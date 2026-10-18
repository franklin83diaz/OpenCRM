package crm

// Custom properties — server side.
//
// A custom property is a user-defined field on an entity type. Each def maps to
// a REAL column on the entity's table (added via ALTER TABLE at definition
// time), so values are native, indexable columns rather than a JSON blob — the
// repo's own convention (every queryable field is a column; JSON only for the
// opaque `activities.meta` bag).
//
// This file owns: the `custom_field_defs` registry CRUD, the schema-sync that
// keeps each entity table's columns in step with its defs, and value coercion
// for the entity write paths. Nothing here is cached: every call reads the
// registry, as TS did.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	"opencrm/internal/db"
	"opencrm/internal/js"
)

// EntityType is "contact", "company", "deal", "partner", "customer_user" or "ticket".
type EntityType string

const (
	Contact EntityType = "contact"
	Company EntityType = "company"
	Deal    EntityType = "deal"
	Partner EntityType = "partner"
	// CustomerUser is a person who uses the product at a customer.
	CustomerUser EntityType = "customer_user"
	// Ticket is a customer's request or problem.
	Ticket EntityType = "ticket"
)

// A relation's side: many_to_one holds the linked id in its own column;
// one_to_many lists the records whose many_to_one points back at this one.
const (
	ManyToOne = "many_to_one"
	OneToMany = "one_to_many"
)

// EntityTables maps an entity type to its real table name.
var EntityTables = map[EntityType]string{
	Contact:      "contacts",
	Company:      "companies",
	Deal:         "deals",
	Partner:      "partners",
	CustomerUser: "customer_users",
	Ticket:       "tickets",
}

// IsEntityType reports whether v names an entity type.
func IsEntityType(v string) bool {
	return v == "contact" || v == "company" || v == "deal" || v == "partner" || v == "customer_user" || v == "ticket"
}

// FieldError is an error TS threw with a message meant for the caller (an
// invalid key, an enum violation, too many filter values): routes answer it
// with a 4xx and its message, where any other error is a 500.
type FieldError struct{ Msg string }

func (e *FieldError) Error() string { return e.Msg }

func fieldError(format string, args ...any) error {
	return &FieldError{fmt.Sprintf(format, args...)}
}

// CustomFieldDef is a custom_field_defs row as the API returns it: every
// column, with options parsed. Base storage types (field_type) are string,
// text, integer, decimal, boolean, date, datetime, enumeration, json and
// relation; widget flavours (score, badge, url…) ride on these via
// custom_field and are purely presentational — the server only sees the base.
type CustomFieldDef struct {
	ID          string     `json:"id"`
	EntityType  EntityType `json:"entity_type"`
	Key         string     `json:"key"`
	Label       string     `json:"label"`
	FieldType   string     `json:"field_type"`
	CustomField string     `json:"custom_field"` // widget registry uid, or "" for a bare base type
	// Options is the parsed JSON: an object (map[string]any) — or, as TS
	// allowed, an array ([]any). Read single options with Option.
	Options      any         `json:"options"`
	Position     float64     `json:"position"`
	RelationType *string     `json:"relation_type"` // ManyToOne, OneToMany or null
	TargetEntity *EntityType `json:"target_entity"`
	InverseDefID *string     `json:"inverse_def_id"`
	CreatedAt    string      `json:"created_at"`
	UpdatedAt    string      `json:"updated_at"`
	// optionsText is the stored options JSON, for the order of its keys:
	// writing the options back keeps it, as JSON.stringify(JSON.parse(…)) did.
	optionsText string
}

// Option is def.options[name]: nil when absent or when options is not an object.
func (d CustomFieldDef) Option(name string) any {
	if m, ok := d.Options.(map[string]any); ok {
		return m[name]
	}
	return nil
}

func (d CustomFieldDef) relationType() string {
	if d.RelationType == nil {
		return ""
	}
	return *d.RelationType
}

func (d CustomFieldDef) targetEntity() EntityType {
	if d.TargetEntity == nil {
		return ""
	}
	return *d.TargetEntity
}

// builtinColumns are the platform columns per table, in schema order — a
// custom key may never collide with these, so schema-sync can never add/drop a
// platform column. A deal's partner_ids is no column (it writes deal_partners)
// but is a base field all the same.
var builtinColumns = map[EntityType][]string{
	Company:      {"id", "name", "domain", "industry", "phone", "email", "notes", "customer_since", "renewal_date", "main_contact_id", "created_at", "updated_at"},
	Contact:      {"id", "first_name", "last_name", "email", "phone", "company_id", "title", "status", "last_contacted_at", "created_at", "updated_at"},
	Deal:         {"id", "name", "contact_id", "company_id", "value", "recurring_value", "stage", "close_date", "notes", "owner_id", "created_at", "updated_at", "partner_ids"},
	Partner:      {"id", "name", "industry", "status", "phone", "email", "notes", "created_at", "updated_at"},
	CustomerUser: {"id", "name", "email", "phone", "company_id", "notes", "created_at", "updated_at"},
	Ticket:       {"id", "subject", "description", "status", "priority", "company_id", "customer_user_id", "owner_id", "due_date", "created_at", "updated_at"},
}

func isBuiltin(entity EntityType, key string) bool {
	for _, c := range builtinColumns[entity] {
		if c == key {
			return true
		}
	}
	return false
}

// reservedKeys are keys a record answers with that are no field of its own —
// a deal's partners and owner, a partner's deals and deal_count, a customer
// user's company name and domain, a company's customer users and main contact, the tickets of
// a company or customer user, a ticket's names of its company, customer user
// and owner — so no custom field may take them either.
var reservedKeys = map[EntityType][]string{
	Company:      {"customer_users", "tickets", "main_contact"},
	CustomerUser: {"company_name", "company_domain", "tickets"},
	Deal:         {"partners", "owner"},
	Partner:      {"deals", "deal_count"},
	Ticket:       {"company_name", "company_domain", "customer_user_name", "customer_user_email", "owner"},
}

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// AssertValidKey returns a *FieldError if key is not a safe, non-reserved column.
func AssertValidKey(entity EntityType, key string) error {
	if !keyPattern.MatchString(key) {
		return fieldError(`Invalid field key "%s" — use lowercase letters, digits, and underscores (must start with a letter).`, key)
	}
	if isBuiltin(entity, key) || indexOf(reservedKeys[entity], key) >= 0 {
		return fieldError(`"%s" is a built-in %s field and can't be used as a custom property.`, key, entity)
	}
	return nil
}

// ClassifyCustomWrite splits a flat bag of candidate values into writable
// custom values (keys that have a registered def) and unknown keys (keys that
// are neither a built-in column nor a defined custom field). Built-in keys are
// handled by the caller's base write path, so they are neither returned nor
// flagged.
//
// This is what makes writes agent-safe: a mistyped or unregistered field
// surfaces as `unknown` (the caller rejects it loudly) instead of being
// silently dropped — the failure mode that bit both operators and agents when
// custom values could only be reached through a nested `custom` bag.
//
// A Go map has no order, so unknown keys come in the order JS would list the
// sorted keys; ResolveCustomWrite keeps the request body's order instead.
func ClassifyCustomWrite(ctx context.Context, entity EntityType, candidates map[string]any) (values map[string]any, unknown []string, err error) {
	return classifyCustomWrite(ctx, entity, jsKeyOrder(sortedKeys(candidates)), candidates)
}

// classifyCustomWrite visits the candidates in `order` (Object.entries order).
func classifyCustomWrite(ctx context.Context, entity EntityType, order []string, candidates map[string]any) (map[string]any, []string, error) {
	defs, err := ListDefs(ctx, &entity)
	if err != nil {
		return nil, nil, err
	}
	known := map[string]bool{}
	for _, d := range defs {
		known[d.Key] = true
	}
	values := map[string]any{}
	unknown := []string{}
	for _, k := range order {
		if known[k] {
			values[k] = candidates[k]
		} else if !isBuiltin(entity, k) {
			unknown = append(unknown, k)
		}
	}
	return values, unknown, nil
}

// MissingRequiredCustom returns the labels of required custom fields
// (`options.required === true`) that a write violates. Create: the value must
// be present and non-empty. Update: only an explicit unset (null / "")
// violates — absent keys keep their stored value. Enforced on the
// create/update APIs only; bulk import stays lenient by design (it coerces bad
// cells to null rather than aborting a 2000-row file). mode is "create" or
// "update".
func MissingRequiredCustom(ctx context.Context, entity EntityType, values map[string]any, mode string) ([]string, error) {
	defs, err := ListDefs(ctx, &entity)
	if err != nil {
		return nil, err
	}
	missing := []string{}
	for _, def := range defs {
		if def.Option("required") != true {
			continue
		}
		v, present := values[def.Key]
		empty := v == nil || v == ""
		if (mode == "create" && empty) || (mode != "create" && present && empty) {
			if def.Label != "" {
				missing = append(missing, def.Label)
			} else {
				missing = append(missing, def.Key)
			}
		}
	}
	return missing, nil
}

// WritableFieldKeys is the human-facing list of writable field keys (built-ins
// + registered custom), for the error body when a write includes an unknown
// field. Omits the server-managed columns a caller can't set, and the
// one_to_many side of a relation (it is set from the other side).
func WritableFieldKeys(ctx context.Context, entity EntityType) ([]string, error) {
	keys := []string{}
	for _, k := range builtinColumns[entity] {
		if k != "id" && k != "created_at" && k != "updated_at" {
			keys = append(keys, k)
		}
	}
	defs, err := ListDefs(ctx, &entity)
	if err != nil {
		return nil, err
	}
	for _, d := range defs {
		if HasColumn(d) {
			keys = append(keys, d.Key)
		}
	}
	return keys, nil
}

// HasColumn reports whether a def has a column of its own: all do but a
// relation's one_to_many side.
func HasColumn(def CustomFieldDef) bool {
	return def.relationType() != OneToMany
}

func indexName(table, key string) string { return QuoteIdent("idx_" + table + "_" + key) }

// ── Registry CRUD ─────────────────────────────────────────────────────

func rowToDef(row db.Row) CustomFieldDef {
	return CustomFieldDef{
		ID:           colText(row["id"]),
		EntityType:   EntityType(colText(row["entity_type"])),
		Key:          colText(row["key"]),
		Label:        colText(row["label"]),
		FieldType:    colText(row["field_type"]),
		CustomField:  colText(row["custom_field"]),
		Options:      parseOptions(row["options"]),
		optionsText:  colText(row["options"]),
		Position:     js.Number(row["position"]),
		RelationType: nullableText(row["relation_type"]),
		TargetEntity: (*EntityType)(nullableText(row["target_entity"])),
		InverseDefID: nullableText(row["inverse_def_id"]),
		CreatedAt:    colText(row["created_at"]),
		UpdatedAt:    colText(row["updated_at"]),
	}
}

// colText reads a TEXT column: "" for NULL.
func colText(v any) string {
	if v == nil {
		return ""
	}
	return js.String(v)
}

func nullableText(v any) *string {
	if v == nil {
		return nil
	}
	s := js.String(v)
	return &s
}

// parseOptions is JSON.parse(raw || "{}") kept only when it is an object or
// an array; anything else, or a parse failure, is {}.
func parseOptions(raw any) any {
	s, _ := raw.(string)
	if s == "" {
		s = "{}"
	}
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		switch v.(type) {
		case map[string]any, []any:
			return v
		}
	}
	return map[string]any{}
}

// ListDefs returns the defs of one entity type, or of all when entity is nil,
// in display order.
func ListDefs(ctx context.Context, entity *EntityType) ([]CustomFieldDef, error) {
	var rows []db.Row
	var err error
	if entity != nil && *entity != "" {
		rows, err = db.Query(ctx, "SELECT * FROM custom_field_defs WHERE entity_type = ? ORDER BY position, created_at", string(*entity))
	} else {
		rows, err = db.Query(ctx, "SELECT * FROM custom_field_defs ORDER BY entity_type, position, created_at")
	}
	if err != nil {
		return nil, err
	}
	defs := make([]CustomFieldDef, len(rows))
	for i, r := range rows {
		defs[i] = rowToDef(r)
	}
	return defs, nil
}

// GetDef returns the def with this id, or nil.
func GetDef(ctx context.Context, id string) (*CustomFieldDef, error) {
	row, err := db.Get(ctx, "SELECT * FROM custom_field_defs WHERE id = ?", id)
	if err != nil || row == nil {
		return nil, err
	}
	def := rowToDef(row)
	return &def, nil
}

// CustomFieldInput is what CreateDef takes. The fields typed any are bound as
// the request sent them, as TS did (`body.options ?? {}`, `body.position ?? 0`);
// nil takes the default.
type CustomFieldInput struct {
	EntityType  EntityType
	Key         string
	Label       string
	FieldType   any // the base type; nil is "string"
	CustomField any // widget uid; nil is ""
	Options     any // JSON-encoded as given (json.RawMessage as is); nil is {}
	Position    any // nil is 0
}

func orDefault(v, def any) any {
	if v == nil {
		return def
	}
	return v
}

// CreateDef creates a def and adds its column. A bad or duplicate key is a
// *FieldError.
func CreateDef(ctx context.Context, input CustomFieldInput) (*CustomFieldDef, error) {
	if err := AssertValidKey(input.EntityType, input.Key); err != nil {
		return nil, err
	}
	existing, err := db.Get(ctx, "SELECT id FROM custom_field_defs WHERE entity_type = ? AND key = ?", string(input.EntityType), input.Key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fieldError(`A "%s" property already exists on %s.`, input.Key, input.EntityType)
	}
	options, err := stringify(orDefault(input.Options, map[string]any{}))
	if err != nil {
		return nil, err
	}
	id := js.UUID()
	if _, err := db.Run(ctx,
		"INSERT INTO custom_field_defs (id, entity_type, key, label, field_type, custom_field, options, position) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		id, string(input.EntityType), input.Key, input.Label, orDefault(input.FieldType, "string"), orDefault(input.CustomField, ""), options, orDefault(input.Position, 0.0),
	); err != nil {
		return nil, err
	}
	if err := SyncEntityColumns(ctx, input.EntityType); err != nil {
		return nil, err
	}
	return GetDef(ctx, id)
}

// RelationInput is what CreateRelation takes. Position is bound as given
// (nil is 0).
type RelationInput struct {
	EntityType   EntityType
	Key          string
	Label        string
	RelationType string     // ManyToOne or OneToMany
	TargetEntity EntityType // the entity on the other side
	// The other side's key and label, on TargetEntity.
	InverseKey   string
	InverseLabel string
	Position     any
}

// CreateRelation creates a relation: a def on each side, pointing at each
// other, and a column on the many_to_one side holding the linked record's id,
// indexed. "Each contact has one partner company" is many_to_one on contacts
// (column contacts.partner_company_id) with a one_to_many "Partner contacts" on
// companies. The column has no foreign key, because SQLite can't drop a column
// that has one: deleting a record clears links to it instead
// (DetachRelations). Returns the def on input.EntityType. Invalid input is a
// *FieldError.
func CreateRelation(ctx context.Context, input RelationInput) (*CustomFieldDef, error) {
	source, target := input.EntityType, input.TargetEntity
	if !IsEntityType(string(target)) {
		return nil, fieldError(`Invalid target_entity "%s".`, target)
	}
	if input.RelationType != ManyToOne && input.RelationType != OneToMany {
		return nil, fieldError(`relation_type must be "many_to_one" or "one_to_many".`)
	}
	if err := AssertValidKey(source, input.Key); err != nil {
		return nil, err
	}
	if err := AssertValidKey(target, input.InverseKey); err != nil {
		return nil, err
	}
	if source == target && input.Key == input.InverseKey {
		return nil, fieldError("The two sides of a relation need different keys.")
	}
	colEntity, colKey := target, input.InverseKey
	if input.RelationType == ManyToOne {
		colEntity, colKey = source, input.Key
	}
	if !strings.HasSuffix(colKey, "_id") {
		return nil, fieldError(`"%s" holds the linked record's id, so its key must end in _id.`, colKey)
	}
	for _, side := range []struct {
		entity EntityType
		key    string
	}{{source, input.Key}, {target, input.InverseKey}} {
		found, err := db.Get(ctx, "SELECT id FROM custom_field_defs WHERE entity_type = ? AND key = ?", string(side.entity), side.key)
		if err != nil {
			return nil, err
		}
		if found != nil {
			return nil, fieldError(`A "%s" property already exists on %s.`, side.key, side.entity)
		}
	}

	id, inverseID := js.UUID(), js.UUID()
	next, err := db.Get(ctx, "SELECT COALESCE(MAX(position) + 1, 0) AS n FROM custom_field_defs WHERE entity_type = ?", string(target))
	if err != nil {
		return nil, err
	}
	var nextPos any = 0.0
	if next != nil && next["n"] != nil {
		nextPos = next["n"]
	}
	const insert = "INSERT INTO custom_field_defs (id, entity_type, key, label, field_type, position, relation_type, target_entity, inverse_def_id) VALUES (?, ?, ?, ?, 'relation', ?, ?, ?, ?)"
	inverseType := ManyToOne
	if input.RelationType == ManyToOne {
		inverseType = OneToMany
	}
	// shortcut: sequential writes with a manual undo, since the db helper has no
	// batch; a crash between them can leave a def without its column (a later
	// SyncEntityColumns adds it).
	table := QuoteIdent(EntityTables[colEntity])
	if _, err := db.Run(ctx, insert, id, string(source), input.Key, input.Label, orDefault(input.Position, 0.0), input.RelationType, string(target), inverseID); err != nil {
		return nil, err
	}
	added := false
	err = func() error {
		if _, err := db.Run(ctx, insert, inverseID, string(target), input.InverseKey, input.InverseLabel, nextPos, inverseType, string(source), id); err != nil {
			return err
		}
		if _, err := db.Run(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s TEXT", table, QuoteIdent(colKey))); err != nil {
			return err
		}
		added = true
		_, err := db.Run(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(%s)", indexName(EntityTables[colEntity], colKey), table, QuoteIdent(colKey)))
		return err
	}()
	if err != nil {
		if added {
			db.Run(ctx, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, QuoteIdent(colKey))) // best effort, as TS's .catch(() => {})
		}
		if _, undoErr := db.Run(ctx, "DELETE FROM custom_field_defs WHERE id IN (?, ?)", id, inverseID); undoErr != nil {
			return nil, undoErr
		}
		return nil, err
	}
	return GetDef(ctx, id)
}

// UpdateDef updates label/options/position/widget of a def from a request
// body; nil when there is no such def. Key + field_type are immutable
// (changing storage would need a destructive column migration). Each value is
// bound as sent (`patch.label ?? def.label`); one that can't be bound fails
// the write with the db package's error. Sent options are JSON-encoded as
// given (json.RawMessage as is).
func UpdateDef(ctx context.Context, id string, patch map[string]any) (*CustomFieldDef, error) {
	def, err := GetDef(ctx, id)
	if err != nil || def == nil {
		return nil, err
	}
	// Options the patch doesn't send are written back in their stored key order.
	options := jsStringify(def.Options, readKeyOrder(def.optionsText))
	if sent := patch["options"]; sent != nil {
		if options, err = stringify(sent); err != nil {
			return nil, err
		}
	}
	if _, err := db.Run(ctx,
		"UPDATE custom_field_defs SET label = ?, custom_field = ?, options = ?, position = ?, updated_at = datetime('now') WHERE id = ?",
		orDefault(patch["label"], def.Label), orDefault(patch["custom_field"], def.CustomField), options, orDefault(patch["position"], def.Position), id,
	); err != nil {
		return nil, err
	}
	return GetDef(ctx, id)
}

// DeleteDef deletes a def and drops its column; false when there is no such
// def. A relation goes as a whole: both sides, and the many_to_one side's
// index before its column (SQLite won't drop an indexed column).
func DeleteDef(ctx context.Context, id string) (bool, error) {
	def, err := GetDef(ctx, id)
	if err != nil || def == nil {
		return false, err
	}
	doomed := []CustomFieldDef{*def}
	if def.InverseDefID != nil && *def.InverseDefID != "" {
		inverse, err := GetDef(ctx, *def.InverseDefID)
		if err != nil {
			return false, err
		}
		if inverse != nil {
			doomed = append(doomed, *inverse)
		}
	}
	for _, d := range doomed {
		if _, err := db.Run(ctx, "DELETE FROM custom_field_defs WHERE id = ?", d.ID); err != nil {
			return false, err
		}
		// Guard again before touching DDL — never drop a built-in.
		if !HasColumn(d) || !keyPattern.MatchString(d.Key) || isBuiltin(d.EntityType, d.Key) {
			continue
		}
		table := EntityTables[d.EntityType]
		if d.relationType() == ManyToOne {
			db.Run(ctx, "DROP INDEX IF EXISTS "+indexName(table, d.Key)) // best effort
		}
		// The column may already be gone; deletion of the def is the source of truth.
		db.Run(ctx, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", QuoteIdent(table), QuoteIdent(d.Key)))
	}
	return true, nil
}

// ── Schema-sync ───────────────────────────────────────────────────────

func sqliteAffinity(fieldType string) string {
	switch fieldType {
	case "integer", "boolean":
		return "INTEGER"
	case "decimal":
		return "REAL"
	default:
		return "TEXT"
	}
}

// SyncEntityColumns idempotently adds a real column for every def on this
// entity that doesn't yet have one. Never drops here — deletion is explicit
// via DeleteDef.
func SyncEntityColumns(ctx context.Context, entity EntityType) error {
	table := EntityTables[entity]
	cols, err := db.Query(ctx, fmt.Sprintf("PRAGMA table_info(%s)", QuoteIdent(table)))
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, c := range cols {
		have[colText(c["name"])] = true
	}
	defs, err := ListDefs(ctx, &entity)
	if err != nil {
		return err
	}
	for _, def := range defs {
		if !HasColumn(def) || !keyPattern.MatchString(def.Key) || isBuiltin(entity, def.Key) || have[def.Key] {
			continue
		}
		if _, err := db.Run(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", QuoteIdent(table), QuoteIdent(def.Key), sqliteAffinity(def.FieldType))); err != nil {
			return err
		}
		if def.relationType() == ManyToOne {
			if _, err := db.Run(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(%s)", indexName(table, def.Key), QuoteIdent(table), QuoteIdent(def.Key))); err != nil {
				return err
			}
		}
	}
	return nil
}

// ── Value coercion (entity write paths) ───────────────────────────────

// CoerceCustomValue coerces + validates one incoming custom value for its
// def. Returns a SQL-bindable value (string, float64 or nil — a number may be
// NaN, as in TS). An enum violation is a *FieldError.
func CoerceCustomValue(value any, def CustomFieldDef) (any, error) {
	if value == nil || value == "" {
		return nil, nil
	}
	switch def.FieldType {
	case "boolean":
		if js.Truthy(value) {
			return 1.0, nil
		}
		return 0.0, nil
	case "integer":
		return math.Trunc(js.Number(value)), nil
	case "decimal":
		return js.Number(value), nil
	case "json":
		if s, ok := value.(string); ok {
			return s, nil
		}
		return stringify(value)
	case "enumeration":
		s := js.String(value)
		if enum, ok := def.Option("enum").([]any); ok && len(enum) > 0 {
			listed := false
			shown := make([]string, len(enum))
			for i, e := range enum {
				listed = listed || js.String(e) == s // enumVals.map(String) — null is "null"
				if e != nil {
					shown[i] = js.String(e) // enumVals.join(", ") — null is ""
				}
			}
			if !listed {
				return nil, fieldError(`%s: "%s" is not one of [%s]`, def.Key, s, strings.Join(shown, ", "))
			}
		}
		return s, nil
	default:
		return js.String(value), nil
	}
}
