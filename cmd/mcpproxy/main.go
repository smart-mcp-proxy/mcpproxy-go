package main

// @title MCPProxy API
// @version 1.0
// @description MCPProxy REST API for managing MCP servers, tools, and diagnostics
// @description
// @description MCPProxy is a smart proxy for AI agents using the Model Context Protocol (MCP).
// @description It provides intelligent tool discovery, massive token savings, and built-in security quarantine.
//
// @contact.name MCPProxy Support
// @contact.url https://github.com/smart-mcp-proxy/mcpproxy-go
//
// @license.name MIT
// @license.url https://opensource.org/licenses/MIT
//
// @host localhost:8080
// @BasePath /api/v1
//
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
// @description API key authentication. Provide your API key in the X-API-Key header.
//
// @securityDefinitions.apikey ApiKeyQuery
// @in query
// @name apikey
// @description API key authentication via query parameter. Use ?apikey=your-key

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	bbolterrors "go.etcd.io/bbolt/errors"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/experiments"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/registries"
	"mcpproxy-go/internal/server"
	"mcpproxy-go/internal/storage"
	_ "mcpproxy-go/oas" // Import generated swagger docs
)

var (
	configFile        string
	dataDir           string
	listen            string
	trayEndpoint      string
	enableSocket      bool
	logLevel          string
	debugSearch       bool
	toolResponseLimit int
	logToFile         bool
	logDir            string

	// Security flags
	readOnlyMode      bool
	disableManagement bool
	allowServerAdd    bool
	allowServerRemove bool
	enablePrompts     bool

	version = "v0.1.0" // This will be injected by -ldflags during build
)

const (
	defaultLogLevel = "info"
)

// maskAPIKey returns a masked version of the API key showing only first and last 4 characters
func maskAPIKey(apiKey string) string {
	if len(apiKey) <= 8 {
		return "****"
	}
	return apiKey[:4] + "****" + apiKey[len(apiKey)-4:]
}

func main() {
	// Set up registries initialization callback to avoid circular imports
	config.SetRegistriesInitCallback(registries.SetRegistriesFromConfig)

	rootCmd := &cobra.Command{
		Use:     "mcpproxy",
		Short:   "Smart MCP Proxy - Intelligent tool discovery and proxying for Model Context Protocol servers",
		Version: version,
	}

	// Add global flags
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "Configuration file path")
	rootCmd.PersistentFlags().StringVarP(&dataDir, "data-dir", "d", "", "Data directory path (default: ~/.mcpproxy)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "", "Log level (trace, debug, info, warn, error) - defaults: server=info, other commands=warn")
	rootCmd.PersistentFlags().BoolVar(&logToFile, "log-to-file", false, "Enable logging to file in standard OS location (default: console only)")
	rootCmd.PersistentFlags().StringVar(&logDir, "log-dir", "", "Custom log directory path (overrides standard OS location)")

	// Add server command
	serverCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP proxy server",
		Long:  "Start the MCP proxy server to handle connections and proxy tool calls",
		RunE:  runServer,
	}

	// Add server-specific flags
	serverCmd.Flags().StringVarP(&listen, "listen", "l", "", "Listen address (for HTTP mode, not used in stdio mode)")
	serverCmd.Flags().StringVar(&trayEndpoint, "tray-endpoint", "", "Tray endpoint override (unix:///path/socket.sock or npipe:////./pipe/name). Default: auto-detect from data-dir")
	serverCmd.Flags().BoolVar(&enableSocket, "enable-socket", true, "Enable Unix socket/named pipe for local IPC (default: true)")
	serverCmd.Flags().BoolVar(&debugSearch, "debug-search", false, "Enable debug search tool for search relevancy debugging")
	serverCmd.Flags().IntVar(&toolResponseLimit, "tool-response-limit", 0, "Tool response limit in characters (0 = disabled, default: 20000 from config)")
	serverCmd.Flags().BoolVar(&readOnlyMode, "read-only", false, "Enable read-only mode")
	serverCmd.Flags().BoolVar(&disableManagement, "disable-management", false, "Disable management features")
	serverCmd.Flags().BoolVar(&allowServerAdd, "allow-server-add", true, "Allow adding new servers")
	serverCmd.Flags().BoolVar(&allowServerRemove, "allow-server-remove", true, "Allow removing existing servers")
	serverCmd.Flags().BoolVar(&enablePrompts, "enable-prompts", true, "Enable prompts for user input")

	// Add search-servers command
	searchCmd := createSearchServersCommand()

	// Add tools command
	toolsCmd := GetToolsCommand()

	// Add call command
	callCmd := GetCallCommand()

	// Add code command
	codeCmd := GetCodeCommand()

	// Add auth command
	authCmd := GetAuthCommand()

	// Add secrets command
	secretsCmd := GetSecretsCommand()

	// Add trust-cert command
	trustCertCmd := GetTrustCertCommand()

	// Add upstream command
	upstreamCmd := GetUpstreamCommand()

	// Add doctor command
	doctorCmd := GetDoctorCommand()

	// Add commands to root
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(toolsCmd)
	rootCmd.AddCommand(callCmd)
	rootCmd.AddCommand(codeCmd)
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(secretsCmd)
	rootCmd.AddCommand(trustCertCmd)
	rootCmd.AddCommand(upstreamCmd)
	rootCmd.AddCommand(doctorCmd)

	// Default to server command for backward compatibility
	rootCmd.RunE = runServer

	if err := rootCmd.Execute(); err != nil {
		// Check for specific error types to return appropriate exit codes
		exitCode := classifyError(err)
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(exitCode)
	}
}

func createSearchServersCommand() *cobra.Command {
	var registryFlag, searchFlag, tagFlag string
	var listRegistries bool
	var limitFlag int

	cmd := &cobra.Command{
		Use:   "search-servers",
		Short: "Search MCP registries for available servers with repository detection",
		Long: `Search known MCP registries for available servers that can be added as upstreams.
This tool queries embedded registries to discover MCP servers and includes automatic
npm/PyPI package detection to enhance results with install commands.
Results can be directly used with the 'upstream_servers add' command.

Examples:
  # List all known registries
  mcpproxy search-servers --list-registries

  # Search for weather-related servers in the Smithery registry (limit 10 results)
  mcpproxy search-servers --registry smithery --search weather --limit 10

  # Search for servers tagged as "finance" in the Pulse registry
  mcpproxy search-servers --registry pulse --tag finance`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Setup logger for search command (non-server command = WARN level by default)
			cmdLogLevel, _ := cmd.Flags().GetString("log-level")
			cmdLogToFile, _ := cmd.Flags().GetBool("log-to-file")
			cmdLogDir, _ := cmd.Flags().GetString("log-dir")

			logger, err := logs.SetupCommandLogger(false, cmdLogLevel, cmdLogToFile, cmdLogDir)
			if err != nil {
				return fmt.Errorf("failed to setup logger: %w", err)
			}
			defer func() {
				_ = logger.Sync()
			}()

			if listRegistries {
				listAllRegistries(logger)
				return nil
			}

			if registryFlag == "" {
				return fmt.Errorf("--registry is required (use --list-registries to see available options)")
			}

			ctx := context.Background()

			// Create config to check if repository guessing is enabled
			cfg, err := config.LoadFromFile("")
			if err != nil {
				// Use default config if loading fails
				cfg = config.DefaultConfig()
			}

			// Initialize registries from config
			registries.SetRegistriesFromConfig(cfg)

			// Create experiments guesser if repository checking is enabled
			var guesser *experiments.Guesser
			if cfg.CheckServerRepo {
				// Use the proper logger instead of no-op
				guesser = experiments.NewGuesser(nil, logger)
			}

			logger.Info("Searching servers",
				zap.String("registry", registryFlag),
				zap.String("search", searchFlag),
				zap.String("tag", tagFlag),
				zap.Int("limit", limitFlag))

			servers, err := registries.SearchServers(ctx, registryFlag, tagFlag, searchFlag, limitFlag, guesser)
			if err != nil {
				logger.Error("Search failed", zap.Error(err))
				return fmt.Errorf("search failed: %w", err)
			}

			logger.Info("Search completed", zap.Int("results_count", len(servers)))

			// Print results as JSON
			output, err := json.MarshalIndent(servers, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to format results: %w", err)
			}

			fmt.Println(string(output))
			return nil
		},
	}

	cmd.Flags().StringVarP(&registryFlag, "registry", "r", "", "Registry ID or name to search (exact match)")
	cmd.Flags().StringVarP(&searchFlag, "search", "s", "", "Search term for server name/description")
	cmd.Flags().StringVarP(&tagFlag, "tag", "t", "", "Filter servers by tag/category")
	cmd.Flags().IntVarP(&limitFlag, "limit", "l", 10, "Maximum number of results to return (default: 10, max: 50)")
	cmd.Flags().BoolVar(&listRegistries, "list-registries", false, "List all known registries")

	return cmd
}

func listAllRegistries(logger *zap.Logger) {
	// Load config and initialize registries
	cfg, err := config.LoadFromFile("")
	if err != nil {
		// Use default config if loading fails
		cfg = config.DefaultConfig()
	}

	logger.Info("Loading registries configuration")

	// Initialize registries from config
	registries.SetRegistriesFromConfig(cfg)

	registryList := registries.ListRegistries()

	logger.Info("Found registries", zap.Int("count", len(registryList)))

	// Format as a simple table for CLI display
	fmt.Printf("%-20s %-30s %s\n", "ID", "NAME", "DESCRIPTION")
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	for i := range registryList {
		reg := &registryList[i]
		fmt.Printf("%-20s %-30s %s\n", reg.ID, reg.Name, reg.Description)
	}

	fmt.Printf("\nUse --registry <ID> to search a specific registry\n")
}

func runServer(cmd *cobra.Command, _ []string) error {
	// Get flag values from command (handles both global and local flags)
	cmdLogLevel, _ := cmd.Flags().GetString("log-level")
	cmdLogToFile, _ := cmd.Flags().GetBool("log-to-file")
	cmdLogDir, _ := cmd.Flags().GetString("log-dir")
	cmdDebugSearch, _ := cmd.Flags().GetBool("debug-search")
	cmdToolResponseLimit, _ := cmd.Flags().GetInt("tool-response-limit")
	cmdReadOnlyMode, _ := cmd.Flags().GetBool("read-only")
	cmdDisableManagement, _ := cmd.Flags().GetBool("disable-management")
	cmdAllowServerAdd, _ := cmd.Flags().GetBool("allow-server-add")
	cmdAllowServerRemove, _ := cmd.Flags().GetBool("allow-server-remove")
	cmdEnablePrompts, _ := cmd.Flags().GetBool("enable-prompts")

	// Load configuration first to get logging settings
	cfg, err := loadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Override logging settings from command line
	if cfg.Logging == nil {
		// Use command-specific default level (INFO for server command)
		defaultLevel := cmdLogLevel
		if defaultLevel == "" {
			defaultLevel = defaultLogLevel // Server command defaults to INFO
		}

		cfg.Logging = &config.LogConfig{
			Level:         defaultLevel,
			EnableFile:    !cmd.Flags().Changed("log-to-file") || cmdLogToFile, // Default true for serve, unless explicitly disabled
			EnableConsole: true,
			Filename:      "main.log",
			MaxSize:       10,
			MaxBackups:    5,
			MaxAge:        30,
			Compress:      true,
			JSONFormat:    false,
		}
	} else {
		// Override specific fields from command line
		if cmdLogLevel != "" {
			cfg.Logging.Level = cmdLogLevel
		} else if cfg.Logging.Level == "" {
			cfg.Logging.Level = defaultLogLevel // Server command defaults to INFO
		}

		// For serve mode: Enable file logging by default, only disable if explicitly set to false
		if cmd.Flags().Changed("log-to-file") {
			cfg.Logging.EnableFile = cmdLogToFile
		} else {
			cfg.Logging.EnableFile = true // Default to true for serve mode
		}

		if cfg.Logging.Filename == "" || cfg.Logging.Filename == "mcpproxy.log" {
			cfg.Logging.Filename = "main.log"
		}
	}

	// Override log directory if specified
	if cmdLogDir != "" {
		cfg.Logging.LogDir = cmdLogDir
	}

	// Setup logger with new logging system
	logger, err := logs.SetupLogger(cfg.Logging)
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer func() {
		_ = logger.Sync()
	}()

	// Log startup information including log directory info
	logDirInfo, err := logs.GetLogDirInfo()
	if err != nil {
		logger.Warn("Failed to get log directory info", zap.Error(err))
	} else {
		logger.Info("Log directory configured",
			zap.String("path", logDirInfo.Path),
			zap.String("os", logDirInfo.OS),
			zap.String("standard", logDirInfo.Standard))
	}

	logger.Info("Starting mcpproxy",
		zap.String("version", version),
		zap.String("log_level", cmdLogLevel),
		zap.Bool("log_to_file", cmdLogToFile))

	// Override other settings from command line
	cfg.DebugSearch = cmdDebugSearch

	if cmdToolResponseLimit != 0 {
		cfg.ToolResponseLimit = cmdToolResponseLimit
	}

	// Apply security settings from command line ONLY if explicitly set
	if cmd.Flags().Changed("read-only") {
		cfg.ReadOnlyMode = cmdReadOnlyMode
	}
	if cmd.Flags().Changed("disable-management") {
		cfg.DisableManagement = cmdDisableManagement
	}
	if cmd.Flags().Changed("allow-server-add") {
		cfg.AllowServerAdd = cmdAllowServerAdd
	}
	if cmd.Flags().Changed("allow-server-remove") {
		cfg.AllowServerRemove = cmdAllowServerRemove
	}
	if cmd.Flags().Changed("enable-prompts") {
		cfg.EnablePrompts = cmdEnablePrompts
	}

	logger.Info("Configuration loaded",
		zap.String("data_dir", cfg.DataDir),
		zap.Int("servers_count", len(cfg.Servers)),
		zap.Bool("read_only_mode", cfg.ReadOnlyMode),
		zap.Bool("disable_management", cfg.DisableManagement),
		zap.Bool("allow_server_add", cfg.AllowServerAdd),
		zap.Bool("allow_server_remove", cfg.AllowServerRemove),
		zap.Bool("enable_prompts", cfg.EnablePrompts))

	// Ensure API key is configured
	apiKey, wasGenerated, source := cfg.EnsureAPIKey()
	if apiKey == "" {
		logger.Info("API key authentication disabled")
	} else if wasGenerated {
		// Frame the auto-generated key message for visibility
		frameMsg := strings.Repeat("*", 80)
		logger.Warn(frameMsg)
		logger.Warn("API key was auto-generated for security. To access the Web UI and REST API, use this key:")
		logger.Warn("",
			zap.String("api_key", apiKey),
			zap.String("web_ui_url", fmt.Sprintf("http://%s/ui/?apikey=%s", cfg.Listen, apiKey)),
			zap.String("source", source.String()))
		logger.Warn(frameMsg)
	} else {
		// Mask API key when it comes from environment or config file
		maskedKey := maskAPIKey(apiKey)
		logger.Info("API key authentication enabled",
			zap.String("source", source.String()),
			zap.String("api_key_prefix", maskedKey))
	}

	// Create server with the actual config path used
	var actualConfigPath string
	if configFile != "" {
		actualConfigPath = configFile
	} else {
		// When using default config, still track the actual path used
		actualConfigPath = config.GetConfigPath(cfg.DataDir)
	}
	srv, err := server.NewServerWithConfigPath(cfg, actualConfigPath, logger)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Setup signal handling for graceful shutdown with force quit on second signal
	logger.Info("Signal handler goroutine starting - waiting for SIGINT or SIGTERM")
	_ = logger.Sync()
	go func() {
		logger.Info("Signal handler goroutine is running, waiting for signal on channel")
		_ = logger.Sync()
		sig := <-sigChan
		logger.Info("Received signal, shutting down", zap.String("signal", sig.String()))
		_ = logger.Sync() // Flush logs immediately so we can see shutdown messages
		logger.Info("Press Ctrl+C again within 10 seconds to force quit")
		_ = logger.Sync() // Flush again
		cancel()

		// Start a timer for force quit
		forceQuitTimer := time.NewTimer(10 * time.Second)
		defer forceQuitTimer.Stop()

		// Wait for second signal or timeout
		select {
		case sig2 := <-sigChan:
			logger.Warn("Received second signal, forcing immediate exit", zap.String("signal", sig2.String()))
			_ = logger.Sync()
			os.Exit(ExitCodeGeneralError)
		case <-forceQuitTimer.C:
			// Normal shutdown timeout - continue with graceful shutdown
			logger.Debug("Force quit timer expired, continuing with graceful shutdown")
		}
	}()

	// Start the server
	logger.Info("Starting mcpproxy server")
	if err := srv.StartServer(ctx); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	// Wait for context to be cancelled
	<-ctx.Done()
	logger.Info("Shutting down server")
	// Use Shutdown() instead of StopServer() to ensure proper container cleanup
	// Shutdown() calls runtime.Close() which triggers ShutdownAll() for Docker cleanup
	if err := srv.Shutdown(); err != nil {
		logger.Error("Error shutting down server", zap.Error(err))
	}

	return nil
}

func loadConfig(cmd *cobra.Command) (*config.Config, error) {
	var cfg *config.Config
	var err error

	// Load configuration - use LoadFromFile if config file specified, otherwise use Load
	if configFile != "" {
		cfg, err = config.LoadFromFile(configFile)
	} else {
		cfg, err = config.Load()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	// Override with command line flags ONLY if they were explicitly set
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	if cmd.Flags().Changed("listen") {
		listenFlag, _ := cmd.Flags().GetString("listen")
		cfg.Listen = listenFlag
	}
	if cmd.Flags().Changed("tray-endpoint") {
		trayEndpointFlag, _ := cmd.Flags().GetString("tray-endpoint")
		cfg.TrayEndpoint = trayEndpointFlag
	}
	if cmd.Flags().Changed("enable-socket") {
		enableSocketFlag, _ := cmd.Flags().GetBool("enable-socket")
		cfg.EnableSocket = enableSocketFlag
	}
	if toolResponseLimit != 0 {
		cfg.ToolResponseLimit = toolResponseLimit
	}

	// Validate the configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// classifyError categorizes errors to return appropriate exit codes
func classifyError(err error) int {
	if err == nil {
		return ExitCodeSuccess
	}

	// Check for port conflict errors
	var portErr *server.PortInUseError
	if errors.As(err, &portErr) {
		return ExitCodePortConflict
	}

	// Check for database lock errors (specific type first, then generic bbolt timeout)
	var dbLockedErr *storage.DatabaseLockedError
	if errors.As(err, &dbLockedErr) {
		return ExitCodeDBLocked
	}

	if errors.Is(err, bbolterrors.ErrTimeout) {
		return ExitCodeDBLocked
	}

	// Check for permission errors (exit code 5)
	var permErr *server.PermissionError
	if errors.As(err, &permErr) {
		return ExitCodePermissionError
	}

	// Check for string-based error messages from various sources
	errMsg := strings.ToLower(err.Error())

	// Port conflict indicators
	if strings.Contains(errMsg, "address already in use") ||
		strings.Contains(errMsg, "port") && strings.Contains(errMsg, "in use") ||
		strings.Contains(errMsg, "bind: address already in use") {
		return ExitCodePortConflict
	}

	// Database lock indicators
	if strings.Contains(errMsg, "database is locked") ||
		strings.Contains(errMsg, "database locked") ||
		strings.Contains(errMsg, "bolt") && strings.Contains(errMsg, "timeout") {
		return ExitCodeDBLocked
	}

	// Configuration error indicators
	if strings.Contains(errMsg, "invalid configuration") ||
		strings.Contains(errMsg, "config") && (strings.Contains(errMsg, "invalid") || strings.Contains(errMsg, "error")) ||
		strings.Contains(errMsg, "failed to load configuration") {
		return ExitCodeConfigError
	}

	// Permission error indicators (fallback for string-based errors)
	if strings.Contains(errMsg, "permission denied") ||
		strings.Contains(errMsg, "access denied") ||
		strings.Contains(errMsg, "operation not permitted") {
		return ExitCodePermissionError
	}

	// Default to general error
	return ExitCodeGeneralError
}
