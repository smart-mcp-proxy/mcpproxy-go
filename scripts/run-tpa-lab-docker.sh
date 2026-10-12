#!/usr/bin/env bash
# Safe end-to-end runner for the TPA attack lab against a local mcpproxy:
#
#   scripts/run-tpa-lab-docker.sh            # build image + proxy, scan, report
#   KEEP=1 scripts/run-tpa-lab-docker.sh     # leave the daemon running for the
#                                             # rug-pull / live-flip walkthrough
#
# Safety model (see tests/tpa-attack-lab/Dockerfile and README):
#   - both lab upstreams run in Docker via mcpproxy's per-server isolation:
#     distroless image (no shell/coreutils), network_mode=none (stdio only),
#     read-only rootfs, all caps dropped, no-new-privileges
#   - the proxy's state stays inside the worktree (data_dir=./test-data-tpa-lab-docker)
#   - the lab fixture itself is a pure simulation (attacker.invalid endpoints,
#     fake canaries, no file/network/exec by construction)
#
# Rug-pull walkthrough after scanning (with KEEP=1 daemon still up):
#   mcpproxy upstream approve tpa-lab-attacker --config tests/tpa-lab-config-docker.json
#   # live flip (no restart):
#   mcpproxy --config tests/tpa-lab-config-docker.json call … lab_arm_stage2
#   # or restart path: set "TPA_LAB_STAGE": "2" in the config, rerun this script
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
# TPA_LAB_CONFIG overrides the config for the preflight tests
# (tests/tpa-attack-lab/scripts_test.go); it is validated like the default.
CONFIG="${TPA_LAB_CONFIG:-$PROJECT_DIR/tests/tpa-lab-config-docker.json}"
# Resolve a relative override against the caller's cwd NOW, before any check:
# the script later cds to the project root, and the file it validated must be
# the file the daemon runs (Sol r8 P2).
[[ "$CONFIG" == /* ]] || CONFIG="$PWD/$CONFIG"
# TPA_LAB_BINARY replaces the mcpproxy binary (tests only, with SKIP_BUILD=1).
BINARY="${TPA_LAB_BINARY:-$PROJECT_DIR/mcpproxy}"
LISTEN="127.0.0.1:8081"
IMAGE="${IMAGE:-tpa-attack-lab:lab}"
DATA_DIR="$PROJECT_DIR/test-data-tpa-lab-docker"
API_KEY="tpa-lab-key"
BASE_URL="http://127.0.0.1:8081"
KEEP="${KEEP:-0}"
LOG_DIR="$(mktemp -d)"

log() { echo "[tpa-lab] $*"; }

# shellcheck source=lib/tpa-lab-verdicts.sh
source "$SCRIPT_DIR/lib/tpa-lab-verdicts.sh"

# Inherited MCPPROXY_DATA / MCPPROXY_LISTEN / MCPPROXY_API_KEY (and every
# other MCPPROXY_* override) would beat the checked config's data_dir and
# listen address; drop them before anything runs (Sol r8 P1). The daemon is
# also started with --data-dir and --listen pinned below.
lab_scrub_mcpproxy_env

# Every upstream must be Docker-isolated with network_mode none: the
# malicious lab server never runs bare or with network access (Sol r5 P2).
lab_config_check_isolation "$CONFIG" || {
  echo "ERROR: $CONFIG does not Docker-isolate every server with network_mode none; refusing to run the lab." >&2
  exit 1
}

# The checked-in config pins its isolation image; an IMAGE override would
# build one tag and run another (a stale fixture). Refuse it rather than
# verify the wrong image (Sol r3 P2) — use scripts/tpa-lab-redteam-experiments.sh,
# which rewrites its generated configs, for a custom tag.
while IFS= read -r cfg_image; do
  if [[ "$cfg_image" != "$IMAGE" ]]; then
    echo "ERROR: IMAGE=$IMAGE but $CONFIG runs isolation image '$cfg_image'; the override is not supported here." >&2
    exit 1
  fi
done < <(lab_config_images "$CONFIG")
# The checked-in config's data_dir is relative: the daemon and every CLI call
# below resolve it against their working directory. Run from the project
# root, whatever the caller's cwd, so state and the tray socket both land in
# $DATA_DIR inside the worktree (Sol r4 P2), and refuse a config whose
# data_dir resolves anywhere else.
cd "$PROJECT_DIR"
cfg_data_dir="$(lab_config_data_dir "$CONFIG" "$PROJECT_DIR")" || exit 1
if [[ "$cfg_data_dir" != "$DATA_DIR" ]]; then
  echo "ERROR: $CONFIG data_dir resolves to $cfg_data_dir, not $DATA_DIR." >&2
  exit 1
fi
# Test hook: stop after the preflight checks, before building or starting
# anything (tests/tpa-attack-lab/scripts_test.go).
[[ "${TPA_LAB_PREFLIGHT_ONLY:-0}" == "1" ]] && { log "preflight ok (cwd=$PWD data_dir=$cfg_data_dir config=$CONFIG)"; exit 0; }

# Refuse ANY listener on the lab port before creating state, building or
# starting anything (Sol r10 P2): the old authenticated-API probe let a
# listener answering 401/404 or another protocol through, and the daemon
# then opened the lab data dir before its bind failed. Nothing is killed.
if lab_port_busy 8081; then
  echo "ERROR: port 8081 is already in use; refusing to run (nothing was stopped)." >&2
  echo "Inspect the listener with 'lsof -nP -iTCP:8081 -sTCP:LISTEN' (a previous KEEP=1 run?) and stop it yourself." >&2
  exit 1
fi

mkdir -p "$DATA_DIR"
export MCPPROXY_TRAY_ENDPOINT="unix://$DATA_DIR/mcpproxy.sock"

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  log "building lab image $IMAGE (context: repo root)"
  docker build -f "$PROJECT_DIR/tests/tpa-attack-lab/Dockerfile" -t "$IMAGE" "$PROJECT_DIR"
  log "building mcpproxy binary"
  (cd "$PROJECT_DIR" && go build -o ./mcpproxy ./cmd/mcpproxy)
fi

log "starting mcpproxy (log: $LOG_DIR/mcpproxy.log)"
"$BINARY" serve --config "$CONFIG" --data-dir "$DATA_DIR" --listen "$LISTEN" --log-level=info >"$LOG_DIR/mcpproxy.log" 2>&1 &
DAEMON_PID=$!

cleanup() {
  if [[ "$KEEP" == "1" ]]; then
    log "KEEP=1 — daemon left running (pid $DAEMON_PID); stop with: kill $DAEMON_PID"
  else
    kill "$DAEMON_PID" 2>/dev/null || true
    wait "$DAEMON_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

# Wait for the REST API.
for i in $(seq 1 60); do
  if curl -sf -o /dev/null -H "X-API-Key: $API_KEY" "$BASE_URL/api/v1/servers"; then break; fi
  if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
    echo "ERROR: daemon exited early — log tail:" >&2
    tail -40 "$LOG_DIR/mcpproxy.log" >&2
    exit 1
  fi
  sleep 1
done

# Wait for BOTH named dockerized upstreams to connect, then for the startup
# config-reload bounce to settle (the daemon normalizes and re-saves the
# config on start, which the watcher treats as a change and reconnects the
# upstreams — scanning during that window fails with "returned 0 tools").
# A timeout on either wait fails the run: a dangerous scan of the attacker
# alone proves nothing about a lab whose victim never connected (Sol r9 P2).
READY_POLLS="${TPA_LAB_READY_POLLS:-60}"
upstreams_ready() {
  [[ "$(curl -sf -H "X-API-Key: $API_KEY" "$BASE_URL/api/v1/servers" 2>/dev/null |
    lab_upstreams_connected tpa-lab-attacker tpa-lab-victim)" == yes ]]
}

log "waiting for tpa-lab-attacker and tpa-lab-victim to connect"
if ! lab_wait_stable 1 "$READY_POLLS" 1 upstreams_ready; then
  echo "ERROR: tpa-lab-attacker and tpa-lab-victim did not both connect; log tail:" >&2
  tail -20 "$LOG_DIR/mcpproxy.log" >&2
  exit 1
fi

log "waiting for upstream connections to stabilize (3 consecutive connected polls)"
if ! lab_wait_stable 3 "$READY_POLLS" 2 upstreams_ready; then
  echo "ERROR: the upstream connections never stabilized; log tail:" >&2
  tail -20 "$LOG_DIR/mcpproxy.log" >&2
  exit 1
fi

log "running TPA scan on tpa-lab-attacker (expect verdict: dangerous)"
scan_ok=0
for attempt in 1 2 3; do
  if "$BINARY" security scan tpa-lab-attacker --config "$CONFIG" 2>&1 | tee "$LOG_DIR/scan.txt"; then
    scan_ok=1
    break
  fi
  log "scan attempt $attempt failed (connection settling?) — retrying in 5s"
  sleep 5
done
[[ "$scan_ok" == "1" ]] || { echo "ERROR: scan failed after retries — daemon log: $LOG_DIR/mcpproxy.log" >&2; exit 1; }

log "scan report for tpa-lab-attacker"
"$BINARY" security report tpa-lab-attacker --config "$CONFIG"

# A completed scan is not a passing baseline: the full attack catalog must
# scan dangerous, and the verdict must come from THIS scan's job (Sol r7 P2).
"$BINARY" security report tpa-lab-attacker -o json --config "$CONFIG" >"$LOG_DIR/report.json" 2>/dev/null || true
if ! baseline_verdict="$(baseline_scan_verdict_ok "$LOG_DIR/scan.txt" "$LOG_DIR/report.json")"; then
  echo "ERROR: baseline scan verdict is '$baseline_verdict', expected dangerous for job $(scan_job_id "$LOG_DIR/scan.txt") — report: $LOG_DIR/report.json, daemon log: $LOG_DIR/mcpproxy.log" >&2
  exit 1
fi
log "baseline verdict: $baseline_verdict (job $(scan_job_id "$LOG_DIR/scan.txt"))"

log "verify the upstreams really are sandboxed containers:"
docker ps --filter "name=mcpproxy-tpa-lab" --format 'table {{.Names}}\t{{.Image}}\t{{.Networks}}' || true

cat <<EOF

[tpa-lab] baseline scan complete. Next steps (daemon $([[ "$KEEP" == "1" ]] && echo running || echo now stopped)):
  - full findings:  mcpproxy security report tpa-lab-attacker -o json --config $CONFIG
  - rug pull:       KEEP=1 $0   then approve tools, call lab_arm_stage2 (live flip),
                    or set "TPA_LAB_STAGE": "2" in the config and rerun (restart path)
  - cleanup:        $( [[ "$KEEP" == "1" ]] && echo "kill $DAEMON_PID; " )rm -rf $DATA_DIR; docker rmi $IMAGE
EOF
