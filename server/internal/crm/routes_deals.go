package crm

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"opencrm/internal/db"
	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── Deals ──────────────────────────────────────────────────────────
//
// Route order: TS registered GET /api/deals/board and /api/deals/progress
// long before GET /api/deals/{id} (index.ts 3673, routes_records.go), so
// those literal paths keep their own handlers; ServeMux prefers them whatever
// the order, which is the same. As in Hono, PUT and DELETE on them reach
// {id}, and POST /api/deals/{id}/updates (routes_tasks.go) has a segment more.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/deals/board", app.dealsBoard)
		rt.Handle("GET /api/deals/progress", app.dealsProgress)
		rt.Handle("GET /api/deals", app.listDeals)
		rt.Handle("POST /api/deals", app.createDeal)
		rt.Handle("PUT /api/deals/{id}", app.updateDeal)
		rt.Handle("DELETE /api/deals/{id}", app.deleteDeal)
	})
}

// dealSelect is a deal row as every deal route returns it: its own columns
// plus the names of its contact and of its own company (deals.company_id, not
// the contact's).
const dealSelect = `SELECT d.*,
       ct.first_name as contact_first_name, ct.last_name as contact_last_name,
       co.name as company_name, co.domain as company_domain
  FROM deals d
  LEFT JOIN contacts ct ON d.contact_id = ct.id
  LEFT JOIN companies co ON d.company_id = co.id`

// contactCompany is the company a contact works at, which a deal takes when
// it has none of its own; nil for no contact, no such contact or no company.
func contactCompany(ctx context.Context, contactID string) (any, error) {
	if contactID == "" {
		return nil, nil
	}
	row, err := db.Get(ctx, "SELECT company_id FROM contacts WHERE id = ?", contactID)
	if err != nil || row == nil {
		return nil, err
	}
	return row["company_id"], nil
}

// wonDay is the day a won deal makes its company a customer: its close date,
// never later than today (a close date is often the planned one), or today
// when it has none. closeDate is whatever the deal holds (TS trimmed only a
// string).
func wonDay(closeDate any, now time.Time) string {
	today := isoDay(now)
	s, _ := closeDate.(string)
	if close := js.Trim(s); IsDay(close) && close < today {
		return close
	}
	return today
}

// markCustomer: a company is a customer from the day its first deal closed
// won. A date already there (set by a person, or by an earlier win) stays.
func markCustomer(ctx context.Context, companyID string, day string) error {
	_, err := db.Run(ctx, "UPDATE companies SET customer_since = ?, updated_at = datetime('now') WHERE id = ? AND (customer_since IS NULL OR TRIM(customer_since) = '')", day, companyID)
	return err
}

// withProgress is a deal row with the progress of deal id: null for a closed
// deal (`progress.get(id) ?? null`).
func withProgress(row db.Row, p *DealsProgressResult, id string) db.Row {
	out := make(db.Row, len(row)+1)
	for k, v := range row {
		out[k] = v
	}
	if dp, ok := p.Progress[id]; ok {
		out["progress"] = dp
	} else {
		out["progress"] = nil
	}
	return out
}

type dealsBody struct {
	Deals []db.Row `json:"deals"`
}

// dealsBoard is every deal for the pipeline board, oldest first, optionally
// filtered, each with its progress (null for a closed deal).
func (app *App) dealsBoard(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := optionalStrings(q, "filters", "tz"); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryList(w, func() (int, any, error) {
		if err := app.backfillDealCompanies(ctx); err != nil {
			return 0, nil, err
		}
		cols, err := TableColumns(ctx, "deals")
		if err != nil {
			return 0, nil, err
		}
		many, err := ManyLinks(ctx, Deal)
		if err != nil {
			return 0, nil, err
		}
		now, tz := app.now(), TzOffsetOf(textAt(q, "tz"))
		clauses, params, err := BuildFilters(cols, textAt(q, "filters"), "d.", tz, many, now)
		if err != nil {
			return 0, nil, err
		}
		rows, err := db.Query(ctx, dealSelect+whereOf(clauses)+" ORDER BY d.created_at ASC", params...)
		if err != nil {
			return 0, nil, err
		}
		deals, err := WithRelations(ctx, Deal, rows, 3)
		if err != nil {
			return 0, nil, err
		}
		p, err := app.progressOf(ctx, deals, now, tz)
		if err != nil {
			return 0, nil, err
		}
		out := make([]db.Row, len(deals))
		for i, d := range deals {
			out[i] = withProgress(d, p, js.String(d["id"]))
		}
		if err := withDealLinks(ctx, out); err != nil {
			return 0, nil, err
		}
		return 200, dealsBody{out}, nil
	})
}

type healthCounts struct {
	Red     float64 `json:"red"`
	Yellow  float64 `json:"yellow"`
	Unknown float64 `json:"unknown"`
	Green   float64 `json:"green"`
}

type dealsProgressBody struct {
	Deals  []db.Row     `json:"deals"`
	Total  float64      `json:"total"`
	Counts healthCounts `json:"counts"`
	Sight  SightOfSyncs `json:"sight"`
}

// dealsProgress is the open deals, worst first, then the biggest. An open
// deal is one whose stage is neither won nor lost. Its next step is the
// soonest of the next meeting booked with its company and its open tasks with
// a date (ours or theirs). Red: one of our promises is overdue, the last call
// went badly, a risk is open, or no next step and no contact in 14 days.
// Yellow: no next step, no contact in 14 days, waiting on them, the close
// date has passed, or the mood dropped. Contact is a call, an email, or a
// call, message, email or meeting logged on the deal, its company or its
// contacts; silence is judged only through what the CRM can see, and the
// reason says what it can't. Unknown: nothing against the deal and no way to
// judge contact. `tz` is the viewer's UTC offset in minutes (e.g. 120), for
// what counts as overdue today; `limit` defaults to 100, at most 500.
func (app *App) dealsProgress(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := optionalStrings(q, "tz", "limit"); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		if err := app.backfillDealCompanies(ctx); err != nil {
			return 0, nil, err
		}
		rows, err := db.Query(ctx, dealSelect+" ORDER BY d.created_at ASC")
		if err != nil {
			return 0, nil, err
		}
		p, err := app.progressOf(ctx, rows, app.now(), TzOffsetOf(textAt(q, "tz")))
		if err != nil {
			return 0, nil, err
		}
		open := []db.Row{}
		for _, d := range rows {
			if _, ok := p.Progress[js.String(d["id"])]; ok {
				open = append(open, withProgress(d, p, js.String(d["id"])))
			}
		}
		status := func(d db.Row) HealthStatus { return d["progress"].(DealProgress).Status }
		value := func(d db.Row) float64 { // Number(value) || 0
			if v := js.Number(d["value"]); !math.IsNaN(v) {
				return v
			}
			return 0
		}
		sort.SliceStable(open, func(i, j int) bool {
			a, b := open[i], open[j]
			if d := Severity[status(a)] - Severity[status(b)]; d != 0 {
				return d < 0
			}
			if d := value(b) - value(a); d != 0 && !math.IsNaN(d) {
				return d < 0
			}
			return localeCompare(js.String(a["name"]), js.String(b["name"])) < 0
		})
		var counts healthCounts
		for _, d := range open {
			switch status(d) {
			case "red":
				counts.Red++
			case "yellow":
				counts.Yellow++
			case "unknown":
				counts.Unknown++
			case "green":
				counts.Green++
			}
		}
		limit := 100.0
		if n := js.ParseInt(textAt(q, "limit")); n != 0 && !math.IsNaN(n) { // parseInt(q.limit || "100") || 100
			limit = n
		}
		limit = math.Min(500, math.Max(1, limit))
		deals, err := WithRelations(ctx, Deal, open[:min(len(open), int(limit))], 3)
		if err != nil {
			return 0, nil, err
		}
		if err := withDealLinks(ctx, deals); err != nil {
			return 0, nil, err
		}
		return 200, dealsProgressBody{deals, float64(len(open)), counts, p.Sight}, nil
	})
}

type dealsPage struct {
	Deals      []db.Row `json:"deals"`
	Total      any      `json:"total"`
	Page       float64  `json:"page"`
	Limit      float64  `json:"limit"`
	TotalValue any      `json:"totalValue"`
}

var dealSorts = []string{"id", "name", "value", "recurring_value", "stage", "close_date", "created_at"}

// listDeals lists deals with pagination, search (name and notes), stage (a
// stage key, see GET /api/stages), contact, company and partner, sorted by
// one of a fixed set of columns. totalValue sums the value of every matching
// deal.
func (app *App) listDeals(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := paginationQueryIssues(q, "stage", "contact_id", "company_id", "partner_id"); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		if err := app.backfillDealCompanies(ctx); err != nil {
			return 0, nil, err
		}
		page, limit, offset := listPage(q)
		search := js.Trim(textAt(q, "search"))
		stage := js.Trim(textAt(q, "stage"))
		contactID := textAt(q, "contact_id")
		companyID := textAt(q, "company_id")
		partnerID := textAt(q, "partner_id")

		sortCol := textAt(q, "sort")
		if indexOf(dealSorts, sortCol) < 0 {
			sortCol = "id"
		}
		order := listOrder(q)

		where := []string{}
		params := []any{}
		if search != "" {
			where = append(where, "(d.name LIKE ? OR d.notes LIKE ?)")
			params = append(params, "%"+search+"%", "%"+search+"%")
		}
		if stage != "" {
			where = append(where, "d.stage = ?")
			params = append(params, stage)
		}
		if contactID != "" {
			where = append(where, "d.contact_id = ?")
			params = append(params, contactID)
		}
		if companyID != "" {
			where = append(where, "d.company_id = ?")
			params = append(params, companyID)
		}
		if partnerID != "" {
			where = append(where, "EXISTS (SELECT 1 FROM deal_partners dp WHERE dp.deal_id = d.id AND dp.partner_id = ?)")
			params = append(params, partnerID)
		}
		whereSQL := whereOf(where)

		count, err := db.Get(ctx, "SELECT COUNT(*) as total FROM deals d"+whereSQL, params...)
		if err != nil {
			return 0, nil, err
		}
		agg, err := db.Get(ctx, "SELECT COALESCE(SUM(d.value), 0) as total_value FROM deals d"+whereSQL, params...)
		if err != nil {
			return 0, nil, err
		}
		rows, err := db.Query(ctx, fmt.Sprintf(`%s
       %s
       ORDER BY d.%s %s, d.id
       LIMIT ? OFFSET ?`, dealSelect, whereSQL, sortCol, order), append(params, limit, offset)...)
		if err != nil {
			return 0, nil, err
		}
		deals, err := WithRelations(ctx, Deal, rows, 3)
		if err != nil {
			return 0, nil, err
		}
		if err := withDealLinks(ctx, deals); err != nil {
			return 0, nil, err
		}
		return 200, dealsPage{deals, orZero(count, "total"), page, limit, orZero(agg, "total_value")}, nil
	})
}

type dealBody struct {
	Deal db.Row `json:"deal,omitempty"` // a row gone missing was undefined: no key
}

// readDeal is a deal as the write routes answer it: dealSelect's row, its
// relations, its partners and its owner.
func readDeal(ctx context.Context, id string) (db.Row, error) {
	row, err := db.Get(ctx, dealSelect+" WHERE d.id = ?", id)
	if err != nil {
		return nil, err
	}
	return dealView(ctx, row)
}

// dealView is a deal row with its relations, its partners and its owner;
// nil for nil.
func dealView(ctx context.Context, row db.Row) (db.Row, error) {
	row, err := WithRelationsOne(ctx, Deal, row)
	if err != nil || row == nil {
		return row, err
	}
	return row, withDealLinks(ctx, []db.Row{row})
}

// dealValue is `parseFloat(String(body.value)) || 0` on a validated body's
// value: a number, a string, or absent.
func dealValue(v any) float64 {
	var f float64
	switch v := v.(type) {
	case float64:
		f = v
	case string:
		f = js.ParseFloat(v)
	default:
		return 0 // String(undefined) is "undefined"
	}
	if f == 0 || math.IsNaN(f) {
		return 0 // -0 and NaN are falsy
	}
	return f
}

// textOrNull is `v ? String(v) : null` on a validated string field.
func textOrNull(v any) any {
	if s, _ := v.(string); s != "" {
		return s
	}
	return nil
}

func dealWriteSchema(create bool) zodCheck {
	name := zString(1, 0)
	if !create {
		name = zOptional(zString(0, 0))
	}
	return zObject(
		zKey("name", name),
		zKey("contact_id", zOptional(zNullable(zString(0, 0)))),
		// The deal's company. Omitted on create, it is the contact's; on update,
		// setting a contact on a deal without one also sets it, to the contact's
		// company.
		zKey("company_id", zOptional(zNullable(zString(0, 0)))),
		// What the deal is worth once.
		zKey("value", zOptional(zNumberOrString())),
		// What it is worth each month while they stay a customer.
		zKey("recurring_value", zOptional(zNumberOrString())),
		zKey("stage", zOptional(zString(0, 0))),
		zKey("close_date", zOptional(zString(0, 0))),
		zKey("notes", zOptional(zString(0, 0))),
		// The account the deal is assigned to (GET /api/team); null for nobody.
		zKey("owner_id", zOptional(zNullable(zString(0, 0)))),
		// The partners taking part in the deal: the whole list, which replaces
		// the one before ([] for none).
		zKey("partner_ids", zOptional(zArray(zString(0, 0), maxDealPartners))),
		zKey("custom", zCustomValues()),
	)
}

var (
	createDealSchema = dealWriteSchema(true)
	updateDealSchema = dealWriteSchema(false)
)

// createDeal creates a deal. Custom fields may be sent flat or in `custom`;
// an unknown field is 422, a missing required one 400. A value an enum field
// doesn't allow fails after the row is written (500), as in TS.
func (app *App) createDeal(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := zodIssues(createDealSchema, v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		custom, refused, answer, err := customWrite(ctx, Deal, body, web.RawBody(r), "create")
		if err != nil || refused != 0 {
			return refused, answer, err
		}
		name := trimmedText(body, "name")
		if name == "" {
			return fail(400, "Name is required")
		}
		partnerIDs, _, problem, err := dealPartnerIDs(ctx, body)
		if err != nil {
			return 0, nil, err
		}
		if problem != "" {
			return fail(400, problem)
		}
		owner, ownerSent, problem, err := ownerID(ctx, body)
		if err != nil {
			return 0, nil, err
		}
		if problem != "" {
			return fail(400, problem)
		}

		contactID := textOrNull(body["contact_id"])
		var companyID any
		if c, sent := body["company_id"]; sent {
			companyID = textOrNull(c)
		} else if companyID, err = contactCompany(ctx, textAt(body, "contact_id")); err != nil {
			return 0, nil, err
		}
		value := dealValue(body["value"])

		// Stage must exist; default is the first stage of the pipeline.
		stageKey := trimmedText(body, "stage")
		if stageKey != "" {
			ok, err := app.getStageRow(ctx, stageKey)
			if err != nil {
				return 0, nil, err
			}
			if ok == nil {
				answer, err := app.unknownStageError(ctx, stageKey)
				return 400, answer, err
			}
		} else {
			all, err := app.listStagesRows(ctx)
			if err != nil {
				return 0, nil, err
			}
			stageKey = "prospect"
			if len(all) > 0 && all[0]["key"] != nil {
				stageKey = js.String(all[0]["key"])
			}
		}

		id := js.UUID()
		cols := []string{"id", "name", "contact_id", "company_id", "value", "stage", "close_date", "notes"}
		params := []any{id, name, contactID, companyID, value, stageKey, trimmedText(body, "close_date"), trimmedText(body, "notes")}
		// The columns that came after the port, only when sent: a deal without
		// them is written as it always was.
		if v, sent := body["recurring_value"]; sent {
			cols, params = append(cols, "recurring_value"), append(params, dealValue(v))
		}
		if ownerSent {
			cols, params = append(cols, "owner_id"), append(params, owner)
		}
		if _, err := db.Run(ctx, fmt.Sprintf("INSERT INTO deals (%s) VALUES (%s)", strings.Join(cols, ", "), marks(len(cols))), params...); err != nil {
			return 0, nil, err
		}

		if err := custom.apply(ctx, Deal, "deals", id); err != nil {
			return 0, nil, err
		}
		if len(partnerIDs) > 0 {
			if err := setDealPartners(ctx, id, partnerIDs); err != nil {
				return 0, nil, err
			}
		}
		if js.Truthy(companyID) {
			st, err := app.getStageRow(ctx, stageKey)
			if err != nil {
				return 0, nil, err
			}
			if st != nil && js.Truthy(st["is_won"]) {
				if err := markCustomer(ctx, js.String(companyID), wonDay(body["close_date"], app.now())); err != nil {
					return 0, nil, err
				}
			}
		}

		inserted, err := readDeal(ctx, id)
		return 201, dealBody{inserted}, err
	})
}

// updateDeal updates the fields sent. Moving a deal into a won stage logs the
// win and makes its company a customer; into a lost stage, logs the loss.
func (app *App) updateDeal(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := zodIssues(updateDealSchema, v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	id := r.PathValue("id")
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		if id == "" {
			return fail(400, "Invalid ID")
		}
		custom, refused, answer, err := customWrite(ctx, Deal, body, web.RawBody(r), "update")
		if err != nil || refused != 0 {
			return refused, answer, err
		}
		stage, stageSent := body["stage"].(string)
		if stageSent {
			ok, err := app.getStageRow(ctx, js.Trim(stage))
			if err != nil {
				return 0, nil, err
			}
			if ok == nil {
				answer, err := app.unknownStageError(ctx, js.Trim(stage))
				return 400, answer, err
			}
		}
		fields := []string{}
		params := []any{}

		for _, key := range []string{"name", "stage", "close_date", "notes"} {
			if s, sent := body[key].(string); sent {
				fields = append(fields, key+" = ?")
				params = append(params, js.Trim(s))
			}
		}
		if v, sent := body["value"]; sent {
			fields = append(fields, "value = ?")
			params = append(params, dealValue(v))
		}
		if v, sent := body["recurring_value"]; sent {
			fields = append(fields, "recurring_value = ?")
			params = append(params, dealValue(v))
		}
		contact, contactSent := body["contact_id"]
		if contactSent {
			fields = append(fields, "contact_id = ?")
			params = append(params, textOrNull(contact))
		}
		company, companySent := body["company_id"]
		if companySent {
			fields = append(fields, "company_id = ?")
			params = append(params, textOrNull(company))
		}
		partnerIDs, partnersSent, problem, err := dealPartnerIDs(ctx, body)
		if err != nil {
			return 0, nil, err
		}
		if problem != "" {
			return fail(400, problem)
		}
		owner, ownerSent, problem, err := ownerID(ctx, body)
		if err != nil {
			return 0, nil, err
		}
		if problem != "" {
			return fail(400, problem)
		}
		if ownerSent {
			fields = append(fields, "owner_id = ?")
			params = append(params, owner)
		}

		if len(fields) == 0 && len(custom.values) == 0 && !partnersSent {
			return fail(400, "No fields to update")
		}

		if err := app.backfillDealCompanies(ctx); err != nil {
			return 0, nil, err
		}
		exists, err := db.Get(ctx, "SELECT id, company_id FROM deals WHERE id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if exists == nil {
			return fail(404, "Deal not found")
		}

		// A contact set on a deal with no company brings its company along.
		if textOrNull(contact) != nil && !companySent && !js.Truthy(exists["company_id"]) {
			inherited, err := contactCompany(ctx, contact.(string))
			if err != nil {
				return 0, nil, err
			}
			if js.Truthy(inherited) {
				fields = append(fields, "company_id = ?")
				params = append(params, inherited)
			}
		}

		if len(fields) > 0 || partnersSent {
			fields = append(fields, "updated_at = datetime('now')")
			params = append(params, id)
			if _, err := db.Run(ctx, "UPDATE deals SET "+strings.Join(fields, ", ")+" WHERE id = ?", params...); err != nil {
				return 0, nil, err
			}
		}
		if err := custom.apply(ctx, Deal, "deals", id); err != nil {
			return 0, nil, err
		}
		if partnersSent {
			if err := setDealPartners(ctx, id, partnerIDs); err != nil {
				return 0, nil, err
			}
		}

		updated, err := db.Get(ctx, dealSelect+" WHERE d.id = ?", id)
		if err != nil {
			return 0, nil, err
		}

		// Stage semantics fire on the stage's FLAGS, never its name — so they
		// work with any vocabulary the user edits the pipeline into. Only when
		// this request actually set the stage (best-effort, never blocks the
		// update).
		if stageSent && updated != nil {
			st, err := app.getStageRow(ctx, js.Trim(stage))
			if err != nil {
				return 0, nil, err
			}
			if st != nil && js.Truthy(st["is_won"]) {
				if err := app.celebrateWin(ctx, id, stage, st, updated); err != nil {
					return 0, nil, err
				}
			}
			if st != nil && js.Truthy(st["is_lost"]) {
				LogActivity(ctx, "deal", id, "stage_change", "Deal lost — "+js.String(st["label"]), struct {
					Stage string `json:"stage"`
				}{stage})
			}
		}
		deal, err := dealView(ctx, updated)
		return 200, dealBody{deal}, err
	})
}

// celebrateWin is what a deal just moved into a won stage (st) sets off: the
// win on its timeline, and its company a customer (the Customers page) from
// its close date. stage is the stage as the request sent it.
//
// TS also posted the win to Slack; the CRM has no integrations, so a win is
// only recorded here.
func (app *App) celebrateWin(ctx context.Context, id, stage string, st, updated db.Row) error {
	value := js.Number(updated["value"])
	if math.IsNaN(value) || value == 0 {
		value = 0 // Number(value) || 0
	}
	LogActivity(ctx, "deal", id, "stage_change", "Deal won — "+js.String(st["label"]), struct {
		Stage string `json:"stage"`
		Value any    `json:"value"`
	}{stage, jsonNumber(value)})
	// Their first won deal makes the company a customer, from its close date.
	if js.Truthy(updated["company_id"]) {
		return markCustomer(ctx, js.String(updated["company_id"]), wonDay(updated["close_date"], app.now()))
	}
	return nil
}

// deleteDeal deletes a deal; custom relations to it are cleared, and
// deal_partners drops its partners.
func (app *App) deleteDeal(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	return tryJSON(w, func() (int, any, error) {
		if id == "" {
			return fail(400, "Invalid ID")
		}
		res, err := db.Run(ctx, "DELETE FROM deals WHERE id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if res.Changes == 0 {
			return fail(404, "Deal not found")
		}
		if err := DetachRelations(ctx, Deal, []string{id}); err != nil {
			return 0, nil, err
		}
		return 200, okBody{true}, nil
	})
}
