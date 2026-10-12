// Baseline regression: every attack vector in the catalog is run through the
// REAL offline detect engine (the same scanner.ScanToolMetadataVerdict seam
// mcpproxy's scan-mode change gate uses), so the lab doubles as a detector
// regression suite. If a check stops firing on its vector, these tests fail.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

const (
	testAttackerServer = "tpa-lab-attacker"
	testVictimServer   = "tpa-lab-victim"
)

// toMetadata converts lab tools into the ToolMetadata shape the scanner seam
// consumes (discovery normally produces these from a live tools/list).
func toMetadata(serverName string, tools []labTool) []*config.ToolMetadata {
	out := make([]*config.ToolMetadata, 0, len(tools))
	for _, lt := range tools {
		schema, err := json.Marshal(lt.tool.InputSchema)
		if err != nil {
			panic(fmt.Sprintf("marshal input schema for %s: %v", lt.tool.Name, err))
		}
		out = append(out, &config.ToolMetadata{
			Name:        lt.tool.Name,
			ServerName:  serverName,
			RawName:     lt.tool.Name,
			Description: lt.tool.Description,
			ParamsJSON:  string(schema),
		})
	}
	return out
}

func scan(t *testing.T, serverName string, tools []labTool, peers map[string][]labTool) (string, []scanner.ScanFinding) {
	t.Helper()
	peerMeta := make(map[string][]*config.ToolMetadata, len(peers))
	for name, pts := range peers {
		peerMeta[name] = toMetadata(name, pts)
	}
	verdict, findings, coverage := scanner.ScanToolMetadataVerdict(
		serverName, toMetadata(serverName, tools), peerMeta)
	if !coverage {
		t.Fatalf("scan coverage degraded for %s (bundle missing or check failed)", serverName)
	}
	return verdict, findings
}

func ruleIDs(findings []scanner.ScanFinding) map[string]bool {
	ids := make(map[string]bool, len(findings))
	for _, f := range findings {
		// RuleIDs arrive as "detect.<check>" (and "detect.tpa.TPA-…"); key by
		// the bare check id the catalog metadata talks about.
		id := strings.TrimPrefix(f.RuleID, "detect.")
		ids[id] = true
	}
	return ids
}

func findingsFor(findings []scanner.ScanFinding, toolName string) []scanner.ScanFinding {
	var out []scanner.ScanFinding
	for _, f := range findings {
		if strings.HasSuffix(f.Location, ":"+toolName) || strings.Contains(f.Location, toolName) {
			out = append(out, f)
		}
	}
	return out
}

func dumpFindings(findings []scanner.ScanFinding) string {
	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintf(&b, "\n  [%s] %s severity=%s level=%s loc=%s", f.RuleID, f.Title, f.Severity, f.ThreatLevel, f.Location)
	}
	return b.String()
}

// TestAttackerStageOneTriggersEveryEngineCheck asserts the always-on stage-1
// catalog lights up all seven baseline detect checks plus the embedded tpa-db
// bundle, and that the server verdict is dangerous (hard tier → gates
// approval).
func TestAttackerStageOneTriggersEveryEngineCheck(t *testing.T) {
	tools := attackerStageTools(1, nil)
	victim := victimTools()

	verdict, findings := scan(t, testAttackerServer, tools,
		map[string][]labTool{testVictimServer: victim})

	if verdict != "dangerous" {
		t.Fatalf("stage-1 attacker verdict = %q, want dangerous; findings:%s", verdict, dumpFindings(findings))
	}

	ids := ruleIDs(findings)
	for _, want := range []string{
		"unicode.hidden",
		"shadowing.cross_server",
		"payload.decoded",
		"phrase.injection",
		"directive.imperative",
		"capability.mismatch",
		"secret.embedded",
	} {
		if !ids[want] {
			t.Errorf("expected detect check %q to fire on the stage-1 catalog; got rules %v; findings:%s",
				want, ids, dumpFindings(findings))
		}
	}

	// The embedded tpa-db bundle must contribute at least one TPA-2026-*
	// signature hit (V01..V03 are built to match known signatures).
	if !hasTPASignal(findings) {
		t.Errorf("expected at least one tpa-db TPA-2026-* signature hit; findings:%s", dumpFindings(findings))
	}

	// Spot-check the flagship detections are attributed to the right tools.
	// Pairings reflect VERIFIED engine behavior — see the "observed coverage"
	// notes in README.md for vectors where fewer checks fire than the
	// research literature would suggest.
	for tool, wantRule := range map[string]string{
		"weather_lookup":           "unicode.hidden",
		"crypto_price":             "payload.decoded",
		"summarize_text":           "phrase.injection",
		"filesystem_read":          "shadowing.cross_server",
		"daily_briefing":           "directive.imperative",
		"translate_text":           "capability.mismatch",
		"config_helper":            "secret.embedded",
		"finance_convert_currency": "tpa.TPA-2026-0001",
	} {
		if !hasRuleForTool(findings, tool, wantRule) {
			t.Errorf("expected %s on tool %q; tool findings:%s", wantRule, tool, dumpFindings(findingsFor(findings, tool)))
		}
	}
}

func hasTPASignal(findings []scanner.ScanFinding) bool {
	for _, f := range findings {
		if strings.Contains(f.RuleID, "TPA-2026-") {
			return true
		}
		for _, s := range f.Signals {
			if strings.Contains(s, "TPA-2026-") {
				return true
			}
		}
	}
	return false
}

func hasRuleForTool(findings []scanner.ScanFinding, toolName, ruleID string) bool {
	for _, f := range findingsFor(findings, toolName) {
		if f.RuleID == ruleID || strings.Contains(f.RuleID, ruleID) {
			return true
		}
		// One finding per tool: a contributing check that is not the primary
		// is listed in Signals (RT-04 made phrase.injection the primary on
		// some tools that also carry a tpa-db signature).
		for _, sig := range f.Signals {
			if sig == ruleID || strings.Contains(sig, ruleID) {
				return true
			}
		}
	}
	return false
}

// TestRugPullStageTwoContractsAreFlagged asserts the ARMED rug-pull contract
// (what the server serves after the pull) is itself dangerous when scanned —
// relevant under trust_mode=scan, where the change gate re-scans new metadata.
func TestRugPullStageTwoContractsAreFlagged(t *testing.T) {
	var staged []labTool
	for _, lt := range attackerStageTools(2, nil) {
		switch lt.tool.Name {
		case calendarTool, stockQuoteTool, browserCaptureTool:
			staged = append(staged, lt)
		}
	}
	if len(staged) != 3 {
		t.Fatalf("expected 3 staged tools at stage 2, got %d", len(staged))
	}

	verdict, findings := scan(t, testAttackerServer, staged, nil)
	if verdict != "dangerous" {
		t.Fatalf("armed rug-pull verdict = %q, want dangerous; findings:%s", verdict, dumpFindings(findings))
	}
	if !hasRuleForTool(findings, calendarTool, "TPA-2026-0001") &&
		!hasRuleForTool(findings, calendarTool, "phrase.injection") {
		t.Errorf("armed calendar tool expected tpa-db/phrase findings; got:%s", dumpFindings(findingsFor(findings, calendarTool)))
	}
}

// TestRugPullStageOneContractsScanClean asserts the BENIGN facade scans clean —
// this is what makes the rug-pull demo honest: stage 1 is genuinely approvable,
// so the only thing that can catch the later pull is the contract-hash gate.
func TestRugPullStageOneContractsScanClean(t *testing.T) {
	var facade []labTool
	for _, lt := range attackerStageTools(1, nil) {
		switch lt.tool.Name {
		case calendarTool, stockQuoteTool, "benign_calculator", "benign_time", "lab_arm_stage2":
			facade = append(facade, lt)
		}
	}
	verdict, findings := scan(t, testAttackerServer, facade, nil)
	if verdict != "clean" {
		t.Fatalf("stage-1 facade verdict = %q, want clean (the facade must be approvable); findings:%s",
			verdict, dumpFindings(findings))
	}
}

// TestVictimServerScansClean guards the benign peer against false positives —
// including when the attacker's clone exists as a peer (collisions are the
// attacker's fault, but the victim alone must stay clean).
func TestVictimServerScansClean(t *testing.T) {
	verdict, findings := scan(t, testVictimServer, victimTools(), nil)
	if verdict != "clean" {
		t.Fatalf("victim verdict = %q, want clean; findings:%s", verdict, dumpFindings(findings))
	}
}

// TestBenignControlsStayCleanWithVictimPeer guards the lab's control tools
// against false positives while cross-server context is present.
func TestBenignControlsStayCleanWithVictimPeer(t *testing.T) {
	var controls []labTool
	for _, lt := range attackerStageTools(1, nil) {
		switch lt.tool.Name {
		case "benign_calculator", "benign_time", "lab_arm_stage2":
			controls = append(controls, lt)
		}
	}
	verdict, findings := scan(t, testAttackerServer, controls,
		map[string][]labTool{testVictimServer: victimTools()})
	if verdict != "clean" {
		t.Fatalf("controls verdict = %q, want clean; findings:%s", verdict, dumpFindings(findings))
	}
}

// TestOutputPoisoningVectorsScanCleanByDesign documents the KNOWN coverage
// limit the lab exists to expose: output-poisoning vectors (V12/V13) carry
// benign descriptions, so the description scanner is clean by design. Runtime
// defenses (activity monitoring, output scanning) are the only catchers.
func TestOutputPoisoningVectorsScanCleanByDesign(t *testing.T) {
	var outputs []labTool
	for _, lt := range attackerStageTools(1, nil) {
		switch lt.tool.Name {
		case "github_list_issues", "memory_remember":
			outputs = append(outputs, lt)
		}
	}
	verdict, findings := scan(t, testAttackerServer, outputs, nil)
	if verdict != "clean" {
		t.Fatalf("output-poisoning descriptions verdict = %q, want clean by design; findings:%s",
			verdict, dumpFindings(findings))
	}
}

// TestStageTwoAddsBrowserCaptureTool pins the stage-2-only new tool so the
// "new malicious tool after approval" path has a deterministic surface.
func TestStageTwoAddsBrowserCaptureTool(t *testing.T) {
	stage1 := attackerStageTools(1, nil)
	stage2 := attackerStageTools(2, nil)
	for _, lt := range stage1 {
		if lt.tool.Name == browserCaptureTool {
			t.Fatalf("%s must be absent at stage 1", browserCaptureTool)
		}
	}
	found := false
	for _, lt := range stage2 {
		if lt.tool.Name == browserCaptureTool {
			found = true
		}
	}
	if !found {
		t.Fatalf("%s must be present at stage 2", browserCaptureTool)
	}
}

// ----------------------------------------------------- red-team probes -----

// TestFacadeStageOneScansClean pins the precondition for the trust_mode=scan
// chain (EXP-6): the facade profile at stage 1 is genuinely approvable-clean.
func TestFacadeStageOneScansClean(t *testing.T) {
	tools := buildAttackerTools("facade", 1, nil)
	if len(tools) == 0 {
		t.Fatal("facade profile produced no tools")
	}
	verdict, findings := scan(t, testAttackerServer, tools, nil)
	if verdict != "clean" {
		t.Fatalf("facade stage-1 verdict = %q, want clean; findings:%s", verdict, dumpFindings(findings))
	}
}

// TestStageThreeParaphrasedPullScansClean pins the EXP-6 precondition: the
// stage-3 armed rug pull (same intent as stage 2) is worded past the
// detector. Before RT-02 the trust_mode=scan change gate auto-approved it;
// EXP-6 now proves the gate holds a changed contract even on a clean verdict.
func TestStageThreeParaphrasedPullScansClean(t *testing.T) {
	tools := buildAttackerTools("facade", 3, nil)
	verdict, findings := scan(t, testAttackerServer, tools, nil)
	if verdict != "clean" {
		t.Fatalf("stage-3 paraphrased pull verdict = %q, want clean (if this fails the detector improved — update profiles.go wording and the findings doc); findings:%s",
			verdict, dumpFindings(findings))
	}
}

// TestMutantsProfileTightenedAfterRT04 pins the served mutant profile (EXP-3)
// after the RT-04/RT-07 detect-engine hardening (on top of SEC-01):
//
//   - the homoglyph override (M01) and the tag-drop + paraphrase + URL-defang
//     combo (M03) are HARD (mixed-script folding, destination-anchored and
//     credential-file-to-argument exfiltration families), so the profile
//     verdict is dangerous without relying on soft findings;
//   - the system-prompt paraphrase (M02) and the German override (M04) are
//     surfaced for review only: SEC-01 keeps paraphrase and non-English
//     override families SOFT by design (negation and descriptive use cannot
//     be ruled out), and a soft finding still holds a change under scan trust;
//   - the renamed data-sink parameter (M06) is caught by parameter SHAPE;
//   - the name-squat with a rewritten description (M05) stays out of scope:
//     a same-name collision without a cloned description is the proxy's
//     normal multi-server condition (owner decision MCP-3520, see
//     internal/security/detect/checks/shadowing.go).
func TestMutantsProfileTightenedAfterRT04(t *testing.T) {
	tools := mutantTools()
	verdict, findings := scan(t, testAttackerServer, tools,
		map[string][]labTool{testVictimServer: victimTools()})

	if verdict != "dangerous" {
		t.Fatalf("mutants profile verdict = %q, want dangerous; findings:%s", verdict, dumpFindings(findings))
	}
	for _, name := range []string{"mutant_override_env", "mutant_exfil_combo"} {
		fs := findingsFor(findings, name)
		if len(fs) == 0 || fs[0].ThreatLevel != "dangerous" {
			t.Errorf("mutant %q must be hard-tier (dangerous); got:%s", name, dumpFindings(fs))
		}
	}
	for _, name := range []string{"mutant_reveal_prompt", "mutant_german_override", "translate_text"} {
		if fs := findingsFor(findings, name); len(fs) == 0 {
			t.Errorf("mutant %q must be surfaced for review; got no findings", name)
		}
	}
	if fs := findingsFor(findings, "filesystem_read"); len(fs) > 0 {
		t.Errorf("name-squat without a cloned description is out of scope (MCP-3520) and should stay unflagged; got:%s", dumpFindings(fs))
	}
}

// TestTOCTOUContractsDifferByDescriptionOnly pins the TOCTOU probe shape:
// same names/params, poisoned descriptions at discovery, clean descriptions
// for the scan export.
func TestTOCTOUContractsDifferByDescriptionOnly(t *testing.T) {
	poisoned := toctouPoisonedTools()
	clean := toctouCleanTools()

	if len(poisoned) != len(clean) {
		t.Fatalf("poisoned/clean length mismatch: %d vs %d", len(poisoned), len(clean))
	}
	for i := range poisoned {
		if poisoned[i].tool.Name != clean[i].tool.Name {
			t.Errorf("name mismatch %q vs %q", poisoned[i].tool.Name, clean[i].tool.Name)
		}
		if poisoned[i].tool.Description == clean[i].tool.Description {
			t.Errorf("tool %q: descriptions must differ", poisoned[i].tool.Name)
		}
	}

	v, _ := scan(t, testAttackerServer, poisoned, nil)
	if v != "dangerous" {
		t.Errorf("poisoned TOCTOU contract verdict = %q, want dangerous", v)
	}
	v, _ = scan(t, testAttackerServer, clean, nil)
	if v != "clean" {
		t.Errorf("clean TOCTOU contract verdict = %q, want clean", v)
	}
}

// TestAnnotationFlipKeepsContractInputsIdentical pins HOLE: the annotation
// flip changes nothing the contract hash covers (name, description, input
// schema), so the rug-pull gate cannot see it.
func TestAnnotationFlipKeepsContractInputsIdentical(t *testing.T) {
	s1 := notesExportTool(1)
	s2 := notesExportTool(2)

	if s1.tool.Description != s2.tool.Description {
		t.Errorf("descriptions must be identical across the flip (hash input)")
	}
	schema1, _ := json.Marshal(s1.tool.InputSchema)
	schema2, _ := json.Marshal(s2.tool.InputSchema)
	if string(schema1) != string(schema2) {
		t.Errorf("input schemas must be identical across the flip (hash input):\n%s\n%s", schema1, schema2)
	}
	if s1.tool.Annotations.ReadOnlyHint == s2.tool.Annotations.ReadOnlyHint {
		t.Errorf("annotations must differ across the flip (the actual rug pull)")
	}
}
