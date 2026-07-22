package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	toolsCmd = &cobra.Command{
		Use:   "tools",
		Short: "Tools management commands",
		Long:  "Commands for managing and debugging MCP tools from upstream servers",
	}

	toolsListCmd = &cobra.Command{
		Use:   "list",
		Short: "List tools from upstream servers",
		Long: `List all available tools. Without --server, lists every tool across all
configured servers from the running daemon (global view). With --server,
lists tools from that specific server only.

Examples:
  mcpproxy tools list                            # global list, all servers
  mcpproxy tools list -o json                    # JSON output
  mcpproxy tools list --status disabled          # only disabled/config-denied
  mcpproxy tools list --risk read                # read-only tools
  mcpproxy tools list --approval pending         # tools pending approval
  mcpproxy tools list --server=github-server     # server-scoped (debug mode)
  mcpproxy tools list --server=github-server --log-level=trace`,
		RunE: runToolsList,
	}

	toolsEnableCmd = &cobra.Command{
		Use:   "enable <server:tool> [<server:tool>...]",
		Short: "Enable one or more tools",
		Long: `Enable one or more tools by their server:tool identifier.

Multiple targets are processed independently. If any target fails, the
command exits non-zero but all other targets are still attempted.

Examples:
  mcpproxy tools enable github:create_issue
  mcpproxy tools enable github:create_issue github:list_repos`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolsSetEnabled(args, true)
		},
	}

	toolsDisableCmd = &cobra.Command{
		Use:   "disable <server:tool> [<server:tool>...]",
		Short: "Disable one or more tools",
		Long: `Disable one or more tools by their server:tool identifier.

Multiple targets are processed independently. If any target fails, the
command exits non-zero but all other targets are still attempted.

Examples:
  mcpproxy tools disable github:create_issue
  mcpproxy tools disable github:create_issue memory:foo`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolsSetEnabled(args, false)
		},
	}

	// Command flags
	serverName     string
	toolsLogLevel  string
	configPath     string
	timeout        time.Duration
	traceTransport bool // Enable HTTP/SSE frame-by-frame tracing

	// Global list filter flags (T019)
	toolsStatusFilter   string // enabled | disabled | config-denied
	toolsRiskFilter     string // read | write | destructive
	toolsApprovalFilter string // approved | pending | changed
)

// serverToolTarget holds a parsed server:tool pair.
type serverToolTarget struct {
	server string
	tool   string
}

// parseServerTool splits an arg of the form "server:tool" on the first ':'.
// The tool name itself may contain further colons.
func parseServerTool(arg string) (server, tool string, err error) {
	if arg == "" {
		return "", "", fmt.Errorf("invalid target %q: must be in <server>:<tool> format", arg)
	}
	idx := strings.Index(arg, ":")
	if idx < 0 {
		return "", "", fmt.Errorf("invalid target %q: missing ':' separator (use <server>:<tool>)", arg)
	}
	server = arg[:idx]
	tool = arg[idx+1:]
	if server == "" {
		return "", "", fmt.Errorf("invalid target %q: server name is empty", arg)
	}
	if tool == "" {
		return "", "", fmt.Errorf("invalid target %q: tool name is empty", arg)
	}
	return server, tool, nil
}

// groupByServer groups targets by their server name.
func groupByServer(targets []serverToolTarget) map[string][]string {
	groups := make(map[string][]string)
	for _, t := range targets {
		groups[t.server] = append(groups[t.server], t.tool)
	}
	return groups
}

// applyGlobalToolFilters applies client-side filters to the global tool list.
// statusFilter: "enabled" | "disabled" | "config-denied" | ""
// riskFilter:   "read" | "write" | "destructive" | ""
// approvalFilter: "approved" | "pending" | "changed" | ""
func applyGlobalToolFilters(tools []map[string]interface{}, statusFilter, riskFilter, approvalFilter string) []map[string]interface{} {
	if statusFilter == "" && riskFilter == "" && approvalFilter == "" {
		return tools
	}

	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		if statusFilter != "" {
			disabled := getBoolField(t, "disabled")
			configDenied := getBoolField(t, "config_denied")
			isDisabled := disabled || configDenied
			switch statusFilter {
			case "enabled":
				if isDisabled {
					continue
				}
			case "disabled":
				if !isDisabled {
					continue
				}
			case "config-denied":
				if !configDenied {
					continue
				}
			}
		}

		if riskFilter != "" {
			opType := ""
			if ann, ok := t["annotations"].(map[string]interface{}); ok {
				opType, _ = ann["operation_type"].(string)
			}
			if !strings.EqualFold(opType, riskFilter) {
				continue
			}
		}

		if approvalFilter != "" {
			status := getStringField(t, "approval_status")
			if !strings.EqualFold(status, approvalFilter) {
				continue
			}
		}

		out = append(out, t)
	}
	return out
}

// GetToolsCommand returns the tools command for adding to the root command
func GetToolsCommand() *cobra.Command {
	return toolsCmd
}

func init() {
	// toolsCmd will be added to rootCmd in main.go
	toolsCmd.AddCommand(toolsListCmd)
	toolsCmd.AddCommand(toolsEnableCmd)
	toolsCmd.AddCommand(toolsDisableCmd)
	toolsCmd.AddCommand(newToolsApproveCmd())
	toolsCmd.AddCommand(newToolsRejectCmd())

	initToolsFlags()
}

// initToolsFlags registers flags on toolsListCmd. Extracted so tests can call
// it independently of init().
func initToolsFlags() {
	// Define flags for tools list command — reset to avoid double-registration
	// in tests that call this function more than once.
	toolsListCmd.ResetFlags()

	toolsListCmd.Flags().StringVarP(&serverName, "server", "s", "", "Name of the upstream server to query (optional; omit for global list)")
	toolsListCmd.Flags().StringVarP(&toolsLogLevel, "log-level", "l", "info", "Log level (trace, debug, info, warn, error)")
	toolsListCmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")
	toolsListCmd.Flags().DurationVarP(&timeout, "timeout", "t", 30*time.Second, "Connection timeout")
	toolsListCmd.Flags().BoolVar(&traceTransport, "trace-transport", false, "Enable detailed HTTP/SSE frame-by-frame tracing")

	// Global-list filter flags (T019)
	toolsListCmd.Flags().StringVar(&toolsStatusFilter, "status", "", "Filter by state: enabled, disabled, config-denied")
	toolsListCmd.Flags().StringVar(&toolsRiskFilter, "risk", "", "Filter by risk: read, write, destructive")
	toolsListCmd.Flags().StringVar(&toolsApprovalFilter, "approval", "", "Filter by approval: approved, pending, changed")

	// Note: -o/--output flag is inherited from root command via globalOutputFormat
	// Note: --server is NOT marked required — global list works without it.

	toolsListCmd.Example = `  # Global list (all servers) — requires daemon
  mcpproxy tools list
  mcpproxy tools list -o json | jq '.[0]'
  mcpproxy tools list --status disabled

  # Server-scoped list (standalone or daemon)
  mcpproxy tools list --server=github-server --log-level=trace

  # Use custom config file
  mcpproxy tools list --server=local-script --config=/path/to/config.json

  # Set custom timeout
  mcpproxy tools list --server=slow-server --timeout=60s`
}

func runToolsList(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Enable transport tracing if requested
	if traceTransport {
		transport.GlobalTraceEnabled = true
		fmt.Println("HTTP/SSE TRANSPORT TRACING ENABLED")
		fmt.Println("   All HTTP requests/responses and SSE frames will be logged")
		fmt.Println()
	}

	// Load configuration
	globalConfig, err := loadToolsConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Create logger
	logger, err := logs.SetupCommandLogger(false, toolsLogLevel, false, "")
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	// If no --server given → global list (requires daemon)
	if serverName == "" {
		return runToolsListGlobal(ctx, globalConfig, logger)
	}

	// --server given → server-scoped path (daemon or standalone)
	if client, ok := newDaemonClient(globalConfig, logger.Sugar()); ok {
		logger.Info("Detected running daemon, using client mode",
			zap.String("server", serverName))
		return runToolsListClientMode(ctx, client, serverName, logger)
	}

	// No daemon detected, use standalone mode
	logger.Info("No daemon detected, using standalone mode",
		zap.String("server", serverName))
	return runToolsListStandalone(ctx, serverName, globalConfig, logger)
}

// runToolsListGlobal fetches all tools from the global endpoint via the daemon.
func runToolsListGlobal(ctx context.Context, globalConfig *config.Config, logger *zap.Logger) error {
	client, ok := newDaemonClient(globalConfig, logger.Sugar())
	if !ok {
		return fmt.Errorf("global tool list requires the daemon to be running.\n" +
			"Start mcpproxy (mcpproxy serve) and try again, or use --server=<name> for a single-server debug listing")
	}

	pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx); err != nil {
		return fmt.Errorf("daemon is not responding: %w\n"+
			"Start mcpproxy (mcpproxy serve) and try again", err)
	}

	fmt.Fprintf(os.Stderr, "Using daemon mode\n\n")

	tools, err := client.GetGlobalTools(ctx)
	if err != nil {
		return cliError("failed to get global tools from daemon", err)
	}

	// Apply client-side filters
	tools = applyGlobalToolFilters(tools, toolsStatusFilter, toolsRiskFilter, toolsApprovalFilter)

	return outputGlobalTools(tools)
}

// outputGlobalTools renders the global tool list with extended columns.
func outputGlobalTools(tools []map[string]interface{}) error {
	outputFormat := ResolveOutputFormat()
	formatter, err := GetOutputFormatter()
	if err != nil {
		return output.NewStructuredError(output.ErrCodeInvalidOutputFormat, err.Error()).
			WithGuidance("Use -o table, -o json, or -o yaml")
	}

	// JSON / YAML: emit the raw slice
	if outputFormat == "json" || outputFormat == "yaml" {
		result, fmtErr := formatter.Format(tools)
		if fmtErr != nil {
			return fmt.Errorf("failed to format output: %w", fmtErr)
		}
		fmt.Println(result)
		return nil
	}

	// Table format with extended columns for global view
	headers := []string{"NAME", "SERVER", "STATE", "APPROVAL", "USAGE", "LAST USED", "DESCRIPTION"}
	var rows [][]string
	for _, t := range tools {
		name := getStringField(t, "name")
		srv := getStringField(t, "server_name")
		disabled := getBoolField(t, "disabled")
		configDenied := getBoolField(t, "config_denied")

		state := "enabled"
		if configDenied {
			state = "config-denied"
		} else if disabled {
			state = "disabled"
		}

		approval := getStringField(t, "approval_status")
		if approval == "" {
			approval = "-"
		}

		usageVal := getIntField(t, "usage")
		usage := fmt.Sprintf("%d", usageVal)

		lastUsed := "-"
		if lu := getStringField(t, "last_used"); lu != "" {
			lastUsed = lu
		}

		desc := getStringField(t, "description")
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}

		rows = append(rows, []string{name, srv, state, approval, usage, lastUsed, desc})
	}

	result, fmtErr := formatter.FormatTable(headers, rows)
	if fmtErr != nil {
		return fmt.Errorf("failed to format table: %w", fmtErr)
	}
	fmt.Print(result)
	return nil
}

// runToolsSetEnabled implements the enable/disable subcommands.
// It parses each arg as server:tool, groups by server, calls the per-tool
// endpoint, prints per-target results, and exits non-zero if any failed.
func runToolsSetEnabled(args []string, enabled bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Load config to find the data dir / socket path
	globalConfig, err := loadToolsConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	logger, err := logs.SetupCommandLogger(false, "warn", false, "")
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	client, ok := newDaemonClient(globalConfig, logger.Sugar())
	if !ok {
		return fmt.Errorf("enable/disable requires the daemon to be running.\n" +
			"Start mcpproxy (mcpproxy serve) and try again")
	}

	pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx); err != nil {
		return fmt.Errorf("daemon is not responding: %w", err)
	}

	// Parse all targets; collect parse errors as per-target failures
	type result struct {
		arg string
		err error
	}
	var validTargets []serverToolTarget
	var results []result

	for _, arg := range args {
		srv, tool, parseErr := parseServerTool(arg)
		if parseErr != nil {
			results = append(results, result{arg: arg, err: parseErr})
			continue
		}
		validTargets = append(validTargets, serverToolTarget{server: srv, tool: tool})
	}

	// Call per-tool endpoint for each valid target
	action := "enabled"
	if !enabled {
		action = "disabled"
	}

	for _, target := range validTargets {
		callErr := client.SetToolEnabled(ctx, target.server, target.tool, enabled)
		results = append(results, result{arg: target.server + ":" + target.tool, err: callErr})
	}

	// Print per-target summary
	anyFailed := false
	for _, r := range results {
		if r.err != nil {
			anyFailed = true
			fmt.Fprintf(os.Stderr, "FAILED  %s: %s\n", r.arg, r.err.Error())
		} else {
			fmt.Printf("OK      %s: %s\n", r.arg, action)
		}
	}

	if anyFailed {
		return fmt.Errorf("one or more targets failed (see above)")
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

	// Respect global --data-dir flag
	if dataDir != "" {
		globalConfig.DataDir = dataDir
	}

	return globalConfig, nil
}

// getAvailableServerNames returns a list of available server names
func getAvailableServerNames(globalConfig *config.Config) []string {
	var names []string
	for _, server := range globalConfig.Servers {
		names = append(names, server.Name)
	}
	return names
}

// outputToolsFromMetadata formats and displays tools from ToolMetadata (standalone mode) using unified formatters.
func outputToolsFromMetadata(tools []*config.ToolMetadata, serverName string) error {
	// Convert to map format for unified output
	toolMaps := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		toolMaps[i] = map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"server":      serverName,
			"full_name":   fmt.Sprintf("%s:%s", serverName, tool.Name),
		}
		// Include schema in debug/trace mode
		if (toolsLogLevel == "debug" || toolsLogLevel == "trace") && tool.ParamsJSON != "" {
			toolMaps[i]["schema"] = tool.ParamsJSON
		}
	}

	outputFormat := ResolveOutputFormat()
	formatter, err := GetOutputFormatter()
	if err != nil {
		return output.NewStructuredError(output.ErrCodeInvalidOutputFormat, err.Error()).
			WithGuidance("Use -o table, -o json, or -o yaml")
	}

	// For JSON/YAML, format directly
	if outputFormat == "json" || outputFormat == "yaml" {
		result, fmtErr := formatter.Format(toolMaps)
		if fmtErr != nil {
			return fmt.Errorf("failed to format output: %w", fmtErr)
		}
		fmt.Println(result)
		return nil
	}

	// Table format: show name and description
	headers := []string{"NAME", "DESCRIPTION"}
	var rows [][]string
	for _, tool := range tools {
		rows = append(rows, []string{tool.Name, tool.Description})
	}

	result, fmtErr := formatter.FormatTable(headers, rows)
	if fmtErr != nil {
		return fmt.Errorf("failed to format table: %w", fmtErr)
	}
	fmt.Print(result)
	return nil
}

// runToolsListClientMode executes tools list via the daemon HTTP API.
func runToolsListClientMode(ctx context.Context, client *cliclient.Client, serverName string, logger *zap.Logger) error {
	// Ping daemon to verify connectivity
	pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx); err != nil {
		logger.Warn("Failed to ping daemon, falling back to standalone mode",
			zap.Error(err))
		// Fall back to standalone mode
		cfg, err := loadToolsConfig()
		if err != nil {
			return fmt.Errorf("failed to load config for standalone mode: %w", err)
		}
		return runToolsListStandalone(ctx, serverName, cfg, logger)
	}

	fmt.Fprintf(os.Stderr, "Using daemon mode - fast execution\n\n")

	// Fetch tools from daemon
	tools, err := client.GetServerTools(ctx, serverName)
	if err != nil {
		// T027: Use cliError to include request_id in error output
		return cliError("failed to get server tools from daemon", err)
	}

	// Output results
	return outputTools(tools, logger)
}

// outputTools formats and displays tools based on output format using unified formatters.
func outputTools(tools []map[string]interface{}, _ *zap.Logger) error {
	outputFormat := ResolveOutputFormat()
	formatter, err := GetOutputFormatter()
	if err != nil {
		return output.NewStructuredError(output.ErrCodeInvalidOutputFormat, err.Error()).
			WithGuidance("Use -o table, -o json, or -o yaml")
	}

	// For JSON/YAML, format directly
	if outputFormat == "json" || outputFormat == "yaml" {
		result, fmtErr := formatter.Format(tools)
		if fmtErr != nil {
			return fmt.Errorf("failed to format output: %w", fmtErr)
		}
		fmt.Println(result)
		return nil
	}

	// Table format: show name and description
	headers := []string{"NAME", "DESCRIPTION"}
	var rows [][]string
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		desc, _ := tool["description"].(string)
		rows = append(rows, []string{name, desc})
	}

	result, fmtErr := formatter.FormatTable(headers, rows)
	if fmtErr != nil {
		return fmt.Errorf("failed to format table: %w", fmtErr)
	}
	fmt.Print(result)
	return nil
}

// runToolsListStandalone executes tools list in standalone mode (original behavior).
func runToolsListStandalone(ctx context.Context, serverName string, globalConfig *config.Config, logger *zap.Logger) error {
	// Find server config
	var serverConfig *config.ServerConfig
	for _, server := range globalConfig.Servers {
		if server.Name == serverName {
			serverConfig = server
			break
		}
	}
	if serverConfig == nil {
		return fmt.Errorf("server '%s' not found in configuration. Available servers: %v",
			serverName, getAvailableServerNames(globalConfig))
	}

	fmt.Printf("MCP Tools List - Server: %s\n", serverName)
	fmt.Printf("Log Level: %s\n", toolsLogLevel)
	fmt.Printf("Timeout: %v\n", timeout)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Create storage (optional, for OAuth persistence)
	var db *storage.BoltDB
	if globalConfig.DataDir != "" {
		boltDB, err := storage.NewBoltDB(globalConfig.DataDir, logger.Sugar())
		if err != nil {
			logger.Warn("Failed to create storage, OAuth will use in-memory")
		} else {
			db = boltDB
			defer db.Close()
		}
	}

	// Create secret resolver
	secretResolver := secret.NewResolver()

	// Create log config for managed client
	logConfig := &config.LogConfig{
		Level:         toolsLogLevel,
		EnableConsole: true,
		EnableFile:    false,
		JSONFormat:    false,
	}

	// Create managed client (same as serve mode!)
	managedClient, err := managed.NewClient(serverName, serverConfig, logger, logConfig, globalConfig, db, secretResolver)
	if err != nil {
		return fmt.Errorf("failed to create managed client: %w", err)
	}

	// Connect to server
	fmt.Printf("Connecting to server '%s'...\n", serverName)
	if err := managedClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to server '%s': %w", serverName, err)
	}

	// Ensure cleanup on exit
	defer func() {
		fmt.Printf("Disconnecting from server...\n")
		if disconnectErr := managedClient.Disconnect(); disconnectErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to disconnect cleanly: %v\n", disconnectErr)
		}
	}()

	// List tools
	tools, err := managedClient.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list tools: %w", err)
	}

	// Output results using unified formatter
	if len(tools) == 0 {
		outputFormat := ResolveOutputFormat()
		if outputFormat == "table" {
			fmt.Printf("No tools found on server '%s'\n", serverName)
			fmt.Printf("This could indicate:\n")
			fmt.Printf("   Server doesn't support tools\n")
			fmt.Printf("   Server is not properly configured\n")
			fmt.Printf("   Connection issues during tool discovery\n")
			return nil
		}
		// For JSON/YAML, output empty array
	}

	return outputToolsFromMetadata(tools, serverName)
}
