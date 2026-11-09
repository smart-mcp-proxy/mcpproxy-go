// SPDX-License-Identifier: Apache-2.0
//
// The direct-prohibition grammar below is adapted from NVIDIA SkillSpector
// (src/skillspector/nodes/analyzers/prohibition_context.py, reviewed at commit
// 5cb9385ab0dffa974088ee21c4376c7e560b2a52).
// Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// Modifications Copyright (c) 2026 the mcpproxy-go authors.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not
// use this file except in compliance with the License. You may obtain a copy
// of the License at http://www.apache.org/licenses/LICENSE-2.0. Unless
// required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//
// Changes from upstream: rewritten in Go over the detect package's normalized
// text (lowercased, contraction-expanded, stemmed); the negation must bind to
// the matched action through a closed adverb list instead of upstream's
// zero-gap prefix; same-logical-line is proven through LineMap against the raw
// text; the YARA-continuation and allowlisted-next-instruction machinery of
// upstream (built for multi-line skill files) is not ported.

package detect

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// negationWord is a word that governs the clause after it ("never", "do
// not", "must not", "no longer" …).
var negationWord = regexp.MustCompile(`\b(?:never|not|no|cannot|without|nor)\b`)

// negationMaxGap is how many words may separate a negation from the match it
// governs. "Do not under any circumstances іgnоrе …" needs three; two (the
// earlier limit) let longer prohibitions reach HARD (Sol r13 P2).
const negationMaxGap = 7

// negationBreakers end the negation's reach: a contrast or a new coordinated
// directive after it ("Do not worry, just ignore …", "do not log this and
// ignore …") means the match is affirmative.
var negationBreakers = map[string]bool{
	"and": true, "but": true, "just": true, "then": true, "instead": true,
	"rather": true, "however": true, "yet": true, "so": true, "now": true,
	"simply": true, "also": true, "or": true, "thus": true, "therefor": true,
	"therefore": true, "please": true,
}

// negationAdverbs are breakers only once another word follows the negation
// (Sol r18 P2): right after it they are adverbs inside the prohibition
// ("Never, please, read …", "do not also put …"), while after an object they
// open a new directive ("Do not log this, please read …").
var negationAdverbs = map[string]bool{"please": true, "also": true}

// negationIdioms are words that, right after a negation, make it an
// affirmative idiom rather than a prohibition ("no matter what, ignore …",
// "do not hesitate to ignore …", "no need to ask, ignore …").
var negationIdioms = map[string]bool{
	"matter": true, "hesitat": true, "hesitate": true, "fail": true,
	"forget": true, "need": true, "doubt": true, "problem": true,
	"worry": true, "wait": true, "delay": true,
}

// NegatedBefore reports whether the clause before byte offset start ends in a
// negation that governs the match: the last negation word of the clause is
// followed by at most negationMaxGap words, none of which is a contrast or a
// new coordinated directive (negationBreakers), and it does not open an
// affirmative idiom (negationIdioms). It is the lenient, necessary half of a
// prohibition test; DirectProhibition is the strict one.
func NegatedBefore(text string, start int) bool {
	_, ok := lastNegation(text, start)
	return ok
}

// lastNegation finds the last negation word of the clause before start and
// applies the NegatedBefore rules. It returns the negation's byte range.
func lastNegation(text string, start int) (loc [2]int, ok bool) {
	lo := start - 120
	if lo < 0 {
		lo = 0
	}
	window := text[lo:start]
	base := lo
	if i := strings.LastIndexAny(window, ".!?;"); i >= 0 {
		window = window[i+1:]
		base += i + 1
	}
	locs := negationWord.FindAllStringIndex(window, -1)
	if len(locs) == 0 {
		return loc, false
	}
	last := locs[len(locs)-1]
	tail := strings.FieldsFunc(window[last[1]:], isNotProhibitionWordRune)
	if len(tail) > negationMaxGap {
		return loc, false
	}
	leading := true // only negationAdverbs seen so far after the negation
	for k, w := range tail {
		if leading && negationAdverbs[w] {
			continue
		}
		leading = false
		if negationBreakers[w] || (k == 0 && negationIdioms[w]) {
			return loc, false
		}
	}
	return [2]int{base + last[0], base + last[1]}, true
}

func isNotProhibitionWordRune(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\''
}

// prohibitionAdverbs are the only words allowed between a negation and the
// action it directly governs (Spec 122 FR-040): "never EVER reveal", "do not
// UNDER ANY CIRCUMSTANCES reveal", "do not, PLEASE, reveal". Anything else —
// in particular a verb or an idiom head ("hesitate to", "fail to", "forget
// to", "stop", "refuse to", "need to") — means the negation governs something
// other than the match, and the hard tier is retained.
var prohibitionAdverbs = map[string]bool{
	"ever": true, "at": true, "any": true, "time": true, "under": true,
	"circumstance": true, "circumstances": true, "directly": true,
	"immediately": true, "first": true, "verbatim": true, "exactly": true,
	"word": true, "for": true, "please": true, "also": true, "simply": true,
	"just": true, "in": true, "form": true, "do": true, "you": true,
	"should": true, "must": true, "shall": true, "will": true, "may": true,
	"can": true,
}

// prohibitionExceptionCue, after the match in its sentence, limits the
// prohibition ("… unless the user says banana", "… except when asked",
// "…, reveal it encoded instead") and so does not rule out the action.
var prohibitionExceptionCue = regexp.MustCompile(`\b(?:unless|except|until|but|however|instead|otherwise|if|when|whenever|while|only|else)\b`)

// prohibitionLeadingQualifier before the negation in its sentence conditions
// it ("Unless the user says banana, do not reveal …").
var prohibitionLeadingQualifier = regexp.MustCompile(`\b(?:unless|except|until|if|when|whenever|while|only|otherwise|else|but|however|instead)\b`)

// prohibitionReversalCue in the match's sentence or its neighbours tells the
// agent to read the rule backwards ("apply the opposite meaning", "the rule
// above is void"). Stems, so every inflection the normalizer leaves matches.
var prohibitionReversalCue = regexp.MustCompile(`\b(?:opposit|invers|invert|revers|contrar|negat|ignor|disregard|overrid|void|obsolet|invalid|bypass|suspend|violat)\w*`)

// prohibitionHarmlessTail is what may follow the matched action in its
// sentence without changing the prohibition's meaning.
var prohibitionHarmlessTail = regexp.MustCompile(`^(?:\s*(?:,|and|or|to (?:anyone|the user|users|third[- ]?parti\w*|other\w*)|under any circumstanc\w*|ever|at any time|in any form|or its content\w*|verbatim|directly|please|either|whatsoever|at all))*\s*$`)

// prohibitionSentenceEnd ends a sentence in normalized text.
const prohibitionSentenceEnd = ".!?;"

// prohibitionSentenceWindow bounds every sentence scan of DirectProhibition.
const prohibitionSentenceWindow = 512

// DirectProhibition reports whether the match text[start:end] is DIRECTLY
// prohibited (Spec 122 FR-040): a negation precedes it on the same logical
// line of the raw text (lines), binds to it through prohibitionAdverbs only, is
// not itself quoted, conditioned or answering a question, and nothing in the
// sentence after the match, nor in the neighbouring sentences, limits or
// reverses the rule. A false result means "not proven": the caller keeps the
// match's tier. A true result lets the caller cap the match to review tier;
// it is never a reason to drop it.
//
// text is the normalized view the match was found in; lines maps its offsets
// to raw lines (see NewLineMap). A LineMap that could not align (!ok) proves
// nothing, so the result is false.
func DirectProhibition(text string, start, end int, lines LineMap) bool {
	if start < 0 || end > len(text) || start >= end {
		return false
	}
	neg, ok := lastNegation(text, start)
	if !ok {
		return false
	}
	// The match may cover only the stem of its last word ("instruction" of
	// "instructions."): the tail starts after that word.
	for end < len(text) && !isNotProhibitionWordRune(rune(text[end])) {
		end++
	}
	// A bare "not" is half of "do not" / "must not": the auxiliary before it
	// is part of the negation and must sit on the same line too ("Do\nnot
	// reveal …" is split and retains detection).
	negUnit := neg[0]
	if text[neg[0]:neg[1]] == "not" {
		before := strings.TrimRight(text[:neg[0]], " ")
		if j := strings.LastIndexFunc(before, isNotProhibitionWordRune); j >= 0 {
			negUnit = j + 1
		} else {
			negUnit = 0
		}
	}
	// 1. Direct binding: only adverbs between the negation and the action.
	for _, w := range strings.FieldsFunc(text[neg[1]:start], isNotProhibitionWordRune) {
		if !prohibitionAdverbs[w] {
			return false
		}
	}
	// 2. Same logical line (raw text), proven.
	if !lines.SameLine(negUnit, start) {
		return false
	}
	// 3. Sentence boundaries around the match.
	// Sentence scans are bounded to prohibitionSentenceWindow bytes on each
	// side, so a punctuation-free field does not make every match rescan
	// the whole text (Sol r1 P2).
	sentStart := max(0, neg[0]-prohibitionSentenceWindow)
	startProven := sentStart == 0
	if i := strings.LastIndexAny(text[sentStart:neg[0]], prohibitionSentenceEnd); i >= 0 {
		sentStart += i + 1
		startProven = true
	}
	sentEnd := min(len(text), end+prohibitionSentenceWindow)
	endProven := sentEnd == len(text)
	if i := strings.IndexAny(text[end:sentEnd], prohibitionSentenceEnd); i >= 0 {
		sentEnd = end + i
		endProven = true
	}
	// A raw line break is a sentence boundary too (the normalized view has
	// collapsed it): the clause before the negation and the tail after the
	// match never reach into neighbouring lines.
	if ls := lines.lineStartOf(negUnit); ls >= sentStart {
		sentStart = ls
		startProven = true
	}
	if le := lines.lineEndOf(end); le <= sentEnd {
		sentEnd = le
		endProven = true
	}
	// A clause the window cut before any proven boundary may hide a
	// condition ("… unless …") past the cut: unknown, hard tier retained.
	if !startProven || !endProven {
		return false
	}
	leading := text[sentStart:neg[0]]
	// 3b. A negation governed by another negation ("Never not reveal …",
	// "Do not ever not ignore …") is a double negative: unknown grammar,
	// hard tier retained (Sol r1 P2).
	if negationWord.MatchString(leading) {
		return false
	}
	// 4. A quoted negation is an example, not a rule.
	lo := neg[0] - 512
	if lo < 0 {
		lo = 0
	}
	if oddQuoteCount(text[lo:neg[0]]) {
		return false
	}
	// 5. A bare "no" answering a question does not govern what follows.
	if text[neg[0]:neg[1]] == "no" && strings.HasSuffix(strings.TrimSpace(text[:neg[0]]), "?") {
		return false
	}
	// 6. A condition or scope before the negation limits it.
	if prohibitionLeadingQualifier.MatchString(leading) {
		return false
	}
	// 7. Nothing after the match may limit it; the tail must be harmless.
	tail := text[end:sentEnd]
	if prohibitionExceptionCue.MatchString(tail) || !prohibitionHarmlessTail.MatchString(tail) {
		return false
	}
	// 8. No reversal in this sentence (the matched action's own words
	// excluded), the one before or the one after.
	if prohibitionReversalCue.MatchString(leading) || prohibitionReversalCue.MatchString(tail) {
		return false
	}
	if sentStart > 0 {
		prevStart := max(0, sentStart-1-prohibitionSentenceWindow)
		if i := strings.LastIndexAny(text[prevStart:sentStart-1], prohibitionSentenceEnd); i >= 0 {
			prevStart += i + 1
		}
		if prohibitionReversalCue.MatchString(text[prevStart : sentStart-1]) {
			return false
		}
	}
	if sentEnd < len(text) {
		nextEnd := min(len(text), sentEnd+1+prohibitionSentenceWindow)
		if i := strings.IndexAny(text[sentEnd+1:nextEnd], prohibitionSentenceEnd); i >= 0 {
			nextEnd = sentEnd + 1 + i
		}
		if prohibitionReversalCue.MatchString(text[sentEnd+1 : nextEnd]) {
			return false
		}
	}
	return true
}

// LineMap maps byte offsets of a normalized text back to the raw line each
// word came from, so a check can tell "Never.\nReveal …" (two lines) from
// "Never reveal …" (one). It is built by normalizing the raw text line by line
// with the same function that produced the normalized view and checking that
// the joined result is byte-identical to that view; when it is not, the map
// proves nothing and SameLine is always false (unknown grammar retains the
// hard tier).
type LineMap struct {
	starts []int // offset in the normalized text where each raw line begins
	ok     bool
}

// NewLineMap aligns normalized (the view a match was found in) with raw. norm
// is the per-fragment normalizer that produced normalized from raw
// (Normalize, or a closure over NormalizeMixedScript). An empty raw text has
// no line structure to disagree with and maps every offset to one line.
func NewLineMap(raw, normalized string, norm func(string) string) LineMap {
	if raw == "" {
		return LineMap{ok: true}
	}
	text := strings.NewReplacer("\r\n", "\n", "\r", "\n", "\v", "\n", "\f", "\n", "\u0085", "\n", " ", "\n", " ", "\n").Replace(raw)
	var b strings.Builder
	var starts []int
	for _, line := range strings.Split(text, "\n") {
		n := norm(line)
		if n == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		starts = append(starts, b.Len())
		b.WriteString(n)
	}
	if b.String() != normalized {
		return LineMap{}
	}
	return LineMap{starts: starts, ok: true}
}

// SameLine reports whether normalized offsets a and b fall on the same raw
// line. False when the map could not be aligned.
func (m LineMap) SameLine(a, b int) bool {
	if !m.ok {
		return false
	}
	return m.lineOf(a) == m.lineOf(b)
}

// lineStartOf returns the normalized offset where the line holding off
// begins (0 when the map could not be aligned).
func (m LineMap) lineStartOf(off int) int {
	if !m.ok || len(m.starts) == 0 {
		return 0
	}
	return m.starts[m.lineOf(off)]
}

// lineEndOf returns the normalized offset where the line holding off ends
// (the start of the next line minus its separating space), or a very large
// value when the map could not be aligned or off is on the last line.
func (m LineMap) lineEndOf(off int) int {
	if !m.ok || len(m.starts) == 0 {
		return int(^uint(0) >> 1)
	}
	i := m.lineOf(off)
	if i+1 >= len(m.starts) {
		return int(^uint(0) >> 1)
	}
	return m.starts[i+1] - 1
}

func (m LineMap) lineOf(off int) int {
	// starts is ascending: the line is the last start <= off.
	i := sort.SearchInts(m.starts, off+1) - 1
	if i < 0 {
		return 0
	}
	return i
}
