package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/bundlesig"
)

// Candidate is one bundle offered for activation, independent of where it came
// from. The file-drop adapter builds it from two bounded reads; Spec 087's
// fetch adapter builds the same struct from a download (FR-018). Everything the
// pipeline decides is a function of these bytes and the evalEnv.
type Candidate struct {
	// Bundle is the exact candidate bytes. They are verified and then parsed
	// from this one buffer (FR-006): nothing re-reads Path.
	Bundle []byte
	// Sidecar is the detached-signature sidecar; meaningful only when
	// SidecarPresent. A present-but-invalid sidecar is a rejection, never a
	// fallback to the unsigned path (tasks.md A7).
	Sidecar        []byte
	SidecarPresent bool
	// Source is the BundleSource* label; Path is informational only.
	Source string
	Path   string
}

// evalMode says how the anti-downgrade rules treat a candidate.
type evalMode int

const (
	// evalNormal is a freshly offered candidate.
	evalNormal evalMode = iota
	// evalLKG re-validates the bundle already recorded as active. The
	// downgradeCheck allow-rules already admit it, so it differs from evalNormal
	// only in intent.
	evalLKG
	// evalRollback is an explicit operator rollback (FR-010): the one mode
	// that may land at or below the watermark, and only on the exact digest the
	// operator named.
	evalRollback
)

// evalEnv is everything evaluateCandidate may consult besides the candidate.
type evalEnv struct {
	Policy *bundlePolicy
	// State is the NORMALIZED state (normalizeState); nil is treated as empty.
	State *TPABundleState
	Mode  evalMode
	// RollbackDigest is the full digest the operator named (evalRollback only).
	RollbackDigest string
	// Now stamps rejections; nil means time.Now.
	Now func() time.Time
}

// acceptedBundle is a candidate that passed every stage.
type acceptedBundle struct {
	Check *BundleCheck
	Stats bundleLoadStats
	// Meta is the manifest header (versions, signature count, generated_at).
	Meta bundleMeta
	// FullDigest is the lowercase-hex SHA-256 of the bundle bytes; its first 12
	// characters are the human Fingerprint.
	FullDigest string
	// SidecarDigest is the SHA-256 of the sidecar bytes ("" when unsigned).
	SidecarDigest string
	// Authority is the verifying key's authority; "" for an unsigned candidate.
	Authority string
	// Verified is true when the detached signature verified.
	Verified bool
	// Sequence and TrustEpoch come from the SIGNED manifest; both are zero for
	// an unsigned candidate, whose manifest values are attacker-chosen.
	Sequence   uint64
	TrustEpoch uint64
	// KeyID is the id of the key that verified ("" when unsigned).
	KeyID string
}

// bundleSelfCheck is the activation self-check stage hook (FR-008; tasks.md
// Δ14). The default (nil) is a no-op; a non-nil error refuses the candidate as
// gate_failure. The real self-check is Spec 087 / US2 D8.
var bundleSelfCheck func(check *BundleCheck) error

// evaluateCandidate runs the FR-008 validate-before-activate stages in order;
// the first failure wins and every failure leaves last-known-good serving.
//
// The signed path verifies the detached signature over the raw bytes BEFORE
// the bytes are parsed or any pattern is compiled, and then parses the very
// buffer that was verified (FR-006): evaluateCandidate never reads c.Path.
func evaluateCandidate(c Candidate, env evalEnv) (*acceptedBundle, *BundleRejection) {
	ev := evaluation{c: c, env: env, policy: env.Policy, digest: digestHex(c.Bundle)}
	if ev.policy == nil {
		return nil, ev.reject(RejectInvalidBundle, "no bundle policy")
	}
	if c.SidecarPresent {
		return ev.signed()
	}
	return ev.unsigned()
}

// evaluation carries one evaluateCandidate call's working values.
type evaluation struct {
	c      Candidate
	env    evalEnv
	policy *bundlePolicy
	digest string
}

func (ev *evaluation) reject(reason, detail string) *BundleRejection {
	now := time.Now
	if ev.env.Now != nil {
		now = ev.env.Now
	}
	return &BundleRejection{Reason: reason, Detail: detail, At: now()}
}

// checkBundleSize enforces the bundle byte ceiling on the raw bytes. The file
// reader already caps what it reads, but a Candidate is source-neutral (Spec
// 087's fetch adapter builds the same struct), so the pipeline enforces it too.
func (ev *evaluation) checkBundleSize() *BundleRejection {
	if len(ev.c.Bundle) > MaxBundleBytes {
		return ev.reject(RejectCeilingExceeded, fmt.Sprintf("bundle: %d bytes > %d", len(ev.c.Bundle), MaxBundleBytes))
	}
	return nil
}

// parseAndCeil is the ONE parse of the candidate buffer followed by the
// rule-count ceiling, shared by both paths.
func (ev *evaluation) parseAndCeil() (*rawBundle, *BundleRejection) {
	m, err := parseBundleManifest(ev.c.Bundle)
	if err != nil {
		return nil, ev.reject(RejectInvalidBundle, err.Error())
	}
	if err := checkRuleCeiling(m); err != nil {
		return nil, ev.reject(RejectCeilingExceeded, err.Error())
	}
	return m, nil
}

// compileAndGate runs the tail every accepted candidate shares: version
// compatibility, compile-all-or-reject, runnable-rules > 0, then the
// activation self-check.
func (ev *evaluation) compileAndGate(m *rawBundle) (*BundleCheck, bundleLoadStats, *BundleRejection) {
	check, stats, err := compileBundle(m)
	if err != nil {
		var ce *bundleCeilingError
		if errors.As(err, &ce) {
			return nil, bundleLoadStats{}, ev.reject(RejectCeilingExceeded, err.Error())
		}
		return nil, bundleLoadStats{}, ev.reject(RejectInvalidBundle, err.Error())
	}
	if bundleSelfCheck != nil {
		if err := bundleSelfCheck(check); err != nil {
			return nil, bundleLoadStats{}, ev.reject(RejectGateFailure, err.Error())
		}
	}
	return check, stats, nil
}

func (ev *evaluation) accepted(m *rawBundle, check *BundleCheck, stats bundleLoadStats) *acceptedBundle {
	return &acceptedBundle{
		Check: check,
		Stats: stats,
		Meta: bundleMeta{
			BundleVersion:  m.BundleVersion,
			SchemaVersion:  m.SchemaVersion,
			SignatureCount: m.SignatureCount,
			GeneratedAt:    m.GeneratedAt,
		},
		FullDigest: ev.digest,
	}
}

// unsigned is the path of a candidate with no sidecar (tasks.md A6): its
// manifest is attacker-chosen, so its sequence, epoch and key_id are never
// consulted, it never advances the watermark, and it activates degraded.
func (ev *evaluation) unsigned() (*acceptedBundle, *BundleRejection) {
	if ev.policy.RequireSigned {
		return nil, ev.reject(RejectSignatureRequired, "require_signed_bundle is on and the candidate has no signature sidecar")
	}
	if ev.env.State != nil && ev.env.State.SignatureRatchet {
		return nil, ev.reject(RejectUnsignedRatchet, "a signature-verified bundle has activated on this install; unsigned candidates are refused")
	}
	if rej := ev.checkBundleSize(); rej != nil {
		return nil, rej
	}
	m, rej := ev.parseAndCeil()
	if rej != nil {
		return nil, rej
	}
	check, stats, rej := ev.compileAndGate(m)
	if rej != nil {
		return nil, rej
	}
	return ev.accepted(m, check, stats), nil
}

// signed is the path of a candidate that carries a sidecar. A present sidecar
// that cannot be used is a rejection and never a fallback to unsigned (A7).
func (ev *evaluation) signed() (*acceptedBundle, *BundleRejection) {
	c, p := ev.c, ev.policy

	sc, err := bundlesig.ParseSidecar(c.Sidecar)
	if err != nil {
		code, detail := bundlesig.CodeOf(err), err.Error()
		var be *bundlesig.Error
		if errors.As(err, &be) {
			detail = be.Detail
		}
		if code == bundlesig.CodeSidecarTooLarge {
			return nil, ev.reject(RejectCeilingExceeded, "sidecar: "+detail)
		}
		return nil, ev.reject(RejectSidecarInvalid, code)
	}

	if rej := ev.checkBundleSize(); rej != nil {
		return nil, rej
	}

	if p.Trust == nil {
		return nil, ev.reject(RejectKeyNotTrusted, sc.KeyID)
	}
	key, err := p.Trust.Verify(c.Bundle, sc)
	if err != nil {
		if bundlesig.CodeOf(err) == bundlesig.CodeKeyNotTrusted {
			return nil, ev.reject(RejectKeyNotTrusted, sc.KeyID)
		}
		return nil, ev.reject(RejectTamper, err.Error())
	}
	if key.Authority != p.ActiveRoot {
		return nil, ev.reject(RejectAuthorityNotActive,
			fmt.Sprintf("verified by %s key %s; active root is %s", key.Authority, key.ID, p.ActiveRoot))
	}

	// From here the bytes are authenticated: parse the SAME buffer once.
	m, rej := ev.parseAndCeil()
	if rej != nil {
		return nil, rej
	}
	seq, _, err := parseSequence(m.Sequence)
	if err != nil {
		return nil, ev.reject(RejectInvalidBundle, err.Error())
	}
	epoch, err := parseTrustEpoch(m.TrustEpoch)
	if err != nil {
		return nil, ev.reject(RejectInvalidBundle, err.Error())
	}
	if err := bundlesig.CheckKeyIDAgreement(m.KeyID, sc, key); err != nil {
		return nil, ev.reject(RejectTamper, err.Error())
	}

	auth := key.Authority
	cur := p.currentEpoch(auth)
	switch {
	case epoch < cur:
		return nil, ev.reject(RejectStaleEpochReplay, fmt.Sprintf("manifest epoch %d, current %s epoch %d", epoch, auth, cur))
	case epoch > cur:
		return nil, ev.reject(RejectEpochAhead, fmt.Sprintf("manifest epoch %d, current %s epoch %d; update mcpproxy or the operator epoch", epoch, auth, cur))
	}

	var a *TPAAuthorityState
	if ev.env.State != nil {
		a = ev.env.State.Authorities[auth]
	}
	if denied(a, seq, ev.digest) {
		return nil, ev.reject(RejectDenyListed, fmt.Sprintf("release %s (sequence %d) was rolled away from", shortDigest(ev.digest), seq))
	}
	if detail := downgradeCheck(a, auth, p, seq, ev.digest, ev.env.Mode, ev.env.RollbackDigest); detail != "" {
		return nil, ev.reject(RejectDowngrade, detail)
	}

	check, stats, rej := ev.compileAndGate(m)
	if rej != nil {
		return nil, rej
	}
	acc := ev.accepted(m, check, stats)
	acc.SidecarDigest = digestHex(c.Sidecar)
	acc.Authority = auth
	acc.Verified = true
	acc.Sequence = seq
	acc.TrustEpoch = epoch
	acc.KeyID = key.ID
	return acc, nil
}

// digestHex is the lowercase-hex SHA-256 of b.
func digestHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// shortDigest is the 12-hex human fingerprint of a full digest.
func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}
