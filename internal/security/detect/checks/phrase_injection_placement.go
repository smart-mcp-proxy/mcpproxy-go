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

// placementMaxItemWords is the longest run of words still read as one item; a
// longer run is not a metadata label, so the item is closed as a secret and
// the clause ends there. It keeps the scan of a verb that is followed by a
// wall of words short.
const placementMaxItemWords = 12

// placementTerminators end a clause: what follows is not payload. "then" and
// "after that" are not here: they separate items when a further item follows
// (placementSequenceWord).
var placementTerminators = map[string]bool{
	"but": true, "so": true, "while": true, "when": true, "if": true,
	"because": true, "since": true, "which": true, "that": true, "where": true,
	"until": true, "unless": true, "after": true, "before": true, "once": true,
	"using": true, "whereas": true, "although": true, "though": true,
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
	"line": true, "lines": true, "row": true, "rows": true, "word": true, "words": true,
}

// itemSkippedWords carry no payload of their own: the "last"/"first" of "last
// modified time".
var itemSkippedWords = map[string]bool{"last": true, "first": true}

// itemUnitWords may trail a metadata head without making the item a secret:
// "its size in bytes", "its fingerprint only", "its length in characters".
var itemUnitWords = map[string]bool{
	"only": true, "in": true, "as": true, "a": true, "number": true, "value": true,
	"byt": true, "byte": true, "bytes": true, "bit": true, "bits": true, "char": true, "chars": true,
	"character": true, "characters": true, "kb": true, "mb": true, "second": true,
	"seconds": true, "minute": true, "minutes": true, "hour": true, "hours": true,
	"day": true, "days": true, "ms": true, "hex": true, "hexadecimal": true,
	"base64": true, "string": true, "just": true, "alone": true,
}

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
// with metadata words followed by "of"/"for" ("the length of the key") or by
// unit words ("its size in bytes"), or name the public key. A bare qualifier
// ("the key", "its contents") is the secret itself.
func payloadWordsAreMetadata(words []string, inParen bool) bool {
	i := 0
	for i < len(words) && (secretDeterminers[words[i]] || itemSkippedWords[words[i]]) {
		i++
	}
	words = words[i:]
	if len(words) == 0 {
		return false
	}
	if inParen {
		// a qualifier made of unit words only: "(hex)", "(in bytes)"
		allUnits := true
		for _, w := range words {
			allUnits = allUnits && itemUnitWords[w]
		}
		if allUnits {
			return true
		}
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
	if j == len(rest) || rest[j] == "of" || rest[j] == "for" {
		return true
	}
	for _, w := range rest[j:] {
		if !itemUnitWords[w] {
			return false
		}
	}
	return true
}

// placementToks tokenizes a clause lazily, so a verb never pays for the text
// after the point where its clause ends.
type placementToks struct {
	t    string
	pos  int
	locs [][2]int
}

// at returns the absolute span of token i.
func (p *placementToks) at(i int) (int, int, bool) {
	for len(p.locs) <= i {
		if p.pos >= len(p.t) {
			return 0, 0, false
		}
		m := placementTokenRe.FindStringIndex(p.t[p.pos:])
		if m == nil {
			p.pos = len(p.t)
			return 0, 0, false
		}
		p.locs = append(p.locs, [2]int{p.pos + m[0], p.pos + m[1]})
		p.pos += m[1]
	}
	return p.locs[i][0], p.locs[i][1], true
}

// word returns the cleaned lower-case word of token i ("" for a parenthetical
// or separator).
func (p *placementToks) word(i int) string {
	s, e, ok := p.at(i)
	if !ok || p.t[s] == '(' || p.t[s] == ',' || p.t[s] == ';' {
		return ""
	}
	w := strings.ToLower(strings.Trim(strings.TrimRight(p.t[s:e], ".!?"), "\"'`"))
	w = strings.TrimSuffix(strings.TrimSuffix(w, "'s"), "\u2019s")
	return w
}

// nextWord returns the index and word of the first word token after i.
func (p *placementToks) nextWord(i int) (int, string) {
	for n := i + 1; ; n++ {
		s, _, ok := p.at(n)
		if !ok {
			return -1, ""
		}
		if p.t[s] == '(' || p.t[s] == ',' {
			continue
		}
		return n, p.word(n)
	}
}

// startsItem reports whether the word w at token k starts a further payload
// item: "its …"/"their …", or "the" + a secret word.
func (p *placementToks) startsItem(k int, w string) bool {
	if followerStarters[w] {
		return true
	}
	if w != "the" {
		return false
	}
	_, next := p.nextWord(k)
	return followerSecretWords[next]
}

// parenLeadWords introduce a further item inside a parenthetical ("(including
// its contents)", "(together with its contents)"); there they act as "and".
var parenLeadWords = map[string]bool{
	"including": true, "include": true, "includes": true, "with": true,
	"together": true, "along": true, "also": true, "as": true, "well": true,
}

// exclusionLeadWords open a parenthetical or phrase that excludes what it
// names: "(not its contents)".
var exclusionLeadWords = map[string]bool{
	"not": true, "never": true, "no": true, "without": true, "except": true,
	"excluding": true, "exclude": true, "neither": true, "nor": true, "but": true,
}

// notButRe is "not X but " at the start of the text: the part after "but" IS
// included ("not its size but its contents", "not just its size but its
// contents").
var notButRe = regexp.MustCompile(`(?i)^not\b[^.;]*?\bbut\s+`)

// notSplitRe is the exclusion head of a parenthetical, up to where the
// included part starts: "(not X but Y)", "(not X, Y)", "(not X; Y)".
var notSplitRe = regexp.MustCompile(`(?i)^not\b[^.;,]*?(?:[,;]\s*(?:but\s+)?|\bbut\s+)`)

// parenSecretRe finds the words that make a parenthetical carry a secret.
var parenSecretRe = regexp.MustCompile(`(?i)\b(?:contents?|values?|secrets?|tokens?|passwords?|passphras\w*|credentials?|body|text|data|api key|ssh key|private key|secret key|key itself|key material|raw bytes|full file|whole file|entire file|everything)\b`)

// keyAlgorithmWords before "key" make it a key TYPE ("RSA 4096 key"), not the
// key itself.
var keyAlgorithmWords = regexp.MustCompile(`^(?:rsa|dsa|ecdsa|ecdh|ed25519|ed448|aes|des|hmac|ec|ecc|openssh|pgp|gpg|sha-?\d+|md5|\d+(?:-?bit)?)$`)

// parenNamesSecret reports whether a parenthetical names a secret: a secret
// word, or a bare "key" ("raw key", "base64-encoded SSH key") that is not a
// key type ("RSA 4096 key") or followed by a metadata head ("key length").
func parenNamesSecret(low string) bool {
	if parenSecretRe.MatchString(low) {
		return true
	}
	words := phraseWords(low)
	for i, w := range words {
		if w != "key" && w != "keys" {
			continue
		}
		if i+1 < len(words) && secretMetadataWords.MatchString(words[i+1]) {
			continue
		}
		algo := false
		for _, p := range words[:i] {
			algo = algo || keyAlgorithmWords.MatchString(p)
		}
		if !algo {
			return true
		}
	}
	return false
}

// clauseDestRe finds a destination anywhere in a clause.
var clauseDestRe = regexp.MustCompile(`(?i)\b(?:in|into|as|via|through|to)\s+(?:the |a |an |its |this )?[\w"'` + "`" + `-]+ (?:field|parameter|param|argument|arg)\b`)

// clauseSecretRe finds the words that make a clause carry a secret.
var clauseSecretRe = regexp.MustCompile(`(?i)\b(?:contents?|values?|secrets?|tokens?|passwords?|passphras\w*|credentials?|body|text|data|private key|key itself|key material)\b`)

// clauseHoldsPlacement reports whether the clause starting at from places
// something (a destination) or names a secret. A new clause that does neither
// ("click submit", "close the file") is a plain action and ends the parse
// cleanly; one that does is something the parse cannot prove metadata-only.
func clauseHoldsPlacement(t string, from int) bool {
	rest := t[from:min(len(t), from+300)]
	if l := sentenceEnd.FindStringIndex(rest); l != nil {
		rest = rest[:l[0]]
	}
	if i := strings.IndexByte(rest, ';'); i >= 0 {
		rest = rest[:i]
	}
	return clauseDestRe.MatchString(rest) || clauseSecretRe.MatchString(rest)
}

// peekMeta reports whether the item starting at token k, up to the next
// separator or destination, is metadata ("the owner", "the number of lines",
// "the key's fingerprint").
func (p *placementToks) peekMeta(k int) bool {
	var words []string
	for n := k; len(words) < placementMaxItemWords; n++ {
		s, e, ok := p.at(n)
		if !ok {
			break
		}
		c := p.t[s]
		if c == ',' || c == ';' {
			break
		}
		if c == '(' {
			continue
		}
		w := p.word(n)
		if placementConjunctions[w] || placementTerminators[w] || w == "then" || placementDestRe.MatchString(p.t[s:]) {
			break
		}
		if w != "" {
			words = append(words, w)
		}
		if strings.ContainsAny(p.t[e-1:e], ".!?") {
			break
		}
	}
	return len(words) > 0 && payloadWordsAreMetadata(words, false)
}

// parsePlacementItems parses the payload items of the clause that starts at
// byte offset from of t. The clause ends at a sentence end, ";", a terminator
// word, or the second non-metadata item: only the first secret item and the
// metadata around it matter, which also keeps the scan linear.
//
// proven reports that the items up to a destination are positively metadata
// as far as the parse can tell: it is false whenever the parse met something
// it cannot place (an item longer than placementMaxItemWords, an unrecognized
// word after a separator) or never reached a destination. Callers blank a
// verb only when proven; any doubt keeps it.
func parsePlacementItems(t string, from int, inParen bool) (items []placementItem, proven bool) {
	toks := &placementToks{t: t, pos: from}
	var (
		words      []string
		wStart     = -1
		wEnd       int
		afterDest  bool
		sawConj    bool
		stop       bool
		secretSeen bool
		uncertain  bool
	)
	add := func(it placementItem) {
		items = append(items, it)
		if !it.meta {
			if secretSeen {
				stop = true
			}
			secretSeen = true
		}
	}
	flush := func() {
		if len(words) > 0 {
			add(placementItem{start: wStart, end: wEnd, meta: payloadWordsAreMetadata(words, inParen), destStart: -1, destEnd: -1})
		}
		words, wStart = nil, -1
	}
	for k := 0; !stop; k++ {
		ts, te, ok := toks.at(k)
		if !ok {
			break
		}
		tok := t[ts:te]
		switch tok[0] {
		case '(':
			// A parenthesized further item is parsed like the rest of the clause.
			// A qualifier ("(UTC)", "(RSA 4096)") is not an item: it counts only
			// when it opens with an inclusion word or names a secret. An
			// exclusion ("(not its contents)") is set aside, except "not just X
			// but Y", where Y is included.
			flush()
			inner := tok[1 : len(tok)-1]
			low := strings.ToLower(inner)
			subFrom := ts + 1
			if m := notSplitRe.FindStringIndex(low); m != nil {
				subFrom += m[1]
				low = low[m[1]:]
			} else if _, first := (&placementToks{t: inner}).nextWord(-1); exclusionLeadWords[first] || strings.HasSuffix(first, "n't") {
				continue
			}
			_, first := (&placementToks{t: low}).nextWord(-1)
			opensItem := placementConjunctions[first] || parenLeadWords[first] || followerStarters[first] || first == "it" || first == "the"
			if !opensItem && !parenNamesSecret(low) {
				continue
			}
			sub, _ := parsePlacementItems(t[:te-1], subFrom, true)
			for _, it := range sub {
				if !it.meta && !stop {
					it.destStart, it.destEnd = -1, -1
					add(it)
				}
			}
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
		w := toks.word(k)
		if w == "" {
			flush()
			break
		}
		switch {
		case placementConjunctions[w] || (inParen && parenLeadWords[w]):
			flush()
			sawConj = true
		case w == "then" || (w == "after" && !endsSentence && func() bool { _, n := toks.nextWord(k); return n == "that" }()):
			// "then"/"after that" separate items when a further one follows
			flush()
			at := k
			if w == "after" {
				at, _ = toks.nextWord(k)
			}
			j, nw := toks.nextWord(at)
			if j >= 0 && !toks.startsItem(j, nw) && !toks.peekMeta(j) {
				js, _, _ := toks.at(j)
				uncertain = uncertain || clauseHoldsPlacement(t, js)
				stop = true
				continue
			}
			k = at
			sawConj = true
		case placementTerminators[w]:
			flush()
			stop = true
		default:
			if m := placementDestRe.FindStringIndex(t[ts:]); m != nil && (len(words) > 0 || len(items) > 0) && !afterDest {
				// a destination: attach it to the item it closes
				flush()
				items[len(items)-1].destStart, items[len(items)-1].destEnd = ts, ts+m[1]
				for {
					s, _, ok := toks.at(k + 1)
					if !ok || s >= ts+m[1] {
						break
					}
					k++
				}
				afterDest, sawConj = true, false
				continue
			}
			if afterDest {
				// only a conjunction followed by a further payload item
				// continues the clause past a destination
				if !sawConj {
					stop = true
					continue
				}
				if exclusionLeadWords[w] || w == "nothing" {
					// "..., not its contents", "..., and nothing else"; but "not
					// just X but Y" includes Y
					if m := notButRe.FindStringIndex(t[ts:]); m != nil {
						for {
							s, _, ok := toks.at(k + 1)
							if !ok || s >= ts+m[1] {
								break
							}
							k++
						}
						afterDest = false
						continue
					}
					stop = true
					continue
				}
				if !toks.startsItem(k, w) && !toks.peekMeta(k) {
					uncertain = uncertain || clauseHoldsPlacement(t, ts)
					stop = true
					continue
				}
				afterDest = false
			}
			if len(words) >= placementMaxItemWords {
				// too long to be a metadata label: close it as a secret and
				// doubt the clause
				add(placementItem{start: wStart, end: wEnd, meta: false, destStart: -1, destEnd: -1})
				words, wStart = nil, -1
				uncertain = true
				if stop {
					continue
				}
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
	reached := false
	for _, it := range items {
		reached = reached || it.destStart >= 0
	}
	return items, reached && !uncertain
}

// chainedOutputVerbRe finds "return"/"log" (any inflection the normalizer
// leaves), which describe tool output ("returns its contents in the data
// field") and are placement only in the chained form below.
var chainedOutputVerbRe = regexp.MustCompile(`(?i)\b(return|log)s?\b`)

// chainedOutputTargetRe is the exfil-shaped target of a chained output verb:
// "to/into the <x> field", never "in/as the <x> field".
var chainedOutputTargetRe = regexp.MustCompile(`(?i)^[^.!?;]{0,80}?\b(?:to|into)\s+(?:the |a |an |its |this )?[\w"'` + "`" + `-]+ (?:field|parameter|param|argument|arg)\b`)

// rewriteChainedOutputVerbs turns a "return"/"log" that opens a LATER clause of
// a sentence already holding a placement verb, and whose target is to/into a
// field, into the placement verb "insert"/"add" (same length), so "save its
// size to the size field and return its contents to the notes field" is read
// as the second placement it is. A standalone tool-output description
// ("returns its contents in the data field") is left alone.
func rewriteChainedOutputVerbs(t string) string {
	var out []byte
	for _, l := range chainedOutputVerbRe.FindAllStringSubmatchIndex(t, -1) {
		start := 0
		if i := strings.LastIndexAny(t[:l[0]], ".!?;"); i >= 0 {
			start = i + 1
		}
		if !placementVerbRe.MatchString(t[start:l[0]]) || !chainedOutputTargetRe.MatchString(t[l[1]:]) {
			continue
		}
		if out == nil {
			out = []byte(t)
		}
		repl := "insert"
		if strings.EqualFold(t[l[2]:l[3]], "log") {
			repl = "add"
		}
		n := l[1] - l[0]
		rep := repl + strings.Repeat(" ", n-len(repl))
		copy(out[l[0]:l[1]], rep)
	}
	if out == nil {
		return t
	}
	return string(out)
}

// maskPlacementClauses rewrites placement clauses so the credential families
// see the secret item of a mixed clause right behind its verb, and never see
// the verb of a metadata-only clause. The result has the length of t.
func maskPlacementClauses(t string) string {
	t = rewriteChainedOutputVerbs(t)
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
		items, proven := parsePlacementItems(t, l[1], false)
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
			if !proven {
				continue // not proven metadata-only: keep the verb
			}
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
		items, _ := parsePlacementItems(t, l[1], false)
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
