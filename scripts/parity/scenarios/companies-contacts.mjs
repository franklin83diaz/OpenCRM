// Companies and contacts area: dashboard stats, and the companies and
// contacts lists (pagination, sort incl. relation and count: sorts, search,
// ?filters= JSON with tz, footer aggregates), create, update, delete and
// bulk-delete. Later calls use the ids earlier creates returned (ids are
// numbered per scenario by first appearance).
//
// Scenarios run in file order on one database, before customers.mjs and the
// others, so everything created here is deleted again and the seed rows are
// left as they were (a seed row that is updated gets its values back).
//
// New rows get random UUIDs on each side, so a list that would order them by
// id (the default sort, or a tie) differs between the two backends: lists
// that can see created rows sort on a unique column or are narrowed by search.
// Ties on the seed (fixed ids) are fine.
//
// No ported route creates custom fields, so custom-field writes (required
// fields, enum violations, relation ids), custom relation sorts and filters,
// and detaching relations on delete are the TS-replay test in
// server/internal/crm (companies_contacts_ts_test.go). So are paths that end
// in a database error (a NaN page, a foreign key), whose message is D1's in TS
// and SQLite's here.
// "Cedar & Co <b>" coming back unescaped is asserted on the raw bytes in
// routes_companies_test.go: this runner compares parsed JSON.

const enc = encodeURIComponent;
const qs = (o) =>
  "?" +
  Object.entries(o)
    .map(([k, v]) => `${enc(k)}=${enc(typeof v === "string" ? v : JSON.stringify(v))}`)
    .join("&");
const idOf = (res, key) => res.body?.[key]?.id ?? `missing-${key}`;

export default [
  {
    name: "stats",
    run: async (call) => {
      // The first read gives Northline its customer_since (its contact's won deal).
      await call("GET", "/api/stats");
      await call("GET", "/api/stats?x=1");
      // Other methods and paths are not routes.
      await call("POST", "/api/stats", { body: {} });
      await call("GET", "/api/stats/");
    },
  },
  {
    name: "companies list and aggregates on the seed",
    run: async (call) => {
      const list = (q = "") => call("GET", "/api/companies" + q);
      await list();
      // Sort: any real column, count:<many side>, else id; order asc/desc.
      await list("?sort=name&order=asc");
      await list("?sort=name&order=desc");
      await list("?sort=name");
      await list("?sort=bogus&order=asc");
      await list("?sort=contact_count&order=asc");
      await list("?sort=count:contacts&order=asc");
      await list("?sort=count:deals&order=desc");
      await list("?sort=count:bogus&order=asc");
      await list("?sort=count:&order=asc");
      await list("?sort=customer_since&order=asc");
      await list("?sort=industry&order=asc");
      await list("?sort=" + enc('na"me') + "&order=asc");
      // Pagination: parseInt, clamped to page >= 1 and 1 <= limit <= 100.
      for (const p of [
        "page=2&limit=2", "page=0&limit=0", "page=-1&limit=-1", "limit=1000", "page=99", "page=1.9&limit=2.9",
        "page=%20%202&limit=1", "limit=1e2", "page=0x2&limit=2", "limit=", "page=&limit=", "limit=2abc", "limit=-0",
      ]) {
        await list("?" + p);
      }
      // Search (name, domain, email) and industry, both trimmed.
      for (const p of ["search=cedar", "search=%20Cedar%20%26%20Co%20", "search=zzz", "search=%25", "search=_", "search=", "search=+studio+",
        "industry=Consulting", "industry=%20Consulting%20", "industry=consulting", "industry=Consulting&search=cedar", "search=Zo%C3%AB"]) {
        await list("?" + p);
      }
      // Filters.
      const filters = [
        [{ field: "name", op: "contains", value: "Studio" }],
        [{ field: "name", op: "does_not_contain", value: "o" }],
        [{ field: "industry", op: "is", value: ["Consulting", "Design agency"] }],
        [{ field: "industry", op: "is_not", value: "Consulting" }],
        [{ field: "domain", op: "is_empty" }],
        [{ field: "customer_since", op: "is_not_empty" }],
        [{ logic: "or", rules: [{ field: "name", op: "contains", value: "Harbour" }, { field: "name", op: "contains", value: "Cedar" }] }],
        [{ field: "contacts", op: "is_not_empty" }],
        [{ field: "deals", op: "is_empty" }],
        [{ field: "bogus", op: "is", value: "x" }],
        [{ field: "created_at", op: "today" }],
        [{ field: "created_at", op: "in_past" }],
        [{ field: "created_at", op: "in_future" }],
        [{ field: "created_at", op: "relative", value: "PAST_7_DAY" }],
        [{ field: "created_at", op: "before", value: "2000-01-01" }],
        [{ field: "customer_since", op: "on", value: "2001-01-01" }],
        { field: "name", op: "contains", value: "Studio" },
        "nope",
      ];
      for (const f of filters) await list(qs({ filters: f }));
      for (const tz of ["120", "-600", "840", "841", "abc", "1.5", ""]) {
        await list(qs({ filters: [{ field: "created_at", op: "today" }], tz }));
      }
      await list("?filters=%5B");
      await list("?filters=");
      // More filter values than D1's bound parameters allow: 400.
      const many = Array.from({ length: 61 }, (_, i) => `v${i}`);
      await list(qs({ filters: [{ field: "name", op: "is", value: many }] }));
      await list(qs({ filters: [{ field: "name", op: "is", value: many.slice(0, 60) }] }));
      // zod on the query (OpenAPI route): order is an enum, the rest strings;
      // a parameter sent twice is an array. Issues in the schema's key order.
      await list("?order=ASC");
      await list("?order=");
      await list("?order=asc&order=desc");
      await list("?page=1&page=2");
      await list("?search=a&search=b");
      await list("?industry=a&industry=b&tz=1&tz=2&order=x&page=1&page=2&filters=a&filters=b&limit=1&limit=2&sort=a&sort=b");
      await list("?other=1&other=2");

      // Footer aggregates: a plain route, first value of each parameter.
      const agg = (q = "") => call("GET", "/api/companies/aggregates" + q);
      await agg();
      await agg(qs({
        ops: [
          { key: "name", op: "count" }, { key: "contacts", op: "sum" }, { key: "contacts", op: "avg" },
          { key: "domain", op: "count_empty" }, { key: "industry", op: "count_unique" }, { key: "name", op: "percent_not_empty" },
          { key: "domain", op: "percent_empty" }, { key: "industry", op: "count_not_empty" },
        ],
      }));
      await agg(qs({ ops: [{ key: "contacts", op: "min" }, { key: "contacts", op: "max" }, { key: "contacts", op: "count" }] }));
      await agg(qs({ ops: [{ key: "name", op: "sum" }, { key: "bogus", op: "count" }, 5, "x", [], { key: 1, op: "count" }, { key: "name" }] }));
      await agg(qs({ ops: [{ key: "name", op: "count" }], search: "cedar" }));
      await agg(qs({ ops: [{ key: "name", op: "count" }], industry: "Consulting" }));
      await agg(qs({ ops: [{ key: "contacts", op: "sum" }], filters: [{ field: "industry", op: "is_not", value: "Consulting" }] }));
      await agg(qs({ ops: [{ key: "name", op: "count" }], filters: [{ field: "created_at", op: "today" }], tz: "-600" }));
      await agg(qs({ ops: Array.from({ length: 51 }, (_, i) => ({ key: i === 50 ? "domain" : "name", op: "count_unique" })) }));
      await agg(qs({ ops: [{ key: "name", op: "count" }], filters: [{ field: "name", op: "is", value: many }] }));
      await agg(qs({ ops: [null] }));
      await agg(qs({ ops: { key: "name", op: "count" } }));
      await agg("?ops=%5B");
      await agg("?ops=");
      await agg("?ops=" + enc(JSON.stringify([{ key: "name", op: "count" }])) + "&ops=" + enc(JSON.stringify([{ key: "domain", op: "count" }])));
      await agg("?search=cedar&search=harbour&ops=" + enc(JSON.stringify([{ key: "name", op: "count" }])));
      await agg("?order=ASC&page=x");
    },
  },
  {
    name: "contacts list and aggregates on the seed",
    run: async (call) => {
      const list = (q = "") => call("GET", "/api/contacts" + q);
      await list();
      await list("?sort=first_name&order=asc");
      await list("?sort=company_id&order=asc");
      await list("?sort=company_id&order=desc");
      await list("?sort=company_id");
      await list("?sort=count:deals&order=asc");
      await list("?sort=count:contacts&order=asc");
      await list("?sort=last_contacted_at&order=asc");
      await list("?sort=company_name&order=asc");
      await list("?page=2&limit=2&sort=last_name&order=asc");
      await list("?limit=0&page=-5");
      for (const p of ["status=lead", "status=%20lead%20", "status=Lead", "company_id=company-cedar", "company_id=%20company-cedar",
        "search=Cedar", "search=alex", "search=director", "search=%20harbour%20", "search=nobody", "status=lead&search=cedar&company_id=company-cedar",
        "status=", "company_id="]) {
        await list("?" + p);
      }
      const filters = [
        [{ field: "company_id", op: "is", value: "company-cedar" }],
        [{ field: "company_id", op: "is", value: ["company-cedar", "company-harbour"] }],
        [{ field: "company_id", op: "is_not", value: ["company-cedar"] }],
        [{ field: "company_id", op: "is_empty" }],
        [{ field: "deals", op: "is_not_empty" }],
        [{ field: "status", op: "is", value: "customer" }],
        [{ field: "last_contacted_at", op: "is_empty" }],
        [{ logic: "and", rules: [{ field: "first_name", op: "contains", value: "a" }, { logic: "or", rules: [{ field: "status", op: "is", value: "lead" }, { field: "title", op: "contains", value: "Ops" }] }] }],
        [{ field: "updated_at", op: "relative", value: "THIS_1_WEEK" }],
      ];
      for (const f of filters) await list(qs({ filters: f, tz: "60" }));
      await list(qs({ filters: [{ field: "email", op: "is", value: Array.from({ length: 61 }, (_, i) => `e${i}`) }] }));
      await list("?status=a&status=b");
      await list("?company_id=a&company_id=b&status=c&status=d&order=up");

      const agg = (q = "") => call("GET", "/api/contacts/aggregates" + q);
      await agg();
      await agg(qs({
        ops: [
          { key: "name", op: "count_unique" }, { key: "email", op: "count_not_empty" }, { key: "company", op: "count_unique" },
          { key: "status", op: "count" }, { key: "title", op: "percent_empty" }, { key: "phone", op: "count_empty" },
          { key: "first_name", op: "count" }, { key: "status", op: "sum" },
        ],
      }));
      await agg(qs({ ops: [{ key: "name", op: "count" }], search: "cedar" }));
      await agg(qs({ ops: [{ key: "name", op: "count" }], status: "lead", company_id: "company-harbour" }));
      await agg(qs({ ops: [{ key: "company", op: "count_unique" }], filters: [{ field: "deals", op: "is_not_empty" }] }));
      await agg(qs({ ops: [{ key: "email", op: "count" }], filters: [{ field: "email", op: "is", value: Array.from({ length: 61 }, (_, i) => `e${i}`) }] }));
      await agg(qs({ ops: [null] }));
    },
  },
  {
    name: "companies create, update, delete",
    run: async (call) => {
      const post = (body, headers) => call("POST", "/api/companies", { body, headers });
      const put = (id, body, headers) => call("PUT", `/api/companies/${id}`, { body, headers });

      const cedar = await post({ name: "Cedar & Co <b>", domain: " cedar-b.example ", industry: " Consulting ", phone: " +1 555 0100 ", email: " hi@cedar-b.example ", notes: " Zoë's notes " });
      const cedarID = idOf(cedar, "company");
      const alpha = await post({ name: "Alpha Parity", domain: "alpha-parity.example", id: "my-own-id", created_at: "2000-01-01" });
      const alphaID = idOf(alpha, "company");
      const beta = await post({ name: "Beta Parity" }, { "Content-Type": "application/json; charset=utf-8" });
      const betaID = idOf(beta, "company");
      // The new rows by name (ids are random on each side, so never sort by id here).
      await call("GET", "/api/companies?search=parity&sort=name&order=asc");
      await call("GET", "/api/companies?sort=name&order=asc&limit=100");
      await call("GET", "/api/companies?search=cedar&sort=name&order=desc");
      await call("GET", "/api/companies/aggregates" + qs({ ops: [{ key: "name", op: "count" }, { key: "contacts", op: "sum" }], search: "parity" }));
      await call("GET", "/api/stats");

      // zod on the body (required: validated whatever the Content-Type says).
      await post({ name: "" });
      await post({});
      await post({ name: 5 });
      await post({ name: null });
      await post({ name: "x", domain: 5, industry: null, phone: [], email: {}, notes: true, custom: [] });
      await post({ name: "", custom: "x" });
      await post({ name: "x", custom: null });
      await post("[]");
      await post("null");
      await post("5");
      await post("{");
      await post("");
      await post(JSON.stringify({ name: "Text Plain Co" }), { "Content-Type": "text/plain" });
      await post(JSON.stringify({ name: "No Type Co" }), { "Content-Type": "" });
      // Handler checks after zod.
      await post({ name: "   " });
      await post({ name: "X", bogus: 1, also_bogus: 2 });
      await post({ name: "X", zeta: 1, 10: 1, 2: 1, custom: { foo: 1, 2: 2 } });
      await post({ name: "X", custom: { domain: "ignored.example" }, last_contacted_at: "x" });
      await post({ name: "   ", bogus: 1 });

      // Update: a field sent as null is not a field left out (Review Focus 1).
      await put(cedarID, { domain: null });
      await put(cedarID, {});
      await put(cedarID, { domain: "  new.example  " });
      await put(cedarID, { name: " Cedar & Co <b> ", industry: "", phone: "", email: "", notes: "" });
      await put(cedarID, { customer_since: "2026-02-30" });
      await put(cedarID, { customer_since: "26-01-15" });
      await put(cedarID, { customer_since: " 2026-01-15 " });
      await put(cedarID, { renewal_date: "2026-13-01" });
      await put(cedarID, { renewal_date: "2027-01-01", customer_since: null });
      await put(cedarID, { customer_since: "", renewal_date: "  " });
      await put(cedarID, { customer_since: 5 });
      await put(cedarID, { renewal_date: [] });
      await put(cedarID, { name: null, domain: 1, custom: 2 });
      await put(cedarID, { bogus: 1, customer_since: "nope" });
      await put(cedarID, { id: "other", created_at: "x", contact_count: 3 });
      await put(cedarID, { last_contacted_at: "x" });
      await put(cedarID, "{");
      await put(cedarID, "[]");
      await put(cedarID, "null");
      await put(cedarID, JSON.stringify({ name: "Ignored" }), { "Content-Type": "text/plain" });
      await put(alphaID, { name: "" });
      await put(alphaID, { name: "Alpha Parity", notes: "  two\nlines  " });
      await call("GET", "/api/companies?search=parity&sort=name&order=asc");
      // Unknown ids: the body is checked first.
      await put("no-such-company", { name: "x" });
      await put("no-such-company", {});
      await put("no-such-company", { bogus: 1 });
      await put("no-such-company", { customer_since: "x" });
      await put("a%20b", { name: "x" });
      // A seed row, updated and put back.
      await put("company-harbour", { phone: "+44 1", customer_since: "2026-01-01" });
      await call("GET", "/api/companies?search=harbour");
      await put("company-harbour", { phone: "", customer_since: null });

      // Route precedence: the literal paths are not ids for GET/POST, but PUT
      // and DELETE on them reach the {id} routes, as in Hono.
      await put("aggregates", { name: "x" });
      await put("bulk-delete", { name: "x" });
      await call("DELETE", "/api/companies/aggregates");
      await call("DELETE", "/api/companies/bulk-delete");
      await call("POST", "/api/companies/aggregates", { body: {} });
      await call("PATCH", `/api/companies/${cedarID}`, { body: { name: "x" } });
      await call("PUT", "/api/companies/", { body: { name: "x" } });
      await call("PUT", `/api/companies/${cedarID}/`, { body: { name: "x" } });
      await call("DELETE", "/api/companies");
      await call("GET", "/api/companies/aggregates/");

      // Delete: 404 the second time.
      await call("DELETE", `/api/companies/${betaID}`);
      await call("DELETE", `/api/companies/${betaID}`);
      await call("DELETE", "/api/companies/no-such-company");
      await call("GET", "/api/companies?search=parity&sort=name&order=asc");

      // Bulk delete: 150 ids, deduplicated, chunked under D1's 100 bound
      // parameters (Review Focus 3); the real ones sit in both chunks.
      const ids = Array.from({ length: 150 }, (_, i) => `ghost-${i}`);
      ids[0] = cedarID;
      ids[95] = alphaID;
      ids[149] = cedarID;
      const bulk = (body, headers) => call("POST", "/api/companies/bulk-delete", { body, headers });
      await bulk({ ids: ["ghost-a", "ghost-a"] });
      await bulk({});
      await bulk({ ids: [] });
      await bulk({ ids: [""] });
      await bulk({ ids: [1, "", null, "ok", {}] });
      await bulk({ ids: Array.from({ length: 501 }, (_, i) => `g${i}`) });
      await bulk({ ids: Array.from({ length: 501 }, (_, i) => (i === 7 ? 7 : `g${i}`)) });
      await bulk({ ids: "x" });
      await bulk({ ids: null });
      await bulk({ other: [] });
      await bulk("null");
      await bulk("[]");
      await bulk("{");
      await bulk(JSON.stringify({ ids: [cedarID] }), { "Content-Type": "text/plain" });
      await bulk({ ids, extra: 1 });
      await bulk({ ids });
      await call("GET", "/api/companies?search=parity");
      await call("GET", "/api/companies?search=cedar&sort=name&order=asc");
      await call("GET", "/api/stats");
    },
  },
  {
    name: "contacts create, update, delete",
    run: async (call) => {
      const post = (body, headers) => call("POST", "/api/contacts", { body, headers });
      const put = (id, body, headers) => call("PUT", `/api/contacts/${id}`, { body, headers });

      // The company comes from the work email's domain when none is chosen:
      // found by its domain (protocol, www and a trailing slash tolerated), else
      // created and named after it.
      const nora = await post({ first_name: " Nora ", last_name: " Díaz ", email: " nora@Widgets-Parity.example ", phone: " 1 ", title: " CEO " });
      const noraID = idOf(nora, "contact");
      const omar = await post({ first_name: "Omar", email: "OMAR@widgets-parity.EXAMPLE" });
      const omarID = idOf(omar, "contact");
      const pia = await post({ first_name: "Pia", email: "pia@gmail.com" });
      const piaID = idOf(pia, "contact");
      const quin = await post({ first_name: "Quin", email: "quin@nodot" });
      const quinID = idOf(quin, "contact");
      const sam2 = await post({ first_name: "Sam", last_name: "Second", company_id: "company-cedar", email: "s@elsewhere-parity.example", status: "  customer " });
      const sam2ID = idOf(sam2, "contact");
      const tia = await post({ first_name: "Tia", company_id: null, email: "tia@cedar-parity.example", status: "" });
      const tiaID = idOf(tia, "contact");
      const uma = await post({ first_name: "Uma", company_id: "", email: "uma@uma-parity.example" });
      const umaID = idOf(uma, "contact");
      const zoe = await post({ first_name: "Zoë", last_name: "& <b>", email: "" });
      const zoeID = idOf(zoe, "contact");
      const widgets = nora.body?.contact?.company_id ?? "missing-widgets";
      const cedarParity = tia.body?.contact?.company_id ?? "missing-cedar-parity";
      const umaParity = uma.body?.contact?.company_id ?? "missing-uma-parity";
      // A company whose stored domain has a protocol, www and a slash.
      const www = await call("POST", "/api/companies", { body: { name: "WWW Parity", domain: "https://www.www-parity.example/" } });
      const wwwID = idOf(www, "company");
      const vic = await post({ first_name: "Vic", email: "vic@www-parity.example" });
      const vicID = idOf(vic, "contact");

      await call("GET", "/api/contacts?search=parity&sort=first_name&order=asc");
      await call("GET", "/api/contacts?search=Zo%C3%AB");
      await call("GET", "/api/contacts?company_id=" + widgets + "&sort=first_name&order=desc");
      await call("GET", "/api/companies?search=parity&sort=name&order=asc");
      await call("GET", "/api/companies?search=parity&sort=count:contacts&order=desc&limit=1");
      await call("GET", "/api/contacts/aggregates" + qs({ ops: [{ key: "company", op: "count_unique" }, { key: "name", op: "count" }], search: "parity" }));
      await call("GET", "/api/stats");

      // zod on the body.
      await post({ first_name: "" });
      await post({});
      await post({ first_name: "W", company_id: 5 });
      await post({ first_name: "W", last_name: null, email: 1, phone: [], title: {}, status: false, custom: 3 });
      await post("[]");
      await post("{");
      await post(JSON.stringify({ first_name: "W" }), { "Content-Type": "text/plain" });
      await post({ first_name: "  " });
      await post({ first_name: "W", bogus: 1, company: "x" });
      await post({ first_name: "W", custom: { nope: 1 }, last_contacted_at: "2026-01-01" });

      // Update.
      await put(noraID, { company_id: null });
      await put(noraID, {});
      await put(noraID, { first_name: " Nora ", last_name: "", status: " customer ", title: "Founder", phone: "" });
      await put(noraID, { company_id: "company-cedar" });
      await put(noraID, { company_id: "" });
      await put(noraID, { company_id: widgets, email: "nora@gmail.com" });
      await put(noraID, { status: null });
      await put(noraID, { company_id: 5, first_name: 1 });
      await put(noraID, { last_contacted_at: "x" });
      await put(noraID, { id: "x" });
      await put(noraID, { bogus: 1 });
      await put(noraID, "{");
      await put(noraID, JSON.stringify({ first_name: "X" }), { "Content-Type": "text/plain" });
      await put("no-such-contact", { first_name: "x" });
      await put("no-such-contact", {});
      await put("aggregates", { first_name: "x" });
      await put("bulk-delete", { first_name: "x" });
      await call("GET", "/api/contacts?search=parity&sort=first_name&order=asc");
      // Sorted by the company's name (no two of these share a company).
      await call("GET", "/api/contacts?sort=company_id&order=asc&search=parity&status=customer");
      await call("GET", "/api/contacts?sort=company_id&order=desc&search=parity&status=customer");

      // Route precedence and non-routes.
      await call("DELETE", "/api/contacts/aggregates");
      await call("DELETE", "/api/contacts/bulk-delete");
      await call("POST", "/api/contacts/aggregates", { body: {} });
      await call("PATCH", `/api/contacts/${noraID}`, { body: {} });
      await call("PUT", "/api/contacts/", { body: { first_name: "x" } });
      await call("GET", "/api/contacts/");

      // Delete and bulk delete.
      await call("DELETE", `/api/contacts/${piaID}`);
      await call("DELETE", `/api/contacts/${piaID}`);
      const bulk = (body, headers) => call("POST", "/api/contacts/bulk-delete", { body, headers });
      await bulk({});
      await bulk({ ids: [] });
      await bulk({ ids: ["", 2] });
      await bulk({ ids: Array.from({ length: 501 }, (_, i) => `g${i}`) });
      await bulk("null");
      const ids = Array.from({ length: 150 }, (_, i) => `ghost-${i}`);
      [noraID, omarID, quinID, sam2ID, tiaID, umaID, zoeID, vicID, piaID, noraID].forEach((id, i) => { ids[i * 16] = id; });
      await bulk({ ids });
      await call("GET", "/api/contacts?search=parity");
      await call("GET", "/api/contacts?sort=first_name&order=asc&limit=100");

      // The companies the emails made, and the www one, go too: the seed is as it was.
      await call("GET", "/api/companies?search=parity&sort=name&order=asc");
      await call("POST", "/api/companies/bulk-delete", { body: { ids: [widgets, cedarParity, umaParity, wwwID] } });
      await call("GET", "/api/companies?sort=name&order=asc");
      await call("GET", "/api/contacts?sort=first_name&order=asc");
      await call("GET", "/api/stats");
    },
  },
];
