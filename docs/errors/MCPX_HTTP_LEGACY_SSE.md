---
id: MCPX_HTTP_LEGACY_SSE
title: MCPX_HTTP_LEGACY_SSE
sidebar_label: LEGACY_SSE
description: The endpoint rejected the streamable-HTTP initialize POST with a 4xx — the signature of a server that only speaks the older SSE transport.
---

# `MCPX_HTTP_LEGACY_SSE`

**Severity:** error
**Domain:** HTTP

## What happened

mcpproxy opened the connection with a streamable-HTTP `initialize` POST and the
endpoint answered with a 4xx. That combination is the signature of a server
implementing only the **older SSE transport**, which expects a `GET` to open an
event stream and a separate POST endpoint for messages — so it rejects the
streamable-HTTP handshake outright.

This is a **transport mismatch, not an authentication or routing failure**. It
is reported separately from `MCPX_HTTP_403` / `MCPX_HTTP_404` precisely because
those send you looking for a credential or URL problem that does not exist.

## How to fix

### Set the transport explicitly

```jsonc
{
  "name": "my-server",
  "protocol": "sse",                       // was "http" or "streamable-http"
  "url": "https://example.com/sse"
}
```

### Check the URL path

Legacy SSE servers usually publish the stream on a distinct path. If the
endpoint documents `/sse` and the config points at `/mcp` (or the bare origin),
correct the URL as well as the protocol.

### Let mcpproxy pick

Omitting `protocol`, or setting it to `auto`, makes mcpproxy negotiate — useful
when the server supports both and you do not want to pin a choice:

```jsonc
{ "name": "my-server", "protocol": "auto", "url": "https://example.com/mcp" }
```

### Confirm the change

```bash
mcpproxy upstream restart <server>
mcpproxy upstream logs <server> --follow
```

## Related

- [`MCPX_HTTP_404`](MCPX_HTTP_404.md) — the endpoint genuinely does not exist
- [`MCPX_HTTP_403`](MCPX_HTTP_403.md) — the endpoint exists but refuses the caller
- [`MCPX_HTTP_CONN_REFUSED`](MCPX_HTTP_CONN_REFUSED.md) — nothing is listening at all
