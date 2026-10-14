// devserver runs the API natively against a SQLite file — usually the one
// `wrangler dev` keeps for the local D1 — with no container or Worker in
// between. The parity harness runs it beside the TS reference.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"opencrm/internal/clawnify"
	"opencrm/internal/crm"
	"opencrm/internal/db"
	"opencrm/internal/db/sqlite"
)

func main() {
	path := flag.String("sqlite", "", "SQLite database file with the schema applied (e.g. .wrangler/state/v3/d1/miniflare-D1DatabaseObject/<id>.sqlite)")
	addr := flag.String("addr", ":8790", "address to listen on")
	flag.Parse()
	if *path == "" {
		log.Fatal("-sqlite is required")
	}
	// Opening a missing file would create an empty database and fail later
	// with "no such table"; say what is wrong instead.
	if _, err := os.Stat(*path); err != nil {
		log.Fatal(err)
	}
	d, err := sqlite.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	db.Use(d)
	env := clawnify.EnvFromOS()
	log.Printf("listening on %s, database %s", *addr, *path)
	log.Fatal(http.ListenAndServe(*addr, crm.New(&crm.App{Env: env, Conn: clawnify.NewConnections(env, nil)})))
}
