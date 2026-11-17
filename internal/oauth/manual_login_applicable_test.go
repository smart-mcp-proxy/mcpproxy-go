package oauth

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The manual-login preflight only rules out what can never work.
//
// GH #1271: an oauth block declares OAuth; static non-credential headers must
// not veto the manual-login entry points.
//
// GH #1922: static headers without an oauth block must not veto them either —
// the connection path falls back to OAuth when the upstream rejects the
// headers, and the sign-in it then offers has to be startable.
func TestManualLoginApplicable(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "")
	base := config.ServerConfig{Name: "s", URL: "https://upstream.example/mcp", Protocol: "http",
		Headers: map[string]string{"Authorization": "Bearer static"}}

	noBlock := base
	assert.True(t, ManualLoginApplicable(&noBlock), "static headers do not veto a requested sign-in")

	sse := base
	sse.Protocol = "sse"
	assert.True(t, ManualLoginApplicable(&sse), "nor on SSE")

	withBlock := base
	withBlock.OAuth = &config.OAuthConfig{}
	assert.True(t, ManualLoginApplicable(&withBlock), "an oauth block declares OAuth even with headers")

	noHeaders := config.ServerConfig{Name: "s", URL: "https://upstream.example/mcp", Protocol: "http"}
	assert.True(t, ManualLoginApplicable(&noHeaders))

	stdio := withBlock
	stdio.Protocol = "stdio"
	assert.False(t, ManualLoginApplicable(&stdio), "stdio never uses OAuth")

	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	assert.False(t, ManualLoginApplicable(&withBlock), "the fixture gate still wins")
}
