package crm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"opencrm/internal/auth"
	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
	"opencrm/internal/web"
)

const (
	unauthorizedBody = `{"error":"unauthorized","message":"Sign in to use the CRM."}`
	wrongLogin       = `{"error":"Wrong email or password"}`
	tooMany          = `{"error":"Too many attempts. Try again in a few minutes."}`
	adminsOnly       = `{"error":"Only an admin can manage accounts"}`
	sessionsOnly     = `{"error":"Sign in with your password to manage accounts and tokens"}`
	lastAdmin        = `{"error":"The CRM needs at least one active admin"}`
	notYourself      = `{"error":"You can't delete, disable or demote your own account"}`
	badPassword      = `{"error":"A password needs 8 to 72 characters"}`
)

// testPassword is every seeded account's password; its hash is made once, at
// bcrypt's lowest cost, the one these tests run at (auth.SetCostForTests).
const testPassword = "password-1"

var testPasswordHash = sync.OnceValue(func() string {
	h, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	return string(h)
})

type authAPI struct {
	t   *testing.T
	ctx context.Context
	app *App
	h   http.Handler
	n   int // requests made, for a fresh client address each
}

// newAuthAPI is app behind its auth middleware on a fresh database, with
// bcrypt at its lowest cost: production's 12 is slow on purpose.
func newAuthAPI(t *testing.T, app *App) *authAPI {
	auth.SetCostForTests(t, bcrypt.MinCost)
	ctx := dbtest.Setup(t)
	return &authAPI{t: t, ctx: ctx, app: app, h: New(app)}
}

type response struct {
	code   int
	body   string
	header http.Header
}

func (r response) String() string { return fmt.Sprintf("%d %s", r.code, r.body) }

// do sends a request as a browser on crm.example.com would, from its own
// address unless header names one. A body that isn't a string is sent as JSON.
func (a *authAPI) do(method, path string, body any, header map[string]string) response {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		j, err := json.Marshal(b)
		if err != nil {
			a.t.Fatal(err)
		}
		rd = bytes.NewReader(j)
	}
	r := httptest.NewRequest(method, path, rd)
	r.Host = "crm.example.com"
	if rd != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	a.n++
	r.Header.Set("CF-Connecting-IP", fmt.Sprintf("198.51.%d.%d", a.n/250, a.n%250))
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	return response{w.Code, w.Body.String(), w.Header()}
}

// user adds an account whose password is testPassword.
func (a *authAPI) user(id, email, role string, disabled bool) {
	a.t.Helper()
	if _, err := db.Run(a.ctx, "INSERT INTO users (id, email, name, role, password_hash, disabled) VALUES (?, ?, ?, ?, ?, ?)",
		id, email, strings.ToUpper(email[:1])+email[1:strings.Index(email, "@")], role, testPasswordHash(), disabled); err != nil {
		a.t.Fatal(err)
	}
}

func sessionCookie(t *testing.T, res response) *http.Cookie {
	t.Helper()
	for _, c := range (&http.Response{Header: res.header}).Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	t.Fatalf("no %s cookie in %v", auth.CookieName, res.header.Values("Set-Cookie"))
	return nil
}

// login signs in and returns the session cookie's value.
func (a *authAPI) login(email, password string) string {
	a.t.Helper()
	res := a.do("POST", "/api/auth/login", map[string]any{"email": email, "password": password}, nil)
	if res.code != 200 {
		a.t.Fatalf("login %s: %s", email, res)
	}
	return sessionCookie(a.t, res).Value
}

func asSession(tok string) map[string]string {
	return map[string]string{"Cookie": auth.CookieName + "=" + tok}
}
func asToken(tok string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + tok}
}

func (a *authAPI) count(sql string, params ...any) int {
	a.t.Helper()
	row, err := db.Get(a.ctx, "SELECT COUNT(*) AS n FROM "+sql, params...)
	if err != nil {
		a.t.Fatal(err)
	}
	return int(row["n"].(float64))
}

func TestAuthLoginSetsTheCookieAndMeReturnsTheUser(t *testing.T) {
	a := newAuthAPI(t, &App{})
	a.user("u-ada", "ada@example.com", "admin", false)

	res := a.do("POST", "/api/auth/login", map[string]any{"email": "  ADA@Example.com ", "password": testPassword}, nil)
	want := `{"user":{"id":"u-ada","email":"ada@example.com","name":"Ada","role":"admin"}}`
	if res.code != 200 || res.body != want {
		t.Fatalf("login = %s", res)
	}
	set := res.header.Get("Set-Cookie")
	if !regexp.MustCompile(`^crm_session=[A-Za-z0-9_-]{43}; Path=/; Max-Age=2592000; HttpOnly; Secure; SameSite=Lax$`).MatchString(set) {
		t.Errorf("Set-Cookie = %q", set)
	}
	tok := sessionCookie(t, res).Value
	// Only the hash is stored, and the session lasts 30 days.
	if n := a.count("sessions WHERE token_hash = ? AND user_id = 'u-ada' AND expires_at BETWEEN datetime('now', '+30 days', '-1 minute') AND datetime('now', '+30 days', '+1 minute')", auth.HashSecret(tok)); n != 1 {
		t.Errorf("sessions with the token's hash = %d", n)
	}
	if n := a.count("sessions WHERE token_hash = ?", tok); n != 0 {
		t.Error("the session token is stored in the clear")
	}

	if res := a.do("GET", "/api/auth/me", nil, asSession(tok)); res.code != 200 || res.body != want {
		t.Errorf("me = %s", res)
	}

	// A login hands out a new session; the one it came with ends.
	tok2 := sessionCookie(t, a.do("POST", "/api/auth/login", map[string]any{"email": "ada@example.com", "password": testPassword}, asSession(tok))).Value
	if tok2 == tok {
		t.Error("login reused the session it was sent")
	}
	if res := a.do("GET", "/api/auth/me", nil, asSession(tok)); res.code != 401 {
		t.Errorf("the session a login replaced = %s", res)
	}

	// Logout ends the session and expires the cookie.
	res = a.do("POST", "/api/auth/logout", nil, asSession(tok2))
	if res.code != 200 || res.body != `{"ok":true}` {
		t.Errorf("logout = %s", res)
	}
	if set := res.header.Get("Set-Cookie"); set != "crm_session=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Lax" {
		t.Errorf("logout Set-Cookie = %q", set)
	}
	if res := a.do("GET", "/api/auth/me", nil, asSession(tok2)); res.code != 401 || res.body != unauthorizedBody {
		t.Errorf("me after logout = %s", res)
	}
}

func TestAuthWrongPasswordAndUnknownEmailLookAlike(t *testing.T) {
	a := newAuthAPI(t, &App{})
	a.user("u-ada", "ada@example.com", "admin", false)
	a.user("u-cy", "cy@example.com", "user", true)
	for name, body := range map[string]any{
		"wrong password":    map[string]any{"email": "ada@example.com", "password": "password-2"},
		"unknown email":     map[string]any{"email": "nobody@example.com", "password": testPassword},
		"disabled account":  map[string]any{"email": "cy@example.com", "password": testPassword},
		"no password":       map[string]any{"email": "ada@example.com"},
		"password a number": map[string]any{"email": "ada@example.com", "password": 12345678},
		"no email":          map[string]any{"password": testPassword},
		"too long":          map[string]any{"email": "ada@example.com", "password": testPassword + strings.Repeat("x", 80)},
		"not JSON":          "email=ada@example.com",
		"empty":             nil,
		// A login reads at most 64 KiB: one past that isn't JSON, whatever it starts with.
		"past 64 KiB": `{"email":"ada@example.com","password":"` + testPassword + `"}` + strings.Repeat(" ", 64<<10),
	} {
		res := a.do("POST", "/api/auth/login", body, nil)
		if res.code != 401 || res.body != wrongLogin || res.header.Get("Set-Cookie") != "" {
			t.Errorf("%s: %s", name, res)
		}
	}
	if n := a.count("sessions"); n != 0 {
		t.Errorf("%d sessions after failed logins", n)
	}
}

func TestAuthLoginLimiter(t *testing.T) {
	a := newAuthAPI(t, &App{})
	a.user("u-ada", "ada@example.com", "admin", false)
	a.user("u-bo", "bo@example.com", "user", false)
	from := map[string]string{"CF-Connecting-IP": "203.0.113.9"}

	// 10 attempts a minute from one address...
	for i := 0; i < 10; i++ {
		if res := a.do("POST", "/api/auth/login", map[string]any{"email": fmt.Sprintf("x%d@example.com", i), "password": "wrong-password"}, from); res.code != 401 {
			t.Fatalf("attempt %d = %s", i+1, res)
		}
	}
	// ...then even the right password waits.
	if res := a.do("POST", "/api/auth/login", map[string]any{"email": "ada@example.com", "password": testPassword}, from); res.code != 429 || res.body != tooMany {
		t.Errorf("11th attempt = %s", res)
	}
	// Another address is fine.
	a.login("ada@example.com", testPassword)

	// 10 failures in 15 minutes for one email, from anywhere, lock that email.
	for i := 0; i < 10; i++ {
		if res := a.do("POST", "/api/auth/login", map[string]any{"email": "BO@example.com", "password": "wrong-password"}, nil); res.code != 401 {
			t.Fatalf("failure %d = %s", i+1, res)
		}
	}
	if res := a.do("POST", "/api/auth/login", map[string]any{"email": "bo@example.com", "password": testPassword}, nil); res.code != 429 || res.body != tooMany {
		t.Errorf("bo after 10 failures = %s", res)
	}
	a.login("ada@example.com", testPassword) // other accounts are not locked
}

// With no accounts, a login says how to make the first one, whether the
// secrets are missing or set to something that can't make an account.
func TestAuthNoAccountsYet(t *testing.T) {
	const noAccounts = `{"error":"No accounts yet: set ADMIN_EMAIL to an email address and ADMIN_PASSWORD to 8–72 characters, then restart the CRM"}`
	for name, app := range map[string]*App{
		"no secrets":          {},
		"no password":         {AdminEmail: "ada@example.com"},
		"not an email":        {AdminEmail: "ada", AdminPassword: testPassword},
		"a short password":    {AdminEmail: "ada@example.com", AdminPassword: "short"},
		"a too-long password": {AdminEmail: "ada@example.com", AdminPassword: strings.Repeat("x", 73)},
	} {
		a := newAuthAPI(t, app)
		res := a.do("POST", "/api/auth/login", map[string]any{"email": "ada@example.com", "password": testPassword}, nil)
		if res.code != 503 || res.body != noAccounts {
			t.Errorf("%s: login with no accounts = %s", name, res)
		}
	}
}

func TestAuthBootstrapCreatesTheAdminOnce(t *testing.T) {
	a := newAuthAPI(t, &App{AdminEmail: " Ada@Example.com ", AdminPassword: "first-password"})
	a.login("ada@example.com", "first-password")
	a.login("ada@example.com", "first-password")
	if n := a.count("users"); n != 1 {
		t.Fatalf("%d users after two logins", n)
	}
	// A bcrypt hash at the tests' cost (production's 12 is auth's TestPasswords).
	if n := a.count("users WHERE email = 'ada@example.com' AND role = 'admin' AND disabled = 0 AND password_hash LIKE '$2a$04$%'"); n != 1 {
		t.Error("the bootstrap admin is not an active admin with a bcrypt hash")
	}

	// A restart with other secrets doesn't touch a CRM that has accounts.
	a.h = New(&App{AdminEmail: "eve@example.com", AdminPassword: "other-password"})
	if res := a.do("POST", "/api/auth/login", map[string]any{"email": "eve@example.com", "password": "other-password"}, nil); res.code != 401 {
		t.Errorf("second bootstrap admin = %s", res)
	}
	a.login("ada@example.com", "first-password")
	if n := a.count("users"); n != 1 {
		t.Errorf("%d users after a restart", n)
	}

	// Secrets that can't make an account leave the CRM without one.
	b := newAuthAPI(t, &App{AdminEmail: "ada@example.com", AdminPassword: "short"})
	if res := b.do("POST", "/api/auth/login", map[string]any{"email": "ada@example.com", "password": "short"}, nil); res.code != 503 {
		t.Errorf("bootstrap with a short password = %s", res)
	}
}

func TestAuthBootstrapToken(t *testing.T) {
	a := newAuthAPI(t, &App{})
	tok, _ := auth.NewSecret(auth.TokenPrefix)
	for i := 0; i < 2; i++ {
		if err := a.app.BootstrapToken(a.ctx, tok); err != nil {
			t.Fatal(err)
		}
	}
	if n := a.count("api_tokens"); n != 1 {
		t.Errorf("%d tokens", n)
	}
	res := a.do("GET", "/api/auth/me", nil, asToken(tok))
	if res.code != 200 || !strings.Contains(res.body, `"role":"admin"`) {
		t.Errorf("me with the bootstrap token = %s", res)
	}
	if err := a.app.BootstrapToken(a.ctx, "not-a-crm-token"); err == nil {
		t.Error("a token without the crm_ prefix was accepted")
	}
}

func TestAuthTokensActAsTheirUser(t *testing.T) {
	a := newAuthAPI(t, &App{})
	a.user("u-ada", "ada@example.com", "admin", false)
	a.user("u-bo", "bo@example.com", "user", false)
	bo := a.login("bo@example.com", testPassword)

	res := a.do("POST", "/api/tokens", map[string]any{"name": "  Claude  "}, asSession(bo))
	var created struct {
		Token  map[string]any `json:"token"`
		Secret string         `json:"secret"`
	}
	if res.code != 201 || json.Unmarshal([]byte(res.body), &created) != nil {
		t.Fatalf("create token = %s", res)
	}
	if !regexp.MustCompile(`^\{"token":\{"id":"[0-9a-f-]{36}","name":"Claude","prefix":"crm_[A-Za-z0-9_-]{8}","last_used_at":null,"created_at":"[0-9 :-]{19}"\},"secret":"crm_[A-Za-z0-9_-]{43}"\}$`).MatchString(res.body) ||
		created.Token["prefix"] != created.Secret[:12] {
		t.Errorf("create token = %s", res.body)
	}
	if n := a.count("api_tokens WHERE token_hash = ? AND user_id = 'u-bo'", auth.HashSecret(created.Secret)); n != 1 {
		t.Error("the token's hash is not stored for its user")
	}

	// It signs in as bo: tasks it creates are bo's.
	res = a.do("POST", "/api/tasks", map[string]any{"title": "Call Sam"}, asToken(created.Secret))
	if res.code != 201 || !strings.Contains(res.body, `"created_by":"bo@example.com"`) {
		t.Errorf("task by token = %s", res)
	}
	res = a.do("POST", "/api/tasks", map[string]any{"title": "Call Ann"}, asSession(bo))
	if res.code != 201 || !strings.Contains(res.body, `"created_by":"bo@example.com"`) {
		t.Errorf("task by session = %s", res)
	}
	if res := a.do("GET", "/api/auth/me", nil, asToken(created.Secret)); res.body != `{"user":{"id":"u-bo","email":"bo@example.com","name":"Bo","role":"user"}}` {
		t.Errorf("me by token = %s", res)
	}
	if n := a.count("api_tokens WHERE last_used_at IS NOT NULL"); n != 1 {
		t.Error("last_used_at was not updated")
	}

	// The list shows what it is, never the secret or its hash.
	res = a.do("GET", "/api/tokens", nil, asSession(bo))
	if res.code != 200 || !strings.HasPrefix(res.body, `{"tokens":[{"id":"`) || strings.Contains(res.body, created.Secret) ||
		strings.Contains(res.body, auth.HashSecret(created.Secret)) || strings.Contains(res.body, "hash") || strings.Contains(res.body, "user_id") {
		t.Errorf("list tokens = %s", res)
	}
	if !strings.Contains(res.body, `"last_used_at":"`) {
		t.Errorf("list tokens has no last_used_at: %s", res.body)
	}

	// Each person sees and deletes only their own.
	ada := a.login("ada@example.com", testPassword)
	if res := a.do("GET", "/api/tokens", nil, asSession(ada)); res.body != `{"tokens":[]}` {
		t.Errorf("ada's tokens = %s", res)
	}
	id := created.Token["id"].(string)
	if res := a.do("DELETE", "/api/tokens/"+id, nil, asSession(ada)); res.code != 404 || res.body != `{"error":"Token not found"}` {
		t.Errorf("ada deletes bo's token = %s", res)
	}
	if res := a.do("DELETE", "/api/tokens/"+id, nil, asSession(bo)); res.code != 200 || res.body != `{"ok":true}` {
		t.Errorf("bo deletes his token = %s", res)
	}
	if res := a.do("GET", "/api/tasks", nil, asToken(created.Secret)); res.code != 401 {
		t.Errorf("a deleted token = %s", res)
	}

	for _, name := range []any{"", "   ", strings.Repeat("x", 61), 5, nil} {
		res := a.do("POST", "/api/tokens", map[string]any{"name": name}, asSession(bo))
		if res.code != 400 || res.body != `{"error":"A token name needs 1 to 60 characters"}` {
			t.Errorf("token name %v = %s", name, res)
		}
	}
	if res := a.do("POST", "/api/tokens", map[string]any{"name": strings.Repeat("é", 60)}, asSession(bo)); res.code != 201 {
		t.Errorf("60 characters = %s", res)
	}
}

func TestAuthDisabledAccountIsSignedOut(t *testing.T) {
	a := newAuthAPI(t, &App{})
	a.user("u-ada", "ada@example.com", "admin", false)
	a.user("u-bo", "bo@example.com", "user", false)
	ada := a.login("ada@example.com", testPassword)
	bo := a.login("bo@example.com", testPassword)
	var tok struct{ Secret string }
	json.Unmarshal([]byte(a.do("POST", "/api/tokens", map[string]any{"name": "ci"}, asSession(bo)).body), &tok)

	res := a.do("PATCH", "/api/users/u-bo", map[string]any{"disabled": true}, asSession(ada))
	if res.code != 200 || !strings.Contains(res.body, `"disabled":true`) {
		t.Fatalf("disable bo = %s", res)
	}
	if res := a.do("GET", "/api/auth/me", nil, asSession(bo)); res.code != 401 {
		t.Errorf("disabled user's session = %s", res)
	}
	if n := a.count("sessions WHERE user_id = 'u-bo'"); n != 0 {
		t.Errorf("disabling left %d sessions", n)
	}
	if res := a.do("GET", "/api/auth/me", nil, asToken(tok.Secret)); res.code != 401 {
		t.Errorf("disabled user's token = %s", res)
	}
	if res := a.do("POST", "/api/auth/login", map[string]any{"email": "bo@example.com", "password": testPassword}, nil); res.code != 401 || res.body != wrongLogin {
		t.Errorf("disabled user's login = %s", res)
	}
	// Disabling is what an admin does about a compromised account: its API
	// tokens are deleted, so enabling it again doesn't bring them back.
	if n := a.count("api_tokens WHERE user_id = 'u-bo'"); n != 0 {
		t.Errorf("disabling left %d tokens", n)
	}
	a.do("PATCH", "/api/users/u-bo", map[string]any{"disabled": false}, asSession(ada))
	if res := a.do("GET", "/api/auth/me", nil, asToken(tok.Secret)); res.code != 401 {
		t.Errorf("re-enabled user's old token = %s", res)
	}
	a.login("bo@example.com", testPassword)
}

func TestAuthExpiredSession(t *testing.T) {
	a := newAuthAPI(t, &App{})
	a.user("u-ada", "ada@example.com", "admin", false)
	tok := a.login("ada@example.com", testPassword)
	if _, err := db.Run(a.ctx, "UPDATE sessions SET expires_at = datetime('now', '-1 second')"); err != nil {
		t.Fatal(err)
	}
	if res := a.do("GET", "/api/auth/me", nil, asSession(tok)); res.code != 401 || res.body != unauthorizedBody {
		t.Errorf("expired session = %s", res)
	}
}

func TestAuthPasswordChange(t *testing.T) {
	a := newAuthAPI(t, &App{})
	a.user("u-ada", "ada@example.com", "admin", false)
	here := a.login("ada@example.com", testPassword)
	there := a.login("ada@example.com", testPassword)
	var tok struct{ Secret string }
	json.Unmarshal([]byte(a.do("POST", "/api/tokens", map[string]any{"name": "ci"}, asSession(here)).body), &tok)

	for _, c := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"current_password": "nope-nope", "new_password": "password-2"}, `{"error":"Your current password is wrong"}`},
		{map[string]any{"new_password": "password-2"}, `{"error":"Your current password is wrong"}`},
		{map[string]any{"current_password": testPassword, "new_password": "short"}, badPassword},
		{map[string]any{"current_password": testPassword, "new_password": strings.Repeat("x", 73)}, badPassword},
		{map[string]any{"current_password": testPassword}, badPassword},
	} {
		if res := a.do("PUT", "/api/auth/password", c.body, asSession(here)); res.code != 400 || res.body != c.want {
			t.Errorf("%v = %s", c.body, res)
		}
	}
	res := a.do("PUT", "/api/auth/password", map[string]any{"current_password": testPassword, "new_password": "password-2"}, asSession(here))
	if res.code != 200 || res.body != `{"ok":true}` {
		t.Fatalf("change = %s", res)
	}
	// This session stays and the others end; her API tokens keep working
	// (her scripts aren't broken by a password change).
	if res := a.do("GET", "/api/auth/me", nil, asSession(here)); res.code != 200 {
		t.Errorf("the session that changed it = %s", res)
	}
	if res := a.do("GET", "/api/auth/me", nil, asSession(there)); res.code != 401 {
		t.Errorf("another session = %s", res)
	}
	if res := a.do("GET", "/api/auth/me", nil, asToken(tok.Secret)); res.code != 200 {
		t.Errorf("her token after a password change = %s", res)
	}
	if res := a.do("POST", "/api/auth/login", map[string]any{"email": "ada@example.com", "password": testPassword}, nil); res.code != 401 {
		t.Errorf("old password = %s", res)
	}
	a.login("ada@example.com", "password-2")

	// Someone locking ada's email out of signing in doesn't stop her, signed
	// in, from changing her password: the two have their own counts.
	for i := 0; i < 10; i++ {
		a.do("POST", "/api/auth/login", map[string]any{"email": "ada@example.com", "password": "guess-guess"}, nil)
	}
	if res := a.do("POST", "/api/auth/login", map[string]any{"email": "ada@example.com", "password": "password-2"}, nil); res.code != 429 {
		t.Fatalf("login after 10 failures = %s", res)
	}
	if res := a.do("PUT", "/api/auth/password", map[string]any{"current_password": "password-2", "new_password": "password-3"}, asSession(here)); res.code != 200 {
		t.Errorf("password change while the login is locked = %s", res)
	}

	// Guessing the current password is limited too: 10 tries in 15 minutes.
	for i := 0; i < 10; i++ {
		a.do("PUT", "/api/auth/password", map[string]any{"current_password": "guess-guess", "new_password": "password-4"}, asSession(here))
	}
	if res := a.do("PUT", "/api/auth/password", map[string]any{"current_password": "password-3", "new_password": "password-4"}, asSession(here)); res.code != 429 || res.body != tooMany {
		t.Errorf("11th guess = %s", res)
	}
}

func TestUsersAreForAdminsOnly(t *testing.T) {
	a := newAuthAPI(t, &App{})
	a.user("u-ada", "ada@example.com", "admin", false)
	a.user("u-bo", "bo@example.com", "user", false)
	bo := a.login("bo@example.com", testPassword)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/users", nil},
		{"POST", "/api/users", map[string]any{"email": "eve@example.com", "password": "password-9", "role": "admin"}},
		{"PATCH", "/api/users/u-bo", map[string]any{"role": "admin"}},
		{"PATCH", "/api/users/u-ada", map[string]any{"disabled": true}},
		{"DELETE", "/api/users/u-ada", nil},
	} {
		if res := a.do(c.method, c.path, c.body, asSession(bo)); res.code != 403 || res.body != adminsOnly {
			t.Errorf("bo %s %s = %s", c.method, c.path, res)
		}
	}
	if n := a.count("users WHERE role = 'admin'"); n != 1 {
		t.Errorf("%d admins", n)
	}
}

func TestUsersManagedByAnAdmin(t *testing.T) {
	a := newAuthAPI(t, &App{})
	a.user("u-ada", "ada@example.com", "admin", false)
	ada := a.login("ada@example.com", testPassword)

	res := a.do("POST", "/api/users", map[string]any{"email": " Bo@Example.COM ", "name": "  Bo Brown ", "password": "bo-password"}, asSession(ada))
	if res.code != 201 || !regexp.MustCompile(`^\{"user":\{"id":"[0-9a-f-]{36}","email":"bo@example.com","name":"Bo Brown","role":"user","disabled":false,"created_at":"[0-9 :-]{19}","updated_at":"[0-9 :-]{19}"\}\}$`).MatchString(res.body) {
		t.Fatalf("create bo = %s", res)
	}
	var created struct{ User struct{ ID string } }
	json.Unmarshal([]byte(res.body), &created)
	boID := created.User.ID
	bo := a.login("bo@example.com", "bo-password")

	if res := a.do("POST", "/api/users", map[string]any{"email": "BO@example.com", "password": "other-password"}, asSession(ada)); res.code != 409 || res.body != `{"error":"An account with this email already exists"}` {
		t.Errorf("duplicate = %s", res)
	}
	for _, c := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"email": "no-at-sign", "password": "password-9"}, `{"error":"Enter a valid email address"}`},
		{map[string]any{"email": 5, "password": "password-9"}, `{"error":"Enter a valid email address"}`},
		{map[string]any{"password": "password-9"}, `{"error":"Enter a valid email address"}`},
		{map[string]any{"email": "eve@example.com", "password": "short"}, badPassword},
		{map[string]any{"email": "eve@example.com", "password": strings.Repeat("é", 37)}, badPassword},
		{map[string]any{"email": "eve@example.com"}, badPassword},
		{map[string]any{"email": "eve@example.com", "password": "password-9", "role": "root"}, `{"error":"A role is admin or user"}`},
		{map[string]any{"email": "eve@example.com", "password": "password-9", "name": strings.Repeat("x", 101)}, `{"error":"A name can have at most 100 characters"}`},
		{map[string]any{"email": "eve@example.com", "password": "password-9", "name": 5}, `{"error":"A name can have at most 100 characters"}`},
	} {
		if res := a.do("POST", "/api/users", c.body, asSession(ada)); res.code != 400 || res.body != c.want {
			t.Errorf("create %v = %s", c.body, res)
		}
	}

	res = a.do("GET", "/api/users", nil, asSession(ada))
	if res.code != 200 || !strings.HasPrefix(res.body, `{"users":[{"id":"u-ada","email":"ada@example.com"`) || !strings.Contains(res.body, `"email":"bo@example.com"`) {
		t.Errorf("list = %s", res)
	}
	if strings.Contains(res.body, "password") || strings.Contains(res.body, "$2a$") {
		t.Errorf("the list shows password hashes: %s", res.body)
	}

	// PATCH: name and role; a new password ends bo's sessions.
	res = a.do("PATCH", "/api/users/"+boID, map[string]any{"name": "Bo B.", "role": "admin"}, asSession(ada))
	if res.code != 200 || !strings.Contains(res.body, `"name":"Bo B.","role":"admin","disabled":false`) || strings.Contains(res.body, "password") {
		t.Errorf("patch = %s", res)
	}
	if res := a.do("GET", "/api/auth/me", nil, asSession(bo)); !strings.Contains(res.body, `"role":"admin"`) {
		t.Errorf("bo's session doesn't see the new role at once: %s", res)
	}
	var boTok struct{ Secret string }
	json.Unmarshal([]byte(a.do("POST", "/api/tokens", map[string]any{"name": "ci"}, asSession(bo)).body), &boTok)
	if res := a.do("PATCH", "/api/users/"+boID, map[string]any{"password": "new-bo-password"}, asSession(ada)); res.code != 200 {
		t.Errorf("set password = %s", res)
	}
	// An admin resetting a password is answering a compromise: the account's
	// sessions end and its API tokens are deleted.
	if res := a.do("GET", "/api/auth/me", nil, asSession(bo)); res.code != 401 {
		t.Errorf("bo's session after a new password = %s", res)
	}
	if res := a.do("GET", "/api/auth/me", nil, asToken(boTok.Secret)); res.code != 401 {
		t.Errorf("bo's token after a new password = %s", res)
	}
	if n := a.count("api_tokens WHERE user_id = ?", boID); n != 0 {
		t.Errorf("a new password left %d tokens", n)
	}
	a.login("bo@example.com", "new-bo-password")
	for _, c := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"role": "owner"}, `{"error":"A role is admin or user"}`},
		{map[string]any{"disabled": "yes"}, `{"error":"disabled is true or false"}`},
		{map[string]any{"password": "short"}, badPassword},
		{map[string]any{"name": strings.Repeat("x", 101)}, `{"error":"A name can have at most 100 characters"}`},
	} {
		if res := a.do("PATCH", "/api/users/"+boID, c.body, asSession(ada)); res.code != 400 || res.body != c.want {
			t.Errorf("patch %v = %s", c.body, res)
		}
	}
	if res := a.do("PATCH", "/api/users/nope", map[string]any{"name": "x"}, asSession(ada)); res.code != 404 || res.body != `{"error":"Account not found"}` {
		t.Errorf("patch unknown = %s", res)
	}
	// An admin can rename herself, but sets her own password with the
	// current one (PUT /api/auth/password), not here.
	if res := a.do("PATCH", "/api/users/u-ada", map[string]any{"name": "Ada L.", "role": "admin", "disabled": false}, asSession(ada)); res.code != 200 || !strings.Contains(res.body, `"name":"Ada L."`) {
		t.Errorf("ada renames herself = %s", res)
	}
	if res := a.do("PATCH", "/api/users/u-ada", map[string]any{"name": "Ada X.", "password": "ada-new-password"}, asSession(ada)); res.code != 400 || res.body != `{"error":"Change your own password from your account settings"}` {
		t.Errorf("ada sets her own password = %s", res)
	}
	if n := a.count("users WHERE id = 'u-ada' AND name = 'Ada L.'"); n != 1 {
		t.Error("the refused change was applied")
	}
	a.login("ada@example.com", testPassword)

	// DELETE takes the account's sessions and tokens with it.
	bo = a.login("bo@example.com", "new-bo-password")
	var tok struct{ Secret string }
	json.Unmarshal([]byte(a.do("POST", "/api/tokens", map[string]any{"name": "ci"}, asSession(bo)).body), &tok)
	if res := a.do("DELETE", "/api/users/"+boID, nil, asSession(ada)); res.code != 200 || res.body != `{"ok":true}` {
		t.Errorf("delete bo = %s", res)
	}
	if n := a.count("sessions WHERE user_id = ?", boID) + a.count("api_tokens WHERE user_id = ?", boID); n != 0 {
		t.Errorf("%d sessions and tokens left", n)
	}
	if res := a.do("GET", "/api/auth/me", nil, asToken(tok.Secret)); res.code != 401 {
		t.Errorf("deleted user's token = %s", res)
	}
	if res := a.do("DELETE", "/api/users/"+boID, nil, asSession(ada)); res.code != 404 || res.body != `{"error":"Account not found"}` {
		t.Errorf("delete again = %s", res)
	}
}

func TestUsersNoAdminRemovesThemselves(t *testing.T) {
	a := newAuthAPI(t, &App{})
	a.user("u-ada", "ada@example.com", "admin", false)
	a.user("u-cat", "cat@example.com", "admin", false)
	ada := a.login("ada@example.com", testPassword)
	for _, c := range []struct {
		method string
		body   any
	}{
		{"PATCH", map[string]any{"role": "user"}},
		{"PATCH", map[string]any{"disabled": true}},
		{"PATCH", map[string]any{"name": "x", "disabled": true}},
		{"DELETE", nil},
	} {
		if res := a.do(c.method, "/api/users/u-ada", c.body, asSession(ada)); res.code != 400 || res.body != notYourself {
			t.Errorf("ada %s herself %v = %s", c.method, c.body, res)
		}
	}
	if n := a.count("users WHERE id = 'u-ada' AND role = 'admin' AND disabled = 0 AND name = 'Ada'"); n != 1 {
		t.Error("a refused change was applied")
	}
}

// A token reaches the CRM's data, not its accounts: managing tokens,
// accounts and passwords takes a signed-in browser session, so a leaked token
// can't mint more tokens or take over an account.
func TestTokensCantManageAccounts(t *testing.T) {
	a := newAuthAPI(t, &App{})
	a.user("u-ada", "ada@example.com", "admin", false)
	a.user("u-bo", "bo@example.com", "user", false)
	ada := a.login("ada@example.com", testPassword)
	var tok struct {
		Token  struct{ ID string }
		Secret string
	}
	json.Unmarshal([]byte(a.do("POST", "/api/tokens", map[string]any{"name": "ci"}, asSession(ada)).body), &tok)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/tokens", nil},
		{"POST", "/api/tokens", map[string]any{"name": "another"}},
		{"DELETE", "/api/tokens/" + tok.Token.ID, nil},
		{"GET", "/api/users", nil},
		{"POST", "/api/users", map[string]any{"email": "eve@example.com", "password": "password-9", "role": "admin"}},
		{"PATCH", "/api/users/u-bo", map[string]any{"password": "password-9"}},
		{"PATCH", "/api/users/u-ada", map[string]any{"name": "x"}},
		{"DELETE", "/api/users/u-bo", nil},
		{"PUT", "/api/auth/password", map[string]any{"current_password": testPassword, "new_password": "password-9"}},
	} {
		if res := a.do(c.method, c.path, c.body, asToken(tok.Secret)); res.code != 403 || res.body != sessionsOnly {
			t.Errorf("token %s %s = %s", c.method, c.path, res)
		}
	}
	if n := a.count("api_tokens"); n != 1 {
		t.Errorf("%d tokens", n)
	}
	if n := a.count("users"); n != 2 {
		t.Errorf("%d users", n)
	}

	// It can still say whose it is, and "sign out": with no session behind
	// it there is nothing to end, and the cookie is left alone.
	if res := a.do("GET", "/api/auth/me", nil, asToken(tok.Secret)); res.code != 200 || !strings.Contains(res.body, `"id":"u-ada"`) {
		t.Errorf("me with a token = %s", res)
	}
	h := asToken(tok.Secret)
	h["Cookie"] = auth.CookieName + "=" + ada
	res := a.do("POST", "/api/auth/logout", nil, h)
	if res.code != 200 || res.body != `{"ok":true}` || res.header.Get("Set-Cookie") != "" {
		t.Errorf("logout with a token = %s %v", res, res.header.Values("Set-Cookie"))
	}
	if res := a.do("GET", "/api/auth/me", nil, asSession(ada)); res.code != 200 {
		t.Errorf("the session after a token's logout = %s", res)
	}
	// The same calls work from the session.
	if res := a.do("GET", "/api/tokens", nil, asSession(ada)); res.code != 200 {
		t.Errorf("tokens with the session = %s", res)
	}
	if res := a.do("GET", "/api/users", nil, asSession(ada)); res.code != 200 {
		t.Errorf("users with the session = %s", res)
	}
}

// While both bcrypt slots are busy, a login waits at most as long as the
// request does (2 s at most) and then answers 429 without checking the
// password, so a flood of logins can't starve the CRM of CPU.
func TestAuthLoginWhenBcryptIsBusy(t *testing.T) {
	a := newAuthAPI(t, &App{})
	a.user("u-ada", "ada@example.com", "admin", false)
	var releases []func()
	for i := 0; i < 2; i++ {
		release, err := auth.AcquireBcryptSlot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()

	body, _ := json.Marshal(map[string]any{"email": "ada@example.com", "password": testPassword})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	start := time.Now()
	a.h.ServeHTTP(w, r)
	// The right password, yet no session: the password was never checked.
	if w.Code != 429 || w.Body.String() != tooMany || w.Header().Get("Set-Cookie") != "" {
		t.Errorf("login with bcrypt busy = %d %s", w.Code, w.Body.String())
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v", d)
	}
	if n := a.count("sessions"); n != 0 {
		t.Errorf("%d sessions", n)
	}
	// So do an admin's account writes that need a hash.
	for _, release := range releases {
		release()
	}
	releases = nil
	ada := a.login("ada@example.com", testPassword)
	for i := 0; i < 2; i++ {
		release, err := auth.AcquireBcryptSlot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()
	r = httptest.NewRequest("POST", "/api/users", strings.NewReader(`{"email":"bo@example.com","password":"password-9"}`)).WithContext(ctx2)
	r.Header.Set("Cookie", auth.CookieName+"="+ada)
	w = httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	if w.Code != 429 || w.Body.String() != tooMany {
		t.Errorf("create user with bcrypt busy = %d %s", w.Code, w.Body.String())
	}
}

// A login refused because no bcrypt slot came free checked no password, so
// it counts against neither the address's attempts nor the email's failures:
// a flood that keeps bcrypt busy locks nobody out. Both places a login can
// find bcrypt busy: making the first admin, and checking the password.
func TestAuthBusyLoginsDontCount(t *testing.T) {
	now := time.Now()
	a := newAuthAPI(t, &App{AdminEmail: "ada@example.com", AdminPassword: testPassword, Now: func() time.Time { return now }})
	from := map[string]string{"CF-Connecting-IP": "203.0.113.9"}
	login := func(password string) response {
		return a.do("POST", "/api/auth/login", map[string]any{"email": "ada@example.com", "password": password}, from)
	}
	busyLogins := func() {
		t.Helper()
		var releases []func()
		for i := 0; i < 2; i++ {
			release, err := auth.AcquireBcryptSlot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			releases = append(releases, release)
		}
		defer func() {
			for _, release := range releases {
				release()
			}
		}()
		body, _ := json.Marshal(map[string]any{"email": "ada@example.com", "password": testPassword})
		for i := 0; i < 12; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			r := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body)).WithContext(ctx)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("CF-Connecting-IP", from["CF-Connecting-IP"])
			w := httptest.NewRecorder()
			a.h.ServeHTTP(w, r)
			cancel()
			if w.Code != 429 || w.Body.String() != tooMany {
				t.Fatalf("busy login %d = %d %s", i+1, w.Code, w.Body.String())
			}
		}
	}
	// Then the whole allowance is left: 9 failures and the right password,
	// from the same address, at the same email.
	allowanceLeft := func(what string) {
		t.Helper()
		for i := 0; i < 9; i++ {
			if res := login("wrong-password"); res.code != 401 {
				t.Fatalf("after busy logins (%s), failure %d = %s", what, i+1, res)
			}
		}
		if res := login(testPassword); res.code != 200 {
			t.Fatalf("after busy logins (%s), the right password = %s", what, res)
		}
	}

	busyLogins() // no accounts yet: the first admin can't be made
	if n := a.count("users"); n != 0 {
		t.Fatalf("%d users made while bcrypt was busy", n)
	}
	allowanceLeft("making the first admin")

	now = now.Add(16 * time.Minute) // past both windows
	busyLogins()                    // the admin exists: the password can't be checked
	allowanceLeft("checking the password")
}

// Only a signed-in person or an API token may write tasks: the "agent" caller
// Clawnify had no longer exists.
func TestMayWrite(t *testing.T) {
	for caller, want := range map[string]bool{"user": true, "api": true, "agent": false, "agent-browser": false, "public": false, "": false} {
		r := httptest.NewRequest("POST", "/api/tasks", nil)
		if caller != "" {
			r = web.WithIdentity(r, caller, &web.User{ID: "u1", Email: "ada@example.com"})
		}
		if got := mayWrite(r); got != want {
			t.Errorf("mayWrite(%q) = %v", caller, got)
		}
	}
}

// guardDriver lets a test run something just before a statement, as a
// concurrent request that lands first would.
type guardDriver struct {
	db.Driver
	before func(sql string)
}

func (g *guardDriver) Run(ctx context.Context, sql string, params []any) (db.Result, error) {
	if g.before != nil {
		g.before(sql)
	}
	return g.Driver.Run(ctx, sql, params)
}

func (g *guardDriver) Query(ctx context.Context, sql string, params []any) ([]db.Row, error) {
	if g.before != nil {
		g.before(sql)
	}
	return g.Driver.Query(ctx, sql, params)
}

// A login that checked the old password must not start a session once the
// password changed (or the account was disabled) in the meantime: the
// change's sign-out everywhere would have missed it.
func TestAuthLoginRacingAPasswordChange(t *testing.T) {
	for name, change := range map[string]string{
		"new password": "UPDATE users SET password_hash = 'changed' WHERE id = 'u-ada'",
		"disabled":     "UPDATE users SET disabled = 1 WHERE id = 'u-ada'",
	} {
		t.Run(name, func(t *testing.T) {
			rec := recordedDB(t, nil)
			a := &authAPI{t: t, ctx: context.Background(), app: &App{}}
			a.h = New(a.app)
			a.user("u-ada", "ada@example.com", "admin", false)
			g := &guardDriver{Driver: rec.inner}
			db.Use(g)
			g.before = func(sql string) {
				if strings.Contains(sql, "INSERT INTO sessions") {
					g.before = nil
					if _, err := rec.inner.Run(a.ctx, change, nil); err != nil {
						t.Fatal(err)
					}
				}
			}
			res := a.do("POST", "/api/auth/login", map[string]any{"email": "ada@example.com", "password": testPassword}, nil)
			if res.code != 401 || res.body != wrongLogin || res.header.Get("Set-Cookie") != "" {
				t.Errorf("login = %s %v", res, res.header.Values("Set-Cookie"))
			}
			if n := a.count("sessions"); n != 0 {
				t.Errorf("%d sessions", n)
			}
		})
	}
}

// A stolen session can't mint a token that outlives the admin's answer to
// it: a POST /api/tokens signed in before a password reset or a disabling,
// whose INSERT lands after it, must not leave a token behind. The INSERT
// itself checks the session is still live and its account enabled.
func TestTokenRacingAResetOrADisable(t *testing.T) {
	for name, admin := range map[string][]string{
		"password reset": {
			"UPDATE users SET password_hash = 'reset' WHERE id = 'u-bo'",
			"DELETE FROM sessions WHERE user_id = 'u-bo'",
			"DELETE FROM api_tokens WHERE user_id = 'u-bo'",
		},
		"disable": {
			"UPDATE users SET disabled = 1 WHERE id = 'u-bo'",
			"DELETE FROM sessions WHERE user_id = 'u-bo'",
			"DELETE FROM api_tokens WHERE user_id = 'u-bo'",
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := recordedDB(t, nil)
			a := &authAPI{t: t, ctx: context.Background(), app: &App{}}
			a.h = New(a.app)
			a.user("u-ada", "ada@example.com", "admin", false)
			a.user("u-bo", "bo@example.com", "user", false)
			bo := a.login("bo@example.com", testPassword)
			g := &guardDriver{Driver: rec.inner}
			db.Use(g)
			g.before = func(sql string) {
				if strings.Contains(sql, "INSERT INTO api_tokens") {
					g.before = nil
					for _, st := range admin { // the admin's request, landing first
						if _, err := rec.inner.Run(a.ctx, st, nil); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			res := a.do("POST", "/api/tokens", map[string]any{"name": "keep-me"}, asSession(bo))
			if res.code != 401 || res.body != unauthorizedBody || strings.Contains(res.body, "crm_") {
				t.Errorf("token minted across the %s = %s", name, res)
			}
			if n := a.count("api_tokens"); n != 0 {
				t.Errorf("%d tokens left", n)
			}
			// Enabled again, nothing comes back.
			if _, err := db.Run(a.ctx, "UPDATE users SET disabled = 0 WHERE id = 'u-bo'"); err != nil {
				t.Fatal(err)
			}
			if n := a.count("api_tokens"); n != 0 {
				t.Errorf("%d tokens after re-enabling", n)
			}
		})
	}
}

// Every login asks the database the same questions, whether the email has an
// account or not: the time it takes says nothing more than its answer.
func TestAuthLoginQueriesDontDependOnTheEmail(t *testing.T) {
	statements := func(email string) []string {
		rec := recordedDB(t, nil)
		a := &authAPI{t: t, ctx: context.Background(), app: &App{}}
		a.h = New(a.app)
		a.user("u-ada", "ada@example.com", "admin", false)
		rec.mu.Lock()
		rec.log = nil
		rec.mu.Unlock()
		a.do("POST", "/api/auth/login", map[string]any{"email": email, "password": "wrong-password"}, nil)
		rec.mu.Lock()
		defer rec.mu.Unlock()
		var sqls []string
		for _, st := range rec.log {
			sqls = append(sqls, st.SQL)
		}
		return sqls
	}
	known, unknown := statements("ada@example.com"), statements("nobody@example.com")
	if strings.Join(known, "\n") != strings.Join(unknown, "\n") {
		t.Errorf("known email:\n%q\nunknown email:\n%q", known, unknown)
	}
}

// Two admins demote (or disable, or delete) each other at the same time:
// each request sees two active admins, but the last one must stay. The check
// and the change are one statement, so the second write finds no other admin.
func TestUsersLastActiveAdminStays(t *testing.T) {
	for _, c := range []struct {
		name   string
		method string
		body   any
	}{
		{"demote", "PATCH", map[string]any{"role": "user"}},
		{"disable", "PATCH", map[string]any{"disabled": true}},
		{"delete", "DELETE", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := recordedDB(t, nil)
			a := &authAPI{t: t, ctx: context.Background(), app: &App{}}
			a.h = New(a.app)
			a.user("u-ada", "ada@example.com", "admin", false)
			a.user("u-cat", "cat@example.com", "admin", false)
			a.user("u-bo", "bo@example.com", "admin", true) // disabled: not an active admin
			ada := a.login("ada@example.com", testPassword)

			g := &guardDriver{Driver: rec.inner}
			db.Use(g)
			g.before = func(sql string) {
				if strings.HasPrefix(sql, "UPDATE users") || strings.HasPrefix(sql, "DELETE FROM users") {
					g.before = nil
					// cat's request, a moment earlier: ada is demoted.
					if _, err := rec.inner.Run(a.ctx, "UPDATE users SET role = 'user' WHERE id = 'u-ada'", nil); err != nil {
						t.Fatal(err)
					}
				}
			}
			res := a.do(c.method, "/api/users/u-cat", c.body, asSession(ada))
			if res.code != 400 || res.body != lastAdmin {
				t.Errorf("%s the last active admin = %s", c.name, res)
			}
			if n := a.count("users WHERE role = 'admin' AND disabled = 0"); n != 1 {
				t.Errorf("%d active admins left", n)
			}
		})
	}
}

// Without credentials, every /api route but the login and the OpenAPI
// document answers 401, and so does any other /api path; llms.txt and the
// pages stay public.
func TestEveryAPIRouteNeedsAnAccount(t *testing.T) {
	a := newAuthAPI(t, &App{AdminEmail: "ada@example.com", AdminPassword: "password-1"})
	public := map[string]bool{"POST /api/auth/login": true, "GET /api/openapi.json": true, "GET /llms.txt": true}
	params := regexp.MustCompile(`\{[^}]*\}`)
	patterns := (&App{}).router().Patterns()
	if len(patterns) < 60 {
		t.Fatalf("only %d routes", len(patterns))
	}
	for _, p := range patterns {
		method, path, _ := strings.Cut(p, " ")
		path = params.ReplaceAllString(path, "x1")
		res := a.do(method, path, "{}", nil)
		if public[p] {
			if res.body == unauthorizedBody {
				t.Errorf("%s is public but answered %s", p, res)
			}
			continue
		}
		if res.code != 401 || res.body != unauthorizedBody || res.header.Get("Content-Type") != "application/json" {
			t.Errorf("%s = %s", p, res)
		}
	}
	for _, p := range []string{"GET /api/nope", "POST /api/companies/x1/nope", "GET /api", "DELETE /api/auth/login", "PATCH /api/companies"} {
		method, path, _ := strings.Cut(p, " ")
		if res := a.do(method, path, nil, nil); res.code != 401 || res.body != unauthorizedBody {
			t.Errorf("%s = %s", p, res)
		}
	}
	if res := a.do("GET", "/nope", nil, nil); res.code != 404 || res.body != "404 Not Found" {
		t.Errorf("GET /nope = %s", res)
	}
	if n := a.count("companies") + a.count("tasks") + a.count("stages"); n != 0 {
		t.Errorf("anonymous requests wrote %d rows", n)
	}
}
