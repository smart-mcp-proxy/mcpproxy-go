package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/upstream/cli"

	"github.com/spf13/cobra"
)

var (
	callCmd = &cobra.Command{
		Use:   "call",
		Short: "Call tools on upstream servers",
		Long:  "Commands for calling tools on upstream MCP servers",
	}

	callToolCmd = &cobra.Command{
		Use:   "tool",
		Short: "Call a specific tool on an upstream server",
		Long: `Call a tool on an upstream server using the server:tool_name format.
The upstream server is automatically derived from the tool name prefix.

Examples:
  mcpproxy call tool --tool-name=github-server:list_repos --json_args='{"owner":"user"}'
  mcpproxy call tool --tool-name=weather-api:get_weather --json_args='{"city":"San Francisco"}'
  mcpproxy call tool --tool-name=local-script:run_analysis --json_args='{}'`,
		RunE: runCallTool,
	}

	// Command flags for call tool
	callToolName     string
	callJsonArgs     string
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
	callToolCmd.Flags().StringVarP(&callJsonArgs, "json_args", "j", "{}", "JSON arguments for the tool (default: {})")
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
	callToolCmd.Example = `  # Call a GitHub server tool with JSON arguments
  mcpproxy call tool --tool-name=github-server:list_repos --json_args='{"owner":"myorg"}'

  # Call a weather API tool 
  mcpproxy call tool --tool-name=weather-api:get_weather --json_args='{"city":"San Francisco"}'

  # Call with trace logging to see all details
  mcpproxy call tool --tool-name=local-script:run_analysis --json_args='{}' --log-level=trace

  # Use custom config file
  mcpproxy call tool --tool-name=custom-server:my_tool --json_args='{"param":"value"}' --config=/path/to/config.json`
}

func runCallTool(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	// Parse tool name to extract server name
	parts := strings.SplitN(callToolName, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid tool name format: %s (expected server:tool_name)", callToolName)
	}

	serverName := parts[0]
	toolName := parts[1]

	// Parse JSON arguments
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(callJsonArgs), &args); err != nil {
		return fmt.Errorf("invalid JSON arguments: %w", err)
	}

	// Load configuration
	globalConfig, err := loadCallConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Validate server exists in config
	if !serverExistsInConfig(serverName, globalConfig) {
		return fmt.Errorf("server '%s' not found in configuration. Available servers: %v",
			serverName, getAvailableServerNames(globalConfig))
	}

	fmt.Printf("🚀 MCP Tool Call - Server: %s, Tool: %s\n", serverName, toolName)
	fmt.Printf("📝 Log Level: %s\n", callLogLevel)
	fmt.Printf("⏱️  Timeout: %v\n", callTimeout)
	fmt.Printf("🔧 Arguments: %s\n", callJsonArgs)
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
	case "json":
		return outputCallResultAsJSON(result)
	case "pretty":
	default:
		fmt.Printf("✅ Tool call completed successfully!\n\n")
		return outputCallResultPretty(result)
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
func outputCallResultPretty(result interface{}) error {
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
	return nil
}
