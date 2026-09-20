package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func TestDirectToolCallabilityBlock_ServerDisabled(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: false}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "list_repos", map[string]interface{}{})
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Tool is disabled")
}

func TestDirectToolCallabilityBlock_ServerQuarantined(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true, Quarantined: true}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "list_repos", map[string]interface{}{"q": "x"})
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	var response map[string]interface{}
	text := result.Content[0].(mcp.TextContent).Text
	require.NoError(t, json.Unmarshal([]byte(text), &response))
	assert.Equal(t, "QUARANTINED_SERVER_BLOCKED", response["status"])
	assert.Equal(t, "github", response["serverName"])
	assert.Equal(t, "list_repos", response["toolName"])

	// The remediation must point at operations the agent can actually execute:
	// list_quarantined/inspect_quarantined live on quarantine_security, and
	// upstream_servers rejects them with "Unknown operation".
	instructions, _ := response["instructions"].(string)
	assert.Contains(t, instructions, "quarantine_security")
	assert.NotContains(t, instructions, "'upstream_servers' tool with operation 'list_quarantined'")
}

func TestDirectToolCallabilityBlock_ConfigDeniedTool(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:          "github",
		Enabled:       true,
		DisabledTools: []string{"delete_repo"},
	}))
	// A pre-existing APPROVED record isolates this test's target: the tool
	// case (config-denied) from the approval-lock gate. Without one, a fresh
	// direct-mode evaluation with no approval record at all synthesizes an
	// implicit "pending" record while the quarantine gate is active (Spec 105
	// FR-009), and — per PR #1326 review round 2 finding #1 — the approval
	// lock now correctly wins over a plain config denial, matching the
	// established handleCallToolVariant/handleCallTool precedence
	// (toolGate.lockStatus checked before the generic/config-denied block).
	// That combined scenario is covered by
	// TestDirectBlockReasonKey_AgreesWithResponse_ConfigDeniedAndApprovalLocked
	// in preflight_telemetry_test.go; this test isolates the config-denied
	// response body in the case that ambiguity does not arise.
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "delete_repo",
		Status:     storage.ToolApprovalStatusApproved,
	}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "delete_repo", map[string]interface{}{})
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "NOT user-overridable")
}

func TestDirectToolCallabilityBlock_DisabledTool(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:          "github",
		Enabled:       true,
		DisabledTools: []string{"config_disabled"},
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "delete_repo",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "delete_repo", map[string]interface{}{})
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Tool is disabled")
}

func TestDirectToolCallabilityBlock_PendingApproval(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:         "github",
		ToolName:           "new_tool",
		Status:             storage.ToolApprovalStatusPending,
		CurrentDescription: "new capability",
	}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "new_tool", map[string]interface{}{})
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	var response map[string]interface{}
	text := result.Content[0].(mcp.TextContent).Text
	require.NoError(t, json.Unmarshal([]byte(text), &response))
	assert.Equal(t, "TOOL_QUARANTINED", response["status"])
	assert.Equal(t, "github", response["server_name"])
	assert.Equal(t, "new_tool", response["tool_name"])
	assert.Equal(t, "new_unapproved_tool", response["reason"])
	assert.Contains(t, response["message"], "has not been approved")
	assert.Equal(t, "new capability", response["current_description"])
	assert.Contains(t, response["action"], "/api/v1/servers/github/tools/approve")
}

func TestDirectToolCallabilityBlock_ChangedApproval(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName:          "github",
		ToolName:            "mutated_tool",
		Status:              storage.ToolApprovalStatusChanged,
		PreviousDescription: "old",
		CurrentDescription:  "new",
	}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "mutated_tool", map[string]interface{}{})
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	var response map[string]interface{}
	text := result.Content[0].(mcp.TextContent).Text
	require.NoError(t, json.Unmarshal([]byte(text), &response))
	assert.Equal(t, "TOOL_QUARANTINED", response["status"])
	assert.Equal(t, "github", response["server_name"])
	assert.Equal(t, "mutated_tool", response["tool_name"])
	assert.Equal(t, "tool_description_changed", response["reason"])
	assert.Contains(t, response["message"], "description has changed")
	assert.Equal(t, "old", response["previous_description"])
	assert.Equal(t, "new", response["current_description"])
	assert.Contains(t, response["action"], "/api/v1/servers/github/tools/approve")
}

func TestDirectToolCallabilityBlock_ApprovedToolAllowed(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "list_repos",
		Status:     storage.ToolApprovalStatusApproved,
	}))

	result := proxy.directToolCallabilityBlock(context.Background(), "github", "list_repos", map[string]interface{}{})
	assert.Nil(t, result)
}

func TestFilterDirectToolsForAgentCallability_AgentOnly(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	require.NoError(t, proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name:          "github",
		Enabled:       true,
		DisabledTools: []string{"config_disabled"},
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "allowed",
		Status:     storage.ToolApprovalStatusApproved,
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "disabled",
		Status:     storage.ToolApprovalStatusApproved,
		Disabled:   true,
	}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "pending",
		Status:     storage.ToolApprovalStatusPending,
	}))

	tools := []mcp.Tool{
		{Name: FormatDirectToolName("github", "allowed")},
		{Name: FormatDirectToolName("github", "disabled")},
		{Name: FormatDirectToolName("github", "pending")},
		{Name: FormatDirectToolName("github", "config_disabled")},
	}

	// Spec 102: the filter resolves through the published catalog, and since
	// T025 the constructor publishes an EMPTY one at init. Handing the filter
	// tools that are absent from the catalog is no longer a realistic state —
	// in production a tool reaching a filter came from the registry, which
	// SetTools populates alongside the catalog — and an empty catalog correctly
	// denies every name (D13 rule 2). Publish the catalog these tools belong to,
	// so the test exercises CALLABILITY rather than catalog membership.
	publishPermsCatalog(proxy, map[string]string{
		FormatDirectToolName("github", "allowed"):         auth.PermRead,
		FormatDirectToolName("github", "disabled"):        auth.PermRead,
		FormatDirectToolName("github", "pending"):         auth.PermRead,
		FormatDirectToolName("github", "config_disabled"): auth.PermRead,
	})

	agentCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "agent",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})

	filtered := proxy.filterDirectToolsForAgentCallability(agentCtx, tools)
	assert.Equal(t, []string{FormatDirectToolName("github", "allowed")}, directCallabilityToolNamesForTest(filtered))

	assert.Equal(t, tools, proxy.filterDirectToolsForAgentCallability(context.Background(), tools))
}

func directCallabilityToolNamesForTest(tools []mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

// Spec 105 FR-009 gap G3 (task T006): direct-name dispatch reads the approval
// record under the EXACT raw name only and classifies "no record" as ready. A
// tool the discovery snapshot contains whose approval identity was collapsed
// by the pre-105 producer — a pending record filed under "erase" for the raw
// tool "ns:erase" — is therefore refused as pending on the retrieve surface
// (evaluateExactToolGate merges both keys) but ADMITTED under "a__ns:erase" on
// /mcp/all, straight through to the upstream.
//
// FR-009: "a pending approval for ns:erase is a pending approval for ns:erase,
// and 'no record found' is never classified as callable for a tool the
// discovery snapshot contains"; "while the quarantine gate is active for a
// server, 'no approval record' for a tool its snapshot contains is pending,
// never ready, at every gate site". The handler under test is the REAL
// registered direct-mode handler built from the catalog entry the listing
// would publish, and the oracle is the counting upstream: a refusal is proven
// by zero invocations, not by the shape of an error string.
//
// Both the collapsed-record cell and the bare no-record cell are driven so a
// fix that only merges the collapsed key (and still fails open on a truly
// absent record) cannot pass by accident.
func TestDirectDispatch_CollapsedOrAbsentApprovalIsPendingNotReady(t *testing.T) {
	fullTier := []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}

	type cell struct {
		name string
		ctx  context.Context
	}
	callers := []cell{
		{name: "full-tier unrestricted agent", ctx: agentCtx([]string{"*"}, fullTier, "")},
		// SC-005 names FR-009's exact-identity gate outcomes as an
		// administrator exception: the quarantine lock on ns:erase binds the
		// administrator exactly as a pending record under the exact name
		// already does on HEAD (TestDirectToolCallabilityBlock_PendingApproval
		// runs with no auth context at all).
		{name: "administrator", ctx: adminCtx()},
	}

	// seed builds a fresh proxy whose server "a" exposes read-tier "erase"
	// (pending under its own name) and destructive "ns:erase" with NO record
	// under its own name — the state the pre-105 discovery producer leaves
	// behind, where the only record for the raw tool "ns:erase" is the one
	// collapsed onto "erase". The explicit delete pins that no other seam
	// (fixture defaults, discovery) filed an exact-name record.
	seed := func(t *testing.T) (*MCPProxyServer, *countingUpstream) {
		t.Helper()
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		erase := readSpec("erase")
		erase.Approval = storage.ToolApprovalStatusPending
		nsErase := destructiveSpec("ns:erase")
		nsErase.NoRecord = true
		up := startCountingUpstream(t, proxy, rt, "a", erase, nsErase)
		require.NoError(t, proxy.storage.DeleteToolApproval("a", "ns:erase"))
		_, err := proxy.storage.GetToolApproval("a", "ns:erase")
		require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "precondition: no exact-name record for ns:erase")
		rec, err := proxy.storage.GetToolApproval("a", "erase")
		require.NoError(t, err)
		require.Equal(t, storage.ToolApprovalStatusPending, rec.Status, "precondition: the collapsed record is pending")
		require.True(t, proxy.currentConfig().IsQuarantineEnabled(), "precondition: the quarantine gate is active")
		return proxy, up
	}

	entryFor := func(tool string) *directCatalogEntry {
		return &directCatalogEntry{
			ServerName:  "a",
			ToolName:    tool,
			DisplayName: FormatDirectToolName("a", tool),
			ParamsJSON:  `{"type":"object"}`,
			Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)},
		}
	}

	// The retrieve-surface gate is the control that proves the fixture
	// really encodes a pending tool: the merged reader already refuses it.
	t.Run("control: retrieve-surface gate reads the collapsed record as pending", func(t *testing.T) {
		proxy, _ := seed(t)
		gate := proxy.evaluateExactToolGate("a", "ns:erase")
		require.NotNil(t, gate.approval, "evaluateExactToolGate merges the collapsed key")
		assert.Equal(t, "erase", gate.approval.ToolName, "the collapsed record itself, not the synthesized no-record placeholder")
		assert.Equal(t, storage.ToolApprovalStatusPending, gate.lockStatus)
		assert.False(t, gate.class.Callable())
	})

	for _, caller := range callers {
		t.Run("collapsed pending record: "+caller.name, func(t *testing.T) {
			proxy, up := seed(t)

			req := mcp.CallToolRequest{}
			req.Params.Name = FormatDirectToolName("a", "ns:erase")
			req.Params.Arguments = map[string]interface{}{}
			result, err := proxy.makeDirectModeHandler(entryFor("ns:erase"))(caller.ctx, req)
			require.NoError(t, err)
			require.NotNil(t, result)

			assertDirectDispatchRefused(t, result)
			assert.Equal(t, int64(0), up.count.Load(),
				"a__ns:erase must not reach the upstream while its only approval record is the pending one collapsed onto 'erase'")
			assert.Empty(t, up.dispatched())
		})
	}

	t.Run("absent record under an active quarantine gate is pending, not ready", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		// "erase" is approved under its own name; "ghostly" is in the
		// discovery snapshot with no record anywhere — neither exact nor
		// collapsed. FR-009's "no record → pending" rule must refuse it at
		// this gate site too; on HEAD the absent record reads as ready.
		ghost := destructiveSpec("ghostly")
		ghost.NoRecord = true
		up := startCountingUpstream(t, proxy, rt, "a", readSpec("erase"), ghost)
		_, err := proxy.storage.GetToolApproval("a", "ghostly")
		require.ErrorIs(t, err, storage.ErrToolApprovalNotFound)

		req := mcp.CallToolRequest{}
		req.Params.Name = FormatDirectToolName("a", "ghostly")
		req.Params.Arguments = map[string]interface{}{}
		result, err := proxy.makeDirectModeHandler(entryFor("ghostly"))(agentCtx([]string{"*"}, fullTier, ""), req)
		require.NoError(t, err)
		require.NotNil(t, result)

		assertDirectDispatchRefused(t, result)
		assert.Equal(t, int64(0), up.count.Load(), "a snapshot tool with no approval record is pending under an active quarantine gate")
		assert.Empty(t, up.dispatched())
	})

	// Admitted control (passes on HEAD): a tool approved under its OWN exact
	// name still dispatches, so the refusals above cannot be a broken fixture.
	t.Run("control: exact-name approved tool is dispatched", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		up := startCountingUpstream(t, proxy, rt, "a", destructiveSpec("ns:erase"))

		req := mcp.CallToolRequest{}
		req.Params.Name = FormatDirectToolName("a", "ns:erase")
		req.Params.Arguments = map[string]interface{}{}
		result, err := proxy.makeDirectModeHandler(entryFor("ns:erase"))(agentCtx([]string{"*"}, fullTier, ""), req)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.False(t, result.IsError, "approved under its own name: %s", directResultText(result))
		assert.Equal(t, []string{"ns:erase"}, up.dispatched())
	})
}

// assertDirectDispatchRefused accepts either refusal envelope direct mode
// produces: an IsError result (the generic not-callable block) or the
// TOOL_QUARANTINED review payload (pending / changed, which is deliberately
// IsError=false so agents parse it). What it rejects is the upstream's "ok".
func assertDirectDispatchRefused(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	text := directResultText(result)
	if result.IsError {
		return
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(text), &payload); err == nil && payload["status"] == "TOOL_QUARANTINED" {
		return
	}
	t.Errorf("direct dispatch must be refused (IsError or TOOL_QUARANTINED), got IsError=%v text=%q", result.IsError, text)
}

func directResultText(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	if tc, ok := result.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// Spec 105 FR-009 on the direct surface, adversarial review finding critique0
// #1: the direct catalog is built from a LIVE tools/list on every
// servers.changed — including server_connected — which races the runtime's
// own discovery + checkToolApprovals pass. In that window a newly added tool
// is in the catalog (so a handler for it is registered) but has neither an
// approval record nor a StateView entry. Feeding the classifier's Discovered
// input from the StateView classified "not discovered, no record" as READY,
// so a token holding the tool's tier executed it upstream until the runtime
// pass filed the pending record — FR009-G5 re-opened on this surface. The
// catalog IS the direct discovery snapshot: a registered entry is discovered
// by construction, and "no record" under an active gate is pending.
func TestDirectDispatch_CatalogToolAbsentFromStateView_IsPendingNotReady(t *testing.T) {
	fullTier := []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}
	for label, ctx := range map[string]context.Context{
		"read-tier a-only token (holds the tool's tier)": agentCtx([]string{"a"}, []string{auth.PermRead}, ""),
		"full-tier unrestricted agent":                   agentCtx([]string{"*"}, fullTier, ""),
		"administrator":                                  adminCtx(),
	} {
		t.Run(label, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{manualTrustServerConfig("a")})
			// The upstream serves "erase" (baselined) AND a freshly added
			// read-only "steal"; the StateView still holds only "erase" and
			// no record exists for "steal" — the reconnect race window.
			steal := readSpec("steal")
			steal.NoRecord = true
			up := startCountingUpstream(t, proxy, rt, "a", readSpec("erase"), steal)
			rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
				s.Tools = []stateview.ToolInfo{readSpec("erase").info()}
			})
			require.False(t, proxy.resolveExactToolIdentity("a", "steal").Found, "fixture: the StateView must not hold steal")
			_, err := proxy.storage.GetToolApproval("a", "steal")
			require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "fixture: no record for steal")
			serverCfg, err := proxy.storage.GetUpstreamServer("a")
			require.NoError(t, err)
			require.False(t, serverCfg.IsQuarantineSkipped(), "fixture: the quarantine gate must be active")

			entry := &directCatalogEntry{
				ServerName: "a", ToolName: "steal", DisplayName: FormatDirectToolName("a", "steal"),
				ParamsJSON: `{"type":"object"}`, Annotations: steal.Annotations,
			}
			req := mcp.CallToolRequest{}
			req.Params.Name = entry.DisplayName
			req.Params.Arguments = map[string]interface{}{}
			result, err := proxy.makeDirectModeHandler(entry)(ctx, req)
			require.NoError(t, err)
			require.NotNil(t, result)

			assertDirectDispatchRefused(t, result)
			text := directResultText(result)
			assert.Contains(t, text, "TOOL_QUARANTINED", "a catalog tool with no record is pending under the active gate: %s", text)
			assert.Contains(t, text, "no_approval_record", "the no-record body, not the stored-pending one: %s", text)
			assert.Equal(t, int64(0), up.count.Load(), "the call must never reach the upstream")
			assert.Empty(t, up.dispatched())

			// The listing filter agrees: an agent must not even see it.
			if ac := auth.AuthContextFromContext(ctx); ac != nil && ac.Type == auth.AuthTypeAgent {
				assert.False(t, proxy.directEntryCallable(ac, entry), "the listing gate must hide what dispatch refuses")
			}

			// Positive control on the same fixture: the baselined tool dispatches.
			ctlEntry := &directCatalogEntry{
				ServerName: "a", ToolName: "erase", DisplayName: FormatDirectToolName("a", "erase"),
				ParamsJSON: `{"type":"object"}`, Annotations: readSpec("erase").Annotations,
			}
			req.Params.Name = ctlEntry.DisplayName
			ctl, err := proxy.makeDirectModeHandler(ctlEntry)(ctx, req)
			require.NoError(t, err)
			assert.False(t, ctl.IsError, "control: erase is approved and must dispatch: %s", directResultText(ctl))
			assert.Equal(t, []string{"erase"}, up.dispatched())
		})
	}
}

// Spec 105 FR-009, test-quality review finding critique3 #1: the direct
// evaluator reads approval records through the shared FR-009 reader
// (lookupToolApproval — exact record wins, a legacy collapsed record RESTRICTS
// but never approves). Every other collapsed-record cell on this surface is
// also satisfied by the implicit no-record rule, so reverting getToolApproval
// to a bare exact-key GetToolApproval was invisible to the suite. This cell
// lifts the gate (trust_mode: auto, so "no record" is READY) and leaves only a
// user-Disabled collapsed record: the block must still hide a__ns:erase, and
// only the reader can do that here.
func TestDirectDispatch_LegacyCollapsedDisabledRecord_StillBlocksWithGateLifted(t *testing.T) {
	fullTier := []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}
	seed := func(t *testing.T) (*MCPProxyServer, *countingUpstream, *directCatalogEntry) {
		t.Helper()
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		nsErase := destructiveSpec("ns:erase")
		nsErase.NoRecord = true
		up := startCountingUpstream(t, proxy, rt, "a", nsErase)
		// Lift the tool-level gate for the server: with trust_mode auto an
		// absent record classifies as ready, so ONLY the legacy record's
		// Disabled flag can refuse the call.
		serverCfg, err := proxy.storage.GetUpstreamServer("a")
		require.NoError(t, err)
		serverCfg.TrustMode = string(config.TrustModeAuto)
		require.NoError(t, proxy.storage.SaveUpstreamServer(serverCfg))
		serverCfg, err = proxy.storage.GetUpstreamServer("a")
		require.NoError(t, err)
		require.True(t, serverCfg.IsQuarantineSkipped(), "fixture: the gate must be lifted")
		// The pre-105 store: the operator hid ns:erase, which the old producer
		// had filed under "erase" — approved, Disabled. No exact record.
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved, Disabled: true,
		}))
		_, err = proxy.storage.GetToolApproval("a", "ns:erase")
		require.ErrorIs(t, err, storage.ErrToolApprovalNotFound, "fixture: no exact record")
		entry := &directCatalogEntry{
			ServerName: "a", ToolName: "ns:erase", DisplayName: FormatDirectToolName("a", "ns:erase"),
			ParamsJSON: `{"type":"object"}`, Annotations: nsErase.Annotations,
		}
		return proxy, up, entry
	}

	t.Run("user-Disabled collapsed record hides a__ns:erase", func(t *testing.T) {
		proxy, up, entry := seed(t)
		req := mcp.CallToolRequest{}
		req.Params.Name = entry.DisplayName
		req.Params.Arguments = map[string]interface{}{}
		result, err := proxy.makeDirectModeHandler(entry)(agentCtx([]string{"*"}, fullTier, ""), req)
		require.NoError(t, err)
		require.NotNil(t, result)
		assertDirectDispatchRefused(t, result)
		assert.Equal(t, int64(0), up.count.Load(), "the legacy user block must keep binding the namespaced name on /mcp/all")
		assert.Empty(t, up.dispatched())
		assert.False(t, proxy.directEntryCallable(auth.AuthContextFromContext(agentCtx([]string{"*"}, fullTier, "")), entry))
	})

	t.Run("control: with the collapsed record enabled the lifted gate admits the call", func(t *testing.T) {
		proxy, up, entry := seed(t)
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusApproved,
		}))
		req := mcp.CallToolRequest{}
		req.Params.Name = entry.DisplayName
		req.Params.Arguments = map[string]interface{}{}
		result, err := proxy.makeDirectModeHandler(entry)(agentCtx([]string{"*"}, fullTier, ""), req)
		require.NoError(t, err)
		assert.False(t, result.IsError, "an approved, enabled collapsed record lends nothing and the gate is off: %s", directResultText(result))
		assert.Equal(t, []string{"ns:erase"}, up.dispatched())
	})

	t.Run("legacy changed record answers the rug-pull body under an active gate", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		nsErase := destructiveSpec("ns:erase")
		nsErase.NoRecord = true
		up := startCountingUpstream(t, proxy, rt, "a", nsErase)
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "a", ToolName: "erase", Status: storage.ToolApprovalStatusChanged,
			PreviousDescription: "before", CurrentDescription: "after",
		}))
		entry := &directCatalogEntry{
			ServerName: "a", ToolName: "ns:erase", DisplayName: FormatDirectToolName("a", "ns:erase"),
			ParamsJSON: `{"type":"object"}`, Annotations: nsErase.Annotations,
		}
		req := mcp.CallToolRequest{}
		req.Params.Name = entry.DisplayName
		req.Params.Arguments = map[string]interface{}{}
		result, err := proxy.makeDirectModeHandler(entry)(agentCtx([]string{"*"}, fullTier, ""), req)
		require.NoError(t, err)
		text := directResultText(result)
		assert.Contains(t, text, "tool_description_changed", "the collapsed record's lock is what answers, not the implicit pending rule: %s", text)
		assert.Equal(t, int64(0), up.count.Load())
	})
}
