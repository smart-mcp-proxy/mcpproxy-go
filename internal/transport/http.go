package transport

import (
	"fmt"

	"mcpproxy-go/internal/config"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
)

const (
	TransportHTTP           = "http"
	TransportStreamableHTTP = "streamable-http"
	TransportSSE            = "sse"
	TransportStdio          = "stdio"
)

// HTTPTransportConfig holds configuration for HTTP transport
type HTTPTransportConfig struct {
	URL         string
	Headers     map[string]string
	OAuthConfig *client.OAuthConfig
	UseOAuth    bool
}

// CreateHTTPClient creates a new MCP client using HTTP transport
func CreateHTTPClient(cfg *HTTPTransportConfig) (*client.Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("no URL specified for HTTP transport")
	}

	if cfg.UseOAuth && cfg.OAuthConfig != nil {
		// Use OAuth-enabled client
		return client.NewOAuthStreamableHttpClient(cfg.URL, *cfg.OAuthConfig)
	}

	// Use regular HTTP client
	if len(cfg.Headers) > 0 {
		httpTransport, err := transport.NewStreamableHTTP(cfg.URL,
			transport.WithHTTPHeaders(cfg.Headers))
		if err != nil {
			return nil, fmt.Errorf("failed to create HTTP transport: %w", err)
		}
		return client.NewClient(httpTransport), nil
	}

	httpTransport, err := transport.NewStreamableHTTP(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP transport: %w", err)
	}
	return client.NewClient(httpTransport), nil
}

// CreateSSEClient creates a new MCP client using SSE transport
func CreateSSEClient(cfg *HTTPTransportConfig) (*client.Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("no URL specified for SSE transport")
	}

	if cfg.UseOAuth && cfg.OAuthConfig != nil {
		// Use OAuth-enabled SSE client
		return client.NewOAuthSSEClient(cfg.URL, *cfg.OAuthConfig)
	}

	// Use regular SSE client
	if len(cfg.Headers) > 0 {
		sseClient, err := client.NewSSEMCPClient(cfg.URL,
			client.WithHeaders(cfg.Headers))
		if err != nil {
			return nil, fmt.Errorf("failed to create SSE client: %w", err)
		}
		return sseClient, nil
	}

	sseClient, err := client.NewSSEMCPClient(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSE client: %w", err)
	}
	return sseClient, nil
}

// CreateHTTPTransportConfig creates an HTTP transport config from server config
func CreateHTTPTransportConfig(serverConfig *config.ServerConfig, oauthConfig *client.OAuthConfig) *HTTPTransportConfig {
	return &HTTPTransportConfig{
		URL:         serverConfig.URL,
		Headers:     serverConfig.Headers,
		OAuthConfig: oauthConfig,
		UseOAuth:    oauthConfig != nil,
	}
}

// DetermineTransportType determines the transport type based on URL and config
func DetermineTransportType(serverConfig *config.ServerConfig) string {
	if serverConfig.Protocol != "" && serverConfig.Protocol != "auto" {
		return serverConfig.Protocol
	}

	// Auto-detect based on command first (highest priority)
	if serverConfig.Command != "" {
		return TransportStdio
	}

	// Auto-detect based on URL
	if serverConfig.URL != "" {
		return TransportStreamableHTTP
	}

	// Default to stdio
	return TransportStdio
}
