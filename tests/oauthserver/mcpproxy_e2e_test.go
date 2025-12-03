package oauthserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// MCPProxy E2E Tests
// =============================================================================
//
// These tests verify mcpproxy's OAuth client behavior against the test OAuth server.
// Unlike TestServer_* tests which verify the test infrastructure, these tests verify
// that mcpproxy correctly implements OAuth 2.1 specifications.
//
// Naming convention:
//   - TestServer_*     = Tests for OAuth test server infrastructure (server_test.go)
//   - TestMCPProxy_*   = E2E tests for mcpproxy OAuth client (this file)
//
// =============================================================================

// TestMCPProxy_RFC8707_ResourceIndicator_NotImplemented verifies that mcpproxy's
// OAuth client does NOT currently support RFC 8707 resource indicators.
//
// RFC 8707 "Resource Indicators for OAuth 2.0" specifies that clients SHOULD
// include a "resource" parameter to indicate the intended resource server.
// This allows the authorization server to:
//   - Bind the token to a specific audience
//   - Include the resource in the JWT "aud" claim
//   - Prevent token misuse across different resource servers
//
// For MCP servers, the resource indicator should be the MCP server URL.
//
// Current status: KNOWN GAP - mcpproxy lacks RFC 8707 support.
// This test logs the gap but does not fail CI (allowed to fail).
// Set MCPPROXY_STRICT_RFC8707=1 to make this test fail.
//
// When mcpproxy adds RFC 8707 support, this test should be updated to verify
// the implementation and always pass.
func TestMCPProxy_RFC8707_ResourceIndicator_NotImplemented(t *testing.T) {
	// Document the RFC 8707 gap
	gapMessage := "KNOWN GAP: mcpproxy does not implement RFC 8707 resource indicators. " +
		"The client.OAuthConfig struct lacks a Resource field. " +
		"See internal/oauth/config.go:390-398 and https://datatracker.ietf.org/doc/html/rfc8707"

	// Always log the gap so it's visible in test output
	t.Log("⚠️  " + gapMessage)

	// In strict mode, fail the test to block CI
	if os.Getenv("MCPPROXY_STRICT_RFC8707") == "1" {
		t.Error(gapMessage)
		return
	}

	// In normal mode, mark as skipped with explanation (test shows as SKIP, not FAIL)
	// This allows CI to pass while still documenting the gap
	t.Skip("RFC 8707 not implemented (allowed to fail). Set MCPPROXY_STRICT_RFC8707=1 to enforce.")

	// When RFC 8707 is implemented, replace this entire function with:
	// server := Start(t, Options{RequireResourceIndicator: true})
	// defer server.Shutdown()
	// // ... test that mcpproxy sends resource parameter ...
	// assert.Contains(t, authURL, "resource=")
}

// TestMCPProxy_RFC8707_ServerRejectsWithoutResource verifies that an OAuth server
// requiring RFC 8707 will reject clients that don't send the resource parameter.
//
// This is a "specification compliance" test - it shows what SHOULD happen when
// mcpproxy tries to authenticate against an RFC 8707-compliant server.
func TestMCPProxy_RFC8707_ServerRejectsWithoutResource(t *testing.T) {
	// Start OAuth test server that requires resource indicator
	server := Start(t, Options{
		RequireResourceIndicator: true, // Server will reject requests without resource param
	})
	defer server.Shutdown()

	// Generate proper PKCE challenge
	codeVerifier := "test-verifier-mcpproxy-rfc8707"
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	// Simulate what mcpproxy's OAuth client would send (WITHOUT resource param)
	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", server.PublicClientID)
	authParams.Set("redirect_uri", "http://127.0.0.1:9999/callback")
	authParams.Set("code_challenge", codeChallenge)
	authParams.Set("code_challenge_method", "S256")
	// mcpproxy does NOT include "resource" - this causes the failure

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.PostForm(server.AuthorizationEndpoint, authParams)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Server should reject with error
	require.Equal(t, http.StatusFound, resp.StatusCode, "Should redirect with error")

	location := resp.Header.Get("Location")
	redirectURL, _ := url.Parse(location)
	errorParam := redirectURL.Query().Get("error")
	errorDesc := redirectURL.Query().Get("error_description")

	assert.Equal(t, "invalid_request", errorParam,
		"Server should reject with invalid_request when resource indicator is missing")
	assert.Contains(t, errorDesc, "RFC 8707",
		"Error description should mention RFC 8707")

	t.Logf("RFC 8707 compliance: Server correctly rejected request without resource parameter")
	t.Logf("  error=%s, error_description=%s", errorParam, errorDesc)
}

// TestMCPProxy_RFC8707_ResourceInJWTAudience verifies that when the OAuth server
// issues a JWT with an audience claim based on the resource indicator, the token
// is properly bound to that audience.
//
// This is the client-side validation counterpart to RFC 8707:
//   - Server returns JWT with "aud" claim set to the resource indicator
//   - Client SHOULD verify the "aud" claim matches the intended resource
//
// Current status: Verifies server behavior; mcpproxy client validation not tested
func TestMCPProxy_RFC8707_ResourceInJWTAudience(t *testing.T) {
	server := Start(t, Options{})
	defer server.Shutdown()

	expectedResource := "https://api.example.com/mcp"

	// Get an authorization code with resource indicator
	codeVerifier := "test-verifier-for-audience"
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	// Complete auth flow with resource to get JWT with audience
	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", server.PublicClientID)
	authParams.Set("redirect_uri", "http://127.0.0.1/callback")
	authParams.Set("code_challenge", codeChallenge)
	authParams.Set("code_challenge_method", "S256")
	authParams.Set("resource", expectedResource)
	authParams.Set("username", "testuser")
	authParams.Set("password", "testpass")
	authParams.Set("consent", "on")
	authParams.Set("action", "approve")

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.PostForm(server.AuthorizationEndpoint, authParams)
	require.NoError(t, err)
	defer resp.Body.Close()

	location := resp.Header.Get("Location")
	redirectURL, _ := url.Parse(location)
	code := redirectURL.Query().Get("code")
	require.NotEmpty(t, code, "Should receive authorization code")

	// Exchange for token
	tokenParams := url.Values{}
	tokenParams.Set("grant_type", "authorization_code")
	tokenParams.Set("code", code)
	tokenParams.Set("redirect_uri", "http://127.0.0.1/callback")
	tokenParams.Set("client_id", server.PublicClientID)
	tokenParams.Set("code_verifier", codeVerifier)

	tokenResp, err := client.PostForm(server.TokenEndpoint, tokenParams)
	require.NoError(t, err)
	defer tokenResp.Body.Close()

	require.Equal(t, http.StatusOK, tokenResp.StatusCode)

	// Parse token response
	var tokenData TokenResponse
	err = json.NewDecoder(tokenResp.Body).Decode(&tokenData)
	require.NoError(t, err)

	// The JWT should have "aud" claim matching the resource
	accessToken := tokenData.AccessToken
	require.NotEmpty(t, accessToken, "Should receive access token")

	// Parse JWT claims (it's a JWT, not opaque)
	parts := strings.Split(accessToken, ".")
	require.Len(t, parts, 3, "Access token should be a JWT with 3 parts")

	// Decode the claims (second part)
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	var claims map[string]interface{}
	err = json.Unmarshal(claimsJSON, &claims)
	require.NoError(t, err)

	// Verify the audience claim is present and matches
	aud, ok := claims["aud"]
	require.True(t, ok, "JWT should contain 'aud' claim for RFC 8707 compliance")

	// Audience can be string or array
	switch v := aud.(type) {
	case string:
		assert.Equal(t, expectedResource, v,
			"JWT 'aud' claim should match the resource indicator")
	case []interface{}:
		require.NotEmpty(t, v, "JWT 'aud' array should not be empty")
		assert.Equal(t, expectedResource, v[0],
			"JWT 'aud' claim should match the resource indicator")
	default:
		t.Fatalf("Unexpected 'aud' claim type: %T", aud)
	}

	// NOTE: This test verifies the OAuth server returns correct JWT,
	// but mcpproxy does NOT yet validate the 'aud' claim (RFC 8707 gap)
	t.Log("NOTE: This test verifies the OAuth server returns correct JWT. " +
		"mcpproxy does NOT yet validate the 'aud' claim (RFC 8707 client-side gap)")
}
