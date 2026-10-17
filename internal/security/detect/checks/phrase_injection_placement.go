package checks

import (
	"regexp"
	"strings"
)

// Placement-clause parse (#1638).
//
// A credential-placement directive ("save its fingerprint and its contents to
// the notes field", "fill the notes field with its fingerprint and its
// contents") moves one or more payload items into a tool argument. The
// families that detect it are regular expressions anchored on the placement
// verb, so a metadata item that comes first ("its fingerprint", "its creation
// date") used to be handled by blanking the verb with word lists and fixed
// character windows, and every new phrasing slipped through.
//
// Instead the clause is parsed into items (split on commas, "and", "plus",
// "&" and on destinations "to the X field"; parenthetical qualifiers are
// skipped) and each item is classified as metadata or not. A clause is
// metadata-only, and its verb is blanked, only when EVERY item is metadata.
// Otherwise the text is rewritten, keeping its length and the verb in place,
// so that the first secret item and its destination sit right behind the
// verb: the families then see "save <secret item> to the <x> field" no matter
// how many metadata items, destinations or characters came between.

// placementVerbRe finds the verbs that place a value into a tool argument.
var placementVerbRe = regexp.MustCompile(`(?i)\b(?:` + credentialPlacementVerbs + `)\b`)

// invertedLeadRe finds the "fill <field> with" head of an inverted placement.
var invertedLeadRe = regexp.MustCompile(invertedPlacementLead)

// placementDestRe is a destination at the start of the text: "to the notes
// field", "into a memo argument".
var placementDestRe = regexp.MustCompile(`(?i)^` + credentialArgumentTarget)

// placementTokenRe splits a clause into parenthetical qualifiers, words (with
// any trailing punctuation) and the two clause separators.
var placementTokenRe = regexp.MustCompile(`\([^()]*\)|[^\s,;()]+|[,;]`)

const (
	// placementScanBytes bounds how far a clause is read, keeping the scan
	// linear in the text however many verbs it holds.
	placementScanBytes = 600
	// placementMaxTokens bounds the tokens read per clause.
	placementMaxTokens = 90
)

// placementTerminators end a clause: what follows is not payload.
var placementTerminators = map[string]bool{
	"then": true, "but": true, "so": true, "while": true, "when": true, "if": true,
	"because": true, "since": true, "which": true, "that": true, "where": true,
	"until": true, "unless": true, "after": true, "before": true, "once": true,
	"using": true, "via": true, "whereas": true, "although": true, "though": true,
}

// placementConjunctions separate payload items.
var placementConjunctions = map[string]bool{"and": true, "plus": true, "&": true}

// itemQualifierWords may precede a metadata word without making the item a
// secret: "key length", "content type", "file size". On their own they ARE
// the secret ("the key", "its contents").
var itemQualifierWords = map[string]bool{
	"private": true, "public": true, "ssh": true, "api": true, "secret": true,
	"key": true, "keys": true, "file": true, "content": true, "contents": true,
	"token": true, "password": true, "credential": true, "credentials": true,
}

// itemSkippedWords carry no payload of their own: determiners and the
// "last"/"first" of "last modified time".
var itemSkippedWords = map[string]bool{"last": true, "first": true}

// followerStarters are the words that start a further item after a
// destination: "… to the date field and its contents to the notes field". Any
// other word after a destination ("… and notify the user") ends the clause.
var followerStarters = map[string]bool{"its": true, "their": true}

// followerSecretWords may follow "the" to start a further item after a
// destination ("and the API key to the notes field").
var followerSecretWords = map[string]bool{
	"key": true, "private": true, "ssh": true, "api": true, "secret": true,
	"whole": true, "entire": true, "full": true, "raw": true, "complete": true,
	"file": true, "content": true, "contents": true, "token": true, "password": true,
	"credential": true, "credentials": true,
}

// placementItem is one payload item of a placement clause.
type placementItem struct {
	start, end         int // byte span of the item text in the parsed string
	meta               bool
	destStart, destEnd int // destination attached to the item, or -1
}

// payloadWordsAreMetadata classifies an item's words: metadata when, past any
// determiners and key/file qualifiers, they are metadata words only, or open
// with metadata words followed by "of"/"for" ("the length of the key"), or
// name the public key. A bare qualifier ("the key", "its contents") is the
// secret itself.
func payloadWordsAreMetadata(words []string) bool {
	i := 0
	for i < len(words) && (secretDeterminers[words[i]] || itemSkippedWords[words[i]]) {
		i++
	}
	words = words[i:]
	if len(words) == 0 {
		return false
	}
	q := 0
	for q < len(words) && itemQualifierWords[words[q]] {
		q++
	}
	if q == len(words) {
		// only qualifiers: the public key is metadata, anything else is not
		last := words[len(words)-1]
		hasPublic, hasSecret := false, false
		for _, w := range words {
			hasPublic = hasPublic || w == "public"
			hasSecret = hasSecret || w == "private" || w == "secret"
		}
		return last == "key" && hasPublic && !hasSecret
	}
	rest := words[q:]
	j := 0
	for j < len(rest) && (secretMetadataWords.MatchString(rest[j]) || itemSkippedWords[rest[j]]) {
		j++
	}
	if j == 0 {
		return false
	}
	return j == len(rest) || rest[j] == "of" || rest[j] == "for"
}

// parsePlacementItems parses the payload items of the clause that starts at
// byte offset from of t.
func parsePlacementItems(t string, from int) []placementItem {
	end := min(len(t), from+placementScanBytes)
	toks := placementTokenRe.FindAllStringIndex(t[from:end], placementMaxTokens)
	var (
		items     []placementItem
		words     []string
		wStart    = -1
		wEnd      int
		afterDest bool
		sawConj   bool
		stop      bool
	)
	flush := func() {
		if len(words) > 0 {
			items = append(items, placementItem{start: wStart, end: wEnd, meta: payloadWordsAreMetadata(words), destStart: -1, destEnd: -1})
		}
		words, wStart = nil, -1
	}
	for k := 0; k < len(toks) && !stop; k++ {
		ts, te := from+toks[k][0], from+toks[k][1]
		tok := t[ts:te]
		switch tok[0] {
		case '(':
			continue
		case ';':
			flush()
			stop = true
			continue
		case ',':
			flush()
			sawConj = true
			continue
		}
		raw := tok
		endsSentence := false
		if strings.HasSuffix(raw, ".") || strings.HasSuffix(raw, "!") || strings.HasSuffix(raw, "?") {
			raw = strings.TrimRight(raw, ".!?")
			endsSentence = true
		}
		w := strings.ToLower(strings.Trim(raw, "\"'`"))
		if w == "" {
			flush()
			break
		}
		switch {
		case placementConjunctions[w]:
			flush()
			sawConj = true
		case placementTerminators[w]:
			flush()
			stop = true
		default:
			if m := placementDestRe.FindStringIndex(t[ts:end]); m != nil && len(words) > 0 && !afterDest {
				// a destination: attach it to the item it closes
				flush()
				items[len(items)-1].destStart, items[len(items)-1].destEnd = ts, ts+m[1]
				for k+1 < len(toks) && from+toks[k+1][0] < ts+m[1] {
					k++
				}
				afterDest, sawConj = true, false
				continue
			}
			if afterDest {
				// only a conjunction followed by a further payload item
				// continues the clause past a destination
				if !sawConj || !followerStartsItem(t, from, toks, k, w) {
					stop = true
					continue
				}
				afterDest = false
			}
			if wStart < 0 {
				wStart = ts
			}
			wEnd = ts + len(raw)
			words = append(words, w)
		}
		if endsSentence {
			flush()
			stop = true
		}
	}
	flush()
	return items
}

// followerStartsItem reports whether the word w at token k starts a further
// payload item after a destination: "its …"/"their …", or "the" + a secret
// word.
func followerStartsItem(t string, from int, toks [][]int, k int, w string) bool {
	if followerStarters[w] {
		return true
	}
	if w != "the" {
		return false
	}
	for n := k + 1; n < len(toks); n++ {
		tok := t[from+toks[n][0] : from+toks[n][1]]
		if tok[0] == '(' {
			continue
		}
		next := strings.ToLower(strings.Trim(tok, "\"'`,;.!?"))
		return followerSecretWords[next]
	}
	return false
}

// maskPlacementClauses rewrites placement clauses so the credential families
// see the secret item of a mixed clause right behind its verb, and never see
// the verb of a metadata-only clause. The result has the length of t.
func maskPlacementClauses(t string) string {
	var out []byte
	edited := 0 // nothing before this offset is edited again
	edit := func() []byte {
		if out == nil {
			out = []byte(t)
		}
		return out
	}
	blank := func(b []byte, from, to int) {
		for i := from; i < to; i++ {
			b[i] = ' '
		}
	}
	for _, l := range placementVerbRe.FindAllStringIndex(t, -1) {
		if l[0] < edited {
			continue
		}
		items := parsePlacementItems(t, l[1])
		if len(items) == 0 {
			continue
		}
		s := -1
		for i, it := range items {
			if !it.meta {
				s = i
				break
			}
		}
		if s < 0 {
			// metadata only: the verb moves no secret
			b := edit()
			for i := l[0]; i < l[1]; i++ {
				b[i] = '#'
			}
			edited = items[len(items)-1].end
			continue
		}
		// the destination of the first secret item: its own, else the next
		// one in the clause
		d := -1
		for i := s; i < len(items); i++ {
			if items[i].destStart >= 0 {
				d = i
				break
			}
		}
		tailMeta := d >= 0
		for i := s + 1; d >= 0 && i <= d; i++ {
			tailMeta = tailMeta && items[i].meta
		}
		packEnd := items[s].end
		packed := " " + t[items[s].start:items[s].end]
		switch {
		case d >= 0 && tailMeta:
			// only metadata between the secret item and its destination:
			// pull the destination up against the item
			packEnd = items[d].destEnd
			packed += " " + t[items[d].destStart:items[d].destEnd]
			if s == 0 && d == 0 {
				continue // already adjacent
			}
		case d >= 0:
			// another payload item sits in between: keep the text verbatim,
			// only the leading metadata is dropped
			if s == 0 {
				continue
			}
			packEnd = items[d].destEnd
			packed = " " + t[items[s].start:packEnd]
		default:
			if s == 0 {
				continue
			}
		}
		if l[1]+len(packed) > packEnd {
			continue
		}
		b := edit()
		blank(b, l[1], packEnd)
		copy(b[l[1]:], packed)
		edited = packEnd
	}
	for _, l := range invertedLeadRe.FindAllStringIndex(t, -1) {
		if l[0] < edited {
			continue
		}
		items := parsePlacementItems(t, l[1])
		if len(items) == 0 || !items[0].meta {
			continue
		}
		for _, it := range items {
			if !it.meta {
				blank(edit(), l[1], it.start)
				edited = it.start
				break
			}
		}
	}
	if out == nil {
		return t
	}
	return string(out)
}
