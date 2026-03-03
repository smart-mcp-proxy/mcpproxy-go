package oauth

import (
	"bytes"
	"io"
	"net/http"
	"net/url"

	"go.uber.org/zap"
)

// OAuthTransportWrapper wraps an HTTP RoundTripper to inject extra OAuth parameters
// into authorization and token requests without modifying the mcp-go library.
//
// This wrapper intercepts HTTP requests and adds custom parameters to:
// - Authorization requests (query parameters)
// - Token exchange requests (form body parameters)
// - Token refresh requests (form body parameters)
//
// The wrapper is stateless and thread-safe for concurrent use.
type OAuthTransportWrapper struct {
	// inner is the wrapped HTTP RoundTripper (typically http.DefaultTransport)
	inner http.RoundTripper

	// extraParams contains the additional OAuth parameters to inject
	// (e.g., RFC 8707 "resource" parameter for Runlayer integration)
	extraParams map[string]string

	// logger for DEBUG level logging of parameter injection
	logger *zap.Logger
}

// NewOAuthTransportWrapper creates a new transport wrapper that injects extra params.
//
// Parameters:
//   - transport: The base HTTP RoundTripper to wrap (use http.DefaultTransport if nil)
//   - extraParams: Map of extra parameters to inject into OAuth requests
//   - logger: Logger for debug output (uses zap.L() if nil)
//
// Returns a wrapper that can be used as http.Client.Transport.
func NewOAuthTransportWrapper(transport http.RoundTripper, extraParams map[string]string, logger *zap.Logger) *OAuthTransportWrapper {
	if transport == nil {
		transport = http.DefaultTransport
	}
	if logger == nil {
		logger = zap.L().Named("oauth-wrapper")
	}

	// Make a copy to avoid external modifications
	params := make(map[string]string, len(extraParams))
	for k, v := range extraParams {
		params[k] = v
	}

	return &OAuthTransportWrapper{
		inner:       transport,
		extraParams: params,
		logger:      logger,
	}
}

// RoundTrip implements http.RoundTripper by intercepting requests and injecting extra params.
//
// This method:
// 1. Detects OAuth authorization and token requests by URL path
// 2. Clones the request to avoid modifying the original
// 3. Injects extra parameters into query string (authorization) or body (token)
// 4. Delegates to the wrapped transport for actual HTTP execution
// 5. Normalizes HTTP 201 responses to 200 for token requests (some providers like Supabase return 201)
// 6. Logs parameter injection at DEBUG level for observability
func (w *OAuthTransportWrapper) RoundTrip(req *http.Request) (*http.Response, error) {
	tokenReq := isTokenRequest(req)

	if len(w.extraParams) > 0 {
		// Clone request to avoid modifying original
		clonedReq := req.Clone(req.Context())

		// Detect OAuth endpoint type and inject params appropriately
		if isAuthorizationRequest(req) {
			w.injectQueryParams(clonedReq)
		} else if tokenReq {
			w.injectFormParams(clonedReq)
		}

		resp, err := w.inner.RoundTrip(clonedReq)
		if err != nil {
			return resp, err
		}

		// Normalize 201 Created to 200 OK for token responses.
		// Some OAuth providers (e.g., Supabase) return 201 for token exchange,
		// but mcp-go only accepts 200.
		if tokenReq && resp.StatusCode == http.StatusCreated {
			w.logger.Debug("Normalized token response status 201→200",
				zap.String("url", req.URL.String()))
			resp.StatusCode = http.StatusOK
			resp.Status = "200 OK"
		}

		return resp, nil
	}

	resp, err := w.inner.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	// Normalize 201 Created to 200 OK for token responses even without extra params.
	if tokenReq && resp.StatusCode == http.StatusCreated {
		w.logger.Debug("Normalized token response status 201→200",
			zap.String("url", req.URL.String()))
		resp.StatusCode = http.StatusOK
		resp.Status = "200 OK"
	}

	return resp, nil
}

// isAuthorizationRequest detects if this is an OAuth authorization request
// by checking for common authorization endpoint patterns.
func isAuthorizationRequest(req *http.Request) bool {
	path := req.URL.Path
	// Common OAuth authorization endpoint paths
	return contains(path, "/authorize") || contains(path, "/oauth/authorize")
}

// isTokenRequest detects if this is an OAuth token request (exchange or refresh)
// by checking for token endpoint patterns and POST method.
func isTokenRequest(req *http.Request) bool {
	if req.Method != http.MethodPost {
		return false
	}
	path := req.URL.Path
	// Common OAuth token endpoint paths
	return contains(path, "/token") || contains(path, "/oauth/token")
}

// injectQueryParams adds extra parameters to the authorization URL query string.
//
// This is used for OAuth authorization requests where params are sent as
// URL query parameters (e.g., /authorize?response_type=code&resource=...).
func (w *OAuthTransportWrapper) injectQueryParams(req *http.Request) {
	q := req.URL.Query()

	for k, v := range w.extraParams {
		q.Set(k, v)
	}

	req.URL.RawQuery = q.Encode()

	// Log at DEBUG level with selective masking
	masked := maskExtraParams(w.extraParams)
	w.logger.Debug("Injected extra params into authorization URL",
		zap.String("url", req.URL.String()),
		zap.Any("extra_params", masked))
}

// injectFormParams adds extra parameters to token request form body.
//
// This is used for OAuth token exchange and refresh requests where params
// are sent as application/x-www-form-urlencoded body parameters.
func (w *OAuthTransportWrapper) injectFormParams(req *http.Request) {
	// Read existing body
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		w.logger.Warn("Failed to read token request body for extra params injection",
			zap.Error(err))
		return
	}
	req.Body.Close()

	// Parse form values
	values, err := url.ParseQuery(string(bodyBytes))
	if err != nil {
		w.logger.Warn("Failed to parse token request form body",
			zap.Error(err))
		// Restore original body
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		return
	}

	// Add extra params
	for k, v := range w.extraParams {
		values.Set(k, v)
	}

	// Encode modified form and update request
	newBody := values.Encode()
	req.Body = io.NopCloser(bytes.NewBufferString(newBody))
	req.ContentLength = int64(len(newBody))

	// Ensure correct content type
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	// Log at DEBUG level with selective masking
	masked := maskExtraParams(w.extraParams)
	w.logger.Debug("Injected extra params into token request body",
		zap.String("url", req.URL.String()),
		zap.Any("extra_params", masked))
}

// contains is a helper to check if a string contains a substring (case-sensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(bytes.Contains([]byte(s), []byte(substr))))
}
