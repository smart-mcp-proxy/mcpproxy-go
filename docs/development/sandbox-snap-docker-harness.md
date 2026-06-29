# Sandbox Mode: Manual Snap-Docker Harness (MCP-34.5)

This document proves **exit criterion #4 of MCP-34**: reproducing the GH #71
snap-Docker AppArmor failure with `mode: docker`, then showing `mode: sandbox`
succeeds as a drop-in replacement on an Ubuntu host where Docker is installed
via snap.

## Background

Snap-installed Docker ships with an AppArmor profile that enforces
`no-new-privileges`. This profile is inherited by any container the snap Docker
daemon launches, including the scanner containers mcpproxy uses for security
analysis. When mcpproxy tries to run a scanner container, Docker rejects the
`setuid`/`setgid` syscalls the container needs, producing an AppArmor denial.
See [docs/errors/MCPX_DOCKER_SNAP_APPARMOR.md](../errors/MCPX_DOCKER_SNAP_APPARMOR.md).

`isolation.mode: sandbox` avoids Docker entirely: stdio servers run under the
native Landlock+rlimit wrapper (`mcpproxy __sandbox_exec -- <cmd>`) and scanner
containers are cleanly skipped with an honest "degraded" status rather than
failing noisily.

## Prerequisites

- Ubuntu 22.04 or 24.04 (kernel 5.15+ or 6.8+, Landlock ≥ ABI 1)
- mcpproxy binary built (`make build` or `go build ./cmd/mcpproxy`)
- An `npx` stdio server available (e.g. `@modelcontextprotocol/server-everything`)

```bash
# Install snap Docker
sudo snap install docker
sudo adduser $USER docker
newgrp docker
docker --version  # e.g. Docker version 27.x.x
```

## Step 1 — Negative Baseline: `mode: docker` fails

Configure mcpproxy with `mode: docker` and a simple stdio server:

```bash
mkdir -p /tmp/harness-docker
cat > /tmp/harness-docker/mcp_config.json <<'EOF'
{
  "listen": "127.0.0.1:18080",
  "api_key": "harness-key",
  "enable_web_ui": false,
  "docker_isolation": { "mode": "docker" },
  "mcpServers": [
    {
      "name": "everything",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"],
      "protocol": "stdio",
      "enabled": true
    }
  ]
}
EOF

MCPPROXY_DATA_DIR=/tmp/harness-docker ./mcpproxy serve \
  --config /tmp/harness-docker/mcp_config.json \
  --log-level=debug 2>&1 &
DOCKER_PID=$!
sleep 5
```

Trigger a security scan:

```bash
curl -sf -H "X-API-Key: harness-key" \
  http://127.0.0.1:18080/api/v1/servers/everything/scan | python3 -m json.tool
```

Expected result: `security_scan` field shows `"failed"` or `"error"` with a
message referencing AppArmor / `no-new-privileges`. The `everything` server
itself may work, but scanner containers fail to run.

In the mcpproxy log you will see lines similar to:

```
ERROR  scanner failed  {"error": "OCI runtime exec failed: ... apparmor='DENIED' ..."}
```

This reproduces GH #71.

```bash
kill $DOCKER_PID 2>/dev/null
```

## Step 2 — Positive Case: `mode: sandbox` succeeds

Switch to `mode: sandbox`. The same stdio server now runs under Landlock
confinement instead of Docker:

```bash
mkdir -p /tmp/harness-sandbox
cat > /tmp/harness-sandbox/mcp_config.json <<'EOF'
{
  "listen": "127.0.0.1:18081",
  "api_key": "harness-key",
  "enable_web_ui": false,
  "docker_isolation": { "mode": "sandbox" },
  "mcpServers": [
    {
      "name": "everything",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"],
      "protocol": "stdio",
      "enabled": true
    }
  ]
}
EOF

MCPPROXY_DATA_DIR=/tmp/harness-sandbox ./mcpproxy serve \
  --config /tmp/harness-sandbox/mcp_config.json \
  --log-level=debug 2>&1 &
SANDBOX_PID=$!
sleep 5
```

Verify server health:

```bash
curl -sf -H "X-API-Key: harness-key" \
  http://127.0.0.1:18081/api/v1/status | python3 -m json.tool
```

Expected: `"running": true`, `"health": {"level": "healthy"}`.

Verify the everything server is up:

```bash
curl -sf -H "X-API-Key: harness-key" \
  http://127.0.0.1:18081/api/v1/servers | python3 -m json.tool
```

Expected: `"status": "connected"` for the `everything` server.

Check the mcpproxy log for the sandbox wrapper message:

```bash
grep -i "sandbox isolation enabled\|Landlock\|sandbox" \
  /tmp/harness-sandbox/*.log 2>/dev/null || \
  journalctl --no-pager -n 50 _PID=$SANDBOX_PID 2>/dev/null
```

Expected: `sandbox isolation enabled for server (Landlock + rlimits)` for the
`everything` server.

Trigger a tool call through the proxy to confirm end-to-end stdio works:

```bash
# Initialize MCP session
HEADERS_FILE=$(mktemp)
curl -sf -D "$HEADERS_FILE" -o /tmp/init.json \
  -X POST http://127.0.0.1:18081/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"harness","version":"1.0"}}}'
SESSION=$(grep -i 'mcp-session-id' "$HEADERS_FILE" | awk '{print $2}' | tr -d '\r')

# Search for tools
curl -sf -X POST http://127.0.0.1:18081/mcp \
  -H "Content-Type: application/json" \
  -H "Mcp-Session-Id: $SESSION" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"retrieve_tools","arguments":{"query":"echo","limit":3}}}' \
  | python3 -m json.tool
```

Expected: tool results returned from the `everything` server running under
Landlock confinement.

Check the scan status — scanner containers are intentionally skipped but the
result is `"degraded"` (not failed) because the in-process TPA scanner still
runs:

```bash
curl -sf -H "X-API-Key: harness-key" \
  http://127.0.0.1:18081/api/v1/servers/everything/scan | python3 -m json.tool
```

Expected: `"security_scan": "degraded"`, `"findings": []`, no AppArmor errors.

```bash
kill $SANDBOX_PID 2>/dev/null
```

## Step 3 — Write-Allowlist + Rlimit Assertions

These are proven by the automated unit tests:

```bash
# Run the full Landlock enforcement test suite (Linux only)
go test -v -race \
  ./internal/sandbox/... \
  ./internal/upstream/core/... \
  ./internal/security/scanner/...
```

Key tests:

| Test | What it proves |
|------|---------------|
| `TestLandlockEnforcesFilesystemAllowlist` | Writes inside RW allowlist succeed; reads outside denied |
| `TestSandboxWrapper_EndToEnd` | Full re-exec path: write outside denied, rlimit applied, stdin→stdout passthrough |
| `TestSandboxWrapper_FailClosed` | Without spec, child refuses to exec (fail-closed) |
| `TestEngineResolveScannersSkipsDockerUnderSandbox` | Docker scanners prefailed under `mode=sandbox`; in-process still runs |
| `TestEngineEffectiveIsolationMode` | SetIsolationMode / resolver wiring |

## CI Coverage

The dedicated `sandbox-integration.yml` workflow runs on `ubuntu-latest`
(Ubuntu 24.04, kernel 6.8, Landlock ABI 3) on every push that touches sandbox
code. It covers:

1. `internal/sandbox/...` — Landlock enforcement tests
2. `internal/upstream/core/...` — wrapper integration tests
3. `internal/security/scanner/...` — isolation-mode degradation tests
4. Server startup probe with `isolation.mode: sandbox`
5. Cross-compile probe for darwin (no-op path)

The existing `unit-tests.yml` additionally runs all of these tests as part of
the full `go test -v -race ./...` sweep on ubuntu-latest.

## Snap-Docker CI Note

Snap Docker in GitHub Actions containers is unreliable — the snap daemon
(`snapd`) does not start cleanly inside most CI container images, and the
`no-new-privileges` AppArmor failure is a snap-host-specific behavior that
requires a full Ubuntu installation with snapd running as a systemd service. The
manual harness above (Steps 1–2) is the documented reproduction path for the
negative baseline.

The positive case (sandbox mode works) is fully covered by CI on ubuntu-latest
without snap Docker, because the sandbox path does not involve Docker at all.
