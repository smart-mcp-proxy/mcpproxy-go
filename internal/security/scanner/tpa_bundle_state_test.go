package scanner

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// opRoot configures the operator authority as the active root with a baseline.
func opRoot(baseline uint64) func(*config.SecurityConfig) {
	return func(s *config.SecurityConfig) {
		s.TPAActiveRoot = config.TPARootOperator
		s.TPAOperatorBaseline = u64p(baseline)
	}
}

// --- normalizeState / cloneState ------------------------------------------------

func TestNormalizeState_SeedsBothAuthoritiesFromTheirBaselines(t *testing.T) {
	withTrustAnchors(t, []ed25519.PublicKey{keyPublisher.pub}, 2, buildBundle(t, bundleOpts{Seq: 100}))
	p := testPolicy(func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(10); s.TPAOperatorEpoch = u64p(3) })

	st, logs := normalizeState(nil, p)
	require.NotNil(t, st)
	assert.Equal(t, TPABundleStateSchemaVersion, st.SchemaVersion)
	assert.Empty(t, logs, "a fresh install logs nothing")
	assert.Equal(t, &TPAAuthorityState{Epoch: 2, Watermark: 100}, st.Authorities[AuthorityPublisher])
	assert.Equal(t, &TPAAuthorityState{Epoch: 3, Watermark: 10}, st.Authorities[AuthorityOperator])
	assert.False(t, st.SignatureRatchet)
	assert.Nil(t, st.Active)
}

func TestNormalizeState_OperatorWithoutBaselineSeedsZero(t *testing.T) {
	withTrustAnchors(t, nil, 0, buildBundle(t, bundleOpts{Seq: 100}))
	st, _ := normalizeState(nil, testPolicy(nil))
	assert.Equal(t, uint64(0), st.Authorities[AuthorityOperator].Watermark)
}

func TestNormalizeState_DoesNotMutateItsInput(t *testing.T) {
	withTrustAnchors(t, nil, 1, buildBundle(t, bundleOpts{Seq: 100}))
	in := &TPABundleState{
		Authorities: map[string]*TPAAuthorityState{
			AuthorityPublisher: {Epoch: 0, Watermark: 7, WatermarkSetBy: "abc", Pin: &TPABundlePin{FullDigest: "p"}, DenyList: []TPABundleDenyEntry{{FullDigest: "d"}}},
		},
	}
	before := cloneState(in)
	out, _ := normalizeState(in, testPolicy(nil))

	assert.Equal(t, before, in, "the committed mirror is never mutated")
	assert.NotSame(t, in, out)
	assert.NotSame(t, in.Authorities[AuthorityPublisher], out.Authorities[AuthorityPublisher])
}

func TestNormalizeState_EpochChangeRescopesAndRetiresPinsAndDenyEntries(t *testing.T) {
	withTrustAnchors(t, nil, 1, buildBundle(t, bundleOpts{Seq: 100}))
	in := &TPABundleState{
		SignatureRatchet: true,
		Authorities: map[string]*TPAAuthorityState{
			AuthorityPublisher: {
				Epoch: 0, Watermark: 18446744073709551615, WatermarkSetBy: "attacker", ExternalEverActivated: true,
				Pin:      &TPABundlePin{Sequence: 5, FullDigest: "pin"},
				DenyList: []TPABundleDenyEntry{{Sequence: 9, FullDigest: "d1"}},
			},
		},
	}
	out, logs := normalizeState(in, testPolicy(nil))

	pub := out.Authorities[AuthorityPublisher]
	assert.Equal(t, uint64(1), pub.Epoch)
	assert.Equal(t, uint64(100), pub.Watermark, "re-baselined from the binary's own corpus, not zero")
	assert.Equal(t, "", pub.WatermarkSetBy)
	assert.True(t, pub.ExternalEverActivated, "an epoch re-scope keeps the record that the authority once served a corpus (FR-009a(b))")
	assert.Nil(t, pub.Pin)
	assert.Empty(t, pub.DenyList)
	assert.True(t, out.SignatureRatchet, "the ratchet is install-wide and survives an epoch bump")

	require.Len(t, logs, 1)
	assert.Contains(t, logs[0], "trust epoch for publisher changed 0→1")
	assert.Contains(t, logs[0], "re-baselined to 100")
	assert.Contains(t, logs[0], "2 pin/deny entries retired")
}

func TestNormalizeState_BaselineIsReappliedAndOnlyEverRaises(t *testing.T) {
	withTrustAnchors(t, nil, 0, buildBundle(t, bundleOpts{Seq: 100}))
	p := testPolicy(func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(50) })

	t.Run("a lowered watermark is raised back and loses its setter", func(t *testing.T) {
		in := &TPABundleState{Authorities: map[string]*TPAAuthorityState{
			AuthorityPublisher: {Watermark: 3, WatermarkSetBy: "forged"},
			AuthorityOperator:  {Watermark: 0, WatermarkSetBy: "forged"},
		}}
		out, _ := normalizeState(in, p)
		assert.Equal(t, uint64(100), out.Authorities[AuthorityPublisher].Watermark)
		assert.Equal(t, "", out.Authorities[AuthorityPublisher].WatermarkSetBy)
		assert.Equal(t, uint64(50), out.Authorities[AuthorityOperator].Watermark)
		assert.Equal(t, "", out.Authorities[AuthorityOperator].WatermarkSetBy)
	})

	t.Run("a higher recorded watermark is kept with its setter", func(t *testing.T) {
		in := &TPABundleState{Authorities: map[string]*TPAAuthorityState{
			AuthorityPublisher: {Watermark: 150, WatermarkSetBy: "real"},
		}}
		out, _ := normalizeState(in, p)
		assert.Equal(t, uint64(150), out.Authorities[AuthorityPublisher].Watermark)
		assert.Equal(t, "real", out.Authorities[AuthorityPublisher].WatermarkSetBy)
	})

	t.Run("an equal watermark keeps its setter", func(t *testing.T) {
		in := &TPABundleState{Authorities: map[string]*TPAAuthorityState{
			AuthorityPublisher: {Watermark: 100, WatermarkSetBy: "release"},
		}}
		out, _ := normalizeState(in, p)
		assert.Equal(t, "release", out.Authorities[AuthorityPublisher].WatermarkSetBy)
	})
}

func TestCloneState_IsDeep(t *testing.T) {
	assert.Nil(t, cloneState(nil))

	in := &TPABundleState{
		SignatureRatchet: true,
		Active:           &TPABundleActiveRecord{FullDigest: "a"},
		Authorities: map[string]*TPAAuthorityState{
			AuthorityPublisher: {Watermark: 5, Pin: &TPABundlePin{FullDigest: "p"}, DenyList: []TPABundleDenyEntry{{FullDigest: "d"}}},
		},
	}
	out := cloneState(in)
	require.Equal(t, in, out)

	out.Active.FullDigest = "changed"
	out.Authorities[AuthorityPublisher].Watermark = 99
	out.Authorities[AuthorityPublisher].Pin.FullDigest = "changed"
	out.Authorities[AuthorityPublisher].DenyList[0].FullDigest = "changed"
	out.Authorities[AuthorityPublisher].DenyList = append(out.Authorities[AuthorityPublisher].DenyList, TPABundleDenyEntry{})

	assert.Equal(t, "a", in.Active.FullDigest)
	assert.Equal(t, uint64(5), in.Authorities[AuthorityPublisher].Watermark)
	assert.Equal(t, "p", in.Authorities[AuthorityPublisher].Pin.FullDigest)
	assert.Equal(t, "d", in.Authorities[AuthorityPublisher].DenyList[0].FullDigest)
	assert.Len(t, in.Authorities[AuthorityPublisher].DenyList, 1)
}

// --- downgradeCheck / denied / pinBinds -----------------------------------------

func TestDowngradeCheck_AllowRules(t *testing.T) {
	withTrustAnchors(t, nil, 0, buildBundle(t, bundleOpts{Seq: 10}))
	p := testPolicy(opRoot(5))

	a := &TPAAuthorityState{Watermark: 20, WatermarkSetBy: "setter"}
	pinned := &TPAAuthorityState{Watermark: 20, WatermarkSetBy: "setter", Pin: &TPABundlePin{Sequence: 15, FullDigest: "pin"}}

	cases := []struct {
		name   string
		a      *TPAAuthorityState
		auth   string
		seq    uint64
		digest string
		mode   evalMode
		rb     string
		allow  bool
		detail string
	}{
		{"above the watermark", a, AuthorityPublisher, 21, "x", evalNormal, "", true, ""},
		{"max sequence", a, AuthorityPublisher, 18446744073709551615, "x", evalNormal, "", true, ""},
		{"the release that set the watermark (restart / LKG loss)", a, AuthorityPublisher, 20, "setter", evalNormal, "", true, ""},
		{"same release in lkg mode", a, AuthorityPublisher, 20, "setter", evalLKG, "", true, ""},
		{"equal sequence, different digest", a, AuthorityPublisher, 20, "other", evalNormal, "", false, "sequence 20, watermark 20, set by setter"},
		{"below the watermark", a, AuthorityPublisher, 19, "x", evalNormal, "", false, "sequence 19, watermark 20"},
		{"a seeded-baseline watermark has no setter to match", &TPAAuthorityState{Watermark: 10}, AuthorityPublisher, 10, "x", evalNormal, "", false, "sequence 10, watermark 10"},
		{"pinned target below the watermark", pinned, AuthorityPublisher, 15, "pin", evalNormal, "", true, ""},
		{"pinned target in lkg mode", pinned, AuthorityPublisher, 15, "pin", evalLKG, "", true, ""},
		{"an intermediate release is refused while a pin binds", pinned, AuthorityPublisher, 17, "mid", evalNormal, "", false, "sequence 17, watermark 20"},
		{"pin matches but the sequence sits below the baseline", &TPAAuthorityState{Watermark: 20, Pin: &TPABundlePin{FullDigest: "pin"}}, AuthorityPublisher, 9, "pin", evalNormal, "", false, "below publisher baseline 10"},
		{"rollback to the named digest", a, AuthorityPublisher, 12, "target", evalRollback, "target", true, ""},
		{"rollback to a digest other than the named one", a, AuthorityPublisher, 12, "target", evalRollback, "someone-else", false, "sequence 12, watermark 20"},
		{"rollback at exactly the baseline", a, AuthorityPublisher, 10, "target", evalRollback, "target", true, ""},
		{"rollback below the publisher baseline", a, AuthorityPublisher, 9, "target", evalRollback, "target", false, "below publisher baseline 10"},
		{"operator rollback measured against the OPERATOR baseline", a, AuthorityOperator, 6, "target", evalRollback, "target", true, ""},
		{"operator rollback below the operator baseline", a, AuthorityOperator, 4, "target", evalRollback, "target", false, "below operator baseline 5"},
		{"normal mode ignores the rollback digest", a, AuthorityPublisher, 12, "target", evalNormal, "target", false, "sequence 12, watermark 20"},
		{"nil authority record is an empty one (above the baseline)", nil, AuthorityPublisher, 11, "x", evalNormal, "", true, ""},
		{"nil authority record still enforces the baseline", nil, AuthorityPublisher, 10, "x", evalNormal, "", false, "sequence 10, watermark 10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := downgradeCheck(tc.a, tc.auth, p, tc.seq, tc.digest, tc.mode, tc.rb)
			if tc.allow {
				assert.Equal(t, "", got)
				return
			}
			require.NotEmpty(t, got)
			assert.Contains(t, got, tc.detail)
		})
	}

	t.Run("an operator authority with no baseline has no floor to enforce", func(t *testing.T) {
		np := testPolicy(nil)
		assert.Equal(t, "", downgradeCheck(a, AuthorityOperator, np, 1, "target", evalRollback, "target"))
	})
}

func TestDeniedAndPinBinds(t *testing.T) {
	a := &TPAAuthorityState{DenyList: []TPABundleDenyEntry{{Sequence: 7, FullDigest: "d7"}}}
	assert.True(t, denied(a, 7, "d7"))
	assert.False(t, denied(a, 7, "other"), "a digest the operator did not reject")
	assert.False(t, denied(a, 8, "d7"), "the key is (sequence, digest)")
	assert.False(t, denied(&TPAAuthorityState{}, 7, "d7"))
	assert.False(t, denied(nil, 7, "d7"))

	assert.False(t, pinBinds(nil))
	assert.False(t, pinBinds(&TPAAuthorityState{}))
	assert.True(t, pinBinds(&TPAAuthorityState{Pin: &TPABundlePin{FullDigest: "p"}}))
}

// --- US1 Independent Test (a)-(i), at the pure layer -----------------------------

func TestUS1IndependentTest(t *testing.T) {
	// Embedded sequence 100 is the publisher baseline; operator baseline 10.
	root := func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(10) }

	t.Run("activation records the sequence, ratchet and watermark", func(t *testing.T) {
		f := newPipelineFixture(t, 100, root)
		c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 101})
		acc := f.activate(c)

		assert.True(t, acc.Verified)
		assert.Equal(t, uint64(101), acc.Sequence)
		pub := f.auth(AuthorityPublisher)
		assert.Equal(t, uint64(101), pub.Watermark)
		assert.Equal(t, digestOf(c.Bundle), pub.WatermarkSetBy)
		assert.True(t, pub.ExternalEverActivated)
		assert.True(t, f.st.SignatureRatchet)
		require.NotNil(t, f.st.Active)
		assert.Equal(t, digestOf(c.Bundle), f.st.Active.FullDigest)
		assert.Equal(t, digestOf(c.Sidecar), f.st.Active.SidecarDigest)
		assert.True(t, f.st.Active.SignatureVerified)
	})

	activeFixture := func(t *testing.T) (*pipelineFixture, Candidate) {
		f := newPipelineFixture(t, 100, root)
		c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 101, Tag: "active"})
		f.activate(c)
		return f, c
	}

	t.Run("(a) one flipped byte", func(t *testing.T) {
		f, _ := activeFixture(t)
		c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 102, Tag: "bbb"})
		c.Bundle = flipByte(t, c.Bundle, "bundle-marker")
		f.refuse(c, RejectTamper)
	})

	t.Run("(b) a signature from a non-trusted key", func(t *testing.T) {
		f, _ := activeFixture(t)
		f.refuse(signedCandidate(t, keyUntrusted, bundleOpts{Seq: 102}), RejectKeyNotTrusted)
	})

	t.Run("(c) missing sidecar with require_signed_bundle", func(t *testing.T) {
		f, _ := activeFixture(t)
		f.repolicy(func(s *config.SecurityConfig) { root(s); s.RequireSignedBundle = true })
		f.refuse(unsignedCandidate(t, bundleOpts{Seq: 102}), RejectSignatureRequired)
	})

	t.Run("(d) a lower sequence", func(t *testing.T) {
		f, _ := activeFixture(t)
		rej := f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 100, Tag: "older"}), RejectDowngrade)
		assert.Contains(t, rej.Detail, "sequence 100, watermark 101")
	})

	t.Run("(e) a failing activation self-check", func(t *testing.T) {
		f, _ := activeFixture(t)
		withSelfCheck(t, func(*BundleCheck) error { return assert.AnError })
		f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 102}), RejectGateFailure)
	})

	t.Run("(f) unsigned after a signed one (ratchet)", func(t *testing.T) {
		f, _ := activeFixture(t)
		f.refuse(unsignedCandidate(t, bundleOpts{Seq: 102}), RejectUnsignedRatchet)
	})

	t.Run("(g) an unsigned bundle claiming a huge sequence leaves the watermark alone", func(t *testing.T) {
		f := newPipelineFixture(t, 100, root)
		huge := unsignedCandidate(t, bundleOpts{SeqRaw: `"18446744073709551615"`, Epoch: 0})
		f.activate(huge)

		assert.Equal(t, uint64(100), f.auth(AuthorityPublisher).Watermark, "an unsigned candidate never advances the watermark")
		assert.False(t, f.st.SignatureRatchet, "an unsigned activation never arms the ratchet")
		require.NotNil(t, f.st.Active)
		assert.False(t, f.st.Active.SignatureVerified)
		assert.Equal(t, "", f.st.Active.Authority)

		// ...so a later legitimate signed release still activates.
		acc := f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 101}))
		assert.Equal(t, uint64(101), acc.Sequence)
		assert.Equal(t, uint64(101), f.auth(AuthorityPublisher).Watermark)
	})

	t.Run("(h) fresh install: an old genuinely-signed bundle is refused by the seeded baseline", func(t *testing.T) {
		f := newPipelineFixture(t, 100, root)
		require.Nil(t, f.st.Active)
		rej := f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 50}), RejectDowngrade)
		assert.Contains(t, rej.Detail, "watermark 100")
		f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 100, Tag: "same-seq-other-bytes"}), RejectDowngrade)
	})

	t.Run("(i) operator-signed while the publisher authority is active", func(t *testing.T) {
		f, c := activeFixture(t)
		rej := f.refuse(signedCandidate(t, keyOperator, bundleOpts{Seq: 11}), RejectAuthorityNotActive)
		assert.Contains(t, rej.Detail, "verified by operator key "+keyOperator.id)
		assert.Contains(t, rej.Detail, "active root is publisher")
		assert.Equal(t, digestOf(c.Bundle), f.st.Active.FullDigest, "the publisher bundle keeps serving")
	})
}

// SC-002: equal sequence + same digest is an idempotent re-load; a different
// digest at the same sequence is a downgrade.
func TestEqualSequenceRules(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)
	c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, Tag: "a"})
	f.activate(c)

	acc, rej := f.eval(c)
	require.Nil(t, rej, "re-loading the release that set the watermark (restart, LKG loss) is allowed")
	assert.Equal(t, uint64(150), acc.Sequence)

	f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, Tag: "b"}), RejectDowngrade)

	// Re-activating it changes nothing.
	before := cloneState(f.st)
	f.st, _ = applyActivation(f.st, acc, c, fixedNow.Add(time.Hour))
	f.renormalize()
	assert.Equal(t, before.Authorities, f.st.Authorities)
}

// --- SC-002a ----------------------------------------------------------------------

func TestSC002a_MaxSequenceThenEpochBumpRecovers(t *testing.T) {
	survivor := newTestKey(0x44)
	f := newPipelineFixture(t, 100, nil)

	// (a) A compromised publisher key signs the maximum sequence.
	compromised := signedCandidate(t, keyPublisher, bundleOpts{SeqRaw: `"18446744073709551615"`, Tag: "evil"})
	f.activate(compromised)
	require.Equal(t, uint64(18446744073709551615), f.auth(AuthorityPublisher).Watermark)

	// Nothing under the same epoch can ever beat that watermark.
	f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 200}), RejectDowngrade)

	// The recovery release revokes the key and bumps the publisher epoch.
	withTrustAnchors(t, []ed25519.PublicKey{survivor.pub}, 1, nil)
	f.repolicy(nil)
	pub := f.auth(AuthorityPublisher)
	assert.Equal(t, uint64(1), pub.Epoch)
	assert.Equal(t, uint64(100), pub.Watermark, "re-baselined from the binary's corpus")

	recovery := signedCandidate(t, survivor, bundleOpts{Seq: 101, Epoch: 1})
	acc, rej := f.eval(recovery)
	require.Nil(t, rej, "the recovery release supersedes the retained watermark")
	assert.Equal(t, uint64(1), acc.TrustEpoch)

	// (a2) The old bundle is refused as a stale-epoch replay even though it is
	// validly signed by a still-trusted key and carries the maximum sequence.
	oldButTrusted := signedCandidate(t, survivor, bundleOpts{SeqRaw: `"18446744073709551615"`, Epoch: 0, Tag: "replay"})
	rej2 := f.refuse(oldButTrusted, RejectStaleEpochReplay)
	assert.Contains(t, rej2.Detail, "epoch 0")
	// The compromised key itself is no longer trusted at all.
	f.refuse(compromised, RejectKeyNotTrusted)
}

func TestSC002a_AuthoritiesAreIsolated(t *testing.T) {
	// (b) operator watermark 900 vs publisher 100: neither blocks the other.
	f := newPipelineFixture(t, 100, opRoot(10))
	f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 900}))
	assert.Equal(t, uint64(900), f.auth(AuthorityOperator).Watermark)
	assert.Equal(t, uint64(100), f.auth(AuthorityPublisher).Watermark, "an operator activation never touches the publisher counter")

	f.refuse(signedCandidate(t, keyOperator, bundleOpts{Seq: 100, Tag: "low"}), RejectDowngrade)

	// Declaring the publisher root: seq 101 activates although 101 < 900.
	f.repolicy(func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(10) })
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 101}))
	assert.Equal(t, uint64(101), f.auth(AuthorityPublisher).Watermark)
	assert.Equal(t, uint64(900), f.auth(AuthorityOperator).Watermark, "a publisher activation never touches the operator counter")

	// And back: the operator counter is exactly where it was.
	f.repolicy(opRoot(10))
	f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 901}))
	f.refuse(signedCandidate(t, keyOperator, bundleOpts{Seq: 500, Tag: "mid"}), RejectDowngrade)
}

func TestSC002a_AuthorityNotActiveBothDirections(t *testing.T) {
	t.Run("operator key, publisher root", func(t *testing.T) {
		f := newPipelineFixture(t, 100, opRoot(10))
		f.repolicy(func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(10) })
		rej := f.refuse(signedCandidate(t, keyOperator, bundleOpts{Seq: 11}), RejectAuthorityNotActive)
		assert.Contains(t, rej.Detail, "active root is publisher")
	})
	t.Run("publisher key, operator root", func(t *testing.T) {
		f := newPipelineFixture(t, 100, opRoot(10))
		rej := f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 101}), RejectAuthorityNotActive)
		assert.Contains(t, rej.Detail, "verified by publisher key "+keyPublisher.id)
		assert.Contains(t, rej.Detail, "active root is operator")
	})
	t.Run("an unrecognised root matches no key", func(t *testing.T) {
		f := newPipelineFixture(t, 100, func(s *config.SecurityConfig) { s.TPAActiveRoot = "bogus" })
		f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 101}), RejectAuthorityNotActive)
		f.refuse(signedCandidate(t, keyOperator, bundleOpts{Seq: 11}), RejectAuthorityNotActive)
	})
	t.Run("a non-active-root key is reported as such, not as untrusted", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		_, rej := f.eval(signedCandidate(t, keyOperator, bundleOpts{Seq: 11}))
		require.NotNil(t, rej)
		assert.NotEqual(t, RejectKeyNotTrusted, rej.Reason)
	})
}

func TestSC002a_OperatorBaseline(t *testing.T) {
	t.Run("after a data-directory wipe the config baseline still refuses an old operator bundle", func(t *testing.T) {
		f := newPipelineFixture(t, 100, opRoot(10))
		f.st = nil // wiped
		f.renormalize()

		rej := f.refuse(signedCandidate(t, keyOperator, bundleOpts{Seq: 5}), RejectDowngrade)
		assert.Contains(t, rej.Detail, "watermark 10")
		f.refuse(signedCandidate(t, keyOperator, bundleOpts{Seq: 10}), RejectDowngrade) // must be ABOVE the baseline
		acc := f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 11}))
		assert.Equal(t, AuthorityOperator, acc.Authority)
	})

	t.Run("an operator with no baseline accepts any sequence (and runs degraded)", func(t *testing.T) {
		f := newPipelineFixture(t, 100, func(s *config.SecurityConfig) { s.TPAActiveRoot = config.TPARootOperator })
		acc := f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 1}))
		assert.Equal(t, AuthorityOperator, acc.Authority)

		degraded, reason := computeCoverage(servingFacts{Verified: true, Authority: AuthorityOperator}, f.st, f.policy, 100)
		assert.True(t, degraded)
		assert.Equal(t, DegradedOperatorAuthorityUnbaselined, reason)
	})
}

func TestEpochRules(t *testing.T) {
	t.Run("publisher epoch ahead of the binary", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		rej := f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 101, Epoch: 1}), RejectEpochAhead)
		assert.Contains(t, rej.Detail, "epoch 1")
	})
	t.Run("publisher epoch behind the binary", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		withTrustAnchors(t, []ed25519.PublicKey{keyPublisher.pub}, 2, nil)
		f.repolicy(nil)
		f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 101, Epoch: 1}), RejectStaleEpochReplay)
		_, rej := f.eval(signedCandidate(t, keyPublisher, bundleOpts{Seq: 101, Epoch: 2}))
		assert.Nil(t, rej, "an exactly equal epoch activates")
	})
	t.Run("operator epoch is config-resident", func(t *testing.T) {
		f := newPipelineFixture(t, 100, func(s *config.SecurityConfig) { opRoot(10)(s); s.TPAOperatorEpoch = u64p(3) })
		_, rej := f.eval(signedCandidate(t, keyOperator, bundleOpts{Seq: 11, Epoch: 3}))
		assert.Nil(t, rej)
		f.refuse(signedCandidate(t, keyOperator, bundleOpts{Seq: 11, Epoch: 2}), RejectStaleEpochReplay)
		f.refuse(signedCandidate(t, keyOperator, bundleOpts{Seq: 11, Epoch: 4}), RejectEpochAhead)
	})
	t.Run("an operator epoch bump re-baselines the operator watermark", func(t *testing.T) {
		f := newPipelineFixture(t, 100, opRoot(10))
		f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 900}))
		f.repolicy(func(s *config.SecurityConfig) { opRoot(10)(s); s.TPAOperatorEpoch = u64p(1) })
		assert.Equal(t, uint64(10), f.auth(AuthorityOperator).Watermark)
		_, rej := f.eval(signedCandidate(t, keyOperator, bundleOpts{Seq: 11, Epoch: 1}))
		assert.Nil(t, rej)
	})
}

// --- SC-012: a substituted state file ------------------------------------------------

// Only the guarantees anchored OUTSIDE the data directory are asserted here:
// nothing in this test claims the ratchet, watermark or pin survive.
func TestSC012_SubstitutedStateCannotLowerTheAnchors(t *testing.T) {
	forge := func(f *pipelineFixture, pinDigest string) {
		f.st = &TPABundleState{
			SchemaVersion:    1,
			SignatureRatchet: false, // cleared
			Authorities: map[string]*TPAAuthorityState{
				AuthorityPublisher: {Epoch: 0, Watermark: 0, Pin: &TPABundlePin{Sequence: 50, FullDigest: pinDigest}},
				AuthorityOperator:  {Epoch: 0, Watermark: 0},
			},
		}
		f.renormalize()
	}

	t.Run("the publisher baseline still refuses an older publisher bundle", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		old := signedCandidate(t, keyPublisher, bundleOpts{Seq: 50})
		forge(f, digestOf(old.Bundle)) // ...even with a forged pin naming exactly that release
		f.refuse(old, RejectDowngrade)
		f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 3}), RejectDowngrade)
	})

	t.Run("require_signed_bundle still refuses unsigned", func(t *testing.T) {
		f := newPipelineFixture(t, 100, func(s *config.SecurityConfig) { s.RequireSignedBundle = true })
		forge(f, "x")
		f.refuse(unsignedCandidate(t, bundleOpts{}), RejectSignatureRequired)
	})

	t.Run("the operator baseline still refuses an older operator bundle", func(t *testing.T) {
		f := newPipelineFixture(t, 100, opRoot(10))
		forge(f, "x")
		f.refuse(signedCandidate(t, keyOperator, bundleOpts{Seq: 4}), RejectDowngrade)
	})

	t.Run("the root declaration still refuses the other authority", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		forge(f, "x")
		f.refuse(signedCandidate(t, keyOperator, bundleOpts{Seq: 11}), RejectAuthorityNotActive)
	})
}

// --- applyActivation ----------------------------------------------------------------

func TestApplyActivation(t *testing.T) {
	now := fixedNow

	t.Run("signed advance", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		f.st.Authorities[AuthorityPublisher].Pin = &TPABundlePin{FullDigest: "old-pin"}
		c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, GeneratedAt: "2026-10-01T00:00:00Z"})
		acc, rej := f.eval(c)
		require.Nil(t, rej)

		in := cloneState(f.st)
		out, row := applyActivation(f.st, acc, c, now)
		assert.Equal(t, in, f.st, "the input is not mutated")

		pub := out.Authorities[AuthorityPublisher]
		assert.Equal(t, uint64(150), pub.Watermark)
		assert.Equal(t, acc.FullDigest, pub.WatermarkSetBy)
		assert.Nil(t, pub.Pin, "a normal advance supersedes the pin")
		assert.True(t, pub.ExternalEverActivated)
		assert.True(t, out.SignatureRatchet)
		assert.Equal(t, &TPABundleActiveRecord{
			Authority: AuthorityPublisher, TrustEpoch: 0, Sequence: 150,
			FullDigest: acc.FullDigest, SidecarDigest: acc.SidecarDigest, SignatureVerified: true,
			Source: BundleSourceFile, Path: c.Path, GeneratedAt: "2026-10-01T00:00:00Z", ActivatedAt: now,
		}, out.Active)

		require.NotNil(t, row)
		assert.Equal(t, &TPABundleActivation{
			Authority: AuthorityPublisher, TrustEpoch: 0, Sequence: "150", FullDigest: acc.FullDigest,
			GeneratedAt: "2026-10-01T00:00:00Z", ActivatedAt: now, Source: BundleSourceFile, SignatureVerified: true,
		}, row)
	})

	t.Run("an activation at or below the watermark leaves it alone and keeps the pin", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		target := signedCandidate(t, keyPublisher, bundleOpts{Seq: 120, Tag: "target"})
		f.activate(target)
		f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 130, Tag: "newest"}))
		f.st.Authorities[AuthorityPublisher].Pin = &TPABundlePin{Sequence: 120, FullDigest: digestOf(target.Bundle)}

		acc, rej := evaluateCandidate(target, f.env(evalLKG))
		require.Nil(t, rej, "the pinned target re-validates")
		out, _ := applyActivation(f.st, acc, target, now)
		pub := out.Authorities[AuthorityPublisher]
		assert.Equal(t, uint64(130), pub.Watermark)
		assert.NotNil(t, pub.Pin)
	})

	t.Run("unsigned never touches the watermark, the ratchet or the authority records", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		c := unsignedCandidate(t, bundleOpts{SeqRaw: `"18446744073709551615"`})
		acc, rej := f.eval(c)
		require.Nil(t, rej)

		out, row := applyActivation(f.st, acc, c, now)
		assert.Equal(t, f.st.Authorities, out.Authorities)
		assert.False(t, out.SignatureRatchet)
		require.NotNil(t, out.Active)
		assert.Equal(t, "", out.Active.Authority)
		assert.False(t, out.Active.SignatureVerified)
		assert.Equal(t, uint64(0), out.Active.Sequence)
		require.NotNil(t, row)
		assert.Equal(t, "", row.Sequence)
		assert.False(t, row.SignatureVerified)
		assert.Equal(t, "", row.Authority)
	})

	t.Run("an operator activation scopes to the operator record", func(t *testing.T) {
		f := newPipelineFixture(t, 100, opRoot(10))
		c := signedCandidate(t, keyOperator, bundleOpts{Seq: 11})
		acc, rej := f.eval(c)
		require.Nil(t, rej)
		out, row := applyActivation(f.st, acc, c, now)
		assert.Equal(t, uint64(11), out.Authorities[AuthorityOperator].Watermark)
		assert.Equal(t, uint64(100), out.Authorities[AuthorityPublisher].Watermark)
		assert.True(t, out.Authorities[AuthorityOperator].ExternalEverActivated)
		assert.False(t, out.Authorities[AuthorityPublisher].ExternalEverActivated)
		assert.Equal(t, AuthorityOperator, row.Authority)
	})
}

// --- applyRollback + the rollback flow ------------------------------------------------

func TestRollbackToEmbedded(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)
	r101 := signedCandidate(t, keyPublisher, bundleOpts{Seq: 101, Tag: "r101"})
	r102 := signedCandidate(t, keyPublisher, bundleOpts{Seq: 102, Tag: "r102"})
	f.activate(r101)
	f.activate(r102)

	embeddedDigest := digestOf(bundledScannerBundle)
	target := embeddedRollbackTarget(f.policy, embeddedDigest, 100, "")
	assert.True(t, target.Embedded)
	assert.Equal(t, AuthorityPublisher, target.Authority)
	assert.Equal(t, BundleSourceEmbedded, target.Source)

	away := f.st.Active
	require.NotNil(t, away)
	in := cloneState(f.st)
	out, row := applyRollback(f.st, f.policy, target, away, fixedNow)
	assert.Equal(t, in, f.st, "the input is not mutated")

	pub := out.Authorities[AuthorityPublisher]
	assert.Equal(t, uint64(102), pub.Watermark, "rollback never lowers the watermark")
	assert.Equal(t, digestOf(r102.Bundle), pub.WatermarkSetBy)
	assert.Equal(t, []TPABundleDenyEntry{{Sequence: 102, FullDigest: digestOf(r102.Bundle), DeniedAt: fixedNow}}, pub.DenyList)
	require.NotNil(t, pub.Pin)
	assert.True(t, pub.Pin.Embedded)
	assert.Equal(t, embeddedDigest, pub.Pin.FullDigest)
	assert.Equal(t, uint64(100), pub.Pin.Sequence)
	assert.Nil(t, out.Active, "the embedded default serves")
	require.NotNil(t, row)
	assert.True(t, row.Rollback)
	assert.Equal(t, BundleSourceEmbedded, row.Source)
	assert.Equal(t, AuthorityPublisher, row.Authority)
	assert.Equal(t, embeddedDigest, row.FullDigest)
	assert.Equal(t, "100", row.Sequence)

	// The next refresh: the rolled-away release is refused, intermediates too.
	f.st = out
	f.renormalize()
	rej := f.refuse(r102, RejectDenyListed)
	assert.NotEmpty(t, rej.Detail)
	f.refuse(r101, RejectDowngrade)

	// A genuinely newer release clears the pin and activates.
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 103}))
	assert.Nil(t, f.auth(AuthorityPublisher).Pin)
	assert.Len(t, f.auth(AuthorityPublisher).DenyList, 1, "the deny entry stays for its epoch")
}

func TestRollbackToNamedFile(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)
	r101 := signedCandidate(t, keyPublisher, bundleOpts{Seq: 101, Tag: "r101"})
	r102 := signedCandidate(t, keyPublisher, bundleOpts{Seq: 102, Tag: "r102"})
	f.activate(r101)
	f.activate(r102)

	// Without rollback mode the older release is a downgrade...
	f.refuse(r101, RejectDowngrade)

	// ...with it, and the operator's exact digest, it activates below the watermark.
	env := f.env(evalRollback)
	env.RollbackDigest = digestOf(r101.Bundle)
	acc, rej := evaluateCandidate(r101, env)
	require.Nil(t, rej)

	away := f.st.Active
	out, row := applyRollback(f.st, f.policy, rollbackTargetFromAccepted(acc, r101), away, fixedNow)
	pub := out.Authorities[AuthorityPublisher]
	assert.Equal(t, uint64(102), pub.Watermark)
	assert.Equal(t, digestOf(r102.Bundle), pub.WatermarkSetBy)
	require.NotNil(t, pub.Pin)
	assert.Equal(t, digestOf(r101.Bundle), pub.Pin.FullDigest)
	assert.False(t, pub.Pin.Embedded)
	assert.Equal(t, uint64(101), pub.Pin.Sequence)
	require.Len(t, pub.DenyList, 1)
	assert.Equal(t, digestOf(r102.Bundle), pub.DenyList[0].FullDigest)

	require.NotNil(t, out.Active)
	assert.True(t, out.Active.Rollback)
	assert.Equal(t, digestOf(r101.Bundle), out.Active.FullDigest)
	assert.True(t, out.Active.SignatureVerified)
	assert.Equal(t, uint64(101), out.Active.Sequence)
	assert.True(t, row.Rollback)
	assert.Equal(t, "101", row.Sequence)
	assert.True(t, row.SignatureVerified)

	// Restart: the pinned target re-validates, the rolled-away release does not.
	f.st = out
	f.renormalize()
	_, rej = evaluateCandidate(r101, f.env(evalLKG))
	assert.Nil(t, rej, "the pinned target re-validates in lkg mode")
	f.refuse(r102, RejectDenyListed)

	// Rolling away from the same release twice does not duplicate the entry.
	again, _ := applyRollback(out, f.policy, rollbackTargetFromAccepted(acc, r101), &TPABundleActiveRecord{
		Authority: AuthorityPublisher, TrustEpoch: 0, Sequence: 102, FullDigest: digestOf(r102.Bundle), SignatureVerified: true,
	}, fixedNow.Add(time.Hour))
	assert.Len(t, again.Authorities[AuthorityPublisher].DenyList, 1)
}

func TestRollback_DenyListOnlyForSignedReleasesOfThisEpoch(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)
	target := embeddedRollbackTarget(f.policy, digestOf(bundledScannerBundle), 100, "")

	t.Run("rolling away from an unsigned bundle denies nothing", func(t *testing.T) {
		out, _ := applyRollback(f.st, f.policy, target, &TPABundleActiveRecord{FullDigest: "u", Source: BundleSourceFile}, fixedNow)
		assert.Empty(t, out.Authorities[AuthorityPublisher].DenyList)
		assert.Empty(t, out.Authorities[AuthorityOperator].DenyList)
	})
	t.Run("rolling away from the embedded default denies nothing", func(t *testing.T) {
		out, _ := applyRollback(f.st, f.policy, target, nil, fixedNow)
		assert.Empty(t, out.Authorities[AuthorityPublisher].DenyList)
	})
	t.Run("a release of a retired epoch is not denied", func(t *testing.T) {
		out, _ := applyRollback(f.st, f.policy, target, &TPABundleActiveRecord{
			Authority: AuthorityPublisher, TrustEpoch: 7, Sequence: 3, FullDigest: "old", SignatureVerified: true,
		}, fixedNow)
		assert.Empty(t, out.Authorities[AuthorityPublisher].DenyList)
	})
}

func TestRollback_BaselineIsMeasuredAgainstTheTargetsOwnAuthority(t *testing.T) {
	t.Run("a publisher target below the embedded corpus", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		old := signedCandidate(t, keyPublisher, bundleOpts{Seq: 90})
		env := f.env(evalRollback)
		env.RollbackDigest = digestOf(old.Bundle)
		_, rej := evaluateCandidate(old, env)
		require.NotNil(t, rej)
		assert.Equal(t, RejectDowngrade, rej.Reason)
		assert.Contains(t, rej.Detail, "below publisher baseline 100")
	})

	t.Run("an operator target at sequence 9 is fine against a publisher corpus at 100", func(t *testing.T) {
		f := newPipelineFixture(t, 100, opRoot(5))
		f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 20, Tag: "now"}))
		tgt := signedCandidate(t, keyOperator, bundleOpts{Seq: 9, Tag: "target"})
		env := f.env(evalRollback)
		env.RollbackDigest = digestOf(tgt.Bundle)
		_, rej := evaluateCandidate(tgt, env)
		assert.Nil(t, rej)

		below := signedCandidate(t, keyOperator, bundleOpts{Seq: 4, Tag: "below"})
		env.RollbackDigest = digestOf(below.Bundle)
		_, rej = evaluateCandidate(below, env)
		require.NotNil(t, rej)
		assert.Contains(t, rej.Detail, "below operator baseline 5")
	})

	t.Run("a baseline-less operator rollback proceeds (degraded)", func(t *testing.T) {
		f := newPipelineFixture(t, 100, func(s *config.SecurityConfig) { s.TPAActiveRoot = config.TPARootOperator })
		f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 20, Tag: "now"}))
		tgt := signedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "target"})
		env := f.env(evalRollback)
		env.RollbackDigest = digestOf(tgt.Bundle)
		acc, rej := evaluateCandidate(tgt, env)
		require.Nil(t, rej)
		out, _ := applyRollback(f.st, f.policy, rollbackTargetFromAccepted(acc, tgt), f.st.Active, fixedNow)
		f.st = out
		f.renormalize()
		degraded, reason := computeCoverage(servingFacts{Verified: true, Authority: AuthorityOperator}, f.st, f.policy, 100)
		assert.True(t, degraded)
		assert.Equal(t, DegradedOperatorAuthorityUnbaselined, reason)
	})
}

func TestRollback_PinSuspendedWhileTheOtherRootIsActive(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)
	r101 := signedCandidate(t, keyPublisher, bundleOpts{Seq: 101})
	f.activate(r101)
	f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 102}))
	out, _ := applyRollback(f.st, f.policy, embeddedRollbackTarget(f.policy, digestOf(bundledScannerBundle), 100, ""), f.st.Active, fixedNow)
	f.st = out

	// Switching to the operator root leaves the publisher pin in place (suspended,
	// not deleted), and the operator authority's own rules apply.
	f.repolicy(opRoot(10))
	assert.NotNil(t, f.auth(AuthorityPublisher).Pin)
	f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 11}))
	assert.NotNil(t, f.auth(AuthorityPublisher).Pin)
}

// --- computeCoverage ----------------------------------------------------------------------

func TestComputeCoverage_Degrades(t *testing.T) {
	t.Run("unsigned external active", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		d, r := computeCoverage(servingFacts{}, f.st, f.policy, 100)
		assert.True(t, d)
		assert.Equal(t, DegradedUnsignedExternalActive, r)
	})

	t.Run("operator authority unbaselined", func(t *testing.T) {
		f := newPipelineFixture(t, 100, func(s *config.SecurityConfig) { s.TPAActiveRoot = config.TPARootOperator })
		d, r := computeCoverage(servingFacts{Verified: true, Authority: AuthorityOperator}, f.st, f.policy, 100)
		assert.True(t, d)
		assert.Equal(t, DegradedOperatorAuthorityUnbaselined, r)
	})

	t.Run("embedded below the publisher watermark", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 150}))
		d, r := computeCoverage(servingFacts{Embedded: true}, f.st, f.policy, 100)
		assert.True(t, d)
		assert.Equal(t, DegradedEmbeddedBelowWatermark, r)
	})

	t.Run("operator root declared and its bundle is gone", func(t *testing.T) {
		f := newPipelineFixture(t, 100, opRoot(10))
		f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 11}))
		d, r := computeCoverage(servingFacts{Embedded: true}, f.st, f.policy, 100)
		assert.True(t, d)
		assert.Equal(t, DegradedOperatorBundleMissing, r)
	})

	t.Run("the first matching condition wins", func(t *testing.T) {
		// Unsigned AND (irrelevantly) operator-authority: rule 1 reports first.
		f := newPipelineFixture(t, 100, func(s *config.SecurityConfig) { s.TPAActiveRoot = config.TPARootOperator })
		_, r := computeCoverage(servingFacts{Authority: AuthorityOperator}, f.st, f.policy, 100)
		assert.Equal(t, DegradedUnsignedExternalActive, r)

		// Embedded below the publisher watermark AND operator bundle missing: rule 3 first.
		g := newPipelineFixture(t, 100, opRoot(10))
		g.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 11}))
		g.st.Authorities[AuthorityPublisher].Watermark = 500
		_, r = computeCoverage(servingFacts{Embedded: true}, g.st, g.policy, 100)
		assert.Equal(t, DegradedEmbeddedBelowWatermark, r)
	})
}

func TestComputeCoverage_MustNotDegrade(t *testing.T) {
	t.Run("a fresh install serving the embedded default", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		d, r := computeCoverage(servingFacts{Embedded: true}, f.st, f.policy, 100)
		assert.False(t, d)
		assert.Equal(t, "", r)
	})

	t.Run("a nil state is a fresh install", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		d, _ := computeCoverage(servingFacts{Embedded: true}, nil, f.policy, 100)
		assert.False(t, d)
	})

	t.Run("embedded at or above the publisher watermark after the external file was removed", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 150}))
		// A binary upgrade shipped a newer corpus; the old external file is gone.
		d, _ := computeCoverage(servingFacts{Embedded: true}, f.st, f.policy, 150)
		assert.False(t, d, "equal to the watermark")
		d, _ = computeCoverage(servingFacts{Embedded: true}, f.st, f.policy, 200)
		assert.False(t, d, "above the watermark")
	})

	t.Run("operator root declared but nothing ever activated", func(t *testing.T) {
		f := newPipelineFixture(t, 100, opRoot(10))
		d, _ := computeCoverage(servingFacts{Embedded: true}, f.st, f.policy, 100)
		assert.False(t, d)
	})

	t.Run("operator watermark 900 against an embedded publisher corpus at 100", func(t *testing.T) {
		f := newPipelineFixture(t, 100, opRoot(10))
		f.activate(signedCandidate(t, keyOperator, bundleOpts{Seq: 900}))
		// Operator authority serving its own baselined bundle: full coverage.
		d, _ := computeCoverage(servingFacts{Verified: true, Authority: AuthorityOperator}, f.st, f.policy, 100)
		assert.False(t, d)
		// Publisher root declared (operator bundle deliberately switched away from):
		// the embedded fallback is compared only to the PUBLISHER watermark.
		f.repolicy(func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(10) })
		d, _ = computeCoverage(servingFacts{Embedded: true}, f.st, f.policy, 100)
		assert.False(t, d, "no cross-authority comparison anywhere")
	})

	t.Run("a verified publisher bundle", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 150}))
		d, _ := computeCoverage(servingFacts{Verified: true, Authority: AuthorityPublisher}, f.st, f.policy, 100)
		assert.False(t, d)
	})

	t.Run("a missing publisher bundle never triggers operator_bundle_missing", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		f.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 100 + 1}))
		d, _ := computeCoverage(servingFacts{Embedded: true}, f.st, f.policy, 101)
		assert.False(t, d)
	})

	t.Run("an invalid active root follows the same four rules", func(t *testing.T) {
		f := newPipelineFixture(t, 100, func(s *config.SecurityConfig) { s.TPAActiveRoot = "bogus" })
		d, _ := computeCoverage(servingFacts{Embedded: true}, f.st, f.policy, 100)
		assert.False(t, d, "embedded fallback on a fresh state is full coverage")
	})
}

func TestStateLogicTestsDoNotLeakAcrossFixtures(t *testing.T) {
	// A guard for the helper itself: two fixtures never share state.
	a := newPipelineFixture(t, 100, nil)
	b := newPipelineFixture(t, 100, nil)
	a.activate(signedCandidate(t, keyPublisher, bundleOpts{Seq: 150}))
	assert.Equal(t, uint64(100), b.auth(AuthorityPublisher).Watermark)
	assert.False(t, strings.Contains(a.policy.Fingerprint, " "))
}
