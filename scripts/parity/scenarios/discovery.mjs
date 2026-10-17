// Discovery routes: llms.txt, the OpenAPI document, and the not-found shapes.
// The Go server has fewer routes than the TS reference (no integrations, no
// AI), so the documents listing them differ by design: those bodies aren't
// compared here (their status and content type are), and Go's own test
// (server/internal/crm/discovery_test.go) checks they list exactly its routes.
const notCompared = () => "(not compared: the Go CRM has fewer routes)";

export default [
  {
    name: "discovery",
    run: async (call) => {
      await call("GET", "/llms.txt", { compare: notCompared });
      await call("GET", "/api/openapi.json", { compare: notCompared });
      await call("GET", "/api/nope");
      await call("GET", "/api/nope", {
        headers: { "X-Clawnify-Caller": "user" },
        compare: ({ endpoints, ...rest }) => ({ ...rest, endpoints: Array.isArray(endpoints) }),
      });
      await call("PATCH", "/api/companies");
      await call("GET", "/nope");
    },
  },
];
