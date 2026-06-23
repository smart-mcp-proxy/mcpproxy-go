package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// LiveModeResult is the per-mode context-token cost from the live run.
type LiveModeResult struct {
	Mode         string  `json:"mode"`
	ContextTools int     `json:"context_tools"`
	Tokens       int     `json:"tokens"`
	SavingsRatio float64 `json:"savings_vs_baseline,omitempty"`
}

// LiveTokenReport is the exact-token comparison from a live proxy, with the
// baseline upstream tools counted WITH their full JSON input schemas.
//
// AuthoritativeHeadline gates the savings percentage: it is only true when
// schemas were counted on BOTH sides — the proxy management tools carry schemas
// (ProxySchemasCounted) AND the baseline upstream tools carry schemas
// (BaselineSchemasCounted). Counting schemas on one side only overstates or
// distorts savings — the exact error corrected in MCP-3161 — so when either side
// is schema-less the savings ratio is withheld and only raw token totals are
// reported. BaselineSchemasCounted also guards against a /api/v1/tools response
// that silently dropped upstream schemas (MCP-3167).
type LiveTokenReport struct {
	Encoding               string           `json:"encoding"`
	UpstreamTools          int              `json:"upstream_tools"`
	BaselineTokens         int              `json:"baseline_tokens"`
	Modes                  []LiveModeResult `json:"modes"`
	ProxySchemasCounted    bool             `json:"proxy_schemas_counted"`
	BaselineSchemasCounted bool             `json:"baseline_schemas_counted"`
	AuthoritativeHeadline  bool             `json:"authoritative_headline"`
	Notes                  []string         `json:"notes"`
}

// LatencyReport summarizes proxy-side retrieve_tools search latency versus the
// fixed one-shot cost of loading every tool. Times are client-measured
// (milliseconds); the server's SearchToolsResponse "took" field is a "0ms" stub.
type LatencyReport struct {
	Samples        int     `json:"samples"`
	P50ms          float64 `json:"p50_ms"`
	P95ms          float64 `json:"p95_ms"`
	P99ms          float64 `json:"p99_ms"`
	MaxMs          float64 `json:"max_ms"`
	LoadAllToolsMs float64 `json:"load_all_tools_ms"`
}

// LiveReport is the full live benchmark result: exact-token comparison,
// retrieval accuracy, and search latency, all gathered from one running proxy.
type LiveReport struct {
	Proxy     string            `json:"proxy"`
	Encoding  string            `json:"encoding"`
	Tokens    *LiveTokenReport  `json:"tokens"`
	Retrieval *RetrievalMetrics `json:"retrieval"`
	Latency   *LatencyReport    `json:"latency"`
}

// recallCutoffs are the standard Recall@k cutoffs reported (matches Spec 065
// score-report.schema.json recall_at keys).
var recallCutoffs = []int{1, 3, 5, 10}

// WriteJSON writes the live report as indented JSON into dir/live_report.json
// (the dir is gitignored — reports are never committed, per Spec 065 CN-003).
func (r *LiveReport) WriteJSON(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %q: %w", dir, err)
	}
	path := filepath.Join(dir, "live_report.json")
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal live report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write %q: %w", path, err)
	}
	return path, nil
}

// RunLive gathers the full live benchmark from a running proxy: it pulls the
// exact tool definitions (with schemas) for the token comparison, replays the
// golden set through the proxy's BM25 search for accuracy, and records the
// per-query search latency.
func RunLive(ctx context.Context, client *LiveClient, golden *GoldenSet) (*LiveReport, error) {
	tk, err := NewTokenizer(DefaultEncoding)
	if err != nil {
		return nil, err
	}

	// 1. Exact-token: fetch upstream tools with schemas (also times "load all").
	loadStart := time.Now()
	upstream, err := client.FetchUpstreamTools(ctx)
	loadAll := time.Since(loadStart)
	if err != nil {
		return nil, fmt.Errorf("fetch upstream tools: %w", err)
	}
	tokenRep := buildTokenReport(tk, upstream,
		ProxyToolsForMode(ModeRetrieveTools), ProxyToolsForMode(ModeCodeExecution))

	// 2. Accuracy + 3. Latency: replay the golden set, capturing search latency.
	var latencies []time.Duration
	searchFn := func(query string, limit int) ([]string, error) {
		ranked, lat, serr := client.Search(ctx, query, limit)
		latencies = append(latencies, lat)
		return ranked, serr
	}
	metrics, err := ScoreRetrieval(golden, searchFn, recallCutoffs)
	if err != nil {
		return nil, fmt.Errorf("score retrieval: %w", err)
	}

	return &LiveReport{
		Proxy:     client.BaseURL,
		Encoding:  tk.encoding,
		Tokens:    tokenRep,
		Retrieval: metrics,
		Latency:   computeLatency(latencies, loadAll),
	}, nil
}

// buildTokenReport counts the baseline upstream tools WITH schemas against each
// proxy routing mode (rt = retrieve_tools, ce = code_execution), also counted
// with schemas. The headline savings is only emitted when schemas were counted
// on BOTH sides: every proxy tool carries a schema AND the baseline upstream
// tools actually carry schemas. Counting schemas on only one side overstates (or
// distorts) savings — the exact error corrected in MCP-3161 — so otherwise the
// ratio is withheld and only raw token totals are reported. The baseline guard
// also catches a silently schema-less /api/v1/tools response (MCP-3167): if the
// management endpoint drops upstream schemas, no upstream tool has one and the
// headline is withheld rather than claiming a full-schema baseline it never had.
func buildTokenReport(tk *Tokenizer, upstream, rt, ce []Tool) *LiveTokenReport {
	baseTokens := tk.countToolsWithSchema(upstream)

	proxySchemasCounted := allHaveSchema(rt) && allHaveSchema(ce)
	// A correct full-schema baseline has schemas on at least some upstream tools.
	// Requiring ALL would wrongly fail on legitimately parameter-less tools, so
	// "any" is the signal that schemas were not systematically dropped.
	baselineSchemasCounted := anyHaveSchema(upstream)
	authoritative := proxySchemasCounted && baselineSchemasCounted

	rep := &LiveTokenReport{
		Encoding:               tk.encoding,
		UpstreamTools:          len(upstream),
		BaselineTokens:         baseTokens,
		ProxySchemasCounted:    proxySchemasCounted,
		BaselineSchemasCounted: baselineSchemasCounted,
		Modes: []LiveModeResult{
			{Mode: ModeBaseline, ContextTools: len(upstream), Tokens: baseTokens},
			{Mode: ModeRetrieveTools, ContextTools: len(rt), Tokens: tk.countToolsWithSchema(rt)},
			{Mode: ModeCodeExecution, ContextTools: len(ce), Tokens: tk.countToolsWithSchema(ce)},
		},
	}
	rep.AuthoritativeHeadline = authoritative
	if authoritative {
		for i := range rep.Modes {
			m := &rep.Modes[i]
			if m.Mode != ModeBaseline && baseTokens > 0 {
				m.SavingsRatio = 1.0 - float64(m.Tokens)/float64(baseTokens)
			}
		}
		rep.Notes = []string{
			"Baseline counts upstream tools with full JSON input schemas from GET /api/v1/tools; proxy modes count the management tools with their schemas. Headline savings is authoritative.",
		}
	} else if !baselineSchemasCounted {
		rep.Notes = []string{
			"HEADLINE SAVINGS WITHHELD: no upstream baseline tool carried a JSON input schema, so the baseline is NOT the required full-schema token count — typically the /api/v1/tools response dropped upstream schemas (MCP-3167). Reporting savings now would compare a schema-less baseline against schema-counted proxy tools and DISTORT the reduction. Token totals are shown for transparency; the authoritative headline lands once the management endpoint emits upstream schemas.",
		}
	} else {
		rep.Notes = []string{
			"HEADLINE SAVINGS WITHHELD: the baseline upstream tools are counted with full schemas, but the proxy management tools (proxy_tools_v1.json) are description-only. Reporting savings now would count schemas on one side only and OVERSTATE the reduction — the exact error corrected in MCP-3161. Token totals are shown for transparency; the authoritative headline lands once proxy-tool schemas are captured live via MCP tools/list.",
		}
	}
	return rep
}

// anyHaveSchema reports whether at least one tool carries a non-empty schema.
// Used to detect a systematically schema-less baseline (every schema dropped)
// versus a corpus that merely contains some parameter-less tools.
func anyHaveSchema(tools []Tool) bool {
	for _, t := range tools {
		if len(t.Schema) > 0 {
			return true
		}
	}
	return false
}

func allHaveSchema(tools []Tool) bool {
	if len(tools) == 0 {
		return false
	}
	for _, t := range tools {
		if len(t.Schema) == 0 {
			return false
		}
	}
	return true
}

// computeLatency summarizes search-call latencies with nearest-rank
// percentiles, plus the fixed one-shot cost of loading all tools.
func computeLatency(samples []time.Duration, loadAll time.Duration) *LatencyReport {
	rep := &LatencyReport{
		Samples:        len(samples),
		LoadAllToolsMs: ms(loadAll),
	}
	if len(samples) == 0 {
		return rep
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rep.P50ms = ms(percentile(sorted, 50))
	rep.P95ms = ms(percentile(sorted, 95))
	rep.P99ms = ms(percentile(sorted, 99))
	rep.MaxMs = ms(sorted[len(sorted)-1])
	return rep
}

// percentile returns the nearest-rank percentile p (0-100) of a sorted slice.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100.0 * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// ms converts a duration to milliseconds as a float.
func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}
