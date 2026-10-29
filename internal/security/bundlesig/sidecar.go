// Package bundlesig implements the detached-signature sidecar format and the
// Ed25519 verification rules of Spec 101 (contracts/sidecar-format.md).
//
// It is a pure leaf package: no file I/O, no globals, stdlib only, and it must
// not import internal/security/scanner. The scanner decides what to do with a
// verification outcome (which authority is active, rollback, activation); this
// package only answers "is this sidecar well-formed" and "did this key sign
// exactly these bytes", and does so with a stable machine-readable code for
// every refusal so two implementations can be shown to reject identically.
package bundlesig

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Rejection codes, one per contract row 1..13. They are part of the wire
// contract (surfaced as LastRejection reasons) and must not be renamed.
const (
	CodeSidecarTooLarge             = "sidecar_too_large"             // row 1
	CodeSidecarMalformed            = "sidecar_malformed"             // row 2
	CodeSidecarMissingField         = "sidecar_missing_field"         // row 3
	CodeSidecarUnknownField         = "sidecar_unknown_field"         // row 4
	CodeSidecarDuplicateField       = "sidecar_duplicate_field"       // row 5
	CodeSidecarType                 = "sidecar_type"                  // row 6
	CodeSidecarVersionUnsupported   = "sidecar_version_unsupported"   // row 7
	CodeSidecarAlgorithmUnsupported = "sidecar_algorithm_unsupported" // row 8
	CodeSidecarKeyIDMalformed       = "sidecar_key_id_malformed"      // row 9
	CodeSidecarSignatureMalformed   = "sidecar_signature_malformed"   // row 10
	CodeKeyNotTrusted               = "key_not_trusted"               // row 11
	CodeSignatureInvalid            = "signature_invalid"             // row 12
	CodeKeyIDMismatch               = "key_id_mismatch"               // row 13
)

// MaxSidecarBytes is the FR-011 cap on a sidecar. It is checked before any
// parsing because the sidecar is read before anything is authenticated.
const MaxSidecarBytes = 4096

const (
	sidecarVersion   = "1"
	sidecarAlgorithm = "ed25519"
	// signatureB64Len is the canonical padded base64 length of a 64-byte
	// Ed25519 signature.
	signatureB64Len = 88
)

// Error is a contract rejection carrying its stable reason code. Detail is
// human-oriented context and never part of the contract.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

// CodeOf returns the contract reason code carried by err (looking through
// wrapping), or "" when err is nil or not a bundlesig rejection.
func CodeOf(err error) string {
	var be *Error
	if errors.As(err, &be) {
		return be.Code
	}
	return ""
}

func reject(code, format string, args ...any) error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Sidecar is a parsed, grammar-valid sidecar. Parsing proves only that the
// sidecar is well-formed; nothing here is authenticated until
// TrustSet.Verify succeeds.
type Sidecar struct {
	Version   string
	Algorithm string
	KeyID     string
	Signature []byte
}

// The four required keys, in canonical order.
var sidecarKeys = [...]string{"sidecar_version", "algorithm", "key_id", "signature"}

// ParseSidecar parses sidecar bytes under the contract's total grammar,
// evaluating rows 1..10 in numerical order; the first matching row wins so
// overlapping conditions (e.g. {"foo":1} is both missing-a-field and
// has-an-unknown-key) always report the same reason.
//
// JSON whitespace around the object is tolerated (a trailing newline is
// normal); any other trailing byte is sidecar_malformed.
func ParseSidecar(b []byte) (*Sidecar, error) {
	// Row 1, before parsing anything.
	if len(b) > MaxSidecarBytes {
		return nil, reject(CodeSidecarTooLarge, "%d bytes exceeds %d", len(b), MaxSidecarBytes)
	}

	// Row 2. json.Valid rejects trailing non-whitespace, a BOM and truncation;
	// invalid UTF-8 is checked separately because encoding/json would silently
	// substitute U+FFFD rather than refuse it.
	if !utf8.Valid(b) || !json.Valid(b) {
		return nil, reject(CodeSidecarMalformed, "not a single valid JSON value")
	}
	if t := bytes.TrimLeft(b, " \t\r\n"); len(t) == 0 || t[0] != '{' {
		return nil, reject(CodeSidecarMalformed, "not a JSON object")
	}

	fields, err := scanObject(b)
	if err != nil {
		return nil, err
	}

	// Row 3.
	for _, k := range sidecarKeys {
		if len(fields[k]) == 0 {
			return nil, reject(CodeSidecarMissingField, "missing %q", k)
		}
	}
	// Row 4. Keys are compared exactly: encoding/json's case-insensitive
	// struct matching would let "Algorithm" through.
	for k := range fields {
		if !isSidecarKey(k) {
			return nil, reject(CodeSidecarUnknownField, "unknown key %q", k)
		}
	}
	// Row 5. encoding/json would silently keep the last duplicate.
	for _, k := range sidecarKeys {
		if len(fields[k]) > 1 {
			return nil, reject(CodeSidecarDuplicateField, "key %q appears %d times", k, len(fields[k]))
		}
	}
	// Row 6.
	var vals [len(sidecarKeys)]string
	for i, k := range sidecarKeys {
		raw := fields[k][0]
		if len(raw) == 0 || raw[0] != '"' {
			return nil, reject(CodeSidecarType, "%q must be a JSON string", k)
		}
		if err := json.Unmarshal(raw, &vals[i]); err != nil {
			return nil, reject(CodeSidecarType, "%q must be a JSON string", k)
		}
	}
	version, algorithm, keyID, sigB64 := vals[0], vals[1], vals[2], vals[3]

	// Rows 7..9.
	if version != sidecarVersion {
		return nil, reject(CodeSidecarVersionUnsupported, "sidecar_version %q", version)
	}
	// An unknown algorithm is a hard rejection, never a fallback or a skip.
	if algorithm != sidecarAlgorithm {
		return nil, reject(CodeSidecarAlgorithmUnsupported, "algorithm %q", algorithm)
	}
	if !isKeyID(keyID) {
		return nil, reject(CodeSidecarKeyIDMalformed, "key_id must be 64 lowercase hex characters")
	}

	// Row 10.
	sig, err := decodeSignature(sigB64)
	if err != nil {
		return nil, err
	}
	return &Sidecar{Version: version, Algorithm: algorithm, KeyID: keyID, Signature: sig}, nil
}

// scanObject walks the top-level object token by token and returns every raw
// value per key, preserving duplicates, which json.Unmarshal into a map or
// struct cannot do. The input has already passed json.Valid and starts with
// '{', so a token error here is unexpected and is reported as malformed.
func scanObject(b []byte) (map[string][]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, reject(CodeSidecarMalformed, "not a JSON object")
	}
	fields := make(map[string][]json.RawMessage)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, reject(CodeSidecarMalformed, "%v", err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, reject(CodeSidecarMalformed, "non-string object key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, reject(CodeSidecarMalformed, "%v", err)
		}
		fields[key] = append(fields[key], raw)
	}
	return fields, nil
}

func isSidecarKey(k string) bool {
	for _, want := range sidecarKeys {
		if k == want {
			return true
		}
	}
	return false
}

// isKeyID reports whether s is exactly 64 lowercase hex characters.
func isKeyID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// decodeSignature enforces contract row 10. base64.StdEncoding is lenient in
// two ways that break "two implementations reject identically": it ignores
// embedded newlines and it accepts non-canonical trailing bits. So the value
// must be exactly 88 chars, decode under Strict(), re-encode to the same
// bytes, and decode to exactly 64 bytes. The length rule is what catches an
// embedded newline that Strict() would skip.
func decodeSignature(s string) ([]byte, error) {
	if len(s) != signatureB64Len {
		return nil, reject(CodeSidecarSignatureMalformed, "signature is %d chars, want %d", len(s), signatureB64Len)
	}
	sig, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, reject(CodeSidecarSignatureMalformed, "signature is not strict standard base64")
	}
	if base64.StdEncoding.EncodeToString(sig) != s {
		return nil, reject(CodeSidecarSignatureMalformed, "signature is not canonical base64")
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, reject(CodeSidecarSignatureMalformed, "signature decodes to %d bytes, want %d", len(sig), ed25519.SignatureSize)
	}
	return sig, nil
}
