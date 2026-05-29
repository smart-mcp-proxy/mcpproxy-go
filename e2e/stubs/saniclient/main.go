// Command saniclient is a tiny MCP client used by the Spec 054 Track B
// verification: it connects to a running mcpproxy /mcp endpoint, calls
// call_tool_read for a given "server:tool", and prints the returned text so
// the harness can assert spotlighting / redaction / block behaviour.
//
// Usage: saniclient <baseURL> <server:tool>
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: saniclient <baseURL> <server:tool>")
		os.Exit(2)
	}
	baseURL := os.Args[1]
	toolName := os.Args[2]

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := client.NewStreamableHttpClient(baseURL)
	if err != nil {
		fail("new client", err)
	}
	if err := c.Start(ctx); err != nil {
		fail("start", err)
	}
	defer c.Close()

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "saniclient", Version: "1.0.0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		fail("initialize", err)
	}

	callReq := mcp.CallToolRequest{}
	if toolName == "read_cache" {
		// saniclient <baseURL> read_cache <key>
		if len(os.Args) < 4 {
			fail("args", fmt.Errorf("read_cache needs a key"))
		}
		callReq.Params.Name = "read_cache"
		callReq.Params.Arguments = map[string]any{"key": os.Args[3], "offset": 0, "limit": 1000}
	} else {
		callReq.Params.Name = "call_tool_read"
		callReq.Params.Arguments = map[string]any{
			"name": toolName,
			"args": map[string]any{},
		}
	}
	res, err := c.CallTool(ctx, callReq)
	if err != nil {
		fail("call", err)
	}

	if res.IsError {
		fmt.Print("ISERROR\t")
	}
	for _, content := range res.Content {
		if tc, ok := content.(mcp.TextContent); ok {
			fmt.Println(tc.Text)
		}
	}
}

func fail(stage string, err error) {
	fmt.Fprintf(os.Stderr, "FAIL[%s]: %v\n", stage, err)
	os.Exit(1)
}
