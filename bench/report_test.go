package bench

// report_test.go — T035: the v2 dashboard must render every report section
// (arms, corpora, response-cost percentiles, break-even, session estimates,
// LAP, subset) with provenance badges and the tokenizer caveat banner
// (SC-005), and stay fully self-contained: a single static file with no
// external http(s) resource loads (FR-018).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// externalLoadRes match HTML/CSS constructs that would fetch an external
// resource when the dashboard is opened: src/href attributes, CSS url() and
// @import. Plain https:// TEXT (e.g. an attribution link rendered as text) is
// not a resource load and stays allowed.
var externalLoadRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(src|href)\s*=\s*["']?\s*(https?:)?//`),
	regexp.MustCompile(`(?i)url\(\s*["']?\s*https?://`),
	regexp.MustCompile(`(?i)@import\b`),
	regexp.MustCompile(`(?i)<(script|link|iframe|img|video|audio|object|embed)\b[^>]*\b(src|href)\b`),
}

func renderDashboardV2(t *testing.T, r *ReportV2) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dashboard.html")
	if err := r.WriteHTML(path); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dashboard: %v", err)
	}
	return string(data)
}

func TestDashboardV2_SelfContained(t *testing.T) {
	html := renderDashboardV2(t, sampleReportV2())
	for _, re := range externalLoadRes {
		if loc := re.FindString(html); loc != "" {
			t.Errorf("dashboard loads an external resource (%s): %q", re, loc)
		}
	}
}

func TestDashboardV2_SectionsAndBadges(t *testing.T) {
	rep := sampleReportV2()
	html := renderDashboardV2(t, rep)

	// Tokenizer caveat banner (SC-005): the caveat text and encoding name.
	for _, want := range []string{rep.Tokenizer.Caveat, rep.Tokenizer.Name} {
		if !strings.Contains(html, want) {
			t.Errorf("dashboard missing tokenizer caveat content %q", want)
		}
	}

	// Provenance badges: every label used by the sample appears as a badge.
	for _, badge := range []string{ProvenanceMeasured, ProvenanceComputed, ProvenanceEstimated} {
		if !strings.Contains(html, ">"+badge+"<") {
			t.Errorf("dashboard missing provenance badge %q", badge)
		}
	}

	// Arms table: every arm row, its skip reason, and the lower-bound label.
	for _, row := range rep.Arms {
		if !strings.Contains(html, row.Arm) {
			t.Errorf("arms table missing arm %q", row.Arm)
		}
		if row.Skipped && !strings.Contains(html, row.SkipReason) {
			t.Errorf("arms table missing skip reason %q", row.SkipReason)
		}
	}

	// Corpora table: id + license + tool count.
	for _, cd := range rep.Corpora {
		if !strings.Contains(html, cd.ID) || !strings.Contains(html, cd.License) {
			t.Errorf("corpora table missing descriptor %q", cd.ID)
		}
	}

	// Response-cost percentiles + per-query component buckets.
	for _, want := range []string{"8640", "54865", "input_schemas"} {
		if !strings.Contains(html, want) {
			t.Errorf("response-cost section missing %q", want)
		}
	}

	// Break-even: inputs echoed (FR-004) and the verdict number.
	for _, want := range []string{"420000", "37.8"} {
		if !strings.Contains(html, want) {
			t.Errorf("break-even section missing %q", want)
		}
	}

	// Session estimates table.
	if !strings.Contains(html, "37000") {
		t.Error("session-estimates section missing the sample row")
	}

	// LAP row: grade + version.
	for _, want := range []string{rep.Lap.Grade, rep.Lap.Version} {
		if !strings.Contains(html, want) {
			t.Errorf("LAP section missing %q", want)
		}
	}

	// Subset info (FR-014).
	if !strings.Contains(html, "42") || !strings.Contains(html, "250") {
		t.Error("subset section missing seed/size")
	}
}

// TestDashboardV2_ConditionalSections: a minimal live-free offline report
// (arms only, no response cost / break-even / LAP / subset) must render
// without the absent sections and without template errors.
func TestDashboardV2_ConditionalSections(t *testing.T) {
	rep := sampleReportV2()
	rep.ResponseCost = nil
	rep.BreakEven = nil
	rep.SessionEstimates = nil
	rep.Lap = nil
	rep.Subset = nil
	rep.Latency = nil
	rep.Proxy = nil

	html := renderDashboardV2(t, rep)
	for _, absent := range []string{"Break-even", "LAP", "Session cost"} {
		if strings.Contains(html, absent) {
			t.Errorf("dashboard renders section %q despite absent data", absent)
		}
	}
	if !strings.Contains(html, "baseline_json") {
		t.Error("arms table missing from minimal report")
	}
}

// TestDashboardV2_LatencySurfaces: the two latency surfaces are labeled and
// never conflated (FR-023): the REST /api/v1/index/search percentiles carry
// their endpoint name, and the MCP retrieve_tools discovery aggregate renders
// its own labeled block with its own numbers.
func TestDashboardV2_LatencySurfaces(t *testing.T) {
	rep := sampleReportV2()
	html := renderDashboardV2(t, rep)
	for _, want := range []string{"/api/v1/index/search", "retrieve_tools", "180.5", "511.3"} {
		if !strings.Contains(html, want) {
			t.Errorf("latency section missing %q", want)
		}
	}

	// Without an MCP aggregate the REST block still renders, clearly labeled.
	rep.Latency.MCPDiscovery = nil
	html = renderDashboardV2(t, rep)
	if !strings.Contains(html, "/api/v1/index/search") {
		t.Error("REST latency label missing when MCP aggregate absent")
	}
	if strings.Contains(html, "180.5") {
		t.Error("MCP aggregate rendered despite being absent")
	}
}

// TestDashboardV2_NoBreakEvenVerdict: the honest NoBreakEven row renders as a
// verdict, not as "0 calls".
func TestDashboardV2_NoBreakEven(t *testing.T) {
	rep := sampleReportV2()
	rep.BreakEven = &BreakEvenAnalysis{
		NaiveFullMenuTokens: 100, ProxyMenuTokens: 5000,
		MeanResponseTokens: 11000, NoBreakEven: true,
	}
	html := renderDashboardV2(t, rep)
	if !strings.Contains(html, "no break-even") {
		t.Error("NoBreakEven verdict not rendered")
	}
}

func TestReportV2_WriteReports(t *testing.T) {
	dir := t.TempDir()
	jsonPath, htmlPath, err := sampleReportV2().WriteReports(dir)
	if err != nil {
		t.Fatalf("WriteReports: %v", err)
	}
	if filepath.Base(jsonPath) != "report.json" || filepath.Base(htmlPath) != "dashboard.html" {
		t.Errorf("unexpected report paths: %s / %s", jsonPath, htmlPath)
	}
	for _, p := range []string{jsonPath, htmlPath} {
		st, err := os.Stat(p)
		if err != nil || st.Size() == 0 {
			t.Errorf("report file %s missing or empty (err=%v)", p, err)
		}
	}
}

// TestDashboardV1_SelfContained keeps the legacy v1 dashboard honest too.
func TestDashboardV1_SelfContained(t *testing.T) {
	rep := &Report{
		Encoding: DefaultEncoding, CorpusVersion: "corpus_v1", CorpusTools: 45,
		Modes: []ModeResult{{Mode: ModeBaseline, ContextTools: 45, Tokens: 1000}},
		Notes: []string{"note"},
	}
	path := filepath.Join(t.TempDir(), "dashboard.html")
	if err := rep.WriteHTML(path); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, re := range externalLoadRes {
		if loc := re.FindString(string(data)); loc != "" {
			t.Errorf("v1 dashboard loads an external resource: %q", loc)
		}
	}
}
