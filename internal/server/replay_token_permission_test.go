package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

func replayTokenCtx(perms ...string) context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "replay-agent", AllowedServers: []string{"*"}, Permissions: perms,
	})
}

// A read-only agent token must not replay a recorded write/destructive call:
// replay dispatches a real call, so it needs the target tool's tier.
func TestReplayToolCall_TokenPermissionTier(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		spec    toolSpec
		perms   []string
		allowed bool
	}{
		{"read-only token, destructive tool", "delete_repo", destructiveSpec("delete_repo"), []string{auth.PermRead}, false},
		{"read-only token, write tool", "create_issue", writeSpec("create_issue"), []string{auth.PermRead}, false},
		{"write token, destructive tool (exact match)", "delete_repo", destructiveSpec("delete_repo"), []string{auth.PermRead, auth.PermWrite}, false},
		{"destructive token, write tool (exact match)", "create_issue", writeSpec("create_issue"), []string{auth.PermDestructive}, false},
		{"write token, write tool", "create_issue", writeSpec("create_issue"), []string{auth.PermWrite}, true},
		{"destructive token, destructive tool", "delete_repo", destructiveSpec("delete_repo"), []string{auth.PermDestructive}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, nil)
			mainSrv := &Server{runtime: rt, mcpProxy: proxy}
			up := startCountingUpstream(t, proxy, rt, "github", tc.spec)
			callID := seedReplayableCall(t, proxy, mainSrv, "github", tc.tool, up.URL)
			require.Eventually(t, func() bool {
				c, ok := rt.UpstreamManager().GetClient("github")
				return ok && c != nil && c.IsConnected()
			}, 5*time.Second, 20*time.Millisecond)

			_, err := mainSrv.ReplayToolCall(replayTokenCtx(tc.perms...), callID, map[string]interface{}{"q": "attacker"})
			if tc.allowed {
				require.NoError(t, err)
				require.Equal(t, int64(1), up.count.Load())
				return
			}
			var refusal *profile.ToolBlockedError
			require.ErrorAs(t, err, &refusal)
			require.Empty(t, up.dispatched(), "refused replay must not reach the upstream")
		})
	}
}

func TestReplayToolCall_AdminUnaffectedByTokenTier(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	mainSrv := &Server{runtime: rt, mcpProxy: proxy}
	up := startCountingUpstream(t, proxy, rt, "github", destructiveSpec("delete_repo"))
	callID := seedReplayableCall(t, proxy, mainSrv, "github", "delete_repo", up.URL)
	require.Eventually(t, func() bool {
		c, ok := rt.UpstreamManager().GetClient("github")
		return ok && c != nil && c.IsConnected()
	}, 5*time.Second, 20*time.Millisecond)

	_, err := mainSrv.ReplayToolCall(auth.WithAuthContext(context.Background(), auth.AdminContext()), callID, nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), up.count.Load())
}

// A scoped caller must not skip the check through the early-return path.
func TestReplayToolCall_ScopedCallerFailsClosedWithoutProxy(t *testing.T) {
	proxy, rt := createTestProxyWithRuntime(t, nil)
	seeder := &Server{runtime: rt, mcpProxy: proxy}
	up := startCountingUpstream(t, proxy, rt, "github", destructiveSpec("delete_repo"))
	callID := seedReplayableCall(t, proxy, seeder, "github", "delete_repo", up.URL)

	noProxy := &Server{runtime: rt}
	_, err := noProxy.ReplayToolCall(replayTokenCtx(auth.PermRead), callID, nil)
	var refusal *profile.ToolBlockedError
	require.ErrorAs(t, err, &refusal)
	require.Empty(t, up.dispatched())
}
