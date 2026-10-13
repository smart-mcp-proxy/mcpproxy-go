package bench_test

// armindex_test.go — T023: the arm index builder must funnel through the
// production index code (internal/index.Manager.BatchIndexTools →
// BleveIndex.SearchTools) and reproduce the recorded golden-set baseline.
//
// This file is an EXTERNAL test package (bench_test) on purpose: it exercises
// the real bench/arms baseline arm as the metadata source, and bench/arms
// imports bench, so an internal test would be an import cycle.

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/bench/arms"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

const (
	armindexCorpusV2Path = "../specs/083-discovery-profiler/datasets/corpus_v2.tools.json"
	armindexGoldenPath   = "../specs/065-evaluation-foundation/datasets/retrieval_golden_v1.json"

	// SC-003: the baseline arm through the arm-aware pipeline must reproduce
	// the recorded golden-set baseline recall@5 = 0.68 within ±0.05.
	sc003RecallAt5 = 0.68
	sc003Tolerance = 0.05
)

// buildBaselineIndex builds an ArmIndex over the given corpus using the
// baseline arm's EncodeIndexMetadata — the production mapping unchanged.
func buildBaselineIndex(t *testing.T, corpus *bench.Corpus) *bench.ArmIndex {
	t.Helper()
	baseline := arms.NewBaseline()
	metas := make([]*config.ToolMetadata, 0, len(corpus.Tools))
	for _, tl := range corpus.Tools {
		meta, err := baseline.EncodeIndexMetadata(tl)
		if err != nil {
			t.Fatalf("baseline EncodeIndexMetadata(%s): %v", tl.ToolID, err)
		}
		metas = append(metas, &meta)
	}
	ai, err := bench.BuildArmIndex(t.TempDir(), metas)
	if err != nil {
		t.Fatalf("BuildArmIndex: %v", err)
	}
	t.Cleanup(func() {
		if err := ai.Close(); err != nil {
			t.Errorf("ArmIndex.Close: %v", err)
		}
	})
	return ai
}

// TestArmIndex_SearchFuncBasics verifies the SearchFunc contract on a tiny
// synthetic corpus: ranked IDs are server:tool, the obviously-relevant tool
// ranks first, and identical queries return identical rankings (FR-010).
func TestArmIndex_SearchFuncBasics(t *testing.T) {
	metas := []*config.ToolMetadata{
		{Name: "read_file", ServerName: "fs", Description: "Read the contents of a file from disk", ParamsJSON: `{"properties":{"path":{"type":"string"}},"required":["path"],"type":"object"}`},
		{Name: "git_log", ServerName: "git", Description: "Show recent commit history of a repository", ParamsJSON: `{"properties":{"max_count":{"type":"integer"}},"type":"object"}`},
		{Name: "get_current_time", ServerName: "time", Description: "Get current time in a specific timezone", ParamsJSON: `{"properties":{"timezone":{"type":"string"}},"required":["timezone"],"type":"object"}`},
	}
	ai, err := bench.BuildArmIndex(t.TempDir(), metas)
	if err != nil {
		t.Fatalf("BuildArmIndex: %v", err)
	}
	defer func() {
		if cerr := ai.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	}()

	search := ai.SearchFunc()
	ranked, err := search("commit history", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ranked) == 0 {
		t.Fatal("search returned no results for a matching query")
	}
	if ranked[0] != "git:git_log" {
		t.Errorf("top result = %q, want git:git_log (ranked IDs must be server:tool)", ranked[0])
	}

	again, err := search("commit history", 10)
	if err != nil {
		t.Fatalf("search (2nd run): %v", err)
	}
	if len(again) != len(ranked) {
		t.Fatalf("ranking length not deterministic: %d vs %d", len(ranked), len(again))
	}
	for i := range ranked {
		if ranked[i] != again[i] {
			t.Errorf("ranking not deterministic at %d: %q vs %q", i, ranked[i], again[i])
		}
	}
}

// TestArmIndex_LimitRespected verifies the limit parameter caps the ranking.
func TestArmIndex_LimitRespected(t *testing.T) {
	metas := []*config.ToolMetadata{
		{Name: "list_files", ServerName: "fs", Description: "List files in a directory"},
		{Name: "read_files", ServerName: "fs", Description: "Read many files at once"},
		{Name: "search_files", ServerName: "fs", Description: "Search for files by pattern"},
	}
	ai, err := bench.BuildArmIndex(t.TempDir(), metas)
	if err != nil {
		t.Fatalf("BuildArmIndex: %v", err)
	}
	defer func() { _ = ai.Close() }()

	ranked, err := ai.SearchFunc()("files", 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ranked) > 2 {
		t.Errorf("limit 2 returned %d results", len(ranked))
	}
}

// TestArmIndex_GoldenSetCoversCorpusV2 documents the id-compatibility check
// research D7 asks for: the golden set is bound to corpus_v1 tool IDs, and
// every labeled ID must exist in corpus_v2 (both corpora snapshot the same 7
// reference servers, verified here) — no mapping layer is needed.
func TestArmIndex_GoldenSetCoversCorpusV2(t *testing.T) {
	corpus, err := bench.LoadCorpusV2(armindexCorpusV2Path)
	if err != nil {
		t.Fatalf("LoadCorpusV2: %v", err)
	}
	golden, err := bench.LoadGoldenSet(armindexGoldenPath)
	if err != nil {
		t.Fatalf("LoadGoldenSet: %v", err)
	}

	ids := make(map[string]bool, len(corpus.Tools))
	for _, tl := range corpus.Tools {
		ids[tl.ToolID] = true
	}
	for _, q := range golden.Queries {
		for _, l := range q.Labels {
			if !ids[l.ToolID] {
				t.Errorf("golden query %s labels tool %q which is absent from corpus_v2 — an ID mapping would be required", q.ID, l.ToolID)
			}
		}
	}
}

// TestArmIndex_BaselineGoldenParity is the SC-003 gate (research D7): the
// baseline arm indexed through the production funnel must reproduce the
// recorded golden-set baseline recall@5 = 0.68 ± 0.05 before any other arm's
// retrieval score is trusted.
func TestArmIndex_BaselineGoldenParity(t *testing.T) {
	corpus, err := bench.LoadCorpusV2(armindexCorpusV2Path)
	if err != nil {
		t.Fatalf("LoadCorpusV2: %v", err)
	}
	golden, err := bench.LoadGoldenSet(armindexGoldenPath)
	if err != nil {
		t.Fatalf("LoadGoldenSet: %v", err)
	}

	ai := buildBaselineIndex(t, corpus)
	metrics, err := bench.ScoreRetrieval(golden, ai.SearchFunc(), []int{1, 3, 5, 10})
	if err != nil {
		t.Fatalf("ScoreRetrieval: %v", err)
	}

	r5 := metrics.Metrics.RecallAt[5]
	t.Logf("baseline arm on retrieval_golden_v1: recall@5=%.4f mrr=%.4f ndcg@10=%.4f map=%.4f (%d queries)",
		r5, metrics.Metrics.MRR, metrics.Metrics.NDCGAt10, metrics.Metrics.MAP, metrics.QueryCount)
	if r5 < sc003RecallAt5-sc003Tolerance || r5 > sc003RecallAt5+sc003Tolerance {
		t.Errorf("SC-003 parity gate FAILED: baseline arm recall@5 = %.4f, want %.2f ± %.2f — the arm-aware pipeline changed what it measures", r5, sc003RecallAt5, sc003Tolerance)
	}
}
