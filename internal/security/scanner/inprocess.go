package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
// {"tools": [ {"name": ..., "description": ..., "inputSchema": {...}, "outputSchema": {...}} ]}.
// Both schemas are scanned: Spec 076 FR-001 operates on
// name+description+inputSchema+outputSchema, since a TPA payload can hide in the
// output schema's field names/descriptions just as easily as the input schema.
type toolDef struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema"`
}

// inProcessToolScan parses an exported tools.json document and returns findings
// from the deterministic detect.Engine (Spec 076 structural checks: hidden
// Unicode, cross-server shadowing, decoded shell payloads) plus the legacy TPA
// phrase heuristics and embedded-secret detection. It is a pure function (no
// Docker, no network) so it works for remote servers.
//
// Spec-076 migration boundary (US1): the structural attack classes are now
// delegated to detect.Engine, which returns confidence-scored findings carrying
// per-check Signals. The directive-phrase rules (tpaRules) and embedded-secret
// detection remain here until US2 lands detect's directive.imperative and
// secret.embedded checks, at which point this function fully delegates. Running
// both side-by-side keeps the MVP from regressing any existing coverage.
// peerTools maps a peer server's name to its current tool definitions. It feeds
// the cross-server shadowing check a real multi-server RegistryView; nil/empty
// means only the scanned server's tools are in view (no cross-server detection).
func inProcessToolScan(toolsJSON []byte, serverName string, peerTools map[string][]toolDef, scannerID string) []ScanFinding {
	var doc struct {
		Tools []toolDef `json:"tools"`
	}
	if err := json.Unmarshal(toolsJSON, &doc); err != nil || len(doc.Tools) == 0 {
		return nil
	}

	// Delegate the structural checks to the offline detect.Engine first.
	findings := detectEngineFindings(doc.Tools, serverName, peerTools, scannerID)

	// Default detector (built-in patterns) for embedded-secret detection in
	// descriptions. nil config → DefaultSensitiveDataDetectionConfig, which
	// already validates matches and ignores documented example keys.
	detector := security.NewDetector(nil)

	for _, tool := range doc.Tools {
		location := "tool:" + tool.Name
		// Scan the description plus the serialized input AND output schemas — TPA
		// payloads hide in any of them (Spec 076 FR-001).
		text := tool.Description
		if len(tool.InputSchema) > 0 {
			text += " " + string(tool.InputSchema)
		}
		if len(tool.OutputSchema) > 0 {
			text += " " + string(tool.OutputSchema)
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

	return findings
}

// detectEngineFindings runs the Spec-076 offline detect.Engine over the scanned
// server's tools PLUS every peer server's tools, then converts each
// detect.Finding 1:1 into a ScanFinding. Building the RegistryView from a real
// cross-server snapshot — each ToolView tagged with its TRUE owning server — is
// what lets shadowing.cross_server fire end-to-end (it only emits when a
// collision/reference points at a *different* server). Findings are filtered to
// the scanned server so a peer's own issues aren't reported under this scan.
func detectEngineFindings(tools []toolDef, serverName string, peerTools map[string][]toolDef, scannerID string) []ScanFinding {
	views := make([]detect.ToolView, 0, len(tools))
	for _, t := range tools {
		views = append(views, toolView(serverName, t))
	}
	// Deterministic peer ordering keeps findings stable across runs.
	peerNames := make([]string, 0, len(peerTools))
	for name := range peerTools {
		if name != serverName {
			peerNames = append(peerNames, name)
		}
	}
	sort.Strings(peerNames)
	for _, name := range peerNames {
		for _, t := range peerTools[name] {
			views = append(views, toolView(name, t))
		}
	}

	engine := detect.NewEngine(detect.Options{
		ScannerID: scannerID,
		Checks: []detect.Check{
			// US1 hard checks (#770).
			&checks.UnicodeHidden{},
			&checks.Shadowing{},
			&checks.PayloadDecoded{},
			// US2 soft checks (MCP-3577).
			&checks.DirectiveImperative{},
			&checks.CapabilityMismatch{},
			&checks.EmbeddedSecret{},
		},
	})
	result := engine.Scan(detect.NewRegistryView(views))

	prefix := serverName + ":"
	out := make([]ScanFinding, 0, len(result.Findings))
	for _, f := range result.Findings {
		// Only report findings on the server being scanned; peers are context.
		if !strings.HasPrefix(f.Location, prefix) {
			continue
		}
		out = append(out, detectFindingToScanFinding(f))
	}
	return out
}

// toolView projects a parsed tool definition onto a detect.ToolView tagged with
// its owning server.
func toolView(server string, t toolDef) detect.ToolView {
	return detect.ToolView{
		Server:       server,
		Name:         t.Name,
		Description:  t.Description,
		InputSchema:  t.InputSchema,
		OutputSchema: t.OutputSchema,
	}
}

// peerToolDefs converts the cross-server snapshot carried on a ScanRequest
// (MCP tools/list maps, keyed by server) into the toolDef form the detect
// engine wiring consumes. Malformed entries for a server are skipped, never
// fatal — the scan degrades to fewer peers rather than failing.
func peerToolDefs(peers map[string][]map[string]interface{}) map[string][]toolDef {
	if len(peers) == 0 {
		return nil
	}
	out := make(map[string][]toolDef, len(peers))
	for server, tools := range peers {
		raw, err := json.Marshal(tools)
		if err != nil {
			continue
		}
		var defs []toolDef
		if err := json.Unmarshal(raw, &defs); err != nil {
			continue
		}
		out[server] = defs
	}
	return out
}

// detectFindingToScanFinding maps a self-contained detect.Finding onto the
// scanner's ScanFinding. detect deliberately mirrors the scanner's severity /
// threat-level / threat-type vocabulary strings, so the copy is verbatim — no
// translation table. The additive Confidence/Signals fields are carried through.
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

	findings := inProcessToolScan(data, req.ServerName, peerToolDefs(req.PeerTools), s.ID)
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
