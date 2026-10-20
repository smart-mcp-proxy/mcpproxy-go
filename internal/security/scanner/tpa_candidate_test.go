package scanner

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/bundlesig"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// countCompiles installs the compile counter seam for one test.
func countCompiles(t *testing.T) *int {
	t.Helper()
	n := 0
	bundleCompileHook = func() { n++ }
	t.Cleanup(func() { bundleCompileHook = nil })
	return &n
}

// withSelfCheck installs the activation self-check hook for one test.
func withSelfCheck(t *testing.T, fn func(*BundleCheck) error) {
	t.Helper()
	prev := bundleSelfCheck
	bundleSelfCheck = fn
	t.Cleanup(func() { bundleSelfCheck = prev })
}

// flipByte returns a copy of b with the byte at the first occurrence of marker
// altered so the result is still valid JSON of the same length.
func flipByte(t *testing.T, b []byte, marker string) []byte {
	t.Helper()
	i := bytes.Index(b, []byte(marker))
	require.GreaterOrEqual(t, i, 0)
	out := append([]byte(nil), b...)
	out[i] ^= 0x01 // 'b' -> 'c', 'x' -> 'y': still printable ASCII inside a string
	require.NotEqual(t, b, out)
	return out
}

// --- T060: stage order -------------------------------------------------------

// TestEvaluateCandidate_StageOrder drives every FR-008 stage with a candidate
// that ALSO fails every later stage it can, and requires the earliest stage's
// reason. A reordering of any two adjacent stages flips one of these.
func TestEvaluateCandidate_StageOrder(t *testing.T) {
	// Publisher baseline 100 (embedded sequence), publisher epoch 0.
	f := newPipelineFixture(t, 100, nil)
	garbage := []byte("this is not json at all")

	t.Run("sidecar grammar beats everything", func(t *testing.T) {
		f.refuse(Candidate{Bundle: garbage, Sidecar: []byte("{}"), SidecarPresent: true}, RejectSidecarInvalid)
	})

	t.Run("sidecar byte cap beats bundle handling", func(t *testing.T) {
		big := append(bundlesig.SignSidecar(keyPublisher.priv, garbage), bytes.Repeat([]byte(" "), MaxSidecarBytes)...)
		rej := f.refuse(Candidate{Bundle: garbage, Sidecar: big, SidecarPresent: true}, RejectCeilingExceeded)
		assert.Contains(t, rej.Detail, "sidecar")
	})

	t.Run("bundle byte ceiling beats the key lookup", func(t *testing.T) {
		huge := bytes.Repeat([]byte("a"), MaxBundleBytes+1)
		rej := f.refuse(Candidate{Bundle: huge, Sidecar: signBundle(t, keyUntrusted.priv, huge), SidecarPresent: true}, RejectCeilingExceeded)
		assert.Contains(t, rej.Detail, "bundle")
	})

	t.Run("key lookup beats parsing", func(t *testing.T) {
		rej := f.refuse(Candidate{Bundle: garbage, Sidecar: signBundle(t, keyUntrusted.priv, garbage), SidecarPresent: true}, RejectKeyNotTrusted)
		assert.Contains(t, rej.Detail, keyUntrusted.id)
	})

	t.Run("signature beats parsing", func(t *testing.T) {
		sc := signBundle(t, keyPublisher.priv, []byte("something else"))
		f.refuse(Candidate{Bundle: garbage, Sidecar: sc, SidecarPresent: true}, RejectTamper)
	})

	t.Run("authority beats parsing", func(t *testing.T) {
		rej := f.refuse(Candidate{Bundle: garbage, Sidecar: signBundle(t, keyOperator.priv, garbage), SidecarPresent: true}, RejectAuthorityNotActive)
		assert.Contains(t, rej.Detail, AuthorityOperator)
		assert.Contains(t, rej.Detail, keyOperator.id)
		assert.Contains(t, rej.Detail, "active root is publisher")
	})

	t.Run("manifest parse", func(t *testing.T) {
		f.refuse(Candidate{Bundle: garbage, Sidecar: signBundle(t, keyPublisher.priv, garbage), SidecarPresent: true}, RejectInvalidBundle)
	})

	t.Run("rule ceiling beats the sequence grammar", func(t *testing.T) {
		// 2001 rules AND no sequence at all.
		c := signedCandidate(t, keyPublisher, bundleOpts{Rules: MaxBundleRules + 1})
		rej := f.refuse(c, RejectCeilingExceeded)
		assert.Contains(t, rej.Detail, "rules")
	})

	t.Run("sequence grammar beats key id agreement", func(t *testing.T) {
		c := signedCandidate(t, keyPublisher, bundleOpts{SeqRaw: "150", KeyID: keyOperator.id})
		f.refuse(c, RejectInvalidBundle)
	})

	t.Run("epoch grammar beats key id agreement", func(t *testing.T) {
		c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, OmitEpoch: true, KeyID: keyOperator.id})
		f.refuse(c, RejectInvalidBundle)
	})

	t.Run("key id agreement beats the epoch comparison", func(t *testing.T) {
		c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, Epoch: 9, KeyID: keyOperator.id})
		f.refuse(c, RejectTamper)
	})

	t.Run("epoch beats the watermark", func(t *testing.T) {
		// epoch ahead AND below the baseline (150 vs 100 is fine; use 50).
		c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 50, Epoch: 1})
		f.refuse(c, RejectEpochAhead)
	})

	t.Run("deny-list beats the watermark", func(t *testing.T) {
		c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 50})
		g := newPipelineFixture(t, 100, nil)
		g.st.Authorities[AuthorityPublisher].DenyList = []TPABundleDenyEntry{{Sequence: 50, FullDigest: digestOf(c.Bundle)}}
		g.refuse(c, RejectDenyListed)
	})

	t.Run("watermark beats version", func(t *testing.T) {
		c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 50, Version: "9.9.0"})
		f.refuse(c, RejectDowngrade)
	})

	t.Run("version beats compile", func(t *testing.T) {
		n := countCompiles(t)
		c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, Version: "9.9.0", BadPattern: true})
		f.refuse(c, RejectInvalidBundle)
		assert.Zero(t, *n, "an unsupported version is refused before any pattern compiles")
	})

	t.Run("compile failure", func(t *testing.T) {
		f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, BadPattern: true}), RejectInvalidBundle)
	})

	t.Run("no runnable rules", func(t *testing.T) {
		rej := f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, NoRunnable: true}), RejectInvalidBundle)
		assert.Contains(t, rej.Detail, "no runnable rules")
	})

	t.Run("compile failure beats the self-check", func(t *testing.T) {
		called := false
		withSelfCheck(t, func(*BundleCheck) error { called = true; return errors.New("nope") })
		f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, BadPattern: true}), RejectInvalidBundle)
		assert.False(t, called)
	})

	t.Run("self-check last", func(t *testing.T) {
		withSelfCheck(t, func(*BundleCheck) error { return errors.New("canary tool flagged clean") })
		rej := f.refuse(signedCandidate(t, keyPublisher, bundleOpts{Seq: 150}), RejectGateFailure)
		assert.Contains(t, rej.Detail, "canary tool flagged clean")
	})

	t.Run("a candidate that clears every stage is accepted", func(t *testing.T) {
		acc, rej := f.eval(signedCandidate(t, keyPublisher, bundleOpts{Seq: 150}))
		require.Nil(t, rej)
		require.NotNil(t, acc)
		assert.Equal(t, uint64(150), acc.Sequence)
	})
}

func TestEvaluateCandidate_UnsignedStageOrder(t *testing.T) {
	garbage := Candidate{Bundle: []byte("not json"), Source: BundleSourceFile}

	t.Run("require_signed_bundle beats the ratchet and the parse", func(t *testing.T) {
		f := newPipelineFixture(t, 100, func(s *config.SecurityConfig) { s.RequireSignedBundle = true })
		f.st.SignatureRatchet = true
		f.refuse(garbage, RejectSignatureRequired)
	})

	t.Run("ratchet beats the parse", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		f.st.SignatureRatchet = true
		f.refuse(garbage, RejectUnsignedRatchet)
	})

	t.Run("parse", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		f.refuse(garbage, RejectInvalidBundle)
	})

	t.Run("rule ceiling beats version", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		rej := f.refuse(unsignedCandidate(t, bundleOpts{Rules: MaxBundleRules + 1, Version: "9.9.0"}), RejectCeilingExceeded)
		assert.Contains(t, rej.Detail, "2001")
	})

	t.Run("version, compile, runnable", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		f.refuse(unsignedCandidate(t, bundleOpts{Version: "9.9.0"}), RejectInvalidBundle)
		f.refuse(unsignedCandidate(t, bundleOpts{BadPattern: true}), RejectInvalidBundle)
		f.refuse(unsignedCandidate(t, bundleOpts{NoRunnable: true}), RejectInvalidBundle)
	})

	t.Run("self-check", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		withSelfCheck(t, func(*BundleCheck) error { return errors.New("boom") })
		f.refuse(unsignedCandidate(t, bundleOpts{}), RejectGateFailure)
	})

	t.Run("the byte ceiling applies to unsigned candidates too", func(t *testing.T) {
		f := newPipelineFixture(t, 100, nil)
		f.refuse(Candidate{Bundle: bytes.Repeat([]byte("a"), MaxBundleBytes+1), Source: BundleSourceFile}, RejectCeilingExceeded)
	})
}

// An unsigned candidate is accepted unverified, with NO sequence or epoch: its
// manifest is attacker-chosen, so nothing in it may reach the watermark.
func TestEvaluateCandidate_UnsignedIgnoresManifestSequence(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)
	c := unsignedCandidate(t, bundleOpts{SeqRaw: `"18446744073709551615"`, Epoch: 99, KeyID: keyPublisher.id})
	acc, rej := f.eval(c)
	require.Nil(t, rej)
	require.NotNil(t, acc)

	assert.False(t, acc.Verified)
	assert.Equal(t, "", acc.Authority)
	assert.Equal(t, uint64(0), acc.Sequence)
	assert.Equal(t, uint64(0), acc.TrustEpoch)
	assert.Equal(t, "", acc.KeyID)
	assert.Equal(t, digestOf(c.Bundle), acc.FullDigest)
	assert.Equal(t, "", acc.SidecarDigest)
}

// A sidecar that is PRESENT but unusable is a rejection; it must never fall
// back to the unsigned path, even with require_signed_bundle off (tasks.md A7).
func TestEvaluateCandidate_PresentButInvalidSidecarNeverFallsBackToUnsigned(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)
	good := buildBundle(t, bundleOpts{Seq: 150, KeyID: keyPublisher.id})

	for name, sidecar := range map[string][]byte{
		"empty":     nil,
		"garbage":   []byte("garbage"),
		"empty obj": []byte("{}"),
		"truncated": bundlesig.SignSidecar(keyPublisher.priv, good)[:40],
	} {
		t.Run(name, func(t *testing.T) {
			acc, rej := f.eval(Candidate{Bundle: good, Sidecar: sidecar, SidecarPresent: true, Source: BundleSourceFile})
			assert.Nil(t, acc)
			require.NotNil(t, rej)
			assert.Equal(t, RejectSidecarInvalid, rej.Reason)
			assert.NotEmpty(t, rej.Detail)
		})
	}
}

// --- T060: verify-before-parse, read-once, SC-001 tamper classes -------------

func TestEvaluateCandidate_TamperClasses_NeverCompileAnything(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)
	good := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, Tag: "bbb"})
	other := buildBundle(t, bundleOpts{Seq: 151, KeyID: keyPublisher.id})

	forged := strings.Replace(string(signBundle(t, keyUntrusted.priv, good.Bundle)), keyUntrusted.id, keyPublisher.id, 1)
	renamedManifest := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, KeyID: keyOperator.id})

	cases := []struct {
		name   string
		cand   Candidate
		reason string
	}{
		{"flipped byte in the bundle", Candidate{Bundle: flipByte(t, good.Bundle, "bundle-marker"), Sidecar: good.Sidecar, SidecarPresent: true}, RejectTamper},
		{"signature over a different bundle", Candidate{Bundle: good.Bundle, Sidecar: signBundle(t, keyPublisher.priv, other), SidecarPresent: true}, RejectTamper},
		{"forged signature under a trusted key id", Candidate{Bundle: good.Bundle, Sidecar: []byte(forged), SidecarPresent: true}, RejectTamper},
		{"signature from an untrusted key", Candidate{Bundle: good.Bundle, Sidecar: signBundle(t, keyUntrusted.priv, good.Bundle), SidecarPresent: true}, RejectKeyNotTrusted},
		{"manifest names another key", renamedManifest, RejectTamper},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := countCompiles(t)
			tc.cand.Source = BundleSourceFile
			f.refuse(tc.cand, tc.reason)
			assert.Zero(t, *n, "no pattern may be compiled for a candidate that failed verification")
		})
	}

	t.Run("the untampered original compiles exactly its rules", func(t *testing.T) {
		n := countCompiles(t)
		_, rej := f.eval(good)
		require.Nil(t, rej)
		assert.Equal(t, 1, *n)
	})
}

// FR-006: the bytes that were verified are the bytes that are parsed. A
// candidate carries its bytes; the path is only a label and is never re-read.
func TestEvaluateCandidate_ReadsOnce(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)
	c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, Tag: "verified"})

	// A different, perfectly valid, perfectly signed bundle sits at the path.
	decoy := signedCandidate(t, keyPublisher, bundleOpts{Seq: 160, Tag: "decoy"})
	dir := t.TempDir()
	c.Path = filepath.Join(dir, "scanner-bundle.json")
	require.NoError(t, os.WriteFile(c.Path, decoy.Bundle, 0o600))
	require.NoError(t, os.WriteFile(c.Path+".sig", decoy.Sidecar, 0o600))

	acc, rej := f.eval(c)
	require.Nil(t, rej)
	assert.Equal(t, digestOf(c.Bundle), acc.FullDigest)
	assert.Equal(t, uint64(150), acc.Sequence, "the sequence is the VERIFIED buffer's, not the file's")

	hits := acc.Check.Inspect(detect.ToolView{Description: "bundle-marker-150-verified"}, detect.RegistryView{})
	assert.NotEmpty(t, hits, "the compiled rules are the verified buffer's")
	none := acc.Check.Inspect(detect.ToolView{Description: "bundle-marker-160-decoy"}, detect.RegistryView{})
	assert.Empty(t, none, "the decoy file at the path was never read")
}

// An accepted signed candidate reports everything the runtime publishes.
func TestEvaluateCandidate_AcceptedSignedFields(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)
	c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, Epoch: 0, GeneratedAt: "2026-10-01T00:00:00Z"})

	acc, rej := f.eval(c)
	require.Nil(t, rej)
	assert.True(t, acc.Verified)
	assert.Equal(t, AuthorityPublisher, acc.Authority)
	assert.Equal(t, uint64(150), acc.Sequence)
	assert.Equal(t, uint64(0), acc.TrustEpoch)
	assert.Equal(t, keyPublisher.id, acc.KeyID)
	assert.Equal(t, digestOf(c.Bundle), acc.FullDigest)
	assert.Equal(t, digestOf(c.Sidecar), acc.SidecarDigest)
	assert.Equal(t, "0.1.0", acc.Meta.BundleVersion)
	assert.Equal(t, "2026-10-01T00:00:00Z", acc.Meta.GeneratedAt)
	assert.Equal(t, 1, acc.Stats.Runnable)
	require.NotNil(t, acc.Check)
}

func TestEvaluateCandidate_RejectionIsTimestamped(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)
	_, rej := f.eval(Candidate{Bundle: []byte("x"), Sidecar: []byte("{}"), SidecarPresent: true})
	require.NotNil(t, rej)
	assert.True(t, rej.At.Equal(fixedNow), "the injected clock stamps the rejection")
}

// --- T061: ceilings, both sides ------------------------------------------------

func TestEvaluateCandidate_BundleByteCeiling(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)

	at := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, PadTo: MaxBundleBytes})
	require.Len(t, at.Bundle, MaxBundleBytes)
	acc, rej := f.eval(at)
	require.Nil(t, rej, "a bundle of exactly 8 MiB loads")
	require.NotNil(t, acc)

	over := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, PadTo: MaxBundleBytes + 1})
	require.Len(t, over.Bundle, MaxBundleBytes+1)
	rej2 := f.refuse(over, RejectCeilingExceeded)
	assert.Contains(t, rej2.Detail, "bundle")

	uat := unsignedCandidate(t, bundleOpts{PadTo: MaxBundleBytes})
	_, rej = f.eval(uat)
	assert.Nil(t, rej, "unsigned: exactly at the ceiling loads")
	f.refuse(unsignedCandidate(t, bundleOpts{PadTo: MaxBundleBytes + 1}), RejectCeilingExceeded)
}

func TestEvaluateCandidate_RuleCountCeiling(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)

	at := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, Rules: MaxBundleRules})
	acc, rej := f.eval(at)
	require.Nil(t, rej, "2000 rules load")
	assert.Equal(t, MaxBundleRules, acc.Stats.Runnable)

	n := countCompiles(t)
	over := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, Rules: MaxBundleRules + 1})
	rej2 := f.refuse(over, RejectCeilingExceeded)
	assert.Equal(t, "rules: 2001 > 2000", rej2.Detail)
	assert.Zero(t, *n, "the ceiling is enforced before anything is compiled")
}

func TestEvaluateCandidate_SidecarByteCeiling(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)
	c := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150})

	pad := func(n int) []byte {
		out := append([]byte(nil), c.Sidecar...)
		return append(out, bytes.Repeat([]byte(" "), n-len(out))...)
	}

	at := c
	at.Sidecar = pad(MaxSidecarBytes)
	require.Len(t, at.Sidecar, 4096)
	_, rej := f.eval(at)
	require.Nil(t, rej, "a 4096-byte sidecar loads")

	over := c
	over.Sidecar = pad(MaxSidecarBytes + 1)
	require.Len(t, over.Sidecar, 4097)
	rej2 := f.refuse(over, RejectCeilingExceeded)
	assert.Contains(t, rej2.Detail, "sidecar")
}

// --- T062: manifest grammar ----------------------------------------------------

func TestParseSequence(t *testing.T) {
	valid := map[string]uint64{
		`"1"`:                    1,
		`"9"`:                    9,
		`"10"`:                   10,
		`"18446744073709551615"`: 18446744073709551615,
	}
	for raw, want := range valid {
		t.Run("accept "+raw, func(t *testing.T) {
			got, s, err := parseSequence([]byte(raw))
			require.NoError(t, err)
			assert.Equal(t, want, got)
			assert.Equal(t, strings.Trim(raw, `"`), s)
		})
	}

	invalid := map[string]string{
		"missing":             ``,
		"null":                `null`,
		"zero":                `"0"`,
		"leading zero":        `"01"`,
		"plus":                `"+1"`,
		"leading space":       `" 1"`,
		"trailing space":      `"1 "`,
		"exponent":            `"1e3"`,
		"empty":               `""`,
		"hex":                 `"0x10"`,
		"negative":            `"-1"`,
		"fraction":            `"1.0"`,
		"bare number":         `5`,
		"bare big number":     `18446744073709551615`,
		"bool":                `true`,
		"object":              `{}`,
		"array":               `["1"]`,
		"21 digits":           `"100000000000000000000"`,
		"2^64":                `"18446744073709551616"`,
		"20 digits >2^64":     `"99999999999999999999"`,
		"unicode digit":       `"١"`,
		"escaped non-grammar": `"0"`,
	}
	for name, raw := range invalid {
		t.Run("reject "+name, func(t *testing.T) {
			_, _, err := parseSequence([]byte(raw))
			assert.Error(t, err)
		})
	}
}

func TestParseTrustEpoch(t *testing.T) {
	valid := map[string]uint64{`0`: 0, `1`: 1, `42`: 42, `18446744073709551615`: 18446744073709551615}
	for raw, want := range valid {
		t.Run("accept "+raw, func(t *testing.T) {
			got, err := parseTrustEpoch([]byte(raw))
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
	for name, raw := range map[string]string{
		"missing":  ``,
		"null":     `null`,
		"negative": `-1`,
		"fraction": `1.0`,
		"exponent": `1e2`,
		"string":   `"1"`,
		"plus":     `+1`,
		"2^64":     `18446744073709551616`,
		"bool":     `false`,
		"minus 0":  `-0`,
		"array":    `[0]`,
		"hex":      `0x1`,
	} {
		t.Run("reject "+name, func(t *testing.T) {
			_, err := parseTrustEpoch([]byte(raw))
			assert.Error(t, err)
		})
	}
}

func TestEvaluateCandidate_ManifestGrammar(t *testing.T) {
	f := newPipelineFixture(t, 100, nil)

	for name, o := range map[string]bundleOpts{
		"bare-number sequence":  {SeqRaw: "150"},
		"missing sequence":      {},
		"sequence 0":            {SeqRaw: `"0"`},
		"sequence leading zero": {SeqRaw: `"0150"`},
		"sequence 2^64":         {SeqRaw: `"18446744073709551616"`},
		"missing trust_epoch":   {Seq: 150, OmitEpoch: true},
		"fractional epoch":      {Seq: 150, EpochRaw: "0.0"},
		"string epoch":          {Seq: 150, EpochRaw: `"0"`},
		"negative epoch":        {Seq: 150, EpochRaw: "-1"},
	} {
		t.Run(name, func(t *testing.T) {
			f.refuse(signedCandidate(t, keyPublisher, o), RejectInvalidBundle)
		})
	}

	t.Run("max sequence is a valid sequence", func(t *testing.T) {
		acc, rej := f.eval(signedCandidate(t, keyPublisher, bundleOpts{SeqRaw: `"18446744073709551615"`}))
		require.Nil(t, rej)
		assert.Equal(t, uint64(18446744073709551615), acc.Sequence)
	})

	t.Run("manifest key_id must be present and agree (row 13)", func(t *testing.T) {
		missing := buildBundle(t, bundleOpts{Seq: 150})
		rej := f.refuse(Candidate{Bundle: missing, Sidecar: signBundle(t, keyPublisher.priv, missing), SidecarPresent: true}, RejectTamper)
		assert.Contains(t, rej.Detail, bundlesig.CodeKeyIDMismatch)

		wrong := signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, KeyID: keyUntrusted.id})
		rej = f.refuse(wrong, RejectTamper)
		assert.Contains(t, rej.Detail, bundlesig.CodeKeyIDMismatch)
	})

	t.Run("unknown additive manifest keys are ignored", func(t *testing.T) {
		_, rej := f.eval(signedCandidate(t, keyPublisher, bundleOpts{Seq: 150, PadTo: 4096}))
		assert.Nil(t, rej)
	})
}

// A rule-count error from the shared compile path is also what existing
// loadBundleCheck callers see.
func TestLoadBundleCheck_RuleCeilingForExistingCallers(t *testing.T) {
	n := countCompiles(t)
	_, _, err := loadBundleCheck(buildBundle(t, bundleOpts{Rules: MaxBundleRules + 1}))
	require.Error(t, err)
	var ce *bundleCeilingError
	assert.True(t, errors.As(err, &ce))
	assert.Zero(t, *n)

	_, stats, err := loadBundleCheck(buildBundle(t, bundleOpts{Rules: MaxBundleRules}))
	require.NoError(t, err)
	assert.Equal(t, MaxBundleRules, stats.Runnable)
}

// SC-010 guard: the collision template really produces two bundles sharing a
// 12-hex fingerprint and nothing else. If the template drifts this fails first,
// before any rollback test silently loses its meaning.
func TestSC010CollisionFixture(t *testing.T) {
	a, b := sc010Bundle(sc010NonceA), sc010Bundle(sc010NonceB)
	assert.Equal(t, sc010DigestA, digestOf(a))
	assert.Equal(t, sc010DigestB, digestOf(b))
	assert.Equal(t, sc010Fingerprint, digestOf(a)[:12])
	assert.Equal(t, sc010Fingerprint, digestOf(b)[:12])
	assert.NotEqual(t, a, b)
	assert.Equal(t, keyPublisher.id, sc010KeyID, "the template names the publisher test key")

	// Both are genuinely valid publisher-signed bundles at sequence 5.
	f := newPipelineFixture(t, 0, nil)
	for _, raw := range [][]byte{a, b} {
		acc, rej := f.eval(Candidate{Bundle: raw, Sidecar: signBundle(t, keyPublisher.priv, raw), SidecarPresent: true, Source: BundleSourceFile})
		require.Nil(t, rej)
		assert.Equal(t, uint64(5), acc.Sequence)
	}
}
