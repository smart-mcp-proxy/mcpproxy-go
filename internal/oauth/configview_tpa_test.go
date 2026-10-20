package oauth

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TestRedactedConfig_LeavesTPATrustRootUnmasked (Spec 101): the trust-root keys
// are public key material and counters, not secrets. GET /api/v1/config serves
// RedactedConfig, and an operator must be able to read back what they set (and
// the Web UI must be able to round-trip it). A mask here would also be
// rewritten into config on a write-back. Base64 32-byte keys look like
// high-entropy strings, so this pins that the walk does not treat them as
// secrets.
func TestRedactedConfig_LeavesTPATrustRootUnmasked(t *testing.T) {
	signer := "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="
	zero, seven := uint64(0), uint64(7)
	cfg := config.DefaultConfig()
	cfg.Security = &config.SecurityConfig{
		RequireSignedBundle: true,
		TPAActiveRoot:       config.TPARootOperator,
		TPAOperatorSigners:  []string{signer},
		TPAOperatorBaseline: &zero,
		TPAOperatorEpoch:    &seven,
	}

	masked := RedactedConfig(cfg)
	require.NotNil(t, masked)
	require.NotNil(t, masked.Security)

	assert.True(t, masked.Security.RequireSignedBundle)
	assert.Equal(t, config.TPARootOperator, masked.Security.TPAActiveRoot)
	assert.Equal(t, []string{signer}, masked.Security.TPAOperatorSigners)
	require.NotNil(t, masked.Security.TPAOperatorBaseline)
	assert.Equal(t, uint64(0), *masked.Security.TPAOperatorBaseline)
	require.NotNil(t, masked.Security.TPAOperatorEpoch)
	assert.Equal(t, uint64(7), *masked.Security.TPAOperatorEpoch)

	// The wire form is what the REST door serves.
	raw, err := json.Marshal(masked.Security)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	assert.Equal(t, []any{signer}, wire["tpa_operator_signers"])
	assert.Equal(t, "operator", wire["tpa_active_root"])
	assert.EqualValues(t, 0, wire["tpa_operator_baseline"])
	assert.EqualValues(t, 7, wire["tpa_operator_epoch"])
	assert.Equal(t, true, wire["require_signed_bundle"])

	// The source config is not mutated by the redaction walk.
	assert.Equal(t, []string{signer}, cfg.Security.TPAOperatorSigners)
}
