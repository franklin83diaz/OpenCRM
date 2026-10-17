package crm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"opencrm/internal/db/dbtest"
	"opencrm/internal/web"
)

// The routes that existed only for Clawnify, the integrations (Gmail,
// Calendar, Slack, Granola) and AI. The CRM no longer has them: each answers
// the not-found JSON, and the discovery documents don't list them.
var removedRoutes = []string{
	"GET /api/integrations/status",
	"POST /api/integrations/email",
	"POST /api/integrations/meeting",
	"GET /api/email-sync",
	"PUT /api/email-sync",
	"GET /api/email-sync/labels",
	"POST /api/email-sync/run",
	"POST /api/email-sync/sync-now",
	"GET /api/contacts/{id}/emails",
	"GET /api/emails/{mailbox}/{id}",
	"GET /api/meetings/sync",
	"PUT /api/meetings/sync",
	"POST /api/meetings/run",
	"POST /api/meetings/sync-now",
	"GET /api/meetings",
	"GET /api/meetings/{id}",
	"PATCH /api/meetings/{id}",
	"POST /api/meetings/{id}/digest",
	"GET /api/insights",
	"PUT /api/insights/{id}",
	"POST /api/insights/{id}/deal",
	"GET /api/ai-columns",
	"PUT /api/ai-columns/{entity}/{key}",
	"DELETE /api/ai-columns/{entity}/{key}",
	"POST /api/ai-columns/{entity}/{key}/fill",
	"POST /api/ai-columns/{entity}/{key}/cells/{id}",
	"POST /api/ai-columns/run",
}

// removedPrefixes are the paths no discovery document may mention anywhere.
var removedPrefixes = []string{
	"/api/integrations", "/api/email-sync", "/emails", "/api/meetings", "/api/insights", "/api/ai-columns",
}

var pathParam = regexp.MustCompile(`\{[^}]*\}|:[A-Za-z_]+`)

// routeKey is "METHOD /path" with every path parameter written {}, so Go's
// {id}, OpenAPI's {id} and Hono's :id compare equal.
func routeKey(method, path string) string {
	return strings.ToUpper(method) + " " + pathParam.ReplaceAllString(path, "{}")
}

func TestRemovedRoutesAreGone(t *testing.T) {
	dbtest.Setup(t)
	api := New(&App{})
	ids := strings.NewReplacer("{id}", "x1", "{mailbox}", "inbox", "{entity}", "contact", "{key}", "score")
	for _, route := range removedRoutes {
		method, path, _ := strings.Cut(route, " ")
		path = ids.Replace(path)
		body := ""
		if method != "GET" && method != "DELETE" {
			body = `{}`
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		want := `{"error":"not_found","message":"No route for ` + method + " " + path + ` on this app.",` +
			`"hint":"GET /llms.txt lists every endpoint; GET /api/openapi.json has full request/response schemas."}`
		if w.Code != http.StatusNotFound || w.Body.String() != want {
			t.Errorf("%s %s = %d %.200s; want 404 not_found", method, path, w.Code, w.Body.String())
		}
	}
}

// TestDiscoveryListsExactlyTheRoutes checks the served documents against the
// router: llms.txt and the not-found endpoint list name every registered
// route and nothing else; the OpenAPI document (which covers only the routes
// TS declared with zod schemas) names only registered ones. None mentions a
// removed route.
func TestDiscoveryListsExactlyTheRoutes(t *testing.T) {
	api := New(&App{})
	registered := map[string]bool{}
	for _, p := range api.(*web.Router).Patterns() {
		method, path, _ := strings.Cut(p, " ")
		if path == "/api/openapi.json" || path == "/llms.txt" {
			continue // discovery's own routes, which the documents don't list
		}
		registered[routeKey(method, path)] = true
	}
	for _, route := range removedRoutes {
		method, path, _ := strings.Cut(route, " ")
		if registered[routeKey(method, path)] {
			t.Errorf("removed route %s is registered", route)
		}
	}

	get := func(path string, header map[string]string) string {
		r := httptest.NewRequest("GET", path, nil)
		for k, v := range header {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w.Body.String()
	}
	llms := get("/llms.txt", nil)
	openapi := get("/api/openapi.json", nil)
	notFound := get("/api/nope", map[string]string{"X-Clawnify-Caller": "user"})

	for name, doc := range map[string]string{"llms.txt": llms, "openapi.json": openapi, "the endpoint list": notFound} {
		for _, p := range removedPrefixes {
			if strings.Contains(doc, p) {
				t.Errorf("%s mentions %s", name, p)
			}
		}
	}

	// llms.txt: one "- `METHOD /path`" line per route.
	var llmsRoutes []string
	for _, m := range regexp.MustCompile("(?m)^- `([A-Z]+) (/[^`]*)`").FindAllStringSubmatch(llms, -1) {
		llmsRoutes = append(llmsRoutes, routeKey(m[1], m[2]))
	}
	sameRoutes(t, "llms.txt", llmsRoutes, registered)

	var nf struct {
		Endpoints []struct{ Method, Path string }
	}
	if err := json.Unmarshal([]byte(notFound), &nf); err != nil {
		t.Fatalf("not-found body: %v", err)
	}
	var listed []string
	for _, e := range nf.Endpoints {
		listed = append(listed, routeKey(e.Method, e.Path))
	}
	sameRoutes(t, "the endpoint list", listed, registered)

	var doc struct {
		Info  struct{ Title, Description string }
		Paths map[string]map[string]json.RawMessage
	}
	if err := json.Unmarshal([]byte(openapi), &doc); err != nil {
		t.Fatalf("openapi.json: %v", err)
	}
	if doc.Info.Title != "OpenCRM" || doc.Info.Description != "A CRM with companies, contacts, and deal pipeline management." {
		t.Errorf("openapi.json info = %+v", doc.Info)
	}
	if len(doc.Paths) == 0 {
		t.Error("openapi.json has no paths")
	}
	for path, ops := range doc.Paths {
		for method := range ops {
			if k := routeKey(method, path); !registered[k] {
				t.Errorf("openapi.json lists %s, which is not a route", k)
			}
		}
	}
}

// sameRoutes reports every difference between a document's routes and the router's.
func sameRoutes(t *testing.T, name string, listed []string, registered map[string]bool) {
	t.Helper()
	seen := map[string]bool{}
	for _, k := range listed {
		if seen[k] {
			t.Errorf("%s lists %s twice", name, k)
		}
		seen[k] = true
		if !registered[k] {
			t.Errorf("%s lists %s, which is not a route", name, k)
		}
	}
	var missing []string
	for k := range registered {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	for _, k := range missing {
		t.Errorf("%s doesn't list %s", name, k)
	}
}
