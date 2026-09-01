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

// Issue #1148, round 7 finding 2: the REST/SSE door skipped the isolation
// blocks entirely, so `isolation.extra_args` — free text an operator puts
// `-e API_KEY=<token>` into — was masked on the MCP door and published in the
// clear here and on every /events servers.changed payload.
//
// `isolation_defaults` is included because it is RESOLVED FROM the global
// docker_isolation block: a credential in the global `extra_args` lands in it
// verbatim (internal/management/service.go). Round 7 finding 4 re-judged it
// from "not secret" for exactly that reason.
func TestRedactServerSecretFields_MasksIsolationBlocks(t *testing.T) {
	const token = "ghp_1234567890abcdefghijABCDEFGHIJ123456"
	srv := &contracts.Server{
		Name: "alpha",
		Isolation: &contracts.IsolationConfig{
			Enabled:   true,
			Image:     "python:3.12",
			ExtraArgs: []string{"-e", "SERVER_API_KEY=" + token},
		},
		IsolationDefaults: &contracts.IsolationDefaults{
			RuntimeType: "uvx",
			Image:       "ghcr.io/astral-sh/uv:python3.13-alpine",
			ExtraArgs:   []string{"-e", "GLOBAL_API_KEY=" + token},
		},
	}

	redactServerSecretFields(srv)

	for _, arg := range srv.Isolation.ExtraArgs {
		assert.NotContains(t, arg, token, "isolation.extra_args published a credential")
	}
	for _, arg := range srv.IsolationDefaults.ExtraArgs {
		assert.NotContains(t, arg, token, "isolation_defaults.extra_args published a credential")
	}

	// Masked-but-PRESENT: the UI renders these as override placeholders, so the
	// non-secret values must survive byte for byte.
	assert.True(t, srv.Isolation.Enabled)
	assert.Equal(t, "python:3.12", srv.Isolation.Image)
	assert.Equal(t, "ghcr.io/astral-sh/uv:python3.13-alpine", srv.IsolationDefaults.Image)
	assert.Equal(t, "uvx", srv.IsolationDefaults.RuntimeType)
	assert.Equal(t, "-e", srv.Isolation.ExtraArgs[0])
	assert.Contains(t, srv.Isolation.ExtraArgs[1], "SERVER_API_KEY",
		"the variable NAME is the audit signal and must survive")
}
