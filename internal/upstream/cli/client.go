package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/upstream/managed"
	"mcpproxy-go/internal/upstream/types"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

const (
	transportStdio = "stdio"
)

// CLIClient provides a simple interface for CLI operations with enhanced debugging
type CLIClient struct {
	managedClient *managed.ManagedClient
	logger        *zap.Logger
	config        *config.ServerConfig

	// Debug output settings
	debugMode     bool
	stderrMonitor *StderrMonitor
}

// StderrMonitor captures and outputs stderr for stdio processes
type StderrMonitor struct {
	reader io.Reader
	done   chan struct{}
	logger *zap.Logger
}

// NewCLIClient creates a new CLI client for debugging and simple operations
func NewCLIClient(serverName string, globalConfig *config.Config, logLevel string) (*CLIClient, error) {
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

	// Create managed client (which includes Docker-specific optimizations)
	managedClient, err := managed.NewManagedClient(serverName, serverConfig, logger, logConfig, globalConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create managed client: %w", err)
	}

	return &CLIClient{
		managedClient: managedClient,
		logger:        logger,
		config:        serverConfig,
		debugMode:     logLevel == "trace" || logLevel == "debug",
	}, nil
}

// Connect establishes connection with detailed progress output
func (c *CLIClient) Connect(ctx context.Context) error {
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

	// Connect managed client
	if err := c.managedClient.Connect(connectCtx); err != nil {
		c.logger.Error("❌ Connection failed", zap.Error(err))
		return err
	}

	c.logger.Info("✅ Successfully connected to server")

	// Docker containers now handled by stateless connections in core client

	// Note: Stderr monitoring not available with ManagedClient
	// TODO: Add stderr monitoring support if needed for debugging

	// Display server information
	c.displayServerInfo()

	return nil
}

// ListTools executes tools/list with detailed output
func (c *CLIClient) ListTools(ctx context.Context) ([]*config.ToolMetadata, error) {
	c.logger.Info("🔍 Discovering tools from server...")

	// Docker containers now use stateless connections with caching in core client

	// Add timeout for tool listing
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tools, err := c.managedClient.ListTools(listCtx)
	if err != nil {
		c.logger.Error("❌ Failed to list tools", zap.Error(err))
		return nil, err
	}

	c.logger.Info("✅ Successfully discovered tools",
		zap.Int("tool_count", len(tools)))

	// Display tools in a nice format
	c.displayTools(tools)

	return tools, nil
}

// Disconnect closes the connection
func (c *CLIClient) Disconnect() error {
	c.logger.Info("🔌 Disconnecting from server...")

	// Note: Stderr monitoring not used with ManagedClient

	if err := c.managedClient.Disconnect(); err != nil {
		c.logger.Error("❌ Disconnect failed", zap.Error(err))
		return err
	}

	c.logger.Info("✅ Successfully disconnected")
	return nil
}

// displayServerInfo shows detailed server information
func (c *CLIClient) displayServerInfo() {
	// Get server info from managed client's state
	connectionInfo := c.managedClient.StateManager.GetConnectionInfo()
	if connectionInfo.ServerName == "" {
		return
	}

	c.logger.Info("📋 Server Information",
		zap.String("name", connectionInfo.ServerName),
		zap.String("version", connectionInfo.ServerVersion),
		zap.String("protocol_version", "2024-11-05"))

	if c.debugMode {
		// Basic capabilities info (we can't access detailed caps through ManagedClient)
		c.logger.Debug("🔧 Server Capabilities",
			zap.Bool("tools", true),
			zap.Bool("resources", false),
			zap.Bool("prompts", true),
			zap.Bool("logging", false))
	}
}

// displayTools shows the discovered tools in a formatted way
func (c *CLIClient) displayTools(tools []*config.ToolMetadata) {
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

// startStderrMonitoring starts monitoring stderr output for stdio processes
func (c *CLIClient) startStderrMonitoring() {
	// Note: Stderr monitoring not available with ManagedClient
	// ManagedClient wraps CoreClient and doesn't expose stderr directly
	return
}

// stopStderrMonitoring stops stderr monitoring
func (c *CLIClient) stopStderrMonitoring() {
	if c.stderrMonitor != nil {
		close(c.stderrMonitor.done)
		c.stderrMonitor = nil
	}
}

// getTransportType returns the transport type for display
func (c *CLIClient) getTransportType() string {
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

// Monitor stderr output for stdio processes
func (m *StderrMonitor) monitor() {
	m.logger.Info("📡 Starting stderr monitoring...")

	buffer := make([]byte, 4096)
	for {
		select {
		case <-m.done:
			m.logger.Info("🛑 Stderr monitoring stopped")
			return
		default:
			// Non-blocking read attempt
			if m.reader != nil {
				n, err := m.reader.Read(buffer)
				if err != nil {
					if err != io.EOF {
						m.logger.Debug("Stderr read error", zap.Error(err))
					}
					time.Sleep(100 * time.Millisecond)
					continue
				}

				if n > 0 {
					output := string(buffer[:n])
					// Output stderr with clear marking
					fmt.Fprintf(os.Stderr, "🔴 STDERR: %s", output)

					// Also log it
					m.logger.Debug("Stderr output captured", zap.String("content", output))
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// CallTool executes a tool (for future CLI extensions)
func (c *CLIClient) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	c.logger.Info("🛠️  Calling tool",
		zap.String("tool", toolName),
		zap.Any("args", args))

	result, err := c.managedClient.CallTool(ctx, toolName, args)
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
func (c *CLIClient) IsConnected() bool {
	return c.managedClient.IsConnected()
}

// GetConnectionInfo returns connection information
func (c *CLIClient) GetConnectionInfo() types.ConnectionInfo {
	return c.managedClient.StateManager.GetConnectionInfo()
}

// GetServerInfo returns server information
func (c *CLIClient) GetServerInfo() *mcp.InitializeResult {
	// ManagedClient doesn't expose server info directly
	// Return nil for now as this isn't used in CLI operations
	return nil
}
