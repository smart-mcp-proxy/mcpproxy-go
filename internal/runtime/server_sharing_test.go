package runtime

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/require"
)

func TestServerSharingPublishedBeforeReturn(t *testing.T) {
	rt, path := newPendingRestartRuntime(t)
	cfg := rt.ConfigSnapshot().Clone()
	cfg.Servers = []*config.ServerConfig{{Name: "fixture", URL: "http://127.0.0.1:1/mcp", Protocol: "streamable-http", Enabled: false, Shared: true}}
	_, err := rt.ApplyConfig(cfg, path)
	require.NoError(t, err)
	old := rt.Config()
	updated, err := rt.SetServerShared("FiXtUrE", false)
	require.NoError(t, err)
	require.Equal(t, "fixture", updated.Name, "response must preserve the configured canonical name")
	require.False(t, rt.Config().Servers[0].Shared)
	require.True(t, old.Servers[0].Shared, "published snapshots must stay immutable")
	saved, err := config.LoadFromFile(path)
	require.NoError(t, err)
	require.False(t, saved.Servers[0].Shared)
	_, err = rt.SetServerShared("fixture", true)
	require.NoError(t, err)
	require.True(t, rt.Config().Servers[0].Shared)
}

func TestServerSharingSurvivesConfigurationSave(t *testing.T) {
	rt, path := newPendingRestartRuntime(t)
	cfg := rt.ConfigSnapshot().Clone()
	var broker config.AuthBrokerConfig
	require.NoError(t, json.Unmarshal([]byte(`{
		"mode":"oauth_connect",
		"authorization_endpoint":"https://issuer.example/authorize",
		"token_endpoint":"https://issuer.example/token",
		"client_id":"fixture-client",
		"client_secret":"fixture-secret"
	}`), &broker))
	cfg.Servers = []*config.ServerConfig{{
		Name: "fixture", URL: "http://127.0.0.1:1/mcp", Protocol: "streamable-http", Enabled: false, Shared: true,
		AuthBroker: &broker,
	}}
	_, err := rt.ApplyConfig(cfg, path)
	require.NoError(t, err)
	require.NoError(t, rt.storageManager.SaveUpstreamServer(cfg.Servers[0]))
	require.NoError(t, rt.SaveConfiguration())
	require.True(t, rt.Config().Servers[0].Shared)
	require.NotNil(t, rt.Config().Servers[0].AuthBroker)
	saved, err := config.LoadFromFile(path)
	require.NoError(t, err)
	require.True(t, saved.Servers[0].Shared)
	require.NotNil(t, saved.Servers[0].AuthBroker)
}

func TestAdmissionPublicationCannotRestoreStaleSharing(t *testing.T) {
	rt, path := newPendingRestartRuntime(t)
	cfg := rt.ConfigSnapshot().Clone()
	cfg.Servers = []*config.ServerConfig{{Name: "fixture", URL: "http://127.0.0.1:1/mcp", Protocol: "streamable-http", Enabled: false, Shared: true}}
	_, err := rt.ApplyConfig(cfg, path)
	require.NoError(t, err)

	// Spec 116 removed the admission re-publication of a derived (possibly
	// stale) config; the decision it carried is now a commit of its own, which
	// reads the live entry under the commit lock.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, setErr := rt.SetServerShared("fixture", false)
		require.NoError(t, setErr)
	}()
	go func() {
		defer wg.Done()
		_, commitErr := rt.Commit("mark_quarantine_explicit", func(tx *CommitTx) error {
			return tx.UpdateServer("fixture", func(cur *config.ServerConfig) error {
				cur.MarkQuarantineExplicitlySet(true)
				return nil
			})
		})
		require.NoError(t, commitErr)
	}()
	wg.Wait()

	require.False(t, rt.Config().Servers[0].Shared)
	saved, err := config.LoadFromFile(path)
	require.NoError(t, err)
	require.False(t, saved.Servers[0].Shared)
}
