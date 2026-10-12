package runtime

import (
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 117 PR-2 (#1566): approvals bound to the reviewed definitions.
//
// A client that shows a review sends back, per tool, the review fingerprint
// it displayed (expected_hashes, from GET /servers/{id}/review current_hash).
// The check runs on records read under the server's tool-approval lock, in
// the same critical section as the writes it guards, so a capture that lands
// between the client's review read and the approval is always seen: the
// approval is refused with *toolreview.StaleToolReviewError (HTTP 409 "out of
// date") and nothing is written.
//
// expected == nil is an UNBOUND (legacy) request; a non-nil map, empty
// included, is bound. Names in expected outside the validated set are ignored
// (FR-009).

// reviewBindingMismatch compares candidates with expected. A candidate whose
// name is absent from expected, or whose expected value is empty, is
// unreviewed; one whose fingerprint differs is changed.
func reviewBindingMismatch(candidates []*storage.ToolApprovalRecord, expected map[string]string) (changed, unreviewed []string) {
	for _, rec := range candidates {
		want, ok := expected[rec.ToolName]
		switch {
		case !ok || want == "":
			unreviewed = append(unreviewed, rec.ToolName)
		case want != reviewFingerprint(rec):
			changed = append(changed, rec.ToolName)
		}
	}
	return changed, unreviewed
}

// staleReviewError logs and returns the out-of-date error for a refused
// bound approval, or nil when nothing is stale.
func (r *Runtime) staleReviewError(serverName string, changed, unreviewed, missingBlocks []string) error {
	stale := toolreview.NewStaleToolReviewError(serverName, changed, unreviewed, missingBlocks)
	if stale == nil {
		return nil
	}
	r.logger.Info("Review-bound approval refused: the review is out of date; nothing was approved",
		zap.String("server", serverName),
		zap.Strings("changed", stale.Changed),
		zap.Strings("unreviewed", stale.Unreviewed),
		zap.Strings("missing_blocks", stale.MissingBlocks))
	return stale
}

// ApproveToolsReviewed approves the named tools when every requested tool
// that has a record still matches its reviewed fingerprint (FR-006). A
// requested name without a record is skipped (the caller reports it as not
// approved). A bound request validates, writes and counts each name once;
// expected == nil is exactly ApproveTools (duplicates included).
func (r *Runtime) ApproveToolsReviewed(serverName string, toolNames []string, expected map[string]string, approvedBy string) (int, error) {
	return r.ApproveToolsReviewedAt(serverName, toolNames, expected, approvedBy, 0)
}

// ApproveToolsReviewedAt is ApproveToolsReviewed bound to expectedIncarnation,
// the server identity the client reviewed (Spec 116 FR-014; 0 binds to the
// incarnation current when the call starts). The two bindings are independent
// and both enforced under the approval lock: a replaced server answers
// ErrServerReplaced (server_replaced), changed definitions answer
// *toolreview.StaleToolReviewError (review_out_of_date); the incarnation is
// checked first because a replaced server's records are not the reviewed ones.
func (r *Runtime) ApproveToolsReviewedAt(serverName string, toolNames []string, expected map[string]string, approvedBy string, expectedIncarnation int64) (int, error) {
	if expected == nil {
		return r.approveToolsAndNotify(serverName, toolNames, approvedBy, false, expectedIncarnation)
	}
	if r.storageManager == nil {
		return 0, nil
	}
	inc, err := r.CaptureApprovalIncarnation(serverName, expectedIncarnation)
	if err != nil {
		return 0, err
	}
	names := make([]string, 0, len(toolNames))
	seen := make(map[string]struct{}, len(toolNames))
	for _, name := range toolNames {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	unlock := r.lockToolApprovals(serverName)
	if err := r.checkApprovalIncarnationHeld(serverName, inc); err != nil {
		unlock()
		return 0, err
	}
	candidates := make([]*storage.ToolApprovalRecord, 0, len(names))
	for _, name := range names {
		rec, err := r.storageManager.GetToolApproval(serverName, name)
		if err != nil {
			if errors.Is(err, storage.ErrToolApprovalNotFound) {
				continue
			}
			unlock()
			return 0, fmt.Errorf("read tool approval %s:%s: %w", serverName, name, err)
		}
		candidates = append(candidates, rec)
	}
	changed, unreviewed := reviewBindingMismatch(candidates, expected)
	if err := r.staleReviewError(serverName, changed, unreviewed, nil); err != nil {
		unlock()
		return 0, err
	}
	if h := r.testHooks.reviewBindingValidated; h != nil {
		h(serverName)
	}
	approved, err := r.approveToolsLocked(serverName, names, approvedBy, false)
	unlock()
	r.notifyToolsApproved(serverName, approved, approvedBy)
	return approved, err
}

// ApproveAllToolsReviewed approves every pending and changed tool when each of
// them still matches its reviewed fingerprint (FR-006). expected == nil is
// exactly ApproveAllTools.
func (r *Runtime) ApproveAllToolsReviewed(serverName string, expected map[string]string, approvedBy string) (int, error) {
	return r.ApproveAllToolsReviewedAt(serverName, expected, approvedBy, 0)
}

// ApproveAllToolsReviewedAt is ApproveAllToolsReviewed bound to
// expectedIncarnation (see ApproveToolsReviewedAt).
func (r *Runtime) ApproveAllToolsReviewedAt(serverName string, expected map[string]string, approvedBy string, expectedIncarnation int64) (int, error) {
	if expected == nil {
		return r.ApproveAllToolsAt(serverName, approvedBy, expectedIncarnation)
	}
	if r.storageManager == nil {
		return 0, nil
	}
	inc, err := r.CaptureApprovalIncarnation(serverName, expectedIncarnation)
	if err != nil {
		return 0, err
	}
	unlock := r.lockToolApprovals(serverName)
	if err := r.checkApprovalIncarnationHeld(serverName, inc); err != nil {
		unlock()
		return 0, err
	}
	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		unlock()
		return 0, err
	}
	var candidates []*storage.ToolApprovalRecord
	var names []string
	for _, rec := range records {
		if rec.Status == storage.ToolApprovalStatusPending || rec.Status == storage.ToolApprovalStatusChanged {
			candidates = append(candidates, rec)
			names = append(names, rec.ToolName)
		}
	}
	changed, unreviewed := reviewBindingMismatch(candidates, expected)
	if err := r.staleReviewError(serverName, changed, unreviewed, nil); err != nil {
		unlock()
		return 0, err
	}
	if h := r.testHooks.reviewBindingValidated; h != nil {
		h(serverName)
	}
	approved := 0
	if len(names) > 0 {
		approved, err = r.approveToolsLocked(serverName, names, approvedBy, false)
	}
	unlock()
	r.notifyToolsApproved(serverName, approved, approvedBy)
	return approved, err
}

// serverApprovalReachable reports whether a server approval makes rec
// callable: a pending record (promoted) or an enabled approved record
// (activated by leaving quarantine). Changed records are never promoted by a
// server approval (A3) and disabled ones stay hidden.
func serverApprovalReachable(rec *storage.ToolApprovalRecord) bool {
	switch rec.Status {
	case storage.ToolApprovalStatusPending:
		return true
	case storage.ToolApprovalStatusApproved:
		return !rec.Disabled
	default:
		return false
	}
}

// serverApprovalBindingError validates a bound server approval against the
// records read under the lock: every record in the reachable set minus the
// block list must match its reviewed fingerprint, and every block target must
// still have a record (A4: a block of a changed tool is fine, a vanished
// target is out of date).
func (r *Runtime) serverApprovalBindingError(serverName string, records []*storage.ToolApprovalRecord, blocks []string, expected map[string]string) error {
	byName := make(map[string]struct{}, len(records))
	for _, rec := range records {
		byName[rec.ToolName] = struct{}{}
	}
	blocked := make(map[string]struct{}, len(blocks))
	var missing []string
	for _, name := range blocks {
		blocked[name] = struct{}{}
		if _, ok := byName[name]; !ok {
			missing = append(missing, name)
		}
	}
	var candidates []*storage.ToolApprovalRecord
	for _, rec := range records {
		if _, isBlocked := blocked[rec.ToolName]; isBlocked || !serverApprovalReachable(rec) {
			continue
		}
		candidates = append(candidates, rec)
	}
	changed, unreviewed := reviewBindingMismatch(candidates, expected)
	return r.staleReviewError(serverName, changed, unreviewed, missing)
}

// serverApprovalPromotedBy is the ApprovedBy of tools a server approval
// promotes from pending (unchanged from the pre-PR-2 unquarantine baseline).
const serverApprovalPromotedBy = "system:server-approval-baseline"

// CommitServerApproval is a security (server) approval's critical section
// (Spec 117 FR-011/FR-012/FR-023). It runs as one config commit that clears
// the server's quarantine (Spec 116: one storage transaction, the config file
// and the live config together), and the commit's approval-lock hook does,
// under the same hold of the server's tool-approval lock that then covers the
// storage flip:
//
//  1. validate the binding (bound requests only): nothing is written on a
//     mismatch, the server stays quarantined;
//  2. record the server's tool baseline decision (source server_approval, by);
//  3. commit(): the scanner's integrity baseline (+ blocks) save;
//  4. promote the pending records present now, never one filed after this
//     section (no promote-all after the unquarantine);
//
// and the commit then writes quarantined=false to storage before it releases
// the lock. Events, the activity record and the reindex follow after the
// lock (notifyToolsApproved here, FinishServerApproval for the rest).
//
// It returns the number of tools promoted. A failure after the hook (config
// file or storage write) leaves the hook's writes in place while the server
// stays quarantined: those are fail-safe (a quarantined server exposes no
// tool, and a retry validates the same fingerprints, which ignore status).
func (r *Runtime) CommitServerApproval(serverName string, blocks []string, expected map[string]string, by string, commit func() error) (int, error) {
	return r.commitServerApproval(serverName, 0, false, blocks, expected, by, commit)
}

// CommitServerApprovalAt is CommitServerApproval bound to a server
// incarnation (Spec 116 FR-014): under the approval lock, before the
// fingerprints are validated or the marker is written, the stored row must
// still carry inc, else ErrServerReplaced and nothing is recorded or
// committed (the server stays quarantined). The removal commit purges the
// marker under the same lock.
func (r *Runtime) CommitServerApprovalAt(serverName string, inc int64, blocks []string, expected map[string]string, by string, commit func() error) (int, error) {
	return r.commitServerApproval(serverName, inc, true, blocks, expected, by, commit)
}

func (r *Runtime) commitServerApproval(serverName string, inc int64, bound bool, blocks []string, expected map[string]string, by string, commit func() error) (int, error) {
	if by == "" {
		by = "unknown"
	}
	promoted := 0
	_, err := r.Commit("security_approve", func(tx *CommitTx) error {
		tx.EventExtra(map[string]any{"server": serverName, "quarantined": false})
		if err := tx.SetQuarantined(serverName, false); err != nil {
			// A bound approval of a server removed since it was reviewed is a
			// replaced server (409 server_replaced), the same answer the
			// under-lock incarnation check gives; never a 404.
			var nf *ServerNotFoundError
			if bound && inc != 0 && r.approvalBindingEnforced() && errors.As(err, &nf) {
				return serverReplacedError(serverName)
			}
			return err
		}
		return tx.UnderApprovalLock(serverName, func() error {
			if bound {
				if err := r.checkApprovalIncarnationHeld(serverName, inc); err != nil {
					return err
				}
			}
			n, err := r.commitServerApprovalHeld(serverName, blocks, expected, by, commit)
			promoted = n
			return err
		})
	})
	if err != nil {
		var stale *toolreview.StaleToolReviewError
		if errors.As(err, &stale) {
			return 0, stale
		}
		return 0, err
	}
	r.notifyToolsApproved(serverName, promoted, serverApprovalPromotedBy)
	if promoted > 0 {
		r.logger.Info("Server approval promoted the reviewed pending tools",
			zap.String("server", serverName), zap.Int("count", promoted), zap.Bool("bound", expected != nil))
	}
	return promoted, nil
}

// commitServerApprovalHeld is CommitServerApproval's locked part. Caller holds
// serverName's tool-approval lock.
func (r *Runtime) commitServerApprovalHeld(serverName string, blocks []string, expected map[string]string, by string, commit func() error) (int, error) {
	if r.storageManager == nil {
		if commit == nil {
			return 0, nil
		}
		return 0, commit()
	}
	records, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		return 0, fmt.Errorf("read tool approvals for %s: %w", serverName, err)
	}
	var validated map[string]struct{}
	if expected != nil {
		if err := r.serverApprovalBindingError(serverName, records, blocks, expected); err != nil {
			return 0, err
		}
		validated = make(map[string]struct{}, len(records))
		for _, rec := range records {
			if rec.Status == storage.ToolApprovalStatusPending {
				validated[rec.ToolName] = struct{}{}
			}
		}
	}
	if err := r.writeToolBaselineMarker(serverName, toolreview.BaselineDecision{
		Source: toolreview.BaselineSourceServerApproval,
		By:     by,
	}); err != nil {
		return 0, fmt.Errorf("record tool baseline decision for %s: %w", serverName, err)
	}
	if commit != nil {
		if err := commit(); err != nil {
			return 0, err
		}
	}
	// Re-list: the commit turned the block targets into approved+disabled
	// records. Every pending record left is one the binding validated (the
	// lock excludes every other writer), and for an unbound request one
	// present at this commit.
	after, err := r.storageManager.ListToolApprovals(serverName)
	if err != nil {
		return 0, fmt.Errorf("read tool approvals for %s after the approval commit: %w", serverName, err)
	}
	var pending []string
	for _, rec := range after {
		if rec.Status != storage.ToolApprovalStatusPending {
			continue
		}
		if validated != nil {
			if _, ok := validated[rec.ToolName]; !ok {
				continue
			}
		}
		pending = append(pending, rec.ToolName)
	}
	if len(pending) == 0 {
		return 0, nil
	}
	return r.approveToolsLocked(serverName, pending, serverApprovalPromotedBy, true)
}

// FinishServerApproval runs the part of a security (server) approval that
// follows CommitServerApproval's critical section, without the approval lock:
// the activity record and the upstream/index refresh. Unlike
// QuarantineServer(false) it promotes nothing, so a tool filed after the
// commit stays pending (FR-012).
func (r *Runtime) FinishServerApproval(serverName string) error {
	r.EmitActivityQuarantineChange(serverName, false, "Server unquarantined by administrator")
	r.HandleUpstreamServerChange(r.AppContext())
	r.logger.Info("Server approval finished; tools listed later wait for review",
		zap.String("server", serverName))
	return nil
}
