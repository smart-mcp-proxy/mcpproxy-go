# Phase 0: Baseline Audit

## Overview
This document captures the baseline state of the mcpproxy-go architecture before refactoring, documenting current dependencies and performance characteristics.

## Hot Path Analysis

### 1. Runtime Lifecycle (`internal/runtime/lifecycle.go`)

**Key Operations:**
- `LoadConfiguredServers` (line 154): Synchronizes server configurations from config to storage and upstream managers
- `backgroundConnections` (line 50): Periodic reconnection attempts with 60s interval
- `DiscoverAndIndexTools` (line 123): Tool discovery and search indexing
- `EnableServer` (line 420): Toggle server enabled state with persistence
- `ReloadConfiguration` (line 355): Full config reload from disk

**Performance Characteristics:**
- `LoadConfiguredServers`: Fast storage writes (~100-200ms), slow async connections (30s+)
- Connection retries: Hold read locks while calling into clients
- Tool indexing: Background operation, doesn't block main operations

**Current Dependencies:**
```
Runtime
├── StorageManager (BoltDB) - Server config persistence
├── IndexManager (Bleve) - Tool search indexing
├── UpstreamManager - MCP server connections
├── CacheManager - Response caching
└── Truncator - Token truncation
```

### 2. Upstream Manager (`internal/upstream/manager.go`)

**Key Operations:**
- `AddServer` (line 201): Adds and connects to new upstream server
- `ConnectAll` (line 487): Concurrent connection attempts across all servers
- `DiscoverTools` (line 291): Tool discovery from all connected servers
- `CallTool` (line 358): Proxies tool calls with read lock held
- `GetStats` (line 606): Collects connection statistics

**Lock Contention Points:**
- Line 376-377: `CallTool` holds read lock during entire tool execution
- Line 489-492: `ConnectAll` copies client map under read lock
- Line 299-313: `DiscoverTools` iterates clients under read lock

**Performance Characteristics:**
- Connection attempts: Parallel with goroutines but synchronous error handling
- Tool calls: Blocking under read lock
- Stats collection: Fast, cached tool counts with 2-minute TTL

**Current Dependencies:**
```
Manager
├── map[string]*managed.Client (sync.RWMutex protected)
├── Storage (BoltDB) - Server metadata and OAuth events
├── NotificationManager - State change notifications
└── SecretResolver - Credential management
```

### 3. HTTP API Layer (`internal/httpapi/server.go`)

**Key Endpoints:**
- `GET /api/v1/servers` (line 395): Merges storage + live upstream stats
- `POST /api/v1/servers/{id}/enable` (line 415): Async toggle with 5s timeout
- `GET /api/v1/tools` (line 664): Search tools via index
- `GET /events` (line 699): Server-Sent Events stream

**REST ↔ Runtime Dependencies:**
```
HTTP API
├── controller.GetAllServers()
│   └── storage.ListUpstreamServers() + upstreamManager.GetStats()
├── controller.EnableServer()
│   └── runtime.EnableServer() → storage + config save + reload
├── controller.SearchTools()
│   └── indexManager.Search()
└── controller.EventsChannel()
    └── runtime.EventsChannel() (buffered chan)
```

**Blocking Patterns:**
- Line 395-412: `/servers` endpoint blocks on storage AND upstream stats
- Line 516-532: Server enable/disable has 5s async timeout, then returns
- Line 699-808: SSE connections read from buffered status/events channels

### 4. Server Controller (`internal/server/server.go`)

**GetAllServers Implementation** (line 376):
```go
func (s *Server) GetAllServers() ([]map[string]interface{}, error) {
    // 1. Read from BoltDB storage
    servers, err := s.runtime.StorageManager().ListUpstreamServers()

    // 2. For each server, query upstream manager for live status
    for _, server := range servers {
        client, exists := s.runtime.UpstreamManager().GetClient(server.Name)
        if exists {
            connected = client.IsConnected()
            state = client.GetState().String()
            // ... more live queries
        }
    }

    return result, nil
}
```

**Problem:** Every API call to `/servers` performs:
1. BoltDB read (disk I/O)
2. Upstream manager lock acquisition
3. Per-server client status queries

## Current Coupling Issues

### Issue 1: Configuration Sync Couples Storage + Upstream + File I/O
**Location:** `runtime/lifecycle.go:154-308`

```
LoadConfiguredServers()
  ├── [SYNC] storage.SaveUpstreamServer() for each server (disk I/O)
  ├── [ASYNC] upstreamManager.AddServer() (network I/O, 30s timeout)
  └── [ASYNC] upstreamManager.RemoveServer() (cleanup)
```

**Impact:** Config changes require coordinated updates across multiple systems

### Issue 2: API Responses Block on Live Upstream Queries
**Location:** `server/server.go:376-450`

```
/api/v1/servers endpoint
  ├── storage.ListUpstreamServers() (BoltDB read)
  └── For each server:
      ├── upstreamManager.GetClient() (map lookup under lock)
      └── client.IsConnected() (state query)
```

**Impact:** REST API latency tied to storage performance and lock contention

### Issue 3: Upstream Manager Holds Locks During Network Calls
**Location:** `upstream/manager.go:358-484`

```
CallTool()
  ├── mu.RLock() (acquire read lock)
  ├── Find client in map
  ├── client.CallTool() (NETWORK I/O - can be slow!)
  └── mu.RUnlock()
```

**Impact:** Long-running tool calls block other operations needing read lock

### Issue 4: No Read Model for UI State
**Current State:** UI polls `/api/v1/servers` which always hits storage + upstream

**Desired State:** UI reads from in-memory snapshot updated by events

```
Current:
  Web UI → HTTP API → Storage + Upstream Manager (every request)

Desired:
  Web UI → HTTP API → StateView Snapshot (instant)
  Runtime Events → StateView.Update() (background)
```

## Benchmark Results (Baseline)

To be populated after running:
```bash
go test -bench=. -benchmem ./internal/runtime/ > docs/phase0-runtime-bench.txt
go test -bench=. -benchmem ./internal/upstream/ > docs/phase0-upstream-bench.txt
```

## Success Criteria for Refactoring

### Phase 1: Config Service
- ✅ Config reads don't block on disk I/O
- ✅ Config updates publish to subscribers via channel
- ✅ Runtime consumes config snapshots, not raw file reads

### Phase 2: Supervisor
- ✅ Server lifecycle managed by supervisor goroutine
- ✅ Upstream operations don't block config operations
- ✅ Events published to runtime bus

### Phase 3: Server Actors
- ✅ Each server has dedicated goroutine with state machine
- ✅ Connection retries don't hold shared locks
- ✅ State transitions emit events

### Phase 4: Read Model
- ✅ `/api/v1/servers` responds in <10ms (no storage/upstream calls)
- ✅ StateView updated from event stream
- ✅ UI SSE receives real-time updates without polling

## Files to Monitor During Refactoring

**Will be modified:**
- `internal/runtime/runtime.go` - Add config service dependency
- `internal/runtime/lifecycle.go` - Move to supervisor/actors
- `internal/upstream/manager.go` - Wrap behind supervisor adapter
- `internal/httpapi/server.go` - Use StateView instead of live queries
- `internal/server/server.go` - Use StateView for GetAllServers

**Will be created:**
- `internal/runtime/configsvc/` - Config service package
- `internal/runtime/supervisor/` - Supervisor + actor system
- `internal/runtime/stateview/` - Read model for UI/API

**Must remain compatible:**
- All tests in `internal/runtime/*_test.go`
- All tests in `internal/upstream/*_test.go`
- E2E tests in `internal/server/*_test.go`

## Next Steps

1. ✅ Create benchmark suite for hot paths
2. ✅ Document REST ↔ runtime dependencies
3. ⏳ Run baseline benchmarks and record results
4. ⏳ Run all tests to ensure current state passes
5. → Begin Phase 1: Config service extraction
