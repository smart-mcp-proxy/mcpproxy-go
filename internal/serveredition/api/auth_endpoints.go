//go:build server

package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	teamsauth "github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// AuthEndpoints provides authentication-related REST endpoints.
type AuthEndpoints struct {
	userStore      *users.UserStore
	sessionManager *teamsauth.SessionManager
	teamsConfig    *config.ServerEditionConfig
	hmacKey        []byte
	logger         *zap.SugaredLogger
}

// NewAuthEndpoints creates a new AuthEndpoints instance.
func NewAuthEndpoints(
	userStore *users.UserStore,
	sessionManager *teamsauth.SessionManager,
	teamsConfig *config.ServerEditionConfig,
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

// RegisterPublicRoutesWithPrefix registers the routes that need NO
// authentication: the edition probe GET {prefix}/auth/provider (Spec 107
// FR-030). Setup mounts it beside login/callback, outside every auth group,
// and only on an enabled block — a disabled block registers nothing, so 404
// falls out of chi exactly as on the personal build.
func (h *AuthEndpoints) RegisterPublicRoutesWithPrefix(r chi.Router, prefix string) {
	r.Get(prefix+"/auth/provider", h.getProvider)
}

// ProviderProbeResponse is the whole body of GET /api/v1/auth/provider
// (contracts/rest-endpoints.md §1): an operator-chosen label and nothing else.
type ProviderProbeResponse struct {
	DisplayName string `json:"display_name"`
}

// getProvider answers the public edition probe: no authentication, no side
// effects (no pending login state), only {display_name} — oauth.display_name,
// falling back to the provider family name. It never returns the issuer,
// client id, tenant id, scopes, domains or provider family.
func (h *AuthEndpoints) getProvider(w http.ResponseWriter, _ *http.Request) {
	label := ""
	if h.teamsConfig != nil && h.teamsConfig.OAuth != nil {
		label = h.teamsConfig.OAuth.DisplayName
		if label == "" {
			label = h.teamsConfig.OAuth.Provider
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, ProviderProbeResponse{DisplayName: label})
}

// --- Response types ---

// MeResponse represents the current user's profile.
//
// Spec 107 FR-008 (contracts/rest-endpoints.md §2): groups is the caller's
// own stored groups ([] when none), groups_updated_at RFC 3339 or null.
type MeResponse struct {
	ID              string   `json:"id"`
	Email           string   `json:"email"`
	DisplayName     string   `json:"display_name"`
	Role            string   `json:"role"`
	Provider        string   `json:"provider"`
	Groups          []string `json:"groups"`
	GroupsUpdatedAt *string  `json:"groups_updated_at"`
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
	// Require the user TIER: agent tokens carry their owner's UserID but must
	// not be able to read that owner's profile.
	if ac == nil || !ac.IsUser() || ac.GetUserID() == "" {
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

	groups := user.Groups
	if groups == nil {
		groups = []string{}
	}
	writeJSON(w, http.StatusOK, MeResponse{
		ID:              user.ID,
		Email:           user.Email,
		DisplayName:     user.DisplayName,
		Role:            ac.Role,
		Provider:        user.Provider,
		Groups:          groups,
		GroupsUpdatedAt: rfc3339OrNil(user.GroupsUpdatedAt),
	})
}

// generateToken creates a new JWT bearer token for the REST/Web UI surfaces.
//
// Session-cookie-only (Spec 107 FR-011): a bearer JWT presented here used to
// mint a fresh full-TTL JWT, so a JWT could renew itself forever and keep
// minting agent tokens without the user ever re-authenticating — an unbounded
// freshness window for stored groups and the live role. A derived credential
// never mints another credential: the Web UI calls this door with the cookie,
// and a JWT or agent token receives 401.
func (h *AuthEndpoints) generateToken(w http.ResponseWriter, r *http.Request) {
	ac := auth.AuthContextFromContext(r.Context())
	// Require the user TIER. This is the sharp one: without it, a scoped
	// read-only agent token carrying its owner's UserID could mint a full user
	// session JWT for that owner — a privilege upgrade, not a lateral move.
	if ac == nil || !ac.IsUser() || ac.GetUserID() == "" || ac.CredentialKind != auth.CredentialKindCookie {
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
