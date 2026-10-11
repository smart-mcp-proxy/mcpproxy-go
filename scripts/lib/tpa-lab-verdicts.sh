# shellcheck shell=bash
# Pure verdict and config helpers for the TPA attack-lab scripts
# (scripts/tpa-lab-redteam-experiments.sh, scripts/run-tpa-lab-docker.sh).
# Sourced, never executed. Kept free of daemon/Docker side effects so
# tests/tpa-attack-lab/scripts_test.go can exercise every predicate.

# is_pulled <status> — the change gate held the tool (changed and/or blocked).
is_pulled() { [[ "$1" == changed* || "$1" == *blocked* ]]; }

# exp2a_verdict <calendar_status> <stock_status> — EXP-2a (contract change
# while the server stays re-quarantined). HOLDS only when the gate held BOTH
# rug-pulled tools (changed/blocked); a stale "approved" on either is the
# hole; anything else (pending, not-found, unknown) proves nothing.
exp2a_verdict() {
  local cal="$1" stock="$2"
  if is_pulled "$cal" && is_pulled "$stock"; then
    echo HOLDS
  elif [[ "$cal" == "approved" || "$stock" == "approved" ]]; then
    echo HOLE
  else
    echo INCONCLUSIVE
  fi
}

# held_unquarantined_verdict <step_ok> <quarantined> <proof> <status> <indexed>
#                           [<status> <indexed>]...
# A rug pull is held on an UNQUARANTINED server (Sol r4/r5 P2). Records may
# already read "changed" from the quarantined verification that ran at
# restart (RT-05), so the statuses alone prove nothing about the gate that
# protects a trusted server: HOLDS needs the step that made the server
# trusted to have succeeded (step_ok=1: the unquarantine call, or a config
# that never quarantined it), the server reported not quarantined (false),
# proof that discovery/the gate ran for the trusted server (proof=yes), and
# every listed tool held (changed/blocked) AND absent from the index
# (indexed=no). On a trusted server a stale "approved" or an indexed tool is
# the hole; anything unproven is INCONCLUSIVE.
held_unquarantined_verdict() {
  local ok="$1" quarantined="$2" proof="$3" st idx all_held=1
  shift 3
  if [[ "$ok" != "1" || "$quarantined" != "false" || "$proof" != "yes" || $# -eq 0 || $(($# % 2)) -ne 0 ]]; then
    echo INCONCLUSIVE
    return
  fi
  local -a args=("$@")
  local i
  for ((i = 0; i < ${#args[@]}; i += 2)); do
    st="${args[i]}" idx="${args[i + 1]}"
    if [[ "$st" == "approved" || "$idx" == "yes" ]]; then
      echo HOLE
      return
    fi
    if ! is_pulled "$st" || [[ "$idx" != "no" ]]; then
      all_held=0
    fi
  done
  if [[ $all_held == 1 ]]; then
    echo HOLDS
  else
    echo INCONCLUSIVE
  fi
}

# exp2b_verdict <unquarantine_ok> <quarantined> <discovered> <cal> <stock>
#               <cal_indexed> <stock_indexed>
# EXP-2b (operator unquarantines the re-quarantined server):
# held_unquarantined_verdict with discovered=yes (an unchanged approved tool
# that was not indexed before the unquarantine reached the index) as proof.
exp2b_verdict() {
  held_unquarantined_verdict "$1" "$2" "$3" "$4" "$6" "$5" "$7"
}

# exp3_verdict <scan_verdict> — EXP-3 (served detector-evasion mutants).
# "dangerous" HOLDS; a recognised "clean"/"warnings" verdict is the deferred
# detector hole (RT-04/RT-07); anything else (scan-failed, stale-report, an
# unreadable/unknown report) proves nothing and is INCONCLUSIVE — never the
# allowed hole (Sol r4 P2).
exp3_verdict() {
  case "$1" in
    dangerous) echo HOLDS ;;
    clean | warnings) echo HOLE ;;
    *) echo INCONCLUSIVE ;;
  esac
}

# lab_config_data_dir <config.json> <base_dir> — prints the config's data_dir
# as an absolute path, resolving a relative one against base_dir (the
# directory the daemon and CLI are run from).
lab_config_data_dir() {
  python3 - "$1" "$2" <<'PYEOF'
import json, os, sys
cfg = json.load(open(sys.argv[1]))
d = cfg.get("data_dir") or ""
if not d:
    sys.exit("config has no data_dir")
print(os.path.normpath(os.path.join(sys.argv[2], os.path.expanduser(d))))
PYEOF
}

# suite_exit_status <result-line>... — 0 only when every recorded experiment
# HOLDS. A HOLE, PARTIAL-HOLE, "HOLE?" or INCONCLUSIVE result (a failed
# baseline approval, a failed scan, a confirmed bypass) fails the suite. The
# one allowed failure is the EXP-3 detector-evasion hole, deferred to
# RT-04/RT-07 (detect engine). No results at all also fails.
suite_exit_status() {
  local line ok=0 n=0
  for line in "$@"; do
    [[ -z "$line" ]] && continue
    n=$((n + 1))
    case "$line" in
      "EXP-3 HOLE"*) ;;                # deferred: RT-04/RT-07
      *HOLE* | *INCONCLUSIVE*) ok=1 ;;
      *HOLDS*) ;;
      *) ok=1 ;;                       # unrecognised result line
    esac
  done
  [[ $n -gt 0 ]] || ok=1
  return $ok
}

# lab_config_images <config.json> — prints the isolation image of every
# server in the config, one per line (a server without isolation prints
# "<none>").
lab_config_images() {
  python3 - "$1" <<'PY'
import json, sys
cfg = json.load(open(sys.argv[1]))
for s in cfg.get("mcpServers", []):
    print((s.get("isolation") or {}).get("image") or "<none>")
PY
}

# lab_config_check_isolation <config.json> — 0 only when the config has
# servers and every one of them is Docker-isolated (isolation.mode docker)
# with network_mode none; otherwise non-zero with the reason on stderr. The
# lab's malicious upstreams must never run bare or with network access, so
# both lab scripts refuse a config that fails it (Sol r5 P2).
lab_config_check_isolation() {
  python3 - "$1" <<'PY'
import json, sys
try:
    cfg = json.load(open(sys.argv[1]))
except Exception as e:
    sys.exit("lab config %s is unreadable: %s" % (sys.argv[1], e))
servers = cfg.get("mcpServers", [])
if not servers:
    sys.exit("lab config has no servers")
for s in servers:
    iso = s.get("isolation") or {}
    if iso.get("mode") != "docker" or iso.get("network_mode") != "none":
        sys.exit("server %s is not Docker-isolated with network_mode none" % s.get("name"))
PY
}

# lab_config_use_image <config.json> <image> — rewrites the isolation image of
# every server in place, so an IMAGE override runs the image that was built
# rather than a stale tpa-attack-lab:lab. Refuses (non-zero, file untouched)
# when any server is not Docker-isolated with network_mode none — the lab's
# malicious upstreams must never run bare.
lab_config_use_image() {
  lab_config_check_isolation "$1" || return 1
  python3 - "$1" "$2" <<'PY'
import json, sys
path, image = sys.argv[1], sys.argv[2]
cfg = json.load(open(path))
servers = cfg.get("mcpServers", [])
if not servers or not image:
    sys.exit("lab config has no servers or the image is empty")
for s in servers:
    s["isolation"]["image"] = image
json.dump(cfg, open(path, "w"), indent=2)
PY
}
