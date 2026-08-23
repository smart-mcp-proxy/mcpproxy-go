#!/usr/bin/env bash
#
# Boots a throwaway mcpproxy instance and runs the Playwright Web UI sweep
# (e2e/web-ui-sweep) against the Web UI IT serves — embedded frontend, never a
# dev server. See docs/development/web-ui-verification.md.
#
# Same script both ways it is used:
#   * by hand:            ./scripts/run-web-smoke.sh --show-report
#   * by the release gate: .github/workflows/release-qa-gate.yml `web-ui-sweep`
#     job (advisory), with MCPPROXY_BINARY_PATH / MCPPROXY_FIXTURE_PATH pointing
#     at the candidate binaries it downloaded.

set -euo pipefail

if [[ "${DEBUG:-}" == "1" ]]; then
  set -x
fi

usage() {
  cat <<EOF
Usage: ${0##*/} [--show-report]

Runs the Playwright Web UI sweep against a throwaway local mcpproxy instance.

Options:
  --show-report    Launch the Playwright HTML report server after the run
  -h, --help       Print this message

Environment:
  MCPPROXY_BINARY_PATH   mcpproxy binary to serve the UI (default: ./mcpproxy, built if absent)
  MCPPROXY_FIXTURE_PATH  optional cmd/mcpfixture binary; registered as a stdio
                         upstream so the sweep sees real servers and tools
  MCPPROXY_BASE_URL      base URL to serve on (default: http://127.0.0.1:18080)
  ARTIFACT_DIR           where the HTML report + server log land
                         (default: <repo>/tmp/web-smoke-artifacts)
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

required curl
required node
required npm

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)

BINARY_PATH="${MCPPROXY_BINARY_PATH:-$REPO_ROOT/mcpproxy}"
FIXTURE_PATH="${MCPPROXY_FIXTURE_PATH:-}"
BASE_URL="${MCPPROXY_BASE_URL:-http://127.0.0.1:18080}"
LISTEN="${BASE_URL#http://}"
SWEEP_DIR="$REPO_ROOT/e2e/web-ui-sweep"
ARTIFACT_DIR="${ARTIFACT_DIR:-$REPO_ROOT/tmp/web-smoke-artifacts}"
REPORT_DIR="$ARTIFACT_DIR/playwright-report"
# Always a fresh throwaway key, NEVER $MCPPROXY_API_KEY: the sweep puts the key
# in every navigation URL, and Playwright stores those URLs in the HTML report
# and failure traces. Honouring an ambient MCPPROXY_API_KEY would copy a
# developer's real key into tmp/web-smoke-artifacts (and, in CI, an uploaded
# artifact). The instance is throwaway, so its key may as well be too.
API_KEY="web-sweep-$(date +%s)-$$"
# The sweep's server-dependent checks run only when a fixture upstream exists.
SWEEP_SERVER_NAME=""

mkdir -p "$ARTIFACT_DIR" "$REPORT_DIR"

if [[ ! -x "$BINARY_PATH" ]]; then
  required go
  echo "building mcpproxy binary..."
  (cd "$REPO_ROOT" && go build -o "$BINARY_PATH" ./cmd/mcpproxy)
fi

TMPDIR_SWEEP=$(mktemp -d)
CONFIG_PATH="$TMPDIR_SWEEP/config.json"
DATA_DIR="$TMPDIR_SWEEP/data"
LOG_PATH="$TMPDIR_SWEEP/mcpproxy.log"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" >/dev/null 2>&1; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  # Preserve the server log before the temp dir goes away. Doing this in the
  # trap (not only on the happy path) is what makes an EARLY failure —
  # readiness timeout, npm/Chromium install blowing up under `set -e` —
  # diagnosable from the uploaded artifact instead of only from the CI console.
  if [[ -f "$LOG_PATH" ]]; then
    cp "$LOG_PATH" "$ARTIFACT_DIR/server.log" 2>/dev/null || true
  fi
  rm -rf "$TMPDIR_SWEEP"
}
trap cleanup EXIT

# mcpServers: the stdio fixture when one was supplied, otherwise an empty fleet
# (the sweep then skips its server-dependent checks instead of failing).
SERVERS_JSON="[]"
if [[ -n "$FIXTURE_PATH" ]]; then
  if [[ ! -x "$FIXTURE_PATH" ]]; then
    echo "MCPPROXY_FIXTURE_PATH is not executable: $FIXTURE_PATH" >&2
    exit 1
  fi
  # The config below is a heredoc, not JSON-encoded output: a path containing a
  # quote, a backslash or a newline would silently produce a malformed (or
  # subtly different) config rather than an error. Reject it loudly instead of
  # taking on a jq/python dependency for a path that is normally boring.
  case "$FIXTURE_PATH" in
    *'"'* | *\\* | *$'\n'*)
      echo "MCPPROXY_FIXTURE_PATH contains a quote, backslash or newline and cannot be embedded in the sweep config: $FIXTURE_PATH" >&2
      exit 1
      ;;
  esac
  SWEEP_SERVER_NAME="sweep-stdio"
  SERVERS_JSON=$(cat <<JSON
[
    {
      "name": "${SWEEP_SERVER_NAME}",
      "command": "${FIXTURE_PATH}",
      "args": ["--transport", "stdio"],
      "protocol": "stdio",
      "enabled": true,
      "quarantined": false,
      "isolation": { "enabled": false }
    }
  ]
JSON
)
fi

cat <<JSON >"$CONFIG_PATH"
{
  "listen": "${LISTEN}",
  "data_dir": "${DATA_DIR}",
  "api_key": "${API_KEY}",
  "enable_tray": false,
  "enable_socket": false,
  "enable_web_ui": true,
  "check_server_repo": false,
  "logging": {
    "level": "info",
    "enable_file": false,
    "enable_console": true
  },
  "mcpServers": ${SERVERS_JSON},
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

# HEADLESS/DO_NOT_TRACK: a QA sweep must never open a browser for OAuth nor
# emit production telemetry (same rule the gate driver applies).
# MCPPROXY_API_KEY is pinned to the generated throwaway key, NOT inherited: the
# env var outranks the config file ("source": "environment variable"), so a
# developer with their own key exported would otherwise leave the server
# demanding that key while the sweep probes with this one — a 46s readiness
# timeout with nothing but 401s in the log.
HEADLESS=1 DO_NOT_TRACK=1 MCPPROXY_API_KEY="$API_KEY" "$BINARY_PATH" serve \
  --config "$CONFIG_PATH" --listen "$LISTEN" >"$LOG_PATH" 2>&1 &
SERVER_PID=$!

echo "mcpproxy started (PID ${SERVER_PID}); waiting for readiness at ${BASE_URL}..."

attempt=0
# --connect-timeout/--max-time are load-bearing, not decoration: a process that
# holds the port but never answers (a hung instance, a half-open socket) makes an
# untimed curl block forever, and the liveness check below would never get a turn
# — the job would sit until its 20-minute timeout instead of failing in seconds.
until curl -s --connect-timeout 2 --max-time 5 -o /dev/null -w '%{http_code}' \
    -H "X-API-Key: ${API_KEY}" "$BASE_URL/api/v1/servers" | grep -q '^200$'; do
  # Fail fast if the candidate already exited (port in use → exit code 2, bad
  # config → 4, DB locked → 3). Without this the loop would burn the full
  # timeout, and — worse — a leftover instance of a PREVIOUS sweep listening on
  # the same port could answer 200 and the sweep would silently exercise that
  # stale binary instead of the candidate.
  if ! kill -0 "$SERVER_PID" >/dev/null 2>&1; then
    wait "$SERVER_PID" >/dev/null 2>&1 || true
    echo "mcpproxy exited before becoming ready at ${BASE_URL} (is ${LISTEN} already in use?)" >&2
    cat "$LOG_PATH" >&2
    exit 1
  fi
  sleep 1
  attempt=$((attempt + 1))
  if [[ $attempt -gt 45 ]]; then
    echo "server did not become ready after ${attempt}s" >&2
    cat "$LOG_PATH" >&2
    exit 1
  fi
done

echo "server ready at $BASE_URL"

export PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH:-$REPO_ROOT/tmp/playwright-browsers}"
mkdir -p "$PLAYWRIGHT_BROWSERS_PATH"

# `npm ci` against the COMMITTED e2e/web-ui-sweep/package-lock.json, never a
# bare `npm install`: this runs during release qualification, and resolving the
# mutable `^1.49.0` range there would execute whatever Playwright and its
# transitive deps published since the last review. The lockfile pins exact
# versions + integrity hashes; bump it deliberately with `npm install`.
#
# The install is skipped only when what is on disk was installed FROM THE CURRENT
# lockfile. A mere "is playwright present?" check would let a stale tree — e.g.
# one an older revision of this script installed with an unlocked `npm install` —
# keep serving an unreviewed version forever; checking only @playwright/test's
# version would still miss a lockfile that moved a transitive dependency. So the
# hash of the whole lockfile is stamped into node_modules after a successful
# `npm ci`, and any drift re-runs it. This keeps the hand-run fast path (no
# registry round-trip when nothing changed) without weakening the pinning.
#
# The path goes through process.argv, never string interpolation into the JS —
# a repo checked out under a path containing a quote would otherwise produce
# invalid JavaScript and a silently empty result.
lock_hash() {
  node -e 'const c=require("crypto"),f=require("fs");process.stdout.write(c.createHash("sha256").update(f.readFileSync(process.argv[1])).digest("hex"))' "$1" 2>/dev/null || true
}

LOCK_STAMP="$SWEEP_DIR/node_modules/.sweep-lock-sha256"
WANT_LOCK_HASH=$(lock_hash "$SWEEP_DIR/package-lock.json")
if [[ -z "$WANT_LOCK_HASH" ]]; then
  echo "cannot hash $SWEEP_DIR/package-lock.json — is the lockfile committed?" >&2
  exit 1
fi

HAVE_LOCK_HASH=""
if [[ -f "$LOCK_STAMP" ]]; then
  HAVE_LOCK_HASH=$(cat "$LOCK_STAMP")
fi

if [[ ! -x "$SWEEP_DIR/node_modules/.bin/playwright" || "$HAVE_LOCK_HASH" != "$WANT_LOCK_HASH" ]]; then
  echo "installing the locked @playwright/test into $SWEEP_DIR (npm ci)"
  # cd rather than `npm ci --prefix`: --prefix has a long history of version-
  # dependent behaviour for `ci` specifically, and this runs during release
  # qualification — an install that silently resolves against the repo root
  # instead of the sweep's lockfile is not a failure mode worth risking.
  (cd "$SWEEP_DIR" && npm ci)
  # After npm ci, which wipes node_modules (and with it any previous stamp).
  printf '%s' "$WANT_LOCK_HASH" >"$LOCK_STAMP"
else
  echo "@playwright/test already installed from the current lockfile"
fi

PLAYWRIGHT_BIN="$SWEEP_DIR/node_modules/.bin/playwright"
if [[ ! -x "$PLAYWRIGHT_BIN" ]]; then
  echo "playwright CLI not found after install" >&2
  exit 1
fi

echo "installing Playwright Chromium (cached under $PLAYWRIGHT_BROWSERS_PATH)"
if [[ "$(uname -s)" == "Linux" ]]; then
  "$PLAYWRIGHT_BIN" install --with-deps chromium
else
  "$PLAYWRIGHT_BIN" install chromium
fi

set +e
(
  cd "$SWEEP_DIR" || exit 1
  MCPPROXY_BASE_URL="$BASE_URL" \
  MCPPROXY_API_KEY="$API_KEY" \
  SWEEP_SERVER_NAME="$SWEEP_SERVER_NAME" \
  SWEEP_REPORT_DIR="$REPORT_DIR" \
    "$PLAYWRIGHT_BIN" test web-ui-sweep.spec.ts
)
PLAYWRIGHT_STATUS=$?
set -e

cp "$LOG_PATH" "$ARTIFACT_DIR/server.log"

if [[ $PLAYWRIGHT_STATUS -ne 0 ]]; then
  echo "web UI sweep FAILED; artifacts (HTML report + server log) in $ARTIFACT_DIR" >&2
  exit $PLAYWRIGHT_STATUS
fi

echo "web UI sweep passed; artifacts stored in $ARTIFACT_DIR"

if [[ $SHOW_REPORT -eq 1 ]]; then
  echo "launching Playwright HTML report (Ctrl+C to exit)..."
  "$PLAYWRIGHT_BIN" show-report "$REPORT_DIR"
fi

exit 0
