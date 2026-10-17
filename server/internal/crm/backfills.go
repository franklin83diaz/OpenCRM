package crm

// What index.ts ran before some routes: the default pipeline, seeded into an
// empty stages table, and one-time data fixes, each recorded in
// data_backfills so it never runs again. TS kept a per-isolate flag for each
// so a warm isolate skipped even the check; the App keeps them here (one App
// serves the process, as one isolate served TS).

import (
	"context"
	"sync/atomic"

	"opencrm/internal/db"
	"opencrm/internal/js"
)

// doneOnce are the fast paths: set once a check has passed in this process.
type doneOnce struct {
	stagesSeeded            atomic.Bool
	dealCompaniesBackfilled atomic.Bool
	customersBackfilled     atomic.Bool
	adminChecked            atomic.Bool // the first admin exists, or can't be made from the secrets
}

// defaultStages is the default sales pipeline: key, label, color, position, is_won, is_lost.
var defaultStages = [][]any{
	{"prospect", "Prospect", "slate", 0, 0, 0},
	{"qualified", "Qualified", "sky", 1, 0, 0},
	{"proposal", "Proposal", "violet", 2, 0, 0},
	{"negotiation", "Negotiation", "amber", 3, 0, 0},
	{"won", "Won", "emerald", 4, 1, 0},
	{"lost", "Lost", "rose", 5, 0, 1},
}

// ensureStagesSeeded seeds the default pipeline when the stages table is
// empty (so a stage someone renamed or deleted never comes back). The flag is
// a per-process fast path; the COUNT re-check is cheap.
func (app *App) ensureStagesSeeded(ctx context.Context) error {
	if app.done.stagesSeeded.Load() {
		return nil
	}
	row, err := db.Get(ctx, "SELECT COUNT(*) as count FROM stages")
	if err != nil {
		return err
	}
	if row == nil || js.Number(row["count"]) == 0 {
		for _, s := range defaultStages {
			if _, err := db.Run(ctx, "INSERT OR IGNORE INTO stages (key, label, color, position, is_won, is_lost) VALUES (?, ?, ?, ?, ?, ?)", s...); err != nil {
				return err
			}
		}
	}
	app.done.stagesSeeded.Store(true)
	return nil
}

// backfillDealCompanies: deals predate deals.company_id; until it existed, a
// deal showed its contact's company. Gives those deals that company, once per
// database, so they read the same after the column arrives. The marker is
// written after the update, so a failed run is retried; it is what stops a
// later run from refilling a company someone cleared on purpose.
func (app *App) backfillDealCompanies(ctx context.Context) error {
	if app.done.dealCompaniesBackfilled.Load() {
		return nil
	}
	done, err := db.Get(ctx, "SELECT key FROM data_backfills WHERE key = 'deals.company_id'")
	if err != nil {
		return err
	}
	if done == nil {
		if _, err := db.Run(ctx, `UPDATE deals SET company_id = (SELECT company_id FROM contacts WHERE contacts.id = deals.contact_id)
       WHERE company_id IS NULL AND contact_id IS NOT NULL`); err != nil {
			return err
		}
		if _, err := db.Run(ctx, "INSERT OR IGNORE INTO data_backfills (key) VALUES ('deals.company_id')"); err != nil {
			return err
		}
	}
	app.done.dealCompaniesBackfilled.Store(true)
	return nil
}

// backfillCustomers: deals won before customer_since existed make their
// companies customers from the first one's close date (or the day it was last
// changed, when it has none), never later than today, once per database. Like
// backfillDealCompanies, the marker is what stops a later run from refilling
// a date someone cleared on purpose.
func (app *App) backfillCustomers(ctx context.Context) error {
	if app.done.customersBackfilled.Load() {
		return nil
	}
	done, err := db.Get(ctx, "SELECT key FROM data_backfills WHERE key = 'companies.customer_since'")
	if err != nil {
		return err
	}
	if done == nil {
		if err := app.backfillDealCompanies(ctx); err != nil {
			return err
		}
		if _, err := db.Run(ctx, `UPDATE companies SET customer_since = min((
          SELECT MIN(CASE WHEN d.close_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]' THEN d.close_date ELSE date(d.updated_at) END)
            FROM deals d JOIN stages s ON s.key = d.stage AND s.is_won = 1
           WHERE d.company_id = companies.id), date('now'))
        WHERE (customer_since IS NULL OR TRIM(customer_since) = '')
          AND EXISTS (SELECT 1 FROM deals d JOIN stages s ON s.key = d.stage AND s.is_won = 1 WHERE d.company_id = companies.id)`); err != nil {
			return err
		}
		if _, err := db.Run(ctx, "INSERT OR IGNORE INTO data_backfills (key) VALUES ('companies.customer_since')"); err != nil {
			return err
		}
	}
	app.done.customersBackfilled.Store(true)
	return nil
}
