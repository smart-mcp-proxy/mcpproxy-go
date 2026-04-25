package server

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// expectedHints describes the explicit annotation hints we expect for an
// internal tool. All three core hints (ReadOnlyHint, DestructiveHint,
// OpenWorldHint) MUST be set explicitly per the MCP spec — nil values default
// to the most permissive interpretation (destructive=true, openWorld=true,
// readOnly=false), which is rarely what an internal proxy tool intends.
type expectedHints struct {
	readOnly    bool
	destructive bool
	openWorld   bool
}

// assertExplicitHints verifies all three core annotation hints are set
// (non-nil) on the given tool and match the expected values.
func assertExplicitHints(t *testing.T, tool mcp.Tool, want expectedHints) {
	t.Helper()
	a := tool.Annotations

	require.NotNil(t, a.ReadOnlyHint, "%s: ReadOnlyHint must be explicit (non-nil) — nil defaults to false but should be set explicitly", tool.Name)
	require.NotNil(t, a.DestructiveHint, "%s: DestructiveHint must be explicit (non-nil) — nil defaults to true (permissive)", tool.Name)
	require.NotNil(t, a.OpenWorldHint, "%s: OpenWorldHint must be explicit (non-nil) — nil defaults to true (permissive)", tool.Name)

	assert.Equal(t, want.readOnly, *a.ReadOnlyHint, "%s: ReadOnlyHint mismatch", tool.Name)
	assert.Equal(t, want.destructive, *a.DestructiveHint, "%s: DestructiveHint mismatch", tool.Name)
	assert.Equal(t, want.openWorld, *a.OpenWorldHint, "%s: OpenWorldHint mismatch", tool.Name)
}

// findTool locates a tool by name in a slice of mcp.Tool.
func findTool(t *testing.T, tools []mcp.Tool, name string) mcp.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return mcp.Tool{}
}

// TestCallToolVariantAnnotations verifies the three call_tool_* variants
// (built via buildCallToolVariantTool, used by both registerTools and the
// routing-mode builders) have all three core hints set explicitly.
func TestCallToolVariantAnnotations(t *testing.T) {
	tests := []struct {
		variant string
		want    expectedHints
	}{
		{
			// Read-only proxy to arbitrary upstream — open-world is honest.
			variant: contracts.ToolVariantRead,
			want:    expectedHints{readOnly: true, destructive: false, openWorld: true},
		},
		{
			// Write proxy: not read-only, non-destructive (writes are not
			// inherently destructive), open-world (calls upstream).
			variant: contracts.ToolVariantWrite,
			want:    expectedHints{readOnly: false, destructive: false, openWorld: true},
		},
		{
			// Destructive proxy: not read-only, destructive, open-world.
			variant: contracts.ToolVariantDestructive,
			want:    expectedHints{readOnly: false, destructive: true, openWorld: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.variant, func(t *testing.T) {
			tool := buildCallToolVariantTool(tc.variant)
			assert.Equal(t, tc.variant, tool.Name)
			assertExplicitHints(t, tool, tc.want)
		})
	}
}

// TestBuildManagementToolsAnnotations verifies the management tool set
// (upstream_servers, quarantine_security, search_servers, list_registries)
// have explicit annotation hints. These are shared across all routing modes.
func TestBuildManagementToolsAnnotations(t *testing.T) {
	cfg := config.DefaultConfig()

	p := &MCPProxyServer{
		logger: zap.NewNop(),
		config: cfg,
	}

	serverTools := p.buildManagementTools()
	tools := make([]mcp.Tool, 0, len(serverTools))
	for _, st := range serverTools {
		tools = append(tools, st.Tool)
	}

	wantByName := map[string]expectedHints{
		// Local config CRUD.
		"upstream_servers": {readOnly: false, destructive: true, openWorld: false},
		// Local approval / inspection.
		"quarantine_security": {readOnly: false, destructive: true, openWorld: false},
		// Bug fix: queries external HTTP registries — open-world is true.
		"search_servers": {readOnly: true, destructive: false, openWorld: true},
		// Reads embedded registry list (in-process).
		"list_registries": {readOnly: true, destructive: false, openWorld: false},
	}

	for name, want := range wantByName {
		tool := findTool(t, tools, name)
		assertExplicitHints(t, tool, want)
	}
}

// TestBuildCodeExecModeToolsAnnotations verifies all tools registered by
// buildCodeExecModeTools (mcp_routing.go) have explicit annotation hints.
func TestBuildCodeExecModeToolsAnnotations(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.EnableCodeExecution = true

	p := &MCPProxyServer{
		logger: zap.NewNop(),
		config: cfg,
	}

	serverTools := p.buildCodeExecModeTools()
	tools := make([]mcp.Tool, 0, len(serverTools))
	for _, st := range serverTools {
		tools = append(tools, st.Tool)
	}

	wantByName := map[string]expectedHints{
		// Local BM25 search over proxy's own index.
		"retrieve_tools": {readOnly: true, destructive: false, openWorld: false},
		// Sandbox can invoke any upstream tool — open-world is correct.
		"code_execution": {readOnly: false, destructive: true, openWorld: true},
		// Management tools (covered by TestBuildManagementToolsAnnotations
		// too, but exercising the full mode build catches integration regressions).
		"upstream_servers":    {readOnly: false, destructive: true, openWorld: false},
		"quarantine_security": {readOnly: false, destructive: true, openWorld: false},
		"search_servers":      {readOnly: true, destructive: false, openWorld: true},
		"list_registries":     {readOnly: true, destructive: false, openWorld: false},
	}

	for name, want := range wantByName {
		tool := findTool(t, tools, name)
		assertExplicitHints(t, tool, want)
	}
}

// TestBuildCallToolModeToolsAnnotations verifies all tools registered by
// buildCallToolModeTools (mcp_routing.go) have explicit annotation hints.
func TestBuildCallToolModeToolsAnnotations(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.EnableCodeExecution = true

	p := &MCPProxyServer{
		logger: zap.NewNop(),
		config: cfg,
	}

	serverTools := p.buildCallToolModeTools()
	tools := make([]mcp.Tool, 0, len(serverTools))
	for _, st := range serverTools {
		tools = append(tools, st.Tool)
	}

	wantByName := map[string]expectedHints{
		"retrieve_tools":                 {readOnly: true, destructive: false, openWorld: false},
		"read_cache":                     {readOnly: true, destructive: false, openWorld: false},
		"code_execution":                 {readOnly: false, destructive: true, openWorld: true},
		contracts.ToolVariantRead:        {readOnly: true, destructive: false, openWorld: true},
		contracts.ToolVariantWrite:       {readOnly: false, destructive: false, openWorld: true},
		contracts.ToolVariantDestructive: {readOnly: false, destructive: true, openWorld: true},
		"upstream_servers":               {readOnly: false, destructive: true, openWorld: false},
		"quarantine_security":            {readOnly: false, destructive: true, openWorld: false},
		"search_servers":                 {readOnly: true, destructive: false, openWorld: true},
		"list_registries":                {readOnly: true, destructive: false, openWorld: false},
	}

	for name, want := range wantByName {
		tool := findTool(t, tools, name)
		assertExplicitHints(t, tool, want)
	}
}

// TestDisabledCodeExecutionAnnotations verifies the disabled stub
// (buildCodeExecutionTool with EnableCodeExecution=false) still has explicit
// annotation hints — preventing it from inheriting the permissive default of
// destructive=true / openWorld=true.
func TestDisabledCodeExecutionAnnotations(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.EnableCodeExecution = false

	p := &MCPProxyServer{
		logger: zap.NewNop(),
		config: cfg,
	}

	serverTools := p.buildCodeExecutionTool()
	require.Len(t, serverTools, 1)
	tool := serverTools[0].Tool
	assert.Equal(t, "code_execution", tool.Name)
	assertExplicitHints(t, tool, expectedHints{readOnly: true, destructive: false, openWorld: false})
}
