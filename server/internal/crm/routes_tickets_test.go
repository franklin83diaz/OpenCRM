package crm

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// Tickets came after the port: these are the Go server's own expectations.

type ticketJSON struct {
	ID               string  `json:"id"`
	Subject          string  `json:"subject"`
	Status           string  `json:"status"`
	Priority         string  `json:"priority"`
	CompanyID        *string `json:"company_id"`
	CompanyName      *string `json:"company_name"`
	CustomerUserID   *string `json:"customer_user_id"`
	CustomerUserName *string `json:"customer_user_name"`
	OwnerID          *string `json:"owner_id"`
	Owner            *struct {
		Name string `json:"name"`
	} `json:"owner"`
	DueDate *string `json:"due_date"`
}

func TestTickets(t *testing.T) {
	recordedDB(t, []string{
		`INSERT INTO companies (id, name, domain, customer_since) VALUES ('co-a', 'Acme', 'acme.example', '2025-01-01'), ('co-b', 'Beta', '', '2025-02-01'), ('co-c', 'Gamma', '', NULL)`,
		`INSERT INTO customer_users (id, name, company_id) VALUES ('cu-ann', 'Ann', 'co-a'), ('cu-bo', 'Bo', 'co-b'), ('cu-cy', 'Cy', NULL)`,
		`INSERT INTO users (id, email, name, role, password_hash) VALUES ('u-ana', 'ana@example.com', 'Ana', 'admin', 'x')`,
	})
	api := replayAPI(&App{})
	var one struct {
		Ticket ticketJSON `json:"ticket"`
	}
	refused := func(body, want string) {
		t.Helper()
		if raw := partnersCall(t, api, "POST", "/api/tickets", body, 400, nil); !strings.Contains(raw, want) {
			t.Fatalf("%s: %s, want %q", body, raw, want)
		}
	}

	refused(`{"subject":" ","company_id":"co-a"}`, "Subject is required")
	refused(`{"subject":"Down"}`, "company_id is required")
	refused(`{"subject":"Down","company_id":"co-c"}`, "Gamma is not a customer")
	refused(`{"subject":"Down","company_id":"nope"}`, `no company with id \"nope\"`)
	refused(`{"subject":"Down","company_id":"co-a","customer_user_id":"cu-bo"}`, "Bo is not one of Acme's users")
	refused(`{"subject":"Down","customer_user_id":"cu-cy"}`, "company_id is required")
	refused(`{"subject":"Down","company_id":"co-a","status":"later"}`, "status must be one of open, in_progress, waiting, resolved, closed")
	refused(`{"subject":"Down","company_id":"co-a","priority":"asap"}`, "priority must be one of low, medium, high, urgent")
	refused(`{"subject":"Down","company_id":"co-a","due_date":"soon"}`, "due_date must be a date as YYYY-MM-DD")
	refused(`{"subject":"Down","company_id":"co-a","owner_id":"nobody"}`, "owner_id: no active account")
	partnersCall(t, api, "POST", "/api/tickets", `{"subject":"Down","company_id":"co-a","bogus":1}`, 422, nil)

	// A customer user alone is enough: the ticket is for their customer.
	partnersCall(t, api, "POST", "/api/tickets", `{"subject":" Login fails ","customer_user_id":"cu-ann","priority":"High","owner_id":"u-ana","due_date":"2026-11-02"}`, 201, &one)
	if tk := one.Ticket; tk.Subject != "Login fails" || tk.Status != "open" || tk.Priority != "high" || tk.CompanyName == nil || *tk.CompanyName != "Acme" ||
		tk.CustomerUserName == nil || *tk.CustomerUserName != "Ann" || tk.Owner == nil || tk.Owner.Name != "Ana" || tk.DueDate == nil || *tk.DueDate != "2026-11-02" {
		t.Fatalf("created: %+v", tk)
	}
	login := one.Ticket.ID
	partnersCall(t, api, "POST", "/api/tickets", `{"subject":"Invoice copy","company_id":"co-b","status":"In progress","priority":"low"}`, 201, &one)
	if tk := one.Ticket; tk.Status != "in_progress" || tk.Owner != nil || tk.DueDate != nil || tk.CustomerUserID != nil {
		t.Fatalf("second: %+v", tk)
	}
	invoice := one.Ticket.ID
	partnersCall(t, api, "POST", "/api/tickets", `{"subject":"Export to CSV","company_id":"co-a","priority":"urgent","status":"closed"}`, 201, nil)

	var page struct {
		Tickets []ticketJSON
		Total   float64
	}
	subjects := func() string {
		out := []string{}
		for _, tk := range page.Tickets {
			out = append(out, tk.Subject)
		}
		return strings.Join(out, ",")
	}
	list := func(query, want string) {
		t.Helper()
		partnersCall(t, api, "GET", "/api/tickets?"+query, "", 200, &page)
		if got := subjects(); got != want {
			t.Fatalf("%s: %q, want %q", query, got, want)
		}
	}
	list("company_id=co-a&sort=subject&order=asc", "Export to CSV,Login fails")
	list("customer_user_id=cu-ann", "Login fails")
	list("search=invoice", "Invoice copy")
	list("sort=priority&order=desc", "Export to CSV,Login fails,Invoice copy")
	list("sort=status&order=asc", "Login fails,Invoice copy,Export to CSV")
	list("sort=due_date&order=asc&limit=1", "Login fails")
	list("filters="+url.QueryEscape(`[{"field":"owner_id","op":"is_empty"}]`)+"&sort=subject&order=asc", "Export to CSV,Invoice copy")
	list("filters="+url.QueryEscape(`[{"field":"status","op":"is","value":["open","in_progress"]}]`)+"&sort=subject&order=asc", "Invoice copy,Login fails")
	var companies struct{ Companies []map[string]any }
	partnersCall(t, api, "GET", "/api/companies?filters="+url.QueryEscape(`[{"field":"count:tickets","op":"gte","value":2}]`), "", 200, &companies)
	if len(companies.Companies) != 1 || companies.Companies[0]["id"] != "co-a" {
		t.Fatalf("companies with tickets: %+v", companies.Companies)
	}

	// Updates: the customer user must be the customer's; moving the ticket to
	// another customer drops one who isn't theirs.
	put := func(id, body string, status int) {
		t.Helper()
		partnersCall(t, api, "PUT", "/api/tickets/"+id, body, status, &one)
	}
	put(login, `{}`, 400)
	put(login, `{"subject":""}`, 400)
	put(login, `{"company_id":null}`, 400)
	put(login, `{"customer_user_id":"cu-bo"}`, 400)
	put("nope", `{"subject":"x"}`, 404)
	put(login, `{"company_id":"co-b"}`, 200)
	if tk := one.Ticket; *tk.CompanyName != "Beta" || tk.CustomerUserID != nil {
		t.Fatalf("moved: %+v", tk)
	}
	put(login, `{"company_id":"co-a","customer_user_id":"cu-ann","status":"waiting","owner_id":null,"due_date":null}`, 200)
	if tk := one.Ticket; *tk.CustomerUserName != "Ann" || tk.Status != "waiting" || tk.OwnerID != nil || tk.DueDate != nil {
		t.Fatalf("back: %+v", tk)
	}
	// A customer that stopped being one stays its tickets', and takes no others.
	mustRun(t, context.Background(), `UPDATE companies SET customer_since = NULL WHERE id = 'co-b'`)
	put(invoice, `{"company_id":"co-b","subject":"Invoice copy, again"}`, 200)
	put(login, `{"company_id":"co-b"}`, 400)

	var stats statsBody
	partnersCall(t, api, "GET", "/api/stats", "", 200, &stats)
	if stats.Tickets != 3.0 || stats.OpenTickets != 2.0 {
		t.Fatalf("stats: %v %v", stats.Tickets, stats.OpenTickets)
	}
	var found recordsBody
	partnersCall(t, api, "GET", "/api/records?entity=ticket&search=login", "", 200, &found)
	checkJSON(t, "records", found.Records, `[{"id":"`+login+`","label":"Login fails","domain":null}]`)

	// Notes on a ticket.
	partnersCall(t, api, "POST", "/api/activities", `{"entity_type":"ticket","entity_id":"`+login+`","type":"note","body":"Asked for a screenshot"}`, 201, nil)

	// Deleting the customer user leaves the ticket, without them.
	mustRun(t, context.Background(), `DELETE FROM customer_users WHERE id = 'cu-ann'`)
	partnersCall(t, api, "GET", "/api/tickets/"+login, "", 200, &one)
	if one.Ticket.CustomerUserID != nil {
		t.Fatalf("after the user went: %+v", one.Ticket)
	}
	partnersCall(t, api, "POST", "/api/tickets/bulk-delete", `{"ids":["`+invoice+`"]}`, 200, nil)
	partnersCall(t, api, "DELETE", "/api/tickets/"+login, "", 200, nil)
	partnersCall(t, api, "GET", "/api/tickets/"+login, "", 404, nil)
	raw := partnersCall(t, api, "GET", "/api/views?entity=ticket", "", 200, nil)
	if !strings.Contains(raw, `"name":"All tickets"`) {
		t.Fatalf("views: %s", raw)
	}
}
