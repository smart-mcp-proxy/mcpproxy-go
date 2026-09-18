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

// connectBroker returns a complete oauth_connect block.
func connectBroker() *AuthBrokerConfig {
	return &AuthBrokerConfig{
		Mode:                  AuthBrokerModeOAuthConnect,
		AuthorizationEndpoint: "https://idp/authorize",
		TokenEndpoint:         "https://idp/token",
	}
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
		require.NoError(t, connectBroker().Validate())
	})
}

func TestAuthBroker_ValidHTTPBroker(t *testing.T) {
	server := &ServerConfig{
		Name:     "github",
		Protocol: "http",
		URL:      "https://api.github.com/mcp",
		AuthBroker: &AuthBrokerConfig{
			Mode:                  AuthBrokerModeOAuthConnect,
			AuthorizationEndpoint: "https://idp.example.com/authorize",
			TokenEndpoint:         "https://idp.example.com/token",
			Resource:              "https://api.github.com",
			Scopes:                []string{"repo"},
			ClientID:              "client-123",
			ClientSecret:          "secret-xyz",
		},
	}
	cfg := baseValidConfig(server)
	require.NoError(t, cfg.Validate())
}

// Spec 107 FR-039: Validate never mutates the block. There is no default to
// apply any more (the header/header_format leaves are gone), so the block
// must come out of Validate exactly as it went in.
func TestAuthBroker_ValidateIsNonMutating(t *testing.T) {
	broker := connectBroker()
	broker.Scopes = []string{"repo"}
	before := *broker.Clone()
	server := &ServerConfig{Name: "github", Protocol: "http", URL: "https://api.github.com/mcp", AuthBroker: broker}
	require.NoError(t, baseValidConfig(server).Validate())
	assert.Equal(t, before, *server.AuthBroker)
}

func TestAuthBroker_RejectedOnStdio(t *testing.T) {
	server := &ServerConfig{
		Name:       "local",
		Protocol:   "stdio",
		Command:    "npx",
		Args:       []string{"some-mcp"},
		AuthBroker: connectBroker(),
	}
	cfg := baseValidConfig(server)
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported in this phase")
}

func TestAuthBroker_RejectedOnImpliedStdio(t *testing.T) {
	// No protocol + Command set => stdio by inference; broker must be rejected.
	server := &ServerConfig{
		Name:       "local-implied",
		Command:    "npx",
		AuthBroker: connectBroker(),
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
			AuthBroker: &AuthBrokerConfig{TokenEndpoint: "https://idp/token", AuthorizationEndpoint: "https://idp/authorize"},
		})
		require.Error(t, cfg.Validate())
	})
	t.Run("missing token_endpoint", func(t *testing.T) {
		cfg := baseValidConfig(&ServerConfig{
			Name: "github", Protocol: "http", URL: "https://api.github.com/mcp",
			AuthBroker: &AuthBrokerConfig{Mode: AuthBrokerModeOAuthConnect, AuthorizationEndpoint: "https://idp/authorize"},
		})
		require.Error(t, cfg.Validate())
	})
}

// The accepted mode set is exactly {oauth_connect} (Spec 107 FR-032).
func TestAuthBroker_AcceptedModeSet(t *testing.T) {
	cfg := baseValidConfig(&ServerConfig{
		Name: "s", Protocol: "streamable-http", URL: "https://x/mcp",
		AuthBroker: connectBroker(),
	})
	require.NoError(t, cfg.Validate())
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
			"mode": "oauth_connect",
			"authorization_endpoint": "https://login.microsoftonline.com/tenant/oauth2/v2.0/authorize",
			"token_endpoint": "https://login.microsoftonline.com/tenant/oauth2/v2.0/token",
			"resource": "api://upstream",
			"scopes": ["user.read"],
			"client_id": "abc",
			"client_secret": "def"
		}
	}`
	var sc ServerConfig
	require.NoError(t, json.Unmarshal([]byte(raw), &sc))
	require.NotNil(t, sc.AuthBroker)
	assert.Equal(t, AuthBrokerModeOAuthConnect, sc.AuthBroker.Mode)
	assert.Equal(t, "api://upstream", sc.AuthBroker.Resource)
	assert.Equal(t, []string{"user.read"}, sc.AuthBroker.Scopes)
}

// Clone must not alias the Scopes backing array (CopyServerConfig relies on it).
func TestAuthBroker_CloneDoesNotAlias(t *testing.T) {
	src := connectBroker()
	src.Scopes = []string{"a", "b"}
	dst := src.Clone()
	dst.Scopes[0] = "changed"
	assert.Equal(t, "a", src.Scopes[0])
	assert.Nil(t, (*AuthBrokerConfig)(nil).Clone())
}
