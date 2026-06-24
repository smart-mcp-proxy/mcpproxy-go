package server

import (
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

// SetSession stores or updates session information
func (s *SessionStore) SetSession(sessionID, clientName, clientVersion string, hasRoots, hasSampling bool, experimental []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[sessionID] = &SessionInfo{
		SessionID:     sessionID,
		ClientName:    clientName,
		ClientVersion: clientVersion,
	}

	// Persist to storage if available
	if s.storageManager != nil {
		now := time.Now()
		session := &storage.SessionRecord{
			ID:            sessionID,
			ClientName:    clientName,
			ClientVersion: clientVersion,
			Status:        "active",
			StartTime:     now,
			LastActivity:  now,
			HasRoots:      hasRoots,
			HasSampling:   hasSampling,
			Experimental:  experimental,
		}
		if err := s.storageManager.CreateSession(session); err != nil {
			s.logger.Warn("failed to persist session to storage",
				zap.String("session_id", sessionID),
				zap.Error(err),
			)
		}
	}

	s.logger.Debug("session info stored",
		zap.String("session_id", sessionID),
		zap.String("client_name", clientName),
		zap.String("client_version", clientVersion),
		zap.Bool("has_roots", hasRoots),
		zap.Bool("has_sampling", hasSampling),
	)
}

// GetSession retrieves session information
func (s *SessionStore) GetSession(sessionID string) *SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.sessions[sessionID]
}

// RemoveSession removes session information
func (s *SessionStore) RemoveSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, sessionID)
	delete(s.activeProfiles, sessionID)

	// Close session in storage if available
	if s.storageManager != nil {
		if err := s.storageManager.CloseSession(sessionID); err != nil {
			s.logger.Warn("failed to close session in storage",
				zap.String("session_id", sessionID),
				zap.Error(err),
			)
		}
	}

	s.logger.Debug("session info removed",
		zap.String("session_id", sessionID),
	)
}

// UpdateSessionStats updates token usage for a session
func (s *SessionStore) UpdateSessionStats(sessionID string, tokens int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Update in storage if available
	if s.storageManager != nil {
		if err := s.storageManager.UpdateSessionStats(sessionID, tokens); err != nil {
			s.logger.Warn("failed to update session stats in storage",
				zap.String("session_id", sessionID),
				zap.Int("tokens", tokens),
				zap.Error(err),
			)
		}
	}
}

// UpdateActivity updates the last activity timestamp for a session without incrementing stats.
func (s *SessionStore) UpdateActivity(sessionID string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.storageManager != nil {
		_ = s.storageManager.UpdateSessionActivity(sessionID)
	}
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
