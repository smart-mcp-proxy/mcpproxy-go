package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// UpdateServer is the REST PATCH / config-to-secret write path. It starts from
// the stored record, so an update that never mentioned `quarantined` must keep
// the stored value instead of copying whatever false the caller's struct holds.

func updateServerFixture(t *testing.T) (*Server, string) {
	t.Helper()
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{
		Name: "victim", Command: "true", Protocol: "stdio", Enabled: true, Quarantined: true,
	}})
	srv := proxy.mainServer
	srv.logger = zap.NewNop()

	// Make sure storage holds the quarantined record, and point the runtime at a
	// config file so SaveConfiguration has somewhere to write.
	require.NoError(t, rt.StorageManager().SaveUpstreamServer(&config.ServerConfig{
		Name: "victim", Command: "true", Protocol: "stdio", Enabled: true, Quarantined: true,
	}))
	cfgPath := filepath.Join(t.TempDir(), "mcp_config.json")
	require.NoError(t, config.SaveConfig(rt.Config(), cfgPath))
	rt.UpdateConfig(rt.Config(), cfgPath)
	return srv, cfgPath
}

func TestUpdateServer_OmittedQuarantineKeepsStoredValue(t *testing.T) {
	srv, _ := updateServerFixture(t)

	updates := &config.ServerConfig{Args: []string{"--x"}, Enabled: true} // Quarantined=false, never stated
	require.NoError(t, srv.UpdateServer(context.Background(), "victim", updates))

	got, err := srv.runtime.StorageManager().GetUpstreamServer("victim")
	require.NoError(t, err)
	assert.True(t, got.Quarantined, "an update that did not state quarantine must keep the stored value")
	assert.Equal(t, []string{"--x"}, got.Args)
}

func TestUpdateServer_ExplicitFalseUnquarantines(t *testing.T) {
	srv, cfgPath := updateServerFixture(t)

	updates := &config.ServerConfig{Enabled: true, Quarantined: false}
	updates.MarkQuarantineExplicitlySet(true)
	require.NoError(t, srv.UpdateServer(context.Background(), "victim", updates))

	got, err := srv.runtime.StorageManager().GetUpstreamServer("victim")
	require.NoError(t, err)
	assert.False(t, got.Quarantined, "an explicit operator decision is applied")

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var doc struct {
		Servers []map[string]any `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	require.Len(t, doc.Servers, 1)
	assert.Equal(t, false, doc.Servers[0]["quarantined"], "the saved file must carry the stated value")
}
