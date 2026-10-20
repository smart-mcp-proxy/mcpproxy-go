package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 116 PR2 review round 2 (Sol chunk 2): a tool call that finishes while
// the connect worker is still parked before it registers the server identity
// writes a hash-keyed history bucket with no identity behind it. The removal
// purge, PurgeSetCounts and the orphan sweep must all still find it, so a
// same-config re-add never exposes the removed server's history.
func TestPurge_R2_UnregisteredHistoryBucketIsPurged(t *testing.T) {
	m := newCommitTestManager(t)
	sc := &config.ServerConfig{Name: "acme", Protocol: "stdio", Command: "echo"}
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 3,
		Saves: []ServerSave{{Config: sc, Stamp: stamp(3, 3)}}}))

	// No RegisterServerIdentityIfIncarnation: the connect worker is parked.
	id := GenerateServerID(sc)
	ok, err := m.RecordToolCallIfIncarnation(&ToolCallRecord{ID: "c1", ServerID: id, ServerName: "acme", ToolName: "echo", Timestamp: time.Now()}, 3)
	require.NoError(t, err)
	require.True(t, ok)

	counts, err := m.PurgeSetCounts("acme")
	require.NoError(t, err)
	assert.Equal(t, 1, counts[PurgeKeyToolCallBuckets], "PurgeSetCounts sees the unregistered history bucket")

	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 4, Removes: []string{"acme"}}))
	bucketGone := func() bool {
		gone := true
		_ = m.db.db.View(func(tx *bbolt.Tx) error {
			gone = tx.Bucket([]byte(toolCallsBucketName(id))) == nil
			return nil
		})
		return gone
	}
	assert.True(t, bucketGone(), "the removal purge drops the hash-keyed history of the removed server")

	// A same-config re-add starts with no history.
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 5,
		Saves: []ServerSave{{Config: sc, Stamp: stamp(5, 5)}}}))
	assert.True(t, bucketGone(), "the re-added server inherits no history")
	counts, err = m.PurgeSetCounts("acme")
	require.NoError(t, err)
	assert.Equal(t, map[string]int{UpstreamsBucket: 1}, counts)
}
