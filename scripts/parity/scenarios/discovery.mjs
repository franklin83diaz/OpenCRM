// Discovery routes: llms.txt, the OpenAPI document, and the not-found shapes.
export default [
  {
    name: "discovery",
    run: async (call) => {
      await call("GET", "/llms.txt");
      await call("GET", "/api/openapi.json");
      await call("GET", "/api/nope");
      await call("GET", "/api/nope", { headers: { "X-Clawnify-Caller": "user" } });
      await call("PATCH", "/api/companies");
      await call("GET", "/nope");
    },
  },
];
