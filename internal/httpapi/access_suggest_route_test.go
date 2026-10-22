package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// GET /access/suggest (#1700): the administrator gate, the query rules and the
// wire shape.

func TestAccessSuggestRoute_ScopedCallerIs403(t *testing.T) {
	g := newProfilesRig(t)
	rec := g.do(g.token, http.MethodGet, "/api/v1/access/suggest", "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, g.fake.calls)
}

func TestAccessSuggestRoute_QueryReachesTheService(t *testing.T) {
	g := newProfilesRig(t)

	require.Equal(t, http.StatusOK, g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/access/suggest", "").Code)
	assert.Equal(t, []string{"suggest:24h0m0s:::false"}, g.fake.calls, "the default window is 24h")

	g.fake.calls = nil
	require.Equal(t, http.StatusOK, g.do(scopeAdminAPIKey, http.MethodGet,
		"/api/v1/access/suggest?since=7d&profile=research&token=ci-bot&patch=true", "").Code)
	assert.Equal(t, []string{"suggest:168h0m0s:research:ci-bot:true"}, g.fake.calls)
}

func TestAccessSuggestRoute_InvalidSinceIs400(t *testing.T) {
	g := newProfilesRig(t)
	for _, since := range []string{"abc", "0", "-1h", "1.5d"} {
		rec := g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/access/suggest?since="+since, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, since)
		assert.Contains(t, rec.Body.String(), "invalid since")
	}
	assert.Empty(t, g.fake.calls, "a malformed request never reaches the service")
}

func TestAccessSuggestRoute_UnwiredEvaluatorIs503(t *testing.T) {
	g := newProfilesRig(t)
	g.fake.err = internalRuntime.ErrEvaluatorUnavailable
	assert.Equal(t, http.StatusServiceUnavailable, g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/access/suggest", "").Code)
}

func TestAccessSuggestRoute_ResponseShape(t *testing.T) {
	g := newProfilesRig(t)
	seen := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	g.fake.suggest = &internalRuntime.AccessSuggestions{
		Window: "24h0m0s", BlockedCalls: 3,
		Suggestions: []internalRuntime.AccessSuggestion{{
			Profile: "research", Tool: "github:create_issue", Server: "github", BlockReason: profile.BlockReasonTier,
			Count: 3, Tokens: []string{"ci-bot"}, Clients: []string{}, FirstSeen: seen, LastSeen: seen,
			Suggestion: internalRuntime.SuggestAllow, Fixes: []internalRuntime.Fix{},
		}},
	}
	rec := g.do(scopeAdminAPIKey, http.MethodGet, "/api/v1/access/suggest", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data := decodeProfilesData(t, rec)
	for _, k := range []string{"since", "until", "window", "suggestions", "blocked_calls", "already_fixed", "profile_removed", "non_profile_blocked", "unattributed"} {
		assert.Contains(t, data, k)
	}
	assert.NotContains(t, data, "patches", "patches are present only when asked for")

	rows, _ := data["suggestions"].([]interface{})
	require.Len(t, rows, 1)
	row, _ := rows[0].(map[string]interface{})
	raw, _ := json.Marshal(row)
	for _, k := range []string{"profile", "tool", "block_reason", "count", "tokens", "clients", "first_seen", "last_seen", "suggestion", "fixes"} {
		assert.Contains(t, row, k, string(raw))
	}
	assert.Equal(t, "profile_tier", row["block_reason"])
	assert.Equal(t, "allow", row["suggestion"])
}
