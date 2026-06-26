package detect

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// irregularContractions expands contractions whose stem before "n't" is not the
// plain base verb. They must run before the generic "n't" → " not" rule.
var irregularContractions = strings.NewReplacer(
	"can't", "cannot",
	"won't", "will not",
	"shan't", "shall not",
)

// apostrophes folds the curly/backtick apostrophe variants to a plain ASCII
// apostrophe so contraction expansion sees a single canonical form. (NFKC does
// not map U+2019 to U+0027.)
var apostrophes = strings.NewReplacer(
	"’", "'", // ’ right single quote
	"‘", "'", // ‘ left single quote
	"ʼ", "'", // ʼ modifier letter apostrophe
	"`", "'",
)

// Normalize canonicalizes free text for semantic matching by the soft checks
// (FR-009). It applies, in order: NFKC, removal of zero-width / bidi / BOM
// format runes (Unicode category Cf), lowercasing, contraction expansion
// (so "don't disclose" and "do not tell" both yield a "do not …" form),
// whitespace collapse + trim, and light stemming.
//
// It is pure and deterministic. The HARD unicode check (FR-007) deliberately
// runs on RAW text instead, so stripping format runes here never hides an
// attack — it only stabilizes phrase matching.
func Normalize(s string) string {
	if s == "" {
		return ""
	}

	// 1. NFKC: fold compatibility forms (fullwidth, ligatures, etc.).
	s = norm.NFKC.String(s)

	// 2. Strip zero-width / bidi / BOM and other format runes.
	s = stripFormatRunes(s)

	// 3. Lowercase.
	s = strings.ToLower(s)

	// 4. Expand contractions.
	s = apostrophes.Replace(s)
	s = irregularContractions.Replace(s)
	s = strings.ReplaceAll(s, "n't", " not")

	// 5. Collapse whitespace + trim, then 6. stem each token.
	fields := strings.Fields(s)
	for i, f := range fields {
		fields[i] = stem(f)
	}
	return strings.Join(fields, " ")
}

// stripFormatRunes removes Unicode format (Cf) runes — zero-width spaces/joiners
// (U+200B–U+200D), the BOM/ZWNBSP (U+FEFF), bidi controls, soft hyphen, etc.
func stripFormatRunes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// stem applies light suffix stripping so plural/gerund forms match their base.
// Conservative by design: it never strips a trailing "s" preceded by s/i/u/e to
// protect words like "previous", "this", "address", and "does".
func stem(w string) string {
	if len(w) <= 3 {
		return w
	}
	switch {
	case strings.HasSuffix(w, "ing") && len(w)-3 >= 3:
		return w[:len(w)-3]
	case strings.HasSuffix(w, "ed") && len(w)-2 >= 3:
		return w[:len(w)-2]
	case strings.HasSuffix(w, "es") && len(w)-2 >= 3:
		return w[:len(w)-2]
	case strings.HasSuffix(w, "s") && len(w)-1 >= 3 && !strings.ContainsRune("siue", rune(w[len(w)-2])):
		return w[:len(w)-1]
	}
	return w
}
