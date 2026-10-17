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
  // The first admin of a CRM with no accounts yet: `wrangler secret put` in
  // production, .dev.vars under `wrangler dev`. The Go server creates the
  // account on its first sign-in and ignores them once accounts exist.
  ADMIN_EMAIL?: string;
  ADMIN_PASSWORD?: string;
}

export class Api extends Container<Env> {
  defaultPort = 8080;
  sleepAfter = "10m";
  envVars = containerEnv(this.env);
  // The Go server calls nothing on the internet: its only outbound requests
  // go to d1.internal, which outboundByHost below still answers.
  enableInternet = false;
}

// The container's environment, fixed when it starts: a changed secret reaches
// it at its next start.
function containerEnv(env: Env): Record<string, string> {
  const vars: Record<string, string> = { D1_URL: "http://d1.internal" };
  if (env.ADMIN_EMAIL) vars.ADMIN_EMAIL = env.ADMIN_EMAIL;
  if (env.ADMIN_PASSWORD) vars.ADMIN_PASSWORD = env.ADMIN_PASSWORD;
  return vars;
}

// The D1 bridge. Wire protocol (fixed; the Go side is server/internal/db/d1.go):
//   POST /query {sql, params, mode: "all" | "run"}
//   → 200 {results, meta: {changes, last_row_id}}  |  500 {error: D1's message}
// JSON has no NaN or Infinity, so a param that is one arrives as
// {"$num": "NaN" | "Infinity" | "-Infinity"} and is bound as that number, as
// the TS app bound it.
interface D1Query {
  sql: string;
  params?: unknown[];
  mode?: "all" | "run";
}

const NON_FINITE: Record<string, number> = { NaN: NaN, Infinity: Infinity, "-Infinity": -Infinity };

function d1Param(p: unknown): unknown {
  if (p !== null && typeof p === "object" && !Array.isArray(p)) {
    const keys = Object.keys(p);
    const num = (p as { $num?: unknown }).$num;
    if (keys.length === 1 && keys[0] === "$num" && typeof num === "string" && Object.hasOwn(NON_FINITE, num)) {
      return NON_FINITE[num];
    }
  }
  return p;
}

Api.outboundByHost = {
  "d1.internal": async (req: Request, env: Env) => {
    if (req.method !== "POST" || new URL(req.url).pathname !== "/query") {
      return Response.json({ error: `no ${req.method} ${new URL(req.url).pathname} on the D1 bridge` }, { status: 404 });
    }
    try {
      const { sql, params = [], mode = "all" } = await req.json<D1Query>();
      const stmt = env.DB.prepare(sql).bind(...params.map(d1Param));
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
    // tell it the scheme and host the browser used, for links it builds and
    // to tell its own pages' requests from other sites'. Set here, so what a
    // client sends in these headers never gets through.
    const url = new URL(request.url);
    const headers = new Headers(request.headers);
    headers.set("X-Forwarded-Host", url.host);
    headers.set("X-Forwarded-Proto", url.protocol.slice(0, -1));
    // The visitor's address, for the login limiter: Cloudflare's edge sets
    // CF-Connecting-IP (overwriting any a client sends), and with Pseudo IPv4
    // on, the real IPv6 address in CF-Connecting-IPv6 (which the Go server
    // believes only beside a Class E CF-Connecting-IP: a client can send one
    // too); the container only sees the Worker's connection.
    for (const name of ["CF-Connecting-IP", "CF-Connecting-IPv6"]) {
      const ip = request.headers.get(name);
      if (ip) headers.set(name, ip);
      else headers.delete(name);
    }
    return getContainer(env.API, "api").fetch(new Request(request, { headers }));
  },
} satisfies ExportedHandler<Env>;
