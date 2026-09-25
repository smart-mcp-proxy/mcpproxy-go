package contracts

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

// Tier is a tool's capability classification, computed once from its
// annotations by AnnotationTier and reused by every surface (Spec 109
// FR-028, X11): the Web Tools page, macOS Tools view, CLI `tools list --tier`
// and `GET /tools` / `GET /servers/{id}/tools`. No surface may compute its
// own — the review payload is the one place a fifth value, TierUnknown,
// appears (data-model.md §2-3: a captured-but-unannotated record is still
// TierUnannotated; TierUnknown is reserved for a record with no captured
// annotations at all, e.g. one predating this spec).
//
// NOTE (Spec 108 PR 108-a): this file is a leaf dependency of Spec 109-a
// (`contracts.AnnotationTier`, research D30) that Spec 108's
// `internal/profile.IntrinsicTier` calls through an exhaustive adapter so
// tier classification can never disagree across surfaces. 108-a merges
// after 109-a per plan.md; until 109-a's own PR (#1378) merges, this file
// is carried here verbatim (byte-identical) as the shared dependency, so
// merging 109-a afterwards is a no-op on this file.
type Tier string

const (
	TierRead        Tier = "read"
	TierWrite       Tier = "write"
	TierDestructive Tier = "destructive"
	TierUnannotated Tier = "unannotated"
	// TierUnknown is returned only by the review composer (internal/runtime
	// review.go), never by AnnotationTier — see the type doc above.
	TierUnknown Tier = "unknown"
)

// AnnotationTier computes a tool's tier from its MCP behavior-hint
// annotations. This is the ONE place the classification happens; Spec 108's
// IntrinsicTier calls this same function rather than reimplementing it
// (research D1).
//
// Priority (highest first):
//  1. destructiveHint == true            -> TierDestructive
//  2. readOnlyHint == false (explicit)   -> TierWrite
//  3. readOnlyHint == true               -> TierRead
//  4. nil annotations, or neither hint set -> TierUnannotated
//
// This is deliberately distinct from DeriveCallWith (Spec 018 call-variant
// routing), which defaults an unannotated tool to "read" for safety when
// picking which call_tool_* variant to use — AnnotationTier never guesses:
// an unannotated tool is TierUnannotated, not TierRead.
func AnnotationTier(a *config.ToolAnnotations) Tier {
	if a == nil {
		return TierUnannotated
	}
	if a.DestructiveHint != nil && *a.DestructiveHint {
		return TierDestructive
	}
	if a.ReadOnlyHint != nil {
		if !*a.ReadOnlyHint {
			return TierWrite
		}
		return TierRead
	}
	return TierUnannotated
}
