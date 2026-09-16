package oauthserver

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

	// FR-020 knob: the token endpoint answers 302 to another origin. A
	// back-channel client that follows it would leak the code and the client
	// secret; the fake answers before it even looks at the credentials.
	if s.options.OIDC && s.errorMode().TokenEndpointRedirect {
		w.Header().Set("Location", "https://token.invalid/token")
		w.WriteHeader(http.StatusFound)
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

	// OIDC: the auth-code response carries an id_token (Options.OIDC).
	if s.options.OIDC {
		idToken, err := s.mintIDToken(authCode, client)
		if err != nil {
			s.tokenError(w, http.StatusInternalServerError, "server_error", "Failed to generate id_token")
			return
		}
		resp.IDToken = idToken
	}

	s.sendTokenResponse(w, resp)
}

// mintIDToken builds the OIDC id_token for an authorization code (research
// D2): RS256 by the active KeyRing key with iss, sub, aud, azp, exp, iat,
// nonce, email, email_verified, name and the groups claim from UserClaims.
// Every ErrorMode tamper knob changes exactly one thing about the result.
func (s *OAuthTestServer) mintIDToken(authCode *AuthorizationCode, client *Client) (string, error) {
	mode := s.errorMode()
	claims := s.identityClaims(authCode.Subject)
	now := time.Now()

	claims["iss"] = s.issuerURL
	if mode.IDTokenWrongIssuer {
		claims["iss"] = "https://issuer.invalid"
	}

	claims["aud"] = client.ClientID
	claims["azp"] = client.ClientID
	switch {
	case mode.IDTokenWrongAudience:
		claims["aud"] = "not-" + client.ClientID
		claims["azp"] = "not-" + client.ClientID
	case mode.IDTokenMultiAudNoAzp:
		claims["aud"] = []string{client.ClientID, "second-audience"}
		delete(claims, "azp")
	}

	claims["iat"] = now.Unix()
	claims["exp"] = now.Add(time.Hour).Unix()
	switch {
	case mode.IDTokenExpired:
		claims["iat"] = now.Add(-2 * time.Hour).Unix()
		claims["exp"] = now.Add(-time.Hour).Unix()
	case mode.IDTokenNbfFuture:
		claims["nbf"] = now.Add(time.Hour).Unix()
		claims["exp"] = now.Add(2 * time.Hour).Unix()
	}

	if authCode.Nonce != "" && !mode.IDTokenNoNonce {
		claims["nonce"] = authCode.Nonce
	}

	// The userinfo knobs need the verifier to consult /userinfo, which it
	// only does when the token lacks the groups claim.
	if mode.userinfoConsulted() {
		delete(claims, s.options.GroupsClaim)
	}

	return s.signIDToken(claims, client, mode)
}

// signIDToken signs the claims per the tamper knobs; the default is RS256 by
// the active key with its kid in the header.
func (s *OAuthTestServer) signIDToken(claims jwt.MapClaims, client *Client, mode ErrorMode) (string, error) {
	activeKid, activeKey := s.keyRing.GetActiveKey()

	switch {
	case mode.IDTokenAlgNone:
		tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
		tok.Header["kid"] = activeKid
		return tok.SignedString(jwt.UnsafeAllowNoneSignatureType)

	case mode.IDTokenHS256:
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tok.Header["kid"] = activeKid
		secret := client.ClientSecret
		if secret == "" {
			secret = "public-client-has-no-secret"
		}
		return tok.SignedString([]byte(secret))

	case mode.IDTokenKeyAlgMismatch:
		ecKid, ecKey := s.keyRing.GetECKey()
		if ecKey == nil {
			return "", fmt.Errorf("no EC key in the ring (kid %q)", ecKid)
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
		tok.Header["kid"] = activeKid // names the RSA key, alg says ES256
		return tok.SignedString(ecKey)

	case mode.IDTokenBadSignature:
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = activeKid // published kid, unpublished key
		return tok.SignedString(s.bogusSigningKey())

	case mode.IDTokenUnknownKid:
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "no-such-kid"
		return tok.SignedString(activeKey)

	default:
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = activeKid
		return tok.SignedString(activeKey)
	}
}

// bogusSigningKey returns a lazily generated RSA key that is never published
// in the JWKS (IDTokenBadSignature).
func (s *OAuthTestServer) bogusSigningKey() *rsa.PrivateKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bogusKey == nil {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(fmt.Sprintf("failed to generate bogus RSA key: %v", err))
		}
		s.bogusKey = key
	}
	return s.bogusKey
}

// identityClaims returns the OIDC identity claims for a ValidUsers entry:
// UserClaims first, derived defaults for anything missing, then the
// EmailVerified/Groups tamper knobs. Shared by the id_token and /userinfo.
func (s *OAuthTestServer) identityClaims(username string) jwt.MapClaims {
	mode := s.errorMode()
	groupsClaim := s.options.GroupsClaim
	claims := jwt.MapClaims{}
	for k, v := range s.options.UserClaims[username] {
		claims[k] = v
	}

	if _, ok := claims["email"]; !ok {
		if strings.Contains(username, "@") {
			claims["email"] = username
		} else {
			claims["email"] = username + "@example.com"
		}
	}
	if _, ok := claims["sub"]; !ok {
		sum := sha256.Sum256([]byte(username))
		claims["sub"] = "sub-" + hex.EncodeToString(sum[:8])
	}
	if _, ok := claims["email_verified"]; !ok {
		claims["email_verified"] = true
	}
	if _, ok := claims["name"]; !ok {
		email, _ := claims["email"].(string)
		claims["name"] = strings.SplitN(email, "@", 2)[0]
	}
	// Groups may be given under the configured claim name or plain "groups".
	if _, ok := claims[groupsClaim]; !ok {
		if g, ok := claims["groups"]; ok {
			claims[groupsClaim] = g
		} else {
			claims[groupsClaim] = []any{}
		}
	}
	if groupsClaim != "groups" {
		delete(claims, "groups")
	}

	if mode.EmailVerifiedFalse {
		claims["email_verified"] = false
	}
	switch {
	case mode.GroupsNonArray:
		claims[groupsClaim] = joinGroups(claims[groupsClaim])
	case mode.GroupsOverageMarker:
		delete(claims, groupsClaim)
		sub, _ := claims["sub"].(string)
		claims["_claim_names"] = map[string]any{groupsClaim: "src1"}
		claims["_claim_sources"] = map[string]any{
			"src1": map[string]any{"endpoint": s.issuerURL + "/users/" + sub + "/getMemberObjects"},
		}
	case mode.GroupsAbsentEverywhere:
		delete(claims, groupsClaim)
	}
	return claims
}

// joinGroups renders a groups value as a single comma-joined string.
func joinGroups(v any) string {
	var parts []string
	switch g := v.(type) {
	case []string:
		parts = g
	case []any:
		for _, item := range g {
			parts = append(parts, fmt.Sprint(item))
		}
	default:
		return fmt.Sprint(v)
	}
	if len(parts) == 0 {
		return "eng"
	}
	return strings.Join(parts, ",")
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
