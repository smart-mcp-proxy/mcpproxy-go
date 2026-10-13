package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// A login completed through oauth.callback_base_url must persist the HTTPS
// redirect URI it was registered with, not the private loopback listener.
// Otherwise the next connect compares the stored (loopback) URI with the
// expected HTTPS one and clears a valid DCR registration after every login.
func TestLoginCompletion_RemoteCallbackPersistsEffectiveRedirectURI(t *testing.T) {
	f := newStaleDCRFixture(t, oauthserver.ErrorMode{}, "login-remote-redirect")
	c := f.client(t, nil) // DCR mode

	serverCfg := &config.ServerConfig{Name: f.name, URL: f.srv.MCPURL, OAuth: &config.OAuthConfig{
		Scopes: []string{"read"}, CallbackBaseURL: "https://proxy.example",
	}}
	_, err := oauth.CreateOAuthConfigWithLogger(serverCfg, f.db, zap.NewNop())
	require.NoError(t, err)
	cb, ok := oauth.GetCallbackServer(f.name)
	require.True(t, ok)
	want := "https://proxy.example/oauth/callback/" + f.name
	require.Equal(t, want, cb.EffectiveRedirectURI())
	require.NotEqual(t, want, cb.RedirectURI, "the loopback listener keeps its own URI")

	h, code, state, verifier := loginPairHandler(t, f, oauth.NewPersistentTokenStore(f.name, f.srv.MCPURL, f.db))
	stored, err := c.exchangeAuthorizationCode(context.Background(), h, cb, code, state, verifier)
	require.NoError(t, err)
	require.True(t, stored)

	rec, err := f.db.GetOAuthToken(f.key)
	require.NoError(t, err)
	require.Equal(t, h.GetClientID(), rec.ClientID)
	assert.Equal(t, want, rec.RedirectURI, "completion must store the redirect URI the provider registered")
	assert.Equal(t, cb.Port, rec.CallbackPort)

	// The registration survives the next connect: nothing clears it.
	next, err := oauth.CreateOAuthConfigWithLogger(serverCfg, f.db, zap.NewNop())
	require.NoError(t, err)
	assert.Equal(t, rec.ClientID, next.ClientID, "a login's DCR registration must survive the next connect")
}
