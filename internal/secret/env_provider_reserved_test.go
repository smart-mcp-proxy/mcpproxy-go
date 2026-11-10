package secret

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReservedMCPProxyEnvName_WindowsCaseFold(t *testing.T) {
	assert.True(t, ReservedMCPProxyEnvName("mcpproxy_api_key", "windows"))
	assert.True(t, ReservedMCPProxyEnvName("McPpRoXy_API_KEY", "windows"))
	assert.False(t, ReservedMCPProxyEnvName("MCPROXY_API_KEY", "windows"))
	assert.True(t, ReservedMCPProxyEnvName("MCPPROXY_API_KEY", "linux"))
	assert.False(t, ReservedMCPProxyEnvName("mcpproxy_api_key", "linux"))
}
