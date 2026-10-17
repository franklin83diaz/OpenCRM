// Package auth is how the CRM knows who is calling: email and password
// sign-in with a session cookie for people, personal API tokens for agents
// and scripts, and the middleware in front of every /api route that turns
// either one into the account on the request's context (web.CurrentUser).
package auth

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"opencrm/internal/db"
	"opencrm/internal/web"
)

const (
	// CookieName is the session cookie. Its value is a NewSecret("") secret;
	// the sessions table keeps only its hash.
	CookieName = "crm_session"
	// TokenPrefix starts every API token, so a leaked one is easy to spot
	// (and to grep for).
	TokenPrefix = "crm_"
	// SessionMaxAge is how long a session lasts, in seconds: 30 days.
	SessionMaxAge = 30 * 24 * 60 * 60
	// sessionLife is SessionMaxAge as a SQLite datetime modifier.
	sessionLife = "+30 days"
)

// User is the signed-in account (web.User, where the routes read it).
type User = web.User

// FromContext is the account the middleware signed the request in as, and
// how: "user" for a session cookie, "api" for an API token, "" for nobody.
func FromContext(ctx context.Context) (*User, string) {
	caller, u := web.IdentityFrom(ctx)
	return u, caller
}

type errorBody struct {
	Error string `json:"error"`
}

type unauthorizedBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Middleware lets a request through to next once it is signed in. Every /api
// path needs a session cookie or an API token (Authorization: Bearer crm_…),
// except the login itself and the OpenAPI document; anything else (/llms.txt,
// the pages) passes untouched. Without credentials the answer is 401 — for
// unknown /api paths too, so nobody maps the API without an account. A
// cookie-signed write (and a login) from another site is refused with 403.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPI(r) {
			next.ServeHTTP(w, r)
			return
		}
		if login := isLogin(r); login || isOpenAPI(r) {
			// Public: not signed in here, even with credentials. A login is a
			// browser form, so another site's page posting it is refused like
			// a cookie-signed write (it would sign the visitor in as someone else).
			if login && crossSite(r) {
				web.JSON(w, http.StatusForbidden, errorBody{"Cross-site request refused"})
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		user, caller, err := authenticate(r)
		if err != nil {
			log.Printf("auth: %s %s: %v", r.Method, r.URL.Path, err)
			web.Text(w, http.StatusInternalServerError, "text/plain; charset=UTF-8", "Internal Server Error")
			return
		}
		if user == nil {
			Unauthorized(w)
			return
		}
		// The browser attaches the cookie to whatever sends a request to this
		// host, other sites' pages included; a token it never attaches.
		if caller == "user" && !safeMethod(r.Method) && crossSite(r) {
			web.JSON(w, http.StatusForbidden, errorBody{"Cross-site request refused"})
			return
		}
		next.ServeHTTP(w, web.WithIdentity(r, caller, user))
	})
}

// Unauthorized answers 401: the request is signed in as nobody (or no
// longer is).
func Unauthorized(w http.ResponseWriter) error {
	w.Header().Set("WWW-Authenticate", `Bearer realm="OpenCRM"`)
	return web.JSON(w, http.StatusUnauthorized, unauthorizedBody{"unauthorized", "Sign in to use the CRM."})
}

// isAPI is whether the path is the API's, decoded as the router decodes it
// ("/api", "/api/…", "/%61pi/…"). The router never serves a path that isn't
// clean ("//api/x", "/api/../x"), so those can't reach an /api route either.
func isAPI(r *http.Request) bool {
	return r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/")
}

// isLogin and isOpenAPI compare the path as sent: "/api/auth%2Flogin" decodes
// to the login's path but the router doesn't take it for the login route.
func isLogin(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.EscapedPath() == "/api/auth/login"
}

func isOpenAPI(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.EscapedPath() == "/api/openapi.json"
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// crossSite is whether a browser sent r from another site's page. Browsers
// say where a request comes from in Sec-Fetch-Site; only "same-origin" (this
// app's own page) and "none" (typed or bookmarked) are ours — "same-site" is
// a sibling subdomain, which is not this app. Older browsers send only
// Origin, whose host must be the one the browser used (web.Origin). A request
// with neither isn't from a browser page (curl, a script), and no other site
// can make a browser send one. Sec-Fetch-Site is asked first because the
// Vite dev server proxies /api to wrangler with changeOrigin: its pages'
// Origin (localhost:5175) never matches the forwarded host (localhost:8787).
func crossSite(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site != "same-origin" && site != "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	o, err := url.Parse(origin)
	if err != nil || o.Host == "" { // "null": a sandboxed frame, a file
		return true
	}
	self, err := url.Parse(web.Origin(r))
	return err != nil || !strings.EqualFold(o.Host, self.Host)
}

// authenticate finds the account behind r's credentials. An Authorization:
// Bearer header decides alone — a bad token is not rescued by a good cookie;
// any other Authorization scheme leaves the cookie to decide.
func authenticate(r *http.Request) (*User, string, error) {
	ctx := r.Context()
	if tok, ok := bearer(r); ok {
		u, err := TokenUser(ctx, tok)
		return u, "api", err
	}
	if tok := SessionToken(r); tok != "" {
		u, err := SessionUser(ctx, tok)
		return u, "user", err
	}
	return nil, "", nil
}

func bearer(r *http.Request) (string, bool) {
	scheme, tok, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(tok), true
}

// SessionToken is the session cookie's value, "" without one.
func SessionToken(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

const userColumns = "u.id, u.email, u.name, u.role"

func userOf(row db.Row) *User {
	if row == nil {
		return nil
	}
	str := func(k string) string { s, _ := row[k].(string); return s }
	return &User{ID: str("id"), Email: str("email"), Name: str("name"), Role: str("role")}
}

// SessionUser is the account a session cookie signs in: nil when there is no
// such session, it expired, or its account is disabled.
func SessionUser(ctx context.Context, token string) (*User, error) {
	if token == "" {
		return nil, nil
	}
	row, err := db.Get(ctx, `SELECT `+userColumns+` FROM sessions s JOIN users u ON u.id = s.user_id
     WHERE s.token_hash = ? AND s.expires_at > datetime('now') AND u.disabled = 0`, HashSecret(token))
	return userOf(row), err
}

// TokenUser is the account an API token acts as: nil when there is no such
// token or its account is disabled. It notes when the token was used, at most
// once a minute, so a busy script doesn't write on every call.
func TokenUser(ctx context.Context, token string) (*User, error) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return nil, nil
	}
	row, err := db.Get(ctx, `SELECT t.id AS token_id, `+userColumns+`,
       (t.last_used_at IS NULL OR t.last_used_at < datetime('now', '-1 minute')) AS stale
     FROM api_tokens t JOIN users u ON u.id = t.user_id
     WHERE t.token_hash = ? AND u.disabled = 0`, HashSecret(token))
	if err != nil || row == nil {
		return nil, err
	}
	if stale, _ := row["stale"].(float64); stale != 0 {
		// Only a note for the token's owner: failing to write it doesn't
		// make the token any less valid.
		if _, err := db.Run(ctx, "UPDATE api_tokens SET last_used_at = datetime('now') WHERE id = ?", row["token_id"]); err != nil {
			log.Printf("auth: noting a token's use: %v", err)
		}
	}
	return userOf(row), nil
}

// StartSession signs userID in: a new session in the database and its cookie
// on w. The value is always new, never one the browser sent, so nobody can
// plant a session id for someone else to sign in to. passwordHash is the hash
// the password was checked against: the session is made only while the
// account still has it and isn't disabled, in the same statement, so a login
// that raced a password change or a disabling (whose sign-out everywhere ran
// in between) starts nothing; ok is false then. Expired sessions are swept on
// the way.
func StartSession(ctx context.Context, w http.ResponseWriter, userID, passwordHash string) (ok bool, err error) {
	if _, err := db.Run(ctx, "DELETE FROM sessions WHERE expires_at <= datetime('now')"); err != nil {
		return false, err
	}
	tok, hash := NewSecret("")
	res, err := db.Run(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at)
     SELECT ?, id, datetime('now', ?) FROM users WHERE id = ? AND password_hash = ? AND disabled = 0`, hash, sessionLife, userID, passwordHash)
	if err != nil || res.Changes == 0 {
		return false, err
	}
	setCookie(w, tok, SessionMaxAge)
	return true, nil
}

// EndSession deletes the session r's cookie names, if any, and tells the
// browser to drop the cookie.
func EndSession(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if err := DeleteSession(ctx, SessionToken(r)); err != nil {
		return err
	}
	setCookie(w, "", -1)
	return nil
}

// DeleteSession deletes the session whose cookie value is token, if any.
func DeleteSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	_, err := db.Run(ctx, "DELETE FROM sessions WHERE token_hash = ?", HashSecret(token))
	return err
}

// DeleteSessions signs userID out everywhere but the session whose cookie
// value is keep ("" keeps none).
func DeleteSessions(ctx context.Context, userID, keep string) error {
	if keep == "" {
		_, err := db.Run(ctx, "DELETE FROM sessions WHERE user_id = ?", userID)
		return err
	}
	_, err := db.Run(ctx, "DELETE FROM sessions WHERE user_id = ? AND token_hash != ?", userID, HashSecret(keep))
	return err
}

// setCookie writes the session cookie: only for this app's requests
// (HttpOnly: no script reads it; Secure: HTTPS only, which browsers waive for
// http://localhost; SameSite=Lax: other sites' requests go without it, but for
// following a link here). maxAge < 0 deletes it.
func setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// pseudoIPv4 is the Class E range Cloudflare's Pseudo IPv4 makes addresses in.
var pseudoIPv4 = netip.MustParsePrefix("240.0.0.0/4")

// ClientIP is the address a request came from, for the login limiter.
// Cloudflare puts the visitor's address in CF-Connecting-IP (a client can't
// set it: the edge always overwrites it) and the Worker passes it on; the
// container itself only sees the Worker's connection. With Cloudflare's
// Pseudo IPv4 on, that address is a made-up Class E one and the real one,
// which the limiter groups by /64, is in CF-Connecting-IPv6. That header is
// believed only then: Cloudflare sets it only for Pseudo IPv4 and doesn't
// say it drops one a client sent. Without CF-Connecting-IP (the dev server),
// it is the connection's address.
func ClientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		if a, err := netip.ParseAddr(ip); err == nil && a.Is4() && pseudoIPv4.Contains(a) {
			v6 := strings.TrimSpace(r.Header.Get("CF-Connecting-IPv6"))
			if b, err := netip.ParseAddr(v6); err == nil && b.Is6() && !b.Is4In6() {
				return v6
			}
		}
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
