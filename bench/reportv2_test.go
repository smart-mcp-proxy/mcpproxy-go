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
		Latency: &LatencyV2{
			P50Ms: 4.2, P95Ms: 9.8, P99Ms: 15.1, MaxMs: 22.0,
			RESTSearch:   &LatencyAggregate{P50Ms: 4.2, P95Ms: 9.8, P99Ms: 15.1, MaxMs: 22.0},
			MCPDiscovery: &LatencyAggregate{P50Ms: 180.5, P95Ms: 310.0, P99Ms: 402.7, MaxMs: 511.3},
		},
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
		return // unreachable; satisfies golangci-lint v2.6.2 staticcheck SA5011
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

// --- Spec 103 additive blocks: replay / agent_loop / payload_decomposition ---
//
// These blocks are additive optional members of the SAME v2 envelope
// (specs/103-token-bench/contracts/report-v2-additions.md): no report_version
// bump, and the document-level Tokenizer field keeps its existing meaning.
// What the tests below pin is the pair of properties that make the additions
// honest — a populated per-block accounting_source, so a reader never has to
// infer a figure's scope from the document-level tokenizer, and a per-ROW
// provenance value, because FR-013 lets measured and estimated figures coexist
// inside one block where the section-level badge can only say one thing.

// sampleReplayBlock is a bodies-off replay block: menu cost per cell plus the
// direct-cell delta, with the absolute workload figure explicitly withheld
// (never zero) and the exclusion accounting populated.
func sampleReplayBlock() *ReplayBlock {
	estimatedResponse := 12500
	return &ReplayBlock{
		AccountingSource: AccountingSource{Kind: AccountingKindTokenizer, Identity: "cl100k_base"},
		Counterfactual:   "cost recomputation over recorded traffic scored against the supplied fleet; not observed agent behaviour",
		BodiesIncluded:   false,
		FleetShape: FleetShape{
			ID: "corpus_v2@2026-07-14", ToolCount: 45,
			MeanDefinitionTokens: 444.4, P95DefinitionTokens: 900,
		},
		SessionsSupplied: 12,
		SessionsUsed:     9,
		Exclusions: []ReplayExclusion{
			{Reason: ReplayExclusionTruncated, Sessions: 2},
			{Reason: ReplayExclusionSensitive, Sessions: 1},
		},
		Cells: []ReplayCellCost{
			{
				CellID: "direct_full", Provenance: ProvenanceMeasured,
				Calls: 30, MenuTokens: 20000,
				AbsoluteWorkloadWithheld: true,
				WithheldReason:           "bodies-off: consumed response text is absent",
			},
			{
				CellID: "direct_deferred", Provenance: ProvenanceEstimated,
				Calls: 30, MenuTokens: 6000, ResponseTokens: &estimatedResponse,
				AbsoluteWorkloadWithheld: true,
				WithheldReason:           "bodies-off: consumed response text is absent",
			},
		},
		DirectDelta: &ReplayDirectDelta{
			FromCellID: "direct_full", ToCellID: "direct_deferred",
			Provenance: ProvenanceComputed, DeltaTokens: -14000, DeltaPct: -70.0,
		},
	}
}

// sampleAgentLoopBlock is a live block: provider-reported usage against a
// pinned model, one headline-eligible cell and one that is not (runs < 4).
func sampleAgentLoopBlock() *AgentLoopBlock {
	return &AgentLoopBlock{
		AccountingSource: AccountingSource{
			Kind: AccountingKindProvider, Identity: "anthropic", Model: "claude-opus-4-5-20260101",
		},
		Suite:        "mcpmark",
		SuiteVersion: "v0.3.1",
		FleetShape:   FleetShape{ID: "corpus_v2@2026-07-14", ToolCount: 45},
		Cells: []AgentLoopCell{
			{
				CellID: "baseline", Provenance: ProvenanceMeasured, Runs: 5, SpreadPct: 8.4,
				TokensPerCompletedTask: 41000, CompletionRatePct: 92.0, FirstAttemptSuccessPct: 71.0,
				RetriesCorrective: 6, RetriesInfrastructure: 1,
				InputTokens: 180000, OutputTokens: 12000, CacheReadTokens: 60000,
				Headline: true,
			},
			{
				CellID: "retrieve_compact", Provenance: ProvenanceEstimated, Runs: 2, SpreadPct: 21.0,
				TokensPerCompletedTask: 18000, CompletionRatePct: 74.0, FirstAttemptSuccessPct: 55.0,
				RetriesCorrective: 11, RetriesInfrastructure: 0, PartialRuns: 1,
				InputTokens: 70000, OutputTokens: 9000, CacheReadTokens: 21000,
				Headline: false, Regression: true,
			},
		},
	}
}

// samplePayloadDecomposition attributes definition cost across two fleet
// shapes, each with its own recomputed ceiling and spec-102 verdict.
func samplePayloadDecomposition() *PayloadDecompositionBlock {
	delta := -12.5
	return &PayloadDecompositionBlock{
		AccountingSource: AccountingSource{Kind: AccountingKindTokenizer, Identity: "cl100k_base"},
		Shapes: []PayloadDecompositionRow{
			{
				FleetShape:           FleetShape{ID: "corpus_v2@2026-07-14", ToolCount: 45},
				Provenance:           ProvenanceMeasured,
				ShareNamesPct:        3.1,
				ShareDescriptionsPct: 21.4,
				ShareAnnotationsPct:  1.5,
				ShareSchemasPct:      74.0,
				AchievableCeilingPct: 68.2,
				Spec102Verdict:       Spec102Confirmed,
			},
			{
				FleetShape:           FleetShape{ID: "fleet_527@2026-08-01", ToolCount: 527},
				Provenance:           ProvenanceMeasured,
				ShareNamesPct:        2.2,
				ShareDescriptionsPct: 30.8,
				ShareAnnotationsPct:  0.9,
				ShareSchemasPct:      66.1,
				AchievableCeilingPct: 55.7,
				Spec102Verdict:       Spec102Corrected,
				Spec102DeltaPct:      &delta,
			},
		},
	}
}

// sampleReportV2WithBlocks is the sample envelope carrying all three additive
// blocks — the fixture the schema and accounting assertions run against.
func sampleReportV2WithBlocks() *ReportV2 {
	r := sampleReportV2()
	r.Replay = sampleReplayBlock()
	r.AgentLoop = sampleAgentLoopBlock()
	r.PayloadDecomposition = samplePayloadDecomposition()
	return r
}

// TestReportV2_AdditiveBlocksAreAdditive guards the contract's versioning
// rule: the new blocks must not change the envelope's version and must not
// narrow the document-level tokenizer identity, either of which would be a
// meaning change requiring a report_version bump.
func TestReportV2_AdditiveBlocksAreAdditive(t *testing.T) {
	if ReportVersion2 != 2 {
		t.Fatalf("ReportVersion2 = %d, want 2 — the additive blocks must not bump the version", ReportVersion2)
	}
	r := sampleReportV2WithBlocks()
	if r.ReportVersion != 2 {
		t.Errorf("report_version = %d, want 2", r.ReportVersion)
	}
	if r.Tokenizer.Name == "" || len(r.Tokenizer.Caveat) < 10 {
		t.Error("document-level tokenizer identity must stay intact and populated")
	}

	// Omitted blocks must vanish entirely, so existing consumers and the
	// existing offline reports are byte-unaffected.
	bare, err := json.Marshal(sampleReportV2())
	if err != nil {
		t.Fatalf("marshal bare report: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(bare, &doc); err != nil {
		t.Fatalf("unmarshal bare report: %v", err)
	}
	for _, key := range []string{"replay", "agent_loop", "payload_decomposition"} {
		if _, ok := doc[key]; ok {
			t.Errorf("block %q present in a report that never set it — must be omitempty", key)
		}
	}
}

// TestReportV2_BlocksCarryPopulatedAccountingSource asserts every emitted
// block names its own accounting source. A field that exists but is never set
// does not satisfy the contract, so this checks the marshaled document rather
// than only the struct definition.
func TestReportV2_BlocksCarryPopulatedAccountingSource(t *testing.T) {
	data, err := json.Marshal(sampleReportV2WithBlocks())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{"replay", "agent_loop", "payload_decomposition"} {
		block, ok := doc[key].(map[string]interface{})
		if !ok {
			t.Errorf("block %q missing from the emitted report", key)
			continue
		}
		src, ok := block["accounting_source"].(map[string]interface{})
		if !ok {
			t.Errorf("block %q has no accounting_source object", key)
			continue
		}
		kind, _ := src["kind"].(string)
		if kind != AccountingKindTokenizer && kind != AccountingKindProvider {
			t.Errorf("block %q accounting_source.kind = %q, not in {%s,%s}",
				key, kind, AccountingKindTokenizer, AccountingKindProvider)
		}
		if identity, _ := src["identity"].(string); identity == "" {
			t.Errorf("block %q accounting_source.identity is empty — an unpopulated field does not satisfy the contract", key)
		}
		if kind == AccountingKindProvider {
			if model, _ := src["model"].(string); model == "" {
				t.Errorf("block %q is provider-sourced but pins no model", key)
			}
		}
	}
}

// TestReportV2_NewRowsCarryProvenance asserts the per-ROW provenance FR-013
// needs: every row of every new block carries a value from the closed enum,
// and a block may legitimately mix them (the sample replay block does).
func TestReportV2_NewRowsCarryProvenance(t *testing.T) {
	data, err := json.Marshal(sampleReportV2WithBlocks())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	valid := map[string]bool{
		ProvenanceMeasured: true, ProvenanceComputed: true, ProvenanceEstimated: true,
	}
	rowSets := []struct {
		block string
		rows  string
	}{
		{"replay", "cells"},
		{"agent_loop", "cells"},
		{"payload_decomposition", "shapes"},
	}
	for _, rs := range rowSets {
		block, ok := doc[rs.block].(map[string]interface{})
		if !ok {
			t.Errorf("block %q missing", rs.block)
			continue
		}
		rows, _ := block[rs.rows].([]interface{})
		if len(rows) == 0 {
			t.Errorf("block %q has no %s rows to check", rs.block, rs.rows)
			continue
		}
		for i, row := range rows {
			obj, _ := row.(map[string]interface{})
			p, _ := obj["provenance"].(string)
			if !valid[p] {
				t.Errorf("%s.%s[%d].provenance = %q, not in {measured,computed,estimated}", rs.block, rs.rows, i, p)
			}
		}
	}

	// The direct-cell delta is a row too — it is computed from two measured
	// menu costs and must say so.
	replay, _ := doc["replay"].(map[string]interface{})
	delta, ok := replay["direct_delta"].(map[string]interface{})
	if !ok {
		t.Fatal("replay.direct_delta missing")
	}
	if p, _ := delta["provenance"].(string); !valid[p] {
		t.Errorf("replay.direct_delta.provenance = %q, not in the closed enum", p)
	}

	// FR-013 concretely: measured and estimated coexist inside ONE block,
	// which the section-level Provenance map cannot express.
	cells, _ := replay["cells"].([]interface{})
	seen := map[string]bool{}
	for _, c := range cells {
		obj, _ := c.(map[string]interface{})
		p, _ := obj["provenance"].(string)
		seen[p] = true
	}
	if !seen[ProvenanceMeasured] || !seen[ProvenanceEstimated] {
		t.Error("the replay sample must mix measured and estimated rows — that mix is exactly what per-row provenance exists for")
	}
}

// TestReportV2_ValidateAdditiveBlocks exercises the emission-time guard: an
// unset accounting source or an out-of-enum row provenance is an error, not a
// silently-valid document (the schema has no additionalProperties:false and
// cannot catch an unset optional field).
func TestReportV2_ValidateAdditiveBlocks(t *testing.T) {
	if err := sampleReportV2WithBlocks().ValidateAdditiveBlocks(); err != nil {
		t.Fatalf("fully-populated sample rejected: %v", err)
	}
	// A report with no new blocks at all is valid — they are optional.
	if err := sampleReportV2().ValidateAdditiveBlocks(); err != nil {
		t.Fatalf("report without additive blocks rejected: %v", err)
	}

	t.Run("unset accounting source", func(t *testing.T) {
		r := sampleReportV2WithBlocks()
		r.Replay.AccountingSource = AccountingSource{}
		if err := r.ValidateAdditiveBlocks(); err == nil {
			t.Error("a block with an unset accounting_source must be rejected")
		}
	})

	t.Run("provider source without pinned model", func(t *testing.T) {
		r := sampleReportV2WithBlocks()
		r.AgentLoop.AccountingSource.Model = ""
		if err := r.ValidateAdditiveBlocks(); err == nil {
			t.Error("a provider-sourced block without a pinned model must be rejected")
		}
	})

	t.Run("row provenance outside the enum", func(t *testing.T) {
		r := sampleReportV2WithBlocks()
		r.AgentLoop.Cells[0].Provenance = "assumed"
		if err := r.ValidateAdditiveBlocks(); err == nil {
			t.Error("a row provenance outside {measured,computed,estimated} must be rejected")
		}
	})

	t.Run("row provenance unset", func(t *testing.T) {
		r := sampleReportV2WithBlocks()
		r.PayloadDecomposition.Shapes[1].Provenance = ""
		if err := r.ValidateAdditiveBlocks(); err == nil {
			t.Error("a row with no provenance must be rejected")
		}
	})
}

// TestReportV2_SchemaDeclaresNewBlocks reads the contract schema file itself.
// The schema has no additionalProperties:false, so an UNDECLARED block would
// validate silently and the python validation below would pass vacuously —
// this test is what makes that validation meaningful.
func TestReportV2_SchemaDeclaresNewBlocks(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean("../specs/083-discovery-profiler/contracts/report-v2.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	props, _ := schema["properties"].(map[string]interface{})

	for _, blockKey := range []string{"replay", "agent_loop", "payload_decomposition"} {
		block, ok := props[blockKey].(map[string]interface{})
		if !ok {
			t.Errorf("schema does not declare the %q block", blockKey)
			continue
		}
		required, _ := block["required"].([]interface{})
		var hasSource bool
		for _, r := range required {
			if s, _ := r.(string); s == "accounting_source" {
				hasSource = true
			}
		}
		if !hasSource {
			t.Errorf("schema block %q does not require accounting_source", blockKey)
		}
	}

	// The row-provenance enum must be closed in the schema, not merely a
	// string, or the contract file stops being the reviewed oracle.
	defs, _ := schema["$defs"].(map[string]interface{})
	prov, ok := defs["provenance"].(map[string]interface{})
	if !ok {
		t.Fatal("schema $defs.provenance missing — the row-level enum must be declared once and reused")
	}
	enum, _ := prov["enum"].([]interface{})
	got := map[string]bool{}
	for _, v := range enum {
		s, _ := v.(string)
		got[s] = true
	}
	for _, want := range []string{"measured", "computed", "estimated"} {
		if !got[want] {
			t.Errorf("schema $defs.provenance enum missing %q", want)
		}
	}
	if len(enum) != 3 {
		t.Errorf("schema $defs.provenance enum has %d values, want exactly 3 (closed enum)", len(enum))
	}
}

// TestReportV2_NewBlocksSchemaValidationPython validates the sample WITH the
// new blocks against the real contract schema (python3+jsonschema when
// available). Paired with TestReportV2_SchemaDeclaresNewBlocks above, which
// proves the validation is not vacuous.
func TestReportV2_NewBlocksSchemaValidationPython(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	if err := exec.Command(python, "-c", "import jsonschema").Run(); err != nil {
		t.Skip("python3 jsonschema module not available")
	}

	data, err := json.Marshal(sampleReportV2WithBlocks())
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
jsonschema.Draft202012Validator(schema).validate(report)
print("VALID")
`, schemaPath, reportPath)
	out, err := exec.Command(python, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("jsonschema validation of the additive blocks failed: %v\n%s", err, out)
	}
}

// TestReportV2_SchemaRejectsMalformedBlocks is the negative half of the schema
// gate. A declaration that never rejects anything is decoration: because the
// schema has no additionalProperties:false, a validation run over a
// well-formed sample would pass identically whether or not the blocks were
// declared. These mutations must each be REFUSED by the contract file.
func TestReportV2_SchemaRejectsMalformedBlocks(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	if err := exec.Command(python, "-c", "import jsonschema").Run(); err != nil {
		t.Skip("python3 jsonschema module not available")
	}
	schemaPath := filepath.Clean("../specs/083-discovery-profiler/contracts/report-v2.schema.json")

	cases := []struct {
		name   string
		mutate func(*ReportV2)
	}{
		{"replay without accounting source", func(r *ReportV2) {
			r.Replay.AccountingSource = AccountingSource{}
		}},
		{"provider block without pinned model", func(r *ReportV2) {
			r.AgentLoop.AccountingSource.Model = ""
		}},
		{"row provenance outside the enum", func(r *ReportV2) {
			r.Replay.Cells[0].Provenance = "assumed"
		}},
		{"withheld absolute workload without a reason", func(r *ReportV2) {
			r.Replay.Cells[0].WithheldReason = ""
		}},
		{"headline claimed on fewer than four runs", func(r *ReportV2) {
			r.AgentLoop.Cells[0].Runs = 2
		}},
		{"spec102 correction without its delta", func(r *ReportV2) {
			r.PayloadDecomposition.Shapes[1].Spec102DeltaPct = nil
		}},
		{"decomposition over a single fleet shape", func(r *ReportV2) {
			r.PayloadDecomposition.Shapes = r.PayloadDecomposition.Shapes[:1]
		}},
		{"replay missing its counterfactual label", func(r *ReportV2) {
			r.Replay.Counterfactual = ""
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := sampleReportV2WithBlocks()
			tc.mutate(r)
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			reportPath := filepath.Join(t.TempDir(), "report.json")
			if err := os.WriteFile(reportPath, data, 0o644); err != nil {
				t.Fatalf("write report: %v", err)
			}
			script := fmt.Sprintf(`
import json, jsonschema, sys
schema = json.load(open(%q))
report = json.load(open(%q))
try:
    jsonschema.Draft202012Validator(schema).validate(report)
except jsonschema.ValidationError as e:
    print("REJECTED:", e.message)
    sys.exit(0)
print("ACCEPTED")
sys.exit(1)
`, schemaPath, reportPath)
			out, err := exec.Command(python, "-c", script).CombinedOutput()
			if err != nil {
				t.Errorf("schema accepted a document it must reject (%s):\n%s", tc.name, out)
			}
		})
	}
}
