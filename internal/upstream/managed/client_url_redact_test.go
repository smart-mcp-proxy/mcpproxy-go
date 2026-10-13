package managed

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// TestOnStateChange_DeprecatedEndpointDoesNotLogRawURL covers issue #1148
// round 4: the deprecated-endpoint branch of onStateChange logged the raw
// configured upstream URL as `current_url` at ERROR level — the exact twin of
// the connection_oauth.go site that round 3 fixed. A 410 Gone from a server
// whose URL carries `?token=…` (or basic-auth userinfo) wrote the credential
// straight into main.log and the per-server log, which
// `upstream_servers tail_log` hands back to any MCP caller.
func TestOnStateChange_DeprecatedEndpointDoesNotLogRawURL(t *testing.T) {
	obsCore, logs := observer.New(zapcore.DebugLevel)

	mc := &Client{logger: zap.New(obsCore)}
	mc.SetConfig(&config.ServerConfig{
		Name: "deprecated-server",
		URL:  "https://alice:hunter2phrase@host/sse?token=urlsecret999&debug=1",
	})
	mc.StateManager = types.NewStateManager()

	mc.onStateChange(types.StateConnecting, types.StateError, &types.ConnectionInfo{
		State:     types.StateError,
		LastError: errors.New("410 Gone: this endpoint has been deprecated"),
	})

	var b strings.Builder
	for _, entry := range logs.All() {
		b.WriteString(entry.Message)
		for _, f := range entry.Context {
			b.WriteString(" ")
			b.WriteString(f.String)
		}
		b.WriteString("\n")
	}

	assert.Contains(t, b.String(), "ENDPOINT DEPRECATED", "precondition: the deprecated-endpoint branch ran")
	assert.NotContains(t, b.String(), "urlsecret999", "the query credential must not reach the log")
	assert.NotContains(t, b.String(), "hunter2phrase", "the userinfo password must not reach the log")
	assert.Contains(t, b.String(), "host/sse", "host and path must stay readable")
	assert.Contains(t, b.String(), "debug=1", "non-sensitive parameters must stay readable")
}
