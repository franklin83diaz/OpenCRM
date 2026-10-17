package crm

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── Stages (the pipeline vocabulary — data, not code) ──────────────
// `key` is immutable and stored on deals.stage. Behavior hangs on the
// semantic flags, never on names: is_won → celebrate (routes_deals.go's
// celebrateWin), is_lost → excluded from pipeline value.
//
// Route order: nothing else matches /api/stages or /api/stages/{key}, so
// ServeMux's choice is Hono's (GET /api/stages/{key} is not a route).

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/stages", app.listStages)
		rt.Handle("POST /api/stages", app.createStage)
		rt.Handle("PUT /api/stages/{key}", app.updateStage)
		rt.Handle("DELETE /api/stages/{key}", app.deleteStage)
	})
}

var stageColors = []string{"sky", "emerald", "amber", "rose", "violet", "fuchsia", "teal", "orange", "slate"}

var (
	stageKeyRE   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	notKeyChars  = regexp.MustCompile(`[^a-z0-9]+`)
	edgeUnderbar = regexp.MustCompile(`^_+|_+$`)
)

// listStagesRows is the pipeline in order, the default stages seeded first.
func (app *App) listStagesRows(ctx context.Context) ([]db.Row, error) {
	if err := app.ensureStagesSeeded(ctx); err != nil {
		return nil, err
	}
	return db.Query(ctx, "SELECT * FROM stages ORDER BY position, key")
}

// getStageRow looks up one stage (seeding the defaults first if the table is
// empty); nil when there is none.
func (app *App) getStageRow(ctx context.Context, key string) (db.Row, error) {
	if err := app.ensureStagesSeeded(ctx); err != nil {
		return nil, err
	}
	return db.Get(ctx, "SELECT * FROM stages WHERE key = ?", key)
}

// progressOf is each open deal's progress (deal_progress.go), with the
// default stages in place first: on a new database they are what says which
// deals are closed.
func (app *App) progressOf(ctx context.Context, deals []db.Row, now time.Time, tz int) (*DealsProgressResult, error) {
	if err := app.ensureStagesSeeded(ctx); err != nil {
		return nil, err
	}
	in := make([]ProgressDeal, len(deals))
	for i, d := range deals {
		in[i] = ProgressDealOf(d)
	}
	return DealsProgress(ctx, in, now, float64(tz))
}

// unknownStageError is the 400 body for an unknown stage key on a deal write.
func (app *App) unknownStageError(ctx context.Context, key string) (errorBody, error) {
	rows, err := app.listStagesRows(ctx)
	if err != nil {
		return errorBody{}, err
	}
	keys := make([]string, len(rows))
	for i, s := range rows {
		keys[i] = js.String(s["key"])
	}
	return errorBody{`Unknown stage "` + key + `". Valid stages: ` + strings.Join(keys, ", ") + ". Create it first via POST /api/stages."}, nil
}

type stagesBody struct {
	Stages []db.Row `json:"stages"`
}

type stageBody struct {
	Stage db.Row `json:"stage"`
}

// listStages lists the pipeline stages in order.
func (app *App) listStages(w http.ResponseWriter, r *http.Request) error {
	return tryJSON(w, func() (int, any, error) {
		rows, err := app.listStagesRows(r.Context())
		return 200, stagesBody{rows}, err
	})
}

var createStageSchema = zObject(
	zKey("label", zString(1, 0)),
	// Immutable identifier; derived from the label when omitted.
	zKey("key", zOptional(zString(0, 0))),
	zKey("color", zOptional(zString(0, 0))),
	// Defaults to the end of the pipeline.
	zKey("position", zOptional(zInt())),
	zKey("is_won", zOptional(zBool())),
	zKey("is_lost", zOptional(zBool())),
)

// jsLower is toLowerCase for deriving a key: Go's simple case mapping, but
// İ (U+0130) lowers to i and a combining dot, its full mapping in JS, which
// the key keeps as an underscore. No other letter lowers to ASCII differently.
func jsLower(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "\u0130", "i\u0307"))
}

// createStage creates a pipeline stage.
func (app *App) createStage(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := zodIssues(createStageSchema, v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		label := js.Trim(body["label"].(string))
		if label == "" {
			return fail(400, "Label is required")
		}
		key := textAt(body, "key")
		if key == "" {
			key = edgeUnderbar.ReplaceAllString(notKeyChars.ReplaceAllString(jsLower(label), "_"), "")
		}
		key = js.Trim(key)
		if !stageKeyRE.MatchString(key) {
			return fail(400, `Invalid stage key "`+key+`" — use lowercase letters, digits, and underscores (must start with a letter).`)
		}
		isWon, isLost := body["is_won"] == true, body["is_lost"] == true
		if isWon && isLost {
			return fail(400, "A stage cannot be both won and lost")
		}
		exists, err := app.getStageRow(ctx, key)
		if err != nil {
			return 0, nil, err
		}
		if exists != nil {
			return fail(409, `Stage "`+key+`" already exists`)
		}

		color := "slate"
		if c := js.Trim(textAt(body, "color")); indexOf(stageColors, c) >= 0 {
			color = c
		}
		position, given := body["position"]
		if !given {
			max, err := db.Get(ctx, "SELECT COALESCE(MAX(position), -1) as m FROM stages")
			if err != nil {
				return 0, nil, err
			}
			m := -1.0 // max?.m ?? -1
			if max != nil && max["m"] != nil {
				m = js.Number(max["m"])
			}
			position = m + 1
		}
		if _, err := db.Run(ctx, "INSERT INTO stages (key, label, color, position, is_won, is_lost) VALUES (?, ?, ?, ?, ?, ?)",
			key, label, color, position, flag(isWon), flag(isLost)); err != nil {
			return 0, nil, err
		}
		inserted, err := db.Get(ctx, "SELECT * FROM stages WHERE key = ?", key)
		return 201, stageBody{inserted}, err
	})
}

var updateStageSchema = zObject(
	zKey("label", zOptional(zString(0, 0))),
	zKey("color", zOptional(zString(0, 0))),
	zKey("position", zOptional(zInt())),
	zKey("is_won", zOptional(zBool())),
	zKey("is_lost", zOptional(zBool())),
)

// updateStage updates a stage's label, color, position, or semantic flags
// (the key is immutable).
func (app *App) updateStage(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := zodIssues(updateStageSchema, v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	key := r.PathValue("key")
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		existing, err := app.getStageRow(ctx, key)
		if err != nil {
			return 0, nil, err
		}
		if existing == nil {
			return fail(404, "Stage not found")
		}

		isWon, isLost := js.Number(existing["is_won"]) == 1, js.Number(existing["is_lost"]) == 1
		if b, sent := body["is_won"].(bool); sent {
			isWon = b
		}
		if b, sent := body["is_lost"].(bool); sent {
			isLost = b
		}
		if isWon && isLost {
			return fail(400, "A stage cannot be both won and lost")
		}

		fields := []string{}
		params := []any{}
		if s, sent := body["label"].(string); sent {
			label := js.Trim(s)
			if label == "" {
				return fail(400, "Label cannot be empty")
			}
			fields = append(fields, "label = ?")
			params = append(params, label)
		}
		if s, sent := body["color"].(string); sent {
			if indexOf(stageColors, js.Trim(s)) < 0 {
				return fail(400, `Unknown color "`+s+`". Valid: `+strings.Join(stageColors, ", "))
			}
			fields = append(fields, "color = ?")
			params = append(params, js.Trim(s))
		}
		if p, sent := body["position"]; sent {
			fields = append(fields, "position = ?")
			params = append(params, p)
		}
		if b, sent := body["is_won"].(bool); sent {
			fields = append(fields, "is_won = ?")
			params = append(params, flag(b))
		}
		if b, sent := body["is_lost"].(bool); sent {
			fields = append(fields, "is_lost = ?")
			params = append(params, flag(b))
		}
		if len(fields) == 0 {
			return fail(400, "No fields to update")
		}

		fields = append(fields, "updated_at = datetime('now')")
		params = append(params, key)
		if _, err := db.Run(ctx, "UPDATE stages SET "+strings.Join(fields, ", ")+" WHERE key = ?", params...); err != nil {
			return 0, nil, err
		}
		updated, err := db.Get(ctx, "SELECT * FROM stages WHERE key = ?", key)
		return 200, stageBody{updated}, err
	})
}

type stageDeletedBody struct {
	OK         bool    `json:"ok"`
	Reassigned float64 `json:"reassigned"`
}

// deleteStage deletes a stage; deals in it must be reassigned via
// ?reassign_to=<stage key> (required when the stage has deals).
func (app *App) deleteStage(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := optionalStrings(q, "reassign_to"); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	key := r.PathValue("key")
	return tryJSON(w, func() (int, any, error) {
		existing, err := app.getStageRow(ctx, key)
		if err != nil {
			return 0, nil, err
		}
		if existing == nil {
			return fail(404, "Stage not found")
		}
		total, err := db.Get(ctx, "SELECT COUNT(*) as count FROM stages")
		if err != nil {
			return 0, nil, err
		}
		if countOf(total) <= 1 {
			return fail(400, "Cannot delete the last stage")
		}

		inStage, err := db.Get(ctx, "SELECT COUNT(*) as count FROM deals WHERE stage = ?", key)
		if err != nil {
			return 0, nil, err
		}
		reassigned := 0.0
		if countOf(inStage) > 0 {
			target := js.Trim(textAt(q, "reassign_to"))
			if target == "" {
				return fail(409, "Stage has "+js.String(inStage["count"])+" deal(s). Pass ?reassign_to=<stage key> to move them first.")
			}
			if target == key {
				return fail(400, "reassign_to must be a different stage")
			}
			targetRow, err := app.getStageRow(ctx, target)
			if err != nil {
				return 0, nil, err
			}
			if targetRow == nil {
				answer, err := app.unknownStageError(ctx, target)
				return 400, answer, err
			}
			res, err := db.Run(ctx, "UPDATE deals SET stage = ?, updated_at = datetime('now') WHERE stage = ?", target, key)
			if err != nil {
				return 0, nil, err
			}
			reassigned = float64(res.Changes)
		}
		if _, err := db.Run(ctx, "DELETE FROM stages WHERE key = ?", key); err != nil {
			return 0, nil, err
		}
		return 200, stageDeletedBody{true, reassigned}, nil
	})
}

// countOf is `row?.count ?? 0`.
func countOf(row db.Row) float64 {
	if row == nil || row["count"] == nil {
		return 0
	}
	return js.Number(row["count"])
}
