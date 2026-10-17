package crm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/db/sqlite"
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
	for _, s := range append([]string{string(schema)}, seed...) {
		if _, err := d.Run(ctx, s, nil); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	rec := &recorder{inner: d}
	db.Use(rec)
	return rec
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
