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
#     ~/.mcpproxy), enable_skills on, a generated throwaway API key, and the
#     fixture as a trust_mode: manual streamable-http upstream;
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
Usage: ${0##*/} [--out DIR] [--report-html FILE]

Options:
  --out DIR           where the results land (default: <repo>/tmp/skills-sweep)
  --report-html FILE  also write a self-contained HTML report (one section per
                      scenario, with its screenshots) to FILE
  -h, --help          print this message

Environment:
  MCPPROXY_BINARY_PATH   core binary with the embedded frontend (default: build
                         the frontend and the core from this checkout)
  SKILLS_FIXTURE_BINARY  cmd/skillsfixture binary (default: go build it)
  SWEEP_SERVER_NAME      upstream name (default: skillsfx)
  PW_CHROMIUM            optional Chromium/Chrome binary; skips the browser
                         download (offline machines)

Output (in --out):
  skills-sweep-results.json  commit, fixture command line and port, each
                             scenario's pass/fail, and GET /skills read back
                             after the approve, block and unblock steps
  screenshots/  state/  playwright-results.json  core.log  fixture.log
Exit status is non-zero when any scenario failed or a state is missing.
EOF
}

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
OUT="$REPO_ROOT/tmp/skills-sweep"
REPORT_HTML=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out) OUT="${2:?--out needs a directory}"; shift 2 ;;
    --report-html) REPORT_HTML="${2:?--report-html needs a file}"; shift 2 ;;
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

mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
if [[ -n "$REPORT_HTML" ]]; then
  mkdir -p "$(dirname "$REPORT_HTML")"
  REPORT_HTML="$(cd "$(dirname "$REPORT_HTML")" && pwd)/$(basename "$REPORT_HTML")"
fi
rm -rf "$OUT/screenshots" "$OUT/state" "$OUT/playwright-results.json" "$OUT/skills-sweep-results.json"
mkdir -p "$OUT/screenshots" "$OUT/state"

SERVER="${SWEEP_SERVER_NAME:-skillsfx}"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/skills-sweep.XXXXXX")
DATA_DIR="$WORK/data"
CONFIG_PATH="$WORK/mcp_config.json"
CORE_LOG="$WORK/core.log"
FIXTURE_LOG="$WORK/fixture.log"
mkdir -p "$DATA_DIR"

cleanup() {
  local pid
  for pid in "${CORE_PID:-}" "${FIXTURE_PID:-}"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
    fi
  done
  cp "$CORE_LOG" "$OUT/core.log" 2>/dev/null || true
  cp "$FIXTURE_LOG" "$OUT/fixture.log" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# A port the kernel just handed out and released. A race with another process
# is possible but harmless: the start below fails fast and says so.
free_port() {
  node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{process.stdout.write(String(s.address().port));s.close()})'
}

# Throwaway, not guessable: the key guards the core's whole admin API (see
# scripts/run-web-smoke.sh for why an ambient MCPPROXY_API_KEY is never used).
API_KEY="skills-sweep-$(node -e 'process.stdout.write(require("crypto").randomBytes(24).toString("hex"))')"
if [[ ${#API_KEY} -lt 32 ]]; then
  echo "failed to generate a random API key" >&2
  exit 1
fi

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
# Every value below is generated here (ports, a temp path, a hex key), so the
# heredoc cannot be broken by quoting; the temp path is checked to be sure.
case "$DATA_DIR" in
  *'"'* | *\\* | *$'\n'*) echo "scratch dir path cannot be embedded in JSON: $DATA_DIR" >&2; exit 1 ;;
esac
cat >"$CONFIG_PATH" <<JSON
{
  "listen": "${LISTEN}",
  "data_dir": "${DATA_DIR}",
  "api_key": "${API_KEY}",
  "enable_tray": false,
  "enable_socket": false,
  "enable_web_ui": true,
  "enable_skills": true,
  "check_server_repo": false,
  "telemetry": { "enabled": false },
  "logging": { "level": "info", "enable_file": false, "enable_console": true },
  "mcpServers": [
    {
      "name": "${SERVER}",
      "url": "${FIXTURE_URL}/mcp",
      "protocol": "streamable-http",
      "enabled": true,
      "trust_mode": "manual"
    }
  ]
}
JSON

HEADLESS=1 DO_NOT_TRACK=1 MCPPROXY_TELEMETRY=false MCPPROXY_API_KEY="$API_KEY" \
  "$CORE_BIN" serve --config "$CONFIG_PATH" --listen "$LISTEN" >"$CORE_LOG" 2>&1 &
CORE_PID=$!
H=(-H "X-API-Key: ${API_KEY}")
for attempt in $(seq 1 60); do
  code=$(curl -s --connect-timeout 2 --max-time 5 -o /dev/null -w '%{http_code}' "${H[@]}" "$BASE_URL/api/v1/servers" || true)
  [[ "$code" == "200" ]] && break
  if ! kill -0 "$CORE_PID" >/dev/null 2>&1 || [[ $attempt -eq 60 ]]; then
    echo "core did not become ready at $BASE_URL" >&2
    cat "$CORE_LOG" >&2
    exit 1
  fi
  sleep 1
done
echo "core ready at $BASE_URL"

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
    "$PLAYWRIGHT_BIN" test skills-tab.spec.ts --reporter=list,json
)
PLAYWRIGHT_STATUS=$?
set -e

# --- results file -------------------------------------------------------------
COMMIT=$(git -C "$REPO_ROOT" rev-parse HEAD)
DIRTY=false
if [[ -n "$(git -C "$REPO_ROOT" status --porcelain)" ]]; then DIRTY=true; fi
set +e
node - "$OUT" "$COMMIT" "$DIRTY" "$FIXTURE_PORT" "$PLAYWRIGHT_STATUS" "$SERVER" "$CORE_PORT" "${FIXTURE_CMD[@]}" <<'NODE'
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
      const status = last ? last.status : 'not run'
      scenarios.push({ title: spec.title, passed: spec.ok === true && status === 'passed', status,
        error: last && last.error ? String(last.error.message || '').slice(0, 2000) : undefined })
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
const result = {
  checked_commit: commit,
  working_tree_dirty: dirty === 'true',
  checked: dirty === 'true' ? `working tree on top of ${commit}` : commit,
  generated_at: new Date().toISOString(),
  fixture: { command: fixtureCmd, port: Number(fixturePort) },
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
RESULT_STATUS=$?
set -e

if [[ -n "$REPORT_HTML" ]]; then
  node - "$OUT" "$REPORT_HTML" <<'NODE'
const fs = require('fs'), path = require('path')
const [out, dest] = process.argv.slice(2)
const r = JSON.parse(fs.readFileSync(path.join(out, 'skills-sweep-results.json'), 'utf8'))
const esc = s => String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#x27;' })[c])
const img = name => {
  const f = path.join(out, 'screenshots', `skills-tab-${name}.png`)
  return fs.existsSync(f) ? `<img alt="${esc(name)}" src="data:image/png;base64,${fs.readFileSync(f).toString('base64')}">` : ''
}
// One section per scenario, in spec order, with what it proves and its screenshots.
const about = [
  ['Tab and list', 'The Skills tab is offered on a server with enable_skills on and a skills-capable upstream (cmd/skillsfixture, manual trust). The three skills show as Awaiting review and Not offered.', ['01-list']],
  ['Viewer and paging', 'SKILL.md opens in the text viewer (inert text). A 150 KiB file is read part by part: each Load more waits for its content response and the rendered text, every part carries the digest listed on screen, and the last part has no next_offset. A binary file (logo.bin) is listed as binary, not shown, with no View button.', ['02-viewer', '03-paged']],
  ['Approve', 'Approve sends the current_hash and incarnation of the version the reviewer opened; the chip turns Approved.', ['04-approved']],
  ['Changed while open, re-open, diff', 'A mutate without the X-Skills-Fixture header is refused (403). The upstream bytes change while the skill is open; after Refresh the chip says Changed and the open review says it changed while you were reviewing it. Re-opening shows the file-set diff (SKILL.md changed) and the line diff with the added line; approving binds the new hash.', ['05a-stale', '05-changed-diff']],
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
  return `<details open><summary>${i + 1}. ${esc(title)} ${badge}</summary><p class="t">${esc(s.title)}</p><p>${esc(text)}</p>${s.error ? `<pre>${esc(s.error)}</pre>` : ''}${shots.map(img).join('')}</details>`
}).join('\n')
const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Spec 125 Skills tab verification</title>
<style>body{font:15px/1.5 system-ui,sans-serif;max-width:1100px;margin:2rem auto;padding:0 1rem;color:#1c1e21}
.card{border:1px solid #d0d7de;border-radius:8px;padding:1rem 1.25rem;margin-bottom:1.25rem;background:#f6f8fa}
details{border:1px solid #d0d7de;border-radius:8px;margin-bottom:1rem;padding:.5rem 1rem}summary{cursor:pointer;font-weight:600}
.ok{background:#1a7f37;color:#fff;border-radius:4px;padding:0 .4rem;font-size:.8em;margin-left:.5rem}.bad{background:#cf222e;color:#fff;border-radius:4px;padding:0 .4rem;font-size:.8em;margin-left:.5rem}
.t{color:#57606a;font-size:.85em;margin:.25rem 0}img{max-width:100%;border:1px solid #d0d7de;border-radius:4px;margin:.25rem 0}code{background:#eaeef2;padding:0 .25rem;border-radius:3px;font-size:.85em}
table{border-collapse:collapse;font-size:.85em;margin:.5rem 0}td,th{border:1px solid #d0d7de;padding:.2rem .4rem;text-align:left}</style></head><body>
<h1>Spec 125: Skills tab (PR-4a) Playwright verification</h1>
<div class="card"><strong>${passed} of ${r.scenarios.length} scenarios passed</strong>, ${esc(r.generated_at.slice(0, 10))}, produced by <code>scripts/run-skills-tab-sweep.sh</code> from <strong>${esc(r.checked)}</strong>.<br>
A throwaway core (${esc(r.core.listen)}, scratch data dir, <code>enable_skills: true</code>, generated API key) served the embedded frontend; the upstream <code>${esc(r.core.server)}</code> (<code>trust_mode: manual</code>) was <code>${esc([path.basename(r.fixture.command[0]), ...r.fixture.command.slice(1)].join(' '))}</code> on port ${r.fixture.port}.<br>
Spec: <code>e2e/web-ui-sweep/skills-tab.spec.ts</code>. Results file: <code>skills-sweep-results.json</code>. Repeat: <code>./scripts/run-skills-tab-sweep.sh --report-html specs/125-skills-over-mcp/verification/report.html</code>.</div>
${sections}
<details open><summary>Skill records read back from GET /api/v1/servers/${esc(r.core.server)}/skills</summary>
<h3>After approve</h3>${stateTable('approve')}<h3>After approving the change</h3>${stateTable('approve-changed')}<h3>After block</h3>${stateTable('block')}<h3>After unblock</h3>${stateTable('unblock')}</details>
</body></html>
`
fs.writeFileSync(dest, html)
NODE
  echo "HTML report written to $REPORT_HTML"
fi

echo "results: $OUT/skills-sweep-results.json"
if [[ $PLAYWRIGHT_STATUS -ne 0 || $RESULT_STATUS -ne 0 ]]; then
  echo "skills tab sweep FAILED (playwright exit $PLAYWRIGHT_STATUS); see $OUT" >&2
  exit 1
fi
echo "skills tab sweep passed"
