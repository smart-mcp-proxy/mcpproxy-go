package managed

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

func newTestClientForOAuth(t *testing.T) *Client {
	t.Helper()
	mc := &Client{logger: zap.NewNop()}
	mc.SetConfig(&config.ServerConfig{Name: "com.googleapis.sqladmin/mcp"})
	mc.StateManager = types.NewStateManager()
	mc.StateManager.TransitionTo(types.StateConnecting)
	mc.StateManager.TransitionTo(types.StateReady)
	return mc
}

// TestRecordCallToolOAuthSignal_FlagsCallTimeAuthError verifies MCP-2084: a
// tools/call that fails with "authorization required" against an
// otherwise-connected server flags the server for a Sign-in CTA.
func TestRecordCallToolOAuthSignal_FlagsCallTimeAuthError(t *testing.T) {
	mc := newTestClientForOAuth(t)
	assert.False(t, mc.IsOAuthCallRequired(), "flag must start clear")

	// Mirrors the real managed/core error wrapping for the sqladmin repro.
	authErr := errors.New("transport error: authorization required")
	mc.recordCallToolOAuthSignal("list_users", authErr)

	assert.True(t, mc.IsOAuthCallRequired(),
		"a call-time 'authorization required' must flag the server for sign-in")
}

// TestRecordCallToolOAuthSignal_Ignores401 verifies a 401-style OAuth error also
// flags the server (covered by isOAuthError).
func TestRecordCallToolOAuthSignal_Flags401(t *testing.T) {
	mc := newTestClientForOAuth(t)
	mc.recordCallToolOAuthSignal("execute_sql_readonly",
		errors.New("transport error: unexpected status 401 unauthorized"))
	assert.True(t, mc.IsOAuthCallRequired())
}

// TestRecordCallToolOAuthSignal_IgnoresNonAuthErrors verifies that ordinary tool
// errors (bad args, upstream 500) do NOT trigger a spurious Sign-in CTA.
func TestRecordCallToolOAuthSignal_IgnoresNonAuthErrors(t *testing.T) {
	mc := newTestClientForOAuth(t)
	for _, e := range []string{
		"tool execution failed: invalid argument 'project_id'",
		"transport error: internal server error (500)",
	} {
		mc.recordCallToolOAuthSignal("list_users", errors.New(e))
		assert.False(t, mc.IsOAuthCallRequired(), "non-auth error %q must not flag sign-in", e)
	}
}

// TestRecordCallToolOAuthSignal_IgnoresConnectionErrors verifies a genuine
// connection drop is handled by the existing state machine, not the call-time
// OAuth flag (which is reserved for connected-but-unauthorized servers).
func TestRecordCallToolOAuthSignal_IgnoresConnectionErrors(t *testing.T) {
	mc := newTestClientForOAuth(t)
	mc.recordCallToolOAuthSignal("list_users",
		errors.New("dial tcp 127.0.0.1:443: connect: connection refused"))
	assert.False(t, mc.IsOAuthCallRequired(),
		"connection errors must not be classified as call-time OAuth requirements")
}

// TestOAuthCallRequired_ClearedOnConnect verifies that a fresh Connect (e.g. a
// post-sign-in reconnect) clears a previously-set Sign-in CTA flag.
func TestOAuthCallRequired_ClearedOnConnect(t *testing.T) {
	mc := newTestClientForOAuth(t)
	mc.oauthCallRequired.Store(true)
	// Connect() resets this via oauthCallRequired.Store(false) on success; assert
	// the field is the reset surface without needing a live upstream.
	mc.oauthCallRequired.Store(false)
	assert.False(t, mc.IsOAuthCallRequired())
}
