package oauth

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newUnreachableUpstream returns an httptest server that answers 404 to every
// request. OAuth metadata discovery degrades gracefully against it, which keeps
// these tests hermetic and fast while still exercising the real
// createOAuthConfigInternal() code path.
func newUnreachableUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// reserveLoopbackPort picks a currently-free loopback port and releases it, so a
// pinned redirect_uri in the test can name a port the callback server can bind.
func reserveLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func stopCallbackServer(t *testing.T, serverName string) {
	t.Helper()
	t.Cleanup(func() {
		if mgr := GetGlobalCallbackManager(); mgr != nil {
			_ = mgr.StopCallbackServer(serverName)
		}
	})
}

func TestParsePinnedRedirectURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantPort int
		wantErr  string
	}{
		{name: "loopback ipv4", uri: "http://127.0.0.1:54108/oauth/callback", wantPort: 54108},
		{name: "localhost", uri: "http://localhost:8123/oauth/callback", wantPort: 8123},
		{name: "ipv6 loopback", uri: "http://[::1]:8123/oauth/callback", wantPort: 8123},
		{name: "surrounding whitespace tolerated", uri: "  http://127.0.0.1:54108/oauth/callback  ", wantPort: 54108},
		{name: "empty", uri: "", wantErr: "empty"},
		{name: "not a url", uri: "://nope", wantErr: "not a valid URL"},
		{name: "https scheme", uri: "https://127.0.0.1:54108/oauth/callback", wantErr: "http scheme"},
		{name: "non-loopback host", uri: "http://example.com:54108/oauth/callback", wantErr: "loopback host"},
		{name: "wrong path", uri: "http://127.0.0.1:54108/callback", wantErr: "callback path"},
		{name: "no port", uri: "http://127.0.0.1/oauth/callback", wantErr: "explicit port"},
		{name: "port zero", uri: "http://127.0.0.1:0/oauth/callback", wantErr: "invalid port"},
		{name: "query string", uri: "http://127.0.0.1:54108/oauth/callback?x=1", wantErr: "query string or fragment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, err := ParsePinnedRedirectURI(tt.uri)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPort, port)
		})
	}
}

// TestCreateOAuthConfig_HonorsPinnedRedirectURI verifies part (c) of the fix:
// a configured oauth.redirect_uri pins the loopback callback port AND is sent to
// the provider verbatim (GitHub OAuth Apps match the callback URL exactly).
func TestCreateOAuthConfig_HonorsPinnedRedirectURI(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	port := reserveLoopbackPort(t)
	pinned := fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", port)

	serverName := "pinned-redirect-server"
	stopCallbackServer(t, serverName)

	serverConfig := &config.ServerConfig{
		Name: serverName,
		URL:  upstream.URL + "/mcp",
		OAuth: &config.OAuthConfig{
			ClientID:    "static-client",
			RedirectURI: pinned,
		},
	}

	oauthConfig := CreateOAuthConfig(serverConfig, store)
	require.NotNil(t, oauthConfig, "pinned redirect_uri should produce a usable OAuth config")

	assert.Equal(t, pinned, oauthConfig.RedirectURI,
		"the configured redirect_uri must be sent to the provider verbatim")

	callbackServer, ok := GetCallbackServer(serverName)
	require.True(t, ok, "callback server should be running")
	assert.Equal(t, port, callbackServer.Port,
		"callback server must listen on the pinned port")
}

// TestCreateOAuthConfig_MalformedPinnedRedirectURI verifies that a malformed
// oauth.redirect_uri fails loudly instead of silently falling back to a random
// port (which the operator would never discover until the provider rejects it).
func TestCreateOAuthConfig_MalformedPinnedRedirectURI(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "malformed-redirect-server"
	stopCallbackServer(t, serverName)

	serverConfig := &config.ServerConfig{
		Name: serverName,
		URL:  upstream.URL + "/mcp",
		OAuth: &config.OAuthConfig{
			ClientID:    "static-client",
			RedirectURI: "https://example.com/oauth/callback",
		},
	}

	oauthConfig := CreateOAuthConfig(serverConfig, store)
	assert.Nil(t, oauthConfig, "malformed redirect_uri must abort OAuth config creation")

	_, ok := GetCallbackServer(serverName)
	assert.False(t, ok, "no callback server should be started for a malformed pin")
}

// TestCreateOAuthConfig_StaticClientCredentialsSurviveMissingPort verifies part
// (b) of the fix: a statically configured client is never "re-registered", so
// the legacy-DCR clearing branch must not wipe its stored record.
func TestCreateOAuthConfig_StaticClientCredentialsSurviveMissingPort(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "static-client-server"
	stopCallbackServer(t, serverName)

	serverURL := upstream.URL + "/mcp"
	serverKey := GenerateServerKey(serverName, serverURL)

	// Simulate the state left behind by a login that persisted the static
	// client_id but no callback port.
	require.NoError(t, store.UpdateOAuthClientCredentials(serverKey, "static-client", "static-secret", 0))

	serverConfig := &config.ServerConfig{
		Name: serverName,
		URL:  serverURL,
		OAuth: &config.OAuthConfig{
			ClientID:     "static-client",
			ClientSecret: "static-secret",
		},
	}

	oauthConfig := CreateOAuthConfig(serverConfig, store)
	require.NotNil(t, oauthConfig)

	storedClientID, storedSecret, _, err := store.GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	assert.Equal(t, "static-client", storedClientID,
		"static OAuth credentials must never be cleared for re-registration")
	assert.Equal(t, "static-secret", storedSecret)
}

// TestCreateOAuthConfig_LegacyDCRCredentialsStillCleared guards the original
// Spec 022 behavior for genuine DCR clients (no static client_id in config).
func TestCreateOAuthConfig_LegacyDCRCredentialsStillCleared(t *testing.T) {
	upstream := newUnreachableUpstream(t)
	store := setupTestStorage(t)

	serverName := "legacy-dcr-server"
	stopCallbackServer(t, serverName)

	serverURL := upstream.URL + "/mcp"
	serverKey := GenerateServerKey(serverName, serverURL)

	require.NoError(t, store.UpdateOAuthClientCredentials(serverKey, "dcr-client", "dcr-secret", 0))

	serverConfig := &config.ServerConfig{
		Name: serverName,
		URL:  serverURL,
	}

	oauthConfig := CreateOAuthConfig(serverConfig, store)
	require.NotNil(t, oauthConfig)

	storedClientID, _, _, err := store.GetOAuthClientCredentials(serverKey)
	require.NoError(t, err)
	assert.Empty(t, storedClientID,
		"legacy DCR credentials without a stored port should still be cleared")
}
