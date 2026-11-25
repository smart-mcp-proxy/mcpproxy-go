#!/usr/bin/env bash
#
# Spec 132 (issue #1971) Connect-screen sweep: runs
# e2e/web-ui-sweep/connect-browser-login.spec.ts twice, against a scratch core
# with browser login ON and then OFF. Connect writes client config files under
# the core's HOME, so each core starts with `env -i`, a scratch HOME and a
# scratch data dir, never ~/.mcpproxy or the real HOME. See
# docs/development/web-ui-verification.md.
#
#   MCPPROXY_BINARY_PATH   mcpproxy binary (default: ./mcpproxy)
#   CONNECT_SWEEP_PORT     base port; the OFF run uses port+1 (default 18390)
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
BINARY_PATH="${MCPPROXY_BINARY_PATH:-$REPO_ROOT/mcpproxy}"
SWEEP_DIR="$REPO_ROOT/e2e/web-ui-sweep"
PORT="${CONNECT_SWEEP_PORT:-18390}"
ROOT=$(mktemp -d)
PIDS=()
cleanup() {
  for p in "${PIDS[@]:-}"; do [[ -n "$p" ]] && kill "$p" 2>/dev/null || true; done
  rm -rf "$ROOT"
}
trap cleanup EXIT

[[ -x "$BINARY_PATH" ]] || { echo "mcpproxy binary not found: $BINARY_PATH" >&2; exit 1; }
PLAYWRIGHT_BIN="$SWEEP_DIR/node_modules/.bin/playwright"
if [[ ! -x "$PLAYWRIGHT_BIN" ]]; then
  (cd "$SWEEP_DIR" && npm ci --no-audit --no-fund)
fi

run_one() { # name oauth(true|false) port off(0|1)
  local name=$1 oauth=$2 port=$3 off=$4
  local home="$ROOT/$name/home" data="$ROOT/$name/data" key
  key=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
  mkdir -p "$home" "$data" "$ROOT/$name/tmp"
  printf '{}' >"$home/.claude.json"
  mkdir -p "$home/.cursor" "$home/Library/Application Support/Code/User" "$home/.config/Code/User"
  printf '{}' >"$home/.cursor/mcp.json"
  printf '{}' >"$home/Library/Application Support/Code/User/mcp.json"
  printf '{}' >"$home/.config/Code/User/mcp.json"
  cat >"$ROOT/$name/config.json" <<CFG
{"listen":"127.0.0.1:$port","data_dir":"$data","api_key":"$key","enable_socket":false,"mcp_oauth_enabled":$oauth,"mcpServers":[]}
CFG
  env -i PATH="$PATH" HOME="$home" TMPDIR="$ROOT/$name/tmp" HEADLESS=1 DO_NOT_TRACK=1 \
    "$BINARY_PATH" serve --config "$ROOT/$name/config.json" --data-dir "$data" --listen "127.0.0.1:$port" \
    >"$ROOT/$name/core.log" 2>&1 &
  local pid=$!
  PIDS+=("$pid")
  local i=0
  until curl -s --connect-timeout 2 --max-time 5 -o /dev/null -w '%{http_code}' \
      -H "X-API-Key: $key" "http://127.0.0.1:$port/api/v1/servers" | grep -q '^200$'; do
    kill -0 "$pid" 2>/dev/null || { cat "$ROOT/$name/core.log" >&2; echo "core ($name) exited early" >&2; return 1; }
    i=$((i + 1)); [[ $i -gt 45 ]] && { cat "$ROOT/$name/core.log" >&2; echo "core ($name) not ready" >&2; return 1; }
    sleep 1
  done
  (
    cd "$SWEEP_DIR"
    MCPPROXY_BASE_URL="http://127.0.0.1:$port" MCPPROXY_API_KEY="$key" \
    MCPPROXY_SWEEP_SCRATCH_HOME="$home" SWEEP_BROWSER_LOGIN_OFF="$off" \
      "$PLAYWRIGHT_BIN" test connect-browser-login.spec.ts
  )
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
}

run_one on true "$PORT" 0
run_one off false "$((PORT + 1))" 1
echo "connect sweep passed (browser login on and off)"
