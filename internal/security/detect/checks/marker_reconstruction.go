// SPDX-License-Identifier: Apache-2.0
//
// The declared-marker reconstruction grammar below is adapted from NVIDIA
// SkillSpector (src/skillspector/security_reconstruction.py, reviewed at
// commit 5cb9385ab0dffa974088ee21c4376c7e560b2a52).
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
// Changes from upstream: a bounded SUBSET rewritten in Go for MCP tool
// metadata (Spec 122 research R3): quoted, backtick, tag, bare all-caps,
// passive and empty-replacement directives; action-governed quoted or inline
// payloads; one deterministic removal pass with an offset map; the existing
// detect checks run over the result. Encoded-entity quotes, anaphoric "run
// it" chains, JSON-container and Markdown-fence quote ownership are NOT
// ported — an active directive in such a form is reported as incomplete
// analysis instead of being guessed at.

package checks

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// MarkerReconstruction (Spec 122 US3) handles tool metadata that declares a
// literal marker and asks the agent to remove it from a nearby payload
// ("Remove XYZ from the following command before using it: cuXYZrl … | sXYZh").
// It applies exactly the declared transformation — remove that literal, once,
// from that payload — and runs the existing matchers over the result. Nothing
// is executed or fetched; the marker is never a pattern; every bound below is
// a hard cap, and an apparently active directive the subset cannot handle is
// reported as incomplete analysis, never as clean.
type MarkerReconstruction struct{}

// ID implements detect.Check.
func (*MarkerReconstruction) ID() string { return "payload.reconstructed" }

// Bounds (FR-031/033/034/037). Upstream values except the lookahead, which is
// 768 (not 8192) because a tool field is short.
const (
	maxPayloadsPerDirective = 4
	maxMarkerLen            = 16
	maxMarkerScope          = 768 // bytes after the directive in which a payload may start
	maxMarkerLookahead      = 768 // bytes in which a closing quote / tag end must appear
	maxPayloadBytes         = 700
	maxActiveDirectives     = 8
	maxMarkerRemovals       = 64
	maxDirectiveHeads       = 32
	maxForwardedSignals     = 4
	maxNegationPrefixLen    = 80
	reconstructedConf       = 0.5
)

// Directive heads. Matched on the RAW field text (case-insensitive) so quotes
// and offsets are the author's own.
const (
	removalVerbs    = `(?:remov(?:e|ing)|strip(?:ping)?|delet(?:e|ing)|drop(?:ping)?|omit(?:ting)?|eras(?:e|ing)|ignor(?:e|ing)|excis(?:e|ing)|tak(?:e|ing)[ \t]+out)`
	declaredPrefix  = `(?:[ \t]+(?:the|this|both|all|every|each|literal|declared|specified|following|next|invisible|markers?|tokens?|substrings?|strings?|text|characters?|occurrences?|of|sequence|instances?)){0,4}[ \t]+`
	quoteOpenClass  = "'\"`‘“"
	tagMarkerSyntax = `^</?[A-Za-z][A-Za-z0-9:_-]*(?:[ \t]*/)?>$`
)

var (
	quotedDirectiveRe     = regexp.MustCompile(`(?i)\b` + removalVerbs + declaredPrefix + `(?P<q>[` + quoteOpenClass + `])`)
	tagDirectiveRe        = regexp.MustCompile(`(?i)\b` + removalVerbs + declaredPrefix + `<`)
	bareDirectiveRe       = regexp.MustCompile(`(?i)\b` + removalVerbs + `[ \t]+(?:(?:the|every|all|each|any)[ \t]+)?(?P<m>(?-i:[^\s'"` + "`" + `‘“’”<>a-z]{2,16}))[ \t]+(?:from|in|within|out[ \t]+of)\b`)
	passiveDirectiveRe    = regexp.MustCompile(`(?i)\b(?:with|once|after)` + declaredPrefix + `(?P<q>[` + quoteOpenClass + `])`)
	passiveSuffixRe       = regexp.MustCompile(`(?i)^[ \t]+(?:(?:the[ \t]+)?(?:markers?|tokens?|substrings?|strings?|text|characters?)[ \t]+)?(?:is[ \t]+|are[ \t]+)?(?:removed|stripped|deleted|dropped|omitted|erased|ignored|excised|taken[ \t]+out)\b`)
	replaceDirectiveRe    = regexp.MustCompile(`(?i)\b(?:replac(?:e|ing)|substitut(?:e|ing))` + declaredPrefix + `(?P<q>[` + quoteOpenClass + `])`)
	emptyReplacementRe    = regexp.MustCompile(`(?i)^[ \t]+with[ \t]+(?:(?:an?|the)[ \t]+)?(?:nothing\b|empty[ \t]+(?:string|text|value)\b|''|"")`)
	encodedQuoteDirective = regexp.MustCompile(`(?i)\b(?:` + removalVerbs + `|replac(?:e|ing)|substitut(?:e|ing))` + declaredPrefix + `(?:&(?:#x0*27|#0*39|apos|#x0*22|#0*34|quot);|\\(?:x27|u0027|x22|u0022))`)
	tagMarkerRe           = regexp.MustCompile(tagMarkerSyntax)
	// actionVerbRe governs a payload (FR-033).
	actionVerbRe = regexp.MustCompile(`(?i)\b(?:run|execute|exec|invoke|issue|launch|perform|carry[ \t]+out|call|eval|type|submit|enter|paste|use|using|install|visit|open|fetch|download)\b`)
	// actionHeadRe is the bounded text allowed between the action verb and
	// the payload: "it:", "the following command:", "exactly this:".
	actionHeadRe = regexp.MustCompile(`(?i)^[ \t]*(?:(?:it|this|that|them|the[ \t]+result|the[ \t]+output)\b[ \t]*)?[,:]?[ \t]*(?:(?:exactly|verbatim|literally|directly|immediately)[ \t]+){0,2}(?:(?:the|this|following|next|below|exact)[ \t]+){0,3}(?:(?:command|instruction|payload|request|shell[ \t]+command|snippet|text|script|line|one)\b[ \t]*(?:(?:below|here|now)\b[ \t]*)?)?(?::|=|-)?[ \t]*(?:\r?\n[ \t]*)?(?:` + "```" + `[A-Za-z0-9_+-]*[ \t]*\r?\n[ \t]*)?`)
	blankLineRe  = regexp.MustCompile(`\n[ \t]*\n`)
)

var quoteCloser = map[rune]rune{'\'': '\'', '"': '"', '`': '`', '‘': '’', '“': '”'}

// markerDirective is one parsed directive head (data-model.md).
type markerDirective struct {
	marker     string
	form       string
	start, end int    // byte range of the directive head in the field text
	limit      string // non-empty: unsupported form, reported as incomplete when active
	exhausted  bool   // the lookahead was cut before the marker closed: incomplete whatever follows
	bare       bool
}

// reconstruction is one supported, active directive with its payload.
type reconstruction struct {
	directive                markerDirective
	payloadStart, payloadEnd int
	frameAt                  int // offset at which the original framing is judged (the opening quote of a quoted payload)
	reconstructed            string
	offsetMap                []int // reconstructed byte → field byte
}

// Inspect implements detect.Check.
func (c *MarkerReconstruction) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	fields, _ := detect.FieldStrings(tool)
	var forwarded []detect.Signal
	var provenance []detect.Signal
	incomplete := ""
	active := 0
	for _, f := range fields {
		if !strings.ContainsAny(f.Text, "'\"`‘“<&\\") && bareDirectiveRe.FindStringIndex(f.Text) == nil {
			continue
		}
		dirs, tooMany := parseMarkerDirectives(f.Text)
		if tooMany && incomplete == "" {
			incomplete = "too many directive heads"
		}
		for di, d := range dirs {
			// A later directive head ends the scope of this one: its actions
			// and payloads belong to that directive.
			scopeLimit := len(f.Text)
			if di+1 < len(dirs) {
				scopeLimit = dirs[di+1].start
			}
			if d.exhausted {
				// A marker the bounded lookahead could not close is not
				// assumed inactive (fail closed, FR-037).
				if incomplete == "" {
					incomplete = d.limit
				}
				continue
			}
			recs := bindPayloads(f.Text, d, scopeLimit)
			if len(recs) == 0 {
				// No payload inside this directive's own scope. If the scope
				// was cut by the next head and an action follows THAT head
				// within the full lookahead, the two directives share one
				// action ("remove 'XYZ' … and remove 'QQ' …, then run: …"):
				// ownership is ambiguous, so the field is incomplete, never
				// silently clean (Sol r4 P2).
				if scopeLimit < len(f.Text) {
					full := min(len(f.Text), d.end+maxMarkerScope)
					if full > scopeLimit && actionVerbRe.MatchString(f.Text[scopeLimit:full]) {
						active++
						if incomplete == "" {
							incomplete = "coordinated directives share one action"
							if d.limit != "" {
								incomplete = d.limit // an unsupported marker is incomplete on its own
							}
						}
					}
				}
				continue // inactive: no action-governed payload
			}
			active++
			if active > maxActiveDirectives {
				if incomplete == "" {
					incomplete = "more than 8 active directives"
				}
				break
			}
			// Every action-governed payload of the directive is inspected
			// ("run 'echo XYZok'; then run 'cuXYZrl …'"): a harmless first
			// payload must not mask a later one (Sol r3 P2).
			for _, act := range recs {
				if act.directive.limit != "" {
					if incomplete == "" {
						incomplete = act.directive.limit
					}
					continue
				}
				sigs, prov := c.inspectReconstruction(tool, f, act)
				forwarded = append(forwarded, sigs...)
				if prov != nil {
					provenance = append(provenance, *prov)
				}
			}
		}
	}
	var out []detect.Signal
	if len(forwarded) > 0 {
		sort.SliceStable(forwarded, func(i, j int) bool {
			if forwarded[i].Confidence != forwarded[j].Confidence {
				return forwarded[i].Confidence > forwarded[j].Confidence
			}
			return forwarded[i].CheckID < forwarded[j].CheckID
		})
		seen := map[string]bool{}
		for _, s := range forwarded {
			if seen[s.CheckID] {
				continue
			}
			seen[s.CheckID] = true
			out = append(out, s)
			if len(out) == maxForwardedSignals {
				break
			}
		}
		out = append(out, provenance[0])
	}
	if incomplete != "" {
		out = append(out, detect.Signal{
			CheckID:    c.ID(),
			Tier:       detect.TierSoft,
			ThreatType: detect.ThreatUncategorized,
			Confidence: reconstructedConf,
			Evidence:   detect.CapEvidence("declared marker directive: " + incomplete),
			Detail:     fmt.Sprintf("A declared-marker transformation could not be analyzed (%s); the payload after it was NOT inspected — analysis incomplete, surfaced for review.", incomplete),
		})
	}
	return out
}

// parseMarkerDirectives finds every directive head in text, in document
// order, deduplicated by start offset. tooMany reports that a 33rd head
// exists (FR-037).
func parseMarkerDirectives(text string) (dirs []markerDirective, tooMany bool) {
	var heads []markerDirective
	add := func(d markerDirective) { heads = append(heads, d) }

	// Each grammar yields at most maxDirectiveHeads+1 candidates (document
	// order): the global 33rd head is then always among them, which is all
	// the cap needs to prove, and parsing stays bounded (Sol r1 P2).
	const headCap = maxDirectiveHeads + 1
	// rawExhausted: some grammar hit its raw budget, so heads beyond it were
	// never examined (even if the filters removed most of the examined ones);
	// that is reported as incomplete, never assumed inactive (Sol r2 P1).
	rawExhausted := false
	budget := func(n int) { rawExhausted = rawExhausted || n >= headCap }
	q1 := quotedDirectiveRe.FindAllStringSubmatchIndex(text, headCap)
	budget(len(q1))
	for _, loc := range q1 {
		if skipDirective(text, loc[0]) {
			continue
		}
		q := loc[2]
		d := quotedMarker(text, loc[0], q, "quoted")
		if d.form == "" {
			continue
		}
		if text[q] == '`' {
			d.form = "backtick"
		}
		add(d)
	}
	taRaw := tagDirectiveRe.FindAllStringIndex(text, headCap)
	budget(len(taRaw))
	for _, loc := range taRaw {
		if skipDirective(text, loc[0]) {
			continue
		}
		open := loc[1] - 1
		limit := min(len(text), open+maxMarkerLookahead)
		close := strings.IndexByte(text[open:limit], '>')
		d := markerDirective{form: "tag", start: loc[0]}
		if close < 0 {
			if limit == len(text) {
				continue // "a < b" at the end of the text: not a tag
			}
			d.end, d.limit, d.exhausted = limit, "tag marker without a closing '>' within the lookahead", true
		} else {
			d.marker = text[open : open+close+1]
			d.end = open + close + 1
			if !tagMarkerRe.MatchString(d.marker) {
				continue // "a < b": not a tag at all, not a directive
			}
		}
		add(d)
	}
	baRaw := bareDirectiveRe.FindAllStringSubmatchIndex(text, headCap)
	budget(len(baRaw))
	for _, loc := range baRaw {
		if skipDirective(text, loc[0]) {
			continue
		}
		m := text[loc[2]:loc[3]]
		if !strings.ContainsFunc(m, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) && len(m) > 4 {
			continue // a long run of punctuation is a separator line, not a marker
		}
		add(markerDirective{marker: m, form: "bare", start: loc[0], end: loc[1], bare: true})
	}
	paRaw := passiveDirectiveRe.FindAllStringSubmatchIndex(text, headCap)
	budget(len(paRaw))
	for _, loc := range paRaw {
		d := quotedMarker(text, loc[0], loc[2], "passive")
		if d.form == "" || (d.marker == "" && d.limit == "") {
			continue
		}
		if !d.exhausted {
			// The suffix is what makes "with '…'" a removal; without it the
			// quote is an ordinary quoted phrase, whatever it contains
			// ("with 'customer display name' as a field", Sol r3 P2).
			suf := passiveSuffixRe.FindStringIndex(text[d.end:min(len(text), d.end+64)])
			if suf == nil {
				continue
			}
			d.end += suf[1]
		}
		add(d)
	}
	reRaw := replaceDirectiveRe.FindAllStringSubmatchIndex(text, headCap)
	budget(len(reRaw))
	for _, loc := range reRaw {
		if skipDirective(text, loc[0]) {
			continue
		}
		d := quotedMarker(text, loc[0], loc[2], "empty-replacement")
		if d.form == "" {
			continue
		}
		if !d.exhausted {
			suf := emptyReplacementRe.FindStringIndex(text[d.end:min(len(text), d.end+64)])
			if suf == nil {
				continue // "replace 'x' with 'y'": a substitution, out of scope
			}
			d.end += suf[1]
		}
		add(d)
	}
	enRaw := encodedQuoteDirective.FindAllStringIndex(text, headCap)
	budget(len(enRaw))
	for _, loc := range enRaw {
		if skipDirective(text, loc[0]) {
			continue
		}
		add(markerDirective{form: "encoded", start: loc[0], end: loc[1], limit: "marker delimited by an encoded quote"})
	}

	sort.SliceStable(heads, func(i, j int) bool {
		if heads[i].start != heads[j].start {
			return heads[i].start < heads[j].start
		}
		return heads[i].end < heads[j].end
	})
	lastStart := -1
	for _, h := range heads {
		if h.start == lastStart {
			continue
		}
		lastStart = h.start
		if len(dirs) == maxDirectiveHeads {
			return dirs, true
		}
		dirs = append(dirs, h)
	}
	return dirs, rawExhausted
}

// quotedMarker reads the marker between the quote at text[q] and its closing
// quote. A marker with whitespace or a quote inside, longer than the cap, or
// a single alphanumeric character is an unsupported form (limit set); no
// closing quote within the lookahead is "exhausted".
func quotedMarker(text string, start, q int, form string) markerDirective {
	open, n := decodeRuneAt(text, q)
	closer := quoteCloser[open]
	d := markerDirective{form: form, start: start}
	limit := min(len(text), q+n+maxMarkerLookahead)
	rel := strings.IndexRune(text[q+n:limit], closer)
	if rel < 0 {
		if limit == len(text) {
			// The field simply ends: an apostrophe ("remove 'em"), not a
			// marker. Only a lookahead that was CUT is exhausted.
			d.end = len(text)
			d.form = ""
			return d
		}
		d.end = limit
		d.limit = "marker without a closing quote within the lookahead"
		d.exhausted = true
		return d
	}
	d.marker = text[q+n : q+n+rel]
	d.end = q + n + rel + len(string(closer))
	switch {
	case d.marker == "":
		d.limit = "empty marker"
	case len(d.marker) > maxMarkerLen:
		d.limit = "marker longer than 16 bytes"
	case strings.ContainsFunc(d.marker, unicode.IsSpace):
		d.limit = "marker containing whitespace"
	case strings.ContainsAny(d.marker, quoteOpenClass+"’”"):
		d.limit = "marker containing a quote"
	case len(d.marker) == 1 && isASCIIAlnum(d.marker[0]):
		d.limit = "single-character alphanumeric marker"
	}
	return d
}

func decodeRuneAt(s string, i int) (rune, int) {
	for _, r := range s[i:] {
		return r, len(string(r))
	}
	return 0, 0
}

func isASCIIAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// skipDirective reports the heads that are not directives (FR-032): a CLI
// flag token ("--strip"), a quoted mapping key ("remove": …), one of a
// sequence of quoted literals ('delete', 'run'), or a negated verb ("do not
// remove 'x'").
func skipDirective(text string, verbStart int) bool {
	if verbStart > 0 {
		prev := text[verbStart-1]
		if prev == '-' || prev == '_' {
			return true
		}
		if strings.ContainsRune(quoteOpenClass+"’”", rune(prev)) {
			// quoted verb: a mapping key or a literal in a sequence
			verbEnd := verbStart
			for verbEnd < len(text) && (unicode.IsLetter(rune(text[verbEnd])) || text[verbEnd] == ' ') {
				verbEnd++
			}
			rest := strings.TrimLeft(text[verbEnd:], " \t")
			if len(rest) > 0 && strings.ContainsRune(quoteOpenClass+"’”", rune(rest[0])) {
				after := strings.TrimLeft(rest[1:], " \t\r\n,")
				if strings.HasPrefix(after, ":") || (len(after) > 0 && strings.ContainsRune(quoteOpenClass+"’”", rune(after[0]))) {
					return true
				}
			}
		}
	}
	return rawNegated(text, verbStart)
}

// rawNegated reports whether the verb at verbStart is governed by a negation
// in its clause. It lowercases and contraction-expands the bounded prefix and
// applies detect.NegatedBefore, so breakers ("do not log, but remove …" is
// affirmative) and idioms ("do not forget to remove …") are handled the same
// way everywhere (Sol r2 P1).
func rawNegated(text string, verbStart int) bool {
	lo := max(0, verbStart-maxNegationPrefixLen)
	prefix := text[lo:verbStart]
	if i := strings.LastIndexAny(prefix, ".!?;\n"); i >= 0 {
		prefix = prefix[i+1:]
	}
	prefix = strings.ToLower(prefix)
	prefix = strings.NewReplacer("can't", "cannot", "won't", "will not", "n't", " not", "’", "'").Replace(prefix)
	return detect.NegatedBefore(prefix, len(prefix))
}

// bindPayloads finds the action-governed payloads of a directive (FR-033),
// at most maxPayloadsPerDirective of them in document order (a further one
// is reported as incomplete). An empty result means the directive is
// inactive. For an unsupported directive any action verb within scope makes
// it active (the form cannot be parsed, so the action alone counts).
func bindPayloads(text string, d markerDirective, scopeLimit int) []reconstruction {
	var out []reconstruction
	seenEnd := -1
	add := func(rec reconstruction) bool {
		if rec.directive.limit == "" && rec.payloadEnd <= seenEnd {
			return false // a payload inside an earlier one
		}
		if len(out) == maxPayloadsPerDirective {
			out = append(out, reconstruction{directive: markerDirective{marker: d.marker, start: d.start, end: d.end, limit: "more than 4 payloads for one directive"}})
			return true
		}
		out = append(out, rec)
		seenEnd = max(seenEnd, rec.payloadEnd)
		return false
	}
	bindPayloadCandidates(text, d, scopeLimit, add)
	return out
}

// bindPayloadCandidates calls add for each payload bound to d, in document
// order, until add reports the cap.
func bindPayloadCandidates(text string, d markerDirective, scopeLimit int, add func(reconstruction) bool) {
	scopeEnd := min(len(text), d.end+maxMarkerScope, max(scopeLimit, d.end))
	type candidate struct{ verbStart, payloadFrom int }
	var cands []candidate
	// An action verb BEFORE the directive, in the same sentence ("Run the
	// following with 'ZZ' removed: …", "Paste this after removing 'XY': …"),
	// governs a payload introduced right after the directive head.
	sentStart := 0
	if i := strings.LastIndexAny(text[:d.start], ".!?;\n"); i >= 0 {
		sentStart = i + 1
	}
	// Only the NEAREST preceding verb matters: every preceding verb binds
	// the same payload (right after the directive head), so one candidate
	// stands for all of them (Sol r9 P2).
	if locs := actionVerbRe.FindAllStringIndex(text[sentStart:d.start], -1); len(locs) > 0 {
		for k := len(locs) - 1; k >= 0; k-- {
			if !verbNegated(text, sentStart+locs[k][0]) {
				cands = append(cands, candidate{sentStart + locs[k][0], d.end})
				break
			}
		}
	}
	for _, loc := range actionVerbRe.FindAllStringIndex(text[d.end:scopeEnd], -1) {
		if !verbNegated(text, d.end+loc[0]) {
			cands = append(cands, candidate{d.end + loc[0], d.end + loc[1]})
		}
	}
	for _, cand := range cands {
		if d.limit != "" {
			add(reconstruction{directive: d})
			return
		}
		// The head is parsed inside the scope only, so a wall of blanks after
		// the verb cannot be rescanned per candidate.
		head := actionHeadRe.FindStringIndex(text[cand.payloadFrom:min(len(text), cand.payloadFrom+maxMarkerScope)])
		pStart := cand.payloadFrom + head[1]
		if cand.payloadFrom == d.end && head[1] == 0 {
			continue // a preceding verb needs an explicit head (":" etc.) after the directive
		}
		if pStart >= len(text) {
			continue
		}
		frameAt := pStart
		fenced := strings.Contains(text[cand.payloadFrom:pStart], "```")
		pStart, pEnd, truncated := payloadBounds(text, pStart, fenced)
		if pStart >= pEnd {
			continue
		}
		payload := text[pStart:pEnd]
		if truncated {
			// The part of the payload beyond the cap was NOT inspected: an
			// action-governed payload cut short is incomplete analysis whether
			// or not the marker was seen before the cut (Sol r2 P1).
			rec := reconstruction{directive: d, payloadStart: pStart, payloadEnd: pEnd, frameAt: frameAt}
			rec.directive.limit = "payload longer than 700 bytes"
			add(rec)
			return
		}
		if !strings.Contains(payload, d.marker) {
			continue
		}
		if d.bare && !markerSplitsWord(payload, d.marker) {
			continue
		}
		rec := reconstruction{directive: d, payloadStart: pStart, payloadEnd: pEnd, frameAt: frameAt}
		if coordinatedMarker(text, d, pStart, pEnd) {
			rec.directive.limit = "two different markers declared over one payload"
			add(rec)
			return
		}
		positions, overlapping := markerPositions(payload, d.marker)
		switch {
		case overlapping:
			rec.directive.limit = "overlapping marker occurrences"
			add(rec)
			return
		case len(positions) > maxMarkerRemovals:
			rec.directive.limit = "more than 64 marker occurrences"
			add(rec)
			return
		}
		rec.reconstructed, rec.offsetMap = removeMarker(payload, d.marker, positions, pStart)
		if add(rec) {
			return
		}
	}
}

// payloadBounds bounds an inline or quoted payload: a quoted span is the text
// between its delimiting quotes (the quotes delimit a command, they do not
// quote an example); an inline span runs to the first line break, blank line
// or field end, capped at maxPayloadBytes.
func payloadBounds(text string, from int, fenced bool) (start, end int, truncated bool) {
	limit := min(len(text), from+maxPayloadBytes)
	if fenced {
		// Inside a ``` block the payload is the whole block (every line is
		// a command): it ends at the closing fence or is cut at the cap.
		if i := strings.Index(text[from:limit], "\n```"); i >= 0 {
			return from, from + i, false
		}
		return from, limit, limit < len(text)
	}
	if r, n := decodeRuneAt(text, from); n > 0 {
		if closer, ok := quoteCloser[r]; ok {
			rel := unescapedQuoteIndex(text[from+n:limit], closer)
			if rel < 0 {
				// no closing quote within the cap: the quoted payload is cut
				return from + n, limit, limit < len(text)
			}
			close := from + n + rel + len(string(closer))
			// A quoted word followed by more command text on the same line
			// ("run: 'cuXYZrl' https://… | sXYZh") quotes the executable, not
			// the whole payload: the payload is the whole line (Sol r6 P2).
			rest := text[close:]
			if i := strings.IndexByte(rest, '\n'); i >= 0 {
				rest = rest[:i]
			}
			// A shell separator after the quote (";", "&&", "||", "|")
			// continues the command (Sol r10 P2); prose punctuation ends it.
			rest = strings.TrimLeft(rest, " \t")
			if rest == "" || (strings.ContainsRune(".,:)!?", rune(rest[0])) && !strings.HasPrefix(rest, ";")) {
				return from + n, from + n + rel, false
			}
			if rest[0] == ';' && strings.TrimSpace(rest[1:]) == "" {
				return from + n, from + n + rel, false
			}
		}
	}
	// Inline: to the end of the line, where a backslash before the line
	// break continues the command on the next line (Sol r6 P2).
	end = from
	for end < limit {
		i := strings.IndexByte(text[end:limit], '\n')
		if i < 0 {
			end = limit
			break
		}
		if lineContinues(text[end : end+i]) {
			end += i + 1
			continue
		}
		end += i
		break
	}
	if loc := blankLineRe.FindStringIndex(text[from:end]); loc != nil {
		end = from + loc[0]
	}
	return from, end, end == limit && limit < len(text) && text[limit] != '\n'
}

// lineContinues reports a line that the shell continues on the next one: a
// trailing backslash or a trailing pipe / and / or operator (Sol r7 P2).
func lineContinues(line string) bool {
	t := strings.TrimRight(line, " \t\r")
	return strings.HasSuffix(t, "\\") || strings.HasSuffix(t, "|") || strings.HasSuffix(t, "&&") || strings.HasSuffix(t, "||")
}

// unescapedQuoteIndex finds the first closer in s that is not preceded by an
// odd run of backslashes ("echo \"ok\"; …" keeps its inner quotes), or -1.
func unescapedQuoteIndex(s string, closer rune) int {
	for i := 0; i < len(s); {
		rel := strings.IndexRune(s[i:], closer)
		if rel < 0 {
			return -1
		}
		at := i + rel
		bs := 0
		for k := at - 1; k >= 0 && s[k] == '\\'; k-- {
			bs++
		}
		if bs%2 == 0 {
			return at
		}
		i = at + len(string(closer))
	}
	return -1
}

// verbNegated reports a negated action ("do not run it").
func verbNegated(text string, verbStart int) bool { return rawNegated(text, verbStart) }

// markerSplitsWord reports that at least one occurrence of the marker sits
// next to a letter or digit: the marker was inserted inside a word.
func markerSplitsWord(payload, marker string) bool {
	for i := 0; ; {
		j := strings.Index(payload[i:], marker)
		if j < 0 {
			return false
		}
		at := i + j
		if (at > 0 && isASCIIAlnum(payload[at-1])) || (at+len(marker) < len(payload) && isASCIIAlnum(payload[at+len(marker)])) {
			return true
		}
		i = at + 1
		if i >= len(payload) {
			return false
		}
	}
}

// coordinatedMarker reports a second, different quoted marker declared over
// the same payload region ("remove 'XY' and 'Z' from …").
func coordinatedMarker(text string, d markerDirective, pStart, pEnd int) bool {
	payload := text[pStart:pEnd]
	// Only an explicitly coordinated literal counts ("remove 'AB' and 'CD'",
	// "'AB', 'CD'"): a quoted word elsewhere in the sentence ("the command
	// labeled 'setup'") is not a declared marker even when it happens to
	// occur in the payload (Sol r9 P2).
	i := d.end
	for i < pStart {
		m := coordinatedJoinRe.FindStringIndex(text[i:pStart])
		if m == nil || m[0] != 0 {
			return false
		}
		q := i + m[1] - 1
		r, n := decodeRuneAt(text, q)
		closer := quoteCloser[r]
		rel := strings.IndexRune(text[q+n:pStart], closer)
		if rel < 0 {
			return false
		}
		lit := text[q+n : q+n+rel]
		if lit != "" && lit != d.marker && len(lit) <= maxMarkerLen && !strings.ContainsFunc(lit, unicode.IsSpace) && strings.Contains(payload, lit) {
			return true
		}
		i = q + n + rel + len(string(closer))
	}
	return false
}

// coordinatedJoinRe is the glue between coordinated marker literals.
var coordinatedJoinRe = regexp.MustCompile(`^[ \t]*(?:,|and|or|&|/|plus|then)?[ \t]*[` + quoteOpenClass + `]`)

// markerPositions lists the non-overlapping occurrences of marker in payload
// and reports an overlapping occurrence.
func markerPositions(payload, marker string) (positions []int, overlapping bool) {
	lastEnd := 0
	for i := 0; i <= len(payload)-len(marker); {
		j := strings.Index(payload[i:], marker)
		if j < 0 {
			break
		}
		at := i + j
		if at < lastEnd {
			overlapping = true
		} else {
			positions = append(positions, at)
			lastEnd = at + len(marker)
		}
		i = at + 1
	}
	return positions, overlapping
}

// removeMarker deletes the occurrences in one pass and keeps, for every byte
// of the result, the field offset it came from.
func removeMarker(payload, marker string, positions []int, base int) (string, []int) {
	var b strings.Builder
	b.Grow(len(payload))
	offsets := make([]int, 0, len(payload))
	cursor := 0
	for _, p := range positions {
		b.WriteString(payload[cursor:p])
		for k := cursor; k < p; k++ {
			offsets = append(offsets, base+k)
		}
		cursor = p + len(marker)
	}
	b.WriteString(payload[cursor:])
	for k := cursor; k < len(payload); k++ {
		offsets = append(offsets, base+k)
	}
	return b.String(), offsets
}

// innerChecks are run over the reconstructed payload (FR-035).
func innerChecks() []detect.Check {
	return []detect.Check{&PhraseInjection{}, &DirectiveImperative{}, &AntiRefusal{}}
}

// inspectReconstruction runs the inner matchers over one reconstruction and
// returns the forwarded signals (with provenance, outer framing and span) and
// the provenance signal.
func (c *MarkerReconstruction) inspectReconstruction(tool detect.ToolView, f detect.Field, rec reconstruction) ([]detect.Signal, *detect.Signal) {
	original := f.Text[rec.payloadStart:rec.payloadEnd]
	inner := signalsOver(rec.reconstructed)
	if len(inner) == 0 {
		return nil, nil
	}
	// The original payload's own matches (FR-036): a check that already
	// reports the original at least as strongly gains nothing from the
	// reconstruction and is not forwarded; a reconstruction that upgrades a
	// review match to a hard one is.
	// Only the inner CHECKS are compared: the plaintext shell matcher is
	// synthetic here (payload.decoded itself reads encoded blobs only), so
	// a "| sh" that is also readable in the original must never erase the
	// reconstructed command (Sol r1 P1).
	already := map[string]detect.Tier{}
	for _, s := range innerSignalsOver(original) {
		if t, ok := already[s.CheckID]; !ok || s.Tier > t {
			already[s.CheckID] = s.Tier
		}
	}
	span := detect.FieldSpan(c.ID(), detect.TierSoft, tool, f, rec.payloadStart, rec.payloadEnd)
	provenanceText := fmt.Sprintf("After removing declared marker %q: ", rec.directive.marker)
	normalizedRec := detect.Normalize(rec.reconstructed)
	var out []detect.Signal
	shell := false
	for _, s := range inner {
		if t, ok := already[s.CheckID]; ok && t >= s.Tier {
			continue
		}
		if s.CheckID == "payload.decoded" {
			shell = true
		} else {
			// Carry the ORIGINAL field's framing: classify the match at its
			// own sentence in the original text (FR-035). The sentence is
			// located through the evidence in the normalized reconstruction;
			// when it cannot be located the payload start's class applies.
			pos := detect.ClassifyPositionForRecall(f.Text, rec.frameAt)
			if idx := strings.Index(normalizedRec, s.Evidence); idx > 0 && strings.ContainsAny(normalizedRec[:idx], ".!?") {
				// A later sentence of the payload: the inner, sentence-scoped
				// classification already applied; the outer frame does not
				// reach across a period.
				pos = detect.PositionInstruction
			}
			if d := pos.Discount(); d < 1 {
				s.Confidence = detect.ClampConfidence(s.Confidence * d)
				if s.Tier == detect.TierHard && s.Confidence < phraseHardMinConfidence {
					s.Tier = detect.TierSoft
					if s.Confidence < phraseSoftFloor {
						s.Confidence = phraseSoftFloor
					}
				}
				if s.Tier == detect.TierSoft && s.Confidence < phraseSoftFloor {
					// never fully suppress: the reconstruction itself is the
					// evidence, so a discounted match stays at the review floor
					s.Confidence = phraseSoftFloor
				}
			}
		}
		s.Detail = provenanceText + s.Detail
		if span != nil {
			sp := span[0]
			sp.CheckID, sp.Tier = s.CheckID, s.Tier.String()
			s.Spans = []detect.Span{sp}
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, nil
	}
	threat := detect.ThreatPromptInjection
	if shell {
		threat = detect.ThreatMaliciousCode
	}
	where := ""
	if f.Path != string(detect.SpanFieldDescription) {
		where = fmt.Sprintf(" [field %s]", f.Path)
	}
	prov := detect.Signal{
		CheckID:    c.ID(),
		Tier:       detect.TierSoft,
		ThreatType: threat,
		Confidence: reconstructedConf,
		Evidence:   detect.CapEvidence("reconstructed payload: " + rec.reconstructed),
		Detail:     fmt.Sprintf("Removing the declared marker %q from the nearby payload (as the description instructs) reveals %s.%s", rec.directive.marker, strings.TrimSuffix(describeForwarded(out), "."), where),
		Spans:      span,
	}
	return out, &prov
}

// signalsOver runs the shell matcher and the inner checks over text as a
// standalone description.
func signalsOver(text string) []detect.Signal {
	var out []detect.Signal
	if s, ok := shellPayloadSignalText("payload.decoded", "reconstructed payload", "The reconstructed payload is", text); ok {
		out = append(out, s)
	}
	return append(out, innerSignalsOver(text)...)
}

// innerSignalsOver runs only the inner checks over text.
func innerSignalsOver(text string) []detect.Signal {
	var out []detect.Signal
	reg := detect.NewRegistryView([]detect.ToolView{{Server: "reconstructed", Name: "payload", Description: text}})
	for _, ch := range innerChecks() {
		out = append(out, ch.Inspect(reg.Tools[0], reg)...)
	}
	return out
}

func describeForwarded(sigs []detect.Signal) string {
	names := make([]string, 0, len(sigs))
	for _, s := range sigs {
		names = append(names, s.CheckID)
	}
	return "matches for " + strings.Join(names, ", ")
}
