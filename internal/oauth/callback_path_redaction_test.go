package oauth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// SEC-01 follow-up. The OAuth loopback callback listener redacted its QUERY
// (LogSafeCallbackQuery, issue #1158) but logged `zap.String("path",
// r.URL.Path)` verbatim at INFO, in two places: the listener's own handler and
// handleCallback below it.
//
// The callback path is normally a fixed operator-configured value, so this is
// consistency work rather than a demonstrated leak of the user's own admin key.
// It is still a real client-controlled sink: the handler is a bare
// http.HandlerFunc with no mux in front of it (deliberately — see the comment
// in StartCallbackServer), so EVERY path reaches the log line, including the
// ones that fall through to the debug page; the listener is a plain loopback
// HTTP server that any local process or any page in the user's browser can
// reach for the whole login window; and r.URL.Path arrives percent-decoded, so
// `%3Fapikey%3D` and `Bearer%20` land here as the real thing.
//
// Every other field on these two lines already goes through this package's
// redactors. The path was the one that did not.

// newObservedCallbackServerAtPath builds a CallbackServer wired to an
// in-memory log sink and serving `path`, so the fall-through (debug page)
// branch of handleRequest can be exercised.
func newObservedCallbackServerAtPath(t *testing.T, path string) (*CallbackServer, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	return &CallbackServer{
		ServerName: "alpha",
		Path:       path,
		Port:       53682,
		logger:     zap.New(core),
		waiters:    make(map[string]chan map[string]string),
	}, logs
}

// callbackKeyInPath is the shape an `?apikey=` credential takes once it has
// been percent-decoded into r.URL.Path. 64 hex characters is what
// cmd/mcpproxy generates for the admin key.
const callbackKeyInPath = "4f3c2b1a9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b"

func TestCallbackListenerRedactsACredentialInTheRequestPath(t *testing.T) {
	cs, logs := newObservedCallbackServerAtPath(t, DefaultRedirectPath)

	cs.handleRequest(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/probe/apikey="+callbackKeyInPath, nil))

	out := renderedLines(logs)
	require.NotEmpty(t, out, "the handler must still log — a fix that deletes the line is not the fix")
	require.Contains(t, out, "/probe/", "the diagnostic part of the path must survive redaction")
	assertNoRun(t, out, callbackKeyInPath, 12)
}

func TestHandleCallbackRedactsACredentialInTheRequestPath(t *testing.T) {
	cs, logs := newObservedCallbackServerAtPath(t, DefaultRedirectPath)

	cs.handleCallback(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet,
			DefaultRedirectPath+"/apikey="+callbackKeyInPath+"?error=access_denied", nil))

	assertNoRun(t, renderedLines(logs), callbackKeyInPath, 12)
}

// r.URL.Path arrives percent-decoded, so `Bearer%20<token>` reaches this log
// field as a real `Bearer <token>` — a shape no `name=value` rule can see.
func TestCallbackListenerRedactsABearerTokenInTheRequestPath(t *testing.T) {
	const agentToken = "mcp_agt_Zt7Qv2Lm9XbR4pWc8HsKd3Ng6JyF1aUe"

	cs, logs := newObservedCallbackServerAtPath(t, DefaultRedirectPath)

	cs.handleRequest(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/probe/Bearer%20"+agentToken, nil))

	assertNoRun(t, renderedLines(logs), agentToken, 12)
}

// TestLogSafeRequestPathRedactsAnEncodedQueryInsideThePath covers the shape
// that makes the two path sinks a LIVE-credential risk rather than a purely
// theoretical one, and that LogSafeRequestPath did not handle.
//
// `?apikey=<KEY>` is the credential form mcpproxy's own Connect wizard and Web
// UI hand out, and a client that percent-encodes its URL suffix sends
// `/mcp/%3Fapikey%3D<KEY>` — which net/http decodes into
// r.URL.Path == "/mcp/?apikey=<KEY>" before any handler sees it. ServeMux
// matches that against the `/mcp/` SUBTREE pattern, so it reaches the handler,
// and the log field gets the decoded form.
//
// logSafeURLComponent split only on '/', so the segment handed to the name rule
// was "?apikey=<KEY>" — whose parameter name reads as "?apikey", which matches
// nothing — and the value is plain hex, under the entropy detector's threshold.
// The credential went to disk in the clear. A path segment carries `k=v` pairs
// after a '?' exactly as a fragment does, and logSafeFragment already accounted
// for that; the path did not.
func TestLogSafeRequestPathRedactsAnEncodedQueryInsideThePath(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/mcp/%3Fapikey%3D"+callbackKeyInPath, nil)
	require.Equal(t, "/mcp/?apikey="+callbackKeyInPath, r.URL.Path,
		"net/http must still hand the handler a DECODED path — the premise of this test")

	rendered := LogSafeRequestPath(r.URL.Path)
	require.Contains(t, rendered, "/mcp/", "the diagnostic part of the path must survive redaction")
	assertNoRun(t, rendered, callbackKeyInPath, 12)
}
