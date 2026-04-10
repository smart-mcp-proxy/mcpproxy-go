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
	"golang.org/x/term"

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
  mcpproxy security enable mcp-scan
  mcpproxy security disable mcp-scan
  mcpproxy security scan github-server
  mcpproxy security report github-server
  mcpproxy security overview`,
	}

	securityCmd.AddCommand(newSecurityScannersCmd())
	securityCmd.AddCommand(newSecurityEnableCmd())
	securityCmd.AddCommand(newSecurityDisableCmd())
	// Keep old names as hidden aliases for backwards compatibility
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
//
// Honors the package-level --config and --data-dir flags from main.go so that
// `mcpproxy security ...` commands behave consistently with `mcpproxy serve`,
// `mcpproxy status`, and `mcpproxy upstream ...`.
func newSecurityCLIClient() (*cliclient.Client, *config.Config, error) {
	var (
		cfg *config.Config
		err error
	)
	if configFile != "" {
		cfg, err = config.LoadFromFile(configFile)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}
	if dataDir != "" {
		cfg.DataDir = dataDir
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

func newSecurityEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <scanner-id>",
		Short: "Enable a security scanner",
		Long: `Enable a security scanner by pulling its Docker image.

Examples:
  mcpproxy security enable mcp-scan
  mcpproxy security enable cisco-mcp-scanner`,
		Args: cobra.ExactArgs(1),
		RunE: runSecurityInstall,
	}
}

func newSecurityDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <scanner-id>",
		Short: "Disable a security scanner",
		Long: `Disable a security scanner and clean up its Docker image.

Examples:
  mcpproxy security disable mcp-scan`,
		Args: cobra.ExactArgs(1),
		RunE: runSecurityRemove,
	}
}

func newSecurityInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "install <scanner-id>",
		Short:  "Install a security scanner (alias for enable)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE:   runSecurityInstall,
	}
	return cmd
}

func newSecurityRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "remove <scanner-id>",
		Short:  "Remove an installed scanner (alias for disable)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE:   runSecurityRemove,
	}
	return cmd
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
		rawStatus := getMapString(sc, "status")
		status := scannerDisplayStatus(rawStatus)
		colorOpen, colorReset := scannerStatusColor(rawStatus)
		inputs := secJoinSlice(sc, "inputs")

		// Pad status BEFORE applying color so column alignment isn't broken
		// by invisible escape sequences.
		paddedStatus := fmt.Sprintf("%-12s", status)
		if colorOpen != "" {
			paddedStatus = colorOpen + paddedStatus + colorReset
		}

		fmt.Printf("%-20s %-22s %-22s %s %-s\n",
			secTruncate(id, 20),
			secTruncate(name, 22),
			secTruncate(vendor, 22),
			paddedStatus,
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

	fmt.Printf("Enabling scanner %q...\n", scannerID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodPost, "/api/v1/security/scanners/"+scannerID+"/enable", nil)
	if err != nil {
		return fmt.Errorf("failed to enable scanner: %w", err)
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

	fmt.Printf("Scanner %q enabled successfully.\n", scannerID)
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

	resp, err := client.DoRaw(ctx, http.MethodPost, "/api/v1/security/scanners/"+scannerID+"/disable", nil)
	if err != nil {
		return fmt.Errorf("failed to disable scanner: %w", err)
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

	fmt.Printf("Scanner %q disabled successfully.\n", scannerID)
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

	// F-15: bumped from 10s -> 60s. Configure can be slow when the scanner
	// engine validates credentials against an upstream provider.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// F-15: client-side prefetch — confirm the scanner exists before issuing
	// the (potentially slow) PUT, so a typo'd id fails fast with a 404 instead
	// of hanging the user for 60s.
	if err := securityVerifyScannerExists(ctx, client, scannerID); err != nil {
		return err
	}

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

// securityVerifyScannerExists pings the scanner status endpoint and returns
// a friendly "not found" error before the caller commits to a slower request.
func securityVerifyScannerExists(ctx context.Context, client *cliclient.Client, scannerID string) error {
	statusCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := client.DoRaw(statusCtx, http.MethodGet, "/api/v1/security/scanners/"+scannerID, nil)
	if err != nil {
		// Network error: don't block — let the real call surface the issue.
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("scanner %q not found", scannerID)
	}
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

	client, cfg, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	serverName := args[0]

	// F-06: --dry-run never starts a real scan. Instead, fetch the scanner
	// inventory + (best-effort) the server's last scan context, and print a
	// human-readable plan describing what *would* run. We still exit 0 so
	// the dry-run is scriptable.
	if secScanDryRun {
		return printScanDryRunPlan(client, serverName, secScanners)
	}

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

	// Compute a sane hard timeout for the whole scan operation:
	//   per-scanner-timeout * scanner_count + 30s margin, capped at 30 min
	// (or default to 15 min if we can't infer it).
	hardTimeout := computeScanHardTimeout(cfg, secScanners)
	ctx, cancel := context.WithTimeout(context.Background(), hardTimeout)
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

	// Synchronous mode: poll until done.
	//
	// F-05 fixes:
	//   1. Unwrap the API envelope (`{success,data}`) before reading status —
	//      the previous loop read `status` from the envelope and never saw
	//      "completed", causing infinite spin.
	//   2. Use a 750ms ticker (no tight loop, no 2s lag).
	//   3. Honor a hard timeout based on the configured per-scanner timeout.
	//   4. Print one progress line per tick with run/running/failed counts.
	fmt.Printf("Scanning %q (timeout %s, job %s)...\n", serverName, hardTimeout, jobID)
	scanStart := time.Now()

	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()

	var lastProgressLen int
	for {
		select {
		case <-ctx.Done():
			fmt.Println()
			return fmt.Errorf("scan timed out after %s for %q (job %s); use 'mcpproxy security status %s' to inspect", hardTimeout, serverName, jobID, serverName)
		case <-ticker.C:
		}

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

		// CRITICAL: unwrap the API envelope. The previous implementation
		// read job fields directly from the envelope and never observed
		// the "completed" terminal state.
		statusBody, err = unwrapAPIResponse(statusBody)
		if err != nil {
			return fmt.Errorf("API error: %w", err)
		}
		var status map[string]interface{}
		if err := json.Unmarshal(statusBody, &status); err != nil {
			return fmt.Errorf("failed to parse status response: %w", err)
		}

		jobStatus := getMapString(status, "status")
		elapsed := time.Since(scanStart).Truncate(time.Second)

		// Aggregate per-scanner counts so the user sees forward progress.
		var run, running, failed, total int
		var runningNames []string
		if scannerStatuses, ok := status["scanner_statuses"].([]interface{}); ok {
			total = len(scannerStatuses)
			for _, s := range scannerStatuses {
				ss, ok := s.(map[string]interface{})
				if !ok {
					continue
				}
				switch getMapString(ss, "status") {
				case "completed":
					run++
				case "failed":
					failed++
				case "running":
					running++
					if name := getMapString(ss, "scanner_id"); name != "" {
						runningNames = append(runningNames, name)
					}
				}
			}
		}

		progress := fmt.Sprintf("  [%s] %d run, %d running, %d failed of %d", elapsed, run, running, failed, total)
		if len(runningNames) > 0 {
			progress += fmt.Sprintf(" (running: %s)", strings.Join(runningNames, ", "))
		}
		// Erase previous line on TTY for a clean rolling display; on a pipe
		// just print one progress line per tick.
		if stdoutIsTTY() {
			pad := ""
			if lastProgressLen > len(progress) {
				pad = strings.Repeat(" ", lastProgressLen-len(progress))
			}
			fmt.Print("\r" + progress + pad)
			lastProgressLen = len(progress)
		} else {
			fmt.Println(progress)
		}

		switch jobStatus {
		case "completed":
			if stdoutIsTTY() {
				fmt.Println()
			}
			fmt.Printf("  Scan completed in %s\n", elapsed)
			return printScanSummary(client, ctx, serverName)
		case "failed":
			if stdoutIsTTY() {
				fmt.Println()
			}
			fmt.Printf("  Scan failed after %s\n", elapsed)
			errMsg := getMapString(status, "error")
			if errMsg != "" {
				return fmt.Errorf("scan failed: %s", errMsg)
			}
			return fmt.Errorf("scan failed for %q", serverName)
		case "cancelled":
			if stdoutIsTTY() {
				fmt.Println()
			}
			fmt.Printf("  Scan cancelled after %s\n", elapsed)
			return fmt.Errorf("scan was cancelled for %q", serverName)
		}
		// pending or running: continue polling
	}
}

// computeScanHardTimeout returns a sensible upper bound for a scan operation:
//
//	per_scanner_timeout * num_scanners + 30s margin, capped at 30 minutes.
//
// If the config does not specify a per-scanner timeout, falls back to 15 min.
func computeScanHardTimeout(cfg *config.Config, scannerFlag string) time.Duration {
	const fallback = 15 * time.Minute
	const cap = 30 * time.Minute

	if cfg == nil || cfg.Security == nil || time.Duration(cfg.Security.ScanTimeoutDefault) <= 0 {
		return fallback
	}
	per := time.Duration(cfg.Security.ScanTimeoutDefault)

	count := 0
	if scannerFlag != "" {
		count = len(splitAndTrim(scannerFlag))
	}
	if count <= 0 {
		// We don't know how many scanners are installed; assume up to 8.
		count = 8
	}
	total := per*time.Duration(count) + 30*time.Second
	if total > cap {
		return cap
	}
	if total < fallback {
		return fallback
	}
	return total
}

// printScanDryRunPlan implements the F-06 frontend dry-run: instead of asking
// the engine to "simulate" a scan (which today still launches containers),
// fetch the scanner inventory and the server's last scan context, then print
// a plan describing what *would* execute.
func printScanDryRunPlan(client *cliclient.Client, serverName, scannerFlag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. Fetch all scanners so we can show docker images / commands.
	scanResp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/security/scanners", nil)
	if err != nil {
		return fmt.Errorf("failed to list scanners: %w", err)
	}
	scanRaw, err := io.ReadAll(scanResp.Body)
	scanResp.Body.Close()
	if err != nil {
		return fmt.Errorf("failed to read scanners response: %w", err)
	}
	if scanResp.StatusCode != http.StatusOK {
		return parseAPIError(scanRaw, scanResp.StatusCode, "list scanners")
	}
	scanRaw, err = unwrapAPIResponse(scanRaw)
	if err != nil {
		return fmt.Errorf("API error: %w", err)
	}
	var allScanners []map[string]interface{}
	if err := json.Unmarshal(scanRaw, &allScanners); err != nil {
		return fmt.Errorf("failed to parse scanners response: %w", err)
	}

	// 2. Filter scanners: explicit --scanners flag wins, otherwise pick the
	//    ones that are configured/installed (i.e. the engine would run them).
	var selected []map[string]interface{}
	if scannerFlag != "" {
		wanted := make(map[string]bool)
		for _, id := range splitAndTrim(scannerFlag) {
			wanted[id] = true
		}
		for _, sc := range allScanners {
			if wanted[getMapString(sc, "id")] {
				selected = append(selected, sc)
			}
		}
	} else {
		for _, sc := range allScanners {
			st := getMapString(sc, "status")
			if st == "installed" || st == "configured" {
				selected = append(selected, sc)
			}
		}
	}

	// 3. Best-effort: fetch the server's last scan context for source info.
	//    This may 404 if the server has never been scanned — that's fine.
	var scanContext map[string]interface{}
	filesResp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/servers/"+serverName+"/scan/files", nil)
	if err == nil {
		filesRaw, _ := io.ReadAll(filesResp.Body)
		filesResp.Body.Close()
		if filesResp.StatusCode == http.StatusOK {
			if unwrapped, uerr := unwrapAPIResponse(filesRaw); uerr == nil {
				_ = json.Unmarshal(unwrapped, &scanContext)
			}
		}
	}

	format := ResolveOutputFormat()
	plan := map[string]interface{}{
		"server":   serverName,
		"dry_run":  true,
		"scanners": selected,
	}
	if scanContext != nil {
		plan["source"] = map[string]interface{}{
			"method":           getMapString(scanContext, "source_method"),
			"path":             getMapString(scanContext, "source_path"),
			"docker_isolation": scanContext["docker_isolation"],
			"total_files":      scanContext["total_files"],
		}
	}

	if format == "json" || format == "yaml" {
		return formatAndPrint(format, plan)
	}

	// Human-readable plan
	fmt.Printf("Dry-run plan for %q\n", serverName)
	fmt.Println(strings.Repeat("-", 60))
	if scanContext != nil {
		fmt.Println("Source (from last scan):")
		fmt.Printf("  Method:           %s\n", getMapString(scanContext, "source_method"))
		fmt.Printf("  Path:             %s\n", getMapString(scanContext, "source_path"))
		if di, ok := scanContext["docker_isolation"].(bool); ok {
			fmt.Printf("  Docker isolation: %t\n", di)
		}
		if tf, ok := scanContext["total_files"].(float64); ok {
			fmt.Printf("  Files (last):     %d\n", int(tf))
		}
	} else {
		fmt.Println("Source: (no prior scan context — source will be resolved at scan time)")
	}
	fmt.Println()

	if len(selected) == 0 {
		fmt.Println("No scanners would run.")
		if scannerFlag != "" {
			fmt.Println("(no scanners matched --scanners filter)")
		} else {
			fmt.Println("(no scanners are installed/configured — run `mcpproxy security enable <id>`)")
		}
		return nil
	}

	fmt.Printf("Scanners that would run (%d):\n", len(selected))
	for _, sc := range selected {
		id := getMapString(sc, "id")
		name := getMapString(sc, "name")
		status := getMapString(sc, "status")
		image := getMapString(sc, "docker_image")
		if override := getMapString(sc, "image_override"); override != "" {
			image = override
		}
		timeout := getMapString(sc, "timeout")
		fmt.Printf("  - %s (%s) [%s]\n", id, name, status)
		if image != "" {
			fmt.Printf("      image:   %s\n", image)
		}
		if timeout != "" {
			fmt.Printf("      timeout: %s\n", timeout)
		}
		if cmd, ok := sc["command"].([]interface{}); ok && len(cmd) > 0 {
			parts := make([]string, len(cmd))
			for i, p := range cmd {
				parts[i] = fmt.Sprintf("%v", p)
			}
			fmt.Printf("      command: %s\n", strings.Join(parts, " "))
		}
		if inputs, ok := sc["inputs"].([]interface{}); ok && len(inputs) > 0 {
			parts := make([]string, len(inputs))
			for i, p := range inputs {
				parts[i] = fmt.Sprintf("%v", p)
			}
			fmt.Printf("      inputs:  %s\n", strings.Join(parts, ", "))
		}
	}

	fmt.Println()
	fmt.Println("Dry-run only — no scanners executed. Re-run without --dry-run to scan.")
	return nil
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

	// F-16: poll on a steady ticker; on a TTY redraw the table in place using
	// ANSI cursor escapes. On a pipe, fall back to one status line per tick so
	// the output is grep/awk friendly.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	tty := stdoutIsTTY()
	var prevLines int
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("batch scan timed out after 30m; use 'mcpproxy security cancel-all' to abort")
		case <-ticker.C:
		}

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

		// Final state — print the final table without redraw, then exit.
		if queueStatus == "completed" || queueStatus == "cancelled" {
			if format == "json" || format == "yaml" {
				return formatAndPrint(format, progress)
			}
			if tty && prevLines > 0 {
				clearPreviousLines(prevLines)
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

		if tty {
			if prevLines > 0 {
				clearPreviousLines(prevLines)
			}
			prevLines = printQueueProgressTable(progress)
		} else {
			printQueueProgressOneLine(progress)
		}
	}
}

// clearPreviousLines uses ANSI escapes to move the cursor up `n` lines and
// erase from the cursor to the end of screen. Used to redraw the batch-scan
// progress table in place.
func clearPreviousLines(n int) {
	if n <= 0 {
		return
	}
	// CUU n: move cursor up; ED 0: clear from cursor to end of screen.
	fmt.Printf("\x1b[%dA\x1b[J", n)
}

// printQueueProgressTable prints the progress table for a batch scan and
// returns the number of lines printed (so the caller can erase them next tick).
func printQueueProgressTable(progress map[string]interface{}) int {
	total := int(getMapFloat(progress, "total"))
	completed := int(getMapFloat(progress, "completed"))
	running := int(getMapFloat(progress, "running"))
	skipped := int(getMapFloat(progress, "skipped"))
	failed := int(getMapFloat(progress, "failed"))

	lines := 0

	header := fmt.Sprintf("Scanning all servers (%d/%d completed, %d running", completed, total, running)
	if skipped > 0 {
		header += fmt.Sprintf(", %d skipped", skipped)
	}
	if failed > 0 {
		header += fmt.Sprintf(", %d failed", failed)
	}
	header += ")..."
	fmt.Println(header)
	lines++

	// Table header
	fmt.Printf("%-24s %-12s %-10s %s\n", "SERVER", "STATUS", "FINDINGS", "ERROR")
	lines++
	fmt.Println(strings.Repeat("-", 70))
	lines++

	// Items
	if items, ok := progress["items"].([]interface{}); ok {
		for _, item := range items {
			it, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			name := getMapString(it, "server_name")
			status := getMapString(it, "status")
			errMsg := getMapString(it, "error")
			skipReason := getMapString(it, "skip_reason")

			// F-16: pull findings_count from the per-item job status so the
			// FINDINGS column shows real numbers instead of "-".
			findings := "-"
			if fc, ok := it["findings_count"].(float64); ok {
				findings = fmt.Sprintf("%d", int(fc))
			} else if fc, ok := it["findings"].(float64); ok {
				findings = fmt.Sprintf("%d", int(fc))
			}

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
			lines++
		}
	}
	return lines
}

// printQueueProgressOneLine prints a single-line summary of batch progress.
// Used in non-TTY mode (e.g. when stdout is piped) to keep output greppable.
func printQueueProgressOneLine(progress map[string]interface{}) {
	total := int(getMapFloat(progress, "total"))
	completed := int(getMapFloat(progress, "completed"))
	running := int(getMapFloat(progress, "running"))
	skipped := int(getMapFloat(progress, "skipped"))
	failed := int(getMapFloat(progress, "failed"))
	fmt.Printf("[batch] %d/%d completed, %d running, %d skipped, %d failed\n",
		completed, total, running, skipped, failed)
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

	// F-10: also fetch /scan/status (best-effort) so we can name the scanners
	// that failed. The aggregated report has counts but only sometimes carries
	// per-scanner names; the live job status is the authoritative source.
	failedNames := fetchFailedScannerNames(client, ctx, serverName)
	return printReportTable(serverName, report, failedNames)
}

// fetchFailedScannerNames returns the IDs of scanners that did not complete
// in the most recent scan job for `serverName`. Returns nil on any error so
// the caller can render the report without per-scanner names.
func fetchFailedScannerNames(client *cliclient.Client, ctx context.Context, serverName string) []string {
	resp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/servers/"+serverName+"/scan/status", nil)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	body, err = unwrapAPIResponse(body)
	if err != nil {
		return nil
	}
	var status map[string]interface{}
	if err := json.Unmarshal(body, &status); err != nil {
		return nil
	}
	scannerStatuses, ok := status["scanner_statuses"].([]interface{})
	if !ok {
		return nil
	}
	var failed []string
	for _, s := range scannerStatuses {
		ss, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		if getMapString(ss, "status") == "failed" {
			if name := getMapString(ss, "scanner_id"); name != "" {
				failed = append(failed, name)
			}
		}
	}
	return failed
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

	// F-14: scrub Go zero-time from last_scan_at so neither the table nor the
	// JSON/YAML serializers show "0001-01-01 00:00:00". We keep the field in
	// the schema (as nil) so consumers don't break, but represent "never".
	normalizeOverviewLastScan(overview)

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
	if v, present := overview["last_scan_at"]; present && v != nil {
		if lastScan, ok := v.(string); ok && lastScan != "" {
			fmt.Printf("  Last scan:          %s\n", formatTimestamp(lastScan))
		} else {
			fmt.Printf("  Last scan:          %s\n", "never")
		}
	} else {
		fmt.Printf("  Last scan:          %s\n", "never")
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
	failedNames := fetchFailedScannerNames(client, ctx, serverName)
	return printReportTable(serverName, report, failedNames)
}

// printReportTable prints a human-readable report with two-pass scan support.
//
// failedScannerNames (F-10) is the list of scanner IDs that failed in the most
// recent scan job, sourced from /scan/status (the report itself only carries
// counts). When non-empty, the table shows a "Scanners: X run, Y failed" line
// and a yellow warn line about incomplete coverage.
func printReportTable(serverName string, report map[string]interface{}, failedScannerNames []string) error {
	riskScore := "?"
	if rs, ok := report["risk_score"].(float64); ok {
		riskScore = fmt.Sprintf("%d", int(rs))
	}

	scannedAt := getMapString(report, "scanned_at")
	jobID := getMapString(report, "job_id")

	fmt.Printf("Security Report: %s\n", serverName)
	if jobID != "" {
		fmt.Printf("Scan ID:     %s\n", jobID)
	}
	fmt.Printf("Risk Score:  %s/100\n", riskScore)
	if scannedAt != "" {
		fmt.Printf("Scanned:     %s\n", formatTimestamp(scannedAt))
	}

	// F-10: scanner coverage line. Pull counts from the aggregated report.
	scannersRun := int(getMapFloat(report, "scanners_run"))
	scannersFailed := int(getMapFloat(report, "scanners_failed"))
	scannersTotal := int(getMapFloat(report, "scanners_total"))
	if scannersTotal > 0 {
		line := fmt.Sprintf("Scanners:    %d run, %d failed", scannersRun, scannersFailed)
		if scannersFailed > 0 && len(failedScannerNames) > 0 {
			line += " (" + strings.Join(failedScannerNames, ", ") + ")"
		}
		line += fmt.Sprintf(" of %d", scannersTotal)
		fmt.Println(line)
	}
	fmt.Println()

	// F-10: warn the user when coverage is incomplete so they don't approve
	// a server based on a "0 findings" result that's actually 5 of 7 scanners
	// silently failing.
	if scannersFailed > 0 && scannersTotal > 0 {
		warn := fmt.Sprintf("WARNING: Scan coverage incomplete: %d of %d scanners did not run", scannersFailed, scannersTotal)
		if stdoutIsTTY() {
			warn = "\x1b[33m" + warn + "\x1b[0m"
		}
		fmt.Println(warn)
		fmt.Println()
	}

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

// scannerDisplayStatus normalizes the scanner status to a single vocabulary
// shared between table and JSON/YAML output (F-09).
//
// Vocabulary (richest set kept consistent across formats):
//
//	available  - registry entry, image not pulled
//	pulling    - docker pull in progress
//	installed  - image present, but required env vars not yet set
//	configured - image present and required secrets set (ready to run)
//	error      - last operation failed; see error_message
//
// Unknown values are returned verbatim so future statuses do not get hidden.
func scannerDisplayStatus(status string) string {
	switch status {
	case "available", "pulling", "installed", "configured", "error":
		return status
	case "":
		return "unknown"
	default:
		return status
	}
}

// scannerStatusColor returns an ANSI color escape for a scanner status, plus
// the matching reset sequence. Returns empty strings when stdout is not a TTY
// so that piped output stays clean.
func scannerStatusColor(status string) (open, reset string) {
	if !stdoutIsTTY() {
		return "", ""
	}
	switch status {
	case "configured":
		return "\x1b[32m", "\x1b[0m" // green
	case "installed":
		return "\x1b[36m", "\x1b[0m" // cyan
	case "pulling":
		return "\x1b[33m", "\x1b[0m" // yellow
	case "error":
		return "\x1b[31m", "\x1b[0m" // red
	case "available":
		return "\x1b[90m", "\x1b[0m" // bright black / grey
	default:
		return "", ""
	}
}

// stdoutIsTTY returns true when stdout is connected to an interactive terminal.
// Used to gate ANSI escapes (colors, cursor moves) so piped output stays clean.
func stdoutIsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// normalizeOverviewLastScan replaces a Go zero-time `last_scan_at` value with
// JSON null so neither the table nor the JSON/YAML serializers display
// "0001-01-01T00:00:00Z" to the user (F-14). The key is preserved (as nil) so
// existing consumers don't see a missing field.
func normalizeOverviewLastScan(overview map[string]interface{}) {
	if overview == nil {
		return
	}
	v, present := overview["last_scan_at"]
	if !present {
		// Insert nil so JSON output still has the field for schema stability.
		overview["last_scan_at"] = nil
		return
	}
	s, ok := v.(string)
	if !ok || s == "" {
		overview["last_scan_at"] = nil
		return
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil && t.IsZero() {
		overview["last_scan_at"] = nil
		return
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil && t.IsZero() {
		overview["last_scan_at"] = nil
		return
	}
	// Also catch the literal stdlib zero serialization.
	if strings.HasPrefix(s, "0001-01-01") {
		overview["last_scan_at"] = nil
	}
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
