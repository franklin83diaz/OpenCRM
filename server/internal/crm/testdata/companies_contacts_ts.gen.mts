// Generates companies_contacts_ts.json: scenarios (seed, steps) and what the
// TS routes at 0068887 answered — GET /api/stats, GET /api/widgets and the
// companies and contacts routes (list, aggregates, create, update, delete,
// bulk-delete) — with every SQL statement and its params.
// companies_contacts_ts_test.go replays each scenario through the Go server
// and wants the same.
//
// These are what the parity scenario (scripts/parity/scenarios/
// companies-contacts.mjs) can't reach: custom fields (no ported route creates
// them yet), relations, database errors (whose text is D1's there and
// SQLite's here), widgets that fail their schema, and the statements a bulk
// delete of 150 ids runs.
//
// Run from the repository root (needs node 22 and the repo's node_modules):
//
//	TZ=UTC npx tsx server/internal/crm/testdata/companies_contacts_ts.gen.mts [ts-src-dir] > server/internal/crm/testdata/companies_contacts_ts.json
//
// ts-src-dir defaults to src/server, else the parity harness's reference
// checkout (.parity/ts-ref/src/server), which stays at 0068887 after the TS
// backend is deleted. The whole Hono app (index.ts) answers each request
// through app.request, with @clawnify/db over node:sqlite (foreign keys on,
// as in D1); custom fields are created with custom-fields.ts directly. Each
// scenario imports index.ts afresh, so its per-isolate flags start cold as a
// new Go App's do. UUIDs come in creation order.

process.env.TZ = "UTC";

import { DatabaseSync } from "node:sqlite";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";

const src = path.resolve(process.argv[2] ?? (existsSync("src/server/index.ts") ? "src/server" : ".parity/ts-ref/src/server"));
const schema = readFileSync("server/schema.sql", "utf8"); // the schema the Go tests use

let uuid = 0;
Object.defineProperty(globalThis.crypto, "randomUUID", {
  value: () => `00000000-0000-4000-8000-${String(++uuid).padStart(12, "0")}`,
  configurable: true,
});

// ── The database ───────────────────────────────────────────────────

let sqlite: DatabaseSync;
let statements: Array<{ sql: string; params: unknown[] }> = [];
const d1 = {
  prepare(sql: string) {
    let params: unknown[] = [];
    const stmt = {
      bind(...p: unknown[]) {
        params = p;
        return stmt;
      },
      async all() {
        statements.push({ sql, params });
        return { results: sqlite.prepare(sql).all(...(params as never[])) };
      },
      async first() {
        statements.push({ sql, params });
        return sqlite.prepare(sql).get(...(params as never[])) ?? null;
      },
      async run() {
        statements.push({ sql, params });
        const r = sqlite.prepare(sql).run(...(params as never[]));
        return { meta: { changes: Number(r.changes), last_row_id: Number(r.lastInsertRowid) } };
      },
    };
    return stmt;
  },
};

const { initDB } = await import(path.join(src, "db.ts"));
const fields = await import(path.join(src, "custom-fields.ts"));
initDB({ DB: d1 }); // the app's middleware does this per request; the custom-field steps come first

// ── Scenarios ──────────────────────────────────────────────────────

type Step =
  // dbError: the answer is a database error, whose text differs between
  // node:sqlite and SQLite in Go: only the status is compared. days: dates
  // (the widgets' weeks) depend on the day it runs. raw: compare the bytes.
  | { op: "request"; method: string; path: string; body?: unknown; headers?: Record<string, string>; dbError?: boolean; days?: boolean; raw?: boolean }
  | { op: "field"; input: Record<string, unknown> }
  | { op: "relation"; input: Record<string, unknown> }
  | { op: "sql"; sql: string };

interface Scenario {
  name: string;
  seed: string[];
  steps: Step[];
}

const seed = [
  `INSERT INTO companies (id, name, domain, industry, customer_since, created_at, updated_at) VALUES
    ('co-acme', 'Acme', 'acme.example', 'Software', '2025-01-01', '2026-01-01 00:00:00', '2026-01-01 00:00:00'),
    ('co-beta', 'Beta', 'https://www.beta.example/', 'Retail', NULL, '2026-01-02 00:00:00', '2026-01-02 00:00:00'),
    ('co-cedar', 'Cedar & Co', '', 'Consulting', NULL, '2026-01-03 00:00:00', '2026-01-03 00:00:00')`,
  `INSERT INTO contacts (id, first_name, last_name, email, company_id, title, status, created_at, updated_at) VALUES
    ('ct-ann', 'Ann', 'Lee', 'ann@acme.example', 'co-acme', 'CEO', 'customer', '2026-01-01 00:00:00', '2026-01-01 00:00:00'),
    ('ct-bo', 'Bo', NULL, 'bo@beta.example', 'co-beta', 'Buyer', 'lead', '2026-01-02 00:00:00', '2026-01-02 00:00:00'),
    ('ct-cy', 'Cy', 'Zed', 'cy@gmail.com', NULL, '', 'lead', '2026-01-03 00:00:00', '2026-01-03 00:00:00')`,
  `INSERT INTO stages (key, label, color, position, is_won, is_lost) VALUES
    ('prospect', 'Prospect', 'slate', 0, 0, 0), ('qualified', 'Qualified', 'sky', 1, 0, 0),
    ('won', 'Won', 'emerald', 4, 1, 0), ('lost', 'Lost', 'rose', 5, 0, 1)`,
  `INSERT INTO deals (id, name, contact_id, company_id, value, stage, close_date, created_at, updated_at) VALUES
    ('d-1', 'Acme renewal', 'ct-ann', 'co-acme', 1200, 'qualified', '2026-02-01', '2020-01-06 00:00:00', '2020-01-06 00:00:00'),
    ('d-2', 'Beta pilot', 'ct-bo', 'co-beta', 300.5, 'prospect', '', '2020-01-07 00:00:00', '2020-01-07 00:00:00'),
    ('d-3', 'Lost one', NULL, 'co-cedar', 999, 'lost', '', '2020-01-08 00:00:00', '2020-01-08 00:00:00'),
    ('d-4', 'Won one', NULL, 'co-acme', 50, 'won', '2025-01-01', '2020-01-09 00:00:00', '2020-01-09 00:00:00')`,
];

const req = (method: string, p: string, body?: unknown, extra: Partial<Step> = {}): Step => ({ op: "request", method, path: p, ...(body === undefined ? {} : { body }), ...extra } as Step);
const get = (p: string, extra: Partial<Step> = {}) => req("GET", p, undefined, extra);
const qs = (o: Record<string, unknown>) =>
  "?" + Object.entries(o).map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(typeof v === "string" ? v : JSON.stringify(v))}`).join("&");

const customFields: Step[] = [
  { op: "field", input: { entity_type: "company", key: "tier", label: "Tier", field_type: "enumeration", options: { enum: ["gold", "silver"], required: true } } },
  { op: "field", input: { entity_type: "company", key: "seats", label: "Seats", field_type: "integer" } },
  { op: "field", input: { entity_type: "company", key: "signed_on", label: "Signed on", field_type: "date" } },
  { op: "field", input: { entity_type: "company", key: "active", label: "Active", field_type: "boolean" } },
  { op: "field", input: { entity_type: "contact", key: "nickname", label: "", field_type: "string", options: { required: true } } },
  { op: "field", input: { entity_type: "contact", key: "score", label: "Score", field_type: "decimal" } },
  { op: "field", input: { entity_type: "contact", key: "company", label: "Company tag", field_type: "string" } },
  { op: "relation", input: { entity_type: "contact", key: "partner_id", label: "Partner", relation_type: "many_to_one", target_entity: "company", inverse_key: "partners", inverse_label: "Partners" } },
  { op: "relation", input: { entity_type: "company", key: "parent_id", label: "Parent", relation_type: "many_to_one", target_entity: "company", inverse_key: "children", inverse_label: "Children" } },
  { op: "relation", input: { entity_type: "company", key: "owner_id", label: "Owner", relation_type: "many_to_one", target_entity: "contact", inverse_key: "owned", inverse_label: "Owned" } },
];

const ghosts = (n: number) => Array.from({ length: n }, (_, i) => `ghost-${i}`);

const scenarios: Scenario[] = [
  {
    name: "custom fields and relations on writes",
    seed,
    steps: [
      ...customFields,
      // Required custom fields: absent or empty on create; null or "" on update.
      req("POST", "/api/companies", { name: "Delta" }),
      req("POST", "/api/companies", { name: "Delta", tier: "" }),
      req("POST", "/api/companies", { name: "   ", tier: null }),
      // An enum violation fails after the row is written: 500, and the row stays.
      req("POST", "/api/companies", { name: "Delta", tier: "bronze", seats: "7" }),
      get("/api/companies?search=Delta"),
      // Flat and nested custom values; the bag wins.
      req("POST", "/api/companies", { name: "Echo", tier: "gold", seats: "12.7", signed_on: "2026-03-01", active: true, custom: { seats: 3 } }),
      req("POST", "/api/companies", { name: "Fox", custom: { tier: "silver", active: 0 }, parent_id: "co-acme", owner_id: "ct-ann" }),
      // Relation writes.
      req("POST", "/api/companies", { name: "Fox2", tier: "gold", parent_id: "nope" }),
      req("POST", "/api/companies", { name: "Fox3", tier: "gold", parent_id: 5 }),
      req("POST", "/api/companies", { name: "Fox4", tier: "gold", children: ["x"] }),
      req("POST", "/api/companies", { name: "Fox5", tier: "gold", parent_id: "", owner_id: null }),
      // Unknown fields: 422 with the valid ones, custom included (not the one_to_many side).
      req("POST", "/api/companies", { name: "X", tier: "gold", bogus: 1, custom: { nope: 2, partners: 1 } }),
      req("POST", "/api/contacts", { first_name: "X", nickname: "x", partners: 1 }),
      // Update.
      req("PUT", "/api/companies/co-acme", { tier: null }),
      req("PUT", "/api/companies/co-acme", { tier: "" }),
      req("PUT", "/api/companies/co-acme", { seats: "40" }),
      req("PUT", "/api/companies/co-acme", { tier: "platinum", name: "Acme Two" }),
      req("PUT", "/api/companies/co-acme", { children: "x" }),
      req("PUT", "/api/companies/co-acme", { children: null }),
      req("PUT", "/api/companies/co-acme", { custom: { tier: "silver", active: "yes" }, signed_on: "" }),
      req("PUT", "/api/companies/co-beta", { parent_id: "co-acme", name: " Beta ", owner_id: "ct-bo" }),
      req("PUT", "/api/companies/co-beta", { parent_id: "ct-ann" }),
      req("PUT", "/api/companies/nope", { seats: 1 }),
      req("PUT", "/api/companies/nope", { tier: null }),
      // Contacts: label-less required field named by its key; a custom field
      // named like a footer column ("company").
      req("POST", "/api/contacts", { first_name: "Gus" }),
      req("POST", "/api/contacts", { first_name: "Gus", nickname: "G", partner_id: "co-cedar", score: "4.5", company: "Big", email: "gus@ACME.example" }),
      req("POST", "/api/contacts", { first_name: "Hal", nickname: "H", partner_id: "co-nope" }),
      req("POST", "/api/contacts", { first_name: "Ida", nickname: "I", email: "ida@beta.example", company_id: null }),
      req("PUT", "/api/contacts/ct-ann", { partner_id: "co-beta", nickname: "Annie" }),
      req("PUT", "/api/contacts/ct-bo", { partner_id: "co-beta", score: "x" }),
      req("PUT", "/api/contacts/ct-cy", { partner_id: "co-acme", score: 9.25, nickname: "C" }),
      req("PUT", "/api/contacts/ct-cy", { nickname: null }),
      req("PUT", "/api/contacts/ct-cy", { owned: [1] }),
      req("PUT", "/api/contacts/ct-cy", { partner_id: "" }),
      req("PUT", "/api/contacts/ct-cy", { partner_id: "co-acme" }),
      req("PUT", "/api/contacts/nope", { partner_id: "co-acme" }),
    ],
  },
  {
    name: "lists, sorts, filters and aggregates with custom fields",
    seed: [
      ...seed,
      `INSERT INTO companies (id, name, domain, created_at, updated_at) VALUES
        ('co-dune', 'dune', 'dune.example', '2026-01-04 00:00:00', '2026-01-04 00:00:00'),
        ('co-echo', 'Écho', NULL, '2026-01-05 00:00:00', '2026-01-05 00:00:00')`,
    ],
    steps: [
      ...customFields,
      { op: "sql", sql: "UPDATE companies SET tier = 'gold', seats = 10, signed_on = '2026-02-01', active = 1, parent_id = NULL WHERE id = 'co-acme'" },
      { op: "sql", sql: "UPDATE companies SET tier = 'silver', seats = 25, signed_on = '2025-12-01', active = 0, parent_id = 'co-acme', owner_id = 'ct-bo' WHERE id = 'co-beta'" },
      { op: "sql", sql: "UPDATE companies SET tier = 'gold', seats = NULL, signed_on = '', parent_id = 'co-acme', owner_id = 'ct-ann' WHERE id = 'co-cedar'" },
      { op: "sql", sql: "UPDATE companies SET parent_id = 'co-beta' WHERE id = 'co-dune'" },
      { op: "sql", sql: "UPDATE contacts SET partner_id = 'co-beta', score = 2.5, nickname = 'a', company = 'x' WHERE id = 'ct-ann'" },
      { op: "sql", sql: "UPDATE contacts SET partner_id = 'co-acme', score = 7, nickname = 'b' WHERE id = 'ct-bo'" },
      { op: "sql", sql: "UPDATE contacts SET partner_id = 'co-beta', score = NULL WHERE id = 'ct-cy'" },
      get("/api/companies"),
      get("/api/companies?sort=name&order=asc"),
      get("/api/companies?sort=parent_id&order=asc"),
      get("/api/companies?sort=parent_id&order=desc"),
      get("/api/companies?sort=owner_id&order=asc"),
      get("/api/companies?sort=count:children&order=desc"),
      get("/api/companies?sort=count:partners&order=desc"),
      get("/api/companies?sort=count:contacts&order=asc"),
      get("/api/companies?sort=count:owned"),
      get("/api/companies?sort=seats&order=desc"),
      get("/api/companies?sort=children&order=asc"),
      get("/api/companies?sort=tier&order=asc&limit=2&page=2"),
      get("/api/contacts"),
      get("/api/contacts?sort=partner_id&order=asc"),
      get("/api/contacts?sort=company_id&order=desc"),
      get("/api/contacts?sort=count:owned&order=desc"),
      get("/api/contacts?sort=count:deals&order=desc"),
      get("/api/contacts?sort=score&order=asc"),
      ...[
        [{ field: "tier", op: "is", value: "gold" }],
        [{ field: "tier", op: "is", value: ["gold", "silver"] }],
        [{ field: "seats", op: "gt", value: 10 }],
        [{ field: "seats", op: "lte", value: "10" }],
        [{ field: "active", op: "is", value: true }],
        [{ field: "signed_on", op: "before", value: "2026-01-01" }],
        [{ field: "signed_on", op: "is_empty" }],
        [{ field: "partners", op: "is_not_empty" }],
        [{ field: "partners", op: "is", value: ["ct-ann", "ct-cy"] }],
        [{ field: "children", op: "is", value: ["co-beta"] }],
        [{ field: "children", op: "is_empty" }],
        [{ field: "parent_id", op: "is", value: "co-acme" }],
        [{ field: "owner_id", op: "is_not_empty" }],
        [{ field: "contacts", op: "is_not_empty" }],
        [{ logic: "or", rules: [{ field: "tier", op: "is", value: "silver" }, { field: "seats", op: "gte", value: 10 }] }],
      ].map((f) => get("/api/companies" + qs({ filters: f, sort: "name", order: "asc" }))),
      ...[
        [{ field: "partner_id", op: "is", value: "co-beta" }],
        [{ field: "score", op: "gte", value: 2.5 }],
        [{ field: "owned", op: "is_not_empty" }],
        [{ field: "company", op: "contains", value: "x" }],
        [{ field: "deals", op: "is_empty" }],
      ].map((f) => get("/api/contacts" + qs({ filters: f, sort: "first_name", order: "asc" }))),
      get("/api/companies/aggregates" + qs({
        ops: [
          { key: "seats", op: "sum" }, { key: "seats", op: "avg" }, { key: "seats", op: "min" }, { key: "seats", op: "max" },
          { key: "active", op: "count_true" }, { key: "active", op: "count_false" }, { key: "signed_on", op: "earliest" },
          { key: "signed_on", op: "latest" }, { key: "tier", op: "count_unique" }, { key: "parent_id", op: "count_not_empty" },
          { key: "children", op: "count" }, { key: "tier", op: "sum" }, { key: "contacts", op: "sum" },
        ],
      })),
      get("/api/companies/aggregates" + qs({ ops: [{ key: "seats", op: "sum" }], filters: [{ field: "tier", op: "is", value: "gold" }] })),
      get("/api/contacts/aggregates" + qs({
        ops: [
          { key: "score", op: "sum" }, { key: "score", op: "avg" }, { key: "nickname", op: "count_not_empty" },
          { key: "partner_id", op: "count_unique" }, { key: "company", op: "count_unique" }, { key: "name", op: "count" },
        ],
        search: "a",
      })),
      // Too many filter values: 400 on lists and aggregates; a null op: 500.
      get("/api/companies" + qs({ filters: [{ field: "tier", op: "is", value: ghosts(61) }] })),
      get("/api/contacts" + qs({ filters: [{ field: "owned", op: "is", value: ghosts(61) }] })),
      get("/api/companies/aggregates" + qs({ ops: [{ key: "seats", op: "sum" }], filters: [{ field: "seats", op: "is", value: ghosts(61) }] })),
      get("/api/contacts/aggregates" + qs({ ops: [{ key: "score", op: "sum" }], filters: [{ logic: "or", rules: [{ field: "score", op: "is", value: ghosts(31) }, { field: "nickname", op: "is", value: ghosts(30) }] }] })),
      get("/api/contacts/aggregates" + qs({ ops: [null] })),
      // zod on the list queries.
      get("/api/companies?order=ASC"),
      get("/api/companies?page=1&page=2&industry=a&industry=b&order=x&order=y"),
      get("/api/contacts?status=a&status=b&company_id=1&company_id=2&tz=x&tz=y"),
      // Deleting a record clears the relations that point at it.
      req("DELETE", "/api/companies/co-beta"),
      get("/api/companies?sort=name&order=asc"),
      get("/api/contacts?sort=first_name&order=asc"),
      req("DELETE", "/api/contacts/ct-ann"),
      get("/api/companies?sort=name&order=asc"),
    ],
  },
  {
    name: "bulk deletes of 150 ids run in chunks",
    seed,
    steps: [
      ...customFields,
      { op: "sql", sql: "UPDATE companies SET parent_id = 'co-acme', owner_id = 'ct-cy' WHERE id IN ('co-beta', 'co-cedar')" },
      { op: "sql", sql: "UPDATE contacts SET partner_id = 'co-cedar'" },
      req("POST", "/api/companies/bulk-delete", { ids: ["co-acme", ...ghosts(147), "co-cedar", "co-acme", "co-cedar"].concat(["ghost-a"]) }),
      get("/api/companies?sort=name&order=asc"),
      get("/api/contacts?sort=first_name&order=asc"),
      req("POST", "/api/contacts/bulk-delete", { ids: [...ghosts(89), "ct-cy", ...ghosts(200).slice(89)] }),
      get("/api/companies?sort=name&order=asc"),
      req("POST", "/api/contacts/bulk-delete", { ids: ["ct-ann", "ct-ann"] }),
      req("POST", "/api/contacts/bulk-delete", { ids: ["ct-ann"] }),
      get("/api/stats"),
    ],
  },
  {
    name: "a round trip of the seed without custom fields",
    seed,
    steps: [
      get("/api/stats"),
      get("/api/companies"),
      get("/api/contacts"),
      req("POST", "/api/companies", { name: "Cedar & Co <b>", domain: " Zoë.example ", custom: {} }),
      req("POST", "/api/companies", { name: "Second" }),
      req("POST", "/api/companies", { name: "Third" }),
      get("/api/companies"),
      get("/api/companies?sort=id&order=asc&limit=4"),
      get("/api/companies?sort=count:contacts&order=asc"),
      req("POST", "/api/contacts", { first_name: "Dee", email: "dee@zoë.example" }),
      req("POST", "/api/contacts", { first_name: "Eve", email: "eve@new-co.example" }),
      req("POST", "/api/contacts", { first_name: "Fay", email: "fay@new-co.example", status: " vip " }),
      req("POST", "/api/contacts", { first_name: "Gil", email: "gil@beta.example" }),
      get("/api/contacts"),
      get("/api/contacts?sort=company_id&order=asc"),
      get("/api/companies"),
      req("PUT", "/api/companies/00000000-0000-4000-8000-000000000001", { domain: null }),
      req("PUT", "/api/companies/00000000-0000-4000-8000-000000000001", {}),
      req("PUT", "/api/companies/00000000-0000-4000-8000-000000000001", { domain: "", customer_since: "2026-02-28", renewal_date: "2028-02-29" }),
      req("PUT", "/api/companies/00000000-0000-4000-8000-000000000001", { renewal_date: "2027-02-29" }),
      req("PUT", "/api/contacts/ct-bo", { company_id: null, last_name: " B " }),
      get("/api/companies/aggregates" + qs({ ops: [{ key: "contacts", op: "sum" }, { key: "domain", op: "count_empty" }] })),
      get("/api/contacts/aggregates" + qs({ ops: [{ key: "company", op: "count_empty" }, { key: "status", op: "count_unique" }] })),
      get("/api/stats"),
    ],
  },
  {
    name: "database errors",
    seed,
    steps: [
      get("/api/companies?page=abc", { dbError: true }),
      get("/api/contacts?limit=abc", { dbError: true }),
      get("/api/companies?page=99999999999999999999", { dbError: true }),
      req("POST", "/api/contacts", { first_name: "X", company_id: "no-such-company" }, { dbError: true }),
      req("PUT", "/api/contacts/ct-ann", { company_id: "no-such-company" }, { dbError: true }),
      get("/api/contacts?sort=first_name&order=asc"),
    ],
  },
  {
    name: "widgets",
    seed: [
      ...seed,
      `INSERT INTO stages (key, label, color, position, is_won, is_lost) VALUES
        ('long', '${"A".repeat(79)}😀 and more', 'amber', 2, 0, 0)`,
      `INSERT INTO deals (id, name, value, stage, created_at) VALUES
        ('d-5', 'Long ${"b".repeat(73)}🎉🎉', 1234567.5, 'long', '2020-02-01 00:00:00'),
        ('d-6', 'Odd', -2.5, 'long', '2020-02-02 00:00:00'),
        ('d-7', 'Half', 0.5, 'prospect', '2020-02-03 00:00:00'),
        ('d-8', 'Nothing', NULL, 'prospect', '2019-12-31 00:00:00'),
        ('d-9', 'Text', 'abc', 'prospect', '2020-02-05 00:00:00'),
        ('d-10', 'Big', 1152921504606846976, 'qualified', '2020-02-06 00:00:00')`,
    ],
    steps: [
      get("/api/widgets", { days: true, raw: true }),
      { op: "sql", sql: "UPDATE deals SET name = '' WHERE id IN ('d-6', 'd-10')" },
      { op: "sql", sql: "UPDATE deals SET value = 1e80 WHERE id = 'd-7'" },
      { op: "sql", sql: "UPDATE stages SET label = '' WHERE key = 'prospect'" },
      get("/api/widgets", { raw: true }),
      { op: "sql", sql: "UPDATE deals SET name = '' WHERE id IN ('d-5', 'd-9')" },
      get("/api/widgets", { raw: true }),
    ],
  },
];

// ── Run ────────────────────────────────────────────────────────────

const out = [];
for (const [i, sc] of scenarios.entries()) {
  uuid = 0;
  statements = [];
  sqlite = new DatabaseSync(":memory:");
  sqlite.exec("PRAGMA foreign_keys = ON");
  sqlite.exec(schema);
  for (const s of sc.seed) sqlite.exec(s);
  const app = (await import(path.join(src, "index.ts") + `?scenario=${i}`)).default;
  const results: unknown[] = [];
  for (const st of sc.steps) {
    switch (st.op) {
      case "request": {
        const headers = { ...(st.headers ?? {}) };
        let body: string | undefined;
        if (st.body !== undefined) {
          body = typeof st.body === "string" ? st.body : JSON.stringify(st.body);
          headers["Content-Type"] ??= "application/json";
        }
        const res = await app.request(st.path, { method: st.method, headers, body }, { DB: d1 });
        results.push({ status: res.status, contentType: res.headers.get("content-type"), body: await res.text() });
        break;
      }
      case "field":
      case "relation":
        try {
          results.push({ def: st.op === "field" ? await fields.createDef(st.input) : await fields.createRelation(st.input) });
        } catch (err) {
          results.push({ error: (err as Error).message });
        }
        break;
      case "sql":
        sqlite.exec(st.sql);
        results.push(null);
        break;
    }
  }
  out.push({ ...sc, expect: { results, statements } });
}
process.stdout.write(JSON.stringify(out, null, 1) + "\n");
