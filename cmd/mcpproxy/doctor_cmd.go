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
	case "pretty", "": // Handle both "pretty" and empty string (default value)
		// Pretty format - parse and display diagnostics
		totalIssues := getIntField(diag, "total_issues")

		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("🔍 MCPProxy Health Check")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()

		if totalIssues == 0 {
			fmt.Println("✅ All systems operational! No issues detected.")
			fmt.Println()
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			return nil
		}

		// Show issue summary
		issueWord := "issue"
		if totalIssues > 1 {
			issueWord = "issues"
		}
		fmt.Printf("⚠️  Found %d %s that need attention\n", totalIssues, issueWord)
		fmt.Println()

		// 1. Upstream Connection Errors
		if upstreamErrors := getArrayField(diag, "upstream_errors"); len(upstreamErrors) > 0 {
			fmt.Println("❌ Upstream Server Connection Errors")
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			for _, errItem := range upstreamErrors {
				if errMap, ok := errItem.(map[string]interface{}); ok {
					server := getStringField(errMap, "server")
					message := getStringField(errMap, "message")
					fmt.Printf("\nServer: %s\n", server)
					fmt.Printf("  Error: %s\n", message)
				}
			}
			fmt.Println()
			fmt.Println("💡 Remediation:")
			fmt.Println("  • Check server configuration in mcp_config.json")
			fmt.Println("  • View detailed logs: mcpproxy upstream logs <server-name>")
			fmt.Println("  • Restart server: mcpproxy upstream restart <server-name>")
			fmt.Println("  • Disable if not needed: mcpproxy upstream disable <server-name>")
			fmt.Println()
		}

		// 2. OAuth Required
		if oauthRequired := getStringArrayField(diag, "oauth_required"); len(oauthRequired) > 0 {
			fmt.Println("🔑 OAuth Authentication Required")
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			for _, server := range oauthRequired {
				fmt.Printf("  • %s\n", server)
			}
			fmt.Println()
			fmt.Println("💡 Remediation:")
			fmt.Println("  • Authenticate: mcpproxy auth login --server=<server-name>")
			fmt.Println("  • Check OAuth config in mcp_config.json")
			fmt.Println()
		}

		// 3. Missing Secrets
		if missingSecrets := getStringArrayField(diag, "missing_secrets"); len(missingSecrets) > 0 {
			fmt.Println("🔐 Missing Secrets")
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			for _, secret := range missingSecrets {
				fmt.Printf("  • %s\n", secret)
			}
			fmt.Println()
			fmt.Println("💡 Remediation:")
			fmt.Println("  • Set environment variables with required secrets")
			fmt.Println("  • Update secret references in mcp_config.json")
			fmt.Println("  • Use mcpproxy secrets command to manage secrets")
			fmt.Println()
		}

		// 4. Runtime Warnings
		if runtimeWarnings := getStringArrayField(diag, "runtime_warnings"); len(runtimeWarnings) > 0 {
			fmt.Println("⚠️  Runtime Warnings")
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			for _, warning := range runtimeWarnings {
				fmt.Printf("  • %s\n", warning)
			}
			fmt.Println()
			fmt.Println("💡 Remediation:")
			fmt.Println("  • Review main log: tail -f ~/.mcpproxy/logs/main.log")
			fmt.Println("  • Check server status: mcpproxy upstream list")
			fmt.Println()
		}

		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()
		fmt.Println("For more details, run: mcpproxy doctor --output=json")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	}

	return nil
}

// Helper functions for extracting fields from diagnostics map

func getArrayField(m map[string]interface{}, key string) []interface{} {
	if v, ok := m[key]; ok && v != nil {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

func getStringArrayField(m map[string]interface{}, key string) []string {
	if v, ok := m[key]; ok && v != nil {
		if arr, ok := v.([]interface{}); ok {
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if str, ok := item.(string); ok {
					result = append(result, str)
				}
			}
			return result
		}
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
