package runtime

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// RefreshHeldScanEvidence re-runs the in-process TPA scan over the stored
// current definition of every pending or changed tool record of one server and
// refreshes only the held-scan evidence (HeldVerdict, HeldSignals), so a newly
// activated bundle's verdict is visible on tools that are already held for
// review (Spec 101 FR-019 leg (i), tasks B17). It takes the tool-approval lock
// and works for quarantined servers too.
//
// It is evidence only: Status, ApprovedHash, ApprovedBy and the server's
// quarantine flag are never written, and nothing is approved or unquarantined
// (RT-02). A signature removed by a newer bundle leaves the tool held, with
// verdict clean and reason awaiting_review. Approved records are not this
// leg's business: the cached informational scan covers them.
//
// Unlike the discovery pass, which rescans a pending record only when its
// definition moved or it has no verdict yet, this rescans every held record: a
// stored verdict was computed under the previous corpus.
func (r *Runtime) RefreshHeldScanEvidence(server string) error {
	if r == nil || r.storageManager == nil || server == "" {
		return nil
	}
	defer r.lockToolApprovals(server)()

	records, err := r.storageManager.ListToolApprovals(server)
	if err != nil {
		return fmt.Errorf("list tool approvals for %s: %w", server, err)
	}
	for _, rec := range records {
		if rec == nil || rec.ToolName == "" {
			continue
		}
		if rec.Status != storage.ToolApprovalStatusPending && rec.Status != storage.ToolApprovalStatusChanged {
			continue
		}
		// A record with no stored definition has nothing to scan; scanning an
		// empty tool would write a misleading "clean".
		if rec.CurrentDescription == "" && rec.CurrentSchema == "" && rec.CurrentOutputSchema == "" {
			continue
		}
		wasUnstamped := !rec.IdentityKeyed
		tool := &config.ToolMetadata{
			ServerName:       server,
			Name:             rec.ToolName,
			RawName:          rec.ToolName,
			Description:      rec.CurrentDescription,
			ParamsJSON:       rec.CurrentSchema,
			OutputSchemaJSON: rec.CurrentOutputSchema,
			Annotations:      rec.CurrentAnnotations,
		}
		// scanToolEvidence, not scanChangeIsClean: this is a re-evaluation of
		// evidence, not a gate invocation, so it must not move the gate counter.
		_, evidence := r.scanToolEvidence(server, tool)
		r.recordScanHold(server, rec.ToolName, rec, wasUnstamped, evidence)
	}
	r.logger.Debug("Refreshed held scan evidence after a TPA bundle activation",
		zap.String("server", server), zap.Int("records", len(records)))
	return nil
}
