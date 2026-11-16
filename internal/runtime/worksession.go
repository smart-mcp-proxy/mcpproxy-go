package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Spec 082 — work sessions.
//
// An MCP "session" is a transport artifact: it is minted at the initialize
// handshake and regenerated every time the client reconnects, which real
// clients do every few minutes. It is not what a user means by "session".
//
// A WORK SESSION is what the user means: one client, working in one project,
// under one principal, for a continuous stretch of time — surviving the
// reconnects underneath it.
//
// We derive it, because MCP gives us nothing to read it from. This is the
// substitution SEP-2567 prescribes for gateways: the protocol's session id is
// being removed outright in the 2026-07-28 revision, and servers that used it
// as a correlation key are told to move to "the authenticated principal ... or
// a request-level correlation ID". Nothing here depends on the handshake or on
// Mcp-Session-Id, so it survives that change.

// DefaultWorkSessionIdleWindow is how long a work session may sit idle before
// the next activity is considered a new one.
//
// 30 minutes is a natural break in human work: long enough to survive a pause
// inside a task, short enough that yesterday's work does not merge into today's.
const DefaultWorkSessionIdleWindow = 30 * time.Minute

// WorkSessionIdentity is everything we know about who is doing the work.
// Every field is optional — the derivation degrades rather than failing.
type WorkSessionIdentity struct {
	// Principal is the authenticated identity (agent token subject / API key).
	// Empty when the proxy is used unauthenticated.
	Principal string

	// ClientName / ClientVersion come from the MCP clientInfo.
	ClientName    string
	ClientVersion string

	// WorkspaceRoot is the project the client is working in, as it disclosed
	// via MCP roots. Empty for clients that do not disclose it — measured:
	// Claude Code, Gemini and opencode do; Codex does not.
	WorkspaceRoot string

	// RunName is the label a caller sent in the X-MCPProxy-Run header (Spec 124
	// FR-003). It splits one principal's work into named runs. It is its own
	// key field, after the principal, so it can never join two principals the
	// way CorrelationID would.
	RunName string

	// CorrelationID is a client-supplied conversation id. No MCP client offers
	// one today (Claude Code has a conversation UUID but does not send it), so
	// this is always empty for now. When a client finally does, it OVERRIDES the
	// derivation below and the grouping stops being a heuristic.
	CorrelationID string
}

// key is the stable identity of a work session, before the idle window is
// applied. Two activities with the same key that are close enough in time
// belong to the same work session.
func (id WorkSessionIdentity) key() string {
	if id.CorrelationID != "" {
		return "cid:" + id.CorrelationID
	}
	// Deliberately NOT including the transport session id: the whole point is to
	// survive reconnects, which change it.
	return strings.Join([]string{
		id.Principal,
		id.ClientName,
		id.ClientVersion,
		id.WorkspaceRoot,
		id.RunName,
	}, "\x00")
}

// isEmpty reports whether we know nothing at all about the client. We refuse to
// group such activity, rather than lumping every anonymous caller together into
// one meaningless "session".
func (id WorkSessionIdentity) isEmpty() bool {
	return id.CorrelationID == "" &&
		id.Principal == "" &&
		id.ClientName == "" &&
		id.WorkspaceRoot == "" &&
		id.RunName == ""
}

// WorkspaceName is the display name of the project — the final path element.
//
// Only the basename is ever shown or stored for display: the full path is a
// local filesystem path and is nobody's business but the user's. It is never
// sent to telemetry.
func WorkspaceName(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	root = strings.TrimPrefix(root, "file://")
	root = strings.TrimRight(root, "/")
	if root == "" {
		return ""
	}
	return filepath.Base(root)
}

// workSessionEntry is a live work session in the tracker.
type workSessionEntry struct {
	id           string
	lastActivity time.Time
	startedAt    time.Time
}

// WorkSessionTracker maps an identity to a stable work-session id, opening a new
// one when the idle window lapses. Safe for concurrent use.
type WorkSessionTracker struct {
	mu         sync.Mutex
	sessions   map[string]*workSessionEntry // identity key -> live work session
	idleWindow time.Duration
	now        func() time.Time // injectable for tests

	// named counts the named work sessions opened per principal on namedDay
	// (Spec 124 FR-008). Past MaxNamedWorkSessionsPerDay a principal's run
	// name is ignored, so a caller cannot mint sessions without bound.
	named    map[string]int
	namedDay string
}

// MaxNamedWorkSessionsPerDay bounds how many run-named work sessions one
// principal may open in one UTC day (Spec 124 FR-008).
const MaxNamedWorkSessionsPerDay = 1000

// NewWorkSessionTracker returns a tracker with the given idle window.
// A zero or negative window falls back to the default.
func NewWorkSessionTracker(idleWindow time.Duration) *WorkSessionTracker {
	if idleWindow <= 0 {
		idleWindow = DefaultWorkSessionIdleWindow
	}
	return &WorkSessionTracker{
		sessions:   make(map[string]*workSessionEntry),
		idleWindow: idleWindow,
		now:        time.Now,
	}
}

// SetNowForTest replaces the tracker's clock. Tests only.
func (t *WorkSessionTracker) SetNowForTest(now func() time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.now = now
}

// SetIdleWindow updates the idle window (hot-reloadable config).
func (t *WorkSessionTracker) SetIdleWindow(d time.Duration) {
	if d <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.idleWindow = d
}

// Resolve returns the work-session id for this activity, opening a new one when
// the identity is unseen or its previous activity is older than the idle window.
//
// Returns "" when we know nothing about the caller: an unattributable record is
// better left unattributed than grouped into a bucket that means nothing.
func (t *WorkSessionTracker) Resolve(id WorkSessionIdentity) string {
	ws, _ := t.ResolveEffective(id)
	return ws
}

// ResolveEffective is Resolve that also returns the identity the session was
// actually filed under. It differs from the one passed in only when the run
// name was dropped because the principal is over its daily bound of named
// sessions. A caller that caches the result, or refreshes it with Touch, must
// use the effective identity: caching under the rejected name would create one
// alias per invented name and let them bypass the bound.
func (t *WorkSessionTracker) ResolveEffective(id WorkSessionIdentity) (string, WorkSessionIdentity) {
	if id.isEmpty() {
		return "", id
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	k := id.key()

	if entry, ok := t.sessions[k]; ok && now.Sub(entry.lastActivity) <= t.idleWindow {
		// Same work, continuing. This is the reconnect case: the transport
		// session underneath may be brand new, but the work is the same.
		entry.lastActivity = now
		return entry.id, id
	}

	if id.RunName != "" && id.CorrelationID == "" {
		if !t.admitNamedLocked(id.Principal, now) {
			// Over the daily bound: the name is dropped and the call joins the
			// principal's unnamed work instead of opening one more session.
			id.RunName = ""
			if id.isEmpty() {
				return "", id
			}
			k = id.key()
			if entry, ok := t.sessions[k]; ok && now.Sub(entry.lastActivity) <= t.idleWindow {
				entry.lastActivity = now
				return entry.id, id
			}
		}
	}

	// Either never seen, or idle long enough that this is new work.
	entry := &workSessionEntry{
		id:           newWorkSessionID(k, now),
		lastActivity: now,
		startedAt:    now,
	}
	t.sessions[k] = entry
	return entry.id, id
}

// EffectiveIdentity returns the identity a Resolve of id would file the
// session under, WITHOUT resolving it: the same identity, or the one with the
// run name dropped when the principal is over its daily bound and the named
// session is not already live. A caller that holds a cached session for the
// effective identity uses it to find that session before asking the tracker
// to open one, so that no unused competing session is created.
func (t *WorkSessionTracker) EffectiveIdentity(id WorkSessionIdentity) WorkSessionIdentity {
	if id.RunName == "" || id.CorrelationID != "" {
		return id
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if entry, ok := t.sessions[id.key()]; ok && now.Sub(entry.lastActivity) <= t.idleWindow {
		return id
	}
	if t.namedDay != now.UTC().Format("2006-01-02") || t.named[id.Principal] < MaxNamedWorkSessionsPerDay {
		return id
	}
	id.RunName = ""
	return id
}

// admitNamedLocked counts one more named work session for the principal today
// and reports whether it is within the daily bound.
func (t *WorkSessionTracker) admitNamedLocked(principal string, now time.Time) bool {
	day := now.UTC().Format("2006-01-02")
	if t.namedDay != day || t.named == nil {
		t.namedDay = day
		t.named = make(map[string]int)
	}
	if t.named[principal] >= MaxNamedWorkSessionsPerDay {
		return false
	}
	t.named[principal]++
	return true
}

// Touch records activity on a work session that was resolved earlier and is
// reused without a new Resolve: a legacy MCP connection keeps the id it was
// given (Spec 124 FR-006), so without this its idle clock would stop at the
// first call (FR-019).
//
// When the entry was reaped or has gone idle, it is reinstated under the same
// id: the connection will keep stamping that id, and another connection with
// the same identity must join it, not open a second session beside it. An
// identity that has since been given a different live session keeps it; the
// touched session's own entry, if it still has one, is refreshed instead.
func (t *WorkSessionTracker) Touch(workSessionID string, id WorkSessionIdentity) {
	if workSessionID == "" || id.isEmpty() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	k := id.key()
	entry, ok := t.sessions[k]
	switch {
	case ok && entry.id == workSessionID:
		entry.lastActivity = now
	case ok && now.Sub(entry.lastActivity) <= t.idleWindow:
		// A different, live session owns this identity now. The touched
		// session may still be filed under the identity it was resolved
		// with (a connection pinned before its workspace was known): keep
		// that entry's idle clock moving, so the session a connection is
		// still writing to is not reaped under it.
		for _, e := range t.sessions {
			if e.id == workSessionID {
				e.lastActivity = now
				break
			}
		}
	default:
		t.sessions[k] = &workSessionEntry{id: workSessionID, lastActivity: now, startedAt: now}
	}
}

// Adopt files the identity under the given work session, replacing whatever
// session the identity had. A legacy connection that keeps a session for an
// identity uses it when a session was opened for that identity beside it
// (Spec 124 FR-006): the one the connection writes to is the one that others
// with the same identity must join.
func (t *WorkSessionTracker) Adopt(workSessionID string, id WorkSessionIdentity) {
	if workSessionID == "" || id.isEmpty() {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	k := id.key()
	if entry, ok := t.sessions[k]; ok && entry.id == workSessionID {
		entry.lastActivity = now
		return
	}
	started := now
	for _, e := range t.sessions {
		if e.id == workSessionID {
			started = e.startedAt
			break
		}
	}
	t.sessions[k] = &workSessionEntry{id: workSessionID, lastActivity: now, startedAt: started}
}

// TouchID records activity on a work session known only by its id (a session
// pinned on a request context, whose rows are stamped without resolving it
// again). It refreshes the entry that holds the id; when none does (the
// session was reaped), nothing happens.
func (t *WorkSessionTracker) TouchID(workSessionID string) {
	if workSessionID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, e := range t.sessions {
		if e.id == workSessionID {
			e.lastActivity = t.now()
			return
		}
	}
}

// StartedAt returns when the given work session began, and whether it is known.
func (t *WorkSessionTracker) StartedAt(workSessionID string) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, e := range t.sessions {
		if e.id == workSessionID {
			return e.startedAt, true
		}
	}
	return time.Time{}, false
}

// Reap drops work sessions idle for more than maxIdle, so the tracker cannot
// grow without bound on a long-lived proxy.
func (t *WorkSessionTracker) Reap(maxIdle time.Duration) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	removed := 0
	for k, e := range t.sessions {
		if now.Sub(e.lastActivity) > maxIdle {
			delete(t.sessions, k)
			removed++
		}
	}
	return removed
}

// newWorkSessionID mints an opaque, stable id for one work session.
//
// It hashes the identity together with the session's start time, so that the
// same user working on the same project tomorrow gets a DIFFERENT id (it is
// different work) while a reconnect within the window keeps the SAME id (it is
// the same work).
//
// Hashed rather than plain so the id never leaks a filesystem path or a token
// subject — the id travels in URLs, logs and CSV exports.
func newWorkSessionID(identityKey string, startedAt time.Time) string {
	h := sha256.New()
	h.Write([]byte(identityKey))
	h.Write([]byte(startedAt.UTC().Format(time.RFC3339Nano)))
	return "ws-" + hex.EncodeToString(h.Sum(nil))[:16]
}
