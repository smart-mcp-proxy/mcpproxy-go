package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// TokenStore defines the storage interface for agent token CRUD operations.
// This interface is satisfied by *storage.Manager.
type TokenStore interface {
	CreateAgentToken(token auth.AgentToken, rawToken string, hmacKey []byte) error
	ListAgentTokens() ([]auth.AgentToken, error)
	GetAgentTokenByName(name string) (*auth.AgentToken, error)
	RevokeAgentToken(name string) error
	RegenerateAgentToken(name string, newRawToken string, hmacKey []byte) (*auth.AgentToken, error)
	ValidateAgentToken(rawToken string, hmacKey []byte) (*auth.AgentToken, error)
	UpdateAgentTokenLastUsed(name string) error
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
}

// createTokenResponse is the JSON response for POST /api/v1/tokens.
type createTokenResponse struct {
	Name           string    `json:"name"`
	Token          string    `json:"token"`
	AllowedServers []string  `json:"allowed_servers"`
	Permissions    []string  `json:"permissions"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
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
}

// regenerateTokenResponse is the JSON response for POST /api/v1/tokens/{name}/regenerate.
type regenerateTokenResponse struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

// tokenNameRegex validates token name format: starts with alphanumeric, followed by alphanumeric, underscores, or hyphens.
var tokenNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// maxExpiryDuration is the maximum allowed token expiry (365 days).
const maxExpiryDuration = 365 * 24 * time.Hour

// defaultExpiryDuration is the default token expiry (30 days).
const defaultExpiryDuration = 30 * 24 * time.Hour

// requireAdminAuth checks that the request is authenticated as admin (not an agent token).
// Returns true if the request should proceed, false if a 403 was written.
func (s *Server) requireAdminAuth(w http.ResponseWriter, r *http.Request) bool {
	ac := auth.AuthContextFromContext(r.Context())
	if ac != nil && ac.Type == auth.AuthTypeAgent {
		s.writeError(w, r, http.StatusForbidden, "Agent tokens cannot manage tokens")
		return false
	}
	return true
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

// handleCreateToken handles POST /api/v1/tokens
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	if !s.requireTokenStore(w, r) {
		return
	}

	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "Invalid JSON body: "+err.Error())
		return
	}

	// Validate name
	if err := validateTokenName(req.Name); err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	// Default permissions to ["read"] if empty
	if len(req.Permissions) == 0 {
		req.Permissions = []string{auth.PermRead}
	}

	// Validate permissions
	if err := validateTokenPermissions(req.Permissions); err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	// Parse expiry
	expiresAt, err := parseExpiry(req.ExpiresIn)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	// Validate allowed_servers
	if err := validateAllowedServers(req.AllowedServers, s.controller); err != nil {
		s.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	// Default allowed_servers to ["*"] if empty
	if len(req.AllowedServers) == 0 {
		req.AllowedServers = []string{"*"}
	}

	// Generate token
	rawToken, err := auth.GenerateToken()
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

	now := time.Now().UTC()
	agentToken := auth.AgentToken{
		Name:           req.Name,
		AllowedServers: req.AllowedServers,
		Permissions:    req.Permissions,
		ExpiresAt:      expiresAt,
		CreatedAt:      now,
	}

	if err := s.tokenStore.CreateAgentToken(agentToken, rawToken, hmacKey); err != nil {
		// Check for duplicate name
		if strings.Contains(err.Error(), "already exists") {
			s.writeError(w, r, http.StatusConflict, err.Error())
			return
		}
		s.logger.Errorf("Failed to create agent token: %v", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to create token")
		return
	}

	resp := createTokenResponse{
		Name:           req.Name,
		Token:          rawToken,
		AllowedServers: req.AllowedServers,
		Permissions:    req.Permissions,
		ExpiresAt:      expiresAt,
		CreatedAt:      now,
	}

	s.writeJSON(w, http.StatusCreated, contracts.NewSuccessResponse(resp))
}

// handleListTokens handles GET /api/v1/tokens
func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	if !s.requireTokenStore(w, r) {
		return
	}

	tokens, err := s.tokenStore.ListAgentTokens()
	if err != nil {
		s.logger.Errorf("Failed to list agent tokens: %v", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to list tokens")
		return
	}

	result := make([]tokenInfoResponse, 0, len(tokens))
	for _, t := range tokens {
		result = append(result, tokenToInfoResponse(t))
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

	s.writeSuccess(w, tokenToInfoResponse(*token))
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

	if err := s.tokenStore.RevokeAgentToken(name); err != nil {
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, r, http.StatusNotFound, fmt.Sprintf("Token %q not found", name))
			return
		}
		s.logger.Errorf("Failed to revoke agent token: %v", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to revoke token")
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

// validateTokenPermissions validates the permissions list using auth.ValidatePermissions.
func validateTokenPermissions(perms []string) error {
	return auth.ValidatePermissions(perms)
}

// parseExpiry parses an expiry duration string and returns the absolute expiry time.
// Accepted formats: "30d" (days), "720h" (hours), or any Go duration string.
// Maximum allowed duration is 365 days. Empty string defaults to 30 days.
func parseExpiry(expiresIn string) (time.Time, error) {
	if expiresIn == "" {
		return time.Now().UTC().Add(defaultExpiryDuration), nil
	}

	var d time.Duration

	// Handle "Nd" format (days)
	if strings.HasSuffix(expiresIn, "d") {
		daysStr := strings.TrimSuffix(expiresIn, "d")
		days, err := strconv.Atoi(daysStr)
		if err != nil || days <= 0 {
			return time.Time{}, fmt.Errorf("invalid expiry duration: %q", expiresIn)
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		// Try standard Go duration
		var err error
		d, err = time.ParseDuration(expiresIn)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid expiry duration: %q", expiresIn)
		}
		if d <= 0 {
			return time.Time{}, fmt.Errorf("expiry duration must be positive")
		}
	}

	if d > maxExpiryDuration {
		return time.Time{}, fmt.Errorf("expiry duration cannot exceed 365 days")
	}

	return time.Now().UTC().Add(d), nil
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

// tokenToInfoResponse converts an auth.AgentToken to a tokenInfoResponse (without secrets).
func tokenToInfoResponse(t auth.AgentToken) tokenInfoResponse {
	return tokenInfoResponse{
		Name:           t.Name,
		TokenPrefix:    t.TokenPrefix,
		AllowedServers: t.AllowedServers,
		Permissions:    t.Permissions,
		ExpiresAt:      t.ExpiresAt,
		CreatedAt:      t.CreatedAt,
		LastUsedAt:     t.LastUsedAt,
		Revoked:        t.Revoked,
	}
}
