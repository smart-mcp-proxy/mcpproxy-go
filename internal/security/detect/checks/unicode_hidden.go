package checks

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// UnicodeHidden is a HARD check (FR-007) that flags invisible/format-control
// runes smuggled into a tool's RAW description or schema text: zero-width
// joiners/spaces, bidirectional controls, Unicode TAG-block characters, and
// Private-Use-Area code points. These never appear in a legitimate
// human-readable tool description, so a hit is near-zero false-positive.
//
// It runs on the RAW text deliberately — detect.Normalize strips format runes
// to stabilize phrase matching, which would hide exactly the attack this check
// exists to catch.
//
// Escalation (FR-007): a description carrying ≥3 distinct hidden classes, or
// TAG-block characters that decode to a printable ASCII message, is rated
// near-certain (critical); a single class is still hard but high.
type UnicodeHidden struct{}

// ID implements detect.Check.
func (*UnicodeHidden) ID() string { return "unicode.hidden" }

// hidden-rune class names, sorted for deterministic evidence rendering.
const (
	classZeroWidth = "zero-width"
	classBidi      = "bidi-control"
	classTag       = "tag-block"
	classPUA       = "private-use"
)

// Inspect implements detect.Check. It scans the raw description plus raw schema
// bytes and emits at most one signal per tool.
func (c *UnicodeHidden) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	raw := tool.Description
	if len(tool.InputSchema) > 0 {
		raw += " " + string(tool.InputSchema)
	}
	if len(tool.OutputSchema) > 0 {
		raw += " " + string(tool.OutputSchema)
	}
	if raw == "" {
		return nil
	}

	classes := make(map[string]struct{})
	var tagRunes []rune
	for _, r := range raw {
		switch {
		case isZeroWidth(r):
			classes[classZeroWidth] = struct{}{}
		case isBidiControl(r):
			classes[classBidi] = struct{}{}
		case isTagChar(r):
			classes[classTag] = struct{}{}
			tagRunes = append(tagRunes, r)
		case isPrivateUse(r):
			classes[classPUA] = struct{}{}
		}
	}
	if len(classes) == 0 {
		return nil
	}

	decoded := decodeTagMessage(tagRunes)
	escalate := len(classes) >= 3 || decoded != ""

	names := make([]string, 0, len(classes))
	for n := range classes {
		names = append(names, n)
	}
	sort.Strings(names)

	detail := fmt.Sprintf("Description contains hidden Unicode (%s) — invisible characters never appear in legitimate tool text.", strings.Join(names, ", "))
	evidence := "hidden classes: " + strings.Join(names, ", ")
	confidence := 0.8
	if escalate {
		confidence = 0.97
		if decoded != "" {
			detail = "Description hides a smuggled message in Unicode TAG-block characters."
			evidence = "decoded tag message: " + decoded
		}
	}

	return []detect.Signal{{
		CheckID:    c.ID(),
		Tier:       detect.TierHard,
		ThreatType: detect.ThreatToolPoisoning,
		Confidence: confidence,
		Evidence:   detect.CapEvidence(evidence),
		Detail:     detail,
	}}
}

// isZeroWidth reports the common invisible spacing/joining format runes. Code
// points are written numerically — invisible literals must never appear in
// source (they are exactly what this check hunts).
func isZeroWidth(r rune) bool {
	switch r {
	case 0x200B, // zero width space
		0x200C, // zero width non-joiner
		0x200D, // zero width joiner
		0x2060, // word joiner
		0xFEFF, // BOM / zero width no-break space
		0x00AD, // soft hyphen
		0x180E: // mongolian vowel separator
		return true
	}
	return false
}

// isBidiControl reports bidirectional override/embedding/isolate/mark runes
// used to visually reorder text (Trojan-Source style).
func isBidiControl(r rune) bool {
	switch r {
	case 0x200E, 0x200F, // LRM, RLM
		0x061C: // arabic letter mark
		return true
	}
	return (r >= 0x202A && r <= 0x202E) || // embeddings / overrides
		(r >= 0x2066 && r <= 0x2069) // isolates
}

// isTagChar reports a Unicode TAG-block code point (U+E0000–U+E007F), which can
// carry an invisible ASCII payload.
func isTagChar(r rune) bool {
	return r >= 0xE0000 && r <= 0xE007F
}

// isPrivateUse reports a Private-Use-Area code point (no assigned meaning,
// frequently abused to smuggle glyphs/markers).
func isPrivateUse(r rune) bool {
	return (r >= 0xE000 && r <= 0xF8FF) ||
		(r >= 0xF0000 && r <= 0xFFFFD) ||
		(r >= 0x100000 && r <= 0x10FFFD)
}

// decodeTagMessage maps TAG-block runes to their ASCII equivalents
// (U+E0000+ascii). It returns the decoded string only when it yields at least
// one printable character, otherwise "".
func decodeTagMessage(tags []rune) string {
	if len(tags) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range tags {
		ascii := r - 0xE0000
		if ascii >= 0x20 && ascii <= 0x7E && unicode.IsPrint(ascii) {
			b.WriteRune(ascii)
		}
	}
	return b.String()
}
