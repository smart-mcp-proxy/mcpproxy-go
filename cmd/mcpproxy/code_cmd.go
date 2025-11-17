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
	"mcpproxy-go/internal/cliclient"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/server"
	"mcpproxy-go/internal/socket"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/truncate"
	"mcpproxy-go/internal/upstream"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	codeCmd = &cobra.Command{
		Use:   "code",
		Short: "JavaScript code execution for multi-tool orchestration",
		Long:  "Execute JavaScript code that orchestrates multiple upstream MCP tools in a single request",
	}

	codeExecCmd = &cobra.Command{
		Use:   "exec",
		Short: "Execute JavaScript code",
		Long: `Execute JavaScript code that can orchestrate multiple upstream MCP tools.

The JavaScript code has access to:
- input: Global variable containing the input data (from --input or --input-file)
- call_tool(serverName, toolName, args): Function to invoke upstream MCP tools

The code must return a JSON-serializable value. The sandbox prevents access to:
- require() - No module loading
- setTimeout/setInterval - No timers
- Filesystem, network, or environment variables

Exit codes:
  0 - Successful execution
  1 - Execution failed (syntax error, runtime error, timeout, etc.)
  2 - Invalid arguments or configuration`,
		RunE: runCodeExec,
	}

	// Command flags for code exec
	codeSource       string
	codeFile         string
	codeInput        string
	codeInputFile    string
	codeTimeout      int
	codeMaxToolCalls int
	codeAllowedSrvs  []string
	codeLogLevel     string
	codeConfigPath   string
)

// GetCodeCommand returns the code command for adding to the root command
func GetCodeCommand() *cobra.Command {
	return codeCmd
}

func init() {
	// Add exec subcommand to code command
	codeCmd.AddCommand(codeExecCmd)

	// Define flags for code exec command
	codeExecCmd.Flags().StringVar(&codeSource, "code", "", "JavaScript code to execute (required if --file is not provided)")
	codeExecCmd.Flags().StringVar(&codeFile, "file", "", "Path to JavaScript file to execute (required if --code is not provided)")
	codeExecCmd.Flags().StringVar(&codeInput, "input", "{}", "Input data as JSON string (default: {})")
	codeExecCmd.Flags().StringVar(&codeInputFile, "input-file", "", "Path to JSON file containing input data")
	codeExecCmd.Flags().IntVar(&codeTimeout, "timeout", 120000, "Execution timeout in milliseconds (1-600000)")
	codeExecCmd.Flags().IntVar(&codeMaxToolCalls, "max-tool-calls", 0, "Maximum number of tool calls (0 = unlimited)")
	codeExecCmd.Flags().StringSliceVar(&codeAllowedSrvs, "allowed-servers", []string{}, "Comma-separated list of allowed server names (empty = all allowed)")
	codeExecCmd.Flags().StringVarP(&codeLogLevel, "log-level", "l", "info", "Log level (trace, debug, info, warn, error)")
	codeExecCmd.Flags().StringVarP(&codeConfigPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")

	// Add examples
	codeExecCmd.Example = `  # Execute inline code with input
  mcpproxy code exec --code="({ result: input.value * 2 })" --input='{"value": 21}'

  # Execute code from file
  mcpproxy code exec --file=script.js --input-file=params.json

  # Call upstream tools
  mcpproxy code exec --code="call_tool('github', 'get_user', {username: input.user})" --input='{"user":"octocat"}'

  # With timeout and tool call limits
  mcpproxy code exec --code="..." --timeout=60000 --max-tool-calls=10

  # Restrict to specific servers
  mcpproxy code exec --code="..." --allowed-servers=github,gitlab

  # With trace logging for debugging
  mcpproxy code exec --code="..." --log-level=trace`
}

func runCodeExec(_ *cobra.Command, _ []string) error {
	// Validate arguments
	if codeSource == "" && codeFile == "" {
		fmt.Fprintf(os.Stderr, "Error: either --code or --file must be provided\n")
		return exitError(2)
	}
	if codeSource != "" && codeFile != "" {
		fmt.Fprintf(os.Stderr, "Error: --code and --file are mutually exclusive\n")
		return exitError(2)
	}

	// Load code and input
	code, inputData, err := loadCodeAndInput()
	if err != nil {
		return exitError(2)
	}

	// Validate options
	if err := validateOptions(); err != nil {
		return exitError(2)
	}

	// Load config to get data directory
	globalConfig, err := loadCodeConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return exitError(2)
	}

	// Check if code_execution is enabled
	if !globalConfig.EnableCodeExecution {
		fmt.Fprintf(os.Stderr, "Error: code_execution is disabled in configuration. Set 'enable_code_execution: true' in config file.\n")
		return exitError(2)
	}

	// Create logger
	logger, err := createCodeLogger(codeLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return exitError(2)
	}

	// Detect daemon and choose mode
	if shouldUseDaemon(globalConfig.DataDir) {
		logger.Info("Detected running daemon, using client mode via socket")
		return runCodeExecClientMode(globalConfig.DataDir, code, inputData, logger)
	}

	logger.Info("No daemon detected, using standalone mode")
	return runCodeExecStandalone(globalConfig, code, inputData, logger)
}

// shouldUseDaemon checks if daemon is running by detecting socket file.
func shouldUseDaemon(dataDir string) bool {
	socketPath := socket.DetectSocketPath(dataDir)
	return socket.IsSocketAvailable(socketPath)
}

// runCodeExecClientMode executes code via daemon HTTP API over socket.
func runCodeExecClientMode(dataDir, code string, input map[string]interface{}, logger *zap.Logger) error {
	// Detect socket endpoint
	socketPath := socket.DetectSocketPath(dataDir)

	// Create CLI client
	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Ping daemon to verify connectivity
	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		logger.Warn("Failed to ping daemon, falling back to standalone mode", zap.Error(err))
		// Fall back to standalone mode
		cfg, _ := loadCodeConfig()
		return runCodeExecStandalone(cfg, code, input, logger)
	}

	// Execute code via daemon
	result, err := client.CodeExec(
		ctx,
		code,
		input,
		codeTimeout,
		codeMaxToolCalls,
		codeAllowedSrvs,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error calling daemon: %v\n", err)
		return exitError(1)
	}

	// Output result
	return outputResult(result)
}

// runCodeExecStandalone executes code locally (existing logic).
func runCodeExecStandalone(globalConfig *config.Config, code string, input map[string]interface{}, logger *zap.Logger) error {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(codeTimeout+5000)*time.Millisecond)
	defer cancel()

	// Create storage manager
	storageManager, err := storage.NewManager(globalConfig.DataDir, logger.Sugar())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating storage manager: %v\n", err)
		return exitError(2)
	}
	defer storageManager.Close()

	// Create index manager
	indexManager, err := index.NewManager(globalConfig.DataDir, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating index manager: %v\n", err)
		return exitError(2)
	}
	defer indexManager.Close()

	// Create secret resolver
	secretResolver := secret.NewResolver()

	// Create upstream manager
	upstreamManager := upstream.NewManager(logger, globalConfig, storageManager.GetBoltDB(), secretResolver, storageManager)

	// Create cache manager
	cacheManager, err := cache.NewManager(storageManager.GetDB(), logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating cache manager: %v\n", err)
		return exitError(2)
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
		nil,
		false,
		globalConfig,
	)
	defer mcpProxy.Close()

	// Build arguments for code_execution tool
	args := map[string]interface{}{
		"code":  code,
		"input": input,
		"options": map[string]interface{}{
			"timeout_ms":      codeTimeout,
			"max_tool_calls":  codeMaxToolCalls,
			"allowed_servers": codeAllowedSrvs,
		},
	}

	// Call the code_execution tool
	result, err := mcpProxy.CallBuiltInTool(ctx, "code_execution", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error calling code_execution tool: %v\n", err)
		return exitError(1)
	}

	// Parse and output result
	return outputResultFromMCP(result)
}

// Helper functions

func loadCodeAndInput() (string, map[string]interface{}, error) {
	var code string
	if codeFile != "" {
		codeBytes, err := os.ReadFile(codeFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading code file: %v\n", err)
			return "", nil, err
		}
		code = string(codeBytes)
	} else {
		code = codeSource
	}

	var inputData map[string]interface{}
	if codeInputFile != "" {
		inputBytes, err := os.ReadFile(codeInputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input file: %v\n", err)
			return "", nil, err
		}
		if err := json.Unmarshal(inputBytes, &inputData); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing input file JSON: %v\n", err)
			return "", nil, err
		}
	} else {
		if err := json.Unmarshal([]byte(codeInput), &inputData); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing input JSON: %v\n", err)
			return "", nil, err
		}
	}

	return code, inputData, nil
}

func validateOptions() error {
	if codeTimeout < 1 || codeTimeout > 600000 {
		fmt.Fprintf(os.Stderr, "Error: timeout must be between 1 and 600000 milliseconds\n")
		return fmt.Errorf("invalid timeout")
	}
	if codeMaxToolCalls < 0 {
		fmt.Fprintf(os.Stderr, "Error: max-tool-calls cannot be negative\n")
		return fmt.Errorf("invalid max-tool-calls")
	}
	return nil
}

func outputResult(result *cliclient.CodeExecResult) error {
	output, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting result: %v\n", err)
		return exitError(1)
	}
	fmt.Println(string(output))

	if !result.OK {
		return exitError(1)
	}
	return nil
}

func outputResultFromMCP(result *mcp.CallToolResult) error {
	// Existing logic to parse MCP result
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			var execResult map[string]interface{}
			if err := json.Unmarshal([]byte(textContent.Text), &execResult); err == nil {
				output, err := json.MarshalIndent(execResult, "", "  ")
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error formatting result: %v\n", err)
					return exitError(1)
				}
				fmt.Println(string(output))

				if okValue, exists := execResult["ok"].(bool); exists && !okValue {
					return exitError(1)
				}
				return nil
			}
		}
	}

	fmt.Fprintf(os.Stderr, "Error: unexpected result format\n")
	return exitError(1)
}

// loadCodeConfig loads the MCP configuration file for code command
func loadCodeConfig() (*config.Config, error) {
	var configFilePath string

	if codeConfigPath != "" {
		configFilePath = codeConfigPath
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
		return nil, fmt.Errorf("configuration file not found at %s. Please run 'mcpproxy serve' first to create the config", configFilePath)
	}

	// Load configuration
	globalConfig, err := config.LoadFromFile(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config from %s: %w", configFilePath, err)
	}

	return globalConfig, nil
}

// createCodeLogger creates a zap logger with the specified level
func createCodeLogger(level string) (*zap.Logger, error) {
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

// exitError wraps an error with the given exit code
type exitCodeError struct {
	code int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("exit code %d", e.code)
}

func exitError(code int) error {
	os.Exit(code)
	return exitCodeError{code: code}
}
