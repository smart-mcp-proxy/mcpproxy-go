//go:build server

package auth

import (
	"encoding/json"
	"net/http"
	"strings"

	"go.uber.org/zap"

	coreauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// agentTokenPrefix is the prefix for agent tokens, which should not be
// treated as JWT bearer tokens.
const agentTokenPrefix = "mcp_agt_"

// ServerEditionConfigProvider returns the server-edition configuration as of
// NOW.
//
// It is a function, not a struct pointer, for the same reason
// api.AdminServersProvider is: `admin_emails` is the sole source of truth for
// the admin role, and the configuration is hot-reloadable. A middleware built
// on the *config.ServerEditionConfig captured at wiring time answers every
// later request from the file as it stood at process start, so an operator who
// edits `admin_emails` — the documented way to promote and, more importantly,
// to DEMOTE someone — changes nothing until the process restarts. Issue #1169
// is that gap; moving it from "until the token expires" to "until restart" is
// not closing it.
//
// The returned value is READ-ONLY. Implementations hand back the current
// snapshot, not a defensive copy.
type ServerEditionConfigProvider func() *config.ServerEditionConfig

// StaticServerEditionConfig adapts a fixed config to ServerEditionConfigProvider.
// It is for callers with genuinely no live configuration to read (tests, and
// embedders with no config service) — never for production wiring, which must
// pass a provider over the current snapshot.
func StaticServerEditionConfig(cfg *config.ServerEditionConfig) ServerEditionConfigProvider {
	return func() *config.ServerEditionConfig { return cfg }
}

// ServerEditionAuthMiddleware validates user authentication via session cookies
// or JWT bearer tokens (server edition).
type ServerEditionAuthMiddleware struct {
	sessionManager *SessionManager
	userStore      *users.UserStore
	teamsConfig    ServerEditionConfigProvider
	hmacKey        []byte
	logger         *zap.SugaredLogger
}

// NewServerEditionAuthMiddleware creates a new ServerEditionAuthMiddleware.
//
// teamsConfig must read the LIVE configuration on every call; see
// ServerEditionConfigProvider for why a captured pointer is the whole of issue
// #1169. A nil provider — or one that returns nil — is tolerated and means "no
// admins", which is the conservative reading for a role decision.
func NewServerEditionAuthMiddleware(
	sessionManager *SessionManager,
	userStore *users.UserStore,
	teamsConfig ServerEditionConfigProvider,
	hmacKey []byte,
	logger *zap.SugaredLogger,
) *ServerEditionAuthMiddleware {
	return &ServerEditionAuthMiddleware{
		sessionManager: sessionManager,
		userStore:      userStore,
		teamsConfig:    teamsConfig,
		hmacKey:        hmacKey,
		logger:         logger,
	}
}

// Middleware returns the middleware function that validates authentication.
//
// Authentication is attempted in the following order:
//  1. Session cookie (mcpproxy_session) — validated via SessionManager
//  2. Bearer token in Authorization header — validated as JWT
//
// If neither method yields a valid identity, a 401 JSON error is returned.
// On success, the request context is enriched with an AuthContext.
func (m *ServerEditionAuthMiddleware) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Try session cookie
			authCtx, err := m.authenticateFromSession(r)
			if err != nil {
				m.logger.Warnw("session authentication error", "error", err)
			}
			if authCtx != nil {
				r = r.WithContext(coreauth.WithAuthContext(r.Context(), authCtx))
				next.ServeHTTP(w, r)
				return
			}

			// 2. Try Bearer token from Authorization header
			authCtx, err = m.authenticateFromBearer(r)
			if err != nil {
				m.logger.Debugw("bearer token authentication failed", "error", err)
			}
			if authCtx != nil {
				r = r.WithContext(coreauth.WithAuthContext(r.Context(), authCtx))
				next.ServeHTTP(w, r)
				return
			}

			// Neither method authenticated the request
			writeJSONError(w, http.StatusUnauthorized, "Authentication required. Provide a valid session cookie or Bearer token.")
		})
	}
}

// AdminOnly returns middleware that requires an admin AuthContext.
// It must be chained after Middleware() so that the AuthContext is already set.
func (m *ServerEditionAuthMiddleware) AdminOnly() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ac := coreauth.AuthContextFromContext(r.Context())
			if ac == nil {
				writeJSONError(w, http.StatusUnauthorized, "Authentication required.")
				return
			}
			if !ac.IsAdmin() {
				writeJSONError(w, http.StatusForbidden, "Admin access required.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// authenticateFromSession attempts to validate a session cookie and returns
// an AuthContext if successful. Returns (nil, nil) if no session cookie is present.
func (m *ServerEditionAuthMiddleware) authenticateFromSession(r *http.Request) (*coreauth.AuthContext, error) {
	session, err := m.sessionManager.GetSessionFromRequest(r)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, nil
	}

	user, err := m.userStore.GetUser(session.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		m.logger.Warnw("session references unknown user", "user_id", session.UserID, "session_id", session.ID)
		return nil, nil
	}
	if user.Disabled {
		m.logger.Warnw("session references disabled user", "user_id", user.ID, "email", user.Email)
		return nil, nil
	}

	return m.buildAuthContext(user), nil
}

// authenticateFromBearer attempts to validate a Bearer token from the
// Authorization header and returns an AuthContext if successful.
// Returns (nil, nil) if no Bearer token is present.
func (m *ServerEditionAuthMiddleware) authenticateFromBearer(r *http.Request) (*coreauth.AuthContext, error) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, nil
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		return nil, nil
	}

	// Don't treat agent tokens as JWTs
	if strings.HasPrefix(token, agentTokenPrefix) {
		return nil, nil
	}

	claims, err := ValidateBearerToken(token, m.hmacKey)
	if err != nil {
		return nil, err
	}

	// Verify the user still exists and is not disabled
	user, err := m.userStore.GetUser(claims.Subject)
	if err != nil {
		return nil, err
	}
	if user == nil {
		m.logger.Warnw("JWT references unknown user", "user_id", claims.Subject, "email", claims.Email)
		return nil, nil
	}
	if user.Disabled {
		m.logger.Warnw("JWT references disabled user", "user_id", user.ID, "email", user.Email)
		return nil, nil
	}

	// Re-derive the role from the CURRENT config rather than trusting the
	// frozen `role` claim. The claim is minted at login and is never revoked,
	// so an admin removed from admin_emails would otherwise keep admin until
	// the token expires — and could renew it indefinitely via
	// POST /api/v1/auth/token, which mints a fresh JWT from ac.Role.
	//
	// The user record was already loaded above, so this costs no extra lookup,
	// and it makes the bearer path agree with the session path (which has
	// always re-derived the role). Identity field parity is exact: GetUser is
	// keyed by User.ID, so user.ID == claims.Subject, and both mint sites pass
	// user.ID/user.Email/user.DisplayName/user.Provider — the same four values
	// buildAuthContext reads, only fresher.
	return m.buildAuthContext(user), nil
}

// buildAuthContext creates an AuthContext for the given user, determining the
// role from the CURRENT server config admin email list.
//
// Both auth paths land here, so this single read is what makes an
// `admin_emails` edit take effect on the next request rather than on the next
// process restart (issue #1169). The provider reads the runtime's published
// config snapshot, which the file watcher republishes on every hot reload —
// `server_edition` is loaded whole by config.LoadFromFile and is not among the
// restart-pinned fields, so the edit really is live.
//
// A missing configuration yields a plain user, never an admin: a role decision
// that cannot be made must not be made in the caller's favour.
func (m *ServerEditionAuthMiddleware) buildAuthContext(user *users.User) *coreauth.AuthContext {
	if m.isAdminEmail(user.Email) {
		return coreauth.AdminUserContext(user.ID, user.Email, user.DisplayName, user.Provider)
	}
	return coreauth.UserContext(user.ID, user.Email, user.DisplayName, user.Provider)
}

// isAdminEmail resolves the admin list from the live configuration, failing to
// "not an admin" when there is none to read.
func (m *ServerEditionAuthMiddleware) isAdminEmail(email string) bool {
	if m.teamsConfig == nil {
		return false
	}
	cfg := m.teamsConfig()
	if cfg == nil {
		return false
	}
	return cfg.IsAdminEmail(email)
}

// writeJSONError writes a JSON-formatted error response.
func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":       http.StatusText(statusCode),
		"message":     message,
		"status_code": statusCode,
	})
}
