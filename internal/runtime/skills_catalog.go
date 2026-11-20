package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// Spec 125 (skills over MCP), PR-1, plan D6: the runtime skills catalog.
//
// One refresh of one server lists the skills the upstream serves, reads and
// verifies every file against the listed digest and size, stages the text
// blobs and applies the records in one transaction. It never runs inside tool
// discovery: a slow file read must not delay tool indexing, and a skills
// failure must not look like a discovery failure (it only sets Error on the
// server's skills status).

const (
	// skillsRefreshTimeout bounds one whole refresh of one server.
	skillsRefreshTimeout = 2 * time.Minute
	// skillsRequestTimeout bounds one upstream file read.
	skillsRequestTimeout = 30 * time.Second
)

var (
	// ErrSkillNotFound: no skill with that path on that server.
	ErrSkillNotFound = errors.New("skill not found")
	// ErrSkillFileNotFound: the skill has no such file in the requested copy.
	ErrSkillFileNotFound = errors.New("skill file not found")
	// ErrSkillFileBinary: the file is binary and not stored or served.
	ErrSkillFileBinary = errors.New("skill file is binary")
	// ErrSkillStoredCopyDamaged: the stored bytes no longer match the digest
	// the record promises.
	ErrSkillStoredCopyDamaged = errors.New("stored skill copy is damaged")

	// errSkillsCaptureStale reports a listing dropped because a newer one was
	// applied or its client was retired or reconnected. Not a status error.
	errSkillsCaptureStale = errors.New("skills listing capture is stale")
	// ErrSkillsRefreshSuperseded is what a caller of RefreshServerSkills sees
	// when its refresh wrote nothing because the connection it listed from was
	// replaced while it ran. The reconnect starts a refresh of its own.
	ErrSkillsRefreshSuperseded = errSkillsCaptureStale
	// errSkillsSwitchedOff ends a refresh whose server stopped being eligible
	// while it ran; like a stale capture it writes nothing and is not a failure.
	errSkillsSwitchedOff = errors.New("skills were switched off for this server during the refresh")
	// errSkillsClosed refuses a refresh once the runtime is shutting down.
	errSkillsClosed = errors.New("the runtime is shutting down; skills are not refreshed")
)

// SkillsServerStatus is the in-memory state of a server's last skills refresh.
type SkillsServerStatus struct {
	Supported   bool
	LastRefresh time.Time
	Error       string
	Truncated   bool
	EntryErrors int
	// ScansPending is how many usable skills the last refresh left unscanned
	// because its scan time was spent; a follow-up refresh scans them.
	ScansPending int
}

// skillsLimits are the per-refresh and per-server budgets. Zero means the
// constant from internal/skills; tests lower them (the constants stay put).
type skillsLimits struct {
	readPerRefresh  int64
	storedPerServer int64
	scanPerRefresh  time.Duration
	scanPerSkill    time.Duration
	followUpDelay   time.Duration
	// scanClock replaces time.Now for the per-refresh scan budget. Tests set
	// it: a budget measured on the real clock cannot be made to run out after
	// exactly one skill on a platform whose clock ticks coarsely (Windows).
	scanClock func() time.Time
}

func (l skillsLimits) now() time.Time {
	if l.scanClock != nil {
		return l.scanClock()
	}
	return time.Now()
}

func (l skillsLimits) scanRefresh() time.Duration {
	if l.scanPerRefresh > 0 {
		return l.scanPerRefresh
	}
	return skillsScanPerRefresh
}

func (l skillsLimits) scanSkill() time.Duration {
	if l.scanPerSkill > 0 {
		return l.scanPerSkill
	}
	return skillScanPerSkill
}

func (l skillsLimits) followUp() time.Duration {
	if l.followUpDelay > 0 {
		return l.followUpDelay
	}
	return skillsScanFollowUpGap
}

func (l skillsLimits) read() int64 {
	if l.readPerRefresh > 0 {
		return l.readPerRefresh
	}
	return skills.MaxReadPerRefresh
}

func (l skillsLimits) stored() int64 {
	if l.storedPerServer > 0 {
		return l.storedPerServer
	}
	return skills.MaxStoredTextPerServer
}

type skillsFlight struct {
	done    chan struct{}
	err     error
	joiners int
}

// skillsCatalogState is the runtime's skills bookkeeping, guarded by mu.
type skillsCatalogState struct {
	mu      sync.Mutex
	flights map[string]*skillsFlight
	// applied is the newest skills capture sequence applied per server, the
	// counterpart of the tool path's toolApprovalServerLock.applied (Spec 117
	// PR-3): an older capture writes nothing.
	applied map[string]uint64
	status  map[string]SkillsServerStatus
	// followUp marks servers whose last refresh left skills unscanned; the
	// flight starts one more refresh for them when it ends.
	followUp map[string]bool
	// rerun marks servers for which a background trigger arrived while a
	// refresh was running. That refresh may already have applied its
	// decisions (with the trust mode of before a configuration change, for
	// one), so joining it would lose the trigger: one more refresh runs.
	rerun map[string]bool

	// Tests only: budgets, a seam after the listing returns, and a switch for
	// the background triggers.
	limits        skillsLimits
	afterList     func(serverName string)
	scanFunc      skillScanFunc
	noAutoTrigger atomic.Bool

	// wg counts the background refreshes; closed stops new ones. Both change
	// under mu, so no refresh can be added once close has started to wait.
	wg     sync.WaitGroup
	closed bool

	// The agent search index (agent_search.go), built lazily and rebuilt when
	// indexGen moved past the generation it was built from.
	indexGen atomic.Uint64
	idxMu    sync.Mutex
	idxCache map[string]*skillIndexEntry
	idxTick  uint64
	idxDone  bool
}

// init allocates the maps; callers hold mu.
func (s *skillsCatalogState) init() {
	if s.flights == nil {
		s.flights = map[string]*skillsFlight{}
		s.applied = map[string]uint64{}
		s.status = map[string]SkillsServerStatus{}
		s.followUp = map[string]bool{}
		s.rerun = map[string]bool{}
	}
}

func (s *skillsCatalogState) joiners(serverName string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.flights[serverName]; f != nil {
		return f.joiners
	}
	return 0
}

// wait blocks until every background trigger has finished (tests only).
func (s *skillsCatalogState) wait() { s.wg.Wait() }

// close stops new background refreshes and joins the running ones. They write
// storage from their own goroutines, so Runtime.Close calls this before the
// storage manager starts to stop; the cancelled lifetime context makes each
// one return at its next upstream request.
func (s *skillsCatalogState) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.wg.Wait()
	s.closeIndex()
}

func (s *skillsCatalogState) setStatus(serverName string, mut func(*SkillsServerStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	st := s.status[serverName]
	mut(&st)
	s.status[serverName] = st
}

// SkillsStatus returns the in-memory status of the server's last refresh.
func (r *Runtime) SkillsStatus(serverName string) SkillsServerStatus {
	r.skills.mu.Lock()
	defer r.skills.mu.Unlock()
	return r.skills.status[serverName]
}

// --- availability ---

func skillsServerConfig(cfg *config.Config, name string) *config.ServerConfig {
	if cfg == nil {
		return nil
	}
	for _, sc := range cfg.Servers {
		if sc != nil && sc.Name == name {
			return sc
		}
	}
	return nil
}

// skillsServerExposed: the feature is on and the server may serve skills.
func skillsServerExposed(cfg *config.Config, sc *config.ServerConfig) bool {
	return cfg != nil && cfg.EnableSkills && sc != nil && sc.Enabled && !sc.Quarantined &&
		(sc.ExposeSkills == nil || *sc.ExposeSkills)
}

// skillAvailableIn is the one availability rule (plan D6), evaluated against
// a configuration: the feature is on, the server exists, is enabled, not
// quarantined and not excluded, and the record is approved, not blocked and
// listed.
func skillAvailableIn(cfg *config.Config, serverName string, rec *storage.SkillApprovalRecord) bool {
	if rec == nil || !skillsServerExposed(cfg, skillsServerConfig(cfg, serverName)) {
		return false
	}
	return rec.Status == storage.SkillStatusApproved && !rec.Blocked && rec.Listed
}

// skillsEligible reports whether the server's skills may be read right now,
// against the live config. A refresh asks again before every upstream request
// and before it writes, so turning the feature off or excluding the server
// stops a refresh that is already running.
func (r *Runtime) skillsEligible(serverName string) bool {
	cfg := r.Config()
	return skillsServerExposed(cfg, skillsServerConfig(cfg, serverName))
}

// SkillAvailable is the only place that answers "may an agent have this
// skill". It reads the live config at call time, so excluding a server or
// turning the feature off withdraws its skills at once.
func (r *Runtime) SkillAvailable(serverName string, rec *storage.SkillApprovalRecord) bool {
	return skillAvailableIn(r.Config(), serverName, rec)
}

// --- status of one record ---

// --- refresh ---

// RefreshServerSkills reads one server's skills into the catalog. It is
// single-flight per server: a call made while one runs joins it and gets its
// result. It returns nil without any upstream request unless the feature is
// on and the server may serve skills.
//
// The refresh belongs to the runtime, not to the caller that started it: it
// runs on the runtime's lifetime context with its own timeout, and ctx only
// bounds how long this caller waits. A caller that goes away (a closed HTTP
// request) therefore cannot cancel the refresh under the others who joined.
func (r *Runtime) RefreshServerSkills(ctx context.Context, serverName string) error {
	st := &r.skills
	st.mu.Lock()
	st.init()
	f := st.flights[serverName]
	switch {
	case f != nil:
		f.joiners++
	case st.closed:
		st.mu.Unlock()
		return errSkillsClosed
	default:
		f = &skillsFlight{done: make(chan struct{})}
		st.flights[serverName] = f
		st.wg.Add(1)
		go r.runSkillsFlight(serverName, f)
	}
	st.mu.Unlock()

	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runSkillsFlight runs one shared refresh to its end and publishes the result
// to everyone waiting on it.
func (r *Runtime) runSkillsFlight(serverName string, f *skillsFlight) {
	st := &r.skills
	defer st.wg.Done()
	defer func() {
		st.mu.Lock()
		delete(st.flights, serverName)
		rerun := st.rerun[serverName] && !st.closed
		again := st.followUp[serverName] && !st.closed
		delete(st.rerun, serverName)
		delete(st.followUp, serverName)
		st.mu.Unlock()
		close(f.done)
		switch {
		case rerun:
			r.triggerSkillsRefresh(serverName)
		case again:
			// After a pause, so a server that keeps changing its skills cannot
			// keep one core scanning without a break.
			time.AfterFunc(st.limits.followUp(), func() { r.triggerSkillsRefresh(serverName) })
		}
	}()
	ctx := r.lifetimeContext()
	if ctx == nil {
		ctx = context.Background()
	}
	f.err = r.refreshServerSkills(ctx, serverName)
}

// skillOutcome is what one skill's read produced.
type skillOutcome struct {
	uri, path, name, description string
	usable                       bool
	reason                       string
	files                        []storage.SkillFile
	hash                         string
	frontmatter                  json.RawMessage
	scan                         *storage.SkillScan
	// needScan: usable, and no earlier scan of this very content stands.
	// scanRetry: an earlier scan of this very content exists but has to be
	// repeated (degraded coverage); it stays on the record until the retry.
	needScan, scanRetry bool
}

// skillsBudget tracks the refresh-wide budgets across skills.
type skillsBudget struct {
	read, stored           int64
	readLimit, storedLimit int64
}

func (r *Runtime) refreshServerSkills(ctx context.Context, serverName string) error {
	cfg := r.Config()
	if !skillsServerExposed(cfg, skillsServerConfig(cfg, serverName)) {
		return nil
	}
	um, sm := r.upstreamManager, r.storageManager
	if um == nil || sm == nil {
		return nil
	}
	if !um.ServerSupportsSkills(serverName) {
		r.skills.setStatus(serverName, func(s *SkillsServerStatus) { s.Supported = false })
		return nil
	}

	// The timeout bounds the upstream requests. The scan that follows them is
	// local work with its own budget and runs on the caller's context, so a
	// slow server cannot eat the scan's time and the scan cannot fail a read.
	netCtx, cancel := context.WithTimeout(ctx, skillsRefreshTimeout)
	defer cancel()

	err := r.runSkillsRefresh(netCtx, ctx, serverName)
	if err != nil && (errors.Is(err, errSkillsSwitchedOff) || !r.skillsEligible(serverName)) {
		// Whatever stopped it, a refresh of a server whose skills are off by
		// now gets the answer it would have got at its start: nothing to do.
		// That also covers a request that was still in flight when the switch
		// changed and then failed or ran into the deadline.
		return nil
	}
	if err != nil && !errors.Is(err, errSkillsCaptureStale) && !errors.Is(err, errSkillsSwitchedOff) &&
		!errors.Is(err, commitview.ErrServerReplaced) && !errors.Is(err, ErrServerNotFound) {
		r.skills.setStatus(serverName, func(s *SkillsServerStatus) {
			s.Supported = true
			s.LastRefresh = time.Now()
			s.Error = err.Error()
		})
	}
	return err
}

func (r *Runtime) runSkillsRefresh(ctx, scanCtx context.Context, serverName string) error {
	um, sm := r.upstreamManager, r.storageManager
	inc, err := r.CaptureApprovalIncarnation(serverName, 0)
	if err != nil {
		return err
	}

	// Leftovers of an earlier refresh that staged blobs and never applied.
	var retained int64
	if err := r.WithServerApprovalLockAt(serverName, inc, func() error {
		_, n, e := sm.CollectUnreferencedSkillFiles(serverName)
		retained = n
		return e
	}); err != nil {
		return err
	}

	// The collection above may have waited for the lock, and the listing may
	// take many pages: the switches are asked again before the first request
	// and before every further page.
	eligible := func() bool { return r.skillsEligible(serverName) }
	if !eligible() {
		return errSkillsSwitchedOff
	}
	entries, entryErrs, capture, lerr := um.ListServerSkills(skills.WithContinueCheck(ctx, eligible), serverName)
	truncated := false
	var tr *skills.TruncatedError
	if lerr != nil {
		// Asked again here as well: a listing that fails or times out after
		// skills were switched off is not a failure anyone needs to hear of.
		// ErrNotServed: the connection itself was not asked (its own copy of
		// the switch can trail the configuration for a moment). No listing was
		// received, so nothing may be concluded from it.
		if errors.Is(lerr, skills.ErrStopped) || errors.Is(lerr, skills.ErrNotServed) || !eligible() {
			return errSkillsSwitchedOff
		}
		// A request that failed on a connection since retired or replaced
		// says nothing about the server; the reconnect reads its skills again.
		if errors.Is(lerr, managed.ErrInstanceRetired) {
			return fmt.Errorf("%w: %v", errSkillsCaptureStale, lerr)
		}
		if serr := supersededIfStale(lerr, capture); serr != lerr {
			return serr
		}
		if !errors.As(lerr, &tr) {
			return fmt.Errorf("list skills: %w", lerr)
		}
		truncated = true
	}
	if len(entries) > skills.MaxSkillsPerServer {
		entries = entries[:skills.MaxSkillsPerServer]
		truncated = true
	}
	if h := r.skills.afterList; h != nil {
		h(serverName)
	}

	budget := &skillsBudget{
		stored:      retained,
		readLimit:   r.skills.limits.read(),
		storedLimit: r.skills.limits.stored(),
	}

	// What earlier refreshes verified, read without the lock: it only decides
	// which binary files need no second download. The apply below reads the
	// records again under the lock.
	known := map[string]*storage.SkillApprovalRecord{}
	if list, err := sm.ListSkillApprovals(serverName); err == nil {
		for _, p := range list {
			known[p.SkillURI] = p
		}
	}

	// A skill is looked up by its path, and two addresses can derive one path
	// (skill://x/pdf and https://x/pdf). Which of them the path would mean is
	// not for the server to decide by listing order, so none of them is usable
	// and each is addressed by its full address instead.
	// An address too long to be one is not stored under: it would be the key
	// of a record. It is counted with the entries that did not decode.
	skipped := 0
	kept := entries[:0:0]
	for _, e := range entries {
		if len(e.URI) > skills.MaxURIBytes {
			skipped++
			continue
		}
		kept = append(kept, e)
	}
	entries = kept

	pathUses := map[string]int{}
	for _, e := range entries {
		pathUses[skillPathOf(e.URI)]++
	}
	for _, ee := range entryErrs {
		if ee.URI != "" {
			pathUses[skillPathOf(ee.URI)]++
		}
	}

	var outcomes []skillOutcome
	for _, e := range entries {
		if p := skillPathOf(e.URI); pathUses[p] > 1 {
			o := describeSkill(e)
			o.path = e.URI
			outcomes = append(outcomes, unusableOutcome(o, &skills.UnusableError{Code: skills.ReasonInvalidURI,
				Detail: fmt.Sprintf("the path %q is used by more than one skill address in this listing", p)}))
			continue
		}
		o, err := r.readSkill(ctx, serverName, inc, e, known[e.URI], budget)
		if err != nil {
			return supersededIfStale(err, capture)
		}
		outcomes = append(outcomes, o)
	}
	// Every skill is read and its text is stored: scan what has no scan yet,
	// outside any lock and outside the upstream timeout.
	firstScans, unscanned, scansPending := r.scanSkillOutcomes(scanCtx, serverName, outcomes)

	// A listing entry that did not decode but carried a URI is still a skill
	// the person should see; without a URI there is nothing to key a record on.
	for _, ee := range entryErrs {
		if ee.URI == "" || len(ee.URI) > skills.MaxURIBytes {
			continue
		}
		path := skillPathOf(ee.URI)
		if pathUses[path] > 1 {
			path = ee.URI
		}
		outcomes = append(outcomes, skillOutcome{
			uri: ee.URI, path: path,
			reason: cutText(fmt.Sprintf("%s: %v", skills.ReasonInvalidURI, ee.Err), maxSkillReasonBytes),
		})
	}

	var counts map[string]int
	var changed bool
	var transitions []skillTransition
	err = r.WithServerApprovalLockAt(serverName, inc, func() error {
		if !r.skillsEligible(serverName) {
			return errSkillsSwitchedOff
		}
		if capture.Client != nil && !capture.Client.CaptureIsCurrent(capture) {
			return fmt.Errorf("%w: its client was retired or reconnected", errSkillsCaptureStale)
		}
		r.skills.mu.Lock()
		r.skills.init()
		applied := r.skills.applied[serverName]
		stale := capture.Seq <= applied && (capture.Seq != 0 || applied != 0)
		if !stale {
			r.skills.applied[serverName] = capture.Seq
		}
		r.skills.mu.Unlock()
		if stale {
			return fmt.Errorf("%w: sequence %d is not newer than %d", errSkillsCaptureStale, capture.Seq, applied)
		}

		prevList, err := sm.ListSkillApprovals(serverName)
		if err != nil {
			return err
		}
		prev := make(map[string]*storage.SkillApprovalRecord, len(prevList))
		for _, p := range prevList {
			prev[p.SkillURI] = p
		}

		// The live configuration is read here, under the lock, so a trust-mode
		// change that landed while the files were being read still decides.
		cfg := r.Config()
		sc := skillsServerConfig(cfg, serverName)
		if sc == nil {
			return errSkillsSwitchedOff
		}
		mode, quarantineOn := sc.EffectiveTrustMode(), cfg.IsQuarantineEnabled()

		now := time.Now()
		upserts := make([]*storage.SkillApprovalRecord, 0, len(outcomes))
		listed := make(map[string]struct{}, len(outcomes))
		for _, o := range outcomes {
			listed[o.uri] = struct{}{}
			p := prev[o.uri]
			rec := mergeSkillRecord(serverName, inc, capture.Seq, now, p, o)
			d := decideSkill(p, o.usable, o.hash, mode, quarantineOn, scanStateOf(rec.Scan, rec.CurrentHash))
			applySkillDecision(rec, d, now)
			if enforceSkillInvariant(rec, r.logger) {
				d.action = "" // nothing was approved; no row claims it was
			}
			if d.action != "" {
				transitions = append(transitions, skillTransition{
					uri: rec.SkillURI, path: rec.SkillPath, action: d.action,
					previousHash: approvedHashOf(p), currentHash: rec.CurrentHash,
					reason: rec.UnusableReason, approvedBy: rec.ApprovedBy,
				})
			}
			if skillRecordChanged(p, rec) {
				changed = true
			}
			upserts = append(upserts, rec)
		}
		var unlisted []string
		if !truncated { // absence from a cut-short listing proves nothing
			for uri, p := range prev {
				if _, ok := listed[uri]; !ok && p.Listed {
					unlisted = append(unlisted, uri)
					changed = true
				}
			}
			sort.Strings(unlisted)
		}
		// Blobs were staged as each skill was read; the apply's own
		// collection removes what no record references any more.
		if _, err := sm.ApplySkillRefresh(serverName, upserts, unlisted, nil); err != nil {
			return err
		}
		// A server that renames its skills on every listing would otherwise
		// leave a record behind each time, also when its listings are cut
		// short and prove nothing about what is gone: the records this
		// listing did not name are bounded, whatever their listed mark says.
		// The blobs the deleted records referenced are collected by the next
		// apply.
		if n, err := sm.PruneSkillApprovalsNotIn(serverName, listed, skills.MaxUnlistedSkillsPerServer); err != nil {
			return err
		} else if n > 0 {
			changed = true
			r.logger.Info("Dropped the oldest records of skills the server no longer lists",
				zap.String("server", serverName), zap.Int("dropped", n))
		}
		recs, err := sm.ListSkillApprovals(serverName)
		if err != nil {
			return err
		}
		counts = r.skillCounts(serverName, recs)
		return nil
	})
	if err != nil {
		return err
	}

	r.skills.setStatus(serverName, func(s *SkillsServerStatus) {
		s.Supported = true
		s.LastRefresh = time.Now()
		s.Error = ""
		s.Truncated = truncated
		s.EntryErrors = len(entryErrs) + skipped
		s.ScansPending = scansPending
	})
	// Scan time ran out with never-scanned skills left: the next refresh
	// scans them (what was scanned now is on the records and comes after
	// them). Only while this refresh gave at least one skill its first scan,
	// so every follow-up shrinks the never-scanned set and the chain ends;
	// skills that only wait for a retry of a degraded scan start none.
	if unscanned > 0 && firstScans > 0 {
		r.skills.mu.Lock()
		r.skills.init()
		r.skills.followUp[serverName] = true
		r.skills.mu.Unlock()
	}
	// Activity rows are emitted after the approval lock is released.
	for _, t := range transitions {
		r.emitSkillTransition(ctx, serverName, t)
	}
	if changed {
		r.publishSkillsChanged(serverName, counts)
	}
	return nil
}

// skillTransition is an automatic status transition to report as activity.
type skillTransition struct {
	uri, path, action, previousHash, currentHash, reason, approvedBy string
}

func (r *Runtime) emitSkillTransition(ctx context.Context, serverName string, t skillTransition) {
	// A refresh has no caller behind it: the row is attributed to the system.
	r.emitSkillQuarantineChangeCtx(context.WithoutCancel(ctx), serverName, t.path, t.uri, t.action, t.previousHash, t.currentHash, t.reason, t.approvedBy)
}

func approvedHashOf(p *storage.SkillApprovalRecord) string {
	if p == nil {
		return ""
	}
	return p.ApprovedHash
}

// skillRecordChanged: whether a refresh changed anything a subscriber shows.
func skillRecordChanged(p, rec *storage.SkillApprovalRecord) bool {
	if p == nil || p.Status != rec.Status || p.CurrentHash != rec.CurrentHash || !p.Listed ||
		p.UnusableReason != rec.UnusableReason || p.ApprovedHash != rec.ApprovedHash {
		return true
	}
	return scanVerdictOf(p.Scan) != scanVerdictOf(rec.Scan)
}

func scanVerdictOf(s *storage.SkillScan) string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("%s/%t/%s/%d", s.Verdict, s.Complete, s.Incomplete, len(s.Findings))
}

func (r *Runtime) skillCounts(serverName string, recs []*storage.SkillApprovalRecord) map[string]int {
	cfg := r.Config()
	counts := map[string]int{"available": 0, "pending": 0, "changed": 0, "unusable": 0, "blocked": 0}
	for _, rec := range recs {
		switch rec.Status {
		case storage.SkillStatusPending:
			counts["pending"]++
		case storage.SkillStatusChanged:
			counts["changed"]++
		case storage.SkillStatusUnusable:
			counts["unusable"]++
		}
		if rec.Blocked {
			counts["blocked"]++
		}
		if skillAvailableIn(cfg, serverName, rec) {
			counts["available"]++
		}
	}
	return counts
}

func skillPathOf(uri string) string {
	if _, p, _, err := skills.ParseSkillURI(uri); err == nil {
		return p
	}
	return uri
}

// mergeSkillRecord builds the record to write: what the refresh observed on
// top of the existing record, whose review fields (FirstSeen, Approved*,
// Blocked, Scan) are never touched here.
func mergeSkillRecord(serverName string, inc int64, seq uint64, now time.Time, prev *storage.SkillApprovalRecord, o skillOutcome) *storage.SkillApprovalRecord {
	rec := &storage.SkillApprovalRecord{}
	if prev != nil {
		*rec = *prev
	} else {
		rec.FirstSeen = now
	}
	rec.ServerName = serverName
	rec.SkillURI = o.uri
	rec.SkillPath = o.path
	rec.Name = o.name
	rec.Description = o.description
	rec.Listed = true
	rec.Incarnation = inc
	rec.UpdatedAt = now
	rec.CaptureSeq = seq
	rec.HashSchemaVersion = 1
	rec.Current = storage.SkillContent{Frontmatter: o.frontmatter, Files: o.files}
	rec.CurrentHash = o.hash
	if o.usable {
		rec.UnusableReason = ""
		rec.Scan = o.scan
	} else {
		rec.UnusableReason = o.reason
		// An unusable skill is not scanned; an earlier scan stays only while it
		// still describes the content on the record.
		if rec.Scan != nil && rec.Scan.Hash != o.hash {
			rec.Scan = nil
		}
	}
	// The status and any new approval come from decideSkill, applied by the
	// caller; until then the record keeps what it had.
	return rec
}

func unusableOutcome(o skillOutcome, u *skills.UnusableError) skillOutcome {
	o.usable = false
	o.reason = cutText(u.Error(), maxSkillReasonBytes)
	return o
}

// supersededIfStale turns an error into a superseded refresh when the
// connection the listing was captured on has been retired or replaced since:
// what a request on that connection ran into, a deadline included, says
// nothing about the server, and the reconnect reads its skills again.
func supersededIfStale(err error, capture managed.InventoryCapture) error {
	if err == nil || capture.Client == nil || capture.Client.CaptureIsCurrent(capture) {
		return err
	}
	return fmt.Errorf("%w: %v", errSkillsCaptureStale, err)
}

// What a server says about a skill is stored for display, and a server can say
// a lot. These bound it to a size worth showing; the verified content is not
// affected, only what a person reads in a list.
const (
	maxSkillNameBytes        = 256
	maxSkillDescriptionBytes = 4096
	maxSkillReasonBytes      = 1024
)

// cutText shortens s to at most limit bytes without splitting a character,
// and marks the cut.
func cutText(s string, limit int) string {
	const mark = "…"
	s = strings.ToValidUTF8(s, "\uFFFD")
	if len(s) <= limit {
		return s
	}
	cut := limit - len(mark)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + mark
}

// describeSkill is what a listing entry says about itself, before anything is
// read: identity, name, description, content hash and the listed files, so a
// skill that turns out unusable still shows what it listed.
func describeSkill(e skills.Entry) skillOutcome {
	o := skillOutcome{uri: e.URI, path: skillPathOf(e.URI)}
	o.name = o.path[strings.LastIndex(o.path, "/")+1:]
	if n, ok := e.Frontmatter["name"].(string); ok && n != "" {
		o.name = n
	}
	o.name = cutText(o.name, maxSkillNameBytes)
	desc, _ := e.Frontmatter["description"].(string)
	o.description = cutText(desc, maxSkillDescriptionBytes)
	// An over-long frontmatter makes the skill unusable; it is not kept.
	if fm, err := json.Marshal(e.Frontmatter); err == nil && len(fm) <= skills.MaxFrontmatterBytes {
		o.frontmatter = fm
	}
	o.hash, _ = skills.ContentHash(e)

	root, _, _, perr := skills.ParseSkillURI(e.URI)
	// The listed files, so a skill that fails midway still shows what it listed.
	listed := e.Files
	if len(listed) > skills.MaxFilesPerSkill { // such a skill is unusable; show the head of its list
		listed = listed[:skills.MaxFilesPerSkill]
	}
	o.files = make([]storage.SkillFile, 0, len(listed))
	for _, f := range listed {
		if len(f.URI) > skills.MaxURIBytes {
			continue
		}
		rel := f.URI
		if perr == nil {
			if p, err := skills.RelPath(root, f.URI); err == nil {
				rel = p
			}
		}
		o.files = append(o.files, storage.SkillFile{RelPath: rel, URI: f.URI, Digest: f.Digest, Size: f.Size})
	}
	return o
}

// readSkill reads one skill's files. A skill-level failure is an unusable
// outcome and not an error; only a cancelled context or a replaced server
// stops the whole refresh. At most one skill's file bytes are held at once.
func (r *Runtime) readSkill(ctx context.Context, serverName string, inc int64, e skills.Entry, prev *storage.SkillApprovalRecord, b *skillsBudget) (skillOutcome, error) {
	um, sm := r.upstreamManager, r.storageManager
	o := describeSkill(e)

	if u := e.Validate(); u != nil {
		return unusableOutcome(o, u), nil
	}

	var skillStored int64
	staged := map[string][]byte{} // digest hex -> new text blob, written once the skill verifies
	// The text is kept for the scan only when there is no scan of this very
	// content to reuse: a finished scan is never repeated.
	reuseScan := reusableSkillScan(prev, o.hash)
	for i, ref := range e.Files {
		sf := &o.files[i]
		digestHex := strings.TrimPrefix(ref.Digest, "sha256:")
		var content []byte
		have := false

		if blob, dup := staged[digestHex]; dup {
			// Same digest as a file this skill just read; still checked against
			// this file's own listed size.
			if u := skills.VerifyFile(ref, blob); u != nil {
				return unusableOutcome(o, u), nil
			}
			content, have = blob, true
			sf.Text = true
		} else if ok, _ := sm.HasSkillFile(serverName, ref.Digest); ok {
			// A stored row is re-verified against this listing's reference;
			// one that fails is treated as missing and fetched again.
			if blob, err := sm.GetSkillFile(serverName, ref.Digest); err == nil && skills.VerifyFile(ref, blob) == nil {
				content, have = blob, true
				sf.Text, sf.Stored = true, true
			}
		}

		if !have && ref.URI != e.URI && verifiedBinaryBefore(prev, ref) {
			continue // sf.Text and sf.Stored stay false
		}

		if !have {
			if !r.skillsEligible(serverName) {
				return o, errSkillsSwitchedOff
			}
			// Compared by subtraction so no sum of listed sizes can wrap.
			if ref.Size > b.readLimit-b.read {
				return unusableOutcome(o, &skills.UnusableError{Code: skills.ReasonServerBoundExceeded,
					Detail: fmt.Sprintf("%s: the refresh read budget of %d bytes is used up", sf.RelPath, b.readLimit)}), nil
			}
			if ref.Size > b.storedLimit-b.stored-skillStored {
				return unusableOutcome(o, &skills.UnusableError{Code: skills.ReasonServerBoundExceeded,
					Detail: fmt.Sprintf("%s: the server's stored text budget of %d bytes is used up", sf.RelPath, b.storedLimit)}), nil
			}
			b.read += ref.Size
			skillStored += ref.Size

			rctx, cancel := context.WithTimeout(ctx, skillsRequestTimeout)
			data, isText, _, rerr := um.ReadServerSkillFile(rctx, serverName, ref)
			cancel()
			if rerr != nil {
				if ctx.Err() != nil {
					return o, ctx.Err()
				}
				var ue *skills.UnusableError
				if errors.As(rerr, &ue) { // the client's own length check
					return unusableOutcome(o, ue), nil
				}
				return unusableOutcome(o, &skills.UnusableError{Code: skills.ReasonReadFailed, Detail: fmt.Sprintf("%s: %v", sf.RelPath, rerr)}), nil
			}
			if u := skills.VerifyFile(ref, data); u != nil {
				return unusableOutcome(o, u), nil
			}
			content, have = data, true
			if isText {
				sf.Text = true
				staged[digestHex] = data
			} else {
				skillStored -= ref.Size // binary is verified, not stored
			}
		}

		if ref.URI == e.URI {
			if !sf.Text {
				return unusableOutcome(o, &skills.UnusableError{Code: skills.ReasonFrontmatterInvalid, Detail: "SKILL.md is not text"}), nil
			}
			parsed, err := skills.ParseFrontmatter(content)
			if err != nil {
				return unusableOutcome(o, &skills.UnusableError{Code: skills.ReasonFrontmatterInvalid, Detail: err.Error()}), nil
			}
			if !skills.FrontmatterEqual(e.Frontmatter, parsed) {
				return unusableOutcome(o, &skills.UnusableError{Code: skills.ReasonFrontmatterMismatch, Detail: "SKILL.md frontmatter differs from the listing"}), nil
			}
		}
	}

	if len(staged) > 0 {
		if err := r.WithServerApprovalLockAt(serverName, inc, func() error {
			if !r.skillsEligible(serverName) {
				return errSkillsSwitchedOff
			}
			for digestHex, blob := range staged {
				if err := sm.PutSkillFile(serverName, digestHex, blob); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			if errors.Is(err, commitview.ErrServerReplaced) || errors.Is(err, errSkillsSwitchedOff) {
				return o, err
			}
			return unusableOutcome(o, &skills.UnusableError{Code: skills.ReasonReadFailed, Detail: "store: " + err.Error()}), nil
		}
		for i := range o.files {
			if o.files[i].Text {
				o.files[i].Stored = true
			}
		}
	}
	b.stored += skillStored
	o.usable = true

	// A finished scan of this very content is never repeated; anything else
	// is scanned after the reads, from the stored text (scanSkillOutcomes).
	switch {
	case reuseScan:
		o.scan = prev.Scan
	case prev != nil && prev.Scan != nil && prev.Scan.Hash == o.hash:
		o.scan, o.needScan, o.scanRetry = prev.Scan, true, true
	default:
		o.needScan = true
	}
	return o, nil
}

// verifiedBinaryBefore reports whether an earlier refresh read and verified
// this exact binary file: the same address, digest and size in the current
// content of a usable record. Binary files are not stored, so there are no
// bytes to check again, and they are never served; reading megabytes on every
// sweep to learn nothing new is the cost this avoids. An unusable record
// proves nothing about its files.
func verifiedBinaryBefore(prev *storage.SkillApprovalRecord, ref skills.FileRef) bool {
	if prev == nil || prev.Status == storage.SkillStatusUnusable {
		return false
	}
	for _, f := range prev.Current.Files {
		if f.URI == ref.URI {
			return !f.Text && f.Digest == ref.Digest && f.Size == ref.Size
		}
	}
	return false
}

// --- reads for later slices ---

// ListSkillRecords returns a server's skill records sorted by SkillPath.
func (r *Runtime) ListSkillRecords(serverName string) ([]*storage.SkillApprovalRecord, error) {
	if r.storageManager == nil {
		return nil, errors.New("storage not initialised")
	}
	recs, err := r.storageManager.ListSkillApprovals(serverName)
	if err != nil {
		return nil, err
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].SkillPath != recs[j].SkillPath {
			return recs[i].SkillPath < recs[j].SkillPath
		}
		return recs[i].SkillURI < recs[j].SkillURI
	})
	return recs, nil
}

// SkillFileText returns one stored text file of a skill, from its current or
// approved copy. The bytes are re-verified against the copy's digest and size
// before they are returned; a stored approved copy that fails marks the
// record unusable (stored_copy_damaged).
func (r *Runtime) SkillFileText(serverName, skillPath, relPath string, approvedCopy bool) ([]byte, storage.SkillFile, error) {
	recs, err := r.ListSkillRecords(serverName)
	if err != nil {
		return nil, storage.SkillFile{}, err
	}
	rec := skillByPath(recs, skillPath)
	if rec == nil {
		return nil, storage.SkillFile{}, fmt.Errorf("%w: %s:%s", ErrSkillNotFound, serverName, skillPath)
	}
	content := &rec.Current
	if approvedCopy {
		if rec.Approved == nil {
			return nil, storage.SkillFile{}, fmt.Errorf("%w: %s has no approved copy", ErrSkillFileNotFound, skillPath)
		}
		content = rec.Approved
	}
	return r.readSkillFileOf(serverName, rec, content, relPath, approvedCopy)
}

// readSkillFileOf reads one file of a copy of a record that the caller already
// holds: the file is matched by exact relative path, then re-verified against
// its digest and size. A failing verification marks the record unusable when
// markDamaged is set (the approved copy).
func (r *Runtime) readSkillFileOf(serverName string, rec *storage.SkillApprovalRecord, content *storage.SkillContent, relPath string, markDamaged bool) ([]byte, storage.SkillFile, error) {
	var file *storage.SkillFile
	for i := range content.Files {
		if content.Files[i].RelPath == relPath {
			file = &content.Files[i]
			break
		}
	}
	if file == nil {
		return nil, storage.SkillFile{}, fmt.Errorf("%w: %s in %s", ErrSkillFileNotFound, relPath, rec.SkillPath)
	}
	if !file.Text {
		return nil, *file, fmt.Errorf("%w: %s", ErrSkillFileBinary, relPath)
	}
	if !file.Stored {
		return nil, *file, fmt.Errorf("%w: %s is not stored", ErrSkillFileNotFound, relPath)
	}
	data, gerr := r.storageManager.GetSkillFile(serverName, file.Digest)
	var u *skills.UnusableError
	if gerr == nil {
		u = skills.VerifyFile(skills.FileRef{URI: file.URI, Digest: file.Digest, Size: file.Size}, data)
	}
	if gerr != nil || u != nil {
		detail := relPath
		if gerr != nil {
			detail += ": " + gerr.Error()
		} else {
			detail += ": " + u.Error()
		}
		if markDamaged {
			r.markSkillStoredCopyDamaged(serverName, rec.SkillURI, *file, detail)
		}
		return nil, *file, fmt.Errorf("%w: %s", ErrSkillStoredCopyDamaged, detail)
	}
	return data, *file, nil
}

// skillByPath finds the record a path means. One record with that path is the
// answer. Several can exist when an earlier, no longer listed record shares
// the path with a skill listed now under another address; then the path means
// the listed one, and nothing when that is not unique.
func skillByPath(recs []*storage.SkillApprovalRecord, path string) *storage.SkillApprovalRecord {
	var all, listed []*storage.SkillApprovalRecord
	for _, c := range recs {
		if c.SkillPath != path {
			continue
		}
		all = append(all, c)
		if c.Listed {
			listed = append(listed, c)
		}
	}
	switch {
	case len(all) == 1:
		return all[0]
	case len(listed) == 1:
		return listed[0]
	}
	return nil
}

// skillsByPath resolves every path of recs at once, by the rule of
// skillByPath: a path that names no single record is absent.
func skillsByPath(recs []*storage.SkillApprovalRecord) map[string]*storage.SkillApprovalRecord {
	type group struct {
		all, listed     int
		one, onlyListed *storage.SkillApprovalRecord
	}
	groups := map[string]*group{}
	for _, c := range recs {
		g := groups[c.SkillPath]
		if g == nil {
			g = &group{}
			groups[c.SkillPath] = g
		}
		g.all++
		g.one = c
		if c.Listed {
			g.listed++
			g.onlyListed = c
		}
	}
	out := make(map[string]*storage.SkillApprovalRecord, len(groups))
	for path, g := range groups {
		switch {
		case g.all == 1:
			out[path] = g.one
		case g.listed == 1:
			out[path] = g.onlyListed
		}
	}
	return out
}

// markSkillStoredCopyDamaged makes a skill unusable because a file of its
// approved copy failed verification when it was read. The read happened
// outside the approval lock, so the finding is checked again under it: the
// record must still approve that very file, and the stored bytes must still
// fail. A refresh that approved a new version in between (and collected the
// old version's bytes) therefore withdraws nothing.
func (r *Runtime) markSkillStoredCopyDamaged(serverName, skillURI string, file storage.SkillFile, detail string) {
	inc, ok := r.ServerIncarnation(serverName)
	if !ok {
		return
	}
	var counts map[string]int
	marked := false
	err := r.WithServerApprovalLockAt(serverName, inc, func() error {
		rec, err := r.storageManager.GetSkillApproval(serverName, skillURI)
		if err != nil {
			return err
		}
		if !approvesFile(rec, file) {
			return nil // another version is approved now
		}
		if data, gerr := r.storageManager.GetSkillFile(serverName, file.Digest); gerr == nil &&
			skills.VerifyFile(skills.FileRef{URI: file.URI, Digest: file.Digest, Size: file.Size}, data) == nil {
			return nil // the bytes are there and right: the first read raced a write
		}
		marked = true
		rec.Status = storage.SkillStatusUnusable
		rec.UnusableReason = skills.ReasonStoredCopyDamaged + ": " + detail
		rec.UpdatedAt = time.Now()
		if err := r.storageManager.SaveSkillApproval(rec); err != nil {
			return err
		}
		recs, err := r.storageManager.ListSkillApprovals(serverName)
		counts = r.skillCounts(serverName, recs)
		return err
	})
	if err != nil {
		r.logger.Warn("Could not mark a damaged stored skill copy unusable",
			zap.String("server", serverName), zap.String("skill", skillURI), zap.Error(err))
		return
	}
	if marked {
		r.publishSkillsChanged(serverName, counts)
	}
}

// approvesFile reports whether the record's approved copy contains exactly
// this file (same path, same digest).
func approvesFile(rec *storage.SkillApprovalRecord, file storage.SkillFile) bool {
	if rec == nil || rec.Approved == nil {
		return false
	}
	for i := range rec.Approved.Files {
		if rec.Approved.Files[i].RelPath == file.RelPath && rec.Approved.Files[i].Digest == file.Digest {
			return true
		}
	}
	return false
}

// --- triggers ---

// triggerSkillsRefresh starts a refresh of one server in the background. It
// never blocks and never runs inside tool discovery. With enable_skills off it
// does nothing.
func (r *Runtime) triggerSkillsRefresh(serverName string) {
	if r.skills.noAutoTrigger.Load() {
		return
	}
	if cfg := r.Config(); cfg == nil || !cfg.EnableSkills {
		return
	}
	ctx := r.lifetimeContext()
	if ctx == nil {
		ctx = context.Background()
	}
	r.skills.mu.Lock()
	if r.skills.closed {
		r.skills.mu.Unlock()
		return
	}
	r.skills.init()
	if r.skills.flights[serverName] != nil {
		// A refresh is running and may be past the point where it reads the
		// configuration and the listing: have it followed by one more.
		r.skills.rerun[serverName] = true
		r.skills.mu.Unlock()
		return
	}
	r.skills.wg.Add(1)
	r.skills.mu.Unlock()
	go func() {
		defer r.skills.wg.Done()
		err := r.RefreshServerSkills(ctx, serverName)
		if err != nil && !errors.Is(err, errSkillsCaptureStale) && !errors.Is(err, errSkillsSwitchedOff) &&
			!errors.Is(err, errSkillsClosed) && ctx.Err() == nil {
			r.logger.Warn("Skills refresh failed", zap.String("server", serverName), zap.Error(err))
		}
	}()
}

// triggerSkillsRefreshAll triggers a refresh for every known server; each
// refresh skips itself unless its server may serve skills.
func (r *Runtime) triggerSkillsRefreshAll() {
	if r.upstreamManager == nil {
		return
	}
	for _, name := range r.upstreamManager.GetAllServerNames() {
		r.triggerSkillsRefresh(name)
	}
}

// Notify each affected Skills tab when exposure changes. Only servers with held
// skills affect the review queue, which receives one notification per commit.
func (r *Runtime) announceSkillsExposureChanges(oldCfg, newCfg *config.Config) {
	if oldCfg == nil || newCfg == nil || r.storageManager == nil {
		return
	}
	seen := map[string]bool{}
	var names, reviewNames []string
	for _, cfg := range []*config.Config{oldCfg, newCfg} {
		for _, sc := range cfg.Servers {
			if sc == nil || seen[sc.Name] {
				continue
			}
			seen[sc.Name] = true
			oldSc, newSc := skillsServerConfig(oldCfg, sc.Name), skillsServerConfig(newCfg, sc.Name)
			wasRead, isRead := skillsServerExposed(oldCfg, oldSc), skillsServerExposed(newCfg, newSc)
			// The admin tab can display retained records even on an ineligible server.
			oldIncluded := oldSc != nil && (oldSc.ExposeSkills == nil || *oldSc.ExposeSkills)
			newIncluded := newSc != nil && (newSc.ExposeSkills == nil || *newSc.ExposeSkills)
			if wasRead != isRead || oldCfg.EnableSkills != newCfg.EnableSkills || oldIncluded != newIncluded || (oldSc == nil) != (newSc == nil) {
				names = append(names, sc.Name)
			}
			if wasRead == isRead {
				continue
			}
			readCfg, readSc := newCfg, newSc
			if wasRead {
				readCfg, readSc = oldCfg, oldSc
			}
			if pending, changed, _, err := r.heldSkills(readCfg, readSc); err == nil && pending+changed > 0 {
				reviewNames = append(reviewNames, sc.Name)
			}
		}
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	r.skills.bumpIndexGeneration()
	for _, name := range names {
		var counts map[string]int
		if recs, err := r.ListSkillRecords(name); err == nil {
			counts = r.skillCounts(name, recs)
		}
		r.publishEvent(newEvent(EventTypeSkillsChanged, map[string]any{"server": name, "counts": counts}))
	}
	if len(reviewNames) > 0 {
		sort.Strings(reviewNames)
		r.publishEvent(newEvent(EventTypeReviewChanged, map[string]any{"servers": reviewNames, ReviewChangedSkillsKey: true}))
	}
}

// onSkillsConfigChanged refreshes after a config change. A server is refreshed
// when it becomes eligible to serve skills (enable_skills turned on, its
// expose_skills turned on, it was enabled or left quarantine), when its
// effective trust mode changed (a pending skill may now be approved, or a
// changed one held), and, for every eligible server, when quarantine_enabled
// changed. Switching off or narrowing never refreshes.
func (r *Runtime) onSkillsConfigChanged(oldCfg, newCfg *config.Config) {
	// Any configuration change may alter which skills are available (the
	// switch, a server's expose_skills, enabled, quarantine, add or remove):
	// the search index is rebuilt at its next use (spec 125 D12).
	r.skills.bumpIndexGeneration()
	r.announceSkillsExposureChanges(oldCfg, newCfg)
	if newCfg == nil || !newCfg.EnableSkills {
		return
	}
	quarantineFlipped := oldCfg != nil && oldCfg.IsQuarantineEnabled() != newCfg.IsQuarantineEnabled()
	for _, sc := range newCfg.Servers {
		if sc == nil || !skillsServerExposed(newCfg, sc) {
			continue
		}
		oldSc := skillsServerConfig(oldCfg, sc.Name)
		becameEligible := !skillsServerExposed(oldCfg, oldSc)
		trustChanged := oldSc != nil && oldSc.EffectiveTrustMode() != sc.EffectiveTrustMode()
		if becameEligible || trustChanged || quarantineFlipped {
			r.triggerSkillsRefresh(sc.Name)
		}
	}
}
