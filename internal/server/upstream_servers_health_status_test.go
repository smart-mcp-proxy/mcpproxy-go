package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpstreamServersList_HealthCarriesStatusVocabulary is T045: the MCP
// `upstream_servers list` output's per-server `health` object carries the
// new `status`/`usable`/`actions` fields (Spec 109 FR-010–012) alongside
// every existing field, unchanged.
func TestUpstreamServersList_HealthCarriesStatusVocabulary(t *testing.T) {
	proxy := createTestMCPProxyServer(t)

	addReq := mcp.CallToolRequest{}
	addReq.Params.Name = "upstream_servers"
	addReq.Params.Arguments = map[string]interface{}{
		"operation": "add",
		"name":      "status-vocab-server",
		"command":   "npx",
		"args":      []interface{}{"-y", "@modelcontextprotocol/server-everything"},
		"enabled":   false,
	}
	addResult, err := proxy.handleUpstreamServers(context.Background(), addReq)
	require.NoError(t, err)
	require.NotNil(t, addResult)
	require.False(t, addResult.IsError, "add should succeed")

	listReq := mcp.CallToolRequest{}
	listReq.Params.Name = "upstream_servers"
	listReq.Params.Arguments = map[string]interface{}{"operation": "list"}

	listResult, err := proxy.handleUpstreamServers(context.Background(), listReq)
	require.NoError(t, err)
	require.NotNil(t, listResult)
	require.False(t, listResult.IsError, "list should succeed")

	payload := toolResultJSON(t, listResult)
	serversRaw, ok := payload["servers"].([]interface{})
	require.True(t, ok, "expected a servers array, got %T", payload["servers"])
	require.NotEmpty(t, serversRaw)

	var found map[string]interface{}
	for _, s := range serversRaw {
		srv, ok := s.(map[string]interface{})
		require.True(t, ok)
		if srv["name"] == "status-vocab-server" {
			found = srv
			break
		}
	}
	require.NotNil(t, found, "added server must be present in the list")

	health, ok := found["health"].(map[string]interface{})
	require.True(t, ok, "expected a health object, got %T", found["health"])

	// New fields (additive; FR-010/FR-012).
	status, ok := health["status"].(string)
	require.True(t, ok, "health.status must be a string")
	assert.NotEmpty(t, status)

	_, ok = health["usable"].(bool)
	require.True(t, ok, "health.usable must be a bool")

	actionsRaw, ok := health["actions"].([]interface{})
	require.True(t, ok, "health.actions must be an array, got %T", health["actions"])

	// Invariant: action == actions[0], or "" when actions is empty.
	action, _ := health["action"].(string)
	if len(actionsRaw) == 0 {
		assert.Empty(t, action)
	} else {
		assert.Equal(t, actionsRaw[0], action)
	}

	// A newly added, disabled server: status must be "disabled" and it must
	// never be labelled connected/healthy (SC-003 / FR-011 spirit — the raw
	// vocabulary value itself, not display text, but still must not leak
	// "healthy").
	assert.Equal(t, "disabled", status)
	assert.False(t, health["usable"].(bool))

	// Existing fields untouched.
	for _, legacyField := range []string{"level", "admin_state", "summary", "action"} {
		_, present := health[legacyField]
		assert.True(t, present, "legacy field %q must still be present", legacyField)
	}
}
