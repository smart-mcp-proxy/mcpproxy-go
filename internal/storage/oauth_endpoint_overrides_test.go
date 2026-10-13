package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 113 FR-029: the four OAuth discovery overrides survive a storage round trip.
func TestUpstreamRecord_OAuthEndpointOverridesRoundTrip(t *testing.T) {
	m, err := NewManager(t.TempDir(), zaptest.NewLogger(t).Sugar())
	require.NoError(t, err)
	defer m.Close()

	in := &config.ServerConfig{
		Name: "ov", URL: "https://x.example/mcp", Protocol: "http", Enabled: true,
		OAuth: &config.OAuthConfig{
			ClientID:              "cid",
			AuthorizationEndpoint: "https://idp.example.com/authorize",
			TokenEndpoint:         "https://idp.example.com/oauth2/v1/exchange",
			RegistrationEndpoint:  "https://idp.example.com/register",
			AuthServerMetadataURL: "https://idp.example.com/.well-known/custom",
		},
	}
	require.NoError(t, m.SaveUpstreamServer(in))
	out, err := m.GetUpstreamServer("ov")
	require.NoError(t, err)
	require.NotNil(t, out.OAuth)
	assert.Equal(t, in.OAuth, out.OAuth)
}
