package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// The usage snapshot is flushed every 30s. Anything persisted after the last
// flush is saved but not folded in, so restoring the snapshot alone loses it —
// and because the aggregate is never rebuilt afterwards, the loss is permanent:
// the histogram drifts further from the activity list with every unclean stop.
//
// After init, the aggregate must contain the snapshot PLUS the tail.
func TestActivityService_SnapshotLoad_ReplaysRecordsPersistedAfterTheFlush(t *testing.T) {
	svc, mgr := newUsageTestService(t)
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	// One call that was both persisted and folded into the flushed snapshot.
	saveToolCall(t, mgr, "github", "search", "success", 1000, base)
	svc.usage = newUsageStore()
	svc.usage.Apply(&storage.ActivityRecord{
		Type: storage.ActivityTypeToolCall, ServerName: "github", ToolName: "search",
		Status: storage.ActivityStatusSuccess, ResponseBytes: 1000, Timestamp: base,
	})
	snap := svc.usage.Snapshot()
	require.NotNil(t, snap)
	// Stamp the snapshot at a known instant and persist it: everything written
	// after this point is the "lost tail".
	flushedAt := base.Add(time.Minute)
	snap.UpdatedAt = flushedAt
	data, err := encodeUsageAggregate(snap)
	require.NoError(t, err)
	require.NoError(t, mgr.SaveUsageSnapshot(data))

	// Two more calls land, then the process dies before the next flush.
	saveToolCall(t, mgr, "github", "search", "success", 500, flushedAt.Add(time.Second))
	saveToolCall(t, mgr, "github", "search", "error", 700, flushedAt.Add(2*time.Second))

	// Cold start.
	restarted, _ := newUsageTestServiceOn(t, mgr)
	restarted.initUsageFromStorage()

	loaded := restarted.UsageSnapshot()
	require.NotNil(t, loaded)
	tu := loaded.Tools[toolKey("github", "search")]
	require.NotNil(t, tu)
	assert.EqualValues(t, 3, tu.Calls, "the snapshot's call plus the two saved after the flush")
	assert.EqualValues(t, 1, tu.Errors)
	assert.EqualValues(t, 2200, tu.RespBytesSum)

	var timelineCalls int64
	for _, b := range loaded.Timeline() {
		timelineCalls += b.Calls
	}
	assert.EqualValues(t, 3, timelineCalls, "the timeline must catch up too")
}

// The replay bound is strictly greater than the snapshot stamp, so a record
// already inside the snapshot can never be counted a second time.
func TestActivityService_SnapshotLoad_DoesNotDoubleCountTheSnapshotItself(t *testing.T) {
	svc, mgr := newUsageTestService(t)
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	saveToolCall(t, mgr, "github", "search", "success", 1000, base)
	svc.usage = newUsageStore()
	svc.usage.Apply(&storage.ActivityRecord{
		Type: storage.ActivityTypeToolCall, ServerName: "github", ToolName: "search",
		Status: storage.ActivityStatusSuccess, ResponseBytes: 1000, Timestamp: base,
	})
	snap := svc.usage.Snapshot()
	snap.UpdatedAt = base.Add(time.Minute) // flushed after the record was written
	data, err := encodeUsageAggregate(snap)
	require.NoError(t, err)
	require.NoError(t, mgr.SaveUsageSnapshot(data))

	restarted, _ := newUsageTestServiceOn(t, mgr)
	restarted.initUsageFromStorage()

	tu := restarted.UsageSnapshot().Tools[toolKey("github", "search")]
	require.NotNil(t, tu)
	assert.EqualValues(t, 1, tu.Calls, "the record was already in the snapshot")
}

// A snapshot with no stamp gives no safe lower bound, so replaying from the
// beginning would double everything it already holds. It is left as loaded.
func TestActivityService_SnapshotLoad_SkipsReplayWhenTheSnapshotIsUndated(t *testing.T) {
	svc, mgr := newUsageTestService(t)
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	saveToolCall(t, mgr, "github", "search", "success", 1000, base)
	svc.usage = newUsageStore()
	svc.usage.Apply(&storage.ActivityRecord{
		Type: storage.ActivityTypeToolCall, ServerName: "github", ToolName: "search",
		Status: storage.ActivityStatusSuccess, ResponseBytes: 1000, Timestamp: base,
	})
	snap := svc.usage.Snapshot()
	snap.UpdatedAt = time.Time{}
	data, err := encodeUsageAggregate(snap)
	require.NoError(t, err)
	require.NoError(t, mgr.SaveUsageSnapshot(data))

	restarted, _ := newUsageTestServiceOn(t, mgr)
	restarted.initUsageFromStorage()

	tu := restarted.UsageSnapshot().Tools[toolKey("github", "search")]
	require.NotNil(t, tu)
	assert.EqualValues(t, 1, tu.Calls, "no replay, no double count")
}

// newUsageTestServiceOn builds a second ActivityService over an EXISTING
// storage manager, which is how these tests model a restart: same database,
// fresh in-memory aggregate.
func newUsageTestServiceOn(t *testing.T, mgr *storage.Manager) (*ActivityService, *storage.Manager) {
	t.Helper()
	return NewActivityService(mgr, zap.NewNop()), mgr
}

// A snapshot flushed by a build with a DIFFERENT admission rule cannot be
// patched incrementally — the hours it already contains were counted under the
// old rule (e.g. no internal calls). The loader must discard it and rebuild
// from the activity scan, once, so upgraded installs do not carry a 24h
// histogram that disagrees with the list.
func TestActivityService_SnapshotLoad_RebuildsWhenTheAdmissionRuleChanged(t *testing.T) {
	svc, mgr := newUsageTestService(t)
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)

	// The store holds an internal call the OLD rule never counted.
	require.NoError(t, mgr.SaveActivity(&storage.ActivityRecord{
		Type: storage.ActivityTypeInternalToolCall, ToolName: "code_execution",
		Status: storage.ActivityStatusSuccess, Timestamp: base,
	}))

	// A pre-upgrade snapshot: stamped, but with no AdmissionVersion (0).
	old := newUsageAggregate()
	old.AdmissionVersion = 0
	old.UpdatedAt = base.Add(time.Hour)
	data, err := encodeUsageAggregate(old)
	require.NoError(t, err)
	require.NoError(t, mgr.SaveUsageSnapshot(data))

	svc.initUsageFromStorage()

	loaded := svc.UsageSnapshot()
	require.NotNil(t, loaded)
	var timelineCalls int64
	for _, b := range loaded.Timeline() {
		timelineCalls += b.Calls
	}
	assert.EqualValues(t, 1, timelineCalls,
		"the stale-rule snapshot must be discarded and the internal call rescanned in")
}
