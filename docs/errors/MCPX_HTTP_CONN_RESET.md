---
id: MCPX_HTTP_CONN_RESET
title: MCPX_HTTP_CONN_RESET
sidebar_label: CONN_RESET
description: The TCP connection to the MCP server was reset by the peer before a reply arrived.
---

# `MCPX_HTTP_CONN_RESET`

**Severity:** warn
**Domain:** HTTP

## What happened

The TCP connection was established and then reset by the other end
(`ECONNRESET`) before the response completed. Something between mcpproxy and
the MCP server tore the socket down mid-exchange.

This is different from [`MCPX_HTTP_CONN_REFUSED`](MCPX_HTTP_CONN_REFUSED.md),
where nothing accepted the connection in the first place. A reset means
something *was* listening.

Resets are transient by nature, so mcpproxy keeps retrying on its normal
backoff ladder. A single occurrence in the log is usually not worth chasing.

## Common causes

- An idle-timeout on a load balancer, reverse proxy or corporate VPN in front
  of the MCP server, killing a long-lived SSE stream.
- The upstream process crashed or was restarted mid-request (a deploy).
- A TLS-intercepting proxy that does not understand streaming responses.
- A NAT or firewall dropping the connection state for a long-lived stream.

## How to fix

### 1. Confirm the endpoint answers at all

```bash
curl -v --max-time 30 <server-url>
```

If curl also gets "Connection reset by peer" the problem is on the path, not in
mcpproxy.

### 2. Rule out an intercepting proxy

```bash
env | grep -i proxy
```

A corporate `HTTPS_PROXY` that buffers or rewrites responses is the most common
cause of resets on streaming MCP transports. Try the same URL from a network
without it.

### 3. If it repeats on a long-lived stream

The endpoint is probably behind an idle timeout shorter than the stream. For a
self-hosted upstream, raise the proxy's read/idle timeout. For a hosted one,
this is worth reporting to the vendor with the timing between connect and reset.

## Related

- [`MCPX_HTTP_CONN_REFUSED`](MCPX_HTTP_CONN_REFUSED.md) — nothing listening
- [`MCPX_NETWORK_OFFLINE`](MCPX_NETWORK_OFFLINE.md) — no route to the network
- [`MCPX_HTTP_5XX`](MCPX_HTTP_5XX.md) — the server answered, badly
