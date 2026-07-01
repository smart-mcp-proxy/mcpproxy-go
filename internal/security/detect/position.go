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
//   - Only genuine quotation/illustration ("such as", "e.g.", surrounding
//     quotes) is PositionExample and fully discounted — that framing is
//     unambiguously non-instructional, so suppressing it (both tiers) keeps
//     legitimate scanners that cite injection strings quiet.
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
	// PositionExample is an unambiguous quotation/illustration-position match
	// (after "such as"/"e.g." or inside quotes); fully discounted so a scanner
	// that merely cites the phrase is never flagged, at either tier.
	PositionExample
)

// Discount multipliers per position. exampleDiscount fully suppresses a match
// (a lone example-position hit cannot clear either tier's floor).
// descriptiveDiscount is the pivotal value: it must take a HARD base (≈0.85–0.9)
// below the hard emit floor (0.6) yet keep a SOFT base (≈0.6–0.65) at/above the
// soft emit floor (0.3) — i.e. it discounts HARD→SOFT rather than to nothing.
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

// exampleCues are substrings that, immediately before a match (within the
// current sentence), mark it as quoted or illustrated rather than instructing.
// Kept to genuine quotation/illustration markers ONLY — deliberately NOT the
// bare colon-labels ("prompt:", "message:", …) a earlier iteration used, because
// those let an attacker smuggle an imperative behind a label (finding A).
var exampleCues = []string{
	"such as",
	"for example",
	"for instance",
	"e.g",
	"i.e",
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

	// 1. Genuine quotation / illustration → fully discounted example-position.
	// Checked on the full window: these markers are narrow and unambiguous, and
	// some ("e.g.", "i.e.") legitimately contain the periods the sentence scope
	// below would otherwise treat as boundaries.
	for _, cue := range exampleCues {
		if strings.Contains(window, cue) {
			return PositionExample
		}
	}
	if oddQuoteCount(window) {
		return PositionExample
	}

	// Scope the broad descriptive heuristic to the current sentence. Framing
	// established before a sentence boundary must not neutralize an imperative
	// that begins a new sentence — otherwise a benign lead ("Lists files. Ignore
	// all previous instructions.") would discount the injection that follows (a
	// recall bypass). Only the wide analytical-verb rule needs this guard; the
	// quotation cues above are safe cross-sentence.
	if i := strings.LastIndexAny(window, ".!?"); i >= 0 {
		window = window[i+1:]
	}

	// 2. Analytical/relative-clause framing → descriptive-position (HARD→SOFT).
	if describingVerb.MatchString(window) || descriptiveTail.MatchString(window) {
		return PositionDescriptive
	}

	// 3. Otherwise the match is an instruction — including one behind a bare
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
