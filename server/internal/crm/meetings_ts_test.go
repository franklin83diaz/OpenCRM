package crm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"opencrm/internal/clawnify"
)

// testdata/meetings_ts.json holds meeting sync scenarios (seed, fake
// calendar / Granola / model rules, steps) and what the TS meetings.ts at
// 0068887 did with each: run by testdata/meetings_ts.gen.mts under node with
// @clawnify/db over node:sqlite, fetch answered by the same rules as
// meetingsPlatform below, and crypto.randomUUID handing out ids in creation
// order. It recorded every step's result, every call to the fakes, every SQL
// statement (with its params) and the tables afterwards. This test replays
// each scenario through the Go port and wants the same. The expectations are
// frozen: the TS source goes away once the port is done.

type fakeRule struct {
	Action  string         `json:"action"`
	Args    map[string]any `json:"args"` // a null value: the arg must be absent
	Path    string         `json:"path"`
	Query   map[string]any `json:"query"` // likewise
	UserHas string         `json:"userHas"`
	Once    bool           `json:"once"`
	Status  int            `json:"status"`
	Data    any            `json:"data"`
	Body    any            `json:"body"`
	Error   *string        `json:"error"`
	used    bool
}

type meetingsRules struct {
	Execute []fakeRule `json:"execute"`
	Granola []fakeRule `json:"granola"`
	Model   []fakeRule `json:"model"`
}

// meetingsPlatform answers what meetings.go reaches: the connections broker
// (connected, token, execute), the model, and Granola's API.
type meetingsPlatform struct {
	mu        sync.Mutex
	connected []string
	rules     meetingsRules
	calls     []any
}

func subsetOf(want map[string]any, got map[string]any) bool {
	for k, v := range want {
		g, present := got[k]
		if v == nil {
			if present {
				return false
			}
			continue
		}
		if canonical(g) != canonical(v) {
			return false
		}
	}
	return true
}

func pickRule(rules []fakeRule, match func(r *fakeRule) bool) *fakeRule {
	for i := range rules {
		r := &rules[i]
		if !r.used && match(r) {
			if r.Once {
				r.used = true
			}
			return r
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (p *meetingsPlatform) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var body map[string]any
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		json.Unmarshal(b, &body)
	}
	if r.Host == "public-api.granola.ai" {
		query := map[string]any{}
		for k, vs := range r.URL.Query() {
			query[k] = vs[0]
		}
		p.calls = append(p.calls, map[string]any{"granola": map[string]any{"path": r.URL.Path, "query": query}})
		rule := pickRule(p.rules.Granola, func(x *fakeRule) bool { return x.Path == r.URL.Path && subsetOf(x.Query, query) })
		if rule == nil {
			writeJSON(w, 404, map[string]any{"error": "no rule"})
			return
		}
		writeJSON(w, statusOr200(rule.Status), rule.Data)
		return
	}
	switch r.URL.Path {
	case "/v1/connections/connected":
		writeJSON(w, 200, map[string]any{"services": p.connected})
	case "/v1/connections/token":
		writeJSON(w, 200, map[string]any{"token": "granola-key"})
	case "/v1/connections/execute":
		action, _ := body["action"].(string)
		args, _ := body["args"].(map[string]any)
		p.calls = append(p.calls, map[string]any{"execute": map[string]any{"service": body["service"], "action": action, "args": args}})
		rule := pickRule(p.rules.Execute, func(x *fakeRule) bool { return x.Action == action && subsetOf(x.Args, args) })
		switch {
		case rule == nil:
			writeJSON(w, 200, map[string]any{"data": nil, "error": "no rule for " + action, "successful": false})
		case rule.Error != nil:
			writeJSON(w, 200, map[string]any{"data": nil, "error": *rule.Error, "successful": false})
		default:
			writeJSON(w, 200, map[string]any{"data": rule.Data, "error": nil, "successful": true})
		}
	case "/v1/chat/completions":
		p.calls = append(p.calls, map[string]any{"model": body})
		user := ""
		if msgs, ok := body["messages"].([]any); ok && len(msgs) > 1 {
			user, _ = msgs[1].(map[string]any)["content"].(string)
		}
		rule := pickRule(p.rules.Model, func(x *fakeRule) bool { return x.UserHas == "" || strings.Contains(user, x.UserHas) })
		if rule == nil {
			writeJSON(w, 500, map[string]any{"error": map[string]any{"message": "no model rule"}})
			return
		}
		writeJSON(w, statusOr200(rule.Status), rule.Body)
	default:
		writeJSON(w, 404, map[string]any{})
	}
}

func statusOr200(s int) int {
	if s == 0 {
		return 200
	}
	return s
}

// toPlatform sends Granola's API (a fixed host in the connections registry)
// to the fake platform, keeping the Host it was meant for.
type toPlatform struct{ base *url.URL }

func (t toPlatform) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "public-api.granola.ai" {
		r = r.Clone(r.Context())
		r.Host = "public-api.granola.ai"
		r.URL.Scheme, r.URL.Host = t.base.Scheme, t.base.Host
	}
	return http.DefaultTransport.RoundTrip(r)
}

// newMeetingsPlatform starts the fake and returns the env and connections that reach it.
func newMeetingsPlatform(t *testing.T, connected []string, rules meetingsRules) (*meetingsPlatform, clawnify.Env, *clawnify.Connections) {
	p := &meetingsPlatform{connected: connected, rules: rules, calls: []any{}}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	base, _ := url.Parse(srv.URL)
	env := clawnify.Env{Token: "tok", APIURL: srv.URL}
	return p, env, clawnify.NewConnections(env, &http.Client{Transport: toPlatform{base}})
}

type meetingsStep struct {
	Op       string         `json:"op"`
	Now      string         `json:"now"`
	Sync     bool           `json:"sync"`
	Ref      map[string]any `json:"ref"`
	To       map[string]any `json:"to"`
	Opts     map[string]any `json:"opts"`
	Patch    map[string]any `json:"patch"`
	Who      *string        `json:"who"`
	Services []string       `json:"services"`
	SQL      string         `json:"sql"`
}

type meetingsScenario struct {
	Name      string         `json:"name"`
	Connected []string       `json:"connected"`
	Seed      []string       `json:"seed"`
	Rules     meetingsRules  `json:"rules"`
	Steps     []meetingsStep `json:"steps"`
	Dumps     []string       `json:"dumps"`
	Expect    struct {
		Results    []string           `json:"results"`
		Calls      []any              `json:"calls"`
		Statements []tsStatement      `json:"statements"`
		Dumps      [][]map[string]any `json:"dumps"`
	} `json:"expect"`
}

var sqliteNow = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMeetingsMatchTS(t *testing.T) {
	raw, err := os.ReadFile("testdata/meetings_ts.json")
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []meetingsScenario
	if err := json.Unmarshal(raw, &scenarios); err != nil {
		t.Fatal(err)
	}
	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			rec := recordedDB(t, sc.Seed)
			ctx := context.Background()
			platform, env, conn := newMeetingsPlatform(t, append([]string{}, sc.Connected...), sc.Rules)
			idOf := func(ref map[string]any) string {
				if id, ok := ref["id"].(string); ok {
					return id
				}
				for col, v := range ref {
					rows, _ := rec.inner.Query(ctx, "SELECT id FROM meetings WHERE "+col+" = ?", []any{v})
					if len(rows) > 0 {
						return rows[0]["id"].(string)
					}
				}
				return "missing"
			}
			var results []string
			for _, st := range sc.Steps {
				var r any
				var err error
				switch st.Op {
				case "run":
					r, err = RunMeetings(ctx, env, conn, mustTime(t, st.Now), st.Sync)
				case "link":
					to := LinkTarget{}
					if st.To["ignored"] == true {
						to.Ignored = true
					} else if c, ok := st.To["company_id"].(string); ok {
						to.CompanyID = &c
					}
					r, err = LinkMeeting(ctx, idOf(st.Ref), to)
				case "retry":
					r, err = RetryDigest(ctx, idOf(st.Ref))
				case "get":
					r, err = GetMeeting(ctx, idOf(st.Ref))
				case "list":
					opts := ListMeetingsOptions{}
					opts.CompanyID, _ = st.Opts["company_id"].(string)
					opts.Link, _ = st.Opts["link"].(string)
					opts.When, _ = st.Opts["when"].(string)
					if n, ok := st.Opts["limit"].(float64); ok {
						opts.Limit = &n
					}
					if n, ok := st.Opts["offset"].(float64); ok {
						opts.Offset = &n
					}
					r, err = ListMeetings(ctx, opts, mustTime(t, st.Now))
				case "counts":
					r, err = MeetingCounts(ctx)
				case "relink":
					r, err = RelinkUnmatched(ctx)
				case "save":
					var patch SettingsPatch
					if b, ok := st.Patch["enabled"].(bool); ok {
						patch.Enabled = &b
					}
					if n, ok := st.Patch["history_days"].(float64); ok {
						patch.HistoryDays = &n
					}
					if s, ok := st.Patch["about"].(string); ok {
						patch.About = &s
					}
					before, after, e := SaveSettings(ctx, patch, st.Who)
					r, err = map[string]any{"before": syncView(before), "after": syncView(after)}, e
				case "connected":
					platform.mu.Lock()
					platform.connected = st.Services
					platform.mu.Unlock()
				case "sql":
					if _, err := rec.inner.Run(ctx, st.SQL, nil); err != nil {
						t.Fatalf("%s: %v", st.SQL, err)
					}
				}
				if err != nil {
					t.Fatalf("step %s: %v", st.Op, err)
				}
				if st.Op == "save" {
					// the map sorts keys; TS wrote before, then after
					m := r.(map[string]any)
					b1, _ := stringify(m["before"])
					b2, _ := stringify(m["after"])
					results = append(results, `{"before":`+b1+`,"after":`+b2+`}`)
					continue
				}
				s, _ := stringify(r)
				results = append(results, s)
			}

			ids := idNumbers{}
			norm := func(s string) string { return sqliteNow.ReplaceAllString(ids.apply(s), "<now>") }
			idsTS := idNumbers{}
			normTS := func(s string) string { return sqliteNow.ReplaceAllString(idsTS.apply(s), "<now>") }

			// Statements first: they hold the ids in the order they were made.
			rec.mu.Lock()
			got := append([]tsStatement{}, rec.log...)
			rec.mu.Unlock()
			for i := 0; i < len(got) || i < len(sc.Expect.Statements); i++ {
				var g, w string
				if i < len(got) {
					g = norm(canonical(got[i]))
				}
				if i < len(sc.Expect.Statements) {
					w = normTS(canonical(sc.Expect.Statements[i]))
				}
				if g != w {
					t.Fatalf("statement %d\n got  %s\n want %s", i, g, w)
				}
			}
			for i, w := range sc.Expect.Results {
				if g := norm(results[i]); g != normTS(w) {
					t.Errorf("step %d (%s %s)\n got  %s\n want %s", i, sc.Steps[i].Op, sc.Steps[i].Now, g, normTS(w))
				}
			}
			platform.mu.Lock()
			calls := platform.calls
			platform.mu.Unlock()
			if g, w := norm(canonical(calls)), normTS(canonical(sc.Expect.Calls)); g != w {
				t.Errorf("calls\n got  %s\n want %s", g, w)
			}
			for i, q := range sc.Dumps {
				rows, err := rec.inner.Query(ctx, q, nil)
				if err != nil {
					t.Fatal(err)
				}
				if g, w := norm(canonical(rows)), normTS(canonical(sc.Expect.Dumps[i])); g != w {
					t.Errorf("dump %s\n got  %s\n want %s", q, g, w)
				}
			}
		})
	}
}
