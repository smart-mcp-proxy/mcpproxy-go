package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

const initializeMessage = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"cursor","version":"1"}}}`

// initSession registers a notifying session on the MCP server and runs a real
// initialize through it under ctx's credential, so the AfterInitialize hook
// stamps identity exactly as it does for a live client.
func initSession(t *testing.T, srvFor func() (register func(context.Context, *notifyingSession) error, withCtx func(context.Context, *notifyingSession) context.Context, handle func(context.Context, []byte)), id string, base context.Context) *notifyingSession {
	t.Helper()
	register, withCtx, handle := srvFor()
	sess := newNotifyingSession(id)
	require.NoError(t, register(context.Background(), sess))
	handle(withCtx(base, sess), []byte(initializeMessage))
	return sess
}

func defaultSurface(p *MCPProxyServer) func() (func(context.Context, *notifyingSession) error, func(context.Context, *notifyingSession) context.Context, func(context.Context, []byte)) {
	return func() (func(context.Context, *notifyingSession) error, func(context.Context, *notifyingSession) context.Context, func(context.Context, []byte)) {
		return func(ctx context.Context, s *notifyingSession) error { return p.server.RegisterSession(ctx, s) },
			func(ctx context.Context, s *notifyingSession) context.Context { return p.server.WithContext(ctx, s) },
			func(ctx context.Context, msg []byte) { p.server.HandleMessage(ctx, msg) }
	}
}

func directSurface(p *MCPProxyServer) func() (func(context.Context, *notifyingSession) error, func(context.Context, *notifyingSession) context.Context, func(context.Context, []byte)) {
	return func() (func(context.Context, *notifyingSession) error, func(context.Context, *notifyingSession) context.Context, func(context.Context, []byte)) {
		return func(ctx context.Context, s *notifyingSession) error { return p.directServer.RegisterSession(ctx, s) },
			func(ctx context.Context, s *notifyingSession) context.Context {
				return p.directServer.WithContext(ctx, s)
			},
			func(ctx context.Context, msg []byte) { p.directServer.HandleMessage(ctx, msg) }
	}
}

// TestSessionIdentity_StampedAtInitialize pins T039 / FR-028: initialize with a
// client credential records the token name, client id and the serving server
// instance; an admin-key session records no identity.
func TestSessionIdentity_StampedAtInitialize(t *testing.T) {
	proxy, _ := createTestProxyWithRuntimeCfg(t, []*config.ServerConfig{{Name: "a", Enabled: true}}, nil)
	require.NotNil(t, proxy.server)
	require.NotNil(t, proxy.directServer)

	initSession(t, defaultSurface(proxy), "cursor-default", clientCtx("cursor", "ro", auth.ProfileModeLocked))
	initSession(t, directSurface(proxy), "cursor-direct", clientCtx("cursor", "ro", auth.ProfileModeLocked))
	initSession(t, defaultSurface(proxy), "admin-1", auth.WithAuthContext(context.Background(), auth.AdminContext()))

	def := proxy.sessionStore.GetSession("cursor-default")
	require.NotNil(t, def)
	require.Equal(t, "client-cursor", def.TokenName)
	require.Equal(t, "cursor", def.ClientID)
	require.Same(t, proxy.server, def.server, "the session records the instance that serves it")

	direct := proxy.sessionStore.GetSession("cursor-direct")
	require.NotNil(t, direct)
	require.Same(t, proxy.directServer, direct.server)

	admin := proxy.sessionStore.GetSession("admin-1")
	require.NotNil(t, admin)
	require.Empty(t, admin.TokenName)
	require.Empty(t, admin.ClientID)

	targets := proxy.sessionStore.NotifyTargets("client-cursor")
	require.Len(t, targets, 2)
	require.Empty(t, proxy.sessionStore.NotifyTargets("client-windsurf"))
}

// TestSessionIdentity_ProfileRecordedFromResolution: every resolution records
// the latest effective profile and source on the calling session (D23).
func TestSessionIdentity_ProfileRecordedFromResolution(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	sess := initSession(t, defaultSurface(proxy), "cursor-1", clientCtx("cursor", "work-readonly", auth.ProfileModeLocked))
	ctx := proxy.server.WithContext(clientCtx("cursor", "work-readonly", auth.ProfileModeLocked), sess)

	idx := proxy.profileIndexFor(proxy.currentConfig())
	res := proxy.ResolveProfileV3(ctx, idx)
	require.Equal(t, string(profile.SourcePin), res.Source)

	info := proxy.sessionStore.GetSession("cursor-1")
	require.NotNil(t, info)
	require.Equal(t, "work-readonly", info.Profile)
	require.Equal(t, string(profile.SourcePin), info.ProfileSource)
}
