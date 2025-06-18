package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/index"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/upstream"
)

// MCPProxyServer implements an MCP server that acts as a proxy
type MCPProxyServer struct {
	server          *mcpserver.MCPServer
	storage         *storage.Manager
	index           *index.Manager
	upstreamManager *upstream.Manager
	logger          *zap.Logger
}

// NewMCPProxyServer creates a new MCP proxy server
func NewMCPProxyServer(
	storage *storage.Manager,
	index *index.Manager,
	upstreamManager *upstream.Manager,
	logger *zap.Logger,
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
		logger:          logger,
	}

	// Register proxy tools
	proxy.registerTools()

	return proxy
}

// registerTools registers all proxy tools with the MCP server
func (p *MCPProxyServer) registerTools() {
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
		mcp.WithDescription("Manage upstream MCP servers (list, add, remove, update)"),
		mcp.WithString("operation",
			mcp.Required(),
			mcp.Description("Operation to perform: list, add, remove, update"),
			mcp.Enum("list", "add", "remove", "update"),
		),
		mcp.WithString("name",
			mcp.Description("Server name (required for add/remove/update)"),
		),
		mcp.WithString("url",
			mcp.Description("Server URL or command (required for add/update)"),
		),
		mcp.WithString("type",
			mcp.Description("Transport type: http or stdio (default: auto-detect)"),
			mcp.Enum("http", "stdio", "auto"),
		),
		mcp.WithBoolean("enabled",
			mcp.Description("Whether server is enabled (for add/update)"),
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

	return mcp.NewToolResultText(string(jsonResult)), nil
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
	case "remove":
		return p.handleRemoveUpstream(ctx, request)
	case "update":
		return p.handleUpdateUpstream(ctx, request)
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

	url, err := request.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError("Missing required parameter 'url'"), nil
	}

	transportType := request.GetString("type", "auto")

	enabled := request.GetBool("enabled", true)

	serverConfig := &config.ServerConfig{
		Name:    name,
		URL:     url,
		Type:    transportType,
		Enabled: enabled,
	}

	id, err := p.storage.AddUpstream(serverConfig)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to add upstream: %v", err)), nil
	}

	// Add to upstream manager if enabled
	if enabled {
		if err := p.upstreamManager.AddServer(id, serverConfig); err != nil {
			p.logger.Warn("Failed to connect to upstream", zap.String("id", id), zap.Error(err))
		}
	}

	jsonResult, err := json.Marshal(map[string]interface{}{
		"id":      id,
		"name":    name,
		"enabled": enabled,
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
	if transportType := request.GetString("type", ""); transportType != "" {
		updatedServer.Type = transportType
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

// GetMCPServer returns the underlying MCP server for serving
func (p *MCPProxyServer) GetMCPServer() *mcpserver.MCPServer {
	return p.server
}
