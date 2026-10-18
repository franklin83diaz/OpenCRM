package crm

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
)

// Expected values below come from running the TS functions
// (custom-fields.ts and index.ts at 0068887) on the same inputs under node.

func fieldErr(t *testing.T, err error) string {
	t.Helper()
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v (%T), want *FieldError", err, err)
	}
	return fe.Msg
}

func mustDef(t *testing.T, ctx context.Context, in CustomFieldInput) *CustomFieldDef {
	t.Helper()
	def, err := CreateDef(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func TestCustomCreateDefAddsColumn(t *testing.T) {
	ctx := dbtest.Setup(t)
	def, err := CreateDef(ctx, CustomFieldInput{EntityType: Company, Key: "tier", Label: "Tier", FieldType: "text"})
	if err != nil {
		t.Fatal(err)
	}
	cols, err := TableColumns(ctx, "companies")
	if err != nil {
		t.Fatal(err)
	}
	if !cols["tier"] {
		t.Errorf("no tier column: %v", cols)
	}
	if def.Key != "tier" || def.EntityType != Company || def.FieldType != "text" || def.CustomField != "" ||
		def.Position != 0 || def.RelationType != nil || def.TargetEntity != nil || def.InverseDefID != nil || def.CreatedAt == "" {
		t.Errorf("def = %+v", def)
	}
	checkJSON(t, "options", def.Options, `{}`)
	// Its JSON is the TS object's: every column of the row, options parsed.
	got := jsonOf(t, def)
	for _, part := range []string{`"entity_type":"company"`, `"custom_field":""`, `"options":{}`, `"position":0`, `"relation_type":null`, `"target_entity":null`, `"inverse_def_id":null`, `"updated_at":"`} {
		if !strings.Contains(got, part) {
			t.Errorf("JSON %s lacks %s", got, part)
		}
	}
	if _, err := CreateDef(ctx, CustomFieldInput{EntityType: Company, Key: "tier", Label: "T", FieldType: "string"}); fieldErr(t, err) != `A "tier" property already exists on company.` {
		t.Errorf("duplicate: %v", err)
	}
	// Types pick the column's affinity.
	mustDef(t, ctx, CustomFieldInput{EntityType: Deal, Key: "seats", Label: "Seats", FieldType: "integer", Options: map[string]any{"min": 1.0}, Position: 3.0})
	mustDef(t, ctx, CustomFieldInput{EntityType: Deal, Key: "ratio", Label: "Ratio", FieldType: "decimal"})
	info, _ := db.Query(ctx, `PRAGMA table_info("deals")`)
	types := map[string]any{}
	for _, c := range info {
		types[c["name"].(string)] = c["type"]
	}
	if types["seats"] != "INTEGER" || types["ratio"] != "REAL" {
		t.Errorf("affinities: %v", types)
	}
	seats, _ := ListDefs(ctx, ptr(Deal))
	if len(seats) != 2 || seats[0].Key != "ratio" || seats[1].Position != 3 {
		t.Errorf("deal defs = %+v", seats)
	}
	checkJSON(t, "seats options", seats[1].Options, `{"min":1}`)
	all, _ := ListDefs(ctx, nil)
	if len(all) != 3 || all[0].EntityType != Company {
		t.Errorf("all defs = %+v", all)
	}
}

func ptr[T any](v T) *T { return &v }

func TestCustomAssertValidKey(t *testing.T) {
	cases := map[string]string{
		"Tier":   `Invalid field key "Tier" — use lowercase letters, digits, and underscores (must start with a letter).`,
		"name":   `"name" is a built-in company field and can't be used as a custom property.`,
		"1x":     `Invalid field key "1x" — use lowercase letters, digits, and underscores (must start with a letter).`,
		"ok_key": "",
		"a-b":    `Invalid field key "a-b" — use lowercase letters, digits, and underscores (must start with a letter).`,
		"id":     `"id" is a built-in company field and can't be used as a custom property.`,
		"x\n":    "Invalid field key \"x\n\" — use lowercase letters, digits, and underscores (must start with a letter).",
	}
	for key, want := range cases {
		err := AssertValidKey(Company, key)
		if want == "" {
			if err != nil {
				t.Errorf("%q: %v", key, err)
			}
			continue
		}
		if got := fieldErr(t, err); got != want {
			t.Errorf("%q: %q, want %q", key, got, want)
		}
	}
}

func TestCustomCoerceValue(t *testing.T) {
	cases := []struct {
		fieldType string
		enum      any // options.enum; nil leaves it out
		value     any
		want      any
		err       string
	}{
		{"boolean", nil, nil, nil, ""},
		{"boolean", nil, "", nil, ""},
		{"boolean", nil, 0.0, 0.0, ""},
		{"boolean", nil, -2.7, 1.0, ""},
		{"boolean", nil, "3.9", 1.0, ""},
		{"boolean", nil, "abc", 1.0, ""},
		{"boolean", nil, true, 1.0, ""},
		{"boolean", nil, false, 0.0, ""},
		{"boolean", nil, []any{5.0}, 1.0, ""},
		{"boolean", nil, map[string]any{"a": 1.0}, 1.0, ""},
		{"boolean", nil, "Zoë & <b>", 1.0, ""},
		{"boolean", nil, 1.5e-7, 1.0, ""},
		{"integer", nil, nil, nil, ""},
		{"integer", nil, "", nil, ""},
		{"integer", nil, 0.0, 0.0, ""},
		{"integer", nil, -2.7, -2.0, ""},
		{"integer", nil, "3.9", 3.0, ""},
		{"integer", nil, "abc", math.NaN(), ""},
		{"integer", nil, true, 1.0, ""},
		{"integer", nil, false, 0.0, ""},
		{"integer", nil, []any{5.0}, 5.0, ""},
		{"integer", nil, map[string]any{"a": 1.0}, math.NaN(), ""},
		{"integer", nil, "Zoë & <b>", math.NaN(), ""},
		{"integer", nil, 1.5e-7, 0.0, ""},
		{"decimal", nil, nil, nil, ""},
		{"decimal", nil, "", nil, ""},
		{"decimal", nil, 0.0, 0.0, ""},
		{"decimal", nil, -2.7, -2.7, ""},
		{"decimal", nil, "3.9", 3.9, ""},
		{"decimal", nil, "abc", math.NaN(), ""},
		{"decimal", nil, true, 1.0, ""},
		{"decimal", nil, false, 0.0, ""},
		{"decimal", nil, []any{5.0}, 5.0, ""},
		{"decimal", nil, map[string]any{"a": 1.0}, math.NaN(), ""},
		{"decimal", nil, "Zoë & <b>", math.NaN(), ""},
		{"decimal", nil, 1.5e-7, 1.5e-7, ""},
		{"json", nil, nil, nil, ""},
		{"json", nil, "", nil, ""},
		{"json", nil, 0.0, "0", ""},
		{"json", nil, -2.7, "-2.7", ""},
		{"json", nil, "3.9", "3.9", ""},
		{"json", nil, "abc", "abc", ""},
		{"json", nil, true, "true", ""},
		{"json", nil, false, "false", ""},
		{"json", nil, []any{5.0}, "[5]", ""},
		{"json", nil, map[string]any{"a": 1.0}, "{\"a\":1}", ""},
		{"json", nil, "Zoë & <b>", "Zoë & <b>", ""},
		{"json", nil, 1.5e-7, "1.5e-7", ""},
		{"string", nil, nil, nil, ""},
		{"string", nil, "", nil, ""},
		{"string", nil, 0.0, "0", ""},
		{"string", nil, -2.7, "-2.7", ""},
		{"string", nil, "3.9", "3.9", ""},
		{"string", nil, "abc", "abc", ""},
		{"string", nil, true, "true", ""},
		{"string", nil, false, "false", ""},
		{"string", nil, []any{5.0}, "5", ""},
		{"string", nil, map[string]any{"a": 1.0}, "[object Object]", ""},
		{"string", nil, "Zoë & <b>", "Zoë & <b>", ""},
		{"string", nil, 1.5e-7, "1.5e-7", ""},
		{"enumeration", []any{"a", "b"}, "a", "a", ""},
		{"enumeration", []any{"a", "b"}, "c", nil, "k: \"c\" is not one of [a, b]"},
		{"enumeration", []any{"a", "b"}, 1.0, nil, "k: \"1\" is not one of [a, b]"},
		{"enumeration", []any{"a", "b"}, "null", nil, "k: \"null\" is not one of [a, b]"},
		{"enumeration", []any{"a", "b"}, nil, nil, ""},
		{"enumeration", []any{"a", "b"}, []any{"a"}, "a", ""},
		{"enumeration", []any{"a", "b"}, true, nil, "k: \"true\" is not one of [a, b]"},
		{"enumeration", []any{"a", 1.0, nil, []any{"x", "y"}, map[string]any{"o": 1.0}}, "a", "a", ""},
		{"enumeration", []any{"a", 1.0, nil, []any{"x", "y"}, map[string]any{"o": 1.0}}, "c", nil, "k: \"c\" is not one of [a, 1, , x,y, [object Object]]"},
		{"enumeration", []any{"a", 1.0, nil, []any{"x", "y"}, map[string]any{"o": 1.0}}, 1.0, "1", ""},
		{"enumeration", []any{"a", 1.0, nil, []any{"x", "y"}, map[string]any{"o": 1.0}}, "null", "null", ""},
		{"enumeration", []any{"a", 1.0, nil, []any{"x", "y"}, map[string]any{"o": 1.0}}, nil, nil, ""},
		{"enumeration", []any{"a", 1.0, nil, []any{"x", "y"}, map[string]any{"o": 1.0}}, []any{"a"}, "a", ""},
		{"enumeration", []any{"a", 1.0, nil, []any{"x", "y"}, map[string]any{"o": 1.0}}, true, nil, "k: \"true\" is not one of [a, 1, , x,y, [object Object]]"},
		{"enumeration", []any{nil}, "a", nil, "k: \"a\" is not one of []"},
		{"enumeration", []any{nil}, "c", nil, "k: \"c\" is not one of []"},
		{"enumeration", []any{nil}, 1.0, nil, "k: \"1\" is not one of []"},
		{"enumeration", []any{nil}, "null", "null", ""},
		{"enumeration", []any{nil}, nil, nil, ""},
		{"enumeration", []any{nil}, []any{"a"}, nil, "k: \"a\" is not one of []"},
		{"enumeration", []any{nil}, true, nil, "k: \"true\" is not one of []"},
	}
	for _, c := range cases {
		def := CustomFieldDef{Key: "k", FieldType: c.fieldType, Options: map[string]any{}}
		if c.enum != nil {
			def.Options = map[string]any{"enum": c.enum}
		}
		got, err := CoerceCustomValue(c.value, def)
		if c.err != "" {
			if err == nil || fieldErr(t, err) != c.err {
				t.Errorf("%s %#v: err %v, want %q", c.fieldType, c.value, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s %#v: %v", c.fieldType, c.value, err)
			continue
		}
		if w, ok := c.want.(float64); ok && math.IsNaN(w) {
			if g, ok := got.(float64); !ok || !math.IsNaN(g) {
				t.Errorf("%s %#v = %#v, want NaN", c.fieldType, c.value, got)
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %#v = %#v, want %#v", c.fieldType, c.value, got, c.want)
		}
	}
	// The brief's case: a select-style field rejects a value outside its options.
	tier := CustomFieldDef{Key: "tier", FieldType: "enumeration", Options: map[string]any{"enum": []any{"a", "b"}}}
	if _, err := CoerceCustomValue("c", tier); fieldErr(t, err) != `tier: "c" is not one of [a, b]` {
		t.Errorf("enum: %v", err)
	}
	// Import is lenient: a bad cell is null.
	imports := []struct {
		fieldType string
		options   map[string]any
		value     any
		want      any
	}{
		{"integer", map[string]any{}, "abc", nil},
		{"integer", map[string]any{}, "12", 12.0},
		{"decimal", map[string]any{}, "x", nil},
		{"enumeration", map[string]any{"enum": []any{"a"}}, "c", nil},
		{"enumeration", map[string]any{"enum": []any{"a"}}, "a", "a"},
		{"string", map[string]any{}, nil, nil},
		{"boolean", map[string]any{}, "no", 1.0},
	}
	for _, c := range imports {
		if got := CoerceForImport(c.value, CustomFieldDef{Key: "k", FieldType: c.fieldType, Options: c.options}); !reflect.DeepEqual(got, c.want) {
			t.Errorf("CoerceForImport(%s, %#v) = %#v, want %#v", c.fieldType, c.value, got, c.want)
		}
	}
}

func TestCustomClassifyWrite(t *testing.T) {
	ctx := dbtest.Setup(t)
	mustDef(t, ctx, CustomFieldInput{EntityType: Company, Key: "tier", Label: "Tier", FieldType: "enumeration", Options: map[string]any{"enum": []any{"a", "b"}, "required": true}})
	values, unknown, err := ClassifyCustomWrite(ctx, Company, map[string]any{"name": "x", "tier": "a", "bogus": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values, map[string]any{"tier": "a"}) || !reflect.DeepEqual(unknown, []string{"bogus"}) {
		t.Errorf("values %v unknown %v", values, unknown)
	}

	// Unknown keys are listed in the order TS saw them: array-index keys
	// first, then the body's keys as sent, then the custom bag's new ones.
	// __proto__ never becomes a key (zod drops it; assigning it sets the prototype).
	raw := `{"zeta":1,"name":"x","alpha":2,"custom":{"tier":"b","10":3,"beta":4,"zeta":9},"2":5,"__proto__":{"q":1}}`
	body := map[string]any{"zeta": 1.0, "name": "x", "alpha": 2.0, "custom": map[string]any{"tier": "b", "10": 3.0, "beta": 4.0, "zeta": 9.0}, "2": 5.0, "__proto__": map[string]any{"q": 1.0}}
	values, unknown, err = ResolveCustomWrite(ctx, Company, body, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values, map[string]any{"tier": "b"}) || !reflect.DeepEqual(unknown, []string{"2", "10", "zeta", "alpha", "beta"}) {
		t.Errorf("resolve: values %v unknown %q", values, unknown)
	}
	// Without the raw body the order is the JS order of the sorted keys.
	_, unknown, _ = ResolveCustomWrite(ctx, Company, body, "")
	if !reflect.DeepEqual(unknown, []string{"2", "10", "alpha", "zeta", "beta"}) {
		t.Errorf("resolve without raw: %q", unknown)
	}

	errBody, err := UnknownFieldsError(ctx, Company, []string{"bogus", "zeta"})
	if err != nil {
		t.Fatal(err)
	}
	checkJSON(t, "422", errBody, `{"error":"Unknown field(s) for company: bogus, zeta. Send a base field or a registered custom field, or define it first via POST /api/custom-fields.","unknown_fields":["bogus","zeta"],"valid_fields":["name","domain","industry","phone","email","notes","customer_since","renewal_date","main_contact_id","main_partner_id","tier"]}`)
	if clean, err := UnknownFieldsError(ctx, Company, nil); clean != nil || err != nil {
		t.Errorf("clean write: %v %v", clean, err)
	}

	missing := func(values map[string]any, mode string) []string {
		got, err := MissingRequiredCustom(ctx, Company, values, mode)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := missing(map[string]any{}, "create"); !reflect.DeepEqual(got, []string{"Tier"}) {
		t.Errorf("create {}: %v", got)
	}
	if got := missing(map[string]any{"tier": ""}, "create"); !reflect.DeepEqual(got, []string{"Tier"}) {
		t.Errorf("create empty: %v", got)
	}
	if got := missing(map[string]any{"tier": "a"}, "create"); len(got) != 0 {
		t.Errorf("create set: %v", got)
	}
	if got := missing(map[string]any{}, "update"); len(got) != 0 {
		t.Errorf("update absent: %v", got)
	}
	if got := missing(map[string]any{"tier": nil}, "update"); !reflect.DeepEqual(got, []string{"Tier"}) {
		t.Errorf("update null: %v", got)
	}
	for entity, want := range map[EntityType]string{
		Contact:      `["first_name","last_name","email","phone","company_id","title","status","last_contacted_at"]`,
		Deal:         `["name","contact_id","company_id","value","recurring_value","stage","close_date","notes","owner_id","partner_ids"]`,
		Partner:      `["name","industry","status","phone","email","notes"]`,
		CustomerUser: `["name","email","phone","company_id","notes"]`,
	} {
		keys, err := WritableFieldKeys(ctx, entity)
		if err != nil {
			t.Fatal(err)
		}
		checkJSON(t, string(entity), keys, want)
	}
}

func TestCustomCreateRelation(t *testing.T) {
	ctx := dbtest.Setup(t)
	mustDef(t, ctx, CustomFieldInput{EntityType: Company, Key: "tier", Label: "Tier", FieldType: "string"})
	bad := []struct {
		in   RelationInput
		want string
	}{
		{RelationInput{EntityType: Contact, Key: "partner", Label: "P", RelationType: "many_to_one", TargetEntity: Company, InverseKey: "partner_contacts", InverseLabel: "PC"}, `"partner" holds the linked record's id, so its key must end in _id.`},
		{RelationInput{EntityType: Contact, Key: "partner_id", Label: "P", RelationType: "sideways", TargetEntity: Company, InverseKey: "x", InverseLabel: "X"}, `relation_type must be "many_to_one" or "one_to_many".`},
		{RelationInput{EntityType: Contact, Key: "partner_id", Label: "P", RelationType: "many_to_one", TargetEntity: "user", InverseKey: "x", InverseLabel: "X"}, `Invalid target_entity "user".`},
		{RelationInput{EntityType: Company, Key: "parent_id", Label: "P", RelationType: "many_to_one", TargetEntity: Company, InverseKey: "parent_id", InverseLabel: "X"}, `The two sides of a relation need different keys.`},
		{RelationInput{EntityType: Company, Key: "kids", Label: "K", RelationType: "one_to_many", TargetEntity: Company, InverseKey: "kid", InverseLabel: "X"}, `"kid" holds the linked record's id, so its key must end in _id.`},
		{RelationInput{EntityType: Company, Key: "tier", Label: "K", RelationType: "many_to_one", TargetEntity: Deal, InverseKey: "tier_id", InverseLabel: "X"}, `"tier" holds the linked record's id, so its key must end in _id.`},
		{RelationInput{EntityType: Company, Key: "tier_id", Label: "K", RelationType: "one_to_many", TargetEntity: Deal, InverseKey: "Bad_id", InverseLabel: "X"}, `Invalid field key "Bad_id" — use lowercase letters, digits, and underscores (must start with a letter).`},
		{RelationInput{EntityType: Deal, Key: "sponsor_id", Label: "K", RelationType: "many_to_one", TargetEntity: Company, InverseKey: "tier", InverseLabel: "X"}, `A "tier" property already exists on company.`},
	}
	for _, c := range bad {
		if _, err := CreateRelation(ctx, c.in); fieldErr(t, err) != c.want {
			t.Errorf("%+v: %v, want %q", c.in, err, c.want)
		}
	}
	def, err := CreateRelation(ctx, RelationInput{EntityType: Contact, Key: "partner_company_id", Label: "Partner", RelationType: "many_to_one", TargetEntity: Company, InverseKey: "partner_contacts", InverseLabel: "Partner contacts"})
	if err != nil {
		t.Fatal(err)
	}
	if def.FieldType != "relation" || *def.RelationType != "many_to_one" || *def.TargetEntity != Company || def.InverseDefID == nil {
		t.Fatalf("def = %+v", def)
	}
	inverse, err := GetDef(ctx, *def.InverseDefID)
	if err != nil || inverse == nil {
		t.Fatal(inverse, err)
	}
	// The inverse goes after the target's existing defs, and has no column.
	if inverse.Key != "partner_contacts" || inverse.Position != 1 || *inverse.RelationType != "one_to_many" || *inverse.InverseDefID != def.ID || HasColumn(*inverse) {
		t.Errorf("inverse = %+v", inverse)
	}
	contactCols, _ := TableColumns(ctx, "contacts")
	companyCols, _ := TableColumns(ctx, "companies")
	if !contactCols["partner_company_id"] || companyCols["partner_contacts"] {
		t.Errorf("columns: contacts %v companies %v", contactCols, companyCols)
	}
	if idx, _ := db.Get(ctx, "SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_contacts_partner_company_id'"); idx == nil {
		t.Error("no index on the relation column")
	}
	// A failed column add undoes both defs.
	if _, err := db.Run(ctx, `ALTER TABLE deals ADD COLUMN "steward_id" TEXT`); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateRelation(ctx, RelationInput{EntityType: Deal, Key: "steward_id", Label: "Steward", RelationType: "many_to_one", TargetEntity: Contact, InverseKey: "stewarded_deals", InverseLabel: "Stewarded"}); err == nil {
		t.Error("duplicate column: no error")
	}
	if n, _ := db.Get(ctx, "SELECT COUNT(*) AS n FROM custom_field_defs WHERE key IN ('steward_id', 'stewarded_deals')"); n["n"] != 0.0 {
		t.Errorf("defs left behind: %v", n)
	}

	// Deleting either side removes the relation as a whole, index and column too.
	ok, err := DeleteDef(ctx, inverse.ID)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if n, _ := db.Get(ctx, "SELECT COUNT(*) AS n FROM custom_field_defs WHERE field_type = 'relation'"); n["n"] != 0.0 {
		t.Errorf("relation defs left: %v", n)
	}
	contactCols, _ = TableColumns(ctx, "contacts")
	if contactCols["partner_company_id"] {
		t.Error("relation column left behind")
	}
	if ok, err := DeleteDef(ctx, "nope"); ok || err != nil {
		t.Errorf("unknown id: %v %v", ok, err)
	}
}

func TestCustomUpdateAndSync(t *testing.T) {
	ctx := dbtest.Setup(t)
	def := mustDef(t, ctx, CustomFieldInput{EntityType: Contact, Key: "score", Label: "Score", FieldType: "integer", CustomField: "clawnify::score.score", Options: map[string]any{"max": 10.0}})
	got, err := UpdateDef(ctx, def.ID, map[string]any{"label": "Rating", "options": map[string]any{"max": 5.0, "note": "<b>&"}, "position": nil})
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "Rating" || got.CustomField != "clawnify::score.score" || got.Position != 0 || got.FieldType != "integer" {
		t.Errorf("updated = %+v", got)
	}
	stored, _ := db.Get(ctx, "SELECT options FROM custom_field_defs WHERE id = ?", def.ID)
	if stored["options"] != `{"max":5,"note":"<b>&"}` {
		t.Errorf("stored options = %v", stored["options"])
	}
	if got, err := UpdateDef(ctx, "nope", map[string]any{"label": "x"}); got != nil || err != nil {
		t.Errorf("unknown id: %v %v", got, err)
	}
	// A bad stored options string reads as {}; an array is kept.
	if _, err := db.Run(ctx, "UPDATE custom_field_defs SET options = 'nope' WHERE id = ?", def.ID); err != nil {
		t.Fatal(err)
	}
	if d, _ := GetDef(ctx, def.ID); jsonOf(t, d.Options) != `{}` {
		t.Errorf("bad options = %v", d.Options)
	}
	if _, err := UpdateDef(ctx, def.ID, map[string]any{"options": []any{1.0}}); err != nil {
		t.Fatal(err)
	}
	if d, _ := GetDef(ctx, def.ID); jsonOf(t, d.Options) != `[1]` || d.Option("required") != nil {
		t.Errorf("array options = %v", d.Options)
	}

	// Schema-sync adds the column a def lacks, and an index for a many_to_one.
	if _, err := db.Run(ctx, "INSERT INTO custom_field_defs (id, entity_type, key, label, field_type, relation_type, target_entity) VALUES ('x1', 'contact', 'mentor_id', 'Mentor', 'relation', 'many_to_one', 'contact'), ('x2', 'contact', 'flag', 'Flag', 'boolean', NULL, NULL), ('x3', 'contact', 'email', 'Email', 'string', NULL, NULL)"); err != nil {
		t.Fatal(err)
	}
	if err := SyncEntityColumns(ctx, Contact); err != nil {
		t.Fatal(err)
	}
	if err := SyncEntityColumns(ctx, Contact); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	cols, _ := TableColumns(ctx, "contacts")
	if !cols["mentor_id"] || !cols["flag"] || !cols["score"] {
		t.Errorf("columns = %v", cols)
	}
	if idx, _ := db.Get(ctx, "SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_contacts_mentor_id'"); idx == nil {
		t.Error("no index for the synced relation column")
	}
	// Deleting a def drops its column; a built-in key is never dropped.
	if ok, err := DeleteDef(ctx, "x3"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := DeleteDef(ctx, def.ID); !ok || err != nil {
		t.Fatal(ok, err)
	}
	cols, _ = TableColumns(ctx, "contacts")
	if !cols["email"] || cols["score"] {
		t.Errorf("after delete: %v", cols)
	}
}

func TestCustomApplyValues(t *testing.T) {
	ctx := dbtest.Setup(t)
	mustDef(t, ctx, CustomFieldInput{EntityType: Company, Key: "seats", Label: "Seats", FieldType: "integer"})
	mustDef(t, ctx, CustomFieldInput{EntityType: Company, Key: "tier", Label: "Tier", FieldType: "enumeration", Options: map[string]any{"enum": []any{"a", "b"}}})
	mustDef(t, ctx, CustomFieldInput{EntityType: Company, Key: "meta", Label: "Meta", FieldType: "json"})
	if _, err := CreateRelation(ctx, RelationInput{EntityType: Contact, Key: "partner_company_id", Label: "P", RelationType: "many_to_one", TargetEntity: Company, InverseKey: "partner_contacts", InverseLabel: "PC"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Run(ctx, "INSERT INTO companies (id, name) VALUES ('c1', 'Acme')"); err != nil {
		t.Fatal(err)
	}
	err := ApplyCustomValues(ctx, Company, "companies", "c1", map[string]any{"seats": "12.9", "tier": "b", "meta": map[string]any{"a": "<&>"}, "partner_contacts": "x", "bogus": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	row, _ := db.Get(ctx, "SELECT seats, tier, meta FROM companies WHERE id = 'c1'")
	checkJSON(t, "row", row, `{"meta":"{\"a\":\"<&>\"}","seats":12,"tier":"b"}`)
	if err := ApplyCustomValues(ctx, Company, "companies", "c1", map[string]any{"tier": "z"}); fieldErr(t, err) != `tier: "z" is not one of [a, b]` {
		t.Errorf("enum: %v", err)
	}
	if err := ApplyCustomValues(ctx, Company, "companies", "c1", nil); err != nil {
		t.Error(err)
	}

	keys, defs, err := ResolveImportCustomColumns(ctx, Company, []map[string]any{nil, {"seats": "", "bogus": 1.0}, {"tier": "a", "seats": 3.0}, {"partner_contacts": "x", "meta": nil}})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys) // the order is the INSERT's column order, which nothing reads back
	if !reflect.DeepEqual(keys, []string{"seats", "tier"}) || len(defs) != 3 || defs["seats"].FieldType != "integer" {
		t.Errorf("import columns: %v %v", keys, defs)
	}
	if _, ok := defs["partner_contacts"]; ok {
		t.Error("relations are not imported")
	}
}
