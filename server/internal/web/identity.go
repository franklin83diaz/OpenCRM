package web

import (
	"context"
	"net/http"
)

// Who is calling. The auth middleware (internal/auth) signs a request in with
// a session cookie or an API token and puts the account on its context; the
// routes read it back here. Nothing in the request itself says who is calling:
// the X-Clawnify-* headers Clawnify's proxy used to send are ignored.

// User is the signed-in account behind a request.
type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"` // "admin" | "user"
}

type identityKey struct{}

type identity struct {
	caller string
	user   *User
}

// WithIdentity is r with caller ("user" for a session, "api" for a token)
// and user on its context.
func WithIdentity(r *http.Request, caller string, user *User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), identityKey{}, identity{caller, user}))
}

// IdentityFrom is what WithIdentity put on ctx: the caller, "" when nothing
// did, and the user.
func IdentityFrom(ctx context.Context) (caller string, user *User) {
	id, _ := ctx.Value(identityKey{}).(identity)
	return id.caller, id.user
}

// Caller is who is calling: "user" for a signed-in session, "api" for an API
// token, "public" when nobody signed the request in.
func Caller(r *http.Request) string {
	if caller, _ := IdentityFrom(r.Context()); caller != "" {
		return caller
	}
	return "public"
}

// CurrentUser is the signed-in account, or nil. An API token acts as its user,
// so a token request has one too.
func CurrentUser(r *http.Request) *User {
	_, u := IdentityFrom(r.Context())
	return u
}

// Origin is new URL(c.req.url).origin: the scheme and host the browser used.
// Behind the Worker the request arrives over plain HTTP from inside the
// container, so the Worker passes the public ones in X-Forwarded-Proto/-Host.
func Origin(r *http.Request) string {
	proto, host := r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-Host")
	if proto == "" {
		proto = "http"
	}
	if host == "" {
		host = r.Host
	}
	return proto + "://" + host
}
