package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/cli"

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
		Long: `Manually trigger OAuth authentication flow for a specific upstream server or all servers.
This is useful when:
- A server requires OAuth but automatic attempts have been rate limited
- You want to authenticate proactively before using server tools
- OAuth tokens have expired and need refreshing
- Multiple servers need re-authentication

The command will open your default browser for OAuth authorization.
Use --all to authenticate all servers that require OAuth (confirmation prompt unless --force is used).

Examples:
  mcpproxy auth login --server=Sentry-2
  mcpproxy auth login --all
  mcpproxy auth login --all --force
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

	authLogoutCmd = &cobra.Command{
		Use:   "logout",
		Short: "Clear OAuth token and disconnect from a server",
		Long: `Clear OAuth authentication token and disconnect from a specific upstream server.
This is useful when:
- You want to revoke access to an OAuth-enabled server
- You need to re-authenticate with different credentials
- Troubleshooting authentication issues

The command clears the stored OAuth token and disconnects the server.
You will need to re-authenticate before using the server's tools again.

Examples:
  mcpproxy auth logout --server=Sentry-2
  mcpproxy auth logout --server=github-server --log-level=debug`,
		RunE: runAuthLogout,
	}

	// Command flags for auth commands
	authServerName string
	authLogLevel   string
	authConfigPath string
	authTimeout    time.Duration
	authAll        bool
	authForce      bool
)

// GetAuthCommand returns the auth command for adding to the root command
func GetAuthCommand() *cobra.Command {
	return authCmd
}

func init() {
	// Add subcommands to auth command
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authStatusCmd)
	authCmd.AddCommand(authLogoutCmd)

	// Define flags for auth login command
	authLoginCmd.Flags().StringVarP(&authServerName, "server", "s", "", "Server name to authenticate with")
	authLoginCmd.Flags().BoolVar(&authAll, "all", false, "Authenticate all servers that require OAuth")
	authLoginCmd.Flags().BoolVar(&authForce, "force", false, "Skip confirmation prompt when using --all")
	authLoginCmd.Flags().StringVarP(&authLogLevel, "log-level", "l", "info", "Log level (trace, debug, info, warn, error)")
	authLoginCmd.Flags().StringVarP(&authConfigPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")
	authLoginCmd.Flags().DurationVar(&authTimeout, "timeout", 5*time.Minute, "Authentication timeout")

	// Define flags for auth status command
	authStatusCmd.Flags().StringVarP(&authServerName, "server", "s", "", "Server name to check status for (optional)")
	authStatusCmd.Flags().StringVarP(&authLogLevel, "log-level", "l", "info", "Log level (trace, debug, info, warn, error)")
	authStatusCmd.Flags().StringVarP(&authConfigPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")
	authStatusCmd.Flags().BoolVar(&authAll, "all", false, "Show status for all servers")

	// Define flags for auth logout command
	authLogoutCmd.Flags().StringVarP(&authServerName, "server", "s", "", "Server name to logout from (required)")
	authLogoutCmd.Flags().StringVarP(&authLogLevel, "log-level", "l", "info", "Log level (trace, debug, info, warn, error)")
	authLogoutCmd.Flags().StringVarP(&authConfigPath, "config", "c", "", "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")
	authLogoutCmd.Flags().DurationVar(&authTimeout, "timeout", 30*time.Second, "Logout timeout")

	// Mark required flags
	// Note: auth login doesn't mark --server as required because --all can be used instead
	err := authLogoutCmd.MarkFlagRequired("server")
	if err != nil {
		panic(fmt.Sprintf("Failed to mark server flag as required: %v", err))
	}

	// Add examples
	authLoginCmd.Example = `  # Authenticate with Sentry-2 server
  mcpproxy auth login --server=Sentry-2

  # Authenticate all servers that require OAuth
  mcpproxy auth login --all

  # Authenticate all servers without confirmation prompt
  mcpproxy auth login --all --force

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

	authLogoutCmd.Example = `  # Logout from Sentry-2 server
  mcpproxy auth logout --server=Sentry-2

  # Logout with debug logging
  mcpproxy auth logout --server=github-server --log-level=debug`
}

func runAuthLogin(cmd *cobra.Command, _ []string) error {
	// Validate flags: either --server or --all must be provided (but not both)
	if authAll && authServerName != "" {
		return fmt.Errorf("cannot use both --server and --all flags together")
	}
	if !authAll && authServerName == "" {
		return fmt.Errorf("either --server or --all flag is required")
	}

	// After validation, silence usage for operational errors
	cmd.SilenceUsage = true

	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()

	// Load configuration to get data directory
	cfg, err := loadAuthConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Handle --all flag: authenticate all servers
	if authAll {
		return runAuthLoginAll(ctx, cfg.DataDir)
	}

	// Handle single server authentication
	// Check if daemon is running and use client mode
	if shouldUseAuthDaemon(cfg.DataDir) {
		return runAuthLoginClientMode(ctx, cfg.DataDir, authServerName)
	}

	// No daemon detected, use standalone mode
	return runAuthLoginStandalone(ctx, authServerName)
}

// runAuthLoginAll authenticates all servers that require OAuth authentication.
func runAuthLoginAll(ctx context.Context, dataDir string) error {
	// Auth login --all REQUIRES daemon
	if !shouldUseAuthDaemon(dataDir) {
		return fmt.Errorf("auth login --all requires running daemon. Start with: mcpproxy serve")
	}

	socketPath := socket.DetectSocketPath(dataDir)
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
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}

	// Fetch all servers to find those that need OAuth authentication
	servers, err := client.GetServers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get servers from daemon: %w", err)
	}

	// Filter servers that need login (health.action == "login")
	var serversNeedingAuth []string
	for _, srv := range servers {
		name, _ := srv["name"].(string)
		if health, ok := srv["health"].(map[string]interface{}); ok && health != nil {
			if action, ok := health["action"].(string); ok && action == "login" {
				serversNeedingAuth = append(serversNeedingAuth, name)
			}
		}
	}

	if len(serversNeedingAuth) == 0 {
		fmt.Println("✅ No servers require authentication")
		fmt.Println("   All OAuth-enabled servers are already authenticated.")
		return nil
	}

	// Display servers that will be authenticated
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🔐 Batch OAuth Authentication")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("\nThe following %d server(s) require authentication:\n", len(serversNeedingAuth))
	for i, name := range serversNeedingAuth {
		fmt.Printf("  %d. %s\n", i+1, name)
	}
	fmt.Println()

	// Show warning about multiple browser tabs
	if len(serversNeedingAuth) > 1 {
		fmt.Printf("⚠️  This will open %d browser tabs for OAuth authorization.\n\n", len(serversNeedingAuth))
	}

	// Prompt for confirmation unless --force is used
	if !authForce {
		fmt.Print("Do you want to continue? (yes/no): ")
		var response string
		if _, err := fmt.Scanln(&response); err != nil {
			return fmt.Errorf("failed to read confirmation: %w", err)
		}
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "yes" && response != "y" {
			fmt.Println("\n❌ Authentication cancelled")
			return nil
		}
		fmt.Println()
	}

	// Authenticate each server
	var successful, failed int
	failedServers := make(map[string]string) // server name -> error message

	fmt.Println("Starting authentication...")
	fmt.Println()

	for i, serverName := range serversNeedingAuth {
		fmt.Printf("[%d/%d] Authenticating %s...\n", i+1, len(serversNeedingAuth), serverName)

		if err := client.TriggerOAuthLogin(ctx, serverName); err != nil {
			fmt.Printf("  ❌ Failed: %v\n", err)
			failed++
			failedServers[serverName] = err.Error()
		} else {
			fmt.Printf("  ✅ Success\n")
			successful++
		}
		fmt.Println()
	}

	// Display summary
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("📊 Authentication Summary")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("Total: %d  |  Successful: %d  |  Failed: %d\n", len(serversNeedingAuth), successful, failed)

	if failed > 0 {
		fmt.Println("\n❌ Failed servers:")
		for serverName, errMsg := range failedServers {
			fmt.Printf("  • %s: %s\n", serverName, errMsg)
		}
		fmt.Println("\nTip: Run 'mcpproxy auth login --server=<name>' to retry individual servers")
		return fmt.Errorf("%d server(s) failed to authenticate", failed)
	}

	fmt.Println("\n✅ All servers authenticated successfully!")
	return nil
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

	// Filter to only OAuth servers
	oauthServers := filterOAuthServers(servers)

	// Get the output formatter based on global flags
	formatter, err := GetOutputFormatter()
	if err != nil {
		return fmt.Errorf("failed to get output formatter: %w", err)
	}

	outputFormat := ResolveOutputFormat()

	// For structured formats (json, yaml), output raw data
	if outputFormat == "json" || outputFormat == "yaml" {
		result, err := formatter.Format(oauthServers)
		if err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}
		fmt.Println(result)
		return nil
	}

	// For table format, use human-readable display
	return displayAuthStatusPretty(oauthServers)
}

// filterOAuthServers filters servers to only those with OAuth configuration
func filterOAuthServers(servers []map[string]interface{}) []map[string]interface{} {
	var oauthServers []map[string]interface{}
	for _, srv := range servers {
		oauth, _ := srv["oauth"].(map[string]interface{})
		authenticated, _ := srv["authenticated"].(bool)
		lastError, _ := srv["last_error"].(string)

		// Check if this is an OAuth server by:
		// 1. Has oauth config OR
		// 2. Has OAuth-related error OR
		// 3. Is authenticated (has OAuth token)
		isOAuthServer := (oauth != nil) ||
			containsIgnoreCase(lastError, "oauth") ||
			authenticated

		if isOAuthServer {
			oauthServers = append(oauthServers, srv)
		}
	}
	return oauthServers
}

// displayAuthStatusPretty displays OAuth status in human-readable format
func displayAuthStatusPretty(servers []map[string]interface{}) error {
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🔐 OAuth Authentication Status")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	if len(servers) == 0 {
		fmt.Println("ℹ️  No servers with OAuth configuration found.")
		fmt.Println("   Configure OAuth in mcp_config.json to enable authentication.")
		return nil
	}

	for _, srv := range servers {
		name, _ := srv["name"].(string)
		oauth, _ := srv["oauth"].(map[string]interface{})
		lastError, _ := srv["last_error"].(string)

		// Use unified health status from backend (FR-006, FR-007)
		var healthLevel, adminState, healthSummary, healthAction string
		if health, ok := srv["health"].(map[string]interface{}); ok && health != nil {
			healthLevel, _ = health["level"].(string)
			adminState, _ = health["admin_state"].(string)
			healthSummary, _ = health["summary"].(string)
			healthAction, _ = health["action"].(string)
		}

		// Determine status emoji based on admin_state first, then health level
		var statusEmoji string
		switch adminState {
		case "disabled":
			statusEmoji = "⏸️"
		case "quarantined":
			statusEmoji = "🔒"
		default:
			// Use health level for enabled servers
			switch healthLevel {
			case "healthy":
				statusEmoji = "✅"
			case "degraded":
				statusEmoji = "⚠️"
			case "unhealthy":
				statusEmoji = "❌"
			default:
				statusEmoji = "❓"
			}
		}

		fmt.Printf("Server: %s\n", name)
		fmt.Printf("  Health: %s %s\n", statusEmoji, healthSummary)
		if adminState != "" && adminState != "enabled" {
			fmt.Printf("  Admin State: %s\n", adminState)
		}
		// Show action as command hint (FR-007)
		if healthAction != "" {
			switch healthAction {
			case "login":
				fmt.Printf("  Action: mcpproxy auth login --server=%s\n", name)
			case "restart":
				fmt.Printf("  Action: mcpproxy upstream restart %s\n", name)
			case "enable":
				fmt.Printf("  Action: mcpproxy upstream enable %s\n", name)
			case "approve":
				fmt.Printf("  Action: Approve via Web UI or tray menu\n")
			case "view_logs":
				fmt.Printf("  Action: mcpproxy upstream logs %s\n", name)
			}
		}

		// Display OAuth configuration details (if available)
		// Check if this is an autodiscovery server (no explicit OAuth config, but has token)
		isAutodiscovery := false
		if oauth != nil {
			if autodiscovery, ok := oauth["autodiscovery"].(bool); ok && autodiscovery {
				isAutodiscovery = true
			}
		}

		if oauth != nil && !isAutodiscovery {
			if clientID, ok := oauth["client_id"].(string); ok && clientID != "" {
				fmt.Printf("  Client ID: %s\n", clientID)
			}

			// Handle both []string (from runtime) and []interface{} (from conversions)
			if scopes, ok := oauth["scopes"].([]string); ok && len(scopes) > 0 {
				fmt.Printf("  Scopes: %s\n", strings.Join(scopes, ", "))
			} else if scopes, ok := oauth["scopes"].([]interface{}); ok && len(scopes) > 0 {
				scopeStrs := make([]string, len(scopes))
				for i, s := range scopes {
					scopeStrs[i] = fmt.Sprintf("%v", s)
				}
				fmt.Printf("  Scopes: %s\n", strings.Join(scopeStrs, ", "))
			}

			if pkceEnabled, ok := oauth["pkce_enabled"].(bool); ok && pkceEnabled {
				fmt.Printf("  PKCE: Enabled\n")
			}

			// Handle both map[string]string (from runtime) and map[string]interface{} (from conversions)
			var manualResource string
			var otherParams []string
			if extraParams, ok := oauth["extra_params"].(map[string]string); ok && len(extraParams) > 0 {
				for key, val := range extraParams {
					if key == "resource" {
						manualResource = val
					} else {
						otherParams = append(otherParams, fmt.Sprintf("%s=%v", key, val))
					}
				}
			} else if extraParams, ok := oauth["extra_params"].(map[string]interface{}); ok && len(extraParams) > 0 {
				for key, val := range extraParams {
					if key == "resource" {
						manualResource = fmt.Sprintf("%v", val)
					} else {
						otherParams = append(otherParams, fmt.Sprintf("%s=%v", key, val))
					}
				}
			}

			// Show non-resource extra params
			if len(otherParams) > 0 {
				fmt.Printf("  Extra Params: %s\n", strings.Join(otherParams, ", "))
			}

			// Show RFC 8707 resource parameter with actual URL
			serverURL, _ := srv["url"].(string)
			if manualResource != "" {
				fmt.Printf("  Resource: %s (manual)\n", manualResource)
			} else if serverURL != "" {
				// Resource will be auto-detected from RFC 9728 Protected Resource Metadata
				// Show server URL as the expected value (fallback if metadata unavailable)
				fmt.Printf("  Resource: Auto-detect → %s\n", serverURL)
			} else {
				fmt.Printf("  Resource: Auto-detect (RFC 9728)\n")
			}
		} else {
			// Server requires OAuth but has no explicit config (discovery/DCR)
			fmt.Printf("  OAuth: Discovered via Dynamic Client Registration\n")
			// RFC 8707 resource will be auto-detected for zero-config OAuth servers
			serverURL, _ := srv["url"].(string)
			if serverURL != "" {
				fmt.Printf("  Resource: Auto-detect → %s\n", serverURL)
			} else {
				fmt.Printf("  Resource: Auto-detect (RFC 9728)\n")
			}
		}

		// Display token expiration if available
		if oauth != nil {
			if tokenExpiresAt, ok := oauth["token_expires_at"].(string); ok && tokenExpiresAt != "" {
				if expiryTime, err := time.Parse(time.RFC3339, tokenExpiresAt); err == nil {
					timeUntilExpiry := time.Until(expiryTime)
					if timeUntilExpiry > 0 {
						fmt.Printf("  Token Expires: %s (in %s)\n", expiryTime.Format("2006-01-02 15:04:05"), formatDuration(timeUntilExpiry))
					} else {
						fmt.Printf("  Token Expires: %s (⚠️  EXPIRED)\n", expiryTime.Format("2006-01-02 15:04:05"))
					}
				}
			} else if tokenValid, ok := oauth["token_valid"].(bool); ok {
				if tokenValid {
					fmt.Printf("  Token: Valid\n")
				} else {
					fmt.Printf("  Token: ⚠️  Invalid or Expired\n")
				}
			}

			if authURL, ok := oauth["auth_url"].(string); ok && authURL != "" {
				fmt.Printf("  Auth URL: %s\n", authURL)
			}

			if tokenURL, ok := oauth["token_url"].(string); ok && tokenURL != "" {
				fmt.Printf("  Token URL: %s\n", tokenURL)
			}
		}

		if lastError != "" {
			fmt.Printf("  Error: %s\n", lastError)
		}

		fmt.Println()
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
		// Spec 020: Check for structured OAuth errors and display rich output
		var oauthFlowErr *contracts.OAuthFlowError
		if errors.As(err, &oauthFlowErr) {
			displayOAuthFlowError(oauthFlowErr)
			return fmt.Errorf("OAuth authentication failed")
		}

		var oauthValidationErr *contracts.OAuthValidationError
		if errors.As(err, &oauthValidationErr) {
			displayOAuthValidationError(oauthValidationErr)
			return fmt.Errorf("OAuth validation failed")
		}

		// T025: Use cliError to include request_id in error output
		return cliError("failed to trigger OAuth login via daemon", err)
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

// displayOAuthFlowError displays a structured OAuth flow error with rich formatting.
// Spec 020: OAuth Login Error Feedback
func displayOAuthFlowError(err *contracts.OAuthFlowError) {
	fmt.Fprintf(os.Stderr, "\n❌ OAuth Authentication Failed\n")
	fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(os.Stderr, "Server:     %s\n", err.ServerName)
	fmt.Fprintf(os.Stderr, "Error Type: %s (%s)\n", err.ErrorType, err.ErrorCode)
	fmt.Fprintf(os.Stderr, "Message:    %s\n", err.Message)

	if err.Details != nil {
		fmt.Fprintf(os.Stderr, "\nDetails:\n")
		if err.Details.ServerURL != "" {
			fmt.Fprintf(os.Stderr, "  Server URL: %s\n", err.Details.ServerURL)
		}
		if err.Details.ProtectedResourceMetadata != nil {
			meta := err.Details.ProtectedResourceMetadata
			fmt.Fprintf(os.Stderr, "  Protected Resource Metadata:\n")
			fmt.Fprintf(os.Stderr, "    Found: %v\n", meta.Found)
			fmt.Fprintf(os.Stderr, "    URL Checked: %s\n", meta.URLChecked)
			if meta.Error != "" {
				fmt.Fprintf(os.Stderr, "    Error: %s\n", meta.Error)
			}
			if len(meta.AuthorizationServers) > 0 {
				fmt.Fprintf(os.Stderr, "    Authorization Servers: %s\n", strings.Join(meta.AuthorizationServers, ", "))
			}
		}
		if err.Details.AuthorizationServerMetadata != nil {
			meta := err.Details.AuthorizationServerMetadata
			fmt.Fprintf(os.Stderr, "  Authorization Server Metadata:\n")
			fmt.Fprintf(os.Stderr, "    Found: %v\n", meta.Found)
			fmt.Fprintf(os.Stderr, "    URL Checked: %s\n", meta.URLChecked)
			if meta.Error != "" {
				fmt.Fprintf(os.Stderr, "    Error: %s\n", meta.Error)
			}
		}
		if err.Details.DCRStatus != nil {
			dcr := err.Details.DCRStatus
			fmt.Fprintf(os.Stderr, "  Dynamic Client Registration:\n")
			fmt.Fprintf(os.Stderr, "    Attempted: %v\n", dcr.Attempted)
			fmt.Fprintf(os.Stderr, "    Success: %v\n", dcr.Success)
			if dcr.StatusCode > 0 {
				fmt.Fprintf(os.Stderr, "    Status Code: %d\n", dcr.StatusCode)
			}
			if dcr.Error != "" {
				fmt.Fprintf(os.Stderr, "    Error: %s\n", dcr.Error)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "\n💡 Suggestion: %s\n", err.Suggestion)

	// Build debug hint with log correlation commands
	// OAuth errors are logged to main.log, not server-specific logs
	fmt.Fprintf(os.Stderr, "\n🔍 Debug:\n")
	fmt.Fprintf(os.Stderr, "   Server logs: mcpproxy upstream logs %s\n", err.ServerName)
	if err.CorrelationID != "" {
		if logDir, logDirErr := logs.GetLogDir(); logDirErr == nil {
			fmt.Fprintf(os.Stderr, "   OAuth logs:  grep %s %s/main.log\n", err.CorrelationID, logDir)
		}
		fmt.Fprintf(os.Stderr, "   Correlation ID: %s\n", err.CorrelationID)
	}
	if err.RequestID != "" {
		fmt.Fprintf(os.Stderr, "   Request ID: %s\n", err.RequestID)
	}
	fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}

// displayOAuthValidationError displays a structured OAuth validation error with rich formatting.
// Spec 020: OAuth Login Error Feedback
func displayOAuthValidationError(err *contracts.OAuthValidationError) {
	fmt.Fprintf(os.Stderr, "\n❌ OAuth Validation Failed\n")
	fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(os.Stderr, "Server:     %s\n", err.ServerName)
	fmt.Fprintf(os.Stderr, "Error Type: %s\n", err.ErrorType)
	fmt.Fprintf(os.Stderr, "Message:    %s\n", err.Message)

	if len(err.AvailableServers) > 0 {
		fmt.Fprintf(os.Stderr, "\nAvailable servers:\n")
		for _, name := range err.AvailableServers {
			fmt.Fprintf(os.Stderr, "  - %s\n", name)
		}
	}

	fmt.Fprintf(os.Stderr, "\n💡 Suggestion: %s\n", err.Suggestion)

	if err.CorrelationID != "" {
		fmt.Fprintf(os.Stderr, "\n🔍 Correlation ID: %s\n", err.CorrelationID)
	}
	fmt.Fprintf(os.Stderr, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}

// formatDuration formats a duration into a human-readable string
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}

	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	} else if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	} else {
		return fmt.Sprintf("%dm", minutes)
	}
}

func runAuthLogout(_ *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()

	// Load configuration to get data directory
	cfg, err := loadAuthConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Check if daemon is running and use client mode
	if shouldUseAuthDaemon(cfg.DataDir) {
		return runAuthLogoutClientMode(ctx, cfg.DataDir, authServerName)
	}

	// No daemon detected, use standalone mode
	return runAuthLogoutStandalone(ctx, authServerName, cfg)
}

// runAuthLogoutClientMode triggers OAuth logout via daemon HTTP API over socket.
func runAuthLogoutClientMode(ctx context.Context, dataDir, serverName string) error {
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

		// Load config for standalone mode
		cfg, loadErr := loadAuthConfig()
		if loadErr != nil {
			return fmt.Errorf("failed to load configuration for standalone: %w", loadErr)
		}
		return runAuthLogoutStandalone(ctx, serverName, cfg)
	}

	fmt.Fprintf(os.Stderr, "ℹ️  Using daemon mode (via socket) - coordinating OAuth logout with running server\n\n")

	// Trigger OAuth logout via daemon
	if err := client.TriggerOAuthLogout(ctx, serverName); err != nil {
		// T025: Use cliError to include request_id in error output
		return cliError("failed to trigger OAuth logout via daemon", err)
	}

	fmt.Printf("✅ OAuth logout completed successfully for server: %s\n", serverName)
	fmt.Println("   The OAuth token has been cleared and the server has been disconnected.")
	fmt.Println("   Use 'mcpproxy auth login --server=" + serverName + "' to re-authenticate.")

	return nil
}

// runAuthLogoutStandalone clears OAuth token in standalone mode.
func runAuthLogoutStandalone(ctx context.Context, serverName string, cfg *config.Config) error {
	fmt.Printf("🔐 OAuth Logout - Server: %s\n", serverName)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Validate server exists in config
	if !authServerExistsInConfig(serverName, cfg) {
		return fmt.Errorf("server '%s' not found in configuration. Available servers: %v",
			serverName, getAuthAvailableServerNames(cfg))
	}

	// Create CLI client for OAuth logout
	fmt.Printf("🔗 Clearing OAuth token for server '%s'...\n", serverName)
	fmt.Println("   Note: Running in standalone mode (no daemon detected)")
	fmt.Println()

	cliClient, err := cli.NewClient(serverName, cfg, authLogLevel)
	if err != nil {
		return fmt.Errorf("failed to create CLI client: %w", err)
	}
	defer cliClient.Close()

	// Clear OAuth token
	if err := cliClient.ClearOAuthToken(ctx); err != nil {
		return fmt.Errorf("failed to clear OAuth token: %w", err)
	}

	fmt.Printf("✅ OAuth token cleared successfully for server '%s'!\n", serverName)
	fmt.Println("   Use 'mcpproxy auth login --server=" + serverName + "' to re-authenticate.")

	return nil
}
