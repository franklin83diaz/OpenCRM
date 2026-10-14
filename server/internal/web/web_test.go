package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opencrm/internal/web"
	"opencrm/internal/web/discovery"
)

const notFoundHint = `"hint":"GET /llms.txt lists every endpoint; GET /api/openapi.json has full request/response schemas."`

func TestJSONDoesNotEscapeHTML(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := web.JSON(rec, 201, map[string]any{"name": "Cedar & Co <x>"}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 201 || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got := rec.Body.String(); got != `{"name":"Cedar & Co <x>"}` {
		t.Errorf("body = %q", got)
	}
}

// JSON.stringify writes NaN and ±Infinity as null; encoding/json refuses them.
func TestJSONWritesNonFiniteNumbersAsNull(t *testing.T) {
	type inner struct {
		F32 float32 `json:"f32"`
		Any any     `json:"any,omitempty"`
	}
	type outer struct {
		X      float64            `json:"x"`
		Ptr    *float64           `json:"ptr"`
		List   []float64          `json:"list"`
		Inner  inner              `json:"inner"`
		Nested map[string]any     `json:"nested"`
		Raw    json.RawMessage    `json:"raw"`
		Text   string             `json:"text"`
		ByKey  map[string]float64 `json:"byKey"`
	}
	nan, inf := math.NaN(), math.Inf(1)
	cases := []struct {
		v    any
		want string
	}{
		{map[string]any{"a": nan, "b": []any{inf, 1.0}}, `{"a":null,"b":[null,1]}`},
		{nan, `null`},
		{[]float64{math.Inf(-1), -1.5e-300, 2}, `[null,-1.5e-300,2]`},
		{outer{
			X: nan, Ptr: &inf, List: []float64{1, nan},
			Inner:  inner{F32: float32(math.Inf(-1)), Any: []any{map[string]any{"deep": nan}}},
			Nested: map[string]any{"n": map[string]any{"m": []any{inf, "NaN & <x>"}}},
			Raw:    json.RawMessage(`{"kept":1}`),
			Text:   "-1e+308 null",
			ByKey:  map[string]float64{"k": nan},
		}, `{"x":null,"ptr":null,"list":[1,null],"inner":{"f32":null,"any":[{"deep":null}]},` +
			`"nested":{"n":{"m":[null,"NaN & <x>"]}},"raw":{"kept":1},"text":"-1e+308 null","byKey":{"k":null}}`},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		if err := web.JSON(rec, 200, c.v); err != nil {
			t.Errorf("JSON(%#v) error: %v", c.v, err)
			continue
		}
		if got := rec.Body.String(); got != c.want {
			t.Errorf("JSON(%#v)\n got %s\nwant %s", c.v, got, c.want)
		}
	}
}

func TestBodyAndObject(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/views", strings.NewReader("not json"))
	if got := web.Object(web.Body(r)); got == nil || len(got) != 0 {
		t.Errorf("Object(Body(not json)) = %#v; want empty map", got)
	}
	if got := web.RawBody(r); got != "not json" {
		t.Errorf("RawBody after Body = %q", got)
	}

	r = httptest.NewRequest("POST", "/api/views", strings.NewReader(`{"name":"Zoë","n":2}`))
	if got := web.RawBody(r); got != `{"name":"Zoë","n":2}` {
		t.Errorf("RawBody = %q", got)
	}
	obj := web.Object(web.Body(r))
	if obj["name"] != "Zoë" || obj["n"] != 2.0 {
		t.Errorf("Object(Body) = %#v", obj)
	}

	r = httptest.NewRequest("POST", "/api/views", nil)
	if got := web.Body(r); got != nil {
		t.Errorf("Body(empty) = %#v; want nil", got)
	}
	if got := web.Object([]any{1.0}); got == nil || len(got) != 0 {
		t.Errorf("Object(array) = %#v; want empty map", got)
	}
}

func TestZodError(t *testing.T) {
	rec := httptest.NewRecorder()
	err := web.ZodError(rec, []web.Issue{{
		Code: "invalid_type", Expected: "string", Received: "undefined",
		Path: []any{"name"}, Message: "Required",
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"success":false,"error":{"issues":[{"code":"invalid_type","expected":"string","received":"undefined","path":["name"],"message":"Required"}],"name":"ZodError"}}`
	if rec.Code != 400 || rec.Body.String() != want {
		t.Errorf("ZodError = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	yes := true
	_ = web.ZodError(rec, []web.Issue{{Code: "too_small", Minimum: 1, Type: "string", Inclusive: &yes, Message: "Name is required"}})
	want = `{"success":false,"error":{"issues":[{"code":"too_small","path":[],"message":"Name is required","minimum":1,"type":"string","inclusive":true}],"name":"ZodError"}}`
	if rec.Body.String() != want {
		t.Errorf("ZodError too_small = %s", rec.Body.String())
	}
}

func newApp(routes func(rt *web.Router)) http.Handler {
	rt := web.NewRouter()
	discovery.Mount(rt)
	if routes != nil {
		routes(rt)
	}
	return rt
}

func serve(h http.Handler, method, path string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestRouterNotFound(t *testing.T) {
	app := newApp(func(rt *web.Router) {
		rt.Handle("GET /api/companies", func(w http.ResponseWriter, r *http.Request) error {
			return web.JSON(w, 200, map[string]any{"companies": []any{}})
		})
	})

	rec := serve(app, "GET", "/api/companies", nil)
	if rec.Code != 200 || rec.Body.String() != `{"companies":[]}` {
		t.Errorf("GET /api/companies = %d %s", rec.Code, rec.Body.String())
	}

	// A path that exists for other methods is still "no route", as in Hono.
	rec = serve(app, "PATCH", "/api/companies", nil)
	want := `{"error":"not_found","message":"No route for PATCH /api/companies on this app.",` + notFoundHint + `}`
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/json" || rec.Body.String() != want {
		t.Errorf("PATCH /api/companies = %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}

	rec = serve(app, "GET", "/api/nope", nil)
	want = `{"error":"not_found","message":"No route for GET /api/nope on this app.",` + notFoundHint + `}`
	if rec.Code != 404 || rec.Body.String() != want {
		t.Errorf("GET /api/nope = %d %s", rec.Code, rec.Body.String())
	}

	// Paths Go's mux would redirect (double slash) are not routes either.
	rec = serve(app, "GET", "/api//companies", nil)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"No route for GET /api//companies on this app."`) {
		t.Errorf("GET /api//companies = %d %s", rec.Code, rec.Body.String())
	}

	rec = serve(app, "GET", "/nope", nil)
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "text/plain; charset=UTF-8" || rec.Body.String() != "404 Not Found" {
		t.Errorf("GET /nope = %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func TestNotFoundListsEndpointsForSignedInCallers(t *testing.T) {
	app := newApp(nil)
	rec := serve(app, "GET", "/api/nope", map[string]string{"X-Clawnify-Caller": "user"})
	body := rec.Body.String()
	if rec.Code != 404 || !strings.HasSuffix(body, `]}`) ||
		!strings.Contains(body, notFoundHint+`,"endpoints":[{"method":"GET","path":"/api/stats","summary":"Get dashboard statistics"},`) {
		t.Errorf("GET /api/nope as user = %d %.400s", rec.Code, body)
	}
	rec = serve(app, "GET", "/api/nope", map[string]string{"X-Clawnify-Caller": "public"})
	if strings.Contains(rec.Body.String(), "endpoints") {
		t.Errorf("public caller sees endpoints: %.200s", rec.Body.String())
	}
}

func TestDiscovery(t *testing.T) {
	app := newApp(nil)
	rec := serve(app, "GET", "/llms.txt", nil)
	if rec.Code != 200 ||
		rec.Header().Get("Content-Type") != "text/markdown; charset=utf-8" ||
		rec.Header().Get("Cache-Control") != "public, max-age=300" ||
		!strings.HasPrefix(rec.Body.String(), "# OpenCRM\n") {
		t.Errorf("GET /llms.txt = %d %v %.40q", rec.Code, rec.Header(), rec.Body.String())
	}
	rec = serve(app, "GET", "/api/openapi.json", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" ||
		!strings.HasPrefix(rec.Body.String(), `{"openapi":"3.1.0","info":{"title":"OpenCRM"`) {
		t.Errorf("GET /api/openapi.json = %d %v %.60q", rec.Code, rec.Header(), rec.Body.String())
	}
}

func TestRouterErrors(t *testing.T) {
	app := newApp(func(rt *web.Router) {
		rt.Handle("GET /api/fails", func(w http.ResponseWriter, r *http.Request) error {
			return errors.New("boom")
		})
		rt.Handle("GET /api/panics", func(w http.ResponseWriter, r *http.Request) error {
			panic("boom")
		})
		rt.Handle("GET /api/items/{id}", func(w http.ResponseWriter, r *http.Request) error {
			return web.JSON(w, 200, map[string]any{"id": r.PathValue("id")})
		})
	})
	for _, path := range []string{"/api/fails", "/api/panics"} {
		rec := serve(app, "GET", path, nil)
		if rec.Code != 500 || rec.Body.String() != "Internal Server Error" ||
			rec.Header().Get("Content-Type") != "text/plain; charset=UTF-8" {
			t.Errorf("GET %s = %d %q %q", path, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
	}
	rec := serve(app, "GET", "/api/items/a%20b", nil)
	if rec.Code != 200 || rec.Body.String() != `{"id":"a b"}` {
		t.Errorf("GET /api/items/a%%20b = %d %s", rec.Code, rec.Body.String())
	}
}

func TestIdentity(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/stats", nil)
	if got := web.Caller(r); got != "public" {
		t.Errorf("Caller(no header) = %q", got)
	}
	r.Header.Set("X-Clawnify-Caller", "agent")
	if got := web.Caller(r); got != "agent" {
		t.Errorf("Caller(agent) = %q", got)
	}
	r.Header.Set("X-Clawnify-Caller", "hacker")
	if got := web.Caller(r); got != "public" {
		t.Errorf("Caller(hacker) = %q", got)
	}

	if u := web.CurrentUser(r); u != nil {
		t.Errorf("CurrentUser(no id) = %#v", u)
	}
	r.Header.Set("X-Clawnify-User-Id", " u1 ")
	r.Header.Set("X-Clawnify-User-Name", "Ana  María López")
	r.Header.Set("X-Clawnify-User-Email", "   ")
	u := web.CurrentUser(r)
	if u == nil || u.ID != "u1" || u.Email != nil || u.AvatarURL != nil ||
		*u.Name != "Ana  María López" || *u.FirstName != "Ana" || *u.LastName != "María López" {
		t.Errorf("CurrentUser = %#v", u)
	}

	if got := web.OrgID(r); got != "" {
		t.Errorf("OrgID(no header) = %q", got)
	}
	r.Header.Set("X-Clawnify-Org-Id", "org_1")
	if got := web.OrgID(r); got != "org_1" {
		t.Errorf("OrgID = %q", got)
	}
}

func TestOrigin(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/stats", nil)
	r.Host = "localhost:8080"
	if got := web.Origin(r); got != "http://localhost:8080" {
		t.Errorf("Origin = %q", got)
	}
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "crm.example.com")
	if got := web.Origin(r); got != "https://crm.example.com" {
		t.Errorf("Origin(forwarded) = %q", got)
	}
}

func TestBackgroundOutlivesTheRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "/api/stats", nil).WithContext(ctx)
	done := make(chan error, 1)
	release := make(chan struct{})
	web.Background(r, func(ctx context.Context) {
		<-release
		done <- ctx.Err()
	})
	cancel() // the response has gone out
	close(release)
	if !web.Drain(time.Second) {
		t.Fatal("Drain timed out")
	}
	if err := <-done; err != nil {
		t.Errorf("background ctx was canceled with the request: %v", err)
	}
}

// Drain must tolerate Background starting work while it waits — including
// from an idle state, which is a misuse of a bare sync.WaitGroup. Run with -race.
func TestBackgroundDuringDrain(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/stats", nil)
	var ran atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); web.Drain(time.Second) }()
		go func() { defer wg.Done(); web.Background(r, func(context.Context) { ran.Add(1) }) }()
	}
	wg.Wait()
	if !web.Drain(time.Second) || ran.Load() != 100 {
		t.Fatalf("after drain, %d of 100 background jobs ran", ran.Load())
	}

	// Work started while Drain waits is waited for too.
	release := make(chan struct{})
	web.Background(r, func(context.Context) { <-release; ran.Add(1) })
	drained := make(chan bool)
	go func() { drained <- web.Drain(5 * time.Second) }()
	for i := 0; i < 10; i++ {
		web.Background(r, func(context.Context) { ran.Add(1) })
	}
	close(release)
	if !<-drained || ran.Load() != 111 {
		t.Fatalf("Drain returned before all work finished: %d of 111 ran", ran.Load())
	}
}
