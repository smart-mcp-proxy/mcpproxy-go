//go:build teams

package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// UserClaims represents the JWT claims for a team member.
type UserClaims struct {
	jwt.RegisteredClaims
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role"`     // "admin" or "user"
	Provider    string `json:"provider"` // google, github, microsoft
}

// GenerateBearerToken creates a signed JWT with team member claims.
// The token is signed with HMAC-SHA256 using the provided key (same key
// used for agent token hashing, stored at ~/.mcpproxy/.token_key).
//
// Claims included: sub=userID, email, display_name, role, provider, exp, iat, jti.
func GenerateBearerToken(hmacKey []byte, userID, email, displayName, role, provider string, ttl time.Duration) (string, error) {
	if len(hmacKey) == 0 {
		return "", fmt.Errorf("HMAC key must not be empty")
	}
	if userID == "" {
		return "", fmt.Errorf("userID must not be empty")
	}
	if email == "" {
		return "", fmt.Errorf("email must not be empty")
	}
	if role == "" {
		return "", fmt.Errorf("role must not be empty")
	}
	if provider == "" {
		return "", fmt.Errorf("provider must not be empty")
	}

	now := time.Now().UTC()
	claims := UserClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        uuid.New().String(),
		},
		Email:       email,
		DisplayName: displayName,
		Role:        role,
		Provider:    provider,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(hmacKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}
	return signed, nil
}

// ValidateBearerToken parses and validates a JWT bearer token.
// It verifies the HMAC-SHA256 signature, checks expiry, and ensures
// required claims (sub, email, role, provider) are present.
// Returns the parsed claims if the token is valid.
func ValidateBearerToken(tokenString string, hmacKey []byte) (*UserClaims, error) {
	if tokenString == "" {
		return nil, fmt.Errorf("token must not be empty")
	}
	if len(hmacKey) == 0 {
		return nil, fmt.Errorf("HMAC key must not be empty")
	}

	claims := &UserClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		// Ensure the signing method is HMAC
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return hmacKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("token is not valid")
	}

	// Verify required claims are present
	if claims.Subject == "" {
		return nil, fmt.Errorf("token missing required claim: sub")
	}
	if claims.Email == "" {
		return nil, fmt.Errorf("token missing required claim: email")
	}
	if claims.Role == "" {
		return nil, fmt.Errorf("token missing required claim: role")
	}
	if claims.Provider == "" {
		return nil, fmt.Errorf("token missing required claim: provider")
	}

	return claims, nil
}

// ParseBearerTokenUnverified parses a JWT token WITHOUT verifying the signature.
// This is useful for logging and debugging purposes only.
// Do NOT use this for authentication decisions.
func ParseBearerTokenUnverified(tokenString string) (*UserClaims, error) {
	if tokenString == "" {
		return nil, fmt.Errorf("token must not be empty")
	}

	claims := &UserClaims{}
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	_, _, err := parser.ParseUnverified(tokenString, claims)
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	return claims, nil
}
