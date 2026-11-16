package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
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
	// routingFP is the routing state (tiers, hashes, schemas, annotations) of
	// this server's catalog entries at the last registration. The listing can
	// stay byte-identical while it moves, and the stamp and handler close over
	// it, so a re-registration is skipped only when BOTH fingerprints match.
	routingFP string
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
	routingFP := singleServerRoutingFingerprint(s.name, cat)
	if directSurfaceUnchanged(s.toolsFP, s.routingFP, fp, routingFP) {
		return
	}
	s.server.SetTools(tools...)
	s.toolsFP = fp
	s.routingFP = routingFP
	p.logger.Debug("registered single-server surface tools",
		zap.String("server", s.name),
		zap.Int("tool_count", len(tools)))
}

// renderSingleServerTools renders the tools of upstream `name` from cat under
// their bare names, plus the translated describe_tool.
func (p *MCPProxyServer) renderSingleServerTools(name string, cat *directCatalog) []mcpserver.ServerTool {
	var tools []mcpserver.ServerTool
	upstreamDescribe := false
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
			// The handler words its refusals and invalid_params results with
			// entry.DisplayName; on this surface that is the bare name the
			// caller used, not "<server>__<tool>". The stamp above keeps the
			// original entry's identity; only the handler gets the copy.
			bare := *entry
			bare.DisplayName = entry.ToolName
			tools = append(tools, mcpserver.ServerTool{
				Tool:    mcpTool,
				Handler: p.makeDirectModeHandler(&bare),
			})
			if entry.ToolName == singleServerDescribeToolName {
				upstreamDescribe = true
			}
		}
	}
	if upstreamDescribe {
		// The upstream's own tool named describe_tool stands in for the
		// built-in: the surface mirrors the upstream, so its tool wins.
		return tools
	}
	return append(tools, mcpserver.ServerTool{
		Tool:    singleServerDescribeTool(),
		Handler: p.singleServerDescribeHandler(name),
	})
}

const singleServerDescribeToolName = "describe_tool"

// singleServerRoutingFingerprint hashes everything the registered stamps and
// handlers of upstream `name` close over that the listing does not show: the
// tier, hash, stored schema and annotations of each of its catalog entries.
func singleServerRoutingFingerprint(name string, cat *directCatalog) string {
	if cat == nil {
		return "nil-catalog"
	}
	sum := sha256.New()
	fmt.Fprintf(sum, "mode:%s\n", cat.Mode())
	for _, displayName := range cat.DisplayNames() {
		e, ok := cat.Lookup(displayName)
		if !ok || e.ServerName != name || e.ToolName == "" {
			continue
		}
		ann, _ := json.Marshal(e.Annotations)
		fmt.Fprintf(sum, "entry:%s\x00%s\x00%s\x00%s\x00%s\x00%s\n",
			e.DisplayName, e.ToolName, e.Hash, e.RequiredPermission, e.ParamsJSON, ann)
	}
	return hex.EncodeToString(sum.Sum(nil))
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

// singleServerAdmits reports whether the caller in ctx may reach the
// /mcp/s/<name> surface at all: the server exists, the credential's server
// allow-list covers it, and the caller's effective profile (token pin, client
// binding, anonymous profile) reaches it. It is the door check the HTTP route
// answers 404 on, so an out-of-scope caller learns nothing about the server,
// not even its instructions, whatever the discovery filters would later hide.
// The profile is resolved list-only, exactly as the direct surface's listing
// filter does, so admission never varies with a connection's set_profile.
func (p *MCPProxyServer) singleServerAdmits(ctx context.Context, name string) bool {
	if !p.upstreamConfigured(name) {
		return false
	}
	if authCtx := auth.AuthContextFromContext(ctx); authCtx != nil && !authCtx.CanAccessServer(name) {
		return false
	}
	resolveCtx := listOnlyProfileResolution(ctx)
	_, scope, idx := p.resolveActiveProfileWithIndex(resolveCtx)
	if res := p.ResolveProfileV3(resolveCtx, idx); res.Scope != nil {
		scope = res.Scope
	}
	return scope.Allows(name)
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
//
// Upstream-authored text is untrusted, so it is forwarded only for a server
// that is enabled and not quarantined, the same gate its tools pass.
func (p *MCPProxyServer) upstreamInstructions(name string) string {
	if p.upstreamManager == nil {
		return ""
	}
	if p.storage != nil {
		sc, err := p.storage.GetUpstreamServer(name)
		if err != nil || sc == nil || !sc.Enabled || sc.Quarantined {
			return ""
		}
	}
	client, ok := p.upstreamManager.GetClient(name)
	if !ok || client == nil {
		return ""
	}
	info := client.GetServerInfo()
	if info == nil {
		return ""
	}
	return p.screenUpstreamInstructions(name, info.Instructions)
}

// maxUpstreamInstructionsBytes caps forwarded upstream instructions. Real
// server instructions are a few paragraphs; a larger blob is withheld.
const maxUpstreamInstructionsBytes = 8 * 1024

// screenUpstreamInstructions applies to upstream instructions the same
// in-process detect scan a changed tool definition gets in scan mode: text that
// is oversized, flagged, or that the scan cannot fully cover is withheld.
// Instructions have no approval hash, so they are screened on every use rather
// than approved once; a change that makes them hostile stops being forwarded
// the next time a client initializes.
func (p *MCPProxyServer) screenUpstreamInstructions(name, text string) string {
	if text == "" {
		return ""
	}
	if len(text) > maxUpstreamInstructionsBytes {
		p.logger.Warn("withholding upstream instructions: too large",
			zap.String("server", name), zap.Int("bytes", len(text)))
		return ""
	}
	verdict, findings, coverageOK := scanner.ScanToolMetadataVerdict(name, []*config.ToolMetadata{{
		ServerName:  name,
		Name:        "initialize_instructions",
		Description: text,
	}}, nil)
	if !coverageOK || verdict != "clean" {
		var signals []string
		for _, f := range findings {
			signals = append(signals, f.Signals...)
		}
		p.logger.Warn("withholding upstream instructions: scan was not clean",
			zap.String("server", name), zap.String("verdict", verdict),
			zap.Bool("coverage_ok", coverageOK), zap.Strings("tpa_signals", signals))
		return ""
	}
	return text
}

// singleServerSessionIDs mints and checks the Mcp-Session-Id of ONE
// /mcp/s/<server> surface. mcp-go's default manager accepts any well-formed
// "mcp-session-<uuid>", so a session id minted on one surface (and the client
// identity attributed to it) would be accepted on every other. Each surface's
// ids carry a tag derived from its server name, and a surface accepts only its
// own. Like the default manager it tracks nothing, so ids stay valid across a
// restart of the same surface.
type singleServerSessionIDs struct {
	suffix string
}

const singleServerSessionPrefix = "mcp-session-"

func newSingleServerSessionIDs(server string) *singleServerSessionIDs {
	sum := sha256.Sum256([]byte("mcpproxy/single-server-session/" + server))
	return &singleServerSessionIDs{suffix: "." + hex.EncodeToString(sum[:8])}
}

func (m *singleServerSessionIDs) Generate() string {
	return singleServerSessionPrefix + uuid.NewString() + m.suffix
}

func (m *singleServerSessionIDs) Validate(sessionID string) (isTerminated bool, err error) {
	rest, ok := strings.CutPrefix(sessionID, singleServerSessionPrefix)
	if ok {
		rest, ok = strings.CutSuffix(rest, m.suffix)
	}
	if ok {
		_, perr := uuid.Parse(rest)
		ok = perr == nil
	}
	if !ok {
		return false, fmt.Errorf("invalid session id for this endpoint")
	}
	return false, nil
}

func (m *singleServerSessionIDs) Terminate(string) (isNotAllowed bool, err error) {
	return false, nil
}

// singleServerStreamableOptions is the Streamable HTTP option set of a
// /mcp/s/<server> transport: the shared client-facing options plus session ids
// bound to that server.
func singleServerStreamableOptions(cfgProvider func() *config.Config, server string) []mcpserver.StreamableHTTPOption {
	return append(clientFacingStreamableOptions(cfgProvider),
		mcpserver.WithSessionIdManager(newSingleServerSessionIDs(server)))
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
					// Every id is resolved as this server's tool: this server's own
					// qualifier ("<server>:" or "<server>__") is removed and the bare
					// name is qualified again with the exact server name. Another
					// server's id, including one whose name merely starts with
					// "<server>__" (a server named "<server>__x"), therefore resolves
					// to not-found rather than to that server's definition.
					if isString {
						bare := strings.TrimSpace(s)
						switch {
						case strings.HasPrefix(bare, name+":"):
							bare = strings.TrimPrefix(bare, name+":")
						case strings.HasPrefix(bare, prefix):
							bare = strings.TrimPrefix(bare, prefix)
						}
						s = name + ":" + bare
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

// stripServerPrefixInJSON removes this server's qualifier from the identity
// fields of a describe_tool response: "<server>__" / "<server>:" from the
// "name" and "id" values and "did_you_mean" entries, and "[server] " from a
// "description". Schemas (inputSchema, output_schema) and annotations are
// never touched, so an enum or default that happens to start with the server
// name keeps its value. Text that is not JSON is returned as is.
func stripServerPrefixInJSON(text, server string) string {
	var doc any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return text
	}
	underscore, colon, bracket := server+"__", server+":", "["+server+"] "
	stripID := func(s string) string {
		switch {
		case strings.HasPrefix(s, underscore):
			return strings.TrimPrefix(s, underscore)
		case strings.HasPrefix(s, colon):
			return strings.TrimPrefix(s, colon)
		}
		return s
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case map[string]any:
			for k, val := range t {
				switch k {
				case "inputSchema", "output_schema", "annotations":
					continue
				case "name", "id":
					if str, ok := val.(string); ok {
						t[k] = stripID(str)
						continue
					}
				case "description":
					if str, ok := val.(string); ok {
						t[k] = strings.TrimPrefix(str, bracket)
						continue
					}
				case "did_you_mean":
					// Suggestions come from every tool the caller can see. Keep
					// only this server's, by their exact "<server>:<tool>" form
					// (the "<server>__<tool>" display form cannot tell server
					// "a" from a server named "a__b"), as bare names.
					if list, ok := val.([]any); ok {
						kept := make([]any, 0, len(list))
						seen := map[string]struct{}{}
						for _, item := range list {
							str, ok := item.(string)
							if !ok {
								continue
							}
							srv, tool, found := strings.Cut(str, ":")
							if !found || srv != server || tool == "" {
								continue
							}
							if _, dup := seen[tool]; dup {
								continue
							}
							seen[tool] = struct{}{}
							kept = append(kept, tool)
						}
						t[k] = kept
						continue
					}
				}
				t[k] = walk(val)
			}
			return t
		case []any:
			for i, val := range t {
				t[i] = walk(val)
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
