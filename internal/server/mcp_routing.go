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

		// Spec 035: Determine content trust based on openWorldHint
		directContentTrust := contracts.ContentTrustForTool(annotations)

		if err != nil {
			// Emit error activity
			p.emitActivityToolCallCompleted(serverName, toolName, sessionID, requestID, "mcp", "error", err.Error(), durationMs, enrichedArgs, "", false, "", nil, directContentTrust)
			return mcp.NewToolResultError(fmt.Sprintf("Error calling %s:%s: %v", serverName, toolName, err)), nil
		}

		// Determine tool variant for activity logging
		toolVariant := contracts.DeriveCallWith(annotations)

		// Forward content blocks (preserving ImageContent, AudioContent, etc.)
		// while applying truncation only to TextContent. See issue #368.
		//
		// Direct mode has a simpler truncator based on ToolResponseLimit; the
		// Truncator type (with caching) is not available here.
		var forwarded *mcp.CallToolResult
		var responseText string
		var truncated bool
		if ctr, ok := result.(*mcp.CallToolResult); ok && ctr != nil {
			newContent := make([]mcp.Content, 0, len(ctr.Content))
			var parts []string
			limit := p.config.ToolResponseLimit
			for _, c := range ctr.Content {
				switch tc := c.(type) {
				case mcp.TextContent:
					txt := tc.Text
					if limit > 0 && len(txt) > limit {
						txt = txt[:limit]
						truncated = true
					}
					tc.Text = txt
					newContent = append(newContent, tc)
					parts = append(parts, txt)
				case mcp.ImageContent:
					newContent = append(newContent, tc)
					parts = append(parts, fmt.Sprintf("[image:%s len=%d]", tc.MIMEType, len(tc.Data)))
				case mcp.AudioContent:
					newContent = append(newContent, tc)
					parts = append(parts, fmt.Sprintf("[audio:%s len=%d]", tc.MIMEType, len(tc.Data)))
				default:
					newContent = append(newContent, c)
					if b, err := json.Marshal(c); err == nil {
						parts = append(parts, string(b))
					}
				}
			}
			forwarded = &mcp.CallToolResult{
				Result:            ctr.Result,
				Content:           newContent,
				StructuredContent: ctr.StructuredContent,
				IsError:           ctr.IsError,
			}
			responseText = joinTextParts(parts)
		} else {
			// Fallback for non-CallToolResult values (string, struct, etc.)
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
			if p.config.ToolResponseLimit > 0 && len(responseText) > p.config.ToolResponseLimit {
				responseText = responseText[:p.config.ToolResponseLimit]
				truncated = true
			}
			forwarded = mcp.NewToolResultText(responseText)
		}

		// Emit success activity
		p.emitActivityToolCallCompleted(serverName, toolName, sessionID, requestID, "mcp", "success", "", durationMs, enrichedArgs, responseText, truncated, toolVariant, nil, directContentTrust)

		return forwarded, nil
	}
}

// buildCodeExecModeTools builds the tool set for code_execution routing mode.
// Includes: code_execution + retrieve_tools (for discovery).
// Does NOT include call_tool_read/write/destructive.
func (p *MCPProxyServer) buildCodeExecModeTools() []mcpserver.ServerTool {
	tools := make([]mcpserver.ServerTool, 0, 4)

	// code_execution tool
	tools = append(tools, p.buildCodeExecutionTool()...)

	// retrieve_tools for discovery — instructs to use code_execution (NOT call_tool_*)
	retrieveToolsTool := mcp.NewTool("retrieve_tools",
		mcp.WithDescription("Search and discover available upstream tools using BM25 full-text search. "+
			"Use this to find tools, then use the `code_execution` tool to call them via `call_tool(serverName, toolName, args)` in JavaScript. "+
			"Do NOT use call_tool_read/write/destructive — they are not available in this mode. "+
			"Use natural language to describe what you want to accomplish. "+
			"Response includes a structured `session_risk` object (level, lethal_trifecta, has_open_world_tools, has_destructive_tools, has_write_tools)."),
		mcp.WithTitleAnnotation("Retrieve Tools"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Natural language description of what you want to accomplish."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tools to return (default: configured tools_limit, max: 100)"),
		),
		mcp.WithBoolean("read_only_only",
			mcp.Description("Only return tools with readOnlyHint=true. Use to self-restrict to safe read operations."),
		),
		mcp.WithBoolean("exclude_destructive",
			mcp.Description("Exclude tools with destructiveHint=true or unset (MCP default is destructive). Use to avoid destructive operations."),
		),
		mcp.WithBoolean("exclude_open_world",
			mcp.Description("Exclude tools with openWorldHint=true or unset (MCP default is open-world). Use to restrict to local/sandboxed tools."),
		),
		mcp.WithBoolean("include_session_risk_warning",
			mcp.Description("Include the prose 'warning' string in session_risk when the lethal trifecta is detected (default: false; structured fields are always returned). Server-side default can be flipped via the 'tool_response_session_risk_warning' config flag."),
		),
	)
	tools = append(tools, mcpserver.ServerTool{
		Tool:    retrieveToolsTool,
		Handler: p.handleRetrieveToolsForMode(config.RoutingModeCodeExecution),
	})

	// Add management tools (upstream_servers, quarantine, registries)
	tools = append(tools, p.buildManagementTools()...)

	p.logger.Info("built code execution mode tools",
		zap.Int("tool_count", len(tools)))

	return tools
}

// buildCallToolModeTools builds the tool set for retrieve_tools routing mode (/mcp/call).
// Includes: retrieve_tools (with call_tool_* instructions) + call_tool_read/write/destructive + read_cache + code_execution.
func (p *MCPProxyServer) buildCallToolModeTools() []mcpserver.ServerTool {
	tools := make([]mcpserver.ServerTool, 0, 8)

	// retrieve_tools — instructs to use call_tool_read/write/destructive
	retrieveToolsTool := mcp.NewTool("retrieve_tools",
		mcp.WithDescription("Search and discover available upstream tools using BM25 full-text search. "+
			"WORKFLOW: 1) Call this tool first to find relevant tools, 2) Check the 'call_with' field in results "+
			"to determine which variant to use, 3) Call the tool using call_tool_read, call_tool_write, or call_tool_destructive. "+
			"Results include 'annotations' (tool behavior hints like destructiveHint), 'call_with' recommendation, "+
			"and a structured `session_risk` object (level, lethal_trifecta, has_open_world_tools, has_destructive_tools, has_write_tools). "+
			"Use annotation filters to self-restrict discovery scope. "+
			"Use natural language to describe what you want to accomplish."),
		mcp.WithTitleAnnotation("Retrieve Tools"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Natural language description of what you want to accomplish. Be specific (e.g., 'create a new GitHub repository', 'get weather for London')."),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of tools to return (default: configured tools_limit, max: 100)"),
		),
		mcp.WithBoolean("include_stats",
			mcp.Description("Include usage statistics for returned tools (default: false)"),
		),
		mcp.WithBoolean("debug",
			mcp.Description("Enable debug mode with detailed scoring and ranking explanations (default: false)"),
		),
		mcp.WithString("explain_tool",
			mcp.Description("When debug=true, explain why a specific tool was ranked low (format: 'server:tool')"),
		),
		mcp.WithBoolean("read_only_only",
			mcp.Description("Only return tools with readOnlyHint=true. Use to self-restrict to safe read operations."),
		),
		mcp.WithBoolean("exclude_destructive",
			mcp.Description("Exclude tools with destructiveHint=true or unset (MCP default is destructive). Use to avoid destructive operations."),
		),
		mcp.WithBoolean("exclude_open_world",
			mcp.Description("Exclude tools with openWorldHint=true or unset (MCP default is open-world). Use to restrict to local/sandboxed tools."),
		),
		mcp.WithBoolean("include_session_risk_warning",
			mcp.Description("Include the prose 'warning' string in session_risk when the lethal trifecta is detected (default: false; structured fields are always returned). Server-side default can be flipped via the 'tool_response_session_risk_warning' config flag."),
		),
	)
	tools = append(tools, mcpserver.ServerTool{
		Tool:    retrieveToolsTool,
		Handler: p.handleRetrieveToolsForMode(config.RoutingModeRetrieveTools),
	})

	// call_tool_read / call_tool_write / call_tool_destructive — all three
	// built from the shared helper in mcp.go so schema stays in sync across
	// the default and retrieve_tools routing modes.
	tools = append(tools, mcpserver.ServerTool{
		Tool:    buildCallToolVariantTool(contracts.ToolVariantRead),
		Handler: p.handleCallToolRead,
	})
	tools = append(tools, mcpserver.ServerTool{
		Tool:    buildCallToolVariantTool(contracts.ToolVariantWrite),
		Handler: p.handleCallToolWrite,
	})
	tools = append(tools, mcpserver.ServerTool{
		Tool:    buildCallToolVariantTool(contracts.ToolVariantDestructive),
		Handler: p.handleCallToolDestructive,
	})

	// read_cache for paginated responses
	readCacheTool := mcp.NewTool("read_cache",
		mcp.WithDescription("Retrieve paginated data when mcpproxy indicates a tool response was truncated. Use the cache key provided in truncation messages."),
		mcp.WithTitleAnnotation("Read Cache"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
		mcp.WithString("key",
			mcp.Required(),
			mcp.Description("Cache key provided by mcpproxy when a response was truncated."),
		),
		mcp.WithNumber("offset",
			mcp.Description("Starting record offset for pagination (default: 0)"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of records to return per page (default: 50, max: 1000)"),
		),
	)
	tools = append(tools, mcpserver.ServerTool{
		Tool:    readCacheTool,
		Handler: p.handleReadCache,
	})

	// code_execution tool (available but not the primary workflow)
	tools = append(tools, p.buildCodeExecutionTool()...)

	// Add management tools (upstream_servers, quarantine, registries)
	tools = append(tools, p.buildManagementTools()...)

	p.logger.Info("built call tool mode tools",
		zap.Int("tool_count", len(tools)))

	return tools
}

// buildCodeExecutionTool builds the code_execution tool for routing mode servers.
// Returns a slice (either 1 tool or 1 disabled stub) for easy appending.
func (p *MCPProxyServer) buildCodeExecutionTool() []mcpserver.ServerTool {
	if p.config != nil && !p.config.EnableCodeExecution {
		// Disabled stub
		codeExecutionTool := mcp.NewTool("code_execution",
			mcp.WithDescription("Code execution is currently disabled. Enable it by setting \"enable_code_execution\": true in your mcpproxy config."),
			mcp.WithTitleAnnotation("Code Execution (Disabled)"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			mcp.WithString("code",
				mcp.Required(),
				mcp.Description("JavaScript source code to execute."),
			),
		)
		return []mcpserver.ServerTool{{
			Tool: codeExecutionTool,
			Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return mcp.NewToolResultError("Code execution is disabled. Enable it by setting \"enable_code_execution\": true in your mcpproxy configuration file."), nil
			},
		}}
	}

	codeExecutionTool := mcp.NewTool("code_execution",
		mcp.WithDescription("Execute JavaScript or TypeScript code that orchestrates multiple upstream MCP tools in a single request. "+
			"Use this when you need to combine results from 2+ tools, implement conditional logic, loops, or data transformations "+
			"that would require multiple round-trips otherwise.\n\n"+
			"**When to use**: Multi-step workflows with data transformation, conditional logic, error handling, or iterating over results.\n"+
			"**When NOT to use**: Single tool calls (use call_tool directly), long-running operations (>2 minutes).\n\n"+
			"**Available in code**:\n"+
			"- `input` global: Your input data passed via the 'input' parameter\n"+
			"- `call_tool(serverName, toolName, args)`: Call upstream tools (returns {ok, result} or {ok, error})\n"+
			"- Modern JavaScript (ES2020+): arrow functions, const/let, template literals, destructuring, classes, for-of, "+
			"optional chaining (?.), nullish coalescing (??), spread/rest, Promises, Symbols, Map/Set, Proxy/Reflect "+
			"(no require(), filesystem, or network access)\n\n"+
			"**TypeScript support**: Set `language: \"typescript\"` to write TypeScript code with type annotations, interfaces, enums, and generics. "+
			"Types are automatically stripped before execution.\n\n"+
			"**Important runtime rules**:\n"+
			"- `call_tool` is strictly SYNCHRONOUS. Do not use `await`.\n"+
			"- Upstream tools usually return an MCP content array. To parse JSON results: `const data = JSON.parse(res.result.content[0].text);`\n"+
			"- The last evaluated expression in your script is automatically returned as the final output.\n\n"+
			"**Security**: Sandboxed execution with timeout enforcement. Respects existing quarantine and server restrictions."),
		mcp.WithTitleAnnotation("Code Execution"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("code",
			mcp.Required(),
			mcp.Description("JavaScript or TypeScript source code (ES2020+) to execute. Supports modern syntax: arrow functions, const/let, template literals, destructuring, "+
				"optional chaining, nullish coalescing. Use `input` to access input data and `call_tool(serverName, toolName, args)` to invoke upstream tools. "+
				"call_tool is SYNCHRONOUS — do not use await. Return value is the last evaluated expression and must be JSON-serializable. "+
				"Example: `const res = call_tool('github', 'get_user', {username: input.username}); const data = JSON.parse(res.result.content[0].text); ({user: data, timestamp: Date.now()})`"),
		),
		mcp.WithString("language",
			mcp.Description("Source code language. When set to 'typescript', the code is automatically transpiled to JavaScript before execution. "+
				"Type annotations are stripped, enums and namespaces are converted to JavaScript equivalents. Default: 'javascript'."),
			mcp.Enum("javascript", "typescript"),
		),
		mcp.WithObject("input",
			mcp.Description("Input data accessible as global `input` variable in code (default: {})"),
		),
		mcp.WithObject("options",
			mcp.Description("Execution options: timeout_ms (1-600000, default: 120000), max_tool_calls (>= 0, 0=unlimited), allowed_servers (array of server names, empty=all allowed)"),
		),
	)
	return []mcpserver.ServerTool{{
		Tool:    codeExecutionTool,
		Handler: p.handleCodeExecution,
	}}
}

// initRoutingModeServers creates separate MCP server instances for each routing mode.
// Each server instance has its own set of tools registered appropriate for that mode.
// The main "server" field remains the retrieve_tools mode server (default).
func (p *MCPProxyServer) initRoutingModeServers() {
	// All routing mode servers share the same hooks for session tracking
	opts := []mcpserver.ServerOption{
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithRecovery(),
	}
	if p.hooks != nil {
		opts = append(opts, mcpserver.WithHooks(p.hooks))
	}

	// Create direct mode server
	p.directServer = mcpserver.NewMCPServer(
		"mcpproxy-go",
		mcpServerVersion(),
		opts...,
	)

	// Create code execution mode server
	p.codeExecServer = mcpserver.NewMCPServer(
		"mcpproxy-go",
		mcpServerVersion(),
		opts...,
	)

	// Create call tool mode server (/mcp/call)
	p.callToolServer = mcpserver.NewMCPServer(
		"mcpproxy-go",
		mcpServerVersion(),
		opts...,
	)

	// Register tools for code execution mode (static tools that don't change)
	codeExecTools := p.buildCodeExecModeTools()
	for _, st := range codeExecTools {
		p.codeExecServer.AddTool(st.Tool, st.Handler)
	}

	// Register tools for call tool mode
	callToolModeTools := p.buildCallToolModeTools()
	for _, st := range callToolModeTools {
		p.callToolServer.AddTool(st.Tool, st.Handler)
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
	case config.RoutingModeRetrieveTools:
		if p.callToolServer != nil {
			return p.callToolServer
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

// GetCallToolServer returns the call tool mode MCP server instance.
func (p *MCPProxyServer) GetCallToolServer() *mcpserver.MCPServer {
	return p.callToolServer
}
