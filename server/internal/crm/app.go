// Package crm is the OpenCRM API: one file per TS module, routes in
// routes_<area>.go.
package crm

import (
	"net/http"

	"opencrm/internal/clawnify"
	"opencrm/internal/web"
	"opencrm/internal/web/discovery"
)

// App is what every route can reach beyond the request: the deployment's
// configuration and clients.
type App struct {
	Env  clawnify.Env
	Conn *clawnify.Connections
	// done holds index.ts's per-isolate fast paths (backfills.go): which
	// one-time checks have passed in this process.
	done doneOnce
}

var routes []func(rt *web.Router, app *App)

// register adds a file's routes. Each routes_<area>.go calls it from init, so
// no file has to list the others.
func register(fn func(rt *web.Router, app *App)) {
	routes = append(routes, fn)
}

// New builds the API: discovery, the not-found answer, and every registered route.
func New(app *App) http.Handler {
	rt := web.NewRouter()
	discovery.Mount(rt)
	for _, fn := range routes {
		fn(rt, app)
	}
	return rt
}
