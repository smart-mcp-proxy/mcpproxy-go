---
id: client-requests
title: Upstream Requests to the Client
sidebar_label: Elicitation, Sampling & Roots
description: How upstream servers ask the user, the client's model or the client's roots through MCPProxy
keywords: [elicitation, sampling, roots, server-initiated requests, approval]
---

# Upstream Requests to the Client

An MCP server can ask its client for something in the middle of a tool call:

- **Elicitation** (`elicitation/create`): ask the user a question or for approval, for example before a destructive command.
- **Sampling** (`sampling/createMessage`): ask the client's model to generate text.
- **Roots** (`roots/list`): ask which project folders the client is working in.

MCPProxy passes these requests to the MCP client that made the tool call and returns its answer to the upstream server. It works on every call surface: `call_tool_*`, the direct `server__tool` names, `/mcp/s/<server>` and `code_execution`.

## What upstream servers see

MCPProxy declares `elicitation` (form and URL modes), `sampling` and `roots` when it connects to a stdio or streamable-HTTP upstream. Legacy SSE upstreams cannot send requests to their client, so they get none.

These requests exist on protocol revisions up to `2025-11-25`, which MCPProxy uses on every upstream connection.

## Who is asked

One upstream connection is shared by every client of MCPProxy, so only the client whose call the request belongs to is asked:

- A streamable-HTTP upstream that sends the request on the call's own response stream is matched to that call.
- A request from a stdio upstream, or one sent on a streamable-HTTP upstream's standalone stream, carries no call. It goes to the client of the only call in flight to that server. With several calls in flight to the same server, nobody is asked.

The client must have declared the capability (and, for elicitation, the requested mode). A client on protocol `2026-07-28` or later cannot receive these requests.

## When nobody can be asked

The upstream gets an immediate answer instead of waiting:

| Request | Answer |
|---|---|
| Elicitation | `decline` |
| Sampling | an error |
| Roots | an empty list |

This happens for calls from the REST API or the CLI, requests sent outside a tool call, concurrent calls to a stdio server, and clients without the capability.

## Limits

- Roots requested outside a tool call (for example right after `initialize`) get an empty list, and changes to the client's roots are not forwarded.
- The tool call's timeout (`call_tool_timeout`, default 2 minutes) bounds how long the user has to answer.
- For a request a streamable-HTTP upstream sends on the call's own response stream, the MCP library MCPProxy uses allows 30 seconds for the answer.
