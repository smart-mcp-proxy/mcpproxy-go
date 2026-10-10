package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// UX-01 review r2: an older ApplyConfig reload runs LoadConfiguredServers with
// a captured config snapshot. If a create-only add lands after that snapshot
// was taken, the late reload used to see the new server in storage but not in
// its snapshot and delete it. The deletion must be judged against the live
// config (under the commit lock), not the stale snapshot.
func TestLoadConfiguredServers_StaleSnapshotDoesNotDeleteNewlyAddedServer(t *testing.T) {
	rt := newPurgeTestRuntime(t)

	cur0 := *rt.Config()
	stale := &cur0
	stale.Servers = append([]*config.ServerConfig(nil), cur0.Servers...)

	added := &config.ServerConfig{Name: "late-add", URL: "http://127.0.0.1:1/mcp", Protocol: "http", Enabled: false, Quarantined: true, Created: time.Now()}
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		if err := rt.StorageManager().CreateUpstreamServer(added); err != nil {
			return nil, err
		}
		n := *cur
		next := &n
		next.Servers = append(append([]*config.ServerConfig(nil), cur.Servers...), added)
		return next, nil
	}))

	// The older apply's reload finally runs with its pre-add snapshot.
	require.NoError(t, rt.LoadConfiguredServers(stale))

	// Removal is asynchronous; give it ample time to (wrongly) happen.
	time.Sleep(400 * time.Millisecond)

	got, err := rt.StorageManager().GetUpstreamServer("late-add")
	require.NoError(t, err)
	require.NotNil(t, got, "stale reload must not delete a server added after its snapshot")

	// A subsequent save must still carry it to disk and keep it in runtime.
	require.NoError(t, rt.SaveConfiguration())
	found := false
	for _, s := range rt.Config().Servers {
		if s.Name == "late-add" {
			found = true
		}
	}
	require.True(t, found, "server must survive in runtime config after another save")
}
