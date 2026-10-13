# Implementation Plan: Residual Security Fixes
**Branch**: `codex/106-security-residual-fixes` | **Date**: 2026-09-12 | **Spec**: [spec.md](spec.md)

## Summary
Deliver three independently reviewable slices: script detection (#1265), residual visibility and status corrections (#1184), and tenant revocation/current entitlement (#1179). Coordinate with the separate Spec 105 implementation and verify the combined result.

## Technical Context
Go version from go.mod; existing BBolt storage, Chi handlers, runtime activity worker, sensitive-data detector and server-edition user store. No new dependencies or database migration. Personal and server builds on macOS; Go tests and real HTTP/MCP processes. Preserve detector bounds, actor ownership and atomic hook publication.

## Constitution Check
Security defaults: fail closed; no new secret-bearing response. TDD: regression tests before each fix. Documentation: update token API docs and status behavior. Local verification: isolated data directories and ports, synthetic credentials only. Run relevant unit/server-tag suites, linter and API E2E; record limitations rather than claim skipped gates passed. PR base main matches current repository practice; constitution's older next-branch guidance is an explicitly documented deviation for PR review.

## Project Structure
- internal/server/mcp_code_execution.go: full nested detection source.
- internal/runtime/activity_service.go and event_bus.go: parent detection and attributed notifications.
- internal/httpapi/scope.go and sse_scope.go: fail-closed visibility and count semantics.
- internal/server/server.go and internal/upstream/manager.go: producer availability metadata.
- cmd/mcpproxy/status_cmd.go: coherent key masking.
- internal/storage/agent_tokens.go and manager.go: atomic per-validation entitlement hook.
- internal/serveredition/setup.go and api/: live entitlement and admin token routes.
- scripts/: reproducible real-instance checks.

## Execution
1. Pin detection failures, implement and run isolated MCP script cases.
2. Pin visibility/masking failures, implement and run scoped SSE/statistics/status checks.
3. Pin tenant revocation failures, reuse owner/name storage operations and entitlement logic, install the scope hook before fallible setup, and run two-user local server-edition cases.
4. Review changes with OpenCode github-copilot/claude-fable-5; resolve findings and repeat affected checks.
5. Commit and publish bounded PRs; monitor and repair CI until green. Do not merge without instruction.

## Complexity Tracking
One atomic validation callback extends the existing owner-gate pattern. No caches, timers, new token identifiers, or migrations are needed. An admin owner/name API reuses the existing durable mutator.
