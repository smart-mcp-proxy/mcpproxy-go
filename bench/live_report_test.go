package bench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	rep, err := RunLive(context.Background(), c, golden, LiveRunOptions{})
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
	rep, err := RunLive(context.Background(), c, golden, LiveRunOptions{})
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

	// FR-023: the MCP retrieve_tools discovery calls get their OWN latency
	// aggregate, computed from the DiscoveryResponseMeasurement latencies —
	// never conflated with the REST /api/v1/index/search percentiles.
	if rep.MCPDiscoveryLatency == nil {
		t.Fatal("MCPDiscoveryLatency missing — discovery-call latencies must be aggregated separately from REST search")
	}
	if rep.MCPDiscoveryLatency.P50Ms <= 0 || rep.MCPDiscoveryLatency.MaxMs < rep.MCPDiscoveryLatency.P50Ms {
		t.Errorf("MCPDiscoveryLatency not plausible: %+v", rep.MCPDiscoveryLatency)
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
	rep, err := RunLive(context.Background(), c, golden, LiveRunOptions{})
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
	// Latency surfaces (FR-023): the flat fields and RESTSearch label the REST
	// /api/v1/index/search calls; MCPDiscovery carries the retrieve_tools
	// aggregate measured over the real MCP protocol.
	if r2.Latency.RESTSearch == nil || r2.Latency.RESTSearch.P50Ms != r2.Latency.P50Ms {
		t.Errorf("Latency.RESTSearch must mirror the flat REST fields: %+v", r2.Latency)
	}
	if r2.Latency.MCPDiscovery == nil {
		t.Error("Latency.MCPDiscovery missing from ReportV2 for a run with measured discovery responses")
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

// TestBuildTokenReportTreatsStubSchemasAsMissing guards the FR-004 stub-schema
// valve: GET /api/v1/tools can serve the supervisor stub
// ({"type":"object","properties":{}} — see scripts/gen-corpus-v2-dump), which
// is NOT a full input schema. A baseline where every schema is a stub (or has
// empty/absent properties) must count as schema-less, so the headline is
// withheld instead of claiming a full-schema baseline it never had (MCP-3167).
func TestBuildTokenReportTreatsStubSchemasAsMissing(t *testing.T) {
	tk, err := NewTokenizer(DefaultEncoding)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	upstreamStubs := []Tool{
		{Name: "a", Description: "d", Schema: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "b", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)},
	}
	rt := []Tool{{Name: "retrieve_tools", Description: "d", Schema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`)}}
	ce := []Tool{{Name: "code_execution", Description: "d", Schema: json.RawMessage(`{"type":"object","properties":{"code":{"type":"string"}}}`)}}

	rep := buildTokenReport(tk, upstreamStubs, rt, ce)
	if rep.BaselineSchemasCounted {
		t.Error("BaselineSchemasCounted must be false when every upstream schema is a stub")
	}
	if rep.AuthoritativeHeadline {
		t.Error("headline must be withheld for a stub-schema baseline")
	}
	// One real schema among stubs is enough to count as schema-bearing.
	upstreamMixed := append([]Tool{
		{Name: "c", Description: "d", Schema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)},
	}, upstreamStubs...)
	rep = buildTokenReport(tk, upstreamMixed, rt, ce)
	if !rep.BaselineSchemasCounted {
		t.Error("BaselineSchemasCounted must be true when at least one upstream tool has a real schema")
	}
}

// writeCorpusV2Fixture writes a minimal corpus_v2 file with the given
// tool_id → schema entries and returns its path.
func writeCorpusV2Fixture(t *testing.T, schemas map[string]string) string {
	t.Helper()
	type ct struct {
		ToolID      string          `json:"tool_id"`
		Server      string          `json:"server"`
		Tool        string          `json:"tool"`
		Description string          `json:"description"`
		Schema      json.RawMessage `json:"schema"`
	}
	var tools []ct
	for id, schema := range schemas {
		server, name, _ := strings.Cut(id, ":")
		tools = append(tools, ct{ToolID: id, Server: server, Tool: name, Description: "corpus description", Schema: json.RawMessage(schema)})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].ToolID < tools[j].ToolID })
	raw, err := json.Marshal(map[string]any{"version": "corpus_v2-test", "tools": tools})
	if err != nil {
		t.Fatalf("marshal corpus fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "corpus_v2.tools.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write corpus fixture: %v", err)
	}
	return path
}

// stubProxyStubSchemas mimics a proxy whose /api/v1/tools serves only the
// supervisor stub schema for every tool — the exact live condition the
// corpus_v2 schema-source join exists for.
func stubProxyStubSchemas(t *testing.T) *httptest.Server {
	t.Helper()
	stub := map[string]any{"type": "object", "properties": map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tools", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"tools": []map[string]any{
					{"name": "read_text_file", "server_name": "filesystem", "description": "Read a file as text", "schema": stub},
					{"name": "echo", "server_name": "memory", "description": "Echo input", "schema": stub},
				},
			},
		})
	})
	mux.HandleFunc("/api/v1/index/search", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"query": r.URL.Query().Get("q"),
				"results": []map[string]any{
					{"tool": map[string]any{"name": "read_text_file", "server_name": "filesystem"}, "score": 0.9},
				},
				"total": 1,
				"took":  "0ms",
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func liveGoldenOneQuery() *GoldenSet {
	return &GoldenSet{
		CorpusVersion: "corpus_v1",
		Queries: []GoldenQuery{
			{ID: "q1", Query: "read a file", Labels: []Label{{ToolID: "filesystem:read_text_file", Relevance: 2}}},
		},
	}
}

// TestRunLive_StubSchemasWithholdHeadline: without a corpus_v2 schema source,
// a live proxy serving only stub schemas must NOT produce an authoritative
// headline — the "naive full menu" would be counted over empty schemas.
func TestRunLive_StubSchemasWithholdHeadline(t *testing.T) {
	srv := stubProxyStubSchemas(t)
	c := NewLiveClient(srv.URL, "test-key")

	rep, err := RunLive(context.Background(), c, liveGoldenOneQuery(), LiveRunOptions{})
	if err != nil {
		t.Fatalf("RunLive: %v", err)
	}
	if rep.Tokens.BaselineSchemasCounted {
		t.Error("BaselineSchemasCounted must be false when the proxy serves only stub schemas")
	}
	if rep.Tokens.AuthoritativeHeadline {
		t.Error("headline must be withheld for a stub-schema live baseline")
	}
	if rep.Tokens.SchemaSource == "" {
		t.Error("SchemaSource provenance missing")
	}
	if !strings.Contains(rep.Tokens.SchemaSource, "/api/v1/tools") {
		t.Errorf("SchemaSource = %q, want the live endpoint named", rep.Tokens.SchemaSource)
	}
}

// TestRunLive_CorpusV2SchemaJoin: providing the corpus_v2 file joins live
// tools by id with the frozen full schemas, restoring an authoritative
// full-definition baseline even when /api/v1/tools serves stubs (FR-004).
func TestRunLive_CorpusV2SchemaJoin(t *testing.T) {
	srv := stubProxyStubSchemas(t)
	c := NewLiveClient(srv.URL, "test-key")

	corpusPath := writeCorpusV2Fixture(t, map[string]string{
		"filesystem:read_text_file": `{"type":"object","properties":{"path":{"type":"string","description":"File location"}},"required":["path"]}`,
		"memory:echo":               `{"type":"object","properties":{"text":{"type":"string"}}}`,
	})
	rep, err := RunLive(context.Background(), c, liveGoldenOneQuery(), LiveRunOptions{CorpusV2Path: corpusPath})
	if err != nil {
		t.Fatalf("RunLive: %v", err)
	}
	if !rep.Tokens.BaselineSchemasCounted {
		t.Error("BaselineSchemasCounted must be true after the corpus_v2 join")
	}
	if !rep.Tokens.AuthoritativeHeadline {
		t.Errorf("headline must be authoritative after a complete corpus_v2 join; notes: %v", rep.Tokens.Notes)
	}
	if !strings.Contains(rep.Tokens.SchemaSource, "corpus_v2") {
		t.Errorf("SchemaSource = %q, want corpus_v2 provenance", rep.Tokens.SchemaSource)
	}

	// The joined baseline must cost more than a name+description-only count.
	tk, err := NewTokenizer(DefaultEncoding)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	nameDescOnly := tk.CountTool(Tool{Name: "read_text_file", Description: "Read a file as text"}) +
		tk.CountTool(Tool{Name: "echo", Description: "Echo input"})
	if rep.Tokens.BaselineTokens <= nameDescOnly {
		t.Errorf("BaselineTokens = %d, want > %d (corpus schemas must be counted)", rep.Tokens.BaselineTokens, nameDescOnly)
	}
}

// TestRunLive_CorpusV2JoinMissWithholdsHeadline: a live tool absent from the
// corpus_v2 schema source falls back to name+description and the headline is
// withheld — the MCP-3161/3167 guard pattern, never a silent partial join.
func TestRunLive_CorpusV2JoinMissWithholdsHeadline(t *testing.T) {
	srv := stubProxyStubSchemas(t)
	c := NewLiveClient(srv.URL, "test-key")

	// memory:echo is missing from the corpus on purpose.
	corpusPath := writeCorpusV2Fixture(t, map[string]string{
		"filesystem:read_text_file": `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
	})
	rep, err := RunLive(context.Background(), c, liveGoldenOneQuery(), LiveRunOptions{CorpusV2Path: corpusPath})
	if err != nil {
		t.Fatalf("RunLive: %v", err)
	}
	if rep.Tokens.AuthoritativeHeadline {
		t.Error("headline must be withheld when a live tool is missing from the corpus_v2 schema source")
	}
	for _, m := range rep.Tokens.Modes {
		if m.SavingsRatio != 0 {
			t.Errorf("savings ratio must be withheld (0), got %v for %q", m.SavingsRatio, m.Mode)
		}
	}
	found := false
	for _, n := range rep.Tokens.Notes {
		if strings.Contains(n, "WITHHELD") && strings.Contains(n, "memory:echo") {
			found = true
		}
	}
	if !found {
		t.Errorf("notes must explain the withheld headline and name the missing tool: %v", rep.Tokens.Notes)
	}
}

// TestRunLive_ExpectedToolCountDrift wires the FR-021 corpus-drift signal:
// the caller-supplied expected tool count lands in ProxyInfo (and hence the
// v2 report), and the dashboard renders a drift warning when actual differs.
func TestRunLive_ExpectedToolCountDrift(t *testing.T) {
	srv := stubProxy(t)
	defer srv.Close()
	c := NewLiveClient(srv.URL, "test-key")

	rep, err := RunLive(context.Background(), c, liveGoldenOneQuery(), LiveRunOptions{ExpectedToolCount: 5})
	if err != nil {
		t.Fatalf("RunLive: %v", err)
	}
	if rep.ProxyInfo == nil || rep.ProxyInfo.ExpectedToolCount != 5 {
		t.Fatalf("ProxyInfo.ExpectedToolCount = %+v, want 5", rep.ProxyInfo)
	}
	if rep.ProxyInfo.ToolCount != 2 {
		t.Fatalf("ProxyInfo.ToolCount = %d, want 2", rep.ProxyInfo.ToolCount)
	}

	r2 := rep.ToReportV2("2026-07-14T00:00:00Z")
	if r2.Proxy == nil || r2.Proxy.ExpectedToolCount != 5 {
		t.Fatalf("ReportV2.Proxy.ExpectedToolCount = %+v, want 5", r2.Proxy)
	}
	htmlPath := filepath.Join(t.TempDir(), "dashboard.html")
	if err := r2.WriteHTML(htmlPath); err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("read dashboard: %v", err)
	}
	if !strings.Contains(string(html), "drift") {
		t.Error("dashboard must render a corpus-drift warning when actual != expected tool count")
	}
}
