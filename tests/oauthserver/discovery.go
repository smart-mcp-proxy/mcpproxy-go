package oauthserver

import (
	"encoding/json"
	"net/http"
)

// buildDiscoveryMetadata constructs the OAuth 2.0 Authorization Server Metadata.
func (s *OAuthTestServer) buildDiscoveryMetadata() *DiscoveryMetadata {
	metadata := &DiscoveryMetadata{
		Issuer:                s.issuerURL,
		AuthorizationEndpoint: s.issuerURL + "/authorize",
		TokenEndpoint:         s.issuerURL + "/token",
		JWKSURI:               s.issuerURL + "/jwks.json",
		ScopesSupported:       s.options.SupportedScopes,
		ResponseTypesSupported: []string{"code"},
		CodeChallengeMethodsSupported: []string{"S256"},
		TokenEndpointAuthMethodsSupported: []string{
			"client_secret_basic",
			"client_secret_post",
			"none",
		},
	}

	// Build grant types based on enabled features
	grantTypes := []string{}
	if s.options.EnableAuthCode {
		grantTypes = append(grantTypes, "authorization_code")
	}
	if s.options.EnableRefreshToken {
		grantTypes = append(grantTypes, "refresh_token")
	}
	if s.options.EnableClientCredentials {
		grantTypes = append(grantTypes, "client_credentials")
	}
	if s.options.EnableDeviceCode {
		grantTypes = append(grantTypes, "urn:ietf:params:oauth:grant-type:device_code")
	}
	metadata.GrantTypesSupported = grantTypes

	// Optional endpoints
	if s.options.EnableDCR {
		metadata.RegistrationEndpoint = s.issuerURL + "/registration"
	}
	if s.options.EnableDeviceCode {
		metadata.DeviceAuthorizationEndpoint = s.issuerURL + "/device_authorization"
	}

	return metadata
}

// handleDiscovery handles GET /.well-known/oauth-authorization-server
// and GET /.well-known/openid-configuration requests.
func (s *OAuthTestServer) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	metadata := s.buildDiscoveryMetadata()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if err := json.NewEncoder(w).Encode(metadata); err != nil {
		http.Error(w, "Failed to encode metadata", http.StatusInternalServerError)
		return
	}
}
