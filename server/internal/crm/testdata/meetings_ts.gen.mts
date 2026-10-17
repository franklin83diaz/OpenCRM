// Generates meetings_ts.json: meeting sync scenarios (seed, fake calendar /
// Granola / model rules, steps) and what the TS meetings.ts at 0068887 did
// with each — every step's result, every call to the fakes, every SQL
// statement with its params, and the tables afterwards. meetings_ts_test.go
// replays each scenario through the Go port and wants the same.
//
// Run from the repository root (needs node 22 and the repo's node_modules):
//
//	npx tsx server/internal/crm/testdata/meetings_ts.gen.mts [ts-src-dir] > server/internal/crm/testdata/meetings_ts.json
//
// ts-src-dir defaults to src/server, else the parity harness's reference
// checkout (.parity/ts-ref/src/server), which stays at 0068887 after the TS
// backend is deleted. @clawnify/db runs over node:sqlite; fetch is replaced by
// the fakes below (the same rules meetings_ts_test.go answers with); UUIDs
// come in creation order.

import { DatabaseSync } from "node:sqlite";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";

const src = path.resolve(process.argv[2] ?? (existsSync("src/server/meetings.ts") ? "src/server" : ".parity/ts-ref/src/server"));
const schema = readFileSync("server/schema.sql", "utf8"); // the schema the Go tests use

let uuid = 0;
Object.defineProperty(globalThis.crypto, "randomUUID", {
  value: () => `00000000-0000-4000-8000-${String(++uuid).padStart(12, "0")}`,
  configurable: true,
});

// ── The fakes ──────────────────────────────────────────────────────

type Rule = { once?: boolean; used?: boolean; [k: string]: unknown };
interface Rules {
  execute?: Array<Rule & { action: string; args?: Record<string, unknown>; data?: unknown; error?: string }>;
  granola?: Array<Rule & { path: string; query?: Record<string, string | null>; status?: number; data?: unknown }>;
  model?: Array<Rule & { userHas?: string; status?: number; body?: unknown }>;
}

const canonical = (v: unknown): string => JSON.stringify(v, (_k, x) =>
  x && typeof x === "object" && !Array.isArray(x) ? Object.fromEntries(Object.entries(x).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))) : x);

function subset(want: Record<string, unknown> | undefined, got: Record<string, unknown>): boolean {
  for (const [k, v] of Object.entries(want ?? {})) {
    if (v === null) {
      if (k in got) return false;
    } else if (canonical(got[k]) !== canonical(v)) return false;
  }
  return true;
}

function pick<T extends Rule>(rules: T[] | undefined, match: (r: T) => boolean): T | undefined {
  const r = (rules ?? []).find((x) => !x.used && match(x));
  if (r?.once) r.used = true;
  return r;
}

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

let state: { connected: string[]; rules: Rules; calls: unknown[] };

async function fakeFetch(input: string | URL | Request, init?: RequestInit): Promise<Response> {
  const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url);
  const body = init?.body ? JSON.parse(String(init.body)) : undefined;
  if (url.host === "platform.test") {
    switch (url.pathname) {
      case "/v1/connections/connected":
        return json(200, { services: state.connected });
      case "/v1/connections/token":
        return json(200, { token: "granola-key" });
      case "/v1/connections/execute": {
        state.calls.push({ execute: { service: body.service, action: body.action, args: body.args } });
        const r = pick(state.rules.execute, (x) => x.action === body.action && subset(x.args, body.args));
        if (!r) return json(200, { data: null, error: `no rule for ${body.action}`, successful: false });
        return json(200, r.error ? { data: null, error: r.error, successful: false } : { data: r.data, error: null, successful: true });
      }
      case "/v1/chat/completions": {
        state.calls.push({ model: body });
        const user = body.messages?.[1]?.content ?? "";
        const r = pick(state.rules.model, (x) => !x.userHas || user.includes(x.userHas));
        if (!r) return json(500, { error: { message: "no model rule" } });
        return json(r.status ?? 200, r.body);
      }
    }
  }
  if (url.host === "public-api.granola.ai") {
    const query = Object.fromEntries(url.searchParams);
    state.calls.push({ granola: { path: url.pathname, query } });
    const r = pick(state.rules.granola, (x) => x.path === url.pathname && subset(x.query, query));
    if (!r) return json(404, { error: "no rule" });
    return json(r.status ?? 200, r.data);
  }
  throw new Error(`unexpected fetch ${url}`);
}
globalThis.fetch = fakeFetch as typeof fetch;

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
const meetings = await import(path.join(src, "meetings.ts"));
initDB({ DB: d1 });

const env = { CLAWNIFY_TOKEN: "tok", CLAWNIFY_API_URL: "http://platform.test" };

// index.ts's meetingSyncView, for the settings a save returns.
function meetingSyncView(s: Record<string, unknown> | null) {
  if (!s) return null;
  return {
    enabled: !!s.enabled,
    history_days: s.history_days,
    about: s.about,
    calendar_owner: s.calendar_owner,
    phase: s.phase,
    calendar_synced_at: s.calendar_synced_at,
    last_run_at: s.last_run_at,
    last_error: s.last_error,
    next_run_at: s.next_run_at,
    updated_by: s.updated_by,
    updated_at: s.updated_at,
  };
}

// ── Scenarios ──────────────────────────────────────────────────────

type IdRef = { calendar_event_id?: string; note_id?: string; id?: string };
type Step =
  | { op: "run"; now: string; sync?: boolean }
  | { op: "link"; ref: IdRef; to: { company_id: string | null } | { ignored: true } }
  | { op: "retry"; ref: IdRef }
  | { op: "get"; ref: IdRef }
  | { op: "list"; now: string; opts: Record<string, unknown> }
  | { op: "counts" }
  | { op: "relink" }
  | { op: "save"; patch: Record<string, unknown>; who: string | null }
  | { op: "connected"; services: string[] }
  | { op: "sql"; sql: string };

interface Scenario {
  name: string;
  connected: string[];
  seed: string[];
  rules: Rules;
  steps: Step[];
  dumps: string[];
}

const T0 = "2026-10-14T12:00:00.000Z";
const at = (min: number) => new Date(Date.parse(T0) + min * 60_000).toISOString();

const people = [
  "INSERT INTO companies (id, name, domain, created_at) VALUES ('co-acme', 'Acme', 'https://www.Acme.example/about', '2026-01-01 00:00:01'), ('co-beta', 'Beta', '', '2026-01-01 00:00:02'), ('co-gamma', 'Gamma', 'gamma.example', '2026-01-01 00:00:03'), ('co-gamma2', 'Gamma Two', 'WWW.gamma.example', '2026-01-01 00:00:04'), ('co-empty', 'Empty', NULL, '2026-01-01 00:00:05')",
  "INSERT INTO contacts (id, first_name, email, company_id) VALUES ('ct-bea', 'Bea', ' Bea@Beta.example ', 'co-beta'), ('ct-bo', 'Bo', 'bo@beta.example', 'co-beta'), ('ct-pat', 'Pat', 'pat@gmail.com', 'co-gamma'), ('ct-gus', 'Gus', 'gus@beta.example', 'co-gamma'), ('ct-none', 'None', '', 'co-acme'), ('ct-free', 'Free', 'free@nowhere.example', NULL)",
];

const event = (id: string, start: string, end: string | null, attendees: unknown[], extra: Record<string, unknown> = {}) => ({
  id, status: "confirmed", summary: ` ${id} call `, htmlLink: `https://calendar.google.com/event?eid=${id}`,
  start: { dateTime: start }, ...(end ? { end: { dateTime: end } } : {}), attendees, ...extra,
});
const me = { email: "me@mine.example", self: true, responseStatus: "accepted" };

const reply = (content: unknown) => ({ choices: [{ message: { content: typeof content === "string" ? content : JSON.stringify(content) } }] });

const scenarios: Scenario[] = [
  {
    name: "first import, digests and links",
    connected: ["googlesuper", "granola"],
    seed: [...people, "INSERT INTO meeting_sync (id, enabled, history_days, about, phase) VALUES (1, 1, 30, ' We sell CRMs ', 'idle')"],
    rules: {
      execute: [
        {
          action: "GOOGLESUPER_EVENTS_LIST", args: { pageToken: null },
          data: {
            summary: " Me@Mine.example ", nextPageToken: "p2",
            items: [
              event("e1", "2026-10-13T10:00:00+02:00", "2026-10-13T11:00:00+02:00", [me, { email: "Bob@Acme.example", displayName: " Bob " }, { email: "room@resource.calendar.google.com", resource: true }, { email: "colleague@mine.example" }]),
              event("e2", "2026-10-13T15:00:00Z", "2026-10-13T15:30:00Z", [me, { email: "colleague@mine.example" }]),
              event("e3", "2026-10-13T16:00:00Z", null, [me, { email: "x@acme.example" }], { status: "cancelled" }),
              { id: "e4", status: "confirmed", summary: "All day", start: { date: "2026-10-15" }, end: { date: "2026-10-16" } },
              event("e5", "2026-10-13T17:00:00Z", null, [{ ...me, responseStatus: "declined" }, { email: "x@acme.example" }]),
              event("e6", "2026-10-13T18:00:00Z", null, [me, { email: "x@acme.example" }], { eventType: "focusTime" }),
              event("", "2026-10-13T18:00:00Z", null, [me, { email: "x@acme.example" }]),
            ],
          },
        },
        {
          action: "GOOGLESUPER_EVENTS_LIST", args: { pageToken: "p2" },
          data: {
            items: [
              event("e7", "2026-10-11T09:00:00Z", "2026-10-11T09:45:00Z", [{ email: "bea@beta.example" }, { email: "new@beta.example", displayName: "New" }]),
              event("e8", "2026-10-09T13:00:00Z", "2026-10-09T14:00:00Z", [{ email: "someone@unknown.example" }, { email: "pat-friend@gmail.com" }]),
              event("e9", "2026-10-20T09:00:00Z", "2026-10-20T10:00:00Z", [{ email: "x@gamma.example", displayName: "X" }]),
              event("e10", "2026-10-21T09:00:00Z", "bad", [{ email: "Pat@Gmail.com" }]),
            ],
          },
        },
      ],
      granola: [
        {
          path: "/v1/notes", query: { cursor: null },
          data: { notes: [{ id: "n1", updated_at: "2026-10-13T09:30:00Z" }, { id: "n2", updated_at: "2026-10-12T15:00:00Z" }, { id: "n3", updated_at: "2026-10-11T08:00:00Z" }, { id: "n4", updated_at: "2026-10-10T08:00:00Z" }], hasMore: true, cursor: "c2" },
        },
        { path: "/v1/notes", query: { cursor: "c2" }, data: { notes: [{ id: "n5" }], hasMore: false, cursor: null } },
        {
          path: "/v1/notes/n1", query: { include: "transcript" },
          data: {
            id: "n1", title: "Acme weekly notes", web_url: "https://notes.granola.ai/d/n1", updated_at: "2026-10-13T09:30:00Z",
            calendar_event: { calendar_event_id: "e1" },
            transcript: [
              { text: "We'll send the proposal by Friday", start_time: "2026-10-13T08:02:00Z", end_time: "2026-10-13T08:03:00Z", speaker: { source: "microphone" } },
              { text: "  ", start_time: "2026-10-13T08:03:00Z", end_time: "2026-10-13T08:03:10Z", speaker: { source: "speaker" } },
              { text: "Our finance team wants this too", start_time: "2026-10-13T08:04:00Z", end_time: "2026-10-13T08:05:00Z", speaker: { source: "speaker", name: "Bob" } },
              { text: "And legal", start_time: "2026-10-13T08:05:00Z", end_time: "2026-10-13T08:06:00Z", speaker: { source: "speaker", name: "Bob" } },
            ],
            summary_markdown: " ## Proposal\n- by Friday ",
          },
        },
        {
          path: "/v1/notes/n2",
          data: {
            id: "n2", title: null, owner: { email: "me@mine.example" },
            attendees: [{ email: "me@mine.example", name: "Me" }, { email: "Bo@beta.example", name: "Bo" }],
            calendar_event: { event_title: " Beta call ", scheduled_start_time: "2026-10-12T14:00:00Z", scheduled_end_time: "2026-10-12T14:30:00Z", invitees: [{ email: "lee@beta.example", name: null }] },
            transcript: [{ text: "All good on our side", start_time: "2026-10-12T14:01:00Z", end_time: "2026-10-12T14:02:00Z", speaker: { source: "microphone" } }],
            summary_text: "Beta is happy",
          },
        },
        {
          path: "/v1/notes/n3",
          data: { id: "n3", title: "Untitled call", created_at: "2026-10-09T12:00:00Z", web_url: "https://notes.granola.ai/d/n3", transcript: [{ text: "hello", start_time: "2026-10-09T13:20:00Z", end_time: "2026-10-09T13:50:00Z" }] },
        },
        { path: "/v1/notes/n4", data: { id: "n4", title: "No time" } },
        { path: "/v1/notes/n5", data: { id: "n5", title: "Team sync", created_at: "2026-10-13T15:10:00Z", updated_at: "2026-10-13T16:00:00Z" } },
      ],
      model: [
        {
          userHas: "Call: e1 call", once: true,
          body: reply(`Sure: ${JSON.stringify({
            summary: "  Acme   wants a proposal. ", sentiment: 1.6, sentiment_reason: " Upbeat ",
            tasks: [
              { title: "Send the proposal", owed_by: "us", due_date: "2026-10-16", quote: "We'll send the proposal by Friday" },
              { title: " send the PROPOSAL ", owed_by: "us" },
              { title: "Share pricing", owed_by: "them", due_date: "2027-12-01", quote: 5 },
            ],
            insights: [
              { kind: "expansion", text: "Finance team wants it", quote: "Our finance team wants this too" },
              { kind: "risk", text: "Budget is tight", quote: null },
              { kind: "bogus", text: "x" },
            ],
          })} — done`),
        },
        { userHas: "Call: Beta call", once: true, status: 402, body: { error: { message: "Payment required" } } },
        { userHas: "Call: Beta call", once: true, status: 429, body: { error: { message: "Rate limited" } } },
        { userHas: "Call: Beta call", once: true, body: reply("not json") },
        { userHas: "Call: Beta call", once: true, body: reply("null") },
        { userHas: "Call: Beta call", once: true, body: reply({ summary: "Old call", sentiment: -3, tasks: [{ title: "Too late" }], insights: [{ kind: "risk", text: "Old risk" }, { kind: "idea", text: "An idea" }] }) },
        { userHas: "Call: e8 call", body: reply({ summary: "Unknown people", sentiment: "x" }) },
      ],
    },
    steps: [
      { op: "run", now: T0 },
      { op: "counts" },
      { op: "run", now: at(1) },
      { op: "run", now: at(2) },
      { op: "retry", ref: { note_id: "n2" } },
      { op: "retry", ref: { note_id: "n2" } },
      { op: "run", now: at(3) },
      { op: "retry", ref: { note_id: "n2" } },
      { op: "run", now: at(4) },
      { op: "retry", ref: { note_id: "n2" } },
      { op: "run", now: at(5) },
      { op: "retry", ref: { note_id: "n2" } },
      { op: "run", now: "2026-11-30T12:00:00.000Z" },
      { op: "run", now: "2026-11-30T12:01:00.000Z" },
      { op: "retry", ref: { note_id: "n2" } },
      { op: "run", now: at(7) },
      { op: "retry", ref: { calendar_event_id: "e2" } },
      { op: "retry", ref: { id: "nope" } },
      { op: "link", ref: { calendar_event_id: "e8" }, to: { company_id: "co-beta" } },
      { op: "run", now: at(8) },
      { op: "get", ref: { calendar_event_id: "e1" } },
      { op: "get", ref: { calendar_event_id: "e2" } },
      { op: "list", now: T0, opts: {} },
      { op: "list", now: T0, opts: { when: "upcoming" } },
      { op: "list", now: T0, opts: { when: "past", limit: 2, offset: 1 } },
      { op: "list", now: T0, opts: { company_id: "co-beta" } },
      { op: "list", now: T0, opts: { link: "unmatched" } },
      { op: "list", now: T0, opts: { link: "ignored" } },
      { op: "list", now: T0, opts: { limit: 0 } },
      { op: "list", now: T0, opts: { limit: 1000, offset: -5 } },
      { op: "counts" },
      { op: "sql", sql: "UPDATE tasks SET done_at = '2026-10-14T13:00:00.000Z' WHERE title = 'Send the proposal'" },
      { op: "sql", sql: "UPDATE insights SET status = 'done' WHERE text = 'Finance team wants it'" },
      { op: "link", ref: { calendar_event_id: "e1" }, to: { ignored: true } },
      { op: "link", ref: { calendar_event_id: "e1" }, to: { company_id: null } },
      { op: "relink" },
      { op: "link", ref: { calendar_event_id: "e2" }, to: { company_id: "co-acme" } },
      { op: "link", ref: { id: "nope" }, to: { ignored: true } },
      { op: "link", ref: { calendar_event_id: "e7" }, to: { company_id: "co-empty" } },
      { op: "sql", sql: "DELETE FROM companies WHERE id = 'co-gamma'" },
      { op: "relink" },
      { op: "list", now: T0, opts: { link: "unmatched" } },
      { op: "counts" },
    ],
    dumps: [
      "SELECT calendar_event_id, note_id, title, starts_at, ends_at, attendees, company_id, link_status, calendar_url, note_url, note_updated_at, summary, sentiment, sentiment_reason, digest_status, digest_error, digested_at FROM meetings ORDER BY starts_at, calendar_event_id",
      "SELECT t.title, t.company_id, m.calendar_event_id, m.note_id, t.owed_by, t.due_date, t.quote, t.created_by, t.done_at FROM tasks t LEFT JOIN meetings m ON m.id = t.meeting_id ORDER BY t.title",
      "SELECT i.kind, i.text, i.quote, i.company_id, m.calendar_event_id, i.status FROM insights i LEFT JOIN meetings m ON m.id = i.meeting_id ORDER BY i.text",
      "SELECT id, enabled, history_days, about, calendar_owner, phase, cursor, calendar_synced_at, notes_synced_until, last_run_at, last_error, running_until, job_id, next_run_at FROM meeting_sync",
      "SELECT id, domain FROM companies ORDER BY id",
    ],
  },
  {
    name: "live reads, prune, errors and resuming",
    connected: ["googlecalendar"],
    seed: [
      ...people,
      "INSERT INTO meeting_sync (id, enabled, history_days, about, calendar_owner, phase, calendar_synced_at, notes_synced_until) VALUES (1, 1, 90, '', 'me@mine.example', 'live', '2026-10-14T11:00:00.000Z', '2026-10-14T11:00:00.000Z')",
      `INSERT INTO meetings (id, calendar_event_id, note_id, title, starts_at, ends_at, attendees, company_id, link_status, note_updated_at, digest_status, updated_at) VALUES
        ('m-keep', 'k1', NULL, 'Kept', '2026-10-15T09:00:00.000Z', NULL, '[{"email":"bea@beta.example","name":null}]', 'co-beta', 'manual', NULL, 'none', '2026-10-01 00:00:00'),
        ('m-ign', 'k2', NULL, 'Ignored', '2026-10-15T10:00:00.000Z', NULL, '[]', NULL, 'ignored', NULL, 'none', '2026-10-01 00:00:00'),
        ('m-gone', 'g1', NULL, 'Gone', '2026-10-13T09:00:00.000Z', NULL, '[]', NULL, 'unmatched', NULL, 'none', '2026-10-01 00:00:00'),
        ('m-noted', 'g2', 'nn', 'Noted', '2026-10-13T10:00:00.000Z', NULL, '[]', 'co-acme', 'auto', '2026-10-13T11:00:00Z', 'done', '2026-10-01 00:00:00'),
        ('m-out', 'o1', NULL, 'Outside', '2026-09-01T10:00:00.000Z', NULL, '[]', NULL, 'unmatched', NULL, 'none', '2026-10-01 00:00:00'),
        ('m-stale', NULL, 'ns', 'Stale', '2026-10-12T10:00:00.000Z', NULL, '[{"email":"bob@acme.example","name":"Bob"}]', 'co-acme', 'auto', 'x', 'running', '2000-01-01 00:00:00'),
        ('m-nocomp', NULL, 'nc', 'No company', '2026-10-11T10:00:00.000Z', NULL, '[]', NULL, 'auto', 'x', 'queued', '2026-10-01 00:00:00')`,
    ],
    rules: {
      execute: [
        { action: "GOOGLECALENDAR_EVENTS_LIST", args: { timeMin: "2026-10-12T12:05:00.000Z" }, once: true, error: "Calendar quota" },
        { action: "GOOGLECALENDAR_EVENTS_LIST",
          data: { items: [
            event("k1", "2026-10-15T09:00:00Z", "2026-10-15T09:30:00Z", [me, { email: "bob@acme.example" }]),
            event("k2", "2026-10-15T10:00:00Z", null, [me, { email: "bob@acme.example" }]),
            event("new1", "2026-10-16T10:00:00Z", null, [{ email: "me@mine.example" }, { email: "bob@acme.example" }]),
          ] } },
      ],
      granola: [
        { path: "/v1/notes", query: { updated_after: "2026-10-14T10:00:00.000Z", cursor: null },
          data: { notes: [{ id: "nn", updated_at: "2026-10-13T11:00:00Z" }, { id: "bad", updated_at: "2027-01-01T00:00:00Z" }, { id: "late", updated_at: "2026-10-14T11:30:00Z" }], hasMore: false } },
        { path: "/v1/notes/bad", once: true, status: 500, data: "upstream down" },
        { path: "/v1/notes/bad", data: { id: "bad", title: "Bad", transcript: [{ text: "hi", start_time: "2026-10-16T10:05:00Z", end_time: "2026-10-16T10:20:00Z" }] } },
        { path: "/v1/notes/late", data: { id: "late", updated_at: "2026-10-14T11:30:00Z", calendar_event: { scheduled_start_time: "2026-10-01T10:00:00Z" }, attendees: [{ email: "zed@zed.example", name: "Zed" }] } },
        { path: "/v1/notes", data: { notes: [], hasMore: false } },
      ],
    },
    steps: [
      { op: "run", now: T0 },
      { op: "run", now: at(1) },
      { op: "run", now: at(2) },
      { op: "connected", services: ["googlecalendar", "granola"] },
      { op: "run", now: at(3), sync: true },
      { op: "run", now: at(4) },
      { op: "connected", services: ["granola"] },
      { op: "run", now: at(5), sync: true },
      { op: "connected", services: ["googlecalendar", "granola"] },
      { op: "run", now: at(6) },
      { op: "run", now: at(7) },
      { op: "list", now: T0, opts: { when: "upcoming", limit: 10 } },
      { op: "sql", sql: "UPDATE meeting_sync SET running_until = '2026-10-14T12:30:00.000Z'" },
      { op: "run", now: at(8) },
      { op: "sql", sql: "UPDATE meeting_sync SET enabled = 0" },
      { op: "run", now: at(40) },
      { op: "counts" },
    ],
    dumps: [
      "SELECT id, calendar_event_id, note_id, title, starts_at, ends_at, attendees, company_id, link_status, calendar_url, note_url, note_updated_at, digest_status, digest_error FROM meetings ORDER BY starts_at, calendar_event_id",
      "SELECT id, enabled, history_days, about, calendar_owner, phase, cursor, calendar_synced_at, notes_synced_until, last_run_at, last_error, running_until FROM meeting_sync",
    ],
  },
  {
    name: "edges: ties, the owner from the calendar, freshness and long errors",
    connected: ["googlecalendar", "granola"],
    seed: [
      "INSERT INTO companies (id, name, domain, created_at) VALUES ('co-a', 'A', '', '2026-01-01 00:00:01'), ('co-b', 'B', NULL, '2026-01-01 00:00:02')",
      "INSERT INTO contacts (id, first_name, email, company_id) VALUES ('c1', 'One', 'one@tie.example', 'co-b'), ('c2', 'Two', 'two@tie.example', 'co-a'), ('c3', 'Three', 'three@two.example', 'co-a'), ('c4', 'Four', 'four@two.example', 'co-b'), ('c5', 'Five', 'five@two.example', 'co-b')",
      "INSERT INTO meeting_sync (id, enabled, history_days, about, phase) VALUES (1, 1, 90, '', 'idle')",
    ],
    rules: {
      execute: [{
        action: "GOOGLECALENDAR_EVENTS_LIST",
        data: { items: [
          event("t1", "2026-10-10T09:00:00Z", "2026-10-10T10:00:00Z", [{ email: "Me@Mine.example", self: true }, { email: "x@tie.example" }]),
          event("t2", "2026-10-09T09:00:00Z", null, [{ email: "y@two.example" }, { email: "colleague@mine.example" }]),
          event("old", "2026-09-30T12:00:00Z", "2026-09-30T13:00:00Z", [{ email: "z@tie.example" }]),
        ] },
      }],
      granola: [
        { path: "/v1/notes", once: true, data: { notes: [{ id: "nt1", updated_at: "2026-10-10T11:00:00Z" }, { id: "nold", updated_at: "2026-10-10T11:00:00.000Z" }, { id: "nx", updated_at: "garbage" }], hasMore: false, cursor: "zz" } },
        { path: "/v1/notes/nt1", data: { id: "nt1", calendar_event: { calendar_event_id: "t1" }, created_at: "2026-10-10T09:00:00Z" } },
        { path: "/v1/notes/nold", data: { id: "nold", calendar_event: { calendar_event_id: "old" }, created_at: "2026-09-30T12:00:00Z", transcript: [] } },
        { path: "/v1/notes/nx", data: { id: "nx", created_at: "not a date" } },
      ],
      model: [
        { userHas: "Call: t1 call", status: 500, body: { error: { message: "Upstream failed: " + "x".repeat(400) } } },
        { userHas: "Call: old call", body: reply({ summary: "On the edge", sentiment: -0.5, sentiment_reason: "meh", tasks: [{ title: "Edge task", owed_by: "them", due_date: "2026-09-29" }], insights: [{ kind: "risk", text: "Edge risk", quote: "q" }] }) },
      ],
    },
    steps: [
      { op: "run", now: T0 },
      { op: "run", now: T0 },
      { op: "run", now: T0 },
      { op: "run", now: T0 },
    ],
    dumps: [
      "SELECT calendar_event_id, note_id, title, attendees, company_id, link_status, note_updated_at, summary, sentiment, sentiment_reason, digest_status, digest_error FROM meetings ORDER BY starts_at, calendar_event_id",
      "SELECT t.title, t.company_id, t.owed_by, t.due_date, t.quote FROM tasks t ORDER BY t.title",
      "SELECT i.kind, i.text, i.quote, i.company_id FROM insights i ORDER BY i.text",
      "SELECT calendar_owner, phase, cursor, calendar_synced_at, notes_synced_until, last_error FROM meeting_sync",
    ],
  },
  {
    name: "settings",
    connected: [],
    seed: [],
    rules: {},
    steps: [
      { op: "run", now: T0 },
      { op: "save", patch: {}, who: null },
      { op: "save", patch: { enabled: true, history_days: 30 }, who: "ada@example.com" },
      { op: "sql", sql: "UPDATE meeting_sync SET phase = 'live', cursor = '{\"step\":\"notes\"}', calendar_synced_at = 'x', notes_synced_until = 'y', next_run_at = 'z'" },
      { op: "save", patch: { history_days: 30, about: "We sell" }, who: "user-1" },
      { op: "save", patch: { history_days: 365 }, who: "ada@example.com" },
      { op: "sql", sql: "UPDATE meeting_sync SET phase = 'live', running_until = NULL" },
      { op: "save", patch: { enabled: false }, who: "ada@example.com" },
      { op: "save", patch: { enabled: true }, who: "ada@example.com" },
      { op: "sql", sql: "UPDATE meeting_sync SET cursor = '{\"step\":\"calendar\",\"from\":\"2026-10-01T00:00:00.000Z\",\"to\":\"2026-10-30T00:00:00.000Z\",\"page\":\"tok\"}'" },
      { op: "run", now: T0 },
      { op: "sql", sql: "UPDATE meeting_sync SET cursor = '[1]', running_until = NULL, phase = 'live', calendar_synced_at = '2026-10-14T11:50:00.000Z'" },
      { op: "run", now: T0 },
      { op: "sql", sql: "UPDATE meeting_sync SET cursor = 'not json', running_until = NULL, calendar_synced_at = 'garbage'" },
      { op: "run", now: T0 },
    ],
    dumps: ["SELECT id, enabled, history_days, about, phase, cursor, calendar_synced_at, notes_synced_until, last_run_at, last_error, next_run_at, updated_by FROM meeting_sync"],
  },
];

// ── Running them ───────────────────────────────────────────────────

const out = [];
for (const sc of scenarios) {
  sqlite = new DatabaseSync(":memory:");
  sqlite.exec("PRAGMA foreign_keys = ON");
  sqlite.exec(schema);
  for (const s of sc.seed) sqlite.exec(s);
  uuid = 0;
  statements = [];
  state = { connected: [...sc.connected], rules: structuredClone(sc.rules), calls: [] };
  const results: string[] = [];
  const idOf = (ref: IdRef) => {
    if (ref.id) return ref.id;
    const [col, v] = Object.entries(ref)[0];
    return (sqlite.prepare(`SELECT id FROM meetings WHERE ${col} = ?`).get(v as string) as { id: string } | undefined)?.id ?? "missing";
  };
  for (const st of sc.steps) {
    let r: unknown;
    switch (st.op) {
      case "run": r = await meetings.runMeetings(env, new Date(st.now), { sync: st.sync }); break;
      case "link": r = await meetings.linkMeeting(idOf(st.ref), st.to); break;
      case "retry": r = await meetings.retryDigest(idOf(st.ref)); break;
      case "get": r = await meetings.getMeeting(idOf(st.ref)); break;
      case "list": r = await meetings.listMeetings(st.opts, new Date(st.now)); break;
      case "counts": r = await meetings.counts(); break;
      case "relink": r = await meetings.relinkUnmatched(); break;
      case "save": {
        const { before, after } = await meetings.saveSettings(st.patch, st.who);
        r = { before: meetingSyncView(before), after: meetingSyncView(after) };
        break;
      }
      case "connected": state.connected = st.services; r = null; break;
      case "sql": sqlite.exec(st.sql); r = null; break;
    }
    results.push(JSON.stringify(r ?? null));
  }
  const dumps = sc.dumps.map((q) => sqlite.prepare(q).all().map((row) => ({ ...row })));
  out.push({ ...sc, expect: { results, calls: state.calls, statements, dumps } });
}
process.stdout.write(JSON.stringify(out, null, 1) + "\n");
