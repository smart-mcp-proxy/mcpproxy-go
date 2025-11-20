package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"mcpproxy-go/internal/cliclient"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/socket"
)

var (
	upstreamCmd = &cobra.Command{
		Use:   "upstream",
		Short: "Manage upstream MCP servers",
		Long:  "Commands for managing and monitoring upstream MCP servers",
	}

	upstreamListCmd = &cobra.Command{
		Use:   "list",
		Short: "List all upstream servers with status",
		Long: `List all configured upstream MCP servers with connection status, tool counts, and errors.

Examples:
  mcpproxy upstream list
  mcpproxy upstream list --output=json
  mcpproxy upstream list --log-level=debug`,
		RunE: runUpstreamList,
	}

	// Command flags
	upstreamOutputFormat string
	upstreamLogLevel     string
	upstreamConfigPath   string
)

// GetUpstreamCommand returns the upstream command for adding to the root command
func GetUpstreamCommand() *cobra.Command {
	return upstreamCmd
}

func init() {
	// Add subcommands
	upstreamCmd.AddCommand(upstreamListCmd)

	// Define flags
	upstreamListCmd.Flags().StringVarP(&upstreamOutputFormat, "output", "o", "table", "Output format (table, json)")
	upstreamListCmd.Flags().StringVarP(&upstreamLogLevel, "log-level", "l", "warn", "Log level (trace, debug, info, warn, error)")
	upstreamListCmd.Flags().StringVarP(&upstreamConfigPath, "config", "c", "", "Path to MCP configuration file")
}

func runUpstreamList(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadUpstreamConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return err
	}

	// Create logger
	logger, err := createUpstreamLogger(upstreamLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return err
	}

	// Check if daemon is running
	if shouldUseUpstreamDaemon(globalConfig.DataDir) {
		logger.Info("Detected running daemon, using client mode via socket")
		return runUpstreamListClientMode(ctx, globalConfig.DataDir, logger)
	}

	// No daemon - load from config file
	logger.Info("No daemon detected, reading from config file")
	return runUpstreamListFromConfig(globalConfig)
}

func shouldUseUpstreamDaemon(dataDir string) bool {
	socketPath := socket.DetectSocketPath(dataDir)
	return socket.IsSocketAvailable(socketPath)
}

func runUpstreamListClientMode(ctx context.Context, dataDir string, logger *zap.Logger) error {
	socketPath := socket.DetectSocketPath(dataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Call GET /api/v1/servers
	servers, err := client.GetServers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get servers from daemon: %w", err)
	}

	return outputServers(servers)
}

func runUpstreamListFromConfig(globalConfig *config.Config) error {
	// Convert config servers to output format
	servers := make([]map[string]interface{}, len(globalConfig.Servers))
	for i, srv := range globalConfig.Servers {
		servers[i] = map[string]interface{}{
			"name":       srv.Name,
			"enabled":    srv.Enabled,
			"protocol":   srv.Protocol,
			"connected":  false,
			"tool_count": 0,
			"status":     "unknown (daemon not running)",
		}
	}

	return outputServers(servers)
}

func outputServers(servers []map[string]interface{}) error {
	switch upstreamOutputFormat {
	case "json":
		output, err := json.MarshalIndent(servers, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}
		fmt.Println(string(output))
	case "table", "":
		// Table format (default)
		fmt.Printf("%-25s %-10s %-10s %-12s %-10s %s\n",
			"NAME", "ENABLED", "PROTOCOL", "CONNECTED", "TOOLS", "STATUS")
		fmt.Printf("%-25s %-10s %-10s %-12s %-10s %s\n",
			"====", "=======", "========", "=========", "=====", "======")

		for _, srv := range servers {
			name := getStringField(srv, "name")
			enabled := getBoolField(srv, "enabled")
			protocol := getStringField(srv, "protocol")
			connected := getBoolField(srv, "connected")
			toolCount := getIntField(srv, "tool_count")
			status := getStringField(srv, "status")

			enabledStr := "no"
			if enabled {
				enabledStr = "yes"
			}

			connectedStr := "no"
			if connected {
				connectedStr = "yes"
			}

			fmt.Printf("%-25s %-10s %-10s %-12s %-10d %s\n",
				name, enabledStr, protocol, connectedStr, toolCount, status)
		}
	default:
		return fmt.Errorf("unknown output format: %s", upstreamOutputFormat)
	}

	return nil
}

func loadUpstreamConfig() (*config.Config, error) {
	if upstreamConfigPath != "" {
		return config.LoadFromFile(upstreamConfigPath)
	}
	return config.Load()
}

func createUpstreamLogger(level string) (*zap.Logger, error) {
	var zapLevel zap.AtomicLevel
	switch level {
	case "trace", "debug":
		zapLevel = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		zapLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		zapLevel = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		zapLevel = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		zapLevel = zap.NewAtomicLevelAt(zap.WarnLevel)
	}

	cfg := zap.Config{
		Level:            zapLevel,
		Development:      false,
		Encoding:         "console",
		EncoderConfig:    zap.NewDevelopmentEncoderConfig(),
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}

	return cfg.Build()
}

func getStringField(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBoolField(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func getIntField(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(int); ok {
		return v
	}
	return 0
}
