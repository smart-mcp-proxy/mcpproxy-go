package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

var (
	// token create flags
	tokenName        string
	tokenServers     string
	tokenPermissions string
	tokenExpires     string
	tokenProfilePin  string
	tokenProfile     string

	// token list filters (Spec 108-g, FR-031)
	tokenListProfile string
	tokenListToken   string

	// tokenConfigPath is the token command's --config override (GH #897).
	tokenConfigPath string
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

	addConfigFlag(tokenCmd.PersistentFlags(), &tokenConfigPath, "Path to configuration file")

	// Subcommands
	tokenCmd.AddCommand(newTokenCreateCmd())
	tokenCmd.AddCommand(newTokenListCmd())
	tokenCmd.AddCommand(newTokenShowCmd())
	tokenCmd.AddCommand(newTokenRevokeCmd())
	tokenCmd.AddCommand(newTokenDeleteCmd())
	tokenCmd.AddCommand(newTokenRegenerateCmd())

	return tokenCmd
}

func newTokenCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new agent token",
		Long: `Create a new scoped agent token for programmatic access.

The token is displayed once on creation and cannot be retrieved again.
Store it securely.

Pin the token to a profile (--profile) and its scope comes from the profile. The
older --servers/--permissions scope still works and is marked "legacy scope";
without --profile at least one of them is required.

Examples:
  mcpproxy token create --name ci --profile work-readonly
  mcpproxy token create --name deploy-bot --servers github,gitlab --permissions read,write
  mcpproxy token create --name ci-agent --servers "*" --permissions read --expires 7d`,
		RunE: runTokenCreate,
	}

	cmd.Flags().StringVar(&tokenName, "name", "", "Token name (required, unique; names starting with \"client-\" are reserved)")
	cmd.Flags().StringVar(&tokenProfile, "profile", "", "Pin the token to a profile: its scope comes from the profile and it cannot switch (recommended)")
	cmd.Flags().StringVar(&tokenServers, "servers", "", "Legacy scope: comma-separated allowed server names, or \"*\" for all (use --profile instead)")
	cmd.Flags().StringVar(&tokenPermissions, "permissions", "", "Legacy scope: comma-separated permission tiers read, write, destructive (use --profile instead)")
	cmd.Flags().StringVar(&tokenExpires, "expires", "30d", "Token expiry duration (e.g., 7d, 30d, 90d, 365d)")
	cmd.Flags().StringVar(&tokenProfilePin, "profile-pin", "", "Deprecated alias of --profile")
	_ = cmd.Flags().MarkHidden("profile-pin")
	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func newTokenListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all agent tokens",
		Long: `List all configured agent tokens with their kind, binding, status, permissions and
expiry. --profile filters by the token's current profile pin ("-" = unpinned);
--token selects one token by name.

Examples:
  mcpproxy token list
  mcpproxy token list --profile work-readonly
  mcpproxy token list -o json`,
		RunE: runTokenList,
	}
	cmd.Flags().StringVar(&tokenListProfile, "profile", "", `Only tokens currently pinned to this profile ("-" = unpinned)`)
	cmd.Flags().StringVar(&tokenListToken, "token", "", "Only the token with this name")
	return cmd
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

// loadTokenConfig loads the token command's config, honoring the --config and
// global --data-dir flags (GH #897, same class as #854).
func loadTokenConfig() (*config.Config, error) {
	return loadCLIConfig(tokenConfigPath)
}

// newTokenCLIClient creates a cliclient.Client connected to the running MCPProxy.
func newTokenCLIClient() (*cliclient.Client, *config.Config, error) {
	cfg, err := loadTokenConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	// Socket first, then TCP fallback. Never generate an API key here —
	// a fabricated key cannot match the running daemon's.
	client, ok := newDaemonClient(cfg, logger.Sugar())
	if !ok {
		return nil, nil, fmt.Errorf("mcpproxy daemon is not reachable. Start with: mcpproxy serve")
	}

	return client, cfg, nil
}

func runTokenCreate(_ *cobra.Command, _ []string) error {
	// Validate locally before any request (exit 1, nothing sent).
	profileName := tokenProfile
	if tokenProfilePin != "" {
		fmt.Fprintln(os.Stderr, "--profile-pin is deprecated; use --profile")
		if profileName != "" && profileName != tokenProfilePin {
			return newFlagValidationError("--profile and --profile-pin name different profiles; use only --profile")
		}
		profileName = tokenProfilePin
	}
	legacyScope := tokenServers != "" || tokenPermissions != ""
	if profileName == "" && !legacyScope {
		return newFlagValidationError("either --profile or --servers/--permissions is required")
	}
	if profileName == "" && legacyScope {
		fmt.Fprintln(os.Stderr, "hint: consider --profile <name> instead of --servers/--permissions (legacy scope)")
	}

	client, _, err := newTokenCLIClient()
	if err != nil {
		return err
	}

	// Build request body: the profile carries the scope, so the legacy fields
	// are sent only when given (REST defaults them under --profile).
	body := map[string]interface{}{
		"name":       tokenName,
		"expires_in": tokenExpires,
	}
	if profileName != "" {
		body["profile"] = profileName
	}
	if tokenServers != "" {
		body["allowed_servers"] = splitAndTrim(tokenServers)
	}
	if tokenPermissions != "" {
		body["permissions"] = splitAndTrim(tokenPermissions)
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

	result, err := parseTokenAPIResponse(respBody)
	if err != nil {
		return err
	}

	if structuredOutput() {
		return printTokenStructured(result)
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
	if pin := getMapString(result, "profile_pin"); pin != "" {
		fmt.Printf("  Profile:     %s\n", pin)
	}
	printField("  Expires:     ", result, "expires_at")

	return nil
}

// printTokenStructured prints a token response as json or yaml.
func printTokenStructured(result map[string]interface{}) error {
	formatter, err := GetOutputFormatter()
	if err != nil {
		return err
	}
	out, err := formatter.Format(result)
	if err != nil {
		return err
	}
	fmt.Println(strings.TrimRight(out, "\n"))
	return nil
}

func runTokenList(_ *cobra.Command, _ []string) error {
	client, _, err := newTokenCLIClient()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	path := "/api/v1/tokens"
	q := url.Values{}
	if tokenListProfile != "" {
		q.Set("profile", tokenListProfile)
	}
	if tokenListToken != "" {
		q.Set("token", tokenListToken)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	resp, err := client.DoRaw(ctx, http.MethodGet, path, nil)
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

	result, err := parseTokenAPIResponse(respBody)
	if err != nil {
		return err
	}

	if structuredOutput() {
		return printTokenStructured(result)
	}

	tokens, ok := result["tokens"].([]interface{})
	if !ok || len(tokens) == 0 {
		fmt.Println("No agent tokens configured.")
		return nil
	}

	rows := make([][]string, 0, len(tokens))
	for _, t := range tokens {
		tok, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		revoked := "no"
		if r, ok := tok["revoked"].(bool); ok && r {
			revoked = "yes"
		}
		legacy := "no"
		if l, ok := tok["legacy_scope"].(bool); ok && l {
			legacy = "yes"
		}
		kind := getMapString(tok, "kind")
		if kind == "" {
			kind = "agent"
		}

		expiresAt := getMapString(tok, "expires_at")
		if expiresAt != "" {
			if t, parseErr := time.Parse(time.RFC3339, expiresAt); parseErr == nil {
				expiresAt = t.Format("2006-01-02 15:04")
			}
		}

		rows = append(rows, []string{
			getMapString(tok, "name"), getMapString(tok, "token_prefix"), kind,
			dash(getMapString(tok, "client_id")), dash(getMapString(tok, "profile_pin")), dash(getMapString(tok, "profile_mode")),
			dash(joinInterfaceSlice(tok, "allowed_servers", 23)), dash(joinInterfaceSlice(tok, "permissions", 0)),
			legacy, revoked, expiresAt,
		})
	}
	return printTable([]string{"NAME", "PREFIX", "KIND", "CLIENT", "PROFILE", "MODE", "SERVERS", "PERMISSIONS", "LEGACY SCOPE", "REVOKED", "EXPIRES"}, rows)
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

	result, err := parseTokenAPIResponse(respBody)
	if err != nil {
		return err
	}

	if structuredOutput() {
		return printTokenStructured(result)
	}

	// Pretty print
	printField("Name:           ", result, "name")
	printField("Token Prefix:   ", result, "token_prefix")
	kind := getMapString(result, "kind")
	if kind == "" {
		kind = "agent"
	}
	fmt.Printf("Kind:           %s\n", kind)
	if clientID := getMapString(result, "client_id"); clientID != "" {
		fmt.Printf("Client:         %s\n", clientID)
	}
	printListField("Servers:        ", result, "allowed_servers")
	printListField("Permissions:    ", result, "permissions")
	if pin := getMapString(result, "profile_pin"); pin != "" {
		fmt.Printf("Profile:        %s\n", pin)
	}
	if mode := getMapString(result, "profile_mode"); mode != "" {
		fmt.Printf("Mode:           %s\n", mode)
	}
	if legacy, ok := result["legacy_scope"].(bool); ok && legacy {
		fmt.Printf("Legacy scope:   yes (servers %s, permissions %s) — migrate: mcpproxy token create --profile <p>\n",
			joinInterfaceSlice(result, "allowed_servers", 0), joinInterfaceSlice(result, "permissions", 0))
	}
	if kind == "client" {
		fmt.Printf("Manage with:    mcpproxy client show %s\n", getMapString(result, "client_id"))
	}
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

func newTokenDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm", "remove"},
		Short:   "Permanently delete an agent token",
		Long: `Permanently delete an agent token, removing it entirely and freeing its
name for reuse. Unlike revoke (a soft delete that keeps the record so the name
stays reserved), delete removes the token completely.

Examples:
  mcpproxy token delete deploy-bot`,
		Args: cobra.ExactArgs(1),
		RunE: runTokenDelete,
	}
}

func runTokenDelete(_ *cobra.Command, args []string) error {
	client, _, err := newTokenCLIClient()
	if err != nil {
		return err
	}

	name := args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodDelete, "/api/v1/tokens/"+name+"/permanent", nil)
	if err != nil {
		return fmt.Errorf("failed to delete token: %w", err)
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
		return parseAPIError(respBody, resp.StatusCode, "delete token")
	}

	fmt.Printf("Token %q has been permanently deleted.\n", name)
	return nil
}

func newTokenRegenerateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "regenerate <name>",
		Short: "Regenerate an agent token secret",
		Long: `Regenerate the secret for an existing agent token.

The old token secret is immediately invalidated and a new one is generated.
The new token is displayed once and cannot be retrieved again.

Examples:
  mcpproxy token regenerate deploy-bot
  mcpproxy token regenerate deploy-bot -o json`,
		Args: cobra.ExactArgs(1),
		RunE: runTokenRegenerate,
	}
}

func runTokenRegenerate(_ *cobra.Command, args []string) error {
	client, _, err := newTokenCLIClient()
	if err != nil {
		return err
	}

	name := args[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.DoRaw(ctx, http.MethodPost, "/api/v1/tokens/"+name+"/regenerate", nil)
	if err != nil {
		return fmt.Errorf("failed to regenerate token: %w", err)
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
		return parseAPIError(respBody, resp.StatusCode, "regenerate token")
	}

	result, err := parseTokenAPIResponse(respBody)
	if err != nil {
		return err
	}

	format := ResolveOutputFormat()
	if format == "json" {
		formatted, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(formatted))
		return nil
	}

	// Table output — highlight the new token since it's only shown once
	fmt.Printf("Token %q regenerated successfully.\n", name)
	fmt.Println()
	if token, ok := result["token"].(string); ok {
		fmt.Printf("  Token: %s\n", token)
		fmt.Println()
		fmt.Println("  IMPORTANT: Save this token now. It cannot be retrieved again.")
		fmt.Println()
	}

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

// parseTokenAPIResponse unmarshals a token REST response and unwraps the
// standard {"success":true,"data":{...}} envelope (contracts.APIResponse).
// The CLI table paths read fields like "token"/"tokens" at the top level, so
// without unwrapping, `token list` always printed "No agent tokens configured"
// and `token create` never displayed the minted token (found verifying #897).
// A body without the envelope is passed through unchanged.
func parseTokenAPIResponse(body []byte) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if data, ok := result["data"].(map[string]interface{}); ok {
		return data, nil
	}
	return result, nil
}

func parseAPIError(body []byte, statusCode int, operation string) error {
	var errResp map[string]interface{}
	if err := json.Unmarshal(body, &errResp); err == nil {
		if errMsg, ok := errResp["error"].(string); ok {
			if field, ok := errResp["field"].(string); ok && field != "" {
				// A validator refusal names its field; exit 1 like every
				// other flag-validation failure.
				return flagValidationError{fmt.Errorf("failed to %s: %s (field: %s)", operation, errMsg, field)}
			}
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
