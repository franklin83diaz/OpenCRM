package crm

// Meetings: the rules that outlive the meeting sync — how an account is doing
// and how an open deal is moving. Pure (no I/O, no clock of its own), so each
// rule can be checked on its own; customers.go and deal_progress.go are the
// I/O around them.
//
// Names that TS shared between a type and a function are split here, since Go
// has one namespace: the type Health is HealthStatus and the interface
// NextStep is DealNextStep; the functions keep their TS names (Health,
// NextStep).

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"opencrm/internal/js"
)

var (
	httpLead = regexp.MustCompile(`^https?://`)
	wwwLead  = regexp.MustCompile(`^www\.`)
	dayShape = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// u16Slice is s.slice(from, to) in UTF-16 units, as JS counts string lengths.
func u16Slice(s string, from, to int) string {
	u := utf16.Encode([]rune(s))
	if to > len(u) {
		to = len(u)
	}
	if from >= to {
		return ""
	}
	return string(utf16.Decode(u[from:to]))
}

func u16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// parseMs is Date.parse: milliseconds since the epoch, NaN when s is no date.
// Comparisons and Math.min/max on NaN behave the same in Go as in JS.
func parseMs(s string) float64 {
	t, ok := js.ParseDate(s)
	if !ok {
		return math.NaN()
	}
	return float64(t.UnixMilli())
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToLower(string(r[0])) + string(r[1:])
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

func strp(s string) *string { return &s }

// ── How an account is doing ────────────────────────────────────────

// HealthStatus is "red" | "yellow" | "green" | "unknown" (TS: Health).
// "unknown": nothing says the account is in trouble, but the CRM can't see
// whether anyone has been in touch, so it doesn't claim it is on track either.
type HealthStatus string

// shortcut: fixed thresholds for every workspace; make them a setting if teams ask for their own.

// QuietYellowDays: no call or email for this long turns an account yellow, then red (QuietRedDays).
const (
	QuietYellowDays = 21
	QuietRedDays    = 45
)

// RenewalSoonDays: a renewal this close, or already past, asks for attention.
const RenewalSoonDays = 30

// SyncState is how well the CRM can see one channel of contact with an
// account. "off": not synced. "importing": the first import hasn't finished.
// "failing": synced, but the latest run failed, so the newest contact may be
// missing. "seen" otherwise.
type SyncState string

// Sight is what the CRM can see of an account's contact with us. Silence is
// only ever claimed through a channel that would have heard it: a dashboard
// that can't see a customer's meetings shows the same blank as a customer who
// went quiet.
type Sight struct {
	Calls  SyncState `json:"calls"`
	Emails SyncState `json:"emails"`
	// Addressed: the account's contacts with an email address the sync reads (not blocked).
	Addressed int `json:"addressed"`
	// AllBlocked: some contacts have an address, but every one of them is on the email blocklist.
	AllBlocked bool `json:"all_blocked"`
	// Unread: first names of the addressed contacts whose email history hasn't been read yet.
	Unread []string `json:"unread"`
}

// OverdueTask is an open promise due before today.
type OverdueTask struct {
	Title   string `json:"title"`
	DueDate string `json:"due_date"`
}

// Sentiment is the mood of one digested call.
type Sentiment struct {
	Value  float64 `json:"value"`
	Reason *string `json:"reason"`
}

// AccountFacts is what the health of an account is judged on.
type AccountFacts struct {
	LastMeetingAt *string `json:"last_meeting_at"`
	LastEmailAt   *string `json:"last_email_at"`
	// LastLoggedAt is the latest call, message, email or meeting someone (or an agent) logged by hand.
	LastLoggedAt  *string `json:"last_logged_at,omitempty"`
	NextMeetingAt *string `json:"next_meeting_at"`
	// OursOverdue: our open promises, due before today.
	OursOverdue []OverdueTask `json:"ours_overdue"`
	// TheirsOverdue: their open promises, due before today.
	TheirsOverdue []OverdueTask `json:"theirs_overdue"`
	// Sentiments: moods of the last digested calls, newest first.
	Sentiments []Sentiment `json:"sentiments"`
	// Risks: open risks noted in calls.
	Risks []string `json:"risks"`
	Sight Sight    `json:"sight"`
	// RenewalDate is the next renewal (YYYY-MM-DD), when someone set one.
	RenewalDate *string `json:"renewal_date,omitempty"`
}

// AccountHealth is the verdict on an account and the reasons for it.
type AccountHealth struct {
	Status      HealthStatus `json:"status"`
	Reasons     []string     `json:"reasons"`
	LastTouchAt *string      `json:"last_touch_at"`
	// DaysQuiet is a pointer to a float64 because TS's is a number that can be NaN (written as null).
	DaysQuiet *float64 `json:"days_quiet"`
	// Silence is the reason about silence, when there is one ("No contact in 50 days"): the call to book.
	Silence *string `json:"silence"`
}

// ShortDay is "2026-10-03" as "3 Oct" (en-GB, UTC; September is "Sept" as in
// current ICU — workerd, which ran the TS, prints "3 Sept" too).
func ShortDay(day string) string {
	d, ok := js.ParseDate(u16Slice(day, 0, 10) + "T00:00:00Z")
	if !ok {
		return day
	}
	d = d.UTC()
	month := d.Format("Jan")
	if d.Month() == time.September {
		month = "Sept"
	}
	return js.String(float64(d.Day())) + " " + month
}

func later(a, b *string) *string {
	if a == nil || *a == "" {
		return b
	}
	if b == nil || *b == "" {
		return a
	}
	if parseMs(*a) >= parseMs(*b) {
		return a
	}
	return b
}

// SightGapsResult is what SightGaps reports.
type SightGapsResult struct {
	Calls  bool
	Emails bool
	Gaps   []string
}

// SightGaps is what the CRM can't see of an account, in words, and whether
// each channel can still be judged. A failing channel is judged on what it
// read before it failed; one that is off, still importing, or has no contact
// to read is not judged.
func SightGaps(v Sight) SightGapsResult {
	gaps := []string{}
	switch v.Calls {
	case "off":
		gaps = append(gaps, "calls aren't synced")
	case "importing":
		gaps = append(gaps, "calls are still importing")
	case "failing":
		gaps = append(gaps, "the meeting sync is failing")
	}

	emails := v.Emails == "seen" || v.Emails == "failing"
	if v.Emails == "off" {
		gaps = append(gaps, "email isn't synced")
	} else if v.Emails == "importing" {
		gaps = append(gaps, "email is still importing")
	} else {
		if v.Emails == "failing" {
			gaps = append(gaps, "the email sync is failing")
		}
		if v.Addressed == 0 {
			emails = false
			if v.AllBlocked {
				gaps = append(gaps, "their addresses are on the email blocklist")
			} else {
				gaps = append(gaps, "no contact has an email address")
			}
		} else if len(v.Unread) > 0 {
			if len(v.Unread) >= v.Addressed {
				emails = false
			}
			if len(v.Unread) <= 2 {
				gaps = append(gaps, "emails with "+strings.Join(v.Unread, " and ")+" aren't read yet")
			} else {
				gaps = append(gaps, "emails with "+js.String(float64(len(v.Unread)))+" contacts aren't read yet")
			}
		}
	}
	return SightGapsResult{Calls: v.Calls == "seen" || v.Calls == "failing", Emails: emails, Gaps: gaps}
}

// ContactFacts is what ContactSilence reads of an account or a deal.
type ContactFacts struct {
	LastMeetingAt *string
	LastEmailAt   *string
	LastLoggedAt  *string
	Sight         Sight
}

// Silence is what ContactSilence works out.
type Silence struct {
	Touch *string
	// Quiet is whole days since Touch (nil without one; a pointer to NaN when Touch is no date).
	Quiet   *float64
	Seen    SightGapsResult
	Judged  bool
	Silence *string
}

// ContactSilence is how long an account has gone without a call or email the
// CRM saw, and the reason to give when that is longer than `quietDays`. Recent
// contact the CRM has seen settles it, whatever it can't see. Silence is
// claimed only through a channel it can see, naming the ones it can't.
// Judged: whether contact can be judged at all.
func ContactSilence(f ContactFacts, now time.Time, quietDays float64) Silence {
	// A touch logged by hand (a phone call, a message) is contact too: it can
	// settle that there was recent contact, but its absence proves nothing.
	touch := later(later(f.LastMeetingAt, f.LastEmailAt), f.LastLoggedAt)
	var quiet *float64
	if touch != nil && *touch != "" {
		q := math.Max(0, math.Floor((float64(now.UnixMilli())-parseMs(*touch))/86_400_000))
		quiet = &q
	} else {
		touch = nil
	}
	seen := SightGaps(f.Sight)
	judged := seen.Calls || seen.Emails || (quiet != nil && *quiet <= quietDays)
	unseen := ""
	if len(seen.Gaps) > 0 {
		unseen = " (" + strings.Join(seen.Gaps, ", ") + ")"
	}
	var silence *string
	if seen.Calls || seen.Emails {
		if quiet == nil {
			what := "emails"
			if seen.Calls && seen.Emails {
				what = "calls or emails"
			} else if seen.Calls {
				what = "calls"
			}
			silence = strp("No " + what + " yet" + unseen)
		} else if *quiet > quietDays {
			silence = strp("No contact in " + js.String(*quiet) + " days" + unseen)
		}
	}
	return Silence{Touch: touch, Quiet: quiet, Seen: seen, Judged: judged, Silence: silence}
}

// daysUntil is the days from `today` (YYYY-MM-DD, the viewer's day) to `day`:
// negative when it has passed. False when it isn't a day.
func daysUntil(day, today string) (int, bool) {
	t, ok1 := js.ParseDate(u16Slice(day, 0, 10) + "T00:00:00Z")
	from, ok2 := js.ParseDate(u16Slice(today, 0, 10) + "T00:00:00Z")
	if !ok1 || !ok2 {
		return 0, false
	}
	return int(math.Floor(float64(t.UnixMilli()-from.UnixMilli())/86_400_000 + 0.5)), true
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func moreSuffix(n int) string {
	if n > 1 {
		return " (and " + js.String(float64(n-1)) + " more)"
	}
	return ""
}

// moodReasons are the reasons the last call's mood gives, shared by accounts and deals.
func moodRed(sentiments []Sentiment) (red string, dropped bool) {
	if len(sentiments) == 0 {
		return "", false
	}
	mood := sentiments[0]
	if mood.Value <= -1 {
		if mood.Reason != nil && *mood.Reason != "" {
			red = "Last call went badly: " + *mood.Reason
		} else {
			red = "Last call went badly"
		}
	}
	if len(sentiments) > 1 && mood.Value < sentiments[1].Value && mood.Value <= 0 && mood.Value > -1 {
		dropped = true
	}
	return red, dropped
}

func unjudgedReason(seen SightGapsResult) string {
	return upperFirst(strings.Join(seen.Gaps, ", "))
}

// Health is red when one of our promises is overdue, the last call went
// badly, a risk is open, or there has been no contact for QuietRedDays. Yellow
// when there has been none for QuietYellowDays, they owe something overdue,
// the mood dropped since the call before, a renewal is due within
// RenewalSoonDays (or passed), or nobody has been in touch yet. Green
// otherwise.
//
// Silence is judged only through what the CRM can see (`sight`), and the
// reason names what it can't: "No contact in 39 days (calls aren't synced)".
// When it can see neither calls nor emails and has seen no recent contact, the
// last reason says why ("Calls aren't synced, email isn't synced"), and an
// account with nothing else against it is "unknown" rather than green. Every
// reason is said, worst first: the status is never a score nobody can explain.
// `today` is YYYY-MM-DD, the viewer's day (now.UTC().Format("2006-01-02") by default).
func Health(f AccountFacts, now time.Time, today string) AccountHealth {
	red := []string{}
	yellow := []string{}
	s := ContactSilence(ContactFacts{f.LastMeetingAt, f.LastEmailAt, f.LastLoggedAt, f.Sight}, now, QuietYellowDays)

	if len(f.OursOverdue) == 1 {
		red = append(red, "Overdue: "+f.OursOverdue[0].Title+" (due "+ShortDay(f.OursOverdue[0].DueDate)+")")
	} else if len(f.OursOverdue) > 1 {
		red = append(red, js.String(float64(len(f.OursOverdue)))+" of our promises are overdue")
	}
	moodReason, dropped := moodRed(f.Sentiments)
	if moodReason != "" {
		red = append(red, moodReason)
	}
	if len(f.Risks) > 0 {
		red = append(red, "Risk: "+f.Risks[0]+moreSuffix(len(f.Risks)))
	}
	if s.Silence != nil && s.Quiet != nil && *s.Quiet > QuietRedDays {
		red = append(red, *s.Silence)
	}

	if s.Silence != nil && (s.Quiet == nil || *s.Quiet <= QuietRedDays) {
		yellow = append(yellow, *s.Silence)
	}
	if len(f.TheirsOverdue) > 0 {
		yellow = append(yellow, "Waiting on them: "+f.TheirsOverdue[0].Title+moreSuffix(len(f.TheirsOverdue)))
	}
	if dropped {
		yellow = append(yellow, "The mood dropped since the call before")
	}
	if f.RenewalDate != nil && *f.RenewalDate != "" {
		if renews, ok := daysUntil(*f.RenewalDate, today); ok {
			if renews < 0 {
				yellow = append(yellow, "The renewal date ("+ShortDay(*f.RenewalDate)+") has passed: set the next one")
			} else if renews <= RenewalSoonDays {
				if renews == 0 {
					yellow = append(yellow, "Renews today")
				} else {
					yellow = append(yellow, "Renews in "+js.String(float64(renews))+" day"+plural(renews))
				}
			}
		}
	}

	reasons := append(append([]string{}, red...), yellow...)
	if !s.Judged {
		reasons = append(reasons, unjudgedReason(s.Seen))
	}
	status := HealthStatus("unknown")
	switch {
	case len(red) > 0:
		status = "red"
	case len(yellow) > 0:
		status = "yellow"
	case s.Judged:
		status = "green"
	}
	return AccountHealth{Status: status, Reasons: reasons, LastTouchAt: s.Touch, DaysQuiet: s.Quiet, Silence: s.Silence}
}

// FocusTask is one of our overdue promises on an account.
type FocusTask struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	DueDate string `json:"due_date"`
}

// DueTodayTask is something we owe today.
type DueTodayTask struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// FocusAccount is an account as Focus reads it.
type FocusAccount struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Status        HealthStatus `json:"status"`
	NextMeetingAt *string      `json:"next_meeting_at"`
	// Silence is the account's reason about silence (AccountHealth.Silence), when there is one.
	Silence      *string        `json:"silence"`
	RenewalDate  *string        `json:"renewal_date,omitempty"`
	OursOverdue  []FocusTask    `json:"ours_overdue"`
	OursDueToday []DueTodayTask `json:"ours_due_today"`
	Risks        []string       `json:"risks"`
}

// FocusItem is one thing worth doing first. Kind is "overdue" | "reach_out" |
// "renewal" | "due_today" | "risk".
type FocusItem struct {
	Kind        string `json:"kind"`
	CompanyID   string `json:"company_id"`
	CompanyName string `json:"company_name"`
	Text        string `json:"text"`
	TaskID      string `json:"task_id,omitempty"`
}

// Focus is the few things worth doing first across all customers: our overdue
// promises, oldest first; then red accounts with no call booked; then renewals
// due within RenewalSoonDays, soonest first; then what we owe today; then open
// risks. At most `limit` (3 by default in TS).
func Focus(accounts []FocusAccount, limit int, today string) []FocusItem {
	out := []FocusItem{}
	type owed struct {
		a *FocusAccount
		t FocusTask
	}
	var overdue []owed
	for i := range accounts {
		for _, t := range accounts[i].OursOverdue {
			overdue = append(overdue, owed{&accounts[i], t})
		}
	}
	sort.SliceStable(overdue, func(i, j int) bool { return overdue[i].t.DueDate < overdue[j].t.DueDate })
	for _, o := range overdue {
		out = append(out, FocusItem{Kind: "overdue", CompanyID: o.a.ID, CompanyName: o.a.Name, Text: o.t.Title + " (was due " + ShortDay(o.t.DueDate) + ")", TaskID: o.t.ID})
	}
	for _, a := range accounts {
		if a.Status != "red" || (a.NextMeetingAt != nil && *a.NextMeetingAt != "") {
			continue
		}
		text := "Book a call"
		if a.Silence != nil && *a.Silence != "" {
			text = "Book a call: " + lowerFirst(*a.Silence)
		}
		out = append(out, FocusItem{Kind: "reach_out", CompanyID: a.ID, CompanyName: a.Name, Text: text})
	}
	type renewing struct {
		a    FocusAccount
		days int
	}
	var renew []renewing
	for _, a := range accounts {
		if a.RenewalDate == nil || *a.RenewalDate == "" {
			continue
		}
		if days, ok := daysUntil(*a.RenewalDate, today); ok && days >= 0 && days <= RenewalSoonDays {
			renew = append(renew, renewing{a, days})
		}
	}
	sort.SliceStable(renew, func(i, j int) bool { return renew[i].days < renew[j].days })
	for _, r := range renew {
		text := "Renews today"
		if r.days != 0 {
			text = "Renews in " + js.String(float64(r.days)) + " day" + plural(r.days) + " (" + ShortDay(*r.a.RenewalDate) + ")"
		}
		out = append(out, FocusItem{Kind: "renewal", CompanyID: r.a.ID, CompanyName: r.a.Name, Text: text})
	}
	for _, a := range accounts {
		for _, t := range a.OursDueToday {
			out = append(out, FocusItem{Kind: "due_today", CompanyID: a.ID, CompanyName: a.Name, Text: t.Title, TaskID: t.ID})
		}
	}
	for _, a := range accounts {
		for _, r := range a.Risks {
			out = append(out, FocusItem{Kind: "risk", CompanyID: a.ID, CompanyName: a.Name, Text: r})
		}
	}
	// out.slice(0, limit): a negative limit drops that many from the end.
	if limit < 0 {
		limit += len(out)
		if limit < 0 {
			limit = 0
		}
	}
	if limit < len(out) {
		out = out[:limit]
	}
	return out
}

// ── How an open deal is moving ─────────────────────────────────────

// shortcut: one threshold for every workspace. No operator post at the 20-like
// bar gives a number (docs research, 2026-10-06); make it a setting if teams ask.

// DealQuietDays: no call or email on an open deal for this long is worth a
// word: deals cool in weeks, not months.
const DealQuietDays = 14

// DealNextStep is a deal's next step (TS: NextStep).
type DealNextStep struct {
	Kind  string `json:"kind"` // "meeting" | "task"
	Title string `json:"title"`
	// At is the meeting's start (ISO) or the task's due day (YYYY-MM-DD).
	At string `json:"at"`
	// OwedBy is who owes a task; nil for a meeting.
	OwedBy *OwedBy `json:"owed_by"`
	// Overdue is a task whose day has passed.
	Overdue bool `json:"overdue"`
}

// NextMeeting is the next meeting booked with a deal's company; Day is its date where the viewer is.
type NextMeeting struct {
	Title    string `json:"title"`
	StartsAt string `json:"starts_at"`
	Day      string `json:"day"`
}

// OwedBy is who owes a task: "us" | "them".
type OwedBy string

// DealTask is an open task on a deal.
type DealTask struct {
	Title   string  `json:"title"`
	DueDate *string `json:"due_date"`
	OwedBy  OwedBy  `json:"owed_by"`
}

// DealFacts is what the progress of a deal is judged on.
type DealFacts struct {
	LastMeetingAt *string `json:"last_meeting_at"`
	LastEmailAt   *string `json:"last_email_at"`
	// LastLoggedAt is the latest call, message, email or meeting logged by hand on the deal or its company.
	LastLoggedAt *string `json:"last_logged_at,omitempty"`
	Sight        Sight   `json:"sight"`
	// NextMeeting is the next meeting booked with the deal's company.
	NextMeeting *NextMeeting `json:"next_meeting"`
	// Tasks are the open tasks on the deal, and its company's tasks on no deal. Undated ones are never a next step.
	Tasks []DealTask `json:"tasks"`
	// Sentiments are the moods of the company's last digested calls, newest first.
	Sentiments []Sentiment `json:"sentiments"`
	// Risks are the open risks noted in the company's calls.
	Risks     []string `json:"risks"`
	CloseDate *string  `json:"close_date"`
	// NoCompany: no company on the deal, so no call or email can be matched to it.
	NoCompany bool `json:"no_company,omitempty"`
}

// DealProgress is the verdict on an open deal.
type DealProgress struct {
	Status      HealthStatus  `json:"status"`
	Reasons     []string      `json:"reasons"`
	NextStep    *DealNextStep `json:"next_step"`
	LastTouchAt *string       `json:"last_touch_at"`
	DaysQuiet   *float64      `json:"days_quiet"`
}

// NextStep is a deal's next step: the soonest of the next meeting booked with
// its company and its open tasks with a date, ours or theirs. An overdue task
// is the soonest of all; a task due the day of the meeting yields to the
// meeting. A task without a date is not a next step: nobody agreed when. Only
// f.NextMeeting and f.Tasks are read. Nil when there is none.
func NextStep(f DealFacts, today string) *DealNextStep {
	var task *DealTask
	for i := range f.Tasks {
		t := &f.Tasks[i]
		if t.DueDate == nil || *t.DueDate == "" {
			continue
		}
		if task == nil || *t.DueDate < *task.DueDate {
			task = t
		}
	}
	if task != nil && (f.NextMeeting == nil || *task.DueDate < f.NextMeeting.Day) {
		by := task.OwedBy
		return &DealNextStep{Kind: "task", Title: task.Title, At: *task.DueDate, OwedBy: &by, Overdue: *task.DueDate < today}
	}
	if f.NextMeeting != nil {
		return &DealNextStep{Kind: "meeting", Title: f.NextMeeting.Title, At: f.NextMeeting.StartsAt}
	}
	return nil
}

var callsGap = map[SyncState]string{
	"off":       "calls aren't synced",
	"importing": "calls are still importing",
	"failing":   "the meeting sync is failing",
}

// DealHealth is how an open deal is moving. Red when one of our promises is
// overdue, the last call went badly, a risk is open, or there is no next step
// and nobody has been in touch for DealQuietDays. Yellow when there is no next
// step, no contact for DealQuietDays, they owe something overdue, the close
// date has passed, or the mood dropped since the call before. Silence is
// judged as for customers, only through what the CRM can see (ContactSilence).
func DealHealth(f DealFacts, now time.Time, today string) DealProgress {
	red := []string{}
	yellow := []string{}
	// With no company, no call or email can be matched to the deal: silence is never judged.
	var s Silence
	if f.NoCompany {
		s = Silence{Seen: SightGaps(f.Sight)}
	} else {
		s = ContactSilence(ContactFacts{f.LastMeetingAt, f.LastEmailAt, f.LastLoggedAt, f.Sight}, now, DealQuietDays)
	}
	step := NextStep(f, today)
	overdue := func(by OwedBy) []DealTask {
		var out []DealTask
		for _, t := range f.Tasks {
			if t.OwedBy == by && t.DueDate != nil && *t.DueDate != "" && *t.DueDate < today {
				out = append(out, t)
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return *out[i].DueDate < *out[j].DueDate })
		return out
	}
	ours := overdue("us")
	theirs := overdue("them")

	if len(ours) == 1 {
		red = append(red, "Overdue: "+ours[0].Title+" (due "+ShortDay(*ours[0].DueDate)+")")
	} else if len(ours) > 1 {
		red = append(red, js.String(float64(len(ours)))+" of our promises are overdue")
	}
	moodReason, dropped := moodRed(f.Sentiments)
	if moodReason != "" {
		red = append(red, moodReason)
	}
	if len(f.Risks) > 0 {
		red = append(red, "Risk: "+f.Risks[0]+moreSuffix(len(f.Risks)))
	}

	if step == nil {
		if s.Silence != nil && s.Quiet != nil && *s.Quiet > DealQuietDays {
			red = append(red, "No next step, and "+lowerFirst(*s.Silence))
		} else {
			// A booked call can only count when the CRM sees the calendar.
			gap := callsGap[f.Sight.Calls]
			if gap != "" && !f.NoCompany {
				yellow = append(yellow, "No next step ("+gap+")")
			} else {
				yellow = append(yellow, "No next step")
			}
			if s.Silence != nil {
				yellow = append(yellow, *s.Silence)
			}
		}
	} else if s.Silence != nil {
		yellow = append(yellow, *s.Silence)
	}
	if len(theirs) > 0 {
		yellow = append(yellow, "Waiting on them: "+theirs[0].Title+moreSuffix(len(theirs)))
	}
	if f.CloseDate != nil && *f.CloseDate != "" {
		if closes, ok := daysUntil(*f.CloseDate, today); ok && closes < 0 {
			yellow = append(yellow, "The close date ("+ShortDay(*f.CloseDate)+") has passed: move it or close the deal")
		}
	}
	if dropped {
		yellow = append(yellow, "The mood dropped since the call before")
	}

	reasons := append(append([]string{}, red...), yellow...)
	if f.NoCompany {
		reasons = append(reasons, "No company on this deal, so its calls and emails can't be matched")
	} else if !s.Judged {
		reasons = append(reasons, unjudgedReason(s.Seen))
	}
	status := HealthStatus("unknown")
	switch {
	case len(red) > 0:
		status = "red"
	case len(yellow) > 0:
		status = "yellow"
	case s.Judged && !f.NoCompany:
		status = "green"
	}
	return DealProgress{Status: status, Reasons: reasons, NextStep: step, LastTouchAt: s.Touch, DaysQuiet: s.Quiet}
}
