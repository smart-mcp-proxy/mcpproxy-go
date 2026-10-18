package runtime

import (
	"errors"
	"fmt"
	"sort"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 117 FR-018 (#1610): a capture whose writes did not all land is
// incomplete, and its server is review-dirty until a complete capture with a
// newer sequence is applied.
//
// A discovery pass admits its capture (admitInventoryHeld) and then writes
// approval records, removes absent tools, evicts blocked ones, writes the
// index, stores the last-good snapshot and publishes the StateView, all in one
// hold of the server's approval lock. When any of those steps fails the
// stored records, the index and the StateView may describe different
// definitions: an approved tool can keep its read-only hints in the StateView
// while the upstream already serves it as destructive. So the server is
// marked dirty at the capture's sequence, under the same lock, and:
//
//   - dispatch of every tool of the server is refused with the
//     ErrReviewNotRecorded text (Runtime.ToolReviewDirty, read lock-free);
//   - nothing is approved or enabled for it (reviewNotRecordedHeld in every
//     transition that makes a tool reachable); disabling and blocking stay
//     available;
//   - the retained last-good snapshot is not re-applied over the partly
//     written records: the approval reindex re-lists instead.
//
// The flag lives in memory only. After a restart the StateView is rebuilt
// from the connect capture, which records the live definition before any
// tool of the server is callable again.

// errCaptureIncomplete reports that a capture's writes did not all land; the
// server was marked review-dirty at the capture's sequence.
var errCaptureIncomplete = errors.New("tool capture incomplete: not every write of the pass succeeded")

// beginApprovalPassHeld resets serverName's per-pass write-failure counter.
// Caller holds the server's approval lock.
func (r *Runtime) beginApprovalPassHeld(serverName string) {
	st := r.toolApprovalServerState(serverName)
	st.writeFailures.Store(0)
	st.failedMu.Lock()
	st.passFailed = nil
	st.failedMu.Unlock()
}

// noteApprovalWriteFailure counts a failed approval-record write of
// toolName in the pass that holds serverName's approval lock.
func (r *Runtime) noteApprovalWriteFailure(serverName, toolName string) {
	r.toolApprovalServerState(serverName).writeFailures.Add(1)
	r.noteFailedTool(serverName, toolName)
}

// noteFailedTool records that a write for toolName failed in the current
// pass; a bound approval while dirty lists these as changed (FR-018).
func (r *Runtime) noteFailedTool(serverName, toolName string) {
	st := r.toolApprovalServerState(serverName)
	st.failedMu.Lock()
	if st.passFailed == nil {
		st.passFailed = make(map[string]struct{})
	}
	st.passFailed[toolName] = struct{}{}
	st.failedMu.Unlock()
}

// approvalPassWriteFailures reports how many record writes of the current
// pass failed. Caller holds the server's approval lock.
func (r *Runtime) approvalPassWriteFailures(serverName string) int64 {
	return r.toolApprovalServerState(serverName).writeFailures.Load()
}

// markReviewDirtyHeld marks serverName review-dirty at seq (or moves an
// existing mark up to it). Caller holds the server's approval lock.
func (r *Runtime) markReviewDirtyHeld(serverName string, seq uint64, cause error) {
	st := r.toolApprovalServerState(serverName)
	if !st.dirty || seq > st.dirtyAt {
		st.dirtyAt = seq
	}
	st.dirty = true
	st.failedMu.Lock()
	for name := range st.passFailed {
		if st.dirtyTools == nil {
			st.dirtyTools = make(map[string]struct{})
		}
		st.dirtyTools[name] = struct{}{}
	}
	st.passFailed = nil
	st.failedMu.Unlock()
	st.dirtyMirror.Store(true)
	r.logger.Warn("Tool capture incomplete: tool calls for the server are refused until a complete capture is applied",
		zap.String("server", serverName), zap.Uint64("capture_seq", seq), zap.Error(cause))
}

// clearReviewDirtyHeld clears serverName's dirty mark when the complete
// capture seq is newer than the one that set it. seq 0 (an unordered capture,
// fixtures without a managed client) clears only a mark set at seq 0. Caller
// holds the server's approval lock. It reports whether a mark was cleared.
func (r *Runtime) clearReviewDirtyHeld(serverName string, seq uint64) bool {
	v, ok := r.toolApprovalLocks.Load(serverName)
	if !ok {
		return false
	}
	st := v.(*toolApprovalServerLock)
	if !st.dirty {
		return false
	}
	if (seq != 0 && seq <= st.dirtyAt) || (seq == 0 && st.dirtyAt != 0) {
		return false
	}
	st.dirty = false
	st.dirtyAt = 0
	st.dirtyTools = nil
	st.dirtyMirror.Store(false)
	r.logger.Info("Tool capture complete: tool calls for the server are allowed again",
		zap.String("server", serverName), zap.Uint64("capture_seq", seq))
	return true
}

// dropReviewDirtyHeld forgets serverName's dirty mark when the server is
// removed (its records are purged with it). Caller holds the approval lock.
func (r *Runtime) dropReviewDirtyHeld(serverName string) {
	if v, ok := r.toolApprovalLocks.Load(serverName); ok {
		st := v.(*toolApprovalServerLock)
		st.dirty = false
		st.dirtyAt = 0
		st.dirtyTools = nil
		st.dirtyMirror.Store(false)
	}
}

// reviewDirtyHeld reports serverName's dirty mark. Caller holds the lock.
func (r *Runtime) reviewDirtyHeld(serverName string) bool {
	v, ok := r.toolApprovalLocks.Load(serverName)
	return ok && v.(*toolApprovalServerLock).dirty
}

// ToolReviewDirty reports whether serverName's latest capture could not be
// fully recorded (FR-018). It takes no lock, so dispatch never waits behind a
// capture; the mirror is written under the approval lock before the failing
// capture releases it.
func (r *Runtime) ToolReviewDirty(serverName string) bool {
	if r == nil {
		return false
	}
	v, ok := r.toolApprovalLocks.Load(serverName)
	return ok && v.(*toolApprovalServerLock).dirtyMirror.Load()
}

// reviewNotRecordedHeld refuses a transition that would make a tool of
// serverName reachable while the server is review-dirty. Caller holds the
// server's approval lock.
func (r *Runtime) reviewNotRecordedHeld(serverName string) error {
	if !r.reviewDirtyHeld(serverName) {
		return nil
	}
	return fmt.Errorf("server '%s': %w", serverName, toolreview.ErrReviewNotRecorded)
}

// reviewBindingDirtyHeld is reviewNotRecordedHeld for a review-bound request
// (FR-018): while dirty it answers the 409 out-of-date error listing the
// tools whose writes failed as changed, so the client reloads its review.
// A failure that names no tool (an index or publish failure) answers
// ErrReviewNotRecorded. Caller holds the server's approval lock.
func (r *Runtime) reviewBindingDirtyHeld(serverName string) error {
	if !r.reviewDirtyHeld(serverName) {
		return nil
	}
	st := r.toolApprovalServerState(serverName)
	names := make([]string, 0, len(st.dirtyTools))
	for name := range st.dirtyTools {
		names = append(names, name)
	}
	sort.Strings(names)
	if stale := toolreview.NewStaleToolReviewError(serverName, names, nil, nil); stale != nil {
		return stale
	}
	return r.reviewNotRecordedHeld(serverName)
}
