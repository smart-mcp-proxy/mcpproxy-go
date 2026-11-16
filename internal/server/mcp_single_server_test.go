package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// newSingleServerSurfaceForTest registers a /mcp/s/<name> surface on p from
// the published catalog, the way SingleServerMCP does, without needing a live
// upstream client behind the name.
func newSingleServerSurfaceForTest(t *testing.T, p *MCPProxyServer, name string) *singleServerSurface {
	t.Helper()
	require.NotNil(t, p.singleServerOpts, "initRoutingModeServers must prepare the single-server options")
	s := &singleServerSurface{
		name:   name,
		server: mcpserver.NewMCPServer("mcpproxy-go", mcpServerVersion(), p.singleServerOpts...),
	}
	p.directRefreshMu.Lock()
	defer p.directRefreshMu.Unlock()
	p.singleServersMu.Lock()
	defer p.singleServersMu.Unlock()
	if p.singleServers == nil {
		p.singleServers = map[string]*singleServerSurface{}
	}
	p.singleServers[name] = s
	p.registerSingleServerTools(s, p.loadDirectCatalog())
	return s
}

type listedTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func listSingleServerTools(t *testing.T, s *singleServerSurface, ctx context.Context) map[string]listedTool {
	t.Helper()
	payload, err := json.Marshal(s.server.HandleMessage(withDirectRequestKindBox(ctx),
		[]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	require.NoError(t, err)
	var listed struct {
		Result struct {
			Tools []listedTool `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(payload, &listed), string(payload))
	out := map[string]listedTool{}
	for _, tool := range listed.Result.Tools {
		out[tool.Name] = tool
	}
	return out
}

func singleServerAdminCtx() context.Context {
	return auth.WithAuthContext(context.Background(), auth.AdminContext())
}

func TestSingleServerSurface_ListsOnlyThatServerUnderBareNames(t *testing.T) {
	p := newDirectDescribeProxy(t)
	s := newSingleServerSurfaceForTest(t, p, "github")

	tools := listSingleServerTools(t, s, singleServerAdminCtx())
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	assert.ElementsMatch(t, []string{"read_file", "delete_repo", "create_issue", "describe_tool"}, names)

	readFile := tools["read_file"]
	assert.Equal(t, "Read a file", readFile.Description, "no [server] prefix on the description")
	assert.Contains(t, readFile.InputSchema["properties"], "path", "the full upstream schema is listed")
}

func TestSingleServerSurface_CallsDispatchToTheUpstreamTool(t *testing.T) {
	p := newDirectDescribeProxy(t)
	s := newSingleServerSurfaceForTest(t, p, "github")

	registered := s.server.ListTools()
	require.Contains(t, registered, "read_file")
	_, prefixed := registered["github__read_file"]
	assert.False(t, prefixed, "the prefixed name is not registered on this surface")

	stamp, ok := readDirectToolStamp(registered["read_file"].Tool)
	require.True(t, ok, "the tool carries the same identity stamp /mcp/all registers")
	assert.Equal(t, "github", stamp.owner)
	assert.Equal(t, "read_file", stamp.rawName)
}

func TestSingleServerSurface_EnforcesTokenScope(t *testing.T) {
	p := newDirectDescribeProxy(t)
	s := newSingleServerSurfaceForTest(t, p, "github")

	otherServer := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "other", AllowedServers: []string{"we__ird"},
		Permissions: []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})
	tools := listSingleServerTools(t, s, otherServer)
	for name := range tools {
		assert.Equal(t, "describe_tool", name, "a token scoped to another server sees none of this server's tools")
	}

	readOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "ro", AllowedServers: []string{"github"},
		Permissions: []string{auth.PermRead},
	})
	tools = listSingleServerTools(t, s, readOnly)
	assert.Contains(t, tools, "read_file")
	assert.NotContains(t, tools, "delete_repo", "the permission tier filter applies as on /mcp/all")
}

func TestSingleServerSurface_DescribeToolTakesAndReturnsBareNames(t *testing.T) {
	p := newDirectDescribeProxy(t)

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"tool_ids": []any{"read_file", "github:create_issue"}}
	result, err := p.singleServerDescribeHandler("github")(singleServerAdminCtx(), req)
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)

	text := result.Content[0].(mcp.TextContent).Text
	assert.NotContains(t, text, "github__")
	var resp describeToolResponse
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	require.Len(t, resp.Definitions, 2, text)
	var names []string
	for _, d := range resp.Definitions {
		names = append(names, d["name"].(string))
	}
	assert.ElementsMatch(t, []string{"read_file", "create_issue"}, names)
}

func TestSingleServerSurface_InstructionsAreTheUpstreamsOwn(t *testing.T) {
	p := newDirectDescribeProxy(t)
	s := newSingleServerSurfaceForTest(t, p, "github")

	got := p.instructionsForCaller(singleServerAdminCtx(), s.server, "static mcpproxy text")
	assert.NotContains(t, got, "mcpproxy", "nothing of mcpproxy's instructions")
	assert.NotContains(t, got, "YOUR ACCESS", "no per-caller access block")
	assert.Equal(t, p.upstreamInstructions("github"), got)

	// The other surfaces are unaffected.
	assert.Contains(t, p.instructionsForCaller(singleServerAdminCtx(), p.directServer, "static mcpproxy text"), "static mcpproxy text")
}

func TestSingleServerSurface_RefreshesOnlyWhenItsToolsChange(t *testing.T) {
	p := newDirectDescribeProxy(t)
	s := newSingleServerSurfaceForTest(t, p, "github")
	firstFP := s.toolsFP
	require.NotEmpty(t, firstFP)

	// A catalog change on another server leaves this surface's registration alone.
	tools := describeDirectFixture()
	other := append(tools, &config.ToolMetadata{
		ServerName: "we__ird", Name: "another", Description: "x", ParamsJSON: `{"type":"object"}`, Hash: "h-another",
	})
	p.directRefreshMu.Lock()
	cat := buildDirectCatalog(other, nil)
	p.publishDirectCatalog(cat)
	p.refreshSingleServerSurfacesLocked(cat)
	p.directRefreshMu.Unlock()
	assert.Equal(t, firstFP, s.toolsFP)

	// A new tool on this server is registered.
	require.NoError(t, p.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "list_prs", Status: storage.ToolApprovalStatusApproved,
	}))
	more := append(other, &config.ToolMetadata{
		ServerName: "github", Name: "list_prs", Description: "List PRs", ParamsJSON: `{"type":"object"}`, Hash: "h-list-prs",
	})
	p.directRefreshMu.Lock()
	cat = buildDirectCatalog(more, nil)
	p.publishDirectCatalog(cat)
	p.refreshSingleServerSurfacesLocked(cat)
	p.directRefreshMu.Unlock()
	assert.NotEqual(t, firstFP, s.toolsFP)
	assert.Contains(t, listSingleServerTools(t, s, singleServerAdminCtx()), "list_prs")
}

func TestSingleServerNameFor(t *testing.T) {
	p := newDirectDescribeProxy(t)
	s := newSingleServerSurfaceForTest(t, p, "github")

	name, ok := p.singleServerNameFor(s.server)
	assert.True(t, ok)
	assert.Equal(t, "github", name)

	_, ok = p.singleServerNameFor(p.directServer)
	assert.False(t, ok, "the direct server is not a single-server surface")
}

func TestStripServerPrefixInJSON(t *testing.T) {
	in := `{"definitions":[{"name":"github__read_file","id":"github:read_file","description":"[github] Read a file","note":"mentions github__x mid-string"}]}`
	out := stripServerPrefixInJSON(in, "github")
	assert.False(t, strings.Contains(out, `"github__read_file"`), out)
	assert.Contains(t, out, `"name":"read_file"`)
	assert.Contains(t, out, `"id":"read_file"`)
	assert.Contains(t, out, `"description":"Read a file"`)
	assert.Contains(t, out, "mentions github__x mid-string", "only leading prefixes are removed")

	assert.Equal(t, "not json", stripServerPrefixInJSON("not json", "github"))
}
