package scanner

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// consensusConfidenceStep is how much each additional independent source raises
// a finding's confidence when scanners agree on the same (location,
// threat_type). Additive and capped at 1.0 (Spec 077 FR-012).
const consensusConfidenceStep = 0.15

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
// Identical findings reported by several scanners (same rule_id + location) are
// deduplicated before scoring.
//
// Spec 076 (FR-006, SC-007) — consensus is additive: the deterministic scanner
// emits ONE finding per tool whose Signals list every independent check that
// fired. A finding contributes its consensus weight (the count of distinct
// contributing signals, min 1) to its category, so a tool flagged by several
// checks raises the score instead of being collapsed to one. Findings from
// scanners that emit no per-signal data weigh 1, so legacy scoring is unchanged.
//
// Formula per category: category_score = weight * log2(1 + weighted_count)
//   - Dangerous: weight 25 (1=25, 2=40, 4=58, 8=72, cap 80)
//   - Warning:   weight 6  (1=6,  2=10, 4=15, 8=18, cap 25)
//   - Info:      weight 2  (1=2,  2=3,  4=5,  8=6,  cap 10)
//
// Note: This score is an experimental heuristic. There is no industry standard
// for aggregating multi-scanner MCP security findings into a single number.
func CalculateRiskScore(findings []ScanFinding) int {
	if len(findings) == 0 {
		return 0
	}

	// Precompute cross-source consensus: for each (location, threat_type) with a
	// non-empty threat_type, the set of distinct sources that independently
	// flagged it. Two different scanners agreeing on the same issue — even via
	// different rule ids — is consensus and raises the weight (Spec 077 FR-012,
	// T020). External/Docker findings that used to flatten to weight 1 now ADD.
	// An empty threat_type cannot form consensus and keeps the legacy per-rule
	// dedup (so existing single-scanner scoring is unchanged).
	type consensusKey struct{ location, threatType string }
	groupSources := make(map[consensusKey]map[string]struct{})
	for i := range findings {
		f := &findings[i]
		if f.ThreatType == "" {
			continue
		}
		ck := consensusKey{f.Location, f.ThreatType}
		set := groupSources[ck]
		if set == nil {
			set = make(map[string]struct{})
			groupSources[ck] = set
		}
		for _, s := range findingSources(*f) {
			set[s] = struct{}{}
		}
	}

	// Deduplicate for scoring:
	//   - legacy: group by (rule_id, location) to avoid triple-counting the same
	//     rule reported by multiple scanners (unchanged behavior).
	//   - consensus: when ≥2 distinct sources agree on the same (location,
	//     threat_type), count that issue ONCE weighted by the number of agreeing
	//     sources, so agreement raises the score without double-counting.
	type dedupKey struct{ ruleID, location string }
	seen := make(map[dedupKey]bool)
	seenConsensus := make(map[consensusKey]bool)
	var dangerousCount, warningCount, infoCount int

	addWeight := func(f *ScanFinding, weight int) {
		switch f.ThreatLevel {
		case ThreatLevelDangerous:
			dangerousCount += weight
		case ThreatLevelWarning:
			warningCount += weight
		case ThreatLevelInfo:
			infoCount += weight
		default:
			// Unclassified: use severity as fallback
			switch f.Severity {
			case SeverityCritical:
				dangerousCount += weight
			case SeverityHigh:
				warningCount += weight
			case SeverityMedium:
				warningCount += weight
			case SeverityLow:
				infoCount += weight
			}
		}
	}

	for i := range findings {
		f := &findings[i]

		// Cross-source consensus path: only when ≥2 distinct sources agree on a
		// classified (location, threat_type). Counted once, weighted by agreement.
		if f.ThreatType != "" {
			ck := consensusKey{f.Location, f.ThreatType}
			if n := len(groupSources[ck]); n >= 2 {
				if seenConsensus[ck] {
					continue
				}
				seenConsensus[ck] = true
				weight := consensusWeight(*f)
				if n > weight {
					weight = n
				}
				addWeight(f, weight)
				continue
			}
		}

		// Legacy per-rule dedup (single source, or unclassified findings).
		key := dedupKey{f.RuleID, f.Location}
		if key.ruleID != "" && seen[key] {
			continue // Skip duplicate finding
		}
		if key.ruleID != "" {
			seen[key] = true
		}

		// Consensus weight: independent signals on one tool ADD to the score
		// (Spec 076 FR-006). A single-signal or signal-less finding weighs 1.
		addWeight(f, consensusWeight(*f))
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

// consensusWeight returns how much a single (deduplicated) finding contributes
// to its risk category. The deterministic scanner (Spec 076) aggregates every
// independent check that fired on a tool into one finding's Signals list, so a
// finding flagged by N distinct checks weighs N — agreement raises the score
// rather than being collapsed (FR-006). Findings with zero or one signal — and
// every legacy scanner finding, which carries none — weigh 1.
func consensusWeight(f ScanFinding) int {
	if n := len(f.Signals); n > 1 {
		return n
	}
	return 1
}

// findingSources returns the contributing scanner ids for a finding, preferring
// the explicit Sources list (Spec 077) and falling back to the single Scanner
// id for legacy findings that predate multi-source attribution.
func findingSources(f ScanFinding) []string {
	if len(f.Sources) > 0 {
		return f.Sources
	}
	if f.Scanner != "" {
		return []string{f.Scanner}
	}
	return nil
}

// sortedUnion returns the deduplicated, sorted union of the given source id
// slices, dropping empty strings. Returns nil when the union is empty so the
// JSON `sources` field stays omitted for legacy findings.
func sortedUnion(lists ...[]string) []string {
	set := make(map[string]struct{})
	for _, list := range lists {
		for _, s := range list {
			if s != "" {
				set[s] = struct{}{}
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// MergeFindings collapses findings from every scanner into a single unified list
// (Spec 077 FR-010/FR-011). Findings sharing a (rule_id, location) merge into one
// entry whose Sources lists every contributing scanner. When ≥2 distinct sources
// independently agree on the same (location, threat_type) — even via different
// rule ids — each agreeing finding's Confidence is raised to reflect that
// consensus (FR-012). Findings with an empty rule_id are never merged (each is
// treated as a distinct issue) but still carry populated Sources.
//
// Order is preserved (first occurrence wins) so the report is stable.
func MergeFindings(findings []ScanFinding) []ScanFinding {
	result := make([]ScanFinding, 0, len(findings))

	// Phase 1 — dedup by (rule_id, location); union contributing sources.
	type key struct{ ruleID, location string }
	index := make(map[key]int)
	for i := range findings {
		f := findings[i]
		srcs := findingSources(f)
		if f.RuleID == "" {
			f.Sources = sortedUnion(f.Sources, srcs)
			result = append(result, f)
			continue
		}
		k := key{f.RuleID, f.Location}
		if pos, ok := index[k]; ok {
			result[pos].Sources = sortedUnion(result[pos].Sources, srcs)
			continue
		}
		f.Sources = sortedUnion(f.Sources, srcs)
		index[k] = len(result)
		result = append(result, f)
	}

	// Phase 2 — consensus confidence boost by (location, threat_type).
	type ckey struct{ location, threatType string }
	group := make(map[ckey]map[string]struct{})
	for i := range result {
		f := &result[i]
		if f.ThreatType == "" {
			continue
		}
		ck := ckey{f.Location, f.ThreatType}
		set := group[ck]
		if set == nil {
			set = make(map[string]struct{})
			group[ck] = set
		}
		for _, s := range f.Sources {
			set[s] = struct{}{}
		}
	}
	for i := range result {
		f := &result[i]
		if f.ThreatType == "" {
			continue
		}
		n := len(group[ckey{f.Location, f.ThreatType}])
		if n < 2 {
			continue
		}
		boosted := f.Confidence + consensusConfidenceStep*float64(n-1)
		if boosted > 1.0 {
			boosted = 1.0
		}
		if boosted > f.Confidence {
			f.Confidence = boosted
		}
	}

	return result
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
	// Spec 077 (T022): guarantee every finding leaves classification with a
	// user-readable severity. External/legacy SARIF findings sometimes arrive
	// with no severity; backfill it from the classified threat level so the
	// unified report never shows a blank severity. An explicit severity is
	// preserved.
	defer backfillSeverity(f)

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

// backfillSeverity derives a severity from the finding's classified threat
// level when none was supplied, so every finding in the unified report carries a
// clear severity (Spec 077 FR-013 / T022). Never overwrites an explicit value.
func backfillSeverity(f *ScanFinding) {
	if f.Severity != "" {
		return
	}
	switch f.ThreatLevel {
	case ThreatLevelDangerous:
		f.Severity = SeverityHigh
	case ThreatLevelWarning:
		f.Severity = SeverityMedium
	case ThreatLevelInfo:
		f.Severity = SeverityInfo
	default:
		f.Severity = SeverityMedium
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
