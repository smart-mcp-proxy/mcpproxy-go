//go:build teams

package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testKey = []byte("test-hmac-key-for-testing-only-32bytes!")

func TestGenerateBearerToken_Success(t *testing.T) {
	token, err := GenerateBearerToken(testKey, "user-001", "alice@example.com", "Alice", "admin", "google", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	// JWT tokens have 3 dot-separated segments
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT segments, got %d", len(parts))
	}
}

func TestGenerateBearerToken_Claims(t *testing.T) {
	token, err := GenerateBearerToken(testKey, "user-002", "bob@example.com", "Bob Smith", "user", "github", 2*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Validate and inspect claims
	claims, err := ValidateBearerToken(token, testKey)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	if claims.Subject != "user-002" {
		t.Errorf("subject = %q, want %q", claims.Subject, "user-002")
	}
	if claims.Email != "bob@example.com" {
		t.Errorf("email = %q, want %q", claims.Email, "bob@example.com")
	}
	if claims.DisplayName != "Bob Smith" {
		t.Errorf("display_name = %q, want %q", claims.DisplayName, "Bob Smith")
	}
	if claims.Role != "user" {
		t.Errorf("role = %q, want %q", claims.Role, "user")
	}
	if claims.Provider != "github" {
		t.Errorf("provider = %q, want %q", claims.Provider, "github")
	}
	if claims.ID == "" {
		t.Error("expected non-empty JTI (jti)")
	}
	if claims.IssuedAt == nil {
		t.Error("expected non-nil IssuedAt (iat)")
	}
	if claims.ExpiresAt == nil {
		t.Fatal("expected non-nil ExpiresAt (exp)")
	}
	// Expiry should be approximately 2 hours from now
	expectedExpiry := time.Now().UTC().Add(2 * time.Hour)
	diff := claims.ExpiresAt.Time.Sub(expectedExpiry)
	if diff < -5*time.Second || diff > 5*time.Second {
		t.Errorf("expiry differs from expected by %v", diff)
	}
}

func TestValidateBearerToken_Valid(t *testing.T) {
	token, err := GenerateBearerToken(testKey, "user-003", "carol@example.com", "Carol", "admin", "microsoft", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error generating token: %v", err)
	}

	claims, err := ValidateBearerToken(token, testKey)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if claims.Subject != "user-003" {
		t.Errorf("subject = %q, want %q", claims.Subject, "user-003")
	}
	if claims.Email != "carol@example.com" {
		t.Errorf("email = %q, want %q", claims.Email, "carol@example.com")
	}
}

func TestValidateBearerToken_Expired(t *testing.T) {
	// Generate a token that expired 1 hour ago
	token, err := GenerateBearerToken(testKey, "user-004", "dave@example.com", "Dave", "user", "google", -time.Hour)
	if err != nil {
		t.Fatalf("unexpected error generating token: %v", err)
	}

	_, err = ValidateBearerToken(token, testKey)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if !strings.Contains(err.Error(), "token is expired") {
		t.Errorf("expected 'token is expired' in error, got: %v", err)
	}
}

func TestValidateBearerToken_WrongKey(t *testing.T) {
	token, err := GenerateBearerToken(testKey, "user-005", "eve@example.com", "Eve", "admin", "github", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error generating token: %v", err)
	}

	wrongKey := []byte("wrong-key-that-is-completely-different!!")
	_, err = ValidateBearerToken(token, wrongKey)
	if err == nil {
		t.Fatal("expected error for wrong key, got nil")
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("expected 'invalid token' in error, got: %v", err)
	}
}

func TestValidateBearerToken_TamperedToken(t *testing.T) {
	token, err := GenerateBearerToken(testKey, "user-006", "frank@example.com", "Frank", "user", "microsoft", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error generating token: %v", err)
	}

	// Tamper with the payload by flipping a character
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("unexpected token format")
	}
	payload := []byte(parts[1])
	if len(payload) > 0 {
		// Flip a byte in the payload
		payload[0] = payload[0] ^ 0x01
	}
	tampered := parts[0] + "." + string(payload) + "." + parts[2]

	_, err = ValidateBearerToken(tampered, testKey)
	if err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
}

func TestValidateBearerToken_MalformedToken(t *testing.T) {
	_, err := ValidateBearerToken("not-a-jwt-token-at-all", testKey)
	if err == nil {
		t.Fatal("expected error for malformed token, got nil")
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("expected 'invalid token' in error, got: %v", err)
	}
}

func TestValidateBearerToken_EmptyToken(t *testing.T) {
	_, err := ValidateBearerToken("", testKey)
	if err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("expected 'must not be empty' in error, got: %v", err)
	}
}

func TestGenerateBearerToken_UniqueJTI(t *testing.T) {
	token1, err := GenerateBearerToken(testKey, "user-007", "grace@example.com", "Grace", "admin", "google", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error generating token 1: %v", err)
	}
	token2, err := GenerateBearerToken(testKey, "user-007", "grace@example.com", "Grace", "admin", "google", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error generating token 2: %v", err)
	}

	claims1, err := ValidateBearerToken(token1, testKey)
	if err != nil {
		t.Fatalf("unexpected validation error for token 1: %v", err)
	}
	claims2, err := ValidateBearerToken(token2, testKey)
	if err != nil {
		t.Fatalf("unexpected validation error for token 2: %v", err)
	}

	if claims1.ID == claims2.ID {
		t.Errorf("expected unique JTIs, but both are %q", claims1.ID)
	}
	if claims1.ID == "" || claims2.ID == "" {
		t.Error("expected non-empty JTI values")
	}
}

func TestParseUnverified(t *testing.T) {
	token, err := GenerateBearerToken(testKey, "user-008", "heidi@example.com", "Heidi", "user", "github", time.Hour)
	if err != nil {
		t.Fatalf("unexpected error generating token: %v", err)
	}

	// Parse without verification
	claims, err := ParseBearerTokenUnverified(token)
	if err != nil {
		t.Fatalf("unexpected error parsing unverified: %v", err)
	}

	if claims.Subject != "user-008" {
		t.Errorf("subject = %q, want %q", claims.Subject, "user-008")
	}
	if claims.Email != "heidi@example.com" {
		t.Errorf("email = %q, want %q", claims.Email, "heidi@example.com")
	}
	if claims.Role != "user" {
		t.Errorf("role = %q, want %q", claims.Role, "user")
	}
	if claims.Provider != "github" {
		t.Errorf("provider = %q, want %q", claims.Provider, "github")
	}

	// Should also work with a wrong key (since it's unverified)
	wrongKeyToken, _ := GenerateBearerToken([]byte("another-key-for-testing-purposes!!"), "user-009", "ivan@example.com", "Ivan", "admin", "microsoft", time.Hour)
	claims2, err := ParseBearerTokenUnverified(wrongKeyToken)
	if err != nil {
		t.Fatalf("unexpected error parsing unverified with different key: %v", err)
	}
	if claims2.Subject != "user-009" {
		t.Errorf("subject = %q, want %q", claims2.Subject, "user-009")
	}
}

func TestParseUnverified_EmptyToken(t *testing.T) {
	_, err := ParseBearerTokenUnverified("")
	if err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
}

func TestParseUnverified_MalformedToken(t *testing.T) {
	_, err := ParseBearerTokenUnverified("not.a.jwt")
	if err == nil {
		t.Fatal("expected error for malformed token, got nil")
	}
}

func TestGenerateBearerToken_ValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		key         []byte
		userID      string
		email       string
		displayName string
		role        string
		provider    string
		wantErr     string
	}{
		{
			name:     "empty key",
			key:      []byte{},
			userID:   "u1",
			email:    "a@b.com",
			role:     "admin",
			provider: "google",
			wantErr:  "HMAC key must not be empty",
		},
		{
			name:     "empty userID",
			key:      testKey,
			userID:   "",
			email:    "a@b.com",
			role:     "admin",
			provider: "google",
			wantErr:  "userID must not be empty",
		},
		{
			name:     "empty email",
			key:      testKey,
			userID:   "u1",
			email:    "",
			role:     "admin",
			provider: "google",
			wantErr:  "email must not be empty",
		},
		{
			name:     "empty role",
			key:      testKey,
			userID:   "u1",
			email:    "a@b.com",
			role:     "",
			provider: "google",
			wantErr:  "role must not be empty",
		},
		{
			name:     "empty provider",
			key:      testKey,
			userID:   "u1",
			email:    "a@b.com",
			role:     "admin",
			provider: "",
			wantErr:  "provider must not be empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GenerateBearerToken(tc.key, tc.userID, tc.email, tc.displayName, tc.role, tc.provider, time.Hour)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateBearerToken_SigningMethodMismatch(t *testing.T) {
	// Create a token signed with a different method (none)
	claims := UserClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-attacker",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        "fake-jti",
		},
		Email:    "attacker@example.com",
		Role:     "admin",
		Provider: "google",
	}

	// Sign with none method - this tests the alg check
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	tokenString, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("failed to create none-signed token: %v", err)
	}

	_, err = ValidateBearerToken(tokenString, testKey)
	if err == nil {
		t.Fatal("expected error for none-signed token, got nil")
	}
}
