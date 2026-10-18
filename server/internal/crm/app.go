// Package crm is the OpenCRM API: one file per TS module, routes in
// routes_<area>.go.
package crm

import (
	"net/http"
	"sync"
	"time"

	"opencrm/internal/auth"
	"opencrm/internal/web"
	"opencrm/internal/web/discovery"
)

// App is what every route can reach beyond the request.
type App struct {
	// Now is the clock for what TS read with new Date() or Date.now(); nil is
	// time.Now. Tests set it to replay TS at a fixed time.
	Now func() time.Time
	// AdminEmail and AdminPassword (the ADMIN_EMAIL and ADMIN_PASSWORD
	// secrets) are the first admin's: created while the CRM has no accounts
	// (routes_auth.go). Empty: no admin is created.
	AdminEmail, AdminPassword string
	// done holds index.ts's per-isolate fast paths (backfills.go): which
	// one-time checks have passed in this process.
	done doneOnce
	// adminMu keeps two first logins from both creating the first admin.
	adminMu sync.Mutex
	// columnsMu keeps two first requests from both adding columns (columns.go).
	columnsMu sync.Mutex
	// logins is the login limiter, made on first use.
	loginsOnce sync.Once
	logins     *auth.Limiter
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

// New builds the API: every registered route, discovery and the not-found
// answer, behind the auth middleware that signs each /api request in, on a
// database given the columns added since its tables were made.
func New(app *App) http.Handler {
	return auth.Middleware(app.withColumns(app.router()))
}

// router is the routes alone, with no one signed in.
func (app *App) router() *web.Router {
	rt := web.NewRouter()
	discovery.Mount(rt)
	for _, fn := range routes {
		fn(rt, app)
	}
	return rt
}

// limiter is the login limiter, on the App's clock.
func (app *App) limiter() *auth.Limiter {
	app.loginsOnce.Do(func() { app.logins = auth.NewLimiter(app.now) })
	return app.logins
}
