#!/bin/sh
# Bring the example up, check it does what the walkthrough claims, tear it down.
# Needs docker compose v2 and curl. Used by .github/workflows/compose-example.yml.
set -eu
cd "$(dirname "$0")"

PORT="${MCPPROXY_PORT:-18080}"
export MCPPROXY_PORT="$PORT"
fail() { echo "FAIL: $*" >&2; docker compose logs --tail=80 >&2 || true; exit 1; }
cleanup() { docker compose down -v --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT

./gen-secrets.sh
KEY="$(cat secrets/mcpproxy_api_key)"

docker compose up --build --wait --wait-timeout 300 || fail "stack did not become healthy"

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
#    only tool in the sidecar image, so the probes are node one-liners.
code="$(docker compose exec -T everything node -e "fetch('http://127.0.0.1:8000/mcp',{method:'POST'}).then(r=>console.log(r.status))")"
[ "$code" = "401" ] || fail "sidecar /mcp without its key returned $code, want 401"
echo "ok: sidecar refuses a request without its key"

# 4. The sidecar cannot reach the internet (neither by IP nor by name) ...
if docker compose exec -T everything node -e "fetch('http://1.1.1.1',{signal:AbortSignal.timeout(5000)}).then(()=>process.exit(0),()=>process.exit(1))"; then
  fail "sidecar reached 1.1.1.1"
fi
if docker compose exec -T everything node -e "fetch('https://example.com',{signal:AbortSignal.timeout(5000)}).then(()=>process.exit(0),()=>process.exit(1))"; then
  fail "sidecar reached example.com"
fi
echo "ok: sidecar has no route out"

# 5. ... and cannot see the proxy's data directory.
if docker compose exec -T everything test -e /data; then
  fail "/data is visible in the sidecar"
fi
echo "ok: /data is not visible in the sidecar"

# 6. No container has the docker socket.
for svc in mcpproxy everything; do
  id="$(docker compose ps -q "$svc")"
  if docker inspect -f '{{range .Mounts}}{{.Source}} {{end}}' "$id" | grep -q docker.sock; then
    fail "$svc mounts the docker socket"
  fi
done
echo "ok: no docker socket"

echo "PASS"
