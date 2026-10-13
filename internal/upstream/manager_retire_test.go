package upstream

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// Spec 117 PR-3 (#1565, FR-036, US3 scenario 6): the manager retires a client
// it replaces or removes, so a tools/list that client captured can no longer
// be applied by the runtime.

func retireTestServer(url string) *config.ServerConfig {
	return &config.ServerConfig{Name: "srv", URL: url, Protocol: "http", Enabled: true}
}

func TestManager_EndpointReplacementRetiresOldClient(t *testing.T) {
	t.Setenv("CI", "")
	m := NewManager(zap.NewNop(), &config.Config{}, nil, secret.NewResolver(), nil)
	t.Cleanup(func() { m.shutdownCancel() })

	require.NoError(t, m.AddServerConfig("srv", retireTestServer("http://127.0.0.1:1/a")))
	old, ok := m.GetClient("srv")
	require.True(t, ok)
	require.False(t, old.Retired())

	// Same connection config: the instance is kept and stays current.
	require.NoError(t, m.AddServerConfig("srv", retireTestServer("http://127.0.0.1:1/a")))
	same, _ := m.GetClient("srv")
	require.Same(t, old, same)
	assert.False(t, old.Retired(), "a retained instance is not retired")

	// Endpoint change: the old instance is replaced and retired.
	require.NoError(t, m.AddServerConfig("srv", retireTestServer("http://127.0.0.1:1/b")))
	repl, ok := m.GetClient("srv")
	require.True(t, ok)
	require.NotSame(t, old, repl)
	assert.True(t, old.Retired(), "the replaced client is retired")
	assert.False(t, repl.Retired())

	m.RemoveServer("srv")
	assert.True(t, repl.Retired(), "a removed client is retired")
}
