package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseOAuthError_FastAPIValidation tests parsing FastAPI validation errors (Runlayer format)
func TestParseOAuthError_FastAPIValidation(t *testing.T) {
	responseBody := []byte(`{
		"detail": [
			{
				"type": "missing",
				"loc": ["query", "resource"],
				"msg": "Field required",
				"input": null
			}
		]
	}`)

	err := parseOAuthError(errors.New("validation failed"), responseBody)

	require.Error(t, err)
	var paramErr *OAuthParameterError
	require.True(t, errors.As(err, &paramErr), "Error should be OAuthParameterError type")
	assert.Equal(t, "resource", paramErr.Parameter)
	assert.Equal(t, "authorization_url", paramErr.Location)
	assert.Contains(t, err.Error(), "requires 'resource' parameter")
}

// TestParseOAuthError_FastAPIValidation_MultipleErrors tests handling multiple validation errors
func TestParseOAuthError_FastAPIValidation_MultipleErrors(t *testing.T) {
	responseBody := []byte(`{
		"detail": [
			{
				"type": "missing",
				"loc": ["query", "resource"],
				"msg": "Field required",
				"input": null
			},
			{
				"type": "missing",
				"loc": ["query", "scope"],
				"msg": "Field required",
				"input": null
			}
		]
	}`)

	err := parseOAuthError(errors.New("validation failed"), responseBody)

	require.Error(t, err)
	var paramErr *OAuthParameterError
	require.True(t, errors.As(err, &paramErr))
	// Should extract the first missing query parameter
	assert.Equal(t, "resource", paramErr.Parameter)
}

// TestParseOAuthError_RFC6749OAuth tests parsing RFC 6749 OAuth error responses
func TestParseOAuthError_RFC6749OAuth(t *testing.T) {
	responseBody := []byte(`{
		"error": "invalid_request",
		"error_description": "The request is missing a required parameter",
		"error_uri": "https://example.com/docs/errors#invalid_request"
	}`)

	err := parseOAuthError(errors.New("oauth failed"), responseBody)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "OAuth error: invalid_request")
	assert.Contains(t, err.Error(), "missing a required parameter")
}

// TestParseOAuthError_UnknownFormat tests fallback to original error for unknown formats
func TestParseOAuthError_UnknownFormat(t *testing.T) {
	responseBody := []byte(`{"some": "unknown", "format": true}`)
	originalErr := errors.New("original error message")

	err := parseOAuthError(originalErr, responseBody)

	require.Error(t, err)
	assert.Equal(t, originalErr, err, "Should return original error for unknown formats")
}

// TestParseOAuthError_InvalidJSON tests handling invalid JSON
func TestParseOAuthError_InvalidJSON(t *testing.T) {
	responseBody := []byte(`not valid json`)
	originalErr := errors.New("parse error")

	err := parseOAuthError(originalErr, responseBody)

	require.Error(t, err)
	assert.Equal(t, originalErr, err, "Should return original error for invalid JSON")
}

// TestParseOAuthError_EmptyBody tests handling empty response body
func TestParseOAuthError_EmptyBody(t *testing.T) {
	responseBody := []byte(``)
	originalErr := errors.New("empty response")

	err := parseOAuthError(originalErr, responseBody)

	require.Error(t, err)
	assert.Equal(t, originalErr, err, "Should return original error for empty body")
}

// TestOAuthParameterError_Unwrap tests error unwrapping
func TestOAuthParameterError_Unwrap(t *testing.T) {
	originalErr := errors.New("underlying error")
	paramErr := &OAuthParameterError{
		Parameter:   "resource",
		Location:    "authorization_url",
		Message:     "Field required",
		OriginalErr: originalErr,
	}

	unwrapped := errors.Unwrap(paramErr)
	assert.Equal(t, originalErr, unwrapped, "Should unwrap to original error")
}

// TestErrOAuthPending_Error tests ErrOAuthPending error message formatting
func TestErrOAuthPending_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *ErrOAuthPending
		expected string
	}{
		{
			name: "with custom message",
			err: &ErrOAuthPending{
				ServerName: "slack",
				ServerURL:  "https://oauth.example.com/mcp",
				Message:    "deferred for tray UI",
			},
			expected: "OAuth authentication required for slack: deferred for tray UI",
		},
		{
			name: "without custom message",
			err: &ErrOAuthPending{
				ServerName: "github",
				ServerURL:  "https://api.github.com/mcp",
			},
			expected: "OAuth authentication required for github - use 'mcpproxy auth login --server=github' or tray menu",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestIsOAuthPending tests the IsOAuthPending helper function
func TestIsOAuthPending(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name: "ErrOAuthPending returns true",
			err: &ErrOAuthPending{
				ServerName: "slack",
				ServerURL:  "https://oauth.example.com/mcp",
			},
			expected: true,
		},
		{
			name:     "regular error returns false",
			err:      errors.New("regular error"),
			expected: false,
		},
		{
			name:     "nil error returns false",
			err:      nil,
			expected: false,
		},
		{
			name: "wrapped ErrOAuthPending returns false",
			err: errors.New("wrapped: " + (&ErrOAuthPending{
				ServerName: "slack",
			}).Error()),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsOAuthPending(tt.err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestErrOAuthPending_AsError tests that ErrOAuthPending satisfies error interface
func TestErrOAuthPending_AsError(t *testing.T) {
	err := &ErrOAuthPending{
		ServerName: "slack",
		ServerURL:  "https://oauth.example.com/mcp",
		Message:    "test message",
	}

	// Should satisfy error interface
	var _ error = err

	// Should work with errors.As
	var target *ErrOAuthPending
	assert.True(t, errors.As(err, &target))
	assert.Equal(t, "slack", target.ServerName)
}
