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

// ── Contacts ───────────────────────────────────────────────────────
//
// Route order: as for companies (routes_companies.go).

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/contacts", app.listContacts)
		rt.Handle("GET /api/contacts/aggregates", app.contactsAggregates)
		rt.Handle("POST /api/contacts", app.createContact)
		rt.Handle("PUT /api/contacts/{id}", app.updateContact)
		rt.Handle("DELETE /api/contacts/{id}", app.deleteContact)
		rt.Handle("POST /api/contacts/bulk-delete", app.bulkDeleteContacts)
	})
}

// contactsWhere is the contacts list's WHERE: search, status, company and
// filters. Shared by the list and its footer aggregates, so both see the same
// rows.
func contactsWhere(q map[string]any, cols map[string]bool, many map[string]ManyLink) (string, []any, error) {
	where := []string{}
	params := []any{}
	search := js.Trim(textAt(q, "search"))
	status := js.Trim(textAt(q, "status"))
	companyID := textAt(q, "company_id")
	if search != "" {
		// Match the contact's own fields OR their company name, so searching a
		// company surfaces its contacts (both queries LEFT JOIN companies as `co`).
		where = append(where, "(ct.first_name LIKE ? OR ct.last_name LIKE ? OR ct.email LIKE ? OR ct.title LIKE ? OR co.name LIKE ?)")
		like := "%" + search + "%"
		params = append(params, like, like, like, like, like)
	}
	if status != "" {
		where = append(where, "ct.status = ?")
		params = append(params, status)
	}
	if companyID != "" {
		where = append(where, "ct.company_id = ?")
		params = append(params, companyID)
	}
	clauses, flt, err := BuildFilters(cols, textAt(q, "filters"), "ct.", TzOffsetOf(textAt(q, "tz")), many, time.Now())
	if err != nil {
		return "", nil, err
	}
	return whereOf(append(where, clauses...)), append(params, flt...), nil
}

// contactsAggregates is the list footer's aggregates over the same rows as
// the list. A plain route: the query is not validated.
func (app *App) contactsAggregates(w http.ResponseWriter, r *http.Request) error {
	q := firstQueryValues(r)
	ctx := r.Context()
	return tryList(w, func() (int, any, error) {
		cols, err := TableColumns(ctx, "contacts")
		if err != nil {
			return 0, nil, err
		}
		many, err := ManyLinks(ctx, Contact)
		if err != nil {
			return 0, nil, err
		}
		whereSQL, params, err := contactsWhere(q, cols, many)
		if err != nil {
			return 0, nil, err
		}
		columns := map[string]AggregateColumn{
			"name":    {SQL: "TRIM(COALESCE(ct.first_name, '') || ' ' || COALESCE(ct.last_name, ''))", Kind: KindText},
			"email":   {SQL: "ct.email", Kind: KindText},
			"phone":   {SQL: "ct.phone", Kind: KindText},
			"company": {SQL: "ct.company_id", Kind: KindText},
			"title":   {SQL: "ct.title", Kind: KindText},
			"status":  {SQL: "ct.status", Kind: KindText},
		}
		custom, err := CustomAggregateColumns(ctx, Contact, "ct", cols)
		if err != nil {
			return 0, nil, err
		}
		for k, v := range custom { // a custom field named like a footer column wins, as the spread did
			columns[k] = v
		}
		values, err := AggregateColumns(ctx, "contacts ct LEFT JOIN companies co ON ct.company_id = co.id", whereSQL, params, columns, textAt(q, "ops"))
		return 200, valuesBody{values}, err
	})
}

type contactsPage struct {
	Contacts []db.Row `json:"contacts"`
	Total    any      `json:"total"`
	Page     float64  `json:"page"`
	Limit    float64  `json:"limit"`
}

// listContacts lists contacts with pagination, search (their own fields or
// their company's name), status, company and filters, sorted as companies
// are (company_id sorts by the company's name). Each has its company's name
// and domain, and relations.
func (app *App) listContacts(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := paginationQueryIssues(q, "status", "company_id"); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryList(w, func() (int, any, error) {
		page, limit, offset := listPage(q)
		cols, err := TableColumns(ctx, "contacts")
		if err != nil {
			return 0, nil, err
		}
		many, err := ManyLinks(ctx, Contact)
		if err != nil {
			return 0, nil, err
		}
		sortSQL, err := listSortSQL(ctx, Contact, "ct", textAt(q, "sort"), cols, many)
		if err != nil {
			return 0, nil, err
		}
		order := listOrder(q)
		whereSQL, params, err := contactsWhere(q, cols, many)
		if err != nil {
			return 0, nil, err
		}
		count, err := db.Get(ctx, "SELECT COUNT(*) as total FROM contacts ct LEFT JOIN companies co ON ct.company_id = co.id"+whereSQL, params...)
		if err != nil {
			return 0, nil, err
		}
		rows, err := db.Query(ctx, fmt.Sprintf(`SELECT ct.*, co.name as company_name, co.domain as company_domain
       FROM contacts ct
       LEFT JOIN companies co ON ct.company_id = co.id
       %s
       ORDER BY %s %s, ct.id
       LIMIT ? OFFSET ?`, whereSQL, sortSQL, order), append(params, limit, offset)...)
		if err != nil {
			return 0, nil, err
		}
		contacts, err := WithRelations(ctx, Contact, rows, 3)
		return 200, contactsPage{contacts, orZero(count, "total"), page, limit}, err
	})
}

type contactBody struct {
	Contact db.Row `json:"contact,omitempty"` // a row gone missing was undefined: no key
}

// readContact is a contact as the write routes answer it: every column, its
// company's name and domain, and its relations.
func readContact(ctx context.Context, id string) (contactBody, error) {
	row, err := db.Get(ctx, `SELECT ct.*, co.name as company_name, co.domain as company_domain
       FROM contacts ct LEFT JOIN companies co ON ct.company_id = co.id
       WHERE ct.id = ?`, id)
	if err != nil {
		return contactBody{}, err
	}
	row, err = WithRelationsOne(ctx, Contact, row)
	return contactBody{row}, err
}

var createContactFields = []stringField{
	{key: "first_name", min: 1},
	{key: "last_name", optional: true},
	{key: "email", optional: true},
	{key: "phone", optional: true},
	{key: "company_id", optional: true, nullable: true},
	{key: "title", optional: true},
	{key: "status", optional: true},
}

// createContact creates a contact (status "lead" unless sent). Custom fields
// as for companies.
func (app *App) createContact(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := writeBodyIssues(v, createContactFields); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		custom, refused, answer, err := customWrite(ctx, Contact, body, web.RawBody(r), "create")
		if err != nil || refused != 0 {
			return refused, answer, err
		}
		firstName := trimmedText(body, "first_name")
		if firstName == "" {
			return fail(400, "First name is required")
		}

		// Link to the chosen company, or infer one from the work-email domain
		// (skips free providers) so a contact never lands orphaned when its email
		// clearly belongs to a company.
		companyID := orNull(textAt(body, "company_id"))
		if email := textAt(body, "email"); companyID == nil && email != "" {
			if dom := WorkEmailDomain(email); dom != "" {
				if companyID, err = FindOrCreateCompanyByDomain(ctx, dom); err != nil {
					return 0, nil, err
				}
			}
		}
		status := textAt(body, "status")
		if status == "" {
			status = "lead"
		}

		id := js.UUID()
		if _, err := db.Run(ctx, "INSERT INTO contacts (id, first_name, last_name, email, phone, company_id, title, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			id, firstName, trimmedText(body, "last_name"), trimmedText(body, "email"), trimmedText(body, "phone"), companyID, trimmedText(body, "title"), js.Trim(status)); err != nil {
			return 0, nil, err
		}

		if err := custom.apply(ctx, Contact, "contacts", id); err != nil {
			return 0, nil, err
		}

		inserted, err := readContact(ctx, id)
		return 201, inserted, err
	})
}

var updateContactFields = []stringField{
	{key: "first_name", optional: true},
	{key: "last_name", optional: true},
	{key: "email", optional: true},
	{key: "phone", optional: true},
	{key: "company_id", optional: true, nullable: true},
	{key: "title", optional: true},
	{key: "status", optional: true},
}

// updateContact updates the fields sent; company_id null or "" unlinks the
// company.
func (app *App) updateContact(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := writeBodyIssues(v, updateContactFields); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	id := r.PathValue("id")
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		if id == "" {
			return fail(400, "Invalid ID")
		}
		custom, refused, answer, err := customWrite(ctx, Contact, body, web.RawBody(r), "update")
		if err != nil || refused != 0 {
			return refused, answer, err
		}
		fields := []string{}
		params := []any{}

		for _, key := range []string{"first_name", "last_name", "email", "phone", "title", "status"} {
			if s, sent := body[key].(string); sent {
				fields = append(fields, key+" = ?")
				params = append(params, js.Trim(s))
			}
		}
		if v, sent := body["company_id"]; sent {
			fields = append(fields, "company_id = ?")
			s, _ := v.(string) // null reads as ""
			params = append(params, orNull(s))
		}

		if len(fields) == 0 && len(custom.values) == 0 {
			return fail(400, "No fields to update")
		}

		exists, err := db.Get(ctx, "SELECT id FROM contacts WHERE id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if exists == nil {
			return fail(404, "Contact not found")
		}

		if len(fields) > 0 {
			fields = append(fields, "updated_at = datetime('now')")
			params = append(params, id)
			if _, err := db.Run(ctx, "UPDATE contacts SET "+strings.Join(fields, ", ")+" WHERE id = ?", params...); err != nil {
				return 0, nil, err
			}
		}
		if err := custom.apply(ctx, Contact, "contacts", id); err != nil {
			return 0, nil, err
		}

		updated, err := readContact(ctx, id)
		return 200, updated, err
	})
}

// deleteContact deletes a contact. Its deals and emails are handled by the
// foreign keys; custom relations to it are cleared here.
func (app *App) deleteContact(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	return tryJSON(w, func() (int, any, error) {
		if id == "" {
			return fail(400, "Invalid ID")
		}
		res, err := db.Run(ctx, "DELETE FROM contacts WHERE id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if res.Changes == 0 {
			return fail(404, "Contact not found")
		}
		if err := DetachRelations(ctx, Contact, []string{id}); err != nil {
			return 0, nil, err
		}
		return 200, okBody{true}, nil
	})
}

// bulkDeleteContacts deletes several contacts (see bulkDelete).
func (app *App) bulkDeleteContacts(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := bulkIDsIssues(v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		out, err := bulkDelete(ctx, Contact, v.(map[string]any)["ids"].([]any))
		return 200, out, err
	})
}
