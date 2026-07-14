package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toonenc"
)

// Spec 084 E2E (SC-005, SC-006, US2-AC1..AC4, FR-001/FR-002): a live proxy
// with a fixture upstream returning a large uniform array. The operator flips
// toon_output via /api/v1/config/apply — off → adaptive → off — and each mode
// applies to the next tool call within one hot-reload cycle, no restart:
// adaptive responses carry the marker and beat the passthrough by the
// threshold; the rollback response is byte-identical to the pre-feature
// baseline.

// createToonFixtureUpstream starts a mock MCP upstream whose single tool
// list_rows returns the given payload verbatim as one text block.
func createToonFixtureUpstream(t *testing.T, env *TestEnvironment, name, payload string) *MockUpstreamServer {
	t.Helper()

	mcpSrv := mcpserver.NewMCPServer(name, "1.0.0-test", mcpserver.WithToolCapabilities(true))
	tool := mcp.Tool{
		Name:        "list_rows",
		Description: "Returns a large uniform JSON array (spec 084 fixture)",
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]interface{}{}},
	}
	mcpSrv.AddTool(tool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(payload), nil
	})

	streamable := mcpserver.NewStreamableHTTPServer(mcpSrv)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	httpServer := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: streamable}
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			env.logger.Error("toon fixture upstream error", zap.Error(err))
		}
	}()
	time.Sleep(200 * time.Millisecond)

	mock := &MockUpstreamServer{
		server:     mcpSrv,
		tools:      []mcp.Tool{tool},
		addr:       fmt.Sprintf("http://127.0.0.1:%d", port),
		httpServer: httpServer,
		stopFunc:   func() error { return httpServer.Shutdown(context.Background()) },
	}
	env.mockServers[name] = mock
	return mock
}

// callListRows invokes call_tool_read on the fixture tool through the proxy
// and returns the single text block. Retries transient failures (server
// reconnection after a config apply) until the deadline.
func callListRows(t *testing.T, mcpClient interface {
	CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
}, serverName string) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = contracts.ToolVariantRead
	req.Params.Arguments = map[string]interface{}{
		"name": serverName + ":list_rows",
		"args": map[string]interface{}{},
	}

	deadline := time.Now().Add(20 * time.Second)
	var lastErr string
	for {
		result, err := mcpClient.CallTool(context.Background(), req)
		if err == nil && result != nil && !result.IsError {
			require.NotEmpty(t, result.Content)
			tc, ok := result.Content[0].(mcp.TextContent)
			require.True(t, ok, "expected text content, got %T", result.Content[0])
			return tc.Text
		}
		if err != nil {
			lastErr = err.Error()
		} else if result != nil {
			if tc, ok := result.Content[0].(mcp.TextContent); ok {
				lastErr = tc.Text
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("call_tool_read %s:list_rows did not succeed before deadline; last error: %s", serverName, lastErr)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// applyToonMode fetches the live config over the REST API, sets the global
// toon_output, and POSTs it back to /api/v1/config/apply — the exact flow the
// Web UI / operator uses (SC-005: config edits alone, one hot-reload cycle).
func applyToonMode(t *testing.T, env *TestEnvironment, mode string) {
	t.Helper()
	applyToonConfig(t, env, func(cfg *config.Config) { cfg.ToonOutput = mode })
}

// applyToonServerOverride sets the per-server toon_output override for the
// named server via the same config-apply flow (US2-AC2: per-server > global).
// An empty mode removes the override (inherit the global again).
func applyToonServerOverride(t *testing.T, env *TestEnvironment, serverName, mode string) {
	t.Helper()
	applyToonConfig(t, env, func(cfg *config.Config) {
		for _, sc := range cfg.Servers {
			if sc.Name == serverName {
				sc.ToonOutput = mode
				return
			}
		}
		t.Fatalf("server %q not found in live config", serverName)
	})
}

// applyToonConfig fetches the live config, applies mutate, and POSTs it back
// to /api/v1/config/apply, asserting the change hot-reloads without restart.
func applyToonConfig(t *testing.T, env *TestEnvironment, mutate func(*config.Config)) {
	t.Helper()
	cfg, err := env.GetConfig()
	require.NoError(t, err)

	// The API config may omit boot-time fields; restore the ones apply
	// validation / hot-reload diffing need (same dance as the quarantine
	// config-apply E2E).
	if cfg.ToolsLimit == 0 {
		cfg.ToolsLimit = 15
	}
	if cfg.ToolResponseLimit == 0 {
		cfg.ToolResponseLimit = 10000
	}
	if cfg.CallToolTimeout == 0 {
		cfg.CallToolTimeout = config.Duration(60 * time.Second)
	}
	if cfg.DataDir == "" {
		cfg.DataDir = env.proxyServer.runtime.Config().DataDir
	}
	if cfg.Listen == "" {
		cfg.Listen = env.proxyServer.runtime.Config().Listen
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "test-api-key-e2e"
	}

	mutate(cfg)

	result, err := env.ApplyConfig(cfg)
	require.NoError(t, err)
	require.True(t, result.Success, "config apply must succeed: %+v", result)
	require.False(t, result.RequiresRestart,
		"a toon_output change must be hot-reloadable, got restart reason %q", result.RestartReason)
}

func TestE2E_ToonOutputModeFlip(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	if raceEnabled {
		t.Skip("Skipping test with race detector enabled - known race in shutdown path")
	}

	env := NewTestEnvironment(t)
	defer env.Cleanup()

	// A large uniform array — the payload class TOON wins on.
	payload := tabularJSON(150)
	require.Greater(t, len(payload), 4000, "fixture must be large enough for a decisive win")
	require.Less(t, len(payload), 10000, "fixture must stay under the response limit (truncation tested elsewhere)")

	mockServer := createToonFixtureUpstream(t, env, "toonserver", payload)

	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	ctx := context.Background()

	// Register the fixture upstream.
	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "toonserver",
		"url":       mockServer.addr,
		"protocol":  "streamable-http",
		"enabled":   true,
	}
	_, err := mcpClient.CallTool(ctx, addRequest)
	require.NoError(t, err)

	// Unquarantine and reconcile (standard E2E dance).
	serverConfig, err := env.proxyServer.runtime.StorageManager().GetUpstreamServer("toonserver")
	require.NoError(t, err)
	serverConfig.Quarantined = false
	require.NoError(t, env.proxyServer.runtime.StorageManager().SaveUpstreamServer(serverConfig))

	servers, err := env.proxyServer.runtime.StorageManager().ListUpstreamServers()
	require.NoError(t, err)
	cfg := env.proxyServer.runtime.Config()
	cfg.Servers = servers
	require.NoError(t, env.proxyServer.runtime.LoadConfiguredServers(cfg))
	time.Sleep(2 * time.Second)
	_ = env.proxyServer.runtime.DiscoverAndIndexTools(ctx)

	// Step 1 — default (off): the baseline is the exact upstream payload,
	// no marker (FR-002, US2-AC1).
	baseline := callListRows(t, mcpClient, "toonserver")
	require.Equal(t, payload, baseline, "off (default) must forward the payload byte-identically")
	require.NotContains(t, baseline, toonenc.Marker)

	// Step 2 — flip to adaptive via config apply: the NEXT call is
	// TOON-encoded with the marker and beats the passthrough by the
	// threshold (US1-AC1, SC-005, SC-006).
	applyToonMode(t, env, "adaptive")

	deadline := time.Now().Add(15 * time.Second)
	var encoded string
	for {
		encoded = callListRows(t, mcpClient, "toonserver")
		if strings.HasPrefix(encoded, toonenc.Marker+"\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("adaptive mode did not apply within the hot-reload window; got %d bytes without marker: %.120q",
				len(encoded), encoded)
		}
		time.Sleep(500 * time.Millisecond)
	}
	assert.LessOrEqual(t, len(encoded), len(baseline)*(100-15)/100,
		"encoded emission (%d bytes) must beat the passthrough (%d bytes) by the 15%% threshold",
		len(encoded), len(baseline))

	// The TOON body round-trips to the original rows (the decode hint is
	// honest): sanity-check a couple of row fragments survived encoding.
	assert.Contains(t, encoded, "row-0")
	assert.Contains(t, encoded, "row-149")

	// Step 2b — per-server override wins over the global (US2-AC2): with
	// global adaptive still on, a per-server "off" makes THIS server pass
	// through byte-identically on the next hot-reload cycle.
	applyToonServerOverride(t, env, "toonserver", "off")

	deadline = time.Now().Add(15 * time.Second)
	var overridden string
	for {
		overridden = callListRows(t, mcpClient, "toonserver")
		if !strings.Contains(overridden, toonenc.Marker) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("per-server off override did not apply within the hot-reload window")
		}
		time.Sleep(500 * time.Millisecond)
	}
	assert.Equal(t, baseline, overridden,
		"per-server off must restore byte-identical passthrough while global stays adaptive")

	// Step 2c — removing the override re-inherits the global adaptive mode,
	// proving the passthrough above came from the override, not a global flip.
	applyToonServerOverride(t, env, "toonserver", "")

	deadline = time.Now().Add(15 * time.Second)
	for {
		reEncoded := callListRows(t, mcpClient, "toonserver")
		if strings.HasPrefix(reEncoded, toonenc.Marker+"\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("removing the per-server override did not restore adaptive encoding")
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Step 3 — roll back to off via config apply: the next response is
	// byte-identical to the pre-feature baseline (SC-002, US2-AC1 rollback).
	applyToonMode(t, env, "off")

	deadline = time.Now().Add(15 * time.Second)
	var reverted string
	for {
		reverted = callListRows(t, mcpClient, "toonserver")
		if !strings.Contains(reverted, toonenc.Marker) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("off rollback did not apply within the hot-reload window")
		}
		time.Sleep(500 * time.Millisecond)
	}
	assert.Equal(t, baseline, reverted, "rollback must restore byte-identical passthrough")

	// The applied modes are visible in the live config (operator surface).
	liveCfg, err := env.GetConfig()
	require.NoError(t, err)
	assert.Equal(t, "off", liveCfg.ToonOutput)
}
