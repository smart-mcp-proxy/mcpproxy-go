package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/socket"
)

var (
	// token create flags
	tokenName        string
	tokenServers     string
	tokenPermissions string
	tokenExpires     string
)

// GetTokenCommand returns the token parent command.
func GetTokenCommand() *cobra.Command {
	tokenCmd := &cobra.Command{
		Use:   "token",
		Short: "Manage agent tokens",
		Long: `Commands for creating and managing scoped agent tokens.

Agent tokens provide limited-scope access to the MCPProxy MCP and REST APIs.
Each token is restricted to specific upstream servers and permission tiers
(read, write, destructive).

Examples:
  mcpproxy token create --name deploy-bot --servers github,gitlab --permissions read,write
  mcpproxy token list
  mcpproxy token show deploy-bot
  mcpproxy token revoke deploy-bot`,
	}

	// Subcommands
	tokenCmd.AddCommand(newTokenCreateCmd())
	tokenCmd.AddCommand(newTokenListCmd())
	tokenCmd.AddCommand(newTokenShowCmd())
	tokenCmd.AddCommand(newTokenRevokeCmd())

	return tokenCmd
}

func newTokenCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new agent token",
		Long: `Create a new scoped agent token for programmatic access.

The token is displayed once on creation and cannot be retrieved again.
Store it securely.

Examples:
  mcpproxy token create --name deploy-bot --servers github,gitlab --permissions read,write
  mcpproxy token create --name ci-agent --servers "*" --permissions read --expires 7d
  mcpproxy token create --name full-access --servers github --permissions read,write,destructive --expires 90d`,
		RunE: runTokenCreate,
	}

	cmd.Flags().StringVar(&tokenName, "name", "", "Token name (required, unique)")
	cmd.Flags().StringVar(&tokenServers, "servers", "", "Comma-separated list of allowed server names, or \"*\" for all (required)")
	cmd.Flags().StringVar(&tokenPermissions, "permissions", "", "Comma-separated permission tiers: read, write, destructive (required, must include read)")
	cmd.Flags().StringVar(&tokenExpires, "expires", "30d", "Token expiry duration (e.g., 7d, 30d, 90d, 365d)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("servers")
	_ = cmd.MarkFlagRequired("permissions")

	return cmd
}

func newTokenListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all agent tokens",
		Long: `List all configured agent tokens with their status, permissions, and expiry.

Examples:
  mcpproxy token list
  mcpproxy token list -o json`,
		RunE: runTokenList,
	}
}

func newTokenShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show details of an agent token",
		Long: `Display detailed information about a specific agent token.

Examples:
  mcpproxy token show deploy-bot
  mcpproxy token show deploy-bot -o json`,
		Args: cobra.ExactArgs(1),
		RunE: runTokenShow,
	}
}

func newTokenRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <name>",
		Short: "Revoke an agent token",
		Long: `Revoke an agent token, immediately preventing its use.

Examples:
  mcpproxy token revoke deploy-bot`,
		Args: cobra.ExactArgs(1),
		RunE: runTokenRevoke,
	}
}

// newTokenCLIClient creates a cliclient.Client connected to the running MCPProxy.
func newTokenCLIClient() (*cliclient.Client, *config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}
	cfg.EnsureAPIKey()

	socketPath := socket.DetectSocketPath(cfg.DataDir)

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	var client *cliclient.Client
	if socket.IsSocketAvailable(socketPath) {
		client = cliclient.NewClient(socketPath, logger.Sugar())
	} else {
		endpoint := fmt.Sprintf("http://%s", cfg.Listen)
		client = cliclient.NewClientWithAPIKey(endpoint, cfg.APIKey, logger.Sugar())
	}

	return client, cfg, nil
}

func runTokenCreate(_ *cobra.Command, _ []string) error {
	client, _, err := newTokenCLIClient()
	if err != nil {
		return err
	}

	// Build request body
	servers := splitAndTrim(tokenServers)
	permissions := splitAndTrim(tokenPermissions)

	body := map[string]interface{}{
		"name":            tokenName,
		"allowed_servers": servers,
		"permissions":     permissions,
		"expires_in":      tokenExpires,
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodPost, "/api/v1/tokens", bodyJSON)
	if err != nil {
		return fmt.Errorf("failed to create token: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "create token")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	// Format output
	format := ResolveOutputFormat()
	if format == "json" {
		formatted, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(formatted))
		return nil
	}

	// Table output — highlight the token since it's only shown once
	fmt.Println("Agent token created successfully.")
	fmt.Println()
	if token, ok := result["token"].(string); ok {
		fmt.Printf("  Token: %s\n", token)
		fmt.Println()
		fmt.Println("  IMPORTANT: Save this token now. It cannot be retrieved again.")
		fmt.Println()
	}
	printField("  Name:        ", result, "name")
	printListField("  Servers:     ", result, "allowed_servers")
	printListField("  Permissions: ", result, "permissions")
	printField("  Expires:     ", result, "expires_at")

	return nil
}

func runTokenList(_ *cobra.Command, _ []string) error {
	client, _, err := newTokenCLIClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/tokens", nil)
	if err != nil {
		return fmt.Errorf("failed to list tokens: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "list tokens")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	format := ResolveOutputFormat()
	if format == "json" {
		formatted, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(formatted))
		return nil
	}

	tokens, ok := result["tokens"].([]interface{})
	if !ok || len(tokens) == 0 {
		fmt.Println("No agent tokens configured.")
		return nil
	}

	// Table format
	fmt.Printf("%-20s %-14s %-25s %-20s %-8s %-25s\n",
		"NAME", "PREFIX", "SERVERS", "PERMISSIONS", "REVOKED", "EXPIRES")
	fmt.Println(strings.Repeat("-", 115))

	for _, t := range tokens {
		tok, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		name := getMapString(tok, "name")
		prefix := getMapString(tok, "token_prefix")
		revoked := "no"
		if r, ok := tok["revoked"].(bool); ok && r {
			revoked = "yes"
		}

		serverList := joinInterfaceSlice(tok, "allowed_servers", 23)
		permList := joinInterfaceSlice(tok, "permissions", 0)

		expiresAt := getMapString(tok, "expires_at")
		if expiresAt != "" {
			if t, parseErr := time.Parse(time.RFC3339, expiresAt); parseErr == nil {
				expiresAt = t.Format("2006-01-02 15:04")
			}
		}

		fmt.Printf("%-20s %-14s %-25s %-20s %-8s %-25s\n",
			name, prefix, serverList, permList, revoked, expiresAt)
	}

	return nil
}

func runTokenShow(_ *cobra.Command, args []string) error {
	client, _, err := newTokenCLIClient()
	if err != nil {
		return err
	}

	name := args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodGet, "/api/v1/tokens/"+name, nil)
	if err != nil {
		return fmt.Errorf("failed to get token: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("token %q not found", name)
	}
	if resp.StatusCode != http.StatusOK {
		return parseAPIError(respBody, resp.StatusCode, "get token")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	format := ResolveOutputFormat()
	if format == "json" {
		formatted, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(formatted))
		return nil
	}

	// Pretty print
	printField("Name:           ", result, "name")
	printField("Token Prefix:   ", result, "token_prefix")
	printListField("Servers:        ", result, "allowed_servers")
	printListField("Permissions:    ", result, "permissions")
	if revoked, ok := result["revoked"].(bool); ok {
		fmt.Printf("Revoked:        %v\n", revoked)
	}
	printField("Created:        ", result, "created_at")
	printField("Expires:        ", result, "expires_at")
	if lastUsed := getMapString(result, "last_used_at"); lastUsed != "" {
		fmt.Printf("Last Used:      %s\n", lastUsed)
	}

	return nil
}

func runTokenRevoke(_ *cobra.Command, args []string) error {
	client, _, err := newTokenCLIClient()
	if err != nil {
		return err
	}

	name := args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodDelete, "/api/v1/tokens/"+name, nil)
	if err != nil {
		return fmt.Errorf("failed to revoke token: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("token %q not found", name)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return parseAPIError(respBody, resp.StatusCode, "revoke token")
	}

	fmt.Printf("Token %q has been revoked.\n", name)
	return nil
}

// --- Helpers ---

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func parseAPIError(body []byte, statusCode int, operation string) error {
	var errResp map[string]interface{}
	if err := json.Unmarshal(body, &errResp); err == nil {
		if errMsg, ok := errResp["error"].(string); ok {
			return fmt.Errorf("failed to %s: %s", operation, errMsg)
		}
	}
	return fmt.Errorf("failed to %s: HTTP %d: %s", operation, statusCode, string(body))
}

func getMapString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func printField(label string, m map[string]interface{}, key string) {
	if v := getMapString(m, key); v != "" {
		fmt.Printf("%s%s\n", label, v)
	}
}

func printListField(label string, m map[string]interface{}, key string) {
	if items, ok := m[key].([]interface{}); ok {
		strs := make([]string, len(items))
		for i, s := range items {
			strs[i] = fmt.Sprintf("%v", s)
		}
		fmt.Printf("%s%s\n", label, strings.Join(strs, ", "))
	}
}

func joinInterfaceSlice(m map[string]interface{}, key string, maxLen int) string {
	items, ok := m[key].([]interface{})
	if !ok {
		return ""
	}
	strs := make([]string, len(items))
	for i, s := range items {
		strs[i] = fmt.Sprintf("%v", s)
	}
	result := strings.Join(strs, ",")
	if maxLen > 0 && len(result) > maxLen {
		result = result[:maxLen-3] + "..."
	}
	return result
}
