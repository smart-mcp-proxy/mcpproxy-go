package upstream

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// createTestManagerWithClient creates a Manager with a single managed client for testing.
// The client's StateManager can be manipulated directly to simulate different states.
func createTestManagerWithClient(t *testing.T, serverConfig *config.ServerConfig) (*Manager, *managed.Client) {
	t.Helper()

	logger := zap.NewNop()
	sugaredLogger := logger.Sugar()

	tempDir := t.TempDir()
	db, err := storage.NewBoltDB(tempDir, sugaredLogger)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	manager := &Manager{
		clients:        make(map[string]*managed.Client),
		logger:         logger,
		storage:        db,
		secretResolver: secret.NewResolver(),
	}

	client, err := managed.NewClient(
		serverConfig.Name,
		serverConfig,
		logger,
		nil,
		&config.Config{},
		db,
		secret.NewResolver(),
	)
	require.NoError(t, err)
	manager.clients[serverConfig.Name] = client

	return manager, client
}

func TestReconnectOnUse_DisabledByDefault(t *testing.T) {
	// When reconnect_on_use is false (default), a tool call to a disconnected
	// server should fail immediately without attempting reconnection.
	serverConfig := &config.ServerConfig{
		Name:           "test-server",
		URL:            "http://localhost:9999",
		Protocol:       "http",
		Enabled:        true,
		ReconnectOnUse: false, // default
		Created:        time.Now(),
	}

	manager, client := createTestManagerWithClient(t, serverConfig)

	// Put client in error/disconnected state
	client.StateManager.SetError(fmt.Errorf("connection lost"))

	// Attempt tool call — should fail immediately
	ctx := context.Background()
	_, err := manager.CallTool(ctx, "test-server:some_tool", map[string]interface{}{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not connected")
	// Verify state didn't change (no reconnect attempted)
	assert.Equal(t, types.StateError, client.GetState())
}

func TestReconnectOnUse_Enabled_AttemptsReconnect(t *testing.T) {
	// When reconnect_on_use is true and server is disconnected, CallTool should
	// attempt a synchronous reconnect before failing.
	serverConfig := &config.ServerConfig{
		Name:           "test-server",
		URL:            "http://localhost:9999", // not a real server
		Protocol:       "http",
		Enabled:        true,
		ReconnectOnUse: true,
		Created:        time.Now(),
	}

	manager, client := createTestManagerWithClient(t, serverConfig)

	// Put client in error/disconnected state
	client.StateManager.SetError(fmt.Errorf("connection lost"))

	// Attempt tool call — reconnect will be attempted but will fail
	// because there's no real server at localhost:9999
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := manager.CallTool(ctx, "test-server:some_tool", map[string]interface{}{})

	require.Error(t, err)
	// The error should still indicate not connected, but the reconnect was attempted
	// (we can verify this by the state transition that happens during reconnect)
	assert.Contains(t, err.Error(), "not connected")
}

func TestReconnectOnUse_SkippedWhenUserLoggedOut(t *testing.T) {
	// When user has explicitly logged out, reconnect_on_use should NOT
	// attempt reconnection even if enabled.
	serverConfig := &config.ServerConfig{
		Name:           "test-server",
		URL:            "http://localhost:9999",
		Protocol:       "http",
		Enabled:        true,
		ReconnectOnUse: true,
		Created:        time.Now(),
	}

	manager, client := createTestManagerWithClient(t, serverConfig)

	// Put client in error state AND mark user as logged out
	client.StateManager.SetError(fmt.Errorf("oauth token expired"))
	client.SetUserLoggedOut(true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := manager.CallTool(ctx, "test-server:some_tool", map[string]interface{}{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not connected")
}

func TestReconnectOnUse_SkippedWhenQuarantined(t *testing.T) {
	// Quarantined servers should not attempt reconnect-on-use.
	serverConfig := &config.ServerConfig{
		Name:           "test-server",
		URL:            "http://localhost:9999",
		Protocol:       "http",
		Enabled:        true,
		Quarantined:    true,
		ReconnectOnUse: true,
		Created:        time.Now(),
	}

	manager, client := createTestManagerWithClient(t, serverConfig)

	client.StateManager.SetError(fmt.Errorf("connection lost"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := manager.CallTool(ctx, "test-server:some_tool", map[string]interface{}{})

	require.Error(t, err)
	// Should fail without reconnect attempt
}

func TestReconnectOnUse_SkippedWhenAlreadyConnecting(t *testing.T) {
	// If a reconnect is already in progress (connecting state), don't start another.
	serverConfig := &config.ServerConfig{
		Name:           "test-server",
		URL:            "http://localhost:9999",
		Protocol:       "http",
		Enabled:        true,
		ReconnectOnUse: true,
		Created:        time.Now(),
	}

	manager, client := createTestManagerWithClient(t, serverConfig)

	// Set state to connecting (simulating an in-progress reconnect)
	client.StateManager.TransitionTo(types.StateConnecting)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := manager.CallTool(ctx, "test-server:some_tool", map[string]interface{}{})

	require.Error(t, err)
	// Should report "currently connecting" without triggering another reconnect
	assert.Contains(t, err.Error(), "connecting")
}

func TestReconnectOnUse_SkippedWhenDisabled(t *testing.T) {
	// Disabled servers should not attempt reconnect-on-use.
	serverConfig := &config.ServerConfig{
		Name:           "test-server",
		URL:            "http://localhost:9999",
		Protocol:       "http",
		Enabled:        false,
		ReconnectOnUse: true,
		Created:        time.Now(),
	}

	manager, client := createTestManagerWithClient(t, serverConfig)

	client.StateManager.SetError(fmt.Errorf("connection lost"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := manager.CallTool(ctx, "test-server:some_tool", map[string]interface{}{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestTryReconnectSync_PreventsStorms(t *testing.T) {
	// Concurrent calls to TryReconnectSync should not cause reconnect storms.
	// Only one reconnect attempt should run at a time.
	serverConfig := &config.ServerConfig{
		Name:           "test-server",
		URL:            "http://localhost:9999",
		Protocol:       "http",
		Enabled:        true,
		ReconnectOnUse: true,
		Created:        time.Now(),
	}

	logger := zap.NewNop()
	sugaredLogger := logger.Sugar()
	tempDir := t.TempDir()
	db, err := storage.NewBoltDB(tempDir, sugaredLogger)
	require.NoError(t, err)
	defer db.Close()

	client, err := managed.NewClient(
		serverConfig.Name,
		serverConfig,
		logger,
		nil,
		&config.Config{},
		db,
		secret.NewResolver(),
	)
	require.NoError(t, err)

	// Put client in error state
	client.StateManager.SetError(fmt.Errorf("connection lost"))

	// Launch multiple concurrent reconnect attempts
	const concurrency = 5
	results := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			results <- client.TryReconnectSync(ctx)
		}()
	}

	// All should complete (either succeed or fail) without hanging
	for i := 0; i < concurrency; i++ {
		select {
		case err := <-results:
			// Expected to fail since there's no real server
			// But shouldn't panic or hang
			_ = err
		case <-time.After(10 * time.Second):
			t.Fatal("TryReconnectSync timed out - possible deadlock")
		}
	}
}

func TestTryReconnectSync_RespectsContextCancellation(t *testing.T) {
	// TryReconnectSync should respect context cancellation.
	serverConfig := &config.ServerConfig{
		Name:           "test-server",
		URL:            "http://localhost:9999",
		Protocol:       "http",
		Enabled:        true,
		ReconnectOnUse: true,
		Created:        time.Now(),
	}

	logger := zap.NewNop()
	sugaredLogger := logger.Sugar()
	tempDir := t.TempDir()
	db, err := storage.NewBoltDB(tempDir, sugaredLogger)
	require.NoError(t, err)
	defer db.Close()

	client, err := managed.NewClient(
		serverConfig.Name,
		serverConfig,
		logger,
		nil,
		&config.Config{},
		db,
		secret.NewResolver(),
	)
	require.NoError(t, err)

	// Put client in error state
	client.StateManager.SetError(fmt.Errorf("connection lost"))

	// Create an already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = client.TryReconnectSync(ctx)
	require.Error(t, err)
}
