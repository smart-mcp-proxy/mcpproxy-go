package callerr

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
)

// Release QA F1: a call that could not be made because the server waits for
// a login is an authentication failure, not an unreachable server; the call
// was still not made, so its audit class stays upstream_unavailable.
func TestNotConnectedOutcome(t *testing.T) {
	login := NotConnectedOutcome(true)
	assert.Equal(t, ClassAuth, login.Class)
	assert.Equal(t, DomainUpstream, login.Domain)
	assert.Equal(t, audit.ErrorClassUpstreamUnavailable, login.AuditClass())

	offline := NotConnectedOutcome(false)
	assert.Equal(t, UnavailableOutcome(), offline)
	assert.Equal(t, ClassNetwork, offline.Class)
}
