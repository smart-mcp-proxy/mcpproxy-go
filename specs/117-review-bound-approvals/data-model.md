# Data Model: Spec 117

## 1. Review fingerprint (derived, PR-2)

Computed from a `storage.ToolApprovalRecord` (`internal/storage/models.go:300`). Never stored.

```go
// internal/runtime/review_fingerprint.go
const reviewFingerprintVersion = "rv2"

func reviewFingerprint(rec *storage.ToolApprovalRecord) string {
    h := sha256.New()
    // each part is written as an 8-byte big-endian length followed by its bytes
    for _, part := range []string{reviewFingerprintVersion, rec.ServerName, rec.ToolName, rec.CurrentHash,
        rec.CurrentDescription, normalizeJSON(rec.CurrentSchema), normalizeJSON(rec.CurrentOutputSchema),
        hintString(rec.CurrentAnnotations)} {
        writeLengthPrefixed(h, part)
    }
    return reviewFingerprintVersion + "-" + hex.EncodeToString(h.Sum(nil))
}

// hintString: "ro=<v>;de=<v>;id=<v>;ow=<v>" with v ∈ {"-" (nil), "0", "1"}; nil annotations → all "-".
```

Sol r2 P1: `rv1` hashed `base` (the storage `CurrentHash`, or a `nohash:` digest when empty) in place of the definition fields. The storage hash joins description and schemas with an unescaped `|` and leaves out an absent output schema, so `(D, I, O)` and `("D|I", O, none)` share one `CurrentHash` and shared one `rv1` fingerprint. `rv2` encodes every field on its own; the rug-pull hash is unchanged. A record with an empty `CurrentHash` still gets a non-empty, definition-sensitive fingerprint (FR-003).

| Input | In the fingerprint | Why |
|---|---|---|
| server name | yes | a binding for server A never matches server B, even with identical definitions (design review r6 #2) |
| tool name (raw, Spec 105) | yes | identity |
| `CurrentHash` | yes | the approval-hashed contract that an approval copies to `ApprovedHash` |
| description, input schema, output schema (each length-prefixed; schemas JSON-normalized) | yes | the definition on screen; field-separated so no two definitions collide (Sol r2 P1) |
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
// internal/toolreview/errors.go  (leaf package: stdlib imports only; see research D4)
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

The value type `toolreview.BaselineDecision{DecidedAt, Source, By}` lives in the leaf package `internal/toolreview`. The storage methods take and return it. The scanner never calls storage for it: it reaches the marker through the runtime adapter (research D7).

Bucket `tool_baseline_decisions` (`internal/storage/models.go` constant `ToolBaselineDecisionsBucket`), created by `CreateBucketIfNotExists` on first write.

| Key | Value (JSON) |
|---|---|
| server name (UTF-8) | `{"decided_at":"<RFC3339Nano UTC>","source":"discovery\|server_approval\|inferred","by":"<approver or auto-baseline>"}` |

The value is decoded leniently: an unreadable value counts as "decided", which fails closed.

```go
func (m *Manager) MarkToolBaselineDecided(server string, d toolreview.BaselineDecision) error // idempotent; keeps the first decided_at, updates source only from inferred → explicit
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
    definitions uint64 // seq of the newest applied NON-EMPTY capture (== retained snapshot); FR-033a
    reviewDirtyAt uint64 // PR-3 FR-018: seq F of a capture whose record writes failed; 0 = clean. Also set (or moved up) by a capture whose apply fails after its record writes and before its StateView publish. Cleared only by a COMPLETE capture with seq > F (non-empty fully recorded, indexed and published, or authoritative empty with removal done and published), in the publish step; a lenient-empty observation never clears it
    dirtyMirror atomic.Uint64 // copy of reviewDirtyAt, written only under mu, read lock-free by the dispatch gate (FR-018 dispatch refusal)
}
```

`applied` is advanced by every current capture, complete or not (the fence). `published`, `definitions`, the `lastGoodTools` snapshot and the capture stamp are **staged** during the apply and committed together, in the publish step, only after the StateView publication is accepted (no error and `published == true`); a failed or rejected publication discards them (FR-018, spec review r5 #2).

`lockToolApprovals` keeps its signature. A new `lockToolApprovalsForCapture(server, seq) (unlock func(), stale bool)` is added.

`lastGoodTools` (`runtime.go`) becomes `map[string]lastGoodSnapshot{tools []*config.ToolMetadata; capture managed.InventoryCapture}`. It keeps the **complete** capture identity (seq, epoch, client), not only the sequence (design review r1 #4). Re-applying the snapshot validates that identity with `WithCaptureCurrent`. A snapshot whose client was replaced or retired, or whose epoch moved (reconnect), is not re-applied: the approval reindex re-lists instead (`RefreshServerTools`, the existing fallback at `tool_quarantine.go:1990-1995`), and the sweep fallback skips the server, since its connect discovery lists it. The `lastGoodToolsSnapshot` accessor returns both.

## 8. State transitions affected

| Transition | Before | After |
|---|---|---|
| pending → approved by server approval | after unquarantine, for every pending record at that time | inside the commit section, only for records present (and, if bound, validated) at commit |
| pending → approved by baseline pass | when no approved/changed record exists | when no approved/changed record exists AND no marker |
| any → (deleted) by absent-tool removal | from any pass, only for names still in the index | only from a pass whose capture is current (`seq >= applied`); the workset also includes every absent `approved && !Disabled` record, so a record left by a partial removal is reconciled by the next eligible capture (FR-032a) |
| quarantined → unquarantined (security approval) | separate step after the commit | storage flip inside the commit section; config/events after |
