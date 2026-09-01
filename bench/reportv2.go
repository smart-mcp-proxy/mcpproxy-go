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
	"strings"
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
	// SchemalessTools counts tools in this fleet that carried no priceable
	// input schema.
	//
	// A fleet is refused outright only when NO tool has a real schema. A
	// PARTIAL regression — 3 of 45, or 20 of 45 — passes that guard and prices
	// the stubbed ones at nothing, quietly shrinking the baseline and
	// inflating the headline. Without this count the report carries no trace
	// of it, so the reader cannot tell a clean fleet from a partly stubbed
	// one, and a drift from 3 to 20 between runs is invisible.
	SchemalessTools int `json:"schemaless_tools,omitempty"`
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
	SensitiveFlagBestEffort bool             `json:"sensitive_flag_best_effort,omitempty"`
	Inputs                  *InputProvenance `json:"inputs,omitempty"`
	Records                 *RawRecordsRef   `json:"records,omitempty"`
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
	Suite        string           `json:"suite,omitempty"`
	SuiteVersion string           `json:"suite_version,omitempty"`
	FleetShape   FleetShape       `json:"fleet_shape"`
	Cells        []AgentLoopCell  `json:"cells,omitempty"`
	Inputs       *InputProvenance `json:"inputs,omitempty"`
	Records      *RawRecordsRef   `json:"records,omitempty"`
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
	Inputs           *InputProvenance          `json:"inputs,omitempty"`
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

	// The US3 marks are optional (a report that sets none of them validates
	// exactly as before, which is what keeps this feature additive), but a
	// mark that IS set and is internally inconsistent is an error here rather
	// than a silently-valid document downstream.
	if err := r.RunStatus.Validate(); err != nil {
		return err
	}

	if b := r.Replay; b != nil {
		if err := checkSource("replay", b.AccountingSource); err != nil {
			return err
		}
		if err := b.Inputs.Validate("replay"); err != nil {
			return err
		}
		if err := b.Records.Validate("replay"); err != nil {
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
		if err := b.Inputs.Validate("agent_loop"); err != nil {
			return err
		}
		if err := b.Records.Validate("agent_loop"); err != nil {
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
		if err := b.Inputs.Validate("payload_decomposition"); err != nil {
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
	RunStatus            *RunStatus                 `json:"run_status,omitempty"`
	Replay               *ReplayBlock               `json:"replay,omitempty"`
	AgentLoop            *AgentLoopBlock            `json:"agent_loop,omitempty"`
	PayloadDecomposition *PayloadDecompositionBlock `json:"payload_decomposition,omitempty"`
	Provenance           map[string]string          `json:"provenance"`
}

// WriteJSON writes the v2 report as indented JSON into dir/report.json.
//
// Before marshaling it resolves every raw-record reference against dir, the
// run-local root those references are relative to: a reference whose artifact
// is no longer there degrades to "records not retained" instead of being
// written out as a path that resolves to nothing (T057/FR-029). This is the
// only moment both halves are in hand, and the report lands in the same
// gitignored tree the records do — so a reference that was accurate when the
// block was built can already be stale by the time it is serialized.
//
// It stays deterministic (FR-010/SC-002): the same run directory produces the
// same bytes, and no wall-clock time is injected.
func (r *ReportV2) WriteJSON(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %q: %w", dir, err)
	}
	if r.Replay != nil {
		r.Replay.Records.resolve(dir)
	}
	if r.AgentLoop != nil {
		r.AgentLoop.Records.resolve(dir)
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

// ---------------------------------------------------------------------------
// US3 — an outsider reproduces the numbers (FR-029/030/032).
//
// The three structures below all answer the same question from different
// angles: what would a reader have to take on trust? Each one exists because
// its absence reads as a guarantee. An unlabelled figure derived from a
// private recording reads as reproducible. A run that stopped after four of
// six cells reads as a complete comparison. A report that references no raw
// records reads as if the records were never needed. None of those readings
// is corrected by a README, because a report travels without one.
//
// All three are ADDITIVE and OPTIONAL: a report that sets none of them
// marshals exactly as before (the contract forbids a report_version bump for
// this feature), and ValidateAdditiveBlocks only checks what is present. The
// strictness lives in PublicationCheck instead — the one moment where "not
// declared" must not be allowed to pass as "fine".
// ---------------------------------------------------------------------------

// Input availability (FR-030). Every input is either in the repository,
// obtainable by a documented pinned procedure, or a private recording that an
// outsider simply cannot get.
//
// The third value is not a lesser version of the first two. Repository and
// pinned-procedure inputs differ only in where the bytes live; a private
// recording differs in kind, because FR-006 forbids publishing the recording
// and there is no procedure that would hand it over. That is a permanent
// limitation of any figure built on it, and the report says so rather than
// leaving a reader to assume the usual.
const (
	// InputAvailabilityRepository marks inputs committed in this repository —
	// the frozen corpora and fixtures. Anyone with a clone has them.
	InputAvailabilityRepository = "repository"
	// InputAvailabilityPinnedProcedure marks inputs an outsider fetches by
	// following a documented, version-pinned procedure (a pinned dataset
	// revision, a pinned task suite). Reproducible, just not in-tree.
	InputAvailabilityPinnedProcedure = "pinned-procedure"
	// InputAvailabilityPrivateRecording marks inputs that are one operator's
	// own recorded traffic. Not publishable, therefore not obtainable,
	// therefore not independently reproducible.
	InputAvailabilityPrivateRecording = "private-recording"
)

// InputProvenance is one block's FR-030 mark: where its inputs come from and,
// consequently, whether an outsider can reproduce its figures.
//
// IndependentlyReproducible is deliberately redundant with Availability — it
// is exactly `Availability != private-recording`, and Validate enforces that.
// The redundancy is the point: a renderer, a dashboard or an outside reader
// consuming the JSON should not have to know which enum values imply
// reproducibility. The one-word answer is in the document, and the enum
// explains it. A mismatch between the two is a hard error, so the redundancy
// can never drift into a contradiction.
type InputProvenance struct {
	Availability              string `json:"availability"`
	IndependentlyReproducible bool   `json:"independently_reproducible"`
	// Limitation states, in the report itself, WHY an outsider cannot
	// reproduce this block. Mandatory whenever the block is not
	// independently reproducible: an unexplained "false" is only marginally
	// better than no mark, because the reader cannot tell whether the input
	// is private, expired, or merely undocumented.
	Limitation string `json:"limitation,omitempty"`
	// Procedure names the documented, pinned procedure that obtains the
	// inputs. Mandatory for pinned-procedure availability — a procedure that
	// is not named is not documented, and FR-030 asks for a documented one.
	Procedure string `json:"procedure,omitempty"`
}

// PrivateRecordingInputs marks a block as built on one operator's own recorded
// sessions (T056/FR-030). It is a constructor rather than a struct literal so
// the not-reproducible flag cannot be forgotten at a call site: the whole
// value of this mark is that it is never omitted on the one block that needs
// it most.
func PrivateRecordingInputs(limitation string) *InputProvenance {
	return &InputProvenance{
		Availability:              InputAvailabilityPrivateRecording,
		IndependentlyReproducible: false,
		Limitation:                limitation,
	}
}

// ReproducibleInputs marks a block whose inputs an outsider can obtain, either
// from the repository or by following the named pinned procedure.
func ReproducibleInputs(availability, procedure string) *InputProvenance {
	return &InputProvenance{
		Availability:              availability,
		IndependentlyReproducible: true,
		Procedure:                 procedure,
	}
}

// Validate reports whether the mark is internally consistent. block names the
// report block for the error message.
func (p *InputProvenance) Validate(block string) error {
	if p == nil {
		return nil
	}
	switch p.Availability {
	case InputAvailabilityRepository, InputAvailabilityPinnedProcedure, InputAvailabilityPrivateRecording:
	default:
		return fmt.Errorf("%s block: inputs.availability %q not in {%s,%s,%s}",
			block, p.Availability, InputAvailabilityRepository,
			InputAvailabilityPinnedProcedure, InputAvailabilityPrivateRecording)
	}
	wantReproducible := p.Availability != InputAvailabilityPrivateRecording
	if p.IndependentlyReproducible != wantReproducible {
		return fmt.Errorf("%s block: inputs.availability %q implies independently_reproducible=%t, got %t",
			block, p.Availability, wantReproducible, p.IndependentlyReproducible)
	}
	if !p.IndependentlyReproducible && p.Limitation == "" {
		return fmt.Errorf("%s block: inputs are not independently reproducible but state no limitation", block)
	}
	if p.Availability == InputAvailabilityPinnedProcedure && p.Procedure == "" {
		return fmt.Errorf("%s block: pinned-procedure inputs name no procedure", block)
	}
	return nil
}

// Run completeness (FR-032). A run is one harness invocation producing one
// report; it is complete only when every planned cell either produced a figure
// or was skipped for a stated reason.
const (
	RunCompletenessComplete = "complete"
	RunCompletenessPartial  = "partial"
)

// RunStatus is the FR-032 mark (T058): whether this report covers the whole
// comparison it set out to make.
//
// Why the three counts rather than one boolean. A cell can be absent for two
// very different reasons, and collapsing them would make the mark useless. A
// DELIBERATE skip — code_exec skipped because code execution is disabled — is
// accounted for, expected, and does not make the comparison partial; the
// harness already reports such cells as skip rows naming what they collapse
// onto. An UNEXPLAINED gap is a cell the run was supposed to measure and did
// not, which is exactly the subset-presented-as-the-whole failure FR-032
// exists to catch. Planned − measured − skipped is that gap, and MissingCells
// names it, because "5 of 6" tells a reader nothing about which comparison
// they are missing.
//
// Interrupted is separate again: a run killed during the fourth repetition of
// the last cell may have touched every cell and still be partial, since the
// per-cell figures are averages over a repetition count the operator chose.
type RunStatus struct {
	Completeness string `json:"completeness"`
	// Reason is mandatory on a partial run. A partial with no reason is the
	// same silence as no mark at all — the reader learns something is
	// missing but not whether it matters.
	Reason      string `json:"reason,omitempty"`
	Interrupted bool   `json:"interrupted,omitempty"`

	CellsPlanned  int      `json:"cells_planned"`
	CellsMeasured int      `json:"cells_measured"`
	CellsSkipped  int      `json:"cells_skipped,omitempty"`
	MissingCells  []string `json:"missing_cells,omitempty"`
}

// DeriveRunStatus computes the completeness label from what the run actually
// did, rather than accepting one the caller asserts.
//
// This direction matters. A producer that stamps its own "complete" stamps it
// from the same optimism that lost the cells; a label derived from the planned
// set and the measured set cannot disagree with them. Validate then re-checks
// the same arithmetic on the way out, so a status that was hand-edited (or
// deserialized from somewhere else) is caught at emission time too.
//
// planned, measured and skipped are cell ids. reason is optional: when the run
// turns out to be partial and the caller supplied none, a factual one is
// synthesized, because a partial run must never reach the report unexplained.
func DeriveRunStatus(planned, measured, skipped []string, interrupted bool, reason string) *RunStatus {
	accounted := make(map[string]bool, len(measured)+len(skipped))
	for _, id := range measured {
		accounted[id] = true
	}
	for _, id := range skipped {
		accounted[id] = true
	}
	// Iterate over planned, not over the maps: the missing list must be
	// deterministic (FR-010/SC-002) and in the operator's own cell order.
	missing := make([]string, 0, len(planned))
	for _, id := range planned {
		if !accounted[id] {
			missing = append(missing, id)
		}
	}

	status := &RunStatus{
		Completeness:  RunCompletenessComplete,
		Interrupted:   interrupted,
		CellsPlanned:  len(planned),
		CellsMeasured: len(measured),
		CellsSkipped:  len(skipped),
		Reason:        reason,
	}
	if len(missing) > 0 {
		status.MissingCells = missing
	}
	if len(missing) > 0 || interrupted {
		status.Completeness = RunCompletenessPartial
		if status.Reason == "" {
			switch {
			case len(missing) > 0 && interrupted:
				status.Reason = fmt.Sprintf("run was interrupted; %d planned cell(s) unmeasured and unaccounted for: %s",
					len(missing), strings.Join(missing, ", "))
			case len(missing) > 0:
				status.Reason = fmt.Sprintf("%d planned cell(s) unmeasured and unaccounted for: %s",
					len(missing), strings.Join(missing, ", "))
			default:
				status.Reason = "run was interrupted before it finished"
			}
		}
	}
	return status
}

// IsPartial reports whether this run may not be published as a complete
// comparison. An unset status is NOT treated as complete here — callers that
// need that distinction check for nil themselves, and PublicationCheck does.
func (s *RunStatus) IsPartial() bool {
	return s != nil && s.Completeness == RunCompletenessPartial
}

// Validate re-derives the completeness label from the counts and refuses a
// status that claims more than the counts support. The interesting case is a
// hand-stamped "complete" over an unexplained gap: that is the exact document
// FR-032 forbids, and it is well-formed JSON, so nothing else would catch it.
func (s *RunStatus) Validate() error {
	if s == nil {
		return nil
	}
	switch s.Completeness {
	case RunCompletenessComplete, RunCompletenessPartial:
	default:
		return fmt.Errorf("run_status: completeness %q not in {%s,%s}",
			s.Completeness, RunCompletenessComplete, RunCompletenessPartial)
	}
	if s.CellsPlanned <= 0 {
		return fmt.Errorf("run_status: cells_planned is %d — a run that planned nothing compares nothing", s.CellsPlanned)
	}
	if s.CellsMeasured < 0 || s.CellsSkipped < 0 {
		return fmt.Errorf("run_status: negative cell counts (measured=%d skipped=%d)", s.CellsMeasured, s.CellsSkipped)
	}
	gap := s.CellsPlanned - s.CellsMeasured - s.CellsSkipped
	if gap < 0 {
		return fmt.Errorf("run_status: measured(%d)+skipped(%d) exceeds planned(%d)",
			s.CellsMeasured, s.CellsSkipped, s.CellsPlanned)
	}
	if gap > 0 && len(s.MissingCells) == 0 {
		return fmt.Errorf("run_status: %d planned cell(s) unaccounted for but missing_cells is empty — name them", gap)
	}
	mustBePartial := gap > 0 || s.Interrupted
	if mustBePartial && s.Completeness != RunCompletenessPartial {
		return fmt.Errorf("run_status: claims %q while %d cell(s) are unaccounted for (interrupted=%t) — that is a subset presented as the whole",
			s.Completeness, gap, s.Interrupted)
	}
	if s.Completeness == RunCompletenessComplete && len(s.MissingCells) > 0 {
		return fmt.Errorf("run_status: claims %q but lists %d missing cell(s)", s.Completeness, len(s.MissingCells))
	}
	if s.Completeness == RunCompletenessPartial && s.Reason == "" {
		return fmt.Errorf("run_status: partial run states no reason")
	}
	return nil
}

// Raw per-run record retention (FR-029).
const (
	// RecordsRetained means the artifact exists in this run's output
	// directory, at RunLocalPath, right now.
	RecordsRetained = "retained"
	// RecordsNotRetained means there is no artifact to point at. It is the
	// honest terminal state of a reference whose file is gone, and it is
	// never a dangling path.
	RecordsNotRetained = "not_retained"
)

// RecordsNotDurableNote is the standing caveat on every retained reference.
// The records live under the gitignored bench/results/ tree — the same tree a
// results cleanup empties, and the same tree SC-011 forbids committing — so
// "retained" means retained in THIS run directory, on THIS machine, until
// someone clears it. Anything stronger would be a promise the harness cannot
// keep.
const RecordsNotDurableNote = "Run-local: retained under the gitignored bench/results/ tree and NOT durable across a results cleanup. Copy it elsewhere before relying on it."

// RawRecordsRef is the report-side reference to a block's raw per-run records
// (T057/FR-029), so a headline figure can be traced back to its inputs without
// embedding any recorded content in the report (FR-006).
//
// The path is RUN-LOCAL — relative to the directory the report itself was
// written into — for two reasons. An absolute path would carry the operator's
// filesystem layout into a document that may be published, and it would dangle
// on every other machine, which is worse than saying nothing. A relative path
// resolves for anyone holding the run directory and resolves nowhere else,
// which is exactly the scope of the claim.
//
// Durable is always false and Validate enforces it. Like
// InputProvenance.IndependentlyReproducible, the redundancy with the note is
// deliberate: a renderer keys off the boolean, a human reads the note, and
// neither has to infer the other.
type RawRecordsRef struct {
	Retention string `json:"retention"`
	// RunLocalPath is relative to the report's own directory, and empty
	// whenever Retention is not_retained — a not-retained reference that
	// still carries a path IS the dangling reference this type replaces.
	RunLocalPath string `json:"run_local_path,omitempty"`
	Records      int    `json:"records,omitempty"`
	Durable      bool   `json:"durable"`
	// Note carries RecordsNotDurableNote on a retained reference, or the
	// reason nothing is retained on a degraded one.
	Note string `json:"note"`
}

// RetainedRecords references a per-run artifact written into this run's own
// output directory. runLocalPath must be relative to that directory.
func RetainedRecords(runLocalPath string, records int) *RawRecordsRef {
	return &RawRecordsRef{
		Retention:    RecordsRetained,
		RunLocalPath: runLocalPath,
		Records:      records,
		Durable:      false,
		Note:         RecordsNotDurableNote,
	}
}

// NotRetainedRecords is the degraded reference: no artifact, a stated reason,
// and deliberately no path. FR-029 wants a traceable figure; when the trace
// does not exist, the report says the trace does not exist.
func NotRetainedRecords(reason string) *RawRecordsRef {
	return &RawRecordsRef{
		Retention: RecordsNotRetained,
		Durable:   false,
		Note:      reason,
	}
}

// Validate enforces the run-local, non-durable, never-dangling shape.
func (r *RawRecordsRef) Validate(block string) error {
	if r == nil {
		return nil
	}
	if r.Durable {
		return fmt.Errorf("%s block: records.durable is true — nothing under bench/results/ survives a cleanup", block)
	}
	if r.Note == "" {
		return fmt.Errorf("%s block: records reference carries no note", block)
	}
	if r.Records < 0 {
		return fmt.Errorf("%s block: records count is negative (%d)", block, r.Records)
	}
	switch r.Retention {
	case RecordsRetained:
		if r.RunLocalPath == "" {
			return fmt.Errorf("%s block: records are retained but no run-local path is given", block)
		}
		if filepath.IsAbs(r.RunLocalPath) || strings.HasPrefix(r.RunLocalPath, "/") {
			return fmt.Errorf("%s block: records path %q is absolute — the reference must be run-local, not a path on the operator's machine",
				block, r.RunLocalPath)
		}
		// Cleaned rather than string-matched: "a/../../x" escapes too, and a
		// reference that points outside the run directory is not run-local
		// whatever it looks like.
		if cleaned := filepath.Clean(r.RunLocalPath); cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%s block: records path %q escapes the run directory", block, r.RunLocalPath)
		}
	case RecordsNotRetained:
		if r.RunLocalPath != "" {
			return fmt.Errorf("%s block: records are not retained but the reference still points at %q — that is the dangling reference it must degrade away from",
				block, r.RunLocalPath)
		}
	default:
		return fmt.Errorf("%s block: records.retention %q not in {%s,%s}",
			block, r.Retention, RecordsRetained, RecordsNotRetained)
	}
	return nil
}

// resolve degrades a retained reference whose artifact is not present in
// reportDir. This runs at write time because that is the only moment the
// report and its records directory are both in hand — and because the report
// is written into the very tree that gets cleaned, a reference that was
// accurate when the block was built can already be stale by the time the
// document is serialized.
//
// Degrading is not a fallback for a bug; it is the specified behaviour. A
// reader who follows a dangling path concludes either that the harness is
// broken or that the figure was never traceable, and both conclusions are
// worse than the truth, which is that the records were cleaned away.
func (r *RawRecordsRef) resolve(reportDir string) {
	if r == nil || r.Retention != RecordsRetained || r.RunLocalPath == "" {
		return
	}
	full := filepath.Join(reportDir, filepath.Clean(r.RunLocalPath))
	if _, err := os.Stat(full); err != nil {
		missing := r.RunLocalPath
		r.Retention = RecordsNotRetained
		r.RunLocalPath = ""
		r.Records = 0
		r.Note = fmt.Sprintf("records not retained: %q is absent from this run directory (a bench/results cleanup removes it)", missing)
	}
}

// PublicationDecision is the answer to "may this report back a published
// claim?" — a blocker list and a caveat list, both prose meant for the human
// about to publish.
//
// The two lists are not severity levels of one thing. A BLOCKER says the
// document cannot honestly support a published claim at all. A CAVEAT says it
// can, but something must travel with it: notably that a private-recording
// figure stays marked as not independently reproducible even in a report that
// publishes perfectly well. A caveat that quietly disappears once the blocker
// is resolved would defeat the mark entirely, so publication keeps them.
//
// The decision is derived on demand and deliberately NOT stored in the report.
// A stored verdict is a verdict that can go stale against the blocks beside
// it, and a stale "publishable: true" is the single most dangerous field this
// document could carry.
type PublicationDecision struct {
	Publishable bool     `json:"publishable"`
	Blockers    []string `json:"blockers,omitempty"`
	Caveats     []string `json:"caveats,omitempty"`
}

// PublicationCheck applies FR-030 and FR-032 to a finished report.
//
// It is strict about ABSENCE in a way ValidateAdditiveBlocks deliberately is
// not. Everywhere else in this harness an unset optional field means "this run
// did not produce that", which is fine; at the publication boundary it means
// "nobody said", and "nobody said" must not read as "fine". Undeclared run
// completeness, undeclared input availability and an undeclared records
// reference are each blockers here, while a report that never calls this
// function is unaffected — which is what keeps the addition additive.
//
// The sole-support rule is the substance of FR-030's second sentence. Marking
// a private-recording figure is necessary but not sufficient: a report whose
// EVERY block rests on a private recording is a published claim supported by
// nothing an outsider can check, however honestly each block is labelled. One
// block backed by reproducible inputs is what turns the private figure into
// corroborating detail.
func (r *ReportV2) PublicationCheck() PublicationDecision {
	var blockers, caveats []string

	// 1. FR-032: is this a complete comparison at all?
	if r.RunStatus == nil {
		blockers = append(blockers, "run completeness is not declared: a report that does not say whether its run finished cannot be published as a complete comparison (FR-032)")
	} else {
		if err := r.RunStatus.Validate(); err != nil {
			blockers = append(blockers, "run status is malformed: "+err.Error())
		}
		if r.RunStatus.IsPartial() {
			blockers = append(blockers, fmt.Sprintf(
				"run is PARTIAL (%s): a partial or interrupted run must not be published as a complete comparison (FR-032)",
				r.RunStatus.Reason))
		}
	}

	// 2. FR-030: what can an outsider actually reproduce? Blocks are walked
	// in a fixed order so the decision is deterministic.
	type blockInputs struct {
		name    string
		present bool
		inputs  *InputProvenance
		records *RawRecordsRef
		// hasRecords marks a block that produces per-run records at all;
		// payload decomposition is pure arithmetic over a committed corpus
		// and has none to retain.
		hasRecords bool
	}
	blocks := []blockInputs{
		{"replay", r.Replay != nil, nil, nil, true},
		{"agent_loop", r.AgentLoop != nil, nil, nil, true},
		{"payload_decomposition", r.PayloadDecomposition != nil, nil, nil, false},
	}
	if r.Replay != nil {
		blocks[0].inputs, blocks[0].records = r.Replay.Inputs, r.Replay.Records
	}
	if r.AgentLoop != nil {
		blocks[1].inputs, blocks[1].records = r.AgentLoop.Inputs, r.AgentLoop.Records
	}
	if r.PayloadDecomposition != nil {
		blocks[2].inputs = r.PayloadDecomposition.Inputs
	}

	var present, reproducible int
	for _, b := range blocks {
		if !b.present {
			continue
		}
		present++
		if b.inputs == nil {
			blockers = append(blockers, fmt.Sprintf(
				"%s block: input availability is not declared — an unset field is not a reproducible input (FR-030)", b.name))
		} else if err := b.inputs.Validate(b.name); err != nil {
			blockers = append(blockers, err.Error())
			continue
		} else if b.inputs.IndependentlyReproducible {
			reproducible++
		} else {
			caveats = append(caveats, fmt.Sprintf(
				"%s block is not independently reproducible: %s", b.name, b.inputs.Limitation))
		}

		// FR-029: a headline must be traceable to its inputs. "Not retained"
		// is an acceptable answer; no answer is not.
		if b.hasRecords {
			// Validate is nil-safe, so the nil case below is about the
			// MEANING of nil (nobody said) rather than about safety.
			err := b.records.Validate(b.name)
			switch {
			case b.records == nil:
				blockers = append(blockers, fmt.Sprintf(
					"%s block: no raw-record reference — a headline figure must be traceable to its per-run records (FR-029)", b.name))
			case err != nil:
				blockers = append(blockers, err.Error())
			case b.records.Retention == RecordsNotRetained:
				caveats = append(caveats, fmt.Sprintf(
					"%s block: raw per-run records are not retained (%s) — its figures cannot be traced to their inputs", b.name, b.records.Note))
			}
		}
	}

	switch {
	case present == 0:
		blockers = append(blockers, "report carries no measured block to publish")
	case reproducible == 0:
		blockers = append(blockers, "every figure in this report rests on inputs an outsider cannot obtain: a private-recording figure must never be the sole support for a published claim (FR-030)")
	}

	return PublicationDecision{
		Publishable: len(blockers) == 0,
		Blockers:    blockers,
		Caveats:     caveats,
	}
}
