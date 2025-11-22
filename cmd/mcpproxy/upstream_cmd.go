package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"mcpproxy-go/internal/cliclient"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/socket"
)

var (
	upstreamCmd = &cobra.Command{
		Use:   "upstream",
		Short: "Manage upstream MCP servers",
		Long:  "Commands for managing and monitoring upstream MCP servers",
	}

	upstreamListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all upstream servers with status",
		Long: `List all configured upstream MCP servers with connection status, tool counts, and errors.

Examples:
  mcpproxy upstream list
  mcpproxy upstream list --output=json
  mcpproxy upstream list --log-level=debug`,
		RunE: runUpstreamList,
	}

	upstreamLogsCmd = &cobra.Command{
		Use:   "logs <server-name>",
		Short: "Show logs for a specific server",
		Long: `Display recent log entries from a specific upstream server.

Examples:
  mcpproxy upstream logs github-server
  mcpproxy upstream logs github-server --tail=100
  mcpproxy upstream logs weather-api --follow`,
		Args: cobra.ExactArgs(1),
		RunE: runUpstreamLogs,
	}

	upstreamEnableCmd = &cobra.Command{
		Use:   "enable <server-name>",
		Short: "Enable a server",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runUpstreamEnable,
	}

	upstreamDisableCmd = &cobra.Command{
		Use:   "disable <server-name>",
		Short: "Disable a server",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runUpstreamDisable,
	}

	upstreamRestartCmd = &cobra.Command{
		Use:   "restart <server-name>",
		Short: "Restart a server",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runUpstreamRestart,
	}

	// Command flags
	upstreamOutputFormat string
	upstreamLogLevel     string
	upstreamConfigPath   string
	upstreamLogsTail     int
	upstreamLogsFollow   bool
	upstreamAll          bool
	upstreamForce        bool
)

// GetUpstreamCommand returns the upstream command for adding to the root command.
// The upstream command provides subcommands for managing and monitoring upstream
// MCP servers, including list, logs, enable/disable, and restart operations.
func GetUpstreamCommand() *cobra.Command {
	return upstreamCmd
}

func init() {
	// Add subcommands
	upstreamCmd.AddCommand(upstreamListCmd)
	upstreamCmd.AddCommand(upstreamLogsCmd)
	upstreamCmd.AddCommand(upstreamEnableCmd)
	upstreamCmd.AddCommand(upstreamDisableCmd)
	upstreamCmd.AddCommand(upstreamRestartCmd)

	// Define flags
	upstreamListCmd.Flags().StringVarP(&upstreamOutputFormat, "output", "o", "table", "Output format (table, json)")
	upstreamListCmd.Flags().StringVarP(&upstreamLogLevel, "log-level", "l", "warn", "Log level (trace, debug, info, warn, error)")
	upstreamListCmd.Flags().StringVarP(&upstreamConfigPath, "config", "c", "", "Path to MCP configuration file")

	upstreamLogsCmd.Flags().IntVarP(&upstreamLogsTail, "tail", "n", 50, "Number of log lines to show")
	upstreamLogsCmd.Flags().BoolVarP(&upstreamLogsFollow, "follow", "f", false, "Follow log output (requires daemon)")
	upstreamLogsCmd.Flags().StringVarP(&upstreamLogLevel, "log-level", "l", "warn", "Log level")
	upstreamLogsCmd.Flags().StringVarP(&upstreamConfigPath, "config", "c", "", "Path to config file")

	// Add --all and --force flags to enable/disable/restart
	upstreamEnableCmd.Flags().BoolVar(&upstreamAll, "all", false, "Enable all servers")
	upstreamEnableCmd.Flags().BoolVar(&upstreamForce, "force", false, "Skip confirmation prompt")

	upstreamDisableCmd.Flags().BoolVar(&upstreamAll, "all", false, "Disable all servers")
	upstreamDisableCmd.Flags().BoolVar(&upstreamForce, "force", false, "Skip confirmation prompt")

	upstreamRestartCmd.Flags().BoolVar(&upstreamAll, "all", false, "Restart all servers")
}

func runUpstreamList(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return err
	}

	// Create logger
	logger, err := createUpstreamLogger(upstreamLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return err
	}

	// Check if daemon is running
	if shouldUseUpstreamDaemon(globalConfig.DataDir) {
		logger.Info("Detected running daemon, using client mode via socket")
		return runUpstreamListClientMode(ctx, globalConfig.DataDir, logger)
	}

	// No daemon - load from config file
	logger.Info("No daemon detected, reading from config file")
	return runUpstreamListFromConfig(globalConfig)
}

func shouldUseUpstreamDaemon(dataDir string) bool {
	socketPath := socket.DetectSocketPath(dataDir)
	return socket.IsSocketAvailable(socketPath)
}

func runUpstreamListClientMode(ctx context.Context, dataDir string, logger *zap.Logger) error {
	socketPath := socket.DetectSocketPath(dataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Call GET /api/v1/servers
	servers, err := client.GetServers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get servers from daemon: %w", err)
	}

	return outputServers(servers)
}

func runUpstreamListFromConfig(globalConfig *config.Config) error {
	// Convert config servers to output format
	servers := make([]map[string]interface{}, len(globalConfig.Servers))
	for i, srv := range globalConfig.Servers {
		servers[i] = map[string]interface{}{
			"name":       srv.Name,
			"enabled":    srv.Enabled,
			"protocol":   srv.Protocol,
			"connected":  false,
			"tool_count": 0,
			"status":     "unknown (daemon not running)",
		}
	}

	return outputServers(servers)
}

func outputServers(servers []map[string]interface{}) error {
	// Sort servers alphabetically by name for consistent output
	sort.Slice(servers, func(i, j int) bool {
		nameI := getStringField(servers[i], "name")
		nameJ := getStringField(servers[j], "name")
		return nameI < nameJ
	})

	switch upstreamOutputFormat {
	case "json":
		output, err := json.MarshalIndent(servers, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}
		fmt.Println(string(output))
	case "table", "":
		// Table format (default)
		fmt.Printf("%-25s %-10s %-10s %-12s %-10s %s\n",
			"NAME", "ENABLED", "PROTOCOL", "CONNECTED", "TOOLS", "STATUS")
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

		for _, srv := range servers {
			name := getStringField(srv, "name")
			enabled := getBoolField(srv, "enabled")
			protocol := getStringField(srv, "protocol")
			connected := getBoolField(srv, "connected")
			toolCount := getIntField(srv, "tool_count")
			status := getStringField(srv, "status")

			enabledStr := "no"
			if enabled {
				enabledStr = "yes"
			}

			connectedStr := "no"
			if connected {
				connectedStr = "yes"
			}

			fmt.Printf("%-25s %-10s %-10s %-12s %-10d %s\n",
				name, enabledStr, protocol, connectedStr, toolCount, status)
		}
	default:
		return fmt.Errorf("unknown output format: %s", upstreamOutputFormat)
	}

	return nil
}

func loadUpstreamConfig() (*config.Config, error) {
	if upstreamConfigPath != "" {
		return config.LoadFromFile(upstreamConfigPath)
	}
	return config.Load()
}

func createUpstreamLogger(level string) (*zap.Logger, error) {
	var zapLevel zap.AtomicLevel
	switch level {
	case "trace", "debug":
		zapLevel = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		zapLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		zapLevel = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		zapLevel = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		zapLevel = zap.NewAtomicLevelAt(zap.WarnLevel)
	}

	cfg := zap.Config{
		Level:            zapLevel,
		Development:      false,
		Encoding:         "console",
		EncoderConfig:    zap.NewDevelopmentEncoderConfig(),
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}

	return cfg.Build()
}

func runUpstreamLogs(cmd *cobra.Command, args []string) error {
	serverName := args[0]

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return err
	}

	// Create logger
	logger, err := createUpstreamLogger(upstreamLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return err
	}

	// Follow mode requires daemon
	if upstreamLogsFollow {
		if !shouldUseUpstreamDaemon(globalConfig.DataDir) {
			return fmt.Errorf("--follow requires running daemon")
		}
		logger.Info("Following logs from daemon")
		// Use background context with signal handling for follow mode
		bgCtx, bgCancel := context.WithCancel(context.Background())
		defer bgCancel()

		// Handle Ctrl+C gracefully
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)

		go func() {
			select {
			case <-sigChan:
				logger.Info("Received interrupt signal, stopping...")
				bgCancel()
			case <-bgCtx.Done():
				// Context canceled, exit goroutine
			}
		}()

		return runUpstreamLogsFollowMode(bgCtx, globalConfig.DataDir, serverName, logger)
	}

	// Check if daemon is running
	if shouldUseUpstreamDaemon(globalConfig.DataDir) {
		logger.Info("Detected running daemon, using client mode via socket")
		return runUpstreamLogsClientMode(ctx, globalConfig.DataDir, serverName, logger)
	}

	// No daemon - read from log file
	logger.Info("No daemon detected, reading from log file")
	return runUpstreamLogsFromFile(globalConfig, serverName)
}

func runUpstreamLogsClientMode(ctx context.Context, dataDir, serverName string, logger *zap.Logger) error {
	socketPath := socket.DetectSocketPath(dataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Call GET /api/v1/servers/{name}/logs?tail=N
	logs, err := client.GetServerLogs(ctx, serverName, upstreamLogsTail)
	if err != nil {
		return fmt.Errorf("failed to get logs from daemon: %w", err)
	}

	for _, entry := range logs {
		fmt.Printf("%s [%s] %s\n", entry.Timestamp.Format("2006-01-02 15:04:05"), entry.Level, entry.Message)
	}

	return nil
}

func runUpstreamLogsFromFile(globalConfig *config.Config, serverName string) error {
	// Read from log file directly
	logDir := globalConfig.Logging.LogDir
	if logDir == "" {
		// Use OS-specific standard log directory
		var err error
		logDir, err = logs.GetLogDir()
		if err != nil {
			return fmt.Errorf("failed to determine log directory: %w", err)
		}
	}

	logFile := filepath.Join(logDir, fmt.Sprintf("server-%s.log", serverName))

	// Check if file exists
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		return fmt.Errorf("log file not found: %s (daemon may not have run yet)", logFile)
	}

	// Read last N lines using tail command
	cmd := exec.Command("tail", "-n", fmt.Sprintf("%d", upstreamLogsTail), logFile)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to read log file: %w", err)
	}

	fmt.Print(string(output))
	return nil
}

func runUpstreamLogsFollowMode(ctx context.Context, dataDir, serverName string, logger *zap.Logger) error {
	socketPath := socket.DetectSocketPath(dataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	fmt.Printf("Following logs for server '%s' (Ctrl+C to stop)...\n", serverName)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Ring buffer to track recently seen lines and prevent unbounded memory growth
	const maxTrackedLines = 1000
	lastLines := make(map[string]bool)
	lineOrder := make([]string, 0, maxTrackedLines)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			logs, err := client.GetServerLogs(ctx, serverName, upstreamLogsTail)
			if err != nil {
				logger.Warn("Failed to fetch logs", zap.Error(err))
				continue
			}

			// Print only new lines
			for _, entry := range logs {
				// Format the log entry as a unique string for deduplication
				logLine := fmt.Sprintf("%s [%s] %s", entry.Timestamp.Format("2006-01-02 15:04:05"), entry.Level, entry.Message)

				if !lastLines[logLine] {
					fmt.Println(logLine)
					lastLines[logLine] = true
					lineOrder = append(lineOrder, logLine)

					// Implement ring buffer: remove oldest line if we exceed max
					if len(lineOrder) > maxTrackedLines {
						oldestLine := lineOrder[0]
						delete(lastLines, oldestLine)
						lineOrder = lineOrder[1:]
					}
				}
			}
		}
	}
}

func runUpstreamEnable(cmd *cobra.Command, args []string) error {
	if upstreamAll {
		return runUpstreamBulkAction("enable", upstreamForce)
	}
	if len(args) == 0 {
		return fmt.Errorf("server name required (or use --all)")
	}
	return runUpstreamAction(args[0], "enable")
}

func runUpstreamDisable(cmd *cobra.Command, args []string) error {
	if upstreamAll {
		return runUpstreamBulkAction("disable", upstreamForce)
	}
	if len(args) == 0 {
		return fmt.Errorf("server name required (or use --all)")
	}
	return runUpstreamAction(args[0], "disable")
}

func runUpstreamRestart(cmd *cobra.Command, args []string) error {
	if upstreamAll {
		return runUpstreamBulkAction("restart", false) // restart doesn't need confirmation
	}
	if len(args) == 0 {
		return fmt.Errorf("server name required (or use --all)")
	}
	return runUpstreamAction(args[0], "restart")
}

// validateServerExists checks if a server exists in the configuration
func validateServerExists(cfg *config.Config, serverName string) error {
	for _, srv := range cfg.Servers {
		if srv.Name == serverName {
			return nil
		}
	}
	return fmt.Errorf("server '%s' not found in configuration", serverName)
}

func runUpstreamAction(serverName, action string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return err
	}

	// Validate server exists
	if err := validateServerExists(globalConfig, serverName); err != nil {
		return err
	}

	// Create logger
	logger, err := createUpstreamLogger(upstreamLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return err
	}

	// Require daemon for actions
	if !shouldUseUpstreamDaemon(globalConfig.DataDir) {
		return fmt.Errorf("server actions require running daemon. Start with: mcpproxy serve")
	}

	socketPath := socket.DetectSocketPath(globalConfig.DataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	fmt.Printf("Performing action '%s' on server '%s'...\n", action, serverName)

	err = client.ServerAction(ctx, serverName, action)
	if err != nil {
		return fmt.Errorf("failed to %s server: %w", action, err)
	}

	fmt.Printf("✅ Successfully %sed server '%s'\n", action, serverName)
	return nil
}

func runUpstreamBulkAction(action string, force bool) error {
	// Use a longer parent context (2 minutes) to allow multiple operations
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return err
	}

	// Create logger
	logger, err := createUpstreamLogger(upstreamLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return err
	}

	// Require daemon
	if !shouldUseUpstreamDaemon(globalConfig.DataDir) {
		return fmt.Errorf("server actions require running daemon. Start with: mcpproxy serve")
	}

	socketPath := socket.DetectSocketPath(globalConfig.DataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Get server list to count
	servers, err := client.GetServers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get server list: %w", err)
	}

	// Filter based on action (enable=disabled servers, disable=enabled servers)
	var targetServers []string
	for _, srv := range servers {
		name := getStringField(srv, "name")
		enabled := getBoolField(srv, "enabled")

		if action == "enable" && !enabled {
			targetServers = append(targetServers, name)
		} else if action == "disable" && enabled {
			targetServers = append(targetServers, name)
		} else if action == "restart" && enabled {
			targetServers = append(targetServers, name)
		}
	}

	if len(targetServers) == 0 {
		fmt.Printf("⚠️  No servers to %s\n", action)
		return nil
	}

	// Require confirmation for enable/disable --all
	if action == "enable" || action == "disable" {
		confirmed, err := confirmBulkAction(action, len(targetServers), force)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println("Operation cancelled")
			return nil
		}
	}

	// Perform action on all servers
	fmt.Printf("Performing action '%s' on %d server(s)...\n", action, len(targetServers))

	for _, serverName := range targetServers {
		// Give each server its own 30-second timeout
		serverCtx, serverCancel := context.WithTimeout(ctx, 30*time.Second)

		err = client.ServerAction(serverCtx, serverName, action)
		serverCancel() // Clean up immediately after each operation

		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to %s server '%s': %v\n", action, serverName, err)
		} else {
			fmt.Printf("✅ Successfully %sed server '%s'\n", action, serverName)
		}
	}

	return nil
}
