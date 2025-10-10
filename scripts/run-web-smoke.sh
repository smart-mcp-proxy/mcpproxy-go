#!/usr/bin/env bash

set -euo pipefail

if [[ "${DEBUG:-}" == "1" ]]; then
  set -x
fi

usage() {
  cat <<EOF
Usage: ${0##*/} [--show-report]

Runs the Playwright smoke test against a local mcpproxy instance.

Options:
  --show-report    Launch the Playwright HTML report server after the run
  -h, --help       Print this message
EOF
}

SHOW_REPORT=0
if [[ $# -gt 0 ]]; then
  for arg in "$@"; do
    case "$arg" in
      --show-report)
        SHOW_REPORT=1
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        echo "unknown argument: $arg" >&2
        usage >&2
        exit 2
        ;;
    esac
  done
fi

required() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

required go
required curl
required node
required npx

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)

BINARY_PATH="${MCPPROXY_BINARY_PATH:-$REPO_ROOT/mcpproxy}"
BASE_URL="${MCPPROXY_BASE_URL:-http://127.0.0.1:18080}"
PLAYWRIGHT_WORKDIR="$REPO_ROOT/.playwright-mcp"
RESULTS_DIR="$PLAYWRIGHT_WORKDIR/test-results"
ARTIFACT_DIR="${ARTIFACT_DIR:-$REPO_ROOT/tmp/web-smoke-artifacts}"

mkdir -p "$ARTIFACT_DIR"
mkdir -p "$PLAYWRIGHT_WORKDIR"

pushd "$REPO_ROOT" >/dev/null
if [[ ! -x "$BINARY_PATH" ]]; then
  echo "building mcpproxy binary..."
  go build -o "$BINARY_PATH" ./cmd/mcpproxy
fi
popd >/dev/null

TMPDIR=$(mktemp -d)
CONFIG_PATH="$TMPDIR/config.json"
DATA_DIR="$TMPDIR/data"
LOG_PATH="$TMPDIR/mcpproxy.log"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]]; then
    if kill -0 "$SERVER_PID" >/dev/null 2>&1; then
      kill "$SERVER_PID" >/dev/null 2>&1 || true
      wait "$SERVER_PID" >/dev/null 2>&1 || true
    fi
  fi
  rm -rf "$TMPDIR"
}
trap cleanup EXIT

cat <<JSON >"$CONFIG_PATH"
{
  "listen": "127.0.0.1:18080",
  "data_dir": "${DATA_DIR}",
  "api_key": "",
  "enable_tray": false,
  "logging": {
    "level": "info",
    "enable_file": false,
    "enable_console": true
  },
  "mcpServers": [],
  "top_k": 10,
  "tools_limit": 20,
  "tool_response_limit": 20000,
  "call_tool_timeout": "30s",
  "environment": {
    "inherit_system_safe": true,
    "allowed_system_vars": ["PATH", "HOME", "TMPDIR", "TEMP", "TMP"],
    "custom_vars": {},
    "enhance_path": false
  }
}
JSON

"$BINARY_PATH" serve --config "$CONFIG_PATH" --listen 127.0.0.1:18080 >"$LOG_PATH" 2>&1 &
SERVER_PID=$!

echo "mcpproxy started (PID ${SERVER_PID}); waiting for readiness..."

attempt=0
until curl -sS -o /dev/null -w '%{http_code}' "$BASE_URL/api/v1/servers" | grep -q '^200$'; do
  sleep 1
  attempt=$((attempt + 1))
  if [[ $attempt -gt 45 ]]; then
    echo "server did not become ready after ${attempt}s"
    cat "$LOG_PATH" >&2
    exit 1
  fi
done

echo "server ready at $BASE_URL"

rm -rf "$RESULTS_DIR"

export MCPPROXY_BASE_URL="$BASE_URL"
export PLAYWRIGHT_HTML_PATH="$ARTIFACT_DIR/playwright-report"
export PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH:-$REPO_ROOT/tmp/playwright-browsers}"
export CI=${CI:-1}

mkdir -p "$PLAYWRIGHT_HTML_PATH"

if [[ ! -d "$PLAYWRIGHT_WORKDIR/node_modules/@playwright/test" ]]; then
  echo "installing @playwright/test into $PLAYWRIGHT_WORKDIR"
  npm install --prefix "$PLAYWRIGHT_WORKDIR" --no-save --package-lock=false @playwright/test
fi

export PATH="$PLAYWRIGHT_WORKDIR/node_modules/.bin:$PATH"

PLAYWRIGHT_BIN="$PLAYWRIGHT_WORKDIR/node_modules/.bin/playwright"
if [[ ! -x "$PLAYWRIGHT_BIN" ]]; then
  echo "playwright CLI not found after install" >&2
  exit 1
fi

mkdir -p "$PLAYWRIGHT_BROWSERS_PATH"

pushd "$PLAYWRIGHT_WORKDIR" >/dev/null

echo "installing Playwright browsers (cached under $PLAYWRIGHT_BROWSERS_PATH)"
if [[ "$(uname -s)" == "Linux" ]]; then
  "$PLAYWRIGHT_BIN" install --with-deps chromium
else
  "$PLAYWRIGHT_BIN" install chromium
fi

set +e
"$PLAYWRIGHT_BIN" test web-smoke.spec.ts --project=chromium
PLAYWRIGHT_STATUS=$?
set -e

popd >/dev/null

cp "$LOG_PATH" "$ARTIFACT_DIR/server.log"

if [[ -d "$RESULTS_DIR" ]]; then
  mkdir -p "$ARTIFACT_DIR/test-results"
  cp -R "$RESULTS_DIR/." "$ARTIFACT_DIR/test-results/" >/dev/null 2>&1 || true
fi

if [[ $PLAYWRIGHT_STATUS -ne 0 ]]; then
  echo "web smoke failed; artifacts stored in $ARTIFACT_DIR" >&2
  exit $PLAYWRIGHT_STATUS
fi

echo "web smoke passed; artifacts stored in $ARTIFACT_DIR"

if [[ $SHOW_REPORT -eq 1 ]]; then
  echo "launching Playwright HTML report (Ctrl+C to exit)..."
  "$PLAYWRIGHT_BIN" show-report "$PLAYWRIGHT_HTML_PATH"
fi

exit 0
