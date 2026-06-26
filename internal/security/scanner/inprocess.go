package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect/checks"
)

// inProcessTPAScannerID is the bundled, Docker-less scanner that analyzes a
// connected server's tool descriptions/schemas for Tool-Poisoning-Attack (TPA)
// indicators and embedded secrets. It runs for ANY connected server — including
// remote http/sse servers that have no source files or Docker container — so
// "Scan Now" yields a real description-based result instead of the
// "No Source Available / all scanners failed" dead-end (MCP-2082).
const inProcessTPAScannerID = "tpa-descriptions"

// tpaRule is a heuristic over tool description/schema text. A rule fires when
// the (lower-cased) text contains any of its phrases. Phrases are matched as
// plain substrings — the same approach ClassifyThreat already uses — which
// keeps the rule set readable and dependency-free.
type tpaRule struct {
	ruleID      string
	title       string
	severity    string
	threatType  string
	threatLevel string
	phrases     []string
}

// tpaRules are ordered most- to least-specific. The first rule that matches a
// given tool wins for that rule's category; a single tool can still match
// multiple distinct rules (e.g. hidden-instructions AND exfiltration).
var tpaRules = []tpaRule{
	{
		ruleID:      "tpa_hidden_instructions",
		title:       "Hidden instructions in tool description",
		severity:    SeverityCritical,
		threatType:  ThreatToolPoisoning,
		threatLevel: ThreatLevelDangerous,
		phrases: []string{
			"ignore previous instruction", "ignore all previous", "ignore the above",
			"disregard previous", "disregard all previous", "disregard the above",
			"do not tell the user", "don't tell the user", "do not inform the user",
			"without telling the user", "without informing the user",
			"do not mention this", "do not reveal", "do not disclose",
			"hide this from", "keep this hidden", "keep this secret",
			"<important>", "<secret>", "<system>", "<system_prompt>", "<hidden>",
		},
	},
	{
		ruleID:      "prompt_injection_in_description",
		title:       "Prompt-injection phrasing in tool description",
		severity:    SeverityHigh,
		threatType:  ThreatPromptInjection,
		threatLevel: ThreatLevelDangerous,
		phrases: []string{
			"new instructions:", "system prompt", "you must always",
			"always call this tool first", "before using any other tool",
			"before calling any other", "before you use any other",
			"jailbreak", "developer mode", "ignore your guidelines",
		},
	},
	{
		ruleID:      "data_exfiltration_in_description",
		title:       "Data-exfiltration hints in tool description",
		severity:    SeverityHigh,
		threatType:  ThreatMaliciousCode,
		threatLevel: ThreatLevelDangerous,
		phrases: []string{
			"exfiltrat", "id_rsa", "~/.ssh", "/.ssh/", "~/.aws", "/.aws/",
			"/etc/passwd", ".env file", "read the .env",
			"send the credentials", "send credentials", "leak the",
			"upload the file to", "post the contents to",
		},
	},
}

// toolDef is the subset of an MCP tool definition the in-process scanner needs.
// Tools are exported by service.exportToolDefinitions as MCP tools/list output:
// {"tools": [ {"name": ..., "description": ..., "inputSchema": {...}} ]}.
type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// inProcessToolScan parses an exported tools.json document and returns findings
// from the TPA heuristics plus any secrets embedded in tool descriptions. It is
// a pure function (no Docker, no network) so it works for remote servers.
func inProcessToolScan(toolsJSON []byte, serverName, scannerID string) []ScanFinding {
	var doc struct {
		Tools []toolDef `json:"tools"`
	}
	if err := json.Unmarshal(toolsJSON, &doc); err != nil || len(doc.Tools) == 0 {
		return nil
	}

	// Default detector (built-in patterns) for embedded-secret detection in
	// descriptions. nil config → DefaultSensitiveDataDetectionConfig, which
	// already validates matches and ignores documented example keys.
	detector := security.NewDetector(nil)

	var findings []ScanFinding
	for _, tool := range doc.Tools {
		location := "tool:" + tool.Name
		// Scan the description plus the serialized input schema — TPA payloads
		// hide in either.
		text := tool.Description
		if len(tool.InputSchema) > 0 {
			text += " " + string(tool.InputSchema)
		}
		lower := strings.ToLower(text)

		for _, rule := range tpaRules {
			if phrase, ok := matchAnyPhrase(lower, rule.phrases); ok {
				findings = append(findings, ScanFinding{
					RuleID:      rule.ruleID,
					Severity:    rule.severity,
					ThreatType:  rule.threatType,
					ThreatLevel: rule.threatLevel,
					Title:       rule.title + " (" + tool.Name + ")",
					Description: fmt.Sprintf("Tool %q description contains a %s indicator: %q.", tool.Name, rule.threatType, phrase),
					Location:    location,
					Scanner:     scannerID,
					Evidence:    truncate(strings.TrimSpace(tool.Description), 500),
				})
			}
		}

		// Embedded secrets in the description (e.g. a hardcoded API key).
		if result := detector.Scan(text, ""); result != nil && result.Detected {
			for _, det := range result.Detections {
				if det.IsLikelyExample {
					continue
				}
				findings = append(findings, ScanFinding{
					RuleID:      "embedded_secret",
					Severity:    SeverityHigh,
					ThreatType:  ThreatToolPoisoning,
					ThreatLevel: ThreatLevelWarning,
					Title:       fmt.Sprintf("Embedded %s in tool description (%s)", det.Category, tool.Name),
					Description: fmt.Sprintf("Tool %q description contains a likely %s (%s).", tool.Name, det.Category, det.Type),
					Location:    location,
					Scanner:     scannerID,
				})
			}
		}
	}

	// Spec-076 offline detect.Engine: the structural soft checks (prompt-injection
	// directives, capability mismatch, embedded secrets) run alongside the legacy
	// phrase/secret heuristics above. Findings convert 1:1 to ScanFinding.
	findings = append(findings, detectEngineFindings(doc.Tools, serverName, scannerID)...)

	return findings
}

// detectEngineFindings runs the Spec-076 offline detect.Engine over the server's
// tool set and converts each detect.Finding 1:1 into a ScanFinding. The engine
// builds a RegistryView from the whole tool set so cross-tool checks can reason
// about what this scan can see.
//
// NOTE (Spec-076 US1/US2 coordination): this wiring is the single shared
// integration point between US1's hard checks (#770) and US2's soft checks. This
// branch registers the three US2 SOFT checks; when the two land, the Checks slice
// below is the merge point — combine into the full six-check set.
func detectEngineFindings(tools []toolDef, serverName, scannerID string) []ScanFinding {
	views := make([]detect.ToolView, 0, len(tools))
	for _, t := range tools {
		views = append(views, detect.ToolView{
			Server:      serverName,
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	engine := detect.NewEngine(detect.Options{
		ScannerID: scannerID,
		Checks: []detect.Check{
			&checks.DirectiveImperative{},
			&checks.CapabilityMismatch{},
			&checks.EmbeddedSecret{},
		},
	})
	result := engine.Scan(detect.NewRegistryView(views))

	out := make([]ScanFinding, 0, len(result.Findings))
	for _, f := range result.Findings {
		out = append(out, detectFindingToScanFinding(f))
	}
	return out
}

// detectFindingToScanFinding maps a self-contained detect.Finding onto the
// scanner's ScanFinding. detect deliberately mirrors the scanner's severity /
// threat-level / threat-type vocabulary strings, so the copy is verbatim. The
// additive Confidence/Signals fields are carried through.
func detectFindingToScanFinding(f detect.Finding) ScanFinding {
	return ScanFinding{
		RuleID:      f.RuleID,
		Severity:    f.Severity,
		Category:    f.Category,
		ThreatType:  f.ThreatType,
		ThreatLevel: f.ThreatLevel,
		Title:       f.Title,
		Description: f.Description,
		Location:    f.Location,
		Scanner:     f.Scanner,
		Evidence:    f.Evidence,
		Confidence:  f.Confidence,
		Signals:     f.Signals,
	}
}

// runInProcessScanner executes a Docker-less, built-in scanner in Go. It reads
// the tool definitions exported to req.SourceDir/tools.json and runs the
// description heuristics. This is what lets a connected remote server (no
// source, no Docker) still produce a real description-based scan instead of the
// "No Source Available / all scanners failed" dead-end (MCP-2082).
func (e *Engine) runInProcessScanner(s *ScannerPlugin, req ScanRequest) (*ScanReport, scannerLogs, error) {
	logs := scannerLogs{}
	report := &ScanReport{
		ID:        fmt.Sprintf("report-%s-%d", s.ID, time.Now().UnixNano()),
		ScannerID: s.ID,
		ScannedAt: time.Now(),
		Findings:  []ScanFinding{},
	}

	if s.ID != inProcessTPAScannerID {
		return nil, logs, fmt.Errorf("unknown in-process scanner: %s", s.ID)
	}

	// The tool-description analyzer is a Pass-1 (security scan) concern. During
	// Pass 2 (supply chain audit) there is nothing new for it to do, so it
	// records a clean, completed result rather than re-emitting the same TPA
	// findings into the supply-chain job.
	if req.ScanPass == ScanPassSupplyChainAudit {
		logs.Stdout = "in-process tool-description scan skipped for supply chain audit (Pass 2)"
		return report, logs, nil
	}

	if req.SourceDir == "" {
		return nil, logs, fmt.Errorf("in-process scanner %s: no source dir with exported tool definitions", s.ID)
	}

	toolsPath := filepath.Join(req.SourceDir, "tools.json")
	data, err := os.ReadFile(toolsPath)
	if err != nil {
		return nil, logs, fmt.Errorf("in-process scanner %s: could not read exported tool definitions (%s): %w", s.ID, toolsPath, err)
	}

	findings := inProcessToolScan(data, req.ServerName, s.ID)
	// Findings already carry threat_type/threat_level; this is a no-op safety
	// net consistent with how Docker scanner output is normalized.
	ClassifyAllFindings(findings)
	report.Findings = findings
	report.RiskScore = CalculateRiskScore(findings)

	logs.Stdout = fmt.Sprintf("in-process tool-description scan: %d finding(s)", len(findings))
	return report, logs, nil
}

// matchAnyPhrase returns the first phrase contained in lowered text.
func matchAnyPhrase(loweredText string, phrases []string) (string, bool) {
	for _, p := range phrases {
		if strings.Contains(loweredText, p) {
			return p, true
		}
	}
	return "", false
}
