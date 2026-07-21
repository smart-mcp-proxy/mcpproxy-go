package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/reqcontext"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestHandleAddServer tests the POST /api/v1/servers endpoint
func TestHandleAddServer(t *testing.T) {
	t.Run("adds HTTP server successfully", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		srv := NewServer(mockCtrl, logger, nil)

		reqBody := AddServerRequest{
			Name:     "test-http",
			URL:      "https://example.com/mcp",
			Protocol: "http",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Expected 200 OK")

		var resp struct {
			Success bool                           `json:"success"`
			Data    contracts.ServerActionResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, "test-http", resp.Data.Server)
		assert.Equal(t, "add", resp.Data.Action)
		assert.True(t, resp.Data.Success)
	})

	t.Run("adds stdio server successfully", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		srv := NewServer(mockCtrl, logger, nil)

		reqBody := AddServerRequest{
			Name:     "test-stdio",
			Command:  "npx",
			Args:     []string{"-y", "@anthropic/mcp-server"},
			Protocol: "stdio",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Expected 200 OK")
	})

	t.Run("carries per-server isolation opt-out through on create", func(t *testing.T) {
		// Regression: the add handler used to drop req.Isolation entirely, so a
		// stdio server added via POST could not opt OUT of isolation when global
		// docker_isolation.enabled=true — it was force-wrapped in a container
		// (with the host command path) and failed to start. The add path must
		// map Isolation exactly like the PATCH/update path.
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		srv := NewServer(mockCtrl, logger, nil)

		optOut := false
		reqBody := AddServerRequest{
			Name:      "test-isolation-optout",
			Command:   "/usr/local/bin/mcpfixture",
			Args:      []string{"--transport", "stdio"},
			Protocol:  "stdio",
			Isolation: &IsolationRequest{Enabled: &optOut},
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, "Expected 200 OK")
		require.NotNil(t, mockCtrl.captured, "controller.AddServer was not called")
		require.NotNil(t, mockCtrl.captured.Isolation, "per-server isolation override was dropped on create")
		require.NotNil(t, mockCtrl.captured.Isolation.Enabled)
		assert.False(t, *mockCtrl.captured.Isolation.Enabled, "isolation.enabled=false must be carried through on create")
	})

	t.Run("carries per-server isolation image override through on create", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		srv := NewServer(mockCtrl, logger, nil)

		on := true
		image := "mcpfixture:gate"
		reqBody := AddServerRequest{
			Name:      "test-isolation-image",
			Command:   "/mcpfixture",
			Args:      []string{"--transport", "stdio"},
			Protocol:  "stdio",
			Isolation: &IsolationRequest{Enabled: &on, Image: &image},
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, mockCtrl.captured)
		require.NotNil(t, mockCtrl.captured.Isolation)
		assert.Equal(t, "mcpfixture:gate", mockCtrl.captured.Isolation.Image)
	})

	t.Run("leaves isolation nil when the request omits it", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		srv := NewServer(mockCtrl, logger, nil)

		reqBody := AddServerRequest{Name: "test-no-isolation", URL: "https://example.com/mcp", Protocol: "http"}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, mockCtrl.captured)
		assert.Nil(t, mockCtrl.captured.Isolation, "omitted isolation must stay nil (do-not-touch semantics)")
	})

	t.Run("rejects duplicate server", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{
			apiKey:       "test-key",
			existsServer: "existing-server",
		}
		srv := NewServer(mockCtrl, logger, nil)

		reqBody := AddServerRequest{
			Name:     "existing-server",
			URL:      "https://example.com/mcp",
			Protocol: "http",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusConflict, w.Code, "Expected 409 Conflict")
	})

	t.Run("rejects missing name", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		srv := NewServer(mockCtrl, logger, nil)

		reqBody := AddServerRequest{
			URL:      "https://example.com/mcp",
			Protocol: "http",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 Bad Request")
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockAddServerController{apiKey: "test-key"}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers", bytes.NewReader([]byte("invalid json")))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 Bad Request")
	})
}

// refreshNotFoundController makes DiscoverServerTools report a not-found error
// so the refresh endpoint's 404 mapping can be exercised.
type refreshNotFoundController struct {
	*MockServerController
}

func (c *refreshNotFoundController) DiscoverServerTools(_ context.Context, _ string) error {
	return fmt.Errorf("server not found")
}

// refreshAdminController wraps a ServerController so the auth middleware sees a
// real *config.Config (required to distinguish admin from agent tokens) with a
// known admin key. Refresh is admin-only (issue #873), so its tests must
// authenticate rather than relying on the middleware's testing passthrough.
type refreshAdminController struct {
	ServerController
	apiKey string
}

func (c *refreshAdminController) GetCurrentConfig() any {
	return &config.Config{APIKey: c.apiKey}
}

// TestHandleRefreshServer tests the POST /api/v1/servers/{id}/refresh endpoint
// (issue #873 operator recovery op).
func TestHandleRefreshServer(t *testing.T) {
	const adminKey = "admin-key"

	t.Run("refreshes server successfully", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		srv := NewServer(&refreshAdminController{ServerController: &MockServerController{}, apiKey: adminKey}, logger, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/test-server/refresh", nil)
		req.Header.Set("X-API-Key", adminKey)
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool                           `json:"success"`
			Data    contracts.ServerActionResponse `json:"data"`
		}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.True(t, resp.Success)
		assert.Equal(t, "test-server", resp.Data.Server)
		assert.Equal(t, "refresh", resp.Data.Action)
		assert.True(t, resp.Data.Success)
	})

	t.Run("returns 404 when server not found", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		ctrl := &refreshAdminController{
			ServerController: &refreshNotFoundController{&MockServerController{}},
			apiKey:           adminKey,
		}
		srv := NewServer(ctrl, logger, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/ghost/refresh", nil)
		req.Header.Set("X-API-Key", adminKey)
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	// SECURITY (issue #873): the MCP surface blocks 'refresh' for agent tokens;
	// the REST alias must too, else an agent bypasses the restriction over HTTP.
	t.Run("agent token forbidden", func(t *testing.T) {
		ctrl := &refreshAdminController{ServerController: &MockServerController{}, apiKey: "admin-secret"}
		srv, agentToken := agentTokenServer(t, ctrl)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/test-server/refresh", nil)
		req.Header.Set("X-API-Key", agentToken)
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "admin access")
	})
}

// TestHandleDiscoverServerTools covers the admin gate on the discover-tools
// endpoint (issue #873): it is the alias of /refresh reaching the same
// authoritative reindex path, so it must be admin-only too.
func TestHandleDiscoverServerTools(t *testing.T) {
	const adminKey = "admin-key"

	t.Run("discovers tools for admin", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		srv := NewServer(&refreshAdminController{ServerController: &MockServerController{}, apiKey: adminKey}, logger, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/test-server/discover-tools", nil)
		req.Header.Set("X-API-Key", adminKey)
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
	})

	// SECURITY (issue #873): an agent token blocked from 'refresh' must not
	// reach the same reindex path through the discover-tools alias.
	t.Run("agent token forbidden", func(t *testing.T) {
		ctrl := &refreshAdminController{ServerController: &MockServerController{}, apiKey: "admin-secret"}
		srv, agentToken := agentTokenServer(t, ctrl)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/test-server/discover-tools", nil)
		req.Header.Set("X-API-Key", agentToken)
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "admin access")
	})
}

// TestHandleRemoveServer tests the DELETE /api/v1/servers/{name} endpoint
func TestHandleRemoveServer(t *testing.T) {
	t.Run("removes server successfully", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRemoveServerController{
			apiKey:       "test-key",
			existsServer: "test-server",
		}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/servers/test-server", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Expected 200 OK")

		var resp struct {
			Success bool                           `json:"success"`
			Data    contracts.ServerActionResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Equal(t, "test-server", resp.Data.Server)
		assert.Equal(t, "remove", resp.Data.Action)
		assert.True(t, resp.Data.Success)
	})

	t.Run("returns 404 for non-existent server", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockRemoveServerController{
			apiKey:       "test-key",
			existsServer: "other-server",
		}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/servers/non-existent", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code, "Expected 404 Not Found")
	})
}

// mockAddServerController is a mock controller for add server tests
type mockAddServerController struct {
	baseController
	apiKey       string
	existsServer string
	captured     *config.ServerConfig
}

func (m *mockAddServerController) GetCurrentConfig() any {
	return &config.Config{
		APIKey: m.apiKey,
	}
}

func (m *mockAddServerController) AddServer(_ context.Context, cfg *config.ServerConfig) error {
	if cfg.Name == m.existsServer {
		return fmt.Errorf("server '%s' already exists", cfg.Name)
	}
	m.captured = cfg
	return nil
}

// mockRemoveServerController is a mock controller for remove server tests
type mockRemoveServerController struct {
	baseController
	apiKey       string
	existsServer string
}

func (m *mockRemoveServerController) GetCurrentConfig() any {
	return &config.Config{
		APIKey: m.apiKey,
	}
}

func (m *mockRemoveServerController) RemoveServer(_ context.Context, name string) error {
	if name != m.existsServer {
		return fmt.Errorf("server '%s' not found", name)
	}
	return nil
}

// T017: Test that request_id is included in logs when using context-aware logger
func TestRequestIDInLogs(t *testing.T) {
	t.Run("GetLogger returns logger with request_id from context", func(t *testing.T) {
		// Create observable logger to capture log output
		core, recorded := observer.New(zapcore.InfoLevel)
		logger := zap.New(core).Sugar()

		// Create a request with context containing request_id
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		requestID := "test-request-id-12345"
		ctx := reqcontext.WithRequestID(req.Context(), requestID)

		// Store logger with request_id in context
		requestLogger := logger.With("request_id", requestID)
		ctx = WithLogger(ctx, requestLogger)
		req = req.WithContext(ctx)

		// Get logger from context and log something
		contextLogger := GetLogger(req.Context())
		contextLogger.Info("test log message")

		// Verify log entry contains request_id field
		entries := recorded.All()
		require.Len(t, entries, 1, "Expected exactly one log entry")

		entry := entries[0]
		assert.Equal(t, "test log message", entry.Message)

		// Find request_id field in context
		var foundRequestID string
		for _, field := range entry.Context {
			if field.Key == "request_id" {
				foundRequestID = field.String
				break
			}
		}
		assert.Equal(t, requestID, foundRequestID, "request_id should be present in log fields")
	})

	t.Run("RequestIDLoggerMiddleware adds request_id to logger in context", func(t *testing.T) {
		// Create observable logger
		core, recorded := observer.New(zapcore.InfoLevel)
		logger := zap.New(core).Sugar()

		// Create middleware chain: RequestIDMiddleware -> RequestIDLoggerMiddleware -> handler
		var capturedLogger *zap.SugaredLogger
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedLogger = GetLogger(r.Context())
			capturedLogger.Info("handler log message")
			w.WriteHeader(http.StatusOK)
		})

		// Chain the middlewares
		chain := RequestIDMiddleware(RequestIDLoggerMiddleware(logger)(handler))

		// Make request with client-provided request ID
		clientRequestID := "client-provided-request-id"
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set(reqcontext.RequestIDHeader, clientRequestID)
		w := httptest.NewRecorder()

		chain.ServeHTTP(w, req)

		// Verify response
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, clientRequestID, w.Header().Get(reqcontext.RequestIDHeader))

		// Verify log entry contains request_id
		entries := recorded.All()
		require.Len(t, entries, 1, "Expected exactly one log entry")

		entry := entries[0]
		assert.Equal(t, "handler log message", entry.Message)

		// Verify request_id field
		var foundRequestID string
		for _, field := range entry.Context {
			if field.Key == "request_id" {
				foundRequestID = field.String
				break
			}
		}
		assert.Equal(t, clientRequestID, foundRequestID, "request_id should match client-provided ID")
	})

	t.Run("RequestIDLoggerMiddleware generates UUID when no client ID provided", func(t *testing.T) {
		// Create observable logger
		core, recorded := observer.New(zapcore.InfoLevel)
		logger := zap.New(core).Sugar()

		// Create middleware chain
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			GetLogger(r.Context()).Info("handler log")
			w.WriteHeader(http.StatusOK)
		})

		chain := RequestIDMiddleware(RequestIDLoggerMiddleware(logger)(handler))

		// Make request WITHOUT client-provided request ID
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		w := httptest.NewRecorder()

		chain.ServeHTTP(w, req)

		// Verify response has generated request ID
		responseRequestID := w.Header().Get(reqcontext.RequestIDHeader)
		assert.NotEmpty(t, responseRequestID, "Should have generated request ID")
		assert.Contains(t, responseRequestID, "-", "Generated ID should be UUID format")

		// Verify log entry contains the same generated request_id
		entries := recorded.All()
		require.Len(t, entries, 1)

		var loggedRequestID string
		for _, field := range entries[0].Context {
			if field.Key == "request_id" {
				loggedRequestID = field.String
				break
			}
		}
		assert.Equal(t, responseRequestID, loggedRequestID, "Logged request_id should match response header")
	})
}

// =============================================================================
// Spec 020: OAuth Login Error Feedback - handleServerLogin Tests
// =============================================================================

// mockOAuthManagementService implements TriggerOAuthLoginQuick for server login tests
type mockOAuthManagementService struct {
	triggerError  error
	triggerResult *core.OAuthStartResult
}

func (m *mockOAuthManagementService) TriggerOAuthLoginQuick(_ context.Context, _ string) (*core.OAuthStartResult, error) {
	if m.triggerError != nil {
		return nil, m.triggerError
	}
	if m.triggerResult != nil {
		return m.triggerResult, nil
	}
	// Default success result
	return &core.OAuthStartResult{
		AuthURL:       "https://example.com/oauth/authorize?client_id=test",
		BrowserOpened: true,
		CorrelationID: "test-correlation-id-12345678",
	}, nil
}

// mockLoginController is a mock controller for server login tests
type mockLoginController struct {
	baseController
	apiKey  string
	mgmtSvc *mockOAuthManagementService
}

func (m *mockLoginController) GetCurrentConfig() any {
	return &config.Config{
		APIKey: m.apiKey,
	}
}

func (m *mockLoginController) GetManagementService() interface{} {
	return m.mgmtSvc
}

// TestHandleServerLogin_OAuthStartResponse tests the POST /api/v1/servers/{id}/login endpoint
// Spec 020: OAuth Login Error Feedback - Phase 3
func TestHandleServerLogin_OAuthStartResponse(t *testing.T) {
	t.Run("returns OAuthStartResponse with all required fields on success", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockLoginController{
			apiKey:  "test-key",
			mgmtSvc: &mockOAuthManagementService{triggerError: nil},
		}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/test-server/login", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "Expected 200 OK")

		var resp struct {
			Success bool                         `json:"success"`
			Data    contracts.OAuthStartResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Verify wrapper success
		assert.True(t, resp.Success, "Wrapper success should be true")

		// Verify OAuthStartResponse fields (Spec 020)
		assert.True(t, resp.Data.Success, "OAuthStartResponse.Success should be true")
		assert.Equal(t, "test-server", resp.Data.ServerName, "ServerName should match")
		assert.NotEmpty(t, resp.Data.CorrelationID, "CorrelationID should be set")
		assert.True(t, resp.Data.BrowserOpened, "BrowserOpened should be true on success")
		assert.Empty(t, resp.Data.BrowserError, "BrowserError should be empty on success")
		assert.Contains(t, resp.Data.Message, "test-server", "Message should mention server name")
		assert.Contains(t, resp.Data.Message, "OAuth", "Message should mention OAuth")
	})

	t.Run("includes correlation_id matching X-Correlation-ID header", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockLoginController{
			apiKey:  "test-key",
			mgmtSvc: &mockOAuthManagementService{triggerError: nil},
		}
		srv := NewServer(mockCtrl, logger, nil)

		clientCorrelationID := "client-correlation-id-12345"
		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/oauth-server/login", nil)
		req.Header.Set("X-API-Key", "test-key")
		req.Header.Set("X-Correlation-ID", clientCorrelationID)
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.OAuthStartResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Correlation ID should match the client-provided X-Correlation-ID header
		assert.Equal(t, clientCorrelationID, resp.Data.CorrelationID,
			"CorrelationID should match client-provided X-Correlation-ID")
	})

	t.Run("generates correlation_id when not provided", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockLoginController{
			apiKey:  "test-key",
			mgmtSvc: &mockOAuthManagementService{triggerError: nil},
		}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/oauth-server/login", nil)
		req.Header.Set("X-API-Key", "test-key")
		// No X-Correlation-ID header - should be auto-generated
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Data contracts.OAuthStartResponse `json:"data"`
		}
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// Correlation ID should be auto-generated (32-char hex string)
		assert.NotEmpty(t, resp.Data.CorrelationID, "CorrelationID should be auto-generated")
		assert.Len(t, resp.Data.CorrelationID, 32, "Auto-generated correlation ID should be 32 chars")
	})

	t.Run("returns OAuthFlowError with request_id on OAuth failure", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		oauthErr := &contracts.OAuthFlowError{
			Success:       false,
			ErrorType:     "client_id_required",
			ServerName:    "broken-server",
			Message:       "Client ID is required for OAuth authentication",
			Suggestion:    "Configure oauth.client_id in server settings",
			CorrelationID: "flow-123",
		}
		mockCtrl := &mockLoginController{
			apiKey:  "test-key",
			mgmtSvc: &mockOAuthManagementService{triggerError: oauthErr},
		}
		srv := NewServer(mockCtrl, logger, nil)

		clientRequestID := "request-for-error-tracking"
		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/broken-server/login", nil)
		req.Header.Set("X-API-Key", "test-key")
		req.Header.Set(reqcontext.RequestIDHeader, clientRequestID)
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 Bad Request for OAuth error")

		var errResp contracts.OAuthFlowError
		err := json.NewDecoder(w.Body).Decode(&errResp)
		require.NoError(t, err)

		// Verify error fields (Spec 020)
		assert.False(t, errResp.Success, "Success should be false")
		assert.Equal(t, "client_id_required", errResp.ErrorType)
		assert.Equal(t, "broken-server", errResp.ServerName)
		assert.NotEmpty(t, errResp.Message)
		assert.NotEmpty(t, errResp.Suggestion)
		// Request ID should be added from context
		assert.Equal(t, clientRequestID, errResp.RequestID,
			"RequestID should be populated from X-Request-Id header")
	})

	t.Run("returns OAuthValidationError for validation failures", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		validationErr := &contracts.OAuthValidationError{
			Success:          false,
			ErrorType:        "server_not_found",
			ServerName:       "nonexistent",
			Message:          "Server 'nonexistent' not found",
			Suggestion:       "Check server name with 'mcpproxy upstream list'",
			AvailableServers: []string{"server-a", "server-b"},
		}
		mockCtrl := &mockLoginController{
			apiKey:  "test-key",
			mgmtSvc: &mockOAuthManagementService{triggerError: validationErr},
		}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/nonexistent/login", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 Bad Request for validation error")

		var errResp contracts.OAuthValidationError
		err := json.NewDecoder(w.Body).Decode(&errResp)
		require.NoError(t, err)

		// Verify validation error fields (Spec 020)
		assert.False(t, errResp.Success)
		assert.Equal(t, "server_not_found", errResp.ErrorType)
		assert.Equal(t, "nonexistent", errResp.ServerName)
		assert.NotEmpty(t, errResp.Message)
		assert.NotEmpty(t, errResp.Suggestion)
		assert.Contains(t, errResp.AvailableServers, "server-a")
		assert.Contains(t, errResp.AvailableServers, "server-b")
	})

	t.Run("returns 404 for server not found error", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockLoginController{
			apiKey:  "test-key",
			mgmtSvc: &mockOAuthManagementService{triggerError: fmt.Errorf("server 'unknown' not found")},
		}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/unknown/login", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code, "Expected 404 Not Found")
	})

	t.Run("returns 403 for management disabled", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockLoginController{
			apiKey:  "test-key",
			mgmtSvc: &mockOAuthManagementService{triggerError: fmt.Errorf("management disabled")},
		}
		srv := NewServer(mockCtrl, logger, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers/test-server/login", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code, "Expected 403 Forbidden")
	})

	t.Run("returns 400 for empty server ID in URL", func(t *testing.T) {
		logger := zap.NewNop().Sugar()
		mockCtrl := &mockLoginController{
			apiKey:  "test-key",
			mgmtSvc: &mockOAuthManagementService{triggerError: nil},
		}
		srv := NewServer(mockCtrl, logger, nil)

		// Route with empty ID segment - the router may treat this differently
		// depending on configuration. Let's test the actual behavior.
		req := httptest.NewRequest(http.MethodPost, "/api/v1/servers//login", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		// The Chi router with empty ID segment returns 400 (Server ID required)
		assert.Equal(t, http.StatusBadRequest, w.Code, "Expected 400 Bad Request for empty server ID")
	})
}
