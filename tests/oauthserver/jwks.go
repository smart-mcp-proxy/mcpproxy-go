package oauthserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
)

// KeyRing manages RSA key pairs for JWT signing with rotation support.
// With Options.OIDC it also holds EC (P-256) keys, published in the JWKS and
// used only by the IDTokenKeyAlgMismatch tamper knob and by tests that mint
// their own tokens; access tokens and id_tokens are always RS256.
type KeyRing struct {
	keys      map[string]*rsa.PrivateKey
	ecKeys    map[string]*ecdsa.PrivateKey
	ecKids    []string // insertion order, so GetJWKS is deterministic
	activeKid string
	mu        sync.RWMutex
}

// JWK represents a JSON Web Key (RSA: n,e; EC: crv,x,y).
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n,omitempty"`
	E   string `json:"e,omitempty"`
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
}

// JWKS represents a JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// NewKeyRing creates a new KeyRing with an initial RSA key.
func NewKeyRing() (*KeyRing, error) {
	kr := &KeyRing{
		keys:   make(map[string]*rsa.PrivateKey),
		ecKeys: make(map[string]*ecdsa.PrivateKey),
	}

	// Generate initial key
	kid := "key-1"
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	kr.keys[kid] = key
	kr.activeKid = kid

	return kr, nil
}

// AddKey adds a new RSA key to the key ring.
func (kr *KeyRing) AddKey(kid string, key *rsa.PrivateKey) error {
	kr.mu.Lock()
	defer kr.mu.Unlock()

	if _, exists := kr.keys[kid]; exists {
		return fmt.Errorf("key with ID %q already exists", kid)
	}

	kr.keys[kid] = key
	return nil
}

// RotateTo switches the active signing key to the specified key ID.
func (kr *KeyRing) RotateTo(kid string) error {
	kr.mu.Lock()
	defer kr.mu.Unlock()

	if _, exists := kr.keys[kid]; !exists {
		return fmt.Errorf("key with ID %q does not exist", kid)
	}

	kr.activeKid = kid
	return nil
}

// RotateKey adds a new signing key and makes it active.
// The old key remains valid for verification.
// Returns the new key ID.
func (kr *KeyRing) RotateKey() (string, error) {
	kr.mu.Lock()
	defer kr.mu.Unlock()

	// Generate new key ID
	newKid := fmt.Sprintf("key-%d", len(kr.keys)+1)

	// Generate new RSA key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", fmt.Errorf("failed to generate RSA key: %w", err)
	}

	kr.keys[newKid] = key
	kr.activeKid = newKid

	return newKid, nil
}

// RemoveKey removes a key from the key ring.
// Tokens signed with this key will fail verification.
func (kr *KeyRing) RemoveKey(kid string) error {
	kr.mu.Lock()
	defer kr.mu.Unlock()

	if _, exists := kr.keys[kid]; !exists {
		return fmt.Errorf("key with ID %q does not exist", kid)
	}

	if kid == kr.activeKid {
		return fmt.Errorf("cannot remove active key %q", kid)
	}

	delete(kr.keys, kid)
	return nil
}

// GetActiveKey returns the currently active private key and its ID.
func (kr *KeyRing) GetActiveKey() (string, *rsa.PrivateKey) {
	kr.mu.RLock()
	defer kr.mu.RUnlock()

	return kr.activeKid, kr.keys[kr.activeKid]
}

// GetKey returns a key by ID.
func (kr *KeyRing) GetKey(kid string) (*rsa.PrivateKey, bool) {
	kr.mu.RLock()
	defer kr.mu.RUnlock()

	key, exists := kr.keys[kid]
	return key, exists
}

// AddECKey generates a P-256 key, adds it to the ring and returns its ID.
// The RSA signing key stays active; the EC key is only published.
func (kr *KeyRing) AddECKey() (string, error) {
	kr.mu.Lock()
	defer kr.mu.Unlock()

	kid := fmt.Sprintf("ec-%d", len(kr.ecKeys)+1)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("failed to generate EC key: %w", err)
	}
	kr.ecKeys[kid] = key
	kr.ecKids = append(kr.ecKids, kid)
	return kid, nil
}

// GetECKey returns the first EC key and its ID, or ("", nil) when none exists.
func (kr *KeyRing) GetECKey() (string, *ecdsa.PrivateKey) {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	if len(kr.ecKids) == 0 {
		return "", nil
	}
	return kr.ecKids[0], kr.ecKeys[kr.ecKids[0]]
}

// GetJWKS returns the public keys in JWK Set format (RSA keys first, then EC).
func (kr *KeyRing) GetJWKS() *JWKS {
	kr.mu.RLock()
	defer kr.mu.RUnlock()

	jwks := &JWKS{
		Keys: make([]JWK, 0, len(kr.keys)+len(kr.ecKeys)),
	}

	for kid, key := range kr.keys {
		jwk := JWK{
			Kty: "RSA",
			Use: "sig",
			Kid: kid,
			Alg: "RS256",
			N:   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}
		jwks.Keys = append(jwks.Keys, jwk)
	}

	for _, kid := range kr.ecKids {
		pub := kr.ecKeys[kid].PublicKey
		size := (pub.Curve.Params().BitSize + 7) / 8
		jwks.Keys = append(jwks.Keys, JWK{
			Kty: "EC",
			Use: "sig",
			Kid: kid,
			Alg: "ES256",
			Crv: "P-256",
			X:   base64.RawURLEncoding.EncodeToString(pub.X.FillBytes(make([]byte, size))),
			Y:   base64.RawURLEncoding.EncodeToString(pub.Y.FillBytes(make([]byte, size))),
		})
	}

	return jwks
}

// GetActiveKid returns the currently active key ID.
func (kr *KeyRing) GetActiveKid() string {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	return kr.activeKid
}

// GetPublicKey returns the public key of the currently active key for token verification.
func (kr *KeyRing) GetPublicKey() *rsa.PublicKey {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	if key, exists := kr.keys[kr.activeKid]; exists {
		return &key.PublicKey
	}
	return nil
}

// handleJWKS handles GET /jwks.json requests.
func (s *OAuthTestServer) handleJWKS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	jwks := s.keyRing.GetJWKS()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if err := json.NewEncoder(w).Encode(jwks); err != nil {
		http.Error(w, "Failed to encode JWKS", http.StatusInternalServerError)
		return
	}
}
