package crm

// Gmail sync: reads the org's connected mailbox and keeps, for every email with
// a contact, who wrote to whom and when. The body never leaves Gmail; the
// subject is stored only while the mailbox's visibility shows it. The rules
// (who counts, which searches) live in email_sync_rules.go; this file is the
// I/O around them.
//
// First import, in steps, resumable from `import_cursor`:
//  1. sent      people you emailed become contacts (auto_create sent or wider)
//  2. received  people who emailed you become contacts (sent_and_received)
//  3. people    every contact's emails, 20 contacts per Gmail search
//
// Then `live`: every run reads the mail that arrived since `synced_until`.
// Runs are short (runBudget) and chained through the platform queue, so an
// import of a large mailbox continues after the page that started it closes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opencrm/internal/clawnify"
	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// Visibilities is how much of each email the team sees, least first:
// "metadata" | "subject" | "everything".
var Visibilities = []string{"metadata", "subject", "everything"}

// AutoCreates are the auto_create policies, narrowest first.
var AutoCreates = []string{"none", "sent", "sent_and_received"}

// Histories are how far back a first import reaches.
var Histories = []string{"3m", "12m", "all"}

// EmailAccount is an email_accounts row.
type EmailAccount struct {
	Mailbox         string
	Enabled         float64
	Labels          string
	History         string
	Visibility      string
	AutoCreate      string
	ExcludeGroup    float64
	ExcludePersonal float64
	Blocklist       string
	Phase           string // "idle" | "importing" | "live"
	ImportCursor    *string
	SyncedUntil     *string
	ContactsCreated float64
	LastRunAt       *string
	LastError       *string
	RunningUntil    *string
	JobID           *string
	NextRunAt       *string
	UpdatedBy       *string
	CreatedAt       *string
	UpdatedAt       *string
}

func accountOf(row db.Row) *EmailAccount {
	if row == nil {
		return nil
	}
	return &EmailAccount{
		Mailbox:         colText(row["mailbox"]),
		Enabled:         js.Number(row["enabled"]),
		Labels:          colText(row["labels"]),
		History:         colText(row["history"]),
		Visibility:      colText(row["visibility"]),
		AutoCreate:      colText(row["auto_create"]),
		ExcludeGroup:    js.Number(row["exclude_group"]),
		ExcludePersonal: js.Number(row["exclude_personal"]),
		Blocklist:       colText(row["blocklist"]),
		Phase:           colText(row["phase"]),
		ImportCursor:    nullableText(row["import_cursor"]),
		SyncedUntil:     nullableText(row["synced_until"]),
		ContactsCreated: js.Number(row["contacts_created"]),
		LastRunAt:       nullableText(row["last_run_at"]),
		LastError:       nullableText(row["last_error"]),
		RunningUntil:    nullableText(row["running_until"]),
		JobID:           nullableText(row["job_id"]),
		NextRunAt:       nullableText(row["next_run_at"]),
		UpdatedBy:       nullableText(row["updated_by"]),
		CreatedAt:       nullableText(row["created_at"]),
		UpdatedAt:       nullableText(row["updated_at"]),
	}
}

// syncCursor is where a run resumes. Absent fields are left out of the stored
// JSON, as undefined ones were.
type syncCursor struct {
	Step         string          `json:"step"` // "sent" | "received" | "people" | "live"
	Page         *string         `json:"page,omitempty"`
	AfterContact *string         `json:"after_contact,omitempty"`
	Batch        []cursorContact `json:"batch,omitempty"`
	// StartedAt is when this import began. Live sync starts here, so mail that
	// arrives while a long import runs is read afterwards rather than skipped.
	StartedAt *string `json:"started_at,omitempty"`
	// MaxSeen is the newest message a live pass has seen; becomes synced_until when the pass ends.
	MaxSeen *string `json:"max_seen,omitempty"`
	// Check is how far checking stored sent mail against Gmail got: the last
	// row checked (`sent_at|id`), or "done".
	Check *string `json:"check,omitempty"`
}

type cursorContact struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// FirstStep is where a first import begins under an auto_create policy.
func FirstStep(autoCreate string) string {
	if autoCreate == "none" {
		return "people"
	}
	return "sent"
}

const (
	// runBudget is the wall-clock budget of one run. Gmail reads are I/O, so
	// this bounds waiting, not CPU.
	runBudget = 20 * time.Second
	// leaseMs: a run's lease outlives its budget, so a crashed run frees the mailbox soon after.
	leaseMs     = 90_000
	pageSize    = 100
	peopleBatch = 20
	// rewindMs is how far back live reads start again when the mark is found in the future.
	rewindMs = 30 * 86_400_000
	// checksPerRun is the most stored sent rows one run checks against Gmail.
	checksPerRun = 20
	// futureSkewMs is the clock skew allowed before a message's date counts as the future.
	futureSkewMs = 5 * 60_000
)

// EmailLiveIntervalMs is, once live, how often new mail is read.
const EmailLiveIntervalMs = 15 * 60_000

// shortcut: the org's one mail connection (Gmail, else Google Workspace). Once
// an org can connect several accounts (platform: multi-account connections),
// pass the mailbox so each email_accounts row reads its own connection.

// mailFor is the org's mail connection, refused when it now signs in as
// another mailbox: one mailbox's mail is never written under another's settings.
func mailFor(ctx context.Context, conn *clawnify.Connections, mailbox string) (*GoogleConnection, error) {
	mail := MailConnection(ctx, conn)
	if mail == nil {
		return nil, errors.New("Gmail is not connected. Connect it in Clawnify to keep syncing.")
	}
	connected, err := ConnectedMailbox(ctx, mail)
	if err != nil {
		return nil, err
	}
	if connected != mailbox {
		return nil, fmt.Errorf("The connected Google account is now %s, not %s. Open Email settings to switch.", connected, mailbox)
	}
	return mail, nil
}

// ConnectedMailbox is the address of the mailbox a Google connection signs in as.
func ConnectedMailbox(ctx context.Context, mail *GoogleConnection) (string, error) {
	data, err := mail.Run(ctx, "GET_PROFILE", map[string]any{"user_id": "me"})
	if err != nil {
		return "", err
	}
	s, _ := asObject(data)["emailAddress"].(string)
	address := strings.ToLower(js.Trim(s))
	if address == "" {
		return "", errors.New("The connected Google account did not report its address")
	}
	return address, nil
}

// GmailLabel is one of the mailbox's own labels.
type GmailLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListLabels is the mailbox's labels, for choosing which ones to import.
// System labels (INBOX, SENT, CATEGORY_*…) are left out: "Everything"
// already covers them.
func ListLabels(ctx context.Context, mail *GoogleConnection) ([]GmailLabel, error) {
	data, err := mail.Run(ctx, "LIST_LABELS", map[string]any{"user_id": "me"})
	if err != nil {
		return nil, err
	}
	labels, _ := asObject(data)["labels"].([]any)
	out := []GmailLabel{}
	for _, l := range labels {
		m := asObject(l)
		id, _ := m["id"].(string)
		name, _ := m["name"].(string)
		if m["type"] != "system" && id != "" && name != "" {
			out = append(out, GmailLabel{ID: id, Name: name})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return localeCompare(out[i].Name, out[j].Name) < 0 })
	return out, nil
}

// gmailMessage is one search result, headers only.
type gmailMessage struct {
	MessageID        string
	ThreadID         string
	MessageTimestamp string
	Sender           string
	To               string
	Subject          string
	LabelIDs         []string
}

// fetchPage is one page of a Gmail search, headers only. The preview snippet
// the API also returns is dropped here, so no body text reaches this app.
func fetchPage(ctx context.Context, mail *GoogleConnection, q string, page *string, max int) ([]gmailMessage, *string, error) {
	args := map[string]any{
		"query":           q,
		"max_results":     max,
		"verbose":         false,
		"include_payload": false,
	}
	if page != nil && *page != "" {
		args["page_token"] = *page
	}
	data, err := mail.Run(ctx, "FETCH_EMAILS", args)
	if err != nil {
		return nil, nil, err
	}
	d := asObject(data)
	raw, _ := d["messages"].([]any)
	messages := make([]gmailMessage, 0, len(raw))
	for _, r := range raw {
		m := asObject(r)
		text := func(k string) string { s, _ := m[k].(string); return s }
		labels := []string{}
		if ls, ok := m["labelIds"].([]any); ok {
			for _, l := range ls {
				if s, ok := l.(string); ok {
					labels = append(labels, s)
				}
			}
		}
		messages = append(messages, gmailMessage{
			MessageID:        text("messageId"),
			ThreadID:         text("threadId"),
			MessageTimestamp: text("messageTimestamp"),
			Sender:           text("sender"),
			To:               text("to"),
			Subject:          text("subject"),
			LabelIDs:         labels,
		})
	}
	var next *string
	if s, _ := d["nextPageToken"].(string); s != "" {
		next = &s
	}
	return messages, next, nil
}

type parsedMessage struct {
	ID        string
	ThreadID  string
	SentAt    string
	Direction string // "sent" | "received"
	From      *Address
	To        []Address
	Subject   string
	LabelIDs  []string
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func parseMessage(m gmailMessage, mailbox string, now time.Time) *parsedMessage {
	if m.MessageID == "" || m.ThreadID == "" || m.MessageTimestamp == "" {
		return nil
	}
	// Only what Gmail has sent is sent. A draft (Gmail saves one under a new id as
	// you type) and a scheduled send (no label until it goes out, dated when it
	// will) haven't happened, so neither is kept, links a contact or creates one.
	if hasLabel(m.LabelIDs, "DRAFT") {
		return nil
	}
	if t, ok := js.ParseDate(m.MessageTimestamp); ok && t.UnixMilli() > now.UnixMilli()+futureSkewMs {
		return nil
	}
	var from *Address
	if all := ParseAddresses(m.Sender); len(all) > 0 {
		from = &all[0]
	}
	sent := hasLabel(m.LabelIDs, "SENT")
	if !sent && from != nil && from.Email == mailbox {
		return nil
	}
	direction := "received"
	if sent {
		direction = "sent"
	}
	return &parsedMessage{
		ID:        m.MessageID,
		ThreadID:  m.ThreadID,
		SentAt:    m.MessageTimestamp,
		Direction: direction,
		From:      from,
		To:        ParseAddresses(m.To),
		Subject:   m.Subject,
		LabelIDs:  m.LabelIDs,
	}
}

func parseAll(messages []gmailMessage, mailbox string, now time.Time) []*parsedMessage {
	out := []*parsedMessage{}
	for _, m := range messages {
		if p := parseMessage(m, mailbox, now); p != nil {
			out = append(out, p)
		}
	}
	return out
}

// ── Settings rows ──────────────────────────────────────────────────

// CurrentAccount is the mailbox being synced. v1 syncs one mailbox at a
// time, the org's default connection; the table allows more. The enabled one
// wins, then the most recently changed. nil when there is none.
func CurrentAccount(ctx context.Context) (*EmailAccount, error) {
	row, err := db.Get(ctx, "SELECT * FROM email_accounts ORDER BY enabled DESC, updated_at DESC LIMIT 1")
	if err != nil {
		return nil, err
	}
	return accountOf(row), nil
}

// AccountFor is the mailbox's settings row, nil when it has none.
func AccountFor(ctx context.Context, mailbox string) (*EmailAccount, error) {
	row, err := db.Get(ctx, "SELECT * FROM email_accounts WHERE mailbox = ?", mailbox)
	if err != nil {
		return nil, err
	}
	return accountOf(row), nil
}

func scopeOf(a *EmailAccount, now time.Time) Scope {
	labels := []string{}
	for _, l := range jsonArray(&a.Labels) {
		if s, ok := l.(string); ok {
			labels = append(labels, s)
		}
	}
	return Scope{Labels: labels, Since: HistoryStart(History(a.History), now)}
}

// BlocklistOf is the addresses and @domains this mailbox never imports.
func BlocklistOf(a *EmailAccount) []string {
	return NormaliseBlocklist(jsonArray(&a.Blocklist))
}

func rulesOf(a *EmailAccount) CreateRules {
	return CreateRules{
		Mailbox:          a.Mailbox,
		Policy:           AutoCreate(a.AutoCreate),
		ExcludeGroup:     a.ExcludeGroup != 0,
		ExcludePersonal:  a.ExcludePersonal != 0,
		Blocklist:        BlocklistOf(a),
		IsPersonalDomain: func(d string) bool { return FreemailDomains[d] },
	}
}

// jsonArray is JSON.parse(v ?? "[]") when that is an array, else [].
func jsonArray(v *string) []any {
	s := "[]"
	if v != nil {
		s = *v
	}
	parsed, err := web.ParseJSON(s)
	if arr, ok := parsed.([]any); ok && err == nil {
		return arr
	}
	return []any{}
}

// ── Contacts ───────────────────────────────────────────────────────

// contactIndex is every contact's address → id, read once per run and kept
// current as the run adds contacts.
func contactIndex(ctx context.Context) (map[string]string, error) {
	rows, err := db.Query(ctx, "SELECT id, email FROM contacts WHERE email IS NOT NULL AND email != ''")
	if err != nil {
		return nil, err
	}
	index := map[string]string{}
	for _, r := range rows {
		index[strings.ToLower(js.Trim(colText(r["email"])))] = colText(r["id"])
	}
	return index, nil
}

// newContactID is a variable so a test can hand out ids that sort in creation
// order: the people step walks contacts by id.
var newContactID = js.UUID

// createContact adds a contact for someone the mailbox wrote to (or heard
// from), linked to their company by work domain the way import and create
// already do.
func createContact(ctx context.Context, person Address) (string, error) {
	name := SplitName(person)
	var companyID any
	if domain := WorkEmailDomain(person.Email); domain != "" {
		id, err := FindOrCreateCompanyByDomain(ctx, domain)
		if err != nil {
			return "", err
		}
		companyID = id
	}
	id := newContactID()
	if _, err := db.Run(ctx, "INSERT INTO contacts (id, first_name, last_name, email, company_id) VALUES (?, ?, ?, ?, ?)",
		id, name.First, name.Last, person.Email, companyID); err != nil {
		return "", err
	}
	return id, nil
}

// idSet is a JS Set of ids: each once, in the order first added.
type idSet struct {
	order []string
	seen  map[string]bool
}

func newIDSet() *idSet { return &idSet{order: []string{}, seen: map[string]bool{}} }

func (s *idSet) add(ids ...string) {
	for _, id := range ids {
		if !s.seen[id] {
			s.seen[id] = true
			s.order = append(s.order, id)
		}
	}
}

func recomputeLastContacted(ctx context.Context, contactIDs []string) error {
	set := newIDSet()
	set.add(contactIDs...)
	for _, part := range Chunk(set.order, 90) {
		_, err := db.Run(ctx, `UPDATE contacts SET last_contacted_at =
         (SELECT MAX(l.sent_at) FROM email_message_contacts l WHERE l.contact_id = contacts.id)
       WHERE id IN (`+marks(len(part))+`)`, stringParams(part)...)
		if err != nil {
			return err
		}
	}
	return nil
}

// ── Storing ────────────────────────────────────────────────────────

// storeAndLink keeps the messages that involve a contact, links them, and
// returns the contacts touched.
func storeAndLink(ctx context.Context, a *EmailAccount, messages []*parsedMessage, index map[string]string) (*idSet, error) {
	rules := rulesOf(a)
	touched := newIDSet()
	for _, m := range messages {
		if m.From != nil && IsBlocked(m.From.Email, rules.Blocklist) {
			continue
		}
		if m.Direction == "received" && rules.ExcludeGroup && m.From != nil && IsGroupAddress(m.From.Email) {
			continue
		}
		people := []Address{}
		if m.From != nil {
			people = append(people, *m.From)
		}
		people = append(people, m.To...)
		contactIDs := newIDSet()
		for _, p := range people {
			if p.Email == a.Mailbox || IsBlocked(p.Email, rules.Blocklist) {
				continue
			}
			if id := index[p.Email]; id != "" {
				contactIDs.add(id)
			}
		}
		if len(contactIDs.order) == 0 {
			continue
		}
		var subject any = m.Subject
		if a.Visibility == "metadata" {
			subject = nil
		}
		var fromEmail string
		var fromName any
		if m.From != nil {
			fromEmail = m.From.Email
			if m.From.Name != nil {
				fromName = *m.From.Name
			}
		}
		toEmails := make([]string, len(m.To))
		for i, t := range m.To {
			toEmails[i] = t.Email
		}
		toJSON, err := stringify(toEmails)
		if err != nil {
			return nil, err
		}
		if _, err := db.Run(ctx,
			`INSERT INTO email_messages (mailbox, id, thread_id, sent_at, direction, from_email, from_name, to_emails, subject)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
       ON CONFLICT (mailbox, id) DO UPDATE SET subject = excluded.subject`,
			a.Mailbox, m.ID, m.ThreadID, m.SentAt, m.Direction, fromEmail, fromName, toJSON, subject); err != nil {
			return nil, err
		}
		for _, cid := range contactIDs.order {
			if _, err := db.Run(ctx, "INSERT OR IGNORE INTO email_message_contacts (mailbox, message_id, contact_id, sent_at) VALUES (?, ?, ?, ?)",
				a.Mailbox, m.ID, cid, m.SentAt); err != nil {
				return nil, err
			}
			touched.add(cid)
		}
	}
	return touched, nil
}

// createFrom adds contacts for the people these messages bring in under the
// account's policy.
func createFrom(ctx context.Context, a *EmailAccount, messages []*parsedMessage, index map[string]string) (int, error) {
	rules := rulesOf(a)
	created := 0
	for _, m := range messages {
		msg := CandidateMessage{Direction: m.Direction, From: m.From, To: m.To, LabelIDs: m.LabelIDs}
		for _, person := range CreationCandidates(msg, rules) {
			if _, ok := index[person.Email]; ok {
				continue
			}
			id, err := createContact(ctx, person)
			if err != nil {
				return 0, err
			}
			index[person.Email] = id
			created++
		}
	}
	return created, nil
}

// laterOf is the later of two ISO timestamps; a missing or empty a loses.
func laterOf(a *string, b string) string {
	if a == nil || *a == "" || b > *a {
		return b
	}
	return *a
}

// forget removes stored messages and their links; returns the contacts whose
// last contact may change.
func forget(ctx context.Context, mailbox string, ids []string) ([]string, error) {
	affected := []string{}
	for _, part := range Chunk(ids, 90) {
		m := marks(len(part))
		params := append([]any{mailbox}, stringParams(part)...)
		links, err := db.Query(ctx, "SELECT DISTINCT contact_id FROM email_message_contacts WHERE mailbox = ? AND message_id IN ("+m+")", params...)
		if err != nil {
			return nil, err
		}
		for _, l := range links {
			affected = append(affected, colText(l["contact_id"]))
		}
		if _, err := db.Run(ctx, "DELETE FROM email_message_contacts WHERE mailbox = ? AND message_id IN ("+m+")", params...); err != nil {
			return nil, err
		}
		if _, err := db.Run(ctx, "DELETE FROM email_messages WHERE mailbox = ? AND id IN ("+m+")", params...); err != nil {
			return nil, err
		}
	}
	return affected, nil
}

// ── Checking stored sent mail ──────────────────────────────────────

// goneMessage is Gmail's answer for a message id it no longer has.
var goneMessage = regexp.MustCompile(`"code":` + wsClass + `*404|NOT_FOUND|Requested entity was not found`)

func gone(err error) bool { return goneMessage.MatchString(err.Error()) }

type sentCheck struct {
	check    string
	dropped  int
	affected []string
	failed   bool
}

// checkSent: sent rows stored before "sent" meant "labelled SENT" can be
// drafts or scheduled mail. Walks them newest first, a few per run, asking
// Gmail about each: a row Gmail no longer has, or has not sent, is forgotten.
// Any other failure stops the walk until the next run and forgets nothing.
func checkSent(ctx context.Context, mail *GoogleConnection, mailbox string, from *string, deadline time.Time) (sentCheck, error) {
	mark := "9999-12-31T23:59:59Z|"
	if from != nil {
		mark = *from
	}
	parts := strings.Split(mark, "|")
	at := parts[0]
	var id any // undefined when the mark has no "|"
	idText := "undefined"
	if len(parts) > 1 {
		id, idText = parts[1], parts[1]
	}
	rows, err := db.Query(ctx, `SELECT id, sent_at FROM email_messages
      WHERE mailbox = ? AND direction = 'sent' AND (sent_at < ? OR (sent_at = ? AND id < ?))
      ORDER BY sent_at DESC, id DESC LIMIT ?`, mailbox, at, at, id, checksPerRun)
	if err != nil {
		return sentCheck{}, err
	}
	drop := []string{}
	check := at + "|" + idText
	checked := 0
	failed := false
	for _, r := range rows {
		if time.Now().After(deadline) {
			break
		}
		rid, sentAt := colText(r["id"]), colText(r["sent_at"])
		data, err := mail.Run(ctx, "FETCH_MESSAGE_BY_MESSAGE_ID", map[string]any{"message_id": rid, "format": "minimal", "user_id": "me"})
		if err != nil {
			if !gone(err) {
				failed = true
				break
			}
			drop = append(drop, rid)
		} else if labels, ok := asObject(data)["labelIds"].([]any); ok && !containsValue(labels, "SENT") {
			drop = append(drop, rid)
		}
		check = sentAt + "|" + rid
		checked++
	}
	if !failed && checked == len(rows) && len(rows) < checksPerRun {
		check = "done"
	}
	affected, err := forget(ctx, mailbox, drop)
	if err != nil {
		return sentCheck{}, err
	}
	return sentCheck{check: check, dropped: len(drop), affected: affected, failed: failed}, nil
}

func containsValue(list []any, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// ── Running ────────────────────────────────────────────────────────

// claim takes the mailbox for one run; false when another run holds it.
func claim(ctx context.Context, mailbox string, now time.Time) (bool, error) {
	rows, err := db.Query(ctx, `UPDATE email_accounts SET running_until = ?
      WHERE mailbox = ? AND enabled = 1 AND (running_until IS NULL OR running_until < ?)
      RETURNING mailbox`, js.ISO(now.Add(leaseMs*time.Millisecond)), mailbox, js.ISO(now))
	if err != nil {
		return false, err
	}
	return len(rows) == 1, nil
}

// EmailRunResult is what one sync run did. Fields TS left undefined are nil
// and left out.
type EmailRunResult struct {
	Status string  `json:"status"` // "ran" | "busy" | "off" | "error"
	Phase  *string `json:"phase,omitempty"`
	Step   *string `json:"step,omitempty"`
	// Created and Stored count contacts added and messages read.
	Created *int `json:"created,omitempty"`
	Stored  *int `json:"stored,omitempty"`
	// Forgotten counts stored rows removed because Gmail no longer has them or never sent them.
	Forgotten *int    `json:"forgotten,omitempty"`
	More      *bool   `json:"more,omitempty"`
	Error     *string `json:"error,omitempty"`
}

// RunSync is one bounded run: continue the first import, or read new mail
// once live. More says whether work is left that should run again right away.
func RunSync(ctx context.Context, conn *clawnify.Connections, mailbox string, now time.Time) (EmailRunResult, error) {
	a, err := AccountFor(ctx, mailbox)
	if err != nil {
		return EmailRunResult{}, err
	}
	if a == nil || a.Enabled == 0 {
		return EmailRunResult{Status: "off"}, nil
	}
	claimed, err := claim(ctx, mailbox, now)
	if err != nil {
		return EmailRunResult{}, err
	}
	if !claimed {
		return EmailRunResult{Status: "busy"}, nil
	}

	started := time.Now()
	created, stored, forgotten := 0, 0, 0
	// A first import keeps only mail Gmail labelled SENT, so it has nothing to check.
	cursor := parseCursor(a.ImportCursor)
	if cursor == nil {
		cursor = &syncCursor{Step: FirstStep(a.AutoCreate), StartedAt: strp(js.ISO(now)), Check: strp("done")}
	}
	check := cursor.Check // outlives the cursor's rebuilds below
	phase := a.Phase
	if phase == "idle" {
		phase = "importing"
	}

	sync := func() (EmailRunResult, error) {
		mail, err := mailFor(ctx, conn, a.Mailbox)
		if err != nil {
			return EmailRunResult{}, err
		}
		scope := scopeOf(a, now)
		index, err := contactIndex(ctx)
		if err != nil {
			return EmailRunResult{}, err
		}
		touched := newIDSet()

		if phase == "live" {
			// The mark once followed a scheduled send into the future, and live reads
			// skipped the mail before it: read the last 30 days again (stores are idempotent).
			if a.SyncedUntil != nil && *a.SyncedUntil != "" && *a.SyncedUntil > js.ISO(now) {
				a.SyncedUntil = strp(js.ISO(now.Add(-rewindMs * time.Millisecond)))
			}
			future, err := db.Query(ctx, "SELECT id FROM email_messages WHERE mailbox = ? AND sent_at > ?", a.Mailbox, js.ISO(now.Add(futureSkewMs*time.Millisecond)))
			if err != nil {
				return EmailRunResult{}, err
			}
			ids := make([]string, len(future))
			for i, f := range future {
				ids[i] = colText(f["id"])
			}
			affected, err := forget(ctx, a.Mailbox, ids)
			if err != nil {
				return EmailRunResult{}, err
			}
			touched.add(affected...)
			forgotten += len(future)
		}

		for time.Since(started) < runBudget {
			if cursor.Step == "sent" || cursor.Step == "received" {
				q := ReceivedQuery(scope)
				if cursor.Step == "sent" {
					q = SentQuery(scope)
				}
				messages, next, err := fetchPage(ctx, mail, q, cursor.Page, pageSize)
				if err != nil {
					return EmailRunResult{}, err
				}
				n, err := createFrom(ctx, a, parseAll(messages, a.Mailbox, now), index)
				if err != nil {
					return EmailRunResult{}, err // created += await … never assigned
				}
				created += n
				if next != nil {
					c := *cursor
					c.Page = next
					cursor = &c
				} else {
					step := "people"
					if cursor.Step == "sent" && a.AutoCreate == "sent_and_received" {
						step = "received"
					}
					cursor = &syncCursor{Step: step, StartedAt: cursor.StartedAt}
				}
				continue
			}

			if cursor.Step == "people" {
				if len(cursor.Batch) == 0 {
					after := ""
					if cursor.AfterContact != nil {
						after = *cursor.AfterContact
					}
					rows, err := db.Query(ctx, `SELECT id, lower(trim(email)) AS email FROM contacts
              WHERE email IS NOT NULL AND email != '' AND id > ?
              ORDER BY id LIMIT ?`, after, peopleBatch)
					if err != nil {
						return EmailRunResult{}, err
					}
					if len(rows) == 0 {
						phase = "live"
						if cursor.StartedAt != nil {
							a.SyncedUntil = cursor.StartedAt
						} else {
							a.SyncedUntil = strp(js.ISO(now))
						}
						cursor = &syncCursor{Step: "live"}
						break // the first live read runs on the next delivery
					}
					batch := make([]cursorContact, len(rows))
					for i, r := range rows {
						batch[i] = cursorContact{ID: colText(r["id"]), Email: colText(r["email"])}
					}
					c := *cursor
					c.Batch, c.Page = batch, nil
					cursor = &c
				}
				blocklist := rulesOf(a).Blocklist
				emails := []string{}
				for _, b := range cursor.Batch {
					if !IsBlocked(b.Email, blocklist) {
						emails = append(emails, b.Email)
					}
				}
				var messages []gmailMessage
				var next *string
				if len(emails) > 0 {
					if messages, next, err = fetchPage(ctx, mail, PeopleQuery(emails, scope), cursor.Page, pageSize); err != nil {
						return EmailRunResult{}, err
					}
				}
				parsed := parseAll(messages, a.Mailbox, now)
				ids, err := storeAndLink(ctx, a, parsed, index)
				if err != nil {
					return EmailRunResult{}, err
				}
				touched.add(ids.order...)
				stored += len(parsed)
				if next != nil {
					c := *cursor
					c.Page = next
					cursor = &c
				} else {
					for _, b := range cursor.Batch {
						if _, err := db.Run(ctx,
							`INSERT INTO email_contact_imports (mailbox, contact_id, email) VALUES (?, ?, ?)
               ON CONFLICT (mailbox, contact_id) DO UPDATE SET email = excluded.email, imported_at = datetime('now')`,
							a.Mailbox, b.ID, b.Email); err != nil {
							return EmailRunResult{}, err
						}
					}
					last := cursor.Batch[len(cursor.Batch)-1].ID
					cursor = &syncCursor{Step: "people", AfterContact: &last, StartedAt: cursor.StartedAt}
				}
				continue
			}

			// live: everything since the last sync, all pages, then move the mark.
			since := js.ISO(now)
			if a.SyncedUntil != nil {
				since = *a.SyncedUntil
			}
			messages, next, err := fetchPage(ctx, mail, sinceQueryOf(since, scope), cursor.Page, pageSize)
			if err != nil {
				return EmailRunResult{}, err
			}
			parsed := parseAll(messages, a.Mailbox, now)
			n, err := createFrom(ctx, a, parsed, index)
			if err != nil {
				return EmailRunResult{}, err
			}
			created += n
			ids, err := storeAndLink(ctx, a, parsed, index)
			if err != nil {
				return EmailRunResult{}, err
			}
			touched.add(ids.order...)
			stored += len(parsed)
			for _, m := range parsed {
				cursor.MaxSeen = strp(laterOf(cursor.MaxSeen, m.SentAt))
			}
			if next != nil {
				c := *cursor
				c.Page = next
				cursor = &c
				continue
			}
			if cursor.MaxSeen != nil && *cursor.MaxSeen != "" {
				a.SyncedUntil = strp(laterOf(a.SyncedUntil, *cursor.MaxSeen))
			}
			// The mark never passes the present, whatever a message's date says.
			if a.SyncedUntil != nil && *a.SyncedUntil != "" && *a.SyncedUntil > js.ISO(now) {
				a.SyncedUntil = strp(js.ISO(now))
			}
			cursor = &syncCursor{Step: "live"}
			break
		}

		checkFailed := false
		if phase == "live" && cursor.Step == "live" && (cursor.Page == nil || *cursor.Page == "") && !isDone(check) {
			r, err := checkSent(ctx, mail, a.Mailbox, check, started.Add(runBudget))
			if err != nil {
				checkFailed = true // checking is a cleanup: it never fails the sync
			} else {
				check = &r.check
				forgotten += r.dropped
				touched.add(r.affected...)
				checkFailed = r.failed
			}
		}

		if err := recomputeLastContacted(ctx, touched.order); err != nil {
			return EmailRunResult{}, err
		}
		more := phase == "importing" || (cursor.Page != nil && *cursor.Page != "") || (phase == "live" && !isDone(check) && !checkFailed)
		saved := *cursor
		saved.Check = check
		cursorJSON, err := stringify(saved)
		if err != nil {
			return EmailRunResult{}, err
		}
		if _, err := db.Run(ctx,
			`UPDATE email_accounts SET phase = ?, import_cursor = ?, synced_until = ?, contacts_created = contacts_created + ?,
              last_run_at = ?, last_error = NULL, running_until = NULL WHERE mailbox = ?`,
			phase, cursorJSON, textOrNil(a.SyncedUntil), created, js.ISO(now), a.Mailbox); err != nil {
			return EmailRunResult{}, err
		}
		return EmailRunResult{Status: "ran", Phase: strp(phase), Step: strp(cursor.Step), Created: &created, Stored: &stored, Forgotten: &forgotten, More: &more}, nil
	}

	res, err := sync()
	if err == nil {
		return res, nil
	}
	message := errMessage(err)
	saved := *cursor
	saved.Check = check
	cursorJSON, jerr := stringify(saved)
	if jerr != nil {
		return EmailRunResult{}, jerr
	}
	if _, err := db.Run(ctx,
		"UPDATE email_accounts SET import_cursor = ?, contacts_created = contacts_created + ?, last_run_at = ?, last_error = ?, running_until = NULL WHERE mailbox = ?",
		cursorJSON, created, js.ISO(now), message, a.Mailbox); err != nil {
		return EmailRunResult{}, err
	}
	return EmailRunResult{Status: "error", Phase: strp(phase), Step: strp(cursor.Step), Created: &created, Stored: &stored, Error: &message}, nil
}

func isDone(check *string) bool { return check != nil && *check == "done" }

// textOrNil binds a nullable text: nil is NULL.
func textOrNil(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// sinceQueryOf is sinceQuery(new Date(syncedUntil), scope). A mark that is no
// date reads back "after:NaN", as TS's Invalid Date did.
func sinceQueryOf(syncedUntil string, scope Scope) string {
	t, ok := js.ParseDate(syncedUntil)
	if !ok {
		scope.Since = nil
		return strings.Join(append(ScopeTerms(scope), "after:NaN"), " ")
	}
	return SinceQuery(t, scope)
}

// parseCursor reads a stored cursor: an object with a string step, else nil.
func parseCursor(v *string) *syncCursor {
	s := "null"
	if v != nil {
		s = *v
	}
	parsed, err := web.ParseJSON(s)
	m, ok := parsed.(map[string]any)
	if err != nil || !ok {
		return nil
	}
	if _, ok := m["step"].(string); !ok {
		return nil
	}
	var c syncCursor
	// A field of an unexpected type is left empty; the rest still reads.
	var typeErr *json.UnmarshalTypeError
	if err := json.Unmarshal([]byte(s), &c); err != nil && !errors.As(err, &typeErr) {
		return nil
	}
	return &c
}

// errMessage is err.message as TS stored or returned it. A failed request
// reads as its cause, without Go's `Post "url": ` prefix: the Workers runtime
// worded network failures its own way, and the URL is the internal bridge's.
func errMessage(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// ── Scheduling ─────────────────────────────────────────────────────

// ScheduleEmailRun books the next run on the platform queue. The key is the
// app's host, the mailbox and the target minute, so two requests noticing the
// same gap book one job; never for the current minute (see BookableAt).
// Unavailable queue (local dev, an outage) is not an error: the next page load
// or "Sync now" books again.
func ScheduleEmailRun(ctx context.Context, env clawnify.Env, origin, mailbox string, when time.Time) {
	runAt := BookableAt(when, time.Now())
	host, ok := urlHost(origin)
	if !ok {
		return // new URL(origin) threw: no queue booking
	}
	job, err := clawnify.Enqueue(ctx, env, clawnify.EnqueueOptions{
		TargetURL:      origin + "/api/email-sync/run",
		Payload:        map[string]any{"mailbox": mailbox},
		RunAt:          runAt,
		IdempotencyKey: "crm-email-" + host + "-" + mailbox + "-" + js.ISO(runAt)[:16],
		MaxAttempts:    3,
	})
	if err != nil {
		return // no queue: the watchdog in EnsureEmailScheduled books it later
	}
	db.Run(ctx, "UPDATE email_accounts SET job_id = ?, next_run_at = ? WHERE mailbox = ?", job.ID, js.ISO(runAt), mailbox)
}

// urlHost is new URL(origin).host: lower case, without the scheme's default port.
func urlHost(origin string) (string, bool) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	host := strings.ToLower(u.Host)
	if def := map[string]string{"http": ":80", "https": ":443"}[u.Scheme]; strings.HasSuffix(host, def) {
		host = strings.TrimSuffix(host, def)
	}
	return host, true
}

// EnsureEmailScheduled books a run when an enabled mailbox has none coming,
// or its booking is long overdue.
func EnsureEmailScheduled(ctx context.Context, env clawnify.Env, origin string, a *EmailAccount, now time.Time) {
	if a.Enabled == 0 {
		return
	}
	overdue := a.NextRunAt == nil || *a.NextRunAt == ""
	if !overdue {
		t, ok := js.ParseDate(*a.NextRunAt)
		overdue = ok && t.UnixMilli() < now.UnixMilli()-5*60_000
	}
	if overdue {
		ScheduleEmailRun(ctx, env, origin, a.Mailbox, now)
	}
}

// CancelEmailScheduled drops the mailbox's booked run, if any.
func CancelEmailScheduled(ctx context.Context, env clawnify.Env, a *EmailAccount) {
	if a.JobID == nil || *a.JobID == "" {
		return
	}
	_ = clawnify.CancelJob(ctx, env, *a.JobID) // already delivered or gone
}

// ── Changing settings ──────────────────────────────────────────────

// Purge deletes everything synced from a mailbox and resets its progress.
func Purge(ctx context.Context, mailbox string) error {
	affected, err := db.Query(ctx, "SELECT DISTINCT contact_id FROM email_message_contacts WHERE mailbox = ?", mailbox)
	if err != nil {
		return err
	}
	for _, sql := range []string{
		"DELETE FROM email_message_contacts WHERE mailbox = ?",
		"DELETE FROM email_messages WHERE mailbox = ?",
		"DELETE FROM email_contact_imports WHERE mailbox = ?",
	} {
		if _, err := db.Run(ctx, sql, mailbox); err != nil {
			return err
		}
	}
	ids := make([]string, len(affected))
	for i, r := range affected {
		ids[i] = colText(r["contact_id"])
	}
	if err := recomputeLastContacted(ctx, ids); err != nil {
		return err
	}
	_, err = db.Run(ctx,
		"UPDATE email_accounts SET phase = 'idle', import_cursor = NULL, synced_until = NULL, next_run_at = NULL, job_id = NULL WHERE mailbox = ?",
		mailbox)
	return err
}

// RestartImport restarts the first import at step (messages already stored
// are kept and refreshed).
func RestartImport(ctx context.Context, mailbox, step string, now time.Time) error {
	// The rows a restart keeps were checked against Gmail before it, or still need
	// to be. No cursor means nothing is stored yet (new or purged), so nothing to check.
	a, err := AccountFor(ctx, mailbox)
	if err != nil {
		return err
	}
	var prev *syncCursor
	if a != nil {
		prev = parseCursor(a.ImportCursor)
	}
	cursor := syncCursor{Step: step, StartedAt: strp(js.ISO(now)), Check: strp("done")}
	if prev != nil {
		cursor.Check = prev.Check
	}
	s, err := stringify(cursor)
	if err != nil {
		return err
	}
	_, err = db.Run(ctx, "UPDATE email_accounts SET phase = 'importing', import_cursor = ?, next_run_at = NULL WHERE mailbox = ?", s, mailbox)
	return err
}

// ForgetSubjects drops every stored subject of the mailbox.
func ForgetSubjects(ctx context.Context, mailbox string) error {
	_, err := db.Run(ctx, "UPDATE email_messages SET subject = NULL WHERE mailbox = ?", mailbox)
	return err
}

// ── Reading ────────────────────────────────────────────────────────

// ContactEmail is one synced email as a contact's timeline shows it.
type ContactEmail struct {
	ID        string   `json:"id"`
	Mailbox   string   `json:"mailbox"`
	ThreadID  string   `json:"thread_id"`
	SentAt    string   `json:"sent_at"`
	Direction string   `json:"direction"` // "sent" | "received"
	FromEmail string   `json:"from_email"`
	FromName  *string  `json:"from_name"`
	ToEmails  []string `json:"to_emails"`
	Subject   *string  `json:"subject"`
	CanOpen   bool     `json:"can_open"`
	GmailURL  string   `json:"gmail_url"`
}

// ContactEmailList is a page of a contact's emails and how many there are.
type ContactEmailList struct {
	Total  float64        `json:"total"`
	Emails []ContactEmail `json:"emails"`
}

// ContactEmails is a contact's synced emails, newest first, as the mailbox's
// visibility allows: the subject only above "metadata", the body (via
// OpenEmail) only at "everything". Every caller gets this same view, people
// and agents alike.
func ContactEmails(ctx context.Context, contactID string, limit int) (ContactEmailList, error) {
	rows, err := db.Query(ctx,
		`SELECT m.id, m.mailbox, m.thread_id, m.sent_at, m.direction, m.from_email, m.from_name, m.to_emails, m.subject, a.visibility
       FROM email_message_contacts l
       JOIN email_messages m ON m.mailbox = l.mailbox AND m.id = l.message_id
       JOIN email_accounts a ON a.mailbox = m.mailbox AND a.enabled = 1
      WHERE l.contact_id = ?
      ORDER BY m.sent_at DESC
      LIMIT ?`,
		contactID, limit)
	if err != nil {
		return ContactEmailList{}, err
	}
	totalRow, err := db.Get(ctx,
		`SELECT COUNT(*) AS n FROM email_message_contacts l JOIN email_accounts a ON a.mailbox = l.mailbox AND a.enabled = 1 WHERE l.contact_id = ?`,
		contactID)
	if err != nil {
		return ContactEmailList{}, err
	}
	out := ContactEmailList{Emails: []ContactEmail{}}
	if totalRow != nil && totalRow["n"] != nil {
		out.Total = js.Number(totalRow["n"])
	}
	for _, r := range rows {
		visibility := colText(r["visibility"])
		toEmails := []string{}
		for _, e := range jsonArray(nullableText(r["to_emails"])) {
			if s, ok := e.(string); ok {
				toEmails = append(toEmails, s)
			}
		}
		subject := nullableText(r["subject"])
		if visibility == "metadata" {
			subject = nil
		}
		mailbox, threadID := colText(r["mailbox"]), colText(r["thread_id"])
		out.Emails = append(out.Emails, ContactEmail{
			ID:        colText(r["id"]),
			Mailbox:   mailbox,
			ThreadID:  threadID,
			SentAt:    colText(r["sent_at"]),
			Direction: colText(r["direction"]),
			FromEmail: colText(r["from_email"]),
			FromName:  nullableText(r["from_name"]),
			ToEmails:  toEmails,
			Subject:   subject,
			CanOpen:   visibility == "everything",
			// authuser picks the signed-in account by address; Gmail doesn't decode an address in the /u/ path.
			GmailURL: "https://mail.google.com/mail/?authuser=" + encodeURIComponent(mailbox) + "#all/" + threadID,
		})
	}
	return out, nil
}

// ImportContactIfNeeded: a contact added after the first import (or whose
// address changed) has no history yet: read it now with one search. Runs only
// once per address.
func ImportContactIfNeeded(ctx context.Context, conn *clawnify.Connections, contactID string, now time.Time) error {
	a, err := CurrentAccount(ctx)
	if err != nil || a == nil || a.Enabled == 0 || a.Phase != "live" {
		return err
	}
	contact, err := db.Get(ctx, "SELECT lower(trim(email)) AS email FROM contacts WHERE id = ?", contactID)
	if err != nil {
		return err
	}
	email := ""
	if contact != nil && contact["email"] != nil {
		email = colText(contact["email"])
	}
	if email == "" {
		return nil
	}
	done, err := db.Get(ctx, "SELECT email FROM email_contact_imports WHERE mailbox = ? AND contact_id = ?", a.Mailbox, contactID)
	if err != nil {
		return err
	}
	if done != nil && done["email"] == email {
		return nil
	}
	if IsBlocked(email, rulesOf(a).Blocklist) {
		return nil
	}

	mail, err := mailFor(ctx, conn, a.Mailbox)
	if err != nil {
		return err
	}
	messages, _, err := fetchPage(ctx, mail, PeopleQuery([]string{email}, scopeOf(a, now)), nil, pageSize)
	if err != nil {
		return err
	}
	parsed := parseAll(messages, a.Mailbox, now)
	index := map[string]string{email: contactID}
	if _, err := storeAndLink(ctx, a, parsed, index); err != nil {
		return err
	}
	if err := recomputeLastContacted(ctx, []string{contactID}); err != nil {
		return err
	}
	_, err = db.Run(ctx,
		`INSERT INTO email_contact_imports (mailbox, contact_id, email) VALUES (?, ?, ?)
     ON CONFLICT (mailbox, contact_id) DO UPDATE SET email = excluded.email, imported_at = datetime('now')`,
		a.Mailbox, contactID, email)
	return err
}

// OpenedEmail is one opened email: its text, read live from Gmail and never
// stored, and who it went between.
type OpenedEmail struct {
	Text      string    `json:"text"`
	Subject   string    `json:"subject"`
	SentAt    string    `json:"sent_at"`
	Direction string    `json:"direction"`
	ThreadID  string    `json:"thread_id"`
	From      *Address  `json:"from"`
	To        []Address `json:"to"`
	Cc        []Address `json:"cc"`
}

// EmailRefusal is why an email can't be opened, with the status to answer.
type EmailRefusal struct {
	Error  string
	Status int
}

// OpenEmail is one email's text, read live from Gmail and never stored. Only
// at "everything".
func OpenEmail(ctx context.Context, conn *clawnify.Connections, mailbox, id string) (*OpenedEmail, *EmailRefusal, error) {
	a, err := AccountFor(ctx, mailbox)
	if err != nil {
		return nil, nil, err
	}
	if a == nil || a.Enabled == 0 {
		return nil, &EmailRefusal{"Email sync is off for this mailbox", 404}, nil
	}
	if a.Visibility != "everything" {
		return nil, &EmailRefusal{"This mailbox shares metadata only; open the email in Gmail", 403}, nil
	}
	known, err := db.Get(ctx, "SELECT 1 AS ok FROM email_message_contacts WHERE mailbox = ? AND message_id = ? LIMIT 1", mailbox, id)
	if err != nil {
		return nil, nil, err
	}
	if known == nil {
		return nil, &EmailRefusal{"Email not found", 404}, nil
	}
	row, err := db.Get(ctx, "SELECT thread_id, sent_at, direction, from_email, from_name, to_emails, subject FROM email_messages WHERE mailbox = ? AND id = ?", mailbox, id)
	if err != nil {
		return nil, nil, err
	}
	if row == nil {
		return nil, &EmailRefusal{"Email not found", 404}, nil
	}
	mail := MailConnection(ctx, conn)
	if mail == nil {
		return nil, &EmailRefusal{"Gmail is not connected", 409}, nil
	}
	raw, err := mail.Run(ctx, "FETCH_MESSAGE_BY_MESSAGE_ID", map[string]any{"message_id": id, "format": "full", "user_id": "me"})
	if err != nil {
		return nil, nil, err
	}
	data := asObject(raw)
	// Cc is only in the raw headers; the sync stores To but never Cc.
	header := func(name string) string {
		headers, _ := asObject(data["payload"])["headers"].([]any)
		for _, h := range headers {
			hm := asObject(h)
			if n, ok := hm["name"].(string); ok && strings.ToLower(n) == name {
				v, _ := hm["value"].(string)
				return v
			}
		}
		return ""
	}
	storedTo := []Address{}
	for _, e := range jsonArray(nullableText(row["to_emails"])) {
		if s, ok := e.(string); ok {
			storedTo = append(storedTo, Address{Email: s})
		}
	}
	to := ParseAddresses(header("to"))
	if len(to) == 0 {
		to = storedTo
	}
	text, _ := data["messageText"].(string)
	if text == "" {
		text, _ = asObject(data["preview"])["body"].(string)
	}
	var from *Address
	if fromEmail := colText(row["from_email"]); fromEmail != "" {
		from = &Address{Email: fromEmail, Name: nullableText(row["from_name"])}
	}
	return &OpenedEmail{
		Text:      js.Trim(text),
		Subject:   colText(row["subject"]),
		SentAt:    colText(row["sent_at"]),
		Direction: colText(row["direction"]),
		ThreadID:  colText(row["thread_id"]),
		From:      from,
		To:        to,
		Cc:        ParseAddresses(header("cc")),
	}, nil, nil
}

// KnownMessage is a synced email the CRM may reply to or forward.
type KnownMessage struct {
	ThreadID   string
	Subject    *string
	Visibility string
}

// KnownEmail is a synced email the CRM may reply to or forward: one it has
// linked to a contact, in an enabled mailbox. nil when there is none.
func KnownEmail(ctx context.Context, mailbox, id string) (*KnownMessage, error) {
	row, err := db.Get(ctx,
		`SELECT m.thread_id, m.subject, a.visibility
       FROM email_messages m
       JOIN email_accounts a ON a.mailbox = m.mailbox AND a.enabled = 1
      WHERE m.mailbox = ? AND m.id = ?
        AND EXISTS (SELECT 1 FROM email_message_contacts l WHERE l.mailbox = m.mailbox AND l.message_id = m.id)`,
		mailbox, id)
	if err != nil || row == nil {
		return nil, err
	}
	return &KnownMessage{ThreadID: colText(row["thread_id"]), Subject: nullableText(row["subject"]), Visibility: colText(row["visibility"])}, nil
}

// MailboxCounts is how many emails and contacts a mailbox has synced.
type MailboxCounts struct {
	Emails   float64 `json:"emails"`
	Contacts float64 `json:"contacts"`
}

// EmailCounts is how many emails and contacts the mailbox has synced.
func EmailCounts(ctx context.Context, mailbox string) (MailboxCounts, error) {
	e, err := db.Get(ctx, "SELECT COUNT(*) AS n FROM email_messages WHERE mailbox = ?", mailbox)
	if err != nil {
		return MailboxCounts{}, err
	}
	c, err := db.Get(ctx, "SELECT COUNT(DISTINCT contact_id) AS n FROM email_message_contacts WHERE mailbox = ?", mailbox)
	if err != nil {
		return MailboxCounts{}, err
	}
	var out MailboxCounts
	if e != nil && e["n"] != nil {
		out.Emails = js.Number(e["n"])
	}
	if c != nil && c["n"] != nil {
		out.Contacts = js.Number(c["n"])
	}
	return out, nil
}

// ── JS string helpers ──────────────────────────────────────────────

// encodeURIComponent is JS's: everything but A–Z a–z 0–9 - _ . ! ~ * ' ( )
// is percent-encoded as UTF-8.
func encodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// errURIMalformed is the URIError decodeURIComponent threw.
var errURIMalformed = errors.New("URI malformed")

// decodeURIComponent is JS's: each %XX escape decoded; a multi-byte character
// must be spelled as escapes that form valid UTF-8, else "URI malformed".
func decodeURIComponent(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		c, ok := escapedByte(s, i)
		if !ok {
			return "", errURIMalformed
		}
		i += 3
		if c < 0x80 {
			b.WriteByte(c)
			continue
		}
		var n int
		switch {
		case c&0xE0 == 0xC0:
			n = 1
		case c&0xF0 == 0xE0:
			n = 2
		case c&0xF8 == 0xF0:
			n = 3
		default:
			return "", errURIMalformed
		}
		seq := []byte{c}
		for j := 0; j < n; j++ {
			cc, ok := escapedByte(s, i)
			if !ok || cc&0xC0 != 0x80 {
				return "", errURIMalformed
			}
			seq = append(seq, cc)
			i += 3
		}
		if !utf8.Valid(seq) { // overlong forms, surrogates, past U+10FFFF
			return "", errURIMalformed
		}
		b.Write(seq)
	}
	return b.String(), nil
}

// escapedByte reads the %XX escape at s[i].
func escapedByte(s string, i int) (byte, bool) {
	if i+2 >= len(s) || s[i] != '%' {
		return 0, false
	}
	v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
	if err != nil {
		return 0, false
	}
	return byte(v), true
}

// localeCompare approximates String.prototype.localeCompare for the en
// locale (ICU's root collation): punctuation before digits before letters,
// letters by their base letter first, accents next, and lower case before
// upper case last. Enough to order Gmail label names as TS did.
func localeCompare(a, b string) int {
	ka, kb := collationKey(a), collationKey(b)
	for level := 0; level < 3; level++ {
		x, y := ka[level], kb[level]
		for i := 0; i < len(x) && i < len(y); i++ {
			if x[i] != y[i] {
				if x[i] < y[i] {
					return -1
				}
				return 1
			}
		}
		if len(x) != len(y) {
			if len(x) < len(y) {
				return -1
			}
			return 1
		}
	}
	return 0
}

// collationOrder is ICU's order of printable ASCII (case aside).
const collationOrder = " _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$0123456789abcdefghijklmnopqrstuvwxyz"

// accented maps common accented Latin letters to their base letter.
var accented = func() map[rune]rune {
	m := map[rune]rune{}
	for base, list := range map[rune]string{
		'a': "àáâãäåāăą", 'c': "çćĉċč", 'd': "ď", 'e': "èéêëēĕėęě", 'g': "ĝğġģ", 'h': "ĥ",
		'i': "ìíîïĩīĭįı", 'j': "ĵ", 'k': "ķ", 'l': "ĺļľ", 'n': "ñńņňŉ", 'o': "òóôõöøōŏő",
		'r': "ŕŗř", 's': "śŝşšß", 't': "ţť", 'u': "ùúûüũūŭůűų", 'w': "ŵ", 'y': "ýÿŷ", 'z': "źżž",
	} {
		for _, r := range list {
			m[r] = base
			m[unicode.ToUpper(r)] = base
		}
	}
	return m
}()

// collationKey is a string's primary (letter), secondary (accent) and
// tertiary (case) weights.
func collationKey(s string) [3][]int {
	var k [3][]int
	for _, r := range s {
		lower := unicode.ToLower(r)
		accent := 0
		if base, ok := accented[r]; ok {
			accent, lower = int(unicode.ToLower(r)), base
		}
		primary := 1000 + int(lower) // anything else sorts after the letters, by code point
		if lower < 128 {
			if i := strings.IndexRune(collationOrder, lower); i >= 0 {
				primary = i
			}
		}
		tertiary := 0
		if unicode.IsUpper(r) {
			tertiary = 1
		}
		k[0] = append(k[0], primary)
		k[1] = append(k[1], accent)
		k[2] = append(k[2], tertiary)
	}
	return k
}
