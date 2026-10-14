package crm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"opencrm/internal/clawnify"
	"opencrm/internal/db"
	"opencrm/internal/db/sqlite"
	"opencrm/internal/js"
)

// testdata/email_sync_ts.json holds Gmail sync scenarios (seed, broker rules,
// steps) and what the TS email-sync.ts at 0068887 did with each: run through
// node with @clawnify/db stubbed over node:sqlite, a fake broker on localhost
// answering by the same rules as scriptedBroker below, and crypto.randomUUID
// handing out ids in creation order. It recorded every result, broker call,
// SQL statement (with its params) and the tables afterwards. This test replays
// each scenario through the Go port and wants the same. The expectations are
// frozen: the TS source goes away once the port is done.

type tsRule struct {
	Action   string         `json:"action"`
	QueryHas string         `json:"queryHas"`
	Args     map[string]any `json:"args"` // a null value: the arg must be absent
	Once     bool           `json:"once"`
	Data     any            `json:"data"`
	Error    *string        `json:"error"`
	used     bool
}

type tsStatement struct {
	SQL    string `json:"sql"`
	Params []any  `json:"params"`
}

type tsScenario struct {
	Name      string   `json:"name"`
	Connected []string `json:"connected"`
	Seed      []string `json:"seed"`
	Rules     []tsRule `json:"rules"`
	Steps     []struct {
		Op        string    `json:"op"`
		Mailbox   string    `json:"mailbox"`
		Contact   string    `json:"contact"`
		Now       string    `json:"now"`
		Connected *[]string `json:"connected"`
	} `json:"steps"`
	Dumps  []string `json:"dumps"`
	Expect struct {
		Results    []string           `json:"results"`
		Calls      []any              `json:"calls"`
		Statements []tsStatement      `json:"statements"`
		Dumps      [][]map[string]any `json:"dumps"`
	} `json:"expect"`
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

// scriptedBroker answers the connections API from a scenario's rules.
type scriptedBroker struct {
	mu        sync.Mutex
	connected []string
	rules     []tsRule
	calls     []any
}

func (b *scriptedBroker) match(rule *tsRule, action string, args map[string]any) bool {
	if rule.Action != action || rule.used {
		return false
	}
	if rule.QueryHas != "" {
		q, ok := args["query"].(string)
		if !ok || !strings.Contains(q, rule.QueryHas) {
			return false
		}
	}
	for k, v := range rule.Args {
		got, present := args[k]
		if v == nil {
			if present {
				return false
			}
			continue
		}
		if canonical(got) != canonical(v) {
			return false
		}
	}
	return true
}

func (b *scriptedBroker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/connections/connected":
		json.NewEncoder(w).Encode(map[string]any{"services": b.connected})
	case "/v1/connections/execute":
		var req struct {
			Service string         `json:"service"`
			Action  string         `json:"action"`
			Args    map[string]any `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		b.calls = append(b.calls, map[string]any{"service": req.Service, "action": req.Action, "args": req.Args})
		for i := range b.rules {
			rule := &b.rules[i]
			if !b.match(rule, req.Action, req.Args) {
				continue
			}
			if rule.Once {
				rule.used = true
			}
			if rule.Error != nil {
				json.NewEncoder(w).Encode(map[string]any{"data": nil, "error": *rule.Error, "successful": false})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"data": rule.Data, "error": nil, "successful": true})
			}
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": nil, "error": "no rule for " + req.Action, "successful": false})
	default:
		w.WriteHeader(404)
		io.WriteString(w, "{}")
	}
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

var uuidShape = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// idNumbers numbers UUIDs by first appearance, so runs with different random
// ids compare equal when they used them alike.
type idNumbers map[string]int

func (n idNumbers) apply(s string) string {
	return uuidShape.ReplaceAllStringFunc(s, func(id string) string {
		if _, ok := n[id]; !ok {
			n[id] = len(n) + 1
		}
		return fmt.Sprintf("<id:%d>", n[id])
	})
}

// sequentialIDs makes new contacts' ids sort in creation order, as gen.mts did.
func sequentialIDs(t *testing.T) {
	seq := 0
	old := newContactID
	newContactID = func() string {
		seq++
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", seq)
	}
	t.Cleanup(func() { newContactID = old })
}

func TestEmailSyncMatchesTS(t *testing.T) {
	raw, err := os.ReadFile("testdata/email_sync_ts.json")
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []tsScenario
	if err := json.Unmarshal(raw, &scenarios); err != nil {
		t.Fatal(err)
	}
	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			rec := recordedDB(t, sc.Seed)
			sequentialIDs(t)
			broker := &scriptedBroker{rules: sc.Rules}
			srv := httptest.NewServer(broker)
			defer srv.Close()
			conn := clawnify.NewConnections(clawnify.Env{Token: "tok", APIURL: srv.URL}, nil)
			ctx := context.Background()

			var results []string
			for _, step := range sc.Steps {
				broker.mu.Lock()
				broker.connected = sc.Connected
				if step.Connected != nil {
					broker.connected = *step.Connected
				}
				broker.mu.Unlock()
				now, ok := js.ParseDate(step.Now)
				if !ok {
					t.Fatalf("bad now %q", step.Now)
				}
				switch step.Op {
				case "runSync":
					res, err := RunSync(ctx, conn, step.Mailbox, now)
					if err != nil {
						t.Fatalf("RunSync: %v", err)
					}
					s, _ := stringify(res)
					results = append(results, s)
				default:
					if err := ImportContactIfNeeded(ctx, conn, step.Contact, now); err != nil {
						s, _ := stringify(map[string]any{"error": errMessage(err)})
						results = append(results, s)
					} else {
						results = append(results, `{"ok":true}`)
					}
				}
			}
			var dumps [][]map[string]any
			for _, q := range sc.Dumps {
				rows, err := rec.inner.Query(ctx, q, nil)
				if err != nil {
					t.Fatal(err)
				}
				dumps = append(dumps, rows)
			}

			want, got := idNumbers{}, idNumbers{}
			compare := func(what string, w, g []string) {
				t.Helper()
				for i := 0; i < len(w) || i < len(g); i++ {
					var ws, gs string
					if i < len(w) {
						ws = want.apply(w[i])
					}
					if i < len(g) {
						gs = got.apply(g[i])
					}
					if ws != gs {
						t.Errorf("%s #%d:\n TS: %s\n Go: %s", what, i, ws, gs)
						return
					}
				}
			}
			compare("result", sc.Expect.Results, results)
			callsOf := func(calls []any) []string {
				out := make([]string, len(calls))
				for i, c := range calls {
					out[i] = canonical(c)
				}
				return out
			}
			compare("broker call", callsOf(sc.Expect.Calls), callsOf(broker.calls))
			stmts := func(log []tsStatement) []string {
				out := make([]string, len(log))
				for i, s := range log {
					out[i] = s.SQL + "\n  params: " + canonical(s.Params)
				}
				return out
			}
			compare("statement", stmts(sc.Expect.Statements), stmts(rec.log))
			for i := range sc.Dumps {
				compare("dump "+sc.Dumps[i][:40], []string{canonical(sc.Expect.Dumps[i])}, []string{canonical(dumps[i])})
			}
		})
	}
}
