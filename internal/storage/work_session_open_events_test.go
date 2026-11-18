package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Release QA F2: a session that starts while a server is already down has
// the event that explains it from before its span: the latest event of the
// server before the session, while no recovery followed it.
func TestOpenUpstreamEventsBefore(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	ev := func(server string, offset time.Duration, kind, owner string) {
		status := ActivityStatusError
		if kind == "connection_restored" {
			status = ActivityStatusSuccess
		}
		require.NoError(t, m.SaveActivity(&ActivityRecord{
			Type: ActivityTypeUpstreamEvent, Status: status, ServerName: server, UserID: owner,
			Timestamp: wsBase.Add(offset), Source: ActivitySourceInternal, Metadata: map[string]interface{}{"kind": kind},
		}))
	}
	ev("down", -10*time.Minute, "connection_lost", "")
	ev("down", -5*time.Minute, "connection_restored", "")
	ev("down", -time.Minute, "login_required", "") // the open one
	ev("down", time.Minute, "connection_lost", "") // inside the span: not this query's
	ev("healed", -3*time.Minute, "connection_lost", "")
	ev("healed", -2*time.Minute, "connection_restored", "")
	ev("owned", -2*time.Minute, "login_required", "bob")
	ev("owned", -4*time.Minute, "connection_lost", "")

	all := func(string) bool { return true }
	refs, err := m.OpenUpstreamEventsBefore(allKinds("down", "healed", "owned", "never"), wsBase, time.Time{}, all)
	require.NoError(t, err)
	byServer := map[string]WorkSessionRowRef{}
	for _, r := range refs {
		byServer[r.Server] = r
	}
	require.Len(t, byServer, 2, "a server whose last event is a recovery has nothing open")
	assert.Equal(t, wsBase.Add(-time.Minute), byServer["down"].Timestamp)
	assert.True(t, byServer["down"].Event)
	assert.Equal(t, "bob", byServer["owned"].Owner)

	// A caller who may not see bob's event gets the latest one it may see.
	notBob := func(owner string) bool { return owner == "" }
	refs, err = m.OpenUpstreamEventsBefore(allKinds("owned"), wsBase, time.Time{}, notBob)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, wsBase.Add(-4*time.Minute), refs[0].Timestamp)
	assert.Empty(t, refs[0].Owner)
}

// Review (Opus, F2 round): only a connection event is evidence that a server
// was down; a recovery is judged by when the rows last happened, since a
// repeat merges into its row; and nothing from before the core's own start
// counts, because the recovery after a restart is not recorded.
func TestOpenUpstreamEventsBefore_EvidenceRules(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	put := func(server string, first, last time.Duration, kind string) {
		status := ActivityStatusError
		if kind == "connection_restored" {
			status = ActivityStatusSuccess
		}
		require.NoError(t, m.SaveActivity(&ActivityRecord{
			Type: ActivityTypeUpstreamEvent, Status: status, ServerName: server, Timestamp: wsBase.Add(first),
			Source: ActivitySourceInternal, Metadata: map[string]interface{}{
				"kind": kind, "first_at": wsBase.Add(first).Format(time.RFC3339Nano), "last_at": wsBase.Add(last).Format(time.RFC3339Nano),
			},
		}))
	}
	// A refresh failure says nothing about the connection.
	put("refresh", -time.Minute, -time.Minute, "oauth_refresh_failed")
	// Lost at -60s, restored at -50s, lost again at -40s: the second loss
	// merged into the first row, so the latest row is the recovery.
	put("relost", -60*time.Second, -40*time.Second, "connection_lost")
	put("relost", -50*time.Second, -50*time.Second, "connection_restored")
	// Lost before the core started.
	put("stale", -2*time.Hour, -2*time.Hour, "connection_lost")

	refs, err := m.OpenUpstreamEventsBefore(allKinds("refresh", "relost", "stale"), wsBase, wsBase.Add(-time.Hour), nil)
	require.NoError(t, err)
	got := map[string]bool{}
	for _, r := range refs {
		got[r.Server] = true
	}
	assert.False(t, got["refresh"], "a refresh failure is not evidence the server was down")
	assert.True(t, got["relost"], "lost again after the recovery: still open")
	assert.False(t, got["stale"], "from before the core started: not evidence")
}

// Review (Opus, F2 round 2): a recovery whose repeat merged into an older
// row still closes a newer loss. Lost t0, restored t10, lost t62 (a new
// row), restored t65 (merged into the t10 row): nothing is open at t120.
func TestOpenUpstreamEventsBefore_MergedRecoveryClosesANewerLoss(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	put := func(first, last time.Duration, kind string) {
		status := ActivityStatusError
		if kind == "connection_restored" {
			status = ActivityStatusSuccess
		}
		require.NoError(t, m.SaveActivity(&ActivityRecord{
			Type: ActivityTypeUpstreamEvent, Status: status, ServerName: "flap", Timestamp: wsBase.Add(first),
			Source: ActivitySourceInternal, Metadata: map[string]interface{}{
				"kind": kind, "first_at": wsBase.Add(first).Format(time.RFC3339Nano), "last_at": wsBase.Add(last).Format(time.RFC3339Nano),
			},
		}))
	}
	put(0, 0, "connection_lost")
	put(10*time.Second, 65*time.Second, "connection_restored")
	put(62*time.Second, 62*time.Second, "connection_lost")
	refs, err := m.OpenUpstreamEventsBefore(allKinds("flap"), wsBase.Add(120*time.Second), time.Time{}, nil)
	require.NoError(t, err)
	assert.Empty(t, refs, "the server recovered at t65")
}

// A failure anywhere in the session that a down server explains marks the
// partition, whatever the session's last failure on that server was.
func TestWorkSessionPartition_MarksAnUnreachableFailure(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	require.NoError(t, m.SaveActivity(wsRow("ws-u", 0, func(r *ActivityRecord) { r.Status, r.ErrorClass = ActivityStatusError, "network" })))
	require.NoError(t, m.SaveActivity(wsRow("ws-u", time.Minute, func(r *ActivityRecord) { r.Status, r.ErrorClass = ActivityStatusError, "tool_error" })))
	require.NoError(t, m.SaveActivity(wsRow("ws-t", 0, func(r *ActivityRecord) { r.Status, r.ErrorClass = ActivityStatusError, "tool_error" })))
	u, err := m.GetWorkSessionSummary("ws-u")
	require.NoError(t, err)
	assert.True(t, u.Servers["github"].NetworkFailure)
	assert.False(t, u.Servers["github"].AuthFailure)
	assert.Equal(t, "tool_error", u.Servers["github"].LastErrorClass)
	tt, err := m.GetWorkSessionSummary("ws-t")
	require.NoError(t, err)
	assert.False(t, tt.Servers["github"].NetworkFailure)
	assert.False(t, tt.Servers["github"].AuthFailure)
}

func allKinds(servers ...string) map[string][]string {
	out := map[string][]string{}
	for _, s := range servers {
		out[s] = []string{"connection_lost", "process_exited", "login_required"}
	}
	return out
}

// Review (Opus, F2 round 3): the kind asked for must explain the failure —
// a login requirement raised while the connection stayed up is no cause of
// a network failure; and a recovery merged into an older row after the
// session started does not close a loss from before it.
func TestOpenUpstreamEventsBefore_KindAndMergedAfterStart(t *testing.T) {
	m, cleanup := setupTestStorageForActivity(t)
	defer cleanup()
	put := func(server string, first, last time.Duration, kind string) {
		status := ActivityStatusError
		if kind == "connection_restored" {
			status = ActivityStatusSuccess
		}
		require.NoError(t, m.SaveActivity(&ActivityRecord{
			Type: ActivityTypeUpstreamEvent, Status: status, ServerName: server, Timestamp: wsBase.Add(first),
			Source: ActivitySourceInternal, Metadata: map[string]interface{}{
				"kind": kind, "first_at": wsBase.Add(first).Format(time.RFC3339Nano), "last_at": wsBase.Add(last).Format(time.RFC3339Nano),
			},
		}))
	}
	put("tok", -time.Minute, -time.Minute, "login_required") // the refresh manager gave up; still connected
	put("flap", 0, 30*time.Second, "connection_restored")    // the t30 recovery merged into the t0 row
	put("flap", 10*time.Second, 10*time.Second, "connection_lost")

	network := []string{"connection_lost", "process_exited"}
	refs, err := m.OpenUpstreamEventsBefore(map[string][]string{"tok": network, "flap": network}, wsBase.Add(20*time.Second), time.Time{}, nil)
	require.NoError(t, err)
	got := map[string]bool{}
	for _, r := range refs {
		got[r.Server] = true
	}
	assert.False(t, got["tok"], "a login requirement does not explain a network failure")
	assert.True(t, got["flap"], "the recovery at t30 came after the session started at t20")

	refs, err = m.OpenUpstreamEventsBefore(map[string][]string{"tok": {"login_required"}}, wsBase.Add(20*time.Second), time.Time{}, nil)
	require.NoError(t, err)
	assert.Len(t, refs, 1, "it does explain an auth failure")
}
