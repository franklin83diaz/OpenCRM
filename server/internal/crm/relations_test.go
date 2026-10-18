package crm

import (
	"context"
	"testing"

	"opencrm/internal/db"
	"opencrm/internal/db/dbtest"
)

// Expected values below come from running the TS functions (relations.ts at
// 0068887) on the same data under node.

// partnerFixture: contacts link to a partner company (many_to_one), which
// lists its partner contacts (one_to_many).
func partnerFixture(t *testing.T) context.Context {
	t.Helper()
	ctx := dbtest.Setup(t)
	mustDef(t, ctx, CustomFieldInput{EntityType: Company, Key: "tier", Label: "Tier", FieldType: "string"})
	if _, err := CreateRelation(ctx, RelationInput{EntityType: Contact, Key: "partner_company_id", Label: "Partner", RelationType: "many_to_one", TargetEntity: Company, InverseKey: "partner_contacts", InverseLabel: "Partner contacts"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"INSERT INTO companies (id, name, domain) VALUES ('c1', 'Acme', 'acme.com'), ('c2', 'Beta', '')",
		"INSERT INTO contacts (id, first_name, last_name, partner_company_id) VALUES ('p1', 'Ann', 'Lee', 'c1'), ('p2', 'bob', '', 'c1'), ('p3', 'Cy', NULL, NULL), ('p4', 'Di', 'X', 'gone')",
	} {
		if _, err := db.Run(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return ctx
}

func TestRelationWithRelations(t *testing.T) {
	ctx := partnerFixture(t)
	contacts, _ := db.Query(ctx, "SELECT id, first_name, partner_company_id FROM contacts ORDER BY id")
	got, err := WithRelations(ctx, Contact, contacts, 3)
	if err != nil {
		t.Fatal(err)
	}
	checkJSON(t, "many_to_one", got, `[{"first_name":"Ann","id":"p1","partner_company_id":"c1","relations":{"partner_company_id":{"id":"c1","label":"Acme","domain":"acme.com"}}},{"first_name":"bob","id":"p2","partner_company_id":"c1","relations":{"partner_company_id":{"id":"c1","label":"Acme","domain":"acme.com"}}},{"first_name":"Cy","id":"p3","partner_company_id":null,"relations":{"partner_company_id":null}},{"first_name":"Di","id":"p4","partner_company_id":"gone","relations":{"partner_company_id":null}}]`)
	if _, touched := contacts[0]["relations"]; touched {
		t.Error("the input rows were changed")
	}

	companies, _ := db.Query(ctx, "SELECT id, name FROM companies ORDER BY id")
	got, err = WithRelations(ctx, Company, companies, 1)
	if err != nil {
		t.Fatal(err)
	}
	checkJSON(t, "one_to_many", got, `[{"id":"c1","name":"Acme","relations":{"partner_contacts":{"items":[{"id":"p1","label":"Ann Lee","domain":null}],"total":2}}},{"id":"c2","name":"Beta","relations":{"partner_contacts":{"items":[],"total":0}}}]`)

	// No relation on deals: rows come back as they were, without `relations`.
	got, _ = WithRelations(ctx, Deal, []db.Row{{"id": "d1"}}, 3)
	checkJSON(t, "none", got, `[{"id":"d1"}]`)

	one, err := WithRelationsOne(ctx, Contact, contacts[0])
	if err != nil {
		t.Fatal(err)
	}
	checkJSON(t, "one", one["relations"], `{"partner_company_id":{"id":"c1","label":"Acme","domain":"acme.com"}}`)
	if none, err := WithRelationsOne(ctx, Contact, nil); none != nil || err != nil {
		t.Errorf("nil row: %v %v", none, err)
	}
}

func TestRelationReadsAndWrites(t *testing.T) {
	ctx := partnerFixture(t)
	found, err := SearchRecords(ctx, Contact, " b ", nil)
	if err != nil {
		t.Fatal(err)
	}
	checkJSON(t, "search", found, `[{"id":"p2","label":"bob","domain":null}]`)
	found, _ = SearchRecords(ctx, Company, "", []string{"c2", "c1"})
	checkJSON(t, "by ids", found, `[{"id":"c1","label":"Acme","domain":"acme.com"},{"id":"c2","label":"Beta","domain":""}]`)
	found, _ = SearchRecords(ctx, Deal, "", nil)
	checkJSON(t, "empty", found, `[]`)

	writes := []struct {
		entity EntityType
		values map[string]any
		want   string
	}{
		{Contact, map[string]any{"partner_company_id": 5.0}, "partner_company_id takes a company id, or null to clear it."},
		{Contact, map[string]any{"partner_company_id": "nope"}, `partner_company_id: no company with id "nope".`},
		{Contact, map[string]any{"partner_company_id": "c1"}, ""},
		{Contact, map[string]any{"partner_company_id": ""}, ""},
		{Company, map[string]any{"partner_contacts": []any{"p1"}}, "partner_contacts lists linked contacts and can't be written here. Set partner_company_id on each contact instead."},
	}
	for _, w := range writes {
		got, err := RelationWriteError(ctx, w.entity, w.values)
		if err != nil || got != w.want {
			t.Errorf("RelationWriteError(%v) = %q %v, want %q", w.values, got, err, w.want)
		}
	}

	for entity, want := range map[EntityType]string{
		Company: `{"contacts":{"table":"contacts","fk":"company_id"},"customer_users":{"table":"customer_users","fk":"company_id"},"deals":{"table":"deals","fk":"company_id"},"partner_contacts":{"table":"contacts","fk":"partner_company_id"}}`,
		Contact: `{"deals":{"table":"deals","fk":"contact_id"}}`,
		Deal:    `{"partners":{"table":"deal_partners","fk":"deal_id","id":"partner_id"}}`,
		Partner: `{"deals":{"table":"deal_partners","fk":"partner_id","id":"deal_id"}}`,
	} {
		links, err := ManyLinks(ctx, entity)
		if err != nil {
			t.Fatal(err)
		}
		checkJSON(t, "ManyLinks "+string(entity), links, want)
	}

	sorts := []struct {
		entity        EntityType
		alias, column string
		want          string
	}{
		{Contact, "ct", "partner_company_id", `(SELECT r.name FROM companies r WHERE r.id = ct."partner_company_id") COLLATE NOCASE`},
		{Deal, "d", "contact_id", `(SELECT TRIM(COALESCE(r.first_name, '') || ' ' || COALESCE(r.last_name, '')) FROM contacts r WHERE r.id = d."contact_id") COLLATE NOCASE`},
		{Company, "c", "name", ""},
	}
	for _, s := range sorts {
		got, err := RelationSortSQL(ctx, s.entity, s.alias, s.column)
		if err != nil || got != s.want {
			t.Errorf("RelationSortSQL(%s, %s) = %q %v, want %q", s.entity, s.column, got, err, s.want)
		}
	}

	if c, ok := CountOf("count:contacts"); !ok || c != "contacts" {
		t.Errorf("CountOf = %q %v", c, ok)
	}
	if _, ok := CountOf("contacts"); ok {
		t.Error("CountOf of a plain field")
	}

	if err := DetachRelations(ctx, Company, []string{"c1"}); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Query(ctx, "SELECT id, partner_company_id FROM contacts ORDER BY id")
	checkJSON(t, "detached", rows, `[{"id":"p1","partner_company_id":null},{"id":"p2","partner_company_id":null},{"id":"p3","partner_company_id":null},{"id":"p4","partner_company_id":"gone"}]`)
}
