package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newActivityTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr, err := NewManager(t.TempDir(), zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

// A code_execution and the sub-calls its sandbox issued are linked by one id in
// both directions, and each direction has to be a single query:
//
//	parent → children: filter by ParentID
//	child  → parent:   filter by RequestID
func TestActivityFilter_ParentID_SelectsChildrenOfOneParent(t *testing.T) {
	parent := &ActivityRecord{
		Type:      ActivityTypeInternalToolCall,
		ToolName:  "code_execution",
		Status:    ActivityStatusSuccess,
		RequestID: "parent-1",
	}
	child := &ActivityRecord{
		Type:       ActivityTypeToolCall,
		ServerName: "github",
		ToolName:   "search",
		Status:     ActivityStatusSuccess,
		RequestID:  "child-1",
		ParentID:   "parent-1",
	}
	otherChild := &ActivityRecord{
		Type:       ActivityTypeToolCall,
		ServerName: "github",
		ToolName:   "search",
		Status:     ActivityStatusSuccess,
		RequestID:  "child-2",
		ParentID:   "parent-2",
	}
	topLevel := &ActivityRecord{
		Type:       ActivityTypeToolCall,
		ServerName: "github",
		ToolName:   "search",
		Status:     ActivityStatusSuccess,
		RequestID:  "top-1",
	}

	byParent := ActivityFilter{ParentID: "parent-1"}
	assert.True(t, byParent.Matches(child))
	assert.False(t, byParent.Matches(otherChild))
	assert.False(t, byParent.Matches(topLevel), "a top-level call has no parent")
	assert.False(t, byParent.Matches(parent), "the parent is not its own child")

	byRequest := ActivityFilter{RequestID: child.ParentID}
	assert.True(t, byRequest.Matches(parent), "child -> parent is one request_id query")
	assert.False(t, byRequest.Matches(child))
}

// The default filter drops the internal call_tool_* rows a direct dispatch
// already covers. Sandbox sub-calls are tool_call records, so that exclusion
// must not touch them — otherwise the feature would ship invisible.
func TestActivityFilter_DefaultDoesNotHideSandboxChildren(t *testing.T) {
	filter := DefaultActivityFilter()
	require.True(t, filter.ExcludeCallToolSuccess)

	child := &ActivityRecord{
		Type:       ActivityTypeToolCall,
		ServerName: "github",
		ToolName:   "search",
		Status:     ActivityStatusSuccess,
		RequestID:  "child-1",
		ParentID:   "parent-1",
	}
	assert.True(t, filter.Matches(child))
}

// ParentID must survive the BBolt round-trip: it is a first-class field, not
// metadata, precisely so it can be stored AND filtered on.
func TestActivityRecord_ParentIDRoundTripsThroughStorage(t *testing.T) {
	mgr := newActivityTestManager(t)
	base := time.Now().UTC().Add(-time.Minute)

	require.NoError(t, mgr.SaveActivity(&ActivityRecord{
		Type: ActivityTypeInternalToolCall, ToolName: "code_execution",
		Status: ActivityStatusSuccess, RequestID: "parent-1", Timestamp: base,
	}))
	require.NoError(t, mgr.SaveActivity(&ActivityRecord{
		Type: ActivityTypeToolCall, ServerName: "github", ToolName: "search",
		Status: ActivityStatusSuccess, RequestID: "child-1", ParentID: "parent-1",
		Timestamp: base.Add(time.Second),
	}))
	require.NoError(t, mgr.SaveActivity(&ActivityRecord{
		Type: ActivityTypeToolCall, ServerName: "github", ToolName: "search",
		Status: ActivityStatusError, RequestID: "child-2", ParentID: "parent-1",
		Timestamp: base.Add(2 * time.Second),
	}))
	require.NoError(t, mgr.SaveActivity(&ActivityRecord{
		Type: ActivityTypeToolCall, ServerName: "github", ToolName: "search",
		Status: ActivityStatusSuccess, RequestID: "unrelated", Timestamp: base.Add(3 * time.Second),
	}))

	records, total, err := mgr.ListActivities(ActivityFilter{ParentID: "parent-1"})
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, records, 2)
	for _, rec := range records {
		assert.Equal(t, "parent-1", rec.ParentID)
	}
}

// ScanActivitiesAfter is the usage aggregate's catch-up replay. Its bound is
// STRICTLY greater than the snapshot stamp, because a record at or before the
// stamp is already folded in and re-applying it would double it.
func TestScanActivitiesAfter_IsStrictlyExclusiveOfTheBound(t *testing.T) {
	mgr := newActivityTestManager(t)
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	for i, ts := range []time.Time{base.Add(-time.Minute), base, base.Add(time.Nanosecond), base.Add(time.Minute)} {
		require.NoError(t, mgr.SaveActivity(&ActivityRecord{
			Type: ActivityTypeToolCall, ServerName: "s", ToolName: "t",
			Status: ActivityStatusSuccess, RequestID: string(rune('a' + i)), Timestamp: ts,
		}))
	}

	var seen []string
	require.NoError(t, mgr.ScanActivitiesAfter(base, func(rec *ActivityRecord) {
		seen = append(seen, rec.RequestID)
	}))
	assert.Equal(t, []string{"c", "d"}, seen,
		"the record AT the bound is already counted; only strictly newer ones replay")

	assert.Error(t, mgr.ScanActivitiesAfter(time.Time{}, func(*ActivityRecord) {}),
		"a zero bound has no safe meaning and must not silently replay everything")
}
