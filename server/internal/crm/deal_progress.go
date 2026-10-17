package crm

// How each open deal is moving: its next step and the reasons to look at it,
// worked out on every read like the Customers page (customers.go). A deal's
// calls, emails and logged touches are its company's (its own company, else
// its contact's), so a meeting imported before the deal existed counts, and a
// company with two open deals shows the same calls on both. The rules live in
// meetings_rules.go (NextStep, DealHealth).

import (
	"context"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/js"
)

// ProgressDeal is what DealsProgress reads of a deal. CompanyID and ContactID
// keep null (nil) apart from "": TS took a deal's own company with `??`, so
// an empty company_id is the deal's (empty) account, not its contact's.
type ProgressDeal struct {
	ID        string  `json:"id"`
	Stage     string  `json:"stage"`
	CompanyID *string `json:"company_id"`
	ContactID *string `json:"contact_id"`
	CloseDate *string `json:"close_date"`
}

// ProgressDealOf is a deal row (with id, stage, company_id, contact_id and
// close_date) as DealsProgress reads it.
func ProgressDealOf(row db.Row) ProgressDeal {
	return ProgressDeal{
		ID:        colText(row["id"]),
		Stage:     colText(row["stage"]),
		CompanyID: textOf(row["company_id"]),
		ContactID: textOf(row["contact_id"]),
		CloseDate: textOf(row["close_date"]),
	}
}

// Severity orders statuses worst first (TS: SEVERITY, which customers.ts
// declared again for itself).
var Severity = map[HealthStatus]int{"red": 0, "yellow": 1, "unknown": 2, "green": 3}

// ClosedStages are the stages that end a deal: won or lost. Any other stage,
// a renamed or unknown one included, is open.
func ClosedStages(ctx context.Context) (map[string]bool, error) {
	rows, err := db.Query(ctx, "SELECT key FROM stages WHERE is_won = 1 OR is_lost = 1")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range rows {
		out[colText(r["key"])] = true
	}
	return out, nil
}

// DealsProgressResult is the progress of each open deal, by deal id, and
// whether the CRM can see calls and emails at all.
type DealsProgressResult struct {
	Progress map[string]DealProgress
	Sight    SightOfSyncs
}

// hasText is whether a nullable text is truthy: neither null nor "".
func hasText(s *string) bool { return s != nil && *s != "" }

// appendNew appends s to list unless it is there already (a JS Set's order).
func appendNew(list []string, seen map[string]bool, s string) []string {
	if seen[s] {
		return list
	}
	seen[s] = true
	return append(list, s)
}

// DealsProgress is the progress of each open deal among `deals`, by deal id;
// won and lost deals are left out. `tz` is the viewer's offset from UTC in
// minutes, for what counts as today and overdue. Also says whether the CRM can
// see calls and emails at all. (TS defaulted now to the clock and tz to 0.)
func DealsProgress(ctx context.Context, deals []ProgressDeal, now time.Time, tz float64) (*DealsProgressResult, error) {
	closed, err := ClosedStages(ctx)
	if err != nil {
		return nil, err
	}
	var open []ProgressDeal
	for _, d := range deals {
		if !closed[d.Stage] {
			open = append(open, d)
		}
	}

	// A deal with no company of its own takes its contact's.
	contactIDs, seen := []string{}, map[string]bool{}
	for _, d := range open {
		if !hasText(d.CompanyID) && hasText(d.ContactID) {
			contactIDs = appendNew(contactIDs, seen, *d.ContactID)
		}
	}
	rows, err := InChunks(ctx, contactIDs, func(m string) string { return `SELECT id, company_id FROM contacts WHERE id IN (` + m + `)` })
	if err != nil {
		return nil, err
	}
	viaContact := map[string]*string{}
	for _, c := range rows {
		viaContact[colText(c["id"])] = textOf(c["company_id"])
	}
	accountOf := func(d ProgressDeal) *string {
		if d.CompanyID != nil {
			return d.CompanyID
		}
		if hasText(d.ContactID) {
			return viaContact[*d.ContactID]
		}
		return nil
	}
	accountIDs, seen := []string{}, map[string]bool{}
	for _, d := range open {
		if id := accountOf(d); hasText(id) {
			accountIDs = appendNew(accountIDs, seen, *id)
		}
	}
	accounts, err := ReadAccounts(ctx, accountIDs, now)
	if err != nil {
		return nil, err
	}

	// Open tasks: the deal's own, and its company's that are on no deal.
	dealIDs := make([]string, len(open))
	for i, d := range open {
		dealIDs[i] = d.ID
	}
	ownTasks, err := InChunks(ctx, dealIDs, func(m string) string {
		return `SELECT deal_id, company_id, title, owed_by, due_date FROM tasks WHERE done_at IS NULL AND deal_id IN (` + m + `)`
	})
	if err != nil {
		return nil, err
	}
	companyTasks, err := InChunks(ctx, accountIDs, func(m string) string {
		return `SELECT deal_id, company_id, title, owed_by, due_date FROM tasks WHERE done_at IS NULL AND deal_id IS NULL AND company_id IN (` + m + `)`
	})
	if err != nil {
		return nil, err
	}
	// A deal with no company: touches logged on the deal itself are all there is.
	loose := []string{}
	for _, d := range open {
		if !hasText(accountOf(d)) {
			loose = append(loose, d.ID)
		}
	}
	looseLogged, err := InChunks(ctx, loose, func(m string) string {
		return `SELECT entity_id AS deal_id, strftime('%Y-%m-%dT%H:%M:%SZ', MAX(created_at)) AS at FROM activities
      WHERE entity_type = 'deal' AND type IN (` + marks(len(TouchKinds)) + `) AND entity_id IN (` + m + `) GROUP BY entity_id`
	}, stringParams(TouchKinds)...)
	if err != nil {
		return nil, err
	}

	today, err := localDay(msOf(now), tz)
	if err != nil {
		return nil, err
	}
	dealTask := func(t db.Row) DealTask {
		by := OwedBy("us")
		if colText(t["owed_by"]) == "them" {
			by = "them"
		}
		return DealTask{Title: colText(t["title"]), DueDate: textOf(t["due_date"]), OwedBy: by}
	}
	progress := map[string]DealProgress{}
	for _, d := range open {
		account := accountOf(d)
		var a *AccountRead
		if hasText(account) {
			read := accounts.Of(*account)
			a = &read
		}
		tasks := []DealTask{}
		for _, t := range ownTasks {
			if id, ok := t["deal_id"].(string); ok && id == d.ID {
				tasks = append(tasks, dealTask(t))
			}
		}
		if hasText(account) {
			for _, t := range companyTasks {
				if id, ok := t["company_id"].(string); ok && id == *account {
					tasks = append(tasks, dealTask(t))
				}
			}
		}
		f := DealFacts{
			Sight:      Sight{Calls: accounts.Calls, Emails: accounts.Emails, Unread: []string{}},
			Tasks:      tasks,
			Sentiments: []Sentiment{},
			Risks:      []string{},
			NoCompany:  !hasText(account),
		}
		if a != nil {
			f.LastMeetingAt, f.LastEmailAt, f.LastLoggedAt, f.Sight = a.LastMeetingAt, a.LastEmailAt, a.LastLoggedAt, a.Sight
			if a.NextMeeting != nil {
				// new Date(starts_at): a start that is no date threw here.
				day, err := localDay(parseMs(a.NextMeeting.StartsAt), tz)
				if err != nil {
					return nil, err
				}
				f.NextMeeting = &NextMeeting{Title: a.NextMeeting.Title, StartsAt: a.NextMeeting.StartsAt, Day: day}
			}
			for _, m := range a.Moods {
				if m.Sentiment != nil {
					f.Sentiments = append(f.Sentiments, Sentiment{Value: js.Number(m.Sentiment), Reason: m.SentimentReason})
				}
			}
			for _, i := range a.Insights {
				if i.Kind == "risk" {
					f.Risks = append(f.Risks, i.Text)
				}
			}
		}
		if f.LastLoggedAt == nil {
			for _, l := range looseLogged {
				if id, ok := l["deal_id"].(string); ok && id == d.ID {
					f.LastLoggedAt = textOf(l["at"])
					break
				}
			}
		}
		if d.CloseDate != nil {
			if c := js.Trim(*d.CloseDate); c != "" {
				f.CloseDate = &c
			}
		}
		progress[d.ID] = DealHealth(f, now, today)
	}
	return &DealsProgressResult{Progress: progress, Sight: SightOfSyncs{Calls: accounts.Calls, Emails: accounts.Emails}}, nil
}
