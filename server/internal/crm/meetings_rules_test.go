package crm

import (
	"math"
	"testing"
	"time"
)

// The expected values below were produced by running the TS functions
// (src/server/meetings-rules.ts) on the same inputs.

func checkJSON(t *testing.T, name string, got any, want string) {
	t.Helper()
	if g := jsonOf(t, got); g != want {
		t.Errorf("%s\n got %s\nwant %s", name, g, want)
	}
}

func TestRulesShortDay(t *testing.T) {
	in := []string{"2026-10-03", "2026-09-30", "2026-01-01", "nonsense", "2026-10-03T12:00"}
	want := []string{"3 Oct", "30 Sept", "1 Jan", "nonsense", "3 Oct"}
	for i, d := range in {
		if got := ShortDay(d); got != want[i] {
			t.Errorf("ShortDay(%q) = %q, want %q", d, got, want[i])
		}
	}
}

var (
	testNow   = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	testToday = "2026-10-14"
)

func ago(n int) *string {
	s := jsISO(testNow.Add(-time.Duration(n) * 24 * time.Hour))
	return &s
}

func seenSight() Sight { return Sight{Calls: "seen", Emails: "seen", Addressed: 2, Unread: []string{}} }

func accountFacts(f func(*AccountFacts)) AccountFacts {
	a := AccountFacts{OursOverdue: []OverdueTask{}, TheirsOverdue: []OverdueTask{}, Sentiments: []Sentiment{}, Risks: []string{}, Sight: seenSight()}
	f(&a)
	return a
}

func TestRulesHealth(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name string
		f    func(*AccountFacts)
		want string
	}{
		{"46 days quiet is red", func(a *AccountFacts) { a.LastEmailAt = ago(46) },
			`{"status":"red","reasons":["No contact in 46 days"],"last_touch_at":"2026-08-29T12:00:00.000Z","days_quiet":46,"silence":"No contact in 46 days"}`},
		{"30 days quiet is yellow", func(a *AccountFacts) { a.LastEmailAt = ago(30) },
			`{"status":"yellow","reasons":["No contact in 30 days"],"last_touch_at":"2026-09-14T12:00:00.000Z","days_quiet":30,"silence":"No contact in 30 days"}`},
		{"recent is green", func(a *AccountFacts) { a.LastEmailAt = ago(3) },
			`{"status":"green","reasons":[],"last_touch_at":"2026-10-11T12:00:00.000Z","days_quiet":3,"silence":null}`},
		{"a logged touch cannot make it quiet", func(a *AccountFacts) { a.LastEmailAt = ago(3); a.LastLoggedAt = ago(60) },
			`{"status":"green","reasons":[],"last_touch_at":"2026-10-11T12:00:00.000Z","days_quiet":3,"silence":null}`},
		{"no contact yet", func(a *AccountFacts) {},
			`{"status":"yellow","reasons":["No calls or emails yet"],"last_touch_at":null,"days_quiet":null,"silence":"No calls or emails yet"}`},
		{"all syncs off is unknown", func(a *AccountFacts) { a.Sight.Calls, a.Sight.Emails = "off", "off" },
			`{"status":"unknown","reasons":["Calls aren't synced, email isn't synced"],"last_touch_at":null,"days_quiet":null,"silence":null}`},
		{"recent contact settles it whatever is unseen", func(a *AccountFacts) { a.LastEmailAt = ago(2); a.Sight.Calls, a.Sight.Emails = "off", "off" },
			`{"status":"green","reasons":[],"last_touch_at":"2026-10-12T12:00:00.000Z","days_quiet":2,"silence":null}`},
		{"silence names what it cannot see", func(a *AccountFacts) { a.LastEmailAt = ago(50); a.Sight.Calls = "off" },
			`{"status":"red","reasons":["No contact in 50 days (calls aren't synced)"],"last_touch_at":"2026-08-25T12:00:00.000Z","days_quiet":50,"silence":"No contact in 50 days (calls aren't synced)"}`},
		{"importing and blocked", func(a *AccountFacts) {
			a.Sight = Sight{Calls: "importing", Emails: "seen", AllBlocked: true, Unread: []string{}}
		},
			`{"status":"unknown","reasons":["Calls are still importing, their addresses are on the email blocklist"],"last_touch_at":null,"days_quiet":null,"silence":null}`},
		{"failing channels, three unread", func(a *AccountFacts) {
			a.Sight = Sight{Calls: "failing", Emails: "failing", Addressed: 3, Unread: []string{"Ann", "Bob", "Cy"}}
		}, `{"status":"yellow","reasons":["No calls yet (the meeting sync is failing, the email sync is failing, emails with 3 contacts aren't read yet)"],"last_touch_at":null,"days_quiet":null,"silence":"No calls yet (the meeting sync is failing, the email sync is failing, emails with 3 contacts aren't read yet)"}`},
		{"two unread of two", func(a *AccountFacts) {
			a.LastMeetingAt = ago(1)
			a.Sight = Sight{Calls: "seen", Emails: "seen", Addressed: 2, Unread: []string{"Ann", "Bob"}}
		}, `{"status":"green","reasons":[],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
		{"one overdue promise", func(a *AccountFacts) {
			a.LastMeetingAt = ago(1)
			a.OursOverdue = []OverdueTask{{Title: "Send deck", DueDate: "2026-10-03"}}
		}, `{"status":"red","reasons":["Overdue: Send deck (due 3 Oct)"],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
		{"several overdue, theirs too", func(a *AccountFacts) {
			a.LastMeetingAt = ago(1)
			a.OursOverdue = []OverdueTask{{Title: "A", DueDate: "2026-10-03"}, {Title: "B", DueDate: "2026-10-04"}}
			a.TheirsOverdue = []OverdueTask{{Title: "Sign", DueDate: "2026-10-01"}, {Title: "X", DueDate: "2026-10-01"}}
		}, `{"status":"red","reasons":["2 of our promises are overdue","Waiting on them: Sign (and 1 more)"],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
		{"bad call and risks", func(a *AccountFacts) {
			a.LastMeetingAt = ago(1)
			a.Sentiments = []Sentiment{{Value: -2, Reason: str("angry")}, {Value: 0}}
			a.Risks = []string{"Budget cut", "Churn"}
		}, `{"status":"red","reasons":["Last call went badly: angry","Risk: Budget cut (and 1 more)"],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
		{"bad call without a reason", func(a *AccountFacts) { a.LastMeetingAt = ago(1); a.Sentiments = []Sentiment{{Value: -1}} },
			`{"status":"red","reasons":["Last call went badly"],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
		{"mood dropped", func(a *AccountFacts) { a.LastMeetingAt = ago(1); a.Sentiments = []Sentiment{{Value: 0}, {Value: 1}} },
			`{"status":"yellow","reasons":["The mood dropped since the call before"],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
		{"a bad mood is red, not a drop", func(a *AccountFacts) { a.LastMeetingAt = ago(1); a.Sentiments = []Sentiment{{Value: -1}, {Value: 1}} },
			`{"status":"red","reasons":["Last call went badly"],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
		{"renews today", func(a *AccountFacts) { a.LastMeetingAt = ago(1); a.RenewalDate = str("2026-10-14") },
			`{"status":"yellow","reasons":["Renews today"],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
		{"renews tomorrow", func(a *AccountFacts) { a.LastMeetingAt = ago(1); a.RenewalDate = str("2026-10-15") },
			`{"status":"yellow","reasons":["Renews in 1 day"],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
		{"renews in 30 days", func(a *AccountFacts) { a.LastMeetingAt = ago(1); a.RenewalDate = str("2026-11-13") },
			`{"status":"yellow","reasons":["Renews in 30 days"],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
		{"renews in 31 days", func(a *AccountFacts) { a.LastMeetingAt = ago(1); a.RenewalDate = str("2026-11-14") },
			`{"status":"green","reasons":[],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
		{"renewal passed", func(a *AccountFacts) { a.LastMeetingAt = ago(1); a.RenewalDate = str("2026-09-30") },
			`{"status":"yellow","reasons":["The renewal date (30 Sept) has passed: set the next one"],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
		{"email still importing", func(a *AccountFacts) { a.LastEmailAt = ago(30); a.Sight.Emails = "importing" },
			`{"status":"yellow","reasons":["No contact in 30 days (email is still importing)"],"last_touch_at":"2026-09-14T12:00:00.000Z","days_quiet":30,"silence":"No contact in 30 days (email is still importing)"}`},
		{"no addressed contact", func(a *AccountFacts) { a.Sight.Addressed = 0 },
			`{"status":"yellow","reasons":["No calls yet (no contact has an email address)"],"last_touch_at":null,"days_quiet":null,"silence":"No calls yet (no contact has an email address)"}`},
		{"every addressed contact unread", func(a *AccountFacts) { a.Sight.Addressed = 2; a.Sight.Unread = []string{"Ann", "Bob"} },
			`{"status":"yellow","reasons":["No calls yet (emails with Ann and Bob aren't read yet)"],"last_touch_at":null,"days_quiet":null,"silence":"No calls yet (emails with Ann and Bob aren't read yet)"}`},
		{"some addressed contacts unread", func(a *AccountFacts) { a.Sight.Addressed = 3; a.Sight.Unread = []string{"Ann", "Bob"} },
			`{"status":"yellow","reasons":["No calls or emails yet (emails with Ann and Bob aren't read yet)"],"last_touch_at":null,"days_quiet":null,"silence":"No calls or emails yet (emails with Ann and Bob aren't read yet)"}`},
		{"calls off, no touch", func(a *AccountFacts) { a.Sight.Calls = "off" },
			`{"status":"yellow","reasons":["No emails yet (calls aren't synced)"],"last_touch_at":null,"days_quiet":null,"silence":"No emails yet (calls aren't synced)"}`},
		{"renewal not a date", func(a *AccountFacts) { a.LastMeetingAt = ago(1); a.RenewalDate = str("garbage") },
			`{"status":"green","reasons":[],"last_touch_at":"2026-10-13T12:00:00.000Z","days_quiet":1,"silence":null}`},
	}
	for _, c := range cases {
		checkJSON(t, c.name, Health(accountFacts(c.f), testNow, testToday), c.want)
	}
}

func TestRulesContactSilenceUnreadableTouch(t *testing.T) {
	// A touch that is no date makes the quiet time NaN, which is neither quiet nor recent.
	junk := "junk"
	s := ContactSilence(ContactFacts{LastEmailAt: &junk, Sight: Sight{Calls: "off", Emails: "off"}}, testNow, 21)
	if s.Quiet == nil || !math.IsNaN(*s.Quiet) || s.Judged || s.Silence != nil {
		t.Errorf("silence = %+v", s)
	}
	h := Health(AccountFacts{LastEmailAt: &junk, Sight: Sight{Calls: "off", Emails: "off"}}, testNow, testToday)
	if h.Status != "unknown" || h.DaysQuiet == nil || !math.IsNaN(*h.DaysQuiet) {
		t.Errorf("health = %+v", h)
	}
}

func dealFacts(f func(*DealFacts)) DealFacts {
	d := DealFacts{Sight: seenSight(), Tasks: []DealTask{}, Sentiments: []Sentiment{}, Risks: []string{}}
	f(&d)
	return d
}

func TestRulesDealHealth(t *testing.T) {
	str := func(s string) *string { return &s }
	demo := &NextMeeting{Title: "Demo", StartsAt: "2026-10-20T10:00:00.000Z", Day: "2026-10-20"}
	const demoStep = `{"kind":"meeting","title":"Demo","at":"2026-10-20T10:00:00.000Z","owed_by":null,"overdue":false}`
	cases := []struct {
		name string
		f    func(*DealFacts)
		want string
	}{
		{"no next step", func(d *DealFacts) { d.LastEmailAt = ago(2) },
			`{"status":"yellow","reasons":["No next step"],"next_step":null,"last_touch_at":"2026-10-12T12:00:00.000Z","days_quiet":2}`},
		{"no next step and quiet", func(d *DealFacts) { d.LastEmailAt = ago(20) },
			`{"status":"red","reasons":["No next step, and no contact in 20 days"],"next_step":null,"last_touch_at":"2026-09-24T12:00:00.000Z","days_quiet":20}`},
		{"quiet with a meeting booked", func(d *DealFacts) { d.LastEmailAt = ago(20); d.NextMeeting = demo },
			`{"status":"yellow","reasons":["No contact in 20 days"],"next_step":` + demoStep + `,"last_touch_at":"2026-09-24T12:00:00.000Z","days_quiet":20}`},
		{"calls not synced", func(d *DealFacts) { d.LastEmailAt = ago(2); d.Sight.Calls = "off" },
			`{"status":"yellow","reasons":["No next step (calls aren't synced)"],"next_step":null,"last_touch_at":"2026-10-12T12:00:00.000Z","days_quiet":2}`},
		{"close date passed", func(d *DealFacts) { d.LastEmailAt = ago(2); d.NextMeeting = demo; d.CloseDate = str("2026-10-01") },
			`{"status":"yellow","reasons":["The close date (1 Oct) has passed: move it or close the deal"],"next_step":` + demoStep + `,"last_touch_at":"2026-10-12T12:00:00.000Z","days_quiet":2}`},
		{"overdue task is the next step", func(d *DealFacts) {
			d.LastEmailAt = ago(2)
			d.Tasks = []DealTask{{Title: "Late", DueDate: str("2026-10-10"), OwedBy: "us"}, {Title: "Soon", DueDate: str("2026-10-30"), OwedBy: "them"}, {Title: "NoDate", OwedBy: "us"}}
		}, `{"status":"red","reasons":["Overdue: Late (due 10 Oct)"],"next_step":{"kind":"task","title":"Late","at":"2026-10-10","owed_by":"us","overdue":true},"last_touch_at":"2026-10-12T12:00:00.000Z","days_quiet":2}`},
		{"waiting on them", func(d *DealFacts) {
			d.LastEmailAt = ago(2)
			d.CloseDate = str("2026-12-01")
			d.Tasks = []DealTask{{Title: "Wait", DueDate: str("2026-10-10"), OwedBy: "them"}, {Title: "Wait2", DueDate: str("2026-10-11"), OwedBy: "them"}}
		}, `{"status":"yellow","reasons":["Waiting on them: Wait (and 1 more)"],"next_step":{"kind":"task","title":"Wait","at":"2026-10-10","owed_by":"them","overdue":true},"last_touch_at":"2026-10-12T12:00:00.000Z","days_quiet":2}`},
		{"no company", func(d *DealFacts) { d.NoCompany = true; d.Sight.Calls = "off" },
			`{"status":"yellow","reasons":["No next step","No company on this deal, so its calls and emails can't be matched"],"next_step":null,"last_touch_at":null,"days_quiet":null}`},
		{"no company with a risk", func(d *DealFacts) { d.NoCompany = true; d.LastEmailAt = ago(1); d.Risks = []string{"R"} },
			`{"status":"red","reasons":["Risk: R","No next step","No company on this deal, so its calls and emails can't be matched"],"next_step":null,"last_touch_at":null,"days_quiet":null}`},
		{"nothing synced", func(d *DealFacts) { d.Sight.Calls, d.Sight.Emails = "off", "off" },
			`{"status":"yellow","reasons":["No next step (calls aren't synced)","Calls aren't synced, email isn't synced"],"next_step":null,"last_touch_at":null,"days_quiet":null}`},
		{"a task due the meeting day yields to it", func(d *DealFacts) {
			d.LastEmailAt = ago(2)
			d.NextMeeting = demo
			d.Tasks = []DealTask{{Title: "Same day", DueDate: str("2026-10-20"), OwedBy: "us"}}
			d.Sentiments = []Sentiment{{Value: 0}, {Value: 1}}
		}, `{"status":"yellow","reasons":["The mood dropped since the call before"],"next_step":` + demoStep + `,"last_touch_at":"2026-10-12T12:00:00.000Z","days_quiet":2}`},
		{"meeting booked and recent contact is green", func(d *DealFacts) { d.LastEmailAt = ago(2); d.NextMeeting = demo },
			`{"status":"green","reasons":[],"next_step":` + demoStep + `,"last_touch_at":"2026-10-12T12:00:00.000Z","days_quiet":2}`},
		{"meeting booked, both syncs off", func(d *DealFacts) { d.NextMeeting = demo; d.Sight.Calls, d.Sight.Emails = "off", "off" },
			`{"status":"unknown","reasons":["Calls aren't synced, email isn't synced"],"next_step":` + demoStep + `,"last_touch_at":null,"days_quiet":null}`},
		{"email off, no touch", func(d *DealFacts) { d.Sight.Emails = "off" },
			`{"status":"yellow","reasons":["No next step","No calls yet (email isn't synced)"],"next_step":null,"last_touch_at":null,"days_quiet":null}`},
		{"two promises overdue", func(d *DealFacts) {
			d.LastEmailAt = ago(2)
			d.Tasks = []DealTask{{Title: "Two", DueDate: str("2026-10-01"), OwedBy: "us"}, {Title: "Three", DueDate: str("2026-10-02"), OwedBy: "us"}}
		}, `{"status":"red","reasons":["2 of our promises are overdue"],"next_step":{"kind":"task","title":"Two","at":"2026-10-01","owed_by":"us","overdue":true},"last_touch_at":"2026-10-12T12:00:00.000Z","days_quiet":2}`},
	}
	for _, c := range cases {
		checkJSON(t, c.name, DealHealth(dealFacts(c.f), testNow, testToday), c.want)
	}
	// A deal whose close date passed, as in the brief, with the contact otherwise fine.
	d := dealFacts(func(d *DealFacts) { d.LastEmailAt = ago(1); d.NextMeeting = demo; d.CloseDate = str("2026-10-13") })
	if got := DealHealth(d, testNow, testToday); got.Status != "yellow" || got.Reasons[0] != "The close date (13 Oct) has passed: move it or close the deal" {
		t.Errorf("close date passed: %+v", got)
	}
	if NextStep(DealFacts{}, testToday) != nil {
		t.Error("NextStep of nothing must be nil")
	}
}

func TestRulesFocus(t *testing.T) {
	str := func(s string) *string { return &s }
	acc := func(f func(*FocusAccount)) FocusAccount {
		a := FocusAccount{Status: "green", OursOverdue: []FocusTask{}, OursDueToday: []DueTodayTask{}, Risks: []string{}}
		f(&a)
		return a
	}
	accs := []FocusAccount{
		acc(func(a *FocusAccount) {
			a.ID, a.Name, a.Status, a.Silence, a.RenewalDate = "a", "A", "red", str("No contact in 50 days"), str("2026-10-20")
			a.OursOverdue, a.Risks = []FocusTask{{ID: "t2", Title: "Later", DueDate: "2026-10-05"}}, []string{"Budget"}
		}),
		acc(func(a *FocusAccount) {
			a.ID, a.Name, a.Status = "b", "B", "red"
			a.OursOverdue, a.OursDueToday = []FocusTask{{ID: "t1", Title: "Earlier", DueDate: "2026-10-01"}}, []DueTodayTask{{ID: "t3", Title: "Today"}}
		}),
		acc(func(a *FocusAccount) {
			a.ID, a.Name, a.Status, a.NextMeetingAt, a.RenewalDate = "c", "C", "red", str("2026-10-20T00:00:00Z"), str("2026-10-14")
		}),
		acc(func(a *FocusAccount) {
			a.ID, a.Name, a.RenewalDate, a.Risks = "d", "D", str("2026-10-15"), []string{"R2"}
		}),
		acc(func(a *FocusAccount) { a.ID, a.Name, a.RenewalDate = "e", "E", str("2026-09-01") }),
	}
	checkJSON(t, "all", Focus(accs, 100, testToday),
		`[{"kind":"overdue","company_id":"b","company_name":"B","text":"Earlier (was due 1 Oct)","task_id":"t1"},{"kind":"overdue","company_id":"a","company_name":"A","text":"Later (was due 5 Oct)","task_id":"t2"},{"kind":"reach_out","company_id":"a","company_name":"A","text":"Book a call: no contact in 50 days"},{"kind":"reach_out","company_id":"b","company_name":"B","text":"Book a call"},{"kind":"renewal","company_id":"c","company_name":"C","text":"Renews today"},{"kind":"renewal","company_id":"d","company_name":"D","text":"Renews in 1 day (15 Oct)"},{"kind":"renewal","company_id":"a","company_name":"A","text":"Renews in 6 days (20 Oct)"},{"kind":"due_today","company_id":"b","company_name":"B","text":"Today","task_id":"t3"},{"kind":"risk","company_id":"a","company_name":"A","text":"Budget"},{"kind":"risk","company_id":"d","company_name":"D","text":"R2"}]`)
	checkJSON(t, "limit", Focus(accs, 3, testToday),
		`[{"kind":"overdue","company_id":"b","company_name":"B","text":"Earlier (was due 1 Oct)","task_id":"t1"},{"kind":"overdue","company_id":"a","company_name":"A","text":"Later (was due 5 Oct)","task_id":"t2"},{"kind":"reach_out","company_id":"a","company_name":"A","text":"Book a call: no contact in 50 days"}]`)
	checkJSON(t, "empty", Focus(nil, 3, testToday), `[]`)
	if got := Focus(accs, -2, testToday); len(got) != 8 {
		t.Errorf("negative limit drops from the end: %d items", len(got))
	}
	if got := Focus(accs, -100, testToday); got == nil || len(got) != 0 {
		t.Errorf("negative limit past the start: %v", got)
	}
}

func TestRulesConstants(t *testing.T) {
	if QuietYellowDays != 21 || QuietRedDays != 45 || RenewalSoonDays != 30 || DealQuietDays != 14 {
		t.Error("constants")
	}
}
