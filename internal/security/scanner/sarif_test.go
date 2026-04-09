package scanner

import (
	"encoding/json"
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
