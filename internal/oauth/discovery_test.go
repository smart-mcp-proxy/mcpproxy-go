package oauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractResourceMetadataURL(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected string
	}{
		{
			name:     "Valid header with resource_metadata",
			header:   `Bearer error="invalid_request", resource_metadata="https://api.example.com/.well-known/oauth-protected-resource"`,
			expected: "https://api.example.com/.well-known/oauth-protected-resource",
		},
		{
			name:     "GitHub MCP header format",
			header:   `Bearer error="invalid_request", error_description="No access token was provided", resource_metadata="https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly"`,
			expected: "https://api.githubcopilot.com/.well-known/oauth-protected-resource/mcp/readonly",
		},
		{
			name:     "Header without resource_metadata",
			header:   `Bearer error="invalid_token"`,
			expected: "",
		},
		{
			name:     "Empty header",
			header:   "",
			expected: "",
		},
		{
			name:     "Malformed header - missing closing quote",
			header:   `Bearer resource_metadata="https://api.example.com`,
			expected: "",
		},
		{
			name:     "Malformed header - missing opening quote",
			header:   `Bearer resource_metadata=https://api.example.com"`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractResourceMetadataURL(tt.header)
			if result != tt.expected {
				t.Errorf("ExtractResourceMetadataURL(%q) = %q, want %q", tt.header, result, tt.expected)
			}
		})
	}
}

func TestDiscoverScopesFromProtectedResource(t *testing.T) {
	tests := []struct {
		name           string
		responseCode   int
		responseBody   string
		expectedScopes []string
		expectError    bool
	}{
		{
			name:         "Valid metadata with scopes",
			responseCode: 200,
			responseBody: `{
				"resource": "https://api.example.com/mcp",
				"resource_name": "Example MCP Server",
				"authorization_servers": ["https://auth.example.com"],
				"scopes_supported": ["repo", "user:email", "read:org"]
			}`,
			expectedScopes: []string{"repo", "user:email", "read:org"},
			expectError:    false,
		},
		{
			name:         "Valid metadata with empty scopes",
			responseCode: 200,
			responseBody: `{
				"resource": "https://api.example.com/mcp",
				"scopes_supported": []
			}`,
			expectedScopes: []string{},
			expectError:    false,
		},
		{
			name:         "404 response",
			responseCode: 404,
			responseBody: `Not Found`,
			expectedScopes: nil,
			expectError:    true,
		},
		{
			name:         "Invalid JSON",
			responseCode: 200,
			responseBody: `{invalid json}`,
			expectedScopes: nil,
			expectError:    true,
		},
		{
			name:         "GitHub MCP metadata format",
			responseCode: 200,
			responseBody: `{
				"resource_name": "GitHub MCP Server",
				"resource": "https://api.githubcopilot.com/mcp/readonly",
				"authorization_servers": ["https://github.com/login/oauth"],
				"bearer_methods_supported": ["header"],
				"scopes_supported": ["gist", "notifications", "public_repo", "repo", "user:email"]
			}`,
			expectedScopes: []string{"gist", "notifications", "public_repo", "repo", "user:email"},
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock HTTP server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.responseCode)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			// Test discovery
			scopes, err := DiscoverScopesFromProtectedResource(server.URL, 5*time.Second)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if len(scopes) != len(tt.expectedScopes) {
					t.Errorf("Scope count mismatch: got %d, want %d", len(scopes), len(tt.expectedScopes))
				}
				for i, scope := range scopes {
					if scope != tt.expectedScopes[i] {
						t.Errorf("Scope[%d] = %q, want %q", i, scope, tt.expectedScopes[i])
					}
				}
			}
		})
	}
}

func TestDiscoverScopesFromProtectedResource_Timeout(t *testing.T) {
	// Create a server that takes longer than the timeout
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(200)
		w.Write([]byte(`{"scopes_supported": ["repo"]}`))
	}))
	defer server.Close()

	// Use 1 second timeout (server takes 3 seconds)
	scopes, err := DiscoverScopesFromProtectedResource(server.URL, 1*time.Second)

	if err == nil {
		t.Errorf("Expected timeout error but got nil")
	}
	if scopes != nil {
		t.Errorf("Expected nil scopes on timeout, got %v", scopes)
	}
}

func TestDiscoverScopesFromAuthorizationServer(t *testing.T) {
	tests := []struct {
		name           string
		responseCode   int
		responseBody   string
		expectedScopes []string
		expectError    bool
	}{
		{
			name:         "Valid metadata with scopes",
			responseCode: 200,
			responseBody: `{
				"issuer": "https://auth.example.com",
				"authorization_endpoint": "https://auth.example.com/authorize",
				"token_endpoint": "https://auth.example.com/token",
				"scopes_supported": ["openid", "email", "profile"],
				"response_types_supported": ["code"]
			}`,
			expectedScopes: []string{"openid", "email", "profile"},
			expectError:    false,
		},
		{
			name:         "Valid metadata with empty scopes",
			responseCode: 200,
			responseBody: `{
				"issuer": "https://auth.example.com",
				"authorization_endpoint": "https://auth.example.com/authorize",
				"token_endpoint": "https://auth.example.com/token",
				"scopes_supported": [],
				"response_types_supported": ["code"]
			}`,
			expectedScopes: []string{},
			expectError:    false,
		},
		{
			name:         "404 response",
			responseCode: 404,
			responseBody: `Not Found`,
			expectedScopes: nil,
			expectError:    true,
		},
		{
			name:         "Invalid JSON",
			responseCode: 200,
			responseBody: `{invalid json}`,
			expectedScopes: nil,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock HTTP server at /.well-known/oauth-authorization-server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Check that the correct path is requested
				if r.URL.Path != "/.well-known/oauth-authorization-server" {
					t.Errorf("Unexpected path: %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				w.WriteHeader(tt.responseCode)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			// Test discovery
			scopes, err := DiscoverScopesFromAuthorizationServer(server.URL, 5*time.Second)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if len(scopes) != len(tt.expectedScopes) {
					t.Errorf("Scope count mismatch: got %d, want %d", len(scopes), len(tt.expectedScopes))
				}
				for i, scope := range scopes {
					if scope != tt.expectedScopes[i] {
						t.Errorf("Scope[%d] = %q, want %q", i, scope, tt.expectedScopes[i])
					}
				}
			}
		})
	}
}

func TestDiscoverScopesFromAuthorizationServer_Timeout(t *testing.T) {
	// Create a server that takes longer than the timeout
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(200)
		w.Write([]byte(`{"scopes_supported": ["openid"]}`))
	}))
	defer server.Close()

	// Use 1 second timeout (server takes 3 seconds)
	scopes, err := DiscoverScopesFromAuthorizationServer(server.URL, 1*time.Second)

	if err == nil {
		t.Errorf("Expected timeout error but got nil")
	}
	if scopes != nil {
		t.Errorf("Expected nil scopes on timeout, got %v", scopes)
	}
}

// T002: Test DiscoverProtectedResourceMetadata returns full struct
func TestDiscoverProtectedResourceMetadata_ReturnsFullStruct(t *testing.T) {
	tests := []struct {
		name             string
		responseCode     int
		responseBody     string
		expectedResource string
		expectedScopes   []string
		expectedAuthSrvs []string
		expectError      bool
	}{
		{
			name:         "Valid metadata with all fields",
			responseCode: 200,
			responseBody: `{
				"resource": "https://api.example.com/mcp",
				"resource_name": "Example MCP Server",
				"authorization_servers": ["https://auth.example.com"],
				"scopes_supported": ["repo", "user:email", "read:org"],
				"bearer_methods_supported": ["header"]
			}`,
			expectedResource: "https://api.example.com/mcp",
			expectedScopes:   []string{"repo", "user:email", "read:org"},
			expectedAuthSrvs: []string{"https://auth.example.com"},
			expectError:      false,
		},
		{
			name:         "Runlayer-style metadata",
			responseCode: 200,
			responseBody: `{
				"resource": "https://oauth.runlayer.com/api/v1/proxy/abc123/mcp",
				"authorization_servers": ["https://oauth.runlayer.com"],
				"scopes_supported": ["mcp"]
			}`,
			expectedResource: "https://oauth.runlayer.com/api/v1/proxy/abc123/mcp",
			expectedScopes:   []string{"mcp"},
			expectedAuthSrvs: []string{"https://oauth.runlayer.com"},
			expectError:      false,
		},
		{
			name:         "Metadata without resource field",
			responseCode: 200,
			responseBody: `{
				"authorization_servers": ["https://auth.example.com"],
				"scopes_supported": ["read"]
			}`,
			expectedResource: "",
			expectedScopes:   []string{"read"},
			expectedAuthSrvs: []string{"https://auth.example.com"},
			expectError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.responseCode)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			metadata, err := DiscoverProtectedResourceMetadata(server.URL, 5*time.Second)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if metadata.Resource != tt.expectedResource {
				t.Errorf("Resource = %q, want %q", metadata.Resource, tt.expectedResource)
			}

			if len(metadata.ScopesSupported) != len(tt.expectedScopes) {
				t.Errorf("Scopes count = %d, want %d", len(metadata.ScopesSupported), len(tt.expectedScopes))
			}

			if len(metadata.AuthorizationServers) != len(tt.expectedAuthSrvs) {
				t.Errorf("AuthorizationServers count = %d, want %d", len(metadata.AuthorizationServers), len(tt.expectedAuthSrvs))
			}
		})
	}
}

// T003: Test DiscoverProtectedResourceMetadata handles errors
func TestDiscoverProtectedResourceMetadata_HandlesError(t *testing.T) {
	tests := []struct {
		name         string
		responseCode int
		responseBody string
	}{
		{
			name:         "404 response",
			responseCode: 404,
			responseBody: `Not Found`,
		},
		{
			name:         "500 response",
			responseCode: 500,
			responseBody: `Internal Server Error`,
		},
		{
			name:         "Invalid JSON",
			responseCode: 200,
			responseBody: `{invalid json}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.responseCode)
				w.Write([]byte(tt.responseBody))
			}))
			defer server.Close()

			metadata, err := DiscoverProtectedResourceMetadata(server.URL, 5*time.Second)

			if err == nil {
				t.Errorf("Expected error but got nil")
			}
			if metadata != nil {
				t.Errorf("Expected nil metadata on error, got %+v", metadata)
			}
		})
	}
}

func TestDiscoverProtectedResourceMetadata_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(200)
		w.Write([]byte(`{"resource": "https://example.com"}`))
	}))
	defer server.Close()

	metadata, err := DiscoverProtectedResourceMetadata(server.URL, 1*time.Second)

	if err == nil {
		t.Errorf("Expected timeout error but got nil")
	}
	if metadata != nil {
		t.Errorf("Expected nil metadata on timeout, got %+v", metadata)
	}
}

// Test RFC 8414 URL building for authorization server metadata discovery
func TestBuildRFC8414MetadataURLs(t *testing.T) {
	tests := []struct {
		name         string
		authServerURL string
		expectedURLs []string
	}{
		{
			name:          "Simple base URL without path",
			authServerURL: "https://auth.example.com",
			expectedURLs:  []string{"https://auth.example.com/.well-known/oauth-authorization-server"},
		},
		{
			name:          "Base URL with trailing slash",
			authServerURL: "https://auth.example.com/",
			expectedURLs:  []string{"https://auth.example.com/.well-known/oauth-authorization-server"},
		},
		{
			name:          "Smithery-style URL with path (RFC 8414 compliant)",
			authServerURL: "https://auth.smithery.ai/googledrive",
			expectedURLs: []string{
				"https://auth.smithery.ai/.well-known/oauth-authorization-server/googledrive",
				"https://auth.smithery.ai/googledrive/.well-known/oauth-authorization-server",
			},
		},
		{
			name:          "URL with multi-level path",
			authServerURL: "https://auth.example.com/path1/path2/issuer",
			expectedURLs: []string{
				"https://auth.example.com/.well-known/oauth-authorization-server/path1/path2/issuer",
				"https://auth.example.com/path1/path2/issuer/.well-known/oauth-authorization-server",
			},
		},
		{
			name:          "URL with path and trailing slash",
			authServerURL: "https://auth.smithery.ai/googledrive/",
			expectedURLs: []string{
				"https://auth.smithery.ai/.well-known/oauth-authorization-server/googledrive",
				"https://auth.smithery.ai/googledrive/.well-known/oauth-authorization-server",
			},
		},
		{
			name:          "GitHub OAuth URL without path",
			authServerURL: "https://github.com",
			expectedURLs:  []string{"https://github.com/.well-known/oauth-authorization-server"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urls := buildRFC8414MetadataURLs(tt.authServerURL)

			if len(urls) != len(tt.expectedURLs) {
				t.Errorf("URL count mismatch: got %d, want %d", len(urls), len(tt.expectedURLs))
				t.Errorf("Got URLs: %v", urls)
				t.Errorf("Want URLs: %v", tt.expectedURLs)
				return
			}

			for i, url := range urls {
				if url != tt.expectedURLs[i] {
					t.Errorf("URL[%d] = %q, want %q", i, url, tt.expectedURLs[i])
				}
			}
		})
	}
}

// Test discovery with fallback - simulates Smithery server behavior
func TestDiscoverAuthServerMetadataWithFallback_SmitheryStyle(t *testing.T) {
	// Simulate Smithery's behavior: RFC 8414 path works, legacy path returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Smithery uses RFC 8414 compliant path
		if r.URL.Path == "/.well-known/oauth-authorization-server/googledrive" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			w.Write([]byte(`{
				"issuer": "https://auth.smithery.ai/googledrive",
				"authorization_endpoint": "https://auth.smithery.ai/googledrive/authorize",
				"token_endpoint": "https://auth.smithery.ai/googledrive/token",
				"registration_endpoint": "https://auth.smithery.ai/googledrive/register",
				"response_types_supported": ["code"],
				"code_challenge_methods_supported": ["S256"]
			}`))
			return
		}
		// Legacy path returns 404
		if r.URL.Path == "/googledrive/.well-known/oauth-authorization-server" {
			w.WriteHeader(404)
			w.Write([]byte("Cannot GET /googledrive/.well-known/oauth-authorization-server"))
			return
		}
		w.WriteHeader(404)
		w.Write([]byte("Not found"))
	}))
	defer server.Close()

	// Use the server URL with path (simulating auth.smithery.ai/googledrive)
	authServerURL := server.URL + "/googledrive"

	metadata, discoveredURL, err := discoverAuthServerMetadataWithFallback(authServerURL, 5*time.Second)

	if err != nil {
		t.Fatalf("Expected success but got error: %v", err)
	}

	if metadata == nil {
		t.Fatal("Expected metadata but got nil")
	}

	expectedURL := server.URL + "/.well-known/oauth-authorization-server/googledrive"
	if discoveredURL != expectedURL {
		t.Errorf("Discovered URL = %q, want %q", discoveredURL, expectedURL)
	}

	if metadata.AuthorizationEndpoint != "https://auth.smithery.ai/googledrive/authorize" {
		t.Errorf("AuthorizationEndpoint = %q, want %q", metadata.AuthorizationEndpoint, "https://auth.smithery.ai/googledrive/authorize")
	}

	if metadata.TokenEndpoint != "https://auth.smithery.ai/googledrive/token" {
		t.Errorf("TokenEndpoint = %q, want %q", metadata.TokenEndpoint, "https://auth.smithery.ai/googledrive/token")
	}
}

// Test discovery with fallback - simulates legacy server behavior (current codebase test servers)
func TestDiscoverAuthServerMetadataWithFallback_LegacyStyle(t *testing.T) {
	// Simulate legacy behavior: legacy path works, RFC 8414 path returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Legacy path (path/.well-known/oauth-authorization-server) works
		if r.URL.Path == "/myserver/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			w.Write([]byte(`{
				"issuer": "https://legacy.example.com/myserver",
				"authorization_endpoint": "https://legacy.example.com/myserver/authorize",
				"token_endpoint": "https://legacy.example.com/myserver/token",
				"response_types_supported": ["code"]
			}`))
			return
		}
		// RFC 8414 path returns 404
		if r.URL.Path == "/.well-known/oauth-authorization-server/myserver" {
			w.WriteHeader(404)
			w.Write([]byte("Not found"))
			return
		}
		w.WriteHeader(404)
		w.Write([]byte("Not found"))
	}))
	defer server.Close()

	authServerURL := server.URL + "/myserver"

	metadata, discoveredURL, err := discoverAuthServerMetadataWithFallback(authServerURL, 5*time.Second)

	if err != nil {
		t.Fatalf("Expected success but got error: %v", err)
	}

	if metadata == nil {
		t.Fatal("Expected metadata but got nil")
	}

	expectedURL := server.URL + "/myserver/.well-known/oauth-authorization-server"
	if discoveredURL != expectedURL {
		t.Errorf("Discovered URL = %q, want %q", discoveredURL, expectedURL)
	}

	if metadata.AuthorizationEndpoint != "https://legacy.example.com/myserver/authorize" {
		t.Errorf("AuthorizationEndpoint = %q", metadata.AuthorizationEndpoint)
	}
}

// Test discovery with fallback - both paths fail
func TestDiscoverAuthServerMetadataWithFallback_AllFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("Not found"))
	}))
	defer server.Close()

	authServerURL := server.URL + "/myserver"

	metadata, discoveredURL, err := discoverAuthServerMetadataWithFallback(authServerURL, 5*time.Second)

	if err == nil {
		t.Fatal("Expected error but got nil")
	}

	if metadata != nil {
		t.Errorf("Expected nil metadata, got %+v", metadata)
	}

	if discoveredURL != "" {
		t.Errorf("Expected empty discoveredURL, got %q", discoveredURL)
	}

	// Error should mention the URLs tried
	if !containsSubstring(err.Error(), "/.well-known/oauth-authorization-server") {
		t.Errorf("Error should mention well-known path, got: %v", err)
	}
}

// Test discovery with fallback - metadata is incomplete (missing required fields)
func TestDiscoverAuthServerMetadataWithFallback_IncompleteMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return metadata missing required fields
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{
			"issuer": "https://auth.example.com",
			"response_types_supported": ["code"]
		}`))
	}))
	defer server.Close()

	metadata, _, err := discoverAuthServerMetadataWithFallback(server.URL, 5*time.Second)

	if err == nil {
		t.Fatal("Expected error for incomplete metadata but got nil")
	}

	if metadata != nil {
		t.Errorf("Expected nil metadata for incomplete response, got %+v", metadata)
	}

	// Error should mention missing fields
	if !containsSubstring(err.Error(), "missing required fields") {
		t.Errorf("Error should mention missing fields, got: %v", err)
	}
}

// Test discovery with simple base URL (no path)
func TestDiscoverAuthServerMetadataWithFallback_SimpleURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			w.Write([]byte(`{
				"issuer": "https://auth.example.com",
				"authorization_endpoint": "https://auth.example.com/authorize",
				"token_endpoint": "https://auth.example.com/token",
				"response_types_supported": ["code"]
			}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	metadata, discoveredURL, err := discoverAuthServerMetadataWithFallback(server.URL, 5*time.Second)

	if err != nil {
		t.Fatalf("Expected success but got error: %v", err)
	}

	if metadata == nil {
		t.Fatal("Expected metadata but got nil")
	}

	expectedURL := server.URL + "/.well-known/oauth-authorization-server"
	if discoveredURL != expectedURL {
		t.Errorf("Discovered URL = %q, want %q", discoveredURL, expectedURL)
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstringHelper(s, substr))
}

func containsSubstringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
