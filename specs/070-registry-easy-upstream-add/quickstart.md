# Quickstart / Verification: Registry Easy Upstream-Add

**Feature**: 070-registry-easy-upstream-add · **Date**: 2026-05-31
Per-surface manual verification that the search→add loop closes and stays consistent. Use a throwaway data-dir (memory: app is config.db-authoritative — never touch real `~/.mcpproxy`).

## Setup
```bash
make build   # embeds frontend
rm -rf /tmp/reg70 && mkdir -p /tmp/reg70
cat > /tmp/reg70/mcp_config.json <<'EOF'
{ "listen": "127.0.0.1:18070", "data_dir": "/tmp/reg70", "api_key": "reg70",
  "enable_web_ui": true, "enable_socket": true, "telemetry": {"enabled": false},
  "mcpServers": [] }
EOF
./mcpproxy serve --config=/tmp/reg70/mcp_config.json --log-level=info &
until curl -sf -H "X-API-Key: reg70" http://127.0.0.1:18070/api/v1/status >/dev/null; do sleep 1; done
```

## US2 — CLI (list → search → add)
```bash
./mcpproxy registry list --config=/tmp/reg70/mcp_config.json            # shows merged registries (FR-006)
./mcpproxy registry search github --registry pulse -o json              # normalized results
./mcpproxy registry add pulse <serverId> --name gh-test                 # NEW — closes loop
./mcpproxy upstream list --config=/tmp/reg70/mcp_config.json            # gh-test present, QUARANTINED
```
**Pass**: `gh-test` appears quarantined; `registry add` printed the approve hint.

## US3 — MCP (add by reference)
```bash
# via tools/call REST shim or an MCP client:
curl -s -H "X-API-Key: reg70" -X POST http://127.0.0.1:18070/api/v1/tools/call \
  -d '{"tool_name":"upstream_servers","arguments":{"operation":"add_from_registry","registry":"pulse","id":"<serverId>","name":"gh-mcp"}}'
```
**Pass**: returns the server with `quarantined:true`; no hand-built command/args needed.

## US1 — Web UI (search → one-click add → prompt)
```
open "http://127.0.0.1:18070/ui/?apikey=reg70"  → Repositories tab
```
- Search `github`, click **Add to MCP** on a result.
- If the result declares required inputs, a prompt appears (FR-003) before add.
- Server appears in the list, **quarantined**.
**Pass**: no manual command re-entry; server quarantined; backend (not client JS) derived the config.

## US4 — resilience
```bash
# FR-007 freshness + manual refresh:
curl -s -H "X-API-Key: reg70" http://127.0.0.1:18070/api/v1/registries/pulse/servers?q=git | jq .cache
curl -s -H "X-API-Key: reg70" -X POST http://127.0.0.1:18070/api/v1/registries/pulse/refresh
# FR-008 key-absent: a RequiresKey registry with no key → marked unavailable, others still return.
```

## SC-004 — cross-surface consistency (the keystone)
```bash
go test ./internal/server/ -run TestAddFromRegistry_CrossSurfaceConsistency -race -v
```
**Pass**: same `(registry, serverId, env, name)` via REST/MCP/CLI → byte-identical persisted `ServerConfig` (modulo `Created`), all quarantined.

## Regression gates (must pass before pre-merge)
```bash
./scripts/run-linter.sh
go test ./internal/... -race
go test ./internal/runtime/... -race   # approval-hash stability canary (memory)
./scripts/test-api-e2e.sh
# Playwright: e2e/playwright registry-add.spec.ts (data-test attrs added on Repositories.vue/AddServerModal.vue)
```
