package crm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"opencrm/internal/db"
)

// A deal's recurring value, its owner and its partners' shares, and the
// columns they need on a database made before them. They came after the
// port: these are the Go server's own expectations.

type valuedDealJSON struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Value          float64 `json:"value"`
	RecurringValue float64 `json:"recurring_value"`
	OwnerID        *string `json:"owner_id"`
	Owner          *struct {
		ID, Name, Email string
	} `json:"owner"`
	Partners []DealPartner `json:"partners"`
}

func TestDealValuesAndOwner(t *testing.T) {
	recordedDB(t, []string{
		`INSERT INTO users (id, email, name, role, password_hash) VALUES ('u-ana', 'ana@example.com', 'Ana', 'admin', 'x'), ('u-bo', 'bo@example.com', '', 'user', 'x')`,
		`INSERT INTO users (id, email, name, role, password_hash, disabled) VALUES ('u-gone', 'gone@example.com', 'Gone', 'user', 'x', 1)`,
	})
	api := replayAPI(&App{})

	var team teamBody
	partnersCall(t, api, "GET", "/api/team", "", 200, &team)
	if len(team.Team) != 2 || team.Team[0].Name != "Ana" || team.Team[1].Email != "bo@example.com" {
		t.Fatalf("team: %+v", team)
	}

	// Once and each month; assigned, or nobody's.
	var made struct{ Deal valuedDealJSON }
	partnersCall(t, api, "POST", "/api/deals", `{"name":"Hosting","value":1200,"recurring_value":"99.5","owner_id":"u-ana"}`, 201, &made)
	if d := made.Deal; d.Value != 1200 || d.RecurringValue != 99.5 || d.Owner == nil || d.Owner.Name != "Ana" {
		t.Fatalf("created: %+v", d)
	}
	partnersCall(t, api, "POST", "/api/deals", `{"name":"Free one","value":10}`, 201, &made)
	if d := made.Deal; d.OwnerID != nil || d.Owner != nil || d.RecurringValue != 0 {
		t.Fatalf("a deal nobody was given: %+v", d)
	}
	free := made.Deal.ID
	partnersCall(t, api, "POST", "/api/deals", `{"name":"Lost one","stage":"lost","recurring_value":1000}`, 201, nil)

	// Only an active account takes a deal.
	partnersCall(t, api, "POST", "/api/deals", `{"name":"X","owner_id":"u-gone"}`, 400, nil)
	partnersCall(t, api, "PUT", "/api/deals/"+free, `{"owner_id":"nope"}`, 400, nil)
	partnersCall(t, api, "PUT", "/api/deals/"+free, `{"owner_id":5}`, 400, nil)

	var one struct{ Deal valuedDealJSON }
	partnersCall(t, api, "PUT", "/api/deals/"+free, `{"owner_id":"u-bo","recurring_value":20}`, 200, &one)
	if d := one.Deal; d.Owner == nil || d.Owner.Email != "bo@example.com" || d.RecurringValue != 20 {
		t.Fatalf("assigned: %+v", d)
	}

	// The free ones, the board's "Owner is empty".
	var page struct{ Deals []valuedDealJSON }
	board := func(rules string) string {
		partnersCall(t, api, "GET", "/api/deals/board?filters="+url.QueryEscape(rules), "", 200, &page)
		names := []string{}
		for _, d := range page.Deals {
			names = append(names, d.Name)
		}
		return strings.Join(names, ",")
	}
	if got := board(`[{"field":"owner_id","op":"is_empty"}]`); got != "Lost one" {
		t.Fatalf("free deals: %q", got)
	}
	if got := board(`[{"field":"owner_id","op":"is","value":["u-bo"]}]`); got != "Free one" {
		t.Fatalf("Bo's deals: %q", got)
	}
	partnersCall(t, api, "PUT", "/api/deals/"+free, `{"owner_id":null}`, 200, &one)
	if one.Deal.Owner != nil {
		t.Fatalf("unassigned: %+v", one.Deal)
	}

	// The pipeline each month leaves out the lost.
	var stats statsBody
	partnersCall(t, api, "GET", "/api/stats", "", 200, &stats)
	if stats.DealRecurringValue != 119.5 {
		t.Fatalf("recurring pipeline: %v", stats.DealRecurringValue)
	}
	partnersCall(t, api, "GET", "/api/deals?sort=recurring_value&order=desc", "", 200, &page)
	if page.Deals[0].Name != "Lost one" {
		t.Fatalf("by recurring value: %+v", page.Deals)
	}

	// A deleted account leaves its deals free.
	mustRun(t, context.Background(), `DELETE FROM users WHERE id = 'u-ana'`)
	partnersCall(t, api, "GET", "/api/deals/"+made.Deal.ID, "", 200, &one)
	partnersCall(t, api, "GET", "/api/deals?search=Hosting", "", 200, &page)
	if d := page.Deals[0]; d.OwnerID != nil || d.Owner != nil {
		t.Fatalf("owner after their account went: %+v", d)
	}
}

func TestDealPartnerShares(t *testing.T) {
	recordedDB(t, []string{
		`INSERT INTO partners (id, name) VALUES ('pa-n', 'Nimbus'), ('pa-a', 'Acme')`,
		`INSERT INTO deals (id, name, stage, updated_at) VALUES ('d-1', 'Hosting', 'prospect', '2020-01-01 00:00:00')`,
	})
	api := replayAPI(&App{})
	share := func(d valuedDealJSON, id string) *DealPartner {
		for i := range d.Partners {
			if d.Partners[i].ID == id {
				return &d.Partners[i]
			}
		}
		return nil
	}

	// A share for a partner not yet on the deal puts it on.
	var one struct{ Deal valuedDealJSON }
	partnersCall(t, api, "PUT", "/api/deals/d-1/partners/pa-n", `{"one_time_value":500,"recurring_value":"25"}`, 200, &one)
	if s := share(one.Deal, "pa-n"); s == nil || s.OneTimeValue != 500 || s.RecurringValue != 25 {
		t.Fatalf("share: %+v", one.Deal.Partners)
	}
	// Only what is sent changes.
	partnersCall(t, api, "PUT", "/api/deals/d-1/partners/pa-n", `{"recurring_value":30}`, 200, &one)
	if s := share(one.Deal, "pa-n"); s.OneTimeValue != 500 || s.RecurringValue != 30 {
		t.Fatalf("one amount: %+v", s)
	}
	partnersCall(t, api, "PUT", "/api/deals/d-1/partners/pa-a", `{}`, 200, &one)
	if s := share(one.Deal, "pa-a"); s == nil || s.OneTimeValue != 0 {
		t.Fatalf("no amounts: %+v", one.Deal.Partners)
	}
	partnersCall(t, api, "PUT", "/api/deals/d-1/partners/pa-n", `{"recurring_value":[1]}`, 400, nil)
	partnersCall(t, api, "PUT", "/api/deals/nope/partners/pa-n", `{}`, 404, nil)
	partnersCall(t, api, "PUT", "/api/deals/d-1/partners/nope", `{}`, 404, nil)

	// partner_ids keeps the shares of the partners it keeps.
	partnersCall(t, api, "PUT", "/api/deals/d-1", `{"partner_ids":["pa-n"]}`, 200, &one)
	if s := share(one.Deal, "pa-n"); len(one.Deal.Partners) != 1 || s.OneTimeValue != 500 || s.RecurringValue != 30 {
		t.Fatalf("kept share: %+v", one.Deal.Partners)
	}

	partnersCall(t, api, "DELETE", "/api/deals/d-1/partners/pa-n", "", 200, &one)
	if len(one.Deal.Partners) != 0 {
		t.Fatalf("taken off: %+v", one.Deal.Partners)
	}
	partnersCall(t, api, "DELETE", "/api/deals/d-1/partners/pa-n", "", 200, nil)
	partnersCall(t, api, "DELETE", "/api/deals/nope/partners/pa-n", "", 404, nil)
}

// A database from before them gets the columns on the first request, once;
// the deals it holds read and write as before, with the new fields.
func TestColumnsAddedToAnOlderDatabase(t *testing.T) {
	rec := recordedDB(t, nil)
	ctx := context.Background()
	mustRun(t, ctx,
		`DROP INDEX IF EXISTS idx_deals_owner`,
		`ALTER TABLE deals DROP COLUMN owner_id`,
		`ALTER TABLE deals DROP COLUMN recurring_value`,
		`DROP TABLE deal_partners`,
		`CREATE TABLE deal_partners (deal_id TEXT NOT NULL REFERENCES deals(id) ON DELETE CASCADE, partner_id TEXT NOT NULL REFERENCES partners(id) ON DELETE CASCADE, created_at TEXT DEFAULT (datetime('now')), PRIMARY KEY (deal_id, partner_id))`,
		`ALTER TABLE partners DROP COLUMN industry`,
		`ALTER TABLE partners DROP COLUMN status`,
		`INSERT INTO partners (id, name) VALUES ('pa-old', 'Old partner')`,
		`INSERT INTO deals (id, name, value) VALUES ('d-old', 'Old deal', 300)`,
	)
	app := &App{}
	h := app.withColumns(replayAPI(app))
	get := func() int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/deals/d-old", nil))
		return w.Code
	}
	if code := get(); code != http.StatusOK {
		t.Fatalf("first request: %d", code)
	}
	for table, want := range map[string][]string{"deals": {"recurring_value", "owner_id"}, "deal_partners": {"one_time_value", "recurring_value"}, "partners": {"industry", "status"}} {
		cols, err := TableColumns(ctx, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range want {
			if !cols[c] {
				t.Errorf("%s has no %s", table, c)
			}
		}
	}
	if idx, _ := db.Get(ctx, "SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_deals_owner'"); idx == nil {
		t.Error("no index on deals.owner_id")
	}
	if old, _ := db.Get(ctx, "SELECT status FROM partners WHERE id = 'pa-old'"); old == nil || old["status"] != "prospect" {
		t.Errorf("a partner from before statuses: %v", old)
	}
	// Once per process: the next request adds nothing.
	rec.mu.Lock()
	before := len(rec.log)
	rec.mu.Unlock()
	get()
	rec.mu.Lock()
	later := append([]tsStatement{}, rec.log[before:]...)
	rec.mu.Unlock()
	for _, st := range later {
		if strings.Contains(st.SQL, "ALTER") || strings.Contains(st.SQL, "PRAGMA") {
			t.Errorf("a second request ran %s", st.SQL)
		}
	}
	// And on a database that has them, it runs again without failing.
	if err := addColumns(ctx); err != nil {
		t.Fatal(err)
	}
	// A company's main contact can't be dropped to begin with (a foreign key):
	// its declaration is tried on a table of its own.
	mustRun(t, ctx, `CREATE TABLE older_companies (id TEXT PRIMARY KEY)`)
	for _, c := range addedColumns {
		if c.table == "companies" {
			mustRun(t, ctx, "ALTER TABLE older_companies ADD COLUMN "+c.column+" "+c.decl)
		}
	}
}
