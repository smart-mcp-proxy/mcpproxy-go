---
id: docker-isolation
title: Security Isolation (Docker · Sandbox · None)
sidebar_label: Security Isolation
sidebar_position: 1
description: Confine MCP servers with Docker containers, native Landlock sandboxing, or no isolation
keywords: [docker, isolation, sandbox, landlock, security, containers]
---

# Security Isolation (Docker · Sandbox · None)

MCPProxy can confine stdio MCP servers so a malicious or buggy server cannot freely touch the host. There are **three isolation modes** — `docker`, `sandbox`, and `none` — selected by `docker_isolation.mode` (global) or `isolation.mode` (per-server). Most of this page describes **Docker** mode (the default and most capable); see [Isolation Modes](#isolation-modes) for the native **Sandbox** mode and the scanner behaviour under each mode.

:::note
The global config key is still `docker_isolation` for backward compatibility, but its `mode` field selects any of the three modes — it is not Docker-only.
:::

## Overview

Docker isolation automatically wraps stdio-based MCP servers in Docker containers, providing:

- **Process Isolation**: Each server runs in a separate container
- **File System Isolation**: Servers cannot access host file system
- **Network Isolation**: Configurable network modes for security
- **Resource Limits**: Memory and CPU limits prevent resource exhaustion
- **Automatic Runtime Detection**: Maps commands to appropriate Docker images

## Isolation Modes

MCPProxy resolves an **isolation mode** for every stdio server. Set it globally with `docker_isolation.mode` and override per-server with `isolation.mode`:

| Mode | What it does | Where it works | uid/gid drop |
|------|--------------|----------------|--------------|
| `docker` | Wraps the server in a Docker container (process/FS/network isolation, resource limits). The default and most capable mode. | Any host with a working Docker daemon. | Yes (container user) |
| `sandbox` | Runs the server **natively** under a Linux [Landlock](https://docs.kernel.org/userspace-api/landlock.html) filesystem allowlist + `setrlimit` resource caps — **no Docker required**. For hosts where Docker isolation is unavailable or broken (e.g. snap-docker + AppArmor). | Linux 5.13+ only. macOS/Windows: documented no-op ⇒ behaves like `none`. | **No** — see [Honest limitations](#honest-limitations) |
| `none` | No confinement; the server runs directly on the host. | Everywhere. | n/a |

```json
{
  "docker_isolation": { "mode": "sandbox" },
  "mcpServers": [
    { "name": "trusted-local", "command": "uvx", "args": ["x"], "isolation": { "mode": "none" } }
  ]
}
```

### Back-compat with the legacy `enabled` flag

The older boolean `docker_isolation.enabled` (and per-server `isolation.enabled`) still works and is mapped to a mode:

- an explicit `mode` always wins;
- otherwise `enabled: true` ⇒ `docker`, `enabled: false` ⇒ `none`;
- a missing isolation config ⇒ `none`.

Per-server precedence: explicit per-server `mode` → per-server legacy `enabled` → global `mode` → global legacy `enabled`.

### Sandbox mode (Landlock)

`sandbox` mode confines a stdio server **without Docker** by applying a Linux Landlock LSM ruleset (a writable-path allowlist) plus `setrlimit` resource caps to the process before it `exec`s, then preserving the raw stdin/stdout JSON-RPC pipes. It needs no user namespaces, so it is unaffected by `kernel.apparmor_restrict_unprivileged_userns=1` — which is exactly why it works where bubblewrap/userns-based sandboxes are blocked (e.g. Ubuntu 24.04 with snap-docker).

### Scanner behaviour under each mode (MCP-34.4)

The security **scanner plugins** are Docker-based and, since Spec 077, belong to the **opt-in deep-scan layer** (they run only when `security.deep_scan.enabled: true`). Under a non-Docker isolation mode they cannot run at all, so MCPProxy **skips them and surfaces the skip informationally** rather than failing silently:

| Mode | Docker scanner plugins | In-process scanner (`tpa-descriptions`) | Scan result for a server with only Docker scanners |
|------|------------------------|------------------------------------------|----------------------------------------------------|
| `docker` | Run normally (when deep scan is on) | Runs | As scanned |
| `sandbox` / `none` | **Skipped** with an honest, mode-specific reason pointing at [`MCPX_DOCKER_SNAP_APPARMOR`](/errors/MCPX_DOCKER_SNAP_APPARMOR) | **Still runs** | The baseline verdict is unchanged; the skip surfaces via the informational `deep_scan` descriptor |

Since Spec 077 (FR-008) a skipped or failed deep scanner **never downgrades the baseline verdict to `degraded`** — the old `security_scan.status: "degraded"` behaviour was removed. The always-emitted `deep_scan` descriptor carries the skip instead: `{ "enabled": <bool>, "ran": <bool>, "available": <bool>, "scanners_failed": [...], "skipped_scanners": [...] }`. The deterministic in-process `tpa-descriptions` baseline scanner is the sole source of the verdict, so a low/zero risk score from a baseline-only scan is a trustworthy result, and the incomplete Docker coverage is reported as an informational note. To run the full Docker-based scanner fleet, enable deep scan and use `mode: docker` on a host with a working Docker daemon, or replace snap-docker with a distro Docker package (see the error doc). The skip is also logged at startup:

```
WARN  Isolation mode runs no Docker for scanner plugins; Docker-based scanners will be skipped …  {"isolation_mode": "sandbox"}
```

## Configuration

### Global Docker Isolation

Add to your `~/.mcpproxy/mcp_config.json`:

```json
{
  "docker_isolation": {
    "enabled": true,
    "memory_limit": "512m",
    "cpu_limit": "1.0",
    "timeout": "60s",
    "network_mode": "bridge",
    "registry": "docker.io",
    "default_images": {
      "python": "python:3.11",
      "python3": "python:3.11",
      "uvx": "python:3.11",
      "pip": "python:3.11",
      "pipx": "python:3.11",
      "node": "node:20",
      "npm": "node:20",
      "npx": "node:20",
      "yarn": "node:20",
      "go": "golang:1.21-alpine",
      "cargo": "rust:1.75-slim",
      "rustc": "rust:1.75-slim",
      "ruby": "ruby:3.2-alpine",
      "gem": "ruby:3.2-alpine",
      "php": "php:8.2-cli-alpine",
      "composer": "php:8.2-cli-alpine",
      "binary": "alpine:3.18",
      "sh": "alpine:3.18",
      "bash": "alpine:3.18"
    },
    "extra_args": []
  }
}
```

### Configuration Options

| Field | Description | Default |
|-------|-------------|---------|
| `enabled` | Enable Docker isolation globally | `false` |
| `memory_limit` | Memory limit per container | `"512m"` |
| `cpu_limit` | CPU limit per container | `"1.0"` |
| `timeout` | Container startup timeout | `"30s"` |
| `network_mode` | Docker network mode | `"bridge"` |
| `registry` | Docker registry to use | `"docker.io"` |
| `default_images` | Runtime to image mappings | See above |
| `extra_args` | Additional docker run arguments | `[]` |

### Per-Server Configuration

You can override isolation settings per server:

```json
{
  "mcpServers": [
    {
      "name": "custom-python-server",
      "command": "python",
      "args": ["-m", "my_server"],
      "isolation": {
        "enabled": true,
        "image": "my-custom-python:latest",
        "network_mode": "none",
        "working_dir": "/app",
        "extra_args": ["--cap-drop=ALL"]
      },
      "enabled": true
    },
    {
      "name": "no-isolation-server",
      "command": "python",
      "args": ["-m", "trusted_server"],
      "isolation": {
        "enabled": false
      },
      "enabled": true
    }
  ]
}
```

## Runtime Detection

MCPProxy automatically detects the runtime type based on the command:

### Python Environments
- `python`, `python3` → `python:3.11`
- `uvx` → `python:3.11` (includes uv package manager)
- `pip`, `pipx` → `python:3.11`

### Node.js Environments
- `node` → `node:20`
- `npm`, `npx` → `node:20`
- `yarn` → `node:20`

### Other Languages
- `go` → `golang:1.21-alpine`
- `cargo`, `rustc` → `rust:1.75-slim`
- `ruby`, `gem` → `ruby:3.2-alpine`
- `php`, `composer` → `php:8.2-cli-alpine`

### Shell/Binary
- `sh`, `bash` → `alpine:3.18`
- Unknown commands → `alpine:3.18`

## Why Full Images Instead of Slim/Alpine?

MCPProxy uses full Docker images (`python:3.11` instead of `python:3.11-slim`) because:

1. **Git Support**: Many MCP servers install packages from Git repositories using `git+https://` URLs
2. **Build Tools**: Some packages require compilation during installation
3. **System Dependencies**: Full images include common libraries needed by MCP servers

This trade-off prioritizes compatibility over image size.

## Environment Variables

Environment variables from server configuration are automatically passed to containers:

```json
{
  "mcpServers": [
    {
      "name": "api-server",
      "command": "uvx",
      "args": ["some-package"],
      "env": {
        "API_KEY": "your-secret-key",
        "DEBUG": "true"
      },
      "enabled": true
    }
  ]
}
```

These become Docker arguments: `-e API_KEY=your-secret-key -e DEBUG=true`

## Docker-in-Docker Prevention

MCPProxy automatically skips isolation for servers that are already Docker commands:

```json
{
  "mcpServers": [
    {
      "name": "existing-docker-server",
      "command": "docker",
      "args": ["run", "-i", "--rm", "mcp/some-server"],
      "enabled": true
    }
  ]
}
```

This prevents Docker-in-Docker complications.

## Debugging

### Check Docker Isolation Status

```bash
# Run with debug logging
mcpproxy serve --log-level=debug

# Filter for isolation messages
mcpproxy serve --log-level=debug 2>&1 | grep -i "docker isolation"
```

### Monitor Docker Containers

```bash
# List MCPProxy containers
docker ps --format "table {{.Names}}\t{{.Image}}\t{{.Status}}"

# View logs from a specific container
docker logs <container-id>

# Watch container resource usage
docker stats
```

### Common Issues

**Container startup timeouts:**
- Increase `timeout` in docker_isolation config
- Check if Docker images need to be pulled
- Verify network connectivity for package installations

**Environment variables not working:**
- Check that variables are defined in server `env` section
- Use debug logging to see Docker command arguments
- Verify container has access to required environment

**Git/package installation failures:**
- Ensure using full images (`python:3.11` not `python:3.11-slim`)
- Check container logs for specific error messages
- Verify network access for package repositories

## Container Lifecycle

### Startup

When a Docker-isolated server starts:
1. MCPProxy detects runtime type (npm, uvx, python, etc.)
2. Selects appropriate Docker image
3. Runs container with stdio transport (`docker run -i`)
4. Establishes MCP connection via stdin/stdout

### Shutdown

When MCPProxy stops, containers are cleaned up with a 30-second timeout:

1. **Graceful Stop**: `docker stop` (sends SIGTERM to container)
2. **Force Kill**: `docker kill` if container doesn't stop gracefully

Containers are labeled with `mcpproxy.managed=true` for identification.

### Manual Cleanup

If containers remain after MCPProxy stops:

```bash
# List MCPProxy-managed containers
docker ps --filter "label=mcpproxy.managed=true"

# Remove all MCPProxy containers
docker rm -f $(docker ps -q --filter "label=mcpproxy.managed=true")
```

See [Shutdown Behavior](/operations/shutdown-behavior) for detailed subprocess lifecycle documentation.

## Honest limitations

`sandbox` mode is deliberately scoped. Known limitations:

- **No uid/gid drop.** Dropping to an unprivileged uid/gid requires `CAP_SETUID`/`CAP_SETGID` (i.e. running as root). When mcpproxy runs unprivileged, the uid/gid drop is **best-effort and typically a no-op** — the sandboxed process keeps the launching user's identity. Landlock (filesystem) and `setrlimit` (resource caps) still apply. Docker mode does drop to a container user. This is an honest trade-off, not a bug.
- **Linux-only.** Landlock is a Linux 5.13+ feature. On older kernels the launcher degrades best-effort (fewer access-right bits enforced). On macOS/Windows `sandbox` is a documented **no-op** and behaves like `none`.
- **Filesystem + resources only.** Landlock confines the filesystem write-allowlist; it does not provide network namespacing. For network-sensitive servers, use `docker` mode with `network_mode: none`.
- **Docker-based scanners do not run under `sandbox`/`none`.** They are skipped and the skip is surfaced via the informational `deep_scan` descriptor — it never downgrades the baseline verdict (Spec 077 FR-008). A native scanner runtime is a future enhancement.

## Platform support matrix

| Platform | `docker` | `sandbox` | `none` | Docker scanner plugins |
|----------|----------|-----------|--------|------------------------|
| Linux (kernel ≥ 5.13) | ✅ (needs Docker daemon) | ✅ Landlock + rlimits (no uid/gid drop) | ✅ | ✅ under `docker` (deep scan on); skipped (informational, no verdict change) under `sandbox`/`none` |
| Linux (kernel &lt; 5.13) | ✅ (needs Docker daemon) | ⚠️ best-effort: rlimits apply, Landlock partial/unavailable | ✅ | same as above |
| macOS | ✅ (Docker Desktop) | ⚠️ no-op ⇒ effectively `none` | ✅ | ✅ under `docker`; n/a otherwise |
| Windows | ✅ (Docker Desktop) | ⚠️ no-op ⇒ effectively `none` | ✅ | ✅ under `docker`; n/a otherwise |

## Security Considerations

Docker isolation provides strong security boundaries but consider:

1. **Network Access**: Containers can still access the network by default
2. **Resource Limits**: Set appropriate memory/CPU limits
3. **Image Trust**: Use trusted base images from official repositories
4. **Secrets**: Environment variables are visible in container inspect output

For maximum security, consider:
- Using `"network_mode": "none"` for servers that don't need network access
- Adding `--cap-drop=ALL` to extra_args to remove Linux capabilities
- Using custom minimal images for specific use cases
