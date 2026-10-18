package crm

import (
	"errors"
	"net/http"

	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── Activity timeline ──────────────────────────────────────────────
// Plain Hono handlers (not createRoute) to keep the integration surface
// compact; validation is done inline in the same defensive style as above.
//
// Route order: nothing else matches /api/activities.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/activities", app.listActivities)
		rt.Handle("POST /api/activities", app.addActivity)
	})
}

// activityEntityTypes is ENTITY_TYPES: what a timeline hangs on.
var activityEntityTypes = []string{"contact", "company", "deal", "partner"}

type activitiesBody struct {
	Activities []db.Row `json:"activities"`
}

// listActivities is a record's timeline, newest first.
func (app *App) listActivities(w http.ResponseWriter, r *http.Request) error {
	q := firstQueryValues(r)
	return tryJSON(w, func() (int, any, error) {
		entityType := js.Trim(textAt(q, "entity_type"))
		entityID := js.Trim(textAt(q, "entity_id"))
		if indexOf(activityEntityTypes, entityType) < 0 || entityID == "" {
			return fail(400, "entity_type and entity_id are required")
		}
		activities, err := db.Query(r.Context(),
			"SELECT * FROM activities WHERE entity_type = ? AND entity_id = ? ORDER BY created_at DESC, id DESC",
			entityType, entityID)
		return 200, activitiesBody{activities}, err
	})
}

// addActivity logs a note (or another kind of entry) on a record's timeline.
// The body is read with c.req.json() and no .catch: one that isn't JSON is
// V8's SyntaxError, a null one a TypeError, both 500s with their message.
func (app *App) addActivity(w http.ResponseWriter, r *http.Request) error {
	return tryJSON(w, func() (int, any, error) {
		v, err := web.JSONBody(r)
		if err != nil {
			return 0, nil, err
		}
		if v == nil {
			return 0, nil, readNull("entity_type")
		}
		body := web.Object(v) // an array, string or number has none of these keys
		entityType, err := trimmedField(body, "entity_type")
		if err != nil {
			return 0, nil, err
		}
		entityID, err := trimmedField(body, "entity_id")
		if err != nil {
			return 0, nil, err
		}
		if indexOf(activityEntityTypes, entityType) < 0 || entityID == "" {
			return fail(400, "entity_type and entity_id are required")
		}
		text, err := trimmedField(body, "body")
		if err != nil {
			return 0, nil, err
		}
		if text == "" {
			return fail(400, "Note body is required")
		}
		typ := "note" // (body.type || "note").trim()
		if t := body["type"]; js.Truthy(t) {
			s, ok := t.(string)
			if !ok {
				return 0, nil, errors.New(`(body.type || "note").trim is not a function`)
			}
			typ = s
		}
		LogActivity(r.Context(), entityType, entityID, js.Trim(typ), text, nil)
		return 201, okBody{true}, nil
	})
}
