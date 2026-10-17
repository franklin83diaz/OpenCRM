package crm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// The parity run has nothing connected; these cover the meetings routes with
// the calendar, Granola and the queue faked. Expected values follow index.ts
// 2491–2712 and meetings.ts.

type meetingsFixture struct {
	t        *testing.T
	ctx      context.Context
	platform *meetingsPlatform
	queue    *fakeQueue
	api      http.Handler
}

func newMeetingsFixture(t *testing.T, connected []string, rules meetingsRules) *meetingsFixture {
	ctx := dbtest.Setup(t)
	platform, env, conn := newMeetingsPlatform(t, connected, rules)
	q := &fakeQueue{}
	qs := httptest.NewServer(q)
	t.Cleanup(qs.Close)
	env.QueueURL = qs.URL
	return &meetingsFixture{t: t, ctx: ctx, platform: platform, queue: q, api: New(&App{Env: env, Conn: conn})}
}

func (f *meetingsFixture) do(method, path, body string, headers map[string]string) (int, string) {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.api.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func (f *meetingsFixture) want(method, path, body string, headers map[string]string, status int, raw string) {
	f.t.Helper()
	gotStatus, gotRaw := f.do(method, path, body, headers)
	if gotStatus != status || gotRaw != raw {
		f.t.Errorf("%s %s %s\n got  %d %s\n want %d %s", method, path, body, gotStatus, gotRaw, status, raw)
	}
}

func (f *meetingsFixture) jobs() []map[string]any {
	f.queue.mu.Lock()
	defer f.queue.mu.Unlock()
	return append([]map[string]any{}, f.queue.enqueued...)
}

// bookedIn is how far from now a booked job's run_at is.
func bookedIn(t *testing.T, job map[string]any) time.Duration {
	t.Helper()
	at, ok := js.ParseDate(job["run_at"].(string))
	if !ok {
		t.Fatalf("run_at %v", job["run_at"])
	}
	return time.Until(at)
}

const noCounts = `"counts":{"meetings":0,"linked":0,"unmatched":0,"with_notes":0,"digested":0,"waiting":0,"failed":0}`

func TestMeetingsSyncSettingsConnected(t *testing.T) {
	f := newMeetingsFixture(t, []string{"googlecalendar"}, meetingsRules{})

	status, raw := f.do("PUT", "/api/meetings/sync", `{"enabled":true,"about":"  CRM  "}`, asUser)
	jobs := f.jobs()
	if status != 200 || len(jobs) != 1 {
		t.Fatalf("turn on: %d %s, jobs %v", status, raw, jobs)
	}
	job := jobs[0]
	runAt := job["run_at"].(string)
	if job["target_url"] != "http://example.com/api/meetings/run" || canonical(job["payload"]) != `{}` || job["max_attempts"] != 3.0 ||
		job["idempotency_key"] != "crm-meetings-example.com-"+runAt[:16] {
		t.Errorf("booked job %v", job)
	}
	if d := bookedIn(t, job); d <= 0 || d > time.Minute {
		t.Errorf("turning on books the next minute, got %v", d)
	}
	if !strings.HasPrefix(raw, `{"settings":{"enabled":true,"history_days":90,"about":"CRM","calendar_owner":null,"phase":"importing","calendar_synced_at":null,"last_run_at":null,"last_error":null,"next_run_at":"`+runAt+`","updated_by":"ada@example.com","updated_at":"`) ||
		!strings.HasSuffix(raw, `"},`+noCounts+`}`) {
		t.Errorf("turn on answered %s", raw)
	}
	if s, _ := SyncSettings(f.ctx); deref(s.JobID) != "job-1" {
		t.Errorf("job id %v", s.JobID)
	}

	// The watchdog: a booked run that is not overdue is left alone; an overdue one is booked again.
	status, raw = f.do("GET", "/api/meetings/sync", "", asUser)
	if status != 200 || !strings.HasPrefix(raw, `{"sources":{"calendar":true,"notes":false},"settings":{"enabled":true,`) ||
		!strings.HasSuffix(raw, noCounts+`,"history_choices":[30,90,365],"can_configure":true}`) || len(f.jobs()) != 1 {
		t.Errorf("state: %d %s, jobs %d", status, raw, len(f.jobs()))
	}
	mustRun(t, f.ctx, "UPDATE meeting_sync SET next_run_at = '2026-01-01T00:00:00.000Z'")
	_, raw = f.do("GET", "/api/meetings/sync", "", nil)
	if len(f.jobs()) != 2 || !strings.Contains(raw, `"next_run_at":"2026-01-01T00:00:00.000Z"`) || !strings.Contains(raw, `"can_configure":false`) {
		t.Errorf("overdue: jobs %d, %s", len(f.jobs()), raw)
	}

	// Reaching further back starts the import over and books a run; a nearer reach does neither.
	mustRun(t, f.ctx, "UPDATE meeting_sync SET phase = 'live'")
	f.do("PUT", "/api/meetings/sync", `{"history_days":30}`, asUser)
	if s, _ := SyncSettings(f.ctx); s.Phase != "live" || len(f.jobs()) != 2 {
		t.Errorf("nearer reach: phase %s, jobs %d", s.Phase, len(f.jobs()))
	}
	f.do("PUT", "/api/meetings/sync", `{"history_days":365}`, map[string]string{"X-Clawnify-Caller": "user", "X-Clawnify-User-Id": "user-7"})
	if s, _ := SyncSettings(f.ctx); s.Phase != "importing" || len(f.jobs()) != 3 || deref(s.UpdatedBy) != "user-7" {
		t.Errorf("further reach: phase %s, jobs %d, updated_by %v", s.Phase, len(f.jobs()), s.UpdatedBy)
	}

	// Turning off cancels the booked run.
	status, raw = f.do("PUT", "/api/meetings/sync", `{"enabled":false}`, asUser)
	if status != 200 || !strings.Contains(raw, `"enabled":false`) || !strings.Contains(raw, `"next_run_at":null`) ||
		len(f.queue.cancelled) != 1 || f.queue.cancelled[0] != "job-3" {
		t.Errorf("turn off: %d %s, cancelled %v", status, raw, f.queue.cancelled)
	}
	if s, _ := SyncSettings(f.ctx); s.JobID != nil {
		t.Errorf("job id kept after turning off: %v", *s.JobID)
	}

	f.platform.connected = nil
	f.want("PUT", "/api/meetings/sync", `{"enabled":true}`, asUser, 409, `{"error":"Connect Google Calendar in Clawnify first."}`)
	f.want("PUT", "/api/meetings/sync", `{"enabled":false}`, map[string]string{"X-Clawnify-Caller": "agent", "X-Clawnify-User-Id": "u"}, 403,
		`{"error":"Only a signed-in person can change meeting sync settings."}`)
}

func TestMeetingsRunRoutesBookTheNextRun(t *testing.T) {
	f := newMeetingsFixture(t, []string{"googlecalendar"}, meetingsRules{Execute: []fakeRule{
		{Action: "GOOGLECALENDAR_EVENTS_LIST", Once: true, Data: map[string]any{"items": []any{}}},
		{Action: "GOOGLECALENDAR_EVENTS_LIST", Once: true, Data: map[string]any{"items": []any{}}},
		{Action: "GOOGLECALENDAR_EVENTS_LIST", Error: strp("Calendar quota")},
	}})
	mustRun(t, f.ctx, "INSERT INTO meeting_sync (id, enabled, phase) VALUES (1, 1, 'idle')")

	// No signature and no signed-in caller: refused before anything runs.
	f.want("POST", "/api/meetings/run", "{}", nil, 403, `{"error":"Sign in to run a sync."}`)
	f.want("POST", "/api/meetings/run", "{}", signedHeaders(t, `{"other":1}`), 403, `{"error":"Sign in to run a sync."}`)
	if len(f.platform.calls) != 0 || len(f.jobs()) != 0 {
		t.Fatalf("a refused delivery ran: %v %v", f.platform.calls, f.jobs())
	}

	// A signed delivery runs with no caller header; a finished pass books the next read in 15 minutes.
	status, raw := f.do("POST", "/api/meetings/run", "{}", signedHeaders(t, "{}"))
	if status != 200 || !strings.HasPrefix(raw, `{"status":"ran","job":"sync","phase":"live","stored":0,"notes":0,"more":false,"settings":{"enabled":true,`) ||
		!strings.HasSuffix(raw, noCounts+`}`) || len(f.jobs()) != 1 {
		t.Fatalf("signed run: %d %s, jobs %d", status, raw, len(f.jobs()))
	}
	if d := bookedIn(t, f.jobs()[0]); d < 14*time.Minute || d > 16*time.Minute {
		t.Errorf("next read in 15 minutes, got %v", d)
	}

	// "Sync now" reads even though a read was just done.
	status, raw = f.do("POST", "/api/meetings/sync-now", "", map[string]string{"X-Clawnify-Caller": "agent"})
	if status != 200 || !strings.HasPrefix(raw, `{"status":"ran","job":"sync",`) || len(f.jobs()) != 2 {
		t.Errorf("sync now: %d %s", status, raw)
	}
	// A failed read retries in 10 minutes, and says why.
	status, raw = f.do("POST", "/api/meetings/sync-now", "", asUser)
	if status != 200 || !strings.HasPrefix(raw, `{"status":"error","phase":"live","error":"Calendar quota","settings":{`) || !strings.Contains(raw, `"last_error":"Calendar quota"`) {
		t.Errorf("failed sync now: %d %s", status, raw)
	}
	if jobs := f.jobs(); len(jobs) != 3 || bookedIn(t, jobs[2]) < 9*time.Minute || bookedIn(t, jobs[2]) > 11*time.Minute {
		t.Errorf("a failed run retries in 10 minutes: %v", jobs)
	}

	// Busy and off book nothing.
	mustRun(t, f.ctx, "UPDATE meeting_sync SET running_until = '9999-01-01T00:00:00.000Z'")
	f.do("POST", "/api/meetings/sync-now", "", asUser)
	mustRun(t, f.ctx, "UPDATE meeting_sync SET enabled = 0")
	status, raw = f.do("POST", "/api/meetings/run", "", map[string]string{"X-Clawnify-Caller": "api"})
	if status != 200 || !strings.HasPrefix(raw, `{"status":"off","settings":{"enabled":false,`) || len(f.jobs()) != 3 {
		t.Errorf("off: %d %s, jobs %d", status, raw, len(f.jobs()))
	}
}

func TestMeetingsLinkListAndDigestRoutes(t *testing.T) {
	f := newMeetingsFixture(t, nil, meetingsRules{})
	mustRun(t, f.ctx,
		"INSERT INTO companies (id, name, domain) VALUES ('co-x', 'X & Co', ''), ('co-y', 'Y', 'y.example')",
		`INSERT INTO meetings (id, note_id, title, starts_at, ends_at, attendees, link_status, note_url) VALUES
		  ('m1', 'n1', 'Call with Ann', '2026-10-13T10:00:00.000Z', NULL, '[{"email":"ann@x.example","name":"Ann"},{"email":"me@gmail.com","name":null}]', 'unmatched', 'https://notes/n1'),
		  ('m2', NULL, 'Bo', '2099-01-01T10:00:00.000Z', '2099-01-01T11:00:00.000Z', '[{"email":"bo@x.example","name":"Bo"}]', 'unmatched', NULL),
		  ('m3', NULL, '', '2026-10-13T11:00:00.000Z', NULL, '[]', 'internal', NULL)`)

	m1 := `{"id":"m1","title":"Call with Ann","starts_at":"2026-10-13T10:00:00.000Z","ends_at":null,"attendees":[{"email":"ann@x.example","name":"Ann"},{"email":"me@gmail.com","name":null}],"company_id":"co-x","company_name":"X & Co","company_domain":"x.example","link_status":"manual","calendar_url":null,"note_url":"https://notes/n1","summary":null,"sentiment":null,"sentiment_reason":null,"digest_status":"queued","digest_error":null,"has_note":true}`
	f.want("PATCH", "/api/meetings/m1", `{"company_id":"co-x"}`, asUser, 200, `{"meeting":`+m1+`}`)
	if !web.Drain(5 * time.Second) {
		t.Fatal("background booking did not finish")
	}
	if len(f.jobs()) != 1 {
		t.Errorf("a queued digest books a run: %v", f.jobs())
	}
	// The company took the people's domain, so Bo's meeting linked itself.
	m2 := `{"id":"m2","title":"Bo","starts_at":"2099-01-01T10:00:00.000Z","ends_at":"2099-01-01T11:00:00.000Z","attendees":[{"email":"bo@x.example","name":"Bo"}],"company_id":"co-x","company_name":"X & Co","company_domain":"x.example","link_status":"auto","calendar_url":null,"note_url":null,"summary":null,"sentiment":null,"sentiment_reason":null,"digest_status":"none","digest_error":null,"has_note":false}`
	f.want("GET", "/api/meetings/m2", "", nil, 200, `{"meeting":`+m2+`}`)
	f.want("GET", "/api/meetings?company_id=co-x", "", nil, 200, `{"meetings":[`+m2+`,`+m1+`],"total":2}`)
	f.want("GET", "/api/meetings?company_id=co-x&when=upcoming&limit=1", "", nil, 200, `{"meetings":[`+m2+`],"total":1}`)
	f.want("GET", "/api/meetings?when=past&offset=1", "", nil, 200, `{"meetings":[],"total":1}`)
	f.want("GET", "/api/meetings/m3", "", nil, 404, `{"error":"Meeting not found"}`)

	// Not an account meeting: nothing is queued, so nothing is booked.
	status, raw := f.do("PATCH", "/api/meetings/m2", `{"ignored":true,"company_id":"co-y"}`, map[string]string{"X-Clawnify-Caller": "agent"})
	if status != 200 || !strings.Contains(raw, `"company_id":null,"company_name":null,"company_domain":null,"link_status":"ignored"`) {
		t.Errorf("ignore: %d %s", status, raw)
	}
	web.Drain(5 * time.Second)
	if len(f.jobs()) != 1 {
		t.Errorf("ignoring booked a run: %v", f.jobs())
	}
	f.want("PATCH", "/api/meetings/m3", `{"ignored":true}`, asUser, 404, `{"error":"Meeting not found"}`)

	// A failed digest is read again on request.
	f.want("POST", "/api/meetings/m1/digest", "", asUser, 409, `{"error":"This meeting has no Granola note, no company, or was already read."}`)
	mustRun(t, f.ctx, "UPDATE meetings SET digest_status = 'error', digest_error = 'Rate limited' WHERE id = 'm1'")
	f.want("POST", "/api/meetings/m1/digest", "", map[string]string{"X-Clawnify-Caller": "api"}, 200, `{"ok":true}`)
	web.Drain(5 * time.Second)
	if len(f.jobs()) != 2 {
		t.Errorf("a retry books a run: %v", f.jobs())
	}
	if s, _ := stringify(f.row("SELECT digest_status, digest_error FROM meetings WHERE id = 'm1'")); s != `{"digest_error":null,"digest_status":"queued"}` {
		t.Errorf("retried meeting %s", s)
	}
}

func (f *meetingsFixture) row(sql string) map[string]any {
	f.t.Helper()
	rows, err := db.Query(f.ctx, sql)
	if err != nil || len(rows) == 0 {
		f.t.Fatalf("%s: %v", sql, err)
	}
	return rows[0]
}
