package crm

import (
	"math"
	"sort"
	"strings"
	"testing"
	"time"
)

// The expected values below were produced by running the TS functions
// (src/server/meetings-rules.ts) on the same inputs.

var personal = func(d string) bool { return d == "gmail.com" }

func checkJSON(t *testing.T, name string, got any, want string) {
	t.Helper()
	if g := jsonOf(t, got); g != want {
		t.Errorf("%s\n got %s\nwant %s", name, g, want)
	}
}

func TestRulesNormaliseDomain(t *testing.T) {
	in := []any{"https://www.Acme.com/x?y", "HTTP://acme.com#z", " www.acme.com ", 5.0, "a.com\nb", nil}
	want := []string{"acme.com", "acme.com", "acme.com", "", "a.com\nb", ""}
	for i, v := range in {
		if got := NormaliseDomain(v); got != want[i] {
			t.Errorf("NormaliseDomain(%v) = %q, want %q", v, got, want[i])
		}
	}
	if EmailDomain("a@B.com ") != "b.com" || EmailDomain("none") != "" {
		t.Error("EmailDomain")
	}
}

func TestRulesOwnSide(t *testing.T) {
	own := OwnSide([]string{"Me@Mine.com", " me@gmail.com", "x"}, personal)
	var emails, domains []string
	for e := range own.Emails {
		emails = append(emails, e)
	}
	for d := range own.Domains {
		domains = append(domains, d)
	}
	sort.Strings(emails)
	if strings.Join(emails, ",") != "me@gmail.com,me@mine.com" || strings.Join(domains, ",") != "mine.com" {
		t.Errorf("OwnSide = %v %v", emails, domains)
	}
}

func testOwn() Own { return OwnSide([]string{"Me@Mine.com", " me@gmail.com", "x"}, personal) }

func TestRulesEventMeeting(t *testing.T) {
	link := "http://l"
	ev := func(f func(*GoogleEvent)) GoogleEvent {
		e := GoogleEvent{ID: "e1", Summary: " Sync ", HTMLLink: &link,
			Start: &EventTime{DateTime: "2026-10-14T10:00:00-07:00"}, End: &EventTime{DateTime: "2026-10-14T11:00:00Z"},
			Attendees: []CalendarAttendee{{Email: "Ann@x.com", DisplayName: " Ann "}, {Email: "me@mine.com", Self: true}, {Email: "c@mine.com"},
				{Email: "room@resource.calendar.google.com"}, {Email: "r@x.com", Resource: true}, {Email: "ann@x.com"}}}
		if f != nil {
			f(&e)
		}
		return e
	}
	full := `{"id":"e1","title":"Sync","starts_at":"2026-10-14T17:00:00.000Z","ends_at":"2026-10-14T11:00:00.000Z","attendees":[{"email":"ann@x.com","name":"Ann"}],"url":"http://l","internal":false}`
	cases := []struct {
		name string
		e    GoogleEvent
		want string // "" for none
	}{
		{"external", ev(nil), full},
		{"internal only", ev(func(e *GoogleEvent) { e.Attendees = []CalendarAttendee{{Email: "c@mine.com"}} }),
			`{"id":"e1","title":"","starts_at":"2026-10-14T17:00:00.000Z","ends_at":"2026-10-14T11:00:00.000Z","attendees":[],"url":null,"internal":true}`},
		{"cancelled", ev(func(e *GoogleEvent) { e.Status = "cancelled" }), ""},
		{"all day", ev(func(e *GoogleEvent) { e.Start = &EventTime{Date: "2026-10-14"} }), ""},
		{"focus time", ev(func(e *GoogleEvent) { e.EventType = "focusTime" }), ""},
		{"default type", ev(func(e *GoogleEvent) { e.EventType = "default" }), full},
		{"declined", ev(func(e *GoogleEvent) {
			e.Attendees = []CalendarAttendee{{Email: "me@mine.com", Self: true, ResponseStatus: "declined"}, {Email: "a@x.com"}}
		}), ""},
		{"bad start", ev(func(e *GoogleEvent) { e.Start = &EventTime{DateTime: "garbage"} }), ""},
		{"no end", ev(func(e *GoogleEvent) { e.End = nil }),
			`{"id":"e1","title":"Sync","starts_at":"2026-10-14T17:00:00.000Z","ends_at":null,"attendees":[{"email":"ann@x.com","name":"Ann"}],"url":"http://l","internal":false}`},
		{"no link", ev(func(e *GoogleEvent) { e.HTMLLink = nil }),
			`{"id":"e1","title":"Sync","starts_at":"2026-10-14T17:00:00.000Z","ends_at":"2026-10-14T11:00:00.000Z","attendees":[{"email":"ann@x.com","name":"Ann"}],"url":null,"internal":false}`},
	}
	for _, c := range cases {
		got := EventMeeting(c.e, testOwn())
		if c.want == "" {
			if got != nil {
				t.Errorf("%s: want none, got %s", c.name, jsonOf(t, got))
			}
			continue
		}
		if got == nil {
			t.Errorf("%s: got none", c.name)
			continue
		}
		checkJSON(t, c.name, got, c.want)
	}
}

func TestRulesMatchCompany(t *testing.T) {
	idx := CompanyIndex{
		Contacts:       map[string]string{"ann@x.com": "c1"},
		ContactDomains: map[string]string{"y.com": "c2"},
		Domains:        map[string]string{"z.com": "c3", "y.com": "c9"},
	}
	P := func(emails ...string) []Person {
		var out []Person
		for _, e := range emails {
			out = append(out, Person{Email: e})
		}
		return out
	}
	cases := []struct {
		name   string
		people []Person
		want   string // "" for none
	}{
		{"exact address outweighs a domain", P("ann@x.com", "a@y.com"), "c1"},
		{"tie goes to the first invited", P("a@y.com", "b@z.com"), "c2"},
		{"tie, other order", P("b@z.com", "a@y.com"), "c3"},
		{"most people win", P("b@z.com", "c@z.com", "a@y.com"), "c3"},
		{"personal and unknown", P("q@gmail.com", "q@nowhere.com"), ""},
		{"nobody", nil, ""},
	}
	for _, c := range cases {
		got, ok := MatchCompany(c.people, idx, personal)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%s: %q %v, want %q", c.name, got, ok, c.want)
		}
	}
}

func TestRulesNoteWindowAndPlace(t *testing.T) {
	s := func(v string) *string { return &v }
	windows := []struct {
		name string
		n    GranolaNote
		want string // "" for none
	}{
		{"transcript", GranolaNote{Transcript: []TranscriptItem{
			{StartTime: "2026-10-14T10:05:00Z", EndTime: "2026-10-14T10:06:00Z"},
			{StartTime: "2026-10-14T10:01:00Z", EndTime: "2026-10-14T10:30:00Z"}, {StartTime: "bad"}}},
			`{"start":"2026-10-14T10:01:00.000Z","end":"2026-10-14T10:30:00.000Z"}`},
		{"transcript without end", GranolaNote{Transcript: []TranscriptItem{{StartTime: "2026-10-14T10:05:00Z"}}},
			`{"start":"2026-10-14T10:05:00.000Z","end":"2026-10-14T10:05:00.000Z"}`},
		{"scheduled start only", GranolaNote{CalendarEvent: &NoteCalendarEvent{ScheduledStartTime: s("2026-10-14T09:00:00Z")}},
			`{"start":"2026-10-14T09:00:00.000Z","end":"2026-10-14T09:00:00.000Z"}`},
		{"scheduled", GranolaNote{CalendarEvent: &NoteCalendarEvent{ScheduledStartTime: s("2026-10-14T09:00:00Z"), ScheduledEndTime: s("2026-10-14T09:45:00Z")}},
			`{"start":"2026-10-14T09:00:00.000Z","end":"2026-10-14T09:45:00.000Z"}`},
		{"created, SQLite-style", GranolaNote{CreatedAt: "2026-10-14 12:00:00"}, `{"start":"2026-10-14T12:00:00.000Z","end":"2026-10-14T12:00:00.000Z"}`},
		{"nothing", GranolaNote{}, ""},
	}
	for _, c := range windows {
		got, ok := NoteWindow(c.n)
		if c.want == "" {
			if ok {
				t.Errorf("%s: want none, got %v", c.name, got)
			}
			continue
		}
		checkJSON(t, c.name, got, c.want)
	}

	slots := []Slot{
		{ID: "s1", CalendarEventID: s("ev1"), StartsAt: "2026-10-14T09:00:00.000Z", EndsAt: s("2026-10-14T10:00:00.000Z")},
		{ID: "s2", StartsAt: "2026-10-14T10:00:00.000Z"},
		{ID: "s3", StartsAt: "2026-10-14T12:00:00.000Z", EndsAt: s("2026-10-14T12:30:00.000Z")},
	}
	named := func(id string) GranolaNote {
		return GranolaNote{CalendarEvent: &NoteCalendarEvent{CalendarEventID: s(id)}}
	}
	w := func(a, b string) NoteSpan { return NoteSpan{Start: a, End: b} }
	places := []struct {
		name string
		n    GranolaNote
		w    NoteSpan
		want string
	}{
		{"named event", named("ev1"), w("2026-10-14T15:00:00Z", "2026-10-14T15:30:00Z"), "s1"},
		{"unknown name falls back to overlap", named("nope"), w("2026-10-14T09:50:00Z", "2026-10-14T10:20:00Z"), "s2"},
		{"overlap", GranolaNote{}, w("2026-10-14T09:30:00Z", "2026-10-14T09:40:00Z"), "s1"},
		{"zero-length call inside a slot", GranolaNote{}, w("2026-10-14T10:05:00Z", "2026-10-14T10:05:00Z"), "s2"},
		{"within slack of a slot's end", GranolaNote{}, w("2026-10-14T11:00:00Z", "2026-10-14T11:10:00Z"), "s2"},
		{"beyond slack", GranolaNote{}, w("2026-10-14T11:15:00Z", "2026-10-14T11:20:00Z"), ""},
		{"far away", GranolaNote{}, w("2026-10-14T20:00:00Z", "2026-10-14T20:20:00Z"), ""},
		{"unreadable call", GranolaNote{}, w("bad", "bad"), ""},
	}
	for _, c := range places {
		got, ok := PlaceNote(c.n, c.w, slots)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%s: %q %v, want %q", c.name, got, ok, c.want)
		}
	}
}

func TestRulesNotePeople(t *testing.T) {
	s := func(v string) *string { return &v }
	n := GranolaNote{
		Owner: &NotePerson{Name: s("O"), Email: s("Owner@o.com")},
		Attendees: []NotePerson{{Name: s("Ann"), Email: s("ann@x.com")}, {Name: s("O"), Email: s("owner@o.com")}, {},
			{Name: s("Me"), Email: s("me@mine.com")}},
		CalendarEvent: &NoteCalendarEvent{Invitees: []NotePerson{{Email: s("bob@y.com"), Name: s(" Bob ")}, {Email: s("ann@x.com"), Name: s("Dup")}}},
	}
	checkJSON(t, "people", NotePeople(n, testOwn()), `[{"email":"ann@x.com","name":"Ann"},{"email":"bob@y.com","name":"Bob"}]`)
}

func TestRulesTranscriptText(t *testing.T) {
	s := func(v string) *string { return &v }
	T := func(text string, attr, src *string, name string) TranscriptItem {
		return TranscriptItem{Text: text, Speaker: &Speaker{Attribution: attr, Source: src, Name: name}}
	}
	items := []TranscriptItem{T("Hi", s("me"), nil, ""), T("there", s("me"), nil, ""), T("Hello", s("them"), nil, "Zed"), T("  ", nil, nil, ""), T("Bye", s("them"), nil, "")}
	if !SpeakersKnown(items) {
		t.Error("SpeakersKnown")
	}
	if got := TranscriptText(items, 1000); got != "Us: Hi there\nThem (Zed): Hello\nThem: Bye" {
		t.Errorf("labelled: %q", got)
	}
	mic := []TranscriptItem{T("one", nil, s("microphone"), ""), T("two", nil, s("microphone"), "")}
	if SpeakersKnown(mic) || TranscriptText(mic, 1000) != "one\ntwo" {
		t.Errorf("one microphone: unlabelled lines expected, got %q", TranscriptText(mic, 1000))
	}
	mixed := []TranscriptItem{T("a", nil, s("microphone"), ""), T("b", nil, s("system"), ""), T("c", nil, s("system"), "N")}
	if got := TranscriptText(mixed, 1000); got != "Us: a\nThem: b\nThem (N): c" {
		t.Errorf("by source: %q", got)
	}
	if !SpeakersKnown([]TranscriptItem{{Text: "x", Speaker: &Speaker{Name: "Solo"}}}) || SpeakersKnown(nil) || TranscriptText(nil, 10) != "" {
		t.Error("edge cases")
	}
	if got := TranscriptText([]TranscriptItem{{Text: "abcdefghijklmnopqrstuvwxyz"}}, 12); got != "abcd\n[…]\nstuvwxyz" {
		t.Errorf("cut: %q", got)
	}
	// Lengths count UTF-16 units, as in JS: the emoji is two.
	if got := TranscriptText([]TranscriptItem{{Text: "0123456789😀0123456789"}}, 9); got != "012\n[…]\n456789" {
		t.Errorf("cut with emoji: %q", got)
	}
}

func TestRulesDueDate(t *testing.T) {
	in := []any{"2026-10-13", "2026-10-12", "2027-10-15", "2027-10-16", "2026-10-20T10:00", "2026-02-30", "abc", 5.0}
	want := []string{"2026-10-13", "", "2027-10-15", "", "2026-10-20", "", "", ""}
	for i, v := range in {
		got, ok := DueDate(v, "2026-10-14")
		if got != want[i] || ok != (want[i] != "") {
			t.Errorf("DueDate(%v) = %q %v, want %q", v, got, ok, want[i])
		}
	}
	if _, ok := DueDate("2026-10-14", "junk"); ok {
		t.Error("an unreadable meeting day keeps no due date")
	}
}

func TestRulesCoerceDigest(t *testing.T) {
	fence := "Here you go:\n```json\n" + `{"summary":"  Good   call\n today ","sentiment":-1.5,"sentiment_reason":"price","tasks":[{"title":"Send quote","owed_by":"them","due_date":"2026-10-20","quote":"q"},{"title":"send QUOTE"},{"title":"Call back","due_date":"2026-02-31"},null,"x",{"title":"Old","due_date":"2020-01-01"}],"insights":[{"kind":"risk","text":"Budget"},{"kind":"bad","text":"x"},{"kind":"idea","text":""}]}` + "\n```"
	ok := []struct{ name, raw, want string }{
		{"fenced reply", fence, `{"summary":"Good call today","sentiment":-1,"sentiment_reason":"price","tasks":[{"title":"Send quote","owed_by":"them","due_date":"2026-10-20","quote":"q"},{"title":"Call back","owed_by":"us","due_date":null,"quote":null},{"title":"Old","owed_by":"us","due_date":null,"quote":null}],"insights":[{"kind":"risk","text":"Budget","quote":null}]}`},
		{"string sentiment", `{"summary":"s","sentiment":"2"}`, `{"summary":"s","sentiment":2,"sentiment_reason":null,"tasks":[],"insights":[]}`},
		{"true is 1", `{"summary":"s","sentiment":true,"sentiment_reason":"r"}`, `{"summary":"s","sentiment":1,"sentiment_reason":"r","tasks":[],"insights":[]}`},
		{"null sentiment drops its reason", `{"summary":"s","sentiment":null,"sentiment_reason":"r"}`, `{"summary":"s","sentiment":null,"sentiment_reason":null,"tasks":[],"insights":[]}`},
		{"sentiment clamps", `{"summary":"s","sentiment":9}`, `{"summary":"s","sentiment":2,"sentiment_reason":null,"tasks":[],"insights":[]}`},
	}
	for _, c := range ok {
		d, err := CoerceDigest(c.raw, "2026-10-14")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		checkJSON(t, c.name, d, c.want)
	}
	bad := []struct{ raw, want string }{
		{"nope", "The AI's answer wasn't readable"},
		{`{"x":1}`, "The AI's answer had no summary"},
		{"null", "Cannot read properties of null (reading 'summary')"},
		{"[1]", "The AI's answer had no summary"},
		{`{"summary":""}`, "The AI's answer had no summary"},
	}
	for _, c := range bad {
		if _, err := CoerceDigest(c.raw, "2026-10-14"); err == nil || err.Error() != c.want {
			t.Errorf("CoerceDigest(%q) error = %v, want %q", c.raw, err, c.want)
		}
	}
	// At most 12 tasks.
	var many []string
	for i := 0; i < 20; i++ {
		many = append(many, `{"title":"t`+string(rune('a'+i))+`"}`)
	}
	d, _ := CoerceDigest(`{"summary":"s","tasks":[`+strings.Join(many, ",")+`]}`, "2026-10-14")
	if len(d.Tasks) != 12 {
		t.Errorf("tasks = %d", len(d.Tasks))
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
	if PlaceSlackMs != 1_800_000 || FreshDays != 14 || QuietYellowDays != 21 || QuietRedDays != 45 || RenewalSoonDays != 30 || DealQuietDays != 14 {
		t.Error("constants")
	}
	if len(InsightKinds) != 3 || InsightKinds[0] != "idea" || InsightKinds[1] != "expansion" || InsightKinds[2] != "risk" {
		t.Error("InsightKinds")
	}
}
