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

// ── Companies ──────────────────────────────────────────────────────
//
// Route order: TS registered GET /api/companies/aggregates before
// /api/companies/{id} (which would otherwise take "aggregates" as an id), and
// POST /api/companies/bulk-delete beside PUT and DELETE /api/companies/{id}.
// ServeMux prefers the literal paths whatever the order, which is the same;
// and as in Hono, PUT and DELETE on a literal path ("aggregates",
// "bulk-delete") reach {id}, while any other method on them is not a route.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/companies", app.listCompanies)
		rt.Handle("GET /api/companies/aggregates", app.companiesAggregates)
		rt.Handle("POST /api/companies", app.createCompany)
		rt.Handle("PUT /api/companies/{id}", app.updateCompany)
		rt.Handle("DELETE /api/companies/{id}", app.deleteCompany)
		rt.Handle("POST /api/companies/bulk-delete", app.bulkDeleteCompanies)
	})
}

// companiesWhere is the companies list's WHERE: search, industry and filters.
// Shared by the list and its footer aggregates, so both see the same rows.
func companiesWhere(q map[string]any, cols map[string]bool, many map[string]ManyLink) (string, []any, error) {
	where := []string{}
	params := []any{}
	search := js.Trim(textAt(q, "search"))
	industry := js.Trim(textAt(q, "industry"))
	if search != "" {
		where = append(where, "(name LIKE ? OR domain LIKE ? OR email LIKE ?)")
		params = append(params, "%"+search+"%", "%"+search+"%", "%"+search+"%")
	}
	if industry != "" {
		where = append(where, "industry = ?")
		params = append(params, industry)
	}
	clauses, flt, err := BuildFilters(cols, textAt(q, "filters"), "c.", TzOffsetOf(textAt(q, "tz")), many, time.Now())
	if err != nil {
		return "", nil, err
	}
	return whereOf(append(where, clauses...)), append(params, flt...), nil
}

// companiesAggregates is the list footer's aggregates over the same rows as
// the list. A plain route: the query is not validated.
func (app *App) companiesAggregates(w http.ResponseWriter, r *http.Request) error {
	q := firstQueryValues(r)
	ctx := r.Context()
	return tryList(w, func() (int, any, error) {
		cols, err := TableColumns(ctx, "companies")
		if err != nil {
			return 0, nil, err
		}
		many, err := ManyLinks(ctx, Company)
		if err != nil {
			return 0, nil, err
		}
		whereSQL, params, err := companiesWhere(q, cols, many)
		if err != nil {
			return 0, nil, err
		}
		columns := map[string]AggregateColumn{
			"name":     {SQL: "c.name", Kind: KindText},
			"domain":   {SQL: "c.domain", Kind: KindText},
			"industry": {SQL: "c.industry", Kind: KindText},
			"contacts": {SQL: "(SELECT COUNT(*) FROM contacts WHERE company_id = c.id)", Kind: KindNumber},
		}
		custom, err := CustomAggregateColumns(ctx, Company, "c", cols)
		if err != nil {
			return 0, nil, err
		}
		for k, v := range custom {
			columns[k] = v
		}
		values, err := AggregateColumns(ctx, "companies c", whereSQL, params, columns, textAt(q, "ops"))
		return 200, valuesBody{values}, err
	})
}

type valuesBody struct {
	Values map[string]any `json:"values"`
}

type companiesPage struct {
	Companies []db.Row `json:"companies"`
	Total     any      `json:"total"`
	Page      float64  `json:"page"`
	Limit     float64  `json:"limit"`
}

// listCompanies lists companies with pagination, search (name, domain,
// email), industry and filters, sorted by any real column (incl. custom
// fields), a linked record's name, or "count:<many side>". Each has its
// contact_count and relations.
func (app *App) listCompanies(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := paginationQueryIssues(q, "industry"); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryList(w, func() (int, any, error) {
		page, limit, offset := listPage(q)
		cols, err := TableColumns(ctx, "companies")
		if err != nil {
			return 0, nil, err
		}
		many, err := ManyLinks(ctx, Company)
		if err != nil {
			return 0, nil, err
		}
		sortSQL, err := listSortSQL(ctx, Company, "c", textAt(q, "sort"), cols, many)
		if err != nil {
			return 0, nil, err
		}
		order := listOrder(q)
		whereSQL, params, err := companiesWhere(q, cols, many)
		if err != nil {
			return 0, nil, err
		}
		count, err := db.Get(ctx, "SELECT COUNT(*) as total FROM companies c"+whereSQL, params...)
		if err != nil {
			return 0, nil, err
		}
		rows, err := db.Query(ctx, fmt.Sprintf(`SELECT c.*, (SELECT COUNT(*) FROM contacts WHERE company_id = c.id) as contact_count
       FROM companies c%s ORDER BY %s %s, c.id LIMIT ? OFFSET ?`, whereSQL, sortSQL, order), append(params, limit, offset)...)
		if err != nil {
			return 0, nil, err
		}
		companies, err := WithRelations(ctx, Company, rows, 3)
		return 200, companiesPage{companies, orZero(count, "total"), page, limit}, err
	})
}

type companyBody struct {
	Company db.Row `json:"company,omitempty"` // a row gone missing was undefined: no key
}

var createCompanyFields = []stringField{
	{key: "name", min: 1},
	{key: "domain", optional: true},
	{key: "industry", optional: true},
	{key: "phone", optional: true},
	{key: "email", optional: true},
	{key: "notes", optional: true},
}

// trimmedText is `(body.<key> || "").trim()` on a validated body, where the
// key is a string or absent.
func trimmedText(body map[string]any, key string) string {
	s, _ := body[key].(string)
	return js.Trim(s)
}

// orNull is `s || null`.
func orNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// readCompany is a company as the write routes answer it: every column and
// its relations.
func readCompany(ctx context.Context, id string) (companyBody, error) {
	row, err := db.Get(ctx, "SELECT * FROM companies WHERE id = ?", id)
	if err != nil {
		return companyBody{}, err
	}
	row, err = WithRelationsOne(ctx, Company, row)
	if err != nil {
		return companyBody{}, err
	}
	return companyBody{row}, withMainContact(ctx, row)
}

// withMainContact sets a company row's `main_contact`: its main contact as a
// record ({id, label}), or null when it has none, or the contact has since
// moved to another company. No query when it has none.
func withMainContact(ctx context.Context, row db.Row) error {
	if row == nil {
		return nil
	}
	row["main_contact"] = nil
	id := colText(row["main_contact_id"])
	if id == "" {
		return nil
	}
	found, err := db.Get(ctx, "SELECT "+recordColumns(Contact, "t")+" FROM contacts t WHERE t.id = ? AND t.company_id = ?", id, row["id"])
	if err != nil || found == nil {
		return err
	}
	row["main_contact"] = toRecord(found)
	return nil
}

// mainContactProblem is the 400 answer for a main_contact_id that isn't one
// of the company's contacts; "" when it is.
func mainContactProblem(ctx context.Context, companyID, contactID string) (string, error) {
	found, err := db.Get(ctx, "SELECT id FROM contacts WHERE id = ? AND company_id = ?", contactID, companyID)
	if err != nil || found != nil {
		return "", err
	}
	return fmt.Sprintf(`main_contact_id: "%s" is not one of this company's contacts.`, contactID), nil
}

// createCompany creates a company. Custom fields may be sent flat or in
// `custom`; an unknown field is 422, a missing required one 400. A value an
// enum field doesn't allow fails after the row is written (500), as in TS.
func (app *App) createCompany(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := writeBodyIssues(v, createCompanyFields); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		custom, refused, answer, err := customWrite(ctx, Company, body, web.RawBody(r), "create")
		if err != nil || refused != 0 {
			return refused, answer, err
		}
		name := trimmedText(body, "name")
		if name == "" {
			return fail(400, "Name is required")
		}
		if trimmedText(body, "main_contact_id") != "" {
			return fail(400, "main_contact_id: a new company has no contacts yet. Set it once it has one")
		}

		id := js.UUID()
		if _, err := db.Run(ctx, "INSERT INTO companies (id, name, domain, industry, phone, email, notes) VALUES (?, ?, ?, ?, ?, ?, ?)",
			id, name, trimmedText(body, "domain"), trimmedText(body, "industry"), trimmedText(body, "phone"), trimmedText(body, "email"), trimmedText(body, "notes")); err != nil {
			return 0, nil, err
		}

		if err := custom.apply(ctx, Company, "companies", id); err != nil {
			return 0, nil, err
		}

		inserted, err := readCompany(ctx, id)
		return 201, inserted, err
	})
}

var updateCompanyFields = []stringField{
	{key: "name", optional: true},
	{key: "domain", optional: true},
	{key: "industry", optional: true},
	{key: "phone", optional: true},
	{key: "email", optional: true},
	{key: "notes", optional: true},
	// YYYY-MM-DD makes the company a customer from that day; null or "" makes
	// it not a customer.
	{key: "customer_since", optional: true, nullable: true},
	// The next renewal as YYYY-MM-DD; null or "" clears it. Move it on after
	// each renewal.
	{key: "renewal_date", optional: true, nullable: true},
	// One of the company's contacts; null or "" clears it.
	{key: "main_contact_id", optional: true, nullable: true},
}

// updateCompany updates the fields sent. A field sent as null is not a field
// left out: null is a zod error for the text fields, and clears
// customer_since, renewal_date and main_contact_id.
func (app *App) updateCompany(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := writeBodyIssues(v, updateCompanyFields); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	id := r.PathValue("id")
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		if id == "" {
			return fail(400, "Invalid ID")
		}
		custom, refused, answer, err := customWrite(ctx, Company, body, web.RawBody(r), "update")
		if err != nil || refused != 0 {
			return refused, answer, err
		}
		fields := []string{}
		params := []any{}

		for _, key := range []string{"name", "domain", "industry", "phone", "email", "notes"} {
			if s, sent := body[key].(string); sent {
				fields = append(fields, key+" = ?")
				params = append(params, js.Trim(s))
			}
		}
		if v, present := body["customer_since"]; present {
			day, _ := v.(string) // null reads as ""
			day = js.Trim(day)
			if day != "" && !IsDay(day) {
				return fail(400, "customer_since must be a date as YYYY-MM-DD")
			}
			fields = append(fields, "customer_since = ?")
			params = append(params, orNull(day))
		}
		if v, present := body["renewal_date"]; present {
			day, _ := v.(string)
			day = js.Trim(day)
			if day != "" && !IsDay(day) {
				return fail(400, "renewal_date must be a date as YYYY-MM-DD")
			}
			fields = append(fields, "renewal_date = ?")
			params = append(params, orNull(day))
		}
		mainContact, mainSent := body["main_contact_id"]
		mainContactID, _ := mainContact.(string)
		mainContactID = js.Trim(mainContactID)
		if mainSent {
			fields = append(fields, "main_contact_id = ?")
			params = append(params, orNull(mainContactID))
		}

		if len(fields) == 0 && len(custom.values) == 0 {
			return fail(400, "No fields to update")
		}

		exists, err := db.Get(ctx, "SELECT id FROM companies WHERE id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if exists == nil {
			return fail(404, "Company not found")
		}
		if mainContactID != "" {
			problem, err := mainContactProblem(ctx, id, mainContactID)
			if err != nil {
				return 0, nil, err
			}
			if problem != "" {
				return fail(400, problem)
			}
		}

		if len(fields) > 0 {
			fields = append(fields, "updated_at = datetime('now')")
			params = append(params, id)
			if _, err := db.Run(ctx, "UPDATE companies SET "+strings.Join(fields, ", ")+" WHERE id = ?", params...); err != nil {
				return 0, nil, err
			}
		}
		if err := custom.apply(ctx, Company, "companies", id); err != nil {
			return 0, nil, err
		}

		updated, err := readCompany(ctx, id)
		return 200, updated, err
	})
}

// deleteCompany deletes a company. Its contacts and deals keep living without
// it (their company_id is cleared by the foreign keys); custom relations to
// it are cleared here.
func (app *App) deleteCompany(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	return tryJSON(w, func() (int, any, error) {
		if id == "" {
			return fail(400, "Invalid ID")
		}
		res, err := db.Run(ctx, "DELETE FROM companies WHERE id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if res.Changes == 0 {
			return fail(404, "Company not found")
		}
		if err := DetachRelations(ctx, Company, []string{id}); err != nil {
			return 0, nil, err
		}
		return 200, okBody{true}, nil
	})
}

// bulkDeleteCompanies deletes several companies (see bulkDelete).
func (app *App) bulkDeleteCompanies(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := bulkIDsIssues(v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		out, err := bulkDelete(ctx, Company, v.(map[string]any)["ids"].([]any))
		return 200, out, err
	})
}
