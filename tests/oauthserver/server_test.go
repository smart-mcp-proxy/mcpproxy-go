package oauthserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStart_DefaultOptions(t *testing.T) {
	server := Start(t, Options{})
	defer server.Shutdown()

	assert.NotEmpty(t, server.IssuerURL)
	assert.NotEmpty(t, server.AuthorizationEndpoint)
	assert.NotEmpty(t, server.TokenEndpoint)
	assert.NotEmpty(t, server.JWKSURL)
	assert.NotEmpty(t, server.ClientID)
	assert.NotEmpty(t, server.ClientSecret)
	assert.NotEmpty(t, server.PublicClientID)
	assert.NotNil(t, server.Server)
}

func TestDiscovery(t *testing.T) {
	server := Start(t, Options{})
	defer server.Shutdown()

	// Test both discovery endpoints
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(server.IssuerURL + path)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

			var metadata DiscoveryMetadata
			err = json.NewDecoder(resp.Body).Decode(&metadata)
			require.NoError(t, err)

			assert.Equal(t, server.IssuerURL, metadata.Issuer)
			assert.Equal(t, server.AuthorizationEndpoint, metadata.AuthorizationEndpoint)
			assert.Equal(t, server.TokenEndpoint, metadata.TokenEndpoint)
			assert.Equal(t, server.JWKSURL, metadata.JWKSURI)
			assert.Contains(t, metadata.GrantTypesSupported, "authorization_code")
			assert.Contains(t, metadata.CodeChallengeMethodsSupported, "S256")
		})
	}
}

func TestJWKS(t *testing.T) {
	server := Start(t, Options{})
	defer server.Shutdown()

	resp, err := http.Get(server.JWKSURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var jwks JWKS
	err = json.NewDecoder(resp.Body).Decode(&jwks)
	require.NoError(t, err)

	assert.Len(t, jwks.Keys, 1)
	assert.Equal(t, "RSA", jwks.Keys[0].Kty)
	assert.Equal(t, "sig", jwks.Keys[0].Use)
	assert.Equal(t, "RS256", jwks.Keys[0].Alg)
	assert.NotEmpty(t, jwks.Keys[0].Kid)
	assert.NotEmpty(t, jwks.Keys[0].N)
	assert.NotEmpty(t, jwks.Keys[0].E)
}

func TestAuthCodeFlow_WithPKCE(t *testing.T) {
	server := Start(t, Options{})
	defer server.Shutdown()

	// Generate PKCE values
	codeVerifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	// Step 1: Simulate authorization (POST to /authorize with credentials)
	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", server.PublicClientID)
	authParams.Set("redirect_uri", "http://127.0.0.1/callback")
	authParams.Set("scope", "read write")
	authParams.Set("state", "test-state")
	authParams.Set("code_challenge", codeChallenge)
	authParams.Set("code_challenge_method", "S256")
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

	// Should redirect with code
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	location := resp.Header.Get("Location")
	assert.NotEmpty(t, location)

	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	code := redirectURL.Query().Get("code")
	assert.NotEmpty(t, code, "Expected authorization code in redirect")
	assert.Equal(t, "test-state", redirectURL.Query().Get("state"))

	// Step 2: Exchange code for tokens
	tokenParams := url.Values{}
	tokenParams.Set("grant_type", "authorization_code")
	tokenParams.Set("code", code)
	tokenParams.Set("redirect_uri", "http://127.0.0.1/callback")
	tokenParams.Set("client_id", server.PublicClientID)
	tokenParams.Set("code_verifier", codeVerifier)

	tokenResp, err := http.PostForm(server.TokenEndpoint, tokenParams)
	require.NoError(t, err)
	defer tokenResp.Body.Close()

	assert.Equal(t, http.StatusOK, tokenResp.StatusCode)

	var tokenResponse TokenResponse
	err = json.NewDecoder(tokenResp.Body).Decode(&tokenResponse)
	require.NoError(t, err)

	assert.NotEmpty(t, tokenResponse.AccessToken)
	assert.Equal(t, "Bearer", tokenResponse.TokenType)
	assert.Greater(t, tokenResponse.ExpiresIn, 0)
	assert.NotEmpty(t, tokenResponse.RefreshToken)
	assert.Contains(t, tokenResponse.Scope, "read")
}

func TestAuthCodeFlow_InvalidPKCE(t *testing.T) {
	server := Start(t, Options{})
	defer server.Shutdown()

	// Generate PKCE values
	codeVerifier := "correct-verifier"
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	// Step 1: Get authorization code
	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", server.PublicClientID)
	authParams.Set("redirect_uri", "http://127.0.0.1/callback")
	authParams.Set("code_challenge", codeChallenge)
	authParams.Set("code_challenge_method", "S256")
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

	// Step 2: Try to exchange with wrong verifier
	tokenParams := url.Values{}
	tokenParams.Set("grant_type", "authorization_code")
	tokenParams.Set("code", code)
	tokenParams.Set("redirect_uri", "http://127.0.0.1/callback")
	tokenParams.Set("client_id", server.PublicClientID)
	tokenParams.Set("code_verifier", "wrong-verifier")

	tokenResp, err := http.PostForm(server.TokenEndpoint, tokenParams)
	require.NoError(t, err)
	defer tokenResp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, tokenResp.StatusCode)

	var errorResp TokenErrorResponse
	err = json.NewDecoder(tokenResp.Body).Decode(&errorResp)
	require.NoError(t, err)

	assert.Equal(t, "invalid_grant", errorResp.Error)
}

func TestRefreshToken(t *testing.T) {
	server := Start(t, Options{
		AccessTokenExpiry: 1 * time.Second, // Short expiry for testing
	})
	defer server.Shutdown()

	// First get tokens via auth code flow
	codeVerifier := "test-verifier-for-refresh"
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", server.PublicClientID)
	authParams.Set("redirect_uri", "http://127.0.0.1/callback")
	authParams.Set("scope", "read write")
	authParams.Set("code_challenge", codeChallenge)
	authParams.Set("code_challenge_method", "S256")
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

	// Exchange code for tokens
	tokenParams := url.Values{}
	tokenParams.Set("grant_type", "authorization_code")
	tokenParams.Set("code", code)
	tokenParams.Set("redirect_uri", "http://127.0.0.1/callback")
	tokenParams.Set("client_id", server.PublicClientID)
	tokenParams.Set("code_verifier", codeVerifier)

	tokenResp, err := http.PostForm(server.TokenEndpoint, tokenParams)
	require.NoError(t, err)
	defer tokenResp.Body.Close()

	var firstTokens TokenResponse
	json.NewDecoder(tokenResp.Body).Decode(&firstTokens)
	require.NotEmpty(t, firstTokens.RefreshToken)

	// Use refresh token
	refreshParams := url.Values{}
	refreshParams.Set("grant_type", "refresh_token")
	refreshParams.Set("refresh_token", firstTokens.RefreshToken)
	refreshParams.Set("client_id", server.PublicClientID)

	refreshResp, err := http.PostForm(server.TokenEndpoint, refreshParams)
	require.NoError(t, err)
	defer refreshResp.Body.Close()

	assert.Equal(t, http.StatusOK, refreshResp.StatusCode)

	var newTokens TokenResponse
	err = json.NewDecoder(refreshResp.Body).Decode(&newTokens)
	require.NoError(t, err)

	assert.NotEmpty(t, newTokens.AccessToken)
	assert.NotEqual(t, firstTokens.AccessToken, newTokens.AccessToken, "Should get new access token")
	assert.NotEmpty(t, newTokens.RefreshToken)
}

func TestInvalidCredentials(t *testing.T) {
	server := Start(t, Options{})
	defer server.Shutdown()

	// Try to authorize with wrong password
	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", server.PublicClientID)
	authParams.Set("redirect_uri", "http://127.0.0.1/callback")
	authParams.Set("username", "testuser")
	authParams.Set("password", "wrongpassword")
	authParams.Set("consent", "on")
	authParams.Set("action", "approve")

	resp, err := http.PostForm(server.AuthorizationEndpoint, authParams)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should return login page with error (200 OK with HTML)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "Invalid username or password")
}

func TestConsentDenied(t *testing.T) {
	server := Start(t, Options{})
	defer server.Shutdown()

	codeVerifier := "test-verifier"
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	// Deny consent
	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", server.PublicClientID)
	authParams.Set("redirect_uri", "http://127.0.0.1/callback")
	authParams.Set("state", "test-state")
	authParams.Set("code_challenge", codeChallenge)
	authParams.Set("code_challenge_method", "S256")
	authParams.Set("username", "testuser")
	authParams.Set("password", "testpass")
	authParams.Set("consent", "") // No consent
	authParams.Set("action", "deny")

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.PostForm(server.AuthorizationEndpoint, authParams)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusFound, resp.StatusCode)
	location := resp.Header.Get("Location")

	redirectURL, _ := url.Parse(location)
	assert.Equal(t, "access_denied", redirectURL.Query().Get("error"))
	assert.Empty(t, redirectURL.Query().Get("code"))
}

func TestErrorInjection_InvalidClient(t *testing.T) {
	server := Start(t, Options{
		ErrorMode: ErrorMode{
			TokenInvalidClient: true,
		},
	})
	defer server.Shutdown()

	// Any token request should fail with invalid_client
	tokenParams := url.Values{}
	tokenParams.Set("grant_type", "authorization_code")
	tokenParams.Set("code", "any-code")
	tokenParams.Set("redirect_uri", "http://127.0.0.1/callback")
	tokenParams.Set("client_id", server.ClientID)
	tokenParams.Set("client_secret", server.ClientSecret)

	resp, err := http.PostForm(server.TokenEndpoint, tokenParams)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var errorResp TokenErrorResponse
	json.NewDecoder(resp.Body).Decode(&errorResp)
	assert.Equal(t, "invalid_client", errorResp.Error)
}

func TestConfidentialClient_AuthCodeFlow(t *testing.T) {
	server := Start(t, Options{
		RequirePKCE: false, // Allow confidential clients without PKCE
	})
	defer server.Shutdown()

	// Step 1: Get authorization code (no PKCE for confidential client)
	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", server.ClientID)
	authParams.Set("redirect_uri", "http://127.0.0.1/callback")
	authParams.Set("scope", "read")
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
	require.NotEmpty(t, code)

	// Step 2: Exchange with client secret
	tokenParams := url.Values{}
	tokenParams.Set("grant_type", "authorization_code")
	tokenParams.Set("code", code)
	tokenParams.Set("redirect_uri", "http://127.0.0.1/callback")
	tokenParams.Set("client_id", server.ClientID)
	tokenParams.Set("client_secret", server.ClientSecret)

	tokenResp, err := http.PostForm(server.TokenEndpoint, tokenParams)
	require.NoError(t, err)
	defer tokenResp.Body.Close()

	assert.Equal(t, http.StatusOK, tokenResp.StatusCode)

	var tokenResponse TokenResponse
	json.NewDecoder(tokenResp.Body).Decode(&tokenResponse)
	assert.NotEmpty(t, tokenResponse.AccessToken)
}

func TestClientCredentials(t *testing.T) {
	server := Start(t, Options{})
	defer server.Shutdown()

	tokenParams := url.Values{}
	tokenParams.Set("grant_type", "client_credentials")
	tokenParams.Set("client_id", server.ClientID)
	tokenParams.Set("client_secret", server.ClientSecret)
	tokenParams.Set("scope", "read")

	resp, err := http.PostForm(server.TokenEndpoint, tokenParams)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var tokenResponse TokenResponse
	json.NewDecoder(resp.Body).Decode(&tokenResponse)
	assert.NotEmpty(t, tokenResponse.AccessToken)
	assert.Empty(t, tokenResponse.RefreshToken, "Client credentials should not return refresh token")
}

func TestWWWAuthenticate(t *testing.T) {
	server := Start(t, Options{
		DetectionMode: Both, // Enable WWW-Authenticate
	})
	defer server.Shutdown()

	resp, err := http.Get(server.ProtectedResourceURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	assert.NotEmpty(t, wwwAuth)
	assert.Contains(t, wwwAuth, "Bearer")
	assert.Contains(t, wwwAuth, "authorization_uri")
}

func TestKeyRotation(t *testing.T) {
	server := Start(t, Options{})
	defer server.Shutdown()

	// Get initial JWKS
	resp1, err := http.Get(server.JWKSURL)
	require.NoError(t, err)
	var jwks1 JWKS
	json.NewDecoder(resp1.Body).Decode(&jwks1)
	resp1.Body.Close()

	initialKid := jwks1.Keys[0].Kid

	// Rotate key
	newKid, err := server.Server.keyRing.RotateKey()
	require.NoError(t, err)
	assert.NotEqual(t, initialKid, newKid)

	// Get new JWKS
	resp2, err := http.Get(server.JWKSURL)
	require.NoError(t, err)
	var jwks2 JWKS
	json.NewDecoder(resp2.Body).Decode(&jwks2)
	resp2.Body.Close()

	// Should have 2 keys now
	assert.Len(t, jwks2.Keys, 2)

	// Find keys by ID
	var foundOld, foundNew bool
	for _, k := range jwks2.Keys {
		if k.Kid == initialKid {
			foundOld = true
		}
		if k.Kid == newKid {
			foundNew = true
		}
	}
	assert.True(t, foundOld, "Old key should still be in JWKS")
	assert.True(t, foundNew, "New key should be in JWKS")
}

func TestResourceIndicator(t *testing.T) {
	server := Start(t, Options{})
	defer server.Shutdown()

	resource := "https://api.example.com"
	codeVerifier := "test-verifier-resource"
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	// Get authorization code with resource
	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", server.PublicClientID)
	authParams.Set("redirect_uri", "http://127.0.0.1/callback")
	authParams.Set("code_challenge", codeChallenge)
	authParams.Set("code_challenge_method", "S256")
	authParams.Set("resource", resource)
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

	// Exchange for token
	tokenParams := url.Values{}
	tokenParams.Set("grant_type", "authorization_code")
	tokenParams.Set("code", code)
	tokenParams.Set("redirect_uri", "http://127.0.0.1/callback")
	tokenParams.Set("client_id", server.PublicClientID)
	tokenParams.Set("code_verifier", codeVerifier)

	tokenResp, err := http.PostForm(server.TokenEndpoint, tokenParams)
	require.NoError(t, err)
	defer tokenResp.Body.Close()

	var tokenResponse TokenResponse
	json.NewDecoder(tokenResp.Body).Decode(&tokenResponse)

	// Decode JWT and check audience
	parts := strings.Split(tokenResponse.AccessToken, ".")
	require.Len(t, parts, 3)

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	var claims map[string]interface{}
	json.Unmarshal(claimsJSON, &claims)

	// Check audience contains the resource
	aud, ok := claims["aud"]
	require.True(t, ok, "Token should have audience claim")

	// Audience can be string or array
	switch v := aud.(type) {
	case string:
		assert.Equal(t, resource, v)
	case []interface{}:
		assert.Contains(t, v, resource)
	}
}

// --- DCR Tests ---

func TestDCR_RegisterClient(t *testing.T) {
	server := Start(t, Options{
		EnableDCR: true,
	})
	defer server.Shutdown()

	// Register a new client
	reqBody := `{
		"redirect_uris": ["http://127.0.0.1/callback"],
		"client_name": "Test Client",
		"grant_types": ["authorization_code", "refresh_token"],
		"response_types": ["code"],
		"scope": "read write"
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
	assert.NotEmpty(t, clientResp.ClientSecret)
	assert.Equal(t, "Test Client", clientResp.ClientName)
	assert.Contains(t, clientResp.RedirectURIs, "http://127.0.0.1/callback")
	assert.Contains(t, clientResp.GrantTypes, "authorization_code")
}

func TestDCR_RegisterPublicClient(t *testing.T) {
	server := Start(t, Options{
		EnableDCR: true,
	})
	defer server.Shutdown()

	// Register a public client (token_endpoint_auth_method: none)
	reqBody := `{
		"redirect_uris": ["http://127.0.0.1/callback"],
		"client_name": "Public CLI App",
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
	json.NewDecoder(resp.Body).Decode(&clientResp)

	assert.NotEmpty(t, clientResp.ClientID)
	assert.Empty(t, clientResp.ClientSecret, "Public client should not have secret")
	assert.Equal(t, "none", clientResp.TokenEndpointAuthMethod)
}

func TestDCR_MissingRedirectURI(t *testing.T) {
	server := Start(t, Options{
		EnableDCR: true,
	})
	defer server.Shutdown()

	reqBody := `{
		"client_name": "Bad Client"
	}`

	resp, err := http.Post(
		server.IssuerURL+"/registration",
		"application/json",
		strings.NewReader(reqBody),
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errorResp OAuthError
	json.NewDecoder(resp.Body).Decode(&errorResp)
	assert.Equal(t, "invalid_redirect_uri", errorResp.Error)
}

func TestDCR_Disabled(t *testing.T) {
	server := Start(t, Options{
		EnableAuthCode: true, // Enable something so defaults don't kick in
		EnableDCR:      false,
	})
	defer server.Shutdown()

	reqBody := `{
		"redirect_uris": ["http://127.0.0.1/callback"]
	}`

	resp, err := http.Post(
		server.IssuerURL+"/registration",
		"application/json",
		strings.NewReader(reqBody),
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	// When DCR is disabled, the endpoint is not registered (404)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDCR_UseRegisteredClient(t *testing.T) {
	server := Start(t, Options{
		EnableAuthCode: true,
		EnableDCR:      true,
	})
	defer server.Shutdown()

	// Register client
	reqBody := `{
		"redirect_uris": ["http://127.0.0.1/callback"],
		"token_endpoint_auth_method": "none"
	}`

	resp, err := http.Post(
		server.IssuerURL+"/registration",
		"application/json",
		strings.NewReader(reqBody),
	)
	require.NoError(t, err)

	var clientResp ClientRegistrationResponse
	json.NewDecoder(resp.Body).Decode(&clientResp)
	resp.Body.Close()

	// Use the registered client for auth code flow
	codeVerifier := "dcr-test-verifier"
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", clientResp.ClientID)
	authParams.Set("redirect_uri", "http://127.0.0.1/callback")
	authParams.Set("code_challenge", codeChallenge)
	authParams.Set("code_challenge_method", "S256")
	authParams.Set("username", "testuser")
	authParams.Set("password", "testpass")
	authParams.Set("consent", "on")
	authParams.Set("action", "approve")

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	authResp, err := client.PostForm(server.AuthorizationEndpoint, authParams)
	require.NoError(t, err)
	defer authResp.Body.Close()

	assert.Equal(t, http.StatusFound, authResp.StatusCode)
	location := authResp.Header.Get("Location")
	redirectURL, _ := url.Parse(location)
	code := redirectURL.Query().Get("code")
	require.NotEmpty(t, code)
}

// --- Device Code Flow Tests ---

func TestDeviceCode_Authorization(t *testing.T) {
	server := Start(t, Options{
		EnableDeviceCode: true,
	})
	defer server.Shutdown()

	// Request device authorization
	params := url.Values{}
	params.Set("client_id", server.PublicClientID)
	params.Set("scope", "read write")

	resp, err := http.PostForm(server.IssuerURL+"/device_authorization", params)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var deviceResp DeviceAuthorizationResponse
	err = json.NewDecoder(resp.Body).Decode(&deviceResp)
	require.NoError(t, err)

	assert.NotEmpty(t, deviceResp.DeviceCode)
	assert.NotEmpty(t, deviceResp.UserCode)
	assert.NotEmpty(t, deviceResp.VerificationURI)
	assert.NotEmpty(t, deviceResp.VerificationURIComplete)
	assert.Greater(t, deviceResp.ExpiresIn, 0)
	assert.Greater(t, deviceResp.Interval, 0)

	// User code should be in format "XXXX-XXXX"
	assert.Contains(t, deviceResp.UserCode, "-")
	assert.Len(t, deviceResp.UserCode, 9) // 4 chars + dash + 4 chars
}

func TestDeviceCode_Disabled(t *testing.T) {
	server := Start(t, Options{
		EnableAuthCode:   true, // Enable something so defaults don't kick in
		EnableDeviceCode: false,
	})
	defer server.Shutdown()

	params := url.Values{}
	params.Set("client_id", server.PublicClientID)

	resp, err := http.PostForm(server.IssuerURL+"/device_authorization", params)
	require.NoError(t, err)
	defer resp.Body.Close()

	// When device code is disabled, the endpoint is not registered (404)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDeviceCode_ProgrammaticApproval(t *testing.T) {
	server := Start(t, Options{
		EnableDeviceCode: true,
	})
	defer server.Shutdown()

	// Request device authorization
	params := url.Values{}
	params.Set("client_id", server.PublicClientID)
	params.Set("scope", "read")

	resp, err := http.PostForm(server.IssuerURL+"/device_authorization", params)
	require.NoError(t, err)

	var deviceResp DeviceAuthorizationResponse
	json.NewDecoder(resp.Body).Decode(&deviceResp)
	resp.Body.Close()

	// First poll should return authorization_pending
	tokenParams := url.Values{}
	tokenParams.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	tokenParams.Set("device_code", deviceResp.DeviceCode)
	tokenParams.Set("client_id", server.PublicClientID)

	tokenResp, err := http.PostForm(server.TokenEndpoint, tokenParams)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, tokenResp.StatusCode)

	var errorResp TokenErrorResponse
	json.NewDecoder(tokenResp.Body).Decode(&errorResp)
	tokenResp.Body.Close()
	assert.Equal(t, "authorization_pending", errorResp.Error)

	// Programmatically approve the device code
	err = server.Server.ApproveDeviceCode(deviceResp.UserCode)
	require.NoError(t, err)

	// Now polling should succeed
	tokenResp2, err := http.PostForm(server.TokenEndpoint, tokenParams)
	require.NoError(t, err)
	defer tokenResp2.Body.Close()

	assert.Equal(t, http.StatusOK, tokenResp2.StatusCode)

	var tokenResponse TokenResponse
	json.NewDecoder(tokenResp2.Body).Decode(&tokenResponse)
	assert.NotEmpty(t, tokenResponse.AccessToken)
	assert.Equal(t, "Bearer", tokenResponse.TokenType)
}

func TestDeviceCode_ProgrammaticDenial(t *testing.T) {
	server := Start(t, Options{
		EnableDeviceCode: true,
	})
	defer server.Shutdown()

	// Request device authorization
	params := url.Values{}
	params.Set("client_id", server.PublicClientID)

	resp, err := http.PostForm(server.IssuerURL+"/device_authorization", params)
	require.NoError(t, err)

	var deviceResp DeviceAuthorizationResponse
	json.NewDecoder(resp.Body).Decode(&deviceResp)
	resp.Body.Close()

	// Programmatically deny the device code
	err = server.Server.DenyDeviceCode(deviceResp.UserCode)
	require.NoError(t, err)

	// Polling should return access_denied
	tokenParams := url.Values{}
	tokenParams.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	tokenParams.Set("device_code", deviceResp.DeviceCode)
	tokenParams.Set("client_id", server.PublicClientID)

	tokenResp, err := http.PostForm(server.TokenEndpoint, tokenParams)
	require.NoError(t, err)
	defer tokenResp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, tokenResp.StatusCode)

	var errorResp TokenErrorResponse
	json.NewDecoder(tokenResp.Body).Decode(&errorResp)
	assert.Equal(t, "access_denied", errorResp.Error)
}

func TestDeviceCode_Expiration(t *testing.T) {
	server := Start(t, Options{
		EnableDeviceCode: true,
	})
	defer server.Shutdown()

	// Request device authorization
	params := url.Values{}
	params.Set("client_id", server.PublicClientID)

	resp, err := http.PostForm(server.IssuerURL+"/device_authorization", params)
	require.NoError(t, err)

	var deviceResp DeviceAuthorizationResponse
	json.NewDecoder(resp.Body).Decode(&deviceResp)
	resp.Body.Close()

	// Expire the device code programmatically
	err = server.Server.ExpireDeviceCode(deviceResp.UserCode)
	require.NoError(t, err)

	// Polling should return expired_token
	tokenParams := url.Values{}
	tokenParams.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	tokenParams.Set("device_code", deviceResp.DeviceCode)
	tokenParams.Set("client_id", server.PublicClientID)

	tokenResp, err := http.PostForm(server.TokenEndpoint, tokenParams)
	require.NoError(t, err)
	defer tokenResp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, tokenResp.StatusCode)

	var errorResp TokenErrorResponse
	json.NewDecoder(tokenResp.Body).Decode(&errorResp)
	assert.Equal(t, "expired_token", errorResp.Error)
}

func TestDeviceCode_VerificationPage(t *testing.T) {
	server := Start(t, Options{
		EnableDeviceCode: true,
	})
	defer server.Shutdown()

	// Request device authorization
	params := url.Values{}
	params.Set("client_id", server.PublicClientID)

	resp, err := http.PostForm(server.IssuerURL+"/device_authorization", params)
	require.NoError(t, err)

	var deviceResp DeviceAuthorizationResponse
	json.NewDecoder(resp.Body).Decode(&deviceResp)
	resp.Body.Close()

	// GET the verification page
	verifyResp, err := http.Get(deviceResp.VerificationURIComplete)
	require.NoError(t, err)
	defer verifyResp.Body.Close()

	assert.Equal(t, http.StatusOK, verifyResp.StatusCode)
	assert.Equal(t, "text/html", verifyResp.Header.Get("Content-Type"))

	body, _ := io.ReadAll(verifyResp.Body)
	assert.Contains(t, string(body), "Device Verification")
	assert.Contains(t, string(body), deviceResp.UserCode) // Should pre-fill the user code
}
