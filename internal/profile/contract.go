package profile

// Enumerations shared by every Profiles v3 surface — REST, CLI, MCP, Web UI,
// macOS (FR-001, FR-032, FR-052: "one Go source of truth"). Every value
// below is the exact wire spelling used in JSON, CLI flags/output and the
// generated frontend/src/types/contracts.ts (cmd/generate-types) and Swift
// models, so no surface can drift from another. Values not yet consumed by
// any implemented code path (most of them: enforcement lands in 108-b..h)
// are still defined here now so later PRs reference this one file instead of
// inventing their own spelling.

// UnannotatedPolicy is a profile's handling of a tool whose effective
// annotations carry no tier hint (FR-001, FR-003). The string spellings
// match config.ProfileUnannotated* (internal/config/profiles.go) exactly;
// that package cannot import this one (see the PolicyEnforcementReady note
// there), so the two are kept in sync by convention and pinned by
// contract_test.go.
type UnannotatedPolicy string

const (
	UnannotatedDeny    UnannotatedPolicy = "deny"
	UnannotatedAsWrite UnannotatedPolicy = "as_write"
	UnannotatedAsRead  UnannotatedPolicy = "as_read"
)

// Reason is why the FR-010 profile decision excluded a tool. The zero value
// ("") means admitted — no exclusion reason.
type Reason string

const (
	ReasonNone               Reason = ""
	ReasonServerNotInProfile Reason = "server_not_in_profile"
	ReasonDeniedByRule       Reason = "denied_by_rule"
	ReasonUnannotatedHidden  Reason = "unannotated_hidden"
	ReasonAboveTierCap       Reason = "above_tier_cap"
)

// Source is where an effective profile resolution came from (FR-020,
// data-model.md §4 ProfileResolution.Source).
type Source string

const (
	SourcePin       Source = "pin"
	SourceBinding   Source = "binding"
	SourceURL       Source = "url"
	SourceSession   Source = "session"
	SourceAnonymous Source = "anonymous"
	SourceNone      Source = "none"
)

// BlockReason is the activity-record cause of a profile-driven call refusal
// (FR-013, FR-014, FR-016; data-model.md §5 ActivityRecord.BlockReason).
type BlockReason string

const (
	BlockReasonTier          BlockReason = "profile_tier"
	BlockReasonRule          BlockReason = "profile_rule"
	BlockReasonUnannotated   BlockReason = "profile_unannotated"
	BlockReasonCodeExecution BlockReason = "profile_code_execution"
	BlockReasonManagement    BlockReason = "profile_management"
)

// ExplainStep is one link of the access-explanation chain, in the canonical
// enforcement order every refusal, view-as row and explainer walks (FR-032,
// FR-035; data-model.md §7 AccessExplanation).
type ExplainStep string

const (
	StepCredential      ExplainStep = "credential"
	StepProfile         ExplainStep = "profile"
	StepServerInScope   ExplainStep = "server_in_scope"
	StepToolRule        ExplainStep = "tool_rule"
	StepTierCap         ExplainStep = "tier_cap"
	StepTokenPermission ExplainStep = "token_permission"
	StepGlobalGate      ExplainStep = "global_gate"
	StepServerState     ExplainStep = "server_state"
	StepToolApproval    ExplainStep = "tool_approval"
)

// explainStepOrder is the canonical enforcement order (data-model.md §7);
// exported via StepOrder for the explainer and any test that must walk it.
var explainStepOrder = []ExplainStep{
	StepCredential, StepProfile, StepServerInScope, StepToolRule, StepTierCap,
	StepTokenPermission, StepGlobalGate, StepServerState, StepToolApproval,
}

// StepOrder returns the canonical access-explanation step order
// (data-model.md §7), a defensive copy the caller may mutate freely.
func StepOrder() []ExplainStep {
	out := make([]ExplainStep, len(explainStepOrder))
	copy(out, explainStepOrder)
	return out
}

// FixAction names a remediation the access explainer offers alongside a
// failing step (FR-035; data-model.md §7 AccessExplanation.fixes[].action).
type FixAction string

const (
	FixAllowInProfile     FixAction = "allow_in_profile"
	FixClassifyInProfile  FixAction = "classify_in_profile"
	FixAddServerToProfile FixAction = "add_server_to_profile"
	FixMoveClient         FixAction = "move_client"
	FixEditToken          FixAction = "edit_token"
	FixEnableServer       FixAction = "enable_server"
	FixApproveTool        FixAction = "approve_tool"
	FixChangeSetting      FixAction = "change_setting"
	FixReconnectClient    FixAction = "reconnect_client"
)

// CredentialState reports what a client's connection carries (FR-025;
// data-model.md §7 ClientView.credential_state / connect.ClientStatus).
type CredentialState string

const (
	CredentialStateClient   CredentialState = "client"
	CredentialStateAdminKey CredentialState = "admin_key"
	CredentialStateNone     CredentialState = "none"
	CredentialStateRevoked  CredentialState = "revoked"
	CredentialStateExpired  CredentialState = "expired"
)

// Surface names the caller of a profile-mutating operation, recorded on
// `profile_change` activity (FR-030; the `X-MCPProxy-Surface` header).
type Surface string

const (
	SurfaceWeb   Surface = "web"
	SurfaceMacOS Surface = "macos"
	SurfaceCLI   Surface = "cli"
	SurfaceMCP   Surface = "mcp"
	SurfaceAPI   Surface = "api"
)

// WarningCode is a Clients-surface warning code (data-model.md §7
// ClientView.warnings[]; Spec 109-l maps these same names to Needs-attention
// kinds).
type WarningCode string

const (
	WarningAnonymousDeniedByBindingGuard WarningCode = "anonymous_denied_by_binding_guard"
	WarningClientHoldsAdminKey           WarningCode = "client_holds_admin_key"
	WarningClientCredentialExpiring      WarningCode = "client_credential_expiring"
	WarningClientRotationPending         WarningCode = "client_rotation_pending"
	WarningProfileMissing                WarningCode = "profile_missing"
	WarningClientTokenNameConflict       WarningCode = "client_token_name_conflict"
)
