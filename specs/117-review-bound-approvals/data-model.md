# Data Model: Spec 117

## 1. Review fingerprint (derived, PR-2)

Computed from a `storage.ToolApprovalRecord` (`internal/storage/models.go:300`). Never stored.

```go
// internal/runtime/review_fingerprint.go
const reviewFingerprintVersion = "rv1"

func reviewFingerprint(rec *storage.ToolApprovalRecord) string {
    base := rec.CurrentHash
    if base == "" { // toggle-synthesized record (newToggleSynthesizedRecord)
        sum := sha256.Sum256([]byte(rec.CurrentDescription + "\x00" + rec.CurrentSchema + "\x00" + rec.CurrentOutputSchema))
        base = "nohash:" + hex.EncodeToString(sum[:])
    }
    h := sha256.New()
    write(h, reviewFingerprintVersion, rec.ToolName, base, hintString(rec.CurrentAnnotations)) // \x00-separated
    return reviewFingerprintVersion + "-" + hex.EncodeToString(h.Sum(nil))
}

// hintString: "ro=<v>;de=<v>;id=<v>;ow=<v>" with v ∈ {"-" (nil), "0", "1"}; nil annotations → all "-".
```

| Input | In the fingerprint | Why |
|---|---|---|
| tool name (raw, Spec 105) | yes | identity |
| `CurrentHash` (description, input schema, output schema) | yes | the approval-hashed contract |
| `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint` | yes | they set the tier and the default selection (G3) |
| `title`, status, `disabled`, timestamps, held evidence | no | not part of what is approved; status changes come with hash changes anyway |

Golden vectors are pinned in `internal/runtime/testdata/review_fingerprint_vectors.json`, so the value is byte-stable across releases. A change of format is a new version prefix.

## 2. `ReviewTool.current_hash` (wire, PR-2)

```go
// internal/runtime/review.go (ReviewTool)
// CurrentHash is the review fingerprint of this tool (not the storage
// CurrentHash): send it back in expected_hashes to bind an approval to
// this review. Opaque; compare only with a later review.
CurrentHash string `json:"current_hash,omitempty"`
```

`frontend/src/types/api.ts` `ReviewTool.current_hash?: string`. The CLI `reviewToolState` gains `CurrentHash string \`json:"current_hash"\``.

## 3. Approve request bodies (wire, PR-2)

```go
type securityApproveRequest struct {
    Force          bool              `json:"force"`
    Block          []string          `json:"block,omitempty"`
    ExpectedHashes map[string]string `json:"expected_hashes,omitempty"` // nil = unbound; {} = bound to the empty snapshot
}

// handleApproveTools
var req struct {
    Tools          []string          `json:"tools"`
    ApproveAll     bool              `json:"approve_all"`
    ExpectedHashes map[string]string `json:"expected_hashes,omitempty"`
}
```

`encoding/json` decodes a missing key and `null` to a nil map, and `{}` to an empty non-nil map, which gives exactly the bound/unbound split.

## 4. `StaleToolReviewError` (PR-2)

```go
// internal/storage/tool_review_errors.go
type StaleToolReviewError struct {
    Server        string
    Changed       []string // fingerprint differs from the reviewed one
    Unreviewed    []string // in the validated set, absent from expected_hashes (or empty value)
    MissingBlocks []string // block targets that no longer have a record (bound requests only)
}
func (e *StaleToolReviewError) Error() string // contains "out of date"; never "dangerous"
```

All lists are sorted and deduplicated. HTTP mapping (both handlers): 409, `error_code: "review_out_of_date"`, `stale_tools: {changed, unreviewed, missing_blocks}`, all lists always present (possibly empty).

## 5. `ToolBaselineDecision` (stored, PR-1)

Bucket `tool_baseline_decisions` (`internal/storage/models.go` constant `ToolBaselineDecisionsBucket`), created by `CreateBucketIfNotExists` on first write.

| Key | Value (JSON) |
|---|---|
| server name (UTF-8) | `{"decided_at":"<RFC3339Nano UTC>","source":"discovery\|server_approval\|inferred","by":"<approver or auto-baseline>"}` |

The value is decoded leniently: an unreadable value counts as "decided", which fails closed.

```go
func (m *Manager) MarkToolBaselineDecided(server string, d ToolBaselineDecision) error // idempotent; keeps the first decided_at, updates source only from inferred → explicit
func (m *Manager) ToolBaselineDecided(server string) (bool, error)
// DeleteServerToolApprovals / PruneToolApprovalsNotIn delete the marker in the same tx.
```

A read error in `checkToolApprovals` counts as "has baseline" (fail closed: new tools are held). This mirrors `serverQuarantinedInStorage`.

## 6. `InventoryCapture` (in memory, PR-3)

```go
// internal/upstream/managed/capture.go
type InventoryCapture struct {
    Seq    uint64  // process-global, strictly increasing; 0 = unordered (no managed client)
    Epoch  int64   // connection epoch at list start (listEpoch)
    Client *Client // the client that ran the list
}
var captureSeq atomic.Uint64
func nextCaptureSeq() uint64 { return captureSeq.Add(1) }
```

## 7. Per-server lock state (in memory, PR-3)

`toolApprovalLocks` (`internal/runtime/runtime.go:234`) changes from `*sync.Mutex` to:

```go
type toolApprovalServerLock struct {
    mu        sync.Mutex
    applied   uint64 // highest capture seq applied to records+index (guarded by mu)
    published uint64 // highest capture seq published to the StateView (guarded by mu)
}
```

`lockToolApprovals` keeps its signature. A new `lockToolApprovalsForCapture(server, seq) (unlock func(), stale bool)` is added.

`lastGoodTools` (`runtime.go`) becomes `map[string]lastGoodSnapshot{tools []*config.ToolMetadata; seq uint64}`. The `lastGoodToolsSnapshot` accessor returns both.

## 8. State transitions affected

| Transition | Before | After |
|---|---|---|
| pending → approved by server approval | after unquarantine, for every pending record at that time | inside the commit section, only for records present (and, if bound, validated) at commit |
| pending → approved by baseline pass | when no approved/changed record exists | when no approved/changed record exists AND no marker |
| any → (deleted) by absent-tool removal | from any pass | only from a pass whose capture is current (`seq >= applied`) |
| quarantined → unquarantined (security approval) | separate step after the commit | storage flip inside the commit section; config/events after |
