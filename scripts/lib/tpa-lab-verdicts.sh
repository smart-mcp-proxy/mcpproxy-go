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

# exp2b_verdict <unquarantine_ok> <quarantined> <discovered> <cal> <stock>
#               <cal_indexed> <stock_indexed>
# EXP-2b (operator unquarantines the re-quarantined server). The EXP-2a
# records already read "changed", so the statuses alone prove nothing about
# the post-unquarantine flow (Sol r4 P2): HOLDS needs a successful
# unquarantine (1), the server reported not quarantined (false), proof that
# post-unquarantine discovery ran (discovered=yes: an unchanged approved tool
# reached the index), both rug-pulled tools held AND absent from the index
# (indexed=no). A held tool that is indexed, or a stale "approved", is the
# hole; anything unproven is INCONCLUSIVE.
exp2b_verdict() {
  local unq="$1" quarantined="$2" discovered="$3" cal="$4" stock="$5" cal_idx="$6" stock_idx="$7"
  if [[ "$unq" != "1" || "$quarantined" != "false" || "$discovered" != "yes" ]]; then
    echo INCONCLUSIVE
  elif [[ "$cal" == "approved" || "$stock" == "approved" || "$cal_idx" == "yes" || "$stock_idx" == "yes" ]]; then
    echo HOLE
  elif is_pulled "$cal" && is_pulled "$stock" && [[ "$cal_idx" == "no" && "$stock_idx" == "no" ]]; then
    echo HOLDS
  else
    echo INCONCLUSIVE
  fi
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

# lab_config_use_image <config.json> <image> — rewrites the isolation image of
# every server in place, so an IMAGE override runs the image that was built
# rather than a stale tpa-attack-lab:lab. Refuses (non-zero, file untouched)
# when any server is not Docker-isolated with network_mode none — the lab's
# malicious upstreams must never run bare.
lab_config_use_image() {
  python3 - "$1" "$2" <<'PY'
import json, sys
path, image = sys.argv[1], sys.argv[2]
cfg = json.load(open(path))
servers = cfg.get("mcpServers", [])
if not servers or not image:
    sys.exit("lab config has no servers or the image is empty")
for s in servers:
    iso = s.get("isolation") or {}
    if iso.get("mode") != "docker" or iso.get("network_mode") != "none":
        sys.exit("server %s is not Docker-isolated with network_mode none" % s.get("name"))
for s in servers:
    s["isolation"]["image"] = image
json.dump(cfg, open(path, "w"), indent=2)
PY
}
