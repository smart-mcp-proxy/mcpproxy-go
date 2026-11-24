package server

// SetSessionGrantID records the browser-login grant that authenticated a
// session (Spec 132 R3), next to SetSessionTokenPrefix, so session-backed
// activity emitted without a request context still carries it.
func (s *SessionStore) SetSessionGrantID(sessionID, grantID string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if info, ok := s.sessions[sessionID]; ok {
		info.GrantID = grantID
	}
}
