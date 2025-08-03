package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/upstream/cli"

	"github.com/spf13/cobra"
)

var (
	toolsCmd = &cobra.Command{
		Use:   "tools",
		Short: "Tools management commands",
		Long:  "Commands for managing and debugging MCP tools from upstream servers",
	}

	toolsListCmd = &cobra.Command{
		Use:   "list",
		Short: "List tools from an upstream server",
		Long: `List all available tools from a specific upstream server.
This command is primarily used for debugging upstream server connections and tool discovery.

Examples:
  mcpproxy tools list --server=github-server --log-level=trace
  mcpproxy tools list --server=weather-api --log-level=debug
  mcpproxy tools list --server=local-script --log-level=info`,
		RunE: runToolsList,
	}

	// Command flags
	serverName    string
	toolsLogLevel string
	configPath    string
	timeout       time.Duration
	outputFormat  string
)

// GetToolsCommand returns the tools command for adding to the root command
func GetToolsCommand() *cobra.Command {
	return toolsCmd
}

func init() {
	// toolsCmd will be added to rootCmd in main.go
	toolsCmd.AddCommand(toolsListCmd)

	// Define flags for tools list command
	toolsListCmd.Flags().StringVarP(&serverName, "server", "s", "", "Name of the upstream server to query (required)")
	toolsListCmd.Flags().StringVarP(&toolsLogLevel, "log-level", "l", "info", "Log level (trace, debug, info, warn, error)")
	toolsListCmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")
	toolsListCmd.Flags().DurationVarP(&timeout, "timeout", "t", 30*time.Second, "Connection timeout")
	toolsListCmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format (table, json, yaml)")

	// Mark required flags
	err := toolsListCmd.MarkFlagRequired("server")
	if err != nil {
		panic(fmt.Sprintf("Failed to mark server flag as required: %v", err))
	}

	// Add examples and usage help
	toolsListCmd.Example = `  # List tools with trace logging to see all JSON-RPC frames
  mcpproxy tools list --server=github-server --log-level=trace

  # List tools with debug output
  mcpproxy tools list --server=weather-api --log-level=debug

  # Use custom config file
  mcpproxy tools list --server=local-script --config=/path/to/config.json

  # Set custom timeout
  mcpproxy tools list --server=slow-server --timeout=60s`
}

func runToolsList(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Load configuration
	globalConfig, err := loadToolsConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Validate server exists in config
	if !serverExistsInConfig(serverName, globalConfig) {
		return fmt.Errorf("server '%s' not found in configuration. Available servers: %v",
			serverName, getAvailableServerNames(globalConfig))
	}

	fmt.Printf("🚀 MCP Tools List - Server: %s\n", serverName)
	fmt.Printf("📝 Log Level: %s\n", toolsLogLevel)
	fmt.Printf("⏱️  Timeout: %v\n", timeout)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Create CLI client
	cliClient, err := cli.NewClient(serverName, globalConfig, toolsLogLevel)
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
			fmt.Fprintf(os.Stderr, "⚠️  Warning: Failed to disconnect cleanly: %v\n", disconnectErr)
		}
	}()

	// List tools
	tools, err := cliClient.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list tools: %w", err)
	}

	// Output results based on format
	switch outputFormat {
	case "json":
		return outputToolsAsJSON(tools)
	case "yaml":
		return outputToolsAsYAML(tools)
	case "table":
	default:
		// Table format is handled by the CLI client's displayTools method
		fmt.Printf("✅ Tool discovery completed successfully!\n")

		if len(tools) == 0 {
			fmt.Printf("⚠️  No tools found on server '%s'\n", serverName)
			fmt.Printf("💡 This could indicate:\n")
			fmt.Printf("   • Server doesn't support tools\n")
			fmt.Printf("   • Server is not properly configured\n")
			fmt.Printf("   • Connection issues during tool discovery\n")
		} else {
			fmt.Printf("🎉 Found %d tool(s) on server '%s'\n", len(tools), serverName)
			fmt.Printf("💡 Use these tools with: mcpproxy call_tool --tool=%s:<tool_name>\n", serverName)
		}
	}

	return nil
}

// loadToolsConfig loads the MCP configuration file for tools command
func loadToolsConfig() (*config.Config, error) {
	var configFilePath string

	if configPath != "" {
		configFilePath = configPath
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

// serverExistsInConfig checks if a server exists in the configuration
func serverExistsInConfig(serverName string, globalConfig *config.Config) bool {
	for _, server := range globalConfig.Servers {
		if server.Name == serverName {
			return true
		}
	}
	return false
}

// getAvailableServerNames returns a list of available server names
func getAvailableServerNames(globalConfig *config.Config) []string {
	var names []string
	for _, server := range globalConfig.Servers {
		names = append(names, server.Name)
	}
	return names
}

// outputToolsAsJSON outputs tools in JSON format
func outputToolsAsJSON(_ []*config.ToolMetadata) error {
	// This would use encoding/json to output tools
	fmt.Printf("📄 JSON output not yet implemented\n")
	return nil
}

// outputToolsAsYAML outputs tools in YAML format
func outputToolsAsYAML(_ []*config.ToolMetadata) error {
	// This would use gopkg.in/yaml.v3 to output tools
	fmt.Printf("📄 YAML output not yet implemented\n")
	return nil
}
