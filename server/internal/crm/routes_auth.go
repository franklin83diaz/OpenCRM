package crm

// The CRM's own accounts: signing in with an email and a password (a session
// cookie), personal API tokens for agents and scripts, and the admin's
// account management. internal/auth's middleware signs each request in; these
// routes make and end what it checks. There is no sign-up: an admin creates
// the accounts, and the first admin comes from the ADMIN_EMAIL and
// ADMIN_PASSWORD secrets.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"opencrm/internal/auth"
	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

const (
	errWrongLogin      = "Wrong email or password"
	errTooMany         = "Too many attempts. Try again in a few minutes."
	errNoAccounts      = "No accounts yet: set ADMIN_EMAIL and ADMIN_PASSWORD and restart the CRM"
	errCurrentPassword = "Your current password is wrong"
	errAdminsOnly      = "Only an admin can manage accounts"
	errEmailTaken      = "An account with this email already exists"
	errLastAdmin       = "The CRM needs at least one active admin"
	errNotYourself     = "You can't delete, disable or demote your own account"
	errEmail           = "Enter a valid email address"
	errRole            = "A role is admin or user"
	errName            = "A name can have at most 100 characters"
	errDisabled        = "disabled is true or false"
	errNoAccount       = "Account not found"
	errTokenName       = "A token name needs 1 to 60 characters"
	errNoToken         = "Token not found"
)

const (
	maxName      = 100
	maxTokenName = 60
	maxEmail     = 254
	// maxLoginBody bounds what an anonymous caller can make the login read.
	maxLoginBody = 64 << 10
)

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("POST /api/auth/login", app.login)
		rt.Handle("POST /api/auth/logout", app.logout)
		rt.Handle("GET /api/auth/me", app.me)
		rt.Handle("PUT /api/auth/password", app.changePassword)
		rt.Handle("GET /api/users", app.listUsers)
		rt.Handle("POST /api/users", app.createUser)
		rt.Handle("PATCH /api/users/{id}", app.updateUser)
		rt.Handle("DELETE /api/users/{id}", app.deleteUser)
		rt.Handle("GET /api/tokens", app.listTokens)
		rt.Handle("POST /api/tokens", app.createToken)
		rt.Handle("DELETE /api/tokens/{id}", app.deleteToken)
	})
}

type userBody struct {
	User *web.User `json:"user"`
}

// member is an account as the admin's list shows it: never the password hash.
type member struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	Disabled  bool   `json:"disabled"`
	CreatedAt any    `json:"created_at"`
	UpdatedAt any    `json:"updated_at"`
}

const memberColumns = "id, email, name, role, disabled, created_at, updated_at"

func memberOf(row db.Row) member {
	str := func(k string) string { s, _ := row[k].(string); return s }
	return member{
		ID: str("id"), Email: str("email"), Name: str("name"), Role: str("role"),
		Disabled: js.Number(row["disabled"]) != 0, CreatedAt: row["created_at"], UpdatedAt: row["updated_at"],
	}
}

type memberBody struct {
	User member `json:"user"`
}

// apiToken is a token as its owner sees it: what it's called and the start of
// the secret, never the secret or its hash.
type apiToken struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	LastUsedAt any    `json:"last_used_at"`
	CreatedAt  any    `json:"created_at"`
}

const tokenColumns = "id, name, prefix, last_used_at, created_at"

func tokenOf(row db.Row) apiToken {
	str := func(k string) string { s, _ := row[k].(string); return s }
	return apiToken{ID: str("id"), Name: str("name"), Prefix: str("prefix"), LastUsedAt: row["last_used_at"], CreatedAt: row["created_at"]}
}

func jsonError(w http.ResponseWriter, status int, msg string) error {
	return web.JSON(w, status, errorBody{msg})
}

// accountEmail is an email as accounts keep it, trimmed and in lower case, and
// whether it looks like one: an @, no spaces, at most 254 bytes.
func accountEmail(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	s = strings.ToLower(strings.TrimSpace(s))
	return s, strings.Contains(s, "@") && len(s) <= maxEmail && !strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

// accountName is a name trimmed, and whether it is one: text of at most 100
// characters. No name (absent or null) is "".
func accountName(v any) (string, bool) {
	if v == nil {
		return "", true
	}
	s, ok := v.(string)
	s = strings.TrimSpace(s)
	return s, ok && utf8.RuneCountInString(s) <= maxName
}

func validRole(v any) (string, bool) {
	s, _ := v.(string)
	return s, s == "admin" || s == "user"
}

// ── Signing in ──

// ensureAdmin creates the first admin from AdminEmail and AdminPassword while
// the CRM has no accounts. Like the backfills it runs until it has passed
// once in this process: the secrets only seed an empty CRM, and changing them
// later changes nothing (change the password in the CRM instead). Secrets
// that can't make an account are logged and leave the CRM without one.
func (app *App) ensureAdmin(ctx context.Context) error {
	if app.done.adminChecked.Load() {
		return nil
	}
	app.adminMu.Lock()
	defer app.adminMu.Unlock()
	if app.done.adminChecked.Load() {
		return nil
	}
	if app.AdminEmail == "" || app.AdminPassword == "" {
		app.done.adminChecked.Store(true)
		return nil
	}
	row, err := db.Get(ctx, "SELECT COUNT(*) AS n FROM users")
	if err != nil {
		return err
	}
	if js.Number(row["n"]) == 0 {
		email, ok := accountEmail(app.AdminEmail)
		hash, err := auth.HashPassword(app.AdminPassword)
		switch {
		case !ok:
			log.Printf("auth: ADMIN_EMAIL is not an email address; no admin created")
		case errors.Is(err, auth.ErrPasswordLength):
			log.Printf("auth: ADMIN_PASSWORD needs 8 to 72 bytes; no admin created")
		case err != nil:
			return err
		default:
			// Only into an empty table, in the same statement that checks it.
			if _, err := db.Run(ctx, `INSERT INTO users (id, email, name, role, password_hash)
           SELECT ?, ?, '', 'admin', ? WHERE NOT EXISTS (SELECT 1 FROM users)`, js.UUID(), email, hash); err != nil {
				return err
			}
			log.Printf("auth: created the first admin, %s", email)
		}
	}
	app.done.adminChecked.Store(true)
	return nil
}

// BootstrapToken makes secret an API token of an active admin: the dev
// server's -bootstrap-token, which the parity harness signs its calls with.
// The admin is the oldest active one (after ensureAdmin), else
// admin@localhost, made with a password nobody knows. Running it again with
// the same secret changes nothing.
func (app *App) BootstrapToken(ctx context.Context, secret string) error {
	if !strings.HasPrefix(secret, auth.TokenPrefix) || len(secret) < len(auth.TokenPrefix)+32 {
		return fmt.Errorf("a bootstrap token is %s followed by at least 32 characters", auth.TokenPrefix)
	}
	if err := app.ensureAdmin(ctx); err != nil {
		return err
	}
	const oldestAdmin = "SELECT id FROM users WHERE role = 'admin' AND disabled = 0 ORDER BY created_at, id LIMIT 1"
	row, err := db.Get(ctx, oldestAdmin)
	if err != nil {
		return err
	}
	if row == nil {
		pw, _ := auth.NewSecret("")
		hash, err := auth.HashPassword(pw[:40])
		if err != nil {
			return err
		}
		if _, err := db.Run(ctx, "INSERT INTO users (id, email, name, role, password_hash) VALUES (?, 'admin@localhost', 'Admin', 'admin', ?) ON CONFLICT(email) DO NOTHING", js.UUID(), hash); err != nil {
			return err
		}
		if row, err = db.Get(ctx, oldestAdmin); err != nil {
			return err
		}
		if row == nil {
			return errors.New("admin@localhost exists but is not an active admin")
		}
	}
	_, err = db.Run(ctx, "INSERT INTO api_tokens (id, user_id, name, token_hash, prefix) VALUES (?, ?, 'bootstrap', ?, ?) ON CONFLICT(token_hash) DO NOTHING",
		js.UUID(), row["id"], auth.HashSecret(secret), secret[:12])
	return err
}

// login signs in with {email, password}: the account and a session cookie.
// A wrong password, an unknown email and a disabled account get the same
// answer after the same work (one bcrypt check), so it doesn't say which
// emails have accounts. Attempts are limited per address and per email.
func (app *App) login(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	body := web.Object(web.Body(r))
	email, _ := accountEmail(body["email"])
	password, _ := body["password"].(string)
	ctx := r.Context()
	if !app.limiter().Allow(auth.ClientIP(r), email) {
		return jsonError(w, http.StatusTooManyRequests, errTooMany)
	}
	if err := app.ensureAdmin(ctx); err != nil {
		return err
	}
	// Asked for every login, known email or not, so that the work (and the
	// time) is the same either way.
	some, err := db.Get(ctx, "SELECT 1 AS one FROM users LIMIT 1")
	if err != nil {
		return err
	}
	if some == nil {
		return jsonError(w, http.StatusServiceUnavailable, errNoAccounts)
	}
	row, err := db.Get(ctx, "SELECT id, email, name, role, password_hash, disabled FROM users WHERE email = ?", email)
	if err != nil {
		return err
	}
	var hash string
	if row != nil {
		hash, _ = row["password_hash"].(string)
	}
	if ok := auth.CheckPassword(hash, password); !ok || row == nil || js.Number(row["disabled"]) != 0 {
		return jsonError(w, http.StatusUnauthorized, errWrongLogin)
	}
	// The session the browser came with, if any, ends: a sign-in always
	// starts a new one.
	if err := auth.DeleteSession(ctx, auth.SessionToken(r)); err != nil {
		return err
	}
	id, _ := row["id"].(string)
	started, err := auth.StartSession(ctx, w, id, hash)
	if err != nil {
		return err
	}
	if !started { // the password changed, or the account was disabled, meanwhile
		return jsonError(w, http.StatusUnauthorized, errWrongLogin)
	}
	app.limiter().Succeeded(email)
	u := memberOf(row)
	return web.JSON(w, http.StatusOK, userBody{&web.User{ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role}})
}

// logout ends this session and drops its cookie.
func (app *App) logout(w http.ResponseWriter, r *http.Request) error {
	if err := auth.EndSession(r.Context(), w, r); err != nil {
		return err
	}
	return web.JSON(w, http.StatusOK, okBody{true})
}

// me is the signed-in account.
func (app *App) me(w http.ResponseWriter, r *http.Request) error {
	return web.JSON(w, http.StatusOK, userBody{web.CurrentUser(r)})
}

// changePassword sets a new password after checking the current one (guesses
// count against the login limiter), and signs the account out everywhere
// else: a session that was stolen ends with the old password.
func (app *App) changePassword(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	me := web.CurrentUser(r)
	body := web.Object(web.Body(r))
	current, _ := body["current_password"].(string)
	next, _ := body["new_password"].(string)
	if !auth.ValidPassword(next) {
		return jsonError(w, http.StatusBadRequest, auth.ErrPasswordLength.Error())
	}
	if !app.limiter().Allow(auth.ClientIP(r), me.Email) {
		return jsonError(w, http.StatusTooManyRequests, errTooMany)
	}
	row, err := db.Get(ctx, "SELECT password_hash FROM users WHERE id = ?", me.ID)
	if err != nil {
		return err
	}
	var hash string
	if row != nil {
		hash, _ = row["password_hash"].(string)
	}
	if !auth.CheckPassword(hash, current) {
		return jsonError(w, http.StatusBadRequest, errCurrentPassword)
	}
	app.limiter().Succeeded(me.Email)
	newHash, err := auth.HashPassword(next)
	if err != nil {
		return err
	}
	if _, err := db.Run(ctx, "UPDATE users SET password_hash = ?, updated_at = datetime('now') WHERE id = ?", newHash, me.ID); err != nil {
		return err
	}
	if err := auth.DeleteSessions(ctx, me.ID, thisSession(r)); err != nil {
		return err
	}
	return web.JSON(w, http.StatusOK, okBody{true})
}

// thisSession is the request's session cookie value when it was signed in by
// one ("" for a token), so a change of password keeps it.
func thisSession(r *http.Request) string {
	if web.Caller(r) != "user" {
		return ""
	}
	return auth.SessionToken(r)
}

// ── Accounts (admin only) ──

// adminOnly answers 403 unless the caller is an admin.
func adminOnly(w http.ResponseWriter, r *http.Request) (*web.User, bool) {
	me := web.CurrentUser(r)
	if me == nil || me.Role != "admin" {
		jsonError(w, http.StatusForbidden, errAdminsOnly)
		return nil, false
	}
	return me, true
}

func readMember(ctx context.Context, id string) (*member, error) {
	row, err := db.Get(ctx, "SELECT "+memberColumns+" FROM users WHERE id = ?", id)
	if err != nil || row == nil {
		return nil, err
	}
	a := memberOf(row)
	return &a, nil
}

// keepsAnAdmin is the condition, on the row being changed, that the CRM
// keeps an active admin afterwards: the row isn't one, or another is. It is
// part of the UPDATE or DELETE itself, so two admins demoting each other at
// once can't both succeed: whichever runs second finds no other admin.
const keepsAnAdmin = "(role != 'admin' OR disabled = 1 OR EXISTS (SELECT 1 FROM users WHERE role = 'admin' AND disabled = 0 AND id != ?))"

func (app *App) listUsers(w http.ResponseWriter, r *http.Request) error {
	if _, ok := adminOnly(w, r); !ok {
		return nil
	}
	rows, err := db.Query(r.Context(), "SELECT "+memberColumns+" FROM users ORDER BY email")
	if err != nil {
		return err
	}
	users := make([]member, len(rows))
	for i, row := range rows {
		users[i] = memberOf(row)
	}
	return web.JSON(w, http.StatusOK, struct {
		Users []member `json:"users"`
	}{users})
}

// createUser creates an account: {email, name?, password, role?}.
func (app *App) createUser(w http.ResponseWriter, r *http.Request) error {
	if _, ok := adminOnly(w, r); !ok {
		return nil
	}
	ctx := r.Context()
	body := web.Object(web.Body(r))
	email, ok := accountEmail(body["email"])
	if !ok {
		return jsonError(w, http.StatusBadRequest, errEmail)
	}
	name, ok := accountName(body["name"])
	if !ok {
		return jsonError(w, http.StatusBadRequest, errName)
	}
	role := "user"
	if v := body["role"]; v != nil {
		if role, ok = validRole(v); !ok {
			return jsonError(w, http.StatusBadRequest, errRole)
		}
	}
	password, _ := body["password"].(string)
	hash, err := auth.HashPassword(password)
	if errors.Is(err, auth.ErrPasswordLength) {
		return jsonError(w, http.StatusBadRequest, err.Error())
	}
	if err != nil {
		return err
	}
	id := js.UUID()
	res, err := db.Run(ctx, "INSERT INTO users (id, email, name, role, password_hash) VALUES (?, ?, ?, ?, ?) ON CONFLICT(email) DO NOTHING",
		id, email, name, role, hash)
	if err != nil {
		return err
	}
	if res.Changes == 0 {
		return jsonError(w, http.StatusConflict, errEmailTaken)
	}
	created, err := readMember(ctx, id)
	if err != nil {
		return err
	}
	return web.JSON(w, http.StatusCreated, memberBody{*created})
}

// updateUser changes an account: {name?, role?, disabled?, password?}. A new
// password or disabling it signs it out everywhere (but for the admin's own
// session, setting their own password). No admin can demote or disable
// themselves, and the last active admin stays one.
func (app *App) updateUser(w http.ResponseWriter, r *http.Request) error {
	me, ok := adminOnly(w, r)
	if !ok {
		return nil
	}
	ctx := r.Context()
	id := r.PathValue("id")
	body := web.Object(web.Body(r))
	var sets []string
	var params []any
	removesAdmin, signsOut := false, false
	if v := body["name"]; v != nil {
		name, ok := accountName(v)
		if !ok {
			return jsonError(w, http.StatusBadRequest, errName)
		}
		sets, params = append(sets, "name = ?"), append(params, name)
	}
	if v := body["role"]; v != nil {
		role, ok := validRole(v)
		if !ok {
			return jsonError(w, http.StatusBadRequest, errRole)
		}
		sets, params = append(sets, "role = ?"), append(params, role)
		removesAdmin = removesAdmin || role != "admin"
	}
	if v := body["disabled"]; v != nil {
		disabled, ok := v.(bool)
		if !ok {
			return jsonError(w, http.StatusBadRequest, errDisabled)
		}
		sets, params = append(sets, "disabled = ?"), append(params, flag(disabled))
		removesAdmin = removesAdmin || disabled
		signsOut = signsOut || disabled
	}
	var password string
	if v := body["password"]; v != nil {
		password, _ = v.(string)
		if !auth.ValidPassword(password) {
			return jsonError(w, http.StatusBadRequest, auth.ErrPasswordLength.Error())
		}
		signsOut = true
	}
	if id == me.ID && removesAdmin {
		return jsonError(w, http.StatusBadRequest, errNotYourself)
	}
	target, err := readMember(ctx, id)
	if err != nil {
		return err
	}
	if target == nil {
		return jsonError(w, http.StatusNotFound, errNoAccount)
	}
	if password != "" {
		hash, err := auth.HashPassword(password)
		if err != nil {
			return err
		}
		sets, params = append(sets, "password_hash = ?"), append(params, hash)
	}
	if len(sets) == 0 {
		return web.JSON(w, http.StatusOK, memberBody{*target})
	}
	sql := "UPDATE users SET " + strings.Join(sets, ", ") + ", updated_at = datetime('now') WHERE id = ?"
	params = append(params, id)
	if removesAdmin {
		sql += " AND " + keepsAnAdmin
		params = append(params, id)
	}
	res, err := db.Run(ctx, sql, params...)
	if err != nil {
		return err
	}
	if res.Changes == 0 {
		return app.unchanged(w, ctx, id)
	}
	if signsOut {
		keep := ""
		if id == me.ID {
			keep = thisSession(r)
		}
		if err := auth.DeleteSessions(ctx, id, keep); err != nil {
			return err
		}
	}
	updated, err := readMember(ctx, id)
	if err != nil {
		return err
	}
	return web.JSON(w, http.StatusOK, memberBody{*updated})
}

// unchanged answers a guarded write that changed no row: the account is gone
// (404), or the change would have left no active admin (400).
func (app *App) unchanged(w http.ResponseWriter, ctx context.Context, id string) error {
	still, err := readMember(ctx, id)
	if err != nil {
		return err
	}
	if still == nil {
		return jsonError(w, http.StatusNotFound, errNoAccount)
	}
	return jsonError(w, http.StatusBadRequest, errLastAdmin)
}

// deleteUser deletes an account with its sessions and tokens. What it created
// in the CRM stays (created_by keeps the email).
func (app *App) deleteUser(w http.ResponseWriter, r *http.Request) error {
	me, ok := adminOnly(w, r)
	if !ok {
		return nil
	}
	ctx := r.Context()
	id := r.PathValue("id")
	if id == me.ID {
		return jsonError(w, http.StatusBadRequest, errNotYourself)
	}
	res, err := db.Run(ctx, "DELETE FROM users WHERE id = ? AND "+keepsAnAdmin, id, id)
	if err != nil {
		return err
	}
	if res.Changes == 0 {
		return app.unchanged(w, ctx, id)
	}
	// The foreign keys cascade; this says so for a database without them.
	if _, err := db.Run(ctx, "DELETE FROM sessions WHERE user_id = ?", id); err != nil {
		return err
	}
	if _, err := db.Run(ctx, "DELETE FROM api_tokens WHERE user_id = ?", id); err != nil {
		return err
	}
	return web.JSON(w, http.StatusOK, okBody{true})
}

// ── API tokens (each person's own) ──

func (app *App) listTokens(w http.ResponseWriter, r *http.Request) error {
	rows, err := db.Query(r.Context(), "SELECT "+tokenColumns+" FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC, rowid DESC", web.CurrentUser(r).ID)
	if err != nil {
		return err
	}
	tokens := make([]apiToken, len(rows))
	for i, row := range rows {
		tokens[i] = tokenOf(row)
	}
	return web.JSON(w, http.StatusOK, struct {
		Tokens []apiToken `json:"tokens"`
	}{tokens})
}

// createToken makes a token, {name}. Its secret is in this answer only: the
// database keeps its hash.
func (app *App) createToken(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	body := web.Object(web.Body(r))
	name, _ := body["name"].(string)
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxTokenName {
		return jsonError(w, http.StatusBadRequest, errTokenName)
	}
	secret, hash := auth.NewSecret(auth.TokenPrefix)
	id := js.UUID()
	if _, err := db.Run(ctx, "INSERT INTO api_tokens (id, user_id, name, token_hash, prefix) VALUES (?, ?, ?, ?, ?)",
		id, web.CurrentUser(r).ID, name, hash, secret[:12]); err != nil {
		return err
	}
	row, err := db.Get(ctx, "SELECT "+tokenColumns+" FROM api_tokens WHERE id = ?", id)
	if err != nil {
		return err
	}
	return web.JSON(w, http.StatusCreated, struct {
		Token  apiToken `json:"token"`
		Secret string   `json:"secret"`
	}{tokenOf(row), secret})
}

// deleteToken deletes one of the caller's own tokens; anyone else's is 404.
func (app *App) deleteToken(w http.ResponseWriter, r *http.Request) error {
	res, err := db.Run(r.Context(), "DELETE FROM api_tokens WHERE id = ? AND user_id = ?", r.PathValue("id"), web.CurrentUser(r).ID)
	if err != nil {
		return err
	}
	if res.Changes == 0 {
		return jsonError(w, http.StatusNotFound, errNoToken)
	}
	return web.JSON(w, http.StatusOK, okBody{true})
}
