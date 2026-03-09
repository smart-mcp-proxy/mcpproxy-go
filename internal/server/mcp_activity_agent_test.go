package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// TestGetAuthMetadata_Admin verifies admin context returns auth_type "admin" only.
func TestGetAuthMetadata_Admin(t *testing.T) {
	adminCtx := &auth.AuthContext{
		Type: auth.AuthTypeAdmin,
	}
	ctx := auth.WithAuthContext(context.Background(), adminCtx)

	meta := getAuthMetadata(ctx)
	assert.NotNil(t, meta)
	assert.Equal(t, "admin", meta["auth_type"])
	assert.Empty(t, meta["agent_name"], "admin context should not have agent_name")
	assert.Empty(t, meta["token_prefix"], "admin context should not have token_prefix")
	assert.Len(t, meta, 1, "admin context should only have auth_type")
}

// TestGetAuthMetadata_Agent verifies agent context returns auth_type, agent_name, and token_prefix.
func TestGetAuthMetadata_Agent(t *testing.T) {
	agentCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "ci-deploy-bot",
		TokenPrefix:    "mcp_abc123def",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	meta := getAuthMetadata(ctx)
	assert.NotNil(t, meta)
	assert.Equal(t, "agent", meta["auth_type"])
	assert.Equal(t, "ci-deploy-bot", meta["agent_name"])
	assert.Equal(t, "mcp_abc123def", meta["token_prefix"])
	assert.Len(t, meta, 3, "agent context should have auth_type, agent_name, token_prefix")
}

// TestGetAuthMetadata_Nil verifies nil context returns nil.
func TestGetAuthMetadata_Nil(t *testing.T) {
	ctx := context.Background() // no auth context attached

	meta := getAuthMetadata(ctx)
	assert.Nil(t, meta)
}

// TestInjectAuthMetadata_Agent verifies auth metadata is injected with _auth_ prefix.
func TestInjectAuthMetadata_Agent(t *testing.T) {
	agentCtx := &auth.AuthContext{
		Type:        auth.AuthTypeAgent,
		AgentName:   "test-bot",
		TokenPrefix: "mcp_xyz789abc",
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	args := map[string]interface{}{
		"query": "search term",
		"limit": 10,
	}

	result := injectAuthMetadata(ctx, args)
	assert.NotNil(t, result)
	// Original args preserved
	assert.Equal(t, "search term", result["query"])
	assert.Equal(t, 10, result["limit"])
	// Auth metadata injected with prefix
	assert.Equal(t, "agent", result["_auth_auth_type"])
	assert.Equal(t, "test-bot", result["_auth_agent_name"])
	assert.Equal(t, "mcp_xyz789abc", result["_auth_token_prefix"])
}

// TestInjectAuthMetadata_Admin verifies admin auth metadata is injected.
func TestInjectAuthMetadata_Admin(t *testing.T) {
	adminCtx := &auth.AuthContext{
		Type: auth.AuthTypeAdmin,
	}
	ctx := auth.WithAuthContext(context.Background(), adminCtx)

	args := map[string]interface{}{
		"name": "github:list_repos",
	}

	result := injectAuthMetadata(ctx, args)
	assert.NotNil(t, result)
	assert.Equal(t, "admin", result["_auth_auth_type"])
	_, hasAgentName := result["_auth_agent_name"]
	assert.False(t, hasAgentName, "admin context should not inject agent_name")
}

// TestInjectAuthMetadata_NilArgs verifies args map is created when nil.
func TestInjectAuthMetadata_NilArgs(t *testing.T) {
	agentCtx := &auth.AuthContext{
		Type:        auth.AuthTypeAgent,
		AgentName:   "bot",
		TokenPrefix: "mcp_123",
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	result := injectAuthMetadata(ctx, nil)
	assert.NotNil(t, result)
	assert.Equal(t, "agent", result["_auth_auth_type"])
	assert.Equal(t, "bot", result["_auth_agent_name"])
	assert.Equal(t, "mcp_123", result["_auth_token_prefix"])
}

// TestInjectAuthMetadata_DoesNotMutateOriginal verifies the original args map is never modified.
// This is the core fix for issue #322: _auth_* fields must not leak to upstream MCP servers.
func TestInjectAuthMetadata_DoesNotMutateOriginal(t *testing.T) {
	agentCtx := &auth.AuthContext{
		Type:        auth.AuthTypeAgent,
		AgentName:   "test-bot",
		TokenPrefix: "mcp_xyz789abc",
	}
	ctx := auth.WithAuthContext(context.Background(), agentCtx)

	args := map[string]interface{}{
		"query": "search term",
		"limit": 10,
	}

	result := injectAuthMetadata(ctx, args)

	// Result should have auth metadata
	assert.Equal(t, "agent", result["_auth_auth_type"])
	assert.Equal(t, "test-bot", result["_auth_agent_name"])

	// Original args must NOT be modified
	assert.Len(t, args, 2, "original args should still have exactly 2 keys")
	_, hasAuthType := args["_auth_auth_type"]
	assert.False(t, hasAuthType, "original args must not contain _auth_ fields (issue #322)")
	_, hasAgentName := args["_auth_agent_name"]
	assert.False(t, hasAgentName, "original args must not contain _auth_ fields (issue #322)")
}

// TestInjectAuthMetadata_NoAuthContext verifies no injection when no auth context.
func TestInjectAuthMetadata_NoAuthContext(t *testing.T) {
	ctx := context.Background()

	args := map[string]interface{}{
		"query": "test",
	}

	result := injectAuthMetadata(ctx, args)
	assert.Equal(t, args, result, "should return original args unchanged")
	_, hasAuthType := result["_auth_auth_type"]
	assert.False(t, hasAuthType, "should not inject auth metadata without auth context")
}
