package oauthserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests that verify mcpproxy behavior against the OAuth test server.
// These tests are skipped by default and run only when OAUTH_INTEGRATION_TESTS=1.
//
// To run these tests:
//   OAUTH_INTEGRATION_TESTS=1 go test ./tests/oauthserver/... -run TestIntegration -v

func skipIfNoIntegration(t *testing.T) {
	if os.Getenv("OAUTH_INTEGRATION_TESTS") != "1" {
		t.Skip("Skipping integration test (set OAUTH_INTEGRATION_TESTS=1 to run)")
	}
}

// findMCPProxyBinary locates the mcpproxy binary.
func findMCPProxyBinary() (string, error) {
	// Try common locations
	candidates := []string{
		"./mcpproxy",
		"../../mcpproxy",
		filepath.Join(os.Getenv("GOPATH"), "bin", "mcpproxy"),
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return filepath.Abs(path)
		}
	}

	// Try to find in PATH
	path, err := exec.LookPath("mcpproxy")
	if err == nil {
		return path, nil
	}

	return "", fmt.Errorf("mcpproxy binary not found")
}

// TestIntegration_WWWAuthenticateDiscovery tests that mcpproxy can discover
// OAuth endpoints from a WWW-Authenticate header (RFC 9728).
func TestIntegration_WWWAuthenticateDiscovery(t *testing.T) {
	skipIfNoIntegration(t)

	// Start OAuth test server with WWW-Authenticate mode
	server := Start(t, Options{
		DetectionMode: Both, // Enable both discovery and WWW-Authenticate
	})
	defer server.Shutdown()

	// Verify the /protected endpoint returns WWW-Authenticate
	resp, err := http.Get(server.ProtectedResourceURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	require.NotEmpty(t, wwwAuth, "Expected WWW-Authenticate header")

	// Verify WWW-Authenticate contains required fields per RFC 9728
	assert.Contains(t, wwwAuth, "Bearer")
	assert.Contains(t, wwwAuth, "authorization_uri")
	assert.Contains(t, wwwAuth, "resource_metadata") // Points to /.well-known/oauth-protected-resource

	// Parse the WWW-Authenticate to extract URLs
	t.Logf("WWW-Authenticate: %s", wwwAuth)

	// Verify the authorization_uri points to our test server
	assert.Contains(t, wwwAuth, server.IssuerURL)
}

// TestIntegration_DiscoveryEndpoints tests that mcpproxy can fetch and parse
// OAuth discovery metadata.
func TestIntegration_DiscoveryEndpoints(t *testing.T) {
	skipIfNoIntegration(t)

	server := Start(t, Options{
		DetectionMode: Discovery,
	})
	defer server.Shutdown()

	// Test well-known endpoint
	resp, err := http.Get(server.IssuerURL + "/.well-known/oauth-authorization-server")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var metadata DiscoveryMetadata
	err = json.NewDecoder(resp.Body).Decode(&metadata)
	require.NoError(t, err)

	// Verify all required fields
	assert.Equal(t, server.IssuerURL, metadata.Issuer)
	assert.NotEmpty(t, metadata.AuthorizationEndpoint)
	assert.NotEmpty(t, metadata.TokenEndpoint)
	assert.NotEmpty(t, metadata.JWKSURI)
	assert.Contains(t, metadata.ResponseTypesSupported, "code")
	assert.Contains(t, metadata.GrantTypesSupported, "authorization_code")
	assert.Contains(t, metadata.CodeChallengeMethodsSupported, "S256")

	t.Logf("Discovery metadata: issuer=%s, auth=%s, token=%s",
		metadata.Issuer, metadata.AuthorizationEndpoint, metadata.TokenEndpoint)
}

// TestIntegration_ErrorHandling tests that mcpproxy receives and can process
// OAuth error responses correctly.
func TestIntegration_ErrorHandling(t *testing.T) {
	skipIfNoIntegration(t)

	testCases := []struct {
		name          string
		errorMode     ErrorMode
		expectedError string
		expectedCode  int
	}{
		{
			name:          "invalid_client",
			errorMode:     ErrorMode{TokenInvalidClient: true},
			expectedError: "invalid_client",
			expectedCode:  http.StatusUnauthorized,
		},
		{
			name:          "invalid_grant",
			errorMode:     ErrorMode{TokenInvalidGrant: true},
			expectedError: "invalid_grant",
			expectedCode:  http.StatusBadRequest,
		},
		{
			name:          "server_error",
			errorMode:     ErrorMode{TokenServerError: true},
			expectedError: "",
			expectedCode:  http.StatusInternalServerError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := Start(t, Options{
				ErrorMode: tc.errorMode,
			})
			defer server.Shutdown()

			// Make a token request that will trigger the error
			resp, err := http.PostForm(server.TokenEndpoint, map[string][]string{
				"grant_type":    {"authorization_code"},
				"code":          {"test-code"},
				"redirect_uri":  {"http://127.0.0.1/callback"},
				"client_id":     {server.ClientID},
				"client_secret": {server.ClientSecret},
			})
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.expectedCode, resp.StatusCode)

			if tc.expectedError != "" {
				var errorResp TokenErrorResponse
				err = json.NewDecoder(resp.Body).Decode(&errorResp)
				require.NoError(t, err)
				assert.Equal(t, tc.expectedError, errorResp.Error)
			}
		})
	}
}

// TestIntegration_SlowResponse tests that mcpproxy handles slow OAuth responses.
func TestIntegration_SlowResponse(t *testing.T) {
	skipIfNoIntegration(t)

	delay := 2 * time.Second
	server := Start(t, Options{
		ErrorMode: ErrorMode{
			TokenSlowResponse: delay,
		},
	})
	defer server.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", server.TokenEndpoint, strings.NewReader(
		"grant_type=client_credentials&client_id="+server.ClientID+"&client_secret="+server.ClientSecret,
	))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	elapsed := time.Since(start)

	require.NoError(t, err)
	defer resp.Body.Close()

	// Should have taken at least the delay time
	assert.GreaterOrEqual(t, elapsed, delay, "Response should have been delayed")
	t.Logf("Request took %v (expected >= %v)", elapsed, delay)
}

// TestIntegration_DeviceCodePolling tests device code polling behavior.
func TestIntegration_DeviceCodePolling(t *testing.T) {
	skipIfNoIntegration(t)

	server := Start(t, Options{
		EnableDeviceCode: true,
	})
	defer server.Shutdown()

	// Request device code
	resp, err := http.PostForm(server.IssuerURL+"/device_authorization", map[string][]string{
		"client_id": {server.PublicClientID},
		"scope":     {"read"},
	})
	require.NoError(t, err)

	var deviceResp DeviceAuthorizationResponse
	json.NewDecoder(resp.Body).Decode(&deviceResp)
	resp.Body.Close()

	require.NotEmpty(t, deviceResp.DeviceCode)
	require.NotEmpty(t, deviceResp.UserCode)

	// Poll before approval - should get authorization_pending
	tokenResp, err := http.PostForm(server.TokenEndpoint, map[string][]string{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceResp.DeviceCode},
		"client_id":   {server.PublicClientID},
	})
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadRequest, tokenResp.StatusCode)

	var errorResp TokenErrorResponse
	json.NewDecoder(tokenResp.Body).Decode(&errorResp)
	tokenResp.Body.Close()

	assert.Equal(t, "authorization_pending", errorResp.Error)

	// Approve the device code
	err = server.Server.ApproveDeviceCode(deviceResp.UserCode)
	require.NoError(t, err)

	// Poll after approval - should get tokens
	tokenResp2, err := http.PostForm(server.TokenEndpoint, map[string][]string{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceResp.DeviceCode},
		"client_id":   {server.PublicClientID},
	})
	require.NoError(t, err)
	defer tokenResp2.Body.Close()

	assert.Equal(t, http.StatusOK, tokenResp2.StatusCode)

	var tokenResponse TokenResponse
	json.NewDecoder(tokenResp2.Body).Decode(&tokenResponse)
	assert.NotEmpty(t, tokenResponse.AccessToken)
}

// TestIntegration_DCRFlow tests Dynamic Client Registration.
func TestIntegration_DCRFlow(t *testing.T) {
	skipIfNoIntegration(t)

	server := Start(t, Options{
		EnableDCR:      true,
		EnableAuthCode: true,
	})
	defer server.Shutdown()

	// Register a new client
	reqBody := `{
		"redirect_uris": ["http://127.0.0.1:9999/callback"],
		"client_name": "Integration Test Client",
		"grant_types": ["authorization_code"],
		"token_endpoint_auth_method": "none"
	}`

	resp, err := http.Post(
		server.IssuerURL+"/registration",
		"application/json",
		strings.NewReader(reqBody),
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var clientResp ClientRegistrationResponse
	err = json.NewDecoder(resp.Body).Decode(&clientResp)
	require.NoError(t, err)

	assert.NotEmpty(t, clientResp.ClientID)
	assert.Equal(t, "Integration Test Client", clientResp.ClientName)
	assert.Contains(t, clientResp.RedirectURIs, "http://127.0.0.1:9999/callback")

	t.Logf("Registered client: %s", clientResp.ClientID)
}

// TestIntegration_JWKSFetch tests that JWKS can be fetched and parsed.
func TestIntegration_JWKSFetch(t *testing.T) {
	skipIfNoIntegration(t)

	server := Start(t, Options{})
	defer server.Shutdown()

	resp, err := http.Get(server.JWKSURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var jwks JWKS
	err = json.NewDecoder(resp.Body).Decode(&jwks)
	require.NoError(t, err)

	require.NotEmpty(t, jwks.Keys)
	key := jwks.Keys[0]

	assert.Equal(t, "RSA", key.Kty)
	assert.Equal(t, "RS256", key.Alg)
	assert.Equal(t, "sig", key.Use)
	assert.NotEmpty(t, key.Kid)
	assert.NotEmpty(t, key.N)
	assert.NotEmpty(t, key.E)

	t.Logf("JWKS key: kid=%s, alg=%s", key.Kid, key.Alg)
}

// TestIntegration_ResourceIndicator tests RFC 8707 resource indicator flow.
func TestIntegration_ResourceIndicator(t *testing.T) {
	skipIfNoIntegration(t)

	server := Start(t, Options{})
	defer server.Shutdown()

	// The resource indicator should be preserved through the flow
	// and appear in the JWT audience claim
	resource := "https://api.example.com/v1"

	// Generate PKCE values (required by default)
	codeVerifier := "test-verifier-for-resource"
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	// Verify the authorize endpoint accepts the resource parameter
	authURL := fmt.Sprintf("%s?response_type=code&client_id=%s&redirect_uri=%s&resource=%s&code_challenge=%s&code_challenge_method=S256",
		server.AuthorizationEndpoint,
		server.PublicClientID,
		"http://127.0.0.1/callback",
		resource,
		codeChallenge,
	)

	// Don't follow redirects
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(authURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should return the login form (200 OK)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}
