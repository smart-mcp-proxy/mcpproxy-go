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
#                     (2a re-quarantined, 2b after unquarantine, 2c manual
#                     mode trusted throughout: schema-only stock_quote held)
#   EXP-3 hole A    — served detector-evasion mutants           → expect NOT dangerous
#   EXP-4 hole B    — TOCTOU: poisoned discovery, clean scan    → probe
#   EXP-5 hole C    — annotation-only flip after approval       → probe (hash blindness)
#   EXP-6 hole D    — trust_mode=scan + paraphrased stage-3 pull → probe (auto-approval chain)
#
# Exit status: 0 only when every experiment that ran HOLDS. A HOLE, a
# PARTIAL-HOLE or an INCONCLUSIVE result (failed baseline approval, failed
# scan) exits 1. The EXP-3 detector-evasion hole is the one allowed failure,
# deferred to RT-04/RT-07 (scripts/lib/tpa-lab-verdicts.sh).
#
# IMAGE=<tag> builds that tag and runs it: every generated config's isolation
# blocks are rewritten to the same image.
#
# A failed config generation aborts the suite before any daemon or CLI runs:
# "$BINARY ... --config ''" would fall back to the user's normal config
# discovery (~/.mcpproxy) and start unrelated upstreams (Sol r6 P1). Every
# daemon/CLI invocation also re-checks its config with lab_config_require.
# The script deliberately does not use errexit: many probes are expected to
# fail and are turned into verdicts.
#
# Test hooks (scripts_test.go): TPA_LAB_TEMPLATE, TPA_LAB_BINARY and
# TPA_LAB_RUN_ROOT replace the template, binary and run root.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TEMPLATE="${TPA_LAB_TEMPLATE:-$PROJECT_DIR/tests/tpa-lab-config-docker.json}"
BINARY="${TPA_LAB_BINARY:-$PROJECT_DIR/mcpproxy}"
API_KEY="tpa-lab-key"
API_URL="http://127.0.0.1:8081/api/v1"
IMAGE="${IMAGE:-tpa-attack-lab:lab}"
RUN_ROOT="${TPA_LAB_RUN_ROOT:-$PROJECT_DIR/test-data-tpa-lab-docker}"
# shellcheck source=lib/tpa-lab-verdicts.sh
source "$SCRIPT_DIR/lib/tpa-lab-verdicts.sh"
# Inherited MCPPROXY_DATA / MCPPROXY_LISTEN / MCPPROXY_API_KEY (and every
# other MCPPROXY_* override) would beat each generated config's data_dir and
# listen address for the daemon AND the CLI calls; drop them before anything
# runs (Sol r8 P1). start_daemon also pins --data-dir and --listen.
lab_scrub_mcpproxy_env
LISTEN="127.0.0.1:8081"
ONLY="${ONLY:-}"
DAEMON_PID=""
CURRENT_CONFIG=""

# One fresh run dir per suite invocation, shared by every stage restart of
# that invocation: approvals or a scan report left by an earlier run can
# never satisfy this one. The name stays short — each experiment's data dir
# holds the daemon's unix socket (macOS sun_path limit is 104 bytes).
# RUN_DIR may be set to reuse a directory explicitly.
mkdir -p "$RUN_ROOT"
RUN_DIR="${RUN_DIR:-$(mktemp -d "$RUN_ROOT/xXXXX")}"
mkdir -p "$RUN_DIR"
ln -sfn "$RUN_DIR" "$RUN_ROOT/experiments-latest"

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
  [[ $? -eq 0 ]] || { echo "lab config generation failed for $cfg" >&2; return 1; }
  # Run the image this invocation built (IMAGE override), never a stale
  # default tag; refuses a config whose upstreams are not Docker-isolated.
  lab_config_use_image "$cfg" "$IMAGE" >&2 || { echo "refusing lab config $cfg" >&2; return 1; }
  echo "$cfg"
}

# config_failed <exp> — a lab config could not be generated: abort the whole
# suite before anything runs against an empty or unchecked config path.
config_failed() {
  echo "ERROR: $1: lab config generation failed — aborting before any daemon or CLI runs" >&2
  RESULTS+=("$1: INCONCLUSIVE (config generation failed)")
  printf '%s\n' "${RESULTS[@]-}"
  exit 1
}

start_daemon() {
  local cfg="$1" logf="$2"
  lab_config_require "$cfg" || { echo "refusing to start the daemon without a checked lab config"; return 1; }
  local ddir
  ddir="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["data_dir"])' "$cfg")" || return 1
  export MCPPROXY_TRAY_ENDPOINT="unix://$ddir/mcpproxy.sock"
  "$BINARY" serve --config "$cfg" --data-dir "$ddir" --listen "$LISTEN" --log-level=info >"$logf" 2>&1 &
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
# of THAT scan job: "scan-failed" when the scan did not complete, and
# "stale-report" when the stored report belongs to another job.
scan_and_verdict() {
  local cfg="$1" out="$2" rc job
  lab_config_require "$cfg" 2>"$out" || { echo "scan-failed"; return; }
  "$BINARY" security scan tpa-lab-attacker --config "$cfg" >"$out" 2>&1
  rc=$?
  job="$(scan_job_id "$out")"
  if [[ $rc -ne 0 || -z "$job" ]]; then
    echo "scan-failed"; return
  fi
  "$BINARY" security report tpa-lab-attacker -o json --config "$cfg" 2>/dev/null >"$out.json"
  scan_report_verdict "$out.json" "$job"
}

approve_all() {
  # Mirror the reference QA flow (tests/test-quarantine.sh): tool approval
  # applies to UNquarantined servers — unquarantine first, then approve.
  # Returns non-zero unless the approve call reported success.
  curl -s -X POST -H "X-API-Key: $API_KEY" \
    "$API_URL/servers/tpa-lab-attacker/unquarantine" >/dev/null
  sleep 2  # re-discovery with tool-level quarantine
  local resp
  resp="$(curl -s -X POST -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' \
    -d '{"approve_all":true}' "$API_URL/servers/tpa-lab-attacker/tools/approve")"
  log "approve_all response: ${resp:0:200}"
  sleep 2
  python3 -c 'import json,sys
d=json.loads(sys.argv[1]); d=d if isinstance(d,dict) else {}
sys.exit(0 if d.get("success") is True and not (d.get("data") or {}).get("still_pending") and not (d.get("data") or {}).get("still_changed") else 1)' "$resp" 2>/dev/null
}

# all_approved <tool>... — every named tool's record is approved (the
# approved baseline a rug-pull experiment needs before it can mean anything).
all_approved() {
  local t s
  for t in "$@"; do
    s="$(wait_status "$t")"
    [[ "$s" == "approved" ]] || { log "baseline not approved: $t=$s"; return 1; }
  done
}

tool_status() {  # tool_status <tool> → status column from `upstream inspect` table
  lab_config_require "$CURRENT_CONFIG" 2>/dev/null || { echo "unknown"; return; }
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

# unquarantine — returns non-zero unless the API reported success (Sol r4 P2).
unquarantine() {
  local resp
  resp="$(curl -s -X POST -H "X-API-Key: $API_KEY" \
    "$API_URL/servers/tpa-lab-attacker/unquarantine")"
  python3 -c 'import json,sys
try: d=json.loads(sys.argv[1])
except Exception: sys.exit(1)
sys.exit(0 if isinstance(d,dict) and d.get("success") is True else 1)' "$resp" 2>/dev/null \
    || { log "unquarantine failed: ${resp:0:200}"; return 1; }
}

# server_quarantined — "true"/"false" from the REST server list, else "unknown".
server_quarantined() {
  curl -sf -H "X-API-Key: $API_KEY" "$API_URL/servers" 2>/dev/null | python3 -c '
import json,sys
try:
    for s in json.load(sys.stdin).get("data",{}).get("servers",[]):
        if s.get("name") == "tpa-lab-attacker":
            print("true" if s.get("quarantined") else "false"); break
    else: print("unknown")
except Exception: print("unknown")' || echo unknown
}

# tool_indexed <tool> — "yes"/"no" whether tpa-lab-attacker:<tool> is in the
# search index (what clients can discover), "unknown" when the search failed.
tool_indexed() {
  curl -sf -G -H "X-API-Key: $API_KEY" --data-urlencode "q=$1" --data-urlencode "limit=100" \
    "$API_URL/index/search" 2>/dev/null | python3 -c '
import json,sys
want = sys.argv[1]
try:
    res = json.load(sys.stdin).get("data",{}).get("results") or []
    hit = any((r.get("tool") or {}).get("server_name") == "tpa-lab-attacker" and
              (r.get("tool") or {}).get("name") in (want, "tpa-lab-attacker:" + want) for r in res)
    print("yes" if hit else "no")
except Exception: print("unknown")' "$1" || echo unknown
}

# wait_indexed <tool> <timeout_s> — "yes" once the tool is indexed, else the
# last tool_indexed answer.
wait_indexed() {
  local s
  for i in $(seq 1 "${2:-90}"); do
    s="$(tool_indexed "$1")"
    [[ "$s" == "yes" ]] && { echo yes; return; }
    sleep 1
  done
  echo "$s"
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

# unquarantine_and_prove — unquarantines tpa-lab-attacker and proves the
# post-unquarantine discovery ran (Sol r4/r5 P2): sets UNQ_OK (1 when the API
# reported success), QUARANTINED (server state after it) and DISCOVERED
# ("yes" only when benign_calculator — unchanged, approved — was NOT in the
# index before the unquarantine and reached it afterwards; the startup guard
# removes a quarantined server's tools from the index, so a "yes" is fresh).
unquarantine_and_prove() {
  local pre
  pre="$(tool_indexed benign_calculator)"
  UNQ_OK=0; unquarantine && UNQ_OK=1
  QUARANTINED="$(server_quarantined)"
  DISCOVERED="$(wait_indexed benign_calculator 90)"
  [[ "$pre" == "no" ]] || DISCOVERED="unproven(indexed-before=$pre)"
}

# gate_held_logged <log> <tool>... — "yes" when the daemon log shows the
# TRUSTED-server change gate holding every named tool and no quarantined
# verification ran (that path would also mark them changed).
gate_held_logged() {
  local logf="$1" t
  shift
  grep -q 'Verified quarantined server tool contracts' "$logf" && { echo "no(quarantined-verify-ran)"; return; }
  for t in "$@"; do
    grep -E 'changed since approval|held for operator confirmation' "$logf" | grep -q "\"tool\": \"$t\"" \
      || { echo "no($t)"; return; }
  done
  echo yes
}

# copy_data_dir <src> <dst> — clone a stopped daemon's data dir (approved
# baseline) for a variant run; the stale socket is dropped.
copy_data_dir() {
  rm -rf "$2" && cp -R "$1" "$2" && rm -f "$2/mcpproxy.sock"
}

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
  pkill -f "mcpproxy serve.*test-data-tpa-lab-docker" 2>/dev/null && sleep 2
  if lsof -ti :8081 >/dev/null 2>&1; then
    echo "ERROR: port 8081 is still in use by a non-lab process — free it first." >&2
    exit 1
  fi
fi

# ==================================================================== EXP-1
if want_only EXP-1; then
  log "EXP-1 (control): full attack catalog must scan dangerous"
  cfg="$(make_config exp1 all 1 "$RUN_DIR/exp1-data")" || config_failed EXP-1; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp1.log" || { record "EXP-1: INCONCLUSIVE (daemon)"; want_only EXP-1 && exit 1 || skip=1; }
  if [[ ${skip:-0} != 1 ]]; then
  v="$(scan_and_verdict "$cfg" "$RUN_DIR/exp1-scan.txt")"
  log "scan verdict: $v (full output: $RUN_DIR/exp1-scan.txt)"
  if [[ "$v" == "scan-failed" || "$v" == "stale-report" ]]; then record "EXP-1 INCONCLUSIVE: scan=$v"
  elif [[ "$v" == "dangerous" ]]; then pass "EXP-1 baseline detection works (dangerous)"; record "EXP-1 HOLDS: baseline scan → dangerous"
  else hole "EXP-1 baseline catalog NOT flagged (verdict=$v)"; record "EXP-1 HOLE?: baseline verdict=$v"; fi
  stop_daemon; fi
fi

# ==================================================================== EXP-2
if want_only EXP-2; then
  log "EXP-2 (control + restart observation): rug pull via restart (stage 1→2)"
  cfg="$(make_config exp2 facade 1 "$RUN_DIR/exp2-data")" || config_failed EXP-2; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp2.log" || { record "EXP-2: INCONCLUSIVE (daemon)"; exit 1; }
  unquarantine
  cal1="$(wait_status calendar_get_events)"
  log "after unquarantine re-discovery: calendar_get_events status=$cal1"
  approve_ok=0; approve_all && approve_ok=1
  cal1="$(wait_status calendar_get_events)"
  log "after approve: calendar_get_events status=$cal1 (approve ok=$approve_ok)"
  baseline_ok=0
  [[ $approve_ok == 1 ]] && all_approved calendar_get_events stock_quote && baseline_ok=1
  stop_daemon

  if [[ $baseline_ok != 1 ]]; then
    record "EXP-2a INCONCLUSIVE: stage-1 baseline was not approved"
    record "EXP-2b INCONCLUSIVE: stage-1 baseline was not approved"
    record "EXP-2c INCONCLUSIVE: stage-1 baseline was not approved"
  else
  # Keep a pristine copy of the approved baseline for EXP-2c: EXP-2a/2b move
  # the records to changed, so they cannot show a trusted server's gate
  # holding a fresh schema-only pull (Sol r8 P2).
  copy_data_dir "$RUN_DIR/exp2-data" "$RUN_DIR/exp2c-data"
  # Phase 2a: restart with the armed contract while config re-quarantines the
  # server (config templates keep quarantined:true). Discovery is skipped for
  # quarantined servers, so the change gate may never see the new contract.
  cfg="$(make_config exp2 facade 2 "$RUN_DIR/exp2-data")" || config_failed EXP-2; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp2-2.log" || { record "EXP-2: INCONCLUSIVE (restart)"; exit 1; }
  sleep 3
  # Both rug-pulled tools must be held (changed/blocked); a pending or
  # missing record proves nothing (Sol r3 P2).
  cal="$(wait_status_not calendar_get_events approved 30)"
  stock="$(wait_status_not stock_quote approved 15)"
  log "2a) after restart (server re-quarantined by config): calendar=$cal stock=$stock"
  case "$(exp2a_verdict "$cal" "$stock")" in
    HOLDS)
      pass "EXP-2a change detected even while re-quarantined (calendar=$cal stock=$stock)"
      record "EXP-2a HOLDS: calendar=$cal stock=$stock" ;;
    HOLE)
      hole "EXP-2a armed contract NOT re-ingested after restart (discovery skipped for quarantined server) — stale approved contract survives the whole quarantine window; gate only runs after unquarantine+sweep"
      record "EXP-2a HOLE (window): re-quarantined restart → stale approved contract while armed server runs (calendar=$cal stock=$stock)" ;;
    *)
      log "EXP-2a inconclusive: neither held nor approved"
      record "EXP-2a INCONCLUSIVE: calendar=$cal stock=$stock (not both held)" ;;
  esac

  # Phase 2b: operator unquarantines (the normal flow for an already-vetted
  # server) → the sweep must re-list and the contract-hash gate must fire.
  # The sweep is asynchronous (observed ~13s), so wait out the stale status.
  # The EXP-2a records already read "changed", so prove the
  # post-unquarantine flow itself (Sol r4 P2): the unquarantine succeeded,
  # the server is no longer quarantined, discovery ran (the unchanged
  # benign_calculator reached the index) and the held tools stayed out of it.
  unquarantine_and_prove
  unq_ok="$UNQ_OK" quarantined="$QUARANTINED" discovered="$DISCOVERED"
  cal="$(wait_status_not calendar_get_events approved 90)"
  stock="$(wait_status_not stock_quote approved 30)"
  cal_idx="$(tool_indexed calendar_get_events)"
  stock_idx="$(tool_indexed stock_quote)"
  notes="$(tool_status notes_export)"
  log "2b) after unquarantine (ok=$unq_ok quarantined=$quarantined discovered=$discovered): calendar=$cal (indexed=$cal_idx) stock=$stock (indexed=$stock_idx) notes_export=$notes"
  case "$(exp2b_verdict "$unq_ok" "$quarantined" "$discovered" "$cal" "$stock" "$cal_idx" "$stock_idx")" in
    HOLDS)
      pass "EXP-2b description+schema rug pulls held after unquarantine (changed/blocked, not indexed)"
      record "EXP-2b HOLDS: calendar=$cal stock=$stock (unquarantined, discovered, held tools not indexed)" ;;
    HOLE)
      hole "EXP-2b rug pull NOT held after unquarantine: calendar=$cal ($cal_idx) stock=$stock ($stock_idx)"
      record "EXP-2b HOLE: calendar=$cal (indexed=$cal_idx) stock=$stock (indexed=$stock_idx)" ;;
    *)
      record "EXP-2b INCONCLUSIVE: unquarantine ok=$unq_ok quarantined=$quarantined discovered=$discovered calendar=$cal stock=$stock" ;;
  esac
  stop_daemon

  # EXP-2c: the stage-2 pull reaches a manual-mode (default trust_mode)
  # server that stays TRUSTED throughout — the restart never re-quarantines
  # it — starting from the approved baseline. Only the trusted server's
  # discovery gate can hold the schema-only stock_quote change, so a
  # re-introduced approved→changed fallback (RT-01) shows up here as an
  # approved or indexed stock_quote (Sol r8 P2).
  cfg="$(make_config exp2c facade 2 "$RUN_DIR/exp2c-data" "" noquarantine)" || config_failed EXP-2c; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp2c.log" || { record "EXP-2c: INCONCLUSIVE (restart)"; exit 1; }
  q="$(server_quarantined)"
  stock="$(wait_status_not stock_quote approved 90)"
  cal="$(wait_status_not calendar_get_events approved 30)"
  stock_idx="$(tool_indexed stock_quote)"
  cal_idx="$(tool_indexed calendar_get_events)"
  proof="$(gate_held_logged "$RUN_DIR/exp2c.log" stock_quote calendar_get_events)"
  log "EXP-2c (manual mode, trusted throughout, quarantined=$q gate=$proof): stock=$stock (indexed=$stock_idx) calendar=$cal (indexed=$cal_idx)"
  case "$(held_unquarantined_verdict 1 "$q" "$proof" "$stock" "$stock_idx" "$cal" "$cal_idx")" in
    HOLDS)
      pass "EXP-2c schema-only pull held on a trusted manual-mode server (stock=$stock, not discovered)"
      record "EXP-2c HOLDS: stock=$stock calendar=$cal (manual mode, trusted throughout, gate held them, not indexed)" ;;
    HOLE)
      hole "EXP-2c schema-only pull NOT held on a trusted manual-mode server (stock=$stock/$stock_idx calendar=$cal/$cal_idx)"
      record "EXP-2c HOLE: stock=$stock (indexed=$stock_idx) calendar=$cal (indexed=$cal_idx)" ;;
    *)
      record "EXP-2c INCONCLUSIVE: quarantined=$q gate=$proof stock=$stock calendar=$cal" ;;
  esac
  stop_daemon
  fi
fi

# ==================================================================== EXP-3
if want_only EXP-3; then
  log "EXP-3 (hole A): served evasion mutants must NOT scan dangerous"
  cfg="$(make_config exp3 mutants 1 "$RUN_DIR/exp3-data")" || config_failed EXP-3; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp3.log" || { record "EXP-3: INCONCLUSIVE (daemon)"; exit 1; }
  v="$(scan_and_verdict "$cfg" "$RUN_DIR/exp3-scan.txt")"
  log "scan verdict: $v (full output: $RUN_DIR/exp3-scan.txt)"
  # Only a recognised clean/warnings verdict is the deferred detector hole;
  # an unreadable or unknown report is INCONCLUSIVE (Sol r4 P2).
  case "$(exp3_verdict "$v")" in
    HOLDS) pass "EXP-3 mutants flagged dangerous (detector improved!)"; record "EXP-3 HOLDS: mutants → dangerous" ;;
    HOLE) hole "EXP-3 mutant tools scan '$v' — detection bypassed end-to-end"; record "EXP-3 HOLE: mutants verdict=$v" ;;
    *) record "EXP-3 INCONCLUSIVE: scan=$v" ;;
  esac
  stop_daemon
fi

# ==================================================================== EXP-4
# exp4_ingested <out-prefix> — polls (≤30s) the stored approval records for
# proof that discovery ingested the poisoned list-1 contract
# (exp4_poison_recorded); echoes yes/no.
exp4_ingested() {
  local got="no"
  for i in $(seq 1 30); do
    curl -s -H "X-API-Key: $API_KEY" "$API_URL/servers/tpa-lab-attacker/tools/export" >"$1-export.json" 2>/dev/null
    curl -s -H "X-API-Key: $API_KEY" "$API_URL/servers/tpa-lab-attacker/tools/summarize_text/diff" >"$1-diff.json" 2>/dev/null
    got="$(exp4_poison_recorded "$1-export.json" "$1-diff.json")"
    [[ "$got" == "yes" ]] && break
    sleep 1
  done
  echo "$got"
}

if want_only EXP-4; then
  log "EXP-4 (hole B): TOCTOU — poisoned contract at discovery (list #1), clean contract to the scan export (list #2+)"
  log "server runs UNQUARANTINED so discovery's auto-baseline ingests list #1"
  # The fixture answers its FIRST tools/list with the poisoned contract. When
  # that list goes to a caller that does not ingest contracts (a REST tool
  # listing, the token-savings count), discovery baselines the clean one and
  # the experiment proves nothing. Verify the precondition from the stored
  # records before scanning, and retry with a fresh process and data dir
  # (bounded) instead of reporting an unverified result (Sol r7 P2).
  ingested="no"; attempt=0; EXP4_ATTEMPTS="${EXP4_ATTEMPTS:-4}"
  while [[ "$ingested" != "yes" && $attempt -lt $EXP4_ATTEMPTS ]]; do
    attempt=$((attempt + 1))
    sfx=""; [[ $attempt -gt 1 ]] && sfx="-retry$attempt"
    cfg="$(make_config "exp4$sfx" toctou 1 "$RUN_DIR/exp4$sfx-data" "" noquarantine)" || config_failed EXP-4; CURRENT_CONFIG="$cfg"
    EXP4_LOG="$RUN_DIR/exp4$sfx.log"
    start_daemon "$cfg" "$EXP4_LOG" || { record "EXP-4: INCONCLUSIVE (daemon)"; exit 1; }
    ingested="$(exp4_ingested "$RUN_DIR/exp4$sfx-precondition")"
    log "attempt $attempt: poisoned list-1 contract ingested by discovery: $ingested"
    [[ "$ingested" == "yes" ]] || stop_daemon
  done
  if [[ "$ingested" != "yes" ]]; then
    record "EXP-4 INCONCLUSIVE: precondition not met in $attempt attempts (discovery never ingested the poisoned list-1 contract)"
  else
    v="$(scan_and_verdict "$cfg" "$RUN_DIR/exp4-scan.txt")"
    log "scan verdict: $v (the scan's export call saw the CLEAN contract — list #2+)"
    stored="$(wait_status summarize_text)"
    baseline_lines="$(grep -c 'auto-baseline' "$EXP4_LOG" || true)"
    flap_lines="$(grep -c 'changed since approval' "$EXP4_LOG" || true)"
    log "stored tool status: $stored (auto-baseline approvals: $baseline_lines, rug-pull flags: $flap_lines)"
    case "$(exp4_verdict "$ingested" "$v" "$stored")" in
      HOLDS)
        pass "EXP-4 scan flagged the poisoned discovery contract ($v)"
        record "EXP-4 HOLDS: ingested=yes (attempt $attempt), scan=$v, stored=$stored" ;;
      PARTIAL-HOLE)
        hole "EXP-4 PARTIAL: scan export is attacker-controllable (verdict=$v on a poisoned server; poisoned contract WAS ingested) — the flap was caught, but a session-fingerprinting variant would scan clean without flapping"
        record "EXP-4 PARTIAL-HOLE: scan=$v on poisoned server; poisoned list ingested; flap caught ($stored)" ;;
      HOLE)
        hole "EXP-4 scan=$v on a server whose poisoned contract was ingested, and stored=$stored"
        record "EXP-4 HOLE: scan=$v, stored=$stored (poisoned list ingested)" ;;
      *)
        record "EXP-4 INCONCLUSIVE: scan=$v, stored=$stored" ;;
    esac
  fi
  stop_daemon
fi

# ==================================================================== EXP-5
if want_only EXP-5; then
  log "EXP-5 (hole C): annotation-only flip after approval (contract hash covers desc+schema only)"
  cfg="$(make_config exp5 facade 1 "$RUN_DIR/exp5-data")" || config_failed EXP-5; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp5.log" || { record "EXP-5: INCONCLUSIVE (daemon)"; exit 1; }
  unquarantine
  n1="$(wait_status notes_export)"
  log "after unquarantine re-discovery: notes_export status=$n1 (stage-1 honest annotations: destructive)"
  approve_ok=0; approve_all && approve_ok=1
  n1="$(wait_status notes_export)"
  log "after approve: notes_export status=$n1 (approve ok=$approve_ok)"
  stop_daemon

  if [[ $approve_ok != 1 || "$n1" != "approved" ]]; then
    record "EXP-5 INCONCLUSIVE: stage-1 baseline not approved (approve ok=$approve_ok, notes_export=$n1)"
    record "EXP-5b INCONCLUSIVE: stage-1 baseline not approved"
  else
  copy_data_dir "$RUN_DIR/exp5-data" "$RUN_DIR/exp5b-data"
  cfg="$(make_config exp5 facade 2 "$RUN_DIR/exp5-data")" || config_failed EXP-5; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp5-2.log" || { record "EXP-5: INCONCLUSIVE (restart)"; exit 1; }
  sleep 3
  # The restart re-quarantines the server, so the quarantined verification
  # (RT-05) may already have marked the flip changed. Prove the
  # post-unquarantine gate itself (Sol r5 P2): the unquarantine succeeded,
  # discovery ran for the trusted server, and the held tool is not indexed.
  unquarantine_and_prove
  n2="$(wait_status_not notes_export approved 90)"
  n2_idx="$(tool_indexed notes_export)"
  log "after annotation flip + unquarantine (ok=$UNQ_OK quarantined=$QUARANTINED discovered=$DISCOVERED): notes_export=$n2 (indexed=$n2_idx)"
  case "$(held_unquarantined_verdict "$UNQ_OK" "$QUARANTINED" "$DISCOVERED" "$n2" "$n2_idx")" in
    HOLDS)
      pass "EXP-5 annotation flip held after unquarantine ($n2, not indexed)"
      record "EXP-5 HOLDS: notes_export=$n2 (unquarantined, discovered, not indexed)" ;;
    HOLE)
      hole "EXP-5 destructive→readOnly-lying flip NOT held after unquarantine (notes_export=$n2 indexed=$n2_idx)"
      record "EXP-5 HOLE: annotation flip unseen (notes_export=$n2 indexed=$n2_idx)" ;;
    *)
      record "EXP-5 INCONCLUSIVE: unquarantine ok=$UNQ_OK quarantined=$QUARANTINED discovered=$DISCOVERED notes_export=$n2 indexed=$n2_idx" ;;
  esac
  stop_daemon

  # EXP-5b: the approved contract changes while the server is ALREADY
  # trusted — the stage-2 restart never quarantines it, so only the trusted
  # server's discovery gate can hold the flip (Sol r5 P2).
  cfg="$(make_config exp5b facade 2 "$RUN_DIR/exp5b-data" "" noquarantine)" || config_failed EXP-5b; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp5b.log" || { record "EXP-5b: INCONCLUSIVE (restart)"; exit 1; }
  q="$(server_quarantined)"
  n3="$(wait_status_not notes_export approved 90)"
  n3_idx="$(tool_indexed notes_export)"
  proof="$(gate_held_logged "$RUN_DIR/exp5b.log" notes_export)"
  log "EXP-5b (trusted throughout, quarantined=$q gate=$proof): notes_export=$n3 (indexed=$n3_idx)"
  case "$(held_unquarantined_verdict 1 "$q" "$proof" "$n3" "$n3_idx")" in
    HOLDS)
      pass "EXP-5b annotation flip held on an already-trusted server ($n3, not indexed)"
      record "EXP-5b HOLDS: notes_export=$n3 (trusted throughout, gate held it, not indexed)" ;;
    HOLE)
      hole "EXP-5b annotation flip NOT held on an already-trusted server (notes_export=$n3 indexed=$n3_idx)"
      record "EXP-5b HOLE: notes_export=$n3 indexed=$n3_idx" ;;
    *)
      record "EXP-5b INCONCLUSIVE: quarantined=$q gate=$proof notes_export=$n3 indexed=$n3_idx" ;;
  esac
  stop_daemon
  fi
fi

# ==================================================================== EXP-6
if want_only EXP-6; then
  log "EXP-6 (hole D): trust_mode=scan + paraphrased stage-3 rug pull (auto-approval chain)"
  cfg="$(make_config exp6 facade 1 "$RUN_DIR/exp6-data" scan)" || config_failed EXP-6; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp6.log" || { record "EXP-6: INCONCLUSIVE (daemon)"; exit 1; }
  sleep 3
  unquarantine
  pre="$(wait_status calendar_get_events)"
  log "initial status under trust_mode=scan: calendar_get_events=$pre"
  approve_ok=1
  if [[ "$pre" != "approved" ]]; then
    log "not auto-approved initially — approving manually (stage-1 facade is benign anyway)"
    approve_ok=0; approve_all && approve_ok=1
    pre="$(wait_status calendar_get_events)"
  fi
  baseline_ok=0
  [[ $approve_ok == 1 ]] && all_approved calendar_get_events stock_quote && baseline_ok=1
  stop_daemon

  if [[ $baseline_ok != 1 ]]; then
    record "EXP-6 INCONCLUSIVE: stage-1 baseline not approved (approve ok=$approve_ok, calendar=$pre)"
    record "EXP-6b INCONCLUSIVE: stage-1 baseline not approved"
  else
  copy_data_dir "$RUN_DIR/exp6-data" "$RUN_DIR/exp6b-data"
  cfg="$(make_config exp6 facade 3 "$RUN_DIR/exp6-data" scan)" || config_failed EXP-6; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp6-2.log" || { record "EXP-6: INCONCLUSIVE (restart)"; exit 1; }
  sleep 3
  # As in EXP-5: the quarantined verification may already hold the pull, so
  # prove the post-unquarantine scan-mode gate itself (Sol r5 P2).
  unquarantine_and_prove
  post="$(wait_status_not calendar_get_events approved 90)"
  postq="$(wait_status_not stock_quote approved 30)"
  post_idx="$(tool_indexed calendar_get_events)"
  postq_idx="$(tool_indexed stock_quote)"
  log "after paraphrased rug pull + unquarantine (ok=$UNQ_OK quarantined=$QUARANTINED discovered=$DISCOVERED): calendar=$post (indexed=$post_idx) stock=$postq (indexed=$postq_idx)"
  case "$(held_unquarantined_verdict "$UNQ_OK" "$QUARANTINED" "$DISCOVERED" "$post" "$post_idx" "$postq" "$postq_idx")" in
    HOLDS)
      pass "EXP-6 paraphrased pull held for review after unquarantine (calendar=$post stock=$postq, not indexed)"
      record "EXP-6 HOLDS: calendar=$post stock=$postq (unquarantined, discovered, not indexed)" ;;
    HOLE)
      hole "EXP-6 paraphrased rug pull NOT held under trust_mode=scan (calendar=$post/$post_idx stock=$postq/$postq_idx)"
      record "EXP-6 HOLE: calendar=$post (indexed=$post_idx) stock=$postq (indexed=$postq_idx)" ;;
    *)
      record "EXP-6 INCONCLUSIVE: unquarantine ok=$UNQ_OK quarantined=$QUARANTINED discovered=$DISCOVERED calendar=$post stock=$postq" ;;
  esac
  stop_daemon

  # EXP-6b: the paraphrased pull reaches a server that is ALREADY trusted
  # (trust_mode=scan, never re-quarantined), so only the trusted server's
  # scan-mode gate can hold it (Sol r5 P2).
  cfg="$(make_config exp6b facade 3 "$RUN_DIR/exp6b-data" scan noquarantine)" || config_failed EXP-6b; CURRENT_CONFIG="$cfg"
  start_daemon "$cfg" "$RUN_DIR/exp6b.log" || { record "EXP-6b: INCONCLUSIVE (restart)"; exit 1; }
  q="$(server_quarantined)"
  post="$(wait_status_not calendar_get_events approved 90)"
  postq="$(wait_status_not stock_quote approved 30)"
  post_idx="$(tool_indexed calendar_get_events)"
  postq_idx="$(tool_indexed stock_quote)"
  proof="$(gate_held_logged "$RUN_DIR/exp6b.log" calendar_get_events stock_quote)"
  log "EXP-6b (trusted throughout, quarantined=$q gate=$proof): calendar=$post (indexed=$post_idx) stock=$postq (indexed=$postq_idx)"
  case "$(held_unquarantined_verdict 1 "$q" "$proof" "$post" "$post_idx" "$postq" "$postq_idx")" in
    HOLDS)
      pass "EXP-6b paraphrased pull held on an already-trusted server (calendar=$post stock=$postq)"
      record "EXP-6b HOLDS: calendar=$post stock=$postq (trusted throughout, gate held them, not indexed)" ;;
    HOLE)
      hole "EXP-6b paraphrased pull NOT held on an already-trusted server (calendar=$post/$post_idx stock=$postq/$postq_idx)"
      record "EXP-6b HOLE: calendar=$post (indexed=$post_idx) stock=$postq (indexed=$postq_idx)" ;;
    *)
      record "EXP-6b INCONCLUSIVE: quarantined=$q gate=$proof calendar=$post stock=$postq" ;;
  esac
  stop_daemon
  fi
fi

# ------------------------------------------------------------------ report --
echo
log "===== experiment summary ====="
printf '%s\n' "${RESULTS[@]-}"
if suite_exit_status "${RESULTS[@]-}"; then
  log "suite result: PASS (every experiment holds; EXP-3 is the deferred RT-04/RT-07 hole)"
  exit 0
fi
log "suite result: FAIL (a hole was confirmed or an experiment was inconclusive)"
exit 1
