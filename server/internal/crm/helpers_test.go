package crm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/db/sqlite"
	"opencrm/internal/web"
)

// Helpers the tests share. The *_ts_test.go files replay what the TS app at
// 0068887 did (frozen in testdata/*_ts.json, made by the *_ts.gen.mts
// generators): every SQL statement it ran, with its params, recorded by a
// stub of @clawnify/db over node:sqlite. recorder keeps the Go port's
// statements the same way, to compare.

type tsStatement struct {
	SQL    string `json:"sql"`
	Params []any  `json:"params"`
}

// recorder is a db.Driver that keeps every statement, as the TS stub did.
type recorder struct {
	inner db.Driver
	mu    sync.Mutex
	log   []tsStatement
}

func (r *recorder) Query(ctx context.Context, sql string, params []any) ([]db.Row, error) {
	r.add(sql, params)
	return r.inner.Query(ctx, sql, params)
}

func (r *recorder) Run(ctx context.Context, sql string, params []any) (db.Result, error) {
	r.add(sql, params)
	return r.inner.Run(ctx, sql, params)
}

func (r *recorder) add(sql string, params []any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, tsStatement{SQL: sql, Params: append([]any{}, params...)})
}

// replayAPI serves the routes as the frozen fixtures were recorded: when
// Clawnify's proxy said who was calling in X-Clawnify-* headers, before the
// CRM had accounts. It skips the auth middleware (whose own statements would
// not be in the fixtures) and reads the caller from those headers as
// @clawnify/app's caller() and user() did. Only tests use it: the CRM itself
// ignores the headers.
//
// TS let Clawnify's "agent" caller write tasks, which then read "agent" as
// created_by. The CRM's writers are now a session or an API token, so an
// agent step replays as a token whose account goes by "agent": the same
// writes, the same rows.
func replayAPI(app *App) http.Handler {
	rt := app.router()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := "public"
		if v := clawnifyHeader(r, "X-Clawnify-Caller"); clawnifyCallers[v] {
			caller = v
		}
		var u *web.User
		if id := clawnifyHeader(r, "X-Clawnify-User-Id"); id != "" {
			u = &web.User{ID: id, Email: clawnifyHeader(r, "X-Clawnify-User-Email"), Name: clawnifyHeader(r, "X-Clawnify-User-Name")}
		}
		if caller == "agent" && u == nil {
			caller, u = "api", &web.User{Email: "agent"}
		}
		rt.ServeHTTP(w, web.WithIdentity(r, caller, u))
	})
}

var clawnifyCallers = map[string]bool{
	"user": true, "api": true, "agent": true, "agent-browser": true,
	"public": true, "bypass": true, "system": true, "app": true,
}

// clawnifyHeader is the trimmed header value, "" when absent or blank.
func clawnifyHeader(r *http.Request, name string) string {
	return strings.TrimSpace(strings.Join(r.Header.Values(name), ", "))
}

// recordedDB is a fresh database with the schema and seed, installed behind a recorder.
func recordedDB(t *testing.T, seed []string) *recorder {
	t.Helper()
	d, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Use(nil)
		if c, ok := d.(io.Closer); ok {
			c.Close()
		}
	})
	schema, err := os.ReadFile("../../schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The schema in one transaction: one write to disk, not one per statement.
	for _, s := range append([]string{"BEGIN;\n" + string(schema) + "\nCOMMIT;"}, seed...) {
		if _, err := d.Run(ctx, s, nil); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	rec := &recorder{inner: d}
	db.Use(rec)
	return rec
}

// Partners, a deal's recurring value and a deal's owner came after the port,
// so TS never answered with them: a replay compares what TS did with them left
// out — a deal's `partners`, `recurring_value`, `owner_id` and `owner`, the
// same among a deal's valid fields (partner_ids for partners), the stats'
// count of partners and recurring value, and the statements on the partners
// and deal_partners tables or the recurring value (no statement TS ran names
// any of them).

// postPortDealKeys are a deal's keys TS never had.
var postPortDealKeys = []string{"partners", "recurring_value", "owner_id", "owner"}

// withoutPartners is a response body without what came after the port; text
// that isn't JSON comes back as it is.
func withoutPartners(text string) string {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return text
	}
	var strip func(any)
	strip = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if _, deal := v["stage"]; deal {
				for _, k := range postPortDealKeys {
					delete(v, k)
				}
			}
			if _, stats := v["dealValue"]; stats {
				delete(v, "partners")
				delete(v, "dealRecurringValue")
			}
			// A deal's valid fields: a company has an owner_id of its own in the replays.
			if valid, ok := v["valid_fields"].([]any); ok && strings.HasPrefix(fmt.Sprint(v["error"]), "Unknown field(s) for deal:") {
				kept := []any{}
				for _, k := range valid {
					if k != "partner_ids" && k != "recurring_value" && k != "owner_id" {
						kept = append(kept, k)
					}
				}
				v["valid_fields"] = kept
			}
			for _, x := range v {
				strip(x)
			}
		case []any:
			for _, x := range v {
				strip(x)
			}
		}
	}
	strip(v)
	b, _ := json.Marshal(v)
	return string(b)
}

var partnerTables = regexp.MustCompile(`\b(partners|deal_partners|recurring_value)\b`)

// withoutPartnerStatements is log without the statements that came after the
// port.
func withoutPartnerStatements(log []tsStatement) []tsStatement {
	out := []tsStatement{}
	for _, st := range log {
		if !partnerTables.MatchString(st.SQL) {
			out = append(out, st)
		}
	}
	return out
}

// canonical is v as JSON with sorted keys, for comparing decoded values.
func canonical(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<%v>", err)
	}
	var back any
	json.Unmarshal(b, &back)
	b, _ = json.Marshal(back)
	return string(b)
}

func mustRun(t *testing.T, ctx context.Context, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := db.Run(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Expected values come from running the same inputs through the JS built-ins (node 22).
func TestJSHelpers(t *testing.T) {
	names := []string{"banana", "Apple", "cherry", "apple", "Älpha", "alpha", "Zeta", "zeta", "a-b", "ab", "a b", "a_b", "10", "9", "x1", "X",
		"Ünder", "under", "Clients 2024", "clients", "[Imap]/Sent", "Work/Projects", "work", "Ñandu", "nandu", "émile", "Emile", "Ébène"}
	sort.SliceStable(names, func(i, j int) bool { return localeCompare(names[i], names[j]) < 0 })
	wantOrder := []string{"[Imap]/Sent", "10", "9", "a b", "a_b", "a-b", "ab", "alpha", "Älpha", "apple", "Apple", "banana", "cherry", "clients",
		"Clients 2024", "Ébène", "Emile", "émile", "nandu", "Ñandu", "under", "Ünder", "work", "Work/Projects", "X", "x1", "zeta", "Zeta"}
	if !reflect.DeepEqual(names, wantOrder) {
		t.Errorf("localeCompare order\n got  %q\n want %q", names, wantOrder)
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
}

// errMessage reads a failed request as its cause, without Go's `Post "url": `.
func TestErrMessage(t *testing.T) {
	cause := errors.New("connection refused")
	if got := errMessage(&url.Error{Op: "Post", URL: "http://d1.internal/query", Err: cause}); got != "connection refused" {
		t.Errorf("errMessage(url.Error) = %q", got)
	}
	if got := errMessage(errors.New("D1_ERROR: boom")); got != "D1_ERROR: boom" {
		t.Errorf("errMessage = %q", got)
	}
}
