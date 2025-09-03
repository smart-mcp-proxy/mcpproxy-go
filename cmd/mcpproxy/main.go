package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/experiments"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/registries"
	"mcpproxy-go/internal/server"
)

var (
	configFile        string
	dataDir           string
	listen            string
	logLevel          string
	enableTray        bool
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

// TrayInterface defines the interface for system tray functionality
type TrayInterface interface {
	Run(ctx context.Context) error
}

// createTray is implemented in build-tagged files

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
	serverCmd.Flags().BoolVar(&enableTray, "tray", true, "Enable system tray (use --tray=false to disable)")
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

	// Add auth command
	authCmd := GetAuthCommand()

	// Add commands to root
	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(toolsCmd)
	rootCmd.AddCommand(callCmd)
	rootCmd.AddCommand(authCmd)

	// Default to server command for backward compatibility
	rootCmd.RunE = runServer

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
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
	fmt.Printf("%-20s %-30s %s\n", "==", "====", "===========")

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
	cmdEnableTray, _ := cmd.Flags().GetBool("tray")
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
		zap.Bool("tray_enabled", cmdEnableTray),
		zap.Bool("log_to_file", cmdLogToFile))

	// Override other settings from command line
	// Check if the tray flag was explicitly set by the user
	if cmd.Flags().Changed("tray") {
		cfg.EnableTray = cmdEnableTray
	}
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
		zap.Bool("tray_enabled", cfg.EnableTray),
		zap.Bool("read_only_mode", cfg.ReadOnlyMode),
		zap.Bool("disable_management", cfg.DisableManagement),
		zap.Bool("allow_server_add", cfg.AllowServerAdd),
		zap.Bool("allow_server_remove", cfg.AllowServerRemove),
		zap.Bool("enable_prompts", cfg.EnablePrompts))

	// Create server with the actual config path used
	var actualConfigPath string
	if configFile != "" {
		actualConfigPath = configFile
	}
	srv, err := server.NewServerWithConfigPath(cfg, actualConfigPath, logger)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Shutdown function that can be called from tray
	shutdownFunc := func() {
		logger.Info("Shutdown requested")
		cancel()
		// Don't wait here - let the main thread handle the delay
	}

	go func() {
		sig := <-sigChan
		logger.Info("Received signal, shutting down", zap.String("signal", sig.String()))
		shutdownFunc()
		// Don't exit here - let main thread handle the shutdown delay
	}()

	if cfg.EnableTray {
		// When tray is enabled, start tray immediately and auto-start server
		logger.Info("Starting system tray with auto-start server")

		// Create and start tray on main thread (required for macOS)
		trayApp := createTray(srv, logger.Sugar(), version, shutdownFunc)

		// Auto-start server in background
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info("Auto-starting server for tray mode")
			if err := srv.StartServer(ctx); err != nil {
				logger.Error("Failed to auto-start server", zap.Error(err))
				// Don't cancel context here - let tray handle manual start/stop
			}
		}()

		// This is a blocking call that runs the tray event loop
		logger.Info("MAIN - Starting tray event loop")
		if err := trayApp.Run(ctx); err != nil && err != context.Canceled {
			logger.Error("Tray application error", zap.Error(err))
		}
		logger.Info("MAIN - Tray event loop exited")

		// If context was cancelled (shutdown requested), wait for container cleanup
		if ctx.Err() != nil {
			logger.Info("MAIN - Tray exited due to shutdown, waiting for container cleanup...")
			time.Sleep(12 * time.Second)
			logger.Info("MAIN - Container cleanup wait completed")
		} else {
			logger.Info("MAIN - Tray exited normally")
		}

		// Wait for server goroutine to finish
		logger.Info("MAIN - Waiting for server goroutine to finish")
		wg.Wait()
		logger.Info("MAIN - Server goroutine finished, exiting")
	} else {
		// Without tray, run server normally and wait
		logger.Info("Starting server without tray")
		if err := srv.StartServer(ctx); err != nil {
			return fmt.Errorf("failed to start server: %w", err)
		}

		// Wait for context to be cancelled
		<-ctx.Done()
		logger.Info("Shutting down server")
		if err := srv.StopServer(); err != nil {
			logger.Error("Error stopping server", zap.Error(err))
		}
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
	if toolResponseLimit != 0 {
		cfg.ToolResponseLimit = toolResponseLimit
	}

	// Validate the configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}
