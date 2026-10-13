# Feature Specification: Residual Security Fixes

**Feature Branch**: `codex/106-security-residual-fixes`
**Created**: 2026-09-12
**Status**: Ready for planning
**Input**: Research and fix #1265, #1179 and confirmed #1184 findings; verify every change on real local instances, cross-review with OpenCode Claude Fable 5, and make PR checks green.

## User Scenarios & Testing

### User Story 1 - Detect secrets in scripts (Priority: P1)
Operators receive the same sensitive-data detection coverage for script input, generated output and nested upstream results as for direct tool calls.
**Independent Test**: Execute real scripts with synthetic detectable values in each location, including beyond the activity display limit; inspect persisted detection metadata.
**Acceptance Scenarios**:
1. Given a nested response with a secret beyond 8 KiB but inside the configured detector limit, when a script calls it, then the nested activity reports the secret.
2. Given a script input or generated output containing a secret, when it executes, then the parent activity reports the secret even if its displayed output is truncated.
3. Given disabled detection or text beyond its configured scan limit, existing detector policy remains authoritative.

### User Story 2 - Revoke tenant access promptly (Priority: P1)
Administrators can identify and revoke an individual tenant credential. Removing sharing takes effect on the next authenticated request without rotating credentials or restarting.
**Independent Test**: Mint same-named tokens for two users, revoke one through the admin surface, and test both; remove sharing and retry an existing credential.
**Acceptance Scenarios**:
1. Admin listing includes owner identity and safe token metadata; no raw token or hash is disclosed.
2. Revocation identifies owner and name, cannot affect another owner's same-named token, and survives restart.
3. A non-admin cannot list or revoke other users' tokens.
4. Explicit grants and historical wildcard grants are intersected with current entitlement on every authentication; failed entitlement reads deny access.
5. Ownerless operator credentials retain their behavior; owned administrator credentials use the owner's current role.
6. Calls already authorized remain point-in-time operations. Long-lived SSE responses revalidate before each status or runtime event: unsharing narrows the next frame and revocation closes the stream.

### User Story 3 - Preserve scoped visibility and safe status output (Priority: P1)
Scoped callers see detection events only for authorized servers; malformed statistics fail closed; tool totals follow enabled/quarantine policy; default status output does not expose the admin key.
**Independent Test**: Compare real admin and scoped event streams/statistics and CLI output, with malformed-shape and nil-storage regression tests.
**Acceptance Scenarios**:
1. Hidden or unidentified detection events are withheld; authorized events and admin events remain visible.
2. Unexpected per-server statistics shapes disclose no unfiltered inventory or counts.
3. Disabled or quarantined servers contribute no tools to totals while per-server counts retain their diagnostic value.
4. Server-edition setup with no token storage does not install a typed-nil revoker.
5. Default status formats mask the key in both the key field and Web UI URL; explicit key-revealing options retain documented behavior.

### Edge Cases
Empty entitlement, deleted owners, same-named tokens, historical wildcard tokens, missing event identity, malformed statistics, nil storage, scan limit versus display limit, error results, and shutdown while detection is running.

## Requirements
- **FR-001**: Detect script input, output and complete nested result subject only to configured detector limits.
- **FR-002**: Keep activity display limits and scan limits independent; await detection work on shutdown.
- **FR-003**: Provide admin-only owner-qualified listing and revocation without credential disclosure; token names are opaque request-body data and successful revocations identify actor and target in operational logs.
- **FR-004**: Revalidate owned token entitlement per request, narrow only, and fail closed on lookup failure.
- **FR-005**: Scope sensitive-data events by server identity and withhold unidentified events from scoped callers.
- **FR-006**: Fail closed on malformed statistics and preserve producer total semantics.
- **FR-007**: Avoid typed-nil token administration wiring.
- **FR-008**: Mask every default status representation of the API key.
- **FR-009**: Include regression tests, real-instance evidence, independent Claude review and green CI before completion.

### Key Entities
Activity and detection metadata; owned agent token; current server entitlement; server statistics; scoped notification.

## Assumptions and Boundaries
Issue #1184 finding 5 was refuted in its maintainer discussion and is excluded. No token schema migration or automatic token rotation is needed: old grants are bounded at use time. Existing Spec 105 remains the separate contract for broader MCP scope hardening. No production configuration is changed for verification. Entitlement is revalidated at HTTP/MCP authentication boundaries and before each status or runtime frame on an agent-authenticated SSE response. An already-running call keeps the authorization established for that call.

## Success Criteria
- **SC-001**: All synthetic in-limit script secret cases are detected in real-instance verification.
- **SC-002**: Revoked tokens fail on their next request and after restart; unshared servers are inaccessible on the next request.
- **SC-003**: No hidden-server event sentinel, unfiltered count, or default-output admin key escapes the regression fixtures.
- **SC-004**: All changed behaviors have automated coverage and documented local verification; all required PR checks pass.

## Commit Message Conventions
Use conventional commits with `Related #1265`, `Related #1179`, or `Related #1184`; no automatic issue-closing keywords or AI co-author trailers.
