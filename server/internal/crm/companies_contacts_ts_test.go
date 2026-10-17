package crm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"opencrm/internal/js"
)

// testdata/companies_contacts_ts.json holds scenarios (seed, steps) and what
// the TS routes at 0068887 answered — stats, widgets, and the companies and
// contacts lists, aggregates and writes — with every SQL statement and its
// params: run by testdata/companies_contacts_ts.gen.mts, the whole Hono app
// under node with @clawnify/db over node:sqlite and crypto.randomUUID handing
// out ids in creation order. This test replays each scenario through the Go
// server (New) and wants the same. They cover what the parity scenario can't
// reach: custom fields and relations, database errors, widgets that fail
// their schema, and the statements of chunked bulk deletes. The expectations
// are frozen: the TS source goes away once the port is done.

type ccStep struct {
	Op      string            `json:"op"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Body    json.RawMessage   `json:"body"`
	Headers map[string]string `json:"headers"`
	DBError bool              `json:"dbError"`
	Days    bool              `json:"days"`
	Raw     bool              `json:"raw"`
	Input   map[string]any    `json:"input"`
	SQL     string            `json:"sql"`
}

type ccResult struct {
	Status      int             `json:"status"`
	ContentType string          `json:"contentType"`
	Body        string          `json:"body"`
	Def         json.RawMessage `json:"def"`
	Error       *string         `json:"error"`
}

type ccScenario struct {
	Name   string   `json:"name"`
	Seed   []string `json:"seed"`
	Steps  []ccStep `json:"steps"`
	Expect struct {
		Results    []*ccResult   `json:"results"`
		Statements []tsStatement `json:"statements"`
	} `json:"expect"`
}

var (
	sqliteStamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)
	isoDayShape = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
)

// sequentialUUIDs hands out ids in creation order, as the generator did.
func sequentialUUIDs(t *testing.T) {
	n := 0
	old := js.UUID
	js.UUID = func() string {
		n++
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
	}
	t.Cleanup(func() { js.UUID = old })
}

func ccStr(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// ccDef runs a custom-field step through the Go custom_fields.go.
func ccDef(ctx context.Context, st ccStep) (*CustomFieldDef, error) {
	in := st.Input
	if st.Op == "field" {
		return CreateDef(ctx, CustomFieldInput{
			EntityType: EntityType(ccStr(in, "entity_type")), Key: ccStr(in, "key"), Label: ccStr(in, "label"),
			FieldType: in["field_type"], CustomField: in["custom_field"], Options: in["options"], Position: in["position"],
		})
	}
	return CreateRelation(ctx, RelationInput{
		EntityType: EntityType(ccStr(in, "entity_type")), Key: ccStr(in, "key"), Label: ccStr(in, "label"),
		RelationType: ccStr(in, "relation_type"), TargetEntity: EntityType(ccStr(in, "target_entity")),
		InverseKey: ccStr(in, "inverse_key"), InverseLabel: ccStr(in, "inverse_label"), Position: in["position"],
	})
}

// ccBody is the request body as the generator sent it: a JSON string as is,
// anything else JSON.stringify'd (compact, keys as listed).
func ccBody(t *testing.T, raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatal(err)
	}
	return buf.String(), true
}

func ccMediaType(ct string) string {
	mt, _, _ := strings.Cut(ct, ";")
	return strings.ToLower(strings.TrimSpace(mt))
}

// canonicalJSON is a JSON text with its keys sorted, or the text itself when
// it isn't JSON.
func canonicalJSON(text string) string {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return text
	}
	return canonical(v)
}

// finiteParams is a statement's params as JSON.stringify recorded them: NaN
// and ±Infinity are null.
func finiteParams(params []any) []any {
	out := make([]any, len(params))
	for i, p := range params {
		if f, ok := p.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
			p = nil
		}
		out[i] = p
	}
	return out
}

func TestCompaniesContactsMatchTS(t *testing.T) {
	raw, err := os.ReadFile("testdata/companies_contacts_ts.json")
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []ccScenario
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
			h := New(&App{})

			// Timestamps the seed wrote are compared; datetime('now') ones are not.
			fixed := map[string]bool{}
			days := false
			for _, s := range sc.Seed {
				for _, m := range sqliteStamp.FindAllString(s, -1) {
					fixed[m] = true
				}
			}
			for _, st := range sc.Steps {
				days = days || st.Days
			}
			norm := func(s string, days bool) string {
				s = sqliteStamp.ReplaceAllStringFunc(s, func(m string) string {
					if fixed[m] {
						return m
					}
					return "<now>"
				})
				if days {
					s = isoDayShape.ReplaceAllString(s, "<day>")
				}
				return s
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
					if g, w := norm(canonicalJSON(string(got)), false), norm(canonicalJSON(string(want.Def)), false); g != w {
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
					switch {
					case st.DBError:
						// The database's own words differ (node:sqlite, SQLite in
						// Go, D1): only that it is an {error} answer.
						var e struct{ Error string }
						if json.Unmarshal([]byte(got), &e) != nil || e.Error == "" {
							t.Errorf("%s: want a database error, got %s", label, got)
						}
					case st.Raw:
						if g, wt := norm(got, st.Days), norm(want.Body, st.Days); g != wt {
							t.Errorf("%s\n got  %s\n want %s", label, g, wt)
						}
					default:
						if g, wt := norm(canonicalJSON(got), false), norm(canonicalJSON(want.Body), false); g != wt {
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
					g = norm(canonical(tsStatement{got[i].SQL, finiteParams(got[i].Params)}), days)
				}
				if i < len(sc.Expect.Statements) {
					w = norm(canonical(sc.Expect.Statements[i]), days)
				}
				if g != w {
					t.Fatalf("statement %d of %d\n got  %s\n want %s", i, len(sc.Expect.Statements), g, w)
				}
			}
		})
	}
}
