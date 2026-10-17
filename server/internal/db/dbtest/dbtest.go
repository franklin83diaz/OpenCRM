// Package dbtest gives a test its own SQLite database with the app's schema.
package dbtest

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"opencrm/internal/db"
	"opencrm/internal/db/sqlite"
)

// Setup creates a fresh database in a temp dir, applies server/schema.sql and
// installs it with db.Use. Tests that use it must not run in parallel: the
// installed driver is package state.
func Setup(t *testing.T) context.Context {
	t.Helper()
	schema, err := os.ReadFile(schemaPath(t))
	if err != nil {
		t.Fatal(err)
	}
	d, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Use(nil)
		if c, ok := d.(io.Closer); ok {
			c.Close()
		}
	})
	ctx := context.Background()
	// Without params SQLite runs every statement in the script; in one
	// transaction, the schema is one write to disk, not one per statement.
	if _, err := d.Run(ctx, "BEGIN;\n"+string(schema)+"\nCOMMIT;", nil); err != nil {
		t.Fatalf("applying schema.sql: %v", err)
	}
	db.Use(d)
	return ctx
}

// schemaPath finds server/schema.sql beside go.mod, from whichever package
// directory the test runs in.
func schemaPath(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "schema.sql")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("dbtest: no go.mod above the test's directory")
		}
		dir = parent
	}
}
