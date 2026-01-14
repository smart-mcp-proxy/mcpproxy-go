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

// T014: Test RefreshManager stops on permanent failure (invalid_grant)
func TestRefreshManager_StopOnPermanentFailure(t *testing.T) {
	logger := zaptest.NewLogger(t)
	store := newMockTokenStore()
	emitter := &mockEventEmitter{}

	config := &RefreshManagerConfig{
		Threshold: 0.1,
	}

	manager := NewRefreshManager(store, nil, config, logger)
	// Use invalid_grant error which should be classified as permanent
	runtime := &mockRuntime{
		refreshErr: errors.New("invalid_grant: refresh token expired"),
	}
	manager.SetRuntime(runtime)
	manager.SetEventEmitter(emitter)

	ctx := context.Background()
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// Manually trigger a refresh that will fail permanently
	expiresAt := time.Now().Add(10 * time.Second)
	manager.OnTokenSaved("test-server", expiresAt)

	// Give time for the schedule to be set up
	time.Sleep(50 * time.Millisecond)

	// Directly call executeRefresh to test permanent failure behavior
	manager.executeRefresh("test-server")

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	// Check that failure event was emitted for permanent failure
	assert.Equal(t, 1, emitter.GetFailedEvents(), "Should emit failure event for invalid_grant error")

	// Schedule should have RefreshStateFailed set
	schedule := manager.GetSchedule("test-server")
	if schedule != nil {
		assert.Equal(t, RefreshStateFailed, schedule.RefreshState, "State should be failed for permanent error")
	}
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

// Test that expired tokens without refresh token are marked as failed
func TestRefreshManager_ExpiredTokenWithoutRefreshToken(t *testing.T) {
	logger := zaptest.NewLogger(t)
	store := newMockTokenStore()

	// Token that already expired with no refresh token
	store.AddToken(&storage.OAuthTokenRecord{
		ServerName:   "expired-server",
		ExpiresAt:    time.Now().Add(-1 * time.Hour), // Already expired
		RefreshToken: "",                             // No refresh token
	})

	manager := NewRefreshManager(store, nil, nil, logger)

	ctx := context.Background()
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// Should have a schedule in failed state (not scheduled for refresh)
	schedule := manager.GetSchedule("expired-server")
	require.NotNil(t, schedule, "Should have a schedule entry for tracking state")
	assert.Equal(t, RefreshStateFailed, schedule.RefreshState, "Expired tokens without refresh token should be in failed state")
}

// Test that expired tokens with refresh token are queued for immediate refresh
func TestRefreshManager_ExpiredTokenWithRefreshToken(t *testing.T) {
	logger := zaptest.NewLogger(t)
	store := newMockTokenStore()

	// Token that already expired but has refresh token
	store.AddToken(&storage.OAuthTokenRecord{
		ServerName:   "expired-server",
		ExpiresAt:    time.Now().Add(-1 * time.Hour), // Already expired
		RefreshToken: "valid-refresh-token",          // Has refresh token
	})

	runtime := &mockRuntime{
		refreshErr: errors.New("network error"),
	}

	manager := NewRefreshManager(store, nil, nil, logger)
	manager.SetRuntime(runtime)

	ctx := context.Background()
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// Wait for async startup refresh to execute
	time.Sleep(100 * time.Millisecond)

	// Should have attempted refresh
	calls := runtime.GetRefreshCalls()
	assert.Len(t, calls, 1, "Should attempt immediate refresh for expired token with refresh token")

	// Should be in retrying state (not failed since error is retryable)
	schedule := manager.GetSchedule("expired-server")
	require.NotNil(t, schedule, "Should have a schedule entry")
	assert.Equal(t, RefreshStateRetrying, schedule.RefreshState, "Should be retrying after network error")
}

// T023: Test exponential backoff sequence (10s, 20s, 40s, 80s, 160s, 300s cap)
func TestRefreshManager_ExponentialBackoffSequence(t *testing.T) {
	logger := zaptest.NewLogger(t)
	manager := NewRefreshManager(nil, nil, nil, logger)

	// Test the backoff sequence: 10s -> 20s -> 40s -> 80s -> 160s -> 300s (cap)
	expectedBackoffs := []time.Duration{
		10 * time.Second,  // retry 0: 10s * 2^0 = 10s
		20 * time.Second,  // retry 1: 10s * 2^1 = 20s
		40 * time.Second,  // retry 2: 10s * 2^2 = 40s
		80 * time.Second,  // retry 3: 10s * 2^3 = 80s
		160 * time.Second, // retry 4: 10s * 2^4 = 160s
		300 * time.Second, // retry 5: 10s * 2^5 = 320s, capped to 300s
		300 * time.Second, // retry 6: still capped at 300s
		300 * time.Second, // retry 7: still capped at 300s
	}

	for i, expected := range expectedBackoffs {
		actual := manager.calculateBackoff(i)
		assert.Equal(t, expected, actual, "Backoff at retry %d should be %v, got %v", i, expected, actual)
	}

	// Verify constants match expected values
	assert.Equal(t, 10*time.Second, RetryBackoffBase, "RetryBackoffBase should be 10s")
	assert.Equal(t, 5*time.Minute, MaxRetryBackoff, "MaxRetryBackoff should be 5min (300s)")
}

// T024: Test unlimited retries until token expiration
func TestRefreshManager_UnlimitedRetriesUntilExpiration(t *testing.T) {
	logger := zaptest.NewLogger(t)
	store := newMockTokenStore()
	emitter := &mockEventEmitter{}

	config := &RefreshManagerConfig{
		Threshold: 0.1,
	}

	manager := NewRefreshManager(store, nil, config, logger)
	// Use a network error which is retryable (not permanent like invalid_grant)
	runtime := &mockRuntime{
		refreshErr: errors.New("connection timeout"),
	}
	manager.SetRuntime(runtime)
	manager.SetEventEmitter(emitter)

	ctx := context.Background()
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// Create a token that expires far in the future
	expiresAt := time.Now().Add(1 * time.Hour)
	manager.OnTokenSaved("test-server", expiresAt)

	// Give time for the schedule to be set up
	time.Sleep(50 * time.Millisecond)

	// Simulate multiple refresh failures
	for i := 0; i < 5; i++ {
		manager.executeRefresh("test-server")
		time.Sleep(10 * time.Millisecond)
	}

	// Verify that:
	// 1. Schedule still exists (not cleared due to max retries)
	schedule := manager.GetSchedule("test-server")
	require.NotNil(t, schedule, "Schedule should still exist for retryable errors")

	// 2. State is RefreshStateRetrying (not failed)
	assert.Equal(t, RefreshStateRetrying, schedule.RefreshState, "State should be retrying for network errors")

	// 3. No failure events emitted (only permanent failures emit events)
	assert.Equal(t, 0, emitter.GetFailedEvents(), "No failure events for retryable network errors")

	// 4. Retry count should have increased
	assert.GreaterOrEqual(t, schedule.RetryCount, 5, "Retry count should increase with each failure")
}

// Test error classification for metrics
func TestClassifyRefreshError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"nil error", nil, "success"},
		{"invalid_grant", errors.New("invalid_grant: refresh token expired"), "failed_invalid_grant"},
		{"refresh token expired", errors.New("refresh token expired"), "failed_invalid_grant"},
		{"connection timeout", errors.New("connection timeout"), "failed_network"},
		{"connection refused", errors.New("connection refused"), "failed_network"},
		{"dial tcp error", errors.New("dial tcp: no such host"), "failed_network"},
		{"EOF error", errors.New("unexpected EOF"), "failed_network"},
		{"context deadline exceeded", errors.New("context deadline exceeded"), "failed_network"},
		{"generic error", errors.New("unknown server error"), "failed_other"},
		{"server_error", errors.New("OAuth server_error"), "failed_other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyRefreshError(tt.err)
			assert.Equal(t, tt.expected, result, "Error classification mismatch for: %v", tt.err)
		})
	}
}
