package server

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/branding"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveInstructions_Default(t *testing.T) {
	result := resolveInstructions("")
	assert.Equal(t, defaultInstructions, result)
}

func TestResolveInstructions_Custom(t *testing.T) {
	custom := "Use retrieve_tools to find tools. Custom instructions."
	result := resolveInstructions(custom)
	assert.Equal(t, custom, result)
}

// TestServer_DefaultInstructions verifies the controller exposes the built-in
// default (not a user's custom value) so /api/v1/status can surface it to the
// Web UI placeholder without drift (MCP-2176).
func TestServer_DefaultInstructions(t *testing.T) {
	s := &Server{}
	assert.Equal(t, defaultInstructions, s.DefaultInstructions())
	assert.Contains(t, s.DefaultInstructions(), "retrieve_tools")
}

func TestDefaultInstructions_ContainsKeyTerms(t *testing.T) {
	assert.Contains(t, defaultInstructions, "retrieve_tools")
	assert.Contains(t, defaultInstructions, "search_servers")
	assert.Contains(t, defaultInstructions, "call_tool_read")
	assert.Contains(t, defaultInstructions, "upstream_servers")
	// Routing-mode-aware guidance: the default must also cover the
	// code_execution and direct (server__tool) modes (Spec 031).
	assert.Contains(t, defaultInstructions, "code_execution")
	assert.Contains(t, defaultInstructions, "server__tool")
}

// TestServer_DefaultInstructions_CarriesProjectLinks verifies discussion #948:
// the protocol-level serverInfo instructions carry the homepage/repo/docs so an
// agent (and anyone reading its logs) can find the project.
func TestServer_DefaultInstructions_CarriesProjectLinks(t *testing.T) {
	assert.Contains(t, defaultInstructions, branding.Homepage)
	assert.Contains(t, defaultInstructions, branding.Repo)
	assert.Contains(t, defaultInstructions, branding.Docs)
}
