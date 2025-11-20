package main

import (
	"fmt"
	"os"
	"path/filepath"

	"mcpproxy-go/internal/storage"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()
	sugar := logger.Sugar()

	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, ".mcpproxy")

	mgr, err := storage.NewManager(dataDir, sugar)
	if err != nil {
		fmt.Printf("Error creating manager: %v\n", err)
		return
	}
	defer mgr.Close()

	calls, err := mgr.GetToolCallHistory(100, 0)
	if err != nil {
		fmt.Printf("Error getting history: %v\n", err)
		return
	}

	fmt.Printf("Total tool calls in database: %d\n\n", len(calls))
	for i, call := range calls {
		fmt.Printf("%d. ID: %s\n", i+1, call.ID)
		fmt.Printf("   Server: %s, Tool: %s\n", call.ServerName, call.ToolName)
		fmt.Printf("   Execution Type: %s\n", call.ExecutionType)
		fmt.Printf("   Session ID: %s\n", call.MCPSessionID)
		fmt.Printf("   Client: %s %s\n", call.MCPClientName, call.MCPClientVersion)
		fmt.Printf("   Parent: %s\n", call.ParentCallID)
		fmt.Printf("   Timestamp: %v\n\n", call.Timestamp)
	}
}
