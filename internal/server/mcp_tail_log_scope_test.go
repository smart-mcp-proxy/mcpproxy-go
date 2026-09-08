package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// tailLogCanary is written into every fixture server's log. Its presence in a
// tool response proves the log was disclosed.
const tailLogCanary = "CANARY-upstream-log-line-7f3a"

// newTailLogScopeProxy builds a proxy with two upstreams, "github" and
// "secret", each with a per-server log file AND a registered (never
// connected) upstream client so a served response carries connection_status,
// plus a profile "gh" that contains only "github". Spec 104 FR-016h: an agent
// token scoped away from "secret" (by AllowedServers or by a profile pin)
// must not learn anything about it through `upstream_servers` `tail_log`.
func newTailLogScopeProxy(t *testing.T) *MCPProxyServer {
	t.Helper()
	proxy := createTestMCPProxyServer(t)

	logDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.Logging.LogDir = logDir
	cfg.Servers = []*config.ServerConfig{
		{Name: "github", Protocol: "http", Enabled: true},
		{Name: "secret", Protocol: "http", Enabled: true},
	}
	cfg.Profiles = []config.ProfileConfig{{Name: "gh", Servers: []string{"github"}}}
	mainSrv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = mainSrv.Shutdown() })
	proxy.mainServer = mainSrv

	for _, name := range []string{"github", "secret"} {
		sc := &config.ServerConfig{Name: name, Protocol: "http", URL: "http://127.0.0.1:1/mcp", Enabled: true}
		require.NoError(t, proxy.storage.SaveUpstreamServer(sc))
		// Register a client without connecting so GetClient resolves and the
		// served response includes connection_status (otherwise the
		// "connection status not disclosed" assertions would pass vacuously).
		require.NoError(t, proxy.upstreamManager.AddServerConfig(name, sc))
		require.NoError(t, os.WriteFile(filepath.Join(logDir, "server-"+name+".log"),
			[]byte(tailLogCanary+" "+name+"\n"), 0o600))
	}
	return proxy
}

// tailLogVia drives the real `upstream_servers` dispatcher — the path an
// agent token reaches on /mcp in retrieve mode — with operation=tail_log.
func tailLogVia(t *testing.T, proxy *MCPProxyServer, ctx context.Context, name string) (*mcp.CallToolResult, string) {
	t.Helper()
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{"operation": "tail_log", "name": name}
	result, err := proxy.handleUpstreamServers(ctx, request)
	require.NoError(t, err)
	return result, toolResultText(t, result)
}

// adminTailLogNotFound returns the response an unrestricted caller gets for a
// server that genuinely does not exist — the storage-not-found path, not the
// scope-refusal path — so the scoped comparison below is against the real
// "no such server" shape rather than against itself.
func adminTailLogNotFound(t *testing.T, proxy *MCPProxyServer, name string) string {
	t.Helper()
	result, body := tailLogVia(t, proxy, context.Background(), name)
	require.True(t, result.IsError, "baseline: a nonexistent server must be an error")
	require.Contains(t, body, "not found")
	return body
}

// assertTailLogHidden asserts the response for an existing but out-of-scope
// server is byte-identical (modulo the echoed name) to the unrestricted
// response for a server that does not exist: no existence, no status, no logs.
func assertTailLogHidden(t *testing.T, proxy *MCPProxyServer, ctx context.Context, name string) {
	t.Helper()
	ghostBody := adminTailLogNotFound(t, proxy, "ghost")

	result, body := tailLogVia(t, proxy, ctx, name)
	assert.True(t, result.IsError, "out-of-scope server must be refused")
	assert.Equal(t, strings.ReplaceAll(ghostBody, "ghost", name), body,
		"refusal must have the same shape as a nonexistent server (no existence oracle)")
	assert.NotContains(t, body, tailLogCanary, "log lines disclosed")
	assert.NotContains(t, body, "server_status", "server status disclosed")
	assert.NotContains(t, body, "connection_status", "connection status disclosed")
}

// assertTailLogServed asserts the in-scope / admin path returns the log,
// the stored flags and the live connection status.
func assertTailLogServed(t *testing.T, proxy *MCPProxyServer, ctx context.Context, name string) {
	t.Helper()
	result, body := tailLogVia(t, proxy, ctx, name)
	assert.False(t, result.IsError, "in-scope tail_log must succeed: %s", body)
	assert.Contains(t, body, tailLogCanary+" "+name)
	assert.Contains(t, body, "server_status")
	assert.Contains(t, body, "connection_status", "fixture must register a client, or the non-disclosure assertions prove nothing")
}

func TestTailLog_ServerRestrictedToken_HidesOutOfScopeServer(t *testing.T) {
	proxy := newTailLogScopeProxy(t)
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "reader",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})

	assertTailLogHidden(t, proxy, ctx, "secret")
	assertTailLogServed(t, proxy, ctx, "github")
}

func TestTailLog_ProfilePinnedToken_HidesServerOutsideProfile(t *testing.T) {
	proxy := newTailLogScopeProxy(t)
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "pinned",
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead},
		ProfilePin:     "gh",
	})

	assertTailLogHidden(t, proxy, ctx, "secret")
	assertTailLogServed(t, proxy, ctx, "github")
}

// A pin whose profile no longer exists resolves to a deny-all scope (see
// resolveActiveProfile); tail_log must honour that rather than widen to the
// token's own server list.
func TestTailLog_StaleProfilePin_DeniesAll(t *testing.T) {
	proxy := newTailLogScopeProxy(t)
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "stale",
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead},
		ProfilePin:     "removed-profile",
	})

	assertTailLogHidden(t, proxy, ctx, "secret")
	assertTailLogHidden(t, proxy, ctx, "github")
}

func TestTailLog_AdminUnchanged(t *testing.T) {
	proxy := newTailLogScopeProxy(t)

	adminCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAdmin})
	assertTailLogServed(t, proxy, adminCtx, "secret")
	assertTailLogServed(t, proxy, adminCtx, "github")

	// No AuthContext at all (in-process / stdio caller) is treated as admin by
	// the shared server-op policy; unchanged here.
	assertTailLogServed(t, proxy, context.Background(), "secret")

	// An admin's AllowedServers is never consulted, even when populated.
	narrowAdmin := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAdmin, AllowedServers: []string{"github"},
	})
	assertTailLogServed(t, proxy, narrowAdmin, "secret")
}

// An explicit URL profile (/mcp/p/<slug>) bounds tail_log for every caller,
// admin included — the same rule `list` and call_tool_* already apply
// (Spec 057 FR-004: profile filtering is independent of agent scope).
func TestTailLog_URLProfileScope_AppliesToAllCallers(t *testing.T) {
	proxy := newTailLogScopeProxy(t)
	scope := profile.NewProfileScope("gh", []string{"github"})

	adminInProfile := profile.WithProfileScope(
		auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAdmin}), scope)
	assertTailLogHidden(t, proxy, adminInProfile, "secret")
	assertTailLogServed(t, proxy, adminInProfile, "github")

	anonInProfile := profile.WithProfileScope(context.Background(), scope)
	assertTailLogHidden(t, proxy, anonInProfile, "secret")
	assertTailLogServed(t, proxy, anonInProfile, "github")
}
