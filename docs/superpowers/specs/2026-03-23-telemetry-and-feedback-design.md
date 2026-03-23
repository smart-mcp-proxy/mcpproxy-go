# MCPProxy Telemetry & Feedback — Design Spec

**Date**: 2026-03-23
**Status**: Approved (brainstormed with user)
**Spec**: 036-telemetry-and-feedback

## Summary

Add anonymous usage telemetry and an in-app feedback/bug-report form to MCPProxy. Telemetry sends a minimal daily heartbeat to a Cloudflare Worker backed by D1. Feedback submissions create GitHub Issues via the same Worker.

## Goals

1. **Product decisions** — understand which features people use, how many servers/tools, version adoption
2. **Stability** — track error counts and crash signals in the wild
3. **Growth** — track active installs, retention, version distribution
4. **User voice** — make it trivially easy to suggest features or report bugs

## Decisions (from brainstorming)

| Decision | Choice |
|----------|--------|
| Telemetry backend | Cloudflare Worker + D1 |
| Feedback backend | Same Worker → GitHub Issues API |
| Opt-in/opt-out | Opt-out (enabled by default with clear notice) |
| Data tier | Minimal: heartbeat only (no tool names, URLs, or content) |
| Approach | Lightweight Go client, no third-party SDK |

## Assumptions

- The Cloudflare Worker and D1 database are set up separately (not part of this spec's implementation scope for the Go codebase). The Go side only needs an HTTP POST endpoint URL.
- The Worker's GitHub Issues creation uses a scoped PAT stored as a Worker secret — not embedded in the mcpproxy binary.
- The telemetry endpoint URL is hardcoded as `https://telemetry.mcpproxy.app/v1/heartbeat` and feedback as `https://telemetry.mcpproxy.app/v1/feedback`. These are configurable via config for development/testing.
- No PII is ever collected. The anonymous_id is a random UUID with no correlation to hardware, user identity, or IP address. The Worker strips IP before storage.
- The feature works identically in personal and server editions.

---

## Architecture

### Component Overview

```
┌─────────────┐     HTTPS POST      ┌─────────────────────┐
│  mcpproxy   │ ──────────────────→  │ Cloudflare Worker    │
│  (Go)       │   /v1/heartbeat      │                     │
│             │   /v1/feedback        │  ┌───────┐         │
│  telemetry  │                      │  │  D1   │ heartbeats│
│  service    │                      │  └───────┘         │
│             │                      │  ┌───────┐         │
│  feedback   │                      │  │GitHub │ issues   │
│  handler    │                      │  │ API   │         │
└─────────────┘                      └─────────────────────┘
```

### 1. Telemetry Service (`internal/telemetry/`)

**New package** following the `updatecheck` pattern.

#### Config (`mcp_config.json`)

```json
{
  "telemetry": {
    "enabled": true,
    "anonymous_id": "550e8400-e29b-41d4-a716-446655440000"
  }
}
```

- `enabled`: default `true`. Respects `MCPPROXY_TELEMETRY=false` env var override.
- `anonymous_id`: auto-generated UUIDv4 on first run if missing. Stored persistently.

#### Heartbeat Payload

```json
{
  "anonymous_id": "550e8400-...",
  "version": "0.21.3",
  "edition": "personal",
  "os": "darwin",
  "arch": "arm64",
  "go_version": "go1.24.10",
  "server_count": 12,
  "connected_server_count": 8,
  "tool_count": 156,
  "uptime_hours": 47,
  "routing_mode": "retrieve_tools",
  "quarantine_enabled": true,
  "timestamp": "2026-03-23T10:00:00Z"
}
```

**Excluded forever**: server names, tool names, URLs, API keys, descriptions, user content, IP-derived location.

#### Behavior

- Sends first heartbeat 5 minutes after startup (avoids noise from short-lived processes).
- Repeats every 24 hours.
- Fire-and-forget: failures are logged at DEBUG level, never shown to user.
- Timeout: 10 seconds per request.
- Skips sending if `enabled: false`, or env `MCPPROXY_TELEMETRY=false`, or version is non-semver (dev build).
- Uses `net/http` directly (no SDK dependency).

#### First-Run Notice

When `telemetry` key is absent from config on startup:
1. Auto-generate config with `enabled: true` and a fresh `anonymous_id`.
2. Log at INFO level: `"Anonymous usage telemetry enabled. Disable in Settings, config file, or 'mcpproxy telemetry disable'. Details: mcpproxy.app/telemetry"`
3. Emit a one-time `telemetry.notice` event on the event bus (Web UI shows a dismissible banner via SSE).

### 2. Feedback Handler

#### REST API Endpoint

`POST /api/v1/feedback`

```json
{
  "category": "bug",
  "message": "OAuth login fails with Cloudflare provider",
  "email": "",
  "context": {
    "version": "0.21.3",
    "edition": "personal",
    "os": "darwin",
    "arch": "arm64",
    "server_count": 12,
    "connected_server_count": 8,
    "routing_mode": "retrieve_tools"
  }
}
```

- `category`: enum `"bug" | "feature" | "other"` (required)
- `message`: string, 10-5000 chars (required)
- `email`: optional, for follow-up
- `context`: auto-populated by the backend from current runtime state (not user-supplied)

The backend proxies this to the Cloudflare Worker at `POST https://telemetry.mcpproxy.app/v1/feedback`. The Worker creates a GitHub Issue in `smart-mcp-proxy/mcpproxy-go` with:
- Title: `[{category}] {first 80 chars of message}`
- Body: full message + context block
- Labels: `user-feedback`, `bug` or `feature-request`

#### Rate Limiting

Max 5 feedback submissions per hour per instance (in-memory counter, resets on restart). Returns 429 if exceeded.

### 3. CLI Commands

```bash
mcpproxy telemetry status    # Show current telemetry config
mcpproxy telemetry enable    # Set enabled: true in config
mcpproxy telemetry disable   # Set enabled: false in config

mcpproxy feedback "OAuth login fails with Cloudflare"           # Quick bug report
mcpproxy feedback --category feature "Add SAML support"         # Feature request
mcpproxy feedback --category bug --email me@x.com "Crash on..."  # With email
```

### 4. Web UI

#### Feedback Page (`/feedback`)

New route and Vue component. Simple form:

- **Category** dropdown: Bug Report / Feature Request / Other
- **Message** textarea (required, 10-5000 chars)
- **Email** input (optional, placeholder: "For follow-up (optional)")
- **Submit** button → `POST /api/v1/feedback`
- Success: toast "Thanks! Your feedback was submitted."
- Includes a link: "You can also open an issue on GitHub"

#### NavBar Addition

Add "Feedback" item to the navigation menu after "Settings", using a speech-bubble or megaphone icon.

#### Telemetry Banner (one-time)

On first run (when `telemetry.notice` SSE event is received), show a dismissible info banner at the top of the Dashboard:

> "MCPProxy sends anonymous usage statistics to help improve the product. No personal data is collected. [Settings] [Learn more]"

Dismissed state stored in `localStorage`.

### 5. Tray Menu

Add two items after "Open Web Control Panel":
- **"Send Feedback..."** → opens `http://127.0.0.1:{port}/ui/#/feedback?apikey={key}` in browser
- **"Report Issue on GitHub"** → opens `https://github.com/smart-mcp-proxy/mcpproxy-go/issues/new`

### 6. Cloudflare Worker (out of scope for Go implementation)

Documented here for completeness. Implemented separately.

**Endpoints:**
- `POST /v1/heartbeat` — validate payload, strip IP, insert into D1 `heartbeats` table
- `POST /v1/feedback` — validate payload, create GitHub Issue via API, return issue URL
- `GET /v1/stats` — (admin, API-key-protected) aggregate stats for internal dashboards

**D1 Schema:**
```sql
CREATE TABLE heartbeats (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  anonymous_id TEXT NOT NULL,
  version TEXT NOT NULL,
  edition TEXT,
  os TEXT,
  arch TEXT,
  server_count INTEGER,
  connected_server_count INTEGER,
  tool_count INTEGER,
  uptime_hours INTEGER,
  routing_mode TEXT,
  quarantine_enabled BOOLEAN,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_heartbeats_date ON heartbeats(created_at);
CREATE INDEX idx_heartbeats_version ON heartbeats(version);
```

---

## File Structure

```
internal/telemetry/
  telemetry.go          # TelemetryService: config, heartbeat loop, HTTP client
  telemetry_test.go     # Unit tests with httptest server
  feedback.go           # Feedback submission logic + rate limiter
  feedback_test.go      # Feedback tests

internal/httpapi/
  feedback.go           # POST /api/v1/feedback handler
  feedback_test.go      # Handler tests

cmd/mcpproxy/
  telemetry_cmd.go      # telemetry enable/disable/status commands
  feedback_cmd.go       # feedback submit command

frontend/src/
  views/Feedback.vue    # Feedback form page
  router/index.ts       # Add /feedback route
  components/NavBar.vue # Add Feedback menu item
  components/TelemetryBanner.vue  # One-time dismissible banner

internal/tray/
  tray.go               # Add feedback menu items (modify existing)

internal/config/
  config.go             # Add TelemetryConfig struct (modify existing)
```

## Testing Strategy

- **Unit tests**: TelemetryService with httptest mock server (heartbeat send/retry/skip logic), feedback validation, rate limiting.
- **Integration test**: Full flow from CLI → API → mock Worker endpoint.
- **Config tests**: Telemetry enable/disable, anonymous_id generation, env var override.
- **No E2E against real Worker** — mock the external endpoint in all tests.

## Security & Privacy

- No PII collected. Anonymous UUID has no correlation to user identity.
- Cloudflare Worker strips source IP before D1 storage.
- Feedback email is optional and only used for the GitHub Issue body.
- No data is sent when telemetry is disabled.
- The telemetry endpoint URL is HTTPS only.
- No third-party analytics SDK in the binary.
- `MCPPROXY_TELEMETRY=false` environment variable provides a system-level kill switch.

## Out of Scope

- Cloudflare Worker implementation (separate repo/deployment)
- Analytics dashboard / visualization
- A/B testing or feature flags based on telemetry
- Session tracking or behavioral analytics
- Crash reporting (future enhancement)
