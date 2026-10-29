package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/term"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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

Scanning works out of the box: the offline baseline scanner is built into
mcpproxy, runs in-process on every scan, and needs no Docker and no setup. It
analyzes tool descriptions and schemas for tool poisoning attacks (TPAs),
prompt injection, and data exfiltration.

Deep scanners are the optional extra layer. They run as Docker containers for
source and dependency analysis (CVEs, secrets), so they need Docker plus
"deep scan" enabled — and when they are unavailable they are skipped, never
blocking the baseline verdict.

Examples:
  mcpproxy security scanners
  mcpproxy security enable mcp-scan
  mcpproxy security disable mcp-scan
  mcpproxy security scan github-server
  mcpproxy security report github-server
  mcpproxy security overview
  mcpproxy security bundle`,
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
	securityCmd.AddCommand(newSecurityBundleCmd())

	return securityCmd
}

// newSecurityCLIClient creates a cliclient.Client connected to the running MCPProxy.
//
// Honors the package-level --config and --data-dir flags from main.go so that
// `mcpproxy security ...` commands behave consistently with `mcpproxy serve`,
// `mcpproxy status`, and `mcpproxy upstream ...`.
func newSecurityCLIClient() (*cliclient.Client, *config.Config, error) {
	cfg, err := loadSecurityConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	// Socket first, then TCP fallback. Never generate an API key here —
	// a fabricated key cannot match the running daemon's.
	client, ok := newDaemonClient(cfg, logger.Sugar())
	if !ok {
		return nil, nil, fmt.Errorf("mcpproxy daemon is not reachable. Start with: mcpproxy serve")
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

Docker-based scanners belong to the opt-in deep-scan layer (Spec 077): they
only run during scans when security.deep_scan.enabled=true in mcp_config.json.
Enabling one here while deep scan is off prints a reminder.

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
Use --force to approve even if findings exist. This remains an alias for
'mcpproxy review approve <server>'.

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
		Long: `Reject a server's security posture and quarantine it. This remains
an alias for 'mcpproxy review reject <server>'.

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
	// Audit FIX 3b: if the core reports that the scanner belongs to the opt-in
	// deep-scan layer and that layer is off, tell the user — otherwise the
	// scanner is enabled but silently never runs.
	if hint := scannerEnableHint(respBody); hint != "" {
		fmt.Printf("Note: %s\n", hint)
	}
	return nil
}

// scannerEnableHint extracts the optional "hint" field from a successful
// POST /security/scanners/{id}/enable response ({"success":true,"data":
// {"status":"enabled","id":...,"hint":...}}). Returns "" when the response
// carries no hint (older cores, deep scan already enabled, in-process scanner).
func scannerEnableHint(respBody []byte) string {
	var wrapper struct {
		Data struct {
			Hint string `json:"hint"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &wrapper); err != nil {
		return ""
	}
	return wrapper.Data.Hint
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

	resp, err := client.DoRaw(ctx, http.MethodPost, serverAPIPath(serverName, "/scan"), body)
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
	// Progress goes to stderr so stdout stays clean for machine formats
	// (see docs/cli-output-formatting.md).
	fmt.Fprintf(os.Stderr, "Scanning %q (timeout %s, job %s)...\n", serverName, hardTimeout, jobID)
	scanStart := time.Now()

	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()

	var lastProgressLen int
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr)
			return fmt.Errorf("scan timed out after %s for %q (job %s); use 'mcpproxy security status %s' to inspect", hardTimeout, serverName, jobID, serverName)
		case <-ticker.C:
		}

		statusResp, err := client.DoRaw(ctx, http.MethodGet, serverAPIPath(serverName, "/scan/status"), nil)
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
		// just print one progress line per tick. Progress is written to
		// stderr, so the TTY check must follow stderr (not stdout) — else
		// `2>scan.log` on a terminal would fill the log with \r frames.
		if stderrIsTTY() {
			pad := ""
			if lastProgressLen > len(progress) {
				pad = strings.Repeat(" ", lastProgressLen-len(progress))
			}
			fmt.Fprint(os.Stderr, "\r"+progress+pad)
			lastProgressLen = len(progress)
		} else {
			fmt.Fprintln(os.Stderr, progress)
		}

		switch jobStatus {
		case "completed":
			if stderrIsTTY() {
				fmt.Fprintln(os.Stderr)
			}
			fmt.Fprintf(os.Stderr, "  Scan completed in %s\n", elapsed)
			return printScanSummary(client, ctx, serverName)
		case "failed":
			if stderrIsTTY() {
				fmt.Fprintln(os.Stderr)
			}
			fmt.Fprintf(os.Stderr, "  Scan failed after %s\n", elapsed)
			errMsg := getMapString(status, "error")
			if errMsg != "" {
				return fmt.Errorf("scan failed: %s", errMsg)
			}
			return fmt.Errorf("scan failed for %q", serverName)
		case "cancelled":
			if stderrIsTTY() {
				fmt.Fprintln(os.Stderr)
			}
			fmt.Fprintf(os.Stderr, "  Scan cancelled after %s\n", elapsed)
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
	filesResp, err := client.DoRaw(ctx, http.MethodGet, serverAPIPath(serverName, "/scan/files"), nil)
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

// scannerDurationMs returns a scanner_statuses entry's wall-clock duration in
// milliseconds. It prefers the explicit duration_ms field and falls back to
// computing it from started_at/completed_at so reports produced before
// duration_ms was recorded still render a timing value.
func scannerDurationMs(ss map[string]interface{}) float64 {
	if ms := getMapFloat(ss, "duration_ms"); ms > 0 {
		return ms
	}
	start := getMapString(ss, "started_at")
	end := getMapString(ss, "completed_at")
	if start == "" || end == "" {
		return 0
	}
	st, err1 := time.Parse(time.RFC3339Nano, start)
	et, err2 := time.Parse(time.RFC3339Nano, end)
	if err1 != nil || err2 != nil || et.Before(st) {
		return 0
	}
	return float64(et.Sub(st).Milliseconds())
}

// formatScannerDurationMs renders a per-scanner duration for human-readable
// output: sub-second values in milliseconds, larger values as a compact "X.Ys",
// and missing/zero timing as a dash.
func formatScannerDurationMs(ms float64) string {
	if ms <= 0 {
		return "-"
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", int(ms))
	}
	return fmt.Sprintf("%.1fs", ms/1000)
}

// printScannerStatusTable renders the per-scanner execution table including a
// DURATION column. Nothing is printed when there are no scanner statuses.
func printScannerStatusTable(scannerStatuses []interface{}) {
	if len(scannerStatuses) == 0 {
		return
	}
	fmt.Println()
	fmt.Printf("  %-20s %-12s %-10s %-8s %s\n", "SCANNER", "STATUS", "DURATION", "FINDINGS", "ERROR")
	fmt.Printf("  %s\n", strings.Repeat("-", 75))
	for _, s := range scannerStatuses {
		ss, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
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
		dur := formatScannerDurationMs(scannerDurationMs(ss))
		fmt.Printf("  %-20s %-12s %-10s %-8s %s\n", scannerID, ssStatus, dur, findings, ssErr)
	}
}

// printScannerTimings renders a compact per-scanner wall-clock timing block
// from a scan report's scanner_statuses. Nothing is printed when timing data
// is absent.
func printScannerTimings(report map[string]interface{}) {
	statuses, ok := report["scanner_statuses"].([]interface{})
	if !ok || len(statuses) == 0 {
		return
	}
	type timingRow struct{ id, status, dur string }
	rows := make([]timingRow, 0, len(statuses))
	for _, s := range statuses {
		ss, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		id := getMapString(ss, "scanner_id")
		if id == "" {
			continue
		}
		rows = append(rows, timingRow{
			id:     id,
			status: getMapString(ss, "status"),
			dur:    formatScannerDurationMs(scannerDurationMs(ss)),
		})
	}
	if len(rows) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("Scanner timing:")
	for _, r := range rows {
		fmt.Printf("  %-20s %-12s %s\n", r.id, r.status, r.dur)
	}
}

func runSecurityStatus(_ *cobra.Command, args []string) error {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}

	serverName := args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodGet, serverAPIPath(serverName, "/scan/status"), nil)
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

	// Per-scanner statuses (includes a DURATION column)
	if scannerStatuses, ok := status["scanner_statuses"].([]interface{}); ok {
		printScannerStatusTable(scannerStatuses)
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

	resp, err := client.DoRaw(ctx, http.MethodGet, serverAPIPath(serverName, "/scan/report"), nil)
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
	// that failed AND surface the per-scanner error message. The aggregated
	// report has counts but only sometimes carries per-scanner names; the
	// live job status is the authoritative source.
	failed := fetchFailedScannerInfo(client, ctx, serverName)
	return printReportTable(serverName, report, failed)
}

// failedScannerInfo carries a failed scanner's ID and the error that the
// scanner runtime recorded for it. The error is rendered in the human-readable
// report so the user can tell e.g. that `ramparts` failed because of a glibc
// version mismatch — previously the CLI showed only the scanner name and the
// user had to dig into log files to find the cause.
type failedScannerInfo struct {
	ID    string
	Error string
}

// fetchFailedScannerInfo returns information about scanners that did not
// complete in the most recent scan job for `serverName`. Returns nil on any
// error so the caller can render the report without per-scanner names.
func fetchFailedScannerInfo(client *cliclient.Client, ctx context.Context, serverName string) []failedScannerInfo {
	resp, err := client.DoRaw(ctx, http.MethodGet, serverAPIPath(serverName, "/scan/status"), nil)
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
	var failed []failedScannerInfo
	for _, s := range scannerStatuses {
		ss, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		if getMapString(ss, "status") == "failed" {
			id := getMapString(ss, "scanner_id")
			if id == "" {
				continue
			}
			failed = append(failed, failedScannerInfo{
				ID:    id,
				Error: getMapString(ss, "error"),
			})
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

	resp, err := client.DoRaw(ctx, http.MethodPost, serverAPIPath(serverName, "/security/approve"), body)
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
		if err := formatAndPrintRaw(format, respBody); err != nil {
			return err
		}
		warnApprovalHolds(os.Stderr, serverName, respBody)
		return nil
	}

	var envelope struct {
		Data map[string]interface{} `json:"data"`
	}
	_ = json.Unmarshal(respBody, &envelope)
	verb := "approved"
	if secApproveForce {
		verb = "force-approved"
	}
	fmt.Printf("Server %q %s%s.\n", serverName, verb, approvalCountsSuffix(envelope.Data))
	warnApprovalHolds(os.Stderr, serverName, respBody)
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

	resp, err := client.DoRaw(ctx, http.MethodPost, serverAPIPath(serverName, "/security/reject"), nil)
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

	// Signature bundle (spec 086 FR-019 / GH #938): which TPA corpus is live,
	// where it came from, and how fresh it is.
	for _, line := range signatureBundleLines(overview) {
		fmt.Println(line)
	}

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

	resp, err := client.DoRaw(ctx, http.MethodGet, serverAPIPath(serverName, "/integrity"), nil)
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
	resp, err := client.DoRaw(ctx, http.MethodGet, serverAPIPath(serverName, "/scan/report"), nil)
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
	failed := fetchFailedScannerInfo(client, ctx, serverName)
	return printReportTable(serverName, report, failed)
}

// printReportTable prints a human-readable report with two-pass scan support.
//
// failedScanners (F-10) is the list of scanner IDs+errors that failed in the
// most recent scan job, sourced from /scan/status (the report itself only
// carries counts). When non-empty, the table shows a "Scanners: X run, Y
// failed" line, a yellow warn line about incomplete coverage, and per-scanner
// error reasons so the user can see WHY a scanner failed without grepping
// log files.
func printReportTable(serverName string, report map[string]interface{}, failedScanners []failedScannerInfo) error {
	riskScore := "?"
	if rs, ok := report["risk_score"].(float64); ok {
		riskScore = fmt.Sprintf("%d", int(rs))
	}

	scannedAt := getMapString(report, "scanned_at")
	jobID := getMapString(report, "job_id")

	// F-10 / MCP-2401: scanner coverage. Read counts up front so the risk-score
	// line can flag low confidence when part of the fleet never ran.
	scannersRun := int(getMapFloat(report, "scanners_run"))
	scannersFailed := int(getMapFloat(report, "scanners_failed"))
	scannersTotal := int(getMapFloat(report, "scanners_total"))

	fmt.Printf("Security Report: %s\n", serverName)
	if jobID != "" {
		fmt.Printf("Scan ID:     %s\n", jobID)
	}
	riskLine := fmt.Sprintf("Risk Score:  %s/100", riskScore)
	if scannersFailed > 0 && scannersTotal > 0 {
		// MCP-2401: the score was computed from an incomplete scan. Flag the
		// degraded confidence on the number itself, not only in the warning
		// block below, so a glance at "0/100" isn't read as an all-clear.
		riskLine += fmt.Sprintf(" (degraded — %d of %d scanners did not run)", scannersFailed, scannersTotal)
	}
	fmt.Println(riskLine)
	if scannedAt != "" {
		fmt.Printf("Scanned:     %s\n", formatTimestamp(scannedAt))
	}

	if scannersTotal > 0 {
		line := fmt.Sprintf("Scanners:    %d run, %d failed", scannersRun, scannersFailed)
		if scannersFailed > 0 && len(failedScanners) > 0 {
			names := make([]string, 0, len(failedScanners))
			for _, f := range failedScanners {
				names = append(names, f.ID)
			}
			line += " (" + strings.Join(names, ", ") + ")"
		}
		line += fmt.Sprintf(" of %d", scannersTotal)
		fmt.Println(line)
	}

	// Per-scanner wall-clock timing, so users can see which scanner dominated
	// the scan time. Sourced from scanner_statuses; silently skipped when the
	// report carries no per-scanner status data.
	printScannerTimings(report)

	// Scan context: show the user what was actually scanned. Without this the
	// terse "0 findings" / "1 finding" output gives no signal as to whether
	// the scan looked at real source code (docker_extract), a working_dir
	// fallback, or just the synthetic tool definitions (tool_definitions_only).
	// That distinction is critical when triaging a finding — a "Malicious
	// Code" hit located at tools.json:85 means something very different from
	// the same hit located at server.py:42.
	printScanContextSection(report)

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
		// Show the actual error message for each failed scanner so users can
		// diagnose without digging through ~/Library/Logs/mcpproxy/main.log.
		for _, f := range failedScanners {
			if f.Error == "" {
				continue
			}
			msg := f.Error
			if len(msg) > 240 {
				msg = msg[:240] + "..."
			}
			// Single-line: replace newlines so a multi-line stderr capture
			// doesn't break the indented layout.
			msg = strings.ReplaceAll(msg, "\n", " ")
			fmt.Printf("  - %s: %s\n", f.ID, msg)
		}
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

// printScanContextSection renders a one-block summary of *what* the scanners
// looked at (source method, files, container, tool definitions exported).
// This is the most important context for triaging a finding: a HIGH-severity
// finding located at tools.json from a tool_definitions_only scan is a very
// different signal from the same finding located at server.py from a
// docker_extract scan.
func printScanContextSection(report map[string]interface{}) {
	ctx, ok := report["scan_context"].(map[string]interface{})
	if !ok {
		return
	}
	method := getMapString(ctx, "source_method")
	if method == "" && getMapString(ctx, "server_protocol") == "" {
		return // nothing useful to render
	}

	fmt.Println()
	fmt.Println("Scan Context")
	if method != "" {
		fmt.Printf("  Source:           %s\n", method)
	}
	if path := getMapString(ctx, "source_path"); path != "" {
		fmt.Printf("  Path:             %s\n", path)
	}
	if proto := getMapString(ctx, "server_protocol"); proto != "" {
		fmt.Printf("  Protocol:         %s\n", proto)
	}
	if di, ok := ctx["docker_isolation"].(bool); ok {
		fmt.Printf("  Docker isolation: %t\n", di)
	}
	if cid := getMapString(ctx, "container_id"); cid != "" {
		// Truncate full SHA256 container IDs to 12 chars (docker's standard).
		short := cid
		if len(short) > 12 {
			short = short[:12]
		}
		fmt.Printf("  Container:        %s\n", short)
	}
	if tf, ok := ctx["total_files"].(float64); ok && int(tf) > 0 {
		fmt.Printf("  Files scanned:    %d\n", int(tf))
	}
	if te, ok := ctx["tools_exported"].(float64); ok && int(te) > 0 {
		fmt.Printf("  Tools analyzed:   %d\n", int(te))
	}
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
		description := getMapString(finding, "description")
		location := getMapString(finding, "location")
		scannerName := getMapString(finding, "scanner")
		helpURI := getMapString(finding, "help_uri")
		pkg := getMapString(finding, "package_name")
		installed := getMapString(finding, "installed_version")
		fixed := getMapString(finding, "fixed_version")
		threatLevel := getMapString(finding, "threat_level")
		threatType := getMapString(finding, "threat_type")
		category := getMapString(finding, "category")

		// Main line: [SEVERITY] CVE-ID: title (scanner)
		label := title
		if ruleID != "" && ruleID != title {
			label = ruleID
		}
		line := fmt.Sprintf("  [%s] %s", severity, label)
		if cvss, ok := finding["cvss_score"].(float64); ok && cvss > 0 {
			line += fmt.Sprintf(" CVSS=%.1f", cvss)
		}
		if scannerName != "" {
			line += " (" + scannerName + ")"
		}
		fmt.Println(line)

		// Title (when ruleID is the headline label)
		if title != "" && label == ruleID {
			fmt.Println("         Title:    " + title)
		}

		// Description: this is the long-form rule explanation. Previously the
		// CLI rendered ONLY the rule ID, leaving users to look up what e.g.
		// "MCP-MC-001" meant. Showing the description here mirrors the Web UI.
		if description != "" && description != title {
			desc := description
			if len(desc) > 240 {
				desc = desc[:240] + "..."
			}
			desc = strings.ReplaceAll(desc, "\n", " ")
			fmt.Println("         What:     " + desc)
		}

		// Threat classification context (already used in the Web UI but absent
		// from CLI output until now). Helps users distinguish a tool-poisoning
		// finding from an obfuscated-code finding from a CVE.
		if threatType != "" || threatLevel != "" || category != "" {
			parts := []string{}
			if threatLevel != "" {
				parts = append(parts, threatLevel)
			}
			if threatType != "" {
				parts = append(parts, threatType)
			}
			if category != "" && category != threatType {
				parts = append(parts, "category="+category)
			}
			if len(parts) > 0 {
				fmt.Println("         Threat:   " + strings.Join(parts, " · "))
			}
		}

		// Deterministic-scanner transparency (Spec 076 US4): the combined
		// confidence and the independent checks that contributed to this
		// finding, so an operator can see WHY a tool was flagged and that
		// agreement among checks raised its score.
		if conf, ok := finding["confidence"].(float64); ok && conf > 0 {
			fmt.Printf("         Confidence: %.2f\n", conf)
		}
		if rawSignals, ok := finding["signals"].([]interface{}); ok && len(rawSignals) > 0 {
			names := make([]string, 0, len(rawSignals))
			for _, s := range rawSignals {
				if name, ok := s.(string); ok && name != "" {
					names = append(names, name)
				}
			}
			if len(names) > 0 {
				fmt.Println("         Signals:  " + strings.Join(names, ", "))
			}
		}

		// Package info
		if pkg != "" {
			pkgLine := "         Package:  " + pkg
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
			fmt.Println("         Details:  " + helpURI)
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
// signatureBundleLines renders the security overview's `signature_bundle`
// descriptor (spec 086 FR-019 / GH #938 finding 2). Before this, no supported
// surface could answer "which signatures is my proxy running, and how old are
// they?" — a years-stale corpus looked identical to a fresh export.
//
// Returns nil when the field is absent so an older daemon renders nothing
// rather than an empty block.
func signatureBundleLines(overview map[string]interface{}) []string {
	bundle, ok := overview["signature_bundle"].(map[string]interface{})
	if !ok || len(bundle) == 0 {
		return nil
	}

	field := func(label, value string) string {
		return fmt.Sprintf("    %-16s %s", label+":", value)
	}

	rawSource, _ := bundle["source"].(string)
	source := rawSource
	if path, _ := bundle["path"].(string); path != "" {
		source = fmt.Sprintf("%s (%s)", source, path)
	}

	lines := []string{
		"  Signature bundle:",
		field("Source", source),
	}
	if version, _ := bundle["bundle_version"].(string); version != "" {
		lines = append(lines, field("Version", version))
	}
	if generated, _ := bundle["generated_at"].(string); generated != "" {
		if days, ok := bundle["staleness_days"].(float64); ok {
			generated = fmt.Sprintf("%s (%d days old)", generated, int(days))
		}
		lines = append(lines, field("Generated", generated))
	}
	lines = append(lines, bundleFreshnessLines(bundle, field)...)
	if fingerprint, _ := bundle["fingerprint"].(string); fingerprint != "" {
		lines = append(lines, field("Fingerprint", fingerprint))
	}
	if digest, _ := bundle["full_digest"].(string); digest != "" {
		// Shown in FULL: it is the only value `security bundle rollback` accepts.
		lines = append(lines, field("Full digest", digest))
	}
	if seq, _ := bundle["sequence"].(string); seq != "" {
		lines = append(lines, field("Sequence", seq))
	}
	authority, _ := bundle["authority"].(string)
	verified, _ := bundle["signature_verified"].(bool)
	switch {
	case authority != "":
		auth := authority
		if epoch, ok := bundle["trust_epoch"].(float64); ok {
			auth = fmt.Sprintf("%s (epoch %d)", authority, int(epoch))
		}
		lines = append(lines, field("Authority", auth))
	case rawSource != "" && rawSource != "embedded":
		lines = append(lines, field("Authority", "none (unsigned external bundle)"))
	}
	switch {
	case verified:
		lines = append(lines, field("Signature", "verified"))
	case rawSource == "embedded":
		lines = append(lines, field("Signature", "binary-trusted (embedded default)"))
	case rawSource != "":
		lines = append(lines, field("Signature", "unsigned - not verified"))
	}
	lines = append(lines, field("Rules", fmt.Sprintf("%s runnable, %s skipped, %s declared-skipped",
		secFormatInt(bundle, "runnable_rules"),
		secFormatInt(bundle, "skipped_rules"),
		secFormatInt(bundle, "declared_skipped"))))
	if degraded, _ := bundle["coverage_degraded"].(bool); degraded {
		// scan-mode auto-approval is suspended while coverage is degraded; a
		// silent flag is indistinguishable from a quiet install.
		reason, _ := bundle["degraded_reason"].(string)
		if reason == "" {
			reason = "unspecified"
		}
		lines = append(lines, field("Coverage", "DEGRADED: "+reason+" (scan-mode auto-approval is suspended)"))
	}
	if stale, _ := bundle["stale"].(bool); stale {
		// An age number alone reads as trivia; a stale corpus is a missing-
		// signature risk, so it gets its own loud line (Spec 087 FR-017).
		lines = append(lines, field("STALE", bundleStaleText(bundle)))
	}
	if reprotect := bundleReprotectText(bundle); reprotect != "" {
		lines = append(lines, field("Re-protection", reprotect))
	}
	if pinned, _ := bundle["pinned"].(bool); pinned {
		lines = append(lines, field("Pinned", "yes - pinned by an operator rollback (`security bundle unpin` clears it)"))
	}
	if rollback, _ := bundle["rollback"].(bool); rollback {
		lines = append(lines, field("Rollback", "the serving bundle was activated by an explicit rollback"))
	}
	if loadErr, _ := bundle["load_error"].(string); loadErr != "" {
		// A configured bundle that failed to load keeps the previous corpus
		// live; say so loudly rather than letting the counts imply all is well.
		lines = append(lines, field("load error", loadErr))
	}
	if rej, ok := bundle["last_rejection"].(map[string]interface{}); ok && len(rej) > 0 {
		reason, _ := rej["reason"].(string)
		text := reason
		if detail, _ := rej["detail"].(string); detail != "" {
			text += ": " + detail
		}
		if at, _ := rej["at"].(string); at != "" {
			text += " (" + formatTimestamp(at) + ")"
		}
		lines = append(lines, field("Last rejection", text))
	}
	if runnable, _ := bundle["runnable_rules"].(float64); runnable == 0 {
		// Zero runnable rules means offline TPA coverage is OFF. Printed in the
		// same tone as a healthy count, it read as "fine" — the exact class of
		// "the runtime is right but the operator is misled" bug #938 is about.
		lines = append(lines, field("WARNING", "no TPA signatures are running — offline scan coverage is OFF"))
	}
	return append(lines, "")
}

// bundleFreshnessLines renders when the corpus was last checked and activated
// and how the last refresh cycle went (Spec 087 FR-017, tasks B15). Everything
// is optional: an older daemon, or an embedded default that never refreshed,
// yields no lines at all.
func bundleFreshnessLines(bundle map[string]interface{}, field func(label, value string) string) []string {
	var lines []string
	if checked, _ := bundle["last_checked_at"].(string); checked != "" {
		lines = append(lines, field("Last checked", formatTimestamp(checked)))
	}
	if activated, _ := bundle["last_activated_at"].(string); activated != "" {
		lines = append(lines, field("Last activated", formatTimestamp(activated)))
	}
	refresh, ok := bundle["refresh"].(map[string]interface{})
	if !ok || len(refresh) == 0 {
		return lines
	}
	outcome, _ := refresh["last_outcome"].(string)
	text := "no cycle has run yet"
	if outcome != "" {
		text = outcome
		if reason, _ := refresh["last_reason"].(string); reason != "" {
			text += " (" + reason + ")"
		}
	}
	if failures, _ := refresh["consecutive_failures"].(float64); failures > 0 {
		noun := "failures"
		if int(failures) == 1 {
			noun = "failure"
		}
		text += fmt.Sprintf(", %d consecutive %s", int(failures), noun)
	}
	if next, _ := refresh["next_check_at"].(string); next != "" {
		text += ", next check " + formatTimestamp(next)
	}
	lines = append(lines, field("Refresh", text))
	// The host is shown only while fetch is on and a host is known. The URL is
	// never reported by the daemon (host-only logging, tasks B12).
	if enabled, _ := refresh["fetch_enabled"].(bool); enabled {
		if host, _ := refresh["fetch_host"].(string); host != "" {
			lines = append(lines, field("Fetch host", host))
		}
	}
	return lines
}

// bundleStaleText is the STALE line: age, threshold and the way out.
func bundleStaleText(bundle map[string]interface{}) string {
	age := "older than the threshold"
	if days, ok := bundle["staleness_days"].(float64); ok {
		age = fmt.Sprintf("%d days old", int(days))
	}
	threshold := ""
	if after, ok := bundle["stale_after_days"].(float64); ok {
		threshold = fmt.Sprintf(" (threshold %d days)", int(after))
	}
	return age + threshold + " - run `mcpproxy security bundle refresh`, or check that the refresh source is reachable"
}

// bundleReprotectText describes the post-activation re-protection sweep (Spec 101
// FR-019): its state, why it is suspended, and how many scanned servers matched
// the newly active corpus. Empty when the daemon reports no sweep status.
func bundleReprotectText(bundle map[string]interface{}) string {
	rp, ok := bundle["reprotect"].(map[string]interface{})
	if !ok || len(rp) == 0 {
		return ""
	}
	state, _ := rp["state"].(string)
	if state == "" {
		return ""
	}
	text := state
	if reason, _ := rp["reason"].(string); reason != "" {
		text += " (" + reason + ")"
	}
	scanned, _ := rp["servers_scanned"].(float64)
	hits, _ := rp["servers_with_hits"].(float64)
	if scanned > 0 {
		text += fmt.Sprintf(", %d of %d servers matched the active corpus", int(hits), int(scanned))
	}
	if done, _ := rp["last_completed_at"].(string); done != "" {
		text += ", last completed " + formatLocalTimestamp(done)
	}
	return text
}

// formatLocalTimestamp is formatTimestamp rendered in the host's local zone.
// The daemon stamps the re-protection completion in UTC while the cycle
// timestamps carry the host's local offset, so the human line converts it to
// keep every time in the block on the same clock.
func formatLocalTimestamp(ts string) string {
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.Local().Format("2006-01-02 15:04:05")
		}
	}
	return ts
}

func secFormatInt(m map[string]interface{}, key string) string {
	if v, ok := m[key].(float64); ok {
		return fmt.Sprintf("%d", int(v))
	}
	return "0"
}

// --- security bundle (Spec 101 US1: status, history, rollback, unpin, reload; US3: refresh) ---

// bundleFullDigestPattern is the only digest grammar rollback accepts: the
// complete SHA-256 as 64 lowercase hex characters. The daemon enforces the same
// rule; checking here refuses a typo before any request is made.
var bundleFullDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func newSecurityBundleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Show and manage the offline TPA signature bundle",
		Long: `Show which TPA signature corpus this proxy is running and whether it is current
and verified, or stuck on last-known-good and why.

The status shows the full SHA-256 digest (the value "rollback" requires), the
sequence, the signing authority and trust epoch, whether the signature
verified, staleness (with a STALE line past the configured threshold), when the
bundle was last checked and activated, how the last refresh cycle went, the
post-activation re-protection sweep, a DEGRADED coverage line with its reason
when scan-mode auto-approval is suspended, the rollback pin, and the last
refused candidate.

Examples:
  mcpproxy security bundle
  mcpproxy security bundle -o json
  mcpproxy security bundle history
  mcpproxy security bundle rollback --embedded --digest <64-hex>
  mcpproxy security bundle rollback --path /var/lib/tpa/known-good.json --digest <64-hex>
  mcpproxy security bundle unpin
  mcpproxy security bundle reload
  mcpproxy security bundle refresh`,
		Args: cobra.NoArgs,
		RunE: runSecurityBundleStatus,
	}
	cmd.AddCommand(newSecurityBundleHistoryCmd())
	cmd.AddCommand(newSecurityBundleRollbackCmd())
	cmd.AddCommand(newSecurityBundleUnpinCmd())
	cmd.AddCommand(newSecurityBundleReloadCmd())
	cmd.AddCommand(newSecurityBundleRefreshCmd())
	return cmd
}

func newSecurityBundleHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "history",
		Short: "List the bundle activation history (oldest first)",
		Long: `List every bundle activation on this install, oldest first, with its full
SHA-256 digest. This is the trustworthy place to learn the digest of a
known-good artifact to name in "bundle rollback".

Examples:
  mcpproxy security bundle history
  mcpproxy security bundle history -o json`,
		Args: cobra.NoArgs,
		RunE: runSecurityBundleHistory,
	}
}

func newSecurityBundleRollbackCmd() *cobra.Command {
	var (
		embedded bool
		path     string
		digest   string
	)
	cmd := &cobra.Command{
		Use:   "rollback (--embedded | --path PATH) --digest <64-hex>",
		Short: "Roll the bundle back to a named artifact (overrides the downgrade watermark)",
		Long: `Roll the signature bundle back to an older artifact. This is the one operator
override of the anti-downgrade watermark, so it is authorized by the COMPLETE
SHA-256 digest of the target bytes (64 lowercase hex characters), never by a
path or the 12-character fingerprint. The path only says where to find the
bytes. Find full digests with "mcpproxy security bundle history".

Give exactly one of --embedded (the corpus compiled into this binary) or
--path. A refused rollback (for example rollback_target_mismatch) leaves the
last-known-good bundle serving and prints the reason.

Examples:
  mcpproxy security bundle rollback --embedded --digest <64-hex>
  mcpproxy security bundle rollback --path /var/lib/tpa/known-good.json --digest <64-hex>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateBundleRollbackFlags(embedded, path, cmd.Flags().Changed("path"), digest); err != nil {
				return err
			}
			return runSecurityBundleRollback(embedded, path, digest)
		},
	}
	cmd.Flags().BoolVar(&embedded, "embedded", false, "Roll back to the bundle compiled into this binary")
	cmd.Flags().StringVar(&path, "path", "", "Path to the target bundle file (a hint for where to find the bytes; a relative path is resolved against the current directory; it must sit directly in the directory of security.tpa_bundle_path)")
	cmd.Flags().StringVar(&digest, "digest", "", "Full SHA-256 of the target as 64 lowercase hex characters (required)")
	return cmd
}

func newSecurityBundleUnpinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unpin",
		Short: "Clear the rollback pin and return to normal downgrade protection",
		Long: `Clear the rollback pin and re-validate the serving bundle under the normal
watermark. A rolled-back target below the watermark no longer serves; the
install falls back as a restart would. A quiet no-op when nothing is pinned.

Examples:
  mcpproxy security bundle unpin`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSecurityBundleMutation("/api/v1/security/bundle/unpin", "unpin bundle", "Rollback pin cleared. Now serving:")
		},
	}
}

func newSecurityBundleReloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reload",
		Short: "Re-run the bundle pipeline against the configured source",
		Long: `Re-read the configured bundle source and run it through the same
verification pipeline as startup. A refused candidate leaves last-known-good
serving and is reported as the last rejection. Reload never makes a network
request; use "refresh" to also check the fetch channel.

Examples:
  mcpproxy security bundle reload`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSecurityBundleMutation("/api/v1/security/bundle/reload", "reload bundle", "Bundle reloaded. Now serving:")
		},
	}
}

func newSecurityBundleRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Check for a newer bundle now (local file, then the optional fetch)",
		Long: `Run one refresh cycle immediately: re-read the configured bundle file, then,
when security.tpa_fetch_enabled is on and a trusted signer and security.tpa_fetch_url
are configured, check the fetch channel for a newer signed bundle. Everything
goes through the same verification pipeline as startup; a refused candidate
leaves last-known-good serving and is reported as the last rejection.

The status shows what the cycle did on the "Refresh" line: activated, unchanged,
not_modified, rejected, fetch_unavailable (a network or rate-limit problem, never
a rejection), channel_invalid, superseded or disabled (with the reason).

"refresh" may make one outbound request when fetch is enabled; "reload" never
does - it only re-reads the local file.

Examples:
  mcpproxy security bundle refresh
  mcpproxy security bundle refresh -o json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runSecurityBundleMutation("/api/v1/security/bundle/refresh", "refresh bundle", "Bundle refresh finished. Now serving:")
		},
	}
}

// validateBundleRollbackFlags enforces the rollback grammar locally, so a
// malformed request is refused before any request is made.
func validateBundleRollbackFlags(embedded bool, path string, pathChanged bool, digest string) error {
	hasPath := pathChanged || path != ""
	if embedded == hasPath {
		return newFlagValidationError("give exactly one of --embedded or --path PATH")
	}
	if hasPath && path == "" {
		return newFlagValidationError("--path must not be empty")
	}
	if !bundleFullDigestPattern.MatchString(digest) {
		return newFlagValidationError("--digest must be the full SHA-256 of the target: 64 lowercase hex characters (got %d characters); the 12-character fingerprint is a label, not an authorization - the full digest is required, see `mcpproxy security bundle history`", len(digest))
	}
	return nil
}

// bundleDaemonRequest sends one bundle request and returns the status and body.
func bundleDaemonRequest(method, path string, body []byte, operation string) (int, []byte, error) {
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := client.DoRaw(ctx, method, path, body)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to %s: %w", operation, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}

// bundleData runs a bundle request that must answer 200 and returns the
// envelope's data payload.
func bundleData(method, path string, body []byte, operation string) ([]byte, error) {
	status, respBody, err := bundleDaemonRequest(method, path, body, operation)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, parseAPIError(respBody, status, operation)
	}
	data, err := unwrapAPIResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("API error: %w", err)
	}
	return data, nil
}

func printBundleStatus(bundle map[string]interface{}) {
	for _, line := range signatureBundleLines(map[string]interface{}{"signature_bundle": bundle}) {
		fmt.Println(line)
	}
}

func runSecurityBundleStatus(_ *cobra.Command, _ []string) error {
	data, err := bundleData(http.MethodGet, "/api/v1/security/bundle", nil, "get bundle status")
	if err != nil {
		return err
	}
	var bundle map[string]interface{}
	if err := json.Unmarshal(data, &bundle); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if format := ResolveOutputFormat(); format == "json" || format == "yaml" {
		return formatAndPrint(format, bundle)
	}
	printBundleStatus(bundle)
	return nil
}

func runSecurityBundleHistory(_ *cobra.Command, _ []string) error {
	data, err := bundleData(http.MethodGet, "/api/v1/security/bundle/history", nil, "get bundle history")
	if err != nil {
		return err
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(data, &rows); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if rows == nil {
		rows = []map[string]interface{}{}
	}
	if format := ResolveOutputFormat(); format == "json" || format == "yaml" {
		return formatAndPrint(format, rows)
	}
	if len(rows) == 0 {
		fmt.Println("No bundle activations recorded.")
		return nil
	}
	// The digest column is last and unpadded: it is the rollback input and must
	// never be truncated or wrapped by a column width.
	fmt.Printf("%-9s %-10s %-6s %-19s %-9s %-17s %s\n", "SEQUENCE", "AUTHORITY", "EPOCH", "ACTIVATED", "SOURCE", "FLAGS", "FULL DIGEST")
	for _, row := range rows {
		seq := getMapString(row, "sequence")
		if seq == "" {
			seq = "-"
		}
		authority := getMapString(row, "authority")
		if authority == "" {
			authority = "-"
		}
		var flags []string
		if v, _ := row["signature_verified"].(bool); v {
			flags = append(flags, "verified")
		}
		if v, _ := row["rollback"].(bool); v {
			flags = append(flags, "rollback")
		}
		flagText := strings.Join(flags, ",")
		if flagText == "" {
			flagText = "-"
		}
		fmt.Printf("%-9s %-10s %-6s %-19s %-9s %-17s %s\n",
			seq, authority, secFormatInt(row, "trust_epoch"),
			formatTimestamp(getMapString(row, "activated_at")),
			getMapString(row, "source"), flagText, getMapString(row, "full_digest"))
	}
	return nil
}

func runSecurityBundleRollback(embedded bool, path, digest string) error {
	// The daemon is another process with its own working directory, so a
	// relative path is resolved here, against the shell the operator typed it in.
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve --path %q: %w", path, err)
		}
		path = abs
	}
	reqBody, err := json.Marshal(map[string]interface{}{"embedded": embedded, "path": path, "digest": digest})
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	status, respBody, err := bundleDaemonRequest(http.MethodPost, "/api/v1/security/bundle/rollback", reqBody, "roll back bundle")
	if err != nil {
		return err
	}
	if status == http.StatusConflict {
		return bundleRollbackRefusal(respBody)
	}
	if status != http.StatusOK {
		return parseAPIError(respBody, status, "roll back bundle")
	}
	data, err := unwrapAPIResponse(respBody)
	if err != nil {
		return fmt.Errorf("API error: %w", err)
	}
	return printBundleMutationResult(data, "Rolled back and pinned. Now serving:")
}

// bundleRollbackRefusal turns the daemon's 409 into an error that names the
// rejection reason, so an operator can tell a wrong digest from a tamper
// attempt from a stale epoch.
func bundleRollbackRefusal(respBody []byte) error {
	var resp struct {
		Error     string `json:"error"`
		ErrorCode string `json:"error_code"`
		Rejection struct {
			Reason string `json:"reason"`
			Detail string `json:"detail"`
		} `json:"rejection"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return cliRefusalError{fmt.Errorf("bundle rollback refused: HTTP 409: %s", string(respBody))}
	}
	reason := resp.Rejection.Reason
	if reason == "" {
		reason = resp.ErrorCode
	}
	msg := "bundle rollback refused (" + reason + ")"
	if resp.Rejection.Detail != "" {
		msg += ": " + resp.Rejection.Detail
	} else if resp.Error != "" {
		msg += ": " + resp.Error
	}
	return cliRefusalError{fmt.Errorf("%s; the last-known-good bundle keeps serving", msg)}
}

func runSecurityBundleMutation(path, operation, successLine string) error {
	data, err := bundleData(http.MethodPost, path, nil, operation)
	if err != nil {
		return err
	}
	return printBundleMutationResult(data, successLine)
}

func printBundleMutationResult(data []byte, successLine string) error {
	var bundle map[string]interface{}
	if err := json.Unmarshal(data, &bundle); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if format := ResolveOutputFormat(); format == "json" || format == "yaml" {
		return formatAndPrint(format, bundle)
	}
	fmt.Println(successLine)
	printBundleStatus(bundle)
	return nil
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

// stderrIsTTY returns true when stderr is connected to an interactive
// terminal. Used to gate the rolling (\r) progress display, which is written
// to stderr so machine formats keep stdout parseable.
func stderrIsTTY() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
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

// serverAPIPath returns the REST path of a per-server sub-resource. The name is
// path-escaped: official-registry ids are namespace/name, and the daemon only
// decodes a %2F-encoded {id} (MCP-1118).
func serverAPIPath(serverName, suffix string) string {
	return "/api/v1/servers/" + url.PathEscape(serverName) + suffix
}
