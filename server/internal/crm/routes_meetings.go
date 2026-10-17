package crm

import (
	"context"
	"math"
	"net/http"
	"time"

	"opencrm/internal/clawnify"
	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── Meetings, tasks, insights, customers ───────────────────────────
//
// The calendar and Granola read into the CRM (meetings.go), what calls produce
// (tasks and insights), and how each customer is doing (customers.go). Turning
// the sync on and what it reads are a signed-in person's decisions, as for
// email; people, agents and other apps read everything and work the tasks.
//
// Route order: TS registered GET/PUT /api/meetings/sync, POST /api/meetings/run
// and POST /api/meetings/sync-now before /api/meetings/{id}, so those literal
// paths reach their own handlers, and any other method on them (PATCH
// /api/meetings/sync, GET /api/meetings/run) falls to {id}. ServeMux prefers
// the more specific literal pattern whatever the order, which is the same.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/meetings/sync", app.meetingSyncState)
		rt.Handle("PUT /api/meetings/sync", app.meetingSyncSave)
		rt.Handle("POST /api/meetings/run", app.meetingsRun)
		rt.Handle("POST /api/meetings/sync-now", app.meetingsSyncNow)
		rt.Handle("GET /api/meetings", app.listMeetings)
		rt.Handle("GET /api/meetings/{id}", app.getMeeting)
		rt.Handle("PATCH /api/meetings/{id}", app.linkMeeting)
		rt.Handle("POST /api/meetings/{id}/digest", app.retryDigest)
	})
}

// mayWrite is whether a caller may change meetings, tasks and insights:
// people, API keys and agents.
func mayWrite(r *http.Request) bool { return mayRun(web.Caller(r)) }

// meetingSyncView is the settings as the API shows them; nil when never saved.
type meetingSyncView struct {
	Enabled          bool    `json:"enabled"`
	HistoryDays      float64 `json:"history_days"`
	About            string  `json:"about"`
	CalendarOwner    *string `json:"calendar_owner"`
	Phase            string  `json:"phase"`
	CalendarSyncedAt *string `json:"calendar_synced_at"`
	LastRunAt        *string `json:"last_run_at"`
	LastError        *string `json:"last_error"`
	NextRunAt        *string `json:"next_run_at"`
	UpdatedBy        *string `json:"updated_by"`
	UpdatedAt        *string `json:"updated_at"`
}

func syncView(s *MeetingSync) *meetingSyncView {
	if s == nil {
		return nil
	}
	return &meetingSyncView{
		Enabled:          s.Enabled != 0,
		HistoryDays:      s.HistoryDays,
		About:            s.About,
		CalendarOwner:    s.CalendarOwner,
		Phase:            s.Phase,
		CalendarSyncedAt: s.CalendarSyncedAt,
		LastRunAt:        s.LastRunAt,
		LastError:        s.LastError,
		NextRunAt:        s.NextRunAt,
		UpdatedBy:        s.UpdatedBy,
		UpdatedAt:        s.UpdatedAt,
	}
}

type meetingSources struct {
	Calendar bool `json:"calendar"`
	Notes    bool `json:"notes"`
}

// meetingSyncState is which sources are connected, the settings, and where
// the sync is. Also the watchdog: sync that is on with no run booked gets one.
func (app *App) meetingSyncState(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		status := ConnectionStatus(ctx, app.Conn)
		s, err := SyncSettings(ctx)
		if err != nil {
			return 0, nil, err
		}
		if s != nil && s.Enabled != 0 {
			EnsureScheduled(ctx, app.Env, web.Origin(r), s, time.Now())
		}
		counts, err := MeetingCounts(ctx)
		if err != nil {
			return 0, nil, err
		}
		return 200, struct {
			Sources        meetingSources   `json:"sources"`
			Settings       *meetingSyncView `json:"settings"`
			Counts         MeetingTotals    `json:"counts"`
			HistoryChoices []float64        `json:"history_choices"`
			CanConfigure   bool             `json:"can_configure"`
		}{meetingSources{status.Meeting, status.Notes}, syncView(s), counts, HistoryDays, mayConfigure(r)}, nil
	})
}

func includesNumber(list []float64, v any) bool {
	n, ok := v.(float64)
	if !ok {
		return false
	}
	for _, x := range list {
		if x == n {
			return true
		}
	}
	return false
}

// meetingSyncSave turns the sync on or off, how far back it reads, and what
// we sell (it frames the ideas and expansion the AI notes). Off stops
// reading; what is in the CRM stays.
func (app *App) meetingSyncSave(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		if !mayConfigure(r) {
			return fail(403, "Only a signed-in person can change meeting sync settings.")
		}
		v, err := web.JSONBody(r)
		if err != nil {
			v = map[string]any{} // .catch(() => ({}))
		} else if v == nil {
			return 0, nil, readNull("enabled")
		}
		body := web.Object(v) // any other non-object reads every field as undefined
		enabled, hasEnabled := body["enabled"]
		if _, isBool := enabled.(bool); hasEnabled && !isBool {
			return fail(400, "enabled must be true or false")
		}
		history, hasHistory := body["history_days"]
		if hasHistory && !includesNumber(HistoryDays, history) {
			return fail(400, "history_days must be one of 30, 90, 365")
		}
		about, hasAbout := body["about"]
		aboutText, isText := about.(string)
		if hasAbout && (!isText || u16Len(aboutText) > 1000) {
			return fail(400, "about must be text under 1,000 characters")
		}
		if enabled == true && !ConnectionStatus(ctx, app.Conn).Meeting {
			return fail(409, "Connect Google Calendar in Clawnify first.")
		}
		u := web.CurrentUser(r)
		who := &u.ID
		if u.Email != nil {
			who = u.Email
		}
		var patch SettingsPatch
		if b, ok := enabled.(bool); ok {
			patch.Enabled = &b
		}
		if n, ok := history.(float64); ok {
			patch.HistoryDays = &n
		}
		if isText {
			patch.About = strp(js.Trim(aboutText))
		}
		before, after, err := SaveSettings(ctx, patch, who)
		if err != nil {
			return 0, nil, err
		}
		if after.Enabled == 0 && before != nil && before.Enabled != 0 {
			if err := CancelScheduled(ctx, app.Env, after); err != nil {
				return 0, nil, err
			}
		}
		if after.Enabled != 0 && (before == nil || before.Enabled == 0 || after.Phase == "importing") {
			ScheduleRun(ctx, app.Env, web.Origin(r), time.Now())
		}
		saved, err := SyncSettings(ctx)
		if err != nil {
			return 0, nil, err
		}
		counts, err := MeetingCounts(ctx)
		if err != nil {
			return 0, nil, err
		}
		return 200, struct {
			Settings *meetingSyncView `json:"settings"`
			Counts   MeetingTotals    `json:"counts"`
		}{syncView(saved), counts}, nil
	})
}

// meetingsReport is a run's result with the settings and counts after it.
type meetingsReport struct {
	MeetingRunResult
	Settings *meetingSyncView `json:"settings"`
	Counts   MeetingTotals    `json:"counts"`
}

// meetingsRunAndBook is one bounded run, then books the next: at once while
// work is left, otherwise in LiveIntervalMs.
func meetingsRunAndBook(ctx context.Context, app *App, origin string, sync bool) (meetingsReport, error) {
	result, err := RunMeetings(ctx, app.Env, app.Conn, time.Now(), sync)
	if err != nil {
		return meetingsReport{}, err
	}
	if result.Status != "busy" && result.Status != "off" {
		var delay time.Duration
		switch {
		case result.Status == "error":
			delay = 10 * time.Minute
		case result.More != nil && *result.More:
			delay = 0
		default:
			delay = LiveIntervalMs * time.Millisecond
		}
		ScheduleRun(ctx, app.Env, origin, time.Now().Add(delay))
	}
	s, err := SyncSettings(ctx)
	if err != nil {
		return meetingsReport{}, err
	}
	counts, err := MeetingCounts(ctx)
	if err != nil {
		return meetingsReport{}, err
	}
	return meetingsReport{MeetingRunResult: result, Settings: syncView(s), Counts: counts}, nil
}

// meetingsRun is the platform queue's target: a signed delivery on a declared
// public route (see emailSyncRun for why people use the route below instead).
func (app *App) meetingsRun(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	signed := clawnify.VerifyDelivery(ctx, web.RawBody(r),
		headerValue(r, "X-Queue-Signature"), headerValue(r, "X-Queue-Timestamp"), headerValue(r, "X-Queue-Key-Id"))
	if !signed && !mayWrite(r) {
		return web.JSON(w, http.StatusForbidden, errorBody{"Sign in to run a sync."})
	}
	return tryJSON(w, func() (int, any, error) {
		out, err := meetingsRunAndBook(ctx, app, web.Origin(r), false)
		return 200, out, err
	})
}

// meetingsSyncNow is "Sync now", for a person or an agent: reads the calendar
// and Granola even when no read is due.
func (app *App) meetingsSyncNow(w http.ResponseWriter, r *http.Request) error {
	if !mayWrite(r) {
		return web.JSON(w, http.StatusForbidden, errorBody{"Sign in to run a sync."})
	}
	return tryJSON(w, func() (int, any, error) {
		out, err := meetingsRunAndBook(r.Context(), app, web.Origin(r), true)
		return 200, out, err
	})
}

// listQueryIssues is the list route's zod query schema: company_id, limit
// and offset optional strings; link and when optional enums.
func listQueryIssues(q map[string]any) []web.Issue {
	var issues []web.Issue
	for _, key := range []string{"company_id", "link", "when", "limit", "offset"} {
		v, present := q[key]
		if !present {
			continue
		}
		var issue *web.Issue
		switch key {
		case "link":
			issue = checkEnum(zodPath(key), v, []string{"unmatched", "ignored"})
		case "when":
			issue = checkEnum(zodPath(key), v, []string{"upcoming", "past"})
		default:
			issue = checkType(zodPath(key), v, "string")
		}
		if issue != nil {
			issues = append(issues, *issue)
		}
	}
	return issues
}

// pageNumber is `q ? parseInt(q, 10) || fallback : fallback`.
func pageNumber(q any, fallback float64) float64 {
	s, _ := q.(string)
	if s == "" {
		return fallback
	}
	n := js.ParseInt(s)
	if n == 0 || math.IsNaN(n) {
		return fallback
	}
	return n
}

// listMeetings lists meetings with people from outside (calendar + Granola), newest first.
func (app *App) listMeetings(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := listQueryIssues(q); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		opts := ListMeetingsOptions{}
		opts.CompanyID, _ = q["company_id"].(string)
		opts.Link, _ = q["link"].(string)
		opts.When, _ = q["when"].(string)
		limit, offset := pageNumber(q["limit"], 25), pageNumber(q["offset"], 0)
		opts.Limit, opts.Offset = &limit, &offset
		list, err := ListMeetings(ctx, opts, time.Now())
		return 200, list, err
	})
}

type meetingBody struct {
	Meeting *MeetingView `json:"meeting"`
}

// getMeeting gets a meeting. Errors answer Hono's plain 500: TS had no try here.
func (app *App) getMeeting(w http.ResponseWriter, r *http.Request) error {
	meeting, err := GetMeeting(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if meeting == nil {
		return web.JSON(w, http.StatusNotFound, errorBody{"Meeting not found"})
	}
	return web.JSON(w, http.StatusOK, meetingBody{meeting})
}

// linkBodyIssues is the link route's zod body schema: an object whose
// company_id is an optional, nullable string and ignored an optional boolean.
func linkBodyIssues(v any) []web.Issue {
	body, isObject := v.(map[string]any)
	if !isObject {
		return []web.Issue{invalidType(zodPath(), "object", v)}
	}
	var issues []web.Issue
	if c, present := body["company_id"]; present && c != nil {
		if issue := checkType(zodPath("company_id"), c, "string"); issue != nil {
			issues = append(issues, *issue)
		}
	}
	if i, present := body["ignored"]; present {
		if issue := checkType(zodPath("ignored"), i, "boolean"); issue != nil {
			issues = append(issues, *issue)
		}
	}
	return issues
}

// linkMeeting links a meeting to a company, unlinks it, or marks it as not an
// account meeting. Linking reads the call (when it has a Granola note) and
// links the other unmatched meetings with people from the same domain. A
// company with no domain takes the domain of the people met. The body is
// validated before the caller is checked, as zod-openapi's validators ran
// before the handler.
func (app *App) linkMeeting(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := linkBodyIssues(v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	if !mayWrite(r) {
		return web.JSON(w, http.StatusForbidden, errorBody{"Sign in to change meetings."})
	}
	ctx := r.Context()
	id := r.PathValue("id")
	body := v.(map[string]any)
	companyID, hasCompany := body["company_id"]
	ignored, hasIgnored := body["ignored"]
	if !hasIgnored && !hasCompany {
		return web.JSON(w, http.StatusBadRequest, errorBody{"Send company_id or ignored"})
	}
	company, _ := companyID.(string) // nil (null) reads as ""
	if company != "" {
		found, err := db.Get(ctx, "SELECT id FROM companies WHERE id = ?", company)
		if err != nil {
			return err
		}
		if found == nil {
			return web.JSON(w, http.StatusBadRequest, errorBody{"Company not found"})
		}
	}
	to := LinkTarget{Ignored: ignored == true}
	if !to.Ignored && companyID != nil {
		to.CompanyID = &company
	}
	meeting, err := LinkMeeting(ctx, id, to)
	if err != nil {
		return err
	}
	if meeting == nil {
		return web.JSON(w, http.StatusNotFound, errorBody{"Meeting not found"})
	}
	if meeting.DigestStatus == "queued" {
		origin, now := web.Origin(r), time.Now()
		web.Background(r, func(ctx context.Context) { ScheduleRun(ctx, app.Env, origin, now) })
	}
	return web.JSON(w, http.StatusOK, meetingBody{meeting})
}

// retryDigest reads a call again after its digest failed.
func (app *App) retryDigest(w http.ResponseWriter, r *http.Request) error {
	if !mayWrite(r) {
		return web.JSON(w, http.StatusForbidden, errorBody{"Sign in to change meetings."})
	}
	queued, err := RetryDigest(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if !queued {
		return web.JSON(w, http.StatusConflict, errorBody{"This meeting has no Granola note, no company, or was already read."})
	}
	origin, now := web.Origin(r), time.Now()
	web.Background(r, func(ctx context.Context) { ScheduleRun(ctx, app.Env, origin, now) })
	return web.JSON(w, http.StatusOK, okBody{true})
}
