---
title: Keep a growing tool catalog out of initial context
sidebar_label: Discover tools on demand
description: Compare direct and on-demand MCP tool exposure, then prove the agent still completes a real task.
---

# Keep a growing tool catalog out of initial context

When an agent connects to many MCP servers, decide whether it should receive every upstream definition at connection time or discover a relevant tool when needed. This guide uses MCPProxy's two dedicated endpoints to compare both routes on **your** catalog. You are done when a fresh client session finds and calls the intended tool through discovery, and you have recorded the initial `tools/list` size for each route.

## Before you start

Use **MCPProxy v0.71.1 or later**, one running instance, and at least one connected, approved upstream tool you can safely call. Install [uv](https://docs.astral.sh/uv/) for the optional measurement probe in step 2. Identify the tool's exact `server:tool` name and a harmless test request. Both comparisons must use the same proxy, client access scope, and upstream state. If you use a custom data directory, give the CLI the same `--config` and `--data-dir` options as the daemon.

```bash
mcpproxy status
mcpproxy tools list
mcpproxy tools preflight SERVER:TOOL
```

Replace `SERVER:TOOL` with your tool's real ID. Preflight should say `ready`; it checks local availability, not the result of an upstream call. If a tool is held or unavailable, [diagnose that first](/guides/diagnose-unavailable-tool).

## 1. Compare the two client endpoints

In **Clients → Endpoint & mode**, find your proxy's local base URL. Use its dedicated endpoints in two fresh client sessions:

| Endpoint | What a client initially sees | Call pattern |
| --- | --- | --- |
| `/mcp/call` | MCPProxy routing and management tools, including `retrieve_tools`; upstream definitions are requested later | Search with `retrieve_tools`, then use the returned `call_with` tool and exact `server:tool` ID |
| `/mcp/all` | Upstream definitions from connected, non-quarantined servers as direct `server__tool` names, alongside MCPProxy tools | Call the direct name; an access gate can still refuse it |

The configured `/mcp` endpoint follows your instance's `routing_mode`; the two dedicated endpoints work regardless of that setting. Keep the comparison to the same client identity/profile so access filtering is comparable. Direct mode can be convenient for a small known catalog; discovery adds a search step and depends on useful tool descriptions. See [Routing Modes](/features/routing-modes) for the full behavior.

## 2. Measure the initial tool list

Set `MCPPROXY_BASE` to the base URL shown by your local instance, **without** `/mcp`. This protocol-only probe uses Python's standard library and prints the number of listed tools, the JSON response size in bytes, and the sum of serialized `inputSchema` bytes. It does not call an upstream tool. If your local `/mcp` endpoint requires authentication, provide your operator key through `MCPPROXY_API_KEY` in the environment without pasting it into the script or a shared transcript.

```bash
export MCPPROXY_BASE=http://127.0.0.1:YOUR_PORT
uv run --no-project python - <<'PY'
import json, os, urllib.request

base = os.environ['MCPPROXY_BASE'].rstrip('/')
key = os.environ.get('MCPPROXY_API_KEY')
for path in ('/mcp/call', '/mcp/all'):
    session = None
    def request(number, method, params):
        global session
        headers = {'Content-Type': 'application/json',
                   'Accept': 'application/json, text/event-stream',
                   'MCP-Protocol-Version': '2025-03-26'}
        if key: headers['X-API-Key'] = key
        if session: headers['Mcp-Session-Id'] = session
        body = json.dumps({'jsonrpc': '2.0', 'id': number,
                           'method': method, 'params': params},
                          separators=(',', ':')).encode()
        with urllib.request.urlopen(urllib.request.Request(
                base + path, data=body, headers=headers), timeout=20) as r:
            session = r.headers.get('Mcp-Session-Id', session)
            raw = r.read()
        return json.loads(raw), len(raw)
    request(1, 'initialize', {'protocolVersion': '2025-03-26',
            'capabilities': {}, 'clientInfo': {'name': 'catalog-check', 'version': '1'}})
    result, payload_bytes = request(2, 'tools/list', {})
    tools = result['result']['tools']
    schema_bytes = sum(len(json.dumps(t.get('inputSchema', {}),
                         separators=(',', ':')).encode()) for t in tools)
    print(f'{path}: {len(tools)} tools; {payload_bytes} response bytes; '
          f'{schema_bytes} inputSchema bytes')
PY
```

**Expected:** `/mcp/call` has no individual upstream entries; `/mcp/all` includes upstream entries from the non-quarantined catalog. A listed direct tool can still fail an approval or policy gate when called; check real task success separately. The counts and byte totals depend on your configuration, access policy and release. These are **transport JSON bytes**, not model tokens, billed cost, or a prediction of whole-task context use. MCPProxy's own built-in tool schemas also contribute to both totals; the sums need not move in the same direction. A client may transform or cache definitions before sending them to a model. An operator-key probe may also see more than a restricted client.

## 3. Prove discovery completes the task

In a fresh session connected to `/mcp/call`, ask your agent:

> Use MCPProxy `retrieve_tools` to find `SERVER:TOOL` for this task: `YOUR HARMLESS TASK`. Use the returned exact name, current schema and `call_with` route to make one real call. Report the actual result and any tool error. Do not use a shell or web search instead.

**Expected:** the transcript shows `retrieve_tools`, then the matching `call_tool_read`, `call_tool_write`, or `call_tool_destructive` call, followed by a real upstream response. Search results may include multiple candidates; a precise query and `limit` narrow the response. If you use compact retrieval (`detail: "compact"`), fetch the full definition with `describe_tool` before supplying unfamiliar arguments. See the [MCP discovery response contract](/api/rest-api#mcp-tool-surface-compact-responses-and-describe_tool-spec-085).

Run the same harmless task in a fresh session connected to `/mcp/all`. That transcript should call `server__tool` directly. Compare whether **both actually succeed**, plus any extra search and definition requests. A smaller initial list is not a win if the agent fails to find or correctly call the tool.

## 4. Check the recorded call

```bash
mcpproxy activity list --server SERVER --tool TOOL --type tool_call --from=-15m
```

Match the actual arguments, result and session to each client run. If discovery returns no match, try the server name and a short verb from the tool description; confirm it is approved and visible to that client's profile. If a result appears but a call fails, inspect its current schema and the Activity error. Use [Find out why an agent cannot use a tool](/guides/diagnose-unavailable-tool) for connection, review, policy and upstream failures.

For a production choice, repeat the comparison with several representative tasks and your usual client. Initial `tools/list` bytes, retrieval response bytes, task success and client behavior are separate measurements. There is no universal token, latency or cost saving from switching modes.

*Validation: 22 October 2026, MCPProxy v0.71.1 on macOS. Two protocol-only local upstreams each defined 24 tools; one allowed only its echo tool. The probe saw 14 tools / 37,419 JSON bytes on `/mcp/call` and 49 tools / 40,380 bytes on `/mcp/all`; their `inputSchema` sums were 22,289 and 3,900 bytes respectively. Compact one-result retrieval was 1,650 bytes. Both routing paths completed an echo call after readiness. These are fixture measurements, not a benchmark for other catalogs or client model context. Maintained by the MCPProxy maintainers.*
