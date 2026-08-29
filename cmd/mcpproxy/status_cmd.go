package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck"
)

// StatusInfo holds the collected status data for display.
type StatusInfo struct {
	State             string                   `json:"state"`
	Edition           string                   `json:"edition"`
	ListenAddr        string                   `json:"listen_addr"`
	Uptime            string                   `json:"uptime,omitempty"`
	UptimeSeconds     float64                  `json:"uptime_seconds,omitempty"`
	APIKey            string                   `json:"api_key"`
	WebUIURL          string                   `json:"web_ui_url"`
	RoutingMode       string                   `json:"routing_mode"`
	Endpoints         map[string]string        `json:"endpoints"`
	Servers           *ServerCounts            `json:"servers,omitempty"`
	SocketPath        string                   `json:"socket_path,omitempty"`
	ConfigPath        string                   `json:"config_path,omitempty"`
	Version           string                   `json:"version,omitempty"`
	LaunchedBy        string                   `json:"launched_by,omitempty"` // Spec 092 FR-001a; empty = user-launched/unknown or older daemon
	Update            *StatusUpdateInfo        `json:"update,omitempty"`
	ServerEditionInfo *ServerEditionStatusInfo `json:"server_edition,omitempty"`
}

// StatusUpdateInfo mirrors the `update` object of GET /api/v1/info
// (internal/updatecheck.InfoResponseUpdate) for status output. The daemon's
// background checker is the single source of truth; status only renders it.
type StatusUpdateInfo struct {
	Available      bool   `json:"available"`
	LatestVersion  string `json:"latest_version,omitempty"`
	ReleaseURL     string `json:"release_url,omitempty"`
	CheckedAt      string `json:"checked_at,omitempty"` // RFC 3339, as serialized by the daemon
	IsPrerelease   bool   `json:"is_prerelease,omitempty"`
	CheckError     string `json:"check_error,omitempty"`
	InstallChannel string `json:"install_channel,omitempty"` // Spec 079 FR-008
	UpdateCommand  string `json:"update_command,omitempty"`  // Spec 079 FR-009

	// BehindSummary is the daemon-rendered "N releases / M weeks behind"
	// clause (Spec 079 FR-002). Empty against a daemon too old to send it, or
	// when the delta could not be resolved — status then prints the same line
	// it printed before the delta existed.
	BehindSummary string `json:"behind_summary,omitempty"`
	// The raw figures behind that clause, for `status -o json` consumers that
	// would otherwise have to parse prose.
	ReleasesBehind          *int `json:"releases_behind,omitempty"`
	ReleasesBehindSaturated bool `json:"releases_behind_saturated,omitempty"`
	WeeksBehind             *int `json:"weeks_behind,omitempty"`
}

// ServerEditionStatusInfo holds server-edition-specific status information.
type ServerEditionStatusInfo struct {
	OAuthProvider string   `json:"oauth_provider"`
	AdminEmails   []string `json:"admin_emails"`
}

// ServerCounts holds upstream server statistics.
type ServerCounts struct {
	Connected   int `json:"connected"`
	Quarantined int `json:"quarantined"`
	Total       int `json:"total"`
}

var (
	statusShowKey  bool
	statusWebURL   bool
	statusResetKey bool
)

// GetStatusCommand returns the status cobra command.
func GetStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show MCPProxy status, API key, and Web UI URL",
		Long: `Display the current state of the MCPProxy proxy including running status,
listen address, API key (masked by default), Web UI URL, and server statistics.

Examples:
  mcpproxy status                  # Show status with masked API key
  mcpproxy status --show-key       # Show full API key
  mcpproxy status --web-url        # Print only the Web UI URL (for piping)
  mcpproxy status --reset-key      # Regenerate API key
  mcpproxy status -o json          # JSON output`,
		RunE: runStatus,
	}

	cmd.Flags().BoolVar(&statusShowKey, "show-key", false, "Show full unmasked API key")
	cmd.Flags().BoolVar(&statusWebURL, "web-url", false, "Print only the Web UI URL (for piping to open)")
	cmd.Flags().BoolVar(&statusResetKey, "reset-key", false, "Regenerate API key and save to config")

	return cmd
}

func runStatus(cmd *cobra.Command, _ []string) error {
	cfg, err := loadStatusConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Ensure API key exists
	cfg.EnsureAPIKey()

	configPath := config.GetConfigPath(cfg.DataDir)

	// Handle --reset-key first (before any display)
	if statusResetKey {
		newKey, resetErr := resetAPIKey(cfg, configPath)
		if resetErr != nil {
			return fmt.Errorf("failed to reset API key: %w", resetErr)
		}

		// Print warning about HTTP clients
		fmt.Fprintln(os.Stderr, "Warning: Resetting the API key will disconnect any HTTP clients using the current key.")
		fmt.Fprintln(os.Stderr, "         Socket connections (tray app) are NOT affected.")
		fmt.Fprintln(os.Stderr)

		// Check if env var overrides
		if envKey, exists := os.LookupEnv("MCPPROXY_API_KEY"); exists && envKey != "" {
			fmt.Fprintln(os.Stderr, "Warning: MCPPROXY_API_KEY environment variable is set and will override the config file key.")
			fmt.Fprintln(os.Stderr)
		}

		fmt.Fprintf(os.Stderr, "New API key: %s\n", newKey)
		fmt.Fprintf(os.Stderr, "Saved to: %s\n", configPath)
		fmt.Fprintln(os.Stderr)

		// Update config with new key for subsequent display
		cfg.APIKey = newKey
		// Implicit --show-key with --reset-key
		statusShowKey = true
	}

	// Collect status info
	info, err := collectStatus(cfg, configPath)
	if err != nil {
		return err
	}

	// Apply key masking based on flags
	if !statusShowKey {
		info.APIKey = statusMaskAPIKey(info.APIKey)
	}

	// Handle --web-url: print only the URL and exit
	if statusWebURL {
		fmt.Println(info.WebUIURL)
		return nil
	}

	// Format and print output
	format := clioutput.ResolveFormat(globalOutputFormat, globalJSONOutput)
	return printStatusOutput(info, format)
}

func collectStatus(cfg *config.Config, configPath string) (*StatusInfo, error) {
	socketPath := socket.DetectSocketPath(cfg.DataDir)

	// Daemon detection: socket first, then TCP fallback (cfg.Listen + API key).
	if client, ok := newDaemonClient(cfg, nil); ok {
		return collectStatusFromDaemon(cfg, client, socketPath, configPath)
	}

	return collectStatusFromConfig(cfg, socketPath, configPath), nil
}

func collectStatusFromDaemon(cfg *config.Config, client *cliclient.Client, socketPath, configPath string) (*StatusInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info := &StatusInfo{
		State:       "Running",
		Edition:     Edition,
		APIKey:      cfg.APIKey,
		RoutingMode: cfg.RoutingMode,
		SocketPath:  socketPath,
		ConfigPath:  configPath,
	}

	// Apply routing mode default if empty
	if info.RoutingMode == "" {
		info.RoutingMode = config.RoutingModeRetrieveTools
	}

	// Add server edition info if available
	info.ServerEditionInfo = collectServerEditionInfo(cfg)

	// Get status data (running, listen_addr, upstream_stats)
	statusData, err := client.GetStatus(ctx)
	if err != nil {
		// Fall back to config-only mode if daemon query fails
		return collectStatusFromConfig(cfg, socketPath, configPath), nil
	}

	if addr, ok := statusData["listen_addr"].(string); ok {
		info.ListenAddr = addr
	} else {
		info.ListenAddr = cfg.Listen
	}

	// Extract upstream stats
	if stats, ok := statusData["upstream_stats"].(map[string]interface{}); ok {
		info.Servers = extractServerCounts(stats)
	}

	// Calculate uptime from started_at if available
	if startedAt, ok := statusData["started_at"].(string); ok {
		if t, parseErr := time.Parse(time.RFC3339, startedAt); parseErr == nil {
			uptime := time.Since(t)
			info.Uptime = statusFormatDuration(uptime)
			info.UptimeSeconds = uptime.Seconds()
		}
	}

	// Get info data (version, web_ui_url)
	infoData, err := client.GetInfo(ctx)
	if err == nil {
		if v, ok := infoData["version"].(string); ok {
			info.Version = v
		}
		if url, ok := infoData["web_ui_url"].(string); ok {
			info.WebUIURL = url
		}
		// Spec 092 FR-001a: durable launch provenance of the running core.
		// Older daemons omit the field entirely — rendered as absent, not as
		// "user-launched", because we cannot tell the two apart.
		if v, ok := infoData["launched_by"].(string); ok {
			info.LaunchedBy = v
		}
		info.Update = extractStatusUpdate(infoData)
	}

	// Construct Web UI URL if not provided by daemon
	if info.WebUIURL == "" {
		info.WebUIURL = statusBuildWebUIURL(info.ListenAddr, cfg.APIKey)
	}

	// Build MCP endpoint URLs
	info.Endpoints = statusBuildEndpoints(info.ListenAddr)

	return info, nil
}

func collectStatusFromConfig(cfg *config.Config, socketPath, configPath string) *StatusInfo {
	listenAddr := cfg.Listen
	if listenAddr == "" {
		listenAddr = "127.0.0.1:8080"
	}

	routingMode := cfg.RoutingMode
	if routingMode == "" {
		routingMode = config.RoutingModeRetrieveTools
	}

	info := &StatusInfo{
		State:       "Not running",
		Edition:     Edition,
		ListenAddr:  listenAddr + " (configured)",
		APIKey:      cfg.APIKey,
		WebUIURL:    statusBuildWebUIURL(listenAddr, cfg.APIKey),
		RoutingMode: routingMode,
		Endpoints:   statusBuildEndpoints(listenAddr),
		ConfigPath:  configPath,
	}

	info.ServerEditionInfo = collectServerEditionInfo(cfg)

	return info
}

func extractServerCounts(stats map[string]interface{}) *ServerCounts {
	counts := &ServerCounts{}

	// The daemon emits connected_servers/quarantined_servers/total_servers
	// (see the GetStats builders in internal/server and internal/upstream);
	// bare connected/quarantined/total are accepted for older daemons.
	counts.Connected = statsInt(stats, "connected_servers", "connected")
	counts.Quarantined = statsInt(stats, "quarantined_servers", "quarantined")
	if v, ok := statsIntOK(stats, "total_servers", "total"); ok {
		counts.Total = v
	} else {
		counts.Total = counts.Connected + counts.Quarantined
	}

	return counts
}

// statsInt returns the first of the given keys present in stats as an int.
func statsInt(stats map[string]interface{}, keys ...string) int {
	v, _ := statsIntOK(stats, keys...)
	return v
}

func statsIntOK(stats map[string]interface{}, keys ...string) (int, bool) {
	for _, key := range keys {
		switch v := stats[key].(type) {
		case float64:
			return int(v), true
		case int:
			return v, true
		}
	}
	return 0, false
}

// extractStatusUpdate pulls the `update` object out of the /api/v1/info
// payload. Returns nil when the daemon did not report update state.
func extractStatusUpdate(infoData map[string]interface{}) *StatusUpdateInfo {
	updateData, ok := infoData["update"].(map[string]interface{})
	if !ok {
		return nil
	}

	u := &StatusUpdateInfo{}
	if v, ok := updateData["available"].(bool); ok {
		u.Available = v
	}
	if v, ok := updateData["latest_version"].(string); ok {
		u.LatestVersion = v
	}
	if v, ok := updateData["release_url"].(string); ok {
		u.ReleaseURL = v
	}
	if v, ok := updateData["checked_at"].(string); ok {
		u.CheckedAt = v
	}
	if v, ok := updateData["is_prerelease"].(bool); ok {
		u.IsPrerelease = v
	}
	if v, ok := updateData["check_error"].(string); ok {
		u.CheckError = v
	}
	if v, ok := updateData["install_channel"].(string); ok {
		u.InstallChannel = v
	}
	if v, ok := updateData["update_command"].(string); ok {
		u.UpdateCommand = v
	}
	// Spec 079 FR-002. Absent from an older daemon, which is exactly the
	// daemon most likely to be running here — treat absence as "no delta",
	// never as an error.
	if v, ok := updateData["behind_summary"].(string); ok {
		u.BehindSummary = v
	}
	if v, ok := updateData["releases_behind"].(float64); ok {
		n := int(v)
		u.ReleasesBehind = &n
	}
	if v, ok := updateData["releases_behind_saturated"].(bool); ok {
		u.ReleasesBehindSaturated = v
	}
	if v, ok := updateData["weeks_behind"].(float64); ok {
		n := int(v)
		u.WeeksBehind = &n
	}
	return u
}

// statusVersionSuffix renders the update annotation appended to the Version
// line, mirroring doctor's presentation. A failed or not-yet-completed check
// renders nothing (quiet on failure; the error stays in JSON for diagnostics).
//
// Spec 079 FR-002/FR-003: the "N releases / M weeks behind" delta is appended
// here when the daemon reports one. The clause itself is authored by the
// daemon (updatecheck.FormatBehindSummary) rather than assembled here, so
// status, doctor, the startup log, the Web UI banner and the tray cannot word
// it differently. An older daemon omits it and this renders the legacy form.
func statusVersionSuffix(u *StatusUpdateInfo) string {
	if u == nil || u.CheckError != "" {
		return ""
	}
	if u.Available && u.LatestVersion != "" {
		behind := ""
		if u.BehindSummary != "" {
			behind = ", " + u.BehindSummary
		}
		// Spec 079 US2 (FR-009): append the channel's exact one-line update
		// command, or the channel-appropriate guidance when no command is
		// safe. Older daemons omit install_channel — render the legacy form.
		action := ""
		switch {
		case u.UpdateCommand != "":
			action = " — Run: " + u.UpdateCommand
		case u.InstallChannel != "":
			// The release URL already appears in the suffix; pass "" so the
			// guidance says "the releases page" instead of repeating it.
			if g := updatecheck.GuidanceLine(u.InstallChannel, ""); g != "" {
				action = " — " + g
			}
		}
		if u.ReleaseURL != "" {
			return fmt.Sprintf(" (update available: %s%s — %s%s)", u.LatestVersion, behind, u.ReleaseURL, action)
		}
		return fmt.Sprintf(" (update available: %s%s%s)", u.LatestVersion, behind, action)
	}
	if u.LatestVersion != "" {
		// A successful check confirmed we are current.
		return " (latest)"
	}
	return ""
}

// statusMaskAPIKey returns a masked version of the API key showing first and last 4 chars.
func statusMaskAPIKey(apiKey string) string {
	if len(apiKey) <= 8 {
		return "****"
	}
	return apiKey[:4] + "****" + apiKey[len(apiKey)-4:]
}

// statusBuildEndpoints constructs the MCP endpoint URLs map.
func statusBuildEndpoints(listenAddr string) map[string]string {
	addr := listenAddr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	base := "http://" + addr
	return map[string]string{
		"default":        base + "/mcp",
		"retrieve_tools": base + "/mcp/call",
		"direct":         base + "/mcp/all",
		"code_execution": base + "/mcp/code",
	}
}

// statusBuildWebUIURL constructs the Web UI URL with embedded API key.
func statusBuildWebUIURL(listenAddr, apiKey string) string {
	addr := listenAddr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	if apiKey != "" {
		return fmt.Sprintf("http://%s/ui/?apikey=%s", addr, apiKey)
	}
	return fmt.Sprintf("http://%s/ui/", addr)
}

func statusFormatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func resetAPIKey(cfg *config.Config, configPath string) (string, error) {
	// Generate new cryptographic key (256-bit)
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", fmt.Errorf("failed to generate random key: %w", err)
	}
	newKey := hex.EncodeToString(keyBytes)

	// Update config and save
	cfg.APIKey = newKey
	if err := config.SaveConfig(cfg, configPath); err != nil {
		return "", fmt.Errorf("failed to save config: %w", err)
	}

	return newKey, nil
}

func printStatusOutput(info *StatusInfo, format string) error {
	switch format {
	case "json":
		return printStatusJSON(info)
	case "yaml":
		formatter, err := clioutput.NewFormatter("yaml")
		if err != nil {
			return err
		}
		output, err := formatter.Format(info)
		if err != nil {
			return err
		}
		fmt.Println(output)
		return nil
	default:
		printStatusTable(info)
		return nil
	}
}

func printStatusJSON(info *StatusInfo) error {
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal status: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func printStatusTable(info *StatusInfo) {
	fmt.Println("MCPProxy Status")

	fmt.Printf("  %-12s %s\n", "State:", info.State)
	fmt.Printf("  %-12s %s\n", "Edition:", info.Edition)

	if info.Version != "" {
		fmt.Printf("  %-12s %s%s\n", "Version:", info.Version, statusVersionSuffix(info.Update))
	}

	// Spec 092 FR-001a. Only rendered when the core asserted a marker: an
	// empty value means user-launched/unknown (or a pre-092 daemon), and
	// printing "unknown" for the ordinary `mcpproxy serve` case would be
	// noise on every status call.
	if info.LaunchedBy != "" {
		fmt.Printf("  %-12s %s\n", "Launched by:", info.LaunchedBy)
	}

	fmt.Printf("  %-12s %s\n", "Listen:", info.ListenAddr)

	if info.Uptime != "" {
		fmt.Printf("  %-12s %s\n", "Uptime:", info.Uptime)
	}

	fmt.Printf("  %-12s %s\n", "API Key:", info.APIKey)
	fmt.Printf("  %-12s %s\n", "Routing:", info.RoutingMode)
	fmt.Printf("  %-12s %s\n", "Web UI:", info.WebUIURL)

	if info.Servers != nil {
		fmt.Printf("  %-12s %d connected, %d quarantined\n", "Servers:", info.Servers.Connected, info.Servers.Quarantined)
	}

	if info.SocketPath != "" {
		fmt.Printf("  %-12s %s\n", "Socket:", info.SocketPath)
	}

	if info.ConfigPath != "" {
		fmt.Printf("  %-12s %s\n", "Config:", info.ConfigPath)
	}

	if info.Endpoints != nil {
		fmt.Println()
		fmt.Println("MCP Endpoints")
		if v, ok := info.Endpoints["default"]; ok {
			fmt.Printf("  %-16s %s  (default, %s mode)\n", "/mcp", v, info.RoutingMode)
		}
		if v, ok := info.Endpoints["retrieve_tools"]; ok {
			fmt.Printf("  %-16s %s  (retrieve + call tools)\n", "/mcp/call", v)
		}
		if v, ok := info.Endpoints["direct"]; ok {
			fmt.Printf("  %-16s %s  (all tools, direct access)\n", "/mcp/all", v)
		}
		if v, ok := info.Endpoints["code_execution"]; ok {
			fmt.Printf("  %-16s %s  (code execution)\n", "/mcp/code", v)
		}
	}

	if info.ServerEditionInfo != nil {
		fmt.Println()
		fmt.Println("Server Edition")
		fmt.Printf("  %-12s %s\n", "OAuth:", info.ServerEditionInfo.OAuthProvider)
		fmt.Printf("  %-12s %s\n", "Admins:", strings.Join(info.ServerEditionInfo.AdminEmails, ", "))
	}
}

func loadStatusConfig() (*config.Config, error) {
	return loadCLIConfig(configFile)
}
