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

func TestDiscoverProtectedResourceMetadata_ReturnsFullMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"resource": "https://example.com/mcp",
			"scopes_supported": ["mcp.read", "mcp.write"],
			"authorization_servers": ["https://auth.example.com"]
		}`))
	}))
	defer server.Close()

	metadata, err := DiscoverProtectedResourceMetadata(server.URL, 5*time.Second)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if metadata == nil {
		t.Fatalf("Expected metadata but got nil")
	}
	if metadata.Resource != "https://example.com/mcp" {
		t.Errorf("Resource = %q, want %q", metadata.Resource, "https://example.com/mcp")
	}
	if len(metadata.ScopesSupported) != 2 {
		t.Errorf("len(ScopesSupported) = %d, want 2", len(metadata.ScopesSupported))
	}
	if len(metadata.ScopesSupported) > 0 && metadata.ScopesSupported[0] != "mcp.read" {
		t.Errorf("ScopesSupported[0] = %q, want %q", metadata.ScopesSupported[0], "mcp.read")
	}
}
