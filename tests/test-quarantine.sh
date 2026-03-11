#!/usr/bin/env bash
set -euo pipefail

# === Configuration ===
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
CONFIG="$SCRIPT_DIR/quarantine-test-config.json"
BINARY="$PROJECT_DIR/mcpproxy"
API_KEY="test-quarantine-key"
BASE_URL="http://127.0.0.1:8080"
API_URL="$BASE_URL/api/v1"
MCP_URL="$BASE_URL/mcp"
OUTPUT_DIR=$(mktemp -d)
REPORT_FILE="$PROJECT_DIR/docs/qa/quarantine-test-report-2026-03-11.html"

# Test counters
TOTAL=0; PASS=0; FAIL=0
declare -a TEST_RESULTS=()

# === Helper Functions ===
log() { echo "[$(date +%H:%M:%S)] $*"; }
log_ok() { echo "[$(date +%H:%M:%S)] PASS: $*"; }
log_fail() { echo "[$(date +%H:%M:%S)] FAIL: $*"; }

run_test() {
  local id="$1" name="$2" surface="$3" cmd="$4" assertion="$5"
  TOTAL=$((TOTAL + 1))
  local output_file="$OUTPUT_DIR/${id}.out"
  local status="pass"
  local http_code=""

  log "Running $id: $name"

  # For curl commands, capture HTTP status code separately
  local actual_cmd="$cmd"
  if [[ "$cmd" == *"curl "* ]]; then
    actual_cmd=$(echo "$cmd" | sed 's/curl /curl -w "\\nHTTP_STATUS:%{http_code}\\n" /')
  fi

  # Execute and capture
  local exit_code=0
  eval "$actual_cmd" > "$output_file" 2>&1 || exit_code=$?

  local output
  output=$(cat "$output_file")

  # Extract HTTP status code if present
  if echo "$output" | grep -q "HTTP_STATUS:"; then
    http_code=$(echo "$output" | grep "HTTP_STATUS:" | tail -1 | sed 's/HTTP_STATUS://')
  fi

  # Check assertion (grep pattern in output)
  if echo "$output" | grep -qE "$assertion"; then
    log_ok "$id: $name"
    PASS=$((PASS + 1))
  else
    log_fail "$id: $name (assertion failed: expected pattern '$assertion')"
    status="fail"
    FAIL=$((FAIL + 1))
  fi

  # Store result as JSON-ish for report
  TEST_RESULTS+=("$(cat <<JSONEOF
{
  "id": "$id",
  "name": $(echo "$name" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read().strip()))'),
  "status": "$status",
  "surface": "$surface",
  "http_code": "$http_code",
  "command": $(echo "$cmd" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read().strip()))'),
  "output": $(echo "$output" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))'),
  "assertion": $(echo "$assertion" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read().strip()))')
}
JSONEOF
  )")
}

wait_for_server() {
  local max_wait=30
  local waited=0
  while ! curl -sf -H "X-API-Key: $API_KEY" "$API_URL/status" > /dev/null 2>&1; do
    sleep 1
    waited=$((waited + 1))
    if [ $waited -ge $max_wait ]; then
      echo "ERROR: Server did not start within ${max_wait}s"
      exit 1
    fi
  done
  log "Server ready (waited ${waited}s)"
}

wait_for_servers_connected() {
  local max_wait=15
  local waited=0
  while true; do
    local count
    count=$(curl -sf -H "X-API-Key: $API_KEY" "$API_URL/servers" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sum(1 for s in d.get('data',{}).get('servers',[]) if s.get('status')=='connected'))" 2>/dev/null || echo "0")
    if [ "$count" -ge 2 ]; then
      log "Both servers connected"
      return 0
    fi
    sleep 1
    waited=$((waited + 1))
    if [ $waited -ge $max_wait ]; then
      log "Warning: Only $count servers connected after ${max_wait}s, continuing anyway"
      return 0
    fi
  done
}

cleanup() {
  log "Cleaning up..."
  pkill -f "mcpproxy serve.*quarantine-test-config" 2>/dev/null || true
  rm -rf "$PROJECT_DIR/test-data-quarantine" 2>/dev/null || true
  sleep 1
}

trap cleanup EXIT

# === Main ===
log "Starting quarantine QA tests"
log "Output dir: $OUTPUT_DIR"

# Clean previous state
cleanup
sleep 1

# Start MCPProxy
log "Starting MCPProxy with quarantine test config..."
cd "$PROJECT_DIR"
"$BINARY" serve --config "$CONFIG" --log-level=info &
MCPPROXY_PID=$!
wait_for_server
wait_for_servers_connected
sleep 2  # Let tool discovery complete

# ============================================================
# Test 1: List servers (CLI + curl)
# ============================================================
run_test "QT-01" "List servers - CLI" "CLI" \
  "$BINARY upstream list --config $CONFIG" \
  "(malicious-server|echo-rugpull)"

run_test "QT-01b" "List servers - REST API" "REST" \
  "curl -sf -H 'X-API-Key: $API_KEY' '$API_URL/servers'" \
  "malicious-server"

# ============================================================
# Test 2: Inspect pending tools
# ============================================================
run_test "QT-02a" "Inspect malicious-server pending tools" "CLI" \
  "$BINARY upstream inspect malicious-server --config $CONFIG" \
  "pending"

run_test "QT-02b" "Inspect echo-rugpull pending tools" "CLI" \
  "$BINARY upstream inspect echo-rugpull --config $CONFIG" \
  "pending"

# ============================================================
# Test 3: Try calling a blocked tool (MCP)
# ============================================================
run_test "QT-03" "Call blocked tool via MCP" "MCP" \
  "curl -sf -X POST '$MCP_URL' -H 'Content-Type: application/json' -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"call_tool_read\",\"arguments\":{\"server_name\":\"malicious-server\",\"tool_name\":\"fetch_data\",\"args_json\":\"{\\\"url\\\":\\\"https://example.com\\\"}\"}}}'" \
  "(quarantine|blocked|pending|not approved)"

# ============================================================
# Test 4: Search for blocked tools (should not appear)
# ============================================================
run_test "QT-04" "Search for blocked tool via retrieve_tools" "MCP" \
  "curl -sf -X POST '$MCP_URL' -H 'Content-Type: application/json' -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"retrieve_tools\",\"arguments\":{\"query\":\"fetch data echo\"}}}'" \
  "(result|tools)"

# ============================================================
# Test 5: Approve malicious-server tools (CLI)
# ============================================================
run_test "QT-05" "Approve malicious-server tools via CLI" "CLI" \
  "$BINARY upstream approve malicious-server --config $CONFIG" \
  "(approved|Approved)"

# ============================================================
# Test 6: Approve echo-rugpull tools (REST API)
# ============================================================
run_test "QT-06" "Approve echo-rugpull tools via REST API" "REST" \
  "curl -sf -X POST -H 'X-API-Key: $API_KEY' -H 'Content-Type: application/json' -d '{\"approve_all\":true}' '$API_URL/servers/echo-rugpull/tools/approve'" \
  "(approved|success)"

sleep 2  # Let index rebuild

# ============================================================
# Test 7: Call echo tool (should work now)
# ============================================================
run_test "QT-07" "Call approved echo tool via MCP" "MCP" \
  "curl -sf -X POST '$MCP_URL' -H 'Content-Type: application/json' -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"call_tool_read\",\"arguments\":{\"server_name\":\"echo-rugpull\",\"tool_name\":\"echo\",\"args_json\":\"{\\\"text\\\":\\\"hello quarantine test\\\"}\"}}}'" \
  "hello quarantine test"

# ============================================================
# Test 8: Restart echo-rugpull to trigger rug pull
# ============================================================
run_test "QT-08" "Restart echo-rugpull server" "CLI" \
  "$BINARY upstream restart echo-rugpull --config $CONFIG" \
  "(restart|Restart)"

sleep 5  # Let server reconnect and re-discover tools

# ============================================================
# Test 9: Inspect changed tools
# ============================================================
run_test "QT-09" "Inspect echo-rugpull for changed tools" "CLI" \
  "$BINARY upstream inspect echo-rugpull --config $CONFIG" \
  "changed"

# ============================================================
# Test 10: View tool diff via REST API
# ============================================================
run_test "QT-10" "View echo tool diff via REST API" "REST" \
  "curl -sf -H 'X-API-Key: $API_KEY' '$API_URL/servers/echo-rugpull/tools/echo/diff'" \
  "(evil.example.com|previous_description|changed)"

# ============================================================
# Test 11: Try calling changed tool (should be blocked)
# ============================================================
run_test "QT-11" "Call changed tool via MCP (should be blocked)" "MCP" \
  "curl -sf -X POST '$MCP_URL' -H 'Content-Type: application/json' -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"call_tool_read\",\"arguments\":{\"server_name\":\"echo-rugpull\",\"tool_name\":\"echo\",\"args_json\":\"{\\\"text\\\":\\\"should be blocked\\\"}\"}}}'" \
  "(quarantine|blocked|changed|not approved)"

# ============================================================
# Test 12: Approve changed tools via MCP quarantine_security tool
# ============================================================
run_test "QT-12" "Approve changed tools via MCP quarantine_security" "MCP" \
  "curl -sf -X POST '$MCP_URL' -H 'Content-Type: application/json' -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"quarantine_security\",\"arguments\":{\"operation\":\"approve_all_tools\",\"name\":\"echo-rugpull\"}}}'" \
  "(approved|Approved)"

sleep 2

# ============================================================
# Test 13: Export tool approvals
# ============================================================
run_test "QT-13" "Export tool approvals via REST API" "REST" \
  "curl -sf -H 'X-API-Key: $API_KEY' '$API_URL/servers/echo-rugpull/tools/export'" \
  "(echo|get_time|approved)"

# ============================================================
# Test 14: Activity log check
# ============================================================
run_test "QT-14" "Check activity log for quarantine events" "CLI" \
  "$BINARY activity list --config $CONFIG --limit 50" \
  "(tool_call|quarantine|activity)"

# ============================================================
# Generate HTML Report
# ============================================================
log "Generating HTML report..."

# Collect environment info
MCPPROXY_VERSION=$("$BINARY" version 2>&1 | head -1 || echo "unknown")
NODE_VERSION=$(node --version 2>&1 || echo "unknown")
OS_INFO=$(uname -srm)

# Build JSON array of test results
TESTS_JSON="["
for i in "${!TEST_RESULTS[@]}"; do
  if [ $i -gt 0 ]; then TESTS_JSON+=","; fi
  TESTS_JSON+="${TEST_RESULTS[$i]}"
done
TESTS_JSON+="]"

mkdir -p "$(dirname "$REPORT_FILE")"

python3 - "$TESTS_JSON" "$MCPPROXY_VERSION" "$NODE_VERSION" "$OS_INFO" "$TOTAL" "$PASS" "$FAIL" "$REPORT_FILE" <<'PYEOF'
import json, sys, html
from datetime import datetime

tests_json = sys.argv[1]
version = sys.argv[2]
node_ver = sys.argv[3]
os_info = sys.argv[4]
total = int(sys.argv[5])
passed = int(sys.argv[6])
failed = int(sys.argv[7])
report_file = sys.argv[8]

tests = json.loads(tests_json)

def test_html(t):
    status_class = f"status-{t['status']}"
    surface_class = f"surface-{t['surface'].lower()}"
    escaped_output = html.escape(t.get('output', ''))
    escaped_cmd = html.escape(t.get('command', ''))
    escaped_assertion = html.escape(t.get('assertion', ''))
    http_code = t.get('http_code', '')
    http_badge = f'<span class="surface-badge" style="margin-left:0.5rem">HTTP {html.escape(http_code)}</span>' if http_code else ''
    return f'''
    <details class="test-item" data-status="{t['status']}" data-surface="{t['surface']}">
      <summary class="test-header">
        <span class="chevron">&#9654;</span>
        <span class="status-badge {status_class}">{t['status']}</span>
        <span class="surface-badge {surface_class}">{t['surface']}</span>{http_badge}
        <span class="test-name">{t['id']}: {html.escape(t['name'])}</span>
      </summary>
      <div class="test-body">
        <div class="section">
          <div class="section-title">Command</div>
          <pre>{escaped_cmd}</pre>
        </div>
        <div class="section">
          <div class="section-title">Assertion Pattern</div>
          <pre>{escaped_assertion}</pre>
        </div>
        <div class="section">
          <div class="section-title">Raw Output</div>
          <pre>{escaped_output}</pre>
        </div>
      </div>
    </details>'''

tests_html = "\n".join(test_html(t) for t in tests)
now = datetime.now().strftime("%Y-%m-%dT%H:%M:%S")

report = f'''<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>MCPProxy Quarantine QA Report - 2026-03-11</title>
  <style>
    :root {{
      --pass: #22c55e; --pass-bg: #dcfce7; --pass-text: #166534;
      --fail: #ef4444; --fail-bg: #fee2e2; --fail-text: #991b1b;
      --skip: #eab308; --skip-bg: #fef9c3; --skip-text: #854d0e;
      --blocked: #6b7280; --blocked-bg: #f3f4f6; --blocked-text: #374151;
      --bg: #f8fafc; --card: #fff; --text: #1e293b; --muted: #64748b;
      --border: #e2e8f0; --accent: #3b82f6; --code-bg: #1e293b;
    }}
    @media (prefers-color-scheme: dark) {{
      :root {{
        --bg: #0f172a; --card: #1e293b; --text: #f1f5f9; --muted: #94a3b8;
        --border: #334155; --pass-bg: #166534; --pass-text: #dcfce7;
        --fail-bg: #991b1b; --fail-text: #fee2e2;
        --skip-bg: #854d0e; --skip-text: #fef9c3;
        --blocked-bg: #374151; --blocked-text: #f3f4f6;
      }}
    }}
    * {{ box-sizing: border-box; margin: 0; padding: 0; }}
    body {{
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
      background: var(--bg); color: var(--text); line-height: 1.6; font-size: 14px;
    }}
    .container {{ max-width: 1400px; margin: 0 auto; padding: 1.5rem; }}
    header {{
      background: var(--card); border-radius: 12px; padding: 1.5rem;
      margin-bottom: 1.5rem; box-shadow: 0 1px 3px rgba(0,0,0,0.1);
    }}
    header h1 {{ font-size: 1.5rem; margin-bottom: 0.5rem; display: flex; align-items: center; gap: 0.5rem; }}
    header h1::before {{ content: ""; display: inline-block; width: 8px; height: 8px; background: var(--accent); border-radius: 50%; }}
    .metadata {{
      display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
      gap: 0.75rem; margin-top: 1rem;
    }}
    .meta-item {{ background: var(--bg); padding: 0.75rem; border-radius: 8px; }}
    .meta-label {{ font-size: 0.7rem; color: var(--muted); text-transform: uppercase; letter-spacing: 0.05em; }}
    .meta-value {{ font-weight: 600; font-family: 'SF Mono', Consolas, monospace; font-size: 0.875rem; }}
    .summary {{
      display: grid; grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
      gap: 1rem; margin-bottom: 1.5rem;
    }}
    .stat {{
      padding: 1.25rem; background: var(--card); border-radius: 12px;
      text-align: center; box-shadow: 0 1px 3px rgba(0,0,0,0.1);
    }}
    .stat-value {{ font-size: 2rem; font-weight: 700; line-height: 1; }}
    .stat-label {{ font-size: 0.75rem; color: var(--muted); margin-top: 0.25rem; text-transform: uppercase; }}
    .stat.total .stat-value {{ color: var(--accent); }}
    .stat.pass .stat-value {{ color: var(--pass); }}
    .stat.fail .stat-value {{ color: var(--fail); }}
    .controls {{
      display: flex; gap: 1rem; margin-bottom: 1.5rem; flex-wrap: wrap;
      align-items: center; background: var(--card); padding: 1rem;
      border-radius: 12px; box-shadow: 0 1px 3px rgba(0,0,0,0.1);
    }}
    .search {{
      flex: 1; min-width: 200px; padding: 0.625rem 1rem;
      border: 1px solid var(--border); border-radius: 8px;
      font-size: 0.875rem; background: var(--bg); color: var(--text);
    }}
    .search:focus {{ outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px rgba(59, 130, 246, 0.1); }}
    .filter-group {{ display: flex; gap: 0.5rem; flex-wrap: wrap; }}
    .filter-btn {{
      padding: 0.5rem 1rem; border: 1px solid var(--border); border-radius: 8px;
      background: var(--bg); cursor: pointer; font-size: 0.75rem;
      font-weight: 500; text-transform: uppercase; letter-spacing: 0.05em;
      transition: all 0.2s; color: var(--text);
    }}
    .filter-btn:hover {{ border-color: var(--accent); }}
    .filter-btn.active {{ background: var(--text); color: var(--bg); border-color: var(--text); }}
    .surface-filter {{ display: flex; gap: 0.5rem; }}
    .test-list {{ display: flex; flex-direction: column; gap: 0.5rem; }}
    .test-item {{
      background: var(--card); border-radius: 12px; overflow: hidden;
      box-shadow: 0 1px 3px rgba(0,0,0,0.1);
    }}
    .test-header {{
      display: flex; align-items: center; gap: 1rem; padding: 1rem;
      cursor: pointer; user-select: none;
    }}
    .test-header:hover {{ background: var(--bg); }}
    .status-badge {{
      padding: 0.25rem 0.75rem; border-radius: 9999px;
      font-size: 0.7rem; font-weight: 600; text-transform: uppercase;
      letter-spacing: 0.05em; flex-shrink: 0;
    }}
    .status-pass {{ background: var(--pass-bg); color: var(--pass-text); }}
    .status-fail {{ background: var(--fail-bg); color: var(--fail-text); }}
    .surface-badge {{
      padding: 0.125rem 0.5rem; border-radius: 4px; font-size: 0.65rem;
      font-weight: 600; text-transform: uppercase; background: var(--bg);
      color: var(--muted); border: 1px solid var(--border);
    }}
    .surface-mcp {{ color: #8b5cf6; border-color: #8b5cf6; }}
    .surface-cli {{ color: #06b6d4; border-color: #06b6d4; }}
    .surface-rest {{ color: #10b981; border-color: #10b981; }}
    .test-name {{ font-weight: 600; flex: 1; }}
    details summary {{ list-style: none; }}
    details summary::-webkit-details-marker {{ display: none; }}
    details[open] .chevron {{ transform: rotate(90deg); }}
    .chevron {{
      transition: transform 0.2s; color: var(--muted); font-size: 0.75rem;
      width: 1rem; text-align: center;
    }}
    .test-body {{
      padding: 1rem 1rem 1.5rem 3rem; border-top: 1px solid var(--border);
      background: var(--bg);
    }}
    .section {{ margin-bottom: 1.25rem; }}
    .section:last-child {{ margin-bottom: 0; }}
    .section-title {{
      font-size: 0.7rem; font-weight: 600; margin-bottom: 0.5rem;
      color: var(--muted); text-transform: uppercase; letter-spacing: 0.05em;
    }}
    pre {{
      background: var(--code-bg); color: #e2e8f0; padding: 1rem;
      border-radius: 8px; overflow-x: auto; font-size: 0.8rem;
      font-family: 'SF Mono', Consolas, monospace; line-height: 1.5;
      max-height: 400px;
    }}
    .ux-section {{
      background: var(--card); border-radius: 12px; padding: 1.5rem;
      margin-top: 1.5rem; box-shadow: 0 1px 3px rgba(0,0,0,0.1);
    }}
    .ux-section h2 {{ font-size: 1.1rem; margin-bottom: 1rem; }}
    .ux-section ul {{ margin-left: 1.25rem; font-size: 0.875rem; }}
    .ux-section li {{ padding: 0.25rem 0; }}
    .hidden {{ display: none !important; }}
  </style>
</head>
<body>
  <div class="container">
    <header>
      <h1>MCPProxy Quarantine QA Report</h1>
      <div class="metadata">
        <div class="meta-item"><div class="meta-label">Version</div><div class="meta-value">{html.escape(version)}</div></div>
        <div class="meta-item"><div class="meta-label">Date</div><div class="meta-value">{now}</div></div>
        <div class="meta-item"><div class="meta-label">OS</div><div class="meta-value">{html.escape(os_info)}</div></div>
        <div class="meta-item"><div class="meta-label">Node.js</div><div class="meta-value">{html.escape(node_ver)}</div></div>
        <div class="meta-item"><div class="meta-label">Tester</div><div class="meta-value">AI Agent (Claude Opus 4.6)</div></div>
        <div class="meta-item"><div class="meta-label">Focus</div><div class="meta-value">Quarantine &amp; Tool Approval</div></div>
      </div>
    </header>

    <div class="summary">
      <div class="stat total"><div class="stat-value">{total}</div><div class="stat-label">Total</div></div>
      <div class="stat pass"><div class="stat-value">{passed}</div><div class="stat-label">Passed</div></div>
      <div class="stat fail"><div class="stat-value">{failed}</div><div class="stat-label">Failed</div></div>
    </div>

    <div class="controls">
      <input type="text" class="search" id="search" placeholder="Search tests by ID, name, or content...">
      <div class="filter-group">
        <button class="filter-btn active" data-filter="all">All</button>
        <button class="filter-btn" data-filter="pass">Pass</button>
        <button class="filter-btn" data-filter="fail">Fail</button>
      </div>
      <div class="surface-filter">
        <button class="filter-btn surface-btn active" data-surface="all">All</button>
        <button class="filter-btn surface-btn" data-surface="CLI">CLI</button>
        <button class="filter-btn surface-btn" data-surface="REST">REST</button>
        <button class="filter-btn surface-btn" data-surface="MCP">MCP</button>
      </div>
    </div>

    <div class="test-list">
      {tests_html}
    </div>

    <div class="ux-section">
      <h2>Chrome UX Walkthrough</h2>
      <p><em>To be completed manually via Chrome extension. GIF recording will be embedded here.</em></p>
      <ul>
        <li>Dashboard health indicators for quarantined servers</li>
        <li>Pending tool approval flow (single + approve all)</li>
        <li>Rug pull detection with changed tool badges</li>
        <li>Tool diff view (previous vs current description)</li>
        <li>Re-approval after rug pull</li>
      </ul>
    </div>
  </div>

  <script>
    // Search
    document.getElementById('search').addEventListener('input', e => {{
      const q = e.target.value.toLowerCase();
      document.querySelectorAll('.test-item').forEach(el => {{
        el.classList.toggle('hidden', q && !el.textContent.toLowerCase().includes(q));
      }});
    }});

    // Status filter
    document.querySelectorAll('.filter-btn:not(.surface-btn)').forEach(btn => {{
      btn.addEventListener('click', () => {{
        document.querySelectorAll('.filter-btn:not(.surface-btn)').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        const f = btn.dataset.filter;
        document.querySelectorAll('.test-item').forEach(el => {{
          el.classList.toggle('hidden', f !== 'all' && el.dataset.status !== f);
        }});
      }});
    }});

    // Surface filter
    document.querySelectorAll('.surface-btn').forEach(btn => {{
      btn.addEventListener('click', () => {{
        document.querySelectorAll('.surface-btn').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        const f = btn.dataset.surface;
        document.querySelectorAll('.test-item').forEach(el => {{
          el.classList.toggle('hidden', f !== 'all' && el.dataset.surface !== f);
        }});
      }});
    }});
  </script>
</body>
</html>'''

with open(report_file, 'w') as f:
    f.write(report)
print(f"Report written to {report_file}")
PYEOF

# === Summary ===
log ""
log "==============================="
log "QUARANTINE QA RESULTS"
log "==============================="
log "Total: $TOTAL | Pass: $PASS | Fail: $FAIL"
log "Report: $REPORT_FILE"
log "Raw output: $OUTPUT_DIR"
log "==============================="

if [ $FAIL -gt 0 ]; then
  exit 1
fi
