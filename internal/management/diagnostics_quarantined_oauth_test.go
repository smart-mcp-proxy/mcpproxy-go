package management

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
)

// A remote OAuth server imported with quarantine on (GitHub MCP at
// api.githubcopilot.com) reports health.action "approve" - quarantine outranks
// sign-in in the health calculator - while its diagnostic says
// MCPX_OAUTH_LOGIN_REQUIRED. Doctor bucketed by health.action alone, so the
// server matched no bucket, and the non-empty action suppressed the
// last_error fallback: it appeared nowhere. The Web UI's oauthSignInState reads
// the diagnostic code; Doctor must too.
func TestDoctor_QuarantinedServerAwaitingSignIn(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	quarantinedAwaitingSignIn := func(code diagnostics.Code, level string) map[string]interface{} {
		return map[string]interface{}{
			"id":          "github",
			"name":        "github",
			"enabled":     true,
			"connected":   false,
			"quarantined": true,
			"last_error":  "authorization required",
			// runtime.GetAllServers hands Doctor the named diagnostics.Code
			// type and a *contracts.HealthStatus, not JSON strings/maps.
			"error_code": string(code),
			"diagnostic": map[string]interface{}{
				"code":     code,
				"severity": "error",
			},
			"health": &contracts.HealthStatus{
				Level:      level,
				AdminState: "quarantined",
				Summary:    "Quarantined — Sign-in required",
				Action:     "approve",
			},
		}
	}

	run := func(t *testing.T, servers ...map[string]interface{}) *contracts.Diagnostics {
		t.Helper()
		rt := newMockRuntime()
		rt.servers = servers
		svc := NewService(rt, &config.Config{}, "", &mockEventEmitter{}, nil, logger)
		diag, err := svc.Doctor(context.Background())
		require.NoError(t, err)
		return diag
	}

	t.Run("first-time sign-in lands in OAuthRequired", func(t *testing.T) {
		diag := run(t, quarantinedAwaitingSignIn(diagnostics.OAuthLoginRequired, "degraded"))

		require.Len(t, diag.OAuthRequired, 1)
		req := diag.OAuthRequired[0]
		assert.Equal(t, "github", req.ServerName)
		assert.Equal(t, "unauthenticated", req.State)
		assert.Contains(t, req.Message, "mcpproxy auth login --server=github")
		// Review stays a parallel action, not dropped for sign-in.
		assert.Contains(t, req.Message, "also quarantined: review and approve it")
		assert.Empty(t, diag.UpstreamErrors, "a sign-in wait is not an upstream error")
		assert.Equal(t, 1, diag.TotalIssues)
	})

	t.Run("re-auth codes land in OAuthRequired as expired", func(t *testing.T) {
		for _, code := range []diagnostics.Code{
			diagnostics.OAuthReauthRequired,
			diagnostics.OAuthRefreshExpired,
			diagnostics.OAuthRefresh403,
		} {
			diag := run(t, quarantinedAwaitingSignIn(code, "unhealthy"))
			require.Len(t, diag.OAuthRequired, 1, code)
			assert.Equal(t, "expired", diag.OAuthRequired[0].State, code)
			assert.Contains(t, diag.OAuthRequired[0].Message, "mcpproxy auth login --server=github", code)
			assert.Contains(t, diag.OAuthRequired[0].Message, "also quarantined", code)
		}
	})

	// No health action (e.g. no connection info yet) with a sticky sign-in
	// diagnostic and last_error: the last_error fallback must not ALSO list
	// it as an upstream error, or it double-counts in TotalIssues.
	t.Run("no health action is listed once, as sign-in", func(t *testing.T) {
		srv := quarantinedAwaitingSignIn(diagnostics.OAuthLoginRequired, "")
		srv["quarantined"] = false
		delete(srv, "health")
		diag := run(t, srv)
		require.Len(t, diag.OAuthRequired, 1)
		assert.Empty(t, diag.UpstreamErrors)
		assert.Equal(t, 1, diag.TotalIssues)
	})

	t.Run("code after a JSON round-trip is a plain string", func(t *testing.T) {
		srv := quarantinedAwaitingSignIn(diagnostics.OAuthLoginRequired, "degraded")
		srv["diagnostic"] = map[string]interface{}{"code": "MCPX_OAUTH_LOGIN_REQUIRED"}
		srv["health"] = map[string]interface{}{"level": "degraded", "admin_state": "quarantined", "action": "approve"}
		diag := run(t, srv)
		require.Len(t, diag.OAuthRequired, 1)
	})

	t.Run("error_code alone is enough", func(t *testing.T) {
		srv := quarantinedAwaitingSignIn(diagnostics.OAuthLoginRequired, "degraded")
		delete(srv, "diagnostic")
		diag := run(t, srv)
		require.Len(t, diag.OAuthRequired, 1)
	})

	t.Run("other OAuth faults are not a sign-in", func(t *testing.T) {
		diag := run(t, quarantinedAwaitingSignIn(diagnostics.OAuthDiscoveryFailed, "unhealthy"))
		assert.Empty(t, diag.OAuthRequired)
	})

	t.Run("disabled server with a stale sign-in diagnostic is not listed", func(t *testing.T) {
		srv := quarantinedAwaitingSignIn(diagnostics.OAuthLoginRequired, "healthy")
		srv["enabled"] = false
		srv["quarantined"] = false
		srv["health"] = &contracts.HealthStatus{Level: "healthy", AdminState: "disabled", Action: "enable"}
		diag := run(t, srv)
		assert.Empty(t, diag.OAuthRequired)
	})

	t.Run("action login with a sign-in code is listed once", func(t *testing.T) {
		srv := quarantinedAwaitingSignIn(diagnostics.OAuthLoginRequired, "degraded")
		srv["quarantined"] = false
		srv["health"] = &contracts.HealthStatus{Level: "degraded", AdminState: "enabled", Action: "login"}
		diag := run(t, srv)
		require.Len(t, diag.OAuthRequired, 1)
		assert.NotContains(t, diag.OAuthRequired[0].Message, "quarantined",
			"no quarantine hint for a server that is not quarantined")
	})
}
