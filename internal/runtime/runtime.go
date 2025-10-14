package runtime

import (
	"context"
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
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/registries"
	"mcpproxy-go/internal/runtime/configsvc"
	"mcpproxy-go/internal/runtime/supervisor"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/server/tokens"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/truncate"
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

	storageManager  *storage.Manager
	indexManager    *index.Manager
	upstreamManager *upstream.Manager
	cacheManager    *cache.Manager
	truncator       *truncate.Truncator
	secretResolver  *secret.Resolver
	tokenizer       tokens.Tokenizer

	// Phase 6: Supervisor for state reconciliation (lock-free reads via StateView)
	supervisor *supervisor.Supervisor

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

	indexManager, err := index.NewManager(cfg.DataDir, logger)
	if err != nil {
		_ = storageManager.Close()
		return nil, fmt.Errorf("failed to initialize index manager: %w", err)
	}

	// Initialize secret resolver
	secretResolver := secret.NewResolver()

	upstreamManager := upstream.NewManager(logger, cfg, storageManager.GetBoltDB(), secretResolver)
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

	// Phase 6: Stop Supervisor first to stop reconciliation
	if r.supervisor != nil {
		r.supervisor.Stop()
		if r.logger != nil {
			r.logger.Info("Supervisor stopped")
		}
	}

	if r.upstreamManager != nil {
		if err := r.upstreamManager.DisconnectAll(); err != nil {
			errs = append(errs, fmt.Errorf("disconnect upstream servers: %w", err))
			if r.logger != nil {
				r.logger.Error("Failed to disconnect upstream servers", zap.Error(err))
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
			ID:         call.ID,
			ServerID:   call.ServerID,
			ServerName: call.ServerName,
			ToolName:   call.ToolName,
			Arguments:  call.Arguments,
			Response:   call.Response,
			Error:      call.Error,
			Duration:   call.Duration,
			Timestamp:  call.Timestamp,
			ConfigPath: call.ConfigPath,
			RequestID:  call.RequestID,
			Metrics:    convertTokenMetrics(call.Metrics),
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
					ID:         call.ID,
					ServerID:   call.ServerID,
					ServerName: call.ServerName,
					ToolName:   call.ToolName,
					Arguments:  call.Arguments,
					Response:   call.Response,
					Error:      call.Error,
					Duration:   call.Duration,
					Timestamp:  call.Timestamp,
					ConfigPath: call.ConfigPath,
					RequestID:  call.RequestID,
					Metrics:    convertTokenMetrics(call.Metrics),
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
			ID:         call.ID,
			ServerID:   call.ServerID,
			ServerName: call.ServerName,
			ToolName:   call.ToolName,
			Arguments:  call.Arguments,
			Response:   call.Response,
			Error:      call.Error,
			Duration:   call.Duration,
			Timestamp:  call.Timestamp,
			ConfigPath: call.ConfigPath,
			RequestID:  call.RequestID,
			Metrics:    convertTokenMetrics(call.Metrics),
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
		ID:         newCall.ID,
		ServerID:   newCall.ServerID,
		ServerName: newCall.ServerName,
		ToolName:   newCall.ToolName,
		Arguments:  newCall.Arguments,
		Response:   newCall.Response,
		Error:      newCall.Error,
		Duration:   newCall.Duration,
		Timestamp:  newCall.Timestamp,
		ConfigPath: newCall.ConfigPath,
		RequestID:  newCall.RequestID,
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

	// If restart is required, don't apply changes (let user restart)
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

	// Save configuration to disk
	saveErr := config.SaveConfig(newCfg, r.cfgPath)
	if saveErr != nil {
		r.logger.Error("Failed to save configuration to disk",
			zap.String("path", r.cfgPath),
			zap.Error(saveErr))
		// Don't fail the entire operation, but log the error
		// In-memory changes are still applied
	} else {
		r.logger.Info("Configuration successfully saved to disk",
			zap.String("path", r.cfgPath))
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
