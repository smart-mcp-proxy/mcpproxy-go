---
id: MCPX_STDIO_EXIT_NONZERO
title: MCPX_STDIO_EXIT_NONZERO
sidebar_label: EXIT_NONZERO
description: The stdio MCP server process exited with a non-zero status before completing the handshake.
---

# `MCPX_STDIO_EXIT_NONZERO`

**Severity:** error
**Domain:** STDIO

## What happened

mcpproxy spawned the stdio server, but the process exited with a non-zero exit
code before the MCP `initialize` handshake completed. Whatever crashed it usually
printed something useful to **stderr**.

## Common causes

- Missing language runtime dependency (e.g. Python 3.10+ required, but only 3.9 available).
- Missing required environment variable / API key.
- Bad CLI flags or arguments passed via `args`.
- Upstream package not installed in the resolved environment (`uvx`/`pipx`/`npx`).
- Crash on startup due to incompatible OS or arch.

## How to fix

### 1. Read the last stderr lines

```bash
mcpproxy upstream logs <server-name> --tail 100
```

The exit was synchronous: the very last lines almost always contain the real
error (a Python traceback, a Node error, "module not found", etc.).

### 2. Run the command manually

Reproduce in a normal shell with the exact same `command`, `args`, and `env`
from your config:

```bash
ENV_KEY=value /full/path/to/cmd --flag1 --flag2
```

If it fails the same way, fix the underlying tool before retrying through mcpproxy.

### 3. Check required environment

Many MCP servers require API keys via environment:

```bash
mcpproxy upstream inspect <server-name>   # shows resolved env (secrets redacted)
```

Add missing keys to the upstream `env` field or to a referenced secret.

### 4. Check Python/Node version

```bash
python3 --version    # the server may require >=3.10
node --version       # some packages drop support for old Node majors
```

## Related

- [`MCPX_STDIO_HANDSHAKE_TIMEOUT`](MCPX_STDIO_HANDSHAKE_TIMEOUT.md) — process started but never replied
- [`MCPX_CONFIG_MISSING_SECRET`](MCPX_CONFIG_MISSING_SECRET.md) — secret references don't resolve
- [Docker Isolation](../features/docker-isolation.md) — sandbox runtimes per server
