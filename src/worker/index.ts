import { Container, ContainerProxy, getContainer } from "@cloudflare/containers";

// The Worker in front of the Go API. Static assets never reach it (wrangler.toml
// runs it first only for /api/* and /llms.txt); everything it gets goes to the
// single `Api` container, which serves the API.
//
// A container can't see Worker bindings, so the Worker bridges them: the Go
// server calls plain-HTTP hosts that only exist inside the container's network
// (`http://d1.internal`), and `outboundByHost` answers them here with env.DB.

// The container's outbound traffic is routed through this entrypoint.
export { ContainerProxy };

interface Env {
  API: DurableObjectNamespace<Api>;
  DB: D1Database;
}

export class Api extends Container<Env> {
  defaultPort = 8080;
  sleepAfter = "10m";
  envVars = { D1_URL: "http://d1.internal" };
}

// The D1 bridge. Wire protocol (fixed; the Go side is server/internal/db/d1.go):
//   POST /query {sql, params, mode: "all" | "run"}
//   → 200 {results, meta: {changes, last_row_id}}  |  500 {error: D1's message}
interface D1Query {
  sql: string;
  params?: unknown[];
  mode?: "all" | "run";
}

Api.outboundByHost = {
  "d1.internal": async (req: Request, env: Env) => {
    if (req.method !== "POST" || new URL(req.url).pathname !== "/query") {
      return Response.json({ error: `no ${req.method} ${new URL(req.url).pathname} on the D1 bridge` }, { status: 404 });
    }
    try {
      const { sql, params = [], mode = "all" } = await req.json<D1Query>();
      const stmt = env.DB.prepare(sql).bind(...params);
      const r = mode === "run" ? await stmt.run() : await stmt.all();
      return Response.json({
        results: r.results ?? [],
        meta: { changes: r.meta?.changes ?? 0, last_row_id: r.meta?.last_row_id ?? 0 },
      });
    } catch (e) {
      return Response.json({ error: e instanceof Error ? e.message : String(e) }, { status: 500 });
    }
  },
};

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    // The container sees the request arrive over plain HTTP from the Worker;
    // tell it the scheme and host the browser used, for links it builds.
    const url = new URL(request.url);
    const headers = new Headers(request.headers);
    headers.set("X-Forwarded-Host", url.host);
    headers.set("X-Forwarded-Proto", url.protocol.slice(0, -1));
    return getContainer(env.API, "api").fetch(new Request(request, { headers }));
  },
} satisfies ExportedHandler<Env>;
