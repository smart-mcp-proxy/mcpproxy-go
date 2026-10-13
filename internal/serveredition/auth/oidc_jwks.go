//go:build server

package auth

// Stdlib JWK Set parsing for the generic `oidc` provider (Spec 107 FR-020,
// research D3): RSA (`n`,`e`) and EC (`crv`,`x`,`y`) public keys only, keyed
// by `kid`. Anything else — symmetric `oct`, `OKP`, an unknown curve, a
// malformed coordinate, a point off its curve — is refused per entry, and a
// refused or kid-less entry is skipped so one exotic key cannot take a whole
// set with it. The algorithm is decided by the verifier's allowed-alg list,
// never by a JWK's `alg`/`use`.

import (
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
)

// jwk is the subset of RFC 7517/7518 a signing key needs.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// parseJWKS decodes a JWK Set document into kid → public key. The document
// must be a JSON object with a `keys` array; entries that are not usable
// signing keys or carry no kid are skipped. A duplicate kid keeps the last
// entry (deterministic, never an error).
func parseJWKS(data []byte) (map[string]crypto.PublicKey, error) {
	var doc struct {
		Keys *[]json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("jwks: decoding document: %w", err)
	}
	if doc.Keys == nil {
		return nil, fmt.Errorf("jwks: document has no keys array")
	}
	keys := make(map[string]crypto.PublicKey, len(*doc.Keys))
	for _, raw := range *doc.Keys {
		var k jwk
		if err := json.Unmarshal(raw, &k); err != nil {
			return nil, fmt.Errorf("jwks: decoding key entry: %w", err)
		}
		if k.Kid == "" {
			continue
		}
		pub, err := jwkToPublicKey(k)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	return keys, nil
}

// jwkToPublicKey converts one JWK into an *rsa.PublicKey or *ecdsa.PublicKey.
func jwkToPublicKey(k jwk) (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		return rsaFromJWK(k)
	case "EC":
		return ecFromJWK(k)
	default:
		return nil, fmt.Errorf("jwk: unsupported kty %q", k.Kty)
	}
}

func rsaFromJWK(k jwk) (crypto.PublicKey, error) {
	if k.N == "" || k.E == "" {
		return nil, fmt.Errorf("jwk: RSA key needs n and e")
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("jwk: RSA n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("jwk: RSA e: %w", err)
	}
	if len(eBytes) == 0 || len(eBytes) > 4 {
		return nil, fmt.Errorf("jwk: RSA e has %d bytes", len(eBytes))
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	if n.Sign() <= 0 || n.BitLen() < 512 || e.Int64() < 3 {
		return nil, fmt.Errorf("jwk: RSA modulus or exponent out of range")
	}
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

func ecFromJWK(k jwk) (crypto.PublicKey, error) {
	var (
		curve elliptic.Curve
		check ecdh.Curve
	)
	switch k.Crv {
	case "P-256":
		curve, check = elliptic.P256(), ecdh.P256()
	case "P-384":
		curve, check = elliptic.P384(), ecdh.P384()
	case "P-521":
		curve, check = elliptic.P521(), ecdh.P521()
	default:
		return nil, fmt.Errorf("jwk: unsupported EC curve %q", k.Crv)
	}
	if k.X == "" || k.Y == "" {
		return nil, fmt.Errorf("jwk: EC key needs x and y")
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, fmt.Errorf("jwk: EC x: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, fmt.Errorf("jwk: EC y: %w", err)
	}
	size := (curve.Params().BitSize + 7) / 8
	if len(xBytes) != size || len(yBytes) != size {
		return nil, fmt.Errorf("jwk: EC coordinates must be %d bytes for %s", size, k.Crv)
	}
	// crypto/ecdh refuses the identity and any point off the curve; it is the
	// non-deprecated on-curve check.
	uncompressed := append(append([]byte{4}, xBytes...), yBytes...)
	if _, err := check.NewPublicKey(uncompressed); err != nil {
		return nil, fmt.Errorf("jwk: EC point is not on %s: %w", k.Crv, err)
	}
	return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(xBytes), Y: new(big.Int).SetBytes(yBytes)}, nil
}
