package bench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
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
	if rep.Retrieval == nil || rep.Retrieval.Metrics.RecallAt[1] != 1.0 {
		t.Errorf("expected Recall@1=1.0, got %+v", rep.Retrieval)
	}
	// Latency populated.
	if rep.Latency == nil || rep.Latency.Samples != 1 {
		t.Errorf("expected 1 latency sample, got %+v", rep.Latency)
	}
	// The stub has no /mcp endpoint: response-cost measurement must degrade
	// to an explicit skip note (T016 additive wiring — the REST-based
	// measurements above stay intact), never to a hard failure.
	if rep.ResponseCost != nil {
		t.Errorf("ResponseCost = %+v, want nil when the proxy has no reachable /mcp", rep.ResponseCost)
	}
	if rep.BreakEven != nil {
		t.Errorf("BreakEven = %+v, want nil without measured responses", rep.BreakEven)
	}
	if rep.ResponseCostNote == "" {
		t.Error("ResponseCostNote empty — a skipped response-cost measurement needs an explicit reason")
	}
}

// stubProxyV2WithREST is stubProxy plus the surfaces the T016 wiring
// consumes: a real streamable-http MCP endpoint at /mcp serving
// retrieve_tools with a fixture response, and the /api/v1/status,
// /api/v1/info, /api/v1/config endpoints carrying routing_mode, version, and
// tools_limit (FR-021). The two stubProxy REST endpoints (/api/v1/tools,
// /api/v1/index/search) are reused by delegating to its mux-backed handler.
func stubProxyV2WithREST(t *testing.T, respText string) *httptest.Server {
	t.Helper()

	mcpSrv := mcpserver.NewMCPServer("bench-live-fixture", "1.0.0")
	mcpSrv.AddTool(
		mcp.NewTool("retrieve_tools", mcp.WithString("query", mcp.Required())),
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(respText), nil
		},
	)
	streamable := mcpserver.NewStreamableHTTPServer(mcpSrv)

	rest := stubProxy(t)
	t.Cleanup(rest.Close)
	restHandler := rest.Config.Handler

	mux := http.NewServeMux()
	mux.Handle("/mcp", streamable)
	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    map[string]any{"routing_mode": "retrieve_tools", "running": true},
		})
	})
	mux.HandleFunc("/api/v1/info", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    map[string]any{"version": "v0.99.0-test"},
		})
	})
	mux.HandleFunc("/api/v1/config", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    map[string]any{"config": map[string]any{"tools_limit": 15}},
		})
	})
	mux.Handle("/", restHandler)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestRunLive_ResponseCostWired is the T016 hermetic gate: RunLive against a
// proxy exposing a real MCP /mcp endpoint must produce per-golden-query
// DiscoveryResponseMeasurement rows (components summing to totals, FR-002;
// client latency, FR-023), the ResponseCostSummary, the BreakEvenAnalysis with
// its inputs echoed (FR-003/004), the FR-021 proxy identity fields, and the
// session-estimate rows for the measured baseline encoding.
func TestRunLive_ResponseCostWired(t *testing.T) {
	fixture := retrieveToolsResponseFixture(t, 3)
	srv := stubProxyV2WithREST(t, fixture)

	c := NewLiveClient(srv.URL, "test-key")
	golden := &GoldenSet{
		CorpusVersion: "corpus_v1",
		Queries: []GoldenQuery{
			{ID: "q1", Query: "read a file", Labels: []Label{{ToolID: "filesystem:read_text_file", Relevance: 2}}},
			{ID: "q2", Query: "echo input", Labels: []Label{{ToolID: "memory:echo", Relevance: 2}}},
		},
	}
	rep, err := RunLive(context.Background(), c, golden)
	if err != nil {
		t.Fatalf("RunLive: %v", err)
	}

	// Existing REST-based measurements stay intact (additive wiring).
	if rep.Tokens == nil || rep.Retrieval == nil || rep.Latency == nil {
		t.Fatalf("REST-based measurements missing: %+v", rep)
	}

	// Response cost: one row per golden query, in golden order.
	if rep.ResponseCostNote != "" {
		t.Fatalf("unexpected response-cost skip: %s", rep.ResponseCostNote)
	}
	if rep.ResponseCost == nil {
		t.Fatal("ResponseCost missing")
	}
	if got := len(rep.ResponseCost.PerQuery); got != len(golden.Queries) {
		t.Fatalf("PerQuery rows = %d, want %d", got, len(golden.Queries))
	}
	for i, m := range rep.ResponseCost.PerQuery {
		if m.QueryID != golden.Queries[i].ID {
			t.Errorf("PerQuery[%d].QueryID = %q, want %q (golden order, FR-010)", i, m.QueryID, golden.Queries[i].ID)
		}
		if m.TotalTokens <= 0 {
			t.Errorf("PerQuery[%d].TotalTokens = %d, want > 0", i, m.TotalTokens)
		}
		if m.ResultCount != 3 {
			t.Errorf("PerQuery[%d].ResultCount = %d, want 3", i, m.ResultCount)
		}
		if m.LatencyMs <= 0 {
			t.Errorf("PerQuery[%d].LatencyMs = %v, want > 0 (FR-023 client-side)", i, m.LatencyMs)
		}
		sum := 0
		for _, v := range m.Components {
			sum += v
		}
		if sum != m.TotalTokens {
			t.Errorf("PerQuery[%d]: sum(components) = %d, want %d (FR-002 invariant)", i, sum, m.TotalTokens)
		}
	}
	if rep.ResponseCost.P50 <= 0 || rep.ResponseCost.Max <= 0 || rep.ResponseCost.Mean <= 0 {
		t.Errorf("response-cost summary not populated: %+v", rep.ResponseCost)
	}

	// Break-even: inputs echoed from the SAME token report (FR-004), and with
	// the stub's tiny 2-tool baseline vs the real proxy menu the numerator is
	// negative — the honest NoBreakEven verdict.
	if rep.BreakEven == nil {
		t.Fatal("BreakEven missing")
	}
	if rep.BreakEven.NaiveFullMenuTokens != rep.Tokens.BaselineTokens {
		t.Errorf("NaiveFullMenuTokens = %d, want baseline %d", rep.BreakEven.NaiveFullMenuTokens, rep.Tokens.BaselineTokens)
	}
	proxyMenu := 0
	for _, m := range rep.Tokens.Modes {
		if m.Mode == ModeRetrieveTools {
			proxyMenu = m.Tokens
		}
	}
	if rep.BreakEven.ProxyMenuTokens != proxyMenu {
		t.Errorf("ProxyMenuTokens = %d, want retrieve_tools mode %d", rep.BreakEven.ProxyMenuTokens, proxyMenu)
	}
	if rep.BreakEven.MeanResponseTokens != rep.ResponseCost.Mean {
		t.Errorf("MeanResponseTokens = %v, want %v", rep.BreakEven.MeanResponseTokens, rep.ResponseCost.Mean)
	}
	if !rep.BreakEven.NoBreakEven {
		t.Errorf("expected NoBreakEven for a 2-tool baseline vs the full proxy menu, got %+v", rep.BreakEven)
	}

	// Proxy identity (FR-021).
	if rep.ProxyInfo == nil {
		t.Fatal("ProxyInfo missing")
	}
	if rep.ProxyInfo.Version != "v0.99.0-test" {
		t.Errorf("ProxyInfo.Version = %q, want v0.99.0-test", rep.ProxyInfo.Version)
	}
	if rep.ProxyInfo.RoutingMode != "retrieve_tools" {
		t.Errorf("ProxyInfo.RoutingMode = %q, want retrieve_tools", rep.ProxyInfo.RoutingMode)
	}
	if rep.ProxyInfo.ToolsLimit != 15 {
		t.Errorf("ProxyInfo.ToolsLimit = %d, want 15", rep.ProxyInfo.ToolsLimit)
	}
	if rep.ProxyInfo.ToolCount != 2 {
		t.Errorf("ProxyInfo.ToolCount = %d, want 2", rep.ProxyInfo.ToolCount)
	}

	// Session estimates: the measured live encoding is the proxy's own full
	// JSON (baseline_json), one row per default calls-per-session point.
	if got, want := len(rep.SessionEstimates), len(DefaultCallsPerSession()); got != want {
		t.Fatalf("SessionEstimates rows = %d, want %d", got, want)
	}
	for _, se := range rep.SessionEstimates {
		if se.Arm != "baseline_json" {
			t.Errorf("session estimate arm = %q, want baseline_json", se.Arm)
		}
		if se.EstimatedTokens <= 0 {
			t.Errorf("session estimate for %d calls not populated: %+v", se.CallsPerSession, se)
		}
	}
}

// TestLiveReport_ToReportV2 checks the ReportV2 projection of a live run:
// versioned envelope, tokenizer caveat, non-nil corpora/arms arrays (schema
// requires arrays, not null), the live sections carried over, and provenance
// labels on every headline number (SC-005).
func TestLiveReport_ToReportV2(t *testing.T) {
	fixture := retrieveToolsResponseFixture(t, 2)
	srv := stubProxyV2WithREST(t, fixture)

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

	r2 := rep.ToReportV2("2026-07-14T00:00:00Z")
	if r2.ReportVersion != ReportVersion2 {
		t.Errorf("ReportVersion = %d, want %d", r2.ReportVersion, ReportVersion2)
	}
	if r2.GeneratedAt != "2026-07-14T00:00:00Z" {
		t.Errorf("GeneratedAt = %q, caller-supplied value must pass through", r2.GeneratedAt)
	}
	if r2.Tokenizer.Name != DefaultEncoding || len(r2.Tokenizer.Caveat) < 10 {
		t.Errorf("tokenizer info incomplete: %+v", r2.Tokenizer)
	}
	if r2.Arms == nil {
		t.Error("Arms must be an empty array, not null (schema: required array)")
	}
	if len(r2.Corpora) != 1 {
		t.Fatalf("Corpora rows = %d, want 1 (the live proxy toolset)", len(r2.Corpora))
	}
	cd := r2.Corpora[0]
	if cd.ToolCount != 2 || cd.Committed {
		t.Errorf("live corpus descriptor wrong: %+v", cd)
	}
	if cd.ID == "" || cd.Name == "" || cd.Version == "" || cd.License == "" {
		t.Errorf("live corpus descriptor missing required fields: %+v", cd)
	}
	if r2.ResponseCost == nil || r2.BreakEven == nil || r2.Latency == nil || r2.Proxy == nil {
		t.Fatalf("live sections missing from ReportV2: %+v", r2)
	}
	if len(r2.SessionEstimates) == 0 {
		t.Error("SessionEstimates missing from ReportV2")
	}
	for _, key := range []string{"response_cost", "break_even", "session_estimates", "latency", "menu_tokens"} {
		if _, ok := r2.Provenance[key]; !ok {
			t.Errorf("provenance missing key %q", key)
		}
	}
	if r2.Provenance["response_cost"] != ProvenanceMeasured {
		t.Errorf("response_cost provenance = %q, want measured", r2.Provenance["response_cost"])
	}
	if r2.Provenance["break_even"] != ProvenanceComputed {
		t.Errorf("break_even provenance = %q, want computed", r2.Provenance["break_even"])
	}
	if r2.Provenance["session_estimates"] != ProvenanceEstimated {
		t.Errorf("session_estimates provenance = %q, want estimated", r2.Provenance["session_estimates"])
	}

	// The projection must marshal deterministically (FR-010).
	a, err := json.Marshal(r2)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b, err := json.Marshal(rep.ToReportV2("2026-07-14T00:00:00Z"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(a) != string(b) {
		t.Error("ToReportV2 not deterministic for identical inputs")
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

// TestBuildTokenReportWithholdsWhenBaselineSchemasMissing guards the other half
// of the MCP-3161 valve: if the baseline upstream tools were counted WITHOUT
// schemas (e.g. the /api/v1/tools converter dropped them), the headline must be
// withheld even when the proxy management tools do carry schemas — otherwise the
// report falsely claims a full-schema baseline (MCP-3132/MCP-3167).
func TestBuildTokenReportWithholdsWhenBaselineSchemasMissing(t *testing.T) {
	tk, err := NewTokenizer(DefaultEncoding)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	upstreamSchemaless := []Tool{{Name: "big", Description: "d"}} // schema dropped
	rt := []Tool{{Name: "retrieve_tools", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}}
	ce := []Tool{{Name: "code_execution", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}}

	rep := buildTokenReport(tk, upstreamSchemaless, rt, ce)
	if rep.AuthoritativeHeadline {
		t.Error("headline must be withheld when the baseline upstream tools carry no schemas")
	}
	if rep.BaselineSchemasCounted {
		t.Error("BaselineSchemasCounted must be false when no upstream tool has a schema")
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
