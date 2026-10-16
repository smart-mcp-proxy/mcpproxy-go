package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Spec 117 FR-018 / FR-032a (#1610, T306d): a capture whose writes do not all
// land is incomplete. Its server is review-dirty until a complete capture
// with a newer sequence is applied: nothing of the failed capture is
// published, dispatch and every reachability transition refuse with
// toolreview.ErrReviewNotRecorded, and the retained snapshot is not
// re-applied over partly written records. The absent-tool removal is derived
// from the stored records too, so a record left by a failed delete is removed
// by the next complete capture.

var errInjectedWrite = errors.New("injected storage write fault")

// failWrites makes the runtime's approval write seam fail for tool on op.
func failWrites(rt *Runtime, tool, op string) {
	rt.testHooks.toolApprovalWriteFault = func(_, got, gotOp string) error {
		if got == tool && gotOp == op {
			return errInjectedWrite
		}
		return nil
	}
}

func healWrites(rt *Runtime) {
	rt.testHooks.toolApprovalWriteFault = nil
	rt.testHooks.toolIndexWriteFault = nil
	rt.testHooks.toolIndexReadFault = nil
	rt.testHooks.discoveryPublishFault = nil
}

// captureAt applies tools as capture seq, published into the StateView.
func captureAt(t *testing.T, rt *Runtime, seq uint64, tools ...*config.ToolMetadata) (published bool, err error) {
	t.Helper()
	gen := rt.discoveryGeneration(baselineServer)
	_, published, err = rt.commitInventoryAt(context.Background(), baselineServer, tools, rt.committedGenForTest(baselineServer), &gen,
		inventoryRef{capture: managed.InventoryCapture{Seq: seq}})
	return published, err
}

func lockState(rt *Runtime) (applied, dirtyAt uint64, dirty bool) {
	st := rt.toolApprovalServerState(baselineServer)
	defer rt.lockToolApprovals(baselineServer)()
	return st.applied, st.dirtyAt, st.dirty
}

func requireNotRecorded(t *testing.T, err error, what string) {
	t.Helper()
	require.Error(t, err, what)
	assert.ErrorIs(t, err, toolreview.ErrReviewNotRecorded, what)
}

// seedApproved publishes an approved read-only read_note (seq 1).
func seedApproved(t *testing.T) *Runtime {
	t.Helper()
	rt := newBaselineRuntime(t, t.TempDir(), false)
	published, err := captureAt(t, rt, 1, readNote())
	require.NoError(t, err)
	require.True(t, published)
	require.Equal(t, storage.ToolApprovalStatusApproved, approvalOf(t, rt, "read_note").Status)
	require.False(t, rt.ToolReviewDirty(baselineServer))
	return rt
}

// (a), (b), (c), (d), (e), (g): a failed record write.
func TestCaptureFault_RecordWriteFailureMarksServerDirty(t *testing.T) {
	rt := seedApproved(t)
	svBefore, _, _ := stateViewTools(t, rt)
	snapBefore := lastGood(rt)
	recBefore := recordJSON(t, rt, "read_note")

	// C2 lists read_note with a destructive hint (the approval hash is
	// unchanged, so only the record's hint refresh records it) and that
	// write fails.
	failWrites(rt, "read_note", "save")
	_, err := captureAt(t, rt, 2, baselineTool("read_note", "Read a note", true))
	require.ErrorIs(t, err, errCaptureIncomplete)

	applied, dirtyAt, dirty := lockState(rt)
	assert.Equal(t, uint64(2), applied, "the failed capture is a fence")
	assert.True(t, dirty)
	assert.Equal(t, uint64(2), dirtyAt)
	assert.True(t, rt.ToolReviewDirty(baselineServer), "the lock-free mirror is set")

	svAfter, _, _ := stateViewTools(t, rt)
	assert.Equal(t, svBefore, svAfter, "the failed capture publishes nothing")
	snapAfter := lastGood(rt)
	assert.Equal(t, snapBefore.tools, snapAfter.tools, "last-good snapshot unchanged")
	assert.True(t, snapBefore.at.Equal(snapAfter.at), "capture stamp unchanged")
	assert.Equal(t, recBefore, recordJSON(t, rt, "read_note"))

	// (b) an older in-flight capture is stale and cannot clear the state.
	healWrites(rt)
	_, err = captureAt(t, rt, 1, readNote())
	require.NoError(t, err, "a stale capture is dropped silently")
	assert.True(t, rt.ToolReviewDirty(baselineServer), "a stale capture does not clear dirty")

	// (g) a lenient empty observation advances the fence and keeps dirty.
	require.True(t, rt.markServerZeroToolsDiscovered(baselineServer, rt.discoveryGeneration(baselineServer),
		rt.committedGenForTest(baselineServer), managed.InventoryCapture{Seq: 3}))
	applied, dirtyAt, _ = lockState(rt)
	assert.Equal(t, uint64(3), applied)
	assert.Equal(t, uint64(2), dirtyAt, "a lenient empty result never clears dirty")
	assert.True(t, rt.ToolReviewDirty(baselineServer))

	// (d) every transition that makes a tool reachable refuses; disabling
	// and blocking stay available.
	require.NoError(t, rt.SetToolEnabled(baselineServer, "read_note", false, "operator"), "disable stays available")
	requireNotRecorded(t, rt.SetToolEnabled(baselineServer, "read_note", true, "operator"), "enable of an approved record")
	_, err = rt.SetAllToolsEnabled(baselineServer, true, "operator")
	requireNotRecorded(t, err, "bulk enable")
	requireNotRecorded(t, rt.ApproveTools(baselineServer, []string{"read_note"}, "operator"), "ApproveTools")
	_, err = rt.CommitServerApproval(baselineServer, nil, nil, "operator", nil)
	requireNotRecorded(t, err, "server approval")
	require.True(t, approvalOf(t, rt, "read_note").Disabled, "the refused enable wrote nothing")

	// (c) the approval reindex does not re-apply the retained snapshot.
	assert.False(t, rt.reindexFromLastGoodSnapshot(baselineServer), "the reindex re-lists instead of replaying the snapshot")
	assert.True(t, rt.ToolReviewDirty(baselineServer))

	// (e) a complete capture newer than the failed one clears dirty and
	// publishes the recorded definition.
	_, err = captureAt(t, rt, 4, baselineTool("read_note", "Read a note", true))
	require.NoError(t, err)
	assert.False(t, rt.ToolReviewDirty(baselineServer), "a complete newer capture clears dirty")
	require.NoError(t, rt.SetToolEnabled(baselineServer, "read_note", true, "operator"))
	_, err = captureAt(t, rt, 5, baselineTool("read_note", "Read a note", true))
	require.NoError(t, err)
	svHealed, _, _ := stateViewTools(t, rt)
	hint, found := destructiveHintOf(svHealed, "read_note")
	require.True(t, found)
	assert.True(t, hint, "the healed capture publishes the destructive hint")
}

// A pending tool's approval is refused while dirty, from every entry point.
func TestCaptureFault_ApprovalOfPendingToolRefusedWhileDirty(t *testing.T) {
	rt := seedApproved(t)
	_, err := captureAt(t, rt, 2, readNote(), wipeAll())
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusPending, approvalOf(t, rt, "wipe_all_notes").Status)

	failWrites(rt, "wipe_all_notes", "save")
	_, err = captureAt(t, rt, 3, readNote(), baselineTool("wipe_all_notes", "Delete every note forever", true))
	require.ErrorIs(t, err, errCaptureIncomplete)
	healWrites(rt)

	requireNotRecorded(t, rt.ApproveTools(baselineServer, []string{"wipe_all_notes"}, "operator"), "ApproveTools")
	_, err = rt.ApproveAllTools(baselineServer, "operator")
	requireNotRecorded(t, err, "ApproveAllTools")
	// A bound approval carrying the (stale) stored fingerprint answers the
	// out-of-date error naming the tool whose write failed.
	rec := approvalOf(t, rt, "wipe_all_notes")
	expected := map[string]string{"wipe_all_notes": reviewFingerprint(rec)}
	_, err = rt.ApproveToolsReviewed(baselineServer, []string{"wipe_all_notes"}, expected, "operator")
	var stale *toolreview.StaleToolReviewError
	require.ErrorAs(t, err, &stale, "bound approval")
	assert.Equal(t, []string{"wipe_all_notes"}, stale.Changed)
	_, err = rt.ApproveAllToolsReviewed(baselineServer, expected, "operator")
	require.ErrorAs(t, err, &stale, "bound approve-all")
	assert.Equal(t, storage.ToolApprovalStatusPending, approvalOf(t, rt, "wipe_all_notes").Status, "nothing promoted")

	_, err = captureAt(t, rt, 4, readNote(), baselineTool("wipe_all_notes", "Delete every note forever", true))
	require.NoError(t, err)
	require.NoError(t, rt.ApproveTools(baselineServer, []string{"wipe_all_notes"}, "operator"))
}

// A failed index write after every record landed is an incomplete capture
// too (spec review r4 #1), and a fault-free repeat of the same seq does not
// clear it: only a newer one does.
func TestCaptureFault_IndexWriteFailureMarksServerDirty(t *testing.T) {
	rt := seedApproved(t)
	svBefore, _, _ := stateViewTools(t, rt)
	rt.testHooks.toolIndexWriteFault = func(string) error { return errInjectedWrite }

	_, err := captureAt(t, rt, 2, baselineTool("read_note", "Read a note", true))
	require.Error(t, err)
	assert.True(t, rt.ToolReviewDirty(baselineServer))
	svAfter, _, _ := stateViewTools(t, rt)
	assert.Equal(t, svBefore, svAfter, "nothing published after a failed index write")

	healWrites(rt)
	_, err = captureAt(t, rt, 2, baselineTool("read_note", "Read a note", true))
	require.NoError(t, err)
	assert.True(t, rt.ToolReviewDirty(baselineServer), "a capture at the failed seq does not clear dirty")
	_, err = captureAt(t, rt, 3, baselineTool("read_note", "Read a note", true))
	require.NoError(t, err)
	assert.False(t, rt.ToolReviewDirty(baselineServer))
}

// (k) a rejected or failed StateView publication is an incomplete capture:
// the snapshot and stamp stay at the prior capture.
func TestCaptureFault_PublicationFailureMarksServerDirty(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fault   func(string) (bool, error)
		wantErr bool
	}{
		{"rejected", func(string) (bool, error) { return false, nil }, false},
		{"error", func(string) (bool, error) { return false, errInjectedWrite }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := seedApproved(t)
			snapBefore := lastGood(rt)
			rt.testHooks.discoveryPublishFault = tc.fault

			published, err := captureAt(t, rt, 2, baselineTool("read_note", "Read a note", true))
			assert.False(t, published)
			if tc.wantErr {
				require.ErrorIs(t, err, errCaptureIncomplete)
			} else {
				require.NoError(t, err, "a rejected publication is re-listed by the caller")
			}
			assert.True(t, rt.ToolReviewDirty(baselineServer))
			snapAfter := lastGood(rt)
			assert.Equal(t, snapBefore.tools, snapAfter.tools, "snapshot not committed")
			assert.True(t, snapBefore.at.Equal(snapAfter.at), "stamp not committed")

			healWrites(rt)
			published, err = captureAt(t, rt, 3, baselineTool("read_note", "Read a note", true))
			require.NoError(t, err)
			assert.True(t, published)
			assert.False(t, rt.ToolReviewDirty(baselineServer))
			assert.False(t, lastGood(rt).at.Equal(snapBefore.at), "the accepted capture commits its stamp")
		})
	}
}

// A quarantined server's review capture whose record write fails marks it
// dirty: a server approval refuses, and the non-review unquarantine
// promotion leaves pending tools pending.
func TestCaptureFault_QuarantinedReviewCaptureMarksServerDirty(t *testing.T) {
	rt := newBaselineRuntime(t, t.TempDir(), true)
	gen := rt.committedGenForTest(baselineServer)
	_, err := rt.persistInventoryDefinitions(baselineServer, []*config.ToolMetadata{readNote()}, gen, managed.InventoryCapture{Seq: 1})
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusPending, approvalOf(t, rt, "read_note").Status)

	changed := baselineTool("read_note", "Read a note, then erase it", true)
	failWrites(rt, "read_note", "save")
	_, err = rt.persistInventoryDefinitions(baselineServer, []*config.ToolMetadata{changed}, gen, managed.InventoryCapture{Seq: 2})
	require.ErrorIs(t, err, errCaptureIncomplete)
	assert.True(t, rt.ToolReviewDirty(baselineServer))
	healWrites(rt)

	_, err = rt.CommitServerApproval(baselineServer, nil, nil, "operator", nil)
	requireNotRecorded(t, err, "server approval while dirty")
	n, err := rt.approveBaselineToolsForServerCount(baselineServer)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Equal(t, storage.ToolApprovalStatusPending, approvalOf(t, rt, "read_note").Status, "pending stays pending")

	_, err = rt.persistInventoryDefinitions(baselineServer, []*config.ToolMetadata{changed}, gen, managed.InventoryCapture{Seq: 3})
	require.NoError(t, err)
	assert.False(t, rt.ToolReviewDirty(baselineServer), "a complete review capture clears dirty")
}

func noteTool(name string) *config.ToolMetadata { return baselineTool(name, "Tool "+name, false) }

// (j) FR-032a removal recovery.
func TestCaptureFault_RemovalRecoversRecordLeftByFailedDelete(t *testing.T) {
	for _, tc := range []struct {
		name  string
		third []*config.ToolMetadata
	}{
		{"same inventory", []*config.ToolMetadata{noteTool("y")}},
		{"authoritative empty", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newBaselineRuntime(t, t.TempDir(), false)
			_, err := captureAt(t, rt, 1, noteTool("x"), noteTool("y"))
			require.NoError(t, err)
			require.Equal(t, storage.ToolApprovalStatusApproved, approvalOf(t, rt, "x").Status)

			// C2 drops x; the index delete lands, the record delete fails.
			failWrites(rt, "x", "delete-record")
			_, err = captureAt(t, rt, 2, noteTool("y"))
			require.ErrorIs(t, err, errCaptureIncomplete)
			assert.True(t, rt.ToolReviewDirty(baselineServer))
			assert.NotNil(t, approvalOf(t, rt, "x"), "the failed delete left x's record")
			assert.NotContains(t, indexedNames(t, rt), "x", "x is no longer indexed")

			healWrites(rt)
			_, err = captureAt(t, rt, 3, tc.third...)
			require.NoError(t, err)
			assert.Nil(t, approvalOf(t, rt, "x"), "the next complete capture removes x's record")
			assert.False(t, rt.ToolReviewDirty(baselineServer))

			// A reappearing x is reviewed again, never auto-approved.
			_, err = captureAt(t, rt, 4, noteTool("x"), noteTool("y"))
			require.NoError(t, err)
			assert.Equal(t, storage.ToolApprovalStatusPending, approvalOf(t, rt, "x").Status)
		})
	}
}

// FR-032a across a restart, without any fault hook: an approved record of a
// tool that is neither listed nor indexed (left behind by a pass that failed
// before the restart) is removed by the first capture. Fails on main, where
// the removal set is the index diff only.
func TestCaptureFault_RemovalReconcilesStoredRecordsAfterRestart(t *testing.T) {
	dir := t.TempDir()
	rt := newBaselineRuntime(t, dir, false)
	require.NoError(t, commitTools(t, rt, noteTool("y")))
	orphan := *approvalOf(t, rt, "y")
	orphan.ToolName = "x"
	require.NoError(t, rt.storageManager.SaveToolApproval(&orphan))
	closeBaselineRuntime(rt)

	rt2 := newBaselineRuntime(t, dir, false)
	require.NotNil(t, approvalOf(t, rt2, "x"))
	require.NoError(t, commitTools(t, rt2, noteTool("y")))
	assert.Nil(t, approvalOf(t, rt2, "x"), "the first capture after the restart removes the orphan")
	assert.NotNil(t, approvalOf(t, rt2, "y"))
}

// FR-032a retention: pending, changed and user-disabled records of absent
// tools, and a pre-105 collapsed alias of a served namespaced tool, survive.
func TestCaptureFault_RemovalRetainsDecisionsAndAliases(t *testing.T) {
	rt := newBaselineRuntime(t, t.TempDir(), false)
	require.NoError(t, commitTools(t, rt, noteTool("y")))
	base := *approvalOf(t, rt, "y")
	save := func(name string, mut func(*storage.ToolApprovalRecord)) {
		rec := base
		rec.ToolName = name
		mut(&rec)
		require.NoError(t, rt.storageManager.SaveToolApproval(&rec))
	}
	save("p", func(r *storage.ToolApprovalRecord) { r.Status = storage.ToolApprovalStatusPending })
	save("c", func(r *storage.ToolApprovalRecord) { r.Status = storage.ToolApprovalStatusChanged })
	save("d", func(r *storage.ToolApprovalRecord) { r.Disabled = true })
	save("erase", func(*storage.ToolApprovalRecord) {})

	require.NoError(t, commitTools(t, rt, noteTool("y"), noteTool("ns:erase")))
	for _, name := range []string{"p", "c", "d", "erase"} {
		assert.NotNil(t, approvalOf(t, rt, name), "%s survives the removal", name)
	}
}

// (h) a baseline-marker abort is an incomplete capture with the same fence
// and dirty mark (FR-027a composed with FR-018).
func TestCaptureFault_MarkerAbortMarksServerDirty(t *testing.T) {
	rt := seedApproved(t)
	dropBaselineMarkers(t, rt)
	failBaseline(rt, "write")
	_, err := captureAt(t, rt, 2, baselineTool("read_note", "Read a note", true))
	require.ErrorIs(t, err, errBaselineMarker)
	applied, dirtyAt, dirty := lockState(rt)
	assert.Equal(t, uint64(2), applied)
	assert.True(t, dirty)
	assert.Equal(t, uint64(2), dirtyAt)

	healBaseline(rt)
	_, err = captureAt(t, rt, 1, readNote())
	require.NoError(t, err)
	assert.True(t, rt.ToolReviewDirty(baselineServer), "the older capture is stale and cannot clear dirty")
	_, err = captureAt(t, rt, 3, baselineTool("read_note", "Read a note", true))
	require.NoError(t, err)
	assert.False(t, rt.ToolReviewDirty(baselineServer))
	_, ok := baselineDecision(t, rt)
	assert.True(t, ok, "the healed capture writes the marker")
}

// Review r1 (A1): when the indexed set cannot be read the pass re-indexes
// everything but cannot remove or evict, so it is not complete.
func TestCaptureFault_IndexReadFailureIsNotComplete(t *testing.T) {
	rt := seedApproved(t)
	rt.MarkToolReviewDirtyForTest(baselineServer, 2)
	rt.testHooks.toolIndexReadFault = func(string) error { return errInjectedWrite }
	_, err := captureAt(t, rt, 3, readNote())
	require.ErrorIs(t, err, errCaptureIncomplete)
	applied, dirtyAt, dirty := lockState(rt)
	assert.Equal(t, uint64(3), applied)
	assert.True(t, dirty, "the full-reindex fallback does not clear dirty")
	assert.Equal(t, uint64(3), dirtyAt)

	healWrites(rt)
	_, err = captureAt(t, rt, 4, readNote())
	require.NoError(t, err)
	assert.False(t, rt.ToolReviewDirty(baselineServer))
}

// Review r1 (A2): an index-derived removal keeps a user-disabled record whose
// stale index document survived an earlier failed removal.
func TestCaptureFault_IndexDerivedRemovalRetainsDisabledRecord(t *testing.T) {
	rt := newBaselineRuntime(t, t.TempDir(), false)
	_, err := captureAt(t, rt, 1, noteTool("x"), noteTool("y"))
	require.NoError(t, err)

	rt.testHooks.toolApprovalWriteFault = func(_, tool, op string) error {
		if tool == "x" && (op == "delete-index" || op == "delete-record") {
			return errInjectedWrite
		}
		return nil
	}
	_, err = captureAt(t, rt, 2, noteTool("y"))
	require.ErrorIs(t, err, errCaptureIncomplete)
	require.Contains(t, indexedNames(t, rt), "x", "the failed index delete left x indexed")
	healWrites(rt)

	require.NoError(t, rt.SetToolEnabled(baselineServer, "x", false, "operator"), "disable stays available while dirty")
	_, err = captureAt(t, rt, 3, noteTool("y"))
	require.NoError(t, err)
	assert.NotContains(t, indexedNames(t, rt), "x")
	rec := approvalOf(t, rt, "x")
	require.NotNil(t, rec, "the operator's disable survives the removal")
	assert.True(t, rec.Disabled)
}

// Review r1 (A3): a review capture whose prune of an absent pending record
// fails is incomplete.
func TestCaptureFault_QuarantinedPruneFailureMarksServerDirty(t *testing.T) {
	rt := newBaselineRuntime(t, t.TempDir(), true)
	gen := rt.committedGenForTest(baselineServer)
	_, err := rt.persistInventoryDefinitions(baselineServer, []*config.ToolMetadata{noteTool("a"), noteTool("b")}, gen, managed.InventoryCapture{Seq: 1})
	require.NoError(t, err)

	failWrites(rt, "b", "delete-record")
	_, err = rt.persistInventoryDefinitions(baselineServer, []*config.ToolMetadata{noteTool("a")}, gen, managed.InventoryCapture{Seq: 2})
	require.ErrorIs(t, err, errCaptureIncomplete)
	assert.True(t, rt.ToolReviewDirty(baselineServer))
	assert.NotNil(t, approvalOf(t, rt, "b"))

	healWrites(rt)
	_, err = rt.persistInventoryDefinitions(baselineServer, []*config.ToolMetadata{noteTool("a")}, gen, managed.InventoryCapture{Seq: 3})
	require.NoError(t, err)
	assert.False(t, rt.ToolReviewDirty(baselineServer))
	assert.Nil(t, approvalOf(t, rt, "b"))
}
