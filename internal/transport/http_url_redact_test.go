package transport

import (
	"strings"
	"testing"

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
