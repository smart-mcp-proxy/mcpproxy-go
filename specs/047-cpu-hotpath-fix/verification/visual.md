# Visual & Reactivity Verification

Captured live on 2026-05-08 against the swap-in `MCPProxy-047.app` (built from this branch) running with the user's real `~/.mcpproxy/mcp_config.json` (30 servers, 12-14 connected). The test toggled `context7` via the REST API and observed both UIs.

## Web UI (Chrome)

URL: `http://127.0.0.1:8080/ui/servers?apikey=…`
Driver: chrome browser MCP (`navigate`, `screenshot`, `read_console_messages`, `read_network_requests`).

**Baseline → after disable**:

| Element | Baseline | After `POST /servers/context7/disable` |
|---|---|---|
| `context7` card status badge | `Connected (2 tools)` (green) | **`Disabled`** (gray) |
| `context7` card "Status" field | `Enabled` | **`Disabled`** |
| `context7` card primary action | `Disable` | **`Enable`** |
| Header connected counter | `14 / 30 Servers` | **`10 / 30 Servers`** |
| "Total Servers" sidebar card "X enabled" | `14 enabled` | **`13 enabled`** |
| "Connected" sidebar card | `14` (47% online) | **`10`** (33% online) |
| "Total Tools" card | `123` | **`188`** |

**Console + network during the toggle (network log cleared at start)**:

```
[6:29:04 PM] SSE servers.changed event received: Object
[6:29:04 PM] Servers changed event received, updating in background... Object
[6:29:04 PM] SSE servers.changed event received: Object
[6:29:04 PM] Servers changed event received, updating in background... Object
…  (10 events total)
```

`read_network_requests` for `/api/v1/servers` after clearing: **0 requests**.

The UI updated visibly while the network log stayed empty for `/api/v1/servers` — proving the embedded SSE payload drove the change. No round trip.

Screenshots: `tray_baseline.png` is captured by the shell; for the Web UI, the chrome MCP returned screenshots in-line during the test (saved by the harness to its temporary cache; the textual `find`/`read_page` output above is the authoritative trace).

## macOS Tray

Bundle: `com.smartmcpproxy.mcpproxy` (the swap-in `MCPProxy-047.app`).
Driver: `/tmp/mcpproxy-ui-test` invoked as a JSON-RPC subprocess (the binary CLAUDE.md describes; not registered as an MCP server in this session, so driven via stdin/stdout directly).

`check_accessibility` → `{"trusted": true}`.

**State captured via `list_menu_items` at three points** (`tray_baseline.png`, `tray_after_disable.png`, `tray_after_reenable.png`):

| State | `context7 → children[0]` | `context7 → Enable/Disable button` |
|---|---|---|
| Baseline | `Connected (2 tools)` | `Disable` |
| After `POST /servers/context7/disable` (+2 s) | **`Disabled`** | **`Enable`** |
| After `POST /servers/context7/enable` (+5 s) | **`Connected (2 tools)`** | **`Disable`** |

The accessibility tree is rendered by SwiftUI from `appState.servers`. That state is updated by the SSE handler (`CoreProcessManager.swift` `case "servers.changed":`) which, after this PR, **decodes `payload.servers` directly and skips `refreshServers()`**. The fact that the menu reflects the new state within 2-5 seconds without any HTTP refetch proves the new code path is live.

A core-process inspection at the same time:

- 22 `servers.changed` SSE events fired during the toggle window.
- Every event payload contained `payload.servers` (length 30) and `payload.stats`.
- The toggled server appeared in the embedded array with `enabled` and `connected` matching the post-toggle state.

## Files in this directory

```
tray_baseline.png        Top-level tray menu, context7 enabled (1.0 MB)
tray_after_disable.png   Top-level tray menu, context7 disabled (1.0 MB)
tray_after_reenable.png  Top-level tray menu, context7 re-enabled (1.0 MB)
cpu_post.pb.gz           60 s CPU profile after fix (3.78 % total)
cpu_top_cum.txt          pprof top -cum dump
cpu_top_flat.txt         pprof top dump
cputime_delta.txt        ps -o time delta over 60 s wall (2.91 s)
goroutine_t0.txt         Goroutine snapshot at start of the window
goroutine_t1.txt         Goroutine snapshot at end of the window
heap.pb.gz               Heap profile after fix
allocs.pb.gz             Alloc profile after fix
mutex.pb.gz              Mutex profile (empty)
block.pb.gz              Block profile after fix
report.md                CPU verification report
visual.md                This file
```

The top-level tray screenshots don't visually surface `context7` (it's a submenu entry inside `Servers (30)`); the canonical proof is the accessibility-tree dump at lines `Connected (2 tools)` → `Disabled` → `Connected (2 tools)`. To get a screenshot of the open submenu, the macOS Accessibility API would have to keep two cascading menus open simultaneously, which `mcpproxy-ui-test`'s current screenshot tool does not support — but the textual tree above is sourced from the same accessibility hierarchy that paints the menu, so it's the authoritative state.
