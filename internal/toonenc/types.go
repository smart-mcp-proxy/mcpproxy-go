package toonenc

// NotTabularReason explains why a block did not classify as tabular-uniform
// (data-model.md §2). Set only when Classification.Tabular is false.
type NotTabularReason string

const (
	// ReasonNotJSON — the block text did not parse as a single JSON value
	// (plain text, base64, binary, malformed JSON, trailing garbage).
	ReasonNotJSON NotTabularReason = "not-json"
	// ReasonNotArray — the parsed value is not an array (and not a
	// single-key envelope object wrapping an array).
	ReasonNotArray NotTabularReason = "not-array"
	// ReasonTooFewRows — the array has fewer than 4 elements (empty
	// included).
	ReasonTooFewRows NotTabularReason = "too-few-rows"
	// ReasonNonObjectElements — at least one array element is not a JSON
	// object.
	ReasonNonObjectElements NotTabularReason = "non-object-elements"
	// ReasonNestedValues — at least one row field holds a nested object or
	// array (v1 is flat-scalar-only, FR-003b).
	ReasonNestedValues NotTabularReason = "nested-values"
	// ReasonTooRagged — the rows disagree beyond the 90% key-presence
	// tolerance, or the union key set collapses to empty.
	ReasonTooRagged NotTabularReason = "too-ragged"
	// ReasonNonRoundtrippableNumber — the value contains a JSON number whose
	// literal does not survive the float64 round-trip toon-go applies to
	// json.Number (e.g. integers beyond 2^53, large uint64). Encoding would
	// silently corrupt the number, so the block passes through (FR-004/FR-006
	// no data loss). Not an encoder fault — it belongs to the
	// passthrough-not-tabular family, never logged or counted.
	ReasonNonRoundtrippableNumber NotTabularReason = "non-roundtrippable-number"
)

// Classification is the deterministic result of the tabular-uniform predicate
// (FR-003b, data-model.md §2).
type Classification struct {
	Tabular  bool
	Envelope bool             // true if unwrapped from a single-key object envelope
	Rows     int              // element count of the classified array
	Cols     int              // size of the >=90%-present union key set
	Reason   NotTabularReason // set only when Tabular == false
}

// Outcome is the per-block encoding decision outcome (data-model.md §3).
type Outcome string

const (
	// OutcomeEncoded — the block was replaced with Marker + "\n" + TOON body.
	OutcomeEncoded Outcome = "encoded"
	// OutcomePassthroughNotTabular — non-JSON input, or (adaptive mode) a
	// JSON value that did not classify tabular-uniform. Ordinary traffic,
	// never logged.
	OutcomePassthroughNotTabular Outcome = "passthrough-not-tabular"
	// OutcomePassthroughBelowThreshold — the encoding did not beat the
	// passthrough emission by the configured margin, or the truncation
	// budget was too small to hold marker + one data row.
	OutcomePassthroughBelowThreshold Outcome = "passthrough-below-threshold"
	// OutcomePassthroughError — a genuine encoder failure on input that
	// already parsed as JSON. The only outcome the caller logs and counts
	// (FR-006).
	OutcomePassthroughError Outcome = "passthrough-error"
)

// Decision is the per-text-block outcome record — feeds the tool_call
// activity metadata (FR-010) and the spec-083 profiler (FR-012).
type Decision struct {
	// BlockIndex is the text-block index within a multi-block response.
	// EncodeBlock leaves it zero; the caller (server seam / bench arm) sets it.
	BlockIndex int
	Mode       Mode
	// Classification is always computed for JSON-parseable input, even in
	// always mode where it does not gate encoding (informational).
	Classification Classification
	// PassthroughEmissionBytes is len(original block text) — the exact
	// passthrough emission the agent would receive with the feature off
	// (FR-003c).
	PassthroughEmissionBytes int
	// EncodedEmissionBytes is len(Marker + "\n" + toonBody) — the complete
	// encoded emission. Zero on every passthrough outcome.
	EncodedEmissionBytes int
	// ThresholdPct is the toon_min_savings_pct in effect for this decision.
	ThresholdPct int
	Outcome      Outcome
}
