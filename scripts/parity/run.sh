#!/usr/bin/env bash
# Runs the parity scenarios against the TS reference (commit PARITY_BASE) and the
# Go dev server, each on an identically seeded fresh database. All state lives in
# .parity/ (gitignored). Usage: scripts/parity/run.sh [scenario-name ...]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
P="$ROOT/.parity"
REF="$P/ts-ref"
TS_PORT=8788
GO_PORT=8790
export GOTOOLCHAIN=local

mkdir -p "$P"

cleanup() {
  # Kill by recorded PID / process group only, never by pattern.
  if [ -f "$P/ts.pgid" ]; then kill -- "-$(cat "$P/ts.pgid")" 2>/dev/null || true; rm -f "$P/ts.pgid"; fi
  if [ -f "$P/go.pid" ]; then kill "$(cat "$P/go.pid")" 2>/dev/null || true; rm -f "$P/go.pid"; fi
}
trap cleanup EXIT

if [ -n "$(ss -ltn "sport = :$TS_PORT or sport = :$GO_PORT" | tail -n +2)" ]; then
  echo "parity: port $TS_PORT or $GO_PORT is already in use" >&2
  exit 2
fi

# 1. The TS reference checkout.
if [ ! -e "$REF" ]; then
  git -C "$ROOT" worktree add --detach "$REF" "${PARITY_BASE:-0068887}" >&2
fi
[ -e "$REF/node_modules" ] || ln -s "$ROOT/node_modules" "$REF/node_modules"

# 2. Fresh, identically seeded databases.
for x in ts go; do
  rm -rf "$P/state-$x"
  for f in "$REF/src/server/schema.sql" "$REF/demo/seed.sql"; do
    (cd "$REF" && npx wrangler d1 execute open-crm-db --local --persist-to "$P/state-$x" --file="$f") >"$P/seed-$x.log" 2>&1 \
      || { cat "$P/seed-$x.log" >&2; echo "parity: seeding $x failed" >&2; exit 2; }
  done
done

# 3. Start both servers.
# The inner shell records its own PID, which setsid makes the process group
# leader, so cleanup can kill wrangler and its workerd child together.
(cd "$REF" && exec setsid bash -c 'echo $$ >"$0"; exec npx wrangler dev --port '$TS_PORT' --inspector-port 9230 --persist-to "$1"' "$P/ts.pgid" "$P/state-ts" >"$P/ts.log" 2>&1) &

(cd "$ROOT/server" && go build -o "$P/devserver" ./cmd/devserver)
shopt -s nullglob
sqlites=("$P"/state-go/v3/d1/miniflare-D1DatabaseObject/[0-9a-f]*.sqlite)
if [ "${#sqlites[@]}" -ne 1 ]; then
  echo "parity: expected one sqlite file under state-go, found ${#sqlites[@]}" >&2
  exit 2
fi
"$P/devserver" -sqlite "${sqlites[0]}" -addr ":$GO_PORT" >"$P/go.log" 2>&1 &
echo $! >"$P/go.pid"

# wait_for <port> <label> <logname> <path>
wait_for() {
  for _ in $(seq 1 120); do
    if curl -fsS -o /dev/null "http://localhost:$1$4" 2>/dev/null; then return 0; fi
    sleep 0.5
  done
  echo "parity: $2 did not answer GET $4; see $P/$3.log" >&2
  tail -n 20 "$P/$3.log" >&2 || true
  return 1
}
# Only discovery is ported to Go so far, so the Go server is probed on /llms.txt.
wait_for $TS_PORT "TS server" ts /api/stats
wait_for $GO_PORT "Go server" go /llms.txt

# 4. Run.
set +e
node "$ROOT/scripts/parity/runner.mjs" "http://localhost:$TS_PORT" "http://localhost:$GO_PORT" "$@"
rc=$?
exit $rc
