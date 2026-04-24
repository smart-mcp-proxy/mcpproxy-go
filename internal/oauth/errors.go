// Package oauth provides OAuth authentication functionality for MCP servers.
package oauth

import (
	"errors"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
)

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

// WrapRefreshExpired attaches the MCPX_OAUTH_REFRESH_EXPIRED diagnostics code
// to an error reported from the refresh-token path. Producers should call
// this at the point the refresh token is determined to be gone/expired so
// the diagnostics classifier returns the right code without resorting to
// free-text matching. Spec 044.
func WrapRefreshExpired(err error) error {
	return diagnostics.WrapError(diagnostics.OAuthRefreshExpired, err)
}

// WrapRefresh403 attaches MCPX_OAUTH_REFRESH_403 — use for invalid_grant
// or HTTP 403 responses from the token endpoint.
func WrapRefresh403(err error) error {
	return diagnostics.WrapError(diagnostics.OAuthRefresh403, err)
}

// WrapDiscoveryFailed attaches MCPX_OAUTH_DISCOVERY_FAILED for failures to
// fetch the OAuth authorization-server metadata document.
func WrapDiscoveryFailed(err error) error {
	return diagnostics.WrapError(diagnostics.OAuthDiscoveryFailed, err)
}
