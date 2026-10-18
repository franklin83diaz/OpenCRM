package crm

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf16"

	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── Tasks and deal updates ─────────────────────────────────────────
//
// What calls promise (tasks: ours or theirs, with a date), and what an agent
// tells the CRM happened on a deal. People, API keys and agents may write
// them (mayWrite); TS had no try around these writes, so a database error is
// Hono's plain 500. (TS's insights routes, what calls said about accounts,
// are gone with the AI.)
//
// Route order: nothing else matches /api/tasks/{id}; POST
// /api/deals/{id}/updates has a segment more than the deal {id} routes, so
// ServeMux's choice is Hono's.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/tasks", app.listTasks)
		rt.Handle("POST /api/tasks", app.createTask)
		rt.Handle("PUT /api/tasks/{id}", app.updateTask)
		rt.Handle("DELETE /api/tasks/{id}", app.deleteTask)
		rt.Handle("POST /api/deals/{id}/updates", app.dealUpdate)
	})
}

// ── Tasks ──

const taskSelect = `SELECT t.*, co.name AS company_name, m.title AS meeting_title, m.starts_at AS meeting_starts_at
  FROM tasks t LEFT JOIN companies co ON co.id = t.company_id LEFT JOIN meetings m ON m.id = t.meeting_id`

// taskView is a task as the API shows it: owed_by "us" unless it is "them",
// and done when it has a done_at.
func taskView(r db.Row) db.Row {
	out := make(db.Row, len(r)+1)
	for k, v := range r {
		out[k] = v
	}
	out["owed_by"] = "us"
	if r["owed_by"] == "them" {
		out["owed_by"] = "them"
	}
	out["done"] = js.Truthy(r["done_at"])
	return out
}

// readTask is one task by id as the API shows it. TS asserted the row was
// there, so a row gone missing is the TypeError taskView threw (a 500).
func readTask(ctx context.Context, id string) (db.Row, error) {
	row, err := db.Get(ctx, taskSelect+" WHERE t.id = ?", id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, errReadUndefined("owed_by")
	}
	return taskView(row), nil
}

// errReadUndefined is the TypeError of reading a property of undefined.
func errReadUndefined(key string) error {
	return fmt.Errorf("Cannot read properties of undefined (reading '%s')", key)
}

// whoActs is `user(c)?.email ?? caller(c) ?? null`: the signed-in person's
// address (a token's user's, for an API token), else who is calling.
func whoActs(r *http.Request) string {
	if u := web.CurrentUser(r); u != nil && u.Email != "" {
		return u.Email
	}
	return web.Caller(r)
}

var listTasksSchema = zObject(
	zKey("company_id", zOptional(zString(0, 0))),
	// One deal's tasks: its own, and its company's that are on no deal.
	zKey("deal_id", zOptional(zString(0, 0))),
	// Default open.
	zKey("status", zOptional(zEnum("open", "done", "all"))),
	zKey("owed_by", zOptional(zEnum("us", "them"))),
	// Default 50, max 200.
	zKey("limit", zOptional(zString(0, 0))),
)

type tasksBody struct {
	Tasks []db.Row `json:"tasks"`
}

// limitOf is `Math.min(max, Math.max(1, parseInt(q.limit || "<fallback>", 10) || fallback))`.
func limitOf(q map[string]any, fallback, max float64) float64 {
	n := js.ParseInt(textAt(q, "limit"))
	if n == 0 || math.IsNaN(n) {
		n = fallback
	}
	return math.Min(max, math.Max(1, n))
}

// listTasks lists tasks: open ones by due date (undated last), done ones
// newest first.
func (app *App) listTasks(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := zodIssues(listTasksSchema, q); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		where := []string{}
		params := []any{}
		status := textAt(q, "status")
		if status == "" {
			status = "open"
		}
		if status == "open" {
			where = append(where, "t.done_at IS NULL")
		}
		if status == "done" {
			where = append(where, "t.done_at IS NOT NULL")
		}
		if c := textAt(q, "company_id"); c != "" {
			where = append(where, "t.company_id = ?")
			params = append(params, c)
		}
		if d := textAt(q, "deal_id"); d != "" {
			// The deal's own tasks, and its company's (its own, else its contact's) on no deal.
			where = append(where, `(t.deal_id = ? OR (t.deal_id IS NULL AND t.company_id = (
        SELECT COALESCE(d.company_id, ct.company_id) FROM deals d LEFT JOIN contacts ct ON ct.id = d.contact_id WHERE d.id = ?)))`)
			params = append(params, d, d)
		}
		if o := textAt(q, "owed_by"); o != "" {
			where = append(where, "t.owed_by = ?")
			params = append(params, o)
		}
		limit := limitOf(q, 50, 200)
		cond := ""
		if len(where) > 0 {
			cond = "WHERE " + strings.Join(where, " AND ")
		}
		rows, err := db.Query(ctx, taskSelect+" "+cond+`
       ORDER BY t.done_at IS NOT NULL, CASE WHEN t.done_at IS NULL THEN COALESCE(t.due_date, '9999-12-31') END, t.done_at DESC, t.created_at DESC
       LIMIT ?`, append(params, limit)...)
		if err != nil {
			return 0, nil, err
		}
		tasks := make([]db.Row, len(rows))
		for i, row := range rows {
			tasks[i] = taskView(row)
		}
		return 200, tasksBody{tasks}, nil
	})
}

// taskWrite is the fields a task write may send, after its own (title, done).
func taskWrite(title zodCheck, more ...zodKey) zodCheck {
	return zObject(append([]zodKey{
		zKey("title", title),
		zKey("company_id", zOptional(zNullable(zString(0, 0)))),
		zKey("contact_id", zOptional(zNullable(zString(0, 0)))),
		zKey("deal_id", zOptional(zNullable(zString(0, 0)))),
		zKey("owed_by", zOptional(zEnum("us", "them"))),
		// YYYY-MM-DD, or null for none.
		zKey("due_date", zOptional(zNullable(zString(0, 0)))),
	}, more...)...)
}

var (
	createTaskSchema = taskWrite(zString(1, 0))
	updateTaskSchema = taskWrite(zOptional(zString(0, 0)), zKey("done", zOptional(zBool())))
)

// checkTaskWrite checks a task write's links and date: the error to answer,
// or "".
func checkTaskWrite(ctx context.Context, body map[string]any) (string, error) {
	if d := textAt(body, "due_date"); d != "" && !IsDay(d) {
		return "due_date must be a date as YYYY-MM-DD", nil
	}
	for _, link := range []struct{ key, sql, missing string }{
		{"company_id", "SELECT id FROM companies WHERE id = ?", "Company not found"},
		{"contact_id", "SELECT id FROM contacts WHERE id = ?", "Contact not found"},
		{"deal_id", "SELECT id FROM deals WHERE id = ?", "Deal not found"},
	} {
		id := textAt(body, link.key)
		if id == "" {
			continue
		}
		found, err := db.Get(ctx, link.sql, id)
		if err != nil {
			return "", err
		}
		if found == nil {
			return link.missing, nil
		}
	}
	return "", nil
}

// taskTitle is `title.trim().slice(0, 300)`.
func taskTitle(s string) string { return jsPrefix(js.Trim(s), 300) }

// jsPrefix is s.slice(0, n) in UTF-16 units, as JS hands it to D1: a
// surrogate pair cut in half leaves its high half, which D1 stores as its
// three WTF-8 bytes (and reads back as three U+FFFD). Go strings keep those
// bytes as they are.
func jsPrefix(s string, n int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			if w == 2 && units+1 == n { // half a pair fits: its high surrogate
				hi, _ := utf16.EncodeRune(r)
				return s[:i] + string(wtf8(hi))
			}
			return s[:i]
		}
		units += w
	}
	return s
}

// wtf8 is a lone surrogate's three bytes, as UTF-8 would encode it.
func wtf8(r rune) []byte {
	return []byte{0xe0 | byte(r>>12), 0x80 | byte(r>>6)&0x3f, 0x80 | byte(r)&0x3f}
}

type taskBody struct {
	Task db.Row `json:"task"`
}

// createTask creates a task. The body is validated before the caller is
// checked, as zod-openapi's validators ran before the handler.
func (app *App) createTask(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := zodIssues(createTaskSchema, v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	if !mayWrite(r) {
		return web.JSON(w, http.StatusForbidden, errorBody{"Sign in to add tasks."})
	}
	ctx := r.Context()
	body := v.(map[string]any)
	title := taskTitle(body["title"].(string))
	if title == "" {
		return web.JSON(w, http.StatusBadRequest, errorBody{"A task needs a title"})
	}
	problem, err := checkTaskWrite(ctx, body)
	if err != nil {
		return err
	}
	if problem != "" {
		return web.JSON(w, http.StatusBadRequest, errorBody{problem})
	}
	id := js.UUID()
	owedBy := any("us") // body.owed_by ?? "us"
	if o, sent := body["owed_by"]; sent {
		owedBy = o
	}
	if _, err := db.Run(ctx, "INSERT INTO tasks (id, title, company_id, contact_id, deal_id, owed_by, due_date, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		id, title, body["company_id"], body["contact_id"], body["deal_id"], owedBy, orNull(textAt(body, "due_date")), whoActs(r)); err != nil {
		return err
	}
	task, err := readTask(ctx, id)
	if err != nil {
		return err
	}
	return web.JSON(w, http.StatusCreated, taskBody{task})
}

// updateTask updates a task, or marks it done (done: true) or open again
// (done: false).
func (app *App) updateTask(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := zodIssues(updateTaskSchema, v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	if !mayWrite(r) {
		return web.JSON(w, http.StatusForbidden, errorBody{"Sign in to change tasks."})
	}
	ctx := r.Context()
	id := r.PathValue("id")
	body := v.(map[string]any)
	found, err := db.Get(ctx, "SELECT id FROM tasks WHERE id = ?", id)
	if err != nil {
		return err
	}
	if found == nil {
		return web.JSON(w, http.StatusNotFound, errorBody{"Task not found"})
	}
	problem, err := checkTaskWrite(ctx, body)
	if err != nil {
		return err
	}
	if problem != "" {
		return web.JSON(w, http.StatusBadRequest, errorBody{problem})
	}
	sets := []string{}
	params := []any{}
	if s, sent := body["title"].(string); sent {
		title := taskTitle(s)
		if title == "" {
			return web.JSON(w, http.StatusBadRequest, errorBody{"A task needs a title"})
		}
		sets = append(sets, "title = ?")
		params = append(params, title)
	}
	for _, key := range []string{"company_id", "contact_id", "deal_id", "owed_by"} {
		if v, sent := body[key]; sent {
			sets = append(sets, key+" = ?")
			params = append(params, v)
		}
	}
	// A task moved to another company is the mover's: it no longer follows its
	// call's company. Only a real move counts (SET reads the row as it was), so
	// resending the same company changes nothing.
	if company, sent := body["company_id"]; sent {
		sets = append(sets, "created_by = CASE WHEN company_id IS NOT ? THEN ? ELSE created_by END")
		params = append(params, company, whoActs(r))
	}
	if _, sent := body["due_date"]; sent {
		sets = append(sets, "due_date = ?")
		params = append(params, orNull(textAt(body, "due_date")))
	}
	if done, sent := body["done"].(bool); sent {
		if done {
			sets = append(sets, "done_at = COALESCE(done_at, datetime('now'))")
		} else {
			sets = append(sets, "done_at = NULL")
		}
	}
	if len(sets) == 0 {
		return web.JSON(w, http.StatusBadRequest, errorBody{"No fields to update"})
	}
	if _, err := db.Run(ctx, "UPDATE tasks SET "+strings.Join(sets, ", ")+", updated_at = datetime('now') WHERE id = ?", append(params, id)...); err != nil {
		return err
	}
	task, err := readTask(ctx, id)
	if err != nil {
		return err
	}
	return web.JSON(w, http.StatusOK, taskBody{task})
}

// deleteTask deletes a task (deleting one that isn't there is fine too).
func (app *App) deleteTask(w http.ResponseWriter, r *http.Request) error {
	if !mayWrite(r) {
		return web.JSON(w, http.StatusForbidden, errorBody{"Sign in to change tasks."})
	}
	if _, err := db.Run(r.Context(), "DELETE FROM tasks WHERE id = ?", r.PathValue("id")); err != nil {
		return err
	}
	return web.JSON(w, http.StatusOK, okBody{true})
}

// ── Deal updates ──
//
// What happened on a deal that the CRM can't read on its own: a phone call, a
// WhatsApp or text message, an email from another inbox, a meeting moved or
// held in person. Usually logged by an agent the user told about it. A call,
// message, email or meeting is contact with the deal's company
// (deal_progress.go); a note is not.

var dealUpdateKinds = []string{"call", "message", "email", "meeting", "note"}

var dealUpdateSchema = zObject(
	// call, message, email, meeting: contact with them. note: anything else worth keeping.
	zKey("kind", zEnum(dealUpdateKinds...)),
	// What happened, in a sentence or two: who, and what was said or agreed.
	zKey("summary", zString(1, 2000)),
	// When it happened: ISO 8601, or YYYY-MM-DD. Default now. Never in the
	// future: what will happen goes in next_step.
	zKey("at", zOptional(zString(0, 0))),
	// Open tasks of this deal (GET /api/tasks?deal_id=) that the update finished.
	zKey("done_task_ids", zOptional(zArray(zString(0, 0), 20))),
	// What happens next, by when, and who owes it: added as a task on the deal.
	zKey("next_step", zOptional(zObject(
		zKey("title", zString(1, 300)),
		zKey("due_date", zString(0, 0)), // YYYY-MM-DD
		zKey("owed_by", zOptional(zEnum("us", "them"))),
	))),
	// YYYY-MM-DD, when the expected close moved.
	zKey("close_date", zOptional(zString(0, 0))),
)

// sqlTime is "YYYY-MM-DD HH:MM:SS" (UTC), as SQLite's datetime('now') stores
// it, so the timeline sorts as one: toISOString() with a space for the T, cut
// to 19 characters (so a year past 0–9999, written ±YYYYYY, loses seconds).
func sqlTime(ms float64) string {
	t := time.UnixMilli(int64(ms)).UTC()
	year := fmt.Sprintf("%04d", t.Year())
	if y := t.Year(); y < 0 || y > 9999 {
		sign := "+"
		if y < 0 {
			sign, y = "-", -y
		}
		year = fmt.Sprintf("%s%06d", sign, y)
	}
	return (year + t.Format("-01-02 15:04:05"))[:19]
}

type dealUpdateBody struct {
	Deal db.Row `json:"deal"`
	Task db.Row `json:"task"`
}

type loggedBy struct {
	LoggedBy any `json:"logged_by"`
}

// dealUpdate logs what happened on a deal, what it finished, and its next
// step, in one call for an update the CRM can't read on its own (a phone
// call, a message, an email from another inbox, a meeting moved or held in
// person, or a note). It goes on the deal's timeline at the time it happened.
// done_task_ids marks open tasks the update finished. next_step adds what
// happens next as a task on the deal, with its date and who owes it.
// close_date moves the expected close. A call, message, email or meeting
// counts as contact with the deal's company; a note does not. The stage moves
// with PUT /api/deals/{id}.
func (app *App) dealUpdate(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := zodIssues(dealUpdateSchema, v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	if !mayWrite(r) {
		return web.JSON(w, http.StatusForbidden, errorBody{"Sign in to log updates."})
	}
	ctx := r.Context()
	id := r.PathValue("id")
	body := v.(map[string]any)
	deal, err := db.Get(ctx, `SELECT d.id, d.company_id, d.contact_id, COALESCE(d.company_id, ct.company_id) AS account
       FROM deals d LEFT JOIN contacts ct ON ct.id = d.contact_id WHERE d.id = ?`, id)
	if err != nil {
		return err
	}
	if deal == nil {
		return web.JSON(w, http.StatusNotFound, errorBody{"Deal not found"})
	}
	summary := js.Trim(body["summary"].(string))
	if summary == "" {
		return web.JSON(w, http.StatusBadRequest, errorBody{"Say what happened in summary"})
	}

	tz := TzOffsetOf(textAt(firstQueryValues(r), "tz"))
	now := msOf(app.now())
	const future = "at is in the future: log what happened, and put what will happen in next_step"
	at := now
	if when := textAt(body, "at"); when != "" {
		var t float64
		if IsDay(when) {
			// A day alone is judged against the viewer's today: today is now, an
			// earlier day is its noon (UTC).
			today, err := localDay(now, float64(tz))
			if err != nil {
				return err
			}
			if when > today {
				return web.JSON(w, http.StatusBadRequest, errorBody{future})
			}
			t = now
			if when != today {
				t = js.DateParse(when + "T12:00:00Z")
			}
		} else {
			t = js.DateParse(when) // V8's Date.parse: ISO, and the legacy dates it accepts
			if math.IsNaN(t) {
				return web.JSON(w, http.StatusBadRequest, errorBody{"at must be ISO 8601 or YYYY-MM-DD"})
			}
			if t > now+5*60_000 {
				return web.JSON(w, http.StatusBadRequest, errorBody{future})
			}
		}
		at = t
	}
	next, hasNext := body["next_step"].(map[string]any)
	if hasNext && !IsDay(next["due_date"].(string)) {
		return web.JSON(w, http.StatusBadRequest, errorBody{"next_step.due_date must be a date as YYYY-MM-DD"})
	}
	closeDate := textAt(body, "close_date")
	if closeDate != "" && !IsDay(closeDate) {
		return web.JSON(w, http.StatusBadRequest, errorBody{"close_date must be a date as YYYY-MM-DD"})
	}
	by := whoActs(r)

	meta, err := stringify(loggedBy{by})
	if err != nil {
		return err
	}
	if _, err := db.Run(ctx, "INSERT INTO activities (id, entity_type, entity_id, type, body, meta, created_at) VALUES (?, 'deal', ?, ?, ?, ?, ?)",
		js.UUID(), id, body["kind"], summary, meta, sqlTime(at)); err != nil {
		return err
	}
	// Only the deal's own open tasks, or its company's on no deal, can be closed from it.
	done := []string{}
	seen := map[string]bool{}
	ids, _ := body["done_task_ids"].([]any)
	for _, x := range ids {
		done = appendNew(done, seen, x.(string))
	}
	if len(done) > 0 {
		if _, err := db.Run(ctx, `UPDATE tasks SET done_at = COALESCE(done_at, datetime('now')), updated_at = datetime('now')
        WHERE id IN (`+marks(len(done))+`) AND done_at IS NULL
          AND (deal_id = ? OR (deal_id IS NULL AND company_id = ?))`, append(stringParams(done), id, deal["account"])...); err != nil {
			return err
		}
	}
	var task db.Row
	if hasNext {
		taskID := js.UUID()
		owedBy := any("us")
		if o, sent := next["owed_by"]; sent {
			owedBy = o
		}
		if _, err := db.Run(ctx, "INSERT INTO tasks (id, title, company_id, contact_id, deal_id, owed_by, due_date, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			taskID, taskTitle(next["title"].(string)), deal["account"], deal["contact_id"], id, owedBy, next["due_date"], by); err != nil {
			return err
		}
		if task, err = readTask(ctx, taskID); err != nil {
			return err
		}
	}
	if closeDate != "" {
		if _, err := db.Run(ctx, "UPDATE deals SET close_date = ?, updated_at = datetime('now') WHERE id = ?", closeDate, id); err != nil {
			return err
		}
	}

	fresh, err := db.Get(ctx, dealSelect+" WHERE d.id = ?", id)
	if err != nil {
		return err
	}
	if fresh == nil {
		return errReadUndefined("stage")
	}
	p, err := app.progressOf(ctx, []db.Row{fresh}, app.now(), tz)
	if err != nil {
		return err
	}
	view, err := dealView(ctx, fresh)
	if err != nil {
		return err
	}
	return web.JSON(w, http.StatusCreated, dealUpdateBody{withProgress(view, p, id), task})
}
