package cliclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_ListRegistries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/registries", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"registries": []map[string]interface{}{
					{"id": "pulse", "name": "Pulse"},
					{"id": "smithery", "name": "Smithery"},
				},
			},
		})
	}))
	defer server.Close()

	client := cliclient.NewClient(server.URL, nil)
	regs, err := client.ListRegistries(context.Background())
	require.NoError(t, err)
	require.Len(t, regs, 2)
	assert.Equal(t, "pulse", regs[0]["id"])
}

func TestClient_SearchRegistry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/registries/pulse/servers", r.URL.Path)
		assert.Equal(t, "weather", r.URL.Query().Get("q"))
		assert.Equal(t, "5", r.URL.Query().Get("limit"))
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"servers": []map[string]interface{}{
					{"id": "weather-mcp", "name": "weather-mcp", "installCmd": "npx weather-mcp"},
				},
			},
		})
	}))
	defer server.Close()

	client := cliclient.NewClient(server.URL, nil)
	servers, err := client.SearchRegistry(context.Background(), "pulse", "", "weather", 5)
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, "weather-mcp", servers[0]["id"])
}

func TestClient_AddFromRegistry_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/registries/pulse/servers/weather-mcp/add", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"server": map[string]interface{}{
					"name":        "weather",
					"protocol":    "stdio",
					"command":     "npx",
					"args":        []string{"weather-mcp"},
					"quarantined": true,
					"enabled":     true,
				},
			},
		})
	}))
	defer server.Close()

	client := cliclient.NewClient(server.URL, nil)
	got, err := client.AddFromRegistry(context.Background(), "pulse", "weather-mcp", "weather", nil, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "weather", got.Name)
	assert.Equal(t, "stdio", got.Protocol)
	assert.True(t, got.Quarantined)
}

func TestClient_AddFromRegistry_MissingRequiredInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success":        false,
			"error":          "missing_required_input: GITHUB_TOKEN",
			"code":           "missing_required_input",
			"missing_inputs": []string{"GITHUB_TOKEN"},
			"request_id":     "req-123",
		})
	}))
	defer server.Close()

	client := cliclient.NewClient(server.URL, nil)
	_, err := client.AddFromRegistry(context.Background(), "pulse", "gh", "", nil, nil)
	require.Error(t, err)

	var addErr *cliclient.RegistryAddError
	require.True(t, errors.As(err, &addErr), "should be a *RegistryAddError")
	assert.Equal(t, "missing_required_input", addErr.Code)
	assert.Equal(t, []string{"GITHUB_TOKEN"}, addErr.MissingInputs)
	assert.Equal(t, "req-123", addErr.RequestID)
}
