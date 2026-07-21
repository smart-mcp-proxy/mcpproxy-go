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

func TestRedactStringHeaders(t *testing.T) {
	// The mask format mirrors the Web UI / macOS tray client-side
	// display: `••••<last2> (<N> chars)`. Keeping length + the last
	// two characters helps operators identify which token is in use
	// without revealing the secret, and gives all callers a single
	// uniform representation — no `***REDACTED***` sentinel branching.
	t.Run("redacts a bearer authorization to the masked-display format", func(t *testing.T) {
		headers := map[string]string{
			"Authorization":  "Bearer fake-test-token-not-a-real-secret",
			"X-MCP-Toolsets": "pull_requests",
		}
		got := RedactStringHeaders(headers)
		assert.Equal(t, "••••et (40 chars)", got["Authorization"],
			"sensitive value should be masked with length + last-2-char suffix")
		assert.NotContains(t, got["Authorization"], "fake-test-token",
			"plaintext must not survive the mask")
		assert.Equal(t, "pull_requests", got["X-MCP-Toolsets"])
	})

	t.Run("redacts x-api-key", func(t *testing.T) {
		got := RedactStringHeaders(map[string]string{"X-Api-Key": "supersecretkey"})
		assert.Equal(t, "••••ey (14 chars)", got["X-Api-Key"])
	})

	t.Run("redacts cookie and set-cookie", func(t *testing.T) {
		got := RedactStringHeaders(map[string]string{
			"Cookie":     "session=abc",
			"Set-Cookie": "session=abc; Path=/",
		})
		assert.Equal(t, "••••bc (11 chars)", got["Cookie"])
		assert.Equal(t, "••••=/ (19 chars)", got["Set-Cookie"])
	})

	t.Run("short values (<=4 chars) emit 4 bullets with no suffix", func(t *testing.T) {
		got := RedactStringHeaders(map[string]string{"Authorization": "ab"})
		assert.Equal(t, "••••", got["Authorization"],
			"don't leak the whole secret as the suffix for very short values")
	})

	t.Run("empty value renders as (empty)", func(t *testing.T) {
		got := RedactStringHeaders(map[string]string{"Authorization": ""})
		assert.Equal(t, "(empty)", got["Authorization"])
	})

	t.Run("keyring reference passes through unchanged", func(t *testing.T) {
		// References aren't secrets — they're labels pointing at the
		// OS keyring entry. Masking them would defeat the UI's
		// "stored in keyring" chip rendering, which depends on
		// detecting the literal `${keyring:NAME}` form.
		got := RedactStringHeaders(map[string]string{"Authorization": "${keyring:synapbus-auth}"})
		assert.Equal(t, "${keyring:synapbus-auth}", got["Authorization"])
	})

	t.Run("env reference passes through unchanged", func(t *testing.T) {
		got := RedactStringHeaders(map[string]string{"Authorization": "${env:GITHUB_TOKEN}"})
		assert.Equal(t, "${env:GITHUB_TOKEN}", got["Authorization"])
	})

	t.Run("preserves non-sensitive headers", func(t *testing.T) {
		got := RedactStringHeaders(map[string]string{"Content-Type": "application/json"})
		assert.Equal(t, "application/json", got["Content-Type"])
	})

	t.Run("case insensitive header matching", func(t *testing.T) {
		got := RedactStringHeaders(map[string]string{"AUTHORIZATION": "Bearer xyz"})
		assert.Equal(t, "••••yz (10 chars)", got["AUTHORIZATION"])
	})

	t.Run("nil input returns nil", func(t *testing.T) {
		assert.Nil(t, RedactStringHeaders(nil))
	})

	t.Run("empty input returns empty map", func(t *testing.T) {
		got := RedactStringHeaders(map[string]string{})
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})

	t.Run("does not mutate input", func(t *testing.T) {
		input := map[string]string{"Authorization": "Bearer secret"}
		_ = RedactStringHeaders(input)
		assert.Equal(t, "Bearer secret", input["Authorization"], "input should be unchanged")
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

func TestRedactEnvValues(t *testing.T) {
	t.Run("masks sensitive env keys with the display format", func(t *testing.T) {
		got := RedactEnvValues(map[string]string{
			"GITHUB_TOKEN":  "ghp_fake_token_value_1234",
			"API_KEY":       "supersecretkey",
			"DB_PASSWORD":   "hunter2hunter2",
			"AWS_SECRET":    "aws-secret-value",
			"AUTH_BEARER":   "bearer-material-x",
			"SIGNING_CERT":  "cert-body-material",
			"PRIVATE_STUFF": "private-material",
		})
		assert.Equal(t, "••••34 (25 chars)", got["GITHUB_TOKEN"])
		assert.Equal(t, "••••ey (14 chars)", got["API_KEY"])
		assert.Equal(t, "••••r2 (14 chars)", got["DB_PASSWORD"])
		assert.NotContains(t, got["AWS_SECRET"], "aws-secret-value")
		assert.NotContains(t, got["AUTH_BEARER"], "bearer-material")
		assert.NotContains(t, got["SIGNING_CERT"], "cert-body")
		assert.NotContains(t, got["PRIVATE_STUFF"], "private-material")
	})

	t.Run("leaves non-sensitive env values readable", func(t *testing.T) {
		got := RedactEnvValues(map[string]string{
			"LOG_LEVEL":    "debug",
			"HOME":         "/home/user",
			"EXISTING_VAR": "existing_value",
			"NODE_ENV":     "production",
			"HTTP_PROXY":   "http://proxy:8080",
			"MAX_RETRIES":  "5",
		})
		assert.Equal(t, "debug", got["LOG_LEVEL"])
		assert.Equal(t, "/home/user", got["HOME"])
		assert.Equal(t, "existing_value", got["EXISTING_VAR"])
		assert.Equal(t, "production", got["NODE_ENV"])
		assert.Equal(t, "http://proxy:8080", got["HTTP_PROXY"])
		assert.Equal(t, "5", got["MAX_RETRIES"])
	})

	t.Run("config references on sensitive keys pass through unchanged", func(t *testing.T) {
		got := RedactEnvValues(map[string]string{
			"GITHUB_TOKEN": "${env:REAL_GITHUB_TOKEN}",
			"API_KEY":      "${keyring:my-api-key}",
		})
		assert.Equal(t, "${env:REAL_GITHUB_TOKEN}", got["GITHUB_TOKEN"])
		assert.Equal(t, "${keyring:my-api-key}", got["API_KEY"])
	})

	t.Run("nil input returns nil", func(t *testing.T) {
		assert.Nil(t, RedactEnvValues(nil))
	})

	t.Run("empty input returns empty map", func(t *testing.T) {
		got := RedactEnvValues(map[string]string{})
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})

	t.Run("does not mutate input", func(t *testing.T) {
		input := map[string]string{"API_KEY": "supersecretkey"}
		_ = RedactEnvValues(input)
		assert.Equal(t, "supersecretkey", input["API_KEY"], "input should be unchanged")
	})
}

func TestRedactURLQueryParams(t *testing.T) {
	t.Run("masks sensitive query params, keeps others", func(t *testing.T) {
		got := RedactURLQueryParams("https://api.example.com/mcp?apikey=supersecretkey&region=eu")
		assert.NotContains(t, got, "supersecretkey", "plaintext secret must not survive")
		assert.Contains(t, got, "region=eu", "non-sensitive params stay readable")
	})

	t.Run("masks token, key, sig, signature params", func(t *testing.T) {
		got := RedactURLQueryParams("https://h/mcp?token=aaaaaaaaaa&key=bbbbbbbbbb&sig=cccccccccc&signature=dddddddddd")
		assert.NotContains(t, got, "aaaaaaaaaa")
		assert.NotContains(t, got, "bbbbbbbbbb")
		assert.NotContains(t, got, "cccccccccc")
		assert.NotContains(t, got, "dddddddddd")
	})

	t.Run("reference-valued sensitive params pass through unchanged", func(t *testing.T) {
		in := "https://api.example.com/mcp?apikey=${env:MY_KEY}&region=eu"
		got := RedactURLQueryParams(in)
		assert.Equal(t, in, got, "config references are labels, not secrets — leave verbatim")
	})

	t.Run("mixed reference and secret preserves the reference verbatim", func(t *testing.T) {
		got := RedactURLQueryParams("https://h/mcp?apikey=${env:MY_KEY}&token=realsecret99")
		assert.Contains(t, got, "apikey=${env:MY_KEY}", "reference must remain recognizable")
		assert.NotContains(t, got, "realsecret99", "the real secret must be masked")
	})

	t.Run("no query string returns URL unchanged", func(t *testing.T) {
		in := "https://api.example.com/mcp"
		assert.Equal(t, in, RedactURLQueryParams(in))
	})

	t.Run("no sensitive params returns URL unchanged", func(t *testing.T) {
		in := "https://api.example.com/mcp?region=eu&limit=10"
		assert.Equal(t, in, RedactURLQueryParams(in))
	})

	t.Run("empty URL returns empty", func(t *testing.T) {
		assert.Empty(t, RedactURLQueryParams(""))
	})

	t.Run("case-insensitive param matching", func(t *testing.T) {
		got := RedactURLQueryParams("https://h/mcp?ApiKey=supersecretkey")
		assert.NotContains(t, got, "supersecretkey")
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
