package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Spec 116 PR2 (research D12, FR-014a, FR-014c): asynchronous writers of
// purge-set state write only while the server's row carries the incarnation
// they were started for.

func TestIncarnationWrites_DroppedAfterRemoveAndReAdd(t *testing.T) {
	m := newCommitTestManager(t)
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 3,
		Saves: []ServerSave{{Config: commitEntry("acme"), Stamp: stamp(3, 3)}}}))

	// Matching incarnation: every writer writes.
	sc := &config.ServerConfig{Name: "acme", Protocol: "stdio", Command: "echo"}
	id, written, err := m.RegisterServerIdentityIfIncarnation(sc, "/cfg", 3)
	require.NoError(t, err)
	require.True(t, written)
	ok, err := m.RecordToolCallIfIncarnation(&ToolCallRecord{ID: "c1", ServerID: id.ID, ServerName: "acme", ToolName: "echo", Timestamp: time.Now()}, 3)
	require.NoError(t, err)
	assert.True(t, ok)
	job := &scanner.ScanJob{ID: "scan-acme-1", ServerName: "acme", Status: scanner.ScanJobStatusRunning, Incarnation: 3}
	ok, err = m.SaveScanJobIfIncarnation(job, 3)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = m.SaveScanReportIfIncarnation(&scanner.ScanReport{ID: "r1", JobID: job.ID, ServerName: "acme", Incarnation: 3}, 3)
	require.NoError(t, err)
	assert.True(t, ok)

	// Remove (purge) and re-add at incarnation 5: the old writers drop.
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 4, Removes: []string{"acme"}}))
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 5,
		Saves: []ServerSave{{Config: commitEntry("acme"), Stamp: stamp(5, 5)}}}))
	counts, err := m.PurgeSetCounts("acme")
	require.NoError(t, err)
	assert.Equal(t, map[string]int{UpstreamsBucket: 1}, counts, "the removal purged everything the old writers wrote")

	_, written, err = m.RegisterServerIdentityIfIncarnation(sc, "/cfg", 3)
	require.NoError(t, err)
	assert.False(t, written)
	ok, err = m.RecordToolCallIfIncarnation(&ToolCallRecord{ID: "c2", ServerID: id.ID, ServerName: "acme", ToolName: "echo", Timestamp: time.Now()}, 3)
	require.NoError(t, err)
	assert.False(t, ok)
	ok, err = m.SaveScanJobIfIncarnation(job, 3)
	require.NoError(t, err)
	assert.False(t, ok)
	ok, err = m.SaveScanReportIfIncarnation(&scanner.ScanReport{ID: "r2", JobID: job.ID, ServerName: "acme", Incarnation: 3}, 3)
	require.NoError(t, err)
	assert.False(t, ok)
	counts, err = m.PurgeSetCounts("acme")
	require.NoError(t, err)
	assert.Equal(t, map[string]int{UpstreamsBucket: 1}, counts, "nothing of the old incarnation reached the replacement")

	// code_execution parent records belong to no upstream: never conditional.
	ok, err = m.RecordToolCallIfIncarnation(&ToolCallRecord{ID: "ce", ServerID: codeExecutionServerID, ServerName: "code_execution", Timestamp: time.Now()}, 3)
	require.NoError(t, err)
	assert.True(t, ok)
}

// Review round 1 (Sol): an UNBOUND write (inc == 0) is the fallback for a
// database no Spec 116 commit has written. Once a commit exists, a caller that
// read 0 — a tool call or a scan that looked its server up just after the
// removal — is writing for a server that is gone; the write is refused instead
// of recreating the removed server's history or scan evidence (FR-014a,
// FR-014c).
func TestIncarnationWrites_UnboundRefusedOnceCommitted(t *testing.T) {
	// Fallback: a database no commit has written accepts unbound writes.
	fresh := newCommitTestManager(t)
	ok, err := fresh.RecordToolCallIfIncarnation(&ToolCallRecord{ID: "u0", ServerID: "sid", ServerName: "acme", ToolName: "echo", Timestamp: time.Now()}, 0)
	require.NoError(t, err)
	assert.True(t, ok, "a database no commit wrote keeps the unbound fallback")

	m := newCommitTestManager(t)
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 3,
		Saves: []ServerSave{{Config: commitEntry("acme"), Stamp: stamp(3, 3)}}}))
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 4, Removes: []string{"acme"}}))

	ok, err = m.RecordToolCallIfIncarnation(&ToolCallRecord{ID: "u1", ServerID: "sid", ServerName: "acme", ToolName: "echo", Timestamp: time.Now()}, 0)
	require.NoError(t, err)
	assert.False(t, ok, "an unbound tool call after the removal writes nothing")
	job := &scanner.ScanJob{ID: "scan-acme-u", ServerName: "acme", Status: scanner.ScanJobStatusRunning}
	ok, err = m.SaveScanJobIfIncarnation(job, 0)
	require.NoError(t, err)
	assert.False(t, ok, "an unbound scan job after the removal writes nothing")
	ok, err = m.SaveScanReportIfIncarnation(&scanner.ScanReport{ID: "ru", JobID: job.ID, ServerName: "acme"}, 0)
	require.NoError(t, err)
	assert.False(t, ok, "an unbound scan report after the removal writes nothing")
	counts, err := m.PurgeSetCounts("acme")
	require.NoError(t, err)
	assert.Empty(t, counts, "nothing of the removed server was recreated")

	// code_execution parent records stay unconditional.
	ok, err = m.RecordToolCallIfIncarnation(&ToolCallRecord{ID: "ce0", ServerID: codeExecutionServerID, ServerName: "code_execution", Timestamp: time.Now()}, 0)
	require.NoError(t, err)
	assert.True(t, ok)
}

// Review round 5 (Sol): a replay's original call names the identity it was
// recorded under. When that identity was purged (the server was removed and
// re-added under the same name with a different configuration), the replay
// write is dropped even against the replacement's incarnation: it must not
// recreate the removed server's history bucket or identity.
func TestRecordReplayedToolCall_R5_RequiresOriginalIdentity(t *testing.T) {
	m := newCommitTestManager(t)
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 3,
		Saves: []ServerSave{{Config: commitEntry("acme"), Stamp: stamp(3, 3)}}}))
	old := &config.ServerConfig{Name: "acme", Protocol: "stdio", Command: "old-binary"}
	idA, written, err := m.RegisterServerIdentityIfIncarnation(old, "/cfg", 3)
	require.NoError(t, err)
	require.True(t, written)

	// Identity present: the replay records against the current incarnation.
	require.NoError(t, m.RecordToolCall(&ToolCallRecord{ID: "orig", ServerID: idA.ID, ServerName: "acme", ToolName: "t", Timestamp: time.Now()}))
	ok, err := m.RecordReplayedToolCallIfIncarnation(&ToolCallRecord{ID: "r1", ServerID: idA.ID, ServerName: "acme", ToolName: "t", Timestamp: time.Now()}, 3, "orig")
	require.NoError(t, err)
	assert.True(t, ok)

	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 4, Removes: []string{"acme"}}))
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 5,
		Saves: []ServerSave{{Config: commitEntry("acme"), Stamp: stamp(5, 5)}}}))

	// The replacement's incarnation matches, the original identity is gone.
	ok, err = m.RecordReplayedToolCallIfIncarnation(&ToolCallRecord{ID: "r2", ServerID: idA.ID, ServerName: "acme", ToolName: "t", Timestamp: time.Now()}, 5, "orig")
	require.NoError(t, err)
	assert.False(t, ok, "a replay of a purged identity's call must not be recorded")
	_, err = m.GetServerIdentityByID(idA.ID)
	assert.Error(t, err, "the purged identity must not be recreated")
	counts, err := m.PurgeSetCounts("acme")
	require.NoError(t, err)
	assert.Equal(t, map[string]int{UpstreamsBucket: 1}, counts)
}

// Review round 6 (Sol): GenerateServerID hashes the configuration, not the
// incarnation, so a same-config replacement registers the SAME identity id as
// the removed server. The identity check then passes; the replay write must
// still be dropped because the original call record was purged with the
// removed server's history.
func TestRecordReplayedToolCall_R6_SameConfigReplacementRequiresOriginalRecord(t *testing.T) {
	m := newCommitTestManager(t)
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 3,
		Saves: []ServerSave{{Config: commitEntry("acme"), Stamp: stamp(3, 3)}}}))
	cfg := &config.ServerConfig{Name: "acme", Protocol: "stdio", Command: "same-binary"}
	idA, written, err := m.RegisterServerIdentityIfIncarnation(cfg, "/cfg", 3)
	require.NoError(t, err)
	require.True(t, written)
	require.NoError(t, m.RecordToolCall(&ToolCallRecord{ID: "orig", ServerID: idA.ID, ServerName: "acme", ToolName: "t", Timestamp: time.Now()}))
	present, err := m.ToolCallRecordExists(idA.ID, "orig")
	require.NoError(t, err)
	require.True(t, present)

	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 4, Removes: []string{"acme"}}))
	require.NoError(t, m.ApplyServerCommit(&ServerCommitBatch{Generation: 5,
		Saves: []ServerSave{{Config: commitEntry("acme"), Stamp: stamp(5, 5)}}}))
	idB, written, err := m.RegisterServerIdentityIfIncarnation(cfg, "/cfg", 5)
	require.NoError(t, err)
	require.True(t, written)
	require.Equal(t, idA.ID, idB.ID, "the same-config replacement reuses the identity id")

	present, err = m.ToolCallRecordExists(idA.ID, "orig")
	require.NoError(t, err)
	assert.False(t, present, "the removal purged the original call")
	ok, err := m.RecordReplayedToolCallIfIncarnation(&ToolCallRecord{ID: "r2", ServerID: idA.ID, ServerName: "acme", ToolName: "t", Timestamp: time.Now()}, 5, "orig")
	require.NoError(t, err)
	assert.False(t, ok, "a replay of the removed server's call must not be recorded under the replacement")
	calls, err := m.GetServerToolCalls(idB.ID, 100)
	require.NoError(t, err)
	assert.Empty(t, calls, "the replacement's history stays empty")
}
