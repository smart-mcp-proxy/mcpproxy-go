# Quickstart: Required-Tools Preflight (098)

Local verification walkthrough — isolated dev instance per repo convention (high port, scratch `--data-dir` **and** `--config`; never the default ~/.mcpproxy, and don't rely on the e2e script's pkill).

## 1. Build & start isolated instance

```bash
go build -o mcpproxy ./cmd/mcpproxy
SCRATCH=$(mktemp -d)
./mcpproxy serve --listen 127.0.0.1:18098 \
  --data-dir "$SCRATCH/data" --config "$SCRATCH/config.json" \
  --log-level=debug &
API_KEY=$(jq -r .api_key "$SCRATCH/config.json")
```

Add a fixture upstream. The repo ships a zero-dependency stdio fixture at
`internal/server/testdata/preflight_fixture_server.js` (the one the sabotage E2E
uses). It serves whatever `FIXTURE_TOOLS_FILE` points at — re-read on every
tools/list, which is what makes the rug-pull and new-tool cells reproducible —
and has two more knobs: `FIXTURE_INIT_DELAY_MS` (parks the proxy in
connecting/discovering) and `FIXTURE_FAIL_FILE` (exists at startup ⇒ the process
exits, so a failure sticks across reconnects).

```bash
cat > "$SCRATCH/tools.json" <<'EOF'
[
  {"name": "echo", "description": "echo back", "inputSchema": {"type": "object"}},
  {"name": "wipe", "description": "dangerous cleanup", "inputSchema": {"type": "object"},
   "annotations": {"destructiveHint": true}}
]
EOF
curl -s -X POST -H "X-API-Key: $API_KEY" localhost:18098/api/v1/servers \
  -d "{\"name\":\"ctl\",\"command\":\"node\",
       \"args\":[\"$PWD/internal/server/testdata/preflight_fixture_server.js\"],
       \"env\":{\"FIXTURE_TOOLS_FILE\":\"$SCRATCH/tools.json\"},
       \"protocol\":\"stdio\",\"enabled\":true}"
```

## 2. Happy path

```bash
./mcpproxy tools preflight ctl:echo ctl:wipe --output json ; echo "exit=$?"
# expect: verdict ready, exit=0, both tools status=ready
curl -s -X POST -H "X-API-Key: $API_KEY" localhost:18098/api/v1/preflight \
  -d '{"tools":[{"id":"ctl:echo"}]}' | jq .
```

## 3. Sabotage matrix (each cell → exact reason + exit code)

| Cell | Induce | Expect |
|---|---|---|
| server_disabled | `mcpproxy upstream disable ctl` | 11 / server_disabled / action enable |
| server_quarantined | quarantine via API | 11 / server_quarantined / approve |
| tool_pending_approval | append a new tool to `$SCRATCH/tools.json`, restart ctl (kill the child; the proxy reconnects) | 11 / tool_pending_approval |
| tool_changed | rewrite `echo`'s description in `$SCRATCH/tools.json`, restart ctl | 11 / tool_changed |
| tool_blocked_by_user | `POST /servers/ctl/tools/block` | 11 / tool_blocked_by_user |
| tool_denied_by_config | add `disabled_tools:["ctl:echo"]` | 11 / tool_denied_by_config |
| oauth_required | fixture server in PendingAuth | 11 / oauth_required / login |
| server_unhealthy | `touch "$SCRATCH/fail"` (with `FIXTURE_FAIL_FILE=$SCRATCH/fail` in the server env), then kill the ctl child — the failure sticks across reconnects | 10 / server_unhealthy |
| server_initializing | add the server with `FIXTURE_INIT_DELAY_MS=8000` in env and preflight immediately | 10 / server_initializing |
| hash_mismatch | `--pin ctl:echo=sha256/v2:deadbeef` | 11 / hash_mismatch |
| missing_annotation | `--read-only-only ctl:echo` (no annotations on echo) | 11 / missing_annotation |
| policy_filtered | `--exclude-destructive ctl:wipe` (explicit destructiveHint:true) | 11 / policy_filtered |
| not_found | `ctl:nope` | 12 / not_found (+did_you_mean) |
| server_not_configured | `ghost:echo` | 12 / server_not_configured |
| scope tiers | profile excluding ctl: operator → server_not_in_scope; agent token → not_found | 11 vs 12, byte-indistinguishable not_found |

## 4. Transparency check

```bash
RID=$(curl -si -X POST -H "X-API-Key: $API_KEY" localhost:18098/api/v1/preflight \
  -d '{"tools":[{"id":"ctl:echo"}]}' | awk -F': ' '/X-Request-Id/{print $2}' | tr -d '\r')
./mcpproxy activity list --request-id "$RID"   # expect kind=preflight record with verdict
```

Open Web UI → Activity: the preflight record renders with its verdict.

## 5. MCP-surface & code-exec non-regression

```bash
go test ./internal/server -run ToolsListSnapshot   # byte-identical across 3 routing modes
# run a code_execution script + a stored script (spec 097) against the instance; behavior unchanged
```

## 6. Full gates before push

```bash
go test -race ./internal/... && go test -tags server ./internal/serveredition/... -race
./scripts/test-api-e2e.sh
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...
make swagger && git diff --exit-code oas/ frontend/src/types/contracts.ts
```
