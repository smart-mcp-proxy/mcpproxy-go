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
// Returns *storage.StaleToolReviewError on mismatch (nothing written).

func (r *Runtime) FinishServerApproval(server string) error
// QuarantineServer(false) minus the storage flip (already done) and minus approveBaselineToolsForServer:
// markQuarantineDecisionExplicit, SaveConfiguration, LoadConfiguredServers, events, activity, HandleUpstreamServerChange.

// PR-1 (#1567)
func (m *storage.Manager) MarkToolBaselineDecided(server string, d storage.ToolBaselineDecision) error
func (m *storage.Manager) ToolBaselineDecided(server string) (bool, error)

// PR-3 (#1565)
func (mc *managed.Client) ListToolsCaptured(ctx context.Context) ([]*config.ToolMetadata, managed.InventoryCapture, error)
func (mc *managed.Client) Retire()
func (mc *managed.Client) CaptureIsCurrent(c managed.InventoryCapture) bool // !retired && epoch == c.Epoch && mc == c.Client
func (m *upstream.Manager) DiscoverToolsReportCaptured(ctx context.Context, dueOnly bool) (tools []*config.ToolMetadata, listed []string, captures map[string]managed.InventoryCapture, err error)
func (r *Runtime) applyInventory(ctx context.Context, server string, tools []*config.ToolMetadata, capture managed.InventoryCapture) (applied bool, err error)
```

Scanner (`internal/security/scanner/service.go`), a new optional capability of the unquarantiner adapter:

```go
type ReviewedServerApprover interface {
    CommitServerApproval(server string, blocks []string, expected map[string]string, by string, commit func() error) (int, error)
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

Rules:
- No code holding a per-server approval lock may take `captureSnapMu`.
- No code holding a per-server approval lock may call `SaveConfiguration`, `LoadConfiguredServers`, `HandleUpstreamServerChange`, `reindexAffectedProfiles` or anything that can synchronously reach `checkToolApprovals` (`tool_approval_lock.go:25-38`, unchanged).
- `CommitServerApproval` and the operator approvals take only the approval lock (no `captureSnapMu`).
- The supervisor publish must not call back into the runtime synchronously. Today it forwards through `supervisorEventForwarder` (`lifecycle.go:2596`); T305 asserts this with a test.

## Capture ordering rules (PR-3)

| Situation | Rule |
|---|---|
| `capture.seq < applied` | stale: no writes of any kind; caller logs at Debug and, for single-server discovery, does not re-list (a newer capture already applied) |
| `capture.seq >= applied` | apply; `applied = seq` |
| `capture.seq == 0` | apply only while `applied == 0` |
| client retired or epoch moved | discard (as stale); the existing Spec 105 re-list loop handles connection churn |
| server quarantined/disabled at lock time | record definitions for review only (`checkToolApprovals` + undecided prune), no index or StateView writes |
| publish | only if `seq >= applied && seq >= published`; then `published = seq` |
| `lastGoodTools` | replaced only by `seq >= stored.seq` |
| approval reindex / sweep fallback | re-apply `lastGoodTools` with its stored seq (`== applied` normally) |
