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

// testdata/settings_ts.json holds scenarios (seed, steps) and what the TS
// routes at 0068887 answered — the activity timeline, CSV import, custom
// fields and views — with every SQL statement and its params: run by
// testdata/settings_ts.gen.mts, the whole Hono app under node with
// @clawnify/db over node:sqlite, crypto.randomUUID handing out ids in
// creation order, and new Date() fixed at the scenario's `now`. This test
// replays each scenario through the Go server (New, with App.Now fixed the
// same way) and wants the same. They cover what the parity scenario can't
// see: the statements of imports chunked under D1's 100 parameters and the
// column order of their custom fields, the key order of the JSON the routes
// store (a view's filters, a field's options, an imported json cell), rows
// the API can't write, and database errors. The expectations are frozen: the
// TS source goes away once the port is done.

type settingsTSScenario struct {
	Name   string   `json:"name"`
	Now    string   `json:"now"`
	Seed   []string `json:"seed"`
	Steps  []ccStep `json:"steps"`
	Expect struct {
		Results    []*ccResult   `json:"results"`
		Statements []tsStatement `json:"statements"`
	} `json:"expect"`
}

func TestSettingsMatchTS(t *testing.T) {
	raw, err := os.ReadFile("testdata/settings_ts.json")
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []settingsTSScenario
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
					shown := body
					if len(shown) > 200 {
						shown = shown[:200] + "…"
					}
					label := fmt.Sprintf("step %d (%s %s %s)", i, st.Method, st.Path, shown)
					if w.Code != want.Status {
						t.Errorf("%s: status %d, want %d\n got  %s\n want %s", label, w.Code, want.Status, w.Body.String(), want.Body)
						continue
					}
					if g, wt := ccMediaType(w.Header().Get("Content-Type")), ccMediaType(want.ContentType); g != wt {
						t.Errorf("%s: content-type %q, want %q", label, g, wt)
					}
					got := w.Body.String()
					switch {
					case st.DBError:
						// The database's own words differ (node:sqlite, SQLite in
						// Go, D1): only that it is an {error} answer.
						var e struct{ Error string }
						if json.Unmarshal([]byte(got), &e) != nil || e.Error == "" {
							t.Errorf("%s: want a database error, got %s", label, got)
						}
					case st.Raw:
						if g, wt := norm(got), norm(want.Body); g != wt {
							t.Errorf("%s\n got  %s\n want %s", label, g, wt)
						}
					default:
						if g, wt := norm(canonicalJSON(got)), norm(canonicalJSON(want.Body)); g != wt {
							t.Errorf("%s\n got  %s\n want %s", label, g, wt)
						}
					}
				default:
					t.Fatalf("unknown op %s", st.Op)
				}
			}

			rec.mu.Lock()
			got := append([]tsStatement{}, rec.log...)
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
