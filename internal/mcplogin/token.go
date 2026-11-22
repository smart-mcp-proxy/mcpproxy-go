package mcplogin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	// AccessTokenPrefix starts every browser-login access token.
	AccessTokenPrefix = "mcp_oat_"
	// RefreshTokenPrefix starts every browser-login refresh token.
	RefreshTokenPrefix = "mcp_ort_"

	// GrantIDLen is the length of a grant id (base62 characters).
	GrantIDLen = 22

	secretLen  = 43 // 32 random bytes, base64url without padding
	macLen     = 22 // 16 bytes, base64url without padding
	macBytes   = 16
	prefixShow = 12

	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// Hash kinds for HashSecret. Each has its own label, so a hash made for one
// kind can never equal a hash made for another.
const (
	// KindAccess labels hashes of access tokens.
	KindAccess = "access"
	// KindRefresh labels hashes of refresh-token secrets.
	KindRefresh = "refresh"
	// KindCode labels hashes of authorization codes.
	KindCode = "code"
)

// ErrBadKey is returned when a token key is empty.
var ErrBadKey = errors.New("mcplogin: token key is empty")

// ErrBadGrantID is returned when a grant id is not GrantIDLen base62 characters.
var ErrBadGrantID = errors.New("mcplogin: malformed grant id")

// randomBytes reads n bytes from crypto/rand.
func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// randomBase62 returns n unbiased random base62 characters. Bytes at or above
// 248 (the largest multiple of 62 that fits in a byte) are rejected.
func randomBase62(n int) (string, error) {
	out := make([]byte, 0, n)
	buf := make([]byte, n+n/2+8)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, c := range buf {
			if c >= 248 {
				continue
			}
			out = append(out, base62Alphabet[int(c)%62])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

func randomSecret() (string, error) {
	b, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewAccessToken returns a fresh access token: "mcp_oat_" plus 43 base64url
// characters (32 random bytes).
func NewAccessToken() (string, error) {
	s, err := randomSecret()
	if err != nil {
		return "", err
	}
	return AccessTokenPrefix + s, nil
}

// NewGrantID returns a fresh grant id of GrantIDLen base62 characters.
func NewGrantID() (string, error) { return randomBase62(GrantIDLen) }

func validBase62(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(base62Alphabet, s[i]) < 0 {
			return false
		}
	}
	return true
}

func validBase64URL(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func refreshMAC(key []byte, grantID, secret string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("mcplogin|ort|v1|" + grantID + "|" + secret))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:macBytes])
}

// NewRefreshToken issues "mcp_ort_<grantID>_<secret>_<mac>" for a grant. It
// returns the whole token and the secret part, which the caller stores hashed.
func NewRefreshToken(key []byte, grantID string) (token, secret string, err error) {
	if len(key) == 0 {
		return "", "", ErrBadKey
	}
	if !validBase62(grantID, GrantIDLen) {
		return "", "", ErrBadGrantID
	}
	secret, err = randomSecret()
	if err != nil {
		return "", "", err
	}
	return RefreshTokenPrefix + grantID + "_" + secret + "_" + refreshMAC(key, grantID, secret), secret, nil
}

// ParseRefreshToken splits a refresh token by fixed field lengths (the secret
// and the MAC may themselves contain '_') and verifies the MAC in constant
// time. ok is false for every malformed token and for a bad MAC, with nothing
// to tell the two apart.
func ParseRefreshToken(key []byte, token string) (grantID, secret string, ok bool) {
	if len(key) == 0 || !strings.HasPrefix(token, RefreshTokenPrefix) {
		return "", "", false
	}
	rest := token[len(RefreshTokenPrefix):]
	if len(rest) != GrantIDLen+1+secretLen+1+macLen {
		return "", "", false
	}
	grantID = rest[:GrantIDLen]
	secret = rest[GrantIDLen+1 : GrantIDLen+1+secretLen]
	mac := rest[GrantIDLen+1+secretLen+1:]
	if rest[GrantIDLen] != '_' || rest[GrantIDLen+1+secretLen] != '_' ||
		!validBase62(grantID, GrantIDLen) || !validBase64URL(secret, secretLen) || !validBase64URL(mac, macLen) {
		return "", "", false
	}
	if !hmac.Equal([]byte(mac), []byte(refreshMAC(key, grantID, secret))) {
		return "", "", false
	}
	return grantID, secret, true
}

// HashSecret returns the hex HMAC-SHA256 of secret under key, labelled by
// kind (KindAccess, KindRefresh, KindCode), so hashes of different kinds, and
// agent-token hashes, never collide.
func HashSecret(key []byte, kind, secret string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("mcplogin|hash|v1|" + kind + "|" + secret))
	return hex.EncodeToString(m.Sum(nil))
}

// SecretEqual compares two strings in constant time.
func SecretEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// DisplayPrefix returns the first 12 characters of a token, safe to show.
func DisplayPrefix(token string) string {
	if len(token) <= prefixShow {
		return token
	}
	return token[:prefixShow]
}

// IsAccessToken reports whether s carries the access-token prefix.
func IsAccessToken(s string) bool { return strings.HasPrefix(s, AccessTokenPrefix) }

// IsRefreshToken reports whether s carries the refresh-token prefix.
func IsRefreshToken(s string) bool { return strings.HasPrefix(s, RefreshTokenPrefix) }
