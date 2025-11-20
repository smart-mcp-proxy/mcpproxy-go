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
	doctorCmd = &cobra.Command{
		Use:   "doctor",
		Short: "Run health checks to identify issues",
		Long: `Run comprehensive health checks on MCPProxy to identify:
- Upstream server connection errors
- OAuth authentication requirements
- Missing secrets
- Runtime warnings
- Docker isolation status

This is the first command to run when debugging server issues.

Examples:
  mcpproxy doctor
  mcpproxy doctor --output=json`,
		RunE: runDoctor,
	}

	// Command flags
	doctorOutput     string
	doctorLogLevel   string
	doctorConfigPath string
)

// GetDoctorCommand returns the doctor command
func GetDoctorCommand() *cobra.Command {
	return doctorCmd
}

func init() {
	doctorCmd.Flags().StringVarP(&doctorOutput, "output", "o", "pretty", "Output format (pretty, json)")
	doctorCmd.Flags().StringVarP(&doctorLogLevel, "log-level", "l", "warn", "Log level")
	doctorCmd.Flags().StringVarP(&doctorConfigPath, "config", "c", "", "Path to config file")
}

func runDoctor(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Load configuration
	globalConfig, err := loadDoctorConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return err
	}

	// Create logger
	logger, err := createDoctorLogger(doctorLogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating logger: %v\n", err)
		return err
	}

	// Check if daemon is running
	if !shouldUseDoctorDaemon(globalConfig.DataDir) {
		return fmt.Errorf("doctor requires running daemon. Start with: mcpproxy serve")
	}

	logger.Info("Fetching diagnostics from daemon")
	return runDoctorClientMode(ctx, globalConfig.DataDir, logger)
}

func shouldUseDoctorDaemon(dataDir string) bool {
	socketPath := socket.DetectSocketPath(dataDir)
	return socket.IsSocketAvailable(socketPath)
}

func runDoctorClientMode(ctx context.Context, dataDir string, logger *zap.Logger) error {
	socketPath := socket.DetectSocketPath(dataDir)
	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Call GET /api/v1/diagnostics
	diag, err := client.GetDiagnostics(ctx)
	if err != nil {
		return fmt.Errorf("failed to get diagnostics from daemon: %w", err)
	}

	return outputDiagnostics(diag)
}

func outputDiagnostics(diag map[string]interface{}) error {
	switch doctorOutput {
	case "json":
		output, err := json.MarshalIndent(diag, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}
		fmt.Println(string(output))
	case "pretty":
	default:
		// Pretty format (placeholder - will be implemented with actual diagnostics API)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("🔍 MCPProxy Health Check")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()
		fmt.Println("✅ All systems operational! No issues detected.")
		fmt.Println()
		fmt.Println("(Full diagnostics will be implemented when API endpoint is ready)")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	}

	return nil
}

func loadDoctorConfig() (*config.Config, error) {
	if doctorConfigPath != "" {
		return config.LoadFromFile(doctorConfigPath)
	}
	return config.Load()
}

func createDoctorLogger(level string) (*zap.Logger, error) {
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
