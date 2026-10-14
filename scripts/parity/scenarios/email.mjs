// Email area: integrations status/email/meeting, Gmail sync settings, labels,
// runs, a contact's emails and opening one. Neither backend has a Clawnify
// token here, so nothing is connected: these cover the not-connected, off,
// validation, auth (403) and 404 paths. The connected paths are Go unit tests.

const user = { "X-Clawnify-Caller": "user", "X-Clawnify-User-Id": "user-1", "X-Clawnify-User-Email": "ada@example.com" };
const userNoId = { "X-Clawnify-Caller": "user" };
const api = { "X-Clawnify-Caller": "api", "X-Clawnify-User-Id": "user-1" };
const agent = { "X-Clawnify-Caller": "agent" };
const json = (headers = {}) => ({ ...headers, "Content-Type": "application/json" });

export default [
  {
    name: "integrations status",
    run: async (call) => {
      await call("GET", "/api/integrations/status");
      await call("GET", "/api/integrations/status", { headers: user });
    },
  },
  {
    name: "integrations email validation",
    run: async (call) => {
      const post = (body) => call("POST", "/api/integrations/email", { body, headers: json() });
      await post("");
      await post("null");
      await post("not json");
      await post({});
      await post([]);
      await post({ reply_to: "x" });
      await post({ reply_to: null });
      await post({ forward: { mailbox: 1, id: "x" } });
      await post({ forward: { mailbox: "a@b.co" } });
      await post({ reply_to: { mailbox: "a@b.co", id: "1" }, forward: { mailbox: "a@b.co", id: "2" } });
      await post({ to: "ada@example.com" });
      await post({ to: {} });
      await post({ to: 5 });
      await post({ to: ["ok@example.com"], cc: ["nope"] });
      await post({ to: ["ok@example.com"], bcc: [5] });
      await post({ to: ["ok@example.com"], bcc: [1.5e21] });
      await post({ to: [null] });
      await post({ to: [{}] });
      await post({ to: [["a@b.co", "c@d.co"]] });
      await post({ to: ["a@b"] });
      await post({ to: ["a b@c.co"] });
      await post({ to: ["<a@b.co>"] });
      await post({ to: ["ok@example.com"], cc: null, bcc: null });
      await post({ contact_id: "nope" });
      await post({ contact_id: "  contact-alex  " });
      await post({ contact_id: "contact-alex", subject: "Hi" });
      await post({ contact_id: "contact-alex", body: "Hello there" });
      await post({ to: [" ADA@Example.com "], subject: "   ", body: " " });
      await post({ to: ["ada@example.com", "ADA@example.com"], subject: "Dupes" });
      await post({ to: Array.from({ length: 51 }, (_, i) => `p${i}@example.com`), subject: "Many" });
      await post({ to: Array.from({ length: 30 }, (_, i) => `p${i}@example.com`), cc: Array.from({ length: 21 }, (_, i) => `c${i}@example.com`), subject: "Many" });
      await post({ to: Array.from({ length: 60 }, () => "same@example.com"), subject: "One" });
      await post({ to: Array.from({ length: 50 }, (_, i) => `p${i}@example.com`), contact_id: "contact-alex", subject: "Over by one" });
      await post({ to: ["a@b.co"], reply_to: { mailbox: " X@Y.co ", id: " m1 " } });
      await post({ to: ["a@b.co"], forward: { mailbox: "x@y.co", id: "m1" }, body: "fyi" });
      await post({ to: ["a@b.co"], reply_to: { mailbox: "x@y.co", id: "m1" }, body: "" });
      await post({ to: ["a@b.co"], subject: 5 });
      await post({ to: ["a@b.co"], subject: "x", body: true });
      await post({ contact_id: 7, to: ["a@b.co"] });
      await post({ contact_id: "", to: ["a@b.co"], subject: "No contact" });
      await post({ contact_id: 0, to: ["a@b.co"], subject: "Falsy contact" });
    },
  },
  {
    name: "integrations meeting validation",
    run: async (call) => {
      const post = (body) => call("POST", "/api/integrations/meeting", { body, headers: json() });
      await post("");
      await post("null");
      await post("{");
      await post({});
      await post({ contact_id: "  " });
      await post({ contact_id: "contact-alex" });
      await post({ contact_id: "contact-alex", summary: " Intro " });
      await post({ contact_id: "contact-alex", summary: "Intro", start_datetime: "  " });
      await post({ contact_id: "nope", summary: "Intro", start_datetime: "2026-10-15T10:00:00" });
      await post({ contact_id: "contact-alex", summary: "Intro", start_datetime: "2026-10-15T10:00:00" });
      await post({ contact_id: "contact-alex", summary: "Intro", start_datetime: "2026-10-15T10:00:00", timezone: "Europe/Madrid", duration_minutes: 90 });
      await post({ contact_id: 5 });
      await post({ contact_id: "contact-alex", summary: ["x"] });
      await post({ contact_id: "contact-alex", summary: "Intro", start_datetime: "2026-10-15T10:00:00", timezone: 3 });
    },
  },
  {
    name: "email sync settings",
    run: async (call) => {
      await call("GET", "/api/email-sync");
      await call("GET", "/api/email-sync", { headers: user });
      await call("GET", "/api/email-sync", { headers: userNoId });
      await call("GET", "/api/email-sync", { headers: api });
      await call("GET", "/api/email-sync?check=1", { headers: user });
      await call("GET", "/api/email-sync?check=1&check=0", { headers: user });

      await call("PUT", "/api/email-sync", { body: { enabled: true } });
      await call("PUT", "/api/email-sync", { body: { enabled: true }, headers: userNoId });
      await call("PUT", "/api/email-sync", { body: { enabled: true }, headers: api });
      await call("PUT", "/api/email-sync", { body: { enabled: true }, headers: { ...agent, "X-Clawnify-User-Id": "user-1" } });
      await call("PUT", "/api/email-sync", { body: { enabled: true }, headers: { ...user, "X-Clawnify-Caller": " user " } });
      await call("PUT", "/api/email-sync", { body: { enabled: true }, headers: { ...user, "X-Clawnify-User-Id": "  " } });
      await call("PUT", "/api/email-sync", { body: { enabled: true }, headers: user });
      await call("PUT", "/api/email-sync", { body: { history: "5y" }, headers: user });
      await call("PUT", "/api/email-sync", { body: "null", headers: json(user) });
      await call("PUT", "/api/email-sync", { body: "", headers: json(user) });
      await call("PUT", "/api/email-sync", { body: "nope", headers: json(user) });

      await call("GET", "/api/email-sync/labels");
      await call("GET", "/api/email-sync/labels", { headers: agent });
      await call("GET", "/api/email-sync/labels", { headers: user });
    },
  },
  {
    name: "email sync runs",
    run: async (call) => {
      const run = (opts) => call("POST", "/api/email-sync/run", opts);
      await run({});
      await run({ body: { mailbox: "ada@example.com" } });
      await run({ headers: { "X-Clawnify-Caller": "public" } });
      await run({ headers: { "X-Clawnify-Caller": "bypass" } });
      await run({ headers: { "X-Clawnify-Caller": "agent-browser" } });
      // A signature that is too old fails before any key is fetched.
      await run({ body: { mailbox: "ada@example.com" }, headers: { "X-Queue-Signature": "c2ln", "X-Queue-Timestamp": "1", "X-Queue-Key-Id": "k1" } });
      await run({ body: { mailbox: "ada@example.com" }, headers: { "X-Queue-Signature": "c2ln", "X-Queue-Timestamp": "soon" } });
      await run({ headers: user });
      await run({ headers: api, body: { mailbox: "nobody@example.com" } });
      await run({ headers: agent, body: "not json" });
      await run({ headers: agent, body: "null" });
      await run({ headers: agent, body: "[1]" });
      await run({ headers: agent, body: { mailbox: null } });
      await run({ headers: agent, body: { mailbox: "" } });
      await run({ headers: agent, body: { mailbox: 0 } });
      await run({ headers: agent, body: { mailbox: 5 } });
      await run({ headers: agent, body: { mailbox: true } });
      await run({ headers: agent, body: { mailbox: { a: 1 } } });
      await run({ headers: agent, body: { mailbox: ["x"] } });
      await run({ headers: agent, body: { mailbox: "ADA@example.com" } });

      const now = (opts) => call("POST", "/api/email-sync/sync-now", opts);
      await now({});
      await now({ headers: { "X-Clawnify-Caller": "public", "X-Clawnify-User-Id": "user-1" } });
      await now({ headers: { "X-Clawnify-Caller": "system" } });
      await now({ headers: { "X-Queue-Signature": "c2ln", "X-Queue-Timestamp": "1" } });
      await now({ headers: user });
      await now({ headers: agent, body: { mailbox: "ada@example.com" } });
      await now({ headers: api, body: "" });
    },
  },
  {
    name: "contact emails and opening one",
    run: async (call) => {
      await call("GET", "/api/contacts/contact-alex/emails");
      await call("GET", "/api/contacts/contact-alex/emails", { headers: user });
      await call("GET", "/api/contacts/nope/emails");
      await call("GET", "/api/contacts/a%20b/emails");

      await call("GET", "/api/emails/ada@example.com/m1");
      await call("GET", "/api/emails/ada%40example.com/m1");
      await call("GET", "/api/emails/ADA%40Example.COM/m1", { headers: user });
      await call("GET", "/api/emails/ada%2540example.com/m1");
      await call("GET", "/api/emails/%25E0%25A4%25A/m1");
      await call("GET", "/api/emails/a%2Fb/m1");
      await call("GET", "/api/emails/ada@example.com");
      await call("GET", "/api/emails/ada@example.com/m1/extra");
    },
  },
];
