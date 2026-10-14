package clawnify

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var bg = context.Background()

func TestEnqueueRequestShape(t *testing.T) {
	var got map[string]any
	var auth, path, method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path, method = r.Header.Get("Authorization"), r.URL.Path, r.Method
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"id":"job-1"}`))
	}))
	defer srv.Close()
	runAt := time.Date(2026, 10, 14, 16, 10, 0, 0, time.UTC)
	job, err := Enqueue(bg, Env{Token: "t", QueueURL: srv.URL + "/queue/"}, EnqueueOptions{
		TargetURL: "https://x/y", Payload: map[string]any{"a": 1}, RunAt: runAt, IdempotencyKey: "k", MaxAttempts: 3,
	})
	if err != nil || job.ID != "job-1" {
		t.Fatalf("job=%v err=%v", job, err)
	}
	if auth != "Bearer t" || method != "POST" || path != "/queue/enqueue" {
		t.Fatalf("auth=%q method=%q path=%q", auth, method, path)
	}
	want := map[string]any{"target_url": "https://x/y", "payload": map[string]any{"a": float64(1)}, "run_at": "2026-10-14T16:10:00.000Z", "idempotency_key": "k", "max_attempts": float64(3)}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("body %v, want %v", got, want)
	}
}

func TestEnqueueOmitsUnsetFields(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"id":"j"}`))
	}))
	defer srv.Close()
	if _, err := Enqueue(bg, Env{Token: "t", QueueURL: srv.URL}, EnqueueOptions{TargetURL: "u"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("body %v", got)
	}
}

func TestQueueErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(502)
			return
		}
		w.WriteHeader(400)
		w.Write([]byte(`{"detail":"bad"}`))
	}))
	defer srv.Close()
	env := Env{Token: "t", QueueURL: srv.URL}
	if _, err := Enqueue(bg, env, EnqueueOptions{TargetURL: "u"}); err == nil || err.Error() != "bad" {
		t.Fatalf("err=%v", err)
	}
	if err := CancelJob(bg, env, "j1"); err == nil || err.Error() != "queue service 502" {
		t.Fatalf("err=%v", err)
	}
	if _, err := Enqueue(bg, Env{}, EnqueueOptions{}); err == nil || err.Error() != "CLAWNIFY_TOKEN is not set — the queue service is unavailable." {
		t.Fatalf("err=%v", err)
	}
}

func jwksServer(t *testing.T, keys ...*ecdsa.PrivateKey) *httptest.Server {
	type entry map[string]string
	var list []entry
	for i, k := range keys {
		list = append(list, entry{"kty": "EC", "crv": "P-256", "kid": fmt.Sprintf("k%d", i),
			"x": base64.RawURLEncoding.EncodeToString(k.X.FillBytes(make([]byte, 32))),
			"y": base64.RawURLEncoding.EncodeToString(k.Y.FillBytes(make([]byte, 32)))})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": list})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sign(t *testing.T, k *ecdsa.PrivateKey, ts int64, body string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d.%s", ts, body)))
	r, s, err := ecdsa.Sign(rand.Reader, k, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...))
}

func TestVerifyDelivery(t *testing.T) {
	k0, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	k1, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	srv := jwksServer(t, k0, k1)
	old := jwksURL
	jwksURL = srv.URL
	defer func() { jwksURL = old }()

	now := time.Now().Unix()
	ts := fmt.Sprint(now)
	body := `{"hello":"é"}`
	sig := sign(t, k1, now, body)
	if !VerifyDelivery(bg, body, sig, ts, "k1") {
		t.Fatal("valid delivery rejected")
	}
	if VerifyDelivery(bg, body, sig, ts, "k0") {
		t.Fatal("signature checked against the wrong key")
	}
	// An unknown key id falls back to the first key, as the TS client did.
	if !VerifyDelivery(bg, body, sign(t, k0, now, body), ts, "nope") {
		t.Fatal("fallback to first key failed")
	}
	if VerifyDelivery(bg, body+" ", sig, ts, "k1") {
		t.Fatal("tampered body accepted")
	}
	oldTS := now - 301
	if VerifyDelivery(bg, body, sign(t, k1, oldTS, body), fmt.Sprint(oldTS), "k1") {
		t.Fatal("301 s old accepted")
	}
	if VerifyDelivery(bg, body, "", ts, "k1") || VerifyDelivery(bg, body, sig, "abc", "k1") || VerifyDelivery(bg, body, "!!", ts, "k1") {
		t.Fatal("malformed input accepted")
	}
}

func TestVerifyDeliveryJWKSUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	old := jwksURL
	jwksURL = srv.URL
	defer func() { jwksURL = old }()
	if VerifyDelivery(bg, "b", "AAAA", fmt.Sprint(time.Now().Unix()), "") {
		t.Fatal("accepted without keys")
	}
}

func TestRenderMarkdown(t *testing.T) {
	var got map[string]any
	var auth string
	status := 200
	reply := `{"markdown":"# Hi","title":"T"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/markdown/render" {
			t.Errorf("path %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	defer srv.Close()
	env := Env{Token: "t", ServicesURL: srv.URL + "/"}
	page, err := RenderMarkdown(bg, env, "https://acme.com", 5000)
	if err != nil || page.Markdown != "# Hi" || auth != "Bearer t" {
		t.Fatalf("page=%v err=%v auth=%q", page, err, auth)
	}
	if got["url"] != "https://acme.com" || got["max_chars"] != float64(5000) || len(got) != 2 {
		t.Fatalf("body %v", got)
	}
	status, reply = 422, `{"error":"blocked","detail":"the page refused"}`
	if _, err := RenderMarkdown(bg, env, "u", 1); err == nil || err.Error() != "the page refused" {
		t.Fatalf("err=%v", err)
	}
	status, reply = 502, `<html>`
	if _, err := RenderMarkdown(bg, env, "u", 1); err == nil || err.Error() != "/markdown/render answered 502" {
		t.Fatalf("err=%v", err)
	}
	if _, err := RenderMarkdown(bg, Env{}, "u", 1); err == nil || err.Error() != "CLAWNIFY_TOKEN is not set, so Clawnify services are unavailable." {
		t.Fatalf("err=%v", err)
	}
}

// fakeBroker answers the three endpoints the remote API and the Worker bridge share.
type fakeBroker struct {
	srv     *httptest.Server
	headers http.Header
	execute map[string]any
	status  int
	granola *httptest.Server
	gAuth   string
	gURL    string
}

func newBroker(t *testing.T) *fakeBroker {
	b := &fakeBroker{status: 200}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.headers = r.Header.Clone()
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		switch r.URL.Path {
		case "/v1/connections/token":
			json.NewEncoder(w).Encode(map[string]any{"token": "tok-" + in["service"].(string)})
		case "/v1/connections/execute":
			w.WriteHeader(b.status)
			json.NewEncoder(w).Encode(b.execute)
		case "/v1/connections/connected":
			io.WriteString(w, `{"services":["gmail","granola"]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func TestRunThroughBridge(t *testing.T) {
	b := newBroker(t)
	c := NewConnections(Env{ConnectionsURL: b.srv.URL + "/", OrgID: "org", Token: "ignored"}, nil)
	b.execute = map[string]any{"data": map[string]any{"n": 1}, "error": nil, "successful": true}
	data, err := c.Run(bg, "gmail", "GMAIL_X", map[string]any{"a": 1})
	if err != nil || fmt.Sprint(data) != "map[n:1]" {
		t.Fatalf("data=%v err=%v", data, err)
	}
	// The bridge is the Worker's own: no credentials of ours go to it.
	if b.headers.Get("Authorization") != "" || b.headers.Get("x-org-id") != "" {
		t.Fatalf("headers %v", b.headers)
	}
	b.execute = map[string]any{"data": nil, "error": "nope", "successful": false}
	if _, err := c.Run(bg, "gmail", "GMAIL_X", nil); err == nil || err.Error() != "nope" {
		t.Fatalf("err=%v", err)
	}
	b.execute = map[string]any{"error": map[string]any{"code": 7}, "successful": false}
	if _, err := c.Run(bg, "gmail", "GMAIL_X", nil); err == nil || err.Error() != `{"code":7}` {
		t.Fatalf("err=%v", err)
	}
	b.execute = map[string]any{"successful": false}
	if _, err := c.Run(bg, "gmail", "GMAIL_X", nil); err == nil || err.Error() != `gmail action "GMAIL_X" failed` {
		t.Fatalf("err=%v", err)
	}
	b.status, b.execute = 500, map[string]any{"error": "binding exploded"}
	if _, err := c.Run(bg, "gmail", "GMAIL_X", nil); err == nil || err.Error() != "binding exploded" {
		t.Fatalf("err=%v", err)
	}
	b.status, b.execute = 502, map[string]any{}
	if _, err := c.Run(bg, "gmail", "GMAIL_X", nil); err == nil || err.Error() != "connections 502" {
		t.Fatalf("err=%v", err)
	}
	if got := c.Connected(bg); fmt.Sprint(got) != "[gmail granola]" {
		t.Fatalf("connected %v", got)
	}
	if c.Token(bg, "granola") != "tok-granola" {
		t.Fatal("token")
	}
}

func TestRunWithTokenSendsCredentials(t *testing.T) {
	b := newBroker(t)
	c := NewConnections(Env{Token: "T", OrgID: "o1", APIURL: b.srv.URL}, nil)
	b.execute = map[string]any{"data": "ok"}
	data, err := c.Run(bg, "slack", "A", nil)
	if err != nil || data != "ok" {
		t.Fatalf("data=%v err=%v", data, err)
	}
	if b.headers.Get("Authorization") != "Bearer T" || b.headers.Get("x-org-id") != "o1" {
		t.Fatalf("headers %v", b.headers)
	}
	c = NewConnections(Env{Token: "T", APIURL: b.srv.URL}, nil)
	c.Connected(bg)
	if b.headers.Get("x-org-id") != "" {
		t.Fatal("x-org-id sent without an org")
	}
}

func TestNoBroker(t *testing.T) {
	const want = `"gmail" needs the Clawnify credentials binding or CLAWNIFY_TOKEN`
	for name, env := range map[string]Env{
		"empty": {},
		// The CREDENTIALS binding only brokers for a known org.
		"bridge without org": {ConnectionsURL: "http://127.0.0.1:1"},
	} {
		c := NewConnections(env, nil)
		if _, err := c.Run(bg, "gmail", "A", nil); err == nil || err.Error() != want {
			t.Fatalf("%s: err=%v", name, err)
		}
		if c.Connected(bg) != nil || c.Token(bg, "gmail") != "" {
			t.Fatalf("%s: brokered", name)
		}
	}
}

func TestConnectedNilOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	c := NewConnections(Env{ConnectionsURL: srv.URL, OrgID: "o"}, nil)
	if c.Connected(bg) != nil || c.Token(bg, "x") != "" {
		t.Fatal("failure not swallowed")
	}
}

func TestGetGranola(t *testing.T) {
	b := newBroker(t)
	var gotAuth, gotURL string
	status, reply, ct := 200, `{"notes":[1]}`, "text/plain"
	g := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotURL = r.Header.Get("authorization"), r.URL.RequestURI()
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	defer g.Close()
	hc := &http.Client{Transport: rewrite{host: strings.TrimPrefix(g.URL, "http://")}}
	c := NewConnections(Env{ConnectionsURL: b.srv.URL, OrgID: "o"}, hc)
	_ = b
	data, err := c.Get(bg, "granola", "/v1/notes", map[string]string{"cursor": "a b", "limit": "5"})
	if err != nil || fmt.Sprint(data) != "map[notes:[1]]" {
		t.Fatalf("data=%v err=%v", data, err)
	}
	if gotAuth != "Bearer tok-granola" || gotURL != "/v1/notes?cursor=a+b&limit=5" {
		t.Fatalf("auth=%q url=%q", gotAuth, gotURL)
	}
	status, reply = 404, "gone"
	if _, err := c.Get(bg, "granola", "v1/notes/1", nil); err == nil || err.Error() != "granola GET v1/notes/1 → 404: gone" {
		t.Fatalf("err=%v", err)
	}
	status, reply = 200, ""
	if data, err := c.Get(bg, "granola", "/x", nil); err != nil || data != nil {
		t.Fatalf("data=%v err=%v", data, err)
	}
	if _, err := NewConnections(Env{}, nil).Get(bg, "granola", "/x", nil); err == nil || !strings.HasPrefix(err.Error(), `"granola" has no usable credential`) {
		t.Fatalf("err=%v", err)
	}
	if _, err := c.Get(bg, "other", "/x", nil); err == nil || !strings.Contains(err.Error(), "not a registry-backed") {
		t.Fatalf("err=%v", err)
	}
}

// rewrite sends calls to any host but the broker's to a test server, so the
// registry's real base URL can be used. The broker's own host passes through.
type rewrite struct{ host string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "public-api.granola.ai" {
		req = req.Clone(req.Context())
		req.URL.Scheme, req.URL.Host = "http", r.host
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestErrText(t *testing.T) {
	long := strings.Repeat("x", 600)
	for _, c := range []struct {
		in   any
		want string
	}{
		{"boom", "boom"}, {"", `""`}, {"[object Object]", `"[object Object]"`}, {nil, "fb"},
		{map[string]any{}, "fb"}, {map[string]any{"a": "<&>"}, `{"a":"<&>"}`}, {[]any{1.0}, "[1]"},
		{map[string]any{"a": long}, `{"a":"` + long[:494]},
	} {
		if got := errText(c.in, "fb"); got != c.want {
			t.Errorf("errText(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEnvFromOS(t *testing.T) {
	t.Setenv("CLAWNIFY_TOKEN", "a")
	t.Setenv("CLAWNIFY_ORG_ID", "b")
	t.Setenv("CLAWNIFY_API_URL", "c")
	t.Setenv("CLAWNIFY_QUEUE_URL", "d")
	t.Setenv("CLAWNIFY_SERVICES_URL", "e")
	t.Setenv("CLAWNIFY_CONNECTIONS_URL", "f")
	t.Setenv("SLACK_CHANNEL", "g")
	if e := EnvFromOS(); e != (Env{"a", "b", "c", "d", "e", "f", "g"}) {
		t.Fatalf("%+v", e)
	}
}
