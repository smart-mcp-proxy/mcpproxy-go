---
id: MCPX_HTTP_4XX
title: MCPX_HTTP_4XX
sidebar_label: HTTP 4xx
description: The MCP server rejected the request with a 4xx status that has no more specific code.
---

# `MCPX_HTTP_4XX`

**Severity:** error
**Domain:** HTTP

## What happened

The upstream MCP server rejected the request with a client-error status that
mcpproxy does not map to a more specific code. The exact status is in the error
detail shown next to the code in the web UI, the tray, and
`mcpproxy upstream list`.

This code covers:

| Status | Usual meaning for an MCP endpoint |
| --- | --- |
| `400 Bad Request` | The endpoint is not an MCP endpoint, or it speaks a protocol version this client does not. |
| `408 Request Timeout` | The server gave up waiting for the request body — usually a slow or interrupted network. |
| `409 Conflict` | A session or state conflict; often a stale session id after the server restarted. |
| `410 Gone` | The endpoint was retired. The vendor has moved or shut down the URL. |
| `451 Unavailable For Legal Reasons` | Blocked in your region or for your account. |

`401`, `403`, `404`, `429` and every `5xx` have their own codes — see *Related*.

## How to fix

### 1. Reproduce it and read the body

```bash
curl -v <server-url>
```

Most MCP servers put a useful sentence in the response body. That sentence is
almost always the actual fix.

### 2. Check the configured URL

```bash
mcpproxy upstream list -o json
```

A `400` or `410` on a URL that used to work is nearly always the vendor moving
the endpoint — compare against their current documentation.

### 3. For a `409`

Restart the connection so a fresh session is negotiated:

```bash
mcpproxy upstream restart <server>
```

## Related

- [`MCPX_HTTP_401`](MCPX_HTTP_401.md) — authentication required
- [`MCPX_HTTP_403`](MCPX_HTTP_403.md) — authenticated but not allowed
- [`MCPX_HTTP_404`](MCPX_HTTP_404.md) — wrong path
- [`MCPX_HTTP_RATE_LIMITED`](MCPX_HTTP_RATE_LIMITED.md) — 429
- [`MCPX_HTTP_LEGACY_SSE`](MCPX_HTTP_LEGACY_SSE.md) — a 4xx that means "use the SSE transport"
