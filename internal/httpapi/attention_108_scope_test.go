package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// 109-l P4: scoped callers are filtered by an ALLOW-list. Every Spec 108 kind
// is administrator-only, and an unknown future subject type fails closed.
func attn108Items() []contracts.AttentionItem {
	return []contracts.AttentionItem{
		{ID: "anonymous_denied_by_binding_guard:setting:require_mcp_auth", Kind: "anonymous_denied_by_binding_guard", Rank: 4, Subject: contracts.AttentionSubject{Type: "setting", ID: "require_mcp_auth", Name: "Anonymous callers"}},
		{ID: "client_holds_admin_key:client:cursor", Kind: "client_holds_admin_key", Rank: 5, Subject: contracts.AttentionSubject{Type: "client", ID: "cursor", Name: "Cursor"}},
		{ID: "client_credential_expiring:client:codex", Kind: "client_credential_expiring", Rank: 9, Subject: contracts.AttentionSubject{Type: "client", ID: "codex", Name: "Codex"}},
		{ID: "future_kind:future:x", Kind: "future_kind", Subject: contracts.AttentionSubject{Type: "future", ID: "x", Name: "x"}},
		{ID: "sign_in_required:server:alpha", Kind: "sign_in_required", Rank: 10, Subject: contracts.AttentionSubject{Type: "server", ID: "alpha", Name: "alpha"}},
		{ID: "server_error:server:beta", Kind: "server_error", Rank: 40, Subject: contracts.AttentionSubject{Type: "server", ID: "beta", Name: "beta"}},
	}
}

func TestHandleGetAttention_108KindsAreAdminOnly(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	ctrl.attentionItemsOverride = attn108Items()
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	call := func(ac *auth.AuthContext) string {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/attention", http.NoBody)
		if ac != nil {
			req = req.WithContext(auth.WithAuthContext(req.Context(), ac))
		}
		rec := httptest.NewRecorder()
		srv.handleGetAttention(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		return rec.Body.String()
	}

	admin := call(auth.AdminContext())
	for _, id := range []string{"require_mcp_auth", "client_holds_admin_key", "client_credential_expiring", "future_kind", "sign_in_required:server:alpha"} {
		assert.Contains(t, admin, id)
	}
	assert.Contains(t, admin, `"count":6`)

	for name, ac := range map[string]*auth.AuthContext{
		"agent token": {Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}},
		"user":        {Type: auth.AuthTypeUser, AllowedServers: []string{"alpha"}},
	} {
		t.Run(name, func(t *testing.T) {
			body := call(ac)
			assert.Contains(t, body, "sign_in_required:server:alpha")
			assert.NotContains(t, body, "beta")
			assert.NotContains(t, body, "require_mcp_auth", "the setting subject never reaches a scoped caller")
			assert.NotContains(t, body, "client_holds_admin_key")
			assert.NotContains(t, body, "client_credential_expiring")
			assert.NotContains(t, body, "future_kind", "an unknown subject type fails closed")
			assert.Contains(t, body, `"count":1`)
		})
	}
}

func TestRenderAttentionChangedForCaller_AllowList(t *testing.T) {
	payload := map[string]interface{}{
		"count": 5,
		"items": []internalRuntime.AttentionEventItem{
			{ID: "g", SubjectType: "setting", SubjectID: "require_mcp_auth"},
			{ID: "c", SubjectType: "client", SubjectID: "cursor"},
			{ID: "f", SubjectType: "future", SubjectID: "x"},
			{ID: "a", SubjectType: "server", SubjectID: "alpha"},
			{ID: "b", SubjectType: "server", SubjectID: "beta"},
		},
	}
	adminCtx := auth.WithAuthContext(context.Background(), auth.AdminContext())
	admin := renderAttentionChangedForCaller(adminCtx, payload)
	assert.Equal(t, 5, admin["count"])

	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}})
	got := renderAttentionChangedForCaller(scoped, payload)
	assert.Equal(t, []string{"a"}, got["ids"])
	assert.Equal(t, 1, got["count"])
}

func TestSSE_AttentionChanged108KindsNeverReachScopedSubscriber(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	adminBody, adminClose := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer adminClose()
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()
	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 2 }, 5*time.Second, 20*time.Millisecond)

	evt := internalRuntime.Event{
		Type: internalRuntime.EventTypeAttentionChanged,
		Payload: map[string]any{
			"count": 3,
			"items": []internalRuntime.AttentionEventItem{
				{ID: "anonymous_denied_by_binding_guard:setting:require_mcp_auth", SubjectType: "setting", SubjectID: "require_mcp_auth"},
				{ID: "future_kind:future:x", SubjectType: "future", SubjectID: "x"},
				{ID: "sign_in_required:server:alpha", SubjectType: "server", SubjectID: "alpha"},
			},
		},
		Timestamp: time.Now(),
	}
	deadline := time.Now().Add(10 * time.Second)
	done := make(chan sseEvent, 2)
	go func() { done <- readSSEUntil(t, adminBody, "attention.changed", deadline) }()
	agentDone := make(chan sseEvent, 1)
	go func() { agentDone <- readSSEUntil(t, agentBody, "attention.changed", deadline) }()
	ctrl.publishToAll(evt)

	adminIDs, _ := (<-done).Data["payload"].(map[string]interface{})["ids"].([]interface{})
	assert.Len(t, adminIDs, 3)
	agentIDs, _ := (<-agentDone).Data["payload"].(map[string]interface{})["ids"].([]interface{})
	assert.Equal(t, []interface{}{"sign_in_required:server:alpha"}, agentIDs)
}
