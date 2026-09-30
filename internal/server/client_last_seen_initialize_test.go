package server

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 109 T124a: the MCP initialize hook records client_last_seen through the
// runtime even when telemetry is off, the record survives a storage reopen, and
// it does not duplicate into the telemetry activation list (mcp_clients_seen_ever).
func TestInitializeHook_PersistsClientLastSeenWithTelemetryOff(t *testing.T) {
	t.Setenv("MCPPROXY_TELEMETRY", "false")
	// The data dir lives outside the environment's temp dir so env.Cleanup()
	// does not delete it before the reopen below.
	dataDir := t.TempDir()
	require.NoError(t, os.Chmod(dataDir, 0o700))

	env := NewTestEnvironmentWithOptions(t, TestEnvironmentOptions{
		Mutate: func(cfg *config.Config, _ string) {
			cfg.DataDir = dataDir
			off := false
			cfg.Telemetry = &config.TelemetryConfig{Enabled: &off}
		},
	})
	cleaned := false
	defer func() {
		if !cleaned {
			env.Cleanup()
		}
	}()

	rt := env.proxyServer.runtime
	require.NotNil(t, rt)
	_, seenBefore := rt.GetActivationFirstMCPClient()
	require.NotContains(t, seenBefore, "cursor")

	cursor := connect.FindClient("cursor")
	require.NotNil(t, cursor)
	require.NotEmpty(t, cursor.ClientInfoNames)
	alias := cursor.ClientInfoNames[0]

	tr, err := transport.NewStreamableHTTP(env.proxyAddr)
	require.NoError(t, err)
	mcpClient := client.NewClient(tr)
	defer mcpClient.Close()
	ctx := context.Background()
	require.NoError(t, mcpClient.Start(ctx))
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: alias, Version: "1.0"}
	_, err = mcpClient.Initialize(ctx, init)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		state, stateErr := rt.GetOnboardingState()
		return stateErr == nil && !state.ClientLastSeen[alias].IsZero()
	}, 5*time.Second, 50*time.Millisecond, "initialize must record client_last_seen without telemetry")

	_, seenAfter := rt.GetActivationFirstMCPClient()
	// The Spec 044 activation list keeps its own writer (RecordMCPClientForActivation)
	// and its []string shape; RecordClientSeen must not add a second entry to it.
	var cursorEntries int
	for _, name := range seenAfter {
		if name == "cursor" {
			cursorEntries++
		}
	}
	assert.Equal(t, 1, cursorEntries, "mcp_clients_seen_ever lists the client once and stays []string")

	_ = mcpClient.Close()
	env.Cleanup()
	cleaned = true

	reopened, err := storage.NewManager(dataDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	persisted, err := reopened.GetOnboardingState()
	require.NoError(t, err)
	assert.False(t, persisted.ClientLastSeen[alias].IsZero(), "client_last_seen survives a storage reopen")
}
