# Spec 048 Verification Report

**Date**: 2026-05-08
**Branch**: `048-tray-refetch-elimination`
**Scenario**: swap-in `MCPProxy-048.app` (this branch's tray binary + v0.29.4 core) running against the user's real `~/.mcpproxy/mcp_config.json` (30 servers, ~14 connected).
**Tray bundle**: `com.smartmcpproxy.mcpproxy`.

## Idle: `/api/v1/servers` GETs over 60 s

Method: started the swap-in app, waited until 5 s elapsed with no new
`/api/v1/servers` GET in `~/Library/Logs/mcpproxy/http.log` (i.e., the initial
startup fetches had drained), then sampled the next 60 s.

```
window: 2026-05-08T19:56:18 -> 2026-05-08T19:57:18
=== /api/v1/servers GETs in that window ===
=== count ===
GETs: 0
```

**0 GETs / 60 s.** Beats the spec's `≤ 1 / 60 s` acceptance.

For comparison, the same scenario on spec 047 alone (PR #450) showed roughly
**~8 GETs in 18 s** — so spec 048 takes the residual to zero at idle.

## Reactivity: tray reflects state changes via SSE only

Driver: `mcpproxy-ui-test` MCP binary in stdio mode reading the tray's
accessibility tree before/after a REST toggle. http.log audited for the same
window.

```
T0: 2026-05-08T19:57:45  context7 baseline:
  status: Connected (2 tools)
  btn:    Disable

POST /api/v1/servers/context7/disable  19:57:47
  t=1s tray says: Disabled                     ← propagated in 1 s

POST /api/v1/servers/context7/enable   19:57:49
  t=1s tray says: Connected
  t=2s tray says: Connected (2 tools)          ← propagated in 2 s

/api/v1/servers GETs during the toggle window
(2026-05-08T19:57:45 -> 2026-05-08T19:57:54): TOTAL: 0
```

Tray reactivity unchanged from spec 047, with **zero refetches** during the
toggle. SSE-driven `appState.servers` is the single source of truth.

## Net effect by call site

| Site | Before | After |
|---|---|---|
| `case "status":` SSE handler | refetch on every connected_count flip (1-3 s under retry storms) | stat-only update; no refetch |
| `refreshState` 30 s tick | 1 fetch every 30 s | 0 |
| `refreshSecurityStatus` Docker fallback | 1 fetch per Docker-fallback path | 0 — reads `appState.servers` |
| `MCPProxyApp.swift` 10 s timer | 1 fetch every 10 s | replaced with 5 min safety-net |
| `menuWillOpen` | 1 fetch per tray-icon click | 0 |
| Combined idle rate (this scenario) | ~8 GETs / 60 s | **0 GETs / 60 s** |

## Out of scope (deferred)

- `refreshActivity`, `refreshTokenMetrics`, `refreshSessions` periodics still hit their respective endpoints every 30 s. SSE doesn't cover those domains today.
- Docker `dockerStatus()` and `diagnostics()` calls inside `refreshSecurityStatus` are unchanged.

## Reproduction

See [`../quickstart.md`](../quickstart.md). One-liner for the idle GET count:

```bash
LOG=~/Library/Logs/mcpproxy/http.log; T0=$(date +%FT%H:%M:%S); sleep 60; T1=$(date +%FT%H:%M:%S)
grep '"path": "/api/v1/servers"' "$LOG" | awk '{print $1}' | awk -v lo="$T0" -v hi="$T1" '$0 >= lo && $0 <= hi' | wc -l
```
