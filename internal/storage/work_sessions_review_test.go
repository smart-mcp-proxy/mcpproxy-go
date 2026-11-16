package storage

import (
	"bytes"
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

// ---- Review round 2 ----

// A candidate list that was cut short must not let the pass move on to a more
// destructive stage: outside rows are used up before any session inside the
// window is touched.
func TestRetention_TruncatedCandidatesAreRescannedBeforeSessionsGo(t *testing.T) {
	old := maxRetentionCandidates
	maxRetentionCandidates = 5
	defer func() { maxRetentionCandidates = old }()

	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	for i := 0; i < 24; i++ {
		retRow(t, m, "", 5*time.Hour+time.Duration(i)*time.Minute, nil)
	}
	for i := 0; i < 3; i++ {
		retRow(t, m, "ws-a", 2*time.Hour+time.Duration(i)*time.Minute, nil)
		retRow(t, m, "ws-b", time.Hour+time.Duration(i)*time.Minute, nil)
	}
	// 30 rows; cap 12 → target 10: 20 outside rows must go, far more than one
	// candidate list holds, and both sessions stay whole.
	p := retPolicy()
	p.MaxRecords = 12
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 20, res.RowsOutsideWindow)
	assert.Zero(t, res.SessionsRemoved)
	assert.Zero(t, res.RowsTrimmed)
	assert.Empty(t, res.ShortfallCause)
	for _, id := range []string{"ws-a", "ws-b"} {
		refs, _ := m.WorkSessionRowRefs(id)
		assert.Len(t, refs, 3, id)
	}

	// The same for bodies: more rows with bodies than one list holds.
	m2, cleanup2 := setupTestStorageForActivity(t)
	defer cleanup2()
	for i := 0; i < 20; i++ {
		retRow(t, m2, "ws-1", time.Duration(20-i)*time.Minute, func(r *ActivityRecord) { r.Response = strings.Repeat("x", 3000) })
	}
	p2 := retPolicy()
	p2.MaxBytes = 12000
	res2, err := m2.RunActivityRetention(p2)
	require.NoError(t, err)
	assert.Greater(t, res2.BodiesStripped, 5)
	assert.Zero(t, res2.RowsTrimmed, "bodies were enough; no row had to go")
	assert.Equal(t, 20, countRows(t, m2))
}

// The scan reads top-level fields only: a nested value that looks like one of
// them neither hides a row from a stage nor nominates it wrongly.
func TestSniffRow_TopLevelOnly(t *testing.T) {
	enc := func(r *ActivityRecord) []byte {
		data, err := r.MarshalBinary()
		require.NoError(t, err)
		return data
	}
	ws, args, resp := sniffRow(enc(&ActivityRecord{Type: ActivityTypeToolCall, WorkSessionID: "ws-real", Response: "body"}))
	assert.Equal(t, "ws-real", ws)
	assert.False(t, args)
	assert.True(t, resp)

	// A row with no session whose arguments name one.
	ws, args, _ = sniffRow(enc(&ActivityRecord{Type: ActivityTypeToolCall, Arguments: map[string]interface{}{"work_session_id": "ws-live"}}))
	assert.Empty(t, ws, "the nested key is not the row's work session")
	assert.True(t, args)

	// A row whose arguments say bodies_removed and whose response is large.
	_, args, resp = sniffRow(enc(&ActivityRecord{Type: ActivityTypeToolCall, Response: "big", Arguments: map[string]interface{}{"bodies_removed": true}}))
	assert.True(t, args)
	assert.True(t, resp, "still a stripping candidate")

	// A response that quotes a whole row, with braces and escaped quotes.
	tricky := `{"work_session_id":"ws-fake","arguments":{"a":[1,{"b":"}"}]},"x":"\\"}`
	ws, args, resp = sniffRow(enc(&ActivityRecord{Type: ActivityTypeToolCall, WorkSessionID: "ws-real", Response: tricky}))
	assert.Equal(t, "ws-real", ws)
	assert.False(t, args)
	assert.True(t, resp)

	// A stripped row with nothing left.
	_, args, resp = sniffRow(enc(&ActivityRecord{Type: ActivityTypeToolCall, WorkSessionID: "ws-1", BodiesRemoved: true}))
	assert.False(t, args)
	assert.False(t, resp)

	// Anything unreadable is nominated for the re-check.
	ws, args, resp = sniffRow([]byte(`not json`))
	assert.Empty(t, ws)
	assert.True(t, args && resp)
}

// A live row for a session that retention is removing cancels the removal:
// the session stays, truncated and consistent, with its new row.
func TestRetention_LiveRowCancelsASessionRemoval(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	n := retentionBatch + 200
	for i := 0; i < n; i++ {
		retRow(t, m, "ws-1", 3*time.Hour-time.Duration(i)*time.Millisecond, func(r *ActivityRecord) { r.Status = ActivityStatusError })
	}
	retRow(t, m, "ws-newer", time.Minute, nil)

	// The first batch of the removal, as removeSession runs it.
	s0, _ := m.GetWorkSessionSummary("ws-1")
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		sums := newTxSummaries(tx)
		sum := sums.get("ws-1")
		sum.Removal = removalSession
		idx := tx.Bucket([]byte(WorkSessionRowsBucket))
		acts := tx.Bucket([]byte(ActivityRecordsBucket))
		prefix := workSessionRowPrefix("ws-1")
		var keys [][]byte
		c := idx.Cursor()
		for k, _ := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix) && len(keys) < retentionBatch; k, _ = c.Next() {
			keys = append(keys, append([]byte(nil), k[len(prefix):]...))
		}
		for _, ak := range keys {
			if _, _, err := deleteRowTx(tx, sums, ak, append([]byte(nil), acts.Get(ak)...)); err != nil {
				return err
			}
		}
		_, err := sums.flush()
		return err
	}))
	hidden, _ := m.GetWorkSessionSummary("ws-1")
	require.Nil(t, hidden)

	// The connection wakes up and writes a row.
	retRow(t, m, "ws-1", 30*time.Second, nil)

	s, err := m.GetWorkSessionSummary("ws-1")
	require.NoError(t, err)
	require.NotNil(t, s, "the session is visible again")
	assert.Empty(t, s.Removal)
	tot := s.Totals(nil)
	assert.Equal(t, s0.Totals(nil).Failures, tot.Failures, "lifetime failures are untouched")
	assert.Equal(t, 200, tot.StoredFailures, "the stored counter matches the rows that are left")
	assert.False(t, s.TruncatedStartAt.IsZero())
	refs, _ := m.WorkSessionRowRefs("ws-1")
	assert.Len(t, refs, 201)

	// The interrupted removal does not go on, at start or otherwise.
	resumed, err := m.ResumeSessionRemovals()
	require.NoError(t, err)
	assert.Zero(t, resumed)
	refs, _ = m.WorkSessionRowRefs("ws-1")
	assert.Len(t, refs, 201)
}

// The same for an expiry that was dropping the index: the stored counters are
// counted again from what is left.
func TestRetention_LiveRowCancelsAnExpiry(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	for i := 0; i < 4; i++ {
		retRow(t, m, "ws-1", 9*24*time.Hour-time.Duration(i)*time.Minute, func(r *ActivityRecord) { r.Status = ActivityStatusError })
	}
	expireFirstEntries(t, m, "ws-1", 2)

	retRow(t, m, "ws-1", time.Minute, nil)
	s, _ := m.GetWorkSessionSummary("ws-1")
	require.NotNil(t, s)
	assert.Equal(t, 4, s.Totals(nil).Failures)
	assert.Equal(t, 2, s.Totals(nil).StoredFailures, "recounted from the index entries that are left")
}

// The selection of a session to remove is re-checked in the transaction that
// starts removing it.
func TestRetention_StaleSessionSelectionIsRefused(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	retRow(t, m, "ws-a", 3*time.Hour, nil)
	retRow(t, m, "ws-b", 2*time.Hour, nil)
	listed, _ := m.GetWorkSessionSummary("ws-a")

	// ws-a gets the newest row of the log after it was listed as the oldest.
	retRow(t, m, "ws-a", time.Minute, nil)

	ok, err := m.removeSession("ws-a", listed.LastActivityAt, nil)
	require.NoError(t, err)
	assert.False(t, ok, "its last activity changed since it was selected")
	s, _ := m.GetWorkSessionSummary("ws-a")
	require.NotNil(t, s)
	assert.Empty(t, s.Removal)

	// Even with the right expectation, the session that holds the newest row
	// of the log is not removed.
	ok, err = m.removeSession("ws-a", s.LastActivityAt, nil)
	require.NoError(t, err)
	assert.False(t, ok)

	// The pass removes the other one instead, and trims; it never empties.
	p := retPolicy()
	p.MaxRecords = 2
	_, err = m.RunActivityRetention(p)
	require.NoError(t, err)
	gone, _ := m.GetWorkSessionSummary("ws-b")
	assert.Nil(t, gone)
	kept, _ := m.GetWorkSessionSummary("ws-a")
	require.NotNil(t, kept)
}

// Many held decisions of ONE attempt followed by its tool call: all of them
// are companions, however many transactions it takes to write them.
func TestWorkSessionBackfill_ManyCompanionsOfOneAttempt(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	n := backfillBatch + 300
	for i := 0; i < n; i++ {
		putRawActivity(t, m, wsRow("ws-old", time.Duration(i)*time.Microsecond, func(r *ActivityRecord) {
			r.Type, r.Status, r.RequestID = ActivityTypePolicyDecision, "blocked", "req-one"
			r.ID = fmt.Sprintf("dec-%05d", i)
		}))
	}
	putRawActivity(t, m, wsRow("ws-old", time.Second, func(r *ActivityRecord) {
		r.RequestID, r.Status = "req-one", ActivityStatusError
	}))
	indexed, err := m.BackfillWorkSessions(nil)
	require.NoError(t, err)
	assert.Equal(t, n+1, indexed)
	s, _ := m.GetWorkSessionSummary("ws-old")
	require.NotNil(t, s)
	assert.Equal(t, 1, s.Totals(nil).Attempts, "one attempt: the tool call")
	assert.Zero(t, s.Totals(nil).Blocks)
}

// A server name that contains the index separator is not indexed, so it can
// never show up in another server's event range.
func TestUpstreamEventIndex_SeparatorInServerName(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	at := wsBase
	save := func(server string) {
		require.NoError(t, m.SaveActivity(&ActivityRecord{
			Type: ActivityTypeUpstreamEvent, Status: ActivityStatusError, ServerName: server, Timestamp: at,
		}))
		at = at.Add(time.Second)
	}
	save("a")
	save("a\x00b")
	save("ab")

	refs, err := m.UpstreamEventRefs("a", wsBase.Add(-time.Hour), wsBase.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, refs, 1, "only server a's own event")
	assert.Equal(t, "a", refs[0].Server)
	assert.False(t, refs[0].Timestamp.IsZero())

	odd, err := m.UpstreamEventRefs("a\x00b", wsBase.Add(-time.Hour), wsBase.Add(time.Hour))
	require.NoError(t, err)
	assert.Empty(t, odd)
	// The row itself is still in the activity log.
	_, total, err := m.ListActivities(ActivityFilter{Types: []string{string(ActivityTypeUpstreamEvent)}, Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
}

// A summary is rebuilt in bounded reads, and rows indexed while it was being
// read are folded in when it is published.
func TestWorkSessionRepair_LargeSessionAndLateRows(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	n := repairChunk*2 + 250
	for i := 0; i < n; i++ {
		require.NoError(t, m.SaveActivity(wsRow("ws-big", time.Duration(i)*time.Millisecond, func(r *ActivityRecord) {
			if i%10 == 0 {
				r.Status = ActivityStatusError
			}
		})))
	}
	before, _ := m.GetWorkSessionSummary("ws-big")
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(WorkSessionsBucket)).Delete([]byte("ws-big"))
	}))

	rebuilt, err := m.RepairWorkSessionSummaries()
	require.NoError(t, err)
	assert.Equal(t, 1, rebuilt)
	after, _ := m.GetWorkSessionSummary("ws-big")
	require.NotNil(t, after)
	assert.True(t, after.Rebuilt)
	assert.Equal(t, before.Totals(nil).Attempts, after.Totals(nil).Attempts)
	assert.Equal(t, before.Totals(nil).Failures, after.Totals(nil).Failures)
	assert.Equal(t, before.Totals(nil).StoredFailures, after.Totals(nil).StoredFailures)
	assert.Equal(t, before.StartedAt, after.StartedAt)
	assert.Equal(t, before.LastActivityAt, after.LastActivityAt)
}

// Review round 2 (doors): the timeline walk merges a session's rows with the
// upstream events of its servers from cursors, in one read transaction.
func TestWalkWorkSessionTimeline_MergesEventsInTimeOrder(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	event := func(server string, d time.Duration) {
		require.NoError(t, m.SaveActivity(&ActivityRecord{
			Type: ActivityTypeUpstreamEvent, Status: ActivityStatusError, ServerName: server, Timestamp: wsBase.Add(d),
		}))
	}
	for i := 0; i < 4; i++ {
		require.NoError(t, m.SaveActivity(wsRow("ws-t", time.Duration(i)*10*time.Second, nil)))
	}
	event("github", -time.Hour)     // before the span
	event("github", 5*time.Second)  // between rows 0 and 1
	event("slack", 5*time.Second)   // same time, another server
	event("slack", 10*time.Second)  // the time of row 1: not before it, so after
	event("github", 25*time.Second) // between rows 2 and 3
	event("github", 40*time.Second) // after the last row, inside the span
	event("github", 2*time.Hour)    // after the span
	event("other", 5*time.Second)   // a server that was not asked for
	event("a\x00b", 5*time.Second)  // not indexable

	type seen struct {
		event  bool
		server string
		at     time.Duration
	}
	var got []seen
	walk := func(stopAfter int) {
		got = got[:0]
		require.NoError(t, m.WalkWorkSessionTimeline("ws-t", []string{"github", "slack", "a\x00b", "none"},
			wsBase, wsBase.Add(time.Hour), func(r WorkSessionRowRef) bool {
				require.GreaterOrEqual(t, len(r.ActivityKey), 22)
				got = append(got, seen{r.Event, r.Server, r.Timestamp.Sub(wsBase)})
				return stopAfter == 0 || len(got) < stopAfter
			}))
	}
	walk(0)
	var order []string
	for _, g := range got {
		kind := "row"
		if g.event {
			kind = "ev:" + g.server
		}
		order = append(order, fmt.Sprintf("%s@%d", kind, int(g.at/time.Second)))
	}
	require.Len(t, got, 9, "%v", order)
	assert.Equal(t, "row@0", order[0])
	assert.ElementsMatch(t, []string{"ev:github@5", "ev:slack@5"}, order[1:3])
	assert.Equal(t, []string{"row@10", "ev:slack@10", "row@20", "ev:github@25", "row@30", "ev:github@40"}, order[3:])

	// The same order on every walk, and fn can stop it.
	first := append([]string(nil), order...)
	walk(0)
	for i, g := range got {
		assert.Equal(t, strings.HasPrefix(first[i], "ev:"), g.event)
	}
	walk(2)
	assert.Len(t, got, 2)

	// No servers: the session's rows only. No session: the events only.
	n := 0
	require.NoError(t, m.WalkWorkSessionTimeline("ws-t", nil, wsBase, wsBase.Add(time.Hour), func(WorkSessionRowRef) bool { n++; return true }))
	assert.Equal(t, 4, n)
	n = 0
	require.NoError(t, m.WalkWorkSessionTimeline("ws-none", []string{"github"}, wsBase, wsBase.Add(time.Hour), func(r WorkSessionRowRef) bool {
		assert.True(t, r.Event)
		n++
		return true
	}))
	assert.Equal(t, 3, n)
}

// Review round 3: the rows a cancelled summary expiry left without an index
// entry are still reachable by retention, so a cap is enforced afterwards.
func TestRetention_CancelledExpiryLeavesNoUnprunableRows(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	for i := 0; i < 4; i++ {
		retRow(t, m, "ws-1", 9*24*time.Hour-time.Duration(i)*time.Minute, nil)
	}
	refs := expireFirstEntries(t, m, "ws-1", 2)
	retRow(t, m, "ws-1", time.Minute, nil) // the session wakes up

	s, _ := m.GetWorkSessionSummary("ws-1")
	require.NotNil(t, s)
	assert.Equal(t, refs[2].Timestamp, s.UnindexedBefore, "rows older than the first entry left are unindexed")
	require.Equal(t, 5, countRows(t, m))

	// Not over any cap: nothing is touched.
	p := retPolicy()
	_, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 5, countRows(t, m))

	// Over the record cap (which prunes to 90% of itself): the two unindexed
	// rows go first, as rows outside the window.
	p.MaxRecords = 4
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 2, res.RowsOutsideWindow)
	assert.Equal(t, 3, countRows(t, m))
	left, _ := m.WorkSessionRowRefs("ws-1")
	assert.Len(t, left, 3, "the indexed rows of the session are still whole")

	// And a cap of one row is reached.
	p.MaxRecords = 1
	_, err = m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 1, countRows(t, m))
}

// Review round 3: a row whose only arguments are the internal _auth_* ones
// has nothing to strip and is not nominated, so such rows cannot use up the
// nominations of every rescan in front of bodies that can be stripped.
func TestRetention_AuthOnlyRowsDoNotStarveStripping(t *testing.T) {
	enc := func(r *ActivityRecord) []byte {
		data, err := r.MarshalBinary()
		require.NoError(t, err)
		return data
	}
	_, args, resp := sniffRow(enc(&ActivityRecord{Type: ActivityTypeToolCall, Arguments: map[string]interface{}{
		sniffAuthArgument + "token_name": "ci", sniffAuthArgument + "x": map[string]interface{}{"a": "}"},
	}}))
	assert.False(t, args, "auth-only arguments are not a body")
	assert.False(t, resp)
	_, args, _ = sniffRow(enc(&ActivityRecord{Type: ActivityTypeToolCall, Arguments: map[string]interface{}{
		sniffAuthArgument + "token_name": "ci", "query": "x",
	}}))
	assert.True(t, args)

	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	old := maxRetentionCandidates
	maxRetentionCandidates = 5
	defer func() { maxRetentionCandidates = old }()

	for i := 0; i < 400; i++ {
		retRow(t, m, "ws-1", 3*time.Hour-time.Duration(i)*time.Second, func(r *ActivityRecord) {
			r.Arguments = map[string]interface{}{sniffAuthArgument + "token_name": "legacy"}
		})
	}
	for i := 0; i < 5; i++ {
		retRow(t, m, "ws-1", time.Hour-time.Duration(i)*time.Second, func(r *ActivityRecord) {
			r.Response = strings.Repeat("x", 4000)
		})
	}
	scan, err := m.scanLog()
	require.NoError(t, err)
	p := retPolicy()
	p.MaxBytes = scan.st.bytes - 15000
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, res.BodiesStripped, 4, "the bodies behind the auth-only rows were reached")
	assert.Equal(t, 405, countRows(t, m), "stripping was enough; no row was removed")
}

// Review round 3: a session that cannot be removed (it holds the newest row
// of the log) does not stop the removal of the next one.
func TestRetention_ProtectedSessionDoesNotBlockTheNext(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	// Same last activity; ws-a sorts first by id and holds the newest key.
	at := retNow.Add(-time.Hour)
	save := func(ws, id string) {
		require.NoError(t, m.SaveActivity(&ActivityRecord{
			ID: id, Type: ActivityTypeToolCall, Status: ActivityStatusSuccess, ServerName: "github",
			Timestamp: at, WorkSessionID: ws,
		}))
	}
	save("ws-b", "01AAAAAAAAAAAAAAAAAAAAAAAA")
	save("ws-a", "01ZZZZZZZZZZZZZZZZZZZZZZZZ")

	p := retPolicy()
	p.MaxRecords = 1
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 1, res.SessionsRemoved)
	a, _ := m.GetWorkSessionSummary("ws-a")
	b, _ := m.GetWorkSessionSummary("ws-b")
	assert.NotNil(t, a, "the session with the newest row stays")
	assert.Nil(t, b, "the other session was removed")
	assert.Equal(t, 1, countRows(t, m))
}

// Review round 3: idle sessions are closed in bounded write transactions, and
// a session that got a row since it was listed is left open.
func TestMarkWorkSessionsEnded_InBatches(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	old := markEndedBatch
	markEndedBatch = 2
	defer func() { markEndedBatch = old }()

	for i := 0; i < 5; i++ {
		retRow(t, m, fmt.Sprintf("ws-idle-%d", i), 2*time.Hour, nil)
	}
	retRow(t, m, "ws-busy", time.Minute, nil)

	n, err := m.MarkWorkSessionsEnded(retNow.Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	for i := 0; i < 5; i++ {
		s, _ := m.GetWorkSessionSummary(fmt.Sprintf("ws-idle-%d", i))
		require.NotNil(t, s)
		assert.Equal(t, s.LastActivityAt, s.EndedAt)
	}
	busy, _ := m.GetWorkSessionSummary("ws-busy")
	assert.True(t, busy.EndedAt.IsZero())
	n, err = m.MarkWorkSessionsEnded(retNow.Add(-time.Hour))
	require.NoError(t, err)
	assert.Zero(t, n, "closing is idempotent")
}

// Review round 4: a failed row that cancels a summary expiry is counted once
// in the stored counters, not once by the recount and again as the new row.
func TestRetention_FailedRowCancellingAnExpiryIsCountedOnce(t *testing.T) {
	for _, block := range []bool{false, true} {
		m, cleanup := setupTestStorageForActivity(t)
		for i := 0; i < 4; i++ {
			retRow(t, m, "ws-1", 9*24*time.Hour-time.Duration(i)*time.Minute, func(r *ActivityRecord) { r.Status = ActivityStatusError })
		}
		expireFirstEntries(t, m, "ws-1", 2)
		retRow(t, m, "ws-1", time.Minute, func(r *ActivityRecord) {
			r.Status = ActivityStatusError
			if block {
				r.Type, r.Status = ActivityTypePolicyDecision, "blocked"
			}
		})
		s, _ := m.GetWorkSessionSummary("ws-1")
		require.NotNil(t, s)
		tot := s.Totals(nil)
		assert.Equal(t, 5, tot.Failures, "block=%v", block)
		assert.Equal(t, 3, tot.StoredFailures, "two entries left and the new row; block=%v", block)
		if block {
			assert.Equal(t, 1, tot.Blocks)
		}
		cleanup()
	}
}

// Review round 4: with a newer row outside the session (an upstream event),
// the session's last row is not protected, so a one-record cap is reached.
func TestRetention_NewerEventDoesNotLeaveTwoRowsUnderACapOfOne(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	retRow(t, m, "ws-1", time.Hour, nil)
	require.NoError(t, m.SaveActivity(&ActivityRecord{
		Type: ActivityTypeUpstreamEvent, Status: ActivityStatusError, ServerName: "github", Timestamp: retNow.Add(-time.Minute),
	}))
	p := retPolicy()
	p.MaxRecords = 1
	_, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 1, countRows(t, m))
	rows, _, err := m.ListActivities(ActivityFilter{Limit: 5})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, ActivityTypeUpstreamEvent, rows[0].Type, "the newest row of the log stays")

	// The session's only row stays when it IS the newest row of the log.
	m2, cleanup2 := setupTestStorageForActivity(t)
	defer cleanup2()
	require.NoError(t, m2.SaveActivity(&ActivityRecord{
		Type: ActivityTypeUpstreamEvent, Status: ActivityStatusError, ServerName: "github", Timestamp: retNow.Add(-time.Hour),
	}))
	retRow(t, m2, "ws-1", time.Minute, nil)
	_, err = m2.RunActivityRetention(p)
	require.NoError(t, err)
	rows, _, _ = m2.ListActivities(ActivityFilter{Limit: 5})
	require.Len(t, rows, 1)
	assert.Equal(t, "ws-1", rows[0].WorkSessionID)
}

// Review round 4: closing at start touches only sessions of the previous
// process; closing at stop touches all.
func TestCloseOpenWorkSessions_AtStartLeavesThisProcessAlone(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	retRow(t, m, "ws-previous", time.Hour, nil)
	retRow(t, m, "ws-current", time.Minute, nil)
	processStart := retNow.Add(-10 * time.Minute)

	n, err := m.CloseOpenWorkSessionsAtStart(processStart)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	prev, _ := m.GetWorkSessionSummary("ws-previous")
	cur, _ := m.GetWorkSessionSummary("ws-current")
	assert.False(t, prev.EndedAt.IsZero())
	assert.True(t, cur.EndedAt.IsZero(), "a session this process opened stays open")

	n, err = m.CloseOpenWorkSessions()
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	cur, _ = m.GetWorkSessionSummary("ws-current")
	assert.Equal(t, cur.LastActivityAt, cur.EndedAt)
}

// Review round 4: a request row with a credential but no user id, mixed with
// a row of a known user (rows from before the user was recorded), makes the
// session ownerless in either order; rows that all carry the user do not.
func TestWorkSessionSummary_MixedUserAttributionIsOwnerless(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	withUser := func(r *ActivityRecord) { r.UserID = "u-alice" }
	legacy := func(r *ActivityRecord) { r.Type, r.Status = ActivityTypePolicyDecision, "blocked" }

	require.NoError(t, m.SaveActivity(wsRow("ws-legacy-first", 0, legacy)))
	require.NoError(t, m.SaveActivity(wsRow("ws-legacy-first", time.Second, withUser)))
	require.NoError(t, m.SaveActivity(wsRow("ws-legacy-last", 0, withUser)))
	require.NoError(t, m.SaveActivity(wsRow("ws-legacy-last", time.Second, legacy)))
	require.NoError(t, m.SaveActivity(wsRow("ws-all-user", 0, withUser)))
	require.NoError(t, m.SaveActivity(wsRow("ws-all-user", time.Second, withUser)))
	require.NoError(t, m.SaveActivity(wsRow("ws-no-user", 0, nil)))
	require.NoError(t, m.SaveActivity(wsRow("ws-no-user", time.Second, legacy)))

	for id, ownerless := range map[string]bool{"ws-legacy-first": true, "ws-legacy-last": true, "ws-all-user": false, "ws-no-user": false} {
		s, err := m.GetWorkSessionSummary(id)
		require.NoError(t, err)
		require.NotNil(t, s, id)
		assert.Equal(t, ownerless, s.Ownerless, id)
	}
}

// expireFirstEntries does what one batch of a summary expiry does to a
// session: marks it as being removed, drops its first n index entries and
// lowers the stored counters for them. It returns the session's entries as
// they were before.
func expireFirstEntries(t *testing.T, m *Manager, id string, n int) []WorkSessionRowRef {
	t.Helper()
	refs, err := m.WorkSessionRowRefs(id)
	require.NoError(t, err)
	require.NoError(t, m.db.db.Update(func(tx *bbolt.Tx) error {
		sums := tx.Bucket([]byte(WorkSessionsBucket))
		s, err := loadSummary(sums, id)
		if err != nil {
			return err
		}
		s.Removal = removalSummary
		idx := tx.Bucket([]byte(WorkSessionRowsBucket))
		for _, ref := range refs[:n] {
			ik := workSessionRowKey(id, ref.ActivityKey)
			flags, server := parseIndexValue(idx.Get(ik))
			s.forgetStoredRow(flags, server, ref.Timestamp)
			if err := idx.Delete(ik); err != nil {
				return err
			}
		}
		return storeSummary(sums, s)
	}))
	return refs
}

// Review round 5: a session whose summary expired completely (rows kept,
// index gone) and that then resumes under the same id does not shield its old
// rows from the caps.
func TestRetention_ResumedAfterCompletedExpiryOldRowsArePrunable(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	for i := 0; i < 20; i++ {
		retRow(t, m, "ws-1", 9*24*time.Hour-time.Duration(i)*time.Minute, nil)
	}
	p := retPolicy()
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	require.Equal(t, 1, res.SummariesExpired)
	require.Equal(t, 20, countRows(t, m), "the rows outlive the summary")

	retRow(t, m, "ws-1", time.Minute, nil) // the connection comes back
	s, _ := m.GetWorkSessionSummary("ws-1")
	require.NotNil(t, s)
	assert.False(t, s.UnindexedBefore.IsZero())

	p.MaxRecords = 10
	res, err = m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 12, res.RowsOutsideWindow, "down to 90% of the cap, from the old rows")
	assert.Equal(t, 9, countRows(t, m))
	left, _ := m.WorkSessionRowRefs("ws-1")
	assert.Len(t, left, 1, "the resumed session's own row is untouched")
}

// Review round 5 (FR-038): a stage that needs more nominations than one list
// holds continues behind the last one; it does not read the log again.
func TestRetention_ContinuationsDoNotRescanTheLog(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	old := maxRetentionCandidates
	maxRetentionCandidates = 10
	defer func() { maxRetentionCandidates = old }()

	const n = 200
	for i := 0; i < n; i++ {
		retRow(t, m, "ws-1", 3*time.Hour-time.Duration(i)*time.Second, func(r *ActivityRecord) {
			r.Response = strings.Repeat("x", 2000)
		})
	}
	scan, err := m.scanLog()
	require.NoError(t, err)
	p := retPolicy()
	p.MaxBytes = scan.st.bytes - int64(n-20)*2000 // nearly every body must go
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, res.BodiesStripped, n-25)
	assert.Equal(t, n, countRows(t, m), "stripping was enough")
	assert.LessOrEqual(t, res.RowsScanned, 2*n+n/2, "the log is measured once and its tail is read once more, not once per list")
}

// Review round 6: rows that share the timestamp of the boundary a cancelled
// expiry left are still reachable by the caps.
func TestRetention_CancelledExpiryWithEqualTimestampsAtTheBoundary(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	for i := 0; i < 6; i++ {
		retRow(t, m, "ws-1", 9*24*time.Hour, nil) // all at one timestamp
	}
	expireFirstEntries(t, m, "ws-1", 4)
	retRow(t, m, "ws-1", time.Minute, nil)
	require.Equal(t, 7, countRows(t, m))

	p := retPolicy()
	p.MaxRecords = 4
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 4, res.RowsOutsideWindow, "the four unindexed rows, not the two indexed ones beside them")
	assert.Equal(t, 3, countRows(t, m))
	left, _ := m.WorkSessionRowRefs("ws-1")
	assert.Len(t, left, 3)
}

// Review round 6: trimming is for the last session left; when another one
// exists the trim does nothing and says so.
func TestRetention_TrimStopsWhenAnotherSessionExists(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		retRow(t, m, "ws-1", time.Hour-time.Duration(i)*time.Second, nil)
	}
	retRow(t, m, "ws-2", 2*time.Hour, nil) // appeared after ws-1 was selected
	scan, err := m.scanLog()
	require.NoError(t, err)
	st := scan.st
	n, another, err := m.trimSession("ws-1", &st, func() bool { return st.rows > 1 })
	require.NoError(t, err)
	assert.True(t, another)
	assert.Zero(t, n)
	assert.Equal(t, 6, countRows(t, m))

	// The whole pass still reaches the cap: the older session goes first.
	p := retPolicy()
	p.MaxRecords = 5
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 1, res.SessionsRemoved)
	gone, _ := m.GetWorkSessionSummary("ws-2")
	assert.Nil(t, gone)
}

// Review round 6: a pass that could remove nothing from the last session
// reports no shortfall: no session lost a row.
func TestRetention_NoShortfallWhenNothingCouldBeRemoved(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	retRow(t, m, "ws-1", time.Minute, func(r *ActivityRecord) {
		r.Status, r.ErrorMessage = ActivityStatusError, strings.Repeat("e", 4000) // not a body: stripping keeps it
	})
	p := retPolicy()
	p.MaxBytes = 1000
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Zero(t, res.SessionsRemoved+res.RowsTrimmed)
	assert.Empty(t, res.ShortfallCause)
	sf, err := m.GetRetentionShortfall()
	require.NoError(t, err)
	assert.Nil(t, sf)
	assert.Equal(t, 1, countRows(t, m))
}

// Review round 7: the summary records the REST surface of its rows, and a
// session with no rows left does not hold the log above the size cap.
func TestWorkSessionSummary_SurfaceAndEmptyLastSession(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	retRow(t, m, "ws-web", time.Hour, func(r *ActivityRecord) { r.Source, r.Surface = ActivitySourceAPI, "webui" })
	retRow(t, m, "ws-mcp", time.Hour, func(r *ActivityRecord) { r.Source = ActivitySourceMCP })
	web, _ := m.GetWorkSessionSummary("ws-web")
	mcp, _ := m.GetWorkSessionSummary("ws-mcp")
	assert.Equal(t, "webui", web.Surface)
	assert.Equal(t, string(ActivitySourceMCP), mcp.Surface)

	m2, cleanup2 := setupTestStorageForActivity(t)
	defer cleanup2()
	retRow(t, m2, "ws-1", time.Hour, nil)
	// Its only row goes (as age pruning would take it); the summary stays.
	refs, _ := m2.WorkSessionRowRefs("ws-1")
	require.NoError(t, m2.db.db.Update(func(tx *bbolt.Tx) error {
		sums := newTxSummaries(tx)
		acts := tx.Bucket([]byte(ActivityRecordsBucket))
		if _, _, err := deleteRowTx(tx, sums, refs[0].ActivityKey, append([]byte(nil), acts.Get(refs[0].ActivityKey)...)); err != nil {
			return err
		}
		_, err := sums.flush()
		return err
	}))
	require.NoError(t, m2.SaveActivity(&ActivityRecord{
		Type: ActivityTypeUpstreamEvent, Status: ActivityStatusError, ServerName: "github", Timestamp: retNow.Add(-time.Minute),
	}))
	s, _ := m2.GetWorkSessionSummary("ws-1")
	require.NotNil(t, s)
	scan, err := m2.scanLog()
	require.NoError(t, err)
	p := retPolicy()
	p.MaxBytes = scan.st.bytes - 50 // over, by less than the summary's size
	_, err = m2.RunActivityRetention(p)
	require.NoError(t, err)
	gone, _ := m2.GetWorkSessionSummary("ws-1")
	assert.Nil(t, gone, "the empty summary was removed under size pressure")
	assert.Equal(t, 1, countRows(t, m2), "the newest row of the log stays")
	after, _ := m2.scanLog()
	assert.LessOrEqual(t, after.st.bytes, p.MaxBytes)
}
