package runtime

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/runtime/configsvc"
	"mcpproxy-go/internal/runtime/supervisor"
)

const connectAttemptTimeout = 45 * time.Second

// StartBackgroundInitialization kicks off configuration sync and background loops.
func (r *Runtime) StartBackgroundInitialization() {
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

	ticker := time.NewTicker(15 * time.Minute)
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

	if err := r.indexManager.BatchIndexTools(tools); err != nil {
		return fmt.Errorf("failed to index tools: %w", err)
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

	// Index the tools
	if err := r.indexManager.BatchIndexTools(tools); err != nil {
		return fmt.Errorf("failed to index tools for server %s: %w", serverName, err)
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
		hasChanged := !existsInStorage ||
			storedServer.Enabled != serverCfg.Enabled ||
			storedServer.Quarantined != serverCfg.Quarantined ||
			storedServer.URL != serverCfg.URL ||
			storedServer.Command != serverCfg.Command ||
			storedServer.Protocol != serverCfg.Protocol

		if hasChanged {
			changed = true
			r.logger.Info("Server configuration changed, updating storage",
				zap.String("server", serverCfg.Name),
				zap.Bool("new", !existsInStorage),
				zap.Bool("enabled_changed", existsInStorage && storedServer.Enabled != serverCfg.Enabled),
				zap.Bool("quarantined_changed", existsInStorage && storedServer.Quarantined != serverCfg.Quarantined))
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

	// Save configuration and reload asynchronously to reduce blocking
	go func() {
		if err := r.SaveConfiguration(); err != nil {
			r.logger.Error("Failed to save configuration after state change", zap.Error(err))
		}

		if err := r.LoadConfiguredServers(nil); err != nil {
			r.logger.Error("Failed to synchronize runtime after enable toggle", zap.Error(err))
		}
	}()

	r.emitServersChanged("enable_toggle", map[string]any{
		"server":  serverName,
		"enabled": enabled,
	})

	r.HandleUpstreamServerChange(r.AppContext())

	return nil
}

// QuarantineServer updates the quarantine state and persists the change.
func (r *Runtime) QuarantineServer(serverName string, quarantined bool) error {
	r.logger.Info("Request to change server quarantine state",
		zap.String("server", serverName),
		zap.Bool("quarantined", quarantined))

	if err := r.storageManager.QuarantineUpstreamServer(serverName, quarantined); err != nil {
		r.logger.Error("Failed to update server quarantine state in storage", zap.Error(err))
		return fmt.Errorf("failed to update quarantine state for server '%s' in storage: %w", serverName, err)
	}

	// Save configuration and reload asynchronously to reduce blocking
	go func() {
		if err := r.SaveConfiguration(); err != nil {
			r.logger.Error("Failed to save configuration after quarantine state change", zap.Error(err))
		}

		if err := r.LoadConfiguredServers(nil); err != nil {
			r.logger.Error("Failed to synchronize runtime after quarantine toggle", zap.Error(err))
		}
	}()

	r.emitServersChanged("quarantine_toggle", map[string]any{
		"server":      serverName,
		"quarantined": quarantined,
	})

	r.HandleUpstreamServerChange(r.AppContext())

	r.logger.Info("Successfully persisted server quarantine state change",
		zap.String("server", serverName),
		zap.Bool("quarantined", quarantined))

	return nil
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
		return r.upstreamManager.AddServer(serverName, serverConfig)
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

	r.upstreamManager.ForceReconnectAll(reason)
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
