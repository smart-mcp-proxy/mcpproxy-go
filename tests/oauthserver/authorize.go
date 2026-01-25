package oauthserver

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

// LoginPageData contains data for rendering the login page.
type LoginPageData struct {
	ClientID            string
	ClientName          string
	RedirectURI         string
	ResponseType        string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
	Scopes              []string
	Error               string
}

// handleAuthorize handles GET and POST /authorize requests.
func (s *OAuthTestServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleAuthorizeGET(w, r)
	case http.MethodPost:
		s.handleAuthorizePOST(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAuthorizeGET displays the login form.
func (s *OAuthTestServer) handleAuthorizeGET(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	query := r.URL.Query()
	clientID := query.Get("client_id")
	redirectURI := query.Get("redirect_uri")
	responseType := query.Get("response_type")
	scope := query.Get("scope")
	state := query.Get("state")
	codeChallenge := query.Get("code_challenge")
	codeChallengeMethod := query.Get("code_challenge_method")
	resource := query.Get("resource")

	// Validate required parameters
	if responseType != "code" {
		s.authorizeError(w, redirectURI, state, "unsupported_response_type", "Only 'code' response type is supported")
		return
	}

	if clientID == "" {
		s.authorizeError(w, redirectURI, state, "invalid_request", "Missing client_id")
		return
	}

	// Validate client
	client, exists := s.GetClient(clientID)
	if !exists {
		s.authorizeError(w, redirectURI, state, "invalid_client", "Unknown client_id")
		return
	}

	// Validate redirect URI
	if !s.isValidRedirectURI(client, redirectURI) {
		s.authorizeError(w, redirectURI, state, "invalid_request", "Invalid redirect_uri")
		return
	}

	// Require PKCE if configured
	if s.options.RequirePKCE && codeChallenge == "" {
		s.authorizeError(w, redirectURI, state, "invalid_request", "PKCE required: missing code_challenge")
		return
	}

	if codeChallenge != "" && codeChallengeMethod != "S256" {
		s.authorizeError(w, redirectURI, state, "invalid_request", "Only S256 code_challenge_method is supported")
		return
	}

	// RFC 8707: Require resource indicator if configured
	if s.options.RequireResourceIndicator && resource == "" {
		// In Runlayer mode, return Pydantic-style 422 error
		if s.options.RunlayerMode {
			s.pydanticValidationError(w, "query", "resource", "Field required")
			return
		}
		s.authorizeError(w, redirectURI, state, "invalid_request", "RFC 8707: resource parameter required")
		return
	}

	// Check for error mode
	if s.options.ErrorMode.AuthInvalidRequest {
		s.authorizeError(w, redirectURI, state, "invalid_request", "Injected error")
		return
	}

	if s.options.ErrorMode.AuthAccessDenied {
		s.authorizeError(w, redirectURI, state, "access_denied", "Injected error")
		return
	}

	// Parse and validate scopes
	scopes := s.parseScopes(scope)

	// Render login page
	data := LoginPageData{
		ClientID:            clientID,
		ClientName:          client.ClientName,
		RedirectURI:         redirectURI,
		ResponseType:        responseType,
		Scope:               scope,
		State:               state,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		Resource:            resource,
		Scopes:              scopes,
	}

	s.renderLoginPage(w, data)
}

// handleAuthorizePOST processes the login form submission.
func (s *OAuthTestServer) handleAuthorizePOST(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Get OAuth parameters
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	scope := r.FormValue("scope")
	state := r.FormValue("state")
	codeChallenge := r.FormValue("code_challenge")
	codeChallengeMethod := r.FormValue("code_challenge_method")
	resource := r.FormValue("resource")

	// Get user credentials
	username := r.FormValue("username")
	password := r.FormValue("password")
	consent := r.FormValue("consent")
	action := r.FormValue("action")

	// Validate client
	client, exists := s.GetClient(clientID)
	if !exists {
		s.authorizeError(w, redirectURI, state, "invalid_client", "Unknown client_id")
		return
	}

	// RFC 8707: Require resource indicator if configured
	if s.options.RequireResourceIndicator && resource == "" {
		// In Runlayer mode, return Pydantic-style 422 error
		if s.options.RunlayerMode {
			s.pydanticValidationError(w, "body", "resource", "Field required")
			return
		}
		s.authorizeError(w, redirectURI, state, "invalid_request", "RFC 8707: resource parameter required")
		return
	}

	// Check if user denied
	if action == "deny" || consent != "on" {
		s.authorizeRedirect(w, redirectURI, "", state, "access_denied", "User denied the authorization request")
		return
	}

	// Validate credentials
	if !s.validateCredentials(username, password) {
		data := LoginPageData{
			ClientID:            clientID,
			ClientName:          client.ClientName,
			RedirectURI:         redirectURI,
			ResponseType:        "code",
			Scope:               scope,
			State:               state,
			CodeChallenge:       codeChallenge,
			CodeChallengeMethod: codeChallengeMethod,
			Resource:            resource,
			Scopes:              s.parseScopes(scope),
			Error:               "Invalid username or password",
		}
		s.renderLoginPage(w, data)
		return
	}

	// Generate authorization code
	code := generateRandomString(32)
	scopes := s.parseScopes(scope)

	authCode := &AuthorizationCode{
		Code:                code,
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		Scopes:              scopes,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		Resource:            resource,
		State:               state,
		Subject:             username,
		ExpiresAt:           time.Now().Add(s.options.AuthCodeExpiry),
		Used:                false,
	}

	s.mu.Lock()
	s.authCodes[code] = authCode
	s.mu.Unlock()

	// Redirect with code
	s.authorizeRedirect(w, redirectURI, code, state, "", "")
}

// authorizeError redirects with an error response.
func (s *OAuthTestServer) authorizeError(w http.ResponseWriter, redirectURI, state, errorCode, errorDescription string) {
	// If no redirect URI, show error page
	if redirectURI == "" {
		http.Error(w, fmt.Sprintf("%s: %s", errorCode, errorDescription), http.StatusBadRequest)
		return
	}

	s.authorizeRedirect(w, redirectURI, "", state, errorCode, errorDescription)
}

// authorizeRedirect redirects to the client with code or error.
func (s *OAuthTestServer) authorizeRedirect(w http.ResponseWriter, redirectURI, code, state, errorCode, errorDescription string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "Invalid redirect_uri", http.StatusBadRequest)
		return
	}

	q := u.Query()
	if code != "" {
		q.Set("code", code)
	}
	if state != "" {
		q.Set("state", state)
	}
	if errorCode != "" {
		q.Set("error", errorCode)
		if errorDescription != "" {
			q.Set("error_description", errorDescription)
		}
	}
	u.RawQuery = q.Encode()

	w.Header().Set("Location", u.String())
	w.WriteHeader(http.StatusFound)
}

// isValidRedirectURI checks if the redirect URI is registered for the client.
func (s *OAuthTestServer) isValidRedirectURI(client *Client, redirectURI string) bool {
	if redirectURI == "" {
		return false
	}

	// Parse the redirect URI
	u, err := url.Parse(redirectURI)
	if err != nil {
		return false
	}

	// Don't allow fragments
	if u.Fragment != "" {
		return false
	}

	// Check against registered URIs
	for _, registered := range client.RedirectURIs {
		// Exact match
		if redirectURI == registered {
			return true
		}

		// Allow localhost variations (with any port)
		registeredURL, err := url.Parse(registered)
		if err != nil {
			continue
		}

		if (registeredURL.Hostname() == "localhost" || registeredURL.Hostname() == "127.0.0.1") &&
			(u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1") &&
			registeredURL.Path == u.Path {
			return true
		}
	}

	return false
}

// validateCredentials checks if the username/password is valid.
func (s *OAuthTestServer) validateCredentials(username, password string) bool {
	expectedPassword, exists := s.options.ValidUsers[username]
	if !exists {
		return false
	}
	return password == expectedPassword
}

// parseScopes splits a space-separated scope string.
func (s *OAuthTestServer) parseScopes(scope string) []string {
	if scope == "" {
		return s.options.DefaultScopes
	}

	scopes := strings.Fields(scope)
	validScopes := make([]string, 0, len(scopes))

	for _, sc := range scopes {
		if s.isScopeSupported(sc) {
			validScopes = append(validScopes, sc)
		}
	}

	if len(validScopes) == 0 {
		return s.options.DefaultScopes
	}

	return validScopes
}

// isScopeSupported checks if a scope is in the supported list.
func (s *OAuthTestServer) isScopeSupported(scope string) bool {
	for _, supported := range s.options.SupportedScopes {
		if scope == supported {
			return true
		}
	}
	return false
}

// renderLoginPage renders the login HTML template.
func (s *OAuthTestServer) renderLoginPage(w http.ResponseWriter, data LoginPageData) {
	tmpl, err := template.ParseFS(templateFS, "templates/login.html")
	if err != nil {
		http.Error(w, "Failed to load template", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
		return
	}
}

// verifyPKCE verifies the PKCE code_verifier against the stored code_challenge.
func verifyPKCE(codeVerifier, codeChallenge, method string) bool {
	if method != "S256" {
		return false
	}

	// SHA256 hash of verifier
	h := sha256.Sum256([]byte(codeVerifier))
	computed := base64.RawURLEncoding.EncodeToString(h[:])

	return computed == codeChallenge
}

// pydanticValidationError returns a 422 Unprocessable Entity response
// with Pydantic-style validation error format (used in Runlayer mode).
func (s *OAuthTestServer) pydanticValidationError(w http.ResponseWriter, location, field, msg string) {
	resp := PydanticValidationError{
		Detail: []PydanticErrorDetail{
			{
				Type:  "missing",
				Loc:   []string{location, field},
				Msg:   msg,
				Input: nil,
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity) // 422
	json.NewEncoder(w).Encode(resp)
}
