#!/usr/bin/env bash
#
# Spec 125 QA control: runs the Skills tab browser sweep
# (e2e/web-ui-sweep/skills-tab.spec.ts) end to end against a throwaway core and
# cmd/skillsfixture, and writes a machine-readable results file. See
# docs/development/web-ui-verification.md.
#
# It starts, and on exit stops, only what it started:
#   * cmd/skillsfixture on a free loopback port;
#   * a core on a free high loopback port with a scratch data dir (never
#     ~/.mcpproxy: the core and the fixture start with a clean environment,
#     env -i with only PATH, a scratch HOME and a scratch TMPDIR, so no
#     MCPPROXY_* of the operator's reaches them; --data-dir names the scratch
#     dir, and a data dir under $HOME/.mcpproxy is refused), enable_skills on,
#     a generated throwaway API
#     key, and the fixture as a trust_mode: manual streamable-http upstream;
# then approves the server, runs the spec, and reads the core's own skill
# records back after the approve, block and unblock steps.
#
# Used both ways:
#   * by hand:             ./scripts/run-skills-tab-sweep.sh
#   * by the release gate: .github/workflows/release-qa-gate.yml `web-ui-sweep`
#     job (advisory), with MCPPROXY_BINARY_PATH / SKILLS_FIXTURE_BINARY pointing
#     at the candidate binaries it downloaded.

set -euo pipefail

# No xtrace, not even with DEBUG=1: a traced line would print the throwaway API
# key (its assignment, the X-API-Key header, the core's environment, the
# redaction call). DEBUG=1 prints progress lines instead (debug below), none of
# which carries a secret; --self-test checks a DEBUG=1 run's stderr for the key.
debug() {
  if [[ "${DEBUG:-}" == "1" ]]; then echo "debug: $*" >&2; fi
  return 0
}

usage() {
  cat <<EOF
Usage: ${0##*/} [--self-test-first] [--out DIR] [--report-html FILE [--no-screenshots]]
       ${0##*/} --self-test

Options:
  --out DIR           where the results land (default: <repo>/tmp/skills-sweep)
  --report-html FILE  also write a self-contained HTML report (one section per
                      scenario, with its screenshots) to FILE
  --no-screenshots    write the --report-html report without embedded
                      screenshots (the copy committed under specs/; the
                      screenshots stay in --out/screenshots for artifacts)
  --self-test         no browser: prove that an ambient MCPPROXY_DATA_DIR /
                      MCPPROXY_DATA, MCPPROXY_AUDIT_LOG_ENABLED/PATH,
                      MCPPROXY_TPA_BUNDLE_PATH and MCPPROXY_CERTS_DIR never
                      reach the core (it starts one with all of them set to
                      decoys and checks the database lands in the scratch dir
                      only and no decoy is touched), that a data dir under
                      \$HOME/.mcpproxy is refused, that the recorded PIDs
                      are the core and the fixture themselves and cleanup
                      leaves no process or listening port of the run alive
                      (and nothing else touched), that the results writer
                      records the first failed attempt and redacts the API key
                      and local paths, that printed paths are relative,
                      placeholders or <path>/basename, and that neither a
                      passing DEBUG=1 run nor a run whose fixture or core
                      fails to start, whose Playwright fails (a stand-in
                      PW_CHROMIUM outside every known prefix) or whose build
                      fails prints the API key or an absolute local path
  --self-test-first   run the --self-test checks, then the sweep (the release
                      gate does this, so both land in one advisory entry)
  -h, --help          print this message

Environment:
  MCPPROXY_BINARY_PATH   core binary with the embedded frontend (default: build
                         the frontend and the core from this checkout)
  SKILLS_FIXTURE_BINARY  cmd/skillsfixture binary (default: go build it)
  SWEEP_SERVER_NAME      upstream name (default: skillsfx)
  PW_CHROMIUM            optional Chromium/Chrome binary; skips the browser
                         download (offline machines)

Output (in --out):
  skills-sweep-results.json  checked_commit (git rev-parse HEAD) and
                             working_tree_dirty (a dirty tree is labelled
                             pre-commit evidence), fixture command (basename +
                             args) and port, each scenario's pass/fail (the
                             first failed attempt's error), and GET /skills read
                             back after the approve, block and unblock steps.
                             The API key is replaced by *** and local paths by
                             <repo>/<out>/<scratch>/~ in every text file written
  screenshots/  state/  playwright-results.json  core.log  fixture.log
Exit status is non-zero when any scenario failed or a state is missing.
EOF
}

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
OUT="$REPO_ROOT/tmp/skills-sweep"
REPORT_HTML=""
SCREENSHOTS=1
SELF_TEST=0
SELF_TEST_FIRST=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out) OUT="${2:?--out needs a directory}"; shift 2 ;;
    --report-html) REPORT_HTML="${2:?--report-html needs a file}"; shift 2 ;;
    --no-screenshots) SCREENSHOTS=0; shift ;;
    --self-test) SELF_TEST=1; shift ;;
    --self-test-first) SELF_TEST_FIRST=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

required() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}
required curl
required node
required npm
required git

if [[ $SCREENSHOTS -eq 0 && -z "$REPORT_HTML" ]]; then
  echo "--no-screenshots only applies to --report-html" >&2
  exit 2
fi

SERVER="${SWEEP_SERVER_NAME:-skillsfx}"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/skills-sweep.XXXXXX")
WORK=$(cd "$WORK" && pwd -P)
DATA_DIR="$WORK/data"
CORE_HOME="$WORK/home"
CONFIG_PATH="$WORK/mcp_config.json"
CORE_LOG="$WORK/core.log"
FIXTURE_LOG="$WORK/fixture.log"
mkdir -p "$DATA_DIR" "$CORE_HOME"
# The operator's real home, captured before anything is started: the core runs
# with HOME=$CORE_HOME, and the guard below compares against this one.
OPERATOR_HOME="${HOME:-}"

# Throwaway, not guessable: the key guards the core's whole admin API (see
# scripts/run-web-smoke.sh for why an ambient MCPPROXY_API_KEY is never used).
API_KEY="skills-sweep-$(node -e 'process.stdout.write(require("crypto").randomBytes(24).toString("hex"))')"
if [[ ${#API_KEY} -lt 32 ]]; then
  echo "failed to generate a random API key" >&2
  exit 1
fi

# Self-test hook: a child run of --self-test hands its key and its scratch dir
# to the parent (a scratch file of the parent's), which checks the child's
# output for both.
if [[ -n "${SWEEP_SELF_TEST_CHILD:-}" ]]; then printf '%s\n%s\n' "$API_KEY" "$WORK" >"$SWEEP_SELF_TEST_CHILD"; fi

# Every text file this script leaves behind goes through redact_file, and every
# message it prints that can carry a path or a log through redact_stream: the
# API key becomes *** and the local paths become placeholders. OUT_REAL is set
# once OUT is known.
OUT_REAL=""
# Paths the run was configured with (PW_CHROMIUM, the binaries, the browser
# dir), newline-separated: each is replaced as a whole by <path>/basename even
# when it holds a space, wherever it is printed. Added to by redact_extra_add.
REDACT_EXTRA=""
redact_extra_add() {
  local p
  for p in "$@"; do [[ "$p" == /* ]] && REDACT_EXTRA+="$p"$'\n'; done
  return 0
}
redact_extra_add "${PW_CHROMIUM:-}" "${MCPPROXY_BINARY_PATH:-}" "${SKILLS_FIXTURE_BINARY:-}" "${PLAYWRIGHT_BROWSERS_PATH:-}"
# shellcheck disable=SC2016 # JavaScript, expanded by node
REDACT_JS='
const fs = require("fs"), f = process.argv[1], e = process.env
let t = f ? fs.readFileSync(f, "utf8") : fs.readFileSync(0, "utf8")
const key = e.SWEEP_REDACT_KEY
if (key) for (const k of [key, encodeURIComponent(key)]) t = t.split(k).join("***")
const base = p => p.split("/").filter(Boolean).pop() || ""
const paths = [[e.SWEEP_REDACT_OUT, "<out>"], [e.SWEEP_REDACT_WORK, "<scratch>"], [e.SWEEP_REDACT_REPO, "<repo>"], [e.SWEEP_REDACT_HOME, "~"]]
  .filter(([p]) => p && p.length > 1).sort((a, b) => b[0].length - a[0].length)
// A configured path outside the known prefixes, as a whole (it may hold a space).
const known = paths.map(([p]) => p)
const extra = (e.SWEEP_REDACT_EXTRA || "").split("\n")
  .filter(p => p.length > 1 && p.startsWith("/") && !known.some(k => p === k || p.startsWith(k + "/")))
  .sort((a, b) => b.length - a.length)
for (const p of extra) t = t.split(p).join("<path>/" + base(p))
// macOS tools can print the same temporary directory with or without /private.
for (const [p, r] of paths) {
  t = t.split(p).join(r)
  if (process.platform === "darwin" && /^\/private\/(tmp|var)\//.test(p)) {
    t = t.split(p.slice("/private".length)).join(r)
  }
}
// Any other absolute filesystem path, whatever its root (/data, /work, /opt,
// ...), is reduced to <path>/basename: a path that starts the text or follows
// a space, a quote, a bracket, "=", ",", ":", ";", an ANSI colour code or a
// JSON escape (\n, \t, \u001b[..m). A URL path follows its host, so it is kept
// (http://127.0.0.1:1/api/...), and so is a bare route of the core (/api/...,
// /mcp, /ui, /events) and a placeholder (<repo>/e2e/...).
const pre = "(^|[\\s\"\x27`(\\[{=,:;]|\x1b\\[[0-9;]*m|\\\\u001b\\[[0-9;]*m|\\\\[nrt\"])"
const seg = "[^\\s\"\x27`()\\[\\]{}<>,:;\\\\/\x1b]+"
const route = "(?!(?:api|mcp|ui|events|healthz|readyz)(?:[/?#\\s\"\x27`)\\],:;\\\\]|$))"
const abs = new RegExp(pre + "(/" + route + seg + "(?:/" + seg + ")*/?)", "gm")
t = t.replace(abs, (m, p1, p) => p1 + "<path>/" + base(p))
if (f) fs.writeFileSync(f, t); else process.stdout.write(t)'
redact_node() {
  SWEEP_REDACT_KEY="$API_KEY" SWEEP_REDACT_OUT="$OUT_REAL" SWEEP_REDACT_REPO="$REPO_ROOT" \
  SWEEP_REDACT_WORK="$WORK" SWEEP_REDACT_HOME="$OPERATOR_HOME" SWEEP_REDACT_EXTRA="$REDACT_EXTRA" node -e "$REDACT_JS" "$@"
}
redact_file() {
  [[ -f "$1" ]] || return 0
  redact_node "$1"
}
# Copies stdin to stdout, redacted.
redact_stream() { redact_node; }
# A path as printed: relative to the repo root when it is inside it, else with
# the placeholders of redact_stream, and any other absolute path (outside every
# known prefix, e.g. PW_CHROMIUM=/usr/bin/chromium) as <path>/basename.
say_path() {
  local p="$1" pre
  case "$p" in
    "$REPO_ROOT"/*) printf '%s' "${p#"$REPO_ROOT"/}"; return 0 ;;
  esac
  if [[ "$p" == /* ]]; then
    for pre in "$OUT_REAL" "$WORK" "$OPERATOR_HOME"; do
      if [[ ${#pre} -gt 1 && "$p" == "$pre"/* ]]; then printf '%s' "$p" | redact_stream; return 0; fi
    done
    printf '<path>/%s' "$(basename "$p")"
    return 0
  fi
  printf '%s' "$p" | redact_stream
}

# run_logged NAME CMD...: runs CMD with its stdout and stderr captured into the
# scratch file $WORK/logs/NAME.log, then prints only a redacted copy of it (the
# tools' own output names the repo, the home, a browser download dir or the
# browser, and Playwright's launch errors carry the browser's environment with
# the API key). Returns CMD's exit status. Call it as `run_logged ... || status=$?`
# (or as a plain command where a failure should end the run under set -e).
run_logged() {
  local name="$1" log status=0
  shift
  mkdir -p "$WORK/logs"
  log="$WORK/logs/$name.log"
  "$@" >"$log" 2>&1 || status=$?
  redact_stream <"$log"
  return "$status"
}

# Prints $1 and every process descended from it (children first found by
# pgrep -P, recursively).
process_tree() {
  local child
  [[ -n "$1" ]] || return 0
  echo "$1"
  for child in $(pgrep -P "$1" 2>/dev/null || true); do process_tree "$child"; done
}

# Stops the core and the fixture this run started (CORE_PID, FIXTURE_PID) and
# every process descended from them, and nothing else: the tree is read before
# the first signal (a child of a stopped server is re-parented and would no
# longer be found), SIGTERM goes to the whole tree, and whatever of it is still
# alive after 15 s (the core's graceful shutdown) gets SIGKILL.
stop_started() {
  local tree pid attempt alive
  tree="$(process_tree "${CORE_PID:-}") $(process_tree "${FIXTURE_PID:-}")"
  # The group's stderr also takes the shell's own "Terminated" job notices.
  {
    for pid in $tree; do kill "$pid" || true; done
    for attempt in $(seq 1 150); do
      alive=""
      for pid in $tree; do kill -0 "$pid" && alive="$alive $pid"; done
      [[ -z "$alive" ]] && break
      sleep 0.1
    done
    for pid in $alive; do kill -9 "$pid" || true; done
    for pid in "${CORE_PID:-}" "${FIXTURE_PID:-}"; do
      [[ -n "$pid" ]] && { wait "$pid" || true; }
    done
  } >/dev/null 2>&1
  CORE_PID=""
  FIXTURE_PID=""
}

cleanup() {
  stop_started
  if [[ -n "$OUT_REAL" ]]; then
    cp "$CORE_LOG" "$OUT_REAL/core.log" 2>/dev/null && redact_file "$OUT_REAL/core.log"
    cp "$FIXTURE_LOG" "$OUT_REAL/fixture.log" 2>/dev/null && redact_file "$OUT_REAL/fixture.log"
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

# Refuses a data dir that is, or is inside, the operator's ~/.mcpproxy (by the
# path as written and by its physical path, so a symlink does not get past it).
data_dir_allowed() {
  local dir="$1" real="$1" home_real="" bad
  [[ -n "$OPERATOR_HOME" ]] || return 0
  [[ -d "$dir" ]] && real=$(cd "$dir" && pwd -P)
  [[ -d "$OPERATOR_HOME" ]] && home_real=$(cd "$OPERATOR_HOME" && pwd -P)
  for bad in "$OPERATOR_HOME/.mcpproxy" ${home_real:+"$home_real/.mcpproxy"}; do
    case "$dir/" in "$bad/"*) return 1 ;; esac
    case "$real/" in "$bad/"*) return 1 ;; esac
  done
  return 0
}

# Writes the throwaway config. Every value is generated here (ports, a temp
# path, a hex key), so the heredoc cannot be broken by quoting; the temp path
# is checked to be sure. $1: listen address, $2: the fixture's /mcp URL or "".
write_config() {
  local listen="$1" fixture_mcp="$2" servers="[]"
  case "$DATA_DIR" in
    *'"'* | *\\* | *$'\n'*) echo "scratch dir path cannot be embedded in JSON: $DATA_DIR" >&2; exit 1 ;;
  esac
  if [[ -n "$fixture_mcp" ]]; then
    servers=$(printf '[\n    {\n      "name": "%s",\n      "url": "%s",\n      "protocol": "streamable-http",\n      "enabled": true,\n      "trust_mode": "manual"\n    }\n  ]' "$SERVER" "$fixture_mcp")
  fi
  cat >"$CONFIG_PATH" <<JSON
{
  "listen": "${listen}",
  "data_dir": "${DATA_DIR}",
  "api_key": "${API_KEY}",
  "enable_tray": false,
  "enable_socket": false,
  "enable_web_ui": true,
  "enable_skills": true,
  "check_server_repo": false,
  "telemetry": { "enabled": false },
  "logging": { "level": "info", "enable_file": false, "enable_console": true },
  "mcpServers": ${servers}
}
JSON
}

# The only environment the core and the fixture get (env -i): PATH, the scratch
# HOME and a scratch TMPDIR. Nothing else of the operator's environment is
# inherited, no MCPPROXY_* at all: MCPPROXY_DATA_DIR / MCPPROXY_DATA would
# override the config's data_dir, MCPPROXY_AUDIT_LOG_ENABLED/PATH would audit
# into the operator's file, and more MCPPROXY_* variables name paths or change
# behaviour (internal/config/loader.go). Every variable the core needs is set
# explicitly by start_core.
CORE_TMP="$WORK/tmp"
mkdir -p "$CORE_TMP"
# An array, not a function: a backgrounded function runs in a subshell, so $!
# would be that shell's PID and stopping it would orphan the server. With the
# array, "${CLEAN_ENV[@]}" cmd ... & forks once and execs env, which execs cmd:
# $! is the server's own PID.
CLEAN_ENV=(env -i "PATH=${PATH:-/usr/bin:/bin}" "HOME=$CORE_HOME" "TMPDIR=$CORE_TMP")

# Starts the core detached from the operator's data and environment:
# CLEAN_ENV above, --data-dir (which wins over the file and the environment)
# names the scratch dir, and HOME is a scratch home, so a default path can never
# resolve to the operator's ~/.mcpproxy. $1: listen.
start_core() {
  local listen="$1"
  if ! data_dir_allowed "$DATA_DIR"; then
    echo "refusing to start: data dir $(say_path "$DATA_DIR") is under ~/.mcpproxy" >&2
    exit 1
  fi
  debug "starting the core on $listen (data dir: the scratch dir)"
  "${CLEAN_ENV[@]}" HEADLESS=1 DO_NOT_TRACK=1 MCPPROXY_TELEMETRY=false MCPPROXY_API_KEY="$API_KEY" \
    "$CORE_BIN" serve --config "$CONFIG_PATH" --data-dir "$DATA_DIR" --listen "$listen" >"$CORE_LOG" 2>&1 &
  CORE_PID=$!
}

# Starts the fixture (FIXTURE_CMD, FIXTURE_URL) and waits for its /mcp.
start_fixture() {
  local attempt
  debug "starting skillsfixture on $FIXTURE_ADDR"
  "${CLEAN_ENV[@]}" "${FIXTURE_CMD[@]}" >"$FIXTURE_LOG" 2>&1 &
  FIXTURE_PID=$!
  for attempt in $(seq 1 30); do
    if curl -s --connect-timeout 1 --max-time 3 -o /dev/null -X POST -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","id":1,"method":"skills/list"}' "$FIXTURE_URL/mcp"; then
      return 0
    fi
    if ! kill -0 "$FIXTURE_PID" >/dev/null 2>&1 || [[ $attempt -eq 30 ]]; then
      echo "skillsfixture did not start on $FIXTURE_ADDR" >&2
      redact_stream <"$FIXTURE_LOG" >&2
      exit 1
    fi
    sleep 0.5
  done
}

# Waits for the core's REST API; $1: base URL.
wait_core() {
  local base="$1" attempt code
  for attempt in $(seq 1 60); do
    code=$(curl -s --connect-timeout 2 --max-time 5 -o /dev/null -w '%{http_code}' -H "X-API-Key: ${API_KEY}" "$base/api/v1/servers" || true)
    [[ "$code" == "200" ]] && return 0
    if ! kill -0 "$CORE_PID" >/dev/null 2>&1 || [[ $attempt -eq 60 ]]; then
      echo "core did not become ready at $base" >&2
      redact_stream <"$CORE_LOG" >&2
      exit 1
    fi
    sleep 1
  done
}

# The core opened its database in the scratch dir, and only there.
check_core_data_dir() {
  if [[ ! -f "$DATA_DIR/config.db" ]]; then
    echo "the core did not open its database in the scratch data dir ($(say_path "$DATA_DIR/config.db") is missing)" >&2
    exit 1
  fi
}

# A port the kernel just handed out and released. A race with another process
# is possible but harmless: the start below fails fast and says so.
free_port() {
  node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{process.stdout.write(String(s.address().port));s.close()})'
}

# --- results writer -------------------------------------------------------------
# write_results OUT COMMIT DIRTY FIXTURE_PORT PW_STATUS SERVER CORE_PORT FIXTURE_CMD...
# Writes OUT/skills-sweep-results.json from OUT/playwright-results.json and
# OUT/state/. A scenario's status and error are those of its FIRST failed
# attempt (a retry replays against changed state and would hide the original
# error). The fixture command is recorded as basename + args. The file is then
# redacted (API key, local paths). Exit status 0 only when everything passed.
write_results() {
  local out="$1"
  node - "$@" <<'NODE'
const fs = require('fs'), path = require('path')
const [out, commit, dirty, fixturePort, pwStatus, server, corePort, ...fixtureCmd] = process.argv.slice(2)
const problems = []
const scenarios = []
try {
  const report = JSON.parse(fs.readFileSync(path.join(out, 'playwright-results.json'), 'utf8'))
  const walk = (suite) => {
    for (const spec of suite.specs || []) {
      const results = (spec.tests || []).flatMap(t => t.results || [])
      const last = results[results.length - 1]
      const firstFailed = results.find(r => r.status !== 'passed' && r.status !== 'skipped')
      const passed = spec.ok === true && !!last && last.status === 'passed'
      const status = passed ? (firstFailed ? 'flaky' : 'passed') : (firstFailed || last || { status: 'not run' }).status
      const errOf = r => r && (r.error || (r.errors || [])[0])
      const err = errOf(firstFailed) || (!passed ? errOf(last) : undefined)
      scenarios.push({ title: spec.title, passed, status, attempts: results.length,
        error: err ? String(err.message || err.value || '').slice(0, 2000) : undefined })
    }
    for (const s of suite.suites || []) walk(s)
  }
  for (const s of report.suites || []) walk(s)
} catch (e) { problems.push(`no Playwright JSON report: ${e.message}`) }
if (scenarios.length === 0) problems.push('no scenario ran')
for (const s of scenarios) if (!s.passed) problems.push(`scenario failed: ${s.title} (${s.status})`)
const states = {}
for (const step of ['approve', 'approve-changed', 'block', 'unblock']) {
  const file = path.join(out, 'state', `state-after-${step}.json`)
  try {
    const d = JSON.parse(fs.readFileSync(file, 'utf8'))
    states[step] = { incarnation: d.incarnation, skills: (d.skills || []).map(s => ({
      path: s.path, status: s.status, blocked: s.blocked, available: s.available,
      current_hash: s.current_hash, approved_hash: s.approved_hash })) }
  } catch { problems.push(`missing state after ${step}`) }
}
const isDirty = dirty === 'true'
const result = {
  // `git rev-parse HEAD` of the checkout the sweep ran from.
  checked_commit: commit,
  working_tree_dirty: isDirty,
  // A dirty tree is not the commit: this file is pre-commit evidence only.
  evidence: isDirty ? 'pre-commit' : 'commit',
  checked: isDirty
    ? `pre-commit evidence: a working tree with uncommitted changes on top of ${commit}; regenerate from a clean checkout before committing this file`
    : commit,
  generated_at: new Date().toISOString(),
  fixture: { command: fixtureCmd.length ? [path.basename(fixtureCmd[0]), ...fixtureCmd.slice(1)] : [], port: Number(fixturePort) },
  core: { listen: `127.0.0.1:${corePort}`, server, enable_skills: true, trust_mode: 'manual' },
  playwright_exit_code: Number(pwStatus),
  scenarios,
  states,
  passed: problems.length === 0 && Number(pwStatus) === 0,
  problems,
}
fs.writeFileSync(path.join(out, 'skills-sweep-results.json'), JSON.stringify(result, null, 2) + '\n')
process.exit(result.passed ? 0 : 1)
NODE
  local status=$?
  redact_file "$out/skills-sweep-results.json"
  return $status
}

# --- self-test ------------------------------------------------------------------
# run_self_test: no browser. Uses its own data dir and output dir, so a sweep
# that follows it (--self-test-first) starts from fresh state.
# st_check_child_output LABEL OUTPUT HANDOFF: the output of a child run (stdout
# and stderr) contains neither the child's API key nor any absolute path under
# its scratch dir, this run's scratch dir (the children's --out lives there) or
# the operator's home (the repo is under it). Never echoes the output.
st_check_child_output() {
  local label="$1" output="$2" handoff="$3" child_key child_work p
  [[ -s "$handoff" ]] || st_fail "$label did not hand back its key"
  child_key=$(sed -n 1p "$handoff")
  child_work=$(sed -n 2p "$handoff")
  [[ ${#child_key} -ge 32 && -n "$child_work" ]] || st_fail "$label handed back no key or scratch dir"
  grep -qF "$child_key" "$output" && st_fail "$label printed its API key ($(grep -cF "$child_key" "$output") lines)"
  grep -qF "$(node -e 'process.stdout.write(encodeURIComponent(process.argv[1]))' "$child_key")" "$output" && st_fail "$label printed its URL-encoded API key"
  for p in "$child_work" "${child_work#/private}" "$WORK" "${WORK#/private}" "$OPERATOR_HOME" "$REPO_ROOT"; do
    [[ ${#p} -gt 1 ]] || continue
    grep -qF "$p" "$output" && st_fail "$label printed a local path ($(grep -cF "$p" "$output") lines)"
  done
  return 0
}
st_fail() { printf 'self-test FAILED: %s\n' "$*" | redact_stream >&2; exit 1; }
run_self_test() {
  local sweep_data_dir="$DATA_DIR" st_port d step st_status results enc_key st_out decoy_new decoy_legacy decoy_audit_dir decoy_certs decoy_tpa
  DATA_DIR="$WORK/st-data"
  mkdir -p "$DATA_DIR"

  # 1. The guard refuses the operator's ~/.mcpproxy and anything inside it.
  [[ -n "$OPERATOR_HOME" ]] || st_fail "HOME is not set"
  data_dir_allowed "$OPERATOR_HOME/.mcpproxy" && st_fail "a data dir of \$HOME/.mcpproxy was allowed"
  data_dir_allowed "$OPERATOR_HOME/.mcpproxy/sub" && st_fail "a data dir under \$HOME/.mcpproxy was allowed"
  data_dir_allowed "$DATA_DIR" || st_fail "the scratch data dir was refused"
  echo "self-test: a data dir under \$HOME/.mcpproxy is refused; the scratch dir is allowed"

  # 1b. A printed path never shows an absolute local path: inside the repo it is
  # relative, under a known prefix it gets its placeholder, and any other
  # absolute path is reduced to <path>/basename (say_path), also inside a
  # message or a captured log (redact_stream).
  local got_path
  got_path=$(say_path "$REPO_ROOT/e2e/web-ui-sweep/x.json"); [[ "$got_path" == "e2e/web-ui-sweep/x.json" ]] || st_fail "say_path in the repo gave '$got_path'"
  got_path=$(say_path "$WORK/st-data/config.db"); [[ "$got_path" == "<scratch>/st-data/config.db" ]] || st_fail "say_path in the scratch dir gave '$got_path'"
  got_path=$(say_path "/usr/bin/chromium"); [[ "$got_path" == "<path>/chromium" ]] || st_fail "say_path of an outside path gave '$got_path'"
  got_path=$(say_path "/srv/odd place/chrome"); [[ "$got_path" == "<path>/chrome" ]] || st_fail "say_path of an outside path with a space gave '$got_path'"
  got_path=$(printf 'launch /opt/google/chrome/chrome failed; PATH=/usr/local/bin:/opt/homebrew/bin url http://127.0.0.1:1/api/v1/servers key %s' "$API_KEY" | redact_stream)
  [[ "$got_path" == 'launch <path>/chrome failed; PATH=<path>/bin:<path>/bin url http://127.0.0.1:1/api/v1/servers key ***' ]] || st_fail "redact_stream gave '$got_path'"
  # Roots in no list: /data and /work (round 7), a colour code and JSON escapes
  # in front of a path (Playwright's JSON report), and a bare core route kept.
  got_path=$(printf 'executable doesn'\''t exist at /data/tools/chromium; /work/tools/chromium:1 GET /api/v1/servers and /mcp' | redact_stream)
  [[ "$got_path" == 'executable doesn'\''t exist at <path>/chromium; <path>/chromium:1 GET /api/v1/servers and /mcp' ]] || st_fail "redact_stream of /data and /work roots gave '$got_path'"
  got_path=$(printf '{"message":"\\u001b[31mError\\u001b[39m: at /data/tools/chromium\\n/work/x/y.js\\t\\"/data/q\\""}' | redact_stream)
  [[ "$got_path" == '{"message":"\u001b[31mError\u001b[39m: at <path>/chromium\n<path>/y.js\t\"<path>/q\""}' ]] || st_fail "redact_stream of a JSON-escaped message gave '$got_path'"
  got_path=$(printf 'x\033[2m/data/tools/chromium\033[22m' | redact_stream)
  [[ "$got_path" == $'x\033[2m<path>/chromium\033[22m' ]] || st_fail "redact_stream after an ANSI code gave '$got_path'"
  # A configured path with a space is replaced as a whole.
  local saved_extra="$REDACT_EXTRA"
  redact_extra_add "/data/odd tools/chromium"
  got_path=$(printf 'launching /data/odd tools/chromium --headless' | redact_stream)
  REDACT_EXTRA="$saved_extra"
  [[ "$got_path" == 'launching <path>/chromium --headless' ]] || st_fail "redact_stream of a configured path with a space gave '$got_path'"
  echo "self-test: printed paths are relative, placeholders or <path>/basename (any root, configured paths whole); URLs and routes are kept"

  # 2. An ambient MCPPROXY_DATA_DIR / MCPPROXY_DATA does not reach the core.
  decoy_new="$WORK/decoy-data-dir"
  decoy_legacy="$WORK/decoy-data-legacy"
  decoy_audit_dir="$WORK/decoy-audit"
  decoy_certs="$WORK/decoy-certs"
  decoy_tpa="$WORK/decoy-tpa/bundle.json"
  mkdir -p "$decoy_new" "$decoy_legacy" "$decoy_audit_dir" "$decoy_certs"
  st_port=$(free_port)
  write_config "127.0.0.1:${st_port}" ""
  MCPPROXY_DATA_DIR="$decoy_new" MCPPROXY_DATA="$decoy_legacy" \
  MCPPROXY_AUDIT_LOG_ENABLED=true MCPPROXY_AUDIT_LOG_PATH="$decoy_audit_dir/audit.jsonl" \
  MCPPROXY_TPA_BUNDLE_PATH="$decoy_tpa" MCPPROXY_CERTS_DIR="$decoy_certs" \
    start_core "127.0.0.1:${st_port}"
  wait_core "http://127.0.0.1:${st_port}"
  check_core_data_dir
  for d in "$decoy_new" "$decoy_legacy"; do
    if [[ -n "$(ls -A "$d" 2>/dev/null)" ]]; then
      st_fail "the core wrote into the ambient data dir $d: $(ls -A "$d")"
    fi
  done
  stop_started
  echo "self-test: with MCPPROXY_DATA_DIR and MCPPROXY_DATA set to decoys, the core opened its database in the scratch dir only"

  # 2b. Nothing else of the operator's environment reaches the core either:
  # MCPPROXY_AUDIT_LOG_ENABLED/PATH would turn auditing on into a file of the
  # operator's (internal/config/loader.go), and MCPPROXY_TPA_BUNDLE_PATH /
  # MCPPROXY_CERTS_DIR name more paths. The core of 2. ran with all of them set
  # to decoys; it must have touched none, and its log must name none.
  for d in "$decoy_audit_dir" "$decoy_certs"; do
    if [[ -n "$(ls -A "$d" 2>/dev/null)" ]]; then
      st_fail "the core wrote into the ambient decoy $d: $(ls -A "$d")"
    fi
  done
  for d in "$decoy_audit_dir" "$decoy_certs" "$decoy_tpa"; do
    grep -qF "$d" "$CORE_LOG" && st_fail "the core log names the ambient decoy $d"
  done
  echo "self-test: with MCPPROXY_AUDIT_LOG_PATH, MCPPROXY_TPA_BUNDLE_PATH and MCPPROXY_CERTS_DIR set to decoys, the core touched none of them"

  # 2c. Cleanup stops what the run started, and only that. The recorded PIDs
  # must be the core and the fixture themselves (not a shell around them, whose
  # death would orphan the server), each must own its listening port, and after
  # stop_started (what the EXIT trap runs) neither PID, no process descended
  # from them, and neither port is alive, while an unrelated process of the
  # same shell is left running.
  local st_fx_port st_core_port tree pid want got bystander listener
  st_fx_port=$(free_port)
  FIXTURE_ADDR="127.0.0.1:${st_fx_port}"
  FIXTURE_URL="http://${FIXTURE_ADDR}"
  FIXTURE_CMD=("$FIXTURE_BIN" -addr "$FIXTURE_ADDR")
  start_fixture
  st_core_port=$(free_port)
  write_config "127.0.0.1:${st_core_port}" ""
  start_core "127.0.0.1:${st_core_port}"
  wait_core "http://127.0.0.1:${st_core_port}"
  sleep 30 &
  bystander=$!
  for pid in "$CORE_PID:$CORE_BIN:$st_core_port" "$FIXTURE_PID:$FIXTURE_BIN:$st_fx_port"; do
    IFS=: read -r pid want st_port <<<"$pid"
    want=$(basename "$want"); want=${want:0:15}
    got=$(ps -o comm= -p "$pid" 2>/dev/null || true); got=$(basename "${got:-?}"); got=${got:0:15}
    [[ "$got" == "$want" ]] || st_fail "the recorded PID $pid is '$got', not the $want process it started"
    if command -v lsof >/dev/null 2>&1; then
      listener=$(lsof -nP -iTCP:"$st_port" -sTCP:LISTEN -t 2>/dev/null | head -1 || true)
      [[ "$listener" == "$pid" ]] || st_fail "port $st_port is held by PID '${listener}', not by the recorded PID $pid ($want)"
    fi
  done
  tree="$(process_tree "$CORE_PID") $(process_tree "$FIXTURE_PID")"
  stop_started
  for pid in $tree; do
    kill -0 "$pid" >/dev/null 2>&1 && st_fail "PID $pid ($(ps -o comm= -p "$pid" 2>/dev/null)) of the run is still alive after cleanup"
  done
  for st_port in "$st_core_port" "$st_fx_port"; do
    set +e
    curl -s --connect-timeout 1 --max-time 2 -o /dev/null "http://127.0.0.1:${st_port}/"
    got=$?
    set -e
    [[ $got -eq 7 ]] || st_fail "port $st_port still answers after cleanup (curl exit $got)"
  done
  kill -0 "$bystander" >/dev/null 2>&1 || st_fail "cleanup killed a process it did not start"
  kill "$bystander" >/dev/null 2>&1 || true
  wait "$bystander" >/dev/null 2>&1 || true
  echo "self-test: the recorded PIDs are the core and the fixture themselves; after cleanup no process of the run is alive and both ports are closed, an unrelated process is untouched"

  # 3. The results writer: first failed attempt, redaction, provenance.
  st_out="$WORK/st-out"
  OUT_REAL="$st_out"
  mkdir -p "$st_out/state"
  for step in approve approve-changed block unblock; do
    printf '{"incarnation":1,"skills":[{"path":"plain","status":"approved","blocked":false,"available":true,"current_hash":"sk1-a","approved_hash":"sk1-a"}]}' >"$st_out/state/state-after-${step}.json"
  done
  enc_key=$(node -e 'process.stdout.write(encodeURIComponent(process.argv[1]))' "$API_KEY")
  # shellcheck disable=SC2016 # the JS template literals are expanded by node
  ST_OUT="$st_out" API_KEY="$API_KEY" ENC_KEY="$enc_key" REPO_ROOT="$REPO_ROOT" node -e '
const fs = require("fs"), e = process.env
const report = { suites: [{ specs: [
  { title: "retried", ok: false, tests: [{ results: [
    { status: "failed", error: { message: `page.goto: net::ERR at http://127.0.0.1:1/ui?apikey=${e.ENC_KEY} key=${e.API_KEY} in ${e.REPO_ROOT}/e2e/x.spec.ts` } },
    { status: "failed", error: { message: "the retry replayed against changed state" } }] }] },
  { title: "fine", ok: true, tests: [{ results: [{ status: "passed" }] }] },
] }] }
fs.writeFileSync(e.ST_OUT + "/playwright-results.json", JSON.stringify(report))'
  set +e
  write_results "$st_out" "0123456789abcdef0123456789abcdef01234567" true 4242 1 skillsfx 4343 "$WORK/bin/skillsfixture" -addr 127.0.0.1:4242
  st_status=$?
  set -e
  [[ $st_status -ne 0 ]] || st_fail "the results writer passed a failed run"
  results="$st_out/skills-sweep-results.json"
  grep -qF "$API_KEY" "$results" && st_fail "the API key is in the results file"
  grep -qF "$enc_key" "$results" && st_fail "the URL-encoded API key is in the results file"
  grep -qF "$WORK" "$results" && st_fail "a scratch path is in the results file"
  grep -qF "$REPO_ROOT" "$results" && st_fail "the repo path is in the results file"
  node -e '
const r = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"))
const s = r.scenarios.find(x => x.title === "retried")
const fail = m => { console.error("self-test FAILED: " + m); process.exit(1) }
if (!s || s.passed || s.status !== "failed" || s.attempts !== 2) fail("retried scenario: " + JSON.stringify(s))
if (!/apikey=\*\*\*/.test(s.error) || !/key=\*\*\*/.test(s.error)) fail("the key was not replaced by ***: " + s.error)
if (/changed state/.test(s.error)) fail("the retry error was recorded instead of the first failure")
if (!s.error.includes("<repo>/e2e/x.spec.ts")) fail("the repo path was not replaced: " + s.error)
if (JSON.stringify(r.fixture.command) !== JSON.stringify(["skillsfixture", "-addr", "127.0.0.1:4242"])) fail("fixture command: " + JSON.stringify(r.fixture.command))
if (r.checked_commit !== "0123456789abcdef0123456789abcdef01234567" || r.working_tree_dirty !== true || r.evidence !== "pre-commit" || !/^pre-commit evidence/.test(r.checked)) fail("provenance: " + JSON.stringify([r.checked_commit, r.working_tree_dirty, r.evidence, r.checked]))
' "$results"
  echo "self-test: the results writer records the first failed attempt, redacts the key and local paths, and labels a dirty tree pre-commit evidence"

  # 4. DEBUG=1 never prints the key or a local path: the whole self-test above
  # (config, core start with the key, curl with the key header, redaction) runs
  # again as a child with DEBUG=1, which hands back its own key and scratch dir
  # through a scratch file; the child's stdout and stderr must contain neither,
  # nor a path under the operator's home. The child skips steps 4 and 5.
  if [[ -z "${SWEEP_SELF_TEST_CHILD:-}" ]]; then
    local probe_out="$WORK/debug-probe.out" probe_key="$WORK/debug-probe.key" probe_status
    set +e
    DEBUG=1 SWEEP_SELF_TEST_CHILD="$probe_key" MCPPROXY_BINARY_PATH="$CORE_BIN" SKILLS_FIXTURE_BINARY="$FIXTURE_BIN" \
      bash "$SCRIPT_DIR/$(basename "${BASH_SOURCE[0]}")" --self-test >"$probe_out" 2>&1
    probe_status=$?
    set -e
    st_check_child_output "a passing DEBUG=1 self-test run" "$probe_out" "$probe_key"
    [[ $probe_status -eq 0 ]] || st_fail "the DEBUG=1 self-test run failed (exit $probe_status)"
    grep -qF "self-test passed" "$probe_out" || st_fail "the DEBUG=1 self-test run did not pass"
    echo "self-test: a passing DEBUG=1 run (the self-test as a child) printed no API key and no local path"

    # 5. A startup failure dumps the fixture's or the core's log, redacted. A
    # sweep run as a child with a stand-in fixture (then a stand-in core) that
    # prints its scratch HOME and TMPDIR, its arguments and (the core) its
    # environment with the API key, then exits 1: the run must fail, print the
    # log with the placeholders, and print no key and no local path.
    local fakes="$WORK/st-fakes" which bin want
    mkdir -p "$fakes"
    # shellcheck disable=SC2016 # expanded by the stand-in's own shell
    printf '#!/bin/sh\necho "fixture HOME=$HOME TMPDIR=$TMPDIR args: $*"\nexit 1\n' >"$fakes/skillsfixture"
    printf '#!/bin/sh\necho "core args: $*"\nenv\nexit 1\n' >"$fakes/mcpproxy"
    chmod +x "$fakes/skillsfixture" "$fakes/mcpproxy"
    for which in fixture core; do
      if [[ $which == fixture ]]; then bin=("$CORE_BIN" "$fakes/skillsfixture"); want="skillsfixture did not start"
      else bin=("$fakes/mcpproxy" "$FIXTURE_BIN"); want="core did not become ready"; fi
      rm -f "$probe_key"
      set +e
      SWEEP_SELF_TEST_CHILD="$probe_key" MCPPROXY_BINARY_PATH="${bin[0]}" SKILLS_FIXTURE_BINARY="${bin[1]}" \
        bash "$SCRIPT_DIR/$(basename "${BASH_SOURCE[0]}")" --out "$WORK/st-fail-$which" >"$probe_out" 2>&1
      probe_status=$?
      set -e
      st_check_child_output "a run whose $which fails to start" "$probe_out" "$probe_key"
      [[ $probe_status -ne 0 ]] || st_fail "a run whose $which fails to start exited 0"
      grep -qF "$want" "$probe_out" || st_fail "a run whose $which fails to start did not say '$want'"
      grep -qF "<scratch>/" "$probe_out" || st_fail "a run whose $which fails to start did not print its log through the redactor"
      if [[ $which == core ]]; then
        grep -qF "MCPPROXY_API_KEY=***" "$probe_out" || st_fail "the core's environment with the redacted key was not in the dumped log"
      fi
    done
    echo "self-test: a fixture or core startup failure prints its log redacted, with no API key and no local path"

    # 6. A Playwright failure prints Playwright's own output (list reporter,
    # launch errors with the browser's logs) redacted only. A sweep run as a
    # child with PW_CHROMIUM naming a stand-in browser outside every known
    # prefix (a fresh dir under /tmp, not the scratch dir, the repo or the
    # home), which prints its arguments and its environment (the API key is in
    # it) and exits 1: the run must fail, say so, name the browser as
    # <path>/chromium, print Playwright's failure, and print no key and no
    # absolute path of the stand-in.
    local ext ext_dir ext_real f
    ext_dir=$(mktemp -d /tmp/skills-sweep-pwext.XXXXXX)
    # A space in the path: the generic rule alone would stop at it; the
    # configured path is replaced as a whole.
    ext="$ext_dir/odd tools"
    mkdir -p "$ext"
    ext_real=$(cd "$ext" && pwd -P)
    # shellcheck disable=SC2016 # expanded by the stand-in's own shell
    printf '#!/bin/sh\necho "stand-in browser $0 args: $*" >&2\necho "MCPPROXY_API_KEY=$MCPPROXY_API_KEY" >&2\nexit 1\n' >"$ext/chromium"
    chmod +x "$ext/chromium"
    rm -f "$probe_key"
    set +e
    SWEEP_SELF_TEST_CHILD="$probe_key" PW_CHROMIUM="$ext/chromium" MCPPROXY_BINARY_PATH="$CORE_BIN" SKILLS_FIXTURE_BINARY="$FIXTURE_BIN" \
      bash "$SCRIPT_DIR/$(basename "${BASH_SOURCE[0]}")" --out "$WORK/st-pw-fail" >"$probe_out" 2>&1
    probe_status=$?
    set -e
    rm -rf "$ext_dir"
    st_check_child_output "a run whose Playwright fails" "$probe_out" "$probe_key"
    for f in "$probe_out" "$WORK/st-pw-fail/playwright-results.json" "$WORK/st-pw-fail/skills-sweep-results.json"; do
      [[ -f "$f" ]] || st_fail "a run whose Playwright fails left no $(basename "$f")"
      grep -qF "$(sed -n 1p "$probe_key")" "$f" && st_fail "a run whose Playwright fails wrote its API key into $(basename "$f")"
      for p in "$ext_dir" "$ext_real" "${ext_real#/private}" "${ext_dir#/private}"; do
        grep -qF "$p" "$f" && st_fail "a run whose Playwright fails printed the stand-in browser's path into $(basename "$f") ($(grep -cF "$p" "$f") lines)"
      done
    done
    [[ $probe_status -ne 0 ]] || st_fail "a run whose Playwright fails exited 0"
    grep -qF "skills tab sweep FAILED" "$probe_out" || st_fail "a run whose Playwright fails did not say so"
    grep -qF "using PW_CHROMIUM=<path>/chromium" "$probe_out" || st_fail "a run whose Playwright fails did not name the browser as <path>/chromium"
    grep -qE "[0-9]+ failed" "$probe_out" || st_fail "a run whose Playwright fails did not print Playwright's own summary"
    echo "self-test: a Playwright failure with an outside PW_CHROMIUM prints Playwright's output redacted, with no API key and no absolute path"

    # 7. Build output is printed redacted too: a self-test run as a child that
    # has to build the fixture, with a stand-in go first on PATH that prints its
    # working directory, its home and its arguments and exits 1.
    local fakego="$WORK/st-fakego"
    mkdir -p "$fakego"
    # shellcheck disable=SC2016 # expanded by the stand-in's own shell
    printf '#!/bin/sh\necho "go in $PWD home $HOME args: $*"\necho "go: error at $PWD/cmd/skillsfixture/main.go:1" >&2\nexit 1\n' >"$fakego/go"
    chmod +x "$fakego/go"
    rm -f "$probe_key"
    set +e
    SWEEP_SELF_TEST_CHILD="$probe_key" PATH="$fakego:$PATH" MCPPROXY_BINARY_PATH="$CORE_BIN" SKILLS_FIXTURE_BINARY="" \
      bash "$SCRIPT_DIR/$(basename "${BASH_SOURCE[0]}")" --self-test >"$probe_out" 2>&1
    probe_status=$?
    set -e
    st_check_child_output "a run whose go build fails" "$probe_out" "$probe_key"
    [[ $probe_status -ne 0 ]] || st_fail "a run whose go build fails exited 0"
    grep -qF "go: error at <repo>/cmd/skillsfixture/main.go:1" "$probe_out" || st_fail "a run whose go build fails did not print the build output redacted"
    echo "self-test: a failing build prints its output redacted, with no absolute path"

    # 8. An unwritable --out or report parent fails at once, with the mkdir
    # diagnostics redacted: a read-only dir (a new subdir cannot be made in it,
    # and the dir itself is not writable). Skipped as root, who can write it.
    if [[ "$(id -u)" != "0" ]]; then
      local ro="$WORK/st-readonly" case_args
      mkdir -p "$ro"
      chmod 555 "$ro"
      for case_args in "--out|$ro/sub" "--out|$ro" "--report-html|$ro/sub/report.html" "--report-html|$ro/report.html"; do
        IFS='|' read -r which p <<<"$case_args"
        rm -f "$probe_key"
        set +e
        if [[ $which == --out ]]; then
          SWEEP_SELF_TEST_CHILD="$probe_key" MCPPROXY_BINARY_PATH="$CORE_BIN" SKILLS_FIXTURE_BINARY="$FIXTURE_BIN" \
            bash "$SCRIPT_DIR/$(basename "${BASH_SOURCE[0]}")" --out "$p" >"$probe_out" 2>&1
        else
          SWEEP_SELF_TEST_CHILD="$probe_key" MCPPROXY_BINARY_PATH="$CORE_BIN" SKILLS_FIXTURE_BINARY="$FIXTURE_BIN" \
            bash "$SCRIPT_DIR/$(basename "${BASH_SOURCE[0]}")" --out "$WORK/st-ro-out" --report-html "$p" >"$probe_out" 2>&1
        fi
        probe_status=$?
        set -e
        st_check_child_output "a run with an unwritable $which" "$probe_out" "$probe_key"
        [[ $probe_status -ne 0 ]] || st_fail "a run with an unwritable $which ($(say_path "$p")) exited 0"
        grep -qF "cannot create or write the" "$probe_out" || st_fail "a run with an unwritable $which ($(say_path "$p")) did not say so"
        grep -qF "skillsfixture ready" "$probe_out" && st_fail "a run with an unwritable $which started the sweep"
      done
      chmod 755 "$ro"
      echo "self-test: an unwritable --out or report directory fails at once, with its diagnostics redacted"
    fi
  fi
  echo "self-test passed"
  DATA_DIR="$sweep_data_dir"
  OUT_REAL=""
}

if [[ $SELF_TEST -eq 1 ]]; then
  CORE_BIN="${MCPPROXY_BINARY_PATH:-}"
  if [[ -z "$CORE_BIN" ]]; then
    required go
    # The data-dir proof needs no frontend: a plain build of the core.
    CORE_BIN="$WORK/bin/mcpproxy"
    (cd "$REPO_ROOT" && run_logged go-build-core go build -o "$CORE_BIN" ./cmd/mcpproxy)
  fi
  FIXTURE_BIN="${SKILLS_FIXTURE_BINARY:-}"
  if [[ -z "$FIXTURE_BIN" ]]; then
    required go
    FIXTURE_BIN="$WORK/bin/skillsfixture"
    (cd "$REPO_ROOT" && run_logged go-build-fixture go build -o "$FIXTURE_BIN" ./cmd/skillsfixture)
  fi
  run_self_test
  exit 0
fi

# The output dirs are made through run_logged, so a failure's diagnostics
# (mkdir names the absolute path) are printed redacted, and checked writable
# up front, so an unwritable dir fails here and not after the whole sweep.
# $1: what it is, $2: the dir.
make_writable_dir() {
  local status=0
  run_logged "mkdir-$1" mkdir -p "$2" >&2 || status=$?
  if [[ $status -eq 0 && ! -w "$2" ]]; then status=1; fi
  if [[ $status -ne 0 ]]; then
    echo "cannot create or write the $1 directory $(say_path "$2") (status $status)" >&2
    exit 1
  fi
}
make_writable_dir out "$OUT"
OUT=$(cd "$OUT" && pwd -P)
OUT_REAL="$OUT"
if [[ -n "$REPORT_HTML" ]]; then
  make_writable_dir report "$(dirname "$REPORT_HTML")"
  REPORT_HTML="$(cd "$(dirname "$REPORT_HTML")" && pwd -P)/$(basename "$REPORT_HTML")"
fi
rm -rf "$OUT/screenshots" "$OUT/state" "$OUT/playwright-results.json" "$OUT/skills-sweep-results.json"
mkdir -p "$OUT/screenshots" "$OUT/state"

# --- binaries ---------------------------------------------------------------
CORE_BIN="${MCPPROXY_BINARY_PATH:-}"
if [[ -z "$CORE_BIN" ]]; then
  required go
  echo "building the frontend and the core (embedded frontend) from this checkout..."
  if [[ ! -d "$REPO_ROOT/frontend/node_modules" ]]; then
    (cd "$REPO_ROOT/frontend" && run_logged frontend-npm-ci npm ci)
  fi
  (cd "$REPO_ROOT/frontend" && run_logged frontend-build npm run build)
  rm -rf "$REPO_ROOT/web/frontend"
  mkdir -p "$REPO_ROOT/web/frontend/dist"
  cp -r "$REPO_ROOT/frontend/dist/." "$REPO_ROOT/web/frontend/dist/"
  touch "$REPO_ROOT/web/frontend/dist/.gitkeep"
  CORE_BIN="$WORK/bin/mcpproxy"
  (cd "$REPO_ROOT" && run_logged go-build-core go build -o "$CORE_BIN" ./cmd/mcpproxy)
fi
FIXTURE_BIN="${SKILLS_FIXTURE_BINARY:-}"
if [[ -z "$FIXTURE_BIN" ]]; then
  required go
  FIXTURE_BIN="$WORK/bin/skillsfixture"
  (cd "$REPO_ROOT" && run_logged go-build-fixture go build -o "$FIXTURE_BIN" ./cmd/skillsfixture)
fi
for b in "$CORE_BIN" "$FIXTURE_BIN"; do
  if [[ ! -x "$b" ]]; then
    echo "not an executable: $(say_path "$b")" >&2
    exit 1
  fi
done
if [[ $SELF_TEST_FIRST -eq 1 ]]; then
  run_self_test
fi
OUT_REAL="$OUT"

# --- fixture ----------------------------------------------------------------
FIXTURE_PORT=$(free_port)
FIXTURE_ADDR="127.0.0.1:${FIXTURE_PORT}"
FIXTURE_URL="http://${FIXTURE_ADDR}"
FIXTURE_CMD=("$FIXTURE_BIN" -addr "$FIXTURE_ADDR")
start_fixture
echo "skillsfixture ready at $FIXTURE_URL"

# --- core -------------------------------------------------------------------
CORE_PORT=$(free_port)
LISTEN="127.0.0.1:${CORE_PORT}"
BASE_URL="http://${LISTEN}"
write_config "$LISTEN" "${FIXTURE_URL}/mcp"
start_core "$LISTEN"
H=(-H "X-API-Key: ${API_KEY}")
wait_core "$BASE_URL"
check_core_data_dir
echo "core ready at $BASE_URL (data dir: the scratch dir)"

# Approve the (quarantined) server, then wait until a refresh reads the three
# skills: that needs the upstream connected and the extension announced.
code=$(curl -s --max-time 10 -o "$WORK/approve.json" -w '%{http_code}' -X POST "${H[@]}" \
  -H 'Content-Type: application/json' -d '{"force":true}' "$BASE_URL/api/v1/servers/${SERVER}/security/approve")
if [[ "$code" != "200" ]]; then
  echo "approving ${SERVER} failed with HTTP $code: $(redact_stream <"$WORK/approve.json")" >&2
  exit 1
fi
ready=0
for attempt in $(seq 1 60); do
  if curl -s --max-time 10 -o "$WORK/refresh.json" -X POST "${H[@]}" "$BASE_URL/api/v1/servers/${SERVER}/skills/refresh" &&
     node -e 'const b=JSON.parse(require("fs").readFileSync(process.argv[1],"utf8"));const d=b.data||b;process.exit(d.supported&&(d.skills||[]).length===3?0:1)' "$WORK/refresh.json" 2>/dev/null; then
    ready=1
    break
  fi
  sleep 1
done
if [[ $ready -ne 1 ]]; then
  echo "the core never listed the fixture's three skills; last refresh: $( (redact_stream <"$WORK/refresh.json") 2>/dev/null)" >&2
  exit 1
fi
echo "server ${SERVER} approved; three skills listed"

debug "running Playwright"
# --- Playwright (same locked install as scripts/run-web-smoke.sh) -------------
SWEEP_DIR="$REPO_ROOT/e2e/web-ui-sweep"
export PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH:-$REPO_ROOT/tmp/playwright-browsers}"
mkdir -p "$PLAYWRIGHT_BROWSERS_PATH"
lock_hash() {
  node -e 'const c=require("crypto"),f=require("fs");process.stdout.write(c.createHash("sha256").update(f.readFileSync(process.argv[1])).digest("hex"))' "$1" 2>/dev/null || true
}
LOCK_STAMP="$SWEEP_DIR/node_modules/.sweep-lock-sha256"
WANT_LOCK_HASH=$(lock_hash "$SWEEP_DIR/package-lock.json")
if [[ -z "$WANT_LOCK_HASH" ]]; then
  echo "cannot hash $(say_path "$SWEEP_DIR/package-lock.json")" >&2
  exit 1
fi
HAVE_LOCK_HASH=""
[[ -f "$LOCK_STAMP" ]] && HAVE_LOCK_HASH=$(cat "$LOCK_STAMP")
if [[ ! -x "$SWEEP_DIR/node_modules/.bin/playwright" || "$HAVE_LOCK_HASH" != "$WANT_LOCK_HASH" ]]; then
  (cd "$SWEEP_DIR" && run_logged sweep-npm-ci npm ci)
  printf '%s' "$WANT_LOCK_HASH" >"$LOCK_STAMP"
fi
PLAYWRIGHT_BIN="$SWEEP_DIR/node_modules/.bin/playwright"
if [[ -n "${PW_CHROMIUM:-}" ]]; then
  # playwright.config.ts launches this binary instead of a downloaded one.
  echo "using PW_CHROMIUM=$(say_path "$PW_CHROMIUM") (no browser download)"
elif [[ "$(uname -s)" == "Linux" ]]; then
  run_logged playwright-install "$PLAYWRIGHT_BIN" install --with-deps chromium
else
  run_logged playwright-install "$PLAYWRIGHT_BIN" install chromium
fi

# The key reaches the spec through the environment only: it sets it in the Web
# UI's localStorage, never in a page URL, and records no traces (the Web UI's
# SSE URL carries it). Playwright's own output (the list reporter, failure stack
# traces, launch errors with the browser's logs) is captured into the scratch
# dir and printed redacted; its exit status is kept.
PLAYWRIGHT_STATUS=0
(
  cd "$SWEEP_DIR" || exit 1
  run_logged playwright-test env \
    MCPPROXY_BASE_URL="$BASE_URL" \
    MCPPROXY_API_KEY="$API_KEY" \
    SWEEP_SERVER_NAME="$SERVER" \
    SKILLS_FIXTURE_URL="$FIXTURE_URL" \
    SKILLS_SWEEP_STATE_DIR="$OUT/state" \
    SWEEP_SCREENSHOT_DIR="$OUT/screenshots" \
    SWEEP_REPORT_DIR="$OUT/playwright-report" \
    PLAYWRIGHT_JSON_OUTPUT_NAME="$OUT/playwright-results.json" \
    "$PLAYWRIGHT_BIN" test skills-tab.spec.ts --reporter=list,json --output "$WORK/test-results"
) || PLAYWRIGHT_STATUS=$?
redact_file "$OUT/playwright-results.json"
for f in "$OUT"/state/*.json; do redact_file "$f"; done

# --- results file -------------------------------------------------------------
COMMIT=$(git -C "$REPO_ROOT" rev-parse HEAD)
DIRTY=false
if [[ -n "$(git -C "$REPO_ROOT" status --porcelain)" ]]; then DIRTY=true; fi
set +e
write_results "$OUT" "$COMMIT" "$DIRTY" "$FIXTURE_PORT" "$PLAYWRIGHT_STATUS" "$SERVER" "$CORE_PORT" "${FIXTURE_CMD[@]}"
RESULT_STATUS=$?
set -e

if [[ -n "$REPORT_HTML" ]]; then
  node - "$OUT" "$REPORT_HTML" "$SCREENSHOTS" <<'NODE'
const fs = require('fs'), path = require('path')
const [out, dest, screenshots] = process.argv.slice(2)
const withShots = screenshots === '1'
const r = JSON.parse(fs.readFileSync(path.join(out, 'skills-sweep-results.json'), 'utf8'))
const esc = s => String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#x27;' })[c])
const img = name => {
  if (!withShots) return ''
  const f = path.join(out, 'screenshots', `skills-tab-${name}.png`)
  return fs.existsSync(f) ? `<img alt="${esc(name)}" src="data:image/png;base64,${fs.readFileSync(f).toString('base64')}">` : ''
}
const shotNote = shots => (!withShots && shots.length)
  ? `<p class="t">Screenshots (not embedded in this copy): ${shots.map(s => `<code>screenshots/skills-tab-${esc(s)}.png</code>`).join(', ')}</p>` : ''
// One section per scenario, in spec order, with what it proves and its screenshots.
const about = [
  ['Tab and list', 'The Skills tab is offered on a server with enable_skills on and a skills-capable upstream (cmd/skillsfixture, manual trust). The three skills show as Awaiting review and Not offered.', ['01-list']],
  ['Viewer and paging', 'SKILL.md opens in the text viewer (inert text). A 150 KiB file (data/large.txt of big) is read part by part in at least three parts: each Load more waits for its content response and for the rendered text to grow by that part. Every part is of the current copy, its digest equals the digest the core lists for that file in the current copy (the same digest the file row shows on screen), its size equals the listed size, its offset is where the previous part ended (in bytes, the first at 0) and its next_offset is where it ends; the last part has no next_offset and ends at the listed size. A binary file (logo.bin) is listed as binary, not shown, with no View button.', ['02-viewer', '03-paged']],
  ['Approve', 'Approve sends the current_hash and incarnation of the version the reviewer opened; the chip turns Approved.', ['04-approved']],
  ['Changed while open, re-open, diff', 'A mutate without the X-Skills-Fixture header is refused (403). The approved skill plain changes upstream while it is open; after Refresh the chip says Changed and the open review says it changed while you were reviewing it (the review shows the approved version, so it offers no Approve). Re-opening shows the file-set diff (SKILL.md changed) and the line diff with the added line; approving binds the new hash.', ['05a-stale', '05-changed-diff']],
  ['Pending skill changed while open', 'The pending skill with-files changes upstream while it is open; after Refresh the review is marked stale and its Approve button stays on screen but disabled, until the new version is re-opened.', ['05b-pending-stale']],
  ['Live skills.changed', 'With the pending skill big open, its SKILL.md changes upstream and the refresh is made through POST /api/v1/servers/{id}/skills/refresh, not in the page. The core\'s skills.changed SSE frame, re-dispatched by the Web UI, reloads the listing by itself and the open review shows the stale banner with Approve disabled.', ['05c-live-event-stale']],
  ['Block and unblock', 'Block marks the skill Blocked and removes Approve; Unblock returns it to Awaiting review.', ['06-blocked']],
  ['Reload', 'The tab survives a reload through ?tab=skills, and no uncaught page error was thrown in any scenario.', []],
]
const stateTable = step => {
  const s = r.states[step]
  if (!s) return `<p>missing</p>`
  return `<table><tr><th>skill</th><th>status</th><th>blocked</th><th>available</th><th>current_hash</th><th>approved_hash</th></tr>${s.skills.map(k =>
    `<tr><td>${esc(k.path)}</td><td>${esc(k.status)}</td><td>${k.blocked}</td><td>${k.available}</td><td><code>${esc(k.current_hash)}</code></td><td><code>${esc(k.approved_hash || '')}</code></td></tr>`).join('')}</table>`
}
const passed = r.scenarios.filter(s => s.passed).length
const sections = r.scenarios.map((s, i) => {
  const [title, text, shots] = about[i] || [s.title, '', []]
  const badge = s.passed ? '<span class="ok">PASS</span>' : '<span class="bad">FAIL</span>'
  return `<details open><summary>${i + 1}. ${esc(title)} ${badge}</summary><p class="t">${esc(s.title)}</p><p>${esc(text)}</p>${s.error ? `<pre>${esc(s.error)}</pre>` : ''}${shots.map(img).join('')}${shotNote(shots)}</details>`
}).join('\n')
const provenance = r.working_tree_dirty
  ? `<div class="card warn"><strong>Pre-commit evidence.</strong> The working tree had uncommitted changes on top of <code>${esc(r.checked_commit)}</code>; this report does not describe that commit. Regenerate it from a clean checkout before committing it.</div>`
  : ''
const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Spec 125 Skills tab verification</title>
<style>body{font:15px/1.5 system-ui,sans-serif;max-width:1100px;margin:2rem auto;padding:0 1rem;color:#1c1e21}
.card{border:1px solid #d0d7de;border-radius:8px;padding:1rem 1.25rem;margin-bottom:1.25rem;background:#f6f8fa}.warn{background:#fff8c5;border-color:#d4a72c}
details{border:1px solid #d0d7de;border-radius:8px;margin-bottom:1rem;padding:.5rem 1rem}summary{cursor:pointer;font-weight:600}
.ok{background:#1a7f37;color:#fff;border-radius:4px;padding:0 .4rem;font-size:.8em;margin-left:.5rem}.bad{background:#cf222e;color:#fff;border-radius:4px;padding:0 .4rem;font-size:.8em;margin-left:.5rem}
.t{color:#57606a;font-size:.85em;margin:.25rem 0}img{max-width:100%;border:1px solid #d0d7de;border-radius:4px;margin:.25rem 0}code{background:#eaeef2;padding:0 .25rem;border-radius:3px;font-size:.85em}
table{border-collapse:collapse;font-size:.85em;margin:.5rem 0}td,th{border:1px solid #d0d7de;padding:.2rem .4rem;text-align:left}</style></head><body>
<h1>Spec 125: Skills tab (PR-4a) Playwright verification</h1>
${provenance}<div class="card"><strong>${passed} of ${r.scenarios.length} scenarios passed</strong>, ${esc(r.generated_at.slice(0, 10))}, produced by <code>scripts/run-skills-tab-sweep.sh</code> from commit <strong><code>${esc(r.checked_commit)}</code></strong> (working tree ${r.working_tree_dirty ? 'dirty' : 'clean'}).<br>
A throwaway core (${esc(r.core.listen)}, scratch data dir, <code>enable_skills: true</code>, generated API key) served the embedded frontend; the upstream <code>${esc(r.core.server)}</code> (<code>trust_mode: manual</code>) was <code>${esc(r.fixture.command.join(' '))}</code> on port ${r.fixture.port}.<br>
Spec: <code>e2e/web-ui-sweep/skills-tab.spec.ts</code>. Results file: <code>skills-sweep-results.json</code>. Repeat: <code>./scripts/run-skills-tab-sweep.sh --report-html specs/125-skills-over-mcp/verification/report.html --no-screenshots</code>.</div>
${sections}
<details open><summary>Skill records read back from GET /api/v1/servers/${esc(r.core.server)}/skills</summary>
<h3>After approve</h3>${stateTable('approve')}<h3>After approving the change</h3>${stateTable('approve-changed')}<h3>After block</h3>${stateTable('block')}<h3>After unblock</h3>${stateTable('unblock')}</details>
</body></html>
`
fs.writeFileSync(dest, html)
NODE
  # Every string in the report comes from the redacted results file; the copy
  # without screenshots (pure text) is redacted once more as a whole.
  [[ $SCREENSHOTS -eq 0 ]] && redact_file "$REPORT_HTML"
  echo "HTML report written to $(say_path "$REPORT_HTML")$([[ $SCREENSHOTS -eq 0 ]] && echo ' (no embedded screenshots)')"
fi

echo "results: $(say_path "$OUT/skills-sweep-results.json")"
if [[ $PLAYWRIGHT_STATUS -ne 0 || $RESULT_STATUS -ne 0 ]]; then
  echo "skills tab sweep FAILED (playwright exit $PLAYWRIGHT_STATUS); see $(say_path "$OUT")" >&2
  exit 1
fi
echo "skills tab sweep passed"
