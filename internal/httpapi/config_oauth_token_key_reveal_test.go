package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// Spec 123: a literal oauth_token_encryption_key must stay masked on
// GET /api/v1/config even when an admin turned reveal_secret_headers on.
func TestGetConfig_RevealFlagNeverUnmasksOAuthTokenKey(t *testing.T) {
	const literal = "q83vEjRWeJCrze8SNFZ4kKvN7xI0VniQq83vEjRWeJA="
	cfg := scopeFixtureConfig(true)
	cfg.OAuthTokenEncryptionKey = literal
	ctrl := &scopeController{cfg: cfg, servers: scopeFixtureServers(), withManagement: true}
	srv := NewServer(ctrl, zap.NewNop().Sugar(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", http.NoBody)
	req = req.WithContext(auth.WithAuthContext(req.Context(), auth.AdminContext()))
	rec := httptest.NewRecorder()
	srv.handleGetConfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), alphaHeaderSecret, "precondition: reveal really is on")
	assert.NotContains(t, rec.Body.String(), literal)
	assert.NotContains(t, rec.Body.String(), literal[4:40])
	assert.Contains(t, rec.Body.String(), "oauth_token_encryption_key", "present, masked")
	assert.Equal(t, literal, cfg.OAuthTokenEncryptionKey, "the live config is not mutated")
}
