package oauth

import (
	"fmt"

	"mcpproxy-go/internal/config"

	"github.com/mark3labs/mcp-go/client"
)

const (
	// Default OAuth redirect URI
	DefaultRedirectURI = "http://localhost:8085/oauth/callback"

	// Default OAuth scopes
	DefaultScopes = "mcp.read,mcp.write"
)

// CreateOAuthConfig creates an OAuth configuration from server config
func CreateOAuthConfig(_ *config.ServerConfig, oauthConfig *config.OAuthConfig) *client.OAuthConfig {
	if oauthConfig == nil {
		return nil
	}

	// Use defaults if not specified
	redirectURI := oauthConfig.RedirectURI
	if redirectURI == "" {
		redirectURI = DefaultRedirectURI
	}

	scopes := oauthConfig.Scopes
	if len(scopes) == 0 {
		scopes = []string{"mcp.read", "mcp.write"}
	}

	return &client.OAuthConfig{
		ClientID:     oauthConfig.ClientID,
		ClientSecret: oauthConfig.ClientSecret,
		RedirectURI:  redirectURI,
		Scopes:       scopes,
		TokenStore:   client.NewMemoryTokenStore(),
		PKCEEnabled:  oauthConfig.PKCEEnabled,
	}
}

// ShouldUseOAuth determines if OAuth should be used for a given server
func ShouldUseOAuth(serverConfig *config.ServerConfig) bool {
	// Only HTTP and SSE transports support OAuth
	if serverConfig.Protocol == "stdio" {
		return false
	}

	// Check if we have explicit OAuth configuration
	if serverConfig.OAuth != nil {
		// Only use OAuth if we have at least ClientID or ClientSecret configured
		return serverConfig.OAuth.ClientID != "" || serverConfig.OAuth.ClientSecret != ""
	}

	// Check if we have OAuth configuration from environment
	envConfig := GetOAuthConfigFromEnv(serverConfig.Name)
	if envConfig != nil {
		return envConfig.ClientID != "" || envConfig.ClientSecret != ""
	}

	// Default to no OAuth if no configuration is available
	// This allows the library to gracefully handle non-OAuth servers
	return false
}

// GetOAuthConfigFromEnv gets OAuth configuration from environment variables
func GetOAuthConfigFromEnv(serverName string) *config.OAuthConfig {
	// Look for server-specific environment variables first
	clientID := getEnvWithFallback(
		fmt.Sprintf("MCP_%s_CLIENT_ID", serverName),
		"MCP_CLIENT_ID",
	)

	clientSecret := getEnvWithFallback(
		fmt.Sprintf("MCP_%s_CLIENT_SECRET", serverName),
		"MCP_CLIENT_SECRET",
	)

	redirectURI := getEnvWithFallback(
		fmt.Sprintf("MCP_%s_REDIRECT_URI", serverName),
		"MCP_REDIRECT_URI",
	)

	if clientID == "" && clientSecret == "" && redirectURI == "" {
		return nil
	}

	return &config.OAuthConfig{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
		Scopes:       []string{"mcp.read", "mcp.write"},
		PKCEEnabled:  true,
	}
}

// getEnvWithFallback gets an environment variable with a fallback
func getEnvWithFallback(_, _ string) string {
	// For this implementation, we'll return empty string
	// In a real implementation, we'd use os.Getenv
	return ""
}

// MergeOAuthConfig merges OAuth configuration from different sources
func MergeOAuthConfig(serverConfig *config.ServerConfig, envConfig *config.OAuthConfig) *config.OAuthConfig {
	// Start with server config OAuth if available
	result := &config.OAuthConfig{
		PKCEEnabled: true, // Default to enabled
	}

	if serverConfig.OAuth != nil {
		result.ClientID = serverConfig.OAuth.ClientID
		result.ClientSecret = serverConfig.OAuth.ClientSecret
		result.RedirectURI = serverConfig.OAuth.RedirectURI
		result.Scopes = serverConfig.OAuth.Scopes
		result.PKCEEnabled = serverConfig.OAuth.PKCEEnabled
	}

	// Override with environment config if available
	if envConfig != nil {
		if envConfig.ClientID != "" {
			result.ClientID = envConfig.ClientID
		}
		if envConfig.ClientSecret != "" {
			result.ClientSecret = envConfig.ClientSecret
		}
		if envConfig.RedirectURI != "" {
			result.RedirectURI = envConfig.RedirectURI
		}
		if len(envConfig.Scopes) > 0 {
			result.Scopes = envConfig.Scopes
		}
	}

	return result
}
