# Plan: Non-Blocking Upstream Stats Refresh

## Phase 1 – Stabilize Tool Count Reads
- **Goals**
  - Remove blocking `ListTools` calls from `/api/v1/servers` path.
  - Serve `GetUpstreamStats` from cached counts only.
- **Key Tasks**
  1. Add aggregation helpers that read tool counts from Supervisor StateView snapshots or cached managed-client totals without hitting upstream transports.
  2. Remove the synchronous `GetCachedToolCount` usage in `internal/upstream/manager.go` and replace it with non-blocking alternatives.
  3. Ensure the HTTP handler composes stats strictly from lock-free snapshots.
- **Verification Criteria**
  - `/api/v1/servers` returns within <100 ms while an upstream is deliberately stalled (simulate with mocked `ListTools` delay).
  - No goroutine in profiler stack traces waits on `ListTools` during stats requests.
- **Regression Tests**
  - `go test ./internal/server`
  - `go test ./internal/upstream`

## Phase 2 – Event-Driven Cache Updates
- **Goals**
  - Update tool count caches only via supervisor-triggered events, keeping cache accurate without polling.
  - Consolidate tool discovery responsibilities inside supervisor actors.
- **Key Tasks**
  1. Emit tool-count change events from managed clients or discovery routines into the supervisor.
  2. Extend StateView entries to capture tool count deltas and publish aggregated metrics.
  3. Add background watchers that refresh counts after enable/disable or connect/disconnect transitions.
- **Verification Criteria**
  - Tool counts reflect upstream changes within one reconciliation cycle after a mock tool discovery.
  - Supervisor log shows deterministic cache updates without duplicate ListTools executions.
- **Regression Tests**
  - `go test ./internal/runtime/supervisor`
  - Scenario test: `go test ./internal/runtime -run TestManagedToolDiscovery -v` (new or existing)
