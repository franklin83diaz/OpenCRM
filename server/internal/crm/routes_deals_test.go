package crm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
	"opencrm/internal/web"
)

// TestDealsZodBytes compares the 400 bodies of the deals area's zod schemas
// byte for byte with what @hono/zod-openapi answered: zod 3's safeParseAsync
// on the same schema and input, JSON.stringify'd (the parity runner compares
// parsed JSON, so it can't see key order). Covers the int check's own key
// order, ZodEnum's, "Required" for a missing enum, arrays, nested objects,
// and the union's issue landing last.
func TestDealsZodBytes(t *testing.T) {
	cases := []struct {
		schema zodCheck
		body   string
		want   string
	}{
		{createStageSchema, `{"label":"x","position":1.5,"is_won":1}`, `{"success":false,"error":{"issues":[{"code":"invalid_type","expected":"integer","received":"float","message":"Expected integer, received float","path":["position"]},{"code":"invalid_type","expected":"boolean","received":"number","path":["is_won"],"message":"Expected boolean, received number"}],"name":"ZodError"}}`},
		{createStageSchema, `{}`, `{"success":false,"error":{"issues":[{"code":"invalid_type","expected":"string","received":"undefined","path":["label"],"message":"Required"}],"name":"ZodError"}}`},
		{createDealSchema, `{"name":5,"value":true,"stage":null}`, `{"success":false,"error":{"issues":[{"code":"invalid_type","expected":"string","received":"number","path":["name"],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"null","path":["stage"],"message":"Expected string, received null"},{"code":"invalid_union","unionErrors":[{"issues":[{"code":"invalid_type","expected":"number","received":"boolean","path":["value"],"message":"Expected number, received boolean"}],"name":"ZodError"},{"issues":[{"code":"invalid_type","expected":"string","received":"boolean","path":["value"],"message":"Expected string, received boolean"}],"name":"ZodError"}],"path":["value"],"message":"Invalid input"}],"name":"ZodError"}}`},
		{dealUpdateSchema, `{"kind":5,"summary":"","done_task_ids":[1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1,1],"next_step":{"title":"","owed_by":"x"}}`, `{"success":false,"error":{"issues":[{"expected":"'call' | 'message' | 'email' | 'meeting' | 'note'","received":"number","code":"invalid_type","path":["kind"],"message":"Expected 'call' | 'message' | 'email' | 'meeting' | 'note', received number"},{"code":"too_small","minimum":1,"type":"string","inclusive":true,"exact":false,"message":"String must contain at least 1 character(s)","path":["summary"]},{"code":"too_big","maximum":20,"type":"array","inclusive":true,"exact":false,"message":"Array must contain at most 20 element(s)","path":["done_task_ids"]},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",0],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",1],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",2],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",3],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",4],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",5],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",6],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",7],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",8],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",9],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",10],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",11],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",12],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",13],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",14],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",15],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",16],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",17],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",18],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",19],"message":"Expected string, received number"},{"code":"invalid_type","expected":"string","received":"number","path":["done_task_ids",20],"message":"Expected string, received number"},{"code":"too_small","minimum":1,"type":"string","inclusive":true,"exact":false,"message":"String must contain at least 1 character(s)","path":["next_step","title"]},{"code":"invalid_type","expected":"string","received":"undefined","path":["next_step","due_date"],"message":"Required"},{"received":"x","code":"invalid_enum_value","options":["us","them"],"path":["next_step","owed_by"],"message":"Invalid enum value. Expected 'us' | 'them', received 'x'"}],"name":"ZodError"}}`},
		{dealUpdateSchema, `{}`, `{"success":false,"error":{"issues":[{"expected":"'call' | 'message' | 'email' | 'meeting' | 'note'","received":"undefined","code":"invalid_type","path":["kind"],"message":"Required"},{"code":"invalid_type","expected":"string","received":"undefined","path":["summary"],"message":"Required"}],"name":"ZodError"}}`},
		{createTaskSchema, `{"title":"","owed_by":"me","due_date":1}`, `{"success":false,"error":{"issues":[{"code":"too_small","minimum":1,"type":"string","inclusive":true,"exact":false,"message":"String must contain at least 1 character(s)","path":["title"]},{"received":"me","code":"invalid_enum_value","options":["us","them"],"path":["owed_by"],"message":"Invalid enum value. Expected 'us' | 'them', received 'me'"},{"code":"invalid_type","expected":"string","received":"number","path":["due_date"],"message":"Expected string, received number"}],"name":"ZodError"}}`},
		{listTasksSchema, `{"status":"x","owed_by":["a","b"]}`, `{"success":false,"error":{"issues":[{"received":"x","code":"invalid_enum_value","options":["open","done","all"],"path":["status"],"message":"Invalid enum value. Expected 'open' | 'done' | 'all', received 'x'"},{"expected":"'us' | 'them'","received":"array","code":"invalid_type","path":["owed_by"],"message":"Expected 'us' | 'them', received array"}],"name":"ZodError"}}`},
	}
	for _, c := range cases {
		var v any
		if err := json.Unmarshal([]byte(c.body), &v); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		web.ZodError(w, zodIssues(c.schema, v))
		if got := w.Body.String(); got != c.want {
			t.Errorf("%s\n got  %s\n want %s", c.body, got, c.want)
		}
	}
}

func TestWonDay(t *testing.T) {
	now := time.Date(2026, 10, 14, 23, 59, 0, 0, time.UTC)
	cases := []struct {
		close any
		want  string
	}{
		{"2020-01-01", "2020-01-01"},
		{" 2020-01-01 ", "2020-01-01"}, // trimmed
		{"2026-10-14", "2026-10-14"},   // today is not before today
		{"2026-10-15", "2026-10-14"},   // a planned close is never later than today
		{"2026-02-30", "2026-10-14"},   // no such day
		{"", "2026-10-14"},
		{nil, "2026-10-14"},
		{20200101.0, "2026-10-14"}, // only a string is a close date
	}
	for _, c := range cases {
		if got := wonDay(c.close, now); got != c.want {
			t.Errorf("wonDay(%v) = %q, want %q", c.close, got, c.want)
		}
	}
}

func TestJSPrefix(t *testing.T) {
	x := strings.Repeat("x", 299)
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 300, "short"},
		{"héllo", 2, "hé"},
		// A pair cut in half keeps its high surrogate as D1 stored it: WTF-8.
		{x + "😀😀", 300, x + "\xed\xa0\xbd"},
		{x + "x😀", 300, x + "x"},
		{"😀😀", 2, "😀"},
		{"😀😀", 3, "😀\xed\xa0\xbd"},
		{"", 3, ""},
	}
	for _, c := range cases {
		if got := jsPrefix(c.in, c.n); got != c.want {
			t.Errorf("jsPrefix(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestSQLTime(t *testing.T) {
	for ms, want := range map[float64]string{
		1791972000123: "2026-10-14 10:00:00",
		// node: new Date(ms).toISOString().replace("T", " ").slice(0, 19)
		-62198755200000:   "-000001-01-01 00:00",
		-8640000000000000: "-271821-04-20 00:00",
		-62152839569600:   "0000-06-15 10:20:30",
		0:                 "1970-01-01 00:00:00",
		-59011545600000:   "0099-12-31 00:00:00", // node: new Date("0099-12-31T00:00:00Z").getTime()
	} {
		if got := sqlTime(ms); got != want {
			t.Errorf("sqlTime(%v) = %q, want %q", ms, got, want)
		}
	}
}

func TestDealValue(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{nil, 0}, {7.0, 7}, {-0.0, 0}, {"1,200.50", 1}, {" 12.5e1x", 125}, {"abc", 0}, {"-0", 0}, {"", 0},
	}
	for _, c := range cases {
		if got := dealValue(c.in); got != c.want {
			t.Errorf("dealValue(%#v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestWonDealTellsNoOne: a deal moved into a won stage is logged and makes
// its company a customer, and that is all — TS also posted the win to Slack,
// and the CRM has no integrations.
func TestWonDealTellsNoOne(t *testing.T) {
	ctx := dbtest.Setup(t)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	api := replayAPI(&App{Now: func() time.Time { return now }})
	for _, sql := range []string{
		"INSERT INTO companies (id, name) VALUES ('co-acme', 'Acme')",
		"INSERT INTO deals (id, name, company_id, value, stage, close_date) VALUES ('d-1', 'Acme', 'co-acme', 1200, 'prospect', '2099-01-01')",
	} {
		if _, err := db.Run(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest("PUT", "/api/deals/d-1", strings.NewReader(`{"stage":" won "}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	rows, err := db.Query(ctx, "SELECT type, body, meta FROM activities WHERE entity_id = 'd-1'")
	if err != nil {
		t.Fatal(err)
	}
	if got := canonical(rows); got != `[{"body":"Deal won — Won","meta":"{\"stage\":\" won \",\"value\":1200}","type":"stage_change"}]` {
		t.Errorf("activities %s", got)
	}
	co, err := db.Get(ctx, "SELECT customer_since FROM companies WHERE id = 'co-acme'")
	if err != nil {
		t.Fatal(err)
	}
	if co["customer_since"] != "2026-10-14" {
		t.Errorf("customer_since %v, want 2026-10-14 (today: the close date is planned)", co["customer_since"])
	}
}
