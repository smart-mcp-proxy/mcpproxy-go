//go:build server

package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Server-edition flags for the credential command group. The credential broker
// surfaces (spec 074) live behind session-or-Bearer auth, so the CLI targets a
// server URL and presents a user JWT — unlike the API-key/socket commands.
var (
	credServerURL string
	credToken     string
)

// newCredentialCommand builds the `mcpproxy credential` command tree (server
// edition only). It manages per-user brokered upstream credentials via the
// T8 REST surfaces and never prints secret values (FR-026).
func newCredentialCommand() *cobra.Command {
	credentialCmd := &cobra.Command{
		Use:   "credential",
		Short: "Manage per-user brokered upstream credentials (server edition)",
		Long: `Inspect and manage your brokered credentials for shared upstream servers.

These commands talk to a running server-edition mcpproxy and require a user
token (a JWT obtained from the Web UI or POST /api/v1/auth/token). Provide it
with --token or the MCPPROXY_TOKEN environment variable, and point at the
server with --url or MCPPROXY_SERVER_URL.

Secret values are never displayed (FR-026).

Examples:
  mcpproxy credential list
  mcpproxy credential status github
  mcpproxy credential connect github
  mcpproxy credential rm github`,
	}

	credentialCmd.PersistentFlags().StringVar(&credServerURL, "url", "", "Base URL of the server-edition mcpproxy (default: $MCPPROXY_SERVER_URL or local listen address)")
	credentialCmd.PersistentFlags().StringVar(&credToken, "token", "", "User JWT bearer token (default: $MCPPROXY_TOKEN)")

	credentialCmd.AddCommand(newCredentialListCmd())
	credentialCmd.AddCommand(newCredentialStatusCmd())
	credentialCmd.AddCommand(newCredentialConnectCmd())
	credentialCmd.AddCommand(newCredentialRemoveCmd())

	return credentialCmd
}

func newCredentialListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List brokered upstreams with connection status (no secrets)",
		Long: `List every brokered upstream and your connection status for it.

Examples:
  mcpproxy credential list
  mcpproxy credential list -o json`,
		Args: cobra.NoArgs,
		RunE: runCredentialList,
	}
}

func newCredentialStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <server>",
		Short: "Show one upstream's connection detail (no secrets)",
		Long: `Show the connection detail for a single brokered upstream.

Examples:
  mcpproxy credential status github
  mcpproxy credential status github -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: runCredentialStatus,
	}
}

func newCredentialConnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "connect <server>",
		Short: "Print the browser URL to connect an upstream credential",
		Long: `Print the connect URL for a brokered upstream. Open it in a browser where
you are signed in to mcpproxy to complete the OAuth connect flow; the proxy
binds the flow to your user and stores the resulting credential server-side.

Examples:
  mcpproxy credential connect github`,
		Args: cobra.ExactArgs(1),
		RunE: runCredentialConnect,
	}
}

func newCredentialRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm <server>",
		Aliases: []string{"remove", "disconnect"},
		Short:   "Disconnect (revoke) your credential for an upstream",
		Long: `Disconnect and revoke your stored credential for a brokered upstream.

Examples:
  mcpproxy credential rm github`,
		Args: cobra.ExactArgs(1),
		RunE: runCredentialRemove,
	}
}

// --- client wiring ---

// resolveCredentialBaseURL determines the server base URL from the --url flag,
// the MCPPROXY_SERVER_URL env var, or the local listen address in config.
func resolveCredentialBaseURL() string {
	if credServerURL != "" {
		return strings.TrimRight(credServerURL, "/")
	}
	if env := os.Getenv("MCPPROXY_SERVER_URL"); env != "" {
		return strings.TrimRight(env, "/")
	}
	if cfg, err := config.Load(); err == nil && cfg.Listen != "" {
		listen := cfg.Listen
		if strings.HasPrefix(listen, ":") {
			listen = "127.0.0.1" + listen
		}
		return "http://" + listen
	}
	return "http://127.0.0.1:8080"
}

// resolveCredentialToken returns the user JWT from --token or MCPPROXY_TOKEN.
func resolveCredentialToken() string {
	if credToken != "" {
		return credToken
	}
	return os.Getenv("MCPPROXY_TOKEN")
}

func newCredentialClient() (*cliclient.Client, string) {
	baseURL := resolveCredentialBaseURL()
	logger, _ := zap.NewProduction()
	return cliclient.NewClientWithBearer(baseURL, resolveCredentialToken(), logger.Sugar()), baseURL
}

// --- run functions ---

func runCredentialList(_ *cobra.Command, _ []string) error {
	client, _ := newCredentialClient()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	creds, err := client.ListCredentials(ctx)
	if err != nil {
		return err
	}
	return emitCredentials(creds)
}

func runCredentialStatus(_ *cobra.Command, args []string) error {
	client, _ := newCredentialClient()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	creds, err := client.ListCredentials(ctx)
	if err != nil {
		return err
	}
	cred, ok := findCredential(creds, args[0])
	if !ok {
		return fmt.Errorf("brokered server %q not found", args[0])
	}
	return emitCredentialDetail(cred)
}

func runCredentialConnect(_ *cobra.Command, args []string) error {
	baseURL := resolveCredentialBaseURL()
	connectURL := credentialConnectURL(baseURL, args[0])

	format := ResolveOutputFormat()
	if format == "json" || format == "yaml" {
		return emitFormatted(map[string]string{"server": args[0], "connect_url": connectURL})
	}

	fmt.Printf("Open this URL in a browser where you are signed in to mcpproxy:\n\n  %s\n\n", connectURL)
	fmt.Println("After authorizing, the credential is stored server-side and tied to your user.")
	return nil
}

func runCredentialRemove(_ *cobra.Command, args []string) error {
	client, _ := newCredentialClient()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	msg, err := client.DeleteCredential(ctx, args[0])
	if err != nil {
		return err
	}
	if msg == "" {
		msg = fmt.Sprintf("Disconnected credential for %q", args[0])
	}
	fmt.Println(msg)
	return nil
}

// --- pure helpers (kept side-effect-free for testing) ---

// findCredential looks up a credential by case-insensitive server name.
func findCredential(creds []cliclient.CredentialStatus, name string) (cliclient.CredentialStatus, bool) {
	for _, c := range creds {
		if strings.EqualFold(c.Server, name) {
			return c, true
		}
	}
	return cliclient.CredentialStatus{}, false
}

// credentialConnectURL builds the browser connect URL for a brokered upstream.
// The server name is path-escaped so namespace/name registry identifiers do not
// inject extra path segments (cf. MCP-1111).
func credentialConnectURL(baseURL, server string) string {
	return fmt.Sprintf("%s/api/v1/user/credentials/%s/connect",
		strings.TrimRight(baseURL, "/"), url.PathEscape(server))
}

// renderCredentialsTable renders the list view. It only ever reads the
// non-secret fields of CredentialStatus, so no secret can appear (FR-026).
func renderCredentialsTable(creds []cliclient.CredentialStatus) string {
	if len(creds) == 0 {
		return "No brokered upstreams configured."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-24s %-16s %-15s %-10s %-20s\n", "SERVER", "MODE", "STATUS", "TOKEN", "EXPIRES")
	b.WriteString(strings.Repeat("-", 90) + "\n")
	needsConnect := false
	for _, c := range creds {
		expires := "-"
		if c.ExpiresAt != nil {
			expires = c.ExpiresAt.Format("2006-01-02 15:04")
		}
		tokenType := c.TokenType
		if tokenType == "" {
			tokenType = "-"
		}
		marker := ""
		if c.ConnectPath != "" {
			marker = " *"
			needsConnect = true
		}
		fmt.Fprintf(&b, "%-24s %-16s %-15s %-10s %-20s\n",
			truncateCell(c.Server, 24), truncateCell(c.Mode, 16), c.Status+marker, truncateCell(tokenType, 10), expires)
	}
	if needsConnect {
		b.WriteString("\n* connectable: run 'mcpproxy credential connect <server>'\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderCredentialDetail renders the single-server status view.
func renderCredentialDetail(c cliclient.CredentialStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Server:       %s\n", c.Server)
	fmt.Fprintf(&b, "Mode:         %s\n", c.Mode)
	fmt.Fprintf(&b, "Status:       %s\n", c.Status)
	if c.TokenType != "" {
		fmt.Fprintf(&b, "Token Type:   %s\n", c.TokenType)
	}
	if len(c.Scopes) > 0 {
		fmt.Fprintf(&b, "Scopes:       %s\n", strings.Join(c.Scopes, ", "))
	}
	if c.Audience != "" {
		fmt.Fprintf(&b, "Audience:     %s\n", c.Audience)
	}
	if c.ObtainedVia != "" {
		fmt.Fprintf(&b, "Obtained Via: %s\n", c.ObtainedVia)
	}
	if c.ExpiresAt != nil {
		fmt.Fprintf(&b, "Expires:      %s\n", c.ExpiresAt.Format("2006-01-02 15:04:05"))
	}
	if c.UpdatedAt != nil {
		fmt.Fprintf(&b, "Updated:      %s\n", c.UpdatedAt.Format("2006-01-02 15:04:05"))
	}
	if c.ConnectPath != "" {
		fmt.Fprintf(&b, "Connect:      mcpproxy credential connect %s\n", c.Server)
	}
	return strings.TrimRight(b.String(), "\n")
}

func truncateCell(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// emitCredentials prints the list in the resolved output format.
func emitCredentials(creds []cliclient.CredentialStatus) error {
	switch ResolveOutputFormat() {
	case "json", "yaml":
		return emitFormatted(creds)
	default:
		fmt.Println(renderCredentialsTable(creds))
		return nil
	}
}

func emitCredentialDetail(c cliclient.CredentialStatus) error {
	switch ResolveOutputFormat() {
	case "json", "yaml":
		return emitFormatted(c)
	default:
		fmt.Println(renderCredentialDetail(c))
		return nil
	}
}

// emitFormatted renders arbitrary data via the resolved structured formatter.
func emitFormatted(data interface{}) error {
	formatter, err := GetOutputFormatter()
	if err != nil {
		return fmt.Errorf("failed to get output formatter: %w", err)
	}
	out, err := formatter.Format(data)
	if err != nil {
		return fmt.Errorf("failed to format output: %w", err)
	}
	fmt.Println(out)
	return nil
}
