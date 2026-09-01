package transport

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestLogSafeURL covers issue #1148 (round 2, finding 8): the HTTP/SSE client
// creation path logged the raw upstream URL into main.log — at Error level, so
// it fired on every attempt regardless of the configured level.
func TestLogSafeURL(t *testing.T) {
	cfg := &HTTPTransportConfig{URL: "https://host/mcp?token=urlsecret999&debug=1"}
	got := cfg.logSafeURL()
	assert.NotContains(t, got, "urlsecret999")
	assert.Contains(t, got, "debug=1", "non-sensitive parameters must stay readable")
	assert.Contains(t, got, "host/mcp")

	userinfo := (&HTTPTransportConfig{URL: "https://alice:hunter2phrase@host/mcp"}).logSafeURL()
	assert.NotContains(t, userinfo, "hunter2phrase")

	assert.Equal(t, "", (&HTTPTransportConfig{}).logSafeURL())
	assert.Equal(t, "", (*HTTPTransportConfig)(nil).logSafeURL())
}

// TestCreateHTTPClient_DoesNotLogRawURL is the end-to-end guard: no log line
// emitted while building the client may carry the credential.
func TestCreateHTTPClient_DoesNotLogRawURL(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	_, err := CreateHTTPClient(&HTTPTransportConfig{URL: "https://host/mcp?token=urlsecret999"})
	assert.NoError(t, err)

	var b strings.Builder
	for _, entry := range logs.All() {
		b.WriteString(entry.Message)
		for _, f := range entry.Context {
			b.WriteString(" ")
			b.WriteString(f.String)
		}
		b.WriteString("\n")
	}
	assert.NotEmpty(t, b.String(), "precondition: client creation logs something")
	assert.NotContains(t, b.String(), "urlsecret999")
}

// TestLogSafeErrorField_RedactsURLCredentials covers issue #1148 round 3: the
// `url` log fields were masked, but a net/http error quotes the request URL
// inside its own message, so the credential kept reaching the log through
// zap.Error on the request-failure path.
func TestLogSafeErrorField_RedactsURLCredentials(t *testing.T) {
	err := errors.New(`Post "https://host/mcp?token=urlsecret999": dial tcp: connection refused`)
	field := logSafeErrorField(err)

	assert.NotContains(t, field.String, "urlsecret999", "the error text must not carry the URL credential")
	assert.Contains(t, field.String, "connection refused", "the diagnostic part must survive")
	assert.Equal(t, zapcore.SkipType, logSafeErrorField(nil).Type, "a nil error contributes no field")
}

// TestRetryAfterTransport_DoesNotLogRawURLCredentials covers issue #1148 round
// 4: the back-off log line rendered the request URL through the stdlib's
// url.URL.Redacted(), which masks ONLY the userinfo password and leaves a
// `?token=…` query credential verbatim.
func TestRetryAfterTransport_DoesNotLogRawURLCredentials(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	rec := NewRetryAfterRecorder()
	client := &http.Client{Transport: NewRetryAfterTransport(http.DefaultTransport, rec, zap.New(core))}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/mcp?token=urlsecret999&debug=1", http.NoBody)
	assert.NoError(t, err)
	req.URL.User = url.UserPassword("alice", "hunter2phrase")

	resp, err := client.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	var b strings.Builder
	for _, entry := range logs.All() {
		b.WriteString(entry.Message)
		for _, f := range entry.Context {
			b.WriteString(" ")
			b.WriteString(f.String)
		}
		b.WriteString("\n")
	}
	assert.Contains(t, b.String(), "back off", "precondition: the back-off line was emitted")
	assert.NotContains(t, b.String(), "urlsecret999", "the query credential must not reach the log")
	assert.NotContains(t, b.String(), "hunter2phrase", "the userinfo password must not reach the log")
	assert.Contains(t, b.String(), "debug=1", "non-sensitive parameters must stay readable")
}

// TestCreateOAuthClient_DoesNotLogRawURLThroughTheError is the last sweep site
// in internal/transport (issue #1148, round 4): the two OAuth client-creation
// failures logged the constructor error with a bare zap.Error. mcp-go builds
// that error from url.Parse, and net/url's *url.Error quotes the raw URL it was
// handed — so a malformed URL carrying a credential wrote it to the log even
// though the sibling `url` fields on the very same function are masked.
func TestCreateOAuthClient_DoesNotLogRawURLThroughTheError(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	// A DEL control byte makes url.Parse fail while the credentials stay in the
	// string the error quotes back.
	bad := "https://alice:hunter2phrase@host/mcp?token=urlsecret999\x7f"

	_, err := CreateHTTPClient(&HTTPTransportConfig{
		URL:         bad,
		UseOAuth:    true,
		OAuthConfig: &client.OAuthConfig{},
	})
	assert.Error(t, err, "precondition: the malformed URL must fail construction")

	rendered := renderEntries(logs.All())
	assert.Contains(t, rendered, "Failed to create OAuth client", "precondition: the site under test ran")
	assert.NotContains(t, rendered, "urlsecret999")
	assert.NotContains(t, rendered, "hunter2phrase")
}

// renderEntries flattens observed entries INCLUDING the non-string fields, so a
// credential riding inside a zap.Error cannot slip past the assertion.
func renderEntries(entries []observer.LoggedEntry) string {
	var b strings.Builder
	for _, entry := range entries {
		b.WriteString(entry.Message)
		b.WriteString(" ")
		b.WriteString(fmt.Sprint(entry.ContextMap()))
		b.WriteString("\n")
	}
	return b.String()
}
