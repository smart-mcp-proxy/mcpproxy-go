// Package oauth provides OAuth 2.1 authentication support for MCP servers.
// This file implements proactive token refresh management.
package oauth

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Default refresh configuration
const (
	// DefaultRefreshThreshold is the percentage of token lifetime at which proactive refresh triggers.
	// Used in hybrid calculation: refresh at the EARLIER of (threshold * lifetime) or (expiry - MinRefreshBuffer).
	// 0.75 means refresh at 75% of lifetime for long-lived tokens.
	DefaultRefreshThreshold = 0.75

	// DefaultMaxRetries is the maximum number of consecutive refresh attempts before giving up.
	// Acts as a circuit breaker to prevent infinite retry loops (issue #310).
	// With exponential backoff (10s base, 5min cap), 50 retries spans ~2+ hours.
	DefaultMaxRetries = 50

	// MinRefreshInterval prevents too-frequent refresh attempts.
	MinRefreshInterval = 5 * time.Second

	// MinRefreshBuffer is the minimum time before expiration to schedule a refresh.
	// This ensures adequate time for retries even with short-lived tokens.
	// Industry best practice: Google and Microsoft recommend 5 minutes.
	MinRefreshBuffer = 5 * time.Minute

	// RetryBackoffBase is the base duration for exponential backoff on retry.
	// Per FR-008: minimum 10 seconds between refresh attempts per server.
	RetryBackoffBase = 10 * time.Second

	// MaxRetryBackoff is the maximum backoff duration (5 minutes per FR-009).
	MaxRetryBackoff = 5 * time.Minute

	// MaxExpiredTokenAge is how long after token expiration we continue retrying
	// before giving up completely. After this duration, we assume the refresh token
	// is no longer valid even if it wasn't explicitly rejected.
	MaxExpiredTokenAge = 24 * time.Hour
)

// RefreshState represents the current state of token refresh for health reporting.
type RefreshState int

const (
	// RefreshStateIdle means no refresh is pending or in progress.
	RefreshStateIdle RefreshState = iota
	// RefreshStateScheduled means a proactive refresh is scheduled (hybrid: 75% lifetime or 5min buffer).
	RefreshStateScheduled
	// RefreshStateRetrying means refresh failed and is retrying with exponential backoff.
	RefreshStateRetrying
	// RefreshStateFailed means refresh permanently failed (e.g., invalid_grant).
	RefreshStateFailed
)

// String returns the string representation of RefreshState.
func (s RefreshState) String() string {
	switch s {
	case RefreshStateIdle:
		return "idle"
	case RefreshStateScheduled:
		return "scheduled"
	case RefreshStateRetrying:
		return "retrying"
	case RefreshStateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// RefreshSchedule tracks the proactive refresh state for a single server.
type RefreshSchedule struct {
	ServerName       string        // Unique server identifier
	ExpiresAt        time.Time     // When the current token expires
	ScheduledRefresh time.Time     // When proactive refresh is scheduled (hybrid strategy)
	RetryCount       int           // Number of refresh retry attempts
	LastError        string        // Last refresh error message
	Timer            *time.Timer   // Background timer for scheduled refresh
	RetryBackoff     time.Duration // Current backoff duration for retries
	MaxBackoff       time.Duration // Maximum backoff duration (5 minutes)
	LastAttempt      time.Time     // Time of last refresh attempt
	RefreshState     RefreshState  // Current state for health reporting
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

// RefreshMetricsRecorder defines metrics recording methods for OAuth refresh operations.
// This interface decouples RefreshManager from the concrete MetricsManager.
type RefreshMetricsRecorder interface {
	// RecordOAuthRefresh records an OAuth token refresh attempt.
	// Result is one of: "success", "failed_network", "failed_invalid_grant", "failed_invalid_client", "failed_server_error", "failed_server_gone", "failed_other".
	RecordOAuthRefresh(server, result string)
	// RecordOAuthRefreshDuration records the duration of an OAuth token refresh attempt.
	RecordOAuthRefreshDuration(server, result string, duration time.Duration)
}

// RefreshManagerConfig holds configuration for the RefreshManager.
type RefreshManagerConfig struct {
	Threshold  float64 // Percentage of lifetime at which to refresh (default: 0.8)
	MaxRetries int     // Maximum retry attempts (default: 3)
}

// RefreshManager coordinates proactive OAuth token refresh across all servers.
type RefreshManager struct {
	storage         RefreshTokenStore
	coordinator     *OAuthFlowCoordinator
	runtime         RefreshRuntimeOperations
	eventEmitter    RefreshEventEmitter
	metricsRecorder RefreshMetricsRecorder
	schedules       map[string]*RefreshSchedule
	threshold       float64
	maxRetries      int
	mu              sync.RWMutex
	logger          *zap.Logger
	ctx             context.Context
	cancel          context.CancelFunc
	started         bool
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

// SetMetricsRecorder sets the metrics recorder for Prometheus metrics.
// This enables FR-011: OAuth refresh metrics emission.
func (m *RefreshManager) SetMetricsRecorder(recorder RefreshMetricsRecorder) {
	m.metricsRecorder = recorder
}

// Start initializes the refresh manager and loads existing tokens.
// For non-expired tokens, it schedules proactive refresh using hybrid strategy.
// For expired tokens with valid refresh tokens, it attempts immediate refresh.
func (m *RefreshManager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		m.logger.Debug("RefreshManager already started, skipping")
		return nil // Already started
	}

	// Create a cancellable context for all timers
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.started = true

	// Spec 113 FR-008: terminal outcomes of reactive refresh flights (started
	// by the token store, not by this manager) reach the schedule through the
	// coordinator's completion hook.
	DefaultRefreshCoordinator().SetCompletionHook(m.onRefreshOutcome)

	m.logger.Info("RefreshManager.Start() called")

	// Track startup refresh stats
	var scheduled, immediateRefresh, expired int

	// Load existing tokens and schedule refreshes
	if m.storage != nil {
		m.logger.Debug("Loading OAuth tokens from storage",
			zap.Bool("storage_available", true))

		tokens, err := m.storage.ListOAuthTokens()
		if err != nil {
			m.logger.Warn("Failed to load existing tokens",
				zap.Error(err))
			// Continue - we can still handle new tokens
		} else {
			m.logger.Info("OAuth tokens retrieved from storage",
				zap.Int("count", len(tokens)))
			// Collect tokens that need immediate refresh (expired access token but valid refresh token)
			var tokensToRefresh []string

			for _, token := range tokens {
				if token == nil || token.ExpiresAt.IsZero() {
					m.logger.Debug("Skipping nil or zero-expiry token")
					continue
				}

				serverName := token.GetServerName()
				now := time.Now()

				// Log each token being processed for debugging
				m.logger.Debug("Processing OAuth token",
					zap.String("server", serverName),
					zap.String("storage_key", token.ServerName),
					zap.Time("expires_at", token.ExpiresAt),
					zap.Bool("has_refresh_token", token.RefreshToken != ""),
					zap.Bool("is_expired", token.ExpiresAt.Before(now)),
					zap.Duration("time_until_expiry", token.ExpiresAt.Sub(now)))

				if token.ExpiresAt.After(now) {
					// Token not expired - schedule proactive refresh using hybrid strategy
					m.logger.Debug("Scheduling proactive refresh for non-expired token",
						zap.String("server", serverName),
						zap.Time("expires_at", token.ExpiresAt))
					m.scheduleRefreshLocked(serverName, token.ExpiresAt)
					scheduled++
				} else if token.RefreshToken != "" {
					// Access token expired but has refresh token - queue for immediate refresh
					tokenAge := now.Sub(token.ExpiresAt)
					m.logger.Info("OAuth token refresh needed at startup",
						zap.String("server", serverName),
						zap.Duration("expired_for", tokenAge),
						zap.Time("expired_at", token.ExpiresAt))

					// Create schedule entry in retrying state
					m.schedules[serverName] = &RefreshSchedule{
						ServerName:   serverName,
						ExpiresAt:    token.ExpiresAt,
						RefreshState: RefreshStateRetrying,
						RetryBackoff: RetryBackoffBase,
						MaxBackoff:   MaxRetryBackoff,
					}

					tokensToRefresh = append(tokensToRefresh, serverName)
					immediateRefresh++
				} else {
					// Both access and refresh tokens expired - needs re-authentication
					m.logger.Warn("OAuth token fully expired at startup - re-authentication required",
						zap.String("server", serverName),
						zap.Time("expired_at", token.ExpiresAt))

					// Create schedule entry in failed state
					m.schedules[serverName] = &RefreshSchedule{
						ServerName:   serverName,
						ExpiresAt:    token.ExpiresAt,
						RefreshState: RefreshStateFailed,
						LastError:    "Token expired and no refresh token available",
					}
					expired++
				}
			}

			m.logger.Info("Loaded existing tokens",
				zap.Int("total", len(tokens)),
				zap.Int("scheduled", scheduled),
				zap.Int("immediate_refresh", immediateRefresh),
				zap.Int("expired", expired))

			// Execute immediate refreshes asynchronously (after releasing the lock)
			if len(tokensToRefresh) > 0 {
				m.logger.Info("Starting asynchronous refresh for expired tokens",
					zap.Int("count", len(tokensToRefresh)),
					zap.Strings("servers", tokensToRefresh))
				go m.executeStartupRefreshes(tokensToRefresh)
			}
		}
	} else {
		m.logger.Warn("RefreshManager started with nil storage - token persistence disabled")
	}

	m.logger.Info("RefreshManager startup complete",
		zap.Int("schedules_created", len(m.schedules)))

	return nil
}

// executeStartupRefreshes attempts immediate refresh for expired tokens at startup.
// This runs asynchronously to not block Start().
func (m *RefreshManager) executeStartupRefreshes(serverNames []string) {
	for _, serverName := range serverNames {
		// Check if context is cancelled
		if m.ctx.Err() != nil {
			return
		}

		m.logger.Info("OAuth token refresh attempt at startup",
			zap.String("server", serverName))

		m.executeImmediateRefresh(serverName)
	}
}

// executeImmediateRefresh attempts an immediate token refresh for expired tokens.
// This is called at startup for tokens with expired access tokens but valid refresh tokens.
func (m *RefreshManager) executeImmediateRefresh(serverName string) {
	m.mu.Lock()
	schedule, ok := m.schedules[serverName]
	if !ok {
		m.mu.Unlock()
		return
	}

	// Check rate limiting
	if m.isRateLimited(schedule) {
		timeSince := time.Since(schedule.LastAttempt)
		waitTime := RetryBackoffBase - timeSince
		m.mu.Unlock()

		m.logger.Debug("OAuth token refresh rate limited",
			zap.String("server", serverName),
			zap.Duration("wait", waitTime))

		// Reschedule after rate limit expires
		m.rescheduleAfterDelay(serverName, waitTime)
		return
	}

	// Update last attempt time
	schedule.LastAttempt = time.Now()
	m.mu.Unlock()

	// Get token info for logging
	var tokenAge time.Duration
	if m.storage != nil {
		if token := m.lookupToken(serverName); token != nil {
			tokenAge = time.Since(token.Updated)
		}
	}

	// Log the refresh attempt
	LogActualTokenRefreshAttempt(m.logger, serverName, tokenAge)

	// Attempt refresh
	startTime := time.Now()
	var refreshErr error
	if m.runtime != nil {
		refreshErr = m.runtime.RefreshOAuthToken(serverName)
	} else {
		refreshErr = ErrRefreshFailed
	}
	duration := time.Since(startTime)

	// Log the result
	LogActualTokenRefreshResult(m.logger, serverName, refreshErr == nil, duration, refreshErr)

	// Record metrics (T014: Emit metrics on refresh attempt)
	if m.metricsRecorder != nil {
		result := classifyRefreshError(refreshErr)
		m.metricsRecorder.RecordOAuthRefresh(serverName, result)
		m.metricsRecorder.RecordOAuthRefreshDuration(serverName, result, duration)
	}

	if refreshErr != nil {
		m.handleRefreshFailureFor(serverName, refreshErr, sameSchedule(schedule))
	} else {
		m.handleRefreshSuccess(serverName)
	}
}

// Stop cancels all scheduled refreshes and cleans up resources.
func (m *RefreshManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return
	}

	m.logger.Info("Stopping RefreshManager", zap.Int("active_schedules", len(m.schedules)))

	DefaultRefreshCoordinator().SetCompletionHook(nil)

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

// RefreshStateInfo contains refresh state information for health status reporting.
type RefreshStateInfo struct {
	State       RefreshState // Current refresh state
	RetryCount  int          // Number of retry attempts
	LastError   string       // Last error message
	NextAttempt *time.Time   // When next refresh attempt is scheduled
	ExpiresAt   time.Time    // When the token expires
}

// GetRefreshState returns the current refresh state for a server.
// This is used by the health calculator to determine health status.
// Returns nil if no schedule exists for the server.
func (m *RefreshManager) GetRefreshState(serverName string) *RefreshStateInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	schedule := m.schedules[serverName]
	if schedule == nil {
		return nil
	}

	info := &RefreshStateInfo{
		State:      schedule.RefreshState,
		RetryCount: schedule.RetryCount,
		LastError:  schedule.LastError,
		ExpiresAt:  schedule.ExpiresAt,
	}

	// Set next attempt time if scheduled
	if !schedule.ScheduledRefresh.IsZero() {
		info.NextAttempt = &schedule.ScheduledRefresh
	}

	return info
}

// scheduleRefreshLocked schedules a proactive refresh for a token.
// Must be called with m.mu held.
//
// Uses a hybrid refresh strategy (industry best practice):
//   - Refresh at the EARLIER of: (threshold * lifetime) OR (expiry - MinRefreshBuffer)
//   - This ensures short-lived tokens get adequate buffer time for retries
//   - Long-lived tokens refresh at 75% of lifetime (e.g., 1-hour token → 45 min)
//   - Short-lived tokens get at least 5 minutes buffer (e.g., 10-min token → 5 min)
func (m *RefreshManager) scheduleRefreshLocked(serverName string, expiresAt time.Time) {
	now := time.Now()

	lifetime := expiresAt.Sub(now)
	if lifetime <= 0 {
		m.logger.Debug("Token already expired, skipping schedule",
			zap.String("server", serverName),
			zap.Time("expires_at", expiresAt))
		return
	}

	// Hybrid refresh calculation:
	// 1. Percentage-based: refresh at threshold% of lifetime (default 75%)
	// 2. Buffer-based: refresh at (expiry - MinRefreshBuffer) for minimum safety margin
	// Use the EARLIER of the two to ensure adequate time for retries
	percentageDelay := time.Duration(float64(lifetime) * m.threshold)
	bufferDelay := lifetime - MinRefreshBuffer

	// Choose the earlier refresh time (smaller delay)
	var refreshDelay time.Duration
	var strategy string
	if bufferDelay > 0 && bufferDelay < percentageDelay {
		// Buffer-based is earlier - use it for short-lived tokens
		refreshDelay = bufferDelay
		strategy = "buffer-based"
	} else {
		// Percentage-based is earlier or buffer would be negative
		refreshDelay = percentageDelay
		strategy = "percentage-based"
	}

	// Ensure minimum interval (prevents hammering on very short tokens)
	if refreshDelay < MinRefreshInterval {
		refreshDelay = MinRefreshInterval
	}

	refreshAt := now.Add(refreshDelay)

	// Final safety check: ensure we're not scheduling after expiration
	if refreshAt.After(expiresAt.Add(-MinRefreshInterval)) {
		refreshAt = expiresAt.Add(-MinRefreshInterval)
		refreshDelay = refreshAt.Sub(now)
		if refreshDelay <= 0 {
			m.logger.Debug("Token too close to expiration for proactive refresh",
				zap.String("server", serverName),
				zap.Time("expires_at", expiresAt))
			return
		}
		strategy = "minimum-interval"
	}

	// Create or update schedule
	schedule := &RefreshSchedule{
		ServerName:       serverName,
		ExpiresAt:        expiresAt,
		ScheduledRefresh: refreshAt,
		RetryCount:       0,
		RefreshState:     RefreshStateScheduled,
		MaxBackoff:       MaxRetryBackoff,
	}

	// Start timer
	schedule.Timer = time.AfterFunc(refreshDelay, func() {
		m.executeRefresh(serverName)
	})

	m.schedules[serverName] = schedule

	m.logger.Info("OAuth token refresh scheduled",
		zap.String("server", serverName),
		zap.Time("expires_at", expiresAt),
		zap.Time("refresh_at", refreshAt),
		zap.Duration("delay", refreshDelay),
		zap.Duration("buffer", expiresAt.Sub(refreshAt)),
		zap.String("strategy", strategy),
		zap.Float64("threshold", m.threshold))
}

// executeRefresh performs the token refresh for a server.
func (m *RefreshManager) executeRefresh(serverName string) {
	m.mu.Lock()
	started, ok := m.schedules[serverName]
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

	// Attempt refresh with timing for metrics (T022: Emit refresh duration metric)
	startTime := time.Now()
	var refreshErr error
	if m.runtime != nil {
		refreshErr = m.runtime.RefreshOAuthToken(serverName)
	} else {
		refreshErr = ErrRefreshFailed
	}
	duration := time.Since(startTime)

	// Record metrics (T022: Emit refresh duration metric on each attempt)
	if m.metricsRecorder != nil {
		result := classifyRefreshError(refreshErr)
		m.metricsRecorder.RecordOAuthRefresh(serverName, result)
		m.metricsRecorder.RecordOAuthRefreshDuration(serverName, result, duration)
	}

	if refreshErr != nil {
		m.handleRefreshFailureFor(serverName, refreshErr, sameSchedule(started))
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
		schedule.RefreshState = RefreshStateIdle
		schedule.RetryBackoff = 0
	}
	m.mu.Unlock()

	m.logger.Info("OAuth token refresh succeeded",
		zap.String("server", serverName))

	// Get the new token expiration to emit event
	if m.storage != nil {
		token := m.lookupToken(serverName)
		if token != nil && m.eventEmitter != nil {
			m.eventEmitter.EmitOAuthTokenRefreshed(serverName, token.ExpiresAt)
		}
	}

	// Note: The token store hook (OnTokenSaved) will reschedule the next refresh
}

// handleRefreshFailure handles a failed token refresh with exponential backoff retry.
// Terminal errors (invalid_grant, server not found) stop immediately.
// Transient errors retry with exponential backoff up to maxRetries.
func (m *RefreshManager) handleRefreshFailure(serverName string, err error) {
	m.handleRefreshFailureFor(serverName, err, nil)
}

// sameSchedule matches only the schedule an attempt started from: a login
// whose OnTokenSaved replaced it meanwhile supersedes the attempt's failure.
func sameSchedule(started *RefreshSchedule) func(*RefreshSchedule) bool {
	return func(s *RefreshSchedule) bool { return s == started }
}

// handleRefreshFailureFor is handleRefreshFailure applied only when match
// accepts the current schedule (nil matches any). It keeps the failure off a
// schedule a login created after the failed attempt (Spec 113 FR-006a).
func (m *RefreshManager) handleRefreshFailureFor(serverName string, err error, match func(*RefreshSchedule) bool) {
	// Classify the error for metrics and handling (Spec 113 FR-007/FR-008).
	cls, httpStatus := ClassifyRefreshError(err)
	errorType := cls.MetricLabel()

	m.mu.Lock()
	schedule := m.schedules[serverName]
	if schedule == nil || (match != nil && !match(schedule)) {
		m.mu.Unlock()
		return
	}
	// A terminal failure is reported once: the coordinator hook (reactive
	// flight) and a proactive attempt answered by the coordinator's latch
	// carry the same outcome.
	if cls.IsTerminal() && schedule.RefreshState == RefreshStateFailed {
		m.mu.Unlock()
		return
	}

	schedule.RetryCount++
	schedule.LastError = err.Error()
	schedule.RefreshState = RefreshStateRetrying
	retryCount := schedule.RetryCount
	expiresAt := schedule.ExpiresAt
	m.mu.Unlock()

	m.logger.Warn("OAuth token refresh failed",
		zap.String("server", serverName),
		zap.Error(err),
		zap.String("error_type", errorType),
		zap.Int("http_status", httpStatus),
		zap.Int("retry_count", retryCount))

	// Terminal failures stop immediately: the refresh token is invalid
	// (invalid_grant), the client was rejected (invalid_client, FR-009), the
	// grant/scope is not allowed, or the server is gone. No amount of retrying
	// fixes these; the user has to sign in again (or fix the config).
	if cls.IsTerminal() {
		lastError := err.Error()
		switch cls {
		case RefreshClassInvalidGrant:
			lastError = "Refresh token expired or revoked - re-authentication required"
		case RefreshClassInvalidClient, RefreshClassTerminalOther:
			var failure *RefreshFailure
			if errors.As(err, &failure) && failure.Message != "" {
				lastError = failure.Message
			}
		}
		m.logger.Error("OAuth token refresh failed permanently - re-authentication required",
			zap.String("server", serverName),
			zap.String("error_type", errorType),
			zap.Error(err))

		if m.failScheduleIfCurrent(serverName, schedule, lastError, true) && m.eventEmitter != nil {
			m.eventEmitter.EmitOAuthRefreshFailed(serverName, err.Error())
		}
		return
	}

	// Check if max retries exceeded (circuit breaker to prevent infinite loops).
	if m.maxRetries > 0 && retryCount >= m.maxRetries {
		m.logger.Error("OAuth token refresh failed - max retries exceeded",
			zap.String("server", serverName),
			zap.Int("max_retries", m.maxRetries),
			zap.Int("retry_count", retryCount))

		if m.failScheduleIfCurrent(serverName, schedule, "Max retries exceeded - re-authentication required", false) && m.eventEmitter != nil {
			m.eventEmitter.EmitOAuthRefreshFailed(serverName, err.Error())
		}
		return
	}

	// Check if we should continue retrying based on token expiration.
	// Only stop if the access token has completely expired AND no more time remains.
	now := time.Now()
	if !expiresAt.IsZero() && now.After(expiresAt) {
		// Token has already expired - check if we should give up
		// We'll keep trying as long as there's a chance the refresh token is still valid
		// Only give up if we've been trying for too long (MaxExpiredTokenAge)
		timeSinceExpiry := now.Sub(expiresAt)
		if timeSinceExpiry > MaxExpiredTokenAge {
			m.logger.Error("OAuth token refresh failed - token expired too long ago",
				zap.String("server", serverName),
				zap.Duration("expired_for", timeSinceExpiry),
				zap.Int("retries", retryCount))

			if m.failScheduleIfCurrent(serverName, schedule, "", false) && m.eventEmitter != nil {
				m.eventEmitter.EmitOAuthRefreshFailed(serverName, err.Error())
			}
			return
		}
	}

	// Calculate backoff delay using exponential backoff with cap
	backoff := m.calculateBackoff(retryCount - 1) // -1 because we just incremented
	m.rescheduleAfterDelayFor(serverName, backoff, schedule)
}

// failScheduleIfCurrent marks the schedule failed only while it is still the
// one the failure was recorded against. A login whose OnTokenSaved replaced
// the schedule after handleRefreshFailure released m.mu supersedes the
// failure (Spec 113 FR-006a): the new grant's schedule keeps running and no
// stale failure event is emitted. lastError "" keeps the current LastError.
func (m *RefreshManager) failScheduleIfCurrent(serverName string, expected *RefreshSchedule, lastError string, stopTimer bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	schedule := m.schedules[serverName]
	if schedule == nil || schedule != expected {
		return false
	}
	schedule.RefreshState = RefreshStateFailed
	if lastError != "" {
		schedule.LastError = lastError
	}
	if stopTimer && schedule.Timer != nil {
		schedule.Timer.Stop()
	}
	return true
}

// onRefreshOutcome is the RefreshCoordinator completion hook. Proactive
// flights are handled by executeRefresh with the error it gets back; this only
// applies terminal outcomes of reactive flights to the schedule.
func (m *RefreshManager) onRefreshOutcome(o RefreshOutcome) {
	if o.Trigger != RefreshTriggerReactive || !o.Class.IsTerminal() || o.Err == nil {
		return
	}
	var match func(*RefreshSchedule) bool
	if !o.ExpiresAt.IsZero() {
		// Only the schedule built for the token the flight failed on.
		match = func(s *RefreshSchedule) bool { return s.ExpiresAt.Equal(o.ExpiresAt) }
	}
	m.handleRefreshFailureFor(o.ServerName, o.Err, match)
}

// lookupToken finds the token record of a server by display name (Spec 113
// FR-010). Records are keyed by GenerateServerKey(name, url), which this
// manager cannot compute (it only knows the name), so it falls back to the
// record whose DisplayName matches, preferring the most recently updated one.
// A legacy record keyed by the bare name is still found directly.
func (m *RefreshManager) lookupToken(serverName string) *storage.OAuthTokenRecord {
	if m.storage == nil {
		return nil
	}
	if token, err := m.storage.GetOAuthToken(serverName); err == nil && token != nil {
		return token
	}
	tokens, err := m.storage.ListOAuthTokens()
	if err != nil {
		return nil
	}
	var best *storage.OAuthTokenRecord
	for _, t := range tokens {
		if t == nil || t.GetServerName() != serverName {
			continue
		}
		if best == nil || t.Updated.After(best.Updated) {
			best = t
		}
	}
	return best
}

// classifyRefreshError returns the metric result label for a refresh error.
// Classification is structural (ClassifyRefreshError, Spec 113 FR-007); the
// label set keeps the pre-Spec-113 values and adds failed_invalid_client and
// failed_server_error.
func classifyRefreshError(err error) string {
	cls, _ := ClassifyRefreshError(err)
	return cls.MetricLabel()
}

// maxBackoffExponent is the maximum shift exponent that won't overflow when
// multiplied by RetryBackoffBase (10s = 10_000_000_000 ns). On 64-bit systems,
// 1<<30 * 10e9 overflows int64. We cap at 25 which gives 10s * 2^25 = 335,544,320s
// — well above MaxRetryBackoff (300s), so the cap applies naturally.
const maxBackoffExponent = 25

// calculateBackoff calculates the exponential backoff duration for a given retry count.
// The formula is: base * 2^retryCount, capped at MaxRetryBackoff (5 minutes).
// Sequence: 10s → 20s → 40s → 80s → 160s → 300s (cap).
//
// The exponent is capped at maxBackoffExponent to prevent integer overflow
// that caused issue #310 (0s delay at high retry counts leading to infinite loops).
func (m *RefreshManager) calculateBackoff(retryCount int) time.Duration {
	if retryCount < 0 {
		retryCount = 0
	}
	// Cap the exponent to prevent integer overflow.
	// Beyond this exponent, the result would exceed MaxRetryBackoff anyway.
	if retryCount > maxBackoffExponent {
		return MaxRetryBackoff
	}
	backoff := RetryBackoffBase * time.Duration(1<<uint(retryCount))
	if backoff <= 0 || backoff > MaxRetryBackoff {
		backoff = MaxRetryBackoff
	}
	return backoff
}

// isRateLimited checks if a refresh attempt would violate the rate limit.
// Per FR-008: minimum 10 seconds between refresh attempts per server.
func (m *RefreshManager) isRateLimited(schedule *RefreshSchedule) bool {
	if schedule == nil || schedule.LastAttempt.IsZero() {
		return false
	}
	timeSinceLastAttempt := time.Since(schedule.LastAttempt)
	return timeSinceLastAttempt < RetryBackoffBase
}

// rescheduleAfterDelay reschedules a refresh attempt after a delay.
// The delay is enforced to be at least MinRefreshInterval to prevent tight loops.
func (m *RefreshManager) rescheduleAfterDelay(serverName string, delay time.Duration) {
	m.rescheduleAfterDelayFor(serverName, delay, nil)
}

// rescheduleAfterDelayFor reschedules only while expected (when non-nil) is
// still the server's schedule: a login's OnTokenSaved that replaced it keeps
// its own timer instead of inheriting the failed attempt's backoff.
func (m *RefreshManager) rescheduleAfterDelayFor(serverName string, delay time.Duration, expected *RefreshSchedule) {
	// Enforce minimum delay to prevent tight retry loops (defense-in-depth for issue #310).
	if delay < MinRefreshInterval {
		delay = MinRefreshInterval
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	schedule, ok := m.schedules[serverName]
	if !ok || (expected != nil && schedule != expected) {
		return
	}

	// Stop existing timer if any
	if schedule.Timer != nil {
		schedule.Timer.Stop()
	}

	// Update schedule with next refresh time
	schedule.ScheduledRefresh = time.Now().Add(delay)
	schedule.RetryBackoff = delay

	// Start new timer
	schedule.Timer = time.AfterFunc(delay, func() {
		m.executeRefresh(serverName)
	})

	m.logger.Info("OAuth token refresh retry scheduled",
		zap.String("server", serverName),
		zap.Duration("delay", delay),
		zap.Time("next_attempt", schedule.ScheduledRefresh),
		zap.Int("retry_count", schedule.RetryCount),
		zap.String("refresh_state", schedule.RefreshState.String()))
}
