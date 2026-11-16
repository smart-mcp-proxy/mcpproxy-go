// SPDX-License-Identifier: Apache-2.0
//
// The padding detector below is adapted from NVIDIA SkillSpector
// (src/skillspector/nodes/analyzers/whitespace_padding.py, rule P9, reviewed
// at commit 5cb9385ab0dffa974088ee21c4376c7e560b2a52).
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
// Changes from upstream: rewritten in Go over each metadata field; thresholds
// recalibrated for tool descriptions (Spec 122 research R2: horizontal 64,
// vertical 12, block 1024 bytes, repeated char 256, repeated line 16); the
// file-ratio kind is dropped; a run counts only when content follows it; the
// Markdown fence and table exclusions are kept.

package checks

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// PaddingHidden is a SOFT check (Spec 122 US2) for presentation tricks that
// push text out of a reviewer's viewport: long horizontal runs of whitespace,
// many blank lines, a large whitespace-only block, or a wall of repeated
// characters or lines, each followed by more content. It reads the ORIGINAL
// field text (normalization would erase the evidence) and reports the
// strongest run per tool as a review warning; the existing checks keep
// inspecting whatever follows the padding.
type PaddingHidden struct{}

// ID implements detect.Check.
func (*PaddingHidden) ID() string { return "padding.hidden" }

// Thresholds (FR-020).
const (
	paddingHorizontalRun  = 64   // padding characters on one line
	paddingVerticalLines  = 12   // consecutive blank lines
	paddingBlockBytes     = 1024 // bytes of one contiguous whitespace block
	paddingRepeatedChar   = 256  // identical consecutive non-padding characters
	paddingRepeatedLines  = 16   // identical consecutive non-blank lines
	paddingBaseConfidence = 0.45
	paddingHighConfidence = 0.6
	// paddingReplacementDensity above which a field is binary-looking and
	// is not inspected for padding.
	paddingReplacementDensity = 0.30
)

// paddingRun is one detected run (data-model.md).
type paddingRun struct {
	kind       string
	unit       string
	start, end int // byte range in the field text
	length     int
	line       int // 1-based
	multiple   float64
	field      detect.Field
	fieldIndex int
}

var (
	paddingLineBreak = regexp.MustCompile("\r\n|\r|\n| | |\u0085")
	paddingFenceRe   = regexp.MustCompile("^\\s*(?:```|~~~)")
	paddingTableRe   = regexp.MustCompile(`^\s*\|?\s*:?-{2,}:?\s*(?:\|\s*:?-{2,}:?\s*)*\|?\s*$`)
)

// isPaddingRune reports a character that renders as nothing: ASCII and
// Unicode spaces and line/paragraph separators, NEL, U+180E and the
// zero-width family (unicode.hidden reports those as HARD on its own; here
// they only make a run longer).
func isPaddingRune(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f', 0x85, 0x180E, 0x200B, 0x200C, 0x200D, 0x2060, 0xFEFF:
		return true
	}
	return unicode.Is(unicode.Zs, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
}

func isBlankLine(s string) bool {
	for _, r := range s {
		if !isPaddingRune(r) {
			return false
		}
	}
	return true
}

// Inspect implements detect.Check.
func (c *PaddingHidden) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	fields, partial := detect.FieldStrings(tool)
	var runs []paddingRun
	for fi, f := range fields {
		for _, r := range detectPadding(f) {
			r.fieldIndex = fi
			runs = append(runs, r)
		}
	}
	var out []detect.Signal
	if len(runs) > 0 {
		kinds := map[string]bool{}
		for _, r := range runs {
			kinds[r.kind] = true
		}
		// Strongest run: largest threshold multiple, then field order, then
		// the earliest offset (FR-024).
		best := runs[0]
		for _, r := range runs[1:] {
			if r.multiple > best.multiple ||
				(r.multiple == best.multiple && (r.fieldIndex < best.fieldIndex || (r.fieldIndex == best.fieldIndex && r.start < best.start))) {
				best = r
			}
		}
		conf := paddingBaseConfidence
		if best.multiple >= 2 || len(kinds) >= 2 {
			conf = paddingHighConfidence
		}
		where := ""
		if best.field.Path != string(detect.SpanFieldDescription) {
			where = fmt.Sprintf(" [field %s]", best.field.Path)
		}
		out = append(out, detect.Signal{
			CheckID:    c.ID(),
			Tier:       detect.TierSoft,
			ThreatType: detect.ThreatToolPoisoning,
			Confidence: conf,
			Evidence:   detect.CapEvidence(fmt.Sprintf("%s run: %d %s at line %d", best.kind, best.length, best.unit, best.line)),
			Detail:     fmt.Sprintf("Description hides text behind a %s padding run (%d %s, line %d) — content after the run is pushed out of view; surfaced for review.%s", best.kind, best.length, best.unit, best.line, where),
			Spans:      detect.FieldSpan(c.ID(), detect.TierSoft, tool, best.field, best.start, best.end),
		})
	}
	if partial != "" {
		out = append(out, detect.Signal{
			CheckID:    c.ID(),
			Tier:       detect.TierSoft,
			ThreatType: detect.ThreatUncategorized,
			Confidence: paddingBaseConfidence,
			Evidence:   detect.CapEvidence("inspection budget: " + partial),
			Detail:     fmt.Sprintf("Analysis incomplete: the tool metadata exceeds the inspection budget (%s), so part of it was not inspected by the field-level checks — surfaced for review.", partial),
		})
	}
	return out
}

// detectPadding returns the deduplicated runs of one field, in document
// order within each kind (vertical, horizontal, block, repeated).
func detectPadding(f detect.Field) []paddingRun {
	text := f.Text
	if text == "" {
		return nil
	}
	if n := strings.Count(text, "�"); n > 0 && float64(n)/float64(utf8.RuneCountInString(text)) > paddingReplacementDensity {
		return nil
	}
	lines, offsets := splitPaddingLines(text)
	// last non-blank line index, to know whether content follows a run
	lastContent := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if !isBlankLine(lines[i]) {
			lastContent = i
			break
		}
	}
	if lastContent < 0 {
		return nil
	}
	var primary []paddingRun
	mk := func(kind, unit string, start, end, length, line int, threshold int) paddingRun {
		return paddingRun{kind: kind, unit: unit, start: start, end: end, length: length, line: line, multiple: float64(length) / float64(threshold), field: f}
	}
	// vertical
	for i := 0; i < len(lines); {
		if !isBlankLine(lines[i]) {
			i++
			continue
		}
		j := i
		for j < len(lines) && isBlankLine(lines[j]) {
			j++
		}
		if n := j - i; n >= paddingVerticalLines && j <= lastContent {
			end := offsets[j]
			primary = append(primary, mk("vertical", "blank lines", offsets[i], end, n, i+1, paddingVerticalLines))
		}
		i = j
	}
	// horizontal
	inFence := false
	for i, line := range lines {
		if paddingFenceRe.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence || strings.Count(line, "|") >= 2 || paddingTableRe.MatchString(line) {
			continue
		}
		runStart, runLen := -1, 0
		flush := func(at int) {
			if runLen >= paddingHorizontalRun {
				contentAfter := at < len(line) && !isBlankLine(line[at:]) || i < lastContent
				if contentAfter {
					primary = append(primary, mk("horizontal", "characters", offsets[i]+runStart, offsets[i]+at, runLen, i+1, paddingHorizontalRun))
				}
			}
			runStart, runLen = -1, 0
		}
		for k, r := range line {
			if isPaddingRune(r) {
				if runStart < 0 {
					runStart = k
				}
				runLen++
				continue
			}
			flush(k)
		}
		flush(len(line))
	}
	overlaps := func(start, end int, set []paddingRun) bool {
		for _, p := range set {
			if start < p.end && p.start < end {
				return true
			}
		}
		return false
	}
	runs := append([]paddingRun(nil), primary...)
	// block: the largest contiguous padding region, counted in bytes
	bestStart, bestEnd := -1, -1
	for i := 0; i < len(text); {
		r, n := utf8.DecodeRuneInString(text[i:])
		if !isPaddingRune(r) {
			i += n
			continue
		}
		j := i
		for j < len(text) {
			r2, n2 := utf8.DecodeRuneInString(text[j:])
			if !isPaddingRune(r2) {
				break
			}
			j += n2
		}
		if j-i > bestEnd-bestStart {
			bestStart, bestEnd = i, j
		}
		i = j
	}
	if bestStart >= 0 && bestEnd-bestStart > paddingBlockBytes && bestEnd < offsets[lastContent]+len(lines[lastContent]) && !overlaps(bestStart, bestEnd, primary) {
		if !isBlankLine(text[bestEnd:]) {
			runs = append(runs, mk("block", "bytes", bestStart, bestEnd, bestEnd-bestStart, lineAt(offsets, bestStart), paddingBlockBytes))
		}
	}
	// repeated character
	for i := 0; i < len(text); {
		r, n := utf8.DecodeRuneInString(text[i:])
		if isPaddingRune(r) {
			i += n
			continue
		}
		j, count := i, 0
		for j < len(text) {
			r2, n2 := utf8.DecodeRuneInString(text[j:])
			if r2 != r {
				break
			}
			j += n2
			count++
		}
		if count >= paddingRepeatedChar && !isBlankLine(text[j:]) && !overlaps(i, j, primary) {
			runs = append(runs, mk("repeated-char", "characters", i, j, count, lineAt(offsets, i), paddingRepeatedChar))
		}
		i = j
	}
	// repeated lines
	for i := 0; i < len(lines); {
		if isBlankLine(lines[i]) {
			i++
			continue
		}
		key := strings.TrimSpace(lines[i])
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == key {
			j++
		}
		if n := j - i; n >= paddingRepeatedLines && j <= lastContent {
			end := offsets[j-1] + len(lines[j-1])
			if !overlaps(offsets[i], end, primary) {
				runs = append(runs, mk("repeated-line", "identical lines", offsets[i], end, n, i+1, paddingRepeatedLines))
			}
		}
		i = j
	}
	return runs
}

// splitPaddingLines splits on every Unicode line break and returns the lines
// with the byte offset of each.
func splitPaddingLines(text string) (lines []string, offsets []int) {
	prev := 0
	for _, loc := range paddingLineBreak.FindAllStringIndex(text, -1) {
		lines = append(lines, text[prev:loc[0]])
		offsets = append(offsets, prev)
		prev = loc[1]
	}
	lines = append(lines, text[prev:])
	offsets = append(offsets, prev)
	return lines, offsets
}

func lineAt(offsets []int, off int) int {
	line := 1
	for i, o := range offsets {
		if o <= off {
			line = i + 1
		}
	}
	return line
}
