package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// Shadowing is a HARD check that flags cross-server tool impersonation and
// reference (FR — shadowing). Two distinct attack shapes:
//
//  1. Impersonation clone: a tool whose name AND near-duplicate description
//     both match another server's tool (one impersonating the other so an
//     agent calls the wrong one). The description clone is the HARD evidence.
//     A name collision ALONE is never HARD: mcpproxy exists to unify many
//     servers, every tool is namespaced server:tool, and ordinary compound
//     names (list_models, search_issues…) legitimately collide across servers
//     — retrieve_tools' ranking is what disambiguates them, and no fixed
//     "distinctive name" heuristic can separate those from attacks (MCP-3520:
//     ElevenLabs vs kaggle list_models).
//     A BYTE-IDENTICAL definition (description and schemas) on another server
//     is not a clone either: that is one MCP server installed twice (two
//     filesystem roots, two GitHub orgs), carries no rewritten text, and is
//     exempt from both shape 1 and 1b (identicalDefinition).
//     1b. Name squat (SOFT, review-only; round-2 red team R2-5): a DISTINCTIVE
//     name that another server also exposes, with a DIFFERENT description.
//     Round 1 and round 2 both showed this is the one offline bypass that
//     stays open under the clone rule: an attacker keeps the victim's tool
//     name and rewrites the prose ("Fetch a file from disk…" for the
//     victim's filesystem_read). It is surfaced for review — it holds a
//     trust_mode: scan change and a first-seen baseline tool (R2-1 gate),
//     never auto-quarantines — UNLESS the description explains the overlap
//     by naming its own server (provenance: "Lists ElevenLabs voices and
//     models", "Browse Kaggle's model registry"), which is how honest servers
//     that share a compound name actually describe themselves and exactly
//     what an impersonator cannot afford to write. The MCP-3520 pair stays
//     clean under that rule; a squat on a name like filesystem_read with no
//     provenance in the text does not.
//  2. Cross-server reference: a tool whose description names a DISTINCTIVE
//     tool that lives ONLY on different servers (steering the agent's tool
//     selection). A name the tool's own server also exposes is ordinary
//     self-documentation, whoever else exposes it.
//
// The reference shape still requires the name to be distinctive: generic verbs
// ("search", "get", "list") appear in prose constantly. A tool referencing its
// OWN name is also ignored.
//
// TIERS — the clone shape is HARD. The reference shape is split by how the
// name is written (writtenAsToolName):
//
//   - HARD when the occurrence is written as a tool name: an identifier
//     (create_payment_intent, createInvoice), code-marked (`reason`, "reason",
//     reason(...)) or beside the word "tool"/"function".
//   - SOFT (review-only) when it is a plain word of the sentence.
//
// Why the split: distinctiveName() is a bare length-and-stop-list test applied
// to every identifier-shaped token in ordinary English prose, and prose is full
// of them. Google Cloud SQL's create_user description says "For this reason,
// you can't add two IAM users…"; another installed server exposes a tool named
// "reason"; a HARD verdict marked a Google API server dangerous over one word
// in one sentence. Patching the heuristic (stop-list additions, a length bump)
// is not the fix — every such patch guesses which English words happen to be
// tool names on some other install, and the next collision moves to the next
// word. The shape of the occurrence is what separates a named tool from a word.
//
// History: demoting the WHOLE reference shape to SOFT was approved on
// 2026-09-01 and deferred until scan reports carry a ruleset version, because a
// tier change only reaches an install on RESCAN (persisted reports store
// ThreatLevel and tier as data, and nothing invalidates a report when the
// ruleset changes). The plain-word half shipped after v0.76.0-rc.1, when the
// false positive above was hit in use; the identifier half stays HARD. The
// propagation limit still holds: an existing report keeps its old verdict until
// the server is scanned again.
//
// The span emitted below is independent of the tier: a genuine steering
// description and a coincidental word both point at the exact word.
type Shadowing struct{}

// shadowingConfidence rates both shapes. detect.aggregate SUMS confidences
// across a tool's signals, so an over-stated number does not just mislabel one
// signal — it inflates the combined confidence of every tool the check touches.
// The plain-word reference has its own, lower number (proseReferenceConfidence).
const shadowingConfidence = 0.85

// proseReferenceConfidence rates a reference that is only a plain word of a
// sentence (not written as a tool name). It is a token-membership test over
// English prose and the weakest evidence this check produces.
const proseReferenceConfidence = 0.3

// nameSquatConfidence rates the SOFT name-squat shape (1b). It sits well below
// shadowingConfidence: a bare name collision with different prose is weak
// evidence on its own, and detect.aggregate sums confidences per tool.
const nameSquatConfidence = 0.4

// ID implements detect.Check.
func (*Shadowing) ID() string { return "shadowing.cross_server" }

// commonNames are generic tool names whose collision/reference across servers is
// ordinary and must never be treated as shadowing.
var commonNames = map[string]struct{}{
	"search": {}, "get": {}, "list": {}, "read": {}, "write": {}, "fetch": {},
	"query": {}, "run": {}, "exec": {}, "call": {}, "create": {}, "update": {},
	"delete": {}, "add": {}, "remove": {}, "find": {}, "open": {}, "close": {},
	"send": {}, "load": {}, "save": {}, "echo": {}, "ping": {}, "status": {},
	"help": {}, "info": {}, "scan": {}, "check": {}, "test": {},
}

// distinctiveName reports whether a tool name is specific enough that a
// cross-server collision/reference is suspicious rather than coincidental.
// Distinctive = reasonably long and not a bare common verb.
func distinctiveName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if len(n) < 6 {
		return false
	}
	if _, common := commonNames[n]; common {
		return false
	}
	return true
}

// Inspect implements detect.Check. Cross-tool reasoning uses the RegistryView
// indexes built once per scan.
func (c *Shadowing) Inspect(tool detect.ToolView, reg detect.RegistryView) []detect.Signal {
	var sigs []detect.Signal

	// 1. Impersonation clone: same name on another server AND a near-duplicate
	// description. The clone is the evidence — a bare name coincidence is the
	// proxy's normal operating condition, not a finding.
	cloned := false
	var squatOf string // first other server sharing the name without a clone
	for _, other := range reg.ToolsByName[tool.Name] {
		if other.Server == tool.Server {
			continue
		}
		// A byte-identical definition (description AND both schemas) is a
		// second instance of the same server software — two filesystem roots,
		// two GitHub orgs — not an impersonation. It is neither clone evidence
		// nor a squat candidate; see identicalDefinition.
		if identicalDefinition(tool, other) {
			continue
		}
		if cloneDescriptions(tool.Description, other.Description) {
			sigs = append(sigs, detect.Signal{
				CheckID:    c.ID(),
				Tier:       detect.TierHard,
				ThreatType: detect.ThreatToolPoisoning,
				Confidence: shadowingConfidence,
				Evidence:   detect.CapEvidence(fmt.Sprintf("tool %q duplicates server %q's tool of the same name, description included", tool.Name, other.Server)),
				// No Spans, deliberately. The clone evidence is a property of the
				// description as a WHOLE (token-set containment against another
				// server's description), not a located substring — cloneDescriptions
				// never identifies an offset, and the shared tokens are scattered
				// across both texts. Marking an arbitrary word, or the whole
				// description, would claim a precision this check does not have; the
				// UI's honest fallback for a span-less finding already exists.
				Detail: fmt.Sprintf("Tool %q clones server %q's tool — same name and near-identical description — possible impersonation.", tool.Name, other.Server),
			})
			cloned = true
			break // one clone signal is enough
		}
		if squatOf == "" {
			squatOf = other.Server
		}
	}

	// 1b. Name squat: the name is shared with another server, the description
	// is GENUINELY different (not a clone, and not identical text too short to
	// evidence one — "Create." == "Create." is neither shape), the name is
	// distinctive, and the description does not name its own server.
	// Review-only (SOFT); see the type comment.
	if !cloned && squatOf != "" && distinctiveName(tool.Name) && !mentionsOwnServer(tool) &&
		!sameTokens(tool.Description, reg.ToolsByName[tool.Name], squatOf) {
		sigs = append(sigs, detect.Signal{
			CheckID:    c.ID(),
			Tier:       detect.TierSoft,
			ThreatType: detect.ThreatToolPoisoning,
			Confidence: nameSquatConfidence,
			Evidence:   detect.CapEvidence(fmt.Sprintf("tool %q shares its name with server %q's tool but describes it differently", tool.Name, squatOf)),
			// No Spans: like the clone shape, the evidence is the (server, name)
			// pair against the whole description, not a located substring.
			Detail: fmt.Sprintf("Tool %q has the same name as server %q's tool with a different description — possible name squat (an impersonator keeps the trusted name and rewrites the prose). Review which one you intend to expose; mentioning the owning server in the description clears this.", tool.Name, squatOf),
		})
	}

	sigs = append(sigs, c.referenceSignals(tool, reg)...)
	return sigs
}

// identicalDefinition reports whether two same-named tools on different
// servers are the SAME definition: byte-equal raw description and
// semantically equal input and output schemas (JSON compared after decoding,
// so key order and whitespace do not matter; an absent schema equals only an
// absent schema). This is what two instances of one MCP server look like,
// and it is exempt from both shadowing shapes (owner decision, 2026-11-07,
// v0.73.0-rc.1 gate): the clone shape exists to catch a copy with injected or
// rewritten prose, and an identical copy has none — the poisoned text the
// scanner guards against is absent by construction, and server:tool
// namespacing keeps the two reachable apart. The remaining risk, an agent
// picking the other server's instance, is the retrieve_tools provenance and
// admission-quarantine story, not a definition finding. Any edit at all —
// one word, one schema property — leaves this exemption and is judged by
// cloneDescriptions as before.
func identicalDefinition(a, b detect.ToolView) bool {
	if a.Description != b.Description {
		return false
	}
	return equalJSON(a.InputSchema, b.InputSchema) && equalJSON(a.OutputSchema, b.OutputSchema)
}

// equalJSON compares two raw JSON documents structurally. Empty and literal
// null are the same absence; a document that does not decode is compared
// byte-for-byte, so malformed input can only ever make two schemas unequal.
func equalJSON(a, b json.RawMessage) bool {
	ta, tb := bytes.TrimSpace(a), bytes.TrimSpace(b)
	if len(ta) == 0 || bytes.Equal(ta, []byte("null")) {
		ta = nil
	}
	if len(tb) == 0 || bytes.Equal(tb, []byte("null")) {
		tb = nil
	}
	if ta == nil || tb == nil {
		return ta == nil && tb == nil
	}
	var va, vb any
	if json.Unmarshal(ta, &va) != nil || json.Unmarshal(tb, &vb) != nil {
		return bytes.Equal(ta, tb)
	}
	return reflect.DeepEqual(va, vb)
}

// sameTokens reports whether desc has exactly the token set of the colliding
// tool on server other — identical prose modulo case/punctuation, which the
// clone shape already judged too short to be evidence and the squat shape must
// not re-label as "describes it differently".
func sameTokens(desc string, owners []detect.ToolView, other string) bool {
	mine := descTokens(desc)
	for _, o := range owners {
		if o.Server != other {
			continue
		}
		theirs := descTokens(o.Description)
		if len(theirs) != len(mine) {
			return false
		}
		for tok := range mine {
			if _, ok := theirs[tok]; !ok {
				return false
			}
		}
		return true
	}
	return false
}

// mentionsOwnServer reports whether a tool's description names the server it
// belongs to — the provenance that explains an honest name collision ("Lists
// ElevenLabs voices and models" on server "elevenlabs"). Every letter/digit
// run of the server name (split on "-", "_", ".", spaces, case-folded) of
// three or more characters counts; a run shorter than that ("ai", "db") is
// too common a token to be provenance. The description is tokenized the same
// way, so "Kaggle's" and "kaggle-mcp" both match the server token "kaggle".
func mentionsOwnServer(tool detect.ToolView) bool {
	serverToks := nameTokens(tool.Server)
	if len(serverToks) == 0 {
		return false
	}
	descToks := nameTokens(tool.Description)
	for tok := range serverToks {
		if _, ok := descToks[tok]; ok {
			return true
		}
	}
	return false
}

// nameTokens splits s into its lowercased letter/digit runs of three or more
// characters, dropping generic connective words that appear in server names
// without identifying anyone ("mcp", "server", "tool(s)", "api").
func nameTokens(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, tok := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(tok)) < 3 {
			continue
		}
		switch tok {
		case "mcp", "server", "tool", "tools", "api", "the", "and", "for", "with", "from", "local", "remote", "official":
			continue
		}
		out[tok] = struct{}{}
	}
	return out
}

// cloneDescriptions reports whether two descriptions are near-duplicates after
// normalization — the impersonation-clone evidence. Deterministic token-set
// containment: cosmetic edits (case, punctuation, whitespace, word order) do
// not launder a copy, while genuinely different descriptions of a shared name
// stay far below the threshold.
//
// Accepted, deliberate limits (owner decision, MCP-3520):
//   - An attacker who writes a genuinely DIFFERENT description for a colliding
//     name is out of THIS (hard) shape's scope — by name alone that case is
//     indistinguishable from two honest servers sharing a compound name
//     (list_models on every model host), which is the proxy's normal
//     condition. Since the round-2 red team (R2-5) that case is surfaced for
//     review by the SOFT name-squat shape (1b) in Inspect, unless the
//     description names its own server; the other defenses there remain
//     admission quarantine for new servers, server:tool namespacing, and
//     server provenance in retrieve_tools.
//   - Descriptions with fewer than three tokens carry too little information
//     to distinguish a clone from a coincidence ("Create" == "Create" says
//     nothing) and never match; empty descriptions likewise.
func cloneDescriptions(a, b string) bool {
	const minTokens = 3
	ta, tb := descTokens(a), descTokens(b)
	if len(ta) < minTokens || len(tb) < minTokens {
		return false
	}
	shared := 0
	for tok := range ta {
		if _, ok := tb[tok]; ok {
			shared++
		}
	}
	smaller, larger := len(ta), len(tb)
	if smaller > larger {
		smaller, larger = larger, smaller
	}
	// Both directions matter: containment of the smaller set catches a copy
	// with words bolted on, while the larger-set floor keeps a short generic
	// sentence from "matching" a long one that merely contains its words.
	return float64(shared) >= 0.85*float64(smaller) && float64(shared) >= 0.7*float64(larger)
}

// descTokens lowercases and splits a description into its letter/digit tokens.
// Unicode-aware on purpose: a Cyrillic description must tokenize to real
// words, not to an empty set that can never evidence a clone. Runs of
// spaceless scripts (Han, kana, Hangul, Thai) have no word boundaries for
// FieldsFunc to find — an informative sentence would collapse to ONE token
// and duck under the floor — so those are emitted as character bigrams, the
// standard segmentation-free indexing unit for CJK text.
func descTokens(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, tok := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		runes := []rune(tok)
		if len(runes) >= 2 && isSpacelessScript(runes) {
			for i := 0; i+1 < len(runes); i++ {
				out[string(runes[i:i+2])] = struct{}{}
			}
			continue
		}
		out[tok] = struct{}{}
	}
	return out
}

// spacelessScripts are writing systems that do not separate words with spaces.
var spacelessScripts = []*unicode.RangeTable{
	unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul, unicode.Thai,
}

// isSpacelessScript reports whether a token is written mostly in a script
// with no word separators, and therefore needs bigram tokenization.
func isSpacelessScript(runes []rune) bool {
	hits := 0
	for _, r := range runes {
		for _, tbl := range spacelessScripts {
			if unicode.Is(tbl, r) {
				hits++
				break
			}
		}
	}
	return hits*2 > len(runes)
}

// wordRe extracts identifier-like tokens (incl. snake_case / camelCase words)
// from a description for reference matching.
var wordRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_]{5,}`)

// referenceSignals flags a description that names a distinctive tool living on a
// different server. A reference to the tool's own name is ignored.
func (c *Shadowing) referenceSignals(tool detect.ToolView, reg detect.RegistryView) []detect.Signal {
	// FindAllStringIndex, not FindAllString: the byte offsets are what become the
	// UI's highlight, and recovering them afterwards with strings.Index would be
	// a coin flip on any description that repeats the token.
	locs := wordRe.FindAllStringIndex(tool.Description, -1)
	seen := make(map[string]struct{})
	// A name can occur more than once, and the occurrences can differ in how
	// they are written ("For this reason, ... use the reason tool"). The signal
	// for a name is HARD when ANY occurrence is written as a tool name, and its
	// span marks the first such occurrence; otherwise it is SOFT on the first.
	namedAt := make(map[string][2]int)
	for _, loc := range locs {
		tok := tool.Description[loc[0]:loc[1]]
		if _, done := namedAt[tok]; done {
			continue
		}
		// The same cheap filters as the loop below, first: a self-reference and
		// a name that is unknown or not distinctive never produce a signal, so
		// no shape work is spent on them however often they repeat.
		if tok == tool.Name || !distinctiveName(tok) {
			continue
		}
		if _, known := reg.ToolsByName[tok]; known && writtenAsToolName(tool.Description, loc[0], loc[1]) {
			namedAt[tok] = [2]int{loc[0], loc[1]}
		}
	}
	var sigs []detect.Signal
	for _, loc := range locs {
		tok := tool.Description[loc[0]:loc[1]]
		if tok == tool.Name {
			continue // self-reference
		}
		if _, dup := seen[tok]; dup {
			continue
		}
		owners, ok := reg.ToolsByName[tok]
		if !ok || !distinctiveName(tok) {
			continue
		}
		// Only flag when the referenced tool lives EXCLUSIVELY on different
		// servers. A name the tool's own server also exposes is ordinary
		// self-documentation ("call list_models first") — that another server
		// happens to expose the same name is the proxy's normal condition,
		// not steering. Accepted corner (owner decision, MCP-3520): a server
		// can silence this branch for itself by exposing a decoy tool under
		// the referenced name — but steering an agent toward a name the
		// server itself exposes reduces to the name-coincidence case above,
		// with the same defenses (admission quarantine, namespacing).
		onOtherServer, onOwnServer := false, false
		for _, o := range owners {
			if o.Server == tool.Server {
				onOwnServer = true
			} else {
				onOtherServer = true
			}
		}
		if !onOtherServer || onOwnServer {
			continue
		}
		// One signal per name: later occurrences are skipped by `seen` above. Its
		// span is the first occurrence written as a tool name when there is one
		// (namedAt), else this first occurrence.
		seen[tok] = struct{}{}
		named, isNamed := namedAt[tok]
		if isNamed {
			loc = named[:]
		}
		if !isNamed {
			// An ordinary word in a sentence that happens to be another server's
			// tool name ("For this reason, ..."). Review-only; see the TIERS note.
			sigs = append(sigs, detect.Signal{
				CheckID:    c.ID(),
				Tier:       detect.TierSoft,
				ThreatType: detect.ThreatToolPoisoning,
				Confidence: proseReferenceConfidence,
				Evidence:   detect.CapEvidence(fmt.Sprintf("description contains the word %q, also the name of a tool on another server", tok)),
				Detail:     fmt.Sprintf("Tool %q description contains the word %q, which is also the name of a tool on another server. In ordinary prose this is a coincidence; review it if the sentence tells the agent to use that tool.", tool.Name, tok),
				Spans:      detect.DescriptionSpan(c.ID(), detect.TierSoft, tool.Description, loc[0], loc[1]),
			})
			continue
		}
		sigs = append(sigs, detect.Signal{
			CheckID:    c.ID(),
			Tier:       detect.TierHard,
			ThreatType: detect.ThreatToolPoisoning,
			Confidence: shadowingConfidence,
			Evidence:   detect.CapEvidence(fmt.Sprintf("description references cross-server tool %q", tok)),
			Detail:     fmt.Sprintf("Tool %q description steers the agent toward another server's tool %q.", tool.Name, tok),
			Spans:      detect.DescriptionSpan(c.ID(), detect.TierHard, tool.Description, loc[0], loc[1]),
		})
	}
	return sigs
}

// writtenAsToolName reports whether the occurrence desc[start:end] is written
// the way a description names a tool, as opposed to an ordinary word of the
// sentence. It is an allow-list of SHAPES, judged on the raw text:
//
//   - the token itself is identifier-shaped: it has an underscore or a digit,
//     or a capital after a lower-case letter (create_user, search2, createUser);
//   - it is marked as code: enclosed in a pair of backticks or quotes (ASCII or
//     typographic), or followed directly by "(";
//   - it is qualified with a server, the proxy's own spelling (server:reason);
//   - the word "tool" or "function" stands directly beside it as a whole word
//     ("the reason tool", "tool reason"), across white space on the same line
//     (fewer than neighbourRuneLimit characters of it).
//
// Everything else is a plain word and is reported as SOFT. The list only ever
// RAISES an occurrence to HARD, so a miss costs a tier and never silence: a
// description that steers toward a plain-word tool name without any of these
// marks is still surfaced for review.
//
// Accepted limit: the noun rule cannot read grammar. "Explains the reason tool
// calls fail" is prose and reads HARD. The plural ("the reason tools are
// unavailable") is left out of the rule because a name is not followed by a
// plural noun; the singular stays, since "the X tool" is the commonest way a
// description names a tool.
func writtenAsToolName(desc string, start, end int) bool {
	before, beforeSize := utf8.DecodeLastRuneInString(desc[:start])
	after, afterSize := utf8.DecodeRuneInString(desc[end:])
	// The tokenizer is ASCII: a letter, digit or combining mark it did not
	// consume on either side means the token is a fragment of a longer word
	// (reasón, 2reason, reasonné). A fragment is not a written tool name.
	if (beforeSize > 0 && isIdentRune(before)) || (afterSize > 0 && isIdentRune(after)) {
		return false
	}
	tok := desc[start:end]
	sawLower := false
	for _, r := range tok {
		switch {
		case r == '_' || unicode.IsDigit(r):
			return true
		case unicode.IsUpper(r) && sawLower:
			return true
		case unicode.IsLower(r):
			sawLower = true
		}
	}
	if after == '(' {
		return true
	}
	if beforeSize > 0 && quotePair(before, after) {
		return true
	}
	// server:tool. The qualifier must be there: a colon after a space, a bracket,
	// a quote or other punctuation, or at the start of the text, is punctuation.
	if before == ':' {
		if q, size := utf8.DecodeLastRuneInString(desc[:start-beforeSize]); size > 0 && isQualifierEnd(q) {
			return true
		}
	}
	return isToolNoun(wordBefore(desc, start)) || isToolNoun(wordAfter(desc, end))
}

// quotePair reports whether open and closing enclose a quoted word: the same
// ASCII mark on both sides, or a typographic opener with a closer its writing
// conventions use („reason“ and „reason” are both in use).
func quotePair(open, closing rune) bool {
	switch open {
	case '`', '"', '\'':
		return closing == open
	case '\u201c': // “
		return closing == '\u201d'
	case '\u201e': // „
		return closing == '\u201c' || closing == '\u201d'
	case '\u2018': // ‘
		return closing == '\u2019'
	case '\u201a': // ‚
		return closing == '\u2018' || closing == '\u2019'
	case '\u00ab': // «reason»
		return closing == '\u00bb'
	case '\u00bb': // »reason«
		return closing == '\u00ab'
	}
	return false
}

// isQualifierEnd reports whether r can be the last character of a server name
// in front of the ":" of server:tool. A server name may hold anything but ":",
// so this excludes only what a sentence puts before a colon that is
// punctuation: white space, brackets, quotes, and other separators.
func isQualifierEnd(r rune) bool {
	if isIdentRune(r) {
		return true
	}
	switch r {
	case ':', ',', ';', '!', '?':
		return false
	}
	if unicode.IsSpace(r) || unicode.In(r, unicode.Ps, unicode.Pe, unicode.Pi, unicode.Pf) {
		return false
	}
	switch r {
	case '"', '\'', '`':
		return false
	}
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

func isToolNoun(w string) bool {
	switch strings.ToLower(w) {
	case "tool", "function":
		return true
	}
	return false
}

// isIdentRune reports whether r can be part of an identifier-like word. A
// combining mark belongs to the letter before it, so it continues the word.
func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r)
}

// isInlineSpace reports whether r separates words on one line: any Unicode
// space (the no-break space included) that is not a line break of any kind.
func isInlineSpace(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', '\u0085', '\u2028', '\u2029':
		return false
	}
	return unicode.IsSpace(r)
}

// neighbourRuneLimit bounds how far wordBefore/wordAfter look, so a hostile
// description cannot make the neighbour scan long.
const neighbourRuneLimit = 32

// wordBefore returns the whole word that ends just before i, separated from it
// by inline white space only. It is "" when there is no white space, when
// another character intervenes, when the scan limit is reached, or when the
// word is only the tail of a longer identifier (multi_tool).
func wordBefore(desc string, i int) string {
	j, n := i, 0
	for j > 0 && n < neighbourRuneLimit {
		r, size := utf8.DecodeLastRuneInString(desc[:j])
		if !isInlineSpace(r) {
			break
		}
		j -= size
		n++
	}
	if j == i || n == neighbourRuneLimit {
		return ""
	}
	k, n := j, 0
	for k > 0 && n < neighbourRuneLimit {
		r, size := utf8.DecodeLastRuneInString(desc[:k])
		if !unicode.IsLetter(r) {
			break
		}
		k -= size
		n++
	}
	if k > 0 {
		if r, _ := utf8.DecodeLastRuneInString(desc[:k]); isIdentRune(r) {
			return ""
		}
	}
	return desc[k:j]
}

// wordAfter is the mirror of wordBefore (tools_enabled is not the word "tools").
func wordAfter(desc string, i int) string {
	j, n := i, 0
	for j < len(desc) && n < neighbourRuneLimit {
		r, size := utf8.DecodeRuneInString(desc[j:])
		if !isInlineSpace(r) {
			break
		}
		j += size
		n++
	}
	if j == i || n == neighbourRuneLimit {
		return ""
	}
	k, n := j, 0
	for k < len(desc) && n < neighbourRuneLimit {
		r, size := utf8.DecodeRuneInString(desc[k:])
		if !unicode.IsLetter(r) {
			break
		}
		k += size
		n++
	}
	if k < len(desc) {
		if r, _ := utf8.DecodeRuneInString(desc[k:]); isIdentRune(r) {
			return ""
		}
	}
	return desc[j:k]
}
