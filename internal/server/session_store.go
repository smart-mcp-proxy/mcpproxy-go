package server

import (
	"sync"
	"time"

	"go.uber.org/zap"
	"mcpproxy-go/internal/storage"
)

// SessionInfo holds MCP session metadata
type SessionInfo struct {
	SessionID     string
	ClientName    string
	ClientVersion string
}

// SessionStore manages MCP session information
type SessionStore struct {
	sessions       map[string]*SessionInfo
	mu             sync.RWMutex
	logger         *zap.Logger
	storageManager *storage.Manager
}

// NewSessionStore creates a new session store
func NewSessionStore(logger *zap.Logger) *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*SessionInfo),
		logger:   logger,
	}
}

// SetStorageManager sets the storage manager for persistence
func (s *SessionStore) SetStorageManager(manager *storage.Manager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storageManager = manager
}

// SetSession stores or updates session information
func (s *SessionStore) SetSession(sessionID, clientName, clientVersion string) {
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

// Count returns the number of active sessions
func (s *SessionStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.sessions)
}
