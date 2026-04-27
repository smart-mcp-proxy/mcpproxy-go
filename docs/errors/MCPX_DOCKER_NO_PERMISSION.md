---
id: MCPX_DOCKER_NO_PERMISSION
title: MCPX_DOCKER_NO_PERMISSION
sidebar_label: NO_PERMISSION
description: The current user can't talk to the Docker socket.
---

# `MCPX_DOCKER_NO_PERMISSION`

**Severity:** error
**Domain:** Docker

## What happened

The Docker daemon is up, but the user mcpproxy is running as does not have
permission to connect to the Docker socket
(`/var/run/docker.sock` on Linux/macOS).

## How to fix

### Linux — add the user to the `docker` group

```bash
sudo usermod -aG docker "$USER"
newgrp docker          # apply in the current shell without logging out
docker info            # verify
```

You may need to log out and back in for GUI apps (the tray) to pick up the new
group.

### macOS — check Docker Desktop permissions

Docker Desktop usually grants access automatically. If not:

- Reinstall Docker Desktop (it re-creates the socket symlink with the right perms).
- Or use Colima, which runs as the current user without socket-permission issues.

### Rootless Docker

If you're using rootless Docker, make sure `DOCKER_HOST` points at the rootless
socket and the systemd user service is running:

```bash
systemctl --user status docker
echo "$DOCKER_HOST"     # typically unix:///run/user/<uid>/docker.sock
```

### Avoid setuid hacks

Don't `chmod 666 /var/run/docker.sock` — that gives every local process root
escalation via Docker. Adding the user to the `docker` group is the safe,
canonical fix (and is documented to grant root-equivalent privileges).

## Related

- [`MCPX_DOCKER_DAEMON_DOWN`](MCPX_DOCKER_DAEMON_DOWN.md)
- [`MCPX_DOCKER_SNAP_APPARMOR`](MCPX_DOCKER_SNAP_APPARMOR.md)
- [Docker Isolation](../features/docker-isolation.md)
