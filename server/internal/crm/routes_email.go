package crm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"opencrm/internal/clawnify"
	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/integrations/status", app.integrationsStatus)
		rt.Handle("POST /api/integrations/email", app.integrationsEmail)
		rt.Handle("POST /api/integrations/meeting", app.integrationsMeeting)
		rt.Handle("GET /api/email-sync", app.emailSyncState)
		rt.Handle("PUT /api/email-sync", app.emailSyncSave)
		rt.Handle("GET /api/email-sync/labels", app.emailSyncLabels)
		rt.Handle("POST /api/email-sync/run", app.emailSyncRun)
		rt.Handle("POST /api/email-sync/sync-now", app.emailSyncNow)
		rt.Handle("GET /api/contacts/{id}/emails", app.contactEmails)
		rt.Handle("GET /api/emails/{mailbox}/{id}", app.openEmail)
	})
}

// errorBody is {error: message}, the answer of every failed route here.
type errorBody struct {
	Error string `json:"error"`
}

type okBody struct {
	OK bool `json:"ok"`
}

// tryJSON runs a route body inside TS's try/catch: an error answers
// 500 {error: err.message}; otherwise status and body.
func tryJSON(w http.ResponseWriter, fn func() (int, any, error)) error {
	status, body, err := fn()
	if err != nil {
		return web.JSON(w, http.StatusInternalServerError, errorBody{errMessage(err)})
	}
	return web.JSON(w, status, body)
}

func fail(status int, msg string) (int, any, error) { return status, errorBody{msg}, nil }

// trimmedField is `(body.<key> || "").trim()`: a truthy value that is not a
// string has no trim, which TS threw as this TypeError.
func trimmedField(body map[string]any, key string) (string, error) {
	v := body[key]
	if !js.Truthy(v) {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf(`(body.%s || "").trim is not a function`, key)
	}
	return js.Trim(s), nil
}

// readNull is the TypeError of reading a property of a null body.
func readNull(key string) error {
	return fmt.Errorf("Cannot read properties of null (reading '%s')", key)
}

// ── Integrations (Clawnify connections) ────────────────────────────

// integrationsStatus: mailbox is the address email goes out from, known once
// email sync has been set up.
func (app *App) integrationsStatus(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var mailbox *string
	if a, err := CurrentAccount(ctx); err == nil && a != nil {
		mailbox = &a.Mailbox
	}
	return web.JSON(w, http.StatusOK, struct {
		IntegrationStatus
		Mailbox *string `json:"mailbox"`
	}{ConnectionStatus(ctx, app.Conn), mailbox})
}

// Send from the connected Gmail: a new email, a reply inside a synced thread
// (reply_to), or a forward of a synced email (forward). Every recipient who is
// a contact gets it on their timeline. contact_id is shorthand for "to this
// contact" and still works on its own.
const maxRecipients = 50

var (
	emailAddress = regexp.MustCompile(`^` + notAddressChar + `+@` + notAddressChar + `+\.` + notAddressChar + `+$`)
	replyPrefix  = regexp.MustCompile(`(?i)^re:` + wsClass + `*`)
	fwdPrefix    = regexp.MustCompile(`(?i)^fwd?:` + wsClass + `*`)
)

// notAddressChar is [^\s@<>,;] with JS's \s.
const notAddressChar = `[^\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}@<>,;]`

// addressList reads to/cc/bcc: lower-cased, de-duplicated addresses, or the
// first value that isn't an address (bad, as String(value) words it).
func addressList(v any) (ok []string, bad string, isBad bool) {
	if v == nil {
		return []string{}, "", false
	}
	list, isList := v.([]any)
	if !isList {
		return nil, js.String(v), true
	}
	out := []string{}
	for _, raw := range list {
		e := ""
		if s, isText := raw.(string); isText {
			e = strings.ToLower(js.Trim(s))
		}
		if !emailAddress.MatchString(e) {
			return nil, js.String(raw), true
		}
		if !contains(out, e) {
			out = append(out, e)
		}
	}
	return out, "", false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

type messageRef struct{ mailbox, id string }

func toMessageRef(v any) *messageRef {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	mailbox, ok1 := m["mailbox"].(string)
	id, ok2 := m["id"].(string)
	if !ok1 || !ok2 {
		return nil
	}
	return &messageRef{mailbox: strings.ToLower(js.Trim(mailbox)), id: js.Trim(id)}
}

// emailMeta is the activity's meta, {to, cc?} in TS's key order.
type emailMeta struct {
	To []string `json:"to"`
	Cc []string `json:"cc,omitempty"`
}

func (app *App) integrationsEmail(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		v, err := web.JSONBody(r)
		if err != nil {
			return 0, nil, err
		}
		if v == nil {
			return 0, nil, readNull("subject")
		}
		body := web.Object(v)
		subject, err := trimmedField(body, "subject")
		if err != nil {
			return 0, nil, err
		}
		text, err := trimmedField(body, "body")
		if err != nil {
			return 0, nil, err
		}
		rawReply, hasReply := body["reply_to"]
		rawForward, hasForward := body["forward"]
		var replyTo, forward *messageRef
		if hasReply {
			replyTo = toMessageRef(rawReply)
		}
		if hasForward {
			forward = toMessageRef(rawForward)
		}
		if hasReply && replyTo == nil {
			return fail(400, "reply_to must be { mailbox, id }")
		}
		if hasForward && forward == nil {
			return fail(400, "forward must be { mailbox, id }")
		}
		if replyTo != nil && forward != nil {
			return fail(400, "Send a reply or a forward, not both")
		}

		lists := map[string][]string{}
		for _, field := range []string{"to", "cc", "bcc"} {
			ok, bad, isBad := addressList(body[field])
			if isBad {
				return fail(400, fmt.Sprintf(`%s: "%s" is not an email address`, field, bad))
			}
			lists[field] = ok
		}
		to, cc, bcc := lists["to"], lists["cc"], lists["bcc"]

		contactID, err := trimmedField(body, "contact_id")
		if err != nil {
			return 0, nil, err
		}
		if contactID != "" {
			contact, err := db.Get(ctx, "SELECT lower(trim(email)) AS email FROM contacts WHERE id = ?", contactID)
			if err != nil {
				return 0, nil, err
			}
			if contact == nil {
				return fail(404, "Contact not found")
			}
			email := colText(contact["email"])
			if email == "" {
				return fail(400, "Contact has no email address")
			}
			if !contains(to, email) {
				to = append([]string{email}, to...)
			}
		}
		if len(to) == 0 {
			return fail(400, "Add at least one recipient")
		}
		if len(to)+len(cc)+len(bcc) > maxRecipients {
			return fail(400, fmt.Sprintf("At most %d recipients per email", maxRecipients))
		}

		recipients := Recipients{To: to, Cc: cc, Bcc: bcc}
		var logged string
		if replyTo != nil || forward != nil {
			ref := replyTo
			if ref == nil {
				ref = forward
			}
			known, err := KnownEmail(ctx, ref.mailbox, ref.id)
			if err != nil {
				return 0, nil, err
			}
			if known == nil {
				return fail(404, "Email not found")
			}
			// A forward carries the email's text to someone new, so it needs the mailbox to share everything.
			if forward != nil && known.Visibility != "everything" {
				return fail(403, "This mailbox shares metadata only; forward the email from Gmail")
			}
			original := ""
			if known.Visibility != "metadata" && known.Subject != nil {
				original = *known.Subject
			}
			if replyTo != nil {
				if text == "" {
					return fail(400, "Write a reply first")
				}
				if _, err := ReplyToThread(ctx, app.Conn, ReplyArgs{Recipients: recipients, ThreadID: known.ThreadID, Body: text}); err != nil {
					return 0, nil, err
				}
				logged = "Reply"
				if original != "" {
					logged = "Re: " + replyPrefix.ReplaceAllString(original, "")
				}
			} else {
				if _, err := ForwardMessage(ctx, app.Conn, ForwardArgs{Recipients: recipients, MessageID: ref.id, Note: text}); err != nil {
					return 0, nil, err
				}
				logged = "Forwarded email"
				if original != "" {
					logged = "Fwd: " + fwdPrefix.ReplaceAllString(original, "")
				}
			}
		} else {
			if subject == "" && text == "" {
				return fail(400, "A subject or body is required")
			}
			if _, err := SendEmail(ctx, app.Conn, EmailArgs{Recipients: recipients, Subject: subject, Body: text}); err != nil {
				return 0, nil, err
			}
			logged = subject
			if logged == "" {
				logged = "(no subject)"
			}
		}

		everyone := append(append(append([]string{}, to...), cc...), bcc...)
		contacts, err := db.Query(ctx, "SELECT id FROM contacts WHERE lower(trim(email)) IN ("+marks(len(everyone))+")", stringParams(everyone)...)
		if err != nil {
			return 0, nil, err
		}
		ids := newIDSet()
		for _, c := range contacts {
			ids.add(colText(c["id"]))
		}
		if contactID != "" {
			ids.add(contactID)
		}
		for _, id := range ids.order {
			LogActivity(ctx, "contact", id, "email", logged, emailMeta{To: to, Cc: cc})
		}
		return 200, okBody{true}, nil
	})
}

// meetingMeta is the activity's meta, {start, timezone}.
type meetingMeta struct {
	Start    string `json:"start"`
	Timezone string `json:"timezone"`
}

// integrationsMeeting schedules a Google Calendar meeting with a contact, then logs it.
func (app *App) integrationsMeeting(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		v, err := web.JSONBody(r)
		if err != nil {
			return 0, nil, err
		}
		if v == nil {
			return 0, nil, readNull("contact_id")
		}
		body := web.Object(v)
		contactID, err := trimmedField(body, "contact_id")
		if err != nil {
			return 0, nil, err
		}
		summary, err := trimmedField(body, "summary")
		if err != nil {
			return 0, nil, err
		}
		start, err := trimmedField(body, "start_datetime")
		if err != nil {
			return 0, nil, err
		}
		if contactID == "" {
			return fail(400, "contact_id is required")
		}
		if summary == "" {
			return fail(400, "A meeting title is required")
		}
		if start == "" {
			return fail(400, "A start time is required")
		}

		contact, err := db.Get(ctx, "SELECT email FROM contacts WHERE id = ?", contactID)
		if err != nil {
			return 0, nil, err
		}
		if contact == nil {
			return fail(404, "Contact not found")
		}

		durationMinutes := js.Number(body["duration_minutes"])
		if !js.Truthy(durationMinutes) {
			durationMinutes = 30
		}
		timezone, err := trimmedField(body, "timezone")
		if err != nil {
			return 0, nil, err
		}
		if timezone == "" {
			timezone = "UTC"
		}
		hour, minutes := math.Floor(durationMinutes/60), math.Mod(durationMinutes, 60)
		attendees := []string{}
		if email := colText(contact["email"]); email != "" {
			attendees = []string{email}
		}
		if _, err := CreateMeeting(ctx, app.Conn, MeetingArgs{
			Summary:         summary,
			StartDatetime:   start,
			Timezone:        timezone,
			DurationHour:    &hour,
			DurationMinutes: &minutes,
			Attendees:       attendees,
		}); err != nil {
			return 0, nil, err
		}
		LogActivity(ctx, "contact", contactID, "meeting", summary, meetingMeta{Start: start, Timezone: timezone})
		return 200, okBody{true}, nil
	})
}

// ── Gmail sync ─────────────────────────────────────────────────────
//
// The org's connected Gmail, read into the CRM: for each email with a contact,
// who wrote to whom and when (email_sync.go). The body stays in Gmail. What the
// team sees of each email is the mailbox's visibility setting, applied here for
// every caller: people, agents and other apps get the same view.
//
// Changing the settings is a person's decision, made in the browser
// (caller "user"). Agents and apps can read synced emails and trigger a sync,
// never change what a mailbox shares.

// mayConfigure is whether this caller may change a mailbox's sync settings.
func mayConfigure(r *http.Request) bool {
	return web.Caller(r) == "user" && web.CurrentUser(r) != nil
}

const configureForbidden = "Only a signed-in person can change email sync settings."

type emailAccountView struct {
	Mailbox         string  `json:"mailbox"`
	Enabled         bool    `json:"enabled"`
	Labels          []any   `json:"labels"`
	History         string  `json:"history"`
	Visibility      string  `json:"visibility"`
	AutoCreate      string  `json:"auto_create"`
	ExcludeGroup    bool    `json:"exclude_group"`
	ExcludePersonal bool    `json:"exclude_personal"`
	Blocklist       []any   `json:"blocklist"`
	Phase           string  `json:"phase"`
	SyncedUntil     *string `json:"synced_until"`
	ContactsCreated float64 `json:"contacts_created"`
	LastRunAt       *string `json:"last_run_at"`
	LastError       *string `json:"last_error"`
	NextRunAt       *string `json:"next_run_at"`
	UpdatedBy       *string `json:"updated_by"`
	UpdatedAt       *string `json:"updated_at"`
}

func accountView(a *EmailAccount) *emailAccountView {
	if a == nil {
		return nil
	}
	return &emailAccountView{
		Mailbox:         a.Mailbox,
		Enabled:         a.Enabled != 0,
		Labels:          jsonArray(&a.Labels),
		History:         a.History,
		Visibility:      a.Visibility,
		AutoCreate:      a.AutoCreate,
		ExcludeGroup:    a.ExcludeGroup != 0,
		ExcludePersonal: a.ExcludePersonal != 0,
		Blocklist:       jsonArray(&a.Blocklist),
		Phase:           a.Phase,
		SyncedUntil:     a.SyncedUntil,
		ContactsCreated: a.ContactsCreated,
		LastRunAt:       a.LastRunAt,
		LastError:       a.LastError,
		NextRunAt:       a.NextRunAt,
		UpdatedBy:       a.UpdatedBy,
		UpdatedAt:       a.UpdatedAt,
	}
}

type emailSyncView struct {
	Connected bool    `json:"connected"`
	Mailbox   *string `json:"mailbox"`
	// MailboxChanged: the Google connection now signs in as a different mailbox than the one synced.
	MailboxChanged bool              `json:"mailbox_changed"`
	Account        *emailAccountView `json:"account"`
	Counts         MailboxCounts     `json:"counts"`
	CanConfigure   bool              `json:"can_configure"`
}

// emailSyncState is the mailbox, its settings and where the sync is. Also the
// watchdog: an enabled mailbox with no run booked gets one, so a lost queue job
// heals on next view. `?check=1` also asks Google which account the connection
// signs in as (the settings page does; the contacts page's banner reads stored
// state only).
func (app *App) emailSyncState(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		mail := MailConnection(ctx, app.Conn)
		var connected *string
		if mail != nil && r.URL.Query().Get("check") == "1" {
			if m, err := ConnectedMailbox(ctx, mail); err == nil {
				connected = &m
			}
		}
		// The mailbox being synced when there is one, else the connected one's last settings.
		current, err := CurrentAccount(ctx)
		if err != nil {
			return 0, nil, err
		}
		account := current
		if current == nil || current.Enabled == 0 {
			var forConnected *EmailAccount
			if connected != nil {
				if forConnected, err = AccountFor(ctx, *connected); err != nil {
					return 0, nil, err
				}
			}
			if forConnected != nil {
				account = forConnected
			}
		}
		mailbox := connected
		if mailbox == nil && account != nil {
			mailbox = &account.Mailbox
		}
		if account != nil && account.Enabled != 0 {
			EnsureEmailScheduled(ctx, app.Env, web.Origin(r), account, time.Now())
		}
		counts := MailboxCounts{}
		if account != nil {
			if counts, err = EmailCounts(ctx, account.Mailbox); err != nil {
				return 0, nil, err
			}
		}
		return 200, emailSyncView{
			Connected:      mail != nil,
			Mailbox:        mailbox,
			MailboxChanged: account != nil && account.Enabled != 0 && connected != nil && *connected != account.Mailbox,
			Account:        accountView(account),
			Counts:         counts,
			CanConfigure:   mayConfigure(r),
		}, nil
	})
}

// settingsError is a setting the request got wrong: 400 with its message.
type settingsError struct{ msg string }

func (e *settingsError) Error() string { return e.msg }

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// emailSyncSave saves settings; turning sync on or off is part of the same
// write. Turning off deletes everything synced from the mailbox. Narrowing
// what is imported starts the import over; showing less deletes what is no
// longer shown.
func (app *App) emailSyncSave(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		if !mayConfigure(r) {
			return fail(403, configureForbidden)
		}
		v, err := web.JSONBody(r)
		if err != nil {
			return 0, nil, err
		}
		u := web.CurrentUser(r)
		who := u.ID
		if u.Email != nil {
			who = *u.Email
		}

		mail := MailConnection(ctx, app.Conn)
		if mail == nil {
			return fail(409, "Connect Gmail in Clawnify first.")
		}
		mailbox, err := ConnectedMailbox(ctx, mail)
		if err != nil {
			return 0, nil, err
		}
		// One mailbox syncs at a time. If the Google connection now signs in as
		// another account, what was synced from the old one goes, as when turning off.
		olds, err := db.Query(ctx, "SELECT * FROM email_accounts WHERE mailbox != ? AND enabled = 1", mailbox)
		if err != nil {
			return 0, nil, err
		}
		for _, row := range olds {
			old := accountOf(row)
			CancelEmailScheduled(ctx, app.Env, old)
			if err := Purge(ctx, old.Mailbox); err != nil {
				return 0, nil, err
			}
			if _, err := db.Run(ctx, "UPDATE email_accounts SET enabled = 0 WHERE mailbox = ?", old.Mailbox); err != nil {
				return 0, nil, err
			}
		}
		before, err := AccountFor(ctx, mailbox)
		if err != nil {
			return 0, nil, err
		}
		if v == nil {
			return 0, nil, readNull("labels")
		}
		next, err := settingsFrom(web.Object(v), before)
		var se *settingsError
		if errors.As(err, &se) {
			return fail(400, se.msg)
		}
		if err != nil {
			return 0, nil, err
		}

		if _, err := db.Run(ctx,
			`INSERT INTO email_accounts (mailbox, enabled, labels, history, visibility, auto_create, exclude_group, exclude_personal, blocklist, updated_by, updated_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
       ON CONFLICT (mailbox) DO UPDATE SET enabled = excluded.enabled, labels = excluded.labels, history = excluded.history,
         visibility = excluded.visibility, auto_create = excluded.auto_create, exclude_group = excluded.exclude_group,
         exclude_personal = excluded.exclude_personal, blocklist = excluded.blocklist, updated_by = excluded.updated_by,
         updated_at = excluded.updated_at`,
			mailbox, flag(next.enabled), next.labels, next.history, next.visibility, next.autoCreate,
			flag(next.excludeGroup), flag(next.excludePersonal), next.blocklist, who); err != nil {
			return 0, nil, err
		}
		origin := web.Origin(r)
		now := time.Now()

		if !next.enabled {
			if before != nil && before.Enabled != 0 {
				CancelEmailScheduled(ctx, app.Env, before)
				if err := Purge(ctx, mailbox); err != nil {
					return 0, nil, err
				}
			}
		} else {
			scopeChanged := before != nil && (before.Labels != next.labels || before.History != next.history ||
				before.Blocklist != next.blocklist || before.ExcludeGroup != flag(next.excludeGroup))
			showsMore := before != nil && indexOf(Visibilities, next.visibility) > indexOf(Visibilities, before.Visibility) && before.Visibility == "metadata"
			createsMore := before != nil && (indexOf(AutoCreates, next.autoCreate) > indexOf(AutoCreates, before.AutoCreate) ||
				(before.ExcludePersonal != 0 && !next.excludePersonal))
			switch {
			case before == nil || before.Enabled == 0:
				err = RestartImport(ctx, mailbox, FirstStep(next.autoCreate), now)
			case scopeChanged:
				// What counts changed: forget what was imported under the old scope and read it again.
				if err = Purge(ctx, mailbox); err == nil {
					err = RestartImport(ctx, mailbox, FirstStep(next.autoCreate), now)
				}
			case createsMore:
				err = RestartImport(ctx, mailbox, FirstStep(next.autoCreate), now)
			case showsMore:
				// Subjects were never stored at "metadata": read the contacts' emails again to fill them in.
				err = RestartImport(ctx, mailbox, "people", now)
			}
			if err != nil {
				return 0, nil, err
			}
			if next.visibility == "metadata" && before != nil && before.Visibility != "metadata" {
				if err := ForgetSubjects(ctx, mailbox); err != nil {
					return 0, nil, err
				}
			}
			ScheduleEmailRun(ctx, app.Env, origin, mailbox, now)
		}

		saved, err := AccountFor(ctx, mailbox)
		if err != nil {
			return 0, nil, err
		}
		if saved == nil {
			return 0, nil, readNull("mailbox")
		}
		counts, err := EmailCounts(ctx, mailbox)
		if err != nil {
			return 0, nil, err
		}
		return 200, struct {
			Account *emailAccountView `json:"account"`
			Counts  MailboxCounts     `json:"counts"`
		}{accountView(saved), counts}, nil
	})
}

func flag(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// emailSettings is a mailbox's settings as the next write stores them.
type emailSettings struct {
	enabled         bool
	labels          string // JSON
	history         string
	visibility      string
	autoCreate      string
	excludeGroup    bool
	excludePersonal bool
	blocklist       string // JSON
}

// settingsFrom reads a settings write: each field the body sends replaces the
// mailbox's current one, checked in TS's order (labels first). A bad field is
// a *settingsError.
func settingsFrom(body map[string]any, before *EmailAccount) (emailSettings, error) {
	pick := func(key string, allowed []string, fallback string) (string, error) {
		v, ok := body[key]
		if !ok {
			return fallback, nil
		}
		s, isText := v.(string)
		if !isText || indexOf(allowed, s) < 0 {
			return "", &settingsError{fmt.Sprintf("%s must be one of %s", key, strings.Join(allowed, ", "))}
		}
		return s, nil
	}
	boolean := func(key string, fallback bool) (bool, error) {
		v, ok := body[key]
		if !ok {
			return fallback, nil
		}
		b, isBool := v.(bool)
		if !isBool {
			return false, &settingsError{key + " must be true or false"}
		}
		return b, nil
	}
	beforeText := func(f func(a *EmailAccount) string, fallback string) string {
		if before == nil {
			return fallback
		}
		return f(before)
	}

	var labels any
	if v, ok := body["labels"]; ok {
		labels = v
	} else {
		labels = jsonArray(strp(beforeText(func(a *EmailAccount) string { return a.Labels }, "[]")))
	}
	list, isList := labels.([]any)
	if !isList {
		return emailSettings{}, &settingsError{"labels must be a list of Gmail label names"}
	}
	names := newIDSet()
	for _, l := range list {
		s, isText := l.(string)
		if !isText || js.Trim(s) == "" {
			return emailSettings{}, &settingsError{"labels must be a list of Gmail label names"}
		}
	}
	for _, l := range list {
		names.add(js.Trim(l.(string)))
	}
	var blocklist any
	if v, ok := body["blocklist"]; ok {
		blocklist = NormaliseBlocklist(v)
	} else {
		blocklist = jsonArray(strp(beforeText(func(a *EmailAccount) string { return a.Blocklist }, "[]")))
	}

	var next emailSettings
	var err error
	if next.enabled, err = boolean("enabled", before != nil && before.Enabled != 0); err != nil {
		return emailSettings{}, err
	}
	if next.labels, err = stringify(names.order); err != nil {
		return emailSettings{}, err
	}
	if next.history, err = pick("history", Histories, beforeText(func(a *EmailAccount) string { return a.History }, "12m")); err != nil {
		return emailSettings{}, err
	}
	if next.visibility, err = pick("visibility", Visibilities, beforeText(func(a *EmailAccount) string { return a.Visibility }, "metadata")); err != nil {
		return emailSettings{}, err
	}
	if next.autoCreate, err = pick("auto_create", AutoCreates, beforeText(func(a *EmailAccount) string { return a.AutoCreate }, "sent")); err != nil {
		return emailSettings{}, err
	}
	if next.excludeGroup, err = boolean("exclude_group", before == nil || before.ExcludeGroup != 0); err != nil {
		return emailSettings{}, err
	}
	if next.excludePersonal, err = boolean("exclude_personal", before == nil || before.ExcludePersonal != 0); err != nil {
		return emailSettings{}, err
	}
	if next.blocklist, err = stringify(blocklist); err != nil {
		return emailSettings{}, err
	}
	return next, nil
}

// emailSyncLabels is the mailbox's Gmail labels, for choosing "Some labels" to import.
func (app *App) emailSyncLabels(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		if !mayConfigure(r) {
			return fail(403, configureForbidden)
		}
		mail := MailConnection(ctx, app.Conn)
		if mail == nil {
			return fail(409, "Connect Gmail in Clawnify first.")
		}
		labels, err := ListLabels(ctx, mail)
		if err != nil {
			return 0, nil, err
		}
		return 200, struct {
			Labels []GmailLabel `json:"labels"`
		}{labels}, nil
	})
}

// syncReport is a run's result with the mailbox's settings and counts after it.
type syncReport struct {
	EmailRunResult
	Account *emailAccountView `json:"account"`
	Counts  MailboxCounts     `json:"counts"`
}

// syncAndBook is one bounded sync run, then books the next: at once while the
// first import has work left, otherwise in EmailLiveIntervalMs.
func syncAndBook(ctx context.Context, app *App, origin, raw string) (any, error) {
	var mailbox any // undefined
	text := raw
	if text == "" {
		text = "{}"
	}
	if v, err := web.ParseJSON(text); err == nil {
		if m, ok := v.(map[string]any); ok {
			mailbox = m["mailbox"]
		}
	}
	if mailbox == nil {
		a, err := CurrentAccount(ctx)
		if err != nil {
			return nil, err
		}
		if a != nil {
			mailbox = a.Mailbox
		}
	}
	if !js.Truthy(mailbox) {
		return EmailRunResult{Status: "off"}, nil
	}
	name, isText := mailbox.(string)
	if !isText {
		// Only a hand-made request names a mailbox that is not a string. TS
		// bound it as sent: an object or a list fails to bind, and a number or
		// true matches no mailbox (every mailbox is an address), so it is off.
		if _, err := db.Get(ctx, "SELECT * FROM email_accounts WHERE mailbox = ?", mailbox); err != nil {
			return nil, err
		}
		return syncReport{EmailRunResult: EmailRunResult{Status: "off"}}, nil
	}

	result, err := RunSync(ctx, app.Conn, name, time.Now())
	if err != nil {
		return nil, err
	}
	if result.Status == "ran" || result.Status == "error" {
		var delay time.Duration
		switch {
		case result.Status == "error":
			delay = 10 * time.Minute
		case result.More != nil && *result.More:
			delay = 0
		default:
			delay = EmailLiveIntervalMs * time.Millisecond
		}
		ScheduleEmailRun(ctx, app.Env, origin, name, time.Now().Add(delay))
	}
	account, err := AccountFor(ctx, name)
	if err != nil {
		return nil, err
	}
	counts, err := EmailCounts(ctx, name)
	if err != nil {
		return nil, err
	}
	return syncReport{EmailRunResult: result, Account: accountView(account), Counts: counts}, nil
}

// mayRun is whether a caller may make the app read a mailbox.
func mayRun(who string) bool { return who == "user" || who == "api" || who == "agent" }

// headerValue is c.req.header(name): every value of the header, comma-joined.
func headerValue(r *http.Request, name string) string {
	return strings.Join(r.Header.Values(name), ", ")
}

// emailSyncRun is the platform queue's target: a signed delivery on a
// declared public route. A browser's request to a public route reaches the app
// with no identity (the platform drops it, so no other site can post here as
// you), which is why "Sync now" has its own route. Callers through the
// platform's API proxy keep theirs.
func (app *App) emailSyncRun(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	raw := web.RawBody(r)
	signed := clawnify.VerifyDelivery(ctx, raw,
		headerValue(r, "X-Queue-Signature"), headerValue(r, "X-Queue-Timestamp"), headerValue(r, "X-Queue-Key-Id"))
	if !signed && !mayRun(web.Caller(r)) {
		return web.JSON(w, http.StatusForbidden, errorBody{"Sign in to run a sync."})
	}
	return tryJSON(w, func() (int, any, error) {
		out, err := syncAndBook(ctx, app, web.Origin(r), raw)
		return 200, out, err
	})
}

// emailSyncNow is "Sync now", for a person or an agent. Anonymous callers
// cannot make the app read a mailbox.
func (app *App) emailSyncNow(w http.ResponseWriter, r *http.Request) error {
	if !mayRun(web.Caller(r)) {
		return web.JSON(w, http.StatusForbidden, errorBody{"Sign in to run a sync."})
	}
	return tryJSON(w, func() (int, any, error) {
		out, err := syncAndBook(r.Context(), app, web.Origin(r), web.RawBody(r))
		return 200, out, err
	})
}

// contactEmails is a contact's synced emails, as the mailbox's visibility
// allows. A contact added after the first import has its history read here, once.
func (app *App) contactEmails(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		id := r.PathValue("id")
		_ = ImportContactIfNeeded(ctx, app.Conn, id, time.Now()) // Gmail unreachable: show what is already synced
		list, err := ContactEmails(ctx, id, 50)
		if err != nil {
			return 0, nil, err
		}
		current, err := CurrentAccount(ctx)
		if err != nil {
			return 0, nil, err
		}
		return 200, struct {
			ContactEmailList
			SyncOn bool `json:"sync_on"`
		}{list, current != nil && current.Enabled != 0}, nil
	})
}

// openEmail is one email's text, read live from Gmail when the mailbox shares
// everything. Never stored.
func (app *App) openEmail(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		mailbox, err := decodeURIComponent(r.PathValue("mailbox"))
		if err != nil {
			return 0, nil, err
		}
		opened, refusal, err := OpenEmail(ctx, app.Conn, strings.ToLower(mailbox), r.PathValue("id"))
		if err != nil {
			return 0, nil, err
		}
		if refusal != nil {
			return fail(refusal.Status, refusal.Error)
		}
		return 200, opened, nil
	})
}
