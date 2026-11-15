package runtime

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		name     string
		tr       types.Transition
		wantKind UpstreamEventKind
		wantCode string
		none     bool
	}{
		{"ready to error is a loss", types.Transition{From: types.StateReady, To: types.StateError, At: at, Err: errors.New("EOF")}, UpstreamConnectionLost, "", false},
		{"ready to disconnected is a loss", types.Transition{From: types.StateReady, To: types.StateDisconnected, At: at}, UpstreamConnectionLost, "", false},
		{"a terminal stdio exit is a process exit", types.Transition{From: types.StateReady, To: types.StateError, At: at, Err: errors.New("x"), TerminalCode: string(diagnostics.STDIOExitNonzero)}, UpstreamProcessExited, string(diagnostics.STDIOExitNonzero), false},
		{"back to ready is a recovery", types.Transition{From: types.StateDiscovering, To: types.StateReady, At: at}, UpstreamConnectionRestored, "", false},
		{"parking for login", types.Transition{From: types.StateConnecting, To: types.StatePendingAuth, At: at, Err: errors.New("login")}, UpstreamLoginRequired, string(diagnostics.OAuthLoginRequired), false},
		{"a reconnect attempt that fails again is not an event", types.Transition{From: types.StateConnecting, To: types.StateError, At: at, Err: errors.New("refused")}, "", "", true},
		{"connecting is not an event", types.Transition{From: types.StateDisconnected, To: types.StateConnecting, At: at}, "", "", true},
		{"ready to ready is not an event", types.Transition{From: types.StateReady, To: types.StateReady, At: at}, "", "", true},
	}
	for _, tc := range cases {
		ev, ok := transitionEvent("github", tc.tr)
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
	ev, ok := transitionEvent("github", types.Transition{From: types.StateReady, To: types.StateError, At: evBase, Err: raw})
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
	svc.NoteUpstreamTokenSaved("github", "", 1)
	login(20*time.Minute, 0)
	require.Len(t, loginRows(), 1, "a stale save does not clear the mark")

	// A successful login clears it; the next condition gets its own row.
	svc.NoteUpstreamTokenSaved("github", "", 3)
	login(30*time.Minute, 4)
	require.Len(t, loginRows(), 2)

	// A failure accepted before a save and delivered after it: written, but it
	// neither opens nor updates the mark.
	svc.NoteUpstreamTokenSaved("github", "", 6)
	login(40*time.Minute, 5)
	require.Len(t, loginRows(), 3, "it happened, so it is a row")
	login(50*time.Minute, 7)
	rows = loginRows()
	require.Len(t, rows, 4, "the next real condition is not merged into the stale one")

	// A failure of a schedule delivered before the save that installed it
	// leaves the mark open.
	svc.NoteUpstreamTokenSaved("github", "", 6)
	login(55*time.Minute, 0)
	assert.Len(t, loginRows(), 4, "still the same open condition")

	// A recovery closes it too.
	svc.RecordUpstreamEvent(UpstreamEvent{Server: "github", Kind: UpstreamConnectionRestored, At: evBase.Add(time.Hour)})
	login(2*time.Hour, 0)
	assert.Len(t, loginRows(), 5)
}
