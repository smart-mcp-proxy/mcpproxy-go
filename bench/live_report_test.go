package bench

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// goldenPath locates the committed Spec 065 golden set relative to the repo
// root (tests run from the bench/ package dir).
func goldenPath() string {
	return filepath.Join("..", "specs", "065-evaluation-foundation", "datasets", "retrieval_golden_v1.json")
}

func TestLoadGoldenSetReal(t *testing.T) {
	g, err := LoadGoldenSet(goldenPath())
	if err != nil {
		t.Fatalf("LoadGoldenSet: %v", err)
	}
	if g.CorpusVersion == "" {
		t.Error("corpus_version empty")
	}
	if len(g.Queries) < 10 {
		t.Errorf("expected a substantial golden set, got %d queries", len(g.Queries))
	}
	for _, q := range g.Queries {
		if q.ID == "" || q.Query == "" {
			t.Errorf("query missing id/text: %+v", q)
		}
		if relevantCount(q.Labels) == 0 {
			t.Errorf("query %q has no relevant labels", q.ID)
		}
	}
}

func TestPercentiles(t *testing.T) {
	ds := []time.Duration{
		10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond,
		40 * time.Millisecond, 50 * time.Millisecond, 60 * time.Millisecond,
		70 * time.Millisecond, 80 * time.Millisecond, 90 * time.Millisecond,
		100 * time.Millisecond,
	}
	lat := computeLatency(ds, 5*time.Millisecond)
	if lat.Samples != 10 {
		t.Errorf("Samples = %d, want 10", lat.Samples)
	}
	// nearest-rank: p50 -> ceil(0.5*10)=5th value (50ms); p95 -> 10th (100ms)
	if lat.P50ms != 50 {
		t.Errorf("P50ms = %v, want 50", lat.P50ms)
	}
	if lat.P95ms != 100 {
		t.Errorf("P95ms = %v, want 100", lat.P95ms)
	}
	if lat.MaxMs != 100 {
		t.Errorf("MaxMs = %v, want 100", lat.MaxMs)
	}
	if lat.LoadAllToolsMs != 5 {
		t.Errorf("LoadAllToolsMs = %v, want 5", lat.LoadAllToolsMs)
	}
}

func TestRunLiveAuthoritativeHeadline(t *testing.T) {
	srv := stubProxy(t)
	defer srv.Close()

	c := NewLiveClient(srv.URL, "test-key")
	golden := &GoldenSet{
		CorpusVersion: "corpus_v1",
		Queries: []GoldenQuery{
			{ID: "q1", Query: "read a file", Labels: []Label{{ToolID: "filesystem:read_text_file", Relevance: 2}}},
		},
	}
	rep, err := RunLive(context.Background(), c, golden)
	if err != nil {
		t.Fatalf("RunLive: %v", err)
	}
	// Token report: baseline counted with schemas AND proxy tools carry their
	// live schemas (from server.ProxyModeToolDefs), so the headline is
	// authoritative — schemas on BOTH sides, no MCP-3161 overstatement.
	if rep.Tokens == nil || rep.Tokens.UpstreamTools != 2 {
		t.Fatalf("expected 2 upstream tools, got %+v", rep.Tokens)
	}
	if !rep.Tokens.ProxySchemasCounted {
		t.Error("proxy tools should carry schemas from the live builders")
	}
	if !rep.Tokens.AuthoritativeHeadline {
		t.Error("headline should be authoritative when both sides count schemas")
	}
	if rep.Tokens.BaselineTokens <= 0 {
		t.Error("baseline tokens should be counted with schemas")
	}
	// A savings ratio must be present for the proxy modes.
	for _, m := range rep.Tokens.Modes {
		if m.Mode != ModeBaseline && m.SavingsRatio == 0 {
			t.Errorf("expected a savings ratio for mode %q", m.Mode)
		}
	}
	// Accuracy: perfect ranking for the one query.
	if rep.Retrieval == nil || rep.Retrieval.RecallAt[1] != 1.0 {
		t.Errorf("expected Recall@1=1.0, got %+v", rep.Retrieval)
	}
	// Latency populated.
	if rep.Latency == nil || rep.Latency.Samples != 1 {
		t.Errorf("expected 1 latency sample, got %+v", rep.Latency)
	}
}

// TestBuildTokenReportWithholdsWhenProxySchemasMissing guards the MCP-3161
// safety valve: if any proxy tool lacks a schema, counting the baseline's
// schemas alone would overstate savings, so the headline is withheld.
func TestBuildTokenReportWithholdsWhenProxySchemasMissing(t *testing.T) {
	tk, err := NewTokenizer(DefaultEncoding)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	upstream := []Tool{{Name: "big", Description: "d", Schema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)}}
	rtSchemaless := []Tool{{Name: "retrieve_tools", Description: "d"}} // no schema
	ce := []Tool{{Name: "code_execution", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}}

	rep := buildTokenReport(tk, upstream, rtSchemaless, ce)
	if rep.AuthoritativeHeadline {
		t.Error("headline must be withheld when a proxy tool lacks a schema")
	}
	for _, m := range rep.Modes {
		if m.SavingsRatio != 0 {
			t.Errorf("savings ratio must be withheld (0), got %v for %q", m.SavingsRatio, m.Mode)
		}
	}
	if rep.BaselineTokens <= 0 {
		t.Error("baseline tokens should still be reported for transparency")
	}
}
