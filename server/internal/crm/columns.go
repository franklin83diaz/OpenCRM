package crm

// Columns a version added to tables that already existed. schema.sql creates
// them on a new database, but it can't add a column to an older one: SQLite
// has no ADD COLUMN IF NOT EXISTS, and schema.sql runs on every upgrade. So
// the server adds what is missing before it serves its first request, once
// per process.

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"opencrm/internal/db"
)

// addedColumns are the columns added after their table, in the order they came.
var addedColumns = []struct{ table, column, decl string }{
	{"deals", "recurring_value", "REAL DEFAULT 0"},
	{"deals", "owner_id", "TEXT REFERENCES users(id) ON DELETE SET NULL"},
	{"deal_partners", "one_time_value", "REAL DEFAULT 0"},
	{"deal_partners", "recurring_value", "REAL DEFAULT 0"},
	{"partners", "industry", "TEXT DEFAULT ''"},
	{"partners", "status", "TEXT NOT NULL DEFAULT 'prospect'"},
	{"companies", "main_contact_id", "TEXT REFERENCES contacts(id) ON DELETE SET NULL"},
}

// addedIndexes go with them: made once their column is there.
var addedIndexes = []string{
	"CREATE INDEX IF NOT EXISTS idx_deals_owner ON deals(owner_id)",
}

// addColumns adds the columns of addedColumns a table lacks, then their indexes.
func addColumns(ctx context.Context) error {
	have := map[string]map[string]bool{}
	for _, c := range addedColumns {
		if have[c.table] == nil {
			cols, err := TableColumns(ctx, c.table)
			if err != nil {
				return err
			}
			have[c.table] = cols
		}
		if have[c.table][c.column] {
			continue
		}
		if _, err := db.Run(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.column, c.decl)); err != nil {
			return fmt.Errorf("adding %s.%s: %w", c.table, c.column, err)
		}
		have[c.table][c.column] = true
	}
	for _, sql := range addedIndexes {
		if _, err := db.Run(ctx, sql); err != nil {
			return err
		}
	}
	return nil
}

// withColumns runs addColumns before the first request it lets through. A
// failure is logged and tried again on the next request; the request goes on
// either way, and fails only if it needs a missing column.
func (app *App) withColumns(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !app.done.columnsAdded.Load() {
			app.columnsMu.Lock()
			if !app.done.columnsAdded.Load() {
				if err := addColumns(r.Context()); err != nil {
					log.Printf("columns: %v", err)
				} else {
					app.done.columnsAdded.Store(true)
				}
			}
			app.columnsMu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}
