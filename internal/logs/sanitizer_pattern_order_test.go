package logs

import (
	"strings"
	"testing"
)

// A key-specific pattern that runs before bearer_token inserts "***", which is
// outside bearer's character class, so bearer stopped at the mask and the rest
// of the credential was logged in clear.
func TestSanitizer_BearerWithKnownKeyPrefixMasksWholeCredential(t *testing.T) {
	s := newTestSanitizer()
	const extra = "EXTRA_SECRET"
	for _, token := range []string{
		"sk-AbCdEf1234567890GhIjKlMnOpQrStUvWxYz0123456789." + extra,
		"sk-proj-AbCdEf1234567890_GhIjKl-MnOpQrStUvWxYz0123456789." + extra,
		"ghp_1234567890abcdefghijABCDEFGHIJ123456." + extra,
		"sk-ant-api03-" + strings.Repeat("aB3dE", 8) + "." + extra,
	} {
		out := s.sanitizeString("Authorization: Bearer " + token)
		if strings.Contains(out, extra) {
			t.Errorf("bearer credential suffix leaked:\n  in:  %s\n  out: %s", token, out)
		}
		if !strings.Contains(out, "Bearer "+token[:4]+"***") {
			t.Errorf("bearer mask format changed: %q", out)
		}
	}
}

// A key-shaped run inside a JWT segment (after a '-', which is a \b boundary)
// must not be masked before jwt runs: the inserted "***" would stop the jwt
// regex and leave the payload and signature in clear.
func TestSanitizer_JWTContainingKeyShapedRunIsMaskedAsJWT(t *testing.T) {
	s := newTestSanitizer()
	const (
		payload   = "eyJzdWIiOiIxMjM0NTY3ODkw-sk-AbCdEf1234567890GhIjKlMnOp"
		signature = "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
		jwt       = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." + payload + "." + signature
	)
	out := s.sanitizeString("got " + jwt)
	if strings.Contains(out, "eyJzdWIiOiIxMjM0NTY3ODkw") || strings.Contains(out, signature) {
		t.Errorf("JWT payload/signature leaked: %q", out)
	}
}

// An unsigned JWT (alg: none) has an empty third segment; its payload must
// still be masked.
func TestSanitizer_UnsignedJWTPayloadMasked(t *testing.T) {
	s := newTestSanitizer()
	const (
		header  = "eyJhbGciOiJub25lIn0"
		payload = "eyJzdWIiOiIxIn0"
		jwt     = header + "." + payload + "."
	)
	for _, in := range []string{
		"jwt " + jwt,
		"jwt " + jwt + " trailing",
		`{"id_token":"` + jwt + `"}`,
		"token=" + jwt,
	} {
		out := s.sanitizeString(in)
		if strings.Contains(out, payload) {
			t.Errorf("unsigned JWT payload leaked:\n  in:  %s\n  out: %s", in, out)
		}
		if !strings.Contains(out, header+".***.***") {
			t.Errorf("unsigned JWT mask format: %q", out)
		}
	}
}
