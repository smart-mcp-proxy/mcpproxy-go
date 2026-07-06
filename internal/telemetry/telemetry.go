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
const SchemaVersion = 6

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
}

// optOutBeaconTimeout bounds the best-effort opt-out beacon send so a slow or
// unreachable endpoint never delays the config save that triggered it.
const optOutBeaconTimeout = 5 * time.Second

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
		resolvedEnabled:   EffectiveTelemetryEnabled(cfg),
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
func (s *Service) Start(ctx context.Context) {
	// Spec 042: env vars override config. DO_NOT_TRACK / CI / MCPPROXY_TELEMETRY=false
	if s.envDisabledReason != EnvDisabledNone {
		s.logger.Info("Telemetry disabled by environment variable",
			zap.String("reason", string(s.envDisabledReason)))
		return
	}

	// Skip if telemetry is disabled
	if !s.config.IsTelemetryEnabled() {
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

	s.logger.Info("Telemetry service starting",
		zap.String("endpoint", s.endpoint),
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

func (s *Service) sendHeartbeat(ctx context.Context) {
	// MCP-2482: once the user has opted out, no further telemetry is emitted —
	// even if the long-running heartbeat loop is still ticking.
	if s.optedOut.Load() {
		return
	}

	payload := s.buildHeartbeat()

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

	url := s.endpoint + "/heartbeat"
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
// Called only on successful heartbeat send.
func (s *Service) advanceUpgradeFunnel() {
	if s.config.Telemetry == nil {
		s.config.Telemetry = &config.TelemetryConfig{}
	}
	if s.config.Telemetry.LastReportedVersion == s.version {
		return
	}
	s.config.Telemetry.LastReportedVersion = s.version
	if s.cfgPath != "" {
		if err := config.SaveConfig(s.config, s.cfgPath); err != nil {
			s.logger.Debug("Failed to persist last_reported_version", zap.Error(err))
		}
	}
}

// BuildPayload renders the heartbeat payload at the current point in time.
// It is exported so the `mcpproxy telemetry show-payload` command can render
// the same payload that would next be sent, without making a network call.
func (s *Service) BuildPayload() HeartbeatPayload {
	return s.buildHeartbeat()
}

func (s *Service) buildHeartbeat() HeartbeatPayload {
	// Spec 042: rotate the anonymous ID if it's older than 365 days.
	s.maybeRotateAnonymousID(time.Now().UTC())

	payload := HeartbeatPayload{
		AnonymousID:    s.config.GetAnonymousID(),
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

	if s.config.Telemetry != nil {
		payload.AnonymousIDCreatedAt = s.config.Telemetry.AnonymousIDCreatedAt
		payload.PreviousVersion = s.config.Telemetry.LastReportedVersion
		payload.LastStartupOutcome = s.config.Telemetry.LastStartupOutcome
	}

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
	payload.FeatureFlags = BuildFeatureFlagSnapshot(s.config)
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
	payload.ServerProtocolCounts = buildServerProtocolCountsWithLogger(s.config, s.logger)

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
		// safe to reuse across heartbeats (1h TTL cache inside).
		s.autostartReader = DefaultAutostartReader()
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

	return payload
}

func (s *Service) ensureAnonymousID() {
	if s.config.GetAnonymousID() != "" {
		// Spec 042: legacy installs need created_at initialized for rotation.
		if s.config.Telemetry != nil && s.config.Telemetry.AnonymousIDCreatedAt == "" {
			s.config.Telemetry.AnonymousIDCreatedAt = time.Now().UTC().Format(time.RFC3339)
			s.persistConfig("Initialized anonymous_id_created_at for legacy install")
		}
		return
	}

	// Generate a new UUIDv4
	newID := uuid.New().String()

	// Persist to config
	if s.config.Telemetry == nil {
		s.config.Telemetry = &config.TelemetryConfig{}
	}
	s.config.Telemetry.AnonymousID = newID
	s.config.Telemetry.AnonymousIDCreatedAt = time.Now().UTC().Format(time.RFC3339)

	// Save config to disk
	if s.cfgPath != "" {
		if err := config.SaveConfig(s.config, s.cfgPath); err != nil {
			s.logger.Warn("Failed to persist anonymous telemetry ID",
				zap.Error(err))
		} else {
			s.logger.Info("Generated and persisted anonymous telemetry ID",
				zap.String("id", newID))
		}
	}
}

// maybeRotateAnonymousID rotates the anonymous ID once it's older than 365
// days. Spec 042 (User Story 8). Clock skew (created_at in the future) is
// treated as "not yet expired".
func (s *Service) maybeRotateAnonymousID(now time.Time) {
	if s.config.Telemetry == nil || s.config.Telemetry.AnonymousID == "" {
		return
	}
	createdAtStr := s.config.Telemetry.AnonymousIDCreatedAt
	if createdAtStr == "" {
		// Legacy install — initialize without rotating.
		s.config.Telemetry.AnonymousIDCreatedAt = now.Format(time.RFC3339)
		s.persistConfig("Initialized anonymous_id_created_at")
		return
	}
	createdAt, err := time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		// Corrupt timestamp: reset to now without rotating.
		s.config.Telemetry.AnonymousIDCreatedAt = now.Format(time.RFC3339)
		s.persistConfig("Reset corrupt anonymous_id_created_at")
		return
	}
	if !createdAt.Before(now) {
		// Future timestamp (clock skew) — do not rotate.
		return
	}
	if now.Sub(createdAt) <= 365*24*time.Hour {
		return
	}

	// Rotate.
	newID := uuid.New().String()
	s.config.Telemetry.AnonymousID = newID
	s.config.Telemetry.AnonymousIDCreatedAt = now.Format(time.RFC3339)
	s.persistConfig("Rotated anonymous_id (annual)")
}

func (s *Service) persistConfig(reason string) {
	if s.cfgPath == "" {
		return
	}
	if err := config.SaveConfig(s.config, s.cfgPath); err != nil {
		s.logger.Debug("Failed to persist telemetry config", zap.String("reason", reason), zap.Error(err))
		return
	}
	s.logger.Debug("Persisted telemetry config", zap.String("reason", reason))
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
