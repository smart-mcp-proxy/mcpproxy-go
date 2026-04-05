package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"
)

var (
	// security scan flags
	secScanAsync  bool
	secScanDryRun bool
	secScanners   string
	secScanAll    bool

	// security approve flags
	secApproveForce bool

	// security configure flags
	secConfigEnv []string
)

// GetSecurityCommand returns the security parent command.
func GetSecurityCommand() *cobra.Command {
	securityCmd := &cobra.Command{
		Use:   "security",
		Short: "Security scanner management and server scanning",
		Long: `Commands for managing security scanners, scanning MCP servers,
and reviewing scan results.

Security scanners run as Docker containers and analyze upstream MCP servers
for vulnerabilities, tool poisoning attacks, and other security issues.

Examples:
  mcpproxy security scanners
  mcpproxy security install mcp-scan
  mcpproxy security scan github-server
  mcpproxy security report github-server
  mcpproxy security overview`,
	}

	securityCmd.AddCommand(newSecurityScannersCmd())
	securityCmd.AddCommand(newSecurityInstallCmd())
	securityCmd.AddCommand(newSecurityRemoveCmd())
	securityCmd.AddCommand(newSecurityConfigureCmd())
	securityCmd.AddCommand(newSecurityScanCmd())
	securityCmd.AddCommand(newSecurityStatusCmd())
	securityCmd.AddCommand(newSecurityReportCmd())
	securityCmd.AddCommand(newSecurityApproveCmd())
	securityCmd.AddCommand(newSecurityRejectCmd())
	securityCmd.AddCommand(newSecurityRescanCmd())
	securityCmd.AddCommand(newSecurityOverviewCmd())
	securityCmd.AddCommand(newSecurityIntegrityCmd())
	securityCmd.AddCommand(newSecurityCancelAllCmd())

	return securityCmd
}

// newSecurityCLIClient creates a cliclient.Client connected to the running MCPProxy.
func newSecurityCLIClient() (*cliclient.Client, *config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}
	cfg.EnsureAPIKey()

	socketPath := socket.DetectSocketPath(cfg.DataDir)

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	var client *cliclient.Client
	if socket.IsSocketAvailable(socketPath) {
		client = cliclient.NewClient(socketPath, logger.Sugar())
	} else {
		endpoint := fmt.Sprintf("http://%s", cfg.Listen)
		client = cliclient.NewClientWithAPIKey(endpoint, cfg.APIKey, logger.Sugar())
	}

	return client, cfg, nil
}

// --- Subcommand constructors ---

func newSecurityScannersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scanners",
		Short: "List available and installed scanners",
		Long: `List all security scanners from the registry and their current status.

Examples:
  mcpproxy security scanners
  mcpproxy security scanners -o json`,
		RunE: runSecurityScanners,
	}
}

func newSecurityInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <scanner-id>",
		Short: "Install a security scanner",
		Long: `Install a security scanner by pulling its Docker image.

Examples:
  mcpproxy security install mcp-scan
  mcpproxy security install cisco-mcp-scanner`,
		Args: cobra.ExactArgs(1),
		RunE: runSecurityInstall,
	}
}

func newSecurityRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <scanner-id>",
		Short: "Remove an installed scanner",
		Long: `Remove an installed security scanner and clean up its Docker image.

Examples:
  mcpproxy security remove mcp-scan`,
		Args: cobra.ExactArgs(1),
		RunE: runSecurityRemove,
	}
}

func newSecurityConfigureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure <scanner-id>",
		Short: "Configure scanner environment variables",
		Long: `Set API keys and other environment variables for a scanner.

Use --env KEY=VALUE (repeatable) to set one or more environment variables.

Examples:
  mcpproxy security configure mcp-scan --env OPENAI_API_KEY=sk-xxx
  mcpproxy security configure cisco-mcp-scanner --env API_KEY=xxx --env API_SECRET=yyy`,
		Args: cobra.ExactArgs(1),
		RunE: runSecurityConfigure,
	}

	cmd.Flags().StringArrayVar(&secConfigEnv, "env", nil, "Environment variable in KEY=VALUE format (repeatable)")
	_ = cmd.MarkFlagRequired("env")

	return cmd
}

func newSecurityScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan [server]",
		Short: "Scan a server with security scanners",
		Long: `Start a security scan on an upstream MCP server.

By default, blocks until the scan completes and shows a summary.
Use --async to start the scan and return immediately.
Use --all to scan all servers at once with a progress table.

Examples:
  mcpproxy security scan github-server
  mcpproxy security scan github-server --async
  mcpproxy security scan github-server --dry-run
  mcpproxy security scan github-server --scanners mcp-scan,cisco-mcp-scanner
  mcpproxy security scan --all
  mcpproxy security scan --all --scanners mcp-scan`,
		Args: cobra.MaximumNArgs(1),
		RunE: runSecurityScan,
	}

	cmd.Flags().BoolVar(&secScanAll, "all", false, "Scan all servers (shows progress table)")
	cmd.Flags().BoolVar(&secScanAsync, "async", false, "Start scan and return immediately without waiting")
	cmd.Flags().BoolVar(&secScanDryRun, "dry-run", false, "Simulate scan without executing")
	cmd.Flags().StringVar(&secScanners, "scanners", "", "Comma-separated scanner IDs to use (default: all installed)")

	return cmd
}

func newSecurityStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <server>",
		Short: "Show current scan status for a server",
		Long: `Display the current or most recent scan status for a server.

Examples:
  mcpproxy security status github-server
  mcpproxy security status github-server -o json`,
		Args: cobra.ExactArgs(1),
		RunE: runSecurityStatus,
	}
}

func newSecurityReportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "report <server>",
		Short: "View the latest scan report for a server",
		Long: `Display the latest security scan report for a server.

Supports multiple output formats:
  -o table  Human-readable summary (default)
  -o json   Full JSON report
  -o yaml   Full YAML report
  -o sarif  Raw SARIF output from scanners

Examples:
  mcpproxy security report github-server
  mcpproxy security report github-server -o json
  mcpproxy security report github-server -o sarif`,
		Args: cobra.ExactArgs(1),
		RunE: runSecurityReport,
	}
}

func newSecurityApproveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve <server>",
		Short: "Approve a server after security scan",
		Long: `Approve a server's security posture based on scan results.
Use --force to approve even if findings exist.

Examples:
  mcpproxy security approve github-server
  mcpproxy security approve github-server --force`,
		Args: cobra.ExactArgs(1),
		RunE: runSecurityApprove,
	}

	cmd.Flags().BoolVar(&secApproveForce, "force", false, "Force approval even with findings")

	return cmd
}

func newSecurityRejectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reject <server>",
		Short: "Reject a server and quarantine it",
		Long: `Reject a server's security posture and quarantine it.

Examples:
  mcpproxy security reject github-server`,
		Args: cobra.ExactArgs(1),
		RunE: runSecurityReject,
	}
}

func newSecurityRescanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rescan <server>",
		Short: "Re-run security scanners on a server",
		Long: `Re-run all installed security scanners on a server.
This is equivalent to running 'security scan' again.

Examples:
  mcpproxy security rescan github-server
  mcpproxy security rescan github-server --async`,
		Args: cobra.ExactArgs(1),
		RunE: runSecurityScan, // Reuses scan logic
	}

	cmd.Flags().BoolVar(&secScanAsync, "async", false, "Start scan and return immediately without waiting")
	cmd.Flags().BoolVar(&secScanDryRun, "dry-run", false, "Simulate scan without executing")
	cmd.Flags().StringVar(&secScanners, "scanners", "", "Comma-separated scanner IDs to use (default: all installed)")

	return cmd
}

func newSecurityOverviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "overview",
		Short: "Show security dashboard summary",
		Long: `Display an aggregate security overview including scanner counts,
scan statistics, and finding summaries.

Examples:
  mcpproxy security overview
  mcpproxy security overview -o json`,
		RunE: runSecurityOverview,
	}
}

func newSecurityIntegrityCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "integrity <server>",
		Short: "Check runtime integrity of a server",
		Long: `Verify the runtime integrity of a server against its approved baseline.
Checks for changes to tool descriptions, Docker images, and source hashes.

Examples:
  mcpproxy security integrity github-server
  mcpproxy security integrity github-server -o json`,
		Args: cobra.ExactArgs(1),
		RunE: runSecurityIntegrity,
	}
}

// --- Command implementations ---

func runSecurityScanners(_ *cobra.Command, _ []string) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/security/scanners", nil)
	if err != nil {
		return fmt.Errorf("failed to list scanners: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "list scanners")
	}

	var scanners []map[string]interface{}
	respBody, err = unwrapAPIResponse(respBody)
	if err != nil {
		return fmt.Errorf("API error: %w", err)
	}
	if err := json.Unmarshal(respBody, &scanners); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		return formatAndPrint(format, scanners)
	}

	if len(scanners) == 0 {
		fmt.Println("No security scanners available.")
		return nil
	}

	// Table format
	fmt.Printf("%-20s %-22s %-22s %-12s %-s\n",
		"ID", "NAME", "VENDOR", "STATUS", "INPUTS")
	fmt.Println(strings.Repeat("-", 95))

	for _, sc := range scanners {
		id := getMapString(sc, "id")
		name := getMapString(sc, "name")
		vendor := getMapString(sc, "vendor")
		status := getMapString(sc, "status")
		inputs := secJoinSlice(sc, "inputs")

		fmt.Printf("%-20s %-22s %-22s %-12s %-s\n",
			secTruncate(id, 20),
			secTruncate(name, 22),
			secTruncate(vendor, 22),
			status,
			inputs)
	}

	return nil
}

func runSecurityInstall(_ *cobra.Command, args []string) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	scannerID := args[0]
	body, err := json.Marshal(map[string]string{"id": scannerID})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	fmt.Printf("Installing scanner %q...\n", scannerID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodPost, "/api/v1/security/scanners/install", body)
	if err != nil {
		return fmt.Errorf("failed to install scanner: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "install scanner")
	}

	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		return formatAndPrintRaw(format, respBody)
	}

	fmt.Printf("Scanner %q installed successfully.\n", scannerID)
	return nil
}

func runSecurityRemove(_ *cobra.Command, args []string) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	scannerID := args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodDelete, "/api/v1/security/scanners/"+scannerID, nil)
	if err != nil {
		return fmt.Errorf("failed to remove scanner: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("scanner %q not found", scannerID)
	}
	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "remove scanner")
	}

	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		return formatAndPrintRaw(format, respBody)
	}

	fmt.Printf("Scanner %q removed successfully.\n", scannerID)
	return nil
}

func runSecurityConfigure(_ *cobra.Command, args []string) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	scannerID := args[0]

	// Parse --env KEY=VALUE flags
	envMap := make(map[string]string)
	for _, e := range secConfigEnv {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return fmt.Errorf("invalid env format %q, expected KEY=VALUE", e)
		}
		envMap[parts[0]] = parts[1]
	}

	body, err := json.Marshal(map[string]interface{}{"env": envMap})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodPut, "/api/v1/security/scanners/"+scannerID+"/config", body)
	if err != nil {
		return fmt.Errorf("failed to configure scanner: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("scanner %q not found", scannerID)
	}
	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "configure scanner")
	}

	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		return formatAndPrintRaw(format, respBody)
	}

	fmt.Printf("Scanner %q configured with %d environment variable(s).\n", scannerID, len(envMap))
	return nil
}

func runSecurityScan(_ *cobra.Command, args []string) error {
	// Handle --all flag
	if secScanAll {
		return runSecurityScanAll()
	}

	// Single server mode requires exactly one argument
	if len(args) < 1 {
		return fmt.Errorf("server name is required (or use --all to scan all servers)")
	}

	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	serverName := args[0]

	// Build request body
	reqBody := map[string]interface{}{
		"dry_run": secScanDryRun,
	}
	if secScanners != "" {
		reqBody["scanner_ids"] = splitAndTrim(secScanners)
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodPost, "/api/v1/servers/"+serverName+"/scan", body)
	if err != nil {
		return fmt.Errorf("failed to start scan: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for disabled server (500 with specific message)
	if resp.StatusCode == http.StatusInternalServerError {
		errMsg := extractAPIErrorMsg(respBody)
		if strings.Contains(strings.ToLower(errMsg), "disabled") || strings.Contains(strings.ToLower(errMsg), "not enabled") {
			fmt.Fprintf(os.Stderr, "Error: Server %q is disabled. Enable it first or quarantine and scan:\n", serverName)
			fmt.Fprintf(os.Stderr, "  mcpproxy upstream enable %s\n", serverName)
			fmt.Fprintf(os.Stderr, "  mcpproxy security scan %s\n", serverName)
			return fmt.Errorf("server %q is disabled", serverName)
		}
	}

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "start scan")
	}

	var job map[string]interface{}
	respBody, err = unwrapAPIResponse(respBody)
	if err != nil {
		return fmt.Errorf("API error: %w", err)
	}
	if err := json.Unmarshal(respBody, &job); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	jobID := getMapString(job, "id")

	// If --async, return immediately with the job ID
	if secScanAsync {
		format := ResolveOutputFormat()
		if format == "json" || format == "yaml" {
			return formatAndPrint(format, job)
		}
		fmt.Printf("Scan started for %q (job: %s)\n", serverName, jobID)
		fmt.Println("Use 'mcpproxy security status " + serverName + "' to check progress.")
		return nil
	}

	// Synchronous mode: poll until done
	fmt.Printf("Scanning %q...\n", serverName)

	for {
		time.Sleep(2 * time.Second)

		statusResp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/servers/"+serverName+"/scan/status", nil)
		if err != nil {
			return fmt.Errorf("failed to check scan status: %w", err)
		}

		statusBody, err := io.ReadAll(statusResp.Body)
		statusResp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read status response: %w", err)
		}

		if statusResp.StatusCode != http.StatusOK {
			return parseAPIError(statusBody, statusResp.StatusCode, "check scan status")
		}

		var status map[string]interface{}
		if err := json.Unmarshal(statusBody, &status); err != nil {
			return fmt.Errorf("failed to parse status response: %w", err)
		}

		jobStatus := getMapString(status, "status")

		// Show progress from per-scanner statuses
		if scannerStatuses, ok := status["scanner_statuses"].([]interface{}); ok {
			var running, done int
			for _, s := range scannerStatuses {
				if ss, ok := s.(map[string]interface{}); ok {
					switch getMapString(ss, "status") {
					case "completed", "failed":
						done++
					case "running":
						running++
					}
				}
			}
			total := len(scannerStatuses)
			if total > 0 {
				fmt.Printf("\r  Progress: %d/%d scanners complete, %d running", done, total, running)
			}
		}

		switch jobStatus {
		case "completed":
			fmt.Println()
			return printScanSummary(client, ctx, serverName)
		case "failed":
			fmt.Println()
			errMsg := getMapString(status, "error")
			if errMsg != "" {
				return fmt.Errorf("scan failed: %s", errMsg)
			}
			return fmt.Errorf("scan failed for %q", serverName)
		case "cancelled":
			fmt.Println()
			return fmt.Errorf("scan was cancelled for %q", serverName)
		}
		// pending or running: continue polling
	}
}

// runSecurityScanAll handles the --all flag: starts a batch scan and polls for progress.
func runSecurityScanAll() error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	// Build request body
	reqBody := map[string]interface{}{}
	if secScanners != "" {
		reqBody["scanner_ids"] = splitAndTrim(secScanners)
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodPost, "/api/v1/security/scan-all", body)
	if err != nil {
		return fmt.Errorf("failed to start batch scan: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "start batch scan")
	}

	format := ResolveOutputFormat()

	// If --async, return immediately
	if secScanAsync {
		if format == "json" || format == "yaml" {
			return formatAndPrintRaw(format, respBody)
		}
		fmt.Println("Batch scan started. Use 'mcpproxy security scan --all' to check progress.")
		return nil
	}

	// Poll for progress
	for {
		time.Sleep(3 * time.Second)

		qResp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/security/queue", nil)
		if err != nil {
			return fmt.Errorf("failed to check queue progress: %w", err)
		}

		qBody, err := io.ReadAll(qResp.Body)
		qResp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read queue response: %w", err)
		}

		if qResp.StatusCode != http.StatusOK {
			return parseAPIError(qBody, qResp.StatusCode, "check queue progress")
		}

		var progress map[string]interface{}
		qBody, err = unwrapAPIResponse(qBody)
		if err != nil {
			return fmt.Errorf("API error: %w", err)
		}
		if err := json.Unmarshal(qBody, &progress); err != nil {
			return fmt.Errorf("failed to parse progress: %w", err)
		}

		// Check if idle (no batch in progress)
		queueStatus := getMapString(progress, "status")
		if queueStatus == "idle" {
			fmt.Println("No batch scan in progress.")
			return nil
		}

		// JSON/YAML output for final state
		if queueStatus == "completed" || queueStatus == "cancelled" {
			if format == "json" || format == "yaml" {
				return formatAndPrint(format, progress)
			}
			printQueueProgressTable(progress)
			fmt.Println()
			if queueStatus == "completed" {
				fmt.Println("Batch scan completed.")
			} else {
				fmt.Println("Batch scan was cancelled.")
			}
			return nil
		}

		// Show progress table (clear screen with carriage returns for terminal)
		printQueueProgressTable(progress)
	}
}

// printQueueProgressTable prints the progress table for a batch scan.
func printQueueProgressTable(progress map[string]interface{}) {
	total := int(getMapFloat(progress, "total"))
	completed := int(getMapFloat(progress, "completed"))
	running := int(getMapFloat(progress, "running"))
	skipped := int(getMapFloat(progress, "skipped"))
	failed := int(getMapFloat(progress, "failed"))

	fmt.Printf("\nScanning all servers (%d/%d completed, %d running", completed, total, running)
	if skipped > 0 {
		fmt.Printf(", %d skipped", skipped)
	}
	if failed > 0 {
		fmt.Printf(", %d failed", failed)
	}
	fmt.Println(")...")

	// Table header
	fmt.Printf("%-24s %-12s %-10s %s\n", "SERVER", "STATUS", "FINDINGS", "ERROR")
	fmt.Println(strings.Repeat("-", 70))

	// Items
	if items, ok := progress["items"].([]interface{}); ok {
		for _, item := range items {
			if it, ok := item.(map[string]interface{}); ok {
				name := getMapString(it, "server_name")
				status := getMapString(it, "status")
				errMsg := getMapString(it, "error")
				skipReason := getMapString(it, "skip_reason")
				findings := "-"

				// Show error or skip reason
				msg := errMsg
				if skipReason != "" {
					msg = skipReason
				}
				if len(msg) > 30 {
					msg = msg[:27] + "..."
				}

				fmt.Printf("%-24s %-12s %-10s %s\n",
					secTruncate(name, 24),
					status,
					findings,
					msg,
				)
			}
		}
	}
}

func newSecurityCancelAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel-all",
		Short: "Cancel a running batch scan",
		Long: `Cancel the current batch security scan in progress.
Any pending server scans will be skipped. Running scans may complete.

Examples:
  mcpproxy security cancel-all`,
		RunE: runSecurityCancelAll,
	}
}

func runSecurityCancelAll(_ *cobra.Command, _ []string) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodPost, "/api/v1/security/cancel-all", nil)
	if err != nil {
		return fmt.Errorf("failed to cancel batch scan: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "cancel batch scan")
	}

	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		return formatAndPrintRaw(format, respBody)
	}

	fmt.Println("Batch scan cancelled.")
	return nil
}

// extractAPIErrorMsg extracts the error message from an API error response body.
func extractAPIErrorMsg(body []byte) string {
	var errResp map[string]interface{}
	if err := json.Unmarshal(body, &errResp); err == nil {
		if msg, ok := errResp["error"].(string); ok {
			return msg
		}
	}
	return string(body)
}

// getMapFloat returns a float64 value from a map, defaulting to 0.
func getMapFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

func runSecurityStatus(_ *cobra.Command, args []string) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	serverName := args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/servers/"+serverName+"/scan/status", nil)
	if err != nil {
		return fmt.Errorf("failed to get scan status: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("no scan found for server %q", serverName)
	}
	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "get scan status")
	}

	var status map[string]interface{}
	respBody, err = unwrapAPIResponse(respBody)
	if err != nil {
		return fmt.Errorf("API error: %w", err)
	}
	if err := json.Unmarshal(respBody, &status); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		return formatAndPrint(format, status)
	}

	// Table output
	fmt.Printf("Scan Status: %s\n", serverName)
	fmt.Printf("  Job ID:   %s\n", getMapString(status, "id"))
	fmt.Printf("  Status:   %s\n", getMapString(status, "status"))
	if startedAt := getMapString(status, "started_at"); startedAt != "" {
		fmt.Printf("  Started:  %s\n", formatTimestamp(startedAt))
	}
	if completedAt := getMapString(status, "completed_at"); completedAt != "" {
		fmt.Printf("  Finished: %s\n", formatTimestamp(completedAt))
	}
	if errMsg := getMapString(status, "error"); errMsg != "" {
		fmt.Printf("  Error:    %s\n", errMsg)
	}

	// Per-scanner statuses
	if scannerStatuses, ok := status["scanner_statuses"].([]interface{}); ok && len(scannerStatuses) > 0 {
		fmt.Println()
		fmt.Printf("  %-20s %-12s %-8s %s\n", "SCANNER", "STATUS", "FINDINGS", "ERROR")
		fmt.Printf("  %s\n", strings.Repeat("-", 65))
		for _, s := range scannerStatuses {
			if ss, ok := s.(map[string]interface{}); ok {
				scannerID := getMapString(ss, "scanner_id")
				ssStatus := getMapString(ss, "status")
				findings := "0"
				if fc, ok := ss["findings_count"].(float64); ok {
					findings = fmt.Sprintf("%d", int(fc))
				}
				ssErr := getMapString(ss, "error")
				if len(ssErr) > 25 {
					ssErr = ssErr[:22] + "..."
				}
				fmt.Printf("  %-20s %-12s %-8s %s\n", scannerID, ssStatus, findings, ssErr)
			}
		}
	}

	return nil
}

func runSecurityReport(_ *cobra.Command, args []string) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	serverName := args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/servers/"+serverName+"/scan/report", nil)
	if err != nil {
		return fmt.Errorf("failed to get scan report: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("no scan report found for server %q", serverName)
	}
	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "get scan report")
	}

	format := ResolveOutputFormat()

	// Special case: SARIF output
	if format == "sarif" {
		var report map[string]interface{}
		respBody, err = unwrapAPIResponse(respBody)
		if err != nil {
			return fmt.Errorf("API error: %w", err)
		}
		if err := json.Unmarshal(respBody, &report); err != nil {
			return fmt.Errorf("failed to parse report: %w", err)
		}
		return printSarifOutput(report)
	}

	if format == "json" || format == "yaml" {
		return formatAndPrintRaw(format, respBody)
	}

	// Table output: parse and display human-readable report
	var report map[string]interface{}
	respBody, err = unwrapAPIResponse(respBody)
	if err != nil {
		return fmt.Errorf("API error: %w", err)
	}
	if err := json.Unmarshal(respBody, &report); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	return printReportTable(serverName, report)
}

func runSecurityApprove(_ *cobra.Command, args []string) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	serverName := args[0]
	body, err := json.Marshal(map[string]interface{}{"force": secApproveForce})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodPost, "/api/v1/servers/"+serverName+"/security/approve", body)
	if err != nil {
		return fmt.Errorf("failed to approve server: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "approve server")
	}

	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		return formatAndPrintRaw(format, respBody)
	}

	if secApproveForce {
		fmt.Printf("Server %q force-approved.\n", serverName)
	} else {
		fmt.Printf("Server %q approved.\n", serverName)
	}
	return nil
}

func runSecurityReject(_ *cobra.Command, args []string) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	serverName := args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodPost, "/api/v1/servers/"+serverName+"/security/reject", nil)
	if err != nil {
		return fmt.Errorf("failed to reject server: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "reject server")
	}

	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		return formatAndPrintRaw(format, respBody)
	}

	fmt.Printf("Server %q rejected and quarantined.\n", serverName)
	return nil
}

func runSecurityOverview(_ *cobra.Command, _ []string) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/security/overview", nil)
	if err != nil {
		return fmt.Errorf("failed to get security overview: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "get security overview")
	}

	var overview map[string]interface{}
	respBody, err = unwrapAPIResponse(respBody)
	if err != nil {
		return fmt.Errorf("API error: %w", err)
	}
	if err := json.Unmarshal(respBody, &overview); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		return formatAndPrint(format, overview)
	}

	// Human-readable overview
	fmt.Println("Security Overview")
	fmt.Printf("  Scanners installed: %s\n", secFormatInt(overview, "scanners_installed"))
	fmt.Printf("  Servers scanned:    %s\n", secFormatInt(overview, "servers_scanned"))
	fmt.Printf("  Total scans:        %s\n", secFormatInt(overview, "total_scans"))
	fmt.Printf("  Active scans:       %s\n", secFormatInt(overview, "active_scans"))
	if lastScan := getMapString(overview, "last_scan_at"); lastScan != "" {
		fmt.Printf("  Last scan:          %s\n", formatTimestamp(lastScan))
	}
	fmt.Println()

	// Findings breakdown
	if findings, ok := overview["findings_by_severity"].(map[string]interface{}); ok {
		fmt.Println("  Findings:")
		fmt.Printf("    Critical: %s\n", secFormatInt(findings, "critical"))
		fmt.Printf("    High:     %s\n", secFormatInt(findings, "high"))
		fmt.Printf("    Medium:   %s\n", secFormatInt(findings, "medium"))
		fmt.Printf("    Low:      %s\n", secFormatInt(findings, "low"))
		fmt.Printf("    Info:     %s\n", secFormatInt(findings, "info"))
	}

	return nil
}

func runSecurityIntegrity(_ *cobra.Command, args []string) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	serverName := args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/servers/"+serverName+"/integrity", nil)
	if err != nil {
		return fmt.Errorf("failed to check integrity: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("no integrity baseline found for server %q", serverName)
	}
	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "check integrity")
	}

	var result map[string]interface{}
	respBody, err = unwrapAPIResponse(respBody)
	if err != nil {
		return fmt.Errorf("API error: %w", err)
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		return formatAndPrint(format, result)
	}

	// Table output
	passed := false
	if p, ok := result["passed"].(bool); ok {
		passed = p
	}

	fmt.Printf("Integrity Check: %s\n", serverName)
	if passed {
		fmt.Println("  Status: PASSED")
	} else {
		fmt.Println("  Status: FAILED")
	}
	if checkedAt := getMapString(result, "checked_at"); checkedAt != "" {
		fmt.Printf("  Checked: %s\n", formatTimestamp(checkedAt))
	}

	// Show violations if any
	if violations, ok := result["violations"].([]interface{}); ok && len(violations) > 0 {
		fmt.Println()
		fmt.Println("  Violations:")
		for _, v := range violations {
			if viol, ok := v.(map[string]interface{}); ok {
				violType := getMapString(viol, "type")
				message := getMapString(viol, "message")
				fmt.Printf("    [%s] %s\n", strings.ToUpper(violType), message)
				if expected := getMapString(viol, "expected"); expected != "" {
					fmt.Printf("      Expected: %s\n", expected)
				}
				if actual := getMapString(viol, "actual"); actual != "" {
					fmt.Printf("      Actual:   %s\n", actual)
				}
			}
		}
	}

	return nil
}

// --- Display helpers ---

// printScanSummary fetches and prints a compact summary after a scan completes.
func printScanSummary(client *cliclient.Client, ctx context.Context, serverName string) error {
	resp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/servers/"+serverName+"/scan/report", nil)
	if err != nil {
		fmt.Println("Scan completed. Use 'mcpproxy security report " + serverName + "' to view results.")
		return nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Scan completed. Use 'mcpproxy security report " + serverName + "' to view results.")
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		fmt.Println("Scan completed. Use 'mcpproxy security report " + serverName + "' to view results.")
		return nil
	}

	var report map[string]interface{}
	respBody, err = unwrapAPIResponse(respBody)
	if err != nil {
		return fmt.Errorf("API error: %w", err)
	}
	if err := json.Unmarshal(respBody, &report); err != nil {
		fmt.Println("Scan completed. Use 'mcpproxy security report " + serverName + "' to view results.")
		return nil
	}

	fmt.Printf("Scan completed for %q.\n\n", serverName)
	return printReportTable(serverName, report)
}

// printReportTable prints a human-readable report with two-pass scan support.
func printReportTable(serverName string, report map[string]interface{}) error {
	riskScore := "?"
	if rs, ok := report["risk_score"].(float64); ok {
		riskScore = fmt.Sprintf("%d", int(rs))
	}

	scannedAt := getMapString(report, "scanned_at")

	fmt.Printf("Security Report: %s\n", serverName)
	fmt.Printf("Risk Score: %s/100\n", riskScore)
	if scannedAt != "" {
		fmt.Printf("Scanned: %s\n", formatTimestamp(scannedAt))
	}
	fmt.Println()

	// Separate findings by scan pass
	var pass1Findings, pass2Findings []interface{}
	if findings, ok := report["findings"].([]interface{}); ok {
		for _, f := range findings {
			if finding, ok := f.(map[string]interface{}); ok {
				scanPass := int(getMapFloat(finding, "scan_pass"))
				if scanPass == 2 {
					pass2Findings = append(pass2Findings, f)
				} else {
					pass1Findings = append(pass1Findings, f)
				}
			}
		}
	}

	// === Security Scan (Pass 1) ===
	fmt.Println("=== Security Scan (Pass 1) ===")
	if len(pass1Findings) == 0 {
		fmt.Println("  0 findings")
	} else {
		fmt.Printf("  %d finding(s)\n", len(pass1Findings))
		fmt.Println()
		printFindingsList(pass1Findings)
	}

	// === Supply Chain Audit (Pass 2) ===
	pass2Running := false
	if v, ok := report["pass2_running"].(bool); ok {
		pass2Running = v
	}
	pass2Complete := false
	if v, ok := report["pass2_complete"].(bool); ok {
		pass2Complete = v
	}

	fmt.Println()
	fmt.Println("=== Supply Chain Audit (Pass 2) ===")
	if pass2Running {
		fmt.Println("  Running in background...")
	} else if pass2Complete {
		if len(pass2Findings) == 0 {
			fmt.Println("  0 findings")
		} else {
			fmt.Printf("  %d finding(s)\n", len(pass2Findings))
			fmt.Println()
			printFindingsList(pass2Findings)
		}
	} else {
		fmt.Println("  Not started")
	}

	return nil
}

// printFindingsList prints a list of findings in the CLI report format.
func printFindingsList(findings []interface{}) {
	for _, f := range findings {
		finding, ok := f.(map[string]interface{})
		if !ok {
			continue
		}
		severity := strings.ToUpper(getMapString(finding, "severity"))
		ruleID := getMapString(finding, "rule_id")
		title := getMapString(finding, "title")
		location := getMapString(finding, "location")
		scannerName := getMapString(finding, "scanner")
		helpURI := getMapString(finding, "help_uri")
		pkg := getMapString(finding, "package_name")
		installed := getMapString(finding, "installed_version")
		fixed := getMapString(finding, "fixed_version")

		// Main line: [SEVERITY] CVE-ID: title (scanner)
		label := title
		if ruleID != "" && ruleID != title {
			label = ruleID
		}
		line := fmt.Sprintf("  [%s] %s", severity, label)
		if scannerName != "" {
			line += " (" + scannerName + ")"
		}
		fmt.Println(line)

		// Package info
		if pkg != "" {
			pkgLine := "         Package: " + pkg
			if installed != "" {
				pkgLine += " v" + installed
			}
			if fixed != "" {
				pkgLine += " -> fix: " + fixed
			}
			fmt.Println(pkgLine)
		}

		// Location
		if location != "" {
			fmt.Println("         Location: " + location)
		}

		// Link to advisory
		if helpURI != "" {
			fmt.Println("         Details: " + helpURI)
		}

		// Evidence (triggering content)
		evidence := getMapString(finding, "evidence")
		if evidence != "" {
			if len(evidence) > 200 {
				evidence = evidence[:200] + "..."
			}
			fmt.Println("         Evidence: " + evidence)
		}
	}
}

// printSarifOutput extracts and prints raw SARIF data from individual scanner reports.
func printSarifOutput(report map[string]interface{}) error {
	// Try to extract SARIF from individual scanner reports
	reports, ok := report["reports"].([]interface{})
	if !ok || len(reports) == 0 {
		return fmt.Errorf("no SARIF data available in report")
	}

	// Collect all SARIF runs into a combined envelope
	var allRuns []interface{}
	for _, r := range reports {
		if rep, ok := r.(map[string]interface{}); ok {
			if sarifRaw, ok := rep["sarif_raw"]; ok && sarifRaw != nil {
				// sarif_raw could be a json.RawMessage (string) or already parsed
				switch v := sarifRaw.(type) {
				case string:
					var sarif map[string]interface{}
					if err := json.Unmarshal([]byte(v), &sarif); err == nil {
						if runs, ok := sarif["runs"].([]interface{}); ok {
							allRuns = append(allRuns, runs...)
						}
					}
				case map[string]interface{}:
					if runs, ok := v["runs"].([]interface{}); ok {
						allRuns = append(allRuns, runs...)
					}
				}
			}
		}
	}

	if len(allRuns) == 0 {
		return fmt.Errorf("no SARIF data available in report")
	}

	// Build a combined SARIF envelope
	sarif := map[string]interface{}{
		"$schema": "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json",
		"version": "2.1.0",
		"runs":    allRuns,
	}

	formatted, err := json.MarshalIndent(sarif, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to format SARIF: %w", err)
	}
	fmt.Println(string(formatted))
	return nil
}

// --- Utility helpers ---

// unwrapAPIResponse extracts the "data" field from an API response envelope
// {success: true, data: ...}. If the response is not wrapped, returns the raw bytes.
func unwrapAPIResponse(raw []byte) ([]byte, error) {
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return raw, nil // Not an envelope, return raw
	}
	if envelope.Error != "" {
		return nil, fmt.Errorf("%s", envelope.Error)
	}
	if envelope.Data != nil {
		return envelope.Data, nil
	}
	return raw, nil // No data field, return raw
}

// formatAndPrint marshals the data in the given format and prints it.
func formatAndPrint(format string, data interface{}) error {
	formatter, err := clioutput.NewFormatter(format)
	if err != nil {
		return err
	}
	out, err := formatter.Format(data)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}
	fmt.Println(out)
	return nil
}

// formatAndPrintRaw parses raw JSON and re-formats it in the given format.
func formatAndPrintRaw(format string, rawJSON []byte) error {
	var data interface{}
	if err := json.Unmarshal(rawJSON, &data); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	return formatAndPrint(format, data)
}

// secJoinSlice joins a string slice field from a map for display.
func secJoinSlice(m map[string]interface{}, key string) string {
	items, ok := m[key].([]interface{})
	if !ok {
		return ""
	}
	strs := make([]string, len(items))
	for i, s := range items {
		strs[i] = fmt.Sprintf("%v", s)
	}
	return strings.Join(strs, ", ")
}

// secFormatInt formats a numeric field from a map as a string.
func secFormatInt(m map[string]interface{}, key string) string {
	if v, ok := m[key].(float64); ok {
		return fmt.Sprintf("%d", int(v))
	}
	return "0"
}

// secTruncate shortens a string to maxLen, appending "..." if truncated.
func secTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// formatTimestamp parses an RFC3339 timestamp and reformats it for display.
func formatTimestamp(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	return ts
}
