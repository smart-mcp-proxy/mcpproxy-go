//go:build server

package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseValidConfig returns a minimal Config that passes Validate() so individual
// tests only need to mutate the single server under test.
func baseValidConfig(server *ServerConfig) *Config {
	return &Config{
		Listen:            "127.0.0.1:8080",
		ToolsLimit:        15,
		ToolResponseLimit: 1000,
		CallToolTimeout:   Duration(60000000000),
		Servers:           []*ServerConfig{server},
	}
}

func TestAuthBrokerConfig_ApplyDefaults(t *testing.T) {
	t.Run("fills header and header_format when empty", func(t *testing.T) {
		b := &AuthBrokerConfig{Mode: AuthBrokerModeTokenExchange, TokenEndpoint: "https://idp/token"}
		b.ApplyDefaults()
		assert.Equal(t, "Authorization", b.Header)
		assert.Equal(t, "Bearer {token}", b.HeaderFormat)
	})

	t.Run("preserves custom header and header_format", func(t *testing.T) {
		b := &AuthBrokerConfig{
			Mode:          AuthBrokerModeTokenExchange,
			TokenEndpoint: "https://idp/token",
			Header:        "X-Upstream-Auth",
			HeaderFormat:  "token {token}",
		}
		b.ApplyDefaults()
		assert.Equal(t, "X-Upstream-Auth", b.Header)
		assert.Equal(t, "token {token}", b.HeaderFormat)
	})
}

func TestAuthBroker_OAuthConnectRequiresAuthorizationEndpoint(t *testing.T) {
	t.Run("missing authorization_endpoint is rejected", func(t *testing.T) {
		b := &AuthBrokerConfig{
			Mode:          AuthBrokerModeOAuthConnect,
			TokenEndpoint: "https://idp/token",
		}
		err := b.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "authorization_endpoint")
	})

	t.Run("authorization_endpoint present is accepted", func(t *testing.T) {
		b := &AuthBrokerConfig{
			Mode:                  AuthBrokerModeOAuthConnect,
			AuthorizationEndpoint: "https://idp/authorize",
			TokenEndpoint:         "https://idp/token",
		}
		require.NoError(t, b.Validate())
	})

	t.Run("authorization_endpoint is not required for token_exchange", func(t *testing.T) {
		b := &AuthBrokerConfig{
			Mode:          AuthBrokerModeTokenExchange,
			TokenEndpoint: "https://idp/token",
		}
		require.NoError(t, b.Validate())
	})
}

func TestAuthBroker_ValidHTTPBroker(t *testing.T) {
	server := &ServerConfig{
		Name:     "github",
		Protocol: "http",
		URL:      "https://api.github.com/mcp",
		AuthBroker: &AuthBrokerConfig{
			Mode:          AuthBrokerModeTokenExchange,
			TokenEndpoint: "https://idp.example.com/token",
			Resource:      "https://api.github.com",
			Scopes:        []string{"repo"},
			ClientID:      "client-123",
			ClientSecret:  "secret-xyz",
		},
	}
	cfg := baseValidConfig(server)
	require.NoError(t, cfg.Validate())

	// Defaults applied to the in-place broker after Validate().
	assert.Equal(t, "Authorization", server.AuthBroker.Header)
	assert.Equal(t, "Bearer {token}", server.AuthBroker.HeaderFormat)
}

func TestAuthBroker_RejectedOnStdio(t *testing.T) {
	server := &ServerConfig{
		Name:     "local",
		Protocol: "stdio",
		Command:  "npx",
		Args:     []string{"some-mcp"},
		AuthBroker: &AuthBrokerConfig{
			Mode:          AuthBrokerModeTokenExchange,
			TokenEndpoint: "https://idp.example.com/token",
		},
	}
	cfg := baseValidConfig(server)
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported in this phase")
}

func TestAuthBroker_RejectedOnImpliedStdio(t *testing.T) {
	// No protocol + Command set => stdio by inference; broker must be rejected.
	server := &ServerConfig{
		Name:    "local-implied",
		Command: "npx",
		AuthBroker: &AuthBrokerConfig{
			Mode:          AuthBrokerModeTokenExchange,
			TokenEndpoint: "https://idp.example.com/token",
		},
	}
	cfg := baseValidConfig(server)
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported in this phase")
}

func TestAuthBroker_InvalidMode(t *testing.T) {
	server := &ServerConfig{
		Name:     "github",
		Protocol: "http",
		URL:      "https://api.github.com/mcp",
		AuthBroker: &AuthBrokerConfig{
			Mode:          "magic",
			TokenEndpoint: "https://idp.example.com/token",
		},
	}
	cfg := baseValidConfig(server)
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode")
}

func TestAuthBroker_MissingRequiredFields(t *testing.T) {
	t.Run("missing mode", func(t *testing.T) {
		cfg := baseValidConfig(&ServerConfig{
			Name: "github", Protocol: "http", URL: "https://api.github.com/mcp",
			AuthBroker: &AuthBrokerConfig{TokenEndpoint: "https://idp/token"},
		})
		require.Error(t, cfg.Validate())
	})
	t.Run("missing token_endpoint", func(t *testing.T) {
		cfg := baseValidConfig(&ServerConfig{
			Name: "github", Protocol: "http", URL: "https://api.github.com/mcp",
			AuthBroker: &AuthBrokerConfig{Mode: AuthBrokerModeEntraOBO},
		})
		require.Error(t, cfg.Validate())
	})
}

func TestAuthBroker_AllValidModes(t *testing.T) {
	for _, mode := range []string{AuthBrokerModeTokenExchange, AuthBrokerModeEntraOBO, AuthBrokerModeOAuthConnect} {
		t.Run(mode, func(t *testing.T) {
			broker := &AuthBrokerConfig{Mode: mode, TokenEndpoint: "https://idp/token"}
			// The connect flow additionally requires the authorize endpoint.
			if mode == AuthBrokerModeOAuthConnect {
				broker.AuthorizationEndpoint = "https://idp/authorize"
			}
			cfg := baseValidConfig(&ServerConfig{
				Name: "s", Protocol: "streamable-http", URL: "https://x/mcp",
				AuthBroker: broker,
			})
			require.NoError(t, cfg.Validate())
		})
	}
}

func TestAuthBroker_NoBrokerUnaffected(t *testing.T) {
	// Servers without a broker block validate exactly as before (FR-003).
	cfg := baseValidConfig(&ServerConfig{Name: "plain", Protocol: "stdio", Command: "echo"})
	require.NoError(t, cfg.Validate())
}

func TestAuthBroker_JSONRoundTrip(t *testing.T) {
	raw := `{
		"name": "github",
		"protocol": "http",
		"url": "https://api.github.com/mcp",
		"auth_broker": {
			"mode": "entra_obo",
			"token_endpoint": "https://login.microsoftonline.com/tenant/oauth2/v2.0/token",
			"resource": "api://upstream",
			"scopes": ["user.read"],
			"client_id": "abc",
			"client_secret": "def",
			"header": "X-Auth",
			"header_format": "Bearer {token}"
		}
	}`
	var sc ServerConfig
	require.NoError(t, json.Unmarshal([]byte(raw), &sc))
	require.NotNil(t, sc.AuthBroker)
	assert.Equal(t, AuthBrokerModeEntraOBO, sc.AuthBroker.Mode)
	assert.Equal(t, "api://upstream", sc.AuthBroker.Resource)
	assert.Equal(t, []string{"user.read"}, sc.AuthBroker.Scopes)
	assert.Equal(t, "X-Auth", sc.AuthBroker.Header)
}
