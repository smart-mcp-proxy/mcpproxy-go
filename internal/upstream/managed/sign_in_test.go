package managed

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// Release QA F1: a server that is up but needs a login must not read as
// unreachable, and an unreachable server must not read as needing a login.
func TestSignInRequired(t *testing.T) {
	refused := fmt.Errorf("failed to list tools: transport error: %w",
		&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED})
	refusedText := errors.New(`failed to send request: Post "http://127.0.0.1:1/mcp": dial tcp 127.0.0.1:1: connect: connection refused`)
	authInit := errors.New("OAuth authorization during MCP init failed: OAuth authorization required - deferred for background processing")
	invalidGrant := errors.New("oauth2: \"invalid_grant\" \"refresh token is invalid\"")

	cases := []struct {
		name         string
		state        types.ConnectionState
		loggedOut    bool
		oauthError   bool
		err          error
		terminalCode string
		want         bool
	}{
		{"logged out, disconnected", types.StateDisconnected, true, false, nil, "", true},
		{"logged out and now unreachable", types.StateError, true, false, refused, "", true},
		{"parked awaiting login", types.StatePendingAuth, false, false, authInit, "", true},
		{"restart without a token", types.StateError, false, false, authInit, "", true},
		{"OAuth failure", types.StateError, false, true, invalidGrant, "", true},
		{"login code", types.StateError, false, false, errors.New("x"), "MCPX_OAUTH_LOGIN_REQUIRED", true},
		{"revoked token answered 401", types.StateError, false, true, errors.New("unexpected status code: 401"), "", true},
		{"server answered 403 after an OAuth failure", types.StateError, false, true, errors.New("request failed with status 403 Forbidden"), "", true},

		{"bad gateway after an earlier OAuth failure", types.StateError, false, true, errors.New("unexpected status code: 502"), "", false},
		{"rate limited after an earlier OAuth failure", types.StateError, false, true, errors.New("unexpected status code: 429"), "", false},
		{"not found after an earlier OAuth failure", types.StateError, false, true, errors.New("unexpected status code: 404"), "", false},
		{"offline", types.StateError, false, false, refused, "", false},
		{"offline, untyped error text", types.StateError, false, false, refusedText, "", false},
		{"offline after an earlier OAuth failure", types.StateError, false, true, refused, "", false},
		{"offline text after an earlier OAuth failure", types.StateError, false, true, refusedText, "", false},
		{"plain error", types.StateError, false, false, errors.New("tool list malformed"), "", false},
		{"connecting", types.StateConnecting, false, false, nil, "", false},
		{"disconnected, not logged out", types.StateDisconnected, false, false, nil, "", false},
		{"ready with a stale logged-out flag", types.StateReady, true, true, nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, SignInRequired(tc.state, tc.loggedOut, tc.oauthError, tc.err, tc.terminalCode))
		})
	}
}

// Review (Opus, F1 round): the code_execution path recognises a call to a
// client that is not connected by a sentinel, so a retired instance or a
// moved generation keeps its own (proxy) class.
func TestCallTool_NotConnectedIsTyped(t *testing.T) {
	mc := &Client{StateManager: types.NewStateManager()}
	_, err := mc.callTool(context.Background(), "x", nil, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrClientNotConnected)
	assert.Contains(t, err.Error(), "client not connected (state: ", "the text callers and logs know is kept")

	mc.retired.Store(true)
	_, err = mc.callTool(context.Background(), "x", nil, nil)
	assert.NotErrorIs(t, err, ErrClientNotConnected)
}
