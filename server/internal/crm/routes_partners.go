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

// ── Partners ───────────────────────────────────────────────────────
//
// Partners are the firms that work deals with us (resellers, referrers,
// integrators): a record type of their own beside companies, which are the
// accounts we sell to. A deal takes part with any number of them
// (deal_partners, written through a deal's partner_ids). They came after the
// port from TS, so nothing here has a TS original to match; the routes are
// the companies' own, without industry, customers and contacts.
//
// Route order: ServeMux prefers the literal paths (aggregates, bulk-delete)
// to {id} whatever the order.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/partners", app.listPartners)
		rt.Handle("GET /api/partners/aggregates", app.partnersAggregates)
		rt.Handle("GET /api/partners/{id}", app.getPartner)
		rt.Handle("POST /api/partners", app.createPartner)
		rt.Handle("PUT /api/partners/{id}", app.updatePartner)
		rt.Handle("DELETE /api/partners/{id}", app.deletePartner)
		rt.Handle("POST /api/partners/bulk-delete", app.bulkDeletePartners)
	})
}

// partnerDealCount is how many deals a partner takes part in, over alias p.
const partnerDealCount = "(SELECT COUNT(*) FROM deal_partners WHERE partner_id = p.id)"

// partnersWhere is the partners list's WHERE: search and filters. Shared by
// the list and its footer aggregates, so both see the same rows.
func partnersWhere(q map[string]any, cols map[string]bool, many map[string]ManyLink) (string, []any, error) {
	where := []string{}
	params := []any{}
	if search := js.Trim(textAt(q, "search")); search != "" {
		where = append(where, "(name LIKE ? OR domain LIKE ? OR email LIKE ?)")
		params = append(params, "%"+search+"%", "%"+search+"%", "%"+search+"%")
	}
	clauses, flt, err := BuildFilters(cols, textAt(q, "filters"), "p.", TzOffsetOf(textAt(q, "tz")), many, time.Now())
	if err != nil {
		return "", nil, err
	}
	return whereOf(append(where, clauses...)), append(params, flt...), nil
}

// partnersAggregates is the list footer's aggregates over the same rows as
// the list.
func (app *App) partnersAggregates(w http.ResponseWriter, r *http.Request) error {
	q := firstQueryValues(r)
	ctx := r.Context()
	return tryList(w, func() (int, any, error) {
		cols, err := TableColumns(ctx, "partners")
		if err != nil {
			return 0, nil, err
		}
		many, err := ManyLinks(ctx, Partner)
		if err != nil {
			return 0, nil, err
		}
		whereSQL, params, err := partnersWhere(q, cols, many)
		if err != nil {
			return 0, nil, err
		}
		columns := map[string]AggregateColumn{
			"name":   {SQL: "p.name", Kind: KindText},
			"domain": {SQL: "p.domain", Kind: KindText},
			"email":  {SQL: "p.email", Kind: KindText},
			"phone":  {SQL: "p.phone", Kind: KindText},
			"deals":  {SQL: partnerDealCount, Kind: KindNumber},
		}
		custom, err := CustomAggregateColumns(ctx, Partner, "p", cols)
		if err != nil {
			return 0, nil, err
		}
		for k, v := range custom {
			columns[k] = v
		}
		values, err := AggregateColumns(ctx, "partners p", whereSQL, params, columns, textAt(q, "ops"))
		return 200, valuesBody{values}, err
	})
}

type partnersPage struct {
	Partners []db.Row `json:"partners"`
	Total    any      `json:"total"`
	Page     float64  `json:"page"`
	Limit    float64  `json:"limit"`
}

// listPartners lists partners with pagination, search (name, domain, email)
// and filters, sorted by any real column (incl. custom fields), a linked
// record's name, or "count:deals". Each has its deal_count and relations.
func (app *App) listPartners(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := paginationQueryIssues(q); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryList(w, func() (int, any, error) {
		page, limit, offset := listPage(q)
		cols, err := TableColumns(ctx, "partners")
		if err != nil {
			return 0, nil, err
		}
		many, err := ManyLinks(ctx, Partner)
		if err != nil {
			return 0, nil, err
		}
		sortSQL, err := listSortSQL(ctx, Partner, "p", textAt(q, "sort"), cols, many)
		if err != nil {
			return 0, nil, err
		}
		whereSQL, params, err := partnersWhere(q, cols, many)
		if err != nil {
			return 0, nil, err
		}
		count, err := db.Get(ctx, "SELECT COUNT(*) as total FROM partners p"+whereSQL, params...)
		if err != nil {
			return 0, nil, err
		}
		rows, err := db.Query(ctx, fmt.Sprintf(`SELECT p.*, %s as deal_count
       FROM partners p%s ORDER BY %s %s, p.id LIMIT ? OFFSET ?`, partnerDealCount, whereSQL, sortSQL, listOrder(q)), append(params, limit, offset)...)
		if err != nil {
			return 0, nil, err
		}
		partners, err := WithRelations(ctx, Partner, rows, 3)
		return 200, partnersPage{partners, orZero(count, "total"), page, limit}, err
	})
}

type partnerBody struct {
	Partner db.Row `json:"partner,omitempty"`
}

// readPartner is a partner as the routes answer it: every column, its
// deal_count and its relations; nil when there is none.
func readPartner(ctx context.Context, id string) (db.Row, error) {
	row, err := db.Get(ctx, "SELECT p.*, "+partnerDealCount+" as deal_count FROM partners p WHERE p.id = ?", id)
	if err != nil {
		return nil, err
	}
	return WithRelationsOne(ctx, Partner, row)
}

// getPartner is one partner.
func (app *App) getPartner(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	return tryJSON(w, func() (int, any, error) {
		partner, err := readPartner(ctx, id)
		if err != nil {
			return 0, nil, err
		}
		if partner == nil {
			return fail(404, "Partner not found")
		}
		return 200, partnerBody{partner}, nil
	})
}

var partnerTextKeys = []string{"name", "domain", "phone", "email", "notes"}

var createPartnerFields = []stringField{
	{key: "name", min: 1},
	{key: "domain", optional: true},
	{key: "phone", optional: true},
	{key: "email", optional: true},
	{key: "notes", optional: true},
}

// createPartner creates a partner. Custom fields may be sent flat or in
// `custom`; an unknown field is 422, a missing required one 400.
func (app *App) createPartner(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := writeBodyIssues(v, createPartnerFields); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		custom, refused, answer, err := customWrite(ctx, Partner, body, web.RawBody(r), "create")
		if err != nil || refused != 0 {
			return refused, answer, err
		}
		name := trimmedText(body, "name")
		if name == "" {
			return fail(400, "Name is required")
		}
		id := js.UUID()
		if _, err := db.Run(ctx, "INSERT INTO partners (id, name, domain, phone, email, notes) VALUES (?, ?, ?, ?, ?, ?)",
			id, name, trimmedText(body, "domain"), trimmedText(body, "phone"), trimmedText(body, "email"), trimmedText(body, "notes")); err != nil {
			return 0, nil, err
		}
		if err := custom.apply(ctx, Partner, "partners", id); err != nil {
			return 0, nil, err
		}
		partner, err := readPartner(ctx, id)
		return 201, partnerBody{partner}, err
	})
}

var updatePartnerFields = []stringField{
	{key: "name", optional: true},
	{key: "domain", optional: true},
	{key: "phone", optional: true},
	{key: "email", optional: true},
	{key: "notes", optional: true},
}

// updatePartner updates the fields sent. A name sent empty is refused: a
// partner always has one.
func (app *App) updatePartner(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := writeBodyIssues(v, updatePartnerFields); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	id := r.PathValue("id")
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		custom, refused, answer, err := customWrite(ctx, Partner, body, web.RawBody(r), "update")
		if err != nil || refused != 0 {
			return refused, answer, err
		}
		fields := []string{}
		params := []any{}
		for _, key := range partnerTextKeys {
			if s, sent := body[key].(string); sent {
				if key == "name" && js.Trim(s) == "" {
					return fail(400, "Name is required")
				}
				fields = append(fields, key+" = ?")
				params = append(params, js.Trim(s))
			}
		}
		if len(fields) == 0 && len(custom.values) == 0 {
			return fail(400, "No fields to update")
		}
		exists, err := db.Get(ctx, "SELECT id FROM partners WHERE id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if exists == nil {
			return fail(404, "Partner not found")
		}
		if len(fields) > 0 {
			fields = append(fields, "updated_at = datetime('now')")
			params = append(params, id)
			if _, err := db.Run(ctx, "UPDATE partners SET "+strings.Join(fields, ", ")+" WHERE id = ?", params...); err != nil {
				return 0, nil, err
			}
		}
		if err := custom.apply(ctx, Partner, "partners", id); err != nil {
			return 0, nil, err
		}
		partner, err := readPartner(ctx, id)
		return 200, partnerBody{partner}, err
	})
}

// deletePartner deletes a partner. Its deals stay, without it (deal_partners
// drops the links); custom relations to it are cleared here.
func (app *App) deletePartner(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	return tryJSON(w, func() (int, any, error) {
		res, err := db.Run(ctx, "DELETE FROM partners WHERE id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if res.Changes == 0 {
			return fail(404, "Partner not found")
		}
		if err := DetachRelations(ctx, Partner, []string{id}); err != nil {
			return 0, nil, err
		}
		return 200, okBody{true}, nil
	})
}

// bulkDeletePartners deletes several partners (see bulkDelete).
func (app *App) bulkDeletePartners(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := bulkIDsIssues(v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		out, err := bulkDelete(ctx, Partner, v.(map[string]any)["ids"].([]any))
		return 200, out, err
	})
}

// ── A deal's partners ──────────────────────────────────────────────

// maxDealPartners caps partner_ids: a deal is worked with a few partners,
// and the check stays under D1's 100-parameter cap.
const maxDealPartners = 50

// dealPartnerIDs reads a write's partner_ids (validated as an array of
// strings): the ids once each; sent is false when the write leaves them
// alone. problem is the 400 answer for an id that names no
// partner.
func dealPartnerIDs(ctx context.Context, body map[string]any) (ids []string, sent bool, problem string, err error) {
	raw, sent := body["partner_ids"].([]any)
	if !sent {
		return nil, false, "", nil
	}
	ids = []string{}
	seen := map[string]bool{}
	for _, v := range raw {
		if id := js.Trim(v.(string)); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ids, true, "", nil
	}
	rows, err := db.Query(ctx, fmt.Sprintf("SELECT id FROM partners WHERE id IN (%s)", marks(len(ids))), stringParams(ids)...)
	if err != nil {
		return nil, true, "", err
	}
	found := map[string]bool{}
	for _, row := range rows {
		found[colText(row["id"])] = true
	}
	for _, id := range ids {
		if !found[id] {
			return nil, true, fmt.Sprintf(`partner_ids: no partner with id "%s".`, id), nil
		}
	}
	return ids, true, "", nil
}

// setDealPartners makes ids the deal's partners: the links not in ids go,
// and the new ones come. A partner deleted since dealPartnerIDs checked it is
// left out, not a failed write after the deal was saved.
func setDealPartners(ctx context.Context, dealID string, ids []string) error {
	if len(ids) == 0 {
		_, err := db.Run(ctx, "DELETE FROM deal_partners WHERE deal_id = ?", dealID)
		return err
	}
	params := append([]any{dealID}, stringParams(ids)...)
	if _, err := db.Run(ctx, fmt.Sprintf("DELETE FROM deal_partners WHERE deal_id = ? AND partner_id NOT IN (%s)", marks(len(ids))), params...); err != nil {
		return err
	}
	_, err := db.Run(ctx, fmt.Sprintf("INSERT OR IGNORE INTO deal_partners (deal_id, partner_id) SELECT ?, id FROM partners WHERE id IN (%s)", marks(len(ids))), params...)
	return err
}

// withDealPartners sets each deal row's `partners`: the partners taking part
// in it, as relation chips, by name ([] for none).
func withDealPartners(ctx context.Context, rows []db.Row) error {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		if r != nil {
			ids = append(ids, js.String(r["id"]))
		}
	}
	byDeal := map[string][]RelationRecord{}
	for _, part := range Chunk(ids, 90) {
		found, err := db.Query(ctx, fmt.Sprintf(`SELECT dp.deal_id AS deal, %s
       FROM deal_partners dp JOIN partners p ON p.id = dp.partner_id
       WHERE dp.deal_id IN (%s) ORDER BY p.name COLLATE NOCASE, p.id`, recordColumns(Partner, "p"), marks(len(part))), stringParams(part)...)
		if err != nil {
			return err
		}
		for _, rec := range found {
			deal := colText(rec["deal"])
			byDeal[deal] = append(byDeal[deal], toRecord(rec))
		}
	}
	for _, r := range rows {
		if r == nil {
			continue
		}
		if list := byDeal[js.String(r["id"])]; list != nil {
			r["partners"] = list
		} else {
			r["partners"] = []RelationRecord{}
		}
	}
	return nil
}
