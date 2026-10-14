import { Container, ContainerProxy, getContainer } from "@cloudflare/containers";
import type { DurableObject } from "cloudflare:workers";

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
  // The Clawnify credentials binding, present only on the platform.
  CREDENTIALS?: CredentialsBinding;
  // Optional configuration, passed on to the Go server as environment variables.
  // In production Clawnify injects CLAWNIFY_ORG_ID and the org token; the URLs
  // are only set off-platform, to point the clients at a local stand-in.
  CLAWNIFY_TOKEN?: string;
  CLAWNIFY_ORG_ID?: string;
  CLAWNIFY_API_URL?: string;
  CLAWNIFY_QUEUE_URL?: string;
  CLAWNIFY_SERVICES_URL?: string;
  SLACK_CHANNEL?: string;
}

// The slice of @clawnify/connections' CredentialBinding the bridge calls.
interface CredentialsBinding {
  getToken(service: string, orgId: string): Promise<string | null>;
  executeTool(service: string, action: string, args: Record<string, unknown>, orgId: string): Promise<unknown>;
  listConnected(orgId: string): Promise<string[]>;
}

const PASSED_ON = [
  "CLAWNIFY_TOKEN",
  "CLAWNIFY_ORG_ID",
  "CLAWNIFY_API_URL",
  "CLAWNIFY_QUEUE_URL",
  "CLAWNIFY_SERVICES_URL",
  "SLACK_CHANNEL",
] as const;

export class Api extends Container<Env> {
  defaultPort = 8080;
  sleepAfter = "10m";

  constructor(ctx: DurableObject["ctx"], env: Env) {
    super(ctx, env);
    const vars: Record<string, string> = { D1_URL: "http://d1.internal" };
    for (const key of PASSED_ON) {
      const value = env[key];
      if (value) vars[key] = value;
    }
    // A container can't see the CREDENTIALS binding; this host is the bridge to it.
    if (env.CREDENTIALS) vars.CLAWNIFY_CONNECTIONS_URL = "http://credentials.internal";
    this.envVars = vars;
  }
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

// The credentials bridge: the three endpoints of Clawnify's connections API
// (server/internal/clawnify/connections.go), answered with env.CREDENTIALS for
// the org Clawnify injected. As in the TS client, a failing call reads as
// "no token" or "nothing connected", and a failing execute as an error message.
//   POST /v1/connections/token {service}                → {token}
//   POST /v1/connections/execute {service, action, args} → {data, error, successful}
//   GET  /v1/connections/connected                       → {services}
Api.outboundByHost["credentials.internal"] = async (req: Request, env: Env) => {
  const path = new URL(req.url).pathname;
  const creds = env.CREDENTIALS;
  const orgId = env.CLAWNIFY_ORG_ID ?? "";
  if (!creds) return Response.json({ error: "no credentials binding" }, { status: 404 });
  try {
    if (req.method === "POST" && path === "/v1/connections/token") {
      const { service } = await req.json<{ service: string }>();
      return Response.json({ token: (await creds.getToken(service, orgId)) ?? null });
    }
    if (req.method === "POST" && path === "/v1/connections/execute") {
      const { service, action, args } = await req.json<{ service: string; action: string; args?: Record<string, unknown> }>();
      return Response.json(await creds.executeTool(service, action, args ?? {}, orgId));
    }
    if (req.method === "GET" && path === "/v1/connections/connected") {
      return Response.json({ services: await creds.listConnected(orgId) });
    }
  } catch (e) {
    if (path === "/v1/connections/execute") {
      return Response.json({ error: e instanceof Error ? e.message : String(e) }, { status: 500 });
    }
    return Response.json(path === "/v1/connections/token" ? { token: null } : { services: [] });
  }
  return Response.json({ error: `no ${req.method} ${path} on the credentials bridge` }, { status: 404 });
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
