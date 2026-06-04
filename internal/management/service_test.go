package management

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
)

// mockEventEmitter implements the EventEmitter interface for testing
type mockEventEmitter struct {
	emittedEvents []eventRecord
}

type eventRecord struct {
	reason string
	extra  map[string]any
}

func (m *mockEventEmitter) EmitServersChanged(reason string, extra map[string]any) {
	m.emittedEvents = append(m.emittedEvents, eventRecord{reason: reason, extra: extra})
}

// T017: Unit test for checkWriteGates
func TestCheckWriteGates(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	tests := []struct {
		name              string
		disableManagement bool
		readOnlyMode      bool
		expectError       bool
		errorContains     string
	}{
		{
			name:              "gates open - allowed",
			disableManagement: false,
			readOnlyMode:      false,
			expectError:       false,
		},
		{
			name:              "disable_management blocks writes",
			disableManagement: true,
			readOnlyMode:      false,
			expectError:       true,
			errorContains:     "disable_management=true",
		},
		{
			name:              "read_only_mode blocks writes",
			disableManagement: false,
			readOnlyMode:      true,
			expectError:       true,
			errorContains:     "read_only_mode=true",
		},
		{
			name:              "both gates block writes",
			disableManagement: true,
			readOnlyMode:      true,
			expectError:       true,
			errorContains:     "disable_management=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				DisableManagement: tt.disableManagement,
				ReadOnlyMode:      tt.readOnlyMode,
			}

			svc := NewService(nil, cfg, "", &mockEventEmitter{}, nil, logger).(*service)
			err := svc.checkWriteGates()

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// T018: Unit test for ListServers
func TestListServers(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	cfg := &config.Config{}
	emitter := &mockEventEmitter{}

	t.Run("success with servers", func(t *testing.T) {
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":          "server1",
				"name":        "test-server-1",
				"enabled":     true,
				"connected":   true,
				"quarantined": false,
			},
			{
				"id":          "server2",
				"name":        "test-server-2",
				"enabled":     false,
				"connected":   false,
				"quarantined": true,
			},
		}

		svc := NewService(runtime, cfg, "", emitter, nil, logger)
		servers, stats, err := svc.ListServers(context.Background())

		require.NoError(t, err)
		assert.Len(t, servers, 2)
		assert.Equal(t, 2, stats.TotalServers)
		assert.Equal(t, 1, stats.ConnectedServers)
		assert.Equal(t, 1, stats.QuarantinedServers)
	})

	// MCP-901: source registry provenance is projected onto contracts.Server so
	// the approval/quarantine view (and the SSE servers.changed embed, which
	// shares this projection) can show a server's origin.
	t.Run("source registry provenance projected", func(t *testing.T) {
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":                         "everything",
				"name":                       "everything",
				"enabled":                    true,
				"source_registry_id":         "modelcontextprotocol",
				"source_registry_provenance": "custom",
			},
			{
				"id":      "manual",
				"name":    "manual",
				"enabled": true,
			},
		}

		svc := NewService(runtime, cfg, "", emitter, nil, logger)
		servers, _, err := svc.ListServers(context.Background())
		require.NoError(t, err)
		require.Len(t, servers, 2)

		byName := map[string]*contracts.Server{}
		for _, s := range servers {
			byName[s.Name] = s
		}
		require.Contains(t, byName, "everything")
		assert.Equal(t, "modelcontextprotocol", byName["everything"].SourceRegistryID)
		assert.Equal(t, "custom", byName["everything"].SourceRegistryProvenance)

		require.Contains(t, byName, "manual")
		assert.Empty(t, byName["manual"].SourceRegistryID)
		assert.Empty(t, byName["manual"].SourceRegistryProvenance)
	})

	// T094: Test that TotalTools only counts enabled servers' tools (Issue #285 fix)
	t.Run("TotalTools excludes disabled servers", func(t *testing.T) {
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":          "server1",
				"name":        "enabled-server",
				"enabled":     true,
				"connected":   true,
				"quarantined": false,
				"tool_count":  30,
			},
			{
				"id":          "server2",
				"name":        "disabled-server",
				"enabled":     false,
				"connected":   false,
				"quarantined": false,
				"tool_count":  20, // This should NOT be counted in TotalTools
			},
			{
				"id":          "server3",
				"name":        "another-enabled-server",
				"enabled":     true,
				"connected":   true,
				"quarantined": false,
				"tool_count":  10,
			},
		}

		svc := NewService(runtime, cfg, "", emitter, nil, logger)
		servers, stats, err := svc.ListServers(context.Background())

		require.NoError(t, err)
		assert.Len(t, servers, 3)
		assert.Equal(t, 3, stats.TotalServers)
		assert.Equal(t, 2, stats.ConnectedServers)

		// CRITICAL: TotalTools should only count enabled servers (30 + 10 = 40)
		// If TotalTools is 60 (30 + 20 + 10), the fix is broken
		assert.Equal(t, 40, stats.TotalTools, "TotalTools should only count enabled servers' tools")

		// Verify individual server tool counts are preserved
		for _, srv := range servers {
			switch srv.Name {
			case "enabled-server":
				assert.Equal(t, 30, srv.ToolCount)
			case "disabled-server":
				assert.Equal(t, 20, srv.ToolCount)
			case "another-enabled-server":
				assert.Equal(t, 10, srv.ToolCount)
			}
		}
	})

	t.Run("runtime error", func(t *testing.T) {
		runtime := newMockRuntime()
		runtime.getAllError = fmt.Errorf("runtime error")

		svc := NewService(runtime, cfg, "", emitter, nil, logger)
		servers, stats, err := svc.ListServers(context.Background())

		assert.Error(t, err)
		assert.Nil(t, servers)
		assert.Nil(t, stats)
	})

	t.Run("server with OAuth config", func(t *testing.T) {
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":            "oauth-server",
				"name":          "slack",
				"enabled":       true,
				"connected":     false,
				"quarantined":   false,
				"authenticated": false,
				"last_error":    "OAuth provider requires 'resource' parameter",
				"oauth": map[string]interface{}{
					"client_id": "test-client-123",
					"scopes":    []interface{}{"read", "write"},
					"auth_url":  "https://oauth.example.com/authorize",
					"token_url": "https://oauth.example.com/token",
				},
			},
		}

		svc := NewService(runtime, cfg, "", emitter, nil, logger)
		servers, stats, err := svc.ListServers(context.Background())

		require.NoError(t, err)
		assert.Len(t, servers, 1)
		assert.Equal(t, 1, stats.TotalServers)

		// Verify OAuth config was extracted correctly
		server := servers[0]
		assert.Equal(t, "slack", server.Name)
		assert.Equal(t, "OAuth provider requires 'resource' parameter", server.LastError)
		assert.False(t, server.Authenticated)

		require.NotNil(t, server.OAuth, "OAuth config should be present")
		assert.Equal(t, "test-client-123", server.OAuth.ClientID)
		assert.Equal(t, []string{"read", "write"}, server.OAuth.Scopes)
		assert.Equal(t, "https://oauth.example.com/authorize", server.OAuth.AuthURL)
		assert.Equal(t, "https://oauth.example.com/token", server.OAuth.TokenURL)
	})

	// ListServers must populate SecurityScan via the wired enricher so that
	// REST and the SSE servers.changed embed (which both call ListServers)
	// share one enrichment site. Without parity, mergeServers on the Web UI
	// strips security_scan from each store-side server on every SSE delivery.
	t.Run("populates SecurityScan via enricher", func(t *testing.T) {
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{"id": "alpha", "name": "alpha", "enabled": true, "connected": true},
			{"id": "beta", "name": "beta", "enabled": true, "connected": true},
		}

		enricher := &fakeScanEnricher{
			byServer: map[string]*contracts.SecurityScanSummary{
				"alpha": {RiskScore: 42, Status: "warnings"},
				// beta intentionally missing — must stay nil.
			},
		}
		svc := NewService(runtime, cfg, "", emitter, nil, logger)
		svc.SetScanSummaryEnricher(enricher)

		servers, _, err := svc.ListServers(context.Background())
		require.NoError(t, err)
		require.Len(t, servers, 2)
		require.NotNil(t, servers[0].SecurityScan, "alpha must carry its scan summary")
		assert.Equal(t, "warnings", servers[0].SecurityScan.Status)
		assert.Equal(t, 42, servers[0].SecurityScan.RiskScore)
		assert.Nil(t, servers[1].SecurityScan, "beta must stay nil — enricher returned nil")
		assert.Equal(t, []string{"alpha", "beta"}, enricher.calls, "enricher called once per server")
	})

	t.Run("nil enricher is a no-op", func(t *testing.T) {
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{"id": "alpha", "name": "alpha", "enabled": true},
		}
		svc := NewService(runtime, cfg, "", emitter, nil, logger)
		// SetScanSummaryEnricher not called — must still succeed.
		servers, _, err := svc.ListServers(context.Background())
		require.NoError(t, err)
		require.Len(t, servers, 1)
		assert.Nil(t, servers[0].SecurityScan)
	})

	// Verifies that headers and env extracted from the runtime map make it
	// onto contracts.Server in both the typed (map[string]string, in-process
	// StateView path) and generic (map[string]interface{}, JSON-round-trip
	// path) shapes. Without this, the Web UI / macOS Edit Config screens see
	// no headers and cannot round-trip configuration.
	t.Run("headers and env (typed shape)", func(t *testing.T) {
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":       "synapbus",
				"name":     "synapbus",
				"protocol": "streamable-http",
				"enabled":  true,
				"headers": map[string]string{
					"Authorization": "Bearer abc123",
					"X-Trace":       "on",
				},
				"env": map[string]string{
					"LOG_LEVEL": "debug",
				},
			},
		}

		svc := NewService(runtime, cfg, "", emitter, nil, logger)
		servers, _, err := svc.ListServers(context.Background())

		require.NoError(t, err)
		require.Len(t, servers, 1)
		s := servers[0]
		assert.Equal(t, "Bearer abc123", s.Headers["Authorization"])
		assert.Equal(t, "on", s.Headers["X-Trace"])
		assert.Equal(t, "debug", s.Env["LOG_LEVEL"])
	})

	t.Run("headers and env (generic shape from JSON round-trip)", func(t *testing.T) {
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":       "synapbus",
				"name":     "synapbus",
				"protocol": "streamable-http",
				"enabled":  true,
				"headers": map[string]interface{}{
					"Authorization": "Bearer abc123",
				},
				"env": map[string]interface{}{
					"LOG_LEVEL": "debug",
				},
			},
		}

		svc := NewService(runtime, cfg, "", emitter, nil, logger)
		servers, _, err := svc.ListServers(context.Background())

		require.NoError(t, err)
		require.Len(t, servers, 1)
		s := servers[0]
		assert.Equal(t, "Bearer abc123", s.Headers["Authorization"])
		assert.Equal(t, "debug", s.Env["LOG_LEVEL"])
	})

	t.Run("missing headers and env stay nil", func(t *testing.T) {
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{
				"id":       "plain",
				"name":     "plain",
				"protocol": "stdio",
				"enabled":  true,
			},
		}

		svc := NewService(runtime, cfg, "", emitter, nil, logger)
		servers, _, err := svc.ListServers(context.Background())

		require.NoError(t, err)
		require.Len(t, servers, 1)
		assert.Nil(t, servers[0].Headers)
		assert.Nil(t, servers[0].Env)
	})
}

// fakeScanEnricher records which servers were asked about and returns a
// canned summary per server (nil when the server isn't in byServer).
type fakeScanEnricher struct {
	byServer map[string]*contracts.SecurityScanSummary
	calls    []string
}

func (f *fakeScanEnricher) GetSecurityScanSummary(_ context.Context, serverName string) *contracts.SecurityScanSummary {
	f.calls = append(f.calls, serverName)
	return f.byServer[serverName]
}

// T019: Unit test for EnableServer
func TestEnableServer(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	t.Run("blocks when disable_management is true", func(t *testing.T) {
		cfg := &config.Config{DisableManagement: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, "", emitter, nil, logger)

		err := svc.EnableServer(context.Background(), "test-server", true)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "disable_management=true")
		assert.Empty(t, emitter.emittedEvents, "No events should be emitted when blocked")
	})

	t.Run("blocks when read_only_mode is true", func(t *testing.T) {
		cfg := &config.Config{ReadOnlyMode: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, "", emitter, nil, logger)

		err := svc.EnableServer(context.Background(), "test-server", true)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "read_only_mode=true")
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("success when gates are open", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		svc := NewService(runtime, cfg, "", emitter, nil, logger)

		err := svc.EnableServer(context.Background(), "test-server", true)

		assert.NoError(t, err)
		assert.Len(t, runtime.enableCalls, 1)
		assert.Equal(t, "test-server", runtime.enableCalls[0].serverName)
		assert.True(t, runtime.enableCalls[0].enabled)
	})
}

// T020: Unit test for RestartServer
func TestRestartServer(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	t.Run("blocks when disable_management is true", func(t *testing.T) {
		cfg := &config.Config{DisableManagement: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, "", emitter, nil, logger)

		err := svc.RestartServer(context.Background(), "test-server")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "disable_management=true")
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("blocks when read_only_mode is true", func(t *testing.T) {
		cfg := &config.Config{ReadOnlyMode: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, "", emitter, nil, logger)

		err := svc.RestartServer(context.Background(), "test-server")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "read_only_mode=true")
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("success when gates are open", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		svc := NewService(runtime, cfg, "", emitter, nil, logger)

		err := svc.RestartServer(context.Background(), "test-server")

		assert.NoError(t, err)
		assert.Len(t, runtime.restartCalls, 1)
		assert.Equal(t, "test-server", runtime.restartCalls[0])
	})
}

// mockRuntimeOperations implements RuntimeOperations for testing
type mockRuntimeOperations struct {
	servers      []map[string]interface{}
	enableCalls  []enableCall
	restartCalls []string
	enableError  error
	restartError error
	getAllError  error
	failOnServer string // If set, only fail operations on this specific server
	mu           sync.Mutex
}

type enableCall struct {
	serverName string
	enabled    bool
}

func newMockRuntime() *mockRuntimeOperations {
	return &mockRuntimeOperations{
		servers:      []map[string]interface{}{},
		enableCalls:  []enableCall{},
		restartCalls: []string{},
	}
}

func (m *mockRuntimeOperations) GetAllServers() ([]map[string]interface{}, error) {
	if m.getAllError != nil {
		return nil, m.getAllError
	}
	return m.servers, nil
}

func (m *mockRuntimeOperations) EnableServer(serverName string, enabled bool) error {
	m.mu.Lock()
	m.enableCalls = append(m.enableCalls, enableCall{serverName: serverName, enabled: enabled})
	m.mu.Unlock()
	if m.failOnServer != "" && serverName == m.failOnServer {
		return m.enableError
	}
	return nil
}

func (m *mockRuntimeOperations) BulkEnableServers(serverNames []string, enabled bool) (map[string]error, error) {
	errs := make(map[string]error)
	for _, name := range serverNames {
		if m.failOnServer != "" && name == m.failOnServer {
			errs[name] = m.enableError
			continue
		}
		m.mu.Lock()
		m.enableCalls = append(m.enableCalls, enableCall{serverName: name, enabled: enabled})
		m.mu.Unlock()
	}
	return errs, nil
}

func (m *mockRuntimeOperations) RestartServer(serverName string) error {
	m.mu.Lock()
	m.restartCalls = append(m.restartCalls, serverName)
	m.mu.Unlock()
	if m.failOnServer != "" && serverName == m.failOnServer {
		return m.restartError
	}
	return nil
}

// GetServerTools implements RuntimeOperations for testing
func (m *mockRuntimeOperations) GetServerTools(serverName string) ([]map[string]interface{}, error) {
	// Return mock tools data or error for testing
	if m.failOnServer != "" && serverName == m.failOnServer {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}
	if serverName == "" {
		return nil, fmt.Errorf("server name required")
	}
	// Return sample tools for valid servers
	return []map[string]interface{}{
		{"name": "test_tool", "description": "A test tool"},
	}, nil
}

// TriggerOAuthLogin implements RuntimeOperations for testing
func (m *mockRuntimeOperations) TriggerOAuthLogin(serverName string) error {
	if m.failOnServer != "" && serverName == m.failOnServer {
		return fmt.Errorf("OAuth start failed")
	}
	if serverName == "" {
		return fmt.Errorf("server name required")
	}
	return nil
}

// TriggerOAuthLoginQuick implements RuntimeOperations for testing (Spec 020 fix)
func (m *mockRuntimeOperations) TriggerOAuthLoginQuick(serverName string) (*core.OAuthStartResult, error) {
	if m.failOnServer != "" && serverName == m.failOnServer {
		return nil, fmt.Errorf("OAuth start failed")
	}
	if serverName == "" {
		return nil, fmt.Errorf("server name required")
	}
	return &core.OAuthStartResult{
		BrowserOpened: true,
		AuthURL:       "https://example.com/oauth/authorize",
		CorrelationID: "test-correlation-id",
	}, nil
}

// TriggerOAuthLogout implements RuntimeOperations for testing
func (m *mockRuntimeOperations) TriggerOAuthLogout(serverName string) error {
	if m.failOnServer != "" && serverName == m.failOnServer {
		return fmt.Errorf("OAuth logout failed")
	}
	if serverName == "" {
		return fmt.Errorf("server name required")
	}
	return nil
}

// RefreshOAuthToken implements RuntimeOperations for testing
func (m *mockRuntimeOperations) RefreshOAuthToken(serverName string) error {
	if m.failOnServer != "" && serverName == m.failOnServer {
		return fmt.Errorf("OAuth refresh failed")
	}
	if serverName == "" {
		return fmt.Errorf("server name required")
	}
	return nil
}

// T065: Unit test for RestartAll() - verify sequential execution and partial failure handling
func TestRestartAll(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	t.Run("blocks when disable_management is true", func(t *testing.T) {
		cfg := &config.Config{DisableManagement: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, "", emitter, nil, logger)

		result, err := svc.RestartAll(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "disable_management=true")
		assert.Nil(t, result)
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("blocks when read_only_mode is true", func(t *testing.T) {
		cfg := &config.Config{ReadOnlyMode: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, "", emitter, nil, logger)

		result, err := svc.RestartAll(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "read_only_mode=true")
		assert.Nil(t, result)
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("success with multiple servers", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{"name": "server1"},
			{"name": "server2"},
			{"name": "server3"},
		}
		svc := NewService(runtime, cfg, "", emitter, nil, logger)

		result, err := svc.RestartAll(context.Background())

		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 3, result.Total)
		assert.Equal(t, 3, result.Successful)
		assert.Equal(t, 0, result.Failed)
		assert.Empty(t, result.Errors)
		assert.Len(t, runtime.restartCalls, 3)
		assert.Contains(t, runtime.restartCalls, "server1")
		assert.Contains(t, runtime.restartCalls, "server2")
		assert.Contains(t, runtime.restartCalls, "server3")
	})

	t.Run("partial failure - some servers fail", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{"name": "server1"},
			{"name": "server2"},
			{"name": "server3"},
		}
		// Configure mock to fail on server2
		runtime.restartError = fmt.Errorf("restart failed")
		runtime.failOnServer = "server2"
		svc := NewService(runtime, cfg, "", emitter, nil, logger)

		result, err := svc.RestartAll(context.Background())

		assert.NoError(t, err) // Bulk operation doesn't fail, returns partial results
		require.NotNil(t, result)
		assert.Equal(t, 3, result.Total)
		assert.Equal(t, 2, result.Successful)
		assert.Equal(t, 1, result.Failed)
		assert.Len(t, result.Errors, 1)
		assert.Contains(t, result.Errors["server2"], "restart failed")
	})

	t.Run("empty server list", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{}
		svc := NewService(runtime, cfg, "", emitter, nil, logger)

		result, err := svc.RestartAll(context.Background())

		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 0, result.Total)
		assert.Equal(t, 0, result.Successful)
		assert.Equal(t, 0, result.Failed)
		assert.Empty(t, result.Errors)
	})
}

// T066: Unit test for EnableAll() - verify sequential execution
func TestEnableAll(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	t.Run("blocks when disable_management is true", func(t *testing.T) {
		cfg := &config.Config{DisableManagement: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, "", emitter, nil, logger)

		result, err := svc.EnableAll(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "disable_management=true")
		assert.Nil(t, result)
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("blocks when read_only_mode is true", func(t *testing.T) {
		cfg := &config.Config{ReadOnlyMode: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, "", emitter, nil, logger)

		result, err := svc.EnableAll(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "read_only_mode=true")
		assert.Nil(t, result)
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("success with multiple servers", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{"name": "server1"},
			{"name": "server2"},
			{"name": "server3"},
		}
		svc := NewService(runtime, cfg, "", emitter, nil, logger)

		result, err := svc.EnableAll(context.Background())

		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 3, result.Total)
		assert.Equal(t, 3, result.Successful)
		assert.Equal(t, 0, result.Failed)
		assert.Empty(t, result.Errors)
		assert.Len(t, runtime.enableCalls, 3)
		// Verify all calls set enabled=true
		for _, call := range runtime.enableCalls {
			assert.True(t, call.enabled)
		}
	})

	t.Run("partial failure - some servers fail", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{"name": "server1"},
			{"name": "server2"},
			{"name": "server3"},
		}
		runtime.enableError = fmt.Errorf("enable failed")
		runtime.failOnServer = "server2"
		svc := NewService(runtime, cfg, "", emitter, nil, logger)

		result, err := svc.EnableAll(context.Background())

		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 3, result.Total)
		assert.Equal(t, 2, result.Successful)
		assert.Equal(t, 1, result.Failed)
		assert.Len(t, result.Errors, 1)
		assert.Contains(t, result.Errors["server2"], "enable failed")
	})
}

// T067: Unit test for DisableAll() - verify sequential execution
func TestDisableAll(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	t.Run("blocks when disable_management is true", func(t *testing.T) {
		cfg := &config.Config{DisableManagement: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, "", emitter, nil, logger)

		result, err := svc.DisableAll(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "disable_management=true")
		assert.Nil(t, result)
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("blocks when read_only_mode is true", func(t *testing.T) {
		cfg := &config.Config{ReadOnlyMode: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, "", emitter, nil, logger)

		result, err := svc.DisableAll(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "read_only_mode=true")
		assert.Nil(t, result)
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("success with multiple servers", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{"name": "server1"},
			{"name": "server2"},
			{"name": "server3"},
		}
		svc := NewService(runtime, cfg, "", emitter, nil, logger)

		result, err := svc.DisableAll(context.Background())

		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 3, result.Total)
		assert.Equal(t, 3, result.Successful)
		assert.Equal(t, 0, result.Failed)
		assert.Empty(t, result.Errors)
		assert.Len(t, runtime.enableCalls, 3)
		// Verify all calls set enabled=false
		for _, call := range runtime.enableCalls {
			assert.False(t, call.enabled)
		}
	})

	t.Run("partial failure - some servers fail", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		runtime.servers = []map[string]interface{}{
			{"name": "server1"},
			{"name": "server2"},
			{"name": "server3"},
		}
		runtime.enableError = fmt.Errorf("disable failed")
		runtime.failOnServer = "server3"
		svc := NewService(runtime, cfg, "", emitter, nil, logger)

		result, err := svc.DisableAll(context.Background())

		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, 3, result.Total)
		assert.Equal(t, 2, result.Successful)
		assert.Equal(t, 1, result.Failed)
		assert.Len(t, result.Errors, 1)
		assert.Contains(t, result.Errors["server3"], "disable failed")
	})
}

// T006: Unit test for GetServerTools with valid server name
func TestGetServerTools_ValidServer(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	cfg := &config.Config{}
	emitter := &mockEventEmitter{}
	runtime := newMockRuntime()

	svc := NewService(runtime, cfg, "", emitter, nil, logger)
	tools, err := svc.GetServerTools(context.Background(), "test-server")

	require.NoError(t, err)
	require.NotNil(t, tools)
	assert.Len(t, tools, 1)
	assert.Equal(t, "test_tool", tools[0]["name"])
}

// T007: Unit test for GetServerTools with empty server name
func TestGetServerTools_EmptyServerName(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	cfg := &config.Config{}
	emitter := &mockEventEmitter{}
	runtime := newMockRuntime()

	svc := NewService(runtime, cfg, "", emitter, nil, logger)
	tools, err := svc.GetServerTools(context.Background(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "server name required")
	assert.Nil(t, tools)
}

// T008: Unit test for GetServerTools with nonexistent server
func TestGetServerTools_NonexistentServer(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	cfg := &config.Config{}
	emitter := &mockEventEmitter{}
	runtime := newMockRuntime()
	runtime.failOnServer = "nonexistent"

	svc := NewService(runtime, cfg, "", emitter, nil, logger)
	tools, err := svc.GetServerTools(context.Background(), "nonexistent")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "server not found")
	assert.Nil(t, tools)
}

// T009: Unit test for TriggerOAuthLogin with valid server
func TestTriggerOAuthLogin_ValidServer(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	cfg := &config.Config{}
	emitter := &mockEventEmitter{}
	runtime := newMockRuntime()

	svc := NewService(runtime, cfg, "", emitter, nil, logger)
	err := svc.TriggerOAuthLogin(context.Background(), "test-server")

	require.NoError(t, err)
}

// T010: Unit test for TriggerOAuthLogin with disable_management enabled
func TestTriggerOAuthLogin_DisableManagement(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	cfg := &config.Config{DisableManagement: true}
	emitter := &mockEventEmitter{}
	runtime := newMockRuntime()

	svc := NewService(runtime, cfg, "", emitter, nil, logger)
	err := svc.TriggerOAuthLogin(context.Background(), "test-server")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "disable_management=true")
	assert.Empty(t, emitter.emittedEvents, "No events should be emitted when blocked")
}

// T011: Unit test for TriggerOAuthLogin with read_only enabled
func TestTriggerOAuthLogin_ReadOnly(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	cfg := &config.Config{ReadOnlyMode: true}
	emitter := &mockEventEmitter{}
	runtime := newMockRuntime()

	svc := NewService(runtime, cfg, "", emitter, nil, logger)
	err := svc.TriggerOAuthLogin(context.Background(), "test-server")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read_only_mode=true")
	assert.Empty(t, emitter.emittedEvents)
}

// T012: Unit test for TriggerOAuthLogin with empty server name
func TestTriggerOAuthLogin_EmptyServerName(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	cfg := &config.Config{}
	emitter := &mockEventEmitter{}
	runtime := newMockRuntime()

	svc := NewService(runtime, cfg, "", emitter, nil, logger)
	err := svc.TriggerOAuthLogin(context.Background(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "server name required")
}

func (m *mockRuntimeOperations) GetToolApproval(serverName, toolName string) (*storage.ToolApprovalRecord, error) {
	return nil, fmt.Errorf("not found")
}
