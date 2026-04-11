package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

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
	}

	return nil
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
