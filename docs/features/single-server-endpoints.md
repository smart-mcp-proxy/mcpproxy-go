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
  upstream server sent mcpproxy, and nothing of mcpproxy's, when they pass the
  checks under [Security](#security); otherwise it carries none.
- **Only its tools**, plus `describe_tool`, which takes and returns the same bare
  names.

mcpproxy still holds the server's credentials. For an OAuth server it signs in
once, stores the tokens in its database (`config.db`, encrypted when
[`oauth_token_encryption_key`](./oauth-token-encryption.md) is set) and refreshes
them for every client, so
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
- A caller whose effective profile does not reach the server (a token pinned to
  another profile, a client binding, or the anonymous profile) gets the same 404,
  even when its server allow-list is `*`.
- The upstream's instructions are forwarded only for a caller the endpoint admits
  and only while the server is enabled and not quarantined; they are text the
  upstream wrote, so they are held back from a server you have not approved.
  They are also scanned for tool-poisoning patterns each time and withheld (with
  a log line) when the scan flags them, cannot cover them, or they exceed 8 KiB.
- Session ids are bound to the endpoint that issued them: a session started on
  `/mcp/s/a` is refused (`404`) on `/mcp/s/b`, for every method.
- `describe_tool` resolves only this server's tools; its `did_you_mean`
  suggestions name only this server's tools.
- Tools that are pending approval or changed since approval are hidden from agent
  tokens and cannot be called by anyone, as on `/mcp/all`. An administrator
  credential keeps them in the operator listing, as on `/mcp/all`, but a call is
  refused before it reaches the upstream.
- Removing a server makes its endpoint answer 404 on the next request; replacing it
  re-registers the new server's tools and drops the old ones.
- Calls dispatch through the same handlers as `/mcp/all`, bound to the catalog
  entry rather than re-resolved by name.

## Behaviour

- The surface is created on the first request and reflects the published direct
  catalog. When the server's tools change, connected clients get
  `notifications/tools/list_changed`; changes to other servers do not notify them.
- A server that is configured but not connected lists only `describe_tool` until it
  connects.
- A server that sends no instructions gets an `initialize` response without any.
