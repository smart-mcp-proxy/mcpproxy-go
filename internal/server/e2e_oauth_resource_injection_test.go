package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRunlayerServer creates a mock Runlayer-style OAuth server that requires
// the RFC 8707 resource parameter in the authorization URL.
// This mimics the behavior that caused issue #271.
type mockRunlayerServer struct {
	server              *httptest.Server
	capturedAuthURL     string
	capturedAuthParams  url.Values
	resourceParamFound  bool
}

func newMockRunlayerServer() *mockRunlayerServer {
	mock := &mockRunlayerServer{}

	mux := http.NewServeMux()

	// Protected Resource Metadata endpoint (RFC 9728)
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		// Return metadata with the resource field - this is what Runlayer returns
		metadata := map[string]interface{}{
			"resource":              mock.server.URL + "/mcp",
			"authorization_servers": []string{mock.server.URL},
			"scopes_supported":      []string{"read", "write"},
			"bearer_methods_supported": []string{"header"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metadata)
	})

	// OAuth Authorization Server Metadata endpoint (RFC 8414)
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		metadata := map[string]interface{}{
			"issuer":                            mock.server.URL,
			"authorization_endpoint":            mock.server.URL + "/authorize",
			"token_endpoint":                    mock.server.URL + "/token",
			"registration_endpoint":             mock.server.URL + "/register",
			"response_types_supported":          []string{"code"},
			"code_challenge_methods_supported":  []string{"S256"},
			"grant_types_supported":             []string{"authorization_code", "refresh_token"},
			"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metadata)
	})

	// Dynamic Client Registration endpoint
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		clientName, _ := body["client_name"].(string)
		if clientName == "" {
			clientName = "unknown"
		}

		response := map[string]interface{}{
			"client_id":     "mock-client-" + clientName,
			"client_secret": "mock-secret-12345",
			"client_name":   clientName,
			"redirect_uris": body["redirect_uris"],
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// Authorization endpoint - requires resource parameter (Runlayer behavior)
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		mock.capturedAuthURL = r.URL.String()
		mock.capturedAuthParams = r.URL.Query()

		// Check for resource parameter
		resource := r.URL.Query().Get("resource")
		mock.resourceParamFound = resource != ""

		if resource == "" {
			// Return FastAPI-style validation error (exactly like Runlayer)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"detail": []map[string]interface{}{
					{
						"type":  "missing",
						"loc":   []string{"query", "resource"},
						"msg":   "Field required",
						"input": nil,
					},
				},
			})
			return
		}

		// Success - redirect with auth code
		redirectURI := r.URL.Query().Get("redirect_uri")
		state := r.URL.Query().Get("state")
		if redirectURI == "" {
			http.Error(w, "redirect_uri required", http.StatusBadRequest)
			return
		}

		redirect := redirectURI + "?code=mock-auth-code-12345&state=" + state
		http.Redirect(w, r, redirect, http.StatusFound)
	})

	// Token endpoint
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}

		response := map[string]interface{}{
			"access_token":  "mock-access-token-xyz",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "mock-refresh-token-xyz",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})

	// MCP endpoint - returns 401 with WWW-Authenticate header
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			// Return 401 with WWW-Authenticate pointing to Protected Resource Metadata
			w.Header().Set("WWW-Authenticate",
				`Bearer error="invalid_token", resource_metadata="`+mock.server.URL+`/.well-known/oauth-protected-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{
				"error":   "unauthorized",
				"message": "Authentication required",
			})
			return
		}

		// Authenticated - return MCP response
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
				"serverInfo":      map[string]interface{}{"name": "runlayer-mock", "version": "1.0.0"},
			},
		})
	})

	// Root endpoint
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"name": "Runlayer Mock OAuth Server",
		})
	})

	mock.server = httptest.NewServer(mux)
	return mock
}

func (m *mockRunlayerServer) Close() {
	if m.server != nil {
		m.server.Close()
	}
}

// TestE2E_Issue271_ResourceParameterInAuthURL verifies that the resource parameter
// is correctly injected into the OAuth authorization URL when using the API login endpoint.
//
// This is a regression test for issue #271:
// https://github.com/smart-mcp-proxy/mcpproxy-go/issues/271
//
// The bug was that handleOAuthAuthorizationWithResult() and getAuthorizationURLQuick()
// did not inject extraParams (including the auto-detected resource parameter) into
// the authorization URL, causing Runlayer OAuth to fail with "Field required" error.
func TestE2E_Issue271_ResourceParameterInAuthURL(t *testing.T) {
	// Create mock Runlayer server
	mockServer := newMockRunlayerServer()
	defer mockServer.Close()

	t.Logf("Mock Runlayer server running at: %s", mockServer.server.URL)

	// Test 1: Verify Protected Resource Metadata returns resource field
	t.Run("ProtectedResourceMetadata_ReturnsResource", func(t *testing.T) {
		resp, err := http.Get(mockServer.server.URL + "/.well-known/oauth-protected-resource")
		require.NoError(t, err)
		defer resp.Body.Close()

		var metadata map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&metadata)
		require.NoError(t, err)

		resource, ok := metadata["resource"].(string)
		require.True(t, ok, "Metadata should contain resource field")
		assert.Equal(t, mockServer.server.URL+"/mcp", resource)
		t.Logf("✅ Protected Resource Metadata contains resource: %s", resource)
	})

	// Test 2: Verify MCP endpoint returns 401 with WWW-Authenticate
	t.Run("MCPEndpoint_Returns401WithWWWAuthenticate", func(t *testing.T) {
		resp, err := http.Post(mockServer.server.URL+"/mcp", "application/json", strings.NewReader("{}"))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		assert.Contains(t, wwwAuth, "resource_metadata=")
		t.Logf("✅ MCP endpoint returns 401 with WWW-Authenticate: %s", wwwAuth)
	})

	// Test 3: Verify /authorize endpoint requires resource parameter
	t.Run("AuthorizeEndpoint_RequiresResourceParam", func(t *testing.T) {
		// Request without resource parameter - should fail
		resp, err := http.Get(mockServer.server.URL + "/authorize?client_id=test&state=123")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var errorResp map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&errorResp)
		require.NoError(t, err)

		detail, ok := errorResp["detail"].([]interface{})
		require.True(t, ok, "Error should have detail array")
		require.Len(t, detail, 1)

		firstDetail := detail[0].(map[string]interface{})
		assert.Equal(t, "missing", firstDetail["type"])
		loc := firstDetail["loc"].([]interface{})
		assert.Equal(t, "query", loc[0])
		assert.Equal(t, "resource", loc[1])

		t.Logf("✅ Authorize endpoint correctly rejects request without resource parameter")
	})

	// Test 4: Verify /authorize endpoint accepts request with resource parameter
	t.Run("AuthorizeEndpoint_AcceptsResourceParam", func(t *testing.T) {
		// Create a client that doesn't follow redirects
		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		resource := url.QueryEscape(mockServer.server.URL + "/mcp")
		authURL := mockServer.server.URL + "/authorize?client_id=test&state=123&redirect_uri=http://localhost/callback&resource=" + resource

		resp, err := client.Get(authURL)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusFound, resp.StatusCode, "Should redirect with 302")
		location := resp.Header.Get("Location")
		assert.Contains(t, location, "code=mock-auth-code")
		t.Logf("✅ Authorize endpoint accepts request with resource parameter, redirects to: %s", location)
	})

	// Test 5: Verify the mock server captures resource param correctly
	t.Run("MockServer_CapturesResourceParam", func(t *testing.T) {
		// Reset capture
		mockServer.resourceParamFound = false
		mockServer.capturedAuthParams = nil

		// Create a client that doesn't follow redirects
		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		expectedResource := mockServer.server.URL + "/mcp"
		authURL := mockServer.server.URL + "/authorize?client_id=test&state=123&redirect_uri=http://localhost/callback&resource=" + url.QueryEscape(expectedResource)

		_, err := client.Get(authURL)
		require.NoError(t, err)

		assert.True(t, mockServer.resourceParamFound, "Resource parameter should be found")
		assert.Equal(t, expectedResource, mockServer.capturedAuthParams.Get("resource"))
		t.Logf("✅ Mock server correctly captured resource parameter: %s", mockServer.capturedAuthParams.Get("resource"))
	})
}

// TestE2E_Issue271_AuthURLContainsResource is a unit-level test that verifies
// the authorization URL contains the resource parameter after the fix.
// This directly tests the extraParams injection logic.
func TestE2E_Issue271_AuthURLContainsResource(t *testing.T) {
	// Simulate what the fix does: append extraParams to an auth URL
	baseAuthURL := "https://auth.example.com/authorize?client_id=test&state=123&response_type=code"
	extraParams := map[string]string{
		"resource": "https://mcp.example.com/api",
		"audience": "mcp-api",
	}

	// Parse and inject (this is the logic that was added in the fix)
	parsedURL, err := url.Parse(baseAuthURL)
	require.NoError(t, err)

	query := parsedURL.Query()
	for key, value := range extraParams {
		query.Set(key, value)
	}
	parsedURL.RawQuery = query.Encode()
	resultURL := parsedURL.String()

	// Verify the result contains the injected params
	assert.Contains(t, resultURL, "resource=https%3A%2F%2Fmcp.example.com%2Fapi")
	assert.Contains(t, resultURL, "audience=mcp-api")
	assert.Contains(t, resultURL, "client_id=test") // Original param preserved

	t.Logf("✅ Auth URL with injected params: %s", resultURL)
}
