package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func newCommitTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func commitEntry(name string, args ...string) *config.ServerConfig {
	return &config.ServerConfig{Name: name, Command: "true", Protocol: "stdio", Args: args}
}

func stamp(g, inc int64) commitview.ServerGen {
	return commitview.ServerGen{Gen: g, Incarnation: inc}
}

// T018: one transaction, stamps written verbatim, meta generation written.
func TestApplyServerCommit_WritesRowsStampsAndMeta(t *testing.T) {
	m := newCommitTestManager(t)
	qe := true
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{
		Generation: 5,
		Saves: []ServerSave{
			{Config: commitEntry("a"), Stamp: stamp(5, 5)},
			{Config: commitEntry("b"), Stamp: stamp(3, 2)}, // a repaired unchanged row keeps its stamp (R9.3)
		},
		QuarantineEnabled: &qe,
	}))
	stored, err := m.ListStoredServers()
	require.NoError(t, err)
	assert.Equal(t, stamp(5, 5), stored["a"].Stamp)
	assert.Equal(t, stamp(3, 2), stored["b"].Stamp, "storage writes the prepared stamp verbatim, never G")
	seed, committed, err := m.ConfigCommitState()
	require.NoError(t, err)
	assert.True(t, committed)
	assert.Equal(t, int64(5), seed)
	enabled, ok, err := m.StoredQuarantineEnabled()
	require.NoError(t, err)
	assert.True(t, ok && enabled)
}

// T018: an invariant violation anywhere in the batch writes nothing.
func TestApplyServerCommit_AllOrNothing(t *testing.T) {
	m := newCommitTestManager(t)
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{
		Generation: 1,
		Saves:      []ServerSave{{Config: commitEntry("keep"), Stamp: stamp(1, 1)}},
	}))

	cases := map[string]*ServerCommitBatch{
		"zero stamp": {Generation: 2, Saves: []ServerSave{
			{Config: commitEntry("keep", "x"), Stamp: stamp(2, 1)},
			{Config: commitEntry("bad"), Stamp: commitview.ServerGen{}},
		}},
		"stamp above G": {Generation: 2, Saves: []ServerSave{
			{Config: commitEntry("keep", "x"), Stamp: stamp(2, 1)},
			{Config: commitEntry("bad"), Stamp: stamp(3, 3)},
		}},
		"remove and save overlap": {Generation: 2, Removes: []string{"keep"}, Saves: []ServerSave{
			{Config: commitEntry("keep", "x"), Stamp: stamp(2, 1)},
		}},
		"replace without new row": {Generation: 2, Replaces: []string{"keep"}},
	}
	for name, b := range cases {
		err := m.ApplyServerCommit(b)
		require.Error(t, err, name)
		assert.True(t, errors.Is(err, ErrInvariant), name)
		stored, lerr := m.ListStoredServers()
		require.NoError(t, lerr)
		require.Len(t, stored, 1, name)
		assert.Nil(t, stored["keep"].Config.Args, "%s: the first row must be unchanged", name)
		seed, _, _ := m.ConfigCommitState()
		assert.Equal(t, int64(1), seed, "%s: meta untouched", name)
	}
}

// The quarantine-keeping invariant still holds per row: a save that would
// lower a recorded quarantine without an explicit decision aborts the commit.
func TestApplyServerCommit_RefusesSilentQuarantineLowering(t *testing.T) {
	m := newCommitTestManager(t)
	q := commitEntry("q")
	q.Quarantined = true
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 1, Saves: []ServerSave{{Config: q, Stamp: stamp(1, 1)}}}))

	lowered := commitEntry("q")
	err := m.ApplyServerCommit(&ServerCommitBatch{Generation: 2, Saves: []ServerSave{{Config: lowered, Stamp: stamp(2, 1)}}})
	require.ErrorIs(t, err, ErrInvariant)

	lowered.MarkQuarantineExplicitlySet(true)
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 2, Saves: []ServerSave{{Config: lowered, Stamp: stamp(2, 1)}}}))
	got, err := m.GetStoredServer("q")
	require.NoError(t, err)
	assert.False(t, got.Config.Quarantined, "an explicit decision may lower it")
}

// Removes and Replaces delete the row and its call history in the same
// transaction; a replaced name gets its new row.
func TestApplyServerCommit_RemoveAndReplace(t *testing.T) {
	m := newCommitTestManager(t)
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 1, Saves: []ServerSave{
		{Config: commitEntry("gone"), Stamp: stamp(1, 1)},
		{Config: commitEntry("re"), Stamp: stamp(1, 1)},
	}}))
	identity, err := m.RegisterServerIdentity(commitEntry("gone"), "")
	require.NoError(t, err)
	require.NoError(t, m.RecordToolCall(&ToolCallRecord{ID: "c1", ServerID: identity.ID, ServerName: "gone", ToolName: "t", Timestamp: time.Now()}))

	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{
		Generation: 2, Removes: []string{"gone"}, Replaces: []string{"re"},
		Saves: []ServerSave{{Config: commitEntry("re", "new"), Stamp: stamp(2, 2)}},
	}))
	stored, err := m.ListStoredServers()
	require.NoError(t, err)
	assert.NotContains(t, stored, "gone")
	assert.Equal(t, stamp(2, 2), stored["re"].Stamp)
	calls, err := m.GetServerToolCalls(identity.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, calls, "a removed server's history goes with its row")
}

func TestServerRowMatches(t *testing.T) {
	a := commitEntry("a", "x")
	b := commitEntry("a", "x")
	b.Created = time.Now()
	b.Shared = true
	assert.True(t, ServerRowMatches(a, b), "timestamps and config-only fields are not part of the projection")
	b.Args = []string{"y"}
	assert.False(t, ServerRowMatches(a, b))
}

// T018 (review round 1): a failure INSIDE the bbolt transaction, after the
// delete of a removed row and the writes of earlier rows, rolls all of them
// back. The cases above are refused by the preflight before the transaction
// starts; this one passes the preflight and fails on the third save.
func TestApplyServerCommit_InTransactionFailureRollsBack(t *testing.T) {
	m := newCommitTestManager(t)
	q := commitEntry("q")
	q.Quarantined = true
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 1, Saves: []ServerSave{
		{Config: commitEntry("keep"), Stamp: stamp(1, 1)},
		{Config: commitEntry("victim"), Stamp: stamp(1, 1)},
		{Config: q, Stamp: stamp(1, 1)},
	}}))

	err := m.ApplyServerCommit(&ServerCommitBatch{
		Generation: 2,
		Removes:    []string{"victim"},
		Saves: []ServerSave{
			{Config: commitEntry("keep", "x"), Stamp: stamp(2, 1)},
			{Config: commitEntry("fresh"), Stamp: stamp(2, 2)},
			{Config: commitEntry("q"), Stamp: stamp(2, 1)}, // lowers a recorded quarantine: refused in the tx
		},
	})
	require.ErrorIs(t, err, ErrInvariant)

	stored, err := m.ListStoredServers()
	require.NoError(t, err)
	assert.Len(t, stored, 3)
	assert.Contains(t, stored, "victim", "the delete ran first in the transaction and must be rolled back")
	assert.NotContains(t, stored, "fresh", "a row written earlier in the transaction must be rolled back")
	assert.Nil(t, stored["keep"].Config.Args, "the first save must be rolled back")
	assert.Equal(t, stamp(1, 1), stored["keep"].Stamp)
	assert.True(t, stored["q"].Config.Quarantined)
	seed, _, err := m.ConfigCommitState()
	require.NoError(t, err)
	assert.Equal(t, int64(1), seed, "meta untouched")
}

// Review round 3 (data-model §1): the OAuth clear of a commit is part of the
// row transaction. A failure later in the same transaction keeps the old
// credentials; a successful commit removes both key forms of the cleared
// server and nothing of the others.
func TestApplyServerCommit_OAuthClearRollsBackWithRows(t *testing.T) {
	m := newCommitTestManager(t)
	q := commitEntry("q")
	q.Quarantined = true
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 1, Saves: []ServerSave{
		{Config: commitEntry("o"), Stamp: stamp(1, 1)},
		{Config: q, Stamp: stamp(1, 1)},
	}}))
	keys := []string{"o", "o_0123456789abcdef", "other_0123456789abcdef"}
	for _, k := range keys {
		require.NoError(t, m.db.SaveOAuthToken(&OAuthTokenRecord{ServerName: k, AccessToken: "at", ClientID: "dcr"}))
	}
	present := func(k string) bool {
		rec, err := m.db.GetOAuthToken(k)
		return err == nil && rec != nil
	}

	err := m.ApplyServerCommit(&ServerCommitBatch{
		Generation:  2,
		OAuthClears: []string{"o"},
		Saves: []ServerSave{
			{Config: commitEntry("o", "new"), Stamp: stamp(2, 1)},
			{Config: commitEntry("q"), Stamp: stamp(2, 1)}, // lowers a recorded quarantine: refused in the tx
		},
	})
	require.ErrorIs(t, err, ErrInvariant)
	for _, k := range keys {
		assert.True(t, present(k), "%s must survive the rolled-back commit", k)
	}

	require.ErrorIs(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 2, OAuthClears: []string{"o"}}), ErrInvariant,
		"a clear without the row save it belongs to is a caller bug")

	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{
		Generation:  2,
		OAuthClears: []string{"o"},
		Saves:       []ServerSave{{Config: commitEntry("o", "new"), Stamp: stamp(2, 1)}},
	}))
	assert.False(t, present("o"))
	assert.False(t, present("o_0123456789abcdef"))
	assert.True(t, present("other_0123456789abcdef"))
}
