package cliclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestClient_BlockTools_Specific verifies BlockTools posts the tool list to the
// block endpoint and returns the "blocked" count from the API envelope.
func TestClient_BlockTools_Specific(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/servers/github/tools/block", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)

		body, _ := io.ReadAll(r.Body)
		var req struct {
			Tools    []string `json:"tools"`
			BlockAll bool     `json:"block_all"`
		}
		require.NoError(t, json.Unmarshal(body, &req))
		assert.Equal(t, []string{"create_issue", "delete_repo"}, req.Tools)
		assert.False(t, req.BlockAll)

		response := map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"blocked": 2,
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

	count, err := client.BlockTools(ctx, "github", []string{"create_issue", "delete_repo"}, false)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// TestClient_BlockTools_All verifies BlockTools sends block_all=true (and no
// tools array) when blockAll is set.
func TestClient_BlockTools_All(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/servers/github/tools/block", r.URL.Path)

		body, _ := io.ReadAll(r.Body)
		var req struct {
			Tools    []string `json:"tools"`
			BlockAll bool     `json:"block_all"`
		}
		require.NoError(t, json.Unmarshal(body, &req))
		assert.True(t, req.BlockAll)
		assert.Empty(t, req.Tools)

		response := map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"blocked": 5,
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

	count, err := client.BlockTools(ctx, "github", nil, true)
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

// TestClient_BlockTools_APIError verifies a non-200 response surfaces an error.
func TestClient_BlockTools_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := map[string]interface{}{
			"success": false,
			"error":   "Server not found",
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, zap.NewNop().Sugar())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.BlockTools(ctx, "nonexistent", []string{"foo"}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Server not found")
}
