package crm

import (
	"net/http"

	"opencrm/internal/db"
	"opencrm/internal/web"
)

// ── Stats ──────────────────────────────────────────────────────────
//
// Route order: nothing else TS registered matches /api/stats.

func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/stats", app.stats)
	})
}

type statsBody struct {
	Contacts  any `json:"contacts"`
	Companies any `json:"companies"`
	Deals     any `json:"deals"`
	DealValue any `json:"dealValue"`
	// Customers: companies that are customers (customer_since set).
	Customers any `json:"customers"`
}

// stats is the dashboard's statistics: how many contacts, companies, deals
// and customers, and the value of the deals not lost.
func (app *App) stats(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	return tryJSON(w, func() (int, any, error) {
		var rows [4]db.Row
		for i, sql := range []string{
			"SELECT COUNT(*) as count FROM contacts",
			"SELECT COUNT(*) as count FROM companies",
			"SELECT COUNT(*) as count FROM deals",
			"SELECT COALESCE(SUM(value), 0) as total FROM deals WHERE stage NOT IN (SELECT key FROM stages WHERE is_lost = 1)",
		} {
			row, err := db.Get(ctx, sql)
			if err != nil {
				return 0, nil, err
			}
			rows[i] = row
		}
		if err := app.backfillCustomers(ctx); err != nil {
			return 0, nil, err
		}
		customers, err := db.Get(ctx, "SELECT COUNT(*) as count FROM companies WHERE customer_since IS NOT NULL AND TRIM(customer_since) != ''")
		if err != nil {
			return 0, nil, err
		}
		return 200, statsBody{
			Contacts:  orZero(rows[0], "count"),
			Companies: orZero(rows[1], "count"),
			Deals:     orZero(rows[2], "count"),
			DealValue: orZero(rows[3], "total"),
			Customers: orZero(customers, "count"),
		}, nil
	})
}
