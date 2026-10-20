package supervisor

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateLastError(t *testing.T) {
	short := "connection refused"
	if got := truncateLastError(short); got != short {
		t.Fatalf("short error changed: %q", got)
	}

	// 501 bytes of 3-byte runes: a raw 500-byte cut lands inside a rune.
	got := truncateLastError(strings.Repeat("世", 167))
	if !utf8.ValidString(got) {
		t.Fatalf("truncated error is invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "... (truncated)") {
		t.Fatalf("missing truncation marker: %q", got)
	}
	if body := strings.TrimSuffix(got, "... (truncated)"); len(body) > maxLastErrorLen {
		t.Fatalf("body is %d bytes, want <= %d", len(body), maxLastErrorLen)
	}
}
