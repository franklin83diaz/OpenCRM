// Generates customers_ts.json: scenarios (seed, steps) and what the TS
// customers.ts, deal-progress.ts and lookup.ts at 0068887 did with each —
// every step's result and every SQL statement with its params.
// customers_ts_test.go replays each scenario through the Go port and wants
// the same.
//
// Run from the repository root (needs node 22 and the repo's node_modules):
//
//	TZ=UTC npx tsx server/internal/crm/testdata/customers_ts.gen.mts [ts-src-dir] > server/internal/crm/testdata/customers_ts.json
//
// ts-src-dir defaults to src/server, else the parity harness's reference
// checkout (.parity/ts-ref/src/server), which stays at 0068887 after the TS
// backend is deleted. @clawnify/db runs over node:sqlite. The clock is UTC,
// as on Workers: a SQLite "YYYY-MM-DD HH:MM:SS" read by Date.parse is UTC
// there, and local time under node otherwise.

process.env.TZ = "UTC";

import { DatabaseSync } from "node:sqlite";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";

const src = path.resolve(process.argv[2] ?? (existsSync("src/server/customers.ts") ? "src/server" : ".parity/ts-ref/src/server"));
const schema = readFileSync("server/schema.sql", "utf8"); // the schema the Go tests use

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
const customers = await import(path.join(src, "customers.ts"));
const progress = await import(path.join(src, "deal-progress.ts"));
const lookup = await import(path.join(src, "lookup.ts"));
initDB({ DB: d1 });

// ── Scenarios ──────────────────────────────────────────────────────

type Deal = { id: string; stage: string; company_id: string | null; contact_id: string | null; close_date: string | null };
type Step =
  | { op: "overview"; now: string; tz: number }
  | { op: "accounts"; now: string; ids: string[]; of: string[] }
  | { op: "progress"; now: string; tz: number; deals: Deal[] }
  | { op: "closed" }
  | { op: "lookup"; now: string; addresses: Array<{ email: string; domain: string }> }
  | { op: "lookupOne"; now: string; email: string; domain: string }
  | { op: "bare"; values: string[] }
  | { op: "sql"; sql: string };

interface Scenario {
  name: string;
  seed: string[];
  steps: Step[];
}

const T0 = "2026-10-17T12:00:00.000Z";

const syncsLive = [
  "INSERT INTO meeting_sync (id, enabled, phase) VALUES (1, 1, 'live')",
  `INSERT INTO email_accounts (mailbox, enabled, phase, blocklist) VALUES ('me@mine.example', 1, 'live', '["Blocked@Beta.example", "@spam.example", 5]')`,
];

const accounts = [
  `INSERT INTO companies (id, name, domain, customer_since, renewal_date, updated_at) VALUES
    ('co-acme', 'Acme', 'https://www.Acme.example/', '2025-01-10', '2026-10-27', '2026-10-01 00:00:00'),
    ('co-beta', 'Beta', 'beta.example', '2024-05-01', '2026-09-03', '2026-10-01 00:00:01'),
    ('co-cedar', 'Cedar & Co', '', '2026-01-01', '', '2026-10-01 00:00:02'),
    ('co-delta', 'Delta', NULL, ' 2025-03-03 ', '2026-10-17', '2026-10-01 00:00:03'),
    ('co-echo', 'Écho', 'echo.example', '2025-06-01', NULL, '2026-10-01 00:00:04'),
    ('co-zulu', 'zulu', 'zulu.example', '2025-01-01', NULL, '2026-10-01 00:00:05'),
    ('co-quiet', 'Quiet Ltd', 'quiet.example', '2025-02-02', 'not a day', '2026-10-01 00:00:06'),
    ('co-yellow', 'Yellow Inc', 'yellow.example', '2025-02-03', '2026-11-30', '2026-10-01 00:00:07'),
    ('co-prospect', 'Prospect Co', 'prospect.example', NULL, '2026-10-20', '2026-10-01 00:00:08'),
    ('co-blank', 'Blank', 'blank.example', '  ', NULL, '2026-10-01 00:00:09'),
    ('co-gamma', 'Gamma', 'gamma.example', NULL, NULL, '2026-10-05 00:00:00'),
    ('co-gamma2', 'Gamma Two', 'gamma.example/', NULL, NULL, '2026-10-06 00:00:00'),
    ('co-gamma3', 'Gamma Three', 'http://gamma.example', '2026-03-03', NULL, '2026-01-01 00:00:00'),
    ('co-gamma4', 'Gamma Four', 'WWW.gamma.example', '2026-03-04', NULL, '2026-10-09 00:00:00'),
    ('co-gamma5', 'Gamma Five', 'gamma.example', ' 2026-03-05 ', ' 2027-01-01 ', '2026-02-02 00:00:00')`,
  `INSERT INTO contacts (id, first_name, last_name, email, company_id, title, status, last_contacted_at, updated_at) VALUES
    ('ct-ann', 'Ann', 'Lee', 'ann@acme.example', 'co-acme', 'CEO', 'customer', '2026-10-10T09:00:00.000Z', '2026-10-02 00:00:00'),
    ('ct-abe', 'Abe', NULL, ' ABE@Acme.example ', 'co-acme', NULL, 'customer', NULL, '2026-10-03 00:00:00'),
    ('ct-bea', 'Bea', 'Bo', 'bea@beta.example', 'co-beta', 'CTO', 'lead', '2026-08-01T00:00:00.000Z', '2026-10-02 00:00:01'),
    ('ct-bill', 'Bill', '', 'blocked@beta.example', 'co-beta', '', 'lead', NULL, '2026-10-02 00:00:02'),
    ('ct-sid', 'Sid', '', 'sid@spam.example', 'co-beta', '', 'lead', NULL, '2026-10-02 00:00:03'),
    ('ct-cy', 'Cy', '', 'cy@cedar.example', 'co-cedar', '', 'lead', NULL, '2026-10-02 00:00:04'),
    ('ct-cleo', 'Cleo', '', 'cleo@cedar.example', 'co-cedar', '', 'lead', NULL, '2026-10-02 00:00:05'),
    ('ct-cal', 'Cal', '', 'cal@cedar.example', 'co-cedar', '', 'lead', NULL, '2026-10-02 00:00:06'),
    ('ct-dee', 'Dee', '', '', 'co-delta', '', 'lead', NULL, '2026-10-02 00:00:07'),
    ('ct-dan', 'Dan', '', NULL, 'co-delta', '', 'lead', NULL, '2026-10-02 00:00:08'),
    ('ct-eve', 'Eve', '', 'eve@spam.example', 'co-echo', '', 'lead', NULL, '2026-10-02 00:00:09'),
    ('ct-quinn', 'Quinn', '', 'quinn@quiet.example', 'co-quiet', '', 'lead', NULL, '2026-10-02 00:00:10'),
    ('ct-yan', 'Yan', '', 'yan@yellow.example', 'co-yellow', '', 'lead', '2026-09-17T08:00:00.000Z', '2026-10-02 00:00:11'),
    ('ct-lou', 'Lou', 'Low', 'lou@gamma.example', NULL, 'Buyer', 'lead', NULL, '2026-10-02 00:00:12'),
    ('ct-dup1', 'Dup', '', 'dup@zulu.example', 'co-zulu', '', 'lead', NULL, '2026-10-01 00:00:00'),
    ('ct-dup2', 'Dupe', '', 'DUP@zulu.example', 'co-acme', '', 'customer', NULL, '2026-10-05 00:00:00')`,
  `INSERT INTO email_contact_imports (mailbox, contact_id, email) VALUES
    ('me@mine.example', 'ct-abe', 'abe@acme.example'),
    ('me@mine.example', 'ct-ann', 'old@acme.example'),
    ('me@mine.example', 'ct-bea', 'bea@beta.example'),
    ('me@mine.example', 'ct-quinn', 'quinn@quiet.example'),
    ('me@mine.example', 'ct-yan', 'yan@yellow.example'),
    ('other@mine.example', 'ct-cy', 'cy@cedar.example')`,
  `INSERT INTO stages (key, label, position, is_won, is_lost) VALUES
    ('prospect', 'Prospect', 0, 0, 0), ('qualified', 'Qualified', 1, 0, 0), ('won', 'Won', 4, 1, 0), ('lost', 'Lost', 5, 0, 1), ('signed', 'Signed', 6, 1, 0)`,
  `INSERT INTO deals (id, name, contact_id, company_id, value, stage, close_date, updated_at) VALUES
    ('d-acme-open', 'Acme expansion', 'ct-ann', 'co-acme', 5000, 'qualified', '2026-10-01', '2026-10-10 00:00:00'),
    ('d-acme-won', 'Acme first', NULL, 'co-acme', 1000.5, 'won', '2025-01-10', '2025-01-10 00:00:00'),
    ('d-acme-lost', 'Acme lost', NULL, 'co-acme', NULL, 'lost', NULL, '2026-10-12 00:00:00'),
    ('d-acme-signed', 'Acme signed', NULL, 'co-acme', 7, 'signed', '', '2026-10-13 00:00:00'),
    ('d-via-contact', 'Beta via contact', 'ct-bea', NULL, 300, 'prospect', '  ', '2026-10-02 00:00:00'),
    ('d-loose', 'Loose deal', NULL, NULL, 0, 'prospect', '2026-12-01', '2026-10-03 00:00:00'),
    ('d-lou', 'Lou deal', 'ct-lou', NULL, 50, 'mystery', NULL, '2026-10-04 00:00:00'),
    ('d-zulu', 'Zulu deal', NULL, 'co-zulu', 10, 'qualified', NULL, '2026-10-05 00:00:00'),
    ('d-quiet', 'Quiet deal', NULL, 'co-quiet', 10, 'qualified', NULL, '2026-10-06 00:00:00'),
    ('d-g01', 'G1', NULL, 'co-gamma5', 1, 'won', '', '2026-10-01 00:00:00'),
    ('d-g02', 'G2', NULL, 'co-gamma5', 2, 'prospect', '', '2026-09-02 00:00:00'),
    ('d-g03', 'G3', NULL, 'co-gamma5', 3, 'lost', '', '2026-10-03 00:00:00'),
    ('d-g04', 'G4', NULL, 'co-gamma5', 4, 'qualified', '', '2026-09-04 00:00:00'),
    ('d-g05', 'G5', NULL, 'co-gamma5', 5, 'prospect', '', NULL),
    ('d-g06', 'G6', NULL, 'co-gamma5', 6, 'prospect', '', '2026-09-06 00:00:00'),
    ('d-g07', 'G7', NULL, 'co-gamma5', 7, 'signed', '', '2026-10-07 00:00:00'),
    ('d-g08', 'G8', NULL, 'co-gamma5', 8, 'prospect', '', '2026-09-08 00:00:00'),
    ('d-g09', 'G9', NULL, 'co-gamma5', 9, 'prospect', '', '2026-09-09 00:00:00'),
    ('d-g10', 'G10', NULL, 'co-gamma5', 10, 'prospect', '', '2026-09-10 00:00:00'),
    ('d-g11', 'G11', NULL, 'co-gamma5', 11, 'qualified', '', '2026-09-11 00:00:00')`,
  `INSERT INTO meetings (id, title, starts_at, company_id, link_status, summary, sentiment, sentiment_reason, digest_status) VALUES
    ('m-acme-1', 'Acme kickoff', '2026-10-01T10:00:00.000Z', 'co-acme', 'manual', 'Went well', 1, 'Happy', 'done'),
    ('m-acme-2', 'Acme check-in', '2026-10-15T10:00:00.000Z', 'co-acme', 'auto', 'Some doubts', 0, 'Unsure', 'done'),
    ('m-acme-3', 'Acme review', '2026-10-20T15:00:00.000Z', 'co-acme', 'auto', NULL, NULL, NULL, 'none'),
    ('m-acme-4', 'Acme QBR', '2026-10-30T15:00:00.000Z', 'co-acme', 'manual', NULL, NULL, NULL, 'none'),
    ('m-acme-old', 'Acme old', '2026-06-01T10:00:00.000Z', 'co-acme', 'auto', 'Old times', 2, 'Great', 'done'),
    ('m-beta-1', 'Beta call', '2026-10-16T09:00:00.000Z', 'co-beta', 'auto', 'Pricing fight', -2, 'Angry about pricing', 'done'),
    ('m-beta-2', 'Beta follow-up', '2026-10-18T09:00:00.000Z', 'co-beta', 'manual', NULL, NULL, NULL, 'none'),
    ('m-quiet-1', 'Quiet call', '2026-09-01 10:00:00', 'co-quiet', 'manual', NULL, NULL, NULL, 'none'),
    ('m-quiet-x', 'Unmatched', '2026-10-16T10:00:00.000Z', 'co-quiet', 'unmatched', NULL, NULL, NULL, 'none'),
    ('m-cedar-x', 'Ignored', '2026-10-16T11:00:00.000Z', 'co-cedar', 'ignored', NULL, NULL, NULL, 'none'),
    ('m-echo-1', 'Echo call', '2026-10-05T10:00:00.000Z', 'co-echo', 'auto', 'Quiet call', NULL, NULL, 'done'),
    ('m-delta-1', 'Delta intro', '2026-10-21T08:00:00.000Z', 'co-delta', 'manual', NULL, NULL, NULL, 'none'),
    ('m-delta-2', 'Delta prep', '2026-10-24T12:00:00.000Z', 'co-delta', 'manual', NULL, NULL, NULL, 'none'),
    ('m-gamma-1', 'Gamma call', '2026-10-09T12:00:00.000Z', 'co-gamma5', 'auto', NULL, NULL, NULL, 'none'),
    ('m-gamma-2', 'Gamma next', '2026-11-09T12:00:00.000Z', 'co-gamma5', 'auto', NULL, NULL, NULL, 'none')`,
  `INSERT INTO tasks (id, title, company_id, deal_id, owed_by, due_date, done_at) VALUES
    ('t1', 'Send proposal', 'co-acme', NULL, 'us', '2026-09-03', NULL),
    ('t2', 'Fix login', 'co-acme', NULL, 'us', '2026-10-10', NULL),
    ('t3', 'Sign contract', 'co-acme', NULL, 'them', '2026-10-01', NULL),
    ('t4', 'Call back', 'co-acme', NULL, 'us', '2026-10-17', NULL),
    ('t5', 'Someday', 'co-acme', NULL, 'us', NULL, NULL),
    ('t6', 'Done already', 'co-acme', NULL, 'us', '2026-09-01', '2026-09-02 00:00:00'),
    ('t7', 'Review', 'co-beta', NULL, 'us', '2026-10-16', NULL),
    ('t8', 'Their docs', 'co-delta', NULL, 'them', NULL, NULL),
    ('t9', 'Plan tomorrow', 'co-delta', NULL, 'us', '2026-10-18', NULL),
    ('t10', 'Deal task overdue', 'co-acme', 'd-acme-open', 'us', '2026-10-12', NULL),
    ('t11', 'Deal task theirs', 'co-acme', 'd-acme-open', 'them', '2026-10-25', NULL),
    ('t12', 'Loose deal task', NULL, 'd-loose', 'us', '2026-10-20', NULL),
    ('t13', 'Odd owner', 'co-zulu', NULL, 'nobody', '2026-10-19', NULL),
    ('t14', 'Their reply', 'co-zulu', 'd-zulu', 'them', '2026-10-16', NULL)`,
  `INSERT INTO insights (id, company_id, kind, text, status, created_at) VALUES
    ('i1', 'co-acme', 'risk', 'Champion leaving', 'open', '2026-10-10 00:00:00'),
    ('i2', 'co-acme', 'risk', 'Budget cut', 'open', '2026-10-12 00:00:00'),
    ('i3', 'co-acme', 'idea', 'Use the API', 'open', '2026-10-11 00:00:00'),
    ('i4', 'co-acme', 'expansion', 'Second team', 'open', '2026-10-13 00:00:00'),
    ('i5', 'co-acme', 'risk', 'Old worry', 'dismissed', '2026-10-14 00:00:00'),
    ('i6', 'co-echo', 'risk', 'Competitor trial', 'open', '2026-10-06 00:00:00')`,
  `INSERT INTO activities (id, entity_type, entity_id, type, body, created_at) VALUES
    ('a1', 'company', 'co-cedar', 'call', '', '2026-10-16 08:00:00'),
    ('a2', 'contact', 'ct-yan', 'note', '', '2026-10-16 08:00:00'),
    ('a3', 'company', 'co-zulu', 'email', '', '2026-09-30 12:00:00'),
    ('a4', 'deal', 'd-zulu', 'meeting', '', '2026-10-01 12:00:00'),
    ('a5', 'company', 'co-echo', 'call', '', 'yesterday'),
    ('a6', 'contact', 'ct-bea', 'message', '', '2026-10-14 10:00:00'),
    ('a7', 'deal', 'd-loose', 'call', '', '2026-10-12 10:00:00'),
    ('a8', 'company', 'co-gamma5', 'meeting', '', '2026-10-11 10:00:00'),
    ('a9', 'contact', 'ct-ann', 'email', '', '2026-10-16 18:00:00'),
    ('a10', 'contact', 'ct-ann', 'call', '', '2026-09-20 18:00:00'),
    ('a11', 'deal', 'd-acme-open', 'stage_change', '', '2026-10-16 19:00:00'),
    ('a12', 'deal', 'd-g02', 'call', '', '2026-10-12 09:30:00'),
    ('a13', 'contact', 'ct-lou', 'meeting', '', '2026-10-13 09:30:00')`,
];

const progressDeals: Deal[] = [
  { id: "d-acme-open", stage: "qualified", company_id: "co-acme", contact_id: "ct-ann", close_date: "2026-10-01" },
  { id: "d-acme-won", stage: "won", company_id: "co-acme", contact_id: null, close_date: "2025-01-10" },
  { id: "d-acme-signed", stage: "signed", company_id: "co-acme", contact_id: null, close_date: "" },
  { id: "d-via-contact", stage: "prospect", company_id: null, contact_id: "ct-bea", close_date: "  " },
  { id: "d-loose", stage: "prospect", company_id: null, contact_id: null, close_date: "2026-12-01" },
  { id: "d-lou", stage: "mystery", company_id: null, contact_id: "ct-lou", close_date: null },
  { id: "d-empty-co", stage: "prospect", company_id: "", contact_id: "ct-cy", close_date: null },
  { id: "d-missing-contact", stage: "prospect", company_id: null, contact_id: "ct-nope", close_date: " 2026-10-30 " },
  { id: "d-blank-contact", stage: "prospect", company_id: null, contact_id: "", close_date: "2026-10-16" },
  { id: "d-zulu", stage: "qualified", company_id: "co-zulu", contact_id: null, close_date: "2026-11-01" },
  { id: "d-quiet", stage: "qualified", company_id: "co-quiet", contact_id: null, close_date: null },
  { id: "d-beta", stage: "qualified", company_id: "co-beta", contact_id: "ct-bill", close_date: "2026-10-18" },
  { id: "d-delta", stage: "Qualified", company_id: "co-delta", contact_id: null, close_date: null },
  { id: "d-echo", stage: "qualified", company_id: "co-echo", contact_id: null, close_date: null },
  { id: "d-cedar", stage: "qualified", company_id: "co-cedar", contact_id: null, close_date: null },
];

const addresses = [
  { email: "ann@acme.example", domain: "" },
  { email: " ABE@ACME.EXAMPLE ", domain: "" },
  { email: "pat@gmail.com", domain: "https://www.Acme.example/" },
  { email: "someone@gamma.example", domain: "" },
  { email: "lou@gamma.example", domain: "" },
  { email: "", domain: "" },
  { email: "nobody@unknown.example", domain: "" },
  { email: "x@beta.example", domain: "gmail.com" },
  { email: "bea@beta.example", domain: "" },
  { email: "dup@zulu.example", domain: "" },
  { email: "quinn@quiet.example", domain: "other.example" },
  { email: "ann@acme.example", domain: "" },
  { email: "noat", domain: "" },
  { email: "a@b", domain: "www.gamma.example" },
  { email: "x@WWW.gamma.example", domain: "" },
  { email: "y@sub.zulu.example", domain: "" },
  { email: "", domain: " HTTPS://zulu.example/path/x " },
  { email: "z@yahoo.com", domain: "outlook.com" },
  { email: "z@quiet.example", domain: "" },
  { email: "Zoë@Cedar.example", domain: "" },
];

const scenarios: Scenario[] = [
  {
    name: "nothing yet",
    seed: [],
    steps: [
      { op: "overview", now: T0, tz: 0 },
      { op: "accounts", now: T0, ids: [], of: ["nope"] },
      { op: "progress", now: T0, tz: 0, deals: [] },
      { op: "closed" },
      { op: "lookup", now: T0, addresses: [{ email: "a@b.example", domain: "" }, { email: "", domain: "" }] },
      { op: "lookupOne", now: T0, email: "", domain: "" },
      { op: "lookup", now: T0, addresses: [] },
    ],
  },
  {
    name: "accounts with everything",
    seed: [...syncsLive, ...accounts],
    steps: [
      { op: "overview", now: T0, tz: 0 },
      { op: "overview", now: T0, tz: 840 },
      { op: "overview", now: T0, tz: -840 },
      { op: "overview", now: T0, tz: 90.5 },
      { op: "overview", now: "2026-10-17T23:59:59.999Z", tz: 0.001 },
      { op: "accounts", now: T0, ids: ["co-acme", "co-beta", "co-cedar", "co-quiet", "co-echo", "co-zulu", "co-delta", "co-yellow", "co-gamma5", "nope"], of: ["co-acme", "co-beta", "co-cedar", "co-quiet", "co-echo", "co-zulu", "co-delta", "co-yellow", "co-gamma5", "nope", "co-prospect"] },
      { op: "accounts", now: "2026-10-20T15:00:00.000Z", ids: ["co-acme", "co-acme"], of: ["co-acme"] },
      { op: "closed" },
      { op: "progress", now: T0, tz: 0, deals: progressDeals },
      { op: "progress", now: T0, tz: 120, deals: progressDeals },
      { op: "progress", now: T0, tz: -840, deals: progressDeals },
      { op: "lookup", now: T0, addresses },
      { op: "lookupOne", now: T0, email: "ann@acme.example", domain: "" },
      { op: "lookupOne", now: T0, email: "", domain: "zulu.example" },
      { op: "lookupOne", now: T0, email: "lou@gamma.example", domain: "zulu.example" },
      { op: "bare", values: ["https://www.Acme.com/", "  HTTP://WWW.x.example/a/b ", "gmail.com", "GMAIL.COM", "localhost", "", "www.", "a.b/\nc", "acme.com/a\nb", "https://", "www.www.x.example", "x.example?q=1", "yahoo.co.uk", "ftp://x.example"] },
      // The email sync goes off: no people read, sight "off".
      { op: "sql", sql: "UPDATE email_accounts SET enabled = 0" },
      { op: "overview", now: T0, tz: 0 },
      { op: "lookup", now: T0, addresses: [{ email: "ann@acme.example", domain: "" }] },
    ],
  },
  {
    name: "sight states",
    seed: [
      `INSERT INTO companies (id, name, customer_since) VALUES ('co-a', 'Alpha', '2025-01-01'), ('co-b', 'Bravo', '2025-01-01')`,
      `INSERT INTO contacts (id, first_name, email, company_id, last_contacted_at) VALUES
        ('c1', 'One', 'one@a.example', 'co-a', '2026-10-10T00:00:00.000Z'), ('c2', 'Two', 'two@a.example', 'co-a', NULL),
        ('c3', 'Three', 'three@a.example', 'co-a', NULL), ('c4', 'Four', 'Four@B.example', 'co-b', '2026-08-01T00:00:00.000Z')`,
      `INSERT INTO email_contact_imports (mailbox, contact_id, email) VALUES ('m@x.example', 'c4', 'four@b.example')`,
    ],
    steps: [
      { op: "overview", now: T0, tz: 0 },
      { op: "sql", sql: "INSERT INTO meeting_sync (id, enabled, phase) VALUES (1, 0, 'live')" },
      { op: "overview", now: T0, tz: 0 },
      { op: "sql", sql: "UPDATE meeting_sync SET enabled = 1, phase = 'importing'" },
      { op: "sql", sql: "INSERT INTO email_accounts (mailbox, enabled, phase, last_error) VALUES ('m@x.example', 1, 'live', 'boom')" },
      { op: "overview", now: T0, tz: 0 },
      { op: "progress", now: T0, tz: 0, deals: [{ id: "d1", stage: "prospect", company_id: "co-b", contact_id: null, close_date: null }, { id: "d2", stage: "prospect", company_id: null, contact_id: null, close_date: null }] },
      { op: "sql", sql: "UPDATE meeting_sync SET phase = 'live', last_error = 'calendar down'" },
      { op: "sql", sql: "UPDATE email_accounts SET last_error = '', phase = 'idle'" },
      { op: "overview", now: T0, tz: 0 },
      { op: "sql", sql: "UPDATE meeting_sync SET last_error = NULL" },
      { op: "sql", sql: "UPDATE email_accounts SET phase = 'live', blocklist = '[\"@a.example\"]'" },
      { op: "overview", now: T0, tz: 0 },
      { op: "progress", now: T0, tz: 0, deals: [{ id: "d1", stage: "prospect", company_id: "co-a", contact_id: null, close_date: null }, { id: "d2", stage: "prospect", company_id: null, contact_id: null, close_date: null }] },
      { op: "sql", sql: "UPDATE email_accounts SET blocklist = 'not json'" },
      { op: "sql", sql: "INSERT INTO email_accounts (mailbox, enabled, phase) VALUES ('old@x.example', 0, 'live')" },
      { op: "overview", now: T0, tz: 0 },
      { op: "sql", sql: "UPDATE email_accounts SET enabled = 0" },
      { op: "sql", sql: "UPDATE meeting_sync SET enabled = 0" },
      { op: "overview", now: T0, tz: 0 },
      { op: "progress", now: T0, tz: 0, deals: [{ id: "d1", stage: "prospect", company_id: "co-a", contact_id: null, close_date: null }] },
    ],
  },
  {
    name: "a call 46 days ago, in SQLite's format",
    seed: [
      "INSERT INTO meeting_sync (id, enabled, phase) VALUES (1, 1, 'live')",
      `INSERT INTO companies (id, name, customer_since) VALUES ('co-q', 'Quiet', '2025-01-01'), ('co-y', 'Recent', '2025-01-01')`,
      `INSERT INTO meetings (id, title, starts_at, company_id, link_status) VALUES
        ('m1', 'Long ago', '2026-09-01 10:00:00', 'co-q', 'manual'),
        ('m2', 'Lately', '2026-09-02 13:00:00', 'co-y', 'auto')`,
    ],
    steps: [
      { op: "overview", now: T0, tz: 0 },
      { op: "overview", now: "2026-10-17T09:59:59.000Z", tz: 0 },
      { op: "progress", now: T0, tz: 0, deals: [{ id: "d1", stage: "x", company_id: "co-q", contact_id: null, close_date: null }] },
    ],
  },
  {
    name: "focus kinds",
    seed: [
      "INSERT INTO meeting_sync (id, enabled, phase) VALUES (1, 1, 'live')",
      `INSERT INTO companies (id, name, customer_since, renewal_date) VALUES
        ('co-1', 'One', '2025-01-01', '2026-11-01'), ('co-2', 'Two', '2025-01-01', '2026-10-18'),
        ('co-3', 'Three', '2025-01-01', NULL), ('co-4', 'Four', '2025-01-01', '2026-10-17')`,
      `INSERT INTO meetings (id, title, starts_at, company_id, link_status) VALUES ('m1', 'Old', '2026-08-01T00:00:00.000Z', 'co-1', 'manual')`,
      `INSERT INTO tasks (id, title, company_id, owed_by, due_date) VALUES ('t1', 'Today one', 'co-3', 'us', '2026-10-17'), ('t2', 'Today two', 'co-3', 'us', '2026-10-17')`,
      `INSERT INTO insights (id, company_id, kind, text) VALUES ('i1', 'co-4', 'risk', 'Watch out')`,
    ],
    steps: [
      { op: "overview", now: T0, tz: 0 },
      { op: "sql", sql: "DELETE FROM meetings" },
      { op: "sql", sql: "DELETE FROM tasks" },
      { op: "overview", now: T0, tz: 0 },
    ],
  },
  {
    name: "an unreadable touch and an unreadable next meeting",
    seed: [
      `INSERT INTO email_accounts (mailbox, enabled, phase) VALUES ('m@x.example', 1, 'live')`,
      `INSERT INTO companies (id, name, customer_since) VALUES ('co-n', 'Nan', '2025-01-01'), ('co-r', 'Red', '2025-01-01')`,
      `INSERT INTO contacts (id, first_name, email, company_id, last_contacted_at) VALUES ('c1', 'Nina', 'nina@n.example', 'co-n', 'not a date')`,
      `INSERT INTO email_contact_imports (mailbox, contact_id, email) VALUES ('m@x.example', 'c1', 'nina@n.example')`,
      `INSERT INTO insights (id, company_id, kind, text) VALUES ('i1', 'co-r', 'risk', 'Bad')`,
      `INSERT INTO meetings (id, title, starts_at, company_id, link_status) VALUES ('m1', 'When?', 'garbage', 'co-n', 'auto')`,
    ],
    steps: [
      { op: "overview", now: T0, tz: 0 },
      { op: "accounts", now: T0, ids: ["co-n"], of: ["co-n"] },
      { op: "progress", now: T0, tz: 0, deals: [{ id: "d1", stage: "x", company_id: "co-r", contact_id: null, close_date: null }] },
      { op: "progress", now: T0, tz: 0, deals: [{ id: "d1", stage: "x", company_id: "co-n", contact_id: null, close_date: null }] },
      { op: "lookup", now: T0, addresses: [{ email: "nina@n.example", domain: "" }] },
    ],
  },
  {
    name: "past one IN list",
    seed: [
      "INSERT INTO meeting_sync (id, enabled, phase) VALUES (1, 1, 'live')",
      "INSERT INTO email_accounts (mailbox, enabled, phase) VALUES ('m@x.example', 1, 'live')",
      "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 95) INSERT INTO companies (id, name, domain, customer_since) SELECT printf('co-%03d', i), printf('Co %03d', 96 - i), printf('c%d.example', i), '2025-01-01' FROM n",
      "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 95) INSERT INTO contacts (id, first_name, email, company_id) SELECT printf('ct-%03d', i), printf('P%d', i), printf('p%d@c%d.example', i, i), printf('co-%03d', i) FROM n",
      "INSERT INTO meetings (id, title, starts_at, company_id, link_status) VALUES ('m1', 'Last', '2026-10-01T00:00:00.000Z', 'co-095', 'auto'), ('m2', 'Next', '2026-10-18T00:00:00.000Z', 'co-095', 'auto')",
    ],
    steps: [
      { op: "overview", now: T0, tz: 0 },
      { op: "lookup", now: T0, addresses: Array.from({ length: 100 }, (_, i) => ({ email: `p${i}@c${i}.example`, domain: i % 2 ? "" : `c${i}.example` })) },
      {
        op: "progress", now: T0, tz: 0,
        deals: Array.from({ length: 95 }, (_, i) => ({ id: `d${i}`, stage: "prospect", company_id: null, contact_id: `ct-${String(i + 1).padStart(3, "0")}`, close_date: null })),
      },
    ],
  },
];

// ── Running them ───────────────────────────────────────────────────

const out = [];
for (const sc of scenarios) {
  sqlite = new DatabaseSync(":memory:");
  sqlite.exec("PRAGMA foreign_keys = ON");
  sqlite.exec(schema);
  for (const s of sc.seed) sqlite.exec(s);
  statements = [];
  const results: string[] = [];
  for (const st of sc.steps) {
    let r: unknown;
    try {
      switch (st.op) {
        case "overview": r = await customers.customersOverview(new Date(st.now), st.tz); break;
        case "accounts": {
          const a = await customers.readAccounts(st.ids, new Date(st.now));
          // What of() says, with each mood and insight as the fields read from it.
          r = {
            of: st.of.map((id) => {
              const x = a.of(id);
              return {
                ...x,
                moods: x.moods.map((m: Record<string, unknown>) => ({ sentiment: m.sentiment, sentiment_reason: m.sentiment_reason, summary: m.summary })),
                insights: x.insights.map((i: Record<string, unknown>) => ({ kind: i.kind, text: i.text })),
              };
            }),
            calls: a.calls,
            emails: a.emails,
          };
          break;
        }
        case "progress": {
          const p = await progress.dealsProgress(st.deals, new Date(st.now), st.tz);
          r = { progress: Object.fromEntries(p.progress), sight: p.sight };
          break;
        }
        case "closed": r = [...(await progress.closedStages())]; break;
        case "lookup": r = await lookup.lookupMany(st.addresses, new Date(st.now)); break;
        case "lookupOne": r = await lookup.lookup(st.email, st.domain, new Date(st.now)); break;
        case "bare": r = st.values.map(lookup.bareDomain); break;
        case "sql": sqlite.exec(st.sql); r = null; break;
      }
    } catch (e) {
      r = { error: (e as Error).message };
    }
    results.push(JSON.stringify(r ?? null));
  }
  out.push({ ...sc, expect: { results, statements } });
}
process.stdout.write(JSON.stringify(out, null, 1) + "\n");
