// Package oauth provides OAuth authentication functionality for MCP servers.
package oauth

import "errors"

// OAuth-specific sentinel errors for consistent error handling across the codebase.
// Note: ErrFlowInProgress and ErrFlowTimeout are defined in coordinator.go
var (
	// ErrServerNotOAuth indicates server doesn't use OAuth authentication.
	// This is returned when attempting OAuth operations on a non-OAuth server.
	ErrServerNotOAuth = errors.New("server does not use OAuth")

	// ErrTokenExpired indicates OAuth token has expired.
	// This triggers token refresh or re-authentication flow.
	ErrTokenExpired = errors.New("OAuth token has expired")

	// ErrRefreshFailed indicates token refresh failed after all retry attempts.
	// This typically requires manual re-authentication via browser.
	ErrRefreshFailed = errors.New("OAuth token refresh failed")

	// ErrNoRefreshToken indicates refresh token is not available.
	// Some OAuth providers don't issue refresh tokens.
	ErrNoRefreshToken = errors.New("no refresh token available")
)
