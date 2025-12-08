package oauth

import (
	"testing"
	"time"

	"mcpproxy-go/internal/storage"

	"github.com/stretchr/testify/assert"
)

func TestOAuthStatus_String(t *testing.T) {
	tests := []struct {
		status   OAuthStatus
		expected string
	}{
		{OAuthStatusNone, "none"},
		{OAuthStatusAuthenticated, "authenticated"},
		{OAuthStatusExpired, "expired"},
		{OAuthStatusError, "error"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.String())
		})
	}
}

func TestOAuthStatus_IsValid(t *testing.T) {
	tests := []struct {
		status   OAuthStatus
		expected bool
	}{
		{OAuthStatusNone, true},
		{OAuthStatusAuthenticated, true},
		{OAuthStatusExpired, true},
		{OAuthStatusError, true},
		{OAuthStatus("invalid"), false},
		{OAuthStatus(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.IsValid())
		})
	}
}

func TestCalculateOAuthStatus(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		token     *storage.OAuthTokenRecord
		lastError string
		expected  OAuthStatus
	}{
		{
			name:      "nil token returns none",
			token:     nil,
			lastError: "",
			expected:  OAuthStatusNone,
		},
		{
			name: "valid token returns authenticated",
			token: &storage.OAuthTokenRecord{
				ExpiresAt: now.Add(1 * time.Hour),
			},
			lastError: "",
			expected:  OAuthStatusAuthenticated,
		},
		{
			name: "expired token returns expired",
			token: &storage.OAuthTokenRecord{
				ExpiresAt: now.Add(-1 * time.Hour),
			},
			lastError: "",
			expected:  OAuthStatusExpired,
		},
		{
			name: "oauth error in lastError returns error",
			token: &storage.OAuthTokenRecord{
				ExpiresAt: now.Add(1 * time.Hour),
			},
			lastError: "OAuth authentication failed",
			expected:  OAuthStatusError,
		},
		{
			name: "unauthorized error returns error",
			token: &storage.OAuthTokenRecord{
				ExpiresAt: now.Add(1 * time.Hour),
			},
			lastError: "401 Unauthorized",
			expected:  OAuthStatusError,
		},
		{
			name: "token error returns error",
			token: &storage.OAuthTokenRecord{
				ExpiresAt: now.Add(1 * time.Hour),
			},
			lastError: "invalid token response",
			expected:  OAuthStatusError,
		},
		{
			name: "non-oauth error with valid token returns authenticated",
			token: &storage.OAuthTokenRecord{
				ExpiresAt: now.Add(1 * time.Hour),
			},
			lastError: "network timeout",
			expected:  OAuthStatusAuthenticated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateOAuthStatus(tt.token, tt.lastError)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestContainsOAuthError(t *testing.T) {
	tests := []struct {
		err      string
		expected bool
	}{
		{"OAuth authentication failed", true},
		{"oauth error", true},
		{"401 Unauthorized", true},
		{"unauthorized access", true},
		{"AUTHENTICATION required", true},
		{"invalid token", true},
		{"Token expired", true},
		{"authorization denied", true},
		{"access denied by server", true},
		{"network timeout", false},
		{"connection refused", false},
		{"server error", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.err, func(t *testing.T) {
			result := containsOAuthError(tt.err)
			assert.Equal(t, tt.expected, result, "containsOAuthError(%q) = %v, want %v", tt.err, result, tt.expected)
		})
	}
}
