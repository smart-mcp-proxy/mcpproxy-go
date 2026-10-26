package logs

import (
	"strings"
	"testing"
)

func TestSanitizer_AnthropicKeyWithUnderscore(t *testing.T) {
	s := newTestSanitizer()
	for _, key := range []string{
		"sk-ant-api03-abcdefghij_klmnopqrstuvwxyz0123456789ABCDEFGHIJ-abcdefghijklmnopqrstuvwx_yzAA",
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJ_abcdefghijklmnopqrstuvwx-yzAA",
		"sk-ant-admin01-Ab_Cd-EfGhIjKlMnOpQrStUvWxYz0123456789AA",
	} {
		out := s.sanitizeString("using key " + key + " for upstream")
		if strings.Contains(out, key[16:]) {
			t.Errorf("anthropic key leaked: %q", out)
		}
		if !strings.Contains(out, key[:10]+"***") {
			t.Errorf("anthropic key not masked by anthropic_key: %q", out)
		}
	}
}
