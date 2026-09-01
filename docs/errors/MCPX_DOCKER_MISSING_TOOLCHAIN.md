---
id: MCPX_DOCKER_MISSING_TOOLCHAIN
title: MCPX_DOCKER_MISSING_TOOLCHAIN
sidebar_label: MISSING_TOOLCHAIN
description: The Docker isolation image is missing a tool the server shells out to at runtime (most often git).
---

# `MCPX_DOCKER_MISSING_TOOLCHAIN`

**Severity:** error
**Domain:** Docker

## What happened

The container started and the server's interpreter ran, but a tool the server
shells out to is **not installed in the image**. The failure text comes from
*inside* the container, e.g.:

```
Git executable not found. Ensure that Git is installed and available.
```

```
  × Failed to resolve `--with` requirement
  ╰─▶ Git operation failed
```

```
sh: 1: git: not found
```

This is a different failure from
[`MCPX_DOCKER_EXEC_NOT_FOUND`](MCPX_DOCKER_EXEC_NOT_FOUND.md), which is the
container's **entrypoint interpreter** missing — reported by the OCI runtime
before the server process runs at all.

## Common cause: a git dependency in a slim Python image

By far the most common case is an MCP server distributed only as a git URL:

```jsonc
{
  "name": "my-server",
  "command": "uvx",
  "args": ["--from", "my-server@git+https://github.com/o/r", "my-server"]
}
```

`uv` clones the repository with `git`, and the Python default image is Astral's
**slim** uv image, which does not contain git:

```console
$ docker run --rm --entrypoint sh ghcr.io/astral-sh/uv:python3.13-bookworm-slim -c 'git --version'
sh: 1: git: not found
$ docker run --rm --entrypoint sh ghcr.io/astral-sh/uv:python3.13-bookworm -c 'git --version'
git version 2.39.5
```

## Is this handled automatically?

**Yes, for git dependencies with no per-server image override.** MCPProxy
detects a `git+` URL in a Python package runner's arguments and swaps in the
git-capable image named by `docker_isolation.default_images.uvx-git`
(`ghcr.io/astral-sh/uv:python3.13-bookworm` by default). Servers without a git
dependency keep the small slim image.

So if you still see this code, one of these applies:

| Situation | Fix |
|-----------|-----|
| The server pins `isolation.image` | The override opts out of the automatic selection. Remove it, or pin an image that ships the tool. |
| `default_images.uvx-git` was retargeted to an image without git | Point it at an image that has git. |
| The missing tool is **not** git (e.g. `make`, `gcc`, `cargo`) | There is no automatic selection for it — pin an `isolation.image` that ships it. |
| You are running an older MCPProxy | Upgrade, or set the image explicitly. |

## How to fix

### Remove a per-server override so the automatic selection applies

```jsonc
{ "name": "my-server", "command": "uvx", "args": ["--from", "my-server@git+https://github.com/o/r", "my-server"] }
```

### Or point the shared key at your own image

```jsonc
{
  "docker_isolation": {
    "default_images": { "uvx-git": "my-registry.example/uv-git:1" }
  }
}
```

### Or pin this one server to an image that ships the tool

```jsonc
{ "isolation": { "image": "ghcr.io/astral-sh/uv:python3.13-bookworm" } }
```

### Verify the image has the tool

```bash
docker run --rm --entrypoint sh <image> -c 'git --version'
```

## Related

- [Docker Isolation](../features/docker-isolation.md)
- [`MCPX_DOCKER_EXEC_NOT_FOUND`](MCPX_DOCKER_EXEC_NOT_FOUND.md)
- [`MCPX_DOCKER_IMAGE_PULL_FAILED`](MCPX_DOCKER_IMAGE_PULL_FAILED.md)
