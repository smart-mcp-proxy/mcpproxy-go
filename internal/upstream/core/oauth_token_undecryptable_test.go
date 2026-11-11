package core

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
	"github.com/smart-mcp-proxy/mcpproxy-go/tests/oauthserver"
)

// Spec 123 T047 (FR-031, D8 inventory, US2.8): on the connection path an
// undecryptable stored row stops the OAuth strategy before any OAuth flow
// (no discovery, no DCR, no authorization request, no callback server) and
// is reported with MCPX_OAUTH_TOKEN_UNDECRYPTABLE; the row is never rewritten.

type undecryptableCoreFixture struct {
	srv  *oauthserver.ServerResult
	m    *storage.Manager
	db   *storage.BoltDB
	name string
	url  string
	key  string
}

func newUndecryptableCoreFixture(t *testing.T, name string, sse bool) *undecryptableCoreFixture {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "")
	t.Setenv("HEADLESS", "1")
	srv := oauthserver.Start(t, oauthserver.Options{})
	t.Cleanup(func() { _ = srv.Shutdown() })
	stopCallbackServerAfterTest(t, name)

	url := srv.MCPURL
	if sse {
		url = srv.SSEURL
	}
	key := oauth.GenerateServerKey(name, url)

	dir := t.TempDir()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	k, err := tokenseal.ParseKey(base64.StdEncoding.EncodeToString(raw))
	require.NoError(t, err)
	sealer, err := storage.NewBoltDB(dir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(k))
	require.NoError(t, err)
	require.NoError(t, sealer.SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName: key, DisplayName: name, AccessToken: "SPEC123-CORE-ACCESS", RefreshToken: "SPEC123-CORE-REFRESH",
		ClientID: srv.PublicClientID, ExpiresAt: time.Now().Add(-time.Minute),
	}))
	require.NoError(t, sealer.Close())

	m, err := storage.NewManager(dir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	return &undecryptableCoreFixture{srv: srv, m: m, db: m.GetBoltDB(), name: name, url: url, key: key}
}

func (f *undecryptableCoreFixture) client(t *testing.T, protocol string) *Client {
	t.Helper()
	cfg := &config.ServerConfig{
		Name: f.name, URL: f.url, Protocol: protocol, Enabled: true,
		OAuth: &config.OAuthConfig{Scopes: []string{"read"}},
	}
	c, err := NewClient(f.name, cfg, zap.NewNop(), nil, &config.Config{}, f.db, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Disconnect() })
	return c
}

func (f *undecryptableCoreFixture) raw(t *testing.T) []byte {
	t.Helper()
	var out []byte
	require.NoError(t, f.m.GetDB().View(func(tx *bbolt.Tx) error {
		out = append([]byte(nil), tx.Bucket([]byte(storage.OAuthTokenBucket)).Get([]byte(f.key))...)
		return nil
	}))
	require.NotEmpty(t, out)
	return out
}

func (f *undecryptableCoreFixture) assertNoOAuthFlow(t *testing.T) {
	t.Helper()
	assert.EqualValues(t, 0, f.srv.Server.RegistrationCount(), "no DCR round trip")
	assert.EqualValues(t, 0, f.srv.Server.AuthorizeGETCount(), "no authorization request")
	_, running := oauth.GetCallbackServer(f.name)
	assert.False(t, running, "no callback server was started")
}

func assertCoreUndecryptable(t *testing.T, err error, server string) {
	t.Helper()
	require.Error(t, err)
	assert.True(t, errors.Is(err, storage.ErrOAuthTokenUndecryptableSentinel), "typed error expected, got %v", err)
	var coded interface{ Code() diagnostics.Code }
	require.True(t, errors.As(err, &coded), "the error must carry its diagnostics code: %v", err)
	assert.Equal(t, diagnostics.Code("MCPX_OAUTH_TOKEN_UNDECRYPTABLE"), coded.Code())
	assert.Contains(t, err.Error(), "stored OAuth token cannot be decrypted: no encryption key configured")
	assert.Contains(t, err.Error(), "mcpproxy auth logout --server="+server)
	assert.False(t, IsOAuthPending(err), "must not park as \"login pending\"")
}

func TestTryOAuthAuth_UndecryptableRowStopsBeforeFlow(t *testing.T) {
	f := newUndecryptableCoreFixture(t, "sealed-http", false)
	before := f.raw(t)
	c := f.client(t, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := c.tryOAuthAuth(ctx)
	assertCoreUndecryptable(t, err, f.name)
	f.assertNoOAuthFlow(t)
	assert.Equal(t, before, f.raw(t))
}

func TestTrySSEOAuthAuth_UndecryptableRowStopsBeforeFlow(t *testing.T) {
	f := newUndecryptableCoreFixture(t, "sealed-sse", true)
	before := f.raw(t)
	c := f.client(t, "sse")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := c.trySSEOAuthAuth(ctx)
	assertCoreUndecryptable(t, err, f.name)
	f.assertNoOAuthFlow(t)
	assert.Equal(t, before, f.raw(t))
}

// client.go RefreshOAuthTokenDirect: the proactive refresh reports the coded
// error (not a generic "no handler") and never falls back to a login.
func TestRefreshOAuthTokenDirect_UndecryptableRow(t *testing.T) {
	f := newUndecryptableCoreFixture(t, "sealed-refresh", false)
	before := f.raw(t)
	c := f.client(t, "http")

	err := c.RefreshOAuthTokenDirect(context.Background())
	assertCoreUndecryptable(t, err, f.name)
	f.assertNoOAuthFlow(t)
	assert.Equal(t, before, f.raw(t))
}
