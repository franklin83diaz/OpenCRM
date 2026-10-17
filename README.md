<img src="readme-banner.png" alt="OpenCRM preview" width="100%" />

# OpenCRM: The Open-Source HubSpot Alternative for SaaS

A lightweight CRM with contacts, companies, and deals — built for SaaS teams and AI agents. It runs on your own Cloudflare account: a Go API in a Cloudflare Container, with its data in D1.

Built with **React + Tailwind + shadcn/ui** on **Go + Cloudflare Containers + D1**. Path-based routing, UUID keys, its own accounts and API tokens, and a dual-mode UI: one for humans and one for AI agents (larger targets, always-visible actions).

## What Is It?

OpenCRM is a production-ready contact relationship manager. Think of it as an open-source HubSpot alternative — a CRM you can self-host and customize.

Unlike HubSpot or Salesforce, it runs on your own Cloudflare account, with no API keys for other services and no per-seat pricing. It provides a complete sales pipeline and lead management system. Manage contacts, companies, and deals with rich column types, inline editing, and full CRUD — all out of the box.

## Features

- **Three entities** — contacts, companies, and deals with foreign-key relationships (UUID keys, not enumerable ids)
- **Accounts and API tokens** — people sign in with an email and a password; an admin adds them in Settings → Users, and everyone can make personal API tokens for agents and scripts in Settings → API tokens (see [Accounts and API tokens](#accounts-and-api-tokens))
- **Activity timeline** — every contact/company/deal has a feed; notes, deal updates (a call, a message, an email, a meeting), and deals won or lost all log to it
- **CSV / XLSX import** — upload a spreadsheet, map columns to fields (exact-match auto-mapping), preview, import; company names resolve to existing companies or are created
- **Deal pipeline** — a board tracking deals through stages (prospect → qualified → proposal → negotiation → won/lost) with per-column totals
- **Next steps** (Deals → Next steps): every open deal shows its next step (its soonest task with a date, ours or theirs), and the open deals are listed worst first with the reasons in plain words: our promise overdue, no next step, waiting on them, the close date passed. The deal page shows its next step, its tasks and its activity. The CRM doesn't read email or calendars, so what happens outside it (a phone call, an email, a WhatsApp message, a meeting) is logged with `POST /api/deals/{id}/updates`, usually by an agent you told about it, and counts as contact
- **Customers** — the Customers page shows every customer worst first with the reasons in plain words (an overdue promise, waiting on them, a renewal coming up) and the few things to do first. A company becomes a customer from the close date of its first won deal, or by hand. It only calls an account on track through what it can see: since it reads no email or calendar, an account with nothing against it and no update logged lately shows "Can't tell" rather than "On track"
- **Path routing** — deep-linkable views and records: a row opens in a side panel beside the list (`/contacts?record=:id`), and expands to its full page (`/contacts/:id`)
- **Rich cells** — avatars, category badges, company favicons, tabular currency, email/phone links
- **Sorting, search, pagination** — server-side, debounced
- **Filters** — one chip per field (text contains/is, number ranges, multi-value status, dates: on, before, after, today, past/next N days/weeks/months), plus an advanced filter of rules joined by AND/OR with one level of rule groups; Open a list pre-filtered with `?filters=` (the same JSON the API takes)
- **Views** — named, shared views of each list ("All contacts" is the default): each keeps its own filters, sort and columns. Switch from the view bar, add one from the list as it is, edit a name in place, delete one; Update view saves your changes to it for everyone, Reset drops them. The default view is locked to the whole list: filter or sort it for the moment, and "Save as new view" keeps it. Export (in a view's menu, the default view included) downloads it as a CSV: its filters, sort and visible columns, every matching record
- **Relations** — link any two record types from Settings → Attributes ("each contact has one partner company", "each company has many subsidiaries", a record type to itself included). Both sides appear at once: the single link as a chip you pick from a search, the many side as a list on the record with "+" to link and × to unlink. The single side sorts by the linked record's name; both sides filter with is / is not / empty (a company's built-in Contacts and Deals, a contact's Company and Deals, a deal's Company and Contact too), and a many side also filters and sorts by its count ("Contacts count ≥ 3", most contacts first); deleting a record clears the links to it
- **Edit in the grid** — click a cell to change it in place: text in an input over the cell (Enter or a click away saves, Escape drops it); a status, an industry (the values already in use, or a new one) or tags from a list under it; linked records from a search that can also create one ("Add "Acme""), and on a many side link and unlink several. Email and phone cells copy on hover. An open record beside the list follows every change
- **Record grid** — the name column and header stay pinned while you scroll; columns are resizable (drag the divider) and hideable (the `+` at the end of the header), and the layout is saved on the list's view for the whole org; tick rows to export them as CSV or delete them in bulk (with the whole page ticked, Export asks: the selected rows, or every record the list selects); a footer calculates each column over the whole filtered list (count, empty %, unique, sum, average, min/max, earliest/latest), and "+ Add new" sits under the last row
- **Dual-mode UI** — human-optimized + AI-agent-optimized (`?agent=true`)

## Quickstart

### Prerequisites

- Node.js 20+ and pnpm
- Docker, running: `wrangler dev` builds the API's container image and runs it
- Go 1.22+, for the tests

Clone the repository, then from its folder:

```bash
cp .dev.vars.example .dev.vars   # fill in ADMIN_EMAIL and ADMIN_PASSWORD
pnpm install
pnpm dev
```

`pnpm dev` applies `server/schema.sql` to a local D1 database, then runs Vite (the UI, on port 5175) and `wrangler dev` (the Worker and the API's container, on port 8787). The first run also pulls the Go and distroless base images to build the container image.

Open `http://localhost:5175` in your browser and sign in with the email and password from `.dev.vars`: the first sign-in creates that admin. Data persists in the local D1 database under `.wrangler/state`.

### Agent Mode (for OpenClaw / Browser-Use)

Append `?agent=true` to the URL:

```
http://localhost:5175/?agent=true
```

This activates an agent-friendly UI with:
- Explicit "Edit" / "Delete" buttons on every row (no hover-to-reveal)
- Larger click targets for reliable browser automation
- Always-visible action buttons
- Semantic labels on all interactive elements

The human UI stays unchanged — hover-to-reveal actions, compact spacing, and a clean interface.

An agent that calls the API instead uses an API token; `/llms.txt` describes the API for it, and [`agent.md`](agent.md) is the longer guide.

## Accounts and API tokens

- **Sign-in** — people sign in with their email and password; a session lasts 30 days. Every `/api` call needs a signed-in person or an API token, except the sign-in itself and `GET /api/openapi.json`.
- **The first admin** comes from `ADMIN_EMAIL` and `ADMIN_PASSWORD`: `.dev.vars` locally, `wrangler secret put` in production. The CRM creates that account at the first sign-in while it has no accounts, and ignores the two values once it has one.
- **Users** (Settings → Users, admins only) — an admin adds people with a role (admin or user), renames them, changes their role, sets a new password, disables or deletes them. A new password, disabling or deleting signs that person out and deletes their API tokens. The CRM always keeps at least one active admin, and nobody can disable, demote or delete themselves.
- **Your password** (Settings → Account) — 8 to 72 characters. Changing it signs out your other sessions; your API tokens keep working.
- **API tokens** (Settings → API tokens) — each person makes their own, for agents and scripts. A token (`crm_…`) is shown once; send it as `Authorization: Bearer crm_…`. It acts as you on the CRM's data, but can't manage tokens, accounts or passwords: those answer 403 to a token. Revoke one at any time.
- **Sign-in attempts** are limited: 10 a minute from one address, and 10 failed attempts in 15 minutes for one email.

```bash
curl -H "Authorization: Bearer crm_…" https://<your-crm>/api/stats
```

## Tests

```bash
pnpm test        # the Go API's tests (cd server && go test ./...)
pnpm typecheck   # the UI and the Worker
scripts/parity/run.sh
```

The API began as a TypeScript (Hono) backend, and the parity harness holds the Go one to it: `scripts/parity/run.sh` checks that backend out at commit `0068887` into `.parity/ts-ref` (installing its own dependencies the first time), seeds two identical local databases, runs it and the Go server side by side on ports 8788 and 8790, sends both the same calls, and prints the mismatches — expected: 0. Name scenarios (`scripts/parity/run.sh deals customers`) to run only those.

## Deploy

Cloudflare Containers need the **Workers Paid** plan, and Docker must be running on the machine you deploy from: `wrangler deploy` builds the API's image and pushes it.

```bash
pnpm wrangler login
pnpm wrangler d1 create open-crm-db    # put the database_id it prints in wrangler.toml
pnpm db:migrate:remote                 # apply server/schema.sql to that database
pnpm wrangler secret put ADMIN_EMAIL
pnpm wrangler secret put ADMIN_PASSWORD
pnpm run deploy                        # vite build && wrangler deploy
```

Use `pnpm run deploy`: a bare `pnpm deploy` is pnpm's own command. Then open the URL wrangler prints and sign in as the admin; change the password in Settings → Account. After 10 minutes without a request the container goes to sleep; the next request starts it again and waits while it starts. Your data stays in D1 meanwhile.

## Tech Stack

| Layer | Technology |
|-------|-----------|
| **Frontend** | React 19, Tailwind v4, shadcn/ui, TypeScript, Vite |
| **API** | Go (the standard library, and bcrypt for passwords) in a Cloudflare Container |
| **Worker** | Serves the built UI, sends `/api/*` and `/llms.txt` to the container, and bridges D1 to it |
| **Database** | Cloudflare D1 (SQLite) |
| **Config** | One `wrangler.toml`: the Worker, the container and the D1 binding |
| **Icons** | Lucide |
| **Favicons** | Google's favicon service |

## Architecture

```
browser ──► Worker (src/worker) ──► the built UI (dist/)
                 │
                 └── /api/*, /llms.txt ──► container: the Go API (server/)
                                                │
                 Worker answers with env.DB ◄───┘  http://d1.internal
```

A container can't see Worker bindings, so the Go server reaches D1 over plain HTTP at `http://d1.internal`, a host that exists only in the container's network, and the Worker answers it with its `DB` binding. The Worker also passes the container `ADMIN_EMAIL` and `ADMIN_PASSWORD`, and the visitor's address for the sign-in limit.

```
src/
  worker/index.ts     — the Worker: the container class, the D1 bridge, the request forwarding
  client/             — the React app (Vite)
server/
  main.go             — the API server in the container (port 8080)
  schema.sql          — the D1 schema (pnpm db:migrate, pnpm db:migrate:remote)
  Dockerfile          — the container image: one static binary on distroless
  cmd/devserver/      — the API on a local SQLite file, without a container (the parity harness runs it)
  internal/
    crm/              — the routes and the rules (next steps, customer health, lookup)
    auth/             — sign-in, sessions, API tokens, the sign-in limit
    db/               — queries over the D1 bridge, or SQLite for tests and the dev server
    web/              — the router, JSON in and out, /llms.txt and /api/openapi.json
    js/               — the JavaScript number and date rules the API keeps from its TypeScript origin
scripts/parity/       — the parity harness
wrangler.toml         — the Worker, the container, D1
```

### Data Model

Three entities with foreign key relationships:

```mermaid
erDiagram
    companies ||--o{ contacts : "has"
    contacts  ||--o{ deals    : "has"
    companies ||--o{ deals    : "has"

    companies {
        text id PK
        text name
        text domain
        text industry
        text phone
        text email
        text notes
        text created_at
        text updated_at
    }
    contacts {
        text id PK
        text first_name
        text last_name
        text email
        text phone
        text company_id FK "→ companies · ON DELETE SET NULL"
        text title
        text status
        text created_at
        text updated_at
    }
    deals {
        text id PK
        text name
        text contact_id FK "→ contacts · ON DELETE SET NULL"
        text company_id FK "→ companies · ON DELETE SET NULL"
        real value
        text stage
        text close_date
        text notes
        text created_at
        text updated_at
    }
```

```sql
companies (id, name, domain, industry, phone, email, notes, customer_since, renewal_date)
contacts  (id, first_name, last_name, email, phone, company_id → companies, title, status)
deals     (id, name, contact_id → contacts, company_id → companies, value, stage, close_date, notes)
```

Contacts belong to companies. A deal has its own company and its own contact, both optional: a deal can name a company before it has a person. Setting a contact on a deal with no company gives the deal that contact's company. Deleting a company sets `company_id` to NULL on its contacts and deals. Deleting a contact sets `contact_id` to NULL on its deals.

Tasks (`tasks`: `owed_by` us or them, each with an optional company, contact and deal) are typed by hand or added as a deal update's next step. A company with `customer_since` set is a customer: the close date of its first won deal, unless someone set it by hand.

Custom attributes are real columns, registered in `custom_field_defs`. A relation is two defs, one per side, pointing at each other (`inverse_def_id`). The single side (`many_to_one`) is an indexed column holding the linked record's id, its key ending in `_id`; the many side (`one_to_many`) has no column and is read back from it. Relation columns carry no foreign key, since SQLite can't drop a column that has one; the API clears links when a record is deleted.

Accounts are `users` (role `admin` or `user`, a bcrypt password hash), with their `sessions` and `api_tokens`; of a session or a token only the SHA-256 of its secret is stored. The schema also keeps the tables of the email sync, meetings and AI columns this CRM used to have: nothing writes them any more, and existing data in them stays.

### API Endpoints

Every endpoint below needs a signed-in person or `Authorization: Bearer crm_…`, except `POST /api/auth/login` and `GET /api/openapi.json`. `/llms.txt` lists them for agents.

Reads carry each relation under `relations[key]`: the linked record `{ id, label, domain }` (or null), or for a many side `{ items, total }`. Write the single side by id (`PUT /api/contacts/:id { "partner_company_id": "<company id>" }`, null to clear); the many side is written from the records it lists.

List endpoints take `filters`: a JSON list, ANDed, of rules `{field, op, value}` and groups `{logic: "and"|"or", rules: [...]}` (groups nest one level). A relation's many side (a custom one_to_many key, or `contacts` on companies, `deals` on contacts) takes `is` / `is_not` (linked record ids), `is_empty`, `is_not_empty`; `count:<key>` (e.g. `count:contacts`) compares how many it links with `is` / `is_not` / `gt` / `gte` / `lt` / `lte` (a number) or `is_empty` (none), and is also a `sort`. Operators: `contains`, `does_not_contain`, `is` / `is_not` (a value or a list, case-insensitive), `is_empty`, `is_not_empty`, `gt` / `gte` / `lt` / `lte`, and for dates `on`, `before`, `after` (on or after), `today`, `in_past`, `in_future`, `relative` (`PAST_7_DAY`, `NEXT_2_WEEK`, `THIS_1_MONTH`). Pass `tz` (minutes east of UTC) to put date rules on the viewer's local day.

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/stats` | Aggregate counts and total deal value |
| GET | `/api/widgets` | A short summary of the deals: created this month, open pipeline value and by stage, per week, the latest |
| GET | `/api/contacts` | List contacts (paginated, sortable, searchable) |
| GET | `/api/contacts/:id` | Get a single contact |
| POST | `/api/contacts` | Create a contact |
| PUT | `/api/contacts/:id` | Update a contact |
| DELETE | `/api/contacts/:id` | Delete a contact |
| POST | `/api/contacts/bulk-delete` | Delete several contacts (`{ ids }`) |
| POST | `/api/contacts/import` | Import contacts (`{ contacts: [...] }`); company names resolve to existing companies or are created |
| GET | `/api/companies` | List companies (paginated, sortable, searchable) |
| GET | `/api/companies/:id` | Get a single company |
| POST | `/api/companies` | Create a company |
| PUT | `/api/companies/:id` | Update a company |
| DELETE | `/api/companies/:id` | Delete a company |
| POST | `/api/companies/bulk-delete` | Delete several companies (`{ ids }`) |
| POST | `/api/companies/import` | Import companies (`{ companies: [...] }`); a name that already exists is skipped |
| GET | `/api/deals` | List deals (paginated, sortable, searchable) |
| GET | `/api/deals/board` | Every deal for the pipeline board, optionally filtered |
| GET | `/api/deals/progress` | Open deals worst first: each one's next step and the reasons to look at it |
| GET | `/api/deals/:id` | Get a single deal |
| POST | `/api/deals` | Create a deal |
| PUT | `/api/deals/:id` | Update a deal |
| DELETE | `/api/deals/:id` | Delete a deal |
| POST | `/api/deals/:id/updates` | Log what happened on a deal, the tasks it finished and its next step |
| GET / POST | `/api/stages` | The pipeline stages in order; add one |
| PUT / DELETE | `/api/stages/:key` | Change a stage's label, color, position or won/lost flags; delete one (`?reassign_to=` moves its deals) |
| GET / POST | `/api/tasks` | Tasks (`company_id`, `deal_id`, `status=open\|done\|all`, `owed_by=us\|them`); create one |
| PUT / DELETE | `/api/tasks/:id` | Update a task, `{ done: true }` to complete it; delete it |
| GET / POST | `/api/activities` | A record's timeline (`entity_type`, `entity_id`), newest first; log a note |
| GET | `/api/views?entity=` | A list's named views (its default "All …" view is created on first use) |
| POST | `/api/views` | Create a view (`{ entity, name, from?, filters?, sort?, order? }`; `from` copies that view's columns) |
| PATCH | `/api/views/:id` | Rename a view or update its filters and sort |
| DELETE | `/api/views/:id` | Delete a view (not the default) |
| GET | `/api/views/:id/fields` | A view's column layout (visibility, widths, footer calculations) |
| PUT | `/api/views/:id/fields/:key` | Show/hide, resize or set the footer calculation of one column (`{ visible?, size?, aggregate? }`) |
| GET | `/api/custom-fields` | The attributes (`entity=contact\|company\|deal` for one record type's) |
| POST | `/api/custom-fields` | Add an attribute. A relation: `{ entity_type, key, label, field_type: "relation", relation_type: "many_to_one" \| "one_to_many", target_entity, inverse_key, inverse_label }` makes both sides |
| PUT | `/api/custom-fields/:id` | Change an attribute's label, options, widget or position (its key and type stay) |
| DELETE | `/api/custom-fields/:id` | Delete an attribute (a relation goes from both sides, with its links) |
| GET | `/api/records?entity=&search=` | Records by name for a relation picker (or `ids=a,b` to name given ids) |
| GET | `/api/values?entity=&field=` | The values a column already holds (case-insensitive, up to 200), for a picker that offers them |
| GET | `/api/contacts/aggregates`, `/api/companies/aggregates` | Column totals over the filtered list (`ops=[{key, op}]` plus the list's `search`/`filters`) |
| GET | `/api/customers` | Every customer worst first with its reasons, and what to do first |
| GET | `/api/lookup` | What the CRM knows about an address (`email`, `domain`): contact, company, customer since, deals |
| POST | `/api/lookup` | The same for up to 100 addresses at once (`{ addresses: [{ email, domain }] }`), in order |
| POST | `/api/auth/login` | Sign in with `{ email, password }`: the account, and a session cookie |
| POST | `/api/auth/logout` | Sign out |
| GET | `/api/auth/me` | The signed-in account |
| PUT | `/api/auth/password` | Change your password (`{ current_password, new_password }`; browser session only) |
| GET / POST | `/api/users` | The accounts; add one (`{ email, name?, password, role? }`; admins, browser session only) |
| PATCH / DELETE | `/api/users/:id` | Change an account's name, role or password, or disable it; delete it (admins, browser session only) |
| GET / POST | `/api/tokens` | Your API tokens; make one (`{ name }`; its secret is in this answer only; browser session only) |
| DELETE | `/api/tokens/:id` | Delete one of your API tokens (browser session only) |
| GET | `/api/openapi.json` | The OpenAPI document |

## Community & Contributions

OpenCRM began in the [OpenClaw](https://github.com/openclaw/openclaw) ecosystem. Contributions are welcome — open an issue or submit a PR.

## License

MIT
