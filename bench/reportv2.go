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

// --- Spec 103 additive blocks (contracts/report-v2-additions.md) ---
//
// The replay, agent-loop and payload-decomposition blocks are ADDITIVE
// optional members of this same v2 envelope: ReportVersion2 stays 2 and no
// existing field changes meaning. That rule is what forces the two shapes
// below to exist rather than reusing what is already here.
//
// Why a per-BLOCK accounting source. The envelope carries ONE tokenizer
// identity (TokenizerInfo) for the whole document. Once provider-reported
// usage enters the same document, that field would silently claim the entire
// report was tokenizer-counted. Narrowing it to "the deterministic sections
// only" is a meaning change, and a meaning change requires a version bump —
// which this feature must not do. So the document-level field keeps its
// meaning intact (it names the deterministic estimator, still true of every
// section that has one) and each new block names its own source explicitly. A
// reader never has to infer scope from a document-level field.
//
// Why a per-ROW provenance. ReportV2.Provenance is section-level: one badge
// per section key. FR-013 lets measured and estimated figures coexist INSIDE
// one block, which a single badge cannot express. The concrete failure it
// prevents: RetryRateForArm returns 0.0 for an unknown arm, indistinguishable
// from a measured 0.0, so one table can mix a measured rate with a defaulted
// one under a single badge. Every new row type therefore carries its own
// provenance, constrained to the same three values as the section map.

// Accounting-source kinds. Figures from different kinds are NEVER summed: a
// cross-source aggregate is withheld with a stated reason (data-model.md
// invariant 2), reusing the harness's withhold-rather-than-compute pattern.
const (
	// AccountingKindTokenizer marks figures counted by the pinned
	// deterministic estimator named in the document-level TokenizerInfo.
	AccountingKindTokenizer = "deterministic-tokenizer"
	// AccountingKindProvider marks figures taken from provider-reported
	// usage, which requires a pinned model to be interpretable at all.
	AccountingKindProvider = "provider-usage"
)

// Exclusion reason codes for replay sessions (FR-003). Every supplied session
// that does not reach a figure is counted under exactly one of these — silence
// is never success (data-model.md invariant 3).
const (
	ReplayExclusionTruncated    = "truncated"
	ReplayExclusionBodiesMissed = "bodies_missing"
	ReplayExclusionSensitive    = "sensitive"
	ReplayExclusionUnreplayable = "unreplayable"
	ReplayExclusionUnattributed = "unattributed"
)

// Spec-102 verdicts (FR-025). The verdict is stated explicitly per fleet
// shape; an absent verdict is not "confirmed by default".
const (
	Spec102Confirmed = "confirmed"
	Spec102Corrected = "corrected"
)

// AccountingSource names where one block's numbers came from. It is
// deliberately a value, not a string: a provider-sourced block is only
// interpretable with the model pinned alongside the provider, and squeezing
// both into one enum string would make the "never sum across sources"
// comparison a string-parsing exercise.
type AccountingSource struct {
	// Kind is AccountingKindTokenizer or AccountingKindProvider.
	Kind string `json:"kind"`
	// Identity names the estimator ("cl100k_base") or the provider.
	Identity string `json:"identity"`
	// Model pins the model for provider-sourced figures. Mandatory for
	// AccountingKindProvider, empty otherwise — usage numbers from an
	// unpinned model are not comparable to any later run.
	Model string `json:"model,omitempty"`
}

// IsZero reports whether the source was never populated. A block with a zero
// source cannot enter any aggregate (data-model.md: accounting_source is
// mandatory), so emission-time validation rejects it rather than defaulting.
func (a AccountingSource) IsZero() bool {
	return a.Kind == "" && a.Identity == "" && a.Model == ""
}

// FleetShape is the tool count and definition-size distribution a figure holds
// for. Every published percentage carries one (IC-004): a saving quoted
// without its fleet shape is a saving quoted at an unstated fleet size, which
// is how spec 102 over-projected from a single corpus.
type FleetShape struct {
	ID                   string  `json:"id"`
	ToolCount            int     `json:"tool_count"`
	MeanDefinitionTokens float64 `json:"mean_definition_tokens,omitempty"`
	P95DefinitionTokens  int     `json:"p95_definition_tokens,omitempty"`
}

// ReplayExclusion counts the supplied sessions dropped for one reason (FR-003,
// SC-008). Reasons are the Replay* constants above.
type ReplayExclusion struct {
	Reason   string `json:"reason"`
	Sessions int    `json:"sessions"`
}

// ReplayCellCost is one mode cell's recomputed cost over the recorded
// workload. Bodies-off it carries a MEASURED menu cost and, at best, an
// ESTIMATED response cost derived from pre-truncation byte LENGTHS — which is
// exactly the mix that per-row provenance exists to express.
// ReplayLoaderReason is one reason code and its count, as the loader recorded
// it. The reason strings are the loader's own vocabulary rather than the
// report's closed enum: they name causes the report-level enum has no term for
// (a truncated internal response that would overstate, a byte-gap record), and
// flattening them into the closed set would destroy the very distinction that
// makes them actionable.
type ReplayLoaderReason struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// ReplayLoaderAccounting is the loader-level companion to ReplayBlock.Exclusions.
//
// The three buckets are deliberately separate rather than one total. A dropped
// record never entered a unit of work; a withheld cost was suppressed on a
// record that DID contribute; a flagged record contributed and was counted but
// carries a usability caveat. "12 exclusions" spanning all three is
// uninterpretable, and an uninterpretable count satisfies the letter of
// "everything is counted" while destroying its purpose.
type ReplayLoaderAccounting struct {
	RecordsDropped int                  `json:"records_dropped"`
	Dropped        []ReplayLoaderReason `json:"dropped,omitempty"`
	CostsWithheld  int                  `json:"costs_withheld"`
	Withheld       []ReplayLoaderReason `json:"withheld,omitempty"`
	RecordsFlagged int                  `json:"records_flagged"`
	Flagged        []ReplayLoaderReason `json:"flagged,omitempty"`
	// OrphanedSubCalls counts sub-calls whose parent fell outside the exported
	// window. They ARE counted in the workload, at top level; what was lost is
	// their attribution to a parent. Reported because a total that hides it
	// cannot be audited.
	OrphanedSubCalls int `json:"orphaned_sub_calls,omitempty"`
}

type ReplayCellCost struct {
	CellID string `json:"cell_id"`
	// Provenance is measured/computed/estimated for THIS row.
	Provenance string `json:"provenance"`
	Calls      int    `json:"calls"`
	// MenuTokens is the tool-surface cost of what the agent was shown under
	// this cell, computed from the supplied fleet input.
	MenuTokens int `json:"menu_tokens"`
	// ResponseTokens is present only when a response figure could be
	// produced at all; bodies-off it is an estimate from byte lengths, never
	// a measurement, and a pointer so a genuine 0 is distinguishable from
	// "not computed".
	ResponseTokens *int `json:"response_tokens,omitempty"`
	// AbsoluteWorkloadWithheld records that no absolute complete-workload
	// figure is available for this cell. Bodies-off that is true of EVERY
	// cell, because complete workload includes every consumed response and
	// that text is absent. It is reported as unavailable, never as zero.
	AbsoluteWorkloadWithheld bool   `json:"absolute_workload_withheld,omitempty"`
	WithheldReason           string `json:"withheld_reason,omitempty"`
}

// ReplayDirectDelta is the honest bodies-off headline: the cross-mode delta
// between the two direct cells, whose identical call responses cancel out of
// the comparison. It is a row in its own right and carries its own provenance
// (computed — it is arithmetic over two measured menu costs).
type ReplayDirectDelta struct {
	FromCellID  string  `json:"from_cell_id"`
	ToCellID    string  `json:"to_cell_id"`
	Provenance  string  `json:"provenance"`
	DeltaTokens int     `json:"delta_tokens"`
	DeltaPct    float64 `json:"delta_pct"`
}

// ReplayBlock is the deterministic recomputation over a real recorded workload
// (US1). It is a COUNTERFACTUAL: recorded call shape scored against the
// SUPPLIED fleet, not the fleet as it stood when the session was recorded, and
// never observed agent behaviour (FR-004). No recorded content reaches this
// struct — only counts, sizes and derived measurements (FR-006).
type ReplayBlock struct {
	AccountingSource AccountingSource `json:"accounting_source"`
	// Counterfactual is the mandatory FR-004 label. It is a required field
	// rather than a rendering concern so a figure cannot be emitted without
	// it and then lose the caveat downstream.
	Counterfactual string `json:"counterfactual"`
	// BodiesIncluded records whether the export carried response bodies.
	// False is the default and the safe mode; true is an explicit opt-in
	// over raw unmasked traffic (the export path does not mask).
	BodiesIncluded bool       `json:"bodies_included"`
	FleetShape     FleetShape `json:"fleet_shape"`
	// SessionsSupplied/SessionsUsed plus Exclusions are the FR-003 accounting:
	// supplied minus used must be explained by the exclusion counts.
	SessionsSupplied int               `json:"sessions_supplied"`
	SessionsUsed     int               `json:"sessions_used"`
	Exclusions       []ReplayExclusion `json:"exclusions,omitempty"`
	// LoaderAccounting is the loader's own exclusion detail: what was dropped
	// before joining a unit of work, what had its cost withheld INSIDE an
	// admitted unit, what was admitted but flagged, and how many sub-calls
	// lost their parent. The session rows above cannot express any of that,
	// and a withheld cost that collapses a response total with no row saying
	// why is precisely the silent accounting SC-008 forbids.
	LoaderAccounting *ReplayLoaderAccounting `json:"loader_accounting,omitempty"`
	Cells            []ReplayCellCost        `json:"cells,omitempty"`
	// DirectDelta is absent when only one direct cell was measured.
	DirectDelta *ReplayDirectDelta `json:"direct_delta,omitempty"`
	// SensitiveFlagBestEffort restates, in the report itself, that the
	// sensitive-data flag is derived from detection metadata written
	// asynchronously AFTER persistence: its absence does not prove a record
	// is clean. It is a reducer, never a guarantee (Principle IV).
	SensitiveFlagBestEffort bool `json:"sensitive_flag_best_effort,omitempty"`
}

// AgentLoopCell is one mode cell measured over a live agent loop (US2). Its
// numbers come from provider-reported usage, so they may never be summed with
// the deterministic blocks' figures.
type AgentLoopCell struct {
	CellID string `json:"cell_id"`
	// Provenance is measured for a cell backed by real runs; a row still
	// carrying an assumed default stays estimated and must be rendered as
	// such beside its measured neighbours (FR-013).
	Provenance string `json:"provenance"`
	// Runs and SpreadPct are the FR-021 consistency requirement: a
	// model-dependent figure needs at least four runs plus a spread before
	// it may be a headline.
	Runs      int     `json:"runs"`
	SpreadPct float64 `json:"spread_pct"`
	// PartialRuns counts runs that did not finish. They are excluded from
	// the figures above and reported, never silently absorbed (FR-032).
	PartialRuns int `json:"partial_runs,omitempty"`
	// TokensPerCompletedTask is the FR-018 headline, and CompletionRatePct
	// travels with it at equal prominence — a cheap mode that finishes
	// fewer tasks is not a saving.
	TokensPerCompletedTask float64 `json:"tokens_per_completed_task"`
	CompletionRatePct      float64 `json:"completion_rate_pct"`
	FirstAttemptSuccessPct float64 `json:"first_attempt_success_pct"`
	// Corrective and infrastructure retries are counted separately: only the
	// first kind says anything about how well the mode serves the agent.
	RetriesCorrective     int `json:"retries_corrective"`
	RetriesInfrastructure int `json:"retries_infrastructure"`
	// Input/output/cache-read consumption stays split (FR-014); collapsing
	// them hides the cache behaviour that dominates a long agent loop.
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	CacheReadTokens int `json:"cache_read_tokens"`
	// Headline is false whenever the FR-021 bar is unmet; the row is still
	// published, just not as a headline.
	Headline bool `json:"headline"`
	// Regression marks a cell whose completion rate falls more than the
	// stated threshold below the baseline's. Such a cell must never be
	// described as a saving regardless of its token cost (FR-019).
	Regression bool `json:"regression,omitempty"`
	// Withheld carries a figure that cannot be computed honestly — a
	// cross-source aggregate above all — with the reason stated.
	Withheld       bool   `json:"withheld,omitempty"`
	WithheldReason string `json:"withheld_reason,omitempty"`
}

// AgentLoopBlock is the live measured block (US2). Its AccountingSource is
// provider-usage plus the pinned model, which is why the block-level field
// exists: this block and the deterministic ones sit in one document and must
// stay separable.
type AgentLoopBlock struct {
	AccountingSource AccountingSource `json:"accounting_source"`
	// Suite and SuiteVersion pin the task suite so a later run is comparable
	// to an earlier one (FR-028).
	Suite        string          `json:"suite,omitempty"`
	SuiteVersion string          `json:"suite_version,omitempty"`
	FleetShape   FleetShape      `json:"fleet_shape"`
	Cells        []AgentLoopCell `json:"cells,omitempty"`
}

// PayloadDecompositionRow attributes tool-definition cost for ONE fleet shape
// (FR-024/025).
type PayloadDecompositionRow struct {
	FleetShape FleetShape `json:"fleet_shape"`
	Provenance string     `json:"provenance"`
	// The four attributions, as percentages of the definition payload.
	ShareNamesPct        float64 `json:"share_names_pct"`
	ShareDescriptionsPct float64 `json:"share_descriptions_pct"`
	// ShareAnnotationsPct is a POINTER because null and zero mean different
	// things here. The frozen corpora carry no annotations field, so a
	// corpus-based decomposition cannot measure this share at all — and a 0
	// would read as "measured, and annotations cost nothing", which is the
	// silent-zero failure the rest of this report works to avoid. nil marshals
	// to null: not measured.
	ShareAnnotationsPct *float64 `json:"share_annotations_pct"`
	ShareSchemasPct     float64  `json:"share_schemas_pct"`
	// AchievableCeilingPct is recomputed for THIS shape. Carrying a
	// previously published ceiling forward is precisely the error spec 102
	// made when it projected from a single corpus.
	AchievableCeilingPct float64 `json:"achievable_ceiling_pct"`
	// Spec102Verdict is an explicit confirmed/corrected call on spec 102's
	// conclusion, with the delta when corrected.
	Spec102Verdict  string   `json:"spec102_verdict"`
	Spec102DeltaPct *float64 `json:"spec102_delta_pct,omitempty"`
}

// PayloadDecompositionBlock holds one row per fleet shape (at least two, per
// FR-024) so the reader can see how the attribution moves with fleet size
// rather than trusting a single-corpus projection.
type PayloadDecompositionBlock struct {
	AccountingSource AccountingSource          `json:"accounting_source"`
	Shapes           []PayloadDecompositionRow `json:"shapes,omitempty"`
}

// IsValidProvenance reports whether s is one of the three closed-enum values
// shared by the section-level map and every per-row provenance field.
func IsValidProvenance(s string) bool {
	return s == ProvenanceMeasured || s == ProvenanceComputed || s == ProvenanceEstimated
}

// ValidateAdditiveBlocks enforces, at emission time, the two properties the
// JSON schema cannot: that every present block names a populated accounting
// source, and that every row of every block carries a provenance from the
// closed enum.
//
// The schema declares these blocks but has no additionalProperties:false and
// cannot require a field to be non-empty, so a block whose accounting source
// was never set would validate silently — which is the exact failure the
// contract calls out ("a field that exists but is never set does not satisfy
// the contract"). Callers that build a report should run this before writing.
func (r *ReportV2) ValidateAdditiveBlocks() error {
	checkSource := func(block string, src AccountingSource) error {
		if src.IsZero() {
			return fmt.Errorf("%s block: accounting_source is unset", block)
		}
		switch src.Kind {
		case AccountingKindTokenizer, AccountingKindProvider:
		default:
			return fmt.Errorf("%s block: accounting_source.kind %q not in {%s,%s}",
				block, src.Kind, AccountingKindTokenizer, AccountingKindProvider)
		}
		if src.Identity == "" {
			return fmt.Errorf("%s block: accounting_source.identity is empty", block)
		}
		if src.Kind == AccountingKindProvider && src.Model == "" {
			return fmt.Errorf("%s block: provider-sourced figures need a pinned model", block)
		}
		return nil
	}
	checkRow := func(block, row string, provenance string) error {
		if provenance == "" {
			return fmt.Errorf("%s block: %s has no provenance", block, row)
		}
		if !IsValidProvenance(provenance) {
			return fmt.Errorf("%s block: %s provenance %q not in {%s,%s,%s}",
				block, row, provenance, ProvenanceMeasured, ProvenanceComputed, ProvenanceEstimated)
		}
		return nil
	}

	if b := r.Replay; b != nil {
		if err := checkSource("replay", b.AccountingSource); err != nil {
			return err
		}
		for i := range b.Cells {
			if err := checkRow("replay", fmt.Sprintf("cells[%d] (%s)", i, b.Cells[i].CellID), b.Cells[i].Provenance); err != nil {
				return err
			}
		}
		if b.DirectDelta != nil {
			if err := checkRow("replay", "direct_delta", b.DirectDelta.Provenance); err != nil {
				return err
			}
		}
	}
	if b := r.AgentLoop; b != nil {
		if err := checkSource("agent_loop", b.AccountingSource); err != nil {
			return err
		}
		for i := range b.Cells {
			if err := checkRow("agent_loop", fmt.Sprintf("cells[%d] (%s)", i, b.Cells[i].CellID), b.Cells[i].Provenance); err != nil {
				return err
			}
		}
	}
	if b := r.PayloadDecomposition; b != nil {
		if err := checkSource("payload_decomposition", b.AccountingSource); err != nil {
			return err
		}
		for i := range b.Shapes {
			if err := checkRow("payload_decomposition", fmt.Sprintf("shapes[%d] (%s)", i, b.Shapes[i].FleetShape.ID), b.Shapes[i].Provenance); err != nil {
				return err
			}
		}
	}
	return nil
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
	// Spec 103 blocks. Additive and optional: omitted they do not appear at
	// all, so existing offline reports and their consumers are byte-
	// unaffected and no report_version bump is needed. Each names its own
	// accounting source rather than borrowing the document-level Tokenizer.
	Replay               *ReplayBlock               `json:"replay,omitempty"`
	AgentLoop            *AgentLoopBlock            `json:"agent_loop,omitempty"`
	PayloadDecomposition *PayloadDecompositionBlock `json:"payload_decomposition,omitempty"`
	Provenance           map[string]string          `json:"provenance"`
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
