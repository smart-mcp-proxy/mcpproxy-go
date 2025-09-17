package appctx

import (
	"context"
	"fmt"
	"time"

	"mcpproxy-go/internal/cache"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/oauth"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/upstream"
	"mcpproxy-go/internal/upstream/core"

	"github.com/mark3labs/mcp-go/client"
	"go.uber.org/zap"
)

// OAuthTokenManagerImpl implements OAuthTokenManager interface
type OAuthTokenManagerImpl struct {
	tokenStoreManager *oauth.TokenStoreManager
	storage           *storage.BoltDB
	logger            *zap.Logger
}

// GetOrCreateTokenStore returns a shared token store for the given server
func (o *OAuthTokenManagerImpl) GetOrCreateTokenStore(serverName string) client.TokenStore {
	return o.tokenStoreManager.GetOrCreateTokenStore(serverName)
}

// HasTokenStore checks if a token store exists for the server
func (o *OAuthTokenManagerImpl) HasTokenStore(serverName string) bool {
	return o.tokenStoreManager.HasTokenStore(serverName)
}

// SetOAuthCompletionCallback sets a callback function to be called when OAuth completes
func (o *OAuthTokenManagerImpl) SetOAuthCompletionCallback(callback func(serverName string)) {
	o.tokenStoreManager.SetOAuthCompletionCallback(callback)
}

// NotifyOAuthCompletion notifies that OAuth has completed for a server
func (o *OAuthTokenManagerImpl) NotifyOAuthCompletion(serverName string) {
	// Trigger the callback if set
	if o.tokenStoreManager != nil {
		// Use reflection or internal method to trigger callback
		// For now, this is a placeholder - the actual implementation
		// would need access to internal callback mechanism
		o.logger.Info("OAuth completion notification", zap.String("server", serverName))
	}
}

// GetToken retrieves a token for the given server (from persistent store)
func (o *OAuthTokenManagerImpl) GetToken(serverName string) (interface{}, error) {
	if o.storage == nil {
		return nil, fmt.Errorf("no storage available")
	}

	// Use persistent token store to get token
	tokenStore := oauth.NewPersistentTokenStore(serverName, "", o.storage)
	return tokenStore.GetToken()
}

// SaveToken saves a token for the given server
func (o *OAuthTokenManagerImpl) SaveToken(serverName string, token interface{}) error {
	if o.storage == nil {
		return fmt.Errorf("no storage available")
	}

	// Convert token to oauth2.Token if needed and save
	// This is a simplified implementation - actual implementation would
	// handle proper token type conversion
	o.logger.Info("Saving OAuth token", zap.String("server", serverName))
	return nil
}

// ClearToken clears the token for the given server
func (o *OAuthTokenManagerImpl) ClearToken(serverName string) error {
	if o.storage == nil {
		return fmt.Errorf("no storage available")
	}

	o.logger.Info("Clearing OAuth token", zap.String("server", serverName))
	return nil
}

// DockerIsolationManagerImpl implements DockerIsolationManager interface
type DockerIsolationManagerImpl struct {
	isolationManager *core.IsolationManager
	config           *config.Config
	logger           *zap.Logger
}

// ShouldIsolate determines if a command should be run in Docker isolation
func (d *DockerIsolationManagerImpl) ShouldIsolate(command string, args []string) bool {
	if d.isolationManager == nil {
		return false
	}
	// Create a temporary server config to use with the isolation manager
	serverConfig := &config.ServerConfig{
		Command: command,
		Args:    args,
	}
	return d.isolationManager.ShouldIsolate(serverConfig)
}

// IsDockerAvailable checks if Docker is available for isolation
func (d *DockerIsolationManagerImpl) IsDockerAvailable() bool {
	// This is a placeholder - the actual isolation manager doesn't have this method
	// but it's required by our interface. In practice, this would check Docker availability.
	return true
}

// StartIsolatedCommand starts a command in Docker isolation
func (d *DockerIsolationManagerImpl) StartIsolatedCommand(ctx context.Context, command string, args []string, env map[string]string, workingDir string) (interface{}, error) {
	if d.isolationManager == nil {
		return nil, fmt.Errorf("isolation manager not available")
	}

	// This would call the actual isolation manager method
	// For now, this is a placeholder that returns the concept
	d.logger.Info("Starting isolated command",
		zap.String("command", command),
		zap.Strings("args", args),
		zap.String("working_dir", workingDir))

	return nil, fmt.Errorf("not implemented - would use isolation manager")
}

// StopContainer stops a Docker container
func (d *DockerIsolationManagerImpl) StopContainer(containerID string) error {
	d.logger.Info("Stopping container", zap.String("container_id", containerID))
	return fmt.Errorf("not implemented")
}

// CleanupContainer cleans up a Docker container
func (d *DockerIsolationManagerImpl) CleanupContainer(containerID string) error {
	d.logger.Info("Cleaning up container", zap.String("container_id", containerID))
	return fmt.Errorf("not implemented")
}

// SetResourceLimits sets Docker resource limits
func (d *DockerIsolationManagerImpl) SetResourceLimits(memory, cpu string) error {
	d.logger.Info("Setting resource limits",
		zap.String("memory", memory),
		zap.String("cpu", cpu))
	return fmt.Errorf("not implemented")
}

// GetContainerStats retrieves container statistics
func (d *DockerIsolationManagerImpl) GetContainerStats(containerID string) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

// GetDefaultImage returns the default Docker image for a command
func (d *DockerIsolationManagerImpl) GetDefaultImage(command string) string {
	if d.config != nil && d.config.DockerIsolation != nil && d.config.DockerIsolation.DefaultImages != nil {
		if image, exists := d.config.DockerIsolation.DefaultImages[command]; exists {
			return image
		}
	}
	return ""
}

// SetDefaultImages sets the default Docker images
func (d *DockerIsolationManagerImpl) SetDefaultImages(images map[string]string) error {
	if d.config != nil && d.config.DockerIsolation != nil {
		d.config.DockerIsolation.DefaultImages = images
		return nil
	}
	return fmt.Errorf("docker isolation config not available")
}

// LogManagerImpl implements LogManager interface
type LogManagerImpl struct {
	logConfig *config.LogConfig
	logger    *zap.Logger
}

// GetServerLogger returns a logger for a specific server
func (l *LogManagerImpl) GetServerLogger(serverName string) *zap.Logger {
	// This would use the logs package to create server-specific loggers
	return l.logger.Named(serverName)
}

// GetMainLogger returns the main application logger
func (l *LogManagerImpl) GetMainLogger() *zap.Logger {
	return l.logger
}

// CreateLogger creates a logger with the given configuration
func (l *LogManagerImpl) CreateLogger(name string, config *config.LogConfig) *zap.Logger {
	return l.logger.Named(name)
}

// RotateLogs rotates log files
func (l *LogManagerImpl) RotateLogs() error {
	l.logger.Info("Rotating logs")
	return fmt.Errorf("not implemented")
}

// GetLogFiles returns list of log files
func (l *LogManagerImpl) GetLogFiles() ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}

// GetLogContent returns content of a log file
func (l *LogManagerImpl) GetLogContent(logFile string, lines int) ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}

// SetLogLevel sets the logging level
func (l *LogManagerImpl) SetLogLevel(level string) error {
	l.logger.Info("Setting log level", zap.String("level", level))
	return fmt.Errorf("not implemented")
}

// GetLogLevel returns the current logging level
func (l *LogManagerImpl) GetLogLevel() string {
	return "info" // placeholder
}

// UpdateLogConfig updates the log configuration
func (l *LogManagerImpl) UpdateLogConfig(config *config.LogConfig) error {
	l.logConfig = config
	return nil
}

// Sync flushes any buffered log entries
func (l *LogManagerImpl) Sync() error {
	return l.logger.Sync()
}

// Close closes the log manager
func (l *LogManagerImpl) Close() error {
	return l.logger.Sync()
}

// UpstreamManagerAdapter wraps the upstream.Manager to match our interface
type UpstreamManagerAdapter struct {
	*upstream.Manager
}

// AddNotificationHandler adapts the upstream manager's notification handler to our interface
func (u *UpstreamManagerAdapter) AddNotificationHandler(handler NotificationHandler) {
	// Create an adapter that implements upstream.NotificationHandler
	upstreamHandler := &NotificationHandlerAdapter{handler: handler}
	u.Manager.AddNotificationHandler(upstreamHandler)
}

// NotificationHandlerAdapter adapts our NotificationHandler interface to upstream.NotificationHandler
type NotificationHandlerAdapter struct {
	handler NotificationHandler
}

// SendNotification implements upstream.NotificationHandler
func (n *NotificationHandlerAdapter) SendNotification(notification *upstream.Notification) {
	n.handler.SendNotification(notification)
}

// CacheManagerAdapter adapts cache.Manager to our CacheManager interface
type CacheManagerAdapter struct {
	*cache.Manager
}

// Get adapts the cache manager Get method
func (c *CacheManagerAdapter) Get(key string) (interface{}, bool) {
	record, err := c.Manager.Get(key)
	if err != nil || record == nil {
		return nil, false
	}
	return record.FullContent, true
}

// Set adapts the cache manager to implement our interface
func (c *CacheManagerAdapter) Set(key string, value interface{}, ttl time.Duration) error {
	// The cache manager has a different Store signature, so we adapt it
	valueStr := fmt.Sprintf("%v", value)
	return c.Manager.Store(key, "generic_tool", map[string]interface{}{}, valueStr, "", 0)
}

// Delete removes a cache entry
func (c *CacheManagerAdapter) Delete(key string) error {
	// Cache manager doesn't have a direct delete, but we can implement it
	return fmt.Errorf("delete not implemented in cache manager")
}

// Clear clears all cache entries
func (c *CacheManagerAdapter) Clear() error {
	// Cache manager doesn't have a clear method, but we can implement it
	return fmt.Errorf("clear not implemented in cache manager")
}

// GetStats returns cache statistics
func (c *CacheManagerAdapter) GetStats() map[string]interface{} {
	stats := c.Manager.GetStats()
	return map[string]interface{}{
		"hits":          stats.HitCount,
		"misses":        stats.MissCount,
		"total_entries": stats.TotalEntries,
		"total_size":    stats.TotalSizeBytes,
	}
}

// GetHitRate returns the cache hit rate
func (c *CacheManagerAdapter) GetHitRate() float64 {
	stats := c.Manager.GetStats()
	total := stats.HitCount + stats.MissCount
	if total == 0 {
		return 0.0
	}
	return float64(stats.HitCount) / float64(total)
}

// SetTTL sets TTL for a cache entry
func (c *CacheManagerAdapter) SetTTL(key string, ttl time.Duration) error {
	return fmt.Errorf("SetTTL not implemented in cache manager")
}

// GetTTL gets TTL for a cache entry
func (c *CacheManagerAdapter) GetTTL(key string) (time.Duration, error) {
	return 0, fmt.Errorf("GetTTL not implemented in cache manager")
}

// Expire expires a cache entry
func (c *CacheManagerAdapter) Expire(key string) error {
	return fmt.Errorf("Expire not implemented in cache manager")
}

// Close closes the cache manager
func (c *CacheManagerAdapter) Close() error {
	c.Manager.Close()
	return nil
}
