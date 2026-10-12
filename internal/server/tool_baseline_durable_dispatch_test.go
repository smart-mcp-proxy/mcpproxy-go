package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 117 PR-1 (#1567, T104s): a trusted server auto-baselined with a
// read-only tool replaces its inventory with a destructive one. Repeated real
// discovery must keep the new tool pending, and call_tool_destructive must be
// refused without any upstream dispatch. On main the second refresh promoted
// it by "auto-baseline" and the call reached the upstream.
func TestToolBaselineDurable_ReplacedInventoryNeverDispatches(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
	proxy.config.IntentDeclaration = &config.IntentDeclarationConfig{StrictServerValidation: false}
	up := startCountingUpstream(t, proxy, rt, "a", noRecordSpec(readSpec("read_note")))
	requireManualTrustGateActive(t, proxy, "a")
	runRuntimeDiscovery(t, proxy, rt, up)

	rec, err := proxy.storage.GetToolApproval("a", "read_note")
	require.NoError(t, err)
	require.Equal(t, storage.ToolApprovalStatusApproved, rec.Status)
	require.Equal(t, "auto-baseline", rec.ApprovedBy, "fixture: the first inventory is the trusted baseline")

	// The upstream replaces its whole inventory with a destructive tool.
	up.mcpSrv.DeleteTools("read_note")
	up.Tools = nil
	up.serve(noRecordSpec(destructiveSpec("wipe_all_notes")))

	for i := 0; i < 3; i++ {
		require.NoError(t, rt.RefreshServerTools(context.Background(), "a"))
		rebindDiscoveryToProxyConnection(t, proxy, rt, "a")
		rec, err := proxy.storage.GetToolApproval("a", "wipe_all_notes")
		require.NoError(t, err, "refresh %d", i+1)
		assert.Equal(t, storage.ToolApprovalStatusPending, rec.Status,
			"refresh %d: the replacement must stay pending (approved_by=%q)", i+1, rec.ApprovedBy)
	}

	assert.False(t, proxy.evaluateToolGate("a", "wipe_all_notes").callable())
	req := mcp.CallToolRequest{}
	req.Params.Name = contracts.ToolVariantDestructive
	req.Params.Arguments = map[string]interface{}{"name": "a:wipe_all_notes"}
	result, err := proxy.handleCallToolVariant(fullTierAgentCtx(), req, contracts.ToolVariantDestructive)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Content)
	text := result.Content[0].(mcp.TextContent).Text
	assert.True(t, result.IsError, "the unreviewed destructive tool must be refused: %s", text)
	assert.Equal(t, int64(0), up.count.Load(), "no upstream dispatch (got %q)", text)
	assert.Empty(t, up.dispatched())
}
