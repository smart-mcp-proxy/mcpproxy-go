---
id: MCPX_STDIO_HANDSHAKE_INVALID
title: MCPX_STDIO_HANDSHAKE_INVALID
sidebar_label: HANDSHAKE_INVALID
description: The stdio server replied, but the MCP handshake frame was malformed.
---

# `MCPX_STDIO_HANDSHAKE_INVALID`

**Severity:** error
**Domain:** STDIO

## What happened

The stdio server emitted bytes on stdout in response to `initialize`, but
mcpproxy could not parse them as a valid MCP / JSON-RPC frame. Common shapes:
HTML, a stack trace, ANSI-coloured logs, partial JSON, or an MCP frame for an
unsupported protocol version.

## Common causes

- The server logs to stdout in addition to JSON-RPC frames.
- The server speaks an older / newer MCP protocol version that mcpproxy can't negotiate.
- An npm/pip wrapper script printed a deprecation warning before the real
  process took over.
- A shell wrapper added stdout content (e.g. `set -x`, `echo "starting…"`).

## How to fix

### Read the captured frame

`upstream logs` records the raw stdout stream — find the offending payload:

```bash
mcpproxy upstream logs <server-name> --tail 200
```

### Silence non-JSON output

- Redirect informational output to stderr in your wrapper script:
  `echo "starting" >&2`.
- Disable colour: `NO_COLOR=1`, `TERM=dumb`.
- Remove `set -x` or `set -v` from start-up scripts.

### Negotiate a compatible protocol

mcpproxy currently advertises a recent protocol version. If the upstream server
only supports an old one, update the server. Project maintainers should target
the [MCP protocol versions](../api/mcp-protocol.md) that mcpproxy understands.

### Try Docker isolation

Running the server inside a clean container removes most sources of
contamination from your shell profile and runtime:

```json
{
  "isolation": { "enabled": true }
}
```

## Related

- [`MCPX_STDIO_HANDSHAKE_TIMEOUT`](MCPX_STDIO_HANDSHAKE_TIMEOUT.md) — server didn't reply at all
- [MCP Protocol](../api/mcp-protocol.md)
