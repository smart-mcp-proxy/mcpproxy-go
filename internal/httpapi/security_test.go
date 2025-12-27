package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/contracts"
	"mcpproxy-go/internal/runtime"
	"mcpproxy-go/internal/secret"
	"mcpproxy-go/internal/storage"
	"mcpproxy-go/internal/transport"
	"mcpproxy-go/internal/updatecheck"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestAPIKeyProtection_EmptyKeyRejected tests that empty API keys are rejected
func TestAPIKeyProtection_EmptyKeyRejected(t *testing.T) {
	logger := zap.NewNop().Sugar()

	// Create mock controller with empty API key
	mockCtrl := &mockControllerEmptyKey{}

	// Create server
	srv := NewServer(mockCtrl, logger, nil)

	// Test all critical REST API endpoints
	testCases := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/status"},
		{"GET", "/api/v1/info"},
		{"GET", "/api/v1/servers"},
		{"POST", "/api/v1/servers"},
		{"DELETE", "/api/v1/servers/test-server"},
		{"GET", "/api/v1/config"},
		{"GET", "/api/v1/doctor"},
		{"POST", "/api/v1/servers/restart_all"},
		{"POST", "/api/v1/servers/enable_all"},
		{"POST", "/api/v1/servers/disable_all"},
		{"GET", "/api/v1/secrets/refs"},
		{"GET", "/api/v1/secrets/config"},
		{"GET", "/api/v1/tool-calls"},
		{"GET", "/api/v1/sessions"},
		{"GET", "/api/v1/registries"},
		{"GET", "/api/v1/index/search"},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.path), func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()

			srv.ServeHTTP(w, req)

			// Must return 401 Unauthorized
			assert.Equal(t, http.StatusUnauthorized, w.Code,
				"Endpoint %s %s must return 401 when API key is empty", tc.method, tc.path)

			// Check error message
			var errResp contracts.ErrorResponse
			err := json.NewDecoder(w.Body).Decode(&errResp)
			require.NoError(t, err)
			assert.Contains(t, errResp.Error, "API key authentication required",
				"Error message should mention API key requirement")
		})
	}
}

// TestAPIKeyProtection_ValidKeyAccepted tests that valid API keys are accepted
func TestAPIKeyProtection_ValidKeyAccepted(t *testing.T) {
	logger := zap.NewNop().Sugar()

	// Create mock controller with valid API key
	apiKey := "test-valid-api-key-12345"
	mockCtrl := &mockControllerWithKey{apiKey: apiKey}

	// Create server
	srv := NewServer(mockCtrl, logger, nil)

	testCases := []struct {
		method     string
		path       string
		authHeader string
		expectCode int
	}{
		{"GET", "/api/v1/status", "X-API-Key", http.StatusOK},
		{"GET", "/api/v1/info", "X-API-Key", http.StatusOK},
		{"GET", "/api/v1/servers", "X-API-Key", http.StatusOK},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s %s with %s", tc.method, tc.path, tc.authHeader), func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set(tc.authHeader, apiKey)
			w := httptest.NewRecorder()

			srv.ServeHTTP(w, req)

			assert.Equal(t, tc.expectCode, w.Code,
				"Endpoint %s %s should return %d with valid API key", tc.method, tc.path, tc.expectCode)
		})
	}
}

// TestAPIKeyProtection_QueryParamAuth tests API key via query parameter
func TestAPIKeyProtection_QueryParamAuth(t *testing.T) {
	logger := zap.NewNop().Sugar()

	apiKey := "test-query-api-key"
	mockCtrl := &mockControllerWithKey{apiKey: apiKey}

	srv := NewServer(mockCtrl, logger, nil)

	req := httptest.NewRequest("GET", "/api/v1/status?apikey="+apiKey, nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"Should accept API key via query parameter")
}

// TestAPIKeyProtection_InvalidKeyRejected tests that invalid API keys are rejected
func TestAPIKeyProtection_InvalidKeyRejected(t *testing.T) {
	logger := zap.NewNop().Sugar()

	mockCtrl := &mockControllerWithKey{apiKey: "correct-key"}
	srv := NewServer(mockCtrl, logger, nil)

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"Should reject invalid API key")

	var errResp contracts.ErrorResponse
	err := json.NewDecoder(w.Body).Decode(&errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp.Error, "Invalid or missing API key")
}

// TestAPIKeyProtection_TrayConnectionTrusted tests that tray connections bypass API key
func TestAPIKeyProtection_TrayConnectionTrusted(t *testing.T) {
	logger := zap.NewNop().Sugar()

	mockCtrl := &mockControllerWithKey{apiKey: "required-key"}
	srv := NewServer(mockCtrl, logger, nil)

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	// Mark as tray connection
	ctx := transport.TagConnectionContext(req.Context(), transport.ConnectionSourceTray)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code,
		"Tray connections should bypass API key requirement")
}

// TestAPIKeyProtection_HealthEndpointsUnprotected tests that health endpoints don't require auth
func TestAPIKeyProtection_HealthEndpointsUnprotected(t *testing.T) {
	logger := zap.NewNop().Sugar()

	mockCtrl := &mockControllerWithKey{apiKey: "some-key"}
	srv := NewServer(mockCtrl, logger, nil)

	healthEndpoints := []string{
		"/healthz",
		"/readyz",
		"/ready",
	}

	for _, path := range healthEndpoints {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()

			srv.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code,
				"Health endpoint %s should not require API key", path)
		})
	}
}

// Mock controller implementations

type mockControllerEmptyKey struct {
	baseController
}

func (m *mockControllerEmptyKey) GetCurrentConfig() any {
	return &config.Config{
		APIKey: "", // Empty API key
	}
}

type mockControllerWithKey struct {
	baseController
	apiKey string
}

func (m *mockControllerWithKey) GetCurrentConfig() any {
	return &config.Config{
		APIKey: m.apiKey,
	}
}

// baseController provides stub implementations for all ServerController methods
type baseController struct{}

func (m *baseController) IsRunning() bool                              { return true }
func (m *baseController) IsReady() bool                                { return true }
func (m *baseController) GetListenAddress() string                     { return "" }
func (m *baseController) GetUpstreamStats() map[string]interface{}     { return nil }
func (m *baseController) StartServer(ctx context.Context) error        { return nil }
func (m *baseController) StopServer() error                            { return nil }
func (m *baseController) GetStatus() interface{}                       { return nil }
func (m *baseController) StatusChannel() <-chan interface{}            { return nil }
func (m *baseController) EventsChannel() <-chan runtime.Event          { return nil }
func (m *baseController) GetAllServers() ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *baseController) EnableServer(serverName string, enabled bool) error { return nil }
func (m *baseController) RestartServer(serverName string) error               { return nil }
func (m *baseController) ForceReconnectAllServers(reason string) error        { return nil }
func (m *baseController) GetDockerRecoveryStatus() *storage.DockerRecoveryState {
	return nil
}
func (m *baseController) QuarantineServer(serverName string, quarantined bool) error {
	return nil
}
func (m *baseController) GetQuarantinedServers() ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *baseController) UnquarantineServer(serverName string) error { return nil }
func (m *baseController) GetManagementService() interface{}           { return nil }
func (m *baseController) GetServerTools(serverName string) ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *baseController) SearchTools(query string, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *baseController) GetServerLogs(serverName string, tail int) ([]contracts.LogEntry, error) {
	return nil, nil
}
func (m *baseController) ReloadConfiguration() error                                       { return nil }
func (m *baseController) GetConfigPath() string                                            { return "" }
func (m *baseController) GetLogDir() string                                                { return "" }
func (m *baseController) TriggerOAuthLogin(serverName string) error                        { return nil }
func (m *baseController) GetSecretResolver() *secret.Resolver                             { return nil }
func (m *baseController) NotifySecretsChanged(ctx context.Context, op, name string) error { return nil }
func (m *baseController) GetToolCalls(limit, offset int) ([]*contracts.ToolCallRecord, int, error) {
	return nil, 0, nil
}
func (m *baseController) GetToolCallByID(id string) (*contracts.ToolCallRecord, error) {
	return nil, nil
}
func (m *baseController) GetServerToolCalls(serverName string, limit int) ([]*contracts.ToolCallRecord, error) {
	return nil, nil
}
func (m *baseController) ReplayToolCall(id string, args map[string]interface{}) (*contracts.ToolCallRecord, error) {
	return nil, nil
}
func (m *baseController) ValidateConfig(cfg *config.Config) ([]config.ValidationError, error) {
	return nil, nil
}
func (m *baseController) ApplyConfig(cfg *config.Config, cfgPath string) (*runtime.ConfigApplyResult, error) {
	return nil, nil
}
func (m *baseController) GetConfig() (*config.Config, error)                       { return nil, nil }
func (m *baseController) GetTokenSavings() (*contracts.ServerTokenMetrics, error) { return nil, nil }
func (m *baseController) ListRegistries() ([]interface{}, error) {
	return nil, nil
}
func (m *baseController) SearchRegistryServers(registryID, query, tag string, limit int) ([]interface{}, error) {
	return nil, nil
}
func (m *baseController) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	return nil, nil
}
func (m *baseController) GetRuntime() *runtime.Runtime                             { return nil }
func (m *baseController) GetSessions(limit, offset int) (interface{}, int, error) { return nil, 0, nil }
func (m *baseController) GetSessionByID(id string) (*contracts.MCPSession, error) { return nil, nil }
func (m *baseController) GetRecentSessions(limit int) ([]*contracts.MCPSession, int, error) {
	return nil, 0, nil
}
func (m *baseController) GetToolCallsBySession(sessionID string, limit, offset int) ([]*contracts.ToolCallRecord, int, error) {
	return nil, 0, nil
}
func (m *baseController) GetVersionInfo() *updatecheck.VersionInfo    { return nil }
func (m *baseController) RefreshVersionInfo() *updatecheck.VersionInfo { return nil }
func (m *baseController) DiscoverServerTools(_ context.Context, _ string) error {
	return nil
}
func (m *baseController) AddServer(_ context.Context, _ *config.ServerConfig) error {
	return nil
}
func (m *baseController) RemoveServer(_ context.Context, _ string) error {
	return nil
}
func (m *baseController) ListActivities(_ storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	return nil, 0, nil
}
func (m *baseController) GetActivity(_ string) (*storage.ActivityRecord, error) {
	return nil, nil
}
func (m *baseController) StreamActivities(_ storage.ActivityFilter) <-chan *storage.ActivityRecord {
	ch := make(chan *storage.ActivityRecord)
	close(ch)
	return ch
}
