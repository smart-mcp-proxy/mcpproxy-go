package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

var evBase = time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC)

func upstreamEventRows(t *testing.T, store *storage.Manager, server string) []*storage.ActivityRecord {
	t.Helper()
	records, _, err := store.ListActivities(storage.ActivityFilter{
		Types: []string{string(storage.ActivityTypeUpstreamEvent)}, Server: server, Limit: 5000,
	})
	require.NoError(t, err)
	// ListActivities is newest first; the tests read oldest first.
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	return records
}

func eventService(t *testing.T) (*ActivityService, *storage.Manager) {
	t.Helper()
	store, cleanup := setupTestStorage(t)
	t.Cleanup(cleanup)
	return NewActivityService(store, zap.NewNop()), store
}

// The mapping from a state change to an event.
func TestTransitionEvent_Classification(t *testing.T) {
	at := evBase
	cases := []struct {
		name      string
		tr        types.Transition
		wantKind  UpstreamEventKind
		wantCode  string
		none      bool
		transport string
	}{
		{"ready to error is a loss", types.Transition{From: types.StateReady, To: types.StateError, At: at, Err: errors.New("EOF")}, UpstreamConnectionLost, "", false, ""},
		{"ready to disconnected is a loss", types.Transition{From: types.StateReady, To: types.StateDisconnected, At: at}, UpstreamConnectionLost, "", false, ""},
		{"a terminal stdio exit is a process exit", types.Transition{From: types.StateReady, To: types.StateError, At: at, Err: errors.New("x"), TerminalCode: string(diagnostics.STDIOExitNonzero)}, UpstreamProcessExited, string(diagnostics.STDIOExitNonzero), false, ""},
		{"back to ready is a recovery", types.Transition{From: types.StateDiscovering, To: types.StateReady, At: at}, UpstreamConnectionRestored, "", false, ""},
		{"parking for login", types.Transition{From: types.StateConnecting, To: types.StatePendingAuth, At: at, Err: errors.New("login")}, UpstreamLoginRequired, string(diagnostics.OAuthLoginRequired), false, ""},
		{"a reconnect attempt that fails again is not an event", types.Transition{From: types.StateConnecting, To: types.StateError, At: at, Err: errors.New("refused")}, "", "", true, ""},
		{"an established stdio connection that fails is its process going away", types.Transition{From: types.StateReady, To: types.StateError, At: at, Err: errors.New("transport closed")}, UpstreamProcessExited, "", false, "stdio"},
		{"a stdio server disconnected on purpose is a closed connection", types.Transition{From: types.StateReady, To: types.StateDisconnected, At: at}, UpstreamConnectionLost, "", false, "stdio"},
		{"an established connection never exited before initialize", types.Transition{From: types.StateReady, To: types.StateError, At: at, Err: errors.New("x"), TerminalCode: string(diagnostics.STDIOExitBeforeInitialize)}, UpstreamProcessExited, "", false, "stdio"},
		{"the same error over http is a lost connection", types.Transition{From: types.StateReady, To: types.StateError, At: at, Err: errors.New("transport closed")}, UpstreamConnectionLost, "", false, "http"},
		{"connecting is not an event", types.Transition{From: types.StateDisconnected, To: types.StateConnecting, At: at}, "", "", true, ""},
		{"ready to ready is not an event", types.Transition{From: types.StateReady, To: types.StateReady, At: at}, "", "", true, ""},
	}
	for _, tc := range cases {
		ev, ok := transitionEvent("github", tc.transport, tc.tr)
		if tc.none {
			assert.False(t, ok, tc.name)
			continue
		}
		require.True(t, ok, tc.name)
		assert.Equal(t, tc.wantKind, ev.Kind, tc.name)
		assert.Equal(t, tc.wantCode, ev.Code, tc.name)
		assert.Equal(t, at, ev.At, "the event carries the time of the change")
		assert.Equal(t, "github", ev.Server)
	}
}

// FR-020/FR-021: a row carries the server, the kind, the time, the code and
// the fixed message, and never the raw error text.
func TestUpstreamEvent_RowShapeAndNoRawError(t *testing.T) {
	svc, store := eventService(t)
	raw := errors.New("dial tcp 10.0.0.7:443: secret-internal-hostname refused")
	ev, ok := transitionEvent("github", "http", types.Transition{From: types.StateReady, To: types.StateError, At: evBase, Err: raw})
	require.True(t, ok)
	svc.RecordUpstreamEvent(ev)

	rows := upstreamEventRows(t, store, "github")
	require.Len(t, rows, 1)
	r := rows[0]
	assert.Equal(t, storage.ActivityTypeUpstreamEvent, r.Type)
	assert.Equal(t, storage.ActivityStatusError, r.Status)
	assert.Equal(t, evBase, r.Timestamp, "the row is placed at the time of the change, not of the write")
	assert.Equal(t, string(UpstreamConnectionLost), r.Metadata[UpstreamEventMetaKind])
	assert.NotContains(t, r.ErrorMessage, "secret-internal-hostname")
	assert.NotContains(t, r.ErrorMessage, "10.0.0.7")
	assert.NotEmpty(t, r.ErrorMessage)
	assert.Empty(t, r.WorkSessionID, "an upstream event belongs to no session; the timeline joins it by server")

	// And it is indexed by server for the timeline join.
	refs, err := store.UpstreamEventRefs("github", evBase.Add(-time.Minute), evBase.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.True(t, refs[0].Event)
}

// A recovery is recorded only after a recorded loss: the first connect of a
// server is not an event.
func TestUpstreamEvent_RestoredOnlyAfterALoss(t *testing.T) {
	svc, store := eventService(t)
	svc.RecordUpstreamEvent(UpstreamEvent{Server: "github", Kind: UpstreamConnectionRestored, At: evBase})
	assert.Empty(t, upstreamEventRows(t, store, "github"))

	svc.RecordUpstreamEvent(UpstreamEvent{Server: "github", Kind: UpstreamConnectionLost, At: evBase.Add(time.Second)})
	svc.RecordUpstreamEvent(UpstreamEvent{Server: "github", Kind: UpstreamConnectionRestored, At: evBase.Add(5 * time.Second)})
	svc.RecordUpstreamEvent(UpstreamEvent{Server: "github", Kind: UpstreamConnectionRestored, At: evBase.Add(6 * time.Second)})

	rows := upstreamEventRows(t, store, "github")
	require.Len(t, rows, 2)
	assert.Equal(t, string(UpstreamConnectionLost), rows[0].Metadata[UpstreamEventMetaKind])
	assert.Equal(t, string(UpstreamConnectionRestored), rows[1].Metadata[UpstreamEventMetaKind])
	assert.Equal(t, storage.ActivityStatusSuccess, rows[1].Status)
}

// FR-022: repeats within a minute are one row with a count and first and last
// times.
func TestUpstreamEvent_RepeatsWithinAMinuteAreOneRow(t *testing.T) {
	svc, store := eventService(t)
	for i := 0; i < 30; i++ {
		svc.RecordUpstreamEvent(UpstreamEvent{Server: "github", Kind: UpstreamOAuthRefreshFailed, At: evBase.Add(time.Duration(i) * time.Second)})
	}
	rows := upstreamEventRows(t, store, "github")
	require.Len(t, rows, 1)
	assert.EqualValues(t, 30, rows[0].Metadata[UpstreamEventMetaCount])
	assert.Equal(t, evBase.Format(time.RFC3339Nano), rows[0].Metadata[UpstreamEventMetaFirstAt])
	assert.Equal(t, evBase.Add(29*time.Second).Format(time.RFC3339Nano), rows[0].Metadata[UpstreamEventMetaLastAt])
	assert.Equal(t, evBase, rows[0].Timestamp)

	svc.RecordUpstreamEvent(UpstreamEvent{Server: "github", Kind: UpstreamOAuthRefreshFailed, At: evBase.Add(61 * time.Second)})
	assert.Len(t, upstreamEventRows(t, store, "github"), 2, "past the minute a new row starts")
}

// SC-007 and FR-022: one failure per second for 24 hours adds at most 2,880
// rows for the server, and never more than 120 in one hour.
func TestUpstreamEvent_StormIsBounded(t *testing.T) {
	c := newUpstreamEventCollapser()
	rows := 0
	perHour := map[time.Time]int{}
	// Alternating kinds defeats the per-minute collapse of a single kind, so
	// the hourly bound is what holds.
	kinds := []UpstreamEventKind{UpstreamConnectionLost, UpstreamOAuthRefreshFailed, UpstreamProcessExited}
	for s := 0; s < 24*3600; s++ {
		at := evBase.Add(time.Duration(s) * time.Second)
		_, isNew := c.place(UpstreamEvent{Server: "github", Kind: kinds[s%len(kinds)], At: at})
		if isNew {
			rows++
			perHour[at.Truncate(time.Hour)]++
		}
	}
	assert.LessOrEqual(t, rows, 2880)
	for h, n := range perHour {
		assert.LessOrEqualf(t, n, MaxUpstreamEventRowsPerHour, "hour %s", h)
	}
	assert.Greater(t, rows, 24, "the storm is still visible hour by hour")
}

// FR-025: events about one user's credential are collapsed per owner, never
// across owners.
func TestUpstreamEvent_CollapsedPerOwner(t *testing.T) {
	svc, store := eventService(t)
	for i := 0; i < 3; i++ {
		at := evBase.Add(time.Duration(i) * time.Second)
		svc.RecordUpstreamEvent(UpstreamEvent{Server: "github", Kind: UpstreamOAuthRefreshFailed, At: at, Owner: "alice"})
		svc.RecordUpstreamEvent(UpstreamEvent{Server: "github", Kind: UpstreamOAuthRefreshFailed, At: at, Owner: "bob"})
	}
	rows := upstreamEventRows(t, store, "github")
	require.Len(t, rows, 2)
	owners := map[string]interface{}{}
	for _, r := range rows {
		owners[r.UserID] = r.Metadata[UpstreamEventMetaCount]
	}
	assert.EqualValues(t, 3, owners["alice"])
	assert.EqualValues(t, 3, owners["bob"])
}

// One open login_required per server and owner: a second source for the same
// condition updates the row; a saved token or a recovery closes it.
func TestUpstreamEvent_OneOpenLoginRequired(t *testing.T) {
	svc, store := eventService(t)
	// Token saves carry no time of their own; they are applied "now".
	svc.upstreamEvents.now = func() time.Time { return evBase.Add(-time.Minute) }
	login := func(at time.Duration, seq uint64) {
		svc.RecordUpstreamEvent(UpstreamEvent{Server: "github", Kind: UpstreamLoginRequired, At: evBase.Add(at), Seq: seq})
	}
	loginRows := func() []*storage.ActivityRecord {
		var out []*storage.ActivityRecord
		for _, r := range upstreamEventRows(t, store, "github") {
			if r.Metadata[UpstreamEventMetaKind] == string(UpstreamLoginRequired) {
				out = append(out, r)
			}
		}
		return out
	}

	login(0, 2)              // the refresh schedule was given up
	login(10*time.Minute, 0) // the connection later parks for login: same condition
	rows := loginRows()
	require.Len(t, rows, 1, "the second source updates the open row")
	assert.EqualValues(t, 2, rows[0].Metadata[UpstreamEventMetaCount])

	// A token save numbered before the failure is stale and clears nothing.
	svc.NoteUpstreamTokenSaved("github", "", 1, evBase.Add(15*time.Minute))
	login(20*time.Minute, 0)
	require.Len(t, loginRows(), 1, "a stale save does not clear the mark")

	// A successful login clears it; the next condition gets its own row.
	svc.NoteUpstreamTokenSaved("github", "", 3, evBase.Add(25*time.Minute))
	login(30*time.Minute, 4)
	require.Len(t, loginRows(), 2)

	// A failure accepted before a save and delivered after it: written, but it
	// neither opens nor updates the mark.
	svc.NoteUpstreamTokenSaved("github", "", 6, evBase.Add(35*time.Minute))
	login(40*time.Minute, 5)
	require.Len(t, loginRows(), 3, "it happened, so it is a row")
	login(50*time.Minute, 7)
	rows = loginRows()
	require.Len(t, rows, 4, "the next real condition is not merged into the stale one")

	// A failure of a schedule delivered before the save that installed it
	// leaves the mark open.
	svc.NoteUpstreamTokenSaved("github", "", 6, evBase.Add(35*time.Minute))
	login(55*time.Minute, 0)
	assert.Len(t, loginRows(), 4, "still the same open condition")

	// A recovery closes it too.
	svc.RecordUpstreamEvent(UpstreamEvent{Server: "github", Kind: UpstreamConnectionRestored, At: evBase.Add(time.Hour)})
	login(2*time.Hour, 0)
	assert.Len(t, loginRows(), 5)
}

// Review finding: the hourly bound is kept per hour of the event, so a late
// event of the previous hour does not reset the current hour's count.
func TestUpstreamEvent_LateEventDoesNotResetTheHourlyBound(t *testing.T) {
	c := newUpstreamEventCollapser()
	hour := evBase.Truncate(time.Hour)
	rows := 0
	place := func(kind UpstreamEventKind, at time.Time, owner string) {
		if row, isNew := c.place(UpstreamEvent{Server: "github", Kind: kind, At: at, Owner: owner}); row != nil && isNew {
			rows++
		}
	}
	// Fill the hour to the limit with distinct owners (no row to merge into).
	for i := 0; i < MaxUpstreamEventRowsPerHour; i++ {
		place(UpstreamOAuthRefreshFailed, hour.Add(time.Duration(i)*time.Second), fmt.Sprintf("user-%03d", i))
	}
	require.Equal(t, MaxUpstreamEventRowsPerHour, rows)

	// A late event of the previous hour, then more of this hour.
	place(UpstreamConnectionLost, hour.Add(-time.Minute), "")
	before := rows
	for i := 0; i < 50; i++ {
		place(UpstreamOAuthRefreshFailed, hour.Add(30*time.Minute+time.Duration(i)*time.Second), fmt.Sprintf("late-%03d", i))
	}
	assert.Equal(t, before, rows, "the current hour is still at its limit")
	assert.Equal(t, 50, c.suppressed, "an event with nothing of its kind and owner to merge into is not recorded past the limit")

	// The next hour has its own budget.
	place(UpstreamOAuthRefreshFailed, hour.Add(time.Hour+time.Second), "user-next")
	assert.Equal(t, before+1, rows)
}

// Review finding: past the merge threshold repeats merge into the latest row
// of their kind, leaving the rest of the hour's budget for unseen keys.
func TestUpstreamEvent_RepeatsMergeBeforeTheHardLimit(t *testing.T) {
	c := newUpstreamEventCollapser()
	hour := evBase.Truncate(time.Hour)
	rows := 0
	kinds := []UpstreamEventKind{UpstreamConnectionLost, UpstreamProcessExited, UpstreamOAuthRefreshFailed}
	for i := 0; i < 59*60; i++ {
		if _, isNew := c.place(UpstreamEvent{Server: "github", Kind: kinds[i%len(kinds)], At: hour.Add(time.Duration(i) * time.Second)}); isNew {
			rows++
		}
	}
	assert.LessOrEqual(t, rows, upstreamMergeAt, "repeats of a known kind stop adding rows at the merge threshold")
	assert.Greater(t, rows, 60, "three kinds a minute did pass sixty rows")
	_, isNew := c.place(UpstreamEvent{Server: "github", Kind: UpstreamLoginRequired, At: hour.Add(59*time.Minute + 30*time.Second)})
	assert.True(t, isNew, "a kind with no row yet still gets one")
}

// Review finding: the collapser forgets servers that have had no event for a
// long time, so removed servers do not stay in memory.
func TestUpstreamEvent_CollapserForgetsIdleServers(t *testing.T) {
	c := newUpstreamEventCollapser()
	for i := 0; i < 200; i++ {
		c.place(UpstreamEvent{Server: fmt.Sprintf("gone-%03d", i), Kind: UpstreamLoginRequired, At: evBase, Seq: 1})
	}
	require.Len(t, c.down, 200)
	c.place(UpstreamEvent{Server: "alive", Kind: UpstreamConnectionLost, At: evBase.Add(collapserServerIdle + time.Hour)})
	assert.Len(t, c.down, 1)
	assert.Len(t, c.seen, 1)
	assert.Empty(t, c.loginOpen)
	assert.Empty(t, c.loginSeq)
	assert.Len(t, c.latest, 1)
	assert.Len(t, c.hourRows, 1)
}

// Review finding: a refresh failure row carries the diagnostics code when the
// error classifies to one in the catalogue.
func TestUpstreamEvent_RefreshFailureKeepsItsCode(t *testing.T) {
	svc, store := eventService(t)
	code := string(diagnostics.OAuthReauthRequired)
	svc.RecordUpstreamEvent(UpstreamEvent{Server: "github", Kind: UpstreamOAuthRefreshFailed, At: evBase, Code: code})
	rows := upstreamEventRows(t, store, "github")
	require.Len(t, rows, 1)
	assert.Equal(t, code, rows[0].Metadata[UpstreamEventMetaCode])
	entry, ok := diagnostics.Get(diagnostics.OAuthReauthRequired)
	require.True(t, ok)
	assert.Equal(t, entry.UserMessage, rows[0].ErrorMessage, "the fixed catalogue message of the code")
}

// Review round 2: a folded transition that hid an outage and its recovery
// still produces both events, in order, before the change the record shows.
func TestTransitionEvents_ReplaysWhatAFoldHid(t *testing.T) {
	got := transitionEvents("srv", "http", types.Transition{
		From: types.StateReady, To: types.StateReady, At: evBase, Folded: 2, FoldedLost: 1, FoldedRestored: 1,
	})
	require.Len(t, got, 2)
	assert.Equal(t, UpstreamConnectionLost, got[0].Kind)
	assert.Equal(t, UpstreamConnectionRestored, got[1].Kind)
	assert.Equal(t, evBase, got[0].At)

	got = transitionEvents("srv", "http", types.Transition{
		From: types.StateError, To: types.StateError, At: evBase, Folded: 1, FoldedLost: 1, FoldedRestored: 1,
		Err: errors.New("x"),
	})
	require.Len(t, got, 2)
	assert.Equal(t, UpstreamConnectionRestored, got[0].Kind, "a record that starts outside Ready hid a recovery first")
	assert.Equal(t, UpstreamConnectionLost, got[1].Kind)

	got = transitionEvents("srv", "http", types.Transition{
		From: types.StateConnecting, To: types.StateConnecting, At: evBase, Folded: 1, FoldedLogin: 1,
	})
	require.Len(t, got, 1)
	assert.Equal(t, UpstreamLoginRequired, got[0].Kind)

	// An unfolded record is exactly what transitionEvent says.
	got = transitionEvents("srv", "http", types.Transition{From: types.StateReady, To: types.StateError, At: evBase, Err: errors.New("x")})
	require.Len(t, got, 1)
	assert.Equal(t, UpstreamConnectionLost, got[0].Kind)

	// Through the collapser: both rows are written.
	svc, store := eventService(t)
	for _, ev := range transitionEvents("srv", "http", types.Transition{
		From: types.StateReady, To: types.StateReady, At: evBase, Folded: 2, FoldedLost: 1, FoldedRestored: 1,
	}) {
		svc.RecordUpstreamEvent(ev)
	}
	rows := upstreamEventRows(t, store, "srv")
	require.Len(t, rows, 2)
}

// Review round 2: a server that only ever had token saves is forgotten too.
func TestUpstreamEvent_CollapserForgetsSaveOnlyServers(t *testing.T) {
	c := newUpstreamEventCollapser()
	for i := 0; i < 50; i++ {
		c.noteTokenSaved(fmt.Sprintf("healthy-%02d", i), "", uint64(i+1), time.Time{})
	}
	require.Len(t, c.loginSeq, 50)
	c.place(UpstreamEvent{Server: "alive", Kind: UpstreamConnectionLost, At: time.Now().Add(collapserServerIdle + time.Hour)})
	assert.Empty(t, c.loginSeq)
	assert.Len(t, c.seen, 1)
}

// Review round 3: a folded record that passed through a login requirement and
// ends in Ready leaves the server up with no login requirement open.
func TestTransitionEvents_FoldedLoginIsClosedByTheRecovery(t *testing.T) {
	c := newUpstreamEventCollapser()
	evs := transitionEvents("srv", "http", types.Transition{
		From: types.StateReady, To: types.StateReady, At: evBase, Folded: 2, FoldedLost: 1, FoldedRestored: 1, FoldedLogin: 1,
	})
	require.Len(t, evs, 3)
	assert.Equal(t, []UpstreamEventKind{UpstreamLoginRequired, UpstreamConnectionLost, UpstreamConnectionRestored},
		[]UpstreamEventKind{evs[0].Kind, evs[1].Kind, evs[2].Kind})
	for _, ev := range evs {
		c.place(ev)
	}
	assert.False(t, c.down["srv"])
	assert.Empty(t, c.loginOpen)
}

// Review round 3: a login failure older than a token save already applied is
// recorded but does not mark the server down again.
func TestUpstreamEvent_StaleLoginFailureDoesNotMarkTheServerDown(t *testing.T) {
	c := newUpstreamEventCollapser()
	c.noteTokenSaved("srv", "", 2, time.Time{})
	row, isNew := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase, Seq: 1})
	require.NotNil(t, row, "the historical failure still has its row")
	assert.True(t, isNew)
	assert.False(t, c.down["srv"])
	assert.Empty(t, c.loginOpen)
	// So a later Ready is not reported as a recovery from it.
	restored, _ := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamConnectionRestored, At: evBase.Add(time.Minute)})
	assert.Nil(t, restored)
}

// Review round 3: an event of an hour whose counter cleanup already dropped
// gets no new row, so a very late delivery cannot restart the hourly bound.
func TestUpstreamEvent_VeryLateEventCannotRestartTheHourlyBound(t *testing.T) {
	c := newUpstreamEventCollapser()
	for i := 0; i < MaxUpstreamEventRowsPerHour+10; i++ {
		c.place(UpstreamEvent{Server: "srv", Kind: UpstreamConnectionLost, At: evBase.Add(time.Duration(i) * 20 * time.Second), Owner: fmt.Sprintf("u%d", i)})
	}
	require.Equal(t, MaxUpstreamEventRowsPerHour, c.hourRows["srv"][evBase.Truncate(time.Hour).Unix()])
	// Hours later the counter of that hour is gone.
	c.place(UpstreamEvent{Server: "srv", Kind: UpstreamConnectionRestored, At: evBase.Add(5 * time.Hour)})
	require.NotContains(t, c.hourRows["srv"], evBase.Truncate(time.Hour).Unix())

	before := c.suppressed
	row, _ := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamConnectionLost, At: evBase.Add(30 * time.Minute), Owner: "late"})
	assert.Nil(t, row)
	assert.Equal(t, before+1, c.suppressed)
	// A current event is recorded as usual.
	row, _ = c.place(UpstreamEvent{Server: "srv", Kind: UpstreamConnectionLost, At: evBase.Add(5*time.Hour + time.Minute)})
	assert.NotNil(t, row)
}

// Review round 3: token saves keep the ordering number of a server that is
// still saving, and cleanup runs from saves alone.
func TestUpstreamEvent_SavesMaintainTheWatermark(t *testing.T) {
	c := newUpstreamEventCollapser()
	now := evBase
	c.now = func() time.Time { return now }

	// A server that saves a token every day for two weeks keeps its number,
	// whatever other servers do.
	for day := 0; day < 14; day++ {
		now = evBase.Add(time.Duration(day) * 24 * time.Hour)
		c.noteTokenSaved("busy", "", uint64(day+1), time.Time{})
		c.place(UpstreamEvent{Server: "other", Kind: UpstreamConnectionLost, At: now})
	}
	require.Equal(t, uint64(14), c.loginSeq[upstreamOwnerKey{"busy", ""}])
	// A failure delivered late, numbered before the last save, opens nothing.
	c.place(UpstreamEvent{Server: "busy", Kind: UpstreamLoginRequired, At: now, Seq: 13})
	assert.Empty(t, c.loginOpen)

	// Save-only churn of server names, with no events at all, is cleaned up.
	c2 := newUpstreamEventCollapser()
	c2.now = func() time.Time { return now }
	for i := 0; i < 100; i++ {
		c2.noteTokenSaved(fmt.Sprintf("gone-%03d", i), "", uint64(i+1), time.Time{})
	}
	now = now.Add(collapserServerIdle + time.Hour)
	c2.noteTokenSaved("fresh", "", 1000, time.Time{})
	assert.Len(t, c2.loginSeq, 1)
	assert.Len(t, c2.seen, 1)
}

// Review round 4 (Spec 124 FR-015): stopping the service records the end of
// every session still open, and a service stopped during its preparation does
// not run a cleanup over a half-prepared log.
func TestActivityService_StopClosesSessionsAndCancelledStartSkipsCleanup(t *testing.T) {
	svc, store := eventService(t)
	for i := 0; i < 5; i++ {
		require.NoError(t, store.SaveActivity(&storage.ActivityRecord{
			Type: storage.ActivityTypeToolCall, Status: storage.ActivityStatusSuccess, ServerName: "github",
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second), WorkSessionID: "ws-open",
		}))
	}
	svc.maxRecords = 1

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc.runRetentionLoop(ctx)
	_, total, err := store.ListActivities(storage.ActivityFilter{Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, 5, total, "no cleanup ran after a cancelled preparation")

	sum, _ := store.GetWorkSessionSummary("ws-open")
	require.NotNil(t, sum)
	require.True(t, sum.EndedAt.IsZero(), "opened after this service was built: not closed at start")

	svc.Stop()
	sum, _ = store.GetWorkSessionSummary("ws-open")
	assert.Equal(t, sum.LastActivityAt, sum.EndedAt, "the end is recorded when the core stops")
}

// Review round 4: a folded record that ends waiting for a login leaves the
// login requirement open, whatever recoveries it hid on the way.
func TestTransitionEvents_FoldedRecordEndingInPendingAuthKeepsTheLoginOpen(t *testing.T) {
	c := newUpstreamEventCollapser()
	c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(-time.Hour), Code: string(diagnostics.OAuthLoginRequired)})
	evs := transitionEvents("srv", "http", types.Transition{
		From: types.StatePendingAuth, To: types.StatePendingAuth, At: evBase,
		Folded: 2, FoldedLost: 1, FoldedRestored: 1, FoldedLogin: 1, FoldedLoginLast: true,
	})
	require.NotEmpty(t, evs)
	assert.Equal(t, UpstreamLoginRequired, evs[len(evs)-1].Kind, "the login requirement is replayed last")
	for _, ev := range evs {
		c.place(ev)
	}
	assert.Len(t, c.loginOpen, 1)
	assert.True(t, c.down["srv"])
}

// Review round 5: hidden counts are carried as weights, not clamped, and a
// login requirement that no recovery followed stays open when the record
// ends in another state.
func TestTransitionEvents_WeightsAndTheLastLogin(t *testing.T) {
	svc, store := eventService(t)
	svc.RecordUpstreamEvent(UpstreamEvent{Server: "srv", Kind: UpstreamConnectionLost, At: evBase.Add(-time.Hour)})
	for _, ev := range transitionEvents("srv", "http", types.Transition{
		From: types.StateError, To: types.StateError, At: evBase, Folded: 200, FoldedLost: 100, FoldedRestored: 100,
	}) {
		svc.RecordUpstreamEvent(ev)
	}
	total := map[string]int{}
	for _, r := range upstreamEventRows(t, store, "srv") {
		kind, _ := r.Metadata[UpstreamEventMetaKind].(string)
		switch n := r.Metadata[UpstreamEventMetaCount].(type) {
		case int:
			total[kind] += n
		case float64:
			total[kind] += int(n)
		}
	}
	assert.Equal(t, 101, total[string(UpstreamConnectionLost)], "the earlier loss and all one hundred hidden ones")
	assert.Equal(t, 100, total[string(UpstreamConnectionRestored)])

	// PendingAuth, Connecting, Ready, Error, PendingAuth, Connecting, Error
	// folded into one record: the second login requirement is still open.
	c := newUpstreamEventCollapser()
	evs := transitionEvents("srv", "http", types.Transition{
		From: types.StatePendingAuth, To: types.StateError, At: evBase,
		Folded: 5, FoldedLost: 1, FoldedRestored: 1, FoldedLogin: 1, FoldedLoginLast: true,
	})
	for _, ev := range evs {
		c.place(ev)
	}
	assert.Len(t, c.loginOpen, 1)
	assert.True(t, c.down["srv"])
}

// Review round 6: a recovery that followed a real loss inside a folded record
// is recorded even when no loss was recorded before the record.
func TestTransitionEvents_LeadingConnectDoesNotSwallowLaterRecoveries(t *testing.T) {
	svc, store := eventService(t)
	for _, ev := range transitionEvents("srv", "http", types.Transition{
		From: types.StateConnecting, To: types.StateError, At: evBase, Err: errors.New("x"),
		Folded: 4, FoldedLost: 2, FoldedRestored: 2,
	}) {
		svc.RecordUpstreamEvent(ev)
	}
	total := map[string]int{}
	for _, r := range upstreamEventRows(t, store, "srv") {
		kind, _ := r.Metadata[UpstreamEventMetaKind].(string)
		switch n := r.Metadata[UpstreamEventMetaCount].(type) {
		case int:
			total[kind] += n
		case float64:
			total[kind] += int(n)
		}
	}
	assert.Equal(t, 1, total[string(UpstreamConnectionRestored)], "the first connect is not a recovery; the second one is")
	assert.Equal(t, 2, total[string(UpstreamConnectionLost)])
}

// Review round 6: a login requirement read from the connection state before a
// token save, and delivered after it, does not reopen the requirement.
func TestUpstreamEvent_StateLoginCapturedBeforeASaveIsStale(t *testing.T) {
	c := newUpstreamEventCollapser()
	now := evBase.Add(10 * time.Minute)
	c.now = func() time.Time { return now }
	c.noteTokenSaved("srv", "", 5, time.Time{})

	row, _ := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase}) // captured earlier, delivered late
	require.NotNil(t, row, "it happened, so it is a row")
	assert.Empty(t, c.loginOpen)
	assert.False(t, c.down["srv"])

	// One captured after the save is a new requirement.
	row, _ = c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(20 * time.Minute)})
	require.NotNil(t, row)
	assert.Len(t, c.loginOpen, 1)
}

// Review round 7: a token-save notification delivered late does not undo a
// login requirement that arose after the save was accepted; and a recovery
// delivered late does not close a login requirement that came after it.
func TestUpstreamEvent_LateDeliveriesDoNotCloseNewerLoginRequirements(t *testing.T) {
	c := newUpstreamEventCollapser()
	savedAt := evBase
	// The save was accepted at evBase; its notification stalls. Meanwhile
	// the connection parks for a login at evBase+5m.
	c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(5 * time.Minute)})
	require.Len(t, c.loginOpen, 1)
	c.noteTokenSaved("srv", "", 1, savedAt)
	assert.Len(t, c.loginOpen, 1, "the requirement is newer than the save")

	// The other order: the save first, then the state event captured later.
	c2 := newUpstreamEventCollapser()
	c2.now = func() time.Time { return evBase.Add(time.Hour) } // delivery time, not used for an explicit save time
	c2.noteTokenSaved("srv", "", 1, savedAt)
	c2.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(5 * time.Minute)})
	assert.Len(t, c2.loginOpen, 1, "captured after the save was accepted: a new requirement")

	// A save accepted after the requirement closes it.
	c.noteTokenSaved("srv", "", 2, evBase.Add(10*time.Minute))
	assert.Empty(t, c.loginOpen)

	// A recovery captured before a numbered failure, delivered after it.
	c3 := newUpstreamEventCollapser()
	c3.place(UpstreamEvent{Server: "srv", Kind: UpstreamConnectionLost, At: evBase})
	c3.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(10 * time.Minute), Seq: 3})
	c3.place(UpstreamEvent{Server: "srv", Kind: UpstreamConnectionRestored, At: evBase.Add(5 * time.Minute)})
	assert.Len(t, c3.loginOpen, 1, "the failure came after that recovery")
	assert.True(t, c3.down["srv"])
	c3.place(UpstreamEvent{Server: "srv", Kind: UpstreamConnectionRestored, At: evBase.Add(15 * time.Minute)})
	assert.Empty(t, c3.loginOpen)
	assert.False(t, c3.down["srv"])
}

// Review round 7: summaries the backfill builds at start belong to the
// previous process and are closed, like the ones that already existed.
func TestActivityService_BackfilledSessionsAreClosedAtStart(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()
	// A row of the previous process, stored the way a pre-124 build did: no
	// index entry and no summary.
	rec := &storage.ActivityRecord{
		ID: "01LEGACYROW000000000000000", Type: storage.ActivityTypeToolCall, Status: storage.ActivityStatusSuccess,
		ServerName: "github", ToolName: "list_issues", Timestamp: time.Now().UTC().Add(-5 * time.Minute), WorkSessionID: "ws-legacy",
	}
	data, err := rec.MarshalBinary()
	require.NoError(t, err)
	require.NoError(t, store.GetDB().Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte(storage.ActivityRecordsBucket)).Put(
			[]byte(fmt.Sprintf("%020d_%s", rec.Timestamp.UnixNano(), rec.ID)), data)
	}))

	svc := NewActivityService(store, zap.NewNop())
	svc.prepareWorkSessions(context.Background())
	sum, err := store.GetWorkSessionSummary("ws-legacy")
	require.NoError(t, err)
	require.NotNil(t, sum, "the backfill built it")
	assert.False(t, sum.EndedAt.IsZero(), "and it is closed: its process is gone")
}

// Review round 8: the classifier the state managers use agrees with the
// event mapping; a login failure from before a recovery already processed is
// history; and a requirement closed by a save does not take the next one
// into its row, however soon it comes.
func TestUpstreamEvent_LoginEpisodes(t *testing.T) {
	loginErr := errors.New("OAuth authentication required")
	tr := types.Transition{From: types.StateConnecting, To: types.StateError, At: evBase, TerminalCode: string(diagnostics.OAuthReauthRequired), Err: loginErr}
	assert.True(t, transitionIsLoginError("srv", "http", tr))
	ev, ok := transitionEvent("srv", "http", tr)
	require.True(t, ok)
	assert.Equal(t, UpstreamLoginRequired, ev.Kind)
	assert.False(t, transitionIsLoginError("srv", "http", types.Transition{From: types.StateConnecting, To: types.StateError, Err: errors.New("connection refused")}))
	assert.False(t, transitionIsLoginError("srv", "http", types.Transition{From: types.StateConnecting, To: types.StateReady}))

	// A failure accepted at T1, a recovery at T2 processed first.
	c := newUpstreamEventCollapser()
	c.place(UpstreamEvent{Server: "srv", Kind: UpstreamConnectionRestored, At: evBase.Add(2 * time.Minute)}) // no row: nothing was down
	row, _ := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(time.Minute), Seq: 1})
	require.NotNil(t, row, "it happened, so it is a row")
	assert.Empty(t, c.loginOpen)
	assert.False(t, c.down["srv"])

	// Failure, save, failure inside one minute: two rows.
	c2 := newUpstreamEventCollapser()
	first, isNew := c2.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase, Seq: 2})
	require.True(t, isNew)
	c2.noteTokenSaved("srv", "", 3, evBase.Add(10*time.Second))
	second, isNew := c2.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(20 * time.Second), Seq: 4})
	require.True(t, isNew, "a new episode has its own row")
	assert.NotEqual(t, first.id, second.id)
	assert.Equal(t, evBase.Add(20*time.Second), second.firstAt)
	// Inside an open episode repeats still collapse.
	again, isNew := c2.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(30 * time.Second), Seq: 5})
	assert.False(t, isNew)
	assert.Equal(t, second.id, again.id)
}

// Review round 9: a stale login failure delivered after the save that closed
// it gets its row, but the next requirement, inside the same minute, is a new
// episode with its own row and time.
func TestUpstreamEvent_StaleFailureRowIsNotAMergeTarget(t *testing.T) {
	c := newUpstreamEventCollapser()
	c.noteTokenSaved("srv", "", 2, evBase.Add(10*time.Second))
	stale, isNew := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase, Seq: 1})
	require.NotNil(t, stale)
	require.True(t, isNew)
	assert.Empty(t, c.loginOpen)

	fresh, isNew := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(20 * time.Second), Seq: 3})
	require.True(t, isNew)
	assert.NotEqual(t, stale.id, fresh.id)
	assert.Equal(t, evBase.Add(20*time.Second), fresh.firstAt)
	assert.Same(t, fresh, c.loginOpen[upstreamOwnerKey{"srv", ""}])
}

// Review round 10: failure 1 opens a requirement; a login saves a token
// (2) whose notification stalls; the new grant fails (3) and that arrives
// first. The second failure is a new episode with its own row, and the late
// save changes nothing.
func TestUpstreamEvent_FailureDeliveredBeforeItsSaveIsANewEpisode(t *testing.T) {
	c := newUpstreamEventCollapser()
	first, isNew := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase, Seq: 1})
	require.True(t, isNew)

	second, isNew := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(20 * time.Second), Seq: 3, PrevSeq: 2, PrevAt: evBase.Add(10 * time.Second)})
	require.True(t, isNew, "the save in between is known to exist: a new episode")
	assert.NotEqual(t, first.id, second.id)
	assert.Equal(t, evBase.Add(20*time.Second), second.firstAt)

	c.noteTokenSaved("srv", "", 2, evBase.Add(10*time.Second)) // the late save
	assert.Same(t, second, c.loginOpen[upstreamOwnerKey{"srv", ""}], "it is older than the open requirement")

	// In order, nothing changes: the save was applied, so the failure after
	// it opens its row the usual way and a repeat of it merges.
	c2 := newUpstreamEventCollapser()
	c2.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase, Seq: 1})
	c2.noteTokenSaved("srv", "", 2, evBase.Add(10*time.Second))
	a, isNew := c2.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(20 * time.Second), Seq: 3, PrevSeq: 2, PrevAt: evBase.Add(10 * time.Second)})
	require.True(t, isNew)
	b, isNew := c2.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(30 * time.Second)})
	assert.False(t, isNew, "the connection state reporting the same condition merges")
	assert.Equal(t, a.id, b.id)
}

// Review round 11: the connection reports the new grant's login requirement
// before the numbered failure, while the grant's save is still on its way.
// The requirement began after that save, so it is this same episode: the
// failure merges into it and no second row is written.
func TestUpstreamEvent_StateRequirementBeforeItsNumberedFailureIsOneEpisode(t *testing.T) {
	c := newUpstreamEventCollapser()
	c.noteTokenSaved("srv", "", 1, evBase)
	saved2 := evBase.Add(10 * time.Second) // save 2 accepted; its notification stalls
	state, isNew := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(15 * time.Second)})
	require.True(t, isNew)
	numbered, isNew := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(20 * time.Second), Seq: 3, PrevSeq: 2, PrevAt: saved2})
	assert.False(t, isNew)
	assert.Equal(t, state.id, numbered.id)
	assert.Equal(t, 2, numbered.count)
}

// Review round 12: failure 1 opens a row; save 2 is accepted but stalls; the
// connection reports a requirement that merges into the old row; then the
// save arrives. The old row began before the save, so it ends, and the new
// grant's failure gets its own row.
func TestUpstreamEvent_LateSaveEndsARequirementThatBeganBeforeIt(t *testing.T) {
	c := newUpstreamEventCollapser()
	old, _ := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase, Seq: 1})
	merged, isNew := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(15 * time.Second)})
	require.False(t, isNew)
	require.Equal(t, old.id, merged.id)

	c.noteTokenSaved("srv", "", 2, evBase.Add(10*time.Second))
	assert.Empty(t, c.loginOpen, "it began before the save")

	fresh, isNew := c.place(UpstreamEvent{Server: "srv", Kind: UpstreamLoginRequired, At: evBase.Add(20 * time.Second), Seq: 3, PrevSeq: 2, PrevAt: evBase.Add(10 * time.Second)})
	require.True(t, isNew)
	assert.NotEqual(t, old.id, fresh.id)
}
