package managed

import (
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// SignInRequired reports whether a server that is not connected is waiting
// for a login rather than being unreachable. It is the one rule behind the
// error class of a call to such a server and the upstream event recorded when
// it gets there (release QA F1), so the two cannot disagree.
//
// A login is required when the operator logged the server out, when the
// client parked it awaiting a login, or when its last failure was an OAuth
// one: an OAuth error, an "authorization required" answer or a login code. A
// network failure is never a login requirement, even when an earlier OAuth
// failure left the OAuth flag set: an offline server reads as offline.
func SignInRequired(state types.ConnectionState, loggedOut, oauthError bool, err error, terminalCode string) bool {
	switch state {
	case types.StateReady:
		return false
	case types.StatePendingAuth:
		return true
	}
	if loggedOut {
		return true
	}
	if state != types.StateError {
		return false
	}
	if isLoginCode(terminalCode) {
		return true
	}
	if err == nil || isNetworkFailure(err) || isServerSideAnswer(err) {
		return false
	}
	if oauthError || isAuthorizationRequiredText(err.Error()) {
		return true
	}
	return isLoginCode(string(diagnostics.Classify(err, diagnostics.ClassifierHints{Transport: "http"})))
}

// NeedsSignIn applies SignInRequired to the client's current state.
func (mc *Client) NeedsSignIn() bool {
	if mc == nil || mc.StateManager == nil {
		return false
	}
	info := mc.StateManager.GetConnectionInfo()
	return SignInRequired(info.State, mc.StateManager.IsUserLoggedOut(), info.IsOAuthError, info.LastError, info.TerminalCode)
}

func isLoginCode(code string) bool {
	switch diagnostics.Code(code) {
	case diagnostics.OAuthLoginRequired, diagnostics.OAuthReauthRequired, diagnostics.OAuthRefreshExpired:
		return true
	}
	return false
}

// isNetworkFailure is a typed network error, or an error the diagnostics
// classifier files under the HTTP transport or network families (a text-only
// "connection refused" that lost its type when it was wrapped).
func isNetworkFailure(err error) bool {
	if core.IsNetworkError(err) {
		return true
	}
	// Transport failures only: an HTTP status (401, 403, 429, ...) is an
	// answer from a server that is reachable.
	switch code := diagnostics.Classify(err, diagnostics.ClassifierHints{Transport: "http"}); code {
	case diagnostics.HTTPConnRefuse, diagnostics.HTTPConnReset, diagnostics.HTTPDNSFailed,
		diagnostics.HTTPTLSFailed, diagnostics.HTTPTimeout, diagnostics.HTTPCanceled:
		return true
	default:
		if strings.HasPrefix(string(code), "MCPX_NETWORK_") {
			return true
		}
	}
	// The classifier matches typed errors; an error that was flattened to
	// text on its way here keeps only the words.
	msg := strings.ToLower(err.Error())
	for _, phrase := range []string{
		"connection refused", "connection reset", "no such host", "i/o timeout",
		"network is unreachable", "host is unreachable", "dial tcp", "broken pipe",
	} {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

// isServerSideAnswer is an HTTP answer that says nothing about credentials:
// the server or a proxy in front of it failed (5xx), it limits the rate, the
// endpoint is gone, or it speaks the legacy SSE transport. An OAuth error flag
// left by an earlier failure does not make such an answer a login need.
func isServerSideAnswer(err error) bool {
	switch diagnostics.Classify(err, diagnostics.ClassifierHints{Transport: "http"}) {
	case diagnostics.HTTPServerErr, diagnostics.HTTPRateLimited, diagnostics.HTTPNotFound, diagnostics.HTTPLegacySSE:
		return true
	}
	return false
}

// isAuthorizationRequiredText matches the errors the connect path returns
// when the server asks for OAuth authorization (see
// Client.isOAuthAuthorizationRequired, which decides the same at connect).
func isAuthorizationRequiredText(s string) bool {
	for _, phrase := range []string{
		"OAuth authorization during MCP init failed",
		"OAuth authorization not implemented",
		"OAuth authorization required",
		"authorization required",
	} {
		if strings.Contains(s, phrase) {
			return true
		}
	}
	return false
}
