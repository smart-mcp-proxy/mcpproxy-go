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

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
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
	// errSkillsSwitchedOff ends a refresh whose server stopped being eligible
	// while it ran; like a stale capture it writes nothing and is not a failure.
	errSkillsSwitchedOff = errors.New("skills were switched off for this server during the refresh")
)

// SkillsServerStatus is the in-memory state of a server's last skills refresh.
type SkillsServerStatus struct {
	Supported   bool
	LastRefresh time.Time
	Error       string
	Truncated   bool
	EntryErrors int
}

// skillsLimits are the per-refresh and per-server budgets. Zero means the
// constant from internal/skills; tests lower them (the constants stay put).
type skillsLimits struct {
	readPerRefresh  int64
	storedPerServer int64
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

	// Tests only: budgets, a seam after the listing returns, and a switch for
	// the background triggers.
	limits        skillsLimits
	afterList     func(serverName string)
	noAutoTrigger atomic.Bool

	// wg counts the background refreshes; closed stops new ones. Both change
	// under mu, so no refresh can be added once close has started to wait.
	wg     sync.WaitGroup
	closed bool
}

// init allocates the maps; callers hold mu.
func (s *skillsCatalogState) init() {
	if s.flights == nil {
		s.flights = map[string]*skillsFlight{}
		s.applied = map[string]uint64{}
		s.status = map[string]SkillsServerStatus{}
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

// nextSkillStatus decides a record's status after a refresh. PR-1 rule: a
// usable skill is pending unless it has an approved copy, in which case it is
// approved while the content equals that copy and changed once it differs.
// The full trust-mode table replaces this function in the next PR.
func nextSkillStatus(prev *storage.SkillApprovalRecord, usable bool, currentHash string) string {
	if !usable {
		return storage.SkillStatusUnusable
	}
	if prev != nil && prev.Approved != nil && prev.ApprovedHash != "" {
		if currentHash == prev.ApprovedHash {
			return storage.SkillStatusApproved
		}
		return storage.SkillStatusChanged
	}
	return storage.SkillStatusPending
}

// --- refresh ---

// RefreshServerSkills reads one server's skills into the catalog. It is
// single-flight per server: a call made while one runs joins it and gets its
// result. It returns nil without any upstream request unless the feature is
// on and the server may serve skills.
func (r *Runtime) RefreshServerSkills(ctx context.Context, serverName string) error {
	st := &r.skills
	st.mu.Lock()
	st.init()
	if f := st.flights[serverName]; f != nil {
		f.joiners++
		st.mu.Unlock()
		select {
		case <-f.done:
			return f.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f := &skillsFlight{done: make(chan struct{})}
	st.flights[serverName] = f
	st.mu.Unlock()

	defer func() {
		st.mu.Lock()
		delete(st.flights, serverName)
		st.mu.Unlock()
		close(f.done)
	}()
	f.err = r.refreshServerSkills(ctx, serverName)
	return f.err
}

// skillOutcome is what one skill's read produced.
type skillOutcome struct {
	uri, path, name, description string
	usable                       bool
	reason                       string
	files                        []storage.SkillFile
	hash                         string
	frontmatter                  json.RawMessage
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

	ctx, cancel := context.WithTimeout(ctx, skillsRefreshTimeout)
	defer cancel()

	err := r.runSkillsRefresh(ctx, serverName)
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

func (r *Runtime) runSkillsRefresh(ctx context.Context, serverName string) error {
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
		if errors.Is(lerr, skills.ErrStopped) {
			return errSkillsSwitchedOff
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
			return err
		}
		outcomes = append(outcomes, o)
	}
	// A listing entry that did not decode but carried a URI is still a skill
	// the person should see; without a URI there is nothing to key a record on.
	for _, ee := range entryErrs {
		if ee.URI == "" {
			continue
		}
		path := skillPathOf(ee.URI)
		if pathUses[path] > 1 {
			path = ee.URI
		}
		outcomes = append(outcomes, skillOutcome{
			uri: ee.URI, path: path,
			reason: fmt.Sprintf("%s: %v", skills.ReasonInvalidURI, ee.Err),
		})
	}

	var counts map[string]int
	var changed bool
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

		now := time.Now()
		upserts := make([]*storage.SkillApprovalRecord, 0, len(outcomes))
		listed := make(map[string]struct{}, len(outcomes))
		for _, o := range outcomes {
			listed[o.uri] = struct{}{}
			p := prev[o.uri]
			rec := mergeSkillRecord(serverName, inc, capture.Seq, now, p, o)
			if p == nil || p.Status != rec.Status || p.CurrentHash != rec.CurrentHash || !p.Listed ||
				p.UnusableReason != rec.UnusableReason {
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
		s.EntryErrors = len(entryErrs)
	})
	if changed {
		r.publishEvent(newEvent(EventTypeSkillsChanged, map[string]any{"server": serverName, "counts": counts}))
	}
	return nil
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
	rec.Status = nextSkillStatus(prev, o.usable, o.hash)
	if o.usable {
		rec.UnusableReason = ""
	} else {
		rec.UnusableReason = o.reason
	}
	return rec
}

func unusableOutcome(o skillOutcome, u *skills.UnusableError) skillOutcome {
	o.usable = false
	o.reason = u.Error()
	return o
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
	o.description, _ = e.Frontmatter["description"].(string)
	if fm, err := json.Marshal(e.Frontmatter); err == nil {
		o.frontmatter = fm
	}
	o.hash, _ = skills.ContentHash(e)

	root, _, _, perr := skills.ParseSkillURI(e.URI)
	// The listed files, so a skill that fails midway still shows what it listed.
	o.files = make([]storage.SkillFile, 0, len(e.Files))
	for _, f := range e.Files {
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
	var file *storage.SkillFile
	for i := range content.Files {
		if content.Files[i].RelPath == relPath {
			file = &content.Files[i]
			break
		}
	}
	if file == nil {
		return nil, storage.SkillFile{}, fmt.Errorf("%w: %s in %s", ErrSkillFileNotFound, relPath, skillPath)
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
		if approvedCopy {
			r.markSkillStoredCopyDamaged(serverName, rec.SkillURI, detail)
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

func (r *Runtime) markSkillStoredCopyDamaged(serverName, skillURI, detail string) {
	inc, ok := r.ServerIncarnation(serverName)
	if !ok {
		return
	}
	var counts map[string]int
	err := r.WithServerApprovalLockAt(serverName, inc, func() error {
		rec, err := r.storageManager.GetSkillApproval(serverName, skillURI)
		if err != nil {
			return err
		}
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
	r.publishEvent(newEvent(EventTypeSkillsChanged, map[string]any{"server": serverName, "counts": counts}))
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
	r.skills.wg.Add(1)
	r.skills.mu.Unlock()
	go func() {
		defer r.skills.wg.Done()
		err := r.RefreshServerSkills(ctx, serverName)
		if err != nil && !errors.Is(err, errSkillsCaptureStale) && !errors.Is(err, errSkillsSwitchedOff) && ctx.Err() == nil {
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

// onSkillsConfigChanged refreshes after a config change: every server when
// enable_skills turned on, and a server whose expose_skills turned on.
func (r *Runtime) onSkillsConfigChanged(oldCfg, newCfg *config.Config) {
	if newCfg == nil || !newCfg.EnableSkills {
		return
	}
	exposed := func(sc *config.ServerConfig) bool { return sc != nil && (sc.ExposeSkills == nil || *sc.ExposeSkills) }
	turnedOn := oldCfg == nil || !oldCfg.EnableSkills
	for _, sc := range newCfg.Servers {
		if sc == nil {
			continue
		}
		if turnedOn || (exposed(sc) && !exposed(skillsServerConfig(oldCfg, sc.Name))) {
			r.triggerSkillsRefresh(sc.Name)
		}
	}
}
