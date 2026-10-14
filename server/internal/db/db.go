// Package db is the Go side of @clawnify/db: package-level Query/Get/Run over
// whichever driver the binary installed with Use — the D1 bridge in the
// container, SQLite on disk for the dev server and tests. Rows are plain maps
// whose numbers are float64, so ported code sees what TS saw from D1.
package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// Row is one result row, column name → value (string, float64, []byte or nil).
type Row = map[string]any

// Result is what a write reports back, as D1's meta.changes / meta.last_row_id.
type Result struct{ Changes, LastRowID int64 }

// Driver runs one SQL statement against the database.
type Driver interface {
	Query(ctx context.Context, sql string, params []any) ([]Row, error)
	Run(ctx context.Context, sql string, params []any) (Result, error)
}

var current Driver

// Use installs the driver every Query, Get and Run goes through. Call it once
// at startup (or per test), before serving.
func Use(d Driver) { current = d }

func driver() (Driver, error) {
	if current == nil {
		return nil, errors.New("@clawnify/db: not initialized — call db.Use first")
	}
	return current, nil
}

// Query returns every row the statement produces.
func Query(ctx context.Context, sql string, params ...any) ([]Row, error) {
	if err := assertBindable(params); err != nil {
		return nil, err
	}
	d, err := driver()
	if err != nil {
		return nil, err
	}
	return d.Query(ctx, sql, nonNil(params))
}

// Get returns the first row, or nil when there is none.
func Get(ctx context.Context, sql string, params ...any) (Row, error) {
	rows, err := Query(ctx, sql, params...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// Run executes a write.
func Run(ctx context.Context, sql string, params ...any) (Result, error) {
	if err := assertBindable(params); err != nil {
		return Result{}, err
	}
	d, err := driver()
	if err != nil {
		return Result{}, err
	}
	return d.Run(ctx, sql, nonNil(params))
}

// nonNil keeps "no params" an empty list on the wire rather than null.
func nonNil(params []any) []any {
	if params == nil {
		return []any{}
	}
	return params
}

// assertBindable is @clawnify/db's guard, with its message: an object handed
// to D1 binds as "[object Object]" without complaint, so refuse it up front.
func assertBindable(params []any) error {
	for i, v := range params {
		switch v.(type) {
		case nil, string, bool, int, int64, float64, []byte:
			continue
		}
		preview := fmt.Sprint(v)
		if b, err := json.Marshal(v); err == nil {
			preview = string(b)
			if len(preview) > 80 {
				preview = preview[:77] + "..."
			}
		}
		return fmt.Errorf("@clawnify/db: param [%d] is %s — D1/SQLite only binds scalars. If this is a JSON column, wrap with json(value). Got: %s", i, jsTypeOf(v), preview)
	}
	return nil
}

// jsTypeOf names a Go value the way JS typeof would have named its JSON twin.
func jsTypeOf(v any) string {
	switch reflect.ValueOf(v).Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Func:
		return "function"
	default:
		return "object"
	}
}
