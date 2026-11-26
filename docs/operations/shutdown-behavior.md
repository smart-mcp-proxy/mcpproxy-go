---
id: shutdown-behavior
title: Shutdown Behavior
sidebar_label: Shutdown Behavior
sidebar_position: 1
description: How MCPProxy handles graceful shutdown of upstream servers and subprocesses
keywords: [shutdown, graceful, sigterm, sigkill, process, cleanup, timeout]
---

# Shutdown Behavior

This document describes how MCPProxy handles graceful shutdown of upstream servers, including subprocess termination timeouts and cleanup procedures.

## Overview

When MCPProxy shuts down (via Ctrl+C, SIGTERM, or tray quit), it follows a structured cleanup process:

1. Stop accepting requests and wait for the running ones (see [Running calls](#running-calls))
2. Cancel application context (signals all background services to stop)
3. Stop OAuth refresh manager
4. Stop Supervisor (reconciliation loop)
5. Shutdown all upstream servers (graceful → force)
6. Close caches, indexes, and storage

## Running calls

Requests that are still running when shutdown starts, tool calls included, get up to **15 seconds** to finish. This includes requests from clients signed in through the browser (`/mcp/oauth`). After that they are cancelled.

Event streams (`/events` and the MCP `GET` stream) never end on their own, so they are not waited for. They close as soon as the last running call ends, or at once when there is none. An idle mcpproxy therefore does not wait at all.

| Limit | Value | What happens |
|-------|-------|--------------|
| Running calls | 15s | Cancelled |
| Core's own hard deadline | 60s | The core exits with code 6 |
| Tray waiting for the core | 65s | The core is force killed |

A second `SIGINT` or `SIGTERM` (pressing Ctrl+C again) makes the core exit immediately, without cleanup.

If you run mcpproxy under a supervisor with its own limit, give it more than these 15 seconds plus container cleanup. Docker and Compose kill a container after 10 seconds by default; the [Compose example](/operations/compose-stdio-sidecars) sets 60.

## Subprocess Shutdown Flow

For stdio-based MCP servers (processes started via `command`), MCPProxy follows the [MCP lifecycle](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle#stdio): close stdin, wait, send SIGTERM, wait, send SIGKILL. Each step runs only if the server is still there.

| Step | Native, Linux and macOS | Native, Windows | Docker isolation |
|------|-------------------------|-----------------|------------------|
| 1. Close stdin and wait | 2s | 2s | 2s |
| 2. Ask it to exit and wait | SIGTERM, 3s | `CTRL_BREAK`, 3s | `docker stop` (SIGTERM), 3s |
| 3. Kill | SIGKILL | The whole process tree | SIGKILL, by `docker stop` |

Windows has no SIGTERM. `CTRL_BREAK` is the closest to it: it ends a program that does not handle it.

A server that is removed, replaced, disabled or quarantined is different: a native process tree is killed at once, so that nothing it does can outlive the decision. A Docker container still gets the sequence above, because only its docker client is killed, which closes its stdin.

## Timeout Constants

| Constant | Value | Description |
|----------|-------|-------------|
| `stdinCloseGrace` | 2s | Time to exit after stdin was closed |
| `mcpClientCloseTimeout` | 10s | Max time for the whole sequence of a native server |
| `processGracefulTimeout` | 9s | Fallback when that did not end it: time after SIGTERM to the process group before SIGKILL |
| `processTerminationPollInterval` | 100ms | How often to check if process exited |
| `containerStopTimeout` | 3s | `--stop-timeout` of isolated containers |
| `dockerCleanupTimeout` | 30s | Max time for Docker container cleanup |

All servers are stopped in parallel. MCPProxy waits up to 10 seconds for all of them together. A server that is still stopping then keeps its own time, but MCPProxy no longer waits for it: it stops, and 5 seconds later kills, the containers that are left.

## Docker Container Shutdown

If `docker stop` fails, MCPProxy runs `docker kill`, which sends SIGKILL.

Containers run under an init process where Docker provides one, so SIGTERM normally ends them at once. These defaults can be changed per server: see [Init process and stop timeout](/features/docker-isolation#init-process-and-stop-timeout).

## Process Groups

MCPProxy uses Unix process groups to ensure all child processes are properly cleaned up:

```go
// All child processes are placed in a new process group
cmd.SysProcAttr = &syscall.SysProcAttr{
    Setpgid: true,  // Create new process group
    Pgid:    0,     // Make this process the group leader
}
```

On Windows each subprocess starts in its own console process group and in a Job Object. The `CTRL_BREAK` the tray uses to stop the core does not reach them, and closing the Job Object ends the whole process tree.

When shutting down, MCPProxy sends signals to the entire process group (`-pgid`), ensuring that:
- Child processes spawned by npm/npx are terminated
- Orphaned processes don't accumulate
- All related processes receive the shutdown signal

## What Happens During Shutdown

### When `call_tool` is called during shutdown

If an AI client tries to call a tool while MCPProxy is shutting down:

```
Error: "Server 'xxx' is not connected (state: Disconnected)"
```

Or if the server client was already removed:

```
Error: "No client found for server: xxx"
```

### When `retrieve_tools` is called during shutdown

- If the search index is still open: Returns results (possibly stale)
- After index is closed: Returns an error

### When `tools/list_changed` notification arrives during shutdown

The notification is safely ignored:
- Callback context is cancelled
- Discovery doesn't block shutdown
- Logged as a warning, no user impact

## Shutdown Order

```
Runtime.Close()
    │
    ├─► Cancel app context
    │
    ├─► Stop OAuth refresh manager
    │       └─► Prevents token refresh during shutdown
    │
    ├─► Stop Supervisor
    │       ├─► Cancel reconciliation context
    │       ├─► Wait for goroutines to exit (10s for actions in progress)
    │       └─► Close upstream adapter
    │
    ├─► ShutdownAll on upstream manager (25s total timeout)
    │       └─► For each server (parallel):
    │               ├─► Close stdin (2s)
    │               ├─► SIGTERM, CTRL_BREAK or docker stop
    │               └─► Kill if needed
    │
    ├─► Close cache manager
    │
    ├─► Close index manager
    │
    ├─► Close storage manager
    │
    └─► Close config service
```

## Debugging Shutdown Issues

### Check for orphaned processes

```bash
# After stopping MCPProxy, check for orphaned MCP server processes
pgrep -f "npx.*mcp"
pgrep -f "uvx.*mcp"
pgrep -f "node.*server"

# If found, kill them manually
pkill -f "npx.*mcp"
```

### Enable debug logging for shutdown

```bash
mcpproxy serve --log-level=debug 2>&1 | grep -E "(Disconnect|shutdown|SIGTERM|SIGKILL|process group)"
```

### View shutdown logs

Look for these log messages during shutdown:

```
INFO  Disconnecting from upstream MCP server
DEBUG Attempting graceful MCP client close
DEBUG MCP client closed gracefully               # Success!
# OR
WARN  MCP client close timed out                 # Graceful failed
INFO  Graceful close failed, force killing process group
DEBUG SIGTERM sent to process group
INFO  Process group terminated gracefully        # SIGTERM worked
# OR
WARN  Process group still running after SIGTERM, sending SIGKILL
INFO  SIGKILL sent to process group
```

## Troubleshooting

### Server processes not terminating

**Symptoms:** `npx` or `uvx` processes remain running after MCPProxy stops.

**Possible causes:**
1. Process ignoring SIGTERM (bad signal handling in MCP server)
2. Process group not properly set up
3. Zombie processes from previous crashes

**Solutions:**
- Check server logs: `mcpproxy upstream logs <server-name>`
- Manually kill orphaned processes
- Report issue if consistently reproducible

### Shutdown taking too long

**Symptoms:** MCPProxy takes 20+ seconds to shut down.

**Possible causes:**
1. Many servers running in parallel
2. Servers not responding to graceful shutdown
3. Docker containers with slow cleanup

**Solutions:**
- Check which servers are slow: enable debug logging
- Consider disabling problematic servers before shutdown
- Report consistently slow servers as bugs

### Docker containers not cleaning up

**Symptoms:** Docker containers remain running after MCPProxy stops.

**Solutions:**
```bash
# List MCPProxy containers
docker ps --filter "label=mcpproxy.managed=true"

# Force remove all MCPProxy containers
docker rm -f $(docker ps -q --filter "label=mcpproxy.managed=true")
```

## Related Documentation

- [Architecture](/development/architecture) - Runtime and lifecycle overview
- [Docker Isolation](/features/docker-isolation) - Container management
- [Upstream Servers](/configuration/upstream-servers) - Server configuration
