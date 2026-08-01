package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpstreamAddTrustModeFlag is GH #938 finding 3: `mcpproxy upstream add`
// had no --trust-mode flag, so a server could not be added directly into scan
// mode from the CLI — the mode could only be set by pre-seeding mcp_config.json
// or by a follow-up REST PATCH.
func TestUpstreamAddTrustModeFlag(t *testing.T) {
	flag := upstreamAddCmd.Flags().Lookup("trust-mode")
	require.NotNil(t, flag, "upstream add must expose --trust-mode")
	assert.Equal(t, "", flag.DefValue, "unset means inherit the migrated default")
	assert.Contains(t, flag.Usage, "scan")
}

// TestValidateTrustModeFlag pins the CLI-side validation: a typo is refused up
// front with the accepted vocabulary, matching the REST 400 (finding 1).
func TestValidateTrustModeFlag(t *testing.T) {
	for _, valid := range []string{"", "auto", "scan", "manual"} {
		assert.NoError(t, validateTrustModeFlag(valid), "trust-mode %q must be accepted", valid)
	}
	for _, invalid := range []string{"yolo", "Scan", "off"} {
		err := validateTrustModeFlag(invalid)
		require.Error(t, err, "trust-mode %q must be refused", invalid)
		assert.Contains(t, err.Error(), "auto, scan, manual")
	}
}
