package storage

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var retNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

const retWindow = 7 * 24 * time.Hour

// retRow saves one row of a session `age` before retNow.
func retRow(t *testing.T, m *Manager, ws string, age time.Duration, mutate func(*ActivityRecord)) *ActivityRecord {
	t.Helper()
	r := &ActivityRecord{
		Type: ActivityTypeToolCall, Status: ActivityStatusSuccess, ServerName: "github", ToolName: "list_issues",
		Timestamp: retNow.Add(-age), WorkSessionID: ws, TokenName: "ci", TokenPrefix: "mcp_agt_aaaa",
	}
	if mutate != nil {
		mutate(r)
	}
	require.NoError(t, m.SaveActivity(r))
	return r
}

func retPolicy() RetentionPolicy {
	return RetentionPolicy{Now: retNow, SessionWindow: retWindow}
}

func countRows(t *testing.T, m *Manager) int {
	t.Helper()
	_, total, err := m.ListActivities(ActivityFilter{Limit: 1})
	require.NoError(t, err)
	return total
}

// Step 1 and FR-036: a summary past the window goes, with its index; its rows
// stay readable through the ordinary filters.
func TestRetention_SessionWindowDropsSummariesNotRows(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	retRow(t, m, "ws-old", 8*24*time.Hour, nil)
	retRow(t, m, "ws-new", time.Hour, nil)

	res, err := m.RunActivityRetention(retPolicy())
	require.NoError(t, err)
	assert.Equal(t, 1, res.SummariesExpired)
	assert.Empty(t, res.ShortfallCause)

	old, _ := m.GetWorkSessionSummary("ws-old")
	assert.Nil(t, old)
	refs, _ := m.WorkSessionRowRefs("ws-old")
	assert.Empty(t, refs)
	kept, _ := m.GetWorkSessionSummary("ws-new")
	assert.NotNil(t, kept)
	assert.Equal(t, 2, countRows(t, m), "rows outlive their summary")
}

// Step 2 and FR-039/FR-013: the age cap trims a session's oldest rows, marks
// it truncated, lowers only the stored counters, and reports a shortfall.
func TestRetention_AgeCapTrimsAndKeepsLifetimeFacts(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	retRow(t, m, "ws-1", 3*24*time.Hour, func(r *ActivityRecord) { r.Status = ActivityStatusError })
	retRow(t, m, "ws-1", time.Hour, func(r *ActivityRecord) { r.Status = ActivityStatusError })

	p := retPolicy()
	p.MaxAge = 2 * 24 * time.Hour // shorter than the window
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 1, res.RowsAged)
	assert.Equal(t, ShortfallAgeCap, res.ShortfallCause)

	s, _ := m.GetWorkSessionSummary("ws-1")
	require.NotNil(t, s)
	tot := s.Totals(nil)
	assert.Equal(t, 2, tot.Attempts, "lifetime attempts are not lowered")
	assert.Equal(t, 2, tot.Failures, "lifetime failures are not lowered")
	assert.Equal(t, 1, tot.StoredFailures)
	assert.Equal(t, retNow.Add(-3*24*time.Hour), tot.FirstFailureAt, "the first failure fact survives its row")
	assert.False(t, s.TruncatedStartAt.IsZero())
	refs, _ := m.WorkSessionRowRefs("ws-1")
	assert.Len(t, refs, 1)

	sf, err := m.GetRetentionShortfall()
	require.NoError(t, err)
	require.NotNil(t, sf)
	assert.Equal(t, ShortfallAgeCap, sf.Cause)
}

// Step 3a and FR-032: over the size cap, bodies go first and the rows stay,
// with every other field intact.
func TestRetention_BodiesAreRemovedBeforeRows(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	big := strings.Repeat("x", 4000)
	for i := 0; i < 10; i++ {
		retRow(t, m, "ws-1", time.Duration(10-i)*time.Minute, func(r *ActivityRecord) {
			r.Response = big
			r.Arguments = map[string]interface{}{"q": big}
			r.Status, r.ErrorMessage, r.ErrorClass, r.RequestID = ActivityStatusError, "boom", "network", fmt.Sprintf("req-%d", i)
			r.DurationMs = 42
		})
	}
	before := countRows(t, m)

	p := retPolicy()
	p.MaxBytes = 30_000 // ten rows of ~8 KB do not fit; ten stripped rows do
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)

	assert.Greater(t, res.BodiesStripped, 0)
	assert.Zero(t, res.RowsOutsideWindow)
	assert.Zero(t, res.SessionsRemoved)
	assert.Zero(t, res.RowsTrimmed)
	assert.Empty(t, res.ShortfallCause, "no session lost a row")
	assert.Equal(t, before, countRows(t, m))

	records, _, err := m.ListActivities(ActivityFilter{Limit: 100})
	require.NoError(t, err)
	strippedSeen := 0
	for _, r := range records {
		if !r.BodiesRemoved {
			continue
		}
		strippedSeen++
		assert.Empty(t, r.Response)
		assert.Empty(t, r.Arguments)
		assert.Equal(t, ActivityStatusError, r.Status)
		assert.Equal(t, "boom", r.ErrorMessage)
		assert.Equal(t, "network", r.ErrorClass)
		assert.Equal(t, "github", r.ServerName)
		assert.Equal(t, "list_issues", r.ToolName)
		assert.Equal(t, int64(42), r.DurationMs)
		assert.NotEmpty(t, r.RequestID)
		assert.Equal(t, "ws-1", r.WorkSessionID)
	}
	assert.Equal(t, res.BodiesStripped, strippedSeen)

	st, err := m.measureLog()
	require.NoError(t, err)
	assert.LessOrEqual(t, st.bytes, p.MaxBytes, "the activity bucket and the new buckets together fit the budget")

	// The oldest rows were stripped first.
	oldestStripped, newestKept := false, false
	for _, r := range records {
		if r.Timestamp.Equal(retNow.Add(-10 * time.Minute)) {
			oldestStripped = r.BodiesRemoved
		}
		if r.Timestamp.Equal(retNow.Add(-time.Minute)) {
			newestKept = !r.BodiesRemoved || res.BodiesStripped == 10
		}
	}
	assert.True(t, oldestStripped)
	assert.True(t, newestKept)

	// A second pass does not strip again what it already stripped.
	again, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Zero(t, again.BodiesStripped)
}

// Steps 3b to 3d and FR-037/FR-033: rows outside the window go before any
// session inside it; then whole sessions, oldest last activity first; the last
// one is trimmed from its oldest end and never emptied.
func TestRetention_OrderOfRowRemoval(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	// 5 rows with no session, 5 of a session outside the window.
	for i := 0; i < 5; i++ {
		retRow(t, m, "", 5*time.Hour+time.Duration(i)*time.Minute, nil)
		retRow(t, m, "ws-expired", 8*24*time.Hour+time.Duration(i)*time.Minute, nil)
	}
	// Three sessions inside the window, 5 rows each.
	for i := 0; i < 5; i++ {
		retRow(t, m, "ws-a", 3*time.Hour+time.Duration(i)*time.Minute, nil)
		retRow(t, m, "ws-b", 2*time.Hour+time.Duration(i)*time.Minute, nil)
		retRow(t, m, "ws-c", 1*time.Hour+time.Duration(i)*time.Minute, func(r *ActivityRecord) { r.Status = ActivityStatusError })
	}
	require.Equal(t, 25, countRows(t, m))

	// Count cap 16 → target 14: the 10 outside rows go, one more row is needed,
	// which costs the oldest whole session.
	p := retPolicy()
	p.MaxRecords = 16
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 10, res.RowsOutsideWindow)
	assert.Equal(t, 1, res.SessionsRemoved)
	assert.Zero(t, res.RowsTrimmed)
	assert.Equal(t, ShortfallRecordCap, res.ShortfallCause)

	a, _ := m.GetWorkSessionSummary("ws-a")
	assert.Nil(t, a, "the session with the oldest last activity goes whole")
	refsA, _ := m.WorkSessionRowRefs("ws-a")
	assert.Empty(t, refsA)
	for _, id := range []string{"ws-b", "ws-c"} {
		s, _ := m.GetWorkSessionSummary(id)
		require.NotNil(t, s, id)
		assert.True(t, s.TruncatedStartAt.IsZero(), "%s is whole", id)
		refs, _ := m.WorkSessionRowRefs(id)
		assert.Len(t, refs, 5, id)
	}
	assert.Equal(t, 10, countRows(t, m))

	// Tighter still: cap 4 → target 3. ws-b goes whole, then ws-c, the last
	// session, is trimmed from its oldest end to 3 rows.
	p.MaxRecords = 4
	res, err = m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 1, res.SessionsRemoved)
	assert.Equal(t, 2, res.RowsTrimmed)
	c, _ := m.GetWorkSessionSummary("ws-c")
	require.NotNil(t, c)
	assert.False(t, c.TruncatedStartAt.IsZero())
	assert.Equal(t, 5, c.Totals(nil).Failures, "lifetime failures survive the trim")
	assert.Equal(t, 3, c.Totals(nil).StoredFailures)
	refs, _ := m.WorkSessionRowRefs("ws-c")
	require.Len(t, refs, 3)
	assert.Equal(t, retNow.Add(-time.Hour), refs[2].Timestamp, "the newest rows are the ones kept")
}

// The last session is never emptied, whatever the cap.
func TestRetention_LastSessionKeepsItsNewestRow(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		retRow(t, m, "ws-1", time.Duration(5-i)*time.Minute, func(r *ActivityRecord) { r.Response = strings.Repeat("x", 2000) })
	}
	p := retPolicy()
	p.MaxBytes = 1 // nothing fits
	_, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 1, countRows(t, m))
	refs, _ := m.WorkSessionRowRefs("ws-1")
	require.Len(t, refs, 1)
	assert.Equal(t, retNow.Add(-time.Minute), refs[0].Timestamp)
}

// FR-035: the shortfall is cleared only after a full window without an
// in-window removal, not by one quiet pass.
func TestRetention_ShortfallClearsOnlyAfterAFullWindow(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	for i := 0; i < 5; i++ {
		retRow(t, m, "ws-a", 3*time.Hour+time.Duration(i)*time.Minute, nil)
		retRow(t, m, "ws-b", time.Hour+time.Duration(i)*time.Minute, nil)
	}
	p := retPolicy()
	p.MaxRecords = 6
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	require.Equal(t, ShortfallRecordCap, res.ShortfallCause)

	// A quiet pass a day later: still reported.
	p.Now = retNow.Add(24 * time.Hour)
	p.MaxRecords = 0
	_, err = m.RunActivityRetention(p)
	require.NoError(t, err)
	sf, _ := m.GetRetentionShortfall()
	require.NotNil(t, sf, "one quiet pass does not clear it")
	assert.Equal(t, retNow, sf.LastInWindowRemovalAt)

	// Past a full window with no loss: cleared.
	p.Now = retNow.Add(retWindow + time.Hour)
	_, err = m.RunActivityRetention(p)
	require.NoError(t, err)
	sf, _ = m.GetRetentionShortfall()
	assert.Nil(t, sf)
}

// FR-038: writes are batched; a session far larger than one batch is still
// removed completely.
func TestRetention_LargeSessionIsRemovedAcrossBatches(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	n := retentionBatch*2 + 50
	for i := 0; i < n; i++ {
		retRow(t, m, "ws-big", 2*time.Hour+time.Duration(i)*time.Millisecond, nil)
	}
	retRow(t, m, "ws-small", time.Minute, nil)

	p := retPolicy()
	p.MaxRecords = 10
	res, err := m.RunActivityRetention(p)
	require.NoError(t, err)
	assert.Equal(t, 1, res.SessionsRemoved)
	big, _ := m.GetWorkSessionSummary("ws-big")
	assert.Nil(t, big)
	refs, _ := m.WorkSessionRowRefs("ws-big")
	assert.Empty(t, refs)
	assert.Equal(t, 1, countRows(t, m))
}
