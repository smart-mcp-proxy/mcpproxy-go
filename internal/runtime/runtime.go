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
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/server/tokens"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/truncate"
	"mcpproxy-go/internal/upstream"
)

// Status captures high-level state for API consumers.
type Status struct {
	Phase         string                 `json:"phase"`
	Message       string                 `json:"message"`
	UpstreamStats map[string]interface{} `json:"upstream_stats"`
	ToolsIndexed  int                    `json:"tools_indexed"`
	LastUpdated   time.Time              `json:"last_updated"`
}

// Runtime owns the non-HTTP lifecycle for the proxy process.
type Runtime struct {
	cfg     *config.Config
	cfgPath string
	logger  *zap.Logger

	mu      sync.RWMutex
	running bool

	statusMu sync.RWMutex
	status   Status
	statusCh chan Status

	eventMu   sync.RWMutex
	eventSubs map[chan Event]struct{}

	storageManager  *storage.Manager
	indexManager    *index.Manager
	upstreamManager *upstream.Manager
	cacheManager    *cache.Manager
	truncator       *truncate.Truncator
	secretResolver  *secret.Resolver
	tokenizer       tokens.Tokenizer

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

	rt := &Runtime{
		cfg:             cfg,
		cfgPath:         cfgPath,
		logger:          logger,
		storageManager:  storageManager,
		indexManager:    indexManager,
		upstreamManager: upstreamManager,
		cacheManager:    cacheManager,
		truncator:       truncator,
		secretResolver:  secretResolver,
		tokenizer:       tokenizer,
		appCtx:          appCtx,
		appCancel:       appCancel,
		status: Status{
			Phase:       "Initializing",
			Message:     "Runtime is initializing...",
			LastUpdated: time.Now(),
		},
		statusCh:  make(chan Status, 10),
		eventSubs: make(map[chan Event]struct{}),
	}

	return rt, nil
}

// Config returns the underlying configuration pointer.
func (r *Runtime) Config() *config.Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

// ConfigPath returns the tracked config path.
func (r *Runtime) ConfigPath() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfgPath
}

// UpdateConfig replaces the runtime configuration in-place.
func (r *Runtime) UpdateConfig(cfg *config.Config, cfgPath string) {
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
func (r *Runtime) UpdateStatus(phase, message string, stats map[string]interface{}, toolsIndexed int) {
	r.statusMu.Lock()
	r.status.Phase = phase
	r.status.Message = message
	r.status.LastUpdated = time.Now()
	r.status.UpstreamStats = stats
	r.status.ToolsIndexed = toolsIndexed
	snapshot := r.status
	r.statusMu.Unlock()

	select {
	case r.statusCh <- snapshot:
	default:
	}

	if r.logger != nil {
		r.logger.Info("Status updated", zap.String("phase", phase), zap.String("message", message))
	}
}

// UpdatePhase gathers runtime metrics and broadcasts a status update.
func (r *Runtime) UpdatePhase(phase, message string) {
	var (
		stats map[string]interface{}
		tools int
	)

	if r.upstreamManager != nil {
		stats = r.upstreamManager.GetStats()
		tools = extractToolCount(stats)
	}

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
	defer r.mu.Unlock()

	// Validate the new configuration first
	validationErrors := newCfg.ValidateDetailed()
	if len(validationErrors) > 0 {
		return &ConfigApplyResult{
			Success: false,
		}, fmt.Errorf("configuration validation failed: %v", validationErrors[0].Error())
	}

	// Detect changes and determine if restart is required
	result := DetectConfigChanges(r.cfg, newCfg)
	if !result.Success {
		return result, fmt.Errorf("failed to detect config changes")
	}

	// If restart is required, don't apply changes (let user restart)
	if result.RequiresRestart {
		r.logger.Warn("Configuration changes require restart",
			zap.String("reason", result.RestartReason),
			zap.Strings("changed_fields", result.ChangedFields))
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

	// Update upstream manager configuration if servers changed
	if contains(result.ChangedFields, "mcpServers") {
		r.logger.Info("Server configuration changed, triggering reload")

		// Reload servers asynchronously to avoid holding lock
		// This ensures storage and upstream manager are synced with new config
		go func() {
			if err := r.LoadConfiguredServers(); err != nil {
				r.logger.Error("Failed to reload servers after config apply", zap.Error(err))
				return
			}

			// Re-index tools after servers are reloaded
			ctx := r.AppContext()
			if ctx == nil {
				r.logger.Warn("Application context not available for tool re-indexing")
				return
			}

			// Brief delay to let server connections stabilize
			time.Sleep(500 * time.Millisecond)

			if err := r.DiscoverAndIndexTools(ctx); err != nil {
				r.logger.Error("Failed to re-index tools after config apply", zap.Error(err))
			}
		}()
	}

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

	// Emit config.reloaded event
	r.emitConfigReloaded(r.cfgPath)

	// Emit servers.changed event if servers were modified
	if contains(result.ChangedFields, "mcpServers") {
		r.emitServersChanged("config hot-reload", map[string]any{
			"changed_fields": result.ChangedFields,
		})
	}

	r.logger.Info("Configuration hot-reload completed successfully",
		zap.Strings("changed_fields", result.ChangedFields))

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
