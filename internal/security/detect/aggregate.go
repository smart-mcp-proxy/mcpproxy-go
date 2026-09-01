package detect

import (
	"fmt"
	"sort"
)

// Severity levels — string values mirror internal/security/scanner so a Finding
// maps onto scanner.ScanFinding without translation (the scanner wiring copies
// these strings verbatim). detect cannot import scanner (import cycle), so the
// vocabulary is mirrored here, not aliased.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
	SeverityInfo     = "info"
)

// Threat levels — user-facing severity, mirrors scanner.ThreatLevel*.
const (
	ThreatLevelDangerous = "dangerous" // any hard signal → auto-quarantine
	ThreatLevelWarning   = "warning"   // soft-only → review
	ThreatLevelInfo      = "info"
)

// Threat types — the report vocabulary, mirrors scanner.Threat* plus the
// exfiltration category from the Spec-076 data model.
const (
	ThreatToolPoisoning   = "tool_poisoning"
	ThreatPromptInjection = "prompt_injection"
	ThreatRugPull         = "rug_pull"
	ThreatExfiltration    = "exfiltration"
	ThreatMaliciousCode   = "malicious_code"
	ThreatUncategorized   = "uncategorized"
)

// criticalConfidence is the hard-signal confidence at/above which a dangerous
// finding is rated critical rather than high. Escalating checks (≥3 unicode
// classes, decoded shell payloads) emit near-1.0 confidence.
const criticalConfidence = 0.9

// Finding is the per-tool aggregation output. It is self-contained (no scanner
// import) and converts 1:1 to scanner.ScanFinding in the scanner wiring (T012);
// the additive Confidence/Signals fields already exist on ScanFinding (T004).
type Finding struct {
	RuleID      string
	Scanner     string
	ThreatType  string
	ThreatLevel string
	Severity    string
	Category    string
	Title       string
	Description string
	Location    string
	Evidence    string
	Confidence  float64
	Signals     []string
	// Spans is the UNION of every contributing signal's spans (deduped, sorted,
	// capped), not just the primary's. See unionSpans.
	Spans []Span
}

// aggregate combines every signal emitted for one tool into a single Finding,
// applying the Spec-076 tier and severity semantics (FR-005, FR-006, FR-010).
// It returns ok=false when there are no signals. It is deterministic: output
// depends only on the signal slice order.
func aggregate(tool ToolView, signals []Signal, scannerID string) (Finding, bool) {
	if len(signals) == 0 {
		return Finding{}, false
	}

	// Distinct CheckIDs in first-seen order, plus combined confidence and the
	// primary (highest-tier, first-seen) signal.
	seen := make(map[string]struct{}, len(signals))
	var ids []string
	var confSum float64
	var primary Signal
	haveHard := false
	maxHardConf := 0.0
	distinctSoft := make(map[string]struct{})

	for i, s := range signals {
		confSum += ClampConfidence(s.Confidence)
		if _, dup := seen[s.CheckID]; !dup {
			seen[s.CheckID] = struct{}{}
			ids = append(ids, s.CheckID)
		}
		switch s.Tier {
		case TierHard:
			if !haveHard {
				primary = s // first hard signal wins as primary
				haveHard = true
			}
			if c := ClampConfidence(s.Confidence); c > maxHardConf {
				maxHardConf = c
			}
		case TierSoft:
			distinctSoft[s.CheckID] = struct{}{}
		}
		if i == 0 && !haveHard {
			primary = s // fall back to first signal until a hard one appears
		}
	}
	if !haveHard {
		primary = signals[0]
	}

	f := Finding{
		RuleID:      "detect." + primary.CheckID,
		Scanner:     scannerID,
		ThreatType:  primary.ThreatType,
		Category:    primary.ThreatType,
		Location:    fmt.Sprintf("%s:%s", tool.Server, tool.Name),
		Title:       findingTitle(primary, tool),
		Description: primary.Detail,
		Evidence:    primary.Evidence,
		Confidence:  ClampConfidence(confSum),
		Signals:     ids,
		Spans:       unionSpans(signals),
	}

	if haveHard {
		f.ThreatLevel = ThreatLevelDangerous
		if maxHardConf >= criticalConfidence {
			f.Severity = SeverityCritical
		} else {
			f.Severity = SeverityHigh
		}
	} else {
		f.ThreatLevel = ThreatLevelWarning
		f.Severity = softSeverity(len(distinctSoft))
	}
	return f, true
}

// unionSpans collects the highlight spans from EVERY signal, not just the
// primary one. aggregate emits exactly one Finding per tool and takes
// Evidence/Description from the primary alone; primary-wins is tolerable for
// prose and fatal for highlighting, because a description tripping both
// tpa.bundle and shadowing.cross_server would mark one rule's words and
// silently swallow the other's.
//
// Structurally impossible spans are dropped here rather than forwarded. Today
// every span in the tree comes from DescriptionSpan, which already refuses to
// build one; this is the backstop at the one seam every span crosses on its way
// into JSON, because Span is exported and checks live in a sibling package where
// a hand-built literal would otherwise reach the frontend unchecked. It can only
// judge structure — the field name and the range's own shape — since the tool
// text is not available here. See Span.valid.
//
// Duplicates are keyed on (Field, Start, End, CheckID) — deliberately NOT on
// Tier/Snippet — and the FIRST occurrence keeps its metadata, so which
// duplicate survives does not depend on signal ordering. The result is then
// sorted on that same key so output is byte-identical for any input ordering
// (the baseline-determinism tests compare whole reports with DeepEqual), and
// capped at MaxSpansPerFinding AFTER the sort, which keeps the earliest matches
// in the text rather than an arbitrary slice of the signal order.
func unionSpans(signals []Signal) []Span {
	type spanKey struct {
		field   SpanField
		start   int
		end     int
		checkID string
	}
	var out []Span
	seen := make(map[spanKey]struct{})
	for _, s := range signals {
		for _, sp := range s.Spans {
			if !sp.valid() {
				continue
			}
			k := spanKey{field: sp.Field, start: sp.Start, end: sp.End, checkID: sp.CheckID}
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, sp)
		}
	}
	if len(out) == 0 {
		return nil // absent key in JSON, never an empty array
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End < b.End
		}
		return a.CheckID < b.CheckID
	})
	if len(out) > MaxSpansPerFinding {
		out = out[:MaxSpansPerFinding]
	}
	return out
}

// softSeverity maps the count of distinct soft CheckIDs to a severity:
// 1→low, 2→medium, 3+→high.
func softSeverity(distinct int) string {
	switch {
	case distinct >= 3:
		return SeverityHigh
	case distinct == 2:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

func findingTitle(primary Signal, tool ToolView) string {
	name := tool.Name
	if name == "" {
		name = "tool"
	}
	return fmt.Sprintf("%s flagged on %s", primary.CheckID, name)
}
