package checks

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// CapabilityMismatch is a SOFT check (FR-009, US2) that flags a gap between what
// a tool *declares* it does and what it *implies* it touches:
//
//   - Declared-vs-implied: a tool whose declared purpose is pure computation or
//     string manipulation (name/lead like "add", "to_uppercase") that
//     nevertheless references a sensitive resource it has no business touching
//     (~/.ssh, /etc/passwd, an external URL, a shell). A calculator reading
//     id_rsa is a classic capability-mismatch exfiltration tell.
//   - Unexplained data-sink param: a free-form input named like an exfiltration
//     channel ("sidenote", "scratchpad") that the description never explains —
//     the model is steered to stuff stolen data into it.
//
// The declared category is taken from the tool NAME and its leading sentence,
// NOT the full description, so an attacker's benign cover sentence still anchors
// the declaration while the smuggled access in the rest of the text is treated
// as implied. Tools that legitimately declare file/network/system access are
// therefore NOT flagged for touching those resources (FR-009 MUST-NOT).
//
// Being soft, a hit raises a finding for review and never auto-quarantines.
type CapabilityMismatch struct{}

// ID implements detect.Check.
func (*CapabilityMismatch) ID() string { return "capability.mismatch" }

const (
	mismatchConfidence = 0.55
	dataSinkConfidence = 0.5
)

// Category keyword sets. IO categories (file/network/system) take precedence so
// a tool that genuinely declares resource access is never flagged for using it.
var (
	fileWords    = []string{"file", "path", "dir", "folder", "read", "write", "load", "save", "open", "document", "filesystem"}
	networkWords = []string{"http", "url", "fetch", "download", "upload", "request", "web", "api", "curl", "wget"}
	systemWords  = []string{"exec", "shell", "command", "process", "terminal", "spawn", "subprocess", "script"}
	computeWords = []string{"add", "sum", "subtract", "minus", "multiply", "divide", "calc", "math", "arithmetic", "average", "count", "modulo", "power", "sqrt", "mean", "round", "compute"}
	stringWords  = []string{"string", "upper", "lower", "concat", "reverse", "trim", "replace", "encode", "decode", "length", "substring", "split", "join", "format", "case", "slug"}
)

// sensitiveMarkers are concrete resource references a pure compute/string tool
// has no reason to touch. Written to match NORMALIZED text (lowercased, lightly
// stemmed — e.g. ".aws/credentials" → ".aws/credential").
var sensitiveMarkers = []string{
	".ssh", "id_rsa", "id_ed25519", "/etc/passwd", "/etc/shadow", ".aws/credential",
	".aws", "private key", "keychain", ".netrc", ".npmrc", ".git-credential",
	"authorized_key", ".pgpass", "kube/config", "/.config/gcloud",
	"http://", "https://", "/bin/sh", "/bin/bash", "subprocess", "exfiltrat",
}

// sinkParamNames are input parameter names that read as free-form exfiltration
// channels rather than genuine tool inputs.
var sinkParamNames = map[string]struct{}{
	"sidenote": {}, "side_note": {}, "scratchpad": {}, "scratch": {},
	"thoughts": {}, "thought": {}, "reasoning": {}, "memo": {}, "exfil": {},
	"secret_note": {}, "debug_info": {}, "extra_context": {}, "notes_to_self": {},
	"hidden_note": {}, "annotation": {}, "annotations": {},
}

// Inspect implements detect.Check. It emits at most one signal per tool,
// preferring the capability-mismatch signal over an unexplained data-sink.
func (c *CapabilityMismatch) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	declared := declaredCategory(tool)
	text := tool.NormalizedText

	// Declared-vs-implied mismatch: a compute/string tool touching a sensitive
	// resource.
	if declared == "compute" || declared == "string" {
		if marker, ok := firstMarker(text); ok {
			return []detect.Signal{{
				CheckID:    c.ID(),
				Tier:       detect.TierSoft,
				ThreatType: detect.ThreatExfiltration,
				Confidence: mismatchConfidence,
				Evidence:   detect.CapEvidence(marker),
				Detail: fmt.Sprintf("Tool declares a %s capability yet references %q — a resource it has no declared reason to access.",
					declared, marker),
			}}
		}
	}

	// Unexplained data-sink parameter.
	if param, byShape, ok := unexplainedSinkParam(tool); ok {
		detail := fmt.Sprintf("Input parameter %q reads as a free-form data sink and is never explained in the description — a likely exfiltration channel.",
			param)
		if byShape {
			detail = fmt.Sprintf("Input parameter %q reads as a free-form data sink: its own description asks for the user's conversation, prompt or environment and the tool description never explains it.",
				param)
		}
		return []detect.Signal{{
			CheckID:    c.ID(),
			Tier:       detect.TierSoft,
			ThreatType: detect.ThreatExfiltration,
			Confidence: dataSinkConfidence,
			Evidence:   detect.CapEvidence(param),
			Detail:     detail,
		}}
	}

	return nil
}

// declaredCategory infers the tool's declared purpose from its name first, then
// its leading sentence. Returns "" when unknown.
func declaredCategory(tool detect.ToolView) string {
	if cat := categoryFromText(strings.ToLower(tool.Name)); cat != "" {
		return cat
	}
	lead := strings.ToLower(tool.Description)
	if i := strings.IndexByte(lead, '.'); i > 0 {
		lead = lead[:i]
	}
	return categoryFromText(lead)
}

// categoryFromText classifies free text into a capability category. IO
// categories are checked first so they win over an incidental compute word.
func categoryFromText(s string) string {
	switch {
	case containsAny(s, fileWords):
		return "file"
	case containsAny(s, networkWords):
		return "network"
	case containsAny(s, systemWords):
		return "system"
	case containsAny(s, computeWords):
		return "compute"
	case containsAny(s, stringWords):
		return "string"
	default:
		return ""
	}
}

func containsAny(hay string, subs []string) bool {
	for _, s := range subs {
		if strings.Contains(hay, s) {
			return true
		}
	}
	return false
}

// firstMarker returns the first sensitive marker present in text, scanning in
// declaration order for determinism.
func firstMarker(text string) (string, bool) {
	for _, m := range sensitiveMarkers {
		if strings.Contains(text, m) {
			return m, true
		}
	}
	return "", false
}

// unexplainedSinkParam returns the first (alphabetically) input parameter whose
// name reads as a data sink AND is not mentioned in the description. Parsing is
// total: a malformed schema yields no parameters rather than an error.
// byShape is true when the parameter was flagged by its own description (RT-04
// shape path) rather than by its name.
func unexplainedSinkParam(tool detect.ToolView) (name string, byShape, ok bool) {
	if len(tool.InputSchema) == 0 {
		return "", false, false
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema, &doc); err != nil {
		return "", false, false
	}
	names := make([]string, 0, len(doc.Properties))
	for name := range doc.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	desc := strings.ToLower(tool.Description)
	for _, name := range names {
		if _, isSink := sinkParamNames[strings.ToLower(name)]; !isSink {
			continue
		}
		// "Explained" = the description references the param name. Checked against
		// the description only (NOT the schema, which always contains the name).
		if mentionsParam(desc, name) {
			continue
		}
		return name, false, true
	}
	// RT-04: the name list is trivially evaded by a synonym ("sidenote" →
	// "marginalia"), so a parameter is also a sink by SHAPE: a free-text
	// (string or untyped) input the description never mentions, whose own
	// description asks for data to be kept alongside the operation or for the
	// user's conversation, prompt or environment rather than for an input of
	// the operation itself.
	normDesc := detect.Normalize(tool.Description)
	for _, name := range names {
		if mentionsParam(desc, name) {
			continue
		}
		var prop struct {
			Type        any    `json:"type"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(doc.Properties[name], &prop); err != nil {
			continue
		}
		if !freeTextType(prop.Type) || prop.Description == "" {
			continue
		}
		// Line by line: a line break ends a phrase ("the system\nPrompt editor").
		for _, line := range strings.FieldsFunc(prop.Description, isLineBreak) {
			pd := detect.Normalize(line)
			if sinkSideChannel.MatchString(pd) {
				return name, true, true
			}
			// Every harvest phrase must be explained: an explained one
			// ("conversation history" on a chat summarizer) must not mask
			// another ("plus your system prompt") in the same line.
			envExempt := envPrecedenceExemptions(line, pd)
			for _, loc := range sinkHarvest.FindAllStringIndex(pd, -1) {
				m := pd[loc[0]:loc[1]]
				if strings.HasPrefix(m, "environment variabl") && envExempt[loc[0]] {
					continue
				}
				if !strings.Contains(normDesc, harvestNoun(m)) {
					return name, true, true
				}
			}
		}
	}
	return "", false, false
}

// The env-var precedence exemption (#1845) is an allow-list over the WHOLE
// sentence that holds the "environment variable" phrase: the sentence must be
// nothing but a configuration-precedence statement. Anything else in the
// sentence (a request to send, include or paste, an instruction after a
// colon or semicolon, a lead-in that is not on the list) leaves the phrase
// flagged. No verbs or variable names are denied; the sentence is simply
// required to have exactly one of these two shapes:
//
//	[lead] CUE [the|a|your] [value of [the]] environment variable(s) IDENT [tail]
//	[lead] CUE [the|a|your] [value of [the]] IDENT environment variable(s) [tail]
//
// CUE is a STRONG precedence cue (weak cues such as see, read from, same as,
// set via also introduce requests and are absent). IDENT is an UPPER_SNAKE
// identifier with at least one underscore (a plain word such as TEAM does not
// count), optionally wrapped in backticks or quotes or prefixed with $, and is
// case-sensitive: Normalize lowercases, so the sentence is matched on the raw
// line. A sentence is not split at a "." that is followed by a lowercase word
// ("e.g. instead of ...").
const (
	envCue  = `(?i:overrides?|overriding|overridden by|defaults? to|defaulted from|falls? back to|fallback to|takes precedence over|instead of)`
	envLead = `(?:(?i:\(optional\)|optional:?)\s+)?` + envCond + `(?:(?i:use|set|provide|specify)\s+(?i:this)\s+)?`
	// A leading condition clause ("If omitted, ...", "When not set, ...").
	envCond = `(?:(?i:if|when|unless)\s+(?:(?i:it|this) (?i:is|was)\s+|(?i:they) (?i:are)\s+)?(?:(?i:not)\s+)?` + envState + `,?\s+)?`
	// A trailing example parenthetical ("(e.g. PROJ,OPS)"); any other
	// parenthetical leaves the sentence flagged.
	envExample = `(?:\s*\((?i:e\.?g\.?|for example|example|examples|default)[^()]{0,60}\))?`
	envDet     = `(?:(?i:the|an?|your)\s+)?(?:(?i:value of)\s+(?:(?i:the)\s+)?)?`
	envIdent   = "[`'\"$]?[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+[`'\"]?"
	envIdents  = envIdent + `(?:\s*(?:,|(?i:and|or))\s*` + envIdent + `)*`
	envNoun    = `(?i:environment variabl(?:e|es))`
	envState   = `(?i:provided|set|given|specified|supplied|present|defined|omitted|missing|empty|unset|available)`
	envTail    = `(?:,?\s+(?i:if|when|unless|where)\s+(?:(?i:it|this) (?i:is|was)\s+|(?i:they) (?i:are)\s+)?(?:(?i:not)\s+)?` + envState + `)?` + envExample + `\s*[.!?]?\s*$`
)

// envExemptMaxLine bounds the per-line sentence scan: a longer line gets no
// exemption at all (it stays flagged), so a parameter description made of
// thousands of repeated phrases cannot make Inspect super-linear.
const envExemptMaxLine = 4096

var (
	envSentenceForward = regexp.MustCompile(`^\s*` + envLead + envCue + `\s+` + envDet + envNoun + `\s+` + envIdents + envTail)
	envSentenceReverse = regexp.MustCompile(`^\s*` + envLead + envCue + `\s+` + envDet + envIdents + `\s+` + envNoun + envTail)
)

// envPhraseRaw finds the phrase in the raw line the same way sinkHarvest does
// in normalized text (single space, stemmed tail).
var envPhraseRaw = regexp.MustCompile(`(?i)\benvironment variabl\w*`)

// sentenceEnds returns, in one pass, the byte offsets just past every
// sentence end in raw. A sentence ends at '!' or '?' or at a '.' followed by
// end of text or by whitespace and then a non-lowercase character ("e.g. x"
// does not split).
func sentenceEnds(raw string) []int {
	var ends []int
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '!', '?':
			ends = append(ends, i+1)
		case '.':
			if abbreviationDot(raw, i) {
				continue // "e.g." / "i.e." never end a sentence
			}
			j := i + 1
			for j < len(raw) {
				r, n := utf8.DecodeRuneInString(raw[j:])
				if !unicode.IsSpace(r) {
					break
				}
				j += n
			}
			if j == len(raw) {
				ends = append(ends, i+1)
				continue
			}
			if j == i+1 {
				continue // no whitespace after the dot
			}
			if r, _ := utf8.DecodeRuneInString(raw[j:]); !unicode.IsLower(r) {
				ends = append(ends, i+1)
			}
		}
	}
	return ends
}

// abbreviationDot reports whether the '.' at raw[i] closes a single-letter
// abbreviation part ("e.g.", "i.e."): the previous byte is a letter that is
// itself preceded by '.', whitespace, '(' or the start of the line.
func abbreviationDot(raw string, i int) bool {
	if i < 1 || !unicode.IsLetter(rune(raw[i-1])) {
		return false
	}
	if i == 1 {
		return true
	}
	switch raw[i-2] {
	case '.', ' ', '\t', '(':
		return true
	}
	return false
}

// sentenceBounds returns the byte range of the sentence containing raw[at],
// given the precomputed sentence ends.
func sentenceBounds(raw string, ends []int, at int) (int, int) {
	start := 0
	for _, e := range ends {
		if e > at {
			return start, e
		}
		start = e
	}
	return start, len(raw)
}

// envPrecedenceExemptions returns the byte offsets (in the normalized line pd)
// of "environment variable" sinkHarvest matches that document config
// precedence, e.g. "Overrides the environment variable JIRA_PROJECTS_FILTER".
// A match is exempt ONLY when the raw sentence holding it is a pure precedence
// statement of one of the two shapes above and holds no other such phrase.
// If the raw and normalized lines disagree about how many such phrases there
// are (homoglyphs, double spaces, format runes), nothing in the line is
// exempt. This is a soft, low-confidence signal: a precedence statement
// naming a token variable is still configuration documentation.
func envPrecedenceExemptions(raw, pd string) map[int]bool {
	if len(raw) > envExemptMaxLine {
		return nil
	}
	normLocs := sinkHarvest.FindAllStringIndex(pd, -1)
	var envNorm [][]int
	for _, loc := range normLocs {
		if strings.HasPrefix(pd[loc[0]:loc[1]], "environment variabl") {
			envNorm = append(envNorm, loc)
		}
	}
	rawLocs := envPhraseRaw.FindAllStringIndex(raw, -1)
	if len(envNorm) == 0 || len(envNorm) != len(rawLocs) {
		return nil
	}
	out := make(map[int]bool, len(envNorm))
	ends := sentenceEnds(raw)
	for i, rl := range rawLocs {
		start, end := sentenceBounds(raw, ends, rl[0])
		if len(envPhraseRaw.FindAllStringIndex(raw[start:end], -1)) != 1 {
			continue
		}
		sentence := raw[start:end]
		if envSentenceForward.MatchString(sentence) || envSentenceReverse.MatchString(sentence) {
			out[envNorm[i][0]] = true
		}
	}
	return out
}

// mentionsParam reports whether the lowercased tool description names the
// parameter as a whole word, i.e. not glued to a letter, digit or underscore
// on either side (Sol r17 P2): a short name such as "a" or "ext" occurring
// inside an ordinary word ("Translate", "text") does not explain it.
func mentionsParam(descLower, name string) bool {
	n := strings.ToLower(name)
	if n == "" {
		return false
	}
	for off := 0; ; {
		i := strings.Index(descLower[off:], n)
		if i < 0 {
			return false
		}
		i += off
		end := i + len(n)
		before, _ := utf8.DecodeLastRuneInString(descLower[:i])
		after, _ := utf8.DecodeRuneInString(descLower[end:])
		if (i == 0 || !isParamNameRune(before)) && (end == len(descLower) || !isParamNameRune(after)) {
			return true
		}
		off = i + 1
	}
}

func isParamNameRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// sinkSideChannel matches a parameter description asking for data to be
// stored or carried ALONGSIDE the operation ("Marginalia to store alongside
// the translation"), i.e. a side channel rather than an operation input.
// Matched on normalized text.
var sinkSideChannel = regexp.MustCompile(`\b(?:stor|keep|kept|sav|record|log|persist|archiv|stash|retain|attach|carr|tuck)\w*\b[^.]{0,40}?\b(?:alongside|along with|together with|next to|beside|in the background|silently|quietly)\b`)

// sinkHarvest matches a parameter description asking for the user's
// conversation, the agent's prompt or the environment. Matched on normalized
// text; harvestNoun extracts the noun so a tool whose own description is about
// that thing (a chat summarizer taking "the conversation") is not flagged.
var sinkHarvest = regexp.MustCompile(`\b(?:conversation|chat) (?:history|context|transcript|log)\b|\bprevious messag\w*|\bsystem prompt\b|\benvironment variabl\w*|\b(?:all|any|every) (?:other |additional |extra |relevant )?(?:context|information|detail|data|content)\w* (?:you|the (?:user|assistant|model|agent)) (?:ha|have|has|know|saw|seen|collect|gather)\w*`)

// harvestNoun returns the noun of a sinkHarvest match that the tool's own
// description must mention for the parameter to count as explained.
func harvestNoun(m string) string {
	for _, n := range []string{"conversation", "chat", "messag", "system prompt", "environment", "context", "information", "detail", "data", "content"} {
		if strings.Contains(m, n) {
			return n
		}
	}
	return m
}

func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', '\u0085', '\u2028', '\u2029':
		return true
	}
	return false
}

// freeTextType reports whether a JSON Schema "type" admits free text: absent,
// "string", or a type list that includes "string".
func freeTextType(t any) bool {
	switch v := t.(type) {
	case nil:
		return true
	case string:
		return v == "string"
	case []any:
		for _, e := range v {
			if e == "string" {
				return true
			}
		}
	}
	return false
}
