package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// T033 gap: an invalid mcp_agt_ token is still 401 at the middleware when
// anonymous_profile is set and auth is off — it is never downgraded to an
// anonymous (confined) caller.
func TestAnonymousProfile_InvalidAgentTokenIsStill401(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.RequireMCPAuth = false
	cfg.AnonymousProfile = "work-readonly"
	cfg.Profiles = enforcementMatrixProfiles()
	rt, err := runtime.New(cfg, "", zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })
	srv := &Server{runtime: rt, logger: zap.NewNop()}

	reached := false
	handler := srv.mcpAuthMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { reached = true }))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+rowSecret(auth.TokenPrefixStr, 4242))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	require.False(t, reached)
}

// T033 gap: a confined anonymous caller asking for another configured profile
// gets the byte-identical Spec 105 refusal an unknown slug gets (slug echo
// aside): the profile URL never confirms which profiles exist.
func TestAnonymousProfile_ProfileURLRefusalIsUniform(t *testing.T) {
	cfg := &config.Config{AnonymousProfile: "work-readonly", Profiles: enforcementMatrixProfiles()}
	cfg.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}, {Name: "notion", Enabled: true}, {Name: "filesystem", Enabled: true}}
	srv := &Server{logger: zap.NewNop()}
	idx := newProfileIndex(cfg)

	get := func(slug string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "/mcp/p/"+slug, http.NoBody)
		w := httptest.NewRecorder()
		srv.serveProfileURL(w, req, idx, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Errorf("the refused request must not reach the MCP handler")
		}))
		return w.Code, strings.ReplaceAll(w.Body.String(), slug, "<slug>")
	}
	otherCode, otherBody := get("legacy") // configured, not selectable from work-readonly
	unknownCode, unknownBody := get("zzz-no-such")
	require.Equal(t, http.StatusNotFound, otherCode)
	require.Equal(t, otherCode, unknownCode)
	require.Equal(t, unknownBody, otherBody, "a configured-but-forbidden profile is indistinguishable from an unknown one")
	require.NotContains(t, otherBody, "available")
}

// T033 gap: with anonymous_profile unset, tools/list for an anonymous caller is
// byte-identical to the administrator's (legacy behaviour, SC-005).
func TestAnonymousProfile_UnsetToolsListMatchesAdminView(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	list := func(ctx context.Context) string {
		msg := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/list"}`)
		out, err := json.Marshal(proxy.server.HandleMessage(ctx, msg))
		require.NoError(t, err)
		return string(out)
	}
	anonymous := list(auth.WithAuthContext(context.Background(), auth.AnonymousContext()))
	admin := list(auth.WithAuthContext(context.Background(), auth.AdminContext()))
	require.Equal(t, admin, anonymous)
	require.Contains(t, admin, "retrieve_tools")
}
