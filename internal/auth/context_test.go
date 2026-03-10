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
	// Verify new fields default to zero values (backward compatibility).
	assert.Empty(t, ac.UserID)
	assert.Empty(t, ac.Email)
	assert.Empty(t, ac.DisplayName)
	assert.Empty(t, ac.Role)
	assert.Empty(t, ac.Provider)
}

func TestUserContext(t *testing.T) {
	ac := UserContext("01HQ3K4N6P8R2S4V6X8Z0B2D4F", "alice@example.com", "Alice Smith", "google")

	assert.Equal(t, AuthTypeUser, ac.Type)
	assert.Equal(t, "01HQ3K4N6P8R2S4V6X8Z0B2D4F", ac.UserID)
	assert.Equal(t, "alice@example.com", ac.Email)
	assert.Equal(t, "Alice Smith", ac.DisplayName)
	assert.Equal(t, "user", ac.Role)
	assert.Equal(t, "google", ac.Provider)
	// Original fields should be zero.
	assert.Empty(t, ac.AgentName)
	assert.Empty(t, ac.TokenPrefix)
	assert.Nil(t, ac.AllowedServers)
	assert.Nil(t, ac.Permissions)
}

func TestAdminUserContext(t *testing.T) {
	ac := AdminUserContext("01HQ3K4N6P8R2S4V6X8Z0B2D4F", "admin@example.com", "Admin User", "github")

	assert.Equal(t, AuthTypeAdminUser, ac.Type)
	assert.Equal(t, "01HQ3K4N6P8R2S4V6X8Z0B2D4F", ac.UserID)
	assert.Equal(t, "admin@example.com", ac.Email)
	assert.Equal(t, "Admin User", ac.DisplayName)
	assert.Equal(t, "admin", ac.Role)
	assert.Equal(t, "github", ac.Provider)
	// Original fields should be zero.
	assert.Empty(t, ac.AgentName)
	assert.Empty(t, ac.TokenPrefix)
	assert.Nil(t, ac.AllowedServers)
	assert.Nil(t, ac.Permissions)
}

func TestIsAdmin_AllTypes(t *testing.T) {
	tests := []struct {
		name     string
		authType string
		want     bool
	}{
		{"admin (API key)", AuthTypeAdmin, true},
		{"admin_user (OAuth)", AuthTypeAdminUser, true},
		{"user (OAuth)", AuthTypeUser, false},
		{"agent (token)", AuthTypeAgent, false},
		{"empty type", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac := &AuthContext{Type: tt.authType}
			assert.Equal(t, tt.want, ac.IsAdmin())
		})
	}
}

func TestIsUser(t *testing.T) {
	tests := []struct {
		name     string
		authType string
		want     bool
	}{
		{"user (OAuth)", AuthTypeUser, true},
		{"admin_user (OAuth)", AuthTypeAdminUser, true},
		{"admin (API key)", AuthTypeAdmin, false},
		{"agent (token)", AuthTypeAgent, false},
		{"empty type", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac := &AuthContext{Type: tt.authType}
			assert.Equal(t, tt.want, ac.IsUser())
		})
	}
}

func TestIsAuthenticated(t *testing.T) {
	tests := []struct {
		name     string
		authType string
		want     bool
	}{
		{"admin", AuthTypeAdmin, true},
		{"agent", AuthTypeAgent, true},
		{"user", AuthTypeUser, true},
		{"admin_user", AuthTypeAdminUser, true},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac := &AuthContext{Type: tt.authType}
			assert.Equal(t, tt.want, ac.IsAuthenticated())
		})
	}
}

func TestGetUserID(t *testing.T) {
	t.Run("user context has ID", func(t *testing.T) {
		ac := UserContext("01HQ3K4N6P8R2S4V6X8Z0B2D4F", "a@b.com", "A", "google")
		assert.Equal(t, "01HQ3K4N6P8R2S4V6X8Z0B2D4F", ac.GetUserID())
	})

	t.Run("admin context has no ID", func(t *testing.T) {
		ac := AdminContext()
		assert.Empty(t, ac.GetUserID())
	})

	t.Run("agent context has no ID", func(t *testing.T) {
		ac := &AuthContext{Type: AuthTypeAgent, AgentName: "bot"}
		assert.Empty(t, ac.GetUserID())
	})
}

func TestWithAuthContext_UserContext_RoundTrip(t *testing.T) {
	ac := UserContext("01HQ3K4N6P8R2S4V6X8Z0B2D4F", "alice@example.com", "Alice", "google")

	ctx := WithAuthContext(context.Background(), ac)
	retrieved := AuthContextFromContext(ctx)

	require.NotNil(t, retrieved)
	assert.Equal(t, AuthTypeUser, retrieved.Type)
	assert.Equal(t, "01HQ3K4N6P8R2S4V6X8Z0B2D4F", retrieved.UserID)
	assert.Equal(t, "alice@example.com", retrieved.Email)
	assert.Equal(t, "Alice", retrieved.DisplayName)
	assert.Equal(t, "user", retrieved.Role)
	assert.Equal(t, "google", retrieved.Provider)
}

func TestWithAuthContext_AdminUserContext_RoundTrip(t *testing.T) {
	ac := AdminUserContext("01HQ3K4N6P8R2S4V6X8Z0B2D4F", "admin@co.com", "Admin", "github")

	ctx := WithAuthContext(context.Background(), ac)
	retrieved := AuthContextFromContext(ctx)

	require.NotNil(t, retrieved)
	assert.Equal(t, AuthTypeAdminUser, retrieved.Type)
	assert.Equal(t, "01HQ3K4N6P8R2S4V6X8Z0B2D4F", retrieved.UserID)
	assert.Equal(t, "admin@co.com", retrieved.Email)
	assert.Equal(t, "Admin", retrieved.DisplayName)
	assert.Equal(t, "admin", retrieved.Role)
	assert.Equal(t, "github", retrieved.Provider)
}

func TestAdminUserContext_HasFullAccess(t *testing.T) {
	ac := AdminUserContext("01HQ3K4N6P8R2S4V6X8Z0B2D4F", "admin@co.com", "Admin", "github")

	// Admin users should have full server access and all permissions, just like API key admins.
	assert.True(t, ac.IsAdmin())
	assert.True(t, ac.CanAccessServer("github"))
	assert.True(t, ac.CanAccessServer("any-server"))
	assert.True(t, ac.HasPermission(PermRead))
	assert.True(t, ac.HasPermission(PermWrite))
	assert.True(t, ac.HasPermission(PermDestructive))
}

func TestUserContext_NoImplicitAccess(t *testing.T) {
	ac := UserContext("01HQ3K4N6P8R2S4V6X8Z0B2D4F", "user@co.com", "User", "google")

	// Regular users are not admins and have no implicit server/permission access.
	assert.False(t, ac.IsAdmin())
	assert.False(t, ac.CanAccessServer("github"))
	assert.False(t, ac.HasPermission(PermRead))
}
