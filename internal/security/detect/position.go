package detect

import "strings"

// Position classifies where a phrase match sits in the surrounding text, so the
// soft checks can tell a genuine embedded instruction ("ignore previous
// instructions") apart from a description that merely talks about such phrases
// ("detects prompts such as 'ignore previous instructions'"). This is the core
// false-positive control for legitimate security tooling (FR-009, US2).
type Position int

const (
	// PositionInstruction is an imperative/instruction-position match; full
	// confidence is kept.
	PositionInstruction Position = iota
	// PositionExample is an example/illustration-position match (after a cue
	// like "such as"/"e.g.", inside quotes, or in a "detects …" list);
	// confidence is discounted.
	PositionExample
)

// exampleDiscount is the confidence multiplier applied to example-position
// matches. Low enough that a lone example-position match cannot, by itself,
// push a tool over a soft threshold.
const exampleDiscount = 0.25

// Discount returns the confidence multiplier for a position: 1.0 for
// instruction-position, exampleDiscount for example-position.
func (p Position) Discount() float64 {
	if p == PositionExample {
		return exampleDiscount
	}
	return 1.0
}

// positionWindow is how many bytes before the match we inspect for cue phrases
// and quoting.
const positionWindow = 80

// exampleCues are substrings that, when they appear shortly before a match,
// indicate the match is being quoted or illustrated rather than instructing.
var exampleCues = []string{
	"such as",
	"for example",
	"e.g",
	"i.e",
	"example",
	"detects",
	"detect ",
	"flags",
	"flag ",
	"phrase",
	"pattern",
	"like ",
	"says ",
	"say ",
}

// ClassifyPosition decides whether the match starting at byte offset matchStart
// in text is in instruction- or example-position. text may be raw or
// normalized; matching is case-insensitive on the preceding window.
func ClassifyPosition(text string, matchStart int) Position {
	if matchStart <= 0 {
		return PositionInstruction
	}
	start := matchStart - positionWindow
	if start < 0 {
		start = 0
	}
	window := strings.ToLower(text[start:matchStart])

	for _, cue := range exampleCues {
		if strings.Contains(window, cue) {
			return PositionExample
		}
	}

	// Inside an unclosed quote → example/illustration position.
	if oddQuoteCount(window) {
		return PositionExample
	}

	return PositionInstruction
}

// oddQuoteCount reports whether the window contains an odd number of quote
// characters, i.e. the match lies inside an open quote.
func oddQuoteCount(window string) bool {
	count := 0
	for _, r := range window {
		switch r {
		case '"', '\'', '`', '“', '”', '‘', '’':
			count++
		}
	}
	return count%2 == 1
}
