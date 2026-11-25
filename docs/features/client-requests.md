---
id: client-requests
title: Upstream Requests to the Client
sidebar_label: Elicitation, Sampling & Roots
description: How upstream servers ask the user, the client's model or the client's roots through MCPProxy
keywords: [elicitation, sampling, roots, server-initiated requests, approval, enable_client_features, client_features, client_request]
---

# Upstream Requests to the Client

An MCP server can ask its client for something in the middle of a tool call:

- **Elicitation** (`elicitation/create`): ask the user a question or for approval, for example before a destructive command.
- **Sampling** (`sampling/createMessage`): ask the client's model to generate text.
- **Roots** (`roots/list`): ask which project folders the client is working in.

MCPProxy passes these requests to the MCP client that made the tool call and returns its answer to the upstream server. It works on every call surface: `call_tool_*`, the direct `server__tool` names, `/mcp/s/<server>` and `code_execution`.

The message of an elicitation starts with the server's name, like `[ssh] Confirm destructive command…`, so the user can tell which server is asking.

## Turning it on

It is off until you list, per server, what that server may use:

```json
{ "mcpServers": [ { "name": "ssh", "command": "ssh-mcp", "client_features": ["elicitation"] } ] }
```

The values are `elicitation`, `sampling` and `roots`. A server is offered only the ones it lists, and a quarantined server is offered none. Changing `client_features` reconnects the server.

To turn it off for every server at once, whatever they list (takes effect after a restart):

```json
{ "enable_client_features": false }
```

## What upstream servers see

When it connects to a stdio or streamable-HTTP upstream, MCPProxy declares the features listed in `client_features`: `elicitation` (form and URL modes), `sampling` and `roots` (with `listChanged`). Legacy SSE upstreams cannot send requests to their client, so they get none.

These requests exist on protocol revisions up to `2025-11-25`, which MCPProxy uses on every upstream connection.

## Who is asked

One upstream connection is shared by every client of MCPProxy, so only the client whose call the request belongs to is asked:

- A streamable-HTTP upstream that sends the request on the call's own response stream is matched to that call.
- A request from a stdio upstream, or one sent on a streamable-HTTP upstream's standalone stream, carries no call. It goes to the client of the only call in flight to that server. With several calls in flight to the same server, nobody is asked.

The client must have declared the capability (and, for elicitation, the requested mode). A client on protocol `2026-07-28` or later cannot receive these requests.

**Roots outside a call.** Servers often ask for roots when they connect or when told the roots changed, outside any call. Such a request goes to the last client that called that server. When a different client calls the server, or the last client reports that its roots changed, MCPProxy sends the server `notifications/roots/list_changed`, so it asks again.

**URL-mode elicitation.** For one the user accepted, the server's `notifications/elicitation/complete` goes to the same client, even after the tool call has returned. MCPProxy waits up to an hour for it.

## When nobody can be asked

The upstream gets an immediate answer instead of waiting:

| Request | Answer |
|---|---|
| Elicitation | `decline` |
| Sampling | an error |
| Roots | an empty list |

This happens for calls from the REST API or the CLI (roots then go to the last client that called the server), requests sent outside a tool call, concurrent calls to a stdio server, and clients without the capability. The upstream cannot tell a `decline` given here from the user's own.

At most 4 requests of one server are answered at a time. The rest are refused at once (elicitation `decline`, otherwise an error) and not recorded.

## Activity log

Every request is recorded as a `client_request` activity: the server, the method, what the server asked (the first 4 KB, scanned for sensitive data) and whether the client was asked (`metadata.outcome`: `forwarded`, `unanswered` or `failed`). What the user, the model or the client answered is never stored, only its shape: the elicitation action with field names and sizes, the size of the sampling response, and the number of roots. Only administrators can read these rows.

```bash
mcpproxy activity list --type client_request
```

## Limits

- The tool call's timeout (`call_tool_timeout`, default 2 minutes) bounds how long the user has to answer. A prompt left open past it stays on the client's screen; answering it then changes nothing.
- For a request a streamable-HTTP upstream sends on the call's own response stream, the MCP library MCPProxy uses allows 30 seconds for the answer.
- Sampling requests are not marked with the server's name: clients show MCPProxy as the requester.
