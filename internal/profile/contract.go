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
	// CredentialStateUnknown means the client's config was not read: the
	// stat-only GET /connect listing (Spec 075 keeps that path free of
	// content reads) reports it for connected rows. The on-demand
	// GET /connect/{client} resolves the real state.
	CredentialStateUnknown CredentialState = "unknown"
)

// Error codes carried in the `code` field of a Profiles v3 refusal body
// (contracts/refusals.md).
const (
	ErrorCodeBindingBypassable  = "binding_bypassable_without_auth"
	ErrorCodeNoClientCredential = "no_client_credential"
	// ErrorCodeConnectInProgress answers a second connect, rotate, finalize or
	// binding change of a client whose connect holds the in-flight claim
	// (FR-021a).
	ErrorCodeConnectInProgress = "connect_in_progress"
	// ErrorCodeCredentialSuperseded answers a connect whose credential was
	// replaced or revoked before it could be finalized (FR-021a).
	ErrorCodeCredentialSuperseded = "credential_superseded"

	// Spec 108-f: the profile CRUD refusals (contracts/rest-api.md).
	ErrorCodeProfileInUse       = "profile_in_use"
	ErrorCodeProfileIsAnonymous = "profile_is_anonymous_profile"
	ErrorCodeProfileExists      = "profile_exists"
	ErrorCodeNameMismatch       = "name_mismatch"
	// ErrorCodePreconditionFailed answers an upgrade-admin-key-holders apply
	// whose precondition_token no longer matches what the preview showed.
	// (Connect's own 409 carries the same meaning under `action`.)
	ErrorCodePreconditionFailed = "precondition_failed"
)

// Why a classify entry no longer applies (EffectiveToolsResult.
// StaleClassificationReasons, FR-005).
const (
	// StaleClassificationAnnotated: the tool now carries its own annotations,
	// which a classify entry never overrides.
	StaleClassificationAnnotated = "annotated"
	// StaleClassificationMissing: the tool no longer exists.
	StaleClassificationMissing = "missing"
)

// WarningSeverity grades a Clients-surface warning (data-model.md §7).
type WarningSeverity string

const (
	WarningSeverityWarn WarningSeverity = "warn"
	WarningSeverityInfo WarningSeverity = "info"
)

// WarningActionUpgradeAdminKeyHolders is the `action.kind` of the
// client_holds_admin_key warning: a one-click "upgrade every client that holds
// the admin key" (FR-025). The other action kinds reuse FixAction spellings
// (change_setting, reconnect_client, move_client, edit_token).
const WarningActionUpgradeAdminKeyHolders = "upgrade_admin_key_holders"

// ExplainVerdict is the overall answer of an access explanation (FR-035):
// allowed = callable; hidden = not visible; blocked = visible but not callable.
type ExplainVerdict string

const (
	ExplainVerdictAllowed ExplainVerdict = "allowed"
	ExplainVerdictBlocked ExplainVerdict = "blocked"
	ExplainVerdictHidden  ExplainVerdict = "hidden"
)

// GuardFix kinds offered by the FR-008a binding-guard refusal `fixes[]`.
const (
	GuardFixRequireMCPAuth      = "require_mcp_auth"
	GuardFixSetAnonymousProfile = "set_anonymous_profile"
)

// ChangeKind is the `change` of a `profile_change` activity record
// (data-model.md §5). The clients service writes assign/lock/unlock/forget/
// rotate; the profiles service writes the rest.
type ChangeKind string

const (
	ChangeCreate    ChangeKind = "create"
	ChangeUpdate    ChangeKind = "update"
	ChangeDelete    ChangeKind = "delete"
	ChangeRename    ChangeKind = "rename"
	ChangeClassify  ChangeKind = "classify"
	ChangeAssign    ChangeKind = "assign"
	ChangeLock      ChangeKind = "lock"
	ChangeUnlock    ChangeKind = "unlock"
	ChangeForget    ChangeKind = "forget"
	ChangeRotate    ChangeKind = "rotate"
	ChangeAnonymous ChangeKind = "anonymous"
)

// Staged-rotation states (FR-021a): the `diff.outcome` of a `rotate` record
// and a client row's `rotation.state`.
const (
	RotationFinalized  = "finalized"
	RotationRolledBack = "rolled_back"
	RotationPending    = "pending"
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
