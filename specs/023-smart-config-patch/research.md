# Research: Smart Config Patching

**Feature Branch**: `023-smart-config-patch`
**Created**: 2026-01-10
**Related Issues**: #239, #240

## Executive Summary

Investigation complete. The root cause of config data loss during patch/quarantine operations has been identified:

1. **Primary Bug**: `async_ops.go:saveServerSync()` is missing the `Isolation` field copy
2. **Secondary Issue**: SaveConfiguration flow can lose config-level fields not in storage
3. **Pattern Break**: Most storage operations correctly preserve Isolation, but the async path doesn't

## Root Cause Analysis

### Bug #1: Missing Isolation Field in saveServerSync()

**File**: `/Users/user/repos/mcpproxy-go/internal/storage/async_ops.go` lines 170-187

```go
func (am *AsyncManager) saveServerSync(serverConfig *config.ServerConfig) error {
    record := &UpstreamRecord{
        ID:          serverConfig.Name,
        Name:        serverConfig.Name,
        URL:         serverConfig.URL,
        Protocol:    serverConfig.Protocol,
        Command:     serverConfig.Command,
        Args:        serverConfig.Args,
        Env:         serverConfig.Env,
        WorkingDir:  serverConfig.WorkingDir,
        Enabled:     serverConfig.Enabled,
        Quarantined: serverConfig.Quarantined,
        Headers:     serverConfig.Headers,
        Created:     serverConfig.Created,
        Updated:     time.Now(),
        // BUG: Isolation field NOT being copied!
        // Missing: Isolation: serverConfig.Isolation,
    }
    return am.db.SaveUpstream(record)
}
```

**Impact**: When servers are saved through the async path (used by save operations), the Isolation config is dropped.

### Bug #2: SaveConfiguration Overwrite Pattern

**File**: `/Users/user/repos/mcpproxy-go/internal/runtime/lifecycle.go` lines 711-783

```go
func (r *Runtime) SaveConfiguration() error {
    // Gets servers from storage (which may have lost Isolation)
    latestServers, err := r.storageManager.ListUpstreamServers()

    // ...

    // OVERWRITES config servers entirely
    configCopy.Servers = latestServers  // Line 736

    // Writes to disk - losing any config-level fields not in storage
}
```

**Impact**: Even if Isolation was in the config file, it gets lost when SaveConfiguration retrieves from storage and overwrites.

### Data Flow Showing the Loss

```
1. User adds server with Isolation config via CLI
   ↓
2. Server is stored (correctly) via SaveUpstreamServer()
   - Isolation IS preserved in BBolt
   ↓
3. Server auto-quarantines (new server security feature)
   ↓
4. User unquarantines via Web UI
   ↓
5. QuarantineServer() calls:
   - quarantineServerSync() - only modifies quarantined field
   - SaveConfiguration() - retrieves from storage
   ↓
6. SaveConfiguration() retrieves servers from storage
   - If any async save operation had dropped Isolation, it's lost
   - Config file is overwritten with storage data
   ↓
7. Result: Isolation config is gone
```

## Affected Code Locations

| File | Function | Line | Issue |
|------|----------|------|-------|
| `internal/storage/async_ops.go` | `saveServerSync()` | 170-187 | Missing `Isolation` field |
| `internal/runtime/lifecycle.go` | `SaveConfiguration()` | 711-783 | Overwrites without merge |
| `internal/server/mcp.go` | `handlePatchUpstream()` | 2792-2906 | Uses full replacement |
| `internal/server/mcp.go` | `handleUpdateUpstream()` | 2665-2790 | Uses full replacement |

## Correct Patterns Already Implemented

The synchronous storage layer correctly handles Isolation:

**SaveUpstreamServer** (`internal/storage/manager.go` lines 80-103):
```go
record := &UpstreamRecord{
    // ... other fields ...
    Isolation:   serverConfig.Isolation,  // ✓ Correctly copies
}
```

**GetUpstreamServer** (`internal/storage/manager.go` lines 106-131):
```go
return &config.ServerConfig{
    // ... other fields ...
    Isolation:   record.Isolation,  // ✓ Correctly retrieves
}
```

**ListUpstreamServers** (`internal/storage/manager.go` lines 134-164):
```go
servers = append(servers, &config.ServerConfig{
    // ... other fields ...
    Isolation:   record.Isolation,  // ✓ Correctly retrieves
})
```

## Research Decisions

### Decision 1: Fix Missing Field vs Implement Deep Merge

**Decision**: Implement both - fix the immediate bug AND add proper deep merge

**Rationale**:
1. The missing `Isolation` field is a quick fix but doesn't prevent future bugs
2. Deep merge provides defense-in-depth against field loss
3. Deep merge enables proper partial patch semantics per spec requirements

**Alternatives Considered**:
- Just fix the missing field: Risk of regression with new fields
- Use external JSON patch library: Added complexity, dependency management

### Decision 2: Merge Strategy for Different Field Types

**Decision**: Use RFC 7396-inspired merge with array replacement

| Field Type | Strategy | Rationale |
|------------|----------|-----------|
| Scalars | Replace | Standard behavior |
| Objects (map/struct) | Deep merge | Preserve nested fields |
| Arrays | Replace entirely | No merge key available, order matters |
| Null values | Remove field | RFC 7396 semantics |
| Omitted fields | Preserve | Never lose user data |

**Alternatives Considered**:
- RFC 6902 (JSON Patch): Too complex for our use case
- Strategic Merge Patch with merge keys: Arrays don't have identity fields
- Full replacement: Current broken behavior

### Decision 3: Implementation Location

**Decision**: Create new `internal/config/merge.go` utility

**Rationale**:
1. Centralized merge logic for all update paths
2. Easy to test independently
3. Can be used by MCP tool, REST API, CLI, and internal operations

**Alternatives Considered**:
- Fix each handler individually: Code duplication, risk of inconsistency
- Use storage layer merge: Wrong abstraction level
- External library: Unnecessary complexity

### Decision 4: Diff Logging Implementation

**Decision**: Add optional diff logging in merge utility

**Rationale**:
1. FR-006 requires config change audit trail
2. Can be enabled/disabled per operation
3. Consistent diff format across all update paths

## Technical Implementation Plan

### Phase 1: Fix Immediate Bug
1. Add `Isolation` field to `saveServerSync()` in `async_ops.go`
2. Also add `OAuth` field if missing (defensive)
3. Add test to verify all fields are copied

### Phase 2: Implement Deep Merge Utility
1. Create `internal/config/merge.go`:
   ```go
   // MergeServerConfig deep merges patch into base, returning merged config
   func MergeServerConfig(base, patch *ServerConfig) (*ServerConfig, *ConfigDiff, error)

   // ConfigDiff represents changes made during merge
   type ConfigDiff struct {
       Modified map[string]FieldChange
       Added    []string
       Removed  []string
   }
   ```

2. Handle field types:
   - Scalars: Replace if patch value is non-zero
   - Objects: Recursively merge
   - Arrays: Replace entirely
   - Null/zero: Preserve unless explicit removal requested

### Phase 3: Update All Update Paths
1. MCP tool handlers (`handlePatchUpstream`, `handleUpdateUpstream`)
2. REST API handlers (quarantine, enable, etc.)
3. CLI commands
4. SaveConfiguration flow

### Phase 4: Add Tests
1. Unit tests for merge utility
2. E2E tests for isolation preservation
3. E2E tests for MCP tool patch operations

## Go Libraries Assessment

**Current go.mod**: No JSON patch or merge libraries present

**Recommendation**: Implement custom merge (simple requirements):
- No need for RFC 6902 JSON Patch (operations-based)
- No need for RFC 7396 JSON Merge Patch library (simple enough to implement)
- Custom implementation allows precise control over merge semantics

**Libraries Evaluated**:
- `github.com/evanphx/json-patch/v5`: Overkill for our needs
- `github.com/imdario/mergo`: Generic Go merge, but doesn't handle null semantics
- `github.com/r3labs/diff`: Good for diff generation but not merge

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| New fields added without merge support | Medium | High | Add test that verifies all ServerConfig fields are handled |
| Concurrent modification race | Low | Medium | Use existing mutex patterns in storage layer |
| Breaking existing behavior | Low | High | Comprehensive E2E tests before merge |

## Success Metrics

1. `async_ops.go` saveServerSync includes all fields
2. Merge utility handles all ServerConfig fields
3. All E2E tests pass
4. Isolation config preserved through quarantine toggle
5. Isolation config preserved through MCP patch operations
