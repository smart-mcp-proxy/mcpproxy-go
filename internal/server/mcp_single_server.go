package server

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Single-server surfaces: /mcp/s/<server>.
//
// Each one is an MCP server that stands in for ONE upstream. It lists only that
// upstream's tools, under the upstream's own tool names (no "<server>__"
// prefix and no "[server]" description prefix), and answers initialize with
// the upstream's own instructions. A client can therefore register
// "http://127.0.0.1:8080/mcp/s/notion" under the name "notion" and see exactly
// the tools and instructions it would get from connecting to Notion directly,
// while mcpproxy holds the OAuth tokens and refreshes them for every client.
//
// The surface is a projection of the published direct catalog, never a second
// discovery path: each tool reuses the direct catalog entry, its identity stamp
// and its handler (which closes over that entry, so dispatch never re-resolves
// by name), and the server runs the direct server's discovery filter chain. It
// can therefore never list or call a tool that /mcp/all would refuse the same
// caller. describe_tool is kept, translated to and from the bare names.

// singleServerSurface is one /mcp/s/<server> MCP server and the fingerprint of
// the tool set last registered on it.
type singleServerSurface struct {
	name    string
	server  *mcpserver.MCPServer
	toolsFP string
}

// SingleServerMCP returns the MCP server for /mcp/s/<name>, creating it on
// first use, or nil when no upstream server with that name is configured.
// The returned server is stable for the life of the process: later catalog
// rebuilds re-register its tools in place.
func (p *MCPProxyServer) SingleServerMCP(name string) *mcpserver.MCPServer {
	if name == "" || p.singleServerOpts == nil || !p.upstreamConfigured(name) {
		return nil
	}

	p.singleServersMu.Lock()
	if s, ok := p.singleServers[name]; ok {
		p.singleServersMu.Unlock()
		return s.server
	}
	p.singleServersMu.Unlock()

	// Lock order: directRefreshMu, then singleServersMu (the rebuild path
	// holds the first and takes the second). Holding directRefreshMu while
	// populating means a rebuild cannot publish a newer catalog between this
	// read and the registration below.
	p.directRefreshMu.Lock()
	defer p.directRefreshMu.Unlock()
	p.singleServersMu.Lock()
	defer p.singleServersMu.Unlock()

	if s, ok := p.singleServers[name]; ok {
		return s.server
	}
	s := &singleServerSurface{
		name:   name,
		server: mcpserver.NewMCPServer("mcpproxy-go", mcpServerVersion(), p.singleServerOpts...),
	}
	if p.singleServers == nil {
		p.singleServers = map[string]*singleServerSurface{}
	}
	p.singleServers[name] = s
	p.registerSingleServerTools(s, p.loadDirectCatalog())
	return s.server
}

// refreshSingleServerSurfacesLocked re-registers every existing single-server
// surface from a newly published catalog. The caller MUST hold
// directRefreshMu. A surface whose tool set did not change is left alone, so
// its clients are not told the tools changed when they did not.
func (p *MCPProxyServer) refreshSingleServerSurfacesLocked(cat *directCatalog) {
	p.singleServersMu.Lock()
	defer p.singleServersMu.Unlock()
	for _, s := range p.singleServers {
		p.registerSingleServerTools(s, cat)
	}
}

// registerSingleServerTools renders s's tools from cat and registers them if
// they differ from what is registered now. The caller holds singleServersMu.
func (p *MCPProxyServer) registerSingleServerTools(s *singleServerSurface, cat *directCatalog) {
	tools := p.renderSingleServerTools(s.name, cat)
	fp := toolSetFingerprint(tools)
	if s.toolsFP != "" && fp == s.toolsFP {
		return
	}
	s.server.SetTools(tools...)
	s.toolsFP = fp
	p.logger.Debug("registered single-server surface tools",
		zap.String("server", s.name),
		zap.Int("tool_count", len(tools)))
}

// renderSingleServerTools renders the tools of upstream `name` from cat under
// their bare names, plus the translated describe_tool.
func (p *MCPProxyServer) renderSingleServerTools(name string, cat *directCatalog) []mcpserver.ServerTool {
	var tools []mcpserver.ServerTool
	if cat != nil {
		deferred := cat.Mode() == config.DirectToolResponseModeDeferred
		for _, displayName := range cat.DisplayNames() {
			entry, ok := cat.Lookup(displayName)
			if !ok || entry.ServerName != name || entry.ToolName == "" {
				continue
			}
			var mcpTool mcp.Tool
			if deferred {
				mcpTool = renderDeferredDirectTool(entry, entry.Description+p.directSignatureSuffix(entry))
			} else {
				mcpTool = renderFullDirectTool(entry, entry.Description)
			}
			mcpTool.Name = entry.ToolName
			// The same identity stamp /mcp/all registers, so the shared filter
			// chain authorizes this tool exactly as it would the prefixed one.
			mcpTool = stampDirectTool(mcpTool, entry)
			tools = append(tools, mcpserver.ServerTool{
				Tool:    mcpTool,
				Handler: p.makeDirectModeHandler(entry),
			})
		}
	}
	return append(tools, mcpserver.ServerTool{
		Tool:    singleServerDescribeTool(),
		Handler: p.singleServerDescribeHandler(name),
	})
}

// singleServerNameFor reports whether srv is a single-server surface and, if
// so, which upstream it stands in for.
func (p *MCPProxyServer) singleServerNameFor(srv *mcpserver.MCPServer) (string, bool) {
	if srv == nil {
		return "", false
	}
	p.singleServersMu.Lock()
	defer p.singleServersMu.Unlock()
	for name, s := range p.singleServers {
		if s.server == srv {
			return name, true
		}
	}
	return "", false
}

// upstreamConfigured reports whether an upstream server called name exists.
func (p *MCPProxyServer) upstreamConfigured(name string) bool {
	if p.upstreamManager == nil {
		return false
	}
	_, ok := p.upstreamManager.GetClient(name)
	return ok
}

// upstreamInstructions returns the instructions the upstream server sent in
// its own initialize response, or "" when it sent none or is not connected.
func (p *MCPProxyServer) upstreamInstructions(name string) string {
	if p.upstreamManager == nil {
		return ""
	}
	client, ok := p.upstreamManager.GetClient(name)
	if !ok || client == nil {
		return ""
	}
	info := client.GetServerInfo()
	if info == nil {
		return ""
	}
	return info.Instructions
}

// singleServerDescribeTool is describe_tool with its tool_ids description
// rewritten for a surface whose tools carry their bare names.
func singleServerDescribeTool() mcp.Tool {
	tool := buildDescribeToolTool()
	if prop, ok := tool.InputSchema.Properties["tool_ids"].(map[string]any); ok {
		copied := make(map[string]any, len(prop))
		for k, v := range prop {
			copied[k] = v
		}
		copied["description"] = "Tool names as listed. Max 5, or 50 with check:true."
		props := make(map[string]any, len(tool.InputSchema.Properties))
		for k, v := range tool.InputSchema.Properties {
			props[k] = v
		}
		props["tool_ids"] = copied
		tool.InputSchema.Properties = props
	}
	return tool
}

// singleServerDescribeHandler wraps the direct surface's describe_tool
// handler: bare tool names in tool_ids are qualified as "<server>:<tool>"
// before the direct handler resolves them, and "<server>__" prefixes in the
// response are removed, so the surface never shows a name it does not list.
func (p *MCPProxyServer) singleServerDescribeHandler(name string) mcpserver.ToolHandlerFunc {
	inner := p.describeToolHandler(describeSurfaceDirect)
	prefix := name + "__"
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if args, ok := request.Params.Arguments.(map[string]any); ok {
			if ids, ok := args["tool_ids"].([]any); ok {
				qualified := make([]any, len(ids))
				for i, id := range ids {
					s, isString := id.(string)
					if isString && !strings.Contains(s, ":") && !strings.HasPrefix(s, prefix) {
						s = name + ":" + s
					}
					if isString {
						qualified[i] = s
					} else {
						qualified[i] = id
					}
				}
				rewritten := make(map[string]any, len(args))
				for k, v := range args {
					rewritten[k] = v
				}
				rewritten["tool_ids"] = qualified
				request.Params.Arguments = rewritten
			}
		}

		result, err := inner(ctx, request)
		if err != nil || result == nil {
			return result, err
		}
		for i, content := range result.Content {
			text, ok := content.(mcp.TextContent)
			if !ok {
				continue
			}
			text.Text = stripServerPrefixInJSON(text.Text, name)
			result.Content[i] = text
		}
		return result, nil
	}
}

// stripServerPrefixInJSON removes the "<server>__" prefix and "<server>:"
// qualifier from every string value in a JSON document that starts with one,
// and "[server] " from descriptions. Text that is not JSON is returned as is.
func stripServerPrefixInJSON(text, server string) string {
	var doc any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return text
	}
	underscore, colon, bracket := server+"__", server+":", "["+server+"] "
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case map[string]any:
			for k, val := range t {
				t[k] = walk(val)
			}
			return t
		case []any:
			for i, val := range t {
				t[i] = walk(val)
			}
			return t
		case string:
			switch {
			case strings.HasPrefix(t, underscore):
				return strings.TrimPrefix(t, underscore)
			case strings.HasPrefix(t, colon):
				return strings.TrimPrefix(t, colon)
			case strings.HasPrefix(t, bracket):
				return strings.TrimPrefix(t, bracket)
			}
			return t
		default:
			return v
		}
	}
	out, err := json.Marshal(walk(doc))
	if err != nil {
		return text
	}
	return string(out)
}
