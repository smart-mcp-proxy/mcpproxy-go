package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// TestGetAttention_Shape pins T055: GET /attention returns {count,
// generated_at, items[]} with the exact item shape contracts/rest-api.md
// documents, for an administrator.
func TestGetAttention_Shape(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	ctrl.attentionItemsOverride = []contracts.AttentionItem{{
		ID:      "sign_in_required:server:alpha",
		Kind:    "sign_in_required",
		Rank:    10,
		Subject: contracts.AttentionSubject{Type: "server", ID: "alpha", Name: "alpha"},
		Summary: "alpha: sign in required",
		Detail:  "OAuth · alpha.example.com",
		Fix:     contracts.AttentionFix{Verb: "login", Label: "Sign in", Target: "/servers/alpha"},
		Since:   time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	}}
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
	require.NotEmpty(t, body.Data.Items, "shape assertions must exercise at least one attention item")
	assert.Equal(t, len(body.Data.Items), body.Data.Count)
	assert.NotEmpty(t, body.Data.GeneratedAt)
	item := body.Data.Items[0]
	assert.Equal(t, "sign_in_required:server:alpha", item.ID)
	assert.Equal(t, "sign_in_required", item.Kind)
	assert.Equal(t, 10, item.Rank)
	assert.Equal(t, contracts.AttentionSubject{Type: "server", ID: "alpha", Name: "alpha"}, item.Subject)
	assert.Equal(t, "alpha: sign in required", item.Summary)
	assert.Equal(t, "OAuth · alpha.example.com", item.Detail)
	assert.Equal(t, contracts.AttentionFix{Verb: "login", Label: "Sign in", Target: "/servers/alpha"}, item.Fix)
	assert.Equal(t, time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC), item.Since)
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
