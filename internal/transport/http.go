package transport

import (
	"fmt"
	"net/http"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

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

var (
	// GlobalTraceEnabled controls whether HTTP/SSE frame tracing is enabled
	// This can be set by CLI flags or other callers
	GlobalTraceEnabled = false
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

// ErrEndpointDeprecated represents a 410 Gone response indicating the endpoint has been deprecated
type ErrEndpointDeprecated struct {
	URL            string `json:"url"`
	Message        string `json:"message"`
	MigrationGuide string `json:"migration_guide,omitempty"`
	NewEndpoint    string `json:"new_endpoint,omitempty"`
}

func (e *ErrEndpointDeprecated) Error() string {
	if e.NewEndpoint != "" {
		return fmt.Sprintf("endpoint deprecated (410 Gone): %s - migrate to: %s", e.Message, e.NewEndpoint)
	}
	if e.MigrationGuide != "" {
		return fmt.Sprintf("endpoint deprecated (410 Gone): %s - see: %s", e.Message, e.MigrationGuide)
	}
	return fmt.Sprintf("endpoint deprecated (410 Gone): %s", e.Message)
}

// IsEndpointDeprecatedError checks if an error indicates a deprecated endpoint (HTTP 410)
func IsEndpointDeprecatedError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*ErrEndpointDeprecated)
	return ok
}

// NewEndpointDeprecatedError creates a new ErrEndpointDeprecated from response details
func NewEndpointDeprecatedError(url, message, migrationGuide, newEndpoint string) *ErrEndpointDeprecated {
	return &ErrEndpointDeprecated{
		URL:            url,
		Message:        message,
		MigrationGuide: migrationGuide,
		NewEndpoint:    newEndpoint,
	}
}

// HTTPTransportConfig holds configuration for HTTP transport
type HTTPTransportConfig struct {
	URL         string
	Headers     map[string]string
	OAuthConfig *client.OAuthConfig
	UseOAuth    bool
	// BrokeredAuth, when set, injects a per-user resolved credential into the
	// outbound headers, replacing any configured/inbound auth header (spec 074
	// FR-016/FR-017). It is edition-neutral plain data so the server-edition
	// credential broker can drive injection without this package importing it.
	BrokeredAuth *BrokeredAuth
	TraceEnabled bool // Enable detailed HTTP/SSE frame tracing
	// RetryAfter, when set, receives the `Retry-After` hints observed on this
	// upstream's rate-limited responses (#1040). mcp-go flattens non-2xx
	// responses into strings, so a RoundTripper is the last place the header
	// still exists. nil disables the wrapper entirely (unchanged behaviour).
	RetryAfter *RetryAfterRecorder
}

// upstreamRoundTripper composes the outbound transport wrappers for an upstream
// HTTP/SSE client: frame tracing (opt-in) and the Retry-After recorder (#1040).
// It always WRAPS the given base rather than replacing it, and it sits strictly
// below mcp-go's OAuth handling — the OAuth authorize/token wrappers live above
// this layer and are untouched.
func (cfg *HTTPTransportConfig) upstreamRoundTripper(base http.RoundTripper, logger *zap.Logger) http.RoundTripper {
	rt := base
	if rt == nil {
		rt = http.DefaultTransport
	}
	if cfg.TraceEnabled {
		rt = NewLoggingTransport(rt, logger)
	}
	if cfg.RetryAfter != nil {
		rt = NewRetryAfterTransport(rt, cfg.RetryAfter, logger)
	}
	return rt
}

// needsCustomTransport reports whether this config requires us to hand mcp-go
// our own *http.Client instead of letting it build the default one.
func (cfg *HTTPTransportConfig) needsCustomTransport() bool {
	return cfg.TraceEnabled || cfg.RetryAfter != nil
}

// effectiveHeaders returns the outbound header set, applying brokered per-user
// auth injection when configured (spec 074 FR-016/FR-017).
func (cfg *HTTPTransportConfig) effectiveHeaders() map[string]string {
	if cfg.BrokeredAuth == nil {
		return cfg.Headers
	}
	return EffectiveHeaders(cfg.Headers, cfg.BrokeredAuth)
}

// CreateHTTPClient creates a new MCP client using HTTP transport
func CreateHTTPClient(cfg *HTTPTransportConfig) (*client.Client, error) {
	logger := zap.L().Named("transport")

	logger.Error("🚨 TRANSPORT HTTP CLIENT CREATION",
		zap.String("url", cfg.URL),
		zap.Bool("oauth_config_nil", cfg.OAuthConfig == nil),
		zap.Bool("use_oauth", cfg.UseOAuth))

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

		logger.Debug("🔧 About to create OAuth client with mcp-go library",
			zap.String("url", cfg.URL),
			zap.String("redirect_uri", cfg.OAuthConfig.RedirectURI))

		logger.Info("Creating OAuth HTTP client with context-based timeout",
			zap.String("url", cfg.URL),
			zap.String("note", "Using 30-minute context timeout from tray"))

		// Add detailed logging about the OAuth config and token store
		logger.Info("🔍 OAuth HTTP client creation details",
			zap.String("url", cfg.URL),
			zap.String("redirect_uri", cfg.OAuthConfig.RedirectURI),
			zap.Strings("scopes", cfg.OAuthConfig.Scopes),
			zap.Bool("pkce_enabled", cfg.OAuthConfig.PKCEEnabled),
			zap.String("client_id", cfg.OAuthConfig.ClientID),
			zap.Bool("has_token_store", cfg.OAuthConfig.TokenStore != nil))

		// Log if extra params wrapper is active (custom HTTP client configured)
		if cfg.OAuthConfig.HTTPClient != nil {
			logger.Debug("🔧 Using custom HTTP client with OAuth extra params wrapper",
				zap.String("note", "Extra parameters will be injected into OAuth requests"))
		}

		// mcp-go builds the OAuth client's transport itself; OAuthConfig.HTTPClient
		// only covers the OAuth metadata/DCR/token requests, NOT the MCP requests.
		// Passing our own basic client is therefore the only way the Retry-After
		// recorder (#1040) sees a 429 on the MCP endpoint. No Timeout is set, to
		// preserve mcp-go's default for this branch (OAuth flows rely on the
		// caller's context deadline, which can be up to 30 minutes).
		var oauthOpts []transport.StreamableHTTPCOption
		if cfg.needsCustomTransport() {
			oauthOpts = append(oauthOpts, transport.WithHTTPBasicClient(&http.Client{
				Transport: cfg.upstreamRoundTripper(http.DefaultTransport, logger),
			}))
		}

		client, err := client.NewOAuthStreamableHttpClient(cfg.URL, *cfg.OAuthConfig, oauthOpts...)
		if err != nil {
			logger.Error("Failed to create OAuth client", zap.Error(err))
			return nil, fmt.Errorf("failed to create OAuth client: %w", err)
		}

		logger.Info("✅ OAuth-enabled HTTP client created successfully")
		return client, nil
	}

	logger.Debug("Creating regular HTTP client", zap.String("url", cfg.URL))

	// Apply brokered per-user auth injection (spec 074): replaces any configured
	// auth header with the resolved per-user credential and never forwards the
	// inbound gateway/IdP token (FR-017).
	headers := cfg.effectiveHeaders()

	opts := []transport.StreamableHTTPCOption{}
	if len(headers) > 0 {
		logger.Debug("Adding HTTP headers", zap.Int("header_count", len(headers)))
		opts = append(opts, transport.WithHTTPHeaders(headers))
	}

	switch {
	case cfg.needsCustomTransport():
		// Timeout policy is preserved exactly as it was before #1040: the trace
		// client and the header-less client carry a 180s request timeout, while
		// the headers variant has always run on mcp-go's default (no timeout),
		// which long-running tool calls depend on.
		base := http.RoundTripper(http.DefaultTransport)
		timeout := time.Duration(0)
		if cfg.TraceEnabled {
			logger.Info("🔍 HTTP TRACE MODE ENABLED - All HTTP traffic will be logged")
			base = &http.Transport{
				MaxIdleConns:          10,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
			}
			timeout = 180 * time.Second
		} else if len(headers) == 0 {
			timeout = 180 * time.Second
		}
		opts = append(opts, transport.WithHTTPBasicClient(&http.Client{
			Transport: cfg.upstreamRoundTripper(base, logger),
			Timeout:   timeout,
		}))
	case len(headers) == 0:
		opts = append(opts, transport.WithHTTPTimeout(180*time.Second)) // Increased timeout for HTTP connections
	}

	httpTransport, err := transport.NewStreamableHTTP(cfg.URL, opts...)
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

		logger.Info("Creating OAuth SSE client with context-based timeout",
			zap.String("url", cfg.URL),
			zap.String("note", "Using 30-minute context timeout from tray"))

		// Add detailed logging about the OAuth config and token store
		logger.Info("🔍 OAuth SSE client creation details",
			zap.String("url", cfg.URL),
			zap.String("redirect_uri", cfg.OAuthConfig.RedirectURI),
			zap.Strings("scopes", cfg.OAuthConfig.Scopes),
			zap.Bool("pkce_enabled", cfg.OAuthConfig.PKCEEnabled),
			zap.String("client_id", cfg.OAuthConfig.ClientID),
			zap.Bool("has_token_store", cfg.OAuthConfig.TokenStore != nil))

		// Log if extra params wrapper is active (custom HTTP client configured)
		if cfg.OAuthConfig.HTTPClient != nil {
			logger.Debug("🔧 Using custom HTTP client with OAuth extra params wrapper",
				zap.String("note", "Extra parameters will be injected into OAuth requests"))
		}

		// As in the streamable-HTTP OAuth branch: OAuthConfig.HTTPClient covers
		// only the OAuth metadata/DCR/token calls, so the MCP requests need our
		// own client for the Retry-After recorder to see a 429 (#1040). No
		// Timeout — SSE streams are long-lived by design.
		var oauthOpts []transport.ClientOption
		if cfg.needsCustomTransport() {
			oauthOpts = append(oauthOpts, client.WithHTTPClient(&http.Client{
				Transport: cfg.upstreamRoundTripper(http.DefaultTransport, logger),
			}))
		}

		client, err := client.NewOAuthSSEClient(cfg.URL, *cfg.OAuthConfig, oauthOpts...)
		if err != nil {
			logger.Error("Failed to create OAuth SSE client", zap.Error(err))
			return nil, fmt.Errorf("failed to create OAuth SSE client: %w", err)
		}

		logger.Info("✅ OAuth-enabled SSE client created successfully")
		return client, nil
	}

	logger.Debug("Creating regular SSE client", zap.String("url", cfg.URL))

	// Apply brokered per-user auth injection (spec 074): replaces any configured
	// auth header with the resolved per-user credential and never forwards the
	// inbound gateway/IdP token (FR-017).
	headers := cfg.effectiveHeaders()

	// Create custom HTTP client for SSE - NO Timeout field to allow indefinite streaming
	// The Timeout field covers the entire request duration, which kills long-lived SSE streams
	// Instead, we rely on IdleConnTimeout to detect dead connections
	baseTransport := &http.Transport{
		MaxIdleConns:        10,
		IdleConnTimeout:     300 * time.Second, // 5 minutes idle before closing
		DisableCompression:  false,
		DisableKeepAlives:   false, // Enable keep-alives for SSE stability
		MaxIdleConnsPerHost: 5,
		// ResponseHeaderTimeout can be used to timeout initial connection, but not ongoing stream
		ResponseHeaderTimeout: 30 * time.Second,
	}

	if cfg.TraceEnabled {
		logger.Info("🔍 SSE TRACE MODE ENABLED - All HTTP traffic and SSE frames will be logged")
	}

	// Tracing and the Retry-After recorder (#1040) both wrap the base transport;
	// the SSE-specific tuning above and the deliberately absent Client.Timeout
	// are unchanged.
	httpClient := &http.Client{
		Transport: cfg.upstreamRoundTripper(baseTransport, logger),
	}

	logger.Info("Creating SSE MCP client with indefinite timeout for long-lived streams",
		zap.String("url", cfg.URL),
		zap.Duration("idle_timeout", 300*time.Second),
		zap.Duration("header_timeout", 30*time.Second),
		zap.Int("header_count", len(headers)),
		zap.String("note", "Removed http.Client.Timeout to allow SSE streams longer than 3 minutes"))

	// Enhanced trace-level debugging for SSE transport
	if logger.Core().Enabled(zap.DebugLevel - 1) { // Trace level
		logger.Debug("TRACE SSE TRANSPORT SETUP",
			zap.String("transport_type", "sse"),
			zap.String("url", cfg.URL),
			zap.Duration("idle_timeout", 300*time.Second),
			zap.Duration("response_header_timeout", 30*time.Second),
			zap.String("debug_note", "SSE client will establish persistent connection for JSON-RPC over SSE with no overall timeout"))
	}

	sseOpts := []transport.ClientOption{client.WithHTTPClient(httpClient)}
	if len(headers) > 0 {
		logger.Debug("Adding SSE headers", zap.Int("header_count", len(headers)))
		sseOpts = append(sseOpts, client.WithHeaders(headers))
	}

	sseClient, err := client.NewSSEMCPClient(cfg.URL, sseOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSE client: %w", err)
	}
	return sseClient, nil
}

// CreateHTTPTransportConfig creates an HTTP transport config from server config
func CreateHTTPTransportConfig(serverConfig *config.ServerConfig, oauthConfig *client.OAuthConfig) *HTTPTransportConfig {
	return &HTTPTransportConfig{
		URL:          serverConfig.URL,
		Headers:      serverConfig.Headers,
		OAuthConfig:  oauthConfig,
		UseOAuth:     oauthConfig != nil,
		TraceEnabled: GlobalTraceEnabled, // Use global trace flag
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
