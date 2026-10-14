package web

import (
	"net/http"
	"strings"
)

// Clawnify's proxy tells the app who is calling in X-Clawnify-* headers; these
// read them as @clawnify/app's caller(), orgId() and user() did.

var callers = map[string]bool{
	"user": true, "api": true, "agent": true, "agent-browser": true,
	"public": true, "bypass": true, "system": true, "app": true,
}

// header is the trimmed header value, "" when absent or blank.
func header(r *http.Request, name string) string {
	return strings.TrimSpace(strings.Join(r.Header.Values(name), ", "))
}

// Caller is who is calling: one of user, api, agent, agent-browser, public,
// bypass, system, app. Anything else, or nothing, is "public".
func Caller(r *http.Request) string {
	if v := header(r, "X-Clawnify-Caller"); callers[v] {
		return v
	}
	return "public"
}

// OrgID is the calling org, "" when the request carries none.
func OrgID(r *http.Request) string {
	return header(r, "X-Clawnify-Org-Id")
}

// User is the signed-in person behind a request. Absent fields are nil (null).
type User struct {
	ID        string  `json:"id"`
	Email     *string `json:"email"`
	Name      *string `json:"name"`
	AvatarURL *string `json:"avatarUrl"`
	FirstName *string `json:"firstName"`
	LastName  *string `json:"lastName"`
}

// CurrentUser is the signed-in user, or nil when the request names none.
func CurrentUser(r *http.Request) *User {
	id := header(r, "X-Clawnify-User-Id")
	if id == "" {
		return nil
	}
	opt := func(name string) *string {
		if v := header(r, name); v != "" {
			return &v
		}
		return nil
	}
	u := &User{
		ID:        id,
		Email:     opt("X-Clawnify-User-Email"),
		Name:      opt("X-Clawnify-User-Name"),
		AvatarURL: opt("X-Clawnify-User-Avatar"),
	}
	// First word is the first name, the rest the last name.
	if u.Name != nil {
		if parts := strings.Fields(*u.Name); len(parts) > 0 {
			u.FirstName = &parts[0]
			if len(parts) > 1 {
				last := strings.Join(parts[1:], " ")
				u.LastName = &last
			}
		}
	}
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
