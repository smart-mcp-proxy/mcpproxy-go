package logs

import (
	"strings"
	"testing"
)

func TestSanitizer_OpenAIProjectAndServiceAccountKeys(t *testing.T) {
	s := newTestSanitizer()
	for _, key := range []string{
		"sk-proj-AbCdEf1234567890_GhIjKl-MnOpQrStUvWxYz0123456789",
		"sk-svcacct-AbCdEf1234567890_GhIjKl-MnOpQrStUvWxYz0123456789",
		"sk-admin-AbCdEf1234567890GhIjKlMnOpQrStUvWxYz0123456789",
		"sk-AbCdEf1234567890GhIjKlMnOpQrStUvWxYz0123456789", // legacy
	} {
		out := s.sanitizeString("using key " + key + " for upstream")
		if strings.Contains(out, key) || strings.Contains(out, key[12:]) {
			t.Errorf("OpenAI key leaked: %q", out)
		}
	}

	// sk-ant- keys keep their own, longer-prefix mask.
	ant := "sk-ant-api03-" + strings.Repeat("aB3dE", 8)
	if out := s.sanitizeString("key " + ant); !strings.Contains(out, "sk-ant-api***") {
		t.Errorf("anthropic key no longer masked by anthropic_key: %q", out)
	}
}
