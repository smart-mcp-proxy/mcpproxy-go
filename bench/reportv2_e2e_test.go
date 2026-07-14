package bench_test

// reportv2_e2e_test.go — T036: the end-to-end report validator. A REAL
// offline arm run over the committed corpus_v2 (every registry arm whose
// runtime is available, plus the fixture-driven toon_results rows) is
// assembled into a ReportV2 and validated against the actual contract file
// (contracts/report-v2.schema.json) with python3+jsonschema when available;
// the structural Go checks below are the always-on gate (adding a Go
// jsonschema dependency is not allowed by the plan).
//
// NOTE (deviation from the tasks.md file listing): this test lives in its own
// file as an EXTERNAL test package (bench_test), not inside reportv2_test.go
// (package bench), because a REAL arm run needs bench/arms — which imports
// bench — and an internal test would be an import cycle. Same precedent as
// armindex_test.go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/bench/arms"
)

const (
	e2eCorpusV2Path       = "../specs/083-discovery-profiler/datasets/corpus_v2.tools.json"
	e2eGoldenPath         = "../specs/065-evaluation-foundation/datasets/retrieval_golden_v1.json"
	e2eResultFixturesPath = "../specs/083-discovery-profiler/datasets/result_fixtures_v1.json"
	e2eSchemaPath         = "../specs/083-discovery-profiler/contracts/report-v2.schema.json"
)

// buildRealOfflineReport runs the actual offline profiler path in-test: load
// corpus_v2 + the in-house golden set, resolve every registered arm (runtime
// absences become skip rows, contract rule 5), run them through the same
// assembly the CLI uses, and append the toon_results fixture rows.
func buildRealOfflineReport(t *testing.T) *bench.ReportV2 {
	t.Helper()

	tk, err := bench.NewTokenizer(bench.DefaultEncoding)
	if err != nil {
		t.Fatalf("NewTokenizer: %v", err)
	}
	corpus, err := bench.LoadCorpusV2(e2eCorpusV2Path)
	if err != nil {
		t.Fatalf("LoadCorpusV2: %v", err)
	}
	golden, err := bench.LoadGoldenSet(e2eGoldenPath)
	if err != nil {
		t.Fatalf("LoadGoldenSet: %v", err)
	}

	corpusID := corpus.Version
	var runArms []bench.EncodingArm
	var skipped []bench.ArmResult
	for _, name := range arms.Names() {
		arm, err := arms.Resolve(name)
		if err != nil {
			if errors.Is(err, arms.ErrArmUnavailable) {
				t.Logf("arm %q unavailable locally, recording skip row: %v", name, err)
				skipped = append(skipped, bench.SkippedArmResult(name, corpusID, err.Error()))
				continue
			}
			t.Fatalf("Resolve(%s): %v", name, err)
		}
		runArms = append(runArms, arm)
	}

	fixtures, err := arms.LoadResultFixtures(e2eResultFixturesPath)
	if err != nil {
		t.Fatalf("LoadResultFixtures: %v", err)
	}
	toonResults, err := arms.RunToonResults(tk, fixtures)
	if err != nil {
		t.Fatalf("RunToonResults: %v", err)
	}

	report, err := bench.BuildOfflineReportV2(tk, "2026-07-14T00:00:00Z", []bench.OfflineSection{
		{
			Corpus: corpus,
			Descriptor: bench.CorpusDescriptor{
				ID: corpusID, Name: "corpus_v2", Version: corpus.Version,
				License: "own capture of public no-auth reference-server metadata (repo-licensed)", Committed: true,
			},
			Golden:      golden,
			Arms:        runArms,
			SkippedArms: skipped,
		},
		{
			Descriptor: bench.CorpusDescriptor{
				ID: fixtures.FixtureID, Name: "result_fixtures_v1 (tool-call outputs)",
				Version:   fixtures.FixtureID + "@" + fixtures.Captured,
				ToolCount: len(fixtures.Results),
				License:   "own capture of reference-server outputs (repo-licensed)", Committed: true,
			},
			ExtraArmRows: toonResults.Rows,
		},
	})
	if err != nil {
		t.Fatalf("BuildOfflineReportV2: %v", err)
	}
	return report
}

// TestReportV2_EndToEndOfflineRun validates the full real-run report against
// the contract: python3+jsonschema when available (the schema file itself is
// the oracle, conditionals included), and structural Go checks always.
func TestReportV2_EndToEndOfflineRun(t *testing.T) {
	if testing.Short() {
		t.Skip("real offline arm run — skipped in -short mode")
	}
	report := buildRealOfflineReport(t)
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}

	validateStructurally(t, report)
	validateWithJSONSchema(t, data)
}

// validateStructurally enforces the contract's conditional rules in Go — the
// always-on gate mirroring the schema's allOf conditions plus the Go-side
// rule the schema cannot express (nullable quality needs an explaining note).
func validateStructurally(t *testing.T, r *bench.ReportV2) {
	t.Helper()

	if r.ReportVersion != 2 {
		t.Errorf("report_version = %d, want 2", r.ReportVersion)
	}
	if r.GeneratedAt == "" || r.Tokenizer.Name == "" || len(r.Tokenizer.Caveat) < 10 {
		t.Error("envelope identity fields incomplete")
	}
	if len(r.Corpora) < 2 {
		t.Fatalf("want corpus_v2 + result_fixtures corpora rows, got %d", len(r.Corpora))
	}
	for _, cd := range r.Corpora {
		if cd.ID == "" || cd.Name == "" || cd.Version == "" || cd.License == "" {
			t.Errorf("corpus descriptor missing required fields: %+v", cd)
		}
	}

	// The mandatory arm set must be present as rows — measured or, only when
	// the runtime is locally absent, skipped-with-reason (SC-002 is enforced
	// in CI where every runtime exists).
	rows := make(map[string]bench.ArmResult, len(r.Arms))
	for _, row := range r.Arms {
		if row.Arm == "" || row.CorpusID == "" {
			t.Errorf("arm row missing identity: %+v", row)
		}
		rows[row.Arm+"@"+row.CorpusID] = row

		if row.Skipped {
			if row.SkipReason == "" {
				t.Errorf("arm %s skipped without skip_reason", row.Arm)
			}
			continue
		}
		if row.TotalTokens <= 0 || row.MeanTokens <= 0 || row.P95Tokens <= 0 {
			t.Errorf("non-skipped arm %s (%s) missing token stats: %+v", row.Arm, row.CorpusID, row)
		}
		if row.PayloadClass == "results" {
			if row.FixtureID == "" || row.TabularCount == nil || row.NonTabularCount == nil {
				t.Errorf("results row %s missing fixture fields: %+v", row.Arm, row)
			}
		}
		if row.IndexAltering {
			if row.Quality == nil {
				t.Errorf("index-altering arm %s has nil quality — nullable only with an explaining metric_note", row.Arm)
			} else if row.Quality.RecallAt5 == 0 && row.Quality.MetricNote == "" {
				t.Errorf("index-altering arm %s: empty quality without a metric_note explaining the absence", row.Arm)
			}
		}
	}
	corpusV2ID := r.Corpora[0].ID
	for _, mandatory := range []string{"baseline_json", "compact_sig", "tscg", "toon_listing"} {
		if _, ok := rows[mandatory+"@"+corpusV2ID]; !ok {
			t.Errorf("mandatory arm %q has no row for corpus %q", mandatory, corpusV2ID)
		}
	}
	if _, ok := rows["toon_results@result_fixtures_v1"]; !ok {
		t.Error("toon_results fixture row missing")
	}

	// Baseline savings is the zero point; other measured listing arms carry a
	// computed savings percentage (negative allowed).
	if base, ok := rows["baseline_json@"+corpusV2ID]; ok && base.SavingsVsBaselinePct != 0 {
		t.Errorf("baseline savings = %v, want 0", base.SavingsVsBaselinePct)
	}

	// SC-003 sanity through the E2E path: the baseline arm's quality is
	// attached only to index-altering arms — baseline itself is rendering-
	// only, so it must be quality-nil.
	if base, ok := rows["baseline_json@"+corpusV2ID]; ok && base.Quality != nil {
		t.Errorf("baseline_json is not index-altering; quality must be nil, got %+v", base.Quality)
	}

	for key, v := range r.Provenance {
		if v != "measured" && v != "computed" && v != "estimated" {
			t.Errorf("provenance[%q] = %q not in the closed enum", key, v)
		}
	}
}

// validateWithJSONSchema shells out to python3+jsonschema (Draft 2020-12,
// conditionals included). Skipped when the interpreter or module is absent —
// the structural checks above still ran.
func validateWithJSONSchema(t *testing.T, reportJSON []byte) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available — structural validation only")
	}
	if err := exec.Command(python, "-c", "import jsonschema").Run(); err != nil {
		t.Skip("python3 jsonschema module not available — structural validation only")
	}

	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.json")
	if err := os.WriteFile(reportPath, reportJSON, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}

	script := fmt.Sprintf(`
import json, jsonschema
schema = json.load(open(%q))
report = json.load(open(%q))
jsonschema.Draft202012Validator(schema).validate(report)
print("VALID")
`, e2eSchemaPath, reportPath)
	out, err := exec.Command(python, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("jsonschema validation of the real offline run failed: %v\n%s", err, out)
	}
}
