package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"go.uber.org/zap"
)

func main() {
	// Setup logger
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()
	sugar := logger.Sugar()

	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		logger.Fatal("Failed to get home dir", zap.Error(err))
	}

	// Create storage manager
	dataDir := filepath.Join(homeDir, ".mcpproxy")
	mgr, err := storage.NewManager(dataDir, sugar)
	if err != nil {
		logger.Fatal("Failed to create storage manager", zap.Error(err))
	}
	defer mgr.Close()

	// Register server identities first (required for GetToolCalls to work)
	configPath := filepath.Join(dataDir, "mcp_config.json")

	// Register mcpproxy server (for code_execution)
	mcpproxyConfig := &config.ServerConfig{
		Name:    "mcpproxy",
		Enabled: true,
	}
	_, err = mgr.RegisterServerIdentity(mcpproxyConfig, configPath)
	if err != nil {
		logger.Info("mcpproxy identity already exists or error", zap.Error(err))
	}

	// Register everything-server
	everythingConfig := &config.ServerConfig{
		Name:    "everything-server",
		Command: "npx",
		Args:    []string{"@modelcontextprotocol/server-everything"},
		Enabled: true,
	}
	_, err = mgr.RegisterServerIdentity(everythingConfig, configPath)
	if err != nil {
		logger.Info("everything-server identity already exists or error", zap.Error(err))
	}

	logger.Info("✅ Server identities registered")

	// Get the generated server IDs
	mcpproxyServerID := storage.GenerateServerID(mcpproxyConfig)
	everythingServerID := storage.GenerateServerID(everythingConfig)

	logger.Info("Server IDs generated",
		zap.String("mcpproxy_id", mcpproxyServerID),
		zap.String("everything_id", everythingServerID))

	// Create parent code_execution call
	parentCallID := fmt.Sprintf("%d-code_execution", time.Now().UnixNano())
	parentCall := &storage.ToolCallRecord{
		ID:               parentCallID,
		ServerID:         mcpproxyServerID,
		ServerName:       "mcpproxy",
		ToolName:         "code_execution",
		Arguments: map[string]interface{}{
			"code": "(function() {\n  var echo1 = call_tool('everything-server', 'echo', {message: 'Testing session tracking!'});\n  var echo2 = call_tool('everything-server', 'echo', {message: 'Nested call #2'});\n  var add = call_tool('everything-server', 'add', {a: 100, b: 200});\n  return {echo1: echo1, echo2: echo2, add: add};\n})()",
			"input": map[string]interface{}{},
		},
		Response: map[string]interface{}{
			"success": true,
			"results": map[string]interface{}{
				"echo1": map[string]interface{}{"ok": true, "result": "Testing session tracking!"},
				"echo2": map[string]interface{}{"ok": true, "result": "Nested call #2"},
				"add":   map[string]interface{}{"ok": true, "result": 300},
			},
		},
		Duration:         2500,
		Timestamp:        time.Now().Add(-5 * time.Minute),
		ConfigPath:       filepath.Join(dataDir, "mcp_config.json"),
		RequestID:        "exec-001",
		ExecutionType:    "code_execution",
		MCPSessionID:     "session-abc123def456",
		MCPClientName:    "Claude Desktop",
		MCPClientVersion: "1.2.3",
	}

	if err := mgr.RecordToolCall(parentCall); err != nil {
		logger.Fatal("Failed to record parent call", zap.Error(err))
	}
	logger.Info("Recorded parent call", zap.String("id", parentCallID))

	// Create nested echo call #1
	nestedCall1 := &storage.ToolCallRecord{
		ID:               fmt.Sprintf("%d-echo", time.Now().UnixNano()),
		ServerID:         everythingServerID,
		ServerName:       "everything-server",
		ToolName:         "echo",
		Arguments: map[string]interface{}{
			"message": "Testing session tracking!",
		},
		Response:         "Testing session tracking!",
		Duration:         150,
		Timestamp:        time.Now().Add(-4*time.Minute - 58*time.Second),
		ConfigPath:       filepath.Join(dataDir, "mcp_config.json"),
		RequestID:        "exec-001",
		ParentCallID:     parentCallID,
		ExecutionType:    "code_execution",
		MCPSessionID:     "session-abc123def456",
		MCPClientName:    "Claude Desktop",
		MCPClientVersion: "1.2.3",
	}

	if err := mgr.RecordToolCall(nestedCall1); err != nil {
		logger.Fatal("Failed to record nested call 1", zap.Error(err))
	}
	logger.Info("Recorded nested call 1")

	// Create nested echo call #2
	time.Sleep(10 * time.Millisecond)
	nestedCall2 := &storage.ToolCallRecord{
		ID:               fmt.Sprintf("%d-echo", time.Now().UnixNano()),
		ServerID:         everythingServerID,
		ServerName:       "everything-server",
		ToolName:         "echo",
		Arguments: map[string]interface{}{
			"message": "Nested call #2",
		},
		Response:         "Nested call #2",
		Duration:         120,
		Timestamp:        time.Now().Add(-4*time.Minute - 57*time.Second),
		ConfigPath:       filepath.Join(dataDir, "mcp_config.json"),
		RequestID:        "exec-001",
		ParentCallID:     parentCallID,
		ExecutionType:    "code_execution",
		MCPSessionID:     "session-abc123def456",
		MCPClientName:    "Claude Desktop",
		MCPClientVersion: "1.2.3",
	}

	if err := mgr.RecordToolCall(nestedCall2); err != nil {
		logger.Fatal("Failed to record nested call 2", zap.Error(err))
	}
	logger.Info("Recorded nested call 2")

	// Create nested add call
	time.Sleep(10 * time.Millisecond)
	nestedCall3 := &storage.ToolCallRecord{
		ID:               fmt.Sprintf("%d-add", time.Now().UnixNano()),
		ServerID:         everythingServerID,
		ServerName:       "everything-server",
		ToolName:         "add",
		Arguments: map[string]interface{}{
			"a": 100,
			"b": 200,
		},
		Response:         300,
		Duration:         80,
		Timestamp:        time.Now().Add(-4*time.Minute - 56*time.Second),
		ConfigPath:       filepath.Join(dataDir, "mcp_config.json"),
		RequestID:        "exec-001",
		ParentCallID:     parentCallID,
		ExecutionType:    "code_execution",
		MCPSessionID:     "session-abc123def456",
		MCPClientName:    "Claude Desktop",
		MCPClientVersion: "1.2.3",
	}

	if err := mgr.RecordToolCall(nestedCall3); err != nil {
		logger.Fatal("Failed to record nested call 3", zap.Error(err))
	}
	logger.Info("Recorded nested call 3")

	// Create a direct tool call (not from code_execution)
	time.Sleep(10 * time.Millisecond)
	directCall := &storage.ToolCallRecord{
		ID:               fmt.Sprintf("%d-longRunningOperation", time.Now().UnixNano()),
		ServerID:         everythingServerID,
		ServerName:       "everything-server",
		ToolName:         "longRunningOperation",
		Arguments: map[string]interface{}{
			"duration": 2000,
			"steps":    5,
		},
		Response: map[string]interface{}{
			"completed": true,
			"steps":     5,
		},
		Duration:         2100,
		Timestamp:        time.Now().Add(-3 * time.Minute),
		ConfigPath:       filepath.Join(dataDir, "mcp_config.json"),
		RequestID:        "direct-001",
		ExecutionType:    "direct",
		MCPSessionID:     "session-xyz789abc012",
		MCPClientName:    "VS Code MCP",
		MCPClientVersion: "0.5.0",
	}

	if err := mgr.RecordToolCall(directCall); err != nil {
		logger.Fatal("Failed to record direct call", zap.Error(err))
	}
	logger.Info("Recorded direct call")

	// Create another code_execution parent without nested calls
	time.Sleep(10 * time.Millisecond)
	parent2CallID := fmt.Sprintf("%d-code_execution", time.Now().UnixNano())
	parent2Call := &storage.ToolCallRecord{
		ID:         parent2CallID,
		ServerID:   mcpproxyServerID,
		ServerName: "mcpproxy",
		ToolName:   "code_execution",
		Arguments: map[string]interface{}{
			"code":  "(function() { return {result: 'Simple execution, no nested calls'}; })()",
			"input": map[string]interface{}{},
		},
		Response: map[string]interface{}{
			"result": "Simple execution, no nested calls",
		},
		Duration:         50,
		Timestamp:        time.Now().Add(-1 * time.Minute),
		ConfigPath:       filepath.Join(dataDir, "mcp_config.json"),
		RequestID:        "exec-002",
		ExecutionType:    "code_execution",
		MCPSessionID:     "session-abc123def456",
		MCPClientName:    "Claude Desktop",
		MCPClientVersion: "1.2.3",
	}

	if err := mgr.RecordToolCall(parent2Call); err != nil {
		logger.Fatal("Failed to record parent call 2", zap.Error(err))
	}
	logger.Info("Recorded parent call 2", zap.String("id", parent2CallID))

	// Create an error call (failed code_execution)
	time.Sleep(10 * time.Millisecond)
	errorCallID := fmt.Sprintf("%d-code_execution", time.Now().UnixNano())
	errorCall := &storage.ToolCallRecord{
		ID:         errorCallID,
		ServerID:   mcpproxyServerID,
		ServerName: "mcpproxy",
		ToolName:   "code_execution",
		Arguments: map[string]interface{}{
			"code":  "invalid syntax { } ]",
			"input": map[string]interface{}{},
		},
		Error:            "SYNTAX_ERROR: Unexpected token '}' at line 1, column 17",
		Duration:         15,
		Timestamp:        time.Now().Add(-30 * time.Second),
		ConfigPath:       filepath.Join(dataDir, "mcp_config.json"),
		RequestID:        "exec-error-001",
		ExecutionType:    "code_execution",
		MCPSessionID:     "session-error123abc",
		MCPClientName:    "Claude Desktop",
		MCPClientVersion: "1.2.3",
	}

	if err := mgr.RecordToolCall(errorCall); err != nil {
		logger.Fatal("Failed to record error call", zap.Error(err))
	}
	logger.Info("Recorded error call", zap.String("id", errorCallID))

	logger.Info("✅ Successfully populated test data", zap.Int("total_records", 7))
	fmt.Println("\n✅ Test data populated successfully!")
	fmt.Printf("   - 2 successful parent code_execution calls\n")
	fmt.Printf("   - 1 failed code_execution call (with error)\n")
	fmt.Printf("   - 3 nested calls (linked to first parent)\n")
	fmt.Printf("   - 1 direct tool call\n")
	fmt.Printf("   - Total: 7 tool call records\n")
}
