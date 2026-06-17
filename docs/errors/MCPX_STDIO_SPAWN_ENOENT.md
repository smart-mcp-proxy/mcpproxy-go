---
id: MCPX_STDIO_SPAWN_ENOENT
title: MCPX_STDIO_SPAWN_ENOENT
sidebar_label: SPAWN_ENOENT
description: The configured command for a stdio MCP server was not found on PATH.
---

# `MCPX_STDIO_SPAWN_ENOENT`

**Severity:** error
**Domain:** STDIO

## What happened

mcpproxy tried to spawn a stdio MCP server, but the operating system reported
`ENOENT` — the configured command does not exist on the user's `PATH`. The
subprocess never started, so the MCP `initialize` handshake timed out.

A typical error chain looks like:

```
failed to connect: stdio transport (command="uvx", docker_isolation=false):
  server did not respond to MCP initialize within 30s
  recent stderr: zsh:1: command not found: uvx
```

## Common causes

- **Tool not installed** — `uvx`, `npx`, `python3`, `bunx`, etc. are missing from the host.
- **Wrong `PATH`** — the GUI launcher (Finder, systemd, launchd) doesn't see the
  shell-only `PATH` that includes `~/.local/bin`, `/opt/homebrew/bin`, NodeSource paths, etc.
- **Wrong working directory** — the command exists at a relative path that's only
  valid inside the server's `working_dir`.
- **Typo** in the `command` field of the upstream entry.

## How to fix

### 1. Check what mcpproxy's environment sees

```bash
which npx && which uvx && which python3
echo "$PATH"
```

If the command is missing, install the runtime:

```bash
# uvx (Python tools) — recommended
curl -LsSf https://astral.sh/uv/install.sh | sh

# npx (Node tools)
brew install node            # macOS
sudo apt install nodejs npm  # Debian/Ubuntu
```

### 2. Make the GUI inherit the shell PATH (macOS)

When launched from a macOS GUI/launchd context (Launchpad, the login item, or
the tray spawning the core), mcpproxy inherits a launchd-minimal environment
rather than your interactive shell `PATH`. Since the daemon now **hydrates the
login-shell environment once at startup** — sourcing your login shell to merge
in `PATH` (and curated `DOCKER_*`/proxy/tool-home vars) — `uvx`/`npx` installed
in `/opt/homebrew/bin`, `~/.local/bin`, or a version-manager shim directory are
normally found automatically. (Hydration is a no-op when mcpproxy is started
from a terminal whose `PATH` is already comprehensive.)

If the tool still isn't found — e.g. it lives in a directory your login shell
doesn't export, or your rc files don't run non-interactively — either:

- Move the binary to a system directory: `sudo ln -s "$(which uvx)" /usr/local/bin/uvx`, or
- Set an absolute path in the upstream config: `"command": "/Users/you/.local/bin/uvx"`.

### 3. Use Docker isolation

If you don't want to install language runtimes on the host, enable Docker
isolation for the server. mcpproxy will run the command inside a container
that already has `uvx` / `npx` available.

```json
{
  "name": "my-server",
  "command": "uvx",
  "args": ["some-mcp-server"],
  "isolation": { "enabled": true }
}
```

See [Docker Isolation](../features/docker-isolation.md).

### 4. Inspect recent server logs

```bash
mcpproxy upstream logs <server-name> --tail 50
```

The last lines of stderr usually identify exactly which executable was missing.

## Related

- [Docker Isolation](../features/docker-isolation.md)
- [Configuration → upstream servers](../configuration/upstream-servers.md)
- `mcpproxy doctor --server <name>` for an interactive walk-through
