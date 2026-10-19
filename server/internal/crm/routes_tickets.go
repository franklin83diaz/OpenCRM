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

// ── Tickets ────────────────────────────────────────────────────────
//
// A customer's requests and problems. Each is for a customer (a company with
// customer_since), may name the customer user who reported it (one of that
// customer's), and is assigned to an account or to nobody. They came after
// the port from TS, so nothing here has a TS original to match.
//
// Route order: ServeMux prefers the literal paths (aggregates, bulk-delete)
// to {id} whatever the order.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/tickets", app.listTickets)
		rt.Handle("GET /api/tickets/aggregates", app.ticketsAggregates)
		rt.Handle("GET /api/tickets/{id}", app.getTicket)
		rt.Handle("POST /api/tickets", app.createTicket)
		rt.Handle("PUT /api/tickets/{id}", app.updateTicket)
		rt.Handle("DELETE /api/tickets/{id}", app.deleteTicket)
		rt.Handle("POST /api/tickets/bulk-delete", app.bulkDeleteTickets)
	})
}

// TicketStatuses are where a ticket stands, in order: open (new, nobody on it
// yet), in_progress (being worked), waiting (on the customer), resolved
// (fixed, the customer may still confirm) and closed. The last two are done.
var TicketStatuses = []string{"open", "in_progress", "waiting", "resolved", "closed"}

// TicketPriorities, lowest first; a new ticket is medium.
var TicketPriorities = []string{"low", "medium", "high", "urgent"}

// ticketFrom is a ticket with its company and customer user, over alias t.
const ticketFrom = ` FROM tickets t LEFT JOIN companies co ON t.company_id = co.id LEFT JOIN customer_users cu ON t.customer_user_id = cu.id`

const ticketSelect = `SELECT t.*, co.name as company_name, co.domain as company_domain, cu.name as customer_user_name, cu.email as customer_user_email` + ticketFrom

// ticketRankSQL orders a ticket by its status or priority's place in values
// (the workflow, the urgency), not alphabetically.
func ticketRankSQL(column string, values []string) string {
	var b strings.Builder
	b.WriteString("CASE t." + column)
	for i, v := range values {
		fmt.Fprintf(&b, " WHEN '%s' THEN %d", v, i)
	}
	fmt.Fprintf(&b, " ELSE %d END", len(values))
	return b.String()
}

// ticketSortSQL is the list's ORDER BY expression: status and priority in
// their order, the owner by name, a ticket with no due date after every
// dated one (ascending), any other column as listSortSQL has it.
func ticketSortSQL(ctx context.Context, sort string, cols map[string]bool, many map[string]ManyLink) (string, error) {
	switch sort {
	case "status":
		return ticketRankSQL("status", TicketStatuses), nil
	case "priority":
		return ticketRankSQL("priority", TicketPriorities), nil
	case "owner_id":
		return "(SELECT COALESCE(NULLIF(u.name, ''), u.email) FROM users u WHERE u.id = t.owner_id) COLLATE NOCASE", nil
	case "due_date":
		return "COALESCE(NULLIF(t.due_date, ''), '9999-12-31')", nil
	}
	return listSortSQL(ctx, Ticket, "t", sort, cols, many)
}

// ticketsWhere is the list's WHERE: search (subject, description, and the
// reporting customer user's name, email and phone),
// company_id, customer_user_id, owner_id, status and filters. Shared by the
// list and its footer aggregates.
func ticketsWhere(q map[string]any, cols map[string]bool, many map[string]ManyLink) (string, []any, error) {
	where := []string{}
	params := []any{}
	if search := js.Trim(textAt(q, "search")); search != "" {
		like := "%" + search + "%"
		cond := "t.subject LIKE ? OR t.description LIKE ? OR cu.name LIKE ? OR cu.email LIKE ?"
		params = append(params, like, like, like, like)
		// A phone number finds the reporter's however either was written.
		if digits := phoneDigits(search); digits != "" {
			cond += " OR " + bareDigitsSQL("cu.phone") + " LIKE ?"
			params = append(params, "%"+digits+"%")
		}
		where = append(where, "("+cond+")")
	}
	for _, key := range []string{"company_id", "customer_user_id", "owner_id", "status"} {
		if v := textAt(q, key); v != "" {
			where = append(where, "t."+key+" = ?")
			params = append(params, v)
		}
	}
	clauses, flt, err := BuildFilters(cols, textAt(q, "filters"), "t.", TzOffsetOf(textAt(q, "tz")), many, time.Now())
	if err != nil {
		return "", nil, err
	}
	return whereOf(append(where, clauses...)), append(params, flt...), nil
}

// ticketsAggregates is the list footer's aggregates over the same rows.
func (app *App) ticketsAggregates(w http.ResponseWriter, r *http.Request) error {
	q := firstQueryValues(r)
	ctx := r.Context()
	return tryList(w, func() (int, any, error) {
		cols, err := TableColumns(ctx, "tickets")
		if err != nil {
			return 0, nil, err
		}
		many, err := ManyLinks(ctx, Ticket)
		if err != nil {
			return 0, nil, err
		}
		whereSQL, params, err := ticketsWhere(q, cols, many)
		if err != nil {
			return 0, nil, err
		}
		columns := map[string]AggregateColumn{
			"subject":       {SQL: "t.subject", Kind: KindText},
			"status":        {SQL: "t.status", Kind: KindText},
			"priority":      {SQL: "t.priority", Kind: KindText},
			"company":       {SQL: "t.company_id", Kind: KindText},
			"customer_user": {SQL: "t.customer_user_id", Kind: KindText},
			"owner":         {SQL: "t.owner_id", Kind: KindText},
			"due_date":      {SQL: "t.due_date", Kind: KindDate},
			"created_at":    {SQL: "t.created_at", Kind: KindDate},
		}
		custom, err := CustomAggregateColumns(ctx, Ticket, "t", cols)
		if err != nil {
			return 0, nil, err
		}
		for k, v := range custom {
			columns[k] = v
		}
		values, err := AggregateColumns(ctx, "tickets t LEFT JOIN companies co ON t.company_id = co.id LEFT JOIN customer_users cu ON t.customer_user_id = cu.id", whereSQL, params, columns, textAt(q, "ops"))
		return 200, valuesBody{values}, err
	})
}

type ticketsPage struct {
	Tickets []db.Row `json:"tickets"`
	Total   any      `json:"total"`
	Page    float64  `json:"page"`
	Limit   float64  `json:"limit"`
}

// listTickets lists tickets with pagination, search (subject, description,
// the reporter's name, email and phone),
// company_id, customer_user_id, owner_id, status and filters, sorted by any
// real column (see ticketSortSQL). Each has its company's and customer user's
// names, its owner, and its relations.
func (app *App) listTickets(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := paginationQueryIssues(q, "company_id", "customer_user_id", "owner_id", "status"); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryList(w, func() (int, any, error) {
		page, limit, offset := listPage(q)
		cols, err := TableColumns(ctx, "tickets")
		if err != nil {
			return 0, nil, err
		}
		many, err := ManyLinks(ctx, Ticket)
		if err != nil {
			return 0, nil, err
		}
		sortSQL, err := ticketSortSQL(ctx, textAt(q, "sort"), cols, many)
		if err != nil {
			return 0, nil, err
		}
		whereSQL, params, err := ticketsWhere(q, cols, many)
		if err != nil {
			return 0, nil, err
		}
		count, err := db.Get(ctx, "SELECT COUNT(*) as total"+ticketFrom+whereSQL, params...)
		if err != nil {
			return 0, nil, err
		}
		rows, err := db.Query(ctx, fmt.Sprintf("%s%s ORDER BY %s %s, t.id LIMIT ? OFFSET ?", ticketSelect, whereSQL, sortSQL, listOrder(q)), append(params, limit, offset)...)
		if err != nil {
			return 0, nil, err
		}
		tickets, err := WithRelations(ctx, Ticket, rows, 3)
		if err != nil {
			return 0, nil, err
		}
		if err := withOwners(ctx, tickets); err != nil {
			return 0, nil, err
		}
		return 200, ticketsPage{tickets, orZero(count, "total"), page, limit}, nil
	})
}

type ticketBody struct {
	Ticket db.Row `json:"ticket,omitempty"`
}

// readTicket is a ticket as the routes answer it; nil when there is none.
func readTicket(ctx context.Context, id string) (db.Row, error) {
	row, err := db.Get(ctx, ticketSelect+" WHERE t.id = ?", id)
	if err != nil || row == nil {
		return nil, err
	}
	row, err = WithRelationsOne(ctx, Ticket, row)
	if err != nil {
		return nil, err
	}
	return row, withOwners(ctx, []db.Row{row})
}

// getTicket is one ticket.
func (app *App) getTicket(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	return tryJSON(w, func() (int, any, error) {
		ticket, err := readTicket(ctx, id)
		if err != nil {
			return 0, nil, err
		}
		if ticket == nil {
			return fail(404, "Ticket not found")
		}
		return 200, ticketBody{ticket}, nil
	})
}

var createTicketFields = []stringField{
	{key: "subject", min: 1},
	{key: "description", optional: true},
	{key: "status", optional: true},
	{key: "priority", optional: true},
	{key: "company_id", optional: true, nullable: true},
	{key: "customer_user_id", optional: true, nullable: true},
	{key: "owner_id", optional: true, nullable: true},
	{key: "due_date", optional: true, nullable: true},
}

var updateTicketFields = []stringField{
	{key: "subject", optional: true},
	{key: "description", optional: true},
	{key: "status", optional: true},
	{key: "priority", optional: true},
	{key: "company_id", optional: true, nullable: true},
	{key: "customer_user_id", optional: true, nullable: true},
	{key: "owner_id", optional: true, nullable: true},
	{key: "due_date", optional: true, nullable: true},
}

// ticketChoice reads a write's status or priority, in any case and with
// spaces or hyphens for underscores ("In progress"): the value, whether it
// was sent, and the 400 answer when it isn't one of values. On a create
// (fallback set) one not sent, or empty, is the fallback.
func ticketChoice(body map[string]any, key string, values []string, fallback string) (value string, sent bool, problem string) {
	s, sent := body[key].(string)
	value = strings.NewReplacer(" ", "_", "-", "_").Replace(strings.ToLower(js.Trim(s)))
	if fallback != "" && value == "" {
		return fallback, sent, ""
	}
	if sent && indexOf(values, value) < 0 {
		return "", sent, fmt.Sprintf(`%s must be one of %s, not "%s"`, key, strings.Join(values, ", "), s)
	}
	return value, sent, ""
}

// ticketDueDate reads a write's due_date: YYYY-MM-DD, or nil for none (null
// or ""); sent is false when the write leaves it alone.
func ticketDueDate(body map[string]any) (day any, sent bool, problem string) {
	v, sent := body["due_date"]
	if !sent {
		return nil, false, ""
	}
	s, _ := v.(string)
	if s = js.Trim(s); s != "" && !IsDay(s) {
		return nil, true, "due_date must be a date as YYYY-MM-DD"
	}
	return orNull(s), true, ""
}

// ticketParties are the customer and customer user a write gives a ticket:
// the ids to write, and whether to write each.
type ticketParties struct {
	company, user       any
	setCompany, setUser bool
}

// ticketCustomer reads a write's company_id and customer_user_id against the
// ticket as it is (nil on a create). The company must be a customer, though
// one the ticket already has stays its own if it stopped being one; the
// customer user must be one of the company's. A create with a customer user
// and no company is for the user's company. A ticket always keeps a company:
// null is refused. Moving it to another customer drops a customer user who
// isn't one of the new customer's, unless the write names another.
func ticketCustomer(ctx context.Context, body map[string]any, current db.Row) (out ticketParties, problem string, err error) {
	curCompany, curUser := colText(current["company_id"]), colText(current["customer_user_id"])
	companyID, userID := curCompany, curUser
	if v, sent := body["company_id"]; sent {
		s, _ := v.(string)
		if companyID = js.Trim(s); companyID == "" {
			return out, "company_id: a ticket is always for a customer", nil
		}
		out.setCompany = true
	}
	if v, sent := body["customer_user_id"]; sent {
		s, _ := v.(string)
		userID = js.Trim(s)
		out.setUser = true
	}
	var user db.Row
	if userID != "" && (out.setUser || out.setCompany) {
		user, err = db.Get(ctx, "SELECT id, name, company_id FROM customer_users WHERE id = ?", userID)
		if err != nil {
			return out, "", err
		}
		if user == nil {
			if out.setUser {
				return out, fmt.Sprintf(`customer_user_id: no customer user with id "%s".`, userID), nil
			}
			userID = "" // gone since: nothing to keep
		}
	}
	if current == nil && companyID == "" && user != nil {
		companyID, out.setCompany = colText(user["company_id"]), true
	}
	if current == nil && companyID == "" {
		return out, "company_id is required: the customer the ticket is for", nil
	}
	var company db.Row
	if out.setCompany {
		company, problem, err = customerCompany(ctx, "company_id", companyID)
		if err != nil {
			return out, "", err
		}
		if problem != "" && !(company != nil && companyID == curCompany) {
			return out, problem, nil
		}
	}
	if user != nil && colText(user["company_id"]) != companyID {
		if out.setUser {
			if company == nil {
				if company, err = db.Get(ctx, "SELECT name FROM companies WHERE id = ?", companyID); err != nil {
					return out, "", err
				}
			}
			name := colText(company["name"])
			return out, fmt.Sprintf(`customer_user_id: %s is not one of %s's users.`, colText(user["name"]), name), nil
		}
		userID, out.setUser = "", true // moved to another customer
	}
	out.company, out.user = orNull(companyID), orNull(userID)
	return out, "", nil
}

// createTicket creates a ticket, open and medium unless sent otherwise.
// Custom fields may be sent flat or in `custom`; an unknown field is 422, a
// missing required one 400.
func (app *App) createTicket(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := writeBodyIssues(v, createTicketFields); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		custom, refused, answer, err := customWrite(ctx, Ticket, body, web.RawBody(r), "create")
		if err != nil || refused != 0 {
			return refused, answer, err
		}
		subject := trimmedText(body, "subject")
		if subject == "" {
			return fail(400, "Subject is required")
		}
		status, _, problem := ticketChoice(body, "status", TicketStatuses, TicketStatuses[0])
		if problem != "" {
			return fail(400, problem)
		}
		priority, _, problem := ticketChoice(body, "priority", TicketPriorities, "medium")
		if problem != "" {
			return fail(400, problem)
		}
		due, _, problem := ticketDueDate(body)
		if problem != "" {
			return fail(400, problem)
		}
		parties, problem, err := ticketCustomer(ctx, body, nil)
		if err != nil {
			return 0, nil, err
		}
		if problem != "" {
			return fail(400, problem)
		}
		owner, _, problem, err := ownerID(ctx, body)
		if err != nil {
			return 0, nil, err
		}
		if problem != "" {
			return fail(400, problem)
		}
		id := js.UUID()
		if _, err := db.Run(ctx, "INSERT INTO tickets (id, subject, description, status, priority, company_id, customer_user_id, owner_id, due_date) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			id, subject, trimmedText(body, "description"), status, priority, parties.company, parties.user, owner, due); err != nil {
			return 0, nil, err
		}
		if err := custom.apply(ctx, Ticket, "tickets", id); err != nil {
			return 0, nil, err
		}
		ticket, err := readTicket(ctx, id)
		return 201, ticketBody{ticket}, err
	})
}

// updateTicket updates the fields sent. A subject sent empty is refused, and
// so is a company_id of null (see ticketCustomer); null clears the customer
// user, the owner and the due date.
func (app *App) updateTicket(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := writeBodyIssues(v, updateTicketFields); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	id := r.PathValue("id")
	body := v.(map[string]any)
	return tryJSON(w, func() (int, any, error) {
		custom, refused, answer, err := customWrite(ctx, Ticket, body, web.RawBody(r), "update")
		if err != nil || refused != 0 {
			return refused, answer, err
		}
		fields := []string{}
		params := []any{}
		set := func(column string, value any) {
			fields = append(fields, column+" = ?")
			params = append(params, value)
		}
		for _, key := range []string{"subject", "description"} {
			if s, sent := body[key].(string); sent {
				if key == "subject" && js.Trim(s) == "" {
					return fail(400, "Subject is required")
				}
				set(key, js.Trim(s))
			}
		}
		if status, sent, problem := ticketChoice(body, "status", TicketStatuses, ""); problem != "" {
			return fail(400, problem)
		} else if sent {
			set("status", status)
		}
		if priority, sent, problem := ticketChoice(body, "priority", TicketPriorities, ""); problem != "" {
			return fail(400, problem)
		} else if sent {
			set("priority", priority)
		}
		if due, sent, problem := ticketDueDate(body); problem != "" {
			return fail(400, problem)
		} else if sent {
			set("due_date", due)
		}
		owner, ownerSent, problem, err := ownerID(ctx, body)
		if err != nil {
			return 0, nil, err
		}
		if problem != "" {
			return fail(400, problem)
		}
		if ownerSent {
			set("owner_id", owner)
		}
		_, companySent := body["company_id"]
		_, userSent := body["customer_user_id"]
		if len(fields) == 0 && len(custom.values) == 0 && !companySent && !userSent {
			return fail(400, "No fields to update")
		}
		current, err := db.Get(ctx, "SELECT id, company_id, customer_user_id FROM tickets WHERE id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if current == nil {
			return fail(404, "Ticket not found")
		}
		parties, problem, err := ticketCustomer(ctx, body, current)
		if err != nil {
			return 0, nil, err
		}
		if problem != "" {
			return fail(400, problem)
		}
		if parties.setCompany {
			set("company_id", parties.company)
		}
		if parties.setUser {
			set("customer_user_id", parties.user)
		}
		if len(fields) > 0 {
			fields = append(fields, "updated_at = datetime('now')")
			params = append(params, id)
			if _, err := db.Run(ctx, "UPDATE tickets SET "+strings.Join(fields, ", ")+" WHERE id = ?", params...); err != nil {
				return 0, nil, err
			}
		}
		if err := custom.apply(ctx, Ticket, "tickets", id); err != nil {
			return 0, nil, err
		}
		ticket, err := readTicket(ctx, id)
		return 200, ticketBody{ticket}, err
	})
}

// deleteTicket deletes a ticket; custom relations to it are cleared.
func (app *App) deleteTicket(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	return tryJSON(w, func() (int, any, error) {
		res, err := db.Run(ctx, "DELETE FROM tickets WHERE id = ?", id)
		if err != nil {
			return 0, nil, err
		}
		if res.Changes == 0 {
			return fail(404, "Ticket not found")
		}
		if err := DetachRelations(ctx, Ticket, []string{id}); err != nil {
			return 0, nil, err
		}
		return 200, okBody{true}, nil
	})
}

// bulkDeleteTickets deletes several tickets (see bulkDelete).
func (app *App) bulkDeleteTickets(w http.ResponseWriter, r *http.Request) error {
	v, ok := openapiJSON(r)
	if !ok {
		return malformedJSON(w)
	}
	if issues := bulkIDsIssues(v); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		out, err := bulkDelete(ctx, Ticket, v.(map[string]any)["ids"].([]any))
		return 200, out, err
	})
}
