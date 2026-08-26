package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

const (
	// DirectModeToolSeparator is the separator between server name and tool name in direct mode.
	// Using double underscore to avoid conflicts with single underscores in tool names.
	DirectModeToolSeparator = "__"
)

// safeTruncateBytes returns the largest cut length <= limit at which s can be
// sliced without splitting a multi-byte UTF-8 rune. Direct-mode truncation uses
// a raw byte budget (ToolResponseLimit); cutting at the raw offset can land in
// the middle of a multi-byte character and emit invalid UTF-8 in the forwarded
// TextContent, which downstream JSON encoders/clients reject or render as a
// replacement char. Callers must ensure limit < len(s) (i.e. truncation is
// actually needed) before calling.
func safeTruncateBytes(s string, limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit >= len(s) {
		return len(s)
	}
	// Back up to the start of the rune that straddles the cut point.
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return cut
}

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

// FormatDirectPromptName formats a server name and prompt name using the
// same "__" separator convention as FormatDirectToolName.
func FormatDirectPromptName(serverName, promptName string) string {
	return FormatDirectToolName(serverName, promptName)
}

// buildDirectModeTools builds MCP tool definitions for direct mode.
// Each upstream tool is exposed directly with serverName__toolName naming.
// Only tools from connected, enabled, non-quarantined servers are included.
func (p *MCPProxyServer) buildDirectModeTools() ([]mcpserver.ServerTool, *directCatalog) {
	ctx := context.Background()

	// DiscoverTools already filters to connected, enabled, non-quarantined
	// servers — server-LEVEL filtering only. Tool-level state (pending/changed
	// approval) is applied later by the callability filter.
	// The initial rebuild (D15) runs during construction, so this can be reached
	// before the upstream manager is wired. Treat it exactly like a discovery
	// failure rather than panicking: built-ins still register, the catalog is
	// still published, and the next servers.changed fills in the upstreams.
	if p.upstreamManager == nil {
		return p.withDirectBuiltins(nil), buildDirectCatalog(nil, p.logger)
	}

	tools, err := p.upstreamManager.DiscoverTools(ctx)
	if err != nil {
		p.logger.Error("failed to discover tools for direct mode", zap.Error(err))
		// A NON-NIL empty catalog, not nil (D13 rule 2). Returning nil here — as
		// this path used to, via setDirectToolPermissions(nil) — would tell the
		// discovery filters "no catalog yet, do not deny" at exactly the moment
		// upstream discovery is failing, flipping them from deny-on-miss to
		// allow-everything.
		return p.withDirectBuiltins(nil), buildDirectCatalog(nil, p.logger)
	}

	cat := buildDirectCatalog(tools, p.logger)
	return p.withDirectBuiltins(p.renderDirectTools(cat)), cat
}

// withDirectBuiltins appends the tools mcpproxy serves itself on the direct
// surface (FR-009/FR-018).
//
// It is applied on EVERY return path of buildDirectModeTools, including the
// failure paths, because SetTools REPLACES the whole registry: a rebuild that
// omitted the built-ins would delete describe_tool from the live surface until
// some later successful rebuild happened to restore it. A built-in that
// disappears on the first upstream hiccup is not a built-in.
//
// NOTE: describe_tool's direct-id resolver is Phase 4 work. Until it lands, the
// tool is LISTED here and resolves canonical `server:tool` ids only; a direct
// `server__tool` id answers not_found. That is the intended intermediate state —
// FR-009 requires presence in both serialization modes, and presence is what
// this task delivers.
func (p *MCPProxyServer) withDirectBuiltins(tools []mcpserver.ServerTool) []mcpserver.ServerTool {
	return append(tools, mcpserver.ServerTool{
		Tool:    buildDescribeToolTool(),
		Handler: p.handleDescribeTool,
	})
}

// renderDirectTools turns a catalog into the registrable tool set.
//
// It renders FROM the catalog rather than from the raw projection, so the
// listing and the catalog cannot disagree by construction: a display-name
// collision withheld by the catalog is absent from the listing for free, rather
// than needing the same rule implemented twice.
func (p *MCPProxyServer) renderDirectTools(cat *directCatalog) []mcpserver.ServerTool {
	names := cat.DisplayNames()
	serverTools := make([]mcpserver.ServerTool, 0, len(names))

	for _, name := range names {
		entry, ok := cat.Lookup(name)
		if !ok {
			continue
		}

		rendered := fmt.Sprintf("[%s] %s", entry.ServerName, entry.Description)

		opts := []mcp.ToolOption{mcp.WithDescription(rendered)}

		if entry.Annotations != nil {
			if entry.Annotations.Title != "" {
				opts = append(opts, mcp.WithTitleAnnotation(entry.Annotations.Title))
			}
			if entry.Annotations.ReadOnlyHint != nil {
				opts = append(opts, mcp.WithReadOnlyHintAnnotation(*entry.Annotations.ReadOnlyHint))
			}
			if entry.Annotations.DestructiveHint != nil {
				opts = append(opts, mcp.WithDestructiveHintAnnotation(*entry.Annotations.DestructiveHint))
			}
			if entry.Annotations.IdempotentHint != nil {
				opts = append(opts, mcp.WithIdempotentHintAnnotation(*entry.Annotations.IdempotentHint))
			}
			if entry.Annotations.OpenWorldHint != nil {
				opts = append(opts, mcp.WithOpenWorldHintAnnotation(*entry.Annotations.OpenWorldHint))
			}
		}

		mcpTool := mcp.NewTool(entry.DisplayName, opts...)

		if entry.ParamsJSON != "" {
			var schema map[string]interface{}
			if err := json.Unmarshal([]byte(entry.ParamsJSON), &schema); err == nil {
				mcpTool.InputSchema = mcp.ToolInputSchema{Type: "object"}
				if props, ok := schema["properties"].(map[string]interface{}); ok {
					mcpTool.InputSchema.Properties = props
				}
				if req, ok := schema["required"].([]interface{}); ok {
					reqStrings := make([]string, 0, len(req))
					for _, r := range req {
						if str, ok := r.(string); ok {
							reqStrings = append(reqStrings, str)
						}
					}
					mcpTool.InputSchema.Required = reqStrings
				}
			}
		}

		applyToolOutputSchemaJSON(&mcpTool, entry.OutputSchemaJSON)

		// Captured at render time and never recomputed: the signature cache
		// mutates independently of rebuilds, so re-rendering later to compare
		// would report a cache warm/evict as a catalog change (D13 rule 5).
		entry.RenderedDescription = rendered

		serverTools = append(serverTools, mcpserver.ServerTool{
			Tool:    mcpTool,
			Handler: p.makeDirectModeHandler(entry.ServerName, entry.ToolName, entry.Annotations),
		})
	}

	p.logger.Info("built direct mode tools", zap.Int("tool_count", len(serverTools)))

	return serverTools
}

// makeDirectModeHandler creates a handler function for a direct mode tool.
// It handles auth checks, permission enforcement, and upstream calls.
func (p *MCPProxyServer) makeDirectModeHandler(serverName, toolName string, annotations *config.ToolAnnotations) mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		startTime := time.Now()

		// Get session ID for activity logging
		var sessionID string
		if sess := mcpserver.ClientSessionFromContext(ctx); sess != nil {
			sessionID = sess.SessionID()
		}

		// Get request ID from context. Direct-mode calls that did not arrive
		// over an HTTP transport carry none, and every activity this handler
		// emits — including the agent-token and callability blocks below, which
		// fire before anything else — needs an id a consumer can correlate on.
		// Mint one rather than emit anonymously; a transport-supplied id always
		// wins so the records still line up with the access log.
		//
		// Both ids are resolved BEFORE the agent-token gates so those denials
		// can emit a correlatable policy decision like every other block.
		requestID := reqcontext.GetRequestID(ctx)
		if requestID == "" {
			requestID = mintActivityRequestID(serverName, toolName)
		}

		// Spec 057 / Profiles v2: the active profile (token pin > URL > session
		// set_profile) gates direct-mode dispatch exactly as it gates
		// call_tool_* (mcp.go handleCallToolVariant). It runs independently of
		// the agent-token gates below so an unauthenticated /mcp/p/<slug>
		// connection is filtered too, and it runs FIRST so a profile-pinned
		// token cannot reach a server outside its pin through this routing mode.
		if _, profileScope := p.resolveActiveProfile(ctx); profileScope != nil && !profileScope.Allows(serverName) {
			errMsg := fmt.Sprintf("server '%s' is not in profile '%s'", serverName, profileScope.Name)
			p.emitActivityPolicyDecision(serverName, toolName, sessionID, requestID, "blocked", errMsg, telemetry.BlockReasonProfileScope)
			return mcp.NewToolResultError(errMsg), nil
		}

		// Check auth context for server access and permissions
		authCtx := auth.AuthContextFromContext(ctx)
		if authCtx != nil {
			// Check server access
			if !authCtx.CanAccessServer(serverName) {
				errMsg := fmt.Sprintf("Access denied: token does not have access to server '%s'", serverName)
				// Direct mode denied these silently: no activity record and,
				// since issue #969, no availability counter either. Emit the
				// same policy decision the call_tool_* variants emit at the
				// equivalent gate so the funnel has no blind spot.
				p.emitActivityPolicyDecision(serverName, toolName, sessionID, requestID, "blocked", errMsg, telemetry.BlockReasonTokenScope)
				return mcp.NewToolResultError(errMsg), nil
			}

			// Determine required permission from annotations
			requiredVariant := contracts.DeriveCallWith(annotations)
			requiredPerm := contracts.ToolVariantToOperationType[requiredVariant]
			if requiredPerm == "" {
				requiredPerm = contracts.OperationTypeRead
			}

			if !authCtx.HasPermission(requiredPerm) {
				errMsg := fmt.Sprintf("Permission denied: token does not have '%s' permission required for tool '%s:%s'", requiredPerm, serverName, toolName)
				p.emitActivityPolicyDecision(serverName, toolName, sessionID, requestID, "blocked", errMsg, telemetry.BlockReasonTokenPermission)
				return mcp.NewToolResultError(errMsg), nil
			}
		}

		// Get arguments from the request
		args := request.GetArguments()
		enrichedArgs := injectAuthMetadata(ctx, args)

		// Enforce direct-mode callability before emitting a tool-started event or
		// invoking upstream. Direct mode must not bypass disabled, quarantine, or
		// approval controls enforced by call_tool_* variants.
		// The reason key comes from the gate that actually fired (quarantine,
		// pending/changed approval, or plain not-callable) rather than from
		// this one funnel site — see directBlockReasonKey.
		if blocked, reasonKey := p.directToolCallabilityBlockWithReason(ctx, serverName, toolName, enrichedArgs); blocked != nil {
			p.emitActivityPolicyDecision(serverName, toolName, sessionID, requestID, "blocked", "direct tool is not callable", reasonKey)
			return blocked, nil
		}

		// Spec 082: a direct tool call is real work — it earns the session a
		// durable record, and does so BEFORE any activity is emitted so the
		// records carry the right work session.
		p.markSessionWorked(ctx, sessionID)

		// Emit activity event
		p.emitActivityToolCallStarted(serverName, toolName, sessionID, requestID, "mcp", enrichedArgs)

		// Call upstream
		qualifiedName := serverName + ":" + toolName
		result, err := p.upstreamManager.CallTool(ctx, qualifiedName, args)

		durationMs := time.Since(startTime).Milliseconds()

		// Spec 035: Determine content trust based on openWorldHint
		directContentTrust := contracts.ContentTrustForTool(annotations)

		if err != nil {
			// Spec 093 FR-010: direct-routing mode sheds like every other
			// dispatch path — retry-friendly isError result, typed identity kept
			// for the REST 429 mapping, and no duplicate activity record (the
			// limiter seam already wrote the "rejected" one).
			if limitErr, isShed := asShed(err); isShed {
				recordShed(ctx, limitErr)
				return shedToolResult(limitErr), nil
			}
			// Emit error activity
			p.emitActivityToolCallCompleted(serverName, toolName, sessionID, requestID, "mcp", "error", err.Error(), durationMs, enrichedArgs, "", false, "", nil, directContentTrust, "", 0, 0, "", nil, "")
			return mcp.NewToolResultError(fmt.Sprintf("Error calling %s:%s: %v", serverName, toolName, err)), nil
		}

		// Determine tool variant for activity logging
		toolVariant := contracts.DeriveCallWith(annotations)

		// Issue #935: direct mode reaches the same upstreams as call_tool_*, so
		// it must classify an isError:true answer as a failure too. Read from
		// the raw result, before the truncation loop below rewrites it.
		activityStatus, activityErrMsg := activityStatusForResult(result)

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
						txt = txt[:safeTruncateBytes(txt, limit)]
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

		// Emit completion activity (success, or error when the upstream itself
		// reported one — issue #935).
		// Spec 069 A1: pre-truncation sizes; result was measured before the truncation loop above.
		routingResponseBytes := rawByteSize(result)
		routingRequestBytes := rawByteSize(enrichedArgs)
		p.emitActivityToolCallCompleted(serverName, toolName, sessionID, requestID, "mcp", activityStatus, activityErrMsg, durationMs, enrichedArgs, responseText, truncated, toolVariant, nil, directContentTrust, "", routingRequestBytes, routingResponseBytes, "", nil, "")

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
	codeExecRetrieveOpts := []mcp.ToolOption{
		mcp.WithDescription("Search and discover available upstream tools using BM25 full-text search. " +
			"Use this to find tools, then use the `code_execution` tool to call them via `call_tool(serverName, toolName, args)` in JavaScript. " +
			"Do NOT use call_tool_read/write/destructive — they are not available in this mode. " +
			"Use natural language to describe what you want to accomplish. " +
			"Response includes a structured `session_risk` object (level, lethal_trifecta, has_open_world_tools, has_destructive_tools, has_write_tools)." +
			retrieveToolsDiagnosticsNote),
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
		mcp.WithBoolean("include_session_risk_warning",
			mcp.Description("Include the prose 'warning' string in session_risk when the lethal trifecta is detected (default: false; structured fields are always returned). Server-side default can be flipped via the 'tool_response_session_risk_warning' config flag."),
		),
		// Spec 085 FR-011 / spec §Out-of-scope: NO retrieveToolsDetailOption()
		// here. describe_tool is absent from the code-execution surface in v1,
		// so a compact response would reference an unavailable second stage;
		// this mode's retrieve_tools always serializes FULL (enforced in
		// handleRetrieveToolsWithMode) and does not expose the detail param.
	}
	codeExecRetrieveOpts = append(codeExecRetrieveOpts, retrieveToolsAnnotationFilterOptions()...)
	retrieveToolsTool := mcp.NewTool("retrieve_tools", codeExecRetrieveOpts...)
	tools = append(tools, p.setProfileServerTool())
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
	callToolRetrieveOpts := []mcp.ToolOption{
		mcp.WithDescription("Search and discover available upstream tools using BM25 full-text search. " +
			"WORKFLOW: 1) Call this tool first to find relevant tools, 2) Check the 'call_with' field in results " +
			"to determine which variant to use, 3) Call the tool using call_tool_read, call_tool_write, or call_tool_destructive. " +
			"Results include 'annotations' (tool behavior hints like destructiveHint), 'call_with' recommendation, " +
			"and a structured `session_risk` object (level, lethal_trifecta, has_open_world_tools, has_destructive_tools, has_write_tools). " +
			"Compact mode returns one-line signatures ('sig': '*'=required, '~'=lossy) with first-sentence 'desc'; call describe_tool for full schemas. " +
			"Use natural language to describe what you want to accomplish." +
			retrieveToolsDiagnosticsNote),
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
		mcp.WithBoolean("include_session_risk_warning",
			mcp.Description("Include the prose 'warning' string in session_risk when the lethal trifecta is detected (default: false; structured fields are always returned). Server-side default can be flipped via the 'tool_response_session_risk_warning' config flag."),
		),
		retrieveToolsDetailOption(),
	}
	callToolRetrieveOpts = append(callToolRetrieveOpts, retrieveToolsAnnotationFilterOptions()...)
	tools = append(tools, mcpserver.ServerTool{
		Tool:    mcp.NewTool("retrieve_tools", callToolRetrieveOpts...),
		Handler: p.handleRetrieveToolsForMode(config.RoutingModeRetrieveTools),
	})

	// describe_tool — Spec 085 (US2, FR-011): second-stage full definitions,
	// beside retrieve_tools. Retrieve_tools routing mode only in v1: not added
	// to buildCodeExecModeTools or direct mode.
	tools = append(tools, mcpserver.ServerTool{
		Tool:    buildDescribeToolTool(),
		Handler: p.handleDescribeTool,
	})

	// set_profile — Profiles v2 (T2): also available in call-tool mode (/mcp/call,
	// and /mcp/p/<slug> which is served by this same server instance).
	tools = append(tools, p.setProfileServerTool())

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
//
// UX audit F16: this reads the LIVE snapshot, never the construction-time
// p.config. Settings advertises enable_code_execution as an instantly-applied
// field; when this read was pinned to startup, flipping it on left the disabled
// stub — whose handler refuses unconditionally — on the /mcp surface, so the
// tool kept answering "Code execution is disabled" until a restart even though
// the handler-level gate (mcp_code_execution.go) had already gone live. The
// paired half of the fix is RefreshCallToolModeTools/RefreshCodeExecModeTools
// being called on config.reloaded.
func (p *MCPProxyServer) buildCodeExecutionTool() []mcpserver.ServerTool {
	if cfg := p.currentConfig(); cfg != nil && !cfg.EnableCodeExecution {
		// Disabled stub
		codeExecutionTool := mcp.NewTool("code_execution",
			mcp.WithDescription("Code execution is currently disabled. Enable it by setting \"enable_code_execution\": true in your mcpproxy config."),
			mcp.WithTitleAnnotation("Code Execution (Disabled)"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
			// Spec 097: the stub mirrors the live parameter shape — optional
			// `code`, optional `script` — so a stored-script call reaches this
			// handler and gets the "enable it" explanation instead of a schema
			// rejection. Its DESCRIPTIONS stay minimal and disabled-only: a
			// disabled tool must not advertise a contract it cannot honor.
			mcp.WithString("code",
				mcp.Description("JavaScript source code to execute."),
			),
			mcp.WithString("script",
				mcp.Description("Name of a stored script to execute."),
			),
		)
		return []mcpserver.ServerTool{{
			Tool: codeExecutionTool,
			Handler: func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				// Same wording, same typed identity as the handler-level gate:
				// which surface refused must not change what the caller is told.
				recordCodeExecRefusal(ctx, config.ErrCodeExecutionDisabled)
				return mcp.NewToolResultError(config.CodeExecutionDisabledMessage), nil
			},
		}}
	}

	codeExecutionTool := mcp.NewTool("code_execution",
		mcp.WithDescription(codeExecutionToolDescription),
		mcp.WithTitleAnnotation("Code Execution"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
		// Spec 097: optional `code` + optional `script`; the handler enforces
		// the exactly-one-of rule (see mcp.go for the same shape).
		mcp.WithString("code",
			mcp.Description(codeExecutionCodeDescription),
		),
		mcp.WithString("script",
			mcp.Description(codeExecutionScriptDescription),
		),
		mcp.WithString("language",
			mcp.Description(codeExecutionLanguageDescription),
			mcp.Enum("javascript", "typescript"),
		),
		mcp.WithObject("input",
			mcp.Description(codeExecutionInputDescription),
		),
		mcp.WithObject("options",
			mcp.Description(codeExecutionOptionsDescription),
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
	// Advertise prompts on every routing-mode server, not just the default
	// retrieve_tools server: /mcp is served via GetMCPServerForMode, which
	// after config.Validate() normalizes routing_mode almost never returns
	// p.server, so without this the aggregated prompts feature is
	// unreachable over Streamable HTTP (PR #973 review, P1).
	if p.config.EnablePrompts {
		opts = append(opts, mcpserver.WithPromptCapabilities(true))
		// Enforce agent-token + profile scope on aggregated prompts across every
		// routing-mode server. mcp-go applies this on BOTH prompts/list and
		// prompts/get (passesPromptFilters), closing the F1 get-time auth bypass.
		// Added to the shared opts (before directOpts copies it) so directServer,
		// codeExecServer and callToolServer all inherit it; p.server gets the
		// same filter in NewMCPProxyServer, where proxy exists.
		opts = append(opts, mcpserver.WithPromptFilter(p.filterAggregatedPromptsForAuth))
	}

	// Create direct mode server. Both direct-mode tool filters are agent-scoped
	// discovery filters and belong only on the direct server (not the shared
	// code-exec / call-tool servers): filterDirectModeToolsForAuth enforces
	// agent-token server/permission scope, filterDirectToolsForAgentCallability
	// hides tools the agent could not actually invoke.
	directOpts := append([]mcpserver.ServerOption{}, opts...)
	directOpts = append(directOpts,
		mcpserver.WithToolFilter(p.filterDirectModeToolsForAuth),
		mcpserver.WithToolFilter(p.filterDirectToolsForAgentCallability),
	)
	p.directServer = mcpserver.NewMCPServer(
		"mcpproxy-go",
		mcpServerVersion(),
		directOpts...,
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

	// Initial direct rebuild (D15). Done by CALLING RefreshDirectModeTools so
	// there is exactly ONE publisher and one copy of the SetTools-then-publish
	// ordering — a second inline copy here would be the obvious way to introduce
	// the mismatch that ordering exists to prevent.
	//
	// Upstreams are typically not connected yet, so this registers the built-ins
	// and publishes an EMPTY catalog. Both matter: FR-009 needs describe_tool on
	// the surface from the first request rather than from the first upstream
	// reconcile, and a published (non-nil) catalog puts the discovery filters in
	// deny-on-miss immediately instead of leaving them permissive until then.
	p.RefreshDirectModeTools()

	p.logger.Info("routing mode servers initialized",
		zap.String("default_mode", p.config.RoutingMode))
}

// RefreshDirectModeTools rebuilds the direct mode server's tool set.
// Should be called when upstream servers change (connect/disconnect/tool updates).
func (p *MCPProxyServer) RefreshDirectModeTools() {
	if p.directServer == nil {
		return
	}

	// Serialize rebuilds. Today there is exactly one caller — the single serial
	// event loop in listenForRoutingModeRefresh — so this lock is uncontended.
	// It is here because this feature's own roadmap adds two more callers: the
	// initial rebuild in initRoutingModeServers (on the CONSTRUCTOR goroutine,
	// not the listener's) and the config.reloaded branch. Without it, two
	// concurrent rebuilds can interleave as SetTools(A), SetTools(B),
	// publish(A) — leaving catalog A paired with tool map B, which is exactly
	// the mismatch the SetTools-then-publish ordering exists to prevent.
	p.directRefreshMu.Lock()
	defer p.directRefreshMu.Unlock()

	directTools, cat := p.buildDirectModeTools()

	serverTools := make([]mcpserver.ServerTool, len(directTools))
	copy(serverTools, directTools)

	// ORDER IS LOAD-BEARING (D13 rule 1). SetTools lands the registry first, the
	// catalog is published immediately after. The two are separate publications
	// and cannot be made one transaction — mcp-go owns its registry read — so the
	// guarantee is directional: a request may see a registry entry whose catalog
	// entry has not landed yet (the filters deny it, which is safe), but never a
	// catalog entry for a name the registry is not serving.
	p.directServer.SetTools(serverTools...)
	p.publishDirectCatalog(cat)

	p.logger.Info("refreshed direct mode tools",
		zap.Int("tool_count", len(directTools)),
		zap.Uint64("catalog_generation", cat.Generation()))
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

// RefreshCallToolModeTools rebuilds the call-tool mode server's tool set.
//
// UX audit F16: callToolServer is the surface behind /mcp in the DEFAULT
// routing mode (retrieve_tools), and its tools were registered exactly once in
// initRoutingModeServers. Its code_execution entry is therefore the one a
// client sees, and a hot enable_code_execution toggle had no way to replace it.
// buildCallToolModeTools reads the live config snapshot, so re-running it on
// config.reloaded swaps the disabled stub for the live tool (and back).
func (p *MCPProxyServer) RefreshCallToolModeTools() {
	if p.callToolServer == nil {
		return
	}

	callToolTools := p.buildCallToolModeTools()
	serverTools := make([]mcpserver.ServerTool, len(callToolTools))
	copy(serverTools, callToolTools)

	p.callToolServer.SetTools(serverTools...)

	p.logger.Info("refreshed call tool mode tools",
		zap.Int("tool_count", len(callToolTools)))
}

// RefreshCodeExecutionAvailability re-advertises code_execution on every tool
// surface that carries it, so an enable_code_execution flip applies without a
// restart (UX audit F16). directServer is deliberately absent — direct mode
// does not expose code_execution at all.
//
// The stdio surface (p.server) is updated with AddTools rather than SetTools:
// its tool set is assembled once by registerTools and SetTools would drop
// everything else. AddTools replaces the entry with the same name in place, so
// a startup-disabled server that never registered code_execution gains it, and
// an enabled one has it swapped for the disabled stub.
func (p *MCPProxyServer) RefreshCodeExecutionAvailability() {
	p.RefreshCallToolModeTools()
	p.RefreshCodeExecModeTools()
	if p.server != nil {
		p.server.AddTools(p.buildCodeExecutionTool()...)
	}
}

// buildAggregatedServerPrompts combines built-in prompts with upstream
// prompts (colon-qualified "serverName:promptName", as returned by
// Manager.ListPrompts) into the full ServerPrompt set for SetPrompts.
// getPrompt is invoked with the original colon-qualified name whenever a
// client requests one of the aggregated prompts — no reverse-parsing of the
// client-facing "__" name is needed since the handler closure already knows
// which server it came from. Upstream prompts with a malformed (unqualified)
// name are skipped.
func buildAggregatedServerPrompts(
	builtins []mcpserver.ServerPrompt,
	upstreamPrompts []mcp.Prompt,
	getPrompt func(ctx context.Context, name string, args map[string]string) (*mcp.GetPromptResult, error),
	logger *zap.Logger,
) []mcpserver.ServerPrompt {
	all := make([]mcpserver.ServerPrompt, 0, len(builtins)+len(upstreamPrompts))
	all = append(all, builtins...)

	// F7: two distinct (server,prompt) pairs can flatten to the same "__" display
	// name (server "a__b"+prompt "c" and "a"+prompt "b__c" both -> "a__b__c").
	// mcp-go's SetPrompts is last-writer-wins by map order, so without this the
	// loser is dropped silently. Config validation rejects ':' in names but keeps
	// '__' for back-compat, so a residual collision can still occur — keep a
	// deterministic first-writer-wins guard here so it is LOGGED, never silent.
	// Built-in names are seeded into the seen-set so an upstream cannot shadow
	// "setup-new-mcp-server"/"troubleshoot-mcp-server".
	seen := make(map[string]struct{}, len(all)+len(upstreamPrompts))
	for i := range all {
		seen[all[i].Prompt.Name] = struct{}{}
	}

	for _, qualified := range upstreamPrompts {
		serverName, promptName, ok := strings.Cut(qualified.Name, ":")
		if !ok {
			continue
		}

		displayName := FormatDirectPromptName(serverName, promptName)
		if _, dup := seen[displayName]; dup {
			if logger != nil {
				logger.Warn("dropping upstream prompt: display-name collision (kept first)",
					zap.String("server", serverName),
					zap.String("prompt", promptName),
					zap.String("display_name", displayName),
					zap.String("qualified_name", qualified.Name))
			}
			continue
		}
		seen[displayName] = struct{}{}

		qualifiedName := qualified.Name
		display := qualified
		display.Name = displayName

		all = append(all, mcpserver.ServerPrompt{
			Prompt: display,
			Handler: func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return getPrompt(ctx, qualifiedName, request.Params.Arguments)
			},
		})
	}

	return all
}

// RefreshPrompts rebuilds every routing-mode server's prompt set: the built-in
// prompts, plus (only when aggregate_upstream_prompts is enabled) every prompt
// aggregated from connected upstream servers. Should be called when upstream
// servers change (connect/disconnect) or on config hot-reload. A no-op when
// prompts are disabled entirely.
func (p *MCPProxyServer) RefreshPrompts() {
	// Read the LIVE config snapshot (currentConfig()), never the construction-
	// time p.config: p.config is never reassigned on hot-reload, so a boot-
	// snapshot read would make the aggregate_upstream_prompts toggle restart-only
	// (PR #973 review, finding F4).
	cfg := p.currentConfig()
	if cfg == nil || !cfg.EnablePrompts {
		return
	}

	builtins := []mcpserver.ServerPrompt{
		{Prompt: setupServerPrompt(), Handler: p.handleSetupServerPrompt},
		{Prompt: troubleshootServerPrompt(), Handler: p.handleTroubleshootPrompt},
	}

	// Upstream aggregation is opt-in (AggregateUpstreamPrompts, default false):
	// users are safe by default and enable it deliberately. When it is off we
	// still (re-)set the built-ins on every routing-mode server, which also
	// clears any previously-aggregated upstream prompts if the flag was flipped
	// off at runtime.
	var all []mcpserver.ServerPrompt
	if cfg.AggregateUpstreamPrompts {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		upstreamPrompts, err := p.upstreamManager.ListPrompts(ctx)
		if err != nil {
			p.logger.Error("failed to list upstream prompts for refresh", zap.Error(err))
			return
		}
		// F2 layer 2: drop prompts whose name/description/args trip the TPA
		// scanner before they are ever registered (parity with tool-description
		// poisoning detection).
		upstreamPrompts = p.scanAggregatedPrompts(upstreamPrompts)
		// Spec 100: rug-pull baseline. Detect pending/changed metadata vs the
		// approved baseline and WITHHOLD those prompts from registration (compose
		// in series after the TPA scan — scan detects poison, baseline detects
		// change). A withheld prompt is absent from prompts/list and fails
		// prompts/get natively; there is no runtime get-time gate.
		approval := p.checkPromptApprovals(upstreamPrompts)
		upstreamPrompts = filterBlockedPrompts(upstreamPrompts, approval.blocked)
		all = buildAggregatedServerPrompts(builtins, upstreamPrompts, p.getPromptAggregated, p.logger)
		p.logger.Info("refreshed prompts",
			zap.Int("upstream_prompt_count", len(upstreamPrompts)),
			zap.Int("total_prompt_count", len(all)),
			zap.Int("withheld_pending", approval.pending),
			zap.Int("withheld_changed", approval.changed))
	} else {
		// nil upstreamPrompts: the aggregation loop never runs, so the nil
		// getPrompt is never invoked.
		all = buildAggregatedServerPrompts(builtins, nil, nil, p.logger)
		p.logger.Debug("refreshed prompts (upstream aggregation disabled, built-ins only)",
			zap.Int("total_prompt_count", len(all)))
	}

	// Set on every routing-mode server (Spec 031), not just the default
	// retrieve_tools server: /mcp is served via GetMCPServerForMode, which
	// returns a routing-mode server in every non-default mode (PR #973
	// review, P1) — those need the same aggregated prompt set.
	for _, srv := range []*mcpserver.MCPServer{p.server, p.directServer, p.codeExecServer, p.callToolServer} {
		if srv != nil {
			srv.SetPrompts(all...)
		}
	}
}

// promptScanText projects a prompt's client-visible metadata (description + every
// argument name/desc) into one string so a TPA payload hidden in a prompt
// description OR an argument description is scanned the same way a poisoned tool
// description is.
func promptScanText(pr mcp.Prompt) string {
	var b strings.Builder
	b.WriteString(pr.Description)
	for _, a := range pr.Arguments {
		b.WriteByte('\n')
		b.WriteString(a.Name)
		if a.Description != "" {
			b.WriteByte(' ')
			b.WriteString(a.Description)
		}
	}
	return b.String()
}

// scanAggregatedPrompts runs the deterministic, offline TPA scanner over each
// aggregated upstream prompt's name+description+arguments and DROPS any prompt
// whose baseline verdict is "dangerous" (hard-tier: hidden-unicode, decoded
// payload, curated injection/exfiltration phrases). This is the prompt analogue
// of the tool-description TPA scan. A "warnings"/"clean" verdict is kept
// (dropping on soft signals would blackhole legitimate prompts). Per-prompt
// scanning is cheap (offline, cached bundle) and RefreshPrompts is off the
// request hot path (Finding F2, layer 2).
func (p *MCPProxyServer) scanAggregatedPrompts(prompts []mcp.Prompt) []mcp.Prompt {
	if len(prompts) == 0 {
		return prompts
	}
	kept := make([]mcp.Prompt, 0, len(prompts))
	for _, pr := range prompts {
		serverName, promptName, ok := strings.Cut(pr.Name, ":")
		if !ok {
			kept = append(kept, pr) // malformed name — dropped later by buildAggregatedServerPrompts
			continue
		}
		meta := &config.ToolMetadata{
			ServerName:  serverName,
			Name:        promptName,
			Description: promptScanText(pr),
		}
		// Schema v9: one counter increment per PROMPT actually put through the
		// scanner (malformed names short-circuit above and are not counted).
		// Invocation count only — never the prompt, the server, or the verdict.
		telemetry.RecordTPAPromptScanOn(p.telemetryRegistry())
		verdict, findings, _ := scanner.ScanToolMetadataVerdict(serverName, []*config.ToolMetadata{meta}, nil)
		if verdict == "dangerous" {
			signals := make([]string, 0, len(findings))
			for _, f := range findings {
				signals = append(signals, f.RuleID)
			}
			p.logger.Warn("Dropping upstream prompt: poisoned description (TPA scan)",
				zap.String("server", serverName),
				zap.String("prompt", promptName),
				zap.String("verdict", verdict),
				zap.Strings("tpa_signals", signals))
			continue
		}
		kept = append(kept, pr)
	}
	return kept
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
