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
	"go.uber.org/zap"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"
)

// StatusInfo holds the collected status data for display.
type StatusInfo struct {
	State         string           `json:"state"`
	Edition       string           `json:"edition"`
	ListenAddr    string           `json:"listen_addr"`
	Uptime        string           `json:"uptime,omitempty"`
	UptimeSeconds float64          `json:"uptime_seconds,omitempty"`
	APIKey        string           `json:"api_key"`
	WebUIURL      string           `json:"web_ui_url"`
	RoutingMode   string           `json:"routing_mode"`
	Servers       *ServerCounts    `json:"servers,omitempty"`
	SocketPath    string           `json:"socket_path,omitempty"`
	ConfigPath    string           `json:"config_path,omitempty"`
	Version       string           `json:"version,omitempty"`
	TeamsInfo     *TeamsStatusInfo `json:"teams,omitempty"`
}

// TeamsStatusInfo holds teams-specific status information.
type TeamsStatusInfo struct {
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

	if socket.IsSocketAvailable(socketPath) {
		return collectStatusFromDaemon(cfg, socketPath, configPath)
	}

	return collectStatusFromConfig(cfg, socketPath, configPath), nil
}

func collectStatusFromDaemon(cfg *config.Config, socketPath, configPath string) (*StatusInfo, error) {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	client := cliclient.NewClient(socketPath, logger.Sugar())
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

	// Add teams info if available
	info.TeamsInfo = collectTeamsInfo(cfg)

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
	}

	// Construct Web UI URL if not provided by daemon
	if info.WebUIURL == "" {
		info.WebUIURL = statusBuildWebUIURL(info.ListenAddr, cfg.APIKey)
	}

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
		ConfigPath:  configPath,
	}

	info.TeamsInfo = collectTeamsInfo(cfg)

	return info
}

func extractServerCounts(stats map[string]interface{}) *ServerCounts {
	counts := &ServerCounts{}

	if v, ok := stats["connected"].(float64); ok {
		counts.Connected = int(v)
	}
	if v, ok := stats["quarantined"].(float64); ok {
		counts.Quarantined = int(v)
	}
	if v, ok := stats["total"].(float64); ok {
		counts.Total = int(v)
	} else {
		counts.Total = counts.Connected + counts.Quarantined
	}

	return counts
}

// statusMaskAPIKey returns a masked version of the API key showing first and last 4 chars.
func statusMaskAPIKey(apiKey string) string {
	if len(apiKey) <= 8 {
		return "****"
	}
	return apiKey[:4] + "****" + apiKey[len(apiKey)-4:]
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
		fmt.Printf("  %-12s %s\n", "Version:", info.Version)
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

	if info.TeamsInfo != nil {
		fmt.Println()
		fmt.Println("Server Edition")
		fmt.Printf("  %-12s %s\n", "OAuth:", info.TeamsInfo.OAuthProvider)
		fmt.Printf("  %-12s %s\n", "Admins:", strings.Join(info.TeamsInfo.AdminEmails, ", "))
	}
}

func loadStatusConfig() (*config.Config, error) {
	if configFile != "" {
		cfg, err := config.LoadFromFile(configFile)
		if err != nil {
			return nil, err
		}
		if dataDir != "" {
			cfg.DataDir = dataDir
		}
		return cfg, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	return cfg, nil
}
