package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mcpproxy-go/internal/cache"
	"mcpproxy-go/internal/cli/output"
	"mcpproxy-go/internal/cliclient"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/server"
	"mcpproxy-go/internal/socket"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/truncate"
	"mcpproxy-go/internal/upstream"
	"mcpproxy-go/internal/upstream/cli"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

// Call command output format constants (kept for backward compatibility)
// Note: "pretty" is the default for call command (different from global "table" default)
const (
	outputFormatJSON   = "json"
	outputFormatPretty = "pretty"
)

var (
	// Built-in tools that are handled by the MCP proxy server directly
	builtInTools = map[string]bool{
		"upstream_servers":     true,
		"quarantine_security":  true,
		"retrieve_tools":       true,
		"call_tool":            true,
		"call_tool_read":       true,
		"call_tool_write":      true,
		"call_tool_destructive": true,
		"read_cache":           true,
		"list_registries":      true,
		"search_servers":       true,
		"code_execution":       true,
	}
)

var (
	callCmd = &cobra.Command{
		Use:   "call",
		Short: "Call tools on upstream servers",
		Long:  "Commands for calling tools on upstream MCP servers",
	}

	callToolCmd = &cobra.Command{
		Use:   "tool",
		Short: "Call a specific tool on an upstream server or built-in tool (legacy)",
		Long: `[DEPRECATED] Use 'call tool-read', 'call tool-write', or 'call tool-destructive' instead.

Call a tool on an upstream server using the server:tool_name format, or call built-in tools directly.
The upstream server is automatically derived from the tool name prefix for external tools.

Built-in tools: upstream_servers, quarantine_security, retrieve_tools, read_cache, list_registries, search_servers

Examples:
  # Built-in tools (no server prefix)
  mcpproxy call tool --tool-name=upstream_servers --json_args='{"operation":"list"}'
  mcpproxy call tool --tool-name=retrieve_tools --json_args='{"query":"github repositories"}'

  # External server tools (server:tool format)
  mcpproxy call tool --tool-name=github-server:list_repos --json_args='{"owner":"user"}'
  mcpproxy call tool --tool-name=weather-api:get_weather --json_args='{"city":"San Francisco"}'`,
		RunE: runCallTool,
	}

	// Intent-based tool variant commands (Spec 018)
	callToolReadCmd = &cobra.Command{
		Use:   "tool-read",
		Short: "Call a tool with read-only intent",
		Long: `Call a tool on an upstream server with read-only intent declaration.
This command is for operations that only read data without making changes.

The intent is automatically set to operation_type="read".
IDEs can configure this command for auto-approval of read operations.

Examples:
  # Read data from a server
  mcpproxy call tool-read --tool-name=github:list_repos --json_args='{"owner":"user"}'

  # With optional reason
  mcpproxy call tool-read --tool-name=weather:get_forecast --json_args='{"city":"NYC"}' --reason="Checking weather for trip planning"

  # With data sensitivity classification
  mcpproxy call tool-read --tool-name=db:query_users --json_args='{}' --sensitivity=internal`,
		RunE: runCallToolRead,
	}

	callToolWriteCmd = &cobra.Command{
		Use:   "tool-write",
		Short: "Call a tool with write intent",
		Long: `Call a tool on an upstream server with write intent declaration.
This command is for operations that create or modify data.

The intent is automatically set to operation_type="write".
IDEs typically prompt for approval before executing write operations.

Examples:
  # Create a new issue
  mcpproxy call tool-write --tool-name=github:create_issue --json_args='{"title":"Bug report","body":"..."}'

  # Update a record with reason
  mcpproxy call tool-write --tool-name=db:update_user --json_args='{"id":123,"name":"New Name"}' --reason="Correcting user profile"`,
		RunE: runCallToolWrite,
	}

	callToolDestructiveCmd = &cobra.Command{
		Use:   "tool-destructive",
		Short: "Call a tool with destructive intent",
		Long: `Call a tool on an upstream server with destructive intent declaration.
This command is for operations that delete data or have irreversible effects.

The intent is automatically set to operation_type="destructive".
IDEs should always prompt for explicit confirmation before executing.

Examples:
  # Delete a repository
  mcpproxy call tool-destructive --tool-name=github:delete_repo --json_args='{"repo":"old-project"}'

  # Drop a database table with reason
  mcpproxy call tool-destructive --tool-name=db:drop_table --json_args='{"table":"temp_data"}' --reason="Cleanup after migration"`,
		RunE: runCallToolDestructive,
	}

	// Command flags for call tool
	callToolName     string
	callJSONArgs     string
	callLogLevel     string
	callConfigPath   string
	callTimeout      time.Duration
	callOutputFormat string

	// Intent flags for tool variant commands
	callIntentReason      string
	callIntentSensitivity string
)

// GetCallCommand returns the call command for adding to the root command
func GetCallCommand() *cobra.Command {
	return callCmd
}

func init() {
	// Add tool subcommands to call command
	callCmd.AddCommand(callToolCmd)
	callCmd.AddCommand(callToolReadCmd)
	callCmd.AddCommand(callToolWriteCmd)
	callCmd.AddCommand(callToolDestructiveCmd)

	// Define flags for legacy call tool command
	callToolCmd.Flags().StringVarP(&callToolName, "tool-name", "t", "", "Tool name in format server:tool_name (required)")
	callToolCmd.Flags().StringVarP(&callJSONArgs, "json_args", "j", "{}", "JSON arguments for the tool (default: {})")
	callToolCmd.Flags().StringVarP(&callLogLevel, "log-level", "l", "info", "Log level (trace, debug, info, warn, error)")
	callToolCmd.Flags().StringVarP(&callConfigPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")
	callToolCmd.Flags().DurationVar(&callTimeout, "timeout", 30*time.Second, "Tool call timeout")
	callToolCmd.Flags().StringVarP(&callOutputFormat, "output", "o", "pretty", "Output format (pretty, json)")

	// Mark required flags for legacy command
	err := callToolCmd.MarkFlagRequired("tool-name")
	if err != nil {
		panic(fmt.Sprintf("Failed to mark tool-name flag as required: %v", err))
	}

	// Add examples and usage help for legacy command
	callToolCmd.Example = `  # Call built-in tools (no server prefix)
  mcpproxy call tool --tool-name=upstream_servers --json_args='{"operation":"list"}'
  mcpproxy call tool --tool-name=retrieve_tools --json_args='{"query":"github repositories"}'

  # Call external server tools (server:tool format)
  mcpproxy call tool --tool-name=github-server:list_repos --json_args='{"owner":"myorg"}'

  # Use custom config file
  mcpproxy call tool --tool-name=upstream_servers --json_args='{"operation":"list"}' --config=/path/to/config.json`

	// Setup flags for intent-based tool variant commands
	setupToolVariantFlags(callToolReadCmd)
	setupToolVariantFlags(callToolWriteCmd)
	setupToolVariantFlags(callToolDestructiveCmd)
}

// setupToolVariantFlags adds common flags to a tool variant command
func setupToolVariantFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&callToolName, "tool-name", "t", "", "Tool name in format server:tool_name (required)")
	cmd.Flags().StringVarP(&callJSONArgs, "json_args", "j", "{}", "JSON arguments for the tool (default: {})")
	cmd.Flags().StringVarP(&callLogLevel, "log-level", "l", "info", "Log level (trace, debug, info, warn, error)")
	cmd.Flags().StringVarP(&callConfigPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")
	cmd.Flags().DurationVar(&callTimeout, "timeout", 30*time.Second, "Tool call timeout")
	cmd.Flags().StringVarP(&callOutputFormat, "output", "o", "pretty", "Output format (pretty, json)")

	// Intent-specific flags
	cmd.Flags().StringVar(&callIntentReason, "reason", "", "Human-readable explanation for the operation (max 1000 chars)")
	cmd.Flags().StringVar(&callIntentSensitivity, "sensitivity", "", "Data sensitivity classification: public, internal, private, unknown")

	// Mark required flags
	if err := cmd.MarkFlagRequired("tool-name"); err != nil {
		panic(fmt.Sprintf("Failed to mark tool-name flag as required: %v", err))
	}
}

func runCallTool(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	// Parse JSON arguments
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(callJSONArgs), &args); err != nil {
		return fmt.Errorf("invalid JSON arguments: %w", err)
	}

	// Load configuration
	globalConfig, err := loadCallConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Create logger early for daemon detection
	logger, err := createLogger(callLogLevel)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	// Detect daemon and use client mode for ALL tool calls (built-in and external)
	if shouldUseCallDaemon(globalConfig.DataDir) {
		logger.Info("Detected running daemon, using client mode via socket")
		return runCallToolClientMode(globalConfig.DataDir, callToolName, args, logger)
	}

	// Check if this is a built-in tool (no server prefix)
	if builtInTools[callToolName] {
		logger.Info("No daemon detected, using standalone mode for built-in tool")
		return runBuiltInTool(ctx, callToolName, args, globalConfig)
	}

	// Parse tool name to extract server name for external tools
	parts := strings.SplitN(callToolName, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid tool name format: %s (expected server:tool_name or built-in tool name)", callToolName)
	}

	serverName := parts[0]
	toolName := parts[1]

	// Validate server exists in config
	if !serverExistsInConfigForCall(serverName, globalConfig) {
		return fmt.Errorf("server '%s' not found in configuration. Available servers: %v",
			serverName, getAvailableServerNamesForCall(globalConfig))
	}

	fmt.Printf("🚀 MCP Tool Call - Server: %s, Tool: %s\n", serverName, toolName)
	fmt.Printf("📝 Log Level: %s\n", callLogLevel)
	fmt.Printf("⏱️  Timeout: %v\n", callTimeout)
	fmt.Printf("🔧 Arguments: %s\n", callJSONArgs)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// No daemon detected (already checked), use standalone mode
	logger.Info("No daemon detected, using standalone mode for external tool")
	return runCallToolStandalone(ctx, serverName, toolName, args, globalConfig)
}

// loadCallConfig loads the MCP configuration file for call command
func loadCallConfig() (*config.Config, error) {
	var configFilePath string

	if callConfigPath != "" {
		configFilePath = callConfigPath
	} else {
		// Use default path
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}
		configFilePath = filepath.Join(homeDir, ".mcpproxy", "mcp_config.json")
	}

	// Check if config file exists
	if _, err := os.Stat(configFilePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("configuration file not found at %s. Please run 'mcpproxy' daemon first to create the config", configFilePath)
	}

	// Load configuration using file-based loading
	globalConfig, err := config.LoadFromFile(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config from %s: %w", configFilePath, err)
	}

	return globalConfig, nil
}

// outputCallResultAsJSON outputs the result in JSON format using unified formatter
func outputCallResultAsJSON(result interface{}) error {
	formatter := &output.JSONFormatter{Indent: true}
	formatted, err := formatter.Format(result)
	if err != nil {
		return fmt.Errorf("failed to format result as JSON: %w", err)
	}
	fmt.Println(formatted)
	return nil
}

// outputCallResultPretty outputs the result in a human-readable format
func outputCallResultPretty(result interface{}) {
	fmt.Printf("📋 Tool Result:\n")
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	// Handle the result based on its type
	switch v := result.(type) {
	case map[string]interface{}:
		// Pretty print map content
		if content, exists := v["content"]; exists {
			if contentList, ok := content.([]interface{}); ok {
				for i, item := range contentList {
					if itemMap, ok := item.(map[string]interface{}); ok {
						if text, ok := itemMap["text"].(string); ok {
							fmt.Printf("Content %d: %s\n", i+1, text)
						} else {
							fmt.Printf("Content %d: %+v\n", i+1, item)
						}
					} else {
						fmt.Printf("Content %d: %+v\n", i+1, item)
					}
				}
			} else {
				fmt.Printf("Content: %+v\n", content)
			}
		} else {
			// Pretty print the entire map
			for key, value := range v {
				fmt.Printf("%s: %+v\n", key, value)
			}
		}
	default:
		// Fallback to JSON for unknown types
		output, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Printf("Raw result: %+v\n", result)
		} else {
			fmt.Println(string(output))
		}
	}

	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
}

// runBuiltInTool handles calling built-in tools via the MCP proxy server
func runBuiltInTool(ctx context.Context, toolName string, args map[string]interface{}, globalConfig *config.Config) error {
	fmt.Printf("🚀 Built-in Tool Call: %s\n", toolName)
	fmt.Printf("📝 Log Level: %s\n", callLogLevel)
	fmt.Printf("⏱️  Timeout: %v\n", callTimeout)
	fmt.Printf("🔧 Arguments: %s\n", callJSONArgs)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Create logger with appropriate level
	logger, err := createLogger(callLogLevel)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	// Create storage manager
	storageManager, err := storage.NewManager(globalConfig.DataDir, logger.Sugar())
	if err != nil {
		return fmt.Errorf("failed to create storage manager: %w", err)
	}
	defer storageManager.Close()

	// Create index manager
	indexManager, err := index.NewManager(globalConfig.DataDir, logger)
	if err != nil {
		return fmt.Errorf("failed to create index manager: %w", err)
	}
	defer indexManager.Close()

	// Create secret resolver for command execution
	secretResolver := secret.NewResolver()

	// Create upstream manager
	upstreamManager := upstream.NewManager(logger, globalConfig, storageManager.GetBoltDB(), secretResolver, storageManager)

	// Create cache manager
	cacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	if err != nil {
		return fmt.Errorf("failed to create cache manager: %w", err)
	}
	defer cacheManager.Close()

	// Create truncator
	truncator := truncate.NewTruncator(globalConfig.ToolResponseLimit)

	// Create MCP proxy server
	mcpProxy := server.NewMCPProxyServer(
		storageManager,
		indexManager,
		upstreamManager,
		cacheManager,
		truncator,
		logger,
		nil, // mainServer not needed for CLI calls
		false,
		globalConfig,
	)

	fmt.Printf("🛠️  Calling built-in tool '%s'...\n", toolName)

	// Call the built-in tool through the proxy server's public method
	result, err := mcpProxy.CallBuiltInTool(ctx, toolName, args)
	if err != nil {
		return fmt.Errorf("failed to call built-in tool '%s': %w", toolName, err)
	}

	// Output results based on format
	switch callOutputFormat {
	case outputFormatJSON:
		return outputCallResultAsJSON(result)
	case outputFormatPretty, "":
		fallthrough
	default:
		fmt.Printf("✅ Built-in tool call completed successfully!\n\n")
		outputCallResultPretty(result)
	}

	return nil
}

// createLogger creates a zap logger with the specified level
func createLogger(level string) (*zap.Logger, error) {
	var zapLevel zap.AtomicLevel
	switch strings.ToLower(level) {
	case "trace", "debug":
		zapLevel = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		zapLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		zapLevel = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		zapLevel = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		zapLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	config := zap.Config{
		Level:            zapLevel,
		Development:      false,
		Encoding:         "console",
		EncoderConfig:    zap.NewDevelopmentEncoderConfig(),
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}

	return config.Build()
}

// getAvailableServerNamesForCall returns a list of available server names from config
func getAvailableServerNamesForCall(globalConfig *config.Config) []string {
	var names []string
	for _, server := range globalConfig.Servers {
		names = append(names, server.Name)
	}
	return names
}

// serverExistsInConfigForCall checks if a server exists in the configuration
func serverExistsInConfigForCall(serverName string, globalConfig *config.Config) bool {
	for _, server := range globalConfig.Servers {
		if server.Name == serverName {
			return true
		}
	}
	return false
}

// shouldUseCallDaemon checks if daemon is running by detecting socket file.
func shouldUseCallDaemon(dataDir string) bool {
	socketPath := socket.DetectSocketPath(dataDir)
	return socket.IsSocketAvailable(socketPath)
}

// runCallToolClientMode calls tool via daemon HTTP API over socket.
func runCallToolClientMode(dataDir, toolName string, args map[string]interface{}, logger *zap.Logger) error {
	// Detect socket endpoint
	socketPath := socket.DetectSocketPath(dataDir)

	// Create CLI client
	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Ping daemon to verify connectivity
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx); err != nil {
		logger.Warn("Failed to ping daemon, falling back to standalone mode",
			zap.Error(err),
			zap.String("socket_path", socketPath),
			zap.String("data_dir", dataDir),
			zap.String("reason", "daemon_unavailable"))
		// Fall back to standalone mode
		cfg, _ := loadCallConfig()
		parts := strings.SplitN(toolName, ":", 2)
		if len(parts) == 2 {
			standaloneCtx := context.Background()
			return runCallToolStandalone(standaloneCtx, parts[0], parts[1], args, cfg)
		}
		return fmt.Errorf("invalid tool name format: %s", toolName)
	}

	// ADD CLI mode indicator
	fmt.Fprintf(os.Stderr, "ℹ️  Using daemon mode (via socket) - fast execution\n")

	// Call tool via daemon with appropriate timeout
	fmt.Printf("🔗 Calling tool via daemon socket...\n")
	callCtx, callCancel := context.WithTimeout(context.Background(), callTimeout)
	defer callCancel()
	result, err := client.CallTool(callCtx, toolName, args)
	if err != nil {
		return fmt.Errorf("failed to call tool via daemon: %w", err)
	}

	// Output results based on format
	switch callOutputFormat {
	case outputFormatJSON:
		return outputCallResultAsJSON(result)
	case outputFormatPretty, "":
		fallthrough
	default:
		fmt.Printf("✅ Tool call completed successfully!\n\n")
		outputCallResultPretty(result)
	}

	return nil
}

// runCallToolStandalone calls tool directly by connecting to the server (existing logic).
func runCallToolStandalone(ctx context.Context, serverName, toolName string, args map[string]interface{}, globalConfig *config.Config) error {
	// ADD standalone mode indicator
	fmt.Fprintf(os.Stderr, "⚠️  Using standalone mode - daemon not detected (slower startup)\n")

	// Create CLI client
	cliClient, err := cli.NewClient(serverName, globalConfig, callLogLevel)
	if err != nil {
		return fmt.Errorf("failed to create CLI client: %w", err)
	}

	// Connect to server
	fmt.Printf("🔗 Connecting to server '%s'...\n", serverName)
	if err := cliClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to server '%s': %w", serverName, err)
	}

	// Ensure cleanup on exit
	defer func() {
		if disconnectErr := cliClient.Disconnect(); disconnectErr != nil {
			fmt.Printf("⚠️  Warning: Failed to disconnect cleanly: %v\n", disconnectErr)
		}
	}()

	// Call the tool
	fmt.Printf("🛠️  Calling tool '%s' with arguments...\n", toolName)
	result, err := cliClient.CallTool(ctx, toolName, args)
	if err != nil {
		return fmt.Errorf("failed to call tool '%s': %w", toolName, err)
	}

	// Output results based on format
	switch callOutputFormat {
	case outputFormatJSON:
		return outputCallResultAsJSON(result)
	case outputFormatPretty, "":
		fallthrough
	default:
		fmt.Printf("✅ Tool call completed successfully!\n\n")
		outputCallResultPretty(result)
	}

	return nil
}

// runCallToolRead handles the tool-read command (Spec 018)
func runCallToolRead(_ *cobra.Command, _ []string) error {
	return runCallToolVariant("call_tool_read", "read")
}

// runCallToolWrite handles the tool-write command (Spec 018)
func runCallToolWrite(_ *cobra.Command, _ []string) error {
	return runCallToolVariant("call_tool_write", "write")
}

// runCallToolDestructive handles the tool-destructive command (Spec 018)
func runCallToolDestructive(_ *cobra.Command, _ []string) error {
	return runCallToolVariant("call_tool_destructive", "destructive")
}

// runCallToolVariant is the common implementation for intent-based tool calls
func runCallToolVariant(toolVariant, operationType string) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	// Parse JSON arguments
	var toolArgs map[string]interface{}
	if err := json.Unmarshal([]byte(callJSONArgs), &toolArgs); err != nil {
		return fmt.Errorf("invalid JSON arguments: %w", err)
	}

	// Build intent declaration
	intent := map[string]interface{}{
		"operation_type": operationType,
	}
	if callIntentSensitivity != "" {
		intent["data_sensitivity"] = callIntentSensitivity
	}
	if callIntentReason != "" {
		intent["reason"] = callIntentReason
	}

	// Build arguments for the tool variant
	variantArgs := map[string]interface{}{
		"name":   callToolName,
		"args":   toolArgs,
		"intent": intent,
	}

	// Load configuration
	globalConfig, err := loadCallConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Create logger
	logger, err := createLogger(callLogLevel)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	// Display intent information
	fmt.Printf("🚀 Intent-Based Tool Call\n")
	fmt.Printf("   Tool: %s\n", callToolName)
	fmt.Printf("   Variant: %s (operation_type=%s)\n", toolVariant, operationType)
	if callIntentSensitivity != "" {
		fmt.Printf("   Sensitivity: %s\n", callIntentSensitivity)
	}
	if callIntentReason != "" {
		fmt.Printf("   Reason: %s\n", callIntentReason)
	}
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Detect daemon and use client mode if available
	if shouldUseCallDaemon(globalConfig.DataDir) {
		logger.Info("Detected running daemon, using client mode via socket")
		return runCallToolVariantClientMode(globalConfig.DataDir, toolVariant, variantArgs, logger)
	}

	// No daemon - use standalone mode
	logger.Info("No daemon detected, using standalone mode")
	return runCallToolVariantStandalone(ctx, toolVariant, variantArgs, globalConfig)
}

// runCallToolVariantClientMode calls tool variant via daemon HTTP API over socket
func runCallToolVariantClientMode(dataDir, toolVariant string, args map[string]interface{}, logger *zap.Logger) error {
	// Detect socket endpoint
	socketPath := socket.DetectSocketPath(dataDir)

	// Create CLI client
	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Ping daemon to verify connectivity
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx); err != nil {
		logger.Warn("Failed to ping daemon, falling back to standalone mode",
			zap.Error(err),
			zap.String("socket_path", socketPath))
		// Fall back to standalone mode
		cfg, _ := loadCallConfig()
		standaloneCtx := context.Background()
		return runCallToolVariantStandalone(standaloneCtx, toolVariant, args, cfg)
	}

	fmt.Fprintf(os.Stderr, "ℹ️  Using daemon mode (via socket) - fast execution\n")

	// Call tool via daemon
	fmt.Printf("🔗 Calling %s via daemon socket...\n", toolVariant)
	callCtx, callCancel := context.WithTimeout(context.Background(), callTimeout)
	defer callCancel()

	// The daemon's CallTool expects a tool name and arguments
	// For tool variants, we call the variant tool directly
	result, err := client.CallTool(callCtx, toolVariant, args)
	if err != nil {
		return fmt.Errorf("failed to call tool via daemon: %w", err)
	}

	// Output results based on format
	switch callOutputFormat {
	case outputFormatJSON:
		return outputCallResultAsJSON(result)
	case outputFormatPretty, "":
		fallthrough
	default:
		fmt.Printf("✅ Tool call completed successfully!\n\n")
		outputCallResultPretty(result)
	}

	return nil
}

// runCallToolVariantStandalone calls tool variant directly without daemon
func runCallToolVariantStandalone(ctx context.Context, toolVariant string, args map[string]interface{}, globalConfig *config.Config) error {
	fmt.Fprintf(os.Stderr, "⚠️  Using standalone mode - daemon not detected (slower startup)\n")

	// Create logger with appropriate level
	logger, err := createLogger(callLogLevel)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	// Create storage manager
	storageManager, err := storage.NewManager(globalConfig.DataDir, logger.Sugar())
	if err != nil {
		return fmt.Errorf("failed to create storage manager: %w", err)
	}
	defer storageManager.Close()

	// Create index manager
	indexManager, err := index.NewManager(globalConfig.DataDir, logger)
	if err != nil {
		return fmt.Errorf("failed to create index manager: %w", err)
	}
	defer indexManager.Close()

	// Create secret resolver for command execution
	secretResolver := secret.NewResolver()

	// Create upstream manager
	upstreamManager := upstream.NewManager(logger, globalConfig, storageManager.GetBoltDB(), secretResolver, storageManager)

	// Create cache manager
	cacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	if err != nil {
		return fmt.Errorf("failed to create cache manager: %w", err)
	}
	defer cacheManager.Close()

	// Create truncator
	truncator := truncate.NewTruncator(globalConfig.ToolResponseLimit)

	// Create MCP proxy server
	mcpProxy := server.NewMCPProxyServer(
		storageManager,
		indexManager,
		upstreamManager,
		cacheManager,
		truncator,
		logger,
		nil, // mainServer not needed for CLI calls
		false,
		globalConfig,
	)

	fmt.Printf("🛠️  Calling %s...\n", toolVariant)

	// Call the tool variant through the proxy server's public method
	result, err := mcpProxy.CallBuiltInTool(ctx, toolVariant, args)
	if err != nil {
		return fmt.Errorf("failed to call %s: %w", toolVariant, err)
	}

	// Output results based on format
	switch callOutputFormat {
	case outputFormatJSON:
		return outputCallResultAsJSON(result)
	case outputFormatPretty, "":
		fallthrough
	default:
		fmt.Printf("✅ Tool call completed successfully!\n\n")
		outputCallResultPretty(result)
	}

	return nil
}
