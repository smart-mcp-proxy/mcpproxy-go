package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// SEC-01 follow-up. PR #1350 routed every `path` log field in internal/httpapi
// through oauth.LogSafeRequestPath. The MCP mux's own logging wrapper was left
// behind: it wrote `zap.String("path", r.URL.Path)` verbatim, three times per
// request (Debug on arrival, then Debug or Warn on completion).
//
// That is a live sink for a client-controlled string. `/mcp/` and `/mcp/p/` are
// registered as SUBTREE patterns, so every byte after the prefix is whatever
// the caller sent; `ExtractToken` accepts `?apikey=<KEY>` on this very
// endpoint, so a client configured with the key in the wrong part of the URL is
// not a hypothetical shape; and r.URL.Path arrives percent-DECODED, so
// `%3Fapikey%3D` and `Bearer%20<token>` both land here as the real thing. The
// completion line is logged at Warn on any >=400 response, which is on at the
// DEFAULT log level — no debug flag involved.
//
// The oracle is the one issue #1158 settled on: no RUN of the credential's
// bytes may survive anywhere in the rendered line. A whole-string containment
// check passes against a half-mask.
func assertNoCredentialRun(t *testing.T, rendered, secret string, minRun int) {
	t.Helper()
	for i := 0; i+minRun <= len(secret); i++ {
		assert.NotContains(t, rendered, secret[i:i+minRun],
			"a %d-byte run of the credential survived into %q", minRun, rendered)
	}
}

func renderObservedLines(logs *observer.ObservedLogs) string {
	var b strings.Builder
	for _, entry := range logs.All() {
		b.WriteString(entry.Message)
		for k, v := range entry.ContextMap() {
			fmt.Fprintf(&b, " %s=%v", k, v)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// newObservedMCPLoggingHandler builds the MCP logging wrapper against an
// in-memory log sink. mcpLoggingHandler reads nothing from Server but the
// logger, so no runtime, storage or listener is needed.
func newObservedMCPLoggingHandler(t *testing.T, status int) (http.Handler, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	s := &Server{logger: zap.New(core)}
	return s.mcpLoggingHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	})), logs
}

// adminKeyInPath is the shape an `?apikey=` credential takes once it has been
// percent-decoded into r.URL.Path, or typed into the path by a
// misconfigured client. 64 hex characters is what cmd/mcpproxy generates.
const adminKeyInPath = "4f3c2b1a9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b"

func TestMCPLoggingHandlerRedactsANamedCredentialInThePath(t *testing.T) {
	handler, logs := newObservedMCPLoggingHandler(t, http.StatusOK)

	handler.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/mcp/apikey="+adminKeyInPath, nil))

	out := renderObservedLines(logs)
	require.NotEmpty(t, out, "the handler must still log — a fix that deletes the line is not the fix")
	require.Contains(t, out, "/mcp/", "the diagnostic part of the path must survive redaction")
	assertNoCredentialRun(t, out, adminKeyInPath, 12)
}

// The completion line at Warn is the one that fires at the DEFAULT log level,
// so it is the one that actually writes to main.log on a stock install.
func TestMCPLoggingHandlerRedactsThePathOnTheWarnCompletionLine(t *testing.T) {
	handler, logs := newObservedMCPLoggingHandler(t, http.StatusNotFound)

	handler.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/mcp/p/apikey="+adminKeyInPath, nil))

	warnLines := logs.FilterLevelExact(zap.WarnLevel).All()
	require.NotEmpty(t, warnLines, "a >=400 response must still be logged at Warn")
	assertNoCredentialRun(t, renderObservedLines(logs), adminKeyInPath, 12)
}

// r.URL.Path arrives percent-decoded, so `Bearer%20<token>` in the request
// target reaches this log field as a real `Bearer <token>` — a shape no
// `name=value` rule can see. LogSafeRequestPath applies the token rule for it.
func TestMCPLoggingHandlerRedactsABearerTokenInThePath(t *testing.T) {
	const agentToken = "mcp_agt_Zt7Qv2Lm9XbR4pWc8HsKd3Ng6JyF1aUe"

	handler, logs := newObservedMCPLoggingHandler(t, http.StatusOK)

	handler.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/mcp/Bearer%20"+agentToken, nil))

	assertNoCredentialRun(t, renderObservedLines(logs), agentToken, 12)
}

// The realistic delivery vehicle for the LIVE admin key. `?apikey=<KEY>` is the
// credential form mcpproxy's own Connect wizard and Web UI hand out, and
// net/http hands the handler a percent-DECODED r.URL.Path — so a client that
// encodes its URL suffix turns `GET /mcp/%3Fapikey%3D<KEY>` into the path
// `/mcp/?apikey=<KEY>`, which ServeMux still matches against the `/mcp/`
// subtree pattern and which this log field then received verbatim.
func TestMCPLoggingHandlerRedactsAnEncodedQueryInsideThePath(t *testing.T) {
	handler, logs := newObservedMCPLoggingHandler(t, http.StatusOK)

	req := httptest.NewRequest(http.MethodGet, "/mcp/%3Fapikey%3D"+adminKeyInPath, nil)
	require.Equal(t, "/mcp/?apikey="+adminKeyInPath, req.URL.Path,
		"net/http must still hand the handler a DECODED path — the premise of this test")

	handler.ServeHTTP(httptest.NewRecorder(), req)

	assertNoCredentialRun(t, renderObservedLines(logs), adminKeyInPath, 12)
}
