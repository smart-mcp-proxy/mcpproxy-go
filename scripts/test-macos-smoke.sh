#!/usr/bin/env bash
#
# macOS tray smoke (Spec 081 T3, US4). One script, used both by hand and by the
# release gate's `macos-smoke` job (advisory, .github/workflows/release-qa-gate.yml).
#
# Stages (each must pass; the script exits non-zero on the first failure):
#   1. build the Swift tray app           (swift build -c release)
#   2. run the Swift unit tests           (swift test)            [--skip-unit-tests]
#   3. start a core with an ISOLATED data dir + socket (scratch HOME)
#   4. launch the tray binary against that core
#        MCPPROXY_TRAY_SKIP_CORE=1  -> the tray must attach to the external core
#        MCPPROXY_HOME / MCPPROXY_SOCKET_PATH -> the scratch instance, never ~/.mcpproxy
#      and assert the socket handshake from the tray's own log:
#        "connectToCore: core is ready" and "got version=<core's /api/v1/info version>"
#   5. SIGTERM the tray; assert it exits 0 within the deadline, and that it left
#      the externally-managed core running (it must not kill a core it did not start)
#
# What this does NOT do (hosted runners): no Accessibility/Screen-Recording
# permission is granted to the job, so the mcpproxy-ui-test status-bar / menu
# primitives (FR-019's menu-item assertions) cannot run. No signing secrets and
# no notarization: the binary is the unsigned SwiftPM product.
#
# Environment:
#   MCPPROXY_BINARY_PATH  core binary to test (default: build ./cmd/mcpproxy, -tags nogui)
#   SMOKE_ARTIFACT_DIR    where logs are kept (default: <repo>/tmp/macos-smoke-artifacts)
#   SMOKE_PORT            core TCP port (default 18391)
#   SMOKE_ATTACH_TIMEOUT  seconds to wait for the handshake (default 60)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SWIFT_DIR="${REPO_ROOT}/native/macos/MCPProxy"
ARTIFACT_DIR="${SMOKE_ARTIFACT_DIR:-${REPO_ROOT}/tmp/macos-smoke-artifacts}"
PORT="${SMOKE_PORT:-18391}"
ATTACH_TIMEOUT="${SMOKE_ATTACH_TIMEOUT:-60}"
SKIP_UNIT_TESTS=0

for arg in "$@"; do
  case "$arg" in
    --skip-unit-tests) SKIP_UNIT_TESTS=1 ;;
    -h|--help) sed -n '2,28p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "test-macos-smoke.sh needs macOS (the tray is a Swift/AppKit app)" >&2
  exit 2
fi

log() { echo "[macos-smoke] $*"; }
fail() { echo "[macos-smoke] FAIL: $*" >&2; exit 1; }

mkdir -p "$ARTIFACT_DIR"
# The socket path has a 103-byte sockaddr_un limit, so the scratch root is short.
SCRATCH="$(mktemp -d /tmp/mcpsmk.XXXXXX)"
CORE_PID=""
TRAY_PID=""

cleanup() {
  local rc=$?
  [[ -n "$TRAY_PID" ]] && kill -9 "$TRAY_PID" 2>/dev/null || true
  if [[ -n "$CORE_PID" ]]; then
    kill "$CORE_PID" 2>/dev/null || true
    for _ in 1 2 3 4 5 6 7 8 9 10; do kill -0 "$CORE_PID" 2>/dev/null || break; sleep 0.5; done
    kill -9 "$CORE_PID" 2>/dev/null || true
  fi
  [[ -f "$SCRATCH/core.log" ]] && cp "$SCRATCH/core.log" "$ARTIFACT_DIR/core.log" 2>/dev/null || true
  [[ -f "$SCRATCH/tray.log" ]] && cp "$SCRATCH/tray.log" "$ARTIFACT_DIR/tray.log" 2>/dev/null || true
  rm -rf "$SCRATCH"
  exit "$rc"
}
trap cleanup EXIT

# ---- 1. build the tray -------------------------------------------------------
log "stage 1: swift build -c release"
( cd "$SWIFT_DIR" && swift build -c release ) 2>&1 | tee "$ARTIFACT_DIR/swift-build.log" | tail -5
TRAY_BIN="$(cd "$SWIFT_DIR" && swift build -c release --show-bin-path)/MCPProxy"
[[ -x "$TRAY_BIN" ]] || fail "tray binary not produced at $TRAY_BIN"

# The bare SwiftPM binary cannot run: UNUserNotificationCenter raises an
# NSException without an app bundle, and Sparkle.framework has no rpath. So the
# smoke assembles the minimum .app (Info.plist + Sparkle.framework) and signs it
# ad-hoc (no identity, no secrets, no notarization).
APP="${SCRATCH}/MCPProxy.app"
mkdir -p "${APP}/Contents/MacOS" "${APP}/Contents/Frameworks"
cp "$TRAY_BIN" "${APP}/Contents/MacOS/MCPProxy"
cp "${SWIFT_DIR}/MCPProxy/Info.plist" "${APP}/Contents/Info.plist"
cp -R "$(dirname "$TRAY_BIN")/Sparkle.framework" "${APP}/Contents/Frameworks/"
install_name_tool -add_rpath "@executable_path/../Frameworks" "${APP}/Contents/MacOS/MCPProxy"
codesign --force --deep --sign - "$APP" 2> "$ARTIFACT_DIR/codesign.log" \
  || { cat "$ARTIFACT_DIR/codesign.log"; fail "ad-hoc codesign of the scratch bundle failed"; }
APP_BIN="${APP}/Contents/MacOS/MCPProxy"

# ---- 2. unit tests -----------------------------------------------------------
if [[ "$SKIP_UNIT_TESTS" == "1" ]]; then
  log "stage 2: swift test SKIPPED (--skip-unit-tests)"
else
  log "stage 2: swift test"
  ( cd "$SWIFT_DIR" && swift test ) > "$ARTIFACT_DIR/swift-test.log" 2>&1 \
    || { tail -40 "$ARTIFACT_DIR/swift-test.log"; fail "swift test failed (full log: swift-test.log)"; }
  tail -3 "$ARTIFACT_DIR/swift-test.log"
fi

# ---- 3. core with an isolated instance root ----------------------------------
CORE_BIN="${MCPPROXY_BINARY_PATH:-}"
if [[ -z "$CORE_BIN" ]]; then
  CORE_BIN="${SCRATCH}/mcpproxy"
  log "stage 3: building the core (go build -tags nogui)"
  ( cd "$REPO_ROOT" && go build -tags nogui -o "$CORE_BIN" ./cmd/mcpproxy )
fi
[[ -x "$CORE_BIN" ]] || fail "core binary not executable: $CORE_BIN"

ROOT="${SCRATCH}/root"
mkdir -p "$ROOT" "${SCRATCH}/home"
SOCK="${ROOT}/mcpproxy.sock"
cat > "${ROOT}/mcp_config.json" <<EOF
{"listen":"127.0.0.1:${PORT}","api_key":"macos-smoke-key","enable_socket":true,"enable_web_ui":false,"mcpServers":[]}
EOF

log "stage 3: starting core on 127.0.0.1:${PORT} (data dir ${ROOT})"
HOME="${SCRATCH}/home" MCPPROXY_TELEMETRY=false "$CORE_BIN" serve \
  --data-dir "$ROOT" --config "${ROOT}/mcp_config.json" --listen "127.0.0.1:${PORT}" \
  > "${SCRATCH}/core.log" 2>&1 &
CORE_PID=$!

CORE_VERSION=""
for _ in $(seq 1 60); do
  if INFO="$(curl -sf -H 'X-API-Key: macos-smoke-key' "http://127.0.0.1:${PORT}/api/v1/info" 2>/dev/null)"; then
    CORE_VERSION="$(printf '%s' "$INFO" | python3 -c 'import json,sys; d=json.load(sys.stdin); print((d.get("data") or d).get("version",""))')"
    break
  fi
  kill -0 "$CORE_PID" 2>/dev/null || { tail -20 "${SCRATCH}/core.log"; fail "core exited during startup"; }
  sleep 0.5
done
[[ -n "$CORE_VERSION" ]] || { tail -20 "${SCRATCH}/core.log"; fail "core never answered /api/v1/info"; }
[[ -S "$SOCK" ]] || fail "core did not create its socket at $SOCK"
log "core up: version=${CORE_VERSION}, socket=${SOCK}"

# ---- 4. launch the tray against it; assert the socket handshake ---------------
log "stage 4: launching the tray (external-core attach, scratch instance root)"
HOME="${SCRATCH}/home" \
MCPPROXY_TRAY_SKIP_CORE=1 \
MCPPROXY_HOME="$ROOT" \
MCPPROXY_SOCKET_PATH="$SOCK" \
  "$APP_BIN" > "${SCRATCH}/tray.log" 2>&1 &
TRAY_PID=$!

ready=0
for _ in $(seq 1 "$ATTACH_TIMEOUT"); do
  kill -0 "$TRAY_PID" 2>/dev/null || { tail -30 "${SCRATCH}/tray.log"; fail "tray exited before attaching to the core"; }
  if grep -q "connectToCore: core is ready" "${SCRATCH}/tray.log" \
     && grep -q "got version=${CORE_VERSION}" "${SCRATCH}/tray.log"; then
    ready=1
    break
  fi
  sleep 1
done
if [[ "$ready" != "1" ]]; then
  tail -40 "${SCRATCH}/tray.log"
  # Evidence for a hang (e.g. a GUI-session service the runner lacks): where is
  # the main thread, and is there a login/GUI session at all?
  {
    echo "--- launchctl managername: $(launchctl managername 2>&1)"
    echo "--- console owner: $(stat -f%Su /dev/console 2>&1)"
    echo "--- sample of the tray (main thread):"
    sample "$TRAY_PID" 2 -file "$ARTIFACT_DIR/tray-sample.txt" > /dev/null 2>&1 \
      && head -60 "$ARTIFACT_DIR/tray-sample.txt" || echo "(sample unavailable)"
  } 2>&1 | tee "$ARTIFACT_DIR/tray-hang-diagnostics.log"
  fail "tray never reported 'connectToCore: core is ready' + 'got version=${CORE_VERSION}' within ${ATTACH_TIMEOUT}s"
fi
log "handshake ok: tray attached over ${SOCK} and read core version ${CORE_VERSION}"

# ---- 5. clean exit -----------------------------------------------------------
log "stage 5: SIGTERM the tray; expect exit 0 and the external core left running"
kill -TERM "$TRAY_PID"
exited=0
for _ in $(seq 1 30); do
  kill -0 "$TRAY_PID" 2>/dev/null || { exited=1; break; }
  sleep 0.5
done
if [[ "$exited" != "1" ]]; then
  tail -20 "${SCRATCH}/tray.log"
  fail "tray did not exit within 15s of SIGTERM"
fi
TRAY_RC=0
wait "$TRAY_PID" || TRAY_RC=$?
TRAY_PID=""
[[ "$TRAY_RC" == "0" ]] || { tail -20 "${SCRATCH}/tray.log"; fail "tray exited with status ${TRAY_RC} after SIGTERM, want 0"; }
kill -0 "$CORE_PID" 2>/dev/null || fail "the tray killed an externally-managed core it did not start"
curl -sf -H 'X-API-Key: macos-smoke-key' "http://127.0.0.1:${PORT}/api/v1/info" > /dev/null \
  || fail "core stopped answering after the tray exited"

log "PASS: tray built, unit tests $([[ $SKIP_UNIT_TESTS == 1 ]] && echo skipped || echo passed), socket handshake ok, clean exit"
