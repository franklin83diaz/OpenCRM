// Customers area: how each customer is doing (GET /api/customers) and what the
// CRM knows about an address (GET and POST /api/lookup). Neither backend has a
// Clawnify token here, so the meeting and email syncs read as off, and no
// route ported so far writes companies, tasks, meetings or insights: these run
// on the seed (three companies, one won deal), which the routes' backfills turn
// into one customer (Northline, from its contact's won deal). They cover the
// backfills, the tz clamp, zod on the query and the body (incl. "Required",
// too_small and too_big), the 400/500 branches, the body validated only when
// the request says it is JSON, and a batch past one IN chunk (90). The
// populated cases (statuses, reasons, focus, upcoming calls, deals' progress,
// domain matching) are the TS-replay test in server/internal/crm.

const json = (headers = {}) => ({ ...headers, "Content-Type": "application/json" });

export default [
  {
    name: "customers overview",
    run: async (call) => {
      // The first read gives Northline its customer_since (its contact's won deal).
      await call("GET", "/api/customers");
      await call("GET", "/api/customers", { headers: { "X-Clawnify-Caller": "agent" } });
      for (const tz of ["120", "-300", "90.5", "abc", "", "99999", "-99999", "Infinity", "-Infinity", "0x1F", "1e3", "%20120%20", "-0", "+60", "1_0"]) {
        await call("GET", `/api/customers?tz=${tz}`);
      }
      await call("GET", "/api/customers?tz");
      await call("GET", "/api/customers?TZ=5&other=1");
      // zod on the query (OpenAPI route): a parameter sent twice is an array.
      await call("GET", "/api/customers?tz=1&tz=2");
      await call("GET", "/api/customers?tz=1&tz=");
      // Other methods and paths are not routes.
      await call("POST", "/api/customers", { body: {} });
      await call("GET", "/api/customers/company-northline");
      await call("GET", "/api/customers/");
    },
  },
  {
    name: "lookup one address",
    run: async (call) => {
      const get = (q) => call("GET", "/api/lookup" + q);
      await get("?email=alex@northline.example");
      await get("?email=%20ALEX@Northline.EXAMPLE%20");
      await get("?email=+alex@northline.example+");
      await get("?email=jamie@harbour.example");
      await get("?email=sam@cedar.example&domain=gmail.com");
      await get("?email=alex@northline.example&domain=harbour.example");
      await get("?email=nobody@nowhere.example");
      await get("?email=someone@gmail.com");
      await get("?domain=northline.example");
      await get("?domain=https://www.Northline.example/about");
      await get("?domain=gmail.com");
      await get("?email=alex");
      await get("?email=@northline.example");
      await get("?email=%E0%A4%A");
      await get("?email=Zo%C3%AB@cedar.example");
      // Neither given: 400.
      await get("");
      await get("?email=");
      await get("?email=%20%20&domain=+");
      await get("?other=alex@northline.example");
      await get("?email");
      // zod on the query: a parameter sent twice is an array.
      await get("?email=alex@northline.example&email=x");
      await get("?email=a&domain=b&domain=c");
      await get("?domain=a&domain=b&email=x&email=y");
      await call("GET", "/api/lookup", { headers: { "X-Clawnify-Caller": "public" } });
      await call("PUT", "/api/lookup", { body: {} });
      await call("GET", "/api/lookup/alex@northline.example");
    },
  },
  {
    name: "lookup many addresses",
    run: async (call) => {
      const post = (body, headers = json()) => call("POST", "/api/lookup", { body, headers });
      await post({ addresses: [{ email: "alex@northline.example" }] });
      await post({
        addresses: [
          { email: "ALEX@northline.example ", domain: "" },
          { email: "jamie@harbour.example", extra: 1 },
          {},
          { domain: "gmail.com" },
          { email: "nobody@nowhere.example", domain: "harbour.example" },
          { email: "alex@northline.example" },
          { email: "sam@cedar.example", domain: "https://www.cedar.example/" },
        ],
      });
      // A batch of 100: more distinct addresses than one IN list (90) holds.
      const many = Array.from({ length: 100 }, (_, i) =>
        i % 10 === 0 ? { email: ["alex@northline.example", "jamie@harbour.example", "sam@cedar.example"][i % 3] } : { email: `person${i}@company${i % 7}.example` });
      await post({ addresses: many });
      // zod on the body (OpenAPI route).
      await post({ addresses: [] });
      await post({ addresses: Array.from({ length: 101 }, () => ({})) });
      await post({ addresses: Array.from({ length: 101 }, (_, i) => (i === 3 ? 5 : { email: i })) });
      await post({});
      await post({ other: [] });
      await post({ addresses: null });
      await post({ addresses: {} });
      await post({ addresses: "alex@northline.example" });
      await post({ addresses: [{ email: 5, domain: null }, "x", [], { email: "a@b.c", extra: 1 }, null, { domain: true }] });
      await post("null");
      await post("[]");
      await post("5");
      await post('"x"');
      await post("true");
      await post("{");
      await post("");
      await post("nope");
      // The body is validated only when the request says it is JSON; otherwise
      // the handler got {} and threw reading addresses.map.
      const body = JSON.stringify({ addresses: [{ email: "alex@northline.example" }] });
      await post(body, { "Content-Type": "text/plain" });
      await post(body, { "Content-Type": "Application/JSON" });
      await post(body, { "Content-Type": "application/json;" });
      await post(body, { "Content-Type": "application/jsonx" });
      await post(body, { "Content-Type": "application/json; charset=utf-8" });
      await post(body, { "Content-Type": "application/vnd.api+json" });
      await post(body, { "Content-Type": "multipart/form-data; boundary=x" });
      await post("nope", { "Content-Type": "text/plain" });
      await call("POST", "/api/lookup");
      await call("POST", "/api/lookup", { headers: { "X-Clawnify-Caller": "public" }, body: { addresses: [{ email: "sam@cedar.example" }] } });
    },
  },
];
