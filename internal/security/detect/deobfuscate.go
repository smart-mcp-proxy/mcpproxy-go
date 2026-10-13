package detect

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// NormalizeForPhrase is Normalize preceded by a de-obfuscation pass that undoes
// the cheap, reversible tricks an attacker uses to keep an injection directive
// readable to a model while hiding it from literal phrase matchers (SEC-01):
//
//   - homoglyphs: Cyrillic/Greek/Armenian look-alike letters mixed into a Latin
//     word ("іgnоrе") are folded to Latin; Latin look-alikes mixed into a
//     Cyrillic word ("игнoрируй") are folded to Cyrillic. A token is folded only
//     when it MIXES scripts, or when it is made entirely of look-alike letters
//     inside predominantly-Latin text, so ordinary Russian/Greek/Ukrainian prose
//     keeps its spelling and stays matchable by the non-English families.
//   - leet digits inside a letter word ("1gn0r3", "pr3v10u5"): only tokens that
//     mix letters with the digits 0 1 3 4 5 7 are touched, so version strings,
//     hashes and identifiers like "sha256", "utf8" or "oauth2" are left alone.
//   - separator interleaving ("i.g.n.o.r.e", "ig-no-re", "i_g_n_o_r_e"): a token
//     made of three or more 1–2 letter segments joined by one repeated separator
//     is joined. Two-segment forms ("e.g.", "i.e.", "co-op") are untouched, which
//     keeps the example cues the position classifier relies on.
//   - letter spacing ("i g n o r e"): a run of three or more single letters
//     separated by single spaces is joined; a wider gap ends the run, so
//     "I G N O R E  A L L" yields "ignore all".
//
// The result is consumed ONLY by the phrase matchers (phrase.injection,
// directive.imperative) through ToolView.PhraseText. NormalizedText is unchanged,
// so capability/shadowing checks and every existing consumer see the same text
// as before. Pure and deterministic, like Normalize.
func NormalizeForPhrase(s string) string {
	if s == "" {
		return ""
	}
	return NormalizeForPhraseIn(s, LatinMajority(s))
}

// LatinMajority reports whether Latin letters outnumber Cyrillic, Greek and
// Armenian letters together in s (after NFKC). It is the document-level
// script census homoglyph folding uses to decide which way an ambiguous mixed
// token ("тeм": Cyrillic т/м, Latin e) folds.
func LatinMajority(s string) bool {
	return scriptCensus([]rune(norm.NFKC.String(s))).latinMajority()
}

// NormalizeForPhraseIn is NormalizeForPhrase for one fragment (clause) of a
// larger text, with the script majority taken from the WHOLE text
// (LatinMajority of it). A short fragment's own census is not evidence: the
// chunk "тeм." of "sys. тeм. pro. mpt." has a Cyrillic majority on its own,
// which would fold it to "тем" and hide "system prompt" from the squashed pass
// (SEC-01 review, Sol round 7).
func NormalizeForPhraseIn(s string, latinMajority bool) string {
	if s == "" {
		return ""
	}
	s = norm.NFKC.String(s)
	s = stripFormatRunes(s)
	s = foldConfusables(s, latinMajority, false)
	s = foldLeetTokens(s)
	s = joinSeparatedLetters(s)
	s = joinSpacedLetters(s)
	return Normalize(s)
}

// FoldHomoglyphsIn applies NFKC, format-rune stripping and homoglyph folding
// (the first steps of NormalizeForPhraseIn) to a WHOLE text before a caller
// cuts it into fragments. Folding needs a word's neighbours: an entirely
// look-alike chunk ("тем" in "sys. тем. pro. mpt.") folds to Latin only beside
// a Latin word, and once the text is cut into clauses those neighbours sit in
// other fragments (SEC-01 review, Sol round 8). Genuine Cyrillic prose keeps
// its spelling because its neighbours are Cyrillic too.
func FoldHomoglyphsIn(s string, latinMajority bool) string {
	if s == "" {
		return ""
	}
	return foldConfusables(stripFormatRunes(norm.NFKC.String(s)), latinMajority, false)
}

// NormalizeMixedScript is Normalize preceded by homoglyph folding of MIXED-script
// words only (RT-04): a word that combines Latin letters with Cyrillic, Greek or
// Armenian look-alikes ("іgnоrе", "рrеvіоuѕ") is folded to one script, while a
// word written entirely in one script keeps its spelling, whatever its
// neighbours. Unlike NormalizeForPhrase it does not join spaced or separated
// letters and does not rewrite leet digits, so word boundaries, punctuation,
// negations and quotes are exactly those of the plain text and position
// classification stays truthful. A single word that mixes scripts is not
// ordinary writing; it is the homoglyph trick itself. changed reports whether
// any word was folded.
func NormalizeMixedScript(s string) (normalized string, changed bool) {
	if s == "" {
		return "", false
	}
	pre := stripFormatRunes(norm.NFKC.String(s))
	folded := foldConfusables(pre, LatinMajority(s), true)
	if folded == pre {
		return Normalize(s), false
	}
	return Normalize(folded), true
}

// Squash reduces text to its letters only. Phrase checks use it as a last,
// soft-tier pass that is indifferent to ANY spacing or punctuation scheme the
// earlier steps did not anticipate ("s y s t e m p r o m p t").
func Squash(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// toLatin maps Cyrillic, Greek and Armenian letters that render like a Latin
// letter (a subset of Unicode confusables.txt) to that Latin letter. Both cases
// are listed because folding runs before lowercasing.
var toLatin = map[rune]rune{
	// Cyrillic
	'а': 'a', 'А': 'A', 'в': 'b', 'В': 'B', 'е': 'e', 'Е': 'E', 'ё': 'e', 'Ё': 'E',
	'і': 'i', 'І': 'I', 'ї': 'i', 'Ї': 'I', 'ј': 'j', 'Ј': 'J', 'к': 'k', 'К': 'K',
	'м': 'm', 'М': 'M', 'н': 'h', 'Н': 'H', 'о': 'o', 'О': 'O', 'р': 'p', 'Р': 'P',
	'с': 'c', 'С': 'C', 'т': 't', 'Т': 'T', 'у': 'y', 'У': 'Y', 'х': 'x', 'Х': 'X',
	'ѕ': 's', 'Ѕ': 'S', 'һ': 'h', 'Һ': 'H', 'ԁ': 'd', 'ԛ': 'q', 'Ԛ': 'Q', 'ԝ': 'w',
	'Ԝ': 'W', 'ӏ': 'l', 'Ӏ': 'I', 'ɡ': 'g', 'ү': 'y', 'Ү': 'Y',
	// Greek
	'α': 'a', 'Α': 'A', 'β': 'b', 'Β': 'B', 'ε': 'e', 'Ε': 'E', 'ζ': 'z', 'Ζ': 'Z',
	'η': 'n', 'Η': 'H', 'ι': 'i', 'Ι': 'I', 'κ': 'k', 'Κ': 'K', 'μ': 'u', 'Μ': 'M',
	'ν': 'v', 'Ν': 'N', 'ο': 'o', 'Ο': 'O', 'ρ': 'p', 'Ρ': 'P', 'τ': 't', 'Τ': 'T',
	'υ': 'u', 'Υ': 'Y', 'χ': 'x', 'Χ': 'X', 'ϲ': 'c', 'Ϲ': 'C', 'ϳ': 'j',
	// Armenian
	'օ': 'o', 'Օ': 'O', 'ս': 'u', 'Ս': 'U', 'հ': 'h', 'ո': 'n', 'զ': 'q', 'ց': 'g',
	'ա': 'w', 'ե': 'e', 'ւ': 'l',
	// Latin-script look-alikes (same script, folded unconditionally below)
	'ı': 'i', 'ɑ': 'a', 'ɩ': 'i', 'ʟ': 'l',
}

// latinLookalikes are Latin-script letters folded wherever they appear: they
// are not plain ASCII but render as one, and they never form a mixed-script
// token on their own.
var latinLookalikes = map[rune]rune{'ı': 'i', 'ɑ': 'a', 'ɩ': 'i', 'ʟ': 'l', 'ɡ': 'g'}

// toCyrillic maps Latin letters that render like a Cyrillic letter, for the
// reverse trick: Latin letters mixed into a Cyrillic word.
var toCyrillic = map[rune]rune{
	'a': 'а', 'A': 'А', 'e': 'е', 'E': 'Е', 'o': 'о', 'O': 'О', 'p': 'р', 'P': 'Р',
	'c': 'с', 'C': 'С', 'x': 'х', 'X': 'Х', 'y': 'у', 'Y': 'У', 'i': 'і', 'I': 'І',
	'k': 'к', 'K': 'К', 'M': 'М', 'H': 'Н', 'T': 'Т', 'B': 'В',
}

type scriptCount struct{ latin, cyrillic, other int }

func (c scriptCount) latinMajority() bool { return c.latin > c.cyrillic+c.other }

// scriptCensus counts the Latin, Cyrillic and Greek/Armenian letters of runes.
func scriptCensus(runes []rune) scriptCount {
	var doc scriptCount
	for _, r := range runes {
		switch classifyLetter(r) {
		case 0:
			doc.latin++
		case 1:
			doc.cyrillic++
		case 2:
			doc.other++
		}
	}
	return doc
}

func classifyLetter(r rune) int {
	switch {
	case unicode.Is(unicode.Latin, r):
		return 0
	case unicode.Is(unicode.Cyrillic, r):
		return 1
	case unicode.Is(unicode.Greek, r), unicode.Is(unicode.Armenian, r):
		return 2
	default:
		return -1
	}
}

// confusableToken is one letter-run (word) with its script census.
type confusableToken struct {
	start, end     int // rune offsets [start,end)
	counts         scriptCount
	canLatin       bool // every non-Latin letter has a Latin look-alike
	canCyr         bool // every Latin letter has a Cyrillic look-alike, the rest is Cyrillic
	hasLatinLetter bool
}

// foldConfusables applies the homoglyph folding described on
// NormalizeForPhrase, one letter-run (word) at a time.
//
// A token made ENTIRELY of look-alike letters (Cyrillic "все", "сору") is
// ambiguous: it is either a genuine Cyrillic word or a fully disguised Latin
// one. It is folded to Latin only when the text is predominantly Latin AND an
// adjacent word contains Latin letters, so a Russian sentence quoted inside an
// English description keeps its own words ("Игнорируй все предыдущие …" stays
// matchable by the Russian family).
//
// latinMajority is the script census of the whole text the fragment s belongs
// to (see NormalizeForPhraseIn); for a whole text it is LatinMajority(s).
//
// mixedOnly restricts folding to words that themselves mix scripts: whole
// look-alike words are kept, and so are the Latin look-alike letters (dotless
// ı and friends), which can be ordinary Turkish or IPA text (see
// NormalizeMixedScript).
func foldConfusables(s string, latinMajority, mixedOnly bool) string {
	runes := []rune(s)

	var toks []confusableToken
	for i := 0; i < len(runes); {
		if !unicode.IsLetter(runes[i]) {
			i++
			continue
		}
		t := confusableToken{start: i, canLatin: true, canCyr: true}
		j := i
		for j < len(runes) && (unicode.IsLetter(runes[j]) || unicode.Is(unicode.Mn, runes[j])) {
			r := runes[j]
			switch classifyLetter(r) {
			case 0:
				t.counts.latin++
				t.hasLatinLetter = true
				if _, ok := toCyrillic[r]; !ok {
					t.canCyr = false
				}
			case 1:
				t.counts.cyrillic++
				if _, ok := toLatin[r]; !ok {
					t.canLatin = false
				}
			case 2:
				t.counts.other++
				t.canCyr = false
				if _, ok := toLatin[r]; !ok {
					t.canLatin = false
				}
			default:
				if unicode.IsLetter(r) {
					t.canLatin, t.canCyr = false, false
				}
			}
			j++
		}
		t.end = j
		toks = append(toks, t)
		i = j
	}

	// Pass 1: decide each token's folding direction. Mixed tokens are resolved
	// on their own evidence; whole-look-alike tokens need their neighbours.
	const (
		keep = iota
		foldLatin
		foldCyr
		undecided // whole word of look-alikes: decided in pass 2
	)
	dir := make([]int, len(toks))
	for ti, t := range toks {
		nonLatin := t.counts.cyrillic + t.counts.other
		mixed := t.counts.latin > 0 && nonLatin > 0
		switch {
		case mixed && t.canLatin && t.canCyr:
			if latinMajority {
				dir[ti] = foldLatin
			} else {
				dir[ti] = foldCyr
			}
		case mixed && t.canLatin:
			dir[ti] = foldLatin
		case mixed && t.canCyr:
			dir[ti] = foldCyr
		case t.counts.latin == 0 && nonLatin > 0 && t.canLatin && latinMajority && !mixedOnly:
			dir[ti] = undecided
		}
	}
	// resolvedLatin reports whether token ti reads as a Latin word AFTER pass 1:
	// a mixed word restored to Cyrillic ("Игнoрируй" → "игнорируй") is not a
	// Latin neighbour, even though it contained a Latin letter.
	pass1 := append([]int(nil), dir...) // pass 2 reads an immutable snapshot
	resolvedLatin := func(ti int) bool {
		if ti < 0 || ti >= len(toks) {
			return false
		}
		switch pass1[ti] {
		case foldLatin:
			return true
		case foldCyr, undecided:
			return false
		}
		return toks[ti].hasLatinLetter && toks[ti].counts.cyrillic+toks[ti].counts.other == 0
	}
	// Pass 2: a whole word of look-alikes folds to Latin only between Latin
	// words; otherwise it is a genuine Cyrillic/Greek word and is kept.
	for ti := range toks {
		if dir[ti] == undecided {
			if resolvedLatin(ti-1) || resolvedLatin(ti+1) {
				dir[ti] = foldLatin
			} else {
				dir[ti] = keep
			}
		}
	}

	changed := false
	for ti, t := range toks {
		for k := t.start; k < t.end; k++ {
			c := classifyLetter(runes[k])
			if dir[ti] == foldLatin && c != 0 {
				if l, ok := toLatin[runes[k]]; ok {
					runes[k] = l
					changed = true
				}
			}
			if dir[ti] == foldCyr && c == 0 {
				if cy, ok := toCyrillic[runes[k]]; ok {
					runes[k] = cy
					changed = true
				}
			}
			if l, ok := latinLookalikes[runes[k]]; ok && !mixedOnly {
				runes[k] = l
				changed = true
			}
		}
	}
	if !changed {
		return s
	}
	return string(runes)
}

// leetDigits are the digit substitutions a model still reads as letters.
var leetDigits = map[rune]rune{'0': 'o', '1': 'i', '3': 'e', '4': 'a', '5': 's', '7': 't'}

// foldLeetTokens rewrites letter+digit words ("1gn0r3", "pr10r", "4ll") whose
// digits are all leet substitutes and which carry at least two letters. Any
// other digit (2, 6, 8, 9) leaves the token alone, which keeps identifiers such
// as "sha256", "utf8", "oauth2", "x86" and "i18n" intact.
func foldLeetTokens(s string) string {
	runes := []rune(s)
	changed := false
	for i := 0; i < len(runes); {
		if !unicode.IsLetter(runes[i]) && !unicode.IsDigit(runes[i]) {
			i++
			continue
		}
		j := i
		letters, digits, foreignDigit := 0, 0, false
		for j < len(runes) && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j])) {
			switch {
			case unicode.IsLetter(runes[j]):
				letters++
			default:
				digits++
				if _, ok := leetDigits[runes[j]]; !ok {
					foreignDigit = true
				}
			}
			j++
		}
		if digits > 0 && letters >= 2 && !foreignDigit && j-i >= 3 {
			for k := i; k < j; k++ {
				if l, ok := leetDigits[runes[k]]; ok {
					runes[k] = l
					changed = true
				}
			}
		}
		i = j
	}
	if !changed {
		return s
	}
	return string(runes)
}

// separatedLetters matches a whole token core of three or more 1–2 letter
// segments joined by ONE repeated separator character.
var separatedLetters = func() []*regexp.Regexp {
	seps := []string{`\.`, `-`, `_`, `·`, `•`, `\*`, `~`, `\|`, `/`, `\+`, `‧`, `∙`}
	out := make([]*regexp.Regexp, 0, len(seps))
	for _, sep := range seps {
		out = append(out, regexp.MustCompile(`^(?:\p{L}{1,2}`+sep+`){2,}\p{L}{1,2}$`))
	}
	return out
}()

var separatorRunes = ".-_·•*~|/+‧∙"

// joinSeparatedLetters joins separator-interleaved tokens ("i.g.n.o.r.e",
// "ig-no-re", "in-st-ru-ct-io-ns") while preserving the token's leading and
// trailing punctuation and every whitespace character.
func joinSeparatedLetters(s string) string {
	if !strings.ContainsAny(s, separatorRunes) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	changed := false
	for _, tok := range splitKeepSpaces(s) {
		if tok.space {
			b.WriteString(tok.text)
			continue
		}
		lead, core, trail := trimPunct(tok.text)
		joined := core
		for _, re := range separatedLetters {
			if re.MatchString(core) {
				joined = strings.Map(func(r rune) rune {
					if strings.ContainsRune(separatorRunes, r) {
						return -1
					}
					return r
				}, core)
				break
			}
		}
		if joined != core {
			changed = true
		}
		b.WriteString(lead)
		b.WriteString(joined)
		b.WriteString(trail)
	}
	if !changed {
		return s
	}
	return b.String()
}

// joinSpacedLetters joins runs of three or more single letters separated by
// exactly one ASCII space ("i g n o r e" → "ignore"). The first letter of a
// run may carry leading punctuation and the last may carry trailing
// punctuation; a gap of two or more spaces, or any other whitespace, ends a
// run (that is how "I G N O R E  A L L" keeps its word boundary).
func joinSpacedLetters(s string) string {
	toks := splitKeepSpaces(s)
	type wordTok struct {
		idx                int // index into toks
		lead, letter, tail string
		single             bool
	}
	words := make([]wordTok, 0, len(toks))
	for i, t := range toks {
		if t.space {
			continue
		}
		lead, core, tail := trimPunct(t.text)
		single := len([]rune(core)) == 1 && unicode.IsLetter([]rune(core)[0])
		words = append(words, wordTok{idx: i, lead: lead, letter: core, tail: tail, single: single})
	}

	// joinedAt[k] = replacement for toks[k]; skip[k] = drop toks[k].
	replace := map[int]string{}
	skip := map[int]bool{}
	for w := 0; w < len(words); {
		if !words[w].single || words[w].tail != "" {
			w++
			continue
		}
		end := w
		for end+1 < len(words) {
			next := words[end+1]
			gap := toks[next.idx-1]
			if !next.single || next.lead != "" || !gap.space || gap.text != " " {
				break
			}
			end++
			if next.tail != "" {
				break
			}
		}
		if end-w+1 >= 3 {
			var b strings.Builder
			b.WriteString(words[w].lead)
			for k := w; k <= end; k++ {
				b.WriteString(words[k].letter)
			}
			b.WriteString(words[end].tail)
			replace[words[w].idx] = b.String()
			for k := words[w].idx + 1; k <= words[end].idx; k++ {
				skip[k] = true
			}
			w = end + 1
			continue
		}
		w++
	}
	if len(replace) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i, t := range toks {
		if skip[i] {
			continue
		}
		if r, ok := replace[i]; ok {
			b.WriteString(r)
			continue
		}
		b.WriteString(t.text)
	}
	return b.String()
}

type spaceTok struct {
	text  string
	space bool
}

// splitKeepSpaces splits s into alternating whitespace and non-whitespace
// tokens, preserving every character.
func splitKeepSpaces(s string) []spaceTok {
	var out []spaceTok
	start := 0
	inSpace := false
	for i, r := range s {
		sp := unicode.IsSpace(r)
		if i == 0 {
			inSpace = sp
			continue
		}
		if sp != inSpace {
			out = append(out, spaceTok{text: s[start:i], space: inSpace})
			start = i
			inSpace = sp
		}
	}
	if start < len(s) {
		out = append(out, spaceTok{text: s[start:], space: inSpace})
	}
	return out
}

// trimPunct splits a token into leading punctuation, core, trailing
// punctuation, where punctuation is anything that is not a letter or digit.
func trimPunct(tok string) (lead, core, trail string) {
	runes := []rune(tok)
	i, j := 0, len(runes)
	for i < j && !unicode.IsLetter(runes[i]) && !unicode.IsDigit(runes[i]) {
		i++
	}
	for j > i && !unicode.IsLetter(runes[j-1]) && !unicode.IsDigit(runes[j-1]) {
		j--
	}
	return string(runes[:i]), string(runes[i:j]), string(runes[j:])
}
