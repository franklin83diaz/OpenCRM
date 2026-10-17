// Package crm is the OpenCRM API: one file per TS module, routes in
// routes_<area>.go.
package crm

import (
	"net/http"
	"time"

	"opencrm/internal/clawnify"
	"opencrm/internal/web"
	"opencrm/internal/web/discovery"
)

// App is what every route can reach beyond the request: the deployment's
// configuration and clients.
type App struct {
	Env  clawnify.Env
	Conn *clawnify.Connections
	// Now is the clock for what TS read with new Date() or Date.now(); nil is
	// time.Now. Tests set it to replay TS at a fixed time.
	Now func() time.Time
	// done holds index.ts's per-isolate fast paths (backfills.go): which
	// one-time checks have passed in this process.
	done doneOnce
}

// now is the time by App.Now.
func (app *App) now() time.Time {
	if app.Now != nil {
		return app.Now()
	}
	return time.Now()
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
