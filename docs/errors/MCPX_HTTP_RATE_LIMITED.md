---
id: MCPX_HTTP_RATE_LIMITED
title: MCPX_HTTP_RATE_LIMITED
sidebar_label: RATE_LIMITED
description: The MCP server returned 429 Too Many Requests — mcpproxy is being rate-limited.
---

# `MCPX_HTTP_RATE_LIMITED`

**Severity:** warn
**Domain:** HTTP

## What happened

The upstream MCP server answered with `429 Too Many Requests`. It is not a
configuration problem: the request was understood and refused because of volume.

mcpproxy already reads the `Retry-After` header on this response and holds off
reconnecting until the window the server asked for has passed (capped at one
hour, so a bogus hint self-heals). You do not need to do anything for a
short-lived burst.

## Common causes

- An agent looping over a tool faster than the vendor's quota allows.
- A shared API key: your quota is being spent by another client, another
  machine, or a teammate.
- A free-tier or trial key with a low ceiling.
- Several mcpproxy instances (laptop + server) pointed at the same upstream
  with the same credential.

## How to fix

### 1. Read the wait the server is asking for

```bash
curl -sS -o /dev/null -D - <server-url>
```

Look for `Retry-After` and any vendor-specific `X-RateLimit-*` headers. They
tell you whether this is a per-second burst limit or a daily quota.

### 2. Find what is spending the quota

```bash
mcpproxy activity list --server <name>
```

A tight retry loop in an agent shows up here as the same tool repeated at high
frequency.

### 3. If the limit is a daily quota

Nothing local will clear it — the server will keep answering 429 until the
window rolls over. Use a separate credential for automation, or ask the vendor
to raise the ceiling.

## Related

- [`MCPX_HTTP_5XX`](MCPX_HTTP_5XX.md) — server-side failure rather than a limit
- [`MCPX_HTTP_4XX`](MCPX_HTTP_4XX.md) — other client-side rejections
- [Activity Log](../features/activity-log.md)
