package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 117 PR-1 (#1567, T104f): a baseline-marker failure aborts the
// destructive half of a discovery pass (FR-027) before the pass writes any
// record (FR-027a), and leaves the last-good snapshot and capture stamp alone.

var errInjectedBaseline = errors.New("injected baseline storage fault")

// dropBaselineMarkers turns rt's store into an "older store": approval
// records but no baseline marker.
func dropBaselineMarkers(t *testing.T, rt *Runtime) {
	t.Helper()
	require.NoError(t, rt.storageManager.GetDB().Update(func(tx *bbolt.Tx) error {
		if tx.Bucket([]byte(storage.ToolBaselineDecisionsBucket)) == nil {
			return nil
		}
		return tx.DeleteBucket([]byte(storage.ToolBaselineDecisionsBucket))
	}))
}

func failBaseline(rt *Runtime, op string) {
	rt.testHooks.toolBaselineFault = func(_ string, got string) error {
		if got == op {
			return errInjectedBaseline
		}
		return nil
	}
}

func healBaseline(rt *Runtime) { rt.testHooks.toolBaselineFault = nil }

type snapState struct {
	tools []*config.ToolMetadata
	at    time.Time
}

func lastGood(rt *Runtime) snapState {
	rt.lastGoodToolsMu.RLock()
	defer rt.lastGoodToolsMu.RUnlock()
	return snapState{tools: append([]*config.ToolMetadata(nil), rt.lastGoodTools[baselineServer]...), at: rt.lastCaptureAt[baselineServer]}
}

func recordJSON(t *testing.T, rt *Runtime, tool string) string {
	t.Helper()
	rec := approvalOf(t, rt, tool)
	if rec == nil {
		return ""
	}
	b, err := json.Marshal(rec)
	require.NoError(t, err)
	return string(b)
}

// olderStoreWithApprovedReadNote: approved+indexed read_note, no marker.
func olderStoreWithApprovedReadNote(t *testing.T) *Runtime {
	t.Helper()
	rt := newBaselineRuntime(t, t.TempDir(), false)
	require.NoError(t, commitTools(t, rt, readNote()))
	dropBaselineMarkers(t, rt)
	require.Equal(t, storage.ToolApprovalStatusApproved, approvalOf(t, rt, "read_note").Status)
	return rt
}

func TestToolBaselineMarkerFault_ReplacementAbortsDestructiveHalf(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools []*config.ToolMetadata
	}{
		{"replaced inventory", []*config.ToolMetadata{wipeAll()}},
		{"authoritative empty", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := olderStoreWithApprovedReadNote(t)
			before := lastGood(rt)
			recBefore := recordJSON(t, rt, "read_note")

			failBaseline(rt, "write")
			err := commitTools(t, rt, tc.tools...)
			require.Error(t, err)
			assert.ErrorIs(t, err, errBaselineMarker)

			assert.Equal(t, recBefore, recordJSON(t, rt, "read_note"), "the absent tool's record is not deleted")
			assert.Nil(t, approvalOf(t, rt, "wipe_all_notes"), "the aborted pass writes no record")
			assert.Contains(t, indexedNames(t, rt), "read_note", "no absent-tool index delete")
			assert.NotContains(t, indexedNames(t, rt), "wipe_all_notes", "nothing new is indexed")
			after := lastGood(rt)
			assert.Equal(t, before.tools, after.tools, "last-good snapshot unchanged")
			assert.True(t, before.at.Equal(after.at), "capture stamp unchanged")

			healBaseline(rt)
			require.NoError(t, commitTools(t, rt, tc.tools...))
			d, ok := baselineDecision(t, rt)
			require.True(t, ok)
			assert.Equal(t, toolreview.BaselineSourceInferred, d.Source)
			assert.Nil(t, approvalOf(t, rt, "read_note"), "healed pass removes the absent record")
			if len(tc.tools) > 0 {
				requireHeldUnindexed(t, rt, "wipe_all_notes", "healed")
			} else {
				require.NoError(t, commitTools(t, rt, wipeAll()))
				require.NoError(t, commitTools(t, rt, wipeAll()))
				requireHeldUnindexed(t, rt, "wipe_all_notes", "after healed empty")
			}
		})
	}
}

// FR-027a: a hint update on an approved tool and a failing marker write leave
// the stored record byte-identical (the marker step precedes every write).
func TestToolBaselineMarkerFault_HintUpdateWritesNothing(t *testing.T) {
	rt := olderStoreWithApprovedReadNote(t)
	before := lastGood(rt)
	recBefore := recordJSON(t, rt, "read_note")
	destructiveRead := readNoteRetitled()

	failBaseline(rt, "write")
	err := commitTools(t, rt, destructiveRead)
	require.ErrorIs(t, err, errBaselineMarker)
	assert.Equal(t, recBefore, recordJSON(t, rt, "read_note"), "record untouched: still the read-only capture")
	after := lastGood(rt)
	assert.Equal(t, before.tools, after.tools)
	assert.True(t, before.at.Equal(after.at))

	healBaseline(rt)
	require.NoError(t, commitTools(t, rt, destructiveRead))
	d, ok := baselineDecision(t, rt)
	require.True(t, ok)
	assert.Equal(t, toolreview.BaselineSourceInferred, d.Source)
	rec := approvalOf(t, rt, "read_note")
	require.NotNil(t, rec.CurrentAnnotations)
	require.NotNil(t, rec.CurrentAnnotations.DestructiveHint)
	assert.True(t, *rec.CurrentAnnotations.DestructiveHint, "the healed pass records the new hints")
}

// FR-027a: a failed prior-record listing must not read as "no baseline" (main
// auto-approved the whole inventory on it).
func TestToolBaselineMarkerFault_ListingFailureNeverAutoBaselines(t *testing.T) {
	rt := olderStoreWithApprovedReadNote(t)
	failBaseline(rt, "list")
	err := commitTools(t, rt, wipeAll())
	require.ErrorIs(t, err, errBaselineMarker)
	assert.Nil(t, approvalOf(t, rt, "wipe_all_notes"), "no record written, nothing auto-baselined")
	assert.Contains(t, indexedNames(t, rt), "read_note")
	assert.NotContains(t, indexedNames(t, rt), "wipe_all_notes")
}

// A marker read failure fails closed the same way.
func TestToolBaselineMarkerFault_ReadFailureAborts(t *testing.T) {
	rt := newBaselineRuntime(t, t.TempDir(), false)
	failBaseline(rt, "read")
	err := commitTools(t, rt, wipeAll())
	require.ErrorIs(t, err, errBaselineMarker)
	assert.Nil(t, approvalOf(t, rt, "wipe_all_notes"), "an unreadable marker never auto-baselines")
}

// The discovery marker (no prior approved record) is not an abort: the pass's
// own approvals establish the baseline by inference, and the next pass writes
// the inferred marker before it can delete anything.
func TestToolBaselineMarkerFault_DiscoveryMarkerFailureIsNotAnAbort(t *testing.T) {
	rt := newBaselineRuntime(t, t.TempDir(), false)
	failBaseline(rt, "write")
	require.NoError(t, commitTools(t, rt, readNote()))
	rec := approvalOf(t, rt, "read_note")
	require.NotNil(t, rec)
	assert.Equal(t, "auto-baseline", rec.ApprovedBy)
	assert.Contains(t, indexedNames(t, rt), "read_note")
	_, ok := baselineDecision(t, rt)
	require.False(t, ok)

	healBaseline(rt)
	require.NoError(t, commitTools(t, rt, wipeAll()))
	d, ok := baselineDecision(t, rt)
	require.True(t, ok)
	assert.Equal(t, toolreview.BaselineSourceInferred, d.Source)
	requireHeldUnindexed(t, rt, "wipe_all_notes", "after discovery-marker fault")
}

// stateViewTools is the StateView's published view of baselineServer.
func stateViewTools(t *testing.T, rt *Runtime) (tools []stateview.ToolInfo, discovered bool, epoch int64) {
	t.Helper()
	st, ok := rt.supervisor.StateView().GetServer(baselineServer)
	require.True(t, ok, "the server is in the StateView")
	return append([]stateview.ToolInfo(nil), st.Tools...), st.ToolsDiscovered, st.DiscoveryEpoch
}

// titleOf is the published annotation title of a StateView tool.
func titleOf(tools []stateview.ToolInfo, name string) (title string, found bool) {
	for _, ti := range tools {
		if ti.Name == name || ti.Name == baselineServer+":"+name {
			if ti.Annotations != nil {
				return ti.Annotations.Title, true
			}
			return "", true
		}
	}
	return "", false
}

func destructiveHintOf(tools []stateview.ToolInfo, name string) (hint, found bool) {
	for _, ti := range tools {
		if ti.Name == name || ti.Name == baselineServer+":"+name {
			if ti.Annotations != nil && ti.Annotations.DestructiveHint != nil {
				return *ti.Annotations.DestructiveHint, true
			}
			return false, true
		}
	}
	return false, false
}

// FR-027a with a published capture (review round 1, finding 2): the marker
// step of a capture-carrying pass precedes the StateView publication, so a
// failed marker write publishes nothing — no new destructive hint, no new
// tool, no discovery-identity change — while the old records, snapshot and
// capture stamp stay in place. The plain commitTools tests pass a nil capture
// and never reach the publication branch.
func TestToolBaselineMarkerFault_CaptureIsNotPublished(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools []*config.ToolMetadata
	}{
		{"title update", []*config.ToolMetadata{readNoteRetitled()}},
		{"replaced inventory", []*config.ToolMetadata{wipeAll()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newBaselineRuntime(t, t.TempDir(), false)
			require.NotNil(t, rt.supervisor)
			capture := rt.discoveryGeneration(baselineServer)
			eligible, published, err := rt.commitDiscoveredToolsAt(context.Background(), baselineServer, []*config.ToolMetadata{readNote()}, rt.committedGenForTest(baselineServer), &capture)
			require.NoError(t, err)
			require.True(t, eligible)
			require.True(t, published, "seed capture is published")
			dropBaselineMarkers(t, rt)

			svBefore, discBefore, epochBefore := stateViewTools(t, rt)
			require.True(t, discBefore)
			title, found := titleOf(svBefore, "read_note")
			require.True(t, found, "seed publishes read_note")
			require.Empty(t, title)
			snapBefore := lastGood(rt)
			recBefore := recordJSON(t, rt, "read_note")

			failBaseline(rt, "write")
			capture = rt.discoveryGeneration(baselineServer)
			eligible, published, err = rt.commitDiscoveredToolsAt(context.Background(), baselineServer, tc.tools, rt.committedGenForTest(baselineServer), &capture)
			require.ErrorIs(t, err, errBaselineMarker)
			assert.True(t, eligible)
			assert.False(t, published, "a marker failure publishes nothing")

			svAfter, discAfter, epochAfter := stateViewTools(t, rt)
			assert.Equal(t, svBefore, svAfter, "StateView tools unchanged")
			assert.Equal(t, discBefore, discAfter, "discovery completion unchanged")
			assert.Equal(t, epochBefore, epochAfter, "discovery identity unchanged")
			title, _ = titleOf(svAfter, "read_note")
			assert.Empty(t, title, "the failed capture's retitled definition is not published")
			_, found = destructiveHintOf(svAfter, "wipe_all_notes")
			assert.False(t, found, "the failed capture's new tool is not published")
			assert.Equal(t, recBefore, recordJSON(t, rt, "read_note"), "record untouched")
			assert.Nil(t, approvalOf(t, rt, "wipe_all_notes"))
			snapAfter := lastGood(rt)
			assert.Equal(t, snapBefore.tools, snapAfter.tools, "last-good snapshot unchanged")
			assert.True(t, snapBefore.at.Equal(snapAfter.at), "capture stamp unchanged")
			_, ok := baselineDecision(t, rt)
			assert.False(t, ok, "no marker written")

			healBaseline(rt)
			capture = rt.discoveryGeneration(baselineServer)
			_, published, err = rt.commitDiscoveredToolsAt(context.Background(), baselineServer, tc.tools, rt.committedGenForTest(baselineServer), &capture)
			require.NoError(t, err)
			assert.True(t, published, "the healed pass publishes")
		})
	}
}
