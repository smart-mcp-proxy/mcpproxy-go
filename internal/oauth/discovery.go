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

// DiscoverScopesFromProtectedResource attempts to discover scopes from Protected Resource Metadata (RFC 9728)
func DiscoverScopesFromProtectedResource(metadataURL string, timeout time.Duration) ([]string, error) {
	logger := zap.L().Named("oauth.discovery")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	// TRACE: Log HTTP request details
	logger.Debug("🌐 HTTP Request - Protected Resource Metadata (RFC 9728)",
		zap.String("method", req.Method),
		zap.String("url", metadataURL),
		zap.Any("headers", req.Header),
		zap.Duration("timeout", timeout))

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

	// TRACE: Log HTTP response details
	logger.Debug("📥 HTTP Response - Protected Resource Metadata",
		zap.String("url", metadataURL),
		zap.Int("status_code", resp.StatusCode),
		zap.String("status", resp.Status),
		zap.Any("headers", resp.Header),
		zap.Duration("elapsed", elapsed))

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

	if len(metadata.ScopesSupported) == 0 {
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

	// TRACE: Log HTTP request details
	logger.Debug("🌐 HTTP Request - Authorization Server Metadata (RFC 8414)",
		zap.String("method", req.Method),
		zap.String("url", metadataURL),
		zap.String("base_url", baseURL),
		zap.Any("headers", req.Header),
		zap.Duration("timeout", timeout))

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

	// TRACE: Log HTTP response details
	logger.Debug("📥 HTTP Response - Authorization Server Metadata",
		zap.String("url", metadataURL),
		zap.Int("status_code", resp.StatusCode),
		zap.String("status", resp.Status),
		zap.Any("headers", resp.Header),
		zap.Duration("elapsed", elapsed))

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
