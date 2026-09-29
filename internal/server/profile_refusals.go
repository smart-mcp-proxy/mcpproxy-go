package server

import (
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// profileToolPolicyRefusal renders the stable, non-disclosing refusal text
// for a tool excluded by a compiled profile policy.
func profileToolPolicyRefusal(reason profile.Reason, tier, cap profile.Tier, server, tool string) (string, profile.BlockReason) {
	switch reason {
	case profile.ReasonAboveTierCap:
		capText := "read and write"
		if cap == profile.TierRead {
			capText = "read"
		}
		return fmt.Sprintf("blocked by profile: %s:%s is a %s tool; this profile allows %s tools only", server, tool, tier, capText), profile.BlockReasonTier
	case profile.ReasonDeniedByRule:
		return fmt.Sprintf("blocked by profile: %s:%s is denied by a profile rule", server, tool), profile.BlockReasonRule
	case profile.ReasonUnannotatedHidden:
		return fmt.Sprintf("blocked by profile: %s:%s has no tier annotation; an operator can classify it in the profile to allow it", server, tool), profile.BlockReasonUnannotated
	default:
		return "blocked by profile: tool is not allowed by the active profile", profile.BlockReasonRule
	}
}
