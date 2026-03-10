package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
)

const (
	// DirectModeToolSeparator is the separator between server name and tool name in direct mode.
	// Using double underscore to avoid conflicts with single underscores in tool names.
	DirectModeToolSeparator = "__"
)

// ParseDirectToolName parses a direct mode tool name (serverName__toolName) into server and tool components.
// Splits on the FIRST occurrence of "__" only, so tool names containing "__" are preserved.
// Returns server name, tool name, and whether the parse was successful.
func ParseDirectToolName(directName string) (serverName, toolName string, ok bool) {
	idx := strings.Index(directName, DirectModeToolSeparator)
	if idx <= 0 || idx+len(DirectModeToolSeparator) >= len(directName) {
		return "", "", false
	}
	return directName[:idx], directName[idx+len(DirectModeToolSeparator):], true
}

// FormatDirectToolName formats a server name and tool name into a direct mode tool name.
func FormatDirectToolName(serverName, toolName string) string {
	return serverName + DirectModeToolSeparator + toolName
}

// buildDirectModeTools builds MCP tool definitions for direct mode.
// Each upstream tool is exposed directly with serverName__toolName naming.
// Only tools from connected, enabled, non-quarantined servers are included.
func (p *MCPProxyServer) buildDirectModeTools() []mcpserver.ServerTool {
	ctx := context.Background()

	// Use DiscoverTools which already filters for connected, enabled, non-quarantined servers
	tools, err := p.upstreamManager.DiscoverTools(ctx)
	if err != nil {
		p.logger.Error("failed to discover tools for direct mode", zap.Error(err))
		return nil
	}

	serverTools := make([]mcpserver.ServerTool, 0, len(tools))
	for _, tool := range tools {
		directName := FormatDirectToolName(tool.ServerName, tool.Name)

		// Build MCP tool options
		opts := []mcp.ToolOption{
			mcp.WithDescription(fmt.Sprintf("[%s] %s", tool.ServerName, tool.Description)),
		}

		// Apply annotations from upstream tool
		if tool.Annotations != nil {
			if tool.Annotations.Title != "" {
				opts = append(opts, mcp.WithTitleAnnotation(tool.Annotations.Title))
			}
			if tool.Annotations.ReadOnlyHint != nil {
				opts = append(opts, mcp.WithReadOnlyHintAnnotation(*tool.Annotations.ReadOnlyHint))
			}
			if tool.Annotations.DestructiveHint != nil {
				opts = append(opts, mcp.WithDestructiveHintAnnotation(*tool.Annotations.DestructiveHint))
			}
			if tool.Annotations.IdempotentHint != nil {
				opts = append(opts, mcp.WithIdempotentHintAnnotation(*tool.Annotations.IdempotentHint))
			}
			if tool.Annotations.OpenWorldHint != nil {
				opts = append(opts, mcp.WithOpenWorldHintAnnotation(*tool.Annotations.OpenWorldHint))
			}
		}

		mcpTool := mcp.NewTool(directName, opts...)

		// Apply input schema from upstream tool
		if tool.ParamsJSON != "" {
			var schema map[string]interface{}
			if err := json.Unmarshal([]byte(tool.ParamsJSON), &schema); err == nil {
				mcpTool.InputSchema = mcp.ToolInputSchema{
					Type: "object",
				}
				if props, ok := schema["properties"].(map[string]interface{}); ok {
					mcpTool.InputSchema.Properties = props
				}
				if req, ok := schema["required"].([]interface{}); ok {
					reqStrings := make([]string, 0, len(req))
					for _, r := range req {
						if s, ok := r.(string); ok {
							reqStrings = append(reqStrings, s)
						}
					}
					mcpTool.InputSchema.Required = reqStrings
				}
			}
		}

		serverTools = append(serverTools, mcpserver.ServerTool{
			Tool:    mcpTool,
			Handler: p.makeDirectModeHandler(tool.ServerName, tool.Name, tool.Annotations),
		})
	}

	p.logger.Info("built direct mode tools",
		zap.Int("tool_count", len(serverTools)))

	return serverTools
}

// makeDirectModeHandler creates a handler function for a direct mode tool.
// It handles auth checks, permission enforcement, and upstream calls.
func (p *MCPProxyServer) makeDirectModeHandler(serverName, toolName string, annotations *config.ToolAnnotations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		startTime := time.Now()

		// Check auth context for server access and permissions
		authCtx := auth.AuthContextFromContext(ctx)
		if authCtx != nil {
			// Check server access
			if !authCtx.CanAccessServer(serverName) {
				return mcp.NewToolResultError(fmt.Sprintf("Access denied: token does not have access to server '%s'", serverName)), nil
			}

			// Determine required permission from annotations
			requiredVariant := contracts.DeriveCallWith(annotations)
			requiredPerm := contracts.ToolVariantToOperationType[requiredVariant]
			if requiredPerm == "" {
				requiredPerm = contracts.OperationTypeRead
			}

			if !authCtx.HasPermission(requiredPerm) {
				return mcp.NewToolResultError(fmt.Sprintf("Permission denied: token does not have '%s' permission required for tool '%s:%s'", requiredPerm, serverName, toolName)), nil
			}
		}

		// Get session ID for activity logging
		var sessionID string
		if sess := mcpserver.ClientSessionFromContext(ctx); sess != nil {
			sessionID = sess.SessionID()
		}

		// Get request ID from context
		requestID := reqcontext.GetRequestID(ctx)

		// Get arguments from the request
		args := request.GetArguments()

		// Emit activity event
		enrichedArgs := injectAuthMetadata(ctx, args)
		p.emitActivityToolCallStarted(serverName, toolName, sessionID, requestID, "mcp", enrichedArgs)

		// Call upstream
		qualifiedName := serverName + ":" + toolName
		result, err := p.upstreamManager.CallTool(ctx, qualifiedName, args)

		durationMs := time.Since(startTime).Milliseconds()

		if err != nil {
			// Emit error activity
			p.emitActivityToolCallCompleted(serverName, toolName, sessionID, requestID, "mcp", "error", err.Error(), durationMs, enrichedArgs, "", false, "", nil)
			return mcp.NewToolResultError(fmt.Sprintf("Error calling %s:%s: %v", serverName, toolName, err)), nil
		}

		// Format response
		var responseText string
		switch v := result.(type) {
		case string:
			responseText = v
		default:
			responseBytes, marshalErr := json.Marshal(v)
			if marshalErr != nil {
				responseText = fmt.Sprintf("%v", v)
			} else {
				responseText = string(responseBytes)
			}
		}

		// Determine tool variant for activity logging
		toolVariant := contracts.DeriveCallWith(annotations)

		// Truncate if needed
		truncated := false
		if p.config.ToolResponseLimit > 0 && len(responseText) > p.config.ToolResponseLimit {
			responseText = responseText[:p.config.ToolResponseLimit]
			truncated = true
		}

		// Emit success activity
		p.emitActivityToolCallCompleted(serverName, toolName, sessionID, requestID, "mcp", "success", "", durationMs, enrichedArgs, responseText, truncated, toolVariant, nil)

		return mcp.NewToolResultText(responseText), nil
	}
}

// buildCodeExecModeTools builds the tool set for code_execution routing mode.
// Includes: code_execution (with enhanced description listing available tools) and retrieve_tools (for discovery).
// Does NOT include call_tool_read/write/destructive.
func (p *MCPProxyServer) buildCodeExecModeTools() []mcpserver.ServerTool {
	ctx := context.Background()

	// Build enhanced description with available tools catalog
	toolCatalog := p.buildToolCatalogDescription(ctx)

	codeExecDescription := fmt.Sprintf(
		"Execute JavaScript code that orchestrates multiple upstream MCP tools in a single request. "+
			"Use this when you need to combine results from 2+ tools, implement conditional logic, loops, or data transformations.\n\n"+
			"**Available in JavaScript**:\n"+
			"- `input` global: Your input data passed via the 'input' parameter\n"+
			"- `call_tool(serverName, toolName, args)`: Call upstream tools (returns {ok, result} or {ok, error})\n"+
			"- Standard ES5.1+ JavaScript (no require(), filesystem, or network access)\n\n"+
			"**Available tools for orchestration**:\n%s\n\n"+
			"Use call_tool('serverName', 'toolName', {args}) to invoke tools.",
		toolCatalog,
	)

	tools := make([]mcpserver.ServerTool, 0, 2)

	// code_execution tool with enhanced description
	codeExecutionTool := mcp.NewTool("code_execution",
		mcp.WithDescription(codeExecDescription),
		mcp.WithTitleAnnotation("Code Execution"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("code",
			mcp.Required(),
			mcp.Description("JavaScript source code (ES5.1+) to execute. Use `input` to access input data and `call_tool(serverName, toolName, args)` to invoke upstream tools."),
		),
		mcp.WithObject("input",
			mcp.Description("Input data accessible as global `input` variable in JavaScript code (default: {})"),
		),
		mcp.WithObject("options",
			mcp.Description("Execution options: timeout_ms (1-600000, default: 120000), max_tool_calls (>= 0, 0=unlimited), allowed_servers (array of server names, empty=all allowed)"),
		),
	)
	tools = append(tools, mcpserver.ServerTool{
		Tool:    codeExecutionTool,
		Handler: p.handleCodeExecution,
	})

	// retrieve_tools for discovery
	retrieveToolsTool := mcp.NewTool("retrieve_tools",
		mcp.WithDescription("Search and discover available upstream tools using BM25 full-text search. Use this to find tools before orchestrating them with code_execution. Use natural language to describe what you want to accomplish."),
		mcp.WithTitleAnnotation("Retrieve Tools"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Natural language description of what you want to accomplish."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tools to return (default: configured tools_limit, max: 100)"),
		),
	)
	tools = append(tools, mcpserver.ServerTool{
		Tool:    retrieveToolsTool,
		Handler: p.handleRetrieveTools,
	})

	p.logger.Info("built code execution mode tools",
		zap.Int("tool_count", len(tools)))

	return tools
}

// buildToolCatalogDescription builds a human-readable catalog of available tools for the code_execution description.
func (p *MCPProxyServer) buildToolCatalogDescription(ctx context.Context) string {
	tools, err := p.upstreamManager.DiscoverTools(ctx)
	if err != nil {
		return "  (unable to discover tools - use retrieve_tools to search)"
	}

	if len(tools) == 0 {
		return "  (no upstream tools available)"
	}

	var sb strings.Builder
	for _, tool := range tools {
		// Determine permission tier from annotations
		callWith := contracts.DeriveCallWith(tool.Annotations)
		perm := contracts.ToolVariantToOperationType[callWith]
		if perm == "" {
			perm = "read"
		}

		// Truncate description for catalog listing
		desc := tool.Description
		if len(desc) > 80 {
			desc = desc[:77] + "..."
		}

		sb.WriteString(fmt.Sprintf("- %s:%s (%s) - %s\n", tool.ServerName, tool.Name, perm, desc))
	}

	return sb.String()
}

// initRoutingModeServers creates separate MCP server instances for each routing mode.
// Each server instance has its own set of tools registered appropriate for that mode.
// The main "server" field remains the retrieve_tools mode server (default).
func (p *MCPProxyServer) initRoutingModeServers() {
	// Create direct mode server
	p.directServer = mcpserver.NewMCPServer(
		"mcpproxy-go",
		"1.0.0",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithRecovery(),
	)

	// Create code execution mode server
	p.codeExecServer = mcpserver.NewMCPServer(
		"mcpproxy-go",
		"1.0.0",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithRecovery(),
	)

	// Register tools for code execution mode (static tools that don't change)
	codeExecTools := p.buildCodeExecModeTools()
	for _, st := range codeExecTools {
		p.codeExecServer.AddTool(st.Tool, st.Handler)
	}

	// Note: Direct mode tools are built lazily/on-demand via RefreshDirectModeTools
	// because upstream servers may not be connected yet during initialization.
	// The servers.changed event will trigger a refresh.

	p.logger.Info("routing mode servers initialized",
		zap.String("default_mode", p.config.RoutingMode))
}

// RefreshDirectModeTools rebuilds the direct mode server's tool set.
// Should be called when upstream servers change (connect/disconnect/tool updates).
func (p *MCPProxyServer) RefreshDirectModeTools() {
	if p.directServer == nil {
		return
	}

	directTools := p.buildDirectModeTools()

	// Convert to the format needed by SetTools
	serverTools := make([]mcpserver.ServerTool, len(directTools))
	copy(serverTools, directTools)

	// Replace all tools atomically
	p.directServer.SetTools(serverTools...)

	p.logger.Info("refreshed direct mode tools",
		zap.Int("tool_count", len(directTools)))
}

// RefreshCodeExecModeTools rebuilds the code execution mode server's tool catalog description.
// Should be called when upstream servers change to update the available tools listing.
func (p *MCPProxyServer) RefreshCodeExecModeTools() {
	if p.codeExecServer == nil {
		return
	}

	codeExecTools := p.buildCodeExecModeTools()
	serverTools := make([]mcpserver.ServerTool, len(codeExecTools))
	copy(serverTools, codeExecTools)

	p.codeExecServer.SetTools(serverTools...)

	p.logger.Info("refreshed code execution mode tools",
		zap.Int("tool_count", len(codeExecTools)))
}

// GetMCPServerForMode returns the MCP server instance for the given routing mode.
// Falls back to the default retrieve_tools server for unknown modes.
func (p *MCPProxyServer) GetMCPServerForMode(mode string) *mcpserver.MCPServer {
	switch mode {
	case config.RoutingModeDirect:
		if p.directServer != nil {
			return p.directServer
		}
	case config.RoutingModeCodeExecution:
		if p.codeExecServer != nil {
			return p.codeExecServer
		}
	}
	// Default: retrieve_tools mode (the original server)
	return p.server
}

// GetDirectServer returns the direct mode MCP server instance.
func (p *MCPProxyServer) GetDirectServer() *mcpserver.MCPServer {
	return p.directServer
}

// GetCodeExecServer returns the code execution mode MCP server instance.
func (p *MCPProxyServer) GetCodeExecServer() *mcpserver.MCPServer {
	return p.codeExecServer
}
