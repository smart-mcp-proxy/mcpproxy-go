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

# scan_job_id <scan-output-file> — the job id "mcpproxy security scan"
# printed for the scan it ran ("... (job scan-…)"), or empty.
scan_job_id() {
  sed -nE 's/.*[(, ]job (scan-[^) ]+)\).*/\1/p' "$1" | head -1
}

# scan_report_verdict <report.json> <job_id> — the verdict of the stored
# "security report -o json" document, bound to the scan job that produced
# it: dangerous|warnings|clean, "stale-report" when the report belongs to
# another job (or no job id is known), "unknown" when it is unreadable or
# carries no recognised verdict.
scan_report_verdict() {
  python3 - "$1" "${2:-}" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception:
    print("unknown"); raise SystemExit
data = d.get("data") if isinstance(d, dict) else None
if not sys.argv[2] or not isinstance(data, dict) or data.get("job_id") != sys.argv[2]:
    print("stale-report"); raise SystemExit
if data.get("verdict") in ("dangerous", "warnings", "clean"):
    print(data["verdict"]); raise SystemExit
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
walk(data)
for want in ("dangerous", "warnings", "clean"):
    if want in found:
        print(want); raise SystemExit
print("unknown")
PY
}

# baseline_scan_verdict_ok <scan-output-file> <report.json> — 0 only when
# the baseline runner's scan produced a job id and that job's stored report
# says "dangerous" (the full attack catalog must never scan clean,
# warnings, unknown or from another job's report — Sol r7 P2). Prints the
# verdict it found.
baseline_scan_verdict_ok() {
  local job v
  job="$(scan_job_id "$1")"
  v="$(scan_report_verdict "$2" "$job")"
  echo "$v"
  [[ "$v" == "dangerous" ]]
}

# EXP4_POISON_MARKER is a phrase only the toctou fixture's POISONED
# summarize_text contract carries (tests/tpa-attack-lab/profiles.go).
EXP4_POISON_MARKER="ignore all previous instructions"

# exp4_poison_recorded <tools-export.json> <summarize_text-diff.json> —
# "yes" when the proxy's stored approval records prove discovery ingested
# the poisoned summarize_text contract: the stored export's current
# description carries the marker (still baselined), or the change diff's
# PREVIOUS description does (baselined, then the flap to clean was caught).
# Anything else — the first tools/list went to a caller that does not ingest
# contracts, so discovery baselined the clean one — is "no" (Sol r7 P2).
exp4_poison_recorded() {
  python3 - "$1" "$2" "$EXP4_POISON_MARKER" <<'PY'
import json, sys
export_path, diff_path, marker = sys.argv[1], sys.argv[2], sys.argv[3].lower()
def load(path):
    try:
        with open(path) as f:
            return json.load(f).get("data") or {}
    except Exception:
        return {}
export = load(export_path)
for tool in export.get("tools") or []:
    if tool.get("tool_name") == "summarize_text" and marker in (tool.get("description") or "").lower():
        print("yes"); sys.exit()
diff = load(diff_path)
if diff.get("tool_name") == "summarize_text" and marker in (diff.get("previous_description") or "").lower():
    print("yes"); sys.exit()
print("no")
PY
}

# exp4_verdict <poison_ingested> <scan_verdict> <stored_status> — EXP-4
# (TOCTOU: poisoned contract to discovery, clean contract to the scan's own
# tools/list). The experiment only means something when discovery verifiably
# ingested the poisoned contract (exp4_poison_recorded = yes); otherwise it
# is INCONCLUSIVE, never a pass (Sol r7 P2). With the precondition met, a
# dangerous scan HOLDS (RT-06: the scan analyses what was ingested); a
# non-dangerous scan is a HOLE, PARTIAL-HOLE when the flap itself was held
# (changed). A failed, stale or unreadable scan is INCONCLUSIVE.
exp4_verdict() {
  local ingested="$1" scan="$2" stored="$3"
  if [[ "$ingested" != "yes" ]]; then
    echo INCONCLUSIVE; return
  fi
  case "$scan" in
    dangerous) echo HOLDS ;;
    clean | warnings)
      if is_pulled "$stored"; then echo PARTIAL-HOLE; else echo HOLE; fi ;;
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
# servers and every one of them is a stdio command that mcpproxy wraps in
# Docker (isolation.mode docker, not disabled) with network_mode none, and
# every extra docker run argument (per-server and global) is one of the
# lab's hardening flags, and the effective (global + per-server) arguments
# include --read-only, --cap-drop=ALL and --security-opt no-new-privileges
# (Sol r7 P2); otherwise non-zero with the reason on stderr. The
# lab's malicious upstreams must never run bare, with network access, or
# with a confinement escape, so both lab scripts refuse a config that fails
# it (Sol r5 P2). Sol r6 P1: a server whose own command is docker is NOT
# wrapped (ResolveIsolation's already-docker gate), so "docker run
# --network=host" passed the old mode/network check unconfined; and extra
# args such as --privileged, --network=host or -v /:/host are appended to
# the wrapping docker run verbatim — the allowlist below refuses them.
lab_config_check_isolation() {
  python3 - "$1" <<'PY'
import json, os, sys
try:
    cfg = json.load(open(sys.argv[1]))
except Exception as e:
    sys.exit("lab config %s is unreadable: %s" % (sys.argv[1], e))

# Hardening flags the lab may pass to docker run, as (flag, value-check).
# A flag listed with a value check takes a value, either as --flag=value or
# as the next argument. Everything else is refused.
VALUE_FLAGS = {
    "--security-opt": lambda v: v in ("no-new-privileges", "no-new-privileges:true", "no-new-privileges=true"),
    "--cap-drop": lambda v: v.upper() == "ALL",
    "--tmpfs": lambda v: v == "/tmp" or v.startswith("/tmp:"),
    "--memory": lambda v: bool(v) and v.replace(".", "").rstrip("bkmgBKMG").isdigit(),
    "--cpus": lambda v: bool(v) and v.replace(".", "").isdigit(),
    "--pids-limit": lambda v: v.isdigit(),
}
BARE_FLAGS = {"--read-only"}

# Protections every wrapped docker run MUST carry (Sol r7 P2): the launcher
# only appends extra_args (internal/upstream/core/isolation.go), so a config
# with extra_args missing or [] would run the attacker with a writable
# rootfs, default capabilities and setuid escalation available.
REQUIRED = {
    "read-only": "--read-only",
    "cap-drop-all": "--cap-drop=ALL",
    "no-new-privileges": "--security-opt no-new-privileges",
}

def check_args(args, where):
    """Refuses any non-allowlisted flag; returns the REQUIRED keys present."""
    present = set()
    if args is None:
        return present
    if not isinstance(args, list):
        sys.exit("%s: extra_args is not a list" % where)
    i = 0
    while i < len(args):
        a = args[i]
        if not isinstance(a, str):
            sys.exit("%s: extra_args holds a non-string %r" % (where, a))
        if a in BARE_FLAGS:
            if a == "--read-only":
                present.add("read-only")
            i += 1
            continue
        flag, eq, val = a.partition("=")
        if flag in VALUE_FLAGS:
            if not eq:
                i += 1
                if i >= len(args):
                    sys.exit("%s: extra_args flag %s has no value" % (where, flag))
                val = args[i]
            if not isinstance(val, str) or not VALUE_FLAGS[flag](val):
                sys.exit("%s: extra_args %s %r is not an allowed hardening value" % (where, flag, val))
            if flag == "--cap-drop":
                present.add("cap-drop-all")
            elif flag == "--security-opt":
                present.add("no-new-privileges")
            i += 1
            continue
        sys.exit("%s: extra_args %r is not an allowed hardening flag (unsafe docker run arguments could escape confinement)" % (where, a))
    return present

def is_docker_command(cmd):
    # Mirrors config.IsDockerCommand: such a command is never wrapped.
    return os.path.basename(cmd) == "docker" or "docker" in cmd or os.path.basename(cmd) == "podman"

glob = cfg.get("docker_isolation") or {}
global_present = check_args(glob.get("extra_args"), "docker_isolation")

servers = cfg.get("mcpServers", [])
if not servers:
    sys.exit("lab config has no servers")
for s in servers:
    name = s.get("name")
    iso = s.get("isolation") or {}
    if iso.get("mode") != "docker" or iso.get("network_mode") != "none":
        sys.exit("server %s is not Docker-isolated with network_mode none" % name)
    if iso.get("enabled") is False:
        sys.exit("server %s disables isolation (isolation.enabled false)" % name)
    if s.get("protocol", "stdio") != "stdio" or s.get("url"):
        sys.exit("server %s is not a stdio server mcpproxy can wrap in Docker" % name)
    cmd = s.get("command") or ""
    if not cmd:
        sys.exit("server %s has no command" % name)
    if is_docker_command(cmd):
        sys.exit("server %s is not Docker-isolated: its own command %r runs docker, which mcpproxy does not wrap" % (name, cmd))
    present = global_present | check_args(iso.get("extra_args"), "server %s" % name)
    missing = [REQUIRED[k] for k in sorted(REQUIRED) if k not in present]
    if missing:
        sys.exit("server %s is not hardened: its docker run lacks %s" % (name, ", ".join(missing)))
PY
}

# lab_config_require <config.json> — 0 only for a non-empty path to an
# existing lab config that sets data_dir and passes
# lab_config_check_isolation. Every daemon/CLI invocation of the red-team
# suite calls it first: an empty --config makes mcpproxy fall back to the
# user's normal config discovery (Sol r6 P1).
lab_config_require() {
  if [[ -z "${1:-}" ]]; then
    echo "lab config path is empty" >&2; return 1
  fi
  if [[ ! -f "$1" ]]; then
    echo "lab config $1 does not exist" >&2; return 1
  fi
  python3 - "$1" <<'PY' || return 1
import json, sys
try:
    d = json.load(open(sys.argv[1])).get("data_dir")
except Exception as e:
    sys.exit("lab config %s is unreadable: %s" % (sys.argv[1], e))
if not isinstance(d, str) or not d.strip():
    sys.exit("lab config %s has no data_dir" % sys.argv[1])
PY
  lab_config_check_isolation "$1"
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

# lab_scrub_mcpproxy_env — unsets every inherited MCPPROXY_* variable and
# prints the names it removed (one per line, to stderr). mcpproxy applies
# MCPPROXY_DATA, MCPPROXY_LISTEN, MCPPROXY_API_KEY and friends over the
# config file (internal/config/loader.go), so an inherited override would
# move the lab daemon onto the real ~/.mcpproxy database, a non-loopback
# listener or another API key, bypassing the checked config (Sol r8 P1).
# Both lab scripts call it before any preflight; variables the scripts need
# (MCPPROXY_TRAY_ENDPOINT) are set again afterwards.
lab_scrub_mcpproxy_env() {
  local v
  while IFS= read -r v; do
    if [[ "$v" == MCPPROXY_* ]]; then
      unset "$v"
      echo "[tpa-lab] ignoring inherited $v" >&2
    fi
  done < <(compgen -e)
  return 0
}
