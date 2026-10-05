package upstream

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// Item 4b: at startup the supervisor's reconcile (actor pool) starts the
// connect, and backgroundInitialization -> LoadConfiguredServers then calls
// AddServer for the same unchanged server. managed.Client.Connect correctly
// refuses a second concurrent connect, but AddServer surfaced that refusal as
// an error, which LoadConfiguredServers logged at ERROR ("connection already
// in progress or established (state: Connecting)") and which also skipped
// RegisterServerIdentity for that server. An in-flight connect owned by
// someone else is not a failure of AddServer.
func TestAddServer_UnchangedServerAlreadyConnectingIsNotAnError(t *testing.T) {
	m := NewManager(zap.NewNop(), &config.Config{}, nil, secret.NewResolver(), nil)
	sc := &config.ServerConfig{
		Name: "fx", Command: "/nonexistent/never-run", Protocol: "stdio", Enabled: true,
	}

	require.NoError(t, m.AddServerConfig("fx", sc))
	client, ok := m.GetClient("fx")
	require.True(t, ok)

	// The supervisor's connect is in flight.
	client.StateManager.TransitionTo(types.StateConnecting)

	// LoadConfiguredServers' AddServer for the same, unchanged config.
	require.NoError(t, m.AddServer("fx", sc),
		"a connect already in progress for an unchanged server must not be reported as a failure")
	require.Equal(t, types.StateConnecting, client.StateManager.GetState(),
		"the in-flight connect must be left alone (no second attempt, no reset)")
}
