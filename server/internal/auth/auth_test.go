package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"opencrm/internal/auth"
	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
	"opencrm/internal/web"
)

const unauthorized = `{"error":"unauthorized","message":"Sign in to use the CRM."}`
const crossSite = `{"error":"Cross-site request refused"}`

func TestPasswords(t *testing.T) {
	ctx := context.Background()
	hash, err := auth.HashPassword(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$2a$12$") {
		t.Errorf("hash %q is not bcrypt at cost 12", hash)
	}
	check := func(hash, pw string) bool {
		ok, err := auth.CheckPassword(ctx, hash, pw)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !check(hash, "correct horse") {
		t.Error("the right password doesn't check")
	}
	if check(hash, "correct horsE") || check(hash, "") {
		t.Error("a wrong password checks")
	}
	// No account: the comparison still runs (against a dummy hash) and fails.
	if check("", "correct horse") {
		t.Error("an empty hash checks")
	}

	// bcrypt only reads 72 bytes: a longer password would be truncated
	// silently, so it is refused instead; so is one under 8.
	for _, pw := range []string{"", "short77", strings.Repeat("x", 73), strings.Repeat("é", 37)} {
		if _, err := auth.HashPassword(ctx, pw); err == nil {
			t.Errorf("HashPassword(%d bytes) accepted", len(pw))
		}
	}
	long := strings.Repeat("x", 72)
	h72, err := auth.HashPassword(ctx, long)
	if err != nil {
		t.Fatalf("72 bytes refused: %v", err)
	}
	if check(h72, long+"y") {
		t.Error("a 73-byte password matches its 72-byte prefix")
	}
	for pw, want := range map[string]bool{"1234567": false, "12345678": true, strings.Repeat("x", 72): true, strings.Repeat("x", 73): false, "ñññññññ": true /* 14 bytes */} {
		if got := auth.ValidPassword(pw); got != want {
			t.Errorf("ValidPassword(%q) = %v", pw, got)
		}
	}
}

func TestNewSecret(t *testing.T) {
	tok, hash := auth.NewSecret("crm_")
	if !regexp.MustCompile(`^crm_[A-Za-z0-9_-]{43}$`).MatchString(tok) {
		t.Errorf("token %q: want crm_ + 32 bytes base64url", tok)
	}
	sum := sha256.Sum256([]byte(tok))
	if hash != hex.EncodeToString(sum[:]) || auth.HashSecret(tok) != hash {
		t.Errorf("hash %q is not the hex SHA-256 of the token", hash)
	}
	s1, _ := auth.NewSecret("")
	s2, _ := auth.NewSecret("")
	if len(s1) != 43 || s1 == s2 {
		t.Errorf("session secrets %q, %q", s1, s2)
	}
}

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	l := auth.NewLimiter(func() time.Time { return now })

	// 10 attempts a minute from one address, whatever the email.
	for i := 0; i < 10; i++ {
		if !l.Allow("1.1.1.1", "a"+string(rune('a'+i))+"@x") {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if l.Allow("1.1.1.1", "new@x") {
		t.Error("11th attempt in a minute allowed")
	}
	if !l.Allow("2.2.2.2", "new@x") {
		t.Error("another address is refused")
	}
	now = now.Add(61 * time.Second)
	if !l.Allow("1.1.1.1", "new2@x") {
		t.Error("still refused a minute later")
	}

	// 10 failures in 15 minutes for one email, from any addresses.
	for i := 0; i < 10; i++ {
		if !l.Allow("10.0.0."+string(rune('0'+i)), "ada@example.com") {
			t.Fatalf("failure %d refused", i+1)
		}
		now = now.Add(time.Minute)
	}
	if l.Allow("9.9.9.9", "ada@example.com") {
		t.Error("11th try for one email in 15 minutes allowed")
	}
	now = now.Add(6 * time.Minute) // the first failure is now 16 minutes old
	if !l.Allow("9.9.9.9", "ada@example.com") {
		t.Error("still refused once the oldest failure left the window")
	}

	// An IPv6 client holds a whole /64: its addresses share one count.
	for i := 0; i < 10; i++ {
		if !l.Allow(fmt.Sprintf("2001:db8:1:2::%x", i+1), fmt.Sprintf("v6-%d@x", i)) {
			t.Fatalf("IPv6 attempt %d refused", i+1)
		}
	}
	if l.Allow("2001:db8:1:2:ffff::1", "v6-new@x") {
		t.Error("another address in the same /64 is a new client")
	}
	if !l.Allow("2001:db8:1:3::1", "v6-new@x") || !l.Allow("10.1.1.1", "v6-new2@x") {
		t.Error("another /64 (or IPv4 address) is refused")
	}

	// Changing a password has its own count per account: 10 tries in 15
	// minutes, apart from the logins' (someone locking ada's email out of
	// signing in can't stop her, signed in, from changing her password).
	for i := 0; i < 10; i++ {
		if !l.AllowPasswordChange("u-ada") {
			t.Fatalf("password change %d refused", i+1)
		}
	}
	if l.AllowPasswordChange("u-ada") {
		t.Error("11th password change try in 15 minutes allowed")
	}
	if !l.AllowPasswordChange("u-bo") {
		t.Error("another account's password change refused")
	}
	l.PasswordChanged("u-ada")
	if !l.AllowPasswordChange("u-ada") {
		t.Error("a change doesn't forget the tries")
	}
	for i := 0; i < 10; i++ {
		l.Allow("6.6.6."+string(rune('0'+i)), "locked@example.com")
	}
	if l.Allow("5.5.5.5", "locked@example.com") || !l.AllowPasswordChange("u-locked") {
		t.Error("the login and password-change counts are not apart")
	}

	// A success forgets the email's failures.
	for i := 0; i < 9; i++ {
		l.Allow("8.8.8."+string(rune('0'+i)), "bo@example.com")
	}
	l.Succeeded("bo@example.com")
	for i := 0; i < 10; i++ {
		if !l.Allow("7.7.7."+string(rune('0'+i)), "bo@example.com") {
			t.Fatalf("after a success, attempt %d refused", i+1)
		}
	}
}

// fixture is a database with an admin (ada), a user (bo) and a disabled user
// (cy), and a handler behind the middleware that echoes who it sees.
type fixture struct {
	t   *testing.T
	ctx context.Context
	h   http.Handler
}

func setup(t *testing.T) *fixture {
	ctx := dbtest.Setup(t)
	for _, u := range [][]any{
		{"u-ada", "ada@example.com", "Ada", "admin", 0},
		{"u-bo", "bo@example.com", "Bo", "user", 0},
		{"u-cy", "cy@example.com", "Cy", "user", 1},
	} {
		if _, err := db.Run(ctx, "INSERT INTO users (id, email, name, role, password_hash, disabled) VALUES (?, ?, ?, ?, 'x', ?)", u...); err != nil {
			t.Fatal(err)
		}
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, kind := auth.FromContext(r.Context())
		email := "-"
		if u != nil {
			email = u.Email + "/" + u.Role
		}
		if u2 := web.CurrentUser(r); u2 != u {
			t.Errorf("web.CurrentUser = %v, auth.FromContext = %v", u2, u)
		}
		web.Text(w, 200, "text/plain", kind+" "+web.Caller(r)+" "+email)
	})
	return &fixture{t: t, ctx: ctx, h: auth.Middleware(next)}
}

func (f *fixture) session(userID, expires string) string {
	tok, hash := auth.NewSecret("")
	if _, err := db.Run(f.ctx, "INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, datetime('now', ?))", hash, userID, expires); err != nil {
		f.t.Fatal(err)
	}
	return tok
}

func (f *fixture) token(userID string) string {
	tok, hash := auth.NewSecret("crm_")
	if _, err := db.Run(f.ctx, "INSERT INTO api_tokens (id, user_id, name, token_hash, prefix) VALUES (?, ?, 'test', ?, ?)", "t-"+tok[4:12], userID, hash, tok[:12]); err != nil {
		f.t.Fatal(err)
	}
	return tok
}

func (f *fixture) do(method, path string, header map[string]string) (int, string) {
	r := httptest.NewRequest(method, path, strings.NewReader("{}"))
	r.Host = "crm.example.com"
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func cookie(tok string) map[string]string {
	return map[string]string{"Cookie": auth.CookieName + "=" + tok}
}
func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

func TestMiddlewareWithoutCredentials(t *testing.T) {
	f := setup(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/companies"}, {"POST", "/api/companies"}, {"GET", "/api/nope"}, {"GET", "/api"},
		{"GET", "/api/auth/me"}, {"GET", "/api/auth/login"}, {"POST", "/api/auth/logout"},
		{"POST", "/api/openapi.json"}, {"GET", "/api/openapi.json/"},
		// A path that decodes to the login route but is spelled otherwise is not it.
		{"POST", "/api/auth%2Flogin"}, {"POST", "/api/auth/login/"},
	} {
		code, body := f.do(c.method, c.path, nil)
		if code != 401 || body != unauthorized {
			t.Errorf("%s %s = %d %s", c.method, c.path, code, body)
		}
	}
	r := httptest.NewRequest("GET", "/api/companies", nil)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Header().Get("Content-Type") != "application/json" || w.Header().Get("WWW-Authenticate") != `Bearer realm="OpenCRM"` {
		t.Errorf("401 headers = %v", w.Header())
	}

	// Public: the login itself, the OpenAPI document, llms.txt and non-API paths.
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/auth/login"}, {"GET", "/api/openapi.json"}, {"HEAD", "/api/openapi.json"},
		{"GET", "/llms.txt"}, {"GET", "/"}, {"GET", "/assets/x.js"},
	} {
		if code, body := f.do(c.method, c.path, nil); code != 200 || body != " public -" {
			t.Errorf("%s %s = %d %q, want it passed through anonymously", c.method, c.path, code, body)
		}
	}
	// X-Clawnify-* headers are nobody.
	code, _ := f.do("GET", "/api/companies", map[string]string{"X-Clawnify-Caller": "user", "X-Clawnify-User-Id": "u-ada", "X-Clawnify-User-Email": "ada@example.com"})
	if code != 401 {
		t.Errorf("X-Clawnify headers = %d, want 401", code)
	}
}

func TestMiddlewareSessions(t *testing.T) {
	f := setup(t)
	ada := f.session("u-ada", "+1 day")
	if code, body := f.do("GET", "/api/companies", cookie(ada)); code != 200 || body != "user user ada@example.com/admin" {
		t.Errorf("session = %d %q", code, body)
	}
	// A valid session on a public path is not needed there and not looked at.
	if code, body := f.do("GET", "/api/openapi.json", cookie(ada)); code != 200 || body != " public -" {
		t.Errorf("openapi with a session = %d %q", code, body)
	}
	for name, tok := range map[string]string{
		"expired":       f.session("u-ada", "-1 second"),
		"disabled user": f.session("u-cy", "+1 day"),
		"unknown":       "nope",
		"empty":         "",
		"a token":       f.token("u-ada"), // a token is not a session
	} {
		if code, body := f.do("GET", "/api/companies", cookie(tok)); code != 401 || body != unauthorized {
			t.Errorf("%s session = %d %s", name, code, body)
		}
	}
	// A deleted account's session (no cascade on this connection) is nobody either.
	bo := f.session("u-bo", "+1 day")
	if _, err := db.Run(f.ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Run(f.ctx, "DELETE FROM users WHERE id = 'u-bo'"); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.do("GET", "/api/companies", cookie(bo)); code != 401 {
		t.Errorf("deleted user's session = %d", code)
	}
}

func TestMiddlewareTokens(t *testing.T) {
	f := setup(t)
	tok := f.token("u-bo")
	if code, body := f.do("POST", "/api/tasks", bearer(tok)); code != 200 || body != "api api bo@example.com/user" {
		t.Errorf("token = %d %q", code, body)
	}
	row, err := db.Get(f.ctx, "SELECT last_used_at FROM api_tokens WHERE token_hash = ?", auth.HashSecret(tok))
	if err != nil || row == nil || row["last_used_at"] == nil {
		t.Errorf("last_used_at not set: %v %v", row, err)
	}
	for name, h := range map[string]map[string]string{
		"disabled user": bearer(f.token("u-cy")),
		"unknown":       bearer("crm_nope"),
		"no prefix":     bearer(tok[4:]),
		"a session":     bearer(f.session("u-bo", "+1 day")),
		"lower bearer":  {"Authorization": "bearer " + tok + "x"},
		"empty":         {"Authorization": "Bearer "},
	} {
		if code, body := f.do("GET", "/api/companies", h); code != 401 || body != unauthorized {
			t.Errorf("%s = %d %s", name, code, body)
		}
	}
	// A bad token is not saved by a good cookie: the Authorization header decides.
	h := cookie(f.session("u-ada", "+1 day"))
	h["Authorization"] = "Bearer crm_nope"
	if code, _ := f.do("GET", "/api/companies", h); code != 401 {
		t.Errorf("bad token + good cookie = %d", code)
	}
	// Another scheme in Authorization (a proxy's Basic) leaves the cookie to decide.
	h["Authorization"] = "Basic eDp5"
	if code, _ := f.do("GET", "/api/companies", h); code != 200 {
		t.Errorf("Basic + good cookie = %d", code)
	}
}

// Cookie-authenticated writes from another site are refused: Sec-Fetch-Site
// when the browser sends it, else Origin against the host the browser used,
// else (a non-browser client) allowed. Token requests are never checked.
func TestMiddlewareCrossSite(t *testing.T) {
	f := setup(t)
	ses := f.session("u-ada", "+1 day")
	tok := f.token("u-ada")
	with := func(base map[string]string, extra ...string) map[string]string {
		h := map[string]string{}
		for k, v := range base {
			h[k] = v
		}
		for i := 0; i+1 < len(extra); i += 2 {
			h[extra[i]] = extra[i+1]
		}
		return h
	}
	cases := []struct {
		name   string
		method string
		header map[string]string
		want   int
	}{
		{"same-origin fetch", "POST", with(cookie(ses), "Sec-Fetch-Site", "same-origin", "Origin", "http://localhost:5175"), 200},
		{"user typed it", "DELETE", with(cookie(ses), "Sec-Fetch-Site", "none"), 200},
		{"cross-site fetch", "POST", with(cookie(ses), "Sec-Fetch-Site", "cross-site"), 403},
		{"same-site (a sibling subdomain)", "PUT", with(cookie(ses), "Sec-Fetch-Site", "same-site", "Origin", "https://crm.example.com"), 403},
		{"cross-site even with a matching Origin", "PATCH", with(cookie(ses), "Sec-Fetch-Site", "cross-site", "Origin", "http://crm.example.com"), 403},
		{"no Sec-Fetch-Site, same Origin host", "POST", with(cookie(ses), "Origin", "http://crm.example.com"), 200},
		{"no Sec-Fetch-Site, Origin host in other case", "POST", with(cookie(ses), "Origin", "https://CRM.example.com"), 200},
		{"no Sec-Fetch-Site, other Origin", "POST", with(cookie(ses), "Origin", "https://evil.example"), 403},
		{"no Sec-Fetch-Site, Origin other port", "POST", with(cookie(ses), "Origin", "http://crm.example.com:8080"), 403},
		{"no Sec-Fetch-Site, Origin null", "POST", with(cookie(ses), "Origin", "null"), 403},
		{"forwarded host matches", "POST", with(cookie(ses), "Origin", "https://crm.public.example", "X-Forwarded-Host", "crm.public.example", "X-Forwarded-Proto", "https"), 200},
		{"forwarded host differs", "POST", with(cookie(ses), "Origin", "https://crm.example.com", "X-Forwarded-Host", "crm.public.example"), 403},
		{"neither header (curl)", "POST", cookie(ses), 200},
		{"a read is never refused", "GET", with(cookie(ses), "Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example"), 200},
		{"token, cross-site", "POST", with(bearer(tok), "Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example"), 200},
		{"login, cross-site", "POST", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"login, other Origin", "POST", map[string]string{"Origin": "https://evil.example"}, 403},
		{"login, same-origin", "POST", map[string]string{"Sec-Fetch-Site": "same-origin"}, 200},
	}
	for _, c := range cases {
		path := "/api/companies"
		if strings.HasPrefix(c.name, "login") {
			path = "/api/auth/login"
		}
		code, body := f.do(c.method, path, c.header)
		if code != c.want || (c.want == 403 && body != crossSite) {
			t.Errorf("%s: %s = %d %s, want %d", c.name, c.method, code, body, c.want)
		}
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "192.0.2.1:5555"
	if got := auth.ClientIP(r); got != "192.0.2.1" {
		t.Errorf("ClientIP(no header) = %q", got)
	}
	r.Header.Set("CF-Connecting-IP", "2001:db8::1")
	if got := auth.ClientIP(r); got != "2001:db8::1" {
		t.Errorf("ClientIP(CF-Connecting-IP) = %q", got)
	}
	// With Cloudflare's Pseudo IPv4, CF-Connecting-IP is a made-up IPv4
	// address and the visitor's own is in CF-Connecting-IPv6.
	r.Header.Set("CF-Connecting-IP", "240.1.2.3")
	r.Header.Set("CF-Connecting-IPv6", "2001:db8:5::7")
	if got := auth.ClientIP(r); got != "2001:db8:5::7" {
		t.Errorf("ClientIP(CF-Connecting-IPv6) = %q", got)
	}
}
