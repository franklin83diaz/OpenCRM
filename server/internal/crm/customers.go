package crm

// Customers: every company with a `customer_since`, and how each is doing. The
// facts come from the CRM (meetings and their digests, emails, tasks, insights)
// in a few grouped reads, with what the CRM can see of each account (whether the
// meeting and email syncs are on and working, and which contacts' emails have
// been read); the status and its reasons from meetings_rules.go. Nothing here is
// stored: it is worked out on every read.

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/js"
)

// CustomerRow is one customer on the Customers page: its AccountHealth
// (without the silence reason, which only Focus reads) and the facts behind it.
type CustomerRow struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Domain        *string      `json:"domain"`
	CustomerSince string       `json:"customer_since"`
	RenewalDate   *string      `json:"renewal_date"`
	Status        HealthStatus `json:"status"`
	Reasons       []string     `json:"reasons"`
	LastTouchAt   *string      `json:"last_touch_at"`
	DaysQuiet     *float64     `json:"days_quiet"`
	LastMeetingAt *string      `json:"last_meeting_at"`
	NextMeetingAt *string      `json:"next_meeting_at"`
	LastSummary   *string      `json:"last_summary"`
	// LastSentiment is the last digested call's sentiment as stored (a number, or null).
	LastSentiment any `json:"last_sentiment"`
	OursOpen      int `json:"ours_open"`
	OursOverdue   int `json:"ours_overdue"`
	TheirsOpen    int `json:"theirs_open"`
	Ideas         int `json:"ideas"`
	Expansion     int `json:"expansion"`
	Risks         int `json:"risks"`
	// MainContact and MainPartner are, with the company, who the customer is
	// (nil when it has none, or the contact moved to another company).
	MainContact *RelationRecord `json:"main_contact"`
	MainPartner *RelationRecord `json:"main_partner"`
}

// UpcomingCall is a call with a customer in the next seven days.
type UpcomingCall struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	StartsAt      string  `json:"starts_at"`
	CompanyID     string  `json:"company_id"`
	CompanyName   string  `json:"company_name"`
	CompanyDomain *string `json:"company_domain"`
	OursOpen      int     `json:"ours_open"`
	LastSummary   *string `json:"last_summary"`
}

// InChunks reads `sql` (its `?`s for `before`, then an `IN (…)` list) for the
// ids, 90 at a time: D1 binds at most 100 values.
func InChunks(ctx context.Context, ids []string, sql func(marks string) string, before ...any) ([]db.Row, error) {
	out := []db.Row{}
	for i := 0; i < len(ids); i += 90 {
		part := ids[i:min(i+90, len(ids))]
		rows, err := db.Query(ctx, sql(marks(len(part))), append(append([]any{}, before...), stringParams(part)...)...)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// localDay is today as a calendar day where the viewer is: `tz` is their
// offset from UTC in minutes (a JS number: fractions count, to the
// millisecond). ms is the instant, NaN when it is no date, which threw.
// (index.ts had a localDay of its own for whole-minute offsets: LocalDay.)
func localDay(ms, tz float64) (string, error) {
	t := ms + tz*60_000
	if math.IsNaN(t) || math.Abs(t) > 8.64e15 {
		return "", errInvalidTime
	}
	return isoDay(time.UnixMilli(int64(t))), nil // int64() truncates toward zero, as new Date(t) did
}

// errInvalidTime is the RangeError toISOString threw on a date that is none.
var errInvalidTime = errors.New("Invalid time value")

// msOf is now.getTime().
func msOf(now time.Time) float64 { return float64(now.UnixMilli()) }

// syncState is whether a sync is on, past its first import, and its latest run worked.
func syncState(enabled float64, phase string, lastError *string) SyncState {
	if !js.Truthy(enabled) {
		return "off"
	}
	if phase != "live" {
		return "importing"
	}
	if lastError != nil && *lastError != "" {
		return "failing"
	}
	return "seen"
}

// TouchKinds are activity types that are contact with the account, logged by
// hand or by an agent: not notes, stage changes or Slack posts.
var TouchKinds = []string{"call", "message", "email", "meeting"}

// isoMax is the latest of the rows' `created_at` (stored as SQLite's
// "YYYY-MM-DD HH:MM:SS", UTC) as ISO 8601.
const isoMax = "strftime('%Y-%m-%dT%H:%M:%SZ', MAX(a.created_at))"

// BookedMeeting is a meeting booked with an account.
type BookedMeeting struct {
	Title    string `json:"title"`
	StartsAt string `json:"starts_at"`
}

// Mood is one digested call's mood, as AccountRead keeps it.
type Mood struct {
	Sentiment       any     `json:"sentiment"` // a number, or null
	SentimentReason *string `json:"sentiment_reason"`
	Summary         *string `json:"summary"`
}

// AccountInsight is an open insight on an account.
type AccountInsight struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// AccountRead is what the CRM has seen of one company's contact with us.
type AccountRead struct {
	LastMeetingAt *string        `json:"last_meeting_at"`
	NextMeeting   *BookedMeeting `json:"next_meeting"`
	LastEmailAt   *string        `json:"last_email_at"`
	// LastLoggedAt is the latest call, message, email or meeting logged by hand
	// on the company, its contacts or its deals.
	LastLoggedAt *string `json:"last_logged_at"`
	// Moods are the last two digested calls, newest first.
	Moods []Mood `json:"moods"`
	// Insights are the open insights, newest first.
	Insights []AccountInsight `json:"insights"`
	Sight    Sight            `json:"sight"`
}

// Accounts is what ReadAccounts read: each company's AccountRead (Of), and
// whether the CRM can see calls and emails at all.
type Accounts struct {
	Calls  SyncState
	Emails SyncState
	of     func(id string) AccountRead
}

// Of is what the CRM has seen of the company with this id.
func (a *Accounts) Of(id string) AccountRead { return a.of(id) }

// rowsBy groups rows by their company_id, keeping their order.
func rowsBy(rows []db.Row) map[string][]db.Row {
	out := map[string][]db.Row{}
	for _, r := range rows {
		if id, ok := r["company_id"].(string); ok {
			out[id] = append(out[id], r)
		}
	}
	return out
}

// textOf is a nullable text column: nil for NULL.
func textOf(v any) *string { return nullableText(v) }

// ReadAccounts is what the CRM has seen of each company's contact with us, in
// a few grouped reads: its calls (the last one and the next booked), emails,
// the moods of its last calls, its open insights, and what the CRM can see of
// it (whether the meeting and email syncs are on and working, and which
// contacts' emails have been read). Shared by Customers and the deals'
// progress, so both judge an account on the same facts.
func ReadAccounts(ctx context.Context, ids []string, now time.Time) (*Accounts, error) {
	nowIso := js.ISO(now)
	meetings, err := InChunks(ctx, ids, func(m string) string {
		return `SELECT company_id,
            MAX(CASE WHEN starts_at <= ? THEN starts_at END) AS last_at,
            MIN(CASE WHEN starts_at > ? THEN starts_at END) AS next_at
       FROM meetings WHERE link_status IN ('auto', 'manual') AND company_id IN (` + m + `) GROUP BY company_id`
	}, nowIso, nowIso)
	if err != nil {
		return nil, err
	}
	upcoming, err := InChunks(ctx, ids, func(m string) string {
		return `SELECT company_id, title, starts_at FROM (
       SELECT company_id, title, starts_at, ROW_NUMBER() OVER (PARTITION BY company_id ORDER BY starts_at) AS rn
         FROM meetings WHERE link_status IN ('auto', 'manual') AND starts_at > ? AND company_id IN (` + m + `)
     ) WHERE rn = 1`
	}, nowIso)
	if err != nil {
		return nil, err
	}
	emails, err := InChunks(ctx, ids, func(m string) string {
		return `SELECT company_id, MAX(last_contacted_at) AS last_at FROM contacts WHERE company_id IN (` + m + `) GROUP BY company_id`
	})
	if err != nil {
		return nil, err
	}
	moods, err := InChunks(ctx, ids, func(m string) string {
		return `SELECT company_id, sentiment, sentiment_reason, summary, rn FROM (
       SELECT company_id, sentiment, sentiment_reason, summary,
              ROW_NUMBER() OVER (PARTITION BY company_id ORDER BY starts_at DESC) AS rn
         FROM meetings
        WHERE digest_status = 'done' AND link_status IN ('auto', 'manual') AND company_id IN (` + m + `)
     ) WHERE rn <= 2`
	})
	if err != nil {
		return nil, err
	}
	insights, err := InChunks(ctx, ids, func(m string) string {
		return `SELECT company_id, kind, text FROM insights WHERE status = 'open' AND company_id IN (` + m + `) ORDER BY created_at DESC`
	})
	if err != nil {
		return nil, err
	}
	// Touches logged by hand or by an agent, on the company, its contacts or its
	// deals; one IN list per read, so each stays inside D1's bound-value cap.
	kinds := marks(len(TouchKinds))
	var logged []db.Row
	for _, sql := range []func(m string) string{
		func(m string) string {
			return `SELECT entity_id AS company_id, ` + isoMax + ` AS at FROM activities a
        WHERE a.entity_type = 'company' AND a.type IN (` + kinds + `) AND a.entity_id IN (` + m + `) GROUP BY a.entity_id`
		},
		func(m string) string {
			return `SELECT c.company_id, ` + isoMax + ` AS at FROM activities a JOIN contacts c ON c.id = a.entity_id
        WHERE a.entity_type = 'contact' AND a.type IN (` + kinds + `) AND c.company_id IN (` + m + `) GROUP BY c.company_id`
		},
		func(m string) string {
			return `SELECT d.company_id, ` + isoMax + ` AS at FROM activities a JOIN deals d ON d.id = a.entity_id
        WHERE a.entity_type = 'deal' AND a.type IN (` + kinds + `) AND d.company_id IN (` + m + `) GROUP BY d.company_id`
		},
	} {
		rows, err := InChunks(ctx, ids, sql, stringParams(TouchKinds)...)
		if err != nil {
			return nil, err
		}
		logged = append(logged, rows...)
	}

	settings, err := SyncSettings(ctx)
	if err != nil {
		return nil, err
	}
	calls := SyncState("off")
	if settings != nil {
		calls = syncState(settings.Enabled, settings.Phase, settings.LastError)
	}
	mailbox, err := CurrentAccount(ctx)
	if err != nil {
		return nil, err
	}
	emailSight := SyncState("off")
	if mailbox != nil {
		emailSight = syncState(mailbox.Enabled, mailbox.Phase, mailbox.LastError)
	}
	// Which contacts the email sync reads, and whose history it has read: a
	// contact added after the first import is read the first time it is opened.
	people := []db.Row{}
	if mailbox != nil && (emailSight == "seen" || emailSight == "failing") {
		people, err = InChunks(ctx, ids, func(m string) string {
			return `SELECT c.company_id, c.first_name, lower(trim(c.email)) AS email,
              (SELECT i.email FROM email_contact_imports i WHERE i.mailbox = ? AND i.contact_id = c.id) AS imported
         FROM contacts c WHERE c.email IS NOT NULL AND TRIM(c.email) != '' AND c.company_id IN (` + m + `)
        ORDER BY c.first_name`
		}, mailbox.Mailbox)
		if err != nil {
			return nil, err
		}
	}
	blocklist := []string{}
	if mailbox != nil {
		blocklist = BlocklistOf(mailbox)
	}

	meetingsBy, upcomingBy, emailsBy := rowsBy(meetings), rowsBy(upcoming), rowsBy(emails)
	moodsBy, insightsBy, peopleBy, loggedBy := rowsBy(moods), rowsBy(insights), rowsBy(people), rowsBy(logged)

	of := func(id string) AccountRead {
		out := AccountRead{Moods: []Mood{}, Insights: []AccountInsight{}}
		if mt := meetingsBy[id]; len(mt) > 0 {
			out.LastMeetingAt = textOf(mt[0]["last_at"])
		}
		if next := upcomingBy[id]; len(next) > 0 {
			out.NextMeeting = &BookedMeeting{Title: colText(next[0]["title"]), StartsAt: colText(next[0]["starts_at"])}
		}
		if e := emailsBy[id]; len(e) > 0 {
			out.LastEmailAt = textOf(e[0]["last_at"])
		}
		// The latest of the logged touches: a plain sort of the ISO strings.
		for _, l := range loggedBy[id] {
			if at, ok := l["at"].(string); ok && at != "" && (out.LastLoggedAt == nil || at > *out.LastLoggedAt) {
				out.LastLoggedAt = &at
			}
		}
		ms := append([]db.Row{}, moodsBy[id]...)
		sort.SliceStable(ms, func(i, j int) bool { return js.Number(ms[i]["rn"]) < js.Number(ms[j]["rn"]) })
		for _, m := range ms {
			out.Moods = append(out.Moods, Mood{Sentiment: m["sentiment"], SentimentReason: textOf(m["sentiment_reason"]), Summary: textOf(m["summary"])})
		}
		for _, i := range insightsBy[id] {
			out.Insights = append(out.Insights, AccountInsight{Kind: colText(i["kind"]), Text: colText(i["text"])})
		}
		unread := []string{}
		addressed := 0
		for _, p := range peopleBy[id] {
			email := colText(p["email"])
			if IsBlocked(email, blocklist) {
				continue
			}
			addressed++
			if imported, ok := p["imported"].(string); !ok || imported != email {
				unread = append(unread, colText(p["first_name"]))
			}
		}
		out.Sight = Sight{
			Calls:      calls,
			Emails:     emailSight,
			Addressed:  addressed,
			AllBlocked: addressed == 0 && len(peopleBy[id]) > 0,
			Unread:     unread,
		}
		return out
	}
	return &Accounts{Calls: calls, Emails: emailSight, of: of}, nil
}

// HealthCounts is how many customers have each status.
type HealthCounts struct {
	Red     int `json:"red"`
	Yellow  int `json:"yellow"`
	Unknown int `json:"unknown"`
	Green   int `json:"green"`
}

// SightOfSyncs is whether the CRM can see calls and emails at all.
type SightOfSyncs struct {
	Calls  SyncState `json:"calls"`
	Emails SyncState `json:"emails"`
}

// CustomersOverviewResult is GET /api/customers's answer.
type CustomersOverviewResult struct {
	Customers []CustomerRow  `json:"customers"`
	Focus     []FocusItem    `json:"focus"`
	Upcoming  []UpcomingCall `json:"upcoming"`
	Counts    HealthCounts   `json:"counts"`
	Sight     SightOfSyncs   `json:"sight"`
}

type openTask struct {
	id, title string
	dueDate   *string
}

// CustomersOverview is every customer with its status and reasons, worst
// first; the few things worth doing first; the calls with customers in the
// next seven days; and whether the CRM can see calls and emails at all. `tz`
// is the viewer's offset from UTC in minutes (TS defaulted now to the clock
// and tz to 0).
func CustomersOverview(ctx context.Context, now time.Time, tz float64) (*CustomersOverviewResult, error) {
	companies, err := db.Query(ctx, "SELECT id, name, domain, customer_since, renewal_date FROM companies WHERE customer_since IS NOT NULL AND TRIM(customer_since) != '' ORDER BY name")
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(companies))
	for i, c := range companies {
		ids[i] = colText(c["id"])
	}
	nowIso := js.ISO(now)
	today, err := localDay(msOf(now), tz)
	if err != nil {
		return nil, err
	}

	accounts, err := ReadAccounts(ctx, ids, now)
	if err != nil {
		return nil, err
	}
	tasks, err := InChunks(ctx, ids, func(m string) string {
		return `SELECT id, company_id, title, owed_by, due_date FROM tasks WHERE done_at IS NULL AND company_id IN (` + m + `) ORDER BY due_date`
	})
	if err != nil {
		return nil, err
	}
	tasksBy := rowsBy(tasks)

	focusInput := []FocusAccount{}
	customers := []CustomerRow{}
	for i, c := range companies {
		id := ids[i]
		a := accounts.Of(id)
		var nextAt *string
		if a.NextMeeting != nil {
			nextAt = &a.NextMeeting.StartsAt
		}
		var ours, theirs []openTask
		for _, t := range tasksBy[id] {
			task := openTask{id: colText(t["id"]), title: colText(t["title"]), dueDate: textOf(t["due_date"])}
			if colText(t["owed_by"]) == "them" {
				theirs = append(theirs, task)
			} else {
				ours = append(ours, task)
			}
		}
		overdue := func(list []openTask) []openTask {
			out := []openTask{}
			for _, t := range list {
				if t.dueDate != nil && *t.dueDate != "" && *t.dueDate < today {
					out = append(out, t)
				}
			}
			return out
		}
		asOverdue := func(list []openTask) []OverdueTask {
			out := []OverdueTask{}
			for _, t := range list {
				out = append(out, OverdueTask{Title: t.title, DueDate: *t.dueDate})
			}
			return out
		}
		risks := []string{}
		ideas, expansion := 0, 0
		for _, n := range a.Insights {
			switch n.Kind {
			case "risk":
				risks = append(risks, n.Text)
			case "idea":
				ideas++
			case "expansion":
				expansion++
			}
		}
		sentiments := []Sentiment{}
		for _, m := range a.Moods {
			if m.Sentiment != nil {
				sentiments = append(sentiments, Sentiment{Value: js.Number(m.Sentiment), Reason: m.SentimentReason})
			}
		}
		renewal := textOf(c["renewal_date"])
		oursOverdue := overdue(ours)
		h := Health(AccountFacts{
			LastMeetingAt: a.LastMeetingAt,
			LastEmailAt:   a.LastEmailAt,
			LastLoggedAt:  a.LastLoggedAt,
			NextMeetingAt: nextAt,
			OursOverdue:   asOverdue(oursOverdue),
			TheirsOverdue: asOverdue(overdue(theirs)),
			Sentiments:    sentiments,
			Risks:         risks,
			Sight:         a.Sight,
			RenewalDate:   renewal,
		}, now, today)
		row := CustomerRow{
			ID:            id,
			Name:          colText(c["name"]),
			Domain:        textOf(c["domain"]),
			CustomerSince: colText(c["customer_since"]),
			Status:        h.Status,
			Reasons:       h.Reasons,
			LastTouchAt:   h.LastTouchAt,
			DaysQuiet:     h.DaysQuiet,
			LastMeetingAt: a.LastMeetingAt,
			NextMeetingAt: nextAt,
			OursOpen:      len(ours),
			OursOverdue:   len(oursOverdue),
			TheirsOpen:    len(theirs),
			Ideas:         ideas,
			Expansion:     expansion,
			Risks:         len(risks),
		}
		if renewal != nil && *renewal != "" {
			row.RenewalDate = renewal
		}
		if len(a.Moods) > 0 {
			row.LastSummary = a.Moods[0].Summary
			row.LastSentiment = a.Moods[0].Sentiment
		}
		customers = append(customers, row)
		focusOverdue := []FocusTask{}
		for _, t := range oursOverdue {
			focusOverdue = append(focusOverdue, FocusTask{ID: t.id, Title: t.title, DueDate: *t.dueDate})
		}
		dueToday := []DueTodayTask{}
		for _, t := range ours {
			if t.dueDate != nil && *t.dueDate == today {
				dueToday = append(dueToday, DueTodayTask{ID: t.id, Title: t.title})
			}
		}
		focusInput = append(focusInput, FocusAccount{
			ID:            id,
			Name:          row.Name,
			Status:        h.Status,
			NextMeetingAt: nextAt,
			Silence:       h.Silence,
			RenewalDate:   renewal,
			OursOverdue:   focusOverdue,
			OursDueToday:  dueToday,
			Risks:         risks,
		})
	}
	sort.SliceStable(customers, func(i, j int) bool { return worstFirst(customers[i], customers[j]) < 0 })

	week := js.ISO(now.Add(7 * 24 * time.Hour))
	calls, err := InChunks(ctx, ids, func(m string) string {
		return `SELECT id, title, starts_at, company_id FROM meetings
      WHERE link_status IN ('auto', 'manual') AND starts_at > ? AND starts_at <= ? AND company_id IN (` + m + `)`
	}, nowIso, week)
	if err != nil {
		return nil, err
	}
	named := map[string]*CustomerRow{}
	for i := range customers {
		named[customers[i].ID] = &customers[i]
	}
	sort.SliceStable(calls, func(i, j int) bool {
		return localeCompare(colText(calls[i]["starts_at"]), colText(calls[j]["starts_at"])) < 0
	})
	upcoming := []UpcomingCall{}
	for _, m := range calls {
		c := named[colText(m["company_id"])]
		upcoming = append(upcoming, UpcomingCall{
			ID: colText(m["id"]), Title: colText(m["title"]), StartsAt: colText(m["starts_at"]), CompanyID: c.ID,
			CompanyName: c.Name, CompanyDomain: c.Domain, OursOpen: c.OursOpen, LastSummary: c.LastSummary,
		})
	}

	var counts HealthCounts
	for _, c := range customers {
		switch c.Status {
		case "red":
			counts.Red++
		case "yellow":
			counts.Yellow++
		case "unknown":
			counts.Unknown++
		case "green":
			counts.Green++
		}
	}
	if err := withCustomerPeople(ctx, customers); err != nil {
		return nil, err
	}
	return &CustomersOverviewResult{
		Customers: customers,
		Focus:     Focus(focusInput, 3, today),
		Upcoming:  upcoming,
		Counts:    counts,
		Sight:     SightOfSyncs{Calls: accounts.Calls, Emails: accounts.Emails},
	}, nil
}

// worstFirst is the Customers order: by Severity, then the longest quiet first
// (no contact at all counts as forever), then by name. It is TS's comparator
// as arithmetic, where Infinity - Infinity and NaN fall through to the name.
func worstFirst(a, b CustomerRow) float64 {
	if d := float64(Severity[a.Status] - Severity[b.Status]); d != 0 {
		return d
	}
	quiet := func(q *float64) float64 {
		if q == nil {
			return math.Inf(1)
		}
		return *q
	}
	if d := quiet(b.DaysQuiet) - quiet(a.DaysQuiet); d != 0 && !math.IsNaN(d) {
		return d
	}
	return float64(localeCompare(a.Name, b.Name))
}

// withCustomerPeople sets each customer's main contact and main partner.
func withCustomerPeople(ctx context.Context, customers []CustomerRow) error {
	ids := make([]string, len(customers))
	for i, c := range customers {
		ids[i] = c.ID
	}
	rows, err := InChunks(ctx, ids, func(marks string) string {
		return `SELECT co.id AS company, ct.id AS contact_id, ` + recordLabel(Contact, "ct") + ` AS contact_label, p.id AS partner_id, p.name AS partner_label
       FROM companies co LEFT JOIN contacts ct ON ct.id = co.main_contact_id AND ct.company_id = co.id LEFT JOIN partners p ON p.id = co.main_partner_id
       WHERE co.id IN (` + marks + `) AND (co.main_contact_id IS NOT NULL OR co.main_partner_id IS NOT NULL)`
	})
	if err != nil {
		return err
	}
	byID := map[string]db.Row{}
	for _, r := range rows {
		byID[colText(r["company"])] = r
	}
	for i := range customers {
		r := byID[customers[i].ID]
		if r == nil {
			continue
		}
		if id := colText(r["contact_id"]); id != "" {
			customers[i].MainContact = &RelationRecord{ID: id, Label: colText(r["contact_label"])}
		}
		if id := colText(r["partner_id"]); id != "" {
			customers[i].MainPartner = &RelationRecord{ID: id, Label: colText(r["partner_label"])}
		}
	}
	return nil
}
