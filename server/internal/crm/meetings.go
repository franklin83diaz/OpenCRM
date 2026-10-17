package crm

// Meetings: the org's calendar and its Granola notes, read into the CRM. For
// each meeting with people from outside: when, with whom, which company, and,
// once its Granola note is found, what the call produced (a summary, the mood,
// tasks and insights). The transcript is read when needed and never stored. The
// rules live in meetings_rules.go; this file is the I/O around them.
//
// A run does one bounded job:
//
//	sync    read the calendar window, then the Granola notes updated since the
//	        last read, placing each note on its meeting. The first import reads
//	        `history_days` back; after that, every LiveIntervalMs, the last
//	        two days and the next two weeks. Resumable from `cursor`.
//	digest  when nothing is due, one queued call's transcript goes to the AI.
//
// Runs chain through the platform queue, so an import continues after the page
// that started it closes (as the Gmail sync does).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"opencrm/internal/clawnify"
	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

func isPersonal(d string) bool { return FreemailDomains[d] }

const dayMs = 86_400_000

const (
	// meetingsRunBudgetMs is the wall-clock budget of one sync run. Calendar
	// and Granola reads are I/O, so this bounds waiting.
	meetingsRunBudgetMs = 20_000
	// meetingsLeaseMs: a run's lease outlives its budget, so a crashed run frees the sync soon after.
	meetingsLeaseMs = 90_000
	// liveBackMs and aheadMs: a live read covers the last two days (late
	// edits) and the next two weeks (calls to prepare).
	liveBackMs = 2 * dayMs
	aheadMs    = 14 * dayMs
	// notesOverlapMs: notes are read again from an hour before the last read:
	// one can update after a later one was read.
	notesOverlapMs = 60 * 60_000
	notesPageSize  = 30
	eventsPageSize = 250
	// staleDigestSeconds: a digest left running this long was dropped by its run and is queued again.
	staleDigestSeconds = 180
	digestTimeout      = 25 * time.Second
	// transcriptChars is enough of a long call for the model; past it the middle goes (TranscriptText).
	transcriptChars = 150_000
)

// LiveIntervalMs is, once live, how often the calendar and Granola are read again.
const LiveIntervalMs = 15 * 60_000

// HistoryDays are how far back a first import may read.
var HistoryDays = []float64{30, 90, 365}

// MeetingSync is the meeting_sync row: the settings and where the sync is.
type MeetingSync struct {
	ID               float64
	Enabled          float64
	HistoryDays      float64
	About            string
	CalendarOwner    *string
	Phase            string // "idle" | "importing" | "live"
	Cursor           *string
	CalendarSyncedAt *string
	NotesSyncedUntil *string
	LastRunAt        *string
	LastError        *string
	RunningUntil     *string
	JobID            *string
	NextRunAt        *string
	UpdatedBy        *string
	CreatedAt        *string
	UpdatedAt        *string
}

func meetingSyncOf(row db.Row) *MeetingSync {
	if row == nil {
		return nil
	}
	return &MeetingSync{
		ID:               js.Number(row["id"]),
		Enabled:          js.Number(row["enabled"]),
		HistoryDays:      js.Number(row["history_days"]),
		About:            colText(row["about"]),
		CalendarOwner:    nullableText(row["calendar_owner"]),
		Phase:            colText(row["phase"]),
		Cursor:           nullableText(row["cursor"]),
		CalendarSyncedAt: nullableText(row["calendar_synced_at"]),
		NotesSyncedUntil: nullableText(row["notes_synced_until"]),
		LastRunAt:        nullableText(row["last_run_at"]),
		LastError:        nullableText(row["last_error"]),
		RunningUntil:     nullableText(row["running_until"]),
		JobID:            nullableText(row["job_id"]),
		NextRunAt:        nullableText(row["next_run_at"]),
		UpdatedBy:        nullableText(row["updated_by"]),
		CreatedAt:        nullableText(row["created_at"]),
		UpdatedAt:        nullableText(row["updated_at"]),
	}
}

// MeetingRow is a meetings row. LinkStatus is "auto" | "manual" | "unmatched"
// | "ignored" | "internal"; DigestStatus "none" | "queued" | "running" |
// "done" | "error".
type MeetingRow struct {
	ID              string
	CalendarEventID *string
	NoteID          *string
	Title           string
	StartsAt        string
	EndsAt          *string
	Attendees       *string
	CompanyID       *string
	LinkStatus      string
	CalendarURL     *string
	NoteURL         *string
	NoteUpdatedAt   *string
	Summary         *string
	Sentiment       *float64
	SentimentReason *string
	DigestStatus    string
	DigestError     *string
	DigestedAt      *string
	CreatedAt       *string
	UpdatedAt       *string
}

func meetingRowOf(row db.Row) *MeetingRow {
	if row == nil {
		return nil
	}
	return &MeetingRow{
		ID:              colText(row["id"]),
		CalendarEventID: nullableText(row["calendar_event_id"]),
		NoteID:          nullableText(row["note_id"]),
		Title:           colText(row["title"]),
		StartsAt:        colText(row["starts_at"]),
		EndsAt:          nullableText(row["ends_at"]),
		Attendees:       nullableText(row["attendees"]),
		CompanyID:       nullableText(row["company_id"]),
		LinkStatus:      colText(row["link_status"]),
		CalendarURL:     nullableText(row["calendar_url"]),
		NoteURL:         nullableText(row["note_url"]),
		NoteUpdatedAt:   nullableText(row["note_updated_at"]),
		Summary:         nullableText(row["summary"]),
		Sentiment:       nullableNumber(row["sentiment"]),
		SentimentReason: nullableText(row["sentiment_reason"]),
		DigestStatus:    colText(row["digest_status"]),
		DigestError:     nullableText(row["digest_error"]),
		DigestedAt:      nullableText(row["digested_at"]),
		CreatedAt:       nullableText(row["created_at"]),
		UpdatedAt:       nullableText(row["updated_at"]),
	}
}

func nullableNumber(v any) *float64 {
	if v == nil {
		return nil
	}
	n := js.Number(v)
	return &n
}

// ── Settings ───────────────────────────────────────────────────────

// SyncSettings is the meeting sync's settings row, nil when it was never saved.
func SyncSettings(ctx context.Context) (*MeetingSync, error) {
	row, err := db.Get(ctx, "SELECT * FROM meeting_sync WHERE id = 1")
	if err != nil {
		return nil, err
	}
	return meetingSyncOf(row), nil
}

// SettingsPatch is a settings write; nil fields keep what is stored.
type SettingsPatch struct {
	Enabled     *bool
	HistoryDays *float64
	About       *string
}

// SaveSettings saves settings. Turning sync on, or reaching further back,
// starts the import over (stored meetings are kept and refreshed). Turning it
// off stops reading; the meetings, tasks and insights already in the CRM stay.
// who nil is NULL.
func SaveSettings(ctx context.Context, patch SettingsPatch, who *string) (before, after *MeetingSync, err error) {
	before, err = SyncSettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	enabled := before != nil && before.Enabled != 0
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	history := 90.0
	if patch.HistoryDays != nil {
		history = *patch.HistoryDays
	} else if before != nil {
		history = before.HistoryDays
	}
	about := ""
	if patch.About != nil {
		about = *patch.About
	} else if before != nil {
		about = before.About
	}
	if _, err := db.Run(ctx,
		`INSERT INTO meeting_sync (id, enabled, history_days, about, updated_by, updated_at) VALUES (1, ?, ?, ?, ?, datetime('now'))
     ON CONFLICT (id) DO UPDATE SET enabled = excluded.enabled, history_days = excluded.history_days, about = excluded.about,
       updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		flag(enabled), history, about, textOrNil(who)); err != nil {
		return nil, nil, err
	}
	reachesFurther := before != nil && history > before.HistoryDays
	if enabled && (before == nil || before.Enabled == 0 || reachesFurther) {
		if _, err := db.Run(ctx,
			"UPDATE meeting_sync SET phase = 'importing', cursor = NULL, calendar_synced_at = NULL, notes_synced_until = NULL, next_run_at = NULL WHERE id = 1"); err != nil {
			return nil, nil, err
		}
	}
	after, err = SyncSettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	return before, after, nil
}

// ── What the CRM knows ─────────────────────────────────────────────

// companyIndex is contacts and company domains, read once per run, for
// telling which company a meeting is with.
func companyIndex(ctx context.Context) (CompanyIndex, error) {
	contacts, err := db.Query(ctx, "SELECT lower(trim(email)) AS email, company_id FROM contacts WHERE company_id IS NOT NULL AND email IS NOT NULL AND email != ''")
	if err != nil {
		return CompanyIndex{}, err
	}
	companies, err := db.Query(ctx, "SELECT id, domain FROM companies WHERE domain IS NOT NULL AND domain != '' ORDER BY created_at")
	if err != nil {
		return CompanyIndex{}, err
	}
	index := CompanyIndex{Contacts: map[string]string{}, ContactDomains: map[string]string{}, Domains: map[string]string{}}
	// Per domain, each company's count in the order first seen: the most
	// contacts wins, a tie the company seen first (a stable sort, as in TS).
	type tally struct {
		order  []string
		counts map[string]int
	}
	perDomain := map[string]*tally{}
	var domainOrder []string
	for _, c := range contacts {
		email, companyID := colText(c["email"]), colText(c["company_id"])
		index.Contacts[email] = companyID
		d := EmailDomain(email)
		if d == "" || isPersonal(d) {
			continue
		}
		t := perDomain[d]
		if t == nil {
			t = &tally{counts: map[string]int{}}
			perDomain[d] = t
			domainOrder = append(domainOrder, d)
		}
		if _, seen := t.counts[companyID]; !seen {
			t.order = append(t.order, companyID)
		}
		t.counts[companyID]++
	}
	for _, d := range domainOrder {
		t := perDomain[d]
		best := t.order[0]
		for _, id := range t.order[1:] {
			if t.counts[id] > t.counts[best] {
				best = id
			}
		}
		index.ContactDomains[d] = best
	}
	for _, c := range companies {
		d := NormaliseDomain(c["domain"])
		if _, has := index.Domains[d]; d != "" && !has {
			index.Domains[d] = colText(c["id"])
		}
	}
	return index, nil
}

// jsonPeople is a stored attendees list: the entries that are objects with a
// string email, as stored (the view returns them as they are).
func jsonPeople(v *string) []map[string]any {
	s := "[]"
	if v != nil {
		s = *v
	}
	parsed, err := web.ParseJSON(s)
	list, ok := parsed.([]any)
	if err != nil || !ok {
		return []map[string]any{}
	}
	out := []map[string]any{}
	for _, p := range list {
		if m, isObject := p.(map[string]any); isObject {
			if _, hasEmail := m["email"].(string); hasEmail {
				out = append(out, m)
			}
		}
	}
	return out
}

// peopleOf is the stored attendees as Persons, for matching.
func peopleOf(raw []map[string]any) []Person {
	out := make([]Person, len(raw))
	for i, p := range raw {
		out[i] = Person{Email: p["email"].(string)}
		if name, ok := p["name"].(string); ok {
			out[i].Name = &name
		}
	}
	return out
}

func matchCompanyID(people []Person, index CompanyIndex) any {
	if id, ok := MatchCompany(people, index, isPersonal); ok {
		return id
	}
	return nil
}

// RelinkUnmatched links unmatched meetings the CRM can now place: a contact or
// a company domain added since (by a person, the Gmail sync, or linking one
// of them by hand). It returns how many it linked.
func RelinkUnmatched(ctx context.Context) (int, error) {
	// A deleted company leaves its meetings linked to nothing: they wait to be linked again.
	if _, err := db.Run(ctx, "UPDATE meetings SET link_status = 'unmatched', updated_at = datetime('now') WHERE company_id IS NULL AND link_status IN ('auto', 'manual')"); err != nil {
		return 0, err
	}
	rows, err := db.Query(ctx, "SELECT id, attendees FROM meetings WHERE link_status = 'unmatched'")
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	index, err := companyIndex(ctx)
	if err != nil {
		return 0, err
	}
	linked := 0
	for _, r := range rows {
		company := matchCompanyID(peopleOf(jsonPeople(nullableText(r["attendees"]))), index)
		if company == nil {
			continue
		}
		if _, err := db.Run(ctx, "UPDATE meetings SET company_id = ?, link_status = 'auto', updated_at = datetime('now') WHERE id = ? AND link_status = 'unmatched'", company, r["id"]); err != nil {
			return 0, err
		}
		linked++
	}
	return linked, nil
}

// SyncCallItems: what a call produced goes where the call goes. The meeting's
// company is the one truth: its AI tasks and its insights follow it when the
// meeting is linked again (by a person, or by a later sync), and wait on no
// company while it is unlinked, so linking it again brings them back without
// reading the call twice. A task a person moved to another company is theirs
// (updateTask), and stays.
func SyncCallItems(ctx context.Context) error {
	if _, err := db.Run(ctx,
		`UPDATE tasks SET company_id = (SELECT m.company_id FROM meetings m WHERE m.id = tasks.meeting_id), updated_at = datetime('now')
      WHERE created_by = 'ai' AND meeting_id IS NOT NULL
        AND company_id IS NOT (SELECT m.company_id FROM meetings m WHERE m.id = tasks.meeting_id)`); err != nil {
		return err
	}
	_, err := db.Run(ctx,
		`UPDATE insights SET company_id = (SELECT m.company_id FROM meetings m WHERE m.id = insights.meeting_id), updated_at = datetime('now')
      WHERE meeting_id IS NOT NULL
        AND company_id IS NOT (SELECT m.company_id FROM meetings m WHERE m.id = insights.meeting_id)`)
	return err
}

// queueDigests: every linked meeting with a note and no digest yet waits for one.
func queueDigests(ctx context.Context) error {
	_, err := db.Run(ctx,
		`UPDATE meetings SET digest_status = 'queued', digest_error = NULL, updated_at = datetime('now')
      WHERE digest_status = 'none' AND note_id IS NOT NULL AND company_id IS NOT NULL AND link_status IN ('auto', 'manual')`)
	return err
}

// ── Calendar ───────────────────────────────────────────────────────

// decodeInto fills out from a decoded JSON value. A field of an unexpected
// type is left empty and the rest still read, as TS read what was there.
func decodeInto(v any, out any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, out)
}

func storeEvent(ctx context.Context, e GoogleEvent, own Own, index CompanyIndex) error {
	m := EventMeeting(e, own)
	if m == nil {
		return nil
	}
	if m.Internal {
		_, err := db.Run(ctx,
			`INSERT INTO meetings (id, calendar_event_id, title, starts_at, ends_at, attendees, link_status) VALUES (?, ?, '', ?, ?, '[]', 'internal')
       ON CONFLICT (calendar_event_id) DO UPDATE SET title = '', starts_at = excluded.starts_at, ends_at = excluded.ends_at, attendees = '[]',
         calendar_url = NULL, company_id = NULL, link_status = 'internal', updated_at = datetime('now')`,
			js.UUID(), m.ID, m.StartsAt, textOrNil(m.EndsAt))
		return err
	}
	company := matchCompanyID(m.Attendees, index)
	attendees, err := stringify(m.Attendees)
	if err != nil {
		return err
	}
	status := "unmatched"
	if company != nil {
		status = "auto"
	}
	// A person's link (or "not a customer meeting") outlives every later read.
	_, err = db.Run(ctx,
		`INSERT INTO meetings (id, calendar_event_id, title, starts_at, ends_at, attendees, calendar_url, company_id, link_status)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
     ON CONFLICT (calendar_event_id) DO UPDATE SET title = excluded.title, starts_at = excluded.starts_at, ends_at = excluded.ends_at,
       attendees = excluded.attendees, calendar_url = excluded.calendar_url,
       company_id = CASE WHEN meetings.link_status IN ('manual', 'ignored') THEN meetings.company_id ELSE excluded.company_id END,
       link_status = CASE WHEN meetings.link_status IN ('manual', 'ignored') THEN meetings.link_status ELSE excluded.link_status END,
       updated_at = datetime('now')`,
		js.UUID(), m.ID, m.Title, m.StartsAt, textOrNil(m.EndsAt), attendees, textOrNil(m.URL), company, status)
	return err
}

// pruneWindow drops meetings in a window the calendar no longer has: cancelled
// or moved out. Only after the whole window came in one page, so every event
// in it was seen. A meeting with a note keeps its row: the call happened.
func pruneWindow(ctx context.Context, from, to string, seen map[string]bool) error {
	stored, err := db.Query(ctx,
		"SELECT id, calendar_event_id FROM meetings WHERE calendar_event_id IS NOT NULL AND note_id IS NULL AND starts_at >= ? AND starts_at <= ?",
		from, to)
	if err != nil {
		return err
	}
	gone := []string{}
	for _, m := range stored {
		if !seen[colText(m["calendar_event_id"])] {
			gone = append(gone, colText(m["id"]))
		}
	}
	for _, part := range Chunk(gone, 90) {
		if _, err := db.Run(ctx, "DELETE FROM meetings WHERE id IN ("+marks(len(part))+")", stringParams(part)...); err != nil {
			return err
		}
	}
	return nil
}

// eventsPage is one page of the primary calendar's events.
type eventsPage struct {
	items         []GoogleEvent
	nextPageToken string
	// summary: for the primary calendar, the owner's address.
	summary *string
}

func calendarPage(ctx context.Context, cal *GoogleConnection, c *meetingCursor) (eventsPage, error) {
	args := map[string]any{
		"calendarId":   "primary",
		"timeMin":      c.From,
		"timeMax":      c.To,
		"singleEvents": true,
		"orderBy":      "startTime",
		"maxResults":   eventsPageSize,
	}
	if c.Page != nil && *c.Page != "" {
		args["pageToken"] = *c.Page
	}
	data, err := cal.Run(ctx, "EVENTS_LIST", args)
	if err != nil {
		return eventsPage{}, err
	}
	var page eventsPage
	raw, _ := data.(map[string]any)
	if items, ok := raw["items"].([]any); ok {
		page.items = make([]GoogleEvent, len(items))
		for i, item := range items {
			decodeInto(item, &page.items[i])
		}
	}
	page.nextPageToken, _ = raw["nextPageToken"].(string)
	if s, ok := raw["summary"].(string); ok {
		page.summary = &s
	}
	return page, nil
}

// ── Granola ────────────────────────────────────────────────────────

// noteSummary is a note as Granola's list gives it.
type noteSummary struct {
	ID        string
	UpdatedAt *string
}

func readNote(ctx context.Context, g *NotesClient, id string) (GranolaNote, map[string]any, error) {
	data, err := g.Get(ctx, "/v1/notes/"+encodeURIComponent(id), map[string]string{"include": "transcript"})
	if err != nil {
		return GranolaNote{}, nil, err
	}
	var note GranolaNote
	decodeInto(data, &note)
	raw, _ := data.(map[string]any)
	return note, raw, nil
}

// storeNote puts a note on the meeting it was taken in. A note whose meeting
// isn't on the calendar (a phone call, an ad hoc chat) becomes a meeting of its
// own, linked by the people it names, or left for someone to link.
func storeNote(ctx context.Context, g *NotesClient, summary noteSummary, own Own, index CompanyIndex) error {
	known, err := db.Get(ctx, "SELECT id, note_updated_at FROM meetings WHERE note_id = ?", summary.ID)
	if err != nil {
		return err
	}
	if known != nil && sameText(nullableText(known["note_updated_at"]), summary.UpdatedAt) {
		return nil
	}
	note, raw, err := readNote(ctx, g, summary.ID)
	if err != nil {
		return err
	}
	w, ok := NoteWindow(note)
	if !ok {
		return nil
	}
	var title string
	switch {
	case note.Title != nil:
		title = *note.Title
	case note.CalendarEvent != nil && note.CalendarEvent.EventTitle != nil:
		title = *note.CalendarEvent.EventTitle
	}
	title = js.Trim(title)
	updated := summary.UpdatedAt
	if u, isText := raw["updated_at"].(string); isText {
		updated = &u
	}

	var meetingID *string
	if known != nil {
		meetingID = nullableText(known["id"])
	}
	if meetingID == nil {
		named := ""
		if note.CalendarEvent != nil && note.CalendarEvent.CalendarEventID != nil {
			named = *note.CalendarEvent.CalendarEventID
		}
		rows, err := db.Query(ctx,
			`SELECT id, calendar_event_id, starts_at, ends_at FROM meetings
        WHERE note_id IS NULL AND (calendar_event_id = ? OR (starts_at >= ? AND starts_at <= ?))`,
			named, isoMs(parseMs(w.Start)-6*3_600_000), isoMs(parseMs(w.End)+3_600_000))
		if err != nil {
			return err
		}
		slots := make([]Slot, len(rows))
		for i, r := range rows {
			slots[i] = Slot{ID: colText(r["id"]), CalendarEventID: nullableText(r["calendar_event_id"]), StartsAt: colText(r["starts_at"]), EndsAt: nullableText(r["ends_at"])}
		}
		if id, placed := PlaceNote(note, w, slots); placed {
			meetingID = &id
		}
	}
	if meetingID != nil {
		_, err := db.Run(ctx,
			`UPDATE meetings SET note_id = ?, note_url = ?, note_updated_at = ?,
         title = CASE WHEN title = '' AND link_status != 'internal' THEN ? ELSE title END, updated_at = datetime('now')
       WHERE id = ?`,
			note.ID, textOrNil(note.WebURL), textOrNil(updated), title, *meetingID)
		return err
	}
	people := NotePeople(note, own)
	company := matchCompanyID(people, index)
	attendees, err := stringify(people)
	if err != nil {
		return err
	}
	status := "unmatched"
	if company != nil {
		status = "auto"
	}
	_, err = db.Run(ctx,
		`INSERT INTO meetings (id, note_id, title, starts_at, ends_at, attendees, note_url, note_updated_at, company_id, link_status)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		js.UUID(), note.ID, title, w.Start, w.End, attendees, textOrNil(note.WebURL), textOrNil(updated), company, status)
	return err
}

func sameText(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ── The sync pass ──────────────────────────────────────────────────

// meetingCursor is where a sync pass resumes. TS kept it as an object whose
// keys came and went: the has* flags say which ones it has, so the stored
// JSON leaves out the ones it never had, as JSON.stringify did.
type meetingCursor struct {
	Step string // "calendar" | "notes"
	From string
	To   string
	Page *string
	// NotesSince is where this pass reads Granola from.
	NotesSince *string
	NotesPage  *string
	// MaxSeen is the newest note update this pass has read; becomes
	// notes_synced_until when it ends.
	MaxSeen *string

	hasPage, hasNotesPage bool
}

func (c *meetingCursor) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	field := func(key string, v any) {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		k, _ := stringify(key)
		s, _ := stringify(v)
		b.WriteString(k + ":" + s)
	}
	field("step", c.Step)
	field("from", c.From)
	field("to", c.To)
	if c.hasPage {
		field("page", c.Page)
	}
	if c.NotesSince != nil {
		field("notes_since", *c.NotesSince)
	}
	if c.hasNotesPage {
		field("notes_page", c.NotesPage)
	}
	if c.MaxSeen != nil {
		field("max_seen", *c.MaxSeen)
	}
	return []byte("{" + b.String() + "}"), nil
}

// parseMeetingCursor reads a stored cursor: an object whose step is
// "calendar" or "notes", else nil.
func parseMeetingCursor(v *string) *meetingCursor {
	s := "null"
	if v != nil {
		s = *v
	}
	parsed, err := web.ParseJSON(s)
	m, ok := parsed.(map[string]any)
	if err != nil || !ok {
		return nil
	}
	step, _ := m["step"].(string)
	if step != "calendar" && step != "notes" {
		return nil
	}
	text := func(key string) *string {
		if s, ok := m[key].(string); ok {
			return &s
		}
		return nil
	}
	c := &meetingCursor{Step: step, Page: text("page"), NotesSince: text("notes_since"), NotesPage: text("notes_page"), MaxSeen: text("max_seen")}
	c.From, _ = m["from"].(string)
	c.To, _ = m["to"].(string)
	_, c.hasPage = m["page"]
	_, c.hasNotesPage = m["notes_page"]
	return c
}

func (c *meetingCursor) json() string {
	b, _ := c.MarshalJSON()
	return string(b)
}

// laterMark is the later of two times; an empty or missing one gives way to
// the other, and a tie or a time that is none keeps the first (meetings.ts's
// later, which differs from the rules' on those).
func laterMark(a, b *string) *string {
	if a == nil || *a == "" {
		return b
	}
	if b == nil || *b == "" {
		return a
	}
	if parseMs(*b) > parseMs(*a) {
		return b
	}
	return a
}

// errInvalidTime is the RangeError toISOString threw on a date that is none.
var errInvalidTime = errors.New("Invalid time value")

// isoChecked is new Date(ms).toISOString(), which threw outside JS's date range.
func isoChecked(ms float64) (string, error) {
	if math.IsNaN(ms) || math.Abs(ms) > 8.64e15 {
		return "", errInvalidTime
	}
	return isoMs(ms), nil
}

type passResult struct {
	finished      bool
	stored, notes int
}

// syncPass reads on from c until the pass ends or the budget runs out.
func syncPass(ctx context.Context, conn *clawnify.Connections, s *MeetingSync, c *meetingCursor, deadline int64) (passResult, error) {
	index, err := companyIndex(ctx)
	if err != nil {
		return passResult{}, err
	}
	stored, notes := 0, 0

	if c.Step == "calendar" {
		cal := CalendarConnection(ctx, conn)
		if cal == nil {
			return passResult{}, errors.New("Google Calendar is not connected. Connect it in Clawnify to keep syncing.")
		}
		for time.Now().UnixMilli() < deadline {
			firstPage := c.Page == nil || *c.Page == ""
			page, err := calendarPage(ctx, cal, c)
			if err != nil {
				return passResult{}, err
			}
			// The primary calendar is named after its owner; the owner also shows as `self` on each event.
			selves := []string{}
			for _, e := range page.items {
				for _, a := range e.Attendees {
					if a.Self && a.Email != "" {
						selves = append(selves, strings.ToLower(a.Email))
					}
				}
			}
			var owner *string
			switch {
			case page.summary != nil && strings.Contains(*page.summary, "@"):
				owner = strp(strings.ToLower(js.Trim(*page.summary)))
			case len(selves) > 0:
				owner = &selves[0]
			default:
				owner = s.CalendarOwner
			}
			if owner != nil && *owner != "" && !sameText(owner, s.CalendarOwner) {
				s.CalendarOwner = owner
				if _, err := db.Run(ctx, "UPDATE meeting_sync SET calendar_owner = ? WHERE id = 1", *owner); err != nil {
					return passResult{}, err
				}
			}
			own := OwnSide(append([]string{deref(owner)}, selves...), isPersonal)
			for _, e := range page.items {
				if err := storeEvent(ctx, e, own, index); err != nil {
					return passResult{}, err
				}
			}
			stored += len(page.items)
			if firstPage && page.nextPageToken == "" {
				seen := map[string]bool{}
				for _, e := range page.items {
					if e.ID != "" {
						seen[e.ID] = true
					}
				}
				if err := pruneWindow(ctx, c.From, c.To, seen); err != nil {
					return passResult{}, err
				}
			}
			if page.nextPageToken != "" {
				c.Page, c.hasPage = strp(page.nextPageToken), true
				continue
			}
			c.Step = "notes"
			c.Page, c.hasPage = nil, true
			if s.NotesSyncedUntil != nil && *s.NotesSyncedUntil != "" {
				since, err := isoChecked(parseMs(*s.NotesSyncedUntil) - notesOverlapMs)
				if err != nil {
					return passResult{}, err
				}
				c.NotesSince = &since
			} else {
				c.NotesSince = strp(c.From)
			}
			c.NotesPage, c.hasNotesPage = nil, true
			break
		}
		if c.Step == "calendar" {
			return passResult{finished: false, stored: stored, notes: notes}, nil
		}
	}

	g := NotesConnection(ctx, conn)
	if g == nil {
		return passResult{finished: true, stored: stored, notes: notes}, nil // no Granola: the calendar alone
	}
	own := OwnSide([]string{deref(s.CalendarOwner)}, isPersonal)
	for time.Now().UnixMilli() < deadline {
		query := map[string]string{"page_size": fmt.Sprint(notesPageSize)}
		if c.NotesSince != nil {
			query["updated_after"] = *c.NotesSince
		}
		if c.NotesPage != nil {
			query["cursor"] = *c.NotesPage
		}
		data, err := g.Get(ctx, "/v1/notes", query)
		if err != nil {
			return passResult{}, err
		}
		page, _ := data.(map[string]any)
		list, _ := page["notes"].([]any)
		for _, item := range list {
			// Out of time mid-page: the next run lists this page again, and skips the notes already stored.
			if time.Now().UnixMilli() > deadline {
				return passResult{finished: false, stored: stored, notes: notes}, nil
			}
			n, _ := item.(map[string]any)
			summary := noteSummary{}
			summary.ID, _ = n["id"].(string)
			if u, ok := n["updated_at"].(string); ok {
				summary.UpdatedAt = &u
			}
			if err := storeNote(ctx, g, summary, own, index); err != nil {
				return passResult{}, err
			}
			notes++
			c.MaxSeen = laterMark(c.MaxSeen, summary.UpdatedAt)
		}
		cursor, _ := page["cursor"].(string)
		if js.Truthy(page["hasMore"]) && cursor != "" {
			c.NotesPage, c.hasNotesPage = &cursor, true
			continue
		}
		return passResult{finished: true, stored: stored, notes: notes}, nil
	}
	return passResult{finished: false, stored: stored, notes: notes}, nil
}

// ── The digest ─────────────────────────────────────────────────────

// longDay is a day as en-GB's long date ("Wednesday, 14 October 2026"), or
// "Invalid Date" when it is none.
func longDay(day string) string {
	t, ok := js.ParseDate(day + "T12:00:00Z")
	if !ok {
		return "Invalid Date"
	}
	t = t.UTC()
	return fmt.Sprintf("%s, %d %s %d", t.Weekday(), t.Day(), t.Month(), t.Year())
}

func digestPrompt(about, company, day string) string {
	sells := ""
	if a := js.Trim(about); a != "" {
		sells = ", who sell: " + a
	}
	return strings.Join([]string{
		"You read the transcript of a call and note what matters for the account, for a CRM.",
		`"Us" is the team that keeps this CRM` + sells + `. "Them" is ` + company + ".",
		"The call was on " + longDay(day) + ".",
		"Reply with JSON only, in this shape:",
		`{"summary": string, "sentiment": -2 to 2, "sentiment_reason": string, "tasks": [{"title": string, "owed_by": "us" or "them", "due_date": "YYYY-MM-DD" or null, "quote": string}], "insights": [{"kind": "idea" or "expansion" or "risk", "text": string, "quote": string}]}`,
		"summary: two or three plain sentences: what the call was about and where things stand.",
		"sentiment: how the call went for the relationship: -2 badly (complaints, doubts about going on), 0 neutral, 2 very well (enthusiasm, commitment). sentiment_reason: one short sentence saying why.",
		`tasks: what someone committed to do in the call. owed_by is "us" when we promised it, "them" when they did. Give due_date only when a day or date was said, resolved against the call date ("Friday" is the first Friday after the call); otherwise null.`,
		`insights, one sentence each: "idea" is a use case or improvement worth proposing to them (a task they find painful, something they wish they had); "expansion" is room to grow the account (another team, more people, another product, budget); "risk" is anything that threatens the relationship (a complaint, a competitor, budget cuts, their champion leaving).`,
		"quote: the few words from the transcript it comes from, as said.",
		"Only include what the call supports; empty lists are fine. Never invent names, numbers or dates. Write in English and keep quotes as said.",
	}, "\n")
}

func digestInput(m *MeetingRow, note GranolaNote) string {
	var people []string
	for _, p := range jsonPeople(m.Attendees) {
		email := p["email"].(string)
		if name := p["name"]; js.Truthy(name) {
			people = append(people, js.String(name)+" <"+email+">")
		} else {
			people = append(people, email)
		}
	}
	notes := ""
	switch {
	case note.SummaryMarkdown != nil:
		notes = *note.SummaryMarkdown
	case note.SummaryText != nil:
		notes = *note.SummaryText
	}
	notes = u16Slice(js.Trim(notes), 0, 4000)
	title := m.Title
	if title == "" && note.Title != nil {
		title = *note.Title
	}
	if title == "" {
		title = "Untitled"
	}
	transcript := TranscriptText(note.Transcript, transcriptChars)
	if transcript == "" {
		transcript = "(none)"
	}
	parts := []string{"Call: " + title}
	if len(people) > 0 {
		parts = append(parts, "With: "+strings.Join(people, ", "))
	}
	if notes != "" {
		parts = append(parts, "Notes Granola took during the call:\n"+notes)
	}
	if len(note.Transcript) > 0 && !SpeakersKnown(note.Transcript) {
		parts = append(parts, "The transcript doesn't say who spoke (the call was held in person or on a speakerphone): tell us and them apart from what is said.")
	}
	parts = append(parts, "Transcript:\n"+transcript)
	return strings.Join(parts, "\n\n")
}

type digestResult struct {
	ran          bool
	outOfCredits bool
	err          *string
}

// digestNext digests the newest queued call. ran is false when none was waiting.
func digestNext(ctx context.Context, env clawnify.Env, conn *clawnify.Connections, s *MeetingSync, now time.Time) (digestResult, error) {
	if _, err := db.Run(ctx, `UPDATE meetings SET digest_status = 'queued' WHERE digest_status = 'running' AND updated_at < datetime('now', ?)`,
		fmt.Sprintf("-%d seconds", staleDigestSeconds)); err != nil {
		return digestResult{}, err
	}
	rows, err := db.Query(ctx,
		`UPDATE meetings SET digest_status = 'running', updated_at = datetime('now')
      WHERE id = (SELECT id FROM meetings WHERE digest_status = 'queued' ORDER BY starts_at DESC LIMIT 1)
      RETURNING *`)
	if err != nil {
		return digestResult{}, err
	}
	if len(rows) == 0 {
		return digestResult{ran: false}, nil
	}
	m := meetingRowOf(rows[0])
	if err := digest(ctx, env, conn, s, m, now); err != nil {
		message := u16Slice(errMessage(err), 0, 300)
		if _, err := db.Run(ctx, "UPDATE meetings SET digest_status = 'error', digest_error = ?, updated_at = datetime('now') WHERE id = ?", message, m.ID); err != nil {
			return digestResult{}, err
		}
		var me *ModelError
		if errors.As(err, &me) && me.OutOfCredits {
			// Nothing waiting can succeed until the org adds credits.
			if _, err := db.Run(ctx, "UPDATE meetings SET digest_status = 'error', digest_error = 'Out of Clawnify credits', updated_at = datetime('now') WHERE digest_status = 'queued'"); err != nil {
				return digestResult{}, err
			}
			return digestResult{ran: true, outOfCredits: true, err: &message}, nil
		}
		return digestResult{ran: true, err: &message}, nil
	}
	return digestResult{ran: true}, nil
}

// digest reads one call and stores what it produced; an error is stored on the meeting.
func digest(ctx context.Context, env clawnify.Env, conn *clawnify.Connections, s *MeetingSync, m *MeetingRow, now time.Time) error {
	var company db.Row
	if m.CompanyID != nil && *m.CompanyID != "" {
		var err error
		if company, err = db.Get(ctx, "SELECT name FROM companies WHERE id = ?", *m.CompanyID); err != nil {
			return err
		}
	}
	if m.NoteID == nil || *m.NoteID == "" || company == nil {
		_, err := db.Run(ctx, "UPDATE meetings SET digest_status = 'none', updated_at = datetime('now') WHERE id = ?", m.ID)
		return err
	}
	g := NotesConnection(ctx, conn)
	if g == nil {
		return errors.New("Granola is not connected")
	}
	note, _, err := readNote(ctx, g, *m.NoteID)
	if err != nil {
		return err
	}
	day := u16Slice(m.StartsAt, 0, 10)
	raw, err := Complete(ctx, env, digestPrompt(s.About, js.String(company["name"]), day), digestInput(m, note),
		CompleteOptions{MaxTokens: 1500, Timeout: digestTimeout})
	if err != nil {
		return err
	}
	d, err := CoerceDigest(raw, day)
	if err != nil {
		return err
	}
	fresh := float64(now.UnixMilli())-parseMs(m.StartsAt) <= FreshDays*dayMs
	// What a run that died midway wrote goes first, so a retry never doubles it.
	if _, err := db.Run(ctx, "DELETE FROM tasks WHERE meeting_id = ? AND created_by = 'ai'", m.ID); err != nil {
		return err
	}
	if _, err := db.Run(ctx, "DELETE FROM insights WHERE meeting_id = ?", m.ID); err != nil {
		return err
	}
	if fresh {
		for _, t := range d.Tasks {
			if _, err := db.Run(ctx,
				"INSERT INTO tasks (id, title, company_id, meeting_id, owed_by, due_date, quote, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, 'ai')",
				js.UUID(), t.Title, textOrNil(m.CompanyID), m.ID, string(t.OwedBy), textOrNil(t.DueDate), textOrNil(t.Quote)); err != nil {
				return err
			}
		}
	}
	for _, i := range d.Insights {
		if !fresh && i.Kind == "risk" {
			continue
		}
		if _, err := db.Run(ctx, "INSERT INTO insights (id, company_id, meeting_id, kind, text, quote) VALUES (?, ?, ?, ?, ?, ?)",
			js.UUID(), textOrNil(m.CompanyID), m.ID, string(i.Kind), i.Text, textOrNil(i.Quote)); err != nil {
			return err
		}
	}
	var sentiment any
	if d.Sentiment != nil {
		sentiment = float64(*d.Sentiment)
	}
	_, err = db.Run(ctx,
		`UPDATE meetings SET summary = ?, sentiment = ?, sentiment_reason = ?, digest_status = 'done', digest_error = NULL,
         digested_at = ?, updated_at = datetime('now') WHERE id = ?`,
		d.Summary, sentiment, textOrNil(d.SentimentReason), js.ISO(now), m.ID)
	return err
}

// ── Running ────────────────────────────────────────────────────────

// claimMeetings takes the sync for one run; false when another run holds it.
func claimMeetings(ctx context.Context, now time.Time) (bool, error) {
	rows, err := db.Query(ctx, `UPDATE meeting_sync SET running_until = ?
      WHERE id = 1 AND enabled = 1 AND (running_until IS NULL OR running_until < ?)
      RETURNING id`, js.ISO(now.Add(meetingsLeaseMs*time.Millisecond)), js.ISO(now))
	if err != nil {
		return false, err
	}
	return len(rows) == 1, nil
}

func queuedCount(ctx context.Context) (float64, error) {
	row, err := db.Get(ctx, "SELECT COUNT(*) AS n FROM meetings WHERE digest_status IN ('queued', 'running')")
	if err != nil || row == nil || row["n"] == nil {
		return 0, err
	}
	return js.Number(row["n"]), nil
}

// MeetingRunResult is what one run did (TS RunResult). Fields TS left
// undefined are nil and left out; the field order is TS's for every result.
type MeetingRunResult struct {
	Status string  `json:"status"` // "ran" | "busy" | "off" | "idle" | "error"
	Job    *string `json:"job,omitempty"`
	Phase  *string `json:"phase,omitempty"`
	Stored *int    `json:"stored,omitempty"`
	Notes  *int    `json:"notes,omitempty"`
	Error  *string `json:"error,omitempty"`
	// More: work is left that should run again right away.
	More *bool `json:"more,omitempty"`
}

// RunMeetings is one bounded run: continue (or start) a sync pass when one is
// due, otherwise digest one queued call. sync starts a pass even when none is
// due ("Sync now").
func RunMeetings(ctx context.Context, env clawnify.Env, conn *clawnify.Connections, now time.Time, sync bool) (MeetingRunResult, error) {
	s, err := SyncSettings(ctx)
	if err != nil {
		return MeetingRunResult{}, err
	}
	if s == nil || s.Enabled == 0 {
		return MeetingRunResult{Status: "off"}, nil
	}
	claimed, err := claimMeetings(ctx, now)
	if err != nil {
		return MeetingRunResult{}, err
	}
	if !claimed {
		return MeetingRunResult{Status: "busy"}, nil
	}

	started := time.Now().UnixMilli()
	phase := s.Phase
	if phase == "idle" {
		phase = "importing"
	}
	cursor := parseMeetingCursor(s.Cursor)
	result, err := runClaimed(ctx, env, conn, s, now, sync, started, &phase, &cursor)
	if err == nil {
		return result, nil
	}
	message := errMessage(err)
	var cursorJSON any
	if cursor != nil {
		cursorJSON = cursor.json()
	}
	if _, err := db.Run(ctx, "UPDATE meeting_sync SET phase = ?, cursor = ?, last_run_at = ?, last_error = ?, running_until = NULL WHERE id = 1",
		phase, cursorJSON, js.ISO(now), message); err != nil {
		return MeetingRunResult{}, err
	}
	return MeetingRunResult{Status: "error", Phase: &phase, Error: &message}, nil
}

// runClaimed is RunMeetings' try block: an error is the run's, stored by the caller.
func runClaimed(ctx context.Context, env clawnify.Env, conn *clawnify.Connections, s *MeetingSync, now time.Time, sync bool,
	started int64, phase *string, cursor **meetingCursor) (MeetingRunResult, error) {
	due := s.CalendarSyncedAt == nil || *s.CalendarSyncedAt == "" ||
		float64(now.UnixMilli())-parseMs(*s.CalendarSyncedAt) >= LiveIntervalMs-60_000
	if *cursor == nil && (*phase == "importing" || due || sync) {
		back := float64(liveBackMs)
		if *phase == "importing" {
			back = s.HistoryDays * dayMs
		}
		from, err := isoChecked(float64(now.UnixMilli()) - back)
		if err != nil {
			return MeetingRunResult{}, err
		}
		*cursor = &meetingCursor{Step: "calendar", From: from, To: js.ISO(now.Add(aheadMs * time.Millisecond)), hasPage: true}
	}

	if c := *cursor; c != nil {
		pass, err := syncPass(ctx, conn, s, c, started+meetingsRunBudgetMs)
		if err != nil {
			return MeetingRunResult{}, err
		}
		if _, err := RelinkUnmatched(ctx); err != nil {
			return MeetingRunResult{}, err
		}
		if err := SyncCallItems(ctx); err != nil {
			return MeetingRunResult{}, err
		}
		if err := queueDigests(ctx); err != nil {
			return MeetingRunResult{}, err
		}
		nowISO := js.ISO(now)
		if pass.finished {
			until := laterMark(s.NotesSyncedUntil, c.MaxSeen)
			*phase = "live"
			var untilParam any
			if until != nil {
				untilParam = *until
				if *until != "" && *until > nowISO {
					untilParam = nowISO
				}
			}
			if _, err := db.Run(ctx,
				`UPDATE meeting_sync SET phase = 'live', cursor = NULL, calendar_synced_at = ?, notes_synced_until = ?,
             last_run_at = ?, last_error = NULL, running_until = NULL WHERE id = 1`,
				nowISO, untilParam, nowISO); err != nil {
				return MeetingRunResult{}, err
			}
		} else {
			if _, err := db.Run(ctx,
				"UPDATE meeting_sync SET phase = ?, cursor = ?, last_run_at = ?, last_error = NULL, running_until = NULL WHERE id = 1",
				*phase, c.json(), nowISO); err != nil {
				return MeetingRunResult{}, err
			}
		}
		more := !pass.finished
		if !more {
			n, err := queuedCount(ctx)
			if err != nil {
				return MeetingRunResult{}, err
			}
			more = n > 0
		}
		return MeetingRunResult{Status: "ran", Job: strp("sync"), Phase: strp(*phase), Stored: &pass.stored, Notes: &pass.notes, More: &more}, nil
	}

	d, err := digestNext(ctx, env, conn, s, now)
	if err != nil {
		return MeetingRunResult{}, err
	}
	if _, err := db.Run(ctx, "UPDATE meeting_sync SET last_run_at = ?, running_until = NULL WHERE id = 1", js.ISO(now)); err != nil {
		return MeetingRunResult{}, err
	}
	if !d.ran {
		return MeetingRunResult{Status: "idle", Phase: strp(*phase)}, nil
	}
	more := false
	if !d.outOfCredits {
		n, err := queuedCount(ctx)
		if err != nil {
			return MeetingRunResult{}, err
		}
		more = n > 0
	}
	return MeetingRunResult{Status: "ran", Job: strp("digest"), Phase: strp(*phase), Error: d.err, More: &more}, nil
}

// ── Scheduling ─────────────────────────────────────────────────────

// ScheduleRun books the next run on the platform queue, keyed by the app's
// host and the target minute so two requests noticing the same gap book one
// job; never for the current minute (see BookableAt). Without a queue (local
// dev, an outage) the next page view or "Sync now" books again.
func ScheduleRun(ctx context.Context, env clawnify.Env, origin string, when time.Time) {
	runAt := BookableAt(when, time.Now())
	host, ok := urlHost(origin)
	if !ok {
		return // new URL(origin) threw: no queue booking
	}
	job, err := clawnify.Enqueue(ctx, env, clawnify.EnqueueOptions{
		TargetURL:      origin + "/api/meetings/run",
		Payload:        map[string]any{},
		RunAt:          runAt,
		IdempotencyKey: "crm-meetings-" + host + "-" + js.ISO(runAt)[:16],
		MaxAttempts:    3,
	})
	if err != nil {
		return // no queue: EnsureScheduled books it later
	}
	db.Run(ctx, "UPDATE meeting_sync SET job_id = ?, next_run_at = ? WHERE id = 1", job.ID, js.ISO(runAt))
}

// EnsureScheduled books a run when sync is on and none is coming, or the
// booked one is long overdue.
func EnsureScheduled(ctx context.Context, env clawnify.Env, origin string, s *MeetingSync, now time.Time) {
	if s.Enabled == 0 {
		return
	}
	overdue := s.NextRunAt == nil || *s.NextRunAt == ""
	if !overdue {
		t, ok := js.ParseDate(*s.NextRunAt)
		overdue = ok && t.UnixMilli() < now.UnixMilli()-5*60_000
	}
	if overdue {
		ScheduleRun(ctx, env, origin, now)
	}
}

// CancelScheduled drops the booked run, if any.
func CancelScheduled(ctx context.Context, env clawnify.Env, s *MeetingSync) error {
	if s.JobID == nil || *s.JobID == "" {
		return nil
	}
	_ = clawnify.CancelJob(ctx, env, *s.JobID) // already delivered or gone
	_, err := db.Run(ctx, "UPDATE meeting_sync SET job_id = NULL, next_run_at = NULL WHERE id = 1")
	return err
}

// ── Reading and changing meetings ──────────────────────────────────

// MeetingView is a meeting as the API shows it. LinkStatus is never
// "internal": internal meetings are never shown.
type MeetingView struct {
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	StartsAt        string           `json:"starts_at"`
	EndsAt          *string          `json:"ends_at"`
	Attendees       []map[string]any `json:"attendees"`
	CompanyID       *string          `json:"company_id"`
	CompanyName     *string          `json:"company_name"`
	CompanyDomain   *string          `json:"company_domain"`
	LinkStatus      string           `json:"link_status"`
	CalendarURL     *string          `json:"calendar_url"`
	NoteURL         *string          `json:"note_url"`
	Summary         *string          `json:"summary"`
	Sentiment       *float64         `json:"sentiment"`
	SentimentReason *string          `json:"sentiment_reason"`
	DigestStatus    string           `json:"digest_status"`
	DigestError     *string          `json:"digest_error"`
	HasNote         bool             `json:"has_note"`
}

const viewColumns = `m.id, m.title, m.starts_at, m.ends_at, m.attendees, m.company_id, c.name AS company_name, c.domain AS company_domain,
  m.link_status, m.calendar_url, m.note_url, m.note_id, m.summary, m.sentiment, m.sentiment_reason, m.digest_status, m.digest_error`

func toView(r db.Row) MeetingView {
	return MeetingView{
		ID:              colText(r["id"]),
		Title:           colText(r["title"]),
		StartsAt:        colText(r["starts_at"]),
		EndsAt:          nullableText(r["ends_at"]),
		Attendees:       jsonPeople(nullableText(r["attendees"])),
		CompanyID:       nullableText(r["company_id"]),
		CompanyName:     nullableText(r["company_name"]),
		CompanyDomain:   nullableText(r["company_domain"]),
		LinkStatus:      colText(r["link_status"]),
		CalendarURL:     nullableText(r["calendar_url"]),
		NoteURL:         nullableText(r["note_url"]),
		Summary:         nullableText(r["summary"]),
		Sentiment:       nullableNumber(r["sentiment"]),
		SentimentReason: nullableText(r["sentiment_reason"]),
		DigestStatus:    colText(r["digest_status"]),
		DigestError:     nullableText(r["digest_error"]),
		HasNote:         js.Truthy(r["note_id"]),
	}
}

// ListMeetingsOptions narrows the list. Link is "unmatched" | "ignored" and
// When "upcoming" | "past" ("" for neither); a nil Limit is 25, a nil Offset 0.
type ListMeetingsOptions struct {
	CompanyID string
	Link      string
	When      string
	Limit     *float64
	Offset    *float64
}

// MeetingList is a page of meetings and how many there are.
type MeetingList struct {
	Meetings []MeetingView `json:"meetings"`
	Total    float64       `json:"total"`
}

// ListMeetings is meetings, newest first, never the internal ones. CompanyID
// narrows to one company; Link to a link status (unmatched lists those waiting
// for a person); When to the ones still to come (soonest first) or already held.
func ListMeetings(ctx context.Context, opts ListMeetingsOptions, now time.Time) (MeetingList, error) {
	where := []string{"m.link_status != 'internal'"}
	params := []any{}
	if opts.CompanyID != "" {
		where = append(where, "m.company_id = ?")
		params = append(params, opts.CompanyID)
	}
	if opts.Link != "" {
		where = append(where, "m.link_status = ?")
		params = append(params, opts.Link)
	}
	if opts.When == "upcoming" {
		where = append(where, "m.starts_at > ?")
		params = append(params, js.ISO(now))
	}
	if opts.When == "past" {
		where = append(where, "m.starts_at <= ?")
		params = append(params, js.ISO(now))
	}
	order := "m.starts_at DESC"
	if opts.When == "upcoming" {
		order = "m.starts_at ASC"
	}
	limit, offset := 25.0, 0.0
	if opts.Limit != nil {
		limit = *opts.Limit
	}
	if opts.Offset != nil {
		offset = *opts.Offset
	}
	limit = math.Min(math.Max(limit, 1), 100)
	offset = math.Max(offset, 0)
	rows, err := db.Query(ctx,
		`SELECT `+viewColumns+` FROM meetings m LEFT JOIN companies c ON c.id = m.company_id
      WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		append(append([]any{}, params...), limit, offset)...)
	if err != nil {
		return MeetingList{}, err
	}
	total, err := db.Get(ctx, `SELECT COUNT(*) AS n FROM meetings m WHERE `+strings.Join(where, " AND "), params...)
	if err != nil {
		return MeetingList{}, err
	}
	list := MeetingList{Meetings: make([]MeetingView, len(rows))}
	for i, r := range rows {
		list.Meetings[i] = toView(r)
	}
	if total != nil && total["n"] != nil {
		list.Total = js.Number(total["n"])
	}
	return list, nil
}

// GetMeeting is one meeting, nil when there is none (or it is internal).
func GetMeeting(ctx context.Context, id string) (*MeetingView, error) {
	r, err := db.Get(ctx, `SELECT `+viewColumns+` FROM meetings m LEFT JOIN companies c ON c.id = m.company_id WHERE m.id = ? AND m.link_status != 'internal'`, id)
	if err != nil || r == nil {
		return nil, err
	}
	v := toView(r)
	return &v, nil
}

// LinkTarget is what a person links a meeting to: Ignored (not an account
// meeting), else CompanyID (nil or "" unlinks).
type LinkTarget struct {
	Ignored   bool
	CompanyID *string
}

// LinkMeeting: a person links a meeting to a company, unlinks it, or marks it
// as not an account meeting. A company with no domain takes the domain of the
// people met, so their later meetings link themselves; other unlinked meetings
// the CRM can now place go with it. What the call produced follows
// (SyncCallItems); marking it as not an account meeting drops what it left
// open. nil when there is no such meeting.
func LinkMeeting(ctx context.Context, id string, to LinkTarget) (*MeetingView, error) {
	m, err := db.Get(ctx, "SELECT * FROM meetings WHERE id = ? AND link_status != 'internal'", id)
	if err != nil || m == nil {
		return nil, err
	}
	switch {
	case to.Ignored:
		for _, sql := range []string{
			"UPDATE meetings SET company_id = NULL, link_status = 'ignored', updated_at = datetime('now') WHERE id = ?",
			"DELETE FROM tasks WHERE meeting_id = ? AND created_by = 'ai' AND done_at IS NULL",
			"DELETE FROM insights WHERE meeting_id = ? AND status = 'open'",
		} {
			if _, err := db.Run(ctx, sql, id); err != nil {
				return nil, err
			}
		}
	case to.CompanyID != nil && *to.CompanyID != "":
		if _, err := db.Run(ctx, "UPDATE meetings SET company_id = ?, link_status = 'manual', updated_at = datetime('now') WHERE id = ?", *to.CompanyID, id); err != nil {
			return nil, err
		}
		domains := newIDSet()
		for _, p := range jsonPeople(nullableText(m["attendees"])) {
			if d := EmailDomain(p["email"].(string)); d != "" && !isPersonal(d) {
				domains.add(d)
			}
		}
		if len(domains.order) == 1 {
			if _, err := db.Run(ctx, "UPDATE companies SET domain = ?, updated_at = datetime('now') WHERE id = ? AND (domain IS NULL OR TRIM(domain) = '')",
				domains.order[0], *to.CompanyID); err != nil {
				return nil, err
			}
		}
		if _, err := RelinkUnmatched(ctx); err != nil {
			return nil, err
		}
	default:
		if _, err := db.Run(ctx, "UPDATE meetings SET company_id = NULL, link_status = 'unmatched', updated_at = datetime('now') WHERE id = ?", id); err != nil {
			return nil, err
		}
	}
	if err := SyncCallItems(ctx); err != nil {
		return nil, err
	}
	if err := queueDigests(ctx); err != nil {
		return nil, err
	}
	return GetMeeting(ctx, id)
}

// RetryDigest reads a call again after its digest failed. False when the
// meeting has no note or company, or its digest isn't failed or missing.
func RetryDigest(ctx context.Context, id string) (bool, error) {
	rows, err := db.Query(ctx,
		`UPDATE meetings SET digest_status = 'queued', digest_error = NULL, updated_at = datetime('now')
      WHERE id = ? AND digest_status IN ('error', 'none') AND note_id IS NOT NULL AND company_id IS NOT NULL RETURNING id`, id)
	if err != nil {
		return false, err
	}
	return len(rows) == 1, nil
}

// MeetingTotals counts the meetings that are shown (never the internal ones).
type MeetingTotals struct {
	Meetings  float64 `json:"meetings"`
	Linked    float64 `json:"linked"`
	Unmatched float64 `json:"unmatched"`
	WithNotes float64 `json:"with_notes"`
	Digested  float64 `json:"digested"`
	Waiting   float64 `json:"waiting"`
	Failed    float64 `json:"failed"`
}

// MeetingCounts is how many meetings there are, linked, waiting for a person,
// with notes, digested, waiting for a digest and failed (TS counts).
func MeetingCounts(ctx context.Context) (MeetingTotals, error) {
	r, err := db.Get(ctx,
		`SELECT COUNT(*) AS meetings,
            SUM(CASE WHEN link_status IN ('auto', 'manual') THEN 1 ELSE 0 END) AS linked,
            SUM(CASE WHEN link_status = 'unmatched' THEN 1 ELSE 0 END) AS unmatched,
            SUM(CASE WHEN note_id IS NOT NULL THEN 1 ELSE 0 END) AS with_notes,
            SUM(CASE WHEN digest_status = 'done' THEN 1 ELSE 0 END) AS digested,
            SUM(CASE WHEN digest_status IN ('queued', 'running') THEN 1 ELSE 0 END) AS waiting,
            SUM(CASE WHEN digest_status = 'error' THEN 1 ELSE 0 END) AS failed
       FROM meetings WHERE link_status != 'internal'`)
	if err != nil {
		return MeetingTotals{}, err
	}
	n := func(k string) float64 {
		if r == nil || r[k] == nil {
			return 0
		}
		return js.Number(r[k])
	}
	return MeetingTotals{Meetings: n("meetings"), Linked: n("linked"), Unmatched: n("unmatched"), WithNotes: n("with_notes"),
		Digested: n("digested"), Waiting: n("waiting"), Failed: n("failed")}, nil
}
