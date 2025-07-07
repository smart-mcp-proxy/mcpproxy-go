package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/logs"
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

// TrayInterface defines the interface for system tray functionality
type TrayInterface interface {
	Run(ctx context.Context) error
}

// createTray is implemented in build-tagged files

func main() {
	rootCmd := &cobra.Command{
		Use:     "mcpproxy",
		Short:   "Smart MCP Proxy - Intelligent tool discovery and proxying for Model Context Protocol servers",
		Version: version,
		RunE:    runServer,
	}

	// Add flags
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "Configuration file path")
	rootCmd.PersistentFlags().StringVarP(&dataDir, "data-dir", "d", "", "Data directory path (default: ~/.mcpproxy)")
	rootCmd.PersistentFlags().StringVarP(&listen, "listen", "l", "", "Listen address (for HTTP mode, not used in stdio mode)")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().BoolVar(&enableTray, "tray", true, "Enable system tray (use --tray=false to disable)")
	rootCmd.PersistentFlags().BoolVar(&debugSearch, "debug-search", false, "Enable debug search tool for search relevancy debugging")
	rootCmd.PersistentFlags().IntVar(&toolResponseLimit, "tool-response-limit", 0, "Tool response limit in characters (0 = disabled, default: 20000 from config)")
	rootCmd.PersistentFlags().BoolVar(&logToFile, "log-to-file", true, "Enable logging to file in standard OS location")
	rootCmd.PersistentFlags().StringVar(&logDir, "log-dir", "", "Custom log directory path (overrides standard OS location)")
	rootCmd.PersistentFlags().BoolVar(&readOnlyMode, "read-only", false, "Enable read-only mode")
	rootCmd.PersistentFlags().BoolVar(&disableManagement, "disable-management", false, "Disable management features")
	rootCmd.PersistentFlags().BoolVar(&allowServerAdd, "allow-server-add", true, "Allow adding new servers")
	rootCmd.PersistentFlags().BoolVar(&allowServerRemove, "allow-server-remove", true, "Allow removing existing servers")
	rootCmd.PersistentFlags().BoolVar(&enablePrompts, "enable-prompts", true, "Enable prompts for user input")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runServer(cmd *cobra.Command, _ []string) error {
	// Load configuration first to get logging settings
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Override logging settings from command line
	if cfg.Logging == nil {
		cfg.Logging = &config.LogConfig{
			Level:         logLevel,
			EnableFile:    logToFile,
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
		cfg.Logging.Level = logLevel
		cfg.Logging.EnableFile = logToFile
		if cfg.Logging.Filename == "" || cfg.Logging.Filename == "mcpproxy.log" {
			cfg.Logging.Filename = "main.log"
		}
	}

	// Override log directory if specified
	if logDir != "" {
		cfg.Logging.LogDir = logDir
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
		zap.String("log_level", logLevel),
		zap.Bool("tray_enabled", enableTray),
		zap.Bool("log_to_file", logToFile))

	// Override other settings from command line
	// Check if the tray flag was explicitly set by the user
	if cmd.Flags().Changed("tray") {
		cfg.EnableTray = enableTray
	}
	cfg.DebugSearch = debugSearch

	if toolResponseLimit != 0 {
		cfg.ToolResponseLimit = toolResponseLimit
	}

	// Apply security settings from command line
	cfg.ReadOnlyMode = readOnlyMode
	cfg.DisableManagement = disableManagement
	cfg.AllowServerAdd = allowServerAdd
	cfg.AllowServerRemove = allowServerRemove
	cfg.EnablePrompts = enablePrompts

	logger.Info("Configuration loaded",
		zap.String("data_dir", cfg.DataDir),
		zap.Int("servers_count", len(cfg.Servers)),
		zap.Bool("tray_enabled", cfg.EnableTray),
		zap.Bool("read_only_mode", cfg.ReadOnlyMode),
		zap.Bool("disable_management", cfg.DisableManagement),
		zap.Bool("enable_prompts", cfg.EnablePrompts))

	// Create server
	srv, err := server.NewServer(cfg, logger)
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
	}

	go func() {
		sig := <-sigChan
		logger.Info("Received signal, shutting down", zap.String("signal", sig.String()))
		shutdownFunc()
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
		if err := trayApp.Run(ctx); err != nil && err != context.Canceled {
			logger.Error("Tray application error", zap.Error(err))
		}

		// Wait for server goroutine to finish
		wg.Wait()
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

func loadConfig() (*config.Config, error) {
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

	// Override with command line flags if provided
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	if listen != "" {
		cfg.Listen = listen
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
