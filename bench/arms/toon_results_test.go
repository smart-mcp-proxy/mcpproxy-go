package arms

// toon_results_test.go — T038: the fixture-driven toon_results arm rows
// (FR-007, research D10). The arm measures TOON on tool-call RESULT payloads
// (datasets/result_fixtures_v1.json) against a compact-JSON baseline of the
// SAME payloads, split tabular vs non-tabular — TOON's favorable regime is
// tabular results, and honest negative results are publishable either way.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

// resultFixturesPath is the committed T037 fixture set, seen from bench/arms.
const resultFixturesPath = "../../specs/083-discovery-profiler/datasets/result_fixtures_v1.json"

func newArmsTestTokenizer(t *testing.T) *bench.Tokenizer {
	t.Helper()
	tk, err := bench.NewTokenizer(bench.DefaultEncoding)
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	return tk
}

func loadRealFixtures(t *testing.T) *ResultFixtureSet {
	t.Helper()
	fx, err := LoadResultFixtures(resultFixturesPath)
	if err != nil {
		t.Fatalf("LoadResultFixtures(%s): %v", resultFixturesPath, err)
	}
	return fx
}

func TestLoadResultFixtures_Real(t *testing.T) {
	fx := loadRealFixtures(t)

	if fx.FixtureID != "result_fixtures_v1" {
		t.Errorf("FixtureID = %q, want result_fixtures_v1", fx.FixtureID)
	}
	if fx.Captured == "" {
		t.Error("Captured empty — the snapshot date is part of the fixture identity")
	}
	if len(fx.Results) != 6 {
		t.Fatalf("got %d results, want 6 (datasets/README.md)", len(fx.Results))
	}

	tabular, nonTabular := 0, 0
	seen := make(map[string]bool, len(fx.Results))
	for _, r := range fx.Results {
		if r.ToolID == "" {
			t.Error("result with empty tool_id")
		}
		if seen[r.ToolID] {
			t.Errorf("duplicate tool_id %q", r.ToolID)
		}
		seen[r.ToolID] = true
		switch r.PayloadClassHint {
		case PayloadClassTabular:
			tabular++
		case PayloadClassNonTabular:
			nonTabular++
		default:
			t.Errorf("tool %s: unknown payload_class_hint %q", r.ToolID, r.PayloadClassHint)
		}
		if !json.Valid(r.Payload) {
			t.Errorf("tool %s: payload is not valid JSON", r.ToolID)
		}
	}
	// The committed split (datasets/README.md): 2 tabular, 4 non-tabular.
	if tabular != 2 || nonTabular != 4 {
		t.Errorf("split = %d tabular / %d non-tabular, want 2/4", tabular, nonTabular)
	}
}

func TestLoadResultFixtures_Validation(t *testing.T) {
	write := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "fixtures.json")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		return path
	}

	cases := []struct {
		name    string
		content string
	}{
		{"missing file", ""}, // path never written
		{"invalid json", `{`},
		{"missing fixture_id", `{"captured":"2026-07-14","results":[{"tool_id":"a:b","payload_class_hint":"tabular","payload":[]}]}`},
		{"missing captured", `{"fixture_id":"f","results":[{"tool_id":"a:b","payload_class_hint":"tabular","payload":[]}]}`},
		{"no results", `{"fixture_id":"f","captured":"2026-07-14","results":[]}`},
		{"empty tool_id", `{"fixture_id":"f","captured":"2026-07-14","results":[{"tool_id":"","payload_class_hint":"tabular","payload":[]}]}`},
		{"duplicate tool_id", `{"fixture_id":"f","captured":"2026-07-14","results":[{"tool_id":"a:b","payload_class_hint":"tabular","payload":[]},{"tool_id":"a:b","payload_class_hint":"tabular","payload":[]}]}`},
		{"bad hint", `{"fixture_id":"f","captured":"2026-07-14","results":[{"tool_id":"a:b","payload_class_hint":"wide","payload":[]}]}`},
		{"missing payload", `{"fixture_id":"f","captured":"2026-07-14","results":[{"tool_id":"a:b","payload_class_hint":"tabular"}]}`},
		{"tabular hint on non-array payload", `{"fixture_id":"f","captured":"2026-07-14","results":[{"tool_id":"a:b","payload_class_hint":"tabular","payload":{"k":1}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing.json")
			if tc.content != "" {
				path = write(t, tc.content)
			}
			if _, err := LoadResultFixtures(path); err == nil {
				t.Errorf("LoadResultFixtures must fail for %s", tc.name)
			}
		})
	}
}

func TestRunToonResults_Rows(t *testing.T) {
	tk := newArmsTestTokenizer(t)
	fx := loadRealFixtures(t)

	run, err := RunToonResults(tk, fx)
	if err != nil {
		t.Fatalf("RunToonResults: %v", err)
	}
	if len(run.Rows) != 2 {
		t.Fatalf("got %d rows, want 2 (compact-JSON baseline + toon_results)", len(run.Rows))
	}
	base, toon := run.Rows[0], run.Rows[1]

	// Both rows are results-class over the same fixture set (schema
	// conditional: payload_class=results requires the fixture fields).
	wantFixtureID := fx.FixtureID + "@" + fx.Captured
	for i, row := range run.Rows {
		if row.PayloadClass != "results" {
			t.Errorf("rows[%d].PayloadClass = %q, want results", i, row.PayloadClass)
		}
		if row.CorpusID != fx.FixtureID {
			t.Errorf("rows[%d].CorpusID = %q, want %q", i, row.CorpusID, fx.FixtureID)
		}
		if row.FixtureID != wantFixtureID {
			t.Errorf("rows[%d].FixtureID = %q, want %q", i, row.FixtureID, wantFixtureID)
		}
		if row.TabularCount == nil || *row.TabularCount != 2 {
			t.Errorf("rows[%d].TabularCount = %v, want 2", i, row.TabularCount)
		}
		if row.NonTabularCount == nil || *row.NonTabularCount != 4 {
			t.Errorf("rows[%d].NonTabularCount = %v, want 4", i, row.NonTabularCount)
		}
		if row.Skipped {
			t.Errorf("rows[%d] skipped, want measured", i)
		}
		if row.IndexAltering {
			t.Errorf("rows[%d].IndexAltering = true — result payloads never touch the retrieval index", i)
		}
		if row.TotalTokens <= 0 || row.MeanTokens <= 0 || row.P95Tokens <= 0 {
			t.Errorf("rows[%d] token stats not populated: %+v", i, row)
		}
		if len(row.HeaviestTools) == 0 {
			t.Errorf("rows[%d].HeaviestTools empty", i)
		}
		for j := 1; j < len(row.HeaviestTools); j++ {
			if row.HeaviestTools[j].Tokens > row.HeaviestTools[j-1].Tokens {
				t.Errorf("rows[%d].HeaviestTools not sorted descending at %d", i, j)
			}
		}
	}

	if base.Arm != BaselineName {
		t.Errorf("baseline row arm = %q, want %q", base.Arm, BaselineName)
	}
	if base.SavingsVsBaselinePct != 0 {
		t.Errorf("baseline row savings = %v, want 0", base.SavingsVsBaselinePct)
	}
	if toon.Arm != ToonResultsName {
		t.Errorf("toon row arm = %q, want %q", toon.Arm, ToonResultsName)
	}

	// Savings recomputable from the two rows alone (FR-004 spirit).
	wantSavings := (1 - float64(toon.TotalTokens)/float64(base.TotalTokens)) * 100
	if diff := toon.SavingsVsBaselinePct - wantSavings; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("toon savings = %v, want %v (from row totals)", toon.SavingsVsBaselinePct, wantSavings)
	}

	// The tabular/non-tabular split partitions each side's total exactly.
	if got := run.TabularToonTokens + run.NonTabularToonTokens; got != toon.TotalTokens {
		t.Errorf("toon split sums to %d, want total %d", got, toon.TotalTokens)
	}
	if got := run.TabularBaselineTokens + run.NonTabularBaselineTokens; got != base.TotalTokens {
		t.Errorf("baseline split sums to %d, want total %d", got, base.TotalTokens)
	}
	t.Logf("toon_results: overall %.1f%%, tabular %.1f%%, non-tabular %.1f%% savings vs compact JSON",
		toon.SavingsVsBaselinePct, run.TabularSavingsPct(), run.NonTabularSavingsPct())
}

func TestRunToonResults_Deterministic(t *testing.T) {
	tk := newArmsTestTokenizer(t)
	fx := loadRealFixtures(t)

	a, err := RunToonResults(tk, fx)
	if err != nil {
		t.Fatalf("RunToonResults: %v", err)
	}
	b, err := RunToonResults(tk, fx)
	if err != nil {
		t.Fatalf("RunToonResults (2nd run): %v", err)
	}
	aj, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	bj, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(aj) != string(bj) {
		t.Error("RunToonResults not deterministic across runs (FR-010)")
	}
}

// TestToonResults_NotRegistered documents that toon_results is deliberately
// NOT a registry arm: it encodes tool-call RESULT payloads from a fixture
// file, not tool definitions from a corpus, so the tool-corpus contract tests
// (EncodeTool/EncodeListing/EncodeIndexMetadata over corpus_v2) do not apply.
func TestToonResults_NotRegistered(t *testing.T) {
	if _, err := Resolve(ToonResultsName); err == nil {
		t.Error("toon_results must not be a registry arm (fixture-driven, results payload class)")
	}
}
