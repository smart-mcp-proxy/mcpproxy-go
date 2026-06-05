---
id: MCPX_STDIO_EXIT_BEFORE_INITIALIZE
title: MCPX_STDIO_EXIT_BEFORE_INITIALIZE
sidebar_label: EXIT_BEFORE_INITIALIZE
description: The stdio MCP server process exited before completing the MCP initialize handshake.
---

# `MCPX_STDIO_EXIT_BEFORE_INITIALIZE`

**Severity:** error
**Domain:** STDIO

## What happened

mcpproxy spawned the stdio server, but the process exited before completing the
MCP `initialize` handshake. The underlying transport reports a closed pipe / EOF
rather than a typed exit error, which previously surfaced as a generic
`MCPX_UNKNOWN_UNCLASSIFIED`. This code makes the cause explicit, and mcpproxy now
folds the child's **exit code** and the last lines of its **stderr** into the
error so you can see the real, usually self-serviceable problem.

This almost always means a **missing or invalid configuration** — a required API
key or environment variable, a bad argument, or a missing dependency — that makes
the server print an error and exit immediately on startup.

## Common causes

- A required environment variable / API key is missing (e.g. a server that needs
  `BRAVE_API_KEY` and exits with `Error: --brave-api-key is required`).
- A required CLI flag/argument was not passed via `args`.
- A Docker image is missing a mandatory `env` value (cf. `MCPX_STDIO_EXIT_NONZERO`).
- The upstream package is not installed in the resolved runtime (`uvx`/`pipx`/`npx`).

## How to fix

### 1. Read the captured stderr

The error banner and the per-server log already include the last stderr lines and
the exit code. To see more:

```bash
mcpproxy upstream logs <server-name> --tail 100
```

The last lines almost always contain the real cause (a missing-key message, a
traceback, a "module not found", etc.).

### 2. Supply the missing configuration

Add the required environment variable / argument to the server entry, e.g.:

```json
{
  "name": "brave-search",
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-brave-search"],
  "env": { "BRAVE_API_KEY": "your-key-here" }
}
```

### 3. Reproduce manually

Run the exact `command`, `args`, and `env` in a normal shell; the server should
start and wait for MCP input instead of exiting. Once it starts cleanly, restart
the server in mcpproxy.

## Related codes

- [`MCPX_STDIO_EXIT_NONZERO`](MCPX_STDIO_EXIT_NONZERO.md) — exited non-zero before handshake
- [`MCPX_STDIO_HANDSHAKE_TIMEOUT`](MCPX_STDIO_HANDSHAKE_TIMEOUT.md) — no `initialize` reply in time
