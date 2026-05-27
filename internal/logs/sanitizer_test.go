package logs

import (
	"strings"
	"sync"
	"testing"
)

// newTestSanitizer builds a SecretSanitizer with the default patterns registered
// but no wrapped core, suitable for exercising sanitizeString directly.
func newTestSanitizer() *SecretSanitizer {
	s := &SecretSanitizer{resolvedCache: &sync.Map{}}
	s.registerDefaultPatterns()
	return s
}

func TestSanitizer_GitHubTokens(t *testing.T) {
	s := newTestSanitizer()

	tests := []struct {
		name  string
		token string
	}{
		{"classic ghp_ (40 chars)", "ghp_1234567890abcdefghijABCDEFGHIJ123456"},
		{"installation ghs_ (40 chars)", "ghs_1234567890abcdefghijABCDEFGHIJ123456"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := s.sanitizeString("token=" + tt.token)
			if strings.Contains(out, tt.token) {
				t.Fatalf("token leaked unmasked: %q", out)
			}
		})
	}
}

// TestSanitizer_LongStatelessGitHubToken verifies the new ~520-char stateless
// GitHub token format is masked. The previous {36,255} upper bound left these
// tokens unmasked because the alphanumeric run had no \b boundary within range.
func TestSanitizer_LongStatelessGitHubToken(t *testing.T) {
	s := newTestSanitizer()

	const tail = 516 // total length 520 incl. "ghs_" prefix
	token := "ghs_" + strings.Repeat("aB3", (tail/3)+1)[:tail]

	out := s.sanitizeString("Authorization context token=" + token)
	if strings.Contains(out, token) {
		t.Fatalf("long stateless token leaked unmasked (len %d)", len(token))
	}
}
