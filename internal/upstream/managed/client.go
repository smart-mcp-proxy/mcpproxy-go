package managed

import (
	"context"
	"fmt"
	"sync"
	"time"

	"mcpproxy-go/internal/config"
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

	// Connection state protection
	mu sync.RWMutex

	// ListTools concurrency control
	listToolsMu         sync.Mutex
	listToolsInProgress bool

	// Background monitoring
	stopMonitoring chan struct{}
	monitoringWG   sync.WaitGroup
}

// NewClient creates a new managed client with state management
func NewClient(id string, serverConfig *config.ServerConfig, logger *zap.Logger, logConfig *config.LogConfig, globalConfig *config.Config) (*Client, error) {
	// Create core client
	coreClient, err := core.NewClient(id, serverConfig, logger, logConfig, globalConfig)
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
		mc.StateManager.SetError(err)
		return fmt.Errorf("core client connection failed: %w", err)
	}

	// Transition to ready state
	mc.StateManager.TransitionTo(types.StateReady)

	// Update state manager with server info
	if serverInfo := mc.coreClient.GetServerInfo(); serverInfo != nil {
		mc.StateManager.SetServerInfo(serverInfo.ServerInfo.Name, serverInfo.ServerInfo.Version)
	}

	mc.logger.Info("Successfully established managed connection",
		zap.String("server", mc.Config.Name))

	// Start background monitoring
	mc.startBackgroundMonitoring()

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
	if !mc.IsConnected() {
		return nil, fmt.Errorf("client not connected (state: %s)", mc.StateManager.GetState().String())
	}

	// Prevent concurrent ListTools calls
	mc.listToolsMu.Lock()
	if mc.listToolsInProgress {
		mc.listToolsMu.Unlock()
		mc.logger.Debug("ListTools already in progress, skipping concurrent call",
			zap.String("server", mc.Config.Name))
		return nil, fmt.Errorf("ListTools operation already in progress for server %s", mc.Config.Name)
	}
	mc.listToolsInProgress = true
	mc.listToolsMu.Unlock()

	defer func() {
		mc.listToolsMu.Lock()
		mc.listToolsInProgress = false
		mc.listToolsMu.Unlock()
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
		// Log the error immediately for better debugging
		mc.logger.Error("Tool call failed",
			zap.String("server", mc.Config.Name),
			zap.String("tool", toolName),
			zap.Error(err))

		// Check if it's a connection error and update state
		if mc.isConnectionError(err) {
			mc.logger.Warn("Connection error detected, updating server state",
				zap.String("server", mc.Config.Name),
				zap.Error(err))
			mc.StateManager.SetError(err)
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

	// Handle error states
	if newState == types.StateError && info.LastError != nil {
		mc.logger.Error("Connection error",
			zap.String("server", mc.Config.Name),
			zap.Error(info.LastError),
			zap.Int("retry_count", info.RetryCount))
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
	mc.monitoringWG.Wait()

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
	// Check if client is in error state and should retry connection
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

	// Create a short timeout for health check
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try a simple operation to check health
	_, err := mc.coreClient.ListTools(ctx)
	if err != nil {
		if mc.isConnectionError(err) {
			mc.logger.Warn("Health check failed, marking connection as error",
				zap.String("server", mc.Config.Name),
				zap.Error(err))
			mc.StateManager.SetError(err)
		}
	}
}

// tryReconnect attempts to reconnect the client with proper error handling
func (mc *Client) tryReconnect() {
	// Create a timeout context for the reconnection attempt
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		mc.logger.Error("Reconnection attempt failed",
			zap.String("server", mc.Config.Name),
			zap.Error(err),
			zap.Int("retry_count", mc.StateManager.GetConnectionInfo().RetryCount))
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
	}

	for _, connErr := range connectionErrors {
		if containsString(errStr, connErr) {
			return true
		}
	}

	return false
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
