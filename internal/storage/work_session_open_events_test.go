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
	refs, err := m.OpenUpstreamEventsBefore([]string{"down", "healed", "owned", "never"}, wsBase, time.Time{}, all)
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
	refs, err = m.OpenUpstreamEventsBefore([]string{"owned"}, wsBase, time.Time{}, notBob)
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

	refs, err := m.OpenUpstreamEventsBefore([]string{"refresh", "relost", "stale"}, wsBase, wsBase.Add(-time.Hour), nil)
	require.NoError(t, err)
	got := map[string]bool{}
	for _, r := range refs {
		got[r.Server] = true
	}
	assert.False(t, got["refresh"], "a refresh failure is not evidence the server was down")
	assert.True(t, got["relost"], "lost again after the recovery: still open")
	assert.False(t, got["stale"], "from before the core started: not evidence")
}
