package scanner

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// SARIF 2.1.0 types for parsing scanner output

// SARIFReport represents the top-level SARIF 2.1.0 document
type SARIFReport struct {
	Version string     `json:"version"`
	Schema  string     `json:"$schema,omitempty"`
	Runs    []SARIFRun `json:"runs"`
}

// SARIFRun represents a single scanner execution run
type SARIFRun struct {
	Tool    SARIFTool     `json:"tool"`
	Results []SARIFResult `json:"results"`
}

// SARIFTool describes the scanner that produced results
type SARIFTool struct {
	Driver SARIFDriver `json:"driver"`
}

// SARIFDriver describes the scanner driver
type SARIFDriver struct {
	Name    string      `json:"name"`
	Version string      `json:"version,omitempty"`
	Rules   []SARIFRule `json:"rules,omitempty"`
}

// SARIFRule defines a detection rule
type SARIFRule struct {
	ID               string              `json:"id"`
	ShortDescription *SARIFMessage       `json:"shortDescription,omitempty"`
	FullDescription  *SARIFMessage       `json:"fullDescription,omitempty"`
	DefaultConfig    *SARIFConfiguration `json:"defaultConfiguration,omitempty"`
	HelpURI          string              `json:"helpUri,omitempty"`
	Properties       map[string]any      `json:"properties,omitempty"`
}

// SARIFConfiguration holds rule configuration
type SARIFConfiguration struct {
	Level string `json:"level,omitempty"`
}

// SARIFResult represents an individual finding
type SARIFResult struct {
	RuleID     string          `json:"ruleId,omitempty"`
	Level      string          `json:"level,omitempty"` // "error", "warning", "note", "none"
	Message    SARIFMessage    `json:"message"`
	Locations  []SARIFLocation `json:"locations,omitempty"`
	Properties map[string]any  `json:"properties,omitempty"`
}

// SARIFMessage holds text content
type SARIFMessage struct {
	Text string `json:"text"`
}

// SARIFLocation describes where a finding was detected
type SARIFLocation struct {
	PhysicalLocation *SARIFPhysicalLocation `json:"physicalLocation,omitempty"`
}

// SARIFPhysicalLocation describes the physical file location
type SARIFPhysicalLocation struct {
	ArtifactLocation *SARIFArtifactLocation `json:"artifactLocation,omitempty"`
	Region           *SARIFRegion           `json:"region,omitempty"`
}

// SARIFArtifactLocation describes a file path
type SARIFArtifactLocation struct {
	URI string `json:"uri"`
}

// SARIFRegion describes a region within a file
type SARIFRegion struct {
	StartLine   int `json:"startLine,omitempty"`
	StartColumn int `json:"startColumn,omitempty"`
	EndLine     int `json:"endLine,omitempty"`
	EndColumn   int `json:"endColumn,omitempty"`
}

// ParseSARIF parses a SARIF 2.1.0 JSON document
func ParseSARIF(data []byte) (*SARIFReport, error) {
	var report SARIFReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("failed to parse SARIF: %w", err)
	}
	if report.Version != "" && !strings.HasPrefix(report.Version, "2.1") {
		return nil, fmt.Errorf("unsupported SARIF version: %s (expected 2.1.x)", report.Version)
	}
	if len(report.Runs) == 0 {
		return &report, nil
	}
	return &report, nil
}

// IsSARIF checks if the given data looks like a SARIF document
func IsSARIF(data []byte) bool {
	var probe struct {
		Version string `json:"version"`
		Runs    []any  `json:"runs"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return strings.HasPrefix(probe.Version, "2.1") && probe.Runs != nil
}

// NormalizeFindings converts SARIF results into normalized ScanFindings
func NormalizeFindings(report *SARIFReport, scannerID string) []ScanFinding {
	var findings []ScanFinding

	for _, run := range report.Runs {
		// Build a rule lookup for enriching findings
		rules := make(map[string]SARIFRule)
		for _, rule := range run.Tool.Driver.Rules {
			rules[rule.ID] = rule
		}

		for _, result := range run.Results {
			finding := ScanFinding{
				RuleID:      result.RuleID,
				Severity:    mapSARIFLevel(result.Level, result.RuleID, rules),
				Title:       result.Message.Text,
				Description: result.Message.Text,
				Scanner:     scannerID,
			}

			// Extract category from rule properties or rule ID
			finding.Category = categorizeFromRule(result.RuleID, result.Properties, rules)

			// Extract location
			if len(result.Locations) > 0 && result.Locations[0].PhysicalLocation != nil {
				pl := result.Locations[0].PhysicalLocation
				if pl.ArtifactLocation != nil {
					finding.Location = pl.ArtifactLocation.URI
					if pl.Region != nil && pl.Region.StartLine > 0 {
						finding.Location += fmt.Sprintf(":%d", pl.Region.StartLine)
					}
				}
			}

			// Enrich from SARIF rule metadata
			if rule, ok := rules[result.RuleID]; ok {
				if rule.ShortDescription != nil && rule.ShortDescription.Text != "" {
					finding.Title = rule.ShortDescription.Text
				}
				// Extract help URI (link to CVE/advisory)
				if rule.HelpURI != "" {
					finding.HelpURI = rule.HelpURI
				}
				// Extract CVSS score from properties
				if rule.Properties != nil {
					if score, ok := rule.Properties["security-severity"]; ok {
						switch v := score.(type) {
						case float64:
							finding.CVSSScore = v
						case string:
							if parsed, err := strconv.ParseFloat(v, 64); err == nil {
								finding.CVSSScore = parsed
							}
						}
					}
				}
			}

			// Parse package info from Trivy-style message text
			// Format: "Package: name\nInstalled Version: x\nVulnerability CVE-xxx\nFixed Version: y"
			finding.PackageName, finding.InstalledVersion, finding.FixedVersion = parsePackageFromMessage(result.Message.Text)

			findings = append(findings, finding)
		}
	}

	return findings
}

// mapSARIFLevel maps SARIF level to our severity constants
func mapSARIFLevel(level, ruleID string, rules map[string]SARIFRule) string {
	// First check the result-level override
	switch strings.ToLower(level) {
	case "error":
		return SeverityHigh
	case "warning":
		return SeverityMedium
	case "note":
		return SeverityLow
	case "none":
		return SeverityInfo
	}

	// Fall back to rule default configuration
	if rule, ok := rules[ruleID]; ok && rule.DefaultConfig != nil {
		switch strings.ToLower(rule.DefaultConfig.Level) {
		case "error":
			return SeverityHigh
		case "warning":
			return SeverityMedium
		case "note":
			return SeverityLow
		case "none":
			return SeverityInfo
		}
	}

	// Default to medium if no level specified
	return SeverityMedium
}

// categorizeFromRule extracts a category from rule metadata
func categorizeFromRule(ruleID string, props map[string]any, rules map[string]SARIFRule) string {
	// Check result properties first
	if props != nil {
		if cat, ok := props["category"].(string); ok {
			return cat
		}
		if tags, ok := props["tags"].([]any); ok && len(tags) > 0 {
			if tag, ok := tags[0].(string); ok {
				return tag
			}
		}
	}

	// Check rule properties
	if rule, ok := rules[ruleID]; ok && rule.Properties != nil {
		if cat, ok := rule.Properties["category"].(string); ok {
			return cat
		}
	}

	// Infer from rule ID prefix
	if ruleID != "" {
		parts := strings.SplitN(ruleID, "/", 2)
		if len(parts) > 0 {
			return parts[0]
		}
	}

	return "security"
}

// CalculateRiskScore computes a 0-100 risk score from findings.
// Scoring is based on user-facing threat levels, not raw CVSS.
//
// This uses logarithmic diminishing returns so duplicate findings from multiple
// scanners don't inflate the score, while still reflecting cumulative risk.
// Findings are deduplicated by (rule_id + location) before scoring.
//
// Formula per category: category_score = weight * log2(1 + unique_count)
//   - Dangerous: weight 25 (1 finding=25, 2=40, 4=58, 8=72, cap 80)
//   - Warning:   weight 6  (1 finding=6,  2=10, 4=15, 8=18, cap 25)
//   - Info:      weight 2  (1 finding=2,  2=3,  4=5,  8=6,  cap 10)
//
// Note: This score is an experimental heuristic. There is no industry standard
// for aggregating multi-scanner MCP security findings into a single number.
func CalculateRiskScore(findings []ScanFinding) int {
	if len(findings) == 0 {
		return 0
	}

	// Deduplicate: group by (rule_id + location) to avoid triple-counting
	// when multiple scanners report the same issue.
	type dedupKey struct{ ruleID, location string }
	seen := make(map[dedupKey]bool)
	var dangerousCount, warningCount, infoCount int

	for _, f := range findings {
		key := dedupKey{f.RuleID, f.Location}
		if key.ruleID != "" && seen[key] {
			continue // Skip duplicate finding
		}
		if key.ruleID != "" {
			seen[key] = true
		}

		switch f.ThreatLevel {
		case ThreatLevelDangerous:
			dangerousCount++
		case ThreatLevelWarning:
			warningCount++
		case ThreatLevelInfo:
			infoCount++
		default:
			// Unclassified: use severity as fallback
			switch f.Severity {
			case SeverityCritical:
				dangerousCount++
			case SeverityHigh:
				warningCount++
			case SeverityMedium:
				warningCount++
			case SeverityLow:
				infoCount++
			}
		}
	}

	// Logarithmic diminishing returns: score = weight * log2(1 + count)
	logScore := func(count int, weight float64, cap int) int {
		if count == 0 {
			return 0
		}
		s := int(weight * math.Log2(1+float64(count)))
		if s > cap {
			return cap
		}
		return s
	}

	dangerousScore := logScore(dangerousCount, 25, 80)
	warningScore := logScore(warningCount, 6, 25)
	infoScore := logScore(infoCount, 2, 10)

	score := dangerousScore + warningScore + infoScore
	if score > 100 {
		score = 100
	}
	return score
}

// SummarizeFindings produces a ReportSummary from findings
func SummarizeFindings(findings []ScanFinding) ReportSummary {
	summary := ReportSummary{Total: len(findings)}
	for _, f := range findings {
		// Count by CVSS severity
		switch f.Severity {
		case SeverityCritical:
			summary.Critical++
		case SeverityHigh:
			summary.High++
		case SeverityMedium:
			summary.Medium++
		case SeverityLow:
			summary.Low++
		case SeverityInfo:
			summary.Info++
		}
		// Count by user-facing threat level
		switch f.ThreatLevel {
		case ThreatLevelDangerous:
			summary.Dangerous++
		case ThreatLevelWarning:
			summary.Warnings++
		case ThreatLevelInfo:
			summary.InfoLevel++
		}
	}
	return summary
}

// parsePackageFromMessage extracts package info from Trivy-style SARIF message text.
// Trivy messages follow the pattern:
//
//	Package: @modelcontextprotocol/sdk\nInstalled Version: 0.6.0\nVulnerability CVE-2025-66414\nSeverity: HIGH\nFixed Version: 1.12.1
func parsePackageFromMessage(msg string) (pkg, installed, fixed string) {
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Package: ") {
			pkg = strings.TrimPrefix(line, "Package: ")
		} else if strings.HasPrefix(line, "Installed Version: ") {
			installed = strings.TrimPrefix(line, "Installed Version: ")
		} else if strings.HasPrefix(line, "Fixed Version: ") {
			fixed = strings.TrimPrefix(line, "Fixed Version: ")
		}
	}
	return
}

// ClassifyThreat assigns user-facing threat_type and threat_level to a finding
// based on rule ID, category, description, and severity.
func ClassifyThreat(f *ScanFinding) {
	ruleLC := strings.ToLower(f.RuleID)
	catLC := strings.ToLower(f.Category)
	titleLC := strings.ToLower(f.Title)
	descLC := strings.ToLower(f.Description)
	combined := ruleLC + " " + catLC + " " + titleLC + " " + descLC

	// Tool Poisoning: hidden instructions in tool descriptions
	if containsAny(combined, "tool-poisoning", "tool_poisoning", "tpa", "hidden instruction",
		"tool description attack", "poisoned tool", "tool shadowing") {
		f.ThreatType = ThreatToolPoisoning
		f.ThreatLevel = ThreatLevelDangerous
		return
	}

	// Prompt Injection: malicious payloads in responses/inputs
	if containsAny(combined, "prompt-injection", "prompt_injection", "injection vector",
		"prompt injection", "indirect injection", "jailbreak") {
		f.ThreatType = ThreatPromptInjection
		f.ThreatLevel = ThreatLevelDangerous
		return
	}

	// Malicious code: malware, backdoors, suspicious patterns
	if containsAny(combined, "malware", "backdoor", "malicious", "trojan", "reverse shell",
		"crypto miner", "exfiltrat") {
		f.ThreatType = ThreatMaliciousCode
		f.ThreatLevel = ThreatLevelDangerous
		return
	}

	// Rug Pull: tool definition changes
	if containsAny(combined, "rug-pull", "rug_pull", "definition change", "tool changed") {
		f.ThreatType = ThreatRugPull
		f.ThreatLevel = ThreatLevelWarning
		return
	}

	// Supply Chain: CVEs, package vulnerabilities
	if strings.HasPrefix(ruleLC, "cve-") || f.PackageName != "" ||
		containsAny(combined, "vulnerability", "cve", "supply chain", "dependency") {
		f.ThreatType = ThreatSupplyChain
		// High CVEs are warning, lower are info
		if f.Severity == SeverityCritical || f.Severity == SeverityHigh {
			f.ThreatLevel = ThreatLevelWarning
		} else {
			f.ThreatLevel = ThreatLevelInfo
		}
		return
	}

	// Code quality / security best practices
	if containsAny(combined, "eval", "subprocess", "shell=true", "command injection",
		"path traversal", "sql injection", "xss", "insecure") {
		f.ThreatType = ThreatMaliciousCode
		if f.Severity == SeverityCritical || f.Severity == SeverityHigh {
			f.ThreatLevel = ThreatLevelWarning
		} else {
			f.ThreatLevel = ThreatLevelInfo
		}
		return
	}

	// Default: uncategorized
	f.ThreatType = ThreatUncategorized
	if f.Severity == SeverityCritical || f.Severity == SeverityHigh {
		f.ThreatLevel = ThreatLevelWarning
	} else {
		f.ThreatLevel = ThreatLevelInfo
	}
}

func containsAny(s string, patterns ...string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// ClassifyAllFindings applies threat classification to all findings
func ClassifyAllFindings(findings []ScanFinding) {
	for i := range findings {
		ClassifyThreat(&findings[i])
	}
}

// isSupplyChainAudit reports whether a finding is a real CVE/package vulnerability
// that should render in the "Supply Chain Audit (CVEs)" UI section. The criteria are
// intentionally narrow — broad keyword matching (e.g. description contains "vulnerability")
// would miscategorize AI-scanner output as CVEs.
func isSupplyChainAudit(f *ScanFinding) bool {
	if f.PackageName != "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(f.RuleID), "cve-")
}
