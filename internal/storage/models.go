package storage

import (
	"encoding/json"
	"errors"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"time"
)

// ErrToolApprovalNotFound is returned by GetToolApproval when no record
// exists for the requested server+tool key. Callers that want to distinguish
// "first time we've seen this tool" from a real read error (corrupt JSON,
// closed DB, mmap remap during compaction) MUST use errors.Is, not a generic
// `err != nil` check — a transient decode error must not be misread as
// "missing", which would otherwise silently overwrite a pending/changed
// record with a synthesized approved one.
var ErrToolApprovalNotFound = errors.New("tool approval not found")

// ErrUpstreamNotFound is returned by GetUpstream/GetUpstreamServer when no
// record exists for the requested server name. Callers that decide policy from
// the record MUST use errors.Is to tell it apart from a real read failure
// (corrupt record, closed DB, mmap remap): "no such server" is a verdict the
// caller can state, while an unreadable record means the caller knows nothing
// and must fail closed rather than answer "not configured".
var ErrUpstreamNotFound = errors.New("upstream not found")

// Bucket names for bbolt database
const (
	UpstreamsBucket       = "upstreams"
	ToolStatsBucket       = "toolstats"
	ToolHashBucket        = "toolhash"
	ToolApprovalBucket    = "tool_approvals"
	PromptApprovalBucket  = "prompt_approvals" // spec 100: per-prompt rug-pull baseline
	OAuthTokenBucket      = "oauth_tokens"     //nolint:gosec // bucket name, not a credential
	OAuthCompletionBucket = "oauth_completion"
	MetaBucket            = "meta"
	CacheBucket           = "cache"
	CacheStatsBucket      = "cache_stats"
	// SessionsBucket holds MCP transport/work session records.
	//
	// It is NOT "sessions". The server edition stores USER LOGIN sessions in a
	// bucket of that name, on the same BBolt database, and the two were silently
	// destroying each other:
	//
	//   - enforceSessionRetention (here) keeps the 100 newest keys and deletes the
	//     rest by raw key order, with no type check — evicting user auth sessions
	//     and logging people out at random.
	//   - the server edition's CleanupExpiredSessions unmarshals every value as an
	//     auth session; an MCP record has no expires_at, so its zero time reads as
	//     long expired and the record is deleted.
	//
	// Namespacing the bucket is what makes each side's sweep safe.
	SessionsBucket = "mcp_sessions"

	// LegacySessionsBucket is the pre-namespacing bucket. In the personal edition
	// it holds only MCP records; in the server edition it holds a mix of those and
	// user login sessions. migrateLegacySessions moves the MCP records out and
	// leaves the auth sessions where they are, so nobody is logged out.
	LegacySessionsBucket = "sessions"

	// Security scanner buckets (Spec 039)
	ScannersBucket           = "security_scanners"
	ScanJobsBucket           = "security_scan_jobs"
	ScanJobIndexBucket       = "security_scan_job_index" // lightweight ScanJobMeta index (MCP-2205)
	ScanReportsBucket        = "security_reports"
	IntegrityBaselinesBucket = "integrity_baselines"

	// Onboarding wizard bucket (Spec 046)
	OnboardingBucket = "onboarding"
)

// Onboarding state keys (Spec 046)
const (
	OnboardingStateKey = "wizard_state"
)

// Onboarding step-status values (Spec 046; enum widened by Spec 080 FR-001).
const (
	// StepStatusCompleted means the user completed the step inside the wizard.
	StepStatusCompleted = "completed"
	// StepStatusCompletedExternal means the wizard's connect step was never
	// advanced, but at dismissal time the install showed positive evidence of
	// a connection made outside the wizard (a supported client currently
	// connected, or an MCP client has ever handshaked). Connect step only
	// (Spec 080 FR-002).
	StepStatusCompletedExternal = "completed_external"
	// StepStatusSkipped means the wizard was dismissed with the step
	// untouched and no external-connection evidence was established.
	StepStatusSkipped = "skipped"
)

// OnboardingState records whether the user has engaged with the first-run
// wizard, and which steps they completed or skipped. Persisted under
// OnboardingBucket / OnboardingStateKey. Absence of the record means
// "wizard has never been shown to this installation".
type OnboardingState struct {
	// Engaged is true once the wizard was shown and the user completed or
	// skipped it. Once true, the wizard does not auto-show again, even if
	// state regresses (e.g. user disconnects all clients).
	Engaged bool `json:"engaged"`

	// FirstShownAt is the timestamp of first wizard render.
	FirstShownAt *time.Time `json:"first_shown_at,omitempty"`

	// EngagedAt is the timestamp of completion or explicit skip.
	EngagedAt *time.Time `json:"engaged_at,omitempty"`

	// ConnectStepStatus is one of: "", "completed", "completed_external",
	// "skipped" (Spec 080 FR-001). "completed_external" records a dismissal
	// where the connect step was untouched but the install was already
	// connected outside the wizard (CLI, ConnectModal, manual config).
	ConnectStepStatus string `json:"connect_step_status,omitempty"`

	// ServerStepStatus is one of: "", "completed", "skipped".
	ServerStepStatus string `json:"server_step_status,omitempty"`
}

// Meta keys
const (
	SchemaVersionKey       = "schema"
	DockerRecoveryStateKey = "docker_recovery_state"
	// BaselineSweepDoneKey marks that the one-shot informational baseline scan
	// sweep has already run on this installation. Presence of the record — not
	// its contents — is what suppresses a re-run, so the sweep stays one-shot
	// across restarts and upgrades.
	BaselineSweepDoneKey = "baseline_sweep_done"
)

// BaselineSweepState records the outcome of the one-shot informational baseline
// scan sweep (the post-upgrade catch-up that scans pre-existing servers which
// have never been scanned). Stored in MetaBucket under BaselineSweepDoneKey.
// Absence of the record means "the sweep has never completed here".
type BaselineSweepState struct {
	// Version is the mcpproxy build version that completed the sweep. Recorded
	// for diagnostics; the sweep does not re-run on a version change.
	Version string `json:"version,omitempty"`
	// CompletedAt is when the sweep finished.
	CompletedAt time.Time `json:"completed_at"`
	// ServersScanned is how many servers the sweep actually scanned.
	ServersScanned int `json:"servers_scanned"`
	// Findings is the total number of findings the sweep's scans produced.
	Findings int `json:"findings"`
}

// Current schema version
const CurrentSchemaVersion = 3

// OutputSchemaHashSchemaVersion is the schema version that starts including
// MCP outputSchema in the tool approval hash baseline.
const OutputSchemaHashSchemaVersion = 3

// UpstreamRecord represents an upstream server record in storage
type UpstreamRecord struct {
	ID             string                  `json:"id"`
	Name           string                  `json:"name"`
	URL            string                  `json:"url,omitempty"`
	Protocol       string                  `json:"protocol,omitempty"` // stdio, http, sse, streamable-http, auto
	Command        string                  `json:"command,omitempty"`
	Args           []string                `json:"args,omitempty"`
	WorkingDir     string                  `json:"working_dir,omitempty"` // Working directory for stdio servers
	Env            map[string]string       `json:"env,omitempty"`
	Headers        map[string]string       `json:"headers,omitempty"` // For HTTP authentication
	OAuth          *config.OAuthConfig     `json:"oauth,omitempty"`   // OAuth configuration
	Enabled        bool                    `json:"enabled"`
	Quarantined    bool                    `json:"quarantined"` // Security quarantine status
	Created        time.Time               `json:"created"`
	Updated        time.Time               `json:"updated"`
	Isolation      *config.IsolationConfig `json:"isolation,omitempty"`        // Per-server isolation settings
	ReconnectOnUse bool                    `json:"reconnect_on_use,omitempty"` // Attempt reconnection on tool call
	// AutoApproveToolChanges (MCP-2930/MCP-2940) is the per-server intent to
	// auto-approve new/changed tools past the trust baseline. Tri-state *bool.
	// Persisted to BBolt because SaveConfiguration rebuilds the JSON config's
	// server list from these records — a field absent here is wiped on the
	// next mutation, so REST/UI toggling (MCP-2932) and runtime enforcement
	// (MCP-2931) would not survive a save/restart without it.
	AutoApproveToolChanges *bool `json:"auto_approve_tool_changes,omitempty"`
	// TrustMode (spec 086) is the per-server trust tier (auto|scan|manual) that
	// governs new-server admission and tool-change approval. Persisted to BBolt
	// for the same reason as AutoApproveToolChanges: SaveConfiguration rebuilds
	// the JSON server list from these records, so a REST/UI/MCP-set trust_mode
	// would be wiped on the next save without it.
	TrustMode           string          `json:"trust_mode,omitempty"`
	LauncherWaitTimeout config.Duration `json:"launcher_wait_timeout,omitempty"` // Spec 046: max wait for locally-launched HTTP/SSE upstream URL to become reachable
	EnabledTools        []string        `json:"enabled_tools,omitempty"`         // Allowlist: only these tools are exposed
	DisabledTools       []string        `json:"disabled_tools,omitempty"`        // Denylist: these tools are hidden
	// MCP-866: persist a server's registry origin + provenance so the
	// approval/quarantine view and the custom-origin skip_quarantine guard
	// survive a restart.
	SourceRegistryID         string `json:"source_registry_id,omitempty"`
	SourceRegistryProvenance string `json:"source_registry_provenance,omitempty"`
	// Spec 074: per-server discovery/health-check interval overrides, persisted
	// so REST-API/UI-set overrides survive a restart. *Duration tri-state:
	// nil = inherit, pointer-to-0s = disabled, positive = interval.
	HealthCheckInterval   *config.Duration `json:"health_check_interval,omitempty"`
	ToolDiscoveryInterval *config.Duration `json:"tool_discovery_interval,omitempty"`
	// MCP-3322: per-server MCP `initialize` handshake deadline override,
	// persisted so a REST/UI/CLI-set init_timeout survives a restart.
	InitTimeout *config.Duration `json:"init_timeout,omitempty"`
	// Spec 084: per-server toon_output override ("" = inherit global),
	// persisted so the override survives a restart and a SaveConfiguration
	// rebuild of the JSON server list.
	ToonOutput string `json:"toon_output,omitempty"`
	// Spec 093: per-server concurrency-limit overrides, persisted for the same
	// reason as the interval overrides above — SaveConfiguration rebuilds the
	// JSON server list from these records, so a REST/UI-set limit would be
	// wiped on the next save without them. Tri-state: nil = inherit the
	// per-server default set, 0 = opt out, positive = override.
	MaxConcurrentRequests *int             `json:"max_concurrent_requests,omitempty"`
	QueueSize             *int             `json:"queue_size,omitempty"`
	QueueTimeout          *config.Duration `json:"queue_timeout,omitempty"`
	// ExposePrompts is the per-server override for exposing upstream prompts
	// through mcpproxy's aggregated prompts/list and prompts/get.
	ExposePrompts *bool `json:"expose_prompts,omitempty"`
}

// ToolStatRecord represents tool usage statistics
type ToolStatRecord struct {
	ToolName string    `json:"tool_name"`
	Count    uint64    `json:"count"`
	LastUsed time.Time `json:"last_used"`
}

// ToolHashRecord represents a tool hash for change detection
type ToolHashRecord struct {
	ToolName string    `json:"tool_name"`
	Hash     string    `json:"hash"`
	Updated  time.Time `json:"updated"`
}

// ToolApprovalStatus constants for tool-level quarantine
const (
	ToolApprovalStatusApproved = "approved"
	ToolApprovalStatusPending  = "pending"
	ToolApprovalStatusChanged  = "changed"
)

// Tool hold reasons (spec 086, FR-018). They explain why the trust_mode: scan
// gate refused to auto-approve a tool and left it for human review, so the
// tool-approval surfaces can show WHY a tool is held, not merely THAT it is.
// Empty on records held for any other reason (manual mode, plain quarantine) and
// on every record written before this field existed — back-compat by omission.
const (
	// ToolHeldReasonScanFindings: the offline TPA scan returned a non-clean
	// verdict. HeldSignals names the matched check ids.
	ToolHeldReasonScanFindings = "scan_findings"
	// ToolHeldReasonScanCoverage: the scan itself could not be trusted (a check
	// failed, or the signature bundle was unavailable), so the gate failed closed
	// with no findings to show.
	ToolHeldReasonScanCoverage = "scan_coverage"
)

// MaxToolHeldSignals caps how many matched check ids are persisted on an
// approval record. The evidence is a review hint, not an audit log (the full
// finding set lives on the scan report), so a small deterministic prefix keeps
// records compact.
const MaxToolHeldSignals = 16

// ToolApprovalRecord represents a tool's approval status for tool-level quarantine.
// When a tool is first discovered, it starts as "pending". Once approved, it becomes "approved".
// If the tool's description or schema changes after approval, it becomes "changed".
type ToolApprovalRecord struct {
	ServerName           string    `json:"server_name"`
	ToolName             string    `json:"tool_name"`
	ApprovedHash         string    `json:"approved_hash"`
	CurrentHash          string    `json:"current_hash"`
	HashSchemaVersion    uint64    `json:"hash_schema_version,omitempty"`
	Status               string    `json:"status"` // "approved", "pending", "changed"
	ApprovedAt           time.Time `json:"approved_at"`
	ApprovedBy           string    `json:"approved_by"`
	PreviousDescription  string    `json:"previous_description,omitempty"`
	CurrentDescription   string    `json:"current_description,omitempty"`
	PreviousSchema       string    `json:"previous_schema,omitempty"`
	CurrentSchema        string    `json:"current_schema,omitempty"`
	PreviousOutputSchema string    `json:"previous_output_schema,omitempty"`
	CurrentOutputSchema  string    `json:"current_output_schema,omitempty"`
	Disabled             bool      `json:"disabled,omitempty"`

	// HeldReason, HeldVerdict and HeldSignals carry the scan evidence that made
	// the trust_mode: scan gate hold this tool for human review (spec 086
	// FR-018). They are set ONLY on the pass that performs the hold and cleared
	// whenever the record leaves the held state, so they always describe the
	// CURRENT hold. All three are additive and omitempty: records written before
	// this field existed (and every record held for a non-scan reason) decode
	// with them empty and render as they always did.
	//
	// HeldReason is one of the ToolHeldReason* constants.
	HeldReason string `json:"held_reason,omitempty"`
	// HeldVerdict is the offline scanner's baseline verdict at hold time
	// ("dangerous" / "warnings"; "clean" when only coverage failed).
	HeldVerdict string `json:"held_verdict,omitempty"`
	// HeldSignals lists the deterministic check ids that matched, e.g.
	// "tpa.TPA-2026-0001.hidden_instruction" or "phrase.injection". Deduplicated,
	// order-stable, capped at MaxToolHeldSignals.
	HeldSignals []string `json:"held_signals,omitempty"`
}

// SetScanHold records the evidence of a trust_mode: scan hold on the record.
// Signals are deduplicated in first-seen order and capped at MaxToolHeldSignals.
func (r *ToolApprovalRecord) SetScanHold(reason, verdict string, signals []string) {
	seen := make(map[string]bool, len(signals))
	out := make([]string, 0, len(signals))
	for _, s := range signals {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) == MaxToolHeldSignals {
			break
		}
	}
	r.HeldReason = reason
	r.HeldVerdict = verdict
	if len(out) == 0 {
		r.HeldSignals = nil
		return
	}
	r.HeldSignals = out
}

// ClearScanHold drops any scan-hold evidence. Called on every transition out of
// the held state so an approved record never renders a stale TPA badge.
func (r *ToolApprovalRecord) ClearScanHold() {
	r.HeldReason = ""
	r.HeldVerdict = ""
	r.HeldSignals = nil
}

// ErrPromptApprovalNotFound is returned by GetPromptApproval when no record
// exists (wrapped so callers can errors.Is it). A real read/decode failure
// returns a different error and MUST NOT be treated as "missing".
var ErrPromptApprovalNotFound = errors.New("prompt approval not found")

// PromptApprovalRecord is the per-(server, prompt) rug-pull baseline for
// aggregated upstream prompts (spec 100). It is the prompt analogue of
// ToolApprovalRecord, deliberately parallel — NOT a shared bucket — because the
// server:tool and server:prompt key spaces would collide and the tool record
// carries schema/scan fields prompts never use.
//
// It baselines ADVERTISED LIST METADATA ONLY (name + description + arguments);
// get-time prompts/get message content is out of scope and not baselineable
// here (spec 100 Non-Goals). Previous* fields are retained so a metadata revert
// is detectable (server swaps a description back → auto re-approve).
type PromptApprovalRecord struct {
	ServerName          string    `json:"server_name"`
	PromptName          string    `json:"prompt_name"`
	ApprovedHash        string    `json:"approved_hash"`
	CurrentHash         string    `json:"current_hash"`
	HashSchemaVersion   uint64    `json:"hash_schema_version,omitempty"`
	Status              string    `json:"status"` // "approved", "pending", "changed"
	ApprovedAt          time.Time `json:"approved_at"`
	ApprovedBy          string    `json:"approved_by"`
	PreviousDescription string    `json:"previous_description,omitempty"`
	CurrentDescription  string    `json:"current_description,omitempty"`
	PreviousArguments   string    `json:"previous_arguments,omitempty"`
	CurrentArguments    string    `json:"current_arguments,omitempty"`
	Disabled            bool      `json:"disabled,omitempty"`
}

// PromptApprovalKey returns the storage key for a prompt approval record.
func PromptApprovalKey(serverName, promptName string) string {
	return serverName + ":" + promptName
}

// Key returns the storage key for this prompt approval record.
func (r *PromptApprovalRecord) Key() string {
	return PromptApprovalKey(r.ServerName, r.PromptName)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (r *PromptApprovalRecord) MarshalBinary() ([]byte, error) {
	return json.Marshal(r)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (r *PromptApprovalRecord) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, r)
}

// ToolApprovalKey returns the storage key for a tool approval record.
func ToolApprovalKey(serverName, toolName string) string {
	return serverName + ":" + toolName
}

// Key returns the storage key for this tool approval record.
func (r *ToolApprovalRecord) Key() string {
	return ToolApprovalKey(r.ServerName, r.ToolName)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (r *ToolApprovalRecord) MarshalBinary() ([]byte, error) {
	return json.Marshal(r)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (r *ToolApprovalRecord) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, r)
}

// OAuthTokenRecord represents stored OAuth tokens for a server
type OAuthTokenRecord struct {
	ServerName   string    `json:"server_name"`            // Storage key (serverName_hash format)
	DisplayName  string    `json:"display_name,omitempty"` // Actual server name (for RefreshManager lookup)
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes,omitempty"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
	// ClientID and ClientSecret are persisted for DCR (Dynamic Client Registration)
	// These are required for token refresh when using DCR-obtained credentials
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	// CallbackPort and RedirectURI are persisted for OAuth redirect URI port persistence (Spec 022)
	// These ensure re-authentication uses the same port registered during DCR
	CallbackPort int    `json:"callback_port,omitempty"` // Port used during DCR for redirect_uri
	RedirectURI  string `json:"redirect_uri,omitempty"`  // Full redirect URI registered with DCR
}

// GetServerName returns the actual server name for RefreshManager lookup.
// Falls back to ServerName if DisplayName is not set (for backward compatibility).
func (r *OAuthTokenRecord) GetServerName() string {
	if r.DisplayName != "" {
		return r.DisplayName
	}
	return r.ServerName
}

// OAuthCompletionEvent represents an OAuth completion event for cross-process notification
type OAuthCompletionEvent struct {
	ServerName  string     `json:"server_name"`
	CompletedAt time.Time  `json:"completed_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"` // Nil if not yet processed by server
}

// DockerRecoveryState represents the persistent state of Docker recovery
// This allows recovery to resume after application restart
type DockerRecoveryState struct {
	LastAttempt      time.Time `json:"last_attempt"`
	FailureCount     int       `json:"failure_count"`
	DockerAvailable  bool      `json:"docker_available"`
	RecoveryMode     bool      `json:"recovery_mode"`
	LastError        string    `json:"last_error,omitempty"`
	AttemptsSinceUp  int       `json:"attempts_since_up"` // Attempts since Docker was last available
	LastSuccessfulAt time.Time `json:"last_successful_at,omitempty"`
}

// MarshalBinary implements encoding.BinaryMarshaler
func (u *UpstreamRecord) MarshalBinary() ([]byte, error) {
	return json.Marshal(u)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (u *UpstreamRecord) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, u)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (t *ToolStatRecord) MarshalBinary() ([]byte, error) {
	return json.Marshal(t)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (t *ToolStatRecord) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, t)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (h *ToolHashRecord) MarshalBinary() ([]byte, error) {
	return json.Marshal(h)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (h *ToolHashRecord) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, h)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (o *OAuthTokenRecord) MarshalBinary() ([]byte, error) {
	return json.Marshal(o)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (o *OAuthTokenRecord) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, o)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (e *OAuthCompletionEvent) MarshalBinary() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (e *OAuthCompletionEvent) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, e)
}

// MarshalBinary implements encoding.BinaryMarshaler
func (d *DockerRecoveryState) MarshalBinary() ([]byte, error) {
	return json.Marshal(d)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (d *DockerRecoveryState) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, d)
}
