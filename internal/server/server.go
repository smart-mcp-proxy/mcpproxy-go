package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	httppprof "net/http/pprof"
	"os"
	"path/filepath"
	gruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/management"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/observability"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tlslocal"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
	"github.com/smart-mcp-proxy/mcpproxy-go/web"
)

// Status represents the current status of the server
type Status struct {
	Phase         string                 `json:"phase"`          // Starting, Ready, Error
	Message       string                 `json:"message"`        // Human readable status message
	UpstreamStats map[string]interface{} `json:"upstream_stats"` // Upstream server statistics
	ToolsIndexed  int                    `json:"tools_indexed"`  // Number of tools indexed
	LastUpdated   time.Time              `json:"last_updated"`
}

// securityScannerService is the subset of *scanner.Service the runtime hot
// paths depend on (scan-verdict reads, admission scan triggering, approval, and
// the deep-scan re-gate). Declaring it as an interface lets tests inject a fake
// to exercise the spec-086 async admission gate (maybeStartAdmissionScan /
// maybeAutoApproveScanSettled) without standing up a full scanner backend.
type securityScannerService interface {
	GetScanSummary(ctx context.Context, serverName string) *scanner.ScanSummary
	ApproveServer(ctx context.Context, serverName string, force bool, approvedBy string) error
	StartScan(ctx context.Context, serverName string, dryRun bool, scannerIDs []string, sourceDir string) (*scanner.ScanJob, error)
	// GetScanStatus / GetScanReport back the quarantine_security scan
	// operations (scan_server / get_scan_report): an agent that just triggered
	// a scan needs the job's terminal state and the verdict that came out of it.
	GetScanStatus(ctx context.Context, serverName string) (*scanner.ScanJob, error)
	// GetScanStatusByPass resolves ONE pass's job. scan_server must poll Pass 1
	// specifically: GetScanStatus answers with whatever job is active for the
	// server, and a completed Pass 1 auto-starts the Pass-2 deep audit, which
	// would otherwise mask the baseline verdict the caller is waiting for.
	GetScanStatusByPass(ctx context.Context, serverName string, pass int) (*scanner.ScanJob, error)
	GetScanReport(ctx context.Context, serverName string) (*scanner.AggregatedReport, error)
	HasApprovalBaseline(serverName string) bool
	ApplySecurityConfig(sec *config.SecurityConfig)
	SetIsolationMode(mode string)
	DeepScanEnabled() bool
}

// securityScannerSvc returns the published scanner service, or nil while the
// HTTP startup path has not wired one yet. Every read of s.securityScanner from
// outside the constructor must go through here — see the field comment.
func (s *Server) securityScannerSvc() securityScannerService {
	s.securityScannerMu.RLock()
	defer s.securityScannerMu.RUnlock()
	return s.securityScanner
}

// setSecurityScanner publishes the scanner service to the event-listener
// goroutine and the HTTP handlers.
func (s *Server) setSecurityScanner(svc securityScannerService) {
	s.securityScannerMu.Lock()
	defer s.securityScannerMu.Unlock()
	s.securityScanner = svc
}

// Server wraps the MCP proxy server with all its dependencies
type Server struct {
	logger   *zap.Logger
	runtime  *runtime.Runtime
	mcpProxy *MCPProxyServer

	// Server control
	httpServer      *http.Server
	listenerManager *ListenerManager
	running         bool
	listenAddr      string
	mu              sync.RWMutex

	serverCtx    context.Context
	serverCancel context.CancelFunc
	shutdown     bool

	statusCh chan interface{}
	eventsCh chan runtime.Event

	// serveErrCh delivers a fatal serve failure (startup bind error or
	// serve-loop death) out of the async StartServer goroutine so the
	// process can exit instead of lingering unreachable. Buffered (cap 1)
	// so the goroutine never blocks; graceful shutdown never fires it.
	serveErrCh chan error

	// Spec 024: Track server start time for lifecycle events
	startTime time.Time

	// Spec 039: Security scanner service (for scan summaries in server list)
	// securityScanner is published LATE — startCustomHTTPServer constructs the
	// scanner service long after NewServerWithConfigPath has already started the
	// event-listener goroutine. That goroutine reads this field on every
	// servers.changed / scan-settled event, so the publish and the reads are
	// concurrent and must be synchronized: guard both with securityScannerMu and
	// reach the field only through securityScannerSvc()/setSecurityScanner().
	// A dedicated mutex, not s.mu, so it can never participate in a lock cycle
	// with the broader server lifecycle lock.
	securityScannerMu sync.RWMutex
	securityScanner   securityScannerService

	// Spec 086 stage 3 (FR-011): tracks scan-mode servers for which a one-shot
	// admission baseline scan has already been triggered this process, so the
	// stream of servers.changed events does not restart the scan. Guarded by
	// admissionScanMu.
	admissionScanMu     sync.Mutex
	admissionScanKicked map[string]bool

	// Informational Pass-1 baseline scanning (see scan_informational.go).
	// infoScanKnown holds every server name observed since process start, so a
	// servers.changed carrying a name that is not in it is a NEW admission;
	// infoScanQueued dedupes the one informational scan per server per process.
	// Both are guarded by infoScanMu. infoScanRunMu serializes scan EXECUTION
	// across the admission path and the sweep.
	infoScanMu            sync.Mutex
	infoScanKnown         map[string]bool
	infoScanQueued        map[string]bool
	infoScanAttempts      map[string]int
	infoScanRunMu         sync.Mutex
	infoScanSettleTimeout time.Duration
	infoScanSweepDelay    time.Duration

	// Spec 024: Shutdown info for lifecycle events
	shutdownReason string
	shutdownSignal string

	// MCP-32: observability manager (Prometheus /metrics + OTLP tracing).
	// Nil when disabled; config-gated and off by default.
	observability *observability.Manager
}

// NewServer creates a new server instance
func NewServer(cfg *config.Config, logger *zap.Logger) (*Server, error) {
	return NewServerWithConfigPath(cfg, "", logger)
}

// buildObservabilityConfig maps the file-level observability config (MCP-32)
// onto the observability package config. Exporters are OFF unless explicitly
// enabled. Health checks remain on (cheap, internal). Defensive against a nil
// observability block.
func buildObservabilityConfig(cfg *config.Config) observability.Config {
	out := observability.DefaultConfig("mcpproxy", httpapi.GetBuildVersion())
	// DefaultConfig enables metrics; flip to opt-in per the MCP-32 directive.
	out.Metrics.Enabled = false
	out.Tracing.Enabled = false
	// The observability package's health manager is out of scope for MCP-32 and
	// its readiness is vacuous (no registered checkers). Keep the existing
	// controller-backed /healthz and /readyz authoritative in all cases.
	out.Health.Enabled = false

	obs := cfg.Observability
	if obs == nil {
		return out
	}
	if obs.Metrics != nil {
		out.Metrics.Enabled = obs.Metrics.Enabled
	}
	if obs.Tracing != nil {
		out.Tracing.Enabled = obs.Tracing.Enabled
		out.Tracing.Protocol = obs.Tracing.Protocol
		out.Tracing.OTLPEndpoint = obs.Tracing.Endpoint
		out.Tracing.SampleRate = obs.Tracing.SampleRate
	}
	return out
}

// NewServerWithConfigPath creates a new server instance with explicit config path tracking
func NewServerWithConfigPath(cfg *config.Config, configPath string, logger *zap.Logger) (*Server, error) {
	rt, err := runtime.New(cfg, configPath, logger)
	if err != nil {
		return nil, err
	}

	// Install the configured offline TPA signature corpus (spec 086 FR-019)
	// here, at construction, because the trust_mode:scan gate is
	// TRANSPORT-INDEPENDENT: internal/runtime/tool_quarantine.go calls the
	// package-level scanner.ScanToolMetadataVerdict in stdio mode exactly as it
	// does over HTTP. The only other ConfigureBundle call site sits inside
	// startCustomHTTPServer, a branch Start() skips entirely when listen is
	// empty — so a stdio deployment silently ran the build's EMBEDDED
	// signatures while security.tpa_bundle_path / MCPPROXY_TPA_BUNDLE_PATH was
	// set and no error was logged. The HTTP path's later
	// Service.ApplySecurityConfig re-applies the identical path; ConfigureBundle
	// is idempotent.
	configureTPABundle(cfg, logger)

	// Initialize update checker with build version
	// This must happen before StartBackgroundInitialization is called
	rt.SetVersion(httpapi.GetBuildVersion())

	// Initialize telemetry service with build version and edition (Spec 036)
	rt.SetTelemetry(httpapi.GetBuildVersion(), httpapi.GetEdition())

	// Initialize observability manager (MCP-32): Prometheus /metrics + OTLP
	// tracing, config-gated and OFF by default. OAuth refresh metrics (Spec 023,
	// FR-011) are wired here too when metrics are enabled.
	obsConfig := buildObservabilityConfig(cfg)
	obsManager, err := observability.NewManager(logger.Sugar(), &obsConfig)
	if err != nil {
		logger.Warn("Failed to create observability manager, metrics/tracing will be disabled", zap.Error(err))
		obsManager = nil
	} else if obsManager.Metrics() != nil {
		// Wire up metrics recorder to RefreshManager for OAuth refresh metrics
		rt.SetRefreshMetricsRecorder(obsManager.Metrics())
		logger.Info("Prometheus metrics enabled", zap.String("endpoint", "/metrics"))
	}

	// Initialize management service and set it on runtime
	secretResolver := secret.NewResolver()
	mgmtService := management.NewService(
		rt,              // RuntimeOperations
		cfg,             // Config
		rt.ConfigPath(), // Config file path for deprecation checks
		rt,              // EventEmitter
		secretResolver,  // SecretResolver
		logger.Sugar(),
	)
	rt.SetManagementService(mgmtService)

	server := &Server{
		logger:              logger,
		runtime:             rt,
		statusCh:            make(chan interface{}, 10),
		eventsCh:            rt.SubscribeEvents(),
		serveErrCh:          make(chan error, 1),
		observability:       obsManager,
		admissionScanKicked: make(map[string]bool),

		infoScanKnown:         make(map[string]bool),
		infoScanQueued:        make(map[string]bool),
		infoScanAttempts:      make(map[string]int),
		infoScanSettleTimeout: informationalScanSettleTimeout,
		infoScanSweepDelay:    baselineSweepStartDelay,
	}
	// Record the servers this process started with: they are the baseline
	// sweep's job, and anything that shows up later is a NEW admission that gets
	// its own informational scan. Seeded from the startup config (available
	// synchronously) rather than from the first servers.changed, so the very
	// first server a fresh install adds still counts as new.
	server.seedKnownServers(cfg.Servers)

	mcpProxy := NewMCPProxyServer(
		rt.StorageManager(),
		rt.IndexManager(),
		rt.UpstreamManager(),
		rt.CacheManager(),
		rt.Truncator, // getter, re-read at use time so hot-reloaded limits apply (#861)
		logger,
		server,
		cfg.DebugSearch,
		cfg,
		rt.SignatureCache(), // Spec 085 FR-008: the ONE Runtime-owned signature cache
		// Spec 097: the daemon's stored-script authority is the config FILE it
		// actually loaded, declared here rather than re-derived per request.
		WithConfigFilePath(server.GetConfigPath()),
	)
	// MCP-32: give the MCP proxy access to observability for tool-call metrics
	// and OTLP spans.
	mcpProxy.SetObservability(obsManager)

	server.mcpProxy = mcpProxy

	go server.forwardRuntimeStatus()

	// Subscribe BEFORE StartBackgroundInitialization, not inside the listener
	// goroutine (R14). The goroutine used to call SubscribeEvents itself, which
	// races the line below: if background initialization publishes
	// servers.changed before the goroutine is scheduled far enough to subscribe,
	// that first event is dropped.
	//
	// That used to self-heal by accident — with no direct catalog published the
	// discovery filters declined to deny, so the surface still worked until some
	// later event. Spec 102's initial rebuild (D15) publishes an EMPTY catalog at
	// init, which retires that accident: after it, a dropped first
	// servers.changed leaves the filters denying against an empty catalog and the
	// direct surface stays blank until an unrelated reconcile. Subscribing here
	// closes the window rather than depending on goroutine scheduling.
	routingEvents := server.runtime.SubscribeEvents()
	go server.listenForRoutingModeRefresh(routingEvents)

	server.runtime.StartBackgroundInitialization()

	return server, nil
}

// mcpAuthMiddleware wraps the MCP endpoint handler to inject AuthContext into the
// request context. The MCP endpoint is not behind the REST API key middleware, so
// this middleware extracts tokens from the request and creates the appropriate
// AuthContext for downstream MCP tool handlers to enforce scope restrictions.
//
// For agent tokens (mcp_agt_ prefix), it validates the token and sets an agent
// AuthContext with server/permission scopes. For the global API key, it sets an
// admin AuthContext.
//
// When config.RequireMCPAuth is true, unauthenticated requests are rejected with
// 401 Unauthorized. When false (default), unauthenticated requests get admin
// context for backward compatibility. Tray connections always bypass auth.
func (s *Server) mcpAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := httpapi.ExtractToken(r)
		if token == "" {
			// Check if MCP auth is required
			if cfg := s.runtime.Config(); cfg != nil && cfg.RequireMCPAuth {
				// Tray connections are always trusted, even with require_mcp_auth
				source := transport.GetConnectionSource(r.Context())
				if source == transport.ConnectionSourceTray {
					ctx := auth.WithAuthContext(r.Context(), auth.AdminContext())
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				http.Error(w, `{"error":"Authentication required. Provide an API key or agent token."}`, http.StatusUnauthorized)
				return
			}
			// Tray connections carry OS-level authentication (Unix socket /
			// named pipe permissions), so they are a real identity even
			// without a token. Checked BEFORE the anonymous fallback so a
			// socket caller is not downgraded (issue #1148).
			if transport.GetConnectionSource(r.Context()) == transport.ConnectionSourceTray {
				ctx := auth.WithAuthContext(r.Context(), auth.AdminContext())
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// No token provided — preserve existing unprotected MCP behavior.
			// Treat as admin (backward compatibility for MCP clients without
			// auth), but mark the context ANONYMOUS: issue #1148: this
			// deliberate upgrade is what let an unauthenticated caller through
			// a gate written as `authCtx != nil && !authCtx.IsAdmin()`. Every
			// operation that worked before still works; only the
			// secret-REVEALING check (AuthContext.CanRevealSecrets) tells the
			// two apart.
			ctx := auth.WithAuthContext(r.Context(), auth.AnonymousContext())
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Check if this is an agent token
		if strings.HasPrefix(token, auth.TokenPrefixStr) {
			cfg := s.runtime.Config()
			if cfg == nil {
				next.ServeHTTP(w, r)
				return
			}

			hmacKey, err := auth.GetOrCreateHMACKey(cfg.DataDir)
			if err != nil {
				s.logger.Error("Failed to get HMAC key for agent token validation", zap.Error(err))
				http.Error(w, `{"error":"Internal server error"}`, http.StatusInternalServerError)
				return
			}

			storageManager := s.runtime.StorageManager()
			if storageManager == nil {
				s.logger.Error("Storage manager not available for agent token validation")
				http.Error(w, `{"error":"Internal server error"}`, http.StatusInternalServerError)
				return
			}

			agentToken, err := storageManager.ValidateAgentToken(token, hmacKey)
			if err != nil {
				s.logger.Warn("Agent token validation failed on MCP endpoint",
					zap.String("error", err.Error()),
					zap.String("remote_addr", r.RemoteAddr))
				http.Error(w, fmt.Sprintf(`{"error":"Agent token invalid: %s"}`, err.Error()), http.StatusUnauthorized)
				return
			}

			// Update last-used timestamp in background
			go func() {
				if updateErr := storageManager.UpdateAgentTokenLastUsed(agentToken.Name); updateErr != nil {
					s.logger.Warn("Failed to update agent token last-used timestamp",
						zap.String("name", agentToken.Name),
						zap.Error(updateErr))
				}
			}()

			authCtx := agentToken.AuthContext()
			ctx := auth.WithAuthContext(r.Context(), authCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Check if it matches the global API key — treat as admin
		cfg := s.runtime.Config()
		if cfg != nil && cfg.APIKey != "" && token == cfg.APIKey {
			ctx := auth.WithAuthContext(r.Context(), auth.AdminContext())
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Tray connections are trusted
		source := transport.GetConnectionSource(r.Context())
		if source == transport.ConnectionSourceTray {
			ctx := auth.WithAuthContext(r.Context(), auth.AdminContext())
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Token provided but doesn't match anything
		if cfg := s.runtime.Config(); cfg != nil && cfg.RequireMCPAuth {
			// When auth is required, reject unrecognized tokens
			http.Error(w, `{"error":"Invalid authentication token"}`, http.StatusUnauthorized)
			return
		}
		// Backward compatibility: allow through with an ANONYMOUS admin
		// context. The token proved nothing, so it is no more of an identity
		// than no token at all (issue #1148).
		ctx := auth.WithAuthContext(r.Context(), auth.AnonymousContext())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// createSelectiveWebUIProtectedHandler serves the Web UI without authentication.
// Since this handler is only mounted on /ui/*, all paths it receives are UI paths
// that should be served without authentication to allow the SPA to work properly.
// API endpoints are protected separately by the httpAPIServer middleware.
func (s *Server) createSelectiveWebUIProtectedHandler(handler http.Handler) http.Handler {
	// Simply pass through all requests without authentication
	// The handler is only mounted on /ui/* so it won't receive API requests
	return handler
}

// GetStatus returns the current server status
func (s *Server) GetStatus() interface{} {
	status := s.runtime.StatusSnapshot(s.IsRunning())
	if status != nil {
		status["listen_addr"] = s.GetListenAddress()
		status["process_pid"] = os.Getpid()
	}
	return status
}

// TriggerOAuthLogin starts an in-process OAuth flow for the given server name.
// Used by the tray to avoid cross-process DB locking issues during OAuth.
func (s *Server) TriggerOAuthLogin(serverName string) error {
	s.logger.Info("Tray requested OAuth login", zap.String("server", serverName))
	manager := s.runtime.UpstreamManager()
	if manager == nil {
		return fmt.Errorf("upstream manager not initialized")
	}
	if err := manager.StartManualOAuth(serverName, true); err != nil {
		s.logger.Error("Failed to start in-process OAuth", zap.String("server", serverName), zap.Error(err))
		return err
	}
	return nil
}

// GetDockerRecoveryStatus returns the current Docker recovery status
func (s *Server) GetDockerRecoveryStatus() *storage.DockerRecoveryState {
	return s.runtime.GetDockerRecoveryStatus()
}

// IsDockerAvailable reports whether the host has a reachable Docker daemon via
// a real probe. The /api/v1/docker/status endpoint uses this for genuine
// availability rather than the synthetic recovery-state value (MCP-2478).
func (s *Server) IsDockerAvailable() bool {
	return s.runtime.IsDockerAvailable()
}

// StatusChannel returns a channel that receives status updates
func (s *Server) StatusChannel() <-chan interface{} {
	return s.statusCh
}

// EventsChannel exposes runtime events for tray/UI consumers.
// Deprecated: Use SubscribeEvents for per-client subscriptions to avoid event competition.
func (s *Server) EventsChannel() <-chan runtime.Event {
	return s.eventsCh
}

// SubscribeEvents creates a new per-client event subscription channel.
// Each SSE client should get its own channel to avoid competing for events.
func (s *Server) SubscribeEvents() chan runtime.Event {
	if s.runtime == nil {
		return nil
	}
	return s.runtime.SubscribeEvents()
}

// UnsubscribeEvents closes and removes the subscription channel.
func (s *Server) UnsubscribeEvents(ch chan runtime.Event) {
	if s.runtime == nil || ch == nil {
		return
	}
	s.runtime.UnsubscribeEvents(ch)
}

// GetManagementService returns the management service instance from runtime.
// Returns nil if service hasn't been set yet.
func (s *Server) GetManagementService() interface{} {
	if s.runtime == nil {
		return nil
	}
	return s.runtime.GetManagementService()
}

// updateStatus updates the current status and notifies subscribers
func (s *Server) updateStatus(phase runtime.Phase, message string) {
	s.runtime.UpdatePhase(phase, message)
}

func (s *Server) enqueueStatusSnapshot() {
	snapshot := s.runtime.StatusSnapshot(s.IsRunning())
	if snapshot != nil {
		snapshot["listen_addr"] = s.GetListenAddress()
	}
	select {
	case s.statusCh <- snapshot:
	default:
	}
}

func (s *Server) forwardRuntimeStatus() {
	// Emit initial snapshot so SSE clients have data immediately.
	s.enqueueStatusSnapshot()

	for range s.runtime.StatusChannel() {
		s.enqueueStatusSnapshot()
	}
}

// listenForRoutingModeRefresh subscribes to server events and refreshes routing
// mode tool sets when upstream servers change (Spec 031), and re-applies the
// security scanner's opt-in deep-scan gate on config hot-reload (Spec 077 US3).
func (s *Server) listenForRoutingModeRefresh(eventCh chan runtime.Event) {
	defer s.runtime.UnsubscribeEvents(eventCh)

	for evt := range eventCh {
		switch evt.Type {
		case runtime.EventTypeServersChanged:
			s.logger.Debug("servers changed, refreshing routing mode tools",
				zap.String("event_type", string(evt.Type)))
			if s.mcpProxy != nil {
				s.mcpProxy.RefreshDirectModeTools()
				s.mcpProxy.RefreshCodeExecModeTools()
				s.mcpProxy.RefreshPrompts()
			}
			// Spec 086 stage 3 (FR-011): a scan-mode server is quarantined on add
			// and must have its baseline scan triggered so the settle handler can
			// auto-approve it. servers.changed is the add/reconcile signal; kick a
			// one-shot admission scan for any scan-mode, still-quarantined,
			// never-scanned server. Idempotent (see maybeStartAdmissionScans).
			s.maybeStartAdmissionScans(context.Background())
			// Every NEWLY admitted server, in ANY trust mode, also gets one
			// INFORMATIONAL Pass-1 baseline scan so its security badge is
			// populated. It drives no gating and deliberately skips the servers
			// the scan-mode admission gate above already owns.
			s.maybeStartInformationalScans(s.informationalScanContext())
		case runtime.EventTypeConfigReloaded:
			// Spec 077 US3: config hot-reload (file edit or /api/v1/config/apply)
			// must re-gate the scanner so a security.deep_scan.* toggle takes
			// effect without a restart. Fires on both reload paths, which both
			// emit config.reloaded.
			s.reapplyScannerSecurityConfig()
			// PR #973: aggregate_upstream_prompts is hot-reloadable. RefreshPrompts
			// reads the live snapshot, so re-run it on config reload to apply a
			// lone flag flip without a restart. (servers.changed already refreshes
			// prompts in its own case; config.reloaded fires for a config-only edit.)
			if s.mcpProxy != nil {
				s.mcpProxy.RefreshPrompts()
				// UX audit F16: enable_code_execution is hot-reloadable. The
				// routing-mode tool surfaces build their code_execution entry
				// (live tool or "disabled" stub) from the live config snapshot,
				// but they build it ONCE at init — so without this the /mcp
				// surface kept serving the stale stub, which refuses every call,
				// until a restart. Same shape as RefreshPrompts above: cheap,
				// idempotent, static construction on this one listener goroutine.
				s.mcpProxy.RefreshCodeExecutionAvailability()
				// Spec 102 FR-014: direct_tool_response_mode is hot-reloadable,
				// but unlike the settings above the direct listing is
				// REGISTERED state — reading the live config on the next call
				// changes nothing, so the surface has to be rebuilt. Guarded on
				// a real change: SetTools re-registers everything and emits
				// notifications/tools/list_changed to every initialized
				// session, so an unguarded call would make any unrelated config
				// edit look, to a client, exactly like the tool set changing.
				s.mcpProxy.RefreshDirectModeToolsOnSerializationChange()
			}
		case runtime.EventTypeUpstreamPromptsChanged:
			// F13: an upstream added/removed a prompt at runtime (debounced
			// notifications/prompts/list_changed). Rebuild only the aggregated
			// prompt set — RefreshPrompts is a no-op when prompts or aggregation
			// are disabled. Runs on THIS single listener goroutine, the same one
			// servers.changed/config.reloaded use, so it never races another
			// RefreshPrompts (no new reentrancy).
			if s.mcpProxy != nil {
				s.mcpProxy.RefreshPrompts()
			}
		case runtime.EventTypeSecurityScanSettled:
			// Spec 086 stage 3 (FR-011): a scan-mode server that was quarantined on
			// add stays quarantined until its baseline scan settles GREEN. React to
			// the single debounced settle event: on a clean verdict, auto-approve
			// (unquarantine + baseline-approve pending tools); otherwise fail closed.
			serverName, _ := evt.Payload["server_name"].(string)
			s.maybeAutoApproveScanSettled(context.Background(), serverName)
		}
	}
}

// shouldAutoApproveScanSettled is the pure spec 086 stage-3 async admission
// predicate (FR-011). It returns true ONLY for a scan-mode server that is still
// quarantined and whose freshly-read baseline verdict is GREEN ("clean" —
// FR-013). Every other combination fails closed (SC-003): manual servers never
// auto-approve (US2 scenario 4); auto servers are never quarantined so there is
// nothing to do; a non-quarantined server is skipped to break the
// ApproveServer -> unquarantine -> reconnect re-entrancy loop and to ignore
// stale/replayed settle events; any verdict other than "clean" (warnings,
// dangerous, failed, not_scanned, scanning) or an empty verdict leaves the
// server quarantined.
func shouldAutoApproveScanSettled(mode config.TrustMode, quarantined bool, verdict string) bool {
	return mode == config.TrustModeScan && quarantined && verdict == "clean"
}

// maybeAutoApproveScanSettled implements the spec 086 stage-3 async new-server
// admission gate (FR-011). Given the server named in a settled scan event, it
// re-resolves the server's trust mode and current quarantine state from the live
// config and re-reads a FRESH baseline verdict (the settle payload carries only
// status + finding counts, never the verdict), then approves only when
// shouldAutoApproveScanSettled says so. ApproveServer(force=false) provides
// defense in depth: it independently re-gates on hard-tier findings and refuses
// if there is no scan report, so a stale or buggy verdict still cannot
// unquarantine a dangerous or unscanned server.
func (s *Server) maybeAutoApproveScanSettled(ctx context.Context, serverName string) {
	scanSvc := s.securityScannerSvc()
	if serverName == "" || scanSvc == nil {
		return
	}
	sc := s.findServerConfig(serverName)
	if sc == nil {
		// No live config entry (not-yet-loaded / removed): fail closed, skip.
		return
	}
	mode := sc.EffectiveTrustMode()
	// Cheap trust-mode gate before the (cached) verdict read: only scan-mode,
	// still-quarantined servers can possibly auto-approve.
	if mode != config.TrustModeScan || !sc.Quarantined {
		return
	}
	// Admission-window gate (spec 086 FR-011 is a NEW-server admission gate).
	// A prior integrity baseline means the server was already approved/
	// unquarantined at least once, so this quarantine is a deliberate operator
	// re-quarantine (or a rug-pull re-quarantine), NOT the initial admission —
	// never silently override that by auto-approving on a later clean settle.
	if scanSvc.HasApprovalBaseline(serverName) {
		s.logger.Debug("scan-mode server has a prior approval baseline; not auto-approving on settle (respect operator re-quarantine)",
			zap.String("server", serverName))
		return
	}
	verdict := ""
	if summary := scanSvc.GetScanSummary(ctx, serverName); summary != nil {
		verdict = summary.Status
	}
	if !shouldAutoApproveScanSettled(mode, sc.Quarantined, verdict) {
		s.logger.Debug("scan-mode server not auto-approved on settle (fail closed)",
			zap.String("server", serverName),
			zap.String("verdict", verdict))
		return
	}
	if err := scanSvc.ApproveServer(ctx, serverName, false, "scan-auto"); err != nil {
		// ApproveServer's own hard-tier/missing-report gate can reject; that is the
		// intended fail-closed outcome, not a fatal error. Log and leave quarantined.
		s.logger.Warn("auto-approve of scan-mode server after green scan was rejected",
			zap.String("server", serverName),
			zap.Error(err))
		return
	}
	s.logger.Info("auto-approved scan-mode server after green baseline scan (spec 086 FR-011)",
		zap.String("server", serverName))
}

// maybeStartAdmissionScans implements the "trigger a scan" half of spec 086
// stage-3 new-server admission (FR-011). A scan-mode server is quarantined on
// add; without this, nothing ever starts its baseline scan, so it would stay
// quarantined forever (the settle handler never fires). Called on every
// servers.changed, it kicks a one-shot baseline scan for each scan-mode,
// still-quarantined, never-scanned server. Idempotent: the in-memory kicked-set
// plus the "already scanned" verdict guard keep the servers.changed stream from
// restarting an in-flight or completed scan.
func (s *Server) maybeStartAdmissionScans(ctx context.Context) {
	if s.securityScannerSvc() == nil {
		return
	}
	// Read servers from storage (RLock-guarded, returns fresh ServerConfig copies)
	// rather than ranging runtime.Config().Servers: Config() hands back a shared,
	// lock-free snapshot whose Servers slice/structs other goroutines (and some
	// tests) mutate in place, so iterating it from this background event-loop
	// goroutine is a data race. ListUpstreamServers is serialized against
	// SaveUpstreamServer by the storage manager mutex.
	sm := s.runtime.StorageManager()
	if sm == nil {
		return
	}
	servers, err := sm.ListUpstreamServers()
	if err != nil {
		s.logger.Debug("admission scan sweep: failed to list servers", zap.Error(err))
		return
	}
	for _, sc := range servers {
		if sc == nil {
			continue
		}
		s.maybeStartAdmissionScan(ctx, sc)
	}
}

// maybeStartAdmissionScan triggers the one-shot admission baseline scan for a
// single server if it is scan-mode, still quarantined, and has never been
// scanned. StartScan auto-connects the quarantined server (for tool-definition
// export), scans, and emits the debounced EventTypeSecurityScanSettled that
// maybeAutoApproveScanSettled consumes. The scan is launched on its own
// goroutine so the event loop is never blocked; a launch failure clears the
// kicked flag so a later servers.changed can retry.
func (s *Server) maybeStartAdmissionScan(ctx context.Context, sc *config.ServerConfig) {
	scanSvc := s.securityScannerSvc()
	if sc == nil || scanSvc == nil {
		return
	}
	if sc.EffectiveTrustMode() != config.TrustModeScan || !sc.Quarantined {
		return
	}
	// Already scanned (scanning/clean/failed/…): the admission scan already ran
	// or a manual scan is in flight. GetScanSummary returns nil only when no
	// scan job exists yet — the sole "never scanned" signal.
	if summary := scanSvc.GetScanSummary(ctx, sc.Name); summary != nil {
		return
	}
	// A prior approval baseline means this is a re-quarantine of a server that
	// was already admitted once, not a first-time admission — do not re-scan or
	// auto-approve it (aligns with the settle handler's admission-window gate).
	if scanSvc.HasApprovalBaseline(sc.Name) {
		return
	}
	name := sc.Name
	s.admissionScanMu.Lock()
	if s.admissionScanKicked[name] {
		s.admissionScanMu.Unlock()
		return
	}
	s.admissionScanKicked[name] = true
	s.admissionScanMu.Unlock()

	s.logger.Info("triggering admission baseline scan for scan-mode server (spec 086 FR-011)",
		zap.String("server", name))
	go func() {
		if _, err := scanSvc.StartScan(ctx, name, false, nil, ""); err != nil {
			s.logger.Warn("admission baseline scan failed to start; will retry on next servers.changed",
				zap.String("server", name),
				zap.Error(err))
			s.admissionScanMu.Lock()
			delete(s.admissionScanKicked, name)
			s.admissionScanMu.Unlock()
		}
	}()
}

// findServerConfig returns the live ServerConfig for serverName, or nil if
// absent.
//
// Reads from STORAGE, not runtime.Config().Servers. The snapshot Config() hands
// back is shared and lock-free, and its ServerConfig structs are mutated in
// place by other goroutines — so ranging it from this background event-loop
// goroutine is a genuine data race (the same hazard maybeStartAdmissionScans
// documents, and one the race detector reports against this function's only
// caller, maybeAutoApproveScanSettled, once anything actually settles a scan).
// ListUpstreamServers is serialized against SaveUpstreamServer by the storage
// manager mutex and returns fresh copies.
//
// TRADE-OFF (cross-model review, PR #1031). Storage is not a strictly better
// source: Runtime.ApplyConfig publishes the new config and emits
// config.reloaded / servers.changed BEFORE the goroutine it spawns reaches
// LoadConfiguredServers, so for that window storage still holds the PREVIOUS
// records. A scan settling inside it resolves the old policy — e.g. a server
// the operator just moved off trust_mode:"scan" can still be seen as scan-mode
// and quarantined here, and auto-approved on a clean verdict.
//
// Storage is still the right read: the alternative races (the config snapshot's
// ServerConfig structs are mutated in place, which the race detector reports
// against this function), and a sub-second staleness window on a fail-closed
// path is a smaller defect than undefined behaviour. Closing it properly means
// making ApplyConfig synchronize storage before it emits — a change to the
// config-apply pipeline, not to this reader.
func (s *Server) findServerConfig(serverName string) *config.ServerConfig {
	sm := s.runtime.StorageManager()
	if sm == nil {
		return nil
	}
	servers, err := sm.ListUpstreamServers()
	if err != nil {
		s.logger.Debug("findServerConfig: failed to list servers",
			zap.String("server", serverName),
			zap.Error(err))
		return nil
	}
	for _, sc := range servers {
		if sc != nil && sc.Name == serverName {
			return sc
		}
	}
	return nil
}

// configureTPABundle installs the offline TPA signature corpus named by
// security.tpa_bundle_path (or MCPPROXY_TPA_BUNDLE_PATH, which outranks it)
// regardless of transport. Nil-safe: a config with no security block runs the
// corpus embedded in this build. Idempotent — startup and every hot-reload call
// it with the same resolution rule.
func configureTPABundle(cfg *config.Config, logger *zap.Logger) {
	var sec *config.SecurityConfig
	if cfg != nil {
		sec = cfg.Security
	}
	scanner.ConfigureBundle(sec.EffectiveTPABundlePath(), logger)
}

// reapplyScannerSecurityConfig re-applies the opt-in deep-scan gate (and the
// engine-wide default isolation mode) to the running scanner service from the
// live config, so a config hot-reload takes effect without a restart (Spec 077
// US3). Mirrors the startup wiring; idempotent and nil-safe.
func (s *Server) reapplyScannerSecurityConfig() {
	cfg := s.runtime.Config()
	// The TPA corpus is reconfigured on EVERY transport, including stdio where
	// there is no scanner Service at all — the scan gate still runs (spec 086
	// FR-019 hot-reload).
	configureTPABundle(cfg, s.logger)
	scanSvc := s.securityScannerSvc()
	if scanSvc == nil {
		return
	}
	if cfg == nil {
		return
	}
	scanSvc.ApplySecurityConfig(cfg.Security)
	if cfg.DockerIsolation != nil {
		scanSvc.SetIsolationMode(string(cfg.DockerIsolation.ResolvedMode()))
	}
	s.logger.Debug("Re-applied security scanner config on hot-reload",
		zap.Bool("deep_scan_enabled", scanSvc.DeepScanEnabled()))
}

// Start starts the MCP proxy server
func (s *Server) Start(ctx context.Context) error {
	// Spec 024: Track server start time for lifecycle events
	s.mu.Lock()
	s.startTime = time.Now()
	s.mu.Unlock()

	s.logger.Info("Starting MCP proxy server")

	// Handle graceful shutdown when context is cancelled (for full application shutdown only)
	go func() {
		<-ctx.Done()
		s.logger.Info("Main context cancelled, shutting down server")
		// First shutdown the HTTP server
		if err := s.StopServer(); err != nil {
			s.logger.Error("Error stopping server during context cancellation", zap.Error(err))
		}
		// Then shutdown the rest (only for full application shutdown, not server restarts)
		// We distinguish this by checking if the cancelled context is the application context
		runtimeCtx := s.runtime.AppContext()
		s.mu.Lock()
		alreadyShutdown := s.shutdown
		isAppContext := (ctx == runtimeCtx)
		s.mu.Unlock()

		if !alreadyShutdown && isAppContext {
			s.logger.Info("Application context cancelled, performing full shutdown")
			if err := s.Shutdown(); err != nil {
				s.logger.Error("Error during context-triggered shutdown", zap.Error(err))
			}
		} else if !isAppContext {
			s.logger.Info("Server context cancelled, server stop completed")
		}

		s.logger.Info("SERVER SHUTDOWN SEQUENCE COMPLETED")
		_ = s.logger.Sync()
	}()

	cfg := s.runtime.Config()
	listenAddr := ""
	if cfg != nil {
		listenAddr = cfg.Listen
	}

	// Determine transport mode based on listen address
	if listenAddr != "" && listenAddr != ":0" {
		// Start the MCP server in HTTP mode (Streamable HTTP)
		s.logger.Info("Starting MCP server",
			zap.String("transport", "streamable-http"),
			zap.String("listen", listenAddr))

		// Create Streamable HTTP server with custom routing
		// Use the MCP server instance that corresponds to the configured routing_mode
		routingMode := ""
		if cfg != nil {
			routingMode = cfg.RoutingMode
		}
		// Record what /mcp is ACTUALLY bound to, resolved the same way
		// GetMCPServerForMode resolves it (an unrecognised value falls back to
		// retrieve_tools). Every surface that reports "the routing mode" reads
		// this, not the config: the config can move underneath a running
		// process — a restart-pending API change, or a hand-edited file the
		// watcher hot-reloads — and reporting it would name a surface /mcp is
		// not serving.
		s.runtime.SetServedRoutingMode(config.ResolveRoutingMode(routingMode))
		// mcp-go's built-in DNS-rebinding protection is disabled in favor of
		// hostValidationMiddleware, which applies the same check but honors the
		// trusted_hosts allowlist for reverse-proxy deployments (GH #898).
		streamableServer := server.NewStreamableHTTPServer(s.mcpProxy.GetMCPServerForMode(routingMode),
			server.WithDisableLocalhostProtection(true))

		// Create custom HTTP server for handling multiple routes
		if err := s.startCustomHTTPServer(ctx, streamableServer); err != nil {
			var portErr *PortInUseError
			if errors.As(err, &portErr) {
				return err
			}
			return fmt.Errorf("MCP Streamable HTTP server error: %w", err)
		}

		s.runtime.SetRunning(true)
	} else {
		// Start the MCP server in stdio mode
		s.logger.Info("Starting MCP server", zap.String("transport", "stdio"))

		// Update status to show server is now running
		s.updateStatus(runtime.PhaseRunning, "Server is running in stdio mode")
		s.runtime.SetRunning(true)

		// Spec 024: Emit system_start activity event for stdio mode
		startupDurationMs := time.Since(s.startTime).Milliseconds()
		configPath := ""
		if s.runtime != nil {
			configPath = s.runtime.ConfigPath()
		}
		s.runtime.EmitActivitySystemStart(
			httpapi.GetBuildVersion(),
			"stdio",
			startupDurationMs,
			configPath,
		)

		// Serve using stdio (standard MCP transport).
		//
		// Issue #1148: stdio has no HTTP middleware, so it used to run with NO
		// auth context and relied on every gate reading nil as admin. Declare
		// the identity explicitly instead — a stdio peer is the local process
		// that launched us, which is as authenticated as the tray socket.
		if err := server.ServeStdio(s.mcpProxy.GetMCPServer(), server.WithStdioContextFunc(stdioAuthContext)); err != nil {
			return fmt.Errorf("MCP server error: %w", err)
		}
	}

	return nil
}

// stdioAuthContext installs the authenticated-admin context for the stdio
// transport. See the ServeStdio call site above (issue #1148).
//
// Round 2 finding 9 asked for the nil-sensitive paths this changes to be
// enumerated rather than assumed. Every consumer of auth.AuthContextFromContext
// reachable from an MCP handler, and what a stdio session now does differently:
//
//	CHANGED, and intended — stdio now behaves exactly like the API-key admin it
//	is, instead of like an absent identity:
//	  - workspace.go principalFromContext: "" → "admin". Session grouping names
//	    the local principal instead of degrading to client+project.
//	  - preflight_glue.go sessionPreflightScope: nil (unrestricted) → a Scope
//	    over the whole server universe. An admin is in scope for every server,
//	    so the same set is allowed; the scope is merely materialised, and can
//	    now surface a storage error that only fires when ListUpstreams itself
//	    fails.
//	  - mcp.go handleListUpstreams: CanRevealSecrets() nil → false, admin →
//	    true. This is the point of the change: `reveal_secret_headers` keeps
//	    working for the local operator on stdio.
//
//	CHANGED shape, identical outcome:
//	  - mcp_code_execution.go: options.AuthContext and ToolAnnotationFunc are
//	    now set. jsruntime's AuthInfo.CanAccessServer / HasPermission both
//	    short-circuit true for type "admin", so nothing is newly denied; only
//	    an annotation lookup is added per dispatch.
//	  - mcp_routing.go direct dispatch: the access and permission checks now
//	    run, and both pass for an admin.
//	  - mcp_describe_check.go: record.UserID / UserEmail are read off the
//	    context and are empty on an admin context, so the record is unchanged.
//
//	UNCHANGED — these gate on agent/user identity, which an admin context is
//	not, or already read nil as admin:
//	  profile_resolver.go, mcp_direct_scope.go, mcp_direct_callability.go,
//	  mcp_describe_direct.go, mcp_visibility.go,
//	  observability_edition_server.go, auth.AuthorizeServerOp and the
//	  `authCtx != nil && !authCtx.IsAdmin()` gates in mcp.go.
func stdioAuthContext(ctx context.Context) context.Context {
	return auth.WithAuthContext(ctx, auth.AdminContext())
}

// discoverAndIndexTools discovers tools from upstream servers and indexes them

// SetShutdownInfo sets the reason and signal for shutdown (Spec 024).
// Call this before Shutdown() to include shutdown context in activity logs.
func (s *Server) SetShutdownInfo(reason, signal string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shutdownReason = reason
	s.shutdownSignal = signal
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() error {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		s.logger.Info("Server already shutdown, skipping")
		return nil
	}
	s.shutdown = true
	httpServer := s.httpServer
	startTime := s.startTime
	reason := s.shutdownReason
	signal := s.shutdownSignal
	s.mu.Unlock()

	// Spec 024: Emit system_stop event before actual shutdown begins
	if s.runtime != nil && !startTime.IsZero() {
		uptimeSeconds := int64(time.Since(startTime).Seconds())
		if reason == "" {
			reason = "shutdown"
		}
		s.runtime.EmitActivitySystemStop(reason, signal, uptimeSeconds, "")
	}

	if s.eventsCh != nil {
		s.runtime.UnsubscribeEvents(s.eventsCh)
	}

	// MCP-32: flush and shut down the OTLP tracer provider so buffered spans
	// are exported before exit.
	if s.observability != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.observability.Close(ctx); err != nil {
			s.logger.Warn("Failed to close observability manager", zap.Error(err))
		}
		cancel()
	}

	s.logger.Info("Shutting down MCP proxy server...")

	// Gracefully shutdown HTTP server first to stop accepting new connections
	if httpServer != nil {
		s.logger.Info("Gracefully shutting down HTTP server...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := httpServer.Shutdown(ctx); err != nil {
			s.logger.Warn("HTTP server forced shutdown due to timeout", zap.Error(err))
			// Force close if graceful shutdown times out
			httpServer.Close()
		} else {
			s.logger.Info("HTTP server shutdown completed gracefully")
		}
	}

	if err := s.runtime.Close(); err != nil {
		s.logger.Error("Failed to close runtime", zap.Error(err))
	}

	// Close MCP proxy server (includes JavaScript runtime pool cleanup)
	if s.mcpProxy != nil {
		if err := s.mcpProxy.Close(); err != nil {
			s.logger.Error("Failed to close MCP proxy server", zap.Error(err))
		}
	}

	s.logger.Info("MCP proxy server shutdown complete")
	return nil
}

// IsRunning returns whether the server is currently running
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// IsReady returns whether the server is fully initialized and ready to serve requests
func (s *Server) IsReady() bool {
	status := s.runtime.CurrentStatus()

	switch status.Phase {
	case runtime.PhaseReady:
		return true
	case runtime.PhaseRunning:
		return true
	case runtime.PhaseError,
		runtime.PhaseStopping,
		runtime.PhaseStopped,
		runtime.PhaseInitializing,
		runtime.PhaseLoading,
		runtime.PhaseStarting:
		return false
	default:
		// Future phases fall back to actual running state.
		return s.IsRunning()
	}
}

// GetListenAddress returns the address the server is listening on
func (s *Server) GetListenAddress() string {
	s.mu.RLock()
	addr := s.listenAddr
	s.mu.RUnlock()
	if addr != "" {
		return addr
	}
	// Don't return config value if it contains :0 (unbound port)
	// This indicates the server hasn't fully started yet
	if cfg := s.runtime.Config(); cfg != nil {
		listen := cfg.Listen
		// Check if the port is 0 (unbound) - don't return it as a fallback
		if listen != "" && !strings.HasSuffix(listen, ":0") {
			return listen
		}
	}
	return ""
}

// SetListenAddress updates the configured listen address and optionally persists it to disk.
func (s *Server) SetListenAddress(addr string, persist bool) error {
	if _, _, err := splitListenAddress(addr); err != nil {
		return err
	}

	if err := s.runtime.UpdateListenAddress(addr); err != nil {
		return err
	}

	if persist {
		if err := s.runtime.SaveConfiguration(); err != nil {
			return fmt.Errorf("failed to save configuration: %w", err)
		}
	}

	s.logger.Info("Listen address updated",
		zap.String("listen", addr),
		zap.Bool("persisted", persist))

	return nil
}

const defaultPortSuggestionAttempts = 20

// SuggestAlternateListen attempts to find an available listen address near baseAddr.
func (s *Server) SuggestAlternateListen(baseAddr string) (string, error) {
	return findAvailableListenAddress(baseAddr, defaultPortSuggestionAttempts)
}

// GetUpstreamStats returns statistics about upstream servers
func (s *Server) GetUpstreamStats() map[string]interface{} {
	// Issue #1148, round 8. This is the SECOND implementation of
	// `upstream_stats` and the one that actually runs: round 4 masked
	// upstream.Manager.GetStats, which is only the fallback below, so the
	// StateView path served `url` and `last_error` verbatim on
	// GET /api/v1/status and on every SSE status event. Both implementations
	// now hand their map to the one shared rule at this boundary.
	reveal := false
	if cfg, err := s.GetConfig(); err == nil && cfg != nil {
		reveal = cfg.RevealSecretHeaders
	}

	if supervisor := s.runtime.Supervisor(); supervisor != nil {
		if view := supervisor.StateView(); view != nil {
			snapshot := view.Snapshot()

			connectedCount := 0
			connectingCount := 0
			quarantinedCount := 0
			totalTools := 0

			serverStats := make(map[string]interface{}, len(snapshot.Servers))

			for name, status := range snapshot.Servers {
				if status == nil {
					continue
				}

				var connInfo *types.ConnectionInfo
				if meta, ok := status.Metadata["connection_info"]; ok {
					if info, ok := meta.(*types.ConnectionInfo); ok {
						connInfo = info
					}
				}

				state := status.State
				if connInfo != nil {
					state = connInfo.State.String()
				}
				if state == "" {
					if status.Enabled {
						if status.Connected {
							state = "Ready"
						} else {
							state = "Disconnected"
						}
					} else {
						state = "Disabled"
					}
				}

				connecting := strings.EqualFold(state, "connecting")

				// #1064: a quarantined server contributes no available tools.
				// This entry map carries no enabled/quarantined key, so
				// downstream consumers (contracts.ConvertUpstreamStatsToServerStats)
				// cannot gate on it -- the value has to be zeroed here.
				availableToolCount := status.ToolCount
				if status.Quarantined {
					availableToolCount = 0
				}

				entry := map[string]interface{}{
					"state":        state,
					"connected":    status.Connected,
					"connecting":   connecting,
					"retry_count":  status.RetryCount,
					"should_retry": false,
					"name":         status.Name,
					"tool_count":   availableToolCount,
				}

				if entry["name"] == "" {
					entry["name"] = name
				}

				if status.Config != nil {
					if status.Config.URL != "" {
						entry["url"] = status.Config.URL
					}
					if status.Config.Protocol != "" {
						entry["protocol"] = status.Config.Protocol
					}
				}

				if connInfo != nil {
					entry["retry_count"] = connInfo.RetryCount
					if connInfo.LastError != nil {
						entry["last_error"] = connInfo.LastError.Error()
					}
					if !connInfo.LastRetryTime.IsZero() {
						entry["last_retry_time"] = connInfo.LastRetryTime
					}
					if connInfo.ServerName != "" {
						entry["server_name"] = connInfo.ServerName
					}
					if connInfo.ServerVersion != "" {
						entry["server_version"] = connInfo.ServerVersion
					}
				} else {
					if status.LastError != "" {
						entry["last_error"] = status.LastError
					}
					if status.LastErrorTime != nil {
						entry["last_retry_time"] = *status.LastErrorTime
					}
				}

				if status.Connected {
					connectedCount++
				}
				if connecting {
					connectingCount++
				}
				if status.Quarantined {
					quarantinedCount++
				}
				if config.ServerContributesTools(status.Enabled, status.Quarantined) {
					totalTools += status.ToolCount
				}

				serverStats[name] = entry
			}

			return oauth.RedactUpstreamStats(map[string]interface{}{
				"connected_servers":   connectedCount,
				"connecting_servers":  connectingCount,
				"quarantined_servers": quarantinedCount,
				"total_servers":       len(snapshot.Servers),
				"servers":             serverStats,
				"total_tools":         totalTools,
			}, reveal)
		}
	}

	// The fallback masks its own entries (upstream.Manager.GetStats applies the
	// same oauth.RedactUpstreamStatsEntry rule with the same reveal gate), so
	// this path is already redacted by the time it gets here.
	stats := s.runtime.UpstreamManager().GetStats()

	// Enhance stats with tool counts per server when falling back
	if servers, ok := stats["servers"].(map[string]interface{}); ok {
		quarantined := s.quarantinedServerNames()
		for id, serverInfo := range servers {
			if serverMap, ok := serverInfo.(map[string]interface{}); ok {
				// #1064: getServerToolCount reads the managed client's tool-count
				// cache, which no quarantine path invalidates, so it would hand
				// back the pre-quarantine number. Gate it on the config instead.
				if quarantined[id] {
					serverMap["tool_count"] = 0
					continue
				}
				serverMap["tool_count"] = s.getServerToolCount(id)
			}
		}
	}

	return stats
}

// GetAllServers returns information about all upstream servers for tray UI.
// Phase 6: Uses lock-free StateView for instant responses (<1ms) even during tool indexing.
func (s *Server) GetAllServers() ([]map[string]interface{}, error) {
	s.logger.Debug("GetAllServers called (Phase 6: using StateView)")

	// Phase 6: Use Supervisor's StateView for lock-free, instant reads
	supervisor := s.runtime.Supervisor()
	if supervisor == nil {
		s.logger.Warn("GetAllServers: supervisor not available, falling back to storage")
		return s.getAllServersLegacy()
	}

	stateView := supervisor.StateView()
	if stateView == nil {
		s.logger.Warn("GetAllServers: StateView not available, falling back to storage")
		return s.getAllServersLegacy()
	}

	// Get snapshot - this is lock-free and instant
	snapshot := stateView.Snapshot()
	s.logger.Debug("StateView snapshot retrieved", zap.Int("count", len(snapshot.Servers)))

	result := make([]map[string]interface{}, 0, len(snapshot.Servers))
	for _, serverStatus := range snapshot.Servers {
		// Convert StateView ServerStatus to API response format
		connected := serverStatus.Connected
		connecting := serverStatus.State == "connecting"

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

		// Extract created time and config fields from stateview or fall back to storage
		var created time.Time
		var url, command, protocol, workingDir string
		var args []string
		cfg := serverStatus.Config
		if cfg == nil {
			// Stateview Config is nil — fall back to storage for config fields
			if storageManager := s.runtime.StorageManager(); storageManager != nil {
				if stored, err := storageManager.GetUpstreamServer(serverStatus.Name); err == nil && stored != nil {
					cfg = stored
				}
			}
		}
		if cfg != nil {
			created = cfg.Created
			url = cfg.URL
			command = cfg.Command
			args = cfg.Args
			workingDir = cfg.WorkingDir
			protocol = cfg.Protocol
		}

		// Calculate unified health status (Spec 013: Health is single source of truth)
		healthInput := health.HealthCalculatorInput{
			Name:        serverStatus.Name,
			Enabled:     serverStatus.Enabled,
			Quarantined: serverStatus.Quarantined,
			State:       status,
			Connected:   connected,
			LastError:   serverStatus.LastError,
			ToolCount:   serverStatus.ToolCount,
			// Extract missing secret and OAuth config error from last error
			MissingSecret:  health.ExtractMissingSecret(serverStatus.LastError),
			OAuthConfigErr: health.ExtractOAuthConfigError(serverStatus.LastError),
			// Gates the "Edit URL" remedy: a stdio server emits the same address
			// phrases from its own failed network calls but has no URL field to
			// send the user to. Read from the `url` resolved above — NOT from
			// serverStatus.Config, which is nil whenever the stateview has no
			// entry and the config came from the storage fallback. Every
			// CalculateHealth call site must supply this or the surfaces
			// disagree about how to fix the same server.
			HasEndpointURL: url != "",
			// GH #1145: automatic reconnection given up for good. Supplied at
			// every CalculateHealth call site or the surfaces disagree.
			RetryStopped:       serverStatus.RetryStopped,
			RetryStoppedCode:   serverStatus.RetryStoppedCode,
			RetryStoppedReason: serverStatus.RetryStoppedReason,
			RetryCount:         serverStatus.RetryCount,
		}

		// Check if OAuth is required for this server
		if serverStatus.Config != nil && serverStatus.Config.OAuth != nil {
			healthInput.OAuthRequired = true
		}

		// T032: Wire refresh state into health calculation (Spec 023)
		if refreshMgr := s.runtime.RefreshManager(); refreshMgr != nil {
			if refreshState := refreshMgr.GetRefreshState(serverStatus.Name); refreshState != nil {
				healthInput.RefreshState = health.RefreshState(refreshState.State)
				healthInput.RefreshRetryCount = refreshState.RetryCount
				healthInput.RefreshLastError = refreshState.LastError
				healthInput.RefreshNextAttempt = refreshState.NextAttempt
			}
		}

		healthStatus := health.CalculateHealth(healthInput, health.DefaultHealthConfig())

		// #1064: quarantined tools are not available -- see the same guard in
		// Runtime.GetAllServers. GetQuarantinedServers below deliberately keeps
		// the real count: that view exists to tell a reviewer how many tools
		// await approval.
		availableToolCount := serverStatus.ToolCount
		if serverStatus.Quarantined {
			availableToolCount = 0
		}

		serverMap := map[string]interface{}{
			"name":            serverStatus.Name,
			"url":             url,
			"command":         command,
			"args":            args,
			"working_dir":     workingDir,
			"protocol":        protocol,
			"enabled":         serverStatus.Enabled,
			"quarantined":     serverStatus.Quarantined,
			"created":         created,
			"connected":       connected,
			"connecting":      connecting,
			"tool_count":      availableToolCount,
			"last_error":      serverStatus.LastError,
			"status":          status,
			"should_retry":    false, // Managed by Actor internally now
			"retry_count":     serverStatus.RetryCount,
			"last_retry_time": nil,          // Actor tracks this internally
			"health":          healthStatus, // Spec 013: Health is source of truth
		}

		// Spec 086: surface the per-server trust tier on the tray/legacy-REST
		// projection too, so the httpapi fallback path (which runs this map
		// through contracts.ConvertGenericServersToTyped) reads back the same
		// mode as the management-service path. Raw value; omitted when unset.
		if cfg != nil && cfg.TrustMode != "" {
			serverMap["trust_mode"] = cfg.TrustMode
		}

		// GH #1145: automatic reconnection permanently given up. This is the
		// projection the REST API actually serves (internal/server.Server is the
		// httpapi controller), so omitting it here would leave the health text as
		// the only trace of a parked server. Emitted only when set, so the
		// payload shape is unchanged for every other server.
		if serverStatus.RetryStopped {
			serverMap["retry_stopped"] = true
			serverMap["retry_stopped_code"] = serverStatus.RetryStoppedCode
			serverMap["retry_stopped_reason"] = serverStatus.RetryStoppedReason
		}

		// Spec 039: Add security scan summary if available
		if scanSvc := s.securityScannerSvc(); scanSvc != nil {
			scanSummary := scanSvc.GetScanSummary(context.Background(), serverStatus.Name)
			if scanSummary != nil {
				serverMap["security_scan"] = scanSummary
			}
		}

		// Spec 044: include structured diagnostic error + stable error code.
		if serverStatus.Diagnostic != nil {
			d := serverStatus.Diagnostic
			diagMap := map[string]interface{}{
				"code":        string(d.Code),
				"severity":    string(d.Severity),
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

		result = append(result, serverMap)
	}

	s.logger.Debug("GetAllServers completed", zap.Int("server_count", len(result)))
	return result, nil
}

// getAllServersLegacy is the old storage-based implementation, kept as fallback.
// This should rarely be called after Phase 6 integration.
func (s *Server) getAllServersLegacy() ([]map[string]interface{}, error) {
	s.logger.Warn("Using legacy storage-based GetAllServers (slow path)")

	// Check if storage manager is available
	if s.runtime.StorageManager() == nil {
		s.logger.Warn("getAllServersLegacy: storage manager is nil")
		return []map[string]interface{}{}, nil
	}

	servers, err := s.runtime.StorageManager().ListUpstreamServers()
	if err != nil {
		// Handle database closed gracefully
		if strings.Contains(err.Error(), "database not open") || strings.Contains(err.Error(), "closed") {
			s.logger.Debug("Database not available for getAllServersLegacy, returning empty list")
			return []map[string]interface{}{}, nil
		}
		s.logger.Error("ListUpstreamServers failed", zap.Error(err))
		return nil, err
	}

	var result []map[string]interface{}
	for _, server := range servers {
		// Get connection status from upstream manager
		var connected bool
		var connecting bool
		var lastError string
		var toolCount int
		var status string

		if s.runtime.UpstreamManager() != nil {
			if client, exists := s.runtime.UpstreamManager().GetClient(server.Name); exists {
				connectionStatus := client.GetConnectionStatus()
				if c, ok := connectionStatus["connected"].(bool); ok {
					connected = c
				}
				if c, ok := connectionStatus["connecting"].(bool); ok {
					connecting = c
				}
				if e, ok := connectionStatus["last_error"].(string); ok {
					lastError = e
				}
				if st, ok := connectionStatus["state"].(string); ok && st != "" {
					status = st
				}
				if connected {
					toolCount = 0 // Skip slow tool count during indexing
					status = "ready"
				}
			}
		}

		if status == "" {
			if server.Enabled {
				if connecting {
					status = "connecting"
				} else {
					status = "disconnected"
				}
			} else {
				status = "disabled"
			}
		}

		serverMap := map[string]interface{}{
			"name":            server.Name,
			"url":             server.URL,
			"command":         server.Command,
			"protocol":        server.Protocol,
			"enabled":         server.Enabled,
			"quarantined":     server.Quarantined,
			"created":         server.Created,
			"connected":       connected,
			"connecting":      connecting,
			"tool_count":      toolCount,
			"last_error":      lastError,
			"status":          status,
			"should_retry":    false,
			"retry_count":     0,
			"last_retry_time": nil,
		}

		// Spec 086: per-server trust tier in parity with the StateView path.
		if server.TrustMode != "" {
			serverMap["trust_mode"] = server.TrustMode
		}

		result = append(result, serverMap)
	}

	return result, nil
}

// GetQuarantinedServers returns information about quarantined servers for tray UI
func (s *Server) GetQuarantinedServers() ([]map[string]interface{}, error) {
	s.logger.Debug("GetQuarantinedServers called (Phase 7.1: using StateView)")

	// Phase 7.1: Use StateView for lock-free read
	supervisor := s.runtime.Supervisor()
	if supervisor == nil {
		s.logger.Warn("Supervisor not available, returning empty list")
		return []map[string]interface{}{}, nil
	}

	snapshot := supervisor.StateView().Snapshot()

	result := make([]map[string]interface{}, 0)
	for _, serverStatus := range snapshot.Servers {
		if !serverStatus.Quarantined {
			continue
		}

		// Extract config fields
		var created time.Time
		var url, command, protocol string
		if serverStatus.Config != nil {
			created = serverStatus.Config.Created
			url = serverStatus.Config.URL
			command = serverStatus.Config.Command
			protocol = serverStatus.Config.Protocol
		}

		result = append(result, map[string]interface{}{
			"name":        serverStatus.Name,
			"url":         url,
			"command":     command,
			"protocol":    protocol,
			"enabled":     serverStatus.Enabled,
			"quarantined": true,
			"created":     created,
			"connected":   serverStatus.Connected,
			"tool_count":  serverStatus.ToolCount,
		})

		s.logger.Debug("Added quarantined server to result",
			zap.String("server", serverStatus.Name))
	}

	s.logger.Debug("GetQuarantinedServers completed",
		zap.Int("total_result_count", len(result)))

	return result, nil
}

// UnquarantineServer removes a server from quarantine via tray UI
func (s *Server) UnquarantineServer(serverName string) error {
	return s.QuarantineServer(serverName, false)
}

// AddServer adds a new upstream server to the configuration.
// New servers are quarantined by default for security.
func (s *Server) AddServer(ctx context.Context, serverConfig *config.ServerConfig) error {
	s.logger.Info("Adding upstream server",
		zap.String("name", serverConfig.Name),
		zap.String("protocol", serverConfig.Protocol),
		zap.Bool("enabled", serverConfig.Enabled),
		zap.Bool("quarantined", serverConfig.Quarantined))

	// Check if server already exists
	storageManager := s.runtime.StorageManager()
	existing, err := storageManager.GetUpstreamServer(serverConfig.Name)
	if err == nil && existing != nil {
		return fmt.Errorf("server '%s' already exists", serverConfig.Name)
	}

	// Set creation timestamp
	serverConfig.Created = time.Now()

	// Save to storage
	if err := storageManager.SaveUpstreamServer(serverConfig); err != nil {
		return fmt.Errorf("failed to save server to storage: %w", err)
	}

	// Update runtime config.
	// runtime.Config() returns the live immutable snapshot, which background
	// goroutines (e.g. LoadConfiguredServers, DiscoverAndIndexTools) may be
	// ranging over concurrently. Mutating its Servers slice in place is a data
	// race, so copy-on-write: clone the config and its server list, append to
	// the clone, then publish atomically via UpdateConfig.
	currentConfig := s.runtime.Config()
	if currentConfig != nil {
		updatedConfig := *currentConfig
		updatedConfig.Servers = append(append([]*config.ServerConfig(nil), currentConfig.Servers...), serverConfig)
		s.runtime.UpdateConfig(&updatedConfig, "")
	}

	// Save configuration to file
	if err := s.SaveConfiguration(); err != nil {
		s.logger.Warn("Failed to save configuration after adding server",
			zap.Error(err))
	}

	// Notify about upstream server change
	s.OnUpstreamServerChange()

	// If quarantined, grant inspection exemption so tools can be discovered and reviewed
	if serverConfig.Quarantined {
		if supervisor := s.runtime.Supervisor(); supervisor != nil {
			// Grant 1 hour exemption for initial tool review
			if err := supervisor.RequestInspectionExemption(serverConfig.Name, 1*time.Hour); err != nil {
				s.logger.Warn("Failed to grant inspection exemption for new quarantined server",
					zap.String("server", serverConfig.Name),
					zap.Error(err))
			} else {
				s.logger.Info("Granted inspection exemption for new quarantined server — tools will be discoverable for review",
					zap.String("server", serverConfig.Name))
			}
		}
	}

	s.logger.Info("Server added successfully",
		zap.String("name", serverConfig.Name))

	return nil
}

// UpdateServer applies partial updates to an existing upstream server configuration.
func (s *Server) UpdateServer(ctx context.Context, serverName string, updates *config.ServerConfig) error {
	s.logger.Info("Updating upstream server", zap.String("name", serverName))

	storageManager := s.runtime.StorageManager()
	existing, err := storageManager.GetUpstreamServer(serverName)
	if err != nil || existing == nil {
		return fmt.Errorf("server '%s' not found", serverName)
	}

	// Apply non-zero/non-nil fields from updates
	if updates.URL != "" {
		existing.URL = updates.URL
	}
	if updates.Command != "" {
		existing.Command = updates.Command
	}
	if updates.Args != nil {
		existing.Args = updates.Args
	}
	if updates.Env != nil {
		existing.Env = updates.Env
	}
	if updates.Headers != nil {
		existing.Headers = updates.Headers
	}
	if updates.WorkingDir != "" {
		existing.WorkingDir = updates.WorkingDir
	}
	if updates.Protocol != "" {
		existing.Protocol = updates.Protocol
	}
	// Booleans are always applied since the handler only calls UpdateServer
	// when the caller explicitly provided these fields
	existing.Enabled = updates.Enabled
	existing.Quarantined = updates.Quarantined
	existing.ReconnectOnUse = updates.ReconnectOnUse

	// AutoApproveToolChanges is a tri-state *bool (MCP-2940): nil means
	// "leave unchanged" so callers that don't touch it (e.g. config-to-secret)
	// don't reset it. A non-nil pointer (including a pointer to false) is
	// applied. The PATCH handler preserves the existing pointer when the
	// request omits the field, so this nil-guard is the second half of the
	// nil-preserve contract.
	if updates.AutoApproveToolChanges != nil {
		existing.AutoApproveToolChanges = updates.AutoApproveToolChanges
	}

	// TrustMode (spec 086) is a plain string: empty means "leave unchanged"; a
	// non-empty value is applied. The PATCH handler preserves the existing value
	// when the request omits the field, so this empty-guard is the second half of
	// the leave-unchanged contract.
	if updates.TrustMode != "" {
		existing.TrustMode = updates.TrustMode
	}

	// InitTimeout (MCP-3322) is a tri-state *Duration: nil means "leave
	// unchanged"; a non-nil pointer is applied. The PATCH handler preserves the
	// existing pointer when the request omits the field, so this nil-guard is
	// the second half of the nil-preserve contract.
	if updates.InitTimeout != nil {
		existing.InitTimeout = updates.InitTimeout
	}

	// Isolation is PATCH-semantic: nil means "leave unchanged"; a present
	// struct REPLACES the override set wholesale.
	//
	// The old field-by-field non-zero merge here could not express a clear —
	// an empty image or a nil `enabled` was indistinguishable from "not
	// supplied" — which is half of why an inheriting server could never be
	// restored once something wrote an explicit opt-out (GH #1142). The REST
	// handler now resolves the patch against the persisted overrides
	// (IsolationRequest.resolve) and hands over a complete struct, so replacing
	// is both correct and the only way clears can work.
	if updates.Isolation != nil {
		existing.Isolation = config.CopyIsolationConfig(updates.Isolation)
	}

	// Save to storage
	if err := storageManager.SaveUpstreamServer(existing); err != nil {
		return fmt.Errorf("failed to save server: %w", err)
	}

	// Update runtime config
	currentConfig := s.runtime.Config()
	if currentConfig != nil {
		for i, sc := range currentConfig.Servers {
			if sc.Name == serverName {
				currentConfig.Servers[i] = existing
				break
			}
		}
		s.runtime.UpdateConfig(currentConfig, "")
	}

	// Save configuration to file
	if err := s.SaveConfiguration(); err != nil {
		s.logger.Warn("Failed to save configuration after updating server", zap.Error(err))
	}

	// Notify about change
	s.OnUpstreamServerChange()

	s.logger.Info("Server updated successfully", zap.String("name", serverName))

	return nil
}

// RemoveServer removes an upstream server from the configuration.
// This stops the server if running and removes it from storage.
func (s *Server) RemoveServer(ctx context.Context, serverName string) error {
	s.logger.Info("Removing upstream server", zap.String("name", serverName))

	// Check if server exists
	storageManager := s.runtime.StorageManager()
	existing, err := storageManager.GetUpstreamServer(serverName)
	if err != nil || existing == nil {
		return fmt.Errorf("server '%s' not found", serverName)
	}

	// Remove from upstream manager (stops the server)
	s.runtime.UpstreamManager().RemoveServer(serverName)

	// Remove from storage
	if err := storageManager.RemoveUpstream(serverName); err != nil {
		return fmt.Errorf("failed to remove server from storage: %w", err)
	}

	// Clear OAuth state (tokens, client registration) for the removed server
	// This prevents orphaned tokens from accumulating in the database
	if err := storageManager.ClearOAuthState(serverName); err != nil {
		s.logger.Warn("Failed to clear OAuth state for removed server",
			zap.String("server", serverName),
			zap.Error(err))
		// Continue - this is cleanup, not critical for removal
	}

	// Notify RefreshManager to stop tracking this server's token refresh
	if refreshManager := s.runtime.RefreshManager(); refreshManager != nil {
		refreshManager.OnTokenCleared(serverName)
	}

	// Remove from search index
	if err := s.runtime.IndexManager().DeleteServerTools(serverName); err != nil {
		s.logger.Warn("Failed to remove server tools from index",
			zap.String("server", serverName),
			zap.Error(err))
	}

	// Clean up tool approval records for the removed server
	// This prevents orphaned approval records from accumulating
	if err := storageManager.DeleteServerToolApprovals(serverName); err != nil {
		s.logger.Warn("Failed to clear tool approvals for removed server",
			zap.String("server", serverName),
			zap.Error(err))
	}

	// Save configuration to file
	if err := s.SaveConfiguration(); err != nil {
		s.logger.Warn("Failed to save configuration after removing server",
			zap.Error(err))
	}

	// Notify about upstream server change
	s.OnUpstreamServerChange()

	s.logger.Info("Server removed successfully",
		zap.String("name", serverName))

	return nil
}

// EnableServer enables/disables a server and ensures all state is synchronized.
// It acts as the entry point for changes originating from the UI or API.
func (s *Server) EnableServer(serverName string, enabled bool) error {
	return s.runtime.EnableServer(serverName, enabled)
}

// RestartServer restarts an upstream server
func (s *Server) RestartServer(serverName string) error {
	return s.runtime.RestartServer(serverName)
}

// DiscoverServerTools triggers manual tool discovery for a specific server.
// This backs the explicit REST operator actions (POST .../discover-tools and
// its .../refresh alias), so it uses the AUTHORITATIVE refresh path (issue
// #873): a server that now reports zero tools has its stale index entries
// removed rather than silently retained.
func (s *Server) DiscoverServerTools(ctx context.Context, serverName string) error {
	s.logger.Info("Manual tool discovery requested", zap.String("server", serverName))
	return s.runtime.RefreshServerTools(ctx, serverName)
}

// ForceReconnectAllServers triggers reconnection attempts for all managed servers.
func (s *Server) ForceReconnectAllServers(reason string) error {
	s.logger.Info("HTTP API requested force reconnect for all upstream servers",
		zap.String("reason", reason))
	return s.runtime.ForceReconnectAllServers(reason)
}

// QuarantineServer quarantines/unquarantines a server
func (s *Server) QuarantineServer(serverName string, quarantined bool) error {
	return s.runtime.QuarantineServer(serverName, quarantined)
}

// getServerToolCount returns the number of tools for a specific server
// Returns cached tool count only (non-blocking) to avoid stalling SSE/API responses
// quarantinedServerNames returns the set of configured server names currently in
// quarantine. Used to gate cached tool counts on the GetUpstreamStats fallback
// path, where neither the manager stats entry nor getServerToolCount has access
// to the server config. Issue #1064.
func (s *Server) quarantinedServerNames() map[string]bool {
	out := make(map[string]bool)
	cfg := s.runtime.Config()
	if cfg == nil {
		return out
	}
	for _, srv := range cfg.Servers {
		if srv != nil && srv.Quarantined {
			out[srv.Name] = true
		}
	}
	return out
}

func (s *Server) getServerToolCount(serverID string) int {
	client, exists := s.runtime.UpstreamManager().GetClient(serverID)
	if !exists {
		return 0
	}

	// Get the cached tool count directly without any blocking calls
	// This is safe to call from SSE/API handlers as it only reads from cache
	count := client.GetCachedToolCountNonBlocking()

	return count
}

// StartServer starts the server if it's not already running
func (s *Server) StartServer(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("server is already running")
	}

	// CRITICAL: Validate data directory security BEFORE starting background goroutine
	// This ensures permission errors are returned synchronously with proper exit codes
	cfg := s.runtime.Config()
	if cfg != nil && cfg.DataDir != "" {
		if err := ValidateDataDirectory(cfg.DataDir, s.logger); err != nil {
			s.logger.Error("Data directory security validation failed",
				zap.Error(err),
				zap.String("fix", fmt.Sprintf("chmod 0700 %s", cfg.DataDir)))
			return &PermissionError{Path: cfg.DataDir, Err: err}
		}
	}

	// Cancel the old context before creating a new one to avoid race conditions
	if s.serverCancel != nil {
		s.serverCancel()
	}

	s.serverCtx, s.serverCancel = context.WithCancel(ctx)

	// MCP-32: project runtime events onto Prometheus metrics while running.
	if s.observability != nil && s.observability.Metrics() != nil {
		go s.runMetricsBridge(s.serverCtx, s.observability.Metrics())
	}

	go func() {
		var serverError error

		defer func() {
			s.mu.Lock()
			s.running = false
			s.listenAddr = ""
			s.mu.Unlock()
			s.runtime.SetRunning(false)

			// Only send "Stopped" status if there was no error
			// If there was an error, the error status should remain.
			// errors.Is: graceful cancellation may arrive wrapped
			// (e.g. "MCP Streamable HTTP server error: context canceled").
			if serverError == nil || errors.Is(serverError, context.Canceled) {
				s.updateStatus(runtime.PhaseStopped, "Server has stopped")
			}
		}()

		s.mu.Lock()
		s.running = true
		s.mu.Unlock()
		s.runtime.SetRunning(true)

		// Notify about server start
		s.updateStatus(runtime.PhaseStarting, "Server is starting...")

		serverError = s.Start(s.serverCtx)
		if serverError != nil && !errors.Is(serverError, context.Canceled) {
			s.logger.Error("Server error during background start", zap.Error(serverError))
			s.updateStatus(runtime.PhaseError, fmt.Sprintf("Server error: %v", serverError))
			// Deliver the fatal error to ServeErr() so the process can
			// exit (e.g. exit code 2 on port conflict) instead of
			// lingering with no listeners. Non-blocking: buffered cap 1
			// tolerates restarts when nobody is draining the channel.
			select {
			case s.serveErrCh <- serverError:
			default:
			}
		}
	}()

	return nil
}

// ServeErr delivers a fatal serve failure: a startup bind error (e.g.
// *PortInUseError) or a serve-loop death after start. Graceful shutdown
// (context cancellation, http.ErrServerClosed) never fires it.
func (s *Server) ServeErr() <-chan error {
	return s.serveErrCh
}

// StopServer stops the server if it's running
func (s *Server) StopServer() error {
	s.logger.Info("STOPSERVER CALLED - STARTING SHUTDOWN SEQUENCE")
	_ = s.logger.Sync()

	s.mu.Lock()
	// Check if Shutdown() has already been called - prevent duplicate shutdown
	if s.shutdown {
		s.mu.Unlock()
		s.logger.Debug("Server shutdown already in progress via Shutdown(), skipping StopServer")
		return nil
	}
	if !s.running {
		s.mu.Unlock()
		// Return nil instead of error to prevent race condition logs
		s.logger.Debug("Server stop requested but server is not running")
		return nil
	}

	// Capture httpServer reference while holding the lock
	httpServer := s.httpServer
	s.mu.Unlock()

	// Notify about server stopping
	s.logger.Info("STOPSERVER - Server is running, proceeding with stop")
	_ = s.logger.Sync()

	s.updateStatus(runtime.PhaseStopping, "Server is stopping...")

	// STEP 1: Gracefully shutdown HTTP server FIRST to stop accepting new connections
	// This must happen before we disconnect upstream servers to prevent new requests
	if httpServer != nil {
		s.logger.Info("STOPSERVER - Shutting down HTTP server gracefully")
		_ = s.logger.Sync()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			s.logger.Warn("STOPSERVER - HTTP server forced shutdown due to timeout", zap.Error(err))
			// Force close if graceful shutdown times out
			if closeErr := httpServer.Close(); closeErr != nil {
				s.logger.Error("STOPSERVER - Failed to force close HTTP server", zap.Error(closeErr))
			}
		} else {
			s.logger.Info("STOPSERVER - HTTP server shutdown completed gracefully")
		}
		_ = s.logger.Sync()
	}

	// STEP 2: Disconnect upstream servers AFTER HTTP server is shut down
	// This ensures no new requests can come in while we're disconnecting
	// Use a FRESH context (not the cancelled server context) for cleanup
	s.logger.Info("STOPSERVER - Disconnecting upstream servers with parallel cleanup")
	_ = s.logger.Sync()

	// NEW: Create dedicated cleanup context with generous timeout (45 seconds)
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cleanupCancel()

	// NEW: Use ShutdownAll for parallel cleanup with proper container verification
	if err := s.runtime.UpstreamManager().ShutdownAll(cleanupCtx); err != nil {
		s.logger.Error("STOPSERVER - Failed to shutdown upstream servers", zap.Error(err))
		_ = s.logger.Sync()
	} else {
		s.logger.Info("STOPSERVER - Successfully shutdown all upstream servers")
		_ = s.logger.Sync()
	}

	// NEW: Verify all containers stopped with retry loop (instead of arbitrary 3s sleep).
	// Gated on Docker isolation actually being in use — otherwise the `docker ps`
	// probe + verifyContainersCleanedUp loop are pure waste (and add ~17s per
	// Stop in test processes). No isolation ⇒ no managed containers ⇒ nothing to verify.
	if s.runtime.UpstreamManager().UsesDockerIsolation() && s.runtime.UpstreamManager().HasDockerContainers() {
		s.logger.Warn("STOPSERVER - Docker containers still running, verifying cleanup...")
		_ = s.logger.Sync()
		s.verifyContainersCleanedUp(cleanupCtx)
	} else {
		s.logger.Info("STOPSERVER - All Docker containers cleaned up successfully")
		_ = s.logger.Sync()
	}

	// STEP 3: Cancel the server context to signal other components
	s.logger.Info("STOPSERVER - Cancelling server context")
	_ = s.logger.Sync()
	s.mu.Lock()
	if s.serverCancel != nil {
		s.serverCancel()
	}

	// Set running to false immediately after server is shut down
	s.running = false
	s.listenAddr = ""
	s.mu.Unlock()
	s.runtime.SetRunning(false)

	// Notify about server stopped with explicit status update
	s.updateStatus(runtime.PhaseStopped, "Server has been stopped")

	s.logger.Info("STOPSERVER - All operations completed successfully")
	_ = s.logger.Sync() // Final log flush

	return nil
}

// verifyContainersCleanedUp verifies all Docker containers have stopped and forces cleanup if needed
func (s *Server) verifyContainersCleanedUp(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	maxAttempts := 15 // 15 seconds total
	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			s.logger.Error("STOPSERVER - Cleanup verification timeout", zap.Error(ctx.Err()))
			_ = s.logger.Sync()
			// Force cleanup as last resort
			s.runtime.UpstreamManager().ForceCleanupAllContainers()
			return
		case <-ticker.C:
			if !s.runtime.UpstreamManager().HasDockerContainers() {
				s.logger.Info("STOPSERVER - All containers cleaned up successfully",
					zap.Int("attempts", attempt+1))
				_ = s.logger.Sync()
				return
			}
			s.logger.Debug("STOPSERVER - Waiting for container cleanup...",
				zap.Int("attempt", attempt+1),
				zap.Int("max_attempts", maxAttempts))
		}
	}

	// Timeout reached - force cleanup
	s.logger.Error("STOPSERVER - Some containers failed to stop gracefully - forcing cleanup")
	_ = s.logger.Sync()
	s.runtime.UpstreamManager().ForceCleanupAllContainers()

	// Give force cleanup a moment to complete
	time.Sleep(2 * time.Second)

	if s.runtime.UpstreamManager().HasDockerContainers() {
		s.logger.Error("STOPSERVER - WARNING: Some containers may still be running after force cleanup")
		_ = s.logger.Sync()
	} else {
		s.logger.Info("STOPSERVER - Force cleanup succeeded - all containers removed")
		_ = s.logger.Sync()
	}
}

func resolveDisplayAddress(actual, requested string) string {
	host, port, err := net.SplitHostPort(actual)
	if err != nil {
		return actual
	}

	if host == "" || host == "::" || host == "0.0.0.0" {
		if reqHost, _, reqErr := net.SplitHostPort(requested); reqErr == nil {
			if reqHost != "" && reqHost != "::" && reqHost != "0.0.0.0" {
				host = reqHost
			} else {
				host = "127.0.0.1"
			}
		} else {
			host = "127.0.0.1"
		}
	}

	return net.JoinHostPort(host, port)
}

// withHSTS adds HTTP Strict Transport Security headers
func withHSTS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		next.ServeHTTP(w, r)
	})
}

// profileMiddleware is an http.Handler middleware registered at /mcp/p/.
// It resolves the profile slug from the URL path, looks up the profile in the
// current config snapshot (lock-free, hot-reload safe), builds a ProfileScope,
// injects it into the request context, then delegates to the retrieve_tools-mode
// MCP handler (next). Auth has already run at this point via mcpAuthMiddleware.
//
// 404 responses:
//   - No profiles configured at all → {"error":"no profiles configured"}
//   - Slug not found               → {"error":"unknown profile '<slug>'","available":[...]}
func (s *Server) profileMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.runtime.Config()

		// FR-008: no profiles configured.
		if cfg == nil || len(cfg.Profiles) == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "no profiles configured",
			})
			return
		}

		// Strip the /mcp/p/ prefix to obtain the slug.
		slug := strings.TrimPrefix(r.URL.Path, "/mcp/p/")
		slug = strings.TrimPrefix(slug, "/mcp/p") // handle /mcp/p with no trailing slash
		slug = strings.Trim(slug, "/")

		// Profiles v2 T3: a profile-pinned agent token may only operate within its
		// pinned profile. A request to any other /mcp/p/<slug> is forbidden (403),
		// regardless of whether that slug is a real profile. Auth has already run
		// (mcpAuthMiddleware wraps this handler), so the pin is on the context.
		if pin := profilePinFromContext(r.Context()); pin != "" && pin != slug {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": fmt.Sprintf("agent token is pinned to profile '%s' and cannot access profile '%s'", pin, slug),
			})
			return
		}

		// Look up profile by slug (lock-free snapshot).
		var found *config.ProfileConfig
		for i := range cfg.Profiles {
			if cfg.Profiles[i].Name == slug {
				found = &cfg.Profiles[i]
				break
			}
		}

		// FR-009: slug not found.
		if found == nil {
			available := make([]string, 0, len(cfg.Profiles))
			for _, p := range cfg.Profiles {
				available = append(available, p.Name)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":     fmt.Sprintf("unknown profile '%s'", slug),
				"available": available,
			})
			return
		}

		// Build scope from the effective server set (unknown-server warn-skip applied).
		effectiveServers := found.EffectiveServers(cfg)
		scope := profile.NewProfileScope(found.Name, effectiveServers)
		ctx := profile.WithProfileScope(r.Context(), scope)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// startCustomHTTPServer creates a custom HTTP server that handles MCP endpoints
// It supports both TCP (for browsers) and Unix socket/named pipe (for tray) listeners
// registerHTTPHandlers forwards the REST API, SSE events, health endpoints,
// and the observability /metrics endpoint from the outer http.ServeMux to the
// httpapi chi router (httpAPIServer).
//
// MCP-3135: /metrics is registered on the chi router (httpapi.setupRoutes), but
// the outer mux must explicitly forward it — otherwise GET /metrics returns 404
// even when metrics are enabled. The forward is gated on metrics actually being
// enabled so a disabled deployment keeps /metrics unrouted (404).
func (s *Server) registerHTTPHandlers(mux *http.ServeMux, httpAPIServer http.Handler) {
	mux.Handle("/api/", httpAPIServer)
	// /events is a long-lived SSE stream: it must escape http.Server.WriteTimeout
	// (and, being a GET, ReadTimeout) or the connection dies mid-stream (GH #965).
	mux.Handle("/events", s.streamingNoDeadline(httpAPIServer))
	// POST /api/v1/code/exec runs caller-supplied code for up to its own
	// timeout_ms budget (600s), well past both http.Server deadlines: at 120s
	// by default WriteTimeout destroys the response and ReadTimeout cancels the
	// request context mid-execution. Unlike the streaming routes this one is a
	// bounded POST, so it gets a wider deadline rather than none at all.
	mux.Handle(codeExecAPIPath, s.extendedDeadline(codeExecRequestDeadline, httpAPIServer))

	// Mount health endpoints directly on main mux at root level
	healthEndpoints := []string{"/healthz", "/readyz", "/livez", "/ready", "/health"}
	for _, endpoint := range healthEndpoints {
		mux.Handle(endpoint, httpAPIServer)
	}

	s.logger.Info("Registered REST API endpoints", zap.Strings("api_endpoints", []string{"/api/v1/*", "/events"}))
	s.logger.Info("Registered health endpoints", zap.Strings("health_endpoints", healthEndpoints))

	// MCP-32/MCP-3135: forward /metrics to the chi router only when the
	// Prometheus exporter is enabled. Without this the handler registered in
	// httpapi.setupRoutes is unreachable through the outer mux.
	if s.observability != nil && s.observability.Metrics() != nil {
		mux.Handle("/metrics", httpAPIServer)
		s.logger.Info("Registered metrics endpoint", zap.String("endpoint", "/metrics"))
	}
}

const (
	// codeExecAPIPath is the REST code-execution endpoint, the one route whose
	// handler legitimately outlives the process-wide http.Server deadlines.
	codeExecAPIPath = "/api/v1/code/exec"

	// codeExecRequestDeadline is the wall-clock budget that route gets: the
	// longest timeout_ms the code_execution tool accepts (600s) plus slack for
	// reading the request and writing the reply. It matches the router-level
	// budget in internal/httpapi so neither layer is the one that ends a
	// legal execution.
	codeExecRequestDeadline = 630 * time.Second
)

// extendedDeadline widens the per-request read and write deadlines a route
// inherits from http.Server.ReadTimeout/WriteTimeout, for handlers that
// legitimately run longer than the process-wide defaults.
//
// Unlike streamingNoDeadline this sets an absolute deadline instead of
// clearing it: the routes that need it are bounded requests, so they stay
// bounded — including a deployment that disabled the deadlines entirely, where
// this caps the route at d. It must likewise be the OUTERMOST wrapper on a
// route (http.NewResponseController only unwraps ResponseWriters implementing
// Unwrap), and a controller error is logged at Debug and ignored: failing to
// relax a deadline must never fail the request.
func (s *Server) extendedDeadline(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline := time.Now().Add(d)
		rc := http.NewResponseController(w)
		if err := rc.SetWriteDeadline(deadline); err != nil {
			s.logger.Debug("Could not extend the write deadline for a long-running route",
				zap.String("path", r.URL.Path),
				zap.Error(err))
		}
		if err := rc.SetReadDeadline(deadline); err != nil {
			s.logger.Debug("Could not extend the read deadline for a long-running route",
				zap.String("path", r.URL.Path),
				zap.Error(err))
		}
		next.ServeHTTP(w, r)
	})
}

// httpServerTimeouts resolves the Read/Write/Idle deadlines applied to the
// main http.Server (GH #965). Extracted as a pure function so the policy is
// unit-testable without binding a socket. A nil config yields the built-in
// defaults (120s read / 120s write / 180s idle). The write deadline is a
// wall-clock cap on a whole response, so the streaming routes opt out of it
// per-request via streamingNoDeadline rather than the process disabling it
// globally. ReadHeaderTimeout is NOT configurable and stays at 60s: it is the
// deadline that actually defends against slowloris, and nothing legitimate
// needs it relaxed.
func httpServerTimeouts(cfg *config.Config) (read, write, idle time.Duration) {
	if cfg == nil {
		cfg = &config.Config{} // all-nil pointers → the built-in defaults
	}
	return cfg.ResolveHTTPReadTimeout(), cfg.ResolveHTTPWriteTimeout(), cfg.ResolveHTTPIdleTimeout()
}

// streamingNoDeadline clears the per-request write deadline (and, for
// body-less GET/HEAD streams, the read deadline) inherited from
// http.Server.WriteTimeout/ReadTimeout, so long tool calls and long-lived SSE
// streams are never truncated mid-response (GH #965).
//
// Only the streaming routes are wrapped — the MCP endpoints and /events. Every
// other route (REST, Web UI, health, /metrics) keeps the configured write
// deadline, which is what protects a non-loopback deployment from slow readers.
// The read deadline is cleared for GET/HEAD only: those have no request body,
// whereas a POST must keep its read deadline so a slow-body upload stays
// bounded.
//
// This must be the OUTERMOST wrapper on a route: http.NewResponseController
// unwraps only ResponseWriters that implement Unwrap() http.ResponseWriter, so
// running it before any logging/auth wrappers guarantees it reaches the real
// connection. A controller error (e.g. http.ErrNotSupported behind a
// non-cooperating ResponseWriter) is logged at Debug and otherwise ignored:
// failing to relax a deadline must never fail the request.
func (s *Server) streamingNoDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			s.logger.Debug("Could not clear the write deadline for a streaming route",
				zap.String("path", r.URL.Path),
				zap.Error(err))
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if err := rc.SetReadDeadline(time.Time{}); err != nil {
				s.logger.Debug("Could not clear the read deadline for a streaming route",
					zap.String("path", r.URL.Path),
					zap.Error(err))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) startCustomHTTPServer(ctx context.Context, streamableServer *server.StreamableHTTPServer) error {
	cfg := s.runtime.Config()
	if cfg == nil {
		return fmt.Errorf("configuration not available")
	}

	// CRITICAL: Validate data directory security before starting
	if err := ValidateDataDirectory(cfg.DataDir, s.logger); err != nil {
		s.logger.Error("Data directory security validation failed",
			zap.Error(err),
			zap.String("fix", fmt.Sprintf("chmod 0700 %s", cfg.DataDir)))
		return &PermissionError{Path: cfg.DataDir, Err: err}
	}

	// Create listener manager
	listenerManager := NewListenerManager(&ListenerConfig{
		DataDir:      cfg.DataDir,
		TrayEndpoint: cfg.TrayEndpoint, // From config/CLI/env or auto-detect
		TCPAddress:   cfg.Listen,
		Logger:       s.logger,
	})

	// Create TCP listener (for browsers and remote clients)
	tcpListener, err := listenerManager.CreateTCPListener()
	if err != nil {
		return err
	}

	// Create tray listener (Unix socket or named pipe) if enabled
	var trayListener *Listener
	if cfg.EnableSocket {
		trayListener, err = listenerManager.CreateTrayListener()
		if err != nil {
			socketPath := cfg.TrayEndpoint
			if socketPath == "" {
				socketPath = filepath.Join(cfg.DataDir, "mcpproxy.sock")
			}
			s.logger.Warn("Failed to create tray/CLI socket listener; tray and CLI will fall back to TCP (API key required)",
				zap.Error(err),
				zap.String("socket_path", socketPath))
			// Continue without tray listener - tray and CLI fall back to TCP
		}
	} else {
		s.logger.Info("Socket communication disabled by configuration, clients will use TCP")
	}

	// Store listener manager for cleanup
	s.mu.Lock()
	s.listenerManager = listenerManager
	s.mu.Unlock()

	mux := http.NewServeMux()

	// Create a logging wrapper for debugging client connections
	loggingHandler := func(handler http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Extract connection source from context
			source := GetConnectionSource(r.Context())

			// Log incoming request with connection details
			s.logger.Debug("MCP client request received",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr),
				zap.String("source", string(source)),
				zap.String("user_agent", r.UserAgent()),
				zap.String("content_type", r.Header.Get("Content-Type")),
				zap.String("connection", r.Header.Get("Connection")),
				zap.Int64("content_length", r.ContentLength),
			)

			// Create response writer wrapper to capture status and errors
			wrappedWriter := &responseWriter{ResponseWriter: w, statusCode: 200}

			// Handle the request
			handler.ServeHTTP(wrappedWriter, r)

			duration := time.Since(start)

			// Log response with timing and status
			if wrappedWriter.statusCode >= 400 {
				s.logger.Warn("MCP client request completed with error",
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.String("remote_addr", r.RemoteAddr),
					zap.String("source", string(source)),
					zap.Int("status_code", wrappedWriter.statusCode),
					zap.Duration("duration", duration),
				)
			} else {
				s.logger.Debug("MCP client request completed successfully",
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.String("remote_addr", r.RemoteAddr),
					zap.String("source", string(source)),
					zap.Int("status_code", wrappedWriter.statusCode),
					zap.Duration("duration", duration),
				)
			}
		})
	}

	// Standard MCP endpoint according to the specification
	// Wrap with auth middleware to inject AuthContext for agent token scope enforcement.
	// hostValidationMiddleware (outermost security wrapper) replaces mcp-go's DNS-rebinding
	// protection — disabled on every StreamableHTTPServer below — adding the
	// trusted_hosts allowlist for reverse-proxy deployments (GH #898).
	//
	// streamingNoDeadline is the OUTERMOST wrapper on every MCP route so a tool
	// call slower than http.Server.WriteTimeout is not truncated (GH #965).
	mcpHandler := s.streamingNoDeadline(s.hostValidationMiddleware(s.mcpAuthMiddleware(loggingHandler(streamableServer))))
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler) // Handle trailing slash

	// Routing mode dedicated endpoints (Spec 031)
	// Each endpoint always serves its specific routing mode regardless of config.
	// /mcp/all → direct mode (all tools with serverName__toolName naming)
	directStreamable := server.NewStreamableHTTPServer(s.mcpProxy.GetMCPServerForMode(config.RoutingModeDirect),
		server.WithDisableLocalhostProtection(true))
	directHandler := s.streamingNoDeadline(s.hostValidationMiddleware(s.mcpAuthMiddleware(loggingHandler(directStreamable))))
	mux.Handle("/mcp/all", directHandler)
	mux.Handle("/mcp/all/", directHandler)

	// /mcp/code → code_execution mode (JS orchestration)
	codeExecStreamable := server.NewStreamableHTTPServer(s.mcpProxy.GetMCPServerForMode(config.RoutingModeCodeExecution),
		server.WithDisableLocalhostProtection(true))
	codeExecHandler := s.streamingNoDeadline(s.hostValidationMiddleware(s.mcpAuthMiddleware(loggingHandler(codeExecStreamable))))
	mux.Handle("/mcp/code", codeExecHandler)
	mux.Handle("/mcp/code/", codeExecHandler)

	// /mcp/call → retrieve_tools mode (focused: retrieve_tools + call_tool_read/write/destructive)
	callToolStreamable := server.NewStreamableHTTPServer(s.mcpProxy.GetMCPServerForMode(config.RoutingModeRetrieveTools),
		server.WithDisableLocalhostProtection(true))
	callToolHandler := s.streamingNoDeadline(s.hostValidationMiddleware(s.mcpAuthMiddleware(loggingHandler(callToolStreamable))))
	mux.Handle("/mcp/call", callToolHandler)
	mux.Handle("/mcp/call/", callToolHandler)

	// /mcp/p/<slug> → profile-scoped retrieve_tools mode (Spec 057)
	// Profile resolution is done by profileMiddleware which runs AFTER mcpAuthMiddleware
	// so that agent-token scope can compose downstream with the profile scope.
	profileStreamable := server.NewStreamableHTTPServer(s.mcpProxy.GetMCPServerForMode(config.RoutingModeRetrieveTools),
		server.WithDisableLocalhostProtection(true))
	profileHandler := s.streamingNoDeadline(s.hostValidationMiddleware(s.mcpAuthMiddleware(s.profileMiddleware(loggingHandler(profileStreamable)))))
	mux.Handle("/mcp/p/", profileHandler)
	mux.Handle("/mcp/p", profileHandler)

	s.logger.Info("Registered routing mode MCP endpoints",
		zap.String("default_mode", cfg.RoutingMode),
		zap.Strings("endpoints", []string{"/mcp/all", "/mcp/code", "/mcp/call", "/mcp/p/<slug>"}))

	// Legacy endpoints for backward compatibility
	mux.Handle("/v1/tool_code", mcpHandler)
	mux.Handle("/v1/tool-code", mcpHandler) // Alias for python client

	// API v1 endpoints with chi router for REST API and SSE.
	// MCP-32: pass the observability manager so /metrics is served (and HTTP
	// request metrics/tracing middleware applied) when enabled.
	httpAPIServer := httpapi.NewServer(s, s.logger.Sugar(), s.observability)
	// Wire agent token management (Spec 028)
	if sm := s.runtime.StorageManager(); sm != nil {
		cfg := s.runtime.Config()
		dataDir := ""
		if cfg != nil {
			dataDir = cfg.DataDir
		}
		httpAPIServer.SetTokenStore(sm, dataDir)
	}
	// Wire the sensitive-data masker so an activity record the detector flagged
	// never serves the credential it flagged (Spec 026). Same config as the
	// detector the activity service scans with, so the two can never disagree
	// about which categories count.
	//
	// Installed unconditionally, including when detection is currently off:
	// records flagged while it was on are still in the log, and turning the
	// feature off must not start serving their credentials in cleartext.
	if cfg := s.runtime.Config(); cfg != nil {
		httpAPIServer.SetSensitiveMasker(security.NewDetector(cfg.SensitiveDataDetection))
	}
	// Wire feedback submitter (Spec 036)
	if ts := s.runtime.TelemetryService(); ts != nil {
		httpAPIServer.SetFeedbackSubmitter(ts)
		// Spec 042: Tier 2 counter registry — surface and REST endpoint
		// middlewares record into this.
		httpAPIServer.SetTelemetryRegistry(ts.Registry())
	}
	// Spec 042: provide live telemetry service to /api/v1/telemetry/payload
	// handler via a closure so `mcpproxy telemetry show-payload` can render
	// the next heartbeat with runtime stats attached.
	httpAPIServer.SetTelemetryPayloadProvider(func() *telemetry.Service {
		return s.runtime.TelemetryService()
	})
	// Wire client connect service
	if cfg := s.runtime.Config(); cfg != nil {
		connectSvc := connect.NewService(cfg.Listen, cfg.APIKey).
			WithRequireMCPAuth(cfg.RequireMCPAuth).
			// Read listen/api_key/require_mcp_auth LIVE so a runtime toggle (the
			// /mcp middleware already honors require_mcp_auth per-request) is
			// reflected in what connect writes, instead of the startup snapshot
			// that would re-leak the API key after auth is turned off (Spec 078).
			WithConfigProvider(func() (string, string, bool) {
				c := s.runtime.Config()
				if c == nil {
					return "", "", false
				}
				return c.Listen, c.APIKey, c.RequireMCPAuth
			})
		httpAPIServer.SetConnectService(connectSvc)

		// Spec 046: wire the onboarding-funnel provider on the telemetry
		// service so each heartbeat carries connected-client count + IDs
		// (from connect.Service) and wizard engagement + per-step status
		// (from BBolt OnboardingState). nil-safe at every layer.
		if ts := s.runtime.TelemetryService(); ts != nil {
			ts.SetOnboardingProvider(func() *telemetry.OnboardingSnapshot {
				snap := &telemetry.OnboardingSnapshot{
					ConnectedClientCount: connectSvc.GetConnectedCount(),
					ConnectedClientIDs:   connectSvc.GetConnectedIDs(),
				}
				if state, err := s.runtime.GetOnboardingState(); err == nil && state != nil {
					snap.WizardEngaged = state.Engaged
					snap.WizardConnectStep = state.ConnectStepStatus
					snap.WizardServerStep = state.ServerStepStatus
					// Spec 080 (FR-005): shown once the wizard has rendered,
					// independent of engagement.
					snap.WizardShown = state.FirstShownAt != nil
				}
				return snap
			})
		}
	}
	// Wire security scanner service (Spec 039)
	if sm := s.runtime.StorageManager(); sm != nil {
		cfg := s.runtime.Config()
		dataDir := ""
		if cfg != nil {
			dataDir = cfg.DataDir
		}
		secRegistry := scanner.NewRegistry(dataDir, s.logger)
		secDocker := scanner.NewDockerRunner(s.logger)
		secService := scanner.NewService(sm, secRegistry, secDocker, dataDir, s.logger)
		// Spec 077 US3: gate the opt-in deep-scan layer (Docker scanners + source
		// extraction). Off by default — only the deterministic in-process baseline
		// runs. The deprecated top-level scanner_* keys are migrated into
		// security.deep_scan on load, so the effective accessors read the unified
		// surface here (T031). This exact call is re-run on every config.reloaded
		// (reapplyScannerSecurityConfig) so a deep-scan toggle hot-reloads without
		// a restart — startup and reload configure the scanner identically.
		// ApplySecurityConfig is nil-safe (audit FIX 1): a nil Config or a nil
		// Config.Security (DefaultConfig never initializes the security block)
		// still forces the deep-scan layer OFF — no Docker scanners, no
		// published-package-source fetch — so the disabled-path gates apply
		// unconditionally rather than only when a security block exists.
		var secCfg *config.SecurityConfig
		if cfg != nil {
			secCfg = cfg.Security
		}
		secService.ApplySecurityConfig(secCfg)
		// MCP-34.4 / D3 option (b): tell the scanner which isolation mode is
		// active. Under "sandbox"/"none" the host runs no Docker for scanner
		// plugins, so they degrade cleanly (skip + "degraded" scan summary)
		// instead of failing with a misleading "pull the image" message.
		//
		// Two layers: SetIsolationMode is the engine-wide default; the resolver
		// is per-server so a server pinned to isolation.mode:docker still runs
		// Docker scanners under a global sandbox default (and vice versa),
		// matching the per-server isolation resolver and the docs.
		if cfg != nil && cfg.DockerIsolation != nil {
			secService.SetIsolationMode(string(cfg.DockerIsolation.ResolvedMode()))
		}
		secService.SetIsolationModeResolver(func(serverName string) string {
			liveCfg := s.runtime.Config()
			if liveCfg == nil || liveCfg.DockerIsolation == nil {
				return ""
			}
			var sc *config.ServerConfig
			for _, candidate := range liveCfg.Servers {
				if candidate != nil && candidate.Name == serverName {
					sc = candidate
					break
				}
			}
			if sc == nil {
				return "" // unknown server → fall back to the engine-wide default
			}
			im := core.NewIsolationManager(liveCfg.DockerIsolation)
			return string(im.ResolveMode(sc))
		})
		secService.SetEmitter(s.runtime)
		secService.SetServerInfoProvider(&configServerInfoProvider{
			cfg:        cfg,
			liveConfig: s.runtime.Config, // resolve against the live snapshot (MCP-2123)
			server:     s,
		})
		secService.SetServerUnquarantiner(&serverUnquarantinerAdapter{server: s})
		secService.SetSecretStore(&keyringSecretStore{resolver: secret.NewResolver()})
		secService.CleanupStaleJobs()
		httpAPIServer.SetSecurityController(secService)
		// Plumb scan summaries through management.ListServers so the SSE
		// servers.changed embed and REST GET /api/v1/servers share a single
		// enrichment site. Without this, mergeServers on the Web UI strips
		// security_scan from every server on every SSE delivery — same bug
		// class as the pre-existing quarantine-stats staleness PR #463
		// already fixes for Quarantine.
		if mgmtSvc, ok := s.runtime.GetManagementService().(management.Service); ok && mgmtSvc != nil {
			mgmtSvc.SetScanSummaryEnricher(&scanSummaryEnricherAdapter{scanner: secService})
		}
		s.setSecurityScanner(secService)
		// One-shot post-upgrade baseline sweep: scan enabled servers that have
		// never been scanned so their badges stop reading "not scanned" on an
		// install that predates automatic scanning. Backgrounded (never delays
		// startup), serialized through the informational scan path, cancelled
		// with the server context, and gated by a persisted marker so it runs
		// exactly once.
		go s.runBaselineSweep(ctx)
	}
	// Wire server edition multi-user OAuth (no-op in personal edition)
	wireServerEditionOAuth(s, httpAPIServer)

	// Forward REST API, events, health, and (MCP-32) /metrics onto the outer
	// mux. Extracted so the routing is unit-testable (MCP-3135 regression).
	s.registerHTTPHandlers(mux, httpAPIServer)

	// Debug / profiling endpoints (API-key gated). Block & mutex profiles
	// default to off; we enable them when the route is hit so the running
	// daemon stays zero-overhead until someone actively profiles.
	pprofMux := http.NewServeMux()
	pprofMux.HandleFunc("/debug/pprof/", httppprof.Index)
	pprofMux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
	pprofMux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
	pprofMux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
	pprofMux.HandleFunc("/debug/pprof/trace", httppprof.Trace)
	pprofGated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.runtime.Config()
		if cfg == nil || cfg.APIKey == "" {
			http.Error(w, "api key not configured", http.StatusUnauthorized)
			return
		}
		token := r.Header.Get("X-API-Key")
		if token == "" {
			token = r.URL.Query().Get("apikey")
		}
		if token == "" {
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				token = strings.TrimPrefix(h, "Bearer ")
			}
		}
		if token != cfg.APIKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		gruntime.SetBlockProfileRate(100_000_000) // 1 sample / 100 ms blocked
		gruntime.SetMutexProfileFraction(100)     // sample 1% of contention
		pprofMux.ServeHTTP(w, r)
	})
	mux.Handle("/debug/pprof/", pprofGated)
	s.logger.Info("Registered pprof endpoints", zap.String("path", "/debug/pprof/"))

	// Swagger UI (OpenAPI documentation) - mounted directly on main mux for /swagger/* access
	swaggerHandler := httpapi.SetupSwaggerHandler(s.logger.Sugar())
	mux.Handle("/swagger/", swaggerHandler)
	s.logger.Info("Registered Swagger UI endpoint", zap.String("swagger_endpoint", "/swagger/*"))

	// Web UI endpoints (serves embedded Vue.js frontend) with selective API key protection.
	// Spec 080 (FR-006): every serve of the UI entrypoint (index document)
	// increments the persistent web_ui_opened funnel counter — independent of
	// the X-MCPProxy-Client-header surface_requests.webui counting. nil-safe
	// at both layers: no telemetry service or no funnel store → no-op.
	webUIHandler := web.NewHandlerWithIndexCallback(s.logger.Sugar(), func() {
		if ts := s.runtime.TelemetryService(); ts != nil {
			ts.RecordWebUIOpen()
		}
	})
	selectiveProtectedWebUIHandler := s.createSelectiveWebUIProtectedHandler(http.StripPrefix("/ui", webUIHandler))
	mux.Handle("/ui/", selectiveProtectedWebUIHandler)
	// Redirect root to web UI
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusFound)
			return
		}
		// A Web UI deep link that lost its /ui prefix (audit F33): send it to
		// the SPA rather than to Go's plain-text 404, which reads like the
		// server is broken.
		if target, ok := uiRedirectTarget(r); ok {
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	s.logger.Info("Registered Web UI endpoints", zap.Strings("ui_endpoints", []string{"/ui/", "/"}))

	// Determine actual TCP address for logging
	var actualAddr, displayAddr string
	if tcpListener != nil {
		actualAddr = tcpListener.Addr().String()
		displayAddr = resolveDisplayAddress(actualAddr, cfg.Listen)
	}

	// Log active listeners
	activeListeners := []string{}
	if tcpListener != nil {
		activeListeners = append(activeListeners, fmt.Sprintf("TCP: %s", displayAddr))
	}
	if trayListener != nil {
		activeListeners = append(activeListeners, fmt.Sprintf("Tray: %s", trayListener.Address))
	}

	s.logger.Info("Active listeners created",
		zap.Strings("listeners", activeListeners),
		zap.Int("count", len(activeListeners)))

	// Create multiplexing listener that combines TCP and tray listeners
	muxListener := &multiplexListener{
		listeners: []*Listener{},
		logger:    s.logger,
	}
	if tcpListener != nil {
		muxListener.listeners = append(muxListener.listeners, tcpListener)
	}
	if trayListener != nil {
		muxListener.listeners = append(muxListener.listeners, trayListener)
	}

	// GH #965: the request deadlines are configurable. The write deadline keeps
	// its 120s default for ordinary endpoints; the streaming routes (MCP,
	// /events) clear it per-request via streamingNoDeadline so slow tool calls
	// and SSE streams survive without weakening the rest of the surface.
	readTimeout, writeTimeout, idleTimeout := httpServerTimeouts(cfg)

	s.mu.Lock()
	s.httpServer = &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 60 * time.Second, // Slowloris guard — deliberately not configurable
		ReadTimeout:       readTimeout,      // Full request read timeout (http_read_timeout)
		WriteTimeout:      writeTimeout,     // Response write timeout (http_write_timeout; 0 = none; streaming routes opt out)
		IdleTimeout:       idleTimeout,      // Keep-alive timeout for persistent connections (http_idle_timeout)
		MaxHeaderBytes:    1 << 20,          // 1MB max header size
		// Enable connection state tracking for better debugging
		ConnState: s.logConnectionState,
		// Tag connections with their source (TCP vs Tray)
		ConnContext: taggedConnContext,
	}
	s.running = true
	s.runtime.SetRunning(true)
	s.listenAddr = displayAddr
	s.mu.Unlock()

	// Broadcast running status with resolved listen address so readiness checks succeed immediately.
	s.updateStatus(runtime.PhaseRunning, fmt.Sprintf("Server is running on %s", displayAddr))

	// Spec 024: Emit system_start activity event
	startupDurationMs := time.Since(s.startTime).Milliseconds()
	configPath := ""
	if s.runtime != nil {
		configPath = s.runtime.ConfigPath()
	}
	s.runtime.EmitActivitySystemStart(
		httpapi.GetBuildVersion(),
		displayAddr,
		startupDurationMs,
		configPath,
	)

	// List all registered endpoints for visibility
	allEndpoints := []string{
		"/mcp", "/mcp/", // MCP protocol endpoints (default routing mode)
		"/mcp/all", "/mcp/code", "/mcp/call", // Routing mode endpoints (Spec 031)
		"/v1/tool_code", "/v1/tool-code", // Legacy MCP endpoints
		"/api/v1/*", "/events", // REST API and SSE endpoints
		"/ui/", "/", // Web UI endpoints
		"/healthz", "/readyz", "/livez", "/ready", "/health", // Health endpoints (at root level)
	}

	// Determine protocol for logging
	protocol := "HTTP"
	if cfg.TLS != nil && cfg.TLS.Enabled {
		protocol = "HTTPS"
	}

	s.logger.Info(fmt.Sprintf("Starting MCP %s server with enhanced client stability", protocol),
		zap.String("protocol", protocol),
		zap.String("address", actualAddr),
		zap.String("requested_address", cfg.Listen),
		zap.Strings("endpoints", allEndpoints),
		zap.Duration("read_timeout", readTimeout),
		zap.Duration("write_timeout", writeTimeout),
		zap.Duration("idle_timeout", idleTimeout),
		zap.String("features", "connection_tracking,graceful_shutdown,enhanced_logging,dual_listener"),
	)

	// Setup error channel for server communication
	serverErrCh := make(chan error, 1)

	// Apply TLS configuration if enabled
	if cfg.TLS != nil && cfg.TLS.Enabled {
		// Setup TLS configuration
		certsDir := cfg.TLS.CertsDir
		if certsDir == "" {
			certsDir = filepath.Join(cfg.DataDir, "certs")
		}

		tlsCfg, err := tlslocal.EnsureServerTLSConfig(tlslocal.Options{
			Dir:               certsDir,
			RequireClientCert: cfg.TLS.RequireClientCert,
		})
		if err != nil {
			return fmt.Errorf("TLS initialization failed: %w", err)
		}

		// Apply HSTS middleware if enabled
		handler := s.httpServer.Handler
		if cfg.TLS.HSTS {
			handler = withHSTS(handler)
			s.httpServer.Handler = handler
		}

		s.logger.Info("Starting HTTPS server with TLS configuration",
			zap.String("certs_dir", certsDir),
			zap.Bool("require_client_cert", cfg.TLS.RequireClientCert),
			zap.Bool("hsts", cfg.TLS.HSTS),
		)

		// Run the HTTPS server in a goroutine to enable graceful shutdown
		go func() {
			if err := tlslocal.ServeWithTLS(s.httpServer, muxListener, tlsCfg); err != nil && err != http.ErrServerClosed {
				s.logger.Error("HTTPS server error", zap.Error(err))
				s.mu.Lock()
				s.running = false
				s.listenAddr = ""
				s.mu.Unlock()
				s.runtime.SetRunning(false)
				s.updateStatus(runtime.PhaseError, fmt.Sprintf("HTTPS server failed: %v", err))
				serverErrCh <- err
			} else {
				s.logger.Info("HTTPS server stopped gracefully")
				s.mu.Lock()
				s.listenAddr = ""
				s.mu.Unlock()
				serverErrCh <- nil
			}
		}()
	} else {
		s.logger.Info("Starting HTTP server (TLS disabled)")

		// Run the HTTP server in a goroutine to enable graceful shutdown
		go func() {
			if err := s.httpServer.Serve(muxListener); err != nil && err != http.ErrServerClosed {
				s.logger.Error("HTTP server error", zap.Error(err))
				s.mu.Lock()
				s.running = false
				s.listenAddr = ""
				s.mu.Unlock()
				s.runtime.SetRunning(false)
				s.updateStatus(runtime.PhaseError, fmt.Sprintf("HTTP server failed: %v", err))
				serverErrCh <- err
			} else {
				s.logger.Info("HTTP server stopped gracefully")
				s.mu.Lock()
				s.listenAddr = ""
				s.mu.Unlock()
				serverErrCh <- nil
			}
		}()
	}

	// Wait for either context cancellation or server error
	select {
	case <-ctx.Done():
		s.logger.Info("Server context cancelled, shutdown will be handled by StopServer")
		// HTTP server shutdown is now handled synchronously in StopServer()
		// to avoid race conditions during graceful shutdown
		return ctx.Err()
	case err := <-serverErrCh:
		return err
	}
}

// responseWriter wraps http.ResponseWriter to capture the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode    int
	headerWritten bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.headerWritten {
		rw.statusCode = code
		rw.headerWritten = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

// Flush delegates to the underlying ResponseWriter so that SSE keeps working
// through this wrapper.
//
// Without it, wrapping the writer silently strips http.Flusher, and the MCP
// transport's CanStream() check fails: GET /mcp — the listening stream the
// protocol uses for every SERVER-TO-CLIENT message — is rejected with
// "405 Streaming unsupported". That kills roots, sampling and elicitation over
// HTTP, since the server has no channel on which to ask the client anything.
//
// Found while implementing Spec 082: the roots request went out and timed out
// every time, because the client was never able to receive it.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logConnectionState logs HTTP connection state changes for debugging client issues
func (s *Server) logConnectionState(conn net.Conn, state http.ConnState) {
	// Handle cases where conn or RemoteAddr might be nil
	remoteAddr := "unknown"
	if conn != nil {
		if addr := conn.RemoteAddr(); addr != nil {
			remoteAddr = addr.String()
		}
	}

	switch state {
	case http.StateNew:
		s.logger.Debug("New client connection established",
			zap.String("remote_addr", remoteAddr),
			zap.String("state", "new"))
	// StateActive and StateIdle removed - too noisy with keep-alive connections and SSE streams
	// case http.StateActive:
	// 	s.logger.Debug("Client connection active",
	// 		zap.String("remote_addr", conn.RemoteAddr().String()),
	// 		zap.String("state", "active"))
	// case http.StateIdle:
	// 	s.logger.Debug("Client connection idle",
	// 		zap.String("remote_addr", conn.RemoteAddr().String()),
	// 		zap.String("state", "idle"))
	case http.StateHijacked:
		s.logger.Debug("Client connection hijacked (likely for upgrade)",
			zap.String("remote_addr", remoteAddr),
			zap.String("state", "hijacked"))
	case http.StateClosed:
		s.logger.Debug("Client connection closed",
			zap.String("remote_addr", remoteAddr),
			zap.String("state", "closed"))
	}
}

// SaveConfiguration saves the current configuration to the persistent config file
func (s *Server) SaveConfiguration() error {
	return s.runtime.SaveConfiguration()
}

// ReloadConfiguration reloads the configuration from disk
func (s *Server) ReloadConfiguration() error {
	return s.runtime.ReloadConfiguration()
}

// OnUpstreamServerChange should be called when upstream servers are modified
func (s *Server) OnUpstreamServerChange() {
	s.runtime.HandleUpstreamServerChange(s.serverCtx)
}

// GetConfigPath returns the path to the configuration file for file watching
func (s *Server) GetConfigPath() string {
	if path := s.runtime.ConfigPath(); path != "" {
		return path
	}
	if cfg := s.runtime.Config(); cfg != nil {
		return config.GetConfigPath(cfg.DataDir)
	}
	return ""
}

// GetLogDir returns the log directory path for tray UI
func (s *Server) GetLogDir() string {
	if cfg := s.runtime.Config(); cfg != nil {
		if cfg.Logging != nil && cfg.Logging.LogDir != "" {
			return cfg.Logging.LogDir
		}
		// Return OS-specific default log directory if not configured
		if defaultLogDir, err := logs.GetLogDir(); err == nil {
			return defaultLogDir
		}
		return cfg.DataDir
	}
	if defaultLogDir, err := logs.GetLogDir(); err == nil {
		return defaultLogDir
	}
	return ""
}

// Configuration management methods

// GetConfig returns the current configuration
func (s *Server) GetConfig() (*config.Config, error) {
	return s.runtime.GetConfig()
}

// GetDesiredConfig returns the configuration as persisted on disk — what the
// next start will use. Read-modify-write callers must merge onto this one; see
// Runtime.GetDesiredConfig.
func (s *Server) GetDesiredConfig() (*config.Config, error) {
	return s.runtime.GetDesiredConfig()
}

// ServedRoutingMode returns the routing mode /mcp actually bound at startup.
func (s *Server) ServedRoutingMode() string {
	return s.runtime.ServedRoutingMode()
}

// DefaultInstructions returns the built-in default MCP instructions text,
// independent of any user-configured custom value. It backs the
// /api/v1/status `default_instructions` field so the Web UI can render the
// built-in default as the instructions placeholder without hardcoding the
// text (MCP-2176). This mirrors resolveInstructions("").
func (s *Server) DefaultInstructions() string {
	return defaultInstructions
}

// ValidateConfig validates a configuration
func (s *Server) ValidateConfig(cfg *config.Config) ([]config.ValidationError, error) {
	return s.runtime.ValidateConfig(cfg)
}

// ApplyConfig applies a new configuration
func (s *Server) ApplyConfig(cfg *config.Config, cfgPath string) (*runtime.ConfigApplyResult, error) {
	return s.runtime.ApplyConfig(cfg, cfgPath)
}

// GetTokenSavings calculates and returns token savings statistics
func (s *Server) GetTokenSavings() (*contracts.ServerTokenMetrics, error) {
	return s.runtime.CalculateTokenSavings()
}

// GetServerTools returns tools for a specific server
func (s *Server) GetServerTools(serverName string) ([]map[string]interface{}, error) {
	s.logger.Debug("GetServerTools called (Phase 7.1: using StateView)", zap.String("server", serverName))

	// Phase 7.1: Use StateView for lock-free cached tool reads
	supervisor := s.runtime.Supervisor()
	if supervisor == nil {
		return nil, fmt.Errorf("supervisor not available")
	}

	snapshot := supervisor.StateView().Snapshot()
	serverStatus, exists := snapshot.Servers[serverName]
	if !exists {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	if !serverStatus.Connected {
		return nil, fmt.Errorf("server %s is not connected", serverName)
	}

	// Convert cached tools to API response format
	result := make([]map[string]interface{}, len(serverStatus.Tools))
	for i, tool := range serverStatus.Tools {
		toolMap := map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
			"server_name": serverName,
		}

		// Include annotations if present
		if tool.Annotations != nil {
			annotations := map[string]interface{}{}
			if tool.Annotations.Title != "" {
				annotations["title"] = tool.Annotations.Title
			}
			if tool.Annotations.ReadOnlyHint != nil {
				annotations["readOnlyHint"] = *tool.Annotations.ReadOnlyHint
			}
			if tool.Annotations.DestructiveHint != nil {
				annotations["destructiveHint"] = *tool.Annotations.DestructiveHint
			}
			if tool.Annotations.IdempotentHint != nil {
				annotations["idempotentHint"] = *tool.Annotations.IdempotentHint
			}
			if tool.Annotations.OpenWorldHint != nil {
				annotations["openWorldHint"] = *tool.Annotations.OpenWorldHint
			}
			if len(annotations) > 0 {
				toolMap["annotations"] = annotations
			}
		}

		result[i] = toolMap
	}

	s.logger.Debug("Retrieved server tools from cache",
		zap.String("server", serverName),
		zap.Int("count", len(result)))
	return result, nil
}

// SearchTools searches for tools using the index
func (s *Server) SearchTools(query string, limit int) ([]map[string]interface{}, error) {
	s.logger.Debug("SearchTools called", zap.String("query", query), zap.Int("limit", limit))

	if s.runtime.IndexManager() == nil {
		return nil, fmt.Errorf("index manager not initialized")
	}

	// Search tools in the index
	results, err := s.runtime.IndexManager().SearchTools(query, limit)
	if err != nil {
		s.logger.Error("Failed to search tools", zap.String("query", query), zap.Error(err))
		return nil, err
	}

	// SECURITY (issue #877): the MCP retrieve_tools path never surfaces a
	// quarantined server's tools — their descriptions/schemas are withheld
	// because they are the Tool Poisoning Attack vector quarantine exists to
	// contain. The Bleve index can still hold entries for a server quarantined
	// after it was indexed, so this REST read path must apply the same
	// server-level visibility gate rather than returning raw index hits. Names
	// stay bare (server_name is a separate field) — only which tools appear is
	// filtered, so the /api/v1/index/search response shape is unchanged.
	withheld := s.quarantinedServerFilter()

	// Convert to map format for API
	var resultMaps []map[string]interface{}
	for _, result := range results {
		if result.Tool != nil {
			serverName := result.Tool.ServerName
			if serverName == "" {
				// Fallback: recover the server from a "server:tool" index name so
				// the quarantine gate still applies when ServerName is unset.
				if parts := strings.SplitN(result.Tool.Name, ":", 2); len(parts) == 2 {
					serverName = parts[0]
				}
			}
			if withheld(serverName) {
				continue
			}
			toolData := map[string]interface{}{
				"name":        result.Tool.Name,
				"description": result.Tool.Description,
				"server_name": result.Tool.ServerName,
			}
			// Parse params JSON as input schema if available
			if result.Tool.ParamsJSON != "" {
				var inputSchema map[string]interface{}
				if err := json.Unmarshal([]byte(result.Tool.ParamsJSON), &inputSchema); err == nil {
					toolData["input_schema"] = inputSchema
				}
			}

			// Wrap in search result format with nested tool
			resultMap := map[string]interface{}{
				"tool":  toolData,
				"score": result.Score,
			}
			resultMaps = append(resultMaps, resultMap)
		}
	}

	s.logger.Debug("Search completed", zap.String("query", query), zap.Int("results", len(resultMaps)))
	return resultMaps, nil
}

// quarantinedServerFilter returns a predicate reporting whether a search hit
// must be WITHHELD, memoizing storage lookups for the duration of a single
// search so a result set spanning N servers costs at most N storage reads. It
// reads authoritative server-level quarantine state from storage — the same
// source describeGateReason (the MCP visibility gate) consults — so the REST
// index-search surface and retrieve_tools agree on which servers are withheld
// (issue #877).
//
// It FAILS CLOSED: a hit is withheld unless it can be positively attributed to
// a resolvable, non-quarantined server. An empty server name (a stale/legacy
// index entry that cannot be attributed), a nil storage manager, a storage
// error, or a missing record all withhold the hit rather than risk exposing a
// description the quarantine boundary is meant to hide.
func (s *Server) quarantinedServerFilter() func(serverName string) bool {
	cache := make(map[string]bool)
	storageMgr := s.runtime.StorageManager()
	return func(serverName string) bool {
		if serverName == "" || storageMgr == nil {
			return true
		}
		if withheld, ok := cache[serverName]; ok {
			return withheld
		}
		withheld := true
		if cfg, err := storageMgr.GetUpstreamServer(serverName); err == nil && cfg != nil {
			withheld = cfg.Quarantined
		}
		cache[serverName] = withheld
		return withheld
	}
}

// GetServerLogs returns recent log lines for a specific server
func (s *Server) GetServerLogs(serverName string, tail int) ([]contracts.LogEntry, error) {
	s.logger.Debug("GetServerLogs called", zap.String("server", serverName), zap.Int("tail", tail))

	if s.runtime.UpstreamManager() == nil {
		return nil, fmt.Errorf("upstream manager not initialized")
	}

	// Check if server exists
	_, exists := s.runtime.UpstreamManager().GetClient(serverName)
	if !exists {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	// Read from server-specific log file
	cfg, err := s.runtime.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	logDir := cfg.Logging.LogDir
	if logDir == "" {
		logDir, err = logs.GetLogDir()
		if err != nil {
			return nil, fmt.Errorf("failed to determine log directory: %w", err)
		}
	}

	logFile := filepath.Join(logDir, logs.ServerLogFilename(serverName))

	// Defense-in-depth: logs.ServerLogFilename already sanitizes the (user-controlled)
	// server name to a single path element, but verify the resolved path stays inside
	// logDir so a crafted name can never escape the log directory (path-injection barrier).
	if !strings.HasPrefix(filepath.Clean(logFile), filepath.Clean(logDir)+string(os.PathSeparator)) {
		return nil, fmt.Errorf("invalid server name: %s", serverName)
	}

	// Check if file exists
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("log file not found: %s (server may not have run yet)", logFile)
	}

	// Read last N lines from file
	file, err := os.Open(logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}
	defer file.Close()

	// Use a simple tail implementation - read lines into buffer
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read log file: %w", err)
	}

	// Get last N lines
	start := 0
	if len(lines) > tail {
		start = len(lines) - tail
	}
	lines = lines[start:]

	// Parse lines into LogEntry structs
	var logEntries []contracts.LogEntry
	for _, line := range lines {
		// Parse structured log format: timestamp [level] message
		// Example: "2025-01-20 15:04:05 [INFO] Server started"
		entry := parseLogLine(line, serverName)
		logEntries = append(logEntries, entry)
	}

	s.logger.Debug("Retrieved server logs", zap.String("server", serverName), zap.Int("lines", len(logEntries)))
	return logEntries, nil
}

// parseLogLine parses a log line into a LogEntry.
//
// Issue #1148, round 4 finding 4: this is the REST twin of `tail_log`. The
// per-server log file is written by mcpproxy AND by the child process, and both
// put credentials in it — the connection logger records the upstream URL with
// its `?token=…`, and an MCP server is free to print its own API key.
// `GET /api/v1/servers/{id}/logs` served those lines verbatim while the MCP
// door scrubbed them. Scrubbing HERE, where a raw line becomes a LogEntry,
// covers every reader of GetServerLogs rather than one handler.
func parseLogLine(line string, serverName string) contracts.LogEntry {
	// Try to parse structured format: "2025-01-20 15:04:05 [LEVEL] message"
	parts := strings.SplitN(line, " ", 3)

	entry := contracts.LogEntry{
		Timestamp: time.Now(), // Default to now
		Level:     "INFO",
		Message:   line, // Full line as fallback
		Server:    serverName,
	}

	// Try to parse timestamp and level
	if len(parts) >= 3 {
		// Try to parse timestamp (first two parts)
		timestampStr := parts[0] + " " + parts[1]
		if ts, err := time.Parse("2006-01-02 15:04:05", timestampStr); err == nil {
			entry.Timestamp = ts

			// Parse level and message
			rest := parts[2]
			if strings.HasPrefix(rest, "[") {
				endIdx := strings.Index(rest, "]")
				if endIdx > 0 {
					entry.Level = rest[1:endIdx]
					if endIdx+2 < len(rest) {
						entry.Message = rest[endIdx+2:]
					}
				}
			}
		}
	}

	entry.Message = scrubUpstreamText(entry.Message)
	return entry
}

// GetSecretResolver returns the secret resolver instance
func (s *Server) GetSecretResolver() *secret.Resolver {
	return s.runtime.GetSecretResolver()
}

// NotifySecretsChanged notifies the runtime that secrets have changed
func (s *Server) NotifySecretsChanged(ctx context.Context, operation, secretName string) error {
	return s.runtime.NotifySecretsChanged(ctx, operation, secretName)
}

// EmitActiveProfileChanged broadcasts an active_profile.changed event so UI
// surfaces (Web UI, tray) reflect a default-profile switch made by another
// client (Profiles v2 T2/T5). The REST handler invokes this via an optional
// capability assertion, so adding it here does not widen the httpapi
// ServerController interface.
func (s *Server) EmitActiveProfileChanged(profile string) {
	s.runtime.EmitActiveProfileChanged(profile)
}

// GetCurrentConfig returns the current configuration
func (s *Server) GetCurrentConfig() interface{} {
	return s.runtime.GetCurrentConfig()
}

// GetToolCalls retrieves tool call history with pagination
func (s *Server) GetToolCalls(limit, offset int) ([]*contracts.ToolCallRecord, int, error) {
	return s.runtime.GetToolCalls(limit, offset)
}

// GetToolCallByID retrieves a single tool call by ID
func (s *Server) GetToolCallByID(id string) (*contracts.ToolCallRecord, error) {
	return s.runtime.GetToolCallByID(id)
}

// GetServerToolCalls retrieves tool call history for a specific server
func (s *Server) GetServerToolCalls(serverName string, limit int) ([]*contracts.ToolCallRecord, error) {
	return s.runtime.GetServerToolCalls(serverName, limit)
}

// ReplayToolCall replays a tool call with modified arguments. ctx is the
// caller's request context: it governs the concurrency-limiter queue wait as
// well as the upstream call (spec 093 FR-005).
func (s *Server) ReplayToolCall(ctx context.Context, id string, arguments map[string]interface{}) (*contracts.ToolCallRecord, error) {
	return s.runtime.ReplayToolCall(ctx, id, arguments)
}

// GetToolCallsBySession retrieves tool calls filtered by session ID
func (s *Server) GetToolCallsBySession(sessionID string, limit, offset int) ([]*contracts.ToolCallRecord, int, error) {
	return s.runtime.GetToolCallsBySession(sessionID, limit, offset)
}

// GetRecentSessions retrieves recent MCP sessions, optionally filtered by
// status ("active" / "closed"; empty means no filter).
func (s *Server) GetRecentSessions(limit int, status string) ([]*contracts.MCPSession, int, error) {
	return s.runtime.GetRecentSessions(limit, status)
}

// GetSessionByID retrieves a session by its ID
func (s *Server) GetSessionByID(sessionID string) (*contracts.MCPSession, error) {
	return s.runtime.GetSessionByID(sessionID)
}

// CallTool calls an MCP tool and returns the result
func (s *Server) CallTool(ctx context.Context, toolName string, arguments map[string]interface{}) (interface{}, error) {
	if s.mcpProxy == nil {
		return nil, fmt.Errorf("MCP proxy not initialized")
	}

	// Create MCP call tool request
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: arguments,
		},
	}

	// Call the tool via MCP proxy
	result, err := s.mcpProxy.CallToolDirect(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("tool call failed: %w", err)
	}

	return result, nil
}

// ListRegistries returns the list of available MCP server registries (Phase 7)
func (s *Server) ListRegistries() ([]interface{}, error) {
	return s.runtime.ListRegistries()
}

// SearchRegistryServers searches for servers in a specific registry (Phase 7)
func (s *Server) SearchRegistryServers(registryID, tag, query string, limit int) ([]interface{}, *contracts.RegistryCacheInfo, error) {
	return s.runtime.SearchRegistryServers(registryID, tag, query, limit)
}

// RefreshRegistryCache invalidates a registry's cached server lists (spec 070 FR-007).
func (s *Server) RefreshRegistryCache(registryID string) (int, error) {
	return s.runtime.RefreshRegistryCache(registryID)
}

// GetVersionInfo returns the current version information from the update checker.
func (s *Server) GetVersionInfo() *updatecheck.VersionInfo {
	return s.runtime.GetVersionInfo()
}

// RefreshVersionInfo performs an immediate update check and returns the result.
func (s *Server) RefreshVersionInfo() *updatecheck.VersionInfo {
	return s.runtime.RefreshVersionInfo()
}

// UpdatePolicy returns the effective update policy (Spec 092 FR-015).
func (s *Server) UpdatePolicy() updatecheck.Policy {
	return s.runtime.UpdatePolicy()
}

// Activity logging methods (RFC-003)

// ListActivities returns activity records matching the filter.
func (s *Server) ListActivities(filter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	return s.runtime.ListActivities(filter)
}

// GetActivity returns a single activity record by ID.
func (s *Server) GetActivity(id string) (*storage.ActivityRecord, error) {
	return s.runtime.GetActivity(id)
}

// StreamActivities returns a channel that yields activity records matching the filter.
func (s *Server) StreamActivities(filter storage.ActivityFilter) <-chan *storage.ActivityRecord {
	return s.runtime.StreamActivities(filter)
}

// AggregateToolUsage rolls up tool_call activity per (server,tool) since the
// given time (spec 050).
func (s *Server) AggregateToolUsage(since time.Time) (map[string]storage.ToolUsageStat, error) {
	return s.runtime.AggregateToolUsage(since)
}

// UsageSnapshot returns the actor-owned usage aggregate snapshot (spec 069 A3).
func (s *Server) UsageSnapshot() *runtime.UsageAggregate {
	return s.runtime.UsageSnapshot()
}

// ListToolApprovals returns tool approval records for a server (Spec 032).
func (s *Server) ListToolApprovals(serverName string) ([]*storage.ToolApprovalRecord, error) {
	return s.runtime.ListToolApprovals(serverName)
}

// ApproveTools approves specific tools for a server (Spec 032).
func (s *Server) ApproveTools(serverName string, toolNames []string, approvedBy string) error {
	return s.runtime.ApproveTools(serverName, toolNames, approvedBy)
}

// ApproveAllTools approves all pending/changed tools for a server (Spec 032).
func (s *Server) ApproveAllTools(serverName string, approvedBy string) (int, error) {
	return s.runtime.ApproveAllTools(serverName, approvedBy)
}

// BlockTools atomically blocks (approve+disable) specific tools (MCP-2198).
func (s *Server) BlockTools(serverName string, toolNames []string, blockedBy string) (int, error) {
	return s.runtime.BlockTools(serverName, toolNames, blockedBy)
}

// BlockAllTools atomically blocks (approve+disable) all pending/changed tools
// for a server (MCP-2198).
func (s *Server) BlockAllTools(serverName string, blockedBy string) (int, error) {
	return s.runtime.BlockAllTools(serverName, blockedBy)
}

// SetToolEnabled sets whether a tool is enabled for MCP exposure (Spec 032).
func (s *Server) SetToolEnabled(serverName, toolName string, enabled bool, updatedBy string) error {
	return s.runtime.SetToolEnabled(serverName, toolName, enabled, updatedBy)
}

// SetAllToolsEnabled bulk-toggles every known tool for a server to the given
// state. Returns the count of tools whose state actually changed.
func (s *Server) SetAllToolsEnabled(serverName string, enabled bool, updatedBy string) (int, error) {
	return s.runtime.SetAllToolsEnabled(serverName, enabled, updatedBy)
}

// IsToolConfigDenied reports whether toolName is denied by the server's static
// enabled_tools / disabled_tools config.
func (s *Server) IsToolConfigDenied(serverName, toolName string) bool {
	return s.runtime.IsToolConfigDenied(serverName, toolName)
}

// GetToolApproval returns the approval record for a specific tool (Spec 032).
func (s *Server) GetToolApproval(serverName, toolName string) (*storage.ToolApprovalRecord, error) {
	return s.runtime.GetToolApproval(serverName, toolName)
}

// GetToolApprovalStatus returns the approval status string for a specific tool.
func (s *Server) GetToolApprovalStatus(serverName, toolName string) (string, error) {
	record, err := s.runtime.GetToolApproval(serverName, toolName)
	if err != nil {
		return "", err
	}
	return string(record.Status), nil
}

// GetOnboardingState returns the wizard engagement state (Spec 046).
func (s *Server) GetOnboardingState() (*storage.OnboardingState, error) {
	return s.runtime.GetOnboardingState()
}

// SaveOnboardingState persists the wizard engagement state (Spec 046).
func (s *Server) SaveOnboardingState(state *storage.OnboardingState) error {
	return s.runtime.SaveOnboardingState(state)
}

// GetActivationFirstMCPClient returns Spec 044's FirstMCPClientEver flag and
// the capped list of recognized client names. Used by the v2 onboarding wizard
// (Spec 046 v2) to drive the Verify tab. Nil-safe: when telemetry isn't wired
// (e.g. CI/test) returns (false, nil).
func (s *Server) GetActivationFirstMCPClient() (bool, []string) {
	if s.runtime == nil {
		return false, nil
	}
	return s.runtime.GetActivationFirstMCPClient()
}

// serverUnquarantinerAdapter adapts *Server to scanner.ServerUnquarantiner so
// the security scanner service can unquarantine a server after ApproveServer
// succeeds. Reuses the existing Server.UnquarantineServer path so the behavior
// stays identical to the REST API and tray code.
type serverUnquarantinerAdapter struct {
	server *Server
}

func (a *serverUnquarantinerAdapter) UnquarantineServer(serverName string) error {
	if a.server == nil {
		return fmt.Errorf("server unavailable")
	}
	return a.server.UnquarantineServer(serverName)
}

// scanSummaryEnricherAdapter bridges scanner.Service.GetScanSummary (which
// returns the scanner-internal *scanner.ScanSummary type) to
// management.SecurityScanEnricher (which returns the wire-shape
// *contracts.SecurityScanSummary used by REST and SSE consumers). Plain
// field copy — the two structs are isomorphic by design.
type scanSummaryEnricherAdapter struct {
	scanner *scanner.Service
}

func (a *scanSummaryEnricherAdapter) GetSecurityScanSummary(ctx context.Context, serverName string) *contracts.SecurityScanSummary {
	if a == nil || a.scanner == nil {
		return nil
	}
	summary := a.scanner.GetScanSummary(ctx, serverName)
	if summary == nil {
		return nil
	}
	out := &contracts.SecurityScanSummary{
		LastScanAt:     summary.LastScanAt,
		RiskScore:      summary.RiskScore,
		Status:         summary.Status,
		ScannersRun:    summary.ScannersRun,
		ScannersFailed: summary.ScannersFailed,
		ScannersTotal:  summary.ScannersTotal,
	}
	if summary.FindingCounts != nil {
		out.FindingCounts = &contracts.FindingCounts{
			Dangerous: summary.FindingCounts.Dangerous,
			Warning:   summary.FindingCounts.Warning,
			Info:      summary.FindingCounts.Info,
			Total:     summary.FindingCounts.Total,
		}
	}
	// Surface the opt-in deep-scan layer status (Spec 077 US3). Always emitted
	// on a computed summary; when the layer is off it reports enabled=false
	// plus any enabled-but-skipped Docker scanners (audit FIX 3a).
	if summary.DeepScan != nil {
		ds := &contracts.DeepScanDescriptor{
			Enabled:   summary.DeepScan.Enabled,
			Ran:       summary.DeepScan.Ran,
			Available: summary.DeepScan.Available,
		}
		for _, f := range summary.DeepScan.ScannersFailed {
			ds.ScannersFailed = append(ds.ScannersFailed, contracts.DeepScanScannerFailure{
				ID:     f.ID,
				Reason: f.Reason,
			})
		}
		ds.SkippedScanners = append(ds.SkippedScanners, summary.DeepScan.SkippedScanners...)
		out.DeepScan = ds
	}
	return out
}

// keyringSecretStore adapts secret.Resolver to scanner.SecretStore for API key management.
type keyringSecretStore struct {
	resolver *secret.Resolver
}

func (k *keyringSecretStore) StoreSecret(ctx context.Context, name, value string) error {
	ref := secret.Ref{Type: "keyring", Name: name}
	return k.resolver.Store(ctx, ref, value)
}

func (k *keyringSecretStore) ResolveSecret(ctx context.Context, refStr string) (string, error) {
	ref, err := secret.ParseSecretRef(refStr)
	if err != nil {
		return "", err
	}
	return k.resolver.Resolve(ctx, *ref)
}

// configServerInfoProvider implements scanner.ServerInfoProvider using the config and server.
//
// liveConfig returns the CURRENT config snapshot. It must be preferred over the
// boot-time cfg field: the runtime swaps in a fresh immutable snapshot on every
// reload, so a server added at runtime (e.g. from the registry) only appears in
// the live snapshot. Resolving against the stale boot cfg made such servers
// invisible to the scanner — the root cause of MCP-2123 ("No Source Available"
// for a freshly-added Docker-isolated stdio server). cfg is kept as a fallback
// for callers/tests that don't wire a live accessor.
type configServerInfoProvider struct {
	cfg        *config.Config
	liveConfig func() *config.Config
	server     *Server
	// liveTools resolves the live upstream client used as the tool-export
	// fallback for quarantined servers (see GetServerTools). nil in production —
	// it defaults to the upstream manager's managed client; tests inject a stub.
	liveTools func(serverName string) (scanToolLister, bool)
}

// scanToolLister is the minimal live-client capability the security-scan tool
// export needs: ask a CONNECTED upstream server for its current tool
// definitions. *managed.Client satisfies it. Kept as a narrow interface so the
// export path is unit-testable without a real upstream process.
type scanToolLister interface {
	IsConnected() bool
	ListTools(ctx context.Context) ([]*config.ToolMetadata, error)
}

// liveToolExportTimeout bounds the inspection-only tools/list call made when a
// quarantined server's tool definitions are exported for a security scan. Same
// order of magnitude as the quarantine-inspection surface's own 10s budget,
// with headroom for slow stdio servers.
const liveToolExportTimeout = 15 * time.Second

// currentConfig returns the live config when an accessor is wired, otherwise the
// boot-time snapshot.
func (p *configServerInfoProvider) currentConfig() *config.Config {
	if p.liveConfig != nil {
		if cfg := p.liveConfig(); cfg != nil {
			return cfg
		}
	}
	return p.cfg
}

func (p *configServerInfoProvider) GetServerInfo(serverName string) (*scanner.ServerInfo, error) {
	cfg := p.currentConfig()
	if cfg == nil {
		return nil, fmt.Errorf("no config available")
	}
	for _, sc := range cfg.Servers {
		if sc.Name == serverName {
			return &scanner.ServerInfo{
				Name:       sc.Name,
				Protocol:   sc.Protocol,
				Command:    sc.Command,
				Args:       sc.Args,
				WorkingDir: sc.WorkingDir,
				URL:        sc.URL,
				Env:        sc.Env,
			}, nil
		}
	}
	return nil, fmt.Errorf("server %q not found in config", serverName)
}

// GetServerTools returns a server's tool definitions for security scanning.
//
// Fast path: the StateView tool cache (Server.GetServerTools).
//
// Fallback (spec 086 FR-011): a LIVE tools/list against the managed upstream
// client. This is required, not an optimization — quarantined servers are
// deliberately excluded from tool discovery and indexing (issue #873:
// "Skipping quarantined client for tool discovery"), so their StateView tool
// cache is empty BY CONSTRUCTION. A scan-mode server is quarantined on
// admission, which is precisely when the admission baseline scan runs, so the
// export always returned 0 tools and StartScan aborted with "tool export failed
// (server is connected but returned 0 tools)": the server could never be
// scanned, never auto-admitted on a green verdict, and never surfaced a TPA
// finding on a red one.
//
// The fallback uses the same direct-client seam the quarantine inspection
// surface already uses (quarantine_security/inspect_quarantined →
// managed.Client.ListTools) and is scoped to scanning/inspection: the retrieved
// definitions are written only into the scan's tools.json, never into StateView
// or the Bleve index, so quarantined tools stay un-indexed and un-callable.
// Only this scanner-facing provider has the fallback; general tool discovery is
// untouched.
func (p *configServerInfoProvider) GetServerTools(serverName string) ([]map[string]interface{}, error) {
	var (
		cached    []map[string]interface{}
		cachedErr error
	)
	if p.server != nil {
		cached, cachedErr = p.server.GetServerTools(serverName)
		if cachedErr == nil && len(cached) > 0 {
			return cached, nil
		}
	} else {
		cachedErr = fmt.Errorf("server tools not available")
	}

	live, liveErr := p.liveServerTools(serverName)
	if liveErr == nil && len(live) > 0 {
		return live, nil
	}
	if liveErr != nil && p.server != nil {
		// Diagnosability: without this the only trace of a failed admission scan
		// is the scanner's generic "returned 0 tools" warning.
		p.server.logger.Debug("Live tool export fallback unavailable for scan",
			zap.String("server", serverName), zap.Error(liveErr))
	}

	if cachedErr != nil {
		return nil, cachedErr
	}
	return cached, nil
}

// liveServerTools performs the inspection-only tools/list against the live
// upstream client and maps the result into the same wire shape the cached path
// returns (what exportToolDefinitions writes to tools.json).
func (p *configServerInfoProvider) liveServerTools(serverName string) ([]map[string]interface{}, error) {
	lister, ok := p.resolveToolLister(serverName)
	if !ok || lister == nil {
		return nil, fmt.Errorf("no live client for server %q", serverName)
	}
	// Never poke a disconnected client: ListTools would fail anyway, and the
	// caller's "server is disconnected" error is the more useful message.
	if !lister.IsConnected() {
		return nil, fmt.Errorf("server %q is not connected", serverName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), liveToolExportTimeout)
	defer cancel()
	tools, err := lister.ListTools(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		result = append(result, toolMetadataToScanMap(serverName, tool))
	}
	if p.server != nil {
		p.server.logger.Info("Exported tool definitions for scanning via live client (quarantine-safe path)",
			zap.String("server", serverName),
			zap.Int("tools", len(result)))
	}
	return result, nil
}

// resolveToolLister returns the live upstream client for serverName. Tests
// override it via the liveTools hook.
func (p *configServerInfoProvider) resolveToolLister(serverName string) (scanToolLister, bool) {
	if p.liveTools != nil {
		return p.liveTools(serverName)
	}
	if p.server == nil || p.server.runtime == nil {
		return nil, false
	}
	mgr := p.server.runtime.UpstreamManager()
	if mgr == nil {
		return nil, false
	}
	client, exists := mgr.GetClient(serverName)
	if !exists || client == nil {
		return nil, false
	}
	return client, true
}

// toolMetadataToScanMap converts a live tool definition into the tools.json
// shape the scanners parse (name / description / inputSchema), mirroring
// Server.GetServerTools' output.
func toolMetadataToScanMap(serverName string, tool *config.ToolMetadata) map[string]interface{} {
	inputSchema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
	if tool.ParamsJSON != "" {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(tool.ParamsJSON), &parsed); err == nil {
			inputSchema = parsed
		}
	}
	toolMap := map[string]interface{}{
		"name":        tool.Name,
		"description": tool.Description,
		"inputSchema": inputSchema,
		"server_name": serverName,
	}
	if tool.Annotations != nil {
		annotations := map[string]interface{}{}
		if tool.Annotations.Title != "" {
			annotations["title"] = tool.Annotations.Title
		}
		if tool.Annotations.ReadOnlyHint != nil {
			annotations["readOnlyHint"] = *tool.Annotations.ReadOnlyHint
		}
		if tool.Annotations.DestructiveHint != nil {
			annotations["destructiveHint"] = *tool.Annotations.DestructiveHint
		}
		if tool.Annotations.IdempotentHint != nil {
			annotations["idempotentHint"] = *tool.Annotations.IdempotentHint
		}
		if tool.Annotations.OpenWorldHint != nil {
			annotations["openWorldHint"] = *tool.Annotations.OpenWorldHint
		}
		if len(annotations) > 0 {
			toolMap["annotations"] = annotations
		}
	}
	return toolMap
}

// GetAllServerTools implements scanner's optional allServerToolsProvider: it
// returns every configured server's current tool definitions, keyed by server
// name, so a scan can build a cross-server snapshot for the shadowing check.
// Best-effort: servers that error or expose no tools are skipped, never fatal.
func (p *configServerInfoProvider) GetAllServerTools() (map[string][]map[string]interface{}, error) {
	cfg := p.currentConfig()
	if cfg == nil || p.server == nil {
		return nil, nil
	}
	all := make(map[string][]map[string]interface{}, len(cfg.Servers))
	for _, sc := range cfg.Servers {
		tools, err := p.server.GetServerTools(sc.Name)
		if err != nil || len(tools) == 0 {
			continue
		}
		all[sc.Name] = tools
	}
	return all, nil
}

// EnsureConnected attempts to connect a disconnected server so tool definitions
// can be retrieved for security scanning. For quarantined servers, grants a
// temporary inspection exemption so the supervisor allows the connection.
func (p *configServerInfoProvider) EnsureConnected(ctx context.Context, serverName string) error {
	if p.server == nil {
		return fmt.Errorf("server instance not available")
	}

	// For quarantined servers, grant inspection exemption before restart.
	// Without this, the supervisor refuses to connect quarantined servers.
	supervisor := p.server.runtime.Supervisor()
	if supervisor != nil {
		snapshot := supervisor.StateView().Snapshot()
		if ss, exists := snapshot.Servers[serverName]; exists && ss.Quarantined {
			if err := supervisor.RequestInspectionExemption(serverName, 15*time.Minute); err != nil {
				p.server.logger.Warn("Failed to grant inspection exemption for scan",
					zap.String("server", serverName), zap.Error(err))
			}
		}
	}

	return p.server.runtime.RestartServer(serverName)
}

// IsConnected checks if the server has an active MCP connection via the stateview.
func (p *configServerInfoProvider) IsConnected(serverName string) bool {
	if p.server == nil {
		return false
	}
	supervisor := p.server.runtime.Supervisor()
	if supervisor == nil {
		return false
	}
	snapshot := supervisor.StateView().Snapshot()
	serverStatus, exists := snapshot.Servers[serverName]
	if !exists {
		return false
	}
	return serverStatus.Connected
}
