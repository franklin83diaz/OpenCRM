package crm

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strings"

	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── Views (a list's named, shared layouts) ─────────────────────────
// Plain routes with no try: a database error is Hono's plain 500. The body
// is read with `c.req.json().catch(() => ({}))`, so one that isn't JSON reads
// as {} — but JSON null is null, and reading a property of it throws (a plain
// 500 too).
//
// Route order: /api/views, /api/views/{id}, /api/views/{id}/fields and
// /api/views/{id}/fields/{key} differ in their segments, so ServeMux's choice
// is Hono's; other methods on them are not routes.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/views", app.listViews)
		rt.Handle("GET /api/views/{id}/fields", app.viewFields)
		rt.Handle("PUT /api/views/{id}/fields/{key}", app.setViewField)
		rt.Handle("POST /api/views", app.createView)
		rt.Handle("PATCH /api/views/{id}", app.updateView)
		rt.Handle("DELETE /api/views/{id}", app.deleteView)
	})
}

var (
	viewFieldKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// A view's sort: a column, or "count:<many side>" (most contacts first).
	viewSortRE = regexp.MustCompile(`^(count:)?[a-z][a-z0-9_]*$`)
)

var defaultViewNames = map[EntityType]string{Contact: "All contacts", Company: "All companies", Deal: "All deals", Partner: "All partners"}

// viewBody is a view as the API returns it (viewJson). The default view
// ("All contacts") is the whole list: it holds no filters or sort, and reads
// that way even if a row from before the lock holds some.
type viewBody struct {
	ID        any             `json:"id"`
	Entity    any             `json:"entity"`
	Name      any             `json:"name"`
	Icon      any             `json:"icon"`
	IsDefault bool            `json:"isDefault"`
	Position  any             `json:"position"`
	Filters   json.RawMessage `json:"filters"`
	Sort      any             `json:"sort"`
	Order     any             `json:"order"`
}

// isOne is `v === 1` for a number read from the database.
func isOne(v any) bool {
	f, ok := v.(float64)
	return ok && f == 1
}

// viewJSON is viewJson: a views row as the API returns it.
func viewJSON(v db.Row) viewBody {
	isDefault := isOne(v["is_default"])
	out := viewBody{ID: v["id"], Entity: v["entity_type"], Name: v["name"], Icon: v["icon"], IsDefault: isDefault,
		Position: v["position"], Filters: json.RawMessage("[]")}
	if isDefault {
		return out
	}
	text := colText(v["filters"])
	if text == "" {
		text = "[]"
	}
	// A bad row reads as no filters.
	if parsed, err := web.ParseJSON(text); err == nil {
		if list, ok := parsed.([]any); ok {
			out.Filters = json.RawMessage(jsStringify(list, readKeyOrder(text)))
		}
	}
	out.Sort = v["sort"]
	if o := v["sort_order"]; o == "asc" || o == "desc" {
		out.Order = o
	}
	return out
}

// viewName is a view name: trimmed, 1–60 characters; "" when it isn't one.
func viewName(raw any) string {
	s, _ := raw.(string)
	name := js.Trim(s)
	if name != "" && u16Len(name) <= 60 {
		return name
	}
	return ""
}

// viewSort is a sent sort when it is one, else null.
func viewSort(v any) any {
	if s, ok := v.(string); ok && viewSortRE.MatchString(s) {
		return s
	}
	return nil
}

// viewOrder is a sent order when it is asc or desc, else null.
func viewOrder(v any) any {
	if v == "asc" || v == "desc" {
		return v
	}
	return nil
}

// viewFilters validates a view's filters by compiling them against the list's
// real columns: the JSON to store, or the error to answer. order is the key
// order the request wrote them in.
func (app *App) viewFilters(ctx context.Context, entity EntityType, raw any, order *keyOrder) (string, string) {
	list, ok := raw.([]any)
	if !ok {
		return "", "filters must be an array"
	}
	text := jsStringify(list, order)
	if u16Len(text) > 20_000 {
		return "", "filters too large"
	}
	table, known := EntityTables[entity]
	if !known {
		table = "undefined" // ENTITY_TABLES[entity] of a row's unknown entity
	}
	cols, err := TableColumns(ctx, table)
	if err == nil {
		_, _, err = BuildFilters(cols, text, "", 0, nil, app.now())
	}
	if err != nil {
		return "", errMessage(err)
	}
	return text, ""
}

// ensureDefaultView is the list's default view, created on first use.
func ensureDefaultView(ctx context.Context, entity EntityType) (db.Row, error) {
	find := func() (db.Row, error) {
		return db.Get(ctx, "SELECT * FROM views WHERE entity_type = ? AND is_default = 1", string(entity))
	}
	found, err := find()
	if err != nil || found != nil {
		return found, err
	}
	// The unique default index turns a racing second insert into a no-op.
	if _, err := db.Run(ctx,
		"INSERT OR IGNORE INTO views (id, entity_type, name, icon, is_default, position) VALUES (?, ?, ?, 'table', 1, 0)",
		js.UUID(), string(entity), defaultViewNames[entity]); err != nil {
		return nil, err
	}
	return find()
}

// lenientBody is `await c.req.json().catch(() => ({}))`: a body that isn't
// JSON is {}; JSON null stays null.
func lenientBody(r *http.Request) any {
	v, err := web.JSONBody(r)
	if err != nil {
		return map[string]any{}
	}
	return v
}

// bodyProp is `body.<key> !== undefined` and its value for a body that isn't
// null: an object's own key; an array has Array.prototype.sort (a function,
// so never a string), and a string or number none of the keys read here.
func bodyProp(body any, key string) (any, bool) {
	switch b := body.(type) {
	case map[string]any:
		v, ok := b[key]
		return v, ok
	case []any:
		if key == "sort" {
			return arrayPrototypeSort{}, true
		}
	}
	return nil, false
}

// arrayPrototypeSort stands for the function `[].sort` reads.
type arrayPrototypeSort struct{}

type viewsBody struct {
	Views []viewBody `json:"views"`
}

type oneViewBody struct {
	View viewBody `json:"view"`
}

// listViews lists a list's views (?entity=), its default view first.
func (app *App) listViews(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	entity := textAt(firstQueryValues(r), "entity")
	if !IsEntityType(entity) {
		return web.JSON(w, http.StatusBadRequest, errorBody{"Invalid entity"})
	}
	if _, err := ensureDefaultView(ctx, EntityType(entity)); err != nil {
		return err
	}
	rows, err := db.Query(ctx, "SELECT * FROM views WHERE entity_type = ? ORDER BY is_default DESC, position, created_at", entity)
	if err != nil {
		return err
	}
	views := make([]viewBody, len(rows))
	for i, v := range rows {
		views[i] = viewJSON(v)
	}
	return web.JSON(w, http.StatusOK, viewsBody{views})
}

type viewField struct {
	Key       any  `json:"key"`
	Visible   bool `json:"visible"`
	Size      any  `json:"size"`
	Aggregate any  `json:"aggregate"`
}

type viewFieldsBody struct {
	Fields []viewField `json:"fields"`
}

type oneViewFieldBody struct {
	Field viewField `json:"field"`
}

// viewExists is whether a view with this id exists.
func viewExists(ctx context.Context, id string) (bool, error) {
	found, err := db.Get(ctx, "SELECT id FROM views WHERE id = ?", id)
	return found != nil, err
}

// viewFields is a view's column layout: the columns someone has changed.
func (app *App) viewFields(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	if ok, err := viewExists(ctx, id); err != nil || !ok {
		if err != nil {
			return err
		}
		return web.JSON(w, http.StatusNotFound, errorBody{"View not found"})
	}
	rows, err := db.Query(ctx, "SELECT field_key, is_visible, size, aggregate FROM view_fields WHERE view_id = ?", id)
	if err != nil {
		return err
	}
	fields := make([]viewField, len(rows))
	for i, f := range rows {
		fields[i] = viewField{Key: f["field_key"], Visible: isOne(f["is_visible"]), Size: f["size"], Aggregate: f["aggregate"]}
	}
	return web.JSON(w, http.StatusOK, viewFieldsBody{fields})
}

// setViewField sets one column's visibility, width and/or footer aggregate
// in a view, keeping whatever isn't sent. `aggregate: null` clears the
// calculation.
func (app *App) setViewField(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id, key := r.PathValue("id"), r.PathValue("key")
	if ok, err := viewExists(ctx, id); err != nil || !ok {
		if err != nil {
			return err
		}
		return web.JSON(w, http.StatusNotFound, errorBody{"View not found"})
	}
	if !viewFieldKey.MatchString(key) {
		return web.JSON(w, http.StatusBadRequest, errorBody{"Invalid key"})
	}
	body := lenientBody(r)
	if body == nil {
		return readNull("visible")
	}
	visible, sentVisible := bodyProp(body, "visible")
	if _, isBool := visible.(bool); sentVisible && !isBool {
		return web.JSON(w, http.StatusBadRequest, errorBody{"visible must be a boolean"})
	}
	size, sentSize := bodyProp(body, "size")
	if n, isNumber := size.(float64); sentSize && (!isNumber || math.IsInf(n, 0) || n != math.Trunc(n) || n < 40 || n > 2000) {
		return web.JSON(w, http.StatusBadRequest, errorBody{"size must be an integer between 40 and 2000"})
	}
	aggregate, sentAggregate := bodyProp(body, "aggregate")
	if s, isText := aggregate.(string); sentAggregate && aggregate != nil && !(isText && AllAggregates[s]) {
		return web.JSON(w, http.StatusBadRequest, errorBody{"Unknown aggregate"})
	}
	prev, err := db.Get(ctx, "SELECT is_visible, size, aggregate FROM view_fields WHERE view_id = ? AND field_key = ?", id, key)
	if err != nil {
		return err
	}
	if !sentVisible { // body.visible ?? (prev ? prev.is_visible === 1 : true)
		visible = prev == nil || isOne(prev["is_visible"])
	}
	if !sentSize { // body.size ?? prev?.size ?? null
		size = nil
		if prev != nil {
			size = prev["size"]
		}
	}
	if !sentAggregate { // prev?.aggregate ?? null
		aggregate = nil
		if prev != nil {
			aggregate = prev["aggregate"]
		}
	}
	isVisible := visible.(bool)
	if _, err := db.Run(ctx,
		`INSERT INTO view_fields (view_id, field_key, is_visible, size, aggregate, updated_at) VALUES (?, ?, ?, ?, ?, datetime('now'))
     ON CONFLICT(view_id, field_key) DO UPDATE SET is_visible = excluded.is_visible, size = excluded.size,
       aggregate = excluded.aggregate, updated_at = excluded.updated_at`,
		id, key, flag(isVisible), size, aggregate); err != nil {
		return err
	}
	return web.JSON(w, http.StatusOK, oneViewFieldBody{viewField{Key: key, Visible: isVisible, Size: size, Aggregate: aggregate}})
}

// createView creates a view from the list's current state: its filters and
// sort, and the columns of the view it was made from (`from`).
func (app *App) createView(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	body := lenientBody(r)
	if body == nil {
		return readNull("entity")
	}
	get := func(key string) any {
		v, _ := bodyProp(body, key)
		return v
	}
	entity, _ := get("entity").(string)
	if !IsEntityType(entity) {
		return web.JSON(w, http.StatusBadRequest, errorBody{"Invalid entity"})
	}
	name := viewName(get("name"))
	if name == "" {
		return web.JSON(w, http.StatusBadRequest, errorBody{"A view needs a name of up to 60 characters"})
	}
	filters := get("filters")
	if filters == nil { // body.filters ?? []
		filters = []any{}
	}
	text, bad := app.viewFilters(ctx, EntityType(entity), filters, bodyOrder(r).field("filters"))
	if bad != "" {
		return web.JSON(w, http.StatusBadRequest, errorBody{bad})
	}
	sort, order := viewSort(get("sort")), viewOrder(get("order"))
	if _, err := ensureDefaultView(ctx, EntityType(entity)); err != nil {
		return err
	}
	last, err := db.Get(ctx, "SELECT MAX(position) as p FROM views WHERE entity_type = ?", entity)
	if err != nil {
		return err
	}
	position := 1.0 // (last?.p ?? 0) + 1
	if last != nil && last["p"] != nil {
		position = js.Number(last["p"]) + 1
	}
	id := js.UUID()
	if _, err := db.Run(ctx,
		"INSERT INTO views (id, entity_type, name, icon, is_default, position, filters, sort, sort_order) VALUES (?, ?, ?, 'table', 0, ?, ?, ?, ?)",
		id, entity, name, position, text, sort, order); err != nil {
		return err
	}
	if from, ok := get("from").(string); ok {
		if _, err := db.Run(ctx,
			`INSERT INTO view_fields (view_id, field_key, is_visible, size, aggregate)
       SELECT ?, field_key, is_visible, size, aggregate FROM view_fields
       WHERE view_id = (SELECT id FROM views WHERE id = ? AND entity_type = ?)`,
			id, from, entity); err != nil {
			return err
		}
	}
	row, err := db.Get(ctx, "SELECT * FROM views WHERE id = ?", id)
	if err != nil {
		return err
	}
	if row == nil {
		return errReadUndefined("is_default") // viewJson(row!) of a row gone already
	}
	return web.JSON(w, http.StatusCreated, oneViewBody{viewJSON(row)})
}

// updateView renames a view, or updates its filters and sort (the "Update
// view" button).
func (app *App) updateView(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	prev, err := db.Get(ctx, "SELECT * FROM views WHERE id = ?", id)
	if err != nil {
		return err
	}
	if prev == nil {
		return web.JSON(w, http.StatusNotFound, errorBody{"View not found"})
	}
	body := lenientBody(r)
	if body == nil {
		if isOne(prev["is_default"]) {
			return readNull("filters")
		}
		return readNull("name")
	}
	name, sentName := bodyProp(body, "name")
	filters, sentFilters := bodyProp(body, "filters")
	sort, sentSort := bodyProp(body, "sort")
	order, sentOrder := bodyProp(body, "order")
	if isOne(prev["is_default"]) && (sentFilters || sentSort || sentOrder) {
		return web.JSON(w, http.StatusBadRequest, errorBody{`"` + js.String(prev["name"]) + `" is the whole list and is locked: save the filters and sort as a new view instead (POST /api/views)`})
	}
	var sets []string
	var params []any
	if sentName {
		n := viewName(name)
		if n == "" {
			return web.JSON(w, http.StatusBadRequest, errorBody{"A view needs a name of up to 60 characters"})
		}
		sets = append(sets, "name = ?")
		params = append(params, n)
	}
	if sentFilters {
		text, bad := app.viewFilters(ctx, EntityType(colText(prev["entity_type"])), filters, bodyOrder(r).field("filters"))
		if bad != "" {
			return web.JSON(w, http.StatusBadRequest, errorBody{bad})
		}
		sets = append(sets, "filters = ?")
		params = append(params, text)
	}
	if sentSort {
		sets = append(sets, "sort = ?")
		params = append(params, viewSort(sort))
	}
	if sentOrder {
		sets = append(sets, "sort_order = ?")
		params = append(params, viewOrder(order))
	}
	if len(sets) > 0 {
		if _, err := db.Run(ctx, "UPDATE views SET "+strings.Join(sets, ", ")+", updated_at = datetime('now') WHERE id = ?", append(params, id)...); err != nil {
			return err
		}
	}
	row, err := db.Get(ctx, "SELECT * FROM views WHERE id = ?", id)
	if err != nil {
		return err
	}
	if row == nil {
		return errReadUndefined("is_default")
	}
	return web.JSON(w, http.StatusOK, oneViewBody{viewJSON(row)})
}

// deleteView deletes a view for everyone. The list's default view stays.
func (app *App) deleteView(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	prev, err := db.Get(ctx, "SELECT * FROM views WHERE id = ?", id)
	if err != nil {
		return err
	}
	if prev == nil {
		return web.JSON(w, http.StatusNotFound, errorBody{"View not found"})
	}
	if isOne(prev["is_default"]) {
		return web.JSON(w, http.StatusBadRequest, errorBody{"The default view can't be deleted"})
	}
	if _, err := db.Run(ctx, "DELETE FROM view_fields WHERE view_id = ?", id); err != nil {
		return err
	}
	if _, err := db.Run(ctx, "DELETE FROM views WHERE id = ?", id); err != nil {
		return err
	}
	return web.JSON(w, http.StatusOK, okBody{true})
}
