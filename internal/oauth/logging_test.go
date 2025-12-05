package oauth

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

func TestRedactSensitiveData(t *testing.T) {
	t.Run("redacts bearer tokens", func(t *testing.T) {
		input := "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"
		result := RedactSensitiveData(input)
		assert.Contains(t, result, "Bearer ***REDACTED***")
		assert.NotContains(t, result, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9")
	})

	t.Run("redacts secrets in key=value format", func(t *testing.T) {
		input := "client_secret=my-super-secret-value"
		result := RedactSensitiveData(input)
		assert.Contains(t, result, "***REDACTED***")
		assert.NotContains(t, result, "my-super-secret-value")
	})

	t.Run("redacts access_token parameter", func(t *testing.T) {
		input := "https://example.com/callback?access_token=abc123def456&state=xyz"
		result := RedactSensitiveData(input)
		assert.Contains(t, result, "access_token=***REDACTED***")
		assert.NotContains(t, result, "abc123def456")
		assert.Contains(t, result, "state=xyz")
	})

	t.Run("redacts refresh_token parameter", func(t *testing.T) {
		input := "refresh_token=my-refresh-token"
		result := RedactSensitiveData(input)
		assert.Contains(t, result, "refresh_token=***REDACTED***")
		assert.NotContains(t, result, "my-refresh-token")
	})

	t.Run("handles empty string", func(t *testing.T) {
		result := RedactSensitiveData("")
		assert.Empty(t, result)
	})

	t.Run("preserves non-sensitive data", func(t *testing.T) {
		input := "Content-Type: application/json"
		result := RedactSensitiveData(input)
		assert.Equal(t, input, result)
	})
}

func TestRedactHeaders(t *testing.T) {
	t.Run("redacts authorization header", func(t *testing.T) {
		headers := http.Header{
			"Authorization": []string{"Bearer secret-token"},
		}
		result := RedactHeaders(headers)
		assert.Equal(t, "***REDACTED***", result["Authorization"])
	})

	t.Run("redacts x-api-key header", func(t *testing.T) {
		headers := http.Header{
			"X-Api-Key": []string{"my-api-key"},
		}
		result := RedactHeaders(headers)
		assert.Equal(t, "***REDACTED***", result["X-Api-Key"])
	})

	t.Run("redacts cookie header", func(t *testing.T) {
		headers := http.Header{
			"Cookie": []string{"session=abc123"},
		}
		result := RedactHeaders(headers)
		assert.Equal(t, "***REDACTED***", result["Cookie"])
	})

	t.Run("preserves non-sensitive headers", func(t *testing.T) {
		headers := http.Header{
			"Content-Type": []string{"application/json"},
			"Accept":       []string{"*/*"},
		}
		result := RedactHeaders(headers)
		assert.Equal(t, "application/json", result["Content-Type"])
		assert.Equal(t, "*/*", result["Accept"])
	})

	t.Run("handles multiple values", func(t *testing.T) {
		headers := http.Header{
			"Accept": []string{"text/html", "application/json"},
		}
		result := RedactHeaders(headers)
		assert.Contains(t, result["Accept"], "text/html")
		assert.Contains(t, result["Accept"], "application/json")
	})

	t.Run("case insensitive header matching", func(t *testing.T) {
		headers := http.Header{
			"AUTHORIZATION": []string{"Bearer token"},
		}
		result := RedactHeaders(headers)
		assert.Equal(t, "***REDACTED***", result["AUTHORIZATION"])
	})
}

func TestRedactURL(t *testing.T) {
	t.Run("redacts access_token in URL", func(t *testing.T) {
		url := "https://example.com/callback?access_token=secret123&state=xyz"
		result := RedactURL(url)
		assert.Contains(t, result, "access_token=***REDACTED***")
		assert.Contains(t, result, "state=xyz")
		assert.NotContains(t, result, "secret123")
	})

	t.Run("redacts code in URL", func(t *testing.T) {
		url := "https://example.com/callback?code=auth_code_123&state=xyz"
		result := RedactURL(url)
		assert.Contains(t, result, "code=***REDACTED***")
		assert.NotContains(t, result, "auth_code_123")
	})

	t.Run("handles empty URL", func(t *testing.T) {
		result := RedactURL("")
		assert.Empty(t, result)
	})

	t.Run("preserves URL without sensitive params", func(t *testing.T) {
		url := "https://example.com/api/users?limit=10&offset=0"
		result := RedactURL(url)
		assert.Equal(t, url, result)
	})
}

func TestLogOAuthRequest(t *testing.T) {
	logger := zaptest.NewLogger(t)
	headers := http.Header{
		"Authorization": []string{"Bearer secret"},
		"Content-Type":  []string{"application/json"},
	}

	// Should not panic
	LogOAuthRequest(logger, "POST", "https://example.com/token", headers)
}

func TestLogOAuthResponse(t *testing.T) {
	logger := zaptest.NewLogger(t)
	headers := http.Header{
		"Content-Type": []string{"application/json"},
		"Set-Cookie":   []string{"session=abc123"},
	}

	// Should not panic
	LogOAuthResponse(logger, 200, headers, 100*time.Millisecond)
}

func TestLogOAuthResponseError(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Should not panic
	LogOAuthResponseError(logger, 401, "invalid_token: Token has expired", 50*time.Millisecond)
}

func TestLogTokenMetadata(t *testing.T) {
	logger := zaptest.NewLogger(t)

	metadata := TokenMetadata{
		TokenType:       "Bearer",
		ExpiresAt:       time.Now().Add(time.Hour),
		ExpiresIn:       time.Hour,
		Scope:           "read write",
		HasRefreshToken: true,
	}

	// Should not panic
	LogTokenMetadata(logger, metadata)
}

func TestLogTokenRefreshAttempt(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Should not panic
	LogTokenRefreshAttempt(logger, 1, 3)
}

func TestLogTokenRefreshSuccess(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Should not panic
	LogTokenRefreshSuccess(logger, 500*time.Millisecond)
}

func TestLogTokenRefreshFailure(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Should not panic
	LogTokenRefreshFailure(logger, 2, assert.AnError)
}

func TestLogOAuthFlowStart(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Should not panic
	LogOAuthFlowStart(logger, "test-server", "correlation-123")
}

func TestLogOAuthFlowEnd(t *testing.T) {
	logger := zaptest.NewLogger(t)

	t.Run("logs success", func(t *testing.T) {
		LogOAuthFlowEnd(logger, "test-server", "correlation-123", true, time.Second)
	})

	t.Run("logs failure", func(t *testing.T) {
		LogOAuthFlowEnd(logger, "test-server", "correlation-456", false, time.Second)
	})
}

func TestSensitiveHeadersList(t *testing.T) {
	// Verify all expected headers are in the sensitive list
	expectedSensitive := []string{
		"authorization",
		"x-api-key",
		"cookie",
		"set-cookie",
		"x-access-token",
		"x-refresh-token",
		"x-auth-token",
		"proxy-authorization",
	}

	for _, header := range expectedSensitive {
		assert.True(t, sensitiveHeaders[header], "Header %q should be in sensitive list", header)
	}
}

func TestSensitiveParamsList(t *testing.T) {
	// Verify all expected params are in the sensitive list
	expectedParams := []string{
		"access_token",
		"refresh_token",
		"client_secret",
		"code",
		"password",
		"token",
		"id_token",
		"assertion",
	}

	for _, param := range expectedParams {
		found := false
		for _, p := range sensitiveParams {
			if p == param {
				found = true
				break
			}
		}
		assert.True(t, found, "Param %q should be in sensitive list", param)
	}
}
