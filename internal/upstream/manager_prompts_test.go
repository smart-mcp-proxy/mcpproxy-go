package upstream

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func newTestPromptUpstreamServer(t *testing.T, promptName string) *mcpserver.MCPServer {
	t.Helper()
	srv := mcpserver.NewMCPServer("test-upstream", "0.0.1",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithPromptCapabilities(true),
	)
	srv.AddPrompt(
		mcp.NewPrompt(promptName, mcp.WithPromptDescription("test prompt")),
		func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{
				Messages: []mcp.PromptMessage{
					{Role: mcp.RoleAssistant, Content: mcp.TextContent{Type: "text", Text: "ok"}},
				},
			}, nil
		},
	)
	return srv
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{DataDir: tmpDir}
	storageManager, err := storage.NewBoltDB(tmpDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storageManager.Close() })

	m := NewManager(zap.NewNop(), cfg, storageManager, secret.NewResolver(), nil)
	t.Cleanup(func() { m.DisconnectAll() })
	return m
}

// addConnectedTestServer registers a real, connected upstream test server
// under the given id/name and returns its httptest.Server for cleanup.
func addConnectedTestServer(t *testing.T, m *Manager, id, name, promptName string) {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	upstream := newTestPromptUpstreamServer(t, promptName)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	t.Cleanup(testServer.Close)

	cfg := &config.ServerConfig{
		Name:     name,
		Protocol: "streamable-http",
		URL:      testServer.URL,
		Enabled:  true,
	}
	require.NoError(t, m.AddServerConfig(id, cfg))

	client, ok := m.GetClient(id)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))
}

func TestManager_ListPrompts_AggregatesAcrossServers(t *testing.T) {
	m := newTestManager(t)

	addConnectedTestServer(t, m, "srv-a", "server-a", "greeting")

	// Server B is configured but never connects (closed port) — should be
	// silently skipped, not fail the whole aggregation.
	require.NoError(t, m.AddServerConfig("srv-b", &config.ServerConfig{
		Name:     "server-b",
		Protocol: "streamable-http",
		URL:      "http://127.0.0.1:1",
		Enabled:  true,
	}))

	prompts, err := m.ListPrompts(context.Background())
	require.NoError(t, err)
	require.Len(t, prompts, 1)
	assert.Equal(t, "server-a:greeting", prompts[0].Name)
}

func TestManager_GetPrompt_ResolvesAndForwards(t *testing.T) {
	m := newTestManager(t)
	addConnectedTestServer(t, m, "srv-a", "server-a", "greeting")

	result, err := m.GetPrompt(context.Background(), "server-a:greeting", nil)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)
}

func TestManager_GetPrompt_MalformedName(t *testing.T) {
	m := newTestManager(t)

	_, err := m.GetPrompt(context.Background(), "no-colon-here", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid prompt name format")
}

func TestManager_GetPrompt_UnknownServer(t *testing.T) {
	m := newTestManager(t)

	_, err := m.GetPrompt(context.Background(), "nonexistent:greeting", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no client found for server")
}
