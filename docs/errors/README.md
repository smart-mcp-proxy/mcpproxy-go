# Error Code Catalog (spec 044)

This directory is the user-facing documentation for every stable mcpproxy
error code. Each code is identified by a name of the form
`MCPX_<DOMAIN>_<SPECIFIC>` and has a dedicated page that explains the
cause, the typical symptoms, and the remediation steps.

Codes are **stable**: once shipped, a code name is never renamed. A
deprecated code points to its replacement.

The authoritative in-code registry lives in
[`internal/diagnostics/registry.go`](../../internal/diagnostics/registry.go).
Run `mcpproxy doctor list-codes` for the machine-readable list.

## Domains

| Domain | Prefix | Covers |
|---|---|---|
| STDIO | `MCPX_STDIO_*` | stdio-transport MCP servers — spawn, handshake, exit |
| OAUTH | `MCPX_OAUTH_*` | OAuth 2.1 / PKCE flows — discovery, refresh, callback |
| HTTP  | `MCPX_HTTP_*`  | HTTP/SSE transports — DNS, TLS, auth, status |
| DOCKER | `MCPX_DOCKER_*` | Docker isolation subsystem |
| CONFIG | `MCPX_CONFIG_*` | Config parsing and secret resolution |
| QUARANTINE | `MCPX_QUARANTINE_*` | Security quarantine state |
| NETWORK | `MCPX_NETWORK_*` | Host network environment |
| UNKNOWN | `MCPX_UNKNOWN_*` | Fallback when classification fails |

## Pages

Each registered code has a `docs/errors/<CODE>.md` page. The index is
verified by `scripts/check-errors-docs-links.sh`; a missing page fails
CI.

See the [diagnostics design doc](../superpowers/specs/2026-04-24-diagnostics-error-taxonomy-design.md)
and [spec 044](../../specs/044-diagnostics-taxonomy/spec.md) for the
design intent.
