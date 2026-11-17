package core

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 FR-031 (review): a MANUAL login (the quick path behind
// Manager.StartManualOAuthQuick and the forced HTTP/SSE paths) must stop on an
// undecryptable stored row before OAuth config creation: no discovery, no DCR,
// no callback listener, and the row bytes stay as they were.

// reopenWithWrongKey swaps the fixture's storage for one opened with a key
// other than the one that sealed the row.
func (f *undecryptableCoreFixture) reopenWithWrongKey(t *testing.T) {
	t.Helper()
	require.NoError(t, f.m.Close())
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	other, err := tokenseal.ParseKey(base64.StdEncoding.EncodeToString(raw))
	require.NoError(t, err)
	m, err := storage.NewManager(f.dir, zap.NewNop().Sugar(), storage.WithOAuthTokenKey(other))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	f.m, f.db = m, m.GetBoltDB()
}

func TestManualLogin_UndecryptableRowStopsBeforeConfigCreation(t *testing.T) {
	for _, wrongKey := range []bool{false, true} {
		for _, transport := range []string{"http", "sse"} {
			name := "manual-" + transport + "-nokey"
			if wrongKey {
				name = "manual-" + transport + "-wrongkey"
			}
			t.Run(name, func(t *testing.T) {
				f := newUndecryptableCoreFixture(t, name, transport == "sse")
				if wrongKey {
					f.reopenWithWrongKey(t)
				}
				before := f.raw(t)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()

				_, err := f.client(t, transport).StartOAuthFlowQuick(ctx)
				assertCoreUndecryptable2(t, err, f.name, wrongKey)
				f.assertNoOAuthFlow(t)
				require.Equal(t, before, f.raw(t))

				_, err = f.client(t, transport).ForceOAuthFlowWithResult(ctx)
				assertCoreUndecryptable2(t, err, f.name, wrongKey)
				f.assertNoOAuthFlow(t)
				require.Equal(t, before, f.raw(t))
			})
		}
	}
}

// assertCoreUndecryptable2 is assertCoreUndecryptable with the reason that
// matches the key situation.
func assertCoreUndecryptable2(t *testing.T, err error, server string, wrongKey bool) {
	t.Helper()
	if !wrongKey {
		assertCoreUndecryptable(t, err, server)
		return
	}
	require.Error(t, err)
	require.ErrorIs(t, err, storage.ErrOAuthTokenUndecryptableSentinel)
	require.Contains(t, err.Error(), "sealed with a different key")
}
