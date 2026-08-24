package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
	"golang.org/x/mod/semver"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// SchemaVersion is the heartbeat payload schema version. v1 payloads have no
// such field; receivers can route by absence vs presence.
//
// v3 (schema bump from 2): adds feature_flags.docker_available,
// server_protocol_counts, and server_docker_isolated_count.
//
// v4 (schema bump from 3): adds onboarding-funnel fields per Spec 046 —
// connected_client_count, connected_client_ids, wizard_engaged,
// wizard_connect_step, wizard_server_step. Forward-compatible: existing
// v3 consumers ignore the new fields.
//
// v5 (schema bump from 4): adds docker-isolation visibility per MCP-2745 —
// feature_flags.docker_isolation_enabled, feature_flags.docker_cli_source
// (4-way enum: path|bundled|login_shell|absent, the #696 fleet signal), and
// three new diagnostics codes surfaced via error_code_counts_24h
// (MCPX_DOCKER_CLI_NOT_FOUND / MCPX_DOCKER_EXEC_NOT_FOUND /
// MCPX_DOCKER_OCI_RUNTIME). Additive only — v3/v4 consumers ignore them.
//
// v6 (schema bump from 5): adds machine_id — a stable, non-reversible hash of
// the OS machine id (HMAC-SHA256 keyed by the OS machine id, scoped by an
// app-specific key). It lets the dashboard dedup installs whose anonymous_id
// churns every run (ephemeral Docker layers, throwaway HOMEs, CI). Additive
// and forward-compatible: v3/v4/v5 consumers ignore it, and the ingest worker
// stores payload_json wholesale without rejecting unknown fields or higher
// schema versions.
//
// v7 (schema bump from 6): Spec 080 — honest activation funnel + churn
// instrumentation. Additive only; v6-and-earlier consumers ignore every
// addition:
//   - wizard_connect_step enum widened with "completed_external" (connected
//     outside the wizard — CLI/ConnectModal/manual config — detected at
//     dismissal). Consumers switching on completed|skipped must treat
//     unknown values as "other/engaged".
//   - wizard_shown (bool): wizard rendered at least once, making "shown but
//     ignored" observable alongside wizard_engaged.
//   - web_ui_opened (int64): lifetime count of embedded Web UI index-document
//     serves, independent of surface_requests.webui.
//   - days_since_install (*int): whole-day UTC install age from a persisted
//     first-install day stamp; 0 is transmitted, nil (store not wired) omits.
//   - active_days_30d (int): count of distinct active UTC days in the
//     trailing 30-day window; the per-day set never leaves the machine.
//   - previous_shutdown (enum "clean"|"crash", absent = unknown/first run):
//     how the PREVIOUS process instance ended, from a persisted marker.
//   - last_error_code (enum MCPX_*): most recent stable diagnostic code,
//     persisted across restarts; never message text, names, or paths.
//
// All v7 fields are omitempty (zero-valued payloads stay shape-compatible
// with v6), fixed-enum/boolean/non-negative-integer only (enforced by
// ScanForPII), and ride the existing opt-out gate.
//
// v8 (schema bump from 7): anonymous TPA / security-scanner stats. Additive
// only; v7-and-earlier consumers ignore both additions:
//   - tpa_scanner (object, omitted entirely when every counter is zero — the
//     same posture as diagnostics): scans_completed, scans_failed,
//     scans_with_findings (non-negative integer counts over the reporting
//     window, reset only after an accepted heartbeat) and findings, a sparse
//     map from the FIXED severity enum (critical|high|medium|low|info) to a
//     non-negative count.
//   - feature_flags.deep_scan_enabled (bool): the opt-in deep-scan master
//     switch (security.deep_scan.enabled), so scan volume can be read against
//     the population that actually enabled the layer.
//
// The unit of every tpa_scanner counter is ONE NON-DEEP-SCAN (PASS 1) SCAN JOB:
// the Pass-2 deep supply-chain audit that deep scan auto-starts after Pass 1 is
// NOT counted (counting it would double the apparent scan volume of exactly the
// deep-scan cohort deep_scan_enabled exists to compare), dry-run jobs are NOT
// counted, and a job with several failing scanners is still one scan —
// scans_failed counts failed jobs, not failed scanners. The producer is
// scanCallbackAdapter.countsForTelemetry in internal/security/scanner.
//
// Anonymity properties: counts and fixed enum keys ONLY. Scanned server
// names, scanner ids, rule ids, finding titles, file paths, and error
// messages are never accepted by the counter API, and ScanForPII re-asserts
// the shape on the wire form (rule "v8_field_invalid"): tpa_scanner must be
// an object whose keys are whitelisted, whose scalar values are non-negative
// integers, and whose findings keys are members of the severity enum.
// Schema v9 makes the TPA funnel measurable. Two additions:
//
//   - tpa_scanner.tool_change_gate_scans / tpa_scanner.prompt_scans: delta
//     counters for the two SYNCHRONOUS detection paths that run for ordinary
//     users — the trust_mode:scan tool-change gate
//     (internal/runtime.scanChangeIsClean) and the aggregated-prompt poisoning
//     filter (internal/server.scanAggregatedPrompts). Neither emitted anything
//     before v9, so the v8 job counters alone made the fleet look like it never
//     scanned. Same window and reset semantics as every other registry counter
//     (zeroed only after an accepted send), and the whole tpa_scanner
//     sub-object is still omitted when every counter — v8 and v9 — is zero.
//   - trust_mode_distribution: a STATE field (not a delta), the count of
//     configured servers per config.ServerConfig.EffectiveTrustMode(), keyed by
//     the fixed auto|scan|manual enum and computed fresh at heartbeat build
//     time. It is the denominator the gate counter needs: gate scans are only
//     possible on servers in "scan" mode.
//
// Anonymity posture is unchanged: non-negative integer counts and fixed enum
// keys only. ScanForPII re-asserts both shapes on the wire form (rules
// "v8_field_invalid" for the widened tpa_scanner whitelist and
// "trust_mode_field_invalid" for the histogram).
const SchemaVersion = 9

// HeartbeatPayload is the anonymous telemetry payload sent periodically.
// Spec 042 expanded the payload with Tier 2 fields; v1 fields are preserved.
type HeartbeatPayload struct {
	// v1 fields (preserved unchanged)
	AnonymousID string `json:"anonymous_id"`
	// MachineID (schema v6) is a stable, non-reversible hash of the OS machine
	// id — HMAC-SHA256 keyed by the OS machine id, scoped by an app-specific key
	// (see machine_id.go). Unlike anonymous_id (a UUID persisted in the config
	// file, which is regenerated on every run in ephemeral environments —
	// throwaway HOMEs, Docker layers, CI), this value is stable per physical
	// machine, letting the dashboard dedup ephemeral installs. Empty/omitted
	// when the OS machine id cannot be read (containers without /etc/machine-id,
	// permission errors, exotic platforms); the backend treats empty as
	// "unknown". The raw machine id is NEVER transmitted — only the salted hash.
	// It rides the same opt-out gate as every other field (the whole heartbeat
	// is suppressed when telemetry is disabled).
	MachineID            string `json:"machine_id,omitempty"`
	Version              string `json:"version"`
	Edition              string `json:"edition"`
	OS                   string `json:"os"`
	Arch                 string `json:"arch"`
	GoVersion            string `json:"go_version"`
	ServerCount          int    `json:"server_count"`
	ConnectedServerCount int    `json:"connected_server_count"`
	ToolCount            int    `json:"tool_count"`
	UptimeHours          int    `json:"uptime_hours"`
	RoutingMode          string `json:"routing_mode"`
	QuarantineEnabled    bool   `json:"quarantine_enabled"`
	Timestamp            string `json:"timestamp"`

	// Spec 042 (Tier 2) additions
	SchemaVersion               int                         `json:"schema_version,omitempty"`
	AnonymousIDCreatedAt        string                      `json:"anonymous_id_created_at,omitempty"`
	CurrentVersion              string                      `json:"current_version,omitempty"`
	PreviousVersion             string                      `json:"previous_version"`
	LastStartupOutcome          string                      `json:"last_startup_outcome,omitempty"`
	SurfaceRequests             map[string]int64            `json:"surface_requests,omitempty"`
	BuiltinToolCalls            map[string]int64            `json:"builtin_tool_calls,omitempty"`
	UpstreamToolCallCountBucket string                      `json:"upstream_tool_call_count_bucket,omitempty"`
	RESTEndpointCalls           map[string]map[string]int64 `json:"rest_endpoint_calls,omitempty"`
	FeatureFlags                *FeatureFlagSnapshot        `json:"feature_flags,omitempty"`
	ErrorCategoryCounts         map[string]int64            `json:"error_category_counts,omitempty"`
	DoctorChecks                map[string]DoctorCounts     `json:"doctor_checks,omitempty"`

	// Schema v3 additions.
	// ServerProtocolCounts is a fixed-enum histogram over cfg.Servers by
	// Protocol. Keys are exactly: stdio, http, sse, streamable_http, auto.
	// Never contains server names, URLs, or unknown values (unknown/empty
	// protocols bucket into "auto").
	ServerProtocolCounts map[string]int `json:"server_protocol_counts,omitempty"`
	// ServerDockerIsolatedCount is the number of configured servers the
	// runtime actually wraps in Docker isolation. Distinct from "has Docker
	// available" — an install can have Docker but never use it for isolation.
	ServerDockerIsolatedCount int `json:"server_docker_isolated_count,omitempty"`

	// Spec 044 additions. schema_version stays at 3 (set by spec 042); these
	// fields are additive and forward-compatible.
	//
	// EnvKind: ground-truth classification of the process environment, computed
	// once at startup by DetectEnvKindOnce. One of the EnvKind* constants.
	EnvKind string `json:"env_kind,omitempty"`
	// EnvMarkers: raw boolean observations feeding EnvKind. Fields are ALL
	// booleans — the anonymity scanner re-asserts this on the serialized form.
	EnvMarkers *EnvMarkers `json:"env_markers,omitempty"`

	// Activation is the retention funnel snapshot: monotonic first-ever flags,
	// 24h sliding counters, and bucketed token-savings estimate. Loaded from
	// the BBolt activation bucket at heartbeat build time. nil when the store
	// is not wired (e.g. in short-lived CLI commands).
	Activation *ActivationState `json:"activation,omitempty"`

	// LaunchSource: how the process was launched. One of "installer", "tray",
	// "login_item", "cli", "unknown". Detected once at startup via
	// DetectLaunchSourceOnce, with a one-shot "installer" override driven by
	// the installer_heartbeat_pending BBolt flag (cleared after first heartbeat).
	LaunchSource string `json:"launch_source,omitempty"`

	// AutostartEnabled: tri-state login-item status.
	//   - *true  : tray reports the app IS registered as a login item
	//   - *false : tray reports the app is NOT registered
	//   - nil    : unknown (tray not running, Linux, or sidecar absent)
	// Pointer intentional so JSON null is distinguishable from false —
	// receivers need this to separate "user disabled" from "we don't know".
	AutostartEnabled *bool `json:"autostart_enabled"`

	// Spec 046 — onboarding funnel fields. All values are anonymous,
	// fixed-enum, and inherit Spec 042 / Spec 044 privacy posture: no
	// upstream-server names, no user-entered strings, no free text.

	// ConnectedClientCount is the number of supported AI clients in which
	// mcpproxy is currently registered (i.e. has the URL/auth in their
	// native config file). Computed by connect.Service from the existing
	// per-client adapter table.
	ConnectedClientCount int `json:"connected_client_count,omitempty"`

	// ConnectedClientIDs are the identifiers of supported clients currently
	// pointing at mcpproxy. Drawn ONLY from the fixed adapter table
	// (e.g. "claude-code", "cursor", "vscode", "windsurf", "codex",
	// "gemini"). User-entered values, paths, and arbitrary strings MUST
	// NEVER appear in this field — the anonymity scanner asserts this.
	ConnectedClientIDs []string `json:"connected_client_ids,omitempty"`

	// WizardEngaged is true once the user completed or explicitly skipped
	// the first-run onboarding wizard. Once true, the wizard does not
	// auto-show again, even if state regresses.
	WizardEngaged bool `json:"wizard_engaged,omitempty"`

	// WizardConnectStep is the per-step status for "Connect an AI client":
	// one of "" (not shown to this install), "completed",
	// "completed_external" (Spec 080: connected outside the wizard —
	// CLI/ConnectModal/manual config — detected at dismissal), or "skipped".
	// Consumers switching on completed|skipped must treat unknown values as
	// "other/engaged".
	WizardConnectStep string `json:"wizard_connect_step,omitempty"`

	// WizardServerStep is the per-step status for "Add an MCP server":
	// one of "" (not shown to this install), "completed", or "skipped".
	WizardServerStep string `json:"wizard_server_step,omitempty"`

	// Spec 080 (US2) — funnel observability fields. All additive and
	// omitempty so zero-valued payloads stay shape-compatible with v6.
	// Privacy posture: booleans and non-negative integers only — no
	// timestamps, no per-day breakdown, no per-server identity.

	// WizardShown is true once the onboarding wizard has rendered at least
	// once for this install (OnboardingState.FirstShownAt set). Combined
	// with WizardEngaged it makes "shown but ignored" observable:
	// wizard_shown=true with wizard_engaged absent/false.
	WizardShown bool `json:"wizard_shown,omitempty"`

	// WebUIOpened is the lifetime count of embedded Web UI index-document
	// serves (the UI entrypoint), persisted in BBolt. Independent of the
	// X-MCPProxy-Client-header-based surface_requests.webui counter: it
	// counts opening the UI, not SPA API traffic. Asset and API requests
	// never increment it. Coarse by design (health checkers fetching /
	// count too); documented as "index serves".
	WebUIOpened int64 `json:"web_ui_opened,omitempty"`

	// DaysSinceInstall is the whole-day UTC age of the install, from a
	// persisted first-install day stamp independent of anonymous_id.
	// Non-negative (clamped at 0 on clock skew). Pointer so day 0 (install
	// day) is transmitted while "store not wired" is omitted — the same
	// nil-safety as Activation. No install timestamp is ever transmitted.
	DaysSinceInstall *int `json:"days_since_install,omitempty"`

	// ActiveDays30d is the number of distinct active UTC days in the
	// trailing 30-day window (1..30 once any activity is recorded). Only
	// this cardinality leaves the machine; the per-day set stays local.
	ActiveDays30d int `json:"active_days_30d,omitempty"`

	// Spec 080 (US3) — pre-churn snapshot fields. Additive and omitempty so
	// zero-valued payloads stay shape-compatible with v6. When the churn
	// pipeline later identifies a churned install, its final heartbeat
	// already distinguishes "crashed and never came back" from "exited
	// cleanly and never returned".

	// PreviousShutdown reports how the PREVIOUS process instance ended:
	// "clean" (graceful-shutdown path resolved the persisted marker) or
	// "crash" (marker armed at startup but never resolved — SIGKILL, panic,
	// power loss). Absent on a first-ever run (no prior marker) or when the
	// store is not wired — a fresh install is never misreported as a crash
	// (FR-010/FR-013). Computed once at startup and stable across all
	// heartbeats of the instance (FR-011).
	PreviousShutdown string `json:"previous_shutdown,omitempty"`

	// LastErrorCode is the most recently observed stable MCPX_* diagnostic
	// code (same fixed code set as diagnostics.error_code_counts_24h),
	// persisted across restarts so the post-crash heartbeat carries the
	// pre-crash code. Enum code only — never message text, stack traces,
	// server names, or paths. Absent when no error was ever recorded
	// (FR-012).
	LastErrorCode string `json:"last_error_code,omitempty"`

	// Spec 044 Phase H: diagnostics counter snapshot. Omitted entirely when
	// all counters are zero (omitempty on the pointer). No PII: only stable
	// MCPX_* enum strings, non-negative int counts.
	Diagnostics *DiagnosticsCounters `json:"diagnostics,omitempty"`

	// Issue #969 (Phase 0): required-tools-preflight BASELINE counters —
	// filter-diagnostics engagement + availability/discovery-omission classes.
	// Omitted entirely when all counters are zero (omitempty on the pointer),
	// so an install that never trips one is shape-identical to a payload from
	// before this field existed. No PII: non-negative counts, plus a reason map
	// keyed exclusively by the closed availabilityBlockReasonKeys enum.
	Preflight *PreflightCounters `json:"preflight,omitempty"`

	// Schema v8: anonymous TPA / security-scanner outcome counters. Omitted
	// entirely when all counters are zero (omitempty on the pointer) — an
	// install that never scans is shape-identical to a v7 payload. No PII:
	// non-negative counts keyed by the fixed severity enum only; never a
	// scanned server name, scanner id, rule id, or finding title.
	TPAScanner *TPAScannerStats `json:"tpa_scanner,omitempty"`

	// Schema v9: count of configured servers per effective trust tier, keyed
	// exclusively by the fixed auto|scan|manual enum (all three keys always
	// present, even at zero — the protocol-counts convention). A STATE field,
	// recomputed from the live config on every heartbeat and never reset. It
	// gives the tpa_scanner gate counter its denominator: only servers in
	// "scan" mode can produce a tool-change gate scan at all. No PII: counts
	// only, no server names, no raw config strings.
	TrustModeDistribution map[string]int `json:"trust_mode_distribution,omitempty"`
}

// OnboardingSnapshot is the data the telemetry service needs to populate
// Spec 046 fields on each heartbeat. Built fresh per heartbeat so changes
// (e.g. user connects another client between heartbeats) are reflected.
type OnboardingSnapshot struct {
	ConnectedClientCount int
	ConnectedClientIDs   []string
	WizardEngaged        bool
	WizardConnectStep    string
	WizardServerStep     string
	// WizardShown (Spec 080 US2): the wizard rendered at least once for
	// this install — derived from OnboardingState.FirstShownAt != nil.
	WizardShown bool
}

// RuntimeStats is an interface to decouple from the runtime package.
type RuntimeStats interface {
	GetServerCount() int
	GetConnectedServerCount() int
	GetToolCount() int
	GetRoutingMode() string
	IsQuarantineEnabled() bool
	// Schema v3 additions.
	// IsDockerAvailable reports whether the host has a reachable Docker
	// daemon. Implementations should memoize the probe result (running
	// `docker info` on every heartbeat has cost) and return the cached value.
	IsDockerAvailable() bool
	// GetDockerIsolatedServerCount returns how many currently-configured
	// servers the runtime is actually wrapping in a Docker container.
	GetDockerIsolatedServerCount() int
	// GetDockerCLISource returns the coarse, fixed-enum branch that resolved
	// the docker CLI — "path" | "bundled" | "login_shell" | "absent" (schema
	// v5, MCP-2745). Implementations should memoize the resolution (it shares
	// the shellwrap docker-path cache) and NEVER return the path string.
	GetDockerCLISource() string
}

// Service manages anonymous telemetry heartbeats and feedback submission.
type Service struct {
	config    *config.Config
	cfgPath   string
	version   string
	edition   string
	endpoint  string
	logger    *zap.Logger
	stats     RuntimeStats
	startTime time.Time
	client    *http.Client

	// Feedback rate limiter (max 5 per hour)
	feedbackLimiter *RateLimiter

	// Spec 042: Tier 2 counter aggregator. Always non-nil after New.
	registry *CounterRegistry

	// Spec 042: env-based opt-out reason captured at construction time.
	envDisabledReason EnvDisabledReason

	// Spec 044: activation store (BBolt-backed) + BBolt DB handle. Optional —
	// may be nil if the telemetry service is constructed for a short-lived CLI
	// command (e.g. `telemetry show-payload` in-process fallback). When nil,
	// Activation is simply omitted from the heartbeat.
	activationStore ActivationStore
	activationDB    *bbolt.DB

	// Spec 044 Phase H: diagnostics counter store + DB handle. Optional — same
	// nil-safety guarantee as activationStore. When nil, Diagnostics is omitted.
	diagCounterStore DiagnosticsCounterStore
	diagCounterDB    *bbolt.DB

	// Issue #969 (Phase 0): preflight baseline counter store + DB handle.
	// Optional — same nil-safety guarantee as activationStore. When nil, the
	// preflight sub-object is omitted and every Record* below is a no-op.
	preflightStore PreflightCounterStore
	preflightDB    *bbolt.DB

	// Spec 080 (US2): funnel observability store + DB handle. Optional —
	// same nil-safety guarantee as activationStore. When nil, web_ui_opened,
	// days_since_install, and active_days_30d are omitted (short-lived CLI
	// commands).
	funnelStore FunnelStore
	funnelDB    *bbolt.DB

	// Spec 080 (US3): pre-churn snapshot state. previousShutdown is derived
	// exactly once at startup by the runtime (ArmShutdownMarker) and copied
	// here before the heartbeat loop starts, so it is stable across every
	// heartbeat of this instance (FR-011). The store/DB pair follows the
	// same nil-safety contract as activationStore: when unset (short-lived
	// CLI commands), previous_shutdown and last_error_code are omitted.
	previousShutdown string
	prechurnStore    PreChurnStore
	prechurnDB       *bbolt.DB

	// Spec 044: optional provider for configured IDE count. Populated by the
	// runtime from internal/connect at wire-up time. nil-safe.
	configuredIDECountProvider func() int

	// Spec 044 (T049): autostart reader for the tray-owned sidecar. Lazy-
	// initialized on first heartbeat; tests may inject a mock via
	// SetAutostartReader.
	autostartReader *AutostartReader

	// Spec 046: optional provider for the onboarding-funnel snapshot.
	// Wired by the runtime at startup with a closure over connect.Service
	// and the BBolt-backed OnboardingState. nil-safe: when unset the
	// onboarding fields are simply omitted from the heartbeat.
	onboardingProvider func() *OnboardingSnapshot

	// For testing: override initial delay and heartbeat interval
	initialDelay      time.Duration
	heartbeatInterval time.Duration

	// MCP-2482: one-time opt-out beacon state.
	// mu guards resolvedEnabled, config, and endpoint, which NotifyConfigChanged
	// mutates on a live config swap.
	mu sync.Mutex
	// resolvedEnabled is the last-known resolved telemetry-enabled state
	// (IsTelemetryEnabled — nil means enabled). Used to detect the
	// enabled->disabled flip that fires the opt-out beacon.
	resolvedEnabled bool
	// optedOut latches true once the opt-out beacon has fired; it gates all
	// further heartbeat emission so no telemetry leaves after the user opts out.
	optedOut atomic.Bool

	// Shutdown coordination (Spec 080 FR-010, review round 6): the heartbeat
	// loop is a BBolt writer — buildHeartbeat records funnel activity
	// (funnelStore.RecordActivity) and the first tick clears the
	// installer-pending activation flag — so Runtime.Close must be able to
	// JOIN the loop, not merely context-cancel it, before the clean-shutdown
	// marker resolves and the DB closes. done closes when the Start body
	// exits on ANY path (including the disabled-by-env/config/semver early
	// returns). startMu/started/stopped mirror ActivityService: production
	// launches Start via `go` (lifecycle.go), so a fast shutdown can run
	// Stop BEFORE the Start goroutine is scheduled — Stop marks stopped
	// terminally under startMu and a later Start becomes a no-op instead of
	// a heartbeat loop writing BBolt after the shutdown-marker path began.
	// started also refuses a second Start (done is single-shot).
	done    chan struct{}
	startMu sync.Mutex
	started bool
	stopped bool

	// Graceful-shutdown flush. Counters live in memory and are only reset after
	// an ACCEPTED send, so everything recorded after the last accepted heartbeat
	// (in practice: everything after the 5-minute first heartbeat, for any
	// process that does not survive to the 24h tick) was previously dropped on
	// exit. Stop now performs ONE final send after joining the loop.
	//
	// flushEligible is set by Start once it has passed every emission gate
	// (env / config / semver) and ensured the anonymous id — i.e. exactly when a
	// heartbeat from this process would have been legitimate. Stop consults it
	// so a disabled or dev build never sends on shutdown.
	//
	// flushOnce keeps the flush single-shot across the idempotent Stop.
	// flushTimeout is overridable in tests; see shutdownFlushTimeout.
	flushEligible atomic.Bool
	flushOnce     sync.Once
	flushTimeout  time.Duration
}

// optOutBeaconTimeout bounds the best-effort opt-out beacon send so a slow or
// unreachable endpoint never delays the config save that triggered it.
const optOutBeaconTimeout = 5 * time.Second

// shutdownFlushTimeout hard-bounds the final heartbeat sent on graceful
// shutdown. It is deliberately much shorter than the HTTP client's own 10s
// timeout: a dead or blackholed endpoint must never turn a user's quit into a
// visible hang. The send is best-effort — on timeout the counters are simply
// retained (no 2xx ⇒ no Reset) and the next run reports them.
const shutdownFlushTimeout = 4 * time.Second

// New creates a new telemetry service.
func New(cfg *config.Config, cfgPath, version, edition string, logger *zap.Logger) *Service {
	_, envReason := IsDisabledByEnv()
	return &Service{
		config:            cfg,
		cfgPath:           cfgPath,
		version:           normalizeVersion(version),
		edition:           edition,
		endpoint:          cfg.GetTelemetryEndpoint(),
		logger:            logger,
		startTime:         time.Now(),
		client:            &http.Client{Timeout: 10 * time.Second},
		feedbackLimiter:   NewRateLimiter(5),
		registry:          NewCounterRegistry(),
		envDisabledReason: envReason,
		initialDelay:      5 * time.Minute,
		heartbeatInterval: 24 * time.Hour,
		flushTimeout:      shutdownFlushTimeout,
		resolvedEnabled:   EffectiveTelemetryEnabled(cfg),
		done:              make(chan struct{}),
	}
}

// Registry returns the counter registry for Tier 2 telemetry events. Always
// non-nil after New, even if telemetry is disabled — that way callers can
// always Record* without nil checks; the data simply never leaves the process.
func (s *Service) Registry() *CounterRegistry {
	return s.registry
}

// EnvDisabledReason returns the env-var reason telemetry is disabled, if any.
func (s *Service) EnvDisabledReason() EnvDisabledReason {
	return s.envDisabledReason
}

// SetRuntimeStats sets the runtime stats provider (called after runtime is fully initialized).
func (s *Service) SetRuntimeStats(stats RuntimeStats) {
	s.stats = stats
}

// SetActivationStore wires the BBolt-backed activation store and the shared
// DB handle (Spec 044). Optional; when unset, heartbeat payloads omit the
// activation object entirely. Safe to call once during startup.
func (s *Service) SetActivationStore(store ActivationStore, db *bbolt.DB) {
	s.activationStore = store
	s.activationDB = db
}

// ActivationStore returns the wired store (or nil). Used by MCP and runtime
// integration points that need to increment counters.
func (s *Service) ActivationStore() ActivationStore {
	return s.activationStore
}

// ActivationDB returns the BBolt DB handle associated with the activation
// store (or nil). Callers pair this with ActivationStore() to perform writes.
func (s *Service) ActivationDB() *bbolt.DB {
	return s.activationDB
}

// SetFunnelStore wires the BBolt-backed funnel observability store (Spec 080
// US2). Optional; when unset, heartbeat payloads omit web_ui_opened,
// days_since_install, and active_days_30d. Safe to call once during startup.
func (s *Service) SetFunnelStore(store FunnelStore, db *bbolt.DB) {
	s.funnelStore = store
	s.funnelDB = db
}

// FunnelStore returns the wired funnel store (or nil).
func (s *Service) FunnelStore() FunnelStore {
	return s.funnelStore
}

// FunnelDB returns the BBolt DB handle associated with the funnel store
// (or nil).
func (s *Service) FunnelDB() *bbolt.DB {
	return s.funnelDB
}

// RecordWebUIOpen increments the lifetime web_ui_opened counter (Spec 080
// FR-006). Called by the embedded Web UI handler whenever the index document
// is served. nil-safe: a no-op when the funnel store is not wired, and a
// persistence error never propagates to the HTTP path (logged at debug).
func (s *Service) RecordWebUIOpen() {
	if s.funnelStore == nil || s.funnelDB == nil {
		return
	}
	if err := s.funnelStore.IncrementWebUIOpened(s.funnelDB); err != nil {
		s.logger.Debug("Failed to increment web_ui_opened counter", zap.Error(err))
	}
}

// SetPreChurn wires the Spec 080 US3 pre-churn snapshot: the startup-derived
// previous_shutdown value (stable for the life of this instance, FR-011) and
// the BBolt-backed store used to read last_error_code at heartbeat build
// time. Optional; when unset, both fields are omitted from the payload.
// Safe to call once during startup, before the heartbeat loop begins.
func (s *Service) SetPreChurn(previousShutdown string, store PreChurnStore, db *bbolt.DB) {
	s.previousShutdown = previousShutdown
	s.prechurnStore = store
	s.prechurnDB = db
}

// SetDiagnosticsCounterStore wires the BBolt-backed diagnostics counter store
// (Spec 044 Phase H). Optional; when unset, heartbeat payloads omit the
// diagnostics object. Safe to call once during startup.
func (s *Service) SetDiagnosticsCounterStore(store DiagnosticsCounterStore, db *bbolt.DB) {
	s.diagCounterStore = store
	s.diagCounterDB = db
}

// DiagnosticsCounterStore returns the wired counter store (or nil).
func (s *Service) DiagnosticsCounterStore() DiagnosticsCounterStore {
	return s.diagCounterStore
}

// DiagnosticsCounterDB returns the BBolt DB handle associated with the
// diagnostics counter store (or nil).
func (s *Service) DiagnosticsCounterDB() *bbolt.DB {
	return s.diagCounterDB
}

// SetPreflightCounterStore wires the BBolt-backed preflight baseline counter
// store (issue #969, Phase 0). Optional; when unset, heartbeat payloads omit
// the preflight object and every Record* below is a no-op. Safe to call once
// during startup.
func (s *Service) SetPreflightCounterStore(store PreflightCounterStore, db *bbolt.DB) {
	s.preflightStore = store
	s.preflightDB = db
}

// PreflightCounterStore returns the wired preflight counter store (or nil).
func (s *Service) PreflightCounterStore() PreflightCounterStore {
	return s.preflightStore
}

// PreflightCounterDB returns the BBolt DB handle associated with the preflight
// counter store (or nil).
func (s *Service) PreflightCounterDB() *bbolt.DB {
	return s.preflightDB
}

// preflightSink returns the store/DB pair to write to, or (nil, nil) when
// nothing may be recorded.
//
// The opt-out is evaluated at EVENT time, not only at heartbeat time — the
// strictest of the existing postures (recordUpdateFailure, spec 095 FR-013).
// An occurrence observed while telemetry is off is never persisted, so it can
// never become transmissible if the user turns telemetry back on later.
func (s *Service) preflightSink() (PreflightCounterStore, *bbolt.DB) {
	if s == nil || s.preflightStore == nil || s.preflightDB == nil {
		return nil, nil
	}
	if s.optedOut.Load() {
		return nil, nil
	}
	if !s.telemetryEnabledLive() {
		return nil, nil
	}
	return s.preflightStore, s.preflightDB
}

// telemetryEnabledLive resolves EffectiveTelemetryEnabled against the live
// config with s.mu held for the WHOLE evaluation.
//
// Snapshotting the pointer and then dereferencing it after unlocking is NOT
// enough: config.IsTelemetryEnabled reads cfg.Telemetry, and both
// ensureAnonymousIDOnce and advanceUpgradeFunnelOnce install that pointer on a
// config that arrived without a telemetry block (`cfg.Telemetry =
// &config.TelemetryConfig{}`) while holding s.mu. A locked write paired with an
// unlocked read is still a data race — the request path (Record*) and the
// heartbeat loop hit exactly that pair on a fresh install. Proven under -race
// by TestPreflightSinkTelemetryPointerRace.
//
// EffectiveTelemetryEnabled only reads env vars and config fields, so calling
// it under the lock cannot re-enter the Service.
func (s *Service) telemetryEnabledLive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return EffectiveTelemetryEnabled(s.config)
}

// preflightDebug logs a counter-persistence failure without ever propagating it
// — a telemetry counter must never break the request path that produced it.
func (s *Service) preflightDebug(msg string, err error) {
	if err == nil || s.logger == nil {
		return
	}
	s.logger.Debug(msg, zap.Error(err))
}

// RecordFilterDiagnosticsEmitted counts one retrieve_tools response that
// carried a spec-094 filter_diagnostics block, plus that block's per-reason
// class totals. Counts only — the filter keys and tool identities stay in the
// response and never reach telemetry.
func (s *Service) RecordFilterDiagnosticsEmitted(missingAnnotation, explicit int) {
	store, db := s.preflightSink()
	if store == nil {
		return
	}
	s.preflightDebug("Failed to record filter_diagnostics emission",
		store.RecordFilterDiagnosticsEmitted(db, missingAnnotation, explicit))
}

// RecordFilterDiagnosticsFollowed counts one diagnostics block the agent acted
// on (a later same-session retrieve_tools relaxed a blamed filter).
func (s *Service) RecordFilterDiagnosticsFollowed() {
	store, db := s.preflightSink()
	if store == nil {
		return
	}
	s.preflightDebug("Failed to record filter_diagnostics follow-up",
		store.RecordFilterDiagnosticsFollowed(db))
}

// RecordAvailabilityBlock counts one policy block by its structured reason key.
// Reasons outside the closed enum are folded into "other" by the store.
func (s *Service) RecordAvailabilityBlock(reason string) {
	store, db := s.preflightSink()
	if store == nil {
		return
	}
	s.preflightDebug("Failed to record availability block",
		store.RecordAvailabilityBlock(db, reason))
}

// RecordDiscoveryOmission counts one retrieve_tools response that withheld
// locked/quarantined matches from the caller.
func (s *Service) RecordDiscoveryOmission() {
	store, db := s.preflightSink()
	if store == nil {
		return
	}
	s.preflightDebug("Failed to record discovery omission",
		store.RecordDiscoveryOmission(db))
}

// SetConfiguredIDECountProvider wires a function that returns the number of
// IDE client config files mcpproxy has registered itself into (Spec 044).
// Typically supplied by internal/connect.Service.
func (s *Service) SetConfiguredIDECountProvider(fn func() int) {
	s.configuredIDECountProvider = fn
}

// SetAutostartReader overrides the default autostart reader (for tests). In
// production the first heartbeat lazy-initializes DefaultAutostartReader.
func (s *Service) SetAutostartReader(r *AutostartReader) {
	s.autostartReader = r
}

// SetOnboardingProvider wires a function that returns an onboarding-funnel
// snapshot for the next heartbeat (Spec 046). Each call should return fresh
// data — connected-client count and wizard engagement state both change
// between heartbeats. Returning nil omits the onboarding fields entirely.
func (s *Service) SetOnboardingProvider(fn func() *OnboardingSnapshot) {
	s.onboardingProvider = fn
}

// resolveLaunchSource returns the LaunchSource to emit in the current
// heartbeat. Precedence:
//  1. If the activation bucket's installer_heartbeat_pending flag is true,
//     emit "installer" and clear the flag (one-shot). This handles crash-
//     recovery between installer-driven startup and the first successful
//     heartbeat.
//  2. Otherwise, emit the cached DetectLaunchSourceOnce() result.
//
// Any BBolt error while inspecting/clearing the flag is logged at debug and
// falls through to the runtime detector — this preserves liveness of the
// heartbeat pipeline at the cost of losing the "installer" classification
// for this one cycle.
func (s *Service) resolveLaunchSource() LaunchSource {
	if s.activationStore != nil && s.activationDB != nil {
		pending, err := s.activationStore.IsInstallerPending(s.activationDB)
		if err == nil && pending {
			// Clear the flag synchronously so a crash before the HTTP POST
			// still downgrades the next heartbeat to the runtime-detected
			// source, rather than re-emitting "installer" forever.
			if clearErr := s.activationStore.SetInstallerPending(s.activationDB, false); clearErr != nil {
				s.logger.Debug("Failed to clear installer_heartbeat_pending", zap.Error(clearErr))
			}
			return LaunchSourceInstaller
		}
		if err != nil {
			s.logger.Debug("Failed to read installer_heartbeat_pending", zap.Error(err))
		}
	}
	return DetectLaunchSourceOnce()
}

// Start begins the telemetry heartbeat loop. This is a blocking call; run in a goroutine.
// Single-shot: a second Start is refused, and a Start that lost the race
// against Stop (fast shutdown) is a no-op. See Stop.
func (s *Service) Start(ctx context.Context) {
	// Registration under startMu (Spec 080 FR-010, review round 6). If Stop
	// already ran — production launches Start via `go` (lifecycle.go), so a
	// fast shutdown can beat this goroutine — the service is terminally
	// stopped: return without entering a loop that could write BBolt after
	// Runtime.Close began the shutdown-marker path. Refuse a second Start:
	// the done bookkeeping is single-shot.
	s.startMu.Lock()
	if s.stopped {
		s.startMu.Unlock()
		s.logger.Debug("Telemetry service Start called after Stop; not starting")
		return
	}
	if s.started {
		s.startMu.Unlock()
		s.logger.Warn("Telemetry service Start called twice; ignoring")
		return
	}
	s.started = true
	s.startMu.Unlock()
	// Every exit below — including the disabled early returns — must release
	// a waiting Stop.
	defer close(s.done)

	// Spec 042: env vars override config. DO_NOT_TRACK / CI / MCPPROXY_TELEMETRY=false
	if s.envDisabledReason != EnvDisabledNone {
		s.logger.Info("Telemetry disabled by environment variable",
			zap.String("reason", string(s.envDisabledReason)))
		return
	}

	// Skip if telemetry is disabled. Resolved under s.mu (telemetryEnabledLive):
	// Start runs on its own goroutine, so both the s.config pointer read and the
	// cfg.Telemetry dereference behind it race the config-reload path.
	if !s.telemetryEnabledLive() {
		s.logger.Info("Telemetry disabled by configuration")
		return
	}

	// Skip for non-semver (dev) builds
	if !isValidSemver(s.version) {
		s.logger.Info("Telemetry disabled for non-semver version",
			zap.String("version", s.version))
		return
	}

	// Ensure anonymous ID exists
	s.ensureAnonymousID()

	// Spec 044 (T025): populate runtime-detected blocked values (hostname,
	// username, sensitive env var values) so the anonymity scanner can catch
	// leaks before they leave the machine. Idempotent.
	PopulateBlockedValues()

	// Every emission gate above has passed and the anonymous id exists, so a
	// heartbeat from this process is legitimate from here on. Arm the
	// graceful-shutdown flush (see flushFinalHeartbeat): even if the process
	// exits before the initial delay elapses, whatever it recorded is worth one
	// bounded final send.
	s.flushEligible.Store(true)

	s.logger.Info("Telemetry service starting",
		zap.String("endpoint", s.liveEndpoint()),
		zap.Duration("initial_delay", s.initialDelay),
		zap.Duration("interval", s.heartbeatInterval))

	// Wait initial delay (avoid noise from short-lived processes)
	select {
	case <-time.After(s.initialDelay):
	case <-ctx.Done():
		s.logger.Info("Telemetry service stopped during initial delay")
		return
	}

	// Send first heartbeat
	s.sendHeartbeat(ctx)

	// Then send every interval
	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.sendHeartbeat(ctx)
		case <-ctx.Done():
			s.logger.Info("Telemetry service stopped")
			return
		}
	}
}

// Stop terminally stops the heartbeat loop and waits for it to exit,
// including any in-flight tick — buildHeartbeat's BBolt writes and
// sendHeartbeat's HTTP send. The send carries the loop context, so the
// runtime's appCancel aborts it promptly and the wait is bounded by the HTTP
// client's timeout; the BBolt writes are never interrupted mid-transaction,
// only awaited. Callers must cancel the context passed to Start before (or
// concurrently with) Stop, or Stop blocks until the loop exits on its own.
// Stop returns immediately when Start never ran, is idempotent, and makes a
// Start goroutine that has not yet been scheduled a permanent no-op —
// Runtime.Close relies on this so no telemetry BBolt write can land after
// the clean-shutdown marker resolves or after the DB closes (Spec 080
// FR-010, review round 6).
//
// After the loop has been joined, Stop performs ONE bounded final heartbeat
// (flushFinalHeartbeat) so counters recorded since the last accepted send are
// not lost on exit. Doing it AFTER the join — not from a second goroutine
// racing the loop — is what keeps the "the loop owns sends" invariant intact:
// at that point there is provably no other sender, so the send+reset path
// cannot interleave with a tick. Runtime.Close calls Stop while the BBolt
// handle is still open and before the clean-shutdown marker resolves, so the
// flush's buildHeartbeat writes are still legal there.
func (s *Service) Stop() {
	s.startMu.Lock()
	s.stopped = true
	started := s.started
	s.startMu.Unlock()
	if !started {
		return
	}
	// Start ran (or is running): its defer guarantees done closes on every
	// exit path, including the disabled-by-env/config early returns.
	<-s.done

	s.flushFinalHeartbeat()
}

// flushFinalHeartbeat sends at most one final heartbeat on the graceful
// shutdown path. Called by Stop AFTER the heartbeat loop has exited, so it is
// never concurrent with a tick.
//
// It reuses sendHeartbeat's send+reset path verbatim, which is what preserves
// the counter contract: counters are zeroed ONLY on a 2xx, so a failed or
// timed-out flush drops nothing (the next run reports the same counts) and an
// accepted flush cannot double-count with a later start.
//
// Skipped when: Start never armed it (telemetry disabled by env/config, dev
// build), the user opted out, telemetry was turned off mid-run, or nothing has
// been recorded since the last accepted send.
//
// The send runs on a FRESH context, not the loop's: Runtime.Close cancels the
// app context before calling Stop, so a derived context would abort the flush
// instantly. It is hard-bounded by flushTimeout so a dead endpoint cannot hang
// shutdown.
//
// Anonymous-id rotation is suppressed here (rotateAnonymousID=false): rotation
// rewrites the whole config file, and shutdown — with the daemon's other config
// writers winding down — is the worst moment to take that risk for a once-a-year
// housekeeping step. The next run's first heartbeat rotates instead.
func (s *Service) flushFinalHeartbeat() {
	s.flushOnce.Do(func() {
		if !s.flushEligible.Load() {
			return
		}
		if s.optedOut.Load() {
			return
		}
		if !s.telemetryEnabledLive() {
			return
		}
		if !s.registry.HasPendingCounters() {
			s.logger.Debug("Skipping telemetry shutdown flush: no counters recorded since the last accepted heartbeat")
			return
		}

		timeout := s.flushTimeout
		if timeout <= 0 {
			timeout = shutdownFlushTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		s.logger.Debug("Flushing final telemetry heartbeat on shutdown",
			zap.Duration("timeout", timeout))
		s.sendHeartbeatWithRotation(ctx, false)
	})
}

func (s *Service) sendHeartbeat(ctx context.Context) {
	s.sendHeartbeatWithRotation(ctx, true)
}

// sendHeartbeatWithRotation is sendHeartbeat's body. rotateAnonymousID is false
// only on the shutdown-flush path; see flushFinalHeartbeat.
func (s *Service) sendHeartbeatWithRotation(ctx context.Context, rotateAnonymousID bool) {
	// MCP-2482: once the user has opted out, no further telemetry is emitted —
	// even if the long-running heartbeat loop is still ticking.
	if s.optedOut.Load() {
		return
	}

	payload := s.buildHeartbeatWithRotation(rotateAnonymousID)

	data, err := json.Marshal(payload)
	if err != nil {
		s.logger.Debug("Failed to marshal heartbeat", zap.Error(err))
		return
	}

	// Spec 044 (FR-011): defense-in-depth anonymity scanner. Runs on the
	// serialized payload before the HTTP POST. On violation: log at error
	// level (WITHOUT the payload — that would leak the very thing we caught),
	// increment the counter, and skip the heartbeat. This catches regressions
	// where a future contributor accidentally widens a field to carry PII.
	if err := ScanForPII(data); err != nil {
		if s.registry != nil {
			s.registry.RecordAnonymityViolation()
		}
		var v *AnonymityViolation
		if errors.As(err, &v) {
			s.logger.Error("telemetry anonymity violation (not transmitted)",
				zap.String("rule", v.Rule),
				zap.String("pattern", v.Pattern))
		} else {
			s.logger.Error("telemetry anonymity violation (not transmitted)",
				zap.Error(err))
		}
		return
	}

	url := s.liveEndpoint() + "/heartbeat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		s.logger.Debug("Failed to create heartbeat request", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// MCP-2482: re-check the opt-out latch immediately before transmit. The
	// entry check above can pass for a heartbeat already in flight when the user
	// opts out mid-build; without this second check that heartbeat would still
	// ship a full usage payload AFTER the opt-out. No usage data leaves once the
	// latch is set.
	if s.optedOut.Load() {
		return
	}

	resp, err := s.client.Do(req)
	if err != nil {
		s.logger.Debug("Failed to send heartbeat", zap.Error(err))
		return
	}
	defer resp.Body.Close()

	s.logger.Debug("Heartbeat sent", zap.Int("status", resp.StatusCode))

	// Spec 042: only on a successful 2xx send do we (a) reset counters and
	// (b) advance the upgrade funnel cursor. Failures preserve state for retry.
	if resp.StatusCode/100 == 2 {
		s.registry.Reset()
		s.advanceUpgradeFunnel()
	}
}

// advanceUpgradeFunnel persists the current version as last_reported_version.
// Called only on successful heartbeat send. Reads the live config through
// liveConfig for the same reason buildHeartbeat does: this runs on the
// heartbeat loop, concurrently with the NotifyConfigChanged pointer swap.
//
// The cursor is NOT self-healing on a skipped write: leaving it unadvanced
// makes the next heartbeat report the same previous_version again, so one
// upgrade is counted twice. If the config was swapped between the read and the
// guarded write, redo the advance against the new live config. Two attempts is
// enough — a second swap in the same window leaves the cursor for the next
// heartbeat, which is the pre-existing (rare, bounded) double-report.
func (s *Service) advanceUpgradeFunnel() {
	for attempt := 0; attempt < 2; attempt++ {
		if s.advanceUpgradeFunnelOnce() {
			return
		}
	}
}

// advanceUpgradeFunnelOnce runs ONE resolve -> check -> mutate -> persist pass
// and reports whether the cursor is settled (nothing to do, or the advance was
// written). It holds s.mu across the whole pass for two reasons:
//
//   - the mutation writes cfg.Telemetry, which buildHeartbeat reads (via
//     telemetryCursor) and maybeRotateAnonymousID writes. Mutating it outside
//     the mutex is a genuine data race with a concurrent BuildPayload — that
//     path is exported and served from an HTTP handler (internal/httpapi), so a
//     `telemetry show-payload` request lands on the heartbeat loop's post-send
//     advance. Proven under -race by TestAdvanceUpgradeFunnelConfigRace.
//   - the liveness check inside persistConfigLocked has to be atomic with the
//     mutation it is guarding, exactly as in maybeRotateAnonymousID.
func (s *Service) advanceUpgradeFunnelOnce() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.config
	if cfg == nil {
		return true
	}
	if cfg.Telemetry == nil {
		cfg.Telemetry = &config.TelemetryConfig{}
	}
	if cfg.Telemetry.LastReportedVersion == s.version {
		return true
	}
	cfg.Telemetry.LastReportedVersion = s.version
	return s.persistConfigLocked(cfg, "Advanced last_reported_version")
}

// BuildPayload renders the heartbeat payload at the current point in time.
// It is exported so the `mcpproxy telemetry show-payload` command can render
// the same payload that would next be sent, without making a network call.
func (s *Service) BuildPayload() HeartbeatPayload {
	return s.buildHeartbeat()
}

// liveConfig returns the service's current *config.Config, read under s.mu.
// NotifyConfigChanged replaces the pointer wholesale when the live config is
// reloaded, so every read outside that critical section must go through here:
// an unsynchronized read of the field is a data race with the swap.
//
// Callers get the pointer, not a deep copy — the pointed-to config is still
// shared with the rest of the daemon and must be treated as read-only.
func (s *Service) liveConfig() *config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config
}

// liveEndpoint returns the service's current telemetry endpoint, read under
// s.mu. NotifyConfigChanged rewrites s.endpoint on a live config reload, so
// every read must take the same lock the write does — an unlocked read is a
// data race, not merely a stale value. The shutdown flush made this reachable in
// practice (it sends after the loop has been joined, i.e. from whichever
// goroutine called Stop), and it is equally reachable from the feedback and
// opt-out-beacon paths.
func (s *Service) liveEndpoint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endpoint
}

// telemetryCursor reads the four cfg.Telemetry scalars the heartbeat reports,
// in one hold of s.mu. Every writer of these fields — maybeRotateAnonymousID
// and advanceUpgradeFunnelOnce — mutates them under the same mutex, so the read
// side must take it too: a locked write paired with an unlocked read is still a
// data race. Taking all four in one pass also means the reported id and its
// created_at can never straddle a rotation.
//
// cfg is the caller's snapshot (see liveConfig); a nil cfg or nil cfg.Telemetry
// yields empty strings, which is what a fresh install reports anyway.
func (s *Service) telemetryCursor(cfg *config.Config) (anonID, createdAt, previousVersion, lastStartupOutcome string) {
	if cfg == nil {
		return "", "", "", ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cfg.Telemetry == nil {
		return "", "", "", ""
	}
	return cfg.Telemetry.AnonymousID,
		cfg.Telemetry.AnonymousIDCreatedAt,
		cfg.Telemetry.LastReportedVersion,
		cfg.Telemetry.LastStartupOutcome
}

func (s *Service) buildHeartbeat() HeartbeatPayload {
	return s.buildHeartbeatWithRotation(true)
}

// buildHeartbeatWithRotation is buildHeartbeat's body. rotateAnonymousID is
// false only on the shutdown-flush path, which must not rewrite the config file
// while the daemon is tearing down; see flushFinalHeartbeat.
func (s *Service) buildHeartbeatWithRotation(rotateAnonymousID bool) HeartbeatPayload {
	// Take ONE snapshot of the live config pointer under s.mu and read only that
	// below. NotifyConfigChanged swaps s.config wholesale on a reload, so an
	// unsynchronized read of the field races that swap; snapshotting also means a
	// reload landing mid-build cannot splice fields from two different configs
	// into one payload. The lock is released immediately — nothing below may hold
	// it, because preflightSink() takes the same non-reentrant mutex.
	cfg := s.liveConfig()

	// Spec 042: rotate the anonymous ID if it's older than 365 days. Runs on the
	// snapshot, so the rotated ID is the one this payload reports.
	if rotateAnonymousID {
		s.maybeRotateAnonymousID(cfg, time.Now().UTC())
	}

	// Read every cfg.Telemetry-derived scalar in ONE locked pass. These four
	// fields are written under s.mu by maybeRotateAnonymousID (anonymous id +
	// created_at) and advanceUpgradeFunnelOnce (last_reported_version), both of
	// which can run on another goroutine while this payload is being built —
	// reading them unlocked is a data race, not merely a torn view.
	anonID, anonCreatedAt, prevVersion, lastStartupOutcome := s.telemetryCursor(cfg)

	payload := HeartbeatPayload{
		AnonymousID:    anonID,
		Version:        s.version,
		Edition:        s.edition,
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		GoVersion:      runtime.Version(),
		UptimeHours:    int(time.Since(s.startTime).Hours()),
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		SchemaVersion:  SchemaVersion,
		CurrentVersion: s.version,
		// Schema v6: stable, non-reversible machine-id hash. Cached after the
		// first call so repeated heartbeats do not re-probe the OS. Empty when
		// the OS machine id is unreadable — never blocks the heartbeat.
		MachineID: resolveMachineID(),
	}

	payload.AnonymousIDCreatedAt = anonCreatedAt
	payload.PreviousVersion = prevVersion
	payload.LastStartupOutcome = lastStartupOutcome

	if s.stats != nil {
		payload.ServerCount = s.stats.GetServerCount()
		payload.ConnectedServerCount = s.stats.GetConnectedServerCount()
		payload.ToolCount = s.stats.GetToolCount()
		payload.RoutingMode = s.stats.GetRoutingMode()
		payload.QuarantineEnabled = s.stats.IsQuarantineEnabled()
		// Schema v3 additions — forwarded from runtime wiring.
		payload.ServerDockerIsolatedCount = s.stats.GetDockerIsolatedServerCount()
	}

	// Spec 042: feature-flag snapshot. Schema v3: BuildFeatureFlagSnapshot
	// does not probe Docker — we splice the runtime probe result in here
	// so the snapshot helper stays cheap and side-effect-free.
	payload.FeatureFlags = BuildFeatureFlagSnapshot(cfg)
	if s.stats != nil && payload.FeatureFlags != nil {
		payload.FeatureFlags.DockerAvailable = s.stats.IsDockerAvailable()
		// Schema v5 (MCP-2745): coarse docker-CLI resolution branch (the #696
		// fleet signal). Resolution is a runtime concern, so it is spliced in
		// here rather than in the side-effect-free BuildFeatureFlagSnapshot.
		payload.FeatureFlags.DockerCLISource = s.stats.GetDockerCLISource()
	}

	// Schema v3: fixed-key per-protocol counter over cfg.Servers. Logs
	// unknown values at debug level via the service logger (bucketed into
	// "auto") so operators can spot mis-typed config without polluting the
	// telemetry cardinality.
	payload.ServerProtocolCounts = buildServerProtocolCountsWithLogger(cfg, s.logger)

	// Schema v9: fixed-key histogram over cfg.Servers by EffectiveTrustMode.
	// Computed fresh each heartbeat so a mid-window trust-tier change is
	// reflected on the next send (state, not a delta counter).
	payload.TrustModeDistribution = buildTrustModeDistribution(cfg)

	// Spec 044: ground-truth environment classification. Cached after first
	// call so repeated heartbeats do not re-probe the filesystem.
	envKind, envMarkers := DetectEnvKindOnce()
	payload.EnvKind = string(envKind)
	// Copy to a local so the omitempty pointer can be distinguished from the
	// zero-value struct (data-model.md requires this).
	markersCopy := envMarkers
	payload.EnvMarkers = &markersCopy

	// Spec 044: activation funnel snapshot. Load from BBolt (decay applied
	// at read time); on any load error, omit the field rather than blocking
	// the heartbeat.
	if s.activationStore != nil && s.activationDB != nil {
		if st, err := s.activationStore.Load(s.activationDB); err == nil {
			// Splice in the configured-IDE count from the external provider.
			if s.configuredIDECountProvider != nil {
				st.ConfiguredIDECount = s.configuredIDECountProvider()
			}
			// Ensure the bucket string is always populated (Load already does
			// this, but be defensive for forward-compat).
			if st.EstimatedTokensSaved24hBucket == "" {
				st.EstimatedTokensSaved24hBucket = BucketTokens(0)
			}
			payload.Activation = &st
		} else {
			s.logger.Debug("Failed to load activation state for heartbeat", zap.Error(err))
		}
	}

	// Spec 044 (T051): LaunchSource. One-shot "installer" override consumes
	// the installer_heartbeat_pending flag set at process startup when
	// MCPPROXY_LAUNCHED_BY=installer was observed. Otherwise the runtime
	// detector result (tray/login_item/cli/unknown) is emitted.
	payload.LaunchSource = string(s.resolveLaunchSource())

	// Spec 044 (T051): AutostartEnabled. Tri-state; nil when the tray sidecar
	// is absent/unreachable/malformed (Linux always falls here).
	if s.autostartReader != nil {
		payload.AutostartEnabled = s.autostartReader.Read()
	} else {
		// Lazy-init the default reader on first heartbeat. The reader is
		// safe to reuse across heartbeats (1h TTL cache inside). It reads the
		// sidecar inside THIS instance's data directory: the tray writes it
		// into the same root it hands the core, and MCPPROXY_HOME moves both
		// (GH #936).
		dataDir := ""
		if cfg != nil {
			dataDir = cfg.DataDir
		}
		s.autostartReader = AutostartReaderForDataDir(dataDir)
		payload.AutostartEnabled = s.autostartReader.Read()
	}

	// Spec 042: counter snapshot.
	if s.registry != nil {
		snap := s.registry.Snapshot()
		payload.SurfaceRequests = snap.SurfaceCounts
		payload.BuiltinToolCalls = snap.BuiltinToolCalls
		payload.UpstreamToolCallCountBucket = snap.UpstreamToolCallCountBucket
		payload.RESTEndpointCalls = snap.RESTEndpointCalls
		payload.ErrorCategoryCounts = snap.ErrorCategoryCounts
		payload.DoctorChecks = snap.DoctorChecks
		// Schema v8: security-scanner counters. nil (and therefore omitted)
		// when the install never completed or failed a scan in the window.
		payload.TPAScanner = snap.TPAScannerStats()
	}

	// Spec 046: onboarding funnel snapshot. Provider closes over connect.Service
	// (for the connected-client count + IDs) and the BBolt-backed onboarding
	// state (for wizard engagement + per-step status). nil-safe.
	if s.onboardingProvider != nil {
		if snap := s.onboardingProvider(); snap != nil {
			payload.ConnectedClientCount = snap.ConnectedClientCount
			payload.ConnectedClientIDs = snap.ConnectedClientIDs
			payload.WizardEngaged = snap.WizardEngaged
			payload.WizardConnectStep = snap.WizardConnectStep
			payload.WizardServerStep = snap.WizardServerStep
			// Spec 080 (FR-005): shown-vs-engaged independence — true once
			// the wizard rendered, regardless of engagement.
			payload.WizardShown = snap.WizardShown
		}
	}

	// Spec 080 (US2): funnel observability. Mark the current UTC day active
	// (a heartbeat is proof of process activity), then surface the reduced
	// integers. On any store error the fields are simply omitted — the
	// heartbeat is never blocked (same posture as Activation).
	if s.funnelStore != nil && s.funnelDB != nil {
		now := time.Now().UTC()
		if err := s.funnelStore.RecordActivity(s.funnelDB, now); err != nil {
			s.logger.Debug("Failed to record funnel activity day", zap.Error(err))
		}
		if st, err := s.funnelStore.Snapshot(s.funnelDB, now); err == nil {
			payload.WebUIOpened = st.WebUIOpened
			if st.HasInstallDay {
				days := st.DaysSinceInstall
				payload.DaysSinceInstall = &days
			}
			payload.ActiveDays30d = st.ActiveDays30d
		} else {
			s.logger.Debug("Failed to load funnel state for heartbeat", zap.Error(err))
		}
	}

	// Spec 080 (US3): pre-churn snapshot. previous_shutdown was derived once
	// at startup and never changes for this instance (FR-011); the empty
	// (unknown / store-not-wired) value is dropped by omitempty (FR-013).
	// last_error_code is re-read each heartbeat so the field always carries
	// the MOST RECENT stable MCPX_* code (FR-012); on any store error the
	// field is simply omitted — the heartbeat is never blocked.
	payload.PreviousShutdown = s.previousShutdown
	if s.prechurnStore != nil && s.prechurnDB != nil {
		if code, err := s.prechurnStore.LastErrorCode(s.prechurnDB); err == nil {
			payload.LastErrorCode = code
		} else {
			s.logger.Debug("Failed to load last_error_code for heartbeat", zap.Error(err))
		}
	}

	// Spec 044 Phase H: diagnostics counter snapshot. Load from BBolt (decay
	// applied at read time); omit entirely when counters are all zero or the
	// store is not wired (short-lived CLI commands).
	if s.diagCounterStore != nil && s.diagCounterDB != nil {
		if snap, err := s.diagCounterStore.Snapshot(s.diagCounterDB); err == nil {
			if !snap.isZero() {
				payload.Diagnostics = &snap
			}
		} else {
			s.logger.Debug("Failed to load diagnostics counters for heartbeat", zap.Error(err))
		}
	}

	// Issue #969 (Phase 0): preflight baseline counters. Same flush shape as
	// the Phase H diagnostics block above — load from BBolt (decay applied at
	// read time), omit entirely when all counters are zero or the store is not
	// wired (short-lived CLI commands).
	if s.preflightStore != nil && s.preflightDB != nil {
		if snap, err := s.preflightStore.Snapshot(s.preflightDB); err == nil {
			if !snap.isZero() {
				payload.Preflight = &snap
			}
		} else {
			s.logger.Debug("Failed to load preflight counters for heartbeat", zap.Error(err))
		}
	}

	return payload
}

// ensureAnonymousID gives the install an anonymous id, generating and
// persisting one on first run. Start() launches on its own goroutine
// (runtime/lifecycle.go `go r.telemetryService.Start(...)`), so this runs
// concurrently with the daemon's config-reload path: reading s.config unlocked
// races NotifyConfigChanged's pointer swap, and saving it directly would write
// a whole config file that may already be stale. Both hazards are handled the
// same way as maybeRotateAnonymousID — one locked pass, persisted only while
// the snapshot is still live — retried once if the pointer moved underneath.
func (s *Service) ensureAnonymousID() {
	for attempt := 0; attempt < 2; attempt++ {
		if s.ensureAnonymousIDOnce() {
			return
		}
	}
}

// ensureAnonymousIDOnce runs ONE locked resolve -> check -> mutate -> persist
// pass and reports whether the id is settled (already present, or written).
// It returns false only when the live config was swapped out from under the
// snapshot, which is the caller's cue to redo the work against the new one.
func (s *Service) ensureAnonymousIDOnce() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.config
	if cfg == nil {
		return true
	}
	if cfg.Telemetry == nil {
		cfg.Telemetry = &config.TelemetryConfig{}
	}

	if cfg.Telemetry.AnonymousID != "" {
		// Spec 042: legacy installs need created_at initialized for rotation.
		if cfg.Telemetry.AnonymousIDCreatedAt != "" {
			return true
		}
		cfg.Telemetry.AnonymousIDCreatedAt = time.Now().UTC().Format(time.RFC3339)
		if !s.persistConfigLocked(cfg, "Initialized anonymous_id_created_at for legacy install") && s.config != cfg {
			cfg.Telemetry.AnonymousIDCreatedAt = ""
			return false
		}
		return true
	}

	newID := uuid.New().String()
	cfg.Telemetry.AnonymousID = newID
	cfg.Telemetry.AnonymousIDCreatedAt = time.Now().UTC().Format(time.RFC3339)

	if s.persistConfigLocked(cfg, "Generated anonymous telemetry ID") {
		s.logger.Info("Generated and persisted anonymous telemetry ID",
			zap.String("id", newID))
		return true
	}
	if s.config != cfg {
		// The live config moved on: this id is neither on disk nor in the
		// config the daemon now reads. Undo it so the snapshot cannot hand out
		// an identity nothing else will ever agree with, and retry against the
		// new live config.
		cfg.Telemetry.AnonymousID = ""
		cfg.Telemetry.AnonymousIDCreatedAt = ""
		return false
	}
	// A genuine write failure on the still-live config. Keep the in-memory id
	// (pre-existing behaviour) so this process at least reports one stable
	// identity for its lifetime.
	s.logger.Warn("Failed to persist anonymous telemetry ID; continuing with in-memory id",
		zap.String("id", newID))
	return true
}

// maybeRotateAnonymousID rotates the anonymous ID once it's older than 365
// days. Spec 042 (User Story 8). Clock skew (created_at in the future) is
// treated as "not yet expired".
//
// cfg is passed in rather than read off s.config so the caller's snapshot is
// the one mutated and persisted: buildHeartbeat resolves the live config once
// (liveConfig) and everything downstream — this rotation included — must act on
// that same pointer, or a config swap landing mid-heartbeat would rotate the ID
// on a config the payload never read.
//
// The ENTIRE check -> generate -> mutate -> persist-or-rollback sequence runs
// under s.mu, so it is atomic against both the NotifyConfigChanged pointer swap
// and a second rotation racing on the same snapshot. That second racer is real,
// not theoretical: BuildPayload is exported and served from an HTTP handler
// (internal/httpapi), so a request can build a heartbeat alongside the loop.
// With two rotations interleaving, one could capture the OTHER's freshly
// generated id as its "previous" value and restore that on rollback, leaving a
// never-persisted id in the config the payload then transmits — exactly the
// identity fragmentation the rollback exists to prevent. Serializing makes the
// loser observe the winner's refreshed created_at and do nothing.
func (s *Service) maybeRotateAnonymousID(cfg *config.Config, now time.Time) {
	if cfg == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if cfg.Telemetry == nil || cfg.Telemetry.AnonymousID == "" {
		return
	}
	createdAtStr := cfg.Telemetry.AnonymousIDCreatedAt
	if createdAtStr == "" {
		// Legacy install — initialize without rotating.
		cfg.Telemetry.AnonymousIDCreatedAt = now.Format(time.RFC3339)
		s.persistConfigLocked(cfg, "Initialized anonymous_id_created_at")
		return
	}
	createdAt, err := time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		// Corrupt timestamp: reset to now without rotating.
		cfg.Telemetry.AnonymousIDCreatedAt = now.Format(time.RFC3339)
		s.persistConfigLocked(cfg, "Reset corrupt anonymous_id_created_at")
		return
	}
	if !createdAt.Before(now) {
		// Future timestamp (clock skew) — do not rotate.
		return
	}
	if now.Sub(createdAt) <= 365*24*time.Hour {
		return
	}

	// Rotate. The caller reports cfg's anonymous_id in the payload it is
	// building, so the rotation must be all-or-nothing: an id that was never
	// written to disk must never be transmitted, or one annual rotation would
	// show up as TWO identities (this heartbeat's unpersisted id, then the id
	// the next heartbeat rotates the live config to) and fragment the install's
	// telemetry continuity.
	prevID := cfg.Telemetry.AnonymousID
	prevCreatedAt := cfg.Telemetry.AnonymousIDCreatedAt
	cfg.Telemetry.AnonymousID = uuid.New().String()
	cfg.Telemetry.AnonymousIDCreatedAt = now.Format(time.RFC3339)
	if !s.persistConfigLocked(cfg, "Rotated anonymous_id (annual)") {
		// The live config was swapped out from under this snapshot, so the new
		// id is not on disk. Put the snapshot back the way we found it and let
		// the next heartbeat rotate the live config instead.
		cfg.Telemetry.AnonymousID = prevID
		cfg.Telemetry.AnonymousIDCreatedAt = prevCreatedAt
	}
}

// persistConfig writes cfg to disk, but ONLY while cfg is still the service's
// live config. It reports whether the write actually happened, so callers can
// undo or retry a mutation that was never persisted.
//
// This writes the WHOLE config file, so a write must never be issued against a
// pointer the daemon has already swapped out: the heartbeat path works from a
// snapshot (liveConfig), and if NotifyConfigChanged installs a newer config
// while that heartbeat is in flight, saving the snapshot would silently roll
// the user's change back on disk. The liveness check and the write share one
// s.mu hold so the swap cannot slip between them.
//
// KNOWN RESIDUAL WINDOW (pre-existing, not closed here): the daemon's config
// writers save the new file BEFORE calling NotifyConfigChanged, so between
// those two steps s.config still points at the old config and a write issued
// here would pass the liveness check and land on top of the just-saved file.
// Closing that needs single-writer ownership of the config file (or a
// mtime/CAS check), which is a config-layer change, not a telemetry one. The
// guard narrows the exposure to that gap; it does not eliminate it.
func (s *Service) persistConfig(cfg *config.Config, reason string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistConfigLocked(cfg, reason)
}

// persistConfigLocked is persistConfig's body. The caller MUST already hold
// s.mu. It exists so a caller whose whole check-mutate-persist sequence has to
// be atomic — maybeRotateAnonymousID, which must not interleave with another
// rotation — can hold the lock across all of it instead of reacquiring here
// (s.mu is not reentrant).
func (s *Service) persistConfigLocked(cfg *config.Config, reason string) bool {
	if cfg == nil {
		return false
	}
	if s.cfgPath == "" {
		// No config FILE backs this service (in-memory/CLI use). There is no
		// on-disk state for the in-memory mutation to be inconsistent with, so
		// report success: this is "nothing to persist", not a failed write, and
		// callers must not roll their mutation back.
		return true
	}
	if s.config != cfg {
		s.logger.Debug("Skipped telemetry config persist: live config was swapped",
			zap.String("reason", reason))
		return false
	}
	if err := config.SaveConfig(cfg, s.cfgPath); err != nil {
		s.logger.Debug("Failed to persist telemetry config", zap.String("reason", reason), zap.Error(err))
		return false
	}
	s.logger.Debug("Persisted telemetry config", zap.String("reason", reason))
	return true
}

// IsValidSemverVersion reports whether a build version is a released (semver)
// build rather than a dev build. It is the EXACT check the heartbeat and
// opt-out beacon gates apply, exported so callers outside this package
// (e.g. the update-failure recording seam, spec 095) evaluate the dev-build
// gate identically instead of reimplementing it.
func IsValidSemverVersion(v string) bool {
	return isValidSemver(v)
}

// isValidSemver checks if the version string is a valid semantic version.
func isValidSemver(v string) bool {
	if v == "" {
		return false
	}
	// semver.IsValid requires "v" prefix
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return semver.IsValid(v)
}

// normalizeVersion ensures semver version strings carry a leading "v" prefix.
//
// Official mcpproxy releases embed versions like "v0.22.0", but third-party
// builds (e.g. custom Dockerfiles using `--build-arg VERSION=0.22.0`) drop the
// prefix. Without normalization, the telemetry dashboard shows both forms as
// separate rows. We normalize on the emit side so both collapse into one.
//
// Rules:
//   - Empty string is returned unchanged.
//   - If the string is already a valid semver with "v" prefix, returned unchanged.
//   - If the string becomes a valid semver once prefixed, the prefixed form is returned.
//   - Otherwise (not a valid semver at all, e.g. "dev"), returned unchanged so that
//     downstream isValidSemver filtering still rejects it and debug logs retain the
//     original garbage value.
func normalizeVersion(v string) string {
	if v == "" {
		return v
	}
	if strings.HasPrefix(v, "v") {
		return v
	}
	if semver.IsValid("v" + v) {
		return "v" + v
	}
	return v
}
