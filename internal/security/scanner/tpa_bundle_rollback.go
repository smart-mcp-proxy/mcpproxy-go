package scanner

import (
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Explicit rollback and unpin (spec 101 FR-010, design §7). Rollback is the one
// path that deliberately bypasses the downgrade watermark, so it is authorized
// by the COMPLETE SHA-256 digest of the target bytes, never by a pathname or a
// 12-hex fingerprint: the path only says where to look for those bytes.

// BundleRollbackRequest names the rollback target. Exactly one of Embedded and
// Path is set; Digest is the full lowercase-hex SHA-256 of the target's bytes.
type BundleRollbackRequest struct {
	// Embedded rolls back to the corpus compiled into this binary.
	Embedded bool `json:"embedded,omitempty"`
	// Path is where to find the target bundle (and Path+".sig"); a hint only.
	Path string `json:"path,omitempty"`
	// Digest is the 64-hex SHA-256 the loaded artifact must have.
	Digest string `json:"digest"`
}

// ErrBundleRollbackUsage marks a malformed rollback request: both or neither of
// embedded/path, or a digest that is not 64 lowercase hex characters (a 12-hex
// fingerprint included). It is a caller error (HTTP 400), not a recorded
// rejection.
var ErrBundleRollbackUsage = errors.New("invalid bundle rollback request")

// BundleRollbackRefused is returned when the target was refused after the
// request was well-formed. The refusal is also recorded as the install's
// LastRejection; last-known-good keeps serving.
type BundleRollbackRefused struct{ Rejection BundleRejection }

func (e *BundleRollbackRefused) Error() string {
	if e.Rejection.Detail == "" {
		return "bundle rollback refused: " + e.Rejection.Reason
	}
	return "bundle rollback refused: " + e.Rejection.Reason + ": " + e.Rejection.Detail
}

// validateRollbackRequest enforces the request grammar.
func validateRollbackRequest(req BundleRollbackRequest) error {
	if req.Embedded == (req.Path != "") {
		return fmt.Errorf("%w: give exactly one of embedded or path", ErrBundleRollbackUsage)
	}
	if !digestHexPattern.MatchString(req.Digest) {
		return fmt.Errorf("%w: digest must be the full SHA-256 as 64 lowercase hex characters (the 12-character fingerprint is a label, not an authorization)", ErrBundleRollbackUsage)
	}
	return nil
}

// RollbackBundle performs the explicit rollback of FR-010. On success the
// target serves, is pinned, the release rolled away from is deny-listed, the
// watermark is untouched and the override is logged at WARN. A refusal returns
// a *BundleRollbackRefused (also recorded as LastRejection); a malformed
// request returns ErrBundleRollbackUsage. The returned BundleInfo is the status
// after the call in every case.
func RollbackBundle(req BundleRollbackRequest, logger *zap.Logger) (BundleInfo, error) {
	if err := validateRollbackRequest(req); err != nil {
		return BundleStatus(), err
	}
	log := bundleLogger(logger)
	bundleRT.mu.Lock()
	defer bundleRT.mu.Unlock()
	err := bundleRT.rollbackLocked(req, log)
	return statusFromSnapshot(), err
}

func (rt *bundleRuntime) rollbackLocked(req BundleRollbackRequest, log *zap.Logger) error {
	now := time.Now()
	if rt.policy == nil || !rt.loaded {
		// Nothing has run yet: establish the baseline serving state first so
		// there is something to roll away from.
		rt.runLocked(rt.policyOrDefaultLocked(), log, runOptions{})
	}
	policy := rt.policy
	nst, normLogs := normalizeState(rt.state, policy)
	for _, line := range normLogs {
		log.Warn(line)
	}
	refuse := func(rej *BundleRejection, digest string) error {
		rt.recordRejectionLocked(rej, digest, true)
		rt.republishRejectionLocked()
		rt.clean = false
		log.Error("TPA scanner bundle rollback refused; the serving corpus is unchanged",
			zap.String("reason", rej.Reason), zap.String("detail", rej.Detail), zap.String("named_digest", req.Digest))
		return &BundleRollbackRefused{Rejection: *rej}
	}

	if rt.loadFailed {
		// A rollback is the one operator override of the watermark; recording
		// it is the whole point (pin + deny-list), so it is not performed
		// against a state store that could not be read.
		return refuse(&BundleRejection{Reason: RejectStateCommitFailed,
			Detail: "the anti-downgrade state could not be loaded, so the rollback cannot be recorded", At: now}, "")
	}

	var (
		sv     *servingBundle
		target rollbackTarget
	)
	if req.Embedded {
		e := rt.embeddedLocked()
		if e.err != nil {
			return refuse(&BundleRejection{Reason: RejectInvalidBundle, Detail: "embedded bundle unavailable: " + e.err.Error(), At: now}, e.digest)
		}
		if e.digest != req.Digest {
			return refuse(&BundleRejection{Reason: RejectRollbackTargetMismatch,
				Detail: fmt.Sprintf("named %s, the embedded default is %s", shortDigest(req.Digest), shortDigest(e.digest)), At: now}, e.digest)
		}
		seq := uint64(0)
		if e.hasSeq {
			seq = e.seq
		}
		target = embeddedRollbackTarget(policy, e.digest, seq, e.meta.GeneratedAt)
		sv = embeddedServing(e, policy)
	} else {
		cand, rej := readFileCandidate(req.Path)
		if rej != nil {
			return refuse(rej, "")
		}
		got := digestHex(cand.Bundle)
		// The full digest is compared BEFORE anything is parsed or verified: the
		// path is attacker-writable, so the bytes themselves must prove they are
		// the artifact the operator named.
		if got != req.Digest {
			return refuse(&BundleRejection{Reason: RejectRollbackTargetMismatch,
				Detail: fmt.Sprintf("named %s, the file holds %s", shortDigest(req.Digest), shortDigest(got)), At: now}, got)
		}
		acc, rej := evaluateCandidate(*cand, evalEnv{Policy: policy, State: nst, Mode: evalRollback, RollbackDigest: req.Digest})
		if rej != nil {
			return refuse(rej, got)
		}
		sv = newExternalServing(*cand, acc)
		acc.Meta.GeneratedAt = sv.generatedAt
		target = rollbackTargetFromAccepted(acc, *cand)
	}

	// The release being rolled away from is whatever was serving; rolling TO the
	// serving release must never deny-list it.
	rolledAway := nst.Active
	if rolledAway != nil && rolledAway.FullDigest == target.FullDigest {
		rolledAway = nil
	}
	st, row := applyRollback(nst, policy, target, rolledAway, now)

	var external *servingBundle
	if !sv.embedded {
		external = sv
	}
	if err := rt.persistLocked(st, row, external, log); err != nil {
		return refuse(&BundleRejection{Reason: RejectStateCommitFailed, Detail: err.Error(), At: now}, target.FullDigest)
	}

	rt.policy = policy
	rt.active = external
	rt.clean = false
	rt.publishLocked(policy, st, sv, log, now)

	fields := []zap.Field{
		zap.String("target_full_digest", target.FullDigest),
		zap.Uint64("target_sequence", target.Sequence),
		zap.String("target_source", target.Source),
		zap.Bool("target_embedded", target.Embedded),
	}
	if rolledAway != nil {
		fields = append(fields, zap.String("rolled_away_full_digest", rolledAway.FullDigest), zap.Uint64("rolled_away_sequence", rolledAway.Sequence))
	}
	if a := st.Authorities[policy.ActiveRoot]; a != nil {
		fields = append(fields, zap.Uint64("watermark", a.Watermark))
	}
	log.Warn("TPA scanner bundle ROLLBACK: operator override", fields...)
	return nil
}

// republishRejectionLocked re-publishes the already serving (check, info) with
// the freshly recorded last rejection, without touching anything else.
func (rt *bundleRuntime) republishRejectionLocked() {
	check, info := snapshotBundle()
	info.LastRejection = rt.lastRejCopy()
	storeBundle(check, info)
}

// UnpinBundle clears the rollback pin of the active signing root (FR-010) and
// re-validates the serving bundle WITHOUT it: a rolled-back target below the
// watermark no longer passes and the install falls back to the embedded default
// under the FR-009a rules, exactly as a restart would. The deny-list is kept.
// Unpinning when nothing is pinned is a quiet no-op.
func UnpinBundle(logger *zap.Logger) (BundleInfo, error) {
	log := bundleLogger(logger)
	bundleRT.mu.Lock()
	defer bundleRT.mu.Unlock()
	rt := &bundleRT

	if rt.policy == nil || !rt.loaded {
		rt.runLocked(rt.policyOrDefaultLocked(), log, runOptions{})
	}
	policy := rt.policy
	if rt.loadFailed {
		return statusFromSnapshot(), errors.New("unpin not recorded: the anti-downgrade state could not be loaded")
	}
	nst, _ := normalizeState(rt.state, policy)
	a := nst.Authorities[policy.ActiveRoot]
	if a == nil || a.Pin == nil {
		return statusFromSnapshot(), nil
	}
	pin := *a.Pin
	st := cloneState(nst)
	st.Authorities[policy.ActiveRoot].Pin = nil
	if err := rt.persistLocked(st, nil, nil, log); err != nil {
		return statusFromSnapshot(), fmt.Errorf("unpin not recorded: %w", err)
	}
	log.Warn("TPA scanner bundle pin removed",
		zap.String("effect", "activation is governed by the watermark again"),
		zap.String("authority", policy.ActiveRoot),
		zap.String("pinned_full_digest", pin.FullDigest),
		zap.Uint64("pinned_sequence", pin.Sequence))
	rt.runLocked(policy, log, runOptions{force: true})
	return statusFromSnapshot(), nil
}

// BundleHistory returns the append-only activation history, oldest first. It is
// how an operator learns the full digest of a known-good artifact to name in a
// rollback; it makes a legitimate rollback possible, it never authorizes one.
func BundleHistory() ([]TPABundleActivation, error) {
	bundleRT.histMu.Lock()
	store := bundleRT.histStore
	mem := append([]TPABundleActivation(nil), bundleRT.history...)
	bundleRT.histMu.Unlock()
	if store != nil {
		return store.ListTPABundleHistory()
	}
	return mem, nil
}
