---
id: MCPX_HTTP_5XX
title: MCPX_HTTP_5XX
sidebar_label: HTTP 5xx
description: The MCP server responded with an HTTP 5xx status — upstream-side failure.
---

# `MCPX_HTTP_5XX`

**Severity:** warn
**Domain:** HTTP

## What happened

The upstream MCP server returned a 5xx status. mcpproxy classifies this as a
server-side problem rather than a misconfiguration. Common subcategories:

- `500 Internal Server Error` — uncaught exception in the upstream.
- `502 Bad Gateway` / `504 Gateway Timeout` — load balancer in front of the
  MCP server lost the backend.
- `503 Service Unavailable` — the upstream is rate-limiting or under
  maintenance.

## How to fix

### 1. Check the upstream's status page

If the vendor publishes a status page, that's the fastest signal. Try again
when they report the incident as resolved.

### 2. Retry with backoff

mcpproxy applies exponential backoff to upstream connections automatically. If
the failure is transient you'll see the server flip back to *Ready* without
intervention.

### 3. Look at the response body

```bash
mcpproxy activity show <id>
```

Many upstreams put a useful diagnostic in the body even on 5xx (request id,
correlation id) — quote that to upstream support.

### 4. Persistent 5xx on a self-hosted upstream

Check the server's own logs. The fix is on the upstream side; mcpproxy is just
a faithful messenger.

## Related

- [`MCPX_HTTP_CONN_REFUSED`](MCPX_HTTP_CONN_REFUSED.md) — connection rejected before HTTP
- [Activity Log](../features/activity-log.md)
