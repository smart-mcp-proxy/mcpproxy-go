package oauth

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// GH #1271: an oauth block declares OAuth; static non-credential headers must
// not veto the manual-login entry points that gate on ShouldUseOAuth.
func TestShouldUseOAuth_OAuthBlockBeatsHeadersVeto(t *testing.T) {
	t.Setenv("MCPPROXY_DISABLE_OAUTH", "")
	base := config.ServerConfig{Name: "s", URL: "https://upstream.example/mcp", Protocol: "http",
		Headers: map[string]string{"X-Goog-User-Project": "p"}}

	noBlock := base
	assert.False(t, ShouldUseOAuth(&noBlock), "headers without an oauth block keep the historical veto")

	withBlock := base
	withBlock.OAuth = &config.OAuthConfig{}
	assert.True(t, ShouldUseOAuth(&withBlock), "an oauth block declares OAuth even with headers")

	stdio := withBlock
	stdio.Protocol = "stdio"
	assert.False(t, ShouldUseOAuth(&stdio), "stdio never uses OAuth")

	t.Setenv("MCPPROXY_DISABLE_OAUTH", "true")
	assert.False(t, ShouldUseOAuth(&withBlock), "the fixture gate still wins")
}
