# Security Isolation (Docker · Sandbox · None)

MCPProxy can confine stdio MCP servers so a malicious or buggy server cannot freely touch the host. There are **three isolation modes** — `docker`, `sandbox`, and `none` — selected by `docker_isolation.mode` (global) or `isolation.mode` (per-server). This document covers all three; most of it describes the **Docker** mode (the default and most capable), with the **Sandbox** mode and the **scanner behaviour under each mode** in [Isolation Modes](#isolation-modes) below.

> **Naming note:** the global config key is still `docker_isolation` for backward compatibility, but its `mode` field selects any of the three modes — it is not Docker-only.

> **New installs:** Docker isolation is turned on automatically when mcpproxy creates its initial `mcp_config.json` and a Docker daemon is reachable (`docker info` responds within 2 seconds). If Docker isn't available at first run, isolation stays off so stdio servers still work — you can enable it later from the **Security** page in the Web UI or by editing the config below.
>
> **Existing installs:** Your current `docker_isolation.enabled` value is preserved on upgrade. To turn isolation on manually, set the top-level flag in `~/.mcpproxy/mcp_config.json` (or use the Web UI toggle):
>
> ```json
> {
>   "docker_isolation": {
>     "enabled": true
>   }
> }
> ```
>
> Existing connections will re-wrap themselves in containers after the next server restart; new connections pick up isolation immediately.

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
| `sandbox` | Runs the server **natively** under a Linux [Landlock](https://docs.kernel.org/userspace-api/landlock.html) filesystem allowlist + `setrlimit` resource caps — **no Docker required**. For hosts where Docker isolation is unavailable or broken (e.g. snap-docker + AppArmor). | Linux 5.13+ only (Landlock). Best-effort downgrade across ABI 1–5. macOS/Windows: documented no-op ⇒ behaves like `none`. | **No** — see [Honest limitations](#honest-limitations) |
| `none` | No confinement; the server runs directly on the host. | Everywhere. | n/a |

```json
{
  "docker_isolation": {
    "mode": "sandbox"
  },
  "mcpServers": [
    { "name": "trusted-local", "command": "uvx", "args": ["x"], "isolation": { "mode": "none" } }
  ]
}
```

### Back-compat with the legacy `enabled` flag

The older boolean `docker_isolation.enabled` (and per-server `isolation.enabled`) still works and is mapped to a mode:

- an explicit `mode` always wins;
- otherwise `enabled: true` ⇒ `docker`, `enabled: false` ⇒ `none`;
- a missing/`nil` isolation config ⇒ `none`.

Per-server precedence: explicit per-server `mode` → per-server legacy `enabled` → global `mode` → global legacy `enabled`. A per-server `mode` (e.g. `none` for a trusted server) overrides the global gate.

### Sandbox mode (Landlock)

`sandbox` mode confines a stdio server **without Docker** by applying a Linux Landlock LSM ruleset (a writable-path allowlist) plus `setrlimit` resource caps to the process before it `exec`s, then preserving the raw stdin/stdout JSON-RPC pipes. It is unaffected by `kernel.apparmor_restrict_unprivileged_userns=1` (it needs no user namespaces), which is exactly why it works where bubblewrap/userns-based sandboxes are blocked. See the spike write-up in [docs/development/sandbox-spike-mcp-34.md](development/sandbox-spike-mcp-34.md) for the mechanism comparison and PoC.

### Scanner behaviour under each mode (MCP-34.4)

The security **scanner plugins** (Spec 039) are Docker-based. Under a non-Docker isolation mode they cannot run, so MCPProxy **degrades cleanly and surfaces it** rather than failing silently:

| Mode | Docker scanner plugins | In-process scanner (`tpa-descriptions`) | Scan result for a server with only Docker scanners |
|------|------------------------|------------------------------------------|----------------------------------------------------|
| `docker` | Run normally | Runs | As scanned |
| `sandbox` / `none` | **Skipped** with an honest, mode-specific reason pointing at [`MCPX_DOCKER_SNAP_APPARMOR`](errors/MCPX_DOCKER_SNAP_APPARMOR.md) | **Still runs** | `security_scan.status: "degraded"` (a low/zero risk score from incomplete coverage is not reported as a trustworthy all-clear) |

This is **decision D3 option (b)** from the [MCP-34 spike](development/sandbox-spike-mcp-34.md#recommendation-for-the-d3-scanner-question): clean, surfaced degradation. A native (non-Docker) scanner runtime — option (a) — is a larger follow-up and is not yet implemented. To run the full Docker-based scanner fleet, use `mode: docker` on a host with a working Docker daemon, or replace snap-docker with a distro Docker package (see the error doc).

The skip is also logged at startup:

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

## Gotcha: global flag gates per-server opt-ins

Per-server `isolation.enabled: true` only takes effect when the global `docker_isolation.enabled` flag is also `true`. If the global flag is `false`, MCPProxy runs the server on the host even if you explicitly opted it into isolation in its per-server config.

Starting in this release, MCPProxy emits a one-time warning in the main log when it detects this configuration (look for `per-server docker isolation opt-in ignored` in `~/.mcpproxy/logs/main.log`). To actually isolate those servers, flip the global flag on.

## Telemetry

When anonymous telemetry is enabled, MCPProxy reports two Docker-related counters at daily cadence:

- `server_docker_available_bool` — whether Docker is actually invocable. Reported `true` only when the `docker` CLI is **resolvable to an absolute path** *and* `docker info --format {{.ServerVersion}}` succeeds (it does **not** fall back to a bare `docker` PATH probe, which could misreport availability when the binary is only inside the macOS app bundle — see issue #696). Cached for up to 15 minutes (5 minutes when the previous probe failed, so a late Docker-Desktop launch is picked up promptly).
- `server_docker_isolated_count` — how many of your configured stdio servers are **configured** for isolation, i.e. servers for which `ShouldIsolate()` returns true. This is a configuration metric, not a count of running containers; it goes to zero whenever the global flag is off regardless of per-server opt-ins.

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
      // Isolation automatically skipped
    }
  ]
}
```

This prevents Docker-in-Docker complications.

## Debugging

### Check Docker Isolation Status

```bash
# Run with debug logging
mcpproxy serve --log-level=debug --tray=false

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

**`command not found: docker` on macOS (Docker Desktop installed):**
- Docker Desktop installed the default way leaves the `docker` CLI only inside
  the app bundle at `/Applications/Docker.app/Contents/Resources/bin/docker` —
  it is **not** on a standard `PATH` dir unless you ran the optional,
  admin-gated "install CLI tools" step. When mcpproxy is launched from a
  LaunchAgent / tray, the captured login-shell `PATH` may omit this directory.
- mcpproxy resolves the `docker` binary to its **absolute path** and then
  **exec's it directly** (no login-shell wrap) when spawning a Docker upstream —
  both servers that mcpproxy *isolates* into `docker run` (uvx/npx) and upstreams
  whose config `command` **is** `docker` (a user-supplied `docker run …`) — so
  the spawn bypasses `PATH` entirely and works even without the CLI-tools step.
  (The enhanced spawn `PATH` still includes the bundle bin dir as a
  belt-and-suspenders measure.) Earlier builds resolved the absolute path but
  still routed the spawn through `$SHELL -l -c "<docker> run …"`, where the
  login shell re-derived `PATH` from rc files and could drop the bundle dir —
  so the error persisted; direct exec fixes that. Direct exec is used only when
  (a) the resolved value is a verified absolute executable and (b) the docker
  daemon-config env is guaranteed without the login shell — on macOS via the
  startup login-shell hydration, or on any platform when `DOCKER_HOST` /
  `DOCKER_CONTEXT` are already exported into mcpproxy's environment. A
  non-absolute result (e.g. a shell function/alias from `command -v docker`), or
  a rootless/remote daemon on Linux whose `DOCKER_HOST` lives only in the
  login-shell rc, falls back to the `$SHELL -l` wrap (still using the resolved
  absolute path when one was found) so `docker run` keeps inheriting the daemon
  config. If you still see this error, confirm the binary exists at the bundle
  path above, or run Docker Desktop's "install CLI tools".
- `upstream_servers list` reports `docker_status.docker_path` (the resolved
  binary) and reports `docker_status.available` / per-server `docker_available`
  as `true` **only** when the CLI is actually resolvable *and* `docker info`
  succeeds. A `false` value with `docker_path: ""` means the CLI could not be
  resolved on the spawn path.

**`error getting credentials … docker-credential-desktop … not found in $PATH` on macOS (image not yet cached):**
- Docker Desktop's default `~/.docker/config.json` sets `"credsStore": "desktop"`,
  so `docker` shells out to `docker-credential-desktop` for **every** registry
  operation — even an anonymous pull of a public image. That helper lives in the
  **same bundle dir** as the docker CLI
  (`/Applications/Docker.app/Contents/Resources/bin/`), which mcpproxy's
  sanitized spawn `PATH` omits. When the isolation image isn't cached locally,
  the pull invokes the helper and fails; a pre-pulled image sidesteps it because
  `docker run` then performs no registry op (which is why direct-exec alone
  looked complete on cached images — issue #715 / MCP-2877).
- mcpproxy now **prepends the resolved docker binary's bundle dir to the child
  `PATH`** whenever `docker` resolves to an absolute path, so the spawned docker
  can exec its sibling tooling (`docker-credential-*`, `docker-compose`,
  `docker-buildx`) exactly as it would from a normal Docker Desktop shell. This
  is applied on every docker spawn path (isolated uvx/npx servers and
  user-supplied `docker run …` upstreams) and is a no-op when `docker` did not
  resolve to an absolute path. If you still see this error, confirm the helper
  exists at the bundle path above, or pre-pull the image with
  `docker pull <image>`.

## Snap-docker (AppArmor) failure mode

On Ubuntu hosts where Docker is installed via **snap**, AppArmor's profile transition fights the security flags the scanner sandbox requires (`--security-opt no-new-privileges` + a pinned AppArmor profile), so in-container commands fail with *operation not permitted*. This is the original driver for non-Docker `sandbox` mode. Symptoms, root cause, and fixes are documented in [`docs/errors/MCPX_DOCKER_SNAP_APPARMOR.md`](errors/MCPX_DOCKER_SNAP_APPARMOR.md). The related systemd/snap-confine variant for *upstream* docker servers is detected by `mcpproxy doctor` (issue #457).

Your options on such a host:

1. Replace snap Docker with a distro/upstream Docker package (full Docker mode works).
2. Set `docker_isolation.mode: "sandbox"` — stdio servers are confined natively with Landlock; Docker-based scanners degrade cleanly (see [Scanner behaviour](#scanner-behaviour-under-each-mode-mcp-344)).
3. Set `security.scanner_disable_no_new_privileges: true` to drop the `no-new-privileges` flag from scanner containers (weakens scanner hardening; prefer 1 or 2).

## Honest limitations

`sandbox` mode is deliberately scoped. Known limitations:

- **No uid/gid drop.** Dropping to an unprivileged uid/gid requires `CAP_SETUID`/`CAP_SETGID` (i.e. running as root). When mcpproxy runs unprivileged, the uid/gid drop is **best-effort and typically a no-op** — the sandboxed process keeps the launching user's identity. Landlock (filesystem) and `setrlimit` (resource caps) still apply. Docker mode does drop to a container user. This is an honest trade-off, not a bug.
- **Linux-only.** Landlock is a Linux 5.13+ feature. On older kernels the launcher degrades best-effort (fewer access-right bits enforced on ABI 1). On macOS/Windows `sandbox` is a documented **no-op** and behaves like `none`.
- **Filesystem + resources only.** Landlock confines the filesystem write-allowlist; it does not provide network namespacing. Pair with care for network-sensitive servers, or use `docker` mode with `network_mode: none`.
- **Docker-based scanners do not run under `sandbox`/`none`.** They are skipped (the scan reports `degraded`). A native scanner runtime is a future enhancement (D3 option a).

## Platform support matrix

| Platform | `docker` | `sandbox` | `none` | Docker scanner plugins |
|----------|----------|-----------|--------|------------------------|
| Linux (kernel ≥ 5.13) | ✅ (needs Docker daemon) | ✅ Landlock + rlimits (no uid/gid drop) | ✅ | ✅ under `docker`; skipped+degraded under `sandbox`/`none` |
| Linux (kernel < 5.13) | ✅ (needs Docker daemon) | ⚠️ best-effort: rlimits apply, Landlock partial/unavailable | ✅ | same as above |
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
