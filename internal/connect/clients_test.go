package connect

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OAuth registration names must not leak into presence matching: the
// registration name and the MCP clientInfo.name are different signals (#1971).
func TestOAuthRegistrationNamesAreNotClientInfoNames(t *testing.T) {
	for _, c := range GetAllClients() {
		for _, reg := range c.OAuthRegistrationNames {
			for _, info := range c.ClientInfoNames {
				if strings.EqualFold(reg, info) {
					t.Fatalf("%s: %q is both a registration name and a clientInfo name", c.ID, reg)
				}
			}
		}
	}
	cp := FindClient("copilot")
	require.NotNil(t, cp)
	assert.NotEmpty(t, cp.OAuthRegistrationNames)
	assert.True(t, cp.PreferredForRegistration)
}
