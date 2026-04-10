package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// TelemetryStatus holds status data for display.
type TelemetryStatus struct {
	Enabled     bool   `json:"enabled"`
	AnonymousID string `json:"anonymous_id,omitempty"`
	Endpoint    string `json:"endpoint"`
	EnvOverride bool   `json:"env_override,omitempty"`
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
		Short: "Print the next telemetry payload as JSON (no network call)",
		Long: `Print the exact JSON heartbeat payload that mcpproxy would next
send to the telemetry endpoint, without making any network call. Counters in
the payload reflect the current in-memory state. Spec 042.

Use this command to audit what telemetry mcpproxy collects on your install.`,
		RunE: runTelemetryShowPayload,
	}
}

func runTelemetryShowPayload(_ *cobra.Command, _ []string) error {
	cfg, err := loadTelemetryConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	configPath := config.GetConfigPath(cfg.DataDir)

	// Build a non-running telemetry service. We never call Start, so no
	// goroutine is launched and no network call is made.
	svc := telemetry.New(cfg, configPath, httpapi.GetBuildVersion(), Edition, zap.NewNop())
	payload := svc.BuildPayload()
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

	if os.Getenv("MCPPROXY_TELEMETRY") == "false" {
		status.EnvOverride = true
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
			fmt.Printf("  %-14s %s\n", "Override:", "MCPPROXY_TELEMETRY=false")
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

	configPath := config.GetConfigPath(cfg.DataDir)
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

	configPath := config.GetConfigPath(cfg.DataDir)
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
