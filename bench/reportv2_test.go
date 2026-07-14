package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// sampleReportV2 builds a report exercising every conditional branch of the
// contract: a non-skipped rendering arm, a skipped arm, a non-skipped
// index-altering arm (quality required), and a results-class arm row.
func sampleReportV2() *ReportV2 {
	quality := &RetrievalScore{
		RecallAt1: 0.55, RecallAt3: 0.64, RecallAt5: 0.68, RecallAt10: 0.72,
		MRR: 0.61, NDCGAt10: 0.63, MAP: 0.59,
		MetricNote: "graded relevance, linear gain, log2 discount",
	}
	tab, nontab := 3, 2
	return &ReportV2{
		ReportVersion: 2,
		GeneratedAt:   "2026-07-14T00:00:00Z",
		Tokenizer: TokenizerInfo{
			Name:   "cl100k_base",
			Caveat: "cl100k_base underestimates Claude tokenizer counts by up to ~60%; relative savings are stable.",
		},
		Proxy: &ProxyInfo{Version: "v0.47.0", ToolCount: 45, ExpectedToolCount: 45, ToolsLimit: 15, RoutingMode: "retrieve_tools"},
		Corpora: []CorpusDescriptor{
			{
				ID: "corpus_v2@2026-07-14", Name: "corpus_v2", Version: "2026-07-14",
				ToolCount: 45, License: "own capture (no-auth reference servers)", Committed: true,
				DegenerateDescriptions: &DegenerateDescriptions{Count: 0, Rules: []string{"empty", "shorter than 10 chars", "equals tool name"}},
			},
		},
		Arms: []ArmResult{
			{
				Arm: "baseline_json", CorpusID: "corpus_v2@2026-07-14", Skipped: false,
				IndexAltering: false, TotalTokens: 20000, MeanTokens: 444.4, P95Tokens: 900,
				SavingsVsBaselinePct: 0, SkippedTools: 0,
				HeaviestTools: []ToolTokenEntry{{ToolID: "sqlite:write_query", Tokens: 900}},
			},
			{
				Arm: "tscg", CorpusID: "corpus_v2@2026-07-14", Skipped: true,
				SkipReason: "node runtime unavailable",
			},
			{
				Arm: "compact_sig", CorpusID: "corpus_v2@2026-07-14", Skipped: false,
				IndexAltering: true, TotalTokens: 4000, MeanTokens: 88.9, P95Tokens: 200,
				SavingsVsBaselinePct: 80.0, SkippedTools: 1,
				SkipExamples: []SkipExample{{ToolID: "memory:weird_tool", Error: "unsupported schema construct"}},
				Quality:      quality,
			},
			{
				Arm: "toon_results", CorpusID: "result_fixtures_v1", Skipped: false,
				IndexAltering: false, TotalTokens: 1500, MeanTokens: 300, P95Tokens: 600,
				SavingsVsBaselinePct: -5.0, SkippedTools: 0,
				PayloadClass: "results", FixtureID: "result_fixtures_v1@2026-07-14",
				TabularCount: &tab, NonTabularCount: &nontab,
			},
		},
		ResponseCost: &ResponseCostSummary{
			P50: 8640, P95: 30000, Max: 54865, Mean: 11000,
			PerQuery: []DiscoveryResponseMeasurement{
				{
					QueryID: "q001", TotalTokens: 8640, ResultCount: 15, LatencyMs: 12.5,
					Components: map[string]int{
						"input_schemas": 6650, "descriptions": 1200,
						"usage_instructions": 500, "metadata": 200, "other": 90,
					},
				},
			},
		},
		BreakEven: &BreakEvenAnalysis{
			NaiveFullMenuTokens: 420000, ProxyMenuTokens: 4000,
			MeanResponseTokens: 11000, BreakEvenCalls: 37.8,
		},
		SessionEstimates: []SessionCostEstimate{
			{Arm: "baseline_json", CallsPerSession: 3, RetryRate: 0, EstimatedTokens: 37000},
		},
		Latency: &LatencyV2{P50Ms: 4.2, P95Ms: 9.8, P99Ms: 15.1, MaxMs: 22.0},
		Lap: &LapVerdict{
			Executed: true, Version: "0.8.0", MenuTokens: 4100, InHouseMenuTokens: 4000,
			DivergencePct: 2.5, Grade: "B", ArtifactPath: "bench/results/lap.json",
		},
		Subset: &SubsetInfo{Seed: 42, Size: 250},
		Provenance: map[string]string{
			"response_cost":     ProvenanceMeasured,
			"break_even":        ProvenanceComputed,
			"session_estimates": ProvenanceEstimated,
		},
	}
}

func TestReportV2_MarshalStructure(t *testing.T) {
	data, err := json.Marshal(sampleReportV2())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}

	// Contract top-level required keys.
	for _, key := range []string{"report_version", "generated_at", "tokenizer", "corpora", "arms", "provenance"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("required top-level key %q missing", key)
		}
	}
	if v, ok := doc["report_version"].(float64); !ok || v != 2 {
		t.Errorf("report_version = %v, want const 2", doc["report_version"])
	}

	// tokenizer requires name + caveat (caveat minLength 10).
	tok, _ := doc["tokenizer"].(map[string]interface{})
	if name, _ := tok["name"].(string); name == "" {
		t.Error("tokenizer.name missing")
	}
	if caveat, _ := tok["caveat"].(string); len(caveat) < 10 {
		t.Errorf("tokenizer.caveat too short (%d chars): %q", len(caveat), tok["caveat"])
	}

	// corpora items require id/name/version/tool_count/license/committed.
	corpora, _ := doc["corpora"].([]interface{})
	if len(corpora) == 0 {
		t.Fatal("corpora empty")
	}
	for i, c := range corpora {
		obj := c.(map[string]interface{})
		for _, key := range []string{"id", "name", "version", "tool_count", "license", "committed"} {
			if _, ok := obj[key]; !ok {
				t.Errorf("corpora[%d] missing required key %q", i, key)
			}
		}
	}

	// Arm conditional rules.
	arms, _ := doc["arms"].([]interface{})
	if len(arms) != 4 {
		t.Fatalf("expected 4 arm rows, got %d", len(arms))
	}
	for i, a := range arms {
		obj := a.(map[string]interface{})
		for _, key := range []string{"arm", "corpus_id", "skipped"} {
			if _, ok := obj[key]; !ok {
				t.Errorf("arms[%d] missing required key %q", i, key)
			}
		}
		skipped, _ := obj["skipped"].(bool)
		if skipped {
			if _, ok := obj["skip_reason"]; !ok {
				t.Errorf("arms[%d] skipped without skip_reason", i)
			}
			continue
		}
		for _, key := range []string{"index_altering", "total_tokens", "mean_tokens", "p95_tokens", "savings_vs_baseline_pct", "skipped_tools"} {
			if _, ok := obj[key]; !ok {
				t.Errorf("arms[%d] (non-skipped) missing required key %q", i, key)
			}
		}
		if obj["payload_class"] == "results" {
			for _, key := range []string{"fixture_id", "tabular_count", "non_tabular_count"} {
				if _, ok := obj[key]; !ok {
					t.Errorf("arms[%d] (results row) missing required key %q", i, key)
				}
			}
		}
		if ia, _ := obj["index_altering"].(bool); ia {
			if _, ok := obj["quality"]; !ok {
				t.Errorf("arms[%d] index-altering without quality key", i)
			}
		}
	}

	// provenance values are the closed enum.
	prov, _ := doc["provenance"].(map[string]interface{})
	for k, v := range prov {
		s, _ := v.(string)
		if s != ProvenanceMeasured && s != ProvenanceComputed && s != ProvenanceEstimated {
			t.Errorf("provenance[%q] = %q, not in {measured,computed,estimated}", k, s)
		}
	}

	// response_cost per-query components carry the five contract buckets.
	rc, _ := doc["response_cost"].(map[string]interface{})
	perQuery, _ := rc["per_query"].([]interface{})
	for i, q := range perQuery {
		comp, _ := q.(map[string]interface{})["components"].(map[string]interface{})
		for _, bucket := range []string{"input_schemas", "descriptions", "usage_instructions", "metadata", "other"} {
			if _, ok := comp[bucket]; !ok {
				t.Errorf("response_cost.per_query[%d].components missing bucket %q", i, bucket)
			}
		}
	}

	// lap requires executed.
	lap, _ := doc["lap"].(map[string]interface{})
	if _, ok := lap["executed"]; !ok {
		t.Error("lap.executed missing")
	}
}

// TestReportV2_SchemaValidationPython validates the sample against the actual
// contract schema file with python3+jsonschema when available (skipped
// otherwise; the structural test above is the always-on gate — adding a Go
// jsonschema dependency is not allowed by the plan).
func TestReportV2_SchemaValidationPython(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	if err := exec.Command(python, "-c", "import jsonschema").Run(); err != nil {
		t.Skip("python3 jsonschema module not available")
	}

	data, err := json.Marshal(sampleReportV2())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.json")
	if err := os.WriteFile(reportPath, data, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	schemaPath := filepath.Clean("../specs/083-discovery-profiler/contracts/report-v2.schema.json")

	script := fmt.Sprintf(`
import json, jsonschema
schema = json.load(open(%q))
report = json.load(open(%q))
jsonschema.validate(report, schema)
print("VALID")
`, schemaPath, reportPath)
	out, err := exec.Command(python, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("jsonschema validation failed: %v\n%s", err, out)
	}
}

func TestMapRetrievalMetrics(t *testing.T) {
	if MapRetrievalMetrics(nil) != nil {
		t.Fatal("MapRetrievalMetrics(nil) must be nil (quality-neutral arm)")
	}

	src := &RetrievalMetrics{
		CorpusVersion: "corpus_v1",
		Metrics: RetrievalMetricValues{
			RecallAt: map[int]float64{1: 0.5, 3: 0.6, 5: 0.68, 10: 0.75},
			MRR:      0.61,
			NDCGAt10: 0.63,
			MAP:      0.59,
		},
	}
	got := MapRetrievalMetrics(src)
	if got == nil {
		t.Fatal("MapRetrievalMetrics returned nil for non-nil input")
	}
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"recall_at_1", got.RecallAt1, 0.5},
		{"recall_at_3", got.RecallAt3, 0.6},
		{"recall_at_5", got.RecallAt5, 0.68},
		{"recall_at_10", got.RecallAt10, 0.75},
		{"mrr", got.MRR, 0.61},
		{"ndcg_at_10", got.NDCGAt10, 0.63},
		{"map", got.MAP, 0.59},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestReportV2_WriteJSONDeterministic guards FR-010: identical report structs
// marshal to identical bytes (map keys sorted by encoding/json; no wall-clock
// injected by the writer — GeneratedAt is caller-supplied data).
func TestReportV2_WriteJSONDeterministic(t *testing.T) {
	a, err := json.Marshal(sampleReportV2())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b, err := json.Marshal(sampleReportV2())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(a) != string(b) {
		t.Error("ReportV2 marshaling is not deterministic")
	}
}
