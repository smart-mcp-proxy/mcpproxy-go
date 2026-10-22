package oauth

import (
	"encoding/json"
	"testing"
	"time"

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

// TestRedactedConfig_LeavesTPARefreshKeysUnmasked (Spec 087 FR-016 / Spec 101
// US3 B1): the four refresh keys are an interval, a flag, a plain https URL and
// a day count - none is a secret. GET /api/v1/config must serve them verbatim
// so an operator can read back what they set, and a mask here would be
// rewritten into config on a write-back. The key names deliberately avoid the
// masked substrings (KEY, AUTH, TOKEN, PASS, CERT, SECRET), which is why the
// URL is tpa_fetch_url and there is no per-fetch key field.
func TestRedactedConfig_LeavesTPARefreshKeysUnmasked(t *testing.T) {
	const fetchURL = "https://api.github.com/repos/smart-mcp-proxy/tpa-db/releases/latest"
	zero := 0
	cfg := config.DefaultConfig()
	cfg.Security = &config.SecurityConfig{
		TPARefreshInterval: config.Duration(12 * time.Hour),
		TPAFetchEnabled:    true,
		TPAFetchURL:        fetchURL,
		TPAStaleAfterDays:  &zero,
	}

	masked := RedactedConfig(cfg)
	require.NotNil(t, masked)
	require.NotNil(t, masked.Security)

	assert.Equal(t, config.Duration(12*time.Hour), masked.Security.TPARefreshInterval)
	assert.True(t, masked.Security.TPAFetchEnabled)
	assert.Equal(t, fetchURL, masked.Security.TPAFetchURL, "a plain https URL is not masked")
	require.NotNil(t, masked.Security.TPAStaleAfterDays)
	assert.Equal(t, 0, *masked.Security.TPAStaleAfterDays, "an explicit 0 survives the walk")

	raw, err := json.Marshal(masked.Security)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	assert.Equal(t, "12h0m0s", wire["tpa_refresh_interval"])
	assert.Equal(t, true, wire["tpa_fetch_enabled"])
	assert.Equal(t, fetchURL, wire["tpa_fetch_url"])
	assert.EqualValues(t, 0, wire["tpa_stale_after_days"])

	assert.Equal(t, fetchURL, cfg.Security.TPAFetchURL, "the source config is not mutated by the redaction walk")
}

// TestRedactedConfig_TPARefreshKeysRoundTripGetThenPatch: the raw-JSON editor
// GETs the config, changes one field and POSTs the whole document back. The
// refresh keys must come back identical, with no mask written over them and no
// absent key turning into an explicit value.
func TestRedactedConfig_TPARefreshKeysRoundTripGetThenPatch(t *testing.T) {
	const fetchURL = "https://mirror.example.com/tpa/scanner-bundle.json"
	zero := 0
	stored := config.DefaultConfig()
	stored.Security = &config.SecurityConfig{
		TPARefreshInterval: config.Duration(6 * time.Hour),
		TPAFetchEnabled:    true,
		TPAFetchURL:        fetchURL,
		TPAStaleAfterDays:  &zero,
	}

	masked := RedactedConfig(stored)
	require.NotNil(t, masked)
	raw, err := json.Marshal(masked)
	require.NoError(t, err)
	var incoming map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &incoming))

	resolved, err := UnmaskLiveConfigDocument(incoming, stored)
	require.NoError(t, err)
	require.NotNil(t, resolved.Security)
	assert.Equal(t, config.Duration(6*time.Hour), resolved.Security.TPARefreshInterval)
	assert.True(t, resolved.Security.TPAFetchEnabled)
	assert.Equal(t, fetchURL, resolved.Security.TPAFetchURL)
	require.NotNil(t, resolved.Security.TPAStaleAfterDays)
	assert.Equal(t, 0, *resolved.Security.TPAStaleAfterDays)

	// A config that never set the keys stays absent through the same trip.
	bare := config.DefaultConfig()
	bare.Security = &config.SecurityConfig{}
	rawBare, err := json.Marshal(RedactedConfig(bare))
	require.NoError(t, err)
	var incomingBare map[string]interface{}
	require.NoError(t, json.Unmarshal(rawBare, &incomingBare))
	resolvedBare, err := UnmaskLiveConfigDocument(incomingBare, bare)
	require.NoError(t, err)
	require.NotNil(t, resolvedBare.Security)
	assert.Zero(t, resolvedBare.Security.TPARefreshInterval)
	assert.False(t, resolvedBare.Security.TPAFetchEnabled)
	assert.Empty(t, resolvedBare.Security.TPAFetchURL)
	assert.Nil(t, resolvedBare.Security.TPAStaleAfterDays)
}
