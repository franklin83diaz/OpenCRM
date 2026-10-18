package crm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Partners and a deal's partners. They came after the port, so these are the
// Go server's own expectations: there is no TS answer to replay.

// partnersCall is one request; out, when not nil, takes the decoded body.
func partnersCall(t *testing.T, api http.Handler, method, path, body string, wantStatus int, out any) string {
	t.Helper()
	status, raw := companiesRequest(t, api, method, path, body)
	if status != wantStatus {
		t.Fatalf("%s %s %s: %d %s, want %d", method, path, body, status, raw, wantStatus)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(raw), out); err != nil {
			t.Fatalf("%s %s: %v in %s", method, path, err, raw)
		}
	}
	return raw
}

type partnerJSON struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Domain    string  `json:"domain"`
	DealCount float64 `json:"deal_count"`
	UpdatedAt string  `json:"updated_at"`
}

type dealJSON struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	UpdatedAt string           `json:"updated_at"`
	Partners  []RelationRecord `json:"partners"`
}

func partnerLabels(d dealJSON) string {
	out := []string{}
	for _, p := range d.Partners {
		out = append(out, p.Label)
	}
	return strings.Join(out, ",")
}

func dealNames(deals []dealJSON) string {
	out := []string{}
	for _, d := range deals {
		out = append(out, d.Name)
	}
	return strings.Join(out, ",")
}

func TestPartnersCRUD(t *testing.T) {
	recordedDB(t, nil)
	api := replayAPI(&App{})

	partnersCall(t, api, "POST", "/api/partners", `{"name":"  "}`, 400, nil)
	partnersCall(t, api, "POST", "/api/partners", `{"name":"Nimbus","bogus":1}`, 422, nil)
	var created struct{ Partner partnerJSON }
	partnersCall(t, api, "POST", "/api/partners", `{"name":" Nimbus ","domain":"nimbus.example","email":"hi@nimbus.example"}`, 201, &created)
	if p := created.Partner; p.Name != "Nimbus" || p.Domain != "nimbus.example" || p.DealCount != 0 || p.ID == "" {
		t.Fatalf("created %+v", p)
	}
	partnersCall(t, api, "POST", "/api/partners", `{"name":"Acme Resellers"}`, 201, nil)

	var page struct {
		Partners []partnerJSON
		Total    float64
	}
	partnersCall(t, api, "GET", "/api/partners?search=nimbus", "", 200, &page)
	if page.Total != 1 || len(page.Partners) != 1 || page.Partners[0].Name != "Nimbus" {
		t.Fatalf("search: %+v", page)
	}
	partnersCall(t, api, "GET", "/api/partners?sort=name&order=asc", "", 200, &page)
	if page.Total != 2 || page.Partners[0].Name != "Acme Resellers" {
		t.Fatalf("sorted: %+v", page)
	}

	id := created.Partner.ID
	var one struct{ Partner partnerJSON }
	partnersCall(t, api, "GET", "/api/partners/"+id, "", 200, &one)
	if one.Partner.Name != "Nimbus" {
		t.Fatalf("get: %+v", one)
	}
	partnersCall(t, api, "GET", "/api/partners/nope", "", 404, nil)
	partnersCall(t, api, "PUT", "/api/partners/"+id, `{"name":""}`, 400, nil)
	partnersCall(t, api, "PUT", "/api/partners/"+id, `{}`, 400, nil)
	partnersCall(t, api, "PUT", "/api/partners/nope", `{"name":"X"}`, 404, nil)
	partnersCall(t, api, "PUT", "/api/partners/"+id, `{"name":"Nimbus Cloud","phone":" 555 "}`, 200, &one)
	if one.Partner.Name != "Nimbus Cloud" {
		t.Fatalf("update: %+v", one)
	}

	partnersCall(t, api, "DELETE", "/api/partners/"+id, "", 200, nil)
	partnersCall(t, api, "DELETE", "/api/partners/"+id, "", 404, nil)
	var deleted deletedBody
	partnersCall(t, api, "POST", "/api/partners/bulk-delete", `{"ids":["`+page.Partners[0].ID+`","`+page.Partners[0].ID+`","gone"]}`, 200, &deleted)
	if deleted.Deleted != 1 {
		t.Fatalf("bulk delete: %+v", deleted)
	}
}

func TestDealPartners(t *testing.T) {
	recordedDB(t, []string{
		`INSERT INTO partners (id, name, domain) VALUES ('pa-n', 'Nimbus', 'nimbus.example'), ('pa-a', 'Acme Resellers', ''), ('pa-z', 'Zeta', '')`,
		`INSERT INTO deals (id, name, stage, updated_at) VALUES ('d-old', 'Old deal', 'prospect', '2020-01-01 00:00:00')`,
	})
	api := replayAPI(&App{})

	// An id that names no partner refuses the whole write: no deal is made.
	partnersCall(t, api, "POST", "/api/deals", `{"name":"Ghost","partner_ids":["pa-n","nope"]}`, 400, nil)
	var page struct{ Deals []dealJSON }
	partnersCall(t, api, "GET", "/api/deals?search=Ghost", "", 200, &page)
	if len(page.Deals) != 0 {
		t.Fatalf("a refused deal was made: %+v", page.Deals)
	}
	partnersCall(t, api, "POST", "/api/deals", `{"name":"Bad","partner_ids":"pa-n"}`, 400, nil)
	partnersCall(t, api, "POST", "/api/deals", `{"name":"Bad","partners":["pa-n"]}`, 422, nil)

	// Created with partners: once each, by name.
	var made struct{ Deal dealJSON }
	partnersCall(t, api, "POST", "/api/deals", `{"name":"Big one","partner_ids":["pa-z","pa-n","pa-z",""]}`, 201, &made)
	if got := partnerLabels(made.Deal); got != "Nimbus,Zeta" {
		t.Fatalf("created with partners %q", got)
	}
	if made.Deal.Partners[0].Domain == nil || *made.Deal.Partners[0].Domain != "nimbus.example" {
		t.Fatalf("partner chip without its domain: %+v", made.Deal.Partners[0])
	}
	id := made.Deal.ID

	// Every deal read carries them.
	var one struct{ Deal dealJSON }
	partnersCall(t, api, "GET", "/api/deals/"+id, "", 200, &one)
	if got := partnerLabels(one.Deal); got != "Nimbus,Zeta" {
		t.Fatalf("get: %q", got)
	}
	partnersCall(t, api, "GET", "/api/deals/board", "", 200, &page)
	if len(page.Deals) != 2 || page.Deals[0].Partners == nil || len(page.Deals[0].Partners) != 0 || partnerLabels(page.Deals[1]) != "Nimbus,Zeta" {
		t.Fatalf("board: %+v", page.Deals)
	}

	// An update with only partner_ids replaces the list, and is an update of
	// the deal.
	partnersCall(t, api, "PUT", "/api/deals/d-old", `{"partner_ids":["pa-a"]}`, 200, &one)
	if got := partnerLabels(one.Deal); got != "Acme Resellers" || one.Deal.UpdatedAt == "2020-01-01 00:00:00" {
		t.Fatalf("update: %q at %s", got, one.Deal.UpdatedAt)
	}
	partnersCall(t, api, "PUT", "/api/deals/"+id, `{"partner_ids":["pa-a","pa-n"]}`, 200, &one)
	if got := partnerLabels(one.Deal); got != "Acme Resellers,Nimbus" {
		t.Fatalf("replace: %q", got)
	}
	partnersCall(t, api, "PUT", "/api/deals/"+id, `{"partner_ids":["nope"]}`, 400, nil)
	partnersCall(t, api, "PUT", "/api/deals/"+id, `{"name":"Big one!"}`, 200, &one)
	if got := partnerLabels(one.Deal); got != "Acme Resellers,Nimbus" {
		t.Fatalf("an update without partner_ids changed them: %q", got)
	}

	// Every deal read carries them: the progress list, and a logged update.
	var progress struct{ Deals []dealJSON }
	partnersCall(t, api, "GET", "/api/deals/progress", "", 200, &progress)
	for _, d := range progress.Deals {
		if d.Partners == nil || (d.ID == id && partnerLabels(d) != "Acme Resellers,Nimbus") {
			t.Fatalf("progress: %+v", progress.Deals)
		}
	}
	var logged struct{ Deal dealJSON }
	asAgent := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Clawnify-Caller", "agent") // an update needs someone to have logged it
		api.ServeHTTP(w, r)
	})
	partnersCall(t, asAgent, "POST", "/api/deals/"+id+"/updates", `{"kind":"call","summary":"Called them"}`, 201, &logged)
	if got := partnerLabels(logged.Deal); got != "Acme Resellers,Nimbus" {
		t.Fatalf("a deal update's deal: %q", got)
	}

	// The partner's deals: by list, by filter, by count.
	partnersCall(t, api, "GET", "/api/deals?partner_id=pa-a&sort=name&order=asc", "", 200, &page)
	if got := dealNames(page.Deals); got != "Big one!,Old deal" {
		t.Fatalf("deals of pa-a: %q", got)
	}
	filter := func(rules string) string {
		partnersCall(t, api, "GET", "/api/deals/board?filters="+url.QueryEscape(rules), "", 200, &page)
		return dealNames(page.Deals)
	}
	if got := filter(`[{"field":"partners","op":"is","value":["pa-n"]}]`); got != "Big one!" {
		t.Fatalf("partners is pa-n: %q", got)
	}
	if got := filter(`[{"field":"partners","op":"is_not","value":["pa-n"]}]`); got != "Old deal" {
		t.Fatalf("partners is not pa-n: %q", got)
	}
	if got := filter(`[{"field":"count:partners","op":"gte","value":2}]`); got != "Big one!" {
		t.Fatalf("2+ partners: %q", got)
	}
	var partners struct{ Partners []partnerJSON }
	partnersCall(t, api, "GET", "/api/partners?sort=count:deals&order=desc", "", 200, &partners)
	if p := partners.Partners[0]; p.Name != "Acme Resellers" || p.DealCount != 2 {
		t.Fatalf("most deals first: %+v", partners.Partners)
	}
	partnersCall(t, api, "GET", "/api/partners?filters="+url.QueryEscape(`[{"field":"deals","op":"is_empty"}]`), "", 200, &partners)
	if len(partners.Partners) != 1 || partners.Partners[0].Name != "Zeta" {
		t.Fatalf("partners without deals: %+v", partners.Partners)
	}

	// [] clears them; deleting a partner drops it from its deals, which stay.
	partnersCall(t, api, "PUT", "/api/deals/d-old", `{"partner_ids":[]}`, 200, &one)
	if len(one.Deal.Partners) != 0 {
		t.Fatalf("cleared: %+v", one.Deal.Partners)
	}
	partnersCall(t, api, "DELETE", "/api/partners/pa-n", "", 200, nil)
	partnersCall(t, api, "GET", "/api/deals/"+id, "", 200, &one)
	if got := partnerLabels(one.Deal); got != "Acme Resellers" {
		t.Fatalf("after deleting a partner: %q", got)
	}
	partnersCall(t, api, "DELETE", "/api/deals/"+id, "", 200, nil)
	var left struct{ Partner partnerJSON }
	partnersCall(t, api, "GET", "/api/partners/pa-a", "", 200, &left)
	if left.Partner.DealCount != 0 {
		t.Fatalf("a deleted deal still counts: %+v", left.Partner)
	}
}

func TestPartnersAsARecordType(t *testing.T) {
	recordedDB(t, []string{`INSERT INTO partners (id, name, domain) VALUES ('pa-n', 'Nimbus', 'nimbus.example'), ('pa-a', 'Acme Resellers', '')`})
	api := replayAPI(&App{})
	ctx := context.Background()

	// Custom fields and relations, as on any record.
	partnersCall(t, api, "POST", "/api/custom-fields", `{"entity_type":"partner","key":"tier","label":"Tier","field_type":"string"}`, 201, nil)
	partnersCall(t, api, "POST", "/api/custom-fields", `{"entity_type":"contact","key":"partner_id","label":"Partner","field_type":"relation","relation_type":"many_to_one","target_entity":"partner","inverse_key":"people","inverse_label":"People"}`, 201, nil)
	mustRun(t, ctx, `INSERT INTO contacts (id, first_name, partner_id) VALUES ('ct-1', 'Ann', 'pa-n')`)
	var one struct {
		Partner map[string]any
	}
	partnersCall(t, api, "PUT", "/api/partners/pa-n", `{"tier":"gold"}`, 200, &one)
	if one.Partner["tier"] != "gold" {
		t.Fatalf("custom field: %+v", one.Partner)
	}
	people, _ := json.Marshal(one.Partner["relations"])
	if !strings.Contains(string(people), `"people":{"items":[{"domain":null,"id":"ct-1","label":"Ann"}],"total":1}`) {
		t.Fatalf("relations: %s", people)
	}

	// Keys a deal or a partner answers with are no custom field's.
	for _, body := range []string{
		`{"entity_type":"deal","key":"partners","label":"Partners","field_type":"string"}`,
		`{"entity_type":"deal","key":"partner_ids","label":"Partner ids","field_type":"string"}`,
		`{"entity_type":"partner","key":"deals","label":"Deals","field_type":"string"}`,
		`{"entity_type":"partner","key":"deal_count","label":"Deal count","field_type":"integer"}`,
	} {
		partnersCall(t, api, "POST", "/api/custom-fields", body, 400, nil)
	}

	// Pickers, values and views.
	var records recordsBody
	partnersCall(t, api, "GET", "/api/records?entity=partner&search=nim", "", 200, &records)
	if len(records.Records) != 1 || records.Records[0].Label != "Nimbus" || records.Records[0].Domain == nil {
		t.Fatalf("records: %+v", records)
	}
	var values pickerValuesBody
	partnersCall(t, api, "GET", "/api/values?entity=partner&field=tier", "", 200, &values)
	if strings.Join(values.Values, ",") != "gold" {
		t.Fatalf("values: %+v", values)
	}
	raw := partnersCall(t, api, "GET", "/api/views?entity=partner", "", 200, nil)
	if !strings.Contains(raw, `"name":"All partners"`) {
		t.Fatalf("views: %s", raw)
	}
	partnersCall(t, api, "POST", "/api/activities", `{"entity_type":"partner","entity_id":"pa-n","body":"Met at the fair"}`, 201, nil)

	// Deleting a partner clears links to it.
	partnersCall(t, api, "DELETE", "/api/partners/pa-n", "", 200, nil)
	var contact struct{ Contact map[string]any }
	partnersCall(t, api, "GET", "/api/contacts/ct-1", "", 200, &contact)
	if contact.Contact["partner_id"] != nil {
		t.Fatalf("a contact still points at a deleted partner: %+v", contact.Contact)
	}
}
