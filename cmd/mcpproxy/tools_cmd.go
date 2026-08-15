package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
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
	toolsCmd.AddCommand(newToolsPreflightCmd())

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
		fmt.Fprintln(os.Stderr, "HTTP/SSE TRANSPORT TRACING ENABLED")
		fmt.Fprintln(os.Stderr, "   All HTTP requests/responses and SSE frames will be logged")
		fmt.Fprintln(os.Stderr)
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

// tpaSignatureRe matches a TPA signature id embedded in a deterministic check id
// (e.g. "tpa.TPA-2026-0001.hidden_instruction" → "TPA-2026-0001").
var tpaSignatureRe = regexp.MustCompile(`TPA-\d{4}-\d{4}`)

// maxHeldSignalsShown is how many hold signals the HELD column renders before
// collapsing the rest into a "+N" suffix.
const maxHeldSignalsShown = 2

// formatToolHold renders the scan-gate hold evidence for the HELD column
// (spec 086 FR-018): the matched TPA signature ids when the bundle fired,
// otherwise the raw check ids (e.g. "phrase.injection"). Returns "-" for tools
// that are not held by the scan gate, so records predating the field render
// exactly as before.
func formatToolHold(t map[string]interface{}) string {
	// Collect matched TPA signature ids and the remaining raw check ids
	// separately, preserving producer order within each group. TPA ids are
	// rendered first so the "+N" truncation never hides them — the scanner
	// emits its heuristic checks (directive.imperative, capability.mismatch, …)
	// ahead of the tpa.* checks, and FR-018 requires the operator-facing view
	// to name the matched TPA-YYYY-NNNN id(s).
	var tpaLabels, otherLabels []string
	seen := make(map[string]bool)
	for _, raw := range getArrayField(t, "held_signals") {
		signal, ok := raw.(string)
		if !ok || signal == "" {
			continue
		}
		label := signal
		isTPA := false
		if m := tpaSignatureRe.FindString(signal); m != "" {
			label = m
			isTPA = true
		}
		if seen[label] {
			continue
		}
		seen[label] = true
		if isTPA {
			tpaLabels = append(tpaLabels, label)
		} else {
			otherLabels = append(otherLabels, label)
		}
	}
	labels := append(tpaLabels, otherLabels...)

	if len(labels) == 0 {
		// A hold with no signals means the scan itself could not be trusted
		// (degraded coverage / missing bundle) — still worth naming.
		if reason := getStringField(t, "held_reason"); reason != "" {
			return reason
		}
		return "-"
	}

	shown := labels
	suffix := ""
	if len(labels) > maxHeldSignalsShown {
		shown = labels[:maxHeldSignalsShown]
		suffix = fmt.Sprintf(" +%d", len(labels)-maxHeldSignalsShown)
	}
	return strings.Join(shown, ",") + suffix
}

// sanitizeCell makes an upstream-controlled string safe to print in a terminal
// table and truncates it to maxRunes (GH #938 finding 3).
//
// Two bugs it fixes: the server-scoped tool list printed a poisoned description
// verbatim — ANSI escapes, bidi overrides and zero-width runes reached the tty
// unfiltered — and the global list truncated with a BYTE slice, which can split
// a multi-byte rune. detect.CapEvidence is the project-wide render-safe
// contract: it ESCAPES (never drops) control/format runes so smuggled content
// is revealed rather than hidden.
func sanitizeCell(s string, maxRunes int) string {
	escaped := detect.CapEvidence(s)
	runes := []rune(escaped)
	if maxRunes > 3 && len(runes) > maxRunes {
		return string(runes[:maxRunes-3]) + "..."
	}
	return escaped
}

// maxToolDescriptionCell is the description column width shared by the global
// and server-scoped tool tables.
const maxToolDescriptionCell = 60

// maxToolNameCell bounds the NAME column. Tool names are upstream-controlled
// just like descriptions — an unbounded one can push every other column off
// screen — so the same cap applies.
const maxToolNameCell = 60

// sanitizeName escapes an upstream-controlled tool name for terminal output.
//
// The description fix alone was bypassable: a server declaring a tool named
// "\x1b[2J\x1b[1;1Happroved" writes ANSI straight to the operator's tty on
// `mcpproxy tools list`, `tools list --server=<name>` and the no-daemon path —
// the same trust boundary, the same attack. Names are upstream-controlled, so
// they get the same render-safe treatment.
func sanitizeName(s string) string {
	return sanitizeCell(s, maxToolNameCell)
}

// serverToolRows builds the table for `mcpproxy tools list --server <name>`.
//
// GH #938 finding 3: the server-scoped view used to render only NAME and
// DESCRIPTION, so a tool held by the trust_mode:scan gate was indistinguishable
// from an approved one — the exact view an operator debugging ONE server opens.
// It now carries the same APPROVAL/HELD state as the global view (the
// per-server REST payload has always included those fields; only the renderer
// dropped them) and escapes the description.
func serverToolRows(tools []map[string]interface{}) (headers []string, rows [][]string) {
	headers = []string{"NAME", "APPROVAL", "HELD", "DESCRIPTION"}
	for _, t := range tools {
		approval := getStringField(t, "approval_status")
		if approval == "" {
			approval = "-"
		}
		rows = append(rows, []string{
			sanitizeName(getStringField(t, "name")),
			approval,
			formatToolHold(t),
			sanitizeCell(getStringField(t, "description"), maxToolDescriptionCell),
		})
	}
	return headers, rows
}

// globalToolRows builds the table for `mcpproxy tools list` (all servers).
// Split out of outputGlobalTools so the rendering — in particular the escaping
// of the two upstream-controlled columns, NAME and DESCRIPTION — is directly
// testable.
func globalToolRows(tools []map[string]interface{}) (headers []string, rows [][]string) {
	headers = []string{"NAME", "SERVER", "STATE", "APPROVAL", "HELD", "USAGE", "LAST USED", "DESCRIPTION"}
	for _, t := range tools {
		name := sanitizeName(getStringField(t, "name"))
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

		usage := fmt.Sprintf("%d", getIntField(t, "usage"))

		lastUsed := "-"
		if lu := getStringField(t, "last_used"); lu != "" {
			lastUsed = lu
		}

		desc := sanitizeCell(getStringField(t, "description"), maxToolDescriptionCell)

		rows = append(rows, []string{name, srv, state, approval, formatToolHold(t), usage, lastUsed, desc})
	}
	return headers, rows
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

	headers, rows := globalToolRows(tools)

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

// standaloneToolRows builds the no-daemon table. That path has no approval
// records, so it keeps the two-column shape — but BOTH upstream-controlled
// columns are sanitized (#938): a poisoned name or description must never reach
// the terminal raw on ANY path.
func standaloneToolRows(tools []*config.ToolMetadata) (headers []string, rows [][]string) {
	headers = []string{"NAME", "DESCRIPTION"}
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		rows = append(rows, []string{
			sanitizeName(tool.Name),
			sanitizeCell(tool.Description, maxToolDescriptionCell),
		})
	}
	return headers, rows
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

	headers, rows := standaloneToolRows(tools)

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

	// Table format: name + approval/hold state + escaped description (#938).
	headers, rows := serverToolRows(tools)

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

	// Human banner/progress goes to stderr so machine formats (-o json|yaml)
	// keep stdout parseable (see docs/cli-output-formatting.md).
	fmt.Fprintf(os.Stderr, "MCP Tools List - Server: %s\n", serverName)
	fmt.Fprintf(os.Stderr, "Log Level: %s\n", toolsLogLevel)
	fmt.Fprintf(os.Stderr, "Timeout: %v\n", timeout)
	fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

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
	fmt.Fprintf(os.Stderr, "Connecting to server '%s'...\n", serverName)
	if err := managedClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to server '%s': %w", serverName, err)
	}

	// Ensure cleanup on exit
	defer func() {
		fmt.Fprintf(os.Stderr, "Disconnecting from server...\n")
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

// --- Spec 098: tools preflight ----------------------------------------------

// newToolsPreflightCmd builds `mcpproxy tools preflight`, the cron/CI gate: one
// deterministic, side-effect-free check that answers "are these tools usable
// right now?" before a job spends model tokens finding out the hard way.
//
// The exit code is the product: 0 all ready, 10 retryable (back off), 11 an
// operator has to act, 12 an id does not exist. A wrapper can branch on it
// without parsing any JSON.
func newToolsPreflightCmd() *cobra.Command {
	var (
		profile            string
		pins               []string
		readOnlyOnly       bool
		excludeDestructive bool
		excludeOpenWorld   bool
		wait               time.Duration
	)

	cmd := &cobra.Command{
		Use:   "preflight <server:tool> [<server:tool>...]",
		Short: "Check that required tools are ready, without calling any upstream server",
		Long: `Check a list of required tools against local proxy state and report, per tool,
whether it is ready or exactly why it is not.

The check performs zero upstream calls and changes nothing: it reads the tool
index, approval records, connection state and configuration policy only.

Exit codes (worst class present wins):
  0   every tool is ready
  10  degraded but retryable (a server is starting up or unhealthy) — back off and retry
  11  blocked: an operator action is needed (approve, enable, log in, re-pin)
  12  at least one requested id is unknown in your view (typo, or removed server)
  1   the command itself failed (daemon unreachable, invalid arguments)

Examples:
  mcpproxy tools preflight gh-ops:sync_issues slack:post_message
  mcpproxy tools preflight ctl:echo -o json
  mcpproxy tools preflight ctl:echo --pin ctl:echo=sha256/v1:9f86d0...
  mcpproxy tools preflight ctl:echo --profile work --wait 5s
  mcpproxy tools preflight ctl:echo --read-only-only`,
		// Tool ids are required — except under --help-json, which is a
		// discovery call an agent makes before it knows what to pass. Cobra
		// validates Args before the --help-json hook runs, so a plain
		// MinimumNArgs(1) would make the command's own metadata unreachable.
		Args: preflightArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			request, err := buildPreflightRequest(args, pins, profile, wait,
				contracts.PreflightPolicy{
					ReadOnlyOnly:       readOnlyOnly,
					ExcludeDestructive: excludeDestructive,
					ExcludeOpenWorld:   excludeOpenWorld,
				})
			if err != nil {
				// Argument errors keep the usage block: the operator mistyped
				// the invocation and the syntax is the answer.
				return err
			}
			cmd.SilenceUsage = true
			// From here the command's return value is a VERDICT, not a usage
			// problem. Cobra would print it a second time on top of the central
			// handler's "Error: …" line, and a cron log with the same verdict
			// twice reads like two failures.
			cmd.SilenceErrors = true
			return runToolsPreflight(request)
		},
	}

	cmd.Flags().StringVar(&profile, "profile", "", "Evaluate under a named profile's server scope")
	cmd.Flags().StringArrayVar(&pins, "pin", nil, "Pin a tool to a schema hash: --pin <server:tool>=sha256/v<N>:<hex> (repeatable)")
	cmd.Flags().BoolVar(&readOnlyOnly, "read-only-only", false, "Require tools to be annotated read-only")
	cmd.Flags().BoolVar(&excludeDestructive, "exclude-destructive", false, "Require tools to be annotated non-destructive")
	cmd.Flags().BoolVar(&excludeOpenWorld, "exclude-open-world", false, "Require tools to be annotated closed-world")
	cmd.Flags().DurationVar(&wait, "wait", 0, "Poll local state for up to this long while every failure is retryable (max 10s)")

	return cmd
}

// preflightArgs requires at least one tool id, but lets `--help-json` through
// with none: that flag is answered by a PersistentPreRunE hook, which cobra
// runs AFTER argument validation, so a bare MinimumNArgs(1) would hide the
// command's machine-readable help from the agents it exists for.
func preflightArgs(cmd *cobra.Command, args []string) error {
	if helpJSON, err := cmd.Flags().GetBool("help-json"); err == nil && helpJSON {
		return nil
	}
	return cobra.MinimumNArgs(1)(cmd, args)
}

// buildPreflightRequest turns CLI arguments into the REST request body.
//
// It validates only what is genuinely local (pin syntax, pins naming an id that
// was not requested). Everything else — the 100-id cap, the wait cap, unknown
// profiles — is the daemon's rule, and duplicating it here would give the two
// surfaces two chances to disagree.
func buildPreflightRequest(ids, pins []string, profile string, wait time.Duration, policy contracts.PreflightPolicy) (*contracts.PreflightRequest, error) {
	pinByID, err := parsePreflightPins(pins)
	if err != nil {
		return nil, err
	}

	request := &contracts.PreflightRequest{
		Tools:   make([]contracts.PreflightToolRef, 0, len(ids)),
		Profile: strings.TrimSpace(profile),
		WaitMS:  int(wait.Milliseconds()),
	}
	if policy.ReadOnlyOnly || policy.ExcludeDestructive || policy.ExcludeOpenWorld {
		policyCopy := policy
		request.Policy = &policyCopy
	}

	requested := make(map[string]bool, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, fmt.Errorf("empty tool id in arguments (expected <server>:<tool>)")
		}
		requested[id] = true
		request.Tools = append(request.Tools, contracts.PreflightToolRef{ID: id, PinHash: pinByID[id]})
	}

	for id := range pinByID {
		if !requested[id] {
			return nil, fmt.Errorf("--pin names %q, which is not in the requested tool list", id)
		}
	}

	return request, nil
}

// parsePreflightPins parses repeatable `--pin <id>=<hash>` flags. The split is
// on the FIRST '=' because a pin value is "sha256/v1:<hex>" — it carries ':'
// and '/', but never '=' — while the id carries ':'.
func parsePreflightPins(pins []string) (map[string]string, error) {
	out := make(map[string]string, len(pins))
	for _, pin := range pins {
		idx := strings.Index(pin, "=")
		if idx <= 0 {
			return nil, fmt.Errorf("invalid --pin %q: expected <server>:<tool>=<hash>", pin)
		}
		id := strings.TrimSpace(pin[:idx])
		hash := strings.TrimSpace(pin[idx+1:])
		if id == "" || hash == "" {
			return nil, fmt.Errorf("invalid --pin %q: expected <server>:<tool>=<hash>", pin)
		}
		if existing, ok := out[id]; ok && existing != hash {
			return nil, fmt.Errorf("conflicting --pin values for %q: %q and %q", id, existing, hash)
		}
		out[id] = hash
	}
	return out, nil
}

// runToolsPreflight calls the daemon, renders the result, and converts a
// non-ready verdict into the typed exit-code error.
func runToolsPreflight(request *contracts.PreflightRequest) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	// The request's own wait budget is capped at 10s daemon-side; the transport
	// deadline just has to outlive it.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	response, err := client.Preflight(ctx, request)
	if err != nil {
		return cliError("preflight failed", err)
	}

	rendered, err := renderPreflight(ResolveOutputFormat(), response)
	if err != nil {
		return err
	}
	fmt.Print(rendered)

	// The verdict is not a command failure — it is the answer. It travels as a
	// typed error purely so the CENTRAL classifier assigns 10/11/12.
	return newPreflightVerdictError(preflightExitVerdict(response), preflightSummary(response))
}

// preflightExitVerdict is the verdict the exit code comes from: the worse of
// what the daemon reported and what the per-tool results imply.
//
// Recomputing locally is deliberate. The exit code is the contract a cron
// wrapper trusts, and taking the max of both readings means an older daemon
// that under-reports the set verdict can never make a blocked tool look like a
// clean run. Both readings use the same locked table (preflight.ExitCode), so
// "worse" is just the higher exit code.
func preflightExitVerdict(response *contracts.PreflightResponse) string {
	if response == nil {
		return preflight.VerdictReady
	}
	reasons := make([]string, 0, len(response.Tools))
	for _, tool := range response.Tools {
		if tool.Status == preflight.StatusReady {
			continue
		}
		reasons = append(reasons, tool.Reason)
	}
	worst := preflight.VerdictForReasons(reasons)
	if preflight.ExitCode(response.Verdict) > preflight.ExitCode(worst) {
		return response.Verdict
	}
	return worst
}

// preflightSummary is the one-line context that rides along with the exit-code
// error, e.g. "2 of 5 tools unavailable: server_disabled, not_found".
func preflightSummary(response *contracts.PreflightResponse) string {
	if response == nil {
		return ""
	}
	unavailable := 0
	seen := make(map[string]bool)
	var reasons []string
	for _, tool := range response.Tools {
		if tool.Status == preflight.StatusReady {
			continue
		}
		unavailable++
		if tool.Reason != "" && !seen[tool.Reason] {
			seen[tool.Reason] = true
			reasons = append(reasons, tool.Reason)
		}
	}
	if unavailable == 0 {
		return ""
	}
	summary := fmt.Sprintf("%d of %d tools unavailable", unavailable, len(response.Tools))
	if len(reasons) > 0 {
		summary += ": " + strings.Join(reasons, ", ")
	}
	return summary
}

// renderPreflight formats one response for the requested output format. It is
// pure (returns the string instead of printing) so every format is unit-tested
// without capturing stdout.
func renderPreflight(outputFormat string, response *contracts.PreflightResponse) (string, error) {
	formatter, err := output.NewFormatter(outputFormat)
	if err != nil {
		return "", output.NewStructuredError(output.ErrCodeInvalidOutputFormat, err.Error()).
			WithGuidance("Use -o table, -o json, or -o yaml")
	}

	if outputFormat == "json" || outputFormat == "yaml" {
		// Marshal through the wire DTO's JSON tags so `-o yaml` emits the same
		// key names as `-o json` and the REST payload, rather than yaml's
		// lowercased Go field names.
		payload, convErr := preflightWirePayload(response)
		if convErr != nil {
			return "", convErr
		}
		rendered, fmtErr := formatter.Format(payload)
		if fmtErr != nil {
			return "", fmt.Errorf("failed to format output: %w", fmtErr)
		}
		return rendered + "\n", nil
	}

	headers, rows := preflightRows(response)
	table, fmtErr := formatter.FormatTable(headers, rows)
	if fmtErr != nil {
		return "", fmt.Errorf("failed to format table: %w", fmtErr)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "VERDICT: %s (exit %d)\n", response.Verdict, preflight.ExitCode(preflightExitVerdict(response)))
	fmt.Fprintf(&b, "CHECKED: %s\n", response.CheckedAt.Format(time.RFC3339))
	if response.WaitedMS != nil {
		fmt.Fprintf(&b, "WAITED:  %dms\n", *response.WaitedMS)
	}
	b.WriteString("\n")
	b.WriteString(table)
	return b.String(), nil
}

// preflightWirePayload converts the response to generic JSON values so the
// YAML formatter honours the wire key names.
func preflightWirePayload(response *contracts.PreflightResponse) (map[string]interface{}, error) {
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to encode preflight response: %w", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, fmt.Errorf("failed to decode preflight response: %w", err)
	}
	return payload, nil
}

// preflightRows builds the table view. Detail and remediation are sanitized:
// detail can quote an upstream-controlled server or tool name.
func preflightRows(response *contracts.PreflightResponse) (headers []string, rows [][]string) {
	headers = []string{"ID", "STATUS", "REASON", "RETRYABLE", "ACTION", "DETAIL"}
	rows = make([][]string, 0, len(response.Tools))
	for _, tool := range response.Tools {
		reason := tool.Reason
		retryable := ""
		if tool.Retryable != nil {
			retryable = fmt.Sprintf("%t", *tool.Retryable)
		}
		action := tool.Action
		if reason == "" {
			reason = "-"
		}
		if retryable == "" {
			retryable = "-"
		}
		if action == "" {
			action = "-"
		}
		detail := tool.Detail
		if detail == "" {
			detail = tool.Remediation
		}
		if detail == "" {
			detail = "-"
		}
		rows = append(rows, []string{
			sanitizeName(tool.ID),
			tool.Status,
			reason,
			retryable,
			action,
			sanitizeCell(detail, maxToolDescriptionCell),
		})
	}
	return headers, rows
}
