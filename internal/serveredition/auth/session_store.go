//go:build server

package auth

import (
	"net/http"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

const (
	// SessionCookieName is the HTTP cookie name used for session tracking.
	SessionCookieName = "mcpproxy_session"
)

// CookieSecurePolicy decides the Secure attribute of the session cookie
// (Spec 107 FR-026, `session_cookie_secure`): "true" and "false" are
// unconditional; "auto" (the default) is Secure when the deployment's
// public_url is https, when the request arrived over in-process TLS, or when
// a trusted proxy (FR-027, read live) says X-Forwarded-Proto: https.
type CookieSecurePolicy struct {
	// Mode is config.SessionCookieSecureAuto|True|False ("" = auto).
	Mode string
	// PublicURLHTTPS is whether server_edition.public_url is https
	// (restart-pinned, resolved at setup).
	PublicURLHTTPS bool
	// TrustedProxies yields the LIVE trusted_proxies list; nil trusts nobody.
	TrustedProxies config.TrustedProxiesProvider
}

// Secure resolves the policy for one request (nil = no request context).
func (p CookieSecurePolicy) Secure(r *http.Request) bool {
	switch p.Mode {
	case config.SessionCookieSecureTrue:
		return true
	case config.SessionCookieSecureFalse:
		return false
	}
	if p.PublicURLHTTPS {
		return true
	}
	if r == nil {
		return false
	}
	return config.ForwardedHeaders(r, p.trusted()).Scheme == "https"
}

func (p CookieSecurePolicy) trusted() []string {
	if p.TrustedProxies == nil {
		return nil
	}
	return p.TrustedProxies()
}

// SessionManager provides high-level session management on top of the
// low-level BBolt UserStore. It adds HTTP cookie semantics, session creation
// tied to OAuth login, and periodic cleanup of expired sessions.
type SessionManager struct {
	store      *users.UserStore
	sessionTTL time.Duration
	policy     CookieSecurePolicy
}

// NewSessionManager creates a SessionManager with an unconditional Secure
// decision (true → policy "true", false → policy "false") and no trusted
// proxies. Production setup uses NewSessionManagerWithPolicy.
func NewSessionManager(store *users.UserStore, sessionTTL time.Duration, secure bool) *SessionManager {
	mode := config.SessionCookieSecureFalse
	if secure {
		mode = config.SessionCookieSecureTrue
	}
	return NewSessionManagerWithPolicy(store, sessionTTL, CookieSecurePolicy{Mode: mode})
}

// NewSessionManagerWithPolicy creates a SessionManager whose Secure decision
// and client-IP resolution follow the given policy (Spec 107 FR-026/FR-027).
func NewSessionManagerWithPolicy(store *users.UserStore, sessionTTL time.Duration, policy CookieSecurePolicy) *SessionManager {
	return &SessionManager{
		store:      store,
		sessionTTL: sessionTTL,
		policy:     policy,
	}
}

// SecureFor resolves the Secure decision for one request.
func (m *SessionManager) SecureFor(r *http.Request) bool {
	return m.policy.Secure(r)
}

// CreateSession creates a new session for the given user, populating UserAgent
// and IPAddress from the HTTP request. The caller is responsible for setting the
// cookie on the response via SetSessionCookie.
func (m *SessionManager) CreateSession(userID string, r *http.Request) (*users.Session, error) {
	session := users.NewSession(userID, m.sessionTTL)
	session.UserAgent = r.UserAgent()
	// FR-027: the forwarded client IP is believed only from a trusted proxy.
	session.IPAddress = config.ForwardedHeaders(r, m.policy.trusted()).ClientIP
	// The Secure decision is recorded on the session so the clearing cookie
	// at logout carries the same attribute (RFC 6265 §4.1.2) whatever shape
	// the logout request arrives in.
	session.CookieSecure = m.policy.Secure(r)

	if err := m.store.CreateSession(session); err != nil {
		return nil, err
	}

	return session, nil
}

// SetSessionCookie sets the session cookie on the HTTP response.
// The cookie is HttpOnly, SameSite=Lax, with path "/" and MaxAge based on TTL;
// Secure is the decision recorded on the session at creation.
func (m *SessionManager) SetSessionCookie(w http.ResponseWriter, session *users.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    session.ID,
		Path:     "/",
		MaxAge:   int(m.sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   session.CookieSecure,
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
// browser to delete it, with the policy's request-free Secure decision.
func (m *SessionManager) ClearSessionCookie(w http.ResponseWriter) {
	m.clearCookie(w, m.policy.Secure(nil))
}

// ClearSessionCookieFor clears the cookie of one session: Secure is what the
// cookie was set with (recorded on the session), or else the policy resolved
// for the logout request, so the browser accepts the deletion (RFC 6265).
func (m *SessionManager) ClearSessionCookieFor(w http.ResponseWriter, r *http.Request, session *users.Session) {
	secure := m.policy.Secure(r)
	if session != nil && session.CookieSecure {
		secure = true
	}
	m.clearCookie(w, secure)
}

func (m *SessionManager) clearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// CleanupExpired removes all expired sessions from the store and returns
// the number of sessions removed.
func (m *SessionManager) CleanupExpired() (int, error) {
	return m.store.CleanupExpiredSessions()
}
