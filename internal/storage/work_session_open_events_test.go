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
	ev("owned", -2*time.Minute, "oauth_refresh_failed", "bob")
	ev("owned", -4*time.Minute, "connection_lost", "")

	all := func(string) bool { return true }
	refs, err := m.OpenUpstreamEventsBefore([]string{"down", "healed", "owned", "never"}, wsBase, all)
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
	refs, err = m.OpenUpstreamEventsBefore([]string{"owned"}, wsBase, notBob)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, wsBase.Add(-4*time.Minute), refs[0].Timestamp)
	assert.Empty(t, refs[0].Owner)
}
