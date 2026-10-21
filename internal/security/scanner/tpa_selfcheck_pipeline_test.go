package scanner

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Activation self-check through the real pipeline (tasks T342, B13). The
// fetch-sourced catch-all case is in T391 (it needs the fetch adapter).

// selfCheckPatternBundle renders a one-rule bundle with an arbitrary regex, so a
// fixture can model a release whose rule is too broad.
func selfCheckPatternBundle(t *testing.T, pattern, tag string) []byte {
	t.Helper()
	// buildBundle gives the right envelope; swap its marker rule for ours.
	base := string(buildBundle(t, bundleOpts{Tag: tag}))
	marker := "bundle-marker-0-" + tag
	require.Contains(t, base, marker)
	return []byte(strings.Replace(base, marker, pattern, 1))
}

// enableSelfCheckForTest installs the real hook, counts invocations, and
// restores the previous hook on cleanup.
func enableSelfCheckForTest(t *testing.T) *atomic.Int64 {
	t.Helper()
	prev := bundleSelfCheck
	t.Cleanup(func() { bundleSelfCheck = prev })
	EnableActivationSelfCheck()
	require.NotNil(t, bundleSelfCheck, "EnableActivationSelfCheck must install the hook")
	real := bundleSelfCheck
	var n atomic.Int64
	bundleSelfCheck = func(c *BundleCheck) error {
		n.Add(1)
		return real(c)
	}
	return &n
}

func TestSelfCheckPipelineRefusesCatchAllFileDrop(t *testing.T) {
	realEmbedded := append([]byte(nil), bundledScannerBundle...)
	f := newRTFixture(t, rtOpts{})
	count := enableSelfCheckForTest(t)

	// Baseline: the embedded default serves.
	base := f.run()
	require.Equal(t, BundleSourceEmbedded, base.Source)
	prevCheck := servingCheck()

	// A signature file with a catch-all rule: it compiles, it is well formed,
	// and it would flag every tool the proxy ever sees.
	require.NoError(t, writeFileForTest(f.path, selfCheckPatternBundle(t, `(?s).+`, "catchall")))
	info := f.run()

	f.requireRejected(info, RejectGateFailure, base.FullDigest, prevCheck)
	assert.Contains(t, info.LastRejection.Detail, "21/21")
	assert.Equal(t, BundleSourceEmbedded, info.Source, "the embedded default keeps serving")
	assert.EqualValues(t, 1, count.Load(), "exactly one self-check for the one distinct candidate")

	t.Run("re-running over the same bytes does not re-score", func(t *testing.T) {
		info := f.run()
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectGateFailure, info.LastRejection.Reason)
		assert.LessOrEqual(t, count.Load(), int64(1), "at most one self-check per distinct candidate")
	})

	t.Run("a second distinct candidate costs one more", func(t *testing.T) {
		require.NoError(t, writeFileForTest(f.path, selfCheckPatternBundle(t, `(?s).+`, "catchall-two")))
		info := f.run()
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectGateFailure, info.LastRejection.Reason)
		assert.EqualValues(t, 2, count.Load())
	})

	t.Run("the shipped default written as a file activates", func(t *testing.T) {
		require.NoError(t, writeFileForTest(f.path, realEmbedded))
		info := f.run()
		assert.Equal(t, BundleSourceFile, info.Source, "load_error=%q", info.LoadError)
		assert.Equal(t, digestOf(realEmbedded), info.FullDigest)
		assert.Empty(t, info.LoadError)
		before := count.Load()
		_ = f.run()
		assert.Equal(t, before, count.Load(), "an idempotent rerun of the serving bundle does not re-score")
	})
}

// TestSelfCheckPipelineNarrowRuleActivates: a release that adds a narrow,
// fixture-specific signature is not collateral damage of the gate.
func TestSelfCheckPipelineNarrowRuleActivates(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	enableSelfCheckForTest(t)
	require.NoError(t, writeFileForTest(f.path, selfCheckPatternBundle(t, `ZZZ-NARROW-FIXTURE-MARKER`, "narrow")))
	info := f.run()
	assert.Equal(t, BundleSourceFile, info.Source, "load_error=%q", info.LoadError)
	assert.Empty(t, info.LoadError)
}
