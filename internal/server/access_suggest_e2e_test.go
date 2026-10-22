package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// `access suggest` (#1700) end to end: real refusals made by a pinned token are
// read back through the REST route with the real access explainer, and the
// proposed profile document, PUT as returned, clears the blocked tool.

func (f *restV3Fixture) suggest(query string) map[string]any {
	f.t.Helper()
	rec := f.do(http.MethodGet, "/api/v1/access/suggest"+query, restV3AdminKey, nil, "")
	require.Equal(f.t, http.StatusOK, rec.Code, rec.Body.String())
	var env struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(f.t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
	return env.Data
}

func suggestionFor(t *testing.T, data map[string]any, tool string, reason profile.BlockReason) map[string]any {
	t.Helper()
	rows, _ := data["suggestions"].([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if row["tool"] == tool && row["block_reason"] == string(reason) {
			return row
		}
	}
	return nil
}

func TestAccessSuggest_E2E_RealRefusalsRealExplainer(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.wireProfiles()
	indexEnforcementMatrixFixtureTools(t, f.proxy)
	ro := f.mint("ro-bot", restV3ReadonlyID)

	for i := 0; i < 2; i++ {
		rec := f.callTool(ro, "call_tool_write", map[string]any{"name": "github:create_issue", "args_json": "{}"}, "")
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	}
	require.Equal(t, http.StatusForbidden,
		f.callTool(ro, "call_tool_read", map[string]any{"name": "github:get_secret_scanning_alert", "args_json": "{}"}, "").Code)
	f.callTool(ro, "code_execution", map[string]any{"code": "1"}, "")
	f.waitBlocked("github", "create_issue", profile.BlockReasonTier)
	f.waitBlocked("github", "get_secret_scanning_alert", profile.BlockReasonRule)
	f.waitBlocked("", "code_execution", profile.BlockReasonCodeExecution)
	f.settle()

	data := f.suggest("?patch=true")

	tier := suggestionFor(t, data, "github:create_issue", profile.BlockReasonTier)
	require.NotNil(t, tier, "%v", data)
	assert.Equal(t, "allow", tier["suggestion"])
	assert.Equal(t, restV3ReadonlyID, tier["profile"])
	assert.EqualValues(t, 3, tier["count"], "two direct refusals and the settle marker")
	assert.Contains(t, tier["tokens"], "ro-bot")

	rule := suggestionFor(t, data, "github:get_secret_scanning_alert", profile.BlockReasonRule)
	require.NotNil(t, rule, "%v", data)
	assert.Equal(t, "none", rule["suggestion"], "a deny rule is never offered for widening")
	assert.Empty(t, rule["fixes"])

	builtin := suggestionFor(t, data, "code_execution", profile.BlockReasonCodeExecution)
	require.NotNil(t, builtin, "%v", data)
	assert.Contains(t, builtin["warning"], "code_execution")

	// The patch is the whole stored profile with the allow rule added.
	patches, _ := data["patches"].([]any)
	require.Len(t, patches, 1, "%v", data)
	patch, _ := patches[0].(map[string]any)
	assert.Equal(t, restV3ReadonlyID, patch["profile"])
	assert.Contains(t, patch["changes"], "tools.allow += github:create_issue")
	proposed, _ := patch["proposed"].(map[string]any)
	assert.Equal(t, restV3ReadonlyID, proposed["name"])

	// Apply it exactly as returned: the document PUT /profiles/{name} takes.
	put := f.do(http.MethodPut, "/api/v1/profiles/"+restV3ReadonlyID, restV3AdminKey, proposed, "")
	require.Equal(t, http.StatusOK, put.Code, put.Body.String())

	// The tool is reachable now, so it drops out; the deny rule stays reported.
	after := f.suggest("")
	assert.Nil(t, suggestionFor(t, after, "github:create_issue", profile.BlockReasonTier), "%v", after)
	assert.EqualValues(t, 1, after["already_fixed"])
	assert.NotNil(t, suggestionFor(t, after, "github:get_secret_scanning_alert", profile.BlockReasonRule))
}

func TestAccessSuggest_E2E_FiltersAndAdminGate(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.wireProfiles()
	indexEnforcementMatrixFixtureTools(t, f.proxy)
	ro := f.mint("ro-bot", restV3ReadonlyID)
	require.Equal(t, http.StatusForbidden,
		f.callTool(ro, "call_tool_write", map[string]any{"name": "github:create_issue", "args_json": "{}"}, "").Code)
	f.waitBlocked("github", "create_issue", profile.BlockReasonTier)

	assert.NotNil(t, suggestionFor(t, f.suggest("?token=ro-bot"), "github:create_issue", profile.BlockReasonTier))
	assert.Nil(t, suggestionFor(t, f.suggest("?token=someone-else"), "github:create_issue", profile.BlockReasonTier))
	assert.Nil(t, suggestionFor(t, f.suggest("?profile=work-full"), "github:create_issue", profile.BlockReasonTier))

	// The refused token itself may not read the suggestions.
	assert.Equal(t, http.StatusForbidden, f.do(http.MethodGet, "/api/v1/access/suggest", ro, nil, "").Code)
}
