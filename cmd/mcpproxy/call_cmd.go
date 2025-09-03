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
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/server"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/truncate"
	"mcpproxy-go/internal/upstream"
	"mcpproxy-go/internal/upstream/cli"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

const (
	outputFormatJSON   = "json"
	outputFormatPretty = "pretty"
)

var (
	// Built-in tools that are handled by the MCP proxy server directly
	builtInTools = map[string]bool{
		"upstream_servers":    true,
		"quarantine_security": true,
		"retrieve_tools":      true,
		"call_tool":           true,
		"read_cache":          true,
		"list_registries":     true,
		"search_servers":      true,
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
		Short: "Call a specific tool on an upstream server or built-in tool",
		Long: `Call a tool on an upstream server using the server:tool_name format, or call built-in tools directly.
The upstream server is automatically derived from the tool name prefix for external tools.

Built-in tools: upstream_servers, quarantine_security, retrieve_tools, call_tool, read_cache, list_registries, search_servers

Examples:
  # Built-in tools (no server prefix)
  mcpproxy call tool --tool-name=upstream_servers --json_args='{"operation":"list"}'
  mcpproxy call tool --tool-name=retrieve_tools --json_args='{"query":"github repositories"}'
  
  # External server tools (server:tool format)
  mcpproxy call tool --tool-name=github-server:list_repos --json_args='{"owner":"user"}'
  mcpproxy call tool --tool-name=weather-api:get_weather --json_args='{"city":"San Francisco"}'`,
		RunE: runCallTool,
	}

	// Command flags for call tool
	callToolName     string
	callJSONArgs     string
	callLogLevel     string
	callConfigPath   string
	callTimeout      time.Duration
	callOutputFormat string
)

// GetCallCommand returns the call command for adding to the root command
func GetCallCommand() *cobra.Command {
	return callCmd
}

func init() {
	// Add tool subcommand to call command
	callCmd.AddCommand(callToolCmd)

	// Define flags for call tool command
	callToolCmd.Flags().StringVarP(&callToolName, "tool-name", "t", "", "Tool name in format server:tool_name (required)")
	callToolCmd.Flags().StringVarP(&callJSONArgs, "json_args", "j", "{}", "JSON arguments for the tool (default: {})")
	callToolCmd.Flags().StringVarP(&callLogLevel, "log-level", "l", "info", "Log level (trace, debug, info, warn, error)")
	callToolCmd.Flags().StringVarP(&callConfigPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")
	callToolCmd.Flags().DurationVar(&callTimeout, "timeout", 30*time.Second, "Tool call timeout")
	callToolCmd.Flags().StringVarP(&callOutputFormat, "output", "o", "pretty", "Output format (pretty, json)")

	// Mark required flags
	err := callToolCmd.MarkFlagRequired("tool-name")
	if err != nil {
		panic(fmt.Sprintf("Failed to mark tool-name flag as required: %v", err))
	}

	// Add examples and usage help
	callToolCmd.Example = `  # Call built-in tools (no server prefix)
  mcpproxy call tool --tool-name=upstream_servers --json_args='{"operation":"list"}'
  mcpproxy call tool --tool-name=retrieve_tools --json_args='{"query":"github repositories"}'
  mcpproxy call tool --tool-name=upstream_servers --json_args='{"operation":"add","name":"test","command":"uvx","args":["mcp-server-fetch"],"enabled":true}'

  # Call external server tools (server:tool format)
  mcpproxy call tool --tool-name=github-server:list_repos --json_args='{"owner":"myorg"}'
  mcpproxy call tool --tool-name=weather-api:get_weather --json_args='{"city":"San Francisco"}'

  # Call with trace logging to see all details
  mcpproxy call tool --tool-name=local-script:run_analysis --json_args='{}' --log-level=trace

  # Use custom config file
  mcpproxy call tool --tool-name=upstream_servers --json_args='{"operation":"list"}' --config=/path/to/config.json`
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

	// Check if this is a built-in tool (no server prefix)
	if builtInTools[callToolName] {
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
	case outputFormatPretty:
	default:
		fmt.Printf("✅ Tool call completed successfully!\n\n")
		outputCallResultPretty(result)
	}

	return nil
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

// outputCallResultAsJSON outputs the result in JSON format
func outputCallResultAsJSON(result interface{}) error {
	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to format result as JSON: %w", err)
	}
	fmt.Println(string(output))
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

	// Create upstream manager
	upstreamManager := upstream.NewManager(logger, globalConfig, storageManager.GetBoltDB())

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
	case outputFormatPretty:
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
