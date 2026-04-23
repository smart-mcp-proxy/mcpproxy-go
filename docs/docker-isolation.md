# Docker Security Isolation

MCPProxy provides Docker isolation for stdio MCP servers to enhance security by running each server in its own isolated container.

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

- `server_docker_available_bool` — whether the host has a reachable Docker daemon. Probed with `docker info --format {{.ServerVersion}}` and cached for up to 15 minutes (5 minutes when the previous probe failed, so a late Docker-Desktop launch is picked up promptly).
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
