package crm

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// A company's main contact came after the port: the Go server's own
// expectations. They may work at another company (a building's property
// manager works at the management firm).
func TestCompanyMainContact(t *testing.T) {
	recordedDB(t, []string{
		`INSERT INTO companies (id, name, domain) VALUES ('co-a', 'Acme', ''), ('co-b', 'Beta Management', 'beta.example')`,
		`INSERT INTO contacts (id, first_name, last_name, company_id) VALUES ('ct-ana', 'Ana', 'Lopez', 'co-a'), ('ct-bo', 'Bo', '', 'co-b'), ('ct-cy', 'Cy', '', NULL)`,
	})
	api := replayAPI(&App{})
	var one struct {
		Company map[string]any `json:"company"`
	}
	partnersCall(t, api, "GET", "/api/companies/co-a", "", 200, &one)
	if one.Company["main_contact"] != nil || one.Company["main_contact_company"] != nil {
		t.Fatalf("none yet: %v", one.Company)
	}
	if raw := partnersCall(t, api, "PUT", "/api/companies/co-a", `{"main_contact_id":"nope"}`, 400, nil); !strings.Contains(raw, `no contact with id \"nope\"`) {
		t.Fatalf("no such contact: %s", raw)
	}
	partnersCall(t, api, "PUT", "/api/companies/nope", `{"main_contact_id":"ct-ana"}`, 404, nil)

	// One of its own.
	partnersCall(t, api, "PUT", "/api/companies/co-a", `{"main_contact_id":"ct-ana"}`, 200, &one)
	checkJSON(t, "own", one.Company["main_contact"], `{"domain":null,"id":"ct-ana","label":"Ana Lopez"}`)
	checkJSON(t, "own company", one.Company["main_contact_company"], `{"domain":"","id":"co-a","label":"Acme"}`)
	// Another company's: the property manager at the management firm.
	partnersCall(t, api, "PUT", "/api/companies/co-a", `{"main_contact_id":"ct-bo"}`, 200, &one)
	checkJSON(t, "another's", one.Company["main_contact"], `{"domain":null,"id":"ct-bo","label":"Bo"}`)
	checkJSON(t, "their company", one.Company["main_contact_company"], `{"domain":"beta.example","id":"co-b","label":"Beta Management"}`)
	// Nobody's: no company to name.
	partnersCall(t, api, "PUT", "/api/companies/co-a", `{"main_contact_id":"ct-cy"}`, 200, &one)
	if one.Company["main_contact_company"] != nil || one.Company["main_contact"] == nil {
		t.Fatalf("no company: %v", one.Company)
	}
	// Moving to another company keeps them the main contact.
	partnersCall(t, api, "PUT", "/api/companies/co-a", `{"main_contact_id":"ct-ana"}`, 200, nil)
	mustRun(t, context.Background(), `UPDATE contacts SET company_id = 'co-b' WHERE id = 'ct-ana'`)
	partnersCall(t, api, "GET", "/api/companies/co-a", "", 200, &one)
	checkJSON(t, "moved", one.Company["main_contact_company"], `{"domain":"beta.example","id":"co-b","label":"Beta Management"}`)

	// A new company may start with one.
	partnersCall(t, api, "POST", "/api/companies", `{"name":"Condo Uno","main_contact_id":"ct-bo"}`, 201, &one)
	checkJSON(t, "on create", one.Company["main_contact"], `{"domain":null,"id":"ct-bo","label":"Bo"}`)
	partnersCall(t, api, "POST", "/api/companies", `{"name":"Condo Dos","main_contact_id":"nope"}`, 400, nil)
	// The companies a contact is the main contact of, by the list's filter.
	var list struct{ Companies []map[string]any }
	partnersCall(t, api, "GET", "/api/companies?sort=name&order=asc&filters="+url.QueryEscape(`[{"field":"main_contact_id","op":"is","value":["ct-bo"]}]`), "", 200, &list)
	if len(list.Companies) != 1 || list.Companies[0]["name"] != "Condo Uno" {
		t.Fatalf("Bo's: %v", list.Companies)
	}

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

// Removing a customer: the company stays, without its customer dates, and its
// timeline keeps that it was one; its customer users stay its own.
func TestRemoveCustomer(t *testing.T) {
	recordedDB(t, []string{
		`INSERT INTO companies (id, name, customer_since, renewal_date) VALUES ('co-a', 'Acme', '2025-01-01', '2026-01-01'), ('co-b', 'Beta', NULL, NULL)`,
		`INSERT INTO customer_users (id, name, company_id) VALUES ('cu-ann', 'Ann', 'co-a')`,
	})
	api := replayAPI(&App{})
	partnersCall(t, api, "POST", "/api/companies/nope/remove-customer", "", 404, nil)
	if raw := partnersCall(t, api, "POST", "/api/companies/co-b/remove-customer", "", 400, nil); !strings.Contains(raw, "isn't a customer") {
		t.Fatalf("not a customer: %s", raw)
	}
	var one struct {
		Company map[string]any `json:"company"`
	}
	partnersCall(t, api, "POST", "/api/companies/co-a/remove-customer", "", 200, &one)
	if one.Company["customer_since"] != nil || one.Company["renewal_date"] != nil || one.Company["name"] != "Acme" {
		t.Fatalf("after: %v", one.Company)
	}
	var acts struct {
		Activities []struct{ Type, Body, Meta string }
	}
	partnersCall(t, api, "GET", "/api/activities?entity_type=company&entity_id=co-a", "", 200, &acts)
	if len(acts.Activities) != 1 || acts.Activities[0].Type != "customer_change" || acts.Activities[0].Body != "No longer a customer (a customer since 2025-01-01)" ||
		acts.Activities[0].Meta != `{"customer_since":"2025-01-01","renewal_date":"2026-01-01"}` {
		t.Fatalf("timeline: %+v", acts.Activities)
	}
	var user struct {
		CustomerUser struct {
			CompanyID *string `json:"company_id"`
		} `json:"customer_user"`
	}
	partnersCall(t, api, "GET", "/api/customer-users/cu-ann", "", 200, &user)
	if user.CustomerUser.CompanyID == nil || *user.CustomerUser.CompanyID != "co-a" {
		t.Fatalf("its user: %+v", user.CustomerUser)
	}
	partnersCall(t, api, "POST", "/api/companies/co-a/remove-customer", "", 400, nil)
}
