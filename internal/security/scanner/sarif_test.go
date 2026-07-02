package scanner

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseSARIFMinimal(t *testing.T) {
	data := []byte(`{
		"version": "2.1.0",
		"runs": [{
			"tool": {"driver": {"name": "test-scanner", "version": "1.0"}},
			"results": []
		}]
	}`)

	report, err := ParseSARIF(data)
	if err != nil {
		t.Fatalf("ParseSARIF: %v", err)
	}
	if len(report.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(report.Runs))
	}
	if report.Runs[0].Tool.Driver.Name != "test-scanner" {
		t.Errorf("unexpected driver name: %s", report.Runs[0].Tool.Driver.Name)
	}
}

func TestParseSARIFWithResults(t *testing.T) {
	data := []byte(`{
		"version": "2.1.0",
		"runs": [{
			"tool": {
				"driver": {
					"name": "mcp-scan",
					"version": "0.4.2",
					"rules": [{
						"id": "tool-poisoning",
						"shortDescription": {"text": "Tool poisoning attack detected"},
						"defaultConfiguration": {"level": "error"}
					}]
				}
			},
			"results": [{
				"ruleId": "tool-poisoning",
				"level": "error",
				"message": {"text": "Tool description contains hidden instructions targeting Claude"},
				"locations": [{
					"physicalLocation": {
						"artifactLocation": {"uri": "mcp_config.json"},
						"region": {"startLine": 15}
					}
				}]
			},
			{
				"ruleId": "prompt-injection",
				"level": "warning",
				"message": {"text": "Possible prompt injection in tool response"},
				"locations": [{
					"physicalLocation": {
						"artifactLocation": {"uri": "tools/search.py"},
						"region": {"startLine": 42, "startColumn": 5}
					}
				}]
			}]
		}]
	}`)

	report, err := ParseSARIF(data)
	if err != nil {
		t.Fatalf("ParseSARIF: %v", err)
	}
	if len(report.Runs[0].Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(report.Runs[0].Results))
	}

	r0 := report.Runs[0].Results[0]
	if r0.RuleID != "tool-poisoning" {
		t.Errorf("expected ruleId 'tool-poisoning', got %q", r0.RuleID)
	}
	if r0.Level != "error" {
		t.Errorf("expected level 'error', got %q", r0.Level)
	}
	if r0.Locations[0].PhysicalLocation.ArtifactLocation.URI != "mcp_config.json" {
		t.Errorf("unexpected URI: %s", r0.Locations[0].PhysicalLocation.ArtifactLocation.URI)
	}
}

func TestParseSARIFInvalidJSON(t *testing.T) {
	_, err := ParseSARIF([]byte(`{invalid`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseSARIFUnsupportedVersion(t *testing.T) {
	data := []byte(`{"version": "1.0.0", "runs": []}`)
	_, err := ParseSARIF(data)
	if err == nil {
		t.Error("expected error for unsupported version")
	}
}

func TestParseSARIFEmptyRuns(t *testing.T) {
	data := []byte(`{"version": "2.1.0", "runs": []}`)
	report, err := ParseSARIF(data)
	if err != nil {
		t.Fatalf("ParseSARIF: %v", err)
	}
	if len(report.Runs) != 0 {
		t.Error("expected 0 runs")
	}
}

func TestIsSARIF(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{"valid SARIF", `{"version":"2.1.0","runs":[]}`, true},
		{"SARIF 2.1.1", `{"version":"2.1.1","runs":[]}`, true},
		{"not SARIF", `{"findings":[]}`, false},
		{"wrong version", `{"version":"1.0","runs":[]}`, false},
		{"invalid JSON", `{bad`, false},
		{"empty", `{}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSARIF([]byte(tt.data)); got != tt.want {
				t.Errorf("IsSARIF() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeFindings(t *testing.T) {
	report := &SARIFReport{
		Runs: []SARIFRun{{
			Tool: SARIFTool{Driver: SARIFDriver{
				Name: "test-scanner",
				Rules: []SARIFRule{
					{
						ID:               "TPA-001",
						ShortDescription: &SARIFMessage{Text: "Tool Poisoning Attack"},
						DefaultConfig:    &SARIFConfiguration{Level: "error"},
						Properties:       map[string]any{"category": "tool-poisoning"},
					},
				},
			}},
			Results: []SARIFResult{
				{
					RuleID:  "TPA-001",
					Level:   "error",
					Message: SARIFMessage{Text: "Hidden instructions in tool description"},
					Locations: []SARIFLocation{{
						PhysicalLocation: &SARIFPhysicalLocation{
							ArtifactLocation: &SARIFArtifactLocation{URI: "config.json"},
							Region:           &SARIFRegion{StartLine: 10},
						},
					}},
				},
				{
					RuleID:  "INJ-002",
					Level:   "warning",
					Message: SARIFMessage{Text: "Possible injection vector"},
				},
				{
					RuleID:  "INFO-003",
					Level:   "note",
					Message: SARIFMessage{Text: "Configuration suggestion"},
				},
			},
		}},
	}

	findings := NormalizeFindings(report, "test-scanner")
	if len(findings) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(findings))
	}

	// First finding: error level, with rule enrichment
	f0 := findings[0]
	if f0.Severity != SeverityHigh {
		t.Errorf("f0 severity: expected %q, got %q", SeverityHigh, f0.Severity)
	}
	if f0.Title != "Tool Poisoning Attack" {
		t.Errorf("f0 title: expected enriched from rule, got %q", f0.Title)
	}
	if f0.Category != "tool-poisoning" {
		t.Errorf("f0 category: expected 'tool-poisoning', got %q", f0.Category)
	}
	if f0.Location != "config.json:10" {
		t.Errorf("f0 location: expected 'config.json:10', got %q", f0.Location)
	}
	if f0.Scanner != "test-scanner" {
		t.Errorf("f0 scanner: expected 'test-scanner', got %q", f0.Scanner)
	}

	// Second finding: warning level
	if findings[1].Severity != SeverityMedium {
		t.Errorf("f1 severity: expected %q, got %q", SeverityMedium, findings[1].Severity)
	}

	// Third finding: note level
	if findings[2].Severity != SeverityLow {
		t.Errorf("f2 severity: expected %q, got %q", SeverityLow, findings[2].Severity)
	}
}

func TestNormalizeFindingsNoLevel(t *testing.T) {
	report := &SARIFReport{
		Runs: []SARIFRun{{
			Tool: SARIFTool{Driver: SARIFDriver{Name: "scanner"}},
			Results: []SARIFResult{{
				RuleID:  "RULE-1",
				Message: SARIFMessage{Text: "No level specified"},
			}},
		}},
	}

	findings := NormalizeFindings(report, "scanner")
	if findings[0].Severity != SeverityMedium {
		t.Errorf("expected default severity %q, got %q", SeverityMedium, findings[0].Severity)
	}
}

func TestCalculateRiskScore(t *testing.T) {
	// Logarithmic formula: score = weight * log2(1 + count)
	// Dangerous: weight=25, cap=80. Warning: weight=6, cap=25. Info: weight=2, cap=10.
	// log2(2)=1.0, log2(3)=1.58, log2(5)=2.32, log2(7)=2.81
	tests := []struct {
		name     string
		findings []ScanFinding
		want     int
	}{
		{"no findings", nil, 0},
		{"one info", []ScanFinding{{ThreatLevel: ThreatLevelInfo}}, 2},            // 2*log2(2)=2
		{"one warning", []ScanFinding{{ThreatLevel: ThreatLevelWarning}}, 6},      // 6*log2(2)=6
		{"one dangerous", []ScanFinding{{ThreatLevel: ThreatLevelDangerous}}, 25}, // 25*log2(2)=25
		{"6 warnings + 1 info", []ScanFinding{
			{ThreatLevel: ThreatLevelWarning},
			{ThreatLevel: ThreatLevelWarning},
			{ThreatLevel: ThreatLevelWarning},
			{ThreatLevel: ThreatLevelWarning},
			{ThreatLevel: ThreatLevelWarning},
			{ThreatLevel: ThreatLevelWarning},
			{ThreatLevel: ThreatLevelInfo},
		}, 18}, // 6*log2(7)=16 + 2*log2(2)=2 = 18
		{"dangerous + warnings", []ScanFinding{
			{ThreatLevel: ThreatLevelDangerous},
			{ThreatLevel: ThreatLevelWarning},
			{ThreatLevel: ThreatLevelWarning},
			{ThreatLevel: ThreatLevelInfo},
		}, 36}, // 25*log2(2)=25 + 6*log2(3)=9 + 2*log2(2)=2 = 36
		{"4 dangerous - diminishing returns", []ScanFinding{
			{ThreatLevel: ThreatLevelDangerous},
			{ThreatLevel: ThreatLevelDangerous},
			{ThreatLevel: ThreatLevelDangerous},
			{ThreatLevel: ThreatLevelDangerous},
		}, 58}, // 25*log2(5)=58 (not 120 like old linear)
		{"unclassified fallback", []ScanFinding{
			{Severity: SeverityCritical},
			{Severity: SeverityHigh},
			{Severity: SeverityLow},
		}, 33}, // dangerous:1→25 + warning:1→6 + info:1→2 = 33
		{"dedup same rule+location", []ScanFinding{
			{RuleID: "MCP-TP-003", Location: "tool:add_numbers", ThreatLevel: ThreatLevelDangerous, Scanner: "scanner-a"},
			{RuleID: "MCP-TP-003", Location: "tool:add_numbers", ThreatLevel: ThreatLevelDangerous, Scanner: "scanner-b"},
			{RuleID: "MCP-TP-003", Location: "tool:add_numbers", ThreatLevel: ThreatLevelDangerous, Scanner: "scanner-c"},
		}, 25}, // Deduped to 1 unique dangerous → 25 (not 3x)
		{"dedup different locations kept", []ScanFinding{
			{RuleID: "MCP-TP-003", Location: "tool:add_numbers", ThreatLevel: ThreatLevelDangerous},
			{RuleID: "MCP-TP-003", Location: "tool:send_message", ThreatLevel: ThreatLevelDangerous},
		}, 39}, // 2 unique dangerous → 25*log2(3)=39
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalculateRiskScore(tt.findings)
			if got != tt.want {
				t.Errorf("CalculateRiskScore() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCalculateRiskScoreConsensusRaisesScore proves Spec-076 FR-006 / SC-007:
// independent signals on one tool ADD to the risk score instead of being
// collapsed, so a tool flagged by several checks scores higher than one flagged
// by a single check (consensus is visible). The deterministic scanner emits ONE
// ScanFinding per tool carrying every contributing check in Signals; the score
// must weigh that finding by its signal count.
func TestCalculateRiskScoreConsensusRaisesScore(t *testing.T) {
	single := []ScanFinding{{
		RuleID:      "detect.unicode.hidden",
		Location:    "srv:tool_a",
		ThreatLevel: ThreatLevelWarning,
		Signals:     []string{"unicode.hidden"},
	}}
	consensus := []ScanFinding{{
		RuleID:      "detect.unicode.hidden",
		Location:    "srv:tool_a",
		ThreatLevel: ThreatLevelWarning,
		Signals:     []string{"unicode.hidden", "directive.imperative", "capability.mismatch"},
	}}

	singleScore := CalculateRiskScore(single)
	consensusScore := CalculateRiskScore(consensus)

	// single: 1 signal → warning count 1 → 6*log2(2)=6
	if singleScore != 6 {
		t.Errorf("single-signal score = %d, want 6", singleScore)
	}
	// consensus: 3 signals → warning count 3 → 6*log2(4)=12
	if consensusScore != 12 {
		t.Errorf("consensus score = %d, want 12", consensusScore)
	}
	if consensusScore <= singleScore {
		t.Errorf("consensus score %d must exceed single-signal score %d", consensusScore, singleScore)
	}
}

// TestCalculateRiskScoreCrossScannerDedupRetained guards that the consensus
// change does NOT break the legitimate cross-scanner de-duplication: the same
// finding (rule_id+location) reported by multiple scanners with no per-signal
// data still counts once. Only independent signals WITHIN a finding add.
func TestCalculateRiskScoreCrossScannerDedupRetained(t *testing.T) {
	findings := []ScanFinding{
		{RuleID: "MCP-TP-003", Location: "tool:add_numbers", ThreatLevel: ThreatLevelDangerous, Scanner: "scanner-a"},
		{RuleID: "MCP-TP-003", Location: "tool:add_numbers", ThreatLevel: ThreatLevelDangerous, Scanner: "scanner-b"},
	}
	if got := CalculateRiskScore(findings); got != 25 {
		t.Errorf("cross-scanner duplicate score = %d, want 25 (deduped to one dangerous)", got)
	}
}

// TestMergeFindingsDedupByRuleAndLocation proves Spec 077 FR-010/FR-011: two
// scanners reporting the same issue (same rule_id + location) collapse into a
// single unified entry whose Sources lists every contributing scanner.
func TestMergeFindingsDedupByRuleAndLocation(t *testing.T) {
	findings := []ScanFinding{
		{RuleID: "detect.tpa", Location: "srv:tool", ThreatType: ThreatToolPoisoning, ThreatLevel: ThreatLevelDangerous, Scanner: "tpa-descriptions", Sources: []string{"tpa-descriptions"}},
		{RuleID: "detect.tpa", Location: "srv:tool", ThreatType: ThreatToolPoisoning, ThreatLevel: ThreatLevelDangerous, Scanner: "cisco-mcp-scanner", Sources: []string{"cisco-mcp-scanner"}},
	}

	merged := MergeFindings(findings)
	if len(merged) != 1 {
		t.Fatalf("expected exactly 1 merged finding, got %d: %+v", len(merged), merged)
	}
	if len(merged[0].Sources) != 2 {
		t.Fatalf("expected merged finding to carry 2 sources, got %v", merged[0].Sources)
	}
	// Sources must be a stable (sorted) union so the wire output is deterministic.
	if merged[0].Sources[0] != "cisco-mcp-scanner" || merged[0].Sources[1] != "tpa-descriptions" {
		t.Errorf("expected sorted sources [cisco-mcp-scanner tpa-descriptions], got %v", merged[0].Sources)
	}
}

// TestMergeFindingsAbsorbsStrongerSeverity proves Spec 077 (US2): when a
// low/info duplicate and a high/warning duplicate share the same
// (rule_id, location), the merged finding takes the MORE-SEVERE Severity and
// ThreatLevel (alongside max Confidence and most-severe Tier) regardless of the
// order in which the two are presented. Absorbing only some of the stronger
// fields would make CalculateRiskScore and the summary order-dependent.
func TestMergeFindingsAbsorbsStrongerSeverity(t *testing.T) {
	weak := ScanFinding{
		RuleID: "detect.tpa", Location: "srv:tool", ThreatType: ThreatToolPoisoning,
		Severity: SeverityInfo, ThreatLevel: ThreatLevelInfo, Tier: TierSoft,
		Confidence: 0.3, Scanner: "scanner-a", Sources: []string{"scanner-a"},
	}
	strong := ScanFinding{
		RuleID: "detect.tpa", Location: "srv:tool", ThreatType: ThreatToolPoisoning,
		Severity: SeverityHigh, ThreatLevel: ThreatLevelWarning, Tier: TierHard,
		Confidence: 0.9, Scanner: "scanner-b", Sources: []string{"scanner-b"},
	}

	assertMerged := func(t *testing.T, merged []ScanFinding) {
		t.Helper()
		if len(merged) != 1 {
			t.Fatalf("expected exactly 1 merged finding, got %d: %+v", len(merged), merged)
		}
		f := merged[0]
		if f.Severity != SeverityHigh {
			t.Errorf("expected merged Severity=high, got %q", f.Severity)
		}
		if f.ThreatLevel != ThreatLevelWarning {
			t.Errorf("expected merged ThreatLevel=warning, got %q", f.ThreatLevel)
		}
		if f.Tier != TierHard {
			t.Errorf("expected merged Tier=hard, got %q", f.Tier)
		}
		// Max of the two confidences (0.9), possibly raised further by the
		// two-source consensus boost — never the weak 0.3.
		if f.Confidence < 0.9 {
			t.Errorf("expected merged Confidence>=0.9, got %v", f.Confidence)
		}
	}

	weakFirst := MergeFindings([]ScanFinding{weak, strong})
	strongFirst := MergeFindings([]ScanFinding{strong, weak})
	assertMerged(t, weakFirst)
	assertMerged(t, strongFirst)

	// The whole point: the merge — and therefore the aggregate risk score — is
	// order-independent. A weak-then-strong ordering must not score lower than a
	// strong-then-weak ordering.
	if got, want := CalculateRiskScore(weakFirst), CalculateRiskScore(strongFirst); got != want {
		t.Errorf("CalculateRiskScore is order-dependent: weak-first=%d strong-first=%d", got, want)
	}
	// And both must reflect the high/warning severity, not the info floor.
	if s := CalculateRiskScore(weakFirst); s == 0 {
		t.Errorf("expected non-zero risk score after absorbing the warning-level duplicate, got %d", s)
	}
}

// TestMergeFindingsConsensusBoostsConfidence proves Spec 077 FR-012: when two
// independent sources agree on the same (location, threat_type) — even via
// different rule ids — the merged finding's confidence rises above the
// single-source confidence.
func TestMergeFindingsConsensusBoostsConfidence(t *testing.T) {
	single := MergeFindings([]ScanFinding{
		{RuleID: "detect.tpa", Location: "srv:tool", ThreatType: ThreatToolPoisoning, ThreatLevel: ThreatLevelDangerous, Scanner: "tpa-descriptions", Confidence: 0.6},
	})
	if len(single) != 1 {
		t.Fatalf("expected 1 finding for single source, got %d", len(single))
	}
	singleConf := single[0].Confidence

	consensus := MergeFindings([]ScanFinding{
		{RuleID: "detect.tpa", Location: "srv:tool", ThreatType: ThreatToolPoisoning, ThreatLevel: ThreatLevelDangerous, Scanner: "tpa-descriptions", Confidence: 0.6},
		{RuleID: "cisco.poison", Location: "srv:tool", ThreatType: ThreatToolPoisoning, ThreatLevel: ThreatLevelDangerous, Scanner: "cisco-mcp-scanner", Confidence: 0.5},
	})

	// The tpa-descriptions finding (same rule as the single-source case) must be
	// boosted by the agreement of the second, independent source.
	var boosted float64
	found := false
	for _, f := range consensus {
		if f.RuleID == "detect.tpa" {
			boosted = f.Confidence
			found = true
		}
	}
	if !found {
		t.Fatalf("expected detect.tpa finding to survive merge, got %+v", consensus)
	}
	if boosted <= singleConf {
		t.Errorf("consensus confidence %v must exceed single-source confidence %v", boosted, singleConf)
	}
}

// TestCalculateRiskScoreCrossSourceConsensusAdds proves Spec 077 (T020): two
// independent external/Docker scanners agreeing on the same (location,
// threat_type) via different rule ids ADD to the consensus weight instead of
// each flattening to weight 1, so the score exceeds a single-source scan.
func TestCalculateRiskScoreCrossSourceConsensusAdds(t *testing.T) {
	single := []ScanFinding{
		{RuleID: "cisco.x", Location: "srv:tool", ThreatType: ThreatToolPoisoning, ThreatLevel: ThreatLevelDangerous, Scanner: "cisco-mcp-scanner"},
	}
	consensus := []ScanFinding{
		{RuleID: "cisco.x", Location: "srv:tool", ThreatType: ThreatToolPoisoning, ThreatLevel: ThreatLevelDangerous, Scanner: "cisco-mcp-scanner"},
		{RuleID: "ramparts.y", Location: "srv:tool", ThreatType: ThreatToolPoisoning, ThreatLevel: ThreatLevelDangerous, Scanner: "ramparts"},
	}

	singleScore := CalculateRiskScore(single)
	consensusScore := CalculateRiskScore(consensus)

	// single: one dangerous, one source → weight 1 → 25*log2(2)=25
	if singleScore != 25 {
		t.Errorf("single-source score = %d, want 25", singleScore)
	}
	// consensus: one issue (location+threat_type), two distinct sources → weight
	// 2 → 25*log2(3)=39. Counted once (not double), but heavier than single.
	if consensusScore != 39 {
		t.Errorf("cross-source consensus score = %d, want 39", consensusScore)
	}
	if consensusScore <= singleScore {
		t.Errorf("consensus score %d must exceed single-source score %d", consensusScore, singleScore)
	}
}

// TestCalculateRiskScoreConsensusUsesMaxSeverity proves the consensus group is
// scored at its MOST-SEVERE member's threat level, not whichever finding is
// encountered first. For severity-derived threat_types (supply_chain here)
// agreeing findings can carry different threat_levels; the previous code counted
// the group at the first finding's level, making the score order-dependent and
// able to drop a genuine warning. The score must be identical in both orders and
// reflect the warning (high) member, not the info (low) one.
//
// It also guards Codex R2 #1: the weaker (info) member shares only the coarse
// (location, threat_type) — it does NOT itself rate the issue at warning, so it
// must NOT add weight to the warning bucket. The warning bucket is weighted by
// its single genuine warning source (weight 1), not by the raw source count (2).
func TestCalculateRiskScoreConsensusUsesMaxSeverity(t *testing.T) {
	warningFinding := ScanFinding{
		RuleID:      "trivy.CVE-1",
		Location:    "srv:tool",
		ThreatType:  ThreatSupplyChain,
		ThreatLevel: ThreatLevelWarning,
		Severity:    SeverityHigh,
		Scanner:     "trivy",
	}
	infoFinding := ScanFinding{
		RuleID:      "grype.CVE-2",
		Location:    "srv:tool",
		ThreatType:  ThreatSupplyChain,
		ThreatLevel: ThreatLevelInfo,
		Severity:    SeverityLow,
		Scanner:     "grype",
	}

	ab := CalculateRiskScore([]ScanFinding{warningFinding, infoFinding})
	ba := CalculateRiskScore([]ScanFinding{infoFinding, warningFinding})

	if ab != ba {
		t.Fatalf("consensus score is order-dependent: [warning,info]=%d [info,warning]=%d", ab, ba)
	}
	// Scored at the warning level (the max), NOT the info level (2*log2(2)=2 would
	// be the bug of using the first finding). Only one source (trivy) rated it
	// warning, so the warning bucket weight is 1: 6*log2(2)=6. The info member
	// does not inflate the warning bucket (Codex R2 #1).
	if ab != 6 {
		t.Errorf("consensus score = %d, want 6 (warning-level, 1 warning-rated source)", ab)
	}
}

// TestCalculateRiskScoreConsensusWeakDoesNotInflateStrong proves Codex R2 #1
// directly: a HARD dangerous finding and a low/info finding that merely share a
// (location, threat_type) must score the DANGEROUS bucket at weight 1 (the info
// finding does not rate the issue as dangerous, so it adds no dangerous weight),
// while two genuine dangerous findings from distinct sources score at weight 2.
// The result is order-independent.
func TestCalculateRiskScoreConsensusWeakDoesNotInflateStrong(t *testing.T) {
	dangerous := ScanFinding{
		RuleID:      "detect.tpa",
		Location:    "srv:tool",
		ThreatType:  ThreatToolPoisoning,
		ThreatLevel: ThreatLevelDangerous,
		Scanner:     "tpa-descriptions",
	}
	info := ScanFinding{
		RuleID:      "cisco.hint",
		Location:    "srv:tool",
		ThreatType:  ThreatToolPoisoning,
		ThreatLevel: ThreatLevelInfo,
		Scanner:     "cisco-mcp-scanner",
	}
	dangerous2 := ScanFinding{
		RuleID:      "ramparts.y",
		Location:    "srv:tool",
		ThreatType:  ThreatToolPoisoning,
		ThreatLevel: ThreatLevelDangerous,
		Scanner:     "ramparts",
	}

	// dangerous + info: two sources gate consensus, but only ONE rated it
	// dangerous → dangerous weight 1 → 25*log2(2)=25 (same as a lone dangerous).
	dangerFirst := CalculateRiskScore([]ScanFinding{dangerous, info})
	infoFirst := CalculateRiskScore([]ScanFinding{info, dangerous})
	if dangerFirst != infoFirst {
		t.Fatalf("weak+strong score is order-dependent: danger-first=%d info-first=%d", dangerFirst, infoFirst)
	}
	if dangerFirst != 25 {
		t.Errorf("dangerous+info consensus score = %d, want 25 (weight 1, info must not inflate dangerous)", dangerFirst)
	}
	if lone := CalculateRiskScore([]ScanFinding{dangerous}); dangerFirst != lone {
		t.Errorf("dangerous+info score %d must equal lone-dangerous score %d — the info adds no dangerous weight", dangerFirst, lone)
	}

	// Two genuine dangerous sources DO boost the dangerous bucket to weight 2 →
	// 25*log2(3)=39. Order-independent.
	twoAB := CalculateRiskScore([]ScanFinding{dangerous, dangerous2})
	twoBA := CalculateRiskScore([]ScanFinding{dangerous2, dangerous})
	if twoAB != twoBA {
		t.Fatalf("two-dangerous score is order-dependent: %d vs %d", twoAB, twoBA)
	}
	if twoAB != 39 {
		t.Errorf("two dangerous sources consensus score = %d, want 39 (weight 2)", twoAB)
	}
	if twoAB <= dangerFirst {
		t.Errorf("genuine dangerous consensus %d must exceed the non-inflated weak+strong %d", twoAB, dangerFirst)
	}
}

// TestMergeFindingsAbsorbsStrongerFields proves that phase-1 dedup keeps the
// absorbed duplicate's stronger fields: on merge the result takes max(Confidence),
// the more-severe Tier (hard > soft), and the union of Signals — regardless of
// which finding is encountered first.
func TestMergeFindingsAbsorbsStrongerFields(t *testing.T) {
	hard := ScanFinding{
		RuleID: "detect.tpa", Location: "srv:tool",
		Tier: TierHard, Confidence: 0.9, Signals: []string{"unicode.hidden"},
		Scanner: "tpa-descriptions",
	}
	soft := ScanFinding{
		RuleID: "detect.tpa", Location: "srv:tool",
		Tier: TierSoft, Confidence: 0.2, Signals: []string{"directive.imperative"},
		Scanner: "cisco-mcp-scanner",
	}

	for _, tc := range []struct {
		name string
		in   []ScanFinding
	}{
		{"soft-first", []ScanFinding{soft, hard}},
		{"hard-first", []ScanFinding{hard, soft}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merged := MergeFindings(tc.in)
			if len(merged) != 1 {
				t.Fatalf("expected 1 merged finding, got %d: %+v", len(merged), merged)
			}
			m := merged[0]
			if m.Tier != TierHard {
				t.Errorf("merged tier = %q, want hard (more-severe tier must win)", m.Tier)
			}
			if m.Confidence != 0.9 {
				t.Errorf("merged confidence = %v, want 0.9 (max)", m.Confidence)
			}
			if len(m.Signals) != 2 {
				t.Errorf("merged signals = %v, want union of both (2)", m.Signals)
			}
		})
	}
}

// TestMergeFindingsBackfillsEnrichmentMetadata proves Codex R2 #2: when a
// duplicate is absorbed on (rule_id, location) dedup, the kept finding fills any
// field it lacks from the duplicate instead of dropping it — HelpURI, CVSSScore,
// package/version metadata, Evidence, category/title/description, scan pass and
// the supply-chain flag. The result is identical in both merge orders.
func TestMergeFindingsBackfillsEnrichmentMetadata(t *testing.T) {
	// bare carries the threat classification but none of the enrichment.
	// Attribution rides Sources (Spec 077); the legacy per-anchor Scanner field is
	// left empty so the DeepEqual order-independence check below is exact.
	bare := ScanFinding{
		RuleID: "CVE-2025-1", Location: "srv:tool", ThreatType: ThreatSupplyChain,
		Severity: SeverityHigh, ThreatLevel: ThreatLevelWarning,
		Sources: []string{"scanner-a"},
	}
	// enriched shares the (rule_id, location) but adds all the metadata.
	enriched := ScanFinding{
		RuleID: "CVE-2025-1", Location: "srv:tool", ThreatType: ThreatSupplyChain,
		Severity: SeverityHigh, ThreatLevel: ThreatLevelWarning,
		Category: "supply-chain", Title: "Vulnerable dependency",
		Description: "Package foo has a known CVE", HelpURI: "https://cve/CVE-2025-1",
		CVSSScore: 7.5, PackageName: "foo", InstalledVersion: "1.0.0",
		FixedVersion: "1.1.0", ScanPass: 2, Evidence: "foo@1.0.0",
		SupplyChainAudit: true, Sources: []string{"scanner-b"},
	}

	assertEnriched := func(t *testing.T, merged []ScanFinding) {
		t.Helper()
		if len(merged) != 1 {
			t.Fatalf("expected exactly 1 merged finding, got %d: %+v", len(merged), merged)
		}
		f := merged[0]
		if f.HelpURI != "https://cve/CVE-2025-1" {
			t.Errorf("HelpURI = %q, want backfilled from duplicate", f.HelpURI)
		}
		if f.CVSSScore != 7.5 {
			t.Errorf("CVSSScore = %v, want 7.5 (max)", f.CVSSScore)
		}
		if f.PackageName != "foo" || f.InstalledVersion != "1.0.0" || f.FixedVersion != "1.1.0" {
			t.Errorf("package metadata not backfilled: %+v", f)
		}
		if f.Evidence != "foo@1.0.0" {
			t.Errorf("Evidence = %q, want backfilled", f.Evidence)
		}
		if f.Category != "supply-chain" || f.Title == "" || f.Description == "" {
			t.Errorf("category/title/description not backfilled: %+v", f)
		}
		if f.ScanPass != 2 {
			t.Errorf("ScanPass = %d, want 2 (backfilled)", f.ScanPass)
		}
		if !f.SupplyChainAudit {
			t.Errorf("SupplyChainAudit = false, want true (OR of both)")
		}
	}

	bareFirst := MergeFindings([]ScanFinding{bare, enriched})
	enrichedFirst := MergeFindings([]ScanFinding{enriched, bare})
	assertEnriched(t, bareFirst)
	assertEnriched(t, enrichedFirst)

	// Order-independence: the merged finding is field-for-field identical whether
	// the enriched or the bare duplicate is the merge anchor.
	if !reflect.DeepEqual(bareFirst, enrichedFirst) {
		t.Errorf("merge is order-dependent:\n bare-first    = %+v\n enriched-first= %+v", bareFirst, enrichedFirst)
	}
}

// TestClassifyThreatBackfillsSeverity proves Spec 077 (T022): a finding that
// arrives with no severity (as some external/legacy SARIF findings do) is given
// a user-readable severity derived from its classified threat level.
func TestClassifyThreatBackfillsSeverity(t *testing.T) {
	tests := []struct {
		name        string
		in          ScanFinding
		wantSevSet  bool
		wantMinKeep string // if in.Severity already set, it must be preserved
	}{
		{
			name:       "dangerous tool poisoning with no severity",
			in:         ScanFinding{RuleID: "tool-poisoning", Description: "hidden instruction"},
			wantSevSet: true,
		},
		{
			name:       "supply chain cve with no severity",
			in:         ScanFinding{RuleID: "CVE-2025-0001", PackageName: "lodash"},
			wantSevSet: true,
		},
		{
			name:        "explicit severity preserved",
			in:          ScanFinding{RuleID: "anything", Severity: SeverityCritical},
			wantSevSet:  true,
			wantMinKeep: SeverityCritical,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.in
			ClassifyThreat(&f)
			if tt.wantSevSet && f.Severity == "" {
				t.Errorf("expected severity to be backfilled, got empty")
			}
			if tt.wantMinKeep != "" && f.Severity != tt.wantMinKeep {
				t.Errorf("expected severity %q preserved, got %q", tt.wantMinKeep, f.Severity)
			}
		})
	}
}

func TestSummarizeFindings(t *testing.T) {
	findings := []ScanFinding{
		{Severity: SeverityCritical},
		{Severity: SeverityHigh},
		{Severity: SeverityHigh},
		{Severity: SeverityMedium},
		{Severity: SeverityLow},
		{Severity: SeverityInfo},
	}

	summary := SummarizeFindings(findings)
	if summary.Critical != 1 {
		t.Errorf("Critical: want 1, got %d", summary.Critical)
	}
	if summary.High != 2 {
		t.Errorf("High: want 2, got %d", summary.High)
	}
	if summary.Medium != 1 {
		t.Errorf("Medium: want 1, got %d", summary.Medium)
	}
	if summary.Low != 1 {
		t.Errorf("Low: want 1, got %d", summary.Low)
	}
	if summary.Info != 1 {
		t.Errorf("Info: want 1, got %d", summary.Info)
	}
	if summary.Total != 6 {
		t.Errorf("Total: want 6, got %d", summary.Total)
	}
}

func TestSARIFRoundTrip(t *testing.T) {
	// Test that we can parse and re-serialize
	original := &SARIFReport{
		Version: "2.1.0",
		Runs: []SARIFRun{{
			Tool: SARIFTool{Driver: SARIFDriver{Name: "test", Version: "1.0"}},
			Results: []SARIFResult{{
				RuleID:  "R1",
				Level:   "error",
				Message: SARIFMessage{Text: "found something"},
			}},
		}},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	parsed, err := ParseSARIF(data)
	if err != nil {
		t.Fatalf("ParseSARIF: %v", err)
	}

	if parsed.Runs[0].Tool.Driver.Name != "test" {
		t.Error("round-trip failed: driver name mismatch")
	}
	if parsed.Runs[0].Results[0].RuleID != "R1" {
		t.Error("round-trip failed: ruleId mismatch")
	}
}

// TestClassifyThreat_PreservesBaselineTier locks Spec 077 US1 Codex finding #2:
// the legacy keyword classifier must NOT rewrite a baseline detect finding (one
// that carries a Tier). Before the fix, ClassifyThreat re-derived threat_level
// from the description keywords, so a HARD finding whose text lacked a
// "dangerous" keyword was downgraded dangerous→warning while its Tier stayed
// "hard" — breaking the tier↔level coupling the summary and approval gate rely
// on. A baseline finding must pass through untouched.
func TestClassifyThreat_PreservesBaselineTier(t *testing.T) {
	// A hard phrase_injection finding whose description contains no keyword the
	// classifier would map to "dangerous" (it would otherwise fall through to the
	// default branch and be set to "warning" at High severity).
	f := ScanFinding{
		RuleID:      "phrase.injection",
		Severity:    SeverityHigh,
		Category:    "phrase_injection",
		ThreatType:  ThreatPromptInjection,
		ThreatLevel: ThreatLevelDangerous,
		Title:       "Curated injection directive",
		Description: "Description contains a high-confidence directive to the agent.",
		Tier:        TierHard,
	}
	ClassifyThreat(&f)
	if f.ThreatLevel != ThreatLevelDangerous {
		t.Errorf("baseline hard finding downgraded: threat_level = %q, want %q", f.ThreatLevel, ThreatLevelDangerous)
	}
	if f.Tier != TierHard {
		t.Errorf("Tier mutated to %q, want %q", f.Tier, TierHard)
	}
	// The hard/dangerous coupling isBlockingFinding depends on must survive.
	if !isBlockingFinding(f) {
		t.Error("hard finding must remain blocking after classification")
	}

	// A soft baseline finding must likewise not be promoted to dangerous even
	// though its threat_type ("prompt_injection") matches a dangerous keyword.
	soft := ScanFinding{
		RuleID:      "directive.imperative",
		Severity:    SeverityHigh,
		Category:    "prompt_injection",
		ThreatType:  ThreatPromptInjection,
		ThreatLevel: ThreatLevelWarning,
		Description: "prompt injection phrasing present but soft-tier",
		Tier:        TierSoft,
	}
	ClassifyThreat(&soft)
	if soft.ThreatLevel != ThreatLevelWarning {
		t.Errorf("soft baseline finding rewritten: threat_level = %q, want %q", soft.ThreatLevel, ThreatLevelWarning)
	}
	if isBlockingFinding(soft) {
		t.Error("soft finding must never block, even with an injection threat_type")
	}
}

// TestClassifyThreat_StillClassifiesLegacy proves the guard is scoped to
// baseline findings only: a legacy/external finding (no Tier) is still
// classified by keyword as before, so back-compat is preserved.
func TestClassifyThreat_StillClassifiesLegacy(t *testing.T) {
	f := ScanFinding{
		RuleID:      "cisco-mcp-001",
		Severity:    SeverityHigh,
		Category:    "prompt-injection",
		Description: "detected prompt injection payload",
		// no Tier — legacy finding
	}
	ClassifyThreat(&f)
	if f.ThreatType != ThreatPromptInjection {
		t.Errorf("legacy finding threat_type = %q, want %q", f.ThreatType, ThreatPromptInjection)
	}
	if f.ThreatLevel != ThreatLevelDangerous {
		t.Errorf("legacy finding threat_level = %q, want %q", f.ThreatLevel, ThreatLevelDangerous)
	}
}
