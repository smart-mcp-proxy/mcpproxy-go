---
id: MCPX_HTTP_DNS_FAILED
title: MCPX_HTTP_DNS_FAILED
sidebar_label: DNS_FAILED
description: DNS lookup for the configured MCP server hostname failed.
---

# `MCPX_HTTP_DNS_FAILED`

**Severity:** error
**Domain:** HTTP

## What happened

mcpproxy could not resolve the hostname in the upstream server URL. The
underlying error is from the OS resolver (`getaddrinfo`).

## Common causes

- Typo in the hostname.
- VPN / corporate split-DNS not connected.
- The hostname only resolves inside a private network and the host is offline
  from that network.
- DNS over HTTPS / DoH proxy is misconfigured.
- `/etc/hosts` overrides interfering.

## How to fix

```bash
# Verify the hostname:
dig <hostname>            # should print A/AAAA records
host <hostname>
nslookup <hostname>
```

If `dig` works but mcpproxy doesn't, the difference is usually:

- A VPN that's only active in the launching shell.
- A `HOSTALIASES` or `/etc/hosts` override visible in the shell but not the GUI.

### VPN / split DNS

Make sure your VPN client is started before mcpproxy if the MCP server lives
inside the corporate network. Tray apps inherit the GUI environment, not the
shell environment.

### Override hostname in config

If the hostname is unstable but the IP is known:

```json
{ "url": "https://203.0.113.10/mcp", "tls_server_name": "internal.example.com" }
```

Use `tls_server_name` to keep TLS verification working.

## Related

- [`MCPX_HTTP_CONN_REFUSED`](MCPX_HTTP_CONN_REFUSED.md)
- [`MCPX_NETWORK_OFFLINE`](MCPX_NETWORK_OFFLINE.md)
- [`MCPX_NETWORK_PROXY_MISCONFIG`](MCPX_NETWORK_PROXY_MISCONFIG.md)
