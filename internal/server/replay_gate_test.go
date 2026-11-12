package server

// replay_gate_test.go — REST replay answers to the shared per-tool gate
// (evaluateExactToolGate) and to the live identity check, for every caller,
// and its dispatch is pinned to the certified connection generation.
// Before this, Server.ReplayToolCall authorized a scoped token against
// EffectiveAnnotations (which can describe a previous connection) and
// dispatched an unpinned call, and it never consulted tool-level quarantine,
// enabled_tools / disabled_tools, server quarantine or the review-dirty
// verdict: a held tool was dispatchable through POST /tool-calls/{id}/replay.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// replayGateFixture is one connected counting upstream "github" exposing spec,
// with a replayable record for it.
func replayGateFixture(t *testing.T, spec toolSpec) (*Server, *MCPProxyServer, *runtime.Runtime, *countingUpstream, string) {
	t.Helper()
	proxy, rt := createTestProxyWithRuntime(t, nil)
	mainSrv := &Server{runtime: rt, mcpProxy: proxy}
	up := startCountingUpstream(t, proxy, rt, "github", spec)
	callID := seedReplayableCallWithAnnotations(t, proxy, mainSrv, "github", spec.Name, up.URL, spec.Annotations)
	return mainSrv, proxy, rt, up, callID
}

func replayCallers() map[string]context.Context {
	return map[string]context.Context{
		"admin":      auth.WithAuthContext(context.Background(), auth.AdminContext()),
		"full token": replayTokenCtx(auth.PermRead, auth.PermWrite, auth.PermDestructive),
	}
}

// A tool the shared gate holds (pending or changed approval) must not be
// replayable by anyone, administrators included.
func TestReplayToolCall_RefusesToolLevelQuarantine(t *testing.T) {
	for _, status := range []string{"pending", "changed"} {
		for label, ctx := range replayCallers() {
			t.Run(status+"/"+label, func(t *testing.T) {
				spec := writeSpec("create_issue")
				spec.Approval = status
				mainSrv, _, _, up, callID := replayGateFixture(t, spec)

				_, err := mainSrv.ReplayToolCall(ctx, callID, map[string]interface{}{"q": "x"})
				var refusal *profile.ToolBlockedError
				require.ErrorAs(t, err, &refusal)
				assert.Equal(t, blockReasonToolQuarantined, refusal.Reason)
				assert.Contains(t, refusal.Message, "TOOL_QUARANTINED")
				require.Empty(t, up.dispatched(), "a held tool must never reach the upstream through replay")
			})
		}
	}
}

// enabled_tools / disabled_tools is operator policy: replay must honour it.
func TestReplayToolCall_RefusesConfigDeniedTool(t *testing.T) {
	for label, ctx := range replayCallers() {
		t.Run(label, func(t *testing.T) {
			spec := writeSpec("create_issue")
			// enabled_tools / disabled_tools is read from the live runtime config.
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{
				{Name: "github", Enabled: true, DisabledTools: []string{"create_issue"}},
			})
			mainSrv := &Server{runtime: rt, mcpProxy: proxy}
			up := startCountingUpstream(t, proxy, rt, "github", spec)
			callID := seedReplayableCallWithAnnotations(t, proxy, mainSrv, "github", spec.Name, up.URL, spec.Annotations)

			_, err := mainSrv.ReplayToolCall(ctx, callID, nil)
			var refusal *profile.ToolBlockedError
			require.ErrorAs(t, err, &refusal)
			assert.Equal(t, replayBlockReasonToolNotCallable, refusal.Reason)
			assert.Contains(t, refusal.Message, "TOOL_BLOCKED")
			require.Empty(t, up.dispatched(), "a config-denied tool must never reach the upstream through replay")
		})
	}
}

// A quarantined server's tools are not callable either.
func TestReplayToolCall_RefusesQuarantinedServer(t *testing.T) {
	spec := writeSpec("create_issue")
	proxy, rt := createTestProxyWithRuntime(t, nil)
	mainSrv := &Server{runtime: rt, mcpProxy: proxy}
	up := startCountingUpstream(t, proxy, rt, "github", spec)
	callID := seedReplayableCallWithAnnotations(t, proxy, mainSrv, "github", spec.Name, up.URL, spec.Annotations)
	// Quarantine after the fixture connected (a quarantined server is not
	// connected at all): the persisted record is what the gate reads.
	record, err := proxy.storage.GetUpstreamServer("github")
	require.NoError(t, err)
	record.Quarantined = true
	require.NoError(t, proxy.storage.SaveUpstreamServer(record))

	_, err = mainSrv.ReplayToolCall(adminCtx(), callID, nil)
	var refusal *profile.ToolBlockedError
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, blockReasonServerQuarantined, refusal.Reason)
	require.Empty(t, up.dispatched())
}

// A tool whose identity is not certified on the live connection (a reconnect
// the supervisor has not re-stamped: the discovery window) is refused for a
// read-only token AND for an administrator, with zero upstream calls. The
// previous generation's annotations must not authorize the replay.
func TestReplayToolCall_RefusesUncertifiedIdentityAfterReconnect(t *testing.T) {
	for label, ctx := range map[string]context.Context{
		"read-only token": replayTokenCtx(auth.PermRead),
		"admin":           auth.WithAuthContext(context.Background(), auth.AdminContext()),
	} {
		t.Run(label, func(t *testing.T) {
			// The previous generation listed a read-only tool.
			mainSrv, proxy, rt, up, callID := replayGateFixture(t, readSpec("create_issue"))

			bounceConnection(t, proxy, "github")

			_, err := mainSrv.ReplayToolCall(ctx, callID, nil)
			var refusal *profile.ToolBlockedError
			require.ErrorAs(t, err, &refusal)
			assert.Equal(t, replayBlockReasonToolNotCallable, refusal.Reason)
			assert.Contains(t, refusal.Message, unresolvedToolIdentityMessage("github", "create_issue", false))
			require.Empty(t, up.dispatched(), "an uncertified identity must not reach the upstream")

			// Control: once the fresh generation is stamped, the same replay dispatches.
			client, ok := proxy.upstreamManager.GetClient("github")
			require.True(t, ok)
			stampDiscoveredOnLiveConnection(t, proxy, rt, "github", up.Tools)
			require.NotZero(t, client.ConnectionEpoch())
			_, err = mainSrv.ReplayToolCall(ctx, callID, nil)
			require.NoError(t, err)
			require.Equal(t, []string{"create_issue"}, up.dispatched())
		})
	}
}

// The dispatch is pinned to the generation the gate certified: a reconnect
// that lands while the replay waits for admission (after the identity check,
// before the transport) is refused by the managed client with zero upstream
// calls, for a scoped token and an administrator alike.
func TestReplayToolCall_GenerationChangesWhileQueued_RefusedWithZeroUpstreamCalls(t *testing.T) {
	for label, ctx := range map[string]context.Context{
		"read-only token": replayTokenCtx(auth.PermRead),
		"admin":           auth.WithAuthContext(context.Background(), auth.AdminContext()),
	} {
		t.Run(label, func(t *testing.T) {
			mainSrv, proxy, _, up, callID := replayGateFixture(t, readSpec("create_issue"))
			reg, release := holdAdmissionSlot(t, proxy, "github")

			done := make(chan error, 1)
			go func() {
				_, err := mainSrv.ReplayToolCall(ctx, callID, nil)
				done <- err
			}()
			require.Eventually(t, func() bool {
				return reg.Server("github").Stats().Queued == 1
			}, 5*time.Second, 5*time.Millisecond, "the certified replay must be parked in the admission queue")

			bounceConnection(t, proxy, "github")
			release()

			select {
			case err := <-done:
				var refusal *profile.ToolBlockedError
				require.ErrorAs(t, err, &refusal)
				assert.Equal(t, replayBlockReasonToolNotCallable, refusal.Reason)
				assert.Contains(t, refusal.Message, unresolvedToolIdentityMessage("github", "create_issue", false))
			case <-time.After(10 * time.Second):
				t.Fatal("the queued replay never returned")
			}
			require.Empty(t, up.dispatched(), "the replay must never reach the reconnected upstream")
		})
	}
}

// Replay needs a CERTIFIED live identity to dispatch. A client that dropped
// between the gate and the live check must be refused (it used to "stand
// aside" and dispatch unpinned), with zero upstream calls.
func TestReplayToolCall_ClientDisconnectedAtLiveCheck_Refused(t *testing.T) {
	for label, ctx := range map[string]context.Context{
		"read-only token": replayTokenCtx(auth.PermRead),
		"admin":           auth.WithAuthContext(context.Background(), auth.AdminContext()),
	} {
		t.Run(label, func(t *testing.T) {
			mainSrv, proxy, _, up, callID := replayGateFixture(t, readSpec("create_issue"))
			proxy.replayAfterGate = func() {
				client, ok := proxy.upstreamManager.GetClient("github")
				require.True(t, ok)
				require.NoError(t, client.Disconnect())
			}
			t.Cleanup(func() { proxy.replayAfterGate = nil })

			_, err := mainSrv.ReplayToolCall(ctx, callID, nil)
			var refusal *profile.ToolBlockedError
			require.ErrorAs(t, err, &refusal)
			assert.Equal(t, replayBlockReasonToolNotCallable, refusal.Reason)
			assert.Contains(t, refusal.Message, "is not connected")
			require.Empty(t, up.dispatched(), "a disconnected client must never be dispatched to")
		})
	}
}

// The gate evaluated generation A; generation B connected and completed
// discovery before the live check, with the tool now destructive. The verdicts
// (quarantine, profile policy, token tier, audit operation) all describe A, so
// the replay is refused with the discovery-window body and the next replay
// evaluates B from scratch. Admin and full-tier token included.
func TestReplayToolCall_GenerationChangesBetweenGateAndLiveCheck_Refused(t *testing.T) {
	for label, ctx := range map[string]context.Context{
		"read-only token": replayTokenCtx(auth.PermRead),
		"admin":           auth.WithAuthContext(context.Background(), auth.AdminContext()),
		"full token":      replayTokenCtx(auth.PermRead, auth.PermWrite, auth.PermDestructive),
	} {
		t.Run(label, func(t *testing.T) {
			mainSrv, proxy, rt, up, callID := replayGateFixture(t, readSpec("create_issue"))
			proxy.replayAfterGate = func() {
				bounceConnection(t, proxy, "github")
				stampDiscoveredOnLiveConnection(t, proxy, rt, "github", []stateview.ToolInfo{destructiveSpec("create_issue").info()})
			}
			t.Cleanup(func() { proxy.replayAfterGate = nil })

			_, err := mainSrv.ReplayToolCall(ctx, callID, nil)
			var refusal *profile.ToolBlockedError
			require.ErrorAs(t, err, &refusal)
			assert.Equal(t, replayBlockReasonToolNotCallable, refusal.Reason)
			assert.Contains(t, refusal.Message, unresolvedToolIdentityMessage("github", "create_issue", false))
			require.Empty(t, up.dispatched(), "a verdict about generation A must not dispatch onto generation B")
		})
	}
}
