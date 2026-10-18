package crm

import (
	"fmt"
	"net/http"
	"strings"

	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── Single records (for deep-linked detail views), records and values ──
//
// Plain routes: nothing is validated, and each query parameter is read as its
// first value.
//
// Route order: TS registered these GET /{id} routes last (index.ts 3656+),
// after every literal GET sibling (GET /api/contacts/aggregates,
// /api/companies/aggregates, /api/deals/board, /api/deals/progress), so the
// literals keep their own handlers, as ServeMux's most-specific rule has it.
// Literal paths of other methods (POST /api/contacts/import, …/bulk-delete)
// are ids to GET in both. GET /api/contacts/{id}/emails has a segment more.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/contacts/{id}", app.getContact)
		rt.Handle("GET /api/deals/{id}", app.getDeal)
		rt.Handle("GET /api/companies/{id}", app.getCompany)
		rt.Handle("GET /api/records", app.records)
		rt.Handle("GET /api/values", app.values)
	})
}

// getContact is one contact, with its company's name and domain and its
// relations. "all" is no contact: the list's own path in the app.
func (app *App) getContact(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	return tryJSON(w, func() (int, any, error) {
		if id == "" || id == "all" {
			return fail(404, "Not found")
		}
		contact, err := db.Get(ctx, `SELECT ct.*, co.name as company_name, co.domain as company_domain
       FROM contacts ct LEFT JOIN companies co ON ct.company_id = co.id
       WHERE ct.id = ?`, id)
		if err != nil {
			return 0, nil, err
		}
		if contact == nil {
			return fail(404, "Contact not found")
		}
		contact, err = WithRelationsOne(ctx, Contact, contact)
		return 200, contactBody{contact}, err
	})
}

type oneDealBody struct {
	Deal db.Row `json:"deal"`
}

// getDeal is one deal, as the deal routes return it, with its progress (null
// for a closed deal). `tz` is the viewer's UTC offset in minutes.
func (app *App) getDeal(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	tz := TzOffsetOf(textAt(firstQueryValues(r), "tz"))
	return tryJSON(w, func() (int, any, error) {
		if id == "" {
			return fail(404, "Not found")
		}
		if err := app.backfillDealCompanies(ctx); err != nil {
			return 0, nil, err
		}
		deal, err := db.Get(ctx, dealSelect+" WHERE d.id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if deal == nil {
			return fail(404, "Deal not found")
		}
		p, err := app.progressOf(ctx, []db.Row{deal}, app.now(), tz)
		if err != nil {
			return 0, nil, err
		}
		view, err := dealView(ctx, deal)
		if err != nil {
			return 0, nil, err
		}
		return 200, oneDealBody{withProgress(view, p, id)}, nil
	})
}

// getCompany is one company, with its contact_count, relations and main contact.
func (app *App) getCompany(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	return tryJSON(w, func() (int, any, error) {
		if id == "" {
			return fail(404, "Not found")
		}
		company, err := db.Get(ctx, `SELECT c.*, (SELECT COUNT(*) FROM contacts WHERE company_id = c.id) as contact_count
       FROM companies c WHERE c.id = ?`, id)
		if err != nil {
			return 0, nil, err
		}
		if company == nil {
			return fail(404, "Company not found")
		}
		company, err = WithRelationsOne(ctx, Company, company)
		if err != nil {
			return 0, nil, err
		}
		return 200, companyBody{company}, withMainContact(ctx, company)
	})
}

type recordsBody struct {
	Records []RelationRecord `json:"records"`
}

// records names records for relation chips and pickers: `search` matches the
// record's name; `ids` (comma-separated, at most 60) returns those records
// instead, to name ids a filter or form already holds. `customers=1`, with
// entity=company, searches only the companies that are customers.
func (app *App) records(w http.ResponseWriter, r *http.Request) error {
	q := firstQueryValues(r)
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		entity := textAt(q, "entity")
		if !IsEntityType(entity) {
			return fail(400, "entity must be contact, company, deal, partner, customer_user or ticket")
		}
		ids := []string{}
		for _, s := range strings.Split(textAt(q, "ids"), ",") {
			if s = js.Trim(s); s != "" {
				ids = append(ids, s)
			}
		}
		if len(ids) > 60 {
			return fail(400, "At most 60 ids")
		}
		customers := textAt(q, "customers")
		if customers == "1" || customers == "true" {
			if EntityType(entity) != Company {
				return fail(400, "customers is only for entity=company")
			}
			if len(ids) == 0 {
				records, err := SearchCustomers(ctx, textAt(q, "search"))
				return 200, recordsBody{records}, err
			}
		}
		records, err := SearchRecords(ctx, EntityType(entity), textAt(q, "search"), ids)
		return 200, recordsBody{records}, err
	})
}

type pickerValuesBody struct {
	Values []string `json:"values"`
}

// values is the values a column already holds, for a picker that offers them
// (an industry typed once is picked after). Any real column of the entity.
func (app *App) values(w http.ResponseWriter, r *http.Request) error {
	q := firstQueryValues(r)
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		entity := textAt(q, "entity")
		if !IsEntityType(entity) {
			return fail(400, "entity must be contact, company, deal, partner, customer_user or ticket")
		}
		table := EntityTables[EntityType(entity)]
		field := textAt(q, "field")
		cols, err := TableColumns(ctx, table)
		if err != nil {
			return 0, nil, err
		}
		if !cols[field] {
			return fail(400, `Unknown field "`+field+`"`)
		}
		col := QuoteIdent(field)
		// One per value ignoring case: "consulting" and "Consulting" are one option.
		rows, err := db.Query(ctx, fmt.Sprintf("SELECT MIN(%s) AS v FROM %s WHERE %s IS NOT NULL AND %s != '' GROUP BY %s COLLATE NOCASE ORDER BY v COLLATE NOCASE LIMIT 200", col, table, col, col, col))
		if err != nil {
			return 0, nil, err
		}
		values := make([]string, len(rows))
		for i, row := range rows {
			values[i] = js.String(row["v"])
		}
		return 200, pickerValuesBody{values}, nil
	})
}
