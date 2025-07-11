package upstream

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/prompt"
)

func TestOAuthAutoDiscovery(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name           string
		serverConfig   *config.ServerConfig
		mockResponses  map[string]string
		expectedError  string
		expectedConfig *config.OAuthConfig
	}{
		{
			name: "successful auto-discovery with OpenID Connect",
			serverConfig: &config.ServerConfig{
				Name:     "test-server",
				URL:      "https://api.example.com/mcp",
				Protocol: "http",
				OAuth: &config.OAuthConfig{
					AutoDiscovery: &config.OAuthAutoDiscovery{
						Enabled:           true,
						PromptForClientID: false,
						AutoDeviceFlow:    true,
					},
					ClientID: "test-client-id",
				},
			},
			mockResponses: map[string]string{
				"/.well-known/oauth-protected-resource": `{
					"authorization_servers": ["SERVER_URL"],
					"resource": "https://api.example.com"
				}`,
				"/.well-known/openid-configuration": `{
					"issuer": "SERVER_URL",
					"authorization_endpoint": "SERVER_URL/oauth/authorize",
					"token_endpoint": "SERVER_URL/oauth/token",
					"scopes_supported": ["read", "write", "openid"]
				}`,
			},
			expectedConfig: &config.OAuthConfig{
				FlowType:              "authorization_code", // Local deployment uses auth code flow
				AuthorizationEndpoint: "SERVER_URL/oauth/authorize",
				TokenEndpoint:         "SERVER_URL/oauth/token",
				DeviceEndpoint:        "SERVER_URL/device/code", // Inferred from generic pattern
				ClientID:              "test-client-id",
				Scopes:                []string{"read", "write", "openid"},
				AutoDiscovery: &config.OAuthAutoDiscovery{
					Enabled:           true,
					PromptForClientID: false,
					AutoDeviceFlow:    true,
				},
			},
		},
		{
			name: "successful auto-discovery with OAuth authorization server",
			serverConfig: &config.ServerConfig{
				Name:     "test-server",
				URL:      "https://api.example.com/mcp",
				Protocol: "http",
				OAuth: &config.OAuthConfig{
					AutoDiscovery: &config.OAuthAutoDiscovery{
						Enabled:           true,
						PromptForClientID: false,
						AutoDeviceFlow:    true,
					},
					ClientID: "test-client-id",
				},
			},
			mockResponses: map[string]string{
				"/.well-known/oauth-protected-resource": `{
					"authorization_servers": ["SERVER_URL"],
					"resource": "https://api.example.com"
				}`,
				"/.well-known/oauth-authorization-server": `{
					"issuer": "SERVER_URL",
					"authorization_endpoint": "SERVER_URL/oauth/authorize",
					"token_endpoint": "SERVER_URL/oauth/token",
					"scopes_supported": ["read", "write"]
				}`,
			},
			expectedConfig: &config.OAuthConfig{
				FlowType:              "authorization_code", // Local deployment uses auth code flow
				AuthorizationEndpoint: "SERVER_URL/oauth/authorize",
				TokenEndpoint:         "SERVER_URL/oauth/token",
				DeviceEndpoint:        "SERVER_URL/device/code", // Inferred from generic pattern
				ClientID:              "test-client-id",
				Scopes:                []string{"read", "write"},
				AutoDiscovery: &config.OAuthAutoDiscovery{
					Enabled:           true,
					PromptForClientID: false,
					AutoDeviceFlow:    true,
				},
			},
		},
		{
			name: "auto-discovery with fallback to default endpoints",
			serverConfig: &config.ServerConfig{
				Name:     "fallback-server",
				URL:      "https://api.example.com/mcp",
				Protocol: "http",
				OAuth: &config.OAuthConfig{
					AutoDiscovery: &config.OAuthAutoDiscovery{
						Enabled:           true,
						PromptForClientID: false,
						AutoDeviceFlow:    true,
					},
					ClientID: "test-client-id",
				},
			},
			mockResponses: map[string]string{
				// No well-known endpoints available - should use defaults
			},
			expectedConfig: &config.OAuthConfig{
				FlowType:              "authorization_code", // Local deployment uses auth code flow
				AuthorizationEndpoint: "SERVER_URL/authorize",
				TokenEndpoint:         "SERVER_URL/token",
				DeviceEndpoint:        "SERVER_URL/device/code", // Inferred from generic pattern
				ClientID:              "test-client-id",
				Scopes:                nil, // No scopes discovered
				AutoDiscovery: &config.OAuthAutoDiscovery{
					Enabled:           true,
					PromptForClientID: false,
					AutoDeviceFlow:    true,
				},
			},
		},
		{
			name: "disabled auto-discovery",
			serverConfig: &config.ServerConfig{
				Name:     "test-server",
				URL:      "https://api.example.com/mcp",
				Protocol: "http",
				OAuth: &config.OAuthConfig{
					AutoDiscovery: &config.OAuthAutoDiscovery{
						Enabled: false,
					},
					ClientID: "test-client-id",
				},
			},
			mockResponses: map[string]string{},
			expectedConfig: &config.OAuthConfig{
				FlowType: "",
				ClientID: "test-client-id",
				Scopes:   nil,
				AutoDiscovery: &config.OAuthAutoDiscovery{
					Enabled: false,
				},
			},
		},
		{
			name: "unsupported protocol",
			serverConfig: &config.ServerConfig{
				Name:     "test-server",
				URL:      "stdio://python",
				Protocol: "stdio",
				OAuth: &config.OAuthConfig{
					AutoDiscovery: &config.OAuthAutoDiscovery{
						Enabled: true,
					},
				},
			},
			mockResponses: map[string]string{},
			expectedError: "OAuth auto-discovery only supported for HTTP protocols",
		},
		{
			name: "auto-discovery enabled by default when no OAuth config",
			serverConfig: &config.ServerConfig{
				Name:     "test-server",
				URL:      "https://api.example.com/mcp",
				Protocol: "http",
				OAuth:    nil, // No OAuth config provided
			},
			mockResponses: map[string]string{
				"/.well-known/openid-configuration": `{
					"issuer": "SERVER_URL",
					"authorization_endpoint": "SERVER_URL/oauth/authorize",
					"token_endpoint": "SERVER_URL/oauth/token",
					"scopes_supported": ["read", "write"]
				}`,
			},
			expectedConfig: &config.OAuthConfig{
				FlowType:              "authorization_code",     // Local deployment uses auth code flow
				AuthorizationEndpoint: "SERVER_URL/authorize",   // Default fallback endpoint
				TokenEndpoint:         "SERVER_URL/token",       // Default fallback endpoint
				DeviceEndpoint:        "SERVER_URL/device/code", // Inferred endpoint
				ClientID:              "",                       // Not prompted during discovery anymore
				Scopes:                nil,                      // No scopes discovered in fallback
				AutoDiscovery: &config.OAuthAutoDiscovery{
					Enabled:           true,
					PromptForClientID: false, // Default changed to false
					AutoDeviceFlow:    true,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var server *httptest.Server

			// Create mock server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Handle different paths
				path := r.URL.Path
				if path == "/.well-known/oauth-protected-resource" {
					if response, ok := tt.mockResponses[path]; ok {
						// Replace SERVER_URL with actual server URL
						response = strings.ReplaceAll(response, "SERVER_URL", server.URL)
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						fmt.Fprint(w, response)
						return
					}
				}

				// Handle auth server paths
				if path == "/.well-known/openid-configuration" {
					if response, ok := tt.mockResponses[path]; ok {
						// Replace SERVER_URL with actual server URL
						response = strings.ReplaceAll(response, "SERVER_URL", server.URL)
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						fmt.Fprint(w, response)
						return
					}
				}

				if path == "/.well-known/oauth-authorization-server" {
					if response, ok := tt.mockResponses[path]; ok {
						// Replace SERVER_URL with actual server URL
						response = strings.ReplaceAll(response, "SERVER_URL", server.URL)
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusOK)
						fmt.Fprint(w, response)
						return
					}
				}

				// HEAD requests for device endpoint testing
				if r.Method == "HEAD" && (path == "/device/code" || path == "/oauth/device/code") {
					w.WriteHeader(http.StatusOK)
					return
				}

				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()

			// Update server config URL to use test server
			if tt.serverConfig.Protocol != "stdio" {
				tt.serverConfig.URL = server.URL + "/mcp"
			}

			// Create client
			client, err := NewClient("test-id", tt.serverConfig, logger, nil, nil)
			require.NoError(t, err)

			// Set mock prompter
			mockPrompter := prompt.NewMockPrompter()
			// Set up mock response for client ID prompt if needed
			if tt.serverConfig.OAuth == nil || (tt.serverConfig.OAuth != nil && tt.serverConfig.OAuth.ClientID == "") {
				mockPrompter.SetResponse(fmt.Sprintf("OAuth client ID required for server '%s'.\nPlease enter your OAuth client ID: ", tt.serverConfig.Name), "test-client-id")
			}
			client.SetPrompter(mockPrompter)

			// Perform auto-discovery
			ctx := context.Background()
			err = client.performOAuthAutoDiscovery(ctx)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)

			// Verify configuration - replace SERVER_URL in expected config
			expectedAuthEndpoint := strings.ReplaceAll(tt.expectedConfig.AuthorizationEndpoint, "SERVER_URL", server.URL)
			expectedTokenEndpoint := strings.ReplaceAll(tt.expectedConfig.TokenEndpoint, "SERVER_URL", server.URL)
			expectedDeviceEndpoint := strings.ReplaceAll(tt.expectedConfig.DeviceEndpoint, "SERVER_URL", server.URL)

			assert.Equal(t, tt.expectedConfig.FlowType, client.config.OAuth.FlowType)
			assert.Equal(t, expectedAuthEndpoint, client.config.OAuth.AuthorizationEndpoint)
			assert.Equal(t, expectedTokenEndpoint, client.config.OAuth.TokenEndpoint)
			assert.Equal(t, expectedDeviceEndpoint, client.config.OAuth.DeviceEndpoint)
			assert.Equal(t, tt.expectedConfig.ClientID, client.config.OAuth.ClientID)
			assert.Equal(t, tt.expectedConfig.Scopes, client.config.OAuth.Scopes)
			assert.Equal(t, tt.expectedConfig.AutoDiscovery.Enabled, client.config.OAuth.AutoDiscovery.Enabled)
		})
	}
}

func TestClientIDPrompting(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name           string
		clientID       string
		promptResponse string
		expectedError  string
		expectedID     string
	}{
		{
			name:           "successful prompt",
			clientID:       "",
			promptResponse: "my-client-id",
			expectedID:     "my-client-id",
		},
		{
			name:           "empty response",
			clientID:       "",
			promptResponse: "",
			expectedError:  "client ID cannot be empty",
		},
		{
			name:       "existing client ID",
			clientID:   "existing-id",
			expectedID: "existing-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serverConfig := &config.ServerConfig{
				Name:     "test-server",
				URL:      "https://api.example.com/mcp",
				Protocol: "http",
				OAuth: &config.OAuthConfig{
					ClientID: tt.clientID,
					AutoDiscovery: &config.OAuthAutoDiscovery{
						Enabled:           true,
						PromptForClientID: false, // Default changed to false
						AutoDeviceFlow:    true,
					},
				},
			}

			client, err := NewClient("test-id", serverConfig, logger, nil, nil)
			require.NoError(t, err)

			// Set mock prompter
			mockPrompter := prompt.NewMockPrompter()
			if tt.clientID == "" {
				mockPrompter.SetResponse(fmt.Sprintf("OAuth client ID required for server '%s'.\nPlease enter your OAuth client ID: ", serverConfig.Name), tt.promptResponse)
			}
			client.SetPrompter(mockPrompter)

			// Test prompting only if client ID is empty
			if tt.clientID == "" {
				err = client.promptForClientID()

				if tt.expectedError != "" {
					assert.Error(t, err)
					assert.Contains(t, err.Error(), tt.expectedError)
					return
				}

				require.NoError(t, err)
			}

			assert.Equal(t, tt.expectedID, client.config.OAuth.ClientID)
		})
	}
}

func TestInferDeviceEndpoint(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name         string
		authEndpoint string
		expected     string
		setupMock    func() *httptest.Server
	}{
		{
			name:         "GitHub",
			authEndpoint: "https://github.com/login/oauth/authorize",
			expected:     "https://github.com/login/device/code",
		},
		{
			name:         "Google",
			authEndpoint: "https://accounts.google.com/oauth/authorize",
			expected:     "https://oauth2.googleapis.com/device/code",
		},
		{
			name:         "Microsoft",
			authEndpoint: "https://login.microsoftonline.com/common/oauth2/authorize",
			expected:     "https://login.microsoftonline.com/common/oauth2/v2.0/devicecode",
		},
		{
			name:         "unknown provider with mock server",
			authEndpoint: "SERVER_URL/oauth/authorize",
			expected:     "SERVER_URL/device/code",
			setupMock: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/device/code" {
						w.WriteHeader(http.StatusOK)
						return
					}
					w.WriteHeader(http.StatusNotFound)
				}))
			},
		},
		{
			name:         "unknown provider no endpoints",
			authEndpoint: "SERVER_URL/oauth/authorize",
			expected:     "",
			setupMock: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}))
			},
		},
		{
			name:         "invalid URL",
			authEndpoint: "invalid-url",
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{logger: logger}

			authEndpoint := tt.authEndpoint
			expected := tt.expected

			// Setup mock server if needed
			if tt.setupMock != nil {
				server := tt.setupMock()
				defer server.Close()

				authEndpoint = strings.ReplaceAll(authEndpoint, "SERVER_URL", server.URL)
				expected = strings.ReplaceAll(expected, "SERVER_URL", server.URL)
			}

			result := client.inferDeviceEndpoint(authEndpoint)
			assert.Equal(t, expected, result)
		})
	}
}

func TestSelectDefaultScopes(t *testing.T) {
	logger := zap.NewNop()
	client := &Client{logger: logger}

	tests := []struct {
		name            string
		supportedScopes []string
		expectedScopes  []string
	}{
		{
			name:            "common scopes",
			supportedScopes: []string{"read", "write", "admin", "user"},
			expectedScopes:  []string{"read", "write", "user"},
		},
		{
			name:            "OpenID scopes",
			supportedScopes: []string{"openid", "profile", "email", "phone"},
			expectedScopes:  []string{"openid", "profile", "email"},
		},
		{
			name:            "GitHub scopes",
			supportedScopes: []string{"repo", "user", "gist", "notifications"},
			expectedScopes:  []string{"user", "repo"},
		},
		{
			name:            "no preferred scopes",
			supportedScopes: []string{"custom1", "custom2", "custom3", "custom4"},
			expectedScopes:  []string{"custom1", "custom2", "custom3"},
		},
		{
			name:            "fewer than max scopes",
			supportedScopes: []string{"custom1", "custom2"},
			expectedScopes:  []string{"custom1", "custom2"},
		},
		{
			name:            "empty supported scopes",
			supportedScopes: []string{},
			expectedScopes:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.selectDefaultScopes(tt.supportedScopes)
			assert.Equal(t, tt.expectedScopes, result)
		})
	}
}

func TestDefaultAutoDiscoveryConfig(t *testing.T) {
	logger := zap.NewNop()

	serverConfig := &config.ServerConfig{
		Name:     "test-server",
		URL:      "https://api.example.com/mcp",
		Protocol: "http",
		OAuth: &config.OAuthConfig{
			ClientID: "test-client-id", // Provide client ID to avoid prompting
		},
	}

	client, err := NewClient("test-id", serverConfig, logger, nil, nil)
	require.NoError(t, err)

	// Mock server that returns 404 for all requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client.config.URL = server.URL + "/mcp"

	// Set mock prompter to avoid prompting issues
	mockPrompter := prompt.NewMockPrompter()
	client.SetPrompter(mockPrompter)

	// Perform auto-discovery
	ctx := context.Background()
	err = client.performOAuthAutoDiscovery(ctx)

	// Should not error, but should set defaults
	assert.NoError(t, err)

	// Verify default configuration was set
	assert.NotNil(t, client.config.OAuth.AutoDiscovery)
	assert.True(t, client.config.OAuth.AutoDiscovery.Enabled)
	assert.False(t, client.config.OAuth.AutoDiscovery.PromptForClientID) // Default changed to false
	assert.True(t, client.config.OAuth.AutoDiscovery.AutoDeviceFlow)
}

func TestEndpointTesting(t *testing.T) {
	logger := zap.NewNop()
	client := &Client{logger: logger}

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/exists" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/method-not-allowed" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tests := []struct {
		name     string
		endpoint string
		expected bool
	}{
		{
			name:     "exists",
			endpoint: server.URL + "/exists",
			expected: true,
		},
		{
			name:     "method not allowed",
			endpoint: server.URL + "/method-not-allowed",
			expected: true,
		},
		{
			name:     "not found",
			endpoint: server.URL + "/not-found",
			expected: false,
		},
		{
			name:     "invalid URL",
			endpoint: "invalid-url",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.testEndpointExists(tt.endpoint)
			assert.Equal(t, tt.expected, result)
		})
	}
}
