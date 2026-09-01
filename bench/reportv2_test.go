package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		SessionEstimates: []SessionCostRow{
			{
				SessionCostEstimate: SessionCostEstimate{
					Arm: "baseline_json", CallsPerSession: 3, RetryRate: 0, EstimatedTokens: 37000,
				},
				// RetryRate 0 with an ESTIMATED source: the defaulted zero.
				// The badge is the only thing separating it from a measured
				// zero, which is why the row type exists.
				Provenance:          ProvenanceEstimated,
				RetryRateProvenance: ProvenanceEstimated,
			},
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
		// The recording is the operator's own activity export: marked, per
		// FR-030, as an input nobody outside the project can obtain.
		Inputs:  PrivateRecordingInputs("recorded on the maintainer's machine via `mcpproxy activity export`; raw user traffic, never publishable"),
		Records: RetainedRecords("records/replay.jsonl", 9),
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
		Inputs:       ReproducibleInputs(InputAvailabilityPinnedProcedure, "MCPMark v0.3.1, pinned suite revision; see bench/README.md"),
		Records:      RetainedRecords("records/agent_loop.jsonl", 7),
		FleetShape:   FleetShape{ID: "corpus_v2@2026-07-14", ToolCount: 45},
		Cells: []AgentLoopCell{
			{
				CellID: "baseline", Provenance: ProvenanceMeasured, Runs: 5, SpreadPct: fptr(8.4),
				TokensPerCompletedTask: 41000, CompletionRatePct: 92.0, FirstAttemptSuccessPct: fptr(71.0),
				RetriesCorrective: 6, RetriesInfrastructure: 1,
				InputTokens: 180000, OutputTokens: 12000, CacheReadTokens: 60000,
				Headline: true,
			},
			{
				CellID: "retrieve_compact", Provenance: ProvenanceEstimated, Runs: 2, SpreadPct: fptr(21.0),
				TokensPerCompletedTask: 18000, CompletionRatePct: 74.0, FirstAttemptSuccessPct: fptr(55.0),
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
	annShare45, annShare527 := 1.5, 0.9
	delta := -12.5
	return &PayloadDecompositionBlock{
		AccountingSource: AccountingSource{Kind: AccountingKindTokenizer, Identity: "cl100k_base"},
		Shapes: []PayloadDecompositionRow{
			{
				FleetShape:           FleetShape{ID: "corpus_v2@2026-07-14", ToolCount: 45},
				Provenance:           ProvenanceMeasured,
				ShareNamesPct:        3.1,
				ShareDescriptionsPct: 21.4,
				ShareAnnotationsPct:  &annShare45,
				ShareSchemasPct:      74.0,
				AchievableCeilingPct: 68.2,
				Spec102Verdict:       Spec102Confirmed,
			},
			{
				FleetShape:           FleetShape{ID: "fleet_527@2026-08-01", ToolCount: 527},
				Provenance:           ProvenanceMeasured,
				ShareNamesPct:        2.2,
				ShareDescriptionsPct: 30.8,
				ShareAnnotationsPct:  &annShare527,
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

// ---------------------------------------------------------------------------
// US3 — an outsider reproduces the numbers (T054/T056/T057/T058).
//
// The three behaviours asserted below are the ones that separate an honest
// limitation from an implied guarantee, so each is tested against the
// MARSHALED document as well as the struct: a mark that exists in Go but never
// reaches the report protects nobody.
// ---------------------------------------------------------------------------

// TestInputProvenance_PrivateRecordingIsNotIndependentlyReproducible is the
// FR-030 mark (T056). A replay block sourced from an operator's own recording
// cannot be reproduced by an outsider — the recording is private and must stay
// so (FR-006) — and the report has to say that in the document, not in a
// README an aggregator will never read.
func TestInputProvenance_PrivateRecordingIsNotIndependentlyReproducible(t *testing.T) {
	p := PrivateRecordingInputs("exported from the maintainer's own mcpproxy activity log; the recording is not publishable")
	if p.Availability != InputAvailabilityPrivateRecording {
		t.Errorf("availability = %q, want %q", p.Availability, InputAvailabilityPrivateRecording)
	}
	if p.IndependentlyReproducible {
		t.Error("a private-recording input must never be marked independently reproducible")
	}
	if p.Limitation == "" {
		t.Error("a private-recording input must state its limitation")
	}
	if err := p.Validate("replay"); err != nil {
		t.Fatalf("well-formed private-recording provenance rejected: %v", err)
	}

	// The converse, so the mark is not vacuously always-false.
	pub := ReproducibleInputs(InputAvailabilityRepository, "specs/083-discovery-profiler/datasets/corpus_v2.tools.json")
	if !pub.IndependentlyReproducible {
		t.Error("a repository input must be marked independently reproducible")
	}
	if err := pub.Validate("payload_decomposition"); err != nil {
		t.Fatalf("well-formed repository provenance rejected: %v", err)
	}

	t.Run("mismarked as reproducible", func(t *testing.T) {
		bad := PrivateRecordingInputs("private export")
		bad.IndependentlyReproducible = true
		if err := bad.Validate("replay"); err == nil {
			t.Error("a private-recording input claiming independent reproducibility must be rejected")
		}
	})
	t.Run("limitation not stated", func(t *testing.T) {
		bad := PrivateRecordingInputs("")
		if err := bad.Validate("replay"); err == nil {
			t.Error("a not-reproducible mark with no stated limitation is the implied guarantee this field exists to prevent")
		}
	})
	t.Run("pinned procedure not named", func(t *testing.T) {
		bad := ReproducibleInputs(InputAvailabilityPinnedProcedure, "")
		if err := bad.Validate("agent_loop"); err == nil {
			t.Error("a 'documented, pinned procedure' that names no procedure is not documented")
		}
	})
	t.Run("availability outside the enum", func(t *testing.T) {
		bad := &InputProvenance{Availability: "somewhere", IndependentlyReproducible: true}
		if err := bad.Validate("replay"); err == nil {
			t.Error("an availability outside the closed enum must be rejected")
		}
	})

	// The mark must survive marshaling onto the block that carries it.
	r := sampleReportV2WithBlocks()
	r.Replay.Inputs = PrivateRecordingInputs("private activity export")
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	replay, _ := doc["replay"].(map[string]interface{})
	inputs, ok := replay["inputs"].(map[string]interface{})
	if !ok {
		t.Fatal("replay.inputs missing from the emitted report")
	}
	if repro, _ := inputs["independently_reproducible"].(bool); repro {
		t.Error("emitted replay.inputs.independently_reproducible = true for a private recording")
	}
}

// TestPublicationCheck_PrivateFigureIsNeverSoleSupport is the second half of
// FR-030 (T056): the mark alone is not enough, because a marked figure can
// still be the only figure in the document. A report in which EVERY block
// rests on a private recording is refused for publication outright; the same
// private block beside a reproducible one publishes with a caveat.
func TestPublicationCheck_PrivateFigureIsNeverSoleSupport(t *testing.T) {
	soleSupport := &ReportV2{
		ReportVersion: ReportVersion2,
		RunStatus:     DeriveRunStatus([]string{"direct_full", "direct_deferred"}, []string{"direct_full", "direct_deferred"}, nil, false, ""),
		Replay:        sampleReplayBlock(),
	}
	soleSupport.Replay.Inputs = PrivateRecordingInputs("maintainer's own activity export")
	soleSupport.Replay.Records = RetainedRecords("records/replay.jsonl", 120)

	decision := soleSupport.PublicationCheck()
	if decision.Publishable {
		t.Error("a report whose only figures come from a private recording must not be publishable (FR-030)")
	}
	if !containsSubstring(decision.Blockers, "sole support") {
		t.Errorf("blockers do not name the sole-support rule: %v", decision.Blockers)
	}

	// Add a block an outsider CAN reproduce: the private figure is no longer
	// the sole support, so it publishes — carrying its caveat.
	corroborated := soleSupport
	corroborated.PayloadDecomposition = samplePayloadDecomposition()
	corroborated.PayloadDecomposition.Inputs = ReproducibleInputs(
		InputAvailabilityRepository, "specs/083-discovery-profiler/datasets/corpus_v2.tools.json")

	decision = corroborated.PublicationCheck()
	if !decision.Publishable {
		t.Errorf("a private figure corroborated by a reproducible block must be publishable: %v", decision.Blockers)
	}
	if !containsSubstring(decision.Caveats, "not independently reproducible") {
		t.Errorf("the caveat must survive publication, not disappear with the blocker: %v", decision.Caveats)
	}
}

// TestPublicationCheck_UndeclaredInputsAreNotPublishable: an unset field is not
// a reproducible input. Silence is never success anywhere else in this
// harness, and it must not become success at the publication boundary.
func TestPublicationCheck_UndeclaredInputsAreNotPublishable(t *testing.T) {
	r := &ReportV2{
		ReportVersion:        ReportVersion2,
		RunStatus:            DeriveRunStatus([]string{"a"}, []string{"a"}, nil, false, ""),
		PayloadDecomposition: samplePayloadDecomposition(), // no Inputs set
	}
	decision := r.PublicationCheck()
	if decision.Publishable {
		t.Error("a block that never declared its input availability must not be publishable")
	}
	if !containsSubstring(decision.Blockers, "payload_decomposition") {
		t.Errorf("blockers do not name the offending block: %v", decision.Blockers)
	}
}

// TestRunStatus_PartialRunIsMarkedAndBlocked is FR-032 (T058). A run that did
// not measure every planned cell, or that was interrupted, is a comparison
// over a subset; presented as complete it is a comparison over a subset
// presented as the whole.
func TestRunStatus_PartialRunIsMarkedAndBlocked(t *testing.T) {
	planned := []string{"baseline", "retrieve_full", "retrieve_compact", "direct_full", "direct_deferred", "code_exec"}

	t.Run("missing cell", func(t *testing.T) {
		s := DeriveRunStatus(planned, planned[:4], []string{"code_exec"}, false, "")
		if s.Completeness != RunCompletenessPartial {
			t.Errorf("completeness = %q, want %q", s.Completeness, RunCompletenessPartial)
		}
		if len(s.MissingCells) != 1 || s.MissingCells[0] != "direct_deferred" {
			t.Errorf("missing cells = %v, want [direct_deferred]", s.MissingCells)
		}
		if s.Reason == "" {
			t.Error("a partial run must carry a reason; an unexplained partial is the silence FR-032 forbids")
		}
		if err := s.Validate(); err != nil {
			t.Fatalf("derived status rejected by its own validation: %v", err)
		}
	})

	t.Run("interrupted with every cell measured", func(t *testing.T) {
		s := DeriveRunStatus(planned, planned, nil, true, "SIGINT during the third repetition")
		if s.Completeness != RunCompletenessPartial {
			t.Error("an interrupted run is partial even when every planned cell produced a figure")
		}
	})

	t.Run("complete", func(t *testing.T) {
		s := DeriveRunStatus(planned, planned[:5], []string{"code_exec"}, false, "")
		if s.Completeness != RunCompletenessComplete {
			t.Errorf("completeness = %q, want %q — a deliberately skipped cell with a stated reason is not a gap",
				s.Completeness, RunCompletenessComplete)
		}
		if err := s.Validate(); err != nil {
			t.Fatalf("complete status rejected: %v", err)
		}
	})

	t.Run("hand-stamped complete over a gap", func(t *testing.T) {
		s := DeriveRunStatus(planned, planned[:4], nil, false, "")
		s.Completeness = RunCompletenessComplete
		if err := s.Validate(); err == nil {
			t.Error("a status claiming complete while cells are unaccounted for must be rejected")
		}
	})

	t.Run("blocked from publication", func(t *testing.T) {
		r := sampleReportV2WithBlocks()
		r.Replay.Inputs = ReproducibleInputs(InputAvailabilityRepository, "committed corpus")
		r.AgentLoop.Inputs = ReproducibleInputs(InputAvailabilityPinnedProcedure, "mcpmark v0.3.1")
		r.PayloadDecomposition.Inputs = ReproducibleInputs(InputAvailabilityRepository, "committed corpus")
		r.Replay.Records = RetainedRecords("records/replay.jsonl", 9)
		r.AgentLoop.Records = RetainedRecords("records/agent_loop.jsonl", 7)

		r.RunStatus = DeriveRunStatus(planned, planned, nil, false, "")
		if d := r.PublicationCheck(); !d.Publishable {
			t.Fatalf("a complete, reproducible report must be publishable: %v", d.Blockers)
		}

		r.RunStatus = DeriveRunStatus(planned, planned[:4], nil, true, "operator interrupted the run")
		d := r.PublicationCheck()
		if d.Publishable {
			t.Error("a partial run must not be publishable as a complete comparison (FR-032)")
		}
		if !containsSubstring(d.Blockers, "partial") {
			t.Errorf("blockers do not name the run as partial: %v", d.Blockers)
		}
	})

	t.Run("undeclared completeness", func(t *testing.T) {
		r := sampleReportV2WithBlocks()
		r.Replay.Inputs = ReproducibleInputs(InputAvailabilityRepository, "committed corpus")
		r.RunStatus = nil
		if r.PublicationCheck().Publishable {
			t.Error("a report that never declared its run completeness must not be publishable")
		}
	})

	// The mark must reach the document.
	r := sampleReportV2WithBlocks()
	r.RunStatus = DeriveRunStatus(planned, planned[:4], nil, true, "operator interrupted the run")
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	status, ok := doc["run_status"].(map[string]interface{})
	if !ok {
		t.Fatal("run_status missing from the emitted report")
	}
	if c, _ := status["completeness"].(string); c != RunCompletenessPartial {
		t.Errorf("emitted run_status.completeness = %q, want %q", c, RunCompletenessPartial)
	}
}

// TestRawRecordsRef_RunLocalAndNotDurable is FR-029 (T057), report side. The
// records live under the gitignored bench/results/ tree, so the reference is
// RUN-LOCAL and explicitly non-durable: an absolute path would leak the
// operator's filesystem into a published document and would dangle on any
// other machine, and a reference with no durability note invites a reader to
// treat a scratch directory as an archive.
func TestRawRecordsRef_RunLocalAndNotDurable(t *testing.T) {
	ref := RetainedRecords("records/replay-2026-09-01.jsonl", 120)
	if ref.Retention != RecordsRetained {
		t.Errorf("retention = %q, want %q", ref.Retention, RecordsRetained)
	}
	if ref.Durable {
		t.Error("no bench/results record is durable — a results cleanup removes it")
	}
	if ref.Note == "" {
		t.Error("a records reference must carry its non-durability note")
	}
	if err := ref.Validate("replay"); err != nil {
		t.Fatalf("well-formed run-local reference rejected: %v", err)
	}

	notRetained := NotRetainedRecords("bodies-off run: no per-record artifact was written")
	if notRetained.Retention != RecordsNotRetained {
		t.Errorf("retention = %q, want %q", notRetained.Retention, RecordsNotRetained)
	}
	if notRetained.RunLocalPath != "" {
		t.Error("a not-retained reference must carry no path — that is precisely the dangling reference it replaces")
	}
	if err := notRetained.Validate("replay"); err != nil {
		t.Fatalf("well-formed not-retained reference rejected: %v", err)
	}

	bad := []struct {
		name string
		ref  *RawRecordsRef
	}{
		{"absolute path", &RawRecordsRef{Retention: RecordsRetained, RunLocalPath: "/Users/someone/bench/results/replay.jsonl", Note: RecordsNotDurableNote}},
		{"escaping path", &RawRecordsRef{Retention: RecordsRetained, RunLocalPath: "../../secrets.jsonl", Note: RecordsNotDurableNote}},
		{"retained with no path", &RawRecordsRef{Retention: RecordsRetained, Note: RecordsNotDurableNote}},
		{"claimed durable", &RawRecordsRef{Retention: RecordsRetained, RunLocalPath: "records/r.jsonl", Durable: true, Note: RecordsNotDurableNote}},
		{"not retained but still pointing somewhere", &RawRecordsRef{Retention: RecordsNotRetained, RunLocalPath: "records/r.jsonl", Note: "gone"}},
		{"retention outside the enum", &RawRecordsRef{Retention: "archived", RunLocalPath: "records/r.jsonl", Note: RecordsNotDurableNote}},
		{"no note", &RawRecordsRef{Retention: RecordsRetained, RunLocalPath: "records/r.jsonl"}},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.ref.Validate("replay"); err == nil {
				t.Errorf("malformed records reference accepted: %+v", tc.ref)
			}
		})
	}
}

// TestRawRecordsRef_DegradesRatherThanDangles is the other half of T057: the
// report is written to the same gitignored tree the records live in, so by the
// time anyone reads it the records may already have been cleaned away. The
// reference must then say "records not retained" — a reader who follows a
// dangling path concludes the harness is broken, or worse, that the figure was
// never traceable at all.
func TestRawRecordsRef_DegradesRatherThanDangles(t *testing.T) {
	t.Run("records absent", func(t *testing.T) {
		dir := t.TempDir()
		r := sampleReportV2WithBlocks()
		r.Replay.Records = RetainedRecords("records/replay.jsonl", 120)

		path, err := r.WriteJSON(dir)
		if err != nil {
			t.Fatalf("WriteJSON: %v", err)
		}
		raw, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			t.Fatalf("read report: %v", err)
		}
		var doc map[string]interface{}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("unmarshal report: %v", err)
		}
		replay, _ := doc["replay"].(map[string]interface{})
		records, ok := replay["records"].(map[string]interface{})
		if !ok {
			t.Fatal("replay.records missing from the emitted report")
		}
		if got, _ := records["retention"].(string); got != RecordsNotRetained {
			t.Errorf("retention = %q, want %q — an absent artifact must degrade, not dangle", got, RecordsNotRetained)
		}
		if p, _ := records["run_local_path"].(string); p != "" {
			t.Errorf("degraded reference still carries a path %q", p)
		}
	})

	t.Run("records present", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "records"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "records", "replay.jsonl"), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write records: %v", err)
		}
		r := sampleReportV2WithBlocks()
		r.Replay.Records = RetainedRecords("records/replay.jsonl", 120)

		path, err := r.WriteJSON(dir)
		if err != nil {
			t.Fatalf("WriteJSON: %v", err)
		}
		raw, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			t.Fatalf("read report: %v", err)
		}
		var doc map[string]interface{}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("unmarshal report: %v", err)
		}
		replay, _ := doc["replay"].(map[string]interface{})
		records, _ := replay["records"].(map[string]interface{})
		if got, _ := records["retention"].(string); got != RecordsRetained {
			t.Errorf("retention = %q, want %q — a present artifact must stay referenced", got, RecordsRetained)
		}
		if p, _ := records["run_local_path"].(string); p != "records/replay.jsonl" {
			t.Errorf("run_local_path = %q, want records/replay.jsonl", p)
		}
	})
}

// TestReportV2_ValidateAdditiveBlocksCoversUS3Structures keeps the new
// structures inside the existing emission-time guard. They are OPTIONAL — a
// report that sets none of them validates exactly as before, which is what
// keeps this change additive — but a malformed one is an error rather than a
// silently-valid document.
func TestReportV2_ValidateAdditiveBlocksCoversUS3Structures(t *testing.T) {
	if err := sampleReportV2WithBlocks().ValidateAdditiveBlocks(); err != nil {
		t.Fatalf("sample with the US3 fields rejected: %v", err)
	}

	t.Run("bad inputs", func(t *testing.T) {
		r := sampleReportV2WithBlocks()
		r.Replay.Inputs = &InputProvenance{Availability: InputAvailabilityPrivateRecording, IndependentlyReproducible: true}
		if err := r.ValidateAdditiveBlocks(); err == nil {
			t.Error("a mismarked input provenance must be rejected at emission time")
		}
	})
	t.Run("bad records", func(t *testing.T) {
		r := sampleReportV2WithBlocks()
		r.AgentLoop.Records = &RawRecordsRef{Retention: RecordsRetained, RunLocalPath: "/tmp/leak.jsonl", Note: RecordsNotDurableNote}
		if err := r.ValidateAdditiveBlocks(); err == nil {
			t.Error("an absolute records path must be rejected at emission time")
		}
	})
	t.Run("bad run status", func(t *testing.T) {
		r := sampleReportV2WithBlocks()
		r.RunStatus = &RunStatus{Completeness: "mostly", CellsPlanned: 1, CellsMeasured: 1}
		if err := r.ValidateAdditiveBlocks(); err == nil {
			t.Error("a completeness outside the closed enum must be rejected")
		}
	})
	t.Run("still additive", func(t *testing.T) {
		data, err := json.Marshal(sampleReportV2())
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var doc map[string]interface{}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, ok := doc["run_status"]; ok {
			t.Error("run_status appeared in a report that never set it — must be omitempty")
		}
	})
}

// containsSubstring reports whether any entry of list contains want. The
// publication decision is prose meant for a human about to publish, so the
// assertions match on the load-bearing phrase rather than on an exact string
// nobody should be pinned to.
func containsSubstring(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

// fptr makes an optional report figure. Nil means NOT MEASURED, which is a
// different claim from a measured zero — see the field docs on AgentLoopCell.
func fptr(f float64) *float64 { return &f }
