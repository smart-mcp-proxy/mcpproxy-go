package oauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func setupTestStorage(t *testing.T) *storage.BoltDB {
	t.Helper()
	logger := zap.NewNop().Sugar()
	// NewBoltDB expects a directory, not a file path
	db, err := storage.NewBoltDB(t.TempDir(), logger)
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
	})
	return db
}

func TestCreateOAuthConfig_ExtractsResourceParameter(t *testing.T) {
	// Setup mock metadata server
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ProtectedResourceMetadata{
			Resource:        "https://mcp.example.com/api",
			ScopesSupported: []string{"mcp.read"},
		})
	}))
	defer metadataServer.Close()

	// Setup mock MCP server that returns WWW-Authenticate
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer resource_metadata=\"%s\"", metadataServer.URL))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpServer.Close()

	storage := setupTestStorage(t)
	serverConfig := &config.ServerConfig{
		Name: "test-server",
		URL:  mcpServer.URL,
	}

	oauthConfig, extraParams := CreateOAuthConfig(serverConfig, storage)

	require.NotNil(t, oauthConfig)
	require.NotNil(t, extraParams)
	assert.Equal(t, "https://mcp.example.com/api", extraParams["resource"])
}
