package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/supervisor"
)

const connectAttemptTimeout = 45 * time.Second

// StartBackgroundInitialization kicks off configuration sync and background loops.
func (r *Runtime) StartBackgroundInitialization() {
	// Start activity service for persisting tool call events
	if r.activityService != nil {
		go r.activityService.Start(r.appCtx, r)
		r.logger.Info("Activity service started for event logging")
	}

	// Start update checker for background version checking
	if r.updateChecker != nil {
		go r.updateChecker.Start(r.appCtx)
		r.logger.Info("Update checker background process started")
	}

	// Start proactive OAuth token refresh manager
	if r.refreshManager != nil {
		r.refreshManager.SetRuntime(r)
		r.refreshManager.SetEventEmitter(r)
		if err := r.refreshManager.Start(r.appCtx); err != nil {
			r.logger.Error("Failed to start OAuth refresh manager", zap.Error(err))
		} else {
			r.logger.Info("OAuth refresh manager started")
		}

		// Register token saved callback to wire PersistentTokenStore -> RefreshManager
		oauth.GetTokenStoreManager().SetTokenSavedCallback(func(serverName string, expiresAt time.Time) {
			r.refreshManager.OnTokenSaved(serverName, expiresAt)
		})
		r.logger.Info("Token saved callback registered for proactive refresh")
	}

	// Phase 6: Start Supervisor for state reconciliation and lock-free reads
	if r.supervisor != nil {
		r.supervisor.Start()
		r.logger.Info("Supervisor started for state reconciliation")

		// Set up reactive tool discovery callback with deduplication
		r.supervisor.SetOnServerConnectedCallback(func(serverName string) {
			// Deduplication: Check if discovery is already in progress for this server
			if _, loaded := r.discoveryInProgress.LoadOrStore(serverName, struct{}{}); loaded {
				r.logger.Debug("Tool discovery already in progress for server, skipping duplicate",
					zap.String("server", serverName))
				return
			}

			// Ensure we clean up the in-progress marker
			defer r.discoveryInProgress.Delete(serverName)

			ctx, cancel := context.WithTimeout(r.AppContext(), 30*time.Second)
			defer cancel()

			r.logger.Info("Reactive tool discovery triggered", zap.String("server", serverName))
			if err := r.DiscoverAndIndexToolsForServer(ctx, serverName); err != nil {
				r.logger.Error("Failed to discover tools for connected server",
					zap.String("server", serverName),
					zap.Error(err))
			}
		})
		r.logger.Info("Reactive tool discovery callback registered")

		// Subscribe to supervisor events and emit servers.changed for Web UI updates
		go r.supervisorEventForwarder()
	}

	// Set up tool discovery callback on upstream manager for notifications/tools/list_changed
	// This enables reactive tool re-indexing when upstream servers change their available tools
	if r.upstreamManager != nil {
		r.upstreamManager.SetToolDiscoveryCallback(func(ctx context.Context, serverName string) error {
			// Deduplication: Check if discovery is already in progress for this server
			if _, loaded := r.discoveryInProgress.LoadOrStore(serverName, struct{}{}); loaded {
				r.logger.Debug("Tool discovery already in progress for server (notification), skipping duplicate",
					zap.String("server", serverName))
				return nil
			}

			// Ensure we clean up the in-progress marker
			defer r.discoveryInProgress.Delete(serverName)

			r.logger.Info("Tool discovery triggered by notification", zap.String("server", serverName))
			return r.DiscoverAndIndexToolsForServer(ctx, serverName)
		})
		r.logger.Info("Tool discovery callback registered on upstream manager")
	}

	go r.backgroundInitialization()
}

func (r *Runtime) backgroundInitialization() {
	if r.CurrentPhase() == PhaseInitializing {
		r.UpdatePhase(PhaseLoading, "Loading configuration...")
	} else {
		r.UpdatePhaseMessage("Loading configuration...")
	}

	appCtx := r.AppContext()

	// Load configured servers - saves to storage synchronously (fast ~100-200ms),
	// then starts connections asynchronously (slow 30s+)
	// We do this synchronously to ensure API /servers endpoint has data immediately
	if err := r.LoadConfiguredServers(nil); err != nil {
		r.logger.Error("Failed to load configured servers", zap.Error(err))
		// Don't set error phase - servers can be loaded later via config reload
	}

	// Mark as ready - storage is now populated with server configs
	switch r.CurrentPhase() {
	case PhaseInitializing, PhaseLoading, PhaseReady:
		r.UpdatePhase(PhaseReady, "Server is ready (upstream servers connecting in background)")
	default:
		r.UpdatePhaseMessage("Server is ready (upstream servers connecting in background)")
	}

	// Start connection retry attempts in background
	go r.backgroundConnections(appCtx)

	// Start tool indexing with reduced delay
	go r.backgroundToolIndexing(appCtx)

	// Start session inactivity cleanup
	go r.backgroundSessionCleanup(appCtx)
}

func (r *Runtime) backgroundConnections(ctx context.Context) {
	r.connectAllWithRetry(ctx)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.connectAllWithRetry(ctx)
		case <-ctx.Done():
			r.logger.Info("Background connections stopped due to context cancellation")
			return
		}
	}
}

func (r *Runtime) connectAllWithRetry(ctx context.Context) {
	if r.upstreamManager == nil {
		return
	}

	stats := r.upstreamManager.GetStats()
	connectedCount := 0
	totalCount := 0

	if serverStats, ok := stats["servers"].(map[string]interface{}); ok {
		totalCount = len(serverStats)
		for _, serverStat := range serverStats {
			if stat, ok := serverStat.(map[string]interface{}); ok {
				if connected, ok := stat["connected"].(bool); ok && connected {
					connectedCount++
				}
			}
		}
	}

	if connectedCount < totalCount {
		r.UpdatePhaseMessage(fmt.Sprintf("Connected to %d/%d servers, retrying...", connectedCount, totalCount))

		connectCtx, cancel := context.WithTimeout(ctx, connectAttemptTimeout)
		defer cancel()

		if err := r.upstreamManager.ConnectAll(connectCtx); err != nil {
			r.logger.Warn("Some upstream servers failed to connect", zap.Error(err))
		}
	}
}

func (r *Runtime) backgroundToolIndexing(ctx context.Context) {
	select {
	case <-time.After(2 * time.Second):
		_ = r.DiscoverAndIndexTools(ctx)
	case <-ctx.Done():
		r.logger.Info("Background tool indexing stopped during initial delay")
		return
	}

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = r.DiscoverAndIndexTools(ctx)
		case <-ctx.Done():
			r.logger.Info("Background tool indexing stopped due to context cancellation")
			return
		}
	}
}

// backgroundSessionCleanup periodically closes sessions that haven't had activity.
// This handles the HTTP transport limitation where OnUnregisterSession is never called.
func (r *Runtime) backgroundSessionCleanup(ctx context.Context) {
	// Session inactivity timeout: 5 minutes
	// This is a reasonable timeout for MCP sessions where clients typically
	// send tool calls every few seconds during active use.
	const sessionInactivityTimeout = 5 * time.Minute

	// Check every minute for inactive sessions
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if r.storageManager != nil {
				closedCount, err := r.storageManager.CloseInactiveSessions(sessionInactivityTimeout)
				if err != nil {
					r.logger.Warn("Failed to close inactive sessions", zap.Error(err))
				} else if closedCount > 0 {
					r.logger.Debug("Closed inactive sessions",
						zap.Int("count", closedCount),
						zap.Duration("timeout", sessionInactivityTimeout))
				}
			}
		case <-ctx.Done():
			r.logger.Info("Background session cleanup stopped due to context cancellation")
			return
		}
	}
}

// DiscoverAndIndexTools discovers tools from upstream servers and indexes them.
func (r *Runtime) DiscoverAndIndexTools(ctx context.Context) error {
	if r.upstreamManager == nil || r.indexManager == nil {
		return fmt.Errorf("runtime managers not initialized")
	}

	r.logger.Info("Discovering and indexing tools...")

	tools, err := r.upstreamManager.DiscoverTools(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover tools: %w", err)
	}

	if len(tools) == 0 {
		r.logger.Warn("No tools discovered from upstream servers")
		return nil
	}

	// Group tools by server name for differential updates
	toolsByServer := make(map[string][]*config.ToolMetadata)
	for _, tool := range tools {
		toolsByServer[tool.ServerName] = append(toolsByServer[tool.ServerName], tool)
	}

	// Apply differential update for each server
	for serverName, serverTools := range toolsByServer {
		if err := r.applyDifferentialToolUpdate(ctx, serverName, serverTools); err != nil {
			r.logger.Error("Failed to apply differential update for server",
				zap.String("server", serverName),
				zap.Error(err))
			// Continue with other servers instead of failing completely
		}
	}

	// Invalidate tool count caches since tools may have changed
	r.upstreamManager.InvalidateAllToolCountCaches()

	// Update StateView with discovered tools
	if r.supervisor != nil {
		if err := r.supervisor.RefreshToolsFromDiscovery(tools); err != nil {
			r.logger.Warn("Failed to refresh tools in StateView", zap.Error(err))
			// Don't fail the entire operation if StateView update fails
		} else {
			r.logger.Debug("Successfully refreshed tools in StateView", zap.Int("tool_count", len(tools)))
		}
	}

	r.logger.Info("Successfully indexed tools", zap.Int("count", len(tools)))
	return nil
}

// DiscoverAndIndexToolsForServer discovers and indexes tools for a single server.
// This is used for reactive tool discovery when a server connects.
// Implements retry logic with exponential backoff for robustness.
func (r *Runtime) DiscoverAndIndexToolsForServer(ctx context.Context, serverName string) error {
	if r.upstreamManager == nil || r.indexManager == nil {
		return fmt.Errorf("runtime managers not initialized")
	}

	r.logger.Info("Discovering and indexing tools for server", zap.String("server", serverName))

	// Get the upstream client for this server
	client, ok := r.upstreamManager.GetClient(serverName)
	if !ok {
		return fmt.Errorf("client not found for server %s", serverName)
	}

	// Retry logic: Sometimes connection events fire slightly before the server is fully ready
	// We retry up to 3 times with exponential backoff (500ms, 1s, 2s)
	var tools []*config.ToolMetadata
	var err error
	maxRetries := 3
	baseDelay := 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(1<<uint(attempt-1)) // Exponential backoff
			r.logger.Debug("Retrying tool discovery after delay",
				zap.String("server", serverName),
				zap.Int("attempt", attempt+1),
				zap.Duration("delay", delay))

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during retry backoff: %w", ctx.Err())
			}
		}

		// Discover tools from this server
		tools, err = client.ListTools(ctx)
		if err == nil {
			break // Success!
		}

		// Log the error for debugging
		r.logger.Warn("Tool discovery attempt failed",
			zap.String("server", serverName),
			zap.Int("attempt", attempt+1),
			zap.Int("max_retries", maxRetries),
			zap.Error(err))

		// Don't retry on context cancellation
		if ctx.Err() != nil {
			return fmt.Errorf("context cancelled during tool discovery: %w", ctx.Err())
		}
	}

	// After all retries, check if we still have an error
	if err != nil {
		return fmt.Errorf("failed to list tools for server %s after %d attempts: %w", serverName, maxRetries, err)
	}

	if len(tools) == 0 {
		r.logger.Warn("No tools discovered from server", zap.String("server", serverName))
		return nil
	}

	// Apply differential update: compare new tools with existing indexed tools
	if err := r.applyDifferentialToolUpdate(ctx, serverName, tools); err != nil {
		return fmt.Errorf("failed to apply differential tool update for server %s: %w", serverName, err)
	}

	// Invalidate tool count caches since tools may have changed
	r.upstreamManager.InvalidateAllToolCountCaches()

	// Update StateView with discovered tools
	if r.supervisor != nil {
		if err := r.supervisor.RefreshToolsFromDiscovery(tools); err != nil {
			r.logger.Warn("Failed to refresh tools in StateView for server",
				zap.String("server", serverName),
				zap.Error(err))
		} else {
			r.logger.Debug("Successfully refreshed tools in StateView for server",
				zap.String("server", serverName),
				zap.Int("tool_count", len(tools)))
		}
	}

	r.logger.Info("Successfully indexed tools for server",
		zap.String("server", serverName),
		zap.Int("count", len(tools)))
	return nil
}

// applyDifferentialToolUpdate performs differential update of tools for a server.
// It compares new tools with existing indexed tools and applies only the changes:
// - Removed tools are deleted from the index
// - Added tools are indexed
// - Modified tools (different hash) are re-indexed
func (r *Runtime) applyDifferentialToolUpdate(ctx context.Context, serverName string, newTools []*config.ToolMetadata) error {
	// Query existing tools from the index
	existingTools, err := r.indexManager.GetToolsByServer(serverName)
	if err != nil {
		r.logger.Warn("Failed to query existing tools, performing full re-index",
			zap.String("server", serverName),
			zap.Error(err))
		// Fall back to full batch index
		return r.indexManager.BatchIndexTools(newTools)
	}

	// Build maps for efficient lookup
	// Extract tool name without server prefix for comparison
	oldToolsMap := make(map[string]*config.ToolMetadata)
	for _, tool := range existingTools {
		toolName := tool.Name
		// Remove server prefix if present (format: "server:tool")
		if idx := strings.Index(tool.Name, ":"); idx != -1 {
			toolName = tool.Name[idx+1:]
		}
		oldToolsMap[toolName] = tool
	}

	newToolsMap := make(map[string]*config.ToolMetadata)
	for _, tool := range newTools {
		toolName := tool.Name
		// Remove server prefix if present
		if idx := strings.Index(tool.Name, ":"); idx != -1 {
			toolName = tool.Name[idx+1:]
		}
		newToolsMap[toolName] = tool
	}

	// Detect changes
	var addedTools []*config.ToolMetadata
	var modifiedTools []*config.ToolMetadata
	var removedTools []string

	// Find added and modified tools
	for toolName, newTool := range newToolsMap {
		oldTool, exists := oldToolsMap[toolName]
		if !exists {
			// Tool is new
			addedTools = append(addedTools, newTool)
		} else if oldTool.Hash != newTool.Hash {
			// Tool exists but has changed (different hash)
			modifiedTools = append(modifiedTools, newTool)
		}
		// else: tool unchanged, no action needed
	}

	// Find removed tools
	for toolName := range oldToolsMap {
		if _, exists := newToolsMap[toolName]; !exists {
			removedTools = append(removedTools, toolName)
		}
	}

	// Log the changes
	if len(addedTools) > 0 || len(modifiedTools) > 0 || len(removedTools) > 0 {
		r.logger.Info("Tool changes detected for server",
			zap.String("server", serverName),
			zap.Int("added", len(addedTools)),
			zap.Int("modified", len(modifiedTools)),
			zap.Int("removed", len(removedTools)))
	} else {
		r.logger.Debug("No tool changes detected for server",
			zap.String("server", serverName),
			zap.Int("tool_count", len(newTools)))
	}

	// Apply changes

	// 1. Delete removed tools
	for _, toolName := range removedTools {
		r.logger.Info("Removing tool from index",
			zap.String("server", serverName),
			zap.String("tool", toolName))

		if err := r.indexManager.DeleteTool(serverName, toolName); err != nil {
			r.logger.Error("Failed to delete tool from index",
				zap.String("server", serverName),
				zap.String("tool", toolName),
				zap.Error(err))
		}

		// Clean up hash storage
		fullToolName := fmt.Sprintf("%s:%s", serverName, toolName)
		if r.storageManager != nil {
			if err := r.storageManager.DeleteToolHash(fullToolName); err != nil {
				r.logger.Debug("Failed to delete tool hash",
					zap.String("tool", fullToolName),
					zap.Error(err))
			}
		}
	}

	// 2. Index added tools
	if len(addedTools) > 0 {
		r.logger.Info("Indexing new tools",
			zap.String("server", serverName),
			zap.Int("count", len(addedTools)))

		if err := r.indexManager.BatchIndexTools(addedTools); err != nil {
			return fmt.Errorf("failed to index added tools: %w", err)
		}
	}

	// 3. Re-index modified tools
	if len(modifiedTools) > 0 {
		r.logger.Info("Re-indexing modified tools",
			zap.String("server", serverName),
			zap.Int("count", len(modifiedTools)))

		for _, tool := range modifiedTools {
			r.logger.Debug("Tool schema changed",
				zap.String("server", serverName),
				zap.String("tool", tool.Name),
				zap.String("old_hash", oldToolsMap[extractToolName(tool.Name)].Hash),
				zap.String("new_hash", tool.Hash))
		}

		if err := r.indexManager.BatchIndexTools(modifiedTools); err != nil {
			return fmt.Errorf("failed to re-index modified tools: %w", err)
		}
	}

	return nil
}

// extractToolName removes the server prefix from a tool name if present
func extractToolName(fullName string) string {
	if idx := strings.Index(fullName, ":"); idx != -1 {
		return fullName[idx+1:]
	}
	return fullName
}

// LoadConfiguredServers synchronizes storage and upstream manager from the given or current config.
// If cfg is nil, it will use the current runtime configuration.
//
//nolint:unparam // maintained for parity with previous implementation
func (r *Runtime) LoadConfiguredServers(cfg *config.Config) error {
	if cfg == nil {
		cfg = r.Config()
		if cfg == nil {
			return fmt.Errorf("runtime configuration is not available")
		}
	}

	if r.storageManager == nil || r.upstreamManager == nil || r.indexManager == nil {
		return fmt.Errorf("runtime managers not initialized")
	}

	r.logger.Info("Synchronizing servers from configuration (config as source of truth)")

	currentUpstreams := r.upstreamManager.GetAllServerNames()
	storedServers, err := r.storageManager.ListUpstreamServers()
	if err != nil {
		r.logger.Error("Failed to get stored servers for sync", zap.Error(err))
		storedServers = []*config.ServerConfig{}
	}

	configuredServers := make(map[string]*config.ServerConfig)
	storedServerMap := make(map[string]*config.ServerConfig)
	var changed bool

	for _, serverCfg := range cfg.Servers {
		configuredServers[serverCfg.Name] = serverCfg
	}

	for _, storedServer := range storedServers {
		storedServerMap[storedServer.Name] = storedServer
	}

	// Add/remove servers asynchronously to prevent blocking on slow connections
	// All server operations now happen in background goroutines with timeouts

	// FIRST: Save all servers to storage in one batch (fast, synchronous)
	// This ensures API /servers endpoint can return data immediately
	r.logger.Debug("Starting synchronous storage save phase", zap.Int("total_servers", len(cfg.Servers)))
	for _, serverCfg := range cfg.Servers {
		storedServer, existsInStorage := storedServerMap[serverCfg.Name]

		// Check if OAuth config changed (requires reconnection)
		oauthChanged := existsInStorage && config.OAuthConfigChanged(storedServer.OAuth, serverCfg.OAuth)

		hasChanged := !existsInStorage ||
			storedServer.Enabled != serverCfg.Enabled ||
			storedServer.Quarantined != serverCfg.Quarantined ||
			storedServer.URL != serverCfg.URL ||
			storedServer.Command != serverCfg.Command ||
			storedServer.Protocol != serverCfg.Protocol ||
			oauthChanged

		if hasChanged {
			changed = true
			r.logger.Info("Server configuration changed, updating storage",
				zap.String("server", serverCfg.Name),
				zap.Bool("new", !existsInStorage),
				zap.Bool("enabled_changed", existsInStorage && storedServer.Enabled != serverCfg.Enabled),
				zap.Bool("quarantined_changed", existsInStorage && storedServer.Quarantined != serverCfg.Quarantined),
				zap.Bool("oauth_changed", oauthChanged))

			// Clear OAuth state if OAuth config changed
			if oauthChanged && r.storageManager != nil {
				r.logger.Info("OAuth config changed, clearing cached OAuth state",
					zap.String("server", serverCfg.Name))
				if err := r.storageManager.ClearOAuthState(serverCfg.Name); err != nil {
					r.logger.Warn("Failed to clear OAuth state",
						zap.String("server", serverCfg.Name),
						zap.Error(err))
				}
			}
		}

		// Save synchronously to ensure storage is populated for API queries
		r.logger.Debug("Saving server to storage", zap.String("server", serverCfg.Name), zap.Bool("exists", existsInStorage))
		if err := r.storageManager.SaveUpstreamServer(serverCfg); err != nil {
			r.logger.Error("Failed to save/update server in storage", zap.Error(err), zap.String("server", serverCfg.Name))
			continue
		}
		r.logger.Debug("Successfully saved server to storage", zap.String("server", serverCfg.Name))
	}
	r.logger.Debug("Completed synchronous storage save phase")

	// SECOND: Manage upstream connections asynchronously (slow, can take 30s+)
	for _, serverCfg := range cfg.Servers {
		if serverCfg.Enabled {
			// Add server asynchronously to prevent blocking on connections
			go func(cfg *config.ServerConfig, cfgPath string) {
				if err := r.upstreamManager.AddServer(cfg.Name, cfg); err != nil {
					r.logger.Error("Failed to add/update upstream server", zap.Error(err), zap.String("server", cfg.Name))
				} else {
					// Register server identity for tool call tracking
					if _, err := r.storageManager.RegisterServerIdentity(cfg, cfgPath); err != nil {
						r.logger.Warn("Failed to register server identity",
							zap.Error(err),
							zap.String("server", cfg.Name))
					}
				}

				if cfg.Quarantined {
					r.logger.Info("Server is quarantined but kept connected for security inspection", zap.String("server", cfg.Name))
				}
			}(serverCfg, r.cfgPath)
		} else {
			// Remove server asynchronously to prevent blocking
			go func(name string) {
				r.upstreamManager.RemoveServer(name)
				r.logger.Info("Server is disabled, removing from active connections", zap.String("server", name))
			}(serverCfg.Name)
		}
	}

	serversToRemove := []string{}

	for _, serverName := range currentUpstreams {
		if _, exists := configuredServers[serverName]; !exists {
			serversToRemove = append(serversToRemove, serverName)
		}
	}

	for _, storedServer := range storedServers {
		if _, exists := configuredServers[storedServer.Name]; !exists {
			found := false
			for _, name := range serversToRemove {
				if name == storedServer.Name {
					found = true
					break
				}
			}
			if !found {
				serversToRemove = append(serversToRemove, storedServer.Name)
			}
		}
	}

	// Remove servers asynchronously to prevent blocking
	for _, serverName := range serversToRemove {
		changed = true
		go func(name string) {
			r.logger.Info("Removing server no longer in config", zap.String("server", name))
			r.upstreamManager.RemoveServer(name)
			if err := r.storageManager.DeleteUpstreamServer(name); err != nil {
				r.logger.Error("Failed to delete server from storage", zap.Error(err), zap.String("server", name))
			}
			if err := r.indexManager.DeleteServerTools(name); err != nil {
				r.logger.Error("Failed to delete server tools from index", zap.Error(err), zap.String("server", name))
			} else {
				r.logger.Info("Removed server tools from search index", zap.String("server", name))
			}
		}(serverName)
	}

	if len(serversToRemove) > 0 {
		r.logger.Info("Comprehensive server cleanup completed",
			zap.Int("removed_count", len(serversToRemove)),
			zap.Strings("removed_servers", serversToRemove))
	}

	r.logger.Info("Server synchronization completed",
		zap.Int("configured_servers", len(cfg.Servers)),
		zap.Int("removed_servers", len(serversToRemove)))

	if changed {
		r.emitServersChanged("sync", map[string]any{
			"configured": len(cfg.Servers),
			"removed":    len(serversToRemove),
		})
	}

	return nil
}

// SaveConfiguration persists the runtime configuration to disk.
func (r *Runtime) SaveConfiguration() error {
	latestServers, err := r.storageManager.ListUpstreamServers()
	if err != nil {
		r.logger.Error("Failed to get latest server list from storage for saving", zap.Error(err))
		return err
	}

	// Get current snapshot (lock-free)
	snapshot := r.ConfigSnapshot()
	if snapshot.Config == nil {
		return fmt.Errorf("runtime configuration is not available")
	}

	if snapshot.Path == "" {
		r.logger.Warn("Configuration file path is not available, cannot save configuration")
		return fmt.Errorf("configuration file path is not available")
	}

	// Create a copy of config to avoid mutations
	configCopy := snapshot.Clone()
	if configCopy == nil {
		return fmt.Errorf("failed to clone configuration")
	}

	// Update servers with latest from storage
	configCopy.Servers = latestServers

	r.logger.Debug("Saving configuration to disk",
		zap.Int("server_count", len(latestServers)),
		zap.String("config_path", snapshot.Path),
		zap.Bool("using_config_service", r.configSvc != nil))

	// Use ConfigService to save (doesn't hold locks, handles file I/O)
	if r.configSvc != nil {
		// Update the config service with latest servers first
		if err := r.configSvc.Update(configCopy, configsvc.UpdateTypeModify, "save_configuration"); err != nil {
			r.logger.Error("Failed to update config service", zap.Error(err))
			return err
		}
		// Then persist to disk
		if err := r.configSvc.SaveToFile(); err != nil {
			r.logger.Error("Failed to save config to file via config service", zap.Error(err))
			return err
		}
		r.logger.Debug("Config saved to disk via config service")
	} else {
		// Fallback to legacy save
		if err := config.SaveConfig(configCopy, snapshot.Path); err != nil {
			r.logger.Error("Failed to save config to file (legacy path)", zap.Error(err))
			return err
		}
		r.logger.Debug("Config saved to disk via legacy path")
	}

	// Update in-memory config (applies to both configSvc and legacy paths)
	r.logger.Debug("Updating in-memory config with latest servers",
		zap.Int("server_count", len(latestServers)))

	r.mu.Lock()
	oldServerCount := len(r.cfg.Servers)
	r.cfg.Servers = latestServers
	r.mu.Unlock()

	r.logger.Debug("Configuration saved and in-memory config updated",
		zap.Int("old_server_count", oldServerCount),
		zap.Int("new_server_count", len(latestServers)),
		zap.String("config_path", snapshot.Path))

	// Emit config.saved event to notify subscribers (Web UI, tray, etc.)
	r.emitConfigSaved(snapshot.Path)

	return nil
}

// ReloadConfiguration reloads the configuration from disk and resyncs state.
func (r *Runtime) ReloadConfiguration() error {
	r.logger.Info("Reloading configuration from disk")

	// Get current snapshot before reload
	oldSnapshot := r.ConfigSnapshot()
	oldServerCount := oldSnapshot.ServerCount()
	dataDir := oldSnapshot.Config.DataDir

	cfgPath := config.GetConfigPath(dataDir)

	// Use ConfigService for file reload (handles disk I/O without holding locks)
	var newSnapshot *configsvc.Snapshot
	var err error
	if r.configSvc != nil {
		newSnapshot, err = r.configSvc.ReloadFromFile()
	} else {
		// Fallback to legacy path
		newConfig, loadErr := config.LoadFromFile(cfgPath)
		if loadErr != nil {
			return fmt.Errorf("failed to reload config: %w", loadErr)
		}
		r.UpdateConfig(newConfig, cfgPath)
		newSnapshot = r.ConfigSnapshot()
	}

	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	if err := r.LoadConfiguredServers(nil); err != nil {
		r.logger.Error("loadConfiguredServers failed", zap.Error(err))
		return fmt.Errorf("failed to reload servers: %w", err)
	}

	go r.postConfigReload()

	r.logger.Info("Configuration reload completed",
		zap.String("path", newSnapshot.Path),
		zap.Int64("version", newSnapshot.Version),
		zap.Int("old_server_count", oldServerCount),
		zap.Int("new_server_count", newSnapshot.ServerCount()),
		zap.Int("server_delta", newSnapshot.ServerCount()-oldServerCount))

	r.emitConfigReloaded(newSnapshot.Path)

	return nil
}

func (r *Runtime) postConfigReload() {
	ctx := r.AppContext()
	if ctx == nil {
		r.logger.Error("Application context is nil, cannot trigger reconnection")
		return
	}

	r.logger.Info("Triggering immediate reconnection after config reload")

	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := r.upstreamManager.ConnectAll(connectCtx); err != nil {
		r.logger.Warn("Some servers failed to reconnect after config reload", zap.Error(err))
	}

	select {
	case <-time.After(2 * time.Second):
		if err := r.DiscoverAndIndexTools(ctx); err != nil {
			r.logger.Error("Failed to re-index tools after config reload", zap.Error(err))
		}
	case <-ctx.Done():
		r.logger.Info("Tool re-indexing cancelled during config reload")
	}
}

// EnableServer enables or disables a server and persists the change.
func (r *Runtime) EnableServer(serverName string, enabled bool) error {
	r.logger.Info("Request to change server enabled state",
		zap.String("server", serverName),
		zap.Bool("enabled", enabled))

	if err := r.storageManager.EnableUpstreamServer(serverName, enabled); err != nil {
		r.logger.Error("Failed to update server enabled state in storage", zap.Error(err))
		return fmt.Errorf("failed to update server '%s' in storage: %w", serverName, err)
	}

	// Save configuration synchronously to ensure changes are persisted before returning
	if err := r.SaveConfiguration(); err != nil {
		r.logger.Error("Failed to save configuration after state change", zap.Error(err))
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	// Reload configuration synchronously to ensure server state is updated before returning
	if err := r.LoadConfiguredServers(nil); err != nil {
		r.logger.Error("Failed to synchronize runtime after enable toggle", zap.Error(err))
		return fmt.Errorf("failed to reload configuration: %w", err)
	}

	// Wait for the server to start connecting (LoadConfiguredServers spawns goroutines)
	// This ensures callers don't race with connection establishment
	// The goroutine needs time to spawn and then AddServer needs to initiate connection
	if enabled {
		time.Sleep(5 * time.Second)
		r.logger.Info("Waited for server to begin connection attempt",
			zap.String("server", serverName),
			zap.Bool("enabled", enabled))
	}

	r.emitServersChanged("enable_toggle", map[string]any{
		"server":  serverName,
		"enabled": enabled,
	})

	// Emit config change activity for audit trail (Spec 024)
	action := "server_disabled"
	if enabled {
		action = "server_enabled"
	}
	r.EmitActivityConfigChange(action, serverName, "api", []string{"enabled"}, map[string]interface{}{"enabled": !enabled}, map[string]interface{}{"enabled": enabled})

	r.HandleUpstreamServerChange(r.AppContext())

	return nil
}

// QuarantineServer updates the quarantine state and persists the change.
// Security: When quarantining a server, all its tools are removed from the index
// to prevent Tool Poisoning Attacks (TPA) from exposing potentially malicious tool descriptions.
func (r *Runtime) QuarantineServer(serverName string, quarantined bool) error {
	r.logger.Info("Request to change server quarantine state",
		zap.String("server", serverName),
		zap.Bool("quarantined", quarantined))

	if err := r.storageManager.QuarantineUpstreamServer(serverName, quarantined); err != nil {
		r.logger.Error("Failed to update server quarantine state in storage", zap.Error(err))
		return fmt.Errorf("failed to update quarantine state for server '%s' in storage: %w", serverName, err)
	}

	// Security: When quarantining a server, immediately remove its tools from the index
	// to prevent TPA exposure through search results
	if quarantined && r.indexManager != nil {
		if err := r.indexManager.DeleteServerTools(serverName); err != nil {
			r.logger.Warn("Failed to remove quarantined server tools from index",
				zap.String("server", serverName),
				zap.Error(err))
			// Continue even if deletion fails - the server is still quarantined
		} else {
			r.logger.Info("Removed quarantined server tools from index",
				zap.String("server", serverName))
		}
	}

	// Save configuration synchronously to ensure changes are persisted before returning
	if err := r.SaveConfiguration(); err != nil {
		r.logger.Error("Failed to save configuration after quarantine state change", zap.Error(err))
		return fmt.Errorf("failed to save configuration: %w", err)
	}

	// Reload configuration synchronously to ensure server state is updated before returning
	if err := r.LoadConfiguredServers(nil); err != nil {
		r.logger.Error("Failed to synchronize runtime after quarantine toggle", zap.Error(err))
		return fmt.Errorf("failed to reload configuration: %w", err)
	}

	r.emitServersChanged("quarantine_toggle", map[string]any{
		"server":      serverName,
		"quarantined": quarantined,
	})

	// Emit activity event for quarantine state change
	reason := "Server unquarantined by administrator"
	if quarantined {
		reason = "Server quarantined for security review"
	}
	r.EmitActivityQuarantineChange(serverName, quarantined, reason)

	r.HandleUpstreamServerChange(r.AppContext())

	r.logger.Info("Successfully persisted server quarantine state change",
		zap.String("server", serverName),
		zap.Bool("quarantined", quarantined))

	return nil
}

// BulkEnableServers toggles the enabled state for multiple servers in a single
// storage/config save to avoid repeated file writes. Returns a map of per-server
// errors for operations that could not be applied.
func (r *Runtime) BulkEnableServers(serverNames []string, enabled bool) (map[string]error, error) {
	resultErrs := make(map[string]error)
	if len(serverNames) == 0 {
		return resultErrs, nil
	}

	servers, err := r.storageManager.ListUpstreamServers()
	if err != nil {
		return nil, fmt.Errorf("failed to list servers: %w", err)
	}
	serversByName := make(map[string]*config.ServerConfig, len(servers))
	for _, srv := range servers {
		serversByName[srv.Name] = srv
	}

	var changed []string
	for _, name := range serverNames {
		cfg, ok := serversByName[name]
		if !ok {
			resultErrs[name] = fmt.Errorf("server '%s' not found", name)
			continue
		}
		if cfg.Enabled == enabled {
			r.logger.Debug("Skipping server already in desired enabled state",
				zap.String("server", name),
				zap.Bool("enabled", enabled))
			continue
		}
		if err := r.storageManager.EnableUpstreamServer(name, enabled); err != nil {
			resultErrs[name] = fmt.Errorf("failed to update server '%s' in storage: %w", name, err)
			continue
		}
		changed = append(changed, name)
	}

	// Nothing changed; return collected errors (if any)
	if len(changed) == 0 {
		return resultErrs, nil
	}

	// Persist once and reload once for all changes
	if err := r.SaveConfiguration(); err != nil {
		return resultErrs, fmt.Errorf("failed to save configuration: %w", err)
	}

	if err := r.LoadConfiguredServers(nil); err != nil {
		return resultErrs, fmt.Errorf("failed to reload configuration: %w", err)
	}

	r.emitServersChanged("bulk_enable_toggle", map[string]any{
		"enabled": enabled,
		"count":   len(changed),
	})

	r.HandleUpstreamServerChange(r.AppContext())

	return resultErrs, nil
}

// RestartServer restarts an upstream server by disconnecting and reconnecting it.
// This is a synchronous operation that waits for the restart to complete.
func (r *Runtime) RestartServer(serverName string) error {
	r.logger.Info("Request to restart server", zap.String("server", serverName))

	// Check if server exists in storage (config)
	servers, err := r.storageManager.ListUpstreamServers()
	if err != nil {
		return fmt.Errorf("failed to list servers: %w", err)
	}

	var serverConfig *config.ServerConfig
	for _, srv := range servers {
		if srv.Name == serverName {
			serverConfig = srv
			break
		}
	}

	if serverConfig == nil {
		return fmt.Errorf("server '%s' not found in configuration", serverName)
	}

	// If server is not enabled, enable it first
	if !serverConfig.Enabled {
		r.logger.Info("Server is disabled, enabling it",
			zap.String("server", serverName))
		return r.EnableServer(serverName, true)
	}

	// Get the client to restart
	client, exists := r.upstreamManager.GetClient(serverName)
	if !exists {
		// Server is enabled but client doesn't exist, try to add it
		r.logger.Info("Server client not found, attempting to create and connect",
			zap.String("server", serverName))
		if err := r.upstreamManager.AddServer(serverName, serverConfig); err != nil {
			return fmt.Errorf("failed to add server '%s': %w", serverName, err)
		}
		// Wait to allow the connection attempt to begin
		time.Sleep(2 * time.Second)
		r.logger.Info("Successfully added server", zap.String("server", serverName))
		return nil
	}

	// CRITICAL FIX: Remove and recreate the client to pick up new secrets
	// Simply reconnecting reuses the old client with old (unresolved) secrets
	r.logger.Info("Removing existing client to recreate with fresh secret resolution",
		zap.String("server", serverName))

	// Disconnect and remove the old client
	if err := client.Disconnect(); err != nil {
		r.logger.Warn("Error disconnecting server during restart",
			zap.String("server", serverName),
			zap.Error(err))
	}

	// Remove the client from the manager (this will clean up resources)
	r.upstreamManager.RemoveServer(serverName)

	// Wait a bit for cleanup
	time.Sleep(500 * time.Millisecond)

	// Create a completely new client with fresh secret resolution
	r.logger.Info("Creating new client with fresh secret resolution",
		zap.String("server", serverName))

	if err := r.upstreamManager.AddServer(serverName, serverConfig); err != nil {
		r.logger.Error("Failed to recreate server after restart",
			zap.String("server", serverName),
			zap.Error(err))
		return fmt.Errorf("failed to recreate server '%s': %w", serverName, err)
	}

	// Wait to allow the connection attempt to begin
	// AddServer starts connection asynchronously, so we give it time to initiate
	time.Sleep(2 * time.Second)

	r.logger.Info("Successfully recreated server with fresh secrets",
		zap.String("server", serverName))

	r.logger.Info("Successfully restarted server", zap.String("server", serverName))

	// Trigger tool reindexing asynchronously
	go func() {
		if err := r.DiscoverAndIndexTools(r.AppContext()); err != nil {
			r.logger.Error("Failed to reindex tools after restart", zap.Error(err))
		}
	}()

	r.emitServersChanged("restart", map[string]any{"server": serverName})

	return nil
}

// ForceReconnectAllServers triggers reconnection attempts for all managed servers.
func (r *Runtime) ForceReconnectAllServers(reason string) error {
	if r.upstreamManager == nil {
		return fmt.Errorf("upstream manager not initialized")
	}

	if r.logger != nil {
		r.logger.Info("Force reconnect requested for all upstream servers",
			zap.String("reason", reason))
	}

	result := r.upstreamManager.ForceReconnectAll(reason)

	if r.logger != nil {
		r.logger.Info("Force reconnect completed",
			zap.Int("total_servers", result.TotalServers),
			zap.Int("attempted", result.AttemptedServers),
			zap.Int("successful", len(result.SuccessfulServers)),
			zap.Int("failed", len(result.FailedServers)),
			zap.Int("skipped", len(result.SkippedServers)))
	}

	return nil
}

// HandleUpstreamServerChange should be called when upstream servers change.
func (r *Runtime) HandleUpstreamServerChange(ctx context.Context) {
	if ctx == nil {
		ctx = r.AppContext()
	}

	r.logger.Info("Upstream server configuration changed, triggering comprehensive update")
	go func() {
		if err := r.DiscoverAndIndexTools(ctx); err != nil {
			r.logger.Error("Failed to update tool index after upstream change", zap.Error(err))
		}
		r.cleanupOrphanedIndexEntries()
	}()

	phase := r.CurrentStatus().Phase
	r.UpdatePhase(phase, "Upstream servers updated")
	r.emitServersChanged("upstream_change", map[string]any{"phase": phase})
}

func (r *Runtime) cleanupOrphanedIndexEntries() {
	if r.indexManager == nil || r.upstreamManager == nil {
		return
	}

	r.logger.Debug("Checking for orphaned index entries")

	activeServers := r.upstreamManager.GetAllServerNames()
	activeServerMap := make(map[string]bool)
	for _, serverName := range activeServers {
		activeServerMap[serverName] = true
	}

	// Placeholder for future cleanup strategy; mirrors previous behaviour.
	r.logger.Debug("Orphaned index cleanup completed",
		zap.Int("active_servers", len(activeServers)))
}

// supervisorEventForwarder subscribes to supervisor events and emits runtime events
// to notify Web UI via SSE when server connection state changes.
func (r *Runtime) supervisorEventForwarder() {
	eventCh := r.supervisor.Subscribe()
	defer r.supervisor.Unsubscribe(eventCh)

	r.logger.Info("Supervisor event forwarder started - will emit servers.changed on connection state changes")

	// Get app context once with proper locking
	appCtx := r.AppContext()

	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				r.logger.Info("Supervisor event channel closed, stopping event forwarder")
				return
			}

			// Emit servers.changed event for connection state changes
			// This triggers Web UI to refresh server list via SSE
			switch event.Type {
			case supervisor.EventServerConnected:
				r.logger.Info("Server connected - emitting servers.changed event",
					zap.String("server", event.ServerName))
				r.emitServersChanged("server_connected", map[string]any{
					"server": event.ServerName,
				})

			case supervisor.EventServerDisconnected:
				r.logger.Info("Server disconnected - emitting servers.changed event",
					zap.String("server", event.ServerName))
				r.emitServersChanged("server_disconnected", map[string]any{
					"server": event.ServerName,
				})

			case supervisor.EventServerStateChanged:
				r.logger.Debug("Server state changed - emitting servers.changed event",
					zap.String("server", event.ServerName))
				r.emitServersChanged("server_state_changed", map[string]any{
					"server": event.ServerName,
				})
			}

		case <-appCtx.Done():
			r.logger.Info("App context cancelled, stopping supervisor event forwarder")
			return
		}
	}
}
