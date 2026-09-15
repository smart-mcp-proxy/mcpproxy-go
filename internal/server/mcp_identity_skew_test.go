package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/jsruntime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 105 FR-009 (research D4) — the identity gate must not skew between the
// surfaces (astra r2 C2, C3).
//
// C2: dispatch refuses a name the KNOWN, CONNECTED server's completed
// discovery does not list ("undiscovered or stale name"), for every caller.
// describe_tool (definition mode) answered the same id from a stale index
// document and check mode / the REST preflight reported it ready — an
// existence/approval gate skew Spec 098 FR-002 forbids (dispatch refusal ⇒
// non-ready preflight). Both readers now consult the same identity resolution
// dispatch does; the retrieve_tools SEARCH listing stays index-based (Spec
// 085), its stale hit self-heals through the dispatch body.
//
// C3: the identity gate authorized from the StateView snapshot alone. When
// the live client reconnected while the snapshot kept the previous
// connection's stamp (dropped or lagging events; no reconcile edge
// observed), a name connection A discovered was dispatched to connection B
// unverified. The snapshot now carries the live client's connection token
// with its discovery stamp, and every identity read compares it with the
// client's current token: a mismatch is the discovery window ("retry
// shortly") until B's own pass re-stamps it.
//
// G1 (codex r6): the identity gate derived its hydration from the StateView's
// cached Enabled / Quarantined flags while the server-level verdicts read the
// persisted record. An operator's quarantine / disable that the StateView
// had not caught up with yet answered the identity refusal for an absent
// name and the server-level verdict for the listed sibling. Hydration now
// derives from the same persisted read as the server-level verdicts, on
// both dispatch paths.

// describeDefinitions runs describe_tool in definition mode for one id and
// returns the definitions and per-id errors.
func describeDefinitions(t *testing.T, proxy *MCPProxyServer, ctx context.Context, id string) (definitions, errs []map[string]interface{}) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"tool_ids": []interface{}{id}}
	result, err := proxy.handleDescribeTool(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "%s", resultText(t, result))
	var payload struct {
		Definitions []map[string]interface{} `json:"definitions"`
		Errors      []map[string]interface{} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
	return payload.Definitions, payload.Errors
}

// describeCheckStatus runs describe_tool in check mode for one id and returns
// the batch verdict and the id's status.
func describeCheckStatus(t *testing.T, proxy *MCPProxyServer, ctx context.Context, id string) (verdict, status, reason string) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"tool_ids": []interface{}{id}, "check": true}
	result, err := proxy.handleDescribeTool(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "%s", resultText(t, result))
	var payload describeCheckPayload
	require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
	res := checkResultByID(t, payload, id)
	return payload.Verdict, res.Status, res.Reason
}

// identitySkewCallers are the D4 callers: an administrator (SC-005's named
// exception) and a full-tier token scoped to the server.
func identitySkewCallers() map[string]context.Context {
	return map[string]context.Context{
		"admin":           adminCtx(),
		"full-tier token": fullTierAgentOn("a"),
	}
}

func TestDescribeAndPreflight_UnresolvedIdentity_MatchDispatch(t *testing.T) {
	t.Run("stale index document for a name discovery no longer lists", func(t *testing.T) {
		for label, ctx := range identitySkewCallers() {
			t.Run(label, func(t *testing.T) {
				proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
				proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
				up := startCountingUpstream(t, proxy, rt, "a", readSpec("erase"))
				rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) { s.State = "ready" })
				require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
					ServerName: "a", Name: "erase", RawName: "erase",
					Description: "Read erase", ParamsJSON: `{"type":"object"}`, Hash: "live",
				}))
				// The index still holds a document for a tool the server's
				// completed discovery does not list (a failed Bleve delete
				// is logged and skipped), and an approved record for it.
				require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
					ServerName: "a", Name: "old_tool", RawName: "old_tool",
					Description: "Read old_tool (STALE)", ParamsJSON: `{"type":"object"}`, Hash: "stale",
				}))
				require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
					ServerName: "a", ToolName: "old_tool", Status: storage.ToolApprovalStatusApproved, IdentityKeyed: true,
				}))
				require.True(t, proxy.resolveExactToolIdentity("a", "old_tool").Unresolved(), "fixture: the name is unresolved")

				// Dispatch: the D4 refusal, zero upstream calls.
				result, text := callToolReadResult(t, proxy, ctx, "a:old_tool")
				require.True(t, result.IsError)
				require.Contains(t, text, "cannot be resolved")
				require.Equal(t, int64(0), up.count.Load())

				// describe_tool definition mode: withheld with the not-found
				// shape — never the stale definition.
				visible, reason := proxy.toolVisibleToSession(ctx, "a", "old_tool")
				assert.False(t, visible, "an unresolved identity must not be visible to describe")
				assert.Equal(t, visReasonToolUnresolved, reason)
				defs, errs := describeDefinitions(t, proxy, ctx, "a:old_tool")
				assert.Empty(t, defs, "describe must not render a definition dispatch refuses: %v", defs)
				require.Len(t, errs, 1)
				assert.Equal(t, "a:old_tool", errs[0]["id"])
				assert.Equal(t, describeErrNotFound, errs[0]["error"])

				// Check mode / preflight: never ready when dispatch refuses
				// (Spec 098 FR-002).
				verdict, status, reason := describeCheckStatus(t, proxy, ctx, "a:old_tool")
				assert.NotEqual(t, "ready", verdict)
				assert.Equal(t, "unavailable", status)
				assert.Equal(t, "not_found", reason)

				// Positive control: the listed name is describable and ready.
				defs, errs = describeDefinitions(t, proxy, ctx, "a:erase")
				assert.Len(t, defs, 1)
				assert.Empty(t, errs)
				_, status, _ = describeCheckStatus(t, proxy, ctx, "a:erase")
				assert.Equal(t, "ready", status)
			})
		}
	})

	t.Run("migration alias: approved collapsed record, only the namespaced name served", func(t *testing.T) {
		for label, ctx := range identitySkewCallers() {
			t.Run(label, func(t *testing.T) {
				proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
				proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
				up := startCountingUpstream(t, proxy, rt, "a", readSpec("ns:erase"))
				rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) { s.State = "ready" })
				// The pre-105 collapsed record is kept on purpose while only
				// "ns:erase" is served (lifecycle.go), so "a:erase" has a
				// record but no identity — durably, until the operator acts.
				require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
					ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
				}))
				require.True(t, proxy.resolveExactToolIdentity("a", "erase").Unresolved())

				result, text := callToolReadResult(t, proxy, ctx, "a:erase")
				require.True(t, result.IsError)
				require.Contains(t, text, "cannot be resolved")
				require.Equal(t, int64(0), up.count.Load())

				verdict, status, reason := describeCheckStatus(t, proxy, ctx, "a:erase")
				assert.NotEqual(t, "ready", verdict)
				assert.Equal(t, "unavailable", status)
				assert.Equal(t, "not_found", reason)
			})
		}
	})

	t.Run("discovery window: a hydrated server without a completed pass is initializing, not ready", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		startCountingUpstream(t, proxy, rt, "a", readSpec("erase"))
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
			ServerName: "a", Name: "erase", RawName: "erase", Description: "Read erase", ParamsJSON: `{"type":"object"}`, Hash: "h",
		}))
		rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
			s.State = "ready"
			s.ToolsDiscovered = false
		})
		require.True(t, proxy.resolveExactToolIdentity("a", "erase").Unresolved())
		_, status, reason := describeCheckStatus(t, proxy, adminCtx(), "a:erase")
		assert.Equal(t, "unavailable", status)
		assert.Equal(t, "server_initializing", reason)
		visible, vreason := proxy.toolVisibleToSession(adminCtx(), "a", "erase")
		assert.False(t, visible)
		assert.Equal(t, visReasonToolUnresolved, vreason)
	})
}

// TestCallTool_StaleConnectionIdentityRefused pins C3: the StateView keeps
// connection A's discovery stamp while the live client is already connection
// B (the connect/disconnect events were dropped or lag, and no reconcile has
// observed the edge). A's names must not dispatch to B.
func TestCallTool_StaleConnectionIdentityRefused(t *testing.T) {
	variants := []struct {
		name string
		// reserve reconfigures the stub for connection B and returns the
		// tool set B actually serves.
		reserve func(up *countingUpstream) []stateview.ToolInfo
	}{
		{name: "B re-serves the name as DESTRUCTIVE", reserve: func(up *countingUpstream) []stateview.ToolInfo {
			up.serve(destructiveSpec("erase"))
			return []stateview.ToolInfo{destructiveSpec("erase").info()}
		}},
		{name: "B does not serve the name at all", reserve: func(*countingUpstream) []stateview.ToolInfo { return nil }},
	}
	for _, v := range variants {
		for _, quarantineOff := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/quarantine_off=%v", v.name, quarantineOff), func(t *testing.T) {
				proxy, rt := createTestProxyWithRuntimeCfg(t, []*config.ServerConfig{{Name: "a", Enabled: true}}, func(cfg *config.Config) {
					if quarantineOff {
						f := false
						cfg.QuarantineEnabled = &f
					}
				})
				proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
				up := startCountingUpstream(t, proxy, rt, "a", readSpec("erase"))
				require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{
					ServerName: "a", Name: "erase", RawName: "erase", Description: "Read erase", ParamsJSON: `{"type":"object"}`, Hash: "h",
				}))

				// Control on connection A: the name dispatches.
				ctl, ctlText := callToolReadResult(t, proxy, adminCtx(), "a:erase")
				require.False(t, ctl.IsError, "control on A: %s", ctlText)
				require.Equal(t, int64(1), up.count.Load())

				// Connection B: the upstream changes its tool set and the
				// managed client really reconnects. The StateView is left
				// exactly as A published it.
				up.mcpSrv.DeleteTools("erase")
				bTools := v.reserve(up)
				client, ok := proxy.upstreamManager.GetClient("a")
				require.True(t, ok)
				require.NoError(t, client.Disconnect())
				require.NoError(t, client.Connect(context.Background()))
				require.Eventually(t, client.IsConnected, 10*time.Second, 50*time.Millisecond, "fixture: the live client is connection B")
				st, ok := rt.Supervisor().StateView().GetServer("a")
				require.True(t, ok)
				require.True(t, st.Connected && st.ToolsDiscovered, "fixture: the snapshot still carries A's stamp")
				require.Len(t, st.Tools, 1)

				identity := proxy.resolveExactToolIdentity("a", "erase")
				require.True(t, identity.ServerKnown && identity.SnapshotHydrated)
				assert.True(t, identity.Unresolved(), "A's stamp must not resolve a name on connection B")

				for label, ctx := range identitySkewCallers() {
					t.Run(label, func(t *testing.T) {
						result, text := callToolReadResult(t, proxy, ctx, "a:erase")
						require.True(t, result.IsError, "%s", text)
						assert.Contains(t, text, "Permission denied", "the unresolved-identity body")
						assert.Contains(t, text, "discovery has not completed", "B has no completed pass: the remediation is to retry")
						assert.NotContains(t, text, "not found", "the upstream must never answer")
						assert.Equal(t, int64(1), up.count.Load(), "D4: nothing reaches connection B unverified (dispatched %v)", up.dispatched())
					})
				}

				// The sandbox path shares the identity read.
				sb := runSandboxCallTool(t, proxy, adminCtx(), "a", "erase")
				assert.False(t, sb.OK, "the sandbox must refuse too")
				assert.Equal(t, int64(1), up.count.Load())

				// describe / preflight agree: the name is in the discovery
				// window, never ready.
				visible, reason := proxy.toolVisibleToSession(adminCtx(), "a", "erase")
				assert.False(t, visible)
				assert.Equal(t, visReasonToolUnresolved, reason)
				rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) { s.State = "ready" })
				_, status, preason := describeCheckStatus(t, proxy, adminCtx(), "a:erase")
				assert.Equal(t, "unavailable", status)
				assert.Equal(t, "server_initializing", preason)

				// Positive control: B's own discovery pass republishes under
				// B's token and the listed name dispatches again.
				stampDiscoveredOnLiveConnection(t, proxy, rt, "a", bTools)
				if len(bTools) == 0 {
					require.True(t, proxy.resolveExactToolIdentity("a", "erase").Unresolved(), "B lists nothing: still refused, as a stale name")
					_, text := callToolReadResult(t, proxy, adminCtx(), "a:erase")
					assert.Contains(t, text, "undiscovered or stale name")
					assert.Equal(t, int64(1), up.count.Load())
					return
				}
				require.False(t, proxy.resolveExactToolIdentity("a", "erase").Unresolved())
				// B serves erase as DESTRUCTIVE: call_tool_read is now the
				// wrong variant, but a destructive call succeeds for the
				// administrator — proving the tier is B's, not A's.
				req := mcp.CallToolRequest{}
				req.Params.Name = "call_tool_destructive"
				req.Params.Arguments = map[string]interface{}{"name": "a:erase"}
				res, err := proxy.handleCallToolVariant(adminCtx(), req, "call_tool_destructive")
				require.NoError(t, err)
				assert.False(t, res.IsError, "control on B: %s", resultText(t, res))
				assert.Equal(t, int64(2), up.count.Load())
			})
		}
	}
}

// TestCallTool_RetainedToolsOnNotConnectedSnapshot_LiveClientRefused pins
// codex r4 E1: the reconcile sweep republishes a server's RETAINED tool set
// into a StateView entry that reads Connected=false, ToolsDiscovered=false,
// DiscoveryEpoch=0 (supervisor.reconcile copies existing.Tools forward;
// TestSupervisor_ToolsDiscoveredMarker pins that shape). Once the next
// connection is Ready but before its connect event or the next sweep flips
// Connected, the snapshot is NOT hydrated (so Unresolved() cannot fire) yet
// still LISTS the previous generation's names. liveIdentityRefusal used to
// admit that shape — Found without certification — and the call fell to the
// unpinned CallTool branch on the fresh connection with zero certification.
// A live client must dispatch only a CERTIFIED identity; a found-but-
// uncertified name answers the discovery-window body with zero upstream
// calls, for every consumer of the check.
func TestCallTool_RetainedToolsOnNotConnectedSnapshot_LiveClientRefused(t *testing.T) {
	for label, ctx := range identitySkewCallers() {
		t.Run(label, func(t *testing.T) {
			proxy, rt, up := seedEpochFixture(t)
			// Connection B is Ready; the StateView carries the reconcile
			// shape: not connected, unstamped, previous generation's tools.
			bounceConnection(t, proxy, "a")
			rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
				s.Connected = false
				s.ToolsDiscovered = false
				s.DiscoveryEpoch = 0
				s.Tools = up.Tools
				s.ToolCount = len(up.Tools)
			})
			identity := proxy.resolveExactToolIdentity("a", "erase")
			require.True(t, identity.ServerKnown && !identity.SnapshotHydrated && identity.Found,
				"fixture: known, not hydrated, yet listed (got %+v)", identity)
			require.False(t, identity.Unresolved(), "fixture: the top-of-dispatch gate defers on a not-hydrated snapshot")
			require.False(t, identity.certified())

			result, text := callToolReadResult(t, proxy, ctx, "a:erase")
			require.True(t, result.IsError, "%s", text)
			assert.Contains(t, text, unresolvedToolIdentityMessage("a", "erase", false),
				"a found-but-uncertified name on a live client is the discovery window (got %s)", text)
			assert.NotContains(t, text, "not found", "the upstream must never answer")
			assert.Equal(t, int64(0), up.count.Load(), "nothing reaches connection B uncertified (dispatched %v)", up.dispatched())

			// The sandbox's permission read shares the closure and answers
			// with jsruntime's permission envelope (its one unresolved
			// wording) before the bridge is reached.
			sb := runSandboxCallTool(t, proxy, adminCtx(), "a", "erase")
			assert.False(t, sb.OK, "the sandbox must refuse too (got %q: %s)", sb.Code, sb.Message)
			assert.Contains(t, sb.Message, "cannot be resolved")
			assert.Equal(t, int64(0), up.count.Load())
			assert.Equal(t, jsruntime.PermissionTierUnresolved, proxy.lookupToolPermission("a", "erase"),
				"the sandbox's tier read must not classify an uncertified name")

			// Positive control: B's own pass stamps the snapshot under B's
			// token and the listed name dispatches, pinned to B.
			stampDiscoveredOnLiveConnection(t, proxy, rt, "a", up.Tools)
			require.True(t, proxy.resolveExactToolIdentity("a", "erase").certified())
			_, text = callToolReadResult(t, proxy, ctx, "a:erase")
			assert.NotContains(t, text, "Permission denied", "control: certified on B (got %s)", text)
			assert.Equal(t, int64(1), up.count.Load())
		})
	}
}

// TestCallTool_StaleConnectedSnapshot_LiveClientDisconnected_KeepsNotConnectedVerdict
// pins codex r5 F1, the mirror of astra r1 I3: the StateView still reads
// Connected=true / ToolsDiscovered=true with connection A's stamp and tools
// (the server_disconnected event is in flight or was dropped, and the 30s
// reconcile has not swept yet) while the live client is NOT connected. The
// identity read used to take the snapshot's Connected at face value
// (hydrated, discovery done) and refused an absent name at the top of the
// dispatch as "undiscovered or stale name — refresh with retrieve_tools",
// while the LISTED sibling on the same dropped server, certified by the same
// stale stamp, fell through to "Server 'a' is not connected". Two names on
// one dropped server answered different verdicts, and the ghost's
// remediation was wrong (retrieve_tools cannot heal a dropped server) —
// contrary to research D4 (disconnected servers keep their server-level
// verdicts) and SC-005 parity. The snapshot's Connected is now overruled by
// the live client: a client that is present and NOT connected makes the
// snapshot not hydrated, exactly as when it reads Connected=false, so the
// not-connected verdict owns BOTH names on every path, with zero upstream
// calls and no reconnect inside dispatch.
func TestCallTool_StaleConnectedSnapshot_LiveClientDisconnected_KeepsNotConnectedVerdict(t *testing.T) {
	proxy, rt, up := seedEpochFixture(t)

	client, ok := proxy.upstreamManager.GetClient("a")
	require.True(t, ok)
	stampEpoch := client.ConnectionEpoch()
	require.NoError(t, client.Disconnect())
	require.Eventually(t, func() bool { return !client.IsConnected() }, 5*time.Second, 20*time.Millisecond,
		"fixture: the live client is dropped")

	// Model the delayed / dropped disconnect event: the snapshot stays
	// exactly as connection A published it.
	rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
		s.Connected = true
		s.ToolsDiscovered = true
		s.DiscoveryEpoch = stampEpoch
		s.Tools = up.Tools
		s.ToolCount = len(up.Tools)
	})
	st, ok := rt.Supervisor().StateView().GetServer("a")
	require.True(t, ok)
	require.True(t, st.Connected && st.ToolsDiscovered, "fixture: the snapshot still reads connected+discovered (%+v)", st)
	require.False(t, client.IsConnected(), "fixture: the live client disagrees with the snapshot")

	ghost := proxy.resolveExactToolIdentity("a", "ghost")
	require.True(t, ghost.ServerKnown)
	assert.False(t, ghost.SnapshotHydrated, "the live client is not connected: the snapshot's Connected is stale (got %+v)", ghost)
	assert.False(t, ghost.Unresolved(), "a dropped server is not the identity condition, whatever its snapshot says")
	listed := proxy.resolveExactToolIdentity("a", "erase")
	assert.False(t, listed.certified(), "a stale stamp certifies nothing on a dropped client (got %+v)", listed)

	for label, ctx := range identitySkewCallers() {
		t.Run(label, func(t *testing.T) {
			// The ghost: the not-connected verdict, not the identity body.
			result, text := callToolReadResult(t, proxy, ctx, "a:ghost")
			require.True(t, result.IsError, "%s", text)
			assert.Contains(t, text, "Server 'a' is not connected", "D4: a dropped server keeps the not-connected verdict (got %s)", text)
			assert.NotContains(t, text, "cannot be resolved", "the identity gate must not pre-empt the connection verdict")
			assert.NotContains(t, text, "retrieve_tools", "retrieve_tools cannot heal a dropped server: wrong remediation")

			// The listed sibling: the same verdict, for the same reason.
			result, text = callToolReadResult(t, proxy, ctx, "a:erase")
			require.True(t, result.IsError, "%s", text)
			assert.Contains(t, text, "Server 'a' is not connected", "SC-005 parity: both names on one dropped server answer alike (got %s)", text)

			assert.False(t, client.IsConnected(), "dispatch must never reconnect on its own")
			assert.Equal(t, int64(0), up.count.Load(), "nothing reaches a dropped upstream (dispatched %v)", up.dispatched())
		})
	}

	// Nested: the sandbox's tier read does not classify the ghost as
	// unresolved, and the managed client's own not-connected refusal answers
	// through the bridge's upstream-error envelope, zero upstream calls.
	assert.NotEqual(t, jsruntime.PermissionTierUnresolved, proxy.lookupToolPermission("a", "ghost"),
		"the sandbox's tier read must defer to the server-level verdict on a dropped client")
	sb := runSandboxCallTool(t, proxy, adminCtx(), "a", "ghost")
	assert.False(t, sb.OK)
	assert.Equal(t, string(jsruntime.ErrorCodeUpstreamError), sb.Code, "got %q: %s", sb.Code, sb.Message)
	assert.Contains(t, sb.Message, "not connected", "sandbox: D4 expects the not-connected verdict (got %q: %s)", sb.Code, sb.Message)
	assert.NotContains(t, sb.Message, "cannot be resolved")
	assert.False(t, client.IsConnected(), "the nested path must never reconnect inside dispatch")
	assert.Equal(t, int64(0), up.count.Load())

	// Positive control: the server reconnects and its own pass re-stamps the
	// fresh generation. The never-listed name is now Unresolved (hydrated,
	// discovery done, absent) on both paths; the listed sibling dispatches.
	require.NoError(t, proxy.upstreamManager.ConnectAll(context.Background()))
	require.Eventually(t, client.IsConnected, 10*time.Second, 50*time.Millisecond, "fixture: the stub must reconnect")
	stampDiscoveredOnLiveConnection(t, proxy, rt, "a", up.Tools)
	require.True(t, proxy.resolveExactToolIdentity("a", "ghost").Unresolved(), "control: the fresh stamp does not list ghost")
	_, text := callToolReadResult(t, proxy, adminCtx(), "a:ghost")
	assert.Contains(t, text, "cannot be resolved")
	assert.Equal(t, jsruntime.PermissionTierUnresolved, proxy.lookupToolPermission("a", "ghost"))
	assert.Equal(t, int64(0), up.count.Load())
	ctl, ctlText := callToolReadResult(t, proxy, adminCtx(), "a:erase")
	assert.False(t, ctl.IsError, "control: the listed sibling dispatches after the reconnect: %s", ctlText)
	assert.Equal(t, int64(1), up.count.Load())
}

// TestCallTool_PersistedServerVerdictOutranksLaggingStateView pins codex r6
// G1: an operator quarantines or disables a server AFTER its discovery
// completed. Runtime.EnableServer / QuarantineServer write the persisted
// record synchronously and reconcile the StateView afterwards (a goroutine,
// or the LoadConfiguredServers pass after the write), so a concurrent MCP
// call reads a record that says quarantined / disabled while the StateView
// entry still reads enabled, non-quarantined, connected, discovered, and
// the live client is still connected. The identity read used to derive its
// hydration from the StateView flags and refused an absent name as
// "undiscovered or stale — refresh with retrieve_tools" while the LISTED
// sibling on the same server answered the persisted record's verdict
// (quarantine analysis / TOOL_BLOCKED) — two names, one server, two
// verdicts (the F1-class parity break of research D4 / SC-005), and the
// ghost's remediation was wrong. Identity hydration now derives from the
// SAME persisted record the server-level verdicts read, so the server-level
// verdict owns every name on the server, on both paths, with zero upstream
// calls.
func TestCallTool_PersistedServerVerdictOutranksLaggingStateView(t *testing.T) {
	cells := []struct {
		name string
		flip func(*config.ServerConfig)
		// directBody / directIsError: the pre-105 retrieve-surface answer
		// (TestCallToolRead_EmptySnapshot_KeepsServerLevelVerdicts pins the
		// same bodies for a consistent snapshot).
		directBody    string
		directIsError bool
		// nestedBody: policyRefusal's text as the sandbox sees it.
		nestedBody string
	}{
		{
			name:          "quarantined after discovery",
			flip:          func(c *config.ServerConfig) { c.Quarantined = true },
			directBody:    "QUARANTINED_SERVER_BLOCKED",
			directIsError: false,
			nestedBody:    "is quarantined for security review",
		},
		{
			name:          "disabled after discovery",
			flip:          func(c *config.ServerConfig) { c.Enabled = false },
			directBody:    "TOOL_BLOCKED",
			directIsError: true,
			nestedBody:    "TOOL_BLOCKED",
		},
	}
	for _, cell := range cells {
		for label, ctx := range identitySkewCallers() {
			t.Run(cell.name+"/"+label, func(t *testing.T) {
				proxy, rt, up := seedEpochFixture(t)

				// The operator's write lands in storage; the StateView and
				// the live client are exactly as discovery left them.
				stored, err := proxy.storage.GetUpstreamServer("a")
				require.NoError(t, err)
				flipped := *stored
				cell.flip(&flipped)
				require.NoError(t, proxy.storage.SaveUpstreamServer(&flipped))
				st, ok := rt.Supervisor().StateView().GetServer("a")
				require.True(t, ok)
				require.True(t, st.Enabled && !st.Quarantined && st.Connected && st.ToolsDiscovered,
					"fixture: the StateView still reads enabled, non-quarantined, connected, discovered (%+v)", st)
				client, ok := proxy.upstreamManager.GetClient("a")
				require.True(t, ok)
				require.True(t, client.IsConnected(), "fixture: the live client is still connected")

				ghost := proxy.resolveExactToolIdentity("a", "ghost")
				require.True(t, ghost.ServerKnown)
				assert.False(t, ghost.SnapshotHydrated,
					"identity hydration derives from the persisted record, not the lagging StateView flags (got %+v)", ghost)
				assert.False(t, ghost.Unresolved(), "a quarantined / disabled server is not the identity condition, whatever the StateView says")

				for _, name := range []string{"ghost", "erase"} {
					// Direct: the persisted record's verdict, for the absent
					// name and the listed sibling alike.
					result, text := callToolReadResult(t, proxy, ctx, "a:"+name)
					assert.Equal(t, cell.directIsError, result.IsError, "%s: %s", name, text)
					assert.Contains(t, text, cell.directBody, "%s: SC-005 parity — the server-level verdict owns every name on the server (got %s)", name, text)
					assert.NotContains(t, text, "cannot be resolved", "%s: the identity gate must not pre-empt the server-level verdict", name)
					assert.NotContains(t, text, "retrieve_tools and retry", "%s: retrieve_tools cannot heal a quarantined / disabled server: wrong remediation", name)

					// Nested: the sandbox's tier read defers to the server-level
					// verdict and policyRefusal answers it with zero upstream calls.
					assert.NotEqual(t, jsruntime.PermissionTierUnresolved, proxy.lookupToolPermission("a", name),
						"%s: the sandbox's tier read must defer to the persisted server-level verdict", name)
					sb := runSandboxCallTool(t, proxy, ctx, "a", name)
					assert.False(t, sb.OK, "%s: the sandbox must refuse", name)
					assert.Equal(t, string(jsruntime.ErrorCodeUpstreamError), sb.Code, "%s: got %q: %s", name, sb.Code, sb.Message)
					assert.Contains(t, sb.Message, cell.nestedBody, "%s: sandbox: the server-level verdict (got %q: %s)", name, sb.Code, sb.Message)
					assert.NotContains(t, sb.Message, "cannot be resolved", "%s: the identity gate must not pre-empt the server-level verdict", name)
				}
				assert.Equal(t, int64(0), up.count.Load(), "nothing reaches the upstream (dispatched %v)", up.dispatched())

				// Positive control: the operator's write is reverted before
				// the StateView ever caught up. The never-listed name is
				// Unresolved again on both paths; the listed sibling
				// dispatches, certified on the live generation.
				require.NoError(t, proxy.storage.SaveUpstreamServer(stored))
				require.True(t, proxy.resolveExactToolIdentity("a", "ghost").Unresolved(), "control: ghost is unresolved once the record is restored")
				_, text := callToolReadResult(t, proxy, ctx, "a:ghost")
				assert.Contains(t, text, "cannot be resolved")
				assert.Equal(t, jsruntime.PermissionTierUnresolved, proxy.lookupToolPermission("a", "ghost"))
				assert.Equal(t, int64(0), up.count.Load())
				ctl, ctlText := callToolReadResult(t, proxy, ctx, "a:erase")
				assert.False(t, ctl.IsError, "control: the listed sibling dispatches once the record is restored: %s", ctlText)
				assert.Equal(t, int64(1), up.count.Load())
			})
		}
	}
}

// TestCallTool_RecordUnquarantinedWhileStateViewLags_NeverDispatches is the
// reverse skew of codex r6 G1: the operator LIFTS the quarantine (the
// persisted record reads enabled, not quarantined) while the StateView still
// carries the quarantined shape — Quarantined=true, Connected=false,
// Tools=nil, no discovery stamp. The persisted record now decides the
// server-level verdicts AND the identity hydration, but hydration still
// requires the snapshot to be connected, and the live client stays the
// authority on dispatch: while the client is not connected every name
// answers the not-connected verdict; once it connects and until its own
// discovery pass re-stamps the snapshot, every name is the D4 discovery
// window ("discovery has not completed") — never a dispatch with the
// destructive-tier fallback, because a snapshot that was never hydrated
// for this connection certifies nothing.
func TestCallTool_RecordUnquarantinedWhileStateViewLags_NeverDispatches(t *testing.T) {
	proxy, rt, up := seedEpochFixture(t)
	stored, err := proxy.storage.GetUpstreamServer("a")
	require.NoError(t, err)
	require.True(t, stored.Enabled && !stored.Quarantined, "fixture: the persisted record reads enabled, not quarantined")

	// The StateView lags: still the quarantined shape (never discovered).
	rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
		s.Quarantined = true
		s.Connected = false
		s.ToolsDiscovered = false
		s.DiscoveryEpoch = 0
		s.Tools = nil
		s.ToolCount = 0
	})
	client, ok := proxy.upstreamManager.GetClient("a")
	require.True(t, ok)

	// Phase 1: the live client is not connected yet.
	require.NoError(t, client.Disconnect())
	require.Eventually(t, func() bool { return !client.IsConnected() }, 5*time.Second, 20*time.Millisecond)
	ghost := proxy.resolveExactToolIdentity("a", "ghost")
	require.True(t, ghost.ServerKnown && !ghost.SnapshotHydrated && !ghost.Unresolved(), "fixture: not hydrated, not the identity condition (%+v)", ghost)
	for label, ctx := range identitySkewCallers() {
		t.Run("not connected/"+label, func(t *testing.T) {
			for _, name := range []string{"ghost", "erase"} {
				result, text := callToolReadResult(t, proxy, ctx, "a:"+name)
				require.True(t, result.IsError, "%s: %s", name, text)
				assert.Contains(t, text, "Server 'a' is not connected", "%s: the not-connected verdict owns a dropped server (got %s)", name, text)
				assert.NotContains(t, text, "QUARANTINED", "%s: the lifted quarantine must not be answered from the lagging StateView", name)
				assert.NotContains(t, text, "cannot be resolved", name)
			}
			sb := runSandboxCallTool(t, proxy, ctx, "a", "ghost")
			assert.False(t, sb.OK)
			assert.Equal(t, string(jsruntime.ErrorCodeUpstreamError), sb.Code, "got %q: %s", sb.Code, sb.Message)
			assert.Contains(t, sb.Message, "not connected", "sandbox: the not-connected verdict (got %q: %s)", sb.Code, sb.Message)
			assert.Equal(t, int64(0), up.count.Load())
		})
	}

	// Phase 2: the client connects; the snapshot has not been re-stamped.
	require.NoError(t, proxy.upstreamManager.ConnectAll(context.Background()))
	require.Eventually(t, client.IsConnected, 10*time.Second, 50*time.Millisecond, "fixture: the stub must reconnect")
	for label, ctx := range identitySkewCallers() {
		t.Run("connected, not re-stamped/"+label, func(t *testing.T) {
			for _, name := range []string{"ghost", "erase"} {
				result, text := callToolReadResult(t, proxy, ctx, "a:"+name)
				require.True(t, result.IsError, "%s: %s", name, text)
				assert.Contains(t, text, unresolvedToolIdentityMessage("a", name, false),
					"%s: the D4 discovery window until this connection's own pass re-stamps the snapshot (got %s)", name, text)
				assert.NotContains(t, text, "QUARANTINED", name)
			}
			assert.Equal(t, jsruntime.PermissionTierUnresolved, proxy.lookupToolPermission("a", "ghost"))
			sb := runSandboxCallTool(t, proxy, ctx, "a", "ghost")
			assert.False(t, sb.OK)
			assert.Equal(t, string(jsruntime.ErrorCodePermissionDenied), sb.Code, "got %q: %s", sb.Code, sb.Message)
			assert.Contains(t, sb.Message, "cannot be resolved")
			assert.Equal(t, int64(0), up.count.Load(), "nothing dispatches on a never-hydrated snapshot (dispatched %v)", up.dispatched())
		})
	}

	// Positive control: this connection's own discovery pass re-stamps the
	// snapshot. The StateView's Quarantined flag is STILL stale (true) — the
	// persisted record is the authority — so the listed name is certified and
	// dispatches, while the never-listed name is a stale name.
	stampDiscoveredOnLiveConnection(t, proxy, rt, "a", up.Tools)
	st, ok := rt.Supervisor().StateView().GetServer("a")
	require.True(t, ok)
	require.True(t, st.Quarantined, "fixture: the StateView flag still lags")
	require.True(t, proxy.resolveExactToolIdentity("a", "erase").certified(), "control: the persisted record hydrates the re-stamped snapshot")
	require.True(t, proxy.resolveExactToolIdentity("a", "ghost").Unresolved())
	_, text := callToolReadResult(t, proxy, adminCtx(), "a:ghost")
	assert.Contains(t, text, unresolvedToolIdentityMessage("a", "ghost", true))
	assert.Equal(t, int64(0), up.count.Load())
	ctl, ctlText := callToolReadResult(t, proxy, adminCtx(), "a:erase")
	assert.False(t, ctl.IsError, "control: the listed sibling dispatches: %s", ctlText)
	assert.Equal(t, int64(1), up.count.Load())
	sb := runSandboxCallTool(t, proxy, adminCtx(), "a", "erase")
	assert.True(t, sb.OK, "control: nested dispatch (got %q: %s)", sb.Code, sb.Message)
	assert.Equal(t, int64(2), up.count.Load())
}

// TestCallTool_GateRecordIsTheOnlyPersistedReadOfADispatch pins the codex r7
// H1 invariant: a dispatch reads the persisted server record ONCE — the
// shared gate's read — and every later identity step (hydration, live
// certification) reuses that record; the live certification re-reads only
// the StateView and the live client. An operator's quarantine / disable
// that lands AFTER the gate has captured its record and BEFORE the live
// certification (dispatchGatePause stages exactly that window) is therefore
// invisible to this dispatch: the gate's record admitted, the identity it
// hydrated is certified on the live generation, and the call is dispatched
// pinned to that generation — the pre-105 race semantics (gate, then
// dispatch), with the operator's write owning every LATER dispatch. It
// used to be answered from a second, independent read that saw the new
// record, un-hydrated the identity, and refused with the discovery-window
// body ("discovery has not completed ... retry shortly") — a verdict neither
// the gate's record (admit) nor the new record (quarantine analysis /
// TOOL_BLOCKED) selects, and a remediation that cannot heal either.
//
// The nested path used to take three independent reads for one sandboxed
// call (policyRefusal, identity hydration, live certification); it now takes
// one.
func TestCallTool_GateRecordIsTheOnlyPersistedReadOfADispatch(t *testing.T) {
	cells := []struct {
		name string
		flip func(*config.ServerConfig)
		// directBody / nestedBody: the persisted verdict the NEXT dispatch —
		// the first one whose gate reads the flipped record — answers.
		directBody string
		nestedBody string
	}{
		{
			name:       "quarantined between the gate and the live certification",
			flip:       func(c *config.ServerConfig) { c.Quarantined = true },
			directBody: "QUARANTINED_SERVER_BLOCKED",
			nestedBody: "is quarantined for security review",
		},
		{
			name:       "disabled between the gate and the live certification",
			flip:       func(c *config.ServerConfig) { c.Enabled = false },
			directBody: "TOOL_BLOCKED",
			nestedBody: "TOOL_BLOCKED",
		},
	}
	for _, cell := range cells {
		for label, ctx := range identitySkewCallers() {
			t.Run(cell.name+"/"+label, func(t *testing.T) {
				proxy, rt, up := seedEpochFixture(t)
				stored, err := proxy.storage.GetUpstreamServer("a")
				require.NoError(t, err)
				flipped := *stored
				cell.flip(&flipped)

				// The seam: the operator's write lands in storage after the
				// dispatch gate captured its record and before the live
				// certification. The StateView and the live client are
				// untouched (the reconcile that follows the write has not run).
				var paused atomic.Int64
				proxy.dispatchGatePause = func(serverName, toolName string) {
					require.Equal(t, "a", serverName)
					require.Equal(t, "erase", toolName)
					require.NoError(t, proxy.storage.SaveUpstreamServer(&flipped))
					paused.Add(1)
				}
				t.Cleanup(func() { proxy.dispatchGatePause = nil })

				// Direct: the gate's record admitted, so the dispatch is
				// certified on the live generation and reaches the upstream
				// exactly once — never the discovery-window body a second
				// persisted read would answer.
				result, text := callToolReadResult(t, proxy, ctx, "a:erase")
				require.Equal(t, int64(1), paused.Load(), "fixture: the seam must have fired once")
				assert.False(t, result.IsError, "direct: the gate's record admitted the dispatch (got %s)", text)
				assert.NotContains(t, text, "cannot be resolved", "direct: a second persisted read must not un-hydrate the identity the gate certified")
				assert.NotContains(t, text, cell.directBody, "direct: the gate's record was not yet flipped when it was read")
				assert.Equal(t, int64(1), up.count.Load(), "direct: dispatched once, on the gate's verdict (dispatched %v)", up.dispatched())

				// The NEXT dispatch reads the flipped record at its gate and
				// answers the persisted verdict, zero further upstream calls.
				proxy.dispatchGatePause = nil
				next, nextText := callToolReadResult(t, proxy, ctx, "a:erase")
				assert.Contains(t, nextText, cell.directBody, "direct: the next gate reads the operator's write (got %s)", nextText)
				assert.NotContains(t, nextText, "cannot be resolved", nextText)
				_ = next
				assert.Equal(t, int64(1), up.count.Load())

				// Nested: same invariant on the sandbox bridge. Reset the
				// record so the bridge's gate admits, and flip it again in
				// the seam.
				require.NoError(t, proxy.storage.SaveUpstreamServer(stored))
				st, ok := rt.Supervisor().StateView().GetServer("a")
				require.True(t, ok)
				require.True(t, st.Enabled && !st.Quarantined && st.Connected && st.ToolsDiscovered, "fixture: the StateView is as discovery left it (%+v)", st)
				paused.Store(0)
				proxy.dispatchGatePause = func(serverName, toolName string) {
					require.NoError(t, proxy.storage.SaveUpstreamServer(&flipped))
					paused.Add(1)
				}
				sb := runSandboxCallTool(t, proxy, ctx, "a", "erase")
				require.Equal(t, int64(1), paused.Load(), "fixture: the bridge's seam must have fired once")
				assert.True(t, sb.OK, "nested: the bridge's gate admitted the dispatch (got %q: %s)", sb.Code, sb.Message)
				assert.NotContains(t, sb.Message, "cannot be resolved", "nested: a second persisted read must not un-hydrate the identity the gate certified")
				assert.Equal(t, int64(2), up.count.Load(), "nested: dispatched once, on the gate's verdict (dispatched %v)", up.dispatched())

				proxy.dispatchGatePause = nil
				sbNext := runSandboxCallTool(t, proxy, ctx, "a", "erase")
				assert.False(t, sbNext.OK, "nested: the next gate reads the operator's write")
				assert.Contains(t, sbNext.Message, cell.nestedBody, "nested: the persisted verdict (got %q: %s)", sbNext.Code, sbNext.Message)
				assert.Equal(t, int64(2), up.count.Load(), "nothing further reaches the upstream (dispatched %v)", up.dispatched())
			})
		}
	}
}
