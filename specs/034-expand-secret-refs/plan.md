# Implementation Plan: Expand Secret/Env Refs in All Config String Fields

**Branch**: `034-expand-secret-refs` | **Date**: 2026-03-10 | **Spec**: [spec.md](./spec.md)
**Issue**: [#333](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/333)

## Summary

Expand `${env:...}` and `${keyring:...}` references in all string fields of `ServerConfig` (not just the current 3 fields) and in `Config.DataDir`. Replace the manual field-by-field expansion in `NewClientWithOptions` with a new reflection-based `ExpandStructSecretsCollectErrors` method that collects errors instead of failing fast. This automatically covers all current and future string fields without code changes.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: `go.uber.org/zap` (logging), `internal/secret` (resolver, parser), `internal/config` (ServerConfig, IsolationConfig, OAuthConfig, merge), `context` (stdlib), `reflect` (stdlib)
**Storage**: N/A — no new storage; existing BBolt database unaffected
**Testing**: `go test -race ./...`, `testify/assert`, `testify/mock` (MockProvider already defined in `resolver_test.go`)
**Target Platform**: macOS/Linux/Windows (cross-platform, no platform-specific code in scope)
**Performance Goals**: Expansion runs once per server at startup (not hot path). Reflection overhead is negligible (<1ms per server).
**Constraints**: Must preserve identical error/log behavior for existing fields (`env`, `args`, `headers`). Must not mutate original `ServerConfig`. Empty string is a valid resolved value.
**Scale/Scope**: 7 files modified, 1 new file created. ~78 lines replaced with ~12 lines in `client.go`.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Performance at Scale | ✅ Pass | Expansion is startup-time only, not on the tool routing hot path |
| II. Actor-Based Concurrency | ✅ Pass | No new goroutines or shared state introduced |
| III. Configuration-Driven Architecture | ✅ Pass | Directly enhances config-driven architecture by making more fields configurable |
| IV. Security by Default | ✅ Pass | FR-003: resolved values never logged. Existing security surface unchanged. |
| V. Test-Driven Development | ✅ Pass | Tests written before implementation (see Phase 1 task order) |
| VI. Documentation Hygiene | ✅ Pass | CLAUDE.md `Active Technologies` updated by agent context script |

**Complexity Tracking**: No constitution violations. No new abstractions. Replacing complex manual code with a simpler, more general approach.

## Project Structure

### Documentation (this feature)

```text
specs/034-expand-secret-refs/
├── spec.md              ✅ complete
├── research.md          ✅ complete (this phase)
├── plan.md              ✅ this file
├── checklists/
│   └── requirements.md  ✅ complete
└── tasks.md             (Phase 2 — /speckit.tasks)
```

### Source Code (affected files)

```text
internal/secret/
├── resolver.go              # Add ExpandStructSecretsCollectErrors + SecretExpansionError
└── resolver_test.go         # Add tests for new method

internal/config/
├── merge.go                 # Export CopyServerConfig (rename + 3 internal call sites)
├── loader.go                # Expand DataDir before Validate() (2 call sites)
└── config_test.go           # Add DataDir expansion test

internal/upstream/core/
├── client.go                # Replace lines 107-182 with struct expansion
└── client_secret_test.go    # New: reflection-based regression test (NEW FILE)
```

## Phase 0: Research

**Status: Complete** — see `research.md` for all decisions.

Key findings:
- `ExpandStructSecrets` fails fast, no path tracking → new `ExpandStructSecretsCollectErrors` needed
- `extractSecretRefs` already has full path tracking → mirror that pattern
- `copyServerConfig` (merge.go:525) handles full deep copy → export it
- `DataDir` expansion belongs in `loader.go` (2 call sites), not in `Validate()`
- Zero existing tests for `ExpandStructSecrets` → TDD from scratch

## Phase 1: Implementation Plan

### Step 1 — Add `ExpandStructSecretsCollectErrors` (TDD: test first)

**File**: `internal/secret/resolver.go` and `resolver_test.go`

**Tests first** (`resolver_test.go`):
```
TestExpandStructSecretsCollectErrors_HappyPath
  - struct with ${env:...} refs in string field → all resolved, empty error slice
TestExpandStructSecretsCollectErrors_PartialFailure
  - struct with 2 fields, 1 resolves, 1 fails → errors contain failed field path, successful field is resolved, failed field retains original
TestExpandStructSecretsCollectErrors_NilPointer
  - struct with nil *IsolationConfig → no panic, no errors
TestExpandStructSecretsCollectErrors_NestedStruct
  - struct with nested struct pointer containing refs → all expanded with path "Isolation.WorkingDir"
TestExpandStructSecretsCollectErrors_SliceField
  - struct with []string args containing refs → expanded with paths "Args[0]", "Args[1]"
TestExpandStructSecretsCollectErrors_MapField
  - struct with map[string]string env containing refs → expanded with paths "Env[KEY]"
TestExpandStructSecretsCollectErrors_NoRefs
  - struct with plain values → empty error slice, values unchanged
```

**Implementation** (`resolver.go`):
```go
type SecretExpansionError struct {
    FieldPath string
    Reference string
    Err       error
}

func (r *Resolver) ExpandStructSecretsCollectErrors(ctx context.Context, v interface{}) []SecretExpansionError
// Internal: expandValueCollectErrors(ctx, reflect.Value, path string, errs *[]SecretExpansionError)
// Mirrors expandValue but: tracks path, appends to errs instead of returning, retains original on failure
```

The path format matches `extractSecretRefs`: `"WorkingDir"`, `"Isolation.WorkingDir"`, `"Args[0]"`, `"Env[MY_VAR]"`.

---

### Step 2 — Export `CopyServerConfig` (TDD: verify existing tests still pass)

**File**: `internal/config/merge.go`

Rename `copyServerConfig` → `CopyServerConfig` at line 525. Update 3 call sites in the same file. No behavior change.

Verify: `go test ./internal/config/... -race` (existing merge tests must pass).

---

### Step 3 — Replace manual expansion in `NewClientWithOptions` (TDD: test first)

**File**: `internal/upstream/core/client_secret_test.go` (new) and `client.go`

**Tests first** (`client_secret_test.go`):
```
TestNewClientWithOptions_ExpandsWorkingDir
  - ServerConfig{WorkingDir: "${env:TEST_DIR}"} → resolved path used
TestNewClientWithOptions_ExpandsIsolationWorkingDir
  - ServerConfig{Isolation: &IsolationConfig{WorkingDir: "${env:TEST_DIR}"}} → resolved
TestNewClientWithOptions_ExpandsURL
  - ServerConfig{URL: "https://${env:API_HOST}/mcp"} → resolved
TestNewClientWithOptions_PreservesExistingEnvArgsHeaders
  - ServerConfig{Env: {"K": "${env:V}"}, Args: ["${env:A}"], Headers: {"H": "${env:V}"}} → all resolved (FR-008)
TestNewClientWithOptions_DoesNotMutateOriginal
  - ServerConfig with refs → original ServerConfig unchanged after NewClientWithOptions returns (FR-004)
TestNewClientWithOptions_ReflectionRegressionTest
  - Walk resolved config via reflection; assert no field matches IsSecretRef() (SC-004)
TestNewClientWithOptions_PartialFailureLogsError
  - One field fails to resolve → error logged, other fields resolved, server still creates
```

**Implementation** (`client.go`, lines 105-182):
```go
// Replace entire block with:
resolvedServerConfig := config.CopyServerConfig(serverConfig)
if secretResolver != nil {
    ctx := context.Background()
    errs := secretResolver.ExpandStructSecretsCollectErrors(ctx, resolvedServerConfig)
    for _, e := range errs {
        logger.Error("CRITICAL: Failed to resolve secret reference - field will use UNRESOLVED placeholder",
            zap.String("server", serverConfig.Name),
            zap.String("field", e.FieldPath),
            zap.String("reference", e.Reference),
            zap.Error(e.Err),
            zap.String("help", "Use Web UI (http://localhost:8080/ui/) or API to add the secret to keyring"))
    }
}
```

---

### Step 4 — Expand `DataDir` in loader (TDD: test first)

**File**: `internal/config/config_test.go` and `loader.go`

**Test first** (`config_test.go`):
```
TestLoadConfig_ExpandsDataDir
  - Config file with "data_dir": "${env:TEST_HOME}/.mcpproxy" → DataDir resolved before Validate()
TestLoadConfig_DataDirExpandFailure
  - Config with "data_dir": "${env:MISSING_VAR}/.test" and dir not existing → error logged, Validate fails with directory-not-found (not resolver error)
```

**Implementation** (`loader.go`, before each `cfg.Validate()` call — lines 50 and 143):
```go
// Expand secret refs in DataDir before validation
if cfg.DataDir != "" {
    resolver := secret.NewResolver()
    if resolved, err := resolver.ExpandSecretRefs(context.Background(), cfg.DataDir); err != nil {
        logger.Warn("Failed to resolve secret ref in data_dir, using original value",
            zap.String("reference", cfg.DataDir), zap.Error(err))
    } else {
        cfg.DataDir = resolved
    }
}
```

Note: `DataDir` uses `ExpandSecretRefs` (single-string variant), not the struct variant. Consistent with FR-003 error/DEBUG semantics; uses WARN here since startup is aborting anyway if the dir doesn't exist.

---

### Step 5 — Update agent context

```bash
.specify/scripts/bash/update-agent-context.sh claude
```

Add to CLAUDE.md `Active Technologies`:
```
- Go 1.24 (toolchain go1.24.10) + reflect (stdlib), internal/secret.ExpandStructSecretsCollectErrors (034-expand-secret-refs)
```

---

### Step 6 — Full verification

```bash
go test ./internal/secret/... -race -v              # Step 1 tests
go test ./internal/config/... -race -v              # Steps 2, 4 tests
go test ./internal/upstream/core/... -race -v       # Step 3 tests
go test ./internal/... -race                        # Full regression check
./scripts/test-api-e2e.sh                           # E2E sanity
```

Manual smoke test: add server with `"working_dir": "${env:HOME}/test"` to config, verify it starts with resolved path.

## Post-Design Constitution Re-Check

| Principle | Re-check | Notes |
|-----------|----------|-------|
| V. TDD | ✅ Pass | Tests written in each step before implementation code |
| IV. Security | ✅ Pass | `ExpandStructSecretsCollectErrors` logs `e.Reference` (the pattern), never the resolved value |
| VI. Documentation | ✅ Pass | Agent context script run in Step 5 |

## Artifacts Generated

- `specs/034-expand-secret-refs/research.md` ✅
- `specs/034-expand-secret-refs/plan.md` ✅ (this file)
- No new API contracts (no REST endpoints changed)
- No new data model (no new storage entities)
