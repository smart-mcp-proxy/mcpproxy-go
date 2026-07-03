package detect

import "fmt"

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
