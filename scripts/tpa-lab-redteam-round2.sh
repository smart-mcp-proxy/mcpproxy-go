#!/usr/bin/env bash
# Round-2 red-team experiments (2026-10-20) against the hardened build
# (feat/tpa-attack-lab merged with latest origin/main; RT-01..RT-07 verified
# fixed). New attack surfaces:
#
#   R2-1  auto-baseline poisoning: an UNQUARANTINED server's first toolset is
#         auto-approved with no scan — even blatantly poisoned descriptions.
#   R2-2  unquarantine race (control): rug pull while quarantined, then call
#         the changed tool immediately after unquarantine, before the sweep.
#
# Same safety model as the round-1 suite: malicious upstreams run in Docker
# (distroless, network none, read-only, caps dropped).
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TEMPLATE="${TPA_LAB_TEMPLATE:-$PROJECT_DIR/tests/tpa-lab-config-docker.json}"
BINARY="${TPA_LAB_BINARY:-$PROJECT_DIR/mcpproxy}"
API_KEY="tpa-lab-key"
LISTEN="127.0.0.1:8081"
API_URL="http://$LISTEN/api/v1"
MCP_URL="http://$LISTEN/mcp"
IMAGE="${IMAGE:-tpa-attack-lab:lab}"
RUN_ROOT="${TPA_LAB_RUN_ROOT:-$PROJECT_DIR/test-data-tpa-lab-docker}"
ONLY="${ONLY:-}"
DAEMON_PID=""
# shellcheck source=lib/tpa-lab-verdicts.sh
source "$SCRIPT_DIR/lib/tpa-lab-verdicts.sh"
# Same guards as the round-1 runner (Sol 6.1 round-2 review, findings 4-7):
# inherited MCPPROXY_* overrides (MCPPROXY_DATA, MCPPROXY_LISTEN, …) would beat
# the generated config's data_dir and listen address for the daemon AND the
# CLI calls — drop them before any malicious fixture runs; start_daemon also
# pins --data-dir and --listen.
lab_scrub_mcpproxy_env

# One fresh run dir per invocation: approvals left by an earlier run would
# otherwise make R2-1 a NON-first-seen pass (pending/approved records already
# exist and never enter the baseline branch), so a broken gate could still
# score HOLDS. R2-2's two stages share one data dir INSIDE this invocation.
mkdir -p "$RUN_ROOT"
RUN_DIR="${RUN_DIR:-$(mktemp -d "$RUN_ROOT/rXXXX")}"
mkdir -p "$RUN_DIR"
ln -sfn "$RUN_DIR" "$RUN_ROOT/round2-latest"
log() { echo "[r2] $*"; }

if lab_port_busy 8081; then
  echo "ERROR: port 8081 is already in use; refusing to run (nothing was stopped)." >&2
  echo "Inspect the listener with 'lsof -nP -iTCP:8081 -sTCP:LISTEN' and stop it yourself if it is a stale lab daemon." >&2
  exit 1
fi

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  docker build -q -f "$PROJECT_DIR/tests/tpa-attack-lab/Dockerfile" -t "$IMAGE" "$PROJECT_DIR" >/dev/null || exit 1
  (cd "$PROJECT_DIR" && go build -o ./mcpproxy ./cmd/mcpproxy) || exit 1
fi

# make_cfg <name> <profile> <stage> <data_dir> [quarantined:yes|no]
make_cfg() {
  local cfg="$RUN_DIR/$1.json"
  python3 - "$TEMPLATE" "$cfg" "$2" "$3" "$4" "$5" <<'PY'
import json, sys
tpl, out, profile, stage, data_dir, quar = sys.argv[1:7]
cfg = json.load(open(tpl)); cfg["data_dir"] = data_dir
for s in cfg["mcpServers"]:
    if s["name"] == "tpa-lab-attacker":
        s["env"] = {"TPA_LAB_PROFILE": profile, "TPA_LAB_STAGE": stage}
        s["quarantined"] = (quar == "yes")
json.dump(cfg, open(out, "w"), indent=2)
PY
  [[ $? -eq 0 ]] || { echo "lab config generation failed for $cfg" >&2; return 1; }
  # Run the image this invocation built (IMAGE override), never the template's
  # default tag; refuses a config whose upstreams are not Docker-isolated.
  lab_config_use_image "$cfg" "$IMAGE" >&2 || { echo "refusing lab config $cfg" >&2; return 1; }
  echo "$cfg"
}

connected() {
  curl -sf -H "X-API-Key: $API_KEY" "$API_URL/servers" 2>/dev/null | python3 -c '
import json,sys
try:
    d=json.load(sys.stdin); s=d.get("data",{}).get("servers",[])
    print(sum(1 for x in s if x.get("status") in ("connected","Ready","ready")))
except Exception: print(0)' || echo 0
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
    kill -0 "$DAEMON_PID" 2>/dev/null || { echo "daemon exited"; tail -10 "$logf"; return 1; }
    sleep 1
  done
  local a b
  for i in $(seq 1 60); do a=$(connected); sleep 2; b=$(connected); [[ "$a" -ge 2 && "$b" -ge 2 ]] && return 0; done
  echo "WARNING: upstreams not stable"; return 0
}

stop_daemon() {
  [[ -n "$DAEMON_PID" ]] && kill "$DAEMON_PID" 2>/dev/null
  [[ -n "$DAEMON_PID" ]] && wait "$DAEMON_PID" 2>/dev/null
  DAEMON_PID=""; sleep 1
}
trap stop_daemon EXIT

tool_status() {
  LC_ALL=C "$BINARY" upstream inspect tpa-lab-attacker --config "$CURRENT_CFG" 2>/dev/null \
    | awk -v t="$1" '$1==t {print $2; found=1; exit} END {if (!found) print "not-found"}'
}

wait_status() {
  local s
  for i in $(seq 1 "${3:-30}"); do s="$(tool_status "$1")"; [[ "$s" != "not-found" ]] && { echo "$s"; return; }; sleep 1; done
  echo "not-found"
}

wait_status_not() {
  local tool="$1" avoid="$2" timeout="${3:-90}" s
  for i in $(seq 1 "$timeout"); do
    s="$(tool_status "$tool")"
    [[ "$s" != "not-found" && "$s" != "$avoid" ]] && { echo "$s"; return; }
    sleep 1
  done
  echo "$s"
}

# unquarantine fails on a non-2xx answer, so an experiment cannot report HOLDS
# while the server stayed quarantined (server-level quarantine would then be
# what blocked the call, not the protection under test).
unquarantine() { curl -sf -X POST -H "X-API-Key: $API_KEY" "$API_URL/servers/tpa-lab-attacker/unquarantine" >/dev/null; }

# mcp_call <tool-name> <args-json> — one-shot MCP session over streamable HTTP.
# args_json is passed as a JSON-ENCODED STRING (call_tool_read's contract).
mcp_call() {
  local tool="$1" args="$2" hdr payload
  hdr=$(mktemp); payload=$(mktemp)
  printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"r2","version":"1"},"capabilities":{}}}' >"$payload"
  curl -s -X POST "$MCP_URL" -H 'Content-Type: application/json' -D "$hdr" -d @"$payload" >/dev/null
  local sid; sid=$(grep -i "Mcp-Session-Id" "$hdr" | tr -d '\r\n' | sed 's/[^:]*: *//')
  printf '{"jsonrpc":"2.0","method":"notifications/initialized"}' >"$payload"
  [[ -n "$sid" ]] && curl -s -X POST "$MCP_URL" -H 'Content-Type: application/json' -H "Mcp-Session-Id: $sid" -d @"$payload" >/dev/null
  local args_json_string; args_json_string=$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$args")
  printf '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"call_tool_read","arguments":{"name":"tpa-lab-attacker:%s","args_json":%s}}}' "$tool" "$args_json_string" >"$payload"
  [[ -n "$sid" ]] && curl -s -X POST "$MCP_URL" -H 'Content-Type: application/json' -H "Mcp-Session-Id: $sid" -d @"$payload"
  rm -f "$hdr" "$payload"
}

RESULTS=()
record() { RESULTS+=("$1"); log "$1"; echo "----------------------------------------"; }

# ================================================================== R2-1
if [[ -z "$ONLY" || "$ONLY" == "R2-1" ]]; then
  log "R2-1: auto-baseline poisoning — UNQUARANTINED server, full 18-vector catalog, NO scan"
  CURRENT_CFG="$(make_cfg r2-1 all 1 "$RUN_DIR/r2-1-data" no)" || { record "R2-1 INCONCLUSIVE: lab config refused"; exit 1; }
  start_daemon "$CURRENT_CFG" "$RUN_DIR/r2-1.log" || { record "R2-1: INCONCLUSIVE (daemon)"; exit 1; }
  sleep 5
  sum_st="$(tool_status summarize_text)"   # poisoned override vector
  fin_st="$(tool_status finance_convert_currency)"  # poisoned exfil vector
  if [[ -z "$sum_st" ]]; then
    log "inspect output (for debugging the status parse):"
    "$BINARY" upstream inspect tpa-lab-attacker --config "$CURRENT_CFG" 2>&1 | head -12 | sed 's/^/    /'
    sum_st="$(tool_status summarize_text)"
  fi
  log "statuses with no operator action: summarize_text=$sum_st finance_convert_currency=$fin_st"
  approved_count=$("$BINARY" upstream inspect tpa-lab-attacker --config "$CURRENT_CFG" 2>/dev/null | grep -c "approved" || true)
  log "approved rows in inspect output: $approved_count"

  # Is the poisoned tool actually CALLABLE through the proxy?
  call_out="$(mcp_call summarize_text '{"text":"hello"}')"
  if echo "$call_out" | grep -q 'would_have_executed'; then
    call_result="CALLED — poisoned tool answered through the proxy"
  elif echo "$call_out" | grep -qiE "blocked|quarantin|pending|security"; then
    call_result="BLOCKED"
  else
    call_result="UNCLEAR: $(echo "$call_out" | head -c 160)"
  fi
  log "call summarize_text → $call_result"

  # Would a scan have flagged it? (operator-triggered, i.e. after the fact)
  "$BINARY" security scan tpa-lab-attacker --config "$CURRENT_CFG" >"$RUN_DIR/r2-1-scan.txt" 2>&1
  verdict="$(grep -oiE 'dangerous|warnings|clean' "$RUN_DIR/r2-1-scan.txt" | head -1)"
  log "post-hoc operator scan verdict: ${verdict:-unknown}"

  held_re='^(pending|changed|blocked)'
  if [[ "$sum_st" == "approved" && "$call_result" == CALLED* ]]; then
    record "R2-1 HOLE: unquarantined first-seen server auto-baselined the POISONED catalog (summarize_text=$sum_st, ${approved_count} approved rows) and the poisoned tool is callable; the operator-triggered scan says '$verdict' only AFTER the tools are live"
  elif [[ "$sum_st" == "approved" || "$fin_st" == "approved" ]]; then
    record "R2-1 HOLE: a poisoned first-seen tool was auto-baselined (summarize_text=$sum_st finance_convert_currency=$fin_st); call: $call_result; scan='$verdict'"
  elif [[ "$sum_st" =~ $held_re && "$fin_st" =~ $held_re && "$call_result" == "BLOCKED" ]]; then
    # Acceptance (round-2 report): BOTH poisoned vectors pending or worse AND the
    # call blocked. An empty/unparsed status or an unclear call never scores HOLDS.
    record "R2-1 HOLDS: poisoned tools not auto-approved (summarize_text=$sum_st finance_convert_currency=$fin_st, ${approved_count} approved rows for the benign controls) and the call is blocked"
  else
    record "R2-1 INCONCLUSIVE: summarize_text='$sum_st' finance_convert_currency='$fin_st' call='$call_result' scan='$verdict'"
  fi
  stop_daemon
fi

# ================================================================== R2-2
if [[ -z "$ONLY" || "$ONLY" == "R2-2" ]]; then
  log "R2-2 (control): rug pull while quarantined → immediate call after unquarantine (race window)"
  CURRENT_CFG="$(make_cfg r2-2 facade 1 "$RUN_DIR/r2-2-data" yes)" || { record "R2-2 INCONCLUSIVE: lab config refused"; exit 1; }
  start_daemon "$CURRENT_CFG" "$RUN_DIR/r2-2.log" || { record "R2-2: INCONCLUSIVE (daemon)"; exit 1; }
  unquarantine || { record "R2-2 INCONCLUSIVE: stage-1 unquarantine refused"; stop_daemon; exit 1; }
  s="$(wait_status calendar_get_events)"
  curl -sf -X POST -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' -d '{"approve_all":true}' \
    "$API_URL/servers/tpa-lab-attacker/tools/approve" >/dev/null \
    || { record "R2-2 INCONCLUSIVE: stage-1 facade approval refused"; stop_daemon; exit 1; }
  log "stage-1 facade approved (calendar=$s)"
  stop_daemon

  CURRENT_CFG="$(make_cfg r2-2 facade 2 "$RUN_DIR/r2-2-data" yes)" || { record "R2-2 INCONCLUSIVE: lab config refused"; exit 1; }
  start_daemon "$CURRENT_CFG" "$RUN_DIR/r2-2-2.log" || { record "R2-2: INCONCLUSIVE (restart)"; exit 1; }
  sleep 6
  during="$(tool_status calendar_get_events)"
  log "while still quarantined, armed contract state: calendar=$during (RT-05 verification)"

  unquarantine || { record "R2-2 INCONCLUSIVE: stage-2 unquarantine refused"; stop_daemon; exit 1; }
  # Call IMMEDIATELY — before the re-discovery sweep could have run.
  call_out="$(mcp_call calendar_get_events '{"date":"2026-10-20"}')"
  if echo "$call_out" | grep -qiE "blocked|changed|security|pending|not approved"; then
    call_result="BLOCKED"
  elif echo "$call_out" | grep -q '"vector"'; then
    call_result="CALLED"
  else
    call_result="UNCLEAR: $(echo "$call_out" | head -c 160)"
  fi
  after="$(wait_status_not calendar_get_events approved 60)"
  log "immediate call after unquarantine → $call_result; settled status: calendar=$after"

  if [[ "$call_result" == "BLOCKED" ]]; then
    record "R2-2 HOLDS: changed tool blocked even in the immediate post-unquarantine window (during-quarantine state: $during, settled: $after)"
  elif [[ "$call_result" == "CALLED" ]]; then
    record "R2-2 HOLE: armed tool CALLABLE immediately after unquarantine before the sweep (during=$during, settled=$after)"
  else
    record "R2-2 INCONCLUSIVE: $call_result (during=$during, settled=$after)"
  fi
  stop_daemon
fi

echo
log "===== round-2 summary ====="
printf '%s\n' "${RESULTS[@]-}"
# Fail closed like the round-1 suite: any HOLE, PARTIAL or INCONCLUSIVE line
# (or no experiment selected) is a non-zero exit.
if suite_exit_status "${RESULTS[@]-}"; then
  log "round-2 result: PASS (every finding holds)"
  exit 0
fi
log "round-2 result: FAIL (a hole was confirmed or an experiment was inconclusive)"
exit 1
