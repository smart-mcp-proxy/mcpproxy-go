---
id: MCPX_HTTP_404
title: MCPX_HTTP_404
sidebar_label: HTTP 404
description: The configured MCP endpoint URL returned 404 Not Found.
---

# `MCPX_HTTP_404`

**Severity:** error
**Domain:** HTTP

## What happened

The hostname resolved and TLS succeeded, but the configured URL path returned
404 Not Found.

## Common causes

- The URL points at the server's homepage (`/`) instead of the MCP endpoint
  (often `/mcp` or `/sse`).
- The vendor moved the MCP endpoint between versions.
- The URL has a trailing-slash mismatch the upstream is strict about.
- The upstream removed the MCP integration.

## How to fix

### Verify the path

```bash
curl -sS -i -H 'Accept: text/event-stream' '<server-url>'
```

A working MCP HTTP/SSE endpoint replies with `200` and a content-type containing
`text/event-stream` (SSE) or `application/json` (HTTP transport).

### Check vendor docs

Most vendors document the canonical MCP path. Common shapes:

- `https://api.example.com/mcp`
- `https://example.com/sse`
- `https://example.com/v1/mcp`

Update the `url` field in your upstream entry and restart that server:

```bash
mcpproxy upstream restart <server-name>
```

## Related

- [`MCPX_HTTP_CONN_REFUSED`](MCPX_HTTP_CONN_REFUSED.md)
- [Configuration → upstream servers](../configuration/upstream-servers.md)
