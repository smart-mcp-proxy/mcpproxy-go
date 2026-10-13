//go:build server

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// TestHandleGetAttention_BranchesOnScopedCaller pins T055b (FR-007): the
// handler (via filterAttentionItems) must branch on auth.IsScopedCaller, not
// on Type == AuthTypeAgent — a non-admin AuthTypeUser session (server
// edition) must be narrowed exactly like an agent token, and a caller with no
// AuthContext at all (bootstrap passthrough) must see everything.
//
// Built with -tags server: AuthTypeUser is a first-class server-edition
// identity, and this file runs in the server-edition CI race lane.
func TestHandleGetAttention_BranchesOnScopedCaller(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	ctrl.attentionItemsOverride = []contracts.AttentionItem{
		{ID: "sign_in_required:server:alpha", Kind: "sign_in_required", Subject: contracts.AttentionSubject{Type: "server", ID: "alpha", Name: "alpha"}},
		{ID: "server_error:server:beta", Kind: "server_error", Subject: contracts.AttentionSubject{Type: "server", ID: "beta", Name: "beta"}},
		{ID: "client_never_seen:client:codex", Kind: "client_never_seen", Subject: contracts.AttentionSubject{Type: "client", ID: "codex", Name: "Codex"}},
	}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	call := func(ac *auth.AuthContext) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/attention", http.NoBody)
		if ac != nil {
			req = req.WithContext(auth.WithAuthContext(req.Context(), ac))
		}
		rec := httptest.NewRecorder()
		srv.handleGetAttention(rec, req)
		return rec
	}

	// Admin API key: every item.
	adminRec := call(auth.AdminContext())
	require.Equal(t, http.StatusOK, adminRec.Code)
	assert.Contains(t, adminRec.Body.String(), `"beta"`)
	assert.Contains(t, adminRec.Body.String(), `"codex"`)

	// Agent token scoped to alpha: only alpha, no client, count recomputed.
	agentRec := call(&auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}})
	require.Equal(t, http.StatusOK, agentRec.Code)
	assert.Contains(t, agentRec.Body.String(), `"alpha"`)
	assert.NotContains(t, agentRec.Body.String(), `"beta"`)
	assert.NotContains(t, agentRec.Body.String(), `"codex"`)
	assert.Contains(t, agentRec.Body.String(), `"count":1`)

	// Non-admin AuthTypeUser session, built directly (never goes through the
	// agent-token auth path): must be narrowed exactly like the agent token.
	// This is the case that a Type == AuthTypeAgent branch would fail open on.
	userRec := call(&auth.AuthContext{Type: auth.AuthTypeUser, AllowedServers: []string{"alpha"}})
	require.Equal(t, http.StatusOK, userRec.Code)
	assert.Contains(t, userRec.Body.String(), `"alpha"`)
	assert.NotContains(t, userRec.Body.String(), `"beta"`)
	assert.NotContains(t, userRec.Body.String(), `"codex"`)

	// Non-admin AuthTypeUser session with no allowed_servers: count 0.
	emptyUserRec := call(&auth.AuthContext{Type: auth.AuthTypeUser})
	require.Equal(t, http.StatusOK, emptyUserRec.Code)
	assert.Contains(t, emptyUserRec.Body.String(), `"count":0`)

	// No AuthContext at all (bootstrap passthrough): every item.
	noAuthRec := call(nil)
	require.Equal(t, http.StatusOK, noAuthRec.Code)
	assert.Contains(t, noAuthRec.Body.String(), `"beta"`)
	assert.Contains(t, noAuthRec.Body.String(), `"codex"`)
}
