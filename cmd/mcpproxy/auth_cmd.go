package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mcpproxy-go/internal/cliclient"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/logs"
	"mcpproxy-go/internal/socket"
	"mcpproxy-go/internal/upstream/cli"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	authCmd = &cobra.Command{
		Use:   "auth",
		Short: "Authentication management commands",
		Long:  "Commands for managing OAuth authentication with upstream MCP servers",
	}

	authLoginCmd = &cobra.Command{
		Use:   "login",
		Short: "Manually authenticate with an OAuth-enabled server",
		Long: `Manually trigger OAuth authentication flow for a specific upstream server.
This is useful when:
- A server requires OAuth but automatic attempts have been rate limited
- You want to authenticate proactively before using server tools
- OAuth tokens have expired and need refreshing

The command will open your default browser for OAuth authorization.

Examples:
  mcpproxy auth login --server=Sentry-2
  mcpproxy auth login --server=github-server --log-level=debug
  mcpproxy auth login --server=google-calendar --timeout=5m`,
		RunE: runAuthLogin,
	}

	authStatusCmd = &cobra.Command{
		Use:   "status",
		Short: "Check authentication status for servers",
		Long: `Check the OAuth authentication status for one or all upstream servers.
Shows whether servers are authenticated, have expired tokens, or require authentication.

Examples:
  mcpproxy auth status --server=Sentry-2
  mcpproxy auth status --all
  mcpproxy auth status`,
		RunE: runAuthStatus,
	}

	// Command flags for auth commands
	authServerName string
	authLogLevel   string
	authConfigPath string
	authTimeout    time.Duration
	authAll        bool
)

// GetAuthCommand returns the auth command for adding to the root command
func GetAuthCommand() *cobra.Command {
	return authCmd
}

func init() {
	// Add subcommands to auth command
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authStatusCmd)

	// Define flags for auth login command
	authLoginCmd.Flags().StringVarP(&authServerName, "server", "s", "", "Server name to authenticate with (required)")
	authLoginCmd.Flags().StringVarP(&authLogLevel, "log-level", "l", "info", "Log level (trace, debug, info, warn, error)")
	authLoginCmd.Flags().StringVarP(&authConfigPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")
	authLoginCmd.Flags().DurationVar(&authTimeout, "timeout", 5*time.Minute, "Authentication timeout")

	// Define flags for auth status command
	authStatusCmd.Flags().StringVarP(&authServerName, "server", "s", "", "Server name to check status for (optional)")
	authStatusCmd.Flags().StringVarP(&authLogLevel, "log-level", "l", "info", "Log level (trace, debug, info, warn, error)")
	authStatusCmd.Flags().StringVarP(&authConfigPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")
	authStatusCmd.Flags().BoolVar(&authAll, "all", false, "Show status for all servers")

	// Mark required flags
	err := authLoginCmd.MarkFlagRequired("server")
	if err != nil {
		panic(fmt.Sprintf("Failed to mark server flag as required: %v", err))
	}

	// Add examples
	authLoginCmd.Example = `  # Authenticate with Sentry-2 server
  mcpproxy auth login --server=Sentry-2

  # Authenticate with debug logging
  mcpproxy auth login --server=github-server --log-level=debug

  # Authenticate with custom timeout
  mcpproxy auth login --server=google-calendar --timeout=10m`

	authStatusCmd.Example = `  # Check status for specific server
  mcpproxy auth status --server=Sentry-2

  # Check status for all servers
  mcpproxy auth status --all

  # Check status with debug logging
  mcpproxy auth status --all --log-level=debug`
}

func runAuthLogin(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()

	// Load configuration to get data directory
	cfg, err := loadAuthConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Check if daemon is running and use client mode
	if shouldUseAuthDaemon(cfg.DataDir) {
		return runAuthLoginClientMode(ctx, cfg.DataDir, authServerName)
	}

	// No daemon detected, use standalone mode
	return runAuthLoginStandalone(ctx, authServerName)
}

func runAuthStatus(_ *cobra.Command, _ []string) error {
	fmt.Printf("🔍 OAuth Authentication Status\n")
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Load configuration
	globalConfig, err := loadAuthConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Get servers to check
	var serversToCheck []string
	if authAll || authServerName == "" {
		serversToCheck = getAuthAvailableServerNames(globalConfig)
	} else {
		if !authServerExistsInConfig(authServerName, globalConfig) {
			return fmt.Errorf("server '%s' not found in configuration", authServerName)
		}
		serversToCheck = []string{authServerName}
	}

	// Check each server
	for _, serverName := range serversToCheck {
		fmt.Printf("🔗 Server: %s\n", serverName)

		// Create CLI client to check status
		cliClient, err := cli.NewClient(serverName, globalConfig, authLogLevel)
		if err != nil {
			fmt.Printf("  ❌ Failed to create client: %v\n", err)
			continue
		}
		// Ensure storage is closed per-iteration (avoid defers in loop)

		status, err := cliClient.GetOAuthStatus()
		if err != nil {
			fmt.Printf("  ❌ Failed to get OAuth status: %v\n", err)
			_ = cliClient.Close()
			continue
		}

		switch status {
		case "authenticated":
			fmt.Printf("  ✅ Authenticated\n")
		case "expired":
			fmt.Printf("  ⚠️  Token expired - run 'mcpproxy auth login --server=%s'\n", serverName)
		case "not_required":
			fmt.Printf("  ℹ️  OAuth not required\n")
		case "required":
			fmt.Printf("  🔐 Authentication required - run 'mcpproxy auth login --server=%s'\n", serverName)
		default:
			fmt.Printf("  ❓ Unknown status: %s\n", status)
		}
		fmt.Printf("\n")
		_ = cliClient.Close()
	}

	return nil
}

func loadAuthConfig() (*config.Config, error) {
	var configFile string
	if authConfigPath != "" {
		configFile = authConfigPath
	} else {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home directory: %w", err)
		}
		configFile = filepath.Join(homeDir, ".mcpproxy", "mcp_config.json")
	}

	globalConfig, err := config.LoadFromFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load config from %s: %w", configFile, err)
	}

	return globalConfig, nil
}

func authServerExistsInConfig(serverName string, cfg *config.Config) bool {
	for _, server := range cfg.Servers {
		if server.Name == serverName {
			return true
		}
	}
	return false
}

func getAuthAvailableServerNames(cfg *config.Config) []string {
	var names []string
	for _, server := range cfg.Servers {
		names = append(names, server.Name)
	}
	return names
}

// shouldUseAuthDaemon checks if daemon is running by detecting socket file.
func shouldUseAuthDaemon(dataDir string) bool {
	socketPath := socket.DetectSocketPath(dataDir)
	return socket.IsSocketAvailable(socketPath)
}

// runAuthLoginClientMode triggers OAuth via daemon HTTP API over socket.
func runAuthLoginClientMode(ctx context.Context, dataDir, serverName string) error {
	socketPath := socket.DetectSocketPath(dataDir)
	// Create simple logger for client (no file logging for command)
	logger, err := logs.SetupCommandLogger(false, authLogLevel, false, "")
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Ping daemon to verify connectivity
	pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx); err != nil {
		logger.Warn("Failed to ping daemon, falling back to standalone mode",
			zap.Error(err),
			zap.String("socket_path", socketPath))
		// Fall back to standalone mode
		return runAuthLoginStandalone(ctx, serverName)
	}

	fmt.Fprintf(os.Stderr, "ℹ️  Using daemon mode (via socket) - coordinating OAuth with running server\n\n")

	// Trigger OAuth via daemon
	if err := client.TriggerOAuthLogin(ctx, serverName); err != nil {
		return fmt.Errorf("failed to trigger OAuth login via daemon: %w", err)
	}

	fmt.Printf("✅ OAuth authentication flow initiated successfully for server: %s\n", serverName)
	fmt.Println("   The daemon will handle the OAuth callback and update server state.")
	fmt.Println("   Check 'mcpproxy upstream list' to verify authentication status.")

	return nil
}

// runAuthLoginStandalone executes OAuth login in standalone mode (original behavior).
func runAuthLoginStandalone(ctx context.Context, serverName string) error {
	fmt.Printf("🔐 Manual OAuth Authentication - Server: %s\n", serverName)
	fmt.Printf("📝 Log Level: %s\n", authLogLevel)
	fmt.Printf("⏱️  Timeout: %v\n", authTimeout)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Load configuration
	globalConfig, err := loadAuthConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Validate server exists in config
	if !authServerExistsInConfig(serverName, globalConfig) {
		return fmt.Errorf("server '%s' not found in configuration. Available servers: %v",
			serverName, getAuthAvailableServerNames(globalConfig))
	}

	// Create CLI client for manual OAuth
	fmt.Printf("🔗 Connecting to server '%s' for OAuth authentication...\n", serverName)
	fmt.Println("   Note: Running in standalone mode (no daemon detected)")
	fmt.Println("   OAuth tokens will not be shared with daemon automatically.")
	fmt.Println()

	cliClient, err := cli.NewClient(serverName, globalConfig, authLogLevel)
	if err != nil {
		return fmt.Errorf("failed to create CLI client: %w", err)
	}
	defer cliClient.Close() // Ensure storage is closed

	// Trigger manual OAuth flow
	fmt.Printf("🌐 Starting manual OAuth flow...\n")
	fmt.Printf("⚠️  This will open your browser for authentication.\n\n")

	if err := cliClient.TriggerManualOAuth(ctx); err != nil {
		fmt.Printf("❌ OAuth authentication failed: %v\n", err)
		return fmt.Errorf("OAuth authentication failed: %w", err)
	}

	fmt.Printf("✅ OAuth authentication successful for server '%s'!\n", serverName)
	fmt.Printf("🎉 You can now use tools from this server.\n")

	return nil
}
