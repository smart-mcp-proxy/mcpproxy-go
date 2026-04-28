# Requirements Checklist: Claude Desktop Bridge Support

**Purpose**: Verify that spec 047 covers all functional, edge-case, and non-regression requirements for shipping the Claude Desktop bridge adapter.
**Created**: 2026-04-28
**Feature**: [spec.md](../spec.md)

## Adapter Output Contract

- [ ] CHK001 Adapter emits `command: "npx"` as a bare command (no absolute path) — FR-001.
- [ ] CHK002 Adapter emits `args: ["-y", "mcp-remote", "<resolved-url>", "--header", "X-MCPProxy-Adapter:1", ...]` with the URL taken from runtime state, not the static `listen` config field — FR-002.
- [ ] CHK003 Sentinel header `X-MCPProxy-Adapter:1` is present on every entry the adapter writes — FR-003.
- [ ] CHK004 All header values use no-space form (`Name:Value`) — FR-006.
- [ ] CHK005 Post-write user-facing message instructs the user to fully quit and relaunch Claude Desktop — FR-014.

## Conditional Authentication

- [ ] CHK006 When `require_mcp_auth: true`, args include `--header X-API-Key:${MCPPROXY_API_KEY}` AND an `env: { "MCPPROXY_API_KEY": "<resolved key>" }` block is present — FR-004.
- [ ] CHK007 When `require_mcp_auth: false`, no `X-API-Key` header is emitted and no `env` block is present — FR-005.
- [ ] CHK008 Toggling `require_mcp_auth` and re-running connect produces `action: "refreshed"` and rewrites the entry consistently — US2 acceptance scenarios 1 & 2.
- [ ] CHK009 API key rotation under `require_mcp_auth: true` changes only `env.MCPPROXY_API_KEY`; args remain byte-identical — US2 acceptance scenario 3.
- [ ] CHK010 Literal API key value never appears in args (always travels via `env`) — FR-006 rationale.

## Round-Trip Identity

- [ ] CHK011 Adapter uses sentinel-header presence (NOT URL match) to identify its own entries — FR-003.
- [ ] CHK012 Re-running connect against an unchanged sentinel-tagged entry returns `action: "exists"` with no file mutation and no new backup — US1 acceptance scenario 2, FR-007.
- [ ] CHK013 `connect` followed by `disconnect` leaves `claude_desktop_config.json` byte-identical to its pre-connect state — FR-008, SC-002.
- [ ] CHK014 `disconnect` leaves user-authored `mcpServers.mcpproxy` entries (no sentinel) untouched and returns a warning — US1 acceptance scenario 3.

## Conflict & Force Semantics

- [ ] CHK015 Pre-existing `mcpServers.mcpproxy` entry without sentinel produces `action: "conflict"` and is not overwritten by default — FR-009.
- [ ] CHK016 `force: true` overrides the conflict guard and the overwritten entry is captured in the backup file — FR-009.

## Backup & File Hygiene

- [ ] CHK017 `.bak.<UTC-timestamp>` backup is created before any mutating write — FR-007.
- [ ] CHK018 No backup is created on no-op re-runs (`action: "exists"`) — FR-007, SC-003.
- [ ] CHK019 Atomic write semantics protect against partial writes on crash mid-update — implied by spec 039 reuse.

## Platform & PATH

- [ ] CHK020 Adapter does NOT call any `resolveNpx()` PATH probe; justification documented in spec assumptions — FR-011.
- [ ] CHK021 `mcpproxy doctor` adds an `npx_available` check that runs `npx --version` (or `where npx` on Windows) and emits a warning with remediation link when absent — FR-010, SC-004.
- [ ] CHK022 Linux Claude Desktop returns an explicit `unsupported_platform` error — Edge Cases.
- [ ] CHK023 Custom mcpproxy port (`listen` ≠ `127.0.0.1:8080`) is honoured by reading the live port from runtime state — Edge Cases.

## Error Surfaces

- [ ] CHK024 Missing `claude_desktop_config.json` triggers fresh-file creation with no backup; response is `action: "created"` — Edge Cases.
- [ ] CHK025 Malformed JSON config is refused with a structured error pointing at the file path; `--force` semantics align with spec 039 — Edge Cases.
- [ ] CHK026 Unresolvable bridge URL (e.g., `0.0.0.0` listen with no usable host) returns an actionable error naming the offending field; no partial entry written; no backup created — FR-013.
- [ ] CHK027 Missing `npx` produces a fail-soft write with a `warnings` array in the API response — Edge Cases.

## Wizard & Telemetry Integration

- [ ] CHK028 Spec 046 US1 wizard copy referencing "Claude Desktop only supports stdio transport; HTTP/SSE not available" is replaced with a bridge recommendation card — FR-012.
- [ ] CHK029 Spec 046 US3 telemetry tracks `bridge_connect_success` and `bridge_connect_failure` distinct from existing `connect_success`/`connect_failure` counters — FR-012.
- [ ] CHK030 Web UI / wizard surfaces a "Refresh" call-to-action when auth-drift is detected — US2 narrative.

## Non-Regression

- [ ] CHK031 All existing Spec 039 adapter tests (`claude-code`, `cursor`, `windsurf`, `vscode`, `codex`, `gemini`) continue to pass — FR-015, SC-005.
- [ ] CHK032 The previous `claude-desktop` stub test is replaced with full adapter tests covering both auth modes and the round-trip identity invariant — FR-015.

## Out-of-Scope Boundaries

- [ ] CHK033 Spec explicitly excludes tier-2 adapters that have native HTTP/SSE (Zed, OpenCode, etc.) — Out of Scope.
- [ ] CHK034 Spec explicitly excludes retroactive `resolveNpx()` PATH probes for other adapters (e.g., Cursor) — Out of Scope.
- [ ] CHK035 Spec explicitly excludes vendoring `mcp-remote` inside the mcpproxy binary — Out of Scope.
- [ ] CHK036 Spec explicitly excludes Linux Claude Desktop, automatic migration of hand-authored entries, and writing a custom stdio↔HTTP shim — Out of Scope.

## Success Criteria Verification

- [ ] CHK037 SC-001 measurable: end-to-end install → connect → restart Claude Desktop → tools visible in ≤ 60 seconds.
- [ ] CHK038 SC-002 measurable: 100% byte-identical round-trip for `connect → disconnect`.
- [ ] CHK039 SC-003 measurable: 100% no-op re-runs report `action: "exists"` with zero new backups.
- [ ] CHK040 SC-004 measurable: 100% of runs without Node trip the doctor warning.
- [ ] CHK041 SC-005 measurable: zero regressions in `internal/connect/...` test packages.
- [ ] CHK042 SC-006 measurable: Spec 046 US3 telemetry shows non-zero `bridge_connect_success` share within one release cycle of GA.
- [ ] CHK043 SC-007 measurable: 100% of toggle test runs (both directions) produce an entry that authenticates against `/mcp`.

## Notes

- Check items off as completed: `[x]`
- Items map back to FRs, US scenarios, edge cases, success criteria, and out-of-scope statements in `spec.md`.
- Use this checklist during plan/tasks generation and again at PR review time.
