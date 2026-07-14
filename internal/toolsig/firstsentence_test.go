package toolsig

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestFirstSentence covers the contract §6 terminator matrix (⟲#3): CJK
// terminators match unconditionally; ASCII terminators split only at a
// boundary (whitespace, EOF, or a closing quote/bracket); the no-terminator
// fallback is a rune-safe length cap; empty in, empty out.
func TestFirstSentence(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   \t\n ", ""},
		{"simple sentence", "Create a CDN for a Spaces bucket. Requires an origin.", "Create a CDN for a Spaces bucket."},
		{"single sentence, EOF boundary", "List all configured servers.", "List all configured servers."},
		{"exclamation", "Deploy now! Then verify.", "Deploy now!"},
		{"question", "Is it up? Check status.", "Is it up? Check status."[:len("Is it up?")]},
		// ASCII '.' splits ONLY at a boundary: 'e.g. text' splits after the
		// space-followed period (v1's simple boundary rule, no abbreviation
		// dictionary).
		{"e.g. splits at the space-followed period", "e.g. text follows here", "e.g."},
		// Decimals and versions never split mid-token.
		{"decimal does not split", "Returns pi as 3.14159 rounded", "Returns pi as 3.14159 rounded"},
		{"version does not split", "Supports v1.2 and newer releases", "Supports v1.2 and newer releases"},
		{"decimal then real terminator", "Returns 3.14 as a value. More detail.", "Returns 3.14 as a value."},
		// Terminator followed by a closing quote/bracket counts as a boundary.
		{"period before closing paren", "Runs the job (see docs.) elsewhere", "Runs the job (see docs."},
		{"period before closing quote", `He said "stop." Then left.`, `He said "stop.`},
		// CJK terminators match unconditionally (unspaced scripts, E6).
		{"CJK 。 unconditional", "検索クエリを実行します。結果はJSONで返されます。", "検索クエリを実行します。"},
		{"CJK ！ unconditional", "実行！次へ", "実行！"},
		{"CJK ？ unconditional", "実行？次へ", "実行？"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FirstSentence(tt.in); got != tt.want {
				t.Errorf("FirstSentence(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestFirstSentence_LengthCapFallback: no terminator ⇒ verbatim first 200
// runes (rune-boundary safe) with a trailing … only when truncation occurred.
func TestFirstSentence_LengthCapFallback(t *testing.T) {
	// Short, no terminator: returned whole, no ellipsis.
	short := "a description without any terminator at all"
	if got := FirstSentence(short); got != short {
		t.Errorf("short no-terminator: got %q, want verbatim input", got)
	}

	// Long multi-byte input, no terminator: exactly 200 runes + "…", never a
	// broken rune.
	long := strings.Repeat("é", 500)
	got := FirstSentence(long)
	if !utf8.ValidString(got) {
		t.Fatalf("length-cap output is not valid UTF-8")
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated output must end with …, got %q", got[len(got)-8:])
	}
	body := strings.TrimSuffix(got, "…")
	if n := utf8.RuneCountInString(body); n != 200 {
		t.Errorf("cap = %d runes, want 200", n)
	}
	if body != strings.Repeat("é", 200) {
		t.Errorf("capped prefix is not verbatim")
	}

	// Exactly at the cap: no ellipsis (no truncation occurred).
	exact := strings.Repeat("x", 200)
	if got := FirstSentence(exact); got != exact {
		t.Errorf("exact-cap input must be returned verbatim without ellipsis")
	}
}

// TestFirstSentence_Deterministic: repeated calls agree byte-for-byte.
func TestFirstSentence_Deterministic(t *testing.T) {
	inputs := []string{
		"Create a CDN. More.",
		"検索クエリを実行します。結果。",
		strings.Repeat("no terminator ", 40),
		"",
	}
	for _, in := range inputs {
		first := FirstSentence(in)
		for i := 0; i < 5; i++ {
			if got := FirstSentence(in); got != first {
				t.Fatalf("FirstSentence(%q) nondeterministic: %q vs %q", in, got, first)
			}
		}
	}
}
