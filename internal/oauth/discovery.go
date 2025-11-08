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

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metadata endpoint returned %d", resp.StatusCode)
	}

	var metadata ProtectedResourceMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

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

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metadata endpoint returned %d", resp.StatusCode)
	}

	var metadata OAuthServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}

	if len(metadata.ScopesSupported) == 0 {
		logger.Debug("Authorization Server Metadata returned empty scopes_supported",
			zap.String("metadata_url", metadataURL))
		return []string{}, nil
	}

	return metadata.ScopesSupported, nil
}
