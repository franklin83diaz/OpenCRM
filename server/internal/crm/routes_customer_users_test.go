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
		`INSERT INTO companies (id, name, domain, customer_since) VALUES ('co-a', 'Acme', 'acme.example', '2025-01-01'), ('co-b', 'Beta', '', '2025-02-01'), ('co-c', 'Gamma', '', NULL)`,
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
	// Only a customer's: a company with no customer_since is refused.
	if raw := partnersCall(t, api, "POST", "/api/customer-users", `{"name":"Bo","company_id":"co-c"}`, 400, nil); !strings.Contains(raw, "Gamma is not a customer") {
		t.Fatalf("not a customer: %s", raw)
	}
	partnersCall(t, api, "POST", "/api/customer-users", `{"name":"Bo","company_id":"co-b"}`, 201, &made)
	bo := made.CustomerUser.ID
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

	// A company that stops being a customer stays its users' own, and takes no others.
	mustRun(t, context.Background(), `UPDATE companies SET customer_since = NULL WHERE id = 'co-b'`)
	partnersCall(t, api, "PUT", "/api/customer-users/"+bo, `{"name":"Bo Ray","company_id":"co-b"}`, 200, nil)
	partnersCall(t, api, "PUT", "/api/customer-users/"+ann, `{"company_id":"co-b"}`, 400, nil)
	mustRun(t, context.Background(), `UPDATE companies SET customer_since = '2025-02-01' WHERE id = 'co-b'`)

	// The company picker's search: customers only, by name; ids name any company.
	var found recordsBody
	partnersCall(t, api, "GET", "/api/records?entity=company&customers=1", "", 200, &found)
	checkJSON(t, "customers", found.Records, `[{"id":"co-a","label":"Acme","domain":"acme.example"},{"id":"co-b","label":"Beta","domain":""}]`)
	partnersCall(t, api, "GET", "/api/records?entity=company&customers=true&search=bet", "", 200, &found)
	checkJSON(t, "customers by name", found.Records, `[{"id":"co-b","label":"Beta","domain":""}]`)
	partnersCall(t, api, "GET", "/api/records?entity=company&customers=1&ids=co-c", "", 200, &found)
	checkJSON(t, "by id", found.Records, `[{"id":"co-c","label":"Gamma","domain":""}]`)
	partnersCall(t, api, "GET", "/api/records?entity=contact&customers=1", "", 400, nil)

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

// Customer users are found by name, email, or phone typed any way.
func TestCustomerUserSearch(t *testing.T) {
	recordedDB(t, []string{
		`INSERT INTO companies (id, name, customer_since) VALUES ('co-a', 'Acme', '2025-01-01')`,
		`INSERT INTO customer_users (id, name, email, phone, company_id) VALUES ('cu-ann', 'Ann Lee', 'ann@acme.example', '+1 (555) 123-4567', 'co-a'), ('cu-bo', 'Bo Ray', 'bo@beta.example', '555.987.6543', 'co-a'), ('cu-cy', 'Cy 555', '', '', NULL)`,
	})
	api := replayAPI(&App{})
	var page struct {
		CustomerUsers []customerUserJSON `json:"customer_users"`
	}
	for search, want := range map[string]string{
		"ann@acme":     "cu-ann",
		"BETA.example": "cu-bo",
		"Bo Ray":       "cu-bo",
		"5551234567":   "cu-ann",
		"555-123-4567": "cu-ann",
		"(555) 987":    "cu-bo",
		"555":          "cu-ann,cu-bo,cu-cy",
		"12":           "",
	} {
		partnersCall(t, api, "GET", "/api/customer-users?sort=id&order=asc&search="+url.QueryEscape(search), "", 200, &page)
		got := []string{}
		for _, u := range page.CustomerUsers {
			got = append(got, u.ID)
		}
		if strings.Join(got, ",") != want {
			t.Errorf("search %q: %v, want %q", search, got, want)
		}
	}
	raw := partnersCall(t, api, "GET", "/api/customer-users/aggregates?search="+url.QueryEscape("987 6543")+"&ops="+url.QueryEscape(`[{"key":"name","op":"count"}]`), "", 200, nil)
	if !strings.Contains(raw, `"name":1`) {
		t.Errorf("aggregates: %s", raw)
	}
}
