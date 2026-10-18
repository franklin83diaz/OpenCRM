package crm

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// A company's main contact came after the port: the Go server's own expectations.
func TestCompanyMainContact(t *testing.T) {
	recordedDB(t, []string{
		`INSERT INTO companies (id, name) VALUES ('co-a', 'Acme'), ('co-b', 'Beta')`,
		`INSERT INTO contacts (id, first_name, last_name, company_id) VALUES ('ct-ana', 'Ana', 'Lopez', 'co-a'), ('ct-bo', 'Bo', '', 'co-b')`,
	})
	api := replayAPI(&App{})
	var one struct {
		Company map[string]any `json:"company"`
	}
	partnersCall(t, api, "GET", "/api/companies/co-a", "", 200, &one)
	if one.Company["main_contact"] != nil {
		t.Fatalf("none yet: %v", one.Company["main_contact"])
	}
	if raw := partnersCall(t, api, "PUT", "/api/companies/co-a", `{"main_contact_id":"ct-bo"}`, 400, nil); !strings.Contains(raw, "not one of this company's contacts") {
		t.Fatalf("another company's: %s", raw)
	}
	partnersCall(t, api, "PUT", "/api/companies/nope", `{"main_contact_id":"ct-ana"}`, 404, nil)
	partnersCall(t, api, "POST", "/api/companies", `{"name":"New","main_contact_id":"ct-ana"}`, 400, nil)
	partnersCall(t, api, "PUT", "/api/companies/co-a", `{"main_contact_id":"ct-ana"}`, 200, &one)
	checkJSON(t, "set", one.Company["main_contact"], `{"domain":null,"id":"ct-ana","label":"Ana Lopez"}`)
	partnersCall(t, api, "GET", "/api/companies/co-a", "", 200, &one)
	checkJSON(t, "read", one.Company["main_contact"], `{"domain":null,"id":"ct-ana","label":"Ana Lopez"}`)

	// A main contact who moves to another company is no longer this one's.
	mustRun(t, context.Background(), `UPDATE contacts SET company_id = 'co-b' WHERE id = 'ct-ana'`)
	partnersCall(t, api, "GET", "/api/companies/co-a", "", 200, &one)
	if one.Company["main_contact"] != nil {
		t.Fatalf("moved away: %v", one.Company["main_contact"])
	}
	mustRun(t, context.Background(), `UPDATE contacts SET company_id = 'co-a' WHERE id = 'ct-ana'`)
	partnersCall(t, api, "PUT", "/api/companies/co-a", `{"main_contact_id":null}`, 200, &one)
	if one.Company["main_contact"] != nil || one.Company["main_contact_id"] != nil {
		t.Fatalf("cleared: %v", one.Company)
	}
	// Deleting the contact clears it.
	partnersCall(t, api, "PUT", "/api/companies/co-a", `{"main_contact_id":"ct-ana"}`, 200, nil)
	mustRun(t, context.Background(), `DELETE FROM contacts WHERE id = 'ct-ana'`)
	partnersCall(t, api, "GET", "/api/companies/co-a", "", 200, &one)
	if one.Company["main_contact_id"] != nil {
		t.Fatalf("after the contact went: %v", one.Company)
	}
}

// A deal's main partner and a company's: who the customer is, beyond the
// company and its main contact.
func TestMainPartners(t *testing.T) {
	recordedDB(t, []string{
		`INSERT INTO companies (id, name) VALUES ('co-a', 'Acme')`,
		`INSERT INTO partners (id, name) VALUES ('pa-n', 'Nimbus'), ('pa-z', 'Zeta')`,
		`INSERT INTO deals (id, name, company_id) VALUES ('d-1', 'Hosting', 'co-a')`,
	})
	api := replayAPI(&App{})
	var deal struct {
		Deal struct {
			Partners []struct {
				ID     string `json:"id"`
				IsMain bool   `json:"is_main"`
			} `json:"partners"`
		} `json:"deal"`
	}
	mains := func() string {
		out := ""
		for _, p := range deal.Deal.Partners {
			out += fmt.Sprintf("%s:%v ", p.ID, p.IsMain)
		}
		return out
	}
	partnersCall(t, api, "PUT", "/api/deals/d-1/partners/pa-n", `{}`, 200, &deal)
	partnersCall(t, api, "PUT", "/api/deals/d-1/partners/pa-z", `{"is_main":true}`, 200, &deal)
	if got := mains(); got != "pa-z:true pa-n:false " {
		t.Fatalf("zeta main, first: %q", got)
	}
	partnersCall(t, api, "PUT", "/api/deals/d-1/partners/pa-n", `{"is_main":true}`, 200, &deal)
	if got := mains(); got != "pa-n:true pa-z:false " {
		t.Fatalf("one main at a time: %q", got)
	}
	partnersCall(t, api, "PUT", "/api/deals/d-1/partners/pa-n", `{"is_main":false}`, 200, &deal)
	if got := mains(); got != "pa-n:false pa-z:false " {
		t.Fatalf("none: %q", got)
	}
	partnersCall(t, api, "PUT", "/api/deals/d-1/partners/pa-n", `{"is_main":"yes"}`, 400, nil)
	// A deal partner_ids write keeps the main partner it keeps.
	partnersCall(t, api, "PUT", "/api/deals/d-1/partners/pa-z", `{"is_main":true}`, 200, nil)
	partnersCall(t, api, "PUT", "/api/deals/d-1", `{"partner_ids":["pa-z"]}`, 200, &deal)
	if got := mains(); got != "pa-z:true " {
		t.Fatalf("kept: %q", got)
	}

	var one struct {
		Company map[string]any `json:"company"`
	}
	partnersCall(t, api, "PUT", "/api/companies/co-a", `{"main_partner_id":"nope"}`, 400, nil)
	partnersCall(t, api, "PUT", "/api/companies/co-a", `{"main_partner_id":"pa-n"}`, 200, &one)
	checkJSON(t, "main partner", one.Company["main_partner"], `{"domain":null,"id":"pa-n","label":"Nimbus"}`)
	partnersCall(t, api, "POST", "/api/companies", `{"name":"Beta","main_partner_id":"pa-z"}`, 201, &one)
	checkJSON(t, "on create", one.Company["main_partner"], `{"domain":null,"id":"pa-z","label":"Zeta"}`)
	partnersCall(t, api, "POST", "/api/companies", `{"name":"Gamma","main_partner_id":"nope"}`, 400, nil)
	// Deleting the partner clears it.
	partnersCall(t, api, "DELETE", "/api/partners/pa-n", "", 200, nil)
	partnersCall(t, api, "GET", "/api/companies/co-a", "", 200, &one)
	if one.Company["main_partner_id"] != nil || one.Company["main_partner"] != nil {
		t.Fatalf("after the partner went: %v", one.Company)
	}
	partnersCall(t, api, "PUT", "/api/companies/co-a", `{"main_partner_id":null,"name":"Acme"}`, 200, nil)

	// The Customers page: who each customer is.
	mustRun(t, context.Background(), `INSERT INTO contacts (id, first_name, last_name, company_id) VALUES ('ct-ana', 'Ana', 'Lopez', 'co-a')`)
	partnersCall(t, api, "PUT", "/api/companies/co-a", `{"customer_since":"2025-01-01","main_contact_id":"ct-ana","main_partner_id":"pa-z"}`, 200, nil)
	var customers struct {
		Customers []struct {
			ID          string `json:"id"`
			MainContact any    `json:"main_contact"`
			MainPartner any    `json:"main_partner"`
		} `json:"customers"`
	}
	partnersCall(t, api, "GET", "/api/customers", "", 200, &customers)
	if len(customers.Customers) != 1 {
		t.Fatalf("customers: %+v", customers.Customers)
	}
	checkJSON(t, "customer people", customers.Customers[0], `{"id":"co-a","main_contact":{"domain":null,"id":"ct-ana","label":"Ana Lopez"},"main_partner":{"domain":null,"id":"pa-z","label":"Zeta"}}`)
}
