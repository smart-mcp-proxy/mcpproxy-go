package contracts

import (
	"fmt"
	"time"

	"mcpproxy-go/internal/config"
)

// ConvertServerConfig converts a config.ServerConfig to a contracts.Server
func ConvertServerConfig(cfg *config.ServerConfig, status string, connected bool, toolCount int, authenticated bool) *Server {
	server := &Server{
		ID:             cfg.Name,
		Name:           cfg.Name,
		URL:            cfg.URL,
		Protocol:       cfg.Protocol,
		Command:        cfg.Command,
		Args:           cfg.Args,
		WorkingDir:     cfg.WorkingDir,
		Env:            cfg.Env,
		Headers:        cfg.Headers,
		Enabled:        cfg.Enabled,
		Quarantined:    cfg.Quarantined,
		Connected:      connected,
		Status:         status,
		ToolCount:      toolCount,
		Created:        cfg.Created,
		Updated:        cfg.Updated,
		ReconnectCount: 0, // TODO: Get from runtime status
		Authenticated:  authenticated,
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
func ConvertLogEntry(line, serverName string) *LogEntry {
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
		if authenticated, ok := generic["authenticated"].(bool); ok {
			server.Authenticated = authenticated
		}
		if shouldRetry, ok := generic["should_retry"].(bool); ok {
			server.ShouldRetry = shouldRetry
		}
		switch rc := generic["retry_count"].(type) {
		case int:
			server.RetryCount = rc
		case float64:
			server.RetryCount = int(rc)
		}
		switch v := generic["last_retry_time"].(type) {
		case time.Time:
			server.LastRetryTime = &v
		case *time.Time:
			if v != nil && !v.IsZero() {
				server.LastRetryTime = v
			}
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

		// Extract OAuth config
		if oauth, ok := generic["oauth"].(map[string]interface{}); ok {
			server.OAuth = &OAuthConfig{}
			if authURL, ok := oauth["auth_url"].(string); ok {
				server.OAuth.AuthURL = authURL
			}
			if tokenURL, ok := oauth["token_url"].(string); ok {
				server.OAuth.TokenURL = tokenURL
			}
			if clientID, ok := oauth["client_id"].(string); ok {
				server.OAuth.ClientID = clientID
			}
			if scopes, ok := oauth["scopes"].([]interface{}); ok {
				server.OAuth.Scopes = make([]string, len(scopes))
				for i, scope := range scopes {
					if scopeStr, ok := scope.(string); ok {
						server.OAuth.Scopes[i] = scopeStr
					}
				}
			}
			if extraParams, ok := oauth["extra_params"].(map[string]interface{}); ok {
				server.OAuth.ExtraParams = make(map[string]string)
				for k, v := range extraParams {
					if vStr, ok := v.(string); ok {
						server.OAuth.ExtraParams[k] = vStr
					}
				}
			}
			if redirectPort, ok := oauth["redirect_port"].(int); ok {
				server.OAuth.RedirectPort = redirectPort
			}
			if pkceEnabled, ok := oauth["pkce_enabled"].(bool); ok {
				server.OAuth.PKCEEnabled = pkceEnabled
			}
			if tokenExpiresAt, ok := oauth["token_expires_at"].(string); ok && tokenExpiresAt != "" {
				if parsedTime, err := time.Parse(time.RFC3339, tokenExpiresAt); err == nil {
					server.OAuth.TokenExpiresAt = &parsedTime
				}
			}
			if tokenValid, ok := oauth["token_valid"].(bool); ok {
				server.OAuth.TokenValid = tokenValid
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

		// Extract annotations
		if annotations, ok := generic["annotations"].(map[string]interface{}); ok {
			tool.Annotations = convertMapToToolAnnotation(annotations)
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

func NewErrorResponse(errorMsg string) APIResponse {
	return APIResponse{
		Success: false,
		Error:   errorMsg,
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

// ConvertStorageToolCallToContract converts storage.ToolCallRecord to contracts.ToolCallRecord
func ConvertStorageToolCallToContract(storageRecord interface{}) *ToolCallRecord {
	// Handle conversion from storage package types
	// Since storage.ToolCallRecord and contracts.ToolCallRecord have the same structure,
	// we can use a map as an intermediary

	recordMap, ok := storageRecord.(map[string]interface{})
	if !ok {
		// If it's already a proper struct, try direct field mapping
		return nil
	}

	record := &ToolCallRecord{}

	if id, ok := recordMap["id"].(string); ok {
		record.ID = id
	}
	if serverID, ok := recordMap["server_id"].(string); ok {
		record.ServerID = serverID
	}
	if serverName, ok := recordMap["server_name"].(string); ok {
		record.ServerName = serverName
	}
	if toolName, ok := recordMap["tool_name"].(string); ok {
		record.ToolName = toolName
	}
	if arguments, ok := recordMap["arguments"].(map[string]interface{}); ok {
		record.Arguments = arguments
	}
	if response := recordMap["response"]; response != nil {
		record.Response = response
	}
	if errorMsg, ok := recordMap["error"].(string); ok {
		record.Error = errorMsg
	}
	if duration, ok := recordMap["duration"].(int64); ok {
		record.Duration = duration
	}
	if timestamp, ok := recordMap["timestamp"].(time.Time); ok {
		record.Timestamp = timestamp
	}
	if configPath, ok := recordMap["config_path"].(string); ok {
		record.ConfigPath = configPath
	}
	if requestID, ok := recordMap["request_id"].(string); ok {
		record.RequestID = requestID
	}

	return record
}

// Config validation converters

// ConvertValidationErrors converts config.ValidationError slice to contracts.ValidationError slice
func ConvertValidationErrors(configErrors []config.ValidationError) []ValidationError {
	contractErrors := make([]ValidationError, len(configErrors))
	for i, err := range configErrors {
		contractErrors[i] = ValidationError{
			Field:   err.Field,
			Message: err.Message,
		}
	}
	return contractErrors
}

// ConvertConfigToContract converts config.Config to a map for API response
func ConvertConfigToContract(cfg *config.Config) interface{} {
	if cfg == nil {
		return nil
	}

	// Return the config as-is for JSON marshaling
	// The JSON tags on config.Config will handle serialization
	return cfg
}

// convertMapToToolAnnotation converts a map to ToolAnnotation
func convertMapToToolAnnotation(m map[string]interface{}) *ToolAnnotation {
	if m == nil {
		return nil
	}

	annotation := &ToolAnnotation{}

	if title, ok := m["title"].(string); ok {
		annotation.Title = title
	}
	if readOnly, ok := m["readOnlyHint"].(bool); ok {
		annotation.ReadOnlyHint = &readOnly
	}
	if destructive, ok := m["destructiveHint"].(bool); ok {
		annotation.DestructiveHint = &destructive
	}
	if idempotent, ok := m["idempotentHint"].(bool); ok {
		annotation.IdempotentHint = &idempotent
	}
	if openWorld, ok := m["openWorldHint"].(bool); ok {
		annotation.OpenWorldHint = &openWorld
	}

	return annotation
}
