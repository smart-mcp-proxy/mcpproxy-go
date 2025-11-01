package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"mcpproxy-go/internal/config"
	"mcpproxy-go/internal/contracts"
	internalRuntime "mcpproxy-go/internal/runtime"
	"mcpproxy-go/internal/secret"
)

// MockServerController implements ServerController for testing
type MockServerController struct{}

func (m *MockServerController) IsRunning() bool          { return true }
func (m *MockServerController) GetListenAddress() string { return ":8080" }
func (m *MockServerController) GetUpstreamStats() map[string]interface{} {
	return map[string]interface{}{
		"servers": map[string]interface{}{
			"test-server": map[string]interface{}{
				"connected":   true,
				"tool_count":  5,
				"quarantined": false,
			},
		},
	}
}
func (m *MockServerController) StartServer(_ context.Context) error { return nil }
func (m *MockServerController) StopServer() error                   { return nil }
func (m *MockServerController) GetStatus() interface{} {
	return map[string]interface{}{
		"phase":   "Ready",
		"message": "All systems operational",
	}
}
func (m *MockServerController) StatusChannel() <-chan interface{} {
	ch := make(chan interface{})
	close(ch)
	return ch
}
func (m *MockServerController) EventsChannel() <-chan internalRuntime.Event {
	ch := make(chan internalRuntime.Event)
	close(ch)
	return ch
}

func (m *MockServerController) GetAllServers() ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{
			"id":              "test-server",
			"name":            "test-server",
			"protocol":        "stdio",
			"command":         "echo",
			"args":            []interface{}{"hello"},
			"enabled":         true,
			"quarantined":     false,
			"connected":       true,
			"status":          "Ready",
			"tool_count":      5,
			"reconnect_count": 0,
			"created":         "2025-09-19T12:00:00Z",
			"updated":         "2025-09-19T12:00:00Z",
		},
	}, nil
}

func (m *MockServerController) EnableServer(_ string, _ bool) error { return nil }
func (m *MockServerController) RestartServer(_ string) error        { return nil }
func (m *MockServerController) ForceReconnectAllServers(_ string) error {
	return nil
}
func (m *MockServerController) QuarantineServer(_ string, _ bool) error {
	return nil
}
func (m *MockServerController) GetQuarantinedServers() ([]map[string]interface{}, error) {
	return []map[string]interface{}{}, nil
}
func (m *MockServerController) UnquarantineServer(_ string) error { return nil }

func (m *MockServerController) GetServerTools(serverName string) ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{
			"name":        "echo_tool",
			"server_name": serverName,
			"description": "A simple echo tool for testing",
			"usage":       10,
		},
	}, nil
}

func (m *MockServerController) SearchTools(_ string, _ int) ([]map[string]interface{}, error) {
	return []map[string]interface{}{
		{
			"tool": map[string]interface{}{
				"name":        "echo_tool",
				"server_name": "test-server",
				"description": "A simple echo tool for testing",
				"usage":       10,
			},
			"score": 0.95,
		},
	}, nil
}

func (m *MockServerController) GetServerLogs(_ string, _ int) ([]string, error) {
	return []string{
		"2025-09-19T12:00:00Z INFO Server started",
		"2025-09-19T12:00:01Z INFO Tool registered: echo_tool",
	}, nil
}

func (m *MockServerController) ReloadConfiguration() error       { return nil }
func (m *MockServerController) GetConfigPath() string            { return "/test/config.json" }
func (m *MockServerController) GetLogDir() string                { return "/test/logs" }
func (m *MockServerController) TriggerOAuthLogin(_ string) error { return nil }

// Secrets management methods
func (m *MockServerController) GetSecretResolver() *secret.Resolver { return nil }
func (m *MockServerController) NotifySecretsChanged(_ context.Context, _, _ string) error {
	return nil
}
func (m *MockServerController) GetCurrentConfig() interface{} { return map[string]interface{}{} }

// Tool call history methods
func (m *MockServerController) GetToolCalls(_ int, _ int) ([]*contracts.ToolCallRecord, int, error) {
	return []*contracts.ToolCallRecord{}, 0, nil
}
func (m *MockServerController) GetToolCallByID(_ string) (*contracts.ToolCallRecord, error) {
	return nil, nil
}
func (m *MockServerController) GetServerToolCalls(_ string, _ int) ([]*contracts.ToolCallRecord, error) {
	return []*contracts.ToolCallRecord{}, nil
}
func (m *MockServerController) ReplayToolCall(_ string, _ map[string]interface{}) (*contracts.ToolCallRecord, error) {
	return &contracts.ToolCallRecord{
		ID:         "replayed-call-123",
		ServerName: "test-server",
		ToolName:   "echo_tool",
		Arguments:  map[string]interface{}{},
	}, nil
}

// Configuration management methods
func (m *MockServerController) ValidateConfig(_ *config.Config) ([]config.ValidationError, error) {
	return []config.ValidationError{}, nil
}
func (m *MockServerController) ApplyConfig(_ *config.Config, _ string) (*internalRuntime.ConfigApplyResult, error) {
	return &internalRuntime.ConfigApplyResult{
		Success:            true,
		AppliedImmediately: true,
		RequiresRestart:    false,
		ChangedFields:      []string{},
	}, nil
}
func (m *MockServerController) GetConfig() (*config.Config, error) {
	return &config.Config{
		Listen:            "127.0.0.1:8080",
		TopK:              5,
		ToolsLimit:        15,
		ToolResponseLimit: 1000,
	}, nil
}

// Readiness method
func (m *MockServerController) IsReady() bool { return true }

// Token statistics
func (m *MockServerController) GetTokenSavings() (*contracts.ServerTokenMetrics, error) {
	return &contracts.ServerTokenMetrics{}, nil
}

// Tool execution
func (m *MockServerController) CallTool(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
	return map[string]interface{}{"result": "success"}, nil
}

// Registry browsing
func (m *MockServerController) ListRegistries() ([]interface{}, error) {
	return []interface{}{}, nil
}
func (m *MockServerController) SearchRegistryServers(_, _, _ string, _ int) ([]interface{}, error) {
	return []interface{}{}, nil
}

// Test contract compliance for API responses
func TestAPIContractCompliance(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	controller := &MockServerController{}
	server := NewServer(controller, logger, nil)

	tests := []struct {
		name         string
		method       string
		path         string
		expectedType string
		goldenFile   string
	}{
		{
			name:         "GET /api/v1/servers",
			method:       "GET",
			path:         "/api/v1/servers",
			expectedType: "GetServersResponse",
			goldenFile:   "get_servers.json",
		},
		{
			name:         "GET /api/v1/servers/test-server/tools",
			method:       "GET",
			path:         "/api/v1/servers/test-server/tools",
			expectedType: "GetServerToolsResponse",
			goldenFile:   "get_server_tools.json",
		},
		{
			name:         "GET /api/v1/index/search",
			method:       "GET",
			path:         "/api/v1/index/search?q=echo",
			expectedType: "SearchToolsResponse",
			goldenFile:   "search_tools.json",
		},
		{
			name:         "GET /api/v1/servers/test-server/logs",
			method:       "GET",
			path:         "/api/v1/servers/test-server/logs?tail=10",
			expectedType: "GetServerLogsResponse",
			goldenFile:   "get_server_logs.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request
			req := httptest.NewRequest(tt.method, tt.path, http.NoBody)
			w := httptest.NewRecorder()

			// Execute request
			server.ServeHTTP(w, req)

			// Check status code
			assert.Equal(t, http.StatusOK, w.Code, "Expected 200 OK")

			// Parse response
			var response contracts.APIResponse
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err, "Response should be valid JSON")

			// Verify it's a success response
			assert.True(t, response.Success, "Response should indicate success")
			assert.Empty(t, response.Error, "Success response should not have error")
			assert.NotNil(t, response.Data, "Success response should have data")

			// Validate specific response type structure
			validateResponseType(t, response.Data, tt.expectedType)

			// Update golden file if needed (useful for initial creation)
			if updateGolden() {
				updateGoldenFile(t, tt.goldenFile, w.Body.Bytes())
			} else {
				// Compare with golden file
				compareWithGoldenFile(t, tt.goldenFile, w.Body.Bytes())
			}
		})
	}
}

func validateResponseType(t *testing.T, data interface{}, expectedType string) {
	dataMap, ok := data.(map[string]interface{})
	require.True(t, ok, "Response data should be a map")

	switch expectedType {
	case "GetServersResponse":
		assert.Contains(t, dataMap, "servers", "GetServersResponse should have servers field")
		assert.Contains(t, dataMap, "stats", "GetServersResponse should have stats field")

		servers, ok := dataMap["servers"].([]interface{})
		assert.True(t, ok, "servers should be an array")
		if len(servers) > 0 {
			server := servers[0].(map[string]interface{})
			assert.Contains(t, server, "id", "Server should have id field")
			assert.Contains(t, server, "name", "Server should have name field")
			assert.Contains(t, server, "enabled", "Server should have enabled field")
		}

	case "GetServerToolsResponse":
		assert.Contains(t, dataMap, "server_name", "GetServerToolsResponse should have server_name field")
		assert.Contains(t, dataMap, "tools", "GetServerToolsResponse should have tools field")
		assert.Contains(t, dataMap, "count", "GetServerToolsResponse should have count field")

	case "SearchToolsResponse":
		assert.Contains(t, dataMap, "query", "SearchToolsResponse should have query field")
		assert.Contains(t, dataMap, "results", "SearchToolsResponse should have results field")
		assert.Contains(t, dataMap, "total", "SearchToolsResponse should have total field")
		assert.Contains(t, dataMap, "took", "SearchToolsResponse should have took field")

	case "GetServerLogsResponse":
		assert.Contains(t, dataMap, "server_name", "GetServerLogsResponse should have server_name field")
		assert.Contains(t, dataMap, "logs", "GetServerLogsResponse should have logs field")
		assert.Contains(t, dataMap, "count", "GetServerLogsResponse should have count field")
	}
}

func updateGolden() bool {
	return os.Getenv("UPDATE_GOLDEN") == "true"
}

func updateGoldenFile(t *testing.T, filename string, data []byte) {
	goldenDir := "testdata/golden"
	err := os.MkdirAll(goldenDir, 0755)
	require.NoError(t, err)

	goldenPath := filepath.Join(goldenDir, filename)

	// Format JSON for readability
	var jsonData interface{}
	err = json.Unmarshal(data, &jsonData)
	require.NoError(t, err)

	formattedData, err := json.MarshalIndent(jsonData, "", "  ")
	require.NoError(t, err)

	err = os.WriteFile(goldenPath, formattedData, 0644)
	require.NoError(t, err)

	t.Logf("Updated golden file: %s", goldenPath)
}

func compareWithGoldenFile(t *testing.T, filename string, actual []byte) {
	goldenPath := filepath.Join("testdata", "golden", filename)

	if _, err := os.Stat(goldenPath); os.IsNotExist(err) {
		t.Logf("Golden file %s does not exist. Run with UPDATE_GOLDEN=true to create it.", goldenPath)
		return
	}

	expected, err := os.ReadFile(goldenPath)
	require.NoError(t, err)

	// Parse both to compare structure, ignoring formatting differences
	var expectedJSON, actualJSON interface{}
	err = json.Unmarshal(expected, &expectedJSON)
	require.NoError(t, err)

	err = json.Unmarshal(actual, &actualJSON)
	require.NoError(t, err)

	assert.Equal(t, expectedJSON, actualJSON, "Response should match golden file %s", filename)
}

// Test that all endpoints return properly typed responses
func TestEndpointResponseTypes(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	controller := &MockServerController{}
	server := NewServer(controller, logger, nil)

	// Test server action endpoints
	actionTests := []struct {
		method string
		path   string
		action string
	}{
		{"POST", "/api/v1/servers/test-server/enable", "enable"},
		{"POST", "/api/v1/servers/test-server/disable", "disable"},
		{"POST", "/api/v1/servers/test-server/restart", "restart"},
		{"POST", "/api/v1/servers/test-server/login", "login"},
	}

	for _, tt := range actionTests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, http.NoBody)
			w := httptest.NewRecorder()

			server.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)

			var response contracts.APIResponse
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.True(t, response.Success)

			// Validate ServerActionResponse structure
			data, ok := response.Data.(map[string]interface{})
			require.True(t, ok)

			assert.Contains(t, data, "server")
			assert.Contains(t, data, "action")
			assert.Contains(t, data, "success")
			assert.Equal(t, tt.action, data["action"])
		})
	}
}

// Benchmark API response marshaling
func BenchmarkAPIResponseMarshaling(b *testing.B) {
	logger := zaptest.NewLogger(b).Sugar()
	controller := &MockServerController{}
	server := NewServer(controller, logger, nil)

	req := httptest.NewRequest("GET", "/api/v1/servers", http.NoBody)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, req)
	}
}
