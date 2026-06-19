---
id: MCPX_DOCKER_EXEC_NOT_FOUND
title: MCPX_DOCKER_EXEC_NOT_FOUND
sidebar_label: EXEC_NOT_FOUND
description: The Docker isolation image is missing the interpreter the server needs (e.g. no uvx/node).
---

# `MCPX_DOCKER_EXEC_NOT_FOUND`

**Severity:** error
**Domain:** Docker

## What happened

The container started, but its entrypoint interpreter was not found *inside* the
image. The failure is reported by the OCI runtime (`runc`) at exec time, e.g.:

```
exec: "uvx": executable file not found in $PATH
```

This means Docker isolation worked — the image just doesn't contain the tool the
server's command needs.

## Common cause: a per-server image override that lacks the interpreter

The usual culprit is a per-server `isolation.image` override pointing at a stock
image that doesn't bundle the runtime. The classic example is a `uvx` server
pinned to `python:3.11`:

```jsonc
{
  "name": "elevenlabs",
  "command": "uvx",
  "args": ["elevenlabs-mcp"],
  "isolation": { "image": "python:3.11" }   // ❌ stock python has no uvx
}
```

`uvx` ships with [Astral's `uv`](https://github.com/astral-sh/uv), which is a
separate tool — `python:3.11` does not include it. So
`docker run python:3.11 uvx …` fails at exec time.

When mcpproxy detects this, the diagnostic names the **detected runtime**, the
**recommended image** for it, and flags the per-server override as the likely
culprit.

## How to fix

### Remove the per-server override to inherit the runtime default (recommended)

Each runtime has a default image that includes the right interpreter
(`default_images` in `docker_isolation`). For `uvx`/`pip`/`pipx`/`python` the
default is `ghcr.io/astral-sh/uv:python3.13-bookworm-slim`; for
`npx`/`npm`/`node`/`yarn` it is `node:22`. Drop the override:

```jsonc
{ "name": "elevenlabs", "command": "uvx", "args": ["elevenlabs-mcp"] }
```

### Or pick an image that includes the interpreter

If you must pin a specific image, choose one that bundles the tool:

```jsonc
{ "isolation": { "image": "ghcr.io/astral-sh/uv:python3.11-bookworm-slim" } }
```

### Verify the image has the tool

```bash
docker run --rm <image> which uvx   # or: node, npx, python3 …
```

## Related

- [Docker Isolation](../features/docker-isolation.md)
- [`MCPX_DOCKER_IMAGE_PULL_FAILED`](MCPX_DOCKER_IMAGE_PULL_FAILED.md)
- [`MCPX_DOCKER_DAEMON_DOWN`](MCPX_DOCKER_DAEMON_DOWN.md)
</content>
</invoke>
