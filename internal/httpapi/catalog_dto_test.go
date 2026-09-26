package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// TestCatalogSearch_DTOShape is T101a: GET /catalog/search on a fixture
// registry marshals exactly the contracts/rest-api.md#catalog example shape —
// transport, install{url|command,args}, required_inputs[].secret_like, and
// none of ServerEntry's own keys (url, installCmd, registry,
// required_inputs[].secret).
func TestCatalogSearch_DTOShape(t *testing.T) {
	body := `[{
		"id": "io.github.github/github-mcp-server",
		"name": "GitHub",
		"description": "…",
		"url": "https://api.githubcopilot.com/mcp/",
		"required_inputs": [{"name": "GITHUB_TOKEN"}]
	}]`
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer src.Close()

	t.Cleanup(registries.AllowPrivateRegistryFetchForTest())
	t.Cleanup(registries.SetRegistriesForTest([]registries.RegistryEntry{
		{ID: "official", Name: "Official", ServersURL: src.URL, Provenance: "official"},
	}))

	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: nil, withManagement: true}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/search?q=github", nil)
	req.Header.Set("X-API-Key", scopeAdminAPIKey)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var envelope struct {
		Data struct {
			Results []map[string]interface{} `json:"results"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Results, 1)
	result := envelope.Data.Results[0]

	require.Equal(t, "official", result["source"])
	require.Equal(t, "io.github.github/github-mcp-server", result["id"])
	require.Equal(t, "GitHub", result["title"])
	require.Equal(t, true, result["official"])
	require.Equal(t, "http", result["transport"])

	install, ok := result["install"].(map[string]interface{})
	require.True(t, ok, "install must be an object: %#v", result)
	require.Equal(t, "https://api.githubcopilot.com/mcp/", install["url"])
	require.NotContains(t, install, "command")
	require.NotContains(t, install, "args")

	inputs, ok := result["required_inputs"].([]interface{})
	require.True(t, ok, "required_inputs must be an array: %#v", result)
	require.Len(t, inputs, 1)
	input, ok := inputs[0].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "GITHUB_TOKEN", input["name"])
	require.Equal(t, true, input["secret_like"])
	require.NotContains(t, input, "secret", "CatalogInput must never reuse ServerEntry's own 'secret' key")

	require.NotContains(t, result, "url", "CatalogResult must not carry ServerEntry's own 'url' key")
	require.NotContains(t, result, "installCmd")
	require.NotContains(t, result, "registry")
	require.Equal(t, false, result["added"])
}

// TestServerEntryJSON_UnchangedByCatalog pins that registries.ServerEntry's
// own JSON shape (url, installCmd, registry, required_inputs[].secret) is
// untouched by the catalog DTO work — GET /registries/{id}/servers and MCP
// search_servers keep serving it as-is (codex round 3, contracts/rest-api.md#catalog).
func TestServerEntryJSON_UnchangedByCatalog(t *testing.T) {
	entry := registries.ServerEntry{
		ID:         "fs",
		Name:       "Filesystem",
		URL:        "https://example.com/mcp",
		InstallCmd: "npx -y server-filesystem",
		Registry:   "Official",
		RequiredInputs: []registries.RequiredInput{
			{Name: "GITHUB_TOKEN", Secret: true},
		},
	}
	raw, err := json.Marshal(entry)
	require.NoError(t, err)

	var generic map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &generic))

	require.Equal(t, "https://example.com/mcp", generic["url"])
	require.Equal(t, "npx -y server-filesystem", generic["installCmd"])
	require.Equal(t, "Official", generic["registry"])

	inputs, ok := generic["required_inputs"].([]interface{})
	require.True(t, ok)
	require.Len(t, inputs, 1)
	input, ok := inputs[0].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, true, input["secret"])
	require.NotContains(t, input, "secret_like", "ServerEntry's RequiredInput must never gain the catalog-only secret_like key")
}
