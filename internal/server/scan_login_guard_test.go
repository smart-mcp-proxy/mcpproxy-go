package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// GH #1722 cause 2: the scanner asks the server layer whether a server is
// waiting on a person's OAuth login before it reconnects (restarts) it for the
// tool export. A manual login in flight is one such wait.
func TestConfigServerInfoProvider_AwaitingOAuthLogin_ManualFlow(t *testing.T) {
	s := newInformationalTestServer(t, newFakeSecurityScanner(), nil, enabledServer("srv", config.TrustModeManual))
	p := &configServerInfoProvider{server: s}

	assert.False(t, p.AwaitingOAuthLogin("srv"), "no login, no wait")

	release := oauth.BeginManualFlow("srv", time.Minute)
	defer release()
	assert.True(t, p.AwaitingOAuthLogin("srv"), "a manual OAuth login in flight blocks scan restarts")

	release()
	assert.False(t, p.AwaitingOAuthLogin("srv"), "released login: restarts allowed again")
}
