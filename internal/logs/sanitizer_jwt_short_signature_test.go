package logs

import (
	"strings"
	"testing"
)

// The jwt regex accepts a 1-3 character signature segment; masking it must
// not slice out of range (a panic inside the zap core takes the caller down).
func TestSanitizer_JWTShortSignatureDoesNotPanic(t *testing.T) {
	s := newTestSanitizer()
	const payload = "eyJzdWIiOiIxIn0"
	for _, sig := range []string{"x", "ab", "abc"} {
		in := "jwt eyJhbGciOiJub25lIn0." + payload + "." + sig
		var out string
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("sanitizeString(%q) panicked: %v", in, r)
				}
			}()
			out = s.sanitizeString(in)
		}()
		if strings.Contains(out, payload) {
			t.Errorf("payload leaked: %q", out)
		}
	}
}
