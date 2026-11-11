// Package tokenseal seals and opens a byte slice as an "enc:v1" envelope
// (AES-256-GCM, random 96-bit nonce, HKDF-derived record key and key id) for
// values stored at rest, such as the upstream OAuth records in config.db.
//
// Envelope: enc:v1:<kid>:<base64(nonce[12] || ciphertext || tag[16])>
//
//	kid = 16 lower-case hex characters, derived from the configured key
//	AAD = "mcpproxy-oauth-token" 0x00 "v1" 0x00 kid 0x00 bucket 0x00 bucketKey
//
// The AAD binds the version, the key id, the bucket and the bucket key, so an
// envelope copied under another key or bucket fails authentication.
//
// The package is a leaf: it imports only the standard library (Spec 123,
// FR-006) and must stay that way so internal/storage can depend on it.
package tokenseal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	keyLen    = 32
	nonceLen  = 12
	kidHexLen = 16

	prefixV1 = "enc:v1:"

	infoRecordKey = "mcpproxy oauth_tokens record key v1"
	infoKeyID     = "mcpproxy oauth_tokens key id v1"
	aadDomain     = "mcpproxy-oauth-token"
	aadVersion    = "v1"
)

// ErrInvalidKey is the only error ParseKey returns. Its text is fixed so that
// no input, path or reference can leak through it.
var ErrInvalidKey = errors.New("is not a base64-encoded 32-byte key")

// Key is a configured sealing key. It has no exported field and never prints
// key material. A nil *Key means "no key configured".
type Key struct {
	aead cipher.AEAD
	kid  string
}

// String and GoString keep %v / %+v / %#v / %s from printing anything but the
// key id.
func (k *Key) String() string {
	if k == nil {
		return "tokenseal.Key(nil)"
	}
	return "tokenseal.Key(" + k.kid + ")"
}

// GoString implements fmt.GoStringer.
func (k *Key) GoString() string { return k.String() }

// derive returns the AES-256 record key and the key id for the input keying
// material (HKDF-SHA256, no salt, distinct versioned info strings).
func derive(ikm []byte) (recordKey []byte, kid string) {
	rk, err := hkdf.Key(sha256.New, ikm, nil, infoRecordKey, keyLen)
	if err != nil {
		panic("tokenseal: hkdf: " + err.Error()) // cannot happen for these lengths
	}
	id, err := hkdf.Key(sha256.New, ikm, nil, infoKeyID, kidHexLen/2)
	if err != nil {
		panic("tokenseal: hkdf: " + err.Error())
	}
	return rk, hex.EncodeToString(id)
}

// ParseKey parses a standard-alphabet base64 string (padding optional,
// surrounding whitespace ignored) that decodes to exactly 32 bytes.
func ParseKey(b64 string) (*Key, error) {
	s := strings.TrimSpace(b64)
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil || len(raw) != keyLen {
		return nil, ErrInvalidKey
	}
	rk, kid := derive(raw)
	blk, err := aes.NewCipher(rk)
	if err != nil {
		return nil, ErrInvalidKey
	}
	g, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, ErrInvalidKey
	}
	return &Key{aead: g, kid: kid}, nil
}

// ID returns the 16-hex-character key id, or "" for a nil key.
func (k *Key) ID() string {
	if k == nil {
		return ""
	}
	return k.kid
}

func aadFor(kid, bucket, bucketKey string) []byte {
	return []byte(aadDomain + "\x00" + aadVersion + "\x00" + kid + "\x00" + bucket + "\x00" + bucketKey)
}

// Seal returns the enc:v1 envelope for plaintext. A nil key is an error: a
// caller that wants plaintext must not call Seal.
func (k *Key) Seal(bucket, bucketKey string, plaintext []byte) ([]byte, error) {
	if k == nil || k.aead == nil {
		return nil, errors.New("tokenseal: no key configured")
	}
	nonce := make([]byte, nonceLen, nonceLen+len(plaintext)+k.aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("tokenseal: nonce: %w", err)
	}
	sealed := k.aead.Seal(nonce, nonce, plaintext, aadFor(k.kid, bucket, bucketKey))
	out := make([]byte, 0, len(prefixV1)+kidHexLen+1+base64.StdEncoding.EncodedLen(len(sealed)))
	out = append(out, prefixV1...)
	out = append(out, k.kid...)
	out = append(out, ':')
	out = base64.StdEncoding.AppendEncode(out, sealed)
	return out, nil
}

// Open authenticates and decrypts an enc:v1 envelope. A nil key is allowed
// and yields ReasonNoKey for a well-formed envelope. Every failure is an
// *OpenError. Open does not accept plaintext values: callers Classify first.
func (k *Key) Open(bucket, bucketKey string, value []byte) ([]byte, error) {
	shape, rowKID := Classify(value)
	switch shape {
	case ShapeUnsupported:
		return nil, &OpenError{Reason: ReasonUnsupportedVersion, KeyKID: k.ID()}
	case ShapeSealedV1:
	default:
		return nil, &OpenError{Reason: ReasonMalformed, KeyKID: k.ID()}
	}
	if k == nil || k.aead == nil {
		return nil, &OpenError{Reason: ReasonNoKey, RowKID: rowKID}
	}
	if rowKID != k.kid {
		return nil, &OpenError{Reason: ReasonKeyMismatch, RowKID: rowKID, KeyKID: k.kid}
	}
	raw, err := base64.StdEncoding.DecodeString(string(value[len(prefixV1)+kidHexLen+1:]))
	if err != nil || len(raw) < nonceLen+k.aead.Overhead() {
		return nil, &OpenError{Reason: ReasonMalformed, RowKID: rowKID, KeyKID: k.kid}
	}
	pt, err := k.aead.Open(nil, raw[:nonceLen], raw[nonceLen:], aadFor(k.kid, bucket, bucketKey))
	if err != nil {
		return nil, &OpenError{Reason: ReasonAuthFailed, RowKID: rowKID, KeyKID: k.kid}
	}
	return pt, nil
}

// Shape is the outer form of a stored value.
type Shape int

// Value shapes returned by Classify.
const (
	ShapeMalformed   Shape = iota // empty, unknown prefix or damaged enc:v1 header
	ShapePlaintext                // first byte '{' (a JSON object)
	ShapeSealedV1                 // enc:v1:<kid>:<payload>
	ShapeUnsupported              // enc:<other version>:...
)

func (s Shape) String() string {
	switch s {
	case ShapePlaintext:
		return "plaintext"
	case ShapeSealedV1:
		return "sealed"
	case ShapeUnsupported:
		return "unsupported"
	default:
		return "malformed"
	}
}

// Classify reports the shape of a stored value and, for ShapeSealedV1, the
// key id in its prefix. It does not decode the payload.
func Classify(value []byte) (Shape, string) {
	if len(value) == 0 {
		return ShapeMalformed, ""
	}
	if value[0] == '{' {
		return ShapePlaintext, ""
	}
	s := string(value)
	if !strings.HasPrefix(s, "enc:") {
		return ShapeMalformed, ""
	}
	if strings.HasPrefix(s, prefixV1) {
		rest := s[len(prefixV1):]
		if len(rest) < kidHexLen+2 || rest[kidHexLen] != ':' {
			return ShapeMalformed, ""
		}
		kid := rest[:kidHexLen]
		for i := 0; i < len(kid); i++ {
			c := kid[i]
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return ShapeMalformed, ""
			}
		}
		return ShapeSealedV1, kid
	}
	// enc:v<digits>: with a version other than v1.
	rest := s[len("enc:"):]
	if len(rest) >= 2 && rest[0] == 'v' {
		i := 1
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i > 1 && i < len(rest) && rest[i] == ':' {
			return ShapeUnsupported, ""
		}
	}
	return ShapeMalformed, ""
}

// Reason says why a value could not be opened.
type Reason string

// Open failure reasons.
const (
	ReasonNoKey              Reason = "no_key"
	ReasonKeyMismatch        Reason = "key_mismatch"
	ReasonAuthFailed         Reason = "auth_failed"
	ReasonUnsupportedVersion Reason = "unsupported_version"
	ReasonMalformed          Reason = "malformed"
)

// OpenError is the error Open returns. It carries key ids only, never key
// material or payload bytes.
type OpenError struct {
	Reason Reason
	RowKID string // kid found in the stored value's prefix ("" if none)
	KeyKID string // kid of the configured key ("" if none)
}

func (e *OpenError) Error() string {
	msg := "oauth token record cannot be opened: " + string(e.Reason)
	if e.RowKID != "" {
		msg += " (row key id " + e.RowKID
		if e.KeyKID != "" {
			msg += ", configured key id " + e.KeyKID
		}
		msg += ")"
	} else if e.KeyKID != "" {
		msg += " (configured key id " + e.KeyKID + ")"
	}
	return msg
}
