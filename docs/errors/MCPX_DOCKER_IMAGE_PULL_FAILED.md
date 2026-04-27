---
id: MCPX_DOCKER_IMAGE_PULL_FAILED
title: MCPX_DOCKER_IMAGE_PULL_FAILED
sidebar_label: IMAGE_PULL_FAILED
description: Docker failed to pull the isolation image used by an upstream server.
---

# `MCPX_DOCKER_IMAGE_PULL_FAILED`

**Severity:** error
**Domain:** Docker

## What happened

Before starting an isolated upstream, mcpproxy needs the runtime image (e.g.
`python:3.12-slim` for `uvx`, `node:20-alpine` for `npx`). Pulling failed.

## Common causes

- No internet connectivity at the moment.
- Registry rate-limit (Docker Hub anonymous limit is 100 pulls / 6h per IP).
- The image name in `isolation.image` is wrong.
- A corporate registry mirror is required and isn't configured.
- Disk full — pull aborted mid-stream.

## How to fix

### Pull manually to see the underlying error

```bash
docker pull <image>
```

Docker's own error is more specific than the wrapped one mcpproxy surfaces.

### Authenticate against Docker Hub

If you're hitting rate-limits:

```bash
docker login
```

A free Docker Hub account doubles the limit and gives you predictable resets.

### Configure a registry mirror

Many corporate networks require a private mirror. Configure it once in
`~/.docker/daemon.json` (or per-platform equivalent):

```json
{ "registry-mirrors": ["https://registry.example.com"] }
```

Restart Docker after editing.

### Free up disk

```bash
docker system df          # see what's using space
docker system prune -a    # nuke unused images/containers (destructive — review first)
```

## Related

- [Docker Isolation](../features/docker-isolation.md)
- [`MCPX_DOCKER_DAEMON_DOWN`](MCPX_DOCKER_DAEMON_DOWN.md)
- [`MCPX_NETWORK_OFFLINE`](MCPX_NETWORK_OFFLINE.md)
