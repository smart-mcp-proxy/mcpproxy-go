#!/bin/sh
# Bring the example up under a separate project name, check it does what the
# walkthrough claims, tear that test project down (including its own volume).
# Needs docker compose v2 and curl. Used by .github/workflows/compose-example.yml.
set -eu
cd "$(dirname "$0")"

# Run under its own Compose project (own containers, network and data volume) and
# its own port, so `down -v` below removes only what this test created and never
# the data volume of a deployment started with plain `docker compose up`.
# The project name is fixed and passed with -p on every call: -p wins over both
# the file's top-level `name:` and an inherited COMPOSE_PROJECT_NAME, which
# could otherwise point this script (and its `down -v`) at a real deployment.
PROJECT="mcpproxy-sidecars-test"
unset COMPOSE_PROJECT_NAME
dc() { docker compose -p "$PROJECT" "$@"; }
PORT="${MCPPROXY_PORT:-18081}"
export MCPPROXY_PORT="$PORT"
fail() { echo "FAIL: $*" >&2; dc logs --tail=80 >&2 || true; exit 1; }
cleanup() { dc down -v --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT

./gen-secrets.sh
KEY="$(cat secrets/mcpproxy_api_key)"

# --wait returns once the sidecar is healthy and mcpproxy is running; the proxy
# has no health check of its own, so step 1 polls it.
dc up --build --wait --wait-timeout 300 || fail "stack did not become healthy"

# 1. The proxy lists the sidecar's tools. The tool list arrives after the first
#    discovery pass, so poll for it.
n=0
until curl -fsS -H "X-API-Key: $KEY" "http://127.0.0.1:$PORT/api/v1/servers/everything/tools" 2>/dev/null | grep -q '"name":"echo"'; do
  n=$((n + 1))
  [ "$n" -le 40 ] || fail "everything:echo never appeared through the proxy"
  sleep 3
done
echo "ok: tools listed through the proxy"

# 2. /mcp refuses a request without a credential (require_mcp_auth: true).
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$PORT/mcp" -d '{}')"
[ "$code" = "401" ] || fail "unauthenticated /mcp returned $code, want 401"
echo "ok: /mcp without a credential is refused"

# 3. The sidecar's own endpoint refuses a request without its key. node is the
#    only tool in the sidecar image, so the probes are node one-liners. Each
#    probe prints its answer and exits 0, so a probe that could not run (no
#    answer, or a non-zero exit) fails the test instead of passing as "blocked".
probe() { dc exec -T everything node -e "$1" || fail "probe could not run: $1"; }

code="$(probe "fetch('http://127.0.0.1:8000/mcp',{method:'POST'}).then(r=>console.log(r.status))")"
[ "$code" = "401" ] || fail "sidecar /mcp without its key returned $code, want 401"
echo "ok: sidecar refuses a request without its key"

# 4. The sidecar cannot reach the internet (neither by IP nor by name) ...
for url in http://1.1.1.1 https://example.com; do
  out="$(probe "fetch('$url',{signal:AbortSignal.timeout(5000)}).then(()=>console.log('reached'),()=>console.log('blocked'))")"
  [ "$out" = "blocked" ] || fail "sidecar egress probe for $url answered '$out', want 'blocked'"
done
echo "ok: sidecar has no route out"

# 5. ... and cannot see the proxy's data directory.
out="$(probe "console.log(require('fs').existsSync('/data')?'present':'absent')")"
[ "$out" = "absent" ] || fail "/data probe in the sidecar answered '$out', want 'absent'"
echo "ok: /data is not visible in the sidecar"

# 6. No container has the docker socket.
for svc in mcpproxy everything; do
  id="$(dc ps -q "$svc")" || fail "could not list the $svc container"
  [ -n "$id" ] || fail "no $svc container"
  mounts="$(docker inspect -f '{{range .Mounts}}{{.Source}} {{end}}' "$id")" || fail "could not inspect $svc"
  case "$mounts" in
    *docker.sock*) fail "$svc mounts the docker socket" ;;
  esac
done
echo "ok: no docker socket"

echo "PASS"
