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
