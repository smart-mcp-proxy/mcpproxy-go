package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 T041 (FR-012, FR-013, FR-031, US2): every token read in this
// package returns the undecryptable error instead of "no token", so nothing
// starts an interactive login over a row that login could not overwrite.

const (
	spec123Server = "sealed-srv"
	spec123URL    = "https://sealed.example.invalid/mcp"
)

func spec123TestKey(t *testing.T) *tokenseal.Key {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	k, err := tokenseal.ParseKey(base64.StdEncoding.EncodeToString(raw))
	require.NoError(t, err)
	return k
}

// undecryptableStore seeds a sealed row for spec123Server (plus, optionally,
// a decodable plaintext row for "open-srv") under one key, then reopens the
// database with no key, so the sealed row reads as no_key.
func undecryptableStore(t *testing.T, withOpenRow bool) (*storage.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	key := GenerateServerKey(spec123Server, spec123URL)

	m1, err := storage.NewManager(dir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(spec123TestKey(t)))
	require.NoError(t, err)
	require.NoError(t, m1.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
		ServerName: key, DisplayName: spec123Server, AccessToken: "SPEC123-SEALED-ACCESS",
		RefreshToken: "SPEC123-SEALED-REFRESH", ClientID: "sealed-client", ExpiresAt: time.Now().Add(time.Hour),
	}))
	require.NoError(t, m1.Close())

	m, err := storage.NewManager(dir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	if withOpenRow {
		require.NoError(t, m.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
			ServerName: "open-srv", DisplayName: "open-srv", AccessToken: "open-access",
			RefreshToken: "open-refresh", ExpiresAt: time.Now().Add(time.Hour),
		}))
	}
	return m, key
}

func spec123Raw(t *testing.T, m *storage.Manager, key string) []byte {
	t.Helper()
	var out []byte
	require.NoError(t, m.GetDB().View(func(tx *bbolt.Tx) error {
		out = append([]byte(nil), tx.Bucket([]byte(storage.OAuthTokenBucket)).Get([]byte(key))...)
		return nil
	}))
	require.NotEmpty(t, out)
	return out
}

func assertUndecryptable(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	assert.False(t, errors.Is(err, transport.ErrNoToken), "must not read as \"no token\": %v", err)
	assert.True(t, errors.Is(err, storage.ErrOAuthTokenUndecryptableSentinel), "typed error expected, got %v", err)
	assert.Equal(t, diagnostics.Code("MCPX_OAUTH_TOKEN_UNDECRYPTABLE"), diagnostics.Classify(err, diagnostics.ClassifierHints{}))
	assert.Contains(t, err.Error(), "stored OAuth token cannot be decrypted: no encryption key configured")
	assert.Contains(t, err.Error(), "mcpproxy auth logout --server="+spec123Server)
}

func TestPersistentTokenStore_GetTokenUndecryptable(t *testing.T) {
	m, key := undecryptableStore(t, false)
	before := spec123Raw(t, m, key)
	for name, store := range map[string]client.TokenStore{
		"unbound": NewPersistentTokenStore(spec123Server, spec123URL, m.GetBoltDB()),
		"bound":   NewBoundPersistentTokenStore(spec123Server, spec123URL, m.GetBoltDB(), commitview.CredentialBinding{}),
	} {
		t.Run(name, func(t *testing.T) {
			tok, err := store.GetToken(context.Background())
			assert.Nil(t, tok)
			assertUndecryptable(t, err)
		})
	}
	assert.Equal(t, before, spec123Raw(t, m, key))
}

func TestHasPersistedTokenBound_Undecryptable(t *testing.T) {
	m, _ := undecryptableStore(t, false)
	has, hasRefresh, expired, err := HasPersistedTokenBound(spec123Server, spec123URL, m.GetBoltDB(), commitview.CredentialBinding{})
	assertUndecryptable(t, err)
	assert.False(t, has)
	assert.False(t, hasRefresh)
	assert.False(t, expired)

	_, _, _, err = HasPersistedToken(spec123Server, spec123URL, m.GetBoltDB())
	assertUndecryptable(t, err)

	// "Not found" stays a plain absence, not an error.
	has, _, _, err = HasPersistedToken("absent", spec123URL, m.GetBoltDB())
	require.NoError(t, err)
	assert.False(t, has)
}

func TestGetPersistedRefreshToken_Undecryptable(t *testing.T) {
	m, _ := undecryptableStore(t, false)
	rt, err := GetPersistedRefreshToken(spec123Server, spec123URL, m.GetBoltDB())
	assert.Empty(t, rt)
	assertUndecryptable(t, err)

	rt, err = GetPersistedRefreshToken("absent", spec123URL, m.GetBoltDB())
	require.NoError(t, err)
	assert.Empty(t, rt)
}

// A login's code exchange saves the token and the DCR client together
// (WithLoginClient). Over an undecryptable row it fails with the coded error
// and the stored bytes do not change (US2.4).
func TestPersistentTokenStore_LoginSaveOverUndecryptableRowRefused(t *testing.T) {
	m, key := undecryptableStore(t, false)
	before := spec123Raw(t, m, key)
	store := NewBoundPersistentTokenStore(spec123Server, spec123URL, m.GetBoltDB(), commitview.CredentialBinding{})
	ctx := WithLoginClient(context.Background(), LoginClient{ClientID: "fresh-client", CallbackPort: 51234, RedirectURI: "http://127.0.0.1:51234/oauth/callback"})
	err := store.SaveToken(ctx, &client.Token{AccessToken: "fresh-access", RefreshToken: "fresh-refresh", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)})
	assertUndecryptable(t, err)
	assert.Equal(t, before, spec123Raw(t, m, key), "the refused login leaves the row")
}

// The refresh manager schedules the servers whose rows open and never the
// undecryptable one.
func TestRefreshManager_StartSkipsUndecryptableRow(t *testing.T) {
	m, _ := undecryptableStore(t, true)
	rm := NewRefreshManager(m, nil, &RefreshManagerConfig{Threshold: 0.8}, zaptest.NewLogger(t))
	require.NoError(t, rm.Start(context.Background()))
	t.Cleanup(rm.Stop)
	assert.NotNil(t, rm.GetSchedule("open-srv"))
	assert.Nil(t, rm.GetSchedule(spec123Server))
	assert.Equal(t, 1, rm.GetScheduleCount())
}
