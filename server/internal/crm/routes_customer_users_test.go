package crm

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// Customer users came after the port: these are the Go server's own
// expectations.

type customerUserJSON struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Email       string  `json:"email"`
	CompanyID   *string `json:"company_id"`
	CompanyName *string `json:"company_name"`
}

func TestCustomerUsers(t *testing.T) {
	recordedDB(t, []string{
		`INSERT INTO companies (id, name, domain, customer_since) VALUES ('co-a', 'Acme', 'acme.example', '2025-01-01'), ('co-b', 'Beta', '', NULL)`,
	})
	api := replayAPI(&App{})

	partnersCall(t, api, "POST", "/api/customer-users", `{"name":" "}`, 400, nil)
	partnersCall(t, api, "POST", "/api/customer-users", `{"name":"Ann","company_id":"nope"}`, 400, nil)
	partnersCall(t, api, "POST", "/api/customer-users", `{"name":"Ann","bogus":1}`, 422, nil)
	var made struct {
		CustomerUser customerUserJSON `json:"customer_user"`
	}
	partnersCall(t, api, "POST", "/api/customer-users", `{"name":" Ann Lee ","email":"ann@acme.example","company_id":"co-a"}`, 201, &made)
	if u := made.CustomerUser; u.Name != "Ann Lee" || u.CompanyName == nil || *u.CompanyName != "Acme" {
		t.Fatalf("created: %+v", u)
	}
	ann := made.CustomerUser.ID
	partnersCall(t, api, "POST", "/api/customer-users", `{"name":"Bo","company_id":"co-b"}`, 201, nil)
	partnersCall(t, api, "POST", "/api/customer-users", `{"name":"Cy"}`, 201, nil)

	var page struct {
		CustomerUsers []customerUserJSON `json:"customer_users"`
		Total         float64
	}
	names := func() string {
		out := ""
		for i, u := range page.CustomerUsers {
			if i > 0 {
				out += ","
			}
			out += u.Name
		}
		return out
	}
	partnersCall(t, api, "GET", "/api/customer-users?company_id=co-a", "", 200, &page)
	if names() != "Ann Lee" {
		t.Fatalf("one customer's: %q", names())
	}
	partnersCall(t, api, "GET", "/api/customer-users?search=acme.example", "", 200, &page)
	if names() != "Ann Lee" {
		t.Fatalf("by email: %q", names())
	}
	// By the company's name, the empty ones last in ascending order.
	partnersCall(t, api, "GET", "/api/customer-users?sort=company_id&order=desc", "", 200, &page)
	if names() != "Bo,Ann Lee,Cy" {
		t.Fatalf("by company: %q", names())
	}
	partnersCall(t, api, "GET", "/api/customer-users?filters="+url.QueryEscape(`[{"field":"company_id","op":"is_empty"}]`), "", 200, &page)
	if names() != "Cy" {
		t.Fatalf("without a company: %q", names())
	}
	// A company's customer users, from the companies' side.
	var companies struct{ Companies []map[string]any }
	partnersCall(t, api, "GET", "/api/companies?filters="+url.QueryEscape(`[{"field":"count:customer_users","op":"gte","value":1}]`)+"&sort=name&order=asc", "", 200, &companies)
	if len(companies.Companies) != 2 {
		t.Fatalf("companies with users: %+v", companies.Companies)
	}

	var one struct {
		CustomerUser customerUserJSON `json:"customer_user"`
	}
	partnersCall(t, api, "PUT", "/api/customer-users/"+ann, `{"name":""}`, 400, nil)
	partnersCall(t, api, "PUT", "/api/customer-users/"+ann, `{}`, 400, nil)
	partnersCall(t, api, "PUT", "/api/customer-users/"+ann, `{"company_id":"nope"}`, 400, nil)
	partnersCall(t, api, "PUT", "/api/customer-users/"+ann, `{"company_id":null,"email":"ann@new.example"}`, 200, &one)
	if u := one.CustomerUser; u.CompanyID != nil || u.Email != "ann@new.example" {
		t.Fatalf("no company: %+v", u)
	}
	partnersCall(t, api, "PUT", "/api/customer-users/"+ann, `{"company_id":"co-a"}`, 200, &one)

	var stats statsBody
	partnersCall(t, api, "GET", "/api/stats", "", 200, &stats)
	if stats.CustomerUsers != 3.0 {
		t.Fatalf("stats: %v", stats.CustomerUsers)
	}

	// Deleting the company leaves its users, without one.
	mustRun(t, context.Background(), `DELETE FROM companies WHERE id = 'co-a'`)
	partnersCall(t, api, "GET", "/api/customer-users/"+ann, "", 200, &one)
	if one.CustomerUser.CompanyID != nil {
		t.Fatalf("after the company went: %+v", one.CustomerUser)
	}
	partnersCall(t, api, "DELETE", "/api/customer-users/"+ann, "", 200, nil)
	partnersCall(t, api, "GET", "/api/customer-users/"+ann, "", 404, nil)
	raw := partnersCall(t, api, "GET", "/api/views?entity=customer_user", "", 200, nil)
	if !strings.Contains(raw, `"name":"All customer users"`) {
		t.Fatalf("views: %s", raw)
	}
}
