package cliclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestClient_GetServerTools(t *testing.T) {
	// Create mock server that returns tools
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/servers/test-server/tools", r.URL.Path)
		assert.Equal(t, "GET", r.Method)

		response := map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"server_name": "test-server",
				"tools": []map[string]interface{}{
					{
						"name":        "test_tool",
						"description": "A test tool",
						"server_name": "test-server",
					},
				},
				"count": 1,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	// Create client
	logger := zap.NewNop().Sugar()
	client := NewClient(ts.URL, logger)

	// Call GetServerTools
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tools, err := client.GetServerTools(ctx, "test-server")
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "test_tool", tools[0]["name"])
	assert.Equal(t, "A test tool", tools[0]["description"])
}

func TestClient_GetServerTools_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"success": false,
			"error":   "Server not found",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	logger := zap.NewNop().Sugar()
	client := NewClient(ts.URL, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.GetServerTools(ctx, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Server not found")
}

func TestClient_TriggerOAuthLogin(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/servers/oauth-server/login", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		response := map[string]interface{}{
			"success": true,
			"message": "OAuth login initiated",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	logger := zap.NewNop().Sugar()
	client := NewClient(ts.URL, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.TriggerOAuthLogin(ctx, "oauth-server")
	require.NoError(t, err)
}

func TestClient_TriggerOAuthLogin_NotConfigured(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"success": false,
			"error":   "Server does not have OAuth configured",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	logger := zap.NewNop().Sugar()
	client := NewClient(ts.URL, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.TriggerOAuthLogin(ctx, "non-oauth-server")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not have OAuth configured")
}
