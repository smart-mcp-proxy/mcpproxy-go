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

// Regression for MCP-1111 / #598: the daemon logs client must percent-encode a
// namespaced (slash-bearing) official-registry server name so the request matches
// the chi /servers/{id}/logs route instead of injecting extra path segments.
func TestClient_GetServerLogs_EscapesSlashName(t *testing.T) {
	const serverName = "io.github.evidai/polymarket-guard"
	var gotEscaped string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscaped = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"logs": []interface{}{}},
		})
	}))
	defer ts.Close()

	client := NewClient(ts.URL, zap.NewNop().Sugar())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.GetServerLogs(ctx, serverName, 50)
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/servers/io.github.evidai%2Fpolymarket-guard/logs", gotEscaped,
		"server name must be percent-encoded in the daemon logs URL")
}
