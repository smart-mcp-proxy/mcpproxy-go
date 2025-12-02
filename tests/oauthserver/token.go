package oauthserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// handleToken handles POST /token requests.
func (s *OAuthTestServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.tokenError(w, http.StatusBadRequest, "invalid_request", "Failed to parse form")
		return
	}

	// Check for error injection - slow response
	if s.options.ErrorMode.TokenSlowResponse > 0 {
		time.Sleep(s.options.ErrorMode.TokenSlowResponse)
	}

	// Check for error injection - server error
	if s.options.ErrorMode.TokenServerError {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	grantType := r.FormValue("grant_type")

	switch grantType {
	case "authorization_code":
		s.handleAuthCodeGrant(w, r)
	case "refresh_token":
		s.handleRefreshTokenGrant(w, r)
	case "client_credentials":
		s.handleClientCredentialsGrant(w, r)
	case "urn:ietf:params:oauth:grant-type:device_code":
		s.handleDeviceCodeGrant(w, r)
	default:
		if s.options.ErrorMode.TokenUnsupportedGrant {
			s.tokenError(w, http.StatusBadRequest, "unsupported_grant_type", "Injected error")
			return
		}
		s.tokenError(w, http.StatusBadRequest, "unsupported_grant_type", "Unsupported grant type: "+grantType)
	}
}

// handleAuthCodeGrant handles authorization_code grant type.
func (s *OAuthTestServer) handleAuthCodeGrant(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	redirectURI := r.FormValue("redirect_uri")
	clientID := r.FormValue("client_id")
	clientSecret := r.FormValue("client_secret")
	codeVerifier := r.FormValue("code_verifier")

	// Check for error injection
	if s.options.ErrorMode.TokenInvalidClient {
		s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Injected error")
		return
	}

	if s.options.ErrorMode.TokenInvalidGrant {
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "Injected error")
		return
	}

	// Try to get client credentials from Basic auth header
	if clientID == "" {
		var ok bool
		clientID, clientSecret, ok = r.BasicAuth()
		if !ok {
			s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Missing client credentials")
			return
		}
	}

	// Validate client
	client, exists := s.GetClient(clientID)
	if !exists {
		s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Unknown client")
		return
	}

	// Validate client secret for confidential clients
	if !client.IsPublic && client.ClientSecret != clientSecret {
		s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Invalid client secret")
		return
	}

	// Validate authorization code
	s.mu.Lock()
	authCode, exists := s.authCodes[code]
	if !exists {
		s.mu.Unlock()
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "Invalid authorization code")
		return
	}

	if authCode.Used {
		s.mu.Unlock()
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "Authorization code already used")
		return
	}

	if authCode.IsExpired() {
		s.mu.Unlock()
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "Authorization code expired")
		return
	}

	if authCode.ClientID != clientID {
		s.mu.Unlock()
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "Client ID mismatch")
		return
	}

	if authCode.RedirectURI != redirectURI {
		s.mu.Unlock()
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "Redirect URI mismatch")
		return
	}

	// Verify PKCE
	if authCode.CodeChallenge != "" {
		if codeVerifier == "" {
			s.mu.Unlock()
			s.tokenError(w, http.StatusBadRequest, "invalid_grant", "Missing code_verifier")
			return
		}

		if !verifyPKCE(codeVerifier, authCode.CodeChallenge, authCode.CodeChallengeMethod) {
			s.mu.Unlock()
			s.tokenError(w, http.StatusBadRequest, "invalid_grant", "Invalid code_verifier")
			return
		}
	}

	// Mark code as used
	authCode.Used = true
	s.mu.Unlock()

	// Generate tokens
	accessToken, err := s.generateAccessToken(authCode.Subject, clientID, authCode.Scopes, authCode.Resource)
	if err != nil {
		s.tokenError(w, http.StatusInternalServerError, "server_error", "Failed to generate access token")
		return
	}

	var refreshToken string
	if s.options.EnableRefreshToken {
		refreshToken = s.generateRefreshToken(authCode.Subject, clientID, authCode.Scopes, authCode.Resource)
	}

	// Record for test verification
	s.recordIssuedToken(TokenInfo{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ClientID:     clientID,
		Subject:      authCode.Subject,
		Scopes:       authCode.Scopes,
		Resource:     authCode.Resource,
		IssuedAt:     time.Now(),
		ExpiresAt:    time.Now().Add(s.options.AccessTokenExpiry),
	})

	// Send response
	resp := TokenResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int(s.options.AccessTokenExpiry.Seconds()),
		Scope:       strings.Join(authCode.Scopes, " "),
	}
	if refreshToken != "" {
		resp.RefreshToken = refreshToken
	}

	s.sendTokenResponse(w, resp)
}

// handleRefreshTokenGrant handles refresh_token grant type.
func (s *OAuthTestServer) handleRefreshTokenGrant(w http.ResponseWriter, r *http.Request) {
	refreshToken := r.FormValue("refresh_token")
	clientID := r.FormValue("client_id")
	clientSecret := r.FormValue("client_secret")
	scope := r.FormValue("scope")

	// Check for error injection
	if s.options.ErrorMode.TokenInvalidClient {
		s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Injected error")
		return
	}

	if s.options.ErrorMode.TokenInvalidGrant {
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "Injected error")
		return
	}

	// Try to get client credentials from Basic auth header
	if clientID == "" {
		var ok bool
		clientID, clientSecret, ok = r.BasicAuth()
		if !ok {
			s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Missing client credentials")
			return
		}
	}

	// Validate client
	client, exists := s.GetClient(clientID)
	if !exists {
		s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Unknown client")
		return
	}

	// Validate client secret for confidential clients
	if !client.IsPublic && client.ClientSecret != clientSecret {
		s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Invalid client secret")
		return
	}

	// Validate refresh token
	tokenData, valid := s.validateRefreshToken(refreshToken)
	if !valid {
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "Invalid or expired refresh token")
		return
	}

	// Verify client owns this refresh token
	if tokenData.ClientID != clientID {
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "Refresh token belongs to different client")
		return
	}

	// Parse requested scopes (must be subset of original)
	scopes := tokenData.Scopes
	if scope != "" {
		requestedScopes := strings.Fields(scope)
		scopes = s.intersectScopes(requestedScopes, tokenData.Scopes)
		if len(scopes) == 0 {
			if s.options.ErrorMode.TokenInvalidScope {
				s.tokenError(w, http.StatusBadRequest, "invalid_scope", "Injected error")
				return
			}
			s.tokenError(w, http.StatusBadRequest, "invalid_scope", "Requested scopes not in original grant")
			return
		}
	}

	// Generate new access token
	accessToken, err := s.generateAccessToken(tokenData.Subject, clientID, scopes, tokenData.Resource)
	if err != nil {
		s.tokenError(w, http.StatusInternalServerError, "server_error", "Failed to generate access token")
		return
	}

	// Optionally rotate refresh token
	newRefreshToken := s.generateRefreshToken(tokenData.Subject, clientID, scopes, tokenData.Resource)

	// Revoke old refresh token (token rotation)
	s.revokeRefreshToken(refreshToken)

	// Record for test verification
	s.recordIssuedToken(TokenInfo{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		ClientID:     clientID,
		Subject:      tokenData.Subject,
		Scopes:       scopes,
		Resource:     tokenData.Resource,
		IssuedAt:     time.Now(),
		ExpiresAt:    time.Now().Add(s.options.AccessTokenExpiry),
	})

	// Send response
	resp := TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.options.AccessTokenExpiry.Seconds()),
		RefreshToken: newRefreshToken,
		Scope:        strings.Join(scopes, " "),
	}

	s.sendTokenResponse(w, resp)
}

// handleClientCredentialsGrant handles client_credentials grant type.
func (s *OAuthTestServer) handleClientCredentialsGrant(w http.ResponseWriter, r *http.Request) {
	if !s.options.EnableClientCredentials {
		s.tokenError(w, http.StatusBadRequest, "unsupported_grant_type", "Client credentials grant not enabled")
		return
	}

	clientID := r.FormValue("client_id")
	clientSecret := r.FormValue("client_secret")
	scope := r.FormValue("scope")
	resource := r.FormValue("resource")

	// Check for error injection
	if s.options.ErrorMode.TokenInvalidClient {
		s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Injected error")
		return
	}

	// Try to get client credentials from Basic auth header
	if clientID == "" {
		var ok bool
		clientID, clientSecret, ok = r.BasicAuth()
		if !ok {
			s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Missing client credentials")
			return
		}
	}

	// Validate client
	client, exists := s.GetClient(clientID)
	if !exists {
		s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Unknown client")
		return
	}

	// Client credentials requires confidential client
	if client.IsPublic {
		s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Public clients cannot use client_credentials")
		return
	}

	// Validate client secret
	if client.ClientSecret != clientSecret {
		s.tokenError(w, http.StatusUnauthorized, "invalid_client", "Invalid client secret")
		return
	}

	// Parse scopes
	scopes := s.parseScopes(scope)
	scopes = s.intersectScopes(scopes, client.Scopes)
	if len(scopes) == 0 {
		scopes = s.options.DefaultScopes
	}

	// Generate access token (subject is the client ID itself)
	accessToken, err := s.generateAccessToken(clientID, clientID, scopes, resource)
	if err != nil {
		s.tokenError(w, http.StatusInternalServerError, "server_error", "Failed to generate access token")
		return
	}

	// Record for test verification
	s.recordIssuedToken(TokenInfo{
		AccessToken: accessToken,
		ClientID:    clientID,
		Subject:     clientID,
		Scopes:      scopes,
		Resource:    resource,
		IssuedAt:    time.Now(),
		ExpiresAt:   time.Now().Add(s.options.AccessTokenExpiry),
	})

	// Send response (no refresh token for client credentials)
	resp := TokenResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int(s.options.AccessTokenExpiry.Seconds()),
		Scope:       strings.Join(scopes, " "),
	}

	s.sendTokenResponse(w, resp)
}

// tokenError sends an OAuth error response.
func (s *OAuthTestServer) tokenError(w http.ResponseWriter, status int, errorCode, description string) {
	resp := TokenErrorResponse{
		Error:            errorCode,
		ErrorDescription: description,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// sendTokenResponse sends a successful token response.
func (s *OAuthTestServer) sendTokenResponse(w http.ResponseWriter, resp TokenResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	json.NewEncoder(w).Encode(resp)
}

// intersectScopes returns scopes that are in both lists.
func (s *OAuthTestServer) intersectScopes(requested, allowed []string) []string {
	allowedMap := make(map[string]bool)
	for _, sc := range allowed {
		allowedMap[sc] = true
	}

	result := make([]string, 0)
	for _, sc := range requested {
		if allowedMap[sc] {
			result = append(result, sc)
		}
	}
	return result
}
