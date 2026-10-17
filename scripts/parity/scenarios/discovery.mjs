// Discovery routes: llms.txt, the OpenAPI document, and the not-found shapes.
// The Go server has other routes than the TS reference (no integrations or
// AI; its own accounts), so the documents listing them differ by design:
// those bodies aren't compared here (their status and content type are), the
// runner compares a not-found's endpoint list only for being there, and Go's
// own test (server/internal/crm/discovery_test.go) checks they list exactly
// its routes. Every call is signed in (see runner.mjs): what an anonymous
// caller gets is Go's test too (server/internal/crm/routes_auth_test.go).
const notCompared = () => "(not compared: the Go CRM has other routes)";

export default [
  {
    name: "discovery",
    run: async (call) => {
      await call("GET", "/llms.txt", { compare: notCompared });
      await call("GET", "/api/openapi.json", { compare: notCompared });
      await call("GET", "/api/nope");
      await call("PATCH", "/api/companies");
      await call("GET", "/nope");
    },
  },
];
