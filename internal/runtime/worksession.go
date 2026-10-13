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
	}, "\x00")
}

// isEmpty reports whether we know nothing at all about the client. We refuse to
// group such activity, rather than lumping every anonymous caller together into
// one meaningless "session".
func (id WorkSessionIdentity) isEmpty() bool {
	return id.CorrelationID == "" &&
		id.Principal == "" &&
		id.ClientName == "" &&
		id.WorkspaceRoot == ""
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
}

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
	if id.isEmpty() {
		return ""
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	k := id.key()

	if entry, ok := t.sessions[k]; ok && now.Sub(entry.lastActivity) <= t.idleWindow {
		// Same work, continuing. This is the reconnect case: the transport
		// session underneath may be brand new, but the work is the same.
		entry.lastActivity = now
		return entry.id
	}

	// Either never seen, or idle long enough that this is new work.
	entry := &workSessionEntry{
		id:           newWorkSessionID(k, now),
		lastActivity: now,
		startedAt:    now,
	}
	t.sessions[k] = entry
	return entry.id
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
