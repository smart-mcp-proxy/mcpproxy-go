package truncate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Truncation limits are byte offsets, so cutting multi-byte text must not
// leave a partial rune at the end of the retained prefix.
func TestSimpleTruncate_KeepsValidUTF8(t *testing.T) {
	content := strings.Repeat("日本語", 400) // 3 bytes per rune
	for limit := 250; limit <= 260; limit++ {
		out := NewTruncator(limit).SimpleTruncate(content)
		if !utf8.ValidString(out) {
			t.Errorf("limit=%d: truncated output is not valid UTF-8", limit)
		}
		if len(out) > limit {
			t.Errorf("limit=%d: output is %d bytes, over the limit", limit, len(out))
		}
	}
}

func TestTruncateWithCache_KeepsValidUTF8(t *testing.T) {
	records := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		records = append(records, `{"name":"日本語日本語日本語日本語日本語日本語"}`)
	}
	content := `{"items":[` + strings.Join(records, ",") + `]}`
	for limit := 600; limit <= 610; limit++ {
		result := NewTruncator(limit).Truncate(content, "tool", map[string]interface{}{"q": "x"})
		if !result.CacheAvailable {
			t.Fatalf("limit=%d: expected the cache path to be used", limit)
		}
		if !utf8.ValidString(result.TruncatedContent) {
			t.Errorf("limit=%d: truncated output is not valid UTF-8", limit)
		}
		if len(result.TruncatedContent) > limit {
			t.Errorf("limit=%d: output is %d bytes, over the limit", limit, len(result.TruncatedContent))
		}
	}
}
