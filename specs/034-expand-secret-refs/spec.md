# Feature Specification: Expand Secret/Env Refs in All Config String Fields

**Feature Branch**: `034-expand-secret-refs`
**Created**: 2026-03-10
**Status**: Draft
**Input**: User description: "Expand secret/env refs in working_dir, data_dir, and all config string fields"
**Issue**: [#333](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/333)

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Environment variable references in working directory (Priority: P1)

As a developer, I want to use `${env:HOME}` or `${env:IH_HOME}` in my server's `working_dir` configuration so that my config files are portable across machines without hardcoding absolute paths.

**Why this priority**: This is the primary reported bug. Users configuring `"working_dir": "${env:IH_HOME}"` get the literal string instead of the resolved path, causing server startup failures when the literal directory doesn't exist.

**Independent Test**: Can be tested by configuring a server with `"working_dir": "${env:HOME}/test-project"` and verifying the stdio child process launches with the resolved directory as its working directory.

**Acceptance Scenarios**:

1. **Given** a server config with `"working_dir": "${env:HOME}/project"`, **When** the server starts, **Then** the stdio child process runs in the resolved directory (e.g., `/Users/alice/project`).
2. **Given** a server config with `"working_dir": "${env:UNDEFINED_VAR}/project"`, **When** the server starts, **Then** an error is logged and the unresolved value is used as a fallback (matching existing error behavior for env/args/headers).
3. **Given** a server config with `"working_dir": "/absolute/path"` (no refs), **When** the server starts, **Then** behavior is unchanged — the literal path is used.

---

### User Story 2 - Secret references in all server config string fields (Priority: P1)

As a platform operator, I want `${env:...}` and `${keyring:...}` references to work in any string field of my server configuration — not just `env`, `args`, and `headers` — so I don't have to guess which fields support variable expansion.

**Why this priority**: This is the systematic fix that prevents future recurrence. Currently only 3 of many string fields are expanded. Fields like `URL`, `Command`, `Isolation.WorkingDir`, and `Isolation.ExtraArgs` silently ignore secret references.

**Independent Test**: Can be tested by setting `${env:TEST_VAR}` in each string field of a server config and verifying all resolve after client creation.

**Acceptance Scenarios**:

1. **Given** a server config with `${env:...}` refs in `URL`, `Command`, `WorkingDir`, `Isolation.WorkingDir`, and `Isolation.ExtraArgs`, **When** the client is created, **Then** all string fields are resolved.
2. **Given** a server config with `${keyring:my-secret}` in the `URL` field, **When** the client is created, **Then** the keyring value is substituted.
3. **Given** a new string field is added to the server config struct in the future, **When** a user sets a `${env:...}` ref in that field, **Then** it is automatically resolved without any code changes to the expansion logic.

---

### User Story 3 - Environment variable references in data directory (Priority: P2)

As an operator, I want to use `${env:...}` references in the top-level `data_dir` configuration so that I can configure storage paths portably across environments.

**Why this priority**: `data_dir` controls where the database, search index, and logs are stored. While less commonly customized than per-server fields, portable paths are still valuable for multi-environment deployments.

**Independent Test**: Can be tested by setting `"data_dir": "${env:HOME}/.mcpproxy"` in the config and verifying the resolved path is used for database operations.

**Acceptance Scenarios**:

1. **Given** a config with `"data_dir": "${env:HOME}/.mcpproxy"`, **When** the application starts, **Then** the database opens at the resolved path.
2. **Given** a config with `"data_dir": "${env:MISSING_VAR}/.mcpproxy"`, **When** the application starts, **Then** an error is logged and the unresolved value is used as a fallback.

---

### Edge Cases

- What happens when a `${env:...}` ref is in a field like `Name` or `Protocol` that doesn't normally contain refs? The expansion is a no-op since those values won't match the `${type:name}` pattern.
- What happens when an env var is set but contains an empty string? Empty string is a successful resolution — the ref is replaced with `""` and no error is logged. Downstream validation (e.g. directory existence check) surfaces any resulting issue.
- What happens when `Isolation` config is nil? The expansion handles nil pointers gracefully without panicking.
- What happens when `OAuth` config contains `${keyring:...}` refs (e.g., in `ClientSecret`)? These are expanded like any other string field.
- What happens when the same secret reference appears in multiple fields and one fails to resolve? Each field is handled independently — successful resolutions are not rolled back due to failures in other fields.
- What happens when expansion is applied to the original config vs. a copy? The original config held by the caller is never mutated. A deep copy is used before expansion.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST resolve `${env:...}` and `${keyring:...}` references in all string fields of the server configuration, including but not limited to: `WorkingDir`, `URL`, `Command`, and all fields within nested isolation and OAuth configuration structs.
- **FR-002**: System MUST resolve `${env:...}` and `${keyring:...}` references in the top-level data directory configuration field before it is consumed by downstream components.
- **FR-003**: On expansion failure, system MUST log at ERROR level (including field path, reference pattern, and remediation guidance) and fall back to the unresolved value. On successful resolution where the value changed, system MUST log at DEBUG level (including field path and reference pattern). Resolved values MUST NOT appear in log output at any log level.
- **FR-004**: System MUST NOT mutate the original server configuration passed to the client constructor. All expansion must operate on a deep copy.
- **FR-005**: System MUST NOT modify string fields that don't contain secret reference patterns (`${type:name}`). Fields containing plain values like server names or protocol identifiers are left untouched.
- **FR-006**: System MUST handle nil nested configuration structures (isolation, OAuth) gracefully without errors.
- **FR-007**: System MUST automatically cover any new string fields added to the server configuration in the future without requiring code changes to the expansion logic.
- **FR-008**: The expansion behavior for currently-supported fields (`env`, `args`, `headers`) MUST be preserved identically — same error handling, same log output, same fallback to unresolved value on failure.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A server configured with `"working_dir": "${env:HOME}/project"` starts successfully with the resolved path as the working directory.
- **SC-002**: 100% of string fields in the server configuration (including nested structs) containing secret references are resolved after client creation — verified by an automated test.
- **SC-003**: All existing tests pass without modification (zero regressions).
- **SC-004**: Adding a new string field to the server configuration causes the regression test to automatically verify it is expanded — no manual test updates needed.
- **SC-005**: Expansion errors in one field do not prevent resolution of other fields.

## Clarifications

### Session 2026-03-10

- Q: Should resolved secret values appear in log output? → A: Never log resolved values — log only the reference pattern (e.g. `${keyring:my-token}`) to confirm resolution occurred, never the resolved value itself.
- Q: If an env var is set but empty (`""`), is that a successful resolution or a failure? → A: Treat as success — consistent with current behavior. An empty string is a valid resolved value. Downstream validation (e.g. directory existence check) surfaces the problem at the appropriate point.
- Q: What log level should expansion use? → A: Match existing behavior — ERROR for failure (with unresolved fallback), DEBUG for success (ref resolved, value changed).

## Assumptions

- The existing reflection-based struct expansion function is the correct foundation for FR-007. It needs a variant that collects errors instead of failing on the first error.
- The existing deep-copy function for server configurations correctly handles all pointer fields and is the right mechanism for FR-004.
- Fields like `Name`, `Protocol`, `Enabled`, `Created`, `Updated` will never contain `${type:name}` patterns in practice, so expanding them is a safe no-op.

## Scope Boundaries

### In Scope

- Expanding refs in all server config string fields (including nested structs)
- Expanding refs in the top-level data directory config
- Replacing manual field-by-field expansion with automatic approach
- Regression test preventing future fields from being missed

### Out of Scope

- Adding new config fields like `Volumes` or `HostPath` to isolation configuration
- Changing the behavior of the existing struct expansion function for other callers
- Expanding refs in non-string fields (booleans, integers, etc.)
- UI changes

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #333` - Links the commit to the issue without auto-closing
- Do NOT use: `Fixes #333`, `Closes #333`, `Resolves #333` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- Do NOT include: `Co-Authored-By: Claude <noreply@anthropic.com>`
- Do NOT include: "Generated with Claude Code"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat: expand secret refs in all ServerConfig string fields

Related #333

Replace manual field-by-field expansion with reflection-based
ExpandStructSecretsCollectErrors to automatically cover all current
and future string fields.

## Changes
- Add ExpandStructSecretsCollectErrors to secret.Resolver
- Export CopyServerConfig for deep-copy before expansion
- Replace ~78 lines of manual expansion with ~12 lines of struct expansion
- Add reflection-based regression test

## Testing
- All existing tests pass (zero regressions)
- New tests for collect-errors method and regression prevention
```
