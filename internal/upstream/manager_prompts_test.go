package upstream

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

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
	addConnectedTestServerWithConfig(t, m, id, &config.ServerConfig{
		Name:    name,
		Enabled: true,
	}, promptName)
}

// addConnectedTestServerWithConfig is addConnectedTestServer with full
// control over the ServerConfig (e.g. Enabled/Quarantined), so tests can
// exercise Manager.ListPrompts's per-client skip conditions against a
// genuinely connected client rather than an absent/never-connected one.
func addConnectedTestServerWithConfig(t *testing.T, m *Manager, id string, cfg *config.ServerConfig, promptName string) *httptest.Server {
	t.Helper()
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	upstream := newTestPromptUpstreamServer(t, promptName)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	t.Cleanup(testServer.Close)

	cfg.Protocol = "streamable-http"
	cfg.URL = testServer.URL
	require.NoError(t, m.AddServerConfig(id, cfg))

	client, ok := m.GetClient(id)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))

	return testServer
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

func TestManager_ListPrompts_SkipsQuarantinedServer(t *testing.T) {
	m := newTestManager(t)

	addConnectedTestServerWithConfig(t, m, "srv-a", &config.ServerConfig{
		Name:        "server-a",
		Enabled:     true,
		Quarantined: true,
	}, "greeting")

	prompts, err := m.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, prompts, "a quarantined server's prompts must not be aggregated")
}

func TestManager_ListPrompts_SkipsDisabledServer(t *testing.T) {
	m := newTestManager(t)

	addConnectedTestServerWithConfig(t, m, "srv-a", &config.ServerConfig{
		Name:    "server-a",
		Enabled: false,
	}, "greeting")

	prompts, err := m.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, prompts, "a disabled server's prompts must not be aggregated")
}

func TestManager_ListPrompts_LogsAndSkipsClientError(t *testing.T) {
	m := newTestManager(t)

	testServer := addConnectedTestServerWithConfig(t, m, "srv-a", &config.ServerConfig{
		Name:    "server-a",
		Enabled: true,
	}, "greeting")

	// Close the upstream out from under an already-connected client so the
	// client's ListPrompts fails; the manager must log and skip it rather
	// than fail the whole aggregation.
	testServer.Close()

	prompts, err := m.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, prompts)
}

// TestManager_GetPrompt_RejectsQuarantinedServer covers the defense-in-depth
// guard added in PR #973 review (item 5): ListPrompts already excludes
// quarantined servers from aggregation, but GetPrompt itself must also
// refuse a call routed to an already-known "server:prompt" name, closing
// the race window between a quarantine flip and the next
// servers.changed-driven refresh.
func TestManager_GetPrompt_RejectsQuarantinedServer(t *testing.T) {
	m := newTestManager(t)

	addConnectedTestServerWithConfig(t, m, "srv-a", &config.ServerConfig{
		Name:        "server-a",
		Enabled:     true,
		Quarantined: true,
	}, "greeting")

	_, err := m.GetPrompt(context.Background(), "server-a:greeting", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quarantined")
}

// TestManager_GetPrompt_RejectsDisabledServer mirrors the Enabled guard
// Manager.CallTool enforces (manager.go:1220), which GetPrompt lacked
// before this fix.
func TestManager_GetPrompt_RejectsDisabledServer(t *testing.T) {
	m := newTestManager(t)

	addConnectedTestServerWithConfig(t, m, "srv-a", &config.ServerConfig{
		Name:    "server-a",
		Enabled: false,
	}, "greeting")

	_, err := m.GetPrompt(context.Background(), "server-a:greeting", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

// --- Finding F12: size & count caps ---

func TestTruncatePromptText(t *testing.T) {
	long := strings.Repeat("a", maxPromptResultChars+500)
	multibyte := strings.Repeat("€", 100) // 300 bytes, 3-byte runes

	tests := []struct {
		name     string
		text     string
		limit    int
		wantCut  bool
		wantMark bool
	}{
		{"under limit", "small prompt", maxPromptResultChars, false, false},
		{"exactly at limit", strings.Repeat("a", 10), 10, false, false},
		{"over limit ascii", long, maxPromptResultChars, true, true},
		{"limit zero disables", long, 0, false, false},
		{"negative disables", long, -1, false, false},
		{"multibyte no split", multibyte, 250, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, cut := truncatePromptText(tt.text, tt.limit)
			assert.Equal(t, tt.wantCut, cut)
			assert.True(t, utf8.ValidString(got), "result must be valid UTF-8")
			assert.Equal(t, tt.wantMark, strings.HasSuffix(got, promptTruncationMarker))
			if cut {
				assert.LessOrEqual(t, len(got)-len(promptTruncationMarker), tt.limit)
			}
		})
	}
}

func TestCapPromptResultSize(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	result := &mcp.GetPromptResult{
		Messages: []mcp.PromptMessage{
			{Role: mcp.RoleUser, Content: mcp.TextContent{Type: "text", Text: strings.Repeat("x", maxPromptResultChars+100)}},
			{Role: mcp.RoleAssistant, Content: mcp.TextContent{Type: "text", Text: "short"}},
			{Role: mcp.RoleAssistant, Content: mcp.ImageContent{Type: "image", MIMEType: "image/png", Data: "AAAA"}},
		},
	}

	capPromptResultSize(result, maxPromptResultChars, logger, "server-a", "greeting")

	first := result.Messages[0].Content.(mcp.TextContent)
	assert.True(t, strings.HasSuffix(first.Text, promptTruncationMarker))
	assert.LessOrEqual(t, len(first.Text)-len(promptTruncationMarker), maxPromptResultChars)
	assert.Equal(t, "short", result.Messages[1].Content.(mcp.TextContent).Text)
	assert.IsType(t, mcp.ImageContent{}, result.Messages[2].Content)

	require.Equal(t, 1, logs.FilterMessage("upstream prompt result exceeded size cap; message text truncated").Len())
}

func TestCapPromptResultSize_NilSafe(t *testing.T) {
	capPromptResultSize(nil, maxPromptResultChars, zap.NewNop(), "s", "p") // must not panic
}

// newTestPromptUpstreamServerN registers `count` distinct prompts so tests can
// exercise the per-server / total count caps.
func newTestPromptUpstreamServerN(t *testing.T, count int) *mcpserver.MCPServer {
	t.Helper()
	srv := mcpserver.NewMCPServer("test-upstream", "0.0.1",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithPromptCapabilities(true),
	)
	for i := 0; i < count; i++ {
		srv.AddPrompt(
			mcp.NewPrompt(fmt.Sprintf("p%d", i), mcp.WithPromptDescription("test prompt")),
			func(_ context.Context, _ mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{
					Messages: []mcp.PromptMessage{{Role: mcp.RoleAssistant, Content: mcp.TextContent{Type: "text", Text: "ok"}}},
				}, nil
			},
		)
	}
	return srv
}

func TestManager_ListPrompts_PerServerCap(t *testing.T) {
	m := newTestManager(t)
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	upstream := newTestPromptUpstreamServerN(t, maxPromptsPerServer+5)
	testServer := mcpserver.NewTestStreamableHTTPServer(upstream)
	t.Cleanup(testServer.Close)

	require.NoError(t, m.AddServerConfig("srv-a", &config.ServerConfig{
		Name:     "server-a",
		Protocol: "streamable-http",
		URL:      testServer.URL,
		Enabled:  true,
	}))
	client, ok := m.GetClient("srv-a")
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Connect(ctx))

	prompts, err := m.ListPrompts(context.Background())
	require.NoError(t, err)
	assert.Len(t, prompts, maxPromptsPerServer, "per-server prompt count must be capped")
}

// TestManager_GetPrompt_ReconnectsOnUse is the F15 regression: a disconnected
// server with reconnect_on_use:true must recover for prompts/get, exactly as
// CallTool recovers it for tool calls. Before the fix GetPrompt had no reconnect
// path and this returned a "not connected"-class failure.
func TestManager_GetPrompt_ReconnectsOnUse(t *testing.T) {
	m := newTestManager(t)
	addConnectedTestServerWithConfig(t, m, "srv-a", &config.ServerConfig{
		Name:           "server-a",
		Enabled:        true,
		ReconnectOnUse: true,
	}, "greeting")

	client, ok := m.GetClient("srv-a")
	require.True(t, ok)

	// Drop the client into an error state as if the transport died. The live
	// upstream (kept open by t.Cleanup) is still reachable, so reconnect_on_use
	// must recover it.
	client.StateManager.SetError(fmt.Errorf("connection lost"))
	require.False(t, client.IsConnected())

	result, err := m.GetPrompt(context.Background(), "server-a:greeting", nil)
	require.NoError(t, err)
	require.Len(t, result.Messages, 1)
	assert.True(t, client.IsConnected(), "reconnect_on_use should have reconnected the client for prompts/get")
}

// TestManager_GetPrompt_NoReconnectWhenDisabledFlag mirrors CallTool's default:
// with reconnect_on_use unset, a disconnected server fails prompts/get instead
// of silently reconnecting.
func TestManager_GetPrompt_NoReconnectWhenDisabledFlag(t *testing.T) {
	m := newTestManager(t)
	addConnectedTestServerWithConfig(t, m, "srv-a", &config.ServerConfig{
		Name:    "server-a",
		Enabled: true,
		// ReconnectOnUse defaults to false
	}, "greeting")

	client, ok := m.GetClient("srv-a")
	require.True(t, ok)
	client.StateManager.SetError(fmt.Errorf("connection lost"))

	_, err := m.GetPrompt(context.Background(), "server-a:greeting", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not connected")
	assert.False(t, client.IsConnected(), "no reconnect should have been attempted")
}
