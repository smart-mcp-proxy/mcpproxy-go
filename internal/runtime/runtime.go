package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cache"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/experiments"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/supervisor"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/server/tokens"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/shellwrap"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolsig"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/truncate"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
)

// Status captures high-level state for API consumers.
type Status struct {
	Phase         Phase                  `json:"phase"`
	Message       string                 `json:"message"`
	UpstreamStats map[string]interface{} `json:"upstream_stats"`
	ToolsIndexed  int                    `json:"tools_indexed"`
	LastUpdated   time.Time              `json:"last_updated"`
}

// Runtime owns the non-HTTP lifecycle for the proxy process.
type Runtime struct {
	// Deprecated: Use configSvc.Current() instead. Will be removed in Phase 5.
	cfg     *config.Config
	cfgPath string
	logger  *zap.Logger

	// ConfigService provides lock-free snapshot-based config reads
	configSvc *configsvc.Service

	mu      sync.RWMutex
	running bool

	statusMu sync.RWMutex
	status   Status
	statusCh chan Status

	phaseMachine *phaseMachine

	eventMu   sync.RWMutex
	eventSubs map[chan Event]struct{}

	storageManager   *storage.Manager
	indexManager     *index.Manager
	upstreamManager  *upstream.Manager
	cacheManager     *cache.Manager
	truncator        *truncate.Truncator
	sigCache         *toolsig.Cache // Spec 085 FR-008: single process-wide signature cache (indexing warms, MCP reads)
	secretResolver   *secret.Resolver
	tokenizer        tokens.Tokenizer
	refreshManager   *oauth.RefreshManager // Proactive OAuth token refresh
	updateChecker    *updatecheck.Checker  // Background version checking
	telemetryService *telemetry.Service    // Anonymous usage telemetry (Spec 036)

	// Spec 080 (US3): pre-churn snapshot. prechurnStore owns the BBolt
	// shutdown marker + last_error_code record; previousShutdown is the
	// outcome of the PREVIOUS process instance, derived exactly once in New
	// when the marker is armed (FR-010/FR-011) and handed to the telemetry
	// service in SetTelemetry.
	prechurnStore     telemetry.PreChurnStore
	previousShutdown  string
	managementService interface{}      // Initialized later to avoid import cycle
	activityService   *ActivityService // Activity logging service

	// workSessions derives a unit of USER WORK from the churn of transport
	// sessions underneath it (Spec 082).
	workSessions *WorkSessionTracker

	// Spec 047: coalesces servers.changed bursts and embeds the server list +
	// stats payload so SSE subscribers can update without a follow-up
	// GET /api/v1/servers.
	coalescer *serversChangedCoalescer

	// Spec 077 US4 (MCP-2207): debounces the per-scanner security-scan
	// lifecycle storm into one settled event per server per scan.
	scanNotify *scanNotifyDebouncer

	// Phase 6: Supervisor for state reconciliation (lock-free reads via StateView)
	supervisor *supervisor.Supervisor

	// Tool discovery deduplication: tracks servers with in-progress reactive discovery
	// Key: serverName, Value: struct{} (presence indicates discovery in progress)
	discoveryInProgress sync.Map

	// Last-good tool snapshots per server used to avoid transient tool loss during
	// global discovery races/restarts.
	lastGoodToolsMu sync.RWMutex
	lastGoodTools   map[string][]*config.ToolMetadata

	// Profiles v2 (Spec 057, T1): tracks the last-synced effective server set per
	// profile so a config reload can rebuild only the profiles whose membership
	// actually changed and drop profiles removed from config. Guards the
	// per-profile Bleve index reconciliation (internal/runtime/profile_index.go).
	profileIndexMu    sync.Mutex
	profileMembership map[string][]string

	// Schema v3 (telemetry): time-cached Docker daemon availability. The
	// probe has a 2s `docker info` cost, so we don't want to run it on every
	// heartbeat — but we also can't memoize it for the whole process lifetime:
	// users often install or launch Docker Desktop after mcpproxy starts, and
	// we want the next heartbeat to pick up the change. Cache semantics:
	//   - Fresh positive result reused for up to 15 minutes.
	//   - Fresh negative result reused for only 5 minutes so a late Docker
	//     launch flips `server_docker_available_bool` promptly.
	// A transition between states is logged at info level; steady-state
	// probes stay silent to keep logs clean.
	dockerProbeMu     sync.Mutex
	dockerProbeResult bool
	dockerProbedAt    time.Time
	dockerProbeKnown  bool

	appCtx    context.Context
	appCancel context.CancelFunc
}

// New creates a runtime helper for the given config and prepares core managers.
func New(cfg *config.Config, cfgPath string, logger *zap.Logger) (*Runtime, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	storageManager, err := storage.NewManager(cfg.DataDir, logger.Sugar())
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage manager: %w", err)
	}

	// Spec 080 (US3, FR-010): derive the previous instance's shutdown outcome
	// and immediately re-arm the marker. This is the FIRST DB operation after
	// storage.NewManager succeeds — before stale-session cleanup or any other
	// DB work — so a crash/hang anywhere later in startup still reads as a
	// crash next time (crash loops stay visible). Single-writer safety is the
	// BBolt file lock itself: a second instance fails storage.NewManager with
	// DatabaseLockedError (exit code 3) and never reaches this code (FR-013).
	prechurnStore := telemetry.NewPreChurnStore()
	previousShutdown := telemetry.PreviousShutdownUnknown
	if db := storageManager.GetDB(); db != nil {
		if prev, err := prechurnStore.ArmShutdownMarker(db); err != nil {
			logger.Warn("Failed to arm shutdown marker; previous_shutdown will be omitted", zap.Error(err))
		} else {
			previousShutdown = prev
		}
	}

	// Close any stale sessions from previous runs
	if err := storageManager.CloseAllActiveSessions(); err != nil {
		logger.Warn("Failed to close stale sessions on startup", zap.Error(err))
	}

	indexManager, err := index.NewManager(cfg.DataDir, logger)
	if err != nil {
		_ = storageManager.Close()
		return nil, fmt.Errorf("failed to initialize index manager: %w", err)
	}

	// Initialize secret resolver
	secretResolver := secret.NewResolver()

	upstreamManager := upstream.NewManager(logger, cfg, storageManager.GetBoltDB(), secretResolver, storageManager)
	if cfg.Logging != nil {
		upstreamManager.SetLogConfig(cfg.Logging)
	}

	cacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	if err != nil {
		_ = indexManager.Close()
		_ = storageManager.Close()
		return nil, fmt.Errorf("failed to initialize cache manager: %w", err)
	}

	truncator := truncate.NewTruncator(cfg.ToolResponseLimit)

	// Initialize tokenizer (defaults to enabled with cl100k_base)
	tokenizerEnabled := true
	tokenizerEncoding := "cl100k_base"
	if cfg.Tokenizer != nil {
		tokenizerEnabled = cfg.Tokenizer.Enabled
		if cfg.Tokenizer.Encoding != "" {
			tokenizerEncoding = cfg.Tokenizer.Encoding
		}
	}

	tokenizer, err := tokens.NewTokenizer(tokenizerEncoding, logger.Sugar(), tokenizerEnabled)
	if err != nil {
		logger.Warn("Failed to initialize tokenizer, disabling token counting", zap.Error(err))
		// Create a disabled tokenizer as fallback
		tokenizer, _ = tokens.NewTokenizer(tokenizerEncoding, logger.Sugar(), false)
	}

	appCtx, appCancel := context.WithCancel(context.Background())

	// Initialize ConfigService for lock-free snapshot-based reads
	configSvc := configsvc.NewService(cfg, cfgPath, logger)

	// Phase 7.3: Initialize Supervisor with ActorPoolSimple (delegates to UpstreamManager)
	actorPool := supervisor.NewActorPoolSimple(upstreamManager, logger)
	supervisorInstance := supervisor.New(configSvc, actorPool, logger)

	// Initialize OAuth refresh manager for proactive token refresh
	// Uses storageManager as the token store and global coordinator for flow coordination
	refreshManager := oauth.NewRefreshManager(
		storageManager,
		oauth.GetGlobalCoordinator(),
		nil, // Use default config (80% threshold, 3 max retries)
		logger,
	)

	// Initialize activity service for logging tool calls and events
	activityService := NewActivityService(storageManager, logger)

	// Initialize sensitive data detector if configured (Spec 026)
	if cfg.SensitiveDataDetection != nil && cfg.SensitiveDataDetection.IsEnabled() {
		detector := security.NewDetector(cfg.SensitiveDataDetection)
		activityService.SetDetector(detector)
		logger.Info("Sensitive data detection enabled",
			zap.Bool("scan_requests", cfg.SensitiveDataDetection.ScanRequests),
			zap.Bool("scan_responses", cfg.SensitiveDataDetection.ScanResponses))
	}

	// Wire observability usage persistence cadence from config (Spec 069 A2).
	if cfg.Observability != nil && cfg.Observability.UsagePersistInterval.Duration() > 0 {
		activityService.SetUsagePersistInterval(cfg.Observability.UsagePersistInterval.Duration())
	}

	// Wire activity retention config from config file
	if cfg.ActivityRetentionDays > 0 || cfg.ActivityMaxRecords > 0 || cfg.ActivityCleanupIntervalMin > 0 || cfg.ActivityMaxSizeMB >= 0 {
		maxAge := time.Duration(cfg.ActivityRetentionDays) * 24 * time.Hour
		checkInterval := time.Duration(cfg.ActivityCleanupIntervalMin) * time.Minute
		// ActivityMaxSizeMB: 0 disables the size cap, so pass the explicit byte
		// value (>= 0 is applied; -1 would mean "unchanged").
		maxSizeBytes := int64(cfg.ActivityMaxSizeMB) * 1024 * 1024
		activityService.SetRetentionConfig(maxAge, cfg.ActivityMaxRecords, checkInterval, maxSizeBytes)
		logger.Info("Activity retention config applied",
			zap.Int("retention_days", cfg.ActivityRetentionDays),
			zap.Int("max_records", cfg.ActivityMaxRecords),
			zap.Int("max_size_mb", cfg.ActivityMaxSizeMB),
			zap.Int("cleanup_interval_min", cfg.ActivityCleanupIntervalMin))
	}

	// Auto-clean deprecated config fields (backup created at .bak before modification).
	if removed, err := config.CleanDeprecatedFields(cfgPath); err != nil {
		logger.Warn("Failed to auto-clean deprecated config fields", zap.Error(err))
	} else if len(removed) > 0 {
		logger.Info("Auto-cleaned deprecated config fields",
			zap.String("backup", cfgPath+".bak"),
			zap.Int("fields_removed", len(removed)))
		for _, df := range removed {
			logger.Info("Removed deprecated field",
				zap.String("field", df.JSONKey),
				zap.String("reason", df.Message),
				zap.String("replacement", df.Replacement))
		}
	}

	rt := &Runtime{
		cfg:              cfg,
		cfgPath:          cfgPath,
		logger:           logger,
		configSvc:        configSvc,
		storageManager:   storageManager,
		indexManager:     indexManager,
		upstreamManager:  upstreamManager,
		cacheManager:     cacheManager,
		truncator:        truncator,
		sigCache:         toolsig.NewCache(),
		secretResolver:   secretResolver,
		tokenizer:        tokenizer,
		refreshManager:   refreshManager,
		activityService:  activityService,
		supervisor:       supervisorInstance,
		prechurnStore:    prechurnStore,
		previousShutdown: previousShutdown,
		appCtx:           appCtx,
		appCancel:        appCancel,
		status: Status{
			Phase:       PhaseInitializing,
			Message:     "Runtime is initializing...",
			LastUpdated: time.Now(),
		},
		statusCh:          make(chan Status, 10),
		eventSubs:         make(map[chan Event]struct{}),
		phaseMachine:      newPhaseMachine(PhaseInitializing),
		lastGoodTools:     make(map[string][]*config.ToolMetadata),
		profileMembership: make(map[string][]string),
	}

	// Spec 047: drainer goroutine that publishes coalesced servers.changed
	// events. Lifetime is tied to appCtx so it shuts down with the runtime.
	rt.coalescer = newServersChangedCoalescer(rt, 50*time.Millisecond)
	rt.coalescer.start(appCtx)

	// Spec 077 US4 (MCP-2207): collapse the per-scanner scan-notification storm
	// into one settled event per server. 750ms bridges the rapid lifecycle
	// signals of a reconnect storm without noticeably delaying the result.
	rt.scanNotify = newScanNotifyDebouncer(rt, 750*time.Millisecond)

	return rt, nil
}

// Config returns the underlying configuration pointer.
// Deprecated: Use ConfigSnapshot() for lock-free reads. This method exists for backward compatibility.
func (r *Runtime) Config() *config.Config {
	// Use ConfigService for lock-free read
	if r.configSvc != nil {
		snapshot := r.configSvc.Current()
		return snapshot.Config
	}

	// Fallback to legacy locked access
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

// ConfigSnapshot returns an immutable configuration snapshot.
// This is the preferred way to read configuration - it's lock-free and non-blocking.
func (r *Runtime) ConfigSnapshot() *configsvc.Snapshot {
	if r.configSvc != nil {
		return r.configSvc.Current()
	}
	// Fallback if service not initialized
	r.mu.RLock()
	defer r.mu.RUnlock()
	return &configsvc.Snapshot{
		Config:    r.cfg,
		Path:      r.cfgPath,
		Version:   0,
		Timestamp: time.Now(),
	}
}

// ConfigService returns the configuration service for advanced access patterns.
func (r *Runtime) ConfigService() *configsvc.Service {
	return r.configSvc
}

// Supervisor returns the supervisor instance for lock-free state reads via StateView.
// Phase 6: Provides access to fast server status without storage queries.
func (r *Runtime) Supervisor() *supervisor.Supervisor {
	return r.supervisor
}

// ConfigPath returns the tracked config path.
func (r *Runtime) ConfigPath() string {
	if r.configSvc != nil {
		return r.configSvc.Current().Path
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfgPath
}

// UpdateConfig replaces the runtime configuration in-place.
// This now updates both the legacy field and the ConfigService.
func (r *Runtime) UpdateConfig(cfg *config.Config, cfgPath string) {
	// Update ConfigService first
	if r.configSvc != nil {
		if cfgPath != "" {
			r.configSvc.UpdatePath(cfgPath)
		}
		_ = r.configSvc.Update(cfg, configsvc.UpdateTypeModify, "runtime_update")
	}

	// Update legacy fields for backward compatibility
	r.mu.Lock()
	r.cfg = cfg
	if cfgPath != "" {
		r.cfgPath = cfgPath
	}
	r.mu.Unlock()
}

// UpdateListenAddress mutates the in-memory listen address used by the runtime.
func (r *Runtime) UpdateListenAddress(addr string) error {
	if addr == "" {
		return fmt.Errorf("listen address cannot be empty")
	}

	if !strings.Contains(addr, ":") {
		return fmt.Errorf("listen address %q must include a port", addr)
	}

	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("invalid listen address %q: %w", addr, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cfg == nil {
		return fmt.Errorf("runtime configuration is not available")
	}
	r.cfg.Listen = addr
	return nil
}

// SetRunning records whether the server HTTP layer is active.
func (r *Runtime) SetRunning(running bool) {
	r.mu.Lock()
	r.running = running
	r.mu.Unlock()
}

// IsRunning reports the last known running state.
func (r *Runtime) IsRunning() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.running
}

// UpdateStatus mutates the status object and notifies subscribers.
func (r *Runtime) UpdateStatus(phase Phase, message string, stats map[string]interface{}, toolsIndexed int) {
	r.statusMu.Lock()
	r.status.Phase = phase
	r.status.Message = message
	r.status.LastUpdated = time.Now()
	r.status.UpstreamStats = stats
	r.status.ToolsIndexed = toolsIndexed
	snapshot := r.status
	r.statusMu.Unlock()

	if r.phaseMachine != nil {
		// Ensure phase machine mirrors the externally provided phase even if this skips validation
		r.phaseMachine.Set(phase)
	}

	select {
	case r.statusCh <- snapshot:
	default:
	}

	if r.logger != nil {
		r.logger.Info("Status updated", zap.String("phase", string(phase)), zap.String("message", message))
	}
}

// UpdatePhase gathers runtime metrics and broadcasts a status update.
func (r *Runtime) UpdatePhase(phase Phase, message string) {
	var (
		stats map[string]interface{}
		tools int
	)

	if r.upstreamManager != nil {
		stats = r.upstreamManager.GetStats()
		tools = extractToolCount(stats)
	}

	if r.phaseMachine != nil {
		if !r.phaseMachine.Transition(phase) {
			if r.logger != nil {
				current := r.phaseMachine.Current()
				r.logger.Warn("Rejected runtime phase transition",
					zap.String("from", string(current)),
					zap.String("to", string(phase)))
			}
			phase = r.phaseMachine.Current()
		}
	}

	r.UpdateStatus(phase, message, stats, tools)
}

// UpdatePhaseMessage refreshes the status message without moving to a new phase.
func (r *Runtime) UpdatePhaseMessage(message string) {
	var (
		stats map[string]interface{}
		tools int
	)

	if r.upstreamManager != nil {
		stats = r.upstreamManager.GetStats()
		tools = extractToolCount(stats)
	}

	phase := r.CurrentPhase()
	r.UpdateStatus(phase, message, stats, tools)
}

// StatusSnapshot returns the latest status as a map for API responses.
// The serverRunning parameter should come from the authoritative server running state.
func (r *Runtime) StatusSnapshot(serverRunning bool) map[string]interface{} {
	r.statusMu.RLock()
	status := r.status
	r.statusMu.RUnlock()

	r.mu.RLock()
	listen := ""
	if r.cfg != nil {
		listen = r.cfg.Listen
	}
	r.mu.RUnlock()

	return map[string]interface{}{
		"running":        serverRunning,
		"listen_addr":    listen,
		"phase":          status.Phase,
		"message":        status.Message,
		"upstream_stats": status.UpstreamStats,
		"tools_indexed":  status.ToolsIndexed,
		"last_updated":   status.LastUpdated,
	}
}

// StatusChannel exposes the status updates stream.
func (r *Runtime) StatusChannel() <-chan Status {
	return r.statusCh
}

// CurrentStatus returns a copy of the underlying status struct.
func (r *Runtime) CurrentStatus() Status {
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	return r.status
}

// CurrentPhase returns the current lifecycle phase.
func (r *Runtime) CurrentPhase() Phase {
	if r.phaseMachine != nil {
		return r.phaseMachine.Current()
	}

	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	return r.status.Phase
}

// Logger returns the runtime logger.
func (r *Runtime) Logger() *zap.Logger {
	return r.logger
}

// StorageManager exposes the storage manager.
func (r *Runtime) StorageManager() *storage.Manager {
	return r.storageManager
}

// IndexManager exposes the index manager.
func (r *Runtime) IndexManager() *index.Manager {
	return r.indexManager
}

// UpstreamManager exposes the upstream manager.
func (r *Runtime) UpstreamManager() *upstream.Manager {
	return r.upstreamManager
}

// CacheManager exposes the cache manager.
func (r *Runtime) CacheManager() *cache.Manager {
	return r.cacheManager
}

// Truncator exposes the truncator utility.
func (r *Runtime) Truncator() *truncate.Truncator {
	return r.truncator
}

// SignatureCache exposes the process-wide compact-signature cache (Spec 085
// FR-008). Exactly one instance exists: the indexing path warms it and the
// MCP request path (via NewMCPProxyServer) reads it. Never construct a second
// cache — warming a cache the request path does not hold is a silent no-op.
func (r *Runtime) SignatureCache() *toolsig.Cache {
	return r.sigCache
}

// ActivityService exposes the activity service for testing.
func (r *Runtime) ActivityService() *ActivityService {
	return r.activityService
}

// AppContext returns the long-lived runtime context.
func (r *Runtime) AppContext() context.Context {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.appCtx
}

// Close releases runtime resources.
func (r *Runtime) Close() error {
	r.mu.Lock()
	if r.appCancel != nil {
		r.appCancel()
		r.appCancel = nil
		r.appCtx = context.Background()
	}
	r.mu.Unlock()

	var errs []error

	// Stop OAuth refresh manager first to prevent refresh attempts during shutdown
	if r.refreshManager != nil {
		r.refreshManager.Stop()
		if r.logger != nil {
			r.logger.Info("OAuth refresh manager stopped")
		}
	}

	// Phase 6: Stop Supervisor first to stop reconciliation
	if r.supervisor != nil {
		r.supervisor.Stop()
		if r.logger != nil {
			r.logger.Info("Supervisor stopped")
		}
	}

	if r.upstreamManager != nil {
		// Use ShutdownAll instead of DisconnectAll to ensure proper container cleanup
		// ShutdownAll handles both graceful disconnection and Docker container cleanup
		// Use 45-second timeout to allow parallel container cleanup to complete
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer shutdownCancel()

		if err := r.upstreamManager.ShutdownAll(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown upstream servers: %w", err))
			if r.logger != nil {
				r.logger.Error("Failed to shutdown upstream servers", zap.Error(err))
			}
		}

		// Verify all containers stopped with retry loop (15 attempts = 15 seconds).
		// Only when Docker isolation could have launched containers — otherwise
		// the `docker ps` probe + this loop are pure waste (and add ~17s per
		// Close in test processes, which made internal/runtime exceed CI's
		// -race timeout). No isolation ⇒ no managed containers ⇒ nothing to verify.
		if r.upstreamManager.UsesDockerIsolation() && r.upstreamManager.HasDockerContainers() {
			r.verifyContainerCleanup(shutdownCtx)
		}
	}

	if r.cacheManager != nil {
		r.cacheManager.Close()
	}

	if r.indexManager != nil {
		if err := r.indexManager.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close index manager: %w", err))
		}
	}

	// Spec 080 (FR-010, review round 4): the ActivityService owns BBolt
	// writers — activity records, retention pruning, usage-snapshot flushes,
	// async sensitive-data detection. The appCancel at the top of Close
	// triggered its flush-on-shutdown; await that flush AND all its worker
	// goroutines here, BEFORE the async-op drain and the shutdown-marker
	// resolve below, so no activity write can land after the marker claims
	// the shutdown was clean (or after the DB closes). This writer-vs-close
	// race pre-dates Spec 080 (Stop existed but was never called); the marker
	// invariant makes it observable, so it is closed here. Stop returns
	// immediately when Start never ran and is itself idempotent.
	if r.activityService != nil {
		r.activityService.Stop()
	}

	// Spec 080 (FR-010, review round 6): the telemetry heartbeat loop is a
	// BBolt writer too — v7's buildHeartbeat records funnel activity
	// (funnelStore.RecordActivity) and the first tick clears the
	// installer-pending activation flag. The appCancel above stops the loop
	// between ticks and aborts an in-flight HTTP send promptly (the request
	// carries the loop context), but an in-flight tick must be JOINED, not
	// just cancelled — otherwise its BBolt write could land after the marker
	// below claims "clean", or against a closed DB. Stop blocks until the
	// loop (including any in-flight buildHeartbeat/sendHeartbeat, bounded by
	// the HTTP client's 10s timeout) has exited; it returns immediately when
	// Start never ran, is idempotent on double Close, and — like
	// ActivityService.Stop above — terminally stops the service so a Start
	// goroutine not yet scheduled (lifecycle.go launches it via `go`) becomes
	// a no-op instead of writing after this point.
	if r.telemetryService != nil {
		r.telemetryService.Stop()
	}

	// Spec 080 (US3, FR-010): resolve the shutdown marker to "clean" at the
	// LAST point the DB is still open — i.e. after the async storage manager
	// has stopped AND drained its queue (those queued operations perform BBolt
	// writes; StopAsync below runs that drain), immediately before the BBolt
	// handle closes. Every branch above — including the container-cleanup
	// verification, whose early exits live inside verifyContainerCleanup —
	// reaches this point, so a graceful Close always resolves; conversely a
	// hang/SIGKILL/panic ANYWHERE earlier in shutdown — including mid-drain —
	// leaves the marker armed and the next instance honestly reports
	// previous_shutdown="crash". Idempotent on double Close (StopAsync no-ops,
	// and storageManager.Close's internal async stop no-ops after StopAsync);
	// on an already-closed DB the marker write fails harmlessly (logged at
	// debug) and the marker is left untouched.
	if r.storageManager != nil {
		// (1) Stop + drain queued async DB operations — the last DB writes
		// other than the marker resolve itself.
		r.storageManager.StopAsync()

		// (2) Resolve the marker to clean, now that no other DB work remains.
		if r.prechurnStore != nil {
			if db := r.storageManager.GetDB(); db != nil {
				if err := r.prechurnStore.ResolveCleanShutdown(db); err != nil {
					if r.logger != nil {
						r.logger.Debug("Failed to resolve shutdown marker to clean", zap.Error(err))
					}
				}
			}
		}

		// (3) Close the BBolt handle; the async stop inside Close is a no-op,
		// so no DB work intervenes between the marker resolve and db.Close.
		if err := r.storageManager.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close storage manager: %w", err))
		}
	}

	// Close ConfigService and its subscribers
	if r.configSvc != nil {
		r.configSvc.Close()
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// verifyContainerCleanup polls until all Docker-isolation containers are gone,
// force-cleaning as a last resort on timeout. Extracted from Close so that its
// early exits (context timeout, all-clean) return HERE instead of returning
// from Close — every Close path must still reach the shutdown-marker resolve
// and the cache/index/storage/configSvc close sequence (Spec 080 FR-010;
// previously these branches leaked all four and skipped the marker).
func (r *Runtime) verifyContainerCleanup(ctx context.Context) {
	if r.logger != nil {
		r.logger.Warn("Docker containers still running after shutdown, verifying cleanup...")
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for attempt := 0; attempt < 15; attempt++ {
		select {
		case <-ctx.Done():
			if r.logger != nil {
				r.logger.Error("Cleanup verification timeout")
			}
			// Force cleanup as last resort
			r.upstreamManager.ForceCleanupAllContainers()
			return
		case <-ticker.C:
			if !r.upstreamManager.HasDockerContainers() {
				if r.logger != nil {
					r.logger.Info("All containers cleaned up successfully", zap.Int("attempts", attempt+1))
				}
				return
			}
			if r.logger != nil {
				r.logger.Debug("Waiting for container cleanup...", zap.Int("attempt", attempt+1))
			}
		}
	}

	// Timeout reached - force cleanup
	if r.logger != nil {
		r.logger.Error("Some containers failed to stop gracefully - forcing cleanup")
	}
	r.upstreamManager.ForceCleanupAllContainers()

	// Give force cleanup a moment to complete
	time.Sleep(2 * time.Second)

	if r.upstreamManager.HasDockerContainers() {
		if r.logger != nil {
			r.logger.Error("WARNING: Some containers may still be running after force cleanup")
		}
	} else {
		if r.logger != nil {
			r.logger.Info("Force cleanup succeeded - all containers removed")
		}
	}
}

func extractToolCount(stats map[string]interface{}) int {
	if stats == nil {
		return 0
	}

	if totalTools, ok := stats["total_tools"].(int); ok {
		return totalTools
	}

	servers, ok := stats["servers"].(map[string]interface{})
	if !ok {
		return 0
	}

	result := 0
	for _, value := range servers {
		serverStats, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		if count, ok := serverStats["tool_count"].(int); ok {
			result += count
		}
	}
	return result
}

// GetSecretResolver returns the secret resolver instance
func (r *Runtime) GetSecretResolver() *secret.Resolver {
	return r.secretResolver
}

// NotifySecretsChanged notifies the runtime that secrets have changed and restarts affected servers.
// This method should be called by the HTTP API when secrets are added, updated, or deleted.
func (r *Runtime) NotifySecretsChanged(ctx context.Context, operation, secretName string) error {
	r.logger.Info("Secrets changed, finding affected servers",
		zap.String("operation", operation),
		zap.String("secret_name", secretName))

	// Emit the secrets.changed event
	r.emitSecretsChanged(operation, secretName, map[string]any{})

	// Get current config to find servers that use this secret
	cfg := r.Config()
	if cfg == nil {
		return fmt.Errorf("config not available")
	}

	// Find all servers that reference this secret in their env vars or args
	secretRef := fmt.Sprintf("${keyring:%s}", secretName)
	var affectedServers []string

	for _, server := range cfg.Servers {
		// Check environment variables
		for _, value := range server.Env {
			if strings.Contains(value, secretRef) {
				affectedServers = append(affectedServers, server.Name)
				break
			}
		}

		// Check arguments
		for _, arg := range server.Args {
			if strings.Contains(arg, secretRef) {
				affectedServers = append(affectedServers, server.Name)
				break
			}
		}
	}

	if len(affectedServers) == 0 {
		r.logger.Info("No servers affected by secret change",
			zap.String("secret_name", secretName))
		return nil
	}

	r.logger.Info("Restarting servers affected by secret change",
		zap.String("secret_name", secretName),
		zap.Strings("servers", affectedServers))

	// Restart affected servers in the background
	go func() {
		for _, serverName := range affectedServers {
			r.logger.Info("Restarting server due to secret change",
				zap.String("server", serverName),
				zap.String("secret_name", secretName))

			if err := r.RestartServer(serverName); err != nil {
				r.logger.Error("Failed to restart server after secret change",
					zap.String("server", serverName),
					zap.String("secret_name", secretName),
					zap.Error(err))
			}
		}
	}()

	return nil
}

// GetCurrentConfig returns the current configuration
func (r *Runtime) GetCurrentConfig() interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

// convertTokenMetrics converts storage.TokenMetrics to contracts.TokenMetrics
func convertTokenMetrics(m *storage.TokenMetrics) *contracts.TokenMetrics {
	if m == nil {
		return nil
	}
	return &contracts.TokenMetrics{
		InputTokens:     m.InputTokens,
		OutputTokens:    m.OutputTokens,
		TotalTokens:     m.TotalTokens,
		Model:           m.Model,
		Encoding:        m.Encoding,
		EstimatedCost:   m.EstimatedCost,
		TruncatedTokens: m.TruncatedTokens,
		WasTruncated:    m.WasTruncated,
	}
}

// convertToolAnnotations converts config.ToolAnnotations to contracts.ToolAnnotation
func convertToolAnnotations(a *config.ToolAnnotations) *contracts.ToolAnnotation {
	if a == nil {
		return nil
	}
	return &contracts.ToolAnnotation{
		Title:           a.Title,
		ReadOnlyHint:    a.ReadOnlyHint,
		DestructiveHint: a.DestructiveHint,
		IdempotentHint:  a.IdempotentHint,
		OpenWorldHint:   a.OpenWorldHint,
	}
}

// GetToolCalls retrieves tool call history with pagination
func (r *Runtime) GetToolCalls(limit, offset int) ([]*contracts.ToolCallRecord, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Get all server identities to aggregate tool calls
	identities, err := r.storageManager.ListServerIdentities()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list server identities: %w", err)
	}

	// Collect tool calls from all servers
	var allCalls []*storage.ToolCallRecord
	for _, identity := range identities {
		calls, err := r.storageManager.GetServerToolCalls(identity.ID, 1000) // Get up to 1000 per server
		if err != nil {
			r.logger.Sugar().Warnw("Failed to get tool calls for server",
				"server_id", identity.ID,
				"error", err)
			continue
		}
		allCalls = append(allCalls, calls...)
	}

	// Also fetch code_execution calls (built-in tool, not in server_identities)
	codeExecCalls, err := r.storageManager.GetServerToolCalls("code_execution", 1000)
	if err != nil {
		r.logger.Sugar().Warnw("Failed to get code_execution tool calls", "error", err)
	} else {
		allCalls = append(allCalls, codeExecCalls...)
	}

	// Sort by timestamp (most recent first)
	sort.Slice(allCalls, func(i, j int) bool {
		return allCalls[i].Timestamp.After(allCalls[j].Timestamp)
	})

	total := len(allCalls)

	// Apply pagination
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	pagedCalls := allCalls[start:end]

	// Convert to contract types
	contractCalls := make([]*contracts.ToolCallRecord, len(pagedCalls))
	for i, call := range pagedCalls {
		contractCalls[i] = &contracts.ToolCallRecord{
			ID:               call.ID,
			ServerID:         call.ServerID,
			ServerName:       call.ServerName,
			ToolName:         call.ToolName,
			Arguments:        call.Arguments,
			Response:         call.Response,
			Error:            call.Error,
			Duration:         call.Duration,
			Timestamp:        call.Timestamp,
			ConfigPath:       call.ConfigPath,
			RequestID:        call.RequestID,
			Metrics:          convertTokenMetrics(call.Metrics),
			ParentCallID:     call.ParentCallID,
			ExecutionType:    call.ExecutionType,
			MCPSessionID:     call.MCPSessionID,
			MCPClientName:    call.MCPClientName,
			MCPClientVersion: call.MCPClientVersion,
			Annotations:      convertToolAnnotations(call.Annotations),
		}
	}

	return contractCalls, total, nil
}

// GetToolCallByID retrieves a single tool call by ID
func (r *Runtime) GetToolCallByID(id string) (*contracts.ToolCallRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Search through all server tool calls
	identities, err := r.storageManager.ListServerIdentities()
	if err != nil {
		return nil, fmt.Errorf("failed to list server identities: %w", err)
	}

	for _, identity := range identities {
		calls, err := r.storageManager.GetServerToolCalls(identity.ID, 1000)
		if err != nil {
			continue
		}

		for _, call := range calls {
			if call.ID == id {
				return &contracts.ToolCallRecord{
					ID:               call.ID,
					ServerID:         call.ServerID,
					ServerName:       call.ServerName,
					ToolName:         call.ToolName,
					Arguments:        call.Arguments,
					Response:         call.Response,
					Error:            call.Error,
					Duration:         call.Duration,
					Timestamp:        call.Timestamp,
					ConfigPath:       call.ConfigPath,
					RequestID:        call.RequestID,
					Metrics:          convertTokenMetrics(call.Metrics),
					ParentCallID:     call.ParentCallID,
					ExecutionType:    call.ExecutionType,
					MCPSessionID:     call.MCPSessionID,
					MCPClientName:    call.MCPClientName,
					MCPClientVersion: call.MCPClientVersion,
					Annotations:      convertToolAnnotations(call.Annotations),
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("tool call not found: %s", id)
}

// GetServerToolCalls retrieves tool call history for a specific server
func (r *Runtime) GetServerToolCalls(serverName string, limit int) ([]*contracts.ToolCallRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Get server config to find its identity
	serverConfig, err := r.storageManager.GetUpstreamServer(serverName)
	if err != nil {
		return nil, fmt.Errorf("server not found: %w", err)
	}

	serverID := storage.GenerateServerID(serverConfig)

	// Get tool calls for this server
	calls, err := r.storageManager.GetServerToolCalls(serverID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get server tool calls: %w", err)
	}

	// Convert to contract types
	contractCalls := make([]*contracts.ToolCallRecord, len(calls))
	for i, call := range calls {
		contractCalls[i] = &contracts.ToolCallRecord{
			ID:               call.ID,
			ServerID:         call.ServerID,
			ServerName:       call.ServerName,
			ToolName:         call.ToolName,
			Arguments:        call.Arguments,
			Response:         call.Response,
			Error:            call.Error,
			Duration:         call.Duration,
			Timestamp:        call.Timestamp,
			ConfigPath:       call.ConfigPath,
			RequestID:        call.RequestID,
			Metrics:          convertTokenMetrics(call.Metrics),
			ParentCallID:     call.ParentCallID,
			ExecutionType:    call.ExecutionType,
			MCPSessionID:     call.MCPSessionID,
			MCPClientName:    call.MCPClientName,
			MCPClientVersion: call.MCPClientVersion,
			Annotations:      convertToolAnnotations(call.Annotations),
		}
	}

	return contractCalls, nil
}

// ReplayToolCall replays a tool call with modified arguments
func (r *Runtime) ReplayToolCall(id string, arguments map[string]interface{}) (*contracts.ToolCallRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Get the original tool call using the same pattern as GetToolCallByID
	var originalCall *storage.ToolCallRecord
	identities, err := r.storageManager.ListServerIdentities()
	if err != nil {
		return nil, fmt.Errorf("failed to list server identities: %w", err)
	}

	for _, identity := range identities {
		calls, err := r.storageManager.GetServerToolCalls(identity.ID, 1000)
		if err != nil {
			continue
		}

		for _, call := range calls {
			if call.ID == id {
				originalCall = call
				break
			}
		}
		if originalCall != nil {
			break
		}
	}

	if originalCall == nil {
		return nil, fmt.Errorf("tool call not found: %s", id)
	}

	// Use modified arguments if provided, otherwise use original
	callArgs := arguments
	if callArgs == nil {
		callArgs = originalCall.Arguments
	}

	// Get the upstream client
	client, ok := r.upstreamManager.GetClient(originalCall.ServerName)
	if !ok || client == nil {
		return nil, fmt.Errorf("server not found: %s", originalCall.ServerName)
	}

	// Call the tool with modified arguments
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.CallToolTimeout.Duration())
	defer cancel()

	startTime := time.Now()
	result, callErr := client.CallTool(ctx, originalCall.ToolName, callArgs)
	duration := time.Since(startTime)

	// Create new tool call record
	newCall := &storage.ToolCallRecord{
		ID:         fmt.Sprintf("%d-%s", time.Now().UnixNano(), originalCall.ToolName),
		ServerID:   originalCall.ServerID,
		ServerName: originalCall.ServerName,
		ToolName:   originalCall.ToolName,
		Arguments:  callArgs,
		Duration:   duration.Nanoseconds(),
		Timestamp:  time.Now(),
		ConfigPath: r.cfgPath,
	}

	if callErr != nil {
		newCall.Error = callErr.Error()
	} else {
		newCall.Response = result
	}

	// Store the new tool call
	if err := r.storageManager.RecordToolCall(newCall); err != nil {
		r.logger.Warn("Failed to record replayed tool call", zap.Error(err))
	}

	// Convert to contract type
	return &contracts.ToolCallRecord{
		ID:          newCall.ID,
		ServerID:    newCall.ServerID,
		ServerName:  newCall.ServerName,
		ToolName:    newCall.ToolName,
		Arguments:   newCall.Arguments,
		Response:    newCall.Response,
		Error:       newCall.Error,
		Duration:    newCall.Duration,
		Timestamp:   newCall.Timestamp,
		ConfigPath:  newCall.ConfigPath,
		RequestID:   newCall.RequestID,
		Annotations: convertToolAnnotations(newCall.Annotations),
	}, nil
}

// GetToolCallsBySession returns tool calls filtered by session ID
func (r *Runtime) GetToolCallsBySession(sessionID string, limit, offset int) ([]*contracts.ToolCallRecord, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	storageRecords, total, err := r.storageManager.GetToolCallsBySession(sessionID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get tool calls by session: %w", err)
	}

	// Convert storage records to contract types
	records := make([]*contracts.ToolCallRecord, 0, len(storageRecords))
	for _, rec := range storageRecords {
		records = append(records, &contracts.ToolCallRecord{
			ID:               rec.ID,
			ServerID:         rec.ServerID,
			ServerName:       rec.ServerName,
			ToolName:         rec.ToolName,
			Arguments:        rec.Arguments,
			Response:         rec.Response,
			Error:            rec.Error,
			Duration:         rec.Duration,
			Timestamp:        rec.Timestamp,
			ConfigPath:       rec.ConfigPath,
			RequestID:        rec.RequestID,
			Metrics:          convertTokenMetrics(rec.Metrics),
			ParentCallID:     rec.ParentCallID,
			ExecutionType:    rec.ExecutionType,
			MCPSessionID:     rec.MCPSessionID,
			MCPClientName:    rec.MCPClientName,
			MCPClientVersion: rec.MCPClientVersion,
			Annotations:      convertToolAnnotations(rec.Annotations),
		})
	}

	return records, total, nil
}

// GetRecentSessions returns recent MCP sessions
func (r *Runtime) GetRecentSessions(limit int) ([]*contracts.MCPSession, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	storageRecords, total, err := r.storageManager.GetRecentSessions(limit)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get recent sessions: %w", err)
	}

	// Convert storage records to contract types
	sessions := make([]*contracts.MCPSession, 0, len(storageRecords))
	for _, rec := range storageRecords {
		sessions = append(sessions, &contracts.MCPSession{
			ID:            rec.ID,
			ClientName:    rec.ClientName,
			ClientVersion: rec.ClientVersion,
			Status:        rec.Status,
			StartTime:     rec.StartTime,
			EndTime:       rec.EndTime,
			LastActivity:  rec.LastActivity,
			ToolCallCount: rec.ToolCallCount,
			TotalTokens:   rec.TotalTokens,
			HasRoots:      rec.HasRoots,
			HasSampling:   rec.HasSampling,
			Experimental:  rec.Experimental,
			WorkspaceName: rec.WorkspaceName,
			WorkSessionID: rec.WorkSessionID,
		})
	}

	// Stable order for UI consumers: most-recently-active first, break ties
	// by session ID so the list doesn't reshuffle on identical timestamps.
	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].LastActivity.Equal(sessions[j].LastActivity) {
			return sessions[i].LastActivity.After(sessions[j].LastActivity)
		}
		return sessions[i].ID < sessions[j].ID
	})

	return sessions, total, nil
}

// GetSessionByID returns a session by its ID
func (r *Runtime) GetSessionByID(sessionID string) (*contracts.MCPSession, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rec, err := r.storageManager.GetSessionByID(sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	return &contracts.MCPSession{
		ID:            rec.ID,
		ClientName:    rec.ClientName,
		ClientVersion: rec.ClientVersion,
		Status:        rec.Status,
		StartTime:     rec.StartTime,
		EndTime:       rec.EndTime,
		LastActivity:  rec.LastActivity,
		ToolCallCount: rec.ToolCallCount,
		TotalTokens:   rec.TotalTokens,
		HasRoots:      rec.HasRoots,
		HasSampling:   rec.HasSampling,
		Experimental:  rec.Experimental,
		WorkspaceName: rec.WorkspaceName,
		WorkSessionID: rec.WorkSessionID,
	}, nil
}

// ValidateConfig validates a configuration without applying it
func (r *Runtime) ValidateConfig(cfg *config.Config) ([]config.ValidationError, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Perform detailed validation
	return cfg.ValidateDetailed(), nil
}

// ApplyConfig applies a new configuration with hot-reload support
func (r *Runtime) ApplyConfig(newCfg *config.Config, cfgPath string) (*ConfigApplyResult, error) {
	if newCfg == nil {
		return &ConfigApplyResult{
			Success: false,
		}, fmt.Errorf("config cannot be nil")
	}

	r.mu.Lock()

	// Validate the new configuration first
	validationErrors := newCfg.ValidateDetailed()
	if len(validationErrors) > 0 {
		r.mu.Unlock() // Unlock before returning
		return &ConfigApplyResult{
			Success: false,
		}, fmt.Errorf("configuration validation failed: %v", validationErrors[0].Error())
	}

	// Normalize the submitted config the same way LoadFromFile does before we
	// diff/save it (Spec 077 US3): fold the deprecated security.scanner_* keys
	// into security.deep_scan. The /api/v1/config/apply path bypasses
	// LoadFromFile, so without this an API apply carrying the deprecated keys
	// would re-serialize them instead of the unified deep_scan surface (SC-007).
	// Idempotent + nil-safe.
	config.MigrateDeepScanConfig(newCfg)

	// Detect changes and determine if restart is required
	result := DetectConfigChanges(r.cfg, newCfg)
	if !result.Success {
		r.mu.Unlock() // Unlock before returning
		return result, fmt.Errorf("failed to detect config changes")
	}

	// Save configuration to disk BEFORE checking if restart is required
	// This ensures config changes that require restart are persisted and take effect on next startup
	savePath := cfgPath
	if savePath == "" {
		savePath = r.cfgPath
	}
	saveErr := config.SaveConfig(newCfg, savePath)
	if saveErr != nil {
		r.logger.Error("Failed to save configuration to disk",
			zap.String("path", savePath),
			zap.Error(saveErr))
		r.mu.Unlock() // Unlock before returning
		return &ConfigApplyResult{
			Success: false,
		}, fmt.Errorf("failed to save configuration: %w", saveErr)
	} else {
		r.logger.Info("Configuration successfully saved to disk",
			zap.String("path", savePath))
	}

	// If restart is required, don't apply changes in-memory (let user restart)
	if result.RequiresRestart {
		r.logger.Warn("Configuration changes require restart",
			zap.String("reason", result.RestartReason),
			zap.Strings("changed_fields", result.ChangedFields))
		r.mu.Unlock() // Unlock before returning
		return result, nil
	}

	// Apply hot-reloadable changes
	oldCfg := r.cfg
	r.cfg = newCfg
	if cfgPath != "" {
		r.cfgPath = cfgPath
	}

	// Propagate the new global config to the upstream manager and every running
	// managed client so their background health-check loops re-resolve the new
	// global health_check_interval (and Docker/discovery decisions) without a
	// restart (spec 074, FR-012/SC-002). The loops re-read the interval each
	// cycle, so this atomic swap is all that's needed.
	if r.upstreamManager != nil {
		r.upstreamManager.SetGlobalConfig(newCfg)
	}

	// Apply configuration changes to components
	r.logger.Info("Applying configuration hot-reload",
		zap.Strings("changed_fields", result.ChangedFields))

	// Update logging configuration
	if contains(result.ChangedFields, "logging") {
		r.logger.Info("Logging configuration changed")
		if r.upstreamManager != nil && newCfg.Logging != nil {
			r.upstreamManager.SetLogConfig(newCfg.Logging)
		}
	}

	// Update truncator if tool response limit changed
	if contains(result.ChangedFields, "tool_response_limit") {
		r.logger.Info("Tool response limit changed, updating truncator",
			zap.Int("old_limit", oldCfg.ToolResponseLimit),
			zap.Int("new_limit", newCfg.ToolResponseLimit))
		r.truncator = truncate.NewTruncator(newCfg.ToolResponseLimit)
	}

	// Apply observability usage cadence (Spec 069 A2 — hot-reloadable). The
	// usage flush loop re-reads the interval each cycle, so the setter suffices.
	if contains(result.ChangedFields, "observability") && r.activityService != nil &&
		newCfg.Observability != nil && newCfg.Observability.UsagePersistInterval.Duration() > 0 {
		r.logger.Info("Observability usage persist interval changed",
			zap.Duration("new_interval", newCfg.Observability.UsagePersistInterval.Duration()))
		r.activityService.SetUsagePersistInterval(newCfg.Observability.UsagePersistInterval.Duration())
	}

	// Apply update-check settings (Spec 079 FR-012 — hot-reloadable). The
	// checker gates its poll + CheckNow on the flag internally; a
	// disabled→enabled flip (or channel switch) triggers a prompt background
	// re-check. Safe while holding r.mu: SetConfig only touches checker state.
	if contains(result.ChangedFields, "update_check") {
		r.applyUpdateCheckConfig(newCfg)
	}

	// Capture app context, config path, and config copy while we still hold the lock
	appCtx := r.appCtx
	cfgPathCopy := r.cfgPath
	configCopy := *r.cfg // Make a copy to pass to async goroutine
	serversChanged := contains(result.ChangedFields, "mcpServers")
	changedFieldsCopy := make([]string, len(result.ChangedFields))
	copy(changedFieldsCopy, result.ChangedFields)

	r.logger.Info("Configuration hot-reload completed successfully",
		zap.Strings("changed_fields", result.ChangedFields))

	// IMPORTANT: Unlock before emitting events to prevent deadlocks
	// Event handlers may need to acquire locks on other resources
	r.mu.Unlock()

	// MCP-2482: drive the one-time telemetry opt-out beacon on an
	// enabled->disabled flip. NotifyConfigChanged is fire-and-forget and
	// nil-safe, so this never blocks the apply path. Covers web UI + macOS app,
	// which both reach this via the REST /config apply pipeline.
	if r.telemetryService != nil {
		r.telemetryService.NotifyConfigChanged(newCfg)
	}

	// Update configSvc to notify subscribers (like supervisor)
	// This must happen BEFORE LoadConfiguredServers to ensure supervisor reconciles
	if err := r.configSvc.Update(&configCopy, configsvc.UpdateTypeModify, "api_apply_config"); err != nil {
		r.logger.Error("Failed to update config service", zap.Error(err))
	}

	// Emit config.reloaded event (after releasing lock)
	r.emitConfigReloaded(cfgPathCopy)

	// Emit servers.changed event if servers were modified (after releasing lock)
	if serversChanged {
		r.emitServersChanged("config hot-reload", map[string]any{
			"changed_fields": changedFieldsCopy,
		})
	}

	// IMPORTANT: Pass config copy to goroutine to avoid lock dependency
	// The goroutine will use the copied config instead of calling r.Config()
	if serversChanged {
		r.logger.Info("Server configuration changed, scheduling async reload")
		// Spawn goroutine with captured config - no lock needed
		go func(cfg *config.Config, ctx context.Context) {
			if err := r.LoadConfiguredServers(cfg); err != nil {
				r.logger.Error("Failed to reload servers after config apply", zap.Error(err))
				return
			}

			// Re-index tools after servers are reloaded
			if ctx == nil {
				r.logger.Warn("Application context not available for tool re-indexing")
				return
			}

			// Brief delay to let server connections stabilize
			time.Sleep(500 * time.Millisecond)

			if err := r.DiscoverAndIndexTools(ctx); err != nil {
				r.logger.Error("Failed to re-index tools after config apply", zap.Error(err))
			}
		}(&configCopy, appCtx)
	}

	return result, nil
}

// GetConfig returns a copy of the current configuration
func (r *Runtime) GetConfig() (*config.Config, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.cfg == nil {
		return nil, fmt.Errorf("config not initialized")
	}

	// Return a deep copy to prevent external modifications
	// For now, we return the same reference (caller should not modify)
	// TODO: Implement deep copy if needed
	return r.cfg, nil
}

// Tokenizer returns the tokenizer instance.
func (r *Runtime) Tokenizer() tokens.Tokenizer {
	return r.tokenizer
}

// CalculateTokenSavings calculates token savings from using MCPProxy
func (r *Runtime) CalculateTokenSavings() (*contracts.ServerTokenMetrics, error) {
	if r.tokenizer == nil {
		return nil, fmt.Errorf("tokenizer not available")
	}

	// Get default model from config
	model := "gpt-4"
	if r.cfg.Tokenizer != nil && r.cfg.Tokenizer.DefaultModel != "" {
		model = r.cfg.Tokenizer.DefaultModel
	}

	// Create savings calculator
	savingsCalc := tokens.NewSavingsCalculator(r.tokenizer, r.logger.Sugar(), model)

	// Get all connected servers and their tools
	serverInfos := []tokens.ServerToolInfo{}

	// Get all server names
	serverNames := r.upstreamManager.GetAllServerNames()
	for _, serverName := range serverNames {
		client, exists := r.upstreamManager.GetClient(serverName)
		if !exists {
			continue
		}

		// Get tools for this server
		toolsList, err := client.ListTools(r.appCtx)
		if err != nil {
			r.logger.Debug("Failed to list tools for server", zap.String("server", serverName), zap.Error(err))
			continue
		}

		// Convert to ToolInfo format
		toolInfos := make([]tokens.ToolInfo, 0, len(toolsList))
		for _, tool := range toolsList {
			// Parse input schema from ParamsJSON
			var inputSchema map[string]interface{}
			if tool.ParamsJSON != "" {
				if err := json.Unmarshal([]byte(tool.ParamsJSON), &inputSchema); err != nil {
					r.logger.Debug("Failed to parse tool params JSON",
						zap.String("tool", tool.Name),
						zap.Error(err))
					inputSchema = make(map[string]interface{})
				}
			} else {
				inputSchema = make(map[string]interface{})
			}

			toolInfos = append(toolInfos, tokens.ToolInfo{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: inputSchema,
			})
		}

		serverInfos = append(serverInfos, tokens.ServerToolInfo{
			ServerName: serverName,
			ToolCount:  len(toolsList),
			Tools:      toolInfos,
		})
	}

	// Calculate savings
	topK := r.cfg.ToolsLimit
	if topK == 0 {
		topK = 15 // Default
	}

	savingsMetrics, err := savingsCalc.CalculateProxySavings(serverInfos, topK)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate savings: %w", err)
	}

	// Convert to contracts type
	result := &contracts.ServerTokenMetrics{
		TotalServerToolListSize: savingsMetrics.TotalServerToolListSize,
		AverageQueryResultSize:  savingsMetrics.AverageQueryResultSize,
		SavedTokens:             savingsMetrics.SavedTokens,
		SavedTokensPercentage:   savingsMetrics.SavedTokensPercentage,
		PerServerToolListSizes:  savingsMetrics.PerServerToolListSizes,
	}

	return result, nil
}

// contains checks if a string slice contains a specific string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ListRegistries returns the list of available MCP server registries (Phase 7).
//
// It routes through the SAME merged source (built-in defaults + user-configured
// registries, keyed by ID) that search/add use via SetRegistriesFromConfig, so
// `mcpproxy registry list` / the Web UI never omit a built-in that is still
// searchable/addable and never show the legacy hard-coded Smithery entry instead
// of the shipped defaults (FR-006 / MCP-800 finding 2).
func (r *Runtime) ListRegistries() ([]interface{}, error) {
	r.mu.RLock()
	cfg := r.cfg
	r.mu.RUnlock()

	// Rebuild the effective catalog (defaults merged with custom) — same call the
	// search/add paths make — then read it back.
	registries.SetRegistriesFromConfig(cfg)
	merged := registries.ListRegistries()

	result := make([]interface{}, 0, len(merged))
	for i := range merged {
		reg := &merged[i]
		result = append(result, map[string]interface{}{
			"id":          reg.ID,
			"name":        reg.Name,
			"description": reg.Description,
			"url":         reg.URL,
			"servers_url": reg.ServersURL,
			"tags":        reg.Tags,
			"protocol":    reg.Protocol,
			"count":       reg.Count,
			// MCP-866: provenance/trust so every surface can flag third-party
			// sources. "trusted" is the convenience boolean the UI reads to decide
			// whether to show the one-time third-party-registry warning.
			"provenance": reg.Provenance,
			"trusted":    reg.IsTrusted(),
		})
	}

	return result, nil
}

// registryServersCachePrefix is the stable cache-key prefix for a registry's
// cached server lists. A single RefreshRegistryCache drops every tag/query/limit
// variant under it (FR-007).
func registryServersCachePrefix(registryID string) string {
	return fmt.Sprintf("registry-servers:%s:", registryID)
}

// registryServersCacheKey keys a specific (registry, tag, query, limit) search.
func registryServersCacheKey(registryID, tag, query string, limit int) string {
	return fmt.Sprintf("%s%s:%s:%d", registryServersCachePrefix(registryID), tag, query, limit)
}

// SearchRegistryServers searches for servers in a specific registry (Phase 7).
// Results are cached per (registry, tag, query, limit) via the cache manager;
// a cached list is served while flagging its freshness (FR-007), and the
// returned *contracts.RegistryCacheInfo carries the age/stale indicator. A
// registry that requires an unconfigured API key surfaces as a wrapped
// registries.ErrRegistryKeyMissing so the caller can mark it unavailable
// without failing the overall search (FR-008).
func (r *Runtime) SearchRegistryServers(registryID, tag, query string, limit int) ([]interface{}, *contracts.RegistryCacheInfo, error) {
	r.mu.RLock()
	cfg := r.cfg
	r.mu.RUnlock()

	r.logger.Info("Registry search requested",
		zap.String("registry_id", registryID),
		zap.String("query", query),
		zap.String("tag", tag),
		zap.Int("limit", limit))

	// Initialize registries from config
	registries.SetRegistriesFromConfig(cfg)

	cacheKey := registryServersCacheKey(registryID, tag, query, limit)

	// Serve a cached server list when present, flagging its age/freshness.
	if r.cacheManager != nil {
		if rec, ok := r.cacheManager.Peek(cacheKey); ok {
			var cached []interface{}
			if err := json.Unmarshal([]byte(rec.FullContent), &cached); err == nil {
				info := &contracts.RegistryCacheInfo{
					AgeSeconds: time.Since(rec.CreatedAt).Seconds(),
					Stale:      rec.IsExpired(),
				}
				r.logger.Debug("Registry search served from cache",
					zap.String("registry_id", registryID),
					zap.Float64("age_seconds", info.AgeSeconds),
					zap.Bool("stale", info.Stale))
				return cached, info, nil
			}
		}
	}

	// Create a guesser for repository detection (with caching)
	guesser := experiments.NewGuesser(r.cacheManager, r.logger)

	// Search the registry. 30s was too tight: a single slow page in the official
	// registry's multi-page cursor walk exhausted it with no retry headroom. 60s
	// absorbs several slow/retried pages (registries.registryGet) without letting
	// a wedged registry hang the request indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	servers, err := registries.SearchServers(ctx, registryID, tag, query, limit, guesser)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to search registry: %w", err)
	}

	// Convert to interface slice
	result := make([]interface{}, len(servers))
	for i, server := range servers {
		serverMap := map[string]interface{}{
			"id":              server.ID,
			"name":            server.Name,
			"description":     server.Description,
			"url":             server.URL,
			"source_code_url": server.SourceCodeURL,
			"installCmd":      server.InstallCmd,
			"connectUrl":      server.ConnectURL,
			"updatedAt":       server.UpdatedAt,
			"createdAt":       server.CreatedAt,
			"registry":        server.Registry,
		}

		// Add repository info if present
		if server.RepositoryInfo != nil {
			repoInfo := make(map[string]interface{})
			if server.RepositoryInfo.NPM != nil {
				repoInfo["npm"] = map[string]interface{}{
					"exists":      server.RepositoryInfo.NPM.Exists,
					"install_cmd": server.RepositoryInfo.NPM.InstallCmd,
				}
			}
			serverMap["repository_info"] = repoInfo
		}

		result[i] = serverMap
	}

	// Cache the freshly fetched list so subsequent searches surface its age.
	var cacheInfo *contracts.RegistryCacheInfo
	if r.cacheManager != nil {
		if data, mErr := json.Marshal(result); mErr == nil {
			if sErr := r.cacheManager.Store(cacheKey, "registry-servers", nil, string(data), "", len(result)); sErr != nil {
				r.logger.Warn("Failed to cache registry search", zap.Error(sErr))
			}
		}
		cacheInfo = &contracts.RegistryCacheInfo{AgeSeconds: 0, Stale: false}
	}

	r.logger.Info("Registry search completed",
		zap.String("registry_id", registryID),
		zap.Int("results", len(result)))

	return result, cacheInfo, nil
}

// RefreshRegistryCache invalidates all cached server lists for a registry,
// forcing the next search to re-fetch from the source (FR-007). Returns the
// number of cache entries dropped.
func (r *Runtime) RefreshRegistryCache(registryID string) (int, error) {
	if r.cacheManager == nil {
		return 0, nil
	}
	cleared, err := r.cacheManager.InvalidatePrefix(registryServersCachePrefix(registryID))
	if err != nil {
		return 0, fmt.Errorf("failed to refresh registry cache: %w", err)
	}
	r.logger.Info("Registry cache refreshed",
		zap.String("registry_id", registryID),
		zap.Int("cleared", cleared))
	return cleared, nil
}

// GetDockerRecoveryStatus returns the current Docker recovery status from the upstream manager
func (r *Runtime) GetDockerRecoveryStatus() *storage.DockerRecoveryState {
	if r.upstreamManager == nil {
		return nil
	}
	return r.upstreamManager.GetDockerRecoveryStatus()
}

// SetManagementService stores the management service instance.
// This is called after runtime initialization to avoid import cycles.
func (r *Runtime) SetManagementService(svc interface{}) {
	r.managementService = svc
}

// GetManagementService returns the management service instance.
// Returns nil if service hasn't been set yet.
func (r *Runtime) GetManagementService() interface{} {
	return r.managementService
}

// SetRefreshMetricsRecorder sets the metrics recorder for OAuth token refresh operations.
// This enables FR-011: OAuth refresh metrics emission.
func (r *Runtime) SetRefreshMetricsRecorder(recorder oauth.RefreshMetricsRecorder) {
	if r.refreshManager != nil {
		r.refreshManager.SetMetricsRecorder(recorder)
	}
}

// RefreshManager returns the OAuth refresh manager for health status integration.
// Returns nil if refresh manager hasn't been initialized.
func (r *Runtime) RefreshManager() *oauth.RefreshManager {
	return r.refreshManager
}

// EmitServersChanged implements the EventEmitter interface for the management service.
// This delegates to the runtime's internal event emission mechanism.
func (r *Runtime) EmitServersChanged(reason string, extra map[string]any) {
	r.emitServersChanged(reason, extra)
}

// GetAllServers implements RuntimeOperations interface for management service.
// Returns all servers with their current status using the Supervisor's StateView.
func (r *Runtime) GetAllServers() ([]map[string]interface{}, error) {
	r.logger.Debug("Runtime.GetAllServers called")

	// Use Supervisor's StateView for lock-free, instant reads
	supervisor := r.Supervisor()
	if supervisor == nil {
		r.logger.Warn("GetAllServers: supervisor not available, falling back to storage")
		return r.getAllServersLegacy()
	}

	stateView := supervisor.StateView()
	if stateView == nil {
		r.logger.Warn("GetAllServers: StateView not available, falling back to storage")
		return r.getAllServersLegacy()
	}

	// Get snapshot - this is lock-free and instant
	snapshot := stateView.Snapshot()
	r.logger.Debug("StateView snapshot retrieved", zap.Int("count", len(snapshot.Servers)))

	result := make([]map[string]interface{}, 0, len(snapshot.Servers))
	for _, serverStatus := range snapshot.Servers {
		// Convert StateView ServerStatus to API response format
		connected := serverStatus.Connected
		connecting := strings.EqualFold(serverStatus.State, "connecting")

		status := serverStatus.State
		if status == "" {
			if serverStatus.Enabled {
				if connecting {
					status = "connecting"
				} else if connected {
					status = "ready"
				} else {
					status = "disconnected"
				}
			} else {
				status = "disabled"
			}
		}

		// Extract created time and config fields
		var created time.Time
		var url, command, protocol string
		var oauthConfig map[string]interface{}
		var authenticated bool
		var oauthStatus string // OAuth status: "authenticated", "expired", "error", "none"
		var tokenExpiresAt time.Time
		var hasRefreshToken bool
		if serverStatus.Config != nil {
			created = serverStatus.Config.Created
			url = serverStatus.Config.URL
			command = serverStatus.Config.Command
			protocol = serverStatus.Config.Protocol

			// Serialize OAuth config if present (explicit config)
			if serverStatus.Config.OAuth != nil {
				oauthConfig = map[string]interface{}{
					"client_id":    serverStatus.Config.OAuth.ClientID,
					"scopes":       serverStatus.Config.OAuth.Scopes,
					"extra_params": serverStatus.Config.OAuth.ExtraParams,
					"pkce_enabled": serverStatus.Config.OAuth.PKCEEnabled,
					// auth_url, token_url will be populated from OAuth runtime state if available
					"auth_url":  "",
					"token_url": "",
				}
			}

			// Check if server has valid OAuth token in storage
			// IMPORTANT: This runs for ALL servers with a URL, including autodiscovery servers
			// PersistentTokenStore uses serverKey (name + URL hash), not just server name
			// We need to generate the same key format: "servername_hash16"
			if url != "" && r.storageManager != nil {
				r.logger.Debug("Checking OAuth token in storage",
					zap.String("server", serverStatus.Name),
					zap.String("url", url),
					zap.Bool("has_explicit_oauth_config", serverStatus.Config.OAuth != nil))

				// Generate server key matching PersistentTokenStore format
				combined := fmt.Sprintf("%s|%s", serverStatus.Name, url)
				hash := sha256.Sum256([]byte(combined))
				hashStr := hex.EncodeToString(hash[:])
				serverKey := fmt.Sprintf("%s_%s", serverStatus.Name, hashStr[:16])

				r.logger.Debug("Generated OAuth token lookup key",
					zap.String("server", serverStatus.Name),
					zap.String("server_key", serverKey))

				token, err := r.storageManager.GetOAuthToken(serverKey)
				r.logger.Debug("OAuth token lookup result",
					zap.String("server", serverStatus.Name),
					zap.String("server_key", serverKey),
					zap.Bool("token_nil", token == nil),
					zap.Error(err))

				if err == nil && token != nil {
					authenticated = true
					tokenExpiresAt = token.ExpiresAt
					hasRefreshToken = token.RefreshToken != ""
					r.logger.Info("OAuth token found for server",
						zap.String("server", serverStatus.Name),
						zap.String("server_key", serverKey),
						zap.Time("expires_at", token.ExpiresAt),
						zap.Bool("has_refresh_token", hasRefreshToken))

					// For autodiscovery servers (no explicit OAuth config), create minimal oauthConfig
					if oauthConfig == nil {
						oauthConfig = map[string]interface{}{
							"autodiscovery": true,
						}
					}

					// Add token expiration info to oauth config
					if !token.ExpiresAt.IsZero() {
						oauthConfig["token_expires_at"] = token.ExpiresAt.Format(time.RFC3339)
						// Check if token is expired
						isValid := time.Now().Before(token.ExpiresAt)
						oauthConfig["token_valid"] = isValid
						if isValid {
							oauthStatus = string(oauth.OAuthStatusAuthenticated)
						} else {
							oauthStatus = string(oauth.OAuthStatusExpired)
						}
					} else {
						// No expiration means token is valid indefinitely
						oauthConfig["token_valid"] = true
						oauthStatus = string(oauth.OAuthStatusAuthenticated)
					}
				} else {
					// No token found - check if OAuth config exists to determine status
					if oauthConfig != nil {
						oauthStatus = string(oauth.OAuthStatusNone)
					}
				}
			}
		}

		// Check for OAuth error in last_error - this indicates OAuth autodiscovery detected
		// an OAuth-required server that has no token (user needs to authenticate)
		if oauthStatus != string(oauth.OAuthStatusExpired) && serverStatus.LastError != "" {
			if oauth.IsOAuthError(serverStatus.LastError) {
				// If we have no oauthConfig yet, this is an autodiscovery server that needs OAuth
				if oauthConfig == nil {
					oauthConfig = map[string]interface{}{
						"autodiscovery": true,
					}
					// Set status to "none" - user hasn't authenticated yet
					oauthStatus = string(oauth.OAuthStatusNone)
				} else {
					// Has config but error - token might be invalid
					oauthStatus = string(oauth.OAuthStatusError)
				}
			}
		}

		serverMap := map[string]interface{}{
			"id":              serverStatus.Name,
			"name":            serverStatus.Name,
			"url":             url,
			"command":         command,
			"protocol":        protocol,
			"enabled":         serverStatus.Enabled,
			"quarantined":     serverStatus.Quarantined,
			"created":         created,
			"connected":       connected,
			"connecting":      connecting,
			"tool_count":      serverStatus.ToolCount,
			"last_error":      serverStatus.LastError,
			"status":          status,
			"should_retry":    false,
			"retry_count":     serverStatus.RetryCount,
			"last_retry_time": nil,
			"oauth":           oauthConfig,
			"authenticated":   authenticated,
		}

		// Expose config fields the UI needs for edit mode (args, working
		// dir, isolation overrides, headers, env). These used to be
		// dropped on the floor here, so the tray/webui could not
		// round-trip stdio server configuration or display the auth
		// headers attached to HTTP servers.
		if serverStatus.Config != nil {
			if len(serverStatus.Config.Args) > 0 {
				serverMap["args"] = serverStatus.Config.Args
			}
			if serverStatus.Config.WorkingDir != "" {
				serverMap["working_dir"] = serverStatus.Config.WorkingDir
			}
			if len(serverStatus.Config.Headers) > 0 {
				// Headers are redacted at the httpapi/MCP serialization
				// boundary (see Server.redactServerHeaders) — emit the
				// raw values here so reveal-secret-headers users still
				// see plaintext.
				serverMap["headers"] = serverStatus.Config.Headers
			}
			if len(serverStatus.Config.Env) > 0 {
				serverMap["env"] = serverStatus.Config.Env
			}
			if iso := serverStatus.Config.Isolation; iso != nil {
				isoMap := map[string]interface{}{
					"enabled": iso.IsEnabled(),
				}
				if iso.Image != "" {
					isoMap["image"] = iso.Image
				}
				if iso.NetworkMode != "" {
					isoMap["network_mode"] = iso.NetworkMode
				}
				if len(iso.ExtraArgs) > 0 {
					isoMap["extra_args"] = iso.ExtraArgs
				}
				if iso.WorkingDir != "" {
					isoMap["working_dir"] = iso.WorkingDir
				}
				serverMap["isolation"] = isoMap
			}
		}

		// Add reconnect_on_use from config
		if serverStatus.Config != nil && serverStatus.Config.ReconnectOnUse {
			serverMap["reconnect_on_use"] = true
		}

		// MCP-2940: surface the per-server auto-approve intent so the REST GET
		// payload (and SSE servers.changed embed) can drive the Web UI toggle.
		// Tri-state *bool — only emit the key when set so the projection stays
		// nil for servers that never configured it.
		if serverStatus.Config != nil && serverStatus.Config.AutoApproveToolChanges != nil {
			serverMap["auto_approve_tool_changes"] = *serverStatus.Config.AutoApproveToolChanges
		}

		// MCP-3322: surface the per-server init_timeout override so the REST GET
		// payload (and SSE servers.changed embed) can read it back. Emitted as a
		// duration string (e.g. "2m0s"); omitted when unset so the projection
		// stays nil for servers that inherit the global default.
		if serverStatus.Config != nil && serverStatus.Config.InitTimeout != nil {
			serverMap["init_timeout"] = serverStatus.Config.InitTimeout.Duration().String()
		}

		// MCP-901: carry registry provenance through to the REST/SSE projection
		// so the approval/quarantine view can show a server's origin. Empty for
		// manually-configured servers.
		if serverStatus.Config != nil {
			if serverStatus.Config.SourceRegistryID != "" {
				serverMap["source_registry_id"] = serverStatus.Config.SourceRegistryID
			}
			if serverStatus.Config.SourceRegistryProvenance != "" {
				serverMap["source_registry_provenance"] = serverStatus.Config.SourceRegistryProvenance
			}
		}

		// Spec 044: include structured diagnostic error when available.
		if serverStatus.Diagnostic != nil {
			d := serverStatus.Diagnostic
			diagMap := map[string]interface{}{
				"code":        d.Code,
				"severity":    d.Severity,
				"cause":       d.Cause,
				"detected_at": d.DetectedAt,
			}
			if entry, ok := diagnostics.Get(d.Code); ok {
				// MCP-2909: prefer the runtime-aware remediation when present so
				// the user sees the detected runtime + recommended image instead
				// of the generic catalog message.
				if d.Remediation != "" {
					diagMap["user_message"] = d.Remediation
				} else {
					diagMap["user_message"] = entry.UserMessage
				}
				diagMap["fix_steps"] = entry.FixSteps
				diagMap["docs_url"] = entry.DocsURL
			}
			serverMap["diagnostic"] = diagMap
			serverMap["error_code"] = string(d.Code)
		}

		// Add OAuth status fields if available
		if oauthStatus != "" {
			serverMap["oauth_status"] = oauthStatus
		}
		if !tokenExpiresAt.IsZero() {
			serverMap["token_expires_at"] = tokenExpiresAt
		}

		// Add user_logged_out flag from managed client
		// This indicates if the user explicitly logged out, which prevents auto-reconnection
		var userLoggedOut bool
		// MCP-2084: call-time OAuth requirement — set when an anonymously-connected
		// server rejected a tools/call with "authorization required". Drives a
		// proactive Sign-in CTA even though the server looks connected.
		var callTimeOAuthRequired bool
		if r.upstreamManager != nil {
			if client, exists := r.upstreamManager.GetClient(serverStatus.Name); exists && client != nil {
				userLoggedOut = client.IsUserLoggedOut()
				callTimeOAuthRequired = client.IsOAuthCallRequired()
			}
		}
		serverMap["user_logged_out"] = userLoggedOut

		// Calculate unified health status
		healthConfig := health.DefaultHealthConfig()
		if r.cfg != nil && r.cfg.OAuthExpiryWarningHours > 0 {
			healthConfig.ExpiryWarningDuration = time.Duration(r.cfg.OAuthExpiryWarningHours * float64(time.Hour))
		}

		healthInput := health.HealthCalculatorInput{
			Name:                  serverStatus.Name,
			Enabled:               serverStatus.Enabled,
			Quarantined:           serverStatus.Quarantined,
			State:                 serverStatus.State,
			Connected:             connected,
			LastError:             serverStatus.LastError,
			OAuthRequired:         oauthConfig != nil,
			OAuthStatus:           oauthStatus,
			HasRefreshToken:       hasRefreshToken,
			UserLoggedOut:         userLoggedOut,
			CallTimeOAuthRequired: callTimeOAuthRequired,
			ToolCount:             serverStatus.ToolCount,
			MissingSecret:         health.ExtractMissingSecret(serverStatus.LastError),
			OAuthConfigErr:        health.ExtractOAuthConfigError(serverStatus.LastError),
		}
		if !tokenExpiresAt.IsZero() {
			healthInput.TokenExpiresAt = &tokenExpiresAt
		}

		// T032: Wire refresh state into health calculation (Spec 023)
		if r.refreshManager != nil {
			if refreshState := r.refreshManager.GetRefreshState(serverStatus.Name); refreshState != nil {
				healthInput.RefreshState = health.RefreshState(refreshState.State)
				healthInput.RefreshRetryCount = refreshState.RetryCount
				healthInput.RefreshLastError = refreshState.LastError
				healthInput.RefreshNextAttempt = refreshState.NextAttempt
			}
		}

		healthStatus := health.CalculateHealth(healthInput, healthConfig)
		serverMap["health"] = healthStatus

		// M-005: Log health status for debugging
		r.logger.Debug("Server health calculated",
			zap.String("server", serverStatus.Name),
			zap.String("level", healthStatus.Level),
			zap.String("admin_state", healthStatus.AdminState),
			zap.String("summary", healthStatus.Summary),
		)

		result = append(result, serverMap)
	}

	// Stable alphabetical order by name — StateView is backed by a map, so
	// iteration order is non-deterministic. UI consumers (tray, web) expect
	// a stable list so the "Servers Needing Attention" and similar filtered
	// views don't shuffle between polls.
	sort.SliceStable(result, func(i, j int) bool {
		ni, _ := result[i]["name"].(string)
		nj, _ := result[j]["name"].(string)
		return ni < nj
	})

	r.logger.Debug("GetAllServers completed", zap.Int("server_count", len(result)))
	return result, nil
}

// getAllServersLegacy is the storage-based fallback implementation.
func (r *Runtime) getAllServersLegacy() ([]map[string]interface{}, error) {
	r.logger.Warn("Using legacy storage-based GetAllServers (slow path)")

	// Check if storage manager is available
	if r.storageManager == nil {
		r.logger.Warn("getAllServersLegacy: storage manager is nil")
		return []map[string]interface{}{}, nil
	}

	// Get all configured servers from storage
	servers, err := r.storageManager.ListUpstreamServers()
	if err != nil {
		return nil, fmt.Errorf("failed to get servers from storage: %w", err)
	}

	// Get connection status from upstream manager
	result := make([]map[string]interface{}, 0, len(servers))
	for _, srv := range servers {
		serverInfo := map[string]interface{}{
			"id":          srv.Name,
			"name":        srv.Name,
			"url":         srv.URL,
			"command":     srv.Command,
			"protocol":    srv.Protocol,
			"enabled":     srv.Enabled,
			"quarantined": srv.Quarantined,
			"created":     srv.Created,
			"connected":   false,
			"connecting":  false,
			"tool_count":  0,
			"status":      "unknown",
		}

		// MCP-901: registry provenance in parity with the StateView path.
		if srv.SourceRegistryID != "" {
			serverInfo["source_registry_id"] = srv.SourceRegistryID
		}
		if srv.SourceRegistryProvenance != "" {
			serverInfo["source_registry_provenance"] = srv.SourceRegistryProvenance
		}

		// MCP-2940: per-server auto-approve intent in parity with the
		// StateView path. Tri-state *bool — only emit when set.
		if srv.AutoApproveToolChanges != nil {
			serverInfo["auto_approve_tool_changes"] = *srv.AutoApproveToolChanges
		}

		// Try to get connection status
		if r.upstreamManager != nil {
			if client, exists := r.upstreamManager.GetClient(srv.Name); exists && client != nil {
				serverInfo["connected"] = client.IsConnected()
				// Skip slow tool count in legacy path
				serverInfo["tool_count"] = 0
			}
		}

		result = append(result, serverInfo)
	}

	return result, nil
}

// GetServerTools implements RuntimeOperations interface for management service.
// Returns all tools for a specific upstream server from StateView cache (lock-free read).
func (r *Runtime) GetServerTools(serverName string) ([]map[string]interface{}, error) {
	r.logger.Debug("Runtime.GetServerTools called", zap.String("server", serverName))

	// Use Supervisor's StateView for lock-free, instant reads
	if r.supervisor == nil {
		return nil, fmt.Errorf("supervisor not available")
	}

	stateView := r.supervisor.StateView()
	if stateView == nil {
		return nil, fmt.Errorf("StateView not available")
	}

	// Get snapshot - this is lock-free and instant
	snapshot := stateView.Snapshot()
	serverStatus, exists := snapshot.Servers[serverName]
	if !exists {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	// Convert []stateview.ToolInfo to []map[string]interface{}
	tools := make([]map[string]interface{}, 0, len(serverStatus.Tools))
	for _, tool := range serverStatus.Tools {
		toolMap := map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"server_name": serverName,
		}
		if tool.InputSchema != nil {
			toolMap["inputSchema"] = tool.InputSchema
		}
		if tool.Annotations != nil {
			toolMap["annotations"] = tool.Annotations
		}
		tools = append(tools, toolMap)
	}

	// Defensive fallback (MCP-2083): the per-server StateView tool list is volatile
	// derived state — it is cleared on disconnect (supervisor clears Tools on a
	// connection-down event) and only repopulated asynchronously by background
	// discovery. Approving a quarantined server triggers a disconnect/reconnect
	// cycle, and in the field this can leave StateView holding zero tools even
	// though the durable search index already indexed the server's tools. Serving
	// that empty snapshot makes the Tools tab show "No tools available" for a
	// connected server that demonstrably has tools. When StateView reports no tools
	// for a server, fall back to the authoritative search index so we never serve
	// an empty list when indexed tools exist.
	if len(tools) == 0 && r.indexManager != nil {
		if indexed, err := r.indexManager.GetToolsByServer(serverName); err == nil && len(indexed) > 0 {
			tools = make([]map[string]interface{}, 0, len(indexed))
			for _, tool := range indexed {
				// Index stores the full tool name; normalize to the bare tool name
				// (no "server:" prefix) to match the StateView/approval-record
				// convention used by the enrichment layer.
				name := tool.Name
				if idx := strings.Index(name, ":"); idx != -1 {
					name = name[idx+1:]
				}
				toolMap := map[string]interface{}{
					"name":        name,
					"description": tool.Description,
					"server_name": serverName,
				}
				if tool.ParamsJSON != "" {
					var inputSchema map[string]interface{}
					if err := json.Unmarshal([]byte(tool.ParamsJSON), &inputSchema); err == nil {
						toolMap["inputSchema"] = inputSchema
					}
				}
				if tool.Annotations != nil {
					toolMap["annotations"] = tool.Annotations
				}
				tools = append(tools, toolMap)
			}
			r.logger.Debug("GetServerTools: StateView empty, served tools from search index fallback",
				zap.String("server", serverName),
				zap.Int("tool_count", len(tools)))
		}
	}

	return tools, nil
}

// TriggerOAuthLogin implements RuntimeOperations interface for management service.
// Initiates OAuth 2.x authentication flow for a specific server.
func (r *Runtime) TriggerOAuthLogin(serverName string) error {
	r.logger.Debug("Runtime.TriggerOAuthLogin called", zap.String("server", serverName))

	// Delegate to upstream manager to start manual OAuth flow
	if r.upstreamManager == nil {
		return fmt.Errorf("upstream manager not available")
	}

	// Clear the user logged out flag to allow connection after successful OAuth
	if err := r.upstreamManager.SetUserLoggedOut(serverName, false); err != nil {
		r.logger.Warn("Failed to clear user logged out state",
			zap.String("server", serverName),
			zap.Error(err))
		// Continue - this is not a fatal error
	}

	// StartManualOAuth launches browser and starts callback server
	if err := r.upstreamManager.StartManualOAuth(serverName, true); err != nil {
		return fmt.Errorf("failed to start OAuth flow: %w", err)
	}

	return nil
}

// TriggerOAuthLoginQuick implements RuntimeOperations interface for management service.
// Returns OAuthStartResult with actual browser status, auth URL, and any errors.
// This is the synchronous version that provides immediate feedback about browser opening.
func (r *Runtime) TriggerOAuthLoginQuick(serverName string) (*core.OAuthStartResult, error) {
	r.logger.Debug("Runtime.TriggerOAuthLoginQuick called", zap.String("server", serverName))

	if r.upstreamManager == nil {
		return nil, fmt.Errorf("upstream manager not available")
	}

	// Clear the user logged out flag to allow connection after successful OAuth
	if err := r.upstreamManager.SetUserLoggedOut(serverName, false); err != nil {
		r.logger.Warn("Failed to clear user logged out state",
			zap.String("server", serverName),
			zap.Error(err))
		// Continue - this is not a fatal error
	}

	// StartManualOAuthQuick returns immediately with browser status
	result, err := r.upstreamManager.StartManualOAuthQuick(serverName)
	if err != nil {
		return result, fmt.Errorf("failed to start OAuth flow: %w", err)
	}

	return result, nil
}

// TriggerOAuthLogout implements RuntimeOperations interface for management service.
// Clears OAuth token and disconnects a specific server.
func (r *Runtime) TriggerOAuthLogout(serverName string) error {
	r.logger.Debug("Runtime.TriggerOAuthLogout called", zap.String("server", serverName))

	if r.upstreamManager == nil {
		return fmt.Errorf("upstream manager not available")
	}

	// IMPORTANT: Set user logged out flag FIRST before any other operations
	// This prevents race conditions where reconnection logic kicks in
	// during ClearOAuthToken or DisconnectServer operations
	if err := r.upstreamManager.SetUserLoggedOut(serverName, true); err != nil {
		r.logger.Warn("Failed to set user logged out state",
			zap.String("server", serverName),
			zap.Error(err))
		// Continue - still try to clear token and disconnect
	}

	// Clear OAuth token from persistent storage
	if err := r.upstreamManager.ClearOAuthToken(serverName); err != nil {
		return fmt.Errorf("failed to clear OAuth token: %w", err)
	}

	// Disconnect the server to force re-authentication
	if err := r.upstreamManager.DisconnectServer(serverName); err != nil {
		r.logger.Warn("Failed to disconnect server after OAuth logout",
			zap.String("server", serverName),
			zap.Error(err))
		// Continue - token was cleared which is the primary goal
	}

	return nil
}

// RefreshOAuthToken implements RuntimeOperations interface for management service.
// Triggers token refresh for a specific server.
func (r *Runtime) RefreshOAuthToken(serverName string) error {
	r.logger.Debug("Runtime.RefreshOAuthToken called", zap.String("server", serverName))

	if r.upstreamManager == nil {
		return fmt.Errorf("upstream manager not available")
	}

	// Delegate to upstream manager to refresh the token
	if err := r.upstreamManager.RefreshOAuthToken(serverName); err != nil {
		// Spec 044 — attribute terminal refresh outcomes to a stable
		// diagnostics code so downstream consumers (web UI ErrorPanel,
		// tray, doctor fix) don't have to re-parse free-text messages.
		// The string-match classifier fallback catches these too, but
		// explicit typing is cheaper and survives message rewording.
		wrapped := fmt.Errorf("failed to refresh OAuth token: %w", err)
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "expired") || strings.Contains(msg, "no refresh token"):
			return diagnostics.WrapOAuthRefreshExpired(wrapped)
		case strings.Contains(msg, "403") || strings.Contains(msg, "invalid_grant"):
			return diagnostics.WrapOAuthRefresh403(wrapped)
		}
		return wrapped
	}

	return nil
}

// SetVersion initializes the update checker with the given version.
// This should be called once during server startup with the build version.
func (r *Runtime) SetVersion(version string) {
	if r.updateChecker != nil {
		// Already initialized
		return
	}

	r.updateChecker = updatecheck.New(r.logger, version)
	// Gate the checker on the update_check config block before its background
	// loop starts (Spec 079 FR-012); the env switches win inside the checker.
	r.applyUpdateCheckConfig(r.Config())
	r.logger.Info("Update checker initialized", zap.String("version", version))
}

// applyUpdateCheckConfig pushes the update_check config block (Spec 079
// FR-012) onto the running update checker. Called at init (SetVersion) and on
// both config hot-reload paths (ApplyConfig + disk ReloadConfiguration) so an
// update_check.{enabled,channel} edit takes effect without a restart.
// Nil-safe and idempotent; the checker itself resolves env-var precedence.
func (r *Runtime) applyUpdateCheckConfig(cfg *config.Config) {
	if r.updateChecker == nil || cfg == nil {
		return
	}
	uc := cfg.UpdateCheck
	r.updateChecker.SetConfig(uc.IsEnabled(), uc.IncludePrereleases())
}

// GetVersionInfo returns the current version information from the update checker.
// Returns nil if the update checker has not been initialized.
func (r *Runtime) GetVersionInfo() *updatecheck.VersionInfo {
	if r.updateChecker == nil {
		return nil
	}
	return r.updateChecker.GetVersionInfo()
}

// RefreshVersionInfo performs an immediate update check and returns the result.
// Returns nil if the update checker has not been initialized.
func (r *Runtime) RefreshVersionInfo() *updatecheck.VersionInfo {
	if r.updateChecker == nil {
		return nil
	}
	return r.updateChecker.CheckNow()
}

// SetTelemetry initializes the telemetry service with the given version and edition.
// This should be called once during server startup.
func (r *Runtime) SetTelemetry(version, edition string) {
	if r.telemetryService != nil {
		return
	}

	r.telemetryService = telemetry.New(r.cfg, r.cfgPath, version, edition, r.logger)
	r.telemetryService.SetRuntimeStats(r)

	// Spec 044: wire the activation store onto the shared BBolt DB. The
	// bucket is created lazily on first write, but we proactively ensure it
	// exists at startup to avoid write-race on concurrent first-ever events
	// (MCP initialize + upstream connect-success can arrive in the same tick).
	if r.storageManager != nil {
		if db := r.storageManager.GetDB(); db != nil {
			if err := telemetry.EnsureActivationBucket(db); err != nil {
				r.logger.Warn("Failed to ensure activation bucket", zap.Error(err))
			}
			store := telemetry.NewActivationStore()
			r.telemetryService.SetActivationStore(store, db)

			// Spec 044 (T052): one-shot installer-launch marker. When the
			// installer invokes mcpproxy with MCPPROXY_LAUNCHED_BY=installer,
			// persist a pending flag so the first heartbeat (which may be
			// minutes away) can emit launch_source=installer even across
			// restarts. The flag is cleared the moment the heartbeat builder
			// sees it (resolveLaunchSource).
			if os.Getenv("MCPPROXY_LAUNCHED_BY") == "installer" {
				if err := store.SetInstallerPending(db, true); err != nil {
					r.logger.Debug("Failed to set installer_heartbeat_pending", zap.Error(err))
				} else {
					r.logger.Info("Installer-launched process: installer_heartbeat_pending=true")
				}
			}

			// Spec 044 Phase H: wire diagnostics counter store. Pre-create the
			// bucket to avoid write-race on first DiagnosticError classification.
			if err := telemetry.EnsureDiagnosticsCountersBucket(db); err != nil {
				r.logger.Warn("Failed to ensure diagnostics_counters bucket", zap.Error(err))
			}
			diagStore := telemetry.NewDiagnosticsCounterStore()
			r.telemetryService.SetDiagnosticsCounterStore(diagStore, db)

			// Wire error-code notifier into supervisor so every classified
			// DiagnosticError increments the 24h per-code counter. Spec 080
			// (US3, FR-012): the same stream also refreshes last_error_code —
			// the single most recent MCPX_* code, persisted across restarts so
			// the post-crash heartbeat carries the pre-crash code.
			if r.supervisor != nil {
				prechurnStore := r.prechurnStore
				r.supervisor.SetErrorCodeNotifier(func(code string) {
					// Spec 080 FR-012: the pre-churn last_error_code write is
					// synchronous at the classification site — a crash right
					// after classification must not lose the final pre-crash
					// code (that loss window is the one case the field exists
					// for). Sub-ms BBolt Update; no supervisor re-entry.
					if prechurnStore != nil {
						_ = prechurnStore.RecordLastErrorCode(db, code)
					}
					// The 24h aggregate counter write is synchronous too
					// (review round 4): an untracked goroutine here could run
					// after Close resolves the shutdown marker (FR-010) or
					// after the DB handle closes. Synchronous means the write
					// completes inside the supervisor's call stack, so
					// supervisor.Stop() — which joins its goroutines before
					// Close touches storage — is a hard barrier: after it
					// returns, no notifier-driven DB write remains. Same
					// safety argument as above: sub-ms BBolt Update, no
					// supervisor re-entry, so no lock cycle even when the
					// caller holds stateMu.
					_ = diagStore.RecordErrorCode(db, code)
				})
			}

			// Spec 080 (US3): hand the startup-derived previous_shutdown value
			// (stable for this instance, FR-011) and the pre-churn store to
			// the telemetry service so heartbeats can surface the snapshot.
			r.telemetryService.SetPreChurn(r.previousShutdown, r.prechurnStore, db)

			// Spec 080 (US2): wire the funnel observability store and record
			// this process start as activity immediately — the first-install
			// day stamp must persist on first run (FR-007) and short sessions
			// that die before the first heartbeat must still count as active
			// days (FR-008). Local persistence is independent of the opt-out
			// gate; transmission is gated elsewhere (FR-017 unchanged).
			if err := telemetry.EnsureFunnelBucket(db); err != nil {
				r.logger.Warn("Failed to ensure telemetry funnel bucket", zap.Error(err))
			}
			funnelStore := telemetry.NewFunnelStore()
			r.telemetryService.SetFunnelStore(funnelStore, db)
			if err := funnelStore.RecordActivity(db, time.Now().UTC()); err != nil {
				r.logger.Debug("Failed to record funnel activity at startup", zap.Error(err))
			}
		}
	}

	r.logger.Info("Telemetry service initialized", zap.String("version", version), zap.String("edition", edition))
}

// TelemetryService returns the telemetry service instance.
func (r *Runtime) TelemetryService() *telemetry.Service {
	return r.telemetryService
}

// TelemetryRegistry returns the Tier 2 counter registry, or nil if telemetry
// has not been initialized yet. Callers can record events without nil-checking
// — use telemetry.RecordSurfaceOn(reg, ...) which is nil-safe.
func (r *Runtime) TelemetryRegistry() *telemetry.CounterRegistry {
	if r.telemetryService == nil {
		return nil
	}
	return r.telemetryService.Registry()
}

// RecordMCPClientForActivation records a sanitized MCP client name and marks
// the first-ever-client flag (Spec 044 US2). No-op if activation store not
// wired or if the raw name cannot be plumbed through (nil-safe all the way
// down). Intentionally takes the raw name; sanitization happens inside the
// store.
func (r *Runtime) RecordMCPClientForActivation(rawClientName string) {
	if r.telemetryService == nil {
		return
	}
	store := r.telemetryService.ActivationStore()
	db := r.telemetryService.ActivationDB()
	if store == nil || db == nil {
		return
	}
	if err := store.MarkFirstMCPClient(db); err != nil {
		r.logger.Debug("activation: MarkFirstMCPClient failed", zap.Error(err))
	}
	if err := store.RecordMCPClient(db, rawClientName); err != nil {
		r.logger.Debug("activation: RecordMCPClient failed", zap.Error(err))
	}
}

// RecordRetrieveToolsCallForActivation bumps the 24h retrieve_tools counter
// and marks the first-ever-call flag (Spec 044 US2). No-op if the activation
// store is not wired.
func (r *Runtime) RecordRetrieveToolsCallForActivation() {
	if r.telemetryService == nil {
		return
	}
	store := r.telemetryService.ActivationStore()
	db := r.telemetryService.ActivationDB()
	if store == nil || db == nil {
		return
	}
	if err := store.MarkFirstRetrieveToolsCall(db); err != nil {
		r.logger.Debug("activation: MarkFirstRetrieveToolsCall failed", zap.Error(err))
	}
	if err := store.IncrementRetrieveToolsCall(db); err != nil {
		r.logger.Debug("activation: IncrementRetrieveToolsCall failed", zap.Error(err))
	}
}

// SetSessionClientResolver wires the session -> MCP client lookup that stamps
// client_name / client_version onto every activity record at write time.
// No-op if the activity service is not wired.
func (r *Runtime) SetSessionClientResolver(resolver SessionClientResolver) {
	if r.activityService == nil {
		return
	}
	r.activityService.SetSessionClientResolver(resolver)
}

// SetWorkSessionResolver wires the session -> work-session lookup used to stamp
// every activity record (Spec 082).
//
// The resolver reads the id CACHED on the connection rather than re-deriving it.
// Re-deriving per record would let one connection's records disagree: the first
// resolves before the client's project has arrived, the second after.
func (r *Runtime) SetWorkSessionResolver(resolver SessionWorkSessionResolver) {
	if r.activityService == nil {
		return
	}
	r.activityService.SetWorkSessionResolver(resolver)
	r.activityService.SetWorkSessionReaper(r.ReapWorkSessions)
}

// ResolveWorkSession derives the work session for an identity, opening a new one
// when it has been idle past the window. Called once per connection.
func (r *Runtime) ResolveWorkSession(id WorkSessionIdentity) string {
	if r.workSessions == nil {
		r.workSessions = NewWorkSessionTracker(DefaultWorkSessionIdleWindow)
	}
	return r.workSessions.Resolve(id)
}

// ReapWorkSessions drops work sessions idle past maxIdle, so a long-lived daemon
// does not accumulate one map entry per identity forever.
func (r *Runtime) ReapWorkSessions(maxIdle time.Duration) int {
	if r.workSessions == nil {
		return 0
	}
	return r.workSessions.Reap(maxIdle)
}

// RecordRealToolCallForActivation marks the first-ever real (upstream) tool
// call. "Real" means a call proxied to an upstream server, as opposed to a
// built-in tool such as retrieve_tools.
//
// This is the lifetime counterpart of RecordRetrieveToolsCallForActivation.
// Until it existed, the retrieve step had a lifetime flag while the call step
// had only a windowed counter, so the retrieve→call funnel compared a
// lifetime value against a 24h one and understated conversion badly.
//
// No-op if the activation store is not wired.
func (r *Runtime) RecordRealToolCallForActivation() {
	if r.telemetryService == nil {
		return
	}
	store := r.telemetryService.ActivationStore()
	db := r.telemetryService.ActivationDB()
	if store == nil || db == nil {
		return
	}
	if err := store.MarkFirstRealToolCall(db); err != nil {
		r.logger.Debug("activation: MarkFirstRealToolCall failed", zap.Error(err))
	}
}

// RecordTokensSavedForActivation adds n to the 24h tokens-saved estimator.
// n <= 0 is a no-op. Spec 044 US2.
func (r *Runtime) RecordTokensSavedForActivation(n int) {
	if n <= 0 || r.telemetryService == nil {
		return
	}
	store := r.telemetryService.ActivationStore()
	db := r.telemetryService.ActivationDB()
	if store == nil || db == nil {
		return
	}
	if err := store.AddTokensSaved(db, n); err != nil {
		r.logger.Debug("activation: AddTokensSaved failed", zap.Error(err))
	}
}

// MarkFirstConnectedServerForActivation sets the first_connected_server_ever
// flag. Called from the supervisor's connect-success callback. Spec 044 US2.
func (r *Runtime) MarkFirstConnectedServerForActivation() {
	if r.telemetryService == nil {
		return
	}
	store := r.telemetryService.ActivationStore()
	db := r.telemetryService.ActivationDB()
	if store == nil || db == nil {
		return
	}
	if err := store.MarkFirstConnectedServer(db); err != nil {
		r.logger.Debug("activation: MarkFirstConnectedServer failed", zap.Error(err))
	}
}

// GetServerCount returns the total number of configured servers (implements telemetry.RuntimeStats).
func (r *Runtime) GetServerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.cfg == nil {
		return 0
	}
	return len(r.cfg.Servers)
}

// GetConnectedServerCount returns the number of connected upstream servers (implements telemetry.RuntimeStats).
func (r *Runtime) GetConnectedServerCount() int {
	if r.upstreamManager == nil {
		return 0
	}
	stats := r.upstreamManager.GetStats()
	if stats == nil {
		return 0
	}
	servers, ok := stats["servers"].(map[string]interface{})
	if !ok {
		return 0
	}
	count := 0
	for _, serverStat := range servers {
		if stat, ok := serverStat.(map[string]interface{}); ok {
			if connected, ok := stat["connected"].(bool); ok && connected {
				count++
			}
		}
	}
	return count
}

// GetToolCount returns the total number of indexed tools (implements telemetry.RuntimeStats).
func (r *Runtime) GetToolCount() int {
	if r.upstreamManager == nil {
		return 0
	}
	stats := r.upstreamManager.GetStats()
	return extractToolCount(stats)
}

// GetRoutingMode returns the current routing mode (implements telemetry.RuntimeStats).
func (r *Runtime) GetRoutingMode() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.cfg == nil || r.cfg.RoutingMode == "" {
		return config.RoutingModeRetrieveTools
	}
	return r.cfg.RoutingMode
}

// IsQuarantineEnabled returns whether tool-level quarantine is enabled (implements telemetry.RuntimeStats).
func (r *Runtime) IsQuarantineEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.cfg == nil {
		return true
	}
	return r.cfg.IsQuarantineEnabled()
}

// dockerProbeTTLPositive is how long a successful docker daemon probe stays
// cached. 15m is a balance between avoiding the 2s `docker info` cost on
// every heartbeat and not sitting on a stale "true" if the user stops Docker.
const dockerProbeTTLPositive = 15 * time.Minute

// dockerProbeTTLNegative is how long a failed probe stays cached. Kept short
// (5m) so users who launch Docker Desktop *after* mcpproxy started see
// `server_docker_available_bool` flip at the next heartbeat rather than the
// next process restart.
const dockerProbeTTLNegative = 5 * time.Minute

// IsDockerAvailable reports whether the host has a reachable Docker daemon
// (implements telemetry.RuntimeStats, schema v3). Uses a time-based cache —
// see dockerProbeTTLPositive / dockerProbeTTLNegative for reasoning.
func (r *Runtime) IsDockerAvailable() bool {
	r.dockerProbeMu.Lock()
	defer r.dockerProbeMu.Unlock()

	if r.dockerProbeKnown {
		age := time.Since(r.dockerProbedAt)
		ttl := dockerProbeTTLPositive
		if !r.dockerProbeResult {
			ttl = dockerProbeTTLNegative
		}
		if age < ttl {
			return r.dockerProbeResult
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Resolve docker via shellwrap so we find Docker Desktop / Homebrew /
	// Colima installs even when mcpproxy was launched from a LaunchAgent /
	// tray with a minimal inherited PATH (see issue: tray "Docker daemon not
	// available" warning despite healthy daemon + socket).
	dockerBin, resolveErr := shellwrap.ResolveDockerPath(r.logger)
	var err error
	var newResult bool
	if resolveErr != nil || dockerBin == "" {
		// Honest availability (#696): if the CLI can't be resolved to an
		// absolute path, Docker-isolated servers can't spawn it — report
		// unavailable rather than probing a bare "docker" that is not the
		// binary used for spawning.
		err = resolveErr
		newResult = false
	} else {
		cmd := exec.CommandContext(ctx, dockerBin, "info", "--format", "{{.ServerVersion}}")
		err = cmd.Run()
		newResult = err == nil
	}

	// Log only on state changes (or first probe) so repeated heartbeats don't
	// spam the log. Rationale: users care about transitions ("Docker just
	// became available") far more than steady-state telemetry probes.
	if r.logger != nil && (!r.dockerProbeKnown || r.dockerProbeResult != newResult) {
		if newResult {
			r.logger.Info("docker daemon probe: available")
		} else {
			r.logger.Info("docker daemon probe: unavailable", zap.Error(err))
		}
	}

	r.dockerProbeResult = newResult
	r.dockerProbedAt = time.Now()
	r.dockerProbeKnown = true
	return r.dockerProbeResult
}

// GetDockerCLISource returns the coarse, fixed-enum branch that resolved the
// docker CLI — "path" | "bundled" | "login_shell" | "absent" (implements
// telemetry.RuntimeStats, schema v5 / MCP-2745). This is the direct #696 fleet
// signal (docker installed but not on the spawn PATH). It delegates to
// shellwrap.ResolveDockerSource, which shares the process-wide docker-path
// cache, so this is cheap on the heartbeat path. NEVER returns the path itself.
func (r *Runtime) GetDockerCLISource() string {
	return shellwrap.ResolveDockerSource(r.logger)
}

// GetDockerIsolatedServerCount returns how many currently-configured servers
// the runtime actually wraps in a Docker container (implements
// telemetry.RuntimeStats, schema v3).
//
// Implementation note: we reuse core.IsolationManager.ShouldIsolate — the
// same function the stdio connection path consults — so the count matches
// the runtime's real behavior rather than a config-only approximation.
// When DockerIsolation is nil or disabled globally, ShouldIsolate returns
// false for every server, giving a count of 0.
func (r *Runtime) GetDockerIsolatedServerCount() int {
	r.mu.RLock()
	cfg := r.cfg
	r.mu.RUnlock()
	if cfg == nil {
		return 0
	}
	im := core.NewIsolationManager(cfg.DockerIsolation)
	count := 0
	for _, srv := range cfg.Servers {
		if srv == nil {
			continue
		}
		if im.ShouldIsolate(srv) {
			count++
		}
	}
	return count
}

// Activity logging methods (RFC-003)

// ListActivities returns activity records matching the filter.
func (r *Runtime) ListActivities(filter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	if r.storageManager == nil {
		return nil, 0, nil
	}
	return r.storageManager.ListActivities(filter)
}

// GetActivity returns a single activity record by ID.
func (r *Runtime) GetActivity(id string) (*storage.ActivityRecord, error) {
	if r.storageManager == nil {
		return nil, nil
	}
	return r.storageManager.GetActivity(id)
}

// AggregateToolUsage rolls up tool_call activity per (server,tool) since the
// given time (spec 050). Returns an empty map when storage is unavailable.
func (r *Runtime) AggregateToolUsage(since time.Time) (map[string]storage.ToolUsageStat, error) {
	if r.storageManager == nil {
		return map[string]storage.ToolUsageStat{}, nil
	}
	return r.storageManager.AggregateToolUsage(since)
}

// UsageSnapshot returns the actor-owned in-memory usage aggregate snapshot
// (spec 069 A2/A3). Reads are lock-free; the returned value is immutable and
// must be treated as read-only. Returns nil when the activity service is
// unavailable.
func (r *Runtime) UsageSnapshot() *UsageAggregate {
	if r.activityService == nil {
		return nil
	}
	return r.activityService.UsageSnapshot()
}

// StreamActivities returns a channel that yields activity records matching the filter.
func (r *Runtime) StreamActivities(filter storage.ActivityFilter) <-chan *storage.ActivityRecord {
	if r.storageManager == nil {
		ch := make(chan *storage.ActivityRecord)
		close(ch)
		return ch
	}
	return r.storageManager.StreamActivities(filter)
}

// ListToolApprovals returns all tool approval records for a server (Spec 032).
func (r *Runtime) ListToolApprovals(serverName string) ([]*storage.ToolApprovalRecord, error) {
	if r.storageManager == nil {
		return nil, nil
	}
	return r.storageManager.ListToolApprovals(serverName)
}

// GetToolApproval returns a single tool approval record (Spec 032).
func (r *Runtime) GetToolApproval(serverName, toolName string) (*storage.ToolApprovalRecord, error) {
	if r.storageManager == nil {
		return nil, fmt.Errorf("storage not available")
	}
	return r.storageManager.GetToolApproval(serverName, toolName)
}

// GetOnboardingState returns the current wizard engagement state (Spec 046).
func (r *Runtime) GetOnboardingState() (*storage.OnboardingState, error) {
	if r.storageManager == nil {
		return &storage.OnboardingState{}, nil
	}
	return r.storageManager.GetOnboardingState()
}

// GetActivationFirstMCPClient returns Spec 044's FirstMCPClientEver flag and
// the capped list of recognized client names from the activation bucket. Used
// by the v2 onboarding wizard (Spec 046 v2) Verify tab. Nil-safe: when
// telemetry/activation isn't wired (CI/test or telemetry disabled) returns
// (false, nil).
func (r *Runtime) GetActivationFirstMCPClient() (bool, []string) {
	if r.telemetryService == nil {
		return false, nil
	}
	store := r.telemetryService.ActivationStore()
	db := r.telemetryService.ActivationDB()
	if store == nil || db == nil {
		return false, nil
	}
	st, err := store.Load(db)
	if err != nil {
		r.logger.Debug("activation: Load failed for onboarding verify", zap.Error(err))
		return false, nil
	}
	return st.FirstMCPClientEver, st.MCPClientsSeenEver
}

// SaveOnboardingState persists the wizard engagement state (Spec 046).
func (r *Runtime) SaveOnboardingState(state *storage.OnboardingState) error {
	if r.storageManager == nil {
		return fmt.Errorf("storage not available")
	}
	return r.storageManager.SaveOnboardingState(state)
}
