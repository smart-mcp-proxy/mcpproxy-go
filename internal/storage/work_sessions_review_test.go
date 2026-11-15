package storage

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
)

// Regression tests for the findings of the first code review of the storage
// chunk (Spec 124).

// A legacy row carries its credential only in the internal arguments. A
// session with such a row of another credential is ownerless; it must not stay
// owned by the first credential.
func TestWorkSessionSummary_LegacyArgumentIdentityCountsForOwnership(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	require.NoError(t, m.SaveActivity(wsRow("ws-1", 0, nil))) // ci / mcp_agt_aaaa
	require.NoError(t, m.SaveActivity(wsRow("ws-1", time.Second, func(r *ActivityRecord) {
		r.TokenName, r.TokenPrefix = "", ""
		r.Arguments = map[string]interface{}{authArgAgentName: "other", authArgTokenPrefix: "mcp_agt_bbbb"}
	})))
	s, err := m.GetWorkSessionSummary("ws-1")
	require.NoError(t, err)
	assert.True(t, s.Ownerless)

	// And a legacy row alone names the owner.
	require.NoError(t, m.SaveActivity(wsRow("ws-2", 0, func(r *ActivityRecord) {
		r.TokenName, r.TokenPrefix = "", ""
		r.Arguments = map[string]interface{}{authArgAgentName: "legacy", authArgTokenPrefix: "mcp_agt_cccc"}
	})))
	s2, _ := m.GetWorkSessionSummary("ws-2")
	assert.Equal(t, "legacy", s2.TokenName)
	assert.Equal(t, "mcp_agt_cccc", s2.TokenPrefix)
	assert.False(t, s2.Ownerless)
}

// A whole-session removal interrupted after its first batch is marked on the
// summary, hidden from readers, and finished at the next start.
func TestRetention_InterruptedSessionRemovalIsFinishedAtStart(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		retRow(t, m, "ws-gone", time.Duration(10-i)*time.Minute, nil)
	}
	retRow(t, m, "ws-stay", time.Minute, nil)

	// What the first batch leaves behind when the process stops: the mark on
	// the summary, and some rows already deleted.
	refs, _ := m.WorkSessionRowRefs("ws-gone")
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		sums := tx.Bucket([]byte(WorkSessionsBucket))
		s, err := loadSummary(sums, "ws-gone")
		if err != nil {
			return err
		}
		s.Removal = removalSession
		if err := storeSummary(sums, s); err != nil {
			return err
		}
		if err := tx.Bucket([]byte(ActivityRecordsBucket)).Delete(refs[0].ActivityKey); err != nil {
			return err
		}
		return tx.Bucket([]byte(WorkSessionRowsBucket)).Delete(workSessionRowKey("ws-gone", refs[0].ActivityKey))
	}))

	hidden, err := m.GetWorkSessionSummary("ws-gone")
	require.NoError(t, err)
	assert.Nil(t, hidden, "a session being removed is not shown")
	list, _ := m.ListWorkSessionSummaries()
	require.Len(t, list, 1)
	assert.Equal(t, "ws-stay", list[0].ID)

	n, err := m.ResumeSessionRemovals()
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	left, _ := m.WorkSessionRowRefs("ws-gone")
	assert.Empty(t, left)
	assert.Equal(t, 1, countRows(t, m), "the rows of the removed session are gone, the other session is whole")
	rebuilt, err := m.RepairWorkSessionSummaries()
	require.NoError(t, err)
	assert.Zero(t, rebuilt, "nothing is left to repair")
}

// A summary selected for expiry that got a row meanwhile is left alone.
func TestRetention_ExpiryIsRevalidated(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	retRow(t, m, "ws-1", 8*24*time.Hour, nil)
	cutoff := retNow.Add(-retWindow)
	// The row that arrives between selection and deletion.
	retRow(t, m, "ws-1", time.Hour, nil)

	dropped, err := m.dropSession("ws-1", removalSummary, cutoff, nil)
	require.NoError(t, err)
	assert.False(t, dropped)
	s, _ := m.GetWorkSessionSummary("ws-1")
	require.NotNil(t, s)
	assert.Empty(t, s.Removal)
	refs, _ := m.WorkSessionRowRefs("ws-1")
	assert.Len(t, refs, 2)
}

// The running byte count follows the summaries it rewrites: after a trim that
// was needed for the size cap, what is stored fits the cap.
func TestRetention_TrimLeavesTheStoredBytesWithinTheCap(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	for i := 0; i < 60; i++ {
		retRow(t, m, "ws-1", time.Duration(60-i)*time.Minute, func(r *ActivityRecord) {
			r.Status, r.ErrorMessage = ActivityStatusError, strings.Repeat("e", 300)
		})
	}
	before, err := m.scanLog()
	require.NoError(t, err)
	p := retPolicy()
	p.MaxBytes = before.st.bytes / 2
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	require.Greater(t, res.RowsTrimmed, 0)

	after, err := m.scanLog()
	require.NoError(t, err)
	assert.LessOrEqual(t, after.st.bytes, p.MaxBytes)
	// And it did not overshoot by more than one row's worth.
	assert.Greater(t, after.st.bytes, p.MaxBytes-2000)
}

// The truncated start is the time of the earliest row still stored.
func TestRetention_TruncatedStartIsTheEarliestRemainingRow(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	retRow(t, m, "ws-1", 5*time.Hour, nil)
	retRow(t, m, "ws-1", 4*time.Hour, nil)
	retRow(t, m, "ws-1", time.Hour, nil)

	p := retPolicy()
	p.MaxAge = 3 * time.Hour
	_, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	s, _ := m.GetWorkSessionSummary("ws-1")
	require.NotNil(t, s)
	assert.Equal(t, retNow.Add(-time.Hour), s.TruncatedStartAt, "readable history starts at the earliest remaining row")
}

// A record cap of one is a cap.
func TestRetention_RecordCapOfOne(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		retRow(t, m, "", time.Duration(5-i)*time.Minute, nil)
	}
	p := retPolicy()
	p.MaxRecords = 1
	_, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 1, countRows(t, m))
}

// A row that sorts before rows already stripped (the clock stepped back) is
// still considered: there is no watermark to hide it.
func TestRetention_LateOlderRowIsStrippedToo(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	big := strings.Repeat("x", 4000)
	for i := 0; i < 6; i++ {
		retRow(t, m, "ws-1", time.Duration(6-i)*time.Minute, func(r *ActivityRecord) { r.Response = big })
	}
	p := retPolicy()
	p.MaxBytes = 8000
	_, err := m.RunActivityRetention(p)
	require.NoError(t, err)

	late := retRow(t, m, "ws-1", 30*time.Minute, func(r *ActivityRecord) { r.Response = big; r.ID = "late-older-row" })
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Greater(t, res.BodiesStripped, 0)
	got, err := m.GetActivity(late.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.True(t, got.BodiesRemoved, "the older row's body is removed before any row is deleted")
	assert.Zero(t, res.RowsTrimmed)
}

// Stripping keeps the internal identity arguments of a legacy row, so its
// owner can still read it and identity filters still match it.
func TestRetention_StrippingKeepsLegacyOwnership(t *testing.T) {
	rec := &ActivityRecord{
		Type: ActivityTypeToolCall, Status: ActivityStatusSuccess, Response: "body",
		Arguments: map[string]interface{}{
			authArgAgentName: "legacy", authArgTokenPrefix: "mcp_agt_cccc", "query": "secret body",
		},
	}
	owner := &ActivityIdentityOwner{TokenName: "legacy", TokenPrefix: "mcp_agt_cccc"}
	require.True(t, owner.Owns(rec))

	require.True(t, stripRecordBodies(rec))
	assert.True(t, rec.BodiesRemoved)
	assert.Empty(t, rec.Response)
	assert.NotContains(t, rec.Arguments, "query")
	assert.True(t, owner.Owns(rec), "the owner still reads its row")
	assert.Equal(t, "legacy", rec.TokenName)
	assert.Equal(t, "mcp_agt_cccc", rec.TokenPrefix)

	// Nothing left to remove: not rewritten again.
	assert.False(t, stripRecordBodies(rec))
	assert.False(t, stripRecordBodies(&ActivityRecord{Type: ActivityTypeToolCall}))
}

// Two servers that failed at the same instant: the facts and the server named
// for them always agree, on every read.
func TestWorkSessionSummary_LastFailureTieIsDeterministic(t *testing.T) {
	s := &WorkSessionSummary{ID: "ws-1"}
	at := wsBase.Add(time.Minute)
	for _, server := range []string{"zeta", "alpha", "mid"} {
		s.applyRow(&ActivityRecord{
			Type: ActivityTypeToolCall, Status: ActivityStatusError, ServerName: server, ToolName: "tool_" + server,
			Metadata: map[string]interface{}{"error_code": server}, Timestamp: at,
		}, AttemptRole{Counted: true, Failed: true})
	}
	for i := 0; i < 50; i++ {
		server, ok := s.LastFailureServer(nil)
		require.True(t, ok)
		tot := s.Totals(nil)
		assert.Equal(t, "alpha", server)
		assert.Equal(t, "tool_alpha", tot.LastTool)
		assert.Equal(t, "alpha", tot.LastErrorCode, "the facts are the same server's")
	}
	server, _ := s.LastFailureServer(func(n string) bool { return n != "alpha" })
	assert.Equal(t, "mid", server)
}

// The connection count is the number of distinct connections.
func TestWorkSessionSummary_ConnectionCountIsDistinct(t *testing.T) {
	s := &WorkSessionSummary{ID: "ws-1"}
	for round := 0; round < 2; round++ {
		for i := 0; i < 100; i++ {
			s.applyRow(&ActivityRecord{
				Type: ActivityTypeToolCall, Status: ActivityStatusSuccess, ServerName: "github",
				SessionID: fmt.Sprintf("conn-%03d", i), Timestamp: wsBase.Add(time.Duration(round*100+i) * time.Second),
			}, AttemptRole{Counted: true})
		}
	}
	assert.Equal(t, 100, s.ConnectionCount)
}

// More held decisions than one transaction may write are settled across
// transactions, and every one is counted once.
func TestWorkSessionBackfill_ManyHeldDecisions(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	n := backfillBatch*2 + 137
	for i := 0; i < n; i++ {
		putRawActivity(t, m, wsRow("ws-old", time.Duration(i)*time.Microsecond, func(r *ActivityRecord) {
			r.Type, r.Status = ActivityTypePolicyDecision, "blocked"
			r.RequestID = fmt.Sprintf("req-%05d", i)
			r.ID = fmt.Sprintf("dec-%05d", i)
		}))
	}
	indexed, err := m.BackfillWorkSessions(nil)
	require.NoError(t, err)
	assert.Equal(t, n, indexed)
	s, _ := m.GetWorkSessionSummary("ws-old")
	require.NotNil(t, s)
	assert.Equal(t, n, s.Totals(nil).Blocks)
	assert.True(t, m.workSessionBackfillDone())
}

// The scan nominates candidates without decoding rows, and a nested value
// that only looks like a work-session key cannot make a row deletable: the
// acting step decodes and re-checks.
func TestRetention_ScanNominatesAndStepsRecheck(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	retRow(t, m, "ws-live", 3*time.Minute, func(r *ActivityRecord) {
		// A body that contains the key pattern, escaped as JSON makes it.
		r.Response = `{"work_session_id":"ws-not-there"}`
	})
	retRow(t, m, "", 2*time.Minute, nil)
	retRow(t, m, "ws-live", time.Minute, nil)

	scan, err := m.scanLog()
	require.NoError(t, err)
	assert.Equal(t, 3, scan.st.rows)
	assert.Len(t, scan.outside, 1, "only the row with no session")
	assert.Len(t, scan.strip, 1, "only the row with a body")

	p := retPolicy()
	p.MaxRecords = 2 // target 1: the outside row goes, then the session is trimmed
	_, err = m.RunActivityRetention(p)
	require.NoError(t, err)
	s, _ := m.GetWorkSessionSummary("ws-live")
	require.NotNil(t, s, "the session inside the window was not mistaken for an outside one")
}

// The summary's reason is fixed wording from closed vocabularies. The row's
// free-text error, which can echo a credential, is never copied into it.
func TestWorkSessionSummary_ReasonNeverCopiesFreeText(t *testing.T) {
	secret := "AKIAIOSFODNN7EXAMPLE"
	cases := []struct {
		rec  ActivityRecord
		want string
	}{
		{ActivityRecord{Type: ActivityTypeToolCall, Status: ActivityStatusError, ErrorClass: "network", ErrorMessage: "dial failed for key " + secret}, "The upstream server could not be reached."},
		{ActivityRecord{Type: ActivityTypeToolCall, Status: ActivityStatusError, ErrorClass: "http", UpstreamHTTPStatus: 503, ErrorMessage: secret}, "The upstream server answered with HTTP 503."},
		{ActivityRecord{Type: ActivityTypeToolCall, Status: ActivityStatusError, ErrorMessage: secret}, "The call failed."},
		{ActivityRecord{Type: ActivityTypePolicyDecision, Status: "blocked", BlockReason: "profile_scope", Metadata: map[string]interface{}{"reason": "blocked " + secret}}, "MCPProxy refused the call (profile_scope)."},
		{ActivityRecord{Type: ActivityTypeToolCall, Status: ActivityStatusRejected, ErrorClass: ErrorClassProxyPolicy, ErrorMessage: secret}, "MCPProxy refused the call (limit reached)."},
	}
	for _, tc := range cases {
		rec := tc.rec
		got := failureReason(&rec, AttemptRoleOf(&rec))
		assert.Equal(t, tc.want, got)
		assert.NotContains(t, got, secret)
	}
}

// A request row with no credential in a session that has one makes the
// session ownerless, whichever comes first.
func TestWorkSessionSummary_UnattributedRequestRowMakesItOwnerless(t *testing.T) {
	bare := func(r *ActivityRecord) { r.TokenName, r.TokenPrefix = "", "" }
	for _, order := range []string{"unattributed first", "attributed first"} {
		m, cleanup := setupTestStorageForActivity(t)
		if order == "unattributed first" {
			require.NoError(t, m.SaveActivity(wsRow("ws-1", 0, bare)))
			require.NoError(t, m.SaveActivity(wsRow("ws-1", time.Second, nil)))
		} else {
			require.NoError(t, m.SaveActivity(wsRow("ws-1", 0, nil)))
			require.NoError(t, m.SaveActivity(wsRow("ws-1", time.Second, bare)))
		}
		s, err := m.GetWorkSessionSummary("ws-1")
		require.NoError(t, err)
		assert.True(t, s.Ownerless, order)
		cleanup()
	}

	// A session with no credential at all (the local admin's) is simply
	// unowned, not marked ownerless by its own rows.
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	require.NoError(t, m.SaveActivity(wsRow("ws-admin", 0, bare)))
	require.NoError(t, m.SaveActivity(wsRow("ws-admin", time.Second, bare)))
	s, _ := m.GetWorkSessionSummary("ws-admin")
	assert.False(t, s.Ownerless)
	assert.Empty(t, s.TokenName)
}
