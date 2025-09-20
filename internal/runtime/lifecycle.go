package runtime

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

// StartBackgroundInitialization kicks off configuration sync and background loops.
func (r *Runtime) StartBackgroundInitialization() {
	go r.backgroundInitialization()
}

func (r *Runtime) backgroundInitialization() {
	r.UpdatePhase("Loading", "Loading configuration and connecting to servers...")

	if err := r.LoadConfiguredServers(); err != nil {
		r.logger.Error("Failed to load configured servers", zap.Error(err))
		r.UpdatePhase("Error", fmt.Sprintf("Failed to load servers: %v", err))
		return
	}

	// Only transition to "Connecting" if the server is not yet running
	// If the server is running, keep it as "Running" while upstream connections happen in background
	if !r.IsRunning() {
		r.UpdatePhase("Connecting", "Connecting to upstream servers...")
	}

	appCtx := r.AppContext()
	go r.backgroundConnections(appCtx)
	go r.backgroundToolIndexing(appCtx)

	if !r.IsRunning() {
		r.UpdatePhase("Ready", "Server is ready (connections continue in background)")
	}
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
		if !r.IsRunning() {
			r.UpdatePhase("Connecting", fmt.Sprintf("Connected to %d/%d servers, retrying...", connectedCount, totalCount))
		}

		connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
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

	r.logger.Info("Successfully indexed tools", zap.Int("count", len(tools)))
	return nil
}

// LoadConfiguredServers synchronizes storage and upstream manager from the current config.
//
//nolint:unparam // maintained for parity with previous implementation
func (r *Runtime) LoadConfiguredServers() error {
	cfg := r.Config()
	if cfg == nil {
		return fmt.Errorf("runtime configuration is not available")
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

		if err := r.storageManager.SaveUpstreamServer(serverCfg); err != nil {
			r.logger.Error("Failed to save/update server in storage", zap.Error(err), zap.String("server", serverCfg.Name))
			continue
		}

		if serverCfg.Enabled {
			if err := r.upstreamManager.AddServer(serverCfg.Name, serverCfg); err != nil {
				r.logger.Error("Failed to add/update upstream server", zap.Error(err), zap.String("server", serverCfg.Name))
			}

			if serverCfg.Quarantined {
				r.logger.Info("Server is quarantined but kept connected for security inspection", zap.String("server", serverCfg.Name))
			}
		} else {
			r.upstreamManager.RemoveServer(serverCfg.Name)
			r.logger.Info("Server is disabled, removing from active connections", zap.String("server", serverCfg.Name))
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

	for _, serverName := range serversToRemove {
		r.logger.Info("Removing server no longer in config", zap.String("server", serverName))
		r.upstreamManager.RemoveServer(serverName)
		if err := r.storageManager.DeleteUpstreamServer(serverName); err != nil {
			r.logger.Error("Failed to delete server from storage", zap.Error(err), zap.String("server", serverName))
		}
		if err := r.indexManager.DeleteServerTools(serverName); err != nil {
			r.logger.Error("Failed to delete server tools from index", zap.Error(err), zap.String("server", serverName))
		} else {
			r.logger.Info("Removed server tools from search index", zap.String("server", serverName))
		}
		changed = true
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

	// Get config and path while holding lock briefly
	r.mu.RLock()
	cfgPath := r.cfgPath
	if r.cfg == nil {
		r.mu.RUnlock()
		return fmt.Errorf("runtime configuration is not available")
	}

	if cfgPath == "" {
		r.mu.RUnlock()
		r.logger.Warn("Configuration file path is not available, cannot save configuration")
		return fmt.Errorf("configuration file path is not available")
	}

	// Create a copy of config to avoid holding lock during file I/O
	configCopy := *r.cfg
	r.mu.RUnlock()

	// Update servers and save without holding runtime lock
	configCopy.Servers = latestServers
	return config.SaveConfig(&configCopy, cfgPath)
}

// ReloadConfiguration reloads the configuration from disk and resyncs state.
func (r *Runtime) ReloadConfiguration() error {
	r.logger.Info("Reloading configuration from disk")

	r.mu.RLock()
	dataDir := ""
	oldServerCount := 0
	if r.cfg != nil {
		dataDir = r.cfg.DataDir
		oldServerCount = len(r.cfg.Servers)
	}
	r.mu.RUnlock()

	cfgPath := config.GetConfigPath(dataDir)
	newConfig, err := config.LoadFromFile(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	r.UpdateConfig(newConfig, cfgPath)

	if err := r.LoadConfiguredServers(); err != nil {
		r.logger.Error("loadConfiguredServers failed", zap.Error(err))
		return fmt.Errorf("failed to reload servers: %w", err)
	}

	go r.postConfigReload()

	r.logger.Info("Configuration reload completed",
		zap.String("path", cfgPath),
		zap.Int("old_server_count", oldServerCount),
		zap.Int("new_server_count", len(newConfig.Servers)),
		zap.Int("server_delta", len(newConfig.Servers)-oldServerCount))

	r.emitConfigReloaded(cfgPath)

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

		if err := r.LoadConfiguredServers(); err != nil {
			r.logger.Error("Failed to synchronize runtime after enable toggle", zap.Error(err))
		}
	}()

	r.emitServersChanged("enable_toggle", map[string]any{
		"server":  serverName,
		"enabled": enabled,
	})

	r.HandleUpstreamServerChange(context.TODO())

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

		if err := r.LoadConfiguredServers(); err != nil {
			r.logger.Error("Failed to synchronize runtime after quarantine toggle", zap.Error(err))
		}
	}()

	r.emitServersChanged("quarantine_toggle", map[string]any{
		"server":      serverName,
		"quarantined": quarantined,
	})

	r.HandleUpstreamServerChange(context.TODO())

	r.logger.Info("Successfully persisted server quarantine state change",
		zap.String("server", serverName),
		zap.Bool("quarantined", quarantined))

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
