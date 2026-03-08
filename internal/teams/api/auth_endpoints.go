//go:build teams

package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

// AuthEndpoints provides authentication-related REST endpoints.
type AuthEndpoints struct {
	userStore      *users.UserStore
	sessionManager *teamsauth.SessionManager
	teamsConfig    *config.TeamsConfig
	hmacKey        []byte
	logger         *zap.SugaredLogger
}

// NewAuthEndpoints creates a new AuthEndpoints instance.
func NewAuthEndpoints(
	userStore *users.UserStore,
	sessionManager *teamsauth.SessionManager,
	teamsConfig *config.TeamsConfig,
	hmacKey []byte,
	logger *zap.SugaredLogger,
) *AuthEndpoints {
	return &AuthEndpoints{
		userStore:      userStore,
		sessionManager: sessionManager,
		teamsConfig:    teamsConfig,
		hmacKey:        hmacKey,
		logger:         logger,
	}
}

// RegisterRoutes registers auth info routes on the provided router.
func (h *AuthEndpoints) RegisterRoutes(r chi.Router) {
	r.Get("/auth/me", h.getMe)
	r.Post("/auth/token", h.generateToken)
}

// RegisterRoutesWithPrefix registers auth routes with a path prefix.
func (h *AuthEndpoints) RegisterRoutesWithPrefix(r chi.Router, prefix string) {
	r.Get(prefix+"/auth/me", h.getMe)
	r.Post(prefix+"/auth/token", h.generateToken)
}

// --- Response types ---

// MeResponse represents the current user's profile.
type MeResponse struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Provider    string `json:"provider"`
}

// TokenResponse contains a generated bearer token.
type TokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// --- Handlers ---

// getMe returns the current authenticated user's profile.
func (h *AuthEndpoints) getMe(w http.ResponseWriter, r *http.Request) {
	ac := auth.AuthContextFromContext(r.Context())
	if ac == nil || ac.GetUserID() == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	user, err := h.userStore.GetUser(ac.GetUserID())
	if err != nil {
		h.logger.Errorw("failed to get user profile", "user_id", ac.GetUserID(), "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get user profile")
		return
	}
	if user == nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	writeJSON(w, http.StatusOK, MeResponse{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Role:        ac.Role,
		Provider:    user.Provider,
	})
}

// generateToken creates a new JWT bearer token for MCP access.
func (h *AuthEndpoints) generateToken(w http.ResponseWriter, r *http.Request) {
	ac := auth.AuthContextFromContext(r.Context())
	if ac == nil || ac.GetUserID() == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	user, err := h.userStore.GetUser(ac.GetUserID())
	if err != nil {
		h.logger.Errorw("failed to get user for token generation", "user_id", ac.GetUserID(), "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to get user")
		return
	}
	if user == nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	ttl := h.teamsConfig.BearerTokenTTL.Duration()
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	token, err := teamsauth.GenerateBearerToken(
		h.hmacKey,
		user.ID,
		user.Email,
		user.DisplayName,
		ac.Role,
		user.Provider,
		ttl,
	)
	if err != nil {
		h.logger.Errorw("failed to generate bearer token", "user_id", ac.GetUserID(), "error", err)
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	expiresAt := time.Now().UTC().Add(ttl)

	h.logger.Infow("bearer token generated", "user_id", ac.GetUserID(), "email", user.Email)
	writeJSON(w, http.StatusOK, TokenResponse{
		Token:     token,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	})
}
