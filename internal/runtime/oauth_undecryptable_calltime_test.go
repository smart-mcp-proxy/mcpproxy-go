package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// Spec 123 FR-031: a header-authenticated server with a call-time sign-in
// requirement must have its stored row probed, so health can refuse `login`
// over an undecryptable row.
func TestShouldProbeStoredOAuthRow(t *testing.T) {
	const u = "https://mcp.example.invalid/mcp"
	cases := []struct {
		name                      string
		url                       string
		storage, inPlay, callTime bool
		want                      bool
	}{
		{"token in play", u, true, true, false, true},
		{"header server, no sign-in need: exempt (GH #1172)", u, true, false, false, false},
		{"header server, call-time sign-in need: probed", u, true, false, true, true},
		{"no url", "", true, true, true, false},
		{"no storage", u, false, true, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, shouldProbeStoredOAuthRow(c.url, c.storage, c.inPlay, c.callTime))
		})
	}
}

// The exemption stays: connected header server, undecryptable row, no
// sign-in need -> healthy, row not reported.
func TestGetAllServers_HeaderServerUndecryptableRowNoSignInNeedStaysHealthy(t *testing.T) {
	spec123ClearEnv(t)
	rt := newStaleTokenRuntime(t)
	sc := headerAuthServer()
	putForeignSealedRow(t, rt, oauth.GenerateServerKey(sc.Name, sc.URL))
	markConnected(t, rt, sc, 3)

	server := findServer(t, rt, sc.Name)
	hs, ok := server["health"].(*contracts.HealthStatus)
	require.True(t, ok)
	assert.Equal(t, health.LevelHealthy, hs.Level)
	assert.NotContains(t, server, "error_code")
}
