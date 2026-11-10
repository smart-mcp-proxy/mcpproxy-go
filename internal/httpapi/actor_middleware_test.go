package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Issue #1698: every /api/v1 request carries its caller (kind, name, surface)
// in the context once authenticated, so the runtime can name who changed a
// server or its quarantine state without each handler threading the actor.
func TestActorContextMiddleware_StampsAuthenticatedCaller(t *testing.T) {
	srv := NewServer(&mockTokenController{apiKey: "test-api-key"}, zap.NewNop().Sugar(), nil)

	var got internalRuntime.Actor
	var ok bool
	h := srv.actorContextMiddleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = internalRuntime.RequestActor(r.Context())
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", nil)
	req.Header.Set(XMCPProxySurfaceHeader, "cli")
	req = req.WithContext(auth.WithAuthContext(req.Context(), &auth.AuthContext{Type: auth.AuthTypeAgent, AgentName: "ops-bot"}))
	h.ServeHTTP(httptest.NewRecorder(), req)

	require.True(t, ok)
	assert.Equal(t, string(auth.CredentialKindAgentToken), got.Kind)
	assert.Equal(t, "ops-bot", got.Name)
	assert.Equal(t, profile.SurfaceCLI, got.Surface)
}

func TestAPIRoutes_CarryActorToController(t *testing.T) {
	ctrl := &mockTokenController{apiKey: "test-api-key"}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	// A real route under /api/v1 — the quarantine endpoint — reaches the
	// controller with the stamped actor when it implements the ctx variant.
	w := doRequest(t, srv, http.MethodPost, "/api/v1/servers/tenant/quarantine", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, ctrl.lastQuarantineCtx, "the handler used QuarantineServerCtx")
	got, ok := internalRuntime.RequestActor(ctrl.lastQuarantineCtx)
	require.True(t, ok)
	assert.Equal(t, string(auth.CredentialKindAPIKey), got.Kind)
	assert.Equal(t, profile.SurfaceAPI, got.Surface)
}

// Round-2 review: the legacy REST enable/disable and restart fallbacks (no
// management service, as in this harness) also hand the controller the
// stamped context when it implements the ctx variants.
func TestAPIRoutes_LegacyToggleAndRestartCarryActor(t *testing.T) {
	ctrl := &mockTokenController{apiKey: "test-api-key", servers: []string{"tenant"}}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	w := doRequest(t, srv, http.MethodPost, "/api/v1/servers/tenant/enable", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, ctrl.lastEnableCtx, "the fallback used EnableServerCtx")
	a, ok := internalRuntime.RequestActor(ctrl.lastEnableCtx)
	require.True(t, ok)
	assert.Equal(t, string(auth.CredentialKindAPIKey), a.Kind)

	w = doRequest(t, srv, http.MethodPost, "/api/v1/servers/tenant/restart", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotNil(t, ctrl.lastRestartCtx, "the fallback used RestartServerCtx")
	_, ok = internalRuntime.RequestActor(ctrl.lastRestartCtx)
	assert.True(t, ok)
}
