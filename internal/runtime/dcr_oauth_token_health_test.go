package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// DCR persists a client registration before a login obtains an access token.
// A zero expiry makes a real token long-lived, but cannot make a registration
// authenticated. The server projection must agree with PersistentTokenStore.
func TestGetAllServers_DCRRequiresAccessToken(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		mode := "autodiscovery"
		if explicit {
			mode = "explicit"
		}
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name          string
				accessToken   string
				refreshToken  string
				expiresAt     time.Time
				authenticated bool
				status        string
				valid         bool
			}{
				{name: "DCR only", status: "none"},
				{name: "no access token with expiry and refresh", refreshToken: "synthetic-refresh", expiresAt: time.Now().Add(time.Hour), status: "none"},
				{name: "non-expiring token", accessToken: "synthetic-access", authenticated: true, status: "authenticated", valid: true},
				{name: "unexpired token", accessToken: "synthetic-access", expiresAt: time.Now().Add(time.Hour), authenticated: true, status: "authenticated", valid: true},
				{name: "expired token", accessToken: "synthetic-access", expiresAt: time.Now().Add(-time.Hour), authenticated: true, status: "expired"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					rt := newStaleTokenRuntime(t)
					sc := &config.ServerConfig{
						Name: "dcr-fixture", URL: "https://mcp.example.invalid/mcp",
						Protocol: "http", Enabled: true,
					}
					if explicit {
						sc.OAuth = &config.OAuthConfig{PKCEEnabled: true}
					}
					db := rt.storageManager.GetBoltDB()
					key := oauth.GenerateServerKey(sc.Name, sc.URL)
					require.NoError(t, db.UpdateOAuthClientCredentials(key, "synthetic-client", "", 0, ""))
					record, err := db.GetOAuthToken(key)
					require.NoError(t, err)
					if tc.accessToken != "" || tc.refreshToken != "" || !tc.expiresAt.IsZero() {
						record.AccessToken = tc.accessToken
						record.RefreshToken = tc.refreshToken
						record.ExpiresAt = tc.expiresAt
						require.NoError(t, db.SaveOAuthToken(record))
					}
					if tc.accessToken == "" {
						store := oauth.NewPersistentTokenStore(sc.Name, sc.URL, db)
						token, err := store.GetToken(context.Background())
						require.ErrorIs(t, err, transport.ErrNoToken)
						require.Nil(t, token)
					}
					rt.Supervisor().StateView().UpdateServer(sc.Name, func(s *stateview.ServerStatus) {
						s.Name, s.Config, s.Enabled, s.State = sc.Name, sc, true, "pending_auth"
					})

					server := findServer(t, rt, sc.Name)
					assert.Equal(t, tc.authenticated, server["authenticated"])
					assert.Equal(t, tc.status, server["oauth_status"])
					oauthConfig, ok := server["oauth"].(map[string]interface{})
					require.True(t, ok, "a client registration still identifies an OAuth server")
					assert.Equal(t, tc.valid, oauthConfig["token_valid"] == true)
					if tc.accessToken == "" {
						assert.NotContains(t, server, "token_expires_at")
						assert.NotContains(t, oauthConfig, "token_expires_at")
						hs, ok := server["health"].(*contracts.HealthStatus)
						require.True(t, ok)
						assert.Equal(t, health.ActionLogin, hs.Action)
					}
					stored, err := db.GetOAuthToken(key)
					require.NoError(t, err)
					assert.Equal(t, "synthetic-client", stored.ClientID, "status reads preserve the registration")
					assert.Equal(t, tc.accessToken, stored.AccessToken)
				})
			}
		})
	}
}
