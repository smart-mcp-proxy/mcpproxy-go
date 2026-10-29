package logs

import (
	"strings"
	"testing"
)

// A JWT after '=' / ':' or inside quotes (an id_token in a JSON body, a
// token=... query) must have its payload and signature masked, not just the
// header.
func TestSanitizer_JWTInKeyValueAndQuotedContexts(t *testing.T) {
	s := newTestSanitizer()
	const (
		payload   = "eyJzdWIiOiIxMjM0NTY3ODkwIn0"
		signature = "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
		jwt       = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." + payload + "." + signature
	)
	for _, in := range []string{
		`{"id_token":"` + jwt + `"}`,
		"token=" + jwt,
		"id_token: " + jwt,
		"got " + jwt,
	} {
		out := s.sanitizeString(in)
		if strings.Contains(out, payload) || strings.Contains(out, signature) {
			t.Errorf("JWT payload/signature leaked:\n  in:  %s\n  out: %s", in, out)
		}
	}
}
