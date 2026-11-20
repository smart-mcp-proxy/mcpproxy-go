package runtime

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 T040 (FR-016, US1.1): with a key configured, runtime.New seals
// every plaintext oauth_tokens row before the upstream manager and the
// refresh manager (the first components that read or write OAuth rows) are
// constructed, and every server keeps its signed-in state.
func TestRuntimeNew_BootSweepSealsBeforeOAuthComponents(t *testing.T) {
	spec123ClearEnv(t)
	dataDir := t.TempDir()

	type seeded struct {
		key, access, client string
		expires             time.Time
	}
	var rows []seeded
	seed, err := storage.NewManager(dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(nil))
	require.NoError(t, err)
	for i, name := range []string{"alpha", "beta", "gamma"} {
		s := seeded{
			key:     oauth.GenerateServerKey(name, "https://"+name+".example.invalid/mcp"),
			access:  fmt.Sprintf("SPEC123-BOOT-ACCESS-%d", i),
			client:  fmt.Sprintf("client-%s", name),
			expires: time.Now().Add(time.Duration(i+1) * time.Hour).UTC(),
		}
		require.NoError(t, seed.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{
			ServerName: s.key, DisplayName: name, AccessToken: s.access, RefreshToken: "refresh-" + name,
			ClientID: s.client, ExpiresAt: s.expires,
		}))
		rows = append(rows, s)
	}
	require.NoError(t, seed.Close())

	b64, kid := spec123Key(t)
	cfg := config.DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.DataDir = dataDir
	cfg.OAuthTokenEncryptionKey = b64

	seen := map[string][]storage.OAuthRowClass{}
	runtimeNewStageHook = func(stage string, sm *storage.Manager) {
		got, err := sm.ListOAuthTokenRows()
		require.NoError(t, err)
		for _, r := range got {
			seen[stage] = append(seen[stage], r.Class)
		}
	}
	t.Cleanup(func() { runtimeNewStageHook = nil })

	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	for _, stage := range []string{runtimeStageUpstreamManager, runtimeStageRefreshManager} {
		classes, ok := seen[stage]
		require.True(t, ok, "stage %q was not reached through the hook", stage)
		assert.Equal(t, []storage.OAuthRowClass{storage.OAuthRowSealed, storage.OAuthRowSealed, storage.OAuthRowSealed}, classes,
			"every row must already be sealed when %s is constructed", stage)
	}

	got, err := rt.storageManager.ListOAuthTokenRows()
	require.NoError(t, err)
	require.Len(t, got, 3)
	for _, r := range got {
		assert.Equal(t, storage.OAuthRowSealed, r.Class)
		assert.Equal(t, kid, r.RowKID)
	}
	for _, s := range rows {
		rec, err := rt.storageManager.GetOAuthToken(s.key)
		require.NoError(t, err)
		assert.Equal(t, s.access, rec.AccessToken, "same access token")
		assert.Equal(t, s.client, rec.ClientID, "same client id")
		assert.True(t, s.expires.Equal(rec.ExpiresAt), "same expiry")
	}
}

// Without a key the boot path writes nothing: no row is touched.
func TestRuntimeNew_NoKeyLeavesPlaintextRows(t *testing.T) {
	spec123ClearEnv(t)
	dataDir := t.TempDir()
	seed, err := storage.NewManager(dataDir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(nil))
	require.NoError(t, err)
	require.NoError(t, seed.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: "plain_0123456789abcdef", AccessToken: "a"}))
	require.NoError(t, seed.Close())

	cfg := config.DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.DataDir = dataDir
	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	got, err := rt.storageManager.ListOAuthTokenRows()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, storage.OAuthRowPlaintext, got[0].Class)
}

// putForeignSealedRow stores a row sealed under a key the runtime does not
// have, so it reads as undecryptable (no_key without a configured key).
func putForeignSealedRow(t *testing.T, rt *Runtime, bucketKey string) []byte {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	k, err := tokenseal.ParseKey(base64.StdEncoding.EncodeToString(raw))
	require.NoError(t, err)
	pt, err := (&storage.OAuthTokenRecord{ServerName: bucketKey, AccessToken: "SPEC123-FOREIGN-ACCESS", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)}).MarshalBinary()
	require.NoError(t, err)
	val, err := k.Seal(storage.OAuthTokenBucket, bucketKey, pt)
	require.NoError(t, err)
	require.NoError(t, rt.storageManager.GetDB().Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(storage.OAuthTokenBucket)).Put([]byte(bucketKey), val)
	}))
	return val
}

func rawRow(t *testing.T, rt *Runtime, bucketKey string) []byte {
	t.Helper()
	var out []byte
	require.NoError(t, rt.storageManager.GetDB().View(func(tx *bbolt.Tx) error {
		out = append([]byte(nil), tx.Bucket([]byte(storage.OAuthTokenBucket)).Get([]byte(bucketKey))...)
		return nil
	}))
	return out
}

// Spec 123 T042 (FR-031, US2.1): the server list reports an undecryptable
// row as unhealthy / configure with the reason in the detail and the
// MCPX_OAUTH_TOKEN_UNDECRYPTABLE code, never as "log in", whether or not the
// connection has failed yet, and the read leaves the stored bytes alone.
func TestGetAllServers_UndecryptableOAuthRow(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, state := range []string{"connecting", "error", "pending_auth", "disconnected"} {
			t.Run(fmt.Sprintf("explicit=%v/%s", explicit, state), func(t *testing.T) {
				spec123ClearEnv(t)
				rt := newStaleTokenRuntime(t)
				sc := &config.ServerConfig{Name: "sealed-fixture", URL: "https://mcp.example.invalid/mcp", Protocol: "http", Enabled: true}
				if explicit {
					sc.OAuth = &config.OAuthConfig{PKCEEnabled: true}
				}
				key := oauth.GenerateServerKey(sc.Name, sc.URL)
				before := putForeignSealedRow(t, rt, key)
				rt.Supervisor().StateView().UpdateServer(sc.Name, func(s *stateview.ServerStatus) {
					s.Name, s.Config, s.Enabled, s.State = sc.Name, sc, true, state
					if state != "connecting" {
						s.LastError = "OAuth authentication required - login available"
					}
				})

				server := findServer(t, rt, sc.Name)
				assert.Equal(t, false, server["authenticated"])
				hs, ok := server["health"].(*contracts.HealthStatus)
				require.True(t, ok)
				assert.Equal(t, health.LevelUnhealthy, hs.Level)
				assert.Equal(t, health.ActionConfigure, hs.Action)
				assert.NotContains(t, hs.Actions, health.ActionLogin)
				assert.Contains(t, hs.Detail, "stored OAuth token cannot be decrypted: no encryption key configured")
				assert.Equal(t, "MCPX_OAUTH_TOKEN_UNDECRYPTABLE", server["error_code"])
				assert.Equal(t, before, rawRow(t, rt, key), "a status read never rewrites the row")
			})
		}
	}
}
