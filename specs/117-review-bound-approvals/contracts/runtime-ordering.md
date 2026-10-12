# Contract: Runtime Interfaces, Lock Order and Capture Ordering

## Runtime API (internal)

```go
// PR-2 (#1566)
func (r *Runtime) ApproveToolsReviewed(server string, names []string, expected map[string]string, by string) (int, error)
func (r *Runtime) ApproveAllToolsReviewed(server string, expected map[string]string, by string) (int, error)
// expected == nil → identical to ApproveTools / ApproveAllTools.

func (r *Runtime) CommitServerApproval(server string, blocks []string, expected map[string]string, by string, commit func() error) (promoted int, err error)
// One hold of the server's approval lock:
// validate(expected) → MarkToolBaselineDecided → commit() → promote pending∖blocks (∩ validated when bound) → storage quarantine=false
// Returns *toolreview.StaleToolReviewError on mismatch (nothing written).

func (r *Runtime) FinishServerApproval(server string) error
// QuarantineServer(false) minus the storage flip (already done) and minus approveBaselineToolsForServer:
// markQuarantineDecisionExplicit, SaveConfiguration, LoadConfiguredServers, events, activity, HandleUpstreamServerChange.

// PR-1 (#1567)
func (m *storage.Manager) MarkToolBaselineDecided(server string, d toolreview.BaselineDecision) error
func (m *storage.Manager) ToolBaselineDecided(server string) (bool, error)

// PR-3 (#1565)
func (mc *managed.Client) ListToolsCaptured(ctx context.Context) ([]*config.ToolMetadata, managed.InventoryCapture, error)
func (mc *managed.Client) Retire()
func (mc *managed.Client) CaptureIsCurrent(c managed.InventoryCapture) bool // !retired && epoch == c.Epoch && mc == c.Client
func (m *upstream.Manager) DiscoverToolsReportCaptured(ctx context.Context, dueOnly bool) (tools []*config.ToolMetadata, listed []string, captures map[string]managed.InventoryCapture, err error)
func (m *upstream.Manager) WithCaptureCurrent(server string, c managed.InventoryCapture, fn func() error) (current bool, err error) // captureMu(R)+epochMu held across fn; false if replaced, retired or epoch moved
func (r *Runtime) applyInventory(ctx context.Context, server string, tools []*config.ToolMetadata, capture managed.InventoryCapture) (applied bool, err error)
```

Scanner (`internal/security/scanner/service.go`). The scanner may import only `internal/toolreview`, never `internal/storage` or `internal/runtime` (import cycle, design review r1 #1).

PR-1 introduces the commit capability without validation:

```go
type ServerApprovalCommitter interface { // implemented by serverUnquarantinerAdapter
    // PR-1: under the server's approval lock, mark the baseline (server_approval, by) and run commit.
    CommitServerApproval(server string, blocks []string, expected map[string]string, by string, commit func() error) (int, error)
}
```

In PR-1 `expected` is always nil and the method only marks and commits; promotion and the unquarantine stay where they are. PR-2 extends the same method (validate, promote, storage flip) and adds `FinishServerApproval`:

```go
type ReviewedServerApprover interface { // PR-2
    ServerApprovalCommitter
    FinishServerApproval(server string) error
}
func (s *Service) ApproveServerReviewed(ctx context.Context, server string, force bool, by string, blocks []string, expected map[string]string) error
```

`ApproveServer` / `ApproveServerWithBlocks` call `ApproveServerReviewed` with `expected = nil`, so every server approval takes the new single-section path (FR-012). A fake without the capability keeps today's path.

## Lock order (must never invert)

```
captureSnapMu (runtime, global RW)
  → toolApprovalLocks[server].mu (runtime, per server)
      → upstream.Manager.captureMu (R)
          → managed.Client.epochMu
      → storage.Manager.mu / bbolt tx
      → index manager locks
      → supervisor state lock (publish only; events are async)
```

The quarantined capture's current outer `WithCurrentClientOnEpoch` wrapper (`lifecycle.go:649`) violates this order (it takes `captureMu`/`epochMu` **before** `captureSnapMu` and the approval lock). PR-3 removes it: client validity is checked by `Manager.WithCaptureCurrent(capture, fn)` **inside** the approval-locked section, holding `captureMu` (R) and `epochMu` across the writes.

Rules:
- Inside a `WithCaptureCurrent` callback (`epochMu` held), no code may call `Manager.GetClient`, `Supervisor.DiscoveryGeneration`/`GetServerState` or anything else that takes `manager.mu`. Retirement takes `epochMu` with `captureMu` (W) held but `manager.mu` released (design review r8 #1).
- No code holding a per-server approval lock may take `captureSnapMu`.
- No code holding a per-server approval lock may call `SaveConfiguration`, `LoadConfiguredServers`, `HandleUpstreamServerChange`, `reindexAffectedProfiles` or anything that can synchronously reach `checkToolApprovals` (`tool_approval_lock.go:25-38`, unchanged).
- `CommitServerApproval` and the operator approvals take only the approval lock (no `captureSnapMu`).
- The supervisor publish must not call back into the runtime synchronously. Today it forwards through `supervisorEventForwarder` (`lifecycle.go:2596`); T305 asserts this with a test.

## Capture ordering rules (PR-3)

| Situation | Rule |
|---|---|
| operator reconciliation (approval/block reindex) with `snapshot.seq == definitions < applied` (after a lenient-empty observation) | allowed: re-apply the retained snapshot without moving `applied`/`published` (FR-033a, design review r8 #3) |
| `capture.seq < applied` | stale: no writes of any kind; caller logs at Debug and, for single-server discovery, does not re-list (a newer capture already applied) |
| `capture.seq >= applied` | apply; `applied = seq` |
| `capture.seq == 0` | apply only while `applied == 0` |
| client retired or epoch moved | discard (as stale); the existing Spec 105 re-list loop handles connection churn |
| server quarantined/disabled at lock time | record definitions for review only (`checkToolApprovals` + undecided prune), no index or StateView writes |
| publish | only if `seq >= applied && seq >= published`; then `published = seq` |
| `lastGoodTools` | replaced only by `seq >= stored.seq` |
| approval reindex / sweep fallback | re-apply `lastGoodTools` with its stored capture, only if `WithCaptureCurrent` holds; otherwise approval reindex re-lists (`RefreshServerTools`) and the sweep fallback skips |
