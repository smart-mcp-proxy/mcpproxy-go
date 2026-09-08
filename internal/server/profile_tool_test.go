package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// setProfileCtx builds a request context carrying a stable session id and an
// optional agent-token profile_pin. The pinned token carries the "*" server
// wildcard that REST minting defaults to (internal/httpapi/tokens.go) — an
// agent token with NO allowed_servers grants nothing under CanAccessServer, so
// a pin-only fixture would model a token that cannot see any server.
func setProfileCtx(sessionID, pin string) context.Context {
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: sessionID})
	if pin != "" {
		ctx = auth.WithAuthContext(ctx, &auth.AuthContext{Type: auth.AuthTypeAgent, ProfilePin: pin, AllowedServers: []string{"*"}})
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
			{Name: "all", Servers: []string{"research-srv", "deploy-srv"}},
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

// setProfileScopedCtx builds a request context for an UNPINNED agent token
// whose AllowedServers is restricted to the given servers (Spec 104 FR-016b).
func setProfileScopedCtx(sessionID string, allowed ...string) context.Context {
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: sessionID})
	return auth.WithAuthContext(ctx, &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: allowed})
}

// setProfileAdminCtx builds a request context for an API-key / socket admin.
func setProfileAdminCtx(sessionID string) context.Context {
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: sessionID})
	return auth.WithAuthContext(ctx, auth.AdminContext())
}

func setProfileScopedPayload(t *testing.T, res *mcp.CallToolResult) (string, []string) {
	t.Helper()
	require.False(t, res.IsError, "unexpected set_profile error: %s", setProfileResultText(t, res))
	var payload struct {
		ActiveProfile string   `json:"active_profile"`
		Servers       []string `json:"servers"`
	}
	require.NoError(t, json.Unmarshal([]byte(setProfileResultText(t, res)), &payload))
	return payload.ActiveProfile, payload.Servers
}

// TestHandleSetProfile_ScopedTokenClearReportsOnlyAllowedServers: an unpinned
// agent token restricted to one server that clears its selection must be told
// about THAT server only — not every configured server (Spec 104 FR-016b).
func TestHandleSetProfile_ScopedTokenClearReportsOnlyAllowedServers(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileScopedCtx("sess-scoped-clear", "research-srv")

	active, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.Equal(t, "", active)
	require.ElementsMatch(t, []string{"research-srv"}, servers,
		"clearing the selection must not enumerate servers outside the token's AllowedServers")
}

// TestHandleSetProfile_ScopedTokenSelectIntersectsAllowedServers: selecting a
// profile returns the profile's servers INTERSECTED with the token's
// AllowedServers, never the profile's complete set (Spec 104 FR-016b).
func TestHandleSetProfile_ScopedTokenSelectIntersectsAllowedServers(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileScopedCtx("sess-scoped-select", "research-srv")

	active, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, "all"))
	require.Equal(t, "all", active)
	require.ElementsMatch(t, []string{"research-srv"}, servers,
		"a profile's servers outside the token's AllowedServers must not be reported")
	require.Equal(t, "all", p.sessionStore.GetActiveProfile("sess-scoped-select"))

}

// TestHandleSetProfile_ScopedTokenDisjointProfileIndistinguishableFromUnknown:
// a profile entirely outside the token's reach must not be confirmable by
// probing its name — selecting it yields the SAME error as a nonexistent slug,
// and the session is not mutated (Spec 104 FR-016b, cross-review finding).
func TestHandleSetProfile_ScopedTokenDisjointProfileIndistinguishableFromUnknown(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileScopedCtx("sess-scoped-disjoint", "research-srv")

	disjoint := callSetProfileTool(t, p, ctx, "deploy")
	unknown := callSetProfileTool(t, p, ctx, "nope")
	require.True(t, disjoint.IsError, "a disjoint profile must not be selectable")
	require.True(t, unknown.IsError)
	require.Equal(t,
		strings.ReplaceAll(setProfileResultText(t, unknown), "'nope'", "'deploy'"),
		setProfileResultText(t, disjoint),
		"disjoint and unknown slugs must produce the same error shape")
	require.NotContains(t, setProfileResultText(t, disjoint), "deploy-srv")
	require.Equal(t, "", p.sessionStore.GetActiveProfile("sess-scoped-disjoint"))
}

// TestHandleSetProfile_EmptyAllowlistTokenSeesNothing: an agent token whose
// AllowedServers is EMPTY grants nothing under CanAccessServer (the predicate
// serverInScope applies to retrieve_tools), so set_profile must report the
// same empty reach rather than read "empty" as "unrestricted".
func TestHandleSetProfile_EmptyAllowlistTokenSeesNothing(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileScopedCtx("sess-empty-allow")

	_, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.Empty(t, servers)

	res := callSetProfileTool(t, p, ctx, "nope")
	require.True(t, res.IsError)
	text := setProfileResultText(t, res)
	for _, name := range []string{"research", "deploy", "all"} {
		require.NotContains(t, text, name)
	}
}

// TestHandleSetProfile_ServerEditionUserScopedLikeVisibility: a server-edition
// "user" context with a server allowlist is scoped by serverInScope exactly
// like an agent token, so set_profile applies the same bound.
func TestHandleSetProfile_ServerEditionUserScopedLikeVisibility(t *testing.T) {
	p := newSetProfileTestServer()
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: "sess-user"})
	ctx = auth.WithAuthContext(ctx, &auth.AuthContext{Type: auth.AuthTypeUser, AllowedServers: []string{"deploy-srv"}})

	_, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.ElementsMatch(t, []string{"deploy-srv"}, servers)
}

// TestHandleSetProfile_ScopedTokenUnknownSlugDoesNotEnumerateAllProfiles: the
// invalid-selection error for an agent token names only the profiles the token
// may select — never profiles entirely outside its reach (Spec 104 FR-016b).
func TestHandleSetProfile_ScopedTokenUnknownSlugDoesNotEnumerateAllProfiles(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileScopedCtx("sess-scoped-unknown", "research-srv")

	res := callSetProfileTool(t, p, ctx, "nope")
	require.True(t, res.IsError)
	text := setProfileResultText(t, res)
	require.Contains(t, text, "unknown profile 'nope'")
	require.Contains(t, text, "research", "profiles overlapping the token's scope stay selectable")
	require.NotContains(t, text, "deploy", "a profile fully outside the token's scope must not be disclosed")
	require.Equal(t, "", p.sessionStore.GetActiveProfile("sess-scoped-unknown"))
}

// TestHandleSetProfile_StalePinUnknownSlugDisclosesNoProfiles: a token pinned
// to a profile that has since been removed reaches the unknown-slug branch
// (slug == pin passes the pin guard); the error must not enumerate the
// configured profiles it can never select.
func TestHandleSetProfile_StalePinUnknownSlugDisclosesNoProfiles(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileCtx("sess-stale-pin", "gone")

	res := callSetProfileTool(t, p, ctx, "gone")
	require.True(t, res.IsError)
	text := setProfileResultText(t, res)
	require.Contains(t, text, "unknown profile 'gone'")
	require.NotContains(t, text, "available: gone", "a removed pin is not a selectable profile")
	for _, name := range []string{"research", "deploy", "all"} {
		require.NotContains(t, text, name)
	}
	require.Equal(t, "", p.sessionStore.GetActiveProfile("sess-stale-pin"))
}

// TestHandleSetProfile_PinnedTokenClearIntersectsAllowedServers: a pinned token
// whose AllowedServers is narrower than its pin sees the intersection.
func TestHandleSetProfile_PinnedTokenClearIntersectsAllowedServers(t *testing.T) {
	p := newSetProfileTestServer()
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: "sess-pin-narrow"})
	ctx = auth.WithAuthContext(ctx, &auth.AuthContext{
		Type: auth.AuthTypeAgent, ProfilePin: "all", AllowedServers: []string{"deploy-srv"},
	})

	active, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.Equal(t, "all", active)
	require.ElementsMatch(t, []string{"deploy-srv"}, servers)
}

// TestHandleSetProfile_AdminUnchanged pins the administrator (API-key / socket)
// behaviour: full server list on clear, the profile's complete set on select,
// and every configured profile in the unknown-slug error.
func TestHandleSetProfile_AdminUnchanged(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileAdminCtx("sess-admin")

	_, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.ElementsMatch(t, []string{"research-srv", "deploy-srv"}, servers)

	active, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, "all"))
	require.Equal(t, "all", active)
	require.ElementsMatch(t, []string{"research-srv", "deploy-srv"}, servers)

	res := callSetProfileTool(t, p, ctx, "nope")
	require.True(t, res.IsError)
	text := setProfileResultText(t, res)
	for _, name := range []string{"research", "deploy", "all"} {
		require.Contains(t, text, name)
	}
}

// TestHandleSetProfile_WildcardTokenUnchanged: an agent token with the "*"
// wildcard is unrestricted by servers and keeps the full listings.
func TestHandleSetProfile_WildcardTokenUnchanged(t *testing.T) {
	p := newSetProfileTestServer()
	ctx := setProfileScopedCtx("sess-wild", "*")

	_, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, ""))
	require.ElementsMatch(t, []string{"research-srv", "deploy-srv"}, servers)

	res := callSetProfileTool(t, p, ctx, "nope")
	require.True(t, res.IsError)
	require.Contains(t, setProfileResultText(t, res), "deploy")
}

// TestHandleSetProfile_PinnedTokenSelectsDisjointPin locks the admission
// decision: a configured pin is always selectable by its own token (the token
// already knows its pin exists), even when the pin's servers are disjoint from
// the token's AllowedServers — the reach is then correctly empty.
func TestHandleSetProfile_PinnedTokenSelectsDisjointPin(t *testing.T) {
	p := newSetProfileTestServer()
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	ctx := helper.WithContext(context.Background(), &fakeClientSession{id: "sess-pin-disjoint"})
	ctx = auth.WithAuthContext(ctx, &auth.AuthContext{
		Type: auth.AuthTypeAgent, ProfilePin: "deploy", AllowedServers: []string{"research-srv"},
	})

	active, servers := setProfileScopedPayload(t, callSetProfileTool(t, p, ctx, "deploy"))
	require.Equal(t, "deploy", active)
	require.Empty(t, servers)
	require.Equal(t, "deploy", p.sessionStore.GetActiveProfile("sess-pin-disjoint"))
}
