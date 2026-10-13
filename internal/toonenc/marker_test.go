package toonenc

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestMarkerExactBytes pins the marker to the contract in
// specs/084-toon-output/contracts/marker-format.md, byte-for-byte.
func TestMarkerExactBytes(t *testing.T) {
	const want = "[mcpproxy:toon/v1] TOON-encoded JSON (toon-format.org); decode to JSON before reuse - tool arguments must still be sent as JSON."
	if Marker != want {
		t.Fatalf("Marker does not match the contract bytes:\n got: %q\nwant: %q", Marker, want)
	}
}

// TestMarkerStrictASCII guards against a multi-byte rune (e.g. an em dash)
// creeping into the marker (contract: strict ASCII).
func TestMarkerStrictASCII(t *testing.T) {
	if utf8.RuneCountInString(Marker) != len(Marker) {
		t.Fatalf("Marker must be pure ASCII: rune count %d != byte length %d",
			utf8.RuneCountInString(Marker), len(Marker))
	}
	for i := 0; i < len(Marker); i++ {
		if Marker[i] > 0x7F {
			t.Fatalf("Marker contains non-ASCII byte 0x%x at index %d", Marker[i], i)
		}
	}
}

func TestMarkerSingleLineNoTrailingWhitespace(t *testing.T) {
	if strings.ContainsAny(Marker, "\n\r") {
		t.Fatalf("Marker must be a single line, got %q", Marker)
	}
	if strings.TrimRight(Marker, " \t") != Marker {
		t.Fatalf("Marker must have no trailing whitespace, got %q", Marker)
	}
}

// TestAssembleEmission asserts the emission assembly rule:
// exactly Marker + "\n" + body.
func TestAssembleEmission(t *testing.T) {
	body := "[2]{a}:\n  1\n  2"
	got := AssembleEmission(body)
	want := Marker + "\n" + body
	if got != want {
		t.Fatalf("AssembleEmission(%q) = %q, want %q", body, got, want)
	}
}
