//go:build teams

package auth

import (
	"encoding/json"
	"net/http"
	"strings"

	"go.uber.org/zap"

	coreauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

// agentTokenPrefix is the prefix for agent tokens, which should not be
// treated as JWT bearer tokens.
const agentTokenPrefix = "mcp_agt_"

// TeamsAuthMiddleware validates team member authentication via session cookies
// or JWT bearer tokens.
type TeamsAuthMiddleware struct {
	sessionManager *SessionManager
	userStore      *users.UserStore
	teamsConfig    *config.TeamsConfig
	hmacKey        []byte
	logger         *zap.SugaredLogger
}

// NewTeamsAuthMiddleware creates a new TeamsAuthMiddleware.
func NewTeamsAuthMiddleware(
	sessionManager *SessionManager,
	userStore *users.UserStore,
	teamsConfig *config.TeamsConfig,
	hmacKey []byte,
	logger *zap.SugaredLogger,
) *TeamsAuthMiddleware {
	return &TeamsAuthMiddleware{
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
func (m *TeamsAuthMiddleware) Middleware() func(http.Handler) http.Handler {
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
func (m *TeamsAuthMiddleware) AdminOnly() func(http.Handler) http.Handler {
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
func (m *TeamsAuthMiddleware) authenticateFromSession(r *http.Request) (*coreauth.AuthContext, error) {
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
func (m *TeamsAuthMiddleware) authenticateFromBearer(r *http.Request) (*coreauth.AuthContext, error) {
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

	// Build auth context from JWT claims
	if claims.Role == "admin" {
		return coreauth.AdminUserContext(claims.Subject, claims.Email, claims.DisplayName, claims.Provider), nil
	}
	return coreauth.UserContext(claims.Subject, claims.Email, claims.DisplayName, claims.Provider), nil
}

// buildAuthContext creates an AuthContext for the given user, determining the
// role from the teams config admin email list.
func (m *TeamsAuthMiddleware) buildAuthContext(user *users.User) *coreauth.AuthContext {
	if m.teamsConfig.IsAdminEmail(user.Email) {
		return coreauth.AdminUserContext(user.ID, user.Email, user.DisplayName, user.Provider)
	}
	return coreauth.UserContext(user.ID, user.Email, user.DisplayName, user.Provider)
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
