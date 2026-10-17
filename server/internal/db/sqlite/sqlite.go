// Package sqlite is the db.Driver for a SQLite file on disk — the dev server
// and tests. It behaves like D1 where ported code can tell: foreign keys are
// enforced, every integer comes back as float64, and a boolean is bound as
// D1 binds it.
package sqlite

import (
	"context"
	"database/sql"
	"net/url"

	"opencrm/internal/db"

	_ "modernc.org/sqlite"
)

// Open opens (or creates) the database at path.
func Open(path string) (db.Driver, error) {
	// Pragmas in the DSN apply to every pooled connection, not just the first.
	q := url.Values{"_pragma": {"foreign_keys(1)", "busy_timeout(5000)"}}
	conn, err := sql.Open("sqlite", path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	// D1 runs one statement at a time; so does this, which also keeps writers
	// from tripping over each other's locks.
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, err
	}
	return &driver{conn}, nil
}

type driver struct{ conn *sql.DB }

// Close releases the database file.
func (d *driver) Close() error { return d.conn.Close() }

// d1Params are params as D1 binds them: a boolean is the number 1 or 0, a
// REAL like every JS number (a TEXT column stores "1.0", not "1").
func d1Params(params []any) []any {
	var out []any // a copy, once there is a boolean to replace
	for i, p := range params {
		b, ok := p.(bool)
		if !ok {
			continue
		}
		if out == nil {
			out = append([]any(nil), params...)
		}
		out[i] = 0.0
		if b {
			out[i] = 1.0
		}
	}
	if out == nil {
		return params
	}
	return out
}

func (d *driver) Query(ctx context.Context, query string, params []any) ([]db.Row, error) {
	rows, err := d.conn.QueryContext(ctx, query, d1Params(params)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []db.Row{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(db.Row, len(cols))
		for i, c := range cols {
			if n, ok := vals[i].(int64); ok {
				row[c] = float64(n)
			} else {
				row[c] = vals[i]
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (d *driver) Run(ctx context.Context, query string, params []any) (db.Result, error) {
	res, err := d.conn.ExecContext(ctx, query, d1Params(params)...)
	if err != nil {
		return db.Result{}, err
	}
	changes, _ := res.RowsAffected()
	last, _ := res.LastInsertId()
	return db.Result{Changes: changes, LastRowID: last}, nil
}
