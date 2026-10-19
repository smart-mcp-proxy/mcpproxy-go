package preflight

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/qapilot"
)

// Quarantine/approval testing pilot — dispatch decision table (QP-14).
//
// Every combination of the classifier's inputs is checked against the
// policy model's own precedence (qapilot.ClassifyCell.Expected), and every
// cell is recorded in the ledger so the report can prove the whole table
// ran rather than a sample of it.
func TestQAPilot_ClassifyTable(t *testing.T) {
	for _, cell := range qapilot.ClassifyCells() {
		cell := cell
		t.Run(cell.ID, func(t *testing.T) {
			qapilot.Track(t, cell.ID, "QP-14")
			in := ClassifyInputs{
				Server: ServerPolicy{
					Found:                  cell.ServerFound,
					Enabled:                cell.Enabled,
					Quarantined:            cell.Quarantined,
					AutoApproveToolChanges: cell.TrustAuto,
				},
				QuarantineEnabled: cell.GlobalQuarantine,
				ConfigDenied:      cell.ConfigDenied,
				Discovered:        cell.Discovered,
			}
			if cell.HasRecord {
				in.Approval = &ApprovalState{Status: string(cell.Status), Disabled: cell.Disabled}
			}
			got := ClassifyTool(in)
			want := cell.Expected()
			if string(got) != string(want) {
				t.Fatalf("cell %s: policy says %q, implementation says %q", cell.ID, want, got)
			}
			if got.Callable() != (want == qapilot.DispatchReady) {
				t.Fatalf("cell %s: Callable()=%v disagrees with class %q", cell.ID, got.Callable(), got)
			}
		})
	}
}
