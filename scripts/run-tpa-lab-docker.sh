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
CONFIG="$PROJECT_DIR/tests/tpa-lab-config-docker.json"
IMAGE="${IMAGE:-tpa-attack-lab:lab}"
DATA_DIR="$PROJECT_DIR/test-data-tpa-lab-docker"
API_KEY="tpa-lab-key"
BASE_URL="http://127.0.0.1:8081"
KEEP="${KEEP:-0}"
LOG_DIR="$(mktemp -d)"

log() { echo "[tpa-lab] $*"; }

mkdir -p "$DATA_DIR"
export MCPPROXY_TRAY_ENDPOINT="unix://$DATA_DIR/mcpproxy.sock"

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  log "building lab image $IMAGE (context: repo root)"
  docker build -f "$PROJECT_DIR/tests/tpa-attack-lab/Dockerfile" -t "$IMAGE" "$PROJECT_DIR"
  log "building mcpproxy binary"
  (cd "$PROJECT_DIR" && go build -o ./mcpproxy ./cmd/mcpproxy)
fi

# Refuse to double-start against the same socket.
if curl -sf -o /dev/null -H "X-API-Key: $API_KEY" "$BASE_URL/api/v1/servers"; then
  echo "ERROR: something is already listening on $BASE_URL (previous run with KEEP=1?)." >&2
  echo "Stop it first: pkill -f 'mcpproxy serve.*tpa-lab-config-docker'" >&2
  exit 1
fi

log "starting mcpproxy (log: $LOG_DIR/mcpproxy.log)"
"$PROJECT_DIR/mcpproxy" serve --config "$CONFIG" --log-level=info >"$LOG_DIR/mcpproxy.log" 2>&1 &
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

# Wait for both dockerized upstreams to connect, then wait for the startup
# config-reload bounce to settle (the daemon normalizes and re-saves the
# config on start, which the watcher treats as a change and reconnects the
# upstreams — scanning during that window fails with "returned 0 tools").
connected_count() {
  curl -sf -H "X-API-Key: $API_KEY" "$BASE_URL/api/v1/servers" 2>/dev/null | python3 -c '
import json,sys
try:
    d=json.load(sys.stdin)
    servers=d.get("data",{}).get("servers",[])
    print(sum(1 for s in servers if s.get("status") in ("connected","Ready","ready")))
except Exception:
    print(0)' || echo 0
}

log "waiting for both isolated upstreams to connect"
for i in $(seq 1 60); do
  connected=$(connected_count)
  [[ "$connected" -ge 2 ]] && break
  sleep 1
done
if [[ "${connected:-0}" -lt 2 ]]; then
  echo "WARNING: only $connected/2 upstreams connected — continuing; log tail:" >&2
  tail -20 "$LOG_DIR/mcpproxy.log" >&2
fi

log "waiting for upstream connections to stabilize (2 consecutive stable polls)"
stable=0
for i in $(seq 1 60); do
  a=$(connected_count); sleep 2; b=$(connected_count)
  if [[ "$a" -ge 2 && "$b" -ge 2 ]]; then
    stable=$((stable+1))
    [[ "$stable" -ge 2 ]] && break
  else
    stable=0
  fi
done

log "running TPA scan on tpa-lab-attacker (expect verdict: dangerous)"
scan_ok=0
for attempt in 1 2 3; do
  if "$PROJECT_DIR/mcpproxy" security scan tpa-lab-attacker --config "$CONFIG"; then
    scan_ok=1
    break
  fi
  log "scan attempt $attempt failed (connection settling?) — retrying in 5s"
  sleep 5
done
[[ "$scan_ok" == "1" ]] || { echo "ERROR: scan failed after retries — daemon log: $LOG_DIR/mcpproxy.log" >&2; exit 1; }

log "scan report for tpa-lab-attacker"
"$PROJECT_DIR/mcpproxy" security report tpa-lab-attacker --config "$CONFIG"

log "verify the upstreams really are sandboxed containers:"
docker ps --filter "name=mcpproxy-tpa-lab" --format 'table {{.Names}}\t{{.Image}}\t{{.Networks}}' || true

cat <<EOF

[tpa-lab] baseline scan complete. Next steps (daemon $([[ "$KEEP" == "1" ]] && echo running || echo now stopped)):
  - full findings:  mcpproxy security report tpa-lab-attacker -o json --config $CONFIG
  - rug pull:       KEEP=1 $0   then approve tools, call lab_arm_stage2 (live flip),
                    or set "TPA_LAB_STAGE": "2" in the config and rerun (restart path)
  - cleanup:        pkill -f 'mcpproxy serve.*tpa-lab-config-docker'; rm -rf $DATA_DIR; docker rmi $IMAGE
EOF
