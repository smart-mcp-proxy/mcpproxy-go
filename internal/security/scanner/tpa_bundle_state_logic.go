package scanner

import (
	"fmt"
	"strconv"
	"time"
)

// Pure anti-downgrade state functions (spec 101 FR-009, FR-009a, FR-010). They
// never mutate their inputs and never perform I/O, so the runtime can compute a
// candidate state, commit it, and only then publish.

// bundleAuthorities lists the authorities every normalized state carries.
var bundleAuthorities = [...]string{AuthorityPublisher, AuthorityOperator}

// cloneState deep-copies st (nil stays nil).
func cloneState(st *TPABundleState) *TPABundleState {
	if st == nil {
		return nil
	}
	out := *st
	if st.Active != nil {
		active := *st.Active
		out.Active = &active
	}
	if st.Authorities != nil {
		out.Authorities = make(map[string]*TPAAuthorityState, len(st.Authorities))
		for name, a := range st.Authorities {
			out.Authorities[name] = cloneAuthority(a)
		}
	}
	return &out
}

func cloneAuthority(a *TPAAuthorityState) *TPAAuthorityState {
	if a == nil {
		return nil
	}
	out := *a
	if a.Pin != nil {
		pin := *a.Pin
		out.Pin = &pin
	}
	if a.DenyList != nil {
		out.DenyList = append([]TPABundleDenyEntry(nil), a.DenyList...)
	}
	return &out
}

// normalizeState returns a clone of st scoped to policy: both authority records
// exist, are scoped to the authority's current trust epoch, and are never below
// the authority's baseline. The second result is the log lines to emit.
//
// Everything that anchors outside the data directory (the publisher baseline,
// the operator baseline, the epochs) is re-applied here on EVERY run, so a
// substituted state file can raise the floor but never lower it (SC-012).
func normalizeState(st *TPABundleState, policy *bundlePolicy) (*TPABundleState, []string) {
	out := cloneState(st)
	if out == nil {
		out = &TPABundleState{}
	}
	if out.SchemaVersion == 0 {
		out.SchemaVersion = TPABundleStateSchemaVersion
	}
	if out.Authorities == nil {
		out.Authorities = make(map[string]*TPAAuthorityState, len(bundleAuthorities))
	}

	var logs []string
	for _, auth := range bundleAuthorities {
		cur := policy.currentEpoch(auth)
		base, _ := policy.baseline(auth) // an unbaselined operator seeds 0
		rec := out.Authorities[auth]

		switch {
		case rec == nil:
			out.Authorities[auth] = &TPAAuthorityState{Epoch: cur, Watermark: base}
		case rec.Epoch != cur:
			retired := len(rec.DenyList)
			if rec.Pin != nil {
				retired++
			}
			logs = append(logs, fmt.Sprintf(
				"trust epoch for %s changed %d→%d: watermark re-baselined to %d, %d pin/deny entries retired",
				auth, rec.Epoch, cur, base, retired))
			out.Authorities[auth] = &TPAAuthorityState{Epoch: cur, Watermark: base}
		case rec.Watermark < base:
			// The baseline only ever raises the floor. The floor is now the
			// baseline, not a bundle, so the recorded setter no longer applies.
			rec.Watermark = base
			rec.WatermarkSetBy = ""
		}
	}
	return out, logs
}

// pinBinds reports whether a rollback pin governs activation for a. After
// normalizeState a pin can only exist for the authority's current epoch, and
// only the active root's record is ever consulted.
func pinBinds(a *TPAAuthorityState) bool { return a != nil && a.Pin != nil }

// denied reports whether the exact release (seq, digest) was rolled away from.
func denied(a *TPAAuthorityState, seq uint64, digest string) bool {
	if a == nil {
		return false
	}
	for _, d := range a.DenyList {
		if d.Sequence == seq && d.FullDigest == digest {
			return true
		}
	}
	return false
}

// downgradeCheck decides whether a signed candidate (seq, digest) of authority
// auth may activate against a. It returns "" when allowed, otherwise the
// human detail of the downgrade rejection.
//
// A candidate is allowed when ANY of:
//   - its sequence is above the watermark (a normal advance);
//   - it is the very release that set the watermark (a restart or LKG loss);
//   - a pin binds and it is the pinned target;
//   - this is a rollback and it carries the digest the operator named.
//
// The last two additionally require the sequence to be at or above the
// authority's OWN baseline, never another authority's: that keeps a forged pin
// or a substituted rollback target from landing below the anchors that live
// outside the data directory. Everything else is a downgrade, including an
// equal sequence with a different digest.
func downgradeCheck(a *TPAAuthorityState, auth string, policy *bundlePolicy, seq uint64, digest string, mode evalMode, rollbackDigest string) string {
	var watermark uint64
	var setBy string
	if a != nil {
		watermark, setBy = a.Watermark, a.WatermarkSetBy
	}
	base, hasBase := policy.baseline(auth)
	if hasBase && base > watermark {
		// Defence in depth for a state that was not normalized.
		watermark, setBy = base, ""
	}

	if seq > watermark {
		return ""
	}
	// While a pin to a DIFFERENT target binds, the release that set the watermark
	// is not special: a rollback that left it unrecorded (nothing external was
	// serving) must not be undone by re-dropping it.
	pinBlocksSetter := pinBinds(a) && digest != a.Pin.FullDigest
	if seq == watermark && setBy != "" && digest == setBy && !pinBlocksSetter {
		return ""
	}

	pinned := pinBinds(a) && digest == a.Pin.FullDigest
	rollingBack := mode == evalRollback && rollbackDigest != "" && digest == rollbackDigest
	if pinned || rollingBack {
		if hasBase && seq < base {
			return fmt.Sprintf("below %s baseline %d", auth, base)
		}
		return ""
	}

	detail := fmt.Sprintf("sequence %d, watermark %d", seq, watermark)
	if setBy != "" {
		detail += ", set by " + shortDigest(setBy)
	}
	return detail
}

// ensureAuthority returns out's record for auth, creating one scoped to epoch
// when the state was not normalized.
func ensureAuthority(out *TPABundleState, auth string, epoch uint64) *TPAAuthorityState {
	if out.Authorities == nil {
		out.Authorities = make(map[string]*TPAAuthorityState, len(bundleAuthorities))
	}
	a := out.Authorities[auth]
	if a == nil {
		a = &TPAAuthorityState{Epoch: epoch}
		out.Authorities[auth] = a
	}
	return a
}

// sequenceString is the published decimal form; 0 (unsigned, or an embedded
// bundle without a sequence) is "".
func sequenceString(seq uint64) string {
	if seq == 0 {
		return ""
	}
	return strconv.FormatUint(seq, 10)
}

// recordSignedActivation applies what a signature-verified activation does to
// out: arm the ratchet, note that this authority has activated an external
// bundle, and raise the watermark when the sequence is above it.
func recordSignedActivation(out *TPABundleState, auth string, epoch, seq uint64, digest string) {
	out.SignatureRatchet = true
	a := ensureAuthority(out, auth, epoch)
	a.ExternalEverActivated = true
	if seq > a.Watermark {
		a.Watermark = seq
		a.WatermarkSetBy = digest
		// A normal advance supersedes any pin.
		a.Pin = nil
	}
}

// applyActivation returns the state after activating acc, and the history row
// for that activation. An unsigned activation changes neither the watermark nor
// the ratchet: its manifest is attacker-chosen.
func applyActivation(st *TPABundleState, acc *acceptedBundle, cand Candidate, now time.Time) (*TPABundleState, *TPABundleActivation) {
	out := cloneState(st)
	if out == nil {
		out = &TPABundleState{SchemaVersion: TPABundleStateSchemaVersion}
	}
	if acc.Verified {
		recordSignedActivation(out, acc.Authority, acc.TrustEpoch, acc.Sequence, acc.FullDigest)
	}
	out.Active = &TPABundleActiveRecord{
		Authority:         acc.Authority,
		TrustEpoch:        acc.TrustEpoch,
		Sequence:          acc.Sequence,
		FullDigest:        acc.FullDigest,
		SidecarDigest:     acc.SidecarDigest,
		SignatureVerified: acc.Verified,
		Source:            cand.Source,
		Path:              cand.Path,
		GeneratedAt:       acc.Meta.GeneratedAt,
		ActivatedAt:       now,
	}
	return out, &TPABundleActivation{
		Authority:         acc.Authority,
		TrustEpoch:        acc.TrustEpoch,
		Sequence:          sequenceString(acc.Sequence),
		FullDigest:        acc.FullDigest,
		GeneratedAt:       acc.Meta.GeneratedAt,
		ActivatedAt:       now,
		Source:            cand.Source,
		SignatureVerified: acc.Verified,
	}
}

// rollbackTarget describes the release an explicit rollback activates.
type rollbackTarget struct {
	// Embedded is true when the target is the binary's embedded default.
	Embedded          bool
	Authority         string
	TrustEpoch        uint64
	Sequence          uint64
	FullDigest        string
	SidecarDigest     string
	SignatureVerified bool
	Source            string
	Path              string
	GeneratedAt       string
}

// rollbackTargetFromAccepted describes a file-sourced rollback target.
func rollbackTargetFromAccepted(acc *acceptedBundle, cand Candidate) rollbackTarget {
	return rollbackTarget{
		Authority:         acc.Authority,
		TrustEpoch:        acc.TrustEpoch,
		Sequence:          acc.Sequence,
		FullDigest:        acc.FullDigest,
		SidecarDigest:     acc.SidecarDigest,
		SignatureVerified: acc.Verified,
		Source:            cand.Source,
		Path:              cand.Path,
		GeneratedAt:       acc.Meta.GeneratedAt,
	}
}

// embeddedRollbackTarget describes the embedded default as a rollback target.
// The embedded default is a publisher artifact that rides on binary integrity,
// so it is never "signature verified".
func embeddedRollbackTarget(policy *bundlePolicy, digest string, seq uint64, generatedAt string) rollbackTarget {
	return rollbackTarget{
		Embedded:    true,
		Authority:   AuthorityPublisher,
		TrustEpoch:  policy.currentEpoch(AuthorityPublisher),
		Sequence:    seq,
		FullDigest:  digest,
		Source:      BundleSourceEmbedded,
		GeneratedAt: generatedAt,
	}
}

// applyRollback returns the state after an explicit rollback to target, away
// from rolledAway (the record of the bundle that was serving; nil when the
// embedded default was), and the history row.
//
// The watermark is never lowered. The release rolled away from is deny-listed
// under its own authority (when it was signed and its epoch is still current)
// so the next refresh cannot re-activate it, and the target is pinned under the
// active root so the intermediate releases between the target and the watermark
// stay refused until a genuinely newer release activates.
func applyRollback(st *TPABundleState, policy *bundlePolicy, target rollbackTarget, rolledAway *TPABundleActiveRecord, now time.Time) (*TPABundleState, *TPABundleActivation) {
	out := cloneState(st)
	if out == nil {
		out = &TPABundleState{SchemaVersion: TPABundleStateSchemaVersion}
	}

	if rolledAway != nil && rolledAway.SignatureVerified && rolledAway.Authority != "" {
		if a := out.Authorities[rolledAway.Authority]; a != nil && a.Epoch == rolledAway.TrustEpoch &&
			!denied(a, rolledAway.Sequence, rolledAway.FullDigest) {
			a.DenyList = append(a.DenyList, TPABundleDenyEntry{
				Sequence:   rolledAway.Sequence,
				FullDigest: rolledAway.FullDigest,
				DeniedAt:   now,
			})
		}
	}

	if !target.Embedded && target.SignatureVerified && target.Authority != "" {
		recordSignedActivation(out, target.Authority, target.TrustEpoch, target.Sequence, target.FullDigest)
	}

	if root := policy.ActiveRoot; root == AuthorityPublisher || root == AuthorityOperator {
		ensureAuthority(out, root, policy.currentEpoch(root)).Pin = &TPABundlePin{
			Sequence:   target.Sequence,
			FullDigest: target.FullDigest,
			Embedded:   target.Embedded,
			PinnedAt:   now,
		}
	}

	if target.Embedded {
		out.Active = nil
	} else {
		out.Active = &TPABundleActiveRecord{
			Authority:         target.Authority,
			TrustEpoch:        target.TrustEpoch,
			Sequence:          target.Sequence,
			FullDigest:        target.FullDigest,
			SidecarDigest:     target.SidecarDigest,
			SignatureVerified: target.SignatureVerified,
			Source:            target.Source,
			Path:              target.Path,
			GeneratedAt:       target.GeneratedAt,
			ActivatedAt:       now,
			Rollback:          true,
		}
	}
	return out, &TPABundleActivation{
		Authority:         target.Authority,
		TrustEpoch:        target.TrustEpoch,
		Sequence:          sequenceString(target.Sequence),
		FullDigest:        target.FullDigest,
		GeneratedAt:       target.GeneratedAt,
		ActivatedAt:       now,
		Source:            target.Source,
		SignatureVerified: target.SignatureVerified,
		Rollback:          true,
	}
}

// servingFacts is what computeCoverage needs to know about the bundle that is
// serving.
type servingFacts struct {
	Embedded  bool
	Verified  bool
	Authority string
}

// computeCoverage decides whether scan-mode auto-approval must be suspended.
// It applies exactly the four degrade conditions of FR-007 / FR-009a / FR-009b,
// in this order, and nothing else: in particular no sequence is ever compared
// across authorities, and a missing PUBLISHER bundle never degrades by itself.
func computeCoverage(serving servingFacts, st *TPABundleState, policy *bundlePolicy, embeddedSeq uint64) (bool, string) {
	var pub, op *TPAAuthorityState
	if st != nil {
		pub, op = st.Authorities[AuthorityPublisher], st.Authorities[AuthorityOperator]
	}

	switch {
	case !serving.Embedded && !serving.Verified:
		return true, DegradedUnsignedExternalActive
	case !serving.Embedded && serving.Authority == AuthorityOperator && !policy.OperatorBaselineSet:
		return true, DegradedOperatorAuthorityUnbaselined
	case serving.Embedded && pub != nil && embeddedSeq < pub.Watermark:
		return true, DegradedEmbeddedBelowWatermark
	case serving.Embedded && policy.ActiveRoot == AuthorityOperator && op != nil && op.ExternalEverActivated:
		return true, DegradedOperatorBundleMissing
	}
	return false, ""
}
