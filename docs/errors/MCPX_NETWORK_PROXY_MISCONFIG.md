---
id: MCPX_NETWORK_PROXY_MISCONFIG
title: MCPX_NETWORK_PROXY_MISCONFIG
sidebar_label: PROXY_MISCONFIG
description: The system HTTP_PROXY / HTTPS_PROXY variables look misconfigured.
---

# `MCPX_NETWORK_PROXY_MISCONFIG`

**Severity:** warn
**Domain:** Network

## What happened

mcpproxy detected `HTTP_PROXY`, `HTTPS_PROXY`, or `ALL_PROXY` in its environment
but they don't appear to be reachable, are missing a scheme, or look syntactically
broken. Outbound connections will likely fail.

## Common causes

- Stale variables left over from a corporate VPN that's now disconnected.
- Missing scheme: `proxy.example.com:8080` instead of `http://proxy.example.com:8080`.
- `NO_PROXY` doesn't include the upstream MCP server's hostname.
- A `.pac` file URL was set instead of a direct proxy.

## How to fix

### Inspect the current environment

```bash
env | grep -iE 'proxy|no_proxy'
```

### Fix the syntax

```bash
export HTTPS_PROXY=http://proxy.example.com:8080
export HTTP_PROXY=http://proxy.example.com:8080
export NO_PROXY="localhost,127.0.0.1,.example.com"
```

The proxy URL **must** include the scheme.

### Set the environment for the tray

Tray apps don't inherit shell `~/.zshrc` exports. On macOS, set them in
`~/Library/LaunchAgents/com.mcpproxy.tray.plist` (the bundle includes a
template) or use `launchctl setenv`.

### Disable proxy when not needed

If you no longer need the proxy:

```bash
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY
```

## Related

- [`MCPX_HTTP_DNS_FAILED`](MCPX_HTTP_DNS_FAILED.md)
- [`MCPX_HTTP_TLS_FAILED`](MCPX_HTTP_TLS_FAILED.md)
- [`MCPX_NETWORK_OFFLINE`](MCPX_NETWORK_OFFLINE.md)
