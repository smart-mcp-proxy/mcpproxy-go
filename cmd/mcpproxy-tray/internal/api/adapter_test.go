package api

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Mock Client for Testing
// =============================================================================

// MockClient implements ClientInterface for testing
type MockClient struct {
	servers              []Server
	serversErr           error
	info                 map[string]interface{}
	infoErr              error
	enableErr            error
	quarantineErr        error
	oauthErr             error
	enabledServers       map[string]bool   // tracks enable/disable calls
	quarantinedServers   map[string]bool   // tracks quarantine calls
	unquarantinedServers []string          // tracks unquarantine calls
	oauthTriggered       []string          // tracks OAuth login calls
	statusCh             chan StatusUpdate
}

func NewMockClient() *MockClient {
	return &MockClient{
		enabledServers:     make(map[string]bool),
		quarantinedServers: make(map[string]bool),
		statusCh:           make(chan StatusUpdate, 10),
	}
}

func (m *MockClient) GetServers() ([]Server, error) {
	if m.serversErr != nil {
		return nil, m.serversErr
	}
	return m.servers, nil
}

func (m *MockClient) GetInfo() (map[string]interface{}, error) {
	if m.infoErr != nil {
		return nil, m.infoErr
	}
	return m.info, nil
}

func (m *MockClient) EnableServer(serverName string, enabled bool) error {
	if m.enableErr != nil {
		return m.enableErr
	}
	m.enabledServers[serverName] = enabled
	return nil
}

func (m *MockClient) QuarantineServer(serverName string) error {
	if m.quarantineErr != nil {
		return m.quarantineErr
	}
	m.quarantinedServers[serverName] = true
	return nil
}

func (m *MockClient) UnquarantineServer(serverName string) error {
	if m.quarantineErr != nil {
		return m.quarantineErr
	}
	m.unquarantinedServers = append(m.unquarantinedServers, serverName)
	delete(m.quarantinedServers, serverName)
	return nil
}

func (m *MockClient) TriggerOAuthLogin(serverName string) error {
	if m.oauthErr != nil {
		return m.oauthErr
	}
	m.oauthTriggered = append(m.oauthTriggered, serverName)
	return nil
}

func (m *MockClient) StatusChannel() <-chan StatusUpdate {
	return m.statusCh
}

// =============================================================================
// isServerHealthy Unit Tests
// =============================================================================

func TestIsServerHealthy_WithHealthLevel(t *testing.T) {
	tests := []struct {
		name            string
		health          *HealthStatus
		legacyConnected bool
		expected        bool
	}{
		{
			name:            "healthy level returns true",
			health:          &HealthStatus{Level: "healthy"},
			legacyConnected: false,
			expected:        true,
		},
		{
			name:            "degraded level returns false",
			health:          &HealthStatus{Level: "degraded"},
			legacyConnected: true, // legacy should be ignored
			expected:        false,
		},
		{
			name:            "unhealthy level returns false",
			health:          &HealthStatus{Level: "unhealthy"},
			legacyConnected: true,
			expected:        false,
		},
		{
			name:            "nil health falls back to legacy true",
			health:          nil,
			legacyConnected: true,
			expected:        true,
		},
		{
			name:            "nil health falls back to legacy false",
			health:          nil,
			legacyConnected: false,
			expected:        false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isServerHealthy(tc.health, tc.legacyConnected)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// =============================================================================
// ServerAdapter.IsRunning Tests
// =============================================================================

func TestServerAdapter_IsRunning_Success(t *testing.T) {
	mock := NewMockClient()
	mock.servers = []Server{{Name: "test"}}
	adapter := NewServerAdapter(mock)

	assert.True(t, adapter.IsRunning())
}

func TestServerAdapter_IsRunning_APIError(t *testing.T) {
	mock := NewMockClient()
	mock.serversErr = errors.New("connection refused")
	adapter := NewServerAdapter(mock)

	assert.False(t, adapter.IsRunning())
}

// =============================================================================
// ServerAdapter.GetListenAddress Tests
// =============================================================================

func TestServerAdapter_GetListenAddress_Success(t *testing.T) {
	mock := NewMockClient()
	mock.info = map[string]interface{}{
		"data": map[string]interface{}{
			"listen_addr": "127.0.0.1:8080",
		},
	}
	adapter := NewServerAdapter(mock)

	assert.Equal(t, "127.0.0.1:8080", adapter.GetListenAddress())
}

func TestServerAdapter_GetListenAddress_APIError(t *testing.T) {
	mock := NewMockClient()
	mock.infoErr = errors.New("server error")
	adapter := NewServerAdapter(mock)

	assert.Equal(t, "", adapter.GetListenAddress())
}

func TestServerAdapter_GetListenAddress_MissingData(t *testing.T) {
	mock := NewMockClient()
	mock.info = map[string]interface{}{} // no data field
	adapter := NewServerAdapter(mock)

	assert.Equal(t, "", adapter.GetListenAddress())
}

// =============================================================================
// ServerAdapter.GetUpstreamStats Tests
// =============================================================================

func TestServerAdapter_GetUpstreamStats_Success(t *testing.T) {
	mock := NewMockClient()
	mock.servers = []Server{
		{Name: "server1", Health: &HealthStatus{Level: "healthy"}, ToolCount: 10},
		{Name: "server2", Health: &HealthStatus{Level: "unhealthy"}, ToolCount: 5},
		{Name: "server3", Health: &HealthStatus{Level: "healthy"}, ToolCount: 3},
	}
	adapter := NewServerAdapter(mock)

	stats := adapter.GetUpstreamStats()

	assert.Equal(t, 2, stats["connected_servers"])
	assert.Equal(t, 3, stats["total_servers"])
	assert.Equal(t, 18, stats["total_tools"])
}

func TestServerAdapter_GetUpstreamStats_LegacyFallback(t *testing.T) {
	mock := NewMockClient()
	mock.servers = []Server{
		{Name: "server1", Health: nil, Connected: true, ToolCount: 10},
		{Name: "server2", Health: nil, Connected: false, ToolCount: 5},
	}
	adapter := NewServerAdapter(mock)

	stats := adapter.GetUpstreamStats()

	// Should fall back to Connected field when Health is nil
	assert.Equal(t, 1, stats["connected_servers"])
	assert.Equal(t, 2, stats["total_servers"])
	assert.Equal(t, 15, stats["total_tools"])
}

func TestServerAdapter_GetUpstreamStats_APIError(t *testing.T) {
	mock := NewMockClient()
	mock.serversErr = errors.New("connection refused")
	adapter := NewServerAdapter(mock)

	stats := adapter.GetUpstreamStats()

	assert.Equal(t, 0, stats["connected_servers"])
	assert.Equal(t, 0, stats["total_servers"])
	assert.Equal(t, 0, stats["total_tools"])
}

// =============================================================================
// ServerAdapter.GetAllServers Tests - Health Data Preservation
// =============================================================================

func TestServerAdapter_GetAllServers_PreservesHealthData(t *testing.T) {
	mock := NewMockClient()
	mock.servers = []Server{
		{
			Name:      "buildkite",
			Connected: false, // stale legacy field
			Enabled:   true,
			ToolCount: 28,
			Health: &HealthStatus{
				Level:      "healthy",
				AdminState: "enabled",
				Summary:    "Connected (28 tools)",
				Detail:     "",
				Action:     "",
			},
		},
	}
	adapter := NewServerAdapter(mock)

	result, err := adapter.GetAllServers()
	require.NoError(t, err)
	require.Len(t, result, 1)

	// Verify health is present and correct
	health, ok := result[0]["health"].(map[string]interface{})
	require.True(t, ok, "health should be a map")
	assert.Equal(t, "healthy", health["level"])
	assert.Equal(t, "enabled", health["admin_state"])
	assert.Equal(t, "Connected (28 tools)", health["summary"])
}

func TestServerAdapter_GetAllServers_NoHealth(t *testing.T) {
	mock := NewMockClient()
	mock.servers = []Server{
		{
			Name:      "legacy-server",
			Connected: true,
			Enabled:   true,
			ToolCount: 5,
			Health:    nil, // no health data
		},
	}
	adapter := NewServerAdapter(mock)

	result, err := adapter.GetAllServers()
	require.NoError(t, err)
	require.Len(t, result, 1)

	// Verify health is not present when nil
	_, ok := result[0]["health"]
	assert.False(t, ok, "health should not be present when nil")
}

func TestServerAdapter_GetAllServers_AllFieldsMapped(t *testing.T) {
	mock := NewMockClient()
	mock.servers = []Server{
		{
			Name:        "test-server",
			URL:         "http://localhost:3000",
			Command:     "node",
			Protocol:    "stdio",
			Enabled:     true,
			Quarantined: false,
			Connected:   true,
			Connecting:  false,
			ToolCount:   15,
			LastError:   "previous error",
			Status:      "ready",
			ShouldRetry: true,
			RetryCount:  2,
			LastRetry:   "2024-01-01T00:00:00Z",
			Health: &HealthStatus{
				Level:      "degraded",
				AdminState: "enabled",
				Summary:    "Token expiring soon",
				Detail:     "Token expires in 2 hours",
				Action:     "login",
			},
		},
	}
	adapter := NewServerAdapter(mock)

	result, err := adapter.GetAllServers()
	require.NoError(t, err)
	require.Len(t, result, 1)

	server := result[0]

	// Verify all fields are mapped correctly
	assert.Equal(t, "test-server", server["name"])
	assert.Equal(t, "http://localhost:3000", server["url"])
	assert.Equal(t, "node", server["command"])
	assert.Equal(t, "stdio", server["protocol"])
	assert.Equal(t, true, server["enabled"])
	assert.Equal(t, false, server["quarantined"])
	assert.Equal(t, true, server["connected"])
	assert.Equal(t, false, server["connecting"])
	assert.Equal(t, 15, server["tool_count"])
	assert.Equal(t, "previous error", server["last_error"])
	assert.Equal(t, "ready", server["status"])
	assert.Equal(t, true, server["should_retry"])
	assert.Equal(t, 2, server["retry_count"])
	assert.Equal(t, "2024-01-01T00:00:00Z", server["last_retry_time"])

	// Verify health fields
	health := server["health"].(map[string]interface{})
	assert.Equal(t, "degraded", health["level"])
	assert.Equal(t, "enabled", health["admin_state"])
	assert.Equal(t, "Token expiring soon", health["summary"])
	assert.Equal(t, "Token expires in 2 hours", health["detail"])
	assert.Equal(t, "login", health["action"])
}

func TestServerAdapter_GetAllServers_APIError(t *testing.T) {
	mock := NewMockClient()
	mock.serversErr = errors.New("connection refused")
	adapter := NewServerAdapter(mock)

	result, err := adapter.GetAllServers()
	assert.Error(t, err)
	assert.Nil(t, result)
}

// =============================================================================
// ServerAdapter.GetQuarantinedServers Tests
// =============================================================================

func TestServerAdapter_GetQuarantinedServers_FiltersCorrectly(t *testing.T) {
	mock := NewMockClient()
	mock.servers = []Server{
		{Name: "server1", Quarantined: false},
		{Name: "server2", Quarantined: true, URL: "http://localhost:3001", Protocol: "http"},
		{Name: "server3", Quarantined: true, Command: "node", Protocol: "stdio"},
		{Name: "server4", Quarantined: false},
	}
	adapter := NewServerAdapter(mock)

	result, err := adapter.GetQuarantinedServers()
	require.NoError(t, err)
	require.Len(t, result, 2)

	assert.Equal(t, "server2", result[0]["name"])
	assert.Equal(t, "server3", result[1]["name"])
}

func TestServerAdapter_GetQuarantinedServers_NoneQuarantined(t *testing.T) {
	mock := NewMockClient()
	mock.servers = []Server{
		{Name: "server1", Quarantined: false},
		{Name: "server2", Quarantined: false},
	}
	adapter := NewServerAdapter(mock)

	result, err := adapter.GetQuarantinedServers()
	require.NoError(t, err)
	assert.Empty(t, result)
}

// =============================================================================
// ServerAdapter.GetStatus Tests
// =============================================================================

func TestServerAdapter_GetStatus_Success(t *testing.T) {
	mock := NewMockClient()
	mock.info = map[string]interface{}{
		"data": map[string]interface{}{
			"listen_addr": "127.0.0.1:8080",
		},
	}
	mock.servers = []Server{
		{Name: "s1", Health: &HealthStatus{Level: "healthy"}},
		{Name: "s2", Health: &HealthStatus{Level: "unhealthy"}},
		{Name: "s3", Health: &HealthStatus{Level: "healthy"}},
	}
	adapter := NewServerAdapter(mock)

	status := adapter.GetStatus().(map[string]interface{})

	assert.Equal(t, "Running", status["phase"])
	assert.Equal(t, true, status["running"])
	assert.Equal(t, "127.0.0.1:8080", status["listen_addr"])
	assert.Equal(t, 2, status["connected_servers"])
	assert.Equal(t, 3, status["total_servers"])
}

func TestServerAdapter_GetStatus_APIError(t *testing.T) {
	mock := NewMockClient()
	mock.serversErr = errors.New("connection error")
	adapter := NewServerAdapter(mock)

	status := adapter.GetStatus().(map[string]interface{})

	assert.Equal(t, "Error", status["phase"])
	assert.Equal(t, false, status["running"])
	assert.Contains(t, status["message"], "API error")
}

// =============================================================================
// ServerAdapter.EnableServer Tests
// =============================================================================

func TestServerAdapter_EnableServer_Success(t *testing.T) {
	mock := NewMockClient()
	adapter := NewServerAdapter(mock)

	err := adapter.EnableServer("test-server", true)
	require.NoError(t, err)
	assert.True(t, mock.enabledServers["test-server"])

	err = adapter.EnableServer("test-server", false)
	require.NoError(t, err)
	assert.False(t, mock.enabledServers["test-server"])
}

func TestServerAdapter_EnableServer_Error(t *testing.T) {
	mock := NewMockClient()
	mock.enableErr = errors.New("server not found")
	adapter := NewServerAdapter(mock)

	err := adapter.EnableServer("missing-server", true)
	assert.Error(t, err)
}

// =============================================================================
// ServerAdapter.TriggerOAuthLogin Tests
// =============================================================================

func TestServerAdapter_TriggerOAuthLogin_Success(t *testing.T) {
	mock := NewMockClient()
	adapter := NewServerAdapter(mock)

	err := adapter.TriggerOAuthLogin("oauth-server")
	require.NoError(t, err)
	assert.Contains(t, mock.oauthTriggered, "oauth-server")
}

func TestServerAdapter_TriggerOAuthLogin_Error(t *testing.T) {
	mock := NewMockClient()
	mock.oauthErr = errors.New("OAuth not supported")
	adapter := NewServerAdapter(mock)

	err := adapter.TriggerOAuthLogin("oauth-server")
	assert.Error(t, err)
}

// =============================================================================
// ServerAdapter.UnquarantineServer Tests
// =============================================================================

func TestServerAdapter_UnquarantineServer_Success(t *testing.T) {
	mock := NewMockClient()
	adapter := NewServerAdapter(mock)

	err := adapter.UnquarantineServer("suspicious-server")
	require.NoError(t, err)
	assert.Contains(t, mock.unquarantinedServers, "suspicious-server")
}

func TestServerAdapter_UnquarantineServer_Error(t *testing.T) {
	mock := NewMockClient()
	mock.quarantineErr = errors.New("server not found")
	adapter := NewServerAdapter(mock)

	err := adapter.UnquarantineServer("missing-server")
	assert.Error(t, err)
}

// =============================================================================
// ServerAdapter.QuarantineServer Tests
// =============================================================================

func TestServerAdapter_QuarantineServer_Quarantine(t *testing.T) {
	mock := NewMockClient()
	adapter := NewServerAdapter(mock)

	err := adapter.QuarantineServer("test-server", true)
	require.NoError(t, err)
	assert.True(t, mock.quarantinedServers["test-server"])
}

func TestServerAdapter_QuarantineServer_Unquarantine(t *testing.T) {
	mock := NewMockClient()
	mock.quarantinedServers["test-server"] = true
	adapter := NewServerAdapter(mock)

	err := adapter.QuarantineServer("test-server", false)
	require.NoError(t, err)
	assert.Contains(t, mock.unquarantinedServers, "test-server")
	_, stillQuarantined := mock.quarantinedServers["test-server"]
	assert.False(t, stillQuarantined)
}

func TestServerAdapter_QuarantineServer_Error(t *testing.T) {
	mock := NewMockClient()
	mock.quarantineErr = errors.New("server not found")
	adapter := NewServerAdapter(mock)

	err := adapter.QuarantineServer("missing-server", true)
	assert.Error(t, err)
}

// =============================================================================
// Integration Test: Health Data Flow Verification
// =============================================================================

func TestHealthDataFlow_EndToEnd(t *testing.T) {
	// This test verifies the complete data flow from API response to adapter output
	// as documented in Spec 013

	mock := NewMockClient()
	mock.servers = []Server{
		{
			Name:      "buildkite",
			Connected: false, // Legacy field shows disconnected
			Enabled:   true,
			ToolCount: 28,
			Health: &HealthStatus{
				Level:      "healthy", // Health shows connected - this is source of truth
				AdminState: "enabled",
				Summary:    "Connected (28 tools)",
			},
		},
		{
			Name:      "github",
			Connected: true,
			Enabled:   true,
			ToolCount: 15,
			Health: &HealthStatus{
				Level:      "unhealthy", // Health shows unhealthy
				AdminState: "enabled",
				Summary:    "Authentication required",
				Action:     "login",
			},
		},
	}
	adapter := NewServerAdapter(mock)

	// Test GetUpstreamStats uses health as source of truth
	stats := adapter.GetUpstreamStats()
	assert.Equal(t, 1, stats["connected_servers"], "Should count servers with health.level='healthy'")

	// Test GetAllServers preserves health data
	servers, err := adapter.GetAllServers()
	require.NoError(t, err)
	require.Len(t, servers, 2)

	// Verify buildkite health is preserved
	buildkiteHealth := servers[0]["health"].(map[string]interface{})
	assert.Equal(t, "healthy", buildkiteHealth["level"])

	// Verify github health with action is preserved
	githubHealth := servers[1]["health"].(map[string]interface{})
	assert.Equal(t, "unhealthy", githubHealth["level"])
	assert.Equal(t, "login", githubHealth["action"])

	// Test GetStatus counts correctly using health
	status := adapter.GetStatus().(map[string]interface{})
	assert.Equal(t, 1, status["connected_servers"], "Status should use health.level for connected count")
}
