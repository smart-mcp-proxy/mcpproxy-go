package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 118 (issue #1686): a pinned caller's visible set is its grant
// intersected with the reach of its pin, computed once and carried as the
// AuthContext's AllowedServers so every consumer of the grant agrees.
func TestAuthContext_BoundToPinReach(t *testing.T) {
	t.Run("star grant takes the whole reach", func(t *testing.T) {
		ac := &AuthContext{Type: AuthTypeAgent, AllowedServers: []string{"*"}, ProfilePin: "p"}
		out := ac.BoundToPinReach([]string{"a", "b"})
		assert.Equal(t, []string{"a", "b"}, out.AllowedServers)
		assert.Equal(t, []string{"*"}, ac.AllowedServers, "the input is never edited in place")
		assert.Equal(t, "p", out.ProfilePin)
	})
	t.Run("listed grant intersects, in reach order", func(t *testing.T) {
		ac := &AuthContext{Type: AuthTypeAgent, AllowedServers: []string{"c", "a"}, ProfilePin: "p"}
		assert.Equal(t, []string{"a"}, ac.BoundToPinReach([]string{"a", "b"}).AllowedServers)
	})
	t.Run("missing or empty reach yields an empty non-nil set", func(t *testing.T) {
		ac := &AuthContext{Type: AuthTypeAgent, AllowedServers: []string{"*"}, ProfilePin: "gone"}
		for _, reach := range [][]string{nil, {}} {
			out := ac.BoundToPinReach(reach)
			require.NotNil(t, out.AllowedServers)
			assert.Empty(t, out.AllowedServers)
			assert.False(t, out.CanAccessServer("a"))
		}
	})
	t.Run("empty grant stays empty whatever the reach", func(t *testing.T) {
		ac := &AuthContext{Type: AuthTypeAgent, AllowedServers: []string{}, ProfilePin: "p"}
		out := ac.BoundToPinReach([]string{"a"})
		require.NotNil(t, out.AllowedServers)
		assert.Empty(t, out.AllowedServers)
	})
	t.Run("reach is deduplicated", func(t *testing.T) {
		ac := &AuthContext{Type: AuthTypeAgent, AllowedServers: []string{"*"}, ProfilePin: "p"}
		assert.Equal(t, []string{"a"}, ac.BoundToPinReach([]string{"a", "a"}).AllowedServers)
	})
	t.Run("admins and unpinned callers are returned untouched", func(t *testing.T) {
		admin := AdminContext()
		assert.Same(t, admin, admin.BoundToPinReach([]string{"a"}))
		unpinned := &AuthContext{Type: AuthTypeAgent, AllowedServers: []string{"*"}}
		assert.Same(t, unpinned, unpinned.BoundToPinReach([]string{"a"}))
		var nilCtx *AuthContext
		assert.Nil(t, nilCtx.BoundToPinReach([]string{"a"}))
	})
	t.Run("the bound context drives the shared enumeration predicate", func(t *testing.T) {
		ac := &AuthContext{Type: AuthTypeAgent, AllowedServers: []string{"*"}, ProfilePin: "p"}
		ctx := WithAuthContext(context.Background(), ac.BoundToPinReach([]string{"a"}))
		assert.True(t, CanEnumerateServer(ctx, "a"))
		assert.False(t, CanEnumerateServer(ctx, "b"))
		assert.True(t, IsScopedCaller(ctx))
	})
}
