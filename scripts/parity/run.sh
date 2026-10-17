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
INSPECTOR_PORT=9230
export GOTOOLCHAIN=local

mkdir -p "$P"

TS_PID=""
GO_PID=""
cleanup() {
  # Kill by recorded PID / process group only, never by pattern. TS_PID is a
  # session leader (setsid), so -TS_PID reaches wrangler and its workerd child.
  if [ -n "$TS_PID" ]; then kill -- "-$TS_PID" 2>/dev/null || kill "$TS_PID" 2>/dev/null || true; fi
  if [ -n "$GO_PID" ]; then kill "$GO_PID" 2>/dev/null || true; fi
}
trap cleanup EXIT

for port in $TS_PORT $GO_PORT $INSPECTOR_PORT; do
  if [ -n "$(ss -ltn "sport = :$port" | tail -n +2)" ]; then
    echo "parity: port $port is already in use" >&2
    exit 2
  fi
done
rm -f "$P/ts.pgid" "$P/go.pid"

# 1. The TS reference checkout.
if [ ! -e "$REF" ]; then
  git -C "$ROOT" worktree add --detach "$REF" "${PARITY_BASE:-0068887}" >&2
fi
BASE="$(git -C "$ROOT" rev-parse "${PARITY_BASE:-0068887}^{commit}")"
if [ "$(git -C "$REF" rev-parse HEAD)" != "$BASE" ]; then
  git -C "$REF" checkout -q --detach "$BASE" \
    || { echo "parity: $REF is not at $BASE and could not be moved there" >&2; exit 2; }
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

# 3. Start both servers. Build and check first, so nothing can fail between
# launching TS and the trap knowing its PID.
(cd "$ROOT/server" && go build -o "$P/devserver" ./cmd/devserver)
shopt -s nullglob
sqlites=("$P"/state-go/v3/d1/miniflare-D1DatabaseObject/[0-9a-f]*.sqlite)
if [ "${#sqlites[@]}" -ne 1 ]; then
  echo "parity: expected one sqlite file under state-go, found ${#sqlites[@]}" >&2
  exit 2
fi

cd "$REF"
setsid npx wrangler dev --port $TS_PORT --inspector-port $INSPECTOR_PORT --persist-to "$P/state-ts" >"$P/ts.log" 2>&1 &
TS_PID=$!
cd "$ROOT"
"$P/devserver" -sqlite "${sqlites[0]}" -addr ":$GO_PORT" >"$P/go.log" 2>&1 &
GO_PID=$!

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
# Both are probed on GET /api/stats, which also runs its backfill (a company
# whose deal is won becomes a customer), so both start from the same data.
wait_for $TS_PORT "TS server" ts /api/stats
wait_for $GO_PORT "Go server" go /api/stats

# 4. Run.
set +e
node "$ROOT/scripts/parity/runner.mjs" "http://localhost:$TS_PORT" "http://localhost:$GO_PORT" "$@"
rc=$?
exit $rc
