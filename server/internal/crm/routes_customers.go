package crm

import (
	"errors"
	"math"
	"net/http"
	"time"

	"opencrm/internal/js"
	"opencrm/internal/web"
)

// ── Customers ──
//
// How each customer is doing (customers.go), and what the CRM knows about an
// address before someone writes to it (lookup.go). Read only; anyone may read.
//
// Route order: nothing else TS registered matches /api/customers or
// /api/lookup, so ServeMux's choice is Hono's.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/customers", app.customers)
		rt.Handle("GET /api/lookup", app.lookupOne)
		rt.Handle("POST /api/lookup", app.lookupMany)
	})
}

// customers is how each customer is doing (worst first), what to do first,
// and calls with customers in the next 7 days. A customer is a company with
// customer_since set; `tz` is the viewer's UTC offset in minutes (e.g. 120),
// for what counts as overdue today.
func (app *App) customers(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := optionalStrings(q, "tz"); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		if err := app.backfillCustomers(ctx); err != nil {
			return 0, nil, err
		}
		tz := 0.0 // Number(tz ?? 0)
		if s, ok := q["tz"].(string); ok {
			tz = js.Number(s)
		}
		if math.IsNaN(tz) || math.IsInf(tz, 0) {
			tz = 0
		} else {
			tz = math.Max(-840, math.Min(840, tz))
		}
		out, err := CustomersOverview(ctx, time.Now(), tz)
		return 200, out, err
	})
}

// prepareLookup is what both lookups run first: the default stages (they say
// which deals are closed) and the backfills that give deals their company and
// companies their customer_since.
func (app *App) prepareLookup(r *http.Request) error {
	ctx := r.Context()
	if err := app.ensureStagesSeeded(ctx); err != nil {
		return err
	}
	if err := app.backfillDealCompanies(ctx); err != nil {
		return err
	}
	return app.backfillCustomers(ctx)
}

// lookupOne is what the CRM knows about an email address before someone
// writes to it: the contact with exactly this address; their company (else
// the company on `domain`, else on the address's own domain when it is a work
// address); its and the contact's deals (open first, at most 10); the latest
// call with the company; and the next meeting booked. Nulls and an empty list
// when the CRM has never heard of them.
func (app *App) lookupOne(w http.ResponseWriter, r *http.Request) error {
	q := queryInput(r)
	if issues := optionalStrings(q, "email", "domain"); len(issues) > 0 {
		return web.ZodError(w, issues)
	}
	return tryJSON(w, func() (int, any, error) {
		email, _ := q["email"].(string)
		domain, _ := q["domain"].(string)
		email, domain = js.Trim(email), js.Trim(domain)
		if email == "" && domain == "" {
			return fail(400, "Give an email, a domain, or both")
		}
		if err := app.prepareLookup(r); err != nil {
			return 0, nil, err
		}
		out, err := Lookup(r.Context(), email, domain, time.Now())
		return 200, out, err
	})
}

// lookupBodyIssues is the batch route's zod body schema: an object whose
// addresses is an array of 1 to 100 objects, each with an optional string
// email and domain.
func lookupBodyIssues(v any) []web.Issue {
	body, isObject := v.(map[string]any)
	if !isObject {
		return []web.Issue{invalidType(zodPath(), "object", v)}
	}
	raw, present := body["addresses"]
	if !present {
		return []web.Issue{requiredIssue(zodPath("addresses"), "array")}
	}
	list, isArray := raw.([]any)
	if !isArray {
		return []web.Issue{invalidType(zodPath("addresses"), "array", raw)}
	}
	var issues []web.Issue
	if len(list) < 1 {
		issues = append(issues, arrayTooSmall(zodPath("addresses"), 1))
	}
	if len(list) > 100 {
		issues = append(issues, arrayTooBig(zodPath("addresses"), 100))
	}
	for i, item := range list {
		address, isObject := item.(map[string]any)
		if !isObject {
			issues = append(issues, invalidType(zodPath("addresses", i), "object", item))
			continue
		}
		for _, key := range []string{"email", "domain"} {
			if v, present := address[key]; present {
				if issue := checkType(zodPath("addresses", i, key), v, "string"); issue != nil {
					issues = append(issues, *issue)
				}
			}
		}
	}
	return issues
}

// errNoAddresses is the TypeError TS threw reading addresses.map when the body
// was never validated (a request that didn't say it was JSON).
var errNoAddresses = errors.New("Cannot read properties of undefined (reading 'map')")

// lookupMany is what GET /api/lookup answers, for up to 100 addresses at once,
// in the order asked. An address with neither email nor domain gets an empty
// answer.
func (app *App) lookupMany(w http.ResponseWriter, r *http.Request) error {
	v, checked, ok := optionalJSONBody(r)
	if !ok {
		return malformedJSON(w)
	}
	if checked {
		if issues := lookupBodyIssues(v); len(issues) > 0 {
			return web.ZodError(w, issues)
		}
	}
	return tryJSON(w, func() (int, any, error) {
		list, _ := v.(map[string]any)["addresses"].([]any)
		if err := app.prepareLookup(r); err != nil {
			return 0, nil, err
		}
		if list == nil {
			return 0, nil, errNoAddresses
		}
		addresses := make([]LookupAddress, len(list))
		for i, item := range list {
			a := item.(map[string]any)
			addresses[i].Email, _ = a["email"].(string)
			addresses[i].Domain, _ = a["domain"].(string)
		}
		out, err := LookupMany(r.Context(), addresses, time.Now())
		return 200, struct {
			Results []LookupResult `json:"results"`
		}{out}, err
	})
}
