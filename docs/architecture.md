# MCPProxy Architecture

This document describes the internal architecture of MCPProxy, including the management service, runtime lifecycle, and event system.

## Management Service Architecture

The management service (`internal/management/`) provides a centralized business logic layer for upstream server management operations, eliminating code duplication across CLI, REST API, and MCP interfaces.

### Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                    Client Interfaces                         │
├───────────────┬─────────────────┬───────────────────────────┤
│  CLI Commands │   REST API      │   MCP Protocol            │
│  (upstream)   │   (/api/v1/*)   │   (upstream_servers tool) │
└───────┬───────┴────────┬────────┴───────────┬───────────────┘
        │                │                    │
        └────────────────┼────────────────────┘
                         │
                         ▼
              ┌─────────────────────┐
              │  Management Service │
              │  (internal/mgmt/)   │
              └──────────┬──────────┘
                         │
        ┌────────────────┼────────────────┐
        │                │                │
        ▼                ▼                ▼
  ┌──────────┐    ┌──────────┐    ┌──────────┐
  │ Runtime  │    │  Config  │    │  Events  │
  │Operations│    │  Gates   │    │  Emitter │
  └──────────┘    └──────────┘    └──────────┘
```

### Key Components

- **Service Interface** (`service.go:16-102`): Defines all management operations
  - Single-server: `RestartServer()`, `EnableServer()`, `DisableServer()`
  - Bulk operations: `RestartAll()`, `EnableAll()`, `DisableAll()`
  - Diagnostics: `GetServerHealth()`, `RunDiagnostics()`
  - Server CRUD: `AddServer()`, `RemoveServer()`, `QuarantineServer()`
  - Tool operations: `GetServerTools()`, `TriggerOAuthLogin()`

- **Configuration Gates**: All operations respect centralized configuration guards
  - `disable_management`: Blocks all write operations when true
  - `read_only_mode`: Blocks all configuration modifications

- **Bulk Operations** (`service.go:243-388`): Efficient multi-server management
  - Sequential execution with partial failure handling
  - Returns `BulkOperationResult` with success/failure counts
  - Collects per-server errors in results map
  - Continues on individual failures, reports aggregate results

- **Event Integration**: All operations emit events through event bus
  - `servers.changed`: Notifies UI of server state changes
  - Triggers SSE updates to web UI and tray application
  - Enables real-time synchronization across interfaces

### Benefits

- **Code Deduplication**: 40%+ reduction in duplicate code across interfaces
- **Consistent Behavior**: All interfaces use identical business logic
- **Centralized Validation**: Configuration gates enforced in one place
- **Easier Testing**: Unit tests cover all interfaces through service layer
- **Future Extensibility**: New interfaces can reuse existing service methods

### Usage Examples

```go
// CLI usage (cmd/mcpproxy/upstream_cmd.go:547-636)
result, err := client.RestartAll(ctx)
fmt.Printf("  Total servers:      %d\n", result.Total)
fmt.Printf("  ✅ Successful:      %d\n", result.Successful)
fmt.Printf("  ❌ Failed:          %d\n", result.Failed)

// REST API usage (internal/httpapi/server.go:772-866)
mgmtSvc := s.controller.GetManagementService().(ManagementService)
result, err := mgmtSvc.RestartAll(r.Context())
s.writeSuccess(w, result)

// MCP protocol usage (future integration)
result, err := mgmtService.RestartAll(ctx)
return mcpResponse(result)
```

## Runtime Architecture

The runtime package (`internal/runtime/`) provides core non-HTTP lifecycle management, separating concerns from the HTTP server layer.

### Runtime Package Components

- **Configuration Management**: Centralized config loading, validation, and hot-reload
- **Background Services**: Connection management, tool indexing, and health monitoring
- **State Management**: Thread-safe status tracking and upstream server state
- **Event System**: Real-time event broadcasting for UI and SSE consumers

### Event Bus System

The event bus enables real-time communication between runtime and UI components:

**Event Types**:
- `servers.changed` - Server configuration or state changes
- `config.reloaded` - Configuration file reloaded from disk

**Event Flow**:
1. Runtime operations trigger events via `emitServersChanged()` and `emitConfigReloaded()`
2. Events are broadcast to subscribers through buffered channels
3. Server forwards events to tray UI and SSE endpoints
4. Tray menus refresh automatically without file watching
5. Web UI receives live updates via `/events` SSE endpoint

**SSE Integration**:
- `/events` endpoint streams both status updates and runtime events
- Automatic connection management with proper cleanup
- JSON-formatted event payloads for easy consumption

### Config Commit (Spec 116)

Every change to the server set, or to any server's fields, is one **config
commit** (`Runtime.Commit`, `internal/runtime/commit.go`). The live
configuration is the only source of server membership and fields; the
`upstreams` rows in `config.db` are its projection, written by the commit.
`SaveConfiguration` never rebuilds the server list from storage, so a removed
server cannot be resurrected by a later save.

A commit holds `configCommitMu` from reading the live config until the new
config is published and runs one pipeline:

1. **Build**: clone the live config; the caller mutates the copy through a
   `CommitTx` (`CreateServer`, `UpdateServer`, `SetEnabled`, `SetQuarantined`,
   `RemoveServer`, `ReplaceConfig`, `ReconcileLive`, `MutateGlobal`).
2. **Gate**: the #937 admission gate, against the stored view.
3. **Prepare**: `configsvc.Prepare` assigns the commit generation `G` and
   per-server generations (`ServerGen{Gen, Incarnation}`; an unchanged entry
   keeps its generation, a re-created name gets a new incarnation).
4. **Locks**: the approval lock of every server step 6 writes, sorted:
   changed servers, servers with an `UnderApprovalLock` hook, removals,
   replacements, OAuth-state clears and projection-repair saves of a server
   the commit did not change (a repair can rewrite the stored trust and
   quarantine policy a discovery pass reads under that lock). A global
   approval-policy change (`quarantine_enabled`) takes every configured
   server's lock. Dropping the lock for an unchanged server whose row is
   repaired reopens the discovery-versus-repair race (#1571).
5. **Disk** (only when a path is set and the commit writes the file): the raw
   file is captured, then the new file is written. A file reload
   (`ReloadConfiguration`) never writes the file, so it skips this step.
6. **Storage**: one bbolt transaction (rows with their stamps, deletions,
   `meta.config_commit_generation`), then the index purge for removed,
   quarantined and disabled servers, then the in-memory purge (last-good tool
   snapshot, capture stamp). A removed or replaced server loses its whole
   **purge set** in that transaction (`internal/storage/purge.go`): tool and
   prompt approvals, OAuth tokens and DCR client (exact `name` or
   `name_<16 hex>` key), OAuth completion events, scan reports and jobs, the
   integrity baseline, tool-call history and identities, and tool hashes.
   The in-memory purge also bumps the server's last-good purge sequence, so a
   local reindex that copied the snapshot before the removal drops its copy
   (it re-checks the sequence under the approval lock).
   The approval locks are released, then — still under the commit lock — the
   purged servers' OAuth refresh schedules are cancelled and their inspection
   exemptions revoked (leaf locks only), so no later commit's same-name
   replacement can lose a fresh schedule or exemption to a name-keyed clear.
   On a
   storage failure nothing is published; when step 5 wrote the file, the
   captured file is restored byte for byte (or removed if it did not exist).
   A failed reload wrote nothing, so the file keeps the operator's edit
   unapplied: the next reload applies it, but the next disk-writing commit
   (`SaveConfiguration`, a server mutation, `ApplyConfig`) writes the live
   config over it.
7. **Advance**: `Manager.AdvanceGenerations` records every configured
   server's committed generation in the upstream manager (and a tombstone
   for a removed one), unregisters the instance of every removed or replaced
   server and of every server whose connection settings, `enabled` or
   `quarantined` changed, and restamps the retained instances with their new
   config and generation (Spec 116 PR3).
8. **Publish**: `configsvc.Publish` and the legacy `r.cfg` swap.

After the lock is released the commit disconnects the instances step 7
unregistered, reconciles the supervisor when an exemption was revoked, drops
cached scan verdicts (purge observers; the scanner's verdict cache is also
keyed by incarnation, so a replacement is never served the old entry before
the observer runs), schedules connect workers, requests discovery for
restamped instances and sends its events once.

**Generation-bound workers and supervisor actions** (Spec 116 PR3, #1563).
Everything that changes the manager's registry or connects an instance
asynchronously is bound to the commit generation it was scheduled for: the
commit's connect/disconnect workers (`AddServerAt`, `RemoveServerAt`), the
supervisor's actions (generation from the snapshot they were planned from),
`RestartServer`, `ForceReconnectAll` and Docker recovery. The manager
re-checks the generation against its desired view under `captureMu` +
`Manager.mu`, in the same critical section as the change, and a stale call
returns `ErrStaleGeneration` and does nothing (logged at debug level). An
action on an existing instance (a supervisor disconnect, an OAuth logout's
disconnect, a restart or reconnect replacement through `ReplaceInstanceAt`)
is also bound to that instance's `InstanceID`, because a restart installs a
new instance at the same generation. Every instance leaves the registry
through one `unregisterLocked`, which marks it unregistered so it never
connects again (Spec 116 PR4 turns this into a full retirement barrier).
Discovery and review captures carry the generation of the instance they
listed and are dropped when it is not the stored row's; a last-good tool
snapshot carries it too, so an approval-driven reindex never reapplies an
old endpoint's tools after an endpoint change (it re-lists instead).
`scripts/check-upstream-write-sites.sh` also fails on an unbound manager
mutation in `internal/runtime` or `internal/server`, and on any change to
`Manager.clients` outside `unregisterLocked`/`installLocked`.

**Incarnation-bound approvals** (Spec 116 PR2). Each server has an
incarnation that changes only when its name is removed and created again.
Every approval writer (tool approve/block/toggle, the unquarantine baseline
promotion, prompt approvals, the scanner's baseline and blocks) captures the
incarnation when it starts — or takes the client's `expected_incarnation` —
and re-checks it against the stored row under the server's approval lock,
the lock the removal purges under. A mismatch writes nothing and returns
`ErrServerReplaced` (REST 409 `server_replaced`). Scanner approval
unquarantines through a commit with `ExpectIncarnation`. Asynchronous
writers that can outlive a removal (scan job and report saves, identity
registration, tool-call history) use storage methods that check the row's
incarnation in the same bbolt transaction as the write. A discovery pass makes its final eligibility check (stored row
present, enabled, not quarantined, generation unchanged) and its index writes
in one hold of the server's approval lock, and approval policy is read from
storage, so a pass can never re-index a server a commit has just quarantined
or removed.

**Lock order** (outermost first): `configCommitMu` → server approval locks
(sorted; released before step 7) → `storage.Manager.mu`/bbolt; `configCommitMu`
→ `upstream.Manager.captureMu` (W) → `Manager.mu` in step 7; `configCommitMu`
→ `configsvc` update lock → `Runtime.mu`. Code holding any of the inner locks
must never call `Commit`. `scripts/check-upstream-write-sites.sh` (CI) fails
when an `upstreams` row is written anywhere but the storage package and
`internal/runtime/commit*.go`.

### Runtime Lifecycle

**Initialization**:
1. Runtime created with config, logger, and manager dependencies
2. Background initialization starts server connections and tool indexing
3. Status updates broadcast through event system

**Background Services**:
- **Connection Management**: Periodic reconnection attempts with exponential backoff
- **Tool Indexing**: Automatic discovery and search index updates every 15 minutes
- **Configuration Sync**: File-based config changes trigger runtime resync

**Shutdown**:
- Graceful context cancellation cascades to all background services
- Upstream servers disconnected with proper subprocess and Docker container cleanup
- Resources closed in dependency order (upstream → cache → index → storage)

**Subprocess Shutdown Flow**:
1. **Graceful Close** (10s timeout): Close MCP client connection, wait for subprocess to exit cleanly
2. **Force Kill** (9s timeout): If graceful close fails, send SIGTERM to process group, poll for exit, then SIGKILL

| Timeout | Value | Purpose |
|---------|-------|---------|
| MCP Client Close | 10s | Wait for graceful stdin/stdout close |
| SIGTERM → SIGKILL | 9s | Time between graceful and force kill |
| Docker Cleanup | 30s | Container stop/kill timeout |

See [Shutdown Behavior](/operations/shutdown-behavior) for detailed documentation.

## Tray Application Architecture

The tray application uses a robust state machine architecture for reliable core management.

### State Machine States

- Normal flow: `StateInitializing` → `StateLaunchingCore` → `StateWaitingForCore` → `StateConnectingAPI` → `StateConnected`
- Error states: `StateCoreErrorPortConflict`, `StateCoreErrorDBLocked`, `StateCoreErrorGeneral`, `StateCoreErrorConfig`
- Recovery states: `StateReconnecting`, `StateFailed`, `StateShuttingDown`

### Key Components

- **Process Monitor** (`cmd/mcpproxy-tray/internal/monitor/process.go`): Monitors core subprocess lifecycle
- **Health Monitor** (`cmd/mcpproxy-tray/internal/monitor/health.go`): Performs socket-aware HTTP health checks on core API (`/healthz`, `/readyz`)
- **State Machine** (`cmd/mcpproxy-tray/internal/state/machine.go`): Manages state transitions and automatic retry logic

### Error Classification

Core process exit codes are mapped to specific state machine events:
- Exit code 2 (port conflict) → `EventPortConflict`
- Exit code 3 (database locked) → `EventDBLocked`
- Exit code 4 (config error) → `EventConfigError`
- Exit code 5 (permission error) → `EventPermissionError`
- Exit code 6 (shutdown timeout: graceful shutdown exceeded its 60s hard deadline after SIGINT/SIGTERM) → `EventGeneralError` (no dedicated event; a restart is the right response)
- Other errors → `EventGeneralError`

### Automatic Retry Logic

Error states automatically retry core launch with exponential backoff:
- `StateCoreErrorGeneral`: 2 retries with 3s delay (3 total attempts)
- `StateCoreErrorPortConflict`: 2 retries with 10s delay
- `StateCoreErrorDBLocked`: 3 retries with 5s delay
- After max retries exceeded → transitions to `StateFailed`

### Development Environment Variables

- `MCPPROXY_TRAY_SKIP_CORE=1` - Skip core launch (for development)
- `MCPPROXY_CORE_URL=http://localhost:8085` - Custom core URL
- `MCPPROXY_TRAY_PORT=8090` - Custom tray port

## Related Documentation

- [Socket Communication](socket-communication.md) - Tray-core IPC details
- [CLI Management Commands](cli-management-commands.md) - CLI reference
- [Configuration](configuration.md) - Full configuration reference
