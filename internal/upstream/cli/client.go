package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/oauth"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/upstream/core"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

const (
	transportStdio = "stdio"
	oauthRequired  = "required"
)

// Client provides a simple interface for CLI operations with enhanced debugging
type Client struct {
	coreClient *core.Client
	logger     *zap.Logger
	config     *config.ServerConfig
	storage    *storage.BoltDB

	// Debug output settings
	debugMode bool
}

// NewClient creates a new CLI client for debugging and simple operations
func NewClient(serverName string, globalConfig *config.Config, logLevel string) (*Client, error) {
	// Find server config by name
	var serverConfig *config.ServerConfig
	for _, server := range globalConfig.Servers {
		if server.Name == serverName {
			serverConfig = server
			break
		}
	}

	if serverConfig == nil {
		return nil, fmt.Errorf("server '%s' not found in configuration", serverName)
	}

	// Create logger with appropriate level for CLI output
	logConfig := &config.LogConfig{
		Level:         logLevel,
		EnableConsole: true,
		EnableFile:    false,
		JSONFormat:    false, // Use console format for CLI
	}

	logger, err := logs.SetupLogger(logConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	// Create storage for persistent OAuth tokens (CLI should also persist tokens)
	var db *storage.BoltDB
	if globalConfig.DataDir != "" {
		boltDB, err := storage.NewBoltDB(globalConfig.DataDir, logger.Sugar())
		if err != nil {
			logger.Warn("Failed to create storage for CLI OAuth tokens, falling back to in-memory",
				zap.String("data_dir", globalConfig.DataDir),
				zap.Error(err))
		} else {
			db = boltDB
		}
	}

	// Create core client directly for CLI operations (with persistent storage for OAuth tokens)
	coreClient, err := core.NewClientWithOptions(serverName, serverConfig, logger, logConfig, globalConfig, db, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create core client: %w", err)
	}

	return &Client{
		coreClient: coreClient,
		logger:     logger,
		config:     serverConfig,
		storage:    db,
		debugMode:  logLevel == "trace" || logLevel == "debug",
	}, nil
}

// Close cleans up the client and closes storage if it was created
func (c *Client) Close() error {
	if c.storage != nil {
		return c.storage.Close()
	}
	return nil
}

// Connect establishes connection with detailed progress output
func (c *Client) Connect(ctx context.Context) error {
	c.logger.Info("🔗 Starting connection to upstream server",
		zap.String("server", c.config.Name),
		zap.String("transport", c.getTransportType()),
		zap.String("url", c.config.URL),
		zap.String("command", c.config.Command))

	// Add timeout for CLI operations
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Enable JSON-RPC frame logging if trace level is enabled
	if c.debugMode {
		c.logger.Debug("🔍 TRACE MODE ENABLED - JSON-RPC frames will be logged")
	}

	// Connect core client
	if err := c.coreClient.Connect(connectCtx); err != nil {
		c.logger.Error("❌ Connection failed", zap.Error(err))
		return err
	}

	c.logger.Info("✅ Successfully connected to server")

	// Display server information
	c.displayServerInfo()

	return nil
}

// ListTools executes tools/list with detailed output
func (c *Client) ListTools(ctx context.Context) ([]*config.ToolMetadata, error) {
	c.logger.Info("🔍 Discovering tools from server...")

	// Use the caller-provided context (it already carries --timeout from CLI)
	tools, err := c.coreClient.ListTools(ctx)

	if err != nil {
		// Enhanced error reporting with timeout diagnostics
		if strings.Contains(err.Error(), "context deadline exceeded") {
			c.logger.Error("❌ Failed to list tools: TIMEOUT",
				zap.Error(err),
				zap.String("diagnosis", "Container may be starting slowly, unresponsive, or stuck"))

			// Health check disabled to avoid concurrent RPCs
		} else if strings.Contains(err.Error(), "broken pipe") || strings.Contains(err.Error(), "closed pipe") {
			c.logger.Error("❌ Failed to list tools: PIPE BROKEN",
				zap.Error(err),
				zap.String("diagnosis", "Container process likely died or crashed"))
		} else {
			c.logger.Error("❌ Failed to list tools", zap.Error(err))
		}
		return nil, err
	}

	c.logger.Info("✅ Successfully discovered tools",
		zap.Int("tool_count", len(tools)))

	// Display tools in a nice format
	c.displayTools(tools)

	return tools, nil
}

// Disconnect closes the connection
func (c *Client) Disconnect() error {
	c.logger.Info("🔌 Disconnecting from server...")

	if err := c.coreClient.Disconnect(); err != nil {
		c.logger.Error("❌ Disconnect failed", zap.Error(err))
		return err
	}

	c.logger.Info("✅ Successfully disconnected")
	return nil
}

// DisconnectWithContext closes the connection with context timeout
func (c *Client) DisconnectWithContext(ctx context.Context) error {
	c.logger.Info("🔌 Disconnecting from server...")

	if err := c.coreClient.DisconnectWithContext(ctx); err != nil {
		c.logger.Error("❌ Disconnect failed", zap.Error(err))
		return err
	}

	c.logger.Info("✅ Successfully disconnected")
	return nil
}

// displayServerInfo shows detailed server information
func (c *Client) displayServerInfo() {
	// Get server info from core client
	serverInfo := c.coreClient.GetServerInfo()
	if serverInfo == nil {
		return
	}

	c.logger.Info("📋 Server Information",
		zap.String("name", serverInfo.ServerInfo.Name),
		zap.String("version", serverInfo.ServerInfo.Version),
		zap.String("protocol_version", serverInfo.ProtocolVersion))

	if c.debugMode {
		// Display server capabilities
		c.logger.Debug("🔧 Server Capabilities",
			zap.Bool("tools", serverInfo.Capabilities.Tools != nil),
			zap.Bool("resources", serverInfo.Capabilities.Resources != nil),
			zap.Bool("prompts", serverInfo.Capabilities.Prompts != nil),
			zap.Bool("logging", serverInfo.Capabilities.Logging != nil))
	}
}

// displayTools shows the discovered tools in a formatted way
func (c *Client) displayTools(tools []*config.ToolMetadata) {
	if len(tools) == 0 {
		c.logger.Warn("⚠️  No tools discovered from server")
		return
	}

	fmt.Printf("\n📚 Discovered Tools (%d):\n", len(tools))
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	for i, tool := range tools {
		fmt.Printf("%d. %s\n", i+1, tool.Name)
		if tool.Description != "" {
			fmt.Printf("   📝 %s\n", tool.Description)
		}

		if c.debugMode && tool.ParamsJSON != "" {
			fmt.Printf("   🔧 Schema: %s\n", tool.ParamsJSON)
		}

		fmt.Printf("   🏷️  Format: %s:%s\n", tool.ServerName, tool.Name)

		if i < len(tools)-1 {
			fmt.Println()
		}
	}

	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
}

// getTransportType returns the transport type for display
func (c *Client) getTransportType() string {
	if c.config.Protocol != "" && c.config.Protocol != "auto" {
		return c.config.Protocol
	}

	if c.config.Command != "" {
		return transportStdio
	}

	if c.config.URL != "" {
		return "streamable-http"
	}

	return transportStdio
}

// CallTool executes a tool (for future CLI extensions)
func (c *Client) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	c.logger.Info("🛠️  Calling tool",
		zap.String("tool", toolName),
		zap.Any("args", args))

	result, err := c.coreClient.CallTool(ctx, toolName, args)
	if err != nil {
		c.logger.Error("❌ Tool call failed", zap.Error(err))
		return nil, err
	}

	c.logger.Info("✅ Tool call successful")

	if c.debugMode {
		c.logger.Debug("🔍 Tool result", zap.Any("result", result))
	}

	return result, nil
}

// IsConnected returns connection status
func (c *Client) IsConnected() bool {
	return c.coreClient.IsConnected()
}

// GetServerInfo returns server information
func (c *Client) GetServerInfo() *mcp.InitializeResult {
	return c.coreClient.GetServerInfo()
}

// TriggerManualOAuth manually triggers OAuth authentication flow for the server
func (c *Client) TriggerManualOAuth(ctx context.Context) error {
	return c.TriggerManualOAuthWithForce(ctx, false)
}

// TriggerManualOAuthWithForce manually triggers OAuth authentication flow for the server
// If force is true, OAuth flow will be triggered even if initial errors don't seem OAuth-related
func (c *Client) TriggerManualOAuthWithForce(ctx context.Context, force bool) error {
	c.logger.Info("🔐 Starting manual OAuth authentication...", zap.Bool("force", force))

	if force {
		c.logger.Info("🚀 Force mode enabled - skipping connection check and proceeding directly to OAuth")
	} else {
		// First, check if already authenticated by attempting a quick connection
		quickCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		err := c.Connect(quickCtx)
		if err == nil {
			c.logger.Info("✅ Server is already authenticated or OAuth not required")
			return nil
		}

		// Check if this is an OAuth-related error (unless forced)
		if !c.isOAuthRelatedError(err) {
			return fmt.Errorf("server error is not OAuth-related: %w", err)
		}

		c.logger.Info("🎯 OAuth authentication required - triggering manual OAuth flow...")
	}

	// Use the new ForceOAuthFlow method that bypasses rate limiting
	err := c.coreClient.ForceOAuthFlow(ctx)
	if err != nil {
		return fmt.Errorf("manual OAuth authentication failed: %w", err)
	}

	c.logger.Info("✅ Manual OAuth authentication completed successfully")

	// Notify global token manager about OAuth completion to trigger connection retries in other processes
	tokenManager := oauth.GetTokenStoreManager()

	// First try database-based notification (cross-process)
	if c.storage != nil {
		if err := tokenManager.MarkOAuthCompletedWithDB(c.config.Name, c.storage); err != nil {
			c.logger.Warn("Failed to save OAuth completion to database, falling back to in-memory notification",
				zap.String("server", c.config.Name),
				zap.Error(err))
			tokenManager.MarkOAuthCompleted(c.config.Name)
		} else {
			c.logger.Info("📢 OAuth completion saved to database for cross-process notification",
				zap.String("server", c.config.Name))
		}
	} else {
		// Fall back to in-memory notification
		tokenManager.MarkOAuthCompleted(c.config.Name)
		c.logger.Info("📢 OAuth completion recorded in-memory (no database available)",
			zap.String("server", c.config.Name))
	}

	// Skip verification step since OAuth flow already includes connection verification
	// The OAuth flow performs MCP initialization which confirms the connection works
	c.logger.Info("🎉 OAuth authentication complete - server connection verified during OAuth flow!")
	return nil
}

// GetOAuthStatus returns the OAuth authentication status for the server
func (c *Client) GetOAuthStatus() (string, error) {
	// Try to connect and analyze the result
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := c.Connect(ctx)
	if err == nil {
		// Successfully connected
		return "authenticated", nil
	}

	// Check the error type
	if c.isOAuthRelatedError(err) {
		if strings.Contains(err.Error(), "expired") || strings.Contains(err.Error(), "invalid_token") {
			return "expired", nil
		}
		return oauthRequired, nil
	}

	// Check if server supports OAuth at all
	if c.hasOAuthConfig() {
		return oauthRequired, nil
	}

	return "not_required", nil
}

// isOAuthRelatedError checks if an error is OAuth-related
func (c *Client) isOAuthRelatedError(err error) bool {
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
		"authorization required",
	}

	for _, oauthErr := range oauthErrors {
		if strings.Contains(strings.ToLower(errStr), strings.ToLower(oauthErr)) {
			return true
		}
	}

	return false
}

// hasOAuthConfig checks if the server has OAuth configuration
func (c *Client) hasOAuthConfig() bool {
	// Check if server config has OAuth-related fields
	if c.config.Headers != nil {
		for key := range c.config.Headers {
			if strings.Contains(strings.ToLower(key), "auth") {
				return true
			}
		}
	}

	// Check if URL suggests OAuth (common OAuth endpoints)
	if c.config.URL != "" {
		url := strings.ToLower(c.config.URL)
		if strings.Contains(url, "oauth") || strings.Contains(url, "auth") {
			return true
		}
	}

	return false
}
