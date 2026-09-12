## ⚡ `code_execution` is now on by default

The sandboxed JavaScript/TypeScript `code_execution` tool — one request that orchestrates several upstream tool calls, with loops, conditionals and data transformation in between — ships **enabled** starting with this release. Previously it had to be switched on by hand.

**What changes for you**

- **New installs** get `"enable_code_execution": true` in `mcp_config.json` and see the tool in `tools/list` on the default `/mcp` endpoint.
- **Existing installs keep their current setting.** mcpproxy writes every setting explicitly when it saves the config, so an older `mcp_config.json` almost certainly contains `"enable_code_execution": false`, and an explicit value always wins over the default. To get the tool, flip it to `true` in the file or in **Settings → Enable code execution tool** — the change is hot-reloaded and connected clients receive `notifications/tools/list_changed`, no restart needed.
- **To keep it off**, set `"enable_code_execution": false`. As of this release a disabled tool is no longer advertised at all ([#1236](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1236)): it disappears from `tools/list` instead of being listed as a stub that refuses every call.

The sandbox has no filesystem, network or `require()`; `call_tool` from inside a script goes through the same quarantine, read-only-mode and per-server restrictions as a direct `call_tool_*` request, and each run is bounded by `code_execution_timeout_ms` (default 2 min) and `code_execution_max_tool_calls`. Details: [docs/features/code-execution](https://docs.mcpproxy.app/features/code-execution/).

## 🔒 Agent-token scope hardening

Five fixes close the places where an agent token restricted to a subset of servers could still learn about, or act on, servers outside its grant: `call_tool_*` now requires the target tool's tier and fails closed on an unresolved one ([#1223](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1223)); `upstream_servers` `tail_log` ([#1224](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1224)), `set_profile` responses ([#1225](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1225)), `read_cache` entries ([#1226](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1226)) and aggregated prompts ([#1227](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1227)) are all scoped to the calling token. No configuration change is needed; if you hand out scoped agent tokens (`mcpproxy token create --servers …`), upgrade.

The registry SSRF guard now also unwraps IPv6 transition addresses (6to4, NAT64, Teredo, IPv4-compatible) and blocks the embedded IPv4 when it is private or link-local ([#1235](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1235), thanks @aroh3006).

## 🪟 Windows: stdio servers no longer leak child processes

Restarting, reconnecting or disconnecting a stdio server on Windows used to leave its `node.exe` / `python.exe` grandchildren running forever (one report counted 413 orphans after an hour). Every spawned server is now wrapped in a Job Object that is closed on every disconnect and stop path, so the whole tree goes with it ([#1234](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1234), thanks @LocoLoboZ; [#1260](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1260)). If you have orphans from earlier builds, end them once by hand — this release cannot reach processes it did not start.

## 🩺 "Token expired" on servers that no longer use OAuth

A server switched from OAuth to a static `Authorization` header stayed `unhealthy / Token expired / action: login` forever, because a stale record in the `oauth_tokens` bucket outranked the current config ([#1172](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1172)). Such servers now report from their live connection; the dead record is ignored (and `mcpproxy auth logout <server>` still removes it).
