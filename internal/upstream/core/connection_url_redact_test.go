package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestLogSafeURL_RedactsQueryCredentials covers the SOURCE half of issue #1148
// (c2). Connect() logged zap.String("url", c.config.URL) into both main.log and
// the per-server server-<name>.log, so a URL-embedded credential
// (`https://host/mcp?token=…`, an Azure SAS `sig=`, an AWS `X-Amz-Signature=`)
// was written to disk on every connection attempt — and `tail_log` then handed
// those lines to any MCP caller.
//
// Scrubbing at the tail_log read seam is necessary but not sufficient: the file
// itself is readable by anything with disk access, and it outlives the process.
func TestLogSafeURL_RedactsQueryCredentials(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		secret string
	}{
		{"token query param", "https://host/mcp?token=urlsecret999", "urlsecret999"},
		{"azure sas signature", "https://host/mcp?sig=sasSECRET123", "sasSECRET123"},
		{"access token", "https://host/mcp?access_token=at-SECRET-42", "at-SECRET-42"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{config: &config.ServerConfig{Name: "srv", URL: tc.url}}
			got := c.logSafeURL()
			assert.NotContains(t, got, tc.secret, "the configured URL must never be logged with its credential")
			assert.True(t, strings.HasPrefix(got, "https://host/mcp"), "the diagnostic part of the URL must survive, got %q", got)
		})
	}

	// A credential-free URL is untouched, so ordinary debugging is unaffected.
	plain := &Client{config: &config.ServerConfig{URL: "https://host/mcp?debug=1"}}
	assert.Equal(t, "https://host/mcp?debug=1", plain.logSafeURL())

	// An empty URL (stdio servers) stays empty rather than becoming a marker.
	empty := &Client{config: &config.ServerConfig{}}
	assert.Equal(t, "", empty.logSafeURL())
}

// TestRedactURLCredentialsInError covers issue #1148 round 3: masking the `url`
// log fields left the same credential travelling inside the transport error's
// own message, which the manager logs at Error level on every failed attempt
// and the client keeps as its last error.
//
// The redaction must not cost the error its identity: the connect paths
// classify by errors.Is/As and by substring, and both have to keep working.
func TestRedactURLCredentialsInError(t *testing.T) {
	cause := errors.New(`Post "https://host/mcp?token=urlsecret999": dial tcp: connection refused`)
	wrapped := fmt.Errorf("failed to connect: %w", cause)

	got := redactURLCredentialsInError(wrapped)

	assert.NotContains(t, got.Error(), "urlsecret999", "the URL credential must not survive in the error text")
	assert.Contains(t, got.Error(), "connection refused", "substring classification must keep working")
	assert.True(t, errors.Is(got, cause), "the original error must stay reachable through Unwrap")

	t.Run("an error with nothing to redact is returned unchanged", func(t *testing.T) {
		plain := errors.New("dial tcp 127.0.0.1:1: connection refused")
		assert.Same(t, plain, redactURLCredentialsInError(plain))
	})

	t.Run("nil stays nil", func(t *testing.T) {
		assert.NoError(t, redactURLCredentialsInError(nil))
	})
}
