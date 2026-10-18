package crm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// testdata/deals_ts.json holds scenarios (seed, steps) and what the TS routes
// at 0068887 answered — stages, deals (board, progress, list, writes, the
// won/lost side effects), tasks, deal updates, and the records read by id,
// records and values — with every SQL statement and its params: run by
// testdata/deals_ts.gen.mts, the whole Hono app under node with @clawnify/db
// over node:sqlite, crypto.randomUUID handing out ids in creation order, and
// new Date() fixed at the scenario's `now`. This test replays each scenario
// through the Go server (New, with App.Now fixed the same way) and wants the
// same. They cover what the parity scenario can't reach: custom fields and
// relations on deals, tasks from calls, a populated deal progress, Date.parse
// on a deal update's `at`, an empty pipeline and the last stage, and database
// errors. The expectations are frozen: the TS source goes away once the port
// is done.

type dealsTSScenario struct {
	Name   string   `json:"name"`
	Now    string   `json:"now"`
	Seed   []string `json:"seed"`
	Steps  []ccStep `json:"steps"`
	Expect struct {
		Results    []*ccResult   `json:"results"`
		Statements []tsStatement `json:"statements"`
	} `json:"expect"`
}

func TestDealsMatchTS(t *testing.T) {
	raw, err := os.ReadFile("testdata/deals_ts.json")
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []dealsTSScenario
	if err := json.Unmarshal(raw, &scenarios); err != nil {
		t.Fatal(err)
	}
	if len(scenarios) == 0 {
		t.Fatal("no scenarios")
	}
	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			rec := recordedDB(t, sc.Seed)
			sequentialUUIDs(t)
			ctx := context.Background()
			now, err := time.Parse(time.RFC3339Nano, sc.Now)
			if err != nil {
				t.Fatal(err)
			}
			h := replayAPI(&App{Now: func() time.Time { return now }})

			// The scenarios' times are all before now + a day; a later stamp is
			// SQLite's datetime('now') at the run (the generator's or this one's).
			cutoff := now.Add(24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
			norm := func(s string) string {
				return sqliteStamp.ReplaceAllStringFunc(s, func(m string) string {
					if m > cutoff {
						return "<now>"
					}
					return m
				})
			}

			for i, st := range sc.Steps {
				want := sc.Expect.Results[i]
				switch st.Op {
				case "sql":
					if _, err := rec.inner.Run(ctx, st.SQL, nil); err != nil {
						t.Fatalf("step %d: %s: %v", i, st.SQL, err)
					}
				case "field", "relation":
					def, err := ccDef(ctx, st)
					if want.Error != nil {
						if err == nil || err.Error() != *want.Error {
							t.Errorf("step %d (%s %s): error %v, want %q", i, st.Op, st.Input["key"], err, *want.Error)
						}
						continue
					}
					if err != nil {
						t.Fatalf("step %d (%s %s): %v", i, st.Op, st.Input["key"], err)
					}
					got, _ := json.Marshal(def)
					if g, w := norm(canonicalJSON(string(got))), norm(canonicalJSON(string(want.Def))); g != w {
						t.Errorf("step %d (%s %s)\n got  %s\n want %s", i, st.Op, st.Input["key"], g, w)
					}
				case "request":
					body, hasBody := ccBody(t, st.Body)
					var r *http.Request
					if hasBody {
						r = httptest.NewRequest(st.Method, st.Path, strings.NewReader(body))
						r.Header.Set("Content-Type", "application/json")
					} else {
						r = httptest.NewRequest(st.Method, st.Path, nil)
					}
					for k, v := range st.Headers {
						r.Header.Set(k, v)
					}
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					label := fmt.Sprintf("step %d (%s %s %s)", i, st.Method, st.Path, body)
					if w.Code != want.Status {
						t.Errorf("%s: status %d, want %d\n got  %s\n want %s", label, w.Code, want.Status, w.Body.String(), want.Body)
						continue
					}
					if g, wt := ccMediaType(w.Header().Get("Content-Type")), ccMediaType(want.ContentType); g != wt {
						t.Errorf("%s: content-type %q, want %q", label, g, wt)
					}
					got := w.Body.String()
					if st.DBError {
						// The database's own words differ (node:sqlite, SQLite in
						// Go, D1): only that it is an {error} answer.
						var e struct{ Error string }
						if json.Unmarshal([]byte(got), &e) != nil || e.Error == "" {
							t.Errorf("%s: want a database error, got %s", label, got)
						}
						continue
					}
					if g, wt := norm(canonicalJSON(withoutPartners(got))), norm(canonicalJSON(want.Body)); g != wt {
						t.Errorf("%s\n got  %s\n want %s", label, g, wt)
					}
				default:
					t.Fatalf("unknown op %s", st.Op)
				}
			}

			rec.mu.Lock()
			got := withoutPartnerStatements(rec.log)
			rec.mu.Unlock()
			for i := 0; i < len(got) || i < len(sc.Expect.Statements); i++ {
				var g, w string
				if i < len(got) {
					g = norm(canonical(tsStatement{got[i].SQL, finiteParams(got[i].Params)}))
				}
				if i < len(sc.Expect.Statements) {
					w = norm(canonical(sc.Expect.Statements[i]))
				}
				if g != w {
					t.Fatalf("statement %d of %d\n got  %s\n want %s", i, len(sc.Expect.Statements), g, w)
				}
			}
		})
	}
}
