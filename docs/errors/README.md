---
id: README
title: Error Code Catalog
sidebar_label: Overview
description: Stable error codes emitted by mcpproxy with cause, symptoms, and remediation steps.
---

# Error Code Catalog

mcpproxy emits a stable error code with every classified failure. Codes follow the
form `MCPX_<DOMAIN>_<SPECIFIC>` and are surfaced in the web UI error panel, the
tray, the CLI (`mcpproxy doctor`, `mcpproxy upstream logs`) and the activity log.

Each code has a dedicated page below explaining the cause, the typical symptoms,
and the remediation steps. Links from the product point at this site
(`https://docs.mcpproxy.app/errors/<CODE>`).

## Domains

| Domain | Prefix | Covers |
|---|---|---|
| [STDIO](#stdio) | `MCPX_STDIO_*` | stdio-transport MCP servers — spawn, handshake, exit |
| [OAuth](#oauth) | `MCPX_OAUTH_*` | OAuth 2.1 / PKCE flows — discovery, refresh, callback |
| [HTTP](#http)  | `MCPX_HTTP_*`  | HTTP/SSE transports — DNS, TLS, auth, status |
| [Docker](#docker) | `MCPX_DOCKER_*` | Docker isolation subsystem |
| [Config](#config) | `MCPX_CONFIG_*` | Config parsing and secret resolution |
| [Quarantine](#quarantine) | `MCPX_QUARANTINE_*` | Security quarantine state |
| [Network](#network) | `MCPX_NETWORK_*` | Host network environment |
| [Unknown](#unknown) | `MCPX_UNKNOWN_*` | Fallback when classification fails |

## Stability guarantee

Codes are stable: once shipped, a code name is never renamed. A deprecated code
points to its replacement. The authoritative in-code registry lives in
[`internal/diagnostics/registry.go`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/internal/diagnostics/registry.go).
Run `mcpproxy doctor list-codes` for the machine-readable list.

## STDIO

- [`MCPX_STDIO_SPAWN_ENOENT`](MCPX_STDIO_SPAWN_ENOENT.md) — command not found on PATH
- [`MCPX_STDIO_SPAWN_EACCES`](MCPX_STDIO_SPAWN_EACCES.md) — permission denied executing command
- [`MCPX_STDIO_EXIT_NONZERO`](MCPX_STDIO_EXIT_NONZERO.md) — server exited before handshake
- [`MCPX_STDIO_HANDSHAKE_TIMEOUT`](MCPX_STDIO_HANDSHAKE_TIMEOUT.md) — no `initialize` reply within 30s
- [`MCPX_STDIO_HANDSHAKE_INVALID`](MCPX_STDIO_HANDSHAKE_INVALID.md) — malformed MCP frame

## OAuth

- [`MCPX_OAUTH_REFRESH_EXPIRED`](MCPX_OAUTH_REFRESH_EXPIRED.md) — refresh token expired
- [`MCPX_OAUTH_REFRESH_403`](MCPX_OAUTH_REFRESH_403.md) — provider rejected refresh
- [`MCPX_OAUTH_DISCOVERY_FAILED`](MCPX_OAUTH_DISCOVERY_FAILED.md) — `.well-known` discovery failed
- [`MCPX_OAUTH_CALLBACK_TIMEOUT`](MCPX_OAUTH_CALLBACK_TIMEOUT.md) — browser callback timed out
- [`MCPX_OAUTH_CALLBACK_MISMATCH`](MCPX_OAUTH_CALLBACK_MISMATCH.md) — redirect URI mismatch

## HTTP

- [`MCPX_HTTP_DNS_FAILED`](MCPX_HTTP_DNS_FAILED.md) — DNS lookup failed
- [`MCPX_HTTP_TLS_FAILED`](MCPX_HTTP_TLS_FAILED.md) — TLS handshake failed
- [`MCPX_HTTP_401`](MCPX_HTTP_401.md) — Unauthorized
- [`MCPX_HTTP_403`](MCPX_HTTP_403.md) — Forbidden
- [`MCPX_HTTP_404`](MCPX_HTTP_404.md) — Not Found
- [`MCPX_HTTP_5XX`](MCPX_HTTP_5XX.md) — Server error
- [`MCPX_HTTP_CONN_REFUSED`](MCPX_HTTP_CONN_REFUSED.md) — Connection refused

## Docker

- [`MCPX_DOCKER_DAEMON_DOWN`](MCPX_DOCKER_DAEMON_DOWN.md) — daemon unreachable
- [`MCPX_DOCKER_IMAGE_PULL_FAILED`](MCPX_DOCKER_IMAGE_PULL_FAILED.md) — pull failed
- [`MCPX_DOCKER_NO_PERMISSION`](MCPX_DOCKER_NO_PERMISSION.md) — socket permission denied
- [`MCPX_DOCKER_SNAP_APPARMOR`](MCPX_DOCKER_SNAP_APPARMOR.md) — snap Docker AppArmor block

## Config

- [`MCPX_CONFIG_DEPRECATED_FIELD`](MCPX_CONFIG_DEPRECATED_FIELD.md) — deprecated field used
- [`MCPX_CONFIG_PARSE_ERROR`](MCPX_CONFIG_PARSE_ERROR.md) — invalid JSON
- [`MCPX_CONFIG_MISSING_SECRET`](MCPX_CONFIG_MISSING_SECRET.md) — secret reference unresolved

## Quarantine

- [`MCPX_QUARANTINE_PENDING_APPROVAL`](MCPX_QUARANTINE_PENDING_APPROVAL.md) — tools awaiting approval
- [`MCPX_QUARANTINE_TOOL_CHANGED`](MCPX_QUARANTINE_TOOL_CHANGED.md) — rug-pull detected

## Network

- [`MCPX_NETWORK_PROXY_MISCONFIG`](MCPX_NETWORK_PROXY_MISCONFIG.md) — proxy env broken
- [`MCPX_NETWORK_OFFLINE`](MCPX_NETWORK_OFFLINE.md) — no network connectivity

## Unknown

- [`MCPX_UNKNOWN_UNCLASSIFIED`](MCPX_UNKNOWN_UNCLASSIFIED.md) — please file a bug
