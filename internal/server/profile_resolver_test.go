package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// fakeClientSession is a minimal mcp-go ClientSession for injecting a stable
// session id into a context via MCPServer.WithContext.
type fakeClientSession struct{ id string }

func (f *fakeClientSession) Initialize()                                         {}
func (f *fakeClientSession) Initialized() bool                                   { return true }
func (f *fakeClientSession) NotificationChannel() chan<- mcp.JSONRPCNotification { return nil }
func (f *fakeClientSession) SessionID() string                                   { return f.id }

// TestResolveActiveProfile_Precedence exercises the Profiles v2 resolver:
// URL > session set_profile > none, and stale-selection cleanup. (The token
// profile_pin tier is a T3 hook that is always "" here.)
func TestResolveActiveProfile_Precedence(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "research-srv"},
			{Name: "deploy-srv"},
		},
		Profiles: []config.ProfileConfig{
			{Name: "research", Servers: []string{"research-srv"}},
			{Name: "deploy", Servers: []string{"deploy-srv"}},
		},
	}
	p := &MCPProxyServer{config: cfg, sessionStore: NewSessionStore(zap.NewNop())}

	helper := mcpserver.NewMCPServer("test", "1.0.0")
	base := helper.WithContext(context.Background(), &fakeClientSession{id: "sess-1"})

	// (1) Nothing set → none.
	name, scope := p.resolveActiveProfile(base)
	require.Equal(t, "", name)
	require.Nil(t, scope)

	// (2) set_profile session selection applies on the base endpoint.
	p.sessionStore.SetActiveProfile("sess-1", "research")
	name, scope = p.resolveActiveProfile(base)
	require.Equal(t, "research", name)
	require.NotNil(t, scope)
	require.True(t, scope.Allows("research-srv"))
	require.False(t, scope.Allows("deploy-srv"))

	// (3) An explicit URL profile overrides the session selection for that request.
	urlCtx := profile.WithProfileScope(base, profile.NewProfileScope("deploy", []string{"deploy-srv"}))
	name, scope = p.resolveActiveProfile(urlCtx)
	require.Equal(t, "deploy", name)
	require.NotNil(t, scope)
	require.True(t, scope.Allows("deploy-srv"))
	require.False(t, scope.Allows("research-srv"))

	// (4) Clearing the session selection returns to none.
	p.sessionStore.SetActiveProfile("sess-1", "")
	name, scope = p.resolveActiveProfile(base)
	require.Equal(t, "", name)
	require.Nil(t, scope)

	// (5) A stale session selection (profile removed from config) is dropped.
	p.sessionStore.SetActiveProfile("sess-1", "ghost")
	name, scope = p.resolveActiveProfile(base)
	require.Equal(t, "", name)
	require.Nil(t, scope)
	require.Equal(t, "", p.sessionStore.GetActiveProfile("sess-1"), "stale selection should be cleared")
}

// TestSessionStore_ActiveProfileLifecycle verifies the per-session profile map
// is set, read and cleared on session close.
func TestSessionStore_ActiveProfileLifecycle(t *testing.T) {
	store := NewSessionStore(zap.NewNop())

	require.Equal(t, "", store.GetActiveProfile("s1"))

	store.SetActiveProfile("s1", "research")
	require.Equal(t, "research", store.GetActiveProfile("s1"))

	// Empty slug clears.
	store.SetActiveProfile("s1", "")
	require.Equal(t, "", store.GetActiveProfile("s1"))

	// Cleared on session close.
	store.SetActiveProfile("s1", "deploy")
	store.RemoveSession("s1")
	require.Equal(t, "", store.GetActiveProfile("s1"))

	// Empty session id is a no-op.
	store.SetActiveProfile("", "research")
	require.Equal(t, "", store.GetActiveProfile(""))
}
