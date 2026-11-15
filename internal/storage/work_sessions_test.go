package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
)

var wsBase = time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC)

func wsRow(ws string, offset time.Duration, mutate func(*ActivityRecord)) *ActivityRecord {
	r := &ActivityRecord{
		Type: ActivityTypeToolCall, Status: ActivityStatusSuccess, ServerName: "github", ToolName: "list_issues",
		Timestamp: wsBase.Add(offset), WorkSessionID: ws, TokenName: "ci", TokenPrefix: "mcp_agt_aaaa",
		ClientName: "rest", Source: ActivitySourceAPI,
	}
	if mutate != nil {
		mutate(r)
	}
	return r
}

// putRawActivity stores a row the way a pre-124 build did: no index entry, no
// summary.
func putRawActivity(t *testing.T, m *Manager, r *ActivityRecord) {
	t.Helper()
	if r.ID == "" {
		r.ID = "old-" + r.Timestamp.Format("150405.000000000") + "-" + string(r.Type) + "-" + r.RequestID
	}
	data, err := r.MarshalBinary()
	require.NoError(t, err)
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(ActivityRecordsBucket)).Put(activityKey(r.Timestamp, r.ID), data)
	}))
}

// FR-010/FR-011/FR-016/FR-049: the row, its index entry and the summary are
// written together, and the summary keeps its facts per server.
func TestWorkSessionSummary_WrittenWithTheRow(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	require.NoError(t, m.SaveActivity(wsRow("ws-1", 0, func(r *ActivityRecord) { r.SessionID = "conn-1"; r.RunName = "nightly" })))
	require.NoError(t, m.SaveActivity(wsRow("ws-1", time.Second, func(r *ActivityRecord) {
		r.Type, r.ToolName = ActivityTypeInternalToolCall, "call_tool_read" // the mirror
	})))
	require.NoError(t, m.SaveActivity(wsRow("ws-1", 2*time.Second, func(r *ActivityRecord) {
		r.Status, r.ErrorClass, r.ErrorMessage = ActivityStatusError, "network", "connection reset by peer"
		r.ToolName, r.SessionID = "create_issue", "conn-2"
		r.Arguments = map[string]interface{}{"secret": "do-not-copy"}
		r.Response = "do-not-copy-either"
	})))
	require.NoError(t, m.SaveActivity(wsRow("ws-1", 3*time.Second, func(r *ActivityRecord) {
		r.Type, r.Status, r.ServerName, r.ToolName = ActivityTypePolicyDecision, "blocked", "linear", "delete_issue"
		r.Metadata = map[string]interface{}{"reason": "outside profile"}
	})))
	// A row with no work session writes nothing.
	require.NoError(t, m.SaveActivity(wsRow("", 4*time.Second, nil)))

	s, err := m.GetWorkSessionSummary("ws-1")
	require.NoError(t, err)
	require.NotNil(t, s)

	assert.Equal(t, "ci", s.TokenName)
	assert.Equal(t, "mcp_agt_aaaa", s.TokenPrefix)
	assert.False(t, s.Ownerless)
	assert.Equal(t, "nightly", s.RunName)
	assert.Equal(t, "api", s.Surface)
	assert.Equal(t, wsBase, s.StartedAt)
	assert.Equal(t, wsBase.Add(3*time.Second), s.LastActivityAt)
	assert.Equal(t, 2, s.ConnectionCount)

	gh := s.Servers["github"]
	require.NotNil(t, gh)
	assert.Equal(t, 2, gh.Attempts, "the mirror is not an attempt")
	assert.Equal(t, 1, gh.Failures)
	assert.Equal(t, 0, gh.Blocks)
	assert.Equal(t, "network", gh.LastErrorClass)
	assert.Equal(t, "create_issue", gh.LastTool)
	assert.Equal(t, "connection reset by peer", gh.LastReason)
	assert.NotContains(t, gh.LastReason, "do-not-copy", "the reason never comes from a body")

	lin := s.Servers["linear"]
	require.NotNil(t, lin)
	assert.Equal(t, WorkSessionPartition{
		Attempts: 1, Failures: 1, Blocks: 1, StoredFailures: 1, StoredBlocks: 1,
		FirstActivityAt: wsBase.Add(3 * time.Second), LastActivityAt: wsBase.Add(3 * time.Second),
		FirstFailureAt: wsBase.Add(3 * time.Second), LastFailureAt: wsBase.Add(3 * time.Second),
		LastErrorClass: ErrorClassProxyPolicy, LastTool: "delete_issue", LastReason: "outside profile", LastFailureBlock: true,
	}, *lin)

	all := s.Totals(nil)
	assert.Equal(t, 3, all.Attempts)
	assert.Equal(t, 2, all.Failures)
	assert.Equal(t, 1, all.Blocks)
	assert.True(t, all.LastFailureBlock)

	// A scoped view sums only the servers in reach.
	onlyGH := s.Totals(func(server string) bool { return server == "github" })
	assert.Equal(t, 2, onlyGH.Attempts)
	assert.Equal(t, 1, onlyGH.Failures)
	assert.Equal(t, 0, onlyGH.Blocks)
	assert.Equal(t, wsBase.Add(2*time.Second), onlyGH.LastActivityAt)

	// The index is in time order and carries each row's role.
	refs, err := m.WorkSessionRowRefs("ws-1")
	require.NoError(t, err)
	require.Len(t, refs, 4)
	for i := 1; i < len(refs); i++ {
		assert.True(t, refs[i-1].Timestamp.Before(refs[i].Timestamp))
	}
	assert.True(t, refs[0].Counted && !refs[0].Failed)
	assert.False(t, refs[1].Counted)
	assert.True(t, refs[2].Failed && !refs[2].Block)
	assert.True(t, refs[3].Failed && refs[3].Block)
	assert.Equal(t, "linear", refs[3].Server)

	none, err := m.GetWorkSessionSummary("")
	require.NoError(t, err)
	assert.Nil(t, none)
}

// Saving a row again (a rewrite of the same key) never counts it twice.
func TestWorkSessionSummary_ResavedRowIsCountedOnce(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	r := wsRow("ws-1", 0, func(r *ActivityRecord) { r.Status = ActivityStatusError })
	require.NoError(t, m.SaveActivity(r))
	require.NoError(t, m.SaveActivity(r))

	s, err := m.GetWorkSessionSummary("ws-1")
	require.NoError(t, err)
	assert.Equal(t, 1, s.Totals(nil).Attempts)
	assert.Equal(t, 1, s.Totals(nil).Failures)
}

// FR-012: the reason is bounded in characters.
func TestWorkSessionSummary_ReasonIsBounded(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	require.NoError(t, m.SaveActivity(wsRow("ws-1", 0, func(r *ActivityRecord) {
		r.Status, r.ErrorMessage = ActivityStatusError, strings.Repeat("ж", 500)
	})))
	s, err := m.GetWorkSessionSummary("ws-1")
	require.NoError(t, err)
	assert.Len(t, []rune(s.Servers["github"].LastReason), MaxWorkSessionReasonLen)
}

// FR-015: a session gets its end once idle, and a later row reopens it.
func TestWorkSessionSummary_EndIsRecordedAndReopened(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	require.NoError(t, m.SaveActivity(wsRow("ws-1", 0, nil)))

	n, err := m.MarkWorkSessionsEnded(wsBase.Add(-time.Minute))
	require.NoError(t, err)
	assert.Zero(t, n, "not idle yet")

	n, err = m.MarkWorkSessionsEnded(wsBase.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	s, _ := m.GetWorkSessionSummary("ws-1")
	assert.Equal(t, wsBase, s.EndedAt)

	require.NoError(t, m.SaveActivity(wsRow("ws-1", 2*time.Hour, nil)))
	s, _ = m.GetWorkSessionSummary("ws-1")
	assert.True(t, s.EndedAt.IsZero(), "a later row makes the session active again")
}

// FR-014/FR-018 and D2: the backfill indexes old rows once, pairs old
// completion and blocked-decision rows, and is idempotent.
func TestWorkSessionBackfill_PairsOldRowsAndIsIdempotent(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	old := func(offset time.Duration, mutate func(*ActivityRecord)) {
		putRawActivity(t, m, wsRow("ws-old", offset, mutate))
	}
	// Attempt A: completion, then its blocking decision (generation change).
	old(0, func(r *ActivityRecord) { r.RequestID, r.Status = "req-a", ActivityStatusError })
	old(10*time.Millisecond, func(r *ActivityRecord) {
		r.RequestID, r.Type, r.Status = "req-a", ActivityTypePolicyDecision, "blocked"
	})
	// Attempt B: the decision first (output sanitisation), then the completion.
	old(time.Minute, func(r *ActivityRecord) {
		r.RequestID, r.Type, r.Status = "req-b", ActivityTypePolicyDecision, "blocked"
	})
	old(time.Minute+20*time.Millisecond, func(r *ActivityRecord) { r.RequestID, r.Status = "req-b", ActivityStatusError })
	// Attempt C: a sole blocking decision (pre-dispatch refusal).
	old(2*time.Minute, func(r *ActivityRecord) {
		r.RequestID, r.Type, r.Status = "req-c", ActivityTypePolicyDecision, "blocked"
	})
	// Attempts D and E: two old limiter sheds that share a transport id.
	old(3*time.Minute, func(r *ActivityRecord) { r.RequestID, r.Status, r.ID = "req-shared", ActivityStatusRejected, "shed-1" })
	old(3*time.Minute+time.Millisecond, func(r *ActivityRecord) {
		r.RequestID, r.Status, r.ID = "req-shared", ActivityStatusRejected, "shed-2"
	})
	// A row with no work session is left alone.
	old(4*time.Minute, func(r *ActivityRecord) { r.WorkSessionID = "" })

	indexed, err := m.BackfillWorkSessions(nil)
	require.NoError(t, err)
	assert.Equal(t, 7, indexed)

	s, err := m.GetWorkSessionSummary("ws-old")
	require.NoError(t, err)
	require.NotNil(t, s)
	tot := s.Totals(nil)
	assert.Equal(t, 5, tot.Attempts, "A, B, C, D, E")
	assert.Equal(t, 5, tot.Failures)
	assert.Equal(t, 3, tot.Blocks, "C and the two sheds")

	again, err := m.BackfillWorkSessions(nil)
	require.NoError(t, err)
	assert.Zero(t, again, "the marker stops a second pass")
}

// A pair split across two batches is still one attempt.
func TestWorkSessionBackfill_PairAcrossABatchBoundary(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// backfillBatch-1 filler rows, then the decision as the last row of the
	// first batch and its completion as the first row of the second.
	for i := 0; i < backfillBatch-1; i++ {
		putRawActivity(t, m, wsRow("ws-fill", time.Duration(i)*time.Microsecond, func(r *ActivityRecord) {
			r.ID = "fill-" + time.Duration(i).String()
		}))
	}
	at := time.Second
	putRawActivity(t, m, wsRow("ws-old", at, func(r *ActivityRecord) {
		r.RequestID, r.Type, r.Status = "req-x", ActivityTypePolicyDecision, "blocked"
	}))
	putRawActivity(t, m, wsRow("ws-old", at+time.Millisecond, func(r *ActivityRecord) {
		r.RequestID, r.Status = "req-x", ActivityStatusError
	}))

	_, err := m.BackfillWorkSessions(nil)
	require.NoError(t, err)
	s, err := m.GetWorkSessionSummary("ws-old")
	require.NoError(t, err)
	assert.Equal(t, 1, s.Totals(nil).Attempts)
	assert.Equal(t, 0, s.Totals(nil).Blocks, "the tool_call row is the counted one")
}

// FR-018: a row written while the pass is pending is counted exactly once.
func TestWorkSessionBackfill_LiveRowIsNotCountedAgain(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	putRawActivity(t, m, wsRow("ws-1", 0, func(r *ActivityRecord) { r.Status = ActivityStatusError }))
	require.NoError(t, m.SaveActivity(wsRow("ws-1", time.Second, func(r *ActivityRecord) { r.Status = ActivityStatusError })))

	indexed, err := m.BackfillWorkSessions(nil)
	require.NoError(t, err)
	assert.Equal(t, 1, indexed)
	s, _ := m.GetWorkSessionSummary("ws-1")
	assert.Equal(t, 2, s.Totals(nil).Failures)
}

// FR-014: old rows of several credentials under one id give an ownerless
// session.
func TestWorkSessionBackfill_MixedCredentialsAreOwnerless(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	putRawActivity(t, m, wsRow("ws-mixed", 0, nil))
	putRawActivity(t, m, wsRow("ws-mixed", time.Second, func(r *ActivityRecord) {
		r.TokenName, r.TokenPrefix = "other", "mcp_agt_bbbb"
	}))
	_, err := m.BackfillWorkSessions(nil)
	require.NoError(t, err)
	s, _ := m.GetWorkSessionSummary("ws-mixed")
	assert.True(t, s.Ownerless)
}

// FR-013: a missing summary is rebuilt from the retained rows with the roles
// stored in the index, and an existing one is never touched.
func TestWorkSessionRepair_RebuildsAMissingSummary(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// An old pair, so the stored role (not a fresh classification) matters.
	putRawActivity(t, m, wsRow("ws-1", 0, func(r *ActivityRecord) { r.RequestID, r.Status, r.ErrorMessage = "req-a", ActivityStatusError, "boom" }))
	putRawActivity(t, m, wsRow("ws-1", time.Millisecond, func(r *ActivityRecord) {
		r.RequestID, r.Type, r.Status = "req-a", ActivityTypePolicyDecision, "blocked"
	}))
	_, err := m.BackfillWorkSessions(nil)
	require.NoError(t, err)
	require.NoError(t, m.SaveActivity(wsRow("ws-keep", 0, nil)))

	before, _ := m.GetWorkSessionSummary("ws-1")
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(WorkSessionsBucket)).Delete([]byte("ws-1"))
	}))

	n, err := m.RepairWorkSessionSummaries()
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	after, err := m.GetWorkSessionSummary("ws-1")
	require.NoError(t, err)
	require.NotNil(t, after)
	assert.True(t, after.Rebuilt)
	assert.Equal(t, before.Totals(nil).Attempts, after.Totals(nil).Attempts)
	assert.Equal(t, 1, after.Totals(nil).Attempts, "the stored role keeps the old pair at one attempt")
	assert.Equal(t, before.TokenName, after.TokenName)
	assert.Equal(t, before.TokenPrefix, after.TokenPrefix)
	assert.Equal(t, before.ClientName, after.ClientName)
	assert.Equal(t, "boom", after.Servers["github"].LastReason)
	assert.Equal(t, before.Servers["github"].LastFailureAt, after.Servers["github"].LastFailureAt)

	kept, _ := m.GetWorkSessionSummary("ws-keep")
	assert.False(t, kept.Rebuilt, "an existing summary is left alone")
}
