package checks

import (
	"math/rand"
	"strings"
	"testing"
	"unicode"
)

// refTrailingLetterRun / refLeadingLetterRun are the original whole-prefix and
// whole-suffix helpers, kept as an oracle: letterRuns must give the same
// answer wherever it matters (both capped at squashedChunkLen+1).
func refTrailingLetterRun(s string) int {
	rs := []rune(strings.TrimRightFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }))
	n := 0
	for i := len(rs) - 1; i >= 0 && unicode.IsLetter(rs[i]); i-- {
		n++
	}
	return n
}

func refLeadingLetterRun(s string) int {
	rs := []rune(strings.TrimLeftFunc(s, func(r rune) bool { return !unicode.IsLetter(r) }))
	if len(rs) == 0 {
		return squashedChunkLen + 1
	}
	n := 0
	for n < len(rs) && unicode.IsLetter(rs[n]) {
		n++
	}
	return n
}

func capRun(n int) int { return min(n, squashedChunkLen+1) }

func checkLetterRuns(t *testing.T, s string) {
	t.Helper()
	r := newLetterRuns(s)
	for i := 0; i <= len(s); i++ {
		if i < len(s) && !isRuneStart(s[i]) {
			continue
		}
		if got, want := r.trailing(i), capRun(refTrailingLetterRun(s[:i])); got != want {
			t.Fatalf("trailing(%q, %d) = %d, want %d", s, i, got, want)
		}
		if got, want := r.leading(i), capRun(refLeadingLetterRun(s[i:])); got != want {
			t.Fatalf("leading(%q, %d) = %d, want %d", s, i, got, want)
		}
	}
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func TestLetterRuns_MatchesReferenceHelpers(t *testing.T) {
	for _, s := range []string{
		"", ".", "a", "ab.", "sys. tem.", " pro. mpt", "system. prompt", "re. ve. al",
		"\n\n\n", "alpha\n\nbeta", "сис. тем промпт", "日本語。テスト", "ab12cd", "x — y",
	} {
		checkLetterRuns(t, s)
	}
	alphabet := []rune("abcdefgh .;!\n\"{}12éЖ語")
	rng := rand.New(rand.NewSource(1))
	for n := 0; n < 3000; n++ {
		rs := make([]rune, rng.Intn(24))
		for i := range rs {
			rs[i] = alphabet[rng.Intn(len(alphabet))]
		}
		checkLetterRuns(t, string(rs))
	}
}
