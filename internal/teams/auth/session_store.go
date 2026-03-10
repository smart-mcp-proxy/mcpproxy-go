//go:build teams

package auth

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

const (
	// SessionCookieName is the HTTP cookie name used for session tracking.
	SessionCookieName = "mcpproxy_session"
)

// SessionManager provides high-level session management on top of the
// low-level BBolt UserStore. It adds HTTP cookie semantics, session creation
// tied to OAuth login, and periodic cleanup of expired sessions.
type SessionManager struct {
	store      *users.UserStore
	sessionTTL time.Duration
	secure     bool // Set Secure flag on cookies (true for HTTPS)
}

// NewSessionManager creates a new SessionManager.
//   - store: the BBolt-backed user/session store
//   - sessionTTL: how long sessions remain valid
//   - secure: whether to set the Secure flag on cookies (true for HTTPS deployments)
func NewSessionManager(store *users.UserStore, sessionTTL time.Duration, secure bool) *SessionManager {
	return &SessionManager{
		store:      store,
		sessionTTL: sessionTTL,
		secure:     secure,
	}
}

// CreateSession creates a new session for the given user, populating UserAgent
// and IPAddress from the HTTP request. The caller is responsible for setting the
// cookie on the response via SetSessionCookie.
func (m *SessionManager) CreateSession(userID string, r *http.Request) (*users.Session, error) {
	session := users.NewSession(userID, m.sessionTTL)
	session.UserAgent = r.UserAgent()
	session.IPAddress = extractIPAddress(r)

	if err := m.store.CreateSession(session); err != nil {
		return nil, err
	}

	return session, nil
}

// SetSessionCookie sets the session cookie on the HTTP response.
// The cookie is HttpOnly, SameSite=Lax, with path "/" and MaxAge based on TTL.
func (m *SessionManager) SetSessionCookie(w http.ResponseWriter, session *users.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    session.ID,
		Path:     "/",
		MaxAge:   int(m.sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// GetSessionFromRequest extracts the session ID from the request cookie and
// looks up the session in the store. Returns nil (without error) if:
//   - No cookie is present
//   - The session is not found in the store
//   - The session has expired
//
// An error is returned only for unexpected store failures.
func (m *SessionManager) GetSessionFromRequest(r *http.Request) (*users.Session, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		// http.ErrNoCookie — not authenticated, not an error
		return nil, nil
	}

	if cookie.Value == "" {
		return nil, nil
	}

	session, err := m.store.GetSession(cookie.Value)
	if err != nil {
		return nil, err
	}

	// GetSession already returns nil for expired sessions
	return session, nil
}

// RevokeSession deletes a single session by ID.
func (m *SessionManager) RevokeSession(sessionID string) error {
	return m.store.DeleteSession(sessionID)
}

// RevokeUserSessions deletes all sessions for the given user.
func (m *SessionManager) RevokeUserSessions(userID string) error {
	return m.store.DeleteUserSessions(userID)
}

// ClearSessionCookie sets the session cookie with MaxAge=-1 to instruct the
// browser to delete it.
func (m *SessionManager) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// CleanupExpired removes all expired sessions from the store and returns
// the number of sessions removed.
func (m *SessionManager) CleanupExpired() (int, error) {
	return m.store.CleanupExpiredSessions()
}

// extractIPAddress returns the client IP address from the request.
// It checks X-Forwarded-For and X-Real-IP headers before falling back
// to the remote address.
func extractIPAddress(r *http.Request) string {
	// Check X-Forwarded-For first (may contain multiple IPs)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (the original client)
		if idx := strings.IndexByte(xff, ','); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to RemoteAddr (strip port)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
