// Settings area: the activity timeline (GET/POST /api/activities), CSV import
// (POST /api/contacts/import, POST /api/companies/import), custom fields
// (GET/POST /api/custom-fields, PUT/DELETE /api/custom-fields/{id}) and a
// list's views (GET/POST /api/views, PATCH/DELETE /api/views/{id}, GET
// /api/views/{id}/fields, PUT /api/views/{id}/fields/{key}). Later calls use
// the ids earlier creates returned (ids are numbered per scenario by first
// appearance).
//
// Scenarios run in file order on one database, after the others, and delete
// what they create: custom fields (and so their columns), imported contacts
// and companies, views. A list's default view stays once created, as it does
// in the app.
//
// New rows get random UUIDs on each side, so lists that can see created rows
// sort on a unique column or are narrowed by search, and the timeline (newest
// first, then by id) gets at most one new note per record in a second.
//
// Malformed bodies: the timeline, import and custom-field routes read the body
// with c.req.json() and no .catch (V8's SyntaxError text in a 500 or 400; a
// null body is a TypeError), while the views routes read it with
// .catch(() => ({})) — a body that isn't JSON is {} there (Review Focus 4),
// but JSON null still throws (Hono's plain 500).
//
// The SQL itself — chunked imports past D1's 100 parameters, column order of
// imported custom fields, key order in stored JSON — and database errors
// (D1's words in TS, SQLite's here) are the TS-replay test in
// server/internal/crm (settings_ts_test.go).

const enc = encodeURIComponent;
const qs = (o) =>
  "?" +
  Object.entries(o)
    .map(([k, v]) => `${enc(k)}=${enc(typeof v === "string" ? v : JSON.stringify(v))}`)
    .join("&");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// workerd's c.req.json() keeps a leading byte order mark: such a body isn't JSON.
const BOM = String.fromCharCode(0xfeff);

export default [
  {
    name: "activities",
    run: async (call) => {
      const list = (q) => call("GET", "/api/activities" + q);
      const post = (body, headers) => call("POST", "/api/activities", { body, headers });

      // Reads: entity_type is contact, company or deal, and an entity_id;
      // both trimmed, first value of each.
      await list("");
      await list("?entity_type=contact");
      await list("?entity_id=contact-alex");
      await list("?entity_type=Contact&entity_id=contact-alex");
      await list("?entity_type=task&entity_id=contact-alex");
      await list("?entity_type=contact&entity_id=%20%20");
      await list("?entity_type&entity_id=contact-alex");
      await list("?entity_type=contact&entity_id=contact-alex");
      await list("?entity_type=+contact+&entity_id=%20contact-alex%09");
      await list("?entity_type=contact&entity_type=deal&entity_id=contact-alex&entity_id=x");
      await list("?entity_type=deal&entity_type=contact&entity_id=contact-alex");
      await list("?entity_type=contact&entity_id=nobody");
      await list("?entity_type=company&entity_id=company-cedar&x=1");

      // Writes: a note (type defaults to "note"), trimmed; no record check.
      await post({ entity_type: "contact", entity_id: "contact-alex", body: "  Called about the renewal  " });
      await post({ entity_type: " company ", entity_id: " company-cedar ", type: " call ", body: "Cedar & Co <b> rang", extra: 1 });
      await post({ entity_type: "deal", entity_id: "deal-property", type: "", body: "Zoë 🎉 note" });
      await post({ entity_type: "contact", entity_id: "parity-timeline", type: "custom kind", body: "first" });
      await post(JSON.stringify({ entity_type: "contact", entity_id: "contact-jamie", body: "sent as text/plain" }), { "Content-Type": "text/plain" });
      // 400s.
      await post({});
      await post({ entity_type: "contact" });
      await post({ entity_type: "Contact", entity_id: "contact-alex", body: "x" });
      await post({ entity_type: "contact", entity_id: "   ", body: "x" });
      await post({ entity_type: 0, entity_id: "contact-alex", body: "x" });
      await post({ entity_type: "contact", entity_id: "contact-alex" });
      await post({ entity_type: "contact", entity_id: "contact-alex", body: "  \n " });
      await post({ entity_type: "contact", entity_id: "contact-alex", body: "", type: 5 });
      await post({ entity_type: "contact", entity_id: "contact-alex", body: null });
      await post("[]");
      await post('"contact"');
      await post("5");
      // 500s: what TS threw (V8's words).
      await post({ entity_type: 5, entity_id: "contact-alex", body: "x" });
      await post({ entity_type: "contact", entity_id: true, body: "x" });
      await post({ entity_type: "contact", entity_id: "contact-alex", body: ["x"] });
      await post({ entity_type: "contact", entity_id: "contact-alex", body: "x", type: { a: 1 } });
      await post({ entity_type: ["contact"], entity_id: 5, body: 5 });
      await post("not json");
      await post("");
      await post("{");
      await post("null");
      await post("{}x");
      await post(BOM + JSON.stringify({ entity_type: "deal", entity_id: "parity-bom", body: "x" }));

      // Read back, newest first.
      await list("?entity_type=contact&entity_id=contact-alex");
      await list("?entity_type=company&entity_id=company-cedar");
      await list("?entity_type=deal&entity_id=deal-property");
      await list("?entity_type=contact&entity_id=contact-jamie");
      await sleep(1100);
      await post({ entity_type: "contact", entity_id: "parity-timeline", body: "second" });
      await list("?entity_type=contact&entity_id=parity-timeline");
      await list("?entity_type=company&entity_id=parity-timeline");
      await list("?entity_type=deal&entity_id=parity-bom");

      // Not routes.
      await call("PUT", "/api/activities", { body: {} });
      await call("DELETE", "/api/activities");
      await call("GET", "/api/activities/contact-alex");
      await call("GET", "/api/activities/");
    },
  },
  {
    name: "custom fields",
    run: async (call) => {
      const list = (q = "") => call("GET", "/api/custom-fields" + q);
      const post = (body, headers) => call("POST", "/api/custom-fields", { body, headers });
      const put = (id, body, headers) => call("PUT", `/api/custom-fields/${id}`, { body, headers });
      const del = (id) => call("DELETE", `/api/custom-fields/${id}`);
      const defOf = (res) => res.body?.def?.id ?? "missing-def";

      await list();
      await list("?entity=contact");
      await list("?entity=");
      await list("?entity=bogus");
      await list("?entity=Contact");
      await list("?entity=deal&entity=bogus");

      // Create: plain fields, with the defaults for what isn't sent.
      const nick = defOf(await post({ entity_type: "contact", key: "nickname", label: "Nickname" }));
      const tier = defOf(await post({
        entity_type: "contact", key: "tier", label: "Tier", field_type: "enumeration",
        custom_field: "clawnify::badge.badge", options: { enum: ["gold", "silver"], colors: { gold: "amber" } }, position: 2,
      }));
      const seats = defOf(await post({ entity_type: "company", key: "seats", label: "Seats", field_type: "integer", position: 1.5 }));
      const vip = defOf(await post({ entity_type: "company", key: "vip", label: true, field_type: "boolean", options: null, custom_field: null, position: null }));
      const meta = defOf(await post({ entity_type: "deal", key: "meta_json", label: 12.5, field_type: "json", options: [1, "two"] }));
      const odd = defOf(await post({ entity_type: "deal", key: "odd", label: { a: 1 }, options: "x", extra: true }));
      const sameKey = defOf(await post({ entity_type: "company", key: "nickname", label: ["Nick", "name"], field_type: "string" }));
      const plain = defOf(await post(JSON.stringify({ entity_type: "deal", key: "plain_text", label: "Plain" }), { "Content-Type": "text/plain" }));
      // Values bound as D1 binds them: a number or a boolean in a TEXT column
      // is a REAL ("5.0", "1.0"); text in an INTEGER one a number.
      const bound = defOf(await post({ entity_type: "deal", key: "bound", label: "Bound", field_type: 5, custom_field: true, position: "3" }));
      // Relations: a def on each side.
      const partner = defOf(await post({
        entity_type: "contact", key: "partner_company_id", label: "Partner", field_type: "relation",
        relation_type: "many_to_one", target_entity: "company", inverse_key: "partner_contacts", inverse_label: "Partner contacts", position: 4,
      }));
      const mentees = defOf(await post({
        entity_type: "contact", key: "mentees", label: "Mentees", field_type: "relation",
        relation_type: "one_to_many", target_entity: "contact", inverse_key: "mentor_id", inverse_label: "Mentor",
      }));
      await list();
      await list("?entity=company");
      // The new columns show on the records.
      await call("GET", "/api/contacts/contact-alex");
      await call("GET", "/api/companies/company-cedar");

      // 400s: the route's checks, then createDef's and createRelation's.
      await post({});
      await post({ entity_type: "Contact", key: "x", label: "X" });
      await post({ entity_type: "contact", label: "X" });
      await post({ entity_type: "contact", key: "x" });
      await post({ entity_type: "contact", key: 0, label: "X" });
      await post({ entity_type: "contact", key: "x", label: "" });
      await post({ entity_type: "contact", key: "Bad Key", label: "X" });
      await post({ entity_type: "contact", key: 123, label: "X" });
      await post({ entity_type: "contact", key: ["a", "b"], label: "X" });
      await post({ entity_type: "contact", key: "email", label: "Email" });
      await post({ entity_type: "deal", key: "stage", label: "Stage" });
      await post({ entity_type: "contact", key: "nickname", label: "Again" });
      await post({ entity_type: "contact", key: "x_id", label: "X", field_type: "relation" });
      await post({ entity_type: "contact", key: "x_id", label: "X", field_type: "relation", target_entity: "Company" });
      await post({ entity_type: "contact", key: "x_id", label: "X", field_type: "relation", target_entity: "company" });
      await post({ entity_type: "contact", key: "x_id", label: "X", field_type: "relation", target_entity: "company", inverse_key: "xs" });
      await post({ entity_type: "contact", key: "x_id", label: "X", field_type: "relation", target_entity: "company", inverse_key: "xs", inverse_label: "Xs" });
      await post({ entity_type: "contact", key: "x_id", label: "X", field_type: "relation", relation_type: "one_to_one", target_entity: "company", inverse_key: "xs", inverse_label: "Xs" });
      await post({ entity_type: "contact", key: "buddy", label: "Buddy", field_type: "relation", relation_type: "many_to_one", target_entity: "company", inverse_key: "buddies", inverse_label: "Buddies" });
      await post({ entity_type: "contact", key: "buddies", label: "Buddies", field_type: "relation", relation_type: "one_to_many", target_entity: "company", inverse_key: "buddy", inverse_label: "Buddy" });
      await post({ entity_type: "contact", key: "pal_id", label: "Pal", field_type: "relation", relation_type: "many_to_one", target_entity: "contact", inverse_key: "pal_id", inverse_label: "Pals" });
      await post({ entity_type: "contact", key: "Pal_id", label: "Pal", field_type: "relation", relation_type: "many_to_one", target_entity: "contact", inverse_key: "pals", inverse_label: "Pals" });
      await post({ entity_type: "contact", key: "pal_id", label: "Pal", field_type: "relation", relation_type: "many_to_one", target_entity: "company", inverse_key: "email", inverse_label: "Email" });
      await post({ entity_type: "contact", key: "partner_company_id", label: "Again", field_type: "relation", relation_type: "many_to_one", target_entity: "company", inverse_key: "partners2", inverse_label: "P" });
      await post({ entity_type: "contact", key: "partner2_id", label: "Again", field_type: "relation", relation_type: "many_to_one", target_entity: "company", inverse_key: "partner_contacts", inverse_label: "P" });
      await post({ entity_type: "deal", key: "bad_bind", label: "X", custom_field: { a: 1 } });
      await post({ entity_type: "deal", key: "bad_bind", label: "X", position: [1] });
      // Bodies that aren't an object.
      await post("not json");
      await post("");
      await post("null");
      await post("[]");
      await post("5");
      await post('"x"');
      await post(BOM + JSON.stringify({ entity_type: "deal", key: "bom", label: "BOM" }));
      await list("?entity=deal");

      // Update: label, options, position and widget; key and type stay.
      await put(nick, { label: "Nick name", options: { placeholder: "e.g. Al", max: 40 }, position: 5, custom_field: "clawnify::text", key: "ignored", field_type: "integer" });
      await put(nick, { label: null, options: null, position: null, custom_field: null });
      await put(nick, {});
      await put(nick, "[]");
      await put(nick, "5");
      await put(nick, '"x"');
      await put(tier, { options: { enum: ["gold", "silver", "bronze"] } });
      await put(partner, { label: "Partner co" });
      await put(mentees, { position: 0 });
      await put(nick, JSON.stringify({ label: "Sent as text" }), { "Content-Type": "text/plain" });
      await put("nope", { label: "x" });
      // A null body throws past the 404 check; malformed JSON before it.
      await put(nick, "null");
      await put("nope", "null");
      await put(nick, "not json");
      await put("nope", "not json");
      await put(nick, "");
      await put(nick, { label: { a: 1 } });
      await put(nick, { position: "7" });
      await put(bound, { label: 7, custom_field: false, position: 2.5 });
      await put(bound, BOM + "{}");
      await list();

      // Delete: a relation goes with its other side.
      await del(nick);
      await del(nick);
      await del("nope");
      await del(partner);
      await list("?entity=company");
      await del(mentees);
      await call("GET", "/api/contacts/contact-alex");

      // Not routes.
      await call("GET", `/api/custom-fields/${tier}`);
      await call("POST", `/api/custom-fields/${tier}`, { body: {} });
      await call("PATCH", `/api/custom-fields/${tier}`, { body: {} });
      await call("DELETE", "/api/custom-fields");
      await call("PUT", "/api/custom-fields/", { body: {} });
      await call("GET", "/api/custom-fields/");

      for (const id of [tier, seats, vip, meta, odd, sameKey, plain, bound]) await del(id);
      await list();
      await call("GET", "/api/companies/company-cedar");
    },
  },
  {
    name: "csv import",
    run: async (call) => {
      const contacts = (body, headers) => call("POST", "/api/contacts/import", { body, headers });
      const companies = (body, headers) => call("POST", "/api/companies/import", { body, headers });
      const def = async (body) => (await call("POST", "/api/custom-fields", { body })).body?.def?.id ?? "missing-def";
      const defs = [
        await def({ entity_type: "contact", key: "tier", label: "Tier", field_type: "enumeration", options: { enum: ["gold", "silver"] } }),
        await def({ entity_type: "contact", key: "seats", label: "Seats", field_type: "integer" }),
        await def({ entity_type: "contact", key: "vip", label: "VIP", field_type: "boolean" }),
        await def({ entity_type: "contact", key: "profile", label: "Profile", field_type: "json" }),
        await def({ entity_type: "contact", key: "score", label: "Score", field_type: "decimal" }),
        await def({ entity_type: "contact", key: "buddy_id", label: "Buddy", field_type: "relation", relation_type: "many_to_one", target_entity: "company", inverse_key: "buddies", inverse_label: "Buddies" }),
        await def({ entity_type: "company", key: "region", label: "Region", field_type: "enumeration", options: { enum: ["emea", "amer"] } }),
        await def({ entity_type: "company", key: "founded", label: "Founded", field_type: "date" }),
      ];
      const listContacts = () => call("GET", "/api/contacts" + qs({ search: "Imported", sort: "first_name", order: "asc", limit: "100" }));
      const listCompanies = (page = "1") => call("GET", "/api/companies" + qs({ search: "parity", sort: "name", order: "asc", limit: "100", page }));

      // Contacts: what is refused before anything is written.
      await contacts("not json");
      await contacts("");
      await contacts("null");
      await contacts("[]");
      await contacts("5");
      await contacts({});
      await contacts({ contacts: "x" });
      await contacts({ contacts: { 0: { first_name: "A" } } });
      await contacts({ contacts: [] });
      await contacts({ contacts: Array.from({ length: 2001 }, (_, i) => ({ first_name: `P${i}` })) });
      await contacts({ contacts: [{ last_name: "No first" }, { first_name: "   " }, { first_name: 0 }, 5, "x", [], {}] });
      await contacts({ contacts: [null] });
      await contacts({ contacts: [{ first_name: "A" }, null] });
      await contacts({ contacts: [{ first_name: "A", email: 5 }] });
      await contacts({ contacts: [{ first_name: 5, company: 7 }] });
      await contacts({ contacts: [{ first_name: 5, last_name: 6 }] });
      await contacts({ contacts: [{ first_name: "A", status: ["lead"] }] });
      await contacts({ contacts: [{ first_name: "A", company_phone: true }] });
      await contacts({ contacts: [{ first_name: "A", phone: 1, title: 2 }] });
      await contacts(BOM + JSON.stringify({ contacts: [{ first_name: "A" }] }));
      await listContacts();

      // A company whose domain an inferred one matches (case-insensitively).
      await call("POST", "/api/companies", { body: { name: "Parity Infer Co", domain: "Parity-Infer.Example" } });
      await contacts({
        inferCompanyFromEmail: true,
        contacts: [
          { first_name: " Pat ", last_name: " Imported ", email: " pat@parity-infer.example ", phone: " 1 ", title: " CTO ", status: " active " },
          { first_name: "Quinn", last_name: "Imported", email: "quinn@parityone.example", status: "Customer" },
          { first_name: "Rae", last_name: "Imported", email: "rae@www.paritytwo.example", status: "churned" },
          { first_name: "Sol", last_name: "Imported", email: "sol@gmail.com" },
          { first_name: "Tam", last_name: "Imported", email: "tam@parityone.example", company: " Parity Explicit ", company_domain: " explicit.example " },
          { first_name: "Uma", last_name: "Imported", company: "parity explicit", company_industry: " Widgets ", company_phone: "555", company_domain: "ignored.example" },
          { first_name: "Vic", last_name: "Imported", company: "northline studio" },
          { first_name: "Wes", last_name: "Imported", email: "wes@localhost" },
          { first_name: "Xan", last_name: "Imported", email: "x@PARITYONE.example" },
          { first_name: "", last_name: "Imported skipped" },
          { first_name: "Yul", last_name: "Imported", email: "yul@.parity" },
          { first_name: "Zoë", last_name: "Imported", company: "ÆRØ Parity" },
          { first_name: "Zia", last_name: "Imported", company: "ærø parity" },
          { first_name: "Ida", last_name: "Imported", company: "İnci Parity" },
          { first_name: "Ike", last_name: "Imported", company: "i̇nci parity" },
          { first_name: "Zed", last_name: "Imported", custom: { tier: "gold", seats: "12.7", vip: "yes", profile: { b: 1, a: [2, { d: 1, c: 2 }] }, score: "3.5", buddy_id: "company-cedar", unknown: "x" } },
          { first_name: "Ann", last_name: "Imported", custom: { tier: "bronze", seats: "abc", vip: 0, profile: "raw text", score: "" } },
          { first_name: "Bo", last_name: "Imported", custom: ["gold"] },
          { first_name: "Cy", last_name: "Imported", custom: "x" },
          { first_name: "Di", last_name: "Imported", custom: null, extra: 1 },
          7,
        ],
      });
      // Not opted in (only true is): no company from the email.
      await contacts({ inferCompanyFromEmail: "true", contacts: [{ first_name: "Eve", last_name: "Imported", email: "eve@parityone.example" }] });
      await listContacts();
      await listCompanies();
      // Sent as text/plain: still JSON to c.req.json().
      await contacts(JSON.stringify({ contacts: [{ first_name: "Flo", last_name: "Imported" }] }), { "Content-Type": "text/plain" });

      // Past D1's 100 parameters: 110 company names (lookups of 100 + 10,
      // inserts of 20 rows), 130 contacts (12 rows a statement, fewer with a
      // custom column).
      const bulk = Array.from({ length: 130 }, (_, i) => ({
        first_name: `Bulk${String(i).padStart(3, "0")}`,
        last_name: "Imported",
        company: `Parity Bulk ${String(i % 110).padStart(3, "0")}`,
        company_domain: `bulk${i % 110}.example`,
        ...(i % 7 === 0 ? { custom: { seats: i, tier: i % 2 ? "gold" : "silver" } } : {}),
      }));
      await contacts({ contacts: bulk });
      // Their companies exist now: reused, none created.
      await contacts({ contacts: bulk.slice(0, 3).map((r) => ({ ...r, first_name: `${r.first_name}b`, company: r.company.toUpperCase() })) });
      const imported = await call("GET", "/api/contacts" + qs({ search: "Imported", sort: "first_name", order: "asc", limit: "100", page: "1" }));
      const imported2 = await call("GET", "/api/contacts" + qs({ search: "Imported", sort: "first_name", order: "asc", limit: "100", page: "2" }));
      await call("GET", "/api/contacts" + qs({ search: "Bulk00", sort: "first_name", order: "asc" }));

      // Companies: refused before anything is written.
      await companies("not json");
      await companies("");
      await companies("null");
      await companies("[]");
      await companies({});
      await companies({ companies: "x" });
      await companies({ companies: [] });
      await companies({ companies: Array.from({ length: 2001 }, (_, i) => ({ name: `P${i}` })) });
      await companies({ companies: [{}, { name: "  " }, { name: 0 }, 5, "x", { domain: "x.example" }] });
      await companies({ companies: [null] });
      await companies({ companies: [{ name: "A" }, null] });
      await companies({ companies: [{ name: 5 }] });
      await companies({ companies: [{ name: "Parity Field Err", domain: 5 }] });
      await companies({ companies: [{ name: "Parity Field Err", notes: ["x"] }] });
      await companies(BOM + JSON.stringify({ companies: [{ name: "Parity BOM" }] }));
      await listCompanies();
      // Dedupe: first row per name wins; names that exist are skipped. A later
      // duplicate's other fields are never read.
      await companies({
        companies: [
          { name: " Parity Alpha ", domain: " alpha.example ", industry: " SaaS ", phone: " 1 ", email: " a@alpha.example ", notes: " n ", custom: { region: "emea", founded: "2020-01-01", unknown: 1 } },
          { name: "parity alpha", domain: 5 },
          { name: "Cedar & Co" },
          { name: "CEDAR & CO" },
          { name: "northline studio" },
          { name: "" },
          { domain: "x.example" },
          7,
          { name: "Parity Beta", custom: { region: "apac", founded: 20200101 } },
          { name: "Parity Gamma", custom: ["x"] },
          { name: "Zoë Parity & Co <b>", custom: { region: null, founded: "" } },
          { name: "Parity Explicit", domain: "again.example" },
        ],
      });
      await companies({ companies: Array.from({ length: 150 }, (_, i) => ({ name: `Parity Many ${String(i).padStart(3, "0")}`, ...(i % 10 === 0 ? { custom: { region: "amer" } } : {}) })) });
      await companies({ companies: [{ name: "Parity Plain" }] }, { "Content-Type": "text/plain" });
      const cos = await listCompanies("1");
      const cos2 = await listCompanies("2");
      const cos3 = await listCompanies("3");

      // Not routes, or another route's.
      await call("GET", "/api/contacts/import");
      await call("PUT", "/api/contacts/import", { body: { first_name: "x" } });
      await call("DELETE", "/api/contacts/import");
      await call("PATCH", "/api/contacts/import", { body: {} });
      await call("POST", "/api/contacts/import/", { body: { contacts: [] } });
      await call("GET", "/api/companies/import");
      await call("PUT", "/api/companies/import", { body: { name: "x" } });
      await call("DELETE", "/api/companies/import");
      await call("POST", "/api/companies/import/x", { body: {} });

      // Clean up: the imported contacts, then the companies.
      const contactIds = [...(imported.body?.contacts ?? []), ...(imported2.body?.contacts ?? [])].map((c) => c.id);
      await call("POST", "/api/contacts/bulk-delete", { body: { ids: contactIds.length ? contactIds : ["none"] } });
      await listContacts();
      const companyIds = [...(cos.body?.companies ?? []), ...(cos2.body?.companies ?? []), ...(cos3.body?.companies ?? [])].map((c) => c.id);
      await call("POST", "/api/companies/bulk-delete", { body: { ids: companyIds.length ? companyIds : ["none"] } });
      await listCompanies();
      for (const id of defs) await call("DELETE", `/api/custom-fields/${id}`);
      await call("GET", "/api/custom-fields");
      await call("GET", "/api/stats");
    },
  },
  {
    name: "views",
    run: async (call) => {
      const views = (q) => call("GET", "/api/views" + q);
      const create = (body, headers) => call("POST", "/api/views", { body, headers });
      const patch = (id, body, headers) => call("PATCH", `/api/views/${id}`, { body, headers });
      const del = (id) => call("DELETE", `/api/views/${id}`);
      const fields = (id) => call("GET", `/api/views/${id}/fields`);
      const setField = (id, key, body, headers) => call("PUT", `/api/views/${id}/fields/${key}`, { body, headers });
      const viewOf = (res) => res.body?.view?.id ?? "missing-view";

      await views("");
      await views("?entity=");
      await views("?entity=bogus");
      await views("?entity=Contact");
      // The list's default view is made on first use, once.
      const first = await views("?entity=contact");
      const all = first.body?.views?.[0]?.id ?? "missing-default";
      await views("?entity=contact");
      await views("?entity=contact&entity=company");
      const coFirst = await views("?entity=company");
      const allCompanies = coFirst.body?.views?.[0]?.id ?? "missing-default";
      await views("?entity=deal&x=1");

      // Columns of a view: one row per changed column.
      await fields(all);
      await fields("nope");
      await setField("nope", "email", { visible: false });
      await setField("nope", "Bad-Key", {});
      await setField(all, "Bad-Key", {});
      await setField(all, "_x", {});
      await setField(all, "x%20y", {});
      await setField(all, "email", { visible: "no" });
      await setField(all, "email", { visible: null });
      await setField(all, "email", { visible: 1 });
      for (const size of [39, 2001, 40.5, "100", null, true, -50]) await setField(all, "email", { size });
      for (const aggregate of ["bogus", 5, true, "constructor", ""]) await setField(all, "email", { aggregate });
      await setField(all, "email", { visible: false, size: 40, aggregate: "count_unique" });
      await setField(all, "email", { size: 2000 });
      await setField(all, "email", { aggregate: null });
      await setField(all, "email", {});
      await setField(all, "email", { visible: true, aggregate: "percent_empty" });
      // A body that isn't JSON is {} here.
      await setField(all, "phone", "not json");
      await setField(all, "title", "[]");
      await setField(all, "status", '"x"');
      await setField(all, "company_id", "", { "Content-Type": "text/plain" });
      await setField(all, "first_name", { size: 1e3, aggregate: "sum", extra: 1 });
      await setField(all, "a", { visible: false });
      await setField(all, "b", BOM + JSON.stringify({ visible: false, size: 100 }));
      await setField(all, "tier_2", { aggregate: "latest" });
      // JSON null still throws (Hono's plain 500).
      await setField(all, "notes", "null");
      await fields(all);
      await setField(allCompanies, "name", { size: 300 });
      await fields(allCompanies);

      // Create: a body that isn't JSON is {} (Review Focus 4).
      await create("not json");
      await create("");
      await create("[]");
      await create('"contact"');
      await create("5");
      await create({});
      await create({ entity: 5, name: "x" });
      await create({ entity: "Contact", name: "x" });
      await create({ entity: "task", name: "x" });
      await create({ entity: "contact" });
      await create({ entity: "contact", name: "   " });
      await create({ entity: "contact", name: 5 });
      await create({ entity: "contact", name: "x".repeat(61) });
      await create({ entity: "contact", name: "🎉".repeat(31) });
      await create({ entity: "contact", name: "x", filters: "[]" });
      await create({ entity: "contact", name: "x", filters: {} });
      await create({ entity: "contact", name: "x", filters: 5 });
      await create({ entity: "contact", name: "x", filters: [{ field: "email", op: "contains", value: "y".repeat(20000) }] });
      await create({ entity: "contact", name: "x", filters: [{ field: "email", op: "is", value: Array.from({ length: 61 }, (_, i) => `v${i}`) }] });
      await create({ entity: "contact", name: "x", filters: Array.from({ length: 31 }, () => ({ field: "created_at", op: "on", value: "2026-01-01" })) });
      await create("null");
      await create(BOM + JSON.stringify({ entity: "contact", name: "BOM" }));
      await views("?entity=contact");

      const leads = viewOf(await create({
        entity: "contact", name: "  Leads  ",
        filters: [{ field: "status", op: "is", value: "lead" }, { logic: "or", rules: [{ op: "contains", field: "email", value: "@" }, { field: "title", op: "is_not_empty" }] }],
        sort: "first_name", order: "asc", from: all,
      }));
      const long = viewOf(await create({ entity: "contact", name: "x".repeat(60), filters: null, sort: "Bad Sort", order: "ASC" }));
      const odd = viewOf(await create({
        entity: "contact", name: "Zoë 🎉 & <b>", filters: [{ field: "nope", op: "is", value: 1 }, 5, null, "x", { b: 2, a: 1, "1": 0 }],
        sort: "count:deals", order: "desc", from: 5, icon: "star", position: 99,
      }));
      const emoji = viewOf(await create({ entity: "contact", name: "🎉".repeat(30), from: "nope" }));
      const big = viewOf(await create({ entity: "company", name: "Big accounts", sort: "count:contacts", from: leads }));
      const plain = viewOf(await create(JSON.stringify({ entity: "deal", name: "Plain", filters: [{ field: "value", op: "gt", value: 1000 }], from: allCompanies }), { "Content-Type": "text/plain" }));
      await views("?entity=contact");
      await views("?entity=company");
      await views("?entity=deal");
      await fields(leads);
      await fields(big);
      await fields(plain);

      // Update: rename, or the filters and sort; the default view is locked.
      await patch("nope", { name: "x" });
      await patch("nope", "not json");
      await patch(leads, "not json");
      await patch(leads, "");
      await patch(leads, "5");
      await patch(leads, '"x"');
      await patch(leads, {});
      await patch(leads, { name: " Hot leads " });
      await patch(leads, { name: "" });
      await patch(leads, { name: null });
      await patch(leads, { name: "x".repeat(61) });
      await patch(leads, { filters: "x" });
      await patch(leads, { filters: null });
      await patch(leads, { name: "Renamed", filters: "bad" });
      await patch(leads, { filters: Array.from({ length: 61 }, (_, i) => ({ field: "email", op: "is", value: `v${i}` })) });
      await patch(leads, { filters: [], sort: null, order: null });
      await patch(leads, { filters: [{ field: "title", op: "is_not_empty" }], sort: "count:deals", order: "desc", icon: "x" });
      await patch(leads, { sort: "Bad", order: "up" });
      await patch(leads, { sort: 5, order: 5 });
      await patch(leads, JSON.stringify({ order: "asc" }), { "Content-Type": "text/plain" });
      await patch(leads, BOM + JSON.stringify({ name: "BOM" }));
      // An array body has a `sort` (Array.prototype.sort), which isn't a string.
      await patch(long, { sort: "email", order: "asc" });
      await patch(long, "[]");
      await patch(all, "[]");
      await patch(all, { name: " Everyone " });
      await patch(all, { filters: [] });
      await patch(all, { sort: null });
      await patch(all, { order: "asc", name: "x" });
      await patch(all, { name: "" });
      await patch(all, "null");
      await patch(leads, "null");
      await patch(all, { name: "All contacts" });
      await views("?entity=contact");

      // Delete: the default view stays.
      await del("nope");
      await del(all);
      await del(leads);
      await del(leads);
      await fields(leads);
      await setField(leads, "email", {});

      // Not routes.
      await call("GET", `/api/views/${all}`);
      await call("PUT", `/api/views/${all}`, { body: {} });
      await call("POST", `/api/views/${all}`, { body: {} });
      await call("PATCH", "/api/views", { body: {} });
      await call("DELETE", "/api/views");
      await call("GET", "/api/views/");
      await call("PUT", `/api/views/${all}/fields`, { body: {} });
      await call("POST", `/api/views/${all}/fields`, { body: {} });
      await call("GET", `/api/views/${all}/fields/email`);
      await call("DELETE", `/api/views/${all}/fields/email`);
      await call("PUT", `/api/views/${all}/fields/`, { body: {} });
      await call("PUT", `/api/views/${all}/fields/email/x`, { body: {} });
      await call("GET", `/api/views/${all}/fields/`);

      for (const id of [long, odd, emoji, big, plain]) await del(id);
      await views("?entity=contact");
      await views("?entity=company");
      await views("?entity=deal");
      await fields(all);
    },
  },
];
