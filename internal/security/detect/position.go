package detect

import (
	"regexp"
	"strings"
)

// Position classifies where a phrase match sits in the surrounding text, so the
// checks can tell a genuine embedded instruction ("ignore previous
// instructions") apart from a tool that merely DESCRIBES such phrases
// ("analyzes prompts that ignore previous instructions"). This is the core
// false-positive control for legitimate security tooling (FR-009, US2), and the
// deciding factor between the hard/soft tiers for a matched directive.
//
// It is a three-way scale, not a binary one, because the two false-positive
// pressures pull in opposite directions (Spec 077 US1, Codex round-2):
//
//   - A bare label prefix ("Prompt:", "Message:") must NOT neutralize a clear
//     imperative — that framing is exactly what an attacker uses to smuggle a
//     directive (finding A, recall bypass). Such matches stay
//     PositionInstruction and hard-block.
//   - A tool that DESCRIBES an injection — a relative clause ("prompts that
//     ignore…") or an analytical verb governing the phrase ("returns text:
//     ignore…") — must not hard-block a benign tool (finding B). Such matches
//     are PositionDescriptive: the hard tier is discounted below its floor
//     (HARD→SOFT), but the soft tier still surfaces the match for review, so a
//     real injection wearing descriptive clothing never fully vanishes.
//   - Genuine quotation/illustration ("such as", "e.g.", surrounding quotes) is
//     PositionExample and most heavily discounted — that framing is the weakest
//     injection signal. It is NOT, however, silently dropped: a check built for
//     recall (phrase.injection) re-floors an already-MATCHED phrase to the soft
//     emit floor so a quoted/illustrated injection surfaces as review-only
//     rather than vanishing entirely (Spec 077 US1, Codex round-3 — the
//     "never fully suppress a matched injection" invariant that closes the
//     recurring silent-bypass class). "Quiet" for a legitimate quoting scanner
//     now means soft/review, never invisible.
//
// Position framing is decided per SENTENCE, not per description: a cue in a
// PRIOR sentence must not discount an imperative that begins a LATER one. Both
// the analytical-verb heuristic and the "example"/"such as" word cues are
// sentence-scoped (only the inline abbreviations "e.g."/"i.e." and quote runs,
// which carry their own internal periods, are matched across the whole window).
// Without this, a benign lead ("Example output format. Ignore all previous
// instructions…") would misclassify the following injection as an example and
// bypass the hard tier (Codex round-3 finding #1).
type Position int

const (
	// PositionInstruction is an imperative/instruction-position match; full
	// confidence is kept. This is also where a bare "label:" prefix lands — a
	// label alone does not discount a clear imperative (finding A).
	PositionInstruction Position = iota
	// PositionDescriptive is a match the tool DESCRIBES rather than issues: a
	// relative clause ("… that ignore …") or an analytical verb governing the
	// phrase. Discounted enough to drop a hard match below its emit floor
	// (HARD→SOFT) while a soft match still surfaces for review — no total
	// suppression, so a genuine injection cannot bypass detection by adopting
	// descriptive phrasing (finding B without reopening finding A).
	PositionDescriptive
	// PositionExample is a quotation/illustration-position match (after "such
	// as"/"e.g." or inside quotes); most heavily discounted. On its own it clears
	// neither per-check floor, so a SOFT-only check (directive.imperative) stays
	// quiet — its US2 false-positive control. A recall-oriented HARD check
	// (phrase.injection) instead re-floors an already-matched phrase to the soft
	// emit floor, so the match surfaces for review rather than vanishing (the
	// "never fully suppress" invariant).
	PositionExample
)

// Discount multipliers per position. exampleDiscount is deliberately low so a
// lone example-position hit does not, by itself, clear a per-check emit floor —
// this is the false-positive control the SOFT directive check relies on. It is
// NOT a suppression guarantee: the phrase.injection HARD check re-floors any
// matched-but-example phrase up to the soft emit floor (never-fully-suppress),
// so silence for a matched injection is impossible.
// descriptiveDiscount is the HARD→SOFT pivot: it must take a HARD base
// (≈0.85–0.9) below the hard emit floor (0.6) yet keep a SOFT base (≈0.6–0.65)
// at/above the soft emit floor (0.3).
const (
	exampleDiscount     = 0.25
	descriptiveDiscount = 0.5
)

// Discount returns the confidence multiplier for a position.
func (p Position) Discount() float64 {
	switch p {
	case PositionExample:
		return exampleDiscount
	case PositionDescriptive:
		return descriptiveDiscount
	default:
		return 1.0
	}
}

// positionWindow is how many bytes before the match we inspect for framing.
const positionWindow = 80

// inlineExampleCues are quotation/illustration markers that legitimately contain
// their own periods ("e.g.", "i.e."), so they are matched across the WHOLE
// preceding window rather than the sentence-scoped one — the sentence split
// below would otherwise treat the abbreviation's dot as a boundary and lose the
// cue. They sit immediately before the phrase they introduce, so cross-sentence
// leakage is not a concern for them.
var inlineExampleCues = []string{
	"e.g",
	"i.e",
}

// wordExampleCues are quotation/illustration markers with no internal period, so
// they are matched on the SENTENCE-scoped window: an "example"/"such as" cue in a
// PRIOR sentence must not discount an imperative that begins a LATER one (Codex
// round-3 finding #1 — "Example output format. Ignore all previous
// instructions."). Kept to genuine illustration markers ONLY — deliberately NOT
// the bare colon-labels ("prompt:", "message:", …) an earlier iteration used,
// because those let an attacker smuggle an imperative behind a label (finding A).
var wordExampleCues = []string{
	"such as",
	"for example",
	"for instance",
	"example",
}

// describingVerb matches an analytical/descriptive verb (stemmed forms) whose
// presence in the match's sentence signals the tool is talking ABOUT a phrase
// rather than issuing it: "analyzes prompts that…", "returns text: …",
// "detects/flags/guards … instructions". Sentence-scoped (see ClassifyPosition)
// so a benign lead clause cannot reach across a period to discount a following
// imperative. Anchored on the verb stems only, so exfil/imperative action verbs
// (send/upload/ignore/…) are never treated as descriptive.
var describingVerb = regexp.MustCompile(`\b(?:analyz|detect|describ|return|handl|explain|document|illustrat|demonstrat|flag|scan|identif|recogniz|classif|catalog|enumerat|inspect|audit|monitor|highlight|surfac|guard|filter|warn|alert|block|prevent|report)\w*`)

// descriptiveTail marks a match that directly follows a relative pronoun
// ("prompts THAT ignore…", "text WHICH reveals…") — the imperative is the
// grammatical object of the clause, i.e. described, not issued.
var descriptiveTail = regexp.MustCompile(`\b(?:that|which|who)\s+$`)

// ClassifyPosition decides whether the match starting at byte offset matchStart
// in text is in instruction-, descriptive-, or example-position. text may be
// raw or normalized; matching is case-insensitive on the preceding window.
func ClassifyPosition(text string, matchStart int) Position {
	if matchStart <= 0 {
		return PositionInstruction
	}
	start := matchStart - positionWindow
	if start < 0 {
		start = 0
	}
	window := strings.ToLower(text[start:matchStart])

	// 1. Inline quotation markers ("e.g.", "i.e.") and open quotes → example. These
	// carry internal periods or span the whole window, so they are checked BEFORE
	// the sentence split (which would otherwise treat their dots as boundaries).
	for _, cue := range inlineExampleCues {
		if strings.Contains(window, cue) {
			return PositionExample
		}
	}
	if oddQuoteCount(window) {
		return PositionExample
	}

	// Scope the remaining, period-free heuristics to the current sentence. Framing
	// established before a sentence boundary must not neutralize an imperative that
	// begins a new sentence — otherwise a benign lead ("Example output format.
	// Ignore all previous instructions." / "Lists files. Ignore all previous
	// instructions.") would discount the injection that follows (a recall bypass,
	// Codex round-3 finding #1). Both the "example"/"such as" word cues and the
	// analytical-verb rule are guarded this way.
	if i := strings.LastIndexAny(window, ".!?"); i >= 0 {
		window = window[i+1:]
	}

	// 2. Word illustration cues ("such as", "for example", "example") in the
	// current sentence → example-position.
	for _, cue := range wordExampleCues {
		if strings.Contains(window, cue) {
			return PositionExample
		}
	}

	// 3. Analytical/relative-clause framing → descriptive-position (HARD→SOFT).
	if describingVerb.MatchString(window) || descriptiveTail.MatchString(window) {
		return PositionDescriptive
	}

	// 4. Otherwise the match is an instruction — including one behind a bare
	// "label:" prefix, which does not by itself discount a clear imperative.
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
