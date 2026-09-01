package core

import (
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
