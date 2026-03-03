# Research: Status Command

**Date**: 2026-03-02 | **Branch**: `027-status-command`

## Key Findings

### 1. Existing API Endpoints

**Decision**: Use both `/api/v1/status` and `/api/v1/info` endpoints to gather live data.

**Rationale**: `/api/v1/status` provides `running`, `listen_addr`, `upstream_stats`, and `timestamp`. `/api/v1/info` provides `version`, `web_ui_url` (with embedded API key), and `endpoints` (socket path). Together they cover all fields needed for the status output.

**Alternatives considered**:
- Create a new dedicated `/api/v1/cli-status` endpoint: Rejected - unnecessary when existing endpoints provide all data
- Merge into single endpoint: Rejected - would change existing API contracts

### 2. Client Method Strategy

**Decision**: Add a `GetStatus()` method to `cliclient.Client` that calls `/api/v1/status`, reuse existing `GetInfo()` for info data.

**Rationale**: `GetInfo()` already exists at line 490 of `client.go`. Only `GetStatus()` is missing. The status command will make two client calls and merge results.

**Alternatives considered**:
- Single combined call: Rejected - would require new API endpoint
- Parse socket file timestamps for uptime: Rejected - unreliable across platforms

### 3. Uptime Source

**Decision**: Compute uptime client-side from `/api/v1/status` timestamp and a new `started_at` field.

**Rationale**: The `ServerStatus` struct in `contracts/types.go` already has `StartedAt time.Time`. The `/api/v1/status` handler needs to include this in its response. Uptime = `time.Since(startedAt)`.

**Alternatives considered**:
- Use `observability.SetUptime()` metrics: Not exposed via API
- Add dedicated uptime field to response: Redundant when `started_at` is available

### 4. API Key Access in CLI

**Decision**: In config-only mode, read API key directly from config file. In daemon mode, the API key is already embedded in the `web_ui_url` from `/api/v1/info`, and also available from the loaded config.

**Rationale**: The CLI always loads config first (for socket detection and data-dir). The API key is always in the config file. No need to fetch it from the daemon.

### 5. Reset Key Implementation

**Decision**: Load config, generate new key via existing `generateAPIKey()` (or equivalent), save via `config.SaveConfig()` with atomic write. Display warning about HTTP clients.

**Rationale**: Matches existing patterns in `main.go` lines 498-541 where the key is auto-generated and saved. File watcher handles hot-reload for running daemon.

**Alternatives considered**:
- Send reset command to daemon via API: Rejected - adds API surface area, config file write is simpler and works without daemon
- Require daemon restart: Rejected - hot-reload already handles it

### 6. Masking Utility

**Decision**: Reuse existing `maskAPIKey()` function from `main.go` line 81-87.

**Rationale**: Already implements the first4+****+last4 pattern. May need to be moved to a shared package or duplicated in status_cmd.go (it's currently unexported in main package).

### 7. CLI Industry Patterns

**Decision**: Follow gh CLI two-tier pattern: masked by default, `--show-key` for full reveal. Inspired by `gh auth status` + `gh auth token`.

**Rationale**: Research across 6 major CLIs (gh, kubectl, docker, heroku, stripe, aws) shows masked-by-default with opt-in reveal as the dominant pattern. First4+last4 masking matches AWS CLI style.

### 8. Documentation Approach

**Decision**: New file `docs/cli/status-command.md` following the pattern of `docs/cli/activity-commands.md`.

**Rationale**: Existing CLI docs use consistent frontmatter (id, title, sidebar_label, sidebar_position, description, keywords), code blocks, flag tables, and example outputs. Status command docs follow the same structure.
