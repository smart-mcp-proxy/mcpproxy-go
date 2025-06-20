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

	"mcpproxy-go/internal/cache"
	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/truncate"
	"mcpproxy-go/internal/upstream"
)

// MCPProxyServer implements an MCP server that acts as a proxy
type MCPProxyServer struct {
	server          *mcpserver.MCPServer
	storage         *storage.Manager
	index           *index.Manager
	upstreamManager *upstream.Manager
	cacheManager    *cache.Manager
	truncator       *truncate.Truncator
	logger          *zap.Logger
	mainServer      *Server // Reference to main server for config persistence
}

// NewMCPProxyServer creates a new MCP proxy server
func NewMCPProxyServer(
	storage *storage.Manager,
	index *index.Manager,
	upstreamManager *upstream.Manager,
	cacheManager *cache.Manager,
	truncator *truncate.Truncator,
	logger *zap.Logger,
	mainServer *Server,
	debugSearch bool,
) *MCPProxyServer {
	// Create MCP server with tool capabilities
	mcpServer := mcpserver.NewMCPServer(
		"mcpproxy-go",
		"1.0.0",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithRecovery(),
	)

	proxy := &MCPProxyServer{
		server:          mcpServer,
		storage:         storage,
		index:           index,
		upstreamManager: upstreamManager,
		cacheManager:    cacheManager,
		truncator:       truncator,
		logger:          logger,
		mainServer:      mainServer,
	}

	// Register proxy tools
	proxy.registerTools(debugSearch)

	return proxy
}

// registerTools registers all proxy tools with the MCP server
func (p *MCPProxyServer) registerTools(debugSearch bool) {
	// retrieve_tools - search for tools across all upstream servers
	retrieveToolsTool := mcp.NewTool("retrieve_tools",
		mcp.WithDescription("Search for tools across all upstream MCP servers using BM25 full-text search"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query to find relevant tools"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results to return (default: 20)"),
		),
	)
	p.server.AddTool(retrieveToolsTool, p.handleRetrieveTools)

	// call_tool - call a tool on an upstream server
	callToolTool := mcp.NewTool("call_tool",
		mcp.WithDescription("Call a tool on an upstream MCP server"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Tool name in format 'server:tool' (e.g., 'sqlite:query')"),
		),
		mcp.WithObject("args",
			mcp.Description("Arguments to pass to the tool"),
		),
	)
	p.server.AddTool(callToolTool, p.handleCallTool)

	// upstream_servers - manage upstream MCP servers
	upstreamServersTool := mcp.NewTool("upstream_servers",
		mcp.WithDescription("Manage upstream MCP servers - supports adding single/multiple servers, updating, removing, and importing from Cursor IDE format"),
		mcp.WithString("operation",
			mcp.Required(),
			mcp.Description("Operation: list, add, add_batch, remove, update, patch, import_cursor"),
			mcp.Enum("list", "add", "add_batch", "remove", "update", "patch", "import_cursor"),
		),
		mcp.WithString("name",
			mcp.Description("Server name (required for add/remove/update/patch)"),
		),
		mcp.WithString("command",
			mcp.Description("Command to run (for stdio servers)"),
		),
		mcp.WithArray("args",
			mcp.Description("Command arguments (for stdio servers)"),
		),
		mcp.WithObject("env",
			mcp.Description("Environment variables (for stdio servers)"),
		),
		mcp.WithString("url",
			mcp.Description("Server URL (for HTTP/SSE servers)"),
		),
		mcp.WithString("protocol",
			mcp.Description("Transport protocol: stdio (for commands), http, sse, streamable-http (default: auto-detect)"),
			mcp.Enum("stdio", "http", "sse", "streamable-http", "auto"),
		),
		mcp.WithObject("headers",
			mcp.Description("HTTP headers for authentication (for HTTP/SSE servers)"),
		),
		mcp.WithBoolean("enabled",
			mcp.Description("Whether server is enabled (default: true)"),
		),
		mcp.WithArray("servers",
			mcp.Description("Array of server configurations for batch operations"),
		),
		mcp.WithObject("cursor_config",
			mcp.Description("Cursor IDE mcpServers configuration to import"),
		),
		mcp.WithObject("patch",
			mcp.Description("Fields to patch/update for existing server"),
		),
	)
	p.server.AddTool(upstreamServersTool, p.handleUpstreamServers)

	// tools_stats - get tool usage statistics
	toolsStatsTool := mcp.NewTool("tools_stats",
		mcp.WithDescription("Get tool usage statistics and metrics"),
		mcp.WithNumber("top_n",
			mcp.Description("Number of top tools to return (default: 10)"),
		),
	)
	p.server.AddTool(toolsStatsTool, p.handleToolsStats)

	// read_cache - retrieve cached tool response data with pagination
	readCacheTool := mcp.NewTool("read_cache",
		mcp.WithDescription("Retrieve cached tool response data with pagination - use this when mcpproxy indicates data was truncated"),
		mcp.WithString("key",
			mcp.Required(),
			mcp.Description("Cache key provided by mcpproxy when response was truncated"),
		),
		mcp.WithNumber("offset",
			mcp.Description("Starting record offset (default: 0)"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of records to return (default: 50)"),
		),
	)
	p.server.AddTool(readCacheTool, p.handleReadCache)

	// debug_search - debug search relevancy (only if enabled)
	if debugSearch {
		debugSearchTool := mcp.NewTool("debug_search",
			mcp.WithDescription("Debug search relevancy - shows detailed scoring and why tools were/weren't ranked highly"),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("Search query to analyze"),
			),
			mcp.WithNumber("limit",
				mcp.Description("Maximum number of results to return (default: 50)"),
			),
			mcp.WithString("explain_tool",
				mcp.Description("Specific tool name to explain why it was ranked low (format: 'server:tool')"),
			),
			mcp.WithBoolean("verbose",
				mcp.Description("Include detailed scoring explanation (default: false)"),
			),
		)
		p.server.AddTool(debugSearchTool, p.handleDebugSearch)
	}
}

// handleRetrieveTools implements the retrieve_tools functionality
func (p *MCPProxyServer) handleRetrieveTools(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Missing required parameter 'query': %v", err)), nil
	}

	// Get optional limit parameter
	limit := request.GetFloat("limit", 20.0)

	// Perform search using index manager
	results, err := p.index.Search(query, int(limit))
	if err != nil {
		p.logger.Error("Search failed", zap.String("query", query), zap.Error(err))
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}

	// Convert results to MCP tool format for LLM compatibility
	var mcpTools []map[string]interface{}
	for _, result := range results {
		// Parse the input schema from ParamsJSON
		var inputSchema map[string]interface{}
		if result.Tool.ParamsJSON != "" {
			if err := json.Unmarshal([]byte(result.Tool.ParamsJSON), &inputSchema); err != nil {
				p.logger.Warn("Failed to parse tool params JSON",
					zap.String("tool_name", result.Tool.Name),
					zap.Error(err))
				inputSchema = map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				}
			}
		} else {
			inputSchema = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		// Create MCP-compatible tool representation
		mcpTool := map[string]interface{}{
			"name":        result.Tool.Name,
			"description": result.Tool.Description,
			"inputSchema": inputSchema,
			"score":       result.Score,
			"server":      result.Tool.ServerName,
		}
		mcpTools = append(mcpTools, mcpTool)
	}

	response := map[string]interface{}{
		"tools": mcpTools,
		"query": query,
		"total": len(results),
	}

	jsonResult, err := json.Marshal(response)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize results: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}

// handleCallTool implements the call_tool functionality
func (p *MCPProxyServer) handleCallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	toolName, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Missing required parameter 'name': %v", err)), nil
	}

	// Get optional args parameter - this should be from the "args" field, not all arguments
	var args map[string]interface{}
	if request.Params.Arguments != nil {
		if argumentsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
			if argsParam, ok := argumentsMap["args"]; ok {
				if argsMap, ok := argsParam.(map[string]interface{}); ok {
					args = argsMap
				}
			}
		}
	}

	// Call tool via upstream manager
	result, err := p.upstreamManager.CallTool(ctx, toolName, args)
	if err != nil {
		p.logger.Error("Tool call failed",
			zap.String("tool_name", toolName),
			zap.Any("args", args),
			zap.Error(err))
		return mcp.NewToolResultError(fmt.Sprintf("Tool call failed: %v", err)), nil
	}

	// Increment usage stats
	if err := p.storage.IncrementToolUsage(toolName); err != nil {
		p.logger.Warn("Failed to update tool stats", zap.String("tool_name", toolName), zap.Error(err))
	}

	// Convert result to JSON string
	jsonResult, err := json.Marshal(result)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize result: %v", err)), nil
	}

	response := string(jsonResult)

	// Apply truncation if configured
	if p.truncator.ShouldTruncate(response) {
		truncResult := p.truncator.Truncate(response, toolName, args)

		// If caching is available, store the full response
		if truncResult.CacheAvailable {
			if err := p.cacheManager.Store(
				truncResult.CacheKey,
				toolName,
				args,
				response,
				truncResult.RecordPath,
				truncResult.TotalRecords,
			); err != nil {
				p.logger.Error("Failed to cache response",
					zap.String("tool_name", toolName),
					zap.String("cache_key", truncResult.CacheKey),
					zap.Error(err))
				// Fall back to simple truncation if caching fails
				truncResult.TruncatedContent = p.truncator.Truncate(response, toolName, args).TruncatedContent
				truncResult.CacheAvailable = false
			}
		}

		response = truncResult.TruncatedContent
	}

	return mcp.NewToolResultText(response), nil
}

// handleUpstreamServers implements upstream server management
func (p *MCPProxyServer) handleUpstreamServers(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	operation, err := request.RequireString("operation")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Missing required parameter 'operation': %v", err)), nil
	}

	switch operation {
	case "list":
		return p.handleListUpstreams(ctx)
	case "add":
		return p.handleAddUpstream(ctx, request)
	case "add_batch":
		return p.handleAddBatchUpstreams(ctx, request)
	case "remove":
		return p.handleRemoveUpstream(ctx, request)
	case "update":
		return p.handleUpdateUpstream(ctx, request)
	case "patch":
		return p.handlePatchUpstream(ctx, request)
	case "import_cursor":
		return p.handleImportCursor(ctx, request)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("Unknown operation: %s", operation)), nil
	}
}

func (p *MCPProxyServer) handleListUpstreams(ctx context.Context) (*mcp.CallToolResult, error) {
	servers, err := p.storage.ListUpstreamServers()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list upstreams: %v", err)), nil
	}

	jsonResult, err := json.Marshal(map[string]interface{}{
		"servers": servers,
		"total":   len(servers),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize servers: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}

func (p *MCPProxyServer) handleAddUpstream(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("Missing required parameter 'name'"), nil
	}

	url := request.GetString("url", "")
	command := request.GetString("command", "")
	enabled := request.GetBool("enabled", true)

	// Must have either URL or command
	if url == "" && command == "" {
		return mcp.NewToolResultError("Either 'url' or 'command' parameter is required"), nil
	}

	// Handle args array
	var args []string
	if request.Params.Arguments != nil {
		if argumentsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
			if argsParam, ok := argumentsMap["args"]; ok {
				if argsList, ok := argsParam.([]interface{}); ok {
					for _, arg := range argsList {
						if argStr, ok := arg.(string); ok {
							args = append(args, argStr)
						}
					}
				}
			}
		}
	}

	// Handle env map
	var env map[string]string
	if request.Params.Arguments != nil {
		if argumentsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
			if envParam, ok := argumentsMap["env"]; ok {
				if envMap, ok := envParam.(map[string]interface{}); ok {
					env = make(map[string]string)
					for k, v := range envMap {
						if vStr, ok := v.(string); ok {
							env[k] = vStr
						}
					}
				}
			}
		}
	}

	// Handle headers map
	var headers map[string]string
	if request.Params.Arguments != nil {
		if argumentsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
			if headersParam, ok := argumentsMap["headers"]; ok {
				if headersMap, ok := headersParam.(map[string]interface{}); ok {
					headers = make(map[string]string)
					for k, v := range headersMap {
						if vStr, ok := v.(string); ok {
							headers[k] = vStr
						}
					}
				}
			}
		}
	}

	// Auto-detect protocol
	protocol := request.GetString("protocol", "")
	if protocol == "" {
		if command != "" {
			protocol = "stdio"
		} else if url != "" {
			protocol = "streamable-http"
		} else {
			protocol = "auto"
		}
	}

	serverConfig := &config.ServerConfig{
		Name:     name,
		URL:      url,
		Command:  command,
		Args:     args,
		Env:      env,
		Headers:  headers,
		Protocol: protocol,
		Enabled:  enabled,
		Created:  time.Now(),
	}

	// Save to storage
	if err := p.storage.SaveUpstreamServer(serverConfig); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to add upstream: %v", err)), nil
	}

	// Add to upstream manager if enabled
	if enabled {
		if err := p.upstreamManager.AddServer(name, serverConfig); err != nil {
			p.logger.Warn("Failed to connect to upstream", zap.String("name", name), zap.Error(err))
		}
	}

	// Trigger configuration save and update
	if p.mainServer != nil {
		p.mainServer.OnUpstreamServerChange()
	}

	jsonResult, err := json.Marshal(map[string]interface{}{
		"name":     name,
		"protocol": protocol,
		"enabled":  enabled,
		"added":    true,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}

func (p *MCPProxyServer) handleAddBatchUpstreams(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var servers []interface{}
	if request.Params.Arguments != nil {
		if argumentsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
			if serversParam, ok := argumentsMap["servers"]; ok {
				if serversList, ok := serversParam.([]interface{}); ok {
					servers = serversList
				}
			}
		}
	}

	if len(servers) == 0 {
		return mcp.NewToolResultError("Missing required parameter 'servers'"), nil
	}

	var serverConfigs []*config.ServerConfig
	for _, server := range servers {
		if serverMap, ok := server.(map[string]interface{}); ok {
			name, _ := serverMap["name"].(string)
			url, _ := serverMap["url"].(string)
			command, _ := serverMap["command"].(string)
			transportType, _ := serverMap["type"].(string)
			enabled, _ := serverMap["enabled"].(bool)

			// Handle args array
			var args []string
			if argsParam, ok := serverMap["args"].([]interface{}); ok {
				for _, arg := range argsParam {
					if argStr, ok := arg.(string); ok {
						args = append(args, argStr)
					}
				}
			}

			// Handle env map
			var env map[string]string
			if envParam, ok := serverMap["env"].(map[string]interface{}); ok {
				env = make(map[string]string)
				for k, v := range envParam {
					if vStr, ok := v.(string); ok {
						env[k] = vStr
					}
				}
			}

			// Handle headers map
			var headers map[string]string
			if headersParam, ok := serverMap["headers"].(map[string]interface{}); ok {
				headers = make(map[string]string)
				for k, v := range headersParam {
					if vStr, ok := v.(string); ok {
						headers[k] = vStr
					}
				}
			}

			// Auto-detect protocol if not specified
			if transportType == "" {
				if command != "" {
					transportType = "stdio"
				} else if url != "" {
					transportType = "streamable-http"
				} else {
					transportType = "auto"
				}
			}

			// Default enabled to true
			if !enabled && url != "" || command != "" {
				enabled = true
			}

			serverConfig := &config.ServerConfig{
				Name:     name,
				URL:      url,
				Command:  command,
				Args:     args,
				Env:      env,
				Headers:  headers,
				Protocol: transportType,
				Enabled:  enabled,
				Created:  time.Now(),
			}
			serverConfigs = append(serverConfigs, serverConfig)
		}
	}

	// Add servers individually using existing method
	var ids []string
	for _, serverConfig := range serverConfigs {
		if err := p.storage.SaveUpstreamServer(serverConfig); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to add upstream %s: %v", serverConfig.Name, err)), nil
		}
		ids = append(ids, serverConfig.Name)

		// Add to upstream manager if enabled
		if serverConfig.Enabled {
			if err := p.upstreamManager.AddServer(serverConfig.Name, serverConfig); err != nil {
				p.logger.Warn("Failed to connect to upstream", zap.String("id", serverConfig.Name), zap.Error(err))
			}
		}
	}

	jsonResult, err := json.Marshal(map[string]interface{}{
		"ids":   ids,
		"total": len(ids),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}

func (p *MCPProxyServer) handleRemoveUpstream(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("Missing required parameter 'name'"), nil
	}

	// Find server by name first
	servers, err := p.storage.ListUpstreams()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list upstreams: %v", err)), nil
	}

	var serverID string
	for _, server := range servers {
		if server.Name == name {
			serverID = server.Name
			break
		}
	}

	if serverID == "" {
		return mcp.NewToolResultError(fmt.Sprintf("Server '%s' not found", name)), nil
	}

	// Remove from storage
	if err := p.storage.RemoveUpstream(serverID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to remove upstream: %v", err)), nil
	}

	// Remove from upstream manager
	p.upstreamManager.RemoveServer(serverID)

	// Trigger configuration save and update
	if p.mainServer != nil {
		p.mainServer.OnUpstreamServerChange()
	}

	jsonResult, err := json.Marshal(map[string]interface{}{
		"id":      serverID,
		"name":    name,
		"removed": true,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}

func (p *MCPProxyServer) handleUpdateUpstream(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("Missing required parameter 'name'"), nil
	}

	// Find server by name first
	servers, err := p.storage.ListUpstreams()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list upstreams: %v", err)), nil
	}

	var serverID string
	var existingServer *config.ServerConfig
	for _, server := range servers {
		if server.Name == name {
			serverID = server.Name
			existingServer = server
			break
		}
	}

	if serverID == "" {
		return mcp.NewToolResultError(fmt.Sprintf("Server '%s' not found", name)), nil
	}

	// Update fields if provided
	updatedServer := *existingServer
	if url := request.GetString("url", ""); url != "" {
		updatedServer.URL = url
	}
	if protocol := request.GetString("protocol", ""); protocol != "" {
		updatedServer.Protocol = protocol
	}
	updatedServer.Enabled = request.GetBool("enabled", updatedServer.Enabled)

	// Update in storage
	if err := p.storage.UpdateUpstream(serverID, &updatedServer); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to update upstream: %v", err)), nil
	}

	// Update in upstream manager
	p.upstreamManager.RemoveServer(serverID)
	if updatedServer.Enabled {
		if err := p.upstreamManager.AddServer(serverID, &updatedServer); err != nil {
			p.logger.Warn("Failed to connect to updated upstream", zap.String("id", serverID), zap.Error(err))
		}
	}

	// Trigger configuration save and update
	if p.mainServer != nil {
		p.mainServer.OnUpstreamServerChange()
	}

	jsonResult, err := json.Marshal(map[string]interface{}{
		"id":      serverID,
		"name":    name,
		"updated": true,
		"enabled": updatedServer.Enabled,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}

func (p *MCPProxyServer) handlePatchUpstream(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError("Missing required parameter 'name'"), nil
	}

	// Find server by name first
	servers, err := p.storage.ListUpstreams()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list upstreams: %v", err)), nil
	}

	var serverID string
	var existingServer *config.ServerConfig
	for _, server := range servers {
		if server.Name == name {
			serverID = server.Name
			existingServer = server
			break
		}
	}

	if serverID == "" {
		return mcp.NewToolResultError(fmt.Sprintf("Server '%s' not found", name)), nil
	}

	// Update fields if provided
	updatedServer := *existingServer
	if url := request.GetString("url", ""); url != "" {
		updatedServer.URL = url
	}
	if protocol := request.GetString("protocol", ""); protocol != "" {
		updatedServer.Protocol = protocol
	}
	updatedServer.Enabled = request.GetBool("enabled", updatedServer.Enabled)

	// Update in storage
	if err := p.storage.UpdateUpstream(serverID, &updatedServer); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to update upstream: %v", err)), nil
	}

	// Update in upstream manager
	p.upstreamManager.RemoveServer(serverID)
	if updatedServer.Enabled {
		if err := p.upstreamManager.AddServer(serverID, &updatedServer); err != nil {
			p.logger.Warn("Failed to connect to updated upstream", zap.String("id", serverID), zap.Error(err))
		}
	}

	// Trigger configuration save and update
	if p.mainServer != nil {
		p.mainServer.OnUpstreamServerChange()
	}

	jsonResult, err := json.Marshal(map[string]interface{}{
		"id":      serverID,
		"name":    name,
		"updated": true,
		"enabled": updatedServer.Enabled,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}

func (p *MCPProxyServer) handleImportCursor(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var cursorConfig map[string]interface{}
	if request.Params.Arguments != nil {
		if argumentsMap, ok := request.Params.Arguments.(map[string]interface{}); ok {
			if configParam, ok := argumentsMap["cursor_config"]; ok {
				if configMap, ok := configParam.(map[string]interface{}); ok {
					cursorConfig = configMap
				}
			}
		}
	}

	if len(cursorConfig) == 0 {
		return mcp.NewToolResultError("Missing required parameter 'cursor_config'"), nil
	}

	var serverConfigs []*config.ServerConfig
	for name, serverConfig := range cursorConfig {
		if configMap, ok := serverConfig.(map[string]interface{}); ok {
			url, _ := configMap["url"].(string)
			command, _ := configMap["command"].(string)
			transportType, _ := configMap["type"].(string)
			enabled, _ := configMap["enabled"].(bool)

			// Handle args array
			var args []string
			if argsParam, ok := configMap["args"].([]interface{}); ok {
				for _, arg := range argsParam {
					if argStr, ok := arg.(string); ok {
						args = append(args, argStr)
					}
				}
			}

			// Handle env map
			var env map[string]string
			if envParam, ok := configMap["env"].(map[string]interface{}); ok {
				env = make(map[string]string)
				for k, v := range envParam {
					if vStr, ok := v.(string); ok {
						env[k] = vStr
					}
				}
			}

			// Handle headers map
			var headers map[string]string
			if headersParam, ok := configMap["headers"].(map[string]interface{}); ok {
				headers = make(map[string]string)
				for k, v := range headersParam {
					if vStr, ok := v.(string); ok {
						headers[k] = vStr
					}
				}
			}

			// Auto-detect protocol if not specified
			if transportType == "" {
				if command != "" {
					transportType = "stdio"
				} else if url != "" {
					transportType = "streamable-http"
				} else {
					transportType = "auto"
				}
			}

			// Default enabled to true
			if !enabled && (url != "" || command != "") {
				enabled = true
			}

			serverConfig := &config.ServerConfig{
				Name:     name,
				URL:      url,
				Command:  command,
				Args:     args,
				Env:      env,
				Headers:  headers,
				Protocol: transportType,
				Enabled:  enabled,
				Created:  time.Now(),
			}
			serverConfigs = append(serverConfigs, serverConfig)
		}
	}

	// Add servers individually using existing method
	var ids []string
	for _, serverConfig := range serverConfigs {
		if err := p.storage.SaveUpstreamServer(serverConfig); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to import server %s: %v", serverConfig.Name, err)), nil
		}
		ids = append(ids, serverConfig.Name)

		// Add to upstream manager if enabled
		if serverConfig.Enabled {
			if err := p.upstreamManager.AddServer(serverConfig.Name, serverConfig); err != nil {
				p.logger.Warn("Failed to connect to upstream", zap.String("id", serverConfig.Name), zap.Error(err))
			}
		}
	}

	// Trigger configuration save and update
	if p.mainServer != nil {
		p.mainServer.OnUpstreamServerChange()
	}

	jsonResult, err := json.Marshal(map[string]interface{}{
		"imported_servers": ids,
		"total":            len(ids),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize result: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}

// handleToolsStats implements tool statistics functionality
func (p *MCPProxyServer) handleToolsStats(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topN := request.GetFloat("top_n", 10.0)

	stats, err := p.storage.GetToolStats(int(topN))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to get tool stats: %v", err)), nil
	}

	// Get total tool count from index
	totalTools := p.upstreamManager.GetTotalToolCount()

	response := map[string]interface{}{
		"total_tools": totalTools,
		"top_tools":   stats,
	}

	jsonResult, err := json.Marshal(response)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize stats: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}

// handleDebugSearch implements the debug_search functionality
func (p *MCPProxyServer) handleDebugSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Missing required parameter 'query': %v", err)), nil
	}

	// Get optional limit parameter
	limit := request.GetFloat("limit", 50.0)

	// Get optional explain_tool parameter
	explainTool := request.GetString("explain_tool", "")

	// Get optional verbose parameter
	verbose := request.GetBool("verbose", false)

	// Perform search using index manager
	results, err := p.index.Search(query, int(limit))
	if err != nil {
		p.logger.Error("Search failed", zap.String("query", query), zap.Error(err))
		return mcp.NewToolResultError(fmt.Sprintf("Search failed: %v", err)), nil
	}

	// Convert results to MCP tool format for LLM compatibility
	var mcpTools []map[string]interface{}
	for _, result := range results {
		// Parse the input schema from ParamsJSON
		var inputSchema map[string]interface{}
		if result.Tool.ParamsJSON != "" {
			if err := json.Unmarshal([]byte(result.Tool.ParamsJSON), &inputSchema); err != nil {
				p.logger.Warn("Failed to parse tool params JSON",
					zap.String("tool_name", result.Tool.Name),
					zap.Error(err))
				inputSchema = map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				}
			}
		} else {
			inputSchema = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		// Create MCP-compatible tool representation
		mcpTool := map[string]interface{}{
			"name":        result.Tool.Name,
			"description": result.Tool.Description,
			"inputSchema": inputSchema,
			"score":       result.Score,
			"server":      result.Tool.ServerName,
		}
		mcpTools = append(mcpTools, mcpTool)
	}

	response := map[string]interface{}{
		"tools": mcpTools,
		"query": query,
		"total": len(results),
	}

	// Add debug information
	response["debug"] = map[string]interface{}{
		"total_indexed_tools": p.getIndexedToolCount(),
		"search_backend":      "BM25",
		"query_analysis":      p.analyzeQuery(query),
		"verbose":             verbose,
	}

	if explainTool != "" {
		explanation := p.explainToolRanking(query, explainTool, results)
		response["explanation"] = explanation
	}

	jsonResult, err := json.Marshal(response)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize results: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}

// getIndexedToolCount returns the total number of indexed tools
func (p *MCPProxyServer) getIndexedToolCount() int {
	count, err := p.index.GetDocumentCount()
	if err != nil {
		p.logger.Warn("Failed to get document count", zap.Error(err))
		return 0
	}
	return int(count)
}

// analyzeQuery analyzes the search query and provides insights
func (p *MCPProxyServer) analyzeQuery(query string) map[string]interface{} {
	analysis := map[string]interface{}{
		"original_query":  query,
		"query_length":    len(query),
		"word_count":      len(strings.Fields(query)),
		"has_underscores": strings.Contains(query, "_"),
		"has_colons":      strings.Contains(query, ":"),
		"is_tool_name":    strings.Contains(query, ":"),
	}

	// Check if query looks like a tool name pattern
	if strings.Contains(query, ":") {
		parts := strings.SplitN(query, ":", 2)
		if len(parts) == 2 {
			analysis["server_part"] = parts[0]
			analysis["tool_part"] = parts[1]
		}
	}

	return analysis
}

// explainToolRanking explains why a specific tool was ranked as it was
func (p *MCPProxyServer) explainToolRanking(query string, targetTool string, results []*config.SearchResult) map[string]interface{} {
	explanation := map[string]interface{}{
		"target_tool":      targetTool,
		"query":            query,
		"found_in_results": false,
		"rank":             -1,
	}

	// Find the tool in results
	for i, result := range results {
		if result.Tool.Name == targetTool {
			explanation["found_in_results"] = true
			explanation["rank"] = i + 1
			explanation["score"] = result.Score
			explanation["tool_details"] = map[string]interface{}{
				"name":        result.Tool.Name,
				"server":      result.Tool.ServerName,
				"description": result.Tool.Description,
				"has_params":  len(result.Tool.ParamsJSON) > 0,
			}
			break
		}
	}

	// Analyze why tool might not rank well
	reasons := []string{}
	if !strings.Contains(targetTool, query) {
		reasons = append(reasons, "Tool name doesn't contain query terms")
	}
	if strings.Contains(targetTool, "_") && !strings.Contains(query, "_") {
		reasons = append(reasons, "Tool name has underscores but query doesn't - exact matching issues")
	}
	if len(query) < 3 {
		reasons = append(reasons, "Query too short for effective BM25 scoring")
	}

	explanation["potential_issues"] = reasons

	// Suggest improvements
	suggestions := []string{}
	if strings.Contains(targetTool, ":") {
		parts := strings.SplitN(targetTool, ":", 2)
		if len(parts) == 2 {
			suggestions = append(suggestions, fmt.Sprintf("Try searching for server name: '%s'", parts[0]))
			suggestions = append(suggestions, fmt.Sprintf("Try searching for tool name: '%s'", parts[1]))
			if strings.Contains(parts[1], "_") {
				words := strings.Split(parts[1], "_")
				suggestions = append(suggestions, fmt.Sprintf("Try searching for individual words: '%s'", strings.Join(words, " ")))
			}
		}
	}

	explanation["suggestions"] = suggestions

	return explanation
}

// handleReadCache implements the read_cache functionality
func (p *MCPProxyServer) handleReadCache(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	key, err := request.RequireString("key")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Missing required parameter 'key': %v", err)), nil
	}

	// Get optional parameters
	offset := int(request.GetFloat("offset", 0))
	limit := int(request.GetFloat("limit", 50))

	// Validate parameters
	if offset < 0 {
		return mcp.NewToolResultError("Offset must be non-negative"), nil
	}
	if limit <= 0 || limit > 1000 {
		return mcp.NewToolResultError("Limit must be between 1 and 1000"), nil
	}

	// Retrieve cached data
	response, err := p.cacheManager.GetRecords(key, offset, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to retrieve cached data: %v", err)), nil
	}

	// Serialize response
	jsonResult, err := json.Marshal(response)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to serialize response: %v", err)), nil
	}

	return mcp.NewToolResultText(string(jsonResult)), nil
}

// GetMCPServer returns the underlying MCP server for serving
func (p *MCPProxyServer) GetMCPServer() *mcpserver.MCPServer {
	return p.server
}
