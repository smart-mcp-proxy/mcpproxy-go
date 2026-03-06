package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// --- Test helpers ---

// testTokenStore is a mock TokenStore for testing agent token auth.
type testTokenStore struct {
	validateFunc func(rawToken string, hmacKey []byte) (*auth.AgentToken, error)
}

func (s *testTokenStore) CreateAgentToken(_ auth.AgentToken, _ string, _ []byte) error   { return nil }
func (s *testTokenStore) ListAgentTokens() ([]auth.AgentToken, error)                    { return nil, nil }
func (s *testTokenStore) GetAgentTokenByName(_ string) (*auth.AgentToken, error)          { return nil, nil }
func (s *testTokenStore) RevokeAgentToken(_ string) error                                 { return nil }
func (s *testTokenStore) RegenerateAgentToken(_ string, _ string, _ []byte) (*auth.AgentToken, error) {
	return nil, nil
}
func (s *testTokenStore) UpdateAgentTokenLastUsed(_ string) error { return nil }

func (s *testTokenStore) ValidateAgentToken(rawToken string, hmacKey []byte) (*auth.AgentToken, error) {
	if s.validateFunc != nil {
		return s.validateFunc(rawToken, hmacKey)
	}
	return nil, fmt.Errorf("token not found")
}

// testControllerWithConfig returns a mock controller with a specific config.
type testControllerWithConfig struct {
	baseController
	cfg *config.Config
}

func (m *testControllerWithConfig) GetCurrentConfig() interface{} {
	return m.cfg
}

// --- Tests ---

func TestExtractToken_XAPIKey(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("X-API-Key", "my-api-key")
	assert.Equal(t, "my-api-key", ExtractToken(req))
}

func TestExtractToken_BearerHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer mcp_agt_abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab")
	assert.Equal(t, "mcp_agt_abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab", ExtractToken(req))
}

func TestExtractToken_QueryParam(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/status?apikey=query-key", nil)
	assert.Equal(t, "query-key", ExtractToken(req))
}

func TestExtractToken_Priority(t *testing.T) {
	// X-API-Key header takes priority over Bearer and query param
	req := httptest.NewRequest("GET", "/api/v1/status?apikey=query-key", nil)
	req.Header.Set("X-API-Key", "header-key")
	req.Header.Set("Authorization", "Bearer bearer-key")
	assert.Equal(t, "header-key", ExtractToken(req))
}

func TestExtractToken_BearerPriorityOverQuery(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/status?apikey=query-key", nil)
	req.Header.Set("Authorization", "Bearer bearer-key")
	assert.Equal(t, "bearer-key", ExtractToken(req))
}

func TestExtractToken_Empty(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	assert.Equal(t, "", ExtractToken(req))
}

func TestExtractToken_EmptyBearer(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer ")
	assert.Equal(t, "", ExtractToken(req))
}

func TestAPIKeyAuth_AgentToken_Valid(t *testing.T) {
	logger := zap.NewNop().Sugar()

	// Create a temp data dir for HMAC key
	tmpDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	// Generate a token and hash it
	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)

	agentToken := &auth.AgentToken{
		Name:           "test-bot",
		TokenPrefix:    auth.TokenPrefix(rawToken),
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		CreatedAt:      time.Now(),
	}

	store := &testTokenStore{
		validateFunc: func(token string, key []byte) (*auth.AgentToken, error) {
			if token == rawToken {
				return agentToken, nil
			}
			return nil, fmt.Errorf("token not found")
		},
	}

	cfg := &config.Config{
		APIKey: "admin-key-12345",
	}
	mockCtrl := &testControllerWithConfig{cfg: cfg}

	srv := NewServer(mockCtrl, logger, nil)
	srv.SetTokenStore(store, tmpDir)

	// Make request with agent token via X-API-Key
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("X-API-Key", rawToken)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	// Should be accepted (200 OK from status endpoint)
	assert.Equal(t, http.StatusOK, w.Code, "Valid agent token should be accepted")
}

func TestAPIKeyAuth_AgentToken_ViaBearer(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tmpDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)

	agentToken := &auth.AgentToken{
		Name:           "bearer-bot",
		TokenPrefix:    auth.TokenPrefix(rawToken),
		AllowedServers: []string{"*"},
		Permissions:    []string{auth.PermRead},
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		CreatedAt:      time.Now(),
	}

	store := &testTokenStore{
		validateFunc: func(token string, key []byte) (*auth.AgentToken, error) {
			if token == rawToken {
				return agentToken, nil
			}
			return nil, fmt.Errorf("token not found")
		},
	}

	cfg := &config.Config{APIKey: "admin-key-12345"}
	mockCtrl := &testControllerWithConfig{cfg: cfg}

	srv := NewServer(mockCtrl, logger, nil)
	srv.SetTokenStore(store, tmpDir)

	// Make request with agent token via Authorization: Bearer
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "Agent token via Bearer header should be accepted")
}

func TestAPIKeyAuth_AgentToken_Expired(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tmpDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)

	store := &testTokenStore{
		validateFunc: func(token string, key []byte) (*auth.AgentToken, error) {
			if token == rawToken {
				return nil, fmt.Errorf("token has expired")
			}
			return nil, fmt.Errorf("token not found")
		},
	}

	cfg := &config.Config{APIKey: "admin-key-12345"}
	mockCtrl := &testControllerWithConfig{cfg: cfg}

	srv := NewServer(mockCtrl, logger, nil)
	srv.SetTokenStore(store, tmpDir)

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("X-API-Key", rawToken)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code, "Expired agent token should be rejected")
}

func TestAPIKeyAuth_AgentToken_Revoked(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tmpDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)

	store := &testTokenStore{
		validateFunc: func(token string, key []byte) (*auth.AgentToken, error) {
			if token == rawToken {
				return nil, fmt.Errorf("token has been revoked")
			}
			return nil, fmt.Errorf("token not found")
		},
	}

	cfg := &config.Config{APIKey: "admin-key-12345"}
	mockCtrl := &testControllerWithConfig{cfg: cfg}

	srv := NewServer(mockCtrl, logger, nil)
	srv.SetTokenStore(store, tmpDir)

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("X-API-Key", rawToken)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code, "Revoked agent token should be rejected")

	var errResp map[string]interface{}
	err = json.NewDecoder(w.Body).Decode(&errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp["error"], "revoked")
}

func TestAPIKeyAuth_GlobalKey_SetsAdminContext(t *testing.T) {
	logger := zap.NewNop().Sugar()
	apiKey := "my-admin-key"

	cfg := &config.Config{APIKey: apiKey}
	mockCtrl := &testControllerWithConfig{cfg: cfg}

	srv := NewServer(mockCtrl, logger, nil)

	// Use a custom handler that checks the auth context
	var capturedCtx *auth.AuthContext

	// We can't easily capture context from the handler since setupRoutes is internal.
	// Instead, test that the request succeeds (admin gets through).
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "Admin API key should be accepted")

	// Verify the context is set correctly by the middleware — test directly
	middleware := srv.apiKeyAuthMiddleware()
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCtx = auth.AuthContextFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", apiKey)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.NotNil(t, capturedCtx, "AuthContext should be set")
	assert.True(t, capturedCtx.IsAdmin(), "Should be admin context")
}

func TestAPIKeyAuth_TrayConnection_SetsAdminContext(t *testing.T) {
	logger := zap.NewNop().Sugar()
	cfg := &config.Config{APIKey: "some-key"}
	mockCtrl := &testControllerWithConfig{cfg: cfg}

	srv := NewServer(mockCtrl, logger, nil)

	var capturedCtx *auth.AuthContext

	middleware := srv.apiKeyAuthMiddleware()
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCtx = auth.AuthContextFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	ctx := transport.TagConnectionContext(req.Context(), transport.ConnectionSourceTray)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, capturedCtx, "AuthContext should be set for tray connections")
	assert.True(t, capturedCtx.IsAdmin(), "Tray connections should get admin context")
}

func TestAPIKeyAuth_AgentToken_SetsAgentContext(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tmpDir := t.TempDir()
	_, err := auth.GetOrCreateHMACKey(tmpDir)
	require.NoError(t, err)

	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)

	agentToken := &auth.AgentToken{
		Name:           "test-agent",
		TokenPrefix:    auth.TokenPrefix(rawToken),
		AllowedServers: []string{"github", "gitlab"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	}

	store := &testTokenStore{
		validateFunc: func(token string, key []byte) (*auth.AgentToken, error) {
			if token == rawToken {
				return agentToken, nil
			}
			return nil, fmt.Errorf("token not found")
		},
	}

	cfg := &config.Config{APIKey: "admin-key"}
	mockCtrl := &testControllerWithConfig{cfg: cfg}

	srv := NewServer(mockCtrl, logger, nil)
	srv.SetTokenStore(store, tmpDir)

	var capturedCtx *auth.AuthContext

	middleware := srv.apiKeyAuthMiddleware()
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCtx = auth.AuthContextFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", rawToken)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, capturedCtx, "AuthContext should be set for agent tokens")
	assert.False(t, capturedCtx.IsAdmin(), "Should not be admin context")
	assert.Equal(t, auth.AuthTypeAgent, capturedCtx.Type)
	assert.Equal(t, "test-agent", capturedCtx.AgentName)
	assert.Equal(t, auth.TokenPrefix(rawToken), capturedCtx.TokenPrefix)
	assert.Equal(t, []string{"github", "gitlab"}, capturedCtx.AllowedServers)
	assert.Equal(t, []string{auth.PermRead, auth.PermWrite}, capturedCtx.Permissions)
}

func TestAPIKeyAuth_NoTokenStore_RejectsAgentToken(t *testing.T) {
	logger := zap.NewNop().Sugar()

	rawToken, err := auth.GenerateToken()
	require.NoError(t, err)

	cfg := &config.Config{APIKey: "admin-key"}
	mockCtrl := &testControllerWithConfig{cfg: cfg}

	srv := NewServer(mockCtrl, logger, nil)
	// Don't set token store

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("X-API-Key", rawToken)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"Agent token should be rejected when token store is not configured")
}

