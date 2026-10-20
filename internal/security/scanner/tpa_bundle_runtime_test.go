package scanner

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// --- Spec 101 US1 Independent Test, end to end through ConfigureBundleFromConfig ---

func TestRuntime_SignedBundleActivates(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, GeneratedAt: "2026-10-01T00:00:00Z"})
	f.put(c)

	info := f.run()

	assert.Equal(t, BundleSourceFile, info.Source)
	assert.Equal(t, f.path, info.Path)
	assert.True(t, info.SignatureVerified)
	assert.Equal(t, "5", info.Sequence)
	assert.Equal(t, AuthorityPublisher, info.Authority)
	assert.Equal(t, uint64(0), info.TrustEpoch)
	assert.Equal(t, digestOf(c.Bundle), info.FullDigest)
	assert.Equal(t, digestOf(c.Bundle)[:12], info.Fingerprint)
	assert.Equal(t, "2026-10-01T00:00:00Z", info.GeneratedAt)
	assert.Equal(t, 1, info.RunnableRules)
	assert.False(t, info.CoverageDegraded, "a verified publisher bundle is full coverage")
	assert.Empty(t, info.LoadError)
	assert.Nil(t, info.LastRejection)
	assert.False(t, info.Pinned)
	assert.False(t, info.Rollback)

	st := f.store.persisted(t)
	require.NotNil(t, st)
	assert.True(t, st.SignatureRatchet)
	require.NotNil(t, st.Active)
	assert.Equal(t, digestOf(c.Bundle), st.Active.FullDigest)
	assert.Equal(t, uint64(5), st.Authorities[AuthorityPublisher].Watermark)
	assert.Equal(t, digestOf(c.Bundle), st.Authorities[AuthorityPublisher].WatermarkSetBy)

	rows := f.store.historyRows()
	require.Len(t, rows, 1, "one activation, one history row")
	assert.Equal(t, "5", rows[0].Sequence)
	assert.True(t, rows[0].SignatureVerified)
	assert.Equal(t, BundleSourceFile, rows[0].Source)
	assert.Equal(t, 1, f.store.commitCount())

	assert.ElementsMatch(t, []string{digestOf(c.Bundle) + ".json", digestOf(c.Bundle) + ".json.sig"}, f.lkgFiles(),
		"the activated release is durable in the content-addressed LKG store")
}

func TestRuntime_US1_RejectionsKeepLastKnownGood(t *testing.T) {
	// base activates a publisher release at sequence 10 and returns its identity.
	base := func(t *testing.T, o rtOpts) (*rtFixture, BundleInfo, *BundleCheck) {
		f := newRTFixture(t, o)
		info := f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 10, Tag: "base"}))
		return f, info, servingCheck()
	}

	t.Run("(a) one flipped byte", func(t *testing.T) {
		f, prev, chk := base(t, rtOpts{})
		good := signedCandidate(t, keyPublisher, bundleOpts{Seq: 11, Tag: "good"})
		n := countCompiles(t)
		f.put(Candidate{Bundle: flipByte(t, good.Bundle, "bundle-marker"), Sidecar: good.Sidecar, SidecarPresent: true})
		info := f.run()
		f.requireRejected(info, RejectTamper, prev.FullDigest, chk)
		assert.Zero(t, *n, "a tampered candidate is refused before any pattern is compiled")
	})

	t.Run("(b) signature from a non-trusted key", func(t *testing.T) {
		f, prev, chk := base(t, rtOpts{})
		f.put(signedCandidate(t, keyUntrusted, bundleOpts{Seq: 11}))
		info := f.run()
		f.requireRejected(info, RejectKeyNotTrusted, prev.FullDigest, chk)
		assert.Contains(t, info.LastRejection.Detail, keyUntrusted.id)
	})

	t.Run("(c) missing sidecar with require_signed_bundle", func(t *testing.T) {
		f, prev, chk := base(t, rtOpts{Sec: func(s *config.SecurityConfig) { s.RequireSignedBundle = true }})
		f.put(unsignedCandidate(t, bundleOpts{Seq: 11}))
		info := f.run()
		f.requireRejected(info, RejectSignatureRequired, prev.FullDigest, chk)
	})

	t.Run("(c) the same on a fresh install keeps the embedded default serving", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) { s.RequireSignedBundle = true }})
		f.put(unsignedCandidate(t, bundleOpts{Seq: 11}))
		info := f.run()
		assert.Equal(t, BundleSourceEmbedded, info.Source)
		assert.Equal(t, embeddedDigest(), info.FullDigest)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectSignatureRequired, info.LastRejection.Reason)
		assert.False(t, info.CoverageDegraded, "refusing a candidate does not degrade the fresh-install embedded default")
	})

	t.Run("(d) lower sequence than the active bundle", func(t *testing.T) {
		f, prev, chk := base(t, rtOpts{})
		f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 9, Tag: "older"}))
		info := f.run()
		f.requireRejected(info, RejectDowngrade, prev.FullDigest, chk)
		assert.Contains(t, info.LastRejection.Detail, "sequence 9")
		assert.Contains(t, info.LastRejection.Detail, "watermark 10")
		assert.Equal(t, uint64(10), f.store.persisted(t).Authorities[AuthorityPublisher].Watermark, "a refusal never moves the watermark")
	})

	t.Run("(e) failing the activation self-check", func(t *testing.T) {
		f, prev, chk := base(t, rtOpts{})
		withSelfCheck(t, func(*BundleCheck) error { return errors.New("self-check said no") })
		f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 11}))
		info := f.run()
		f.requireRejected(info, RejectGateFailure, prev.FullDigest, chk)
		assert.Contains(t, info.LastRejection.Detail, "self-check said no")
	})

	t.Run("(f) unsigned drop after a signed bundle was active (ratchet)", func(t *testing.T) {
		f, prev, chk := base(t, rtOpts{})
		f.put(unsignedCandidate(t, bundleOpts{Seq: 11}))
		info := f.run()
		f.requireRejected(info, RejectUnsignedRatchet, prev.FullDigest, chk)
	})

	t.Run("(g) unsigned bundle claiming a huge sequence never becomes the watermark", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{})
		huge := unsignedCandidate(t, bundleOpts{SeqRaw: `"18446744073709551615"`, Tag: "huge"})
		f.put(huge)
		info := f.run()

		assert.Equal(t, BundleSourceFile, info.Source, "an unsigned drop is accepted by default")
		assert.False(t, info.SignatureVerified)
		assert.Empty(t, info.Sequence, "the attacker-chosen manifest sequence is never surfaced as authoritative")
		assert.True(t, info.CoverageDegraded)
		assert.Equal(t, DegradedUnsignedExternalActive, info.DegradedReason)
		st := f.store.persisted(t)
		assert.False(t, st.SignatureRatchet)
		assert.Equal(t, uint64(0), st.Authorities[AuthorityPublisher].Watermark, "watermark unchanged")

		legit := signedCandidate(t, keyPublisher, bundleOpts{Seq: 7, Tag: "legit"})
		info = f.activate(legit)
		assert.Equal(t, "7", info.Sequence, "a later legitimate signed release still activates")
		assert.True(t, info.SignatureVerified)
		assert.False(t, info.CoverageDegraded)
	})

	t.Run("(h) fresh install, publisher-signed bundle older than the embedded corpus", func(t *testing.T) {
		f := newRTFixture(t, rtOpts{EmbeddedSeq: 100})
		f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5}))
		info := f.run()

		assert.Equal(t, BundleSourceEmbedded, info.Source)
		assert.Equal(t, embeddedDigest(), info.FullDigest)
		require.NotNil(t, info.LastRejection)
		assert.Equal(t, RejectDowngrade, info.LastRejection.Reason)
		assert.Contains(t, info.LastRejection.Detail, "watermark 100")
		assert.False(t, info.CoverageDegraded, "the embedded default at the baseline is full coverage")
	})

	t.Run("(i) operator bundle while the publisher authority is active", func(t *testing.T) {
		f, prev, chk := base(t, rtOpts{Sec: func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(1) }})
		f.put(signedCandidate(t, keyOperator, bundleOpts{Seq: 50, Tag: "operator"}))
		info := f.run()
		f.requireRejected(info, RejectAuthorityNotActive, prev.FullDigest, chk)
		assert.Equal(t, AuthorityPublisher, info.Authority, "the publisher bundle keeps serving")
	})
}

// The rollback half of the Independent Test: embedded restored, override
// logged, watermark unchanged, the rolled-away release is not re-activated.
func TestRuntime_US1_ExplicitRollbackToEmbedded(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	rel := signedCandidate(t, keyPublisher, bundleOpts{Seq: 10})
	f.activate(rel)

	info, err := RollbackBundle(BundleRollbackRequest{Embedded: true, Digest: embeddedDigest()}, f.log)
	require.NoError(t, err)

	assert.Equal(t, BundleSourceEmbedded, info.Source)
	assert.Equal(t, embeddedDigest(), info.FullDigest)
	assert.True(t, info.Rollback)
	assert.True(t, info.Pinned)
	st := f.store.persisted(t)
	assert.Equal(t, uint64(10), st.Authorities[AuthorityPublisher].Watermark, "the high-watermark is unchanged")
	assert.Nil(t, st.Active)
	assert.Contains(t, f.logMessages(zapcore.WarnLevel), "TPA scanner bundle ROLLBACK: operator override")

	// The next refresh finds the rejected release at the path and refuses it.
	info = f.run()
	assert.Equal(t, BundleSourceEmbedded, info.Source)
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectDenyListed, info.LastRejection.Reason)
	assert.Equal(t, embeddedDigest(), info.FullDigest)
	assert.True(t, info.CoverageDegraded, "the embedded default sits below the retained publisher watermark")
	assert.Equal(t, DegradedEmbeddedBelowWatermark, info.DegradedReason)
}

// --- SC-001: tamper classes at the pipeline level --------------------------------

func TestRuntime_SC001_TamperClassesNeverCompile(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	prev := f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 10, Tag: "base"}))
	chk := servingCheck()

	good := signedCandidate(t, keyPublisher, bundleOpts{Seq: 11, Tag: "bbb"})
	other := buildBundle(t, bundleOpts{Seq: 12, KeyID: keyPublisher.id})
	renamed := signedCandidate(t, keyPublisher, bundleOpts{Seq: 11, KeyID: keyOperator.id})

	cases := []struct {
		name   string
		cand   Candidate
		reason string
	}{
		{"flipped byte", Candidate{Bundle: flipByte(t, good.Bundle, "bundle-marker"), Sidecar: good.Sidecar, SidecarPresent: true}, RejectTamper},
		{"signature over a different bundle", Candidate{Bundle: good.Bundle, Sidecar: signBundle(t, keyPublisher.priv, other), SidecarPresent: true}, RejectTamper},
		{"signature from an untrusted key", Candidate{Bundle: good.Bundle, Sidecar: signBundle(t, keyUntrusted.priv, good.Bundle), SidecarPresent: true}, RejectKeyNotTrusted},
		{"manifest names another key", renamed, RejectTamper},
		{"garbage sidecar never falls back to unsigned", Candidate{Bundle: good.Bundle, Sidecar: []byte("not json"), SidecarPresent: true}, RejectSidecarInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := countCompiles(t)
			f.put(tc.cand)
			info := f.run()
			f.requireRejected(info, tc.reason, prev.FullDigest, chk)
			assert.Zero(t, *n, "no pattern may be compiled for a refused candidate")
		})
	}

	t.Run("the untampered original activates", func(t *testing.T) {
		info := f.activate(good)
		assert.Equal(t, "11", info.Sequence)
		require.NotNil(t, info.LastRejection, "the last rejection survives a later success")
	})
}

// FR-006 at the pipeline level: each run opens the bundle and the sidecar once.
func TestRuntime_ReadsEachFileOnce(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	var mu sync.Mutex
	opens := map[string]int{}
	candidateOpenHook = func(p string) {
		mu.Lock()
		opens[p]++
		mu.Unlock()
	}
	t.Cleanup(func() { candidateOpenHook = nil })

	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 3}))
	f.run()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, opens[f.path], "the bundle is read exactly once")
	assert.Equal(t, 1, opens[f.path+".sig"], "the sidecar is read exactly once")
}

// --- SC-010a: ceilings, both sides, at the pipeline level -------------------------

func TestRuntime_SC010a_Ceilings(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	prev := f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 1, Tag: "base"}))
	chk := servingCheck()

	t.Run("bundle exactly 8 MiB activates, one byte over is refused", func(t *testing.T) {
		at := signedCandidate(t, keyPublisher, bundleOpts{Seq: 2, PadTo: MaxBundleBytes})
		require.Len(t, at.Bundle, MaxBundleBytes)
		info := f.activate(at)
		assert.Equal(t, "2", info.Sequence)
		prev, chk = info, servingCheck()

		over := signedCandidate(t, keyPublisher, bundleOpts{Seq: 3, PadTo: MaxBundleBytes + 1})
		require.Len(t, over.Bundle, MaxBundleBytes+1)
		f.put(over)
		info = f.run()
		f.requireRejected(info, RejectCeilingExceeded, prev.FullDigest, chk)
		assert.Contains(t, info.LastRejection.Detail, "bundle")
	})

	t.Run("2000 rules activate, 2001 are refused", func(t *testing.T) {
		at := signedCandidate(t, keyPublisher, bundleOpts{Seq: 4, Rules: MaxBundleRules})
		info := f.activate(at)
		prev, chk = info, servingCheck()

		over := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Rules: MaxBundleRules + 1})
		n := countCompiles(t)
		f.put(over)
		info = f.run()
		f.requireRejected(info, RejectCeilingExceeded, prev.FullDigest, chk)
		assert.Equal(t, "rules: 2001 > 2000", info.LastRejection.Detail)
		assert.Zero(t, *n, "the rule ceiling is enforced before anything is compiled")
	})

	t.Run("sidecar 4096 bytes activates, 4097 is refused", func(t *testing.T) {
		c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 6})
		pad := func(n int) []byte {
			return append(append([]byte(nil), c.Sidecar...), bytes.Repeat([]byte(" "), n-len(c.Sidecar))...)
		}
		at := c
		at.Sidecar = pad(MaxSidecarBytes)
		info := f.activate(at)
		prev, chk = info, servingCheck()

		over := signedCandidate(t, keyPublisher, bundleOpts{Seq: 7})
		over.Sidecar = append(append([]byte(nil), over.Sidecar...), bytes.Repeat([]byte(" "), MaxSidecarBytes+1-len(over.Sidecar))...)
		f.put(over)
		info = f.run()
		f.requireRejected(info, RejectCeilingExceeded, prev.FullDigest, chk)
		assert.Contains(t, info.LastRejection.Detail, "sidecar")
	})
	_ = prev
	_ = chk
}

// stallReader blocks every Read until release is closed, like a read on a
// stalled network filesystem.
type stallReader struct{ release <-chan struct{} }

func (b stallReader) Read([]byte) (int, error) {
	<-b.release
	return 0, io.EOF
}

func TestRuntime_SC010a_ReadBudget(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	prev := f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 1, Tag: "base"}))
	chk := servingCheck()

	prevBudget := bundleReadBudget
	bundleReadBudget = 50 * time.Millisecond
	t.Cleanup(func() { bundleReadBudget = prevBudget })

	release := make(chan struct{})
	var blocking atomic.Bool
	blocking.Store(true)
	candidateReaderWrap = func(r io.Reader) io.Reader {
		if blocking.Load() {
			return stallReader{release: release}
		}
		return r
	}
	t.Cleanup(func() {
		candidateReaderWrap = nil
		select {
		case <-release:
		default:
			close(release)
		}
	})

	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 2}))
	start := time.Now()
	info := f.run()
	assert.Less(t, time.Since(start), 5*time.Second, "the run returns within the budget, it does not wait for the reader")
	f.requireRejected(info, RejectReadTimeout, prev.FullDigest, chk)

	// A second refresh while the first reader is still parked is refused fast.
	start = time.Now()
	info = f.run()
	assert.Less(t, time.Since(start), 2*time.Second)
	f.requireRejected(info, RejectReadTimeout, prev.FullDigest, chk)

	// Once the stalled read returns the path is usable again and a legitimate
	// drop activates.
	blocking.Store(false)
	close(release)
	require.Eventually(t, func() bool {
		candidateParked.mu.Lock()
		defer candidateParked.mu.Unlock()
		return len(candidateParked.paths) == 0
	}, 5*time.Second, 10*time.Millisecond, "the parked reader must drain")
	bundleReadBudget = prevBudget
	info = f.run()
	assert.Equal(t, "2", info.Sequence)
	assert.Empty(t, info.LoadError)
}

// --- idempotence ------------------------------------------------------------------

func TestRuntime_IdenticalRunsAreIdempotent(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5}))

	first := f.run()
	second := f.run()
	third := f.run()

	assert.Equal(t, first, second)
	assert.Equal(t, first, third)
	assert.Equal(t, 1, f.store.commitCount(), "identical runs commit once")
	assert.Len(t, f.store.historyRows(), 1, "and append one history row")
	loaded := 0
	for _, m := range f.logMessages(zapcore.InfoLevel) {
		if m == "loaded TPA scanner bundle" {
			loaded++
		}
	}
	assert.Equal(t, 1, loaded, "and log once")
}

func TestRuntime_RepeatedRefusalIsLoggedAndRecordedOnce(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 10}))
	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 9}))

	first := f.run()
	require.NotNil(t, first.LastRejection)
	time.Sleep(5 * time.Millisecond)
	second := f.run()

	assert.Equal(t, first.LastRejection, second.LastRejection, "the same refusal is not re-recorded")
	errs := 0
	for _, m := range f.logMessages(zapcore.ErrorLevel) {
		if m == "configured TPA scanner bundle refused; keeping the last-known-good corpus" {
			errs++
		}
	}
	assert.Equal(t, 1, errs, "and not re-logged")
}

func TestRuntime_ReloadPicksUpAChangedFileWithoutNewConfig(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5}))
	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 6, Tag: "next"}))

	info := ReloadBundle(f.log)

	assert.Equal(t, "6", info.Sequence)
	assert.Len(t, f.store.historyRows(), 2)
}

// --- FR-004b: the active-root declaration governs the SERVING bundle -------------------

func TestRuntime_FR004b_RootSwitchDeactivatesServingBundle(t *testing.T) {
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(1)
	}})
	op := signedCandidate(t, keyOperator, bundleOpts{Seq: 5})
	info := f.activate(op)
	assert.Equal(t, AuthorityOperator, info.Authority)
	assert.False(t, info.CoverageDegraded, "a baselined operator bundle under the operator root is full coverage")

	// The operator declares the publisher root; the bundle path is untouched.
	f.sec.TPAActiveRoot = config.TPARootPublisher
	info = f.run()

	assert.Equal(t, BundleSourceEmbedded, info.Source, "the now-inactive authority's bundle stops serving")
	assert.Equal(t, embeddedDigest(), info.FullDigest)
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectAuthorityNotActive, info.LastRejection.Reason)
	assert.False(t, info.CoverageDegraded, "an embedded default under the publisher root is full coverage")
	assert.Nil(t, f.store.persisted(t).Active)
	assert.NotEmpty(t, f.logMessages(zapcore.WarnLevel))

	// Switching back and removing the file: the operator's bundle is gone.
	f.sec.TPAActiveRoot = config.TPARootOperator
	f.remove()
	info = f.run()
	assert.Equal(t, BundleSourceEmbedded, info.Source)
	assert.True(t, info.CoverageDegraded)
	assert.Equal(t, DegradedOperatorBundleMissing, info.DegradedReason)
}

func TestRuntime_OperatorWithoutBaselineRunsDegraded(t *testing.T) {
	f := newRTFixture(t, rtOpts{Sec: func(s *config.SecurityConfig) { s.TPAActiveRoot = config.TPARootOperator }})
	info := f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 5}))
	assert.Equal(t, AuthorityOperator, info.Authority)
	assert.True(t, info.SignatureVerified)
	assert.True(t, info.CoverageDegraded)
	assert.Equal(t, DegradedOperatorAuthorityUnbaselined, info.DegradedReason)
}

// --- state_commit_failed ----------------------------------------------------------------

func TestRuntime_CommitFailureKeepsPreviousBundleServing(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	prev := f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5}))
	chk := servingCheck()

	f.store.failCommit = errors.New("disk full")
	next := signedCandidate(t, keyPublisher, bundleOpts{Seq: 6, Tag: "next"})
	f.put(next)
	info := f.run()

	f.requireRejected(info, RejectStateCommitFailed, prev.FullDigest, chk)
	assert.Contains(t, info.LastRejection.Detail, "disk full")
	assert.NotContains(t, f.lkgFiles(), digestOf(next.Bundle)+".json",
		"the LKG file of a candidate that was not activated is reconciled away")
	assert.Equal(t, uint64(5), f.store.persisted(t).Authorities[AuthorityPublisher].Watermark)

	// The store recovers; the very next run activates the candidate.
	f.store.failCommit = nil
	info = f.run()
	assert.Equal(t, "6", info.Sequence)
	assert.Empty(t, info.LoadError)
}

func TestRuntime_CommitFailureOnFreshInstallKeepsEmbeddedServing(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.store.failCommit = errors.New("read-only database")
	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5}))

	info := f.run()

	assert.Equal(t, BundleSourceEmbedded, info.Source)
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectStateCommitFailed, info.LastRejection.Reason)
	assert.False(t, info.CoverageDegraded)
}

func TestRuntime_LKGWriteFailureRefusesActivation(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	// A regular file where the LKG directory must be makes every LKG write fail.
	require.NoError(t, writeFileForTest(f.dir+"/tpa-lkg", []byte("not a directory")))
	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5}))

	info := f.run()

	assert.Equal(t, BundleSourceEmbedded, info.Source, "an activation that cannot be made durable does not happen")
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectStateCommitFailed, info.LastRejection.Reason)
	assert.Contains(t, info.LastRejection.Detail, "last-known-good")
	assert.Zero(t, f.store.commitCount())
}

func TestRuntime_NoStoreNoDataDirStillWorks(t *testing.T) {
	f := newRTFixture(t, rtOpts{NoStore: true, NoDataDir: true})
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 10}))
	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 9, Tag: "older"}))
	info := f.run()
	require.NotNil(t, info.LastRejection)
	assert.Equal(t, RejectDowngrade, info.LastRejection.Reason, "in-memory state alone enforces the watermark")
	assert.Empty(t, f.lkgFiles())
	rows, err := BundleHistory()
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}

// ApplySecurityConfig carries the whole trust policy, and the Service exposes
// thin delegates for the REST/CLI layer.
func TestService_BundleDelegates(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	svc, _, _ := newTestService(t)
	a := signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, Tag: "A"})
	aPath := f.drop + "/a.json"
	f.putAt(aPath, a)
	f.put(a)

	svc.ApplySecurityConfig(f.sec)
	require.Equal(t, "5", svc.BundleStatus().Sequence, "ApplySecurityConfig runs the signed pipeline")
	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 6, Tag: "B"}))
	assert.Equal(t, "6", svc.ReloadBundle().Sequence)

	rows, err := svc.BundleHistory()
	require.NoError(t, err)
	assert.Len(t, rows, 2)

	info, err := svc.RollbackBundle(BundleRollbackRequest{Path: aPath, Digest: digestOf(a.Bundle)})
	require.NoError(t, err)
	assert.True(t, info.Pinned)

	info, err = svc.UnpinBundle()
	require.NoError(t, err)
	assert.False(t, info.Pinned)

	// A nil security block means "embedded, publisher root, nothing configured".
	svc.ApplySecurityConfig(nil)
	assert.Equal(t, BundleSourceEmbedded, svc.BundleStatus().Source)
}

func TestBundleStatus_StalenessDays(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	stamp := time.Now().Add(-72*time.Hour - time.Minute).UTC().Format(time.RFC3339)
	f.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 5, GeneratedAt: stamp}))

	info := f.run()

	require.NotNil(t, info.StalenessDays)
	assert.Equal(t, 3, *info.StalenessDays)
	assert.Nil(t, stalenessDays("", time.Now()))
	assert.Nil(t, stalenessDays("not a date", time.Now()))
	future := stalenessDays(time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339), time.Now())
	require.NotNil(t, future)
	assert.Equal(t, 0, *future)
}

// An unsigned file without a manifest generated_at falls back to its mtime; a
// SIGNED bundle's freshness comes from its signed manifest only.
func TestRuntime_GeneratedAtFallbackIsUnsignedOnly(t *testing.T) {
	f := newRTFixture(t, rtOpts{})
	f.put(unsignedCandidate(t, bundleOpts{Tag: "u"}))
	assert.NotEmpty(t, f.run().GeneratedAt, "unsigned: mtime fallback")

	f2 := newRTFixture(t, rtOpts{})
	f2.put(signedCandidate(t, keyPublisher, bundleOpts{Seq: 2}))
	assert.Empty(t, f2.run().GeneratedAt, "signed: never the attacker-writable mtime")
}
