package runtime

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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
	destructiveRead := baselineTool("read_note", "Read a note", true)

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
