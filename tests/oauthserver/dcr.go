package oauthserver

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// handleRegistration handles POST /registration requests (Dynamic Client Registration).
func (s *OAuthTestServer) handleRegistration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.options.EnableDCR {
		s.dcrError(w, http.StatusBadRequest, "invalid_request", "Dynamic client registration is disabled")
		return
	}

	// Check for error injection
	if s.options.ErrorMode.DCRInvalidRedirectURI {
		s.dcrError(w, http.StatusBadRequest, "invalid_redirect_uri", "Injected error")
		return
	}

	if s.options.ErrorMode.DCRInvalidScope {
		s.dcrError(w, http.StatusBadRequest, "invalid_client_metadata", "Invalid scope requested")
		return
	}

	var req ClientRegistrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.dcrError(w, http.StatusBadRequest, "invalid_client_metadata", "Failed to parse request body")
		return
	}

	// Validate redirect URIs
	if len(req.RedirectURIs) == 0 {
		s.dcrError(w, http.StatusBadRequest, "invalid_redirect_uri", "At least one redirect_uri is required")
		return
	}

	for _, uri := range req.RedirectURIs {
		if err := validateRedirectURI(uri); err != nil {
			s.dcrError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
	}

	// Parse and validate scopes
	var scopes []string
	if req.Scope != "" {
		scopes = strings.Fields(req.Scope)
		for _, sc := range scopes {
			if !s.isScopeSupported(sc) {
				s.dcrError(w, http.StatusBadRequest, "invalid_client_metadata", "Unsupported scope: "+sc)
				return
			}
		}
	} else {
		scopes = s.options.SupportedScopes
	}

	// Determine grant types
	grantTypes := req.GrantTypes
	if len(grantTypes) == 0 {
		grantTypes = []string{"authorization_code", "refresh_token"}
	}

	// Determine response types
	responseTypes := req.ResponseTypes
	if len(responseTypes) == 0 {
		responseTypes = []string{"code"}
	}

	// Determine token endpoint auth method
	authMethod := req.TokenEndpointAuthMethod
	if authMethod == "" {
		authMethod = "client_secret_basic"
	}

	// Generate client credentials
	clientID := generateRandomString(16)
	var clientSecret string
	if authMethod != "none" {
		clientSecret = generateRandomString(32)
	}

	// Create client
	client := &Client{
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		RedirectURIs:  req.RedirectURIs,
		GrantTypes:    grantTypes,
		ResponseTypes: responseTypes,
		Scopes:        scopes,
		ClientName:    req.ClientName,
		IsPublic:      authMethod == "none",
		CreatedAt:     time.Now(),
	}

	s.mu.Lock()
	s.clients[clientID] = client
	s.mu.Unlock()

	// Build response
	resp := ClientRegistrationResponse{
		ClientID:                clientID,
		ClientIDIssuedAt:        time.Now().Unix(),
		ClientSecretExpiresAt:   0, // Never expires
		RedirectURIs:            req.RedirectURIs,
		GrantTypes:              grantTypes,
		ResponseTypes:           responseTypes,
		ClientName:              req.ClientName,
		TokenEndpointAuthMethod: authMethod,
	}

	if clientSecret != "" {
		resp.ClientSecret = clientSecret
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// validateRedirectURI validates a redirect URI per RFC 7591.
func validateRedirectURI(uri string) error {
	u, err := url.Parse(uri)
	if err != nil {
		return err
	}

	// Must not contain fragment
	if u.Fragment != "" {
		return &url.Error{Op: "parse", URL: uri, Err: nil}
	}

	// Must have scheme
	if u.Scheme == "" {
		return &url.Error{Op: "parse", URL: uri, Err: nil}
	}

	return nil
}

// dcrError sends a DCR error response.
func (s *OAuthTestServer) dcrError(w http.ResponseWriter, status int, errorCode, description string) {
	resp := OAuthError{
		Error:            errorCode,
		ErrorDescription: description,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}
