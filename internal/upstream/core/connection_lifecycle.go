package core

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// initialize performs MCP initialization handshake
func (c *Client) initialize(ctx context.Context) error {
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "mcpproxy-go",
		Version: "1.0.0",
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	// Log request for trace debugging - use main logger for CLI debug mode
	if reqBytes, err := json.MarshalIndent(initRequest, "", "  "); err == nil {
		c.logger.Debug("🔍 JSON-RPC INITIALIZE REQUEST",
			zap.String("method", "initialize"),
			zap.String("formatted_json", string(reqBytes)))
	}

	serverInfo, err := c.client.Initialize(ctx, initRequest)
	if err != nil {
		// Log initialization failure to server-specific log
		if c.upstreamLogger != nil {
			c.upstreamLogger.Error("MCP initialize JSON-RPC call failed",
				zap.Error(err))
		}

		// CRITICAL FIX: Additional cleanup for direct initialize() calls
		// This handles cases where initialize() is called independently
		if c.isDockerCommand {
			c.logger.Debug("Direct initialization failed for Docker command - cleanup may be handled by caller",
				zap.String("server", c.config.Name),
				zap.String("container_name", c.containerName),
				zap.String("container_id", c.containerID),
				zap.Error(err))
		}

		return fmt.Errorf("MCP initialize failed: %w", err)
	}

	// Log response for trace debugging - use main logger for CLI debug mode
	if respBytes, err := json.MarshalIndent(serverInfo, "", "  "); err == nil {
		c.logger.Debug("🔍 JSON-RPC INITIALIZE RESPONSE",
			zap.String("method", "initialize"),
			zap.String("formatted_json", string(respBytes)))
	}

	c.serverInfo = serverInfo
	c.logger.Info("MCP initialization successful",
		zap.String("server_name", serverInfo.ServerInfo.Name),
		zap.String("server_version", serverInfo.ServerInfo.Version))

	// Log initialization success to server-specific log
	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("MCP initialization completed successfully",
			zap.String("server_name", serverInfo.ServerInfo.Name),
			zap.String("server_version", serverInfo.ServerInfo.Version),
			zap.String("protocol_version", serverInfo.ProtocolVersion))
	}

	return nil
}

// registerNotificationHandler registers a handler for MCP notifications.
// This should be called after client.Start() and initialize() succeed.
// It handles notifications/tools/list_changed to trigger reactive tool discovery.
func (c *Client) registerNotificationHandler() {
	if c.client == nil {
		c.logger.Debug("Skipping notification handler registration - client is nil",
			zap.String("server", c.config.Name))
		return
	}

	c.client.OnNotification(func(notification mcp.JSONRPCNotification) {
		// Filter for tools/list_changed notifications only
		if notification.Method != string(mcp.MethodNotificationToolsListChanged) {
			return
		}

		c.logger.Info("Received tools/list_changed notification from upstream server",
			zap.String("server", c.config.Name))

		// Log capability status for debugging
		if c.serverInfo != nil && c.serverInfo.Capabilities.Tools != nil && c.serverInfo.Capabilities.Tools.ListChanged {
			c.logger.Debug("Server advertised tools.listChanged capability",
				zap.String("server", c.config.Name))
		} else {
			c.logger.Warn("Received tools notification from server that did not advertise listChanged capability",
				zap.String("server", c.config.Name))
		}

		// Invoke the callback if set
		c.mu.RLock()
		callback := c.onToolsChanged
		c.mu.RUnlock()

		if callback != nil {
			callback(c.config.Name)
		} else {
			c.logger.Debug("No onToolsChanged callback set - notification ignored",
				zap.String("server", c.config.Name))
		}
	})

	// Log capability status after registration
	if c.serverInfo != nil && c.serverInfo.Capabilities.Tools != nil && c.serverInfo.Capabilities.Tools.ListChanged {
		c.logger.Debug("Server supports tool change notifications - registered handler",
			zap.String("server", c.config.Name))
	} else {
		c.logger.Debug("Server does not advertise tool change notifications support",
			zap.String("server", c.config.Name))
	}
}

// Disconnect closes the connection
func (c *Client) Disconnect() error {
	return c.DisconnectWithContext(context.Background())
}

// DisconnectWithContext closes the connection with context timeout
func (c *Client) DisconnectWithContext(_ context.Context) error {
	// Step 1: Read state under lock, then release for I/O operations
	c.mu.Lock()
	wasConnected := c.connected
	mcpClient := c.client
	isDocker := c.isDockerCommand
	containerID := c.containerID
	containerName := c.containerName
	pgid := c.processGroupID
	processCmd := c.processCmd
	serverName := c.config.Name
	c.mu.Unlock()

	c.logger.Info("Disconnecting from upstream MCP server",
		zap.Bool("was_connected", wasConnected))

	if c.upstreamLogger != nil {
		c.upstreamLogger.Info("Disconnecting from server",
			zap.Bool("was_connected", wasConnected))
	}

	// Step 2: Stop monitoring (these have their own locks)
	c.StopStderrMonitoring()
	c.StopProcessMonitoring()

	// Step 3: For Docker containers, use Docker-specific cleanup
	if isDocker {
		c.logger.Debug("Disconnecting Docker command, attempting container cleanup",
			zap.String("server", serverName),
			zap.Bool("has_container_id", containerID != ""))

		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), dockerCleanupTimeout)
		defer cleanupCancel()

		if containerID != "" {
			c.logger.Debug("Cleaning up Docker container by ID",
				zap.String("server", serverName),
				zap.String("container_id", containerID))
			c.killDockerContainerWithContext(cleanupCtx)
		} else if containerName != "" {
			c.logger.Debug("Cleaning up Docker container by name",
				zap.String("server", serverName),
				zap.String("container_name", containerName))
			c.killDockerContainerByNameWithContext(cleanupCtx, containerName)
		} else {
			c.logger.Debug("No container ID or name, using pattern-based cleanup",
				zap.String("server", serverName))
			c.killDockerContainerByCommandWithContext(cleanupCtx)
		}
	}

	// Step 4: Try graceful close via MCP client FIRST
	// This gives the subprocess a chance to exit cleanly via stdin/stdout close
	gracefulCloseSucceeded := false
	if mcpClient != nil {
		c.logger.Debug("Attempting graceful MCP client close",
			zap.String("server", serverName))

		closeDone := make(chan struct{})
		go func() {
			mcpClient.Close()
			close(closeDone)
		}()

		select {
		case <-closeDone:
			c.logger.Debug("MCP client closed gracefully",
				zap.String("server", serverName))
			gracefulCloseSucceeded = true
		case <-time.After(mcpClientCloseTimeout):
			c.logger.Warn("MCP client close timed out",
				zap.String("server", serverName),
				zap.Duration("timeout", mcpClientCloseTimeout))
		}
	}

	// Step 5: Force kill process group only if graceful close failed
	// For non-Docker stdio processes that didn't exit gracefully
	if !gracefulCloseSucceeded && !isDocker && pgid > 0 {
		c.logger.Info("Graceful close failed, force killing process group",
			zap.String("server", serverName),
			zap.Int("pgid", pgid))

		if err := killProcessGroup(pgid, c.logger, serverName); err != nil {
			c.logger.Error("Failed to kill process group",
				zap.String("server", serverName),
				zap.Int("pgid", pgid),
				zap.Error(err))
		}

		// Also try direct process kill as last resort
		if processCmd != nil && processCmd.Process != nil {
			if err := processCmd.Process.Kill(); err != nil {
				c.logger.Debug("Direct process kill failed (may already be dead)",
					zap.String("server", serverName),
					zap.Error(err))
			}
		}
	}

	// Step 6: Update state under lock
	c.mu.Lock()
	c.client = nil
	c.serverInfo = nil
	c.connected = false
	c.cachedTools = nil
	c.processGroupID = 0
	c.mu.Unlock()

	c.logger.Debug("Disconnect completed successfully",
		zap.String("server", serverName))
	return nil
}
