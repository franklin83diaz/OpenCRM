# OpenCRM — agent guide

A CRM with **companies**, **contacts**, **partners**, **customer users**,
**tickets** and a **deals** pipeline, plus tasks and an activity timeline. React + Go + D1: the API is a Go server in a Cloudflare
Container. `GET /llms.txt` lists every endpoint; `GET /api/openapi.json` is the
spec.

## Authentication

Every `/api` call needs an account, except `POST /api/auth/login` and
`GET /api/openapi.json`. Send a personal API token:

```
Authorization: Bearer crm_…
```

- A person signed in to the CRM makes the token in Settings → **API tokens**
  and gives it to you; its secret is shown only once. `GET /api/auth/me` says
  whose it is.
- A token acts as its owner on the CRM's data: what you create or log is
  recorded as theirs.
- A token can't manage tokens, accounts or passwords: `/api/tokens`,
  `/api/users` and `PUT /api/auth/password` answer 403 to it. That is a
  person's job, in the browser; don't try to work around it.
- Without a valid token every call answers 401
  `{"error":"unauthorized","message":"Sign in to use the CRM."}`. A token stops
  working when it is revoked, or when an admin disables or deletes its owner or
  sets them a new password. Ask the person for a new one.

## Core entities

- `GET/POST/PUT/DELETE /api/contacts` · `/api/companies` · `/api/partners` · `/api/customer-users` · `/api/tickets` · `/api/deals`
- Customer users are the people who use the product at a customer: `{ name,
  email, phone, company_id, notes }`, kept apart from contacts (the people we
  deal with). `GET /api/customer-users?company_id=` lists one customer's. The
  company must be a customer (it has a `customer_since`): any other is a 400.
  `GET /api/records?entity=company&customers=1&search=` finds them by name.
- A company is a customer while it has a `customer_since`. `POST
  /api/companies/{id}/remove-customer` ends that: it clears `customer_since`
  and `renewal_date` and logs a `customer_change` activity (both dates in its
  `meta`); nothing else changes. A later won deal makes it a customer again.
- Tickets are a customer's requests and problems: `{ subject, description,
  status, priority, company_id, customer_user_id, owner_id, due_date }`.
  `status`: `open` (the default), `in_progress`, `waiting` (on the customer),
  `resolved`, `closed`; the last two are done. `priority`: `low`, `medium`
  (the default), `high`, `urgent`. Both take any case, with spaces ("In
  progress"). `company_id` is required, never `null`, and must be a customer
  (400 otherwise; one that stops being a customer stays the ticket's); send
  only `customer_user_id` and the ticket takes that user's company.
  `customer_user_id` (optional) must be one of that customer's;
  moving a ticket to another customer drops one who isn't theirs. `owner_id`
  is an account (`GET /api/team`) or `null`; `due_date` is `YYYY-MM-DD` or
  `null`. Reads carry `company_name`, `customer_user_name`, `customer_user_email`
  and `owner`. `GET /api/tickets?company_id=&customer_user_id=&owner_id=&status=`
  filters; `sort=status`/`priority` follow the workflow/urgency, `sort=due_date`
  puts undated ones last. Company and customer user filters take
  `count:tickets`.
- A company may have a main contact and a main partner: the customer is the
  company, its main contact and its main partner. `POST`/`PUT`
  `{ main_contact_id }`: any contact, of this company or another (a
  building's main contact is often the property manager at the management
  firm); `{ main_partner_id }`: any partner. An id that names none is a 400;
  `null` or `""` clears it. `GET /api/companies/{id}`, and the answers to
  `POST` and `PUT`, carry `main_contact`, `main_contact_company` (the company
  that contact works at, or `null`) and `main_partner` (each `{ id, label }`
  or `null`). The companies a contact is the main contact of:
  `GET /api/companies?filters=[{"field":"main_contact_id","op":"is","value":["<id>"]}]`.
- Contacts belong to companies. A deal has its own `company_id` and `contact_id`,
  both optional. Setting a contact on a deal with no company gives it the
  contact's company; send `company_id` to set or clear it yourself.
- A deal is worth `value` once and `recurring_value` each month while they
  stay a customer.
- A deal is assigned to one account, `owner_id` (`GET /api/team` lists them;
  reads also carry `owner: { id, name, email }`), or to nobody: `null`. The
  board's free deals are the filter `owner_id is_empty`.
- Partners are the firms that work deals with us (resellers, referrers,
  integrators), kept apart from companies. Each has an `industry` (free text)
  and a `status`: `prospect` (new ones start there), `onboarding`, `active`
  or `inactive`. A deal has any number of them,
  each with its share of the deal, and one of them may be its main partner:
  deal reads carry `partners`
  (`[{ id, label, one_time_value, recurring_value, is_main }]`, the main
  partner first). `PUT /api/deals/{id}/partners/{partner_id}`
  `{ one_time_value?, recurring_value?, is_main? }` puts a partner on the deal
  and sets its share; `is_main: true` makes it the deal's main partner (the
  one before stops being it), `false` unsets it. `DELETE` takes it off. A
  write's `partner_ids` (a list of partner ids) replaces the whole list,
  keeping the shares and the main partner of the ones it keeps.
  `GET /api/deals?partner_id=` lists a partner's deals; deal filters take
  `partners` and `count:partners`.
- `GET /api/stats` — counts (`tickets`, and `openTickets`: not resolved or
  closed) + total pipeline value (excludes lost deals).

## Pipeline stages (data, not code)

Stages live in the database — `GET /api/stages` is the vocabulary. Defaults:
`prospect → qualified → proposal → negotiation → won`, plus `lost`. Deal writes
validate the stage key (400 lists valid keys).

- `POST /api/stages` `{ label, key?, color?, position?, is_won?, is_lost? }` — add a stage.
- `PUT /api/stages/{key}` — rename, recolor, reorder, or change flags. Key is immutable.
- `DELETE /api/stages/{key}?reassign_to=<key>` — reassign_to is required when
  the stage still has deals.
- **Semantics ride on flags, not names**: moving a deal to an `is_won: 1` stage
  logs the win on its timeline and makes its company a customer (from the deal's
  close date); `is_lost: 1` stages are excluded from pipeline value.

## Custom fields

Define real, typed columns at runtime: `POST /api/custom-fields`
`{ entity_type, key, label, field_type, options }` — then write values flat on
the entity or under `custom`. Unknown fields are rejected loudly (422) with the
valid-field list. Set `options.required: true` to make a field mandatory —
create/update then reject (400) when it's missing or being cleared. Bulk import
stays lenient and does not enforce required.

## Activity timeline

Every contact/company/deal (and partner, customer user, ticket) has a timeline.
Notes, deal updates, and deals won or lost write to it.

- `GET /api/activities?entity_type=contact&entity_id=<id>` — newest first.
- `POST /api/activities` `{ entity_type, entity_id, type, body }` — log a note.

## Tasks and customers

The CRM doesn't read email or calendars. What it knows about contact with an
account is what was logged: an update on one of its deals (below), or an
activity of type `call`, `message`, `email` or `meeting` on the company, its
contacts or its deals.

- `GET /api/tasks?company_id=&deal_id=&status=open|done|all&owed_by=us|them`: tasks,
  open ones by due date. `owed_by: us` is something we promised; `them` is something
  we're waiting on. `POST /api/tasks` `{ title, company_id?, contact_id?, deal_id?, due_date?, owed_by? }`,
  `PUT /api/tasks/{id}` `{ done: true }` to complete one, `DELETE /api/tasks/{id}`.
- `GET /api/customers`: every company with `customer_since` set, worst first,
  each with `status` (red/yellow/green, or `unknown` when nothing is against it
  but the CRM can't see whether anyone has been in touch) and the `reasons` in
  plain words; and `focus`, the few things to do first. Red: one of our promises
  is overdue. Yellow: waiting on them, or a renewal within 30 days or passed.
  `sight` says what the CRM can see of calls and emails: with no sync, `off`,
  and a reason says so ("Calls aren't synced, email isn't synced"). An account
  is green only when contact with it was logged in the last 21 days. Say that part too: never
  report an account as quiet, or as fine, when the CRM can't see it.
  A company becomes a customer with `PUT /api/companies/{id}` `{ customer_since: "YYYY-MM-DD" }`,
  or from the close date of its first won deal (never later than today). A date
  already set stays: if a deal was marked won by mistake, clear `customer_since`
  by hand. `{ renewal_date: "YYYY-MM-DD" }` sets the next renewal: within 30 days
  it shows as a reason and in `focus`, and once passed it asks for the next date.
- `GET /api/lookup?email=&domain=`: what the CRM knows about an address before
  anyone writes to it. The contact with exactly that address; their company (else
  the one on `domain`, else on the address's own work domain), with
  `customer_since` when it is a customer; and its and the contact's `deals` (open
  first, each with `state` open, won or lost). Check it before a cold email: a
  customer or an open deal means a person should decide whether it goes out.
  `POST /api/lookup` `{ "addresses": [{ "email", "domain"? }] }` answers up to 100
  at once, in order (`results`), to check a batch before they join a campaign.

To answer "how is <customer> doing" or "what did we promise <company>", read
these, and quote the reasons rather than paraphrasing them.

## Deals: next steps and updates

Every open deal (stage neither won nor lost) has a next step: the soonest of its
open tasks with a date, ours or theirs. A task without a date is not a next step.

- `GET /api/deals/progress`: open deals, worst first, each with `progress`:
  `status` (red/yellow/green/unknown), the `reasons` in plain words, `next_step`,
  and `days_quiet`. `GET /api/deals/{id}` and the board carry `progress` too.
  Red: one of our promises is overdue. Yellow: no next step, waiting on them, or
  the close date has passed. Green only when nothing is against it and contact
  with its company was logged in the last 14 days; otherwise `unknown`. As for customers, say
  what the CRM can't see.
- `GET /api/tasks?deal_id=`: the deal's tasks, and its company's on no deal.

When the user tells you what happened on a deal (a phone call, a WhatsApp or
text message, an email, a meeting), log it in one call:
`POST /api/deals/{id}/updates`
`{ kind: call|message|email|meeting|note, summary, at?, done_task_ids?, next_step?: { title, due_date, owed_by }, close_date? }`.

- `at` is when it happened (ISO 8601 or `YYYY-MM-DD`), never in the future: what
  will happen goes in `next_step`, with the date agreed and who owes it.
- Read the deal's open tasks first, and put the ones this update finished in
  `done_task_ids` ("they sent the contract" closes "Signed contract").
- Find the deal through its company: `GET /api/companies?search=<name>`, then
  `GET /api/deals?company_id=<id>` (`search` on deals matches only a deal's name
  and notes). Ask when two deals could match; never log an update on a guess.
- The CRM reads no inbox or calendar, so a call, an email or a meeting counts
  only once it is logged here.
- Next steps are this CRM's tasks, not your own task list. Move the stage with
  `PUT /api/deals/{id}` `{ stage }`.

## Import contacts (CSV / XLSX)

Users import via the dashboard UI (Contacts → **Import**): upload a CSV/XLSX, map
columns to fields, import. Programmatically: `POST /api/contacts/import`
`{ contacts: [{ first_name, last_name?, email?, phone?, title?, status?, company? }] }`.
Company names are resolved to existing companies or created. Rows without a first
name are skipped. Returns `{ imported, companiesCreated, skipped }`.

## Agent-mode UI

Append `?agent=true` for larger targets and always-visible action buttons.
