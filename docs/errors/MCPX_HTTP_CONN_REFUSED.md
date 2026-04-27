---
id: MCPX_HTTP_CONN_REFUSED
title: MCPX_HTTP_CONN_REFUSED
sidebar_label: CONN_REFUSED
description: TCP connection to the MCP server was refused.
---

# `MCPX_HTTP_CONN_REFUSED`

**Severity:** error
**Domain:** HTTP

## What happened

DNS resolved the upstream hostname, but the TCP connection to the resolved
address+port was refused (`ECONNREFUSED`). Nothing is listening, or a firewall
rejected the SYN.

## Common causes

- The upstream MCP server is not running.
- Wrong port in the URL (e.g. `https://localhost/mcp` without `:8080`).
- The upstream is bound to a different interface (e.g. `127.0.0.1` only, but
  mcpproxy connects from another network namespace).
- Local firewall (`pf`, `ufw`, `iptables`, Windows Defender Firewall).
- Docker container exposing the port wasn't started.

## How to fix

### Verify the port is open

```bash
nc -vz <host> <port>
curl -v <server-url>
```

If `nc` reports "Connection refused", nothing is listening. Start the upstream.

### For self-hosted upstreams

```bash
docker ps                 # is the container running?
ss -tlnp | grep <port>    # who's bound to that port?
systemctl status <unit>   # is the service up?
```

### Switch to localhost-aware binding

If the upstream binds only to `0.0.0.0` but you're connecting via `localhost`,
make sure the URL uses an address that's actually listening.

## Related

- [`MCPX_HTTP_DNS_FAILED`](MCPX_HTTP_DNS_FAILED.md)
- [`MCPX_NETWORK_OFFLINE`](MCPX_NETWORK_OFFLINE.md)
