---
id: MCPX_NETWORK_OFFLINE
title: MCPX_NETWORK_OFFLINE
sidebar_label: OFFLINE
description: The host appears to have no network connectivity.
---

# `MCPX_NETWORK_OFFLINE`

**Severity:** error
**Domain:** Network

## What happened

mcpproxy classified a connection failure as "host appears offline" — typically
DNS resolution failed for a stable, well-known host *and* TCP probes all
errored with "no route" / "network unreachable".

## How to fix

### Confirm

```bash
ping -c 2 1.1.1.1
ping -c 2 8.8.8.8
dig +short cloudflare.com
```

If all three fail, you really are offline.

### Reconnect

- Wi-Fi: forget + rejoin, or toggle airplane mode.
- VPN: reconnect (or disconnect, if the VPN itself is the cause).
- Cellular hotspot: usually a quick toggle.

### Check captive portals

Public Wi-Fi often hijacks DNS until you accept a portal. Open
`http://neverssl.com` in a browser to be redirected to the portal.

### Mobile / low-bandwidth

If the network is reachable but very slow, mcpproxy may classify slow operations
as offline because of strict timeouts. Increase per-server timeouts as needed.

## Related

- [`MCPX_HTTP_DNS_FAILED`](MCPX_HTTP_DNS_FAILED.md)
- [`MCPX_HTTP_CONN_REFUSED`](MCPX_HTTP_CONN_REFUSED.md)
- [`MCPX_NETWORK_PROXY_MISCONFIG`](MCPX_NETWORK_PROXY_MISCONFIG.md)
