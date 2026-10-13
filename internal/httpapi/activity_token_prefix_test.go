package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func prefixedActivity(id string, typ storage.ActivityType, prefix, name string) *storage.ActivityRecord {
	return &storage.ActivityRecord{
		ID: id, Type: typ, ServerName: "alpha", ToolName: "create_issue", Status: "blocked",
		Timestamp: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Profile:   "ro", ProfileSource: "binding", ClientID: "cursor", TokenName: name, TokenPrefix: prefix,
	}
}

// policy_decision and prompt_get rows have no _auth_token_prefix argument; the
// persisted TokenPrefix is what lets a scoped caller recognise (and keep the
// attribution of) its own rows.
func TestActivity_ScopedCallerFindsOwnPolicyDecision(t *testing.T) {
	own1 := prefixedActivity("own-policy", storage.ActivityTypePolicyDecision, "", "scoped-ci")
	own2 := prefixedActivity("own-prompt", storage.ActivityTypePromptGet, "", "scoped-ci")
	foreign := prefixedActivity("foreign-policy", storage.ActivityTypePolicyDecision, "mcp_agt_zzzz", "other-bot")

	ctrl := &mockActivityController{apiKey: "test-key", activities: []*storage.ActivityRecord{own1, own2, foreign}}
	srv, token := scopedAgentServer(t, ctrl, []string{"*"})
	own1.TokenPrefix = auth.TokenPrefix(token)
	own2.TokenPrefix = auth.TokenPrefix(token)

	data := scopeDecodeData(t, scopeGet(t, srv, "/api/v1/activity?token=scoped-ci", token))
	assert.EqualValues(t, 2, data["total"])
	rows := data["activities"].([]interface{})
	require.Len(t, rows, 2)
	for _, r := range rows {
		a := r.(map[string]interface{})
		assert.Contains(t, []string{"own-policy", "own-prompt"}, a["id"])
		assert.Equal(t, "ro", a["profile"], "an own row keeps its attribution")
		assert.Equal(t, "cursor", a["client_id"])
		assert.Equal(t, "scoped-ci", a["token_name"])
	}

	data = scopeDecodeData(t, scopeGet(t, srv, "/api/v1/activity?token=-", token))
	for _, r := range data["activities"].([]interface{}) {
		id := r.(map[string]interface{})["id"]
		assert.NotContains(t, []string{"own-policy", "own-prompt"}, id, "own rows are attributed, so token=- must not return them")
	}

	// the foreign row stays redacted
	data = scopeDecodeData(t, scopeGet(t, srv, "/api/v1/activity", token))
	for _, r := range data["activities"].([]interface{}) {
		a := r.(map[string]interface{})
		if a["id"] == "foreign-policy" {
			assert.NotContains(t, a, "token_name")
			assert.NotContains(t, a, "client_id")
			assert.NotContains(t, a, "profile")
		}
	}
}

// The prefix is an internal ownership proof: no REST shape, export or CSV
// carries it, even to an administrator.
func TestActivityContract_NeverCarriesTokenPrefix(t *testing.T) {
	const secretPrefix = "mcp_agt_secret"
	rec := prefixedActivity("p1", storage.ActivityTypePolicyDecision, secretPrefix, "scoped-ci")

	for name, projected := range map[string]interface{}{
		"list":   storageToContractActivity(rec),
		"export": storageToContractActivityForExport(rec, true),
	} {
		raw, err := json.Marshal(projected)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), secretPrefix, name)
		assert.NotContains(t, string(raw), "token_prefix", name)
	}

	srv := NewServer(&mockActivityController{apiKey: "test-key", activities: []*storage.ActivityRecord{rec}}, zap.NewNop().Sugar(), nil)
	for _, path := range []string{
		"/api/v1/activity/export?format=csv",
		"/api/v1/activity/export?format=json&include_bodies=true",
		"/api/v1/activity/p1",
		"/api/v1/activity",
	} {
		out := scopeGet(t, srv, path, "test-key")
		require.Equal(t, http.StatusOK, out.Code, path)
		assert.NotContains(t, out.Body.String(), secretPrefix, path)
		assert.NotContains(t, out.Body.String(), "token_prefix", path)
	}
}
