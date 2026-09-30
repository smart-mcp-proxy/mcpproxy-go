package profile

import "errors"

// ErrUnknownProfile is returned when an access subject names a profile that
// does not exist.
var ErrUnknownProfile = errors.New("profile not found")

// ErrUnknownClient is returned when an access subject names a client that is
// neither a known client nor holds a credential record.
var ErrUnknownClient = errors.New("client not found")

// The access chain vocabulary shared by the view-as rows of GET /tools
// (Spec 108 FR-032) and the access explainer (FR-035, Spec 108-f). One Go
// source: the REST rows, the CLI columns, the MCP effective_tools operation
// and the generated Web/Swift types all spell these values the same way
// (FR-052).

// AccessReason is why an effective-tool row is not visible or not callable.
// The zero value ("") means callable. The FR-010 decision reasons are reported
// by the step that carries them (data-model.md §7 EffectiveTool); every other
// value is the failing step's own name.
type AccessReason string

const (
	AccessReasonNone AccessReason = ""
	// The four FR-010 decision reasons (Reason), reported by the step that
	// carries them: server_not_in_profile by server_in_scope, denied_by_rule
	// by tool_rule, unannotated_hidden and above_tier_cap by tier_cap.
	AccessReasonServerNotInProfile AccessReason = "server_not_in_profile"
	AccessReasonDeniedByRule       AccessReason = "denied_by_rule"
	AccessReasonUnannotatedHidden  AccessReason = "unannotated_hidden"
	AccessReasonAboveTierCap       AccessReason = "above_tier_cap"
	// The step names.
	AccessReasonCredential      AccessReason = "credential"
	AccessReasonProfile         AccessReason = "profile"
	AccessReasonServerInScope   AccessReason = "server_in_scope"
	AccessReasonTokenPermission AccessReason = "token_permission"
	AccessReasonGlobalGate      AccessReason = "global_gate"
	AccessReasonServerState     AccessReason = "server_state"
	AccessReasonToolApproval    AccessReason = "tool_approval"
)

// AccessReasons returns every non-empty AccessReason in data-model.md §7 order.
// It is the enumeration the contract test pins, the generated types are built
// from and every consumer switches over.
func AccessReasons() []AccessReason {
	return []AccessReason{
		AccessReasonServerNotInProfile,
		AccessReasonDeniedByRule,
		AccessReasonUnannotatedHidden,
		AccessReasonAboveTierCap,
		AccessReasonCredential,
		AccessReasonProfile,
		AccessReasonServerInScope,
		AccessReasonTokenPermission,
		AccessReasonGlobalGate,
		AccessReasonServerState,
		AccessReasonToolApproval,
	}
}

// AccessReasonFromDecision maps an FR-010 decision Reason onto AccessReason.
func AccessReasonFromDecision(r Reason) AccessReason {
	switch r {
	case ReasonServerNotInProfile:
		return AccessReasonServerNotInProfile
	case ReasonDeniedByRule:
		return AccessReasonDeniedByRule
	case ReasonUnannotatedHidden:
		return AccessReasonUnannotatedHidden
	case ReasonAboveTierCap:
		return AccessReasonAboveTierCap
	default:
		return AccessReasonNone
	}
}

// AccessReasonFromStep is the reason a failing step reports when it carries no
// FR-010 decision reason of its own: the step's name. tool_rule and tier_cap
// always report a decision reason instead (AccessReasonFromDecision).
func AccessReasonFromStep(s ExplainStep) AccessReason {
	switch s {
	case StepCredential:
		return AccessReasonCredential
	case StepProfile:
		return AccessReasonProfile
	case StepServerInScope:
		return AccessReasonServerInScope
	case StepTokenPermission:
		return AccessReasonTokenPermission
	case StepGlobalGate:
		return AccessReasonGlobalGate
	case StepServerState:
		return AccessReasonServerState
	case StepToolApproval:
		return AccessReasonToolApproval
	default:
		return AccessReasonNone
	}
}

// AccessSubjectKind selects whose access is evaluated.
type AccessSubjectKind string

const (
	// AccessSubjectClient is a client as its connection resolves today: its
	// credential (or lack of one) and binding.
	AccessSubjectClient AccessSubjectKind = "client"
	// AccessSubjectProfile is a profile's own reach, independent of any
	// credential: the credential and token_permission steps are skipped.
	AccessSubjectProfile AccessSubjectKind = "profile"
)

// AccessSubject names the subject of an access evaluation.
type AccessSubject struct {
	Kind AccessSubjectKind
	// ClientID names the client for AccessSubjectClient.
	ClientID string
	// CredentialState is what the client's connection actually carries
	// (client | admin_key | none | revoked | expired). Empty means "derive it
	// from the client's credential record".
	CredentialState CredentialState
	// Profile names the profile for AccessSubjectProfile.
	Profile string
}

// AccessStepStatus is the outcome of one step of the chain.
type AccessStepStatus string

const (
	AccessStepPass AccessStepStatus = "pass"
	AccessStepFail AccessStepStatus = "fail"
	// AccessStepSkip marks a step that does not apply to the subject (the
	// credential and token_permission steps of a profile subject).
	AccessStepSkip AccessStepStatus = "skip"
)

// AccessStep is one evaluated link of the chain, in StepOrder.
type AccessStep struct {
	Step   ExplainStep      `json:"step"`
	Status AccessStepStatus `json:"status"`
	Detail string           `json:"detail,omitempty"`
}

// AccessVerdict is the complete evaluation of one (server, tool) for one
// subject. Steps is complete (every step of StepOrder, in order), so the
// explainer (Spec 108-f) only renders it and adds fixes[].
type AccessVerdict struct {
	// Visible is true when credential, profile, server_in_scope, tool_rule and
	// tier_cap all pass: what the subject's discovery would list (FR-010/FR-011
	// plus scope).
	Visible bool
	// Callable is true when Visible and every later step passes: what a real
	// call would do (FR-032, SC-009).
	Callable bool
	// Reason is the first failing step's reason; empty when Callable.
	Reason AccessReason
	// ProfileTier is the tool's tier under the subject's profile (the intrinsic
	// tier when no profile applies).
	ProfileTier Tier
	Steps       []AccessStep
}

// FirstFailure returns the first failing step, or "" when none failed.
func (v AccessVerdict) FirstFailure() ExplainStep {
	for _, s := range v.Steps {
		if s.Status == AccessStepFail {
			return s.Step
		}
	}
	return ""
}
