package cliclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_CodeExec_Success(t *testing.T) {
	// Given: Mock server returning success
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/code/exec", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)

		response := map[string]interface{}{
			"ok":     true,
			"result": map[string]interface{}{"value": 42},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := cliclient.NewClient(server.URL, nil)

	// When: Executing code
	result, err := client.CodeExec(context.Background(), "code", map[string]interface{}{}, 60000, 0, nil)

	// Then: Returns result
	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, 42.0, result.Result.(map[string]interface{})["value"])
}

func TestClient_CodeExec_ExecutionError(t *testing.T) {
	// Given: Mock server returning execution error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"ok": false,
			"error": map[string]interface{}{
				"code":    "SYNTAX_ERROR",
				"message": "Invalid syntax",
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := cliclient.NewClient(server.URL, nil)

	// When: Executing code
	result, err := client.CodeExec(context.Background(), "invalid", map[string]interface{}{}, 60000, 0, nil)

	// Then: Returns error in result
	require.NoError(t, err)
	assert.False(t, result.OK)
	assert.Equal(t, "SYNTAX_ERROR", result.Error.Code)
}

func TestClient_CodeExec_NetworkError(t *testing.T) {
	// Given: Client with invalid endpoint
	client := cliclient.NewClient("http://invalid-endpoint-12345.local", nil)

	// When: Executing code
	_, err := client.CodeExec(context.Background(), "code", map[string]interface{}{}, 60000, 0, nil)

	// Then: Returns network error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to call code execution API")
}

func TestClient_Ping_Success(t *testing.T) {
	// Given: Mock server responding to status endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/status", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
	}))
	defer server.Close()

	client := cliclient.NewClient(server.URL, nil)

	// When: Pinging daemon
	err := client.Ping(context.Background())

	// Then: Returns no error
	require.NoError(t, err)
}

func TestClient_Ping_Failure(t *testing.T) {
	// Given: Mock server returning error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := cliclient.NewClient(server.URL, nil)

	// When: Pinging daemon
	err := client.Ping(context.Background())

	// Then: Returns error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "daemon returned status")
}
