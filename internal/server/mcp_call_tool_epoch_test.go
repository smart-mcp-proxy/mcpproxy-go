package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Spec 105 FR-009 "stale generation" (research D4; codex r3 D2), dispatch
// coupling. liveIdentityRefusal certifies a name on ONE connection generation
// (the live client's ConnectionEpoch, which the discovery stamp must match),
// but the dispatch that follows used to reach the upstream through an
// unpinned CallTool: with admission control the already-authorized call can
// sit queued for seconds while the connection drops and comes back, and the
// call then executes on generation B — before B's own discovery has run —
// with a name, tier and approval hash certified on generation A. The fix
// threads the certified epoch into the managed client (CallToolOnEpoch),
// which re-checks it after the queue wait and immediately before the
// transport; every path that consumes liveIdentityRefusal maps the refusal
// onto the discovery-window body it would have answered pre-dispatch.

// holdAdmissionSlot installs a one-slot admission limiter on the server's
// live client and takes the slot, so the next dispatch has to queue. It
// returns the registry (to observe the queue) and the release closure.
func holdAdmissionSlot(t *testing.T, proxy *MCPProxyServer, server string) (*limiter.Registry, func()) {
	t.Helper()
	client, ok := proxy.upstreamManager.GetClient(server)
	require.True(t, ok)
	reg := limiter.NewRegistry()
	reg.Apply(limiter.Limits{}, map[string]limiter.Limits{
		server: {Max: 1, QueueSize: 4, QueueTimeout: 30 * time.Second},
	})
	client.SetAdmissionControl(reg, nil)
	release, err := reg.Acquire(context.Background(), server)
	require.NoError(t, err)
	return reg, release
}

// bounceConnection drops the live client and reconnects it to the still-alive
// stub: two epoch bumps, a NEW generation that is Ready. The StateView stamp
// is left as it was — exactly the window in which the supervisor has not yet
// re-stamped the fresh connection.
func bounceConnection(t *testing.T, proxy *MCPProxyServer, server string) {
	t.Helper()
	client, ok := proxy.upstreamManager.GetClient(server)
	require.True(t, ok)
	before := client.ConnectionEpoch()
	require.NoError(t, client.Disconnect())
	require.NoError(t, proxy.upstreamManager.ConnectAll(context.Background()))
	require.Eventually(t, client.IsConnected, 10*time.Second, 50*time.Millisecond, "fixture: the stub must reconnect")
	require.NotEqual(t, before, client.ConnectionEpoch(), "fixture: the generation must have moved")
}

func seedEpochFixture(t *testing.T) (*MCPProxyServer, *runtime.Runtime, *countingUpstream) {
	t.Helper()
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
	proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
	up := startCountingUpstream(t, proxy, rt, "a", readSpec("erase"))
	require.False(t, proxy.resolveExactToolIdentity("a", "erase").Unresolved(), "fixture: erase is certified on the live connection")
	return proxy, rt, up
}

// TestCallToolRead_GenerationChangesWhileQueued_RefusedWithZeroUpstreamCalls
// is the retrieve-path reproduction.
func TestCallToolRead_GenerationChangesWhileQueued_RefusedWithZeroUpstreamCalls(t *testing.T) {
	for label, ctx := range map[string]context.Context{
		"full-tier a-only token": fullTierAgentOn("a"),
		"api-key admin":          adminCtx(),
	} {
		t.Run(label, func(t *testing.T) {
			proxy, _, up := seedEpochFixture(t)
			reg, release := holdAdmissionSlot(t, proxy, "a")

			type outcome struct {
				text string
			}
			done := make(chan outcome, 1)
			go func() {
				_, text := callToolReadResult(t, proxy, ctx, "a:erase")
				done <- outcome{text: text}
			}()
			require.Eventually(t, func() bool {
				return reg.Server("a").Stats().Queued == 1
			}, 5*time.Second, 5*time.Millisecond, "the certified call must be parked in the admission queue")

			bounceConnection(t, proxy, "a")
			release()

			var out outcome
			select {
			case out = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the queued call never returned")
			}
			assert.Contains(t, out.text, unresolvedToolIdentityMessage("a", "erase", false),
				"a call certified on the previous generation must be refused with the discovery-window body (got %s)", out.text)
			assert.Equal(t, int64(0), up.count.Load(), "the call must never reach the reconnected upstream")

			// Once the fresh generation is stamped, the same name dispatches.
			rt := proxy.mainServer.runtime
			stampDiscoveredOnLiveConnection(t, proxy, rt, "a", up.Tools)
			_, text := callToolReadResult(t, proxy, ctx, "a:erase")
			assert.NotContains(t, text, "Permission denied", "control: re-certified on the new generation (got %s)", text)
			assert.Equal(t, int64(1), up.count.Load())
		})
	}
}

// TestCodeExecution_GenerationChangesWhileQueued_RefusedWithZeroUpstreamCalls
// is the nested-path twin: the sandbox bridge dispatches through the managed
// client directly, so it pins the same epoch.
func TestCodeExecution_GenerationChangesWhileQueued_RefusedWithZeroUpstreamCalls(t *testing.T) {
	proxy, rt, up := seedEpochFixture(t)
	reg, release := holdAdmissionSlot(t, proxy, "a")

	done := make(chan sandboxCall, 1)
	go func() {
		done <- runSandboxCallTool(t, proxy, adminCtx(), "a", "erase")
	}()
	require.Eventually(t, func() bool {
		return reg.Server("a").Stats().Queued == 1
	}, 5*time.Second, 5*time.Millisecond, "the certified call must be parked in the admission queue")

	bounceConnection(t, proxy, "a")
	release()

	var call sandboxCall
	select {
	case call = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the queued call never returned")
	}
	assert.False(t, call.OK)
	assert.Contains(t, call.Message, "cannot be resolved because tool discovery has not completed",
		"the bridge must answer with the discovery-window body (got %q: %s)", call.Code, call.Message)
	assert.Equal(t, int64(0), up.count.Load(), "the call must never reach the reconnected upstream")

	stampDiscoveredOnLiveConnection(t, proxy, rt, "a", up.Tools)
	ctl := runSandboxCallTool(t, proxy, adminCtx(), "a", "erase")
	assert.True(t, ctl.OK, "control: re-certified on the new generation (got %q: %s)", ctl.Code, ctl.Message)
	assert.Equal(t, int64(1), up.count.Load())
}

// TestDirectMode_GenerationChangesWhileQueued_RefusedWithZeroUpstreamCalls
// covers the /mcp/all handler, which pins the generation it observed the
// live client on at its callability gate.
func TestDirectMode_GenerationChangesWhileQueued_RefusedWithZeroUpstreamCalls(t *testing.T) {
	proxy, rt, up := seedEpochFixture(t)
	reg, release := holdAdmissionSlot(t, proxy, "a")
	entry := &directCatalogEntry{
		ServerName:  "a",
		ToolName:    "erase",
		DisplayName: FormatDirectToolName("a", "erase"),
		ParamsJSON:  `{"type":"object"}`,
		Annotations: readSpec("erase").Annotations,
	}
	call := func() string {
		req := mcp.CallToolRequest{}
		req.Params.Name = entry.DisplayName
		req.Params.Arguments = map[string]interface{}{}
		result, err := proxy.makeDirectModeHandler(entry)(adminCtx(), req)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Content)
		return result.Content[0].(mcp.TextContent).Text
	}

	done := make(chan string, 1)
	go func() { done <- call() }()
	require.Eventually(t, func() bool {
		return reg.Server("a").Stats().Queued == 1
	}, 5*time.Second, 5*time.Millisecond, "the call must be parked in the admission queue")

	bounceConnection(t, proxy, "a")
	release()

	var text string
	select {
	case text = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the queued call never returned")
	}
	assert.Contains(t, text, unresolvedToolIdentityMessage("a", "erase", false),
		"a direct call pinned to the previous generation must be refused with the discovery-window body (got %s)", text)
	assert.Equal(t, int64(0), up.count.Load(), "the call must never reach the reconnected upstream")

	stampDiscoveredOnLiveConnection(t, proxy, rt, "a", up.Tools)
	text = call()
	assert.NotContains(t, text, "Permission denied", "control: the new generation dispatches (got %s)", text)
	assert.Equal(t, int64(1), up.count.Load())
}

// TestManagerCallToolOnEpoch_NeverReconnectsOnUse pins the manager seam: a
// pinned dispatch on a client that is Disconnected with reconnect_on_use
// enabled is refused with the generation verdict and does NOT reconnect —
// the unpinned CallTool on the same client still does (the pre-105
// reconnect_on_use behaviour direct mode relies on when it finds the client
// disconnected at its gate).
func TestManagerCallToolOnEpoch_NeverReconnectsOnUse(t *testing.T) {
	serverCfg := &config.ServerConfig{Name: "a", Enabled: true, ReconnectOnUse: true}
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{serverCfg})
	up := startCountingUpstream(t, proxy, rt, "a", readSpec("erase"))
	client, ok := proxy.upstreamManager.GetClient("a")
	require.True(t, ok)
	// startCountingUpstream wires the stub under its own record; flip
	// reconnect_on_use on the LIVE client's config, which is what
	// Manager.CallTool's reconnect branch reads.
	liveCfg := *client.GetConfig()
	liveCfg.ReconnectOnUse = true
	client.SetConfig(&liveCfg)
	require.True(t, client.GetConfig().ReconnectOnUse, "fixture: reconnect_on_use is on")
	certified := client.ConnectionEpoch()

	require.NoError(t, client.Disconnect())
	require.False(t, client.IsConnected())
	rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
		s.Connected = false
		s.Tools = nil
		s.ToolsDiscovered = false
	})

	_, err := proxy.upstreamManager.CallToolOnEpoch(context.Background(), "a:erase", nil, certified)
	require.Error(t, err)
	assert.True(t, errors.Is(err, managed.ErrConnectionGenerationChanged), "got %v", err)
	assert.False(t, client.IsConnected(), "a pinned dispatch must never reconnect inside the call")
	assert.Equal(t, int64(0), up.count.Load())

	// Control: the unpinned entry point keeps reconnect_on_use.
	_, err = proxy.upstreamManager.CallTool(context.Background(), "a:erase", nil)
	require.NoError(t, err)
	assert.True(t, client.IsConnected(), "control: CallTool reconnects on use")
	assert.Equal(t, int64(1), up.count.Load())
}
