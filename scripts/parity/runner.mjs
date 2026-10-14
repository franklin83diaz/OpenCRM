// Runs every scenario against two backends and diffs the normalized responses.
// Usage: node runner.mjs <ts-base-url> <go-base-url> [scenario-name ...]
import { readdir } from "node:fs/promises";
import { fileURLToPath, pathToFileURL } from "node:url";
import path from "node:path";

const [tsBase, goBase, ...names] = process.argv.slice(2);
if (!tsBase || !goBase) {
  console.error("usage: runner.mjs <ts-base-url> <go-base-url> [scenario-name ...]");
  process.exit(2);
}

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

function makeCaller(base, record) {
  return async (method, p, opts = {}) => {
    const headers = { ...(opts.headers || {}) };
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
    record.push({ method, path: p, status: res.status, contentType, isJson, body: parsed, raw: text });
    return { status: res.status, body: parsed };
  };
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
    for (const [who, base] of [["ts", tsBase], ["go", goBase]]) {
      try {
        await sc.run(makeCaller(base, rec[who]));
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
    const b = normalize(rec.go);
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

console.log(`parity: ${scenarios} scenarios, ${calls} calls, ${mismatches} mismatches`);
process.exit(mismatches ? 1 : 0);
