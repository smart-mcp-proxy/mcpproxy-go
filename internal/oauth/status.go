// Package oauth provides OAuth authentication functionality for MCP servers.
package oauth

import (
	"strings"
	"time"

	"mcpproxy-go/internal/storage"
)

// OAuthStatus represents the current authentication state of an OAuth server.
type OAuthStatus string

const (
	// OAuthStatusNone indicates the server does not use OAuth.
	OAuthStatusNone OAuthStatus = "none"

	// OAuthStatusAuthenticated indicates valid OAuth token is available.
	OAuthStatusAuthenticated OAuthStatus = "authenticated"

	// OAuthStatusExpired indicates OAuth token has expired.
	OAuthStatusExpired OAuthStatus = "expired"

	// OAuthStatusError indicates OAuth authentication error.
	OAuthStatusError OAuthStatus = "error"
)

// String returns the string representation of the OAuthStatus.
func (s OAuthStatus) String() string {
	return string(s)
}

// IsValid returns true if the status represents a valid OAuth state.
func (s OAuthStatus) IsValid() bool {
	switch s {
	case OAuthStatusNone, OAuthStatusAuthenticated, OAuthStatusExpired, OAuthStatusError:
		return true
	default:
		return false
	}
}

// CalculateOAuthStatus determines the OAuth status for a server based on token state.
// Returns OAuthStatusNone if token is nil (server doesn't use OAuth).
// Returns OAuthStatusError if lastError contains OAuth-related errors.
// Returns OAuthStatusExpired if token has expired.
// Returns OAuthStatusAuthenticated if token is valid.
func CalculateOAuthStatus(token *storage.OAuthTokenRecord, lastError string) OAuthStatus {
	if token == nil {
		return OAuthStatusNone
	}
	if lastError != "" && containsOAuthError(lastError) {
		return OAuthStatusError
	}
	if time.Now().After(token.ExpiresAt) {
		return OAuthStatusExpired
	}
	return OAuthStatusAuthenticated
}

// containsOAuthError checks if an error message indicates an OAuth-related problem.
func containsOAuthError(err string) bool {
	lowerErr := strings.ToLower(err)
	oauthIndicators := []string{
		"oauth",
		"authentication",
		"unauthorized",
		"401",
		"token",
		"authorization",
		"access denied",
	}
	for _, indicator := range oauthIndicators {
		if strings.Contains(lowerErr, indicator) {
			return true
		}
	}
	return false
}
