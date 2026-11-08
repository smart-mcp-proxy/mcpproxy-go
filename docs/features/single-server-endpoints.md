---
id: single-server-endpoints
title: Single-Server Endpoints
sidebar_label: Single-Server Endpoints
sidebar_position: 3
description: Serve one upstream server at its own URL, with its own tool names and instructions
keywords: [single server, per server, endpoint, direct, tool names, instructions, oauth]
---

# Single-Server Endpoints

`/mcp/s/<server>` serves **one** upstream server at its own URL. The client sees
that server exactly as if it had connected to it directly:

- **Its own tool names.** `get_story`, not `shortcut__get_story`, and descriptions
  without the `[server]` prefix.
- **Its own instructions.** The `initialize` response carries the instructions the
  upstream server sent mcpproxy, and nothing of mcpproxy's.
- **Only its tools**, plus `describe_tool`, which takes and returns the same bare
  names.

mcpproxy still holds the server's credentials. For an OAuth server it signs in
once, stores the tokens in the OS keyring and refreshes them for every client, so
many concurrent agent sessions share one grant instead of each racing to rotate
its own refresh token.

## When to use it

Use it when you want mcpproxy's credential handling without changing what your
agents see. Register each server under its usual name:

```json
{
  "mcpServers": {
    "notion":   { "type": "http", "url": "http://127.0.0.1:8080/mcp/s/notion",   "headers": { "Authorization": "Bearer mcp_agt_..." } },
    "shortcut": { "type": "http", "url": "http://127.0.0.1:8080/mcp/s/shortcut", "headers": { "Authorization": "Bearer mcp_agt_..." } }
  }
}
```

Tools keep the names your client already uses for them (in Claude Code,
`mcp__notion__notion-fetch`), so permission rules, skills and prompts that name
them keep working, and each server stays a separate entry the client can enable,
disable or reconnect on its own.

Compared with the other surfaces:

| Endpoint | What a client sees |
|----------|--------------------|
| `/mcp/call` | `retrieve_tools` plus `call_tool_*`; upstream tools are found by search |
| `/mcp/all` | every server's tools as `server__tool`, one combined server |
| `/mcp/p/<slug>` | a profile's servers through `retrieve_tools` |
| `/mcp/s/<server>` | one server's tools under their own names, with its own instructions |

## Security

The endpoint sits behind the same authentication, host validation and
`require_mcp_auth` setting as every other MCP route, and runs the same discovery
filters as `/mcp/all`:

- An agent token must be allowed the server. Otherwise, and for a server that does
  not exist, the endpoint answers `404 Server not found`, so it cannot be used to
  probe which servers are configured.
- The token's permission tiers, profile policy and tool quarantine apply exactly as
  on `/mcp/all`: a read-only token does not see write or destructive tools.
- Calls dispatch through the same handlers as `/mcp/all`, bound to the catalog
  entry rather than re-resolved by name.

## Behaviour

- The surface is created on the first request and reflects the published direct
  catalog. When the server's tools change, connected clients get
  `notifications/tools/list_changed`; changes to other servers do not notify them.
- A server that is configured but not connected lists only `describe_tool` until it
  connects.
- A server that sends no instructions gets an `initialize` response without any.
