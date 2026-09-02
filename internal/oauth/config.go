package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"

	"github.com/mark3labs/mcp-go/client"
	"go.uber.org/zap"
)

const (
	// Default OAuth redirect URI base - port will be dynamically assigned
	DefaultRedirectURIBase = "http://" + config.LoopbackIPv4Host
	// DefaultRedirectPath aliases the canonical callback path. The constant is
	// defined in internal/config because `oauth.redirect_uri` has to be
	// validated where the operator types it, and internal/config cannot import
	// this package.
	DefaultRedirectPath = config.OAuthCallbackPath

	// oauthLoggerName is the logger name every record in this package carries.
	// Log filters and runbooks are written against it, so it is applied exactly
	// once (see namedOAuthLogger).
	oauthLoggerName         = "oauth"
	oauthCallbackLoggerName = "oauth-callback"

	// Rate limit retry constants for resource auto-detection
	resourceDetectMaxRetries     = 3                // Maximum retry attempts on 429
	resourceDetectMaxWait        = 30 * time.Second // Maximum wait time per retry
	resourceDetectDefaultBackoff = 5 * time.Second  // Default backoff when no Retry-After hint
	resourceDetectRequestTimeout = 5 * time.Second  // Timeout for preflight requests
)

// ParsePinnedRedirectURI validates an operator-pinned OAuth redirect URI (the
// per-server `oauth.redirect_uri` config field) and returns the loopback port it
// pins.
//
// The rules live in internal/config (ParseLoopbackRedirectURI) so that the REST
// config API, the MCP `upstream_servers` tool and this flow all reject exactly
// the same values. See that function for the rationale.
func ParsePinnedRedirectURI(rawURI string) (int, error) {
	_, port, err := config.ParseLoopbackRedirectURI(rawURI)
	return port, err
}

// ParsePinnedRedirectURIBinding is ParsePinnedRedirectURI plus the loopback host
// the callback listener must bind. A pinned `http://[::1]:PORT/oauth/callback`
// is only honored if something actually listens on ::1 — binding 127.0.0.1 for
// it produced an authorize URL nobody could call back to.
func ParsePinnedRedirectURIBinding(rawURI string) (bindHost string, port int, err error) {
	return config.ParseLoopbackRedirectURI(rawURI)
}

// namedOAuthLogger returns logger tagged with this package's logger name.
//
// It is idempotent: applying it twice must not produce "oauth.oauth" and break
// every filter written against the documented name. A nil logger falls back to
// the zap global, which is what the pre-logger-threading call sites used.
func namedOAuthLogger(logger *zap.Logger) *zap.Logger {
	if logger == nil {
		return zap.L().Named(oauthLoggerName)
	}
	name := logger.Name()
	if name == oauthLoggerName || strings.HasSuffix(name, "."+oauthLoggerName) {
		return logger
	}
	return logger.Named(oauthLoggerName)
}

// CallbackServerManager manages OAuth callback servers for dynamic port allocation
type CallbackServerManager struct {
	servers map[string]*CallbackServer
	mu      sync.RWMutex

	// logger is guarded by mu. It starts as the zap global, which in this
	// binary is the NO-OP logger (zap.ReplaceGlobals is never called), so every
	// record the callback surface emits would be silently discarded — the
	// tear-down of a live listener, a dropped waiter, a failed bind on a pinned
	// port, and "did the callback ever arrive?" all vanish. SetLogger (and the
	// logger threaded through StartCallbackServerOnHost) replaces it with the
	// caller's real logger the first time a flow runs.
	logger *zap.Logger
}

// SetLogger installs a real logger on the manager. Safe to call repeatedly; a
// nil logger is ignored so a caller without one cannot blind the manager again.
func (m *CallbackServerManager) SetLogger(logger *zap.Logger) {
	if logger == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.adoptLoggerLocked(logger)
}

// adoptLoggerLocked records the caller's logger as the manager logger and
// returns the logger to use for this call. m.mu must be held.
func (m *CallbackServerManager) adoptLoggerLocked(logger *zap.Logger) *zap.Logger {
	if logger != nil {
		m.logger = logger.Named(oauthCallbackLoggerName)
	}
	if m.logger == nil {
		m.logger = zap.L().Named(oauthCallbackLoggerName)
	}
	return m.logger
}

// CallbackServer represents an active OAuth callback server.
//
// Callback parameters are dispatched by the `state` parameter (issue #975):
// every flow registers the state it minted before opening the browser and gets
// its own single-use channel. A callback whose state nobody registered is
// rejected with an explicit failure page instead of being handed to whichever
// flow happened to be listening.
type CallbackServer struct {
	Port        int
	RedirectURI string
	Server      *http.Server
	ServerName  string
	// BindHost is the loopback address the listener is actually bound to
	// ("127.0.0.1" or "::1"). A pinned `http://[::1]:PORT/oauth/callback` is
	// only honored when the listener is on the same family, so the binding is
	// recorded rather than assumed.
	BindHost string
	logger   *zap.Logger

	waitersMu sync.Mutex
	waiters   map[string]chan map[string]string
}

var globalCallbackManager = &CallbackServerManager{
	servers: make(map[string]*CallbackServer),
	logger:  zap.L().Named(oauthCallbackLoggerName),
}

// Global token store manager to persist tokens across client instances
type TokenStoreManager struct {
	stores                  map[string]client.TokenStore
	completedOAuth          map[string]time.Time // Track successful OAuth completions
	mu                      sync.RWMutex
	logger                  *zap.Logger
	oauthCompletionCallback func(serverName string)                      // Callback when OAuth completes
	oauthFailureCallback    func(serverName string, err error)           // Callback when an unattended OAuth flow fails
	tokenSavedCallback      func(serverName string, expiresAt time.Time) // Callback when token is saved
}

var globalTokenStoreManager = &TokenStoreManager{
	stores:         make(map[string]client.TokenStore),
	completedOAuth: make(map[string]time.Time),
	logger:         zap.L().Named("oauth-tokens"),
}

// GetOrCreateTokenStore returns a shared token store for the given server
func (m *TokenStoreManager) GetOrCreateTokenStore(serverName string) client.TokenStore {
	m.mu.Lock()
	defer m.mu.Unlock()

	if store, exists := m.stores[serverName]; exists {
		m.logger.Info("Reusing existing token store",
			zap.String("server", serverName),
			zap.String("note", "tokens should be available if OAuth was completed"))
		return store
	}

	store := client.NewMemoryTokenStore()
	m.stores[serverName] = store
	m.logger.Info("Created new token store", zap.String("server", serverName))
	return store
}

// HasTokenStore checks if a token store exists for the server in memory (for debugging)
// Note: This only checks the in-memory store, not persisted tokens in BBolt.
// Use HasPersistedToken() to check for tokens in persistent storage.
func (m *TokenStoreManager) HasTokenStore(serverName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.stores[serverName]
	return exists
}

// HasPersistedToken checks if a token exists in persistent storage (BBolt) for the server.
// This is the preferred method to check for existing tokens as it reflects actual token availability.
//
// A zero ExpiresAt (time.Time{}) is treated as "no expiration" — some authorization servers
// (e.g. Atlassian's MCP endpoint) issue access tokens without an `expires_in` field, which
// the oauth2 library deserializes as the Go zero time. Returning isExpired=true for those
// records would make the upstream connection loop forever asking for re-auth even though a
// valid token is on disk. This mirrors HasValidToken (see below), which already treats
// IsZero() as "never expires".
func HasPersistedToken(serverName, serverURL string, boltStorage *storage.BoltDB) (hasToken bool, hasRefreshToken bool, isExpired bool) {
	if boltStorage == nil {
		return false, false, false
	}

	serverKey := GenerateServerKey(serverName, serverURL)
	record, err := boltStorage.GetOAuthToken(serverKey)
	if err != nil || record == nil {
		return false, false, false
	}

	hasToken = record.AccessToken != ""
	hasRefreshToken = record.RefreshToken != ""
	if record.ExpiresAt.IsZero() {
		isExpired = false
	} else {
		isExpired = time.Now().After(record.ExpiresAt)
	}
	return
}

// GetPersistedRefreshToken retrieves the refresh token from persistent storage if available.
// Returns empty string if no token exists or token has no refresh_token.
func GetPersistedRefreshToken(serverName, serverURL string, boltStorage *storage.BoltDB) string {
	if boltStorage == nil {
		return ""
	}

	serverKey := GenerateServerKey(serverName, serverURL)
	record, err := boltStorage.GetOAuthToken(serverKey)
	if err != nil || record == nil {
		return ""
	}

	return record.RefreshToken
}

// TokenRefreshResult contains the result of a token refresh attempt.
type TokenRefreshResult struct {
	Success     bool
	NewToken    *storage.OAuthTokenRecord
	Error       error
	Attempt     int
	MaxAttempts int
}

// RefreshTokenConfig contains configuration for token refresh operations.
type RefreshTokenConfig struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// DefaultRefreshConfig returns the default token refresh configuration.
func DefaultRefreshConfig() RefreshTokenConfig {
	return RefreshTokenConfig{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     10 * time.Second,
	}
}

// GetTokenStoreManager returns the global token store manager for debugging
func GetTokenStoreManager() *TokenStoreManager {
	return globalTokenStoreManager
}

// SetOAuthCompletionCallback sets a callback function to be called when OAuth completes
func (m *TokenStoreManager) SetOAuthCompletionCallback(callback func(serverName string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.oauthCompletionCallback = callback
}

// SetOAuthFailureCallback sets a callback invoked when an OAuth flow fails in a
// place that has no caller to return the error to — a callback that could not
// be delivered, a state mismatch, or a background token exchange that failed.
// The upstream manager wires this to the server's connection status so the
// failure is visible to the operator rather than only in the log (issue #975).
// Pass nil to clear it.
func (m *TokenStoreManager) SetOAuthFailureCallback(callback func(serverName string, err error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.oauthFailureCallback = callback
}

// RecordOAuthFailure reports an OAuth failure that nobody is waiting on.
func (m *TokenStoreManager) RecordOAuthFailure(serverName string, err error) {
	if err == nil {
		return
	}

	m.mu.RLock()
	callback := m.oauthFailureCallback
	m.mu.RUnlock()

	m.logger.Warn("OAuth failure recorded",
		zap.String("server", serverName),
		zap.Error(err))

	if callback != nil {
		callback(serverName, err)
	}
}

// SetTokenSavedCallback sets a callback function to be called when a token is saved.
// Used by RefreshManager to reschedule proactive refresh when tokens are updated.
func (m *TokenStoreManager) SetTokenSavedCallback(callback func(serverName string, expiresAt time.Time)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokenSavedCallback = callback
}

// NotifyTokenSaved triggers the token saved callback if set.
// Called by PersistentTokenStore.SaveToken() to notify the RefreshManager.
func (m *TokenStoreManager) NotifyTokenSaved(serverName string, expiresAt time.Time) {
	m.mu.RLock()
	callback := m.tokenSavedCallback
	m.mu.RUnlock()

	if callback != nil {
		callback(serverName, expiresAt)
	}
}

// MarkOAuthCompleted records that OAuth was successfully completed for a server
// This method is used by CLI processes to notify server processes about OAuth completion
func (m *TokenStoreManager) MarkOAuthCompleted(serverName string) {
	m.mu.Lock()
	callback := m.oauthCompletionCallback
	completionTime := time.Now()
	m.completedOAuth[serverName] = completionTime
	m.mu.Unlock()

	m.logger.Info("OAuth completion recorded",
		zap.String("server", serverName),
		zap.Time("completion_time", completionTime))

	// Trigger in-process callback if available (for same process)
	if callback != nil {
		m.logger.Info("Triggering in-process OAuth completion callback",
			zap.String("server", serverName))
		callback(serverName)
	} else {
		m.logger.Info("No in-process callback registered - OAuth completion will be handled via database events",
			zap.String("server", serverName))
	}
}

// MarkOAuthCompletedWithDB records OAuth completion in the database for cross-process notification
// This is the new preferred method that works across different processes
func (m *TokenStoreManager) MarkOAuthCompletedWithDB(serverName string, storage DatabaseOAuthNotifier) error {
	m.mu.Lock()
	completionTime := time.Now()
	m.completedOAuth[serverName] = completionTime
	m.mu.Unlock()

	// Save to database for cross-process notification
	event := &CompletionEvent{
		ServerName:  serverName,
		CompletedAt: completionTime,
	}

	if err := storage.SaveOAuthCompletionEvent(event); err != nil {
		m.logger.Error("Failed to save OAuth completion event to database",
			zap.String("server", serverName),
			zap.Error(err))
		return err
	}

	m.logger.Info("OAuth completion saved to database for cross-process notification",
		zap.String("server", serverName),
		zap.Time("completion_time", completionTime))

	return nil
}

// DatabaseOAuthNotifier interface for database-based OAuth completion notifications
type DatabaseOAuthNotifier interface {
	SaveOAuthCompletionEvent(event *CompletionEvent) error
}

// OAuthCompletionEvent represents an OAuth completion event (re-exported from storage)
type CompletionEvent = storage.OAuthCompletionEvent

// HasRecentOAuthCompletion checks if OAuth was recently completed for a server
func (m *TokenStoreManager) HasRecentOAuthCompletion(serverName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	completionTime, exists := m.completedOAuth[serverName]
	if !exists {
		return false
	}

	// Consider OAuth recent if completed within last 5 minutes
	isRecent := time.Since(completionTime) < 5*time.Minute
	m.logger.Debug("Checking OAuth completion status",
		zap.String("server", serverName),
		zap.Time("completion_time", completionTime),
		zap.Bool("is_recent", isRecent))

	return isRecent
}

// HasValidToken checks if a server has a valid, non-expired OAuth token
// Returns true if token exists and hasn't expired (with grace period)
func (m *TokenStoreManager) HasValidToken(ctx context.Context, serverName string, storage *storage.BoltDB) bool {
	m.mu.RLock()
	store, exists := m.stores[serverName]
	m.mu.RUnlock()

	if !exists {
		m.logger.Debug("No token store found for server",
			zap.String("server", serverName))
		return false
	}

	// Try to get token from persistent store if available
	if persistentStore, ok := store.(*PersistentTokenStore); ok && storage != nil {
		token, err := persistentStore.GetToken(ctx)
		if err != nil {
			// No token or error retrieving it
			m.logger.Debug("Failed to retrieve token from persistent store",
				zap.String("server", serverName),
				zap.Error(err))
			return false
		}

		// Check if token is expired (considering grace period)
		now := time.Now()
		if token.ExpiresAt.IsZero() {
			// No expiration time means token is always valid (unusual but possible)
			m.logger.Debug("Token has no expiration, treating as valid",
				zap.String("server", serverName))
			return true
		}

		isExpired := now.After(token.ExpiresAt)
		m.logger.Debug("Token expiration check",
			zap.String("server", serverName),
			zap.Bool("is_expired", isExpired),
			zap.Time("expires_at", token.ExpiresAt),
			zap.Duration("time_until_expiry", token.ExpiresAt.Sub(now)))

		return !isExpired
	}

	// For in-memory stores, just check if store exists
	// (no expiration checking for non-persistent stores)
	m.logger.Debug("Using in-memory token store, assuming valid",
		zap.String("server", serverName))
	return true
}

// CreateOAuthConfigWithExtraParams creates an OAuth configuration and returns auto-detected extra parameters.
// This function implements RFC 8707 resource auto-detection for zero-config OAuth.
//
// The function returns:
//   - *client.OAuthConfig: The OAuth configuration for mcp-go client
//   - map[string]string: Extra parameters (including auto-detected resource) to inject into authorization URL
//
// Resource auto-detection logic (in priority order):
//  1. Manual extra_params.resource from config (highest priority - preserves backward compatibility)
//  2. Auto-detected resource from RFC 9728 Protected Resource Metadata
//  3. Fallback to server URL if metadata is unavailable or lacks resource field
func CreateOAuthConfigWithExtraParams(ctx context.Context, serverConfig *config.ServerConfig, storage *storage.BoltDB) (*client.OAuthConfig, map[string]string) {
	cfg, extraParams, _ := CreateOAuthConfigWithExtraParamsAndLogger(ctx, serverConfig, storage, nil)
	return cfg, extraParams
}

// CreateOAuthConfigWithExtraParamsAndLogger is CreateOAuthConfigWithExtraParams
// with the caller's logger and the reason it failed.
//
// Both additions exist because this package used to log through zap.L(), and
// zap.ReplaceGlobals is never called in this binary — so zap.L() is the NO-OP
// logger and every diagnostic here was silently discarded, including the one
// that names a malformed `oauth.redirect_uri`. Callers now pass a real logger
// and surface the returned error instead of reporting the generic "server may
// not support OAuth".
func CreateOAuthConfigWithExtraParamsAndLogger(ctx context.Context, serverConfig *config.ServerConfig, storage *storage.BoltDB, logger *zap.Logger) (*client.OAuthConfig, map[string]string, error) {
	logger = namedOAuthLogger(logger)

	// Initialize extraParams map
	extraParams := make(map[string]string)

	// Priority 1: Check for manual extra_params.resource from config
	if serverConfig.OAuth != nil && len(serverConfig.OAuth.ExtraParams) > 0 {
		for key, value := range serverConfig.OAuth.ExtraParams {
			extraParams[key] = value
		}
		if resource, hasResource := extraParams["resource"]; hasResource {
			logger.Info("Using manual resource parameter from config",
				zap.String("server", serverConfig.Name),
				zap.String("resource", logSafeURL(resource)))
		}
	}

	// Priority 2 & 3: Auto-detect resource if not manually specified
	if _, hasResource := extraParams["resource"]; !hasResource {
		detectedResource := autoDetectResource(ctx, serverConfig, logger)
		if detectedResource != "" {
			extraParams["resource"] = detectedResource
		}
	}

	// Create the base OAuth config, passing extraParams for transport wrapper injection
	oauthConfig, err := createOAuthConfigInternal(serverConfig, storage, extraParams, logger)

	return oauthConfig, extraParams, err
}

// parseRateLimitWait extracts wait duration from a 429 response.
// Checks (in order):
// 1. Retry-After header (seconds or HTTP-date per RFC 7231)
// 2. JSON body with reset_at field (Unix timestamp)
// Returns 0 if no hints found (caller should use backoff).
func parseRateLimitWait(resp *http.Response, body []byte) time.Duration {
	// Try Retry-After header first (RFC 7231)
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		// Try parsing as seconds
		if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		// Try parsing as HTTP-date
		if t, err := http.ParseTime(retryAfter); err == nil {
			wait := time.Until(t)
			if wait > 0 {
				return wait
			}
		}
	}

	// Try JSON body with reset_at (common pattern in rate limit responses)
	// Supports both top-level and nested in "detail" object
	var rateLimit struct {
		ResetAt int64 `json:"reset_at"`
		Detail  struct {
			ResetAt int64 `json:"reset_at"`
		} `json:"detail"`
	}
	if json.Unmarshal(body, &rateLimit) == nil {
		resetAt := rateLimit.ResetAt
		if resetAt == 0 {
			resetAt = rateLimit.Detail.ResetAt
		}
		if resetAt > 0 {
			wait := time.Until(time.Unix(resetAt, 0))
			if wait > 0 {
				return wait
			}
		}
	}

	return 0 // No hints, caller should use backoff
}

// parseRateLimitWaitWithSource extracts wait duration and its source from a 429 response.
// Returns (duration, source) where source is one of:
// - "retry-after-header" (Retry-After header with seconds or HTTP-date)
// - "json-body-reset-at" (JSON body with reset_at field)
// - "" (no hints found, caller should use backoff)
func parseRateLimitWaitWithSource(resp *http.Response, body []byte) (time.Duration, string) {
	// Try Retry-After header first (RFC 7231)
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		// Try parsing as seconds
		if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second, "retry-after-header"
		}
		// Try parsing as HTTP-date
		if t, err := http.ParseTime(retryAfter); err == nil {
			wait := time.Until(t)
			if wait > 0 {
				return wait, "retry-after-header"
			}
		}
	}

	// Try JSON body with reset_at (common pattern in rate limit responses)
	// Supports both top-level and nested in "detail" object
	var rateLimit struct {
		ResetAt int64 `json:"reset_at"`
		Detail  struct {
			ResetAt int64 `json:"reset_at"`
		} `json:"detail"`
	}
	if json.Unmarshal(body, &rateLimit) == nil {
		resetAt := rateLimit.ResetAt
		if resetAt == 0 {
			resetAt = rateLimit.Detail.ResetAt
		}
		if resetAt > 0 {
			wait := time.Until(time.Unix(resetAt, 0))
			if wait > 0 {
				return wait, "json-body-reset-at"
			}
		}
	}

	return 0, "" // No hints, caller should use backoff
}

// autoDetectResource attempts to discover the RFC 8707 resource parameter.
// Returns the detected resource URL, or server URL as fallback, or empty string if
// the server clearly doesn't require OAuth.
//
// This function handles rate limiting (429) by retrying with appropriate backoff,
// respecting Retry-After headers and JSON body reset_at fields.
// The context allows cancellation during rate limit waits (e.g., during shutdown).
func autoDetectResource(ctx context.Context, serverConfig *config.ServerConfig, logger *zap.Logger) string {
	httpClient := &http.Client{Timeout: resourceDetectRequestTimeout}

	for attempt := 0; attempt <= resourceDetectMaxRetries; attempt++ {
		// Check context before making request
		if ctx.Err() != nil {
			logger.Debug("Context cancelled before resource detection request",
				zap.String("server", serverConfig.Name),
				zap.Int("attempt", attempt),
				zap.Error(ctx.Err()))
			return serverConfig.URL
		}

		// POST is the only method guaranteed by MCP spec for the main endpoint
		req, err := http.NewRequestWithContext(ctx, "POST", serverConfig.URL, strings.NewReader("{}"))
		if err != nil {
			logger.Debug("Failed to create request for resource detection",
				zap.String("server", serverConfig.Name),
				zap.Int("attempt", attempt),
				zap.Error(err))
			return serverConfig.URL
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			logger.Debug("Failed to make preflight request for resource detection",
				zap.String("server", serverConfig.Name),
				zap.Int("attempt", attempt),
				zap.Error(err))
			// Network error - fallback to server URL per spec
			return serverConfig.URL
		}

		// Read body for potential rate limit info or later use
		// Limit read size to prevent memory exhaustion from unexpectedly large responses
		const maxBodySize = 1 << 20 // 1MB
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			// 401 - Normal path: extract resource from metadata
			return handleUnauthorizedResponse(resp, body, serverConfig, logger)

		case resp.StatusCode == http.StatusTooManyRequests:
			// 429 - Rate limited: retry with appropriate wait time
			if attempt < resourceDetectMaxRetries {
				waitTime, waitSource := parseRateLimitWaitWithSource(resp, body)
				if waitTime == 0 {
					// No hint from server, use exponential backoff
					waitTime = resourceDetectDefaultBackoff * time.Duration(1<<uint(attempt))
					waitSource = "exponential-backoff"
				}
				// Cap wait time to avoid blocking too long
				if waitTime > resourceDetectMaxWait {
					waitTime = resourceDetectMaxWait
				}

				logger.Info("Rate limited during resource auto-detection, waiting before retry",
					zap.String("server", serverConfig.Name),
					zap.Int("attempt", attempt+1),
					zap.Int("max_attempts", resourceDetectMaxRetries+1),
					zap.Duration("wait_time", waitTime),
					zap.String("wait_source", waitSource))

				// Context-aware wait: allows cancellation during shutdown
				select {
				case <-time.After(waitTime):
					continue
				case <-ctx.Done():
					logger.Debug("Rate-limit wait cancelled",
						zap.String("server", serverConfig.Name),
						zap.Int("attempt", attempt+1),
						zap.Duration("remaining_wait", waitTime),
						zap.Error(ctx.Err()))
					return serverConfig.URL
				}
			}

			// Exhausted retries - fallback to server URL
			logger.Warn("Rate limited during resource auto-detection, exhausted retries, using server URL as fallback",
				zap.String("server", serverConfig.Name),
				zap.Int("attempts", resourceDetectMaxRetries+1),
				zap.String("fallback_resource", logSafeURL(serverConfig.URL)))
			return serverConfig.URL

		case resp.StatusCode >= 400:
			// Other 4xx/5xx errors - can't determine auth requirements, fallback to server URL
			logger.Debug("Server returned error during resource auto-detection, using server URL as fallback",
				zap.String("server", serverConfig.Name),
				zap.Int("status_code", resp.StatusCode),
				zap.String("fallback_resource", logSafeURL(serverConfig.URL)))
			return serverConfig.URL

		default:
			// 2xx/3xx - Server doesn't require authentication at this endpoint
			logger.Debug("Server did not return 401, skipping resource auto-detection",
				zap.String("server", serverConfig.Name),
				zap.Int("status_code", resp.StatusCode))
			return ""
		}
	}

	// Should not reach here, but fallback just in case
	return serverConfig.URL
}

// handleUnauthorizedResponse processes a 401 response to extract the resource parameter.
func handleUnauthorizedResponse(resp *http.Response, body []byte, serverConfig *config.ServerConfig, logger *zap.Logger) string {
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	metadataURL := ExtractResourceMetadataURL(wwwAuth)

	if metadataURL != "" {
		// Try to fetch Protected Resource Metadata
		metadata, err := DiscoverProtectedResourceMetadata(metadataURL, resourceDetectRequestTimeout)
		if err != nil {
			logger.Debug("Failed to fetch Protected Resource Metadata",
				zap.String("server", serverConfig.Name),
				zap.String("metadata_url", logSafeURL(metadataURL)),
				zap.Error(err))
			// Fallback to server URL
			return serverConfig.URL
		}

		// Use resource from metadata if available
		if metadata.Resource != "" {
			logger.Info("Auto-detected resource parameter from Protected Resource Metadata (RFC 9728)",
				zap.String("server", serverConfig.Name),
				zap.String("resource", logSafeURL(metadata.Resource)))
			return metadata.Resource
		}

		// Metadata exists but lacks resource field - fallback to server URL
		logger.Info("Protected Resource Metadata lacks resource field, using server URL as fallback",
			zap.String("server", serverConfig.Name),
			zap.String("fallback_resource", logSafeURL(serverConfig.URL)))
		return serverConfig.URL
	}

	// No resource_metadata in WWW-Authenticate - fallback to server URL
	logger.Debug("WWW-Authenticate header lacks resource_metadata, using server URL as fallback",
		zap.String("server", serverConfig.Name),
		zap.String("fallback_resource", logSafeURL(serverConfig.URL)))
	return serverConfig.URL
}

// CreateOAuthConfig creates an OAuth configuration for dynamic client registration
// This implements proper callback server coordination required for Cloudflare OAuth
//
// Note: For zero-config OAuth with auto-detected resource parameter, use
// CreateOAuthConfigWithExtraParams() instead, which returns both config and extraParams.
func CreateOAuthConfig(serverConfig *config.ServerConfig, storage *storage.BoltDB) *client.OAuthConfig {
	cfg, _ := CreateOAuthConfigWithLogger(serverConfig, storage, nil)
	return cfg
}

// CreateOAuthConfigWithLogger is CreateOAuthConfig with the caller's logger and
// the reason it failed. See CreateOAuthConfigWithExtraParamsAndLogger.
func CreateOAuthConfigWithLogger(serverConfig *config.ServerConfig, storage *storage.BoltDB, logger *zap.Logger) (*client.OAuthConfig, error) {
	// Extract manual extra_params from config for backward compatibility
	var extraParams map[string]string
	if serverConfig.OAuth != nil && len(serverConfig.OAuth.ExtraParams) > 0 {
		extraParams = serverConfig.OAuth.ExtraParams
	}
	return createOAuthConfigInternal(serverConfig, storage, extraParams, logger)
}

// clearOAuthCredentials drops a DCR registration so the next login re-registers.
func clearOAuthCredentials(logger *zap.Logger, storage *storage.BoltDB, serverKey, serverName string) {
	if storage == nil {
		return
	}
	if err := storage.ClearOAuthClientCredentials(serverKey); err != nil {
		logger.Warn("Failed to clear DCR credentials",
			zap.String("server", serverName),
			zap.Error(err))
	}
}

// createOAuthConfigInternal is the internal implementation that accepts extraParams
// for transport wrapper injection. This enables both manual and auto-detected params
// to be injected into token exchange and refresh requests.
func createOAuthConfigInternal(serverConfig *config.ServerConfig, storage *storage.BoltDB, extraParams map[string]string, logger *zap.Logger) (*client.OAuthConfig, error) {
	startTime := time.Now()
	// Applied exactly once per record: namedOAuthLogger is idempotent, so the
	// wrappers above may have named it already without producing "oauth.oauth".
	logger = namedOAuthLogger(logger)

	// Hand the real logger to the callback-server manager, whose own default is
	// the no-op zap global. Without this the entire callback surface — the bind
	// attempt on a pinned port, a tear-down, a dropped waiter, the arrival of
	// the callback itself — is invisible in main.log and the per-server log.
	globalCallbackManager.SetLogger(logger)

	logger.Debug("🚀 Starting OAuth config creation",
		zap.String("server", serverConfig.Name),
		zap.String("url", logSafeURL(serverConfig.URL)))

	// Defer logging of total duration
	defer func() {
		logger.Debug("⏱️ OAuth config creation completed",
			zap.String("server", serverConfig.Name),
			zap.Duration("total_duration", time.Since(startTime)))
	}()

	logger.Debug("Creating OAuth config for dynamic registration",
		zap.String("server", serverConfig.Name))

	// Scope discovery waterfall (FR-003):
	// 1. Config-specified scopes (highest priority - manual override)
	// 2. RFC 9728 Protected Resource Metadata
	// 3. RFC 8414 Authorization Server Metadata
	// 4. Empty scopes (server specifies via WWW-Authenticate)
	var scopes []string

	// Priority 1: Check config-specified scopes first (manual override)
	if serverConfig.OAuth != nil && len(serverConfig.OAuth.Scopes) > 0 {
		scopes = serverConfig.OAuth.Scopes
		logger.Info("✅ Using config-specified OAuth scopes",
			zap.String("server", serverConfig.Name),
			zap.Strings("scopes", scopes))
	}

	// Priority 2: Try RFC 9728 Protected Resource Metadata discovery
	if len(scopes) == 0 {
		baseURL, err := parseBaseURL(serverConfig.URL)
		if err == nil && baseURL != "" {
			logger.Debug("Attempting Protected Resource Metadata scope discovery (RFC 9728)",
				zap.String("server", serverConfig.Name),
				zap.String("base_url", logSafeURL(baseURL)))

			// Make a preflight HEAD request to get WWW-Authenticate header
			resp, err := http.Head(serverConfig.URL)
			if err == nil && resp.StatusCode == 401 {
				wwwAuth := resp.Header.Get("WWW-Authenticate")
				if metadataURL := ExtractResourceMetadataURL(wwwAuth); metadataURL != "" {
					discoveredScopes, err := DiscoverScopesFromProtectedResource(metadataURL, 5*time.Second)
					if err == nil && len(discoveredScopes) > 0 {
						scopes = discoveredScopes
						logger.Info("✅ Auto-discovered OAuth scopes from Protected Resource Metadata (RFC 9728)",
							zap.String("server", serverConfig.Name),
							zap.String("metadata_url", logSafeURL(metadataURL)),
							zap.Strings("scopes", scopes))
					} else if err != nil {
						logger.Debug("Protected Resource Metadata discovery failed",
							zap.String("server", serverConfig.Name),
							zap.Error(err))
					} else {
						// err == nil but no scopes returned
						logger.Warn("Protected Resource Metadata returned no scopes - some clients wait for this before showing OAuth UI",
							zap.String("server", serverConfig.Name),
							zap.String("metadata_url", logSafeURL(metadataURL)))
					}
				} else {
					logger.Warn("WWW-Authenticate header missing resource_metadata; OAuth clients may refuse to launch browser until PRM exists",
						zap.String("server", serverConfig.Name),
						zap.Any("www_authenticate", resp.Header["Www-Authenticate"]))
				}
			} else if err != nil {
				logger.Warn("Preflight request for WWW-Authenticate header failed; OAuth clients may not see login button",
					zap.String("server", serverConfig.Name),
					zap.Error(err))
			} else {
				logger.Warn("Preflight request did not return 401; server did not advertise WWW-Authenticate metadata",
					zap.String("server", serverConfig.Name),
					zap.Int("status_code", resp.StatusCode))
			}
		}
	}

	// Priority 3: Fallback to RFC 8414 Authorization Server Metadata
	if len(scopes) == 0 {
		baseURL, err := parseBaseURL(serverConfig.URL)
		if err == nil && baseURL != "" {
			logger.Debug("Attempting Authorization Server Metadata scope discovery (RFC 8414)",
				zap.String("server", serverConfig.Name),
				zap.String("base_url", logSafeURL(baseURL)))

			discoveredScopes, err := DiscoverScopesFromAuthorizationServer(baseURL, 5*time.Second)
			if err == nil && len(discoveredScopes) > 0 {
				scopes = discoveredScopes
				logger.Info("✅ Auto-discovered OAuth scopes from Authorization Server Metadata (RFC 8414)",
					zap.String("server", serverConfig.Name),
					zap.Strings("scopes", scopes))
			} else if err == nil {
				logger.Warn("Authorization Server Metadata returned no scopes; some OAuth clients will wait until scopes_supported is published",
					zap.String("server", serverConfig.Name),
					zap.String("metadata_url", logSafeURL(baseURL+"/.well-known/oauth-authorization-server")))
			} else {
				logger.Warn("Authorization Server Metadata discovery failed",
					zap.String("server", serverConfig.Name),
					zap.String("metadata_url", logSafeURL(baseURL+"/.well-known/oauth-authorization-server")),
					zap.Error(err))
			}
		}
	}

	// Priority 4: Final fallback to empty scopes (valid OAuth 2.1)
	if len(scopes) == 0 {
		scopes = []string{}
		logger.Warn("No OAuth scopes discovered; falling back to empty list. Some providers (e.g., Google) require `openid`/`email` to be set manually.",
			zap.String("server", serverConfig.Name),
			zap.String("hint", "Set oauth.scopes in server config or ensure PRM/AS metadata advertises scopes_supported"))
	}

	// A static client_id in the server config is an operator-supplied credential.
	// It is never obtained from — nor refreshable by — Dynamic Client Registration,
	// so the DCR "clear and re-register" recovery paths below must never wipe it.
	hasStaticClientID := serverConfig.OAuth != nil && serverConfig.OAuth.ClientID != ""

	// An explicit `oauth.redirect_uri` pins the loopback callback port. Providers
	// such as GitHub OAuth Apps match the callback URL exactly and reject
	// wildcards, so an operator must be able to nail the port down (issue #975).
	var pinnedRedirectURI string
	var pinnedPort int
	bindHost := config.LoopbackIPv4Host
	if serverConfig.OAuth != nil && strings.TrimSpace(serverConfig.OAuth.RedirectURI) != "" {
		host, port, err := ParsePinnedRedirectURIBinding(serverConfig.OAuth.RedirectURI)
		if err != nil {
			// Fail loudly: silently falling back to a random port would leave the
			// operator believing they pinned the callback URL when they had not.
			logger.Error("❌ Invalid oauth.redirect_uri in server config - refusing to fall back to a random callback port",
				zap.String("server", serverConfig.Name),
				zap.String("redirect_uri", serverConfig.OAuth.RedirectURI),
				zap.Error(err),
				zap.String("expected_format", "http://127.0.0.1:<port>"+DefaultRedirectPath))
			// Returned, not just logged: the caller's "server may not support
			// OAuth" message never mentions redirect_uri, which made a typo here
			// a permanent, undiagnosable connect failure.
			return nil, fmt.Errorf("invalid oauth.redirect_uri for server %q: %w", serverConfig.Name, err)
		}
		pinnedRedirectURI = strings.TrimSpace(serverConfig.OAuth.RedirectURI)
		pinnedPort = port
		// Bind the family the operator pinned. Binding 127.0.0.1 for a pinned
		// `http://[::1]:PORT/...` produced an authorize URL whose callback hit a
		// closed port, and the login then hung to the callback timeout.
		bindHost = host
	}

	// Check for stored callback port from previous DCR (Spec 022: OAuth Redirect URI Port Persistence)
	//
	// The stored record is read whether or not a port is pinned. A pin only
	// short-circuits the PORT CHOICE; it must not short-circuit the Spec 022
	// credential hygiene below, because a DCR client_id is registered against
	// the redirect_uri it was created with. Skipping the read meant a stale
	// registration was reused forever under a new pin, producing a permanent
	// redirect_uri_mismatch that no retry could clear.
	var preferredPort int
	var serverKey string
	var storedClientID string
	var storedPort int
	if storage != nil {
		serverKey = GenerateServerKey(serverConfig.Name, serverConfig.URL)
		var storedErr error
		storedClientID, _, storedPort, storedErr = storage.GetOAuthClientCredentials(serverKey)
		if storedErr != nil {
			storedClientID, storedPort = "", 0
		}
	}

	switch {
	case pinnedPort > 0:
		preferredPort = pinnedPort
		logger.Info("📌 Using operator-pinned OAuth callback port from oauth.redirect_uri",
			zap.String("server", serverConfig.Name),
			zap.String("redirect_uri", pinnedRedirectURI),
			zap.String("bind_host", bindHost),
			zap.Int("preferred_port", preferredPort))
	case storedPort > 0:
		preferredPort = storedPort
		logger.Info("🔄 Found stored callback port from previous OAuth login",
			zap.String("server", serverConfig.Name),
			zap.Int("preferred_port", preferredPort))
	}

	// Spec 022 credential hygiene, independent of the pin. Only DCR-issued
	// credentials are ever cleared: a static client_id is operator-supplied,
	// cannot be re-registered, and clearing it would only destroy configuration.
	if storage != nil && storedClientID != "" && !hasStaticClientID {
		switch {
		case storedPort == 0:
			// Legacy DCR credentials exist but the port is unknown. Clear them to
			// force fresh registration with port tracking.
			logger.Warn("⚠️ Legacy DCR credentials found without stored port, clearing for re-registration",
				zap.String("server", serverConfig.Name),
				zap.String("client_id", storedClientID))
			clearOAuthCredentials(logger, storage, serverKey, serverConfig.Name)
		case pinnedPort > 0 && storedPort != pinnedPort:
			// The registration was created for a different callback port than the
			// one now pinned, so the provider will reject the redirect_uri this
			// client_id is paired with. This is the feature's most likely upgrade
			// path: log in unpinned (DCR registers port A), then add
			// oauth.redirect_uri with port B because the moving port was rejected.
			// Re-register rather than fail forever.
			logger.Warn("⚠️ Stored DCR credentials were registered for a different callback port than oauth.redirect_uri pins, clearing for re-registration",
				zap.String("server", serverConfig.Name),
				zap.String("client_id", storedClientID),
				zap.Int("stored_port", storedPort),
				zap.Int("pinned_port", pinnedPort))
			clearOAuthCredentials(logger, storage, serverKey, serverConfig.Name)
		}
	}

	// Start callback server first to get the exact port (as documented in successful approach)
	logger.Info("🔧 Starting OAuth callback server",
		zap.String("server", serverConfig.Name),
		zap.Int("preferred_port", preferredPort),
		zap.String("approach", "MCPProxy callback server coordination for exact URI matching"))

	// Start our own callback server to get exact port for Cloudflare OAuth
	callbackServer, err := globalCallbackManager.StartCallbackServerOnHost(serverConfig.Name, CallbackBinding{
		Host:   bindHost,
		Port:   preferredPort,
		Pinned: pinnedPort > 0,
		Logger: logger,
	})
	if err != nil {
		logger.Error("Failed to start OAuth callback server",
			zap.String("server", serverConfig.Name),
			zap.Error(err))
		return nil, fmt.Errorf("failed to start OAuth callback server for %q: %w", serverConfig.Name, err)
	}

	// Spec 022: Detect port conflict and clear DCR credentials if port changed
	// This forces fresh DCR with the new port
	if preferredPort > 0 && callbackServer.Port != preferredPort {
		switch {
		case pinnedPort > 0:
			// The operator pinned this port; any other port produces a
			// redirect_uri the provider will reject. Abort instead of pretending.
			logger.Error("❌ Pinned OAuth callback port is unavailable - cannot honor oauth.redirect_uri",
				zap.String("server", serverConfig.Name),
				zap.String("redirect_uri", pinnedRedirectURI),
				zap.Int("pinned_port", pinnedPort),
				zap.Int("bound_port", callbackServer.Port),
				zap.String("hint", "free the pinned port, or change oauth.redirect_uri (and the provider's callback URL) to a free one"))
			if stopErr := globalCallbackManager.StopCallbackServerWithLogger(serverConfig.Name, logger); stopErr != nil {
				logger.Warn("Failed to stop callback server bound to the wrong port",
					zap.String("server", serverConfig.Name),
					zap.Error(stopErr))
			}
			return nil, fmt.Errorf("pinned OAuth callback port %d for server %q is unavailable (bound %d instead); free it or change oauth.redirect_uri and the provider's callback URL",
				pinnedPort, serverConfig.Name, callbackServer.Port)
		case hasStaticClientID:
			// Static credentials are not re-registerable, so clearing them would
			// only destroy the operator's configuration for no benefit.
			logger.Warn("⚠️ Callback port changed for a statically configured OAuth client; keeping credentials",
				zap.String("server", serverConfig.Name),
				zap.Int("stored_port", preferredPort),
				zap.Int("new_port", callbackServer.Port),
				zap.String("hint", "pin the port with oauth.redirect_uri if the provider requires an exact callback URL"))
		default:
			logger.Warn("⚠️ Callback port changed, clearing DCR credentials for re-registration",
				zap.String("server", serverConfig.Name),
				zap.Int("stored_port", preferredPort),
				zap.Int("new_port", callbackServer.Port))
			clearOAuthCredentials(logger, storage, serverKey, serverConfig.Name)
		}
	}

	// The pinned URI is sent to the provider verbatim so the string matches the
	// one registered there (host spelling included: localhost vs 127.0.0.1).
	redirectURI := callbackServer.RedirectURI
	if pinnedRedirectURI != "" {
		redirectURI = pinnedRedirectURI
	}

	logger.Info("Using exact redirect URI from allocated callback server",
		zap.String("server", serverConfig.Name),
		zap.String("redirect_uri", redirectURI),
		zap.Int("port", callbackServer.Port))

	logger.Info("OAuth callback server started successfully",
		zap.String("server", serverConfig.Name),
		zap.String("redirect_uri", redirectURI),
		zap.Int("port", callbackServer.Port))

	// Try to find a working metadata URL by validating multiple URL patterns
	// Different servers use different URL formats:
	// - Smithery: Uses separate domains (server.smithery.ai/x for MCP, auth.smithery.ai/x for OAuth)
	//   OAuth metadata at: https://auth.smithery.ai/.well-known/oauth-authorization-server/googledrive
	// - Cloudflare: Same domain for MCP and OAuth
	//   OAuth metadata at: https://logs.mcp.cloudflare.com/.well-known/oauth-authorization-server
	var authServerMetadataURL string
	if serverConfig.URL != "" {
		// First, try to discover the auth server URL from Protected Resource Metadata
		// This is necessary for servers like Smithery that use separate domains
		authServerURL := DiscoverAuthServerURL(serverConfig.URL, 5*time.Second)
		urlToUse := serverConfig.URL
		if authServerURL != "" {
			urlToUse = authServerURL
			logger.Info("Using discovered auth server URL for metadata discovery",
				zap.String("server", serverConfig.Name),
				zap.String("mcp_url", logSafeURL(serverConfig.URL)),
				zap.String("auth_server_url", logSafeURL(authServerURL)))
		}

		// Now find the working metadata URL using the auth server URL (or server URL as fallback)
		workingURL, err := FindWorkingMetadataURL(urlToUse, 10*time.Second)
		if err != nil {
			logger.Warn("Could not find working OAuth metadata URL, will rely on auto-discovery",
				zap.String("server", serverConfig.Name),
				zap.String("url_tried", urlToUse),
				zap.Error(err))
		} else {
			authServerMetadataURL = workingURL
			logger.Info("Using validated OAuth metadata URL",
				zap.String("server", serverConfig.Name),
				zap.String("metadata_url", logSafeURL(authServerMetadataURL)))
		}
	} else {
		logger.Info("Skipping OAuth metadata URL - no server URL configured",
			zap.String("server", serverConfig.Name))
	}

	// Use persistent token store to persist tokens across daemon restarts if storage is available
	var tokenStore client.TokenStore
	if storage != nil {
		tokenStore = NewPersistentTokenStore(serverConfig.Name, serverConfig.URL, storage)
		logger.Info("🔧 Using persistent token store for OAuth tokens",
			zap.String("server", serverConfig.Name),
			zap.String("storage", "BBolt database"))

		// Check if token exists in persistent storage
		existingToken, err := tokenStore.GetToken(context.Background())
		if err != nil {
			logger.Info("🔍 No existing token found in persistent storage",
				zap.String("server", serverConfig.Name),
				zap.Error(err))
		} else {
			logger.Info("✅ Found existing token in persistent storage",
				zap.String("server", serverConfig.Name),
				zap.Time("expires_at", existingToken.ExpiresAt),
				zap.Bool("expired", time.Now().After(existingToken.ExpiresAt)))
		}
	} else {
		tokenStore = globalTokenStoreManager.GetOrCreateTokenStore(serverConfig.Name)
		logger.Info("🔧 Using in-memory token store for OAuth tokens (CLI mode)",
			zap.String("server", serverConfig.Name),
			zap.String("storage", "memory"))
	}

	// Create HTTP client with transport wrapper for all OAuth servers.
	// The wrapper injects extra params (if any) and normalizes non-standard
	// token response status codes (e.g., 201 Created from Supabase → 200 OK).
	if len(extraParams) > 0 {
		masked := maskExtraParams(extraParams)
		logger.Debug("OAuth extra parameters will be injected into token requests",
			zap.String("server", serverConfig.Name),
			zap.Any("extra_params", masked))
	}

	wrapper := NewOAuthTransportWrapper(http.DefaultTransport, extraParams, logger)
	httpClient := &http.Client{
		Transport: wrapper,
		Timeout:   30 * time.Second,
	}

	// Check if static OAuth credentials are provided in config
	// If not provided, will attempt DCR or fall back to public client OAuth with PKCE
	var clientID, clientSecret string
	var registrationMode string

	if serverConfig.OAuth != nil && serverConfig.OAuth.ClientID != "" {
		// Use static credentials from config
		clientID = serverConfig.OAuth.ClientID
		clientSecret = serverConfig.OAuth.ClientSecret
		registrationMode = "static credentials"
		logger.Info("✅ Using static OAuth credentials from config",
			zap.String("server", serverConfig.Name),
			zap.String("client_id", clientID))
	} else {
		// Try to load persisted DCR credentials for token refresh.
		// Re-read rather than reuse the earlier snapshot: the Spec 022 hygiene
		// above may have just cleared a registration that is no longer valid for
		// this callback port.
		if storage != nil {
			persistedClientID, persistedClientSecret, _, err := storage.GetOAuthClientCredentials(serverKey)
			if err == nil && persistedClientID != "" {
				clientID = persistedClientID
				clientSecret = persistedClientSecret
				registrationMode = "persisted DCR credentials"
				logger.Info("✅ Using persisted DCR credentials for token refresh",
					zap.String("server", serverConfig.Name),
					zap.String("client_id", clientID))
			} else {
				// No persisted credentials - will attempt DCR or use public client OAuth with PKCE
				clientID = ""
				clientSecret = ""
				registrationMode = "public client (PKCE)"
				logger.Info("🔓 No persisted DCR credentials found - will attempt DCR or use public client mode",
					zap.String("server", serverConfig.Name),
					zap.String("mode", "Public client OAuth with PKCE"))
			}
		} else {
			// No storage available (CLI mode) - will attempt DCR
			clientID = ""
			clientSecret = ""
			registrationMode = "public client (PKCE)"
			logger.Info("🔓 No storage available - will attempt DCR or use public client mode",
				zap.String("server", serverConfig.Name),
				zap.String("mode", "Public client OAuth with PKCE"))
		}
	}

	oauthConfig := &client.OAuthConfig{
		ClientID:              clientID,
		ClientSecret:          clientSecret,
		RedirectURI:           redirectURI, // Exact redirect URI: operator-pinned, or the allocated port
		Scopes:                scopes,
		TokenStore:            tokenStore,            // Shared token store for this server
		PKCEEnabled:           true,                  // Always enable PKCE for security
		AuthServerMetadataURL: authServerMetadataURL, // Explicit metadata URL for proper discovery
		HTTPClient:            httpClient,            // Custom HTTP client with transport wrapper (extra params + status normalization)
	}

	logger.Info("OAuth config created successfully",
		zap.String("server", serverConfig.Name),
		zap.Strings("scopes", scopes),
		zap.Bool("pkce_enabled", true),
		zap.String("redirect_uri", redirectURI),
		zap.String("auth_server_metadata_url", logSafeURL(authServerMetadataURL)),
		zap.String("registration_mode", registrationMode),
		zap.String("discovery_mode", "explicit metadata URL"), // Using explicit metadata URL to avoid discovery timeouts
		zap.String("token_store", "shared"))                   // Using shared token store for token persistence

	return oauthConfig, nil
}

// CallbackBinding describes the loopback endpoint a flow needs its OAuth
// callback server on.
type CallbackBinding struct {
	// Host is the loopback address to listen on ("127.0.0.1" or "::1").
	// Empty means 127.0.0.1.
	Host string
	// Port is the port to try first. 0 means dynamic allocation.
	Port int
	// Pinned reports whether Port came from an operator's `oauth.redirect_uri`
	// rather than from the Spec 022 stored-port record.
	//
	// It is carried explicitly instead of being inferred from Port > 0 because
	// the two cases need opposite handling when a cached server is already
	// bound elsewhere: a pin CANNOT be satisfied by another port (the provider
	// rejects the mismatched redirect_uri), whereas a stored port is only a
	// preference and must never cost a live flow its listener.
	Pinned bool
	// Logger is the caller's real logger. Without it the callback surface logs
	// into the no-op zap global and the whole flow is invisible.
	Logger *zap.Logger
}

func (b CallbackBinding) host() string {
	if b.Host == "" {
		return config.LoopbackIPv4Host
	}
	return b.Host
}

// StartCallbackServer starts a new OAuth callback server for the given server name.
// If preferredPort > 0, it attempts to bind to that port first for redirect URI persistence (Spec 022).
// Falls back to dynamic allocation if the preferred port is unavailable.
//
// Deprecated in favour of StartCallbackServerOnHost, which can bind IPv6
// loopback and carries the caller's logger. Kept for callers that have neither.
func (m *CallbackServerManager) StartCallbackServer(serverName string, preferredPort int) (*CallbackServer, error) {
	return m.StartCallbackServerOnHost(serverName, CallbackBinding{Port: preferredPort})
}

// StartCallbackServerOnHost starts (or reuses) the OAuth callback server for a
// server name on the requested loopback binding.
//
// Reuse rules, in order:
//
//   - A cached server that already satisfies the binding is reused. Reuse must
//     preserve parked waiters: callbacks are dispatched by `state` (issue #975),
//     so several flows can safely share one listener.
//   - A cached server bound elsewhere is REPLACED only when that cannot strand a
//     live flow — i.e. when nothing is parked on it — or when the caller pinned
//     the port, in which case reusing the wrong port would hand the provider a
//     redirect_uri it rejects and the flow could never complete anyway.
//   - Otherwise the cached server is reused despite the mismatch, and the
//     mismatch is logged. This is the case that matters for a static client_id
//     whose stored port is occupied by another process: attempt 1 falls back to
//     a dynamic port and parks a waiter, attempt 2 still prefers the stored
//     port, and tearing down attempt 1's listener would make that server
//     impossible to log into for as long as the stored port stays occupied.
func (m *CallbackServerManager) StartCallbackServerOnHost(serverName string, binding CallbackBinding) (*CallbackServer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	logger := m.adoptLoggerLocked(binding.Logger)
	bindHost := binding.host()
	preferredPort := binding.Port

	// Check if we already have a server for this name
	if existing, exists := m.servers[serverName]; exists {
		matchesBinding := existing.BindHost == bindHost &&
			(preferredPort == 0 || existing.Port == preferredPort)

		switch {
		case matchesBinding:
			// No draining here (issue #975): params are dispatched per `state`, so a
			// stale callback can no longer be mistaken for this attempt's, and a
			// waiter already parked on this server must survive the reuse.
			logger.Debug("Reusing existing callback server",
				zap.String("server", serverName),
				zap.String("bind_host", existing.BindHost),
				zap.Int("port", existing.Port))
			return existing, nil

		case existing.waiterCount() == 0 || binding.Pinned:
			logger.Warn("Replacing stale OAuth callback server that does not match the requested binding",
				zap.String("server", serverName),
				zap.String("cached_bind_host", existing.BindHost),
				zap.Int("cached_port", existing.Port),
				zap.String("requested_bind_host", bindHost),
				zap.Int("requested_port", preferredPort),
				zap.Bool("pinned", binding.Pinned),
				zap.Int("waiters_dropped", existing.waiterCount()))
			if err := m.stopCallbackServerLocked(serverName, logger); err != nil {
				logger.Warn("Failed to stop stale OAuth callback server",
					zap.String("server", serverName),
					zap.Error(err))
			}

		default:
			// A flow is parked on the cached listener. Dropping it would send a
			// user's browser to a closed port; the preference is not worth that.
			logger.Warn("Keeping the existing OAuth callback server: a flow is still waiting on it",
				zap.String("server", serverName),
				zap.String("cached_bind_host", existing.BindHost),
				zap.Int("cached_port", existing.Port),
				zap.Int("requested_port", preferredPort),
				zap.Int("waiters", existing.waiterCount()),
				zap.String("hint", "free the preferred port and retry once the pending login finishes or times out"))
			return existing, nil
		}
	}

	var listener net.Listener
	var err error

	// Try preferred port first if specified (Spec 022: OAuth Redirect URI Port Persistence)
	if preferredPort > 0 {
		listener, err = net.Listen("tcp", net.JoinHostPort(bindHost, strconv.Itoa(preferredPort)))
		if err != nil {
			logger.Warn("Preferred port unavailable, falling back to dynamic allocation",
				zap.String("server", serverName),
				zap.String("bind_host", bindHost),
				zap.Int("preferred_port", preferredPort),
				zap.Bool("pinned", binding.Pinned),
				zap.Error(err))
			// Fall through to dynamic allocation
		} else {
			logger.Info("✅ Using preferred port for OAuth callback (port persistence)",
				zap.String("server", serverName),
				zap.String("bind_host", bindHost),
				zap.Int("port", preferredPort))
		}
	}

	// Fall back to dynamic port allocation if no listener yet
	if listener == nil {
		listener, err = net.Listen("tcp", net.JoinHostPort(bindHost, "0"))
		if err != nil {
			return nil, fmt.Errorf("failed to allocate dynamic port on %s: %w", bindHost, err)
		}
	}

	// Extract the dynamically allocated port
	addr := listener.Addr().(*net.TCPAddr)
	port := addr.Port
	listenAddr := net.JoinHostPort(bindHost, strconv.Itoa(port))
	// JoinHostPort brackets an IPv6 literal, which is what the redirect URI
	// needs too ("http://[::1]:PORT/oauth/callback").
	redirectURI := fmt.Sprintf("http://%s%s", listenAddr, DefaultRedirectPath)

	// Create HTTP server with dedicated mux
	mux := http.NewServeMux()
	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second, // Security: prevent Slowloris attacks
		ReadTimeout:       30 * time.Second, // Extended timeout for OAuth discovery
		WriteTimeout:      30 * time.Second, // Extended timeout for OAuth responses
	}

	// Create callback server instance
	callbackServer := &CallbackServer{
		Port:        port,
		RedirectURI: redirectURI,
		Server:      server,
		ServerName:  serverName,
		BindHost:    bindHost,
		logger:      logger.With(zap.String("server", serverName), zap.String("bind_host", bindHost), zap.Int("port", port)),
		waiters:     make(map[string]chan map[string]string),
	}

	// Set up HTTP handler for OAuth callback
	mux.HandleFunc(DefaultRedirectPath, func(w http.ResponseWriter, r *http.Request) {
		callbackServer.handleCallback(w, r)
	})

	// Add a debug handler for the root path to see all requests
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		callbackServer.logger.Info("📥 HTTP request received on callback server",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("query", r.URL.RawQuery),
			zap.String("user_agent", r.UserAgent()),
			zap.String("remote_addr", r.RemoteAddr))

		if r.URL.Path == DefaultRedirectPath {
			callbackServer.handleCallback(w, r)
		} else {
			w.Header().Set("Content-Type", "text/html")
			debugPage := fmt.Sprintf(`
				<html>
					<body>
						<h1>OAuth Callback Server Debug</h1>
						<p>Path: %s</p>
						<p>Expected: %s</p>
						<p>Server: %s</p>
						<p>Port: %d</p>
					</body>
				</html>
			`, html.EscapeString(r.URL.Path), DefaultRedirectPath, html.EscapeString(serverName), port)
			if _, err := w.Write([]byte(debugPage)); err != nil {
				callbackServer.logger.Error("Error writing debug page", zap.Error(err))
			}
		}
	})

	// Start the server using the existing listener
	go func() {
		defer listener.Close()
		callbackServer.logger.Info("Starting OAuth callback server")

		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			callbackServer.logger.Error("OAuth callback server error", zap.Error(err))
		} else {
			callbackServer.logger.Info("OAuth callback server stopped")
		}
	}()

	// Store the server
	m.servers[serverName] = callbackServer

	callbackServer.logger.Info("OAuth callback server started successfully",
		zap.String("redirect_uri", redirectURI))

	return callbackServer, nil
}

// RegisterState registers the OAuth `state` a flow just minted and returns the
// channel its callback parameters will be delivered on. Call it BEFORE opening
// the browser so a fast user cannot beat the registration, and pair it with
// UnregisterState so a finished or cancelled flow's state cannot be reused.
//
// Registering the same state twice returns the same channel, which lets the
// flow that mints the state register early and the goroutine that waits for it
// obtain the same channel later without a second buffer.
func (c *CallbackServer) RegisterState(state string) <-chan map[string]string {
	c.waitersMu.Lock()
	defer c.waitersMu.Unlock()

	if ch, exists := c.waiters[state]; exists {
		return ch
	}

	ch := make(chan map[string]string, 1)
	c.waiters[state] = ch
	c.logger.Debug("Registered OAuth callback waiter", zap.String("state", state))
	return ch
}

// UnregisterState drops the waiter for a state. Safe to call more than once and
// safe to call after delivery (delivery already removes the waiter).
func (c *CallbackServer) UnregisterState(state string) {
	c.waitersMu.Lock()
	defer c.waitersMu.Unlock()

	if _, exists := c.waiters[state]; exists {
		delete(c.waiters, state)
		c.logger.Debug("Unregistered OAuth callback waiter", zap.String("state", state))
	}
}

// waiterCount reports how many flows are currently parked on this server.
// Used to decide whether a cached server may be torn down: replacing one that
// still has a waiter drops a live flow, whose browser then lands on a closed
// port (see StartCallbackServerOnHost).
func (c *CallbackServer) waiterCount() int {
	c.waitersMu.Lock()
	defer c.waitersMu.Unlock()
	return len(c.waiters)
}

// HasState reports whether a flow is currently waiting for this state.
func (c *CallbackServer) HasState(state string) bool {
	c.waitersMu.Lock()
	defer c.waitersMu.Unlock()
	_, exists := c.waiters[state]
	return exists
}

// deliver hands the callback params to the flow that registered this state.
// The waiter is single-use: it is removed on delivery so a replayed callback
// cannot be delivered twice. Returns false when no flow is waiting.
func (c *CallbackServer) deliver(state string, params map[string]string) bool {
	c.waitersMu.Lock()
	ch, exists := c.waiters[state]
	if exists {
		delete(c.waiters, state)
	}
	waiterCount := len(c.waiters)
	c.waitersMu.Unlock()

	if !exists {
		c.logger.Warn("OAuth callback carries a state no flow is waiting for",
			zap.String("state", state),
			zap.Int("other_waiters", waiterCount))
		return false
	}

	select {
	case ch <- params:
		return true
	default:
		// Cannot happen: the channel is buffered and single-use.
		c.logger.Error("OAuth callback waiter channel unexpectedly full",
			zap.String("state", state))
		return false
	}
}

// dropAllWaiters removes every registered waiter. Waiters unblock through their
// own context deadline; the channels are deliberately left unclosed so a parked
// flow never observes a zero-value callback as if it were a real one.
func (c *CallbackServer) dropAllWaiters() int {
	c.waitersMu.Lock()
	defer c.waitersMu.Unlock()

	dropped := len(c.waiters)
	c.waiters = make(map[string]chan map[string]string)
	return dropped
}

// callbackPage renders the page the user's browser lands on. Success is only
// ever reported when the authorization code was actually delivered to the flow
// that asked for it (issue #975).
func callbackPage(title, message string) string {
	closeScript := ""
	if title == "Authorization Successful" {
		closeScript = `
				<script>
					setTimeout(function() {
						window.close();
					}, 2000);
				</script>`
	}
	return fmt.Sprintf(`
		<html>
			<body>
				<h1>%s</h1>
				<p>%s</p>%s
			</body>
		</html>
	`, html.EscapeString(title), html.EscapeString(message), closeScript)
}

// handleCallback handles OAuth callback requests
func (c *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	c.logger.Info("🎯 OAuth callback received",
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("query", r.URL.RawQuery),
		zap.String("remote_addr", r.RemoteAddr),
		zap.String("user_agent", r.UserAgent()))

	// Extract query parameters
	params := make(map[string]string)
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}

	// Log specific OAuth parameters
	c.logger.Info("🔍 OAuth callback parameters extracted",
		zap.String("code", params["code"]),
		zap.String("state", params["state"]),
		zap.String("error", params["error"]),
		zap.String("error_description", params["error_description"]),
		zap.Int("total_params", len(params)))

	state := params["state"]

	// Route the params to the flow that minted this state. A callback nobody is
	// waiting for is NOT delivered to some other flow and NOT reported to the
	// user as a success (issue #975).
	if !c.deliver(state, params) {
		reason := fmt.Errorf("OAuth callback for server %q could not be delivered: state %q is unknown or expired "+
			"(the sign-in may have timed out, or it was started by a different mcpproxy session)", c.ServerName, state)
		if state == "" {
			reason = fmt.Errorf("OAuth callback for server %q carried no state parameter and was rejected", c.ServerName)
		}
		c.logger.Error("❌ OAuth callback dropped - no flow is waiting for this state",
			zap.String("state", state),
			zap.String("error", params["error"]))

		// Surface it to the operator instead of burying it in the log (issue #975).
		GetTokenStoreManager().RecordOAuthFailure(c.ServerName, reason)

		c.writePage(w, http.StatusBadRequest, callbackPage(
			"Authorization Failed",
			fmt.Sprintf("mcpproxy is not waiting for this sign-in (unknown or expired state). "+
				"Nothing was signed in. Start the login again from mcpproxy for '%s'.", c.ServerName)))
		return
	}

	c.logger.Info("✅ OAuth callback parameters delivered to the waiting flow",
		zap.String("state", state))

	// The provider itself reported a failure: the flow was told, but the user
	// must not see a success page.
	if providerErr := params["error"]; providerErr != "" {
		message := providerErr
		if desc := params["error_description"]; desc != "" {
			message = providerErr + ": " + desc
		}
		c.writePage(w, http.StatusBadRequest, callbackPage(
			"Authorization Failed",
			fmt.Sprintf("The authorization server rejected the request (%s). You can close this window and try again.", message)))
		return
	}

	c.writePage(w, http.StatusOK, callbackPage(
		"Authorization Successful",
		"You can now close this window and return to the application."))
}

// writePage writes an HTML response for the callback browser window.
func (c *CallbackServer) writePage(w http.ResponseWriter, status int, page string) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(status)
	if _, err := w.Write([]byte(page)); err != nil {
		c.logger.Error("Error writing OAuth callback response", zap.Error(err))
	}
}

// GetCallbackServer retrieves the callback server for a given server name
func (m *CallbackServerManager) GetCallbackServer(serverName string) (*CallbackServer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	server, exists := m.servers[serverName]
	return server, exists
}

// GetCallbackServer is a global helper to access callback servers
func GetCallbackServer(serverName string) (*CallbackServer, bool) {
	return globalCallbackManager.GetCallbackServer(serverName)
}

// StopCallbackServer stops and removes the callback server for a given server name
func (m *CallbackServerManager) StopCallbackServer(serverName string) error {
	return m.StopCallbackServerWithLogger(serverName, nil)
}

// StopCallbackServerWithLogger is StopCallbackServer with the caller's logger,
// so the tear-down (and any waiter it drops) is actually recorded.
func (m *CallbackServerManager) StopCallbackServerWithLogger(serverName string, logger *zap.Logger) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.stopCallbackServerLocked(serverName, m.adoptLoggerLocked(logger))
}

// stopCallbackServerLocked shuts the server down and removes it from the map.
// m.mu must be held.
func (m *CallbackServerManager) stopCallbackServerLocked(serverName string, logger *zap.Logger) error {
	server, exists := m.servers[serverName]
	if !exists {
		return nil // Already stopped or never started
	}

	// Shutdown the server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Server.Shutdown(ctx); err != nil {
		logger.Error("Error shutting down OAuth callback server",
			zap.String("server", serverName),
			zap.Error(err))
	}

	// Drop any registered waiters. They unblock on their own context deadline;
	// the channels are left unclosed so a parked flow never mistakes a
	// zero-value receive for a real callback.
	if dropped := server.dropAllWaiters(); dropped > 0 {
		logger.Warn("Stopped OAuth callback server while flows were still waiting",
			zap.String("server", serverName),
			zap.Int("waiters", dropped))
	}

	// Remove from map
	delete(m.servers, serverName)

	logger.Info("OAuth callback server stopped",
		zap.String("server", serverName),
		zap.String("bind_host", server.BindHost),
		zap.Int("port", server.Port))

	return nil
}

// GetGlobalCallbackManager returns the global callback manager instance
func GetGlobalCallbackManager() *CallbackServerManager {
	return globalCallbackManager
}

// ShouldUseOAuth determines if OAuth should be attempted for a given server
// Headers are tried first if configured, then OAuth as fallback on auth errors
func ShouldUseOAuth(serverConfig *config.ServerConfig) bool {
	logger := zap.L().Named("oauth")

	// Check if OAuth is disabled for tests
	if os.Getenv("MCPPROXY_DISABLE_OAUTH") == "true" {
		logger.Debug("OAuth disabled for tests", zap.String("server", serverConfig.Name))
		return false
	}

	// Only HTTP and SSE transports support OAuth
	if serverConfig.Protocol == "stdio" {
		logger.Debug("OAuth not supported for stdio protocol", zap.String("server", serverConfig.Name))
		return false
	}

	// If headers are configured, try headers first, not OAuth
	if len(serverConfig.Headers) > 0 {
		logger.Debug("Headers configured - will try headers first, OAuth as fallback if needed",
			zap.String("server", serverConfig.Name),
			zap.Int("header_count", len(serverConfig.Headers)))
		return false
	}

	// For HTTP/SSE servers without headers, try OAuth-enabled clients
	logger.Debug("No headers configured - OAuth-enabled client will be used",
		zap.String("server", serverConfig.Name),
		zap.String("protocol", serverConfig.Protocol))

	return true
}

// IsOAuthConfigured checks if server has OAuth configuration in config file
// This is mainly for informational purposes - we don't require pre-configuration
func IsOAuthConfigured(serverConfig *config.ServerConfig) bool {
	return serverConfig.OAuth != nil
}

// parseBaseURL extracts the base URL (scheme + host) from a full URL
func parseBaseURL(fullURL string) (string, error) {
	if fullURL == "" {
		return "", fmt.Errorf("empty URL")
	}

	// Handle URLs that might not have a scheme
	if !strings.HasPrefix(fullURL, "http://") && !strings.HasPrefix(fullURL, "https://") {
		fullURL = "https://" + fullURL
	}

	u, err := url.Parse(fullURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid URL: missing scheme or host")
	}

	return fmt.Sprintf("%s://%s", u.Scheme, u.Host), nil
}

// IsOAuthCapable determines if a server can use OAuth authentication
// Returns true if:
//  1. OAuth is explicitly configured in config, OR
//  2. Server uses HTTP-based protocol (OAuth auto-detection available)
func IsOAuthCapable(serverConfig *config.ServerConfig) bool {
	// Explicitly configured
	if serverConfig.OAuth != nil {
		return true
	}

	// Auto-detection available for HTTP-based protocols
	protocol := strings.ToLower(serverConfig.Protocol)
	switch protocol {
	case "http", "sse", "streamable-http", "auto":
		return true // OAuth can be auto-detected
	case "stdio":
		return false // OAuth not applicable for stdio
	default:
		// Unknown protocol - assume HTTP-based and try OAuth
		return true
	}
}
