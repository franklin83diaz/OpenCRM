package crm

import (
	"encoding/json"
	"net/http"

	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── Custom properties (field definitions + schema-sync) ────────────
// Plain routes. The writes answer any error with 400 and its message — a bad
// key, a duplicate, a value D1 can't bind, a body that isn't JSON (V8's
// SyntaxError) — as TS's catch did; the reads and the delete have no catch,
// so a database error there is Hono's plain 500.
//
// Route order: nothing else matches /api/custom-fields or
// /api/custom-fields/{id}.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/custom-fields", app.listCustomFields)
		rt.Handle("POST /api/custom-fields", app.createCustomField)
		rt.Handle("PUT /api/custom-fields/{id}", app.updateCustomField)
		rt.Handle("DELETE /api/custom-fields/{id}", app.deleteCustomField)
	})
}

type defsBody struct {
	Defs []CustomFieldDef `json:"defs"`
}

type defBody struct {
	Def *CustomFieldDef `json:"def"`
}

// try400 runs a route body inside a catch that answers 400 {error: message}.
func try400(w http.ResponseWriter, fn func() (int, any, error)) error {
	status, body, err := fn()
	if err != nil {
		return web.JSON(w, http.StatusBadRequest, errorBody{errMessage(err)})
	}
	return web.JSON(w, status, body)
}

// listCustomFields lists the defs, of one entity (?entity=) or all of them.
func (app *App) listCustomFields(w http.ResponseWriter, r *http.Request) error {
	entity := textAt(firstQueryValues(r), "entity")
	if entity != "" && !IsEntityType(entity) {
		return web.JSON(w, http.StatusBadRequest, errorBody{"Invalid entity"})
	}
	var which *EntityType
	if entity != "" {
		e := EntityType(entity)
		which = &e
	}
	defs, err := ListDefs(r.Context(), which)
	if err != nil {
		return err
	}
	return web.JSON(w, http.StatusOK, defsBody{defs})
}

// optionsJSON is a sent `options` value as JSON.stringify wrote it, with the
// request's key order (CreateDef and UpdateDef store pre-encoded JSON as is);
// nil when it wasn't sent or is null, which takes the default.
func optionsJSON(v any, order *keyOrder) any {
	if v == nil {
		return nil
	}
	return json.RawMessage(jsStringify(v, order))
}

// bodyOrder is the key order of the request's JSON body.
func bodyOrder(r *http.Request) *keyOrder {
	return readKeyOrder(web.RawBody(r))
}

// createCustomField creates a def (and its column), or a relation: a def on
// each side.
func (app *App) createCustomField(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return try400(w, func() (int, any, error) {
		v, err := web.JSONBody(r)
		if err != nil {
			return 0, nil, err
		}
		if v == nil {
			return 0, nil, readNull("entity_type")
		}
		body := web.Object(v)
		entity, _ := body["entity_type"].(string)
		if !IsEntityType(entity) {
			return fail(400, "Invalid entity_type")
		}
		if !js.Truthy(body["key"]) || !js.Truthy(body["label"]) {
			return fail(400, "key and label are required")
		}
		if body["field_type"] == "relation" {
			target, _ := body["target_entity"].(string)
			if !IsEntityType(target) {
				return fail(400, "A relation needs target_entity: contact, company, deal, partner or customer_user")
			}
			if !js.Truthy(body["inverse_key"]) || !js.Truthy(body["inverse_label"]) {
				return fail(400, "A relation needs inverse_key and inverse_label for its other side")
			}
			relationType, _ := body["relation_type"].(string) // anything else fails CreateRelation's check, as in TS
			def, err := CreateRelation(ctx, RelationInput{
				EntityType:   EntityType(entity),
				Key:          js.String(body["key"]),
				Label:        js.String(body["label"]),
				RelationType: relationType,
				TargetEntity: EntityType(target),
				InverseKey:   js.String(body["inverse_key"]),
				InverseLabel: js.String(body["inverse_label"]),
				Position:     body["position"],
			})
			if err != nil {
				return 0, nil, err
			}
			return 201, defBody{def}, nil
		}
		def, err := CreateDef(ctx, CustomFieldInput{
			EntityType:  EntityType(entity),
			Key:         js.String(body["key"]),
			Label:       js.String(body["label"]),
			FieldType:   body["field_type"],
			CustomField: body["custom_field"],
			Options:     optionsJSON(body["options"], bodyOrder(r).field("options")),
			Position:    body["position"],
		})
		if err != nil {
			return 0, nil, err
		}
		return 201, defBody{def}, nil
	})
}

// updateCustomField updates a def's label, options, position or widget. The
// body is read before anything else: one that isn't JSON is a 400 even for an
// unknown id. A null body passes the 404 check and then fails reading it.
func (app *App) updateCustomField(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	return try400(w, func() (int, any, error) {
		v, err := web.JSONBody(r)
		if err != nil {
			return 0, nil, err
		}
		if v == nil {
			def, err := GetDef(ctx, id)
			if err != nil {
				return 0, nil, err
			}
			if def == nil {
				return fail(404, "Not found")
			}
			return 0, nil, readNull("label")
		}
		patch := web.Object(v) // an array, string or number has none of the keys read
		if options, sent := patch["options"]; sent {
			patch["options"] = optionsJSON(options, bodyOrder(r).field("options"))
		}
		def, err := UpdateDef(ctx, id, patch)
		if err != nil {
			return 0, nil, err
		}
		if def == nil {
			return fail(404, "Not found")
		}
		return 200, defBody{def}, nil
	})
}

// deleteCustomField deletes a def and drops its column (a relation: both
// sides).
func (app *App) deleteCustomField(w http.ResponseWriter, r *http.Request) error {
	ok, err := DeleteDef(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if !ok {
		return web.JSON(w, http.StatusNotFound, errorBody{"Not found"})
	}
	return web.JSON(w, http.StatusOK, okBody{true})
}
