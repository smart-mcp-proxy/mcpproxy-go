package bench

// Report v2 types for the discovery-effectiveness profiler (spec 083). Every
// struct mirrors specs/083-discovery-profiler/contracts/report-v2.schema.json
// — that schema file is the contract; these types are its Go projection, and
// bench/reportv2_test.go validates a sample against it.
//
// Determinism (FR-010): marshaling is canonical — struct field order is fixed,
// map keys are sorted by encoding/json, and no writer here injects wall-clock
// time (GeneratedAt is caller-supplied data, set once per run).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReportVersion2 is the report_version const of the v2 envelope.
const ReportVersion2 = 2

// Provenance labels for headline metrics (SC-005): a number is either measured
// (observed over the real protocol), computed (derived arithmetic over
// measured inputs), or estimated (model with documented assumptions).
const (
	ProvenanceMeasured  = "measured"
	ProvenanceComputed  = "computed"
	ProvenanceEstimated = "estimated"
)

// TokenizerInfo names the token estimator and carries the mandatory accuracy
// caveat rendered wherever absolute numbers appear (research D11, SC-005).
type TokenizerInfo struct {
	Name   string `json:"name"`
	Caveat string `json:"caveat"`
}

// ProxyInfo records the measured proxy's identity and discovery configuration
// (FR-021): interpreting response cost requires knowing tools_limit and
// routing_mode; tool_count vs expected_tool_count surfaces corpus drift.
type ProxyInfo struct {
	Version           string `json:"version,omitempty"`
	ToolCount         int    `json:"tool_count,omitempty"`
	ExpectedToolCount int    `json:"expected_tool_count,omitempty"`
	ToolsLimit        int    `json:"tools_limit,omitempty"`
	RoutingMode       string `json:"routing_mode,omitempty"`
}

// DegenerateDescriptions counts corpus tools whose descriptions trip the
// FR-020 quality rules, with the rule list echoed for reproducibility.
type DegenerateDescriptions struct {
	Count int      `json:"count"`
	Rules []string `json:"rules,omitempty"`
}

// CorpusDescriptor identifies one measured corpus with license/attribution
// provenance (FR-011/012/013).
type CorpusDescriptor struct {
	ID                     string                  `json:"id"`
	Name                   string                  `json:"name"`
	Version                string                  `json:"version"`
	ToolCount              int                     `json:"tool_count"`
	License                string                  `json:"license"`
	Attribution            string                  `json:"attribution,omitempty"`
	Committed              bool                    `json:"committed"`
	DegenerateDescriptions *DegenerateDescriptions `json:"degenerate_descriptions,omitempty"`
}

// SkipExample is one (tool, error) pair from an arm's skipped tools (FR-009).
type SkipExample struct {
	ToolID string `json:"tool_id"`
	Error  string `json:"error"`
}

// ToolTokenEntry is one heaviest-tools row (FR-020).
type ToolTokenEntry struct {
	ToolID string `json:"tool_id"`
	Tokens int    `json:"tokens"`
}

// RetrievalScore is the flat retrieval-quality DTO of the v2 contract. It is
// mapped from the existing nested RetrievalMetrics by MapRetrievalMetrics —
// the single conversion point — so the report schema stays flat and stable.
type RetrievalScore struct {
	RecallAt1  float64 `json:"recall_at_1"`
	RecallAt3  float64 `json:"recall_at_3"`
	RecallAt5  float64 `json:"recall_at_5"`
	RecallAt10 float64 `json:"recall_at_10"`
	MRR        float64 `json:"mrr"`
	NDCGAt10   float64 `json:"ndcg_at_10"`
	MAP        float64 `json:"map"`
	// MetricNote documents the gain formula (FR-012), or — for an
	// index-altering arm scored on a corpus without relevance labels —
	// explains the absence of numbers.
	MetricNote string `json:"metric_note,omitempty"`
}

// MapRetrievalMetrics converts the existing nested bench.RetrievalMetrics
// (recall_at as a map) into the flat report DTO. nil in, nil out — a nil score
// marks a quality-neutral arm (FR-008).
func MapRetrievalMetrics(m *RetrievalMetrics) *RetrievalScore {
	if m == nil {
		return nil
	}
	return &RetrievalScore{
		RecallAt1:  m.Metrics.RecallAt[1],
		RecallAt3:  m.Metrics.RecallAt[3],
		RecallAt5:  m.Metrics.RecallAt[5],
		RecallAt10: m.Metrics.RecallAt[10],
		MRR:        m.Metrics.MRR,
		NDCGAt10:   m.Metrics.NDCGAt10,
		MAP:        m.Metrics.MAP,
	}
}

// ArmResult is one encoding arm measured on one corpus (FR-005/006/007/009).
//
// Contract conditionals: a skipped row requires SkipReason; a non-skipped row
// requires the token stats; a results-class row requires FixtureID and the
// tabular/non-tabular split (pointers so an explicit 0 still serializes); a
// non-skipped index-altering row requires the quality key (nullable only when
// the corpus has no relevance labels, with MetricNote explaining the absence).
type ArmResult struct {
	Arm                  string           `json:"arm"`
	CorpusID             string           `json:"corpus_id"`
	Skipped              bool             `json:"skipped"`
	SkipReason           string           `json:"skip_reason,omitempty"`
	LowerBound           bool             `json:"lower_bound,omitempty"`
	IndexAltering        bool             `json:"index_altering"`
	PayloadClass         string           `json:"payload_class,omitempty"` // "listing" | "results"
	FixtureID            string           `json:"fixture_id,omitempty"`
	TabularCount         *int             `json:"tabular_count,omitempty"`
	NonTabularCount      *int             `json:"non_tabular_count,omitempty"`
	TotalTokens          int              `json:"total_tokens"`
	MeanTokens           float64          `json:"mean_tokens"`
	P95Tokens            int              `json:"p95_tokens"`
	SavingsVsBaselinePct float64          `json:"savings_vs_baseline_pct"`
	SkippedTools         int              `json:"skipped_tools"`
	SkipExamples         []SkipExample    `json:"skip_examples,omitempty"`
	HeaviestTools        []ToolTokenEntry `json:"heaviest_tools,omitempty"`
	Quality              *RetrievalScore  `json:"quality"`
}

// DiscoveryResponseMeasurement is one golden query's retrieve_tools response
// cost with its span-attributed component breakdown (FR-001/002). Invariant:
// the component values sum EXACTLY to TotalTokens — enforced by construction
// in the span attributor (bench/respcost.go), never by re-tokenizing fields.
type DiscoveryResponseMeasurement struct {
	QueryID     string         `json:"query_id"`
	TotalTokens int            `json:"total_tokens"`
	ResultCount int            `json:"result_count,omitempty"`
	LatencyMs   float64        `json:"latency_ms,omitempty"`
	Components  map[string]int `json:"components"`
}

// ResponseCostSummary aggregates per-query response measurements (FR-001).
type ResponseCostSummary struct {
	P50      int                            `json:"p50"`
	P95      int                            `json:"p95"`
	Max      int                            `json:"max"`
	Mean     float64                        `json:"mean"`
	PerQuery []DiscoveryResponseMeasurement `json:"per_query,omitempty"`
}

// BreakEvenAnalysis is the FR-003 break-even computation with its inputs
// echoed (FR-004): break_even_calls = (naive − proxy_menu) / mean_response.
type BreakEvenAnalysis struct {
	NaiveFullMenuTokens int     `json:"naive_full_menu_tokens"`
	ProxyMenuTokens     int     `json:"proxy_menu_tokens"`
	MeanResponseTokens  float64 `json:"mean_response_tokens"`
	BreakEvenCalls      float64 `json:"break_even_calls"`
	NoBreakEven         bool    `json:"no_break_even,omitempty"`
}

// SessionCostEstimate is one row of the FR-019 session estimator (provenance
// is always "estimated"; retry-rate defaults documented in research D8).
type SessionCostEstimate struct {
	Arm             string  `json:"arm"`
	CallsPerSession int     `json:"calls_per_session"`
	RetryRate       float64 `json:"retry_rate"`
	EstimatedTokens int     `json:"estimated_tokens"`
}

// LatencyAggregate is one nearest-rank percentile summary of client-measured
// latencies (FR-023) for a single measured surface.
type LatencyAggregate struct {
	P50Ms float64 `json:"p50_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
}

// LatencyV2 is the client-measured latency block (FR-023). Two DIFFERENT
// surfaces are measured and must never be conflated: the REST
// /api/v1/index/search calls used for retrieval scoring, and the MCP
// retrieve_tools calls the discovery-response rows time. The flat fields
// summarize the REST search calls (kept additive for existing consumers and
// mirrored in RESTSearch); MCPDiscovery, when present, is the retrieve_tools
// aggregate over the real MCP protocol.
type LatencyV2 struct {
	P50Ms float64 `json:"p50_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
	// RESTSearch labels the flat fields' surface explicitly:
	// GET /api/v1/index/search round-trips.
	RESTSearch *LatencyAggregate `json:"rest_search,omitempty"`
	// MCPDiscovery aggregates the MCP retrieve_tools call latencies from the
	// per-query DiscoveryResponseMeasurement rows.
	MCPDiscovery *LatencyAggregate `json:"mcp_discovery,omitempty"`
}

// LapVerdict is the pinned independent LAP run (FR-015/016, SC-006).
type LapVerdict struct {
	Executed          bool    `json:"executed"`
	SkipReason        string  `json:"skip_reason,omitempty"`
	Version           string  `json:"version,omitempty"`
	MenuTokens        int     `json:"menu_tokens,omitempty"`
	InHouseMenuTokens int     `json:"in_house_menu_tokens,omitempty"`
	DivergencePct     float64 `json:"divergence_pct,omitempty"`
	Grade             string  `json:"grade,omitempty"`
	ArtifactPath      string  `json:"artifact_path,omitempty"`
}

// SubsetInfo records the seeded query subset of a public-corpus run (FR-014):
// same revision + seed + size ⇒ same subset.
type SubsetInfo struct {
	Seed int `json:"seed"`
	Size int `json:"size"`
}

// ReportV2 is the versioned report envelope (research D12). Additive over the
// v1 report: existing consumers are unaffected (reports are never committed,
// Spec 065 CN-003).
type ReportV2 struct {
	ReportVersion    int                   `json:"report_version"`
	GeneratedAt      string                `json:"generated_at"`
	Tokenizer        TokenizerInfo         `json:"tokenizer"`
	Proxy            *ProxyInfo            `json:"proxy,omitempty"`
	Corpora          []CorpusDescriptor    `json:"corpora"`
	Arms             []ArmResult           `json:"arms"`
	ResponseCost     *ResponseCostSummary  `json:"response_cost,omitempty"`
	BreakEven        *BreakEvenAnalysis    `json:"break_even,omitempty"`
	SessionEstimates []SessionCostEstimate `json:"session_estimates,omitempty"`
	Latency          *LatencyV2            `json:"latency,omitempty"`
	Lap              *LapVerdict           `json:"lap,omitempty"`
	Subset           *SubsetInfo           `json:"subset,omitempty"`
	Provenance       map[string]string     `json:"provenance"`
}

// WriteJSON writes the v2 report as indented JSON into dir/report.json.
func (r *ReportV2) WriteJSON(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %q: %w", dir, err)
	}
	path := filepath.Join(dir, "report.json")
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal report v2: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write %q: %w", path, err)
	}
	return path, nil
}
