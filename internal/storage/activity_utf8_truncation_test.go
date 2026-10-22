package storage

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Round 3 of the UTF-8 truncation sweep (#1647, #1664, #1665): the activity
// log's own per-record cut was still a raw byte slice, so a response whose
// cut offset lands inside a multi-byte rune was persisted — and served over
// /api/v1/activities and the CSV export — as invalid UTF-8, which JSON
// encoding rewrites to U+FFFD.
//
// Every offset inside a 3-byte rune is exercised, because only two of the
// three are mid-rune: a single fixed cap would pass by luck.
func TestTruncateResponseKeepsRuneBoundary(t *testing.T) {
	body := strings.Repeat("語", 200) // 3 bytes per rune

	for _, maxSize := range []int{100, 101, 102, 103, 104, 105} {
		got, truncated := truncateResponse(body, maxSize)
		if !truncated {
			t.Fatalf("maxSize=%d: expected truncation of a %d-byte body", maxSize, len(body))
		}
		kept := strings.TrimSuffix(got, "...[truncated]")
		if !utf8.ValidString(kept) {
			t.Errorf("maxSize=%d: stored response ends in a partial rune: % x",
				maxSize, []byte(kept)[max(0, len(kept)-4):])
		}
		if len(kept) > maxSize {
			t.Errorf("maxSize=%d: kept %d bytes, which is over the cap", maxSize, len(kept))
		}
	}
}

// ASCII behaviour must not move: the cap is still applied exactly.
func TestTruncateResponseASCIIUnchanged(t *testing.T) {
	got, truncated := truncateResponse(strings.Repeat("x", 500), 100)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if want := strings.Repeat("x", 100) + "...[truncated]"; got != want {
		t.Errorf("ASCII cut moved:\n got %q\nwant %q", got, want)
	}
}
