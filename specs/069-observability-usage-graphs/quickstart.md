# Quickstart: Observability Usage Graphs

## Build & run

```bash
make build                      # backend + embedded frontend (frontend changes need this)
./mcpproxy serve --config=/tmp/mcpproxy-uitest/mcp_config.json --listen=127.0.0.1:18081
```

## Verify the backend endpoint

```bash
# After driving some tool calls through the proxy:
curl -s -H "X-API-Key: <key>" "http://127.0.0.1:18081/api/v1/activity/usage?window=24h&sort=resp_bytes" | jq
curl -s -H "X-API-Key: <key>" "http://127.0.0.1:18081/api/v1/activity/usage?window=all&server=github" | jq
```

Expect: per-tool rollup ordered by total response bytes, `token_source:"bytes"`, a `tokens_saved` headline, a `timeline[]`, and (with >`top` tools) an `other` bucket. Empty log → `tools:[]`, no error.

## Tests

```bash
go test ./internal/storage/... ./internal/runtime/... ./internal/httpapi/... -race   # unit + API
./scripts/test-api-e2e.sh
./scripts/verify-oas-coverage.sh                                                     # swagger coverage
# When touching ActivityRecord/approval-adjacent code, run the full runtime suite (approval-hash canary).
```

## Web UI verification (FR-011)

Follow the Playwright sweep pattern in `CLAUDE.md` → "Verifying Web UI changes": fresh mcpproxy on a throwaway data-dir, drive the Dashboard Overview↔Usage switcher + window selector, snapshot each chart state, build the self-contained HTML report under `specs/069-observability-usage-graphs/verification/`. Keep the report + screenshots **local** (do not commit QA reports — repo convention).

## Performance check (SC-005)

Confirm the `/usage` handler reads the snapshot/TTL cache and does not call `AggregateToolUsage` (full scan) per request — only on cold start. A large synthetic log should not change p95 of the endpoint materially.
