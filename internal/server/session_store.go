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

	s.sessions[sessionID] = &SessionInfo{
		SessionID:     sessionID,
		ClientName:    clientName,
		ClientVersion: clientVersion,
		hasRoots:      hasRoots,
		hasSampling:   hasSampling,
		experimental:  experimental,
		startTime:     time.Now(),
	}

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
// real, and is a no-op afterwards.
//
// This is the counterpart of SetSession's deferral. It MUST run before anything
// updates the session's stats: UpdateSessionStats requires the row to exist and
// errors if it does not, so a first tool call would otherwise lose its counts.
func (s *SessionStore) EnsurePersisted(sessionID, workSessionID string) {
	if sessionID == "" {
		return
	}

	s.mu.Lock()
	info, ok := s.sessions[sessionID]
	if !ok || info.persisted || s.storageManager == nil {
		s.mu.Unlock()
		return
	}
	// Mark before releasing the lock: two concurrent first-calls must produce
	// one CreateSession, not two.
	info.persisted = true
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
		WorkSessionID: workSessionID,
	}
	mgr := s.storageManager
	s.mu.Unlock()

	if err := mgr.CreateSession(record); err != nil {
		s.logger.Warn("failed to persist session to storage",
			zap.String("session_id", sessionID),
			zap.Error(err),
		)
		// Let a later activity retry rather than silently never persisting.
		s.mu.Lock()
		if info, ok := s.sessions[sessionID]; ok {
			info.persisted = false
		}
		s.mu.Unlock()
	}
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

// GetSession retrieves session information
func (s *SessionStore) GetSession(sessionID string) *SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.sessions[sessionID]
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
