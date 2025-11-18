package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mcpproxy-go/internal/httpapi"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockController is a minimal mock for testing the code exec handler
type mockController struct {
	callToolFunc func(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error)
}

func (m *mockController) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	if m.callToolFunc != nil {
		return m.callToolFunc(ctx, toolName, args)
	}
	return nil, nil
}

// Stub implementations for other ServerController methods
func (m *mockController) IsRunning() bool                              { return true }
func (m *mockController) IsReady() bool                                { return true }
func (m *mockController) GetListenAddress() string                     { return "" }
func (m *mockController) GetUpstreamStats() map[string]interface{}     { return nil }
func (m *mockController) StartServer(ctx context.Context) error        { return nil }
func (m *mockController) StopServer() error                            { return nil }
func (m *mockController) GetStatus() interface{}                       { return nil }
func (m *mockController) StatusChannel() <-chan interface{}            { return nil }
func (m *mockController) EventsChannel() <-chan interface{}            { return nil }
func (m *mockController) GetAllServers() ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *mockController) EnableServer(serverName string, enabled bool) error      { return nil }
func (m *mockController) RestartServer(serverName string) error                   { return nil }
func (m *mockController) ForceReconnectAllServers(reason string) error            { return nil }
func (m *mockController) GetDockerRecoveryStatus() interface{}                    { return nil }
func (m *mockController) QuarantineServer(serverName string, quarantined bool) error {
	return nil
}
func (m *mockController) GetQuarantinedServers() ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *mockController) UnquarantineServer(serverName string) error { return nil }
func (m *mockController) GetServerTools(serverName string) ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *mockController) SearchTools(query string, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *mockController) GetServerLogs(serverName string, tail int) ([]string, error) { return nil, nil }
func (m *mockController) ReloadConfiguration() error                                   { return nil }
func (m *mockController) GetConfigPath() string                                        { return "" }
func (m *mockController) GetLogDir() string                                            { return "" }
func (m *mockController) TriggerOAuthLogin(serverName string) error                    { return nil }
func (m *mockController) GetSecretResolver() interface{}                               { return nil }
func (m *mockController) GetCurrentConfig() interface{}                                { return nil }
func (m *mockController) NotifySecretsChanged(ctx context.Context, operation, secretName string) error {
	return nil
}
func (m *mockController) GetToolCalls(limit, offset int) (interface{}, int, error) { return nil, 0, nil }
func (m *mockController) GetToolCallByID(id string) (interface{}, error)           { return nil, nil }
func (m *mockController) GetServerToolCalls(serverName string, limit int) (interface{}, error) {
	return nil, nil
}
func (m *mockController) ReplayToolCall(id string, arguments map[string]interface{}) (interface{}, error) {
	return nil, nil
}
func (m *mockController) ValidateConfig(cfg interface{}) (interface{}, error) { return nil, nil }
func (m *mockController) ApplyConfig(cfg interface{}, cfgPath string) (interface{}, error) {
	return nil, nil
}
func (m *mockController) GetConfig() (interface{}, error) { return nil, nil }
func (m *mockController) GetTokenSavings() (interface{}, error) {
	return nil, nil
}
func (m *mockController) ListRegistries() ([]interface{}, error) { return nil, nil }
func (m *mockController) SearchRegistryServers(registryID, tag, query string, limit int) ([]interface{}, error) {
	return nil, nil
}

func TestCodeExecHandler_Success(t *testing.T) {
	// Given: Valid code execution request
	reqBody := map[string]interface{}{
		"code":  "({ result: input.value * 2 })",
		"input": map[string]interface{}{"value": 21},
		"options": map[string]interface{}{
			"timeout_ms":     60000,
			"max_tool_calls": 10,
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/code/exec", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Mock controller that returns success result in MCP Content format
	mockCtrl := &mockController{
		callToolFunc: func(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
			assert.Equal(t, "code_execution", toolName)
			// Return MCP Content array format (matches actual CallTool behavior)
			execResult := map[string]interface{}{
				"ok":     true,
				"result": 42,
			}
			resultJSON, _ := json.Marshal(execResult)
			return []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": string(resultJSON),
				},
			}, nil
		},
	}

	logger := zap.NewNop().Sugar()
	handler := httpapi.NewCodeExecHandler(mockCtrl, logger)

	// When: Calling endpoint
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	// Then: Returns success with result
	assert.Equal(t, http.StatusOK, recorder.Code)

	var response map[string]interface{}
	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response["ok"].(bool))
	assert.Contains(t, response, "result")
}

func TestCodeExecHandler_MissingCode(t *testing.T) {
	// Given: Request without code
	reqBody := map[string]interface{}{
		"input": map[string]interface{}{},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/code/exec", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	mockCtrl := &mockController{}
	logger := zap.NewNop().Sugar()
	handler := httpapi.NewCodeExecHandler(mockCtrl, logger)

	// When: Calling endpoint
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	// Then: Returns 400 Bad Request
	assert.Equal(t, http.StatusBadRequest, recorder.Code)

	var response map[string]interface{}
	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.False(t, response["ok"].(bool))
	assert.Contains(t, response, "error")
}

func TestCodeExecHandler_ExecutionError(t *testing.T) {
	// Given: Code with syntax error (returned from code_execution tool)
	reqBody := map[string]interface{}{
		"code":  "invalid javascript {{{",
		"input": map[string]interface{}{},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/code/exec", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Mock controller returns error result from code_execution tool in MCP Content format
	mockCtrl := &mockController{
		callToolFunc: func(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
			// Return MCP Content array format with error
			execResult := map[string]interface{}{
				"ok": false,
				"error": map[string]interface{}{
					"code":    "SYNTAX_ERROR",
					"message": "Invalid syntax",
				},
			}
			resultJSON, _ := json.Marshal(execResult)
			return []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": string(resultJSON),
				},
			}, nil
		},
	}

	logger := zap.NewNop().Sugar()
	handler := httpapi.NewCodeExecHandler(mockCtrl, logger)

	// When: Calling endpoint
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	// Then: Returns 200 with ok=false (execution error, not HTTP error)
	assert.Equal(t, http.StatusOK, recorder.Code)

	var response map[string]interface{}
	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.False(t, response["ok"].(bool))
	errorMap := response["error"].(map[string]interface{})
	assert.Equal(t, "SYNTAX_ERROR", errorMap["code"])
}
