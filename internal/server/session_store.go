package server

import (
	"strings"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"go.uber.org/zap"
)

// SessionInfo holds MCP session metadata
type SessionInfo struct {
	SessionID     string
	ClientName    string
	ClientVersion string

	// Workspace is the project the client is working in, as disclosed via MCP
	// roots. Filled in asynchronously AFTER the handshake completes (see
	// Spec 082) — never during initialize, which would deadlock. Empty for
	// clients that do not disclose roots (e.g. Codex).
	Workspace string

	// Capabilities captured at initialize, needed when we finally persist.
	hasRoots     bool
	hasSampling  bool
	experimental []string
	startTime    time.Time

	// persisted records whether this session has been written to storage.
	//
	// Spec 082: a connection that only performs the handshake and never does any
	// work is NOT persisted. On a real machine 99 of 100 session records were
	// background agents connecting, doing nothing, and leaving — every ~15
	// minutes, around the clock. They buried the user's real sessions and
	// evicted them from the retention cap within a day.
	//
	// The in-memory entry is still created at initialize (activity records
	// resolve their client name from it), only the durable write is deferred
	// until the session does something.
	persisted bool

	// workspaceFetchStarted guards the roots request so it is attempted at most
	// once per connection.
	workspaceFetchStarted bool

	// workspaceReady is closed once the roots answer has arrived OR we have given
	// up on it. Activity waits (briefly) on this before deriving a work session,
	// so the first tool call of a connection is not filed under a workspace-less
	// key while the second lands under a workspace-keyed one — which would split
	// one connection across two work sessions.
	workspaceReady chan struct{}

	// workSessionID is resolved once per connection and then reused, so every
	// record from this connection agrees on which work session it belongs to.
	workSessionID string

	// persistDone is closed once the durable record exists. Concurrent first
	// calls wait on it rather than racing ahead to UpdateSessionStats, which
	// errors if the row is not there yet.
	persistDone chan struct{}
}

// SessionStore manages MCP session information
type SessionStore struct {
	sessions map[string]*SessionInfo
	// activeProfiles holds the per-session active profile slug selected via the
	// set_profile MCP tool (Profiles v2 T2). Kept orthogonal to sessions so a
	// re-initialize (SetSession) never clobbers a live selection. Cleared on
	// session close (RemoveSession) — covering both the OnUnregisterSession hook
	// and the background inactivity cleanup, which both call RemoveSession.
	activeProfiles map[string]string
	mu             sync.RWMutex
	logger         *zap.Logger
	storageManager *storage.Manager
}

// NewSessionStore creates a new session store
func NewSessionStore(logger *zap.Logger) *SessionStore {
	return &SessionStore{
		sessions:       make(map[string]*SessionInfo),
		activeProfiles: make(map[string]string),
		logger:         logger,
	}
}

// SetStorageManager sets the storage manager for persistence
func (s *SessionStore) SetStorageManager(manager *storage.Manager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storageManager = manager
}

// SetSession registers a session in memory at initialize time.
//
// Spec 082: it deliberately does NOT write to storage. A connection earns a
// durable record by doing something (see EnsurePersisted); one that only shakes
// hands and leaves — which is what background agents do, every ~15 minutes,
// around the clock — leaves no trace.
//
// The in-memory entry is still created here, immediately: activity records
// resolve their client name from this map (SetSessionClientResolver), so
// deferring it would strip the identity from the very first thing a session does.
func (s *SessionStore) SetSession(sessionID, clientName, clientVersion string, hasRoots, hasSampling bool, experimental []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info := &SessionInfo{
		SessionID:      sessionID,
		ClientName:     clientName,
		ClientVersion:  clientVersion,
		hasRoots:       hasRoots,
		hasSampling:    hasSampling,
		experimental:   experimental,
		startTime:      time.Now(),
		workspaceReady: make(chan struct{}),
		persistDone:    make(chan struct{}),
	}
	// A client that cannot report roots will never make us wait for them.
	if !hasRoots {
		close(info.workspaceReady)
	}
	s.sessions[sessionID] = info

	s.logger.Debug("session registered (not yet persisted — awaiting first activity)",
		zap.String("session_id", sessionID),
		zap.String("client_name", clientName),
		zap.String("client_version", clientVersion),
		zap.Bool("has_roots", hasRoots),
		zap.Bool("has_sampling", hasSampling),
	)
}

// SetWorkspace records the project this session is working in, discovered by
// asking the client for its MCP roots after the handshake completed.
//
// If the session has already been persisted (it did some work before the roots
// answer arrived — a real race, the roots round-trip is not instant), the stored
// record is updated too, so the project is not lost.
func (s *SessionStore) SetWorkspace(sessionID, workspaceRoot string) {
	s.mu.Lock()
	info, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	info.Workspace = workspaceRoot
	persisted := info.persisted
	mgr := s.storageManager
	s.signalWorkspaceReadyLocked(info)
	s.mu.Unlock()

	s.logger.Debug("session workspace resolved",
		zap.String("session_id", sessionID),
		zap.String("workspace", workspaceRoot),
	)

	if persisted && mgr != nil {
		if err := mgr.SetSessionWorkspace(sessionID, workspaceRoot); err != nil {
			s.logger.Debug("failed to backfill workspace on persisted session",
				zap.String("session_id", sessionID), zap.Error(err))
		}
	}
}

// EnsurePersisted writes the session to storage the first time it does something
// real, and blocks concurrent callers until that row exists.
//
// The blocking matters. UpdateSessionStats requires the row and errors if it is
// missing, so if a second concurrent first-call merely saw "someone else is
// persisting" and raced on, its stats would be dropped with a warning. It waits
// instead.
//
// Returns the work-session id this connection belongs to. It is resolved ONCE
// per connection and reused, so every record from this connection agrees.
func (s *SessionStore) EnsurePersisted(sessionID string, resolveWorkSession func(*SessionInfo) string) string {
	if sessionID == "" {
		return ""
	}

	s.mu.Lock()
	info, ok := s.sessions[sessionID]
	if !ok || s.storageManager == nil {
		s.mu.Unlock()
		return ""
	}

	if info.persisted {
		workSessionID := info.workSessionID
		done := info.persistDone
		s.mu.Unlock()
		<-done // the row may still be in flight; stats callers need it to exist
		return workSessionID
	}

	// We are the one who persists. Resolve the work session now, once, under the
	// lock, and cache it for every later record from this connection.
	info.persisted = true
	if info.workSessionID == "" && resolveWorkSession != nil {
		cp := *info
		info.workSessionID = resolveWorkSession(&cp)
	}

	record := &storage.SessionRecord{
		ID:            info.SessionID,
		ClientName:    info.ClientName,
		ClientVersion: info.ClientVersion,
		Status:        "active",
		StartTime:     info.startTime,
		LastActivity:  time.Now(),
		HasRoots:      info.hasRoots,
		HasSampling:   info.hasSampling,
		Experimental:  info.experimental,
		WorkspaceRoot: info.Workspace,
		WorkspaceName: workspaceDisplayName(info.Workspace),
		WorkSessionID: info.workSessionID,
	}
	workSessionID := info.workSessionID
	done := info.persistDone
	mgr := s.storageManager
	s.mu.Unlock()

	err := mgr.CreateSession(record)

	s.mu.Lock()
	if err != nil {
		s.logger.Warn("failed to persist session to storage",
			zap.String("session_id", sessionID),
			zap.Error(err),
		)
		// Let a later activity retry rather than silently never persisting. The
		// waiters are released either way — a blocked caller is worse than a
		// caller that finds no row.
		if cur, ok := s.sessions[sessionID]; ok {
			cur.persisted = false
		}
	}
	select {
	case <-done:
	default:
		close(done)
	}
	s.mu.Unlock()

	return workSessionID
}

// WorkSessionID returns the work session this connection belongs to, if it has
// been resolved yet.
func (s *SessionStore) WorkSessionID(sessionID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if info, ok := s.sessions[sessionID]; ok {
		return info.workSessionID
	}
	return ""
}

// TryClaimWorkspaceFetch returns true exactly once per session, for the caller
// that should go and ask the client for its roots. Returns false if there is
// nothing to fetch (no roots capability, already known) or someone already went.
func (s *SessionStore) TryClaimWorkspaceFetch(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, ok := s.sessions[sessionID]
	if !ok || info.workspaceFetchStarted || info.Workspace != "" || !info.hasRoots {
		return false
	}
	info.workspaceFetchStarted = true
	return true
}

// GetSession returns a COPY of the session info.
//
// It must be a copy: the roots goroutine writes Workspace under the lock while
// callers (the workspace resolver, the activity path) read it, and handing out
// the live pointer is a data race.
func (s *SessionStore) GetSession(sessionID string) *SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	info, ok := s.sessions[sessionID]
	if !ok {
		return nil
	}
	cp := *info
	return &cp
}

// signalWorkspaceReadyLocked closes the readiness channel exactly once.
// Caller must hold the write lock.
func (s *SessionStore) signalWorkspaceReadyLocked(info *SessionInfo) {
	select {
	case <-info.workspaceReady:
		// already closed
	default:
		close(info.workspaceReady)
	}
}

// WorkspaceSettled blocks until the client's roots have arrived, or we have
// given up waiting, or the deadline passes — whichever comes first.
//
// This exists so the FIRST piece of activity on a connection is not attributed
// before we know the project. Without it, the first tool call resolves a
// workspace-less work session and the second (roots having landed in between)
// resolves a workspace-keyed one: a single connection would split across two
// work sessions, which is exactly the thing this feature exists to prevent.
func (s *SessionStore) WorkspaceSettled(sessionID string, wait time.Duration) {
	s.mu.RLock()
	info, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return
	}

	select {
	case <-info.workspaceReady:
	case <-time.After(wait):
		// The client is not going to answer in time. Give up for good, so we do
		// not pay this wait on every subsequent call and do not later change our
		// mind about which work session this connection belongs to.
		s.mu.Lock()
		if cur, ok := s.sessions[sessionID]; ok {
			s.signalWorkspaceReadyLocked(cur)
		}
		s.mu.Unlock()
	}
}

// AbandonWorkspaceFetch marks the roots answer as never coming, unblocking
// anything waiting on it.
func (s *SessionStore) AbandonWorkspaceFetch(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if info, ok := s.sessions[sessionID]; ok {
		s.signalWorkspaceReadyLocked(info)
	}
}

// RemoveSession removes session information.
//
// Only sessions that were actually persisted are closed in storage. A session
// that never did any work has no stored record (Spec 082), and asking storage to
// close it would return "session not found" on every idle disconnect — trading
// a flood of junk records for a flood of junk warnings.
func (s *SessionStore) RemoveSession(sessionID string) {
	s.mu.Lock()
	info, ok := s.sessions[sessionID]
	wasPersisted := ok && info.persisted
	delete(s.sessions, sessionID)
	delete(s.activeProfiles, sessionID)
	mgr := s.storageManager
	s.mu.Unlock()

	if wasPersisted && mgr != nil {
		if err := mgr.CloseSession(sessionID); err != nil {
			s.logger.Warn("failed to close session in storage",
				zap.String("session_id", sessionID),
				zap.Error(err),
			)
		}
	}

	s.logger.Debug("session info removed",
		zap.String("session_id", sessionID),
		zap.Bool("was_persisted", wasPersisted),
	)
}

// UpdateSessionStats updates token usage for a session.
//
// Callers MUST have called EnsurePersisted first: storage refuses to update the
// stats of a row that does not exist, and under Spec 082 the row is not created
// until the session does its first piece of work. We skip the write for a
// never-persisted session rather than logging a warning for it.
func (s *SessionStore) UpdateSessionStats(sessionID string, tokens int) {
	s.mu.RLock()
	persisted := false
	if info, ok := s.sessions[sessionID]; ok {
		persisted = info.persisted
	}
	mgr := s.storageManager
	s.mu.RUnlock()

	if !persisted || mgr == nil {
		return
	}
	if err := mgr.UpdateSessionStats(sessionID, tokens); err != nil {
		s.logger.Warn("failed to update session stats in storage",
			zap.String("session_id", sessionID),
			zap.Int("tokens", tokens),
			zap.Error(err),
		)
	}
}

// UpdateActivity updates the last activity timestamp for a session without
// incrementing stats. A never-persisted session has no row to touch — and must
// not gain one here, since merely exchanging MCP messages is not "work"
// (Spec 082): that is exactly what the handshake-only agents do.
func (s *SessionStore) UpdateActivity(sessionID string) {
	s.mu.RLock()
	persisted := false
	if info, ok := s.sessions[sessionID]; ok {
		persisted = info.persisted
	}
	mgr := s.storageManager
	s.mu.RUnlock()

	if !persisted || mgr == nil {
		return
	}
	_ = mgr.UpdateSessionActivity(sessionID)
}

// SetActiveProfile records the active profile slug for a session (Profiles v2
// T2, set_profile). An empty slug clears the selection (back to all-servers).
func (s *SessionStore) SetActiveProfile(sessionID, profileSlug string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if profileSlug == "" {
		delete(s.activeProfiles, sessionID)
		return
	}
	s.activeProfiles[sessionID] = profileSlug
}

// GetActiveProfile returns the active profile slug for a session, or "" when the
// session has no active profile selection.
func (s *SessionStore) GetActiveProfile(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeProfiles[sessionID]
}

// Count returns the number of active sessions
func (s *SessionStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.sessions)
}

// workspaceDisplayName is the basename of a workspace root. Only the basename is
// ever shown — the full path is a local filesystem path and stays local.
func workspaceDisplayName(root string) string {
	root = strings.TrimSpace(root)
	root = strings.TrimPrefix(root, "file://")
	root = strings.TrimRight(root, "/")
	if root == "" {
		return ""
	}
	if i := strings.LastIndex(root, "/"); i >= 0 {
		return root[i+1:]
	}
	return root
}
