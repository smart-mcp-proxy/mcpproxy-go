package preflight

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/health"

// Status is the per-tool outcome. `ready` is a success status, not a failure
// reason — a ready result carries no reason, retryable or action field.
type Status = string

const (
	StatusReady       Status = "ready"
	StatusUnavailable Status = "unavailable"
)

// Reason is the closed v1 failure-reason enum (Spec 098 FR-003). Evolution is
// additive-only and consumers must treat an unknown code as non-retryable.
//
// `server_saturated` is RESERVED (spec 093 queue saturation) and deliberately
// not defined here: defining it would put it in AllReasons and therefore in the
// generated wire enum, promising a verdict nothing emits.
type Reason = string

const (
	ReasonServerInitializing  Reason = "server_initializing"
	ReasonServerUnhealthy     Reason = "server_unhealthy"
	ReasonServerDisabled      Reason = "server_disabled"
	ReasonServerQuarantined   Reason = "server_quarantined"
	ReasonToolPendingApproval Reason = "tool_pending_approval"
	ReasonToolChanged         Reason = "tool_changed"
	ReasonToolBlockedByUser   Reason = "tool_blocked_by_user"
	ReasonOAuthRequired       Reason = "oauth_required"
	ReasonHashMismatch        Reason = "hash_mismatch"
	ReasonServerNotInScope    Reason = "server_not_in_scope"
	ReasonToolDeniedByConfig  Reason = "tool_denied_by_config"
	ReasonMissingAnnotation   Reason = "missing_annotation"
	ReasonPolicyFiltered      Reason = "policy_filtered"
	ReasonNotFound            Reason = "not_found"
	ReasonServerNotConfigured Reason = "server_not_configured"
)

// Class groups reasons by what an operator must do about them. It drives the
// set verdict and therefore the CLI exit code.
type Class = string

const (
	// ClassRetryable: waiting may fix it (the proxy is mid-transition).
	ClassRetryable Class = "retryable"
	// ClassFixState: an operator action on live state fixes it (approve,
	// enable, log in, re-lock a pin).
	ClassFixState Class = "fix_state"
	// ClassPermanentConfig: nothing changes until configuration or the request
	// itself changes.
	ClassPermanentConfig Class = "permanent"
)

// Verdict is the set-level aggregate: the worst class present.
type Verdict = string

const (
	VerdictReady             Verdict = "ready"
	VerdictDegradedRetryable Verdict = "degraded_retryable"
	VerdictBlocked           Verdict = "blocked"
	VerdictUnknownIDs        Verdict = "unknown_ids"
)

// CLI exit codes (FR-009). Worst class present wins: 12 > 11 > 10 > 0.
const (
	ExitReady             = 0
	ExitDegradedRetryable = 10
	ExitBlocked           = 11
	ExitUnknownIDs        = 12
)

// reasonSpec is one row of the normative FR-003 table. Keeping the columns in
// one literal is what makes the table auditable against the spec by eye.
type reasonSpec struct {
	class     Class
	retryable bool
	// action uses the existing health-action vocabulary. "No action" is the
	// empty string, which serializers omit — matching the health constants
	// (health.ActionNone), not a literal "none".
	action      string
	verdict     Verdict
	exitCode    int
	remediation string
}

// reasonTable is the single source of truth for the FR-003 taxonomy.
var reasonTable = map[Reason]reasonSpec{
	ReasonServerInitializing: {
		class: ClassRetryable, retryable: true, action: health.ActionNone,
		verdict: VerdictDegradedRetryable, exitCode: ExitDegradedRetryable,
		remediation: "Server is still starting up; retry shortly.",
	},
	ReasonServerUnhealthy: {
		class: ClassRetryable, retryable: true, action: health.ActionViewLogs,
		verdict: VerdictDegradedRetryable, exitCode: ExitDegradedRetryable,
		remediation: "Check the server's logs (mcpproxy upstream logs <server>) and retry once it recovers.",
	},
	ReasonServerDisabled: {
		class: ClassFixState, retryable: false, action: health.ActionEnable,
		verdict: VerdictBlocked, exitCode: ExitBlocked,
		remediation: "Enable the server (mcpproxy upstream enable <server>).",
	},
	ReasonServerQuarantined: {
		class: ClassFixState, retryable: false, action: health.ActionApprove,
		verdict: VerdictBlocked, exitCode: ExitBlocked,
		remediation: "Review the quarantined server and approve it if it is trusted (Web UI, Quarantine).",
	},
	ReasonToolPendingApproval: {
		class: ClassFixState, retryable: false, action: health.ActionApprove,
		verdict: VerdictBlocked, exitCode: ExitBlocked,
		remediation: "Review and approve the tool (Web UI, Server detail -> Tools).",
	},
	ReasonToolChanged: {
		class: ClassFixState, retryable: false, action: health.ActionApprove,
		verdict: VerdictBlocked, exitCode: ExitBlocked,
		remediation: "The tool definition changed after approval; review the diff and re-approve it (Web UI, Server detail -> Tools).",
	},
	ReasonToolBlockedByUser: {
		class: ClassFixState, retryable: false, action: health.ActionEnable,
		verdict: VerdictBlocked, exitCode: ExitBlocked,
		remediation: "Re-enable the tool (Web UI, Server detail -> Tools).",
	},
	ReasonOAuthRequired: {
		class: ClassFixState, retryable: false, action: health.ActionLogin,
		verdict: VerdictBlocked, exitCode: ExitBlocked,
		remediation: "Log in to the server (mcpproxy upstream login <server>); waiting will not help.",
	},
	ReasonHashMismatch: {
		class: ClassFixState, retryable: false, action: health.ActionConfigure,
		verdict: VerdictBlocked, exitCode: ExitBlocked,
		remediation: "Review the tool's current definition and re-pin it with the current hash.",
	},
	ReasonServerNotInScope: {
		class: ClassPermanentConfig, retryable: false, action: health.ActionConfigure,
		verdict: VerdictBlocked, exitCode: ExitBlocked,
		remediation: "Add the server to the profile, or run the preflight without the profile.",
	},
	ReasonToolDeniedByConfig: {
		class: ClassPermanentConfig, retryable: false, action: health.ActionConfigure,
		verdict: VerdictBlocked, exitCode: ExitBlocked,
		remediation: "Operator policy (enabled_tools/disabled_tools) denies this tool; edit mcp_config.json to allow it.",
	},
	ReasonMissingAnnotation: {
		class: ClassPermanentConfig, retryable: false, action: health.ActionConfigure,
		verdict: VerdictBlocked, exitCode: ExitBlocked,
		remediation: "The upstream tool lacks the annotation this filter requires; publish annotations upstream or drop the filter.",
	},
	ReasonPolicyFiltered: {
		class: ClassPermanentConfig, retryable: false, action: health.ActionNone,
		verdict: VerdictBlocked, exitCode: ExitBlocked,
		remediation: "The tool is explicitly marked unsafe for this filter; drop the filter to use it.",
	},
	ReasonNotFound: {
		class: ClassPermanentConfig, retryable: false, action: health.ActionConfigure,
		verdict: VerdictUnknownIDs, exitCode: ExitUnknownIDs,
		remediation: "Check the tool id (format <server>:<tool>) against mcpproxy tools list.",
	},
	ReasonServerNotConfigured: {
		class: ClassPermanentConfig, retryable: false, action: health.ActionConfigure,
		verdict: VerdictUnknownIDs, exitCode: ExitUnknownIDs,
		remediation: "Add the server to mcp_config.json (mcpproxy upstream add), or fix the id.",
	},
}

// Precedence is the fixed FR-004 chain: for one ID, the first reason in this
// order that applies wins. It covers every enum member exactly once — the two
// annotation reasons sit in the final "annotation filters" slot, where
// missing_annotation and policy_filtered are mutually exclusive per owning
// filter and therefore unordered relative to each other in practice.
var Precedence = []Reason{
	ReasonServerNotConfigured,
	ReasonServerNotInScope,
	ReasonServerQuarantined,
	ReasonServerDisabled,
	ReasonNotFound,
	ReasonToolDeniedByConfig,
	ReasonToolBlockedByUser,
	ReasonToolChanged,
	ReasonToolPendingApproval,
	ReasonHashMismatch,
	ReasonOAuthRequired,
	ReasonServerUnhealthy,
	ReasonServerInitializing,
	ReasonMissingAnnotation,
	ReasonPolicyFiltered,
}

// AllReasons returns the enum in precedence order (a stable, meaningful order
// for docs and tests). The slice is a copy; callers may not mutate Precedence.
func AllReasons() []Reason {
	out := make([]Reason, len(Precedence))
	copy(out, Precedence)
	return out
}

// ValidReason reports whether code is a member of the closed v1 enum.
func ValidReason(code Reason) bool {
	_, ok := reasonTable[code]
	return ok
}

// ReasonClass returns the remediation class of a reason. Unknown codes are
// treated as permanent-config, matching the documented consumer rule ("treat
// unknown codes as non-retryable").
func ReasonClass(code Reason) Class {
	if spec, ok := reasonTable[code]; ok {
		return spec.class
	}
	return ClassPermanentConfig
}

// Retryable reports whether waiting can plausibly clear the reason. Unknown
// codes are non-retryable.
func Retryable(code Reason) bool {
	spec, ok := reasonTable[code]
	return ok && spec.retryable
}

// DefaultAction returns the health-vocabulary action for a reason, or "" when
// the reason has no action (the field is then omitted from the response).
// server_unhealthy's action is a best-effort default that the evaluator may
// override from spec 044 diagnostics.
func DefaultAction(code Reason) string {
	return reasonTable[code].action
}

// DefaultRemediation returns the one actionable instruction for a reason.
func DefaultRemediation(code Reason) string {
	return reasonTable[code].remediation
}

// ReasonVerdict maps one reason to the set verdict it forces on its own.
func ReasonVerdict(code Reason) Verdict {
	if spec, ok := reasonTable[code]; ok {
		return spec.verdict
	}
	// An unknown code must never silently downgrade the verdict: treat it as
	// blocked (non-retryable, operator action needed).
	return VerdictBlocked
}

// verdictRank orders verdicts from best to worst; the aggregate is the max.
var verdictRank = map[Verdict]int{
	VerdictReady:             0,
	VerdictDegradedRetryable: 1,
	VerdictBlocked:           2,
	VerdictUnknownIDs:        3,
}

// ExitCode maps a set verdict to the CLI exit code (FR-009).
func ExitCode(v Verdict) int {
	switch v {
	case VerdictUnknownIDs:
		return ExitUnknownIDs
	case VerdictBlocked:
		return ExitBlocked
	case VerdictDegradedRetryable:
		return ExitDegradedRetryable
	case VerdictReady:
		return ExitReady
	default:
		// Unknown verdicts must not look like success.
		return ExitBlocked
	}
}

// VerdictForReasons aggregates per-tool reasons into the set verdict: the worst
// class present. An empty list (every tool ready) is VerdictReady.
func VerdictForReasons(reasons []Reason) Verdict {
	worst := VerdictReady
	for _, r := range reasons {
		if v := ReasonVerdict(r); verdictRank[v] > verdictRank[worst] {
			worst = v
		}
	}
	return worst
}

// VerdictForResults is VerdictForReasons over evaluator results, ignoring ready
// entries.
func VerdictForResults(results []Result) Verdict {
	worst := VerdictReady
	for i := range results {
		if results[i].Status == StatusReady {
			continue
		}
		if v := ReasonVerdict(results[i].Reason); verdictRank[v] > verdictRank[worst] {
			worst = v
		}
	}
	return worst
}
