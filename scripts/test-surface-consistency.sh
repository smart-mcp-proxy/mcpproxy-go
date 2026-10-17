#!/usr/bin/env bash
#
# Spec 081 T4 — surface-state consistency check, runnable by hand next to
# scripts/test-api-e2e.sh. Same driver and same steps as the release gate's
# `surface-consistency` job (.github/workflows/release-qa-gate.yml):
#
#   1. `release-gate matrix` boots the candidate core against the server-type
#      fixtures (stdio/http/sse/oauth, plus docker when Docker is available)
#      and leaves it running;
#   2. `release-gate consistency` adds the mixed-state servers (quarantined,
#      disabled, unhealthy, live) and asserts the CLI (`upstream list -o json`),
#      the MCP `upstream_servers list` and the rendered Web UI agree with the
#      core's /api/v1/servers on every server — settled, after a live change,
#      and after a reload. Then it tears everything down.
#
# Usage:
#   ./scripts/test-surface-consistency.sh                 # full check
#   ./scripts/test-surface-consistency.sh --seed-mismatch cli:gate-cons-live:health.level=degraded
#                                                         # prove it fails
#   ./scripts/test-surface-consistency.sh --no-webui      # no Node/Chromium
#
# Extra arguments go to `release-gate consistency`. Environment:
#   MCPPROXY_BINARY_PATH  candidate core (default: ./mcpproxy; must embed the
#                         Web UI — build with `make build`)
#   GATE_CELLS            matrix cells (default: stdio,http,sse,oauth[,docker])
#   REPORT_DIR            fragment output (default: tmp/surface-consistency)

set -uo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$REPO_ROOT"

BIN_DIR="$REPO_ROOT/tmp/surface-consistency-bin"
REPORT_DIR="${REPORT_DIR:-$REPO_ROOT/tmp/surface-consistency}"
WORK_DIR=$(mktemp -d)
CORE="${MCPPROXY_BINARY_PATH:-$REPO_ROOT/mcpproxy}"
SWEEP_DIR="$REPO_ROOT/e2e/web-ui-sweep"
mkdir -p "$BIN_DIR" "$REPORT_DIR"
KEEP_CORE=0
for arg in "$@"; do
  [[ "$arg" == "--keep-core" ]] && KEEP_CORE=1
done

cleanup() {
  # The matrix leaves its core + fixtures running for the consistency step,
  # which tears them down itself — unless it never got that far (bad flags,
  # an early error). Teardown is idempotent, so run it before the state file
  # it needs is deleted. --keep-core means "leave it all up": keep the state
  # file too, so `release-gate teardown` can stop it later.
  if [[ $KEEP_CORE -eq 1 && -f "$WORK_DIR/gate-state.json" ]]; then
    echo "--keep-core: instance left running; stop it with:"
    echo "  $BIN_DIR/release-gate teardown --state-file $WORK_DIR/gate-state.json"
    return
  fi
  if [[ -f "$WORK_DIR/gate-state.json" && -x "$BIN_DIR/release-gate" ]]; then
    "$BIN_DIR/release-gate" teardown --state-file "$WORK_DIR/gate-state.json" >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

NO_WEBUI=0
for arg in "$@"; do
  [[ "$arg" == "--no-webui" ]] && NO_WEBUI=1
done

if [[ ! -x "$CORE" ]]; then
  echo "candidate core not found at $CORE — run 'make build' (the Web UI must be embedded)" >&2
  exit 1
fi

echo "building gate driver + fixtures into $BIN_DIR"
go build -o "$BIN_DIR/release-gate" ./cmd/release-gate || exit 1
go build -o "$BIN_DIR/mcpfixture" ./cmd/mcpfixture || exit 1
go build -o "$BIN_DIR/oauthserver" ./tests/oauthserver/cmd/server || exit 1

CELLS="${GATE_CELLS:-}"
if [[ -z "$CELLS" ]]; then
  CELLS="stdio,http,sse,oauth"
  if docker info >/dev/null 2>&1; then
    CELLS="$CELLS,docker"
  else
    echo "Docker unavailable — skipping the docker cell locally (the gate job always runs it)"
  fi
fi
if [[ ",$CELLS," == *",docker,"* ]]; then
  scripts/gate/build-fixture-image.sh || exit 1
fi

WEBUI_ARGS=()
if [[ $NO_WEBUI -eq 0 ]]; then
  if [[ ! -x "$SWEEP_DIR/node_modules/.bin/playwright" ]]; then
    (cd "$SWEEP_DIR" && npm ci) || exit 1
  fi
  export PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH:-$REPO_ROOT/tmp/playwright-browsers}"
  "$SWEEP_DIR/node_modules/.bin/playwright" install chromium || exit 1
  WEBUI_ARGS=(--webui-script "$SWEEP_DIR/surface-snapshot.mjs")
fi

# The matrix writes its own fragments into a scratch dir: this script (and
# the gate job) only reports the consistency entry.
"$BIN_DIR/release-gate" matrix \
  --binary "$CORE" \
  --fixture "$BIN_DIR/mcpfixture" \
  --oauth-server "$BIN_DIR/oauthserver" \
  --cells "$CELLS" \
  --report-dir "$WORK_DIR/matrix-report" \
  --state-file "$WORK_DIR/gate-state.json" \
  --work-dir "$WORK_DIR"
echo "matrix exit=$? (its cells are reported by the matrix-invariants job, not here)"

if [[ ! -f "$WORK_DIR/gate-state.json" ]]; then
  echo "matrix left no state file — nothing to compare" >&2
  exit 1
fi

"$BIN_DIR/release-gate" consistency \
  --state-file "$WORK_DIR/gate-state.json" \
  --report-dir "$REPORT_DIR" \
  ${WEBUI_ARGS[@]+"${WEBUI_ARGS[@]}"} \
  "$@"
RC=$?
echo "report fragment: $REPORT_DIR/invariant-surface-consistency.json"
exit $RC
