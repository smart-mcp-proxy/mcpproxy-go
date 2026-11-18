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
#     ~/.mcpproxy: MCPPROXY_DATA_DIR / MCPPROXY_DATA, which would override the
#     config's data_dir, are removed from its environment, --data-dir names the
#     scratch dir, HOME points at a scratch home, and a data dir under
#     $HOME/.mcpproxy is refused), enable_skills on, a generated throwaway API
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

if [[ "${DEBUG:-}" == "1" ]]; then
  set -x
fi

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
                      MCPPROXY_DATA never reaches the core (it starts one with
                      both set to decoy dirs and checks the database lands in
                      the scratch dir only), that a data dir under
                      \$HOME/.mcpproxy is refused, and that the results writer
                      records the first failed attempt and redacts the API key
                      and local paths
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

# Every text file this script leaves behind goes through redact_file: the API
# key becomes *** and the local paths become placeholders. Set once OUT is known.
OUT_REAL=""
redact_file() {
  [[ -f "$1" ]] || return 0
  SWEEP_REDACT_KEY="$API_KEY" SWEEP_REDACT_OUT="$OUT_REAL" SWEEP_REDACT_REPO="$REPO_ROOT" \
  SWEEP_REDACT_WORK="$WORK" SWEEP_REDACT_HOME="$OPERATOR_HOME" node -e '
const fs = require("fs"), f = process.argv[1], e = process.env
let t = fs.readFileSync(f, "utf8")
const key = e.SWEEP_REDACT_KEY
if (key) for (const k of [key, encodeURIComponent(key)]) t = t.split(k).join("***")
const paths = [[e.SWEEP_REDACT_OUT, "<out>"], [e.SWEEP_REDACT_WORK, "<scratch>"], [e.SWEEP_REDACT_REPO, "<repo>"], [e.SWEEP_REDACT_HOME, "~"]]
  .filter(([p]) => p && p.length > 1).sort((a, b) => b[0].length - a[0].length)
for (const [p, r] of paths) t = t.split(p).join(r)
fs.writeFileSync(f, t)' "$1"
}

cleanup() {
  local pid
  for pid in "${CORE_PID:-}" "${FIXTURE_PID:-}"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
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

# Starts the core detached from the operator's data: MCPPROXY_DATA_DIR and the
# legacy MCPPROXY_DATA override the config's data_dir (internal/config/loader.go),
# so both are removed from its environment; --data-dir (which wins over the file
# and the environment) names the scratch dir; HOME is a scratch home, so a
# default path can never resolve to the operator's ~/.mcpproxy. $1: listen.
start_core() {
  local listen="$1"
  if ! data_dir_allowed "$DATA_DIR"; then
    echo "refusing to start: data dir $DATA_DIR is under $OPERATOR_HOME/.mcpproxy" >&2
    exit 1
  fi
  env -u MCPPROXY_DATA_DIR -u MCPPROXY_DATA \
    HOME="$CORE_HOME" HEADLESS=1 DO_NOT_TRACK=1 MCPPROXY_TELEMETRY=false MCPPROXY_API_KEY="$API_KEY" \
    "$CORE_BIN" serve --config "$CONFIG_PATH" --data-dir "$DATA_DIR" --listen "$listen" >"$CORE_LOG" 2>&1 &
  CORE_PID=$!
}

# Waits for the core's REST API; $1: base URL.
wait_core() {
  local base="$1" attempt code
  for attempt in $(seq 1 60); do
    code=$(curl -s --connect-timeout 2 --max-time 5 -o /dev/null -w '%{http_code}' -H "X-API-Key: ${API_KEY}" "$base/api/v1/servers" || true)
    [[ "$code" == "200" ]] && return 0
    if ! kill -0 "$CORE_PID" >/dev/null 2>&1 || [[ $attempt -eq 60 ]]; then
      echo "core did not become ready at $base" >&2
      cat "$CORE_LOG" >&2
      exit 1
    fi
    sleep 1
  done
}

# The core opened its database in the scratch dir, and only there.
check_core_data_dir() {
  if [[ ! -f "$DATA_DIR/config.db" ]]; then
    echo "the core did not open its database in the scratch data dir ($DATA_DIR/config.db is missing)" >&2
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
st_fail() { echo "self-test FAILED: $*" >&2; exit 1; }
run_self_test() {
  local sweep_data_dir="$DATA_DIR" st_port d step st_status results enc_key st_out decoy_new decoy_legacy
  DATA_DIR="$WORK/st-data"
  mkdir -p "$DATA_DIR"

  # 1. The guard refuses the operator's ~/.mcpproxy and anything inside it.
  [[ -n "$OPERATOR_HOME" ]] || st_fail "HOME is not set"
  data_dir_allowed "$OPERATOR_HOME/.mcpproxy" && st_fail "a data dir of \$HOME/.mcpproxy was allowed"
  data_dir_allowed "$OPERATOR_HOME/.mcpproxy/sub" && st_fail "a data dir under \$HOME/.mcpproxy was allowed"
  data_dir_allowed "$DATA_DIR" || st_fail "the scratch data dir was refused"
  echo "self-test: a data dir under \$HOME/.mcpproxy is refused; the scratch dir is allowed"

  # 2. An ambient MCPPROXY_DATA_DIR / MCPPROXY_DATA does not reach the core.
  decoy_new="$WORK/decoy-data-dir"
  decoy_legacy="$WORK/decoy-data-legacy"
  mkdir -p "$decoy_new" "$decoy_legacy"
  st_port=$(free_port)
  write_config "127.0.0.1:${st_port}" ""
  MCPPROXY_DATA_DIR="$decoy_new" MCPPROXY_DATA="$decoy_legacy" start_core "127.0.0.1:${st_port}"
  wait_core "http://127.0.0.1:${st_port}"
  check_core_data_dir
  for d in "$decoy_new" "$decoy_legacy"; do
    if [[ -n "$(ls -A "$d" 2>/dev/null)" ]]; then
      st_fail "the core wrote into the ambient data dir $d: $(ls -A "$d")"
    fi
  done
  kill "$CORE_PID" >/dev/null 2>&1 || true
  wait "$CORE_PID" >/dev/null 2>&1 || true
  CORE_PID=""
  echo "self-test: with MCPPROXY_DATA_DIR and MCPPROXY_DATA set to decoys, the core opened its database in the scratch dir only"

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
    (cd "$REPO_ROOT" && go build -o "$CORE_BIN" ./cmd/mcpproxy)
  fi
  run_self_test
  exit 0
fi

mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd -P)
OUT_REAL="$OUT"
if [[ -n "$REPORT_HTML" ]]; then
  mkdir -p "$(dirname "$REPORT_HTML")"
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
    (cd "$REPO_ROOT/frontend" && npm ci)
  fi
  (cd "$REPO_ROOT/frontend" && npm run build)
  rm -rf "$REPO_ROOT/web/frontend"
  mkdir -p "$REPO_ROOT/web/frontend/dist"
  cp -r "$REPO_ROOT/frontend/dist/." "$REPO_ROOT/web/frontend/dist/"
  touch "$REPO_ROOT/web/frontend/dist/.gitkeep"
  CORE_BIN="$WORK/bin/mcpproxy"
  (cd "$REPO_ROOT" && go build -o "$CORE_BIN" ./cmd/mcpproxy)
fi
FIXTURE_BIN="${SKILLS_FIXTURE_BINARY:-}"
if [[ -z "$FIXTURE_BIN" ]]; then
  required go
  FIXTURE_BIN="$WORK/bin/skillsfixture"
  (cd "$REPO_ROOT" && go build -o "$FIXTURE_BIN" ./cmd/skillsfixture)
fi
for b in "$CORE_BIN" "$FIXTURE_BIN"; do
  if [[ ! -x "$b" ]]; then
    echo "not an executable: $b" >&2
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
"${FIXTURE_CMD[@]}" >"$FIXTURE_LOG" 2>&1 &
FIXTURE_PID=$!
for attempt in $(seq 1 30); do
  if curl -s --connect-timeout 1 --max-time 3 -o /dev/null -X POST -H 'Content-Type: application/json' \
      -d '{"jsonrpc":"2.0","id":1,"method":"skills/list"}' "$FIXTURE_URL/mcp"; then
    break
  fi
  if ! kill -0 "$FIXTURE_PID" >/dev/null 2>&1 || [[ $attempt -eq 30 ]]; then
    echo "skillsfixture did not start on $FIXTURE_ADDR" >&2
    cat "$FIXTURE_LOG" >&2
    exit 1
  fi
  sleep 0.5
done
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
  echo "approving ${SERVER} failed with HTTP $code: $(cat "$WORK/approve.json")" >&2
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
  echo "the core never listed the fixture's three skills; last refresh: $(cat "$WORK/refresh.json" 2>/dev/null)" >&2
  exit 1
fi
echo "server ${SERVER} approved; three skills listed"

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
  echo "cannot hash $SWEEP_DIR/package-lock.json" >&2
  exit 1
fi
HAVE_LOCK_HASH=""
[[ -f "$LOCK_STAMP" ]] && HAVE_LOCK_HASH=$(cat "$LOCK_STAMP")
if [[ ! -x "$SWEEP_DIR/node_modules/.bin/playwright" || "$HAVE_LOCK_HASH" != "$WANT_LOCK_HASH" ]]; then
  (cd "$SWEEP_DIR" && npm ci)
  printf '%s' "$WANT_LOCK_HASH" >"$LOCK_STAMP"
fi
PLAYWRIGHT_BIN="$SWEEP_DIR/node_modules/.bin/playwright"
if [[ -n "${PW_CHROMIUM:-}" ]]; then
  # playwright.config.ts launches this binary instead of a downloaded one.
  echo "using PW_CHROMIUM=$PW_CHROMIUM (no browser download)"
elif [[ "$(uname -s)" == "Linux" ]]; then
  "$PLAYWRIGHT_BIN" install --with-deps chromium
else
  "$PLAYWRIGHT_BIN" install chromium
fi

# The key reaches the spec through the environment only: it sets it in the Web
# UI's localStorage, never in a page URL, and records no traces (the Web UI's
# SSE URL carries it). Playwright's own output stays in the scratch dir.
set +e
(
  cd "$SWEEP_DIR" || exit 1
  MCPPROXY_BASE_URL="$BASE_URL" \
  MCPPROXY_API_KEY="$API_KEY" \
  SWEEP_SERVER_NAME="$SERVER" \
  SKILLS_FIXTURE_URL="$FIXTURE_URL" \
  SKILLS_SWEEP_STATE_DIR="$OUT/state" \
  SWEEP_SCREENSHOT_DIR="$OUT/screenshots" \
  SWEEP_REPORT_DIR="$OUT/playwright-report" \
  PLAYWRIGHT_JSON_OUTPUT_NAME="$OUT/playwright-results.json" \
    "$PLAYWRIGHT_BIN" test skills-tab.spec.ts --reporter=list,json --output "$WORK/test-results"
)
PLAYWRIGHT_STATUS=$?
set -e
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
  ['Viewer and paging', 'SKILL.md opens in the text viewer (inert text). A 150 KiB file is read part by part: each Load more waits for its content response and the rendered text, every part carries the digest listed on screen, and the last part has no next_offset. A binary file (logo.bin) is listed as binary, not shown, with no View button.', ['02-viewer', '03-paged']],
  ['Approve', 'Approve sends the current_hash and incarnation of the version the reviewer opened; the chip turns Approved.', ['04-approved']],
  ['Changed while open, re-open, diff', 'A mutate without the X-Skills-Fixture header is refused (403). The approved skill plain changes upstream while it is open; after Refresh the chip says Changed and the open review says it changed while you were reviewing it (the review shows the approved version, so it offers no Approve). Re-opening shows the file-set diff (SKILL.md changed) and the line diff with the added line; approving binds the new hash.', ['05a-stale', '05-changed-diff']],
  ['Pending skill changed while open', 'The pending skill with-files changes upstream while it is open; after Refresh the review is marked stale and its Approve button stays on screen but disabled, until the new version is re-opened.', ['05b-pending-stale']],
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
  echo "HTML report written to $REPORT_HTML$([[ $SCREENSHOTS -eq 0 ]] && echo ' (no embedded screenshots)')"
fi

echo "results: $OUT/skills-sweep-results.json"
if [[ $PLAYWRIGHT_STATUS -ne 0 || $RESULT_STATUS -ne 0 ]]; then
  echo "skills tab sweep FAILED (playwright exit $PLAYWRIGHT_STATUS); see $OUT" >&2
  exit 1
fi
echo "skills tab sweep passed"
