package scanner

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// logOfferedRefused is the message of the one log line an offered (fetch)
// refusal emits.
const logOfferedRefused = "fetched TPA scanner bundle refused"

// T320: Hazard A. With no tpa_bundle_path, a fetched bundle must survive config
// re-applies, restarts and a fetch-disable, because the three external-source
// guards (canSkipLocked, fallbackLocked, restorePinLocked) consult
// externalConfigured rather than the path alone (tasks B9).
func TestRefreshRuntime_FetchedBundleSurvivesReloadsAndRestarts(t *testing.T) {
	f := newFetchFixture(t, nil)
	obs := recordActivations(t)
	require.Equal(t, BundleSourceEmbedded, f.run().Source, "the embedded default serves before anything is fetched")

	c := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "fetched", GeneratedAt: "2026-10-20T00:00:00Z"})
	digest := digestOf(c.Bundle)

	info := runOffered(c, f.log)
	require.Equal(t, digest, info.FullDigest, "the offered operator-signed candidate must activate; load_error=%q", info.LoadError)
	require.Equal(t, BundleSourceFetch, info.Source)
	require.Len(t, f.store.historyRows(), 1, "one activation, one history row")
	require.Equal(t, BundleSourceFetch, f.store.historyRows()[0].Source)
	require.Equal(t, 1, obs.count(), "the activation observer fires once")
	activeBefore := *f.store.persisted(t).Active
	lkgBefore := f.lkgFiles()
	require.Len(t, lkgBefore, 2)

	t.Run("(a) a config re-apply keeps it serving", func(t *testing.T) {
		info := f.run()
		assert.Equal(t, digest, info.FullDigest)
		assert.Equal(t, BundleSourceFetch, info.Source)
		active := f.store.persisted(t).Active
		require.NotNil(t, active, "the Active record must not be cleared")
		assert.Equal(t, activeBefore, *active)
		assert.ElementsMatch(t, lkgBefore, f.lkgFiles(), "reconcile must keep the last-known-good files")
		assert.Len(t, f.store.historyRows(), 1)
		assert.Empty(t, f.logMessages(zapcore.WarnLevel), "no deactivation, no fallback")
	})

	t.Run("(b) a restart restores it from its last-known-good files", func(t *testing.T) {
		f.restart()
		info := f.run()
		assert.Equal(t, digest, info.FullDigest)
		assert.Equal(t, BundleSourceFetch, info.Source)
		assert.True(t, info.SignatureVerified)
		assert.ElementsMatch(t, lkgBefore, f.lkgFiles())
		assert.Len(t, f.store.historyRows(), 1, "a restoration is not a new activation")
		assert.Equal(t, 1, obs.count(), "an LKG restore never fires the activation observer")
	})

	t.Run("(c) disabling the fetch keeps the last verified bundle serving", func(t *testing.T) {
		f.sec.TPAFetchEnabled = false
		info := f.run()
		assert.Equal(t, digest, info.FullDigest, "dropping to the weaker embedded corpus would be an unrequested degrade")
		assert.Equal(t, BundleSourceFetch, info.Source)
		assert.ElementsMatch(t, lkgBefore, f.lkgFiles())
		f.sec.TPAFetchEnabled = true
	})

	t.Run("(d) rollback --embedded, then the rolled-away digest is refused quietly", func(t *testing.T) {
		info, err := RollbackBundle(BundleRollbackRequest{Embedded: true, Digest: embeddedDigest()}, f.log)
		require.NoError(t, err)
		require.Equal(t, BundleSourceEmbedded, info.Source)
		fired := obs.count() // the rollback row is a history row and fires once
		require.Equal(t, 2, fired)

		info = runOffered(c, f.log)
		assert.Equal(t, BundleSourceEmbedded, info.Source, "no reactivation of a deny-listed release")
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectDenyListed, info.LastRejection.Reason)
		assert.Equal(t, 1, f.countLogs(zapcore.WarnLevel, logOfferedRefused))

		n := countCompiles(t)
		for i := 0; i < 3; i++ {
			info = runOffered(c, f.log)
			assert.Equal(t, BundleSourceEmbedded, info.Source)
		}
		assert.Zero(t, *n, "a refused pair offered again costs no compile")
		assert.Equal(t, 1, f.countLogs(zapcore.WarnLevel, logOfferedRefused), "one log line in total")
		assert.Equal(t, fired, obs.count(), "no observer fire for a quiet refusal")
		assert.Len(t, f.store.historyRows(), 2)
	})
}

// T321: the offered-run semantics.
func TestRefreshRuntime_OfferedRunSemantics(t *testing.T) {
	t.Run("a refusal never reads the file and never sets load_error", func(t *testing.T) {
		f := newFetchFixture(t, nil)
		bad := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "bad"})
		bad.Bundle = flipByte(t, bad.Bundle, "bundle-marker")

		info := runOffered(bad, f.log)

		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectTamper, info.LastRejection.Reason)
		assert.Empty(t, info.LoadError, "load_error belongs to the file leg")
		assert.Equal(t, BundleSourceEmbedded, info.Source)
		assert.Equal(t, 1, f.countLogs(zapcore.WarnLevel, logOfferedRefused))
		runOffered(bad, f.log)
		assert.Equal(t, 1, f.countLogs(zapcore.WarnLevel, logOfferedRefused), "recorded once")
	})

	t.Run("an offered run never reads the configured file", func(t *testing.T) {
		f := newFetchFixture(t, func(s *config.SecurityConfig) { s.TPABundlePath = "" })
		// A dropped file that would activate if anything read it; the policy has
		// no path, and the offered run must not look anyway.
		f.put(signedCandidate(t, keyOperator, bundleOpts{Seq: 9, Tag: "dropped"}))
		bad := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "bad"})
		bad.Bundle = flipByte(t, bad.Bundle, "bundle-marker")
		info := runOffered(bad, f.log)
		assert.Equal(t, BundleSourceEmbedded, info.Source)
		assert.Len(t, f.store.historyRows(), 0)
	})

	t.Run("a file-leg load_error is preserved by an offered run", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
			s.TPAActiveRoot = config.TPARootOperator
			s.TPAOperatorBaseline = u64p(1)
			s.TPAFetchEnabled = true
			s.TPAFetchURL = fetchChannelURL
		}})
		f.put(signedCandidate(t, keyOperator, bundleOpts{Seq: 0, Tag: "unusable"})) // no sequence: invalid
		info := f.run()
		require.NotEmpty(t, info.LoadError)
		fileErr := info.LoadError

		info = runOffered(fetchedCandidate(t, keyOperator, bundleOpts{Seq: 3, Tag: "ok"}), f.log)

		assert.Equal(t, BundleSourceFetch, info.Source)
		assert.Equal(t, fileErr, info.LoadError, "an offered run must not clear or replace the file leg's load_error")
	})

	t.Run("direct-mode unsigned is refused as signature_required without require_signed_bundle", func(t *testing.T) {
		f := newFetchFixture(t, nil)
		require.False(t, f.sec.RequireSignedBundle)
		c := Candidate{Bundle: buildBundle(t, bundleOpts{Seq: 3, Tag: "unsigned"}), Source: BundleSourceFetch, Path: "https://mirror.test"}

		info := runOffered(c, f.log)

		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectSignatureRequired, info.LastRejection.Reason)
		assert.Equal(t, BundleSourceEmbedded, info.Source)
	})

	t.Run("a refused fetch leaves a serving unsigned file-drop bundle serving", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
			s.TPAFetchEnabled = true
			s.TPAFetchURL = fetchChannelURL
		}})
		unsigned := unsignedCandidate(t, bundleOpts{Tag: "dropped"})
		f.activate(unsigned)
		prev := servingCheck()
		require.False(t, f.sec.RequireSignedBundle)

		info := runOffered(Candidate{Bundle: buildBundle(t, bundleOpts{Seq: 3, Tag: "unsigned-fetch"}), Source: BundleSourceFetch, Path: "https://mirror.test"}, f.log)

		assert.Equal(t, digestOf(unsigned.Bundle), info.FullDigest, "the unsigned file bundle must keep serving")
		assert.Same(t, prev, servingCheck())
		assert.Equal(t, BundleSourceFile, info.Source)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectSignatureRequired, info.LastRejection.Reason)
		for _, m := range f.logMessages(zapcore.WarnLevel) {
			assert.NotContains(t, m, "deactivated")
		}
	})

	t.Run("over-limit bodies give ceiling_exceeded", func(t *testing.T) {
		f := newFetchFixture(t, nil)
		sc := signBundle(t, keyOperator.priv, []byte("x"))

		big := Candidate{Bundle: make([]byte, MaxBundleBytes+1), Sidecar: sc, SidecarPresent: true, Source: BundleSourceFetch, Path: "https://mirror.test"}
		info := runOffered(big, f.log)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectCeilingExceeded, info.LastRejection.Reason)

		okBundle := buildBundle(t, bundleOpts{Seq: 3, Tag: "big-sidecar", KeyID: keyOperator.id})
		bigSig := Candidate{Bundle: okBundle, Sidecar: make([]byte, MaxSidecarBytes+1), SidecarPresent: true, Source: BundleSourceFetch, Path: "https://mirror.test"}
		info = runOffered(bigSig, f.log)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectCeilingExceeded, info.LastRejection.Reason)
		assert.Contains(t, info.LastRejection.Detail, "sidecar")
	})

	t.Run("an offered candidate equal to the active bytes is unchanged with zero compiles", func(t *testing.T) {
		f := newFetchFixture(t, nil)
		c := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "same"})
		first := runOffered(c, f.log)
		require.Equal(t, digestOf(c.Bundle), first.FullDigest)
		rows, commits := len(f.store.historyRows()), f.store.commitCount()
		n := countCompiles(t)

		_, res := offerCandidate(c, f.log)

		assert.Equal(t, RefreshOutcomeUnchanged, res.Outcome)
		assert.Zero(t, *n)
		assert.Len(t, f.store.historyRows(), rows)
		assert.Equal(t, commits, f.store.commitCount())
	})

	t.Run("a refused pair offered again is unchanged with zero compiles and no second log", func(t *testing.T) {
		f := newFetchFixture(t, nil)
		withSelfCheck(t, func(*BundleCheck) error { return errors.New("self-check said no") })
		c := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "gated"})
		n := countCompiles(t)

		_, res := offerCandidate(c, f.log)
		require.Equal(t, RefreshOutcomeRejected, res.Outcome)
		require.NotNil(t, res.Rejection)
		assert.Equal(t, RejectGateFailure, res.Rejection.Reason)
		compiled := *n
		require.Positive(t, compiled)

		_, res = offerCandidate(c, f.log)

		assert.Equal(t, RefreshOutcomeUnchanged, res.Outcome)
		assert.Equal(t, compiled, *n, "the negative cache spares the compile and the self-check")
		assert.Equal(t, 1, f.countLogs(zapcore.WarnLevel, logOfferedRefused))
	})

	t.Run("a policy change re-evaluates a previously refused pair", func(t *testing.T) {
		f := newFetchFixture(t, func(s *config.SecurityConfig) { s.TPAOperatorSigners = nil })
		c := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "late-key"})
		_, res := offerCandidate(c, f.log)
		require.Equal(t, RefreshOutcomeRejected, res.Outcome)
		assert.Equal(t, RejectKeyNotTrusted, res.Rejection.Reason)

		f.sec.TPAOperatorSigners = []string{keyOperator.b64}
		f.run()
		_, res = offerCandidate(c, f.log)

		assert.Equal(t, RefreshOutcomeActivated, res.Outcome, "the key is trusted now; the negative cache must not hide the pair")
	})

	t.Run("fetch active plus a stale file: the file leg logs one downgrade and fetch keeps serving", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
			s.TPAActiveRoot = config.TPARootOperator
			s.TPAOperatorBaseline = u64p(1)
			s.TPAFetchEnabled = true
			s.TPAFetchURL = fetchChannelURL
		}})
		f.run()
		fetched := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "fetched"})
		require.Equal(t, digestOf(fetched.Bundle), runOffered(fetched, f.log).FullDigest)

		f.put(signedCandidate(t, keyOperator, bundleOpts{Seq: 3, Tag: "stale-file"}))
		info := f.run()
		f.run()

		assert.Equal(t, digestOf(fetched.Bundle), info.FullDigest, "the fetched bundle keeps serving")
		assert.Equal(t, BundleSourceFetch, info.Source)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectDowngrade, info.LastRejection.Reason)
		assert.Contains(t, info.LoadError, RejectDowngrade, "the stale file is reported as the file leg's load_error")
		downgrades := 0
		for _, e := range f.logs.All() {
			if e.Level == zapcore.ErrorLevel && e.ContextMap()["reason"] == RejectDowngrade {
				downgrades++
			}
		}
		assert.Equal(t, 1, downgrades, "the stale file is refused once, deduplicated")
	})

	t.Run("equal sequence with a different digest is a downgrade on the fetch leg", func(t *testing.T) {
		f := newFetchFixture(t, nil)
		a := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "A"})
		b := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "B"})
		require.Equal(t, digestOf(a.Bundle), runOffered(a, f.log).FullDigest)

		info, res := offerCandidate(b, f.log)

		assert.Equal(t, RefreshOutcomeRejected, res.Outcome)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectDowngrade, info.LastRejection.Reason)
		assert.Equal(t, digestOf(a.Bundle), info.FullDigest)
	})

	t.Run("a byte-identical fetched pair while the file copy is active changes nothing", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
			s.TPAActiveRoot = config.TPARootOperator
			s.TPAOperatorBaseline = u64p(1)
			s.TPAFetchEnabled = true
			s.TPAFetchURL = fetchChannelURL
		}})
		obs := recordActivations(t)
		file := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "same"})
		f.activate(file)
		require.Equal(t, 1, obs.count())
		rows := len(f.store.historyRows())

		fetched := file
		fetched.Source, fetched.Path = BundleSourceFetch, "https://mirror.test"
		info, res := offerCandidate(fetched, f.log)

		assert.Equal(t, RefreshOutcomeUnchanged, res.Outcome)
		assert.Equal(t, BundleSourceFile, info.Source, "the source stays file")
		assert.Len(t, f.store.historyRows(), rows)
		assert.Equal(t, 1, obs.count(), "no observer fire")
	})

	t.Run("pinned after rollback, a strictly newer release activates and clears the pin", func(t *testing.T) {
		f := newFetchFixture(t, nil)
		two := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "two"})
		require.Equal(t, digestOf(two.Bundle), runOffered(two, f.log).FullDigest)
		info, err := RollbackBundle(BundleRollbackRequest{Embedded: true, Digest: embeddedDigest()}, f.log)
		require.NoError(t, err)
		require.True(t, info.Pinned)

		three := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 3, Tag: "three"})
		info = runOffered(three, f.log)

		assert.Equal(t, digestOf(three.Bundle), info.FullDigest)
		assert.False(t, info.Pinned, "a genuinely newer release clears the pin (US1 A4)")
		assert.Nil(t, f.store.persisted(t).Authorities[AuthorityOperator].Pin)
	})

	t.Run("an operator fetch without a baseline activates degraded", func(t *testing.T) {
		f := newFetchFixture(t, func(s *config.SecurityConfig) { s.TPAOperatorBaseline = nil })
		c := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "unbaselined"})

		info := runOffered(c, f.log)

		assert.Equal(t, digestOf(c.Bundle), info.FullDigest)
		assert.True(t, info.CoverageDegraded)
		assert.Equal(t, DegradedOperatorAuthorityUnbaselined, info.DegradedReason)
	})

	t.Run("source is fetch and the path is host-only", func(t *testing.T) {
		f := newFetchFixture(t, nil)
		info := runOffered(fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2}), f.log)
		assert.Equal(t, BundleSourceFetch, info.Source)
		assert.Equal(t, "https://mirror.test", info.Path)
		require.NotNil(t, info.LastActivatedAt)
	})
}

// US3 AS1: the same signed pair gives the same activation tuple whether it
// arrives as a file drop or as an offered fetch.
func TestRefreshRuntime_FetchAndFileDropAreEquivalent(t *testing.T) {
	type tuple struct {
		Digest, Sequence, Authority string
		Verified, Degraded          bool
		Reason                      string
		Epoch                       uint64
	}
	tupleOf := func(i BundleInfo) tuple {
		return tuple{i.FullDigest, i.Sequence, i.Authority, i.SignatureVerified, i.CoverageDegraded, i.DegradedReason, i.TrustEpoch}
	}
	c := signedCandidate(t, keyOperator, bundleOpts{Seq: 4, Tag: "parity", GeneratedAt: "2026-10-20T00:00:00Z"})

	viaFile := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(1)
	}})
	fileInfo := viaFile.activate(c)

	viaFetch := newFetchFixture(t, nil)
	offered := c
	offered.Source, offered.Path = BundleSourceFetch, "https://mirror.test"
	fetchInfo := runOffered(offered, viaFetch.log)

	require.Equal(t, digestOf(c.Bundle), fetchInfo.FullDigest)
	assert.Equal(t, tupleOf(fileInfo), tupleOf(fetchInfo))
	assert.Equal(t, BundleSourceFile, fileInfo.Source)
	assert.Equal(t, BundleSourceFetch, fetchInfo.Source)
}

// B9: the fetch settings are part of the policy fingerprint; the interval and
// the stale threshold are not.
func TestRefreshRuntime_PolicyFingerprintCoversFetchSettings(t *testing.T) {
	base := func() *config.SecurityConfig {
		return &config.SecurityConfig{TPAOperatorSigners: []string{keyOperator.b64}, TPAFetchEnabled: true, TPAFetchURL: fetchChannelURL}
	}
	fp := func(s *config.SecurityConfig) string { return policyFromConfig(s).Fingerprint }

	ref := fp(base())
	off := base()
	off.TPAFetchEnabled = false
	assert.NotEqual(t, ref, fp(off), "FetchEnabled")
	other := base()
	other.TPAFetchURL = "https://other.test/repos/a/b/releases/latest"
	assert.NotEqual(t, ref, fp(other), "FetchURL")

	interval := base()
	interval.TPARefreshInterval = config.Duration(2 * time.Hour)
	assert.Equal(t, ref, fp(interval), "the refresh interval is not policy")
	stale := base()
	stale.TPAStaleAfterDays = intp(7)
	assert.Equal(t, ref, fp(stale), "the stale threshold is not policy")
}

// Guard for the file-drop path: a configured file still works exactly as
// before with the new guards in place.
func TestRefreshRuntime_FileDropUnaffectedByExternalConfigured(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "file"})
	f.activate(c)
	f.remove()
	info := f.run()
	assert.Equal(t, BundleSourceFile, info.Source, "a vanished file keeps the last-known-good serving while the path is configured")

	f.sec.TPABundlePath = ""
	info = f.run()
	assert.Equal(t, BundleSourceEmbedded, info.Source, "removing the path drops a FILE bundle (only fetched bundles outlive the path)")
	assert.Nil(t, f.store.persisted(t).Active)
}

// US3 judge finding 10 (B9): the grace that keeps a bundle alive when its
// source setting disappears belongs to FETCHED bundles. A FILE bundle whose
// tpa_bundle_path was removed is gone with the path, whether or not fetch is
// switched on, on this run and on every restart.
func TestRefreshRuntime_RemovingTheFilePathDropsAFileBundleEvenWithFetchOn(t *testing.T) {
	f := newFileAndFetchFixture(t, nil, true)
	c := signedCandidate(t, keyOperator, bundleOpts{Seq: 5, Tag: "file"})
	info := f.activate(c)
	require.Equal(t, BundleSourceFile, info.Source)

	f.sec.TPABundlePath = ""
	info = f.run()
	assert.Equal(t, BundleSourceEmbedded, info.Source, "the operator removed the file bundle: the embedded corpus serves")

	f.restart()
	info = f.run()
	assert.Equal(t, BundleSourceEmbedded, info.Source, "and a restart does not restore it from the last-known-good files")
}
