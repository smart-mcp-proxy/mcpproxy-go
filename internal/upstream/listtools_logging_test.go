package upstream

import (
	"context"
	"strings"
	"testing"
	"time"

	servertest "github.com/mark3labs/mcp-go/server/servertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// newObservedManager is newTestManager with a logger whose entries the test can
// inspect, at Debug so the downgraded sites are visible rather than filtered.
func newObservedManager(t *testing.T) (*Manager, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	tmpDir := t.TempDir()
	cfg := &config.Config{DataDir: tmpDir}
	storageManager, err := storage.NewBoltDB(tmpDir, zap.NewNop().Sugar())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storageManager.Close() })

	m := NewManager(zap.New(core), cfg, storageManager, secret.NewResolver(), nil)
	t.Cleanup(func() { m.DisconnectAll() })
	return m, logs
}

// One ListTools failure used to be logged at ERROR three times — once each by
// core.Client, managed.Client and Manager.discoverTools. On an install with
// several stdio servers the periodic sweep hit this roughly once a minute per
// server, forever, which is what rotated main.log every couple of hours.
//
// The sweep deliberately does not markSwept on failure so the next cycle
// retries; a failure the code already plans to retry is a Warn at most, and it
// belongs to exactly one layer.
func TestListToolsFailureIsLoggedOnceAndNotAsError(t *testing.T) {
	m, logs := newObservedManager(t)
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	upstream := newTestPromptUpstreamServer(t, "greeting")
	testServer := servertest.NewTestStreamableHTTPServer(upstream)

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

	// Kill the upstream out from under a client that still believes it is
	// connected — the exact shape of the transport-closed sweep miss.
	testServer.Close()
	logs.TakeAll()

	_, _ = m.DiscoverTools(context.Background())

	// The three layers that each reported the SAME failed list operation. A
	// state-transition log ("Connection error detected during ListTools,
	// updating server state") is a different fact — it fires once when the
	// client gives up on the connection — and is deliberately not counted here.
	reportsTheListFailure := []string{
		"Failed to list tools via direct call to upstream server", // core.Client
		"ListTools operation failed",                              // managed.Client
		"Failed to list tools from client",                        // Manager.discoverTools
	}

	var elevated []string
	for _, e := range logs.All() {
		if e.Level < zapcore.WarnLevel {
			continue
		}
		for _, msg := range reportsTheListFailure {
			if e.Message == msg {
				elevated = append(elevated, e.Level.String()+": "+e.Message)
			}
		}
	}

	assert.Len(t, elevated, 1,
		"one failed ListTools must be reported by exactly one layer, got: %v", elevated)
	for _, entry := range elevated {
		assert.False(t, strings.HasPrefix(entry, "error:"),
			"a sweep failure the manager retries next cycle must not be ERROR, got: %v", elevated)
	}
}

// The failure must still be visible: silencing all three layers would trade a
// log storm for an invisible outage.
func TestListToolsFailureIsStillReported(t *testing.T) {
	m, logs := newObservedManager(t)
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	upstream := newTestPromptUpstreamServer(t, "greeting")
	testServer := servertest.NewTestStreamableHTTPServer(upstream)

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

	testServer.Close()
	logs.TakeAll()

	_, _ = m.DiscoverTools(context.Background())

	found := false
	for _, e := range logs.All() {
		if e.Level >= zapcore.WarnLevel && strings.Contains(e.Message, "Failed to list tools from client") {
			found = true
			assert.Equal(t, zapcore.WarnLevel, e.Level, "the surviving log should be Warn")
		}
	}
	assert.True(t, found, "the sweep miss must still be reported at Warn by the manager")
}

// Spec 105 FR-009 (round-3 finding 5): the sweep must be able to tell a server
// whose tools/list SUCCEEDED with zero tools (discovery completed; its names
// resolve as absent) from one whose list FAILED (discovery did not complete;
// retried next cycle). DiscoverToolsReport names the former in its listed set
// and leaves the latter out.
func TestDiscoverToolsReport_ListsZeroToolServersButNotFailedOnes(t *testing.T) {
	m, _ := newObservedManager(t)
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")

	// "server-a": a prompt-only upstream — tools/list succeeds with no tools.
	okServer := servertest.NewTestStreamableHTTPServer(newTestPromptUpstreamServer(t, "greeting"))
	t.Cleanup(okServer.Close)
	require.NoError(t, m.AddServerConfig("srv-a", &config.ServerConfig{
		Name: "server-a", Protocol: "streamable-http", URL: okServer.URL, Enabled: true,
	}))
	// "server-b": connected, then its upstream dies — tools/list fails.
	deadServer := servertest.NewTestStreamableHTTPServer(newTestPromptUpstreamServer(t, "greeting"))
	require.NoError(t, m.AddServerConfig("srv-b", &config.ServerConfig{
		Name: "server-b", Protocol: "streamable-http", URL: deadServer.URL, Enabled: true,
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, id := range []string{"srv-a", "srv-b"} {
		client, ok := m.GetClient(id)
		require.True(t, ok)
		require.NoError(t, client.Connect(ctx))
	}
	deadServer.Close()

	tools, listed, err := m.DiscoverToolsReport(context.Background(), false)
	require.NoError(t, err)
	assert.Empty(t, tools, "neither upstream serves a tool")
	assert.Equal(t, []string{"server-a"}, listed,
		"only the server whose tools/list completed is reported as listed")

	// The plain wrappers keep their signature and result.
	plain, err := m.DiscoverTools(context.Background())
	require.NoError(t, err)
	assert.Empty(t, plain)
}
