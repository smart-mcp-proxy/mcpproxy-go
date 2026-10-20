package scanner

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestBuildTrustSet_UnionOfAuthorities(t *testing.T) {
	ts, warnings := buildTrustSet([]string{keyPublisher.b64}, []string{keyOperator.b64})
	require.Empty(t, warnings)

	pk, ok := ts.Lookup(keyPublisher.id)
	require.True(t, ok)
	assert.Equal(t, AuthorityPublisher, pk.Authority)

	ok2, found := ts.Lookup(keyOperator.id)
	require.True(t, found)
	assert.Equal(t, AuthorityOperator, ok2.Authority)

	_, found = ts.Lookup(keyUntrusted.id)
	assert.False(t, found)
}

func TestBuildTrustSet_DropsBadOperatorSignersWithoutEchoingThem(t *testing.T) {
	const garbage = "not-base64-@@@-SECRETISH"
	short := "AAAA" // valid base64, 3 bytes
	ts, warnings := buildTrustSet(nil, []string{garbage, short, keyOperator.b64, keyOperator.b64})

	_, ok := ts.Lookup(keyOperator.id)
	require.True(t, ok, "the one valid operator key is kept")
	require.Len(t, warnings, 3, "garbage, short and the duplicate each produce one warning")
	for _, w := range warnings {
		assert.NotContains(t, w, garbage, "a warning must not echo the offending value")
		assert.NotContains(t, w, short)
	}
}

func TestBuildTrustSet_KeyInBothSetsKeepsThePublisherCopy(t *testing.T) {
	ts, warnings := buildTrustSet([]string{keyPublisher.b64}, []string{keyPublisher.b64, keyOperator.b64})
	require.Len(t, warnings, 1)

	k, ok := ts.Lookup(keyPublisher.id)
	require.True(t, ok)
	assert.Equal(t, AuthorityPublisher, k.Authority, "a key can never satisfy the operator root as well")

	op, ok := ts.Lookup(keyOperator.id)
	require.True(t, ok)
	assert.Equal(t, AuthorityOperator, op.Authority)
}

func TestBuildTrustSet_DropsMalformedAndDuplicatePublisherSigners(t *testing.T) {
	ts, warnings := buildTrustSet([]string{"@@@", keyPublisher.b64, keyPublisher.b64}, nil)
	require.Len(t, warnings, 2)
	_, ok := ts.Lookup(keyPublisher.id)
	assert.True(t, ok)
}

func TestPolicyFromConfig_Defaults(t *testing.T) {
	withTrustAnchors(t, []ed25519.PublicKey{keyPublisher.pub}, 3, buildBundle(t, bundleOpts{Seq: 42}))
	p := policyFor(&config.SecurityConfig{}, "")

	assert.False(t, p.RequireSigned)
	assert.Equal(t, AuthorityPublisher, p.ActiveRoot)
	assert.False(t, p.OperatorBaselineSet)
	assert.Equal(t, uint64(0), p.OperatorEpoch)
	assert.Equal(t, uint64(3), p.currentEpoch(AuthorityPublisher))
	assert.Equal(t, uint64(0), p.currentEpoch(AuthorityOperator))
	assert.Equal(t, uint64(0), p.currentEpoch("nobody"))
	require.NotNil(t, p.Trust)
	_, ok := p.Trust.Lookup(keyPublisher.id)
	assert.True(t, ok)
	assert.Len(t, p.Fingerprint, 64)
}

func TestPolicy_Baselines(t *testing.T) {
	t.Run("publisher baseline is the embedded sequence", func(t *testing.T) {
		withTrustAnchors(t, nil, 0, buildBundle(t, bundleOpts{Seq: 42}))
		p := policyFor(&config.SecurityConfig{}, "")
		b, ok := p.baseline(AuthorityPublisher)
		assert.True(t, ok)
		assert.Equal(t, uint64(42), b)
		seq, has := embeddedSequence()
		assert.True(t, has)
		assert.Equal(t, uint64(42), seq)
	})

	t.Run("publisher baseline falls back when the embedded manifest has no sequence", func(t *testing.T) {
		withTrustAnchors(t, nil, 0, buildBundle(t, bundleOpts{}))
		publisherFallbackBaseline = 7
		p := policyFor(&config.SecurityConfig{}, "")
		b, ok := p.baseline(AuthorityPublisher)
		assert.True(t, ok, "the publisher always has a baseline")
		assert.Equal(t, uint64(7), b)
		_, has := embeddedSequence()
		assert.False(t, has)
	})

	t.Run("operator baseline only when configured", func(t *testing.T) {
		withTrustAnchors(t, nil, 0, nil)
		p := policyFor(&config.SecurityConfig{}, "")
		_, ok := p.baseline(AuthorityOperator)
		assert.False(t, ok, "no silent zero")

		p = policyFor(&config.SecurityConfig{TPAOperatorBaseline: u64p(0)}, "")
		b, ok := p.baseline(AuthorityOperator)
		assert.True(t, ok, "an explicit 0 is a declared baseline")
		assert.Equal(t, uint64(0), b)

		p = policyFor(&config.SecurityConfig{TPAOperatorBaseline: u64p(9), TPAOperatorEpoch: u64p(4)}, "")
		b, _ = p.baseline(AuthorityOperator)
		assert.Equal(t, uint64(9), b)
		assert.Equal(t, uint64(4), p.currentEpoch(AuthorityOperator))
	})
}

func TestPolicyFromConfig_PathEnvOverrideOnlyOnTheConfigEntryPoint(t *testing.T) {
	withTrustAnchors(t, nil, 0, nil)
	t.Setenv(config.EnvTPABundlePath, "/env/bundle.json")
	sec := &config.SecurityConfig{TPABundlePath: "/file/bundle.json"}

	assert.Equal(t, "/env/bundle.json", policyFromConfig(sec).Path, "the env override wins on the config entry point")
	assert.Equal(t, "/explicit.json", policyFor(sec, "/explicit.json").Path, "an explicit path is taken verbatim")
}

func TestPolicyFingerprint(t *testing.T) {
	withTrustAnchors(t, []ed25519.PublicKey{keyPublisher.pub}, 0, buildBundle(t, bundleOpts{Seq: 5}))
	base := func() *config.SecurityConfig {
		return &config.SecurityConfig{TPAOperatorSigners: []string{keyOperator.b64}}
	}
	fp := func(mut func(*config.SecurityConfig)) string {
		sec := base()
		if mut != nil {
			mut(sec)
		}
		return policyFor(sec, "/p").Fingerprint
	}

	ref := fp(nil)
	assert.Equal(t, ref, fp(nil), "deterministic")

	differs := map[string]func(*config.SecurityConfig){
		"require signed":        func(s *config.SecurityConfig) { s.RequireSignedBundle = true },
		"active root":           func(s *config.SecurityConfig) { s.TPAActiveRoot = config.TPARootOperator },
		"operator signers":      func(s *config.SecurityConfig) { s.TPAOperatorSigners = nil },
		"operator baseline 0":   func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(0) },
		"operator baseline 1":   func(s *config.SecurityConfig) { s.TPAOperatorBaseline = u64p(1) },
		"operator epoch":        func(s *config.SecurityConfig) { s.TPAOperatorEpoch = u64p(2) },
		"extra operator signer": func(s *config.SecurityConfig) { s.TPAOperatorSigners = append(s.TPAOperatorSigners, keyUntrusted.b64) },
		// Same cardinality, different key: only the per-key ids tell these
		// apart (an operator rotating or revoking one signer).
		"swapped operator signer": func(s *config.SecurityConfig) { s.TPAOperatorSigners = []string{keyUntrusted.b64} },
	}
	seen := map[string]string{"ref": ref}
	for name, mut := range differs {
		got := fp(mut)
		assert.NotEqual(t, ref, got, name)
		for prev, v := range seen {
			assert.NotEqual(t, v, got, "%s collides with %s", name, prev)
		}
		seen[name] = got
	}
	assert.NotEqual(t, policyFor(base(), "/p").Fingerprint, policyFor(base(), "/q").Fingerprint, "the path is part of the key")

	// Key order is not significant.
	a := fp(func(s *config.SecurityConfig) { s.TPAOperatorSigners = []string{keyOperator.b64, keyUntrusted.b64} })
	b := fp(func(s *config.SecurityConfig) { s.TPAOperatorSigners = []string{keyUntrusted.b64, keyOperator.b64} })
	assert.Equal(t, a, b)

	// The binary's own anchors are part of the key as well.
	withTrustAnchors(t, []ed25519.PublicKey{keyPublisher.pub}, 1, buildBundle(t, bundleOpts{Seq: 5}))
	assert.NotEqual(t, ref, fp(nil), "publisher epoch")
	withTrustAnchors(t, []ed25519.PublicKey{keyPublisher.pub}, 0, buildBundle(t, bundleOpts{Seq: 6}))
	assert.NotEqual(t, ref, fp(nil), "publisher baseline")
}

func TestPolicy_ActiveRootIsKeptVerbatim(t *testing.T) {
	p := policyFor(&config.SecurityConfig{TPAActiveRoot: "bogus"}, "")
	assert.Equal(t, "bogus", p.ActiveRoot)
}

// The trust set built for a policy must hand bundlesig keys of the right
// authority, so Verify's result can be compared against ActiveRoot.
func TestPolicy_TrustSetAuthorities(t *testing.T) {
	withTrustAnchors(t, []ed25519.PublicKey{keyPublisher.pub}, 0, nil)
	p := testPolicy(nil)

	k, ok := p.Trust.Lookup(keyOperator.id)
	require.True(t, ok)
	assert.Equal(t, AuthorityOperator, k.Authority)
	assert.False(t, strings.Contains(strings.Join(p.Warnings, " "), keyOperator.b64))
}
