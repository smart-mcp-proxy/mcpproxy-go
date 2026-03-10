//go:build server

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/teams/users"
)

// OAuthHandler handles the OAuth login/callback/logout HTTP endpoints.
type OAuthHandler struct {
	userStore      *users.UserStore
	sessionManager *SessionManager
	config         *config.TeamsConfig
	hmacKey        []byte
	logger         *zap.SugaredLogger

	// CSRF state storage (in-memory, keyed by state string)
	pendingStates map[string]*oauthState
	statesMu      sync.Mutex
}

type oauthState struct {
	CodeVerifier string    // PKCE code verifier
	RedirectURI  string    // Where to redirect after login
	CreatedAt    time.Time // For cleanup
}

// stateMaxAge is the maximum age for pending OAuth states before cleanup.
const stateMaxAge = 10 * time.Minute

// NewOAuthHandler creates a new OAuthHandler.
func NewOAuthHandler(
	userStore *users.UserStore,
	sessionManager *SessionManager,
	cfg *config.TeamsConfig,
	hmacKey []byte,
	logger *zap.SugaredLogger,
) *OAuthHandler {
	return &OAuthHandler{
		userStore:      userStore,
		sessionManager: sessionManager,
		config:         cfg,
		hmacKey:        hmacKey,
		logger:         logger,
		pendingStates:  make(map[string]*oauthState),
	}
}

// HandleLogin initiates the OAuth login flow by redirecting the user to the
// OAuth provider's authorization URL.
// GET /api/v1/auth/login?redirect_uri=/dashboard
func (h *OAuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.config.OAuth == nil {
		http.Error(w, "OAuth not configured", http.StatusInternalServerError)
		return
	}

	// Clean up stale states to prevent memory leaks
	h.cleanupStaleStates()

	// Generate random state (32 bytes, hex encoded)
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		h.logger.Errorw("failed to generate state", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	state := hex.EncodeToString(stateBytes)

	// Generate PKCE code verifier (32 bytes, base64url encoded, no padding)
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		h.logger.Errorw("failed to generate code verifier", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Compute code challenge: SHA256(verifier), base64url encoded, no padding
	challengeHash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])

	// Store state for validation in callback
	redirectURI := r.URL.Query().Get("redirect_uri")
	if redirectURI == "" {
		redirectURI = "/ui/"
	}

	h.statesMu.Lock()
	h.pendingStates[state] = &oauthState{
		CodeVerifier: codeVerifier,
		RedirectURI:  redirectURI,
		CreatedAt:    time.Now(),
	}
	h.statesMu.Unlock()

	// Get provider configuration
	provider, err := GetProvider(h.config.OAuth.Provider, h.config.OAuth.TenantID)
	if err != nil {
		h.logger.Errorw("failed to get OAuth provider", "error", err)
		http.Error(w, "OAuth provider configuration error", http.StatusInternalServerError)
		return
	}

	// Build the callback URL from the request
	callbackURL := buildCallbackURL(r)

	// Build the authorization URL
	authURL := provider.BuildAuthURL(h.config.OAuth.ClientID, callbackURL, state, codeChallenge)

	h.logger.Infow("initiating OAuth login", "provider", h.config.OAuth.Provider)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// HandleCallback handles the OAuth provider callback after user authentication.
// GET /api/v1/auth/callback?code=xxx&state=yyy
func (h *OAuthHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.config.OAuth == nil {
		http.Error(w, "OAuth not configured", http.StatusInternalServerError)
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	// Validate required parameters
	if code == "" {
		h.logger.Warnw("OAuth callback missing code parameter")
		writeJSONError(w, http.StatusBadRequest, "missing code parameter")
		return
	}
	if state == "" {
		h.logger.Warnw("OAuth callback missing state parameter")
		writeJSONError(w, http.StatusBadRequest, "missing state parameter")
		return
	}

	// Validate and consume state (CSRF protection)
	h.statesMu.Lock()
	pending, ok := h.pendingStates[state]
	if ok {
		delete(h.pendingStates, state)
	}
	h.statesMu.Unlock()

	if !ok {
		h.logger.Warnw("OAuth callback with invalid state", "state", state)
		writeJSONError(w, http.StatusBadRequest, "invalid or expired state parameter")
		return
	}

	// Get provider
	provider, err := GetProvider(h.config.OAuth.Provider, h.config.OAuth.TenantID)
	if err != nil {
		h.logger.Errorw("failed to get OAuth provider", "error", err)
		http.Error(w, "OAuth provider configuration error", http.StatusInternalServerError)
		return
	}

	// Build the callback URL (must match the one used in login)
	callbackURL := buildCallbackURL(r)

	// Exchange code for tokens
	tokenResp, err := provider.ExchangeCode(
		r.Context(),
		code,
		callbackURL,
		h.config.OAuth.ClientID,
		h.config.OAuth.ClientSecret,
		pending.CodeVerifier,
	)
	if err != nil {
		h.logger.Errorw("OAuth code exchange failed", "error", err)
		http.Error(w, "authentication failed", http.StatusInternalServerError)
		return
	}

	// Fetch user info (try ID token first for OIDC providers, fall back to userinfo endpoint)
	userInfo, err := provider.FetchUserInfoFromToken(r.Context(), tokenResp)
	if err != nil {
		h.logger.Errorw("failed to fetch user info", "error", err)
		http.Error(w, "failed to retrieve user information", http.StatusInternalServerError)
		return
	}

	if userInfo.Email == "" {
		h.logger.Warnw("OAuth user info missing email")
		http.Error(w, "email address is required", http.StatusBadRequest)
		return
	}

	// Check allowed domains
	if !h.isDomainAllowed(userInfo.Email) {
		h.logger.Warnw("OAuth login domain not allowed",
			"email", userInfo.Email,
			"allowed_domains", h.config.OAuth.AllowedDomains,
		)
		writeJSONError(w, http.StatusForbidden, "email domain not allowed")
		return
	}

	// Upsert user: find existing or create new
	user, err := h.upsertUser(userInfo, h.config.OAuth.Provider)
	if err != nil {
		h.logger.Errorw("failed to upsert user", "error", err)
		http.Error(w, "failed to create user account", http.StatusInternalServerError)
		return
	}

	// Determine role
	role := "user"
	if h.config.IsAdminEmail(user.Email) {
		role = "admin"
	}

	// Generate JWT bearer token
	bearerToken, err := GenerateBearerToken(
		h.hmacKey,
		user.ID,
		user.Email,
		user.DisplayName,
		role,
		user.Provider,
		h.config.BearerTokenTTL.Duration(),
	)
	if err != nil {
		h.logger.Errorw("failed to generate bearer token", "error", err)
		http.Error(w, "failed to generate access token", http.StatusInternalServerError)
		return
	}

	// Create session
	session, err := h.sessionManager.CreateSession(user.ID, r)
	if err != nil {
		h.logger.Errorw("failed to create session", "error", err)
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	// Store bearer token in session
	session.BearerToken = bearerToken
	if err := h.sessionManager.store.CreateSession(session); err != nil {
		h.logger.Errorw("failed to update session with bearer token", "error", err)
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	// Set session cookie
	h.sessionManager.SetSessionCookie(w, session)

	h.logger.Infow("OAuth login successful",
		"user_id", user.ID,
		"email", user.Email,
		"provider", user.Provider,
		"role", role,
	)

	// Redirect to the original destination or dashboard
	redirectTarget := pending.RedirectURI
	if redirectTarget == "" {
		redirectTarget = "/ui/"
	}
	http.Redirect(w, r, redirectTarget, http.StatusFound)
}

// HandleLogout handles user logout by revoking the session and clearing the cookie.
// POST /api/v1/auth/logout
func (h *OAuthHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get session from request
	session, err := h.sessionManager.GetSessionFromRequest(r)
	if err != nil {
		h.logger.Errorw("failed to get session", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if session == nil {
		writeJSONError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Revoke session
	if err := h.sessionManager.RevokeSession(session.ID); err != nil {
		h.logger.Errorw("failed to revoke session", "error", err, "session_id", session.ID)
		// Continue with cookie clearing even if revoke fails
	}

	// Clear session cookie
	h.sessionManager.ClearSessionCookie(w)

	h.logger.Infow("user logged out", "user_id", session.UserID, "session_id", session.ID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "logged_out"}) //nolint:errcheck
}

// cleanupStaleStates removes pending states older than stateMaxAge.
func (h *OAuthHandler) cleanupStaleStates() {
	h.statesMu.Lock()
	defer h.statesMu.Unlock()

	cutoff := time.Now().Add(-stateMaxAge)
	for state, info := range h.pendingStates {
		if info.CreatedAt.Before(cutoff) {
			delete(h.pendingStates, state)
		}
	}
}

// isDomainAllowed checks if the user's email domain is in the allowed domains list.
// Returns true if no allowed domains are configured (allow all).
func (h *OAuthHandler) isDomainAllowed(email string) bool {
	if h.config.OAuth == nil || len(h.config.OAuth.AllowedDomains) == 0 {
		return true
	}

	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return false
	}
	domain := strings.ToLower(parts[1])

	for _, allowed := range h.config.OAuth.AllowedDomains {
		if strings.EqualFold(allowed, domain) {
			return true
		}
	}
	return false
}

// upsertUser finds an existing user by email or creates a new one.
// If the user exists, LastLoginAt is updated.
func (h *OAuthHandler) upsertUser(info *OAuthUserInfo, provider string) (*users.User, error) {
	existing, err := h.userStore.GetUserByEmail(info.Email)
	if err != nil {
		return nil, fmt.Errorf("looking up user: %w", err)
	}

	if existing != nil {
		// Update last login time and any changed profile info
		existing.LastLoginAt = time.Now().UTC()
		if info.DisplayName != "" {
			existing.DisplayName = info.DisplayName
		}
		if info.AvatarURL != "" && info.SubjectID != "" {
			existing.ProviderSubjectID = info.SubjectID
		}
		if err := h.userStore.UpdateUser(existing); err != nil {
			return nil, fmt.Errorf("updating user: %w", err)
		}
		return existing, nil
	}

	// Create new user
	newUser := users.NewUser(info.Email, info.DisplayName, provider, info.SubjectID)
	if err := h.userStore.CreateUser(newUser); err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}
	return newUser, nil
}

// buildCallbackURL constructs the OAuth callback URL from the HTTP request.
func buildCallbackURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// Check X-Forwarded-Proto for reverse proxy setups
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return fmt.Sprintf("%s://%s/api/v1/auth/callback", scheme, r.Host)
}
