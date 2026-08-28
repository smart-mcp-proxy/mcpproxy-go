---
id: MCPX_CONFIG_INVALID_COMMAND
title: MCPX_CONFIG_INVALID_COMMAND
sidebar_label: INVALID_COMMAND
description: A stdio server's command and args cannot spawn anything — typically a package runner with no package to run.
---

# `MCPX_CONFIG_INVALID_COMMAND`

**Severity:** error
**Domain:** Config

## What happened

The server's `command` / `args` pair cannot start an MCP server. The common case
is a package runner named with nothing to run:

```jsonc
{
  "name": "demo-filesystem",
  "protocol": "stdio",
  "command": "npx"        // ← no "args", so there is no package to execute
}
```

`npx`, `uvx`, `pipx` and `bunx` all take the package to run as their first
argument. Without it they either print usage and exit or drop into an
interactive prompt, and neither speaks MCP — so the connection fails on every
attempt and the server stays permanently unhealthy.

mcpproxy detects this *before* spawning the process, which is why the failure is
instant rather than a timeout.

## How to fix

### Add the package to `args`

```jsonc
{
  "name": "demo-filesystem",
  "protocol": "stdio",
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/serve"]
}
```

`-y` (npm) skips the install prompt, which would otherwise hang the handshake.
The `uvx` equivalent needs no flag:

```jsonc
{ "command": "uvx", "args": ["mcp-server-time"] }
```

### Inspect what is currently configured

```bash
mcpproxy upstream get <server> -o json
```

### Remove the entry if it is a leftover

An entry that was added for a demo or a test and never completed is worth
deleting rather than fixing — it keeps the tray badge red for a server nobody
uses:

```bash
mcpproxy upstream remove <server>
```

### Apply the change

The config file is hot-reloaded, so saving `~/.mcpproxy/mcp_config.json` is
enough. To force a retry immediately:

```bash
mcpproxy upstream restart <server>
```

## Related

- [`MCPX_STDIO_SPAWN_ENOENT`](MCPX_STDIO_SPAWN_ENOENT.md) — the command itself was not found
- [`MCPX_CONFIG_PARSE_ERROR`](MCPX_CONFIG_PARSE_ERROR.md) — the config file is not valid JSON
