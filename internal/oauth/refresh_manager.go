// Package oauth provides OAuth 2.1 authentication support for MCP servers.
// This file implements proactive token refresh management.
package oauth

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/internal/storage"
)

// Default refresh configuration
const (
	// DefaultRefreshThreshold is the percentage of token lifetime at which proactive refresh triggers.
	// 0.8 means refresh at 80% of lifetime (e.g., 30s token → refresh at 24s).
	DefaultRefreshThreshold = 0.8

	// DefaultMaxRetries is the maximum number of refresh attempts before giving up.
	DefaultMaxRetries = 3

	// MinRefreshInterval prevents too-frequent refresh attempts.
	MinRefreshInterval = 5 * time.Second

	// RetryBackoffBase is the base duration for exponential backoff on retry.
	RetryBackoffBase = 2 * time.Second
)

// RefreshSchedule tracks the proactive refresh state for a single server.
type RefreshSchedule struct {
	ServerName       string      // Unique server identifier
	ExpiresAt        time.Time   // When the current token expires
	ScheduledRefresh time.Time   // When proactive refresh is scheduled (80% of lifetime)
	RetryCount       int         // Number of refresh retry attempts (0-3)
	LastError        string      // Last refresh error message
	Timer            *time.Timer // Background timer for scheduled refresh
}

// RefreshTokenStore defines storage operations needed by RefreshManager.
type RefreshTokenStore interface {
	ListOAuthTokens() ([]*storage.OAuthTokenRecord, error)
	GetOAuthToken(serverName string) (*storage.OAuthTokenRecord, error)
}

// RefreshRuntimeOperations defines runtime methods needed by RefreshManager.
type RefreshRuntimeOperations interface {
	RefreshOAuthToken(serverName string) error
}

// RefreshEventEmitter defines event emission methods for OAuth refresh events.
type RefreshEventEmitter interface {
	EmitOAuthTokenRefreshed(serverName string, expiresAt time.Time)
	EmitOAuthRefreshFailed(serverName string, errorMsg string)
}

// RefreshManagerConfig holds configuration for the RefreshManager.
type RefreshManagerConfig struct {
	Threshold  float64 // Percentage of lifetime at which to refresh (default: 0.8)
	MaxRetries int     // Maximum retry attempts (default: 3)
}

// RefreshManager coordinates proactive OAuth token refresh across all servers.
type RefreshManager struct {
	storage      RefreshTokenStore
	coordinator  *OAuthFlowCoordinator
	runtime      RefreshRuntimeOperations
	eventEmitter RefreshEventEmitter
	schedules    map[string]*RefreshSchedule
	threshold    float64
	maxRetries   int
	mu           sync.RWMutex
	logger       *zap.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	started      bool
}

// NewRefreshManager creates a new RefreshManager instance.
func NewRefreshManager(
	tokenStore RefreshTokenStore,
	coordinator *OAuthFlowCoordinator,
	config *RefreshManagerConfig,
	logger *zap.Logger,
) *RefreshManager {
	threshold := DefaultRefreshThreshold
	maxRetries := DefaultMaxRetries

	if config != nil {
		if config.Threshold > 0 && config.Threshold < 1 {
			threshold = config.Threshold
		}
		if config.MaxRetries > 0 {
			maxRetries = config.MaxRetries
		}
	}

	if logger == nil {
		logger = zap.L()
	}

	return &RefreshManager{
		storage:     tokenStore,
		coordinator: coordinator,
		schedules:   make(map[string]*RefreshSchedule),
		threshold:   threshold,
		maxRetries:  maxRetries,
		logger:      logger.Named("refresh-manager"),
	}
}

// SetRuntime sets the runtime operations interface.
// This must be called before Start() to enable token refresh.
func (m *RefreshManager) SetRuntime(runtime RefreshRuntimeOperations) {
	m.runtime = runtime
}

// SetEventEmitter sets the event emitter for SSE notifications.
func (m *RefreshManager) SetEventEmitter(emitter RefreshEventEmitter) {
	m.eventEmitter = emitter
}

// Start initializes the refresh manager and loads existing tokens.
// It schedules proactive refresh for all non-expired tokens.
func (m *RefreshManager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return nil // Already started
	}

	// Create a cancellable context for all timers
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.started = true

	m.logger.Info("Starting RefreshManager")

	// Load existing tokens and schedule refreshes
	if m.storage != nil {
		tokens, err := m.storage.ListOAuthTokens()
		if err != nil {
			m.logger.Warn("Failed to load existing tokens", zap.Error(err))
			// Continue - we can still handle new tokens
		} else {
			for _, token := range tokens {
				if token != nil && !token.ExpiresAt.IsZero() {
					m.scheduleRefreshLocked(token.ServerName, token.ExpiresAt)
				}
			}
			m.logger.Info("Loaded existing tokens",
				zap.Int("count", len(tokens)),
				zap.Int("scheduled", len(m.schedules)))
		}
	}

	return nil
}

// Stop cancels all scheduled refreshes and cleans up resources.
func (m *RefreshManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return
	}

	m.logger.Info("Stopping RefreshManager", zap.Int("active_schedules", len(m.schedules)))

	// Cancel context to signal all goroutines
	if m.cancel != nil {
		m.cancel()
	}

	// Stop all timers
	for serverName, schedule := range m.schedules {
		if schedule.Timer != nil {
			schedule.Timer.Stop()
		}
		delete(m.schedules, serverName)
	}

	m.started = false
}

// OnTokenSaved is called when a token is saved to storage.
// It reschedules the proactive refresh for the new token expiration.
func (m *RefreshManager) OnTokenSaved(serverName string, expiresAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return
	}

	// Cancel existing schedule if any
	if existing, ok := m.schedules[serverName]; ok && existing.Timer != nil {
		existing.Timer.Stop()
	}

	// Schedule refresh for new token
	m.scheduleRefreshLocked(serverName, expiresAt)
}

// OnTokenCleared is called when a token is cleared (e.g., logout).
// It cancels any scheduled refresh for that server.
func (m *RefreshManager) OnTokenCleared(serverName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if schedule, ok := m.schedules[serverName]; ok {
		if schedule.Timer != nil {
			schedule.Timer.Stop()
		}
		delete(m.schedules, serverName)
		m.logger.Info("Cancelled refresh schedule due to token cleared",
			zap.String("server", serverName))
	}
}

// GetSchedule returns the refresh schedule for a server (for testing/debugging).
func (m *RefreshManager) GetSchedule(serverName string) *RefreshSchedule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.schedules[serverName]
}

// GetScheduleCount returns the number of active schedules (for testing/debugging).
func (m *RefreshManager) GetScheduleCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.schedules)
}

// scheduleRefreshLocked schedules a proactive refresh for a token.
// Must be called with m.mu held.
func (m *RefreshManager) scheduleRefreshLocked(serverName string, expiresAt time.Time) {
	now := time.Now()

	// Calculate when to refresh (at threshold % of lifetime)
	lifetime := expiresAt.Sub(now)
	if lifetime <= 0 {
		m.logger.Debug("Token already expired, skipping schedule",
			zap.String("server", serverName),
			zap.Time("expires_at", expiresAt))
		return
	}

	// Calculate refresh time at threshold of remaining lifetime
	refreshDelay := time.Duration(float64(lifetime) * m.threshold)

	// Ensure minimum interval
	if refreshDelay < MinRefreshInterval {
		refreshDelay = MinRefreshInterval
	}

	refreshAt := now.Add(refreshDelay)

	// If refresh would be after expiration, schedule for just before expiration
	if refreshAt.After(expiresAt.Add(-MinRefreshInterval)) {
		refreshAt = expiresAt.Add(-MinRefreshInterval)
		refreshDelay = refreshAt.Sub(now)
		if refreshDelay <= 0 {
			m.logger.Debug("Token too close to expiration for proactive refresh",
				zap.String("server", serverName),
				zap.Time("expires_at", expiresAt))
			return
		}
	}

	// Create or update schedule
	schedule := &RefreshSchedule{
		ServerName:       serverName,
		ExpiresAt:        expiresAt,
		ScheduledRefresh: refreshAt,
		RetryCount:       0,
	}

	// Start timer
	schedule.Timer = time.AfterFunc(refreshDelay, func() {
		m.executeRefresh(serverName)
	})

	m.schedules[serverName] = schedule

	m.logger.Info("Scheduled proactive token refresh",
		zap.String("server", serverName),
		zap.Time("expires_at", expiresAt),
		zap.Time("refresh_at", refreshAt),
		zap.Duration("delay", refreshDelay),
		zap.Float64("threshold", m.threshold))
}

// executeRefresh performs the token refresh for a server.
func (m *RefreshManager) executeRefresh(serverName string) {
	m.mu.Lock()
	_, ok := m.schedules[serverName]
	if !ok {
		m.mu.Unlock()
		return // Schedule was cancelled
	}

	// Check if context is cancelled
	if m.ctx.Err() != nil {
		m.mu.Unlock()
		return
	}

	m.mu.Unlock()

	// Check if a manual OAuth flow is in progress
	if m.coordinator != nil && m.coordinator.IsFlowActive(serverName) {
		m.logger.Info("Skipping proactive refresh, OAuth flow in progress",
			zap.String("server", serverName))
		// Reschedule for later
		m.rescheduleAfterDelay(serverName, RetryBackoffBase)
		return
	}

	m.logger.Info("Executing proactive token refresh",
		zap.String("server", serverName))

	// Attempt refresh
	var refreshErr error
	if m.runtime != nil {
		refreshErr = m.runtime.RefreshOAuthToken(serverName)
	} else {
		refreshErr = ErrRefreshFailed
	}

	if refreshErr != nil {
		m.handleRefreshFailure(serverName, refreshErr)
	} else {
		m.handleRefreshSuccess(serverName)
	}
}

// handleRefreshSuccess handles a successful token refresh.
func (m *RefreshManager) handleRefreshSuccess(serverName string) {
	m.mu.Lock()
	schedule := m.schedules[serverName]
	if schedule != nil {
		schedule.RetryCount = 0
		schedule.LastError = ""
	}
	m.mu.Unlock()

	m.logger.Info("Proactive token refresh succeeded",
		zap.String("server", serverName))

	// Get the new token expiration to emit event
	if m.storage != nil {
		token, err := m.storage.GetOAuthToken(serverName)
		if err == nil && token != nil && m.eventEmitter != nil {
			m.eventEmitter.EmitOAuthTokenRefreshed(serverName, token.ExpiresAt)
		}
	}

	// Note: The token store hook (OnTokenSaved) will reschedule the next refresh
}

// handleRefreshFailure handles a failed token refresh with exponential backoff retry.
func (m *RefreshManager) handleRefreshFailure(serverName string, err error) {
	m.mu.Lock()
	schedule := m.schedules[serverName]
	if schedule == nil {
		m.mu.Unlock()
		return
	}

	schedule.RetryCount++
	schedule.LastError = err.Error()
	retryCount := schedule.RetryCount
	maxRetries := m.maxRetries
	m.mu.Unlock()

	m.logger.Warn("Proactive token refresh failed",
		zap.String("server", serverName),
		zap.Error(err),
		zap.Int("retry_count", retryCount),
		zap.Int("max_retries", maxRetries))

	if retryCount >= maxRetries {
		// Max retries exceeded, emit failure event
		m.logger.Error("Proactive token refresh failed after max retries",
			zap.String("server", serverName),
			zap.Int("retries", retryCount))

		if m.eventEmitter != nil {
			m.eventEmitter.EmitOAuthRefreshFailed(serverName, err.Error())
		}

		// Clear the schedule - user will need to re-authenticate manually
		m.mu.Lock()
		delete(m.schedules, serverName)
		m.mu.Unlock()
		return
	}

	// Calculate backoff delay: base * 2^(retry-1)
	backoff := RetryBackoffBase * time.Duration(1<<(retryCount-1))
	m.rescheduleAfterDelay(serverName, backoff)
}

// rescheduleAfterDelay reschedules a refresh attempt after a delay.
func (m *RefreshManager) rescheduleAfterDelay(serverName string, delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	schedule, ok := m.schedules[serverName]
	if !ok {
		return
	}

	// Stop existing timer if any
	if schedule.Timer != nil {
		schedule.Timer.Stop()
	}

	// Start new timer
	schedule.Timer = time.AfterFunc(delay, func() {
		m.executeRefresh(serverName)
	})

	m.logger.Info("Rescheduled token refresh",
		zap.String("server", serverName),
		zap.Duration("delay", delay),
		zap.Int("retry_count", schedule.RetryCount))
}
