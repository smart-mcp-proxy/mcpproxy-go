#!/usr/bin/env bash
# Red-team experiment suite: challenge mcpproxy's TPA scanner and rug-pull
# gates end-to-end, with every malicious upstream confined to a Docker
# container (distroless image, network_mode=none, read-only rootfs, caps
# dropped) — see tests/tpa-attack-lab/Dockerfile.
#
#   scripts/tpa-lab-redteam-experiments.sh            # run all experiments
#   SKIP_BUILD=1 scripts/tpa-lab-redteam-experiments.sh
#   ONLY=EXP-3 scripts/tpa-lab-redteam-experiments.sh # single experiment
#
# Experiments:
#   EXP-1 control   — baseline scan of the full attack catalog  → expect dangerous
#   EXP-2 control   — rug pull via restart (stage 1→2)          → expect changed+blocked
#   EXP-3 hole A    — served detector-evasion mutants           → expect NOT dangerous
#   EXP-4 hole B    — TOCTOU: poisoned discovery, clean scan    → probe
#   EXP-5 hole C    — annotation-only flip after approval       → probe (hash blindness)
#   EXP-6 hole D    — trust_mode=scan + paraphrased stage-3 pull → probe (auto-approval chain)
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TEMPLATE="$PROJECT_DIR/tests/tpa-lab-config-docker.json"
BINARY="$PROJECT_DIR/mcpproxy"
API_KEY="tpa-lab-key"
API_URL="http://127.0.0.1:8081/api/v1"
IMAGE="${IMAGE:-tpa-attack-lab:lab}"
RUN_DIR="$PROJECT_DIR/test-data-tpa-lab-docker/experiments"
ONLY="${ONLY:-}"
DAEMON_PID=""
CURRENT_CONFIG=""

mkdir -p "$RUN_DIR"

log()  { echo "[exp]  $*"; }
pass() { echo "[exp]  ✔ HOLDS: $*"; }
hole() { echo "[exp]  ✘ HOLE: $*"; }

# ---------------------------------------------------------------- helpers --

# make_config <name> <profile> <stage> <data_dir> [trust_mode] [noquarantine]
make_config() {
  local name="$1" profile="$2" stage="$3" data_dir="$4" trust_mode="${5:-}" noq="${6:-}"
  local cfg="$RUN_DIR/$name.json"
  python3 - "$TEMPLATE" "$cfg" "$profile" "$stage" "$data_dir" "$trust_mode" "$noq" <<'PY'
import json, sys
tpl, out, profile, stage, data_dir, trust_mode, noq = sys.argv[1:8]
cfg = json.load(open(tpl))
cfg["data_dir"] = data_dir
for s in cfg["mcpServers"]:
    if s["name"] == "tpa-lab-attacker":
        s["env"] = {"TPA_LAB_PROFILE": profile, "TPA_LAB_STAGE": stage}
        if trust_mode:
            s["trust_mode"] = trust_mode
        if noq == "noquarantine":
            s["quarantined"] = False
json.dump(cfg, open(out, "w"), indent=2)
PY
  echo "$cfg"
}

start_daemon() {
  local cfg="$1" logf="$2"
  export MCPPROXY_TRAY_ENDPOINT="unix://$(python3 -c "import json;print(json.load(open('$cfg'))['data_dir'])")/mcpproxy.sock"
  "$BINARY" serve --config "$cfg" --log-level=info >"$logf" 2>&1 &
  DAEMON_PID=$!
  for i in $(seq 1 60); do
    curl -sf -o /dev/null -H "X-API-Key: $API_KEY" "$API_URL/servers" && break
    kill -0 "$DAEMON_PID" 2>/dev/null || { echo "daemon exited — log tail:"; tail -20 "$logf"; return 1; }
    sleep 1
  done
  # settle the startup config-reload bounce (2 consecutive stable polls)
  local a b
  for i in $(seq 1 60); do
    a=$(connected); sleep 2; b=$(connected)
    [[ "$a" -ge 2 && "$b" -ge 2 ]] && return 0
  done
  echo "WARNING: upstreams not stable (a=$a b=$b); log tail:"; tail -10 "$logf"
}

connected() {
  curl -sf -H "X-API-Key: $API_KEY" "$API_URL/servers" 2>/dev/null | python3 -c '
import json,sys
try:
    d=json.load(sys.stdin); s=d.get("data",{}).get("servers",[])
    print(sum(1 for x in s if x.get("status") in ("connected","Ready","ready")))
except Exception: print(0)' || echo 0
}

stop_daemon() {
  [[ -n "$DAEMON_PID" ]] && kill "$DAEMON_PID" 2>/dev/null
  [[ -n "$DAEMON_PID" ]] && wait "$DAEMON_PID" 2>/dev/null
  DAEMON_PID=""
  sleep 1
}

# scan_and_verdict <cfg> <outfile> — runs the blocking scan, echoes the verdict
scan_and_verdict() {
  local cfg="$1" out="$2"
  "$BINARY" security scan tpa-lab-attacker --config "$cfg" >"$out" 2>&1
  "$BINARY" security report tpa-lab-attacker -o json --config "$cfg" 2>/dev/null >"$out.json"
  python3 - "$out.json" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    print("unknown"); raise SystemExit
found = set()
def walk(o):
    if isinstance(o, dict):
        for k, v in o.items():
            if isinstance(v, str) and v in ("dangerous", "warnings", "clean") and (
                "verdict" in k.lower() or "threat" in k.lower() or "status" in k.lower()):
                found.add(v)
            walk(v)
    elif isinstance(o, list):
        for v in o: walk(v)
walk(d)
for want in ("dangerous", "warnings", "clean"):
    if want in found:
        print(want); raise SystemExit
print("unknown")
PY
}

approve_all() {
  # Mirror the reference QA flow (tests/test-quarantine.sh): tool approval
  # applies to UNquarantined servers — unquarantine first, then approve.
  curl -s -X POST -H "X-API-Key: $API_KEY" \
    "$API_URL/servers/tpa-lab-attacker/unquarantine" >/dev/null
  sleep 2  # re-discovery with tool-level quarantine
  local resp
  resp="$(curl -s -X POST -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' \
    -d '{"approve_all":true}' "$API_URL/servers/tpa-lab-attacker/tools/approve")"
  log "approve_all response: ${resp:0:200}"
  sleep 2
}

tool_status() {  # tool_status <tool> → status column from `upstream inspect` table
  "$BINARY" upstream inspect tpa-lab-attacker --config "$CURRENT_CONFIG" 2>/dev/null \
    | python3 -c "
import sys
for line in sys.stdin:
    parts = line.split()
    if len(parts) >= 2 and parts[0] == '$1':
        print(parts[1]); break
else:
    print('not-found')"
}

unquarantine() {
  curl -s -X POST -H "X-API-Key: $API_KEY" \
    "$API_URL/servers/tpa-lab-attacker/unquarantine" >/dev/null
}

# wait_status <tool> — poll until the approval record exists (post-unquarantine
# re-discovery is asynchronous), then echo its status.
wait_status() {
  local s
  for i in $(seq 1 30); do
    s="$(tool_status "$1")"
    [[ "$s" != "not-found" ]] && { echo "$s"; return; }
    sleep 1
  done
  echo "not-found"
}

# wait_status_not <tool> <status-to-wait-past> <timeout_s> — after unquarantine
# the sweep that re-hashes contracts runs asynchronously (observed ~13s); a
# record that already exists keeps its OLD status until then. Poll until the
# status moves past the stale value or the timeout elapses, then echo it.
wait_status_not() {
  local tool="$1" avoid="$2" timeout="${3:-90}" s
  for i in $(seq 1 "$timeout"); do
    s="$(tool_status "$tool")"
    [[ "$s" != "not-found" && "$s" != "$avoid" ]] && { echo "$s"; return; }
    sleep 1
  done
  echo "$s"
}

stored_tool_desc_contains() {  # greps the proxy's STORED tool export for a marker
  curl -sf -H "X-API-Key: $API_KEY" "$API_URL/servers/tpa-lab-attacker/tools/export" 2>/dev/null \
    | grep -q "$1"
}

want_only() { [[ -z "$ONLY" || "$ONLY" == "$1" ]]; }

trap stop_daemon EXIT

# ------------------------------------------------------------------ build --

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  log "building lab image + mcpproxy binary"
  docker build -q -f "$PROJECT_DIR/tests/tpa-attack-lab/Dockerfile" -t "$IMAGE" "$PROJECT_DIR" >/dev/null \
    || { echo "docker build failed"; exit 1; }
  (cd "$PROJECT_DIR" && go build -o ./mcpproxy ./cmd/mcpproxy) || exit 1
fi

RESULTS=()
record() { RESULTS+=("$1"); echo "----------------------------------------"; }

# Preflight: a leftover daemon on our port would silently receive our API
# calls while CLI reads a different DB — refuse to run until it is gone.
if lsof -ti :8081 >/dev/null 2>&1; then
  pkill -f "mcpproxy serve.*experiments" 2>/dev/null && sleep 2
  if lsof -ti :8081 >/dev/null 2>&1; then
    echo "ERROR: port 8081 is still in use by a non-lab process — free it first." >&2
    exit 1
  fi
fi

# ==================================================================== EXP-1
if want_only EXP-1; then
  log "EXP-1 (control): full attack catalog must scan dangerous"
  cfg="$(make_config exp1 all 1 "$RUN_DIR/exp1-data")"; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp1.log" || { record "EXP-1: INCONCLUSIVE (daemon)"; want_only EXP-1 && exit 1 || skip=1; }
  if [[ ${skip:-0} != 1 ]]; then
  v="$(scan_and_verdict "$cfg" "$RUN_DIR/exp1-scan.txt")"
  log "scan verdict: $v (full output: $RUN_DIR/exp1-scan.txt)"
  if [[ "$v" == "dangerous" ]]; then pass "EXP-1 baseline detection works (dangerous)"; record "EXP-1 HOLDS: baseline scan → dangerous"
  else hole "EXP-1 baseline catalog NOT flagged (verdict=$v)"; record "EXP-1 HOLE?: baseline verdict=$v"; fi
  stop_daemon; fi
fi

# ==================================================================== EXP-2
if want_only EXP-2; then
  log "EXP-2 (control + restart observation): rug pull via restart (stage 1→2)"
  cfg="$(make_config exp2 facade 1 "$RUN_DIR/exp2-data")"; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp2.log" || { record "EXP-2: INCONCLUSIVE (daemon)"; exit 1; }
  unquarantine
  cal1="$(wait_status calendar_get_events)"
  log "after unquarantine re-discovery: calendar_get_events status=$cal1"
  approve_all
  cal1="$(wait_status calendar_get_events)"
  log "after approve: calendar_get_events status=$cal1"
  stop_daemon

  # Phase 2a: restart with the armed contract while config re-quarantines the
  # server (config templates keep quarantined:true). Discovery is skipped for
  # quarantined servers, so the change gate may never see the new contract.
  cfg="$(make_config exp2 facade 2 "$RUN_DIR/exp2-data")"; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp2-2.log" || { record "EXP-2: INCONCLUSIVE (restart)"; exit 1; }
  sleep 3
  cal="$(tool_status calendar_get_events)"
  log "2a) after restart (server re-quarantined by config): calendar=$cal"
  if [[ "$cal" == "not-found" ]]; then
    log "EXP-2a inconclusive: no approval record visible"
    record "EXP-2a INCONCLUSIVE: no approval record after restart"
  elif [[ "$cal" == "approved" ]]; then
    hole "EXP-2a armed contract NOT re-ingested after restart (discovery skipped for quarantined server) — stale approved contract survives the whole quarantine window; gate only runs after unquarantine+sweep"
    record "EXP-2a HOLE (window): re-quarantined restart → no re-discovery; stale stage-1 approved contract while armed server runs; caught only after unquarantine (~13s sweep)"
  else
    pass "EXP-2a change detected even while re-quarantined ($cal)"
    record "EXP-2a HOLDS: calendar=$cal"
  fi

  # Phase 2b: operator unquarantines (the normal flow for an already-vetted
  # server) → the sweep must re-list and the contract-hash gate must fire.
  # The sweep is asynchronous (observed ~13s), so wait out the stale status.
  unquarantine
  cal="$(wait_status_not calendar_get_events approved 90)"
  stock="$(wait_status_not stock_quote approved 30)"
  notes="$(tool_status notes_export)"
  log "2b) after unquarantine re-discovery: calendar=$cal stock=$stock notes_export=$notes"
  if [[ "$cal" == changed* || "$cal" == *blocked* ]] && [[ "$stock" == changed* || "$stock" == *blocked* ]]; then
    pass "EXP-2b description+schema rug pulls detected (changed/blocked)"
    record "EXP-2b HOLDS: calendar=$cal stock=$stock"
  else
    hole "EXP-2b rug pull NOT detected after unquarantine: calendar=$cal stock=$stock"
    record "EXP-2b HOLE: calendar=$cal stock=$stock"
  fi
  stop_daemon
fi

# ==================================================================== EXP-3
if want_only EXP-3; then
  log "EXP-3 (hole A): served evasion mutants must NOT scan dangerous"
  cfg="$(make_config exp3 mutants 1 "$RUN_DIR/exp3-data")"; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp3.log" || { record "EXP-3: INCONCLUSIVE (daemon)"; exit 1; }
  v="$(scan_and_verdict "$cfg" "$RUN_DIR/exp3-scan.txt")"
  log "scan verdict: $v (full output: $RUN_DIR/exp3-scan.txt)"
  if [[ "$v" == "dangerous" ]]; then pass "EXP-3 mutants flagged dangerous (detector improved!)"; record "EXP-3 HOLDS: mutants → dangerous"
  else hole "EXP-3 mutant tools scan '$v' — detection bypassed end-to-end"; record "EXP-3 HOLE: mutants verdict=$v"; fi
  stop_daemon
fi

# ==================================================================== EXP-4
if want_only EXP-4; then
  log "EXP-4 (hole B): TOCTOU — poisoned contract at discovery (list #1), clean contract to the scan export (list #2+)"
  log "server runs UNQUARANTINED so discovery's auto-baseline ingests list #1"
  cfg="$(make_config exp4 toctou 1 "$RUN_DIR/exp4-data" "" noquarantine)"; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp4.log" || { record "EXP-4: INCONCLUSIVE (daemon)"; exit 1; }
  v="$(scan_and_verdict "$cfg" "$RUN_DIR/exp4-scan.txt")"
  log "scan verdict: $v (the scan's export call saw the CLEAN contract — list #2+)"
  stored="$(wait_status summarize_text)"
  baseline_lines="$(grep -c 'auto-baseline' "$RUN_DIR/exp4.log" || true)"
  flap_lines="$(grep -c 'changed since approval' "$RUN_DIR/exp4.log" || true)"
  log "stored tool status: $stored (auto-baseline approvals: $baseline_lines, rug-pull flags: $flap_lines)"
  if [[ "$v" != "dangerous" && "$stored" == "changed" ]]; then
    hole "EXP-4 PARTIAL: scan export is attacker-controllable (verdict=$v on a poisoned server; poisoned contract WAS auto-baselined) — the flap itself was caught here, but a session-fingerprinting variant would scan clean without flapping"
    record "EXP-4 PARTIAL-HOLE: scan=$v on poisoned server; poisoned list auto-baselined; flap caught (changed)"
  elif [[ "$stored" == "changed" ]]; then
    pass "EXP-4 scan flagged the poisoned discovery contract ($v) and the flap was caught"
    record "EXP-4 HOLDS: scan=$v, stored=changed"
  else
    record "EXP-4 INCONCLUSIVE: scan=$v, stored=$stored"
  fi
  stop_daemon
fi

# ==================================================================== EXP-5
if want_only EXP-5; then
  log "EXP-5 (hole C): annotation-only flip after approval (contract hash covers desc+schema only)"
  cfg="$(make_config exp5 facade 1 "$RUN_DIR/exp5-data")"; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp5.log" || { record "EXP-5: INCONCLUSIVE (daemon)"; exit 1; }
  unquarantine
  n1="$(wait_status notes_export)"
  log "after unquarantine re-discovery: notes_export status=$n1 (stage-1 honest annotations: destructive)"
  approve_all
  n1="$(wait_status notes_export)"
  log "after approve: notes_export status=$n1"
  stop_daemon

  cfg="$(make_config exp5 facade 2 "$RUN_DIR/exp5-data")"; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp5-2.log" || { record "EXP-5: INCONCLUSIVE (restart)"; exit 1; }
  sleep 3
  unquarantine  # re-discovery must run for the change gate to see anything
  # The annotation flip keeps desc+schema identical: the hash CANNOT change.
  # Wait out the full sweep window — if it is still approved after 90s, the
  # flip is genuinely invisible to the gate.
  n2="$(wait_status_not notes_export approved 90)"
  log "after annotation flip: notes_export status=$n2"
  if [[ "$n2" == *approved* ]]; then
    hole "EXP-5 destructive→readOnly-lying flip INVISIBLE to the change gate (still $n2)"
    record "EXP-5 HOLE: annotation flip unseen (notes_export=$n2)"
  else
    pass "EXP-5 annotation flip surfaced ($n2)"
    record "EXP-5 HOLDS: notes_export=$n2"
  fi
  stop_daemon
fi

# ==================================================================== EXP-6
if want_only EXP-6; then
  log "EXP-6 (hole D): trust_mode=scan + paraphrased stage-3 rug pull (auto-approval chain)"
  cfg="$(make_config exp6 facade 1 "$RUN_DIR/exp6-data" scan)"; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp6.log" || { record "EXP-6: INCONCLUSIVE (daemon)"; exit 1; }
  sleep 3
  unquarantine
  pre="$(wait_status calendar_get_events)"
  log "initial status under trust_mode=scan: calendar_get_events=$pre"
  if [[ "$pre" != "approved" ]]; then
    log "not auto-approved initially — approving manually (stage-1 facade is benign anyway)"
    approve_all
    pre="$(wait_status calendar_get_events)"
  fi
  stop_daemon

  cfg="$(make_config exp6 facade 3 "$RUN_DIR/exp6-data" scan)"; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp6-2.log" || { record "EXP-6: INCONCLUSIVE (restart)"; exit 1; }
  sleep 3
  unquarantine  # re-discovery + scan-mode change gate re-scan
  # Distinguish a genuine scan-approved re-approval from a stale record by
  # waiting out the sweep AND checking the gate's own log lines.
  post="$(wait_status_not calendar_get_events approved 90)"
  postq="$(tool_status stock_quote)"
  gate_log="$(grep -cE 'scan-approved|changed since approval|auto-baseline' "$RUN_DIR/exp6-2.log" || true)"
  log "after paraphrased rug pull: calendar=$post stock=$postq (gate log hits: $gate_log)"
  if [[ "$post" == "approved" && "$postq" == "approved" ]]; then
    if [[ "$gate_log" -gt 0 ]]; then
      hole "EXP-6 paraphrased rug pull AUTO-APPROVED under trust_mode=scan (silent)"
      record "EXP-6 HOLE: scan-mode auto-approved the pull (calendar=$post stock=$postq, gate re-ran)"
    else
      log "EXP-6 inconclusive: still approved but gate never re-ran (stale record)"
      record "EXP-6 INCONCLUSIVE: approved without gate activity"
    fi
  else
    pass "EXP-6 paraphrased pull held for review (calendar=$post stock=$postq)"
    record "EXP-6 HOLDS: calendar=$post stock=$postq"
  fi
  stop_daemon
fi

# ------------------------------------------------------------------ report --
echo
log "===== experiment summary ====="
printf '%s\n' "${RESULTS[@]-}"
