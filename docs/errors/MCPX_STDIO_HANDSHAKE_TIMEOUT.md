---
id: MCPX_STDIO_HANDSHAKE_TIMEOUT
title: MCPX_STDIO_HANDSHAKE_TIMEOUT
sidebar_label: HANDSHAKE_TIMEOUT
description: The stdio server did not reply to the MCP initialize request in time.
---

# `MCPX_STDIO_HANDSHAKE_TIMEOUT`

**Severity:** error
**Domain:** STDIO

## What happened

mcpproxy spawned the server and sent `initialize`, but the server did not reply
within 30 seconds. The process is still alive (otherwise we'd see
[`MCPX_STDIO_EXIT_NONZERO`](MCPX_STDIO_EXIT_NONZERO.md)), but it is not speaking
the MCP protocol on stdout.

## Common causes

- The server prints log lines / banners to **stdout** instead of stderr,
  corrupting the JSON-RPC channel.
- The first call out of the server (auth handshake, model download, npm/pip
  install on first run) is slower than 30s.
- Wrong protocol — a CLI tool that doesn't actually implement MCP was configured
  as an MCP server.
- The runtime is buffering stdout (Python without `-u`, Node without
  `process.stdout.write` flush) so frames never reach mcpproxy.

## How to fix

### 1. Inspect both streams

```bash
mcpproxy upstream logs <server-name> --tail 100
```

If you see human-readable banners or `pip install` output, the server is
contaminating stdout. Either fix the server to log to stderr, or wrap it.

### 2. Pre-warm slow first runs

If the underlying tool is downloading a model or installing dependencies on
first start, run it once manually to populate caches:

```bash
uvx some-mcp-server --help
npx -y some-mcp-server --version
```

### 3. Force unbuffered stdout

For Python servers, add `-u` to args or set `PYTHONUNBUFFERED=1` in env. For
Node, ensure `process.stdout.write` calls aren't held in a TTY-detection branch.

### 4. Verify it's actually an MCP server

Test directly via stdio:

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}' \
  | <command> <args>
```

You should get a JSON-RPC reply within a few seconds.

## Related

- [`MCPX_STDIO_HANDSHAKE_INVALID`](MCPX_STDIO_HANDSHAKE_INVALID.md) — server replied but malformed
- [`MCPX_STDIO_EXIT_NONZERO`](MCPX_STDIO_EXIT_NONZERO.md) — server crashed instead of stalling
