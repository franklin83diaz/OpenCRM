# The OpenCRM API (Go)

The CRM's API: a Go module (`opencrm`) that runs in a Cloudflare Container behind the Worker in `src/worker`, and reaches D1 through it. It was ported from a TypeScript (Hono + zod) backend, and still answers as that backend did: the parity harness compares the two. The repository's [README](../README.md) covers running and deploying the whole CRM.

Go 1.22 (`go.mod`); it builds with a local go1.22 (`GOTOOLCHAIN=local`) and in the image's `golang:1.24-alpine`. The production binary uses the standard library and `golang.org/x/crypto/bcrypt` only. SQLite (`modernc.org/sqlite`) is for the dev server and the tests.

## Packages

```
main.go               — the container's server: D1 through the Worker (D1_URL), the first admin from ADMIN_EMAIL / ADMIN_PASSWORD
cmd/devserver/        — the same API on a local SQLite file (-sqlite), on :8790: the fast loop, and the parity harness
schema.sql            — the D1 schema, applied on every migration (CREATE … IF NOT EXISTS, then idempotent upgrades)
internal/
  crm/                — the API: one file of routes per area (routes_<area>.go), the rules beside them
                        (customers.go, deal_progress.go, meetings_rules.go, lookup.go, custom_fields.go, lists.go, …)
  auth/               — the middleware that signs every /api request in, sessions, API tokens, passwords, the sign-in limit
  db/                 — Query / Get / Run over the installed driver: the D1 bridge (d1.go) in the container,
                        SQLite (db/sqlite) for the dev server, db/dbtest for a test's own database
  web/                — the router, JSON in and out, who is calling; web/discovery serves /llms.txt and /api/openapi.json
  js/                 — the JavaScript rules the TS backend relied on: Number(), parseFloat, truthiness, Date parsing, toISOString
```

Rows come back as `map[string]any` with every number a `float64`, as JS saw them. A function that touches the database takes `ctx` first.

## Adding a route

Each `routes_<area>.go` registers its own routes from `init`, so no file lists the others:

```go
func init() {
	register(func(rt *web.Router, app *App) {
		rt.Handle("GET /api/things", app.things)
	})
}

func (app *App) things(w http.ResponseWriter, r *http.Request) error {
	rows, err := db.Query(r.Context(), "SELECT id, name FROM things ORDER BY name")
	if err != nil {
		return err // a plain-text 500, logged
	}
	return web.JSON(w, http.StatusOK, struct {
		Things []db.Row `json:"things"`
	}{rows})
}
```

- Patterns are Go 1.22's (`"PUT /api/things/{id}"`, read with `r.PathValue("id")`). Anything that isn't a route gets the not-found answer, never a 405 or a redirect.
- Every `/api` request is signed in before it gets here (`web.CurrentUser(r)`, `web.Caller(r)`: `"user"` for a session, `"api"` for a token). Account routes add `sessionOnly` or `adminOnly`.
- Answer with `web.JSON` (no HTML escaping, no trailing newline; a struct when field order matters) or `web.Text`.
- List the route in `internal/web/discovery/llms.txt` and `endpoints.json`, and in `openapi.json` if it has a schema: `TestDiscoveryListsExactlyTheRoutes` fails until the documents and the router agree.

## Reading the body

Every body is capped at 25 MiB (`web.MaxBody`); one past the cap reads as a body that isn't JSON.

| Helper | Is TS's | Use it when |
|---|---|---|
| `web.Object(web.Body(r))` | `await c.req.json().catch(() => ({}))` | The body is an object of optional fields, and anything else may read as `{}`. The usual choice for a new route. |
| `web.JSONBody(r)` | `await c.req.json()`, no catch | A body that isn't JSON is the route's error to answer (its message is V8's, or `web.ErrBodyTooLarge`); JSON `null` is `nil`. |
| `openapiJSON(r)` | zod-openapi's json validator, body required | A route TS declared with `createRoute`: the body is parsed only when the Content-Type says JSON (else `{}`); `ok == false` is answered with `malformedJSON(w)`. |
| `optionalJSONBody(r)` | the same, body not required | As `openapiJSON`, but without a JSON Content-Type the body is `{}` and unchecked (`checked == false`). |
| `web.RawBody(r)` | `c.req.text()` | The text itself, e.g. for the key order of a JSON object (`json_order.go`). |

## Answering errors

- `tryJSON(w, fn)` — TS's `try { … } catch (e) { return c.json({ error: e.message }, 500) }`: `fn` returns `(status, body, err)`; an `err` is a 500 `{"error": …}`. Inside it, `fail(status, msg)` answers `{"error": msg}` with that status.
- `tryList(w, fn)` — the same for the lists, where a `*FieldError` (an unknown filter or sort field, too many values) is a 400.
- `try400(w, fn)` — every error is a 400 `{"error": …}` (the custom-field routes).
- A handler that returns an error itself (no wrapper) answers Hono's plain `500 Internal Server Error`; a panic does too.

## Validation: zod issues

The routes TS declared with zod answer a bad request with zod 3's issues, built in `hono.go`: the issue builders (`invalidType`, `requiredIssue`, `stringTooSmall` / `stringTooBig`, `arrayTooSmall` / `arrayTooBig`, `checkEnum`, `checkType`) and the schema combinators that compose them as the TS schemas did (`zObject(zKey("label", zString(1, 0)), zKey("position", zOptional(zInt())))`, `zEnum`, `zArray`, `zNullable`, `zBool`, …), run with `zodIssues(schema, v)` and answered with `web.ZodError(w, issues)`. Query strings are read as Hono read them: `queryInput(r)`, checked with `optionalStrings`.

## Why the JavaScript emulation

The routes reproduce what the TS API answered, byte for byte where a client can tell: JSON key order, JS number formatting and parsing, truthiness, `Date` parsing (SQLite's `datetime('now')` read as UTC), UTF-16 string lengths, V8's JSON error messages, zod's issues. That is what `internal/js`, `hono.go`, `json_order.go` and the comments naming a TS expression are for: parity with the old API, which the frontend and the agents using it were built against. A new route doesn't need to emulate anything; a change to an existing one must keep the parity harness at 0 mismatches, or change a scenario on purpose.

## Tests

```bash
go test ./...   # from server/ (pnpm test, from the root)
go vet ./...
gofmt -l .      # lists nothing
```

- `dbtest.Setup(t)` gives a test its own SQLite database with `schema.sql` and installs it; the installed driver is package state, so those tests don't run in parallel.
- Tests that sign in call `auth.SetCostForTests(t, bcrypt.MinCost)`: production's bcrypt cost (12) takes a quarter of a second per password.
- `testdata/*_ts.json` hold what the TS routes answered, with every SQL statement, frozen from the TS reference by the `*_ts.gen.mts` generators; the `*_ts_test.go` tests replay them through the Go server.
- The parity harness, from the repository root: `scripts/parity/run.sh [scenario …]` runs the TS reference (commit `0068887`, checked out into `.parity/ts-ref`) and `cmd/devserver` side by side on identically seeded databases, sends both the calls in `scripts/parity/scenarios/*.mjs` and prints the mismatches. It needs Linux (`ss`, `setsid`) and ports 8788, 8790 and 9230.
