package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"mcpproxy-go/internal/cli/output"
	"mcpproxy-go/internal/cliclient"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/socket"
)

// Activity command flags
var (
	// Shared filter flags
	activityType       string
	activityServer     string
	activityTool       string
	activityStatus     string
	activitySessionID  string
	activityStartTime  string
	activityEndTime    string
	activityLimit      int
	activityOffset     int
	activityIntentType string // Spec 018: Filter by operation type (read, write, destructive)
	activityRequestID  string // Spec 021: Filter by HTTP request ID for correlation
	activityNoIcons    bool   // Disable emoji icons in output

	// Show command flags
	activityIncludeResponse bool

	// Summary command flags
	activityPeriod  string
	activityGroupBy string

	// Export command flags
	activityExportOutput  string
	activityExportFormat  string
	activityIncludeBodies bool
)

// ActivityFilter contains options for filtering activity records
type ActivityFilter struct {
	Type       string
	Server     string
	Tool       string
	Status     string
	SessionID  string
	StartTime  string
	EndTime    string
	Limit      int
	Offset     int
	IntentType string // Spec 018: Filter by operation type (read, write, destructive)
	RequestID  string // Spec 021: Filter by HTTP request ID for correlation
}

// Validate validates the filter options
func (f *ActivityFilter) Validate() error {
	// Validate type(s) - supports comma-separated values (Spec 024)
	if f.Type != "" {
		validTypes := []string{
			"tool_call", "policy_decision", "quarantine_change", "server_change",
			"system_start", "system_stop", "internal_tool_call", "config_change", // Spec 024: new types
		}
		// Split by comma for multi-type support
		types := strings.Split(f.Type, ",")
		for _, t := range types {
			t = strings.TrimSpace(t)
			valid := false
			for _, vt := range validTypes {
				if t == vt {
					valid = true
					break
				}
			}
			if !valid {
				return fmt.Errorf("invalid type '%s': must be one of %v", t, validTypes)
			}
		}
	}

	// Validate status
	if f.Status != "" {
		validStatuses := []string{"success", "error", "blocked"}
		valid := false
		for _, s := range validStatuses {
			if f.Status == s {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("invalid status '%s': must be one of %v", f.Status, validStatuses)
		}
	}

	// Validate intent_type (Spec 018)
	if f.IntentType != "" {
		validIntentTypes := []string{"read", "write", "destructive"}
		valid := false
		for _, it := range validIntentTypes {
			if f.IntentType == it {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("invalid intent-type '%s': must be one of %v", f.IntentType, validIntentTypes)
		}
	}

	// Validate time formats
	if f.StartTime != "" {
		if _, err := time.Parse(time.RFC3339, f.StartTime); err != nil {
			return fmt.Errorf("invalid start-time format: must be RFC3339 (e.g., 2025-01-01T00:00:00Z)")
		}
	}
	if f.EndTime != "" {
		if _, err := time.Parse(time.RFC3339, f.EndTime); err != nil {
			return fmt.Errorf("invalid end-time format: must be RFC3339 (e.g., 2025-01-01T00:00:00Z)")
		}
	}

	// Clamp limit
	if f.Limit < 1 {
		f.Limit = 50
	} else if f.Limit > 100 {
		f.Limit = 100
	}

	return nil
}

// ToQueryParams converts filter to URL query parameters
func (f *ActivityFilter) ToQueryParams() url.Values {
	q := url.Values{}
	if f.Type != "" {
		q.Set("type", f.Type)
	}
	if f.Server != "" {
		q.Set("server", f.Server)
	}
	if f.Tool != "" {
		q.Set("tool", f.Tool)
	}
	if f.Status != "" {
		q.Set("status", f.Status)
	}
	if f.SessionID != "" {
		q.Set("session_id", f.SessionID)
	}
	if f.StartTime != "" {
		q.Set("start_time", f.StartTime)
	}
	if f.EndTime != "" {
		q.Set("end_time", f.EndTime)
	}
	if f.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", f.Limit))
	}
	if f.Offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", f.Offset))
	}
	if f.IntentType != "" {
		q.Set("intent_type", f.IntentType)
	}
	// Spec 021: Add request_id filter for log correlation
	if f.RequestID != "" {
		q.Set("request_id", f.RequestID)
	}
	return q
}

// formatRelativeTime formats a timestamp as relative time for recent events
func formatRelativeTime(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case diff < 7*24*time.Hour:
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	case t.Year() == now.Year():
		return t.Format("Jan 02")
	default:
		return t.Format("Jan 02, 2006")
	}
}

// formatActivityDuration formats duration in milliseconds to human-readable
func formatActivityDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

// formatSourceIndicator returns an icon/abbreviation for the activity source
func formatSourceIndicator(source string) string {
	switch source {
	case "mcp":
		return "MCP" // AI agent via MCP protocol
	case "cli":
		return "CLI" // Direct CLI command
	case "api":
		return "API" // REST API call
	default:
		return "MCP" // Default to MCP for backwards compatibility
	}
}

// formatSourceDescription returns a human-readable description for the activity source
func formatSourceDescription(source string) string {
	switch source {
	case "mcp":
		return "AI agent via MCP protocol"
	case "cli":
		return "CLI command"
	case "api":
		return "REST API"
	default:
		return "AI agent via MCP protocol"
	}
}

// formatIntentIndicator extracts intent from activity metadata and returns visual indicator
// Returns emoji indicators: 📖 read, ✏️ write, ⚠️ destructive, or "-" if no intent
func formatIntentIndicator(activity map[string]interface{}) string {
	// Extract metadata from activity
	metadata := getMapField(activity, "metadata")
	if metadata == nil {
		return "-"
	}

	// Extract intent from metadata
	intent := getMapField(metadata, "intent")
	if intent == nil {
		// Check for tool_variant as fallback
		if toolVariant := getStringField(metadata, "tool_variant"); toolVariant != "" {
			return formatOperationIcon(toolVariantToOperationType(toolVariant))
		}
		return "-"
	}

	// Get operation_type from intent
	opType := getStringField(intent, "operation_type")
	if opType == "" {
		return "-"
	}

	return formatOperationIcon(opType)
}

// formatOperationIcon returns the visual indicator for an operation type
// If activityNoIcons is true, returns text instead of emoji
func formatOperationIcon(opType string) string {
	if activityNoIcons {
		// Text-only output
		switch opType {
		case "read":
			return "read"
		case "write":
			return "write"
		case "destructive":
			return "destructive"
		default:
			return "-"
		}
	}
	// Emoji output
	switch opType {
	case "read":
		return "📖" // Read operation
	case "write":
		return "✏️" // Write operation
	case "destructive":
		return "⚠️" // Destructive operation
	default:
		return "-"
	}
}

// toolVariantToOperationType converts tool variant name to operation type
func toolVariantToOperationType(variant string) string {
	switch variant {
	case "call_tool_read":
		return "read"
	case "call_tool_write":
		return "write"
	case "call_tool_destructive":
		return "destructive"
	default:
		return ""
	}
}

// displayIntentSection displays intent information for activity show command
func displayIntentSection(activity map[string]interface{}) {
	// Extract metadata from activity
	metadata := getMapField(activity, "metadata")
	if metadata == nil {
		return
	}

	// Check if there's any intent-related data
	toolVariant := getStringField(metadata, "tool_variant")
	intent := getMapField(metadata, "intent")

	if toolVariant == "" && intent == nil {
		return
	}

	fmt.Println()
	fmt.Println("Intent Declaration:")

	// Display tool variant if present
	if toolVariant != "" {
		opType := toolVariantToOperationType(toolVariant)
		fmt.Printf("  Tool Variant:      %s\n", toolVariant)
		if opType != "" {
			fmt.Printf("  Operation Type:    %s %s\n", formatOperationIcon(opType), opType)
		}
	}

	// Display intent details if present
	if intent != nil {
		if opType := getStringField(intent, "operation_type"); opType != "" && toolVariant == "" {
			fmt.Printf("  Operation Type:    %s %s\n", formatOperationIcon(opType), opType)
		}
		if sensitivity := getStringField(intent, "data_sensitivity"); sensitivity != "" {
			fmt.Printf("  Data Sensitivity:  %s\n", sensitivity)
		}
		if reason := getStringField(intent, "reason"); reason != "" {
			fmt.Printf("  Reason:            %s\n", reason)
		}
		if reversible, ok := intent["reversible"].(bool); ok {
			reversibleStr := "no"
			if reversible {
				reversibleStr = "yes"
			}
			fmt.Printf("  Reversible:        %s\n", reversibleStr)
		}
	}
}

// outputActivityError outputs an error in the appropriate format
func outputActivityError(err error, code string) error {
	outputFormat := ResolveOutputFormat()
	formatter, fmtErr := GetOutputFormatter()
	if fmtErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	// T026: Extract request_id from APIError if available
	var requestID string
	var apiErr *cliclient.APIError
	if errors.As(err, &apiErr) && apiErr.HasRequestID() {
		requestID = apiErr.RequestID
	}

	if outputFormat == "json" || outputFormat == "yaml" {
		structErr := output.NewStructuredError(code, err.Error()).
			WithGuidance("Use 'mcpproxy activity list' to view recent activities").
			WithRecoveryCommand("mcpproxy activity list --limit 10")
		// T026: Add request_id to StructuredError if available
		if requestID != "" {
			structErr = structErr.WithRequestID(requestID)
		}
		result, _ := formatter.FormatError(structErr)
		fmt.Println(result)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		// T026: Include request ID with log retrieval suggestion if available
		if requestID != "" {
			fmt.Fprintf(os.Stderr, "\nRequest ID: %s\n", requestID)
			fmt.Fprintf(os.Stderr, "Use 'mcpproxy activity list --request-id %s' to find related logs.\n", requestID)
		}
		fmt.Fprintf(os.Stderr, "Hint: Use 'mcpproxy activity list' to view recent activities\n")
	}
	return err
}

// Activity command definitions
var (
	activityCmd = &cobra.Command{
		Use:   "activity",
		Short: "Query and monitor activity logs",
		Long:  "Commands for listing, watching, and exporting activity logs from the MCPProxy daemon",
	}

	activityListCmd = &cobra.Command{
		Use:   "list",
		Short: "List activity records with filtering",
		Long: `List activity records with optional filtering and pagination.

Examples:
  # List recent activity
  mcpproxy activity list

  # List last 10 tool calls
  mcpproxy activity list --type tool_call --limit 10

  # List errors from github server
  mcpproxy activity list --server github --status error

  # List activity by request ID (for error correlation)
  mcpproxy activity list --request-id abc123-def456

  # List activity as JSON
  mcpproxy activity list -o json`,
		RunE: runActivityList,
	}

	activityWatchCmd = &cobra.Command{
		Use:   "watch",
		Short: "Watch activity stream in real-time",
		Long: `Watch activity events in real-time via SSE stream.

Examples:
  # Watch all activity
  mcpproxy activity watch

  # Watch only tool calls from github
  mcpproxy activity watch --type tool_call --server github

  # Watch with JSON output
  mcpproxy activity watch -o json`,
		RunE: runActivityWatch,
	}

	activityShowCmd = &cobra.Command{
		Use:   "show <id>",
		Short: "Show activity details",
		Long: `Show full details of a specific activity record.

Examples:
  # Show activity details
  mcpproxy activity show 01JFXYZ123ABC

  # Show with full response body
  mcpproxy activity show 01JFXYZ123ABC --include-response`,
		Args: cobra.ExactArgs(1),
		RunE: runActivityShow,
	}

	activitySummaryCmd = &cobra.Command{
		Use:   "summary",
		Short: "Show activity statistics",
		Long: `Show aggregated activity statistics for a time period.

Examples:
  # Show 24-hour summary
  mcpproxy activity summary

  # Show weekly summary
  mcpproxy activity summary --period 7d

  # Show summary grouped by server
  mcpproxy activity summary --by server`,
		RunE: runActivitySummary,
	}

	activityExportCmd = &cobra.Command{
		Use:   "export",
		Short: "Export activity records",
		Long: `Export activity records for compliance and auditing.

Examples:
  # Export all activity as JSON Lines to file
  mcpproxy activity export --output activity.jsonl

  # Export as CSV
  mcpproxy activity export --format csv --output activity.csv

  # Export to stdout for piping
  mcpproxy activity export --format csv | gzip > activity.csv.gz`,
		RunE: runActivityExport,
	}
)

// GetActivityCommand returns the activity command for registration
func GetActivityCommand() *cobra.Command {
	return activityCmd
}

func init() {
	// Add subcommands
	activityCmd.AddCommand(activityListCmd)
	activityCmd.AddCommand(activityWatchCmd)
	activityCmd.AddCommand(activityShowCmd)
	activityCmd.AddCommand(activitySummaryCmd)
	activityCmd.AddCommand(activityExportCmd)

	// List command flags
	activityListCmd.Flags().StringVarP(&activityType, "type", "t", "", "Filter by type (comma-separated for multiple): tool_call, system_start, system_stop, internal_tool_call, config_change, policy_decision, quarantine_change, server_change")
	activityListCmd.Flags().StringVarP(&activityServer, "server", "s", "", "Filter by server name")
	activityListCmd.Flags().StringVar(&activityTool, "tool", "", "Filter by tool name")
	activityListCmd.Flags().StringVar(&activityStatus, "status", "", "Filter by status: success, error, blocked")
	activityListCmd.Flags().StringVar(&activitySessionID, "session", "", "Filter by MCP session ID")
	activityListCmd.Flags().StringVar(&activityStartTime, "start-time", "", "Filter records after this time (RFC3339)")
	activityListCmd.Flags().StringVar(&activityEndTime, "end-time", "", "Filter records before this time (RFC3339)")
	activityListCmd.Flags().IntVarP(&activityLimit, "limit", "n", 50, "Max records to return (1-100)")
	activityListCmd.Flags().IntVar(&activityOffset, "offset", 0, "Pagination offset")
	activityListCmd.Flags().StringVar(&activityIntentType, "intent-type", "", "Filter by intent operation type: read, write, destructive")
	activityListCmd.Flags().StringVar(&activityRequestID, "request-id", "", "Filter by HTTP request ID for log correlation")
	activityListCmd.Flags().BoolVar(&activityNoIcons, "no-icons", false, "Disable emoji icons in output (use text instead)")

	// Watch command flags
	activityWatchCmd.Flags().StringVarP(&activityType, "type", "t", "", "Filter by type (comma-separated): tool_call, system_start, system_stop, internal_tool_call, config_change, policy_decision, quarantine_change, server_change")
	activityWatchCmd.Flags().StringVarP(&activityServer, "server", "s", "", "Filter by server name")

	// Show command flags
	activityShowCmd.Flags().BoolVar(&activityIncludeResponse, "include-response", false, "Show full response (may be large)")
	activityShowCmd.Flags().BoolVar(&activityNoIcons, "no-icons", false, "Disable emoji icons in output (use text instead)")

	// Summary command flags
	activitySummaryCmd.Flags().StringVarP(&activityPeriod, "period", "p", "24h", "Time period: 1h, 24h, 7d, 30d")
	activitySummaryCmd.Flags().StringVar(&activityGroupBy, "by", "", "Group by: server, tool, status")

	// Export command flags
	activityExportCmd.Flags().StringVar(&activityExportOutput, "output", "", "Output file path (stdout if not specified)")
	activityExportCmd.Flags().StringVarP(&activityExportFormat, "format", "f", "json", "Export format: json, csv")
	activityExportCmd.Flags().BoolVar(&activityIncludeBodies, "include-bodies", false, "Include full request/response bodies")
	// Reuse list filter flags for export
	activityExportCmd.Flags().StringVarP(&activityType, "type", "t", "", "Filter by type (comma-separated): tool_call, system_start, system_stop, internal_tool_call, config_change, policy_decision, quarantine_change, server_change")
	activityExportCmd.Flags().StringVarP(&activityServer, "server", "s", "", "Filter by server name")
	activityExportCmd.Flags().StringVar(&activityTool, "tool", "", "Filter by tool name")
	activityExportCmd.Flags().StringVar(&activityStatus, "status", "", "Filter by status")
	activityExportCmd.Flags().StringVar(&activitySessionID, "session", "", "Filter by session ID")
	activityExportCmd.Flags().StringVar(&activityStartTime, "start-time", "", "Filter after this time (RFC3339)")
	activityExportCmd.Flags().StringVar(&activityEndTime, "end-time", "", "Filter before this time (RFC3339)")
}

// getActivityClient creates an HTTP client for the daemon
func getActivityClient(logger *zap.SugaredLogger) (*cliclient.Client, error) {
	// Load config - use explicit config file if provided via -c flag
	var cfg *config.Config
	var err error
	if configFile != "" {
		cfg, err = config.LoadFromFile(configFile)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Try socket first, then HTTP
	endpoint := socket.GetDefaultSocketPath(cfg.DataDir)
	if cfg.Listen != "" {
		// Check if socket exists
		if _, err := os.Stat(endpoint); os.IsNotExist(err) {
			// Handle listen addresses like ":8080" (no host)
			listen := cfg.Listen
			if strings.HasPrefix(listen, ":") {
				listen = "127.0.0.1" + listen
			}
			endpoint = "http://" + listen
		}
	}

	return cliclient.NewClientWithAPIKey(endpoint, cfg.APIKey, logger), nil
}

// runActivityList implements the activity list command
func runActivityList(cmd *cobra.Command, _ []string) error {
	// Setup logger
	cmdLogLevel, _ := cmd.Flags().GetString("log-level")
	cmdLogToFile, _ := cmd.Flags().GetBool("log-to-file")
	cmdLogDir, _ := cmd.Flags().GetString("log-dir")

	logger, err := logs.SetupCommandLogger(false, cmdLogLevel, cmdLogToFile, cmdLogDir)
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	// Build filter
	filter := &ActivityFilter{
		Type:       activityType,
		Server:     activityServer,
		Tool:       activityTool,
		Status:     activityStatus,
		SessionID:  activitySessionID,
		StartTime:  activityStartTime,
		EndTime:    activityEndTime,
		Limit:      activityLimit,
		Offset:     activityOffset,
		IntentType: activityIntentType,
		RequestID:  activityRequestID,
	}

	if err := filter.Validate(); err != nil {
		return outputActivityError(err, "INVALID_FILTER")
	}

	// Create client
	client, err := getActivityClient(logger.Sugar())
	if err != nil {
		return outputActivityError(err, "CONNECTION_ERROR")
	}

	// Fetch activities
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	activities, total, err := client.ListActivities(ctx, filter)
	if err != nil {
		return outputActivityError(err, "FETCH_ERROR")
	}

	// Format output
	outputFormat := ResolveOutputFormat()
	formatter, err := GetOutputFormatter()
	if err != nil {
		return err
	}

	if outputFormat == "json" || outputFormat == "yaml" {
		data := map[string]interface{}{
			"activities": activities,
			"total":      total,
			"limit":      filter.Limit,
			"offset":     filter.Offset,
		}
		result, err := formatter.Format(data)
		if err != nil {
			return err
		}
		fmt.Println(result)
		return nil
	}

	// Table output
	if len(activities) == 0 {
		fmt.Println("No activities found")
		return nil
	}

	headers := []string{"ID", "SRC", "TYPE", "SERVER", "TOOL", "INTENT", "STATUS", "DURATION", "TIME"}
	rows := make([][]string, 0, len(activities))

	for _, act := range activities {
		id := getStringField(act, "id")
		source := getStringField(act, "source")
		actType := getStringField(act, "type")
		server := getStringField(act, "server_name")
		tool := getStringField(act, "tool_name")
		status := getStringField(act, "status")
		durationMs := getIntField(act, "duration_ms")
		timestamp := getStringField(act, "timestamp")

		// Extract intent from metadata (Spec 018)
		intentStr := formatIntentIndicator(act)

		// Parse and format timestamp
		timeStr := timestamp
		if t, err := time.Parse(time.RFC3339, timestamp); err == nil {
			timeStr = formatRelativeTime(t)
		}

		// Truncate type for display
		if len(actType) > 12 {
			actType = actType[:12]
		}

		// Format source indicator
		sourceIcon := formatSourceIndicator(source)

		rows = append(rows, []string{
			id, // Show full ID so it can be used with 'activity show'
			sourceIcon,
			actType,
			server,
			tool,
			intentStr,
			status,
			formatActivityDuration(int64(durationMs)),
			timeStr,
		})
	}

	result, err := formatter.FormatTable(headers, rows)
	if err != nil {
		return err
	}
	fmt.Print(result)

	// Show pagination info
	fmt.Printf("\nShowing %d of %d records", len(activities), total)
	if filter.Offset > 0 || total > filter.Limit {
		page := (filter.Offset / filter.Limit) + 1
		fmt.Printf(" (page %d)", page)
	}
	fmt.Println()

	return nil
}

// runActivityWatch implements the activity watch command
func runActivityWatch(cmd *cobra.Command, _ []string) error {
	// Setup logger
	cmdLogLevel, _ := cmd.Flags().GetString("log-level")
	cmdLogToFile, _ := cmd.Flags().GetBool("log-to-file")
	cmdLogDir, _ := cmd.Flags().GetString("log-dir")

	logger, err := logs.SetupCommandLogger(false, cmdLogLevel, cmdLogToFile, cmdLogDir)
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	// Load config to get endpoint - use same logic as getActivityClient
	var cfg *config.Config
	if configFile != "" {
		cfg, err = config.LoadFromFile(configFile)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return outputActivityError(err, "CONFIG_ERROR")
	}

	// Try socket first, then HTTP (same as getActivityClient)
	endpoint := socket.GetDefaultSocketPath(cfg.DataDir)
	if cfg.Listen != "" {
		// Check if socket exists
		if _, err := os.Stat(endpoint); os.IsNotExist(err) {
			// Handle listen addresses like ":8080" (no host)
			listen := cfg.Listen
			if strings.HasPrefix(listen, ":") {
				listen = "127.0.0.1" + listen
			}
			endpoint = "http://" + listen
		}
	}

	// Create HTTP client with socket support
	transport := &http.Transport{}
	dialer, baseURL, dialErr := socket.CreateDialer(endpoint)
	if dialErr != nil {
		logger.Sugar().Warnw("Failed to create socket dialer, using TCP",
			"endpoint", endpoint,
			"error", dialErr)
		baseURL = endpoint
	}
	if dialer != nil {
		transport.DialContext = dialer
		logger.Sugar().Debugw("Using socket/pipe connection for SSE", "endpoint", endpoint)
	} else {
		baseURL = endpoint
	}

	// Build SSE URL
	sseURL := baseURL + "/events"
	if cfg.APIKey != "" {
		sseURL += "?apikey=" + cfg.APIKey
	}

	// Setup context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Fprintln(os.Stderr, "\nReceived interrupt, stopping...")
		cancel()
	}()

	outputFormat := ResolveOutputFormat()

	// Create HTTP client with transport
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   0, // No timeout for SSE
	}

	// Watch with reconnection
	return watchWithReconnect(ctx, sseURL, outputFormat, logger.Sugar(), httpClient)
}

// watchWithReconnect watches the SSE stream with automatic reconnection
func watchWithReconnect(ctx context.Context, sseURL string, outputFormat string, logger *zap.SugaredLogger, httpClient *http.Client) error {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		err := watchActivityStream(ctx, sseURL, outputFormat, logger, httpClient)

		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Connection lost: %v. Reconnecting in %v...\n", err, backoff)
			time.Sleep(backoff)
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		backoff = 1 * time.Second
	}
}

// watchActivityStream connects to SSE and streams events
func watchActivityStream(ctx context.Context, sseURL string, outputFormat string, _ *zap.SugaredLogger, httpClient *http.Client) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	var eventType, eventData string

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			eventData = strings.TrimPrefix(line, "data: ")
		case line == "":
			// Empty line = event complete
			// Display all activity events except .started (which have no meaningful status/duration)
			// Includes: .completed, policy_decision, system_start, system_stop, config_change
			if strings.HasPrefix(eventType, "activity.") && !strings.HasSuffix(eventType, ".started") {
				displayActivityEvent(eventType, eventData, outputFormat)
			}
			eventType, eventData = "", ""
		}
	}

	return scanner.Err()
}

// displayActivityEvent formats and displays an SSE activity event
func displayActivityEvent(eventType, eventData, outputFormat string) {
	if outputFormat == "json" {
		// NDJSON output
		fmt.Println(eventData)
		return
	}

	// Parse event data - SSE wraps the actual payload in {"payload": ..., "timestamp": ...}
	var wrapper map[string]interface{}
	if err := json.Unmarshal([]byte(eventData), &wrapper); err != nil {
		return
	}

	// Extract the actual payload from the wrapper
	event, ok := wrapper["payload"].(map[string]interface{})
	if !ok {
		// If no wrapper, use the data directly (for backwards compatibility/testing)
		event = wrapper
	}

	// Determine event category from eventType (e.g., "activity.tool_call.completed" -> "tool_call")
	parts := strings.Split(eventType, ".")
	eventCategory := ""
	if len(parts) >= 2 {
		eventCategory = parts[1]
	}

	// Apply client-side filters
	if activityServer != "" {
		// For tool_call events, check server_name
		// For internal_tool_call events, check target_server
		server := getStringField(event, "server_name")
		if server == "" {
			server = getStringField(event, "target_server")
		}
		if server == "" {
			server = getStringField(event, "affected_entity") // for config_change
		}
		if server != activityServer {
			return
		}
	}
	if activityType != "" {
		if eventCategory != activityType {
			return
		}
	}

	// Skip successful call_tool_* internal tool calls to avoid duplicates
	// These have a corresponding tool_call entry that shows the actual upstream call.
	// Failed call_tool_* calls are shown since they have no corresponding tool_call.
	if eventCategory == "internal_tool_call" {
		internalToolName := getStringField(event, "internal_tool_name")
		status := getStringField(event, "status")
		if status == "success" && strings.HasPrefix(internalToolName, "call_tool_") {
			return
		}
	}

	// Format output based on event type
	timestamp := time.Now().Format("15:04:05")

	var line string
	switch eventCategory {
	case "tool_call":
		line = formatToolCallEvent(event, timestamp)
	case "internal_tool_call":
		line = formatInternalToolCallEvent(event, timestamp)
	case "policy_decision":
		line = formatPolicyDecisionEvent(event, timestamp)
	case "system_start":
		line = formatSystemStartEvent(event, timestamp)
	case "system_stop":
		line = formatSystemStopEvent(event, timestamp)
	case "config_change":
		line = formatConfigChangeEvent(event, timestamp)
	default:
		// Fallback for unknown event types
		line = fmt.Sprintf("[%s] [?] %s", timestamp, eventType)
	}

	fmt.Println(line)
}

// formatToolCallEvent formats a tool_call event for display
func formatToolCallEvent(event map[string]interface{}, timestamp string) string {
	source := getStringField(event, "source")
	server := getStringField(event, "server_name")
	tool := getStringField(event, "tool_name")
	status := getStringField(event, "status")
	durationMs := getIntField(event, "duration_ms")
	errMsg := getStringField(event, "error_message")

	sourceIcon := formatSourceIndicator(source)
	statusIcon := formatStatusIcon(status)

	line := fmt.Sprintf("[%s] [%s] %s:%s %s %s", timestamp, sourceIcon, server, tool, statusIcon, formatActivityDuration(int64(durationMs)))
	if errMsg != "" {
		line += " " + errMsg
	}
	if status == "blocked" {
		line += " BLOCKED"
	}
	return line
}

// formatInternalToolCallEvent formats an internal_tool_call event for display
func formatInternalToolCallEvent(event map[string]interface{}, timestamp string) string {
	internalTool := getStringField(event, "internal_tool_name")
	targetServer := getStringField(event, "target_server")
	targetTool := getStringField(event, "target_tool")
	status := getStringField(event, "status")
	durationMs := getIntField(event, "duration_ms")
	errMsg := getStringField(event, "error_message")

	statusIcon := formatStatusIcon(status)

	// Format: [HH:MM:SS] [INT] internal_tool -> target_server:target_tool status duration
	target := ""
	if targetServer != "" && targetTool != "" {
		target = fmt.Sprintf(" -> %s:%s", targetServer, targetTool)
	} else if targetServer != "" {
		target = fmt.Sprintf(" -> %s", targetServer)
	}

	line := fmt.Sprintf("[%s] [INT] %s%s %s %s", timestamp, internalTool, target, statusIcon, formatActivityDuration(int64(durationMs)))
	if errMsg != "" {
		line += " " + errMsg
	}
	return line
}

// formatPolicyDecisionEvent formats a policy_decision event for display
func formatPolicyDecisionEvent(event map[string]interface{}, timestamp string) string {
	server := getStringField(event, "server_name")
	tool := getStringField(event, "tool_name")
	decision := getStringField(event, "decision")
	reason := getStringField(event, "reason")

	statusIcon := "\u2298" // circle with slash for blocked
	if decision == "allowed" {
		statusIcon = "\u2713"
	}

	line := fmt.Sprintf("[%s] [POL] %s:%s %s", timestamp, server, tool, statusIcon)
	if reason != "" {
		line += " " + reason
	}
	return line
}

// formatSystemStartEvent formats a system_start event for display
func formatSystemStartEvent(event map[string]interface{}, timestamp string) string {
	version := getStringField(event, "version")
	listenAddr := getStringField(event, "listen_address")
	startupMs := getIntField(event, "startup_duration_ms")

	return fmt.Sprintf("[%s] [SYS] \u25B6 Started v%s on %s (%s)", timestamp, version, listenAddr, formatActivityDuration(int64(startupMs)))
}

// formatSystemStopEvent formats a system_stop event for display
func formatSystemStopEvent(event map[string]interface{}, timestamp string) string {
	reason := getStringField(event, "reason")
	signal := getStringField(event, "signal")
	uptimeSec := getIntField(event, "uptime_seconds")
	errMsg := getStringField(event, "error_message")

	line := fmt.Sprintf("[%s] [SYS] \u25A0 Stopped: %s", timestamp, reason)
	if signal != "" {
		line += fmt.Sprintf(" (signal: %s)", signal)
	}
	if uptimeSec > 0 {
		line += fmt.Sprintf(" uptime: %ds", uptimeSec)
	}
	if errMsg != "" {
		line += " error: " + errMsg
	}
	return line
}

// formatConfigChangeEvent formats a config_change event for display
func formatConfigChangeEvent(event map[string]interface{}, timestamp string) string {
	action := getStringField(event, "action")
	entity := getStringField(event, "affected_entity")
	source := getStringField(event, "source")

	sourceIcon := formatSourceIndicator(source)

	return fmt.Sprintf("[%s] [%s] \u2699 Config: %s %s", timestamp, sourceIcon, action, entity)
}

// formatStatusIcon returns a status icon for the given status
func formatStatusIcon(status string) string {
	switch status {
	case "success":
		return "\u2713" // checkmark
	case "error":
		return "\u2717" // X
	case "blocked":
		return "\u2298" // circle with slash
	default:
		return "?"
	}
}

// runActivityShow implements the activity show command
func runActivityShow(cmd *cobra.Command, args []string) error {
	activityID := args[0]

	// Setup logger
	cmdLogLevel, _ := cmd.Flags().GetString("log-level")
	cmdLogToFile, _ := cmd.Flags().GetBool("log-to-file")
	cmdLogDir, _ := cmd.Flags().GetString("log-dir")

	logger, err := logs.SetupCommandLogger(false, cmdLogLevel, cmdLogToFile, cmdLogDir)
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	// Create client
	client, err := getActivityClient(logger.Sugar())
	if err != nil {
		return outputActivityError(err, "CONNECTION_ERROR")
	}

	// Fetch activity detail
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	activity, err := client.GetActivityDetail(ctx, activityID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return outputActivityError(fmt.Errorf("activity not found: %s", activityID), "ACTIVITY_NOT_FOUND")
		}
		return outputActivityError(err, "FETCH_ERROR")
	}

	// Format output
	outputFormat := ResolveOutputFormat()
	formatter, err := GetOutputFormatter()
	if err != nil {
		return err
	}

	if outputFormat == "json" || outputFormat == "yaml" {
		result, err := formatter.Format(activity)
		if err != nil {
			return err
		}
		fmt.Println(result)
		return nil
	}

	// Table output - key-value pairs
	fmt.Println("Activity Details")
	fmt.Println("================")
	fmt.Println()

	fmt.Printf("ID:           %s\n", getStringField(activity, "id"))
	fmt.Printf("Type:         %s\n", getStringField(activity, "type"))
	source := getStringField(activity, "source")
	if source == "" {
		source = "mcp" // Default for backwards compatibility
	}
	fmt.Printf("Source:       %s (%s)\n", formatSourceIndicator(source), formatSourceDescription(source))
	fmt.Printf("Server:       %s\n", getStringField(activity, "server_name"))
	fmt.Printf("Tool:         %s\n", getStringField(activity, "tool_name"))
	fmt.Printf("Status:       %s\n", getStringField(activity, "status"))
	fmt.Printf("Duration:     %s\n", formatActivityDuration(int64(getIntField(activity, "duration_ms"))))
	fmt.Printf("Timestamp:    %s\n", getStringField(activity, "timestamp"))

	if sessionID := getStringField(activity, "session_id"); sessionID != "" {
		fmt.Printf("Session ID:   %s\n", sessionID)
	}

	if errMsg := getStringField(activity, "error_message"); errMsg != "" {
		fmt.Printf("Error:        %s\n", errMsg)
	}

	// Intent information (Spec 018)
	displayIntentSection(activity)

	// Arguments
	if args, ok := activity["arguments"].(map[string]interface{}); ok && len(args) > 0 {
		fmt.Println()
		fmt.Println("Arguments:")
		argsJSON, _ := json.MarshalIndent(args, "  ", "  ")
		fmt.Printf("  %s\n", string(argsJSON))
	}

	// Response (if included)
	if activityIncludeResponse {
		if response := getStringField(activity, "response"); response != "" {
			fmt.Println()
			fmt.Println("Response:")
			fmt.Printf("  %s\n", response)

			if truncated, ok := activity["response_truncated"].(bool); ok && truncated {
				fmt.Println("  (response was truncated)")
			}
		}
	}

	return nil
}

// runActivitySummary implements the activity summary command
func runActivitySummary(cmd *cobra.Command, _ []string) error {
	// Setup logger
	cmdLogLevel, _ := cmd.Flags().GetString("log-level")
	cmdLogToFile, _ := cmd.Flags().GetBool("log-to-file")
	cmdLogDir, _ := cmd.Flags().GetString("log-dir")

	logger, err := logs.SetupCommandLogger(false, cmdLogLevel, cmdLogToFile, cmdLogDir)
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	// Validate period
	validPeriods := []string{"1h", "24h", "7d", "30d"}
	valid := false
	for _, p := range validPeriods {
		if activityPeriod == p {
			valid = true
			break
		}
	}
	if !valid {
		return outputActivityError(fmt.Errorf("invalid period '%s': must be one of %v", activityPeriod, validPeriods), "INVALID_PERIOD")
	}

	// Create client
	client, err := getActivityClient(logger.Sugar())
	if err != nil {
		return outputActivityError(err, "CONNECTION_ERROR")
	}

	// Fetch summary
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	summary, err := client.GetActivitySummary(ctx, activityPeriod, activityGroupBy)
	if err != nil {
		return outputActivityError(err, "FETCH_ERROR")
	}

	// Format output
	outputFormat := ResolveOutputFormat()
	formatter, err := GetOutputFormatter()
	if err != nil {
		return err
	}

	if outputFormat == "json" || outputFormat == "yaml" {
		result, err := formatter.Format(summary)
		if err != nil {
			return err
		}
		fmt.Println(result)
		return nil
	}

	// Table output
	period := getStringField(summary, "period")
	totalCount := getIntField(summary, "total_count")
	successCount := getIntField(summary, "success_count")
	errorCount := getIntField(summary, "error_count")
	blockedCount := getIntField(summary, "blocked_count")

	fmt.Printf("Activity Summary (last %s)\n", period)
	fmt.Println("===========================")
	fmt.Println()

	// Calculate percentages
	successPct := float64(0)
	errorPct := float64(0)
	blockedPct := float64(0)
	if totalCount > 0 {
		successPct = float64(successCount) / float64(totalCount) * 100
		errorPct = float64(errorCount) / float64(totalCount) * 100
		blockedPct = float64(blockedCount) / float64(totalCount) * 100
	}

	fmt.Printf("%-15s %s\n", "METRIC", "VALUE")
	fmt.Printf("%-15s %s\n", strings.Repeat("-", 15), strings.Repeat("-", 20))
	fmt.Printf("%-15s %d\n", "Total Calls", totalCount)
	fmt.Printf("%-15s %d (%.1f%%)\n", "Successful", successCount, successPct)
	fmt.Printf("%-15s %d (%.1f%%)\n", "Errors", errorCount, errorPct)
	fmt.Printf("%-15s %d (%.1f%%)\n", "Blocked", blockedCount, blockedPct)

	// Top servers
	if topServers, ok := summary["top_servers"].([]interface{}); ok && len(topServers) > 0 {
		fmt.Println()
		fmt.Println("TOP SERVERS")
		fmt.Println(strings.Repeat("-", 30))
		for _, s := range topServers {
			if srv, ok := s.(map[string]interface{}); ok {
				name := getStringField(srv, "name")
				count := getIntField(srv, "count")
				fmt.Printf("%-20s %d calls\n", name, count)
			}
		}
	}

	// Top tools
	if topTools, ok := summary["top_tools"].([]interface{}); ok && len(topTools) > 0 {
		fmt.Println()
		fmt.Println("TOP TOOLS")
		fmt.Println(strings.Repeat("-", 40))
		for _, t := range topTools {
			if tool, ok := t.(map[string]interface{}); ok {
				server := getStringField(tool, "server")
				toolName := getStringField(tool, "tool")
				count := getIntField(tool, "count")
				fmt.Printf("%-30s %d calls\n", server+":"+toolName, count)
			}
		}
	}

	return nil
}

// runActivityExport implements the activity export command
func runActivityExport(cmd *cobra.Command, _ []string) error {
	// Setup logger
	cmdLogLevel, _ := cmd.Flags().GetString("log-level")
	cmdLogToFile, _ := cmd.Flags().GetBool("log-to-file")
	cmdLogDir, _ := cmd.Flags().GetString("log-dir")

	logger, err := logs.SetupCommandLogger(false, cmdLogLevel, cmdLogToFile, cmdLogDir)
	if err != nil {
		return fmt.Errorf("failed to setup logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	// Validate format
	if activityExportFormat != "json" && activityExportFormat != "csv" {
		return outputActivityError(fmt.Errorf("invalid format '%s': must be 'json' or 'csv'", activityExportFormat), "INVALID_FORMAT")
	}

	// Load config - use explicit config file if provided via -c flag
	var cfg *config.Config
	if configFile != "" {
		cfg, err = config.LoadFromFile(configFile)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return outputActivityError(err, "CONFIG_ERROR")
	}

	// Build export URL
	listen := cfg.Listen
	if strings.HasPrefix(listen, ":") {
		listen = "127.0.0.1" + listen
	}
	baseURL := "http://" + listen
	exportURL := baseURL + "/api/v1/activity/export"

	q := url.Values{}
	q.Set("format", activityExportFormat)
	if activityType != "" {
		q.Set("type", activityType)
	}
	if activityServer != "" {
		q.Set("server", activityServer)
	}
	if activityTool != "" {
		q.Set("tool", activityTool)
	}
	if activityStatus != "" {
		q.Set("status", activityStatus)
	}
	if activitySessionID != "" {
		q.Set("session_id", activitySessionID)
	}
	if activityStartTime != "" {
		q.Set("start_time", activityStartTime)
	}
	if activityEndTime != "" {
		q.Set("end_time", activityEndTime)
	}
	if activityIncludeBodies {
		q.Set("include_bodies", "true")
	}

	exportURL += "?" + q.Encode()

	// Create HTTP request
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, exportURL, nil)
	if err != nil {
		return outputActivityError(err, "REQUEST_ERROR")
	}

	// Add API key header
	if cfg.APIKey != "" {
		req.Header.Set("X-API-Key", cfg.APIKey)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return outputActivityError(err, "CONNECTION_ERROR")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return outputActivityError(fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body)), "EXPORT_ERROR")
	}

	// Determine output destination
	var w io.Writer = os.Stdout
	if activityExportOutput != "" {
		f, err := os.Create(activityExportOutput)
		if err != nil {
			return outputActivityError(fmt.Errorf("failed to create output file: %w", err), "FILE_ERROR")
		}
		defer f.Close()
		w = f
	}

	// Stream response to output
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		return outputActivityError(fmt.Errorf("failed to write output: %w", err), "WRITE_ERROR")
	}

	// Report success for file output
	if activityExportOutput != "" {
		fmt.Fprintf(os.Stderr, "Exported to %s\n", activityExportOutput)
	}

	return nil
}
