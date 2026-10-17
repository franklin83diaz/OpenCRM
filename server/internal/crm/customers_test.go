package crm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
)

func mustExec(t *testing.T, ctx context.Context, sql string, params ...any) {
	t.Helper()
	if _, err := db.Run(ctx, sql, params...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// Review Focus 5: a call stored as SQLite's "YYYY-MM-DD HH:MM:SS" is UTC, as
// on Workers, whatever this machine's zone. 46 days and 2 hours before noon
// UTC is 46 days quiet: red. Read as New York time it would be 45 days and 22
// hours, 45 days: yellow.
func TestCustomersQuietCallInSQLiteFormatIsUTC(t *testing.T) {
	ctx := dbtest.Setup(t)
	local := time.Local
	time.Local = time.FixedZone("EDT", -4*3600)
	t.Cleanup(func() { time.Local = local })

	now := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	day := now.AddDate(0, 0, -46).Format("2006-01-02")
	mustExec(t, ctx, "INSERT INTO meeting_sync (id, enabled, phase) VALUES (1, 1, 'live')")
	mustExec(t, ctx, "INSERT INTO companies (id, name, customer_since) VALUES ('co-q', 'Quiet', '2025-01-01')")
	mustExec(t, ctx, "INSERT INTO meetings (id, title, starts_at, company_id, link_status) VALUES ('m1', 'Long ago', ?, 'co-q', 'manual')", day+" 10:00:00")

	out, err := CustomersOverview(ctx, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Customers) != 1 {
		t.Fatalf("customers = %+v", out.Customers)
	}
	c := out.Customers[0]
	if c.Status != "red" || strings.Join(c.Reasons, "|") != "No contact in 46 days (email isn't synced)" || c.DaysQuiet == nil || *c.DaysQuiet != 46 {
		t.Errorf("got %s %q days_quiet %v; want red, the quiet reason, 46", c.Status, c.Reasons, c.DaysQuiet)
	}
	if *c.LastTouchAt != day+" 10:00:00" || out.Counts.Red != 1 {
		t.Errorf("last_touch_at %v, counts %+v", *c.LastTouchAt, out.Counts)
	}
	if len(out.Focus) != 1 || out.Focus[0].Text != "Book a call: no contact in 46 days (email isn't synced)" {
		t.Errorf("focus = %+v", out.Focus)
	}
}

func TestCustomersInChunks(t *testing.T) {
	rec := recordedDB(t, nil)
	ctx := context.Background()
	ids := make([]string, 181)
	for i := range ids {
		ids[i] = "id"
	}
	rows, err := InChunks(ctx, ids, func(m string) string { return "SELECT 1 AS one WHERE ? IN (" + m + ")" }, "id")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || len(rec.log) != 3 {
		t.Fatalf("rows %d, statements %d; want 3 and 3", len(rows), len(rec.log))
	}
	for i, n := range []int{90, 90, 1} {
		if got := len(rec.log[i].Params); got != n+1 {
			t.Errorf("chunk %d binds %d values, want %d", i, got, n+1)
		}
	}
	if none, _ := InChunks(ctx, nil, func(string) string { return "SELECT nothing" }); len(none) != 0 || len(rec.log) != 3 {
		t.Error("no ids should read nothing")
	}
}

// A database from before the CRM dropped its integrations still has the
// syncs' settings rows switched on, and open insights from digested calls.
// Nothing syncs or digests any more, so applying schema.sql (pnpm db:migrate,
// which an upgrade runs) switches them off: the customers read "off" rather
// than "seen", and no old risk keeps an account red. Running it again changes
// nothing. Historical calls and their moods stay.
func TestSchemaRetiresTheOldSyncs(t *testing.T) {
	ctx := dbtest.Setup(t)
	mustExec(t, ctx, "INSERT INTO email_accounts (mailbox, enabled, phase) VALUES ('me@crm.example', 1, 'live')")
	mustExec(t, ctx, "INSERT INTO meeting_sync (id, enabled, phase) VALUES (1, 1, 'live')")
	mustExec(t, ctx, "INSERT INTO companies (id, name, customer_since) VALUES ('co-a', 'Acme', '2025-01-01')")
	mustExec(t, ctx, `INSERT INTO insights (id, company_id, kind, text, status) VALUES
    ('i-open', 'co-a', 'risk', 'They might churn', 'open'), ('i-done', 'co-a', 'idea', 'An idea', 'done')`)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	before, err := CustomersOverview(ctx, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if before.Sight.Calls != "seen" || before.Sight.Emails != "seen" || before.Customers[0].Status != "red" {
		t.Fatalf("before the upgrade: sight %+v, status %s", before.Sight, before.Customers[0].Status)
	}

	schema, err := os.ReadFile("../../schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		mustRun(t, ctx, string(schema))
		after, err := CustomersOverview(ctx, now, 0)
		if err != nil {
			t.Fatal(err)
		}
		c := after.Customers[0]
		if after.Sight.Calls != "off" || after.Sight.Emails != "off" || c.Status != "unknown" || c.Risks != 0 ||
			strings.Join(c.Reasons, "|") != "Calls aren't synced, email isn't synced" {
			t.Errorf("run %d: sight %+v, status %s, reasons %q, risks %d", run, after.Sight, c.Status, c.Reasons, c.Risks)
		}
		rows, err := db.Query(ctx, "SELECT id, status FROM insights ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		if got := canonical(rows); got != `[{"id":"i-done","status":"done"},{"id":"i-open","status":"dismissed"}]` {
			t.Errorf("run %d: insights %s", run, got)
		}
	}
}

// Expected days from node: new Date(ms + tz * 60_000).toISOString().slice(0, 10).
func TestCustomersLocalDay(t *testing.T) {
	at := func(s string) float64 { return msOf(mustTime(t, s)) }
	cases := []struct {
		ms, tz float64
		want   string
	}{
		{at("2026-10-17T23:30:00Z"), 0, "2026-10-17"},
		{at("2026-10-17T23:30:00Z"), 30, "2026-10-18"},
		{at("2026-10-17T23:30:00Z"), 29.99, "2026-10-17"},
		{at("2026-10-17T02:00:00Z"), -120.5, "2026-10-16"},
		{at("2026-10-17T00:00:00Z"), -0.00001, "2026-10-16"}, // 0.6 ms before midnight is 23:59:59.999
		{at("2026-10-17T00:00:00Z"), 0.00001, "2026-10-17"},
	}
	for _, c := range cases {
		if got, err := localDay(c.ms, c.tz); err != nil || got != c.want {
			t.Errorf("localDay(%v, %v) = %q, %v; want %q", c.ms, c.tz, got, err, c.want)
		}
	}
	if _, err := localDay(parseMs("garbage"), 0); err == nil || err.Error() != "Invalid time value" {
		t.Errorf("an unparseable start: %v", err)
	}
}

func TestCustomersBackfills(t *testing.T) {
	rec := recordedDB(t, nil)
	ctx := context.Background()
	app := &App{}
	mustExec(t, ctx, `INSERT INTO companies (id, name, customer_since) VALUES ('co-a', 'A', NULL), ('co-b', 'B', '  '), ('co-c', 'C', '2020-02-02'), ('co-d', 'D', NULL)`)
	mustExec(t, ctx, `INSERT INTO contacts (id, first_name, company_id) VALUES ('ct-a', 'Ann', 'co-a'), ('ct-b', 'Bo', 'co-b')`)

	// An empty pipeline gets the defaults, once.
	if err := app.ensureStagesSeeded(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Query(ctx, "SELECT key, label, color, position, is_won, is_lost FROM stages ORDER BY position")
	if got := asJS(t, rows); got != `[{"color":"slate","is_lost":0,"is_won":0,"key":"prospect","label":"Prospect","position":0},{"color":"sky","is_lost":0,"is_won":0,"key":"qualified","label":"Qualified","position":1},{"color":"violet","is_lost":0,"is_won":0,"key":"proposal","label":"Proposal","position":2},{"color":"amber","is_lost":0,"is_won":0,"key":"negotiation","label":"Negotiation","position":3},{"color":"emerald","is_lost":0,"is_won":1,"key":"won","label":"Won","position":4},{"color":"rose","is_lost":1,"is_won":0,"key":"lost","label":"Lost","position":5}]` {
		t.Errorf("stages = %s", got)
	}
	mustExec(t, ctx, "DELETE FROM stages WHERE key <> 'won'")

	mustExec(t, ctx, `INSERT INTO deals (id, name, contact_id, company_id, stage, close_date, updated_at) VALUES
		('d1', 'Won by contact', 'ct-a', NULL, 'won', '2026-01-05', '2026-03-01 10:00:00'),
		('d2', 'Won later', 'ct-a', NULL, 'won', '2099-01-01', '2026-03-01 10:00:00'),
		('d3', 'Won, no date', 'ct-b', NULL, 'won', 'soon', '2025-07-07 10:00:00'),
		('d4', 'Won, own date', NULL, 'co-c', 'won', '2019-01-01', '2026-03-01 10:00:00'),
		('d5', 'Open', NULL, 'co-d', 'prospect', '2001-01-01', '2026-03-01 10:00:00')`)
	if err := app.backfillCustomers(ctx); err != nil {
		t.Fatal(err)
	}
	rows, _ = db.Query(ctx, "SELECT id, customer_since FROM companies ORDER BY id")
	if got := asJS(t, rows); got != `[{"customer_since":"2026-01-05","id":"co-a"},{"customer_since":"2025-07-07","id":"co-b"},{"customer_since":"2020-02-02","id":"co-c"},{"customer_since":null,"id":"co-d"}]` {
		t.Errorf("customer_since = %s", got)
	}
	rows, _ = db.Query(ctx, "SELECT id, company_id FROM deals ORDER BY id")
	if got := asJS(t, rows); got != `[{"company_id":"co-a","id":"d1"},{"company_id":"co-a","id":"d2"},{"company_id":"co-b","id":"d3"},{"company_id":"co-c","id":"d4"},{"company_id":"co-d","id":"d5"}]` {
		t.Errorf("deal companies = %s", got)
	}

	// The markers stop a later run (another process) from refilling what
	// someone cleared on purpose; the flags stop even the check here.
	mustExec(t, ctx, "UPDATE companies SET customer_since = NULL WHERE id = 'co-a'")
	mustExec(t, ctx, "UPDATE deals SET company_id = NULL WHERE id = 'd1'")
	rec.log = nil
	if err := app.backfillCustomers(ctx); err != nil {
		t.Fatal(err)
	}
	if err := app.ensureStagesSeeded(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rec.log) != 0 {
		t.Errorf("a warm App checked again: %v", rec.log)
	}
	fresh := &App{}
	if err := fresh.backfillCustomers(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fresh.ensureStagesSeeded(ctx); err != nil {
		t.Fatal(err)
	}
	if got := asJS(t, rec.log); got != `[{"params":[],"sql":"SELECT key FROM data_backfills WHERE key = 'companies.customer_since'"},{"params":[],"sql":"SELECT COUNT(*) as count FROM stages"}]` {
		t.Errorf("a fresh App ran %s", got)
	}
	row, _ := db.Get(ctx, "SELECT (SELECT customer_since FROM companies WHERE id = 'co-a') AS since, (SELECT company_id FROM deals WHERE id = 'd1') AS company, (SELECT COUNT(*) FROM stages) AS stages")
	if row["since"] != nil || row["company"] != nil || row["stages"] != float64(1) {
		t.Errorf("after a second run: %v", row)
	}
}
