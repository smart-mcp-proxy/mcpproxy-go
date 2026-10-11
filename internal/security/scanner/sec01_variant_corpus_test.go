package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// SEC-01 (assessment 2026-10-11): under trust_mode=scan a definition change is
// auto-approved only when ScanToolMetadataVerdict returns a fully-covered
// "clean" verdict. Paraphrased, letter-spaced, homoglyph and non-English
// instruction overrides used to come back clean and were scan-approved. This
// test runs the SAME seam the runtime change gate uses over a stratified corpus
// and reports a per-class outcome table.
//
// Corpus contract (testdata/sec01_injection_variants.json):
//   - label=malicious, expect=held   → verdict must NOT be "clean" (held for review).
//   - label=benign,    expect=clean  → verdict must be "clean" (stays auto-approvable).
//   - label=benign,    expect=not_dangerous → quoted/described examples: they may
//     surface for review (never-fully-suppress), but must never be hard-blocked.
//   - known_gap=true marks a malicious entry the detector is documented NOT to
//     catch; it is reported in the table but not asserted, so the gap stays
//     visible instead of being silently tuned away or hidden.
//
// split=heldout entries were written before the detector change and were not
// used to tune patterns; split=redteam entries come from the assessment's
// independent tpa-redteam harness. No universal accuracy figure follows from
// this corpus.

const sec01CorpusPath = "testdata/sec01_injection_variants.json"

type sec01Entry struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Class       string `json:"class"`
	Split       string `json:"split"`
	Lang        string `json:"lang"`
	Expect      string `json:"expect"`
	Description string `json:"description"`
	KnownGap    bool   `json:"known_gap,omitempty"`
}

func loadSEC01Corpus(t *testing.T) []sec01Entry {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(sec01CorpusPath))
	if err != nil {
		t.Fatalf("read SEC-01 corpus: %v", err)
	}
	var doc struct {
		Entries []sec01Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse SEC-01 corpus: %v", err)
	}
	if len(doc.Entries) == 0 {
		t.Fatal("SEC-01 corpus has no entries")
	}
	return doc.Entries
}

// sec01Scan runs the trust_mode=scan change-gate seam over one tool.
func sec01Scan(server, name, desc, schema string) (verdict string, coverageOK bool, signals []string) {
	if schema == "" {
		schema = `{"type":"object"}`
	}
	tool := &config.ToolMetadata{ServerName: server, Name: name, Description: desc, ParamsJSON: schema}
	verdict, findings, coverageOK := ScanToolMetadataVerdict(server, []*config.ToolMetadata{tool}, nil)
	for _, f := range findings {
		signals = append(signals, f.Signals...)
	}
	return verdict, coverageOK, signals
}

func TestSEC01_VariantCorpus_ScanGateOutcomes(t *testing.T) {
	entries := loadSEC01Corpus(t)

	type tally struct{ total, dangerous, warnings, clean int }
	byClass := map[string]*tally{}

	for _, e := range entries {
		verdict, coverageOK, sigs := sec01Scan("sec01", "probe_tool", e.Description, "")
		if !coverageOK {
			t.Fatalf("%s: scan coverage unexpectedly degraded (bundle missing?)", e.ID)
		}
		k := e.Label + "/" + e.Class + "/" + e.Split
		if byClass[k] == nil {
			byClass[k] = &tally{}
		}
		tl := byClass[k]
		tl.total++
		switch verdict {
		case "clean":
			tl.clean++
		case "dangerous":
			tl.dangerous++
		default:
			tl.warnings++
		}
		t.Logf("SEC01 id=%s label=%s class=%s split=%s lang=%s known_gap=%v verdict=%s signals=%v",
			e.ID, e.Label, e.Class, e.Split, e.Lang, e.KnownGap, verdict, sigs)

		switch {
		case e.KnownGap:
			// Reported in the table, deliberately not asserted.
		case e.Label == "malicious" && e.Expect == "held":
			if verdict == "clean" {
				t.Errorf("%s (%s/%s): hostile variant scanned clean and would be scan-approved", e.ID, e.Class, e.Split)
			}
		case e.Label == "benign" && e.Expect == "clean":
			if verdict != "clean" {
				t.Errorf("%s (%s): benign definition is no longer auto-approvable: verdict=%s signals=%v", e.ID, e.Class, verdict, sigs)
			}
		case e.Label == "benign" && e.Expect == "not_dangerous":
			if verdict == "dangerous" {
				t.Errorf("%s (%s): quoted/described example is hard-blocked: signals=%v", e.ID, e.Class, sigs)
			}
		default:
			t.Fatalf("%s: unknown label/expect combination %q/%q", e.ID, e.Label, e.Expect)
		}
	}

	keys := make([]string, 0, len(byClass))
	for k := range byClass {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "\nSEC01-TABLE %-50s %5s %9s %8s %5s\n", "label/class/split", "total", "dangerous", "warnings", "clean")
	for _, k := range keys {
		tl := byClass[k]
		fmt.Fprintf(&b, "SEC01-TABLE %-50s %5d %9d %8d %5d\n", k, tl.total, tl.dangerous, tl.warnings, tl.clean)
	}
	t.Log(b.String())
}

// Pre-change (origin/main c9c1ad2d1) counts of real benign tools that the
// trust_mode=scan gate already holds (verdict != clean) on the two vendored
// real-world corpora. The detector change must not raise them.
const (
	sec01LiveMCPToolBaselineHeld = 17
	sec01ReferenceBaselineHeld   = 0
)

// TestSEC01_RealWorldBenignCorpora_NoNewHolds measures the false-positive side
// of SEC-01 on real tool definitions the repository already vendors: the
// 527-tool LiveMCPTool snapshot (70 servers, includes Chinese descriptions) and
// the 45-tool reference-server corpus. Each tool is scanned on its own, exactly
// as the change gate scans one changed tool.
func TestSEC01_RealWorldBenignCorpora_NoNewHolds(t *testing.T) {
	type rwTool struct {
		Server      string          `json:"server"`
		Tool        string          `json:"tool"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
		Schema      json.RawMessage `json:"schema"`
	}
	load := func(path string) []rwTool {
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc struct {
			Tools []rwTool `json:"tools"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		return doc.Tools
	}
	corpora := []struct {
		name    string
		path    string
		maxHeld int
	}{
		{"livemcptool_527", "../../../specs/083-discovery-profiler/datasets/livemcptool_snapshot/tools.json", sec01LiveMCPToolBaselineHeld},
		{"reference_45", "../../../specs/065-evaluation-foundation/datasets/corpus_v1.tools.json", sec01ReferenceBaselineHeld},
	}
	for _, c := range corpora {
		tools := load(c.path)
		held := 0
		for _, tl := range tools {
			name := tl.Name
			if name == "" {
				name = tl.Tool
			}
			schema := tl.InputSchema
			if len(schema) == 0 {
				schema = tl.Schema
			}
			verdict, _, sigs := sec01Scan(tl.Server, name, tl.Description, string(schema))
			if verdict != "clean" {
				held++
				t.Logf("SEC01-FP corpus=%s tool=%s:%s verdict=%s signals=%v", c.name, tl.Server, name, verdict, sigs)
			}
		}
		t.Logf("SEC01-FP corpus=%s tools=%d held=%d baseline_max=%d", c.name, len(tools), held, c.maxHeld)
		if c.maxHeld >= 0 && held > c.maxHeld {
			t.Errorf("corpus %s: %d real benign tools are now held, more than the pre-change %d", c.name, held, c.maxHeld)
		}
	}
}
