package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// The wire types of the Profiles v3 REST surface (Spec 108-f). REST, the CLI
// (-o json), the MCP `profiles` tool, the Web UI and the macOS app all read
// these shapes; the field names here are the contract (contracts/rest-api.md).

// ToolCounts is the number of a profile's VISIBLE tools by the tier the profile
// gives them, plus the unannotated tools it hides (FR-034).
type ToolCounts struct {
	Read              int `json:"read"`
	Write             int `json:"write"`
	Destructive       int `json:"destructive"`
	UnannotatedHidden int `json:"unannotated_hidden"`
}

// UsedByClient names one client credential bound to a profile.
type UsedByClient struct {
	ID   string `json:"id"`
	Mode string `json:"mode"`
}

// UsedBy lists what points at a profile: non-revoked client credentials, non-
// revoked regular tokens pinned to it, and whether it is the anonymous_profile.
// It discloses other credentials' bindings, so REST shows it to administrators
// only (FR-032).
type UsedBy struct {
	Clients          []UsedByClient `json:"clients"`
	Tokens           []string       `json:"tokens"`
	AnonymousProfile bool           `json:"anonymous_profile"`
}

// tokenNames lists the token-store names of every referencing credential.
func (u UsedBy) tokenNames() []string {
	out := append([]string{}, u.Tokens...)
	for _, c := range u.Clients {
		out = append(out, "client-"+c.ID)
	}
	return out
}

// Empty reports whether nothing points at the profile through a client or a
// token (the anonymous_profile flag is its own refusal).
func (u UsedBy) Empty() bool { return len(u.Clients) == 0 && len(u.Tokens) == 0 }

// ProfileView is one profile as REST returns it: the config fields as stored,
// the derived effective values, tool counts, 24 h stats and (administrators
// only) used_by.
type ProfileView struct {
	Name        string                   `json:"name"`
	Title       string                   `json:"title,omitempty"`
	Description string                   `json:"description,omitempty"`
	Servers     []string                 `json:"servers"`
	MaxTier     string                   `json:"max_tier,omitempty"`
	Unannotated string                   `json:"unannotated,omitempty"`
	Tools       *config.ProfileToolRules `json:"tools,omitempty"`

	CodeExecution   *bool     `json:"code_execution,omitempty"`
	ManagementTools *bool     `json:"management_tools,omitempty"`
	SwitchableTo    *[]string `json:"switchable_to,omitempty"`

	EffectiveServers       []string `json:"effective_servers"`
	EffectiveUnannotated   string   `json:"effective_unannotated"`
	EffectiveCodeExecution bool     `json:"effective_code_execution"`
	IsLegacy               bool     `json:"is_legacy"`

	ToolCounts ToolCounts `json:"tool_counts"`
	// ToolCount is the v2 field: indexed tools on the effective servers.
	// Deprecated: use tool_counts.
	ToolCount  int     `json:"tool_count"`
	Calls24h   int     `json:"calls_24h"`
	Blocked24h int     `json:"blocked_24h"`
	UsedBy     *UsedBy `json:"used_by,omitempty"`
}

// ProfileList is GET /profiles: the views and, for administrators, the
// anonymous_profile.
type ProfileList struct {
	Profiles         []ProfileView `json:"profiles"`
	AnonymousProfile string        `json:"anonymous_profile,omitempty"`
}

// MovedRefs names what a rename or a delete-with-reassign moved.
type MovedRefs struct {
	Clients []string `json:"clients"`
	Tokens  []string `json:"tokens"`
}

// RenameResult is the outcome of ProfilesService.Rename.
type RenameResult struct {
	Profile ProfileView `json:"profile"`
	Moved   MovedRefs   `json:"moved"`
}

// DeleteResult is the outcome of ProfilesService.Delete.
type DeleteResult struct {
	Deleted                 string    `json:"deleted"`
	Moved                   MovedRefs `json:"moved"`
	AnonymousProfileMovedTo string    `json:"anonymous_profile_moved_to,omitempty"`

	movedTokenNames []string
}

// WriteResult is the outcome of a create or update: the stored profile and the
// validator's warnings about it.
type WriteResult struct {
	Profile  ProfileView `json:"profile"`
	Warnings []string    `json:"warnings"`
}

// ViewerScope says how much of the world a profile read may reveal. The zero
// value is an administrator.
type ViewerScope struct {
	// Restricted marks a non-administrator viewer.
	Restricted bool
	// Visible reports whether the viewer may learn a server exists. nil for an
	// administrator.
	Visible func(server string) bool
	// AllowedServers is the viewer's server grant (nil = unrestricted); it
	// bounds the 24 h stats.
	AllowedServers []string
}

// --- effective tools -------------------------------------------------------

// EffectiveToolsOptions selects the rows of ProfileEvaluator.EffectiveTools.
type EffectiveToolsOptions struct {
	// Client evaluates that client's credential UNDER the profile ("what
	// would Cursor get on this profile"). Administrators only.
	Client string
	// Server keeps only that server's rows.
	Server string
	// Reason keeps only rows with that access reason. Administrators only.
	Reason string
	// Viewer bounds what the caller may see; the zero value is an
	// administrator.
	Viewer ViewerScope
}

// ToolAccessView is a row's verdict: visible = listed by discovery, callable =
// a real call would run.
type ToolAccessView struct {
	Visible  bool   `json:"visible"`
	Callable bool   `json:"callable"`
	Reason   string `json:"reason"`
}

// EffectiveTool is one row of a profile's effective tools (data-model §7).
type EffectiveTool struct {
	Server              string         `json:"server"`
	Tool                string         `json:"tool"`
	IntrinsicTier       string         `json:"intrinsic_tier"`
	ProfileTier         string         `json:"profile_tier"`
	Access              ToolAccessView `json:"access"`
	ClassificationStale bool           `json:"classification_stale"`
}

// EffectiveCounts are the response-level counts. Visible and Hidden are for
// every caller; the rest are administrator-only.
type EffectiveCounts struct {
	Visible  int            `json:"visible"`
	Hidden   int            `json:"hidden"`
	Callable *int           `json:"callable,omitempty"`
	ByReason map[string]int `json:"by_reason,omitempty"`
}

// EffectiveToolsResult is GET /profiles/{name}/effective-tools.
type EffectiveToolsResult struct {
	Profile string          `json:"profile"`
	Tools   []EffectiveTool `json:"tools"`
	Counts  EffectiveCounts `json:"counts"`
	// StaleClassifications lists classify entries for tools that are now
	// annotated or no longer exist (FR-005). Administrators only.
	StaleClassifications []string `json:"stale_classifications,omitempty"`
	// StaleClassificationReasons maps each stale classify entry to why it no
	// longer applies: profile.StaleClassificationAnnotated or
	// profile.StaleClassificationMissing. It is computed over the UNFILTERED
	// tool set, so a server or reason filter never changes it. Administrators
	// only.
	StaleClassificationReasons map[string]string `json:"stale_classification_reasons,omitempty"`
}

// --- try ---------------------------------------------------------------------

// TryHidden is one search hit the draft profile would hide.
type TryHidden struct {
	Server string `json:"server"`
	Tool   string `json:"tool"`
	Reason string `json:"reason"`
}

// TryResult is POST /profiles/try: what retrieve_tools would return under a
// draft profile, with what the draft hides. Nothing is persisted.
type TryResult struct {
	Results         []map[string]interface{} `json:"results"`
	HiddenByProfile int                      `json:"hidden_by_profile"`
	Hidden          []TryHidden              `json:"hidden"`
	HiddenTruncated bool                     `json:"hidden_truncated"`
}

// --- access explanation ----------------------------------------------------

// ExplainSubjectView names whose access was explained.
type ExplainSubjectView struct {
	Kind profile.AccessSubjectKind `json:"kind"`
	Name string                    `json:"name,omitempty"`
}

// ExplainProfileView is the profile the subject resolved to and where that
// resolution came from (pin, binding, anonymous, ...).
type ExplainProfileView struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// ExplainStepView is one evaluated step of the chain.
type ExplainStepView struct {
	Step   profile.ExplainStep      `json:"step"`
	Status profile.AccessStepStatus `json:"status"`
	Detail string                   `json:"detail"`
}

// Fix is one remediation of the first failing step, in preference order.
type Fix struct {
	Step   profile.ExplainStep `json:"step"`
	Action profile.FixAction   `json:"action"`
	Target string              `json:"target"`
	Label  string              `json:"label"`
}

// AccessExplanation is GET /access/explain (FR-035, data-model §7).
type AccessExplanation struct {
	Subject      ExplainSubjectView     `json:"subject"`
	Tool         string                 `json:"tool"`
	Profile      ExplainProfileView     `json:"profile"`
	Steps        []ExplainStepView      `json:"steps"`
	Verdict      profile.ExplainVerdict `json:"verdict"`
	FirstFailure profile.ExplainStep    `json:"first_failure"`
	Fixes        []Fix                  `json:"fixes"`
}

// ProfileEvaluator is everything the profiles service needs from the server
// package's unexported predicates (the profile index, the shared per-tool gate,
// the search index). *server.MCPProxyServer implements it and
// Runtime.SetProfileEvaluator installs it (the SetBindingGuard pattern); an
// unwired evaluator answers ErrEvaluatorUnavailable (503), never an empty
// "allowed".
type ProfileEvaluator interface {
	EffectiveTools(ctx context.Context, name string, opt EffectiveToolsOptions) (*EffectiveToolsResult, error)
	TryProfile(ctx context.Context, draft config.ProfileConfig, query string, limit int) (*TryResult, error)
	Explain(ctx context.Context, subject profile.AccessSubject, tool string) (*AccessExplanation, error)
	ToolCounts(ctx context.Context, name string, viewer ViewerScope) ToolCounts
}

// ErrEvaluatorUnavailable is returned when no ProfileEvaluator is installed.
var ErrEvaluatorUnavailable = errors.New("profile evaluator unavailable")

// --- typed refusals ---------------------------------------------------------

// ProfileNotFoundError is `404 profile not found`.
type ProfileNotFoundError struct{ Name string }

func (e *ProfileNotFoundError) Error() string { return "profile not found" }

// ProfileExistsError is `409 profile_exists`.
type ProfileExistsError struct{ Name string }

func (e *ProfileExistsError) Error() string { return fmt.Sprintf("profile %q already exists", e.Name) }

// Code is the wire `code`.
func (e *ProfileExistsError) Code() string { return profile.ErrorCodeProfileExists }

// NameMismatchError is `409 name_mismatch`: a PUT whose body names another
// profile than its path (a rename has its own route).
type NameMismatchError struct{ Path, Body string }

func (e *NameMismatchError) Error() string {
	return "name must equal the path; use POST /profiles/{name}/rename"
}

// Code is the wire `code`.
func (e *NameMismatchError) Code() string { return profile.ErrorCodeNameMismatch }

// ProfileInUseError is `409 profile_in_use`: clients or tokens still point at
// the profile and neither reassign_to nor force was given.
type ProfileInUseError struct{ UsedBy UsedBy }

func (e *ProfileInUseError) Error() string { return "profile in use" }

// Code is the wire `code`.
func (e *ProfileInUseError) Code() string { return profile.ErrorCodeProfileInUse }

// ProfileIsAnonymousError is `409 profile_is_anonymous_profile`: the profile is
// the anonymous_profile and reassign_to was not given (force never overrides).
type ProfileIsAnonymousError struct{ UsedBy UsedBy }

func (e *ProfileIsAnonymousError) Error() string {
	return "profile is the anonymous_profile; pass reassign_to or change anonymous_profile first"
}

// Code is the wire `code`.
func (e *ProfileIsAnonymousError) Code() string { return profile.ErrorCodeProfileIsAnonymous }
