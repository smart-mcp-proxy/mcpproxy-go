package scanner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/bundlesig"
)

// The serialized bundle runtime (spec 101 FR-008, FR-009, FR-011a; design §2/§6).
//
// The loader is process-global because the scan gate also runs where no
// scanner.Service exists (stdio mode). One mutex, bundleRT.mu, serializes EVERY
// pipeline run (startup, config reload, explicit reload, rollback, unpin), so
// two runs can never interleave their read -> validate -> persist -> publish
// steps. Scans never take that mutex: they read the published (check, info)
// snapshot under activeBundleMu, and a run publishes both under one lock at its
// very end.

// bundleReadBudget is the per-candidate read budget (FR-011: 5 s for a local
// file). It is a variable only so a test can shrink it.
var bundleReadBudget = 5 * time.Second

// scannerLogName is the logger name every bundle log line carries.
const scannerLogName = "security.scanner"

// servingBundle is the corpus a run chose to serve.
type servingBundle struct {
	// embedded is true for the binary's own default.
	embedded bool
	// cand holds the exact bytes of an external bundle, kept in memory so the
	// serving bundle can be re-validated under a new policy without touching the
	// filesystem.
	cand Candidate
	acc  *acceptedBundle

	check *BundleCheck
	stats bundleLoadStats
	meta  bundleMeta

	digest        string
	sidecarDigest string
	authority     string
	verified      bool
	sequence      uint64
	hasSequence   bool
	epoch         uint64
	// generatedAt is the freshness stamp surfaced in BundleInfo (the manifest's
	// own, or for an unsigned file the file's mtime).
	generatedAt string
}

// id identifies the serving bundle across runs; it decides LoadedAt and the
// "loaded" log line.
func (s *servingBundle) id() string {
	if s.embedded {
		return "embedded|" + s.digest
	}
	return s.digest + "|" + s.sidecarDigest + "|" + s.authority
}

// embeddedEntry caches the compiled embedded default, keyed by the digest of the
// embedded bytes, so a refused candidate never costs a recompile.
type embeddedEntry struct {
	digest string
	check  *BundleCheck
	stats  bundleLoadStats
	meta   bundleMeta
	seq    uint64
	hasSeq bool
	err    error
}

// bundleRuntime is the process-wide runtime state.
type bundleRuntime struct {
	// mu is the activation mutex: it serializes every run. It is never held
	// while activeBundleMu is held, and activeBundleMu is never held across I/O.
	mu sync.Mutex

	store   TPABundleStateStore // nil => in-memory state only (unit tests, scan-eval)
	dataDir string              // "" => no LKG files

	// state mirrors the last COMMITTED (or, with no store, applied) state.
	state  *TPABundleState
	loaded bool
	// loadFailed is true while the state store could not be read: the run
	// proceeds against the out-of-data-dir anchors but records nothing.
	loadFailed bool

	policy *bundlePolicy
	active *servingBundle // the serving EXTERNAL bundle; nil => embedded serves

	lastRej    *BundleRejection
	lastRejKey string
	loadErr    string
	// clean is true when the last run completed without a persistence failure,
	// i.e. repeating it with identical inputs is a no-op.
	clean bool

	servingID   string
	loadedAt    time.Time
	lastLogKey  string
	degradedKey string
	warnedFP    string

	embedded *embeddedEntry

	// refusedOffer is the negative cache of the fetch leg (tasks B7): the key of
	// the last offered pair the pipeline refused, so the same pair offered again
	// costs no compile, no self-check and no second log line. It is cleared by
	// every committed state change and keyed by the policy fingerprint, so a
	// trust or watermark change always re-evaluates.
	refusedOffer string

	// histMu guards history and histStore, so BundleHistory never waits on a
	// run that is blocked in a (budget-bounded) file read.
	histMu    sync.Mutex
	histStore TPABundleStateStore   // mirror of store for BundleHistory
	history   []TPABundleActivation // kept only when store == nil

	// disp is the read-time status (refresh settings and status, last_checked_at,
	// stale threshold, re-protection); it has its own mutex and is merged into
	// the copies BundleStatus returns (tasks B15).
	disp bundleDisplay
}

var bundleRT bundleRuntime

// resetLocked returns the runtime to its zero state (as a fresh process would
// find it). The published snapshot is left to the caller.
func (rt *bundleRuntime) resetLocked() {
	rt.store = nil
	rt.dataDir = ""
	rt.state = nil
	rt.loaded = false
	rt.loadFailed = false
	rt.policy = nil
	rt.active = nil
	rt.lastRej = nil
	rt.lastRejKey = ""
	rt.loadErr = ""
	rt.clean = false
	rt.servingID = ""
	rt.loadedAt = time.Time{}
	rt.lastLogKey = ""
	rt.degradedKey = ""
	rt.warnedFP = ""
	rt.embedded = nil
	rt.refusedOffer = ""
	rt.histMu.Lock()
	rt.history = nil
	rt.histStore = nil
	rt.histMu.Unlock()
	rt.disp.reset()
	bundleFacts.Store(nil)
}

// SetBundleRuntime injects the persistent state store and the data directory the
// LKG files live under. Call it once at startup, before the first configure; it
// resets the runtime so the next run loads state from the new store (spec 101
// Δ4). A nil store keeps the anti-downgrade state in memory only; an empty
// dataDir keeps no LKG files.
func SetBundleRuntime(store TPABundleStateStore, dataDir string) {
	bundleRT.mu.Lock()
	defer bundleRT.mu.Unlock()
	bundleRT.resetLocked()
	bundleRT.store = store
	bundleRT.dataDir = dataDir
	bundleRT.histMu.Lock()
	bundleRT.histStore = store
	bundleRT.histMu.Unlock()
}

// ConfigureBundleFromConfig applies the security config's bundle policy: the
// path (MCPPROXY_TPA_BUNDLE_PATH outranks the file value), require_signed_bundle,
// the active signing root, the operator signers, baseline and epoch. It is safe
// to call repeatedly and from any goroutine; an unchanged configuration with an
// unchanged bundle file is a no-op (no commit, no history row, no log line).
func ConfigureBundleFromConfig(sec *config.SecurityConfig, logger *zap.Logger) {
	configureWithPolicy(policyFromConfig(sec), logger)
}

// configureWithPolicy runs the pipeline for an already-built policy. The
// display settings (stale threshold, interval, fetch host) are refreshed first,
// on every apply, so an edit shows on the next status read even when the
// pipeline has nothing to redo.
func configureWithPolicy(policy *bundlePolicy, logger *zap.Logger) {
	applyPolicy(policy, logger, "")
}

// applyPolicy is configureWithPolicy with an explicit trigger label ("" means a
// configure: startup the first time in this process, config afterwards) and
// the leg's outcome.
func applyPolicy(policy *bundlePolicy, logger *zap.Logger, trigger string) runOutcome {
	log := bundleLogger(logger)
	bundleRT.disp.applySettings(policy)
	bundleRT.disp.noteTrigger(trigger)
	var out runOutcome
	bundleRT.mu.Lock()
	defer bundleRT.mu.Unlock()
	bundleRT.runLocked(policy, log, runOptions{out: &out})
	return out
}

// ReloadBundle re-runs the pipeline with the last applied policy and returns the
// resulting status. It is the local, network-free refresh the REST/CLI reload
// action uses (B20): it never issues a request, whatever the fetch settings.
func ReloadBundle(logger *zap.Logger) BundleInfo {
	info, _ := reloadLastPolicy(logger, RefreshTriggerManual)
	return info
}

// reloadLastPolicy re-runs the pipeline with the last applied policy and
// returns the resulting status and the run's outcome.
func reloadLastPolicy(logger *zap.Logger, trigger string) (BundleInfo, runOutcome) {
	log := bundleLogger(logger)
	bundleRT.mu.Lock()
	defer bundleRT.mu.Unlock()
	policy := bundleRT.policyOrDefaultLocked()
	bundleRT.disp.applySettings(policy)
	bundleRT.disp.noteTrigger(trigger)
	var out runOutcome
	bundleRT.runLocked(policy, log, runOptions{out: &out})
	return statusFromSnapshot(), out
}

// runOffered runs the pipeline with c as the freshly offered candidate (the
// fetch adapter's entry point, tasks B7): it evaluates ONLY c, never re-reads
// the configured file, and on refusal leaves the serving bundle in place. It
// returns the resulting status.
func runOffered(c Candidate, logger *zap.Logger) BundleInfo {
	info, _ := offerCandidate(c, logger)
	return info
}

// offerResult is what an offered (fetch) run decided about its candidate.
type offerResult struct {
	// Outcome is RefreshOutcomeActivated, RefreshOutcomeUnchanged or
	// RefreshOutcomeRejected.
	Outcome string
	// Rejection is the refusal when Outcome is RefreshOutcomeRejected.
	Rejection *BundleRejection
	// Refused is true when Outcome is RefreshOutcomeUnchanged only because the
	// pair equals one the pipeline refused earlier under this policy (the
	// negative cache), not because it equals what serves.
	Refused bool
}

// offerCandidate is runOffered plus the decision, for the refresher.
func offerCandidate(c Candidate, logger *zap.Logger) (BundleInfo, offerResult) {
	log := bundleLogger(logger)
	var out runOutcome
	bundleRT.mu.Lock()
	defer bundleRT.mu.Unlock()
	bundleRT.runLocked(bundleRT.policyOrDefaultLocked(), log, runOptions{offered: &c, out: &out})
	return statusFromSnapshot(), out.offerResult()
}

// runOutcome is what one run decided, for the refresher's status: the file leg
// (a configured-path run) and the fetch leg (an offered run) share it.
type runOutcome struct {
	// Activated is true when the run durably activated a new bundle.
	Activated bool
	// Rejection is the refusal of the leg's candidate (the configured file, or
	// the offered pair); nil when it was accepted or there was none.
	Rejection *BundleRejection
	// RejectionNew is true when Rejection is a refusal this run recorded for
	// the first time (not the same refusal repeated).
	RejectionNew bool
	// RepeatRefusal is true when an offered run did nothing because the pair
	// equals one refused earlier under this policy.
	RepeatRefusal bool
}

func (o runOutcome) offerResult() offerResult {
	switch {
	case o.Activated:
		return offerResult{Outcome: RefreshOutcomeActivated}
	case o.Rejection != nil:
		return offerResult{Outcome: RefreshOutcomeRejected, Rejection: o.Rejection}
	}
	return offerResult{Outcome: RefreshOutcomeUnchanged, Refused: o.RepeatRefusal}
}

// BundleCoverageDegraded reports whether the active bundle demotes the install to
// degraded coverage, and why. The scan-mode admission gates consult it: while it
// is true auto-approval is suspended and changes route to human review.
func BundleCoverageDegraded() (bool, string) {
	_, info := activeBundleSnapshot()
	return info.CoverageDegraded, info.DegradedReason
}

func bundleLogger(logger *zap.Logger) *zap.Logger {
	if logger == nil {
		logger = zap.NewNop()
	}
	return logger.Named(scannerLogName)
}

// policyOrDefaultLocked is the last applied policy, or the embedded-only default
// when nothing has been configured yet.
func (rt *bundleRuntime) policyOrDefaultLocked() *bundlePolicy {
	if rt.policy != nil {
		return rt.policy
	}
	return policyFromConfig(nil)
}

// runOptions tunes one run.
type runOptions struct {
	// force skips the idempotence short-circuit (unpin re-validates the serving
	// bundle even though no input file changed).
	force bool
	// offered, when non-nil, is the single freshly offered candidate (fetch
	// leg). The run evaluates only it as the fresh one: it never reads the
	// configured file and never touches the file leg's load_error (tasks B7).
	offered *Candidate
	// out, when non-nil, receives what the run decided.
	out *runOutcome
}

// selection is what a run chose to serve and why.
type selection struct {
	serving *servingBundle
	// fromPath is true when serving is the freshly accepted configured-path
	// candidate (an activation unless it equals the recorded active record).
	fromPath bool
	// attempt is the refusal of the configured-path candidate; attemptDigest is
	// the candidate's digest when it was read ("" for a read failure).
	attempt       *BundleRejection
	attemptDigest string
	// deactivated is the refusal of the bundle that WAS serving, when it no
	// longer passes validation under the current policy and state.
	deactivated       *BundleRejection
	deactivatedDigest string
	// keepActive is set when the recorded active release could not be restored
	// for a TRANSIENT reason (an I/O error or timeout reading its last-known-good
	// files): the embedded default serves for this run, but the committed active
	// record and its files are kept so the release comes back once reads work.
	keepActive bool
	// restored is the Active record put back from the active root's rollback
	// pin (restorePinLocked); serving is then that pin's bundle.
	restored *TPABundleActiveRecord
	// offeredRej is the refusal of the offered (fetch) candidate and
	// offeredDigest the candidate's digest; they are kept apart from attempt
	// so a fetch refusal never sets the file leg's load_error (tasks B7).
	offeredRej    *BundleRejection
	offeredDigest string
}

// runLocked is one pipeline run (design §6). The caller holds rt.mu.
func (rt *bundleRuntime) runLocked(policy *bundlePolicy, log *zap.Logger, opts runOptions) {
	now := bundleClock()
	// Every run is a check, including the no-op ones the idempotence
	// short-circuit skips (last_checked_at, tasks B15).
	defer func() { rt.disp.markChecked(bundleClock()) }()
	rt.ensureLoadedLocked(log)
	rt.warnPolicyLocked(policy, log)

	var (
		cand    *Candidate
		readRej *BundleRejection
		// fileAbsent: the configured path does not exist yet. That is still
		// recorded as the file leg's read_error (US1), but it is "nothing
		// dropped", not a refusal, for the refresh status.
		fileAbsent bool
	)
	if opts.offered != nil {
		// The fetch leg: no file is read. The "same as serving" and "already
		// refused" comparisons happen here, under the lock and before any
		// compile or self-check, so an unchanged channel costs nothing.
		if rt.offerUnchangedLocked(policy, opts.offered) {
			if opts.out != nil {
				opts.out.RepeatRefusal = rt.refusedOffer != "" && rt.refusedOffer == offerKey(policy, opts.offered)
			}
			return
		}
	} else {
		if policy.Path != "" {
			cand, readRej = readFileCandidate(policy.Path)
			fileAbsent = readRej != nil && pathMissing(policy.Path)
		}
		if !opts.force && rt.canSkipLocked(policy, cand) {
			return
		}
	}

	nst, normLogs := normalizeState(rt.state, policy)
	for _, line := range normLogs {
		log.Warn(line)
	}

	sel := rt.chooseLocked(policy, nst, cand, readRej, opts.offered, log)
	rt.restorePinLocked(policy, nst, &sel, log, now)

	st := nst
	var row *TPABundleActivation
	activation := false
	activated := false
	needCommit := len(normLogs) > 0
	switch {
	case sel.restored != nil:
		// A restoration is not a new activation: no history row, no watermark
		// movement; only the Active record is put back.
		st = cloneState(nst)
		st.Active = sel.restored
		needCommit = true
	case sel.fromPath && !sameActive(nst.Active, sel.serving):
		activation = true
		sel.serving.acc.Meta.GeneratedAt = sel.serving.generatedAt
		st, row = applyActivation(nst, sel.serving.acc, sel.serving.cand, now)
	case sel.serving != nil && sel.serving.embedded && nst.Active != nil && !sel.keepActive:
		st = cloneState(nst)
		st.Active = nil
		needCommit = true
	}

	clean := !rt.loadFailed
	switch {
	case activation:
		if err := rt.persistLocked(st, row, sel.serving, log); err != nil {
			// Refusing to activate is the safe outcome: activating without a
			// durable watermark would reopen the replay window FR-011a exists
			// to close. Last-known-good keeps serving.
			rej := &BundleRejection{Reason: RejectStateCommitFailed, Detail: err.Error(), At: time.Now()}
			clean = false
			st = nst
			failed := sel
			if opts.offered != nil {
				sel = selection{offeredRej: rej, offeredDigest: failed.serving.digest}
			} else {
				sel = selection{attempt: rej, attemptDigest: failed.serving.digest}
			}
			rt.fallbackLocked(policy, nst, &sel, log)
			if sel.serving != nil && sel.serving.embedded && nst.Active != nil && !sel.keepActive {
				st = cloneState(nst)
				st.Active = nil
				if perr := rt.persistLocked(st, nil, nil, log); perr != nil {
					log.Error("could not record the fall-back to the embedded TPA scanner bundle", zap.Error(perr))
					st = nst
				}
			}
		} else {
			activated = true
		}
	case needCommit:
		if err := rt.persistLocked(st, nil, nil, log); err != nil {
			clean = false
			st = nst
			log.Error("could not persist the TPA bundle anti-downgrade state", zap.Error(err))
		}
	case sel.fromPath && rt.dataDir != "" && !rt.loadFailed:
		// The path serves the very bundle the state records; make sure its LKG
		// files exist (a wiped tpa-lkg directory heals here).
		if err := lkgWrite(rt.dataDir, sel.serving.digest, sel.serving.cand.Bundle, sel.serving.cand.Sidecar, sel.serving.cand.SidecarPresent); err != nil {
			log.Error("could not refresh the last-known-good copy of the serving TPA bundle", zap.Error(err))
		}
	}

	rt.finishLocked(policy, st, sel, clean, opts, log, now)

	if opts.out != nil {
		opts.out.Activated = activated
		if opts.offered != nil {
			opts.out.Rejection = sel.offeredRej
		} else if !fileAbsent {
			opts.out.Rejection = sel.attempt
		}
	}
	if activated {
		// After the publish, and on its own goroutine: no observer ever runs
		// under bundleRT.mu.
		fireBundleActivation(BundleActivation{
			Digest:         row.FullDigest,
			Sequence:       row.Sequence,
			Authority:      row.Authority,
			Source:         row.Source,
			PreviousDigest: previousDigest(nst.Active),
			ActivatedAt:    row.ActivatedAt,
		})
	}
}

// pathMissing reports whether path does not exist, as opposed to existing but
// being unreadable.
func pathMissing(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// previousDigest is the digest of the external bundle that served before rec's
// replacement ("" for the embedded default).
func previousDigest(rec *TPABundleActiveRecord) string {
	if rec == nil {
		return ""
	}
	return rec.FullDigest
}

// finishLocked records rejections, then builds and publishes the status.
func (rt *bundleRuntime) finishLocked(policy *bundlePolicy, st *TPABundleState, sel selection, clean bool, opts runOptions, log *zap.Logger, now time.Time) {
	loadErr := ""
	var fileRejNew bool
	switch {
	case opts.offered != nil:
		// An offered run neither reads nor clears the file leg's load_error.
		loadErr = rt.loadErr
	case policy.Path != "" && sel.attempt != nil:
		loadErr = sel.attempt.Reason + ": " + sel.attempt.Detail
		fileRejNew = rt.recordRejectionLocked(sel.attempt, sel.attemptDigest, false)
		if opts.out != nil {
			opts.out.RejectionNew = fileRejNew
		}
		if fileRejNew {
			log.Error("configured TPA scanner bundle refused; keeping the last-known-good corpus",
				zap.String("path", policy.Path),
				zap.String("reason", sel.attempt.Reason),
				zap.String("detail", sel.attempt.Detail),
				zap.String("candidate_digest", sel.attemptDigest))
		}
	}
	if sel.offeredRej != nil {
		// The origin is scheme and host only (B12); the detail names a key id,
		// an epoch or a sequence, never a URL.
		if rt.recordRejectionLocked(sel.offeredRej, sel.offeredDigest, false) {
			log.Warn("fetched TPA scanner bundle refused; keeping the last-known-good corpus",
				zap.String("origin", opts.offered.Path),
				zap.String("reason", sel.offeredRej.Reason),
				zap.String("detail", sel.offeredRej.Detail),
				zap.String("candidate_digest", sel.offeredDigest))
		}
		if sel.offeredRej.Reason != RejectStateCommitFailed {
			rt.refusedOffer = offerKey(policy, opts.offered)
		}
	}
	if sel.deactivated != nil {
		rt.recordRejectionLocked(sel.deactivated, sel.deactivatedDigest, false)
	}

	// rt.state is only ever advanced by persistLocked, i.e. by a transition
	// that was actually recorded; the normalized view (st) is recomputed on
	// every run and is used here for status only.
	rt.policy = policy
	rt.clean = clean
	rt.loadErr = loadErr
	if sel.serving == nil || sel.serving.embedded {
		rt.active = nil
	} else {
		rt.active = sel.serving
	}

	rt.publishLocked(policy, st, sel.serving, log, now)
}

// ensureLoadedLocked loads the persisted state the first time a run needs it
// (and again after a failed load), then reconciles the LKG directory.
func (rt *bundleRuntime) ensureLoadedLocked(log *zap.Logger) {
	if rt.loaded {
		return
	}
	rt.loadFailed = false
	if rt.store != nil {
		st, err := rt.store.LoadTPABundleState()
		if err != nil {
			// Start from nil: the anchors outside the data directory (publisher
			// baseline, operator baseline, require_signed, root declaration)
			// still hold. Nothing is recorded against a state that could not be
			// read, and the load is retried on the next run.
			log.Error("could not load the TPA bundle anti-downgrade state; running against the config and binary anchors only",
				zap.Error(err))
			rt.state = nil
			rt.loadFailed = true
			return
		}
		rt.state = st
	}
	rt.loaded = true
	rt.reconcileLocked(rt.state, log)
}

// reconcileLocked deletes LKG debris no live state record references.
func (rt *bundleRuntime) reconcileLocked(st *TPABundleState, log *zap.Logger) {
	if rt.dataDir == "" || rt.loadFailed {
		return
	}
	removed, err := lkgReconcile(rt.dataDir, st)
	if err != nil {
		log.Warn("could not fully reconcile the TPA last-known-good directory", zap.Error(err))
	}
	if len(removed) > 0 {
		log.Info("removed unreferenced TPA last-known-good files", zap.Strings("files", removed))
	}
}

// warnPolicyLocked logs the dropped-signer notes once per distinct policy.
func (rt *bundleRuntime) warnPolicyLocked(policy *bundlePolicy, log *zap.Logger) {
	if rt.warnedFP == policy.Fingerprint {
		return
	}
	rt.warnedFP = policy.Fingerprint
	for _, w := range policy.Warnings {
		log.Warn("TPA bundle trust configuration: " + w)
	}
}

// canSkipLocked is the idempotence short-circuit (design §6.3): the same policy
// and the same bytes already serving mean there is nothing to decide, commit,
// record or log.
func (rt *bundleRuntime) canSkipLocked(policy *bundlePolicy, cand *Candidate) bool {
	if !rt.clean || rt.policy == nil || rt.policy.Fingerprint != policy.Fingerprint || rt.loadErr != "" {
		return false
	}
	if !externalConfigured(policy, rt.state) {
		return rt.active == nil
	}
	if policy.Path == "" {
		// A fetch-only install has no file to compare. The run is a no-op iff
		// the serving external bundle is the one the committed state records
		// (or neither exists); a recorded bundle that is not serving (a
		// transient LKG read failure) is retried (tasks B9).
		recorded := rt.state != nil && rt.state.Active != nil
		if rt.active == nil {
			return !recorded
		}
		return recorded && sameActive(rt.state.Active, rt.active)
	}
	if cand == nil || rt.active == nil {
		return false
	}
	return rt.active.cand.SidecarPresent == cand.SidecarPresent &&
		bytes.Equal(rt.active.cand.Bundle, cand.Bundle) &&
		bytes.Equal(rt.active.cand.Sidecar, cand.Sidecar)
}

// offerKey identifies an offered pair under a policy, for the negative cache.
func offerKey(policy *bundlePolicy, c *Candidate) string {
	return policy.Fingerprint + "|" + digestHex(c.Bundle) + "|" + digestHex(c.Sidecar) + "|" + strconv.FormatBool(c.SidecarPresent)
}

// offerUnchangedLocked is the fetch leg's idempotence check, made under the
// lock and before anything is parsed or compiled: the offered pair is
// byte-identical to the serving external bundle, or to the pair the pipeline
// refused last time under this very policy (tasks B7). Either way there is
// nothing to decide.
func (rt *bundleRuntime) offerUnchangedLocked(policy *bundlePolicy, c *Candidate) bool {
	if a := rt.active; a != nil &&
		a.cand.SidecarPresent == c.SidecarPresent &&
		bytes.Equal(a.cand.Bundle, c.Bundle) &&
		bytes.Equal(a.cand.Sidecar, c.Sidecar) {
		return true
	}
	return rt.refusedOffer != "" && rt.refusedOffer == offerKey(policy, c)
}

// chooseLocked picks what serves, first that succeeds (design §6.4):
//
//	a. the fresh candidate: the configured-path candidate (read once, evaluated
//	   in normal mode), or, for an offered run, the offered fetch candidate;
//	b. the bundle already serving, re-validated under the new policy and state;
//	c. the active record from the LKG files (a fresh process);
//	d. the embedded default.
//
// With no external source configured (no path, no fetch, no fetched bundle
// serving) the operator removed the external bundle, so b and c are skipped too
// (externalConfigured).
func (rt *bundleRuntime) chooseLocked(policy *bundlePolicy, nst *TPABundleState, cand *Candidate, readRej *BundleRejection, offered *Candidate, log *zap.Logger) selection {
	var sel selection
	if offered != nil {
		// Signed-only is enforced for THIS evaluation (MustSign), never by
		// mutating the policy: a policy copy with RequireSigned would also reach
		// fallback step b and deactivate a serving unsigned file-drop bundle.
		acc, rej := evaluateCandidate(*offered, evalEnv{Policy: policy, State: nst, Mode: evalNormal, MustSign: true})
		if rej != nil {
			sel.offeredRej, sel.offeredDigest = rej, digestHex(offered.Bundle)
		} else {
			sel.serving = newExternalServing(*offered, acc)
			sel.fromPath = true
			return sel
		}
	} else if policy.Path != "" {
		switch {
		case readRej != nil:
			sel.attempt = readRej
		default:
			acc, rej := evaluateCandidate(*cand, evalEnv{Policy: policy, State: nst, Mode: evalNormal})
			if rej != nil {
				sel.attempt, sel.attemptDigest = rej, digestHex(cand.Bundle)
			} else {
				sel.serving = newExternalServing(*cand, acc)
				sel.fromPath = true
				return sel
			}
		}
	}
	rt.fallbackLocked(policy, nst, &sel, log)
	return sel
}

// fallbackLocked fills sel.serving from steps b, c and d.
func (rt *bundleRuntime) fallbackLocked(policy *bundlePolicy, nst *TPABundleState, sel *selection, log *zap.Logger) {
	sel.serving, sel.fromPath = nil, false
	if externalConfigured(policy, nst) {
		switch {
		case rt.active != nil:
			acc, rej := evaluateCandidate(rt.active.cand, evalEnv{Policy: policy, State: nst, Mode: evalLKG, Reuse: rt.active.acc})
			if rej == nil {
				sv := newExternalServing(rt.active.cand, acc)
				sv.generatedAt = rt.active.generatedAt
				sel.serving = sv
				return
			}
			sel.deactivated, sel.deactivatedDigest = rej, rt.active.digest
			log.Warn("serving TPA scanner bundle deactivated: it no longer passes validation under the current policy",
				zap.String("full_digest", rt.active.digest),
				zap.String("reason", rej.Reason),
				zap.String("detail", rej.Detail))
		case nst.Active != nil && rt.dataDir != "" && !rt.loadFailed:
			sv, rej, transient := rt.restoreFromLKGLocked(policy, nst, log)
			if sv != nil {
				sel.serving = sv
				return
			}
			if rej != nil {
				sel.deactivated, sel.deactivatedDigest = rej, nst.Active.FullDigest
				sel.keepActive = transient
			}
		}
	}

	e := rt.embeddedLocked()
	if e.err != nil {
		sel.serving = nil
		log.Warn("embedded TPA scanner bundle failed to load; continuing without bundle-backed checks", zap.Error(e.err))
		return
	}
	sel.serving = embeddedServing(e, policy)
}

// restorePinLocked is the last external fallback, tried only when nothing
// external was chosen: when the ACTIVE root's rollback pin binds and names a
// file target, its content-addressed last-known-good files are re-validated in
// LKG mode (the pin allow-rule admits the target below the watermark) and, if
// they pass, serve again and are recorded as the Active record.
//
// This is what makes FR-004b/FR-010 true across a root round trip: switching the
// active root away deactivates the pinned bundle (it no longer belongs to the
// active authority) and clears Active, but the pin is only SUSPENDED, so
// switching back must restore the pinned bundle unchanged rather than leave the
// install on the embedded default while the pin still binds.
func (rt *bundleRuntime) restorePinLocked(policy *bundlePolicy, nst *TPABundleState, sel *selection, log *zap.Logger, now time.Time) {
	if !externalConfigured(policy, nst) || rt.dataDir == "" || rt.loadFailed || sel.keepActive || sel.serving == nil || !sel.serving.embedded {
		return
	}
	a := nst.Authorities[policy.ActiveRoot]
	if !pinBinds(a) || a.Pin.Embedded || a.Pin.FullDigest == sel.deactivatedDigest {
		return
	}
	pin := a.Pin
	source := pin.Source
	if source == "" {
		source = BundleSourceFile
	}
	// A pinned FILE bundle belongs to the configured path: with the path
	// removed the operator removed the bundle, and an enabled fetch does not
	// make the pin a reason to bring it back (see externalConfigured).
	if source == BundleSourceFile && policy.Path == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), bundleReadBudget)
	defer cancel()
	cand, err := lkgReadPair(ctx, rt.dataDir, pin.FullDigest, pin.SidecarDigest, pin.SidecarDigest != "", source, pin.Path)
	if err != nil {
		log.Warn("rollback-pinned TPA bundle cannot be restored: its last-known-good files are unavailable",
			zap.String("full_digest", pin.FullDigest), zap.Error(err))
		return
	}
	acc, rej := evaluateCandidate(cand, evalEnv{Policy: policy, State: nst, Mode: evalLKG})
	if rej != nil {
		log.Warn("rollback-pinned TPA bundle cannot be restored: it no longer passes validation",
			zap.String("full_digest", pin.FullDigest), zap.String("reason", rej.Reason), zap.String("detail", rej.Detail))
		return
	}
	sv := newExternalServing(cand, acc)
	sv.generatedAt = acc.Meta.GeneratedAt
	sel.serving = sv
	sel.fromPath = false
	sel.restored = &TPABundleActiveRecord{
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
		Rollback:          true,
	}
	log.Warn("TPA scanner bundle restored from its rollback pin",
		zap.String("authority", acc.Authority),
		zap.String("full_digest", acc.FullDigest),
		zap.Uint64("sequence", acc.Sequence))
}

// restoreFromLKGLocked re-validates the state's Active record from its LKG
// files. A missing or substituted file, or a record that no longer validates,
// yields a rejection and no serving bundle. The third result is true when the
// failure is transient (an I/O error or timeout, not proof the release is gone):
// the caller must then keep the active record and its files.
func (rt *bundleRuntime) restoreFromLKGLocked(policy *bundlePolicy, nst *TPABundleState, log *zap.Logger) (*servingBundle, *BundleRejection, bool) {
	rec := nst.Active
	ctx, cancel := context.WithTimeout(context.Background(), bundleReadBudget)
	defer cancel()
	cand, err := lkgRead(ctx, rt.dataDir, rec)
	if err != nil {
		rej := &BundleRejection{At: time.Now()}
		transient := false
		var ce *candidateReadError
		switch {
		case errors.Is(err, errLKGSubstituted):
			rej.Reason, rej.Detail = RejectTamper, "last-known-good file does not match its recorded digest "+shortDigest(rec.FullDigest)
		case errors.As(err, &ce):
			rej.Reason, rej.Detail = ce.Reason, "last-known-good: "+ce.Detail
			transient = !ce.NotExist && (ce.Reason == RejectReadError || ce.Reason == RejectReadTimeout)
		default:
			rej.Reason, rej.Detail = RejectReadError, "last-known-good: "+err.Error()
			transient = true
		}
		log.Warn("recorded last-known-good TPA bundle is unavailable; falling back to the embedded default",
			zap.String("full_digest", rec.FullDigest), zap.String("reason", rej.Reason), zap.String("detail", rej.Detail),
			zap.Bool("record_kept", transient))
		return nil, rej, transient
	}
	acc, rej := evaluateCandidate(cand, evalEnv{Policy: policy, State: nst, Mode: evalLKG})
	if rej != nil {
		log.Warn("recorded last-known-good TPA bundle no longer passes validation; falling back to the embedded default",
			zap.String("full_digest", rec.FullDigest), zap.String("reason", rej.Reason), zap.String("detail", rej.Detail))
		return nil, rej, false
	}
	sv := newExternalServing(cand, acc)
	sv.generatedAt = rec.GeneratedAt
	return sv, nil, false
}

// persistLocked makes one transition durable and then publishes nothing: LKG
// files first (when an external bundle is entering service), then the single
// state transaction (state + history row), with the crash seams between. On
// success it updates the state mirror and reconciles the LKG directory.
func (rt *bundleRuntime) persistLocked(st *TPABundleState, row *TPABundleActivation, sv *servingBundle, log *zap.Logger) error {
	if rt.loadFailed && row != nil {
		// A NEW activation needs a durable watermark and ratchet. With the state
		// unreadable nothing can be recorded, and activating anyway would let a
		// later restart (with a readable store holding an older watermark) accept
		// an older release than the one that had been serving. Refuse, as for a
		// failed commit.
		return errors.New("the anti-downgrade state could not be loaded, so the activation cannot be recorded")
	}
	if sv != nil && !sv.embedded && rt.dataDir != "" && !rt.loadFailed {
		if err := lkgWrite(rt.dataDir, sv.digest, sv.cand.Bundle, sv.cand.Sidecar, sv.cand.SidecarPresent); err != nil {
			rt.reconcileLocked(rt.state, log)
			return fmt.Errorf("last-known-good write: %w", err)
		}
	}
	if lkgAfterWriteHook != nil {
		lkgAfterWriteHook()
	}
	if rt.store != nil {
		// With the state unreadable (loadFailed) nothing is recorded: the run
		// serves against the config and binary anchors only.
		if !rt.loadFailed {
			if err := rt.store.CommitTPABundleState(st, row); err != nil {
				rt.reconcileLocked(rt.state, log)
				return fmt.Errorf("state commit: %w", err)
			}
		}
	} else if row != nil {
		rt.histMu.Lock()
		rt.history = append(rt.history, *row)
		rt.histMu.Unlock()
	}
	rt.state = st
	// The state moved (a watermark, a pin, a deny entry, the active record): a
	// refusal made against the old state is not evidence about the new one.
	rt.refusedOffer = ""
	if stateAfterCommitHook != nil {
		stateAfterCommitHook()
	}
	rt.reconcileLocked(st, log)
	return nil
}

// recordRejectionLocked stores rej as the last rejection. A repeat of the same
// (reason, digest) is NOT re-recorded (so a file that stays bad does not churn
// the timestamp or the log) unless force is set. It reports whether rej was new.
func (rt *bundleRuntime) recordRejectionLocked(rej *BundleRejection, digest string, force bool) bool {
	key := rej.Reason + "|" + digest
	if !force && rt.lastRej != nil && rt.lastRejKey == key {
		return false
	}
	cp := *rej
	rt.lastRej, rt.lastRejKey = &cp, key
	return true
}

// publishLocked builds the BundleInfo for sv and publishes (check, info) under
// one activeBundleMu acquisition.
func (rt *bundleRuntime) publishLocked(policy *bundlePolicy, st *TPABundleState, sv *servingBundle, log *zap.Logger, now time.Time) {
	rt.publishFactsLocked(st, sv)
	if sv == nil {
		// The embedded default itself is broken: as before, serve without
		// bundle-backed checks and say so.
		e := rt.embeddedLocked()
		msg := "embedded bundle unavailable"
		if e.err != nil {
			msg = e.err.Error()
		}
		storeBundle(nil, BundleInfo{Source: BundleSourceEmbedded, LoadedAt: now, LoadError: msg, LastRejection: rt.lastRejCopy()})
		return
	}

	if id := sv.id(); id != rt.servingID {
		rt.servingID, rt.loadedAt = id, now
	}
	info := rt.buildInfoLocked(policy, st, sv)
	storeBundle(sv.check, info)

	key := fmt.Sprintf("%s|%s|%s|%t|%s|%t|%t", info.Source, info.FullDigest, info.Authority, info.CoverageDegraded, info.DegradedReason, info.Pinned, info.Rollback)
	if key != rt.lastLogKey {
		rt.lastLogKey = key
		logBundle(log, info)
	}
	rt.logDegradedLocked(log, info)
}

// publishedFacts is the small, immutable snapshot the fetch leg reads WITHOUT
// bundleRT.mu (tasks B7, B10): per-authority "already satisfied" sequences for
// the pre-download hint skip, and whether the anti-downgrade state is
// unreadable. Nothing outside the runtime reads rt.state or rt.active directly.
type publishedFacts struct {
	satisfied  map[string]uint64
	loadFailed bool
}

var bundleFacts atomic.Pointer[publishedFacts]

// publishFactsLocked refreshes the snapshot from the state being published.
// An authority's satisfied sequence is its watermark, except when the release
// that set the watermark is not the one serving: then that release itself is
// still wanted (a lost last-known-good file heals by re-fetching it), so
// satisfied is one below.
func (rt *bundleRuntime) publishFactsLocked(st *TPABundleState, sv *servingBundle) {
	f := &publishedFacts{satisfied: make(map[string]uint64, len(bundleAuthorities)), loadFailed: rt.loadFailed}
	if st != nil {
		for name, a := range st.Authorities {
			if a == nil {
				continue
			}
			v := a.Watermark
			serving := sv != nil && !sv.embedded && sv.verified && sv.authority == name && sv.hasSequence && sv.sequence == a.Watermark
			if a.WatermarkSetBy != "" && !serving && v > 0 {
				v--
			}
			f.satisfied[name] = v
		}
	}
	bundleFacts.Store(f)
}

// publishedWatermark returns the sequence authority already satisfies, for the
// fetch leg's hint skip.
func publishedWatermark(authority string) (uint64, bool) {
	f := bundleFacts.Load()
	if f == nil {
		return 0, false
	}
	v, ok := f.satisfied[authority]
	return v, ok
}

// publishedStateUnavailable reports whether the last published run could not
// read the anti-downgrade state.
func publishedStateUnavailable() bool {
	f := bundleFacts.Load()
	return f != nil && f.loadFailed
}

func (rt *bundleRuntime) lastRejCopy() *BundleRejection {
	if rt.lastRej == nil {
		return nil
	}
	cp := *rt.lastRej
	return &cp
}

// logDegradedLocked logs every transition into, between and out of degraded
// coverage; a silently suspended auto-approval is the failure FR-020 exists to
// prevent.
func (rt *bundleRuntime) logDegradedLocked(log *zap.Logger, info BundleInfo) {
	key := ""
	if info.CoverageDegraded {
		key = info.DegradedReason
		if key == "" {
			key = "degraded"
		}
	}
	if key == rt.degradedKey {
		return
	}
	prev := rt.degradedKey
	rt.degradedKey = key
	switch {
	case key != "":
		log.Warn("TPA scanner bundle coverage degraded: scan-mode auto-approval is suspended and changes route to human review",
			zap.String("degraded_reason", key),
			zap.String("source", info.Source),
			zap.String("authority", info.Authority),
			zap.String("full_digest", info.FullDigest))
	case prev != "":
		log.Info("TPA scanner bundle coverage restored", zap.String("previous_degraded_reason", prev))
	}
}

// buildInfoLocked renders the operator-facing status of sv (design §6.6).
func (rt *bundleRuntime) buildInfoLocked(policy *bundlePolicy, st *TPABundleState, sv *servingBundle) BundleInfo {
	info := BundleInfo{
		BundleVersion:     sv.meta.BundleVersion,
		SchemaVersion:     sv.meta.SchemaVersion,
		SignatureCount:    sv.meta.SignatureCount,
		RunnableRules:     sv.stats.Runnable,
		SkippedRules:      sv.stats.Skipped,
		DeclaredSkipped:   sv.stats.Declared,
		GeneratedAt:       sv.generatedAt,
		FullDigest:        sv.digest,
		Fingerprint:       shortDigest(sv.digest),
		LoadedAt:          rt.loadedAt,
		Authority:         sv.authority,
		TrustEpoch:        sv.epoch,
		SignatureVerified: sv.verified,
		LastRejection:     rt.lastRejCopy(),
		LoadError:         rt.loadErr,
	}
	if sv.hasSequence {
		info.Sequence = strconv.FormatUint(sv.sequence, 10)
	}
	if sv.embedded {
		info.Source = BundleSourceEmbedded
	} else {
		info.Source = sv.cand.Source
		if info.Source == "" {
			info.Source = BundleSourceFile
		}
		// A fetched candidate's Path is scheme://host (B12); a file's is the
		// configured path.
		info.Path = sv.cand.Path
		if st != nil && st.Active != nil && st.Active.FullDigest == sv.digest && !st.Active.ActivatedAt.IsZero() {
			at := st.Active.ActivatedAt
			info.LastActivatedAt = &at
		}
	}

	// The embedded default IS the publisher baseline: when its manifest carries
	// no sequence the baseline is the declared fallback, and the default must
	// not read as "below" the very floor it seeds (FR-009b).
	embeddedSeq := policy.PublisherBaseline
	if e := rt.embeddedLocked(); e.err == nil && e.hasSeq {
		embeddedSeq = e.seq
	}
	info.CoverageDegraded, info.DegradedReason = computeCoverage(
		servingFacts{Embedded: sv.embedded, Verified: sv.verified, Authority: sv.authority}, st, policy, embeddedSeq)

	var pin *TPABundlePin
	if a := st.Authorities[policy.ActiveRoot]; a != nil {
		pin = a.Pin
	}
	info.Pinned = pin != nil
	if sv.embedded {
		info.Rollback = pin != nil && pin.Embedded
	} else {
		info.Rollback = st.Active != nil && st.Active.Rollback && st.Active.FullDigest == sv.digest
	}
	return info
}

// embeddedLocked returns the compiled embedded default, compiling it at most
// once per distinct embedded byte string.
func (rt *bundleRuntime) embeddedLocked() *embeddedEntry {
	digest := digestHex(bundledScannerBundle)
	if rt.embedded != nil && rt.embedded.digest == digest {
		return rt.embedded
	}
	e := &embeddedEntry{digest: digest}
	m, err := parseBundleManifest(bundledScannerBundle)
	if err == nil {
		e.meta = bundleMeta{
			BundleVersion:  m.BundleVersion,
			SchemaVersion:  m.SchemaVersion,
			SignatureCount: m.SignatureCount,
			GeneratedAt:    m.GeneratedAt,
		}
		if seq, _, serr := parseSequence(m.Sequence); serr == nil {
			e.seq, e.hasSeq = seq, true
		}
		e.check, e.stats, err = compileBundle(m)
	}
	e.err = err
	rt.embedded = e
	return e
}

// embeddedServing wraps the embedded default as a serving bundle. It is a
// publisher artifact that rides on binary integrity, so it is never
// "signature verified".
func embeddedServing(e *embeddedEntry, policy *bundlePolicy) *servingBundle {
	return &servingBundle{
		embedded:    true,
		check:       e.check,
		stats:       e.stats,
		meta:        e.meta,
		digest:      e.digest,
		authority:   AuthorityPublisher,
		sequence:    e.seq,
		hasSequence: e.hasSeq,
		epoch:       policy.currentEpoch(AuthorityPublisher),
		generatedAt: e.meta.GeneratedAt,
	}
}

// newExternalServing wraps an accepted external candidate.
func newExternalServing(cand Candidate, acc *acceptedBundle) *servingBundle {
	sv := &servingBundle{
		cand:          cand,
		acc:           acc,
		check:         acc.Check,
		stats:         acc.Stats,
		meta:          acc.Meta,
		digest:        acc.FullDigest,
		sidecarDigest: acc.SidecarDigest,
		authority:     acc.Authority,
		verified:      acc.Verified,
		epoch:         acc.TrustEpoch,
		generatedAt:   acc.Meta.GeneratedAt,
	}
	if acc.Verified {
		sv.sequence, sv.hasSequence = acc.Sequence, true
	}
	if sv.generatedAt == "" && cand.Path != "" {
		// The v0.1.0 bundle format carries no generated_at; the file's mtime is
		// the next-best freshness signal for an unsigned drop and is strictly
		// better than nothing. A signed bundle's freshness comes from its
		// signed manifest only.
		if !acc.Verified && !cand.ModTime.IsZero() {
			sv.generatedAt = cand.ModTime.UTC().Format(time.RFC3339)
		}
	}
	return sv
}

// sameActive reports whether the state's active record already names sv.
func sameActive(rec *TPABundleActiveRecord, sv *servingBundle) bool {
	return rec != nil && sv != nil && !sv.embedded &&
		rec.FullDigest == sv.digest &&
		rec.SidecarDigest == sv.sidecarDigest &&
		rec.Authority == sv.authority &&
		rec.SignatureVerified == sv.verified
}

// readFileCandidate reads the configured bundle and its sidecar under ONE read
// budget (design §6.4a). An absent sidecar (ENOENT only) means "unsigned"; any
// other sidecar failure is a rejection, never a fallback to the unsigned path.
func readFileCandidate(path string) (*Candidate, *BundleRejection) {
	ctx, cancel := context.WithTimeout(context.Background(), bundleReadBudget)
	defer cancel()

	sidecar, serr := readCandidateFile(ctx, path+".sig", MaxSidecarBytes)
	present := true
	if serr != nil {
		var ce *candidateReadError
		if errors.As(serr, &ce) && ce.NotExist {
			present, sidecar = false, nil
		} else {
			return nil, readRejection(serr, "sidecar")
		}
	}
	data, modTime, berr := readCandidateFileStat(ctx, path, MaxBundleBytes)
	if berr != nil {
		if present {
			// FR-008 stage order: the sidecar's grammar outranks the bundle's byte
			// ceiling, so a malformed sidecar is reported as such even when the
			// bundle was too large to read.
			if _, perr := bundlesig.ParseSidecar(sidecar); perr != nil {
				reason, detail := sidecarRejectionFields(perr)
				return nil, &BundleRejection{Reason: reason, Detail: detail, At: time.Now()}
			}
		}
		return nil, readRejection(berr, "bundle")
	}
	return &Candidate{Bundle: data, Sidecar: sidecar, SidecarPresent: present, Source: BundleSourceFile, Path: path, ModTime: modTime}, nil
}

// readRejection turns a candidate read failure into a recorded rejection.
func readRejection(err error, what string) *BundleRejection {
	rej := &BundleRejection{At: time.Now()}
	var ce *candidateReadError
	if errors.As(err, &ce) {
		rej.Reason = ce.Reason
		rej.Detail = ce.Detail
		if ce.Reason == RejectCeilingExceeded {
			rej.Detail = what + ": " + ce.Detail
		}
		return rej
	}
	rej.Reason, rej.Detail = RejectReadError, err.Error()
	return rej
}
