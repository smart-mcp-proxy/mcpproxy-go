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

// LogTokenRefreshAttempt logs a token refresh attempt.
func LogTokenRefreshAttempt(logger *zap.Logger, attempt int, maxAttempts int) {
	logger.Info("Attempting OAuth token refresh",
		zap.Int("attempt", attempt),
		zap.Int("max_attempts", maxAttempts),
	)
}

// LogTokenRefreshSuccess logs a successful token refresh.
func LogTokenRefreshSuccess(logger *zap.Logger, duration time.Duration) {
	logger.Info("OAuth token refresh successful",
		zap.Duration("duration", duration),
	)
}

// LogTokenRefreshFailure logs a failed token refresh attempt.
func LogTokenRefreshFailure(logger *zap.Logger, attempt int, err error) {
	logger.Warn("OAuth token refresh failed",
		zap.Int("attempt", attempt),
		zap.Error(err),
	)
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
