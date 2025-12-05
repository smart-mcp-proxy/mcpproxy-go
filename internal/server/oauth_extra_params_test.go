package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"mcpproxy-go/internal/oauth"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockOAuthServer is a test OAuth provider that requires the "resource" parameter
type mockOAuthServer struct {
	server                *httptest.Server
	authorizationURL      string
	tokenURL              string
	metadataURL           string
	requireResourceParam  bool
	capturedAuthParams    url.Values
	capturedTokenParams   url.Values
	capturedRefreshParams url.Values
}

// newMockOAuthServer creates a mock OAuth server for testing
func newMockOAuthServer(requireResource bool) *mockOAuthServer {
	mock := &mockOAuthServer{
		requireResourceParam: requireResource,
	}

	mux := http.NewServeMux()

	// OAuth metadata endpoint (RFC 8414)
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		metadata := map[string]interface{}{
			"issuer":                 mock.server.URL,
			"authorization_endpoint": mock.server.URL + "/authorize",
			"token_endpoint":         mock.server.URL + "/token",
			"scopes_supported":       []string{"read", "write"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metadata)
	})

	// Authorization endpoint
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		mock.capturedAuthParams = r.URL.Query()

		// Require resource parameter if configured
		if mock.requireResourceParam && r.URL.Query().Get("resource") == "" {
			http.Error(w, `{"error":"invalid_request","error_description":"resource parameter required"}`, http.StatusBadRequest)
			return
		}

		// Return authorization code
		callbackURL := r.URL.Query().Get("redirect_uri")
		if callbackURL == "" {
			http.Error(w, "redirect_uri required", http.StatusBadRequest)
			return
		}

		redirectURL := callbackURL + "?code=test_auth_code_123&state=" + r.URL.Query().Get("state")
		http.Redirect(w, r, redirectURL, http.StatusFound)
	})

	// Token endpoint (exchange and refresh)
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		params, err := url.ParseQuery(string(bodyBytes))
		if err != nil {
			http.Error(w, "invalid form data", http.StatusBadRequest)
			return
		}

		grantType := params.Get("grant_type")

		// Capture params for verification
		switch grantType {
		case "authorization_code":
			mock.capturedTokenParams = params
		case "refresh_token":
			mock.capturedRefreshParams = params
		}

		// Require resource parameter if configured
		if mock.requireResourceParam && params.Get("resource") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error":             "invalid_request",
				"error_description": "resource parameter required",
			})
			return
		}

		// Return access token
		w.Header().Set("Content-Type", "application/json")
		response := map[string]interface{}{
			"access_token":  "test_access_token_xyz",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "test_refresh_token_xyz",
		}
		json.NewEncoder(w).Encode(response)
	})

	mock.server = httptest.NewServer(mux)
	mock.authorizationURL = mock.server.URL + "/authorize"
	mock.tokenURL = mock.server.URL + "/token"
	mock.metadataURL = mock.server.URL + "/.well-known/oauth-authorization-server"

	return mock
}

func (m *mockOAuthServer) Close() {
	if m.server != nil {
		m.server.Close()
	}
}

// TestOAuthExtraParams_AuthorizationFlow verifies that extra params are injected into authorization URL
func TestOAuthExtraParams_AuthorizationFlow(t *testing.T) {
	// Create mock OAuth server requiring resource parameter
	mockServer := newMockOAuthServer(true)
	defer mockServer.Close()

	// Create wrapper with resource parameter
	extraParams := map[string]string{
		"resource": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
		"audience": "mcp-api",
	}

	wrapper := oauth.NewOAuthTransportWrapper(http.DefaultTransport, extraParams, zap.NewNop())

	// Create HTTP client with wrapper
	httpClient := &http.Client{
		Transport: wrapper,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Don't follow redirects in test
			return http.ErrUseLastResponse
		},
	}

	// Make authorization request
	authURL := mockServer.authorizationURL + "?response_type=code&client_id=test_client&redirect_uri=http://localhost:8080/callback&state=test_state"
	resp, err := httpClient.Get(authURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify extra params were injected
	assert.NotNil(t, mockServer.capturedAuthParams)
	assert.Equal(t, "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp", mockServer.capturedAuthParams.Get("resource"),
		"resource parameter should be injected")
	assert.Equal(t, "mcp-api", mockServer.capturedAuthParams.Get("audience"),
		"audience parameter should be injected")

	// Verify standard OAuth params are preserved
	assert.Equal(t, "code", mockServer.capturedAuthParams.Get("response_type"))
	assert.Equal(t, "test_client", mockServer.capturedAuthParams.Get("client_id"))
}

// TestOAuthExtraParams_TokenExchange verifies that extra params are injected into token exchange request
func TestOAuthExtraParams_TokenExchange(t *testing.T) {
	// Create mock OAuth server requiring resource parameter
	mockServer := newMockOAuthServer(true)
	defer mockServer.Close()

	// Create wrapper with resource parameter
	extraParams := map[string]string{
		"resource": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
	}

	wrapper := oauth.NewOAuthTransportWrapper(http.DefaultTransport, extraParams, zap.NewNop())

	// Create HTTP client with wrapper
	httpClient := &http.Client{
		Transport: wrapper,
	}

	// Make token exchange request
	tokenForm := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {"test_auth_code_123"},
		"redirect_uri": {"http://localhost:8080/callback"},
		"client_id":    {"test_client"},
	}

	resp, err := httpClient.PostForm(mockServer.tokenURL, tokenForm)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify response is successful
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify extra params were injected
	assert.NotNil(t, mockServer.capturedTokenParams)
	assert.Equal(t, "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp", mockServer.capturedTokenParams.Get("resource"),
		"resource parameter should be injected into token request")

	// Verify standard token params are preserved
	assert.Equal(t, "authorization_code", mockServer.capturedTokenParams.Get("grant_type"))
	assert.Equal(t, "test_auth_code_123", mockServer.capturedTokenParams.Get("code"))
}

// TestOAuthExtraParams_TokenRefresh verifies that extra params are injected into token refresh request
func TestOAuthExtraParams_TokenRefresh(t *testing.T) {
	// Create mock OAuth server requiring resource parameter
	mockServer := newMockOAuthServer(true)
	defer mockServer.Close()

	// Create wrapper with resource parameter
	extraParams := map[string]string{
		"resource": "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp",
		"tenant":   "tenant-456",
	}

	wrapper := oauth.NewOAuthTransportWrapper(http.DefaultTransport, extraParams, zap.NewNop())

	// Create HTTP client with wrapper
	httpClient := &http.Client{
		Transport: wrapper,
	}

	// Make token refresh request
	refreshForm := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {"test_refresh_token_xyz"},
		"client_id":     {"test_client"},
	}

	resp, err := httpClient.PostForm(mockServer.tokenURL, refreshForm)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify response is successful
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify extra params were injected
	assert.NotNil(t, mockServer.capturedRefreshParams)
	assert.Equal(t, "https://oauth.runlayer.com/api/v1/proxy/UUID/mcp", mockServer.capturedRefreshParams.Get("resource"),
		"resource parameter should be injected into refresh request")
	assert.Equal(t, "tenant-456", mockServer.capturedRefreshParams.Get("tenant"),
		"tenant parameter should be injected")

	// Verify standard refresh params are preserved
	assert.Equal(t, "refresh_token", mockServer.capturedRefreshParams.Get("grant_type"))
	assert.Equal(t, "test_refresh_token_xyz", mockServer.capturedRefreshParams.Get("refresh_token"))
}

// TestOAuthExtraParams_BackwardCompatibility verifies OAuth works without extra params
func TestOAuthExtraParams_BackwardCompatibility(t *testing.T) {
	// Create mock OAuth server NOT requiring resource parameter
	mockServer := newMockOAuthServer(false)
	defer mockServer.Close()

	// Create wrapper with NO extra params (backward compatibility)
	wrapper := oauth.NewOAuthTransportWrapper(http.DefaultTransport, nil, zap.NewNop())

	// Create HTTP client with wrapper
	httpClient := &http.Client{
		Transport: wrapper,
	}

	// Make token exchange request without extra params
	tokenForm := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {"test_auth_code_123"},
		"redirect_uri": {"http://localhost:8080/callback"},
		"client_id":    {"test_client"},
	}

	resp, err := httpClient.PostForm(mockServer.tokenURL, tokenForm)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify response is successful (no extra params required)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify no extra params were added
	assert.NotNil(t, mockServer.capturedTokenParams)
	assert.Empty(t, mockServer.capturedTokenParams.Get("resource"),
		"resource parameter should not be present")
}

// TestOAuthExtraParams_ServerRejectsWithoutResource verifies server rejection when resource is missing
func TestOAuthExtraParams_ServerRejectsWithoutResource(t *testing.T) {
	// Create mock OAuth server REQUIRING resource parameter
	mockServer := newMockOAuthServer(true)
	defer mockServer.Close()

	// Create wrapper with NO extra params (should fail)
	wrapper := oauth.NewOAuthTransportWrapper(http.DefaultTransport, nil, zap.NewNop())

	// Create HTTP client with wrapper
	httpClient := &http.Client{
		Transport: wrapper,
	}

	// Make token exchange request WITHOUT resource param
	tokenForm := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {"test_auth_code_123"},
		"redirect_uri": {"http://localhost:8080/callback"},
		"client_id":    {"test_client"},
	}

	resp, err := httpClient.PostForm(mockServer.tokenURL, tokenForm)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify server rejects the request
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Verify error response
	var errorResponse map[string]string
	err = json.NewDecoder(resp.Body).Decode(&errorResponse)
	require.NoError(t, err)
	assert.Equal(t, "invalid_request", errorResponse["error"])
	assert.Contains(t, strings.ToLower(errorResponse["error_description"]), "resource")
}

// TestOAuthExtraParams_MultipleParams verifies multiple extra params are injected correctly
func TestOAuthExtraParams_MultipleParams(t *testing.T) {
	// Create mock OAuth server
	mockServer := newMockOAuthServer(false) // Not requiring resource for this test
	defer mockServer.Close()

	// Create wrapper with multiple extra params
	extraParams := map[string]string{
		"resource": "https://example.com/mcp",
		"audience": "mcp-api",
		"tenant":   "tenant-789",
		"custom":   "value",
	}

	wrapper := oauth.NewOAuthTransportWrapper(http.DefaultTransport, extraParams, zap.NewNop())

	// Create HTTP client with wrapper
	httpClient := &http.Client{
		Transport: wrapper,
	}

	// Make token request
	tokenForm := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {"test_code"},
		"redirect_uri": {"http://localhost:8080/callback"},
	}

	resp, err := httpClient.PostForm(mockServer.tokenURL, tokenForm)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify all extra params were injected
	assert.NotNil(t, mockServer.capturedTokenParams)
	assert.Equal(t, "https://example.com/mcp", mockServer.capturedTokenParams.Get("resource"))
	assert.Equal(t, "mcp-api", mockServer.capturedTokenParams.Get("audience"))
	assert.Equal(t, "tenant-789", mockServer.capturedTokenParams.Get("tenant"))
	assert.Equal(t, "value", mockServer.capturedTokenParams.Get("custom"))
}

// TestOAuthExtraParams_NonOAuthRequestPassthrough verifies non-OAuth requests are not modified
func TestOAuthExtraParams_NonOAuthRequestPassthrough(t *testing.T) {
	// Create wrapper with extra params
	extraParams := map[string]string{
		"resource": "https://example.com/mcp",
	}

	wrapper := oauth.NewOAuthTransportWrapper(http.DefaultTransport, extraParams, zap.NewNop())

	// Create HTTP client with wrapper
	httpClient := &http.Client{
		Transport: wrapper,
	}

	// Create test server for non-OAuth endpoint
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request was not modified
		body, _ := io.ReadAll(r.Body)
		bodyStr := string(body)

		// Non-OAuth requests should not have extra params injected
		assert.NotContains(t, bodyStr, "resource")
		assert.NotContains(t, r.URL.RawQuery, "resource")

		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"ok"}`)
	}))
	defer testServer.Close()

	// Make non-OAuth request
	resp, err := httpClient.Post(testServer.URL+"/api/mcp", "application/json", strings.NewReader(`{"method":"tools/list"}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
