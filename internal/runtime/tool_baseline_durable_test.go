package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 117 PR-1 (#1567, T104/T106): a server's tool baseline is durable.
// Replacing the inventory, an authoritative empty refresh, a restart or an
// empty-review server approval must never re-trigger the first-baseline
// auto-approval of tools nobody reviewed.

const baselineServer = "notes"

func baselineTool(name, desc string, destructive bool) *config.ToolMetadata {
	t := &config.ToolMetadata{
		ServerName:  baselineServer,
		Name:        baselineServer + ":" + name,
		Description: desc,
		ParamsJSON:  `{"type":"object"}`,
		Hash:        "h-" + name + "-" + desc,
	}
	if destructive {
		yes := true
		t.Annotations = &config.ToolAnnotations{DestructiveHint: &yes}
	}
	return t
}

func readNote() *config.ToolMetadata { return baselineTool("read_note", "Read a note", false) }
func wipeAll() *config.ToolMetadata {
	return baselineTool("wipe_all_notes", "Delete every note", true)
}

// newBaselineRuntime opens a runtime over dataDir with one server.
func newBaselineRuntime(t *testing.T, dataDir string, quarantined bool) *Runtime {
	t.Helper()
	cfg := &config.Config{
		DataDir: dataDir,
		Listen:  "127.0.0.1:0",
		// manual: these fixtures encode manual-mode holds (see setupQuarantineRuntime).
		Servers: []*config.ServerConfig{{Name: baselineServer, Enabled: true, Quarantined: quarantined, TrustMode: string(config.TrustModeManual)}},
	}
	rt, err := New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { closeBaselineRuntime(rt) })
	return rt
}

var baselineRuntimeClosed sync.Map

// closeBaselineRuntime closes rt once (a restart test closes it early).
func closeBaselineRuntime(rt *Runtime) {
	if _, done := baselineRuntimeClosed.LoadOrStore(rt, struct{}{}); !done {
		_ = rt.Close()
	}
}

func commitTools(t *testing.T, rt *Runtime, tools ...*config.ToolMetadata) error {
	t.Helper()
	_, err := rt.commitDiscoveredTools(context.Background(), baselineServer, tools, rt.committedGenForTest(baselineServer))
	return err
}

func approvalOf(t *testing.T, rt *Runtime, tool string) *storage.ToolApprovalRecord {
	t.Helper()
	rec, err := rt.storageManager.GetToolApproval(baselineServer, tool)
	if err != nil {
		return nil
	}
	return rec
}

func indexedNames(t *testing.T, rt *Runtime) []string {
	t.Helper()
	tools, err := rt.indexManager.GetToolsByServer(baselineServer)
	require.NoError(t, err)
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		names = append(names, config.RawToolName(tl))
	}
	return names
}

func requireHeldUnindexed(t *testing.T, rt *Runtime, tool, step string) {
	t.Helper()
	rec := approvalOf(t, rt, tool)
	require.NotNil(t, rec, "%s: %s must have a record", step, tool)
	assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status, "%s: %s must stay pending (approved_by=%q)", step, tool, rec.ApprovedBy)
	assert.NotContains(t, indexedNames(t, rt), tool, "%s: %s must not be indexed", step, tool)
}

func baselineDecision(t *testing.T, rt *Runtime) (toolreview.BaselineDecision, bool) {
	t.Helper()
	d, ok, err := rt.storageManager.GetToolBaselineDecision(baselineServer)
	require.NoError(t, err)
	return d, ok
}

// T104(a): baseline [A] -> replace with [B] -> repeated discovery -> restart.
func TestToolBaselineDurable_ReplacedInventoryStaysPendingAcrossPassesAndRestart(t *testing.T) {
	dir := t.TempDir()
	rt := newBaselineRuntime(t, dir, false)

	require.NoError(t, commitTools(t, rt, readNote()))
	a := approvalOf(t, rt, "read_note")
	require.NotNil(t, a)
	require.Equal(t, storage.ToolApprovalStatusApproved, a.Status)
	require.Equal(t, "auto-baseline", a.ApprovedBy)
	require.Contains(t, indexedNames(t, rt), "read_note")

	for i := 0; i < 3; i++ {
		require.NoError(t, commitTools(t, rt, wipeAll()))
		requireHeldUnindexed(t, rt, "wipe_all_notes", "pass")
	}
	assert.Nil(t, approvalOf(t, rt, "read_note"), "the replaced tool's record is removed")

	closeBaselineRuntime(rt)
	rt2 := newBaselineRuntime(t, dir, false)
	require.NoError(t, commitTools(t, rt2, wipeAll()))
	requireHeldUnindexed(t, rt2, "wipe_all_notes", "after restart")
	d, ok := baselineDecision(t, rt2)
	require.True(t, ok, "the first trusted baseline records the marker")
	assert.Equal(t, toolreview.BaselineSourceDiscovery, d.Source)
	assert.Equal(t, "auto-baseline", d.By)
}

// T104(b): an authoritative empty refresh between A and B.
func TestToolBaselineDurable_AuthoritativeEmptyThenNewInventoryStaysPending(t *testing.T) {
	rt := newBaselineRuntime(t, t.TempDir(), false)
	require.NoError(t, commitTools(t, rt, readNote()))
	require.NoError(t, commitTools(t, rt)) // authoritative empty: clears A
	assert.Nil(t, approvalOf(t, rt, "read_note"))

	for i := 0; i < 2; i++ {
		require.NoError(t, commitTools(t, rt, wipeAll()))
		requireHeldUnindexed(t, rt, "wipe_all_notes", "after empty")
	}
}

// T104(c): an older store (approved record, no marker) is back-filled.
func TestToolBaselineDurable_OlderStoreMarkerInferred(t *testing.T) {
	rt := newBaselineRuntime(t, t.TempDir(), false)
	require.NoError(t, commitTools(t, rt, readNote()))
	require.NoError(t, rt.storageManager.GetDB().Update(func(tx *bbolt.Tx) error {
		if tx.Bucket([]byte(storage.ToolBaselineDecisionsBucket)) == nil {
			return nil
		}
		return tx.DeleteBucket([]byte(storage.ToolBaselineDecisionsBucket))
	}))
	_, ok := baselineDecision(t, rt)
	require.False(t, ok, "precondition: an older store has no marker")

	require.NoError(t, commitTools(t, rt, readNote()))
	d, ok := baselineDecision(t, rt)
	require.True(t, ok)
	assert.Equal(t, toolreview.BaselineSourceInferred, d.Source)

	require.NoError(t, commitTools(t, rt, wipeAll()))
	require.NoError(t, commitTools(t, rt, wipeAll()))
	requireHeldUnindexed(t, rt, "wipe_all_notes", "older store")
}

// T104(d): server removal (records + marker deleted) -> first inventory is a
// new baseline again.
func TestToolBaselineDurable_RemovalClearsMarker(t *testing.T) {
	rt := newBaselineRuntime(t, t.TempDir(), false)
	require.NoError(t, commitTools(t, rt, readNote()))
	require.NoError(t, commitTools(t, rt, wipeAll()))
	require.NoError(t, rt.storageManager.DeleteServerToolApprovals(baselineServer))
	_, ok := baselineDecision(t, rt)
	require.False(t, ok)

	require.NoError(t, commitTools(t, rt, wipeAll()))
	rec := approvalOf(t, rt, "wipe_all_notes")
	require.NotNil(t, rec)
	assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
	assert.Equal(t, "auto-baseline", rec.ApprovedBy)
}

// T104(e): an empty first inventory sets no marker (FR-025).
func TestToolBaselineDurable_EmptyFirstInventoryNoMarker(t *testing.T) {
	rt := newBaselineRuntime(t, t.TempDir(), false)
	require.NoError(t, commitTools(t, rt))
	_, ok := baselineDecision(t, rt)
	require.False(t, ok, "an empty first inventory must not decide the baseline")

	require.NoError(t, commitTools(t, rt, readNote()))
	rec := approvalOf(t, rt, "read_note")
	require.NotNil(t, rec)
	assert.Equal(t, "auto-baseline", rec.ApprovedBy)
	_, ok = baselineDecision(t, rt)
	assert.True(t, ok)
}

// T106 (US2 scenario 3): a quarantined server approved with an empty review
// records the baseline inside the locked commit; tools that appear after the
// unquarantine are held.
func TestToolBaselineDurable_EmptyReviewServerApprovalHoldsLaterTools(t *testing.T) {
	dir := t.TempDir()
	rt := newBaselineRuntime(t, dir, true)

	var markedInsideCommit bool
	_, err := rt.CommitServerApproval(baselineServer, nil, nil, "api", func() error {
		// The marker is written before the commit closure runs, under the lock.
		_, markedInsideCommit = baselineDecision(t, rt)
		return nil
	})
	require.NoError(t, err)
	require.True(t, markedInsideCommit, "the marker is written before the integrity baseline")
	d, ok := baselineDecision(t, rt)
	require.True(t, ok)
	assert.Equal(t, toolreview.BaselineSourceServerApproval, d.Source)
	assert.Equal(t, "api", d.By)
	closeBaselineRuntime(rt)

	// The server is now trusted (the unquarantine is outside PR-1's scope).
	rt2 := newBaselineRuntime(t, dir, false)
	require.NoError(t, commitTools(t, rt2, wipeAll()))
	require.NoError(t, commitTools(t, rt2, wipeAll()))
	requireHeldUnindexed(t, rt2, "wipe_all_notes", "after empty-review approval")
}

// CommitServerApproval runs under the server's approval lock.
func TestCommitServerApproval_HoldsApprovalLock(t *testing.T) {
	rt := newBaselineRuntime(t, t.TempDir(), true)
	_, err := rt.CommitServerApproval(baselineServer, nil, nil, "api", func() error {
		got := make(chan struct{})
		go func() {
			unlock := rt.lockToolApprovals(baselineServer)
			unlock()
			close(got)
		}()
		deadline := time.Now().Add(5 * time.Second)
		for {
			select {
			case <-got:
				t.Error("the approval lock must be held while commit runs")
				return nil
			default:
			}
			if rt.approvalLockWaiter(baselineServer).Load() > 0 {
				return nil // the competing locker is parked behind the commit
			}
			if time.Now().After(deadline) {
				t.Error("timed out waiting for the competing locker")
				return nil
			}
			time.Sleep(time.Millisecond)
		}
	})
	require.NoError(t, err)
}
