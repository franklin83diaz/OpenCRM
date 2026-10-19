package crm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── Customer users ─────────────────────────────────────────────────
//
// The people who use the product at a customer (a company with
// customer_since): a record type of their own, apart from contacts, who are
// the people we deal with. Each belongs to a company (company_id, optional).
// They came after the port from TS, so nothing here has a TS original to
// match.
//
// Route order: ServeMux prefers the literal paths (aggregates, bulk-delete)
// to {id} whatever the order.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/customer-users", app.listCustomerUsers)
		rt.Handle("GET /api/customer-users/aggregates", app.customerUsersAggregates)
		rt.Handle("GET /api/customer-users/{id}", app.getCustomerUser)
		rt.Handle("POST /api/customer-users", app.createCustomerUser)
		rt.Handle("PUT /api/customer-users/{id}", app.updateCustomerUser)
		rt.Handle("DELETE /api/customer-users/{id}", app.deleteCustomerUser)
		rt.Handle("POST /api/customer-users/bulk-delete", app.bulkDeleteCustomerUsers)
	})
}

// customerUserFrom is a customer user row with its company's name and domain, over alias u.
const customerUserFrom = ` FROM customer_users u LEFT JOIN companies co ON u.company_id = co.id`

const customerUserSelect = `SELECT u.*, co.name as company_name, co.domain as company_domain` + customerUserFrom

// customerUsersWhere is the list's WHERE: search (name, email, phone typed any way), company_id
// and filters. Shared by the list and its footer aggregates.
func customerUsersWhere(q map[string]any, cols map[string]bool, many map[string]ManyLink) (string, []any, error) {
	where := []string{}
	params := []any{}
	if search := js.Trim(textAt(q, "search")); search != "" {
		like := "%" + search + "%"
		cond := "u.name LIKE ? OR u.email LIKE ?"
		params = append(params, like, like)
		// A phone number finds theirs however either was written.
		if digits := phoneDigits(search); digits != "" {
			cond += " OR " + bareDigitsSQL("u.phone") + " LIKE ?"
			params = append(params, "%"+digits+"%")
		}
		where = append(where, "("+cond+")")
	}
	if companyID := textAt(q, "company_id"); companyID != "" {
		where = append(where, "u.company_id = ?")
		params = append(params, companyID)
	}
	clauses, flt, err := BuildFilters(cols, textAt(q, "filters"), "u.", TzOffsetOf(textAt(q, "tz")), many, time.Now())
	if err != nil {
		return "", nil, err
	}
	return whereOf(append(where, clauses...)), append(params, flt...), nil
}

// customerUsersAggregates is the list footer's aggregates over the same rows.
func (app *App) customerUsersAggregates(w http.ResponseWriter, r *http.Request) error {
	q := firstQueryValues(r)
	ctx := r.Context()
	return tryList(w, func() (int, any, error) {
		cols, err := TableColumns(ctx, "customer_users")
		if err != nil {
			return 0, nil, err
		}
		many, err := ManyLinks(ctx, CustomerUser)
		if err != nil {
			return 0, nil, err
		}
		whereSQL, params, err := customerUsersWhere(q, cols, many)
		if err != nil {
			return 0, nil, err
		}
		columns := map[string]AggregateColumn{
			"name":    {SQL: "u.name", Kind: KindText},
			"email":   {SQL: "u.email", Kind: KindText},
			"phone":   {SQL: "u.phone", Kind: KindText},
			"company": {SQL: "u.company_id", Kind: KindText},
		}
		custom, err := CustomAggregateColumns(ctx, CustomerUser, "u", cols)
		if err != nil {
			return 0, nil, err
		}
		for k, v := range custom {
			columns[k] = v
		}
		values, err := AggregateColumns(ctx, "customer_users u LEFT JOIN companies co ON u.company_id = co.id", whereSQL, params, columns, textAt(q, "ops"))
		return 200, valuesBody{values}, err
	})
}

type customerUsersPage struct {
	CustomerUsers []db.Row `json:"customer_users"`
	Total         any      `json:"total"`
	Page          float64  `json:"page"`
	Limit         float64  `json:"limit"`
}

// listCustomerUsers lists customer users with pagination, search (name,
// email, phone), company_id and filters, sorted by any real column (company_id by
// the company's name). Each has its company's name and domain, and its
// relations.
func (app *App) listCustomerUsers(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := paginationQueryIssues(q, "company_id"); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryList(w, func() (int, any, error) {
		page, limit, offset := listPage(q)
		cols, err := TableColumns(ctx, "customer_users")
		if err != nil {
			return 0, nil, err
		}
		many, err := ManyLinks(ctx, CustomerUser)
		if err != nil {
			return 0, nil, err
		}
		sortSQL, err := listSortSQL(ctx, CustomerUser, "u", textAt(q, "sort"), cols, many)
		if err != nil {
			return 0, nil, err
		}
		whereSQL, params, err := customerUsersWhere(q, cols, many)
		if err != nil {
			return 0, nil, err
		}
		count, err := db.Get(ctx, "SELECT COUNT(*) as total"+customerUserFrom+whereSQL, params...)
		if err != nil {
			return 0, nil, err
		}
		rows, err := db.Query(ctx, fmt.Sprintf("%s%s ORDER BY %s %s, u.id LIMIT ? OFFSET ?", customerUserSelect, whereSQL, sortSQL, listOrder(q)), append(params, limit, offset)...)
		if err != nil {
			return 0, nil, err
		}
		users, err := WithRelations(ctx, CustomerUser, rows, 3)
		return 200, customerUsersPage{users, orZero(count, "total"), page, limit}, err
	})
}

type customerUserBody struct {
	CustomerUser db.Row `json:"customer_user,omitempty"`
}

// readCustomerUser is a customer user as the routes answer it; nil when there is none.
func readCustomerUser(ctx context.Context, id string) (db.Row, error) {
	row, err := db.Get(ctx, customerUserSelect+" WHERE u.id = ?", id)
	if err != nil {
		return nil, err
	}
	return WithRelationsOne(ctx, CustomerUser, row)
}

// getCustomerUser is one customer user.
func (app *App) getCustomerUser(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	return tryJSON(w, func() (int, any, error) {
		user, err := readCustomerUser(ctx, id)
		if err != nil {
			return 0, nil, err
		}
		if user == nil {
			return fail(404, "Customer user not found")
		}
		return 200, customerUserBody{user}, nil
	})
}

var customerUserTextKeys = []string{"name", "email", "phone", "notes"}

var createCustomerUserFields = []stringField{
	{key: "name", min: 1},
	{key: "email", optional: true},
	{key: "phone", optional: true},
	{key: "company_id", optional: true, nullable: true},
	{key: "notes", optional: true},
}

var updateCustomerUserFields = []stringField{
	{key: "name", optional: true},
	{key: "email", optional: true},
	{key: "phone", optional: true},
	{key: "company_id", optional: true, nullable: true},
	{key: "notes", optional: true},
}

// customerCompany is the company id names when it is a customer (it has a
// customer_since): its id, name and customer_since. Otherwise problem is the
// 400 answer, `key` naming the field: no such company, or not a customer.
func customerCompany(ctx context.Context, key, id string) (company db.Row, problem string, err error) {
	found, err := db.Get(ctx, "SELECT id, name, customer_since FROM companies WHERE id = ?", id)
	if err != nil {
		return nil, "", err
	}
	if found == nil {
		return nil, fmt.Sprintf(`%s: no company with id "%s".`, key, id), nil
	}
	if js.Trim(colText(found["customer_since"])) == "" {
		return found, fmt.Sprintf(`%s: %s is not a customer (no customer_since).`, key, colText(found["name"])), nil
	}
	return found, "", nil
}

// customerUserCompany reads a write's company_id: the company, or nil for
// none; sent is false when the write leaves it alone. problem is the 400
// answer for an id that names no company, or a company that isn't a customer
// (no customer_since). userID, on an update, is the customer user written:
// the company they already have stays theirs if it stopped being a customer.
func customerUserCompany(ctx context.Context, body map[string]any, userID string) (company any, sent bool, problem string, err error) {
	v, sent := body["company_id"]
	if !sent {
		return nil, false, "", nil
	}
	id, _ := v.(string) // validated: a string or null
	if id = js.Trim(id); id == "" {
		return nil, true, "", nil
	}
	found, problem, err := customerCompany(ctx, "company_id", id)
	if err != nil || problem == "" {
		return id, true, "", err
	}
	if found != nil {
		kept, err := db.Get(ctx, "SELECT id FROM customer_users WHERE id = ? AND company_id = ?", userID, id)
		if err != nil || kept != nil {
			return id, true, "", err
		}
	}
	return nil, true, problem, nil
}

// createCustomerUser creates a customer user. Custom fields may be sent flat
// or in `custom`; an unknown field is 422, a missing required one 400.
func (app *App) createCustomerUser(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := writeBodyIssues(v, createCustomerUserFields); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		custom, refused, answer, err := customWrite(ctx, CustomerUser, body, web.RawBody(r), "create")
		if err != nil || refused != 0 {
			return refused, answer, err
		}
		name := trimmedText(body, "name")
		if name == "" {
			return fail(400, "Name is required")
		}
		company, _, problem, err := customerUserCompany(ctx, body, "")
		if err != nil {
			return 0, nil, err
		}
		if problem != "" {
			return fail(400, problem)
		}
		id := js.UUID()
		if _, err := db.Run(ctx, "INSERT INTO customer_users (id, name, email, phone, company_id, notes) VALUES (?, ?, ?, ?, ?, ?)",
			id, name, trimmedText(body, "email"), trimmedText(body, "phone"), company, trimmedText(body, "notes")); err != nil {
			return 0, nil, err
		}
		if err := custom.apply(ctx, CustomerUser, "customer_users", id); err != nil {
			return 0, nil, err
		}
		user, err := readCustomerUser(ctx, id)
		return 201, customerUserBody{user}, err
	})
}

// updateCustomerUser updates the fields sent; null or "" for company_id
// leaves it without a company. A name sent empty is refused.
func (app *App) updateCustomerUser(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := writeBodyIssues(v, updateCustomerUserFields); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	id := r.PathValue("id")
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		custom, refused, answer, err := customWrite(ctx, CustomerUser, body, web.RawBody(r), "update")
		if err != nil || refused != 0 {
			return refused, answer, err
		}
		fields := []string{}
		params := []any{}
		for _, key := range customerUserTextKeys {
			if s, sent := body[key].(string); sent {
				if key == "name" && js.Trim(s) == "" {
					return fail(400, "Name is required")
				}
				fields = append(fields, key+" = ?")
				params = append(params, js.Trim(s))
			}
		}
		company, companySent, problem, err := customerUserCompany(ctx, body, id)
		if err != nil {
			return 0, nil, err
		}
		if problem != "" {
			return fail(400, problem)
		}
		if companySent {
			fields = append(fields, "company_id = ?")
			params = append(params, company)
		}
		if len(fields) == 0 && len(custom.values) == 0 {
			return fail(400, "No fields to update")
		}
		exists, err := db.Get(ctx, "SELECT id FROM customer_users WHERE id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if exists == nil {
			return fail(404, "Customer user not found")
		}
		if len(fields) > 0 {
			fields = append(fields, "updated_at = datetime('now')")
			params = append(params, id)
			if _, err := db.Run(ctx, "UPDATE customer_users SET "+strings.Join(fields, ", ")+" WHERE id = ?", params...); err != nil {
				return 0, nil, err
			}
		}
		if err := custom.apply(ctx, CustomerUser, "customer_users", id); err != nil {
			return 0, nil, err
		}
		user, err := readCustomerUser(ctx, id)
		return 200, customerUserBody{user}, err
	})
}

// deleteCustomerUser deletes a customer user; custom relations to it are cleared.
func (app *App) deleteCustomerUser(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	return tryJSON(w, func() (int, any, error) {
		res, err := db.Run(ctx, "DELETE FROM customer_users WHERE id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if res.Changes == 0 {
			return fail(404, "Customer user not found")
		}
		if err := DetachRelations(ctx, CustomerUser, []string{id}); err != nil {
			return 0, nil, err
		}
		return 200, okBody{true}, nil
	})
}

// bulkDeleteCustomerUsers deletes several customer users (see bulkDelete).
func (app *App) bulkDeleteCustomerUsers(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := bulkIDsIssues(v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		out, err := bulkDelete(ctx, CustomerUser, v.(map[string]any)["ids"].([]any))
		return 200, out, err
	})
}
