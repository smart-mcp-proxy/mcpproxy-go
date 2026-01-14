package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"
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

// GetDoctorCommand returns the doctor command for adding to the root command.
// The doctor command runs comprehensive health checks on MCPProxy to identify
// upstream server connection errors, OAuth authentication requirements, missing
// secrets, and runtime warnings. This is the first command to run when debugging
// server issues.
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

	// Call GET /api/v1/info with refresh=true to get fresh update info
	info, err := client.GetInfoWithRefresh(ctx, true)
	if err != nil {
		logger.Debug("Failed to get info from daemon", zap.Error(err))
		// Non-fatal: continue with diagnostics even if info fails
	}

	// Call GET /api/v1/diagnostics
	diag, err := client.GetDiagnostics(ctx)
	if err != nil {
		return fmt.Errorf("failed to get diagnostics from daemon: %w", err)
	}

	return outputDiagnostics(diag, info)
}

func outputDiagnostics(diag map[string]interface{}, info map[string]interface{}) error {
	switch doctorOutput {
	case "json":
		// Combine diagnostics with info for JSON output
		combined := map[string]interface{}{
			"diagnostics": diag,
		}
		if info != nil {
			combined["info"] = info
		}
		output, err := json.MarshalIndent(combined, "", "  ")
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

		// Display version information
		if info != nil {
			version := getStringField(info, "version")
			if version != "" {
				// Check for update info
				if updateInfo, ok := info["update"].(map[string]interface{}); ok {
					updateAvailable := getBoolField(updateInfo, "available")
					latestVersion := getStringField(updateInfo, "latest_version")
					releaseURL := getStringField(updateInfo, "release_url")

					if updateAvailable && latestVersion != "" {
						fmt.Printf("Version: %s (update available: %s)\n", version, latestVersion)
						if releaseURL != "" {
							fmt.Printf("Download: %s\n", releaseURL)
						}
					} else {
						fmt.Printf("Version: %s (latest)\n", version)
					}
				} else {
					fmt.Printf("Version: %s\n", version)
				}
			}
		}
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
			// Sort by server name for consistent output
			sortArrayByServerName(upstreamErrors)

			fmt.Println("❌ Upstream Server Connection Errors")
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			for _, errItem := range upstreamErrors {
				if errMap, ok := errItem.(map[string]interface{}); ok {
					server := getStringField(errMap, "server_name")
					message := getStringField(errMap, "error_message")
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
		if oauthRequired := getArrayField(diag, "oauth_required"); len(oauthRequired) > 0 {
			// Sort by server name for consistent output
			sortArrayByServerName(oauthRequired)

			fmt.Println("🔑 OAuth Authentication Required")
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			for _, item := range oauthRequired {
				if oauthMap, ok := item.(map[string]interface{}); ok {
					serverName := getStringField(oauthMap, "server_name")
					message := getStringField(oauthMap, "message")
					fmt.Printf("\nServer: %s\n", serverName)
					if message != "" {
						fmt.Printf("  %s\n", message)
					} else {
						fmt.Printf("  Run: mcpproxy auth login --server=%s\n", serverName)
					}
				}
			}
			fmt.Println()
		}

		// 3. OAuth Configuration Issues
		if oauthIssues := getArrayField(diag, "oauth_issues"); len(oauthIssues) > 0 {
			// Sort by server name for consistent output
			sortArrayByServerName(oauthIssues)

			fmt.Println("🔍 OAuth Configuration Issues")
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			for _, issueItem := range oauthIssues {
				if issueMap, ok := issueItem.(map[string]interface{}); ok {
					serverName := getStringField(issueMap, "server_name")
					issue := getStringField(issueMap, "issue")
					errorMsg := getStringField(issueMap, "error")
					resolution := getStringField(issueMap, "resolution")
					docURL := getStringField(issueMap, "documentation_url")

					fmt.Printf("\n  Server: %s\n", serverName)
					fmt.Printf("    Issue: %s\n", issue)
					fmt.Printf("    Error: %s\n", errorMsg)
					fmt.Printf("    Impact: Server cannot authenticate until parameter is provided\n")
					fmt.Println()
					fmt.Printf("    Resolution:\n")
					fmt.Printf("      %s\n", resolution)
					if docURL != "" {
						fmt.Printf("      Documentation: %s\n", docURL)
					}
				}
			}
			fmt.Println()
		}

		// 4. Missing Secrets
		if missingSecrets := getArrayField(diag, "missing_secrets"); len(missingSecrets) > 0 {
			fmt.Println("🔐 Missing Secrets")
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			for _, secretItem := range missingSecrets {
				if secretMap, ok := secretItem.(map[string]interface{}); ok {
					// Use correct field names from contracts.MissingSecretInfo
					secretName := getStringField(secretMap, "secret_name")
					usedBy := getArrayField(secretMap, "used_by")

					fmt.Printf("\n  • %s\n", secretName)
					if len(usedBy) > 0 {
						fmt.Printf("    Used by: ")
						for i, server := range usedBy {
							if serverStr, ok := server.(string); ok {
								if i > 0 {
									fmt.Printf(", ")
								}
								fmt.Printf("%s", serverStr)
							}
						}
						fmt.Println()
					}
				}
			}
			fmt.Println()
			fmt.Println("💡 Remediation:")
			fmt.Println("  • Set environment variables with required secrets")
			fmt.Println("  • Update secret references in mcp_config.json")
			fmt.Println("  • Use mcpproxy secrets command to manage secrets")
			fmt.Println()
		}

		// 4. Runtime Warnings
		if runtimeWarnings := getArrayField(diag, "runtime_warnings"); len(runtimeWarnings) > 0 {
			fmt.Println("⚠️  Runtime Warnings")
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			for _, warningItem := range runtimeWarnings {
				if warningMap, ok := warningItem.(map[string]interface{}); ok {
					message := getStringField(warningMap, "message")
					severity := getStringField(warningMap, "severity")
					title := getStringField(warningMap, "title")

					// Display title if present, otherwise just the message
					if title != "" {
						fmt.Printf("\n  • %s\n", title)
						if message != "" {
							fmt.Printf("    %s\n", message)
						}
					} else if message != "" {
						fmt.Printf("  • %s\n", message)
					}

					if severity != "" && severity != "warning" {
						fmt.Printf("    Severity: %s\n", severity)
					}
				}
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

// sortArrayByServerName sorts an array of maps by the "server_name" field alphabetically.
func sortArrayByServerName(arr []interface{}) {
	sort.Slice(arr, func(i, j int) bool {
		iMap, iOk := arr[i].(map[string]interface{})
		jMap, jOk := arr[j].(map[string]interface{})
		if !iOk || !jOk {
			return false
		}
		iName := getStringField(iMap, "server_name")
		jName := getStringField(jMap, "server_name")
		return iName < jName
	})
}
