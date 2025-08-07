package transport

import (
	"fmt"
	"net/http"
	"time"

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

// HTTPError represents detailed HTTP error information for debugging
type HTTPError struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Method     string            `json:"method"`
	URL        string            `json:"url"`
	Err        error             `json:"-"` // Original error
}

func (e *HTTPError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("HTTP %d %s: %s", e.StatusCode, http.StatusText(e.StatusCode), e.Body)
	}
	return fmt.Sprintf("HTTP %d %s", e.StatusCode, http.StatusText(e.StatusCode))
}

// JSONRPCError represents JSON-RPC specific error information
type JSONRPCError struct {
	Code      int         `json:"code"`
	Message   string      `json:"message"`
	Data      interface{} `json:"data,omitempty"`
	HTTPError *HTTPError  `json:"http_error,omitempty"`
}

func (e *JSONRPCError) Error() string {
	if e.HTTPError != nil {
		return fmt.Sprintf("JSON-RPC Error %d: %s (HTTP: %s)", e.Code, e.Message, e.HTTPError.Error())
	}
	return fmt.Sprintf("JSON-RPC Error %d: %s", e.Code, e.Message)
}

// HTTPResponseDetails captures detailed HTTP response information for debugging
type HTTPResponseDetails struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	URL        string            `json:"url"`
	Method     string            `json:"method"`
}

// EnhancedHTTPError creates an HTTPError with full context
func NewHTTPError(statusCode int, body, method, url string, headers map[string]string, originalErr error) *HTTPError {
	return &HTTPError{
		StatusCode: statusCode,
		Headers:    headers,
		Body:       body,
		Method:     method,
		URL:        url,
		Err:        originalErr,
	}
}

// NewJSONRPCError creates a JSONRPCError with optional HTTP context
func NewJSONRPCError(code int, message string, data interface{}, httpErr *HTTPError) *JSONRPCError {
	return &JSONRPCError{
		Code:      code,
		Message:   message,
		Data:      data,
		HTTPError: httpErr,
	}
}

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

	httpTransport, err := transport.NewStreamableHTTP(cfg.URL,
		transport.WithHTTPTimeout(180*time.Second)) // Increased timeout for HTTP connections
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
		// Create custom HTTP client with longer timeout for SSE
		httpClient := &http.Client{
			Timeout: 180 * time.Second, // Increased timeout for SSE connections
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     90 * time.Second,
				DisableCompression:  false,
				DisableKeepAlives:   false, // Enable keep-alives for SSE stability
				MaxIdleConnsPerHost: 5,
			},
		}

		zap.L().Debug("Creating SSE MCP client with custom HTTP timeout and headers",
			zap.String("url", cfg.URL),
			zap.Duration("timeout", 180*time.Second),
			zap.Int("header_count", len(cfg.Headers)))

		sseClient, err := client.NewSSEMCPClient(cfg.URL,
			client.WithHTTPClient(httpClient),
			client.WithHeaders(cfg.Headers))
		if err != nil {
			return nil, fmt.Errorf("failed to create SSE client: %w", err)
		}
		return sseClient, nil
	}

	// Create custom HTTP client with longer timeout for SSE
	httpClient := &http.Client{
		Timeout: 180 * time.Second, // Increased timeout for SSE connections
		Transport: &http.Transport{
			MaxIdleConns:        10,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  false,
			DisableKeepAlives:   false, // Enable keep-alives for SSE stability
			MaxIdleConnsPerHost: 5,
		},
	}

	zap.L().Debug("Creating SSE MCP client with custom HTTP timeout",
		zap.String("url", cfg.URL),
		zap.Duration("timeout", 180*time.Second))

	// Enhanced trace-level debugging for SSE transport
	if zap.L().Core().Enabled(zap.DebugLevel - 1) { // Trace level
		zap.L().Debug("TRACE SSE TRANSPORT SETUP",
			zap.String("transport_type", "sse"),
			zap.String("url", cfg.URL),
			zap.Duration("http_timeout", 180*time.Second),
			zap.String("debug_note", "SSE client will establish persistent connection for JSON-RPC over SSE"))
	}

	sseClient, err := client.NewSSEMCPClient(cfg.URL,
		client.WithHTTPClient(httpClient))
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
