package server

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 109-m (T144 last clause, M4): the MCP field names equal the REST field
// names. Values are already pinned per surface; this file pins the KEY SETS,
// which is what "same field names" means. Review payloads are pinned against
// the composer both surfaces serialize, catalog results against
// registries.CatalogResult, and `health` against contracts.HealthStatus.

// p109JSONTags returns the JSON object keys a struct type declares.
func p109JSONTags(t reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		tag := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" {
			keys[tag] = true
		}
	}
	return keys
}

func p109SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestSpec109MCPRESTNames_ReviewTools: MCP inspect_quarantined and
// inspect_tools expose, per tool, exactly the keys of the review payload that
// GET /api/v1/servers/{id}/review serves (both serialize runtime.ReviewTool).
func TestSpec109MCPRESTNames_ReviewTools(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "github", Enabled: true, Quarantined: true}})
	require.NoError(t, rt.StorageManager().SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true, Quarantined: true}))
	readOnly := false
	previousReadOnly := true
	require.NoError(t, rt.StorageManager().SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "delete_issue", Status: storage.ToolApprovalStatusChanged,
		CurrentHash: "current", CurrentDescription: "Deletes an issue", PreviousDescription: "Lists issues",
		CurrentSchema: `{"type":"object"}`, PreviousSchema: `{"type":"object"}`,
		CurrentAnnotations:  &config.ToolAnnotations{ReadOnlyHint: &readOnly},
		PreviousAnnotations: &config.ToolAnnotations{ReadOnlyHint: &previousReadOnly},
	}))

	review, err := rt.GetServerReview(context.Background(), "github")
	require.NoError(t, err)
	require.Len(t, review.Tools, 1)
	canonical, err := json.Marshal(review.Tools[0])
	require.NoError(t, err)
	var restKeys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(canonical, &restKeys))
	for _, field := range []string{"tier", "annotations", "scan_verdict", "approval_status", "previous", "diff"} {
		require.Contains(t, restKeys, field, "the REST review tool must name %q", field)
	}

	for name, call := range map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error){
		"inspect_quarantined": proxy.handleInspectQuarantinedTools,
		"inspect_tools":       proxy.handleInspectToolApprovals,
	} {
		result, err := call(context.Background(), quarantineRequest(map[string]interface{}{"name": "github"}))
		require.NoError(t, err, name)
		require.False(t, result.IsError, "%s: %v", name, result.Content)
		var payload struct {
			Tools []map[string]json.RawMessage `json:"tools"`
		}
		require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &payload), name)
		require.Len(t, payload.Tools, 1, name)
		for _, field := range []string{"tier", "annotations", "scan_verdict", "approval_status", "previous", "diff"} {
			assert.Contains(t, payload.Tools[0], field, "%s must use the REST review name %q", name, field)
		}
		// inspect_quarantined serializes the composer directly, so its key set IS
		// the REST key set; inspect_tools adds legacy approval-record keys beside
		// the canonical ones and is pinned by name only (above).
		if name == "inspect_quarantined" {
			assert.Equal(t, p109SortedKeys(restKeys), p109SortedKeys(payload.Tools[0]), "inspect_quarantined must expose exactly the REST review key set")
		}
	}
}

// TestSpec109MCPRESTNames_Catalog: search_servers items carry the catalog
// result names of GET /api/v1/catalog/search, minus the REST-only `added`.
func TestSpec109MCPRESTNames_Catalog(t *testing.T) {
	withMCPCatalogFixture(t)
	proxy := createTestMCPProxyServer(t)
	result, err := proxy.handleSearchServers(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "search_servers", Arguments: map[string]interface{}{"search": "Alpha"},
	}})
	require.NoError(t, err)
	require.False(t, result.IsError, "%+v", result.Content)

	var payload struct {
		Servers []map[string]json.RawMessage `json:"servers"`
	}
	require.NoError(t, json.Unmarshal([]byte(toolResultText(t, result)), &payload))
	require.Len(t, payload.Servers, 1)
	item := payload.Servers[0]

	catalogKeys := p109JSONTags(reflect.TypeOf(registries.CatalogResult{}))
	require.True(t, catalogKeys["added"], "the REST DTO carries added")
	assert.NotContains(t, item, "added", "added is REST-only")
	for _, key := range []string{"source", "id", "title", "verified", "official", "publisher"} {
		require.True(t, catalogKeys[key], "registries.CatalogResult must name %q", key)
		assert.Contains(t, item, key, "search_servers must use the REST catalog name %q", key)
	}
}

// TestSpec109MCPRESTNames_Health: the `health` object of upstream_servers list
// uses contracts.HealthStatus names and nothing else.
func TestSpec109MCPRESTNames_Health(t *testing.T) {
	proxy := createTestMCPProxyServer(t)
	add := mcp.CallToolRequest{}
	add.Params.Name = "upstream_servers"
	add.Params.Arguments = map[string]interface{}{"operation": "add", "name": "names-server", "command": "npx", "args": []interface{}{"-y", "x"}, "enabled": false}
	res, err := proxy.handleUpstreamServers(context.Background(), add)
	require.NoError(t, err)
	require.False(t, res.IsError)

	list := mcp.CallToolRequest{}
	list.Params.Name = "upstream_servers"
	list.Params.Arguments = map[string]interface{}{"operation": "list"}
	res, err = proxy.handleUpstreamServers(context.Background(), list)
	require.NoError(t, err)
	payload := toolResultJSON(t, res)
	var health map[string]interface{}
	for _, s := range payload["servers"].([]interface{}) {
		if srv := s.(map[string]interface{}); srv["name"] == "names-server" {
			health, _ = srv["health"].(map[string]interface{})
		}
	}
	require.NotNil(t, health)

	want := p109JSONTags(reflect.TypeOf(contracts.HealthStatus{}))
	for _, key := range []string{"level", "admin_state", "summary", "status", "usable", "actions"} {
		require.True(t, want[key], "contracts.HealthStatus must name %q", key)
		assert.Contains(t, health, key)
	}
	for key := range health {
		assert.True(t, want[key], "health key %q is not a contracts.HealthStatus name (have %v)", key, p109SortedKeys(want))
	}
}
