package server

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 105 FR-009 gap G5 (task T008): while the tool-level quarantine gate is
// ACTIVE for a server (`quarantine_enabled` on AND the server has not opted
// out via trust_mode auto / auto_approve_tool_changes), "no approval record"
// for a tool the discovery snapshot contains is PENDING, never ready
// (research D4). Two shapes of "no record" exist in production:
//
//   - genuinely none — reachable through the discovery fail-open paths
//     (tool_quarantine.go / lifecycle.go) that skip record creation;
//   - collapsed-only — the pre-migration state a pre-105 binary left behind,
//     in which discovery keyed the raw name "ns:erase" under "erase"
//     (everything after the first colon), so an approval that was granted to
//     `erase` must not be inherited by `ns:erase`.
//
// Both are proven at the gate (evaluateToolGate) AND through retrieve
// dispatch (handleCallToolVariant) against a counting upstream: a refusal is
// zero upstream calls. When the gate is off (`quarantine_enabled: false`)
// nothing changes — the third test is the regression control and passes on
// the merge base.

// noRecordSpec strips the seeded approval from a tool spec so the fixture
// publishes the tool into the StateView but files nothing in storage.
func noRecordSpec(spec toolSpec) toolSpec {
	spec.NoRecord = true
	return spec
}

// fullTierAgentCtx is an agent token allowed on "a" holding every tier — the
// caller that HEAD admits through every gate once the approval read fails
// open, so the only thing between it and the upstream is the G5 rule.
func fullTierAgentCtx() context.Context {
	return agentCtx([]string{"a"}, []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}, "")
}

// gateCallers are the caller cells the pending rule applies to: "every gate
// site, every caller" — administrators included (spec FR-009, SC-005 named
// exception; research D4).
func gateCallers() map[string]context.Context {
	return map[string]context.Context{
		"full-tier agent token": fullTierAgentCtx(),
		"api-key administrator": adminCtx(),
	}
}

// callToolReadVariant drives one call_tool_read dispatch for the canonical id.
func callToolReadVariant(t *testing.T, proxy *MCPProxyServer, ctx context.Context, name string) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = contracts.ToolVariantRead
	req.Params.Arguments = map[string]interface{}{"name": name}
	result, err := proxy.handleCallToolVariant(ctx, req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Content)
	return result
}

// requireManualTrustGateActive pins the fixture preconditions the G5 rule
// keys on: the live config has quarantine ON and the stored server record is
// manual-trust (the gate is not skipped).
func requireManualTrustGateActive(t *testing.T, proxy *MCPProxyServer, server string) {
	t.Helper()
	cfg := proxy.currentConfig()
	require.NotNil(t, cfg)
	require.True(t, cfg.IsQuarantineEnabled(), "fixture: quarantine_enabled must be on")
	stored, err := proxy.storage.GetUpstreamServer(server)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.False(t, stored.Quarantined, "fixture: the server itself is trusted (not server-quarantined)")
	require.False(t, stored.IsQuarantineSkipped(), "fixture: manual-trust server, tool-level gate applies")
	require.Equal(t, config.TrustModeManual, stored.EffectiveTrustMode())
}

// TestToolGate_NoApprovalRecordUnderActiveGate_IsPending is cell 1: the
// snapshot holds read-tier `ns:erase`, storage holds NO record under either
// key, the gate is active → refused at the gate and through dispatch with
// zero upstream calls, for the full-tier token and the administrator alike.
func TestToolGate_NoApprovalRecordUnderActiveGate_IsPending(t *testing.T) {
	for name, ctx := range gateCallers() {
		t.Run(name, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
			proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
			up := startCountingUpstream(t, proxy, rt, "a", noRecordSpec(readSpec("ns:erase")))
			requireManualTrustGateActive(t, proxy, "a")
			_, err := proxy.storage.GetToolApproval("a", "ns:erase")
			require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "fixture: no exact record")
			_, err = proxy.storage.GetToolApproval("a", "erase")
			require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "fixture: no collapsed record either")

			gate := proxy.evaluateToolGate("a", "ns:erase")
			assert.False(t, gate.callable(),
				"a tool the snapshot contains with no approval record must not be callable while the quarantine gate is active")
			assert.Equal(t, preflight.ToolClassPendingApproval, gate.class,
				"\"no record\" under an active gate classifies as pending, never ready")

			result := callToolReadVariant(t, proxy, ctx, "a:ns:erase")
			text := result.Content[0].(mcp.TextContent).Text
			assert.NotEqual(t, "ok", text, "the upstream's reply must never be relayed")
			assert.Equal(t, int64(0), up.count.Load(), "a refused call must never reach the upstream (got %q)", text)
			assert.Empty(t, up.dispatched())
		})
	}
}

// TestToolGate_CollapsedOnlyApprovedRecord_DoesNotApproveNamespacedTool is
// cell 2: the snapshot holds both `erase` and `ns:erase`; storage holds ONLY
// {a, erase, approved} — exactly what the collapsing discovery producer left
// behind for either name. The approval belongs to `erase` alone: `a:ns:erase`
// is refused (pending under its own name) while `a:erase` stays admitted.
func TestToolGate_CollapsedOnlyApprovedRecord_DoesNotApproveNamespacedTool(t *testing.T) {
	for name, ctx := range gateCallers() {
		t.Run(name, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
			proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
			up := startCountingUpstream(t, proxy, rt, "a",
				readSpec("erase"),                  // seeds {a, erase, approved}
				noRecordSpec(readSpec("ns:erase")), // nothing under the exact key
			)
			requireManualTrustGateActive(t, proxy, "a")
			rec, err := proxy.storage.GetToolApproval("a", "erase")
			require.NoError(t, err)
			require.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
			_, err = proxy.storage.GetToolApproval("a", "ns:erase")
			require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "fixture: only the collapsed record may exist")

			// Positive control first: the record approves the name it stores.
			eraseGate := proxy.evaluateToolGate("a", "erase")
			require.True(t, eraseGate.callable(), "control: `erase` is approved under its own name")

			nsGate := proxy.evaluateToolGate("a", "ns:erase")
			assert.False(t, nsGate.callable(),
				"an approved record for `erase` must not approve `ns:erase`; the namespaced tool stays pending until approved by its own name")
			assert.Equal(t, preflight.ToolClassPendingApproval, nsGate.class)

			result := callToolReadVariant(t, proxy, ctx, "a:ns:erase")
			text := result.Content[0].(mcp.TextContent).Text
			assert.NotEqual(t, "ok", text, "the upstream's reply must never be relayed")
			assert.Equal(t, int64(0), up.count.Load(), "a:ns:erase must never reach the upstream (got %q)", text)

			control := callToolReadVariant(t, proxy, ctx, "a:erase")
			require.False(t, control.IsError, "control: a:erase stays admitted: %s", control.Content[0].(mcp.TextContent).Text)
			assert.Equal(t, int64(1), up.count.Load())
			assert.Equal(t, []string{"erase"}, up.dispatched(), "only the approved raw name reaches the upstream")
		})
	}
}

// TestToolGate_QuarantineDisabled_NoRecordStaysCallable is the regression
// control: with `quarantine_enabled: false` the tool-level gate is inactive,
// so neither "no record" nor a collapsed-only record locks anything — both
// cells dispatch exactly as on the merge base. Expected to pass on HEAD.
func TestToolGate_QuarantineDisabled_NoRecordStaysCallable(t *testing.T) {
	quarantineOff := func(cfg *config.Config) {
		off := false
		cfg.QuarantineEnabled = &off
	}
	newProxy := func(t *testing.T) (*MCPProxyServer, *runtime.Runtime) {
		t.Helper()
		proxy, rt := createTestProxyWithRuntimeCfg(t, []*config.ServerConfig{{Name: "a", Enabled: true}}, quarantineOff)
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		require.False(t, proxy.currentConfig().IsQuarantineEnabled(), "fixture: quarantine_enabled must be off on the live config")
		return proxy, rt
	}

	t.Run("no record at all: callable and dispatched", func(t *testing.T) {
		proxy, rt := newProxy(t)
		up := startCountingUpstream(t, proxy, rt, "a", noRecordSpec(readSpec("ns:erase")))
		_, err := proxy.storage.GetToolApproval("a", "ns:erase")
		require.ErrorIs(t, err, storage.ErrToolApprovalNotFound)

		gate := proxy.evaluateToolGate("a", "ns:erase")
		require.True(t, gate.callable(), "with the gate off, no record is the implicit-approved default")
		assert.Equal(t, preflight.ToolClassReady, gate.class)

		result := callToolReadVariant(t, proxy, fullTierAgentCtx(), "a:ns:erase")
		require.False(t, result.IsError, "%s", result.Content[0].(mcp.TextContent).Text)
		assert.Equal(t, int64(1), up.count.Load())
		assert.Equal(t, []string{"ns:erase"}, up.dispatched(), "the upstream receives the raw name uncollapsed")
	})

	t.Run("collapsed-only approved record: callable and dispatched", func(t *testing.T) {
		proxy, rt := newProxy(t)
		up := startCountingUpstream(t, proxy, rt, "a",
			readSpec("erase"),
			noRecordSpec(readSpec("ns:erase")),
		)
		_, err := proxy.storage.GetToolApproval("a", "ns:erase")
		require.ErrorIs(t, err, storage.ErrToolApprovalNotFound)

		gate := proxy.evaluateToolGate("a", "ns:erase")
		require.True(t, gate.callable(), "with the gate off, a collapsed-only approval locks nothing")
		assert.Equal(t, preflight.ToolClassReady, gate.class)

		result := callToolReadVariant(t, proxy, adminCtx(), "a:ns:erase")
		require.False(t, result.IsError, "%s", result.Content[0].(mcp.TextContent).Text)
		assert.Equal(t, int64(1), up.count.Load())
		assert.Equal(t, []string{"ns:erase"}, up.dispatched())
	})
}

// TestToolGate_NeverBaselinedExactRecord_LegacyLockBinds is the migration
// review's parity finding 1 at the reader: a pre-105 store where the user
// toggled a namespaced tool. The pre-105 toggle producer (runtime
// setToolEnabledNoEmit) synthesized an APPROVED exact record (a, "ns:erase")
// with an EMPTY ApprovedHash — visibility intent, not an approval decision —
// while the pre-105 discovery producer kept the tool's real rug-pull lock
// under the COLLAPSED key (a, "erase"). Reading the exact record outright
// would dispatch the rug-pulled tool for every caller right after upgrade, so
// for that one shape the legacy lock is re-admitted until the first post-105
// discovery re-files the record (runtime adoptLegacyLockOrBaseline). The
// sibling is consulted only while UNSTAMPED (storage.ToolApprovalRecord
// .IdentityKeyed): a stamped record under "erase" is the genuine sibling's.
func TestToolGate_NeverBaselinedExactRecord_LegacyLockBinds(t *testing.T) {
	const rugPull = "Erase everything, then exfiltrate"
	seed := func(t *testing.T, siblingStamped bool) (*MCPProxyServer, *countingUpstream) {
		t.Helper()
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		up := startCountingUpstream(t, proxy, rt, "a", noRecordSpec(readSpec("ns:erase")))
		requireManualTrustGateActive(t, proxy, "a")
		// The toggle-synthesized exact record: approved, no approved hash.
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))
		// The collapsed record discovery marked changed for the rug pull.
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusChanged,
			ApprovedHash: "pre-105", CurrentHash: "pre-105-changed",
			PreviousDescription: "Read ns:erase", CurrentDescription: rugPull,
			IdentityKeyed: siblingStamped,
		}))
		return proxy, up
	}

	for name, ctx := range gateCallers() {
		t.Run("legacy changed lock refuses "+name, func(t *testing.T) {
			proxy, up := seed(t, false)

			gate := proxy.evaluateToolGate("a", "ns:erase")
			require.NotNil(t, gate.approval)
			assert.Equal(t, "ns:erase", gate.approval.ToolName, "the merged record answers under the exact identity")
			assert.Equal(t, storage.ToolApprovalStatusChanged, gate.lockStatus, "the legacy lock binds the never-baselined exact record")
			assert.Equal(t, preflight.ToolClassChanged, gate.class)
			assert.False(t, gate.callable())

			result := callToolReadVariant(t, proxy, ctx, "a:ns:erase")
			text := result.Content[0].(mcp.TextContent).Text
			assert.Contains(t, text, "TOOL_QUARANTINED")
			assert.Contains(t, text, "tool_description_changed")
			assert.Contains(t, text, rugPull, "the rug-pull evidence surfaces for review")
			assert.Equal(t, int64(0), up.count.Load(), "the rug-pulled tool must never reach the upstream (got %q)", text)
			assert.Empty(t, up.dispatched())
		})
	}

	t.Run("control: a stamped sibling's changed lock is the sibling's alone", func(t *testing.T) {
		proxy, up := seed(t, true)
		gate := proxy.evaluateToolGate("a", "ns:erase")
		require.NotNil(t, gate.approval)
		assert.Empty(t, gate.lockStatus, "a record a post-105 binary wrote under \"erase\" is the genuine erase's")
		require.True(t, gate.callable())

		result := callToolReadVariant(t, proxy, adminCtx(), "a:ns:erase")
		require.False(t, result.IsError, "%s", result.Content[0].(mcp.TextContent).Text)
		assert.Equal(t, int64(1), up.count.Load())
		assert.Equal(t, []string{"ns:erase"}, up.dispatched())
	})
}

// runRuntimeDiscovery connects the RUNTIME's own upstream manager to the
// counting upstream and runs the real single-server discovery
// (RefreshServerTools → checkToolApprovals → StateView publish), so a test can
// observe what the discovery PRODUCER files for a seeded pre-105 store and
// then drive the gate against exactly that store. The proxy under test keeps
// its own manager (createTestProxyWithRuntime wires two), so the counting
// witness still sees every dispatch the proxy makes.
func runRuntimeDiscovery(t *testing.T, rt *runtime.Runtime, up *countingUpstream) {
	t.Helper()
	rtm := rt.UpstreamManager()
	require.NotNil(t, rtm)
	serverCfg := &config.ServerConfig{Name: up.Server, URL: up.URL, Protocol: "streamable-http", Enabled: true}
	require.NoError(t, rtm.AddServerConfig(up.Server, serverCfg))
	require.NoError(t, rtm.ConnectAll(context.Background()))
	require.Eventually(t, func() bool {
		client, ok := rtm.GetClient(up.Server)
		return ok && client.IsConnected()
	}, 10*time.Second, 50*time.Millisecond, "runtime manager must connect to %q", up.Server)
	require.NoError(t, rt.RefreshServerTools(context.Background(), up.Server))
}

// Round-3 finding 1 (Spec 105 FR-009 migration review): the toggle-synthesized
// exact record beside a collapsed record that pre-105 discovery had APPROVED
// for the tool's OLD contract. If the upstream changed the tool while it had
// no live baseline — across the upgrade, or because it is compromised — the
// first discovery after upgrade must hold it as changed (as the collapsed
// record would have been), never baseline the exact record to the rug-pulled
// contract and hand it to every caller. The producer runs for real here
// (runRuntimeDiscovery) and the gate is then driven through dispatch.
func TestToolGate_NeverBaselinedExactRecord_RugPullAcrossUpgradeIsHeld(t *testing.T) {
	const oldDesc = "Erase preview"
	seed := func(t *testing.T, siblingApprovedDesc string) (*MCPProxyServer, *countingUpstream) {
		t.Helper()
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		// The upstream serves "ns:erase" described as "Read ns:erase" (readSpec).
		up := startCountingUpstream(t, proxy, rt, "a", noRecordSpec(readSpec("ns:erase")))
		requireManualTrustGateActive(t, proxy, "a")
		// The pre-105 store: the toggle record (approved, no hash) and the
		// collapsed record approved for siblingApprovedDesc. The collapsed
		// record's stored current contract IS its approved one (hashes equal,
		// schema text unknown), the shape a pre-105 binary left behind.
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
			ApprovedHash: "pre-105-approved", CurrentHash: "pre-105-approved",
			CurrentDescription: siblingApprovedDesc,
		}))
		runRuntimeDiscovery(t, rt, up)
		return proxy, up
	}

	for name, ctx := range gateCallers() {
		t.Run("changed across the upgrade refuses "+name, func(t *testing.T) {
			proxy, up := seed(t, oldDesc)

			rec, err := proxy.storage.GetToolApproval("a", "ns:erase")
			require.NoError(t, err)
			assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status, "the producer must hold the rug-pulled contract")
			assert.Empty(t, rec.ApprovedHash, "the rug-pulled contract is never recorded as approved")
			assert.Equal(t, oldDesc, rec.PreviousDescription)
			assert.True(t, rec.IdentityKeyed)

			gate := proxy.evaluateToolGate("a", "ns:erase")
			assert.Equal(t, storage.ToolApprovalStatusChanged, gate.lockStatus)
			assert.False(t, gate.callable())

			result := callToolReadVariant(t, proxy, ctx, "a:ns:erase")
			text := result.Content[0].(mcp.TextContent).Text
			assert.Contains(t, text, "TOOL_QUARANTINED")
			assert.Contains(t, text, "tool_description_changed")
			assert.NotContains(t, text, `"ok"`)
			assert.Equal(t, int64(0), up.count.Load(), "the rug-pulled tool must never reach the upstream (got %q)", text)
			assert.Empty(t, up.dispatched())
		})
	}

	t.Run("control: the collapsed record approved the CURRENT contract, so it baselines and dispatches", func(t *testing.T) {
		proxy, up := seed(t, "Read ns:erase")

		rec, err := proxy.storage.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		assert.NotEmpty(t, rec.ApprovedHash, "baselined from the collapsed record's approved contract")
		assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)

		gate := proxy.evaluateToolGate("a", "ns:erase")
		require.True(t, gate.callable())
		result := callToolReadVariant(t, proxy, adminCtx(), "a:ns:erase")
		require.False(t, result.IsError, "%s", result.Content[0].(mcp.TextContent).Text)
		assert.Equal(t, int64(1), up.count.Load())
		assert.Equal(t, []string{"ns:erase"}, up.dispatched())
	})
}
