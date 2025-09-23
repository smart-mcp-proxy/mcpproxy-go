package managed

import (
	"context"
	"fmt"
	"sync"
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/upstream/core"
	"mcpproxy-go/internal/upstream/types"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// Client wraps a core client with state management, concurrency control, and background recovery
type Client struct {
	id           string
	Config       *config.ServerConfig // Public field for compatibility with existing code
	coreClient   *core.Client
	logger       *zap.Logger
	StateManager *types.StateManager // Public field for callback access

	// Configuration for creating fresh connections
	logConfig    *config.LogConfig
	globalConfig *config.Config
	storage      *storage.BoltDB

	// Connection state protection
	mu sync.RWMutex

	// ListTools concurrency control
	listToolsMu         sync.Mutex
	listToolsInProgress bool

	// Background monitoring
	stopMonitoring chan struct{}
	monitoringWG   sync.WaitGroup

	// Reconnection protection
	reconnectMu         sync.Mutex
	reconnectInProgress bool

	// Tool count caching to reduce upstream ListTools calls
	toolCountMu   sync.RWMutex
	toolCount     int
	toolCountTime time.Time
}

// NewClient creates a new managed client with state management
func NewClient(id string, serverConfig *config.ServerConfig, logger *zap.Logger, logConfig *config.LogConfig, globalConfig *config.Config, storage *storage.BoltDB) (*Client, error) {
	// Create core client
	coreClient, err := core.NewClient(id, serverConfig, logger, logConfig, globalConfig, storage)
	if err != nil {
		return nil, fmt.Errorf("failed to create core client: %w", err)
	}

	// Create managed client
	mc := &Client{
		id:             id,
		Config:         serverConfig,
		coreClient:     coreClient,
		logger:         logger.With(zap.String("component", "managed_client")),
		StateManager:   types.NewStateManager(),
		logConfig:      logConfig,
		globalConfig:   globalConfig,
		storage:        storage,
		stopMonitoring: make(chan struct{}),
	}

	// Set up state change callback
	mc.StateManager.SetStateChangeCallback(mc.onStateChange)

	return mc, nil
}

// Connect establishes connection with state management
func (mc *Client) Connect(ctx context.Context) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Check if already connecting or connected
	if mc.StateManager.IsConnecting() || mc.StateManager.IsReady() {
		return fmt.Errorf("connection already in progress or established (state: %s)", mc.StateManager.GetState().String())
	}

	mc.logger.Info("Starting managed connection to upstream server",
		zap.String("server", mc.Config.Name),
		zap.String("current_state", mc.StateManager.GetState().String()))

	// Transition to connecting state
	mc.StateManager.TransitionTo(types.StateConnecting)

	// Connect core client
	if err := mc.coreClient.Connect(ctx); err != nil {
		// Check if this is an OAuth authorization requirement (not an error)
		if mc.isOAuthAuthorizationRequired(err) {
			mc.logger.Info("🎯 OAuth authorization required during MCP initialization",
				zap.String("server", mc.Config.Name))
			// Don't apply backoff for OAuth authorization requirement
			mc.StateManager.SetError(err)
			return fmt.Errorf("OAuth authorization during MCP init failed: %w", err)
		} else if mc.isOAuthError(err) {
			mc.logger.Warn("OAuth authentication failed, applying extended backoff",
				zap.String("server", mc.Config.Name),
				zap.Error(err))
			mc.StateManager.SetOAuthError(err)
		} else {
			mc.StateManager.SetError(err)
		}
		return fmt.Errorf("core client connection failed: %w", err)
	}

	// Transition to ready state only if not already ready
	if mc.StateManager.GetState() != types.StateReady {
		mc.StateManager.TransitionTo(types.StateReady)
	}

	// Update state manager with server info
	if serverInfo := mc.coreClient.GetServerInfo(); serverInfo != nil {
		mc.StateManager.SetServerInfo(serverInfo.ServerInfo.Name, serverInfo.ServerInfo.Version)
	}

	mc.logger.Info("Successfully established managed connection",
		zap.String("server", mc.Config.Name))

	// Add a small delay before starting background monitoring to let connection stabilize
	mc.logger.Debug("🔍 Adding stabilization delay before starting background monitoring",
		zap.String("server", mc.Config.Name))

	go func() {
		time.Sleep(2 * time.Second) // Let connection stabilize
		mc.logger.Debug("🔍 Starting background monitoring after stabilization delay",
			zap.String("server", mc.Config.Name))
		mc.startBackgroundMonitoring()
	}()

	return nil
}

// Disconnect closes the connection and stops monitoring
func (mc *Client) Disconnect() error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.logger.Info("Disconnecting managed client", zap.String("server", mc.Config.Name))

	// Stop background monitoring
	mc.stopBackgroundMonitoring()

	// Disconnect core client
	if err := mc.coreClient.Disconnect(); err != nil {
		mc.logger.Error("Core client disconnect failed", zap.Error(err))
	}

	// Reset state
	mc.StateManager.Reset()

	return nil
}

// IsConnected returns whether the client is ready for operations
func (mc *Client) IsConnected() bool {
	return mc.StateManager.IsReady()
}

// IsConnecting returns whether the client is in a connecting state
func (mc *Client) IsConnecting() bool {
	return mc.StateManager.IsConnecting()
}

// GetState returns the current connection state
func (mc *Client) GetState() types.ConnectionState {
	return mc.StateManager.GetState()
}

// GetConnectionInfo returns detailed connection information
func (mc *Client) GetConnectionInfo() types.ConnectionInfo {
	return mc.StateManager.GetConnectionInfo()
}

// GetServerInfo returns server information
func (mc *Client) GetServerInfo() *mcp.InitializeResult {
	return mc.coreClient.GetServerInfo()
}

// GetLastError returns the last error from the state manager
func (mc *Client) GetLastError() error {
	info := mc.StateManager.GetConnectionInfo()
	return info.LastError
}

// GetConnectionStatus returns detailed connection status information for compatibility
func (mc *Client) GetConnectionStatus() map[string]interface{} {
	info := mc.StateManager.GetConnectionInfo()

	status := map[string]interface{}{
		"state":        info.State.String(),
		"connected":    mc.IsConnected(),
		"connecting":   mc.IsConnecting(),
		"should_retry": mc.ShouldRetry(),
		"retry_count":  info.RetryCount,
		"server_name":  info.ServerName,
	}

	if info.LastError != nil {
		status["last_error"] = info.LastError.Error()
	}

	if !info.LastRetryTime.IsZero() {
		status["last_retry_time"] = info.LastRetryTime
	}

	return status
}

// GetEnvManager returns the environment manager for testing purposes
func (mc *Client) GetEnvManager() interface{} {
	// This is a wrapper method to access the core client's environment manager
	// We use interface{} to avoid exposing internal types
	return mc.coreClient.GetEnvManager()
}

// ShouldRetry returns whether connection should be retried
func (mc *Client) ShouldRetry() bool {
	return mc.StateManager.ShouldRetry()
}

// SetStateChangeCallback sets a callback for state changes
func (mc *Client) SetStateChangeCallback(callback func(oldState, newState types.ConnectionState, info *types.ConnectionInfo)) {
	mc.StateManager.SetStateChangeCallback(callback)
}

// ListTools retrieves tools with concurrency control
func (mc *Client) ListTools(ctx context.Context) ([]*config.ToolMetadata, error) {
	mc.logger.Debug("🔍 ListTools called",
		zap.String("server", mc.Config.Name),
		zap.String("state", mc.StateManager.GetState().String()),
		zap.Bool("connected", mc.IsConnected()))

	if !mc.IsConnected() {
		mc.logger.Debug("🔍 ListTools rejected - client not connected",
			zap.String("server", mc.Config.Name),
			zap.String("state", mc.StateManager.GetState().String()))
		return nil, fmt.Errorf("client not connected (state: %s)", mc.StateManager.GetState().String())
	}

	// Prevent concurrent ListTools calls
	mc.listToolsMu.Lock()
	if mc.listToolsInProgress {
		mc.listToolsMu.Unlock()
		mc.logger.Debug("🔍 ListTools already in progress, rejecting",
			zap.String("server", mc.Config.Name))
		return nil, fmt.Errorf("ListTools operation already in progress for server %s", mc.Config.Name)
	}
	mc.listToolsInProgress = true
	mc.listToolsMu.Unlock()

	defer func() {
		mc.listToolsMu.Lock()
		mc.listToolsInProgress = false
		mc.listToolsMu.Unlock()
		mc.logger.Debug("🔍 ListTools operation completed, flag reset",
			zap.String("server", mc.Config.Name))
	}()

	// Add timeout for tool listing
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tools, err := mc.coreClient.ListTools(listCtx)
	if err != nil {
		// Log the error immediately for better debugging
		mc.logger.Error("ListTools operation failed",
			zap.String("server", mc.Config.Name),
			zap.Error(err))

		// Check if it's a connection error and update state
		if mc.isConnectionError(err) {
			mc.logger.Warn("Connection error detected during ListTools, updating server state",
				zap.String("server", mc.Config.Name),
				zap.Error(err))
			mc.StateManager.SetError(err)
		}
		return nil, fmt.Errorf("ListTools failed: %w", err)
	}

	return tools, nil
}

// CallTool executes a tool with error handling
func (mc *Client) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if !mc.IsConnected() {
		return nil, fmt.Errorf("client not connected (state: %s)", mc.StateManager.GetState().String())
	}

	result, err := mc.coreClient.CallTool(ctx, toolName, args)
	if err != nil {
		// Check if it's a connection error and update state
		if mc.isConnectionError(err) {
			// Use different log levels based on error type
			if mc.isNormalReconnectionError(err) {
				mc.logger.Warn("Tool call failed due to connection loss, will attempt reconnection",
					zap.String("server", mc.Config.Name),
					zap.String("tool", toolName),
					zap.String("error_type", "normal_reconnection"),
					zap.Error(err))
			} else {
				mc.logger.Error("Tool call failed with connection error",
					zap.String("server", mc.Config.Name),
					zap.String("tool", toolName),
					zap.Error(err))
			}
			mc.StateManager.SetError(err)
		} else {
			// Log non-connection errors at error level
			mc.logger.Error("Tool call failed",
				zap.String("server", mc.Config.Name),
				zap.String("tool", toolName),
				zap.Error(err))
		}
		return nil, err
	}

	return result, nil
}

// onStateChange handles state transition events
func (mc *Client) onStateChange(oldState, newState types.ConnectionState, info *types.ConnectionInfo) {
	mc.logger.Info("State transition",
		zap.String("from", oldState.String()),
		zap.String("to", newState.String()),
		zap.String("server", mc.Config.Name))

	// Handle error states with appropriate log levels
	if newState == types.StateError && info.LastError != nil {
		if mc.isNormalReconnectionError(info.LastError) {
			mc.logger.Warn("Connection error, will attempt automatic reconnection",
				zap.String("server", mc.Config.Name),
				zap.String("error_type", "normal_reconnection"),
				zap.Error(info.LastError),
				zap.Int("retry_count", info.RetryCount))
		} else {
			mc.logger.Error("Connection error",
				zap.String("server", mc.Config.Name),
				zap.Error(info.LastError),
				zap.Int("retry_count", info.RetryCount))
		}
	}
}

// startBackgroundMonitoring starts monitoring the connection health
func (mc *Client) startBackgroundMonitoring() {
	mc.monitoringWG.Add(1)
	go func() {
		defer mc.monitoringWG.Done()
		mc.backgroundHealthCheck()
	}()
}

// stopBackgroundMonitoring stops the background monitoring
func (mc *Client) stopBackgroundMonitoring() {
	close(mc.stopMonitoring)

	// Use a timeout for the wait to prevent hanging during shutdown
	done := make(chan struct{})
	go func() {
		mc.monitoringWG.Wait()
		close(done)
	}()

	select {
	case <-done:
		mc.logger.Debug("Background monitoring stopped successfully",
			zap.String("server", mc.Config.Name))
	case <-time.After(1 * time.Second):
		mc.logger.Warn("Background monitoring stop timed out after 1s, forcing shutdown",
			zap.String("server", mc.Config.Name))
	}

	// Recreate the channel for potential reuse
	mc.stopMonitoring = make(chan struct{})
}

// backgroundHealthCheck performs periodic health checks
func (mc *Client) backgroundHealthCheck() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			mc.performHealthCheck()
		case <-mc.stopMonitoring:
			mc.logger.Debug("Background health monitoring stopped",
				zap.String("server", mc.Config.Name))
			return
		}
	}
}

// performHealthCheck checks if the connection is still healthy and attempts reconnection if needed
func (mc *Client) performHealthCheck() {
	// Handle OAuth errors with extended backoff
	if mc.StateManager.GetState() == types.StateError && mc.StateManager.IsOAuthError() {
		if mc.StateManager.ShouldRetryOAuth() {
			info := mc.StateManager.GetConnectionInfo()
			mc.logger.Info("Attempting OAuth reconnection with extended backoff",
				zap.String("server", mc.Config.Name),
				zap.Int("oauth_retry_count", info.OAuthRetryCount),
				zap.Time("last_oauth_attempt", info.LastOAuthAttempt))
			mc.tryReconnect()
		} else {
			info := mc.StateManager.GetConnectionInfo()
			mc.logger.Debug("OAuth backoff period not elapsed, skipping reconnection",
				zap.String("server", mc.Config.Name),
				zap.Int("oauth_retry_count", info.OAuthRetryCount),
				zap.Time("last_oauth_attempt", info.LastOAuthAttempt))
		}
		return
	}

	// Check if client is in error state and should retry connection (non-OAuth errors)
	if mc.StateManager.GetState() == types.StateError && mc.ShouldRetry() {
		mc.logger.Info("Attempting automatic reconnection with exponential backoff",
			zap.String("server", mc.Config.Name),
			zap.Int("retry_count", mc.StateManager.GetConnectionInfo().RetryCount))

		mc.tryReconnect()
		return
	}

	// Skip health checks if not connected
	if !mc.IsConnected() {
		return
	}

	// Skip health checks for Docker servers to avoid interference with container management
	if mc.isDockerServer() {
		mc.logger.Debug("Skipping health check for Docker server",
			zap.String("server", mc.Config.Name),
			zap.String("command", mc.Config.Command))
		return
	}

	// Create a short timeout for health check
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try a simple operation to check health with concurrency protection
	if !mc.tryListTools(ctx) {
		mc.logger.Debug("Health check skipped - ListTools already in progress",
			zap.String("server", mc.Config.Name))
		return
	}

	_, err := mc.coreClient.ListTools(ctx)
	mc.listToolsInProgress = false // Reset the flag

	if err != nil {
		// Only mark as error if it's a real connection issue, not timeout during high activity
		if mc.isConnectionError(err) {
			mc.logger.Warn("Health check failed with connection error, marking as error",
				zap.String("server", mc.Config.Name),
				zap.Error(err))
			mc.StateManager.SetError(err)
		} else {
			mc.logger.Debug("Health check failed with timeout (high activity), ignoring",
				zap.String("server", mc.Config.Name),
				zap.Error(err))
		}
		return
	}

	mc.logger.Debug("Health check passed successfully",
		zap.String("server", mc.Config.Name))
}

// tryReconnect attempts to reconnect the client with proper error handling
func (mc *Client) tryReconnect() {
	// CRITICAL FIX: Prevent concurrent reconnection attempts to avoid duplicate containers
	mc.reconnectMu.Lock()
	if mc.reconnectInProgress {
		mc.reconnectMu.Unlock()
		mc.logger.Debug("Reconnection already in progress, skipping duplicate attempt",
			zap.String("server", mc.Config.Name))
		return
	}
	mc.reconnectInProgress = true
	mc.reconnectMu.Unlock()

	// Ensure we clear the reconnection flag when done
	defer func() {
		mc.reconnectMu.Lock()
		mc.reconnectInProgress = false
		mc.reconnectMu.Unlock()
	}()

	// Create a timeout context for the reconnection attempt - increased for OAuth flows
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	mc.logger.Info("Starting reconnection attempt",
		zap.String("server", mc.Config.Name),
		zap.String("current_state", mc.StateManager.GetState().String()))

	// First, disconnect the current client to clean up any broken connections
	// We don't need to hold the mutex here as Disconnect() already handles it
	if err := mc.coreClient.Disconnect(); err != nil {
		mc.logger.Warn("Failed to disconnect during reconnection attempt",
			zap.String("server", mc.Config.Name),
			zap.Error(err))
	}

	// Reset state to disconnected before attempting reconnection
	mc.StateManager.Reset()

	// Attempt to reconnect using the existing Connect method
	// The Connect method already handles state transitions and error management
	if err := mc.Connect(ctx); err != nil {
		info := mc.StateManager.GetConnectionInfo()

		// Use different log levels based on error type and retry count
		if mc.isOAuthError(err) {
			mc.logger.Warn("OAuth reconnection attempt failed, extended backoff will apply",
				zap.String("server", mc.Config.Name),
				zap.String("error_type", "oauth_authentication"),
				zap.Error(err),
				zap.Int("oauth_retry_count", info.OAuthRetryCount))
		} else if mc.isNormalReconnectionError(err) && info.RetryCount <= 5 {
			mc.logger.Warn("Reconnection attempt failed, will retry with exponential backoff",
				zap.String("server", mc.Config.Name),
				zap.String("error_type", "normal_reconnection"),
				zap.Error(err),
				zap.Int("retry_count", info.RetryCount))
		} else {
			mc.logger.Error("Reconnection attempt failed",
				zap.String("server", mc.Config.Name),
				zap.Error(err),
				zap.Int("retry_count", info.RetryCount))
		}
		// Connect method already sets the error state, so we don't need to do it here
		return
	}

	mc.logger.Info("Reconnection attempt successful",
		zap.String("server", mc.Config.Name),
		zap.String("new_state", mc.StateManager.GetState().String()))
}

// isConnectionError checks if an error indicates a connection problem
func (mc *Client) isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	connectionErrors := []string{
		"connection refused",
		"no such host",
		"connection reset",
		"broken pipe",
		"network is unreachable",
		"timeout",
		"deadline exceeded",
		"context canceled",
		// SSE and HTTP transport specific errors
		"terminated",
		"fetch failed",
		"TypeError",
		"ECONNREFUSED",
		"SSE stream disconnected",
		"stream disconnected",
		"Failed to reconnect SSE stream",
		"Maximum reconnection attempts",
		"connect ECONNREFUSED",
	}

	for _, connErr := range connectionErrors {
		if containsString(errStr, connErr) {
			return true
		}
	}

	return false
}

// isOAuthAuthorizationRequired checks if OAuth authorization is needed (not an error)
func (mc *Client) isOAuthAuthorizationRequired(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	authRequiredErrors := []string{
		"OAuth authorization during MCP init failed",
		"OAuth authorization not implemented",
		"OAuth authorization required",
		"authorization required",
	}

	for _, authErr := range authRequiredErrors {
		if containsString(errStr, authErr) {
			return true
		}
	}

	return false
}

// isOAuthError checks if the error is OAuth-related (actual authentication failure)
func (mc *Client) isOAuthError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	oauthErrors := []string{
		"invalid_token",
		"invalid_grant",
		"access_denied",
		"unauthorized",
		"401", // HTTP 401 Unauthorized
		"Missing or invalid access token",
		"OAuth authentication failed",
		"oauth timeout",
		"oauth error",
	}

	for _, oauthErr := range oauthErrors {
		if containsString(errStr, oauthErr) {
			return true
		}
	}

	return false
}

// isNormalReconnectionError checks if error is part of normal reconnection flow
func (mc *Client) isNormalReconnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	normalReconnectionErrors := []string{
		"SSE stream disconnected",
		"stream disconnected",
		"terminated",
		"fetch failed",
		"Failed to reconnect SSE stream",
		"Maximum reconnection attempts",
		"TypeError: terminated",
	}

	for _, reconnErr := range normalReconnectionErrors {
		if containsString(errStr, reconnErr) {
			return true
		}
	}

	return false
}

// GetCachedToolCount returns the cached tool count or fetches fresh count if cache is expired
// Uses a 2-minute cache TTL to reduce frequent ListTools calls
func (mc *Client) GetCachedToolCount(ctx context.Context) (int, error) {
	const cacheTimeout = 2 * time.Minute

	mc.toolCountMu.RLock()
	cachedCount := mc.toolCount
	cachedTime := mc.toolCountTime
	mc.toolCountMu.RUnlock()

	// Check if cache is valid and not expired
	if !cachedTime.IsZero() && time.Since(cachedTime) < cacheTimeout {
		mc.logger.Debug("🔍 Tool count cache hit",
			zap.String("server", mc.Config.Name),
			zap.Int("cached_count", cachedCount),
			zap.Duration("cache_age", time.Since(cachedTime)))
		return cachedCount, nil
	}

	// Cache miss or expired - need to fetch fresh count
	if !mc.IsConnected() {
		mc.logger.Debug("🔍 Tool count fetch skipped - client not connected",
			zap.String("server", mc.Config.Name),
			zap.String("state", mc.StateManager.GetState().String()))
		return 0, fmt.Errorf("client not connected (state: %s)", mc.StateManager.GetState().String())
	}

	// Prevent concurrent ListTools calls for tool counting
	mc.listToolsMu.Lock()
	if mc.listToolsInProgress {
		mc.listToolsMu.Unlock()
		mc.logger.Debug("🔍 Tool count fetch skipped - ListTools already in progress",
			zap.String("server", mc.Config.Name))
		// Return cached count even if expired rather than causing another concurrent call
		return cachedCount, nil
	}
	mc.listToolsInProgress = true
	mc.listToolsMu.Unlock()

	defer func() {
		mc.listToolsMu.Lock()
		mc.listToolsInProgress = false
		mc.listToolsMu.Unlock()
	}()

	mc.logger.Debug("🔍 Tool count cache miss - fetching fresh count",
		zap.String("server", mc.Config.Name),
		zap.Bool("cache_expired", !cachedTime.IsZero()),
		zap.Duration("cache_age", time.Since(cachedTime)))

	// Fetch fresh tool count with timeout
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tools, err := mc.coreClient.ListTools(listCtx)
	if err != nil {
		mc.logger.Debug("Tool count fetch failed, returning cached value",
			zap.String("server", mc.Config.Name),
			zap.Error(err),
			zap.Int("cached_count", cachedCount))

		// Check if it's a connection error and update state
		if mc.isConnectionError(err) {
			mc.StateManager.SetError(err)
		}

		// Return cached count if available, even if stale
		if !cachedTime.IsZero() {
			return cachedCount, nil
		}
		return 0, fmt.Errorf("tool count fetch failed: %w", err)
	}

	freshCount := len(tools)

	// Update cache
	mc.toolCountMu.Lock()
	mc.toolCount = freshCount
	mc.toolCountTime = time.Now()
	mc.toolCountMu.Unlock()

	mc.logger.Debug("🔍 Tool count cache updated",
		zap.String("server", mc.Config.Name),
		zap.Int("fresh_count", freshCount),
		zap.Int("previous_count", cachedCount))

	return freshCount, nil
}

// InvalidateToolCountCache clears the tool count cache
// Should be called when tools are known to have changed
func (mc *Client) InvalidateToolCountCache() {
	mc.toolCountMu.Lock()
	mc.toolCount = 0
	mc.toolCountTime = time.Time{}
	mc.toolCountMu.Unlock()

	mc.logger.Debug("🔍 Tool count cache invalidated",
		zap.String("server", mc.Config.Name))
}

// tryListTools attempts to acquire the ListTools lock, returns true if successful
func (mc *Client) tryListTools(_ context.Context) bool {
	mc.listToolsMu.Lock()
	defer mc.listToolsMu.Unlock()

	if mc.listToolsInProgress {
		return false
	}

	mc.listToolsInProgress = true
	return true
}

// Helper function to check if string contains substring
func containsString(str, substr string) bool {
	if substr == "" {
		return true
	}
	if len(str) < len(substr) {
		return false
	}

	for i := 0; i <= len(str)-len(substr); i++ {
		if str[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// IsDockerCommand returns whether this client is running a Docker command
func (mc *Client) IsDockerCommand() bool {
	return mc.isDockerServer()
}

// isDockerServer checks if the server is running via Docker
func (mc *Client) isDockerServer() bool {
	return containsString(mc.Config.Command, "docker")
}
