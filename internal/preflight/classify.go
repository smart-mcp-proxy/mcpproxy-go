package preflight

// Tool-approval status values, mirrored from internal/storage
// (ToolApprovalStatus*). They are duplicated rather than imported so this
// package stays a leaf with no storage dependency; a unit test asserts the
// mirror is exact, so the duplication cannot drift.
const (
	ApprovalStatusApproved = "approved"
	ApprovalStatusPending  = "pending"
	ApprovalStatusChanged  = "changed"
)

// ServerPolicy is the config-derived (non-runtime) view of one upstream server.
// Found=false means no server with that name is configured at all.
type ServerPolicy struct {
	Found       bool
	Enabled     bool
	Quarantined bool
	// AutoApproveToolChanges mirrors ServerConfig.IsAutoApproveToolChanges()
	// (equivalently IsQuarantineSkipped() / trust_mode auto): the server opted
	// out of tool-level quarantine, so pending/changed approval records do NOT
	// gate its tools. This is the divergence research D2 records: dispatch
	// honors it, the old classifyServerToolStatus did not.
	AutoApproveToolChanges bool
}

// ApprovalState is the narrow read of a spec 032 ToolApprovalRecord. A nil
// *ApprovalState means "no record", which is the implicit-approved default —
// NOT an error and NOT a pending state.
type ApprovalState struct {
	Status            string
	Disabled          bool
	CurrentHash       string
	HashSchemaVersion uint64
}

// ClassifyInputs are the locally-read facts the shared classifier needs. Every
// field is a plain value: the classifier performs no I/O, so it is callable
// from dispatch (latency-sensitive) and from the evaluator alike.
type ClassifyInputs struct {
	Server ServerPolicy
	// QuarantineEnabled is the GLOBAL quarantine switch (config.IsQuarantineEnabled()).
	QuarantineEnabled bool
	// ConfigDenied is the operator's enabled_tools/disabled_tools verdict.
	ConfigDenied bool
	// Approval is the tool's approval record, or nil when none exists.
	Approval *ApprovalState
}

// ToolClass is the shared classification consumed by the preflight evaluator,
// classifyServerToolStatus and describeGateReason (research D2). Exactly one
// class per tool, by fixed first-match precedence.
type ToolClass string

const (
	ToolClassReady               ToolClass = "ready"
	ToolClassServerNotConfigured ToolClass = "server_not_configured"
	ToolClassServerQuarantined   ToolClass = "server_quarantined"
	ToolClassServerDisabled      ToolClass = "server_disabled"
	ToolClassDeniedByConfig      ToolClass = "denied_by_config"
	ToolClassBlockedByUser       ToolClass = "blocked_by_user"
	ToolClassChanged             ToolClass = "changed"
	ToolClassPendingApproval     ToolClass = "pending_approval"
)

// ClassifyTool is the single tool-eligibility classification (Spec 098 FR-002,
// research D2). Dispatch behavior is ground truth, so three divergences of the
// legacy classifiers are resolved here in dispatch's favor:
//
//   - the tool-level quarantine gate applies ONLY when the global quarantine
//     switch is on AND the server has not opted out (trust_mode auto /
//     auto_approve_tool_changes). classifyServerToolStatus used to check the
//     approval status unconditionally, which reported false positives for
//     auto-approving servers;
//   - `changed` (rug-pull guard) is a class of its own, not collapsed into
//     `pending_approval` — the two have different stories and different
//     remediation text;
//   - a user block (ToolApprovalRecord.Disabled) applies unconditionally, even
//     for auto-approving servers: it is a user decision, not a quarantine gate.
//
// Precedence: server not configured → quarantined → disabled → denied by config
// → blocked by user → changed → pending → ready. Existence (index presence) is
// NOT part of this classification; the evaluator interleaves it at its FR-004
// slot, between server_disabled and tool_denied_by_config.
func ClassifyTool(in ClassifyInputs) ToolClass {
	if !in.Server.Found {
		return ToolClassServerNotConfigured
	}
	if in.Server.Quarantined {
		return ToolClassServerQuarantined
	}
	if !in.Server.Enabled {
		return ToolClassServerDisabled
	}
	if in.ConfigDenied {
		return ToolClassDeniedByConfig
	}
	if in.Approval == nil {
		return ToolClassReady
	}
	if in.Approval.Disabled {
		return ToolClassBlockedByUser
	}
	if !quarantineGateApplies(in) {
		return ToolClassReady
	}
	switch in.Approval.Status {
	case ApprovalStatusChanged:
		return ToolClassChanged
	case ApprovalStatusPending:
		return ToolClassPendingApproval
	default:
		return ToolClassReady
	}
}

// quarantineGateApplies reports whether pending/changed approval records gate
// this server's tools — the exact condition the dispatch path and
// describeGateReason use.
func quarantineGateApplies(in ClassifyInputs) bool {
	return in.QuarantineEnabled && !in.Server.AutoApproveToolChanges
}

// Reason maps a class to its preflight reason code. ToolClassReady maps to the
// empty string (no failure).
func (c ToolClass) Reason() Reason {
	switch c {
	case ToolClassServerNotConfigured:
		return ReasonServerNotConfigured
	case ToolClassServerQuarantined:
		return ReasonServerQuarantined
	case ToolClassServerDisabled:
		return ReasonServerDisabled
	case ToolClassDeniedByConfig:
		return ReasonToolDeniedByConfig
	case ToolClassBlockedByUser:
		return ReasonToolBlockedByUser
	case ToolClassChanged:
		return ReasonToolChanged
	case ToolClassPendingApproval:
		return ReasonToolPendingApproval
	case ToolClassReady:
		return ""
	default:
		return ""
	}
}

// Callable reports whether the class permits dispatch. It is the one-line form
// the dispatch paths consume (FR-002).
func (c ToolClass) Callable() bool {
	return c == ToolClassReady
}
