package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/upstream/core"
	"mcpproxy-go/internal/upstream/types"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

const (
	transportStdio = "stdio"
)

// CLIClient provides a simple interface for CLI operations with enhanced debugging
type CLIClient struct {
	coreClient *core.CoreClient
	logger     *zap.Logger
	config     *config.ServerConfig

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

	// Create core client with CLI debug logging enabled
	coreClient, err := core.NewCoreClientWithOptions(serverName, serverConfig, logger, logConfig, globalConfig, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create core client: %w", err)
	}

	return &CLIClient{
		coreClient: coreClient,
		logger:     logger,
		config:     serverConfig,
		debugMode:  logLevel == "trace" || logLevel == "debug",
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

	// Connect core client
	if err := c.coreClient.Connect(connectCtx); err != nil {
		c.logger.Error("❌ Connection failed", zap.Error(err))
		return err
	}

	c.logger.Info("✅ Successfully connected to server")

	// Start stderr monitoring for stdio processes
	if c.coreClient.GetTransportType() == transportStdio {
		c.startStderrMonitoring()
	}

	// Display server information
	c.displayServerInfo()

	return nil
}

// ListTools executes tools/list with detailed output
func (c *CLIClient) ListTools(ctx context.Context) ([]*config.ToolMetadata, error) {
	c.logger.Info("🔍 Discovering tools from server...")

	// Add timeout for tool listing
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tools, err := c.coreClient.ListTools(listCtx)
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

	// Stop stderr monitoring
	if c.stderrMonitor != nil {
		c.stopStderrMonitoring()
	}

	if err := c.coreClient.Disconnect(); err != nil {
		c.logger.Error("❌ Disconnect failed", zap.Error(err))
		return err
	}

	c.logger.Info("✅ Successfully disconnected")
	return nil
}

// displayServerInfo shows detailed server information
func (c *CLIClient) displayServerInfo() {
	serverInfo := c.coreClient.GetServerInfo()
	if serverInfo == nil {
		return
	}

	c.logger.Info("📋 Server Information",
		zap.String("name", serverInfo.ServerInfo.Name),
		zap.String("version", serverInfo.ServerInfo.Version),
		zap.String("protocol_version", serverInfo.ProtocolVersion))

	if c.debugMode {
		// Display capabilities
		caps := serverInfo.Capabilities
		c.logger.Debug("🔧 Server Capabilities",
			zap.Bool("tools", caps.Tools != nil),
			zap.Bool("resources", caps.Resources != nil),
			zap.Bool("prompts", caps.Prompts != nil),
			zap.Bool("logging", caps.Logging != nil))
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
	stderr := c.coreClient.GetStderr()
	if stderr == nil {
		return
	}

	c.stderrMonitor = &StderrMonitor{
		reader: stderr,
		done:   make(chan struct{}),
		logger: c.logger.With(zap.String("component", "stderr_monitor")),
	}

	go c.stderrMonitor.monitor()
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
func (c *CLIClient) IsConnected() bool {
	return c.coreClient.IsConnected()
}

// GetConnectionInfo returns connection information
func (c *CLIClient) GetConnectionInfo() types.ConnectionInfo {
	return c.coreClient.GetConnectionInfo()
}

// GetServerInfo returns server information
func (c *CLIClient) GetServerInfo() *mcp.InitializeResult {
	return c.coreClient.GetServerInfo()
}
