package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 123: POST /api/v1/servers/{id}/login for a server whose stored OAuth
// row cannot be decrypted answers with the stable diagnostic code and a
// conflict status, not an opaque 500.
func TestHandleServerLogin_UndecryptableTokenRow(t *testing.T) {
	coded := fmt.Errorf("failed to start OAuth: %w",
		oauth.TokenUndecryptableError("sealed", storage.ErrOAuthTokenUndecryptableSentinel))
	ctrl := &mockLoginController{apiKey: "test-key", mgmtSvc: &mockOAuthManagementService{triggerError: coded}}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/sealed/login", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "MCPX_OAUTH_TOKEN_UNDECRYPTABLE")
	assert.Contains(t, resp.Error, "auth logout --server=sealed")
}
