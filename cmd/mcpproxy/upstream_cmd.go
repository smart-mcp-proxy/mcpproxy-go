package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"
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
		Use:   "logs [server-name]",
		Short: "Show logs for a specific server",
		Long: `Display recent log entries from a specific upstream server.

Examples:
  mcpproxy upstream logs --server github-server
  mcpproxy upstream logs --server github-server --tail=100
  mcpproxy upstream logs weather-api --follow`,
		Args: cobra.MaximumNArgs(1),
		RunE: runUpstreamLogs,
	}

	upstreamEnableCmd = &cobra.Command{
		Use:   "enable [server-name]",
		Short: "Enable a server",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runUpstreamEnable,
	}

	upstreamDisableCmd = &cobra.Command{
		Use:   "disable [server-name]",
		Short: "Disable a server",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runUpstreamDisable,
	}

	upstreamRestartCmd = &cobra.Command{
		Use:   "restart [server-name]",
		Short: "Restart a server",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runUpstreamRestart,
	}

	upstreamAddCmd = &cobra.Command{
		Use:   "add <name> [url] [-- command args...]",
		Short: "Add an upstream MCP server",
		Long: `Add a new upstream MCP server to the configuration.

For HTTP-based servers:
  mcpproxy upstream add notion https://mcp.notion.com/sse
  mcpproxy upstream add weather https://api.weather.com/mcp --header "Authorization: Bearer token"

For stdio-based servers (use -- to separate command):
  mcpproxy upstream add fs -- npx -y @anthropic/mcp-server-filesystem /path/to/dir
  mcpproxy upstream add sqlite -- uvx mcp-server-sqlite --db mydb.db

New servers are quarantined by default for security. Use the web UI to approve them.`,
		RunE: runUpstreamAdd,
	}

	upstreamRemoveCmd = &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an upstream MCP server",
		Long: `Remove an upstream MCP server from the configuration.

Examples:
  mcpproxy upstream remove notion
  mcpproxy upstream remove fs --yes  # Skip confirmation`,
		Args: cobra.ExactArgs(1),
		RunE: runUpstreamRemove,
	}

	upstreamAddJSONCmd = &cobra.Command{
		Use:   "add-json <name> <json>",
		Short: "Add an upstream server using JSON configuration",
		Long: `Add a new upstream MCP server using a JSON configuration object.

Examples:
  mcpproxy upstream add-json weather '{"url":"https://api.weather.com/mcp","headers":{"Authorization":"Bearer token"}}'
  mcpproxy upstream add-json sqlite '{"command":"uvx","args":["mcp-server-sqlite","--db","mydb.db"]}'`,
		Args: cobra.ExactArgs(2),
		RunE: runUpstreamAddJSON,
	}

	// Command flags
	upstreamLogLevel   string
	upstreamConfigPath string
	upstreamLogsTail   int
	upstreamLogsFollow bool
	upstreamAll        bool
	upstreamForce      bool
	upstreamServerName string

	// Add command flags
	upstreamAddHeaders      []string
	upstreamAddEnvs         []string
	upstreamAddWorkingDir   string
	upstreamAddTransport    string
	upstreamAddIfNotExists  bool
	upstreamAddNoQuarantine bool

	// Remove command flags
	upstreamRemoveYes      bool
	upstreamRemoveIfExists bool
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
	upstreamCmd.AddCommand(upstreamAddCmd)
	upstreamCmd.AddCommand(upstreamRemoveCmd)
	upstreamCmd.AddCommand(upstreamAddJSONCmd)

	// Define flags (note: output format handled by global --output/-o flag from root command)
	upstreamListCmd.Flags().StringVarP(&upstreamLogLevel, "log-level", "l", "warn", "Log level (trace, debug, info, warn, error)")
	upstreamListCmd.Flags().StringVarP(&upstreamConfigPath, "config", "c", "", "Path to MCP configuration file")

	upstreamLogsCmd.Flags().IntVarP(&upstreamLogsTail, "tail", "n", 50, "Number of log lines to show")
	upstreamLogsCmd.Flags().BoolVarP(&upstreamLogsFollow, "follow", "f", false, "Follow log output (requires daemon)")
	upstreamLogsCmd.Flags().StringVarP(&upstreamLogLevel, "log-level", "l", "warn", "Log level")
	upstreamLogsCmd.Flags().StringVarP(&upstreamConfigPath, "config", "c", "", "Path to config file")
	upstreamLogsCmd.Flags().StringVarP(&upstreamServerName, "server", "s", "", "Name of the upstream server")

	// Add --all and --force flags to enable/disable/restart
	upstreamEnableCmd.Flags().BoolVar(&upstreamAll, "all", false, "Enable all servers")
	upstreamEnableCmd.Flags().BoolVar(&upstreamForce, "force", false, "Skip confirmation prompt")
	upstreamEnableCmd.Flags().StringVarP(&upstreamServerName, "server", "s", "", "Name of the upstream server (required unless --all)")

	upstreamDisableCmd.Flags().BoolVar(&upstreamAll, "all", false, "Disable all servers")
	upstreamDisableCmd.Flags().BoolVar(&upstreamForce, "force", false, "Skip confirmation prompt")
	upstreamDisableCmd.Flags().StringVarP(&upstreamServerName, "server", "s", "", "Name of the upstream server (required unless --all)")

	upstreamRestartCmd.Flags().BoolVar(&upstreamAll, "all", false, "Restart all servers")
	upstreamRestartCmd.Flags().StringVarP(&upstreamServerName, "server", "s", "", "Name of the upstream server (required unless --all)")

	// Add command flags
	upstreamAddCmd.Flags().StringArrayVar(&upstreamAddHeaders, "header", nil, "HTTP header in 'Name: value' format (repeatable)")
	upstreamAddCmd.Flags().StringArrayVar(&upstreamAddEnvs, "env", nil, "Environment variable in KEY=value format (repeatable)")
	upstreamAddCmd.Flags().StringVar(&upstreamAddWorkingDir, "working-dir", "", "Working directory for stdio commands")
	upstreamAddCmd.Flags().StringVar(&upstreamAddTransport, "transport", "", "Transport type: http or stdio (auto-detected if not specified)")
	upstreamAddCmd.Flags().BoolVar(&upstreamAddIfNotExists, "if-not-exists", false, "Don't error if server already exists")
	upstreamAddCmd.Flags().BoolVar(&upstreamAddNoQuarantine, "no-quarantine", false, "Don't quarantine the new server (use with caution)")

	// Remove command flags
	upstreamRemoveCmd.Flags().BoolVar(&upstreamRemoveYes, "yes", false, "Skip confirmation prompt")
	upstreamRemoveCmd.Flags().BoolVarP(&upstreamRemoveYes, "y", "y", false, "Skip confirmation prompt (short form)")
	upstreamRemoveCmd.Flags().BoolVar(&upstreamRemoveIfExists, "if-exists", false, "Don't error if server doesn't exist")
}

func runUpstreamList(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		return outputError(output.NewStructuredError(output.ErrCodeConfigNotFound, err.Error()).
			WithGuidance("Check that your config file exists and is valid").
			WithRecoveryCommand("mcpproxy doctor"), output.ErrCodeConfigNotFound)
	}

	// Create logger
	logger, err := createUpstreamLogger(upstreamLogLevel)
	if err != nil {
		return outputError(err, output.ErrCodeOperationFailed)
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
		return outputError(output.NewStructuredError(output.ErrCodeConnectionFailed, err.Error()).
			WithGuidance("Ensure the mcpproxy daemon is running").
			WithRecoveryCommand("mcpproxy serve"), output.ErrCodeConnectionFailed)
	}

	return outputServers(servers)
}

func runUpstreamListFromConfig(globalConfig *config.Config) error {
	// Convert config servers to output format
	servers := make([]map[string]interface{}, len(globalConfig.Servers))
	for i, srv := range globalConfig.Servers {
		// I-003: Use health.CalculateHealth() instead of inline logic for DRY principle
		healthInput := health.HealthCalculatorInput{
			Name:        srv.Name,
			Enabled:     srv.Enabled,
			Quarantined: srv.Quarantined,
			State:       "disconnected", // Daemon not running
			Connected:   false,
			ToolCount:   0,
		}
		healthStatus := health.CalculateHealth(healthInput, health.DefaultHealthConfig())

		// Override summary for config-only mode to indicate daemon status
		summary := healthStatus.Summary
		if healthStatus.AdminState == health.StateEnabled {
			summary = "Daemon not running"
		}

		servers[i] = map[string]interface{}{
			"name":       srv.Name,
			"enabled":    srv.Enabled,
			"protocol":   srv.Protocol,
			"connected":  false,
			"tool_count": 0,
			"status":     summary,
			"health": map[string]interface{}{
				"level":       healthStatus.Level,
				"admin_state": healthStatus.AdminState,
				"summary":     summary,
				"detail":      healthStatus.Detail,
				"action":      healthStatus.Action,
			},
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

	// Get the output formatter based on global flags
	formatter, err := GetOutputFormatter()
	if err != nil {
		return output.NewStructuredError(output.ErrCodeInvalidOutputFormat, err.Error()).
			WithGuidance("Use -o table, -o json, or -o yaml")
	}

	outputFormat := ResolveOutputFormat()

	// For structured formats (json, yaml), output raw data
	if outputFormat == "json" || outputFormat == "yaml" {
		result, err := formatter.Format(servers)
		if err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}
		fmt.Println(result)
		return nil
	}

	// For table format, build headers and rows with formatted data
	headers := []string{"", "NAME", "PROTOCOL", "TOOLS", "STATUS", "ACTION"}
	rows := make([][]string, 0, len(servers))

	for _, srv := range servers {
		name := getStringField(srv, "name")
		protocol := getStringField(srv, "protocol")
		toolCount := getIntField(srv, "tool_count")

		// Extract unified health status
		healthData, _ := srv["health"].(map[string]interface{})
		healthLevel := "unknown"
		healthAdminState := "enabled"
		healthSummary := getStringField(srv, "status") // fallback to old status
		healthAction := ""
		healthDetail := ""

		if healthData != nil {
			healthLevel = getStringField(healthData, "level")
			healthAdminState = getStringField(healthData, "admin_state")
			healthSummary = getStringField(healthData, "summary")
			healthAction = getStringField(healthData, "action")
			healthDetail = getStringField(healthData, "detail")
		}

		// Status emoji based on health level and admin state
		statusEmoji := "⚪" // unknown
		switch healthAdminState {
		case "disabled":
			statusEmoji = "⏸️ " // paused
		case "quarantined":
			statusEmoji = "🔒" // locked
		default:
			switch healthLevel {
			case "healthy":
				statusEmoji = "✅"
			case "degraded":
				statusEmoji = "⚠️ "
			case "unhealthy":
				statusEmoji = "❌"
			}
		}

		// Format action as CLI command hint
		actionHint := "-"
		switch healthAction {
		case "login":
			actionHint = fmt.Sprintf("auth login --server=%s", name)
		case "restart":
			actionHint = fmt.Sprintf("upstream restart %s", name)
		case "enable":
			actionHint = fmt.Sprintf("upstream enable %s", name)
		case "approve":
			actionHint = "Approve in Web UI"
		case "view_logs":
			actionHint = fmt.Sprintf("upstream logs %s", name)
		case health.ActionSetSecret:
			if healthDetail != "" {
				actionHint = fmt.Sprintf("Set %s", healthDetail)
			} else {
				actionHint = "Set secret in config"
			}
		case health.ActionConfigure:
			actionHint = "Edit config"
		}

		rows = append(rows, []string{
			statusEmoji,
			name,
			protocol,
			fmt.Sprintf("%d", toolCount),
			healthSummary,
			actionHint,
		})
	}

	result, err := formatter.FormatTable(headers, rows)
	if err != nil {
		return fmt.Errorf("failed to format table: %w", err)
	}
	fmt.Print(result)
	return nil
}

// outputError formats and outputs an error based on the current output format.
// For structured formats (json, yaml), it outputs a StructuredError.
// For table format, it outputs a human-readable error message to stderr.
// T023: Updated to extract request_id from APIError for log correlation
func outputError(err error, code string) error {
	outputFormat := ResolveOutputFormat()

	// T023: Extract request_id from APIError if available
	var requestID string
	var apiErr *cliclient.APIError
	if errors.As(err, &apiErr) && apiErr.HasRequestID() {
		requestID = apiErr.RequestID
	}

	// Convert to StructuredError if not already
	var structErr output.StructuredError
	if se, ok := err.(output.StructuredError); ok {
		structErr = se
	} else {
		structErr = output.NewStructuredError(code, err.Error())
	}

	// T023: Add request_id to StructuredError if available
	if requestID != "" {
		structErr = structErr.WithRequestID(requestID)
	}

	// For structured formats, output JSON/YAML error to stdout
	if outputFormat == "json" || outputFormat == "yaml" {
		formatter, fmtErr := GetOutputFormatter()
		if fmtErr != nil {
			// Fallback to plain error if formatter fails
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return err
		}

		result, formatErr := formatter.FormatError(structErr)
		if formatErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return err
		}

		fmt.Println(result)
		return structErr
	}

	// For table format, output human-readable error to stderr
	// T023: Include request ID with log retrieval suggestion if available
	if requestID != "" {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nRequest ID: %s\n", requestID)
		fmt.Fprintf(os.Stderr, "Use 'mcpproxy activity list --request-id %s' to find related logs.\n", requestID)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
	return err
}

func loadUpstreamConfig() (*config.Config, error) {
	if upstreamConfigPath != "" {
		cfg, err := config.LoadFromFile(upstreamConfigPath)
		if err != nil {
			return nil, err
		}
		// Respect global --data-dir flag
		if dataDir != "" {
			cfg.DataDir = dataDir
		}
		return cfg, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	// Respect global --data-dir flag
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	return cfg, nil
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
	serverName, err := resolveServerName(args, false)
	if err != nil {
		return err
	}

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
		if upstreamServerName != "" || len(args) > 0 {
			return fmt.Errorf("do not combine --all with a specific server")
		}
		return runUpstreamBulkAction("enable", upstreamForce)
	}
	serverName, err := resolveServerName(args, true)
	if err != nil {
		return err
	}
	return runUpstreamAction(serverName, "enable")
}

func runUpstreamDisable(cmd *cobra.Command, args []string) error {
	if upstreamAll {
		if upstreamServerName != "" || len(args) > 0 {
			return fmt.Errorf("do not combine --all with a specific server")
		}
		return runUpstreamBulkAction("disable", upstreamForce)
	}
	serverName, err := resolveServerName(args, true)
	if err != nil {
		return err
	}
	return runUpstreamAction(serverName, "disable")
}

func runUpstreamRestart(cmd *cobra.Command, args []string) error {
	if upstreamAll {
		if upstreamServerName != "" || len(args) > 0 {
			return fmt.Errorf("do not combine --all with a specific server")
		}
		return runUpstreamBulkAction("restart", false) // restart doesn't need confirmation
	}
	serverName, err := resolveServerName(args, true)
	if err != nil {
		return err
	}
	return runUpstreamAction(serverName, "restart")
}

func resolveServerName(args []string, allowAll bool) (string, error) {
	// Prefer --server, but allow positional for parity with other commands
	if upstreamServerName != "" && len(args) > 0 {
		if args[0] != upstreamServerName {
			return "", fmt.Errorf("specify server once, either as positional or with --server")
		}
	}

	if upstreamServerName != "" {
		return upstreamServerName, nil
	}

	if len(args) > 0 {
		return args[0], nil
	}

	if allowAll {
		return "", fmt.Errorf("server name required (or use --all)")
	}

	return "", fmt.Errorf("server name required")
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
	// Create context with correlation ID and request source tracking
	ctx := reqcontext.WithMetadata(context.Background(), reqcontext.SourceCLI)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
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

// T081-T082: Updated to use new bulk operation endpoints
func runUpstreamBulkAction(action string, force bool) error {
	// Create context with correlation ID and request source tracking
	ctx := reqcontext.WithMetadata(context.Background(), reqcontext.SourceCLI)
	// Use a longer parent context (2 minutes) to allow multiple operations
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
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

	// Get server count for confirmation
	servers, err := client.GetServers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get server list: %w", err)
	}

	if len(servers) == 0 {
		fmt.Printf("⚠️  No servers configured\n")
		return nil
	}

	// Require confirmation for enable/disable --all
	if action == "enable" || action == "disable" {
		confirmed, err := confirmBulkAction(action, len(servers), force)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println("Operation cancelled")
			return nil
		}
	}

	// Call appropriate bulk operation endpoint
	var result *cliclient.BulkOperationResult
	switch action {
	case "restart":
		result, err = client.RestartAll(ctx)
	case "enable":
		result, err = client.EnableAll(ctx)
	case "disable":
		result, err = client.DisableAll(ctx)
	default:
		return fmt.Errorf("unknown bulk action: %s", action)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Bulk %s failed: %v\n", action, err)
		return err
	}

	// Display results
	actionTitle := action
	if len(action) > 0 {
		actionTitle = strings.ToUpper(action[:1]) + action[1:]
	}
	fmt.Printf("\n%s Operation Results:\n", actionTitle)
	fmt.Printf("  Total servers:      %d\n", result.Total)
	fmt.Printf("  ✅ Successful:      %d\n", result.Successful)
	fmt.Printf("  ❌ Failed:          %d\n", result.Failed)

	// Show errors if any
	if len(result.Errors) > 0 {
		fmt.Printf("\nErrors:\n")
		for serverName, errMsg := range result.Errors {
			fmt.Printf("  • %s: %s\n", serverName, errMsg)
		}
	}

	// Return error if any servers failed
	if result.Failed > 0 {
		return fmt.Errorf("%d server(s) failed to %s", result.Failed, action)
	}

	return nil
}

// runUpstreamAdd handles the 'upstream add' command
func runUpstreamAdd(cmd *cobra.Command, args []string) error {
	// Parse command-line arguments
	// Usage: add <name> [url] [-- command args...]
	if len(args) < 1 {
		return fmt.Errorf("server name is required")
	}

	serverName := args[0]

	// Validate server name (alphanumeric, hyphens, underscores, 1-64 chars)
	if err := validateServerName(serverName); err != nil {
		return err
	}

	// Check for -- separator to detect stdio mode
	var url string
	var stdioCmd []string
	dashDashIndex := cmd.ArgsLenAtDash()

	if dashDashIndex >= 0 {
		// Stdio mode: args before -- are name (and optionally url), args after are command
		preArgs := args[:dashDashIndex]
		stdioCmd = args[dashDashIndex:]

		if len(preArgs) > 1 {
			url = preArgs[1] // URL provided before --
		}
		if len(stdioCmd) == 0 {
			return fmt.Errorf("command required after '--'")
		}
	} else {
		// HTTP mode or auto-detect
		if len(args) > 1 {
			url = args[1]
		}
	}

	// Determine transport type
	transport := upstreamAddTransport
	if transport == "" {
		if len(stdioCmd) > 0 {
			transport = "stdio"
		} else if url != "" {
			transport = "streamable-http"
		}
	}

	// Validate based on transport
	if transport == "stdio" && len(stdioCmd) == 0 {
		return fmt.Errorf("command required for stdio transport (use -- to separate)")
	}
	if (transport == "http" || transport == "streamable-http") && url == "" {
		return fmt.Errorf("URL required for http transport")
	}

	// Parse headers (for HTTP)
	headers := make(map[string]string)
	for _, h := range upstreamAddHeaders {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid header format: %s (expected 'Name: value')", h)
		}
		headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	// Parse environment variables (for stdio)
	env := make(map[string]string)
	for _, e := range upstreamAddEnvs {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid env format: %s (expected 'KEY=value')", e)
		}
		env[parts[0]] = parts[1]
	}

	// Build the request
	req := &cliclient.AddServerRequest{
		Name:       serverName,
		URL:        url,
		Headers:    headers,
		Env:        env,
		WorkingDir: upstreamAddWorkingDir,
		Protocol:   transport,
	}

	// Set quarantine based on --no-quarantine flag
	if upstreamAddNoQuarantine {
		quarantined := false
		req.Quarantined = &quarantined
	}

	// For stdio, extract command and args
	if len(stdioCmd) > 0 {
		req.Command = stdioCmd[0]
		if len(stdioCmd) > 1 {
			req.Args = stdioCmd[1:]
		}
	}

	// Create context
	ctx := reqcontext.WithMetadata(context.Background(), reqcontext.SourceCLI)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Load configuration to get data dir
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		return outputError(output.NewStructuredError(output.ErrCodeConfigNotFound, err.Error()).
			WithGuidance("Check that your config file exists and is valid").
			WithRecoveryCommand("mcpproxy doctor"), output.ErrCodeConfigNotFound)
	}

	// Check if daemon is running
	if shouldUseUpstreamDaemon(globalConfig.DataDir) {
		return runUpstreamAddDaemonMode(ctx, globalConfig.DataDir, req)
	}

	// Direct config file mode
	return runUpstreamAddConfigMode(req, globalConfig)
}

func runUpstreamAddDaemonMode(ctx context.Context, dataDir string, req *cliclient.AddServerRequest) error {
	socketPath := socket.DetectSocketPath(dataDir)
	client := cliclient.NewClient(socketPath, nil)

	result, err := client.AddServer(ctx, req)
	if err != nil {
		// Check if it's "already exists" error and --if-not-exists is set
		if upstreamAddIfNotExists && strings.Contains(err.Error(), "already exists") {
			fmt.Printf("Server '%s' already exists (skipped)\n", req.Name)
			return nil
		}
		return outputError(output.NewStructuredError(output.ErrCodeOperationFailed, err.Error()).
			WithGuidance("Check the server name and configuration"), output.ErrCodeOperationFailed)
	}

	// Output success
	outputFormat := ResolveOutputFormat()
	if outputFormat == "json" || outputFormat == "yaml" {
		formatter, _ := GetOutputFormatter()
		output, _ := formatter.Format(result)
		fmt.Println(output)
	} else {
		fmt.Printf("✅ Added server '%s'\n", req.Name)
		if result != nil && result.Quarantined {
			fmt.Println("   ⚠️  New servers are quarantined by default. Approve in the web UI.")
		}
	}

	return nil
}

func runUpstreamAddConfigMode(req *cliclient.AddServerRequest, globalConfig *config.Config) error {
	// Check if server already exists
	for _, srv := range globalConfig.Servers {
		if srv.Name == req.Name {
			if upstreamAddIfNotExists {
				fmt.Printf("Server '%s' already exists (skipped)\n", req.Name)
				return nil
			}
			return fmt.Errorf("server '%s' already exists", req.Name)
		}
	}

	// Determine quarantine status
	quarantined := true // Default: quarantine new servers
	if req.Quarantined != nil {
		quarantined = *req.Quarantined
	}

	// Create new server config
	newServer := &config.ServerConfig{
		Name:        req.Name,
		URL:         req.URL,
		Command:     req.Command,
		Args:        req.Args,
		Env:         req.Env,
		Headers:     req.Headers,
		WorkingDir:  req.WorkingDir,
		Protocol:    req.Protocol,
		Enabled:     true,
		Quarantined: quarantined,
	}

	// Add to config
	globalConfig.Servers = append(globalConfig.Servers, newServer)

	// Save config
	configPath := config.GetConfigPath(globalConfig.DataDir)
	if err := config.SaveConfig(globalConfig, configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// Output success
	fmt.Printf("✅ Added server '%s' to config\n", req.Name)
	if quarantined {
		fmt.Println("   ⚠️  New servers are quarantined by default. Start the daemon and approve in the web UI.")
	}

	return nil
}

// runUpstreamRemove handles the 'upstream remove' command
func runUpstreamRemove(cmd *cobra.Command, args []string) error {
	serverName := args[0]

	// Create context
	ctx := reqcontext.WithMetadata(context.Background(), reqcontext.SourceCLI)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		return outputError(output.NewStructuredError(output.ErrCodeConfigNotFound, err.Error()).
			WithGuidance("Check that your config file exists and is valid").
			WithRecoveryCommand("mcpproxy doctor"), output.ErrCodeConfigNotFound)
	}

	// Prompt for confirmation if not --yes
	if !upstreamRemoveYes {
		confirmed, err := promptConfirmation(fmt.Sprintf("Remove server '%s'?", serverName))
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println("Operation cancelled")
			return nil
		}
	}

	// Check if daemon is running
	if shouldUseUpstreamDaemon(globalConfig.DataDir) {
		return runUpstreamRemoveDaemonMode(ctx, globalConfig.DataDir, serverName)
	}

	// Direct config file mode
	return runUpstreamRemoveConfigMode(serverName, globalConfig)
}

func runUpstreamRemoveDaemonMode(ctx context.Context, dataDir, serverName string) error {
	socketPath := socket.DetectSocketPath(dataDir)
	client := cliclient.NewClient(socketPath, nil)

	err := client.RemoveServer(ctx, serverName)
	if err != nil {
		// Check if it's "not found" error and --if-exists is set
		if upstreamRemoveIfExists && strings.Contains(err.Error(), "not found") {
			fmt.Printf("Server '%s' not found (skipped)\n", serverName)
			return nil
		}
		return outputError(output.NewStructuredError(output.ErrCodeOperationFailed, err.Error()).
			WithGuidance("Check the server name"), output.ErrCodeOperationFailed)
	}

	// Output success
	outputFormat := ResolveOutputFormat()
	if outputFormat == "json" || outputFormat == "yaml" {
		formatter, _ := GetOutputFormatter()
		output, _ := formatter.Format(map[string]interface{}{
			"name":    serverName,
			"removed": true,
		})
		fmt.Println(output)
	} else {
		fmt.Printf("✅ Removed server '%s'\n", serverName)
	}

	return nil
}

func runUpstreamRemoveConfigMode(serverName string, globalConfig *config.Config) error {
	// Find and remove server
	found := false
	newServers := make([]*config.ServerConfig, 0, len(globalConfig.Servers))
	for _, srv := range globalConfig.Servers {
		if srv.Name == serverName {
			found = true
			continue
		}
		newServers = append(newServers, srv)
	}

	if !found {
		if upstreamRemoveIfExists {
			fmt.Printf("Server '%s' not found (skipped)\n", serverName)
			return nil
		}
		return fmt.Errorf("server '%s' not found", serverName)
	}

	// Update config
	globalConfig.Servers = newServers

	// Save config
	configPath := config.GetConfigPath(globalConfig.DataDir)
	if err := config.SaveConfig(globalConfig, configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("✅ Removed server '%s' from config\n", serverName)
	return nil
}

// runUpstreamAddJSON handles the 'upstream add-json' command
func runUpstreamAddJSON(cmd *cobra.Command, args []string) error {
	serverName := args[0]
	jsonStr := args[1]

	// Validate server name
	if err := validateServerName(serverName); err != nil {
		return err
	}

	// Parse JSON
	var jsonConfig struct {
		URL        string            `json:"url"`
		Command    string            `json:"command"`
		Args       []string          `json:"args"`
		Env        map[string]string `json:"env"`
		Headers    map[string]string `json:"headers"`
		WorkingDir string            `json:"working_dir"`
		Protocol   string            `json:"protocol"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &jsonConfig); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	// Auto-detect protocol
	protocol := jsonConfig.Protocol
	if protocol == "" {
		if jsonConfig.Command != "" {
			protocol = "stdio"
		} else if jsonConfig.URL != "" {
			protocol = "streamable-http"
		}
	}

	// Validate
	if jsonConfig.URL == "" && jsonConfig.Command == "" {
		return fmt.Errorf("JSON must contain either 'url' or 'command'")
	}

	// Build the request
	req := &cliclient.AddServerRequest{
		Name:       serverName,
		URL:        jsonConfig.URL,
		Command:    jsonConfig.Command,
		Args:       jsonConfig.Args,
		Headers:    jsonConfig.Headers,
		Env:        jsonConfig.Env,
		WorkingDir: jsonConfig.WorkingDir,
		Protocol:   protocol,
	}

	// Create context
	ctx := reqcontext.WithMetadata(context.Background(), reqcontext.SourceCLI)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		return outputError(output.NewStructuredError(output.ErrCodeConfigNotFound, err.Error()).
			WithGuidance("Check that your config file exists and is valid").
			WithRecoveryCommand("mcpproxy doctor"), output.ErrCodeConfigNotFound)
	}

	// Check if daemon is running
	if shouldUseUpstreamDaemon(globalConfig.DataDir) {
		return runUpstreamAddDaemonMode(ctx, globalConfig.DataDir, req)
	}

	// Direct config file mode
	return runUpstreamAddConfigMode(req, globalConfig)
}

// validateServerName validates server name format (alphanumeric, hyphens, underscores, 1-64 chars)
func validateServerName(name string) error {
	if len(name) == 0 {
		return fmt.Errorf("server name cannot be empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("server name too long (max 64 characters)")
	}

	for i, c := range name {
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		isAllowed := c == '-' || c == '_'
		if !isAlphaNum && !isAllowed {
			return fmt.Errorf("invalid character '%c' at position %d in server name (allowed: a-z, A-Z, 0-9, -, _)", c, i)
		}
	}

	return nil
}

// promptConfirmation prompts the user for yes/no confirmation
func promptConfirmation(message string) (bool, error) {
	fmt.Printf("%s [y/N]: ", message)
	var response string
	_, err := fmt.Scanln(&response)
	if err != nil {
		// If EOF or empty, treat as "no"
		return false, nil
	}

	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes", nil
}
