# Anonymous Telemetry

MCPProxy collects anonymous usage statistics to help improve the product. This page explains what is collected, what is not, and how to disable it.

## What is collected

MCPProxy sends a **daily heartbeat** containing only aggregate, non-identifying information. The current schema is **version 3** (`schema_version: 3` in the JSON payload); the schema is forward-compatible so older consumers simply ignore fields they don't recognize.

| Field | Example | Purpose |
|-------|---------|---------|
| `anonymous_id` | `550e8400-...` | Random UUID for deduplication (not linked to you) |
| `version` | `0.21.3` | Track version adoption |
| `edition` | `personal` | Understand edition usage |
| `os` | `darwin` | Platform distribution |
| `arch` | `arm64` | Architecture distribution |
| `server_count` | `12` | Understand scale of usage |
| `connected_server_count` | `8` | Connection success rates |
| `tool_count` | `156` | Tool ecosystem size |
| `uptime_hours` | `47` | Usage patterns |
| `routing_mode` | `retrieve_tools` | Feature adoption |
| `quarantine_enabled` | `true` | Security feature adoption |
| `feature_flags.docker_available` | `true` | Fraction of installs with a reachable Docker daemon (schema v3) |
| `server_protocol_counts` | `{"stdio":3,"http":2,"sse":0,"streamable_http":1,"auto":0}` | Ratio of remote-HTTP vs local-stdio upstreams (schema v3) |
| `server_docker_isolated_count` | `2` | How many configured servers the runtime actually wraps in Docker isolation (schema v3) |

The `server_protocol_counts` map uses a **fixed enum of keys** (`stdio`, `http`, `sse`, `streamable_http`, `auto`) — server names and URLs are never included. Unknown or misconfigured protocol values are bucketed into `auto`.

## What is NOT collected

The following is **never** collected:

- Server names, URLs, or configurations
- Tool names or descriptions
- API keys, tokens, or credentials
- File paths or environment variables
- IP addresses (stripped by our server before storage)
- User identity, email, or account information
- Tool call content, arguments, or responses
- Any user-generated content

## Anonymous ID

The anonymous ID is a random UUID (v4) generated on first run. It has **no correlation** to your hardware, user account, or identity. It exists solely to deduplicate heartbeats (so we don't count the same install twice in a day).

You can delete it by removing the `telemetry.anonymous_id` field from your config — a new random ID will be generated on next startup.

## How to disable

There are three ways to disable telemetry:

### 1. CLI (recommended)

```bash
mcpproxy telemetry disable
```

Verify with:
```bash
mcpproxy telemetry status
```

Re-enable anytime:
```bash
mcpproxy telemetry enable
```

### 2. Configuration file

Edit `~/.mcpproxy/mcp_config.json`:

```json
{
  "telemetry": {
    "enabled": false
  }
}
```

### 3. Environment variable

```bash
export MCPPROXY_TELEMETRY=false
```

This overrides the config file setting and is useful for CI/CD environments or system-wide policies.

## Data handling

- Telemetry data is sent to a Cloudflare Worker over HTTPS
- Source IP addresses are stripped before storage
- Data is stored in Cloudflare D1 (EU region)
- Used only for aggregate product analytics
- No third-party analytics services receive the data

## Source code

The telemetry implementation is fully open-source:

- [`internal/telemetry/telemetry.go`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/internal/telemetry/telemetry.go) — heartbeat logic
- [`internal/config/config.go`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/internal/config/config.go) — configuration (`TelemetryConfig`)
