package db_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
)

// fakeD1 answers the bridge's wire protocol the way the Worker does.
func fakeD1(t *testing.T, got *map[string]any) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/query" {
			http.Error(w, `{"error":"wrong endpoint"}`, 404)
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding bridge request: %v", err)
		}
		*got = req
		if strings.Contains(req["sql"].(string), "boom") {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"D1_ERROR: boom"}`))
			return
		}
		w.Write([]byte(`{"results":[{"id":"a","n":3,"name":"Cedar & Co"},{"id":"b","n":4.5,"name":null}],"meta":{"changes":2,"last_row_id":17}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestD1(t *testing.T) {
	var got map[string]any
	srv := fakeD1(t, &got)
	db.Use(db.NewD1(srv.URL, srv.Client()))
	ctx := context.Background()

	row, err := db.Get(ctx, "SELECT * FROM companies WHERE id = ?", "a", 2, int64(3), 1.5, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (db.Row{"id": "a", "n": float64(3), "name": "Cedar & Co"}); !reflect.DeepEqual(row, want) {
		t.Errorf("Get = %#v; want %#v", row, want)
	}
	wantReq := map[string]any{
		"sql":    "SELECT * FROM companies WHERE id = ?",
		"params": []any{"a", 2.0, 3.0, 1.5, true, nil},
		"mode":   "all",
	}
	if !reflect.DeepEqual(got, wantReq) {
		t.Errorf("bridge request = %#v; want %#v", got, wantReq)
	}

	rows, err := db.Query(ctx, "SELECT * FROM companies")
	if err != nil || len(rows) != 2 || rows[1]["n"] != 4.5 || rows[1]["name"] != nil {
		t.Errorf("Query = %#v, %v", rows, err)
	}
	if p, ok := got["params"].([]any); !ok || len(p) != 0 {
		t.Errorf("no params sent as %#v; want []", got["params"])
	}

	res, err := db.Run(ctx, "DELETE FROM companies")
	if err != nil || res != (db.Result{Changes: 2, LastRowID: 17}) || got["mode"] != "run" {
		t.Errorf("Run = %#v, %v (mode %v)", res, err, got["mode"])
	}

	_, err = db.Query(ctx, "SELECT boom")
	if err == nil || err.Error() != "D1_ERROR: boom" {
		t.Errorf("Query error = %v; want D1_ERROR: boom", err)
	}
}

// JSON has no NaN or Infinity, but TS bound them into D1 as numbers (?page=abc
// is NaN, a deal value "Infinity" is Infinity). Such a param crosses the
// bridge as {"$num": ...}, which the Worker binds as that number again; every
// other param is sent as it is.
func TestD1NonFiniteParams(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Write([]byte(`{"results":[],"meta":{"changes":0,"last_row_id":0}}`))
	}))
	t.Cleanup(srv.Close)
	db.Use(db.NewD1(srv.URL, srv.Client()))

	params := []any{math.NaN(), math.Inf(1), math.Inf(-1), 1.5, 0.0, math.Copysign(0, -1), 7, int64(8), "NaN", true, nil}
	if _, err := db.Query(context.Background(), "SELECT ? LIMIT ? OFFSET ?", params...); err != nil {
		t.Fatal(err)
	}
	want := `{"sql":"SELECT ? LIMIT ? OFFSET ?","params":[{"$num":"NaN"},{"$num":"Infinity"},{"$num":"-Infinity"},1.5,0,-0,7,8,"NaN",true,null],"mode":"all"}`
	if body != want {
		t.Errorf("bridge body\n got  %s\n want %s", body, want)
	}
	if !math.IsNaN(params[0].(float64)) || !math.IsInf(params[1].(float64), 1) {
		t.Error("the caller's params were changed")
	}

	if _, err := db.Run(context.Background(), "UPDATE deals SET value = ?", math.Inf(1)); err != nil {
		t.Fatal(err)
	}
	if want := `{"sql":"UPDATE deals SET value = ?","params":[{"$num":"Infinity"}],"mode":"run"}`; body != want {
		t.Errorf("bridge body\n got  %s\n want %s", body, want)
	}
}

func TestParamsMustBeScalars(t *testing.T) {
	var got map[string]any
	srv := fakeD1(t, &got)
	db.Use(db.NewD1(srv.URL, srv.Client()))

	_, err := db.Query(context.Background(), "x", struct{}{})
	want := "@clawnify/db: param [0] is object — D1/SQLite only binds scalars. If this is a JSON column, wrap with json(value). Got: {}"
	if err == nil || err.Error() != want {
		t.Errorf("Query(struct{}{}) error = %v; want %q", err, want)
	}
	_, err = db.Run(context.Background(), "x", "ok", map[string]any{"a": strings.Repeat("x", 100)})
	if err == nil || !strings.HasPrefix(err.Error(), "@clawnify/db: param [1] is object") || !strings.HasSuffix(err.Error(), `xxx...`) {
		t.Errorf("Run(map) error = %v", err)
	}
	// The bridge speaks JSON: a []byte would cross as base64 and be bound as
	// TEXT in production while SQLite stored a real BLOB in tests.
	_, err = db.Run(context.Background(), "x", nil, []byte("blob"))
	want = "@clawnify/db: param [1] is []byte — blobs aren't supported over the D1 bridge (they would be stored as base64 text)"
	if err == nil || err.Error() != want {
		t.Errorf("Run([]byte) error = %v; want %q", err, want)
	}
	if got != nil {
		t.Errorf("a refused query reached the bridge: %#v", got)
	}
}

func TestSQLite(t *testing.T) {
	ctx := dbtest.Setup(t)

	// The whole schema script ran, not just its first statement (its last
	// ones, the upgrade UPDATEs, are crm's TestSchemaRetiresTheOldSyncs).
	if row, err := db.Get(ctx, "SELECT name FROM sqlite_master WHERE name = 'idx_api_tokens_user'"); err != nil || row == nil {
		t.Fatalf("schema.sql's last CREATE did not run: %v, %v", row, err)
	}
	if _, err := db.Run(ctx, "INSERT INTO companies (id, name) VALUES (?, ?)", "c1", "Cedar & Co"); err != nil {
		t.Fatal(err)
	}
	row, err := db.Get(ctx, "SELECT name, COUNT(*) AS n, 1.5 AS f, NULL AS z FROM companies WHERE id = ?", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if want := (db.Row{"name": "Cedar & Co", "n": float64(1), "f": 1.5, "z": nil}); !reflect.DeepEqual(row, want) {
		t.Errorf("Get = %#v; want %#v", row, want)
	}
	if row, err := db.Get(ctx, "SELECT id FROM companies WHERE id = ?", "missing"); err != nil || row != nil {
		t.Errorf("Get(no rows) = %#v, %v; want nil, nil", row, err)
	}

	// D1 enforces foreign keys, so the local driver must too.
	_, err = db.Run(ctx, "INSERT INTO contacts (id, first_name, company_id) VALUES (?, ?, ?)", "p1", "Ann", "missing")
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("insert with a dangling company_id: err = %v; want a FOREIGN KEY failure", err)
	}
	res, err := db.Run(ctx, "INSERT INTO contacts (id, first_name, company_id) VALUES (?, ?, ?)", "p1", "Ann", "c1")
	if err != nil || res.Changes != 1 {
		t.Errorf("insert contact = %#v, %v", res, err)
	}

	// D1 binds a boolean as the number 1 or 0, a REAL like every JS number:
	// a TEXT column keeps "1.0", an INTEGER one 1.
	if _, err := db.Run(ctx, "INSERT INTO custom_field_defs (id, entity_type, key, label, custom_field, position) VALUES (?, ?, ?, ?, ?, ?)",
		"d1", "deal", "k", true, false, true); err != nil {
		t.Fatal(err)
	}
	row, err = db.Get(ctx, "SELECT label, custom_field, position FROM custom_field_defs WHERE id = ? AND position = ?", "d1", true)
	if want := (db.Row{"label": "1.0", "custom_field": "0.0", "position": float64(1)}); err != nil || !reflect.DeepEqual(row, want) {
		t.Errorf("booleans bound = %#v, %v; want %#v", row, err, want)
	}
}
