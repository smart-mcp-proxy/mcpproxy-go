# Research: Expand Secret/Env Refs in All Config String Fields

**Branch**: `034-expand-secret-refs` | **Date**: 2026-03-10

## Decision 1: Implement `ExpandStructSecretsCollectErrors` vs. calling `ExpandStructSecrets` per-field

**Decision**: Add a new method `ExpandStructSecretsCollectErrors` that collects errors instead of failing fast, with full field path tracking.

**Rationale**:
- `ExpandStructSecrets` (resolver.go:107) fails fast on first error — incompatible with the existing "log and continue" semantics of `NewClientWithOptions`
- The existing `extractSecretRefs` (resolver.go:234) already has full path tracking (`"Isolation.WorkingDir"`, `"Args[0]"`, `"Env[MY_VAR]"`) — the collect-errors variant mirrors this pattern
- `ExpandStructSecrets` doesn't track paths; the new variant adds path tracking for richer error logs

**Alternatives considered**:
- Wrapping `ExpandStructSecrets` with a recover: rejected — paths still not tracked, awkward error handling
- Keeping manual field expansion but adding missing fields: rejected — doesn't satisfy FR-007 (future-proof) and re-introduces the same class of bug on the next PR

## Decision 2: Use `CopyServerConfig` (exported) for deep copy before expansion

**Decision**: Export `copyServerConfig` → `CopyServerConfig` in `internal/config/merge.go` and call it from `NewClientWithOptions`.

**Rationale**:
- The current code does `resolvedServerConfig := *serverConfig` (shallow copy). Pointer fields `Isolation *IsolationConfig` and `OAuth *OAuthConfig` alias the original.
- `copyServerConfig` (merge.go:525) correctly deep-copies all pointer fields, maps, and slices.
- `ExpandStructSecretsCollectErrors` must be called with a pointer to the copied struct so reflection `CanSet()` works on struct fields.

**Alternatives considered**:
- `encoding/json` marshal+unmarshal for deep copy: rejected — loses zero values, slower, requires json tags on all fields, drops `*bool` nil vs. false distinction
- `encoding/gob`: rejected — same issues, requires exported fields only (already have them but adds a dependency for a simple copy)

## Decision 3: Expand `DataDir` in `loader.go`, not in `Validate()`

**Decision**: Expand `DataDir` in `internal/config/loader.go` immediately before each call to `cfg.Validate()` (lines 50 and 143).

**Rationale**:
- `Validate()` doesn't have a resolver parameter and is called from multiple places including hot-reload in `runtime.go:1139` — modifying its signature would require widespread changes
- The loader is the canonical "first load" entry point; the two call sites at loader.go:50 and 143 cover both initial load and reload-from-file
- `DataDir` validation at config.go:945 checks directory existence — expansion must precede this check
- `secret.NewResolver()` requires no parameters and can be instantiated locally in the loader

**Alternatives considered**:
- Add `*secret.Resolver` parameter to `Validate()`: rejected — 6+ call sites need updating, including hot-reload, CLI config validation, test code
- Expand at point-of-use (when BBolt opens at `cli/client.go:66`): rejected — too late, `DataDir` is used for multiple things (logs, search index) before the BBolt client is created

## Decision 4: Where to call `ExpandStructSecretsCollectErrors` in `NewClientWithOptions`

**Decision**: Call on the `CopyServerConfig` result (a pointer), replacing all 3 manual expansion blocks (lines 107-182).

**Rationale**:
- `ExpandStructSecretsCollectErrors` must receive a pointer to the struct for reflection `CanSet()` to work on struct fields
- Calling it on the copy (not the original) satisfies FR-004
- Replaces ~78 lines with ~12 lines while covering all current and future string fields
- FR-008 (preserve existing behavior for env/args/headers) is automatically satisfied — the reflect walker handles maps and slices the same way as the manual approach

## Decision 5: Test strategy

**Decision**: Two new test files + tests in the existing resolver_test.go:
1. `internal/secret/resolver_test.go` — add `TestResolver_ExpandStructSecretsCollectErrors` table-driven tests (MockProvider already defined in same file)
2. `internal/upstream/core/client_secret_test.go` (new file) — test `NewClientWithOptions` with secret refs in all fields including `WorkingDir`; use reflection to assert no fields contain unresolved `${...}` patterns after creation

**Rationale**: No existing tests for `ExpandStructSecrets` at all (confirmed by audit). The reflection-based assertion in client_secret_test.go serves as SC-004 (regression prevention for future fields).

## Key File Map

| File | Change | Why |
|------|--------|-----|
| `internal/secret/resolver.go` | Add `ExpandStructSecretsCollectErrors` + `SecretExpansionError` type | Core of FR-007 |
| `internal/secret/resolver_test.go` | Add tests for new method | Constitution V (TDD) |
| `internal/config/merge.go` | Export `CopyServerConfig` (rename + 3 internal call sites) | FR-004 deep copy |
| `internal/upstream/core/client.go` | Replace lines 107-182, use `CopyServerConfig` + `ExpandStructSecretsCollectErrors` | FR-001 |
| `internal/upstream/core/client_secret_test.go` | New — regression test with reflection assertion | SC-002, SC-004 |
| `internal/config/loader.go` | Expand `DataDir` before each `Validate()` call (2 sites) | FR-002 |
| `internal/config/config_test.go` | Add `DataDir` expansion test | Constitution V |

## No NEEDS CLARIFICATION items remaining

All technical unknowns resolved. No blockers to Phase 1.
