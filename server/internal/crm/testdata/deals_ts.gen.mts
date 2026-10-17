// Generates deals_ts.json: scenarios (seed, steps) and what the TS routes at
// 0068887 answered — stages, deals (board, progress, list, writes, the
// won/lost side effects), tasks, deal updates, and the records read by id,
// records and values — with every SQL statement and its params.
// deals_ts_test.go replays each scenario through the Go server and wants the
// same. (The insights routes and the Slack message on a won deal are not
// ported, so no scenario calls them or sets SLACK_CHANNEL.)
//
// These are what the parity scenario (scripts/parity/scenarios/deals.mjs)
// can't reach: custom fields and relations on deals, meetings, insights and
// tasks from calls (no route creates them), the last stage and an empty
// pipeline, a populated deal progress at a fixed time, Date.parse on the `at`
// of a deal update, and database errors (whose text is D1's there and
// SQLite's here).
//
// Run from the repository root (needs node 22 and the repo's node_modules):
//
//	TZ=UTC npx tsx server/internal/crm/testdata/deals_ts.gen.mts [ts-src-dir] > server/internal/crm/testdata/deals_ts.json
//
// ts-src-dir defaults to src/server, else the parity harness's reference
// checkout (.parity/ts-ref/src/server), which stays at 0068887 after the TS
// backend is deleted. The whole Hono app (index.ts) answers each request
// through app.request, with @clawnify/db over node:sqlite (foreign keys on,
// as in D1); custom fields are created with custom-fields.ts directly. Each
// scenario imports index.ts afresh, so its per-isolate flags start cold as a
// new Go App's do. UUIDs come in creation order. The clock is fixed at the
// scenario's `now` for new Date() and Date.now() (SQLite's datetime('now')
// still reads the real time). fetch throws: nothing here reaches a network.

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

const { initDB } = await import(path.join(src, "db.ts"));
const fields = await import(path.join(src, "custom-fields.ts"));
initDB({ DB: d1 }); // the app's middleware does this per request; the custom-field steps come first

// ── Scenarios ──────────────────────────────────────────────────────

type Step =
  // dbError: the answer is a database error, whose text differs between
  // node:sqlite and SQLite in Go: only the status is compared.
  | { op: "request"; method: string; path: string; body?: unknown; headers?: Record<string, string>; dbError?: boolean }
  | { op: "field"; input: Record<string, unknown> }
  | { op: "relation"; input: Record<string, unknown> }
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

const user = { "X-Clawnify-Caller": "user", "X-Clawnify-User-Id": "u-1", "X-Clawnify-User-Email": "ada@example.com" };
const agent = { "X-Clawnify-Caller": "agent" };

const base = [
  `INSERT INTO companies (id, name, domain, industry, customer_since, created_at, updated_at) VALUES
    ('co-acme', 'Acme', 'acme.example', 'Software', NULL, '2026-01-01 00:00:00', '2026-01-01 00:00:00'),
    ('co-beta', 'Beta', 'beta.example', 'retail', '  ', '2026-01-02 00:00:00', '2026-01-02 00:00:00'),
    ('co-cedar', 'Cedar & Co', '', 'Retail', '2024-02-02', '2026-01-03 00:00:00', '2026-01-03 00:00:00')`,
  `INSERT INTO contacts (id, first_name, last_name, email, company_id, title, status, created_at, updated_at) VALUES
    ('ct-ann', 'Ann', 'Lee', 'ann@acme.example', 'co-acme', 'CEO', 'customer', '2026-01-01 00:00:00', '2026-01-01 00:00:00'),
    ('ct-bo', 'Bo', NULL, 'bo@beta.example', 'co-beta', 'Buyer', 'lead', '2026-01-02 00:00:00', '2026-01-02 00:00:00'),
    ('ct-cy', '', 'Zed', 'cy@gmail.com', NULL, '', 'lead', '2026-01-03 00:00:00', '2026-01-03 00:00:00')`,
  `INSERT INTO stages (key, label, color, position, is_won, is_lost, created_at, updated_at) VALUES
    ('prospect', 'Prospect', 'slate', 0, 0, 0, '2026-01-01 00:00:00', '2026-01-01 00:00:00'),
    ('qualified', 'Qualified', 'sky', 1, 0, 0, '2026-01-01 00:00:00', '2026-01-01 00:00:00'),
    ('won', 'Won', 'emerald', 4, 1, 0, '2026-01-01 00:00:00', '2026-01-01 00:00:00'),
    ('signed', 'Signed 🎉', 'teal', 5, 1, 0, '2026-01-01 00:00:00', '2026-01-01 00:00:00'),
    ('lost', 'Lost', 'rose', 6, 0, 1, '2026-01-01 00:00:00', '2026-01-01 00:00:00')`,
  `INSERT INTO deals (id, name, contact_id, company_id, value, stage, close_date, notes, created_at, updated_at) VALUES
    ('d-1', 'Acme renewal', 'ct-ann', 'co-acme', 1200, 'qualified', '2026-02-01', '', '2020-01-06 00:00:00', '2020-01-06 00:00:00'),
    ('d-2', 'Beta pilot', 'ct-bo', NULL, 300.5, 'prospect', '', 'pilot notes', '2020-01-07 00:00:00', '2020-01-07 00:00:00'),
    ('d-3', 'Lost one', NULL, 'co-cedar', 999, 'lost', '', '', '2020-01-08 00:00:00', '2020-01-08 00:00:00'),
    ('d-4', 'Won one', NULL, 'co-acme', 50, 'won', '2025-01-01', '', '2020-01-09 00:00:00', '2020-01-09 00:00:00'),
    ('d-5', 'Cy deal', 'ct-cy', NULL, NULL, 'qualified', NULL, NULL, '2020-01-10 00:00:00', '2020-01-10 00:00:00')`,
];

const customFields: Step[] = [
  { op: "field", input: { entity_type: "deal", key: "tier", label: "Tier", field_type: "enumeration", options: { enum: ["gold", "silver"], required: true } } },
  { op: "field", input: { entity_type: "deal", key: "seats", label: "Seats", field_type: "integer" } },
  { op: "field", input: { entity_type: "deal", key: "signed_on", label: "", field_type: "date" } },
  { op: "relation", input: { entity_type: "deal", key: "partner_id", label: "Partner", relation_type: "many_to_one", target_entity: "company", inverse_key: "partner_deals", inverse_label: "Partner deals" } },
  { op: "relation", input: { entity_type: "contact", key: "favorite_deal_id", label: "Favorite deal", relation_type: "many_to_one", target_entity: "deal", inverse_key: "fans", inverse_label: "Fans" } },
];

const many = (n: number) => Array.from({ length: n }, (_, i) => `v-${i}`);

const scenarios: Scenario[] = [
  {
    name: "custom fields and relations on deals",
    now: T0,
    seed: base,
    steps: [
      ...customFields,
      { op: "sql", sql: "UPDATE deals SET tier = 'gold', partner_id = 'co-beta' WHERE id = 'd-1'" },
      { op: "sql", sql: "UPDATE contacts SET favorite_deal_id = 'd-1' WHERE id IN ('ct-ann', 'ct-bo')" },
      // Required custom fields: absent or empty on create; null or "" on update.
      req("POST", "/api/deals", { name: "Alpha" }),
      req("POST", "/api/deals", { name: "Alpha", tier: "" }),
      req("POST", "/api/deals", { name: "   ", tier: null }),
      // An enum violation fails after the row is written: 500, and the row stays.
      req("POST", "/api/deals", { name: "Alpha", tier: "bronze", seats: "3" }),
      get("/api/deals?search=Alpha&sort=name&order=asc"),
      // Flat and nested custom values (the bag wins); relations.
      req("POST", "/api/deals", { name: "Bravo", tier: "gold", seats: "12.7", signed_on: "2026-03-01", partner_id: "co-acme", contact_id: "ct-bo", custom: { seats: 3 } }),
      req("POST", "/api/deals", { name: "Charlie", custom: { tier: "silver" }, partner_id: "nope" }),
      req("POST", "/api/deals", { name: "Charlie", tier: "silver", partner_id: 5 }),
      req("POST", "/api/deals", { name: "Charlie", tier: "silver", fans: ["ct-ann"] }),
      req("POST", "/api/deals", { name: "Charlie", tier: "silver", partner_id: "", stage: "won", company_id: "co-beta", value: "Infinity" }),
      // Unknown fields: 422 with the valid ones (custom included, not the one_to_many side).
      req("POST", "/api/deals", { name: "X", tier: "gold", bogus: 1, 10: 2, 2: 3, custom: { nope: 2, fans: 1 } }),
      req("POST", "/api/deals", { name: "X", tier: "gold", contact_first_name: "x", company_name: "y" }),
      // Update.
      req("PUT", "/api/deals/d-1", { tier: null }),
      req("PUT", "/api/deals/d-1", { tier: "" }),
      req("PUT", "/api/deals/d-1", { seats: "40" }),
      req("PUT", "/api/deals/d-1", { tier: "platinum", name: "Acme renewal two" }),
      req("PUT", "/api/deals/d-1", { fans: "x" }),
      req("PUT", "/api/deals/d-1", { custom: { tier: "silver", seats: 7 }, signed_on: "", partner_id: "co-cedar" }),
      req("PUT", "/api/deals/d-2", { partner_id: "ct-ann" }),
      req("PUT", "/api/deals/nope", { seats: 1 }),
      req("PUT", "/api/deals/nope", { tier: null }),
      // Reads carry the relations: three of a many side on lists, a hundred on one record.
      get("/api/deals?sort=name&order=asc"),
      get("/api/deals/board"),
      get("/api/deals/progress"),
      get("/api/deals/d-1"),
      get("/api/companies/co-beta"),
      get("/api/companies/co-acme"),
      get("/api/contacts/ct-ann"),
      get("/api/contacts/ct-cy"),
      get("/api/records?entity=deal&search=a"),
      get("/api/records?entity=deal&ids=d-1,d-2,nope"),
      get("/api/values?entity=deal&field=tier"),
      get("/api/values?entity=deal&field=partner_id"),
      get("/api/values?entity=deal&field=seats"),
      get("/api/values?entity=deal&field=fans"),
      get("/api/values?entity=deal&field=value"),
      // The board filters on custom fields and many sides.
      ...[
        [{ field: "tier", op: "is", value: "gold" }],
        [{ field: "seats", op: "gt", value: 5 }],
        [{ field: "partner_id", op: "is", value: ["co-cedar", "co-acme"] }],
        [{ field: "fans", op: "is_not_empty" }],
        [{ field: "fans", op: "is", value: ["ct-bo"] }],
        [{ field: "signed_on", op: "is_empty" }],
        [{ field: "tier", op: "is", value: many(61) }],
        [{ logic: "or", rules: [{ field: "tier", op: "is", value: many(31) }, { field: "seats", op: "is", value: many(30) }] }],
      ].map((f) => get("/api/deals/board" + qs({ filters: f }))),
      get("/api/deals?sort=tier&order=asc"),
      // Deleting a deal clears the relations that point at it.
      req("DELETE", "/api/deals/d-1"),
      get("/api/contacts/ct-ann"),
      get("/api/companies/co-beta"),
      req("DELETE", "/api/deals/d-1"),
    ],
  },
  {
    name: "won and lost",
    now: T0,
    seed: [
      ...base,
      `INSERT INTO deals (id, name, contact_id, company_id, value, stage, close_date, created_at, updated_at) VALUES
        ('d-big', 'Big & <b>', 'ct-ann', 'co-cedar', 1234567.891, 'prospect', ' 2020-01-01 ', '2020-02-01 00:00:00', '2020-02-01 00:00:00'),
        ('d-half', 'Half', 'ct-cy', NULL, 0.5, 'prospect', '2099-01-01', '2020-02-02 00:00:00', '2020-02-02 00:00:00'),
        ('d-round', 'Round', NULL, 'co-beta', 999.9995, 'prospect', '2026-02-30', '2020-02-03 00:00:00', '2020-02-03 00:00:00'),
        ('d-text', 'Text', NULL, NULL, 'abc', 'prospect', NULL, '2020-02-04 00:00:00', '2020-02-04 00:00:00'),
        ('d-huge', 'Huge', NULL, NULL, 1e21, 'prospect', '', '2020-02-05 00:00:00', '2020-02-05 00:00:00'),
        ('d-thou', 'Thousandths', NULL, NULL, 1234.5675, 'prospect', '', '2020-02-06 00:00:00', '2020-02-06 00:00:00'),
        ('d-neg', 'Negative', NULL, NULL, -1234.5, 'prospect', '', '2020-02-07 00:00:00', '2020-02-07 00:00:00')`,
    ],
    steps: [
      req("PUT", "/api/deals/d-big", { stage: "won" }),
      get("/api/companies/co-cedar"),
      req("PUT", "/api/deals/d-half", { stage: " signed " }),
      req("PUT", "/api/deals/d-round", { stage: "won", notes: "x" }),
      get("/api/companies/co-beta"),
      req("PUT", "/api/deals/d-2", { stage: "won" }),
      // Moved into won again: logged again.
      req("PUT", "/api/deals/d-2", { stage: "won" }),
      req("PUT", "/api/deals/d-text", { stage: "won" }),
      req("PUT", "/api/deals/d-huge", { stage: "won" }),
      req("PUT", "/api/deals/d-thou", { stage: "won", value: "1234.5675" }),
      req("PUT", "/api/deals/d-neg", { stage: "won" }),
      req("PUT", "/api/deals/d-5", { stage: "won", value: 0 }),
      req("PUT", "/api/deals/d-1", { stage: "lost" }),
      req("PUT", "/api/deals/d-1", { stage: "qualified" }),
      req("PUT", "/api/deals/d-1", { name: "Renamed in won", value: 12 }),
      // A deal created in a won stage makes its company a customer, unlogged.
      req("POST", "/api/deals", { name: "Born won", company_id: "co-acme", stage: "won", close_date: "2026-10-15" }),
      get("/api/companies/co-acme"),
      req("POST", "/api/deals", { name: "Born won by contact", contact_id: "ct-bo", stage: "signed", close_date: "2026-01-01" }),
      get("/api/deals/progress"),
    ],
  },
  {
    name: "tasks from calls, and deal updates",
    now: T0,
    seed: [
      ...base,
      "INSERT INTO meeting_sync (id, enabled, phase) VALUES (1, 1, 'live')",
      "INSERT INTO email_accounts (mailbox, enabled, phase) VALUES ('me@mine.example', 1, 'live')",
      `INSERT INTO meetings (id, title, starts_at, company_id, link_status, summary, sentiment, sentiment_reason, digest_status) VALUES
        ('m-1', 'Acme kickoff', '2026-10-01T10:00:00.000Z', 'co-acme', 'manual', 'Went well', 1, 'Happy', 'done'),
        ('m-2', 'Acme check-in', '2026-10-10T10:00:00.000Z', 'co-acme', 'auto', 'Doubts', -2, 'Pricing', 'done'),
        ('m-3', 'Acme review', '2026-10-20T15:00:00.000Z', 'co-acme', 'auto', NULL, NULL, NULL, 'none'),
        ('m-4', 'Beta call', '2026-09-01T09:00:00.000Z', 'co-beta', 'auto', 'Fine', 0, 'Ok', 'done'),
        ('m-5', 'Unlinked', '2026-10-05T09:00:00.000Z', NULL, 'unmatched', NULL, NULL, NULL, 'none')`,
      `INSERT INTO tasks (id, title, company_id, contact_id, deal_id, meeting_id, owed_by, due_date, done_at, quote, created_by, created_at, updated_at) VALUES
        ('t-1', 'Send proposal', 'co-acme', NULL, NULL, 'm-1', 'us', '2026-10-10', NULL, 'We will send it', 'ai', '2026-10-01 11:00:00', '2026-10-01 11:00:00'),
        ('t-2', 'Their legal', 'co-acme', NULL, 'd-1', 'm-2', 'them', '2026-10-20', NULL, NULL, 'ai', '2026-10-10 11:00:00', '2026-10-10 11:00:00'),
        ('t-3', 'Undated', 'co-acme', 'ct-ann', NULL, NULL, 'us', NULL, NULL, NULL, 'ada@example.com', '2026-10-02 11:00:00', '2026-10-02 11:00:00'),
        ('t-4', 'Done before', 'co-acme', NULL, NULL, NULL, 'us', '2026-09-01', '2026-09-02 00:00:00', NULL, NULL, '2026-09-01 11:00:00', '2026-09-02 00:00:00'),
        ('t-5', 'Done later', 'co-beta', NULL, NULL, NULL, 'nobody', '2026-09-05', '2026-09-06 00:00:00', NULL, NULL, '2026-09-01 12:00:00', '2026-09-06 00:00:00'),
        ('t-6', 'Beta follow-up', 'co-beta', NULL, NULL, 'm-4', 'us', '2026-10-13', NULL, NULL, 'ai', '2026-09-01 10:00:00', '2026-09-01 10:00:00'),
        ('t-7', 'Other deal task', 'co-acme', NULL, 'd-4', NULL, 'us', '2026-10-11', NULL, NULL, NULL, '2026-10-03 11:00:00', '2026-10-03 11:00:00')`,
      `INSERT INTO insights (id, company_id, meeting_id, kind, text, quote, status, deal_id, created_at, updated_at) VALUES
        ('i-1', 'co-acme', 'm-1', 'expansion', 'A second team wants it ${"x".repeat(110)} and more', 'Our ops team would use this', 'open', NULL, '2026-10-01 12:00:00', '2026-10-01 12:00:00'),
        ('i-2', 'co-acme', 'm-2', 'risk', 'Champion leaving', NULL, 'open', NULL, '2026-10-10 12:00:00', '2026-10-10 12:00:00'),
        ('i-3', 'co-acme', NULL, 'idea', 'Use the API', '', 'open', NULL, '2026-10-12 12:00:00', '2026-10-12 12:00:00'),
        ('i-4', NULL, 'm-5', 'expansion', 'No company yet', NULL, 'open', NULL, '2026-10-05 12:00:00', '2026-10-05 12:00:00'),
        ('i-5', 'co-beta', 'm-4', 'expansion', 'Already a deal', NULL, 'done', 'd-2', '2026-09-01 12:00:00', '2026-09-01 12:00:00'),
        ('i-6', 'co-beta', NULL, 'risk', 'Dismissed worry', NULL, 'dismissed', NULL, '2026-09-02 12:00:00', '2026-09-02 12:00:00')`,
    ],
    steps: [
      // Tasks.
      get("/api/tasks"),
      get("/api/tasks?status=all"),
      get("/api/tasks?status=done"),
      get("/api/tasks?deal_id=d-1"),
      get("/api/tasks?deal_id=d-5"),
      get("/api/tasks?deal_id=d-2&status=all"),
      get("/api/tasks?company_id=co-beta&status=all&owed_by=us"),
      get("/api/tasks?limit=2"),
      req("PUT", "/api/tasks/t-1", { company_id: "co-acme" }, { headers: user }),
      req("PUT", "/api/tasks/t-1", { company_id: "co-beta", due_date: null }, { headers: user }),
      req("PUT", "/api/tasks/t-6", { company_id: null }, { headers: agent }),
      req("PUT", "/api/tasks/t-2", { done: true, owed_by: "us" }, { headers: user }),
      req("PUT", "/api/tasks/t-2", { done: false }, { headers: user }),
      req("POST", "/api/tasks", { title: " New ", company_id: "co-acme", contact_id: "ct-ann", deal_id: "d-1", owed_by: "them", due_date: "2026-10-30" }, { headers: user }),
      req("POST", "/api/tasks", { title: "x", company_id: "" }, { headers: user }),
      // Deal updates: what they close, and the next step on the deal's account.
      req("POST", "/api/deals/d-1/updates", { kind: "call", summary: " Talked ", done_task_ids: ["t-1", "t-2", "t-3", "t-6", "t-7", "t-3"], next_step: { title: " Send the contract ", due_date: "2026-10-21" } }, { headers: user }),
      req("POST", "/api/deals/d-5/updates", { kind: "message", summary: "WhatsApp", next_step: { title: "Call back", due_date: "2026-10-01", owed_by: "them" }, close_date: "2026-12-01" }, { headers: agent }),
      req("POST", "/api/deals/d-2/updates", { kind: "note", summary: "A note", done_task_ids: ["t-6"] }, { headers: { "X-Clawnify-Caller": "api" } }),
      get("/api/tasks?status=all"),
      get("/api/deals/progress"),
      get("/api/deals/board"),
      get("/api/deals/d-5?tz=-600"),
      get("/api/deals/d-1?tz=840"),
      req("DELETE", "/api/tasks/t-3", undefined, { headers: user }),
      get("/api/tasks?status=all&limit=200"),
    ],
  },
  {
    name: "when a deal update happened",
    now: T0,
    seed: [...base, "INSERT INTO meeting_sync (id, enabled, phase) VALUES (1, 1, 'live')"],
    steps: [
      ...[
        ["2026-10-14", ""], ["2026-10-13", ""], ["2026-10-15", ""], ["2026-10-15", "?tz=840"], ["2026-10-14", "?tz=-720"], ["2026-10-13", "?tz=-720"],
        ["2026-10-15", "?tz=841"], ["2026-10-15", "?tz=720&tz=0"],
        ["2026-10-14T12:04:59Z", ""], ["2026-10-14T12:05:00Z", ""], ["2026-10-14T12:05:00.001Z", ""], ["2026-10-14T13:00:00+02:00", ""],
        ["2026-10-14T13:00:00+0200", ""], ["2026-10-14 10:00", ""], ["2026-10-14T10:00", ""], ["2026-10-14t10:00:00z", ""],
        ["2026-02-30T10:00:00Z", ""], ["2026-10-14T24:00:00Z", ""], ["2026-10-14T10:00:00.123456Z", ""], ["2026-10-14T10:00:00.5Z", ""],
        ["0099-01-01T00:00:00Z", ""], ["yesterday", ""], ["2026-10-14T10:00:60Z", ""], ["2026-13-01T00:00:00Z", ""],
        ["2026-10", ""], [" 2026-10-14T10:00:00Z", ""], ["2026-10-14T10:00:00Z ", ""], ["October 1, 2026", ""], ["2026/10/01", ""],
        ["2026-10-14 10:00:00 +01:00", ""], ["2026-1-4", ""], ["+002026-10-01T00:00:00Z", ""], ["1", ""], ["2026-10-14T10Z", ""],
      ].map(([at, q]) => req("POST", `/api/deals/d-1/updates${q}`, { kind: "call", summary: `at ${at}`, at }, { headers: agent })),
      get("/api/deals/d-1"),
    ],
  },
  {
    name: "stages at the edges",
    now: T0,
    seed: [
      `INSERT INTO companies (id, name) VALUES ('co-acme', 'Acme')`,
    ],
    steps: [
      // An empty pipeline gets the default stages on first read.
      get("/api/stages"),
      req("POST", "/api/stages", { label: "İstanbul Straße" }),
      req("POST", "/api/stages", { label: "Ünïcode", position: 2 }),
      req("PUT", "/api/stages/prospect", { position: 9, label: "Lead" }),
      get("/api/stages"),
      // The first stage by position is the default for a new deal: here a won one.
      { op: "sql", sql: "UPDATE stages SET position = -1 WHERE key = 'won'" },
      req("POST", "/api/deals", { name: "Default won", company_id: "co-acme" }),
      get("/api/companies/co-acme"),
      // Every stage won or lost: a deal is open only in a stage that isn't one.
      { op: "sql", sql: "UPDATE stages SET is_lost = 1 WHERE is_won = 0" },
      { op: "sql", sql: "INSERT INTO deals (id, name, company_id, stage) VALUES ('d-gone', 'Stage gone', 'co-acme', 'prospect')" },
      get("/api/deals/progress"),
      get("/api/deals?sort=name&order=asc"),
      // Reassigning on delete.
      req("DELETE", "/api/stages/won?reassign_to=lost"),
      req("DELETE", "/api/stages/won"),
      req("DELETE", "/api/stages/prospect?reassign_to=i_stanbul_stra_e"),
      // The last stage can't go.
      { op: "sql", sql: "DELETE FROM stages WHERE key != 'lost'" },
      req("DELETE", "/api/stages/lost?reassign_to=nope"),
      req("POST", "/api/deals", { name: "x", stage: "nope" }),
      get("/api/stages"),
    ],
  },
  {
    name: "deal progress on a populated database",
    now: T0,
    seed: [
      ...base,
      "INSERT INTO meeting_sync (id, enabled, phase) VALUES (1, 1, 'live')",
      "INSERT INTO email_accounts (mailbox, enabled, phase) VALUES ('me@mine.example', 1, 'live')",
      `INSERT INTO companies (id, name, domain) VALUES ('co-dune', 'dune', 'dune.example'), ('co-echo', 'Écho', 'echo.example')`,
      `INSERT INTO deals (id, name, contact_id, company_id, value, stage, close_date, created_at) VALUES
        ('d-e1', 'écho', NULL, 'co-echo', 100, 'prospect', '2026-10-01', '2020-03-01 00:00:00'),
        ('d-e2', 'Echo', NULL, 'co-echo', 100, 'prospect', '2026-10-01', '2020-03-02 00:00:00'),
        ('d-e3', 'echo', NULL, 'co-echo', 100, 'prospect', '2026-10-01', '2020-03-03 00:00:00'),
        ('d-e4', 'alpha', NULL, 'co-dune', 'lots', 'mystery', NULL, '2020-03-04 00:00:00'),
        ('d-e5', 'Zed', NULL, 'co-dune', 5000, 'qualified', ' 2026-12-01 ', '2020-03-05 00:00:00')`,
      `INSERT INTO meetings (id, title, starts_at, company_id, link_status, summary, sentiment, sentiment_reason, digest_status) VALUES
        ('m-1', 'Acme next', '2026-10-16T10:00:00.000Z', 'co-acme', 'manual', NULL, NULL, NULL, 'none'),
        ('m-2', 'Dune bad call', '2026-10-12T10:00:00.000Z', 'co-dune', 'auto', 'Angry', -2, 'Pricing', 'done')`,
      `INSERT INTO tasks (id, title, company_id, deal_id, owed_by, due_date) VALUES
        ('t-1', 'Overdue promise', 'co-echo', NULL, 'us', '2026-10-01'),
        ('t-2', 'Their answer', NULL, 'd-e5', 'them', '2026-10-20')`,
      `INSERT INTO insights (id, company_id, kind, text) VALUES ('i-1', 'co-beta', 'risk', 'Budget cut')`,
      `INSERT INTO activities (id, entity_type, entity_id, type, body, created_at) VALUES
        ('a-1', 'deal', 'd-5', 'call', '', '2026-10-13 08:00:00'),
        ('a-2', 'company', 'co-beta', 'email', '', '2026-09-01 08:00:00')`,
    ],
    steps: [
      get("/api/deals/progress"),
      get("/api/deals/progress?limit=2&tz=-600"),
      get("/api/deals/progress?tz=840"),
      get("/api/deals/board"),
      get("/api/deals/board?tz=840" + "&filters=" + encodeURIComponent(JSON.stringify([{ field: "close_date", op: "in_past" }]))),
      get("/api/deals/board" + qs({ filters: [{ field: "created_at", op: "relative", value: "PAST_7_DAY" }] })),
      get("/api/deals/d-e5"),
      get("/api/deals/d-4"),
      get("/api/deals?sort=value&order=desc"),
      get("/api/deals?sort=close_date&order=asc&limit=3&page=2"),
      get("/api/deals?company_id=co-echo&sort=name&order=asc"),
      get("/api/values?entity=deal&field=name"),
      get("/api/values?entity=company&field=industry"),
      get("/api/records?entity=contact"),
      get("/api/records?entity=company&search=%C3%A9"),
    ],
  },
  {
    name: "database errors",
    now: T0,
    seed: base,
    steps: [
      req("POST", "/api/deals", { name: "x", contact_id: "no-such-contact" }, { dbError: true }),
      req("PUT", "/api/deals/d-1", { company_id: "no-such-company" }, { dbError: true }),
      get("/api/deals?page=abc", { dbError: true }),
      req("PUT", "/api/tasks/nope", { title: "x" }, { headers: user }),
      req("POST", "/api/tasks", { title: "x", deal_id: "" }, { headers: user }),
      get("/api/deals?sort=name&order=asc"),
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
