package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"strings"
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
// server_protocol_counts, and server_docker_isolated_count. Forward-compatible:
// existing v2 consumers simply ignore the new fields.
const SchemaVersion = 3

// HeartbeatPayload is the anonymous telemetry payload sent periodically.
// Spec 042 expanded the payload with Tier 2 fields; v1 fields are preserved.
type HeartbeatPayload struct {
	// v1 fields (preserved unchanged)
	AnonymousID          string `json:"anonymous_id"`
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

	// Spec 044: optional provider for configured IDE count. Populated by the
	// runtime from internal/connect at wire-up time. nil-safe.
	configuredIDECountProvider func() int

	// Spec 044 (T049): autostart reader for the tray-owned sidecar. Lazy-
	// initialized on first heartbeat; tests may inject a mock via
	// SetAutostartReader.
	autostartReader *AutostartReader

	// For testing: override initial delay and heartbeat interval
	initialDelay      time.Duration
	heartbeatInterval time.Duration
}

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
