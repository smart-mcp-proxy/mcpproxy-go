package runtime

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// seedStateViewServer registers a server in the supervisor StateView with the
// given tool set, simulating a connected server whose StateView per-server tool
// cache holds exactly `tools` (possibly empty).
func seedStateViewServer(t *testing.T, rt *Runtime, name string, connected bool, tools []stateview.ToolInfo) {
	t.Helper()
	sv := rt.supervisor.StateView()
	if sv == nil {
		t.Fatal("StateView not available on test runtime")
	}
	sv.UpdateServer(name, func(status *stateview.ServerStatus) {
		status.Connected = connected
		status.State = "ready"
		status.Tools = tools
		status.ToolCount = len(tools)
	})
}

// TestGetServerTools_FallsBackToIndexWhenStateViewEmpty reproduces MCP-2083:
// after approving (unquarantining) a server, the per-server StateView tool list
// is transiently cleared by the disconnect/reconnect cycle, but the durable
// search index still holds the server's tools. GetServerTools must never serve
// an empty list when the index has tools for a known server.
func TestGetServerTools_FallsBackToIndexWhenStateViewEmpty(t *testing.T) {
	rt := newTestRuntime(t)

	const server = "com.googleapis.sqladmin/mcp"
	// Index holds 2 tools (analogue of the 15 indexed after approval).
	if err := rt.indexManager.BatchIndexTools([]*config.ToolMetadata{
		{ServerName: server, Name: "list_instances", Description: "List instances", ParamsJSON: `{"type":"object"}`},
		{ServerName: server, Name: "get_instance", Description: "Get instance"},
	}); err != nil {
		t.Fatalf("failed to seed index: %v", err)
	}

	// StateView has the server present and connected, but with an empty tool list
	// (the bug state right after the unquarantine reconnect).
	seedStateViewServer(t, rt, server, true, nil)

	tools, err := rt.GetServerTools(server)
	if err != nil {
		t.Fatalf("GetServerTools returned error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected fallback to index (2 tools), got %d: %#v", len(tools), tools)
	}

	got := map[string]bool{}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		got[name] = true
		if sn, _ := tool["server_name"].(string); sn != server {
			t.Fatalf("expected server_name %q, got %q", server, sn)
		}
	}
	for _, want := range []string{"list_instances", "get_instance"} {
		if !got[want] {
			t.Fatalf("missing tool %q in fallback result: %#v", want, got)
		}
	}
}

// TestGetServerTools_PrefersStateViewWhenPopulated guards against the fallback
// overriding a populated StateView: when StateView already has tools, the index
// is not consulted (StateView is the freshest source).
func TestGetServerTools_PrefersStateViewWhenPopulated(t *testing.T) {
	rt := newTestRuntime(t)

	const server = "srv"
	if err := rt.indexManager.BatchIndexTools([]*config.ToolMetadata{
		{ServerName: server, Name: "stale_indexed_tool", Description: "stale"},
	}); err != nil {
		t.Fatalf("failed to seed index: %v", err)
	}

	seedStateViewServer(t, rt, server, true, []stateview.ToolInfo{
		{Name: "live_a", Description: "A"},
		{Name: "live_b", Description: "B"},
	})

	tools, err := rt.GetServerTools(server)
	if err != nil {
		t.Fatalf("GetServerTools returned error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected StateView tools (2), got %d: %#v", len(tools), tools)
	}
	for _, tool := range tools {
		if name, _ := tool["name"].(string); name == "stale_indexed_tool" {
			t.Fatalf("fallback incorrectly overrode a populated StateView with stale index data")
		}
	}
}

// TestGetServerTools_EmptyStateViewAndIndexReturnsEmpty ensures a genuinely
// empty server (no StateView tools, no index tools) still reports empty — the
// fallback must not invent tools.
func TestGetServerTools_EmptyStateViewAndIndexReturnsEmpty(t *testing.T) {
	rt := newTestRuntime(t)

	const server = "empty-srv"
	seedStateViewServer(t, rt, server, true, nil)

	tools, err := rt.GetServerTools(server)
	if err != nil {
		t.Fatalf("GetServerTools returned error: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected empty result for a server with no tools, got %d: %#v", len(tools), tools)
	}
}
