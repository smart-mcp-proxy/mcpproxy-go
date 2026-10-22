package main

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// Finishing the sweep of #1647 / #1664 / #1665 in cmd/mcpproxy. Every CLI
// table cut was a raw byte slice, so a non-ASCII registry/tool description,
// catalog title or server name longer than its column ended in a partial
// rune on the terminal.
//
// The 1-byte padding shifts the cut through all three offsets inside a 3-byte
// rune; only two of the three are mid-rune, so a single fixed width would
// pass by luck.
func TestTruncateCellKeepsRuneBoundary(t *testing.T) {
	for _, maxLen := range []int{10, 24, 30, 40, 45, 60} {
		for pad := 0; pad < 3; pad++ {
			cell := strings.Repeat("a", pad) + strings.Repeat("語", 100)

			got := truncateCell(cell, maxLen)
			if len(got) > maxLen {
				t.Errorf("maxLen=%d pad=%d: %d bytes, over the column width", maxLen, pad, len(got))
			}
			if !utf8.ValidString(got) {
				t.Errorf("maxLen=%d pad=%d: cell ends in a partial rune: % x", maxLen, pad, []byte(got))
			}
		}
	}
}

// Short cells and exact fits are returned verbatim, and ASCII truncation is
// byte-for-byte what the four removed helpers produced.
func TestTruncateCellASCIIUnchanged(t *testing.T) {
	cases := []struct {
		s      string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactlyten", 10, "exactlyten"},
		{strings.Repeat("x", 30), 10, strings.Repeat("x", 7) + "..."},
		{strings.Repeat("x", 100), 60, strings.Repeat("x", 57) + "..."},
		{strings.Repeat("x", 10), 3, "xxx"},
		{strings.Repeat("x", 10), 2, "xx"},
	}
	for _, c := range cases {
		if got := truncateCell(c.s, c.maxLen); got != c.want {
			t.Errorf("truncateCell(%d chars, %d) = %q, want %q", len(c.s), c.maxLen, got, c.want)
		}
	}
}

// joinInterfaceSlice keeps maxLen == 0 meaning "do not truncate", and takes
// the rune-safe cut for a positive width. allowed_servers holds server names,
// which the config only forbids from being empty or containing ':'.
func TestJoinInterfaceSliceTruncationIsRuneSafe(t *testing.T) {
	name := strings.Repeat("語", 20)
	m := map[string]interface{}{"allowed_servers": []interface{}{name, name}}

	if got := joinInterfaceSlice(m, "allowed_servers", 0); got != name+","+name {
		t.Errorf("maxLen 0 must not truncate: got %d bytes, want %d", len(got), len(name)*2+1)
	}

	got := joinInterfaceSlice(m, "allowed_servers", 23)
	if len(got) > 23 {
		t.Errorf("got %d bytes, over the 23-byte column", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("joined server names end in a partial rune: % x", []byte(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("a shortened value must stay marked: %q", got)
	}
	fmt.Fprint(nopWriter{}, got)
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
