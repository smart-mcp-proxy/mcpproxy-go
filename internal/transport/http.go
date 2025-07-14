package transport

import (
	"fmt"

	"mcpproxy-go/internal/config"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"go.uber.org/zap"
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
	logger := zap.L().Named("transport")

	if cfg.URL == "" {
		return nil, fmt.Errorf("no URL specified for HTTP transport")
	}

	logger.Debug("Creating HTTP client",
		zap.String("url", cfg.URL),
		zap.Bool("use_oauth", cfg.UseOAuth),
		zap.Bool("has_oauth_config", cfg.OAuthConfig != nil))

	if cfg.UseOAuth && cfg.OAuthConfig != nil {
		// Use OAuth-enabled client with Dynamic Client Registration
		logger.Info("Creating OAuth-enabled streamable HTTP client with Dynamic Client Registration",
			zap.String("url", cfg.URL),
			zap.String("redirect_uri", cfg.OAuthConfig.RedirectURI),
			zap.Strings("scopes", cfg.OAuthConfig.Scopes),
			zap.Bool("pkce_enabled", cfg.OAuthConfig.PKCEEnabled))

		logger.Debug("OAuth config details",
			zap.String("client_id", cfg.OAuthConfig.ClientID),
			zap.String("client_secret", cfg.OAuthConfig.ClientSecret),
			zap.Any("token_store", cfg.OAuthConfig.TokenStore))

		client, err := client.NewOAuthStreamableHttpClient(cfg.URL, *cfg.OAuthConfig)
		if err != nil {
			logger.Error("Failed to create OAuth client", zap.Error(err))
			return nil, fmt.Errorf("failed to create OAuth client: %w", err)
		}

		logger.Debug("OAuth-enabled HTTP client created successfully")
		return client, nil
	}

	logger.Debug("Creating regular HTTP client", zap.String("url", cfg.URL))
	// Use regular HTTP client
	if len(cfg.Headers) > 0 {
		logger.Debug("Adding HTTP headers", zap.Int("header_count", len(cfg.Headers)))
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
	logger := zap.L().Named("transport")

	if cfg.URL == "" {
		return nil, fmt.Errorf("no URL specified for SSE transport")
	}

	logger.Debug("Creating SSE client",
		zap.String("url", cfg.URL),
		zap.Bool("use_oauth", cfg.UseOAuth),
		zap.Bool("has_oauth_config", cfg.OAuthConfig != nil))

	if cfg.UseOAuth && cfg.OAuthConfig != nil {
		// Use OAuth-enabled SSE client with Dynamic Client Registration
		logger.Info("Creating OAuth-enabled SSE client with Dynamic Client Registration",
			zap.String("url", cfg.URL),
			zap.String("redirect_uri", cfg.OAuthConfig.RedirectURI),
			zap.Strings("scopes", cfg.OAuthConfig.Scopes),
			zap.Bool("pkce_enabled", cfg.OAuthConfig.PKCEEnabled))

		logger.Debug("OAuth SSE config details",
			zap.String("client_id", cfg.OAuthConfig.ClientID),
			zap.String("client_secret", cfg.OAuthConfig.ClientSecret),
			zap.Any("token_store", cfg.OAuthConfig.TokenStore))

		client, err := client.NewOAuthSSEClient(cfg.URL, *cfg.OAuthConfig)
		if err != nil {
			logger.Error("Failed to create OAuth SSE client", zap.Error(err))
			return nil, fmt.Errorf("failed to create OAuth SSE client: %w", err)
		}

		logger.Debug("OAuth-enabled SSE client created successfully")
		return client, nil
	}

	logger.Debug("Creating regular SSE client", zap.String("url", cfg.URL))
	// Use regular SSE client
	if len(cfg.Headers) > 0 {
		logger.Debug("Adding SSE headers", zap.Int("header_count", len(cfg.Headers)))
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
