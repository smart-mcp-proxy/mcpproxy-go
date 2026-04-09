package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Minimal 1x1 red PNG for fixtures. Base64-decoded is a valid PNG file.
const tinyRedPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Zx+m0oAAAAASUVORK5CYII="

// CreateMockImageServer starts a mock MCP server exposing a single tool that
// returns a mixed-content response: a text block followed by an ImageContent
// block. Used to verify the proxy preserves non-text content types end-to-end
// (issue #368).
func (env *TestEnvironment) CreateMockImageServer(name string) *MockUpstreamServer {
	mcpServer := mcpserver.NewMCPServer(
		name,
		"1.0.0-test",
		mcpserver.WithToolCapabilities(true),
	)

	mockServer := &MockUpstreamServer{
		server: mcpServer,
	}

	// Tool: get_image returns text + image + audio in a single CallToolResult
	tool := mcp.Tool{
		Name:        "get_image",
		Description: "Returns a tiny test image with a text caption and an audio snippet",
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]interface{}{},
		},
	}
	mockServer.tools = []mcp.Tool{tool}

	mcpServer.AddTool(tool, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent("Here is the test image:"),
				mcp.NewImageContent(tinyRedPNGBase64, "image/png"),
				mcp.NewAudioContent(tinyRedPNGBase64, "audio/wav"),
			},
		}, nil
	})

	// Start HTTP server on random port
	streamableServer := mcpserver.NewStreamableHTTPServer(mcpServer)

	ln, err := net.Listen("tcp", ":0")
	require.NoError(env.t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	mockServer.addr = fmt.Sprintf("http://localhost:%d", port)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           streamableServer,
		ReadHeaderTimeout: 5 * time.Second,
	}
	mockServer.httpServer = httpServer

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			env.logger.Error("Mock image server error", zap.Error(err))
		}
	}()

	time.Sleep(200 * time.Millisecond)

	env.mockServers[name] = mockServer
	return mockServer
}

// TestE2E_ImageContentPreservation verifies that ImageContent and AudioContent
// blocks returned by an upstream MCP server are forwarded to the downstream
// client as native content types — not serialized into JSON-in-text blocks.
//
// This is the regression test for issue #368.
func TestE2E_ImageContentPreservation(t *testing.T) {
	env := NewTestEnvironment(t)
	defer env.Cleanup()

	mockServer := env.CreateMockImageServer("imgserver")

	mcpClient := env.CreateProxyClient()
	defer mcpClient.Close()
	env.ConnectClient(mcpClient)

	ctx := context.Background()

	// Add upstream server via the management tool
	addRequest := mcp.CallToolRequest{}
	addRequest.Params.Name = "upstream_servers"
	addRequest.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "imgserver",
		"url":       mockServer.addr,
		"protocol":  "streamable-http",
		"enabled":   true,
	}
	_, err := mcpClient.CallTool(ctx, addRequest)
	require.NoError(t, err)

	// Unquarantine for testing
	serverConfig, err := env.proxyServer.runtime.StorageManager().GetUpstreamServer("imgserver")
	require.NoError(t, err)
	serverConfig.Quarantined = false
	err = env.proxyServer.runtime.StorageManager().SaveUpstreamServer(serverConfig)
	require.NoError(t, err)

	servers, err := env.proxyServer.runtime.StorageManager().ListUpstreamServers()
	require.NoError(t, err)
	cfg := env.proxyServer.runtime.Config()
	cfg.Servers = servers
	err = env.proxyServer.runtime.LoadConfiguredServers(cfg)
	require.NoError(t, err)

	time.Sleep(3 * time.Second)
	_ = env.proxyServer.runtime.DiscoverAndIndexTools(ctx)
	time.Sleep(3 * time.Second)

	// Call the image tool through the proxy using call_tool_read
	callRequest := mcp.CallToolRequest{}
	callRequest.Params.Name = "call_tool_read"
	callRequest.Params.Arguments = map[string]interface{}{
		"name": "imgserver:get_image",
		"args": map[string]interface{}{},
		"intent": map[string]interface{}{
			"operation_type": "read",
		},
	}

	callResult, err := mcpClient.CallTool(ctx, callRequest)
	require.NoError(t, err)
	require.False(t, callResult.IsError, "tool call should succeed")

	// The core assertion: the proxy MUST forward all three content blocks
	// (text, image, audio) as native typed blocks — not JSON-wrapped text.
	require.Equal(t, 3, len(callResult.Content),
		"expected 3 content blocks (text + image + audio), got %d. "+
			"If this is 1, the proxy is still wrapping everything in TextContent (issue #368).",
		len(callResult.Content))

	// Marshal each block to inspect its type field — the mcp-go client may
	// unmarshal Content as a concrete type or as a generic map depending on
	// transport.
	blockTypes := make([]string, 0, 3)
	var foundText, foundImage, foundAudio bool
	for i, c := range callResult.Content {
		raw, mErr := json.Marshal(c)
		require.NoError(t, mErr)
		var m map[string]interface{}
		require.NoError(t, json.Unmarshal(raw, &m))
		typ, _ := m["type"].(string)
		blockTypes = append(blockTypes, typ)

		switch typ {
		case "text":
			foundText = true
			assert.Equal(t, "Here is the test image:", m["text"],
				"text block [%d] content mismatch", i)
		case "image":
			foundImage = true
			assert.Equal(t, "image/png", m["mimeType"],
				"image block [%d] mimeType mismatch", i)
			dataStr, ok := m["data"].(string)
			require.True(t, ok, "image block [%d] missing data field", i)
			assert.Equal(t, tinyRedPNGBase64, dataStr,
				"image block [%d] data should be forwarded unchanged", i)
			// Sanity: the data is valid base64
			_, decErr := base64.StdEncoding.DecodeString(dataStr)
			assert.NoError(t, decErr, "image block [%d] data is not valid base64", i)
		case "audio":
			foundAudio = true
			assert.Equal(t, "audio/wav", m["mimeType"],
				"audio block [%d] mimeType mismatch", i)
		}
	}

	assert.True(t, foundText, "missing text block (got types: %v)", blockTypes)
	assert.True(t, foundImage, "missing image block — image was silently converted. "+
		"Got types: %v (issue #368)", blockTypes)
	assert.True(t, foundAudio, "missing audio block — audio was silently converted. "+
		"Got types: %v (issue #368)", blockTypes)
}
