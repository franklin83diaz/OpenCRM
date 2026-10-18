package crm

import (
	"context"
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
