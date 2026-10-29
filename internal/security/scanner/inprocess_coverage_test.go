package scanner

import (
	"crypto/ed25519"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// benignTools is a toolset no rule of any test bundle matches, so the verdict is
// "clean" and the only thing that can vary is coverage.
func benignTools() []*config.ToolMetadata {
	return []*config.ToolMetadata{{
		ServerName:  "srv",
		Name:        "list_instances",
		Description: "List Cloud SQL instances in the given project. Returns instance names and regions.",
		ParamsJSON:  `{"type":"object"}`,
	}}
}

// requireCoverage asserts what the scan gate sees: the bundle-coverage flag from
// BundleCoverageDegraded and coverageOK from ScanToolMetadataVerdict, which
// must be the exact inverse for a clean toolset.
func requireCoverage(t *testing.T, degraded bool, reason string) {
	t.Helper()
	gotDegraded, gotReason := BundleCoverageDegraded()
	assert.Equal(t, degraded, gotDegraded)
	assert.Equal(t, reason, gotReason)
	info := BundleStatus()
	assert.Equal(t, degraded, info.CoverageDegraded)
	assert.Equal(t, reason, info.DegradedReason)

	verdict, findings, coverageOK := ScanToolMetadataVerdict("srv", benignTools(), nil)
	assert.Equal(t, "clean", verdict, "a degraded bundle never changes the verdict itself; findings=%+v", findings)
	assert.Equal(t, !degraded, coverageOK, "coverageOK must follow the degraded state")
}

// The four conditions of FR-007 / FR-009a / FR-009b, and only those, degrade.
func TestCoverage_DegradingConditions(t *testing.T) {
	t.Run("unsigned external bundle active", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{})
		f.put(unsignedCandidate(t, bundleOpts{Tag: "unsigned"}))
		f.run()
		requireCoverage(t, true, DegradedUnsignedExternalActive)
	})

	t.Run("operator bundle under an unbaselined operator authority", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) { s.TPAActiveRoot = config.TPARootOperator }})
		f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 5}))
		requireCoverage(t, true, DegradedOperatorAuthorityUnbaselined)
	})

	t.Run("embedded default below the publisher watermark", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{EmbeddedSeq: 3})
		f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 10}))
		requireCoverage(t, false, "")
		f.sec.TPABundlePath = ""
		f.run()
		requireCoverage(t, true, DegradedEmbeddedBelowWatermark)
	})

	t.Run("operator root active and its bundle gone", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
			s.TPAActiveRoot = config.TPARootOperator
			s.TPAOperatorBaseline = u64p(1)
		}})
		f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 5}))
		requireCoverage(t, false, "")
		f.sec.TPABundlePath = ""
		f.run()
		requireCoverage(t, true, DegradedOperatorBundleMissing)
	})
}

func TestCoverage_MustNotDegrade(t *testing.T) {
	t.Run("fresh install on the embedded default", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{EmbeddedSeq: 100})
		f.sec.TPABundlePath = ""
		f.run()
		requireCoverage(t, false, "")
		assert.Equal(t, BundleSourceEmbedded, BundleStatus().Source)
	})

	t.Run("embedded at or above the publisher watermark after a binary upgrade", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{EmbeddedSeq: 100})
		f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 120}))

		// A binary upgrade ships a newer embedded corpus (sequence 150). The
		// publisher baseline re-applies on start, so the old external release is
		// now strictly worse than the fallback: it is refused, the embedded
		// default serves, and it sits ABOVE the retained watermark, so it is
		// full coverage rather than degraded.
		withTrustAnchors(t, []ed25519.PublicKey{keyPublisher.pub}, 0, buildBundle(t, bundleOpts{Seq: 150, Tag: "newer-binary"}))
		f.restart()
		info := f.run()
		assert.Equal(t, BundleSourceEmbedded, info.Source)
		requireCoverage(t, false, "")
	})

	t.Run("operator root declared but nothing ever activated", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) { s.TPAActiveRoot = config.TPARootOperator }})
		f.sec.TPABundlePath = ""
		f.run()
		requireCoverage(t, false, "")
	})

	t.Run("operator watermark 900 against an embedded publisher corpus at 100", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{EmbeddedSeq: 100, Sec: func(s *config.SecurityConfig) {
			s.TPAActiveRoot = config.TPARootOperator
			s.TPAOperatorBaseline = u64p(800)
		}})
		f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 900}))
		requireCoverage(t, false, "")

		// Back to the publisher root: the operator bundle is deactivated and the
		// embedded default serves. No sequence is compared across authorities.
		f.sec.TPAActiveRoot = config.TPARootPublisher
		info := f.run()
		require.Equal(t, BundleSourceEmbedded, info.Source)
		requireCoverage(t, false, "")
	})
}

// The degraded state is surfaced even when nothing about the serving bundle
// changed in a given run, and clears when a verified release activates.
func TestCoverage_ClearsWhenASignedReleaseActivates(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.put(unsignedCandidate(t, bundleOpts{Tag: "unsigned"}))
	f.run()
	requireCoverage(t, true, DegradedUnsignedExternalActive)

	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 4}))
	requireCoverage(t, false, "")
}
