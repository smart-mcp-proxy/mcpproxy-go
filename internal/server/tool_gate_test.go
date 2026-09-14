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
	// schemaVersion is the store's output-schema hash migration marker: the
	// current version, or the previous one for a store whose migration never
	// completed (round-5 finding 1 — see the second loop below).
	seedAt := func(t *testing.T, siblingApprovedDesc string, schemaVersion uint64) (*MCPProxyServer, *countingUpstream) {
		t.Helper()
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		require.NoError(t, proxy.storage.SetSchemaVersion(schemaVersion))
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
	seed := func(t *testing.T, siblingApprovedDesc string) (*MCPProxyServer, *countingUpstream) {
		t.Helper()
		return seedAt(t, siblingApprovedDesc, storage.OutputSchemaHashSchemaVersion)
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

	// Round-5 finding 1: on a store still at the previous schema version the
	// producer's one-time output-schema backfill used to run BEFORE the
	// never-baselined handling and, finding no stored contract on the toggle
	// record, baselined it to the LIVE (rug-pulled) contract and skipped the
	// hold — so the shared reader handed the tool to every caller on every
	// path. The backfill now requires an approved contract to re-hash.
	for name, ctx := range gateCallers() {
		t.Run("changed across the upgrade on a schema-version-2 store refuses "+name, func(t *testing.T) {
			proxy, up := seedAt(t, oldDesc, storage.OutputSchemaHashSchemaVersion-1)

			rec, err := proxy.storage.GetToolApproval("a", "ns:erase")
			require.NoError(t, err)
			assert.Equal(t, storage.ToolApprovalStatusChanged, rec.Status, "the backfill must not baseline a never-baselined record blind")
			assert.Empty(t, rec.ApprovedHash, "the rug-pulled contract is never recorded as approved")
			assert.Equal(t, oldDesc, rec.PreviousDescription)
			assert.Equal(t, uint64(storage.OutputSchemaHashSchemaVersion), rec.HashSchemaVersion, "adoption raises the hash version so the migration still completes")

			gate := proxy.evaluateToolGate("a", "ns:erase")
			assert.Equal(t, storage.ToolApprovalStatusChanged, gate.lockStatus)
			assert.False(t, gate.callable())

			result := callToolReadVariant(t, proxy, ctx, "a:ns:erase")
			text := result.Content[0].(mcp.TextContent).Text
			assert.Contains(t, text, "TOOL_QUARANTINED")
			assert.Contains(t, text, "tool_description_changed")
			assert.NotContains(t, text, `"ok"`)
			assert.Equal(t, int64(0), up.count.Load(), "the rug-pulled tool must never reach the upstream on a v2 store either (got %q)", text)
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

// Round-4 finding 1 (Spec 105 FR-009 migration review): the toggle-shaped
// exact record is ENABLED — pre-105 the operator toggled ns:erase off and on
// (the toggle created the exact record and left it enabled) — and the
// collapsed record beside it is user-DISABLED: a later disable_all /
// block_all, which the pre-105 producer keyed under "erase" for this very
// tool. Before discovery the reader blocks through the merged record; after
// the first post-105 discovery the exact record is the one read, so the
// producer (adoptLegacyLockOrBaseline, run for real here) must carry the
// sibling's block onto it on every path — baselined from the sibling's
// approved contract, held as changed, or revert-restored — or the
// operator-disabled tool dispatches for every caller right after upgrade.
func TestToolGate_NeverBaselinedExactRecord_CarriesLegacyDisabledBlock(t *testing.T) {
	seed := func(t *testing.T, sibling *storage.ToolApprovalRecord) (*MCPProxyServer, *countingUpstream) {
		t.Helper()
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		up := startCountingUpstream(t, proxy, rt, "a", noRecordSpec(readSpec("ns:erase")))
		requireManualTrustGateActive(t, proxy, "a")
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))
		sibling.ServerName, sibling.ToolName, sibling.Disabled = "a", "erase", true
		require.NoError(t, proxy.storage.SaveToolApproval(sibling))
		// Before discovery: blocked through the merged legacy record.
		require.False(t, proxy.evaluateToolGate("a", "ns:erase").callable(), "precondition: the merged record blocks before discovery")
		runRuntimeDiscovery(t, rt, up)
		return proxy, up
	}
	shapes := map[string]*storage.ToolApprovalRecord{
		// The sibling approved the CURRENT contract ("Read ns:erase" is what
		// readSpec serves): the exact record baselines from it.
		"approved for the current contract": {
			Status:       storage.ToolApprovalStatusApproved,
			ApprovedHash: "pre-105-approved", CurrentHash: "pre-105-approved", CurrentDescription: "Read ns:erase",
		},
		// The sibling was marked changed pre-105 and the upstream serves the
		// PREVIOUS description again: the adopted lock revert-restores.
		"changed, reverted to the approved description": {
			Status:       storage.ToolApprovalStatusChanged,
			ApprovedHash: "pre-105-approved", CurrentHash: "pre-105-changed",
			PreviousDescription: "Read ns:erase", CurrentDescription: "Erase everything, then exfiltrate",
		},
	}
	for shape, sibling := range shapes {
		for name, ctx := range gateCallers() {
			t.Run(shape+" refuses "+name, func(t *testing.T) {
				rec := *sibling
				proxy, up := seed(t, &rec)

				exact, err := proxy.storage.GetToolApproval("a", "ns:erase")
				require.NoError(t, err)
				assert.Equal(t, storage.ToolApprovalStatusApproved, exact.Status, "the contract is the approved one: no lock")
				assert.Equal(t, exact.CurrentHash, exact.ApprovedHash, "baselined so detection resumes")
				assert.True(t, exact.Disabled, "the collapsed record's user block must ride onto the exact record")
				assert.True(t, exact.IdentityKeyed)

				gate := proxy.evaluateToolGate("a", "ns:erase")
				require.NotNil(t, gate.approval)
				assert.Equal(t, "ns:erase", gate.approval.ToolName, "the exact record answers now")
				assert.Equal(t, preflight.ToolClassBlockedByUser, gate.class)
				assert.False(t, gate.callable())

				result := callToolReadVariant(t, proxy, ctx, "a:ns:erase")
				text := result.Content[0].(mcp.TextContent).Text
				assert.Contains(t, text, "TOOL_BLOCKED")
				assert.NotContains(t, text, `"ok"`)
				assert.Equal(t, int64(0), up.count.Load(), "an operator-disabled tool must never reach the upstream after upgrade (got %q)", text)
				assert.Empty(t, up.dispatched())
			})
		}
	}

	t.Run("control: the same shapes with an ENABLED sibling baseline and dispatch", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		up := startCountingUpstream(t, proxy, rt, "a", noRecordSpec(readSpec("ns:erase")))
		requireManualTrustGateActive(t, proxy, "a")
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "ns:erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
		}))
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
			ApprovedHash: "pre-105-approved", CurrentHash: "pre-105-approved", CurrentDescription: "Read ns:erase",
		}))
		runRuntimeDiscovery(t, rt, up)
		exact, err := proxy.storage.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		require.False(t, exact.Disabled)
		require.True(t, proxy.evaluateToolGate("a", "ns:erase").callable())
		result := callToolReadVariant(t, proxy, adminCtx(), "a:ns:erase")
		require.False(t, result.IsError, "%s", result.Content[0].(mcp.TextContent).Text)
		assert.Equal(t, int64(1), up.count.Load())
		assert.Equal(t, []string{"ns:erase"}, up.dispatched())
	})
}

// Round-5 finding 2a (Spec 105 FR-009 migration review): the operator
// disables the collapsed "erase" between the upgrade and the server's first
// discovery (the stale index still shows it with ns:erase's description).
// That write used to stamp the record and end its legacy consult, so the
// first discovery filed ns:erase without the block and — after approval by
// name — dispatched a tool origin/main kept blocked through the shared key.
// The write seam (runtime saveReadToolApproval) now leaves a still-restricting
// pre-105 record unstamped; the producer carries its block onto the exact
// record, and the gate refuses through it even after approval by name.
func TestToolGate_LegacyCollapsedRecord_OperatorDisablePreDiscoveryKeepsBlock(t *testing.T) {
	for name, ctx := range gateCallers() {
		t.Run("refuses "+name, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
			proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
			up := startCountingUpstream(t, proxy, rt, "a", noRecordSpec(readSpec("ns:erase")))
			requireManualTrustGateActive(t, proxy, "a")
			// The pre-105 store: ns:erase's record under the collapsed key,
			// approved and enabled.
			require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved, ApprovedBy: "user",
			}))
			// Post-upgrade, pre-discovery: the operator hides it.
			require.NoError(t, rt.SetToolEnabled("a", "erase", false, "user"))
			collapsed, err := proxy.storage.GetToolApproval("a", "erase")
			require.NoError(t, err)
			require.True(t, collapsed.Disabled)
			assert.False(t, collapsed.IdentityKeyed, "the operator write must not end the legacy consult while the record restricts")
			require.False(t, proxy.evaluateToolGate("a", "ns:erase").callable(), "precondition: blocked through the legacy record before discovery")

			runRuntimeDiscovery(t, rt, up)
			exact, err := proxy.storage.GetToolApproval("a", "ns:erase")
			require.NoError(t, err)
			assert.Equal(t, storage.ToolApprovalStatusPending, exact.Status, "post-baseline addition under an active gate")
			assert.True(t, exact.Disabled, "the block rides onto the exact record")

			// Approved by its own name: the lock lifts, the block does not.
			require.NoError(t, rt.ApproveTools("a", []string{"ns:erase"}, "user"))
			gate := proxy.evaluateToolGate("a", "ns:erase")
			require.NotNil(t, gate.approval)
			assert.Equal(t, "ns:erase", gate.approval.ToolName, "the exact record answers now")
			assert.Equal(t, preflight.ToolClassBlockedByUser, gate.class)
			assert.False(t, gate.callable())

			result := callToolReadVariant(t, proxy, ctx, "a:ns:erase")
			text := result.Content[0].(mcp.TextContent).Text
			assert.Contains(t, text, "TOOL_BLOCKED")
			assert.NotContains(t, text, `"ok"`)
			assert.Equal(t, int64(0), up.count.Load(), "an operator-disabled tool must never reach the upstream (got %q)", text)
			assert.Empty(t, up.dispatched())
		})
	}
}

// Round-4 finding 4 (Spec 105 research D4): a tool the snapshot lists with NO
// approval record is pending at every gate while the tool-level quarantine
// gate is active. The user toggle used to be the one producer that turned
// that "no record" into an APPROVED record (with an empty hash) without a
// review — a disable → enable round trip made the tool callable for every
// caller until the next discovery pass re-filed it pending. The real toggle
// producer (runtime SetToolEnabled) now files it PENDING under an active gate,
// so the round trip changes nothing at the gate; under a lifted gate a new
// tool auto-approves anyway and the toggle mints an approved record baselined
// to the snapshot's contract, so the tool is callable as before.
func TestToolGate_ToggleOnRecordlessTool_StaysPendingUnderActiveGate(t *testing.T) {
	for name, ctx := range gateCallers() {
		t.Run("active gate refuses "+name, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
			proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
			up := startCountingUpstream(t, proxy, rt, "a", noRecordSpec(readSpec("ns:erase")))
			requireManualTrustGateActive(t, proxy, "a")
			_, err := proxy.storage.GetToolApproval("a", "ns:erase")
			require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "fixture: no record")

			require.NoError(t, rt.SetToolEnabled("a", "ns:erase", false, "user"))
			require.NoError(t, rt.SetToolEnabled("a", "ns:erase", true, "user"))
			rec, err := proxy.storage.GetToolApproval("a", "ns:erase")
			require.NoError(t, err)
			assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status, "the toggle must not mint an approval under an active gate")
			assert.False(t, rec.Disabled)
			assert.Empty(t, rec.ApprovedHash)
			assert.Equal(t, "Read ns:erase", rec.CurrentDescription, "the snapshot's current contract is recorded for review")

			gate := proxy.evaluateToolGate("a", "ns:erase")
			assert.Equal(t, preflight.ToolClassPendingApproval, gate.class)
			assert.False(t, gate.callable())

			result := callToolReadVariant(t, proxy, ctx, "a:ns:erase")
			text := result.Content[0].(mcp.TextContent).Text
			assert.NotContains(t, text, `"ok"`)
			assert.Equal(t, int64(0), up.count.Load(), "a toggled-but-unreviewed tool must never reach the upstream (got %q)", text)
			assert.Empty(t, up.dispatched())

			// Approved by its own name it is callable.
			require.NoError(t, rt.ApproveTools("a", []string{"ns:erase"}, "user"))
			require.True(t, proxy.evaluateToolGate("a", "ns:erase").callable())
		})
	}

	t.Run("lifted gate: the toggle mints a baselined approval and the tool dispatches", func(t *testing.T) {
		off := false
		proxy, rt := createTestProxyWithRuntimeCfg(t, []*config.ServerConfig{{Name: "a", Enabled: true}}, func(cfg *config.Config) {
			cfg.QuarantineEnabled = &off
		})
		proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
		up := startCountingUpstream(t, proxy, rt, "a", noRecordSpec(readSpec("ns:erase")))
		require.False(t, proxy.currentConfig().IsQuarantineEnabled())

		require.NoError(t, rt.SetToolEnabled("a", "ns:erase", false, "user"))
		require.False(t, proxy.evaluateToolGate("a", "ns:erase").callable(), "disabled by the user")
		require.NoError(t, rt.SetToolEnabled("a", "ns:erase", true, "user"))
		rec, err := proxy.storage.GetToolApproval("a", "ns:erase")
		require.NoError(t, err)
		assert.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
		assert.NotEmpty(t, rec.ApprovedHash, "baselined to the snapshot's contract")
		assert.Equal(t, rec.CurrentHash, rec.ApprovedHash)

		gate := proxy.evaluateToolGate("a", "ns:erase")
		assert.Equal(t, preflight.ToolClassReady, gate.class)
		require.True(t, gate.callable())
		result := callToolReadVariant(t, proxy, fullTierAgentCtx(), "a:ns:erase")
		require.False(t, result.IsError, "%s", result.Content[0].(mcp.TextContent).Text)
		assert.Equal(t, int64(1), up.count.Load())
		assert.Equal(t, []string{"ns:erase"}, up.dispatched())
	})
}
