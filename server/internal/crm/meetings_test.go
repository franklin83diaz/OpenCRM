package crm

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
)

// The meeting sync end to end on fakes (meetingsPlatform, in
// meetings_ts_test.go): a calendar event and its Granola note become a
// meeting linked to the company by the attendee's domain, and the next run
// stores what the model read in the call.
func TestRunMeetingsLinksByDomainAndStoresDigest(t *testing.T) {
	ctx := dbtest.Setup(t)
	mustRun(t, ctx,
		"INSERT INTO companies (id, name, domain) VALUES ('co-acme', 'Acme', 'https://www.acme.example/')",
		"INSERT INTO meeting_sync (id, enabled, history_days, about, phase) VALUES (1, 1, 30, 'CRM software', 'idle')")
	reply := `{"summary":"Acme wants a proposal.","sentiment":2,"sentiment_reason":"Keen to start","tasks":[{"title":"Send the proposal","owed_by":"us","due_date":"2026-10-16","quote":"we will send it Friday"}],"insights":[{"kind":"expansion","text":"Finance wants it too","quote":"finance too"},{"kind":"risk","text":"Budget is tight","quote":null}]}`
	platform, env, conn := newMeetingsPlatform(t, []string{"googlecalendar", "granola"}, meetingsRules{
		Execute: []fakeRule{{Action: "GOOGLECALENDAR_EVENTS_LIST", Data: map[string]any{
			"summary": "me@mine.example",
			"items": []any{map[string]any{
				"id": "ev1", "status": "confirmed", "summary": "Acme weekly", "htmlLink": "https://calendar.google.com/ev1",
				"start": map[string]any{"dateTime": "2026-10-13T10:00:00Z"}, "end": map[string]any{"dateTime": "2026-10-13T10:30:00Z"},
				"attendees": []any{
					map[string]any{"email": "me@mine.example", "self": true},
					map[string]any{"email": "Bob@Acme.example", "displayName": "Bob"},
				},
			}},
		}}},
		Granola: []fakeRule{
			{Path: "/v1/notes", Data: map[string]any{"notes": []any{map[string]any{"id": "note-1", "updated_at": "2026-10-13T11:00:00Z"}}, "hasMore": false}},
			{Path: "/v1/notes/note-1", Data: map[string]any{
				"id": "note-1", "title": "Acme weekly", "web_url": "https://notes.granola.ai/d/note-1", "updated_at": "2026-10-13T11:00:00Z",
				"calendar_event": map[string]any{"calendar_event_id": "ev1"},
				"transcript": []any{
					map[string]any{"text": "We will send it Friday", "start_time": "2026-10-13T10:01:00Z", "end_time": "2026-10-13T10:02:00Z", "speaker": map[string]any{"source": "microphone"}},
					map[string]any{"text": "Finance too", "start_time": "2026-10-13T10:03:00Z", "end_time": "2026-10-13T10:04:00Z", "speaker": map[string]any{"source": "speaker"}},
				},
			}},
		},
		Model: []fakeRule{{Body: map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}}}},
	})
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	got, err := RunMeetings(ctx, env, conn, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := stringify(got); s != `{"status":"ran","job":"sync","phase":"live","stored":1,"notes":1,"more":true}` {
		t.Fatalf("sync run %s", s)
	}
	m, _ := db.Get(ctx, "SELECT * FROM meetings WHERE calendar_event_id = 'ev1'")
	want := map[string]any{
		"title": "Acme weekly", "starts_at": "2026-10-13T10:00:00.000Z", "ends_at": "2026-10-13T10:30:00.000Z",
		"attendees": `[{"email":"bob@acme.example","name":"Bob"}]`, "company_id": "co-acme", "link_status": "auto",
		"calendar_url": "https://calendar.google.com/ev1", "note_id": "note-1", "note_url": "https://notes.granola.ai/d/note-1",
		"note_updated_at": "2026-10-13T11:00:00Z", "digest_status": "queued", "summary": nil,
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("meeting %s = %v, want %v", k, m[k], v)
		}
	}

	got, err = RunMeetings(ctx, env, conn, now.Add(time.Minute), false)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := stringify(got); s != `{"status":"ran","job":"digest","phase":"live","more":false}` {
		t.Fatalf("digest run %s", s)
	}
	m, _ = db.Get(ctx, "SELECT summary, sentiment, sentiment_reason, digest_status, digest_error, digested_at FROM meetings WHERE calendar_event_id = 'ev1'")
	if s, _ := stringify(m); s != `{"digest_error":null,"digest_status":"done","digested_at":"2026-10-14T12:01:00.000Z","sentiment":2,"sentiment_reason":"Keen to start","summary":"Acme wants a proposal."}` {
		t.Errorf("digested meeting %s", s)
	}
	tasks, _ := db.Query(ctx, "SELECT t.title, t.company_id, t.owed_by, t.due_date, t.quote, t.created_by, m.calendar_event_id FROM tasks t JOIN meetings m ON m.id = t.meeting_id")
	if s, _ := stringify(tasks); s != `[{"calendar_event_id":"ev1","company_id":"co-acme","created_by":"ai","due_date":"2026-10-16","owed_by":"us","quote":"we will send it Friday","title":"Send the proposal"}]` {
		t.Errorf("tasks %s", s)
	}
	insights, _ := db.Query(ctx, "SELECT i.kind, i.text, i.quote, i.company_id, i.status, m.calendar_event_id FROM insights i JOIN meetings m ON m.id = i.meeting_id ORDER BY i.text")
	if s, _ := stringify(insights); s != `[{"calendar_event_id":"ev1","company_id":"co-acme","kind":"risk","quote":null,"status":"open","text":"Budget is tight"},{"calendar_event_id":"ev1","company_id":"co-acme","kind":"expansion","quote":"finance too","status":"open","text":"Finance wants it too"}]` {
		t.Errorf("insights %s", s)
	}
	if n, _ := db.Get(ctx, "SELECT COUNT(*) AS n FROM activities"); n["n"] != 0.0 {
		t.Errorf("a sync logs no activity, got %v", n["n"])
	}

	// What the model was asked: the company by name, the call's day, who was there and what was said.
	var asked map[string]any
	for _, c := range platform.calls {
		if m, ok := c.(map[string]any)["model"]; ok {
			asked = m.(map[string]any)
		}
	}
	msgs := asked["messages"].([]any)
	system, user := msgs[0].(map[string]any)["content"].(string), msgs[1].(map[string]any)["content"].(string)
	if !strings.Contains(system, `"Us" is the team that keeps this CRM, who sell: CRM software. "Them" is Acme.`) ||
		!strings.Contains(system, "The call was on Tuesday, 13 October 2026.") || asked["max_tokens"] != 1500.0 {
		t.Errorf("system prompt %q, max_tokens %v", system, asked["max_tokens"])
	}
	if user != "Call: Acme weekly\n\nWith: Bob <bob@acme.example>\n\nTranscript:\nUs: We will send it Friday\nThem: Finance too" {
		t.Errorf("user prompt %q", user)
	}
	s, _ := SyncSettings(ctx)
	if s.Phase != "live" || *s.CalendarOwner != "me@mine.example" || *s.NotesSyncedUntil != "2026-10-13T11:00:00Z" || s.RunningUntil != nil || s.Cursor != nil {
		t.Errorf("settings after the runs %+v", s)
	}
}

func TestMeetingsHelpers(t *testing.T) {
	for _, c := range []struct{ a, b, want string }{
		{"", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"},
		{"2026-01-02T00:00:00Z", "", "2026-01-02T00:00:00Z"},
		{"2026-01-01T00:00:00Z", "2026-01-01T00:00:00.000Z", "2026-01-01T00:00:00Z"}, // a tie keeps the first
		{"garbage", "2026-01-01T00:00:00Z", "garbage"},                               // NaN never wins nor loses
		{"2026-01-01T00:00:00Z", "2027-01-01T00:00:00Z", "2027-01-01T00:00:00Z"},
	} {
		var a, b *string
		if c.a != "" {
			a = &c.a
		}
		if c.b != "" {
			b = &c.b
		}
		if got := laterMark(a, b); deref(got) != c.want {
			t.Errorf("laterMark(%q, %q) = %q, want %q", c.a, c.b, deref(got), c.want)
		}
	}
	if laterMark(nil, nil) != nil {
		t.Error("laterMark of nothing is undefined")
	}
	for day, want := range map[string]string{"2026-10-14": "Wednesday, 14 October 2026", "2025-09-07": "Sunday, 7 September 2025", "bad": "Invalid Date"} {
		if got := longDay(day); got != want {
			t.Errorf("longDay(%s) = %q, want %q", day, got, want)
		}
	}
	if _, err := isoChecked(parseMs("nope")); err == nil || err.Error() != "Invalid time value" {
		t.Errorf("isoChecked(NaN) err %v", err)
	}
	c := parseMeetingCursor(strp(`{"step":"notes","from":"a","to":"b","notes_since":"s","notes_page":null,"max_seen":"m"}`))
	if c.json() != `{"step":"notes","from":"a","to":"b","notes_since":"s","notes_page":null,"max_seen":"m"}` {
		t.Errorf("cursor round trip %s", c.json())
	}
	for _, bad := range []string{`[1]`, `{"step":"live"}`, `nope`, `null`} {
		if parseMeetingCursor(&bad) != nil {
			t.Errorf("cursor %s should read as none", bad)
		}
	}
	if got := jsonPeople(strp(`[{"email":"a@b.co","name":"A","x":1},{"email":5},null,"s",{"name":"n"}]`)); len(got) != 1 || got[0]["x"] != 1.0 {
		t.Errorf("jsonPeople kept %v", got)
	}
}

func TestHonoQueries(t *testing.T) {
	// Expectations from hono/utils/url getQueryParams on the same URL.
	r := httptest.NewRequest("GET", "/x?link=un%6Datched&a=1+2&a=%E0%A4%A&=skip&flag&b=x=y&c=%41%E0%A4&d=%41-%E0%A4", nil)
	got := honoQueries(r)
	want := map[string][]string{"link": {"unmatched"}, "a": {"1 2", "%E0%A4%A"}, "flag": {""}, "b": {"x=y"}, "c": {"%41%E0%A4"}, "d": {"A-%E0%A4"}}
	if canonical(got) != canonical(want) {
		t.Errorf("honoQueries %s, want %s", canonical(got), canonical(want))
	}
	if q := queryInput(r); canonical(q["a"]) != `["1 2","%E0%A4%A"]` || q["link"] != "unmatched" {
		t.Errorf("queryInput %v", q)
	}
	for ct, isJSON := range map[string]bool{
		"application/json": true, "application/json; charset=utf-8": true, "application/vnd.api+json": true,
		"application/json;charset=UTF-8": true, "Application/JSON": false, "application/json;": false, "text/plain": false, "": false,
	} {
		if got := jsonContentType.MatchString(ct); got != isJSON {
			t.Errorf("JSON content type %q: %v", ct, got)
		}
	}
}
