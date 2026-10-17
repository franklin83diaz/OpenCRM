package web_test

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

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

// What the API writes is only ever what its Content-Type says: a browser
// doesn't sniff a JSON or text answer into something it would run.
func TestAnswersAreNotSniffed(t *testing.T) {
	for name, write := range map[string]func(w http.ResponseWriter) error{
		"JSON": func(w http.ResponseWriter) error { return web.JSON(w, 200, map[string]any{"x": "<script>"}) },
		"Text": func(w http.ResponseWriter) error {
			return web.Text(w, 404, "text/plain; charset=UTF-8", "404 Not Found")
		},
	} {
		rec := httptest.NewRecorder()
		if err := write(rec); err != nil {
			t.Fatal(err)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", name, got)
		}
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

func TestZodErrorKeyOrder(t *testing.T) {
	// zod built some issues in their own key order (ZodEnum's), which
	// JSON.stringify kept; Keys says it, and leaves the rest out.
	rec := httptest.NewRecorder()
	_ = web.ZodError(rec, []web.Issue{
		{Code: "invalid_enum_value", Received: "<b>", Options: []string{"a", "b"}, Path: []any{"link"}, Message: "Invalid enum value. Expected 'a' | 'b', received '<b>'",
			Keys: []string{"received", "code", "options", "path", "message"}},
		{Code: "invalid_type", Expected: "'a' | 'b'", Received: "array", Path: []any{"when"}, Message: "Expected 'a' | 'b', received array",
			Keys: []string{"expected", "received", "code", "path", "message"}},
		{Code: "invalid_enum_value", Received: "", Options: []string{"a"}, Message: "m", Keys: []string{"received", "code", "options", "path", "message"}},
	})
	want := `{"success":false,"error":{"issues":[` +
		`{"received":"<b>","code":"invalid_enum_value","options":["a","b"],"path":["link"],"message":"Invalid enum value. Expected 'a' | 'b', received '<b>'"},` +
		`{"expected":"'a' | 'b'","received":"array","code":"invalid_type","path":["when"],"message":"Expected 'a' | 'b', received array"},` +
		`{"received":"","code":"invalid_enum_value","options":["a"],"path":[],"message":"m"}],"name":"ZodError"}}`
	if rec.Body.String() != want {
		t.Errorf("ZodError key order\n got  %s\n want %s", rec.Body.String(), want)
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
	as := func(caller string, header map[string]string) string {
		r := httptest.NewRequest("GET", "/api/nope", nil)
		for k, v := range header {
			r.Header.Set(k, v)
		}
		if caller != "" {
			r = web.WithIdentity(r, caller, &web.User{ID: "u1", Email: "ada@example.com"})
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Errorf("GET /api/nope as %q = %d", caller, w.Code)
		}
		return w.Body.String()
	}
	for _, caller := range []string{"user", "api"} {
		body := as(caller, nil)
		if !strings.HasSuffix(body, `]}`) ||
			!strings.Contains(body, notFoundHint+`,"endpoints":[{"method":"GET","path":"/api/stats","summary":"Get dashboard statistics"},`) {
			t.Errorf("GET /api/nope as %s = %.400s", caller, body)
		}
	}
	// Nobody signed in, whatever the old Clawnify headers claim.
	for name, body := range map[string]string{
		"anonymous":         as("", nil),
		"X-Clawnify-Caller": as("", map[string]string{"X-Clawnify-Caller": "user"}),
	} {
		if strings.Contains(body, "endpoints") {
			t.Errorf("%s sees endpoints: %.200s", name, body)
		}
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
	want := []string{"GET /api/openapi.json", "GET /llms.txt", "GET /api/fails", "GET /api/panics", "GET /api/items/{id}"}
	if got := app.(*web.Router).Patterns(); !reflect.DeepEqual(got, want) {
		t.Errorf("Patterns = %q; want %q", got, want)
	}
}

// Every request body is capped at 25 MiB. One past the cap reads as a body
// that isn't JSON, whatever its first 25 MiB say, so a route answers it as it
// answers an unparseable body; and no more of it is read. A body at the cap
// reads in full.
func TestBodyCap(t *testing.T) {
	if web.MaxBody != 25<<20 {
		t.Errorf("MaxBody = %d; want 25 MiB", web.MaxBody)
	}
	type read struct {
		body     any
		jsonErr  error
		raw      int
		rawFirst bool
	}
	var got read
	app := newApp(func(rt *web.Router) {
		rt.Handle("POST /api/x", func(w http.ResponseWriter, r *http.Request) error {
			got = read{}
			got.body = web.Body(r)
			_, got.jsonErr = web.JSONBody(r)
			got.raw = len(web.RawBody(r))
			return web.JSON(w, 200, struct{}{})
		})
		rt.Handle("POST /api/raw", func(w http.ResponseWriter, r *http.Request) error {
			got = read{rawFirst: true, raw: len(web.RawBody(r))}
			got.body = web.Body(r)
			return web.JSON(w, 200, struct{}{})
		})
	})
	post := func(path, body string) {
		t.Helper()
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, r)
		if rec.Code != 200 {
			t.Fatalf("POST %s = %d %s", path, rec.Code, rec.Body.String())
		}
	}

	atCap := `{"a":1}` + strings.Repeat(" ", web.MaxBody-7)
	post("/api/x", atCap)
	if m, _ := got.body.(map[string]any); m["a"] != 1.0 || got.jsonErr != nil || got.raw != web.MaxBody {
		t.Errorf("a body at the cap: %#v, %v, %d bytes", got.body, got.jsonErr, got.raw)
	}

	overCap := atCap + " "
	for _, path := range []string{"/api/x", "/api/raw"} {
		post(path, overCap)
		if got.body != nil || got.raw != 0 {
			t.Errorf("%s: a body past the cap reads as %#v, %d raw bytes; want nil, 0", path, got.body, got.raw)
		}
		if !got.rawFirst && !errors.Is(got.jsonErr, web.ErrBodyTooLarge) {
			t.Errorf("%s: JSONBody past the cap: %v", path, got.jsonErr)
		}
	}
	if web.ErrBodyTooLarge.Error() != "Request body too large: the limit is 25 MiB" {
		t.Errorf("ErrBodyTooLarge = %q", web.ErrBodyTooLarge)
	}
}

func TestIdentity(t *testing.T) {
	// The X-Clawnify-* headers Clawnify's proxy used to send name nobody now.
	r := httptest.NewRequest("GET", "/api/stats", nil)
	r.Header.Set("X-Clawnify-Caller", "user")
	r.Header.Set("X-Clawnify-User-Id", "u1")
	r.Header.Set("X-Clawnify-User-Email", "ada@example.com")
	if got := web.Caller(r); got != "public" {
		t.Errorf("Caller(headers) = %q", got)
	}
	if u := web.CurrentUser(r); u != nil {
		t.Errorf("CurrentUser(headers) = %#v", u)
	}

	// Who is calling is what the auth middleware put on the context.
	u := &web.User{ID: "u1", Email: "ada@example.com", Name: "Ada", Role: "admin"}
	signed := web.WithIdentity(r, "api", u)
	if got := web.Caller(signed); got != "api" {
		t.Errorf("Caller = %q", got)
	}
	if got := web.CurrentUser(signed); got != u {
		t.Errorf("CurrentUser = %#v", got)
	}
	if caller, got := web.IdentityFrom(signed.Context()); caller != "api" || got != u {
		t.Errorf("IdentityFrom = %q, %#v", caller, got)
	}
	if caller, got := web.IdentityFrom(r.Context()); caller != "" || got != nil {
		t.Errorf("IdentityFrom(no identity) = %q, %#v", caller, got)
	}
	if web.Caller(r) != "public" {
		t.Error("WithIdentity changed the original request")
	}
	rec := httptest.NewRecorder()
	web.JSON(rec, 200, u)
	if rec.Body.String() != `{"id":"u1","email":"ada@example.com","name":"Ada","role":"admin"}` {
		t.Errorf("User JSON = %s", rec.Body.String())
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

func TestZodErrorNonFiniteStillNull(t *testing.T) {
	rec := httptest.NewRecorder()
	_ = web.ZodError(rec, []web.Issue{{Code: "too_big", Maximum: math.Inf(1), Message: "m", Keys: []string{"code", "maximum", "path", "message"}}})
	if want := `{"success":false,"error":{"issues":[{"code":"too_big","maximum":null,"path":[],"message":"m"}],"name":"ZodError"}}`; rec.Body.String() != want {
		t.Errorf("got %s", rec.Body.String())
	}
}
