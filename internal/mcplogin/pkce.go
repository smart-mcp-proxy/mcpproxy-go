package mcplogin

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// VerifyS256 reports whether challenge is the S256 transform of verifier
// (RFC 7636): the verifier is 43 to 128 characters of the unreserved set and
// the challenge is base64url(sha256(verifier)) without padding.
func VerifyS256(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for i := 0; i < len(verifier); i++ {
		c := verifier[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
		default:
			return false
		}
	}
	if !ValidChallenge(challenge) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(want), []byte(challenge)) == 1
}

// ValidChallenge reports whether s is shaped like an S256 challenge: 43
// base64url characters.
func ValidChallenge(s string) bool { return validBase64URL(s, 43) }
