package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	// Command flags
	upstreamOutputFormat string
	upstreamLogLevel     string
	upstreamConfigPath   string
	upstreamLogsTail     int
	upstreamLogsFollow   bool
)

// GetUpstreamCommand returns the upstream command for adding to the root command
func GetUpstreamCommand() *cobra.Command {
	return upstreamCmd
}

func init() {
	// Add subcommands
	upstreamCmd.AddCommand(upstreamListCmd)
	upstreamCmd.AddCommand(upstreamLogsCmd)

	// Define flags
	upstreamListCmd.Flags().StringVarP(&upstreamOutputFormat, "output", "o", "table", "Output format (table, json)")
	upstreamListCmd.Flags().StringVarP(&upstreamLogLevel, "log-level", "l", "warn", "Log level (trace, debug, info, warn, error)")
	upstreamListCmd.Flags().StringVarP(&upstreamConfigPath, "config", "c", "", "Path to MCP configuration file")

	upstreamLogsCmd.Flags().IntVarP(&upstreamLogsTail, "tail", "n", 50, "Number of log lines to show")
	upstreamLogsCmd.Flags().BoolVarP(&upstreamLogsFollow, "follow", "f", false, "Follow log output (requires daemon)")
	upstreamLogsCmd.Flags().StringVarP(&upstreamLogLevel, "log-level", "l", "warn", "Log level")
	upstreamLogsCmd.Flags().StringVarP(&upstreamConfigPath, "config", "c", "", "Path to config file")
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
		fmt.Printf("%-25s %-10s %-10s %-12s %-10s %s\n",
			"====", "=======", "========", "=========", "=====", "======")

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

func getStringField(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBoolField(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func getIntField(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(int); ok {
		return v
	}
	return 0
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
		return runUpstreamLogsFollowMode(ctx, globalConfig.DataDir, serverName, logger)
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

	for _, logLine := range logs {
		fmt.Println(logLine)
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

	lastLines := make(map[string]bool)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			logs, err := client.GetServerLogs(ctx, serverName, 10)
			if err != nil {
				logger.Warn("Failed to fetch logs", zap.Error(err))
				continue
			}

			// Print only new lines
			for _, line := range logs {
				if !lastLines[line] {
					fmt.Println(line)
					lastLines[line] = true
				}
			}
		}
	}
}
