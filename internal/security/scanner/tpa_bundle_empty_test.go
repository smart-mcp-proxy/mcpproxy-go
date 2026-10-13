package scanner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// emptyRunnableBundle parses cleanly, passes the version gate, and yields ZERO
// runnable rules. Before the fix it loaded "successfully" into a BundleCheck
// with no rules, which made bundlePresent (and therefore the scan gate's
// coverageOK) true while nothing was actually being matched.
const emptyRunnableBundle = `{"bundle_version":"0.1.0","schema_version":"0.1.0","signature_count":0,"rules":[],"skipped":[]}`

// allNonRunnableBundle carries rules, but none of them run in the offline tier
// (wrong engine / wrong target). Same failure mode as an empty rules array.
const allNonRunnableBundle = `{
  "bundle_version": "0.1.0",
  "schema_version": "0.1.0",
  "signature_count": 2,
  "rules": [
    {"id": "TPA-2099-0002", "detector": "diff_only", "engine": "structural_diff",
     "target": "tool_description", "pattern": "", "category": "rug-pull", "level": "high", "confidence": 0.9},
    {"id": "TPA-2099-0003", "detector": "manifest_only", "engine": "regex",
     "target": "server_manifest", "pattern": "canary", "category": "rug-pull", "level": "high", "confidence": 0.9}
  ],
  "skipped": []
}`

// TestLoadBundleCheck_ZeroRunnableRulesIsALoadError is the P1 fix: a corpus that
// contributes no runnable signature is an EMPTY corpus, not a working one.
// Accepting it silently disabled all TPA coverage while the trust_mode:scan gate
// still reported full coverage and auto-approved rug-pulled tools.
func TestLoadBundleCheck_ZeroRunnableRulesIsALoadError(t *testing.T) {
	t.Run("empty rules array", func(t *testing.T) {
		check, _, err := loadBundleCheck([]byte(emptyRunnableBundle))
		require.Error(t, err, "a bundle with no runnable rules must fail the load")
		assert.Nil(t, check, "no half-live corpus may be handed to the engine")
		assert.Contains(t, err.Error(), "no runnable rules")
	})

	t.Run("all rules non-runnable offline", func(t *testing.T) {
		check, _, err := loadBundleCheck([]byte(allNonRunnableBundle))
		require.Error(t, err, "a bundle whose rules are all non-runnable offline is an empty corpus")
		assert.Nil(t, check)
		assert.Contains(t, err.Error(), "no runnable rules")
	})
}

// TestConfigureBundle_EmptyCorpusKeepsLastKnownGood proves the operator-reachable
// path (security.tpa_bundle_path / MCPPROXY_TPA_BUNDLE_PATH) fails CLOSED on an
// empty corpus: the previously active corpus stays live and the reason is
// surfaced, instead of silently switching the scanner off.
func TestConfigureBundle_EmptyCorpusKeepsLastKnownGood(t *testing.T) {
	restoreEmbeddedBundle(t)
	ConfigureBundle("", zap.NewNop())
	embedded := BundleStatus()
	require.Positive(t, embedded.RunnableRules)

	ConfigureBundle(writeBundle(t, emptyRunnableBundle), zap.NewNop())

	info := BundleStatus()
	assert.Equal(t, BundleSourceEmbedded, info.Source, "an empty configured corpus must not become the live corpus")
	assert.Equal(t, embedded.RunnableRules, info.RunnableRules)
	assert.Contains(t, info.LoadError, "no runnable rules", "the operator must be told why the configured bundle was refused")
	require.NotNil(t, defaultBundleCheck())
}

// TestScanToolMetadataVerdict_NoCorpusFailsCoverage is the gate-level assertion:
// with no live corpus the scan gate must report degraded coverage so a
// trust_mode:scan server is never auto-approved on zero signatures (FR-014).
func TestScanToolMetadataVerdict_NoCorpusFailsCoverage(t *testing.T) {
	restoreEmbeddedBundle(t)
	// Simulate "the only configured corpus was empty and there was no fallback".
	storeBundle(nil, BundleInfo{Source: BundleSourceFile, LoadError: "scanner bundle: no runnable rules"})

	verdict, _, coverageOK := ScanToolMetadataVerdict("srv", []*config.ToolMetadata{
		{Name: "create_issue", Description: "Create an issue"},
	}, nil)

	assert.False(t, coverageOK, "no signatures running means coverage is NOT ok — never auto-approve")
	assert.Equal(t, "clean", verdict)
}
