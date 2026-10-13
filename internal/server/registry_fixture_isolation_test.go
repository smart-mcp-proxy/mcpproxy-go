package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// TestRegistryFixturesRestoreSSRFPolicy verifies that the registry test fixtures
// restore the SSRF allow-policy (MCP-1076) they opt out of.
//
// The policy is process-global in internal/registries. A fixture that leaves it
// disabled suppresses every later SSRF assertion in this test binary, including
// the five in TestBuildRegistrySourceEntry_RejectsSSRFLiteralIP.
//
// Each case resets the policy to its default before running one fixture in a
// nested subtest, so the result does not depend on test order. t.Run returns
// only after the subtest's cleanups have run, so the assertion below observes
// the restored value rather than the fixture's.
func TestRegistryFixturesRestoreSSRFPolicy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T)
	}{
		{"startTestRegistry", func(t *testing.T) { startTestRegistry(t, nil) }},
		{"startOfficialTestRegistry", startOfficialTestRegistry},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A nil config yields the default catalog and an enabled guard.
			registries.SetRegistriesFromConfig(nil)
			t.Cleanup(func() { registries.SetRegistriesFromConfig(nil) })

			t.Run("fixture", tc.setup)

			_, err := buildRegistrySourceEntry("https://169.254.169.254/v0.1/servers", "", "", "")
			require.Errorf(t, err, "%s did not restore the SSRF allow-policy: the cloud-metadata endpoint was accepted", tc.name)
			assert.ErrorIs(t, err, ErrInvalidRegistryURL)
		})
	}
}
