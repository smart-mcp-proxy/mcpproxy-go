package oauthserver

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenClaims represents the claims in an access token.
type TokenClaims struct {
	jwt.RegisteredClaims
	ClientID string `json:"client_id,omitempty"`
	Scope    string `json:"scope,omitempty"`
}

// generateTokenID creates a unique token identifier.
func generateTokenID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// generateAccessToken creates a signed JWT access token.
func (s *OAuthTestServer) generateAccessToken(subject, clientID string, scopes []string, resource string) (string, error) {
	kid, key := s.keyRing.GetActiveKey()

	now := time.Now()
	expiry := now.Add(s.options.AccessTokenExpiry)

	claims := TokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuerURL,
			Subject:   subject,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiry),
			ID:        generateTokenID(),
		},
		ClientID: clientID,
		Scope:    strings.Join(scopes, " "),
	}

	// Add audience if resource indicator was provided (RFC 8707)
	if resource != "" {
		claims.Audience = jwt.ClaimStrings{resource}
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid

	signedToken, err := token.SignedString(key)
	if err != nil {
		return "", err
	}

	return signedToken, nil
}

// generateRefreshToken creates an opaque refresh token and stores its data.
func (s *OAuthTestServer) generateRefreshToken(subject, clientID string, scopes []string, resource string) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)

	s.mu.Lock()
	s.refreshTokens[token] = &RefreshTokenData{
		Token:     token,
		ClientID:  clientID,
		Subject:   subject,
		Scopes:    scopes,
		Resource:  resource,
		ExpiresAt: time.Now().Add(s.options.RefreshTokenExpiry),
	}
	s.mu.Unlock()

	return token
}

// validateRefreshToken checks if a refresh token is valid.
func (s *OAuthTestServer) validateRefreshToken(token string) (*RefreshTokenData, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, exists := s.refreshTokens[token]
	if !exists {
		return nil, false
	}

	if data.IsExpired() {
		return nil, false
	}

	return data, true
}

// revokeRefreshToken removes a refresh token.
func (s *OAuthTestServer) revokeRefreshToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.refreshTokens, token)
}

// TokenInfo contains information about an issued token (for testing).
type TokenInfo struct {
	AccessToken  string
	RefreshToken string
	ClientID     string
	Subject      string
	Scopes       []string
	Resource     string
	IssuedAt     time.Time
	ExpiresAt    time.Time
}

// GetIssuedTokens returns all tokens issued (for verification in tests).
func (s *OAuthTestServer) GetIssuedTokens() []TokenInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tokens := make([]TokenInfo, 0, len(s.issuedTokens))
	tokens = append(tokens, s.issuedTokens...)
	return tokens
}

// recordIssuedToken records a token for test verification.
func (s *OAuthTestServer) recordIssuedToken(info TokenInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issuedTokens = append(s.issuedTokens, info)
}
