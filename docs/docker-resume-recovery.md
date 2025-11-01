# Docker Resume Recovery Improvements

This branch introduces a coordinated recovery path for the tray and runtime when Docker Desktop is paused and resumed.

## Tray Enhancements
- Added a pre-launch Docker health probe. The tray now waits until `docker info` succeeds before launching the core process, avoiding repeated failures when Docker is down.
- Introduced a polling loop (5s cadence) that detects when Docker becomes available again. Once recovered, the tray sets a pending reconnect flag and continues startup.
- Added a new `error_docker` connection state with explicit UX messaging so the tray menu and tooltip explain that Docker Desktop is unavailable.
- When the core transitions back to `Connected`, and a Docker recovery was detected, the tray calls a new HTTP API endpoint (`POST /api/v1/servers/reconnect`) to trigger fast upstream reconnection.

## HTTP API and Runtime Changes
- Added `ForceReconnectAllServers(reason string)` to the runtime and exposed it through the HTTP controller to support the new `/servers/reconnect` route.
- The runtime delegates to `upstream.Manager.ForceReconnectAll`, which now rebuilds any disconnected, enabled managed client. The manager clones each server configuration, removes the old client, waits briefly for cleanup, and recreates the client so container state is refreshed.
- Existing, already connected HTTP/SSE/stdio clients are left untouched, so only affected Docker-backed servers restart.

## Upstream Manager & Client Updates
- Added `Client.ForceReconnect` so the manager can bypass exponential backoff when forced.
- Implemented safe configuration cloning in the manager to avoid mutating shared configuration when recreating clients.
- Ensured managed clients skip force reconnect if they are already connected or currently connecting.

## Docker Cleanup Reliability
- Increased the timeout for container shutdown/kill operations from 5–10 seconds to a shared 30-second budget. This prevents the manager from launching a new container while the previous cleanup is still in progress.

## API Client Support
- The tray’s API adapter gained `ForceReconnectAllServers`, which works over both TCP and Unix socket transports, ensuring the new recovery path functions regardless of how the tray connects to the core.

## Testing
- `go test ./internal/upstream/...`
- `go test ./internal/httpapi`
- `go test ./internal/runtime/...`
- `go test ./cmd/mcpproxy-tray/...` *(fails on this machine due to linker disk-space error; code compiles otherwise)*

## Expected Behaviour
- When Docker Desktop is paused, the tray surfaces an explicit error state instead of hanging.
- After Docker resumes, the tray immediately triggers upstream reconnection; Docker-backed servers begin launching within a couple of seconds.
- HTTP-only or stdio servers continue operating throughout the outage and are not needlessly restarted.
