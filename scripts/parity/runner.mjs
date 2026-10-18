// Runs every scenario against two backends and diffs the normalized responses.
// Usage: PARITY_GO_TOKEN=crm_… node runner.mjs <ts-base-url> <go-base-url> [scenario-name ...]
//
// Every call is made as one signed-in account. The Go server needs an
// account for every /api call: its calls carry the API token run.sh gave the
// dev server (-bootstrap-token). The TS reference learned who was calling from
// Clawnify's X-Clawnify-* headers: its calls name the token's account there,
// so both sides write tasks and updates as the same person.
import { readdir } from "node:fs/promises";
import { fileURLToPath, pathToFileURL } from "node:url";
import path from "node:path";

const [tsBase, goBase, ...names] = process.argv.slice(2);
if (!tsBase || !goBase) {
  console.error("usage: PARITY_GO_TOKEN=crm_… runner.mjs <ts-base-url> <go-base-url> [scenario-name ...]");
  process.exit(2);
}
const goToken = process.env.PARITY_GO_TOKEN;
if (!goToken) {
  console.error("parity: PARITY_GO_TOKEN is not set (run.sh passes the Go dev server's -bootstrap-token)");
  process.exit(2);
}
const goAuth = { Authorization: `Bearer ${goToken}` };
const meRes = await fetch(goBase + "/api/auth/me", { headers: goAuth });
const me = meRes.ok ? (await meRes.json()).user : null;
if (!me?.email) {
  console.error(`parity: GET /api/auth/me with the token answered ${meRes.status}`);
  process.exit(2);
}
const tsAuth = { "X-Clawnify-Caller": "user", "X-Clawnify-User-Id": me.id, "X-Clawnify-User-Email": me.email };

const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/g;
const TS = /\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?Z?/g;

// One normalizer per scenario per backend: UUIDs are numbered by first
// appearance, in call order, across paths and bodies alike.
function makeNormalizer() {
  const ids = new Map();
  const str = (s) =>
    s.replace(UUID, (m) => {
      if (!ids.has(m)) ids.set(m, ids.size + 1);
      return `<id:${ids.get(m)}>`;
    }).replace(TS, "<ts>");
  const walk = (v) => {
    if (typeof v === "string") return str(v);
    if (Array.isArray(v)) return v.map(walk);
    if (v && typeof v === "object") {
      const o = {};
      for (const k of Object.keys(v).sort()) o[str(k)] = walk(v[k]);
      return o;
    }
    return v;
  };
  return { str, walk };
}

// A call's opts.compare, when given, picks what of its parsed body is compared
// (status and content type always are): for a body the Go server is meant to
// answer differently, such as the discovery documents of a CRM with other
// routes. A not-found's endpoint list is compared only for being there, for
// the same reason. auth is the backend's credentials, sent with every call.
function makeCaller(base, record, auth) {
  return async (method, p, opts = {}) => {
    const headers = { ...auth, ...(opts.headers || {}) };
    let body;
    if (opts.body !== undefined) {
      body = typeof opts.body === "string" ? opts.body : JSON.stringify(opts.body);
      if (!Object.keys(headers).some((h) => h.toLowerCase() === "content-type")) {
        headers["Content-Type"] = "application/json";
      }
    }
    const res = await fetch(base + p, { method, headers, body });
    const text = await res.text();
    let parsed = text;
    let isJson = false;
    try {
      parsed = JSON.parse(text);
      isJson = true;
    } catch {}
    const contentType = (res.headers.get("content-type") || "").split(";")[0].trim().toLowerCase();
    let compared = opts.compare ? opts.compare(parsed) : parsed;
    if (!opts.compare && isJson && compared?.error === "not_found" && Array.isArray(compared.endpoints)) {
      compared = { ...compared, endpoints: "(a list: not compared, the Go CRM has other routes)" };
    }
    record.push({ method, path: p, status: res.status, contentType, isJson, body: compared, raw: text });
    return { status: res.status, body: parsed };
  };
}

// Partners, a deal's recurring value and a deal's owner, customer users,
// tickets and a company's main contact came after the port: TS never answered
// with them, so the Go side's answers are compared without them — on a deal,
// in the stats, on a company (anywhere), and among a deal's or a company's
// valid fields.
const POST_PORT_DEAL_KEYS = ["partners", "recurring_value", "owner_id", "owner"];
const POST_PORT_STATS_KEYS = ["partners", "dealRecurringValue", "customerUsers", "tickets", "openTickets"];
const POST_PORT_KEYS = ["main_contact_id", "main_contact"]; // a company's
const POST_PORT_DEAL_FIELDS = ["partner_ids", "recurring_value", "owner_id"];
const POST_PORT_COMPANY_FIELDS = ["main_contact_id"];
function withoutPartners(v) {
  if (Array.isArray(v)) return v.map(withoutPartners);
  if (!v || typeof v !== "object") return v;
  const dropped = [...POST_PORT_KEYS, ...("stage" in v ? POST_PORT_DEAL_KEYS : "dealValue" in v ? POST_PORT_STATS_KEYS : [])];
  const unknown = typeof v.error === "string" ? v.error : "";
  const postPortFields = unknown.startsWith("Unknown field(s) for deal:")
    ? POST_PORT_DEAL_FIELDS
    : unknown.startsWith("Unknown field(s) for company:")
      ? POST_PORT_COMPANY_FIELDS
      : null;
  const o = {};
  for (const [k, x] of Object.entries(v)) {
    if (dropped.includes(k)) continue;
    o[k] = k === "valid_fields" && postPortFields && Array.isArray(x) ? x.filter((f) => !postPortFields.includes(f)) : withoutPartners(x);
  }
  return o;
}

function normalize(records) {
  const n = makeNormalizer();
  return records.map((r) => ({
    method: r.method,
    path: n.str(r.path),
    status: r.status,
    contentType: r.contentType,
    body: r.isJson ? n.walk(r.body) : n.str(String(r.body)),
    raw: r.raw,
  }));
}

function diff(a, b, at, out) {
  if (out.length >= 10) return;
  if (JSON.stringify(a) === JSON.stringify(b)) return;
  const isObj = (v) => v && typeof v === "object";
  if (isObj(a) && isObj(b) && Array.isArray(a) === Array.isArray(b)) {
    const keys = Array.isArray(a)
      ? [...Array(Math.max(a.length, b.length)).keys()]
      : [...new Set([...Object.keys(a), ...Object.keys(b)])].sort();
    for (const k of keys) diff(a?.[k], b?.[k], `${at}${Array.isArray(a) ? `[${k}]` : `.${k}`}`, out);
    return;
  }
  out.push({ at: at || "(root)", ts: a, go: b });
}

const trunc = (s) => (s.length > 2048 ? s.slice(0, 2048) + `... (${s.length} bytes)` : s);

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), "scenarios");
const files = (await readdir(dir)).filter((f) => f.endsWith(".mjs")).sort();
const selected = names.length ? files.filter((f) => names.includes(f.replace(/\.mjs$/, ""))) : files;
if (names.length) {
  for (const n of names) {
    if (!selected.includes(`${n}.mjs`)) {
      console.error(`parity: no scenario file named ${n}.mjs`);
      process.exit(2);
    }
  }
}

let scenarios = 0;
let calls = 0;
let mismatches = 0;

for (const file of selected) {
  const mod = await import(pathToFileURL(path.join(dir, file)).href);
  for (const sc of mod.default) {
    scenarios++;
    const rec = { ts: [], go: [] };
    const errs = {};
    for (const [who, base, auth] of [["ts", tsBase, tsAuth], ["go", goBase, goAuth]]) {
      try {
        await sc.run(makeCaller(base, rec[who], auth));
      } catch (e) {
        errs[who] = e;
      }
    }
    for (const who of ["ts", "go"]) {
      if (errs[who]) {
        mismatches++;
        console.log(`MISMATCH ${sc.name}: scenario threw against ${who}: ${errs[who]?.stack || errs[who]}`);
      }
    }
    const a = normalize(rec.ts);
    const b = normalize(rec.go.map((r) => (r.isJson ? { ...r, body: withoutPartners(r.body) } : r)));
    const n = Math.max(a.length, b.length);
    for (let i = 0; i < n; i++) {
      calls++;
      const x = a[i];
      const y = b[i];
      if (!x || !y) {
        mismatches++;
        console.log(`MISMATCH ${sc.name} #${i}: only ${x ? "ts" : "go"} made this call`);
        continue;
      }
      const d = [];
      if (x.status !== y.status) d.push({ at: "status", ts: x.status, go: y.status });
      if (x.contentType !== y.contentType) d.push({ at: "content-type", ts: x.contentType, go: y.contentType });
      diff(x.body, y.body, "body", d);
      if (d.length) {
        mismatches++;
        console.log(`MISMATCH ${sc.name} #${i} ${x.method} ${x.path}`);
        console.log("  diff: " + JSON.stringify(d));
        console.log("  ts raw: " + trunc(x.raw));
        console.log("  go raw: " + trunc(y.raw));
      }
    }
  }
}

if (scenarios === 0) {
  console.error("parity: no scenarios ran");
  process.exit(2);
}
console.log(`parity: ${scenarios} scenarios, ${calls} calls, ${mismatches} mismatches`);
process.exit(mismatches ? 1 : 0);
