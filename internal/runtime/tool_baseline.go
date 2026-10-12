package runtime

import (
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 117 PR-1 (#1567): durable server tool baseline.
//
// Whether a trusted server "has a baseline" decides whether a discovery pass
// may auto-approve the tools it sees (MCP-2931's first-baseline pass). It used
// to be inferred only from approved/changed records, which are deleted when
// the upstream replaces its inventory or returns an authoritative empty list;
// the next pass then auto-approved the replacement unreviewed. The marker in
// storage (bucket tool_baseline_decisions) records the decision itself:
//
//   - a trusted discovery pass that leaves an approved record writes it
//     (source "discovery"), after its loop;
//   - a pass that finds prior approved/changed records and no marker
//     back-fills it (source "inferred") BEFORE its first record write;
//   - every security (server) approval writes it (source "server_approval")
//     inside its locked commit, before the integrity baseline;
//   - only the removal of the server's approval records removes it (same
//     bbolt transaction).
//
// The record inference is kept (OR), so a store written by an older binary
// works without migration and a downgrade loses nothing.

// errBaselineMarker reports that the baseline step of a discovery pass could
// not read or write the durable marker (or list the server's prior records).
// The pass is aborted before it writes any record: no record, no absent-tool
// delete, no tool-hash delete, no index write, and the caller publishes
// nothing (FR-027, FR-027a). The next pass retries.
var errBaselineMarker = errors.New("tool baseline marker unavailable")

// toolBaseline is the baseline step's result for one pass.
type toolBaseline struct {
	// prior is the server's approval records as they stood before the pass,
	// keyed by raw tool name (also the Spec 105 legacy consults' only store).
	prior map[string]*storage.ToolApprovalRecord
	// hasBaseline: the marker exists, or a prior record is approved/changed.
	hasBaseline bool
}

func (r *Runtime) toolBaselineFault(serverName, op string) error {
	if f := r.testHooks.toolBaselineFault; f != nil {
		return f(serverName, op)
	}
	return nil
}

// resolveToolBaselineHeld lists the server's prior approval records and
// resolves whether its baseline was decided. When prior approved/changed
// records exist and no marker does (an older store), it writes the marker
// with source "inferred" right here, before the caller's first record write,
// so a marker failure can never leave this pass's record mutations behind
// (FR-027a). A listing error, a marker read error or that write's error
// returns errBaselineMarker: an unreadable baseline is never "no baseline".
// Caller holds serverName's approval lock.
func (r *Runtime) resolveToolBaselineHeld(serverName string) (*toolBaseline, error) {
	b := &toolBaseline{prior: make(map[string]*storage.ToolApprovalRecord)}
	if r.storageManager == nil {
		return b, nil
	}
	records, err := r.storageManager.ListToolApprovals(serverName)
	if ferr := r.toolBaselineFault(serverName, "list"); ferr != nil {
		err = ferr
	}
	if err != nil {
		return nil, r.baselineMarkerError(serverName, "list prior tool approvals", err)
	}
	inferred := false
	for _, rec := range records {
		b.prior[rec.ToolName] = rec
		if rec.Status == storage.ToolApprovalStatusApproved || rec.Status == storage.ToolApprovalStatusChanged {
			inferred = true
		}
	}
	marked, err := r.storageManager.ToolBaselineDecided(serverName)
	if ferr := r.toolBaselineFault(serverName, "read"); ferr != nil {
		err = ferr
	}
	if err != nil {
		return nil, r.baselineMarkerError(serverName, "read tool baseline marker", err)
	}
	if !marked && inferred {
		if err := r.writeToolBaselineMarker(serverName, toolreview.BaselineDecision{
			Source: toolreview.BaselineSourceInferred,
			By:     "auto-baseline",
		}); err != nil {
			return nil, r.baselineMarkerError(serverName, "back-fill tool baseline marker", err)
		}
	}
	b.hasBaseline = marked || inferred
	return b, nil
}

func (r *Runtime) baselineMarkerError(serverName, what string, err error) error {
	r.logger.Warn("Tool baseline step failed; discovery pass aborted before any write (retried by the next pass)",
		zap.String("server", serverName), zap.String("step", what), zap.Error(err))
	return fmt.Errorf("%w: %s for %s: %v", errBaselineMarker, what, serverName, err)
}

// writeToolBaselineMarker writes the marker (through the test fault seam) and
// logs the first write at Info.
func (r *Runtime) writeToolBaselineMarker(serverName string, d toolreview.BaselineDecision) error {
	if err := r.toolBaselineFault(serverName, "write"); err != nil {
		return err
	}
	if err := r.storageManager.MarkToolBaselineDecided(serverName, d); err != nil {
		return err
	}
	r.logger.Info("Tool baseline recorded for server; new or replaced tools now wait for review",
		zap.String("server", serverName), zap.String("source", d.Source), zap.String("by", d.By))
	return nil
}

// markDiscoveryToolBaselineHeld runs after the loop of a pass that started
// without a baseline: when the pass left at least one approved or changed
// record (a first trusted baseline, or a lifted gate approving everything) it
// records the decision with source "discovery". An empty or all-pending
// result records nothing (FR-025). A failure is NOT an abort: the only
// approved records are the ones this pass just wrote and they establish the
// baseline by inference, so the next pass writes the "inferred" marker before
// it can delete anything (FR-027a). Caller holds serverName's approval lock.
func (r *Runtime) markDiscoveryToolBaselineHeld(serverName string) {
	if r.storageManager == nil {
		return
	}
	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		r.logger.Warn("Cannot record the tool baseline after discovery; the next pass infers it from the approved records",
			zap.String("server", serverName), zap.Error(err))
		return
	}
	by := ""
	for _, rec := range records {
		if rec.Status == storage.ToolApprovalStatusApproved || rec.Status == storage.ToolApprovalStatusChanged {
			by = rec.ApprovedBy
			if by == "" {
				by = "auto-baseline"
			}
			break
		}
	}
	if by == "" {
		return
	}
	if err := r.writeToolBaselineMarker(serverName, toolreview.BaselineDecision{
		Source: toolreview.BaselineSourceDiscovery,
		By:     by,
	}); err != nil {
		r.logger.Warn("Cannot record the tool baseline after discovery; the next pass infers it from the approved records",
			zap.String("server", serverName), zap.Error(err))
	}
}

// CommitServerApproval runs a security (server) approval's storage commit
// under serverName's tool-approval lock, after recording the server's tool
// baseline decision (source "server_approval", by) — whether the review had
// tools or not, so an empty or blind review still holds every tool the server
// lists later (Spec 117 FR-023, A6). commit is the scanner's integrity
// baseline (+ blocks) save; it must not call back into a runtime method that
// takes the same lock.
//
// This is the PR-1 form of the Spec 117 commit section: blocks and expected
// are accepted for the PR-2 contract (review binding, promotion and the
// storage flip move in there) and expected must be nil here. The returned
// count of promoted tools is always 0 in PR-1: promotion still happens in the
// unquarantine that follows.
//
// A marker that cannot be written fails the approval before anything is
// committed. A commit that fails after the marker leaves the marker in place:
// that only holds later tools for review, which is the safe direction.
func (r *Runtime) CommitServerApproval(serverName string, blocks []string, expected map[string]string, by string, commit func() error) (int, error) {
	_ = blocks
	if expected != nil {
		return 0, fmt.Errorf("review-bound server approval is not supported by this core")
	}
	defer r.lockToolApprovals(serverName)()
	if r.storageManager != nil {
		if by == "" {
			by = "unknown"
		}
		if err := r.writeToolBaselineMarker(serverName, toolreview.BaselineDecision{
			Source: toolreview.BaselineSourceServerApproval,
			By:     by,
		}); err != nil {
			return 0, fmt.Errorf("record tool baseline decision for %s: %w", serverName, err)
		}
	}
	if commit == nil {
		return 0, nil
	}
	return 0, commit()
}
