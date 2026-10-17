// Deals area: pipeline stages (list, create, update, delete with
// ?reassign_to), deals (board, progress, list, create, update, delete, and the
// won/lost side effects: the timeline and the company becoming a customer),
// tasks, deal updates (POST /api/deals/{id}/updates), and the records read by
// id (GET /api/contacts|deals|companies/{id}), records and values for
// pickers. Later calls use the ids earlier creates returned (ids are numbered
// per scenario by first appearance).
//
// Not ported, so not called here: the insights routes and the Slack message
// on a won deal (the port leaves integrations and AI out).
//
// Scenarios run in file order on one database, after companies-contacts.mjs
// and customers.mjs and before the others, so what is created here is deleted
// again and seed rows that are changed get their values back.
//
// New rows get random UUIDs on each side, so a list that would order them by
// id (the deals list's default sort, or a tie) differs between the two
// backends: lists that can see created deals sort on a unique column or are
// narrowed by search. Ties on the seed (fixed ids) are fine.
//
// No route creates meetings, insights or custom fields, so tasks from calls,
// a progress with meetings and risks, custom fields on deals (422/400/enum
// 500), the last stage and an empty pipeline, Date.parse on a deal update's
// `at` at a fixed time, and database errors (D1's words in TS, SQLite's here)
// are the TS-replay test in server/internal/crm (deals_ts_test.go).

const enc = encodeURIComponent;
const qs = (o) =>
  "?" +
  Object.entries(o)
    .map(([k, v]) => `${enc(k)}=${enc(typeof v === "string" ? v : JSON.stringify(v))}`)
    .join("&");
const idOf = (res, key) => res.body?.[key]?.id ?? `missing-${key}`;

const user = { "X-Clawnify-Caller": "user", "X-Clawnify-User-Id": "user-1", "X-Clawnify-User-Email": "ada@example.com" };
const userNoEmail = { "X-Clawnify-Caller": "user", "X-Clawnify-User-Id": "user-2" };
const agent = { "X-Clawnify-Caller": "agent" };
const api = { "X-Clawnify-Caller": "api" };
const pub = { "X-Clawnify-Caller": "public" };

// Computed once, so both backends are sent the same values. Times are in the
// past (a touch a minute ahead would read as -1 days quiet on one side and 0
// on the other).
const DAY = 86_400_000;
const today = new Date().toISOString().slice(0, 10);
const tomorrow = new Date(Date.now() + DAY).toISOString().slice(0, 10);
const twoHoursAgo = new Date(Date.now() - 2 * 3_600_000).toISOString();
const inTwoHours = new Date(Date.now() + 2 * 3_600_000).toISOString();
const nextWeek = new Date(Date.now() + 7 * DAY).toISOString().slice(0, 10);
const lastWeek = new Date(Date.now() - 7 * DAY).toISOString().slice(0, 10);

export default [
  {
    name: "stages",
    run: async (call) => {
      await call("GET", "/api/stages");
      await call("GET", "/api/stages?x=1", { headers: agent });

      const post = (body, headers) => call("POST", "/api/stages", { body, headers });
      const put = (key, body, headers) => call("PUT", `/api/stages/${key}`, { body, headers });
      const del = (key, q = "") => call("DELETE", `/api/stages/${key}${q}`);

      // Create: the key comes from the label when omitted; color falls back to
      // slate; position defaults to the end.
      await post({ label: "Discovery call" });
      await post({ label: "Discovery call" });
      await post({ label: "  Review  ", key: " review_2 ", color: " teal ", position: 2, is_won: false });
      await post({ label: "Ünïcode Stage!" });
      await post({ label: "İstanbul" });
      await post({ label: "Weird color", color: "purple", is_lost: false, extra: 1 });
      await post({ label: "Done done", key: "done_done", is_won: true });
      await call("GET", "/api/stages");
      // Handler checks after zod.
      await post({ label: "x", key: "Bad Key" });
      await post({ label: "!!!" });
      await post({ label: "123 go" });
      await post({ label: "   " });
      await post({ label: "x", key: "" });
      await post({ label: "x", key: "   " });
      await post({ label: "x", is_won: true, is_lost: true });
      await post({ label: "Prospect again", key: "prospect" });
      // zod on the body.
      await post({ label: "" });
      await post({});
      await post({ label: 5 });
      await post({ label: "x", position: 1.5 });
      await post({ label: "x", position: "1", is_won: "yes", is_lost: 0 });
      await post({ label: "x", key: 5, color: null });
      await post({ label: null, position: null });
      await post("[]");
      await post("null");
      await post("{");
      await post("");
      await post(JSON.stringify({ label: "Text plain" }), { "Content-Type": "text/plain" });

      // Update.
      await put("discovery_call", { label: " Discovery ", color: " amber ", position: 7 });
      await put("discovery_call", { color: "purple" });
      await put("discovery_call", { color: " Purple " });
      await put("discovery_call", { label: "  " });
      await put("discovery_call", { label: "  ", color: "purple" });
      await put("discovery_call", {});
      await put("discovery_call", { foo: 1 });
      await put("discovery_call", { is_won: true });
      await put("discovery_call", { is_lost: true });
      await put("discovery_call", { is_won: false, is_lost: true });
      await put("discovery_call", { is_won: true, label: "" });
      await put("discovery_call", { is_lost: false });
      await put("nope", { label: "x" });
      await put("nope", {});
      await put("a%20b", { label: "x" });
      await put("discovery_call", { position: 1.5 });
      await put("discovery_call", { is_won: 1, label: null, color: 3 });
      await put("discovery_call", "[]");
      await put("discovery_call", "{");
      await put("discovery_call", JSON.stringify({ label: "ignored" }), { "Content-Type": "text/plain" });
      // A seed stage, changed and put back.
      await put("prospect", { color: "amber", label: "Lead" });
      await call("GET", "/api/stages");
      await put("prospect", { color: "sky", label: "Prospect" });

      // Delete: a stage with deals needs ?reassign_to.
      const deal = await call("POST", "/api/deals", { body: { name: "Stage parity deal", stage: "review_2" } });
      const dealID = idOf(deal, "deal");
      await call("POST", "/api/deals", { body: { name: "Stage parity deal 2", stage: "review_2" } });
      await del("nope");
      await del("review_2");
      await del("review_2", "?reassign_to=");
      await del("review_2", "?reassign_to=%20%20");
      await del("review_2", "?reassign_to=review_2");
      await del("review_2", "?reassign_to=%20review_2%20");
      await del("review_2", "?reassign_to=nope");
      await del("review_2", "?reassign_to=a&reassign_to=b");
      await del("review_2", "?reassign_to=%20discovery_call%20");
      await call("GET", `/api/deals/${dealID}`);
      await del("review_2");
      await del("discovery_call", "?reassign_to=prospect");
      for (const key of ["n_code_stage", "i_stanbul", "weird_color", "done_done", "a%20b"]) await del(key, "?other=1");
      await call("GET", "/api/deals?search=Stage%20parity&sort=name&order=asc");
      await call("GET", "/api/stages");

      // Not routes.
      await call("GET", "/api/stages/prospect");
      await call("POST", "/api/stages/prospect", { body: {} });
      await call("PATCH", "/api/stages/prospect", { body: {} });
      await call("PUT", "/api/stages", { body: {} });
      await call("GET", "/api/stages/");

      // Clean up.
      const left = await call("GET", "/api/deals?search=Stage%20parity&sort=name&order=asc");
      for (const d of left.body?.deals ?? []) await call("DELETE", `/api/deals/${d.id}`);
    },
  },
  {
    name: "deals read on the seed",
    run: async (call) => {
      const list = (q = "") => call("GET", "/api/deals" + q);
      await list();
      for (const sort of ["name", "value", "stage", "close_date", "created_at", "id", "bogus", "company_id", "contact_first_name"]) {
        await list(`?sort=${sort}&order=asc`);
        await list(`?sort=${sort}&order=desc`);
      }
      for (const p of ["page=2&limit=2", "page=0&limit=0", "page=-1&limit=-5", "limit=1000", "page=99", "page=1.9&limit=2.9", "limit=", "limit=2abc"]) {
        await list("?" + p);
      }
      for (const p of [
        "search=client", "search=%20Client%20", "search=spreadsheet", "search=%25", "search=_", "search=zzz",
        "stage=won", "stage=%20won%20", "stage=WON", "stage=",
        "contact_id=contact-alex", "contact_id=%20contact-alex", "company_id=company-northline", "company_id=company-cedar&stage=prospect",
        "search=client&stage=qualified", "filters=%5B%5D&tz=120",
      ]) {
        await list("?" + p);
      }
      // zod on the query (OpenAPI route).
      await list("?order=ASC");
      await list("?stage=a&stage=b");
      await list("?contact_id=1&contact_id=2&company_id=1&company_id=2&order=x&page=1&page=2");

      // The board: every deal, oldest first, with each open one's progress.
      const board = (q = "") => call("GET", "/api/deals/board" + q);
      await board();
      for (const f of [
        [{ field: "name", op: "contains", value: "Client" }],
        [{ field: "stage", op: "is", value: ["won", "prospect"] }],
        [{ field: "value", op: "gt", value: 3000 }],
        [{ field: "company_id", op: "is", value: "company-northline" }],
        [{ field: "contact_id", op: "is_empty" }],
        [{ field: "created_at", op: "today" }],
        [{ field: "bogus", op: "is", value: "x" }],
        [{ logic: "or", rules: [{ field: "value", op: "lt", value: 3000 }, { field: "stage", op: "is", value: "won" }] }],
        "nope",
      ]) {
        await board(qs({ filters: f }));
      }
      for (const tz of ["120", "-600", "841", "abc", ""]) await board(qs({ tz, filters: [{ field: "close_date", op: "is_empty" }] }));
      await board("?filters=%5B");
      await board(qs({ filters: [{ field: "name", op: "is", value: Array.from({ length: 61 }, (_, i) => `v${i}`) }] }));
      await board("?filters=a&filters=b");
      await board("?tz=1&tz=2&filters=x");
      await board("?page=1&page=2");

      // Progress: open deals, worst first.
      const progress = (q = "") => call("GET", "/api/deals/progress" + q);
      await progress();
      for (const p of ["limit=1", "limit=0", "limit=-3", "limit=abc", "limit=1000", "limit=2.9", "tz=120", "tz=-840", "tz=abc"]) await progress("?" + p);
      await progress("?limit=1&limit=2");
      await progress("?tz=1&tz=2&limit=x&limit=y");

      // One deal by id (a plain route).
      await call("GET", "/api/deals/deal-onboarding");
      await call("GET", "/api/deals/deal-onboarding?tz=120");
      await call("GET", "/api/deals/deal-onboarding?tz=1&tz=abc");
      await call("GET", "/api/deals/deal-support");
      await call("GET", "/api/deals/nope");
      await call("GET", "/api/deals/a%20b");

      // One contact and one company by id.
      await call("GET", "/api/contacts/contact-alex");
      await call("GET", "/api/contacts/contact-jamie", { headers: pub });
      await call("GET", "/api/contacts/all");
      await call("GET", "/api/contacts/nope");
      await call("GET", "/api/companies/company-northline");
      await call("GET", "/api/companies/company-cedar");
      await call("GET", "/api/companies/all");
      await call("GET", "/api/companies/nope");

      // Route precedence: the literal paths registered first keep their own
      // handlers for GET; {id} takes every other GET, and PUT/DELETE on them.
      await call("GET", "/api/contacts/aggregates");
      await call("GET", "/api/companies/aggregates");
      await call("GET", "/api/contacts/bulk-delete");
      await call("GET", "/api/companies/bulk-delete");
      await call("GET", "/api/contacts/import");
      await call("GET", "/api/companies/import");
      await call("GET", "/api/contacts/contact-alex/emails");
      await call("GET", "/api/contacts/contact-alex/");
      await call("GET", "/api/deals/board/");
      await call("PUT", "/api/deals/board", { body: { name: "x" } });
      await call("PUT", "/api/deals/progress", { body: {} });
      await call("DELETE", "/api/deals/board");
      await call("DELETE", "/api/deals/progress");
      await call("POST", "/api/deals/board", { body: {} });
      await call("GET", "/api/deals/deal-onboarding/updates");
      await call("POST", "/api/deals/board/updates", { body: { kind: "note", summary: "x" }, headers: agent });
      await call("PATCH", "/api/deals/deal-onboarding", { body: {} });

      // Records for chips and pickers.
      const records = (q) => call("GET", "/api/records" + q);
      for (const q of [
        "?entity=deal", "?entity=deal&search=client", "?entity=deal&search=%20CLIENT%20", "?entity=company&search=cedar", "?entity=contact&search=alex",
        "?entity=contact&search=Alex%20Morgan", "?entity=company", "?entity=deal&ids=deal-onboarding,deal-support", "?entity=deal&ids=%20deal-onboarding%20,,nope,",
        "?entity=company&ids=company-cedar&search=zzz", "?entity=contact&ids=,,", "?entity=deal&ids=" + Array.from({ length: 60 }, (_, i) => `d${i}`).join(","),
        "?entity=deal&ids=" + Array.from({ length: 61 }, (_, i) => `d${i}`).join(","), "?entity=bogus", "", "?entity=Deal", "?entity=deal&entity=bogus",
      ]) {
        await records(q);
      }
      // The values a column holds.
      const values = (q) => call("GET", "/api/values" + q);
      for (const q of [
        "?entity=company&field=industry", "?entity=deal&field=stage", "?entity=deal&field=value", "?entity=contact&field=status",
        "?entity=contact&field=company_id", "?entity=deal&field=bogus", "?entity=deal", "?entity=bogus&field=name", "?field=name", "?entity=company&field=Industry",
      ]) {
        await values(q);
      }
    },
  },
  {
    name: "deals create, update, delete",
    run: async (call) => {
      const post = (body, headers) => call("POST", "/api/deals", { body, headers });
      const put = (id, body, headers) => call("PUT", `/api/deals/${id}`, { body, headers });

      // Create: the company comes from the contact unless given; the stage
      // defaults to the first one; value is parseFloat'd.
      const a = idOf(await post({ name: " Parity A ", contact_id: "contact-jamie", notes: " n ", close_date: " 2026-12-01 " }), "deal");
      const b = idOf(await post({ name: "Parity B", contact_id: "contact-jamie", company_id: null, value: "1,200.50", stage: " proposal " }), "deal");
      const c = idOf(await post({ name: "Parity C", contact_id: "", company_id: "company-cedar", value: " 12.5e1x" }), "deal");
      const d = idOf(await post({ name: "Parity D", company_id: "", value: 99.99 }), "deal");
      const e = idOf(await post({ name: "Parity E", value: "abc", contact_id: null }), "deal");
      const f = idOf(await post({ name: "Parity F", value: -0, stage: "negotiation", custom: {} }), "deal");
      await call("GET", "/api/deals?search=parity&sort=name&order=asc");
      // Handler checks after zod.
      await post({ name: "   " });
      await post({ name: "X", stage: "nope" });
      await post({ name: "X", stage: " Won " });
      await post({ name: "X", bogus: 1, also: 2 });
      await post({ name: "X", custom: { nope: 1 } });
      await post({ name: "   ", bogus: 1 });
      await post({ name: "X", id: "mine", created_at: "x", contact_first_name: "x" });
      // zod on the body.
      await post({ name: "" });
      await post({});
      await post({ name: 5, contact_id: 5, company_id: [], value: true, stage: 5, close_date: null, notes: 1, custom: "x" });
      await post({ name: "x", value: null });
      await post({ name: "x", value: {} });
      await post({ name: "x", custom: null });
      await post("[]");
      await post("null");
      await post("{");
      await post("");
      await post(JSON.stringify({ name: "Text plain deal" }), { "Content-Type": "text/plain" });

      // A won deal makes its company a customer from its close date (never
      // later than today), once: a date already there stays.
      await call("GET", "/api/companies/company-cedar");
      await post({ name: "Parity won", company_id: "company-cedar", stage: "won", close_date: " 2020-02-03 " });
      await call("GET", "/api/companies/company-cedar");
      await post({ name: "Parity won 2", company_id: "company-cedar", stage: "won", close_date: "2019-01-01" });
      await call("GET", "/api/companies/company-cedar");
      await call("PUT", "/api/companies/company-cedar", { body: { customer_since: null } });
      await post({ name: "Parity won 3", company_id: "company-cedar", stage: "won", close_date: "2999-01-01" });
      await call("GET", "/api/companies/company-cedar");
      await call("PUT", "/api/companies/company-cedar", { body: { customer_since: null } });

      // Update.
      await put(a, { name: " Parity A2 ", notes: " notes ", close_date: " 2026-01-01 ", value: "3.5" });
      await put(a, { value: 7 });
      await put(a, { value: "x" });
      await put(a, { stage: " nope " });
      await put(a, { stage: "" });
      await put(a, {});
      await put(a, { bogus: 1 });
      await put(a, { custom: {} });
      await put(a, { contact_first_name: "x", company_name: "y" });
      // A contact set on a deal with no company brings its company along.
      await put(d, { contact_id: "contact-sam" });
      await put(e, { contact_id: "contact-sam", company_id: null });
      await put(e, { contact_id: "" });
      await put(b, { company_id: "" });
      await put(b, { contact_id: "contact-alex" });
      await put(c, { company_id: "company-harbour", contact_id: "contact-alex" });
      await put(c, { contact_id: null, company_id: null });
      // Into a won stage: logged, and the company becomes a customer; into a lost one: logged.
      await put(d, { stage: "won", close_date: "2021-05-06" });
      await call("GET", "/api/companies/company-cedar");
      await put(d, { stage: " lost " });
      await put(d, { stage: "qualified" });
      await put(e, { stage: "won" });
      await put(f, { name: "Parity F2", stage: "won" });
      await call("PUT", "/api/companies/company-cedar", { body: { customer_since: null } });
      await call("GET", "/api/companies/company-harbour");
      // Unknown ids: the body is checked first.
      await put("nope", { name: "x" });
      await put("nope", {});
      await put("nope", { bogus: 1 });
      await put("nope", { stage: "nope" });
      await put("a%20b", { name: "x" });
      // zod on the body.
      await put(a, { name: 5, contact_id: 1, company_id: true, value: [], stage: null, close_date: 1, notes: null, custom: [] });
      await put(a, { name: "" });
      await put(a, { value: false });
      await put(a, "[]");
      await put(a, "{");
      await put(a, JSON.stringify({ name: "Ignored" }), { "Content-Type": "text/plain" });
      await call("GET", "/api/deals?search=parity&sort=name&order=asc");
      await call("GET", "/api/deals?search=parity&sort=value&order=desc&limit=3");
      await call("GET", "/api/deals/board" + qs({ filters: [{ field: "name", op: "contains", value: "Parity A" }] }));
      await call("GET", "/api/deals/progress");
      await call("GET", `/api/deals/${a}`);
      await call("GET", "/api/records?entity=deal&search=parity");

      // Delete: 404 the second time.
      await call("DELETE", `/api/deals/${f}`);
      await call("DELETE", `/api/deals/${f}`);
      await call("DELETE", "/api/deals/nope");
      await call("GET", `/api/deals/${f}`);

      // Clean up.
      const left = await call("GET", "/api/deals?search=parity&sort=name&order=asc&limit=100");
      for (const x of left.body?.deals ?? []) await call("DELETE", `/api/deals/${x.id}`);
      await call("GET", "/api/deals?search=parity");
      await call("GET", "/api/stats");
    },
  },
  {
    name: "tasks and deal updates",
    run: async (call) => {
      const deal = idOf(await call("POST", "/api/deals", { body: { name: "Parity tasks deal", contact_id: "contact-sam" } }), "deal");
      const other = idOf(await call("POST", "/api/deals", { body: { name: "Parity other deal", company_id: "company-cedar" } }), "deal");
      const noCo = idOf(await call("POST", "/api/deals", { body: { name: "Parity no company" } }), "deal");

      // Tasks: create.
      const post = (body, headers = user) => call("POST", "/api/tasks", { body, headers });
      await post({ title: "x" }, pub);
      await post({ title: "x" }, {});
      await post({ title: "x" }, { "X-Clawnify-Caller": "agent-browser" });
      await post({ title: "" }, pub);
      const t1 = idOf(await post({ title: "  Send the proposal  ", deal_id: deal, due_date: lastWeek }), "task");
      const t2 = idOf(await post({ title: "Their legal review", company_id: "company-cedar", owed_by: "them", due_date: nextWeek }, agent), "task");
      const t3 = idOf(await post({ title: "Book the demo", company_id: "company-cedar", contact_id: "contact-sam", due_date: null }, userNoEmail), "task");
      const t4 = idOf(await post({ title: "Other deal's task", deal_id: other, due_date: "2020-01-01" }, api), "task");
      const t5 = idOf(await post({ title: "x".repeat(299) + "😀😀", company_id: null, contact_id: null, deal_id: null, due_date: "" }), "task");
      // Tasks sort by due date, then by when they were created or closed, to
      // the second: each list below holds at most one undated open task and
      // one done task, so the clock can't order them differently on each side.
      await call("PUT", `/api/tasks/${t5}`, { body: { due_date: "2030-01-01" }, headers: user });
      await post({ title: "   " });
      await post({ title: "x", due_date: "2026-02-30" });
      await post({ title: "x", due_date: "26-01-01" });
      await post({ title: "x", company_id: "nope" });
      await post({ title: "x", contact_id: "nope" });
      await post({ title: "x", deal_id: "nope" });
      await post({ title: "x", company_id: "nope", due_date: "bad" });
      await post({ title: "x", company_id: "", contact_id: "", deal_id: "" });
      await post({});
      await post({ title: 5, company_id: 5, contact_id: [], deal_id: {}, owed_by: "me", due_date: 1, extra: 1 });
      await post({ title: "x", owed_by: null });
      await post("[]");
      await post("{");
      await post(JSON.stringify({ title: "Text plain" }), { ...user, "Content-Type": "text/plain" });

      // Tasks: list.
      const list = (q = "") => call("GET", "/api/tasks" + q);
      await list();
      for (const q of ["status=done", "status=all", "status=open", "owed_by=them", "owed_by=us", "company_id=company-cedar", `deal_id=${deal}`, `deal_id=${other}`,
        "deal_id=nope", "limit=1", "limit=0", "limit=abc", "limit=500", "status=all&owed_by=us&company_id=company-cedar&limit=2"]) {
        await list("?" + q);
      }
      await list("?status=bogus");
      await list("?owed_by=me&status=x");
      await list("?company_id=a&company_id=b&limit=1&limit=2");

      // Tasks: update.
      const put = (id, body, headers = user) => call("PUT", `/api/tasks/${id}`, { body, headers });
      await put(t1, { title: "x" }, pub);
      await put("nope", { title: "x" });
      await put("nope", {});
      await put(t1, {});
      await put(t1, { extra: 1 });
      await put(t1, { title: "  " });
      await put(t1, { title: " Send the revised proposal ", owed_by: "us" });
      await put(t1, { due_date: "2026-13-01" });
      await put(t1, { company_id: "nope", title: "" });
      await put(t3, { company_id: "company-cedar" }, agent);
      await put(t3, { company_id: "company-harbour" }, agent);
      await put(t3, { company_id: null, contact_id: null, deal_id: deal });
      await put(t3, { due_date: "" });
      await put(t3, { due_date: tomorrow, owed_by: "them" });
      await put(t2, { done: true });
      await put(t2, { done: true });
      await put(t5, { done: false, title: "short" });
      await put(t1, { title: 5, done: "yes", owed_by: "x" });
      await put(t1, { company_id: 1, due_date: [] });
      await put(t1, "[]");
      await put(t1, "{");
      await list("?status=all");

      // Deal updates: what happened, what it finished, the next step.
      const upd = (id, body, headers = agent, q = "") => call("POST", `/api/deals/${id}/updates${q}`, { body, headers });
      await upd(deal, { kind: "call", summary: "x" }, pub);
      await upd(deal, { kind: "bad", summary: "x" }, pub);
      await upd("nope", { kind: "call", summary: "x" });
      await upd("nope", { kind: "call", summary: "   " });
      await upd(deal, { kind: "call", summary: "   " });
      await upd(deal, { kind: "call", summary: "x", at: tomorrow });
      await upd(deal, { kind: "call", summary: "x", at: inTwoHours });
      await upd(deal, { kind: "call", summary: "x", at: "yesterday" });
      await upd(deal, { kind: "call", summary: "x", at: "2026-02-30" });
      await upd(deal, { kind: "call", summary: "x", next_step: { title: "x", due_date: "tomorrow" } });
      await upd(deal, { kind: "call", summary: "x", close_date: "2026-13-01" });
      await upd(deal, { kind: "call", summary: "x", next_step: { title: "x", due_date: "2026-13-01" }, close_date: "bad" });
      await upd(deal, {
        kind: "call", summary: "  Talked through the proposal with Sam  ", at: twoHoursAgo,
        done_task_ids: [t1, t3, t1, t2, t4, "nope"], next_step: { title: "  Send the contract  ", due_date: nextWeek, owed_by: "them" }, close_date: "2026-12-24",
      });
      await upd(deal, { kind: "note", summary: "A note is not contact", at: "2020-01-02" }, user);
      await upd(deal, { kind: "email", summary: "Emailed from another inbox", at: today }, userNoEmail, "?tz=120");
      await upd(deal, { kind: "message", summary: "WhatsApp", at: "" }, api);
      await upd(noCo, { kind: "meeting", summary: "Met in person", at: "2026-10-01T10:00:00+02:00", next_step: { title: "Follow up", due_date: lastWeek } });
      await upd(noCo, { kind: "call", summary: "Called", done_task_ids: [] }, agent, "?tz=-600");
      await upd(other, { kind: "call", summary: "Closed theirs", done_task_ids: [t4, t2] });
      // zod on the body.
      await upd(deal, {});
      await upd(deal, { kind: 5, summary: "", done_task_ids: [1, "a", null], next_step: {} });
      await upd(deal, { kind: "x", summary: "s".repeat(2001), done_task_ids: "a", next_step: [] });
      await upd(deal, { kind: "call", summary: "a", at: 5, next_step: { title: "", due_date: 5, owed_by: "x" }, close_date: null });
      await upd(deal, { kind: "call", summary: "a", next_step: { title: "t".repeat(301), due_date: "2026-01-01" }, done_task_ids: Array.from({ length: 21 }, (_, i) => `t${i}`) });
      await upd(deal, { kind: "call", summary: "a", next_step: null });
      await upd(deal, "null");
      await upd(deal, "{");
      await upd(deal, JSON.stringify({ kind: "call", summary: "x" }), { ...agent, "Content-Type": "text/plain" });
      await list("?status=open");
      await list("?status=done&company_id=company-cedar");
      await list(`?deal_id=${deal}&status=done&owed_by=us`);
      await list(`?deal_id=${other}&status=done&owed_by=us`);
      await list(`?deal_id=${deal}&status=open`);
      await call("GET", `/api/deals/${deal}`);
      await call("GET", `/api/deals/${noCo}?tz=-600`);
      await call("GET", "/api/deals/progress");
      await call("GET", "/api/deals/board" + qs({ filters: [{ field: "name", op: "contains", value: "Parity tasks" }] }));

      // Tasks: delete (no 404: deleting nothing is fine).
      const del = (id, headers = user) => call("DELETE", `/api/tasks/${id}`, { headers });
      await del(t1, pub);
      await del("nope");
      for (const t of [t1, t2, t3, t4, t5]) await del(t);
      const left = await list("?status=all&limit=200");
      for (const t of left.body?.tasks ?? []) await del(t.id);
      await list("?status=all");
      // Not routes.
      await call("GET", `/api/tasks/${t1}`);
      await call("PATCH", `/api/tasks/${t1}`, { body: {} });
      await call("DELETE", "/api/tasks");

      for (const id of [deal, other, noCo]) await call("DELETE", `/api/deals/${id}`);
    },
  },
];
