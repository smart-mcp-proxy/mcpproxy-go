package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"mcpproxy-go/internal/jsruntime"
	"mcpproxy-go/internal/upstream"

	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// handleCodeExecution executes JavaScript code that orchestrates multiple upstream tools
func (p *MCPProxyServer) handleCodeExecution(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p.logger.Debug("code_execution tool called")

	// Parse arguments
	var options jsruntime.ExecutionOptions

	// Extract code (required)
	code, err := request.RequireString("code")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Missing required parameter 'code': %v", err)), nil
	}

	// Get all arguments
	args := request.GetArguments()

	// Extract input (optional) - this is an object
	input, ok := args["input"].(map[string]interface{})
	if !ok || input == nil {
		input = make(map[string]interface{})
	}
	options.Input = input

	// Extract options object (optional)
	if optionsObj, ok := args["options"].(map[string]interface{}); ok && optionsObj != nil {
		// Parse timeout_ms
		if timeoutMs, ok := optionsObj["timeout_ms"].(float64); ok {
			options.TimeoutMs = int(timeoutMs)
			// Validate timeout range
			if options.TimeoutMs < 1 || options.TimeoutMs > 600000 {
				return mcp.NewToolResultError("timeout_ms must be between 1 and 600000 milliseconds"), nil
			}
		}

		// Parse max_tool_calls
		if maxToolCalls, ok := optionsObj["max_tool_calls"].(float64); ok {
			options.MaxToolCalls = int(maxToolCalls)
			// Validate max_tool_calls
			if options.MaxToolCalls < 0 {
				return mcp.NewToolResultError("max_tool_calls cannot be negative"), nil
			}
		}

		// Parse allowed_servers
		if allowedServers, ok := optionsObj["allowed_servers"].([]interface{}); ok {
			serverNames := make([]string, 0, len(allowedServers))
			for _, serverVal := range allowedServers {
				if serverName, ok := serverVal.(string); ok {
					serverNames = append(serverNames, serverName)
				} else {
					return mcp.NewToolResultError("allowed_servers must be an array of strings"), nil
				}
			}
			options.AllowedServers = serverNames
		}
	}

	// Apply defaults from config if not specified in request
	if options.TimeoutMs == 0 {
		options.TimeoutMs = p.config.CodeExecutionTimeoutMs
	}
	if options.MaxToolCalls == 0 {
		options.MaxToolCalls = p.config.CodeExecutionMaxToolCalls
	}

	// Create tool caller adapter that wraps the upstream manager
	toolCaller := &upstreamToolCaller{
		upstreamManager: p.upstreamManager,
		logger:          p.logger,
		executionID:     options.ExecutionID,
	}

	// Log pool metrics before acquisition
	if p.jsPool != nil {
		p.logger.Debug("pool metrics before acquisition",
			zap.String("execution_id", options.ExecutionID),
			zap.Int("pool_size", p.jsPool.Size()),
			zap.Int("available", p.jsPool.Available()),
			zap.Int("in_use", p.jsPool.Size()-p.jsPool.Available()),
		)
	}

	// Acquire a runtime instance from the pool (if pool is available)
	// This limits concurrent executions to the configured pool size
	acquireStart := time.Now()
	if p.jsPool != nil {
		vm, err := p.jsPool.Acquire(ctx)
		if err != nil {
			p.logger.Error("failed to acquire JavaScript runtime from pool",
				zap.String("execution_id", options.ExecutionID),
				zap.Error(err),
			)
			return mcp.NewToolResultError(fmt.Sprintf("Failed to acquire JavaScript runtime: %v", err)), nil
		}

		acquireDuration := time.Since(acquireStart)
		p.logger.Debug("acquired JavaScript runtime from pool",
			zap.String("execution_id", options.ExecutionID),
			zap.Duration("acquire_duration", acquireDuration),
			zap.Int("available_after", p.jsPool.Available()),
		)

		// Release the runtime back to the pool when done
		defer func() {
			releaseStart := time.Now()
			if releaseErr := p.jsPool.Release(vm); releaseErr != nil {
				p.logger.Warn("failed to release JavaScript runtime to pool",
					zap.String("execution_id", options.ExecutionID),
					zap.Error(releaseErr),
				)
			} else {
				p.logger.Debug("released JavaScript runtime to pool",
					zap.String("execution_id", options.ExecutionID),
					zap.Duration("release_duration", time.Since(releaseStart)),
					zap.Int("available_after", p.jsPool.Available()),
				)
			}
		}()
	}

	// Execute JavaScript
	p.logger.Info("executing JavaScript code",
		zap.String("execution_id", options.ExecutionID),
		zap.Int("code_length", len(code)),
		zap.Int("timeout_ms", options.TimeoutMs),
		zap.Int("max_tool_calls", options.MaxToolCalls),
		zap.Int("allowed_servers_count", len(options.AllowedServers)),
	)

	executionStart := time.Now()
	result := jsruntime.Execute(ctx, toolCaller, code, options)
	executionDuration := time.Since(executionStart)

	// Log execution result with metrics
	if result.Ok {
		p.logger.Info("code execution succeeded",
			zap.String("execution_id", options.ExecutionID),
			zap.Duration("execution_duration", executionDuration),
			zap.Int("tool_calls_made", len(toolCaller.getToolCalls())),
		)
	} else {
		p.logger.Warn("code execution failed",
			zap.String("execution_id", options.ExecutionID),
			zap.Duration("execution_duration", executionDuration),
			zap.String("error_code", string(result.Error.Code)),
			zap.String("error_message", result.Error.Message),
			zap.Int("tool_calls_made", len(toolCaller.getToolCalls())),
		)
	}

	// Log detailed tool call metrics
	if len(toolCaller.getToolCalls()) > 0 {
		p.logger.Debug("tool call summary",
			zap.String("execution_id", options.ExecutionID),
			zap.Int("total_calls", len(toolCaller.getToolCalls())),
			zap.Any("tool_calls", toolCaller.getToolCalls()),
		)
	}

	// Convert result to MCP response format
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize result: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(resultJSON)),
		},
	}, nil
}

// toolCallRecord tracks information about a single tool call for observability
type toolCallRecord struct {
	ServerName string        `json:"server_name"`
	ToolName   string        `json:"tool_name"`
	StartTime  time.Time     `json:"start_time"`
	Duration   time.Duration `json:"duration"`
	Success    bool          `json:"success"`
	Error      string        `json:"error,omitempty"`
}

// upstreamToolCaller adapts the upstream.Manager to implement jsruntime.ToolCaller
type upstreamToolCaller struct {
	upstreamManager *upstream.Manager
	logger          *zap.Logger
	executionID     string
	toolCalls       []toolCallRecord
	mu              sync.Mutex
}

// CallTool implements jsruntime.ToolCaller interface
func (u *upstreamToolCaller) CallTool(ctx context.Context, serverName, toolName string, args map[string]interface{}) (interface{}, error) {
	startTime := time.Now()

	u.logger.Debug("calling upstream tool from JavaScript",
		zap.String("execution_id", u.executionID),
		zap.String("server", serverName),
		zap.String("tool", toolName),
	)

	// Get the managed client for the server
	client, exists := u.upstreamManager.GetClient(serverName)
	if !exists {
		err := fmt.Errorf("server not found: %s", serverName)
		duration := time.Since(startTime)
		u.recordToolCall(serverName, toolName, startTime, duration, false, err)
		return nil, err
	}

	// Call the tool
	result, err := client.CallTool(ctx, toolName, args)
	duration := time.Since(startTime)

	// Record the tool call with timing and result
	u.recordToolCall(serverName, toolName, startTime, duration, err == nil, err)

	u.logger.Debug("upstream tool call completed",
		zap.String("execution_id", u.executionID),
		zap.String("server", serverName),
		zap.String("tool", toolName),
		zap.Duration("duration", duration),
		zap.Bool("success", err == nil),
	)

	if err != nil {
		return nil, err
	}

	return result, nil
}

// recordToolCall records a tool call with timing and result information (thread-safe)
func (u *upstreamToolCaller) recordToolCall(serverName, toolName string, startTime time.Time, duration time.Duration, success bool, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	record := toolCallRecord{
		ServerName: serverName,
		ToolName:   toolName,
		StartTime:  startTime,
		Duration:   duration,
		Success:    success,
	}

	if err != nil {
		record.Error = err.Error()
	}

	u.toolCalls = append(u.toolCalls, record)
}

// getToolCalls returns all recorded tool calls (thread-safe)
func (u *upstreamToolCaller) getToolCalls() []toolCallRecord {
	u.mu.Lock()
	defer u.mu.Unlock()

	// Return a copy to prevent external modification
	calls := make([]toolCallRecord, len(u.toolCalls))
	copy(calls, u.toolCalls)
	return calls
}
