package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/upstream/core"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

const (
	transportStdio = "stdio"
)

// Client provides a simple interface for CLI operations with enhanced debugging
type Client struct {
	coreClient *core.Client
	logger     *zap.Logger
	config     *config.ServerConfig

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

	// Create core client directly for CLI operations (no DB sync manager)
	coreClient, err := core.NewClientWithOptions(serverName, serverConfig, logger, logConfig, globalConfig, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create core client: %w", err)
	}

	return &Client{
		coreClient: coreClient,
		logger:     logger,
		config:     serverConfig,
		debugMode:  logLevel == "trace" || logLevel == "debug",
	}, nil
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
