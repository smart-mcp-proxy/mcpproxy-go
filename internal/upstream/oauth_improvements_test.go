package upstream

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

// Test PKCE Support

func TestGenerateCodeVerifier(t *testing.T) {
	verifier, err := generateCodeVerifier()
	require.NoError(t, err)

	// Verify verifier is base64url encoded
	decoded, err := base64.RawURLEncoding.DecodeString(verifier)
	require.NoError(t, err)

	// Should be 32 bytes (256 bits)
	assert.Equal(t, 32, len(decoded))

	// Each call should generate unique verifier
	verifier2, err := generateCodeVerifier()
	require.NoError(t, err)
	assert.NotEqual(t, verifier, verifier2)
}

func TestGenerateCodeChallenge(t *testing.T) {
	verifier := "test_code_verifier_123456789"
	challenge := generateCodeChallenge(verifier)

	// Verify challenge is correct SHA256 base64url encoding
	hash := sha256.Sum256([]byte(verifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(hash[:])

	assert.Equal(t, expectedChallenge, challenge)
}

func TestGeneratePKCEParams(t *testing.T) {
	logger := zap.NewNop()
	client := &Client{logger: logger}

	params, err := client.generatePKCEParams()
	require.NoError(t, err)

	assert.NotEmpty(t, params.CodeVerifier)
	assert.NotEmpty(t, params.CodeChallenge)
	assert.Equal(t, "S256", params.Method)

	// Verify challenge is correctly derived from verifier
	expectedChallenge := generateCodeChallenge(params.CodeVerifier)
	assert.Equal(t, expectedChallenge, params.CodeChallenge)
}

func TestShouldUsePKCE(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name        string
		oauthConfig *config.OAuthConfig
		expected    bool
	}{
		{
			name: "explicitly enabled",
			oauthConfig: &config.OAuthConfig{
				UsePKCE:      true,
				ClientSecret: "secret",
			},
			expected: true,
		},
		{
			name: "public client (no secret)",
			oauthConfig: &config.OAuthConfig{
				UsePKCE:      false,
				ClientSecret: "",
			},
			expected: true,
		},
		{
			name: "authorization code flow",
			oauthConfig: &config.OAuthConfig{
				UsePKCE:      false,
				ClientSecret: "secret",
				FlowType:     config.OAuthFlowAuthorizationCode,
			},
			expected: true,
		},
		{
			name: "confidential client with other flow",
			oauthConfig: &config.OAuthConfig{
				UsePKCE:      false,
				ClientSecret: "secret",
				FlowType:     config.OAuthFlowDeviceCode,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				logger: logger,
				config: &config.ServerConfig{
					OAuth: tt.oauthConfig,
				},
			}

			result := client.shouldUsePKCE()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test Dynamic Client Registration (DCR)

func TestBuildRegistrationRequest(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name         string
		oauthConfig  *config.OAuthConfig
		deployment   DeploymentType
		expectedType string
	}{
		{
			name: "local deployment",
			oauthConfig: &config.OAuthConfig{
				DynamicClientRegistration: &config.DCRConfig{
					ClientName: "Test App",
					ClientURI:  "https://example.com",
					Contacts:   []string{"admin@example.com"},
				},
			},
			deployment:   DeploymentLocal,
			expectedType: "native",
		},
		{
			name: "remote deployment",
			oauthConfig: &config.OAuthConfig{
				DynamicClientRegistration: &config.DCRConfig{
					ClientName: "Test App",
					ClientURI:  "https://example.com",
				},
			},
			deployment:   DeploymentRemote,
			expectedType: "web",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				logger:         logger,
				deploymentType: tt.deployment,
				config: &config.ServerConfig{
					OAuth: tt.oauthConfig,
				},
			}

			req := client.buildRegistrationRequest()

			assert.Equal(t, tt.expectedType, req.ApplicationType)
			assert.Equal(t, "none", req.TokenEndpointAuthMethod)
			assert.Contains(t, req.GrantTypes, "authorization_code")
			assert.Contains(t, req.GrantTypes, "refresh_token")
			assert.Contains(t, req.ResponseTypes, "code")
			assert.NotEmpty(t, req.RedirectURIs)
		})
	}
}

func TestPerformDynamicClientRegistration(t *testing.T) {
	logger := zap.NewNop()

	// Mock DCR server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		response := config.ClientRegistrationResponse{
			ClientID:     "generated-client-id",
			ClientSecret: "generated-client-secret",
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	oauthConfig := &config.OAuthConfig{
		DynamicClientRegistration: &config.DCRConfig{
			Enabled: true,
		},
		RegistrationEndpoint: server.URL,
	}

	client := &Client{
		logger: logger,
		config: &config.ServerConfig{
			OAuth: oauthConfig,
		},
	}

	ctx := context.Background()
	err := client.performDynamicClientRegistration(ctx)
	require.NoError(t, err)

	assert.Equal(t, "generated-client-id", oauthConfig.ClientID)
	assert.Equal(t, "generated-client-secret", oauthConfig.ClientSecret)
}

// Test Deployment Type Detection

func TestDetectDeploymentType(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name      string
		publicURL string
		expected  DeploymentType
	}{
		{
			name:      "local deployment",
			publicURL: "",
			expected:  DeploymentLocal,
		},
		{
			name:      "remote deployment with public URL",
			publicURL: "https://mcpproxy.example.com",
			expected:  DeploymentRemote,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				logger: logger,
				config: &config.ServerConfig{
					PublicURL: tt.publicURL,
				},
			}

			result := client.detectDeploymentType()
			assert.Equal(t, tt.expected, result)

			// Test caching
			result2 := client.detectDeploymentType()
			assert.Equal(t, result, result2)
		})
	}
}

// Test OAuth Flow Selection

func TestSelectOAuthFlow(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name           string
		flowType       string
		deploymentType DeploymentType
		expected       string
	}{
		{
			name:           "manual flow selection",
			flowType:       config.OAuthFlowDeviceCode,
			deploymentType: DeploymentLocal,
			expected:       config.OAuthFlowDeviceCode,
		},
		{
			name:           "auto selection - local",
			flowType:       config.OAuthFlowAuto,
			deploymentType: DeploymentLocal,
			expected:       config.OAuthFlowAuthorizationCode,
		},
		{
			name:           "auto selection - remote",
			flowType:       config.OAuthFlowAuto,
			deploymentType: DeploymentRemote,
			expected:       config.OAuthFlowAuthorizationCode,
		},
		{
			name:           "auto selection - headless",
			flowType:       config.OAuthFlowAuto,
			deploymentType: DeploymentHeadless,
			expected:       config.OAuthFlowDeviceCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				logger:         logger,
				deploymentType: tt.deploymentType,
				config: &config.ServerConfig{
					OAuth: &config.OAuthConfig{
						FlowType: tt.flowType,
					},
				},
			}

			result := client.selectOAuthFlow()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test OAuth Pending State with Cached Tools

func TestOAuthPendingStateManagement(t *testing.T) {
	logger := zap.NewNop()
	client := &Client{
		logger: logger,
		config: &config.ServerConfig{},
	}

	// Initially not pending
	assert.False(t, client.isOAuthPending())
	assert.Equal(t, "", client.getConnectionState())

	// Set OAuth pending
	client.setOAuthPending(true, nil)
	assert.True(t, client.isOAuthPending())

	// Set OAuth pending with error
	testErr := assert.AnError
	client.setOAuthPending(true, testErr)
	assert.True(t, client.isOAuthPending())
	assert.Equal(t, testErr, client.oauthError)

	// Clear OAuth pending
	client.setOAuthPending(false, nil)
	assert.False(t, client.isOAuthPending())
	assert.Nil(t, client.oauthError)
}

func TestCachedToolsManagement(t *testing.T) {
	logger := zap.NewNop()
	client := &Client{
		logger: logger,
		config: &config.ServerConfig{},
	}

	// Initially no cached tools
	tools := client.getCachedTools()
	assert.Empty(t, tools)

	// Cache some tools
	testTools := []*config.ToolMetadata{
		{Name: "test-tool-1", Description: "Test tool 1"},
		{Name: "test-tool-2", Description: "Test tool 2"},
	}

	client.setCachedTools(testTools, 5*time.Minute)

	// Should return cached tools
	cachedTools := client.getCachedTools()
	assert.Len(t, cachedTools, 2)
	assert.Equal(t, "test-tool-1", cachedTools[0].Name)
	assert.Equal(t, "test-tool-2", cachedTools[1].Name)

	// Clear cache
	client.clearCachedTools()
	tools = client.getCachedTools()
	assert.Empty(t, tools)
}

func TestCachedToolsExpiration(t *testing.T) {
	logger := zap.NewNop()
	client := &Client{
		logger: logger,
		config: &config.ServerConfig{},
	}

	testTools := []*config.ToolMetadata{
		{Name: "test-tool", Description: "Test tool"},
	}

	// Cache tools with very short expiration
	client.setCachedTools(testTools, 1*time.Millisecond)

	// Should be available immediately
	tools := client.getCachedTools()
	assert.Len(t, tools, 1)

	// Wait for expiration
	time.Sleep(2 * time.Millisecond)

	// Should be empty after expiration
	tools = client.getCachedTools()
	assert.Empty(t, tools)
}

// Test Redirect URI Generation

func TestGetRedirectURIs(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name           string
		deploymentType DeploymentType
		publicURL      string
		expectedCount  int
		expectedLocal  bool
		expectedPublic bool
	}{
		{
			name:           "local deployment",
			deploymentType: DeploymentLocal,
			publicURL:      "",
			expectedCount:  2,
			expectedLocal:  true,
			expectedPublic: false,
		},
		{
			name:           "remote deployment",
			deploymentType: DeploymentRemote,
			publicURL:      "https://mcpproxy.example.com",
			expectedCount:  1,
			expectedLocal:  false,
			expectedPublic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				logger:            logger,
				deploymentType:    tt.deploymentType,
				detectedPublicURL: tt.publicURL,
				config: &config.ServerConfig{
					PublicURL: tt.publicURL,
				},
			}

			redirectURIs := client.getRedirectURIs()

			assert.Len(t, redirectURIs, tt.expectedCount)

			if tt.expectedLocal {
				found := false
				for _, uri := range redirectURIs {
					if strings.Contains(uri, "127.0.0.1") || strings.Contains(uri, "localhost") {
						found = true
						break
					}
				}
				assert.True(t, found, "Expected to find localhost/127.0.0.1 redirect URI")
			}

			if tt.expectedPublic {
				found := false
				for _, uri := range redirectURIs {
					if strings.Contains(uri, tt.publicURL) {
						found = true
						break
					}
				}
				assert.True(t, found, "Expected to find public URL redirect URI")
			}
		})
	}
}

// Test Lazy Authentication Logic

func TestShouldUseLazyAuth(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name        string
		oauthConfig *config.OAuthConfig
		expected    bool
	}{
		{
			name: "lazy auth enabled",
			oauthConfig: &config.OAuthConfig{
				LazyAuth: true,
			},
			expected: true,
		},
		{
			name: "lazy auth disabled",
			oauthConfig: &config.OAuthConfig{
				LazyAuth: false,
			},
			expected: false,
		},
		{
			name:        "no oauth config",
			oauthConfig: nil,
			expected:    false, // Default is now false
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				logger: logger,
				config: &config.ServerConfig{
					OAuth: tt.oauthConfig,
				},
			}

			result := client.shouldUseLazyAuth()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCreateAutoOAuthConfig(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name       string
		serverURL  string
		deployment DeploymentType
		expected   func(*config.OAuthConfig) bool
	}{
		{
			name:       "cloudflare server - local deployment",
			serverURL:  "https://dns-analytics.mcp.cloudflare.com/sse",
			deployment: DeploymentLocal,
			expected: func(cfg *config.OAuthConfig) bool {
				return cfg.LazyAuth == true &&
					cfg.AutoDiscovery.Enabled == true &&
					cfg.DynamicClientRegistration.Enabled == true &&
					cfg.FlowType == config.OAuthFlowAuthorizationCode &&
					cfg.DynamicClientRegistration.ClientName == defaultClientName
			},
		},
		{
			name:       "cloudflare server - headless deployment",
			serverURL:  "https://builds.mcp.cloudflare.com/sse",
			deployment: DeploymentHeadless,
			expected: func(cfg *config.OAuthConfig) bool {
				return cfg.LazyAuth == true &&
					cfg.AutoDiscovery.Enabled == true &&
					cfg.DynamicClientRegistration.Enabled == true &&
					cfg.FlowType == config.OAuthFlowDeviceCode &&
					cfg.DynamicClientRegistration.ClientName == defaultClientName
			},
		},
		{
			name:       "generic server",
			serverURL:  "https://api.example.com/mcp",
			deployment: DeploymentLocal,
			expected: func(cfg *config.OAuthConfig) bool {
				return cfg.LazyAuth == true &&
					cfg.AutoDiscovery.Enabled == true &&
					cfg.DynamicClientRegistration.Enabled == true &&
					cfg.FlowType == config.OAuthFlowAuto
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				logger:         logger,
				deploymentType: tt.deployment,
				config: &config.ServerConfig{
					URL: tt.serverURL,
				},
			}

			cfg := client.createAutoOAuthConfig()

			assert.True(t, tt.expected(cfg), "OAuth config doesn't match expected values")
		})
	}
}

// Test OAuth Notification System

func TestSendOAuthNotification(t *testing.T) {
	logger := zap.NewNop()

	// Test with minimal notification config
	client := &Client{
		logger: logger,
		config: &config.ServerConfig{
			OAuth: &config.OAuthConfig{
				NotificationMethods: []string{"log"},
			},
		},
	}

	// This should not panic - just test that it executes without error
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("sendOAuthNotification panicked: %v", r)
		}
	}()

	client.sendOAuthNotification("test", map[string]interface{}{
		"message": "test notification",
	})
}

// Test Helper Functions

func TestGetStringValue(t *testing.T) {
	testMap := map[string]interface{}{
		"string_key": "test_value",
		"int_key":    123,
		"nil_key":    nil,
	}

	assert.Equal(t, "test_value", getStringValue(testMap, "string_key"))
	assert.Equal(t, "", getStringValue(testMap, "int_key"))
	assert.Equal(t, "", getStringValue(testMap, "nil_key"))
	assert.Equal(t, "", getStringValue(testMap, "missing_key"))
}

func TestGetDurationValue(t *testing.T) {
	testMap := map[string]interface{}{
		"int_key":    300,
		"float_key":  300.5,
		"string_key": "not_a_number",
		"nil_key":    nil,
	}

	defaultDuration := 10 * time.Second

	assert.Equal(t, 300*time.Second, getDurationValue(testMap, "int_key", defaultDuration))
	assert.Equal(t, 300*time.Second, getDurationValue(testMap, "float_key", defaultDuration))
	assert.Equal(t, defaultDuration, getDurationValue(testMap, "string_key", defaultDuration))
	assert.Equal(t, defaultDuration, getDurationValue(testMap, "nil_key", defaultDuration))
	assert.Equal(t, defaultDuration, getDurationValue(testMap, "missing_key", defaultDuration))
}

// Test Authorization Code Flow Redirect URI Selection

func TestAuthorizationCodeFlowRedirectURI(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name           string
		oauthConfig    *config.OAuthConfig
		expectDynamic  bool
		expectStatic   bool
		staticRedirect string
	}{
		{
			name: "no configured redirect URI - should use dynamic",
			oauthConfig: &config.OAuthConfig{
				AuthorizationEndpoint: "https://example.com/oauth/authorize",
				TokenEndpoint:         "https://example.com/oauth/token",
			},
			expectDynamic: true,
			expectStatic:  false,
		},
		{
			name: "explicit redirect URI - should use static",
			oauthConfig: &config.OAuthConfig{
				AuthorizationEndpoint: "https://example.com/oauth/authorize",
				TokenEndpoint:         "https://example.com/oauth/token",
				RedirectURI:           "http://localhost:3000/auth/callback",
			},
			expectDynamic:  false,
			expectStatic:   true,
			staticRedirect: "http://localhost:3000/auth/callback",
		},
		{
			name: "configured redirect URIs - should use static",
			oauthConfig: &config.OAuthConfig{
				AuthorizationEndpoint: "https://example.com/oauth/authorize",
				TokenEndpoint:         "https://example.com/oauth/token",
				RedirectURIs:          []string{"http://localhost:4000/callback", "http://127.0.0.1:4000/callback"},
			},
			expectDynamic:  false,
			expectStatic:   true,
			staticRedirect: "http://localhost:4000/callback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				logger:         logger,
				deploymentType: DeploymentLocal,
				config: &config.ServerConfig{
					OAuth: tt.oauthConfig,
				},
			}

			// This test would require actual HTTP server setup to fully test handleAuthorizationCodeFlow
			// For now, test the redirect URI selection logic by checking getOAuthConfig and related methods
			oauth := client.getOAuthConfig()

			if tt.expectDynamic {
				// For dynamic case, there should be no explicit redirect URIs configured
				assert.Empty(t, oauth.RedirectURI, "RedirectURI should be empty for dynamic flow")
				assert.Empty(t, oauth.RedirectURIs, "RedirectURIs should be empty for dynamic flow")
			}

			if tt.expectStatic {
				// For static case, we should have explicitly configured redirect URIs
				redirectURIs := client.getRedirectURIs()
				assert.NotEmpty(t, redirectURIs, "Should have redirect URIs configured")
				if tt.staticRedirect != "" {
					assert.Contains(t, redirectURIs, tt.staticRedirect, "Should contain the expected static redirect URI")
				}
			}
		})
	}
}
