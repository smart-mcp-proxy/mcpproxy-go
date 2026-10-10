package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// liveConfigOrNil is the controller's config, or nil when unavailable.
func (s *Server) liveConfigOrNil() *config.Config {
	if s.controller == nil {
		return nil
	}
	cfg, err := s.controller.GetConfig()
	if err != nil {
		return nil
	}
	return cfg
}

// TokenStore defines the storage interface for agent token CRUD operations.
// This interface is satisfied by *storage.Manager.
type TokenStore interface {
	CreateAgentToken(token auth.AgentToken, rawToken string, hmacKey []byte) error
	ListAgentTokens() ([]auth.AgentToken, error)
	GetAgentTokenByName(name string) (*auth.AgentToken, error)
	RevokeAgentToken(name string) error
	DeleteAgentToken(name string) error
	RegenerateAgentToken(name string, newRawToken string, hmacKey []byte) (*auth.AgentToken, error)
	ValidateAgentToken(rawToken string, hmacKey []byte) (*auth.AgentToken, error)
	// UpdateAgentTokenLastUsedByHash is keyed by the token's HMAC hash, not by
	// its name: names are unique only within an owner in the server edition,
	// so a by-name stamp could land on another tenant's token.
	UpdateAgentTokenLastUsedByHash(hash string) error
}

// ServerNameLister provides the list of known server names for allowed_servers validation.
type ServerNameLister interface {
	GetAllServers() ([]map[string]interface{}, error)
}

// createTokenRequest is the JSON body for POST /api/v1/tokens.
type createTokenRequest struct {
	Name           string   `json:"name"`
	AllowedServers []string `json:"allowed_servers"`
	Permissions    []string `json:"permissions"`
	ExpiresIn      string   `json:"expires_in"`
	ProfilePin     string   `json:"profile_pin,omitempty"`
	// Profile is the Spec 108 spelling of profile_pin: it pins the token to a
	// profile, and allowed_servers / permissions default to "*" / all three
	// when it is given and they are omitted (scope comes from the profile).
	Profile string `json:"profile,omitempty"`
	// Purpose is the stated, unenforced task brief (Spec 115), at most 500
	// characters; screened for secrets before anything is stored.
	Purpose string `json:"purpose,omitempty"`
}

// createTokenResponse is the JSON response for POST /api/v1/tokens.
type createTokenResponse struct {
	Name           string    `json:"name"`
	Token          string    `json:"token"`
	AllowedServers []string  `json:"allowed_servers"`
	Permissions    []string  `json:"permissions"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
	ProfilePin     string    `json:"profile_pin,omitempty"`
}

// tokenInfoResponse is the JSON response for GET endpoints (no secret).
type tokenInfoResponse struct {
	Name           string     `json:"name"`
	TokenPrefix    string     `json:"token_prefix"`
	AllowedServers []string   `json:"allowed_servers"`
	Permissions    []string   `json:"permissions"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	Revoked        bool       `json:"revoked"`
	ProfilePin     string     `json:"profile_pin,omitempty"`
	// Kind is "agent" (a regular token; an empty stored kind reads as agent) or
	// "client" (a per-client credential). ClientID and ProfileMode are set for
	// a client credential only. LegacyScope is true when the token's scope is
	// its own allowed_servers/permissions rather than a profile's: allowed
	// servers other than ["*"], or permissions other than all three.
	Kind        string `json:"kind"`
	ClientID    string `json:"client_id,omitempty"`
	ProfileMode string `json:"profile_mode,omitempty"`
	LegacyScope bool   `json:"legacy_scope"`
	// Spec 115 lifecycle fields (additive): when the token was revoked, who
	// issued it, its stated (unenforced) purpose, whether its lifetime at
	// issue is a task lease (<= 24h), and whether its profile still exists.
	RevokedAt    *time.Time             `json:"revoked_at,omitempty"`
	Issuer       *auth.CredentialIssuer `json:"issuer,omitempty"`
	Purpose      string                 `json:"purpose,omitempty"`
	Lease        bool                   `json:"lease"`
	ProfileState string                 `json:"profile_state"`
}

// regenerateTokenResponse is the JSON response for POST /api/v1/tokens/{name}/regenerate.
type regenerateTokenResponse struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

// tokenNameRegex validates token name format: starts with alphanumeric, followed by alphanumeric, underscores, or hyphens.
var tokenNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// The expiry rule (30-day default, 365-day cap) lives in auth.ParseTokenExpiry
// (Spec 107 FR-011), shared with the server edition's POST /user/tokens.

// requireAdminAuth checks that the request is authenticated as admin (not an agent token).
// Returns true if the request should proceed, false if a 403 was written.
func (s *Server) requireAdminAuth(w http.ResponseWriter, r *http.Request) bool {
	return s.requireAdminRead(w, r, "Agent tokens cannot manage tokens")
}

// requireAdminRead is requireAdminAuth with a caller-supplied denial message,
// so a route outside token management does not 403 with "Agent tokens cannot
// manage tokens" (#1166 — GET /api/v1/config reuses this gate).
//
// It keys on !IsAdmin(), the SAME test auth.IsScopedCaller and
// (*Server).revealSecrets use. It used to key on Type == AuthTypeAgent, which
// is identical today — agent is the only non-admin type apiKeyAuthMiddleware
// installs on this mux — but would fail OPEN the moment a server-edition
// AuthTypeUser context reached it: a plain OAuth user is not an admin, yet
// would have passed an AuthTypeAgent-only test and read GET /api/v1/config.
// One mux must not carry two different definitions of "not admin".
//
// A request with NO AuthContext is still allowed through: that is the
// middleware's testing/bootstrap passthrough, and it must stay exactly as
// permissive as it is today (same rule as auth.CanEnumerateServer).
func (s *Server) requireAdminRead(w http.ResponseWriter, r *http.Request, message string) bool {
	ac := auth.AuthContextFromContext(r.Context())
	if ac != nil && !ac.IsAdmin() {
		s.writeError(w, r, http.StatusForbidden, message)
		return false
	}
	return true
}

// requireAdminReadMiddleware is requireAdminRead as chi middleware, for routes
// that are whole-handler admin-only rather than admin-only in one branch
// (SEC-07: /metrics). It keeps the identical semantics, including the
// nil-AuthContext passthrough, so one mux still carries one definition of
// "not admin".
func (s *Server) requireAdminReadMiddleware(message string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.requireAdminRead(w, r, message) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireTokenStore checks that the token store is configured.
// Returns true if the store is available, false if a 500 was written.
func (s *Server) requireTokenStore(w http.ResponseWriter, r *http.Request) bool {
	if s.tokenStore == nil {
		s.writeError(w, r, http.StatusInternalServerError, "Token management not available")
		return false
	}
	return true
}

// handleCreateToken handles POST /api/v1/tokens. Since Spec 115 the body goes
// through runtime.CredentialsService (the path the MCP `credentials` tool and
// the CLI share): the raw values are screened for secrets first, then the
// existing validation runs with its existing status codes and texts. The token
// gets a profile_change{issue} record and a credentials.changed event. The
// FR-008a guard is NOT enforced here (Spec 115 A9).
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	if !s.requireTokenStore(w, r) {
		return
	}

	var req createTokenRequest
	if derr := decodeIssuanceBody(r, &req, false); derr != nil {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", derr.Field, derr.Error())
		return
	}

	res, err := s.credentials().IssueToken(r.Context(), actorFromRequest(r), internalRuntime.IssueTokenRequest{
		Name: req.Name, Profile: req.Profile, ProfilePin: req.ProfilePin, Purpose: req.Purpose,
		AllowedServers: req.AllowedServers, Permissions: req.Permissions,
		ExpiresIn: req.ExpiresIn, Expiry: internalRuntime.ExpiryTokenDefault,
		ValidateServers: func(servers []string) error { return validateAllowedServers(servers, s.controller) },
	})
	if err != nil {
		s.writeCredentialIssueError(w, r, err)
		return
	}
	created, _ := s.tokenStore.GetAgentTokenByName(req.Name)
	resp := createTokenResponse{
		Name:      req.Name,
		Token:     res.Secret,
		CreatedAt: res.View.CreatedAt,
	}
	if res.View.ExpiresAt != nil {
		resp.ExpiresAt = *res.View.ExpiresAt
	}
	if created != nil {
		resp.AllowedServers, resp.Permissions, resp.ProfilePin = created.AllowedServers, created.Permissions, created.ProfilePin
	} else {
		// The listing is best-effort after the commit (FR-007): fall back to the
		// view, never fail a call whose token exists.
		resp.ProfilePin = res.View.Profile
	}

	s.writeJSON(w, http.StatusCreated, contracts.NewSuccessResponse(resp))
}

// credentials returns the credential lifecycle service: the runtime's when
// wired, else one built over the token store (unit tests that wire only
// SetTokenStore keep working, with no audit sink).
func (s *Server) credentials() *internalRuntime.CredentialsService {
	if s.credentialsService != nil {
		return s.credentialsService
	}
	if s.clientsService != nil {
		var tokens internalRuntime.CredentialTokenStore
		if s.tokenStore != nil {
			tokens = s.tokenStore
		}
		return s.clientsService.CredentialsServiceOver(tokens)
	}
	dataDir := s.dataDir
	return internalRuntime.NewCredentialsService(internalRuntime.CredentialsServiceDeps{
		Tokens:  s.tokenStore,
		Clients: s.clientsService,
		HMACKey: func() ([]byte, error) { return auth.GetOrCreateHMACKey(dataDir) },
		Config: func() *config.Config {
			cfg, err := s.controller.GetConfig()
			if err != nil {
				return nil
			}
			return cfg
		},
	})
}

// writeCredentialIssueError maps a CredentialsService refusal to the REST
// route's existing status codes and texts. A secret-shaped value answers 400
// naming the field, never the value (FR-020b).
func (s *Server) writeCredentialIssueError(w http.ResponseWriter, r *http.Request, err error) {
	var screen *internalRuntime.SecretInputError
	var ce *internalRuntime.CredentialError
	switch {
	case errors.As(err, &screen):
		s.writeClientBindingError(w, r, http.StatusBadRequest, screen.Code(), screen.Field(), screen.Error())
	case errors.As(err, &ce):
		status := ce.Status
		if status == 0 {
			status = http.StatusInternalServerError
		}
		if ce.ErrCode == profile.CredentialErrorCodeReservedIdentity || (ce.ErrCode == profile.CredentialErrorCodeInvalidArgument && ce.Field == "profile") {
			s.writeClientBindingError(w, r, status, "", ce.Field, ce.Msg)
			return
		}
		s.writeError(w, r, status, ce.Msg)
	default:
		if s.writeCredentialFailure(w, r, "", err) || s.writeProfilesError(w, r, err) {
			return
		}
		s.logger.Errorw("credential issue failed", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to create token")
	}
}

// handleListTokens handles GET /api/v1/tokens
func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	if !s.requireTokenStore(w, r) {
		return
	}
	// Spec 108-f FR-031: `?profile=` and `?token=` are honoured here; `client`
	// stays a 400 (a token row is not addressed by client).
	if !rejectUnsupportedScopeFilters(w, r, "profile", "token") {
		return
	}

	tokens, err := s.tokenStore.ListAgentTokens()
	if err != nil {
		s.logger.Errorf("Failed to list agent tokens: %v", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to list tokens")
		return
	}

	profileFilter, tokenFilter := r.URL.Query().Get("profile"), r.URL.Query().Get("token")
	result := make([]tokenInfoResponse, 0, len(tokens))
	for _, t := range tokens {
		// `profile` matches the CURRENT pin (either kind); "-" matches an empty
		// pin. `token` is an exact name. An unknown value matches nothing.
		if profileFilter == "-" && t.ProfilePin != "" || profileFilter != "" && profileFilter != "-" && t.ProfilePin != profileFilter {
			continue
		}
		if tokenFilter != "" && t.Name != tokenFilter {
			continue
		}
		result = append(result, tokenToInfoResponseCfg(t, s.liveConfigOrNil()))
	}

	s.writeSuccess(w, map[string]interface{}{"tokens": result})
}

// handleGetToken handles GET /api/v1/tokens/{name}
func (s *Server) handleGetToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	if !s.requireTokenStore(w, r) {
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "Token name is required")
		return
	}

	token, err := s.tokenStore.GetAgentTokenByName(name)
	if err != nil {
		s.logger.Errorf("Failed to get agent token: %v", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to get token")
		return
	}
	if token == nil {
		s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Token %q not found", name))
		return
	}

	s.writeSuccess(w, tokenToInfoResponseCfg(*token, s.liveConfigOrNil()))
}

// handleRevokeToken handles DELETE /api/v1/tokens/{name}
func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	if !s.requireTokenStore(w, r) {
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "Token name is required")
		return
	}

	// Spec 115: through CredentialsService, so the revoke writes
	// profile_change{revoke} and publishes credentials.changed. An already
	// revoked token still answers 204 (idempotent, as before).
	if _, err := s.credentials().Revoke(r.Context(), actorFromRequest(r), internalRuntime.CredentialRef{Token: name}, true); err != nil {
		var ce *internalRuntime.CredentialError
		if errors.As(err, &ce) && ce.ErrCode == profile.CredentialErrorCodeIdentityNotFound {
			s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Token %q not found", name))
			return
		}
		s.logger.Errorf("Failed to revoke agent token: %v", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to revoke token")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteToken handles DELETE /api/v1/tokens/{name}/permanent
// It permanently removes a token (unlike revoke, which is a soft delete),
// freeing the name so it can be reused for a new token.
func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	if !s.requireTokenStore(w, r) {
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "Token name is required")
		return
	}

	if err := s.tokenStore.DeleteAgentToken(name); err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Token %q not found", name))
			return
		}
		s.logger.Errorf("Failed to delete agent token: %v", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to delete token")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleRegenerateToken handles POST /api/v1/tokens/{name}/regenerate
func (s *Server) handleRegenerateToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	if !s.requireTokenStore(w, r) {
		return
	}

	name := chi.URLParam(r, "name")
	if name == "" {
		s.writeError(w, r, http.StatusBadRequest, "Token name is required")
		return
	}

	// Generate new token
	newRawToken, err := auth.GenerateToken()
	if err != nil {
		s.logger.Errorf("Failed to generate agent token: %v", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	// Get HMAC key
	hmacKey, err := auth.GetOrCreateHMACKey(s.dataDir)
	if err != nil {
		s.logger.Errorf("Failed to get HMAC key: %v", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to initialize token security")
		return
	}

	_, err = s.tokenStore.RegenerateAgentToken(name, newRawToken, hmacKey)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Token %q not found", name))
			return
		}
		// Rotation refreshes a live secret; it is not an un-revoke. Classified
		// here so the refusal is a 409 with an actionable body rather than the
		// generic 500 the fall-through would produce.
		if errors.Is(err, storage.ErrAgentTokenRevoked) {
			s.writeError(w, r, http.StatusConflict,
				fmt.Sprintf("Token %q is revoked and cannot be regenerated. Delete it and create a new token.", name))
			return
		}
		// F1 (Spec 108-c review): this generic, name-based regenerate mints a
		// fresh mcp_agt_ secret and cannot update a kind=client record's
		// Kind/ClientID/ProfileMode fields to match — doing so would
		// permanently brick the credential (every future authentication
		// fails ValidateTokenInvariants). Refused before any mutation.
		if errors.Is(err, storage.ErrClientCredentialRegenerateRefused) {
			s.writeError(w, r, http.StatusConflict,
				fmt.Sprintf("Token %q is a client credential and cannot be regenerated via this endpoint. Use the client-credential rotation flow instead.", name))
			return
		}
		s.logger.Errorf("Failed to regenerate agent token: %v", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to regenerate token")
		return
	}

	resp := regenerateTokenResponse{
		Name:  name,
		Token: newRawToken,
	}

	s.writeSuccess(w, resp)
}

// --- Validation helpers (T021) ---

// validateTokenName checks that a token name matches the required format.
// Names must be 1-64 characters, starting with an alphanumeric character,
// followed by alphanumeric characters, underscores, or hyphens.
func validateTokenName(name string) error {
	if name == "" {
		return fmt.Errorf("token name is required")
	}
	if len(name) > 64 {
		return fmt.Errorf("token name must be at most 64 characters")
	}
	if !tokenNameRegex.MatchString(name) {
		return fmt.Errorf("token name must start with a letter or digit and contain only letters, digits, underscores, or hyphens")
	}
	return nil
}

// parseExpiry parses an expiry duration string and returns the absolute expiry
// time. It is a one-line wrapper over auth.ParseTokenExpiry (Spec 107 FR-011):
// "30d", "720h" or any Go duration, positive, at most 365 days, 30 days when
// empty — the SAME rule the server edition's POST /user/tokens applies, so the
// two minting doors cannot drift.
func parseExpiry(expiresIn string) (time.Time, error) {
	return auth.ParseTokenExpiry(expiresIn, time.Now().UTC())
}

// validateAllowedServers checks that each server name in the list either is "*"
// or corresponds to a known server in the current configuration.
func validateAllowedServers(servers []string, controller ServerNameLister) error {
	if len(servers) == 0 {
		return nil // empty means default to ["*"]
	}

	// Collect known server names
	allServers, err := controller.GetAllServers()
	if err != nil {
		return fmt.Errorf("failed to retrieve server list: %w", err)
	}

	knownNames := make(map[string]bool, len(allServers))
	for _, srv := range allServers {
		if name, ok := srv["name"].(string); ok && name != "" {
			knownNames[name] = true
		}
		// Also check "id" field which some server representations use
		if id, ok := srv["id"].(string); ok && id != "" {
			knownNames[id] = true
		}
	}

	for _, s := range servers {
		if s == "*" {
			continue
		}
		if !knownNames[s] {
			return fmt.Errorf("unknown server: %q", s)
		}
	}

	return nil
}

// legacyScope reports whether a regular token's scope is its own
// allowed_servers/permissions rather than a profile's (data-model §3).
func legacyScope(t auth.AgentToken) bool {
	if len(t.AllowedServers) != 1 || t.AllowedServers[0] != "*" {
		return true
	}
	seen := map[string]bool{}
	for _, p := range t.Permissions {
		seen[p] = true
	}
	return !seen[auth.PermRead] || !seen[auth.PermWrite] || !seen[auth.PermDestructive]
}

// tokenToInfoResponseCfg is tokenToInfoResponse with the profile state
// resolved against cfg.
func tokenToInfoResponseCfg(t auth.AgentToken, cfg *config.Config) tokenInfoResponse {
	out := tokenToInfoResponse(t)
	out.ProfileState = internalRuntime.ProfileStateOf(t.ProfilePin, cfg)
	return out
}

// tokenToInfoResponse converts an auth.AgentToken to a tokenInfoResponse (without secrets).
func tokenToInfoResponse(t auth.AgentToken) tokenInfoResponse {
	kind := t.Kind
	if kind == "" {
		kind = auth.KindAgent
	}
	return tokenInfoResponse{
		Kind:           kind,
		ClientID:       t.ClientID,
		ProfileMode:    t.ProfileMode,
		LegacyScope:    kind == auth.KindAgent && legacyScope(t),
		Name:           t.Name,
		TokenPrefix:    t.TokenPrefix,
		AllowedServers: t.AllowedServers,
		Permissions:    t.Permissions,
		ExpiresAt:      t.ExpiresAt,
		CreatedAt:      t.CreatedAt,
		LastUsedAt:     t.LastUsedAt,
		Revoked:        t.Revoked,
		ProfilePin:     t.ProfilePin,
		RevokedAt:      t.RevokedAt,
		Issuer:         t.Issuer,
		Purpose:        t.Purpose,
		Lease:          t.IsLease(),
		ProfileState:   internalRuntime.ProfileStateNone,
	}
}
