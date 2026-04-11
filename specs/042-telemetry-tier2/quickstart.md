# Quickstart: Telemetry Tier 2

How to build, run, and verify Tier 2 changes.

## Build

```bash
# From the repo or worktree root
go build ./cmd/mcpproxy                       # Personal edition
go build -tags server ./cmd/mcpproxy          # Server edition
```

Both must compile cleanly with no errors or warnings. CI will reject either failing.

## Unit tests

```bash
go test ./internal/telemetry/... -race -v
go test ./internal/httpapi/... -race -v
```

The full test suite:

```bash
go test -race ./internal/... -v
```

## Show the next telemetry payload

```bash
mcpproxy telemetry show-payload | jq .
```

Sample output:

```json
{
  "schema_version": 2,
  "anonymous_id": "550e8400-e29b-41d4-a716-446655440000",
  "anonymous_id_created_at": "2026-04-10T12:00:00Z",
  "version": "v0.21.0-dev",
  "current_version": "v0.21.0-dev",
  "previous_version": "",
  "edition": "personal",
  "os": "darwin",
  "arch": "arm64",
  "go_version": "go1.24.10",
  "server_count": 5,
  "connected_server_count": 4,
  "tool_count": 87,
  "uptime_hours": 0,
  "routing_mode": "dynamic",
  "quarantine_enabled": true,
  "timestamp": "2026-04-10T12:34:56Z",
  "surface_requests": { "mcp": 0, "cli": 0, "webui": 0, "tray": 0, "unknown": 0 },
  "builtin_tool_calls": {},
  "upstream_tool_call_count_bucket": "0",
  "rest_endpoint_calls": {},
  "feature_flags": {
    "enable_web_ui": true,
    "enable_socket": true,
    "require_mcp_auth": false,
    "enable_code_execution": false,
    "quarantine_enabled": true,
    "sensitive_data_detection_enabled": true,
    "oauth_provider_types": []
  },
  "last_startup_outcome": "success",
  "error_category_counts": {},
  "doctor_checks": {}
}
```

This command makes **no** network call. It is safe to run with telemetry fully disabled.

## Disable telemetry

Three ways, in precedence order (first match wins):

```bash
DO_NOT_TRACK=1 mcpproxy serve         # Honors the consoledonottrack.com convention
CI=true mcpproxy serve                # Auto-detected in GitHub Actions, GitLab, etc.
MCPPROXY_TELEMETRY=false mcpproxy serve  # Existing v1 mechanism
mcpproxy telemetry disable            # Persisted via config edit
```

Verify it's disabled:

```bash
mcpproxy telemetry status
# Should print: "Telemetry: disabled (DO_NOT_TRACK env var)"  (or similar)
```

## Verify a counter increments

In one terminal:

```bash
mcpproxy serve --log-level=debug
```

In another terminal:

```bash
# Hit a built-in tool via the MCP endpoint (uses curl as a stand-in)
curl -X POST http://127.0.0.1:8080/mcp ... # see test-api-e2e.sh for the exact request

# Or hit a REST endpoint
curl -H "X-API-Key: $(cat ~/.mcpproxy/mcp_config.json | jq -r .api_key)" \
  -H "X-MCPProxy-Client: cli/test" \
  http://127.0.0.1:8080/api/v1/status

# Render payload again
mcpproxy telemetry show-payload | jq '.surface_requests, .rest_endpoint_calls'
```

You should see `surface_requests.cli` incremented and `rest_endpoint_calls."GET /api/v1/status"."2xx"` incremented.

## Verify privacy: forbidden-substring assertion

```bash
go test -run TestPayloadHasNoForbiddenSubstrings ./internal/telemetry/... -v
```

This test renders a fully populated payload and asserts that none of the forbidden substrings (`/Users/`, `localhost`, `Bearer `, etc.) appear anywhere in the JSON.

## Verify the first-run notice

```bash
# Wipe the notice flag
jq 'del(.telemetry.notice_shown)' ~/.mcpproxy/mcp_config.json > /tmp/cfg.json && mv /tmp/cfg.json ~/.mcpproxy/mcp_config.json

mcpproxy serve 2>&1 | head -10
# Should print the telemetry notice on stderr

mcpproxy serve 2>&1 | head -10
# Should NOT print the notice the second time
```

## Verify upstream tool names are NOT in the payload

```bash
# Configure a server with a deliberately distinctive name
jq '.mcpServers += [{"name": "MY-CANARY-SERVER", "url": "...", "enabled": true}]' \
  ~/.mcpproxy/mcp_config.json > /tmp/cfg.json && mv /tmp/cfg.json ~/.mcpproxy/mcp_config.json

mcpproxy serve &
# ... call any upstream tool from MY-CANARY-SERVER ...

mcpproxy telemetry show-payload | grep -i canary
# Should print NOTHING. If it prints anything, the privacy contract is broken.
```

## Run the full E2E suite

```bash
./scripts/test-api-e2e.sh
```

Should pass with zero new failures.
