package crm

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"

	"opencrm/internal/clawnify"
	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
)

// Expected values in this file come from running the same inputs through the
// JS built-ins (node 22) or reading email-sync.ts; the scenario replay in
// email_sync_ts_test.go compares whole runs with the TS.

func TestEmailRunSyncStoresFetchedEmail(t *testing.T) {
	ctx := dbtest.Setup(t)
	mustRun(t, ctx,
		"INSERT INTO contacts (id, first_name, last_name, email) VALUES ('c-ada', 'Ada', 'Lovelace', ' Ada@Acme.example ')",
		"INSERT INTO email_accounts (mailbox, enabled, visibility, auto_create) VALUES ('me@mine.example', 1, 'subject', 'none')")
	broker := &scriptedBroker{connected: []string{"gmail"}, rules: []tsRule{
		{Action: "GMAIL_GET_PROFILE", Data: map[string]any{"emailAddress": "me@mine.example"}},
		{Action: "GMAIL_FETCH_EMAILS", Data: map[string]any{"messages": []any{map[string]any{
			"messageId": "m1", "threadId": "t1", "messageTimestamp": "2026-10-01T09:30:00Z",
			"sender": "Ada Lovelace <ada@acme.example>", "to": "Me <me@mine.example>", "subject": "Hello",
			"labelIds": []any{"INBOX"}, "preview": map[string]any{"body": "never stored"},
		}}}},
	}}
	srv := httptest.NewServer(broker)
	defer srv.Close()
	conn := clawnify.NewConnections(clawnify.Env{Token: "tok", APIURL: srv.URL}, nil)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	res, err := RunSync(ctx, conn, "me@mine.example", now)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := stringify(res); got != `{"status":"ran","phase":"live","step":"live","created":0,"stored":1,"forgotten":0,"more":false}` {
		t.Errorf("result %s", got)
	}
	row, _ := db.Get(ctx, "SELECT * FROM email_messages")
	want := db.Row{
		"mailbox": "me@mine.example", "id": "m1", "thread_id": "t1", "sent_at": "2026-10-01T09:30:00Z",
		"direction": "received", "from_email": "ada@acme.example", "from_name": "Ada Lovelace",
		"to_emails": `["me@mine.example"]`, "subject": "Hello",
	}
	if !reflect.DeepEqual(row, want) {
		t.Errorf("email_messages row\n got  %v\n want %v", row, want)
	}
	link, _ := db.Get(ctx, "SELECT * FROM email_message_contacts")
	if !reflect.DeepEqual(link, db.Row{"mailbox": "me@mine.example", "message_id": "m1", "contact_id": "c-ada", "sent_at": "2026-10-01T09:30:00Z"}) {
		t.Errorf("link %v", link)
	}
	c, _ := db.Get(ctx, "SELECT last_contacted_at FROM contacts WHERE id = 'c-ada'")
	if c["last_contacted_at"] != "2026-10-01T09:30:00Z" {
		t.Errorf("last_contacted_at %v", c["last_contacted_at"])
	}
	a, _ := AccountFor(ctx, "me@mine.example")
	if a.Phase != "live" || *a.ImportCursor != `{"step":"live","check":"done"}` || *a.SyncedUntil != "2026-10-14T12:00:00.000Z" ||
		a.RunningUntil != nil || a.LastError != nil || *a.LastRunAt != "2026-10-14T12:00:00.000Z" {
		t.Errorf("account %+v", a)
	}
	fetch := broker.calls[1].(map[string]any)["args"]
	if got := canonical(fetch); got != `{"include_payload":false,"max_results":100,"query":"{from:ada@acme.example to:ada@acme.example} -in:chats after:2025/10/14","verbose":false}` {
		t.Errorf("FETCH_EMAILS args %s", got)
	}
}

func mustRun(t *testing.T, ctx context.Context, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := db.Run(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func TestEmailJSHelpers(t *testing.T) {
	names := []string{"banana", "Apple", "cherry", "apple", "Älpha", "alpha", "Zeta", "zeta", "a-b", "ab", "a b", "a_b", "10", "9", "x1", "X",
		"Ünder", "under", "Clients 2024", "clients", "[Imap]/Sent", "Work/Projects", "work", "Ñandu", "nandu", "émile", "Emile", "Ébène"}
	sort.SliceStable(names, func(i, j int) bool { return localeCompare(names[i], names[j]) < 0 })
	wantOrder := []string{"[Imap]/Sent", "10", "9", "a b", "a_b", "a-b", "ab", "alpha", "Älpha", "apple", "Apple", "banana", "cherry", "clients",
		"Clients 2024", "Ébène", "Emile", "émile", "nandu", "Ñandu", "under", "Ünder", "work", "Work/Projects", "X", "x1", "zeta", "Zeta"}
	if !reflect.DeepEqual(names, wantOrder) {
		t.Errorf("localeCompare order\n got  %q\n want %q", names, wantOrder)
	}

	for in, want := range map[string]string{
		"me@mine.example": "me%40mine.example", "a b+c/d?e=f&g": "a%20b%2Bc%2Fd%3Fe%3Df%26g", "Zoë": "Zo%C3%AB",
		"!~*()'-_.": "!~*()'-_.", "😀": "%F0%9F%98%80", "%": "%25",
	} {
		if got := encodeURIComponent(in); got != want {
			t.Errorf("encodeURIComponent(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"ada%40x.com": "ada@x.com", "%E2%82%AC": "€", "%e2%82%ac": "€", "%": "!", "%4": "!", "%zz": "!", "%E2%82": "!", "%C0%80": "!",
		"%ED%A0%80": "!", "%F4%90%80%80": "!", "a+b": "a+b", "%25": "%", "%F0%9F%98%80": "😀", "%E2%82%41": "!",
	} {
		got, err := decodeURIComponent(in)
		if err != nil {
			got = "!"
			if err.Error() != "URI malformed" {
				t.Errorf("decodeURIComponent(%q) error %q", in, err)
			}
		}
		if got != want {
			t.Errorf("decodeURIComponent(%q) = %q, want %q", in, got, want)
		}
	}

	for in, want := range map[string]string{
		"https://crm.example": "crm.example", "http://localhost:8790": "localhost:8790", "https://CRM.example:443": "crm.example",
		"http://x.example:80": "x.example", "http://x.example:443": "x.example:443",
	} {
		if got, ok := urlHost(in); !ok || got != want {
			t.Errorf("urlHost(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestEmailParseCursor(t *testing.T) {
	for in, want := range map[string]string{
		`{"step":"people","page":null,"after_contact":"c1","batch":[{"id":"c2","email":"x@y"}],"started_at":"s","check":"done"}`: `{"step":"people","after_contact":"c1","batch":[{"id":"c2","email":"x@y"}],"started_at":"s","check":"done"}`,
		`{"step":5}`: "nil",
		`[]`:         "nil",
		`null`:       "nil",
		``:           "nil",
		`{`:          "nil",
	} {
		c := parseCursor(&in)
		got := "nil"
		if c != nil {
			b, _ := json.Marshal(c)
			got = string(b)
		}
		if got != want {
			t.Errorf("parseCursor(%s) = %s, want %s", in, got, want)
		}
	}
	if parseCursor(nil) != nil {
		t.Error("parseCursor(nil) should be nil")
	}
}

func TestEmailCountsAndContactEmails(t *testing.T) {
	ctx := dbtest.Setup(t)
	mustRun(t, ctx,
		"INSERT INTO contacts (id, first_name, email) VALUES ('c1', 'Ada', 'ada@acme.example')",
		"INSERT INTO email_accounts (mailbox, enabled, visibility) VALUES ('me+crm@mine.example', 1, 'metadata'), ('off@mine.example', 0, 'everything')",
		`INSERT INTO email_messages (mailbox, id, thread_id, sent_at, direction, from_email, from_name, to_emails, subject) VALUES
		 ('me+crm@mine.example', 'm1', 't1', '2026-10-01T00:00:00Z', 'received', 'ada@acme.example', NULL, '["me+crm@mine.example", 5]', 'Hi'),
		 ('me+crm@mine.example', 'm2', 't2', '2026-10-02T00:00:00Z', 'sent', 'me+crm@mine.example', 'Me', 'not json', NULL),
		 ('off@mine.example', 'm3', 't3', '2026-10-03T00:00:00Z', 'sent', 'off@mine.example', NULL, '[]', 'Hidden')`,
		`INSERT INTO email_message_contacts (mailbox, message_id, contact_id, sent_at) VALUES
		 ('me+crm@mine.example', 'm1', 'c1', '2026-10-01T00:00:00Z'), ('me+crm@mine.example', 'm2', 'c1', '2026-10-02T00:00:00Z'),
		 ('off@mine.example', 'm3', 'c1', '2026-10-03T00:00:00Z')`)
	list, err := ContactEmails(ctx, "c1", 1)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := stringify(list)
	want := `{"total":2,"emails":[{"id":"m2","mailbox":"me+crm@mine.example","thread_id":"t2","sent_at":"2026-10-02T00:00:00Z","direction":"sent","from_email":"me+crm@mine.example","from_name":"Me","to_emails":[],"subject":null,"can_open":false,"gmail_url":"https://mail.google.com/mail/?authuser=me%2Bcrm%40mine.example#all/t2"}]}`
	if got != want {
		t.Errorf("ContactEmails\n got  %s\n want %s", got, want)
	}
	counts, err := EmailCounts(ctx, "me+crm@mine.example")
	if err != nil || counts != (MailboxCounts{Emails: 2, Contacts: 1}) {
		t.Errorf("counts %+v %v", counts, err)
	}
}
