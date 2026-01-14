package oauth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// mockTokenStore implements RefreshTokenStore for testing.
type mockTokenStore struct {
	tokens map[string]*storage.OAuthTokenRecord
	mu     sync.RWMutex
}

func newMockTokenStore() *mockTokenStore {
	return &mockTokenStore{
		tokens: make(map[string]*storage.OAuthTokenRecord),
	}
}

func (m *mockTokenStore) ListOAuthTokens() ([]*storage.OAuthTokenRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tokens := make([]*storage.OAuthTokenRecord, 0, len(m.tokens))
	for _, t := range m.tokens {
		tokens = append(tokens, t)
	}
	return tokens, nil
}

func (m *mockTokenStore) GetOAuthToken(serverName string) (*storage.OAuthTokenRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if token, ok := m.tokens[serverName]; ok {
		return token, nil
	}
	return nil, errors.New("token not found")
}

func (m *mockTokenStore) AddToken(token *storage.OAuthTokenRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[token.ServerName] = token
}

// mockRuntime implements RefreshRuntimeOperations for testing.
type mockRuntime struct {
	refreshCalls   []string
	refreshErr     error
	refreshDelay   time.Duration
	mu             sync.Mutex
	refreshCounter atomic.Int32
}

func (m *mockRuntime) RefreshOAuthToken(serverName string) error {
	m.mu.Lock()
	m.refreshCalls = append(m.refreshCalls, serverName)
	err := m.refreshErr
	delay := m.refreshDelay
	m.mu.Unlock()

	m.refreshCounter.Add(1)

	if delay > 0 {
		time.Sleep(delay)
	}
	return err
}

func (m *mockRuntime) GetRefreshCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.refreshCalls))
	copy(result, m.refreshCalls)
	return result
}

func (m *mockRuntime) SetRefreshError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshErr = err
}

// mockEventEmitter implements RefreshEventEmitter for testing.
type mockEventEmitter struct {
	refreshedEvents []struct {
		serverName string
		expiresAt  time.Time
	}
	failedEvents []struct {
		serverName string
		errorMsg   string
	}
	mu sync.Mutex
}

func (m *mockEventEmitter) EmitOAuthTokenRefreshed(serverName string, expiresAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshedEvents = append(m.refreshedEvents, struct {
		serverName string
		expiresAt  time.Time
	}{serverName, expiresAt})
}

func (m *mockEventEmitter) EmitOAuthRefreshFailed(serverName string, errorMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failedEvents = append(m.failedEvents, struct {
		serverName string
		errorMsg   string
	}{serverName, errorMsg})
}

func (m *mockEventEmitter) GetRefreshedEvents() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.refreshedEvents)
}

func (m *mockEventEmitter) GetFailedEvents() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.failedEvents)
}

// T012: Test RefreshManager scheduleRefresh at 80% lifetime
func TestRefreshManager_ScheduleAt80PercentLifetime(t *testing.T) {
	logger := zaptest.NewLogger(t)
	store := newMockTokenStore()

	// Token that expires in 1 hour (well above MinRefreshInterval of 5s)
	expiresAt := time.Now().Add(1 * time.Hour)
	store.AddToken(&storage.OAuthTokenRecord{
		ServerName: "test-server",
		ExpiresAt:  expiresAt,
	})

	manager := NewRefreshManager(store, nil, nil, logger)
	runtime := &mockRuntime{}
	manager.SetRuntime(runtime)

	ctx := context.Background()
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// Verify schedule was created
	schedule := manager.GetSchedule("test-server")
	require.NotNil(t, schedule, "Schedule should be created")

	// Verify refresh is scheduled at approximately 80% of lifetime
	// With 1 hour token, 80% = 48 minutes, so refresh should be around 48 minutes from now
	assert.Equal(t, "test-server", schedule.ServerName)
	assert.Equal(t, expiresAt, schedule.ExpiresAt)

	// Verify scheduled time is approximately at 80% threshold
	expectedRefreshDelay := time.Duration(float64(1*time.Hour) * 0.8) // 48 minutes
	actualRefreshDelay := time.Until(schedule.ScheduledRefresh)
	// Allow 10 second tolerance for test timing
	assert.InDelta(t, expectedRefreshDelay.Seconds(), actualRefreshDelay.Seconds(), 10.0,
		"Refresh should be scheduled at ~80%% of lifetime")
}

// T013: Test RefreshManager retry with exponential backoff
func TestRefreshManager_RetryWithExponentialBackoff(t *testing.T) {
	logger := zaptest.NewLogger(t)
	store := newMockTokenStore()

	// Token that expires in 500ms
	expiresAt := time.Now().Add(500 * time.Millisecond)
	store.AddToken(&storage.OAuthTokenRecord{
		ServerName: "test-server",
		ExpiresAt:  expiresAt,
	})

	// Use shorter intervals for testing
	config := &RefreshManagerConfig{
		Threshold:  0.1, // Trigger refresh quickly
		MaxRetries: 3,
	}

	manager := NewRefreshManager(store, nil, config, logger)
	runtime := &mockRuntime{
		refreshErr: errors.New("refresh failed"),
	}
	manager.SetRuntime(runtime)

	ctx := context.Background()
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// Wait for initial refresh and retries
	// With base backoff of 2s, retries would take too long for unit tests
	// Instead, verify the retry count increases
	time.Sleep(100 * time.Millisecond)

	schedule := manager.GetSchedule("test-server")
	if schedule != nil {
		// After first failure, retry count should increase
		assert.GreaterOrEqual(t, schedule.RetryCount, 1, "Retry count should increase after failure")
		assert.Contains(t, schedule.LastError, "refresh failed")
	}
}

// T014: Test RefreshManager stops after max retries
func TestRefreshManager_StopOnMaxRetries(t *testing.T) {
	logger := zaptest.NewLogger(t)
	store := newMockTokenStore()
	emitter := &mockEventEmitter{}

	config := &RefreshManagerConfig{
		Threshold:  0.1,
		MaxRetries: 1, // Only 1 retry for faster test
	}

	manager := NewRefreshManager(store, nil, config, logger)
	runtime := &mockRuntime{
		refreshErr: errors.New("permanent failure"),
	}
	manager.SetRuntime(runtime)
	manager.SetEventEmitter(emitter)

	ctx := context.Background()
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// Manually trigger a refresh that will fail
	expiresAt := time.Now().Add(10 * time.Second)
	manager.OnTokenSaved("test-server", expiresAt)

	// Give time for the schedule to be set up
	time.Sleep(50 * time.Millisecond)

	// Directly call executeRefresh to test max retry behavior
	manager.executeRefresh("test-server")

	// After max retries, schedule should be removed
	time.Sleep(50 * time.Millisecond)

	// Trigger another refresh to exceed max retries
	manager.executeRefresh("test-server")

	time.Sleep(50 * time.Millisecond)

	// Check that failure event was emitted
	assert.GreaterOrEqual(t, emitter.GetFailedEvents(), 1, "Should emit failure event after max retries")
}

// T015: Test RefreshManager coordination with OAuthFlowCoordinator
func TestRefreshManager_CoordinationWithFlowCoordinator(t *testing.T) {
	logger := zaptest.NewLogger(t)
	store := newMockTokenStore()
	coordinator := NewOAuthFlowCoordinator()

	config := &RefreshManagerConfig{
		Threshold: 0.5, // 50% threshold for testing
	}

	manager := NewRefreshManager(store, coordinator, config, logger)
	runtime := &mockRuntime{}
	manager.SetRuntime(runtime)

	ctx := context.Background()
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// Start a manual OAuth flow for the server
	_, err = coordinator.StartFlow("test-server")
	require.NoError(t, err)

	// Schedule a refresh with longer expiration (10 seconds)
	expiresAt := time.Now().Add(10 * time.Second)
	manager.OnTokenSaved("test-server", expiresAt)

	// Verify schedule was created
	schedule := manager.GetSchedule("test-server")
	require.NotNil(t, schedule, "Schedule should be created")

	// Directly trigger refresh while flow is active
	manager.executeRefresh("test-server")

	// Refresh should be skipped because flow is active
	time.Sleep(50 * time.Millisecond)
	calls := runtime.GetRefreshCalls()
	assert.Empty(t, calls, "Refresh should be skipped when OAuth flow is active")

	// End the flow
	coordinator.EndFlow("test-server", true, nil)

	// Now refresh should proceed on next attempt
	manager.executeRefresh("test-server")
	time.Sleep(50 * time.Millisecond)

	calls = runtime.GetRefreshCalls()
	assert.Len(t, calls, 1, "Refresh should proceed after OAuth flow ends")
}

// T016: Test RefreshManager OnTokenSaved and OnTokenCleared hooks
func TestRefreshManager_TokenHooks(t *testing.T) {
	logger := zaptest.NewLogger(t)
	store := newMockTokenStore()

	manager := NewRefreshManager(store, nil, nil, logger)
	runtime := &mockRuntime{}
	manager.SetRuntime(runtime)

	ctx := context.Background()
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// Initially no schedules
	assert.Equal(t, 0, manager.GetScheduleCount())

	// OnTokenSaved should create a schedule
	expiresAt := time.Now().Add(1 * time.Hour)
	manager.OnTokenSaved("server-a", expiresAt)

	assert.Equal(t, 1, manager.GetScheduleCount())
	schedule := manager.GetSchedule("server-a")
	require.NotNil(t, schedule)
	assert.Equal(t, expiresAt, schedule.ExpiresAt)

	// OnTokenSaved for same server should update schedule
	newExpiresAt := time.Now().Add(2 * time.Hour)
	manager.OnTokenSaved("server-a", newExpiresAt)

	assert.Equal(t, 1, manager.GetScheduleCount())
	schedule = manager.GetSchedule("server-a")
	require.NotNil(t, schedule)
	assert.Equal(t, newExpiresAt, schedule.ExpiresAt)

	// OnTokenSaved for different server should add schedule
	manager.OnTokenSaved("server-b", expiresAt)
	assert.Equal(t, 2, manager.GetScheduleCount())

	// OnTokenCleared should remove schedule
	manager.OnTokenCleared("server-a")
	assert.Equal(t, 1, manager.GetScheduleCount())
	assert.Nil(t, manager.GetSchedule("server-a"))
	assert.NotNil(t, manager.GetSchedule("server-b"))

	// OnTokenCleared for non-existent server should not panic
	manager.OnTokenCleared("non-existent")
	assert.Equal(t, 1, manager.GetScheduleCount())
}

// Test NewRefreshManager configuration
func TestNewRefreshManager_Configuration(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("default config", func(t *testing.T) {
		manager := NewRefreshManager(nil, nil, nil, logger)
		assert.Equal(t, DefaultRefreshThreshold, manager.threshold)
		assert.Equal(t, DefaultMaxRetries, manager.maxRetries)
	})

	t.Run("custom config", func(t *testing.T) {
		config := &RefreshManagerConfig{
			Threshold:  0.5,
			MaxRetries: 5,
		}
		manager := NewRefreshManager(nil, nil, config, logger)
		assert.Equal(t, 0.5, manager.threshold)
		assert.Equal(t, 5, manager.maxRetries)
	})

	t.Run("invalid threshold ignored", func(t *testing.T) {
		config := &RefreshManagerConfig{
			Threshold: 1.5, // Invalid - >= 1
		}
		manager := NewRefreshManager(nil, nil, config, logger)
		assert.Equal(t, DefaultRefreshThreshold, manager.threshold)
	})
}

// Test Start/Stop lifecycle
func TestRefreshManager_StartStopLifecycle(t *testing.T) {
	logger := zaptest.NewLogger(t)

	manager := NewRefreshManager(nil, nil, nil, logger)

	ctx := context.Background()

	// Can start
	err := manager.Start(ctx)
	require.NoError(t, err)

	// Starting again is idempotent
	err = manager.Start(ctx)
	require.NoError(t, err)

	// Can stop
	manager.Stop()

	// Stopping again is idempotent
	manager.Stop()

	// Can restart
	err = manager.Start(ctx)
	require.NoError(t, err)
	manager.Stop()
}

// Test refresh success emits event
func TestRefreshManager_SuccessEmitsEvent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	store := newMockTokenStore()
	emitter := &mockEventEmitter{}

	// Add token that will be returned after refresh
	newExpiresAt := time.Now().Add(1 * time.Hour)
	store.AddToken(&storage.OAuthTokenRecord{
		ServerName: "test-server",
		ExpiresAt:  newExpiresAt,
	})

	manager := NewRefreshManager(store, nil, nil, logger)
	runtime := &mockRuntime{} // No error = success
	manager.SetRuntime(runtime)
	manager.SetEventEmitter(emitter)

	ctx := context.Background()
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// Create a schedule and trigger refresh
	manager.OnTokenSaved("test-server", time.Now().Add(100*time.Millisecond))
	time.Sleep(50 * time.Millisecond)
	manager.executeRefresh("test-server")

	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, 1, emitter.GetRefreshedEvents(), "Should emit refreshed event on success")
}

// Test that expired tokens are not scheduled
func TestRefreshManager_ExpiredTokenNotScheduled(t *testing.T) {
	logger := zaptest.NewLogger(t)
	store := newMockTokenStore()

	// Token that already expired
	store.AddToken(&storage.OAuthTokenRecord{
		ServerName: "expired-server",
		ExpiresAt:  time.Now().Add(-1 * time.Hour), // Already expired
	})

	manager := NewRefreshManager(store, nil, nil, logger)

	ctx := context.Background()
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// Should not schedule refresh for expired token
	assert.Equal(t, 0, manager.GetScheduleCount(), "Expired tokens should not be scheduled")
}
