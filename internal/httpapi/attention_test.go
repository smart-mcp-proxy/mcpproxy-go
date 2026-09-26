package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// TestGetAttention_Shape pins T055: GET /attention returns {count,
// generated_at, items[]} with the exact item shape contracts/rest-api.md
// documents, for an administrator.
func TestGetAttention_Shape(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, _ := scopedAgentServer(t, ctrl, []string{"alpha"})

	rec := scopeGet(t, srv, "/api/v1/attention", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Count       int                       `json:"count"`
			GeneratedAt string                    `json:"generated_at"`
			Items       []contracts.AttentionItem `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.True(t, body.Success)
	assert.Equal(t, len(body.Data.Items), body.Data.Count)
	assert.NotEmpty(t, body.Data.GeneratedAt)
	for _, it := range body.Data.Items {
		assert.NotEmpty(t, it.ID)
		assert.NotEmpty(t, it.Kind)
		assert.NotEmpty(t, it.Fix.Verb)
	}
}

// TestGetAttention_AgentTokenSeesOnlyAllowedServersNoClients pins T055: a
// scoped caller (agent token) sees only items whose server it may enumerate,
// and never a client item, with count recomputed from the narrowed list.
func TestGetAttention_AgentTokenSeesOnlyAllowedServersNoClients(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	ctrl.attentionItemsOverride = []contracts.AttentionItem{
		{ID: "sign_in_required:server:alpha", Kind: "sign_in_required", Subject: contracts.AttentionSubject{Type: "server", ID: "alpha", Name: "alpha"}, Fix: contracts.AttentionFix{Verb: "login"}},
		{ID: "server_error:server:beta", Kind: "server_error", Subject: contracts.AttentionSubject{Type: "server", ID: "beta", Name: "beta"}, Fix: contracts.AttentionFix{Verb: "restart"}},
		{ID: "client_never_seen:client:codex", Kind: "client_never_seen", Subject: contracts.AttentionSubject{Type: "client", ID: "codex", Name: "Codex"}, Fix: contracts.AttentionFix{Verb: "reload_hint"}},
	}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	admin := scopeGet(t, srv, "/api/v1/attention", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, admin.Code)
	require.Contains(t, admin.Body.String(), `"beta"`, "precondition: admin sees every item")
	require.Contains(t, admin.Body.String(), `"codex"`, "precondition: admin sees the client item")

	scoped := scopeGet(t, srv, "/api/v1/attention", token)
	require.Equal(t, http.StatusOK, scoped.Code, "body: %s", scoped.Body.String())

	var body struct {
		Data struct {
			Count int                       `json:"count"`
			Items []contracts.AttentionItem `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(scoped.Body.Bytes(), &body))
	require.Len(t, body.Data.Items, 1)
	assert.Equal(t, "alpha", body.Data.Items[0].Subject.ID)
	assert.Equal(t, 1, body.Data.Count)
	assert.NotContains(t, scoped.Body.String(), `"beta"`)
	assert.NotContains(t, scoped.Body.String(), `"codex"`)
}

// TestGetAttention_RegisteredInBothEditions is a structural check: the route
// is registered in the shared chi router server.go builds, which both the
// personal and server-edition binaries mount unconditionally (no build-tag
// gate on the route itself).
func TestGetAttention_RegisteredInBothEditions(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: nil, withManagement: true}
	srv, _ := scopedAgentServer(t, ctrl, nil)
	rec := scopeGet(t, srv, "/api/v1/attention", scopeAdminAPIKey)
	assert.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}
