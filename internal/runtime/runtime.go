package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/cache"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/contracts"
	"mcpproxy-go/internal/experiments"
	"mcpproxy-go/internal/health"
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/oauth"
	"mcpproxy-go/internal/registries"
	"mcpproxy-go/internal/runtime/configsvc"
	"mcpproxy-go/internal/runtime/supervisor"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/server/tokens"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/truncate"
	"mcpproxy-go/internal/updatecheck"
	"mcpproxy-go/internal/upstream"
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

	storageManager    *storage.Manager
	indexManager      *index.Manager
	upstreamManager   *upstream.Manager
	cacheManager      *cache.Manager
	truncator         *truncate.Truncator
	secretResolver    *secret.Resolver
	tokenizer         tokens.Tokenizer
	refreshManager    *oauth.RefreshManager    // Proactive OAuth token refresh
	updateChecker     *updatecheck.Checker     // Background version checking
	managementService interface{}              // Initialized later to avoid import cycle

	// Phase 6: Supervisor for state reconciliation (lock-free reads via StateView)
	supervisor *supervisor.Supervisor

	// Tool discovery deduplication: tracks servers with in-progress reactive discovery
	// Key: serverName, Value: struct{} (presence indicates discovery in progress)
	discoveryInProgress sync.Map

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

	rt := &Runtime{
		cfg:             cfg,
		cfgPath:         cfgPath,
		logger:          logger,
		configSvc:       configSvc,
		storageManager:  storageManager,
		indexManager:    indexManager,
		upstreamManager: upstreamManager,
		cacheManager:    cacheManager,
		truncator:       truncator,
		secretResolver:  secretResolver,
		tokenizer:       tokenizer,
		refreshManager:  refreshManager,
		supervisor:      supervisorInstance,
		appCtx:          appCtx,
		appCancel:       appCancel,
		status: Status{
			Phase:       PhaseInitializing,
			Message:     "Runtime is initializing...",
			LastUpdated: time.Now(),
		},
		statusCh:     make(chan Status, 10),
		eventSubs:    make(map[chan Event]struct{}),
		phaseMachine: newPhaseMachine(PhaseInitializing),
	}

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

		// Verify all containers stopped with retry loop (15 attempts = 15 seconds)
		if r.upstreamManager.HasDockerContainers() {
			if r.logger != nil {
				r.logger.Warn("Docker containers still running after shutdown, verifying cleanup...")
			}

			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()

			for attempt := 0; attempt < 15; attempt++ {
				select {
				case <-shutdownCtx.Done():
					if r.logger != nil {
						r.logger.Error("Cleanup verification timeout")
					}
					// Force cleanup as last resort
					r.upstreamManager.ForceCleanupAllContainers()
					return nil
				case <-ticker.C:
					if !r.upstreamManager.HasDockerContainers() {
						if r.logger != nil {
							r.logger.Info("All containers cleaned up successfully", zap.Int("attempts", attempt+1))
						}
						return nil
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
	}

	if r.cacheManager != nil {
		r.cacheManager.Close()
	}

	if r.indexManager != nil {
		if err := r.indexManager.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close index manager: %w", err))
		}
	}

	if r.storageManager != nil {
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
		})
	}

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

// ListRegistries returns the list of available MCP server registries (Phase 7)
func (r *Runtime) ListRegistries() ([]interface{}, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Import registries package dynamically to avoid import cycles
	// For now, we'll return registries from config or use defaults
	registries := r.cfg.Registries
	if len(registries) == 0 {
		// Return default registry (Smithery)
		defaultRegistry := map[string]interface{}{
			"id":          "smithery",
			"name":        "Smithery MCP Registry",
			"description": "The official community registry for Model Context Protocol (MCP) servers.",
			"url":         "https://smithery.ai/protocols",
			"servers_url": "https://smithery.ai/api/smithery-protocol-registry",
			"tags":        []string{"official", "community"},
			"protocol":    "modelcontextprotocol/registry",
			"count":       -1,
		}
		return []interface{}{defaultRegistry}, nil
	}

	// Convert config registries to interface slice
	result := make([]interface{}, 0, len(registries))
	for _, reg := range registries {
		regMap := map[string]interface{}{
			"id":          reg.ID,
			"name":        reg.Name,
			"description": reg.Description,
			"url":         reg.URL,
			"servers_url": reg.ServersURL,
			"tags":        reg.Tags,
			"protocol":    reg.Protocol,
			"count":       reg.Count,
		}
		result = append(result, regMap)
	}

	return result, nil
}

// SearchRegistryServers searches for servers in a specific registry (Phase 7)
func (r *Runtime) SearchRegistryServers(registryID, tag, query string, limit int) ([]interface{}, error) {
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

	// Create a guesser for repository detection (with caching)
	guesser := experiments.NewGuesser(r.cacheManager, r.logger)

	// Search the registry
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	servers, err := registries.SearchServers(ctx, registryID, tag, query, limit, guesser)
	if err != nil {
		return nil, fmt.Errorf("failed to search registry: %w", err)
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

	r.logger.Info("Registry search completed",
		zap.String("registry_id", registryID),
		zap.Int("results", len(result)))

	return result, nil
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

		// Check for OAuth error in last_error
		if oauthStatus != string(oauth.OAuthStatusExpired) && serverStatus.LastError != "" {
			if oauth.IsOAuthError(serverStatus.LastError) {
				oauthStatus = string(oauth.OAuthStatusError)
			}
		}

		serverMap := map[string]interface{}{
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
		if r.upstreamManager != nil {
			if client, exists := r.upstreamManager.GetClient(serverStatus.Name); exists && client != nil {
				userLoggedOut = client.IsUserLoggedOut()
			}
		}
		serverMap["user_logged_out"] = userLoggedOut

		// Calculate unified health status
		healthConfig := health.DefaultHealthConfig()
		if r.cfg != nil && r.cfg.OAuthExpiryWarningHours > 0 {
			healthConfig.ExpiryWarningDuration = time.Duration(r.cfg.OAuthExpiryWarningHours * float64(time.Hour))
		}

		healthInput := health.HealthCalculatorInput{
			Name:            serverStatus.Name,
			Enabled:         serverStatus.Enabled,
			Quarantined:     serverStatus.Quarantined,
			State:           serverStatus.State,
			Connected:       connected,
			LastError:       serverStatus.LastError,
			OAuthRequired:   oauthConfig != nil,
			OAuthStatus:     oauthStatus,
			HasRefreshToken: hasRefreshToken,
			UserLoggedOut:   userLoggedOut,
			ToolCount:       serverStatus.ToolCount,
		}
		if !tokenExpiresAt.IsZero() {
			healthInput.TokenExpiresAt = &tokenExpiresAt
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
		return fmt.Errorf("failed to refresh OAuth token: %w", err)
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
	r.logger.Info("Update checker initialized", zap.String("version", version))
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
