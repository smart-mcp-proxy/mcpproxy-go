---
id: MCPX_DOCKER_DAEMON_DOWN
title: MCPX_DOCKER_DAEMON_DOWN
sidebar_label: DAEMON_DOWN
description: The Docker daemon is not reachable; isolated upstream servers cannot run.
---

# `MCPX_DOCKER_DAEMON_DOWN`

**Severity:** error
**Domain:** Docker

## What happened

A server is configured with `isolation.enabled: true` (or auto-detected as
needing isolation), but mcpproxy could not connect to the Docker daemon. The
upstream server cannot start without Docker.

## Common causes

- Docker Desktop is installed but not running (macOS / Windows).
- The Docker service hasn't been started (Linux: `dockerd`, `containerd`).
- `DOCKER_HOST` points to a stale socket (e.g. left over from a previous Colima session).
- Permissions issue — see [`MCPX_DOCKER_NO_PERMISSION`](MCPX_DOCKER_NO_PERMISSION.md)
  for that variant.

## How to fix

```bash
docker info               # works → daemon is up
docker context ls         # which context (rootful, rootless, colima, …) is active
echo "$DOCKER_HOST"       # should be empty or point at a real socket
```

### macOS / Windows — start Docker Desktop or Colima

```bash
open -a Docker            # macOS Docker Desktop
colima start              # macOS / Linux Colima
```

### Linux — start the daemon

```bash
sudo systemctl start docker
sudo systemctl enable docker     # auto-start on boot
```

### Disable isolation for one server

If you don't need isolation for a particular server, turn it off:

```json
{ "isolation": { "enabled": false } }
```

Note this means the upstream runs directly on the host with the host's
`PATH` / network — only do it for servers you trust.

## Related

- [Docker Isolation](../features/docker-isolation.md)
- [`MCPX_DOCKER_NO_PERMISSION`](MCPX_DOCKER_NO_PERMISSION.md)
- [`MCPX_DOCKER_SNAP_APPARMOR`](MCPX_DOCKER_SNAP_APPARMOR.md)
