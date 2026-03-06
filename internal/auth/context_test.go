package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithAuthContext_RoundTrip(t *testing.T) {
	ac := &AuthContext{
		Type:           AuthTypeAgent,
		AgentName:      "deploy-bot",
		TokenPrefix:    "mcp_agt_abcd",
		AllowedServers: []string{"github", "gitlab"},
		Permissions:    []string{PermRead, PermWrite},
	}

	ctx := WithAuthContext(context.Background(), ac)
	retrieved := AuthContextFromContext(ctx)

	require.NotNil(t, retrieved)
	assert.Equal(t, AuthTypeAgent, retrieved.Type)
	assert.Equal(t, "deploy-bot", retrieved.AgentName)
	assert.Equal(t, "mcp_agt_abcd", retrieved.TokenPrefix)
	assert.Equal(t, []string{"github", "gitlab"}, retrieved.AllowedServers)
	assert.Equal(t, []string{PermRead, PermWrite}, retrieved.Permissions)
}

func TestAuthContextFromContext_Nil(t *testing.T) {
	ctx := context.Background()
	ac := AuthContextFromContext(ctx)
	assert.Nil(t, ac, "should return nil from empty context")
}

func TestIsAdmin(t *testing.T) {
	t.Run("admin context", func(t *testing.T) {
		ac := AdminContext()
		assert.True(t, ac.IsAdmin())
	})

	t.Run("agent context", func(t *testing.T) {
		ac := &AuthContext{Type: AuthTypeAgent, AgentName: "bot"}
		assert.False(t, ac.IsAdmin())
	})
}

func TestCanAccessServer(t *testing.T) {
	t.Run("admin can access any server", func(t *testing.T) {
		ac := AdminContext()
		assert.True(t, ac.CanAccessServer("github"))
		assert.True(t, ac.CanAccessServer("anything"))
	})

	t.Run("agent with explicit list", func(t *testing.T) {
		ac := &AuthContext{
			Type:           AuthTypeAgent,
			AllowedServers: []string{"github", "gitlab"},
		}
		assert.True(t, ac.CanAccessServer("github"))
		assert.True(t, ac.CanAccessServer("gitlab"))
		assert.False(t, ac.CanAccessServer("bitbucket"))
	})

	t.Run("agent with wildcard", func(t *testing.T) {
		ac := &AuthContext{
			Type:           AuthTypeAgent,
			AllowedServers: []string{"*"},
		}
		assert.True(t, ac.CanAccessServer("github"))
		assert.True(t, ac.CanAccessServer("any-server"))
	})

	t.Run("agent with empty name", func(t *testing.T) {
		ac := &AuthContext{
			Type:           AuthTypeAgent,
			AllowedServers: []string{"github"},
		}
		assert.False(t, ac.CanAccessServer(""))
	})

	t.Run("agent with no allowed servers", func(t *testing.T) {
		ac := &AuthContext{
			Type:           AuthTypeAgent,
			AllowedServers: nil,
		}
		assert.False(t, ac.CanAccessServer("github"))
	})
}

func TestHasPermission(t *testing.T) {
	t.Run("admin has all permissions", func(t *testing.T) {
		ac := AdminContext()
		assert.True(t, ac.HasPermission(PermRead))
		assert.True(t, ac.HasPermission(PermWrite))
		assert.True(t, ac.HasPermission(PermDestructive))
	})

	t.Run("read-only agent", func(t *testing.T) {
		ac := &AuthContext{
			Type:        AuthTypeAgent,
			Permissions: []string{PermRead},
		}
		assert.True(t, ac.HasPermission(PermRead))
		assert.False(t, ac.HasPermission(PermWrite))
		assert.False(t, ac.HasPermission(PermDestructive))
	})

	t.Run("read+write agent", func(t *testing.T) {
		ac := &AuthContext{
			Type:        AuthTypeAgent,
			Permissions: []string{PermRead, PermWrite},
		}
		assert.True(t, ac.HasPermission(PermRead))
		assert.True(t, ac.HasPermission(PermWrite))
		assert.False(t, ac.HasPermission(PermDestructive))
	})

	t.Run("all permissions agent", func(t *testing.T) {
		ac := &AuthContext{
			Type:        AuthTypeAgent,
			Permissions: []string{PermRead, PermWrite, PermDestructive},
		}
		assert.True(t, ac.HasPermission(PermRead))
		assert.True(t, ac.HasPermission(PermWrite))
		assert.True(t, ac.HasPermission(PermDestructive))
	})

	t.Run("agent with no permissions", func(t *testing.T) {
		ac := &AuthContext{
			Type:        AuthTypeAgent,
			Permissions: nil,
		}
		assert.False(t, ac.HasPermission(PermRead))
	})
}

func TestAdminContext(t *testing.T) {
	ac := AdminContext()
	assert.Equal(t, AuthTypeAdmin, ac.Type)
	assert.Empty(t, ac.AgentName)
	assert.Empty(t, ac.TokenPrefix)
	assert.Nil(t, ac.AllowedServers)
	assert.Nil(t, ac.Permissions)
}
