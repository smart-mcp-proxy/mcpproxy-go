package server

import (
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRetrieveToolsQualifiedIDGates(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	enabled := true
	proxy.config.QuarantineEnabled = &enabled
	for _, server := range []config.ServerConfig{{Name: "orchid-hub", Enabled: true}, {Name: "cedar", Enabled: true}, {Name: "quarantine", Enabled: true, Quarantined: true}} {
		s := server
		require.NoError(t, proxy.storage.SaveUpstreamServer(&s))
	}
	for _, v := range []struct{ server, name string }{{"orchid-hub", "read_item"}, {"orchid-hub", "blocked_item"}, {"orchid-hub", "pending_item"}, {"cedar", "read_item"}, {"quarantine", "read_item"}} {
		require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{ServerName: v.server, Name: v.name, Description: "A neutral item.", ParamsJSON: "{}"}))
	}
	for _, v := range []struct{ server, name string }{{"orchid-hub", "read_item"}, {"cedar", "read_item"}} {
		require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{ServerName: v.server, ToolName: v.name, Status: storage.ToolApprovalStatusApproved}))
	}
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{ServerName: "orchid-hub", ToolName: "blocked_item", Status: storage.ToolApprovalStatusApproved, Disabled: true}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{ServerName: "orchid-hub", ToolName: "pending_item", Status: storage.ToolApprovalStatusPending}))
	for _, tc := range []struct {
		query string
		want  bool
	}{{"orchid-hub:read_item", true}, {"orchid-hub:blocked_item", false}, {"orchid-hub:pending_item", false}, {"quarantine:read_item", false}} {
		t.Run(tc.query, func(t *testing.T) {
			resp := callRetrieveScoped(t, proxy, adminCtx(), map[string]interface{}{"query": tc.query, "limit": float64(1)})
			if tc.want {
				require.Contains(t, toolNamesOf(resp), tc.query)
			} else {
				require.NotContains(t, toolNamesOf(resp), tc.query)
			}
		})
	}
	// A lexical decoy can score above the new exact match. The endpoint must
	// keep the exact ID first through its promoted ranking score.
	require.NoError(t, proxy.index.IndexTool(&config.ToolMetadata{ServerName: "cedar", Name: "decoy", Description: "orchid-hub:read_item", ParamsJSON: "{}"}))
	require.NoError(t, proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{ServerName: "cedar", ToolName: "decoy", Status: storage.ToolApprovalStatusApproved}))
	ranked := callRetrieveScoped(t, proxy, adminCtx(), map[string]interface{}{"query": "orchid-hub:read_item", "limit": float64(10)})
	require.Equal(t, "orchid-hub:read_item", toolNamesOf(ranked)[0])
	filtered := callRetrieveScoped(t, proxy, adminCtx(), map[string]interface{}{"query": "orchid-hub:read_item", "limit": float64(1), "read_only_only": true})
	require.NotContains(t, toolNamesOf(filtered), "orchid-hub:read_item") // No read-only annotation.

	scoped := callRetrieveScoped(t, proxy, agentCtx([]string{"cedar"}, []string{auth.PermRead}, ""), map[string]interface{}{"query": "orchid-hub:read_item", "limit": float64(1)})
	require.NotContains(t, toolNamesOf(scoped), "orchid-hub:read_item")
}

func TestRetrieveToolsQualifiedIDProfilePolicy(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)
	readonly := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "work-readonly"), "github:create_issue", 5)
	require.Empty(t, readonly.Tools)
	require.NotNil(t, readonly.HiddenByProfile)
	require.Equal(t, 1, *readonly.HiddenByProfile)
	full := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "work-full"), "github:create_issue", 5)
	require.NotEmpty(t, full.Tools)
}

func TestRetrieveToolsQualifiedIDRefillsAfterGates(t *testing.T) {
	for _, state := range []string{"disabled", "pending", "quarantine"} {
		t.Run(state, func(t *testing.T) {
			p := createTestMCPProxyServer(t)
			enabled := true
			p.config.QuarantineEnabled = &enabled
			require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{Name: "orchid", Enabled: true, Quarantined: state == "quarantine"}))
			require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{Name: "cedar", Enabled: true}))
			require.NoError(t, p.index.IndexTool(&config.ToolMetadata{ServerName: "orchid", Name: "read_item", Description: "Neutral"}))
			require.NoError(t, p.index.IndexTool(&config.ToolMetadata{ServerName: "cedar", Name: "fallback", Description: "orchid:read_item"}))
			status := storage.ToolApprovalStatusApproved
			if state == "pending" {
				status = storage.ToolApprovalStatusPending
			}
			require.NoError(t, p.storage.SaveToolApproval(&storage.ToolApprovalRecord{ServerName: "orchid", ToolName: "read_item", Status: status, Disabled: state == "disabled"}))
			require.NoError(t, p.storage.SaveToolApproval(&storage.ToolApprovalRecord{ServerName: "cedar", ToolName: "fallback", Status: storage.ToolApprovalStatusApproved}))
			resp := callRetrieveScoped(t, p, adminCtx(), map[string]interface{}{"query": "orchid:read_item", "limit": float64(1)})
			require.Equal(t, []string{"cedar:fallback"}, toolNamesOf(resp))
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]interface{}{"query": "orchid:read_item", "limit": float64(1), "include_disabled": true}
			result, err := p.handleRetrieveTools(adminCtx(), req)
			require.NoError(t, err)
			decoded := decodeRetrieve(t, result)
			require.Len(t, decoded.Disabled, 1)
			require.Equal(t, "orchid:read_item", decoded.Disabled[0].Name)
		})
	}
}

func TestRetrieveToolsQualifiedIDAnnotationRefills(t *testing.T) {
	p, _ := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, p)
	require.NoError(t, p.index.IndexTool(&config.ToolMetadata{ServerName: "github", Name: "list_issues", Description: "github:create_issue", ParamsJSON: "{}"}))
	require.NoError(t, p.index.RebuildProfileFromShared("work-full", []string{"github", "notion", "filesystem"}))
	resp := callRetrieveScoped(t, p, urlProfileCtx(p, "work-full"), map[string]interface{}{"query": "github:create_issue", "limit": float64(1), "read_only_only": true})
	require.Equal(t, []string{"github:list_issues"}, toolNamesOf(resp))
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": "github:create_issue", "limit": float64(1), "read_only_only": true}
	result, err := p.handleRetrieveTools(urlProfileCtx(p, "work-full"), req)
	require.NoError(t, err)
	var body struct {
		Diagnostics filterDiagnostics `json:"filter_diagnostics"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &body))
	require.Equal(t, 1, body.Diagnostics.OmittedTotal)
	require.Equal(t, 1, body.Diagnostics.OmittedByFilter["read_only_only"].Explicit)
}
