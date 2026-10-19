package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 117 PR-1 (T102, #1567): the durable per-server tool baseline marker.

func TestToolBaselineDecision_MarkThenDecided(t *testing.T) {
	m, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	decided, err := m.ToolBaselineDecided("notes")
	require.NoError(t, err)
	assert.False(t, decided, "unknown server has no marker")

	require.NoError(t, m.MarkToolBaselineDecided("notes", toolreview.BaselineDecision{
		Source: toolreview.BaselineSourceDiscovery, By: "auto-baseline",
	}))
	decided, err = m.ToolBaselineDecided("notes")
	require.NoError(t, err)
	assert.True(t, decided)

	other, err := m.ToolBaselineDecided("other")
	require.NoError(t, err)
	assert.False(t, other, "the marker is per server")
}

func TestToolBaselineDecision_IdempotentKeepsFirstDecidedAtUpgradesOnlyInferred(t *testing.T) {
	m, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	first := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	require.NoError(t, m.MarkToolBaselineDecided("s", toolreview.BaselineDecision{
		DecidedAt: first, Source: toolreview.BaselineSourceInferred, By: "auto-baseline",
	}))
	// An explicit source upgrades an inferred one; decided_at is kept.
	require.NoError(t, m.MarkToolBaselineDecided("s", toolreview.BaselineDecision{
		DecidedAt: first.Add(time.Hour), Source: toolreview.BaselineSourceServerApproval, By: "api",
	}))
	d, ok, err := m.GetToolBaselineDecision("s")
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, d.DecidedAt.Equal(first), "first decided_at is kept, got %v", d.DecidedAt)
	assert.Equal(t, toolreview.BaselineSourceServerApproval, d.Source)
	assert.Equal(t, "api", d.By)

	// A later decision never downgrades or rewrites an explicit one.
	require.NoError(t, m.MarkToolBaselineDecided("s", toolreview.BaselineDecision{
		Source: toolreview.BaselineSourceDiscovery, By: "auto-baseline",
	}))
	d, _, err = m.GetToolBaselineDecision("s")
	require.NoError(t, err)
	assert.Equal(t, toolreview.BaselineSourceServerApproval, d.Source)
	assert.Equal(t, "api", d.By)
	assert.True(t, d.DecidedAt.Equal(first))
}

func TestToolBaselineDecision_UnreadableValueFailsClosed(t *testing.T) {
	m, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	require.NoError(t, m.GetDB().Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(ToolBaselineDecisionsBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte("s"), []byte("{not json"))
	}))
	decided, err := m.ToolBaselineDecided("s")
	require.NoError(t, err)
	assert.True(t, decided, "an unreadable marker counts as decided (fail closed)")
}

func TestToolBaselineDecision_RemovedWithServerApprovals(t *testing.T) {
	m, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	for _, s := range []string{"a", "b"} {
		require.NoError(t, m.SaveToolApproval(&ToolApprovalRecord{ServerName: s, ToolName: "t", Status: ToolApprovalStatusApproved}))
		require.NoError(t, m.MarkToolBaselineDecided(s, toolreview.BaselineDecision{Source: toolreview.BaselineSourceDiscovery}))
	}

	require.NoError(t, m.DeleteServerToolApprovals("a"))
	// One read transaction sees both the records and the marker gone.
	require.NoError(t, m.GetDB().View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(ToolBaselineDecisionsBucket))
		require.NotNil(t, b)
		assert.Nil(t, b.Get([]byte("a")), "marker of a removed with its records")
		assert.NotNil(t, b.Get([]byte("b")))
		assert.Nil(t, tx.Bucket([]byte(ToolApprovalBucket)).Get([]byte("a:t")))
		return nil
	}))

	decidedB, err := m.ToolBaselineDecided("b")
	require.NoError(t, err)
	assert.True(t, decidedB)
}

func TestToolBaselineDecision_PrunedWithOrphanApprovals(t *testing.T) {
	m, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	for _, s := range []string{"a", "b"} {
		require.NoError(t, m.SaveToolApproval(&ToolApprovalRecord{ServerName: s, ToolName: "t", Status: ToolApprovalStatusApproved}))
		require.NoError(t, m.MarkToolBaselineDecided(s, toolreview.BaselineDecision{Source: toolreview.BaselineSourceDiscovery}))
	}
	// c has a marker but no records (an empty-review approval): pruned too.
	require.NoError(t, m.MarkToolBaselineDecided("c", toolreview.BaselineDecision{Source: toolreview.BaselineSourceServerApproval}))

	_, err := m.PruneOrphanToolApprovals([]string{"a"})
	require.NoError(t, err)

	for name, want := range map[string]bool{"a": true, "b": false, "c": false} {
		got, err := m.ToolBaselineDecided(name)
		require.NoError(t, err)
		assert.Equal(t, want, got, "marker of %s", name)
	}
}

func TestToolBaselineDecision_ReadDoesNotCreateBucket(t *testing.T) {
	m, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	// Simulate a store written by an older binary: no marker bucket.
	require.NoError(t, m.GetDB().Update(func(tx *bbolt.Tx) error {
		if tx.Bucket([]byte(ToolBaselineDecisionsBucket)) == nil {
			return nil
		}
		return tx.DeleteBucket([]byte(ToolBaselineDecisionsBucket))
	}))
	decided, err := m.ToolBaselineDecided("s")
	require.NoError(t, err)
	assert.False(t, decided)
	require.NoError(t, m.GetDB().View(func(tx *bbolt.Tx) error {
		assert.Nil(t, tx.Bucket([]byte(ToolBaselineDecisionsBucket)), "a read must not create the bucket")
		return nil
	}))
	// Removal on such a store is a no-op, not an error.
	require.NoError(t, m.DeleteServerToolApprovals("s"))
	_, err = m.PruneOrphanToolApprovals(nil)
	require.NoError(t, err)
}
