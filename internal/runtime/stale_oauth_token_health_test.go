package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// GH #1172.
//
// An upstream that once authenticated with OAuth (via autodiscovery — no
// explicit `oauth` block) and was then switched to a static `Authorization`
// header keeps its old record in the oauth_tokens bucket: nothing removes it,
// because the config never carried an OAuth block that could "change". The
// server-list projection used to build an `oauth` object from that dead record
// and, once its expiry passed, the health calculator reported the connected,
// tool-serving server as unhealthy / "Token expired" / login — forever.
//
// A static Authorization header and OAuth are mutually exclusive on the wire
// (OAuth populates that very header), so a config that declares one has
// stopped using the other. These tests pin that the projection no longer
// consults the stored token for such a server, and that the refresh-manager
// state that rides on the same token does not resurface as "Refresh token
// expired" once the token itself is ignored (or cleared by logout).

const (
	staleTokenServerName = "cloudflare-workers"
	staleTokenServerURL  = "https://mcp.example.invalid/mcp"
)

func newStaleTokenRuntime(t *testing.T) *Runtime {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp_config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"mcpServers":[]}`), 0o600))

	cfg := &config.Config{
		DataDir: dir,
		Listen:  "127.0.0.1:0",
		Servers: []*config.ServerConfig{},
	}

	rt, err := New(cfg, cfgPath, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	return rt
}

// saveStaleOAuthToken persists an EXPIRED token with no refresh token under the
// exact storage key PersistentTokenStore uses, which is also the key the
// projection looks up.
func saveStaleOAuthToken(t *testing.T, rt *Runtime, name, url string) {
	t.Helper()

	expired := time.Now().Add(-14 * 24 * time.Hour)
	require.NoError(t, rt.storageManager.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName:  oauth.GenerateServerKey(name, url),
		DisplayName: name,
		AccessToken: "dead-access-token",
		TokenType:   "Bearer",
		ExpiresAt:   expired,
		Created:     expired.Add(-time.Hour),
		Updated:     expired.Add(-time.Hour),
	}))
}

// markConnected puts the server into the StateView exactly as the supervisor
// does for a live, tool-serving upstream.
func markConnected(t *testing.T, rt *Runtime, sc *config.ServerConfig, toolCount int) {
	t.Helper()

	rt.Supervisor().StateView().UpdateServer(sc.Name, func(s *stateview.ServerStatus) {
		s.Name = sc.Name
		s.Config = sc
		s.Enabled = true
		s.Connected = true
		s.State = "connected"
		s.ToolCount = toolCount
	})
}

func findServer(t *testing.T, rt *Runtime, name string) map[string]interface{} {
	t.Helper()

	servers, err := rt.GetAllServers()
	require.NoError(t, err)
	for _, s := range servers {
		if s["name"] == name {
			return s
		}
	}
	t.Fatalf("server %q not in GetAllServers projection", name)
	return nil
}

func headerAuthServer() *config.ServerConfig {
	return &config.ServerConfig{
		Name:     staleTokenServerName,
		URL:      staleTokenServerURL,
		Protocol: "http",
		Enabled:  true,
		Headers:  map[string]string{"Authorization": "Bearer static-api-token"},
		// No OAuth block: the reporter's servers had `"oauth": null`.
	}
}

// TestGetAllServers_StaticAuthHeaderIgnoresStaleOAuthToken is THE regression
// test for the issue's first-order symptom: connected + serving tools, yet
// unhealthy / "Token expired" / login because of a leftover token record.
func TestGetAllServers_StaticAuthHeaderIgnoresStaleOAuthToken(t *testing.T) {
	rt := newStaleTokenRuntime(t)
	sc := headerAuthServer()

	saveStaleOAuthToken(t, rt, sc.Name, sc.URL)
	markConnected(t, rt, sc, 31)

	server := findServer(t, rt, sc.Name)

	hs, ok := server["health"].(*contracts.HealthStatus)
	require.True(t, ok, "health must be the contracts.HealthStatus pointer, got %T", server["health"])
	assert.Equal(t, health.LevelHealthy, hs.Level,
		"a connected server authenticating with a static Authorization header must not be judged by a stored OAuth token")
	assert.Equal(t, health.ActionNone, hs.Action, "there is no login to do for a header-auth server")
	assert.NotEqual(t, "Token expired", hs.Summary)

	assert.Nil(t, server["oauth"],
		"the API must not synthesize an oauth object from a dead token record when the config has no OAuth")
	assert.Equal(t, false, server["authenticated"])
	assert.NotContains(t, server, "oauth_status")
	assert.NotContains(t, server, "token_expires_at")
}

// TestGetAllServers_StaticAuthHeaderIgnoresStaleRefreshState covers the second
// symptom: the RefreshManager builds a FAILED schedule out of the same stale
// record at startup ("Token expired and no refresh token available"), and that
// schedule used to surface as "Refresh token expired" the moment the token
// itself stopped driving the OAuth branch.
func TestGetAllServers_StaticAuthHeaderIgnoresStaleRefreshState(t *testing.T) {
	rt := newStaleTokenRuntime(t)
	sc := headerAuthServer()

	saveStaleOAuthToken(t, rt, sc.Name, sc.URL)

	// Startup path: the refresh manager loads every stored token and parks the
	// fully-expired one in the failed state, keyed by display name.
	require.NoError(t, rt.refreshManager.Start(context.Background()))
	t.Cleanup(rt.refreshManager.Stop)
	require.NotNil(t, rt.refreshManager.GetRefreshState(sc.Name),
		"precondition: the stale record must have produced a refresh schedule")

	markConnected(t, rt, sc, 31)

	server := findServer(t, rt, sc.Name)
	hs, ok := server["health"].(*contracts.HealthStatus)
	require.True(t, ok)
	assert.Equal(t, health.LevelHealthy, hs.Level)
	assert.NotEqual(t, "Refresh token expired", hs.Summary,
		"refresh state is a property of an OAuth token; it must not judge a server whose token is not in play")
}

// TestGetAllServers_AutodiscoveryOAuthStillReportsExpiredToken pins the scope
// of the fix: a server with NO static Authorization header (OAuth via
// autodiscovery) keeps deriving its OAuth state from the stored token, so a
// genuinely expired login is still reported.
func TestGetAllServers_AutodiscoveryOAuthStillReportsExpiredToken(t *testing.T) {
	rt := newStaleTokenRuntime(t)
	sc := &config.ServerConfig{
		Name:     staleTokenServerName,
		URL:      staleTokenServerURL,
		Protocol: "http",
		Enabled:  true,
		// A non-credential header must NOT flip the server to "static auth".
		Headers: map[string]string{"X-Client-Version": "1.0"},
	}

	saveStaleOAuthToken(t, rt, sc.Name, sc.URL)
	markConnected(t, rt, sc, 5)

	server := findServer(t, rt, sc.Name)
	hs, ok := server["health"].(*contracts.HealthStatus)
	require.True(t, ok)
	assert.Equal(t, health.LevelUnhealthy, hs.Level)
	assert.Equal(t, "Token expired", hs.Summary)
	assert.Equal(t, health.ActionLogin, hs.Action)
	assert.NotNil(t, server["oauth"], "an autodiscovery OAuth server still exposes its token state")
	assert.Equal(t, true, server["authenticated"])
}

// TestTriggerOAuthLogout_ClearsRefreshSchedule pins the issue's third
// observation: logout removed the token record but left the in-memory refresh
// schedule behind, so the server flipped from "Token expired" to "Refresh token
// expired" instead of recovering.
func TestTriggerOAuthLogout_ClearsRefreshSchedule(t *testing.T) {
	rt := newStaleTokenRuntime(t)
	sc := &config.ServerConfig{
		Name:     staleTokenServerName,
		URL:      staleTokenServerURL,
		Protocol: "http",
		Enabled:  true,
	}

	saveStaleOAuthToken(t, rt, sc.Name, sc.URL)
	require.NoError(t, rt.refreshManager.Start(context.Background()))
	t.Cleanup(rt.refreshManager.Stop)
	require.NotNil(t, rt.refreshManager.GetRefreshState(sc.Name), "precondition: schedule exists before logout")

	// Logout needs the server registered with the upstream manager; register
	// without connecting.
	require.NoError(t, rt.upstreamManager.AddServerConfig(sc.Name, sc))

	require.NoError(t, rt.TriggerOAuthLogout(sc.Name))

	// GetOAuthToken reports a miss as an error; only the nil record matters here.
	tok, _ := rt.storageManager.GetOAuthToken(oauth.GenerateServerKey(sc.Name, sc.URL))
	assert.Nil(t, tok, "logout must remove the token record")
	assert.Nil(t, rt.refreshManager.GetRefreshState(sc.Name),
		"logout must also drop the refresh schedule that rode on the removed token")
}
