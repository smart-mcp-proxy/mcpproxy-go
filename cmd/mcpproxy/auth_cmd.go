package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()

	// Load configuration to get data directory
	cfg, err := loadAuthConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Auth status REQUIRES daemon (to show real daemon state)
	if !shouldUseAuthDaemon(cfg.DataDir) {
		return fmt.Errorf("auth status requires running daemon. Start with: mcpproxy serve")
	}

	return runAuthStatusClientMode(ctx, cfg.DataDir, authServerName, authAll)
}

// runAuthStatusClientMode fetches auth status from daemon via socket.
func runAuthStatusClientMode(ctx context.Context, dataDir, serverName string, allServers bool) error {
	socketPath := socket.DetectSocketPath(dataDir)
	logger, err := logs.SetupCommandLogger(false, authLogLevel, false, "")
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	client := cliclient.NewClient(socketPath, logger.Sugar())

	// Fetch all servers to check OAuth status
	servers, err := client.GetServers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get servers from daemon: %w", err)
	}

	// Filter by server name if specified
	if serverName != "" && !allServers {
		var found bool
		for _, srv := range servers {
			if name, ok := srv["name"].(string); ok && name == serverName {
				servers = []map[string]interface{}{srv}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("server '%s' not found", serverName)
		}
	}

	// Display OAuth status
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🔐 OAuth Authentication Status")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	hasOAuthServers := false
	for _, srv := range servers {
		name, _ := srv["name"].(string)
		oauth, hasOAuth := srv["oauth"].(map[string]interface{})

		if !hasOAuth || oauth == nil {
			continue // Skip non-OAuth servers
		}

		hasOAuthServers = true
		authenticated, _ := srv["authenticated"].(bool)
		lastError, _ := srv["last_error"].(string)

		// Determine status emoji and text
		var status string
		if authenticated {
			status = "✅ Authenticated"
		} else if lastError != "" {
			status = "❌ Authentication Failed"
		} else {
			status = "⏳ Pending Authentication"
		}

		fmt.Printf("Server: %s\n", name)
		fmt.Printf("  Status: %s\n", status)

		if authURL, ok := oauth["auth_url"].(string); ok && authURL != "" {
			fmt.Printf("  Auth URL: %s\n", authURL)
		}

		if tokenURL, ok := oauth["token_url"].(string); ok && tokenURL != "" {
			fmt.Printf("  Token URL: %s\n", tokenURL)
		}

		if lastError != "" {
			fmt.Printf("  Error: %s\n", lastError)

			// Provide suggestions based on error type
			if containsIgnoreCase(lastError, "requires") && containsIgnoreCase(lastError, "parameter") {
				fmt.Println()
				fmt.Println("  💡 Suggestion:")
				fmt.Println("     This OAuth provider requires additional parameters that")
				fmt.Println("     MCPProxy doesn't currently support. Support for custom")
				fmt.Println("     OAuth parameters (extra_params) is coming soon.")
				fmt.Println()
				fmt.Println("     For more information:")
				fmt.Println("     - RFC 8707: https://www.rfc-editor.org/rfc/rfc8707.html")
				fmt.Println("     - Track progress: https://github.com/smart-mcp-proxy/mcpproxy-go/issues")
			}
		}

		fmt.Println()
	}

	if !hasOAuthServers {
		fmt.Println("ℹ️  No servers with OAuth configuration found.")
		fmt.Println("   Configure OAuth in mcp_config.json to enable authentication.")
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

// containsIgnoreCase checks if a string contains a substring (case-insensitive)
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
