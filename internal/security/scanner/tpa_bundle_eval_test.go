package scanner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestLoadBundleCheck_MatchesBundleStatusForEmbedded(t *testing.T) {
	restoreEmbeddedBundle(t)
	ConfigureBundle("", zap.NewNop())
	status := BundleStatus()

	check, info, err := LoadBundleCheck(EmbeddedBundle())
	require.NoError(t, err)
	require.NotNil(t, check)
	assert.Equal(t, status.RunnableRules, info.RunnableRules)
	assert.Equal(t, status.SkippedRules, info.SkippedRules)
	assert.Equal(t, status.SignatureCount, info.SignatureCount)
	assert.Equal(t, status.Fingerprint, info.Fingerprint)
	assert.Equal(t, status.BundleVersion, info.BundleVersion)
}

func TestLoadBundleCheck_IsPure(t *testing.T) {
	restoreEmbeddedBundle(t)
	ConfigureBundle("", zap.NewNop())
	before := BundleStatus()
	beforeCheck := defaultBundleCheck()

	// Bad bytes: an error and no state change.
	check, _, err := LoadBundleCheck([]byte(`{"bundle_version":"9.9.0"}`))
	require.Error(t, err)
	assert.Nil(t, check)
	assert.Equal(t, before, BundleStatus())

	// Good, different bytes: still no state change.
	check, info, err := LoadBundleCheck([]byte(miniBundle))
	require.NoError(t, err)
	require.NotNil(t, check)
	assert.Equal(t, 1, info.RunnableRules)
	assert.Equal(t, before, BundleStatus(), "LoadBundleCheck must not install the bundle")
	assert.Same(t, beforeCheck, defaultBundleCheck())
}

func TestEmbeddedBundle_ReturnsCopy(t *testing.T) {
	a := EmbeddedBundle()
	require.NotEmpty(t, a)
	orig := a[0]
	a[0] ^= 0xff
	b := EmbeddedBundle()
	assert.Equal(t, orig, b[0], "mutating the result must not change the embed")
	assert.Equal(t, orig, bundledScannerBundle[0])
}

func TestParseBundleSignatures_RoundTripsRuleIDs(t *testing.T) {
	sigs, err := ParseBundleSignatures(mustJSON(t, sigBundle()))
	require.NoError(t, err)
	require.Len(t, sigs, 2)
	assert.Equal(t, "TPA-2026-0001", sigs[0].ID)
	assert.Equal(t, "tpa_hidden_instruction_block", sigs[0].Category)
	assert.True(t, sigs[0].Gating)
	assert.Equal(t, "https://example.test/disclosure", sigs[0].Provenance.Source)
	assert.Equal(t, "2026-01-14", sigs[0].Provenance.Published)
	assert.Equal(t, "MIT", sigs[0].License)
	assert.Equal(t, []string{"TPA-2026-0001/hidden_block", "TPA-2026-0001/rug_diff"}, sigs[0].RuleIDs)
	assert.False(t, sigs[1].Gating)

	empty, err := ParseBundleSignatures([]byte(miniBundle))
	require.NoError(t, err)
	assert.Empty(t, empty, "a v0.1 bundle has no signatures")

	_, err = ParseBundleSignatures([]byte(`{`))
	require.Error(t, err)
}
