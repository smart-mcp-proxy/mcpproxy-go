package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
// from the deterministic, offline detect.Engine (Spec 076/077). It is a pure
// function (no Docker, no network) so it works for remote servers with no source
// or container.
//
// Spec 077 (US1): the engine is now the SOLE in-process detector. The duplicate
// legacy TPA phrase rules and the duplicate legacy embedded-secret path have
// been removed — the blocking posture for high-confidence injection/exfiltration
// phrases is preserved by the hard-tier detect check phrase.injection, and
// embedded secrets are covered by detect's secret.embedded check. This is one
// deterministic engine instead of three overlapping ones.
//
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

	return detectEngineFindings(doc.Tools, serverName, peerTools, scannerID)
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
			// Spec 076 US1 hard checks (#770).
			&checks.UnicodeHidden{},
			&checks.Shadowing{},
			&checks.PayloadDecoded{},
			// Spec 077 US1 hard check: curated injection/exfiltration phrases.
			// Restores the approval-blocking posture of the deleted legacy
			// tpaRules without their false positives.
			&checks.PhraseInjection{},
			// Spec 076 US2 soft checks (MCP-3577).
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
	// A detect Finding is dangerous iff at least one HARD signal contributed to
	// it (see detect.aggregate), so ThreatLevel is the faithful tier witness:
	// dangerous → hard (gates approval), otherwise soft (review-only). Spec 077
	// makes the verdict tier-driven, so every baseline finding carries a tier.
	tier := TierSoft
	if f.ThreatLevel == ThreatLevelDangerous {
		tier = TierHard
	}
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
		Tier:        tier,
		Sources:     []string{f.Scanner},
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
