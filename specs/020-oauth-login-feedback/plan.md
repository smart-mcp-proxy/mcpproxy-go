# Implementation Plan: OAuth Login Error Feedback

**Branch**: `020-oauth-login-feedback` | **Date**: 2026-01-06 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/020-oauth-login-feedback/spec.md`

## Summary

Enhance the OAuth login endpoint response to provide actionable feedback to all clients (CLI, tray, Web UI). The endpoint remains async (non-blocking) but now returns:

1. `correlation_id` - UUID for log correlation
2. `auth_url` - Authorization URL for manual use
3. `browser_opened` - Whether browser launch succeeded
4. `browser_error` - Error message if browser failed
5. Pre-flight validation errors with suggestions

This is a **response payload enhancement** - not a change to the async flow model.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: Cobra (CLI), Chi router (HTTP), Zap (logging), mark3labs/mcp-go (MCP protocol)
**Testing**: go test, scripts/test-api-e2e.sh, scripts/run-all-tests.sh
**Target Platform**: macOS, Linux, Windows (cross-platform CLI)
**Project Type**: Single project with CLI and daemon components
**Constraints**: Must maintain backward compatibility; all clients share same response format

## PR Dependencies & Merge Order

This feature depends on and builds upon related PRs. **Recommended merge order:**

| Order | PR | Status | Dependency Reason |
|-------|-----|--------|-------------------|
| 1 | **#237** Request ID Tracking | OPEN | Provides `request_id` for error correlation. FR-013 requires this. |
| 2 | **#241** Auth Login Fix | OPEN | Fixes browser not opening. Unblocks manual testing. |
| 3 | **#205** Structured Server State | OPEN | Provides unified health status model. Optional but improves consistency. |
| 4 | **020** OAuth Login Feedback | This spec | Builds on all above for complete error reporting. |

**Critical Path**: #237 → #241 → 020 implementation

**Why this order:**
- **PR #237 first**: The `request_id` field is essential for error correlation (FR-013). Without it, `OAuthFlowError.RequestID` would be empty.
- **PR #241 second**: Fixes the "browser not opening" bug. Must merge before 020 to avoid duplicate fixes.
- **PR #205 optional**: Health status model provides consistent server state. Can merge in parallel with 020.

## Implementation Phases

### Phase 1: Error Classification (Quick Win)
Replace generic "panic recovered" messages with structured `OAuthFlowError`.

**Changes:**
- Add `OAuthFlowError` type to `internal/contracts/types.go`
- Update `handleOAuthAuthorization` in `internal/upstream/core/connection.go` to return structured errors
- Update `StartManualOAuth` in `internal/upstream/manager.go` to propagate errors
- Update CLI `auth login` to display rich error output

**Files:** 4 | **Effort:** Small | **Risk:** Low

### Phase 2: Metadata Pre-flight Validation
Validate OAuth metadata BEFORE starting flow to fail fast with clear errors.

**Changes:**
- Add `validateOAuthMetadata()` to `internal/upstream/core/connection.go`
- Fetch protected resource metadata and validate
- Fetch authorization server metadata and validate
- Return `OAuthFlowError` with `details` on failure

**Files:** 2 | **Effort:** Medium | **Risk:** Medium (network calls)

### Phase 3: Full Response Enhancement
Complete the `OAuthStartResponse` with browser status and auth URL.

**Changes:**
- Update `POST /api/v1/servers/{id}/login` response
- Return `auth_url`, `browser_opened`, `correlation_id`
- Update CLI to display auth_url when browser fails
- Add E2E tests for all response fields

**Files:** 5 | **Effort:** Medium | **Risk:** Low

## Constitution Check

*GATE: Must pass before implementation.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | PASS | No impact on tool search/indexing |
| II. Actor-Based Concurrency | PASS | No new concurrency patterns; uses existing async flow |
| III. Configuration-Driven Architecture | PASS | No new config needed |
| IV. Security by Default | PASS | Pre-flight validates quarantine/disabled state |
| V. Test-Driven Development | PASS | Will add unit tests for validation, E2E for response |
| VI. Documentation Hygiene | PASS | Will update CLAUDE.md CLI docs |

**Architecture Constraints:**

| Constraint | Status | Notes |
|------------|--------|-------|
| Core + Tray Split | PASS | Response changes in core; tray uses same endpoint |
| Event-Driven Updates | PASS | Uses existing `servers.changed` event for completion |
| DDD Layering | PASS | Validation in management, response in httpapi |
| Upstream Client Modularity | PASS | Browser open detection added to client layer |

## Project Structure

### Documentation (this feature)

```text
specs/020-oauth-login-feedback/
├── plan.md              # This file
├── research.md          # Design decisions
├── data-model.md        # OAuthStartResponse, OAuthValidationError
├── contracts/
│   └── oauth-api.yaml   # OpenAPI 3.0 contract
├── quickstart.md        # Usage examples
└── tasks.md             # Implementation tasks (/speckit.tasks)
```

### Source Code (repository root)

```text
cmd/mcpproxy/
├── auth_cmd.go          # MODIFY: Handle browser_opened, display auth_url

internal/
├── httpapi/
│   └── server.go        # MODIFY: Enhanced login response with browser status
├── management/
│   └── service.go       # MODIFY: Pre-flight validation, return OAuthStartResponse
├── upstream/
│   └── manager.go       # MODIFY: Return browser open status from StartManualOAuth
└── oauth/
    └── browser.go       # MODIFY: Return success/error from browser open attempt

tests/
└── internal/
    └── server/e2e_test.go  # MODIFY: E2E tests for new response fields
```

## Implementation Scope

### In Scope

| Component | Change |
|-----------|--------|
| HTTP API | Enhanced `POST /api/v1/servers/{id}/login` response |
| Management Service | Pre-flight validation with structured errors |
| Upstream Manager | Browser open status detection |
| CLI | Display auth_url when browser_opened=false |
| E2E Tests | Verify new response fields |

### Out of Scope

- Synchronous/blocking OAuth endpoint (removed from original plan)
- New SSE event types (use existing `servers.changed`)
- OAuth status polling endpoint (use `GET /servers`)
- Tray/Web UI changes (they receive same response, implement their own UX)

## Multi-Client Behavior

All clients use `POST /api/v1/servers/{id}/login` and receive `OAuthStartResponse`:

| Field | CLI Handling | Tray Handling | Web UI Handling |
|-------|-------------|---------------|-----------------|
| browser_opened=true | Print "Opening browser..." | Show notification | Show "Check browser" toast |
| browser_opened=false | Print auth_url | Notification with URL | Modal with URL |
| error_type | Print error + suggestion | Show error notification | Show error in UI |

CLI is the only client modified in this feature. Tray and Web UI will implement their handling in future work.

## Complexity Tracking

| Aspect | Decision | Rationale |
|--------|----------|-----------|
| API Model | Async (non-blocking) | Maintains backward compat; simpler implementation |
| Completion Detection | Existing `servers.changed` | No new SSE infrastructure needed |
| Browser Status | Return in response | Immediate feedback to client |

---

## Generated Artifacts

| Artifact | Status | Description |
|----------|--------|-------------|
| `research.md` | Complete | Design decisions with rationale |
| `data-model.md` | Complete | OAuthStartResponse, OAuthValidationError entities |
| `contracts/oauth-api.yaml` | Complete | OpenAPI 3.0 contract for async response |
| `quickstart.md` | Complete | Usage examples and troubleshooting |

---

## Next Steps

Run `/speckit.tasks` to generate the implementation task list.
