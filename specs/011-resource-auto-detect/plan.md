# Implementation Plan: Auto-Detect RFC 8707 Resource Parameter

**Branch**: `011-resource-auto-detect` | **Date**: 2025-12-10 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/011-resource-auto-detect/spec.md`

## Summary

Enable automatic detection of the RFC 8707 `resource` parameter from RFC 9728 Protected Resource Metadata, eliminating manual `extra_params` configuration for OAuth flows with providers like Runlayer. The implementation modifies the OAuth discovery layer to return full metadata, updates the OAuth config creation to extract and inject the resource parameter, and adds URL injection in the connection layer after mcp-go constructs the authorization URL.

## Technical Context

**Language/Version**: Go 1.24.0
**Primary Dependencies**: mcp-go (OAuth transport), zap (logging), BBolt (storage)
**Storage**: BBolt embedded database (`~/.mcpproxy/config.db`) for OAuth tokens
**Testing**: go test, E2E tests with `tests/oauthserver/`
**Target Platform**: macOS, Linux, Windows (cross-platform CLI/daemon)
**Project Type**: Single project with CLI, tray, and daemon components
**Performance Goals**: OAuth metadata discovery adds <500ms to existing flow (already performed for scopes)
**Constraints**: No breaking changes to existing `CreateOAuthConfig()` callers without updating them
**Scale/Scope**: Supports all HTTP-based MCP servers requiring RFC 8707 compliance

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Gate | Status | Notes |
|------|--------|-------|
| I. Performance at Scale | ✅ PASS | Metadata discovery already happens for scopes; resource extraction reuses same request |
| II. Actor-Based Concurrency | ✅ PASS | No new concurrency patterns; uses existing OAuth flow channels |
| III. Configuration-Driven Architecture | ✅ PASS | Adds auto-detection, preserves manual `extra_params` config override |
| IV. Security by Default | ✅ PASS | No security relaxation; resource parameter is public URL metadata |
| V. Test-Driven Development | ✅ PASS | Will add unit tests for discovery, E2E test for full flow |
| VI. Documentation Hygiene | ✅ PASS | Will update CLAUDE.md OAuth section if behavior changes |

**Architecture Constraints Check**:
- Core + Tray Split: ✅ Changes only in core (`internal/oauth/`, `internal/upstream/core/`)
- Event-Driven Updates: ✅ N/A - OAuth flow is request/response, not event-based
- DDD Layering: ✅ Discovery in `internal/oauth/`, connection handling in `internal/upstream/core/`
- Upstream Client Modularity: ✅ Changes flow through existing 3-layer client design

## Project Structure

### Documentation (this feature)

```text
specs/011-resource-auto-detect/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output (N/A - no new APIs)
└── tasks.md             # Phase 2 output (/speckit.tasks command)
```

### Source Code (repository root)

```text
internal/
├── oauth/
│   ├── discovery.go         # MODIFY: Add DiscoverProtectedResourceMetadata()
│   ├── discovery_test.go    # ADD: Test new function
│   ├── config.go            # MODIFY: Change CreateOAuthConfig() signature
│   └── config_test.go       # ADD: Test resource detection
├── upstream/
│   └── core/
│       └── connection.go    # MODIFY: Update callers, add URL injection
tests/
└── oauthserver/             # MODIFY: Add resource requirement option
```

**Structure Decision**: Single project structure. All changes are in existing `internal/` packages following the established DDD layering. No new packages or architectural changes needed.

## Complexity Tracking

> No violations - feature follows existing patterns.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| N/A | N/A | N/A |
