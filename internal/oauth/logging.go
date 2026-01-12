// Package oauth provides OAuth 2.1 authentication support for MCP servers.
// This file implements enhanced logging utilities with sensitive data redaction.
package oauth

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Sensitive header names that should be redacted in logs.
var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"x-api-key":           true,
	"cookie":              true,
	"set-cookie":          true,
	"x-access-token":      true,
	"x-refresh-token":     true,
	"x-auth-token":        true,
	"proxy-authorization": true,
}

// Sensitive parameter names in request bodies or URLs.
var sensitiveParams = []string{
	"access_token",
	"refresh_token",
	"client_secret",
	"code",
	"password",
	"token",
	"id_token",
	"assertion",
}

// tokenPattern matches Bearer tokens and other sensitive token patterns.
var tokenPattern = regexp.MustCompile(`(?i)(bearer\s+)[a-zA-Z0-9\-_\.]+`)

// secretPattern matches common secret patterns.
var secretPattern = regexp.MustCompile(`(?i)(secret|password|token|key)["']?\s*[:=]\s*["']?[a-zA-Z0-9\-_\.]+`)

// RedactSensitiveData redacts sensitive information from a string.
// It replaces tokens, secrets, and other sensitive data with redacted placeholders.
func RedactSensitiveData(data string) string {
	if data == "" {
		return data
	}

	// Redact Bearer tokens
	result := tokenPattern.ReplaceAllString(data, "${1}***REDACTED***")

	// Redact secrets and passwords
	result = secretPattern.ReplaceAllStringFunc(result, func(match string) string {
		// Find the position of = or : and redact everything after
		for _, sep := range []string{"=", ":"} {
			if idx := strings.Index(match, sep); idx != -1 {
				return match[:idx+1] + "***REDACTED***"
			}
		}
		return "***REDACTED***"
	})

	// Redact sensitive URL parameters
	for _, param := range sensitiveParams {
		pattern := regexp.MustCompile(`(?i)(` + param + `=)[^&\s]+`)
		result = pattern.ReplaceAllString(result, "${1}***REDACTED***")
	}

	return result
}

// RedactHeaders creates a copy of headers with sensitive values redacted.
// Returns a map suitable for logging.
func RedactHeaders(headers http.Header) map[string]string {
	redacted := make(map[string]string)

	for key, values := range headers {
		lowerKey := strings.ToLower(key)
		if sensitiveHeaders[lowerKey] {
			redacted[key] = "***REDACTED***"
		} else {
			// Join multiple values and redact any sensitive data within
			value := strings.Join(values, ", ")
			redacted[key] = RedactSensitiveData(value)
		}
	}

	return redacted
}

// RedactURL redacts sensitive query parameters from a URL string.
func RedactURL(urlStr string) string {
	if urlStr == "" {
		return urlStr
	}

	result := urlStr
	for _, param := range sensitiveParams {
		pattern := regexp.MustCompile(`(?i)(` + param + `=)[^&]+`)
		result = pattern.ReplaceAllString(result, "${1}***REDACTED***")
	}

	return result
}

// LogOAuthRequest logs an OAuth HTTP request with redacted sensitive data.
// Use at debug level for comprehensive request tracing.
func LogOAuthRequest(logger *zap.Logger, method, url string, headers http.Header) {
	logger.Debug("OAuth HTTP request",
		zap.String("method", method),
		zap.String("url", RedactURL(url)),
		zap.Any("headers", RedactHeaders(headers)),
		zap.Time("timestamp", time.Now()),
	)
}

// LogOAuthResponse logs an OAuth HTTP response with redacted sensitive data.
// Use at debug level for comprehensive response tracing.
func LogOAuthResponse(logger *zap.Logger, statusCode int, headers http.Header, duration time.Duration) {
	logger.Debug("OAuth HTTP response",
		zap.Int("status_code", statusCode),
		zap.String("status", http.StatusText(statusCode)),
		zap.Any("headers", RedactHeaders(headers)),
		zap.Duration("duration", duration),
		zap.Time("timestamp", time.Now()),
	)
}

// LogOAuthResponseError logs an OAuth HTTP response error.
func LogOAuthResponseError(logger *zap.Logger, statusCode int, errorMsg string, duration time.Duration) {
	logger.Warn("OAuth HTTP response error",
		zap.Int("status_code", statusCode),
		zap.String("status", http.StatusText(statusCode)),
		zap.String("error", RedactSensitiveData(errorMsg)),
		zap.Duration("duration", duration),
	)
}

// TokenMetadata contains non-sensitive token information for logging.
type TokenMetadata struct {
	TokenType       string
	ExpiresAt       time.Time
	ExpiresIn       time.Duration
	Scope           string
	HasRefreshToken bool
}

// LogTokenMetadata logs token metadata without exposing actual token values.
// Safe to use at info level as no sensitive data is included.
func LogTokenMetadata(logger *zap.Logger, metadata TokenMetadata) {
	logger.Info("OAuth token metadata",
		zap.String("token_type", metadata.TokenType),
		zap.Time("expires_at", metadata.ExpiresAt),
		zap.Duration("expires_in", metadata.ExpiresIn),
		zap.String("scope", metadata.Scope),
		zap.Bool("has_refresh_token", metadata.HasRefreshToken),
	)
}

// LogClientConnectionAttempt logs a client connection attempt (not an actual token refresh).
// Note: This is called when retrying client.Start(), which may trigger automatic
// token refresh internally by mcp-go, but we cannot observe whether refresh actually occurred.
func LogClientConnectionAttempt(logger *zap.Logger, attempt int, maxAttempts int) {
	logger.Info("OAuth client connection attempt",
		zap.Int("attempt", attempt),
		zap.Int("max_attempts", maxAttempts),
	)
}

// LogClientConnectionSuccess logs a successful client connection.
// Note: This does NOT mean a token refresh occurred - it means the client connected.
// The mcp-go library may have used a cached token or performed automatic refresh internally.
func LogClientConnectionSuccess(logger *zap.Logger, duration time.Duration) {
	logger.Info("OAuth client connection successful",
		zap.Duration("duration", duration),
	)
}

// LogClientConnectionFailure logs a failed client connection attempt.
func LogClientConnectionFailure(logger *zap.Logger, attempt int, err error) {
	logger.Warn("OAuth client connection failed",
		zap.Int("attempt", attempt),
		zap.Error(err),
	)
}

// Deprecated: Use LogClientConnectionAttempt instead.
// LogTokenRefreshAttempt is kept for backward compatibility but is misleading.
func LogTokenRefreshAttempt(logger *zap.Logger, attempt int, maxAttempts int) {
	LogClientConnectionAttempt(logger, attempt, maxAttempts)
}

// Deprecated: Use LogClientConnectionSuccess instead.
// LogTokenRefreshSuccess is kept for backward compatibility but is misleading.
// This is called when client.Start() succeeds, not when a token refresh occurs.
func LogTokenRefreshSuccess(logger *zap.Logger, duration time.Duration) {
	LogClientConnectionSuccess(logger, duration)
}

// Deprecated: Use LogClientConnectionFailure instead.
// LogTokenRefreshFailure is kept for backward compatibility but is misleading.
func LogTokenRefreshFailure(logger *zap.Logger, attempt int, err error) {
	LogClientConnectionFailure(logger, attempt, err)
}

// LogActualTokenRefreshAttempt logs an actual proactive token refresh attempt.
// This is called by RefreshManager when it initiates a token refresh operation.
func LogActualTokenRefreshAttempt(logger *zap.Logger, serverName string, tokenAge time.Duration) {
	logger.Info("OAuth token refresh attempt",
		zap.String("server", serverName),
		zap.Duration("token_age", tokenAge),
	)
}

// LogActualTokenRefreshResult logs the result of an actual token refresh operation.
// This is called by RefreshManager after a refresh attempt completes.
func LogActualTokenRefreshResult(logger *zap.Logger, serverName string, success bool, duration time.Duration, err error) {
	if success {
		logger.Info("OAuth token refresh succeeded",
			zap.String("server", serverName),
			zap.Duration("duration", duration),
		)
	} else {
		logger.Warn("OAuth token refresh failed",
			zap.String("server", serverName),
			zap.Duration("duration", duration),
			zap.Error(err),
		)
	}
}

// LogOAuthFlowStart logs the start of an OAuth flow.
func LogOAuthFlowStart(logger *zap.Logger, serverName string, correlationID string) {
	logger.Info("Starting OAuth flow",
		zap.String("server", serverName),
		zap.String("correlation_id", correlationID),
		zap.Time("start_time", time.Now()),
	)
}

// LogOAuthFlowEnd logs the end of an OAuth flow.
func LogOAuthFlowEnd(logger *zap.Logger, serverName string, correlationID string, success bool, duration time.Duration) {
	if success {
		logger.Info("OAuth flow completed successfully",
			zap.String("server", serverName),
			zap.String("correlation_id", correlationID),
			zap.Duration("total_duration", duration),
		)
	} else {
		logger.Warn("OAuth flow failed",
			zap.String("server", serverName),
			zap.String("correlation_id", correlationID),
			zap.Duration("total_duration", duration),
		)
	}
}
