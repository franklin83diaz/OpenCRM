package crm

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opencrm/internal/clawnify"
	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
	"opencrm/internal/js"
)

// The parity run has no Clawnify token, so nothing is connected there; these
// tests cover the connected paths with a scripted broker, a fake queue and a
// fake JWKS. Expected values follow index.ts 2074–2480 and email-sync.ts.

// fakeQueue is the platform queue: it books jobs and drops them.
type fakeQueue struct {
	mu        sync.Mutex
	enqueued  []map[string]any
	cancelled []string
}

func (q *fakeQueue) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q.mu.Lock()
	defer q.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "POST" && r.URL.Path == "/enqueue":
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		q.enqueued = append(q.enqueued, body)
		fmt.Fprintf(w, `{"id":"job-%d"}`, len(q.enqueued))
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/jobs/"):
		q.cancelled = append(q.cancelled, strings.TrimPrefix(r.URL.Path, "/jobs/"))
		io.WriteString(w, "{}")
	default:
		w.WriteHeader(404)
	}
}

type emailFixture struct {
	t      *testing.T
	ctx    context.Context
	broker *scriptedBroker
	queue  *fakeQueue
	api    http.Handler
}

func newEmailFixture(t *testing.T, connected []string, rules ...tsRule) *emailFixture {
	ctx := dbtest.Setup(t)
	f := &emailFixture{t: t, ctx: ctx, broker: &scriptedBroker{connected: connected, rules: rules}, queue: &fakeQueue{}}
	b := httptest.NewServer(f.broker)
	q := httptest.NewServer(f.queue)
	t.Cleanup(b.Close)
	t.Cleanup(q.Close)
	env := clawnify.Env{Token: "tok", APIURL: b.URL, QueueURL: q.URL}
	f.api = New(&App{Env: env, Conn: clawnify.NewConnections(env, nil)})
	return f
}

// do sends a request to the API and returns the status and raw body.
func (f *emailFixture) do(method, path, body string, headers map[string]string) (int, string) {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.api.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func (f *emailFixture) want(method, path, body string, headers map[string]string, status int, raw string) {
	f.t.Helper()
	gotStatus, gotRaw := f.do(method, path, body, headers)
	if gotStatus != status || gotRaw != raw {
		f.t.Errorf("%s %s %s\n got  %d %s\n want %d %s", method, path, body, gotStatus, gotRaw, status, raw)
	}
}

// lastCall is the broker's most recent call as canonical JSON.
func (f *emailFixture) lastCall() string {
	f.broker.mu.Lock()
	defer f.broker.mu.Unlock()
	if len(f.broker.calls) == 0 {
		return ""
	}
	return canonical(f.broker.calls[len(f.broker.calls)-1])
}

func (f *emailFixture) row(sql string) db.Row {
	f.t.Helper()
	r, err := db.Get(f.ctx, sql)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

var asUser = map[string]string{"X-Clawnify-Caller": "user", "X-Clawnify-User-Id": "user-1", "X-Clawnify-User-Email": "ada@example.com"}

func profileRule(address string) tsRule {
	return tsRule{Action: "GMAIL_GET_PROFILE", Data: map[string]any{"emailAddress": address}}
}

func TestEmailIntegrationsStatusConnected(t *testing.T) {
	f := newEmailFixture(t, []string{"googlesuper", "granola", "notion"})
	f.want("GET", "/api/integrations/status", "", nil, 200, `{"email":true,"meeting":true,"slack":false,"notes":true,"mailbox":null}`)
	f.broker.connected = []string{"gmail", "slack"}
	mustRun(t, f.ctx, "INSERT INTO email_accounts (mailbox, enabled) VALUES ('me@mine.example', 0)")
	f.want("GET", "/api/integrations/status", "", nil, 200, `{"email":true,"meeting":false,"slack":true,"notes":false,"mailbox":"me@mine.example"}`)
}

func TestEmailSendConnected(t *testing.T) {
	f := newEmailFixture(t, []string{"gmail"},
		tsRule{Action: "GMAIL_SEND_EMAIL", Args: map[string]any{"subject": "Fail"}, Error: strp("Quota exceeded")},
		tsRule{Action: "GMAIL_SEND_EMAIL", Data: map[string]any{"id": "sent-1"}},
		tsRule{Action: "GMAIL_REPLY_TO_THREAD", Data: map[string]any{}},
		tsRule{Action: "GMAIL_FORWARD_MESSAGE", Data: map[string]any{}})
	mustRun(t, f.ctx,
		"INSERT INTO contacts (id, first_name, email) VALUES ('c-ada', 'Ada', ' Ada@Acme.example '), ('c-bob', 'Bob', 'bob@beta.example'), ('c-none', 'No', NULL)",
		"INSERT INTO email_accounts (mailbox, enabled, visibility) VALUES ('me@mine.example', 1, 'subject')",
		`INSERT INTO email_messages (mailbox, id, thread_id, sent_at, direction, from_email, to_emails, subject)
		 VALUES ('me@mine.example', 'm1', 't1', '2026-10-01T00:00:00Z', 'received', 'ada@acme.example', '["me@mine.example"]', 'RE:  Plans')`,
		"INSERT INTO email_message_contacts (mailbox, message_id, contact_id, sent_at) VALUES ('me@mine.example', 'm1', 'c-ada', '2026-10-01T00:00:00Z')")
	activities := func() string {
		rows, _ := db.Query(f.ctx, "SELECT entity_id, type, body, meta FROM activities ORDER BY rowid")
		s, _ := stringify(rows)
		db.Run(f.ctx, "DELETE FROM activities")
		return s
	}

	f.want("POST", "/api/integrations/email", `{"contact_id":"c-ada","to":["x@y.example"],"cc":["BOB@beta.example"],"subject":" Hi ","body":"Hello"}`, nil, 200, `{"ok":true}`)
	if got := f.lastCall(); got != `{"action":"GMAIL_SEND_EMAIL","args":{"body":"Hello","cc":["bob@beta.example"],"extra_recipients":["x@y.example"],"is_html":false,"recipient_email":"ada@acme.example","subject":"Hi"},"service":"gmail"}` {
		t.Errorf("send call %s", got)
	}
	if got := activities(); got != `[{"body":"Hi","entity_id":"c-ada","meta":"{\"to\":[\"ada@acme.example\",\"x@y.example\"],\"cc\":[\"bob@beta.example\"]}","type":"email"},{"body":"Hi","entity_id":"c-bob","meta":"{\"to\":[\"ada@acme.example\",\"x@y.example\"],\"cc\":[\"bob@beta.example\"]}","type":"email"}]` {
		t.Errorf("activities %s", got)
	}
	f.want("POST", "/api/integrations/email", `{"to":["x@y.example"],"body":"No subject"}`, nil, 200, `{"ok":true}`)
	if got := activities(); got != `[]` {
		t.Errorf("an email to no contact logs nothing: %s", got)
	}
	f.want("POST", "/api/integrations/email", `{"contact_id":"c-none","subject":"x"}`, nil, 400, `{"error":"Contact has no email address"}`)
	f.want("POST", "/api/integrations/email", `{"to":["x@y.example"],"subject":"Fail"}`, nil, 500, `{"error":"Quota exceeded"}`)

	// A reply keeps the thread; the timeline says what it replied to.
	f.want("POST", "/api/integrations/email", `{"to":["ada@acme.example"],"reply_to":{"mailbox":" ME@mine.example ","id":" m1 "},"body":"Sounds good"}`, nil, 200, `{"ok":true}`)
	if got := f.lastCall(); got != `{"action":"GMAIL_REPLY_TO_THREAD","args":{"is_html":false,"message_body":"Sounds good","recipient_email":"ada@acme.example","thread_id":"t1"},"service":"gmail"}` {
		t.Errorf("reply call %s", got)
	}
	if got := activities(); got != `[{"body":"Re: Plans","entity_id":"c-ada","meta":"{\"to\":[\"ada@acme.example\"]}","type":"email"}]` {
		t.Errorf("reply activity %s", got)
	}
	f.want("POST", "/api/integrations/email", `{"to":["ada@acme.example"],"forward":{"mailbox":"me@mine.example","id":"m1"}}`, nil, 403, `{"error":"This mailbox shares metadata only; forward the email from Gmail"}`)
	mustRun(t, f.ctx, "UPDATE email_accounts SET visibility = 'everything'")
	f.want("POST", "/api/integrations/email", `{"to":["new@else.example"],"bcc":["bob@beta.example"],"forward":{"mailbox":"me@mine.example","id":"m1"},"body":"fyi"}`, nil, 200, `{"ok":true}`)
	if got := f.lastCall(); got != `{"action":"GMAIL_FORWARD_MESSAGE","args":{"additional_text":"fyi","bcc":["bob@beta.example"],"message_id":"m1","recipients":["new@else.example"]},"service":"gmail"}` {
		t.Errorf("forward call %s", got)
	}
	if got := activities(); got != `[{"body":"Fwd: RE:  Plans","entity_id":"c-bob","meta":"{\"to\":[\"new@else.example\"]}","type":"email"}]` {
		t.Errorf("forward activity %s", got)
	}
	mustRun(t, f.ctx, "UPDATE email_accounts SET visibility = 'metadata'")
	f.want("POST", "/api/integrations/email", `{"to":["ada@acme.example"],"reply_to":{"mailbox":"me@mine.example","id":"m1"},"body":"ok"}`, nil, 200, `{"ok":true}`)
	if got := activities(); !strings.Contains(got, `"body":"Reply"`) {
		t.Errorf("a reply at metadata names no subject: %s", got)
	}
	mustRun(t, f.ctx, "UPDATE email_accounts SET enabled = 0")
	f.want("POST", "/api/integrations/email", `{"to":["ada@acme.example"],"reply_to":{"mailbox":"me@mine.example","id":"m1"},"body":"ok"}`, nil, 404, `{"error":"Email not found"}`)
}

func TestEmailMeetingConnected(t *testing.T) {
	f := newEmailFixture(t, []string{"googlesuper"}, tsRule{Action: "GOOGLESUPER_CREATE_EVENT", Data: map[string]any{"id": "ev1"}})
	mustRun(t, f.ctx, "INSERT INTO contacts (id, first_name, email) VALUES ('c-ada', 'Ada', 'Ada@Acme.example'), ('c-none', 'No', NULL)")
	f.want("POST", "/api/integrations/meeting", `{"contact_id":"c-ada","summary":" Intro ","start_datetime":"2026-10-15T10:00:00","timezone":" Europe/Madrid ","duration_minutes":"90"}`, nil, 200, `{"ok":true}`)
	if got := f.lastCall(); got != `{"action":"GOOGLESUPER_CREATE_EVENT","args":{"attendees":["Ada@Acme.example"],"event_duration_hour":1,"event_duration_minutes":30,"start_datetime":"2026-10-15T10:00:00","summary":"Intro","timezone":"Europe/Madrid"},"service":"googlesuper"}` {
		t.Errorf("create call %s", got)
	}
	f.want("POST", "/api/integrations/meeting", `{"contact_id":"c-none","summary":"Call","start_datetime":"2026-10-15T10:00:00","duration_minutes":-45.5}`, nil, 200, `{"ok":true}`)
	if got := f.lastCall(); got != `{"action":"GOOGLESUPER_CREATE_EVENT","args":{"event_duration_hour":-1,"event_duration_minutes":-45.5,"start_datetime":"2026-10-15T10:00:00","summary":"Call","timezone":"UTC"},"service":"googlesuper"}` {
		t.Errorf("create call without attendees %s", got)
	}
	rows, _ := db.Query(f.ctx, "SELECT entity_id, type, body, meta FROM activities ORDER BY rowid")
	if got, _ := stringify(rows); got != `[{"body":"Intro","entity_id":"c-ada","meta":"{\"start\":\"2026-10-15T10:00:00\",\"timezone\":\"Europe/Madrid\"}","type":"meeting"},{"body":"Call","entity_id":"c-none","meta":"{\"start\":\"2026-10-15T10:00:00\",\"timezone\":\"UTC\"}","type":"meeting"}]` {
		t.Errorf("activities %s", got)
	}
}

func TestEmailSyncSettingsConnected(t *testing.T) {
	f := newEmailFixture(t, []string{"gmail"}, profileRule(" Me@Mine.example "))
	mustRun(t, f.ctx,
		"INSERT INTO contacts (id, first_name, email) VALUES ('c-ada', 'Ada', 'ada@acme.example')",
		"INSERT INTO email_accounts (mailbox, enabled, job_id) VALUES ('old@mine.example', 1, 'job-old')",
		`INSERT INTO email_messages (mailbox, id, thread_id, sent_at, direction, from_email, to_emails) VALUES ('old@mine.example', 'o1', 't', '2026-10-01T00:00:00Z', 'sent', 'old@mine.example', '[]')`,
		"INSERT INTO email_message_contacts (mailbox, message_id, contact_id, sent_at) VALUES ('old@mine.example', 'o1', 'c-ada', '2026-10-01T00:00:00Z')")
	put := func(body string, status int, raw string) {
		t.Helper()
		if raw != "" {
			f.want("PUT", "/api/email-sync", body, asUser, status, raw)
		} else if got, out := f.do("PUT", "/api/email-sync", body, asUser); got != status {
			t.Errorf("PUT %s: %d %s", body, got, out)
		}
	}

	// Bad settings are refused before anything is written, labels first.
	put(`{"labels":5,"enabled":"x"}`, 400, `{"error":"labels must be a list of Gmail label names"}`)
	put(`{"labels":["ok"," "]}`, 400, `{"error":"labels must be a list of Gmail label names"}`)
	put(`{"enabled":"yes"}`, 400, `{"error":"enabled must be true or false"}`)
	put(`{"history":"5y"}`, 400, `{"error":"history must be one of 3m, 12m, all"}`)
	put(`{"visibility":"all"}`, 400, `{"error":"visibility must be one of metadata, subject, everything"}`)
	put(`{"auto_create":true}`, 400, `{"error":"auto_create must be one of none, sent, sent_and_received"}`)
	put(`{"exclude_personal":1}`, 400, `{"error":"exclude_personal must be true or false"}`)
	put(`null`, 500, `{"error":"Cannot read properties of null (reading 'labels')"}`)
	// The connection signs in as me@, so old@'s sync went as soon as a write was attempted.
	if r := f.row("SELECT enabled, phase FROM email_accounts WHERE mailbox = 'old@mine.example'"); r["enabled"] != 0.0 || r["phase"] != "idle" {
		t.Errorf("old mailbox %v", r)
	}
	if r := f.row("SELECT COUNT(*) AS n FROM email_messages"); r["n"] != 0.0 {
		t.Errorf("old mailbox's mail kept: %v", r)
	}
	if fmt.Sprint(f.queue.cancelled) != "[job-old]" {
		t.Errorf("cancelled %v", f.queue.cancelled)
	}

	// Turning on: the import starts at "sent" and a run is booked.
	status, raw := f.do("PUT", "/api/email-sync", `{"enabled":true,"labels":[" Clients ","Clients","Big"],"blocklist":["Spam@X.example","beta.example",3],"visibility":"subject"}`, asUser)
	var out struct {
		Account map[string]any `json:"account"`
		Counts  MailboxCounts  `json:"counts"`
	}
	json.Unmarshal([]byte(raw), &out)
	if status != 200 || !strings.HasPrefix(raw, `{"account":{"mailbox":"me@mine.example","enabled":true,"labels":["Clients","Big"],"history":"12m","visibility":"subject","auto_create":"sent","exclude_group":true,"exclude_personal":true,"blocklist":["spam@x.example","@beta.example"],"phase":"importing","synced_until":null,"contacts_created":0,"last_run_at":null,"last_error":null,"next_run_at":"`) ||
		out.Account["updated_by"] != "ada@example.com" || !strings.HasSuffix(raw, `,"counts":{"emails":0,"contacts":0}}`) {
		t.Fatalf("turn on: %d %s", status, raw)
	}
	a, _ := AccountFor(f.ctx, "me@mine.example")
	if !strings.HasPrefix(*a.ImportCursor, `{"step":"sent","started_at":"`) || !strings.HasSuffix(*a.ImportCursor, `","check":"done"}`) || *a.JobID != "job-1" {
		t.Errorf("after turning on: cursor %s job %v", *a.ImportCursor, *a.JobID)
	}
	job := f.queue.enqueued[0]
	runAt, _ := js.ParseDate(job["run_at"].(string))
	if job["target_url"] != "http://example.com/api/email-sync/run" || canonical(job["payload"]) != `{"mailbox":"me@mine.example"}` ||
		job["max_attempts"] != 3.0 || job["idempotency_key"] != "crm-email-example.com-me@mine.example-"+job["run_at"].(string)[:16] ||
		*a.NextRunAt != job["run_at"] || time.Until(runAt) > time.Minute || runAt.Second() != 0 {
		t.Errorf("booked job %v (next_run_at %s)", job, *a.NextRunAt)
	}

	// Narrowing the labels forgets what was imported and starts again.
	mustRun(t, f.ctx,
		`INSERT INTO email_messages (mailbox, id, thread_id, sent_at, direction, from_email, to_emails, subject) VALUES ('me@mine.example', 'm1', 't', '2026-10-01T00:00:00Z', 'received', 'ada@acme.example', '[]', 'Hi')`,
		"INSERT INTO email_message_contacts (mailbox, message_id, contact_id, sent_at) VALUES ('me@mine.example', 'm1', 'c-ada', '2026-10-01T00:00:00Z')",
		`UPDATE email_accounts SET phase = 'live', import_cursor = '{"step":"live","check":"2026|x"}' WHERE mailbox = 'me@mine.example'`)
	put(`{"labels":["Clients"]}`, 200, "")
	if r := f.row("SELECT COUNT(*) AS n FROM email_messages"); r["n"] != 0.0 {
		t.Errorf("scope change kept mail: %v", r)
	}
	a, _ = AccountFor(f.ctx, "me@mine.example")
	if a.Phase != "importing" || !strings.HasSuffix(*a.ImportCursor, `"check":"done"}`) || a.Labels != `["Clients"]` {
		t.Errorf("after scope change %+v cursor %s", a, *a.ImportCursor)
	}

	// Showing less forgets subjects; showing more than metadata reads the people again.
	mustRun(t, f.ctx,
		`INSERT INTO email_messages (mailbox, id, thread_id, sent_at, direction, from_email, to_emails, subject) VALUES ('me@mine.example', 'm2', 't', '2026-10-01T00:00:00Z', 'received', 'ada@acme.example', '[]', 'Secret')`,
		`UPDATE email_accounts SET phase = 'live', import_cursor = '{"step":"live","check":"2026|x"}' WHERE mailbox = 'me@mine.example'`)
	put(`{"visibility":"metadata"}`, 200, "")
	if r := f.row("SELECT subject FROM email_messages WHERE id = 'm2'"); r["subject"] != nil {
		t.Errorf("subject kept at metadata: %v", r)
	}
	if a, _ = AccountFor(f.ctx, "me@mine.example"); a.Phase != "live" {
		t.Errorf("showing less restarted the import: %s", a.Phase)
	}
	put(`{"visibility":"everything"}`, 200, "")
	if a, _ = AccountFor(f.ctx, "me@mine.example"); a.Phase != "importing" || !strings.HasPrefix(*a.ImportCursor, `{"step":"people","started_at":"`) || !strings.HasSuffix(*a.ImportCursor, `"check":"2026|x"}`) {
		t.Errorf("showing more: %s %s", a.Phase, *a.ImportCursor)
	}

	// Turning off cancels the booked run and deletes what was synced.
	before := len(f.queue.cancelled)
	put(`{"enabled":false}`, 200, "")
	a, _ = AccountFor(f.ctx, "me@mine.example")
	if a.Enabled != 0 || a.Phase != "idle" || a.ImportCursor != nil || a.JobID != nil || a.NextRunAt != nil || len(f.queue.cancelled) != before+1 {
		t.Errorf("after turning off %+v, cancelled %v", a, f.queue.cancelled)
	}
	// A person without an email address is named by id.
	f.do("PUT", "/api/email-sync", `{}`, map[string]string{"X-Clawnify-Caller": "user", "X-Clawnify-User-Id": "user-9"})
	if a, _ = AccountFor(f.ctx, "me@mine.example"); *a.UpdatedBy != "user-9" {
		t.Errorf("updated_by %v", *a.UpdatedBy)
	}
}

func TestEmailSyncStateConnected(t *testing.T) {
	f := newEmailFixture(t, []string{"gmail"}, profileRule("Other@Mine.example"))
	f.want("GET", "/api/email-sync?check=1", "", asUser, 200, `{"connected":true,"mailbox":"other@mine.example","mailbox_changed":false,"account":null,"counts":{"emails":0,"contacts":0},"can_configure":true}`)
	mustRun(t, f.ctx, "INSERT INTO email_accounts (mailbox, enabled, updated_at) VALUES ('me@mine.example', 1, '2026-10-14 10:00:00')")
	status, raw := f.do("GET", "/api/email-sync?check=1", "", nil)
	if status != 200 || !strings.HasPrefix(raw, `{"connected":true,"mailbox":"other@mine.example","mailbox_changed":true,"account":{"mailbox":"me@mine.example","enabled":true,`) ||
		!strings.HasSuffix(raw, `,"counts":{"emails":0,"contacts":0},"can_configure":false}`) {
		t.Errorf("changed mailbox: %d %s", status, raw)
	}
	// The watchdog booked the missing run; once booked, a view books nothing.
	if len(f.queue.enqueued) != 1 {
		t.Fatalf("enqueued %v", f.queue.enqueued)
	}
	f.do("GET", "/api/email-sync", "", nil)
	if len(f.queue.enqueued) != 1 {
		t.Errorf("booked again: %v", f.queue.enqueued)
	}
	mustRun(t, f.ctx, "UPDATE email_accounts SET next_run_at = '2026-01-01T00:00:00.000Z'")
	f.do("GET", "/api/email-sync", "", nil)
	if len(f.queue.enqueued) != 2 {
		t.Errorf("an overdue booking was not renewed: %v", f.queue.enqueued)
	}
	// Sync off: the connected mailbox's own settings are shown.
	mustRun(t, f.ctx, "UPDATE email_accounts SET enabled = 0",
		"INSERT INTO email_accounts (mailbox, enabled, visibility, updated_at) VALUES ('other@mine.example', 0, 'everything', '2026-01-01 00:00:00')")
	_, raw = f.do("GET", "/api/email-sync?check=1", "", nil)
	if !strings.HasPrefix(raw, `{"connected":true,"mailbox":"other@mine.example","mailbox_changed":false,"account":{"mailbox":"other@mine.example","enabled":false,`) {
		t.Errorf("sync off: %s", raw)
	}
	_, raw = f.do("GET", "/api/email-sync", "", nil)
	if !strings.HasPrefix(raw, `{"connected":true,"mailbox":"me@mine.example","mailbox_changed":false,"account":{"mailbox":"me@mine.example","enabled":false,`) {
		t.Errorf("sync off without check: %s", raw)
	}
}

func TestEmailLabelsConnected(t *testing.T) {
	f := newEmailFixture(t, []string{"gmail"}, tsRule{Action: "GMAIL_LIST_LABELS", Data: map[string]any{"labels": []any{
		map[string]any{"id": "INBOX", "name": "INBOX", "type": "system"},
		map[string]any{"id": "L1", "name": "banana", "type": "user"},
		map[string]any{"id": "L2", "name": "Apple"},
		map[string]any{"id": "L3", "name": "cherry"},
		map[string]any{"id": "", "name": "no id"},
		map[string]any{"id": "L4"},
		map[string]any{"id": "L5", "name": "apple"},
	}}})
	f.want("GET", "/api/email-sync/labels", "", asUser, 200, `{"labels":[{"id":"L5","name":"apple"},{"id":"L2","name":"Apple"},{"id":"L1","name":"banana"},{"id":"L3","name":"cherry"}]}`)
	if got := f.lastCall(); got != `{"action":"GMAIL_LIST_LABELS","args":{"user_id":"me"},"service":"gmail"}` {
		t.Errorf("call %s", got)
	}
}

func TestEmailOpenConnected(t *testing.T) {
	f := newEmailFixture(t, []string{"gmail"},
		tsRule{Action: "GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID", Args: map[string]any{"message_id": "m1"}, Data: map[string]any{
			"messageText": "  Hi Ada  ",
			"payload": map[string]any{"headers": []any{
				map[string]any{"name": "Subject", "value": "x"},
				map[string]any{"name": "To", "value": "Ada <ADA@acme.example>, bob@beta.example"},
				map[string]any{"name": "CC", "value": `"Carol C" <carol@c.example>`},
			}},
		}},
		tsRule{Action: "GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID", Args: map[string]any{"message_id": "m2"}, Data: map[string]any{"messageText": "", "preview": map[string]any{"body": " preview "}}},
		tsRule{Action: "GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID", Error: strp("Gmail said no")})
	mustRun(t, f.ctx,
		"INSERT INTO contacts (id, first_name, email) VALUES ('c-ada', 'Ada', 'ada@acme.example')",
		"INSERT INTO email_accounts (mailbox, enabled, visibility) VALUES ('me@mine.example', 1, 'everything')",
		`INSERT INTO email_messages (mailbox, id, thread_id, sent_at, direction, from_email, from_name, to_emails, subject) VALUES
		 ('me@mine.example', 'm1', 't1', '2026-10-01T00:00:00Z', 'received', 'ada@acme.example', 'Ada', '["me@mine.example"]', 'Hello'),
		 ('me@mine.example', 'm2', 't2', '2026-10-02T00:00:00Z', 'sent', '', NULL, '["ada@acme.example", 7]', NULL),
		 ('me@mine.example', 'm3', 't3', '2026-10-03T00:00:00Z', 'sent', 'me@mine.example', NULL, '[]', NULL),
		 ('me@mine.example', 'm4', 't4', '2026-10-03T00:00:00Z', 'sent', 'me@mine.example', NULL, '[]', NULL)`,
		`INSERT INTO email_message_contacts (mailbox, message_id, contact_id, sent_at) VALUES
		 ('me@mine.example', 'm1', 'c-ada', '2026-10-01T00:00:00Z'), ('me@mine.example', 'm2', 'c-ada', '2026-10-02T00:00:00Z'),
		 ('me@mine.example', 'm4', 'c-ada', '2026-10-03T00:00:00Z')`)
	f.want("GET", "/api/emails/ME%40Mine.example/m1", "", nil, 200, `{"text":"Hi Ada","subject":"Hello","sent_at":"2026-10-01T00:00:00Z","direction":"received","thread_id":"t1","from":{"email":"ada@acme.example","name":"Ada"},"to":[{"email":"ada@acme.example","name":"Ada"},{"email":"bob@beta.example","name":null}],"cc":[{"email":"carol@c.example","name":"Carol C"}]}`)
	f.want("GET", "/api/emails/me@mine.example/m2", "", nil, 200, `{"text":"preview","subject":"","sent_at":"2026-10-02T00:00:00Z","direction":"sent","thread_id":"t2","from":null,"to":[{"email":"ada@acme.example","name":null}],"cc":[]}`)
	f.want("GET", "/api/emails/me@mine.example/m3", "", nil, 404, `{"error":"Email not found"}`)
	f.want("GET", "/api/emails/me@mine.example/m4", "", nil, 500, `{"error":"Gmail said no"}`)
	f.broker.connected = []string{"slack"}
	f.want("GET", "/api/emails/me@mine.example/m1", "", nil, 409, `{"error":"Gmail is not connected"}`)
	mustRun(t, f.ctx, "UPDATE email_accounts SET visibility = 'subject'")
	f.want("GET", "/api/emails/me@mine.example/m1", "", nil, 403, `{"error":"This mailbox shares metadata only; open the email in Gmail"}`)
}

func TestEmailContactEmailsConnected(t *testing.T) {
	f := newEmailFixture(t, []string{"gmail"}, profileRule("me@mine.example"),
		tsRule{Action: "GMAIL_FETCH_EMAILS", Once: true, Data: map[string]any{"messages": []any{map[string]any{
			"messageId": "m1", "threadId": "t1", "messageTimestamp": "2026-10-01T09:30:00Z",
			"sender": "Ada <ada@acme.example>", "to": "me@mine.example", "subject": "Hello", "labelIds": []any{"INBOX"},
		}}}},
		tsRule{Action: "GMAIL_FETCH_EMAILS", Error: strp("Gmail is down")})
	mustRun(t, f.ctx,
		"INSERT INTO contacts (id, first_name, email) VALUES ('c-ada', 'Ada', 'ada@acme.example'), ('c-bob', 'Bob', 'bob@beta.example')",
		"INSERT INTO email_accounts (mailbox, enabled, phase, visibility) VALUES ('me@mine.example', 1, 'live', 'metadata')")
	f.want("GET", "/api/contacts/c-ada/emails", "", nil, 200, `{"total":1,"emails":[{"id":"m1","mailbox":"me@mine.example","thread_id":"t1","sent_at":"2026-10-01T09:30:00Z","direction":"received","from_email":"ada@acme.example","from_name":"Ada","to_emails":["me@mine.example"],"subject":null,"can_open":false,"gmail_url":"https://mail.google.com/mail/?authuser=me%40mine.example#all/t1"}],"sync_on":true}`)
	// Gmail unreachable: what is synced still shows.
	f.want("GET", "/api/contacts/c-bob/emails", "", nil, 200, `{"total":0,"emails":[],"sync_on":true}`)
	if r := f.row("SELECT COUNT(*) AS n FROM email_contact_imports WHERE contact_id = 'c-bob'"); r["n"] != 0.0 {
		t.Errorf("a failed import was recorded: %v", r)
	}
}

// deliveryKey signs queue deliveries in tests; the fake JWKS publishes it.
var deliveryKey = sync.OnceValue(func() *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return k
})

// jwksTransport answers the queue's JWKS URL with deliveryKey; everything
// else goes out as usual.
type jwksTransport struct{}

func (jwksTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "services.clawnify.com" || r.URL.Path != "/.well-known/jwks.json" {
		return http.DefaultTransport.RoundTrip(r)
	}
	k := deliveryKey()
	body, _ := json.Marshal(map[string]any{"keys": []any{map[string]string{
		"kty": "EC", "crv": "P-256", "kid": "test-key",
		"x": base64.RawURLEncoding.EncodeToString(k.X.FillBytes(make([]byte, 32))),
		"y": base64.RawURLEncoding.EncodeToString(k.Y.FillBytes(make([]byte, 32))),
	}}})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
}

// signedHeaders are a queue delivery's headers for body, signed now.
func signedHeaders(t *testing.T, body string) map[string]string {
	t.Helper()
	old := http.DefaultClient.Transport
	http.DefaultClient.Transport = jwksTransport{}
	t.Cleanup(func() { http.DefaultClient.Transport = old })
	ts := time.Now().Unix()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d.%s", ts, body)))
	r, s, err := ecdsa.Sign(rand.Reader, deliveryKey(), sum[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := base64.StdEncoding.EncodeToString(append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...))
	return map[string]string{"X-Queue-Signature": sig, "X-Queue-Timestamp": fmt.Sprint(ts), "X-Queue-Key-Id": "test-key"}
}

func TestEmailRunRouteSignedDelivery(t *testing.T) {
	f := newEmailFixture(t, []string{"gmail"}, profileRule("me@mine.example"), tsRule{Action: "GMAIL_FETCH_EMAILS", Data: map[string]any{}})
	mustRun(t, f.ctx, "INSERT INTO email_accounts (mailbox, enabled, auto_create) VALUES ('me@mine.example', 1, 'none')")
	body := `{"mailbox":"me@mine.example"}`

	// No signature and no signed-in caller: refused before anything runs.
	f.want("POST", "/api/email-sync/run", body, nil, 403, `{"error":"Sign in to run a sync."}`)
	f.want("POST", "/api/email-sync/run", body, map[string]string{"X-Clawnify-Caller": "public"}, 403, `{"error":"Sign in to run a sync."}`)
	forged := signedHeaders(t, `{"mailbox":"someone@else.example"}`)
	f.want("POST", "/api/email-sync/run", body, forged, 403, `{"error":"Sign in to run a sync."}`)
	if len(f.broker.calls) != 0 || len(f.queue.enqueued) != 0 {
		t.Fatalf("a refused delivery ran: calls %v, jobs %v", f.broker.calls, f.queue.enqueued)
	}

	// A signed delivery runs with no caller header, and books the next run.
	status, raw := f.do("POST", "/api/email-sync/run", body, signedHeaders(t, body))
	if status != 200 || !strings.HasPrefix(raw, `{"status":"ran","phase":"live","step":"live","created":0,"stored":0,"forgotten":0,"more":false,"account":{"mailbox":"me@mine.example","enabled":true,`) ||
		!strings.HasSuffix(raw, `"counts":{"emails":0,"contacts":0}}`) {
		t.Fatalf("signed run: %d %s", status, raw)
	}
	if len(f.queue.enqueued) != 1 {
		t.Fatalf("jobs %v", f.queue.enqueued)
	}
	runAt, _ := js.ParseDate(f.queue.enqueued[0]["run_at"].(string))
	if d := time.Until(runAt); d < 14*time.Minute || d > 16*time.Minute {
		t.Errorf("a finished import books the next read in 15 minutes, got %v", d)
	}
}

func TestEmailSyncNowBooksNextRun(t *testing.T) {
	f := newEmailFixture(t, []string{"gmail"}, profileRule("someone@else.example"))
	mustRun(t, f.ctx, "INSERT INTO email_accounts (mailbox, enabled, auto_create) VALUES ('me@mine.example', 1, 'none')")
	f.want("POST", "/api/email-sync/sync-now", "", map[string]string{"X-Clawnify-Caller": "public"}, 403, `{"error":"Sign in to run a sync."}`)
	status, raw := f.do("POST", "/api/email-sync/sync-now", "", asUser)
	if status != 200 || !strings.HasPrefix(raw, `{"status":"error","phase":"importing","step":"people","created":0,"stored":0,"error":"The connected Google account is now someone@else.example, not me@mine.example. Open Email settings to switch.","account":{`) {
		t.Fatalf("sync-now: %d %s", status, raw)
	}
	runAt, _ := js.ParseDate(f.queue.enqueued[0]["run_at"].(string))
	if d := time.Until(runAt); d < 9*time.Minute || d > 11*time.Minute {
		t.Errorf("a failed run retries in 10 minutes, got %v", d)
	}
	// A run that is still holding the mailbox: busy, and nothing more is booked.
	mustRun(t, f.ctx, "UPDATE email_accounts SET running_until = '9999-01-01T00:00:00.000Z'")
	f.do("POST", "/api/email-sync/sync-now", `{"mailbox":"me@mine.example"}`, map[string]string{"X-Clawnify-Caller": "agent"})
	if len(f.queue.enqueued) != 1 {
		t.Errorf("busy booked a run: %v", f.queue.enqueued)
	}
}
