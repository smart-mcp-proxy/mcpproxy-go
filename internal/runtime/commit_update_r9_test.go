package runtime

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func liveServerNamed(rt *Runtime, name string) *config.ServerConfig {
	for _, s := range rt.Config().Servers {
		if s != nil && s.Name == name {
			return s
		}
	}
	return nil
}

// UX-01 r9: an update built from the stored record must not erase the
// configuration-only Shared and AuthBroker fields of the live entry.
func TestCommitServerUpdate_KeepsSharedAndAuthBroker(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	a := r6Server("srv-a", "http://127.0.0.1:1/mcp", false)
	a.Shared = true
	require.NoError(t, json.Unmarshal([]byte(`{"mode":"oauth_connect","token_endpoint":"https://idp.example/token"}`), &a.AuthBroker))
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(a))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{a}
		return &n, nil
	}))
	require.True(t, liveServerNamed(rt, "srv-a").Shared)
	require.NotNil(t, liveServerNamed(rt, "srv-a").AuthBroker)

	require.NoError(t, rt.CommitServerUpdate("srv-a", func(existing *config.ServerConfig) (*config.ServerConfig, error) {
		existing.URL = "http://127.0.0.1:2/mcp"
		return existing, nil
	}, nil))

	live := liveServerNamed(rt, "srv-a")
	require.Equal(t, "http://127.0.0.1:2/mcp", live.URL)
	require.True(t, live.Shared, "update unshared the server")
	require.NotNil(t, live.AuthBroker, "update dropped the auth broker config")
	raw, err := os.ReadFile(rt.ConfigPath())
	require.NoError(t, err)
	require.Contains(t, string(raw), `"shared": true`)
	require.Contains(t, string(raw), `"auth_broker"`)
}

// UX-01 r9: a server an applied config removed is not found for an update even
// while its storage row waits for the asynchronous cleanup.
func TestCommitServerUpdate_RemovedByApplyConfigIsNotFound(t *testing.T) {
	rt := newPurgeTestRuntime(t)
	keep := r6Server("keep", "http://127.0.0.1:2/mcp", false)
	a := r6Server("srv-a", "http://127.0.0.1:1/mcp", false)
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(keep))
	require.NoError(t, rt.StorageManager().CreateUpstreamServer(a))
	require.NoError(t, rt.UpdateConfigFrom(func(cur *config.Config) (*config.Config, error) {
		n := *cur
		n.Servers = []*config.ServerConfig{keep, a}
		return &n, nil
	}))

	parked := make(chan struct{})
	gate := make(chan struct{})
	serverRemovalCleanupHook = func(n string) {
		if n == "srv-a" {
			close(parked)
			<-gate
		}
	}
	t.Cleanup(func() { serverRemovalCleanupHook = nil })

	_, err := rt.ApplyConfig(validConfigWith(rt, keep), "")
	require.NoError(t, err)
	select {
	case <-parked:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup never started")
	}

	built := false
	err = rt.CommitServerUpdate("srv-a", func(existing *config.ServerConfig) (*config.ServerConfig, error) {
		built = true
		existing.URL = "http://127.0.0.1:9/mcp"
		return existing, nil
	}, func(*config.ServerConfig) { t.Error("apply ran for a removed server") })
	require.ErrorIs(t, err, ErrServerNotFound)
	require.False(t, built)
	require.Nil(t, liveServerNamed(rt, "srv-a"), "update republished the removed server")
	close(gate)
	waitServerGone(t, rt, "srv-a")
	time.Sleep(200 * time.Millisecond)
	require.ErrorIs(t, rt.CommitServerUpdate("srv-a", func(e *config.ServerConfig) (*config.ServerConfig, error) { return e, nil }, nil), ErrServerNotFound)
}
