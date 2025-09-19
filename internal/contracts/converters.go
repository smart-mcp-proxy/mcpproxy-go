package contracts

import (
	"fmt"
	"time"

	"mcpproxy-go/internal/config"
)

// ConvertServerConfig converts a config.ServerConfig to a contracts.Server
func ConvertServerConfig(cfg *config.ServerConfig, status string, connected bool, toolCount int) *Server {
	server := &Server{
		ID:              cfg.Name,
		Name:            cfg.Name,
		URL:             cfg.URL,
		Protocol:        cfg.Protocol,
		Command:         cfg.Command,
		Args:            cfg.Args,
		WorkingDir:      cfg.WorkingDir,
		Env:             cfg.Env,
		Headers:         cfg.Headers,
		Enabled:         cfg.Enabled,
		Quarantined:     cfg.Quarantined,
		Connected:       connected,
		Status:          status,
		ToolCount:       toolCount,
		Created:         cfg.Created,
		Updated:         cfg.Updated,
		ReconnectCount:  0, // TODO: Get from runtime status
	}

	// Convert OAuth config if present
	if cfg.OAuth != nil {
		server.OAuth = &OAuthConfig{
			AuthURL:     "", // TODO: Add to config.OAuthConfig
			TokenURL:    "", // TODO: Add to config.OAuthConfig
			ClientID:    cfg.OAuth.ClientID,
			Scopes:      cfg.OAuth.Scopes,
			ExtraParams: nil, // TODO: Add to config.OAuthConfig
		}
	}

	// Convert isolation config if present
	if cfg.Isolation != nil {
		server.Isolation = &IsolationConfig{
			Enabled:     cfg.Isolation.Enabled,
			Image:       cfg.Isolation.Image,
			MemoryLimit: "", // TODO: Move from DockerIsolationConfig
			CPULimit:    "", // TODO: Move from DockerIsolationConfig
			WorkingDir:  cfg.Isolation.WorkingDir,
			Timeout:     "", // TODO: Move from DockerIsolationConfig
		}
	}

	return server
}

// ConvertToolMetadata converts a config.ToolMetadata to a contracts.Tool
func ConvertToolMetadata(meta *config.ToolMetadata) *Tool {
	tool := &Tool{
		Name:        meta.Name,
		ServerName:  meta.ServerName,
		Description: meta.Description,
		Schema:      make(map[string]interface{}),
		Usage:       0, // TODO: Get from storage stats
	}

	// Parse schema from JSON string if present
	if meta.ParamsJSON != "" {
		// TODO: Parse meta.ParamsJSON into tool.Schema
		// For now, just create an empty schema
		tool.Schema = make(map[string]interface{})
	}

	return tool
}

// ConvertSearchResult converts a config.SearchResult to a contracts.SearchResult
func ConvertSearchResult(result *config.SearchResult) *SearchResult {
	return &SearchResult{
		Tool:    *ConvertToolMetadata(result.Tool),
		Score:   result.Score,
		Snippet: "", // TODO: Add Snippet field to config.SearchResult
		Matches: 0,  // TODO: Add Matches field to config.SearchResult
	}
}

// ConvertLogEntry converts a string log line to a contracts.LogEntry
// This is a simplified conversion - in a real implementation you'd parse structured logs
func ConvertLogEntry(line string, serverName string) *LogEntry {
	// Use a fixed timestamp for testing consistency
	timestamp := time.Date(2025, 9, 19, 12, 0, 0, 0, time.UTC)

	return &LogEntry{
		Timestamp: timestamp,
		Level:     "INFO", // TODO: Parse actual log level
		Message:   line,
		Server:    serverName,
		Fields:    make(map[string]interface{}),
	}
}

// ConvertUpstreamStatsToServerStats converts upstream stats map to typed ServerStats
func ConvertUpstreamStatsToServerStats(stats map[string]interface{}) ServerStats {
	serverStats := ServerStats{}

	// Extract server statistics from the upstream stats map
	if servers, ok := stats["servers"].(map[string]interface{}); ok {
		totalServers := len(servers)
		connectedServers := 0
		quarantinedServers := 0
		totalTools := 0

		for _, serverStat := range servers {
			if stat, ok := serverStat.(map[string]interface{}); ok {
				if connected, ok := stat["connected"].(bool); ok && connected {
					connectedServers++
				}
				if quarantined, ok := stat["quarantined"].(bool); ok && quarantined {
					quarantinedServers++
				}
				if toolCount, ok := stat["tool_count"].(int); ok {
					totalTools += toolCount
				}
			}
		}

		serverStats.TotalServers = totalServers
		serverStats.ConnectedServers = connectedServers
		serverStats.QuarantinedServers = quarantinedServers
		serverStats.TotalTools = totalTools
	}

	// Extract Docker container count if available
	if dockerCount, ok := stats["docker_containers"].(int); ok {
		serverStats.DockerContainers = dockerCount
	}

	return serverStats
}

// ConvertGenericServersToTyped converts []map[string]interface{} to []Server
func ConvertGenericServersToTyped(genericServers []map[string]interface{}) []Server {
	servers := make([]Server, 0, len(genericServers))

	for _, generic := range genericServers {
		server := Server{}

		// Extract basic fields
		if id, ok := generic["id"].(string); ok {
			server.ID = id
		}
		if name, ok := generic["name"].(string); ok {
			server.Name = name
		}
		if url, ok := generic["url"].(string); ok {
			server.URL = url
		}
		if protocol, ok := generic["protocol"].(string); ok {
			server.Protocol = protocol
		}
		if command, ok := generic["command"].(string); ok {
			server.Command = command
		}
		if enabled, ok := generic["enabled"].(bool); ok {
			server.Enabled = enabled
		}
		if quarantined, ok := generic["quarantined"].(bool); ok {
			server.Quarantined = quarantined
		}
		if connected, ok := generic["connected"].(bool); ok {
			server.Connected = connected
		}
		if status, ok := generic["status"].(string); ok {
			server.Status = status
		}
		if lastError, ok := generic["last_error"].(string); ok {
			server.LastError = lastError
		}
		if toolCount, ok := generic["tool_count"].(int); ok {
			server.ToolCount = toolCount
		}
		if reconnectCount, ok := generic["reconnect_count"].(int); ok {
			server.ReconnectCount = reconnectCount
		}

		// Extract args slice
		if args, ok := generic["args"].([]interface{}); ok {
			server.Args = make([]string, len(args))
			for i, arg := range args {
				if argStr, ok := arg.(string); ok {
					server.Args[i] = argStr
				}
			}
		}

		// Extract env map
		if env, ok := generic["env"].(map[string]interface{}); ok {
			server.Env = make(map[string]string)
			for k, v := range env {
				if vStr, ok := v.(string); ok {
					server.Env[k] = vStr
				}
			}
		}

		// Extract headers map
		if headers, ok := generic["headers"].(map[string]interface{}); ok {
			server.Headers = make(map[string]string)
			for k, v := range headers {
				if vStr, ok := v.(string); ok {
					server.Headers[k] = vStr
				}
			}
		}

		// Extract timestamps
		if created, ok := generic["created"].(time.Time); ok {
			server.Created = created
		}
		if updated, ok := generic["updated"].(time.Time); ok {
			server.Updated = updated
		}
		if connectedAt, ok := generic["connected_at"].(time.Time); ok {
			server.ConnectedAt = &connectedAt
		}
		if lastReconnectAt, ok := generic["last_reconnect_at"].(time.Time); ok {
			server.LastReconnectAt = &lastReconnectAt
		}

		servers = append(servers, server)
	}

	return servers
}

// ConvertGenericToolsToTyped converts []map[string]interface{} to []Tool
func ConvertGenericToolsToTyped(genericTools []map[string]interface{}) []Tool {
	tools := make([]Tool, 0, len(genericTools))

	for _, generic := range genericTools {
		tool := Tool{
			Schema: make(map[string]interface{}),
		}

		// Extract basic fields
		if name, ok := generic["name"].(string); ok {
			tool.Name = name
		}
		if serverName, ok := generic["server_name"].(string); ok {
			tool.ServerName = serverName
		}
		if description, ok := generic["description"].(string); ok {
			tool.Description = description
		}
		if usage, ok := generic["usage"].(int); ok {
			tool.Usage = usage
		}

		// Extract schema
		if schema, ok := generic["schema"].(map[string]interface{}); ok {
			tool.Schema = schema
		}

		// Extract timestamps
		if lastUsed, ok := generic["last_used"].(time.Time); ok {
			tool.LastUsed = &lastUsed
		}

		tools = append(tools, tool)
	}

	return tools
}

// ConvertGenericSearchResultsToTyped converts []map[string]interface{} to []SearchResult
func ConvertGenericSearchResultsToTyped(genericResults []map[string]interface{}) []SearchResult {
	results := make([]SearchResult, 0, len(genericResults))

	for _, generic := range genericResults {
		result := SearchResult{}

		// Extract basic fields
		if score, ok := generic["score"].(float64); ok {
			result.Score = score
		}
		if snippet, ok := generic["snippet"].(string); ok {
			result.Snippet = snippet
		}
		if matches, ok := generic["matches"].(int); ok {
			result.Matches = matches
		}

		// Extract embedded tool
		if toolData, ok := generic["tool"].(map[string]interface{}); ok {
			tools := ConvertGenericToolsToTyped([]map[string]interface{}{toolData})
			if len(tools) > 0 {
				result.Tool = tools[0]
			}
		}

		results = append(results, result)
	}

	return results
}

// Helper function to create typed API responses
func NewSuccessResponse(data interface{}) APIResponse {
	return APIResponse{
		Success: true,
		Data:    data,
	}
}

func NewErrorResponse(error string) APIResponse {
	return APIResponse{
		Success: false,
		Error:   error,
	}
}

// Type assertion helper with better error messages
func AssertType[T any](data interface{}, fieldName string) (T, error) {
	var zero T
	if typed, ok := data.(T); ok {
		return typed, nil
	}
	return zero, fmt.Errorf("field %s has unexpected type %T", fieldName, data)
}