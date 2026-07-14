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
// retrieval accuracy, and search latency, all gathered from one running
// proxy — plus, since Spec 083 (T016), the retrieve_tools RESPONSE cost
// measured over the real MCP protocol (per-golden-query rows with span-based
// component attribution, FR-001/002), the break-even analysis (FR-003/004),
// the FR-021 proxy identity fields, and the session-cost estimate rows for
// the measured live encoding.
type LiveReport struct {
	Proxy     string            `json:"proxy"`
	Encoding  string            `json:"encoding"`
	Tokens    *LiveTokenReport  `json:"tokens"`
	Retrieval *RetrievalMetrics `json:"retrieval"`
	Latency   *LatencyReport    `json:"latency"`

	// ProxyInfo records the measured proxy's identity and discovery
	// configuration (FR-021): version, tool count, tools_limit, routing_mode.
	// Collected best-effort from /api/v1/info, /api/v1/config, /api/v1/status;
	// fields the proxy does not expose stay zero-valued.
	ProxyInfo *ProxyInfo `json:"proxy_info,omitempty"`
	// ResponseCost is the per-golden-query retrieve_tools response-cost
	// summary over the real MCP protocol (FR-001). nil when the measurement
	// was skipped — see ResponseCostNote.
	ResponseCost *ResponseCostSummary `json:"response_cost,omitempty"`
	// BreakEven is the FR-003 analysis over the SAME token report as the
	// headline (naive full menu vs proxy menu, both schema-counted). nil when
	// responses were not measured or the menu counts are non-authoritative.
	BreakEven *BreakEvenAnalysis `json:"break_even,omitempty"`
	// SessionEstimates are the FR-019 estimator rows for the measured live
	// encoding (baseline_json — the proxy's own full-JSON responses).
	SessionEstimates []SessionCostEstimate `json:"session_estimates,omitempty"`
	// ResponseCostNote explains a skipped or degraded response-cost /
	// break-even measurement (never silent, SC-006 spirit). Empty on success.
	ResponseCostNote string `json:"response_cost_note,omitempty"`
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

	rep := &LiveReport{
		Proxy:     client.BaseURL,
		Encoding:  tk.encoding,
		Tokens:    tokenRep,
		Retrieval: metrics,
		Latency:   computeLatency(latencies, loadAll),
	}

	// 4. Response cost over the real MCP protocol (Spec 083 US1, FR-001/002/
	// 023): one retrieve_tools call per golden query through a single MCP
	// session. A proxy without a reachable /mcp endpoint degrades to an
	// explicit skip note — the REST-based measurements above stay intact
	// (additive wiring) — but per-call failures after a successful session
	// are real faults and fail the run.
	respCost, note, err := measureResponseCost(ctx, tk, client, golden)
	if err != nil {
		return nil, err
	}
	rep.ResponseCost = respCost
	rep.ResponseCostNote = note

	// 5. Break-even (FR-003) + session estimates (FR-019), both derived from
	// the SAME token report as the headline (research D7b: one denominator).
	// Non-authoritative menu counts (schemas missing on either side) would
	// make the numerator compare different token shapes, so the analysis is
	// withheld with a reason instead of computed dishonestly.
	if respCost != nil {
		proxyMenuTokens := 0
		for _, m := range tokenRep.Modes {
			if m.Mode == ModeRetrieveTools {
				proxyMenuTokens = m.Tokens
			}
		}
		if !tokenRep.AuthoritativeHeadline {
			rep.ResponseCostNote = "break-even withheld: menu token counts are non-authoritative (schemas missing on one side — see tokens.notes)"
		} else {
			be, berr := ComputeBreakEven(tokenRep.BaselineTokens, proxyMenuTokens, respCost.Mean)
			if berr != nil {
				return nil, fmt.Errorf("break-even: %w", berr)
			}
			rep.BreakEven = be
			// The measured live encoding is the proxy's own full-JSON
			// response rendering — the baseline_json arm by definition.
			rep.SessionEstimates = EstimateSessionCosts(proxyMenuTokens,
				map[string]float64{"baseline_json": respCost.Mean})
		}
	}

	// 6. Proxy identity (FR-021), best-effort: fields the proxy does not
	// expose stay zero-valued rather than failing the run.
	rep.ProxyInfo = fetchProxyInfo(ctx, client, len(upstream))

	return rep, nil
}

// measureResponseCost opens one MCP session against the proxy and replays the
// golden set through retrieve_tools with the proxy's configured default limit
// (what a real agent pays per call), measuring the raw text content of each
// response (FR-001) with client-side latency (FR-023). A failed session
// initialization returns (nil, reason, nil) — the skip path; a failed call
// inside an established session is an error.
func measureResponseCost(ctx context.Context, tk *Tokenizer, client *LiveClient, golden *GoldenSet) (*ResponseCostSummary, string, error) {
	caller, err := NewMCPCaller(ctx, client.BaseURL, client.APIKey)
	if err != nil {
		return nil, fmt.Sprintf("response-cost measurement skipped: %v", err), nil
	}
	defer caller.Close()

	perQuery := make([]DiscoveryResponseMeasurement, 0, len(golden.Queries))
	for _, q := range golden.Queries {
		raw, latency, err := caller.RetrieveTools(ctx, q.Query, 0)
		if err != nil {
			return nil, "", fmt.Errorf("measure response cost: %w", err)
		}
		m, err := MeasureRetrieveToolsResponse(tk, q.ID, raw, ms(latency))
		if err != nil {
			return nil, "", fmt.Errorf("measure response cost: %w", err)
		}
		perQuery = append(perQuery, *m)
	}
	return SummarizeResponseCost(perQuery), "", nil
}

// fetchProxyInfo collects the FR-021 proxy identity fields best-effort:
// version from GET /api/v1/info, routing_mode from GET /api/v1/status, and
// tools_limit from GET /api/v1/config (no dedicated endpoint exposes it).
// Endpoint failures leave the corresponding field zero-valued — recording
// what IS known must not fail the run.
func fetchProxyInfo(ctx context.Context, client *LiveClient, toolCount int) *ProxyInfo {
	info := &ProxyInfo{ToolCount: toolCount}

	var infoResp struct {
		Version string `json:"version"`
	}
	if err := client.getJSON(ctx, "/api/v1/info", &infoResp); err == nil {
		info.Version = infoResp.Version
	}

	var statusResp struct {
		RoutingMode string `json:"routing_mode"`
	}
	if err := client.getJSON(ctx, "/api/v1/status", &statusResp); err == nil {
		info.RoutingMode = statusResp.RoutingMode
	}

	var cfgResp struct {
		Config struct {
			ToolsLimit int `json:"tools_limit"`
		} `json:"config"`
	}
	if err := client.getJSON(ctx, "/api/v1/config", &cfgResp); err == nil {
		info.ToolsLimit = cfgResp.Config.ToolsLimit
	}
	return info
}

// ToReportV2 projects a live run into the versioned v2 report envelope
// (research D12): the live proxy toolset becomes the single corpus row, arm
// rows stay empty (arms are offline-computable, not live-measured), and every
// headline number carries its provenance label (SC-005).
func (r *LiveReport) ToReportV2(generatedAt string) *ReportV2 {
	version := "live"
	toolCount := 0
	var proxy *ProxyInfo
	if r.ProxyInfo != nil {
		p := *r.ProxyInfo
		proxy = &p
		toolCount = p.ToolCount
		if p.Version != "" {
			version = p.Version
		}
	}

	r2 := &ReportV2{
		ReportVersion: ReportVersion2,
		GeneratedAt:   generatedAt,
		Tokenizer:     TokenizerInfo{Name: r.Encoding, Caveat: TokenizerCaveatText},
		Proxy:         proxy,
		Corpora: []CorpusDescriptor{{
			ID:        "live-proxy",
			Name:      "live proxy toolset (" + r.Proxy + ")",
			Version:   version,
			ToolCount: toolCount,
			License:   "n/a (live measurement of a running proxy; not redistributed)",
			Committed: false,
		}},
		Arms:             []ArmResult{},
		ResponseCost:     r.ResponseCost,
		BreakEven:        r.BreakEven,
		SessionEstimates: r.SessionEstimates,
		Provenance: map[string]string{
			"menu_tokens":       ProvenanceMeasured,
			"response_cost":     ProvenanceMeasured,
			"break_even":        ProvenanceComputed,
			"session_estimates": SessionEstimateProvenance,
			"latency":           ProvenanceMeasured,
		},
	}
	if r.Latency != nil {
		r2.Latency = &LatencyV2{
			P50Ms: r.Latency.P50ms,
			P95Ms: r.Latency.P95ms,
			P99Ms: r.Latency.P99ms,
			MaxMs: r.Latency.MaxMs,
		}
	}
	return r2
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
