// Generates settings_ts.json: scenarios (seed, steps) and what the TS routes
// at 0068887 answered — the activity timeline, CSV import, custom fields and
// views — with every SQL statement and its params. settings_ts_test.go
// replays each scenario through the Go server and wants the same.
//
// These are what the parity scenario (scripts/parity/scenarios/settings.mjs)
// can't see: the SQL itself (imports chunked under D1's 100 parameters, the
// column order of imported custom fields, the key order of JSON the routes
// store), rows the API can't write (a view's bad filters, a default view from
// before the lock, a view of an unknown entity, options stored in another
// order), and database errors (whose text is D1's there and SQLite's here).
//
// Run from the repository root (needs node 22, and the TS backend's own
// node_modules: scripts/parity/run.sh installs them in .parity/ts-ref):
//
//	TZ=UTC npx tsx server/internal/crm/testdata/settings_ts.gen.mts [ts-src-dir] > server/internal/crm/testdata/settings_ts.json
//
// ts-src-dir defaults to src/server, else the parity harness's reference
// checkout (.parity/ts-ref/src/server), which stays at 0068887 after the TS
// backend is deleted. The whole Hono app (index.ts) answers each request
// through app.request, with @clawnify/db over node:sqlite (foreign keys on,
// as in D1). Each scenario imports index.ts afresh, so its per-isolate flags
// start cold as a new Go App's do. UUIDs come in creation order. The clock is
// fixed at the scenario's `now` for new Date() and Date.now() (SQLite's
// datetime('now') still reads the real time). fetch throws: nothing here
// reaches a network. node:sqlite can't bind a boolean, so no step sends one
// where a route would bind it; and node's Request.json() drops a leading byte
// order mark, which workerd keeps (a body that isn't JSON), so no step sends
// one: the parity scenario does.

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

// ── The clock ──────────────────────────────────────────────────────

const RealDate = Date;
let fixedNow = 0;
class FixedDate extends RealDate {
  constructor(...args: unknown[]) {
    if (args.length === 0) super(fixedNow);
    else super(...(args as [string]));
  }
  static now() {
    return fixedNow;
  }
}
globalThis.Date = FixedDate as DateConstructor;

globalThis.fetch = (async (input: string | URL | Request) => {
  throw new Error(`unexpected fetch ${String(input)}`);
}) as typeof fetch;

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

// ── Scenarios ──────────────────────────────────────────────────────

type Step =
  // dbError: the answer is a database error, whose text differs between
  // node:sqlite and SQLite in Go: only the status is compared. raw: compare
  // the bytes (key order included), not the parsed JSON.
  | { op: "request"; method: string; path: string; body?: unknown; headers?: Record<string, string>; dbError?: boolean; raw?: boolean }
  | { op: "sql"; sql: string };

interface Scenario {
  name: string;
  now: string;
  seed: string[];
  steps: Step[];
}

const T0 = "2026-10-14T12:00:00.000Z";

const req = (method: string, p: string, body?: unknown, extra: Partial<Step> = {}): Step => ({ op: "request", method, path: p, ...(body === undefined ? {} : { body }), ...extra } as Step);
const get = (p: string, extra: Partial<Step> = {}) => req("GET", p, undefined, extra);
const qs = (o: Record<string, unknown>) =>
  "?" + Object.entries(o).map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(typeof v === "string" ? v : JSON.stringify(v))}`).join("&");
const sql = (s: string): Step => ({ op: "sql", sql: s });
const pad = (n: number) => String(n).padStart(3, "0");

const base = [
  `INSERT INTO companies (id, name, domain, industry, created_at, updated_at) VALUES
    ('co-acme', 'Acme', 'acme.example', 'Software', '2026-01-01 00:00:00', '2026-01-01 00:00:00'),
    ('co-acme2', 'ACME', 'ACME2.example', '', '2026-01-02 00:00:00', '2026-01-02 00:00:00'),
    ('co-cedar', 'Cedar & Co', '', 'Retail', '2026-01-03 00:00:00', '2026-01-03 00:00:00'),
    ('co-zoe', 'Zoë', 'Zoe.Example', '', '2026-01-04 00:00:00', '2026-01-04 00:00:00')`,
  `INSERT INTO contacts (id, first_name, last_name, email, company_id, title, status, created_at, updated_at) VALUES
    ('ct-ann', 'Ann', 'Lee', 'ann@acme.example', 'co-acme', 'CEO', 'active', '2026-01-01 00:00:00', '2026-01-01 00:00:00')`,
  `INSERT INTO deals (id, name, contact_id, company_id, value, stage, created_at, updated_at) VALUES
    ('d-1', 'Acme renewal', 'ct-ann', 'co-acme', 1200, 'qualified', '2026-01-01 00:00:00', '2026-01-01 00:00:00')`,
  `INSERT INTO activities (id, entity_type, entity_id, type, body, meta, created_at) VALUES
    ('a-1', 'contact', 'ct-ann', 'note', 'old', '', '2026-01-01 00:00:00'),
    ('a-2', 'contact', 'ct-ann', 'email', 'same second', '{"to":["x@y.z"]}', '2026-01-02 00:00:00'),
    ('a-3', 'contact', 'ct-ann', 'note', 'same second, higher id', '{}', '2026-01-02 00:00:00'),
    ('a-4', 'deal', 'd-1', 'stage_change', 'Moved', '{"stage":"won","value":1200}', '2026-01-03 00:00:00')`,
];

const field = (body: Record<string, unknown>) => req("POST", "/api/custom-fields", body);
const customFields: Step[] = [
  field({ entity_type: "contact", key: "tier", label: "Tier", field_type: "enumeration", options: { enum: ["gold", "silver"], required: true } }),
  field({ entity_type: "contact", key: "seats", label: "Seats", field_type: "integer" }),
  field({ entity_type: "contact", key: "vip", label: "VIP", field_type: "boolean" }),
  field({ entity_type: "contact", key: "profile", label: "Profile", field_type: "json" }),
  field({ entity_type: "contact", key: "score", label: "Score", field_type: "decimal" }),
  field({ entity_type: "contact", key: "buddy_id", label: "Buddy", field_type: "relation", relation_type: "many_to_one", target_entity: "company", inverse_key: "buddies", inverse_label: "Buddies" }),
  field({ entity_type: "company", key: "region", label: "Region", field_type: "enumeration", options: { enum: ["emea", "amer"] } }),
  field({ entity_type: "company", key: "founded", label: "Founded", field_type: "date" }),
  field({ entity_type: "company", key: "meta", label: "Meta", field_type: "json" }),
];

const scenarios: Scenario[] = [
  {
    name: "activity timeline",
    now: T0,
    seed: base,
    steps: [
      get("/api/activities?entity_type=contact&entity_id=ct-ann"),
      get("/api/activities?entity_type=%20deal%20&entity_id=d-1&entity_type=x"),
      get("/api/activities?entity_type=company&entity_id="),
      req("POST", "/api/activities", { entity_type: " contact ", entity_id: " ct-ann ", type: "  ", body: " spaced " }),
      req("POST", "/api/activities", { entity_type: "deal", entity_id: "d-1", type: " call ", body: "Zoë & <b> \u2028 line" }),
      req("POST", "/api/activities", { entity_type: "company", entity_id: "nobody", body: "no record check" }),
      req("POST", "/api/activities", { entity_type: "deal", entity_id: "d-1", body: "x", type: 7 }),
      req("POST", "/api/activities", "{\"entity_type\":\"deal\",\"entity_id\":\"d-1\",\"body\":\"x\",\"body\":\"dup wins\"}"),
      get("/api/activities?entity_type=deal&entity_id=d-1"),
      // Logging never breaks the action: with the table gone, the note is still "ok".
      sql("DROP TABLE activities"),
      req("POST", "/api/activities", { entity_type: "deal", entity_id: "d-1", body: "lost" }),
      get("/api/activities?entity_type=deal&entity_id=d-1", { dbError: true }),
    ],
  },
  {
    name: "contacts import statements",
    now: T0,
    seed: base,
    steps: [
      ...customFields,
      // 110 company names (lookups of 100 + 10; inserts of 20 rows), existing
      // ones matched without case (two rows answer to "acme": the last wins),
      // custom bags in different key orders (the columns follow the first
      // row that has each key), json cells with their key order.
      req("POST", "/api/contacts/import", {
        contacts: [
          { first_name: "Ann2", company: "acme", custom: { seats: "7", tier: "gold" } },
          { first_name: "Bo", company: "zoë", custom: { profile: { z: 1, a: [{ y: 1, b: 2 }], "10": 0, "2": 1, "": 2 }, vip: "no", tier: "bronze" } },
          { first_name: "Cy", company: "Cedar & Co", custom: { score: "1e3", buddy_id: "co-acme", nope: 1, seats: "abc" } },
          { first_name: "Di", custom: { profile: [3, { b: 1, a: 2 }], seats: -2.5 } },
          { first_name: "Ed", custom: { profile: 12.5 } },
          { first_name: "Fa", custom: { profile: "{\"kept\":\"as text\"}", tier: "" } },
          ...Array.from({ length: 110 }, (_, i) => ({
            first_name: `N${pad(i)}`,
            company: `Name Co ${pad(i)}`,
            company_domain: i % 3 ? "" : `name${i}.example`,
            company_industry: i % 4 ? "" : "Industry",
            company_phone: i % 5 ? "" : `+${i}`,
          })),
          ...Array.from({ length: 5 }, (_, i) => ({ first_name: `M${i}`, company: `NAME CO ${pad(i)}`, company_industry: "late", company_phone: "late" })),
        ],
      }),
      get("/api/contacts" + qs({ search: "Ann2", sort: "first_name", order: "asc" })),
      // A number past float64's range is Infinity: a json cell writes it as null.
      req("POST", "/api/contacts/import", '{"contacts":[{"first_name":"Inf","custom":{"profile":[1e400,{"b":-0,"a":1}],"score":-1e400}}]}'),
      get("/api/contacts" + qs({ filters: [{ field: "first_name", op: "is", value: ["Bo", "Cy", "Di", "Ed", "Fa"] }], sort: "first_name", order: "asc" })),
      get("/api/companies" + qs({ search: "Name Co 00", sort: "name", order: "asc" })),
      // 105 distinct work-email domains (lookups of 100 + 5, inserts of 20):
      // existing domains matched without case, new ones named from the domain.
      req("POST", "/api/contacts/import", {
        inferCompanyFromEmail: true,
        contacts: [
          { first_name: "Dom1", email: "a@ACME.example" },
          { first_name: "Dom2", email: "b@acme2.example" },
          { first_name: "Dom3", email: "c@zoe.example" },
          { first_name: "Dom4", email: "d@www.wwwco.example" },
          { first_name: "Dom5", email: "e@gmail.com" },
          { first_name: "Dom6", email: "f@.lead.example" },
          { first_name: "Dom7", email: "g@ßeta.example" },
          { first_name: "Dom8", email: "h@nodot" },
          { first_name: "Dom9", email: "i@acme.example", company: "Explicit Co" },
          ...Array.from({ length: 100 }, (_, i) => ({ first_name: `D${pad(i)}`, email: ` p${i}@dom${pad(i)}.example ` })),
          ...Array.from({ length: 3 }, (_, i) => ({ first_name: `E${i}`, email: `q@DOM${pad(i)}.EXAMPLE` })),
        ],
      }),
      get("/api/companies" + qs({ filters: [{ field: "domain", op: "is", value: ["wwwco.example", "www.wwwco.example", ".lead.example", "ßeta.example", "dom000.example"] }], sort: "name", order: "asc" })),
      get("/api/contacts" + qs({ search: "Dom", sort: "first_name", order: "asc" })),
      // Without custom columns: 12 rows a statement.
      req("POST", "/api/contacts/import", { contacts: Array.from({ length: 25 }, (_, i) => ({ first_name: `Plain${pad(i)}`, status: ["lead", "active", "inactive", "churned", "x"][i % 5] })) }),
      // Errors after nothing was written.
      req("POST", "/api/contacts/import", { contacts: [{ first_name: "A" }, { first_name: "B", title: 5 }] }),
    ],
  },
  {
    name: "companies import statements",
    now: T0,
    seed: base,
    steps: [
      ...customFields,
      // 150 names (lookups of 100 + 50), 10 custom columns' worth of rows
      // (100 / 10 = 10 rows a statement), duplicates in the file and in the
      // database.
      req("POST", "/api/companies/import", {
        companies: [
          { name: "acme", domain: "x" },
          { name: " New One ", domain: " one.example ", industry: " I ", phone: " P ", email: " e@one.example ", notes: " N ", custom: { meta: { z: [1, { b: 1, a: 2 }], a: null, "3": 3 }, founded: "2020-01-01", region: "emea" } },
          { name: "NEW ONE", domain: 5 },
          { name: "Second", custom: { region: "apac", founded: 2020, nope: 1 } },
          { name: "Third", custom: { meta: "text", region: null } },
          ...Array.from({ length: 150 }, (_, i) => ({ name: `Bulk ${pad(i)}`, ...(i % 9 ? {} : { custom: { region: "amer", meta: { n: i, a: true } } }) })),
          { name: "" },
          7,
        ],
      }),
      get("/api/companies" + qs({ search: "o", sort: "name", order: "asc", limit: "20" })),
      req("POST", "/api/companies/import", { companies: Array.from({ length: 30 }, (_, i) => ({ name: `Plain ${pad(i)}` })) }),
      req("POST", "/api/companies/import", { companies: [{ name: "x", industry: [1] }] }),
    ],
  },
  {
    name: "custom fields",
    now: T0,
    seed: [
      ...base,
      // Options stored by an older version, in an order of their own; and one
      // whose options aren't an object.
      `INSERT INTO custom_field_defs (id, entity_type, key, label, field_type, options, position, created_at, updated_at) VALUES
        ('def-old', 'deal', 'old', 'Old', 'string', '{"z":1,"a":{"y":2,"b":3},"10":4,"2":5}', 3, '2026-01-01 00:00:00', '2026-01-01 00:00:00'),
        ('def-bad', 'deal', 'bad', 'Bad', 'string', 'not json', 1.5, '2026-01-01 00:00:00', '2026-01-01 00:00:00'),
        ('def-arr', 'deal', 'arr', 'Arr', 'string', '[{"b":1,"a":2}]', 2, '2026-01-01 00:00:00', '2026-01-01 00:00:00')`,
      `ALTER TABLE deals ADD COLUMN "old" TEXT`,
      `ALTER TABLE deals ADD COLUMN "bad" TEXT`,
      `ALTER TABLE deals ADD COLUMN "arr" TEXT`,
    ],
    steps: [
      get("/api/custom-fields"),
      get("/api/custom-fields?entity=deal"),
      req("POST", "/api/custom-fields", { entity_type: "contact", key: "tier", label: "Tier", field_type: "enumeration", options: { z: 1, enum: ["b", "a"], colors: { b: "x", a: "y" }, "7": 1 }, custom_field: "badge", position: 2 }),
      req("POST", "/api/custom-fields", { entity_type: "contact", key: "plain", label: ["a", ["b"]], options: "text", position: 1e21 }),
      req("POST", "/api/custom-fields", { entity_type: "company", key: "partner_id", label: "Partner", field_type: "relation", relation_type: "many_to_one", target_entity: "company", inverse_key: "partners", inverse_label: "Partners", position: 9 }),
      req("PUT", "/api/custom-fields/def-old", { label: "Old renamed" }),
      req("PUT", "/api/custom-fields/def-old", { options: null, position: null }),
      req("PUT", "/api/custom-fields/def-bad", { custom_field: "w" }),
      req("PUT", "/api/custom-fields/def-arr", "[]"),
      req("PUT", "/api/custom-fields/def-arr", { options: { y: [{ d: 1, c: 2 }], b: 1 } }),
      req("PUT", "/api/custom-fields/def-old", "null"),
      req("PUT", "/api/custom-fields/nope", "null"),
      req("PUT", "/api/custom-fields/nope", "{"),
      get("/api/custom-fields?entity=deal"),
      req("DELETE", "/api/custom-fields/def-bad"),
      req("DELETE", "/api/custom-fields/00000000-0000-4000-8000-000000000003"),
      get("/api/custom-fields"),
      // Database errors: the writes answer 400 with D1's words, the reads
      // and the delete Hono's plain 500.
      sql("DROP TABLE custom_field_defs"),
      req("POST", "/api/custom-fields", { entity_type: "deal", key: "x", label: "X" }, { dbError: true }),
      req("PUT", "/api/custom-fields/def-old", { label: "x" }, { dbError: true }),
      get("/api/custom-fields"),
      req("DELETE", "/api/custom-fields/def-old"),
    ],
  },
  {
    name: "views",
    now: T0,
    seed: [
      ...base,
      // Rows the API can't write: a default view from before the lock (with
      // filters and a sort), bad filters, an upper-case order, a view of an
      // entity that isn't one.
      `INSERT INTO views (id, entity_type, name, icon, is_default, position, filters, sort, sort_order, created_at, updated_at) VALUES
        ('v-all', 'contact', 'Everyone', 'table', 1, 0, '[{"field":"status","op":"is","value":"lead"}]', 'email', 'asc', '2026-01-01 00:00:00', '2026-01-01 00:00:00'),
        ('v-bad', 'contact', 'Bad filters', 'star', 0, 2.5, 'not json', 'name', 'ASC', '2026-01-02 00:00:00', '2026-01-02 00:00:00'),
        ('v-obj', 'contact', 'Object filters', 'table', 0, 1, '{"a":1}', NULL, 'desc', '2026-01-03 00:00:00', '2026-01-03 00:00:00'),
        ('v-empty', 'contact', 'Empty filters', 'table', 0, 3, '', NULL, NULL, '2026-01-04 00:00:00', '2026-01-04 00:00:00'),
        ('v-order', 'contact', 'Ordered', 'table', 0, 4, '[{"b":1,"a":2,"10":3,"2":4},1e400,-0]', NULL, NULL, '2026-01-05 00:00:00', '2026-01-05 00:00:00'),
        ('v-task', 'task', 'Tasks', 'table', 0, 0, '[]', NULL, NULL, '2026-01-06 00:00:00', '2026-01-06 00:00:00')`,
      `INSERT INTO view_fields (view_id, field_key, is_visible, size, aggregate, updated_at) VALUES
        ('v-all', 'email', 0, 120, 'count', '2026-01-01 00:00:00'),
        ('v-all', 'name', 1, NULL, NULL, '2026-01-01 00:00:00'),
        ('v-bad', 'title', 1, 300, 'count_unique', '2026-01-01 00:00:00')`,
    ],
    steps: [
      get("/api/views?entity=contact", { raw: true }),
      get("/api/views?entity=company"),
      get("/api/views/v-all/fields", { raw: true }),
      // Filters are stored as JSON.stringify wrote them: the request's key order.
      req("POST", "/api/views", { entity: "contact", name: "Ordered", filters: [{ value: ["a", "b"], op: "is", field: "status" }, { rules: [{ op: "contains", value: "x", field: "email" }], logic: "or", "2": 1, "1": 0 }, "x\u2028\u0007", 1e21, -0], sort: "count:deals", order: "desc", from: "v-all" }, { raw: true }),
      req("POST", "/api/views", { entity: "contact", name: "From another list", from: "v-all", filters: null }),
      req("POST", "/api/views", { entity: "company", name: "From a contact view", from: "v-all" }),
      req("POST", "/api/views", { entity: "deal", name: "Deals", filters: [{ field: "value", op: "gt", value: 1 }] }),
      // 20 000 characters of JSON pass, 20 001 don't (UTF-16 units: an emoji is two).
      req("POST", "/api/views", { entity: "contact", name: "Big", filters: [{ field: "email", op: "contains", value: "y".repeat(19952) + "🎉" }] }),
      req("POST", "/api/views", { entity: "contact", name: "Too big", filters: [{ field: "email", op: "contains", value: "y".repeat(19953) + "🎉" }] }),
      // 20 relative dates are 60 parameters; 21 are too many.
      req("POST", "/api/views", { entity: "contact", name: "Sixty", filters: Array.from({ length: 20 }, () => ({ field: "created_at", op: "relative", value: "PAST_7_DAY" })) }),
      req("POST", "/api/views", { entity: "contact", name: "Nope", filters: Array.from({ length: 21 }, () => ({ field: "created_at", op: "relative", value: "PAST_7_DAY" })) }),
      // PATCH: the filters of a view of an unknown entity compile against no
      // columns (ENTITY_TABLES has no table for it).
      req("PATCH", "/api/views/v-task", { filters: [{ field: "x", op: "is", value: "y" }], sort: "x", order: "asc" }),
      req("PATCH", "/api/views/v-bad", { filters: [{ op: "is_empty", field: "title" }], name: " Fixed " }),
      req("PATCH", "/api/views/v-obj", "[]"),
      req("PATCH", "/api/views/v-empty", {}),
      req("PATCH", "/api/views/v-all", { name: "All contacts" }),
      req("PATCH", "/api/views/v-all", { order: null }),
      req("PATCH", "/api/views/v-all", "null"),
      // Columns.
      req("PUT", "/api/views/v-all/fields/email", { visible: true }),
      req("PUT", "/api/views/v-all/fields/name", { size: 2000, aggregate: "earliest" }),
      req("PUT", "/api/views/v-all/fields/phone", "not json"),
      req("PUT", "/api/views/v-bad/fields/title", { aggregate: null }),
      get("/api/views/v-all/fields", { raw: true }),
      get("/api/views?entity=contact", { raw: true }),
      get("/api/views?entity=deal"),
      req("DELETE", "/api/views/v-bad"),
      req("DELETE", "/api/views/v-all"),
      get("/api/views/v-bad/fields"),
      // Database errors: no try in these routes, so Hono's plain 500.
      sql("DROP TABLE view_fields"),
      get("/api/views/v-all/fields"),
      req("PUT", "/api/views/v-all/fields/email", {}),
      req("DELETE", "/api/views/v-obj"),
      sql("DROP TABLE views"),
      get("/api/views?entity=contact"),
      req("POST", "/api/views", { entity: "contact", name: "x" }),
      req("PATCH", "/api/views/v-obj", {}),
    ],
  },
];

// ── Run ────────────────────────────────────────────────────────────

const out = [];
for (const [i, sc] of scenarios.entries()) {
  uuid = 0;
  statements = [];
  fixedNow = RealDate.parse(sc.now);
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
      case "sql":
        sqlite.exec(st.sql);
        results.push(null);
        break;
    }
  }
  out.push({ ...sc, expect: { results, statements } });
}
process.stdout.write(JSON.stringify(out, null, 1) + "\n");
