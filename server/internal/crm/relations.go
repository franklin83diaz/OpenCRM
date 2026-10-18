package crm

// Relations: reading and keeping links between records.
//
// A relation is a pair of custom-field defs (see CreateRelation in
// custom_fields.go). The many_to_one side is a column holding the linked
// record's id; the one_to_many side is that column read from the other end.
// This file names records for chips and pickers, attaches linked records to
// rows read from the API, validates writes, sorts by a linked record's name,
// and clears links when records are deleted.

import (
	"context"
	"fmt"
	"strings"

	"opencrm/internal/db"
	"opencrm/internal/js"
)

// RelationRecord is a linked record as a chip shows it. Domain is a
// company's, for its logo; null for the others.
type RelationRecord struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Domain *string `json:"domain"`
}

// RelationList is a one_to_many side on a row: the first linked records and
// how many there are.
type RelationList struct {
	Items []RelationRecord `json:"items"`
	Total float64          `json:"total"`
}

// recordLabel is a record's name, as SQL over table alias a.
func recordLabel(entity EntityType, a string) string {
	if entity == Contact {
		return fmt.Sprintf("TRIM(COALESCE(%s.first_name, '') || ' ' || COALESCE(%s.last_name, ''))", a, a)
	}
	return a + ".name"
}

func recordColumns(entity EntityType, a string) string {
	domain := "NULL"
	if entity == Company {
		domain = a + ".domain"
	}
	return fmt.Sprintf("%s.id AS id, %s AS label, %s AS domain", a, recordLabel(entity, a), domain)
}

func toRecord(row db.Row) RelationRecord {
	return RelationRecord{ID: colText(row["id"]), Label: colText(row["label"]), Domain: nullableText(row["domain"])}
}

func toRecords(rows []db.Row) []RelationRecord {
	out := make([]RelationRecord, len(rows))
	for i, r := range rows {
		out[i] = toRecord(r)
	}
	return out
}

// marks is n comma-separated placeholders.
func marks(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func stringParams(ids []string) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// relationDefs are the relation defs of entity (all entities when ""), with
// both a target and a side.
func relationDefs(ctx context.Context, entity EntityType) ([]CustomFieldDef, error) {
	var which *EntityType
	if entity != "" {
		which = &entity
	}
	defs, err := ListDefs(ctx, which)
	if err != nil {
		return nil, err
	}
	out := []CustomFieldDef{}
	for _, d := range defs {
		if d.FieldType == "relation" && d.targetEntity() != "" && d.relationType() != "" {
			out = append(out, d)
		}
	}
	return out, nil
}

// SearchRecords returns records of entity for a picker: by name (search), or
// the given ids.
func SearchRecords(ctx context.Context, entity EntityType, search string, ids []string) ([]RelationRecord, error) {
	table := EntityTables[entity]
	if len(ids) > 0 {
		rows, err := db.Query(ctx, fmt.Sprintf("SELECT %s FROM %s t WHERE t.id IN (%s)", recordColumns(entity, "t"), table, marks(len(ids))), stringParams(ids)...)
		if err != nil {
			return nil, err
		}
		return toRecords(rows), nil
	}
	s := js.Trim(search)
	where := ""
	var params []any
	if s != "" {
		where = fmt.Sprintf(" WHERE %s LIKE ?", recordLabel(entity, "t"))
		params = []any{"%" + s + "%"}
	}
	rows, err := db.Query(ctx, fmt.Sprintf("SELECT %s FROM %s t%s ORDER BY label COLLATE NOCASE LIMIT 20", recordColumns(entity, "t"), table, where), params...)
	if err != nil {
		return nil, err
	}
	return toRecords(rows), nil
}

// WithRelations returns copies of rows with `relations` added: for every
// relation on entity, the linked record (many_to_one: a RelationRecord, or
// nil) or the first manyLimit linked records and their total (one_to_many: a
// RelationList). One query per relation per 90 rows. With no rows or no
// relations the rows come back as they are, without `relations` (TS's default
// manyLimit was 3).
func WithRelations(ctx context.Context, entity EntityType, rows []db.Row, manyLimit int) ([]db.Row, error) {
	defs, err := relationDefs(ctx, entity)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || len(defs) == 0 {
		return rows, nil
	}
	out := make([]db.Row, len(rows))
	rels := make([]map[string]any, len(rows))
	for i, r := range rows {
		cp := make(db.Row, len(r)+1)
		for k, v := range r {
			cp[k] = v
		}
		rels[i] = map[string]any{}
		cp["relations"] = rels[i]
		out[i] = cp
	}

	for _, def := range defs {
		target := def.targetEntity()
		table := EntityTables[target]
		if def.relationType() == ManyToOne {
			seen := map[string]bool{}
			ids := []string{}
			for _, r := range rows {
				if id, ok := r[def.Key].(string); ok && id != "" && !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
			found := map[string]RelationRecord{}
			for _, part := range Chunk(ids, 90) {
				recs, err := db.Query(ctx, fmt.Sprintf("SELECT %s FROM %s t WHERE t.id IN (%s)", recordColumns(target, "t"), table, marks(len(part))), stringParams(part)...)
				if err != nil {
					return nil, err
				}
				for _, rec := range recs {
					found[colText(rec["id"])] = toRecord(rec)
				}
			}
			for i, r := range out {
				var linked any // null when unset or gone
				if id, ok := r[def.Key].(string); ok {
					if rec, ok := found[id]; ok {
						linked = rec
					}
				}
				rels[i][def.Key] = linked
			}
			continue
		}

		if def.InverseDefID == nil || *def.InverseDefID == "" {
			continue
		}
		inverse, err := GetDef(ctx, *def.InverseDefID)
		if err != nil {
			return nil, err
		}
		if inverse == nil {
			continue
		}
		col := "t." + QuoteIdent(inverse.Key)
		byParent := map[string]*RelationList{}
		parents := make([]string, len(out))
		for i, r := range out {
			parents[i] = js.String(r["id"])
		}
		for _, part := range Chunk(parents, 89) {
			found, err := db.Query(ctx, fmt.Sprintf(`SELECT id, label, domain, parent, total FROM (
           SELECT %s, %s AS parent,
                  ROW_NUMBER() OVER (PARTITION BY %s ORDER BY %s COLLATE NOCASE) AS n,
                  COUNT(*) OVER (PARTITION BY %s) AS total
           FROM %s t WHERE %s IN (%s)
         ) WHERE n <= ?`, recordColumns(target, "t"), col, col, recordLabel(target, "t"), col, table, col, marks(len(part))),
				append(stringParams(part), float64(manyLimit))...)
			if err != nil {
				return nil, err
			}
			for _, rec := range found {
				parent, ok := rec["parent"].(string)
				if !ok {
					continue // the column is TEXT: a parent is always a string
				}
				entry := byParent[parent]
				if entry == nil {
					entry = &RelationList{Items: []RelationRecord{}, Total: js.Number(rec["total"])}
					byParent[parent] = entry
				}
				entry.Items = append(entry.Items, toRecord(rec))
			}
		}
		for i, r := range out {
			if entry := byParent[js.String(r["id"])]; entry != nil {
				rels[i][def.Key] = *entry
			} else {
				rels[i][def.Key] = RelationList{Items: []RelationRecord{}, Total: 0}
			}
		}
	}
	return out, nil
}

// RelationWriteError says why a write's relation values can't be saved, or
// "" when they can. A many_to_one value must be an existing record's id (null
// or "" clears it). A one_to_many side is not written here: link a record by
// setting the other side on it.
func RelationWriteError(ctx context.Context, entity EntityType, values map[string]any) (string, error) {
	defs, err := relationDefs(ctx, entity)
	if err != nil {
		return "", err
	}
	for _, def := range defs {
		v := values[def.Key]
		// An empty value is no write at all (a form sends every field it shows).
		if v == nil || v == "" {
			continue
		}
		target := def.targetEntity()
		if def.relationType() == OneToMany {
			other := "the other side"
			if def.InverseDefID != nil && *def.InverseDefID != "" {
				inverse, err := GetDef(ctx, *def.InverseDefID)
				if err != nil {
					return "", err
				}
				if inverse != nil {
					other = inverse.Key
				}
			}
			return fmt.Sprintf("%s lists linked %s and can't be written here. Set %s on each %s instead.", def.Key, EntityTables[target], other, target), nil
		}
		id, ok := v.(string)
		if !ok {
			return fmt.Sprintf("%s takes a %s id, or null to clear it.", def.Key, target), nil
		}
		found, err := db.Get(ctx, fmt.Sprintf("SELECT id FROM %s WHERE id = ?", EntityTables[target]), id)
		if err != nil {
			return "", err
		}
		if found == nil {
			return fmt.Sprintf(`%s: no %s with id "%s".`, def.Key, target, id), nil
		}
	}
	return "", nil
}

// ManyLink is a one_to_many side as a filter can reach it: the linked table
// and the column there that points back. ID is the column there holding the
// linked record's id: "" is the table's own id, and a link table (a deal's
// partners) names the other side's column.
type ManyLink struct {
	Table string `json:"table"`
	FK    string `json:"fk"`
	ID    string `json:"id,omitempty"`
}

// idColumn is the column of link.Table holding the linked record's id.
func (link ManyLink) idColumn() string {
	if link.ID == "" {
		return "id"
	}
	return link.ID
}

// CountSQL is how many records link back to the row named by prefix ("c.").
func CountSQL(link ManyLink, prefix string) string {
	return fmt.Sprintf("(SELECT COUNT(*) FROM %s m WHERE m.%s = %sid)", link.Table, QuoteIdent(link.FK), prefix)
}

// CountOf is a count field's many side: "count:contacts" → "contacts", true.
// false for any other field. ("count:" gives "", true — no many side is named
// "", so callers looking it up find nothing, as TS's falsy "" did.)
func CountOf(field string) (string, bool) {
	if rest, ok := strings.CutPrefix(field, "count:"); ok {
		return rest, true
	}
	return "", false
}

// builtinMany are the built-in one_to_many sides: a company's contacts and
// deals, a contact's deals, and both ends of deal_partners.
var builtinMany = map[EntityType]map[string]ManyLink{
	Company: {"contacts": {Table: "contacts", FK: "company_id"}, "deals": {Table: "deals", FK: "company_id"}},
	Contact: {"deals": {Table: "deals", FK: "contact_id"}},
	Deal:    {"partners": {Table: "deal_partners", FK: "deal_id", ID: "partner_id"}},
	Partner: {"deals": {Table: "deal_partners", FK: "partner_id", ID: "deal_id"}},
}

// ManyLinks returns the one_to_many sides of entity, built-in and custom, by
// key, for filters ("Referrals is Nora").
func ManyLinks(ctx context.Context, entity EntityType) (map[string]ManyLink, error) {
	out := map[string]ManyLink{}
	for k, v := range builtinMany[entity] {
		out[k] = v
	}
	defs, err := relationDefs(ctx, entity)
	if err != nil {
		return nil, err
	}
	for _, def := range defs {
		if def.relationType() != OneToMany || def.InverseDefID == nil || *def.InverseDefID == "" {
			continue
		}
		inverse, err := GetDef(ctx, *def.InverseDefID)
		if err != nil {
			return nil, err
		}
		if inverse != nil {
			out[def.Key] = ManyLink{Table: EntityTables[def.targetEntity()], FK: inverse.Key}
		}
	}
	return out, nil
}

// builtinLinks are the built-in links, which predate relation defs: column →
// the entity it holds.
var builtinLinks = map[EntityType]map[string]EntityType{
	Contact: {"company_id": Company},
	Deal:    {"contact_id": Contact, "company_id": Company},
}

// RelationSortSQL is the ORDER BY for a column holding a linked record's id (a
// many_to_one relation, or the built-in company_id / contact_id): the linked
// record's name. "" for any other column.
func RelationSortSQL(ctx context.Context, entity EntityType, alias, column string) (string, error) {
	target, ok := builtinLinks[entity][column]
	if !ok {
		defs, err := relationDefs(ctx, entity)
		if err != nil {
			return "", err
		}
		for _, d := range defs {
			if d.Key == column && d.relationType() == ManyToOne {
				target = d.targetEntity()
				break
			}
		}
	}
	if target == "" {
		return "", nil
	}
	return fmt.Sprintf("(SELECT %s FROM %s r WHERE r.id = %s.%s) COLLATE NOCASE", recordLabel(target, "r"), EntityTables[target], alias, QuoteIdent(column)), nil
}

// DetachRelations clears links to deleted records of entity. The built-in
// company_id and contact_id are cleared by their foreign keys; custom relation
// columns have none (see CreateRelation), so they are cleared here.
func DetachRelations(ctx context.Context, entity EntityType, ids []string) error {
	defs, err := relationDefs(ctx, "")
	if err != nil {
		return err
	}
	for _, def := range defs {
		if def.relationType() != ManyToOne || def.targetEntity() != entity {
			continue
		}
		col := QuoteIdent(def.Key)
		for _, part := range Chunk(ids, 90) {
			if _, err := db.Run(ctx, fmt.Sprintf("UPDATE %s SET %s = NULL WHERE %s IN (%s)", EntityTables[def.EntityType], col, col, marks(len(part))), stringParams(part)...); err != nil {
				return err
			}
		}
	}
	return nil
}
