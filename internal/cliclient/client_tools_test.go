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
			"data": map[string]interface{}{
				"server":  "oauth-server",
				"action":  "login",
				"success": true,
			},
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

func TestClient_TriggerOAuthLoginWithResult_DecodesBrowserStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/servers/oauth-server/login", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		// Mirrors handleServerLogin: contracts.OAuthStartResponse wrapped in the success envelope.
		response := map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"success":        true,
				"server_name":    "oauth-server",
				"correlation_id": "corr-123",
				"auth_url":       "https://auth.example.com/authorize?state=abc",
				"browser_opened": false,
				"browser_error":  "HEADLESS mode - browser not opened. Please open the auth_url manually.",
				"message":        "Could not open browser automatically. Please open this URL manually: https://auth.example.com/authorize?state=abc",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, zap.NewNop().Sugar())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.TriggerOAuthLoginWithResult(ctx, "oauth-server")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "oauth-server", result.ServerName)
	assert.Equal(t, "corr-123", result.CorrelationID)
	assert.Equal(t, "https://auth.example.com/authorize?state=abc", result.AuthURL)
	assert.False(t, result.BrowserOpened)
	assert.Equal(t, "HEADLESS mode - browser not opened. Please open the auth_url manually.", result.BrowserError)
	assert.Contains(t, result.Message, "Please open this URL manually")
}

func TestClient_TriggerOAuthLoginWithResult_BrowserOpened(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"success":        true,
				"server_name":    "oauth-server",
				"auth_url":       "https://auth.example.com/authorize?state=abc",
				"browser_opened": true,
				"message":        "OAuth authentication started",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, zap.NewNop().Sugar())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.TriggerOAuthLoginWithResult(ctx, "oauth-server")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.BrowserOpened)
	assert.Equal(t, "https://auth.example.com/authorize?state=abc", result.AuthURL)
	assert.Empty(t, result.BrowserError)
}

func TestClient_TriggerOAuthLoginWithResult_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := map[string]interface{}{
			"success":    false,
			"error":      "Server does not have OAuth configured",
			"request_id": "req-42",
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, zap.NewNop().Sugar())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.TriggerOAuthLoginWithResult(ctx, "oauth-server")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "does not have OAuth configured")
}
