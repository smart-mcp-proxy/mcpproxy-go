package httpapi

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Issue #872: the REST /api/v1/servers response must mask env-var secrets and
// URL query credentials alongside headers, and scrub secrets echoed into the
// last_error / health.detail strings — the same fields carried on the SSE path
// so the Web UI's mergeServers doesn't flicker masked-vs-plaintext.
func TestRedactServerSecretFields(t *testing.T) {
	srv := &contracts.Server{
		Name: "alpha",
		URL:  "https://api.example.com/mcp?apikey=supersecretkey123&region=eu",
		Headers: map[string]string{
			"Authorization": "Bearer super-secret-token",
			"Content-Type":  "application/json",
		},
		Env: map[string]string{
			"GITHUB_TOKEN": "ghp_fake_secret_value_1234",
			"LOG_LEVEL":    "debug",
			"API_KEY":      "${keyring:my-key}",
		},
		LastError: "dial https://api.example.com/mcp?token=leakedsecret failed",
		Health: &contracts.HealthStatus{
			Detail: "connect error: apikey=anothersecret rejected",
		},
		// Spec 044 diagnostic — its Cause echoes the raw connect error, which
		// commonly carries the full upstream URL (query secrets and all).
		Diagnostic: &contracts.Diagnostic{
			Code:  "MCPX_HTTP_DNS_FAILED",
			Cause: "Post \"https://api.example.com/mcp?access_token=diagsecret999&region=eu\": no such host",
		},
	}

	redactServerSecretFields(srv)

	// URL query secret masked, path + non-sensitive param intact.
	assert.NotContains(t, srv.URL, "supersecretkey123")
	assert.Contains(t, srv.URL, "region=eu")

	// Headers still masked.
	assert.NotContains(t, srv.Headers["Authorization"], "super-secret-token")
	assert.Contains(t, srv.Headers["Authorization"], "••••")
	assert.Equal(t, "application/json", srv.Headers["Content-Type"])

	// Env secrets masked; non-sensitive readable; references verbatim.
	assert.NotContains(t, srv.Env["GITHUB_TOKEN"], "ghp_fake_secret_value_1234")
	assert.Equal(t, "debug", srv.Env["LOG_LEVEL"])
	assert.Equal(t, "${keyring:my-key}", srv.Env["API_KEY"])

	// URL secrets scrubbed from error/detail strings.
	assert.NotContains(t, srv.LastError, "leakedsecret")
	assert.NotContains(t, srv.Health.Detail, "anothersecret")

	// URL secrets scrubbed from the structured diagnostic cause too.
	assert.NotContains(t, srv.Diagnostic.Cause, "diagsecret999")
}

// nil-safe: a server with no secret-bearing fields must pass through untouched.
func TestRedactServerSecretFields_NoSecrets(t *testing.T) {
	srv := &contracts.Server{
		Name: "beta",
		URL:  "https://api.example.com/mcp",
		Env:  map[string]string{"LOG_LEVEL": "info"},
	}
	redactServerSecretFields(srv)
	assert.Equal(t, "https://api.example.com/mcp", srv.URL)
	assert.Equal(t, "info", srv.Env["LOG_LEVEL"])
	assert.Nil(t, srv.Health)
}
