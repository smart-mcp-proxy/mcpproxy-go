package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// setProfileCtx builds a request context carrying a stable session id and an
// optional agent-token profile_pin.
func setProfileCtx(sessionID, pin string) context.Context {
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: sessionID})
	if pin != "" {
		ctx = auth.WithAuthContext(ctx, &auth.AuthContext{Type: auth.AuthTypeAgent, ProfilePin: pin})
	}
	return ctx
}

func newSetProfileTestServer() *MCPProxyServer {
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
	return &MCPProxyServer{
		config:       cfg,
		logger:       zap.NewNop(),
		sessionStore: NewSessionStore(zap.NewNop()),
	}
}

func callSetProfileTool(t *testing.T, p *MCPProxyServer, ctx context.Context, slug string) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = "set_profile"
	req.Params.Arguments = map[string]interface{}{"profile": slug}
	res, err := p.handleSetProfile(ctx, req)
	require.NoError(t, err)
	return res
}

func setProfileResultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	b, err := json.Marshal(res.Content[0])
	require.NoError(t, err)
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &m))
	text, _ := m["text"].(string)
	return text
}

// TestHandleSetProfile_PinnedRejectsOtherSlug verifies a profile-pinned agent
// token cannot switch away from its pinned profile via set_profile (Profiles v2 T3).
func TestHandleSetProfile_PinnedRejectsOtherSlug(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileCtx("sess-pinned", "research")

	res := callSetProfileTool(t, p, ctx, "deploy")
	require.True(t, res.IsError, "switching a pinned token to another profile must error")
	require.Contains(t, setProfileResultText(t, res), "pinned to profile 'research'")

	// The session selection must NOT have been changed by the rejected call.
	require.Equal(t, "", p.sessionStore.GetActiveProfile("sess-pinned"))
}

// TestHandleSetProfile_PinnedAllowsSameSlug verifies set_profile to the pinned
// profile itself succeeds.
func TestHandleSetProfile_PinnedAllowsSameSlug(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileCtx("sess-same", "research")

	res := callSetProfileTool(t, p, ctx, "research")
	require.False(t, res.IsError, "set_profile to the pinned profile must succeed: %s", setProfileResultText(t, res))

	var payload struct {
		ActiveProfile string   `json:"active_profile"`
		Servers       []string `json:"servers"`
	}
	require.NoError(t, json.Unmarshal([]byte(setProfileResultText(t, res)), &payload))
	require.Equal(t, "research", payload.ActiveProfile)
	require.Contains(t, payload.Servers, "research-srv")
}

// TestHandleSetProfile_UnpinnedUnchanged verifies tokens without a pin retain
// the existing T2 switching behaviour.
func TestHandleSetProfile_UnpinnedUnchanged(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileCtx("sess-free", "")

	res := callSetProfileTool(t, p, ctx, "deploy")
	require.False(t, res.IsError, "unpinned token must switch freely: %s", setProfileResultText(t, res))
	require.Equal(t, "deploy", p.sessionStore.GetActiveProfile("sess-free"))
}
