package scanner

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/bundlesig"
)

// Shared Spec 101 test helpers. The Wave 2 runtime tests (tpa_bundle_runtime*,
// rollback, crash points) build on exactly these, so keys are DETERMINISTIC:
// the same seeds always yield the same key ids, which is what lets the SC-010
// collision fixture below name its signing key in advance.

// testKey is one deterministic Ed25519 identity.
type testKey struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	// id is bundlesig.KeyID(pub): the lowercase-hex SHA-256 of the public key.
	id string
	// b64 is the standard padded base64 of the raw public key, the form
	// publisherSigners and security.tpa_operator_signers carry.
	b64 string
}

func newTestKey(seedByte byte) testKey {
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seedByte}, ed25519.SeedSize))
	pub := priv.Public().(ed25519.PublicKey)
	return testKey{priv: priv, pub: pub, id: bundlesig.KeyID(pub), b64: base64.StdEncoding.EncodeToString(pub)}
}

var (
	// keyPublisher is the publisher test key (seed 0x11).
	keyPublisher = newTestKey(0x11)
	// keyOperator is the operator test key (seed 0x22).
	keyOperator = newTestKey(0x22)
	// keyUntrusted is a key that is in NO trust set (seed 0x33).
	keyUntrusted = newTestKey(0x33)
)

// withTrustAnchors installs publisher trust anchors for one test: the publisher
// signer set, the publisher epoch and (when embedded is non-nil) the embedded
// default bundle. Everything is restored on cleanup.
func withTrustAnchors(t *testing.T, publisherKeys []ed25519.PublicKey, publisherEpoch uint64, embedded []byte) {
	t.Helper()
	prevSigners, prevEpoch, prevBase, prevEmbedded := publisherSigners, publisherTrustEpoch, publisherFallbackBaseline, bundledScannerBundle
	t.Cleanup(func() {
		publisherSigners, publisherTrustEpoch, publisherFallbackBaseline, bundledScannerBundle = prevSigners, prevEpoch, prevBase, prevEmbedded
	})
	signers := make([]string, 0, len(publisherKeys))
	for _, k := range publisherKeys {
		signers = append(signers, base64.StdEncoding.EncodeToString(k))
	}
	publisherSigners = signers
	publisherTrustEpoch = publisherEpoch
	if embedded != nil {
		bundledScannerBundle = embedded
	}
}

// bundleOpts renders one minimal valid manifest. Zero values mean "a normal
// signed-release manifest"; the Raw/Omit/Bad fields break exactly one thing.
type bundleOpts struct {
	// Seq is the manifest sequence rendered as a decimal STRING; 0 omits it.
	Seq uint64
	// SeqRaw, when non-empty, is the raw JSON value of "sequence" (e.g. a bare
	// number) and overrides Seq.
	SeqRaw string
	// Epoch is the manifest trust_epoch; OmitEpoch drops the key and EpochRaw
	// overrides the raw JSON value.
	Epoch     uint64
	OmitEpoch bool
	EpochRaw  string
	// KeyID is the manifest key_id; "" omits it.
	KeyID string
	// Rules is the number of regex rules (default 1; negative means none).
	Rules int
	// PadTo pads the rendered bundle to exactly this many bytes.
	PadTo int
	// Version overrides bundle_version (default "0.1.0").
	Version string
	// Tag makes two bundles with the same Seq differ byte-for-byte.
	Tag string
	// GeneratedAt is rendered as generated_at when non-empty.
	GeneratedAt string
	// BadPattern makes the first rule an uncompilable regex; NoRunnable makes
	// every rule non-runnable offline.
	BadPattern bool
	NoRunnable bool
}

// buildBundle renders a minimal valid manifest (one line, keys sorted).
func buildBundle(t *testing.T, o bundleOpts) []byte {
	t.Helper()
	version := o.Version
	if version == "" {
		version = "0.1.0"
	}
	n := o.Rules
	if n == 0 {
		n = 1
	}
	if n < 0 {
		n = 0
	}
	var rules []string
	for i := 0; i < n; i++ {
		pattern := fmt.Sprintf("never-matches-%04d", i)
		if i == 0 {
			pattern = fmt.Sprintf("bundle-marker-%d-%s", o.Seq, o.Tag)
			if o.BadPattern {
				pattern = "(unclosed"
			}
		}
		engine := "regex"
		if o.NoRunnable {
			engine = "structural_diff"
		}
		rules = append(rules, fmt.Sprintf(
			`{"category":"rug-pull","confidence":0.9,"detector":"d%d","engine":%q,"id":"TPA-2099-%04d","level":"high","pattern":%q,"target":"tool_description"}`,
			i, engine, i, pattern))
	}

	var sb strings.Builder
	sb.WriteString(`{"bundle_version":` + fmt.Sprintf("%q", version))
	if o.GeneratedAt != "" {
		sb.WriteString(`,"generated_at":` + fmt.Sprintf("%q", o.GeneratedAt))
	}
	if o.KeyID != "" {
		sb.WriteString(`,"key_id":` + fmt.Sprintf("%q", o.KeyID))
	}
	sb.WriteString(`,"rules":[` + strings.Join(rules, ",") + `]`)
	sb.WriteString(`,"schema_version":"0.1.0"`)
	switch {
	case o.SeqRaw != "":
		sb.WriteString(`,"sequence":` + o.SeqRaw)
	case o.Seq > 0:
		fmt.Fprintf(&sb, `,"sequence":"%d"`, o.Seq)
	}
	fmt.Fprintf(&sb, `,"signature_count":%d`, n)
	switch {
	case o.EpochRaw != "":
		sb.WriteString(`,"trust_epoch":` + o.EpochRaw)
	case !o.OmitEpoch:
		fmt.Fprintf(&sb, `,"trust_epoch":%d`, o.Epoch)
	}
	sb.WriteString("}")
	out := []byte(sb.String())

	if o.PadTo > 0 {
		const overhead = len(`,"padding":""`)
		need := o.PadTo - len(out) - overhead
		require.GreaterOrEqual(t, need, 0, "PadTo %d is smaller than the bundle itself (%d)", o.PadTo, len(out)+overhead)
		padded := make([]byte, 0, o.PadTo)
		padded = append(padded, out[:len(out)-1]...)
		padded = append(padded, `,"padding":"`...)
		padded = append(padded, bytes.Repeat([]byte{'x'}, need)...)
		padded = append(padded, `"}`...)
		out = padded
		require.Len(t, out, o.PadTo)
	}
	return out
}

// signBundle returns the canonical sidecar for bundle under priv.
func signBundle(t *testing.T, priv ed25519.PrivateKey, bundle []byte) []byte {
	t.Helper()
	return bundlesig.SignSidecar(priv, bundle)
}

// signedCandidate builds a manifest whose key_id names key (unless the options
// say otherwise), signs it with key, and wraps both in a file-sourced Candidate.
func signedCandidate(t *testing.T, key testKey, o bundleOpts) Candidate {
	t.Helper()
	if o.KeyID == "" {
		o.KeyID = key.id
	}
	b := buildBundle(t, o)
	return Candidate{Bundle: b, Sidecar: signBundle(t, key.priv, b), SidecarPresent: true, Source: BundleSourceFile, Path: "/drop/scanner-bundle.json"}
}

// unsignedCandidate builds an unsigned file-sourced Candidate.
func unsignedCandidate(t *testing.T, o bundleOpts) Candidate {
	t.Helper()
	return Candidate{Bundle: buildBundle(t, o), Source: BundleSourceFile, Path: "/drop/scanner-bundle.json"}
}

// digestOf is the full lowercase-hex SHA-256 of b.
func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func u64p(v uint64) *uint64 { return &v }

// --- SC-010: a REAL 48-bit fingerprint collision (birthday search, precomputed) ---

const (
	// sc010Template is the collision template: one line, no trailing newline.
	// KEYID and NONCE are substituted by sc010Bundle.
	sc010Template = `{"bundle_version":"0.1.0","generated_from":"sc010-collision-NONCE","key_id":"KEYID","rules":[{"category":"rug-pull","confidence":0.9,"detector":"sc010","engine":"regex","id":"TPA-2026-9010","level":"high","pattern":"(?i)sc010-collision-marker","target":"tool_description"}],"schema_version":"0.1.0","sequence":"5","signature_count":1,"trust_epoch":0}`
	// sc010KeyID is the key id of ed25519.NewKeyFromSeed(bytes.Repeat(0x11, 32)),
	// i.e. keyPublisher.
	sc010KeyID = "10ba682c8ad13513971e8b56881aab8bd702bb807796eca81932c735a94d6e6d"
	// sc010NonceA / sc010NonceB produce bundles A and B, which share their first
	// 12 hex digest characters and nothing else.
	sc010NonceA  = "0000000e5e41"
	sc010NonceB  = "00000145dd8d"
	sc010DigestA = "6cd669dbf40882df872bf7b7b29ca1596671b915992e35d957d6a6ff62c37db3"
	sc010DigestB = "6cd669dbf408404bc7280e8d622dc6b4f8132785980ee32b7c7baf6119112354"
	// sc010Fingerprint is the shared 12-hex human fingerprint.
	sc010Fingerprint = "6cd669dbf408"
)

// sc010Bundle renders collision bundle A or B (pass sc010NonceA / sc010NonceB).
func sc010Bundle(nonce string) []byte {
	return []byte(strings.NewReplacer("NONCE", nonce, "KEYID", sc010KeyID).Replace(sc010Template))
}

// --- policy / environment fixtures ---

// testPolicy builds a policy from a SecurityConfig seeded with the operator test
// key; mutate may change it. The path is always "" so MCPPROXY_TPA_BUNDLE_PATH
// can never leak into a test.
func testPolicy(mutate func(*config.SecurityConfig)) *bundlePolicy {
	sec := &config.SecurityConfig{TPAOperatorSigners: []string{keyOperator.b64}}
	if mutate != nil {
		mutate(sec)
	}
	return policyFor(sec, "")
}

// fixedNow is the clock every pure test uses.
var fixedNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// pipelineFixture is the pure analogue of one runtime: a policy and the
// committed state, advanced only through applyActivation / applyRollback.
type pipelineFixture struct {
	t      *testing.T
	policy *bundlePolicy
	st     *TPABundleState
}

// newPipelineFixture installs the publisher test key as the only publisher
// signer at publisher epoch 0, an embedded default carrying embeddedSeq (0 =
// none), and a fresh (nil) state.
func newPipelineFixture(t *testing.T, embeddedSeq uint64, mutate func(*config.SecurityConfig)) *pipelineFixture {
	t.Helper()
	withTrustAnchors(t, []ed25519.PublicKey{keyPublisher.pub}, 0,
		buildBundle(t, bundleOpts{Seq: embeddedSeq, Tag: "embedded"}))
	f := &pipelineFixture{t: t, policy: testPolicy(mutate)}
	f.renormalize()
	return f
}

// renormalize re-derives the state the way every pipeline run does.
func (f *pipelineFixture) renormalize() {
	f.st, _ = normalizeState(f.st, f.policy)
}

// repolicy swaps the policy (a config change) and renormalizes.
func (f *pipelineFixture) repolicy(mutate func(*config.SecurityConfig)) {
	f.policy = testPolicy(mutate)
	f.renormalize()
}

func (f *pipelineFixture) env(mode evalMode) evalEnv {
	return evalEnv{Policy: f.policy, State: f.st, Mode: mode, Now: func() time.Time { return fixedNow }}
}

// eval evaluates c in normal mode.
func (f *pipelineFixture) eval(c Candidate) (*acceptedBundle, *BundleRejection) {
	return evaluateCandidate(c, f.env(evalNormal))
}

// activate evaluates c, requires acceptance, applies the activation and
// returns the accepted bundle.
func (f *pipelineFixture) activate(c Candidate) *acceptedBundle {
	f.t.Helper()
	acc, rej := f.eval(c)
	require.Nil(f.t, rej, "candidate must be accepted")
	require.NotNil(f.t, acc)
	f.st, _ = applyActivation(f.st, acc, c, fixedNow)
	f.renormalize()
	return acc
}

// refuse evaluates c and requires rejection with the given reason.
func (f *pipelineFixture) refuse(c Candidate, reason string) *BundleRejection {
	f.t.Helper()
	acc, rej := f.eval(c)
	require.Nil(f.t, acc, "candidate must be refused")
	require.NotNil(f.t, rej)
	require.Equal(f.t, reason, rej.Reason, "detail: %s", rej.Detail)
	return rej
}

func (f *pipelineFixture) auth(name string) *TPAAuthorityState {
	f.t.Helper()
	a := f.st.Authorities[name]
	require.NotNil(f.t, a, "authority %s state", name)
	return a
}
