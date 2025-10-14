# Phase 7: Complete StateView Migration & Actor Integration

**Status**: 📋 Planning | **Goal**: Direct replacement of legacy paths with StateView + Actors

## Overview

Phase 7 completes the architectural refactoring by:
1. Migrating remaining API endpoints to StateView for consistent <50ms performance
2. Replacing UpstreamAdapter with native Actor integration in Supervisor
3. Removing all legacy code paths

**No Feature Flags**: This is a direct replacement strategy with thorough testing at each step.

## Phase 7.1: Complete API Migration to StateView

**Goal**: All API endpoints use StateView for instant, lock-free reads.

### Current State Analysis

**Already Migrated** (Phase 6):
- `GetAllServers()` - ✅ Uses StateView, 15-25ms response time

**Needs Migration**:
- `GetQuarantinedServers()` ([server.go:539-585](internal/server/server.go#L539-L585))
  - Current: Queries `storage.GetServers()` then filters quarantined servers
  - Problem: Storage I/O during indexing causes 30+ second delays
  - Solution: Filter StateView snapshot by `Quarantined` field

- `GetServerTools()` ([server.go:1129-1168](internal/server/server.go#L1129-L1168))
  - Current: Queries `upstream.GetClient()` then calls `client.ListTools()`
  - Problem: Blocks on upstream client lock contention
  - Solution: Read cached tools from StateView

### Implementation Plan

#### Step 1: Add Tool Caching to StateView

**File**: `internal/runtime/supervisor/state_view.go`

Add tools to `ServerStatus`:
```go
type ServerStatus struct {
    Name            string
    Enabled         bool
    Quarantined     bool
    Connected       bool
    State           string
    ToolCount       int
    Tools           []ToolInfo    // NEW: Cached tool list
    LastError       string
    RetryCount      int
    Config          *config.ServerConfig
    ConnectionInfo  *types.ConnectionInfo
}

type ToolInfo struct {
    Name        string
    Description string
    InputSchema map[string]interface{}
}
```

**Rationale**: StateView already has ToolCount, adding Tools makes tool queries lock-free too.

#### Step 2: Populate Tools in UpdatePhase

**File**: `internal/runtime/supervisor/adapter.go` (UpstreamAdapter)

Modify `GetStats()` to include tools:
```go
func (a *UpstreamAdapter) GetStats() map[string]*ServerStats {
    stats := make(map[string]*ServerStats)

    for name, client := range a.upstreamManager.GetAllClients() {
        info := client.GetConnectionInfo()

        // Get tools if connected
        var tools []ToolInfo
        if client.IsConnected() {
            toolList, _ := client.ListTools()
            tools = make([]ToolInfo, len(toolList))
            for i, t := range toolList {
                tools[i] = ToolInfo{
                    Name:        t.Name,
                    Description: t.Description,
                    InputSchema: t.InputSchema,
                }
            }
        }

        stats[name] = &ServerStats{
            Connected:      client.IsConnected(),
            ToolCount:      len(tools),
            Tools:          tools,  // NEW
            ConnectionInfo: info,
        }
    }
    return stats
}
```

#### Step 3: Migrate GetQuarantinedServers

**File**: `internal/server/server.go`

Replace storage query with StateView filter:
```go
func (s *Server) GetQuarantinedServers() ([]map[string]interface{}, error) {
    s.logger.Debug("GetQuarantinedServers called (Phase 7: using StateView)")

    // Use StateView for lock-free read
    supervisor := s.runtime.Supervisor()
    if supervisor == nil {
        return nil, fmt.Errorf("supervisor not available")
    }

    snapshot := supervisor.StateView().Snapshot()

    result := make([]map[string]interface{}, 0)
    for _, serverStatus := range snapshot.Servers {
        if !serverStatus.Quarantined {
            continue
        }

        result = append(result, map[string]interface{}{
            "name":        serverStatus.Name,
            "quarantined": true,
            "enabled":     serverStatus.Enabled,
            "connected":   serverStatus.Connected,
            "tool_count":  serverStatus.ToolCount,
        })
    }

    return result, nil
}
```

**Remove**: `storage.GetServers()` call and quarantine filtering logic.

#### Step 4: Migrate GetServerTools

**File**: `internal/server/server.go`

Replace upstream client query with StateView read:
```go
func (s *Server) GetServerTools(serverName string) ([]map[string]interface{}, error) {
    s.logger.Debug("GetServerTools called", "server", serverName)

    // Phase 7: Use StateView for cached tools
    supervisor := s.runtime.Supervisor()
    if supervisor == nil {
        return nil, fmt.Errorf("supervisor not available")
    }

    snapshot := supervisor.StateView().Snapshot()
    serverStatus, exists := snapshot.Servers[serverName]
    if !exists {
        return nil, fmt.Errorf("server not found: %s", serverName)
    }

    if !serverStatus.Connected {
        return nil, fmt.Errorf("server %s is not connected", serverName)
    }

    result := make([]map[string]interface{}, len(serverStatus.Tools))
    for i, tool := range serverStatus.Tools {
        result[i] = map[string]interface{}{
            "name":         tool.Name,
            "description":  tool.Description,
            "inputSchema":  tool.InputSchema,
        }
    }

    return result, nil
}
```

**Remove**: `s.runtime.UpstreamManager().GetClient(serverName)` and `client.ListTools()` calls.

#### Step 5: Testing

```bash
# Build and run
go build -o mcpproxy ./cmd/mcpproxy
./mcpproxy serve --log-level=debug

# Test quarantined servers endpoint
time curl -H "X-API-Key: $MCPPROXY_API_KEY" http://127.0.0.1:8080/api/v1/servers/quarantined
# Expected: <50ms

# Test server tools endpoint
time curl -H "X-API-Key: $MCPPROXY_API_KEY" http://127.0.0.1:8080/api/v1/servers/github-server/tools
# Expected: <50ms, returns cached tools

# Verify during tool indexing (wait for "Indexing tools..." log)
time curl -H "X-API-Key: $MCPPROXY_API_KEY" http://127.0.0.1:8080/api/v1/servers
# Expected: Still <50ms, no blocking
```

**Success Criteria**:
- All API endpoints respond in <50ms
- No storage I/O blocking
- Tools are cached and accurate
- Unit tests pass with `-race`

---

## Phase 7.2: Replace UpstreamAdapter with Native Actor Integration

**Goal**: Supervisor uses Actors directly, eliminating translation layer.

### Current Architecture Issues

**UpstreamAdapter Problem** ([adapter.go:1-200](internal/runtime/supervisor/adapter.go)):
- Translation layer between Supervisor and UpstreamManager
- Supervisor calls UpstreamAdapter → UpstreamAdapter calls UpstreamManager → UpstreamManager calls Actors
- Two layers of indirection for every operation
- Stats polling every second adds overhead

**Desired Architecture**:
- Supervisor → ActorPool → Actors (direct)
- Actors emit events → Supervisor updates StateView
- No polling, pure event-driven updates

### Implementation Plan

#### Step 1: Create ActorPool Manager

**New File**: `internal/runtime/supervisor/actor_pool.go`

```go
package supervisor

import (
    "context"
    "sync"

    "mcpproxy-go/internal/config"
    "mcpproxy-go/internal/runtime/actor"
    "mcpproxy-go/internal/upstream/types"
    "go.uber.org/zap"
)

// ActorPool manages Actor lifecycle and provides stats for Supervisor.
type ActorPool struct {
    actors map[string]*actor.Actor
    mu     sync.RWMutex
    logger *zap.SugaredLogger
}

// NewActorPool creates a new actor pool.
func NewActorPool(logger *zap.SugaredLogger) *ActorPool {
    return &ActorPool{
        actors: make(map[string]*actor.Actor),
        logger: logger,
    }
}

// AddServer creates and starts an actor for the given server.
func (p *ActorPool) AddServer(ctx context.Context, serverCfg *config.ServerConfig) error {
    p.mu.Lock()
    defer p.mu.Unlock()

    if _, exists := p.actors[serverCfg.Name]; exists {
        return fmt.Errorf("actor already exists: %s", serverCfg.Name)
    }

    // Create actor with event callback
    a := actor.New(serverCfg, p.logger)

    // Start actor (non-blocking)
    go a.Start(ctx)

    p.actors[serverCfg.Name] = a
    p.logger.Info("Actor started", "server", serverCfg.Name)

    return nil
}

// RemoveServer stops and removes an actor.
func (p *ActorPool) RemoveServer(serverName string) error {
    p.mu.Lock()
    defer p.mu.Unlock()

    a, exists := p.actors[serverName]
    if !exists {
        return fmt.Errorf("actor not found: %s", serverName)
    }

    a.Stop()
    delete(p.actors, serverName)
    p.logger.Info("Actor stopped", "server", serverName)

    return nil
}

// UpdateServer updates an actor's configuration.
func (p *ActorPool) UpdateServer(serverCfg *config.ServerConfig) error {
    p.mu.Lock()
    defer p.mu.Unlock()

    a, exists := p.actors[serverCfg.Name]
    if !exists {
        return fmt.Errorf("actor not found: %s", serverCfg.Name)
    }

    // Actor handles config updates internally
    a.UpdateConfig(serverCfg)

    return nil
}

// GetStats returns current stats for all actors.
func (p *ActorPool) GetStats() map[string]*ServerStats {
    p.mu.RLock()
    defer p.mu.RUnlock()

    stats := make(map[string]*ServerStats, len(p.actors))

    for name, a := range p.actors {
        info := a.GetConnectionInfo()
        tools := a.GetTools()

        stats[name] = &ServerStats{
            Connected:      a.IsConnected(),
            ToolCount:      len(tools),
            Tools:          convertTools(tools),
            ConnectionInfo: info,
        }
    }

    return stats
}

func convertTools(tools []types.Tool) []ToolInfo {
    result := make([]ToolInfo, len(tools))
    for i, t := range tools {
        result[i] = ToolInfo{
            Name:        t.Name,
            Description: t.Description,
            InputSchema: t.InputSchema,
        }
    }
    return result
}
```

**Rationale**: Direct Actor management without UpstreamManager indirection.

#### Step 2: Add Actor Event Subscription

**Modify**: `internal/runtime/actor/actor.go`

Add event callback to Actor:
```go
type Actor struct {
    // ... existing fields ...
    onStateChange func(oldState, newState types.ConnectionState, info *types.ConnectionInfo)
}

// SetStateChangeCallback sets a callback for state changes.
func (a *Actor) SetStateChangeCallback(cb func(oldState, newState types.ConnectionState, info *types.ConnectionInfo)) {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.onStateChange = cb
}

// In transitionTo method, call the callback:
func (a *Actor) transitionTo(newState types.ConnectionState) {
    a.mu.Lock()
    oldState := a.state
    a.state = newState
    cb := a.onStateChange
    info := a.connectionInfo.Clone()
    a.mu.Unlock()

    a.logger.Info("Actor state transition", "server", a.name, "from", oldState, "to", newState)

    // Emit metrics
    if a.metricsManager != nil {
        a.metricsManager.RecordActorStateTransition(a.name, oldState.String(), newState.String())
    }

    // Call callback if set (for Supervisor)
    if cb != nil {
        cb(oldState, newState, info)
    }
}
```

**Rationale**: Event-driven updates instead of polling.

#### Step 3: Wire ActorPool into Supervisor

**Modify**: `internal/runtime/supervisor/supervisor.go`

Replace `ServerAdapter` with `ActorPool`:
```go
type Supervisor struct {
    configSvc  *configsvc.ConfigService
    actorPool  *ActorPool          // CHANGED: was ServerAdapter
    stateView  *StateView
    // ... rest unchanged ...
}

func New(configSvc *configsvc.ConfigService, logger *zap.SugaredLogger) *Supervisor {
    actorPool := NewActorPool(logger)

    s := &Supervisor{
        configSvc:  configSvc,
        actorPool:  actorPool,  // CHANGED
        stateView:  NewStateView(),
        // ... rest unchanged ...
    }

    // Subscribe to actor events
    s.setupActorEventHandlers()

    return s
}

func (s *Supervisor) setupActorEventHandlers() {
    // When actors change state, update StateView immediately
    // (Implementation in ActorPool.AddServer to set callbacks)
}
```

#### Step 4: Update Reconciliation Actions

**Modify**: `internal/runtime/supervisor/supervisor.go`

Replace adapter calls with ActorPool calls:
```go
func (s *Supervisor) executeAction(serverName string, action ReconcileAction, configSnapshot *configsvc.Snapshot) error {
    switch action {
    case ActionConnect:
        serverCfg := configSnapshot.FindServer(serverName)
        if serverCfg == nil {
            return fmt.Errorf("server config not found: %s", serverName)
        }
        return s.actorPool.AddServer(s.ctx, serverCfg)  // CHANGED: was adapter.AddServer

    case ActionDisconnect:
        return s.actorPool.RemoveServer(serverName)  // CHANGED: was adapter.RemoveServer

    case ActionReconnect:
        if err := s.actorPool.RemoveServer(serverName); err != nil {
            s.logger.Warn("Failed to remove server during reconnect", "server", serverName, "error", err)
        }
        serverCfg := configSnapshot.FindServer(serverName)
        if serverCfg == nil {
            return fmt.Errorf("server config not found: %s", serverName)
        }
        return s.actorPool.AddServer(s.ctx, serverCfg)  // CHANGED

    case ActionRemove:
        return s.actorPool.RemoveServer(serverName)  // CHANGED

    default:
        return fmt.Errorf("unknown action: %s", action)
    }
}
```

#### Step 5: Update Runtime Initialization

**Modify**: `internal/runtime/runtime.go`

Remove UpstreamAdapter creation:
```go
func New(opts *Options) (*Runtime, error) {
    // ... existing initialization ...

    // Phase 7: Supervisor uses Actors directly, no adapter needed
    supervisorInstance := supervisor.New(configSvc, logger)

    r := &Runtime{
        configService:   configSvc,
        upstreamManager: upstreamManager,  // Keep for legacy MCP proxy paths
        supervisor:      supervisorInstance,
        // ... rest unchanged ...
    }

    return r, nil
}
```

**Note**: Keep `upstreamManager` temporarily for MCP proxy tool calls until Phase 8.

#### Step 6: Testing

```bash
# Build and test
go build -o mcpproxy ./cmd/mcpproxy
./mcpproxy serve --log-level=debug

# Verify actors start correctly
# Should see: "Actor started" logs for each server

# Test API endpoints still work
curl -H "X-API-Key: $MCPPROXY_API_KEY" http://127.0.0.1:8080/api/v1/servers
# Expected: <50ms, all servers listed

# Test state changes (enable/disable server)
curl -X POST -H "X-API-Key: $MCPPROXY_API_KEY" http://127.0.0.1:8080/api/v1/servers/github-server/enable?enabled=false
# Expected: Actor stopped, state updated immediately

# Run race detector
go test -race ./internal/runtime/supervisor/...
# Expected: No data races
```

**Success Criteria**:
- All servers connect via Actors
- StateView updates instantly on state changes
- No UpstreamAdapter code paths executed
- All tests pass with `-race`

---

## Phase 7.3: Remove Legacy Code

**Goal**: Delete all legacy code paths and unused abstractions.

### Files to Remove

1. **`internal/runtime/supervisor/adapter.go`** (200 lines)
   - UpstreamAdapter no longer needed
   - Replaced by ActorPool

2. **`internal/server/server.go`** - Remove methods:
   - `getAllServersLegacy()` (lines 451-536)
   - Any fallback paths checking `if supervisor == nil`

### Code Cleanup

**File**: `internal/server/server.go`

Simplify GetAllServers (remove legacy fallback):
```go
func (s *Server) GetAllServers() ([]map[string]interface{}, error) {
    s.logger.Debug("GetAllServers called")

    snapshot := s.runtime.Supervisor().StateView().Snapshot()

    result := make([]map[string]interface{}, 0, len(snapshot.Servers))
    for _, serverStatus := range snapshot.Servers {
        // ... convert to API response ...
    }

    return result, nil
}
```

**Remove**:
- `if supervisor == nil { return s.getAllServersLegacy() }` checks
- `getAllServersLegacy()` method entirely

### Documentation Updates

**Files to Update**:
1. `ARCHITECTURE.md` - Mark Phase 7 complete, update architecture diagrams
2. `README.md` - Update architecture section if needed
3. `CLAUDE.md` - Note Phase 7 completion

### Testing

```bash
# Full test suite
go test ./internal/... -v -race

# E2E tests
./scripts/run-all-tests.sh

# API E2E tests
./scripts/test-api-e2e.sh

# Build tray + core
make build

# Manual testing
./mcpproxy-tray
# - Verify system tray works
# - Check all menu items
# - Test server enable/disable
# - Verify Web UI loads and works
```

**Success Criteria**:
- No compilation errors
- All tests pass
- No race conditions
- API response times <50ms
- No legacy code paths remaining

---

## Implementation Order

Execute phases sequentially:

1. **Phase 7.1**: Complete API Migration (~2-3 hours)
   - Add tool caching to StateView
   - Migrate GetQuarantinedServers
   - Migrate GetServerTools
   - Test all endpoints
   - Commit: "Phase 7.1: Migrate all API endpoints to StateView"

2. **Phase 7.2**: Actor Integration (~4-5 hours)
   - Create ActorPool
   - Add Actor event callbacks
   - Wire into Supervisor
   - Update reconciliation
   - Test thoroughly
   - Commit: "Phase 7.2: Replace UpstreamAdapter with ActorPool"

3. **Phase 7.3**: Cleanup (~1-2 hours)
   - Remove adapter.go
   - Remove getAllServersLegacy
   - Update documentation
   - Final testing
   - Commit: "Phase 7.3: Remove legacy code paths"

**Total Estimate**: 7-10 hours of focused work

---

## Risk Mitigation

### No Feature Flags

This plan uses **direct replacement** with thorough testing at each step:
- Each phase has clear success criteria
- Tests run before moving to next phase
- Rollback is standard git revert

### Testing Strategy

1. **Unit Tests**: Cover all new StateView functionality
2. **Race Detection**: Run all tests with `-race` flag
3. **API Tests**: Verify <50ms response times
4. **E2E Tests**: Full flow from tray → core → upstream
5. **Manual Testing**: System tray and Web UI verification

### Rollback Plan

If issues arise at any phase:
```bash
git revert HEAD  # Revert last commit
git push         # Deploy rollback
```

Each phase is a self-contained commit, enabling surgical rollbacks.

---

## Success Metrics

**Performance**:
- All API endpoints: <50ms response time
- Startup time: <5 seconds to HTTP ready
- No blocking during tool indexing

**Code Quality**:
- Zero race conditions (all tests pass with `-race`)
- Zero legacy code paths remaining
- Clean architecture with direct Actor integration

**Operational**:
- System tray works flawlessly
- Web UI loads in <1 second
- Server enable/disable instant response

---

## Next Steps

1. Review this plan
2. Start with Phase 7.1: Add tool caching to StateView
3. Execute one phase at a time with full testing
4. Commit after each phase completes successfully
