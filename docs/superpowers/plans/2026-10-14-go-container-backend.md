# Go Backend on Cloudflare Containers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the TypeScript/Hono API (`src/server/*.ts`, ~8,300 lines) with a Go server that runs in a Cloudflare Container, keeping every `/api` endpoint's behavior identical, all configured from one `wrangler.toml`.

**Architecture:** A thin Worker (`src/worker/index.ts`) serves the built React app from `[assets]` and forwards `/api/*` and `/llms.txt` to a single Go container instance (a `Container` Durable Object). The container cannot see Worker bindings, so the Worker's `outboundByHost` handlers bridge them: the Go server calls `http://d1.internal/query` for D1 and `http://credentials.internal/v1/connections/*` for the Clawnify CREDENTIALS binding. Everything else (Clawnify queue, services, model, Granola) the Go server calls over the internet as the TS code did.

**Tech Stack:** Go 1.22+ standard library (`net/http` with method patterns, `encoding/json`, `crypto/ecdsa`); `modernc.org/sqlite` for tests and the native dev server only; `@cloudflare/containers@0.3.7`; `wrangler@4.151.0`; Docker for image builds.

**Spec:** The user's request: "change the backend of this project to use Go and Cloudflare Containers, all with a single wrangler.toml" (user asked to skip questions; the decisions below are Claude's calls). A spike on 2026-10-14 proved Go-in-container → `outboundByHost` → `env.DB` works under `wrangler dev` (~13 ms warm round trip).

## Global Constraints

- Behavior parity: every route keeps its method, path, status codes, JSON field names, error strings and SQL. The TS code at commit `0068887` is the reference; the parity harness (Task 2) is the judge.
- Copy SQL strings byte-for-byte from the TS source. Copy user-facing strings (errors, reasons, summaries) byte-for-byte.
- Go module root: `server/`, module path `opencrm`. `go 1.22` in `go.mod`; must build with the local `go1.22.2` (`GOTOOLCHAIN=local`) and in the Docker image `golang:1.24-alpine`.
- Production binary (`server/main.go`) imports only the standard library and `opencrm/...`. `modernc.org/sqlite` is allowed only in `server/internal/db/sqlite`, `server/cmd/devserver` and `_test.go` files.
- Numbers read from the database are `float64` in Go (JS number semantics), whichever driver returned them.
- JSON responses: `Content-Type: application/json`, no HTML escaping (`SetEscapeHTML(false)`), no trailing newline.
- Every exported Go function that touches the DB or the network takes `ctx context.Context` first.
- Go files mirror TS files: `src/server/custom-fields.ts` → `server/internal/crm/custom_fields.go`. Routes from `index.ts` go to `server/internal/crm/routes_<area>.go`. Carry over the TS doc comments that explain *why*, adapted to Go.
- Routes register themselves with `func init() { register(func(rt *web.Router, app *App) { ... }) }` so parallel tasks never edit a shared file.
- Wire protocol for the D1 bridge and credentials bridge is fixed in Task 1 / Task 3 — do not change it in later tasks.
- Container: class `Api`, binding `API`, one named instance `"api"`, `defaultPort = 8080`, `sleepAfter = "10m"`, `instance_type = "basic"`, `max_instances = 1`.

## Review Focus

1. A partial update that sends a field as `null` versus omitting it (`PUT /api/companies/{id}` with `{"domain": null}` vs `{}`): TS distinguishes `undefined` from `null`; Go must too. Test owned by Task 9.
2. Names with `&`, `<`, `>` or non-ASCII ("Cedar & Co", "Zoë"): Go's encoder escapes `&` as `&` by default; the API must return them as-is. Test owned by Task 1 (`web.JSON`) and covered by the seed data in the parity run.
3. Bulk operations past D1's 100 bound-parameter cap (bulk-delete of 150 ids, filters with many values): must chunk exactly as TS does. Test owned by Task 9.
4. Malformed or missing JSON bodies on non-OpenAPI routes (`c.req.json().catch(() => ({}))`): must behave like an empty object, never a 500. Test owned by Task 11 (views) and Task 1 (`web.Object`).
5. SQLite `datetime('now')` strings ("2026-10-14 16:10:00") parsed as UTC, not local time, wherever TS did `new Date(x)`/`Date.parse(x)`. Test owned by Task 1 (`js.ParseDate`) and Task 8 (customer health).

---

## File Structure

```
wrangler.toml                         # single config: worker, assets, container, DO, D1
src/worker/index.ts                   # Worker: Api container class, D1 + credentials bridges, routing
server/
  go.mod  go.sum  Dockerfile  .dockerignore
  main.go                             # prod entry: D1 bridge driver, :8080
  schema.sql                          # moved from src/server/schema.sql
  cmd/devserver/main.go               # native entry: -sqlite <file> -addr :8790
  internal/db/db.go                   # Driver interface + package-level Query/Get/Run
  internal/db/d1.go                   # D1 bridge driver
  internal/db/sqlite/sqlite.go        # modernc driver (dev/tests)
  internal/db/dbtest/dbtest.go        # Setup(t): temp sqlite + schema.sql + db.Use
  internal/js/js.go                   # JS semantics: UUID, ISO, ParseDate, Number, String, Truthy
  internal/web/*.go                   # Router, JSON/Text, Body/Object/RawBody, ZodError, identity, Origin, Background
  internal/web/discovery/             # openapi.json, llms.txt, endpoints.json (snapshots, committed) + discovery.go
  internal/clawnify/*.go              # Env, queue, services, connections
  internal/crm/*.go                   # one file per TS module + routes_<area>.go + app.go
scripts/parity/run.sh                 # boots TS reference + Go on identical seeded DBs, runs scenarios
scripts/parity/runner.mjs             # runs scenarios on both, normalizes, diffs
scripts/parity/scenarios/<area>.mjs   # one file per area, added by each porting task
```

---

### Task 1: Go server skeleton, Worker, container config

**Files:**
- Create: `wrangler.toml` (replace), `src/worker/index.ts`, `server/go.mod`, `server/Dockerfile`, `server/.dockerignore`, `server/main.go`, `server/cmd/devserver/main.go`, `server/internal/db/{db.go,d1.go,db_test.go}`, `server/internal/db/sqlite/sqlite.go`, `server/internal/db/dbtest/dbtest.go`, `server/internal/js/{js.go,js_test.go}`, `server/internal/web/{router.go,io.go,identity.go,background.go,web_test.go}`, `server/internal/web/discovery/discovery.go`, `server/internal/crm/app.go`
- Move: `src/server/schema.sql` → `server/schema.sql` (`git mv`); update `package.json` `db:migrate` to `--file=server/schema.sql`
- Modify: `package.json` (add `@cloudflare/containers` `0.3.7`, bump `wrangler` to `4.151.0`), `.gitignore` (add `.parity/`)
- Already present (committed with this plan): `server/internal/web/discovery/{openapi.json,llms.txt,endpoints.json}` captured from the TS app.

**Interfaces:**
- Produces (package `db`):
  - `type Row = map[string]any`; `type Result struct{ Changes, LastRowID int64 }`
  - `type Driver interface { Query(ctx context.Context, sql string, params []any) ([]Row, error); Run(ctx context.Context, sql string, params []any) (Result, error) }`
  - `func Use(d Driver)`; `func Query(ctx, sql string, params ...any) ([]Row, error)`; `func Get(ctx, sql string, params ...any) (Row, error)` (first row, `nil` when none); `func Run(ctx, sql string, params ...any) (Result, error)`
  - Params allowed: `nil, string, bool, int, int64, float64, []byte`; anything else → error `@clawnify/db: param [i] is <type> — D1/SQLite only binds scalars...` (same text as `assertBindable`).
  - `func NewD1(baseURL string, hc *http.Client) Driver` — wire protocol: `POST {baseURL}/query` body `{"sql":string,"params":[...],"mode":"all"|"run"}`; 200 → `{"results":[{...}],"meta":{"changes":n,"last_row_id":n}}`; non-200 → `{"error":"<D1 message>"}` returned as `errors.New(message)`.
  - `sqlite.Open(path string) (db.Driver, error)` — sets `PRAGMA foreign_keys = ON` and `busy_timeout = 5000` (D1 enforces foreign keys).
  - `dbtest.Setup(t *testing.T) context.Context` — temp file DB with `server/schema.sql` applied, installed via `db.Use`.
- Produces (package `js`): `UUID() string`; `ISO(t time.Time) string` (`2006-01-02T15:04:05.000Z`, UTC); `ParseDate(s string) (time.Time, bool)` accepting `YYYY-MM-DD`, `YYYY-MM-DDTHH:MM[:SS[.fff]][Z|±HH:MM]` and `YYYY-MM-DD HH:MM:SS` — all without zone read as UTC; `Number(v any) float64` (JS `Number()`: nil→0, bool→0/1, trimmed ""→0, numeric string→value, else NaN); `String(v any) string` (JS `String()`, integral floats without `.0`); `Truthy(v any) bool`.
- Produces (package `web`):
  - `type HandlerFunc func(w http.ResponseWriter, r *http.Request) error`
  - `type Router struct{...}`; `NewRouter() *Router`; `(*Router).Handle(pattern string, h HandlerFunc)` with Go 1.22 patterns (`"GET /api/companies/{id}"`); `ServeHTTP` sends unmatched paths **and wrong methods** to the not-found handler (use `mux.Handler(r)`; empty pattern ⇒ not found), turns a returned error or panic into `500 text/plain "Internal Server Error"`.
  - `JSON(w, status int, v any) error`; `Text(w, status int, contentType, body string) error`
  - `Body(r) any` (parsed JSON or `nil` when empty/invalid; body cached so `RawBody` still works); `Object(v any) map[string]any` (the map, else empty map); `RawBody(r) string`
  - `type Issue struct{ Code string; Expected, Received any; Path []any; Message string; Minimum, Maximum any; Type string; Inclusive, Exact *bool; Options []string }` (JSON names as zod 3: `code, expected, received, path, message, minimum, maximum, type, inclusive, exact, options`, omitempty) and `ZodError(w, issues []Issue) error` → `400 {"success":false,"error":{"issues":[...],"name":"ZodError"}}`
  - `Caller(r) string` (header `X-Clawnify-Caller` if in `user, api, agent, agent-browser, public, bypass, system, app`, else `"public"`); `OrgID(r) string`; `type User struct{ ID string; Email, Name, AvatarURL, FirstName, LastName *string }`; `CurrentUser(r) *User` (headers `X-Clawnify-User-Id/-Email/-Name/-Avatar`, trimmed, empty ⇒ absent; name split on whitespace as `@clawnify/app`).
  - `Origin(r) string` — `X-Forwarded-Proto` + `X-Forwarded-Host` when present (the Worker sets them), else `"http://" + r.Host`.
  - `Background(r *http.Request, fn func(ctx context.Context))` — `waitUntil`: goroutine on `context.WithoutCancel(r.Context())`, tracked so `web.Drain(timeout)` waits on shutdown.
- Produces (package `discovery`): `Mount(rt *web.Router)` — `GET /api/openapi.json` (embedded file, `application/json`), `GET /llms.txt` (`text/markdown; charset=utf-8`, `Cache-Control: public, max-age=300`), and the router's not-found: `/api*` → `404 {"error":"not_found","message":"No route for <METHOD> <path> on this app.","hint":"GET /llms.txt lists every endpoint; GET /api/openapi.json has full request/response schemas."}` plus `"endpoints": <endpoints.json>` when `X-Clawnify-Caller` is present and not `public`; other paths → `404 text/plain; charset=UTF-8 "404 Not Found"`.
- Produces (package `crm`): `type App struct{ Env clawnify.Env }` (Task 3 adds fields); `func register(fn func(rt *web.Router, app *App))`; `func New(app *App) http.Handler` (router + discovery + every registered route). Until Task 3 lands, `App` holds an empty `Env struct{}` placeholder defined in `crm/app.go`; Task 3 replaces it with `clawnify.Env`.
- Worker (`src/worker/index.ts`): `export class Api extends Container` with `defaultPort = 8080`, `sleepAfter = "10m"`, `envVars` built in the constructor from `env`: `D1_URL="http://d1.internal"`, plus `CLAWNIFY_TOKEN, CLAWNIFY_ORG_ID, CLAWNIFY_API_URL, CLAWNIFY_QUEUE_URL, CLAWNIFY_SERVICES_URL, SLACK_CHANNEL` when set. `Api.outboundByHost["d1.internal"]` implements the D1 wire protocol above with `env.DB.prepare(sql).bind(...params)` → `.all()` / `.run()`, catching errors into `{error: e.message}` status 500. `export { ContainerProxy }`. Default fetch: copy the request, set `X-Forwarded-Host`/`X-Forwarded-Proto` from the URL, forward to `getContainer(env.API, "api")`.
- `wrangler.toml`:

```toml
name = "open-crm"
main = "src/worker/index.ts"
compatibility_date = "2026-06-01"

[assets]
directory = "./dist"
not_found_handling = "single-page-application"
run_worker_first = ["/api/*", "/llms.txt"]

[[containers]]
class_name = "Api"
image = "./server/Dockerfile"
instance_type = "basic"
max_instances = 1

[[durable_objects.bindings]]
name = "API"
class_name = "Api"

[[migrations]]
tag = "v1"
new_sqlite_classes = ["Api"]

[[d1_databases]]
binding = "DB"
database_name = "open-crm-db"
database_id = "local"
```

- Dockerfile: `golang:1.24-alpine` build stage (`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /api .`), runtime `gcr.io/distroless/static-debian12:nonroot` (has CA certs for HTTPS to Clawnify), `EXPOSE 8080`.
- `main.go`: reads `PORT` (default `8080`) and `D1_URL` (required), `db.Use(db.NewD1(...))`, serves `crm.New(...)`, drains background work on SIGTERM.
- `cmd/devserver`: flags `-sqlite`, `-addr` (default `:8790`); same handler.

- [ ] **Step 1: Write failing tests** — `js_test.go`: `ParseDate("2026-10-14 16:10:00")` == `2026-10-14T16:10:00Z`; `ParseDate("2026-10-14")` == midnight UTC; `ParseDate("2026-10-14T16:10:00+02:00")` == `14:10Z`; `ParseDate("nope")` → false; `Number(" 12 ")==12`, `Number("")==0`, `math.IsNaN(Number("x"))`; `String(3.0)=="3"`, `String(0.1)=="0.1"`; `ISO` of `time.Date(2026,10,14,16,10,0,5e6,time.UTC)` == `"2026-10-14T16:10:00.005Z"`. `web_test.go`: `JSON` of `{"name":"Cedar & Co <x>"}` writes `{"name":"Cedar & Co <x>"}` exactly; `Object(Body(r))` for body `not json` is an empty map; router: `PATCH /api/companies` (only GET registered) → 404 JSON `not_found` body; `GET /nope` → `404 Not Found` text; handler returning error → 500 `Internal Server Error`; `Caller` with header `agent` → `agent`, with `hacker` → `public`. `db_test.go`: `NewD1` against an `httptest.Server` that echoes the protocol — `Get` returns first row with numbers as `float64`; server error `{"error":"D1_ERROR: boom"}` → `err.Error()=="D1_ERROR: boom"`; `Query(ctx,"x", struct{}{})` errors with the `@clawnify/db: param [0]` text; sqlite driver: inserting a contact with a missing `company_id` FK fails (foreign keys on).
- [ ] **Step 2: Run** `cd server && GOTOOLCHAIN=local go test ./...` — Expected: FAIL (packages don't exist).
- [ ] **Step 3: Implement** the packages, `main.go`, `cmd/devserver`, Dockerfile, Worker, `wrangler.toml`, package.json changes (`pnpm install` to update the lockfile).
- [ ] **Step 4: Run** `cd server && GOTOOLCHAIN=local go test ./... && go vet ./...` — Expected: PASS.
- [ ] **Step 5: Container smoke test.** `mkdir -p dist && pnpm db:migrate && npx wrangler dev --port 8787` (Docker builds the image); then `curl -s localhost:8787/llms.txt | head -1` → `# OpenCRM`; `curl -s localhost:8787/api/nope` → the `not_found` JSON; `curl -s -o /dev/null -w '%{http_code}' localhost:8787/api/openapi.json` → `200`. Stop wrangler and remove leftover `workerd-*` containers (`docker ps -a --filter name=workerd- -q | xargs -r docker rm -f`). If `[assets]` fails without `dist/`, keep the `mkdir -p dist` in the `dev` script.
- [ ] **Step 6: Commit** — `git add -A && git commit -m "Go server skeleton in a Cloudflare Container, bridged to D1 by the Worker"`

---

### Task 2: Parity harness

**Files:**
- Create: `scripts/parity/run.sh`, `scripts/parity/runner.mjs`, `scripts/parity/scenarios/discovery.mjs`

**Interfaces:**
- Consumes: `server/cmd/devserver` (Task 1).
- Produces: `scripts/parity/run.sh [scenario-name ...]` — exit 0 when every scenario matches. Each later task adds `scripts/parity/scenarios/<area>.mjs` exporting `default [{ name: string, run: async (call) => void }]`, where `call(method, path, { body?, headers? }) → Promise<{ status, body }>` (`body` parsed JSON, else text). Scenarios run in file-name order, sequentially, on one fresh DB per backend.

`run.sh` steps (all state under `.parity/`, gitignored):
1. `git worktree add --detach .parity/ts-ref ${PARITY_BASE:-0068887}` if missing; symlink the repo's `node_modules` into it.
2. For `ts` and `go`: fresh `--persist-to .parity/state-<x>`; from `.parity/ts-ref` run `npx wrangler d1 execute open-crm-db --local --persist-to <dir> --file=src/server/schema.sql` then `--file=../../demo/seed.sql` (paths resolved absolutely).
3. Start TS: in `.parity/ts-ref`, `npx wrangler dev --port 8788 --persist-to ../state-ts`. Start Go: `go run ./server/cmd/devserver -sqlite <the single *.sqlite under .parity/state-go/v3/d1/> -addr :8790`. Wait until both answer `GET /api/stats`.
4. `node scripts/parity/runner.mjs http://localhost:8788 http://localhost:8790 [names]`; always kill both servers on exit (trap), killing by PID file, never by `pkill -f` pattern.

`runner.mjs` normalization, applied per scenario to every recorded `{method, path, status, body}`: UUIDs (`/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/`) → `<id:N>` numbered by first appearance; timestamps (`/\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?Z?/`) → `<ts>`; object keys sorted. Report: scenario name, call index, method+path, and a compact JSON diff of the first mismatch per call; summary line `parity: N scenarios, M calls, K mismatches`.

- [ ] **Step 1: Write** `scenarios/discovery.mjs` — calls `GET /llms.txt`, `GET /api/openapi.json`, `GET /api/nope`, `GET /api/nope` with header `X-Clawnify-Caller: user`, `PATCH /api/companies`, `GET /nope`.
- [ ] **Step 2: Run** `scripts/parity/run.sh discovery` — Expected: `parity: 1 scenarios, 6 calls, 0 mismatches`.
- [ ] **Step 3: Prove it catches a difference**: temporarily change the not-found `message` in Go, run again, expect exit 1 with the diff shown, revert.
- [ ] **Step 4: Commit** — `git commit -m "Parity harness: the TS reference and the Go server answer the same calls"`

---

### Task 3: Clawnify clients, credentials bridge, model

**Files:**
- Create: `server/internal/clawnify/{env.go,queue.go,services.go,connections.go,clawnify_test.go}`, `server/internal/crm/model.go` (+ `model_test.go`)
- Modify: `server/internal/crm/app.go` (replace placeholder `Env` with `clawnify.Env`; add `Conn *clawnify.Connections`), `server/main.go`, `server/cmd/devserver/main.go` (`clawnify.EnvFromOS()`), `src/worker/index.ts` (credentials bridge + `CLAWNIFY_CONNECTIONS_URL` env var)

**Interfaces:**
- Consumes: `web`, `js` (Task 1).
- Produces (package `clawnify`) — ports of `@clawnify/queue`, `@clawnify/services`, `@clawnify/connections` as read in `node_modules/@clawnify/*/dist/index.js`:
  - `type Env struct{ Token, OrgID, APIURL, QueueURL, ServicesURL, ConnectionsURL, SlackChannel string }`; `EnvFromOS() Env` (env vars `CLAWNIFY_TOKEN, CLAWNIFY_ORG_ID, CLAWNIFY_API_URL, CLAWNIFY_QUEUE_URL, CLAWNIFY_SERVICES_URL, CLAWNIFY_CONNECTIONS_URL, SLACK_CHANNEL`).
  - `type EnqueueOptions struct{ TargetURL string; Payload any; RunAt time.Time; IdempotencyKey string; MaxAttempts int }`; `type Job struct{ ID string \`json:"id"\` }`; `Enqueue(ctx, env, o) (Job, error)`; `CancelJob(ctx, env, id string) error` (default base `https://services.clawnify.com/queue`; `CLAWNIFY_TOKEN is not set — the queue service is unavailable.` when no token).
  - `VerifyDelivery(ctx, rawBody, signature, timestamp, keyID string) bool` — JWKS `https://services.clawnify.com/.well-known/jwks.json` (cached), 300 s tolerance, ECDSA P-256 SHA-256 over `"<ts>.<rawBody>"`, signature base64 of raw `r||s` (64 bytes).
  - `type MarkdownResult` with the fields `ai-columns.ts` reads from `renderMarkdown`; `RenderMarkdown(ctx, env, url string, maxChars int) (MarkdownResult, error)` (POST `/markdown/render`, default base `https://services.clawnify.com`); errors carry `detail || error || "<path> answered <status>"`.
  - `type Connections struct{...}`; `NewConnections(env Env, hc *http.Client) *Connections`; `(c) Run(ctx, service, action string, args map[string]any) (any, error)`; `(c) Connected(ctx) []string` (nil on any failure); `(c) Token(ctx, service string) string` (`""` when none); `(c) Get(ctx, service, endpoint string, query map[string]string) (any, error)` — registry-backed direct call; only `granola` is needed: base `https://public-api.granola.ai`, header `authorization: Bearer <token>`, response parsed like `parseResponse`.
  - Broker selection: `ConnectionsURL` set ⇒ that base, no auth header (the Worker bridge); else `Token` set ⇒ `APIURL` or `https://provision.clawnify.com` with `Authorization: Bearer <token>` and `x-org-id` when `OrgID` set; else `Run` → error `"<service>" needs the Clawnify credentials binding or CLAWNIFY_TOKEN`, `Connected` → nil. Endpoints: `POST /v1/connections/token {service}` → `{token}`; `POST /v1/connections/execute {service, action, args}` → `{data, error, successful}`; `GET /v1/connections/connected` → `{services}`. Error text follows `errText`.
- Produces (package `crm`, port of `model.ts`): `const Model = "google/gemini-3.1-flash-lite"`; `type ModelError struct{...}` implementing `error`; `Complete(ctx, env clawnify.Env, <params exactly as model.ts complete()>) (string, error)`.
- Worker bridge: `Api.outboundByHost["credentials.internal"]` implements the three endpoints above with `env.CREDENTIALS.getToken(service, orgId)`, `.executeTool(service, action, args, orgId)`, `.listConnected(orgId)` where `orgId = env.CLAWNIFY_ORG_ID ?? ""`; the constructor sets `CLAWNIFY_CONNECTIONS_URL="http://credentials.internal"` only when `env.CREDENTIALS` exists.

- [ ] **Step 1: Write failing tests** with `httptest.Server` fakes: `Enqueue` sends `Authorization: Bearer t`, body keys `target_url, payload, run_at (ISO), idempotency_key, max_attempts`; queue 400 `{"detail":"bad"}` → error `bad`; `VerifyDelivery` true for a body signed with a test key served by a fake JWKS (inject the JWKS URL through an unexported variable), false when the timestamp is 301 s old; `Connections.Run` through a fake broker returns `data`, and `{successful:false,error:"nope"}` → error `nope`; with empty Env, `Run` errors with the exact "needs the Clawnify credentials binding" text and `Connected` is nil; `Get("granola","/v1/notes",...)` sends `authorization: Bearer <token from /token>`; `Complete` sends the request shape `model.ts` sends and returns the text; no token → `ModelError` with `AI isn't available here: the app has no Clawnify token`.
- [ ] **Step 2: Run** `cd server && GOTOOLCHAIN=local go test ./internal/clawnify/ ./internal/crm/` — Expected: FAIL.
- [ ] **Step 3: Implement**; add the credentials bridge to `src/worker/index.ts`.
- [ ] **Step 4: Run** tests — Expected: PASS. `npx tsc --noEmit -p .` shows no errors in `src/worker/`.
- [ ] **Step 5: Commit** — `git commit -m "Clawnify queue, services, connections and model clients in Go"`

---

### Task 4: Pure rules

**Files:**
- Create: `server/internal/crm/{email_sync_rules.go,meetings_rules.go,jobs.go,email_domains.go}` + `_test.go` for each; `server/internal/crm/freemail.txt` (embedded; generated once from `node_modules/free-email-domains`, lower-cased, one per line)

**Interfaces:**
- Consumes: `db`, `js` (Task 1).
- Produces: Go ports of every export listed below, same names in Go case (`parseAddresses` → `ParseAddresses`), same parameters, `Date` → `time.Time`, `T | null` → pointer or `(T, bool)`; TS interfaces → structs with the same JSON field names as tags:
  - `email-sync-rules.ts`: `Address, AutoCreate, History, ParseAddresses, DomainOf, IsGroupAddress, NormaliseBlocklist, IsBlocked, CreateRules, MayCreate, BulkCategories, CreationCandidates, SplitName, HistoryStart, Scope, ScopeTerms, SentQuery, ReceivedQuery, PeopleQuery, LiveOverlapMs, LabelOverlapMs, SinceQuery`
  - `meetings-rules.ts`: all exports (types, consts `PlaceSlackMs, FreshDays, QuietYellowDays, QuietRedDays, RenewalSoonDays, DealQuietDays, InsightKinds`, functions `NormaliseDomain … DealHealth`)
  - `jobs.ts`: `BookableAt(runAt, now time.Time) time.Time`
  - `email-domains.ts`: `FreemailDomains map[string]bool`, `WorkEmailDomain(email string) string`, `FindOrCreateCompanyByDomain(ctx, domain string) (string, error)`

- [ ] **Step 1: Write failing tests** — table tests whose cases come from reading the TS functions' branches, at least: `ParseAddresses(`"Ann Lee" <ann@x.com>, bob@y.org`)` → two addresses with names; `IsGroupAddress("noreply@x.com")` true; `BookableAt` returns next minute when `runAt` is in the current minute, else `runAt`; `CoerceDigest` on a JSON reply wrapped in ```` ```json ```` fences; `Health` for an account with no contact in 46 days and sight `seen` → `red` with the TS reason string; `Health` with all syncs `off` → `unknown`; `DealHealth` for a deal whose close date passed; `WorkEmailDomain("a@gmail.com")==""`; `FindOrCreateCompanyByDomain` creates once and returns the same id twice (uses `dbtest.Setup`).
- [ ] **Step 2: Run** `go test ./internal/crm/ -run 'Rules|Jobs|Domain'` — Expected: FAIL.
- [ ] **Step 3: Implement** by translating each function statement by statement.
- [ ] **Step 4: Run** — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "Port email-sync, meetings and job rules to Go"`

---

### Task 5: Custom fields, relations and the shared list/write helpers

**Files:**
- Create: `server/internal/crm/{custom_fields.go,relations.go,lists.go}` + tests
- Source: `custom-fields.ts`, `relations.ts`, and `index.ts` lines 63–560 (`chunk, isDay, withRelationsOne, logActivity, applyCustomValues, CustomValues, resolveCustomWrite, unknownFieldsError, quoteIdent, coerceForImport, resolveImportCustomColumns, tableColumns, filter value cap, localDay, relativeRange, buildFilters, tzOffsetOf, kindOf, aggregateSQL, aggregateColumns, customAggregateColumns`)

**Interfaces:**
- Consumes: `db`, `js`, `web` (Task 1).
- Produces: `EntityType` (string type with `Contact, Company, Deal`), `EntityTables`, `IsEntityType`, `CustomFieldDef` (JSON tags as the TS object), `ListDefs(ctx, entity *EntityType)`, `GetDef, CreateDef, CreateRelation, UpdateDef, DeleteDef, SyncEntityColumns, CoerceCustomValue, ClassifyCustomWrite, MissingRequiredCustom, WritableFieldKeys, HasColumn, AssertValidKey`; relations: `RelationRecord, SearchRecords, WithRelations(ctx, entity, rows []db.Row, manyLimit int) ([]db.Row, error), RelationWriteError, ManyLink, CountSQL, CountOf, ManyLinks, RelationSortSQL, DetachRelations`; helpers: `Chunk[T any]([]T, int) [][]T`, `IsDay(string) bool`, `WithRelationsOne`, `LogActivity(ctx, entityType, entityID, typ, body string, meta map[string]any)`, `ApplyCustomValues`, `ResolveCustomWrite(ctx, entity, body map[string]any) (values map[string]any, unknown []string, err error)`, `UnknownFieldsError(ctx, entity, unknown) (map[string]any, error)` (nil when clean), `QuoteIdent`, `CoerceForImport`, `ResolveImportCustomColumns`, `TableColumns`, `BuildFilters`, `TzOffsetOf`, `AggregateColumns`, `CustomAggregateColumns`, `RelativeRange` — signatures follow the TS ones with `ctx` first and errors returned.
- Errors that TS throws with a message a route turns into a 4xx (e.g. enum violations in `coerceCustomValue`, invalid keys) are a distinct type `*FieldError{Msg string}` so routes can match them with `errors.As`.

- [ ] **Step 1: Write failing tests** (`dbtest.Setup`): `CreateDef` for `company`/`text` key `tier` adds a `tier` column (`TableColumns` includes it); `CoerceCustomValue` rejects a value outside a `select` field's options with the TS message; `ClassifyCustomWrite` with `{name:"x", tier:"a", bogus:1}` → values `{tier:"a"}`, unknown `["bogus"]`; `BuildFilters` for `{"op":"and","rules":[{"field":"name","op":"contains","value":"Acme"}]}` → the same SQL and params the TS function builds for that input (copy them from running the TS function mentally or with `node -e`); `RelativeRange("PAST_7_DAY", now)` boundaries; `WithRelations` attaches a many-to-one relation; `Chunk` of 150 by 90 → sizes `[90,60]`.
- [ ] **Step 2: Run** `go test ./internal/crm/ -run 'Custom|Relation|Filter|Range|Chunk'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "Port custom fields, relations and list filters to Go"`

---

### Tasks 6–12: Route and module ports

Each of these tasks follows the same steps; only the sources, files and scenario differ.

- [ ] **Step 1: Write the parity scenario** `scripts/parity/scenarios/<area>.mjs` covering every route in the task: each success path, each 4xx branch in the TS handler (missing/invalid fields, unknown ids → 404, unknown custom fields → 422, zod validation on OpenAPI routes), and the Review Focus inputs the task owns.
- [ ] **Step 2: Run** `scripts/parity/run.sh <area>` — Expected: FAIL (Go answers 404 `not_found`).
- [ ] **Step 3: Write Go unit tests** for module logic the scenario cannot reach without real integrations (Gmail/Calendar/Granola/model responses), using `httptest` fakes behind `clawnify.Env` URLs.
- [ ] **Step 4: Implement** the module files and `routes_<area>.go` (handlers registered via `register`). OpenAPI routes translate their zod schemas into `web.ZodError` issues with zod 3's codes and messages for the cases the scenario exercises (`invalid_type` "Required", `too_small`, `invalid_enum_value`, `invalid_string`). `c.executionCtx.waitUntil(p)` → `web.Background`. `new URL(c.req.url).origin` → `web.Origin(r)`.
- [ ] **Step 5: Run** `cd server && GOTOOLCHAIN=local go test ./... && go vet ./...` and `scripts/parity/run.sh <area>` — Expected: PASS, `0 mismatches`.
- [ ] **Step 6: Commit** — `git commit -m "<area>: served by the Go backend"`

| Task | Area (`<area>`) | TS sources | Go files | Depends on |
|---|---|---|---|---|
| 6 | `email` | `integrations.ts`, `email-sync.ts`, `index.ts` 2074–2490 (integrations status/email/meeting, email-sync settings/labels/run/sync-now, contact emails, open email) | `integrations.go`, `email_sync.go`, `routes_email.go` | 3, 4, 5 |
| 7 | `meetings` | `meetings.ts`, `index.ts` 2491–2738 (meetings sync settings/run/sync-now, list/get/link/digest) | `meetings.go`, `routes_meetings.go` | 3, 4, 6 (`integrations.go`) |
| 8 | `customers` | `customers.ts`, `deal-progress.ts`, `lookup.ts`, `index.ts` 3148–3278 | `customers.go`, `deal_progress.go`, `lookup.go`, `routes_customers.go` | 6, 7 |
| 9 | `companies-contacts` | `index.ts` 560–1330 (stats, widgets, companies + contacts list/aggregates/create/update/delete/bulk-delete) | `routes_stats.go`, `routes_companies.go`, `routes_contacts.go` | 5, 8 (`progressOf` for widgets if used) |
| 10 | `deals` | `index.ts` 1331–2036 (stages, deals board/progress/list/create/update/delete, customer marking), 2739–3147 (tasks, deal updates, insights), 3656–3740 (GET contact/deal/company by id, records, values) | `routes_stages.go`, `routes_deals.go`, `routes_tasks.go`, `routes_records.go` | 5, 8 |
| 11 | `settings` | `index.ts` 2037–2073 (activities), 3400–3655 (CSV import), 3741–3981 (custom fields, views) | `routes_activities.go`, `routes_import.go`, `routes_custom_fields.go`, `routes_views.go` | 5 |
| 12 | `ai-columns` | `ai-columns.ts`, `index.ts` 3279–3399 | `ai_columns.go`, `routes_ai_columns.go` | 3, 5 |

Task-specific test lines (add to that task's scenario or unit tests):
- Task 9: `PUT /api/companies/{id}` with `{"domain": null}` then `{}` — both match TS (Review Focus 1); `POST /api/companies/bulk-delete` with 150 ids (Review Focus 3); a company named `Cedar & Co <b>` round-trips unescaped (Review Focus 2).
- Task 8: unit test — a customer whose last meeting `started_at` is `"<46 days ago> 10:00:00"` (SQLite format) is `red` with the quiet reason, computed in UTC (Review Focus 5).
- Task 11: `PATCH /api/views/{id}` and `POST /api/views` with body `not json` behave as TS (Review Focus 4).
- Task 6: unit tests — `runSync` against a fake broker returning one `FETCH_EMAILS` page stores the email row with the TS columns; `/api/email-sync/run` with a valid signature (fake JWKS) and no caller header runs; without a signature and caller `public` → 403 `Sign in to run a sync.`
- Task 7: unit test — `runMeetings` with a fake calendar event and Granola note links the meeting to the company by attendee domain and stores the digest from a fake model reply.
- Task 12: unit test — `runQueued` fills 20 cells from a fake model reply and never overwrites a non-empty cell.

---

### Task 13: Cutover

**Files:**
- Delete: `src/server/` (all `.ts`), `docs`/README references to Hono as the backend
- Modify: `package.json` (remove `hono`, `@hono/zod-openapi`, `zod`, `@clawnify/app`, `@clawnify/connections`, `@clawnify/db`, `@clawnify/queue`, `@clawnify/services`, `free-email-domains`, `@cloudflare/workers-types` only if `src/worker` no longer needs it; scripts: `"dev": "pnpm db:migrate && mkdir -p dist && concurrently -n ui,api -c cyan,green \"vite\" \"wrangler dev --port 8787\""`, `"deploy": "vite build && wrangler deploy"`, `"db:migrate:remote": "wrangler d1 execute open-crm-db --remote --file=server/schema.sql"`, `"test": "cd server && go test ./..."`), `tsconfig.json` (include `src/worker`), `README.md` (Tech Stack table: Backend = Go on Cloudflare Containers; Quickstart needs Docker + Go; deploy steps: `wrangler d1 create open-crm-db`, put its id in `wrangler.toml`, `pnpm db:migrate:remote`, `wrangler secret put CLAWNIFY_TOKEN` etc., `pnpm deploy`), `agent.md` (stack line), `clawnify.json` (`app.framework`: leave unchanged and note in the PR that Clawnify's deploy pipeline needs to support containers)
- Keep: `scripts/parity/*` (its TS reference comes from the `0068887` worktree, not from `src/server`)

- [ ] **Step 1: Run** the full suite: `cd server && GOTOOLCHAIN=local go test ./... && go vet ./...` and `scripts/parity/run.sh` (all scenarios) — Expected: PASS, `0 mismatches`.
- [ ] **Step 2: Apply** the deletions and edits above; `pnpm install`; `pnpm typecheck` — Expected: no errors (the pre-existing errors were all in `src/server/index.ts`).
- [ ] **Step 3: End-to-end in the container**: `pnpm dev`; in the browser at `http://localhost:5175` create a company, a contact and a deal, move the deal across stages, add a custom field and a view; `curl -s localhost:8787/api/stats` reflects them. Then stop the container (`docker rm -f` the `workerd-open-crm-Api-*` container) and repeat `curl -s localhost:8787/api/stats` — Expected: 200 after the cold start, not 5xx.
- [ ] **Step 4: Build check**: `docker build -f server/Dockerfile server -t opencrm-api` — Expected: success, image under 30 MB (`docker image ls opencrm-api`).
- [ ] **Step 5: Commit** — `git commit -m "The CRM's backend is Go in a Cloudflare Container"`
