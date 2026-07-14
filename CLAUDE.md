# CLAUDE.md

Guidance for Claude Code / AI agents working in this repo. **This file is loaded into every session and every Paperclip heartbeat — keep it lean.** It is orientation + behavior only; detailed reference lives in `docs/`.

## Autonomous Operation Constraints

### Must-Do (Defaults & Assumptions)
- **Zero Interruption Policy**: If a decision is needed and no explicit instruction exists, make an informed, safe assumption based on idiomatic Go best practices and document it in the PR/commit. Do NOT ask for human clarification mid-task.
- **Test-Driven Progress**: Write a failing Go test (`_test.go`) for every sub-task before implementing the feature.
- **Graceful Fallbacks**: If an API or dependency lacks documentation, use mock interfaces or a simplified implementation rather than blocking the task.

### Must-Nots
- **Do NOT ask for plan approval**: Once a plan/spec is generated, begin execution immediately.
- **Do NOT stop for code style choices**: Run `gofmt`/`goimports` and follow standard Go conventions.

### Escalation Triggers (Stop Conditions)
Only halt and ask a human IF:
1. You need destructive data operations or to delete core proxy logic that cannot be mocked.
2. A required environment variable is missing from `.env` and cannot be mocked for the task's scope.
3. You are stuck in an error loop for the same `go test` failing after 5 consecutive attempts.
4. **Cross-model (Codex) review round cap — per PR:** when a PR is gated by a cross-model Codex review, run at most **5 fix→re-review rounds on that PR**. If Codex has not returned a clean verdict after the 5th round, STOP and ask the human how to proceed (do not auto-run round 6). The counter is per-PR and resets for each new PR. (Verify each Codex finding is genuine before fixing — Codex can false-positive; a round only counts when you push a fix and re-review.)

## Project Overview

MCPProxy is a Go desktop application that acts as a smart proxy for AI agents using the Model Context Protocol (MCP): intelligent tool discovery, massive token savings, and built-in security quarantine against malicious MCP servers.

**Stack**: Go 1.24 (backend) · TypeScript 5.9 / Vue 3.5 (frontend) · Swift 5.9 (macOS tray). Storage: BBolt (`config.db`) + Bleve (search index). Avoid new dependencies without clear need.

## Editions (Personal & Server)

Built in two editions from one codebase via Go build tags:

| Edition | Build | Binary | Distribution |
|---------|-------|--------|--------------|
| **Personal** (default) | `go build ./cmd/mcpproxy` | `mcpproxy` | macOS DMG, Windows installer, Linux tar.gz |
| **Server** | `go build -tags server ./cmd/mcpproxy` | `mcpproxy-server` | Docker image, .deb, Linux tar.gz |

All server code is behind `//go:build server` in `internal/serveredition/`; the personal edition is unaffected. The binary self-identifies (`mcpproxy version`, `/api/v1/status` → `"edition"`). Server multi-user OAuth (Spec 024): see [docs/development/server-edition-multiuser-auth.md](docs/development/server-edition-multiuser-auth.md).

> Every feature decision should ask: "Does this make the personal edition so good that developers tell their teammates about it?"

## Architecture

**Core + Tray split**: `mcpproxy` (headless HTTP API + MCP proxy) and `mcpproxy-tray` (GUI that manages the core). The tray is a UI controller — it holds no state; it reads/writes core config via REST + SSE. Tray↔core over a Unix socket (`~/.mcpproxy/mcpproxy.sock`) / named pipe on Windows; socket connections bypass the API key (OS-level auth), TCP requires it.

| Directory | Purpose |
|-----------|---------|
| `cmd/mcpproxy/` | CLI entry point (Cobra) |
| `cmd/mcpproxy-tray/` | System tray app (state machine) |
| `internal/runtime/` | Lifecycle, event bus, background services |
| `internal/server/` | HTTP server, MCP proxy |
| `internal/httpapi/` | REST API (`/api/v1`) |
| `internal/upstream/` | 3-layer client: core/managed/cli |
| `internal/config/` | Configuration management |
| `internal/index/` | Bleve BM25 search index |
| `internal/storage/` | BBolt database |
| `internal/oauth/` | OAuth 2.1 + PKCE |
| `internal/security/` | Sensitive-data detection + quarantine |
| `internal/serveredition/` | Server-only code (`//go:build server`) |
| `native/macos/MCPProxy/` | Swift macOS tray app |

See [docs/architecture.md](docs/architecture.md) and [docs/socket-communication.md](docs/socket-communication.md).

## Development Commands

```bash
# Build
go build -o mcpproxy ./cmd/mcpproxy                       # core (personal)
go build -tags server -o mcpproxy-server ./cmd/mcpproxy   # core (server edition)
make build                                                # frontend + backend
make build-docker                                         # server Docker image

# Test — ALWAYS run before committing
./scripts/test-api-e2e.sh                                 # quick API E2E (required)
go test -race ./internal/... -v                           # unit + race
go test -tags server ./internal/serveredition/... -race   # server edition
./scripts/run-all-tests.sh                                # full suite

# Lint — CI uses golangci-lint v2 with .github/.golangci.yml, which is STRICTER
# than the local scripts/run-linter.sh (v1.x) and catches things it misses.
# Run the v2 binary before pushing:
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...

# Run
./mcpproxy serve [--listen :8080] [--log-level=debug]     # core (localhost:8080)
./mcpproxy-tray                                           # tray (auto-starts core)
```

**CLI management** — `mcpproxy upstream|tools|activity|token|telemetry|feedback|doctor …`. Output: `-o json|yaml`, `MCPPROXY_OUTPUT=json`, `--help-json` (machine-readable for agents). References: [docs/cli-management-commands.md](docs/cli-management-commands.md) · [docs/cli/activity-commands.md](docs/cli/activity-commands.md) · [docs/features/agent-tokens.md](docs/features/agent-tokens.md) · [docs/cli-output-formatting.md](docs/cli-output-formatting.md).

**Verifying Web-UI changes** (Playwright sweep + HTML report) — required when touching `frontend/src/`: [docs/development/web-ui-verification.md](docs/development/web-ui-verification.md).

## Configuration

Default locations: Config `~/.mcpproxy/mcp_config.json` · Data `~/.mcpproxy/config.db` (BBolt) · Index `~/.mcpproxy/index.bleve/` · Logs `~/.mcpproxy/logs/`.

```json
{
  "listen": "127.0.0.1:8080",
  "api_key": "auto-generated-if-empty",
  "require_mcp_auth": false,
  "enable_socket": true,
  "enable_web_ui": true,
  "mcpServers": [
    { "name": "github", "url": "https://api.github.com/mcp", "protocol": "http", "enabled": true },
    { "name": "ast-grep", "command": "npx", "args": ["ast-grep-mcp"], "working_dir": "/path", "protocol": "stdio", "enabled": true }
  ]
}
```

Env vars: `MCPPROXY_LISTEN`, `MCPPROXY_API_KEY`, `MCPPROXY_DEBUG`, `MCPPROXY_TELEMETRY=false`, `HEADLESS`. Full reference: [docs/configuration.md](docs/configuration.md).

## MCP Protocol

**Built-in tools**: `retrieve_tools` (BM25 search across upstream tools; Spec 049 opt-in `include_disabled`) · `call_tool_read|write|destructive` (Spec 018 intent variants; operation type inferred from the variant) · `code_execution` (sandboxed JS, off by default) · `upstream_servers` (CRUD, Spec 049) · `quarantine_security` (Spec 032). **Tool format**: `<serverName>:<toolName>` (e.g. `github:create_issue`).

**REST API** base `/api/v1`, auth via `X-API-Key` header or `?apikey=`. MCP endpoints (`/mcp`) stay unprotected for client compatibility; the REST API always requires a key (auto-generated if absent). All responses carry `X-Request-Id` (correlate with `mcpproxy activity list --request-id <id>`). Live updates via SSE at `/events`. Full endpoint list: `oas/swagger.yaml` + [docs/api/rest-api.md](docs/api/rest-api.md).

All server responses include a unified `health` field: `level` (healthy|degraded|unhealthy), `admin_state` (enabled|disabled|quarantined), plus `summary`/`detail`/`action`.

**Connect payload (Spec 075)**: `GET /api/v1/connect` is content-read-free (stat-only; no macOS App-Data prompt) — each `ClientStatus` carries `access_state="unknown"`. `GET /api/v1/connect/{client}` resolves it on-demand to `accessible|absent|malformed|denied` (+ `remediation` when denied); a denied connect/disconnect returns `403` with remediation. See [docs/api/rest-api.md](docs/api/rest-api.md#connect-client-wizard).

## Security Model

- **Localhost-only by default** (`127.0.0.1:8080`); **API key always required** (auto-generated and persisted if not provided).
- **Agent tokens**: scoped credentials for AI agents (`mcp_agt_` prefix, HMAC-SHA256 hashed). See [docs/features/agent-tokens.md](docs/features/agent-tokens.md).
- **Quarantine**: new servers quarantined until approved; Tool Poisoning Attack (TPA) detection on descriptions. **Tool-level quarantine (Spec 032)**: SHA-256 hashes detect new ("pending") and changed ("changed", rug-pull) tools. Trusted (non-quarantined) servers auto-approve their current toolset as a baseline; post-baseline changes/additions are reviewed unless per-server `auto_approve_tool_changes:true` (MCP-2931, deprecates `skip_quarantine`). Config: `quarantine_enabled` (global), `auto_approve_tool_changes` (per-server). See [docs/features/security-quarantine.md](docs/features/security-quarantine.md).
- **`require_mcp_auth`**: when enabled, `/mcp` rejects unauthenticated requests (default off, for back-compat).
- **Sensitive-data detection** (`internal/security/`): scans tool args/responses for secrets (cloud creds, private keys, API tokens, DB strings, Luhn-validated cards, sensitive file paths, high-entropy strings). On by default; integrates with the activity log. Config under `sensitive_data_detection`. See [docs/features/sensitive-data-detection.md](docs/features/sensitive-data-detection.md).

## Key Implementation Details

- **Docker isolation**: runtime detection (uvx→Python, npx→Node), image selection, container lifecycle. [docs/docker-isolation.md](docs/docker-isolation.md)
- **OAuth**: dynamic port allocation, RFC 8252 + PKCE, `internal/oauth/coordinator.go`, automatic token refresh. [docs/oauth-resource-autodetect.md](docs/oauth-resource-autodetect.md)
- **Code execution**: sandboxed JavaScript (ES2020+) orchestrating multiple upstream tools in one request. [docs/code_execution/overview.md](docs/code_execution/overview.md)
- **Connection management**: exponential backoff; state machine Disconnected → Connecting → Authenticating → Ready.
- **Tool indexing**: full rebuild on server changes, hash-based change detection, background indexing.
- **Tool-level quarantine (Spec 032)** key files: `internal/storage/models.go` & `bbolt.go`, `internal/runtime/tool_quarantine.go`, `internal/runtime/lifecycle.go` (`applyDifferentialToolUpdate`), `internal/server/mcp.go`, `internal/config/config.go`, `frontend/src/views/ServerDetail.vue`.
- **Signal handling**: graceful shutdown, context cancellation, Docker cleanup, double-shutdown protection. **Before running the core, kill existing instances — it locks the DB.**

## Debugging

```bash
mcpproxy doctor                           # quick diagnostics
mcpproxy upstream list                    # server status
mcpproxy upstream logs <name> --follow    # per-server logs
tail -f ~/Library/Logs/mcpproxy/main.log  # main log (macOS; Linux: ~/.mcpproxy/logs/main.log)
```

**Exit codes**: 0 success · 1 general · 2 port conflict · 3 DB locked · 4 config · 5 permission.

## Development Guidelines

- File organization: `internal/` subdirectories, Go conventions. Tests: `*_test.go`; E2E in `internal/server/e2e_test.go`. E2E prereqs: Node.js, npm, jq, a built `mcpproxy` binary.
- Error handling: structured logging (zap), context wrapping, graceful degradation.
- Config changes: update both storage and file system; the file watcher hot-reloads.
- **macOS tray dev** (build / replace / verify with `mcpproxy-ui-test`): [docs/development/macos-tray.md](docs/development/macos-tray.md).
- **Windows installer**: [docs/github-actions-windows-wix-research.md](docs/github-actions-windows-wix-research.md). **Prerelease** (`next` branch + `v*-rc.*` tags, opt-in, off stable channels): [docs/prerelease-builds.md](docs/prerelease-builds.md).

## Recent Changes
- 084-toon-output: Adaptive TOON encoding of `call_tool_*` result text blocks (`internal/toonenc/`, seam in `internal/server/toon_encode.go`). Config `toon_output` (off|adaptive|always, per-server override) + `toon_min_savings_pct`; off by default, hot-reloadable; never-larger by construction; sanitise-before-encode, detection-parity, truncate-after-encode invariants. **New dependency: `github.com/toon-format/toon-go`** (the production encoder; shared with the spec-083 profiler). Docs: [docs/features/toon-output.md](docs/features/toon-output.md).
- 083-discovery-profiler: Added Go 1.24 (bench package, same module as mcpproxy-go); Node.js ≥20 for the TSCG arm subprocess (CI-provided, matches existing E2E prereqs); Python via `uv` for dataset fetch + LAP (CI only)
- 077-scanner-simplification: Added Go 1.24 (backend/core), TypeScript 5.9 / Vue 3.5 (frontend Web UI) + Existing only — `internal/security/detect` (stdlib + `golang.org/x/text/unicode/norm`, already an indirect dep), `internal/security/scanner`, BBolt (scanner records + tool approvals), Bleve (index, untouched), zap (logging). **No new third-party dependency.**
- 076-deterministic-tool-scanner: Added Go 1.24 + stdlib only for detection (`unicode`, `unicode/utf8`, `encoding/base64`, `encoding/hex`, `regexp`); `golang.org/x/text/unicode/norm` (already an indirect dep via x/text) for NFKC; existing `internal/security/patterns/`, `internal/security/scanner/`, `internal/runtime/tool_quarantine.go`. No new third-party dependency.
