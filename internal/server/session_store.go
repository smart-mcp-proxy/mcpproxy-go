package server

import (
	"sync"

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
	mu       sync.RWMutex
	logger   *zap.Logger
}

// NewSessionStore creates a new session store
func NewSessionStore(logger *zap.Logger) *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*SessionInfo),
		logger:   logger,
	}
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

	s.logger.Debug("session info removed",
		zap.String("session_id", sessionID),
	)
}

// Count returns the number of active sessions
func (s *SessionStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.sessions)
}
