package management

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"mcpproxy-go/internal/config"
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

			svc := NewService(nil, cfg, &mockEventEmitter{}, nil, logger).(*service)
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

		svc := NewService(runtime, cfg, emitter, nil, logger)
		servers, stats, err := svc.ListServers(context.Background())

		require.NoError(t, err)
		assert.Len(t, servers, 2)
		assert.Equal(t, 2, stats.TotalServers)
		assert.Equal(t, 1, stats.ConnectedServers)
		assert.Equal(t, 1, stats.QuarantinedServers)
	})

	t.Run("runtime error", func(t *testing.T) {
		runtime := newMockRuntime()
		runtime.getAllError = fmt.Errorf("runtime error")

		svc := NewService(runtime, cfg, emitter, nil, logger)
		servers, stats, err := svc.ListServers(context.Background())

		assert.Error(t, err)
		assert.Nil(t, servers)
		assert.Nil(t, stats)
	})
}

// T019: Unit test for EnableServer
func TestEnableServer(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	t.Run("blocks when disable_management is true", func(t *testing.T) {
		cfg := &config.Config{DisableManagement: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, emitter, nil, logger)

		err := svc.EnableServer(context.Background(), "test-server", true)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "disable_management=true")
		assert.Empty(t, emitter.emittedEvents, "No events should be emitted when blocked")
	})

	t.Run("blocks when read_only_mode is true", func(t *testing.T) {
		cfg := &config.Config{ReadOnlyMode: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, emitter, nil, logger)

		err := svc.EnableServer(context.Background(), "test-server", true)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "read_only_mode=true")
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("success when gates are open", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		svc := NewService(runtime, cfg, emitter, nil, logger)

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
		svc := NewService(nil, cfg, emitter, nil, logger)

		err := svc.RestartServer(context.Background(), "test-server")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "disable_management=true")
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("blocks when read_only_mode is true", func(t *testing.T) {
		cfg := &config.Config{ReadOnlyMode: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, emitter, nil, logger)

		err := svc.RestartServer(context.Background(), "test-server")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "read_only_mode=true")
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("success when gates are open", func(t *testing.T) {
		cfg := &config.Config{}
		emitter := &mockEventEmitter{}
		runtime := newMockRuntime()
		svc := NewService(runtime, cfg, emitter, nil, logger)

		err := svc.RestartServer(context.Background(), "test-server")

		assert.NoError(t, err)
		assert.Len(t, runtime.restartCalls, 1)
		assert.Equal(t, "test-server", runtime.restartCalls[0])
	})
}

// mockRuntimeOperations implements RuntimeOperations for testing
type mockRuntimeOperations struct {
	servers       []map[string]interface{}
	enableCalls   []enableCall
	restartCalls  []string
	enableError   error
	restartError  error
	getAllError   error
	failOnServer  string // If set, only fail operations on this specific server
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
	m.enableCalls = append(m.enableCalls, enableCall{serverName: serverName, enabled: enabled})
	if m.failOnServer != "" && serverName == m.failOnServer {
		return m.enableError
	}
	return nil
}

func (m *mockRuntimeOperations) RestartServer(serverName string) error {
	m.restartCalls = append(m.restartCalls, serverName)
	if m.failOnServer != "" && serverName == m.failOnServer {
		return m.restartError
	}
	return nil
}

// T065: Unit test for RestartAll() - verify sequential execution and partial failure handling
func TestRestartAll(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	t.Run("blocks when disable_management is true", func(t *testing.T) {
		cfg := &config.Config{DisableManagement: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, emitter, nil, logger)

		result, err := svc.RestartAll(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "disable_management=true")
		assert.Nil(t, result)
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("blocks when read_only_mode is true", func(t *testing.T) {
		cfg := &config.Config{ReadOnlyMode: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, emitter, nil, logger)

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
		svc := NewService(runtime, cfg, emitter, nil, logger)

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
		svc := NewService(runtime, cfg, emitter, nil, logger)

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
		svc := NewService(runtime, cfg, emitter, nil, logger)

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
		svc := NewService(nil, cfg, emitter, nil, logger)

		result, err := svc.EnableAll(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "disable_management=true")
		assert.Nil(t, result)
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("blocks when read_only_mode is true", func(t *testing.T) {
		cfg := &config.Config{ReadOnlyMode: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, emitter, nil, logger)

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
		svc := NewService(runtime, cfg, emitter, nil, logger)

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
		svc := NewService(runtime, cfg, emitter, nil, logger)

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
		svc := NewService(nil, cfg, emitter, nil, logger)

		result, err := svc.DisableAll(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "disable_management=true")
		assert.Nil(t, result)
		assert.Empty(t, emitter.emittedEvents)
	})

	t.Run("blocks when read_only_mode is true", func(t *testing.T) {
		cfg := &config.Config{ReadOnlyMode: true}
		emitter := &mockEventEmitter{}
		svc := NewService(nil, cfg, emitter, nil, logger)

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
		svc := NewService(runtime, cfg, emitter, nil, logger)

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
		svc := NewService(runtime, cfg, emitter, nil, logger)

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
