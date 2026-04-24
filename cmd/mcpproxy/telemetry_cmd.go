package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.etcd.io/bbolt"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// TelemetryStatus holds status data for display.
type TelemetryStatus struct {
	Enabled         bool   `json:"enabled"`
	AnonymousID     string `json:"anonymous_id,omitempty"`
	Endpoint        string `json:"endpoint"`
	EnvOverride     bool   `json:"env_override,omitempty"`
	EnvOverrideName string `json:"env_override_name,omitempty"`
	// Spec 044 (T042): activation funnel snapshot, rendered from the BBolt
	// store when reachable. Omitted (nil) when the DB is locked by a running
	// daemon or not present.
	Activation *telemetry.ActivationState `json:"activation,omitempty"`
}

// GetTelemetryCommand returns the telemetry management command.
func GetTelemetryCommand() *cobra.Command {
	telemetryCmd := &cobra.Command{
		Use:   "telemetry",
		Short: "Manage anonymous usage telemetry",
		Long: `Manage anonymous usage telemetry for MCPProxy.

Telemetry sends anonymous, non-identifiable usage statistics to help
improve MCPProxy. No personal data, tool names, or server details are
ever transmitted.

Examples:
  mcpproxy telemetry status    # Show telemetry status
  mcpproxy telemetry enable    # Enable telemetry
  mcpproxy telemetry disable   # Disable telemetry`,
	}

	telemetryCmd.AddCommand(getTelemetryStatusCommand())
	telemetryCmd.AddCommand(getTelemetryEnableCommand())
	telemetryCmd.AddCommand(getTelemetryDisableCommand())
	telemetryCmd.AddCommand(getTelemetryShowPayloadCommand())

	return telemetryCmd
}

func getTelemetryShowPayloadCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show-payload",
		Short: "Print the next telemetry payload as JSON (requires running daemon)",
		Long: `Print the exact JSON heartbeat payload that mcpproxy would next
send to the telemetry endpoint, without making any network call. Counters in
the payload reflect the current in-memory state of the running daemon. Spec 042.

Use this command to audit what telemetry mcpproxy collects on your install.

Requires the daemon to be running so runtime stats (server_count,
connected_server_count, tool_count, surface_requests, etc.) are populated.
Start the daemon with: mcpproxy serve`,
		RunE: runTelemetryShowPayload,
	}
}

func runTelemetryShowPayload(_ *cobra.Command, _ []string) error {
	cfg, err := loadTelemetryConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Require running daemon so runtime stats are populated. Offline mode
	// would emit zero-valued runtime fields and mislead users.
	socketPath := socket.DetectSocketPath(cfg.DataDir)
	if !socket.IsSocketAvailable(socketPath) {
		return fmt.Errorf("telemetry show-payload requires running daemon. Start with: mcpproxy serve")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := cliclient.NewClient(socketPath, nil)
	payload, err := client.GetTelemetryPayload(ctx)
	if err != nil {
		return fmt.Errorf("failed to get telemetry payload from daemon: %w", err)
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func getTelemetryStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show telemetry status",
		RunE:  runTelemetryStatus,
	}
}

func getTelemetryEnableCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "enable",
		Short: "Enable anonymous telemetry",
		RunE:  runTelemetryEnable,
	}
}

func getTelemetryDisableCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Disable anonymous telemetry",
		RunE:  runTelemetryDisable,
	}
}

func runTelemetryStatus(cmd *cobra.Command, _ []string) error {
	cfg, err := loadTelemetryConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	status := TelemetryStatus{
		Enabled:  cfg.IsTelemetryEnabled(),
		Endpoint: cfg.GetTelemetryEndpoint(),
	}

	if id := cfg.GetAnonymousID(); id != "" {
		status.AnonymousID = id
	}

	// Spec 042: env vars override config (DO_NOT_TRACK > CI > MCPPROXY_TELEMETRY).
	if disabled, reason := telemetry.IsDisabledByEnv(); disabled {
		status.EnvOverride = true
		status.EnvOverrideName = string(reason)
		status.Enabled = false
	}

	// Spec 044 (T042): try to render the activation funnel from the BBolt
	// store when the DB is reachable. If a daemon has the DB locked, we
	// silently omit — the same data is available via `/api/v1/status` when
	// the daemon is running.
	if snap, ok := loadActivationSnapshot(cfg.DataDir); ok {
		status.Activation = &snap
	}

	format := clioutput.ResolveFormat(globalOutputFormat, globalJSONOutput)
	switch format {
	case "json":
		data, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	case "yaml":
		formatter, err := clioutput.NewFormatter("yaml")
		if err != nil {
			return err
		}
		output, err := formatter.Format(status)
		if err != nil {
			return err
		}
		fmt.Println(output)
	default:
		fmt.Println("Telemetry Status")
		enabledStr := "Enabled"
		if !status.Enabled {
			enabledStr = "Disabled"
		}
		fmt.Printf("  %-14s %s\n", "Status:", enabledStr)
		if status.EnvOverride {
			fmt.Printf("  %-14s %s\n", "Override:", status.EnvOverrideName)
		}
		if status.AnonymousID != "" {
			fmt.Printf("  %-14s %s\n", "Anonymous ID:", status.AnonymousID)
		}
		fmt.Printf("  %-14s %s\n", "Endpoint:", status.Endpoint)
		if status.Activation != nil {
			a := status.Activation
			fmt.Println()
			fmt.Println("Activation Funnel")
			fmt.Printf("  %-28s %v\n", "first_connected_server:", a.FirstConnectedServerEver)
			fmt.Printf("  %-28s %v\n", "first_mcp_client:", a.FirstMCPClientEver)
			fmt.Printf("  %-28s %v\n", "first_retrieve_tools:", a.FirstRetrieveToolsCallEver)
			fmt.Printf("  %-28s %d\n", "retrieve_tools_calls_24h:", a.RetrieveToolsCalls24h)
			fmt.Printf("  %-28s %s\n", "tokens_saved_24h_bucket:", a.EstimatedTokensSaved24hBucket)
			if len(a.MCPClientsSeenEver) > 0 {
				fmt.Printf("  %-28s %s\n", "mcp_clients_seen_ever:", strings.Join(a.MCPClientsSeenEver, ", "))
			}
			if a.ConfiguredIDECount > 0 {
				fmt.Printf("  %-28s %d\n", "configured_ide_count:", a.ConfiguredIDECount)
			}
		}
	}

	return nil
}

// loadActivationSnapshot attempts to open the BBolt DB in read-only mode at
// the standard path and load the activation bucket. Returns (zero, false)
// when the DB file does not exist, is locked (daemon running), or read errs.
// We use a short Timeout so a locked DB fails fast rather than hanging the
// CLI.
func loadActivationSnapshot(dataDir string) (telemetry.ActivationState, bool) {
	if dataDir == "" {
		return telemetry.ActivationState{}, false
	}
	dbPath := filepath.Join(dataDir, "config.db")
	if _, err := os.Stat(dbPath); err != nil {
		return telemetry.ActivationState{}, false
	}
	db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{Timeout: 200 * time.Millisecond, ReadOnly: true})
	if err != nil {
		return telemetry.ActivationState{}, false
	}
	defer db.Close()
	store := telemetry.NewActivationStore()
	st, err := store.Load(db)
	if err != nil {
		return telemetry.ActivationState{}, false
	}
	return st, true
}

func runTelemetryEnable(cmd *cobra.Command, _ []string) error {
	cfg, err := loadTelemetryConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if cfg.Telemetry == nil {
		cfg.Telemetry = &config.TelemetryConfig{}
	}
	enabled := true
	cfg.Telemetry.Enabled = &enabled

	configPath := telemetryConfigSavePath(cfg)
	if err := config.SaveConfig(cfg, configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println("Telemetry enabled.")
	if os.Getenv("MCPPROXY_TELEMETRY") == "false" {
		fmt.Println("Warning: MCPPROXY_TELEMETRY=false environment variable is set and will override this setting.")
	}
	return nil
}

func runTelemetryDisable(cmd *cobra.Command, _ []string) error {
	cfg, err := loadTelemetryConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if cfg.Telemetry == nil {
		cfg.Telemetry = &config.TelemetryConfig{}
	}
	disabled := false
	cfg.Telemetry.Enabled = &disabled

	configPath := telemetryConfigSavePath(cfg)
	if err := config.SaveConfig(cfg, configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println("Telemetry disabled.")
	return nil
}

func loadTelemetryConfig() (*config.Config, error) {
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

// telemetryConfigSavePath returns the config path that telemetry subcommands
// should write to. It mirrors loadTelemetryConfig: when the user passed
// --config, that exact file is used; otherwise the default derived from
// DataDir. This fixes a bug where enable/disable always wrote to the default
// location regardless of --config (pre-existing from PR #345 / Spec 036).
func telemetryConfigSavePath(cfg *config.Config) string {
	if configFile != "" {
		return configFile
	}
	return config.GetConfigPath(cfg.DataDir)
}
