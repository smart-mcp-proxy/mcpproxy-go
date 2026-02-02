package core

import (
	"context"
	"fmt"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"

	"go.uber.org/zap"
)

const (
	osLinux   = "linux"
	osDarwin  = "darwin"
	osWindows = "windows"

	dockerCleanupTimeout = 30 * time.Second

	// Subprocess shutdown timeouts
	// mcpClientCloseTimeout is the max time to wait for graceful MCP client close
	mcpClientCloseTimeout = 10 * time.Second
	// processGracefulTimeout is the max time to wait after SIGTERM before SIGKILL
	// Must be less than mcpClientCloseTimeout to complete within the close timeout
	processGracefulTimeout = 9 * time.Second
	// processTerminationPollInterval is how often to check if process exited
	processTerminationPollInterval = 100 * time.Millisecond

	// Transport types
	transportHTTP           = "http"
	transportHTTPStreamable = "streamable-http"
	transportSSE            = "sse"
)

// Context key types
type contextKey string

const (
	manualOAuthKey contextKey = "manual_oauth"
)




// ErrOAuthPending represents a deferred OAuth authentication requirement.
// This error indicates that OAuth is required but has been intentionally deferred
// (e.g., for user action via tray UI or CLI) rather than being a connection failure.


// IsOAuthPending checks if an error is an ErrOAuthPending

// Used by Phase 3 (Spec 020) to return auth URL and browser status synchronously.

// parseOAuthError extracts structured error information from OAuth provider responses

// Connect establishes connection to the upstream server
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// CRITICAL FIX: Check for concurrent connection attempts to prevent duplicate containers
	if c.connecting {
		c.logger.Debug("Connection already in progress, rejecting concurrent attempt",
			zap.String("server", c.config.Name))
		return fmt.Errorf("connection already in progress")
	}

	// Allow reconnection if OAuth was recently completed (bypass "already connected" check)
	if c.connected && !c.wasOAuthRecentlyCompleted() {
		c.logger.Debug("Client already connected and OAuth not recent",
			zap.String("server", c.config.Name),
			zap.Bool("connected", c.connected))
		return fmt.Errorf("client already connected")
	}

	// Set connecting flag to prevent concurrent attempts
	c.connecting = true
	defer func() {
		c.connecting = false
	}()

	// Reset connection state for fresh connection attempt
	if c.connected {
		c.logger.Info("🔄 Reconnecting after OAuth completion",
			zap.String("server", c.config.Name))
		c.connected = false
		if c.client != nil {
			c.client.Close()
			c.client = nil
		}
	}

	c.logger.Info("Connecting to upstream MCP server",
		zap.String("server", c.config.Name),
		zap.String("url", c.config.URL),
		zap.String("command", c.config.Command),
		zap.String("protocol", c.config.Protocol))

	// Determine transport type
	c.transportType = transport.DetermineTransportType(c.config)

	// Log to server-specific log file as well
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Starting connection attempt",
			zap.String("transport", c.transportType),
			zap.String("url", c.config.URL),
			zap.String("command", c.config.Command),
			zap.String("protocol", c.config.Protocol))
	}

	// Debug: Show transport type determination
	c.logger.Debug("🔍 Transport Type Determination",
		zap.String("server", c.config.Name),
		zap.String("command", c.config.Command),
		zap.String("url", c.config.URL),
		zap.String("protocol", c.config.Protocol),
		zap.String("determined_transport", c.transportType))

	// Create and connect client based on transport type
	var err error
	switch c.transportType {
	case transportStdio:
		c.logger.Debug("📡 Using STDIO transport")
		err = c.connectStdio(ctx)
	case transportHTTP, transportHTTPStreamable:
		c.logger.Debug("🌐 Using HTTP transport")
		err = c.connectHTTP(ctx)
	case transportSSE:
		c.logger.Debug("📡 Using SSE transport")
		err = c.connectSSE(ctx)
	default:
		return fmt.Errorf("unsupported transport type: %s", c.transportType)
	}

	if err != nil {
		// Log connection failure to server-specific log
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("Connection failed",
				zap.String("transport", c.transportType),
				zap.Error(err))
		}

		// CRITICAL FIX: Cleanup Docker containers when any connection type fails
		// This prevents container accumulation when connections fail after Docker setup
		if c.isDockerCommand {
			c.logger.Warn("Connection failed for Docker command - cleaning up container",
				zap.String("server", c.config.Name),
				zap.String("transport", c.transportType),
				zap.String("container_name", c.containerName),
				zap.String("container_id", c.containerID),
				zap.Error(err))

			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), dockerCleanupTimeout)
			defer cleanupCancel()

			// Try to cleanup using container name first, then ID, then pattern matching
			if c.containerName != "" {
				c.logger.Debug("Attempting container cleanup by name after connection failure")
				if success := c.killDockerContainerByNameWithContext(cleanupCtx, c.containerName); success {
					c.logger.Info("Successfully cleaned up container by name after connection failure")
				}
			} else if c.containerID != "" {
				c.logger.Debug("Attempting container cleanup by ID after connection failure")
				c.killDockerContainerWithContext(cleanupCtx)
			} else {
				c.logger.Debug("Attempting container cleanup by pattern matching after connection failure")
				c.killDockerContainerByCommandWithContext(cleanupCtx)
			}
		}

		// CRITICAL FIX: Also cleanup process groups to prevent zombie processes on connection failure
		if c.processGroupID > 0 {
			c.logger.Warn("Connection failed - cleaning up process group to prevent zombie processes",
				zap.String("server", c.config.Name),
				zap.Int("pgid", c.processGroupID))

			if err := killProcessGroup(c.processGroupID, c.logger, c.config.Name); err != nil {
				c.logger.Error("Failed to clean up process group after connection failure",
					zap.String("server", c.config.Name),
					zap.Int("pgid", c.processGroupID),
					zap.Error(err))
			}
			c.processGroupID = 0
		}

		return fmt.Errorf("failed to connect: %w", err)
	}

	// CRITICAL FIX: Authentication strategies now handle initialize() testing
	// This eliminates the duplicate initialize() call that was causing OAuth strategy
	// to never be reached when no-auth succeeded at Start() but failed at initialize()
	// All authentication strategies (tryNoAuth, tryHeadersAuth, tryOAuthAuth) now test
	// both client.Start() AND c.initialize() to ensure OAuth errors are properly detected

	c.connected = true

	// If we had an OAuth flow in progress and connection succeeded, mark OAuth as complete
	if c.isOAuthInProgress() {
		c.logger.Info("✅ OAuth flow completed successfully - connection established with token",
			zap.String("server", c.config.Name))
		c.markOAuthComplete()
	}

	c.logger.Info("Successfully connected to upstream MCP server",
		zap.String("server", c.config.Name),
		zap.String("transport", c.transportType))

	// Tools caching disabled - will make direct calls to upstream server each time
	c.logger.Debug("Tools caching disabled - will make direct calls to upstream server",
		zap.String("server", c.config.Name),
		zap.String("transport", c.transportType))

	// Log successful connection to server-specific log
	if c.upstreamLogger != nil {
		if c.serverInfo != nil && c.serverInfo.ServerInfo.Name != "" {
			c.upstreamLogger.Info("Successfully connected and initialized",
				zap.String("transport", c.transportType),
				zap.String("server_name", c.serverInfo.ServerInfo.Name),
				zap.String("server_version", c.serverInfo.ServerInfo.Version),
				zap.String("protocol_version", c.serverInfo.ProtocolVersion))
		} else {
			c.upstreamLogger.Info("Successfully connected",
				zap.String("transport", c.transportType),
				zap.String("note", "serverInfo not yet available"))
		}
	}

	return nil
}

// connectStdio establishes stdio transport connection

// handleOAuthAuthorization handles the manual OAuth flow following the mcp-go example pattern.
// extraParams contains auto-detected or manually configured OAuth extra parameters (e.g., RFC 8707 resource).

// handleOAuthAuthorizationWithResult handles the manual OAuth flow and returns the auth URL and browser status.
// This is used by Phase 3 (Spec 020) to return structured information about the OAuth flow start.

// isOAuthInProgress checks if OAuth is in progress

// markOAuthInProgress marks OAuth as in progress

// markOAuthComplete marks OAuth as complete and cleans up callback server

// wasOAuthRecentlyCompleted checks if OAuth was completed recently to prevent retry loops

// ClearOAuthState clears OAuth state (public API for manual OAuth flows)

// ForceOAuthFlow forces an OAuth authentication flow, bypassing rate limiting (for manual auth)

// StartOAuthFlowQuick starts the OAuth flow and returns browser status immediately.
// Unlike ForceOAuthFlowWithResult which blocks until OAuth completes, this function:
// 1. Gets authorization URL synchronously (quick operation)
// 2. Checks HEADLESS environment variable
// 3. Attempts browser open and captures result
// 4. Returns OAuthStartResult immediately
// 5. Continues OAuth callback handling in a goroutine
//
// This is used by the login API endpoint to return accurate browser_opened status
// without blocking the HTTP response for the full OAuth flow.

// getAuthorizationURLQuick gets the authorization URL without starting the full OAuth flow.
// Returns the URL, OAuth handler, code verifier, and state for later use.

// waitForOAuthCallbackAsync waits for OAuth callback and handles token exchange in background.

// ForceOAuthFlowWithResult forces an OAuth authentication flow and returns the auth URL and browser status.
// This is used by Phase 3 (Spec 020) to provide the auth URL to clients even when browser opens successfully.
// forceHTTPOAuthFlowWithResult forces OAuth flow for HTTP transport and returns auth URL/browser status.

// forceSSEOAuthFlowWithResult forces OAuth flow for SSE transport and returns auth URL/browser status.

// isManualOAuthFlow checks if this is a manual OAuth flow

// clearOAuthState clears OAuth state (for cleaning up stale state)
