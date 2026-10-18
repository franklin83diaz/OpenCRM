package crm

// The helpers every entity route shares, from the top of index.ts: chunked
// bulk SQL, custom-value writes, the list filter compiler and the list
// footer's column aggregates.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// Chunk splits arr into slices of at most size. Used to keep bulk SQL within
// D1's 100-bound-parameter limit (the same cap applies to the preview-tier
// Facet).
func Chunk[T any](arr []T, size int) [][]T {
	if size < 1 {
		panic("crm.Chunk: size must be positive") // TS looped forever
	}
	out := [][]T{}
	for i := 0; i < len(arr); i += size {
		out = append(out, arr[i:min(i+size, len(arr))])
	}
	return out
}

// IsDay reports whether v is a real calendar day written as YYYY-MM-DD.
func IsDay(v string) bool {
	if !dayShape.MatchString(v) {
		return false
	}
	t, ok := js.ParseDate(v + "T00:00:00Z")
	return ok && isoDay(t) == v
}

// isoDay is toISOString().slice(0, 10): years outside 0–9999 are written
// with a sign and six digits, so the slice cuts them short as JS did.
func isoDay(t time.Time) string {
	t = t.UTC()
	y := t.Year()
	var s string
	if y >= 0 && y <= 9999 {
		s = fmt.Sprintf("%04d-%02d-%02d", y, int(t.Month()), t.Day())
	} else {
		sign := "+"
		if y < 0 {
			sign, y = "-", -y
		}
		s = fmt.Sprintf("%s%06d-%02d-%02d", sign, y, int(t.Month()), t.Day())
	}
	return s[:10]
}

// WithRelationsOne is WithRelations for a single record read (list reads call
// it directly); a nil row stays nil.
func WithRelationsOne(ctx context.Context, entity EntityType, row db.Row) (db.Row, error) {
	if row == nil {
		return nil, nil
	}
	out, err := WithRelations(ctx, entity, []db.Row{row}, 100)
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// LogActivity appends a row to the activity timeline. Never fails — logging
// is best-effort: timeline logging must never break the primary action. meta
// is stored as JSON: a struct keeps its field order, as TS's object literal
// did; a map's keys come out sorted. nil is {}.
func LogActivity(ctx context.Context, entityType, entityID, typ, body string, meta any) {
	if m, ok := meta.(map[string]any); meta == nil || (ok && m == nil) {
		meta = map[string]any{}
	}
	s, err := stringify(meta)
	if err != nil {
		return
	}
	db.Run(ctx, "INSERT INTO activities (id, entity_type, entity_id, type, body, meta) VALUES (?, ?, ?, ?, ?, ?)",
		js.UUID(), entityType, entityID, typ, body, s)
}

// ApplyCustomValues writes custom-property values for one entity row. custom
// holds { key: value }; only keys with a matching def that has a column are
// written, each coerced/validated for its type. Runs as a follow-up UPDATE so
// the built-in INSERT/UPDATE paths stay untouched. An enum violation is a
// *FieldError. A map has no order: the keys are written in JS order of the
// sorted keys. Where TS's object kept a request body's order, use
// ApplyCustomValuesInOrder with the order ResolveCustomWrite saw.
func ApplyCustomValues(ctx context.Context, entity EntityType, table, id string, custom map[string]any) error {
	return ApplyCustomValuesInOrder(ctx, entity, table, id, custom, jsKeyOrder(sortedKeys(custom)))
}

// ApplyCustomValuesInOrder is ApplyCustomValues visiting custom's keys in
// order (Object.entries order): the SET list, and which enum violation is
// named first, follow it. Keys in order that custom lacks are skipped.
func ApplyCustomValuesInOrder(ctx context.Context, entity EntityType, table, id string, custom map[string]any, order []string) error {
	if custom == nil {
		return nil
	}
	defs, err := ListDefs(ctx, &entity)
	if err != nil {
		return err
	}
	byKey := map[string]CustomFieldDef{}
	for _, d := range defs {
		byKey[d.Key] = d
	}
	var sets []string
	var params []any
	for _, key := range order {
		if _, ok := custom[key]; !ok {
			continue
		}
		def, ok := byKey[key]
		if !ok || !HasColumn(def) {
			continue // ignore unknown keys: only defined properties with a column are writable
		}
		v, err := CoerceCustomValue(custom[key], def)
		if err != nil {
			return err
		}
		sets = append(sets, QuoteIdent(key)+" = ?")
		params = append(params, v)
	}
	if len(sets) == 0 {
		return nil
	}
	params = append(params, id)
	_, err = db.Run(ctx, fmt.Sprintf("UPDATE %s SET %s WHERE id = ?", table, strings.Join(sets, ", ")), params...)
	return err
}

// CustomValuesIssue checks a request body's `custom` the way the reusable zod
// field `z.record(z.string(), z.any()).optional()` did: absent or an object is
// fine; anything else is its invalid_type issue. The nested bag is still
// accepted for back-compat, but custom keys may now also be sent flat at the
// top level (see ResolveCustomWrite).
func CustomValuesIssue(body map[string]any) (web.Issue, bool) {
	v, present := body["custom"]
	if _, isObject := v.(map[string]any); !present || isObject {
		return web.Issue{}, false
	}
	received := zodType(v)
	return web.Issue{Code: "invalid_type", Expected: "object", Received: received, Path: []any{"custom"},
		Message: "Expected object, received " + received}, true
}

// zodType is zod's name for a JSON value's type.
func zodType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	default:
		return "object"
	}
}

// ResolveCustomWrite merges a request body's flat top-level custom keys with
// its nested `custom` bag, then classifies against the entity's registry. Both
// shapes are accepted (the bag wins on a key conflict); built-in base keys
// pass through untouched. Returns the writable custom values and any unknown
// keys the caller rejects.
//
// raw is the request's JSON text (web.RawBody), read only for the order of
// its keys: TS listed unknown keys in the body's order (array-index keys
// first, as JS objects keep them), then the bag's new keys. With raw "" they
// come in the JS order of the sorted keys. __proto__ is never a key: zod
// dropped it, and assigning it would have set the prototype.
func ResolveCustomWrite(ctx context.Context, entity EntityType, body map[string]any, raw string) (values map[string]any, unknown []string, err error) {
	candidates := map[string]any{}
	for k, v := range body {
		if k != "custom" && k != "__proto__" {
			candidates[k] = v
		}
	}
	bag, _ := body["custom"].(map[string]any)
	for k, v := range bag {
		if k != "__proto__" {
			candidates[k] = v
		}
	}
	return classifyCustomWrite(ctx, entity, customWriteOrder(body, raw), candidates)
}

// customWriteOrder is the order of a write's candidate keys as TS's merged
// object listed them (see ResolveCustomWrite): the values ResolveCustomWrite
// returns were added in this order, which is the order TS wrote them in.
func customWriteOrder(body map[string]any, raw string) []string {
	bag, _ := body["custom"].(map[string]any)
	var order []string
	seen := map[string]bool{}
	add := func(k string, ok bool) {
		if ok && !seen[k] && k != "__proto__" {
			seen[k] = true
			order = append(order, k)
		}
	}
	topKeys, members := objectMembers(raw)
	for _, k := range append(topKeys, sortedKeys(body)...) { // then any key raw didn't show
		_, ok := body[k]
		add(k, ok && k != "custom")
	}
	if bag != nil {
		bagKeys, _ := objectMembers(string(members["custom"]))
		for _, k := range append(bagKeys, sortedKeys(bag)...) {
			_, ok := bag[k]
			add(k, ok)
		}
	}
	return jsKeyOrder(order)
}

// UnknownFieldsError builds the 422 body when a write named unknown keys —
// a field that is neither a base column nor a registered custom field,
// surfaced loudly instead of silently dropped — or nil when the write is clean.
func UnknownFieldsError(ctx context.Context, entity EntityType, unknown []string) (map[string]any, error) {
	if len(unknown) == 0 {
		return nil, nil
	}
	valid, err := WritableFieldKeys(ctx, entity)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"error":          fmt.Sprintf("Unknown field(s) for %s: %s. Send a base field or a registered custom field, or define it first via POST /api/custom-fields.", entity, strings.Join(unknown, ", ")),
		"unknown_fields": unknown,
		"valid_fields":   valid,
	}, nil
}

// QuoteIdent quotes a SQL identifier (custom-field keys are already
// regex-validated at def creation, but quote defensively).
func QuoteIdent(k string) string {
	return `"` + strings.ReplaceAll(k, `"`, `""`) + `"`
}

// CoerceForImport is lenient coercion for bulk import: an invalid cell (bad
// enum, unparseable number) becomes nil rather than aborting the whole import
// batch.
func CoerceForImport(value any, def CustomFieldDef) any {
	v, err := CoerceCustomValue(value, def)
	if err != nil {
		return nil
	}
	if f, ok := v.(float64); ok && math.IsNaN(f) {
		return nil
	}
	return v
}

// ResolveImportCustomColumns returns the custom columns to write for an
// import: defs whose key is present and non-empty in at least one row's
// `custom` bag (customs[i], nil when the row has none). Keeps the bulk INSERT
// narrow. Relations aren't imported: a cell holds a name, not the linked
// record's id. The keys come in first-seen order, each row's in JS order of
// its sorted keys.
func ResolveImportCustomColumns(ctx context.Context, entity EntityType, customs []map[string]any) (keys []string, defByKey map[string]CustomFieldDef, err error) {
	return ResolveImportCustomColumnsInOrder(ctx, entity, customs, nil)
}

// ResolveImportCustomColumnsInOrder is ResolveImportCustomColumns visiting
// row i's keys in orders[i] (Object.entries order, as the request listed
// them); a row without one is visited in JS order of its sorted keys.
func ResolveImportCustomColumnsInOrder(ctx context.Context, entity EntityType, customs []map[string]any, orders [][]string) (keys []string, defByKey map[string]CustomFieldDef, err error) {
	defs, err := ListDefs(ctx, &entity)
	if err != nil {
		return nil, nil, err
	}
	defByKey = map[string]CustomFieldDef{}
	for _, d := range defs {
		if d.FieldType != "relation" {
			defByKey[d.Key] = d
		}
	}
	keys = []string{}
	present := map[string]bool{}
	for i, custom := range customs {
		order := jsKeyOrder(sortedKeys(custom))
		if i < len(orders) && orders[i] != nil {
			order = orders[i]
		}
		for _, k := range order {
			v, has := custom[k]
			if _, ok := defByKey[k]; ok && has && v != nil && v != "" && !present[k] {
				present[k] = true
				keys = append(keys, k)
			}
		}
	}
	return keys, defByKey, nil
}

// TableColumns returns the real column names of a table (from sqlite). Used to
// validate sort/filter fields against actual columns — the safe allowlist for
// built-ins + custom.
func TableColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := db.Query(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, err
	}
	cols := make(map[string]bool, len(rows))
	for _, r := range rows {
		cols[colText(r["name"])] = true
	}
	return cols, nil
}

// ── Filters ────────────────────────────────────────────────────────
// A filter is a tree. The top level is a list, ANDed: each entry is a rule
// ({field, op, value}) or a group ({logic: "and"|"or", rules: [...]}). A group
// may hold rules and one more level of groups. The flat list of rules is the
// same shape with no groups, so older links keep working.

// MaxFilterParams: filter values past this many would crowd D1's 100 bound
// parameters per query.
const MaxFilterParams = 60

// LocalDay is "YYYY-MM-DD" of a UTC instant shifted to the viewer's offset
// (minutes east of UTC).
func LocalDay(t time.Time, tzOffset int) string {
	return isoDay(t.Add(time.Duration(tzOffset) * time.Minute))
}

var relativePattern = regexp.MustCompile(`^(PAST|NEXT|THIS)_(\d{1,4})_(DAY|WEEK|MONTH|YEAR)$`)

// RelativeRange is a relative date ("PAST_7_DAY", "NEXT_2_WEEK",
// "THIS_1_MONTH") as an inclusive [start, end] of local days on the day of
// now; ok is false for anything else. Weeks start on Monday.
func RelativeRange(value string, tzOffset int, now time.Time) (start, end string, ok bool) {
	m := relativePattern.FindStringSubmatch(value)
	if m == nil {
		return "", "", false
	}
	dir, unit := m[1], m[3]
	amount, _ := strconv.Atoi(m[2])
	local := now.UTC().Add(time.Duration(tzOffset) * time.Minute)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	// Date's setUTC* setters roll an overflowing day over (Jan 31 + 1 month is
	// Mar 3), as time.Date does.
	shift := func(d time.Time, by int) time.Time {
		switch unit {
		case "DAY":
			return d.AddDate(0, 0, by)
		case "WEEK":
			return d.AddDate(0, 0, 7*by)
		case "MONTH":
			return d.AddDate(0, by, 0)
		default:
			return d.AddDate(by, 0, 0)
		}
	}
	switch dir {
	case "PAST":
		return isoDay(shift(today, -amount)), isoDay(today), true
	case "NEXT":
		return isoDay(today), isoDay(shift(today, amount)), true
	}
	first := today
	switch unit {
	case "WEEK":
		first = first.AddDate(0, 0, -((int(first.Weekday()) + 6) % 7))
	case "MONTH":
		first = time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "YEAR":
		first = time.Date(first.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	}
	last := shift(first, 1).AddDate(0, 0, -1)
	if unit == "DAY" {
		last = today
	}
	return isoDay(first), isoDay(last), true
}

var (
	countOps = map[string]string{"is": "=", "is_not": "!=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}
	cmpOps   = map[string]string{"gt": ">", "lt": "<", "gte": ">=", "lte": "<="}
	dayOps   = map[string]string{"on": "=", "before": "<", "after": ">="}
)

// BuildFilters builds safe WHERE clauses from a JSON filter tree. Fields are
// validated against cols (real columns), so identifiers are never
// user-controlled; values are always parameterised (numbers as float64,
// strings as string). tzOffset (minutes east of UTC) puts date rules on the
// viewer's local day; now is the instant relative dates count from. many are
// the one_to_many sides a rule may name (only with a prefix naming the row's
// table). Invalid rules are skipped; too many values are a *FieldError.
func BuildFilters(cols map[string]bool, raw, prefix string, tzOffset int, many map[string]ManyLink, now time.Time) (clauses []string, params []any, err error) {
	params = []any{}
	var nodes []any
	if raw == "" {
		raw = "[]"
	}
	var parsed any
	if json.Unmarshal([]byte(raw), &parsed) == nil {
		nodes, _ = parsed.([]any)
	}
	sign := ""
	if tzOffset >= 0 {
		sign = "+"
	}
	mod := fmt.Sprintf("%s%d minutes", sign, tzOffset)

	// A value's local day: a date-only value is already one; a timestamp is
	// stored in UTC and shifted to the viewer's offset.
	dayOf := func(col string) string {
		params = append(params, mod)
		return fmt.Sprintf("(CASE WHEN length(%s) <= 10 THEN %s ELSE date(%s, ?) END)", col, col, col)
	}
	today := func() string {
		params = append(params, mod)
		return "date('now', ?)"
	}
	stringsOf := func(v any) []string {
		list, ok := v.([]any)
		if !ok {
			return nil
		}
		out := []string{}
		for _, e := range list {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	addStrings := func(list []string) string {
		for _, s := range list {
			params = append(params, s)
		}
		return marks(len(list))
	}

	// A one_to_many field has no column here: it matches through the linked
	// records that point back at this row (prefix must name the row's table).
	manyLeaf := func(link ManyLink, op string, r map[string]any) string {
		linked := fmt.Sprintf("SELECT 1 FROM %s m WHERE m.%s = %sid", link.Table, QuoteIdent(link.FK), prefix)
		ids := stringsOf(r["value"])
		switch op {
		case "is_empty":
			return "NOT EXISTS (" + linked + ")"
		case "is_not_empty":
			return "EXISTS (" + linked + ")"
		case "is", "is_not":
			if len(ids) == 0 {
				return ""
			}
			found := fmt.Sprintf("EXISTS (%s AND m.%s IN (%s))", linked, link.idColumn(), addStrings(ids)) // a built-in name, never a key
			if op == "is" {
				return found
			}
			return "NOT " + found
		}
		return ""
	}

	// "count:<many side>" compares how many records link back: Contacts count ≥ 3.
	countLeaf := func(link ManyLink, r map[string]any) string {
		switch r["op"] {
		case "is_empty":
			return CountSQL(link, prefix) + " = 0"
		case "is_not_empty":
			return CountSQL(link, prefix) + " > 0"
		}
		n := math.NaN()
		switch v := r["value"].(type) {
		case string, float64:
			n = js.Number(v)
		}
		op, ok := countOps[js.String(r["op"])]
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return ""
		}
		params = append(params, n)
		return CountSQL(link, prefix) + " " + op + " ?"
	}

	leaf := func(r map[string]any) string {
		field, fieldOK := r["field"].(string)
		op, opOK := r["op"].(string)
		if fieldOK && prefix != "" {
			if counted, ok := CountOf(field); ok && counted != "" {
				if link, ok := many[counted]; ok {
					return countLeaf(link, r)
				}
			}
			if link, ok := many[field]; ok && opOK {
				return manyLeaf(link, op, r)
			}
		}
		if !fieldOK || !cols[field] || !opOK {
			return ""
		}
		col := prefix + QuoteIdent(field)
		list := stringsOf(r["value"])
		v := ""
		switch x := r["value"].(type) {
		case string:
			v = x
		case float64:
			v = js.String(x)
		}
		num := js.Number(v)
		switch op {
		case "contains":
			if v == "" {
				return ""
			}
			params = append(params, "%"+v+"%")
			return col + " LIKE ?"
		case "does_not_contain":
			if v == "" {
				return ""
			}
			params = append(params, "%"+v+"%")
			return fmt.Sprintf("(%s IS NULL OR %s NOT LIKE ?)", col, col)
		// is / is not ignore case, as contains (LIKE) already does: "consulting" finds "Consulting".
		case "is":
			if list != nil {
				if len(list) == 0 {
					return ""
				}
				return fmt.Sprintf("%s COLLATE NOCASE IN (%s)", col, addStrings(list))
			}
			params = append(params, v)
			return col + " = ? COLLATE NOCASE"
		case "is_not":
			if list != nil {
				if len(list) == 0 {
					return ""
				}
				return fmt.Sprintf("(%s IS NULL OR %s COLLATE NOCASE NOT IN (%s))", col, col, addStrings(list))
			}
			params = append(params, v)
			return fmt.Sprintf("(%s IS NULL OR %s != ? COLLATE NOCASE)", col, col)
		case "is_empty":
			return fmt.Sprintf("(%s IS NULL OR %s = '')", col, col)
		case "is_not_empty":
			return fmt.Sprintf("(%s IS NOT NULL AND %s != '')", col, col)
		case "gt", "lt", "gte", "lte":
			if v == "" || math.IsNaN(num) {
				return ""
			}
			params = append(params, num)
			return col + " " + cmpOps[op] + " ?"
		case "on", "before", "after":
			if !dayShape.MatchString(v) {
				return ""
			}
			d := dayOf(col)
			params = append(params, v)
			return d + " " + dayOps[op] + " ?"
		case "today":
			d := dayOf(col)
			return d + " = " + today()
		case "in_past":
			return fmt.Sprintf("(%s IS NOT NULL AND %s != '' AND datetime(%s) < datetime('now'))", col, col, col)
		case "in_future":
			return fmt.Sprintf("(%s IS NOT NULL AND %s != '' AND datetime(%s) > datetime('now'))", col, col, col)
		case "relative":
			start, end, ok := RelativeRange(v, tzOffset, now)
			if !ok {
				return ""
			}
			d := dayOf(col)
			params = append(params, start, end)
			return d + " BETWEEN ? AND ?"
		}
		return ""
	}

	// depth 0: the top-level list; a group there may hold one more level of groups.
	var node func(n any, depth int) string
	node = func(n any, depth int) string {
		g, ok := n.(map[string]any) // an array has no rules and no field: skipped
		if !ok {
			return ""
		}
		rules, isGroup := g["rules"].([]any)
		if !isGroup {
			return leaf(g)
		}
		if depth > 1 {
			return ""
		}
		var parts []string
		for _, r := range rules {
			if p := node(r, depth+1); p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) == 0 {
			return ""
		}
		sep := " AND "
		if g["logic"] == "or" {
			sep = " OR "
		}
		return "(" + strings.Join(parts, sep) + ")"
	}

	clauses = []string{}
	for _, n := range nodes {
		if c := node(n, 0); c != "" {
			clauses = append(clauses, c)
		}
	}
	if len(params) > MaxFilterParams {
		return nil, nil, fieldError("Too many filter values (at most %d)", MaxFilterParams)
	}
	return clauses, params, nil
}

// TzOffsetOf is the viewer's UTC offset in minutes from a `tz` query value; 0
// when absent or invalid.
func TzOffsetOf(raw string) int {
	n := js.Number(raw)
	if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || math.Abs(n) > 840 {
		return 0
	}
	return int(n)
}

// ── Column aggregates (the list footer's "Calculate" row) ──────────
// The footer's operations: every column can count; numbers also sum,
// average and range; dates give the earliest and latest; booleans count each
// side. Computed over the whole filtered list, not the page on screen.

// ColumnKind is how a column aggregates.
type ColumnKind string

const (
	KindText    ColumnKind = "text"
	KindNumber  ColumnKind = "number"
	KindDate    ColumnKind = "date"
	KindBoolean ColumnKind = "boolean"
)

var baseAggregates = []string{"count", "count_empty", "count_not_empty", "count_unique", "percent_empty", "percent_not_empty"}

var aggregatesFor = map[ColumnKind][]string{
	KindText:    baseAggregates,
	KindNumber:  append(append([]string{}, baseAggregates...), "sum", "avg", "min", "max"),
	KindDate:    append(append([]string{}, baseAggregates...), "earliest", "latest"),
	KindBoolean: append(append([]string{}, baseAggregates...), "count_true", "count_false"),
}

// AllAggregates is every operation of any kind (a view field's footer
// aggregate must be one).
var AllAggregates = func() map[string]bool {
	all := map[string]bool{}
	for _, ops := range aggregatesFor {
		for _, op := range ops {
			all[op] = true
		}
	}
	return all
}()

// KindOf is how a custom field of this base type aggregates.
func KindOf(fieldType string) ColumnKind {
	switch fieldType {
	case "integer", "decimal":
		return KindNumber
	case "date", "datetime":
		return KindDate
	case "boolean":
		return KindBoolean
	}
	return KindText
}

func aggregateSQL(op, x string) (string, error) {
	empty := fmt.Sprintf("(%s IS NULL OR %s = '')", x, x)
	switch op {
	case "count":
		return "COUNT(*)", nil
	case "count_empty":
		return "SUM(CASE WHEN " + empty + " THEN 1 ELSE 0 END)", nil
	case "count_not_empty":
		return "SUM(CASE WHEN " + empty + " THEN 0 ELSE 1 END)", nil
	case "count_unique":
		return "COUNT(DISTINCT NULLIF(" + x + ", ''))", nil
	case "percent_empty":
		return "ROUND(100.0 * SUM(CASE WHEN " + empty + " THEN 1 ELSE 0 END) / MAX(COUNT(*), 1))", nil
	case "percent_not_empty":
		return "ROUND(100.0 * SUM(CASE WHEN " + empty + " THEN 0 ELSE 1 END) / MAX(COUNT(*), 1))", nil
	case "sum":
		return "SUM(" + x + ")", nil
	case "avg":
		return "ROUND(AVG(" + x + "), 2)", nil
	case "min":
		return "MIN(" + x + ")", nil
	case "max":
		return "MAX(" + x + ")", nil
	case "earliest":
		return "MIN(NULLIF(" + x + ", ''))", nil
	case "latest":
		return "MAX(NULLIF(" + x + ", ''))", nil
	case "count_true":
		return "SUM(CASE WHEN " + x + " = 1 THEN 1 ELSE 0 END)", nil
	case "count_false":
		return "SUM(CASE WHEN " + x + " = 0 THEN 1 ELSE 0 END)", nil
	}
	return "", errors.New("Unknown aggregate " + op)
}

// AggregateColumn is a column the footer can aggregate: its SQL over the
// list's FROM, and its kind.
type AggregateColumn struct {
	SQL  string     `json:"sql"`
	Kind ColumnKind `json:"kind"`
}

// AggregateColumns runs the requested ops ([{key, op}] as JSON, at most 50)
// in one query over from + whereSQL. Only keys in columns and ops valid for
// their kind are run; anything else (a deleted field, a sum of text) is left
// out of the answer. A null entry fails as it did in TS (a 500).
func AggregateColumns(ctx context.Context, from, whereSQL string, params []any, columns map[string]AggregateColumn, raw string) (map[string]any, error) {
	if raw == "" {
		raw = "[]"
	}
	var asked []any
	var parsed any
	if json.Unmarshal([]byte(raw), &parsed) == nil {
		asked, _ = parsed.([]any)
		asked = asked[:min(len(asked), 50)]
	}
	type wanted struct{ key, op string }
	var valid []wanted
	for _, a := range asked {
		if a == nil {
			return nil, errors.New("Cannot read properties of null (reading 'key')")
		}
		m, _ := a.(map[string]any)
		key, keyOK := m["key"].(string)
		op, opOK := m["op"].(string)
		col, known := columns[key]
		if keyOK && opOK && known && slices.Contains(aggregatesFor[col.Kind], op) {
			valid = append(valid, wanted{key, op})
		}
	}
	out := map[string]any{}
	if len(valid) == 0 {
		return out, nil
	}
	selects := make([]string, len(valid))
	for i, a := range valid {
		sql, err := aggregateSQL(a.op, columns[a.key].SQL)
		if err != nil {
			return nil, err
		}
		selects[i] = fmt.Sprintf("%s AS a%d", sql, i)
	}
	row, err := db.Get(ctx, fmt.Sprintf("SELECT %s FROM %s%s", strings.Join(selects, ", "), from, whereSQL), params...)
	if err != nil {
		return nil, err
	}
	for i, a := range valid {
		out[a.key] = row[fmt.Sprintf("a%d", i)] // a later op on the same key wins, as in TS
	}
	return out, nil
}

// CustomAggregateColumns are the custom fields' columns for aggregates, for
// the fields that still have one in cols.
func CustomAggregateColumns(ctx context.Context, entity EntityType, alias string, cols map[string]bool) (map[string]AggregateColumn, error) {
	defs, err := ListDefs(ctx, &entity)
	if err != nil {
		return nil, err
	}
	out := map[string]AggregateColumn{}
	for _, d := range defs {
		if cols[d.Key] {
			out[d.Key] = AggregateColumn{SQL: alias + "." + QuoteIdent(d.Key), Kind: KindOf(d.FieldType)}
		}
	}
	return out, nil
}

// ── JS object and JSON semantics ───────────────────────────────────

// stringify is JSON.stringify for the values this package stores: no HTML
// escaping and U+2028/U+2029 written as they are. Map keys come out sorted,
// where JS kept insertion order.
func stringify(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	b := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	// encoding/json always escapes the two line separators; every backslash
	// in its output starts an escape, so walking escape by escape is exact.
	if !bytes.Contains(b, []byte(`\u202`)) {
		return string(b), nil
	}
	var out strings.Builder
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' || i+1 >= len(b) {
			out.WriteByte(b[i])
			continue
		}
		if b[i+1] == 'u' && i+5 < len(b) && (string(b[i+2:i+6]) == "2028" || string(b[i+2:i+6]) == "2029") {
			if b[i+5] == '8' {
				out.WriteString("\u2028")
			} else {
				out.WriteString("\u2029")
			}
			i += 5
			continue
		}
		out.WriteByte(b[i])
		out.WriteByte(b[i+1])
		i++
	}
	return out.String(), nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// jsKeyOrder orders keys as a JS object lists them when they were added in
// this order: array-index keys first, ascending, then the rest as added.
func jsKeyOrder(keys []string) []string {
	var indexes, rest []string
	for _, k := range keys {
		if isArrayIndex(k) {
			indexes = append(indexes, k)
		} else {
			rest = append(rest, k)
		}
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		a, _ := strconv.ParseUint(indexes[i], 10, 64)
		b, _ := strconv.ParseUint(indexes[j], 10, 64)
		return a < b
	})
	return append(indexes, rest...)
}

// isArrayIndex reports whether k is a canonical array index (0 … 2³²−2).
func isArrayIndex(k string) bool {
	if k == "" || (k[0] == '0' && k != "0") {
		return false
	}
	for _, c := range k {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(k, 10, 64)
	return err == nil && n < 1<<32-1
}

// objectMembers reads a JSON object's keys in document order (a repeated key
// keeps its first place, as JSON.parse does) and each key's last value. Not
// an object: nil.
func objectMembers(raw string) ([]string, map[string]json.RawMessage) {
	dec := json.NewDecoder(strings.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, nil
	}
	keys := []string{}
	vals := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil
		}
		k, ok := tok.(string)
		if !ok {
			return nil, nil
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil
		}
		if _, dup := vals[k]; !dup {
			keys = append(keys, k)
		}
		vals[k] = v
	}
	return keys, vals
}

// ── What the entity routes repeat ──────────────────────────────────
// index.ts wrote these steps out in each list and write handler (companies,
// contacts, deals); here they are once.

// paginationQueryIssues is the list routes' zod query schema,
// PaginationQuery.extend({<extra>: z.string().optional(), …}): page, limit,
// sort, order (z.enum(["asc", "desc"])), search, filters, tz, then extra.
func paginationQueryIssues(q map[string]any, extra ...string) []web.Issue {
	var issues []web.Issue
	for _, key := range append([]string{"page", "limit", "sort", "order", "search", "filters", "tz"}, extra...) {
		v, present := q[key]
		if !present {
			continue
		}
		var issue *web.Issue
		if key == "order" {
			issue = checkEnum(zodPath(key), v, []string{"asc", "desc"})
		} else {
			issue = checkType(zodPath(key), v, "string")
		}
		if issue != nil {
			issues = append(issues, *issue)
		}
	}
	return issues
}

// textAt is m[key] when it is a string (a query value, or a validated body's
// field), else "".
func textAt(q map[string]any, key string) string {
	s, _ := q[key].(string)
	return s
}

// firstQueryValues is c.req.query() on a plain route: each parameter's first
// value, decoded as Hono did. Nothing is validated.
func firstQueryValues(r *http.Request) map[string]any {
	out := map[string]any{}
	for k, vs := range honoQueries(r) {
		out[k] = vs[0]
	}
	return out
}

// listPage is a list's page, limit and offset: parseInt'd, page at least 1
// (default 1), limit 1 to 100 (default 25). One that isn't a number stays
// NaN, as Math.max left it, and the query fails binding it.
func listPage(q map[string]any) (page, limit, offset float64) {
	p, l := textAt(q, "page"), textAt(q, "limit")
	if p == "" {
		p = "1"
	}
	if l == "" {
		l = "25"
	}
	page = math.Max(1, js.ParseInt(p))
	limit = math.Min(100, math.Max(1, js.ParseInt(l)))
	return page, limit, (page - 1) * limit
}

// listOrder is the sort direction: asc or desc (the default).
func listOrder(q map[string]any) string {
	order := strings.ToLower(textAt(q, "order"))
	if order != "asc" && order != "desc" {
		return "desc"
	}
	return order
}

// listSortSQL is what a companies or contacts list orders by, over the table
// aliased `alias`: "count:<many side>" counts the linked records; a real
// column (built-in or custom) sorts by itself, or by the linked record's name
// when it holds one; anything else sorts by id.
func listSortSQL(ctx context.Context, entity EntityType, alias, sort string, cols map[string]bool, many map[string]ManyLink) (string, error) {
	if sort == "" {
		sort = "id"
	}
	if side, ok := CountOf(sort); ok {
		if link, ok := many[side]; ok {
			return CountSQL(link, alias+"."), nil
		}
	}
	if !cols[sort] {
		sort = "id"
	}
	byName, err := RelationSortSQL(ctx, entity, alias, sort)
	if err != nil || byName != "" {
		return byName, err
	}
	return alias + "." + QuoteIdent(sort), nil
}

// whereOf joins a list's conditions: "" when there are none.
func whereOf(where []string) string {
	if len(where) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(where, " AND ")
}

// tryList is tryJSON for the list and aggregate routes, which answered a bad
// filter (a *FieldError: too many values) with 400 and anything else with 500.
func tryList(w http.ResponseWriter, fn func() (int, any, error)) error {
	status, body, err := fn()
	if err != nil {
		status = http.StatusInternalServerError
		var fe *FieldError
		if errors.As(err, &fe) {
			status = http.StatusBadRequest
		}
		return web.JSON(w, status, errorBody{errMessage(err)})
	}
	return web.JSON(w, status, body)
}

// orZero is `row?.<key> || 0`.
func orZero(row db.Row, key string) any {
	if row == nil || !js.Truthy(row[key]) {
		return 0.0
	}
	return row[key]
}

// writeBodyIssues is a create or update route's zod body schema: an object of
// the given string fields, then `custom` (CustomValues), passthrough.
func writeBodyIssues(v any, fields []stringField) []web.Issue {
	body, isObject := v.(map[string]any)
	if !isObject {
		return []web.Issue{invalidType(zodPath(), "object", v)}
	}
	issues := stringFieldIssues(body, fields)
	if issue, bad := CustomValuesIssue(body); bad {
		issues = append(issues, issue)
	}
	return issues
}

// customSet is a write's custom values, with their keys in the order TS's
// object listed them: the order they are written in.
type customSet struct {
	values map[string]any
	order  []string
}

// apply writes them to row id (ApplyCustomValuesInOrder).
func (c customSet) apply(ctx context.Context, entity EntityType, table, id string) error {
	return ApplyCustomValuesInOrder(ctx, entity, table, id, c.values, c.order)
}

// customWrite is what every create and update checks of a body's custom
// fields before writing: the writable custom values, or the answer that
// refuses the write — 422 when it names fields that don't exist, 400 when a
// required one is missing (mode "create" or "update", see
// MissingRequiredCustom) or a relation names no record. raw is the request's
// JSON text (web.RawBody), for the order of the keys.
func customWrite(ctx context.Context, entity EntityType, body map[string]any, raw, mode string) (custom customSet, status int, answer any, err error) {
	values, unknown, err := ResolveCustomWrite(ctx, entity, body, raw)
	if err != nil {
		return custom, 0, nil, err
	}
	unknownErr, err := UnknownFieldsError(ctx, entity, unknown)
	if err != nil {
		return custom, 0, nil, err
	}
	if unknownErr != nil {
		return custom, http.StatusUnprocessableEntity, unknownErr, nil
	}
	missing, err := MissingRequiredCustom(ctx, entity, values, mode)
	if err != nil {
		return custom, 0, nil, err
	}
	if len(missing) > 0 {
		return custom, http.StatusBadRequest, errorBody{"Missing required field(s): " + strings.Join(missing, ", ")}, nil
	}
	relationErr, err := RelationWriteError(ctx, entity, values)
	if err != nil {
		return custom, 0, nil, err
	}
	if relationErr != "" {
		return custom, http.StatusBadRequest, errorBody{relationErr}, nil
	}
	return customSet{values, customWriteOrder(body, raw)}, 0, nil, nil
}

// bulkIDsIssues is the bulk-delete body schema,
// z.object({ ids: z.array(z.string().min(1)).min(1).max(500) }).
func bulkIDsIssues(v any) []web.Issue {
	body, isObject := v.(map[string]any)
	if !isObject {
		return []web.Issue{invalidType(zodPath(), "object", v)}
	}
	raw, present := body["ids"]
	if !present {
		return []web.Issue{requiredIssue(zodPath("ids"), "array")}
	}
	list, isArray := raw.([]any)
	if !isArray {
		return []web.Issue{invalidType(zodPath("ids"), "array", raw)}
	}
	var issues []web.Issue
	if len(list) < 1 {
		issues = append(issues, arrayTooSmall(zodPath("ids"), 1))
	}
	if len(list) > 500 {
		issues = append(issues, arrayTooBig(zodPath("ids"), 500))
	}
	for i, item := range list {
		s, isString := item.(string)
		if !isString {
			issues = append(issues, invalidType(zodPath("ids", i), "string", item))
		} else if s == "" {
			issues = append(issues, stringTooSmall(zodPath("ids", i), 1))
		}
	}
	return issues
}

type deletedBody struct {
	Deleted float64 `json:"deleted"`
}

// bulkDelete deletes many records of entity at once: the table's selection
// bar. Chunked under D1's 100-parameter cap; not one transaction, so a
// failure mid-way leaves the earlier chunks deleted (the list refetches
// either way). ids is the validated body's list; repeats count once.
func bulkDelete(ctx context.Context, entity EntityType, ids []any) (deletedBody, error) {
	unique := []string{}
	seen := map[string]bool{}
	for _, v := range ids {
		if id := v.(string); !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	var out deletedBody
	for _, part := range Chunk(unique, 90) {
		res, err := db.Run(ctx, fmt.Sprintf("DELETE FROM %s WHERE id IN (%s)", EntityTables[entity], marks(len(part))), stringParams(part)...)
		if err != nil {
			return out, err
		}
		out.Deleted += float64(res.Changes)
	}
	return out, DetachRelations(ctx, entity, unique)
}
