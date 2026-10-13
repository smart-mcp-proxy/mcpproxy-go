package httpapi

import (
	"sort"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// maxHeldToolsReported caps the held tool names echoed in an approval
// response; the counts are always exact.
const maxHeldToolsReported = 50

// toolApprovalOutcome is the post-write state of a server's tool-approval
// records, read back after an approval so the response reports what actually
// applied instead of echoing the request (UX-02). The fields are additive to
// the existing response shapes.
type toolApprovalOutcome struct {
	// ApprovedCount counts approved records that are enabled (callable as far
	// as tool approval is concerned).
	ApprovedCount int `json:"approved_count"`
	// BlockedCount counts approved records the operator disabled (blocks).
	BlockedCount int `json:"blocked_count"`
	// StillPending / StillChanged count records still held for review.
	StillPending int `json:"still_pending"`
	StillChanged int `json:"still_changed"`
	// HeldTools names the held records (sorted, capped at 50).
	HeldTools []string `json:"held_tools,omitempty"`
}

func summarizeToolApprovals(records []*storage.ToolApprovalRecord) toolApprovalOutcome {
	var out toolApprovalOutcome
	var held []string
	for _, rec := range records {
		if rec == nil {
			continue
		}
		switch rec.Status {
		case storage.ToolApprovalStatusApproved:
			if rec.Disabled {
				out.BlockedCount++
			} else {
				out.ApprovedCount++
			}
		case storage.ToolApprovalStatusPending:
			out.StillPending++
			held = append(held, rec.ToolName)
		case storage.ToolApprovalStatusChanged:
			out.StillChanged++
			held = append(held, rec.ToolName)
		}
	}
	sort.Strings(held)
	if len(held) > maxHeldToolsReported {
		held = held[:maxHeldToolsReported]
	}
	out.HeldTools = held
	return out
}

// toolApprovalOutcomeFor reads the server's approval records back. ok is false
// when the records cannot be read; the caller then omits the outcome fields
// rather than reporting made-up counts.
func (s *Server) toolApprovalOutcomeFor(serverName string) (records []*storage.ToolApprovalRecord, outcome toolApprovalOutcome, ok bool) {
	if s.controller == nil {
		return nil, toolApprovalOutcome{}, false
	}
	records, err := s.controller.ListToolApprovals(serverName)
	if err != nil {
		s.logger.Warnw("Failed to read tool approvals for approval outcome", "server", serverName, "error", err)
		return nil, toolApprovalOutcome{}, false
	}
	return records, summarizeToolApprovals(records), true
}

// addTo merges the outcome into a response map.
func (o toolApprovalOutcome) addTo(m map[string]interface{}) {
	m["approved_count"] = o.ApprovedCount
	m["blocked_count"] = o.BlockedCount
	m["still_pending"] = o.StillPending
	m["still_changed"] = o.StillChanged
	if len(o.HeldTools) > 0 {
		m["held_tools"] = o.HeldTools
	}
}
