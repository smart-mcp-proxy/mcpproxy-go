package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ProtectedResourceMetadata represents RFC 9728 Protected Resource Metadata
type ProtectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	ResourceName           string   `json:"resource_name,omitempty"`
	AuthorizationServers   []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported,omitempty"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
}

// OAuthServerMetadata represents RFC 8414 OAuth Authorization Server Metadata
type OAuthServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	RevocationEndpoint                string   `json:"revocation_endpoint,omitempty"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
}

// ExtractResourceMetadataURL parses WWW-Authenticate header to extract resource_metadata URL
// Format: Bearer error="invalid_request", resource_metadata="https://..."
func ExtractResourceMetadataURL(wwwAuthHeader string) string {
	// Look for resource_metadata parameter
	if !strings.Contains(wwwAuthHeader, "resource_metadata") {
		return ""
	}

	// Split on resource_metadata=" to find the URL
	parts := strings.Split(wwwAuthHeader, "resource_metadata=\"")
	if len(parts) < 2 {
		return ""
	}

	// Find the closing quote
	endIdx := strings.Index(parts[1], "\"")
	if endIdx == -1 {
		return ""
	}

	return parts[1][:endIdx]
}

// DiscoverProtectedResourceMetadata fetches RFC 9728 Protected Resource Metadata
// and returns the full metadata structure including the resource parameter.
// This is the primary function for RFC 8707 resource auto-detection.
func DiscoverProtectedResourceMetadata(metadataURL string, timeout time.Duration) (*ProtectedResourceMetadata, error) {
	logger := zap.L().Named("oauth.discovery")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	// TRACE: Log HTTP request details (using standard logging function for consistent redaction)
	LogOAuthRequest(logger, req.Method, metadataURL, req.Header)

	startTime := time.Now()
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	elapsed := time.Since(startTime)

	if err != nil {
		logger.Debug("❌ HTTP Request failed",
			zap.String("url", metadataURL),
			zap.Error(err),
			zap.Duration("elapsed", elapsed))
		return nil, fmt.Errorf("failed to fetch metadata: %w", err)
	}
	defer resp.Body.Close()

	// TRACE: Log HTTP response details (using standard logging function for consistent redaction)
	LogOAuthResponse(logger, resp.StatusCode, resp.Header, elapsed)

	if resp.StatusCode != http.StatusOK {
		logger.Debug("⚠️ Non-200 status code from metadata endpoint",
			zap.String("url", metadataURL),
			zap.Int("status_code", resp.StatusCode))
		return nil, fmt.Errorf("metadata endpoint returned %d", resp.StatusCode)
	}

	var metadata ProtectedResourceMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		logger.Debug("❌ Failed to parse JSON response",
			zap.String("url", metadataURL),
			zap.Error(err))
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	// TRACE: Log parsed metadata
	logger.Debug("✅ Successfully parsed Protected Resource Metadata",
		zap.String("url", metadataURL),
		zap.String("resource", metadata.Resource),
		zap.String("resource_name", metadata.ResourceName),
		zap.Strings("scopes_supported", metadata.ScopesSupported),
		zap.Strings("authorization_servers", metadata.AuthorizationServers),
		zap.Strings("bearer_methods_supported", metadata.BearerMethodsSupported))

	// Log resource discovery for RFC 8707 auto-detection
	if metadata.Resource != "" {
		logger.Info("Protected Resource Metadata discovered",
			zap.String("resource", metadata.Resource),
			zap.Strings("scopes", metadata.ScopesSupported),
			zap.Strings("auth_servers", metadata.AuthorizationServers))
	}

	return &metadata, nil
}

// DiscoverScopesFromProtectedResource attempts to discover scopes from Protected Resource Metadata (RFC 9728)
// This is a convenience wrapper around DiscoverProtectedResourceMetadata for backward compatibility.
func DiscoverScopesFromProtectedResource(metadataURL string, timeout time.Duration) ([]string, error) {
	metadata, err := DiscoverProtectedResourceMetadata(metadataURL, timeout)
	if err != nil {
		return nil, err
	}

	if len(metadata.ScopesSupported) == 0 {
		logger := zap.L().Named("oauth.discovery")
		logger.Debug("Protected Resource Metadata returned empty scopes_supported",
			zap.String("metadata_url", metadataURL))
		return []string{}, nil
	}

	return metadata.ScopesSupported, nil
}

// DiscoverScopesFromAuthorizationServer attempts to discover scopes from OAuth Server Metadata (RFC 8414)
func DiscoverScopesFromAuthorizationServer(baseURL string, timeout time.Duration) ([]string, error) {
	logger := zap.L().Named("oauth.discovery")

	// Construct the well-known metadata URL
	metadataURL := baseURL + "/.well-known/oauth-authorization-server"

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	// TRACE: Log HTTP request details (using standard logging function for consistent redaction)
	LogOAuthRequest(logger, req.Method, metadataURL, req.Header)

	startTime := time.Now()
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	elapsed := time.Since(startTime)

	if err != nil {
		logger.Debug("❌ HTTP Request failed",
			zap.String("url", metadataURL),
			zap.Error(err),
			zap.Duration("elapsed", elapsed))
		return nil, fmt.Errorf("failed to fetch metadata: %w", err)
	}
	defer resp.Body.Close()

	// TRACE: Log HTTP response details (using standard logging function for consistent redaction)
	LogOAuthResponse(logger, resp.StatusCode, resp.Header, elapsed)

	if resp.StatusCode != http.StatusOK {
		logger.Debug("⚠️ Non-200 status code from metadata endpoint",
			zap.String("url", metadataURL),
			zap.Int("status_code", resp.StatusCode))
		return nil, fmt.Errorf("metadata endpoint returned %d", resp.StatusCode)
	}

	var metadata OAuthServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		logger.Debug("❌ Failed to parse JSON response",
			zap.String("url", metadataURL),
			zap.Error(err))
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	// TRACE: Log parsed metadata
	logger.Debug("✅ Successfully parsed Authorization Server Metadata",
		zap.String("url", metadataURL),
		zap.String("issuer", metadata.Issuer),
		zap.String("authorization_endpoint", metadata.AuthorizationEndpoint),
		zap.String("token_endpoint", metadata.TokenEndpoint),
		zap.Strings("scopes_supported", metadata.ScopesSupported),
		zap.Strings("response_types_supported", metadata.ResponseTypesSupported),
		zap.Strings("grant_types_supported", metadata.GrantTypesSupported))

	logger.Debug("Authorization Server Metadata fetched",
		zap.String("issuer", metadata.Issuer),
		zap.String("authorization_endpoint", metadata.AuthorizationEndpoint),
		zap.String("token_endpoint", metadata.TokenEndpoint),
		zap.String("registration_endpoint", metadata.RegistrationEndpoint),
		zap.Strings("scopes_supported", metadata.ScopesSupported))

	if metadata.RegistrationEndpoint == "" {
		logger.Warn("Authorization server metadata missing registration_endpoint; clients that require DCR may keep the Login button disabled",
			zap.String("issuer", metadata.Issuer),
			zap.String("hint", "Provide oauth.client_id in config or use a proxy that emulates /register"))
	}

	if len(metadata.ScopesSupported) == 0 {
		logger.Debug("Authorization Server Metadata returned empty scopes_supported",
			zap.String("metadata_url", metadataURL))
		return []string{}, nil
	}

	return metadata.ScopesSupported, nil
}

// DetectOAuthAvailability checks if a server supports OAuth by probing the well-known endpoint
// Returns true if OAuth metadata is discoverable, false otherwise
func DetectOAuthAvailability(baseURL string, timeout time.Duration) bool {
	logger := zap.L().Named("oauth.detection")

	metadataURL := baseURL + "/.well-known/oauth-authorization-server"

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", metadataURL, nil)
	if err != nil {
		return false
	}

	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		logger.Debug("OAuth detection failed - endpoint unreachable",
			zap.String("url", metadataURL),
			zap.Error(err))
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Debug("OAuth detection failed - non-200 status",
			zap.String("url", metadataURL),
			zap.Int("status_code", resp.StatusCode))
		return false
	}

	var metadata OAuthServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		logger.Debug("OAuth detection failed - invalid JSON",
			zap.String("url", metadataURL),
			zap.Error(err))
		return false
	}

	// Verify it's valid OAuth metadata
	if metadata.AuthorizationEndpoint == "" || metadata.TokenEndpoint == "" {
		logger.Debug("OAuth detection failed - incomplete metadata",
			zap.String("url", metadataURL),
			zap.String("authorization_endpoint", metadata.AuthorizationEndpoint),
			zap.String("token_endpoint", metadata.TokenEndpoint))
		return false
	}

	logger.Info("✅ OAuth detected automatically",
		zap.String("server_url", baseURL),
		zap.String("issuer", metadata.Issuer),
		zap.String("authorization_endpoint", metadata.AuthorizationEndpoint),
		zap.String("token_endpoint", metadata.TokenEndpoint))

	return true
}

// OAuthMetadataValidationResult contains the result of OAuth metadata validation
type OAuthMetadataValidationResult struct {
	Valid                         bool
	ProtectedResourceMetadata     *ProtectedResourceMetadata
	AuthorizationServerMetadata   *OAuthServerMetadata
	ProtectedResourceMetadataURL  string
	AuthorizationServerMetadataURL string
	ProtectedResourceError        error
	AuthorizationServerError      error
}

// ValidateOAuthMetadata performs pre-flight validation of OAuth metadata.
// It fetches and validates both protected resource metadata (RFC 9728) and
// authorization server metadata (RFC 8414) before starting the OAuth flow.
// This enables early failure with clear, actionable error messages.
//
// Parameters:
//   - serverURL: The MCP server URL to validate OAuth for
//   - serverName: The server name for error messages
//   - timeout: Timeout for HTTP requests
//
// Returns:
//   - *OAuthMetadataValidationResult: Validation result with metadata if successful
//   - error: Structured OAuthFlowError if validation fails
func ValidateOAuthMetadata(serverURL, serverName string, timeout time.Duration) (*OAuthMetadataValidationResult, error) {
	logger := zap.L().Named("oauth.validation")
	result := &OAuthMetadataValidationResult{}

	logger.Info("🔍 Starting OAuth metadata pre-flight validation",
		zap.String("server", serverName),
		zap.String("url", serverURL))

	// Step 1: Make preflight request to get WWW-Authenticate header
	client := &http.Client{Timeout: timeout}
	resp, err := client.Post(serverURL, "application/json", strings.NewReader("{}"))
	if err != nil {
		logger.Debug("Preflight request failed",
			zap.String("server", serverName),
			zap.Error(err))
		// This is a connection error, not a metadata error - return nil to let OAuth flow proceed
		// The actual OAuth flow will handle connection errors appropriately
		return nil, nil
	}
	defer resp.Body.Close()

	// If not 401, OAuth might not be required or server uses different auth
	if resp.StatusCode != http.StatusUnauthorized {
		logger.Debug("Server did not return 401, skipping metadata validation",
			zap.String("server", serverName),
			zap.Int("status_code", resp.StatusCode))
		return nil, nil
	}

	// Step 2: Extract protected resource metadata URL from WWW-Authenticate
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	metadataURL := ExtractResourceMetadataURL(wwwAuth)

	if metadataURL == "" {
		// No resource_metadata in WWW-Authenticate - this is OK, some servers don't use RFC 9728
		logger.Debug("WWW-Authenticate header lacks resource_metadata",
			zap.String("server", serverName))

		// Try direct authorization server metadata discovery
		baseURL, err := parseBaseURL(serverURL)
		if err != nil {
			return nil, nil // Can't validate, let OAuth flow handle it
		}

		authServerURL := baseURL + "/.well-known/oauth-authorization-server"
		result.AuthorizationServerMetadataURL = authServerURL

		authMetadata, err := fetchAuthorizationServerMetadata(authServerURL, timeout)
		if err != nil {
			result.AuthorizationServerError = err
			// Return structured error
			return result, createMetadataError(serverName, serverURL, result)
		}

		result.AuthorizationServerMetadata = authMetadata
		result.Valid = true
		return result, nil
	}

	result.ProtectedResourceMetadataURL = metadataURL

	// Step 3: Fetch protected resource metadata
	protectedMetadata, err := DiscoverProtectedResourceMetadata(metadataURL, timeout)
	if err != nil {
		result.ProtectedResourceError = err
		logger.Debug("Failed to fetch protected resource metadata",
			zap.String("server", serverName),
			zap.String("metadata_url", metadataURL),
			zap.Error(err))
		// Don't return error yet - try to get auth server metadata for better error info
	} else {
		result.ProtectedResourceMetadata = protectedMetadata
	}

	// Step 4: Extract authorization server URL and fetch its metadata
	var authServerBaseURL string
	if protectedMetadata != nil && len(protectedMetadata.AuthorizationServers) > 0 {
		authServerBaseURL = protectedMetadata.AuthorizationServers[0]
	} else {
		// Fallback to base URL of server
		baseURL, err := parseBaseURL(serverURL)
		if err != nil {
			if result.ProtectedResourceError != nil {
				return result, createMetadataError(serverName, serverURL, result)
			}
			return nil, nil
		}
		authServerBaseURL = baseURL
	}

	// Trim trailing slash to avoid double-slash in URL (e.g., "https://example.com//.well-known/...")
	authServerBaseURL = strings.TrimSuffix(authServerBaseURL, "/")
	authServerMetadataURL := authServerBaseURL + "/.well-known/oauth-authorization-server"
	result.AuthorizationServerMetadataURL = authServerMetadataURL

	authMetadata, err := fetchAuthorizationServerMetadata(authServerMetadataURL, timeout)
	if err != nil {
		result.AuthorizationServerError = err
		logger.Debug("Failed to fetch authorization server metadata",
			zap.String("server", serverName),
			zap.String("metadata_url", authServerMetadataURL),
			zap.Error(err))
		return result, createMetadataError(serverName, serverURL, result)
	}

	result.AuthorizationServerMetadata = authMetadata

	// Step 5: Validate required fields
	if authMetadata.AuthorizationEndpoint == "" || authMetadata.TokenEndpoint == "" {
		result.AuthorizationServerError = fmt.Errorf("metadata missing required fields: authorization_endpoint=%q, token_endpoint=%q",
			authMetadata.AuthorizationEndpoint, authMetadata.TokenEndpoint)
		return result, createMetadataError(serverName, serverURL, result)
	}

	result.Valid = true
	logger.Info("✅ OAuth metadata validation successful",
		zap.String("server", serverName),
		zap.String("authorization_endpoint", authMetadata.AuthorizationEndpoint),
		zap.String("token_endpoint", authMetadata.TokenEndpoint))

	return result, nil
}

// fetchAuthorizationServerMetadata fetches RFC 8414 OAuth Authorization Server Metadata
func fetchAuthorizationServerMetadata(metadataURL string, timeout time.Duration) (*OAuthServerMetadata, error) {
	logger := zap.L().Named("oauth.validation")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	var metadata OAuthServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		logger.Debug("Failed to parse authorization server metadata",
			zap.String("url", metadataURL),
			zap.Error(err))
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	return &metadata, nil
}

// createMetadataError creates a structured OAuthFlowError from validation result
func createMetadataError(serverName, serverURL string, result *OAuthMetadataValidationResult) error {
	// Import contracts for error types - using string literals to avoid import cycle
	// These match the constants in internal/contracts/types.go
	const (
		errorTypeMetadataMissing = "oauth_metadata_missing"
		errorTypeMetadataInvalid = "oauth_metadata_invalid"
		errorCodeNoMetadata      = "OAUTH_NO_METADATA"
		errorCodeBadMetadata     = "OAUTH_BAD_METADATA"
	)

	errorType := errorTypeMetadataMissing
	errorCode := errorCodeNoMetadata
	message := "OAuth authorization server metadata not available"
	suggestion := "The OAuth authorization server is not properly configured. Contact the server administrator."

	// Check if metadata was found but invalid
	if result.AuthorizationServerError != nil && strings.Contains(result.AuthorizationServerError.Error(), "missing required fields") {
		errorType = errorTypeMetadataInvalid
		errorCode = errorCodeBadMetadata
		message = "OAuth authorization server metadata is incomplete"
		suggestion = "The OAuth server metadata is missing required fields. Contact the server administrator."
	}

	// Build details structure
	details := &metadataErrorDetails{
		ServerURL: serverURL,
	}

	if result.ProtectedResourceMetadataURL != "" {
		details.ProtectedResourceMetadata = &metadataStatus{
			Found:      result.ProtectedResourceMetadata != nil,
			URLChecked: result.ProtectedResourceMetadataURL,
		}
		if result.ProtectedResourceError != nil {
			details.ProtectedResourceMetadata.Error = result.ProtectedResourceError.Error()
		}
		if result.ProtectedResourceMetadata != nil {
			details.ProtectedResourceMetadata.AuthorizationServers = result.ProtectedResourceMetadata.AuthorizationServers
		}
	}

	if result.AuthorizationServerMetadataURL != "" {
		details.AuthorizationServerMetadata = &metadataStatus{
			Found:      result.AuthorizationServerMetadata != nil,
			URLChecked: result.AuthorizationServerMetadataURL,
		}
		if result.AuthorizationServerError != nil {
			details.AuthorizationServerMetadata.Error = result.AuthorizationServerError.Error()
		}
	}

	return &OAuthMetadataError{
		ErrorType:  errorType,
		ErrorCode:  errorCode,
		ServerName: serverName,
		Message:    message,
		Details:    details,
		Suggestion: suggestion,
	}
}

// OAuthMetadataError represents a metadata validation error (internal type)
// This is converted to contracts.OAuthFlowError by the caller
type OAuthMetadataError struct {
	ErrorType  string
	ErrorCode  string
	ServerName string
	Message    string
	Details    *metadataErrorDetails
	Suggestion string
}

func (e *OAuthMetadataError) Error() string {
	return e.Message
}

// metadataErrorDetails contains structured details about metadata validation failure
type metadataErrorDetails struct {
	ServerURL                   string          `json:"server_url"`
	ProtectedResourceMetadata   *metadataStatus `json:"protected_resource_metadata,omitempty"`
	AuthorizationServerMetadata *metadataStatus `json:"authorization_server_metadata,omitempty"`
}

// metadataStatus represents the status of OAuth metadata discovery
type metadataStatus struct {
	Found                bool     `json:"found"`
	URLChecked           string   `json:"url_checked"`
	Error                string   `json:"error,omitempty"`
	AuthorizationServers []string `json:"authorization_servers,omitempty"`
}

// Note: parseBaseURL is defined in config.go and shared within the oauth package
