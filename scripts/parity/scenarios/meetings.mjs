// Meetings area: calendar + Granola sync settings, runs, the meetings list,
// one meeting, linking it and retrying its digest. Neither backend has a
// Clawnify token here, so the calendar, Granola and the model read as not
// connected, and no API path creates a meeting without them: these cover the
// off/not-connected paths, settings that save with sync off, validation (incl.
// zod on the OpenAPI routes), auth (403), 404/409 on unknown meetings, and
// route precedence (/api/meetings/sync before /api/meetings/{id}). The
// connected paths (sync pass, digest, linking stored meetings) are Go unit tests.

const user = { "X-Clawnify-Caller": "user", "X-Clawnify-User-Id": "user-1", "X-Clawnify-User-Email": "ada@example.com" };
const userNoEmail = { "X-Clawnify-Caller": "user", "X-Clawnify-User-Id": "user-2" };
const userNoId = { "X-Clawnify-Caller": "user" };
const api = { "X-Clawnify-Caller": "api", "X-Clawnify-User-Id": "user-1" };
const agent = { "X-Clawnify-Caller": "agent" };
const json = (headers = {}) => ({ ...headers, "Content-Type": "application/json" });

export default [
  {
    name: "meetings runs before any settings",
    run: async (call) => {
      await call("GET", "/api/meetings/sync");
      await call("POST", "/api/meetings/run", { headers: user });
      await call("POST", "/api/meetings/sync-now", { headers: agent });
    },
  },
  {
    name: "meetings sync settings",
    run: async (call) => {
      await call("GET", "/api/meetings/sync", { headers: user });
      await call("GET", "/api/meetings/sync", { headers: userNoId });
      await call("GET", "/api/meetings/sync", { headers: api });
      await call("GET", "/api/meetings/sync?check=1", { headers: agent });

      const put = (body, headers = user) => call("PUT", "/api/meetings/sync", { body, headers });
      await put({ enabled: true }, {});
      await put({ enabled: true }, userNoId);
      await put({ enabled: true }, api);
      await put({ enabled: true }, { ...agent, "X-Clawnify-User-Id": "user-1" });
      await put({ enabled: true }, { ...user, "X-Clawnify-User-Id": "  " });
      await put({ enabled: "yes" });
      await put({ enabled: null });
      await put({ enabled: 1 });
      await put({ history_days: 60 });
      await put({ history_days: "90" });
      await put({ history_days: null });
      await put({ history_days: 90.5 });
      await put({ about: 5 });
      await put({ about: null });
      await put({ about: "x".repeat(1001) });
      await put({ about: "😀".repeat(501) });
      await put({ enabled: "no", history_days: 7, about: 1 });
      await put({ enabled: true, history_days: 60 });
      await put({ enabled: true });
      await put({ enabled: true, history_days: 30, about: "x" });
      await put("null", json(user));
      await put("nope", json(user));
      await put("", json(user));
      await put("[1]", json(user));
      await put("5", json(user));
      await put('"text"', json(user));
      await put({ history_days: 30, about: "  We sell CRMs  " });
      await put({ enabled: false });
      await put({ history_days: 365, extra: 1 }, { ...user, "X-Clawnify-Caller": " user " });
      await put({ about: "😀".repeat(500) }, userNoEmail);
      await put({ about: "é".repeat(1000), history_days: 30.0 });
      await put({ about: "" });
      await put({ enabled: false, history_days: 90, about: "  Cedar & Co <tools> \n" });
      await call("GET", "/api/meetings/sync", { headers: user });
      await call("GET", "/api/meetings/sync");
    },
  },
  {
    name: "meetings runs",
    run: async (call) => {
      const run = (opts) => call("POST", "/api/meetings/run", opts);
      await run({});
      await run({ body: {} });
      await run({ headers: { "X-Clawnify-Caller": "public" } });
      await run({ headers: { "X-Clawnify-Caller": "bypass" } });
      await run({ headers: { "X-Clawnify-Caller": "agent-browser" } });
      await run({ headers: { "X-Clawnify-Caller": "system" } });
      await run({ headers: { "X-Clawnify-Caller": "app" } });
      // A signature that is too old fails before any key is fetched.
      await run({ body: {}, headers: { "X-Queue-Signature": "c2ln", "X-Queue-Timestamp": "1", "X-Queue-Key-Id": "k1" } });
      await run({ body: {}, headers: { "X-Queue-Signature": "c2ln", "X-Queue-Timestamp": "soon" } });
      await run({ headers: user });
      await run({ headers: api });
      await run({ headers: agent, body: "not json" });
      await run({ headers: { ...agent, "X-Clawnify-Caller": " agent " } });

      const now = (opts) => call("POST", "/api/meetings/sync-now", opts);
      await now({});
      await now({ headers: { "X-Clawnify-Caller": "public", "X-Clawnify-User-Id": "user-1" } });
      await now({ headers: { "X-Clawnify-Caller": "system" } });
      await now({ body: {}, headers: { "X-Queue-Signature": "c2ln", "X-Queue-Timestamp": "1", "X-Queue-Key-Id": "k1" } });
      await now({ headers: user });
      await now({ headers: api, body: { sync: false } });
      await now({ headers: agent, body: "[" });
    },
  },
  {
    name: "meetings list and get",
    run: async (call) => {
      const list = (q = "") => call("GET", "/api/meetings" + q);
      await list();
      await list("?company_id=company-northline");
      await list("?company_id=");
      await list("?link=unmatched");
      await list("?link=ignored&when=past");
      await list("?when=upcoming");
      await list("?when=past&limit=5&offset=10");
      await list("?limit=abc&offset=xyz");
      await list("?limit=0&offset=-1");
      await list("?limit=-3");
      await list("?limit=1e3&offset=2.9");
      await list("?limit=%2012px&offset=+4");
      await list("?link=un%6Datched");
      await list("?foo=bar&company_id=nope");
      // zod on the query (OpenAPI route): 400 with zod 3's issues.
      await list("?link=bad");
      await list("?when=later");
      await list("?link=bad&when=later");
      await list("?when=later&link=bad");
      await list("?link=");
      await list("?link");
      await list("?link=UNMATCHED");
      await list("?link=unmatched+");
      await list("?link=unmatched&link=ignored");
      await list("?company_id=a&company_id=b");
      await list("?limit=1&limit=2&offset=1&offset=2");
      await list("?company_id=a&company_id=b&link=x&when=y&limit=1&limit=2&offset=1&offset=2");
      await list("?link=%E0%A4%A");

      await call("GET", "/api/meetings", { headers: user });
      await call("GET", "/api/meetings/nope");
      await call("GET", "/api/meetings/nope", { headers: agent });
      // Route precedence: the literal routes registered first win over {id}.
      await call("GET", "/api/meetings/sync", { headers: agent });
      await call("GET", "/api/meetings/run");
      await call("GET", "/api/meetings/sync-now");
      await call("GET", "/api/meetings/a%2Fb");
      await call("GET", "/api/meetings/");
      await call("GET", "/api/meetings/x/digest");
      await call("POST", "/api/meetings/sync", { headers: user });
      await call("DELETE", "/api/meetings/nope", { headers: user });
      await call("PUT", "/api/meetings/nope", { headers: user, body: {} });
      await call("GET", "/api/meetings/nope/digest", { headers: user });
    },
  },
  {
    name: "meetings link and digest",
    run: async (call) => {
      const patch = (id, body, headers = user) => call("PATCH", `/api/meetings/${id}`, { body, headers });
      await patch("nope", {}, {});
      await patch("nope", { ignored: true }, {});
      await patch("nope", { ignored: true }, { "X-Clawnify-Caller": "public" });
      await patch("nope", { company_id: 5 }, {});
      await patch("nope", {});
      await patch("nope", { other: 1 });
      await patch("nope", { company_id: "nope" });
      await patch("nope", { company_id: "  " });
      await patch("nope", { company_id: "company-northline" });
      await patch("nope", { company_id: "company-northline" }, api);
      await patch("nope", { company_id: "company-northline" }, agent);
      await patch("nope", { company_id: null });
      await patch("nope", { company_id: "" });
      await patch("nope", { ignored: true });
      await patch("nope", { ignored: false });
      await patch("nope", { ignored: true, company_id: "nope" });
      await patch("nope", { ignored: false, company_id: null });
      // zod on the body (OpenAPI route).
      await patch("nope", { ignored: "yes" });
      await patch("nope", { ignored: null });
      await patch("nope", { company_id: 5 });
      await patch("nope", { company_id: ["x"] });
      await patch("nope", { company_id: { id: "x" } });
      await patch("nope", { company_id: false, ignored: 0 });
      await patch("nope", "null", json(user));
      await patch("nope", "[]", json(user));
      await patch("nope", "5", json(user));
      await patch("nope", '"x"', json(user));
      await patch("nope", "true", json(user));
      await patch("nope", "nope", json(user));
      await patch("nope", "", json(user));
      await patch("nope", "{", json({}));
      // The body is read only when it says it is JSON.
      await patch("nope", JSON.stringify({ ignored: true }), { ...user, "Content-Type": "text/plain" });
      await patch("nope", JSON.stringify({ ignored: true }), { ...user, "Content-Type": "Application/JSON" });
      await patch("nope", JSON.stringify({ ignored: true }), { ...user, "Content-Type": "application/json;" });
      await patch("nope", JSON.stringify({ ignored: true }), { ...user, "Content-Type": "application/json; charset=utf-8" });
      await patch("nope", JSON.stringify({ ignored: true }), { ...user, "Content-Type": "application/vnd.api+json" });
      await patch("nope", "nope", { ...user, "Content-Type": "text/plain" });
      await call("PATCH", "/api/meetings/nope", { headers: user });
      await patch("sync", { ignored: true });
      await patch("a%2Fb", { ignored: true });

      const digest = (id, headers) => call("POST", `/api/meetings/${id}/digest`, { headers });
      await digest("nope", {});
      await digest("nope", { "X-Clawnify-Caller": "public" });
      await digest("nope", user);
      await digest("nope", api);
      await digest("nope", agent);
      await digest("run", user);
      await call("POST", "/api/meetings/nope/digest", { headers: user, body: "not json" });
    },
  },
];
