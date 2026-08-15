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

Add fixture upstreams (QA harness ctl-server; DESC_FILE enables the rug-pull cell):

```bash
curl -s -X POST -H "X-API-Key: $API_KEY" localhost:18098/api/v1/servers \
  -d '{"name":"ctl","command":"node","args":["internal/testdata/ctl-server.js"],"protocol":"stdio","enabled":true}'
```

## 2. Happy path

```bash
./mcpproxy tools preflight ctl:echo ctl:add --output json ; echo "exit=$?"
# expect: verdict ready, exit=0, both tools status=ready
curl -s -X POST -H "X-API-Key: $API_KEY" localhost:18098/api/v1/preflight \
  -d '{"tools":[{"id":"ctl:echo"}]}' | jq .
```

## 3. Sabotage matrix (each cell → exact reason + exit code)

| Cell | Induce | Expect |
|---|---|---|
| server_disabled | `mcpproxy upstream disable ctl` | 11 / server_disabled / action enable |
| server_quarantined | quarantine via API | 11 / server_quarantined / approve |
| tool_pending_approval | add new tool on ctl (DESC_FILE) before approval | 11 / tool_pending_approval |
| tool_changed | rewrite tool description via DESC_FILE after approval | 11 / tool_changed |
| tool_blocked_by_user | `POST /servers/ctl/tools/block` | 11 / tool_blocked_by_user |
| tool_denied_by_config | add `disabled_tools:["ctl:echo"]` | 11 / tool_denied_by_config |
| oauth_required | fixture server in PendingAuth | 11 / oauth_required / login |
| server_unhealthy | SIGSTOP the ctl child | 10 / server_unhealthy |
| server_initializing | preflight immediately after enable | 10 / server_initializing |
| hash_mismatch | `--pin ctl:echo=sha256/v2:deadbeef` | 11 / hash_mismatch |
| missing_annotation | `--read-only-only` against ctl (no annotations) | 11 / missing_annotation |
| policy_filtered | `--exclude-destructive` vs tool with destructiveHint=true | 11 / policy_filtered |
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
