package runtime

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 117 PR-2 T207: the server approval's one critical section
// (FR-011/FR-012) and its binding (US1 scenarios 1-3, 5).

func baselineSave(t *testing.T, rt *Runtime, blocks ...string) (func() error, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	return func() error {
		calls.Add(1)
		b := &scanner.IntegrityBaseline{ServerName: rvServer, ApprovedAt: time.Now(), ApprovedBy: "api"}
		if len(blocks) == 0 {
			return rt.storageManager.SaveIntegrityBaseline(b)
		}
		bl := make([]scanner.ToolApprovalBlock, 0, len(blocks))
		for _, name := range blocks {
			bl = append(bl, scanner.ToolApprovalBlock{ToolName: name, ApprovedAt: time.Now(), ApprovedBy: "api"})
		}
		return rt.storageManager.SaveIntegrityBaselineWithBlocks(b, bl)
	}, calls
}

func requireNothingApproved(t *testing.T, rt *Runtime, calls *atomic.Int32) {
	t.Helper()
	requireNothingApprovedMarker(t, rt, calls, false)
}

// requireNothingApprovedMarker: markerBefore says whether a (discovery or
// inferred) marker existed before the refused approval; a refused approval
// never writes the server_approval one.
func requireNothingApprovedMarker(t *testing.T, rt *Runtime, calls *atomic.Int32, markerBefore bool) {
	t.Helper()
	assert.Zero(t, calls.Load(), "the integrity baseline must not be written")
	d, ok, err := rt.storageManager.GetToolBaselineDecision(rvServer)
	require.NoError(t, err)
	assert.Equal(t, markerBefore, ok, "a refused approval writes no baseline marker")
	if ok {
		assert.NotEqual(t, toolreview.BaselineSourceServerApproval, d.Source)
	}
	assert.True(t, rt.serverQuarantinedInStorage(rvServer), "the server stays quarantined")
	_, err = rt.storageManager.GetIntegrityBaseline(rvServer)
	assert.Error(t, err, "no integrity baseline")
}

// US1 scenario 1: a hint change after the review refuses the approval.
func TestCommitServerApproval_BoundMismatchWritesNothing(t *testing.T) {
	rt := newReviewRuntime(t, true)
	capture(t, rt, rvTool("read_note", "Read a note", readOnlyHints()))
	h1 := reviewHashes(t, rt)["read_note"]
	capture(t, rt, rvTool("read_note", "Read a note", destructiveHints()))

	save, calls := baselineSave(t, rt)
	n, err := rt.CommitServerApproval(rvServer, nil, map[string]string{"read_note": h1}, "api", save)
	stale := requireStale(t, err)
	assert.Zero(t, n)
	assert.Equal(t, []string{"read_note"}, stale.Changed)
	requireNothingApproved(t, rt, calls)
	assert.Equal(t, storage.ToolApprovalStatusPending, rvRecord(t, rt, "read_note").Status)
	cfg, _ := rt.GetConfig()
	for _, sc := range cfg.Servers {
		if sc.Name == rvServer {
			assert.True(t, sc.Quarantined, "the live config stays quarantined")
		}
	}
}

// US1 scenario 2: a tool captured after the review is "not in the review";
// blocking it approves the rest.
func TestCommitServerApproval_UnreviewedToolAndBlock(t *testing.T) {
	rt := newReviewRuntime(t, true)
	capture(t, rt, rvTool("read_note", "Read a note", readOnlyHints()))
	h := reviewHashes(t, rt)
	capture(t, rt, rvTool("read_note", "Read a note", readOnlyHints()), rvTool("drop_db", "Drop the database", destructiveHints()))

	save, calls := baselineSave(t, rt)
	_, err := rt.CommitServerApproval(rvServer, nil, h, "api", save)
	assert.Equal(t, []string{"drop_db"}, requireStale(t, err).Unreviewed)
	requireNothingApproved(t, rt, calls)

	save, _ = baselineSave(t, rt, "drop_db")
	n, err := rt.CommitServerApproval(rvServer, []string{"drop_db"}, h, "api", save)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.False(t, rt.serverQuarantinedInStorage(rvServer))
	assert.Equal(t, storage.ToolApprovalStatusApproved, rvRecord(t, rt, "read_note").Status)
	drop := rvRecord(t, rt, "drop_db")
	assert.Equal(t, storage.ToolApprovalStatusApproved, drop.Status)
	assert.True(t, drop.Disabled, "the block is approved+disabled")
}

// A bound block target that no longer exists is out of date (an unbound one
// keeps the storage error, HTTP 400).
func TestCommitServerApproval_MissingBlockTarget(t *testing.T) {
	rt := newReviewRuntime(t, true)
	capture(t, rt, rvTool("read_note", "Read a note", readOnlyHints()))
	h := reviewHashes(t, rt)

	save, calls := baselineSave(t, rt, "gone")
	_, err := rt.CommitServerApproval(rvServer, []string{"gone"}, h, "api", save)
	assert.Equal(t, []string{"gone"}, requireStale(t, err).MissingBlocks)
	requireNothingApproved(t, rt, calls)

	_, err = rt.CommitServerApproval(rvServer, []string{"gone"}, nil, "api", save)
	require.Error(t, err)
	assert.True(t, errors.Is(err, scanner.ErrToolApprovalBlockNotFound), "unbound: %v", err)
	assert.True(t, rt.serverQuarantinedInStorage(rvServer))
}

// An approved+enabled tool whose hints moved (here: unset -> spelled-out
// default, which keeps the rug-pull hash and the approved status) is in the
// activation set and fails the binding.
func TestCommitServerApproval_ApprovedEnabledHintMoveIsChanged(t *testing.T) {
	rt := newReviewRuntime(t, true)
	capture(t, rt, rvTool("read_note", "Read a note", nil))
	require.NoError(t, rt.ApproveTools(rvServer, []string{"read_note"}, "api"))
	h := reviewHashes(t, rt)
	capture(t, rt, rvTool("read_note", "Read a note", &config.ToolAnnotations{DestructiveHint: fpBool(true)}))
	require.Equal(t, storage.ToolApprovalStatusApproved, rvRecord(t, rt, "read_note").Status, "the effective hints did not move")

	save, calls := baselineSave(t, rt)
	_, err := rt.CommitServerApproval(rvServer, nil, h, "api", save)
	assert.Equal(t, []string{"read_note"}, requireStale(t, err).Changed)
	requireNothingApprovedMarker(t, rt, calls, true)
}

// US1 scenario 3 (composition with PR-1's marker): an empty review binds to
// the empty snapshot; tools first captured after the commit are held.
func TestCommitServerApproval_EmptyReviewBindsEmptySnapshot(t *testing.T) {
	rt := newReviewRuntime(t, true)
	save, _ := baselineSave(t, rt)
	n, err := rt.CommitServerApproval(rvServer, nil, map[string]string{}, "api", save)
	require.NoError(t, err)
	assert.Zero(t, n)
	require.NoError(t, rt.FinishServerApproval(rvServer))
	assert.False(t, rt.serverQuarantinedInStorage(rvServer))
	d, ok, err := rt.storageManager.GetToolBaselineDecision(rvServer)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, toolreview.BaselineSourceServerApproval, d.Source)

	capture(t, rt, rvTool("wipe", "Delete every note", destructiveHints()))
	assert.Equal(t, storage.ToolApprovalStatusPending, rvRecord(t, rt, "wipe").Status)

	// A tool captured BEFORE the commit fails the empty binding.
	rt2 := newReviewRuntime(t, true)
	capture(t, rt2, rvTool("wipe", "Delete every note", destructiveHints()))
	save2, calls2 := baselineSave(t, rt2)
	_, err = rt2.CommitServerApproval(rvServer, nil, map[string]string{}, "api", save2)
	assert.Equal(t, []string{"wipe"}, requireStale(t, err).Unreviewed)
	assert.Zero(t, calls2.Load())
}

// FR-012 (failing-first pair for T208): a record filed between the commit and
// the finish of an UNBOUND approval is not promoted. On main the
// post-unquarantine approveBaselineToolsForServer promoted it.
func TestServerApproval_ToolFiledAfterCommitStaysPending(t *testing.T) {
	rt := newReviewRuntime(t, true)
	capture(t, rt, rvTool("read_note", "Read a note", readOnlyHints()))
	save, _ := baselineSave(t, rt)
	n, err := rt.CommitServerApproval(rvServer, nil, nil, "api", save)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "the pending tool present at the commit is promoted")

	capture(t, rt, rvTool("read_note", "Read a note", readOnlyHints()), rvTool("late", "Filed after the commit", destructiveHints()))
	require.Equal(t, storage.ToolApprovalStatusPending, rvRecord(t, rt, "late").Status)

	require.NoError(t, rt.FinishServerApproval(rvServer))
	assert.Equal(t, storage.ToolApprovalStatusPending, rvRecord(t, rt, "late").Status, "FinishServerApproval must not promote")
	assert.Equal(t, storage.ToolApprovalStatusApproved, rvRecord(t, rt, "read_note").Status)
}

// Bound match: the validated pending set is promoted, the blocks are applied
// by commit, and the storage flip happens before the lock is released.
func TestCommitServerApproval_BoundMatchFlipsStorageUnderTheLock(t *testing.T) {
	rt := newReviewRuntime(t, true)
	capture(t, rt, rvTool("a", "A", readOnlyHints()), rvTool("b", "B", readOnlyHints()), rvTool("x", "X", destructiveHints()))
	h := reviewHashes(t, rt)

	saveBlocks, _ := baselineSave(t, rt, "x")
	var sawQuarantinedAfterLock atomic.Int32
	parked := make(chan struct{})
	n, err := rt.CommitServerApproval(rvServer, []string{"x"}, h, "api", func() error {
		// A competitor that queues for the lock now only gets it after the
		// section, storage flip included, is over.
		go func() {
			unlock := rt.lockToolApprovals(rvServer)
			if rt.serverQuarantinedInStorage(rvServer) {
				sawQuarantinedAfterLock.Store(1)
			}
			unlock()
			close(parked)
		}()
		deadline := time.Now().Add(5 * time.Second)
		for rt.approvalLockWaiter(rvServer).Load() == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		assert.True(t, rt.serverQuarantinedInStorage(rvServer), "still quarantined inside the section before the flip")
		return saveBlocks()
	})
	require.NoError(t, err)
	<-parked
	assert.Zero(t, sawQuarantinedAfterLock.Load(), "the storage flip must land before the lock is released")
	assert.Equal(t, 2, n)
	for _, name := range []string{"a", "b"} {
		assert.Equal(t, storage.ToolApprovalStatusApproved, rvRecord(t, rt, name).Status, name)
	}
	assert.True(t, rvRecord(t, rt, "x").Disabled)
}
