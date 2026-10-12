package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage/purgetest"
)

// Spec 116 PR2 storage tests (quickstart.md §T2).

func newPurgeTestManager(t *testing.T) *storage.Manager {
	t.Helper()
	m, err := storage.NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func purgeEntry(name string) *config.ServerConfig {
	return &config.ServerConfig{Name: name, Command: "true", Protocol: "stdio"}
}

func seedRows(t *testing.T, m *storage.Manager, g int64, names ...string) {
	t.Helper()
	b := &storage.ServerCommitBatch{Generation: g}
	for _, n := range names {
		b.Saves = append(b.Saves, storage.ServerSave{Config: purgeEntry(n), Stamp: commitview.ServerGen{Gen: g, Incarnation: g}})
	}
	require.NoError(t, m.ApplyServerCommit(b))
}

// T2.1 (storage half): a removal in ApplyServerCommit deletes the whole purge
// set of the name in the row's transaction, and nothing of a neighbour whose
// name shares the prefix.
func TestPurge_T2_1_RemoveDeletesWholePurgeSet(t *testing.T) {
	m := newPurgeTestManager(t)
	seedRows(t, m, 1, "acme", "acme_x")
	purgetest.Seed(t, m, "acme")
	purgetest.Seed(t, m, "acme_x")
	purgetest.AssertSeeded(t, m, "acme")
	before := purgetest.Counts(t, m, "acme_x")

	require.NoError(t, m.ApplyServerCommit(&storage.ServerCommitBatch{Generation: 2, Removes: []string{"acme"}}))

	purgetest.AssertPurged(t, m, "acme", "ApplyServerCommit.Removes")
	assert.Equal(t, before, purgetest.Counts(t, m, "acme_x"), "the neighbour acme_x keeps every record")
}

// T2.17 (storage half, review R8.2): a same-commit replacement runs the full
// purge before it writes the new row with the new stamp.
func TestPurge_T2_17_ReplaceDeletesWholePurgeSetThenSaves(t *testing.T) {
	m := newPurgeTestManager(t)
	seedRows(t, m, 1, "acme")
	purgetest.Seed(t, m, "acme")
	purgetest.AssertSeeded(t, m, "acme")

	require.NoError(t, m.ApplyServerCommit(&storage.ServerCommitBatch{
		Generation: 4, Replaces: []string{"acme"},
		Saves: []storage.ServerSave{{Config: purgeEntry("acme"), Stamp: commitview.ServerGen{Gen: 4, Incarnation: 4}}},
	}))
	c := purgetest.Counts(t, m, "acme")
	assert.Equal(t, 1, c[purgetest.Upstream], "the new row is written")
	delete(c, purgetest.Upstream)
	for bucket, n := range c {
		assert.Zero(t, n, "%s still holds %d key(s) of the replaced server", bucket, n)
	}
	row, err := m.GetStoredServer("acme")
	require.NoError(t, err)
	assert.Equal(t, int64(4), row.Stamp.Incarnation)
}

// T2.6 (B2): the OAuth clear matches exactly name and name_<16 lowercase hex>;
// foo_bar's credentials survive the removal (and the logout) of foo.
func TestPurge_T2_6_OAuthExactKey(t *testing.T) {
	for _, path := range []string{"remove", "logout"} {
		t.Run(path, func(t *testing.T) {
			m := newPurgeTestManager(t)
			seedRows(t, m, 1, "foo", "foo_bar")
			db := m.GetBoltDB()
			fooKey := oauth.GenerateServerKey("foo", "https://a.example/mcp")
			barKey := oauth.GenerateServerKey("foo_bar", "https://b.example/mcp")
			for _, k := range []string{"foo", fooKey, "foo_bar", barKey, "foo_notahexsuffix"} {
				require.NoError(t, db.SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: k, AccessToken: "at-" + k}))
			}
			if path == "remove" {
				require.NoError(t, m.ApplyServerCommit(&storage.ServerCommitBatch{Generation: 2, Removes: []string{"foo"}}))
			} else {
				require.NoError(t, m.ClearOAuthState("foo"))
			}
			for _, gone := range []string{"foo", fooKey} {
				_, err := db.GetOAuthToken(gone)
				assert.Error(t, err, "%s must be cleared", gone)
			}
			for _, kept := range []string{"foo_bar", barKey, "foo_notahexsuffix"} {
				_, err := db.GetOAuthToken(kept)
				assert.NoError(t, err, "%s belongs to another server and must survive (B2)", kept)
			}
		})
	}
}

// T046 (A11): toolhash keys are "server:tool", and server names cannot hold
// ':' (config validation), so the purge's "name:" prefix is exact: it never
// reaches acme_x:… or acmex:….
func TestPurge_T046_ToolHashKeyFormat(t *testing.T) {
	m := newPurgeTestManager(t)
	seedRows(t, m, 1, "acme", "acme_x", "acmex")
	for _, k := range []string{"acme:echo", "acme:ns:tool", "acme_x:echo", "acmex:echo"} {
		require.NoError(t, m.SaveToolHash(k, "h"))
	}
	require.NoError(t, m.ApplyServerCommit(&storage.ServerCommitBatch{Generation: 2, Removes: []string{"acme"}}))
	for _, gone := range []string{"acme:echo", "acme:ns:tool"} {
		_, err := m.GetToolHash(gone)
		assert.Error(t, err, "%s must be purged", gone)
	}
	for _, kept := range []string{"acme_x:echo", "acmex:echo"} {
		_, err := m.GetToolHash(kept)
		assert.NoError(t, err, "%s belongs to another server", kept)
	}
	cfg := config.DefaultConfig()
	cfg.Servers = []*config.ServerConfig{{Name: "acme:evil", Command: "true", Protocol: "stdio"}}
	require.NotEmpty(t, cfg.ValidateDetailed(), "a server name containing ':' must be rejected, or the toolhash prefix would not be exact")
}

// The startup empty-set guard: RowOnlyRemoves deletes the row and history and
// keeps the rest of the purge set; a replacement still purges.
func TestPurge_RowOnlyRemovesKeepsTrustState(t *testing.T) {
	m := newPurgeTestManager(t)
	seedRows(t, m, 1, "acme")
	purgetest.Seed(t, m, "acme")
	require.NoError(t, m.ApplyServerCommit(&storage.ServerCommitBatch{Generation: 2, Removes: []string{"acme"}, RowOnlyRemoves: true}))
	c := purgetest.Counts(t, m, "acme")
	assert.Zero(t, c[purgetest.Upstream])
	assert.Zero(t, c[purgetest.ToolCalls])
	assert.Equal(t, 1, c[purgetest.ToolApprovals], "approvals survive an empty startup config")
	assert.Equal(t, 1, c[purgetest.OAuthTokens], "credentials survive an empty startup config")
}
