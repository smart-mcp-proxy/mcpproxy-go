package contracts

// Spec 109 FR-090 terminology enums. Each family has ONE Go source here; the
// generator (cmd/generate-types) emits the same values into
// frontend/src/types/contracts.ts, and the golden
// internal/contracts/testdata/terminology.json is decoded by Go, vitest and
// the Swift tests so no surface can drift (TestSpec109TerminologyGolden).
//
// Health statuses live in internal/health (health.StatusOrder) and attention
// kinds in internal/runtime (runtime.AttentionKinds): both packages import
// this one (directly or through storage), so they cannot be re-exported from
// here without an import cycle. The golden test reads all of them together.

// Tool review states (the `approval_status` wire value). The values equal
// storage.ToolApprovalStatus*; a test asserts it.
const (
	ToolApprovalApproved = "approved"
	ToolApprovalPending  = "pending"
	ToolApprovalChanged  = "changed"
)

// AllToolApprovalStates lists the tool review states in display order.
func AllToolApprovalStates() []string {
	return []string{ToolApprovalApproved, ToolApprovalPending, ToolApprovalChanged}
}

// Activity views (the Web/macOS `view` parameter; the CLI accepts every value
// except sessions, which has no CLI listing, parity row 19).
const (
	ActivityViewCalls    = "calls"
	ActivityViewSessions = "sessions"
	ActivityViewSystem   = "system"
	ActivityViewAll      = "all"
)

// AllActivityViews lists the Activity views in tab order.
func AllActivityViews() []string {
	return []string{ActivityViewCalls, ActivityViewSessions, ActivityViewSystem, ActivityViewAll}
}

// Client presence states (`GET /clients` row `state`).
const (
	ClientPresenceConnectedSeen      = "connected_seen"
	ClientPresenceConnectedNeverSeen = "connected_never_seen"
	ClientPresenceInstalled          = "installed"
	ClientPresenceNotInstalled       = "not_installed"
	ClientPresenceOther              = "other"
)

// AllClientPresenceStates lists the presence states in display order.
func AllClientPresenceStates() []string {
	return []string{
		ClientPresenceConnectedSeen,
		ClientPresenceConnectedNeverSeen,
		ClientPresenceInstalled,
		ClientPresenceNotInstalled,
		ClientPresenceOther,
	}
}

// AllTiers lists the tool tiers in display order.
func AllTiers() []Tier {
	return []Tier{TierRead, TierWrite, TierDestructive, TierUnannotated, TierUnknown}
}

// Hold-signal ids (spec 120): the deterministic structural-gate signals the
// scan-mode change gate records on a held tool (`held_signals`), ahead of the
// scanner's own check ids. Each is a prefix: the recorded signal carries a
// reason and a path after it (`GATE-SCHEMA-SHAPE:new-required:bcc`,
// `GATE-HINT-CHANGE:openWorldHint:true>false`), and the surfaces label by
// prefix. Not TPA signature ids on purpose (the Web UI lists them with the
// heuristic check ids).
const (
	HoldSignalHintSaferFlip = "GATE-HINT-SAFER-FLIP"
	HoldSignalHintChange    = "GATE-HINT-CHANGE"
	HoldSignalSchemaShape   = "GATE-SCHEMA-SHAPE"
	HoldSignalOutputSchema  = "GATE-OUTPUT-SCHEMA"
)

// AllHoldSignals lists the structural-gate signal prefixes in display order.
func AllHoldSignals() []string {
	return []string{HoldSignalHintSaferFlip, HoldSignalHintChange, HoldSignalSchemaShape, HoldSignalOutputSchema}
}
