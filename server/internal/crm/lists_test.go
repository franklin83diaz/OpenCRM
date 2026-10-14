package crm

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
)

// Expected values below come from running the TS functions (index.ts at
// 0068887, lines 63–545) on the same inputs under node.

func TestChunkSizes(t *testing.T) {
	ids := make([]string, 150)
	var sizes []int
	for _, part := range Chunk(ids, 90) {
		sizes = append(sizes, len(part))
	}
	if !reflect.DeepEqual(sizes, []int{90, 60}) {
		t.Errorf("sizes = %v, want [90 60]", sizes)
	}
	if n := len(Chunk([]int{}, 90)); n != 0 {
		t.Errorf("empty input gave %d chunks", n)
	}
	if got := Chunk([]int{1, 2, 3}, 3); !reflect.DeepEqual(got, [][]int{{1, 2, 3}}) {
		t.Errorf("exact fit = %v", got)
	}
}

// filterNow, filterCols and filterMany are the inputs testdata/filters_ts.json
// was produced with.
var (
	filterNow  = time.Date(2026, 10, 14, 23, 30, 0, 0, time.UTC)
	filterCols = map[string]bool{"name": true, "domain": true, "value": true, "close_date": true, "created_at": true, "tier": true}
	filterMany = map[string]ManyLink{
		"contacts": {Table: "contacts", FK: "company_id"},
		"partners": {Table: "contacts", FK: "partner_company_id"},
	}
)

func TestFilterBriefExample(t *testing.T) {
	// An object at the top is not a list of rules: nothing is filtered.
	clauses, params, err := BuildFilters(filterCols, `{"op":"and","rules":[{"field":"name","op":"contains","value":"Acme"}]}`, "c.", 0, nil, filterNow)
	if err != nil || len(clauses) != 0 || len(params) != 0 {
		t.Errorf("object: %q %v %v", clauses, params, err)
	}
	// In a list it is a group, ANDed ("op" is not "logic").
	clauses, params, err = BuildFilters(filterCols, `[{"op":"and","rules":[{"field":"name","op":"contains","value":"Acme"}]}]`, "c.", 0, nil, filterNow)
	if err != nil || !reflect.DeepEqual(clauses, []string{`(c."name" LIKE ?)`}) || !reflect.DeepEqual(params, []any{"%Acme%"}) {
		t.Errorf("group: %q %v %v", clauses, params, err)
	}
}

func TestFilterBuildMatchesTS(t *testing.T) {
	raw, err := os.ReadFile("testdata/filters_ts.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Raw, Prefix, Error string
		Tz                       int
		Clauses                  []string
		Params                   []any
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 60 {
		t.Fatalf("only %d cases", len(cases))
	}
	for _, c := range cases {
		clauses, params, err := BuildFilters(filterCols, c.Raw, c.Prefix, c.Tz, filterMany, filterNow)
		if c.Error != "" {
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Msg != c.Error {
				t.Errorf("%s: err = %v, want FieldError %q", c.Name, err, c.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if c.Clauses == nil {
			c.Clauses = []string{}
		}
		if c.Params == nil {
			c.Params = []any{}
		}
		if !reflect.DeepEqual(clauses, c.Clauses) || !reflect.DeepEqual(params, c.Params) {
			t.Errorf("%s\n got %q %#v\nwant %q %#v", c.Name, clauses, params, c.Clauses, c.Params)
		}
	}
}

func TestFilterTzOffsetOf(t *testing.T) {
	cases := map[string]int{"": 0, "120": 120, "-300": -300, "840": 840, "841": 0, "-840": -840, "-841": 0,
		"1.5": 0, "120.0": 120, "1e2": 100, "0x10": 16, " 60 ": 60, "abc": 0, "-0": 0, "+60": 60,
		"Infinity": 0, "0b11": 3, "\t-60\n": -60}
	for in, want := range cases {
		if got := TzOffsetOf(in); got != want {
			t.Errorf("TzOffsetOf(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestRangeRelative(t *testing.T) {
	cases := []struct {
		now        string
		tz         int
		value      string
		start, end string
		ok         bool
	}{
		{"2026-10-14T16:10:00Z", 0, "PAST_7_DAY", "2026-10-07", "2026-10-14", true},
		{"2026-10-14T16:10:00Z", 0, "PAST_1_DAY", "2026-10-13", "2026-10-14", true},
		{"2026-10-14T16:10:00Z", 0, "NEXT_1_DAY", "2026-10-14", "2026-10-15", true},
		{"2026-10-14T16:10:00Z", 0, "PAST_0_DAY", "2026-10-14", "2026-10-14", true},
		{"2026-10-14T16:10:00Z", 0, "PAST_2_WEEK", "2026-09-30", "2026-10-14", true},
		{"2026-10-14T16:10:00Z", 0, "NEXT_2_WEEK", "2026-10-14", "2026-10-28", true},
		{"2026-10-14T16:10:00Z", 0, "PAST_1_MONTH", "2026-09-14", "2026-10-14", true},
		{"2026-10-14T16:10:00Z", 0, "NEXT_1_MONTH", "2026-10-14", "2026-11-14", true},
		{"2026-10-14T16:10:00Z", 0, "PAST_13_MONTH", "2025-09-14", "2026-10-14", true},
		{"2026-10-14T16:10:00Z", 0, "PAST_1_YEAR", "2025-10-14", "2026-10-14", true},
		{"2026-10-14T16:10:00Z", 0, "NEXT_4_YEAR", "2026-10-14", "2030-10-14", true},
		{"2026-10-14T16:10:00Z", 0, "THIS_1_DAY", "2026-10-14", "2026-10-14", true},
		{"2026-10-14T16:10:00Z", 0, "THIS_1_WEEK", "2026-10-12", "2026-10-18", true},
		{"2026-10-14T16:10:00Z", 0, "THIS_5_WEEK", "2026-10-12", "2026-10-18", true},
		{"2026-10-14T16:10:00Z", 0, "THIS_1_MONTH", "2026-10-01", "2026-10-31", true},
		{"2026-10-14T16:10:00Z", 0, "THIS_1_YEAR", "2026-01-01", "2026-12-31", true},
		{"2026-10-14T16:10:00Z", 0, "PAST_9999_YEAR", "-007973-10", "2026-10-14", true},
		{"2026-10-14T16:10:00Z", 0, "NEXT_9999_YEAR", "2026-10-14", "+012025-10", true},
		{"2026-10-14T16:10:00Z", 0, "NEXT_9999_MONTH", "2026-10-14", "2860-01-14", true},
		{"2026-10-14T16:10:00Z", 0, "PAST_9999_DAY", "1999-05-30", "2026-10-14", true},
		{"2026-10-14T16:10:00Z", 0, "PAST_0007_DAY", "2026-10-07", "2026-10-14", true},
		{"2026-10-14T16:10:00Z", 0, "PAST_7_DAYS", "", "", false},
		{"2026-10-14T16:10:00Z", 0, "LAST_7_DAY", "", "", false},
		{"2026-10-14T16:10:00Z", 0, "THIS__DAY", "", "", false},
		{"2026-10-14T16:10:00Z", 0, " PAST_7_DAY", "", "", false},
		{"2026-01-31T12:00:00Z", 0, "PAST_1_MONTH", "2025-12-31", "2026-01-31", true},
		{"2026-01-31T12:00:00Z", 0, "NEXT_1_MONTH", "2026-01-31", "2026-03-03", true},
		{"2026-01-31T12:00:00Z", 0, "PAST_13_MONTH", "2024-12-31", "2026-01-31", true},
		{"2026-01-31T12:00:00Z", 0, "THIS_1_WEEK", "2026-01-26", "2026-02-01", true},
		{"2026-01-31T12:00:00Z", 0, "PAST_9999_YEAR", "-007973-01", "2026-01-31", true},
		{"2026-01-31T12:00:00Z", 0, "NEXT_9999_YEAR", "2026-01-31", "+012025-01", true},
		{"2028-02-29T00:00:00Z", 0, "PAST_1_YEAR", "2027-03-01", "2028-02-29", true},
		{"2028-02-29T00:00:00Z", 0, "NEXT_4_YEAR", "2028-02-29", "2032-02-29", true},
		{"2028-02-29T00:00:00Z", 0, "THIS_1_WEEK", "2028-02-28", "2028-03-05", true},
		{"2028-02-29T00:00:00Z", 0, "THIS_1_MONTH", "2028-02-01", "2028-02-29", true},
		{"2026-10-18T23:59:59.999Z", 0, "THIS_1_WEEK", "2026-10-12", "2026-10-18", true},
		{"2026-10-18T23:59:59.999Z", 120, "THIS_1_DAY", "2026-10-19", "2026-10-19", true},
		{"2026-10-18T23:59:59.999Z", 120, "THIS_1_WEEK", "2026-10-19", "2026-10-25", true},
		{"2026-10-19T00:00:00Z", -300, "PAST_7_DAY", "2026-10-11", "2026-10-18", true},
		{"2026-10-19T00:00:00Z", -300, "THIS_1_WEEK", "2026-10-12", "2026-10-18", true},
		{"2026-12-31T22:30:00Z", 120, "NEXT_1_DAY", "2027-01-01", "2027-01-02", true},
		{"2026-12-31T22:30:00Z", 120, "THIS_1_MONTH", "2027-01-01", "2027-01-31", true},
		{"2026-12-31T22:30:00Z", 120, "THIS_1_YEAR", "2027-01-01", "2027-12-31", true},
		{"2026-12-31T22:30:00Z", -840, "THIS_1_YEAR", "2026-01-01", "2026-12-31", true},
		{"2026-03-01T01:00:00Z", -300, "PAST_1_YEAR", "2025-02-28", "2026-02-28", true},
		{"2026-03-01T01:00:00Z", -300, "THIS_1_MONTH", "2026-02-01", "2026-02-28", true},
		{"2026-03-01T01:00:00Z", 840, "THIS_1_MONTH", "2026-03-01", "2026-03-31", true},
	}
	for _, c := range cases {
		now, _ := time.Parse(time.RFC3339Nano, c.now)
		start, end, ok := RelativeRange(c.value, c.tz, now)
		if start != c.start || end != c.end || ok != c.ok {
			t.Errorf("RelativeRange(%q, %d, %s) = %q %q %v, want %q %q %v", c.value, c.tz, c.now, start, end, ok, c.start, c.end, c.ok)
		}
	}
	if got := LocalDay(time.Date(2026, 10, 14, 23, 30, 0, 0, time.UTC), 120); got != "2026-10-15" {
		t.Errorf("LocalDay = %q", got)
	}
}

func TestRangeIsDay(t *testing.T) {
	cases := map[string]bool{"2026-10-14": true, "2026-02-29": false, "2028-02-29": true, "2026-13-01": false,
		"2026-00-10": false, "0000-01-01": true, "2026-1-01": false, "2026-10-14 ": false,
		"2026-10-14T00:00:00Z": false, "2026-04-31": false, "9999-12-31": true, "2026-10-32": false,
		"2026-10-00": false, "": false, "abcd-ef-gh": false, "2026-10-14\n": false}
	for in, want := range cases {
		if got := IsDay(in); got != want {
			t.Errorf("IsDay(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestFilterAggregates(t *testing.T) {
	ctx := dbtest.Setup(t)
	for _, d := range []CustomFieldInput{
		{EntityType: Company, Key: "score", Label: "Score", FieldType: "integer"},
		{EntityType: Company, Key: "vip", Label: "VIP", FieldType: "boolean"},
		{EntityType: Company, Key: "since", Label: "Since", FieldType: "date"},
		{EntityType: Company, Key: "tier", Label: "Tier", FieldType: "string"},
	} {
		if _, err := CreateDef(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Run(ctx, `INSERT INTO companies (id, name, domain, industry, score, vip, since, tier) VALUES
 ('c1', 'Acme', 'acme.com', 'SaaS', 10, 1, '2024-05-01', 'a'),
 ('c2', 'Beta', '', 'saas', 5, 0, '', 'b'),
 ('c3', 'Gamma', NULL, 'Retail', NULL, 1, '2023-01-02', NULL),
 ('c4', 'Delta', 'd.io', 'SaaS', 7, NULL, NULL, 'a')`); err != nil {
		t.Fatal(err)
	}
	cols, err := TableColumns(ctx, "companies")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"id", "name", "renewal_date", "score", "vip", "since", "tier"} {
		if !cols[c] {
			t.Errorf("TableColumns lacks %q: %v", c, cols)
		}
	}
	custom, err := CustomAggregateColumns(ctx, Company, "c", cols)
	if err != nil {
		t.Fatal(err)
	}
	checkJSON(t, "custom", custom, `{"score":{"sql":"c.\"score\"","kind":"number"},"since":{"sql":"c.\"since\"","kind":"date"},"tier":{"sql":"c.\"tier\"","kind":"text"},"vip":{"sql":"c.\"vip\"","kind":"boolean"}}`)
	columns := map[string]AggregateColumn{
		"name":     {SQL: "c.name", Kind: KindText},
		"domain":   {SQL: "c.domain", Kind: KindText},
		"contacts": {SQL: "(SELECT COUNT(*) FROM contacts WHERE company_id = c.id)", Kind: KindNumber},
	}
	for k, v := range custom {
		columns[k] = v
	}
	ops := `[{"key":"name","op":"count"},{"key":"domain","op":"count_empty"},{"key":"domain","op":"percent_not_empty"},{"key":"score","op":"sum"},{"key":"score","op":"avg"},{"key":"name","op":"sum"},{"key":"bogus","op":"count"},{"key":"since","op":"earliest"},{"key":"vip","op":"count_true"},{"key":"tier","op":"count_unique"},{"key":"contacts","op":"max"},"x",5,[1]]`
	got, err := AggregateColumns(ctx, "companies c", " WHERE industry = ?", []any{"SaaS"}, columns, ops)
	if err != nil {
		t.Fatal(err)
	}
	checkJSON(t, "aggregates", got, `{"contacts":0,"domain":100,"name":2,"score":8.5,"since":"2024-05-01","tier":1,"vip":1}`)
	for _, raw := range []string{`[{"key":"bogus","op":"count"}]`, "nope", `{"key":"name","op":"count"}`, ""} {
		got, err := AggregateColumns(ctx, "companies c", "", nil, columns, raw)
		if err != nil || len(got) != 0 {
			t.Errorf("%s: %v %v", raw, got, err)
		}
	}
	got, _ = AggregateColumns(ctx, "companies c", "", nil, columns, `[{"key":"score","op":"min"},{"key":"score","op":"max"}]`)
	checkJSON(t, "last op wins", got, `{"score":10}`)
	if _, err := AggregateColumns(ctx, "companies c", "", nil, columns, "[null]"); err == nil || err.Error() != "Cannot read properties of null (reading 'key')" {
		t.Errorf("[null]: %v", err)
	}
	if KindOf("decimal") != KindNumber || KindOf("datetime") != KindDate || KindOf("relation") != KindText || !AllAggregates["count_false"] || AllAggregates["median"] {
		t.Error("KindOf / AllAggregates")
	}
}

func TestListsLogActivity(t *testing.T) {
	ctx := dbtest.Setup(t)
	LogActivity(ctx, "deal", "d1", "stage_change", "Deal won — Won", map[string]any{"stage": "won", "value": 5.0, "note": "Cedar & Co <b>"})
	LogActivity(ctx, "contact", "p1", "email", "Sent", struct {
		To []string `json:"to"`
		Cc []string `json:"cc,omitempty"`
	}{[]string{"a@x.com"}, []string{"b@y.com"}})
	LogActivity(ctx, "company", "c1", "note", "hello", nil)
	rows, err := db.Query(ctx, "SELECT entity_type, entity_id, type, body, meta FROM activities ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	var metas []string
	for _, r := range rows {
		metas = append(metas, r["meta"].(string))
	}
	want := []string{`{"note":"Cedar & Co <b>","stage":"won","value":5}`, `{"to":["a@x.com"],"cc":["b@y.com"]}`, `{}`}
	if !reflect.DeepEqual(metas, want) || rows[0]["body"] != "Deal won — Won" || rows[2]["type"] != "note" {
		t.Errorf("activities = %v", rows)
	}
	// Never throws: a broken database is swallowed.
	db.Use(nil)
	LogActivity(ctx, "deal", "d1", "x", "y", nil)
}

func TestListsQuoteIdent(t *testing.T) {
	if got := QuoteIdent(`a"b`); got != `"a""b"` {
		t.Errorf("QuoteIdent = %s", got)
	}
	if !strings.HasPrefix(CountSQL(ManyLink{Table: "deals", FK: "company_id"}, "c."), `(SELECT COUNT(*) FROM deals m WHERE m."company_id" = c.id)`) {
		t.Error("CountSQL")
	}
}

func TestListsStringify(t *testing.T) {
	// JSON.stringify writes U+2028/U+2029 as they are; encoding/json escapes
	// them. A backslash before "u2028" in the text itself stays escaped.
	got, err := stringify(map[string]any{"a": "x\u2028y\u2029", "b": "<a&b>", "c": `\u2028`})
	want := "{\"a\":\"x\u2028y\u2029\",\"b\":\"<a&b>\",\"c\":\"\\\\u2028\"}"
	if err != nil || got != want {
		t.Errorf("stringify = %s, want %s", got, want)
	}
}

func TestListsCustomValuesIssue(t *testing.T) {
	// zod 3's issue for z.record(z.string(), z.any()).optional() at "custom".
	for v, want := range map[string]string{
		"null": `{"code":"invalid_type","expected":"object","received":"null","path":["custom"],"message":"Expected object, received null"}`,
		"[1]":  `{"code":"invalid_type","expected":"object","received":"array","path":["custom"],"message":"Expected object, received array"}`,
		"5":    `{"code":"invalid_type","expected":"object","received":"number","path":["custom"],"message":"Expected object, received number"}`,
		`"s"`:  `{"code":"invalid_type","expected":"object","received":"string","path":["custom"],"message":"Expected object, received string"}`,
	} {
		var body map[string]any
		if err := json.Unmarshal([]byte(`{"custom":`+v+`}`), &body); err != nil {
			t.Fatal(err)
		}
		issue, bad := CustomValuesIssue(body)
		if !bad || jsonOf(t, issue) != want {
			t.Errorf("custom %s: %v %s", v, bad, jsonOf(t, issue))
		}
	}
	for _, body := range []map[string]any{{}, {"custom": map[string]any{"a": 1.0}}} {
		if _, bad := CustomValuesIssue(body); bad {
			t.Errorf("%v flagged", body)
		}
	}
}
