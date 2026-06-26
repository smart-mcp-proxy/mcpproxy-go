package detect

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// Tier ranks a signal's contribution to the risk decision.
type Tier int

const (
	// TierSoft signals raise a finding for review but never auto-quarantine on
	// their own. Zero value, so an uninitialized Signal is conservatively soft.
	TierSoft Tier = iota
	// TierHard signals contribute to auto-quarantine; checks emitting them are
	// built for near-zero false positives.
	TierHard
)

// String renders the tier for logs and evidence.
func (t Tier) String() string {
	switch t {
	case TierHard:
		return "hard"
	case TierSoft:
		return "soft"
	default:
		return "unknown"
	}
}

// MaxEvidenceLen caps the rune length of rendered evidence before an ellipsis is
// appended, keeping reports bounded regardless of input size.
const MaxEvidenceLen = 200

// Signal is the output of a single Check for a single tool.
type Signal struct {
	CheckID    string  // stable identifier, e.g. "unicode.hidden"
	Tier       Tier    // hard (auto-quarantine) vs soft (review)
	ThreatType string  // maps to ScanFinding.ThreatType
	Confidence float64 // 0.0–1.0; already position-discounted for soft signals
	Evidence   string  // render-safe; for payload.decoded this is the decoded content
	Detail     string  // short human explanation
}

// Check inspects one tool against the whole registry snapshot and emits zero or
// more signals. Implementations MUST be pure, total, and deterministic — the
// engine wraps every Inspect in recover(), but a well-behaved check never
// panics and returns its signals in a stable order.
type Check interface {
	ID() string
	Inspect(tool ToolView, reg RegistryView) []Signal
}

// ToolView is a read-only projection of one tool supplied to checks. An empty
// description/schema is valid input and yields zero signals (no error).
type ToolView struct {
	Server         string
	Name           string
	Description    string // raw, un-normalized
	InputSchema    json.RawMessage
	OutputSchema   json.RawMessage
	NormalizedText string // precomputed Normalize(description + schema text)
}

// RegistryView is a read-only snapshot of every server's current tools, built
// once per scan and passed to every check so cross-tool checks (shadowing) can
// reason about collisions and references.
type RegistryView struct {
	Tools       []ToolView
	ToolsByName map[string][]ToolView // name → tools with that name (collision detection)
	ToolNames   map[string]struct{}   // fast membership for cross-tool references
}

// NewRegistryView builds the cross-tool indexes and precomputes each tool's
// NormalizedText (once). It preserves input ordering in Tools and in each
// ToolsByName slice, which keeps downstream findings deterministic.
func NewRegistryView(tools []ToolView) RegistryView {
	byName := make(map[string][]ToolView, len(tools))
	names := make(map[string]struct{}, len(tools))
	out := make([]ToolView, len(tools))
	for i, tv := range tools {
		if tv.NormalizedText == "" {
			tv.NormalizedText = Normalize(combinedText(tv))
		}
		out[i] = tv
		byName[tv.Name] = append(byName[tv.Name], tv)
		names[tv.Name] = struct{}{}
	}
	return RegistryView{Tools: out, ToolsByName: byName, ToolNames: names}
}

// combinedText concatenates the description with the raw schema text so the
// soft checks see parameter names/descriptions too.
func combinedText(tv ToolView) string {
	var b strings.Builder
	b.WriteString(tv.Description)
	if len(tv.InputSchema) > 0 {
		b.WriteByte(' ')
		b.Write(tv.InputSchema)
	}
	if len(tv.OutputSchema) > 0 {
		b.WriteByte(' ')
		b.Write(tv.OutputSchema)
	}
	return b.String()
}

// ClampConfidence constrains a confidence to the valid [0,1] range.
func ClampConfidence(c float64) float64 {
	if c < 0 {
		return 0
	}
	if c > 1 {
		return 1
	}
	return c
}

// CapEvidence makes a string safe to render in CLI/HTML/JSON reports: control
// runes and zero-width/bidi format runes are escaped to a visible \uXXXX form
// (revealed, never silently dropped — the whole point is to expose smuggling),
// and the result is truncated to MaxEvidenceLen runes with an ellipsis.
func CapEvidence(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			fmt.Fprintf(&b, "\\u%04x", r)
			continue
		}
		b.WriteRune(r)
	}
	escaped := b.String()
	runes := []rune(escaped)
	if len(runes) > MaxEvidenceLen {
		return string(runes[:MaxEvidenceLen]) + "…"
	}
	return escaped
}
